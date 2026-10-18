//go:build fleetops_test

package fleet_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestDemoControlsPostgresDecisions(t *testing.T) {
	for _, decision := range []string{"approve", "reject", "expired", "poisoned"} {
		t.Run(decision, func(t *testing.T) {
			pool := dashboardPool(t)
			now := time.Now()
			service, err := fleet.NewDemoReassignmentService(pool, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			a, raw := dashboardCredentials(t)
			var tokens map[string]string
			if err := json.Unmarshal([]byte(raw), &tokens); err != nil {
				t.Fatal(err)
			}
			handler := graphql.NewHandler(service, a)
			call := func(query, role string) string {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"query": query})
				r := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer "+tokens[role])
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatal(w.Body.String())
				}
				return w.Body.String()
			}
			observation, err := service.ReadState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			created := call(`mutation{submitReassignment(input:{jobId:"J1",fromRobotId:"R1",toRobotId:"R3",expiresAt:"`+now.Add(time.Minute).UTC().Format(time.RFC3339Nano)+`",expectedObservationId:"`+observation.ObservationID+`"}){proposalId error}}`, "proposer")
			var result struct {
				Data struct {
					SubmitReassignment struct {
						ProposalID string
						Error      *string
					}
				}
			}
			if json.Unmarshal([]byte(created), &result) != nil || result.Data.SubmitReassignment.ProposalID == "" || result.Data.SubmitReassignment.Error != nil {
				t.Fatal(created)
			}
			id := result.Data.SubmitReassignment.ProposalID
			scenario := &demoScenario{service: service, evidence: demoEvidence{ProposalID: id}}
			c := unitController()
			c.interactive = true
			c.run.inspect = scenario.inspect
			c.run.step = func(context.Context) error { return service.Tick() }
			g := c.run.generation
			initial := service.Snapshot()
			if controllerRequest(c, "/demo/play", g).Code != 409 || stepRequest(c, g, 42).Code != 409 {
				t.Fatal("pending decision bypassed")
			}
			kind := decision
			if decision == "expired" {
				now = now.Add(2 * time.Minute)
				kind = "approve"
			}
			if decision == "poisoned" {
				if _, err := pool.Exec(context.Background(), `ALTER TABLE fleet_audit RENAME TO controls_unavailable_audit`); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := pool.Exec(context.Background(), `ALTER TABLE controls_unavailable_audit RENAME TO fleet_audit`); err != nil {
						t.Error(err)
					}
				}()
				kind = "approve"
			}
			outcome := call(`mutation{`+kind+`Proposal(id:"`+id+`"){state execution auditCommitted executionAttempted error}}`, "operator")
			if service.Snapshot().Tick != 42 {
				t.Fatal("decision advanced time")
			}
			if decision == "expired" || decision == "poisoned" {
				if controllerRequest(c, "/demo/play", g).Code < 400 || stepRequest(c, g, 42).Code < 400 || !reflect.DeepEqual(initial, service.Snapshot()) {
					t.Fatal("expiry/poison bypassed", outcome)
				}
				return
			}
			if !strings.Contains(outcome, `"auditCommitted":true`) {
				t.Fatal(outcome)
			}
			if decision == "approve" && service.Snapshot().Jobs[0].Status == sim.JobDone {
				t.Fatal("approval fabricated delivery")
			}
			if decision == "reject" && !reflect.DeepEqual(initial, service.Snapshot()) {
				t.Fatal("rejection executed")
			}
			if w := stepRequest(c, g, 42); w.Code != 200 || service.Snapshot().Tick != 43 {
				t.Fatal(w.Body.String())
			}
			if w := controllerRequest(c, "/demo/play", g); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			for range 199 {
				if err := pulse(c, c.nextTick); err != nil {
					t.Fatal(err)
				}
			}
			state := service.Snapshot()
			if state.Tick != 242 {
				t.Fatal("incorrect real tick count")
			}
			if decision == "approve" && (state.Jobs[0].Status != sim.JobDone || state.Jobs[0].AssignedRobot != "R3") {
				t.Fatal("genuine delivery missing")
			}
			if decision == "reject" && (state.Jobs[0].Status != sim.JobAssigned || state.Jobs[0].AssignedRobot != "R1" || !state.Robots[0].Blocked) {
				t.Fatal("no-intervention path changed")
			}
			var executions int
			if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='execute' AND accepted`, id).Scan(&executions); err != nil {
				t.Fatal(err)
			}
			if (decision == "approve" && executions != 1) || (decision == "reject" && executions != 0) {
				t.Fatal("ticking executed command", executions)
			}
		})
	}
}
