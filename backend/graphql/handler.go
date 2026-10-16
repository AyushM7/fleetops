package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	gql "github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// NewHandler is injectable and supports only bounded authenticated JSON POSTs.
func NewHandler(service Service, credentials *auth.Authenticator) http.Handler {
	if service == nil || credentials == nil {
		panic("GraphQL service and authentication are required")
	}
	server := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{Service: service}}))
	server.AddTransport(transport.POST{})
	server.SetParserTokenLimit(1000)
	server.Use(extension.FixedComplexityLimit(200))
	server.SetRecoverFunc(func(context.Context, any) error { return gql.ErrorOnPath(context.Background(), errInternal) })
	server.AroundOperations(func(ctx context.Context, next gql.OperationHandler) gql.ResponseHandler {
		operation := gql.GetOperationContext(ctx)
		count := 0
		if len(operation.Doc.Operations) != 1 || !boundedSelections(operation.Operation.SelectionSet, operation.Doc, 0, &count) {
			return gql.OneShot(gql.ErrorResponse(ctx, "query limits exceeded"))
		}
		if operation.Operation.Operation == ast.Mutation && rootFields(operation.Operation.SelectionSet, operation.Doc, 0) != 1 {
			return gql.OneShot(gql.ErrorResponse(ctx, "one mutation field per request is required"))
		}
		return next(ctx)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", 405)
			return
		}
		values := r.Header.Values("Authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
			http.Error(w, "unauthorized", 401)
			return
		}
		ctx, err := credentials.Authenticate(r.Context(), strings.TrimPrefix(values[0], "Bearer "))
		if err != nil {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.ContentLength > 65536 {
			http.Error(w, "request too large", 413)
			return
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "request too large or unreadable", 413)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		// Bound fragment expansion before gqlgen validation and complexity traversal.
		if !boundedDocument(body) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"errors":[{"message":"query limits exceeded"}]}`))
			return
		}
		server.ServeHTTP(w, r)
	})
}

func boundedDocument(body []byte) bool {
	var request struct {
		Query string `json:"query"`
	}
	if json.Unmarshal(body, &request) != nil {
		return true
	} // Transport reports malformed JSON.
	if len(request.Query) > 16384 {
		return false
	}
	doc, err := parser.ParseQueryWithTokenLimit(&ast.Source{Input: request.Query}, 1000)
	if err != nil {
		return true
	} // gqlgen applies the same token limit and reports syntax errors.
	if len(doc.Operations) != 1 {
		return false
	}
	count := 0
	return boundedSelections(doc.Operations[0].SelectionSet, doc, 0, &count)
}

func boundedSelections(selections ast.SelectionSet, doc *ast.QueryDocument, depth int, count *int) bool {
	if depth > 8 {
		return false
	}
	for _, selection := range selections {
		*count++
		if *count > 100 {
			return false
		}
		var children ast.SelectionSet
		switch node := selection.(type) {
		case *ast.Field:
			children = node.SelectionSet
		case *ast.InlineFragment:
			children = node.SelectionSet
		case *ast.FragmentSpread:
			fragment := doc.Fragments.ForName(node.Name)
			if fragment == nil {
				return false
			}
			children = fragment.SelectionSet
		}
		if !boundedSelections(children, doc, depth+1, count) {
			return false
		}
	}
	return true
}
func rootFields(selections ast.SelectionSet, doc *ast.QueryDocument, depth int) int {
	if depth > 8 {
		return 2
	}
	count := 0
	for _, selection := range selections {
		switch node := selection.(type) {
		case *ast.Field:
			count++
		case *ast.InlineFragment:
			count += rootFields(node.SelectionSet, doc, depth+1)
		case *ast.FragmentSpread:
			count += rootFields(doc.Fragments.ForName(node.Name).SelectionSet, doc, depth+1)
		}
	}
	return count
}
