//go:build fleetops_test

package fleet_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/sim"
)

const reassignmentOutcome = `{proposalId state execution executionAttempted auditCommitted error}`

func TestIntegrationGraphQLReassignmentRecovery(t *testing.T) {
	for _, decision := range []string{"approve", "reject", "expire", "stale"} {
		t.Run(decision, func(t *testing.T) {
			now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
			s := fleet.NewReassignmentTestService(t, func() time.Time { return now })
			baseline := fleet.NewReassignmentTestService(t, func() time.Time { return now })
			tokens := map[string]string{}
			credentials := []auth.Credential{}
			for _, role := range []auth.Role{auth.Proposer, auth.Operator, auth.Viewer} {
				token := strings.Repeat(string(role), 12)
				tokens[string(role)] = token
				hash := sha256.Sum256([]byte(token))
				credentials = append(credentials, auth.Credential{Subject: string(role), Role: role, SHA256: hex.EncodeToString(hash[:])})
			}
			// Different role, same subject: independent approval must still reject it.
			tokens["self"] = strings.Repeat("self", 12)
			hash := sha256.Sum256([]byte(tokens["self"]))
			credentials = append(credentials, auth.Credential{Subject: "proposer", Role: auth.Operator, SHA256: hex.EncodeToString(hash[:])})
			raw, _ := json.Marshal(credentials)
			authenticator, err := auth.New(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			handler := graphql.NewHandler(s, authenticator)
			request := func(role, query string, variables map[string]any) map[string]any {
				t.Helper()
				raw, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
				r := httptest.NewRequest("POST", "/graphql", bytes.NewReader(raw))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer "+tokens[role])
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				var envelope struct {
					Data   map[string]any
					Errors []any
				}
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || w.Code != 200 || len(envelope.Errors) != 0 {
					t.Fatalf("response %d: %s (%v)", w.Code, w.Body.String(), err)
				}
				return envelope.Data
			}
			before := s.Snapshot()
			if !reflect.DeepEqual(before, baseline.Snapshot()) {
				t.Fatal("baseline and intervention must start from identical snapshots")
			}
			state := request("proposer", `{fleetState{tick jobs{id status assignedRobot} reassignmentCandidates{jobId fromRobotId toRobotId pickupSteps deliverySteps chargingExitSteps requiredBattery} activeIncidents{robotId type severity}}}`, nil)["fleetState"].(map[string]any)
			if len(state["reassignmentCandidates"].([]any)) == 0 || !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal("missing real candidates or read mutated state")
			}
			variables := map[string]any{"input": map[string]any{"jobId": "J1", "fromRobotId": "R1", "toRobotId": "R3", "expiresAt": now.Add(time.Minute).Format(time.RFC3339Nano)}}
			mutation := `mutation($input:ReassignUnstartedJobInput!){submitReassignment(input:$input)` + reassignmentOutcome + `}`
			viewer := request("viewer", mutation, variables)["submitReassignment"].(map[string]any)
			if viewer["error"] == nil || viewer["executionAttempted"] != false {
				t.Fatal("viewer submitted", viewer)
			}
			pending := request("proposer", mutation, variables)["submitReassignment"].(map[string]any)
			if pending["error"] != nil || pending["state"] != "pending" || pending["executionAttempted"] != false || !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal("submission changed simulator", pending)
			}
			id := pending["proposalId"].(string)
			if decision == "approve" && os.Getenv("FLEETOPS_AGENT_CONTRACT_CHECK") == "1" {
				server := httptest.NewServer(handler)
				defer server.Close()
				script, err := filepath.Abs("../../agent/scripts/check-reassignment-fixture.mjs")
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("node", script)
				cmd.Env = append(os.Environ(), "FLEETOPS_REASSIGNMENT_FIXTURE_URL="+server.URL, "FLEETOPS_REASSIGNMENT_FIXTURE_TOKEN="+tokens["proposer"])
				output, err := cmd.CombinedOutput()
				var result struct {
					ProposalID                    string
					Status                        string
					LocalModelCalls, PaidRequests int
				}
				if err != nil || json.Unmarshal(bytes.TrimSpace(output), &result) != nil || result.Status != "pending_approval" || result.ProposalID == "" || result.LocalModelCalls != 1 || result.PaidRequests != 0 {
					t.Fatalf("agent contract failed: %v\n%s", err, output)
				}
				id = result.ProposalID
				if !reflect.DeepEqual(before, s.Snapshot()) {
					t.Fatal("agent submission executed before approval")
				}
				t.Log("PASS: real Go incident/job/candidate HTTP data -> local Mastra generation -> grounding -> opted-in proposal; zero paid requests")
			}
			decide := func(role, action string) map[string]any {
				return request(role, `mutation($id:ID!){`+action+`(id:$id)`+reassignmentOutcome+`}`, map[string]any{"id": id})[action].(map[string]any)
			}
			for _, role := range []string{"viewer", "proposer", "self"} {
				if denied := decide(role, "approveProposal"); denied["error"] == nil || denied["executionAttempted"] != false {
					t.Fatal("unauthorized approval", denied)
				}
			}
			if !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal("denied approval changed state")
			}
			if decision == "expire" {
				now = now.Add(time.Minute)
			}
			if decision == "stale" { // Trusted test process changes the source before approval, through the sole command.
				// A separate approved proposal consumes the same source job.
				other := request("proposer", mutation, variables)["submitReassignment"].(map[string]any)
				approved := request("operator", `mutation($id:ID!){approveProposal(id:$id)`+reassignmentOutcome+`}`, map[string]any{"id": other["proposalId"]})["approveProposal"].(map[string]any)
				if approved["error"] != nil {
					t.Fatal(approved)
				}
				before = s.Snapshot()
			}
			action := "approveProposal"
			if decision == "reject" {
				action = "rejectProposal"
			}
			result := decide("operator", action)
			if decision != "approve" {
				if result["executionAttempted"] != false || !reflect.DeepEqual(before, s.Snapshot()) {
					t.Fatal("denied/rejected decision mutated state", result)
				}
				if decision != "reject" && result["error"] == nil {
					t.Fatal("stale/expired accepted", result)
				}
				return
			}
			if result["error"] != nil || result["execution"] != "succeeded" || result["executionAttempted"] != true || result["auditCommitted"] != true {
				t.Fatal(result)
			}
			after := s.Snapshot()
			if after.Robots[0].JobID != "" || after.Robots[2].JobID != "J1" || after.Jobs[0].AssignedRobot != "R3" || after.Jobs[0].Status != sim.JobAssigned || after.Tick != before.Tick {
				t.Fatal("non-atomic transfer", after)
			}
			if duplicate := decide("operator", "approveProposal"); duplicate["error"] == nil || duplicate["executionAttempted"] != false {
				t.Fatal("duplicate executed", duplicate)
			}
			for range 200 {
				if err := s.Tick(); err != nil {
					t.Fatal(err)
				}
				if err := baseline.Tick(); err != nil {
					t.Fatal(err)
				}
			}
			if s.Snapshot().Jobs[0].Status != sim.JobDone || baseline.Snapshot().Jobs[0].Status != sim.JobAssigned {
				t.Fatal("approved run did not beat automatic baseline")
			}
			view, err := s.ReadProposal(context.Background(), id)
			if err != nil || view.Execution != fleet.Succeeded || view.Proposal.Reassignment.JobID != "J1" {
				t.Fatal("persisted result", view, err)
			}
			t.Logf("GraphQL: tick-42 identical baseline; approval transferred J1 R1->R3 atomically; approved delivery completed, baseline still assigned at tick %d", s.Snapshot().Tick)
		})
	}
}
