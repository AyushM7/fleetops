package graphql

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestIntegrationPendingOperatorAuthorization(t *testing.T) {
	api := integrationAPI(t)
	id := api.submit(t, "proposer")["proposalId"].(string)
	query := `{pendingProposals{id action robotId stationId state execution submittedBy currentRuntime createdAt expiresAt}}`
	for _, role := range []string{"viewer", "proposer", "missing"} {
		t.Run(role, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]string{"query": query})
			req, err := http.NewRequest("POST", api.server.URL, bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if role != "missing" {
				req.Header.Set("Authorization", "Bearer "+api.tokens[role])
			}
			response, err := api.server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if role == "missing" {
				if response.StatusCode != 401 {
					t.Fatal("unauthenticated request accepted")
				}
				return
			}
			var body struct {
				Data   any
				Errors []struct {
					Message    string
					Extensions map[string]any
				}
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body.Errors) != 1 || body.Errors[0].Extensions["code"] != "FORBIDDEN" || body.Data != nil {
				t.Fatal("non-operator inbox access", body)
			}
		})
	}
	rows := api.request(t, "operator", query, nil)["pendingProposals"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != id || rows[0].(map[string]any)["submittedBy"] != "proposer" {
		t.Fatal("inbox did not use persisted proposal", rows)
	}
	api.decide(t, "operator", "rejectProposal", id)
	rows = api.request(t, "operator", query, nil)["pendingProposals"].([]any)
	if len(rows) != 0 {
		t.Fatal("decided proposal remained pending")
	}
	api.request(t, "viewer", `{fleetState{tick} proposal(id:"absent"){id}}`, nil)
}
