package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/sim"
)

const automaticBodyLimit = 8192

// NewAutomaticHandler binds the private host API to one V2 service. It serves
// GET /state and POST /commands after the host explicitly enables automation.
func NewAutomaticHandler(service *Service, credentials *auth.Authenticator) http.Handler {
	if service == nil || credentials == nil {
		panic("automatic command service and credentials are required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var method string
		switch r.URL.Path {
		case "/state":
			method = http.MethodGet
		case "/commands":
			method = http.MethodPost
		default:
			http.NotFound(w, r)
			return
		}
		if r.Method != method {
			w.Header().Set("Allow", method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		values := r.Header.Values("Authorization")
		if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx, err := credentials.Authenticate(r.Context(), strings.TrimPrefix(values[0], "Bearer "))
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if method == http.MethodGet {
			view, err := service.ReadAutomaticState(ctx)
			if err != nil {
				status, message := automaticHTTPError(err)
				writeAutomaticJSON(w, status, automaticHTTPResponse{Error: message})
				return
			}
			writeAutomaticJSON(w, http.StatusOK, view)
			return
		}

		command, decodeErr := decodeAutomaticRequest(w, r)
		if decodeErr != nil {
			// Audit authenticated malformed attempts without saving the request body.
			command = AutomaticCommand{RuntimeID: service.RuntimeID()}
		}
		result, err := service.ExecuteAutomatic(ctx, command)
		status, message := automaticHTTPError(err)
		var tooLarge *http.MaxBytesError
		if status == http.StatusBadRequest && errors.As(decodeErr, &tooLarge) {
			status, message = http.StatusRequestEntityTooLarge, "automatic command body too large"
		}
		writeAutomaticJSON(w, status, automaticHTTPResponse{Receipt: &result, Error: message})
	})
}

type automaticHTTPResponse struct {
	Receipt *AutomaticResult `json:"receipt,omitempty"`
	Error   string           `json:"error,omitempty"`
}

func writeAutomaticJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func automaticHTTPError(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, ErrUnauthorized), errors.Is(err, ErrAutomaticDisabled):
		return http.StatusForbidden, "automatic command access denied"
	case errors.Is(err, ErrPersistence), errors.Is(err, ErrPoisoned):
		return http.StatusServiceUnavailable, "automatic command runtime unavailable"
	case errors.Is(err, ErrAutomaticCommand), errors.Is(err, sim.ErrInvalidAssignmentBatch):
		return http.StatusBadRequest, "invalid automatic command"
	case errors.Is(err, ErrAutomaticExpired), errors.Is(err, ErrObservation):
		return http.StatusConflict, "automatic command observation is stale"
	case errors.Is(err, ErrAutomaticKeyConflict):
		return http.StatusConflict, "automatic command key conflict"
	default:
		return http.StatusConflict, "automatic command rejected"
	}
}

func decodeAutomaticRequest(w http.ResponseWriter, r *http.Request) (AutomaticCommand, error) {
	var command AutomaticCommand
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return command, ErrAutomaticCommand
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, automaticBodyLimit))
	if err != nil {
		return command, err
	}
	if !uniqueAutomaticJSON(json.NewDecoder(bytes.NewReader(body)), 0) || DecodeAgentJSON(string(body), &command) != nil {
		return command, ErrAutomaticCommand
	}
	return command, nil
}

func uniqueAutomaticJSON(decoder *json.Decoder, depth int) bool {
	if depth > 8 {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch token {
	case json.Delim('{'):
		keys := make(map[string]bool)
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			key = strings.ToLower(key)
			if err != nil || !ok || utf8.RuneCountInString(key) != len(key) || keys[key] || !uniqueAutomaticJSON(decoder, depth+1) {
				return false
			}
			keys[key] = true
		}
	case json.Delim('['):
		for decoder.More() {
			if !uniqueAutomaticJSON(decoder, depth+1) {
				return false
			}
		}
	default:
		return true
	}
	_, err = decoder.Token()
	return err == nil
}
