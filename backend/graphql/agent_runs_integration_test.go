package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/AyushM7/fleetops/backend/fleet"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestIntegrationGraphQLAgentProvenance(t *testing.T) {
	f := integrationAPI(t)
	view, err := f.service.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	metadata := `{"version":"agent-run-v1","contract":"recovery-decision-v2","objective":"compare_tradeoffs","reportedSource":"mocked","requestedModel":"offline/test"}`
	start := fmt.Sprintf(`mutation{startAgentRun(expectedObservationId:%q,metadata:%q){id runtimeId observationId data}}`, view.ObservationID, metadata)
	response := f.request(t, "proposer", start, nil)
	data := response["startAgentRun"].(map[string]any)
	id := data["id"].(string)
	runtime := data["runtimeId"].(string)
	input := fleet.AgentFinish{RuntimeID: runtime, ObservationID: view.ObservationID, Completion: fleet.AgentCompletion{Status: "failed", Failure: "timeout", SubmissionCheck: "not_requested", Usage: fleet.AgentUsage{Status: "unavailable"}}}
	raw, _ := json.Marshal(input)
	finished := f.request(t, "proposer", fmt.Sprintf(`mutation{finishAgentRun(id:%q,completion:%q){runId recorded denial proposalResult{proposalId}}}`, id, string(raw)), nil)["finishAgentRun"].(map[string]any)
	if finished["recorded"] != true || finished["proposalResult"] != nil {
		t.Fatal(finished)
	}
	record := f.request(t, "proposer", fmt.Sprintf(`{agentRun(id:%q){data proposal{id}}}`, id), nil)["agentRun"].(map[string]any)
	if !strings.Contains(record["data"].(string), `"failure":"timeout"`) || record["proposal"] != nil {
		t.Fatal(record)
	}
	body := agentRawRequest(t, f, "viewer", fmt.Sprintf(`{agentRun(id:%q){data}}`, id))
	if !strings.Contains(body, "errors") {
		t.Fatal("viewer obtained private provenance", body)
	}
	if f.request(t, "operator", fmt.Sprintf(`{agentRun(id:%q){id}}`, id), nil)["agentRun"] == nil {
		t.Fatal("authorized operator cannot review record")
	}

	bad := fmt.Sprintf(`mutation{startAgentRun(expectedObservationId:%q,metadata:%q){id}}`, view.ObservationID, `{"apiKey":"sk-private"}`)
	body = agentRawRequest(t, f, "proposer", bad)
	if !strings.Contains(body, "errors") || strings.Contains(body, "sk-private") {
		t.Fatal(body)
	}
}

func agentRawRequest(t *testing.T, f *testAPI, role, query string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequest("POST", f.server.URL, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.tokens[role])
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
