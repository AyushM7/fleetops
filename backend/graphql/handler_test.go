package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
)

type readOnlyFixture struct{ calls int }

func (f *readOnlyFixture) ReadState(context.Context) (fleet.StateView, error) {
	f.calls++
	return fleet.StateView{Robots: []fleet.RobotView{}, Stations: []fleet.StationView{}}, nil
}
func (*readOnlyFixture) ReadProposal(context.Context, string) (*fleet.ProposalView, error) {
	return nil, nil
}
func (*readOnlyFixture) Submit(context.Context, safety.SetChargingRequest, time.Time) (fleet.Result, error) {
	panic("unexpected mutation")
}
func (*readOnlyFixture) Decide(context.Context, string, proposal.Decision, proposal.OperatorIdentity) (fleet.Result, error) {
	panic("unexpected mutation")
}
func TestHTTPBoundaryAndLimits(t *testing.T) {
	a, tokens := authFixture(t)
	fixture := &readOnlyFixture{}
	h := NewHandler(fixture, a)
	for _, tc := range []struct {
		name, method, token, body string
		status                    int
		wantErrors                bool
	}{
		{"viewer read", "POST", tokens["viewer"], `{"query":"{fleetState{tick}}"}`, 200, false},
		{"missing auth", "POST", "", `{"query":"{fleetState{tick}}"}`, 401, false},
		{"invalid auth", "POST", strings.Repeat("x", 64), `{"query":"{fleetState{tick}}"}`, 401, false},
		{"GET disabled", "GET", tokens["viewer"], "", 405, false},
		{"body limit", "POST", tokens["viewer"], strings.Repeat(" ", 65537), 413, false},
		{"batch disabled", "POST", tokens["viewer"], `[{"query":"{fleetState{tick}}"}]`, 400, true},
		{"no tick bypass", "POST", tokens["operator"], `{"query":"mutation{tick}"}`, 422, true},
		{"no charging bypass", "POST", tokens["operator"], `{"query":"mutation{setCharging(robotId:\"R1\",stationId:\"S1\")}"}`, 422, true},
		{"multiple mutations", "POST", tokens["operator"], `{"query":"mutation{a:approveProposal(id:\"x\"){state} b:rejectProposal(id:\"x\"){state}}"}`, 200, true},
		{"deep fragments", "POST", tokens["viewer"], `{"query":"{... on Query {... on Query {... on Query {... on Query {... on Query {... on Query {... on Query {... on Query {... on Query {fleetState{tick}}}}}}}}}}}"}`, 200, true},
		{"many operations", "POST", tokens["viewer"], `{"query":"query A{fleetState{tick}} query B{fleetState{tick}}","operationName":"A"}`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/graphql", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code != tc.status || tc.wantErrors && !bytes.Contains(response.Body.Bytes(), []byte(`"errors"`)) {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
		})
	}
	if fixture.calls != 1 {
		t.Fatal("unsafe request reached service", fixture.calls)
	}
	schema := NewExecutableSchema(Config{Resolvers: &Resolver{Service: fixture}}).Schema()
	if len(schema.Mutation.Fields) != 3 || schema.Mutation.Fields.ForName("submitProposal") == nil || schema.Mutation.Fields.ForName("approveProposal") == nil || schema.Mutation.Fields.ForName("rejectProposal") == nil {
		t.Fatal("mutation surface changed")
	}
	readFields := 0
	for _, field := range schema.Query.Fields {
		if !strings.HasPrefix(field.Name, "__") {
			readFields++
		}
	}
	if readFields != 2 {
		t.Fatal("read surface changed")
	}
}
func TestMutationUncertaintyAndErrorRedaction(t *testing.T) {
	result := mutationResult(fleet.Result{Execution: fleet.Unknown, ExecutionAttempted: true}, errors.New("private connection token"))
	if result.Error == nil || strings.Contains(*result.Error, "private") || result.AuditCommitted || !result.ExecutionAttempted || result.Execution != "unknown" {
		t.Fatal(result)
	}
}

func TestPrevalidationBounds(t *testing.T) {
	for _, query := range []string{
		`{` + strings.Repeat(`fleetState { tick } `, 101) + `}`,
		`{...F} fragment F on Query { ...F }`,
		`{` + strings.Repeat(` `, 16384) + `fleetState{tick}}`,
		`{...A} fragment A on Query {...B ...B} fragment B on Query {...C ...C} fragment C on Query {...D ...D} fragment D on Query {...E ...E} fragment E on Query {...F ...F} fragment F on Query {fleetState{tick}}`,
	} {
		body, _ := json.Marshal(map[string]string{"query": query})
		if boundedDocument(body) {
			t.Fatal("unbounded document accepted")
		}
	}
}
