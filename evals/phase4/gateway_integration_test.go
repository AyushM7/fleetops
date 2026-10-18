//go:build fleetops_test

package fleet_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestDemoGatewayBackendIntegration(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()) }
	service := fleet.NewReassignmentTestService(t, now)
	a, raw := dashboardCredentials(t)
	var tokens map[string]string
	if err := json.Unmarshal([]byte(raw), &tokens); err != nil {
		t.Fatal(err)
	}
	backend := graphql.NewHandler(dashboardFixture{Service: service}, a)
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<head></head>"), 0600); err != nil {
		t.Fatal(err)
	}
	g, err := newDemoGateway(backend, tokens["operator"], dist)
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	g.origin = "http://127.0.0.1:12345"
	bootstrap := httptest.NewRequest("GET", g.origin+"/", nil)
	bootstrap.RemoteAddr = "127.0.0.1:50000"
	page := httptest.NewRecorder()
	g.ServeHTTP(page, bootstrap)
	cookie := page.Result().Cookies()[0]
	call := func(handler http.Handler, query string, variables map[string]any, proposer bool) map[string]any {
		t.Helper()
		body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", g.origin+"/graphql", bytes.NewReader(body))
		r.RemoteAddr = "127.0.0.1:50000"
		r.Header.Set("Content-Type", "application/json")
		if proposer {
			r.Header.Set("Authorization", "Bearer "+tokens["proposer"])
		} else {
			r.AddCookie(cookie)
			r.Header.Set("Origin", g.origin)
			r.Header.Set("X-FleetOps-Demo", "1")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var result struct {
			Data   map[string]any
			Errors []any
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Errors) != 0 {
			t.Fatal("GraphQL request failed", w.Code, w.Body.String())
		}
		for _, token := range tokens {
			if bytes.Contains(w.Body.Bytes(), []byte(token)) {
				t.Fatal("credential in response")
			}
		}
		return result.Data
	}
	submit := func() string {
		state := call(backend, `{fleetState{observationId}}`, nil, true)["fleetState"].(map[string]any)
		input := map[string]any{"jobId": "J1", "fromRobotId": "R1", "toRobotId": "R3", "expiresAt": now().Add(time.Minute).UTC().Format(time.RFC3339Nano), "expectedObservationId": state["observationId"]}
		data := call(backend, `mutation($input:ReassignUnstartedJobInput!){submitReassignment(input:$input){proposalId error auditCommitted}}`, map[string]any{"input": input}, true)
		row := data["submitReassignment"].(map[string]any)
		if row["error"] != nil || row["auditCommitted"] != true {
			t.Fatal("submit failed", row)
		}
		return row["proposalId"].(string)
	}
	decide := func(operation, id string) map[string]any {
		return call(g, `mutation($id:ID!){`+operation+`(id:$id){state execution executionAttempted auditCommitted error}}`, map[string]any{"id": id}, false)[operation].(map[string]any)
	}
	initial := service.Snapshot()
	rejected, approved, stale, expired := submit(), submit(), submit(), submit()
	if !reflect.DeepEqual(initial, service.Snapshot()) {
		t.Fatal("execution before approval")
	}
	if call(g, `{fleetState{tick} pendingProposals{id}}`, nil, false)["fleetState"].(map[string]any)["tick"] != float64(42) {
		t.Fatal("not real seeded warehouse")
	}
	r := decide("rejectProposal", rejected)
	if r["state"] != "rejected" || r["executionAttempted"] != false || !reflect.DeepEqual(initial, service.Snapshot()) {
		t.Fatal("unsafe rejection", r)
	}
	r = decide("approveProposal", rejected)
	if r["error"] == nil || r["executionAttempted"] != false {
		t.Fatal("rejected proposal executed", r)
	}
	r = decide("approveProposal", approved)
	if r["execution"] != "succeeded" || r["auditCommitted"] != true {
		t.Fatal("approval failed", r)
	}
	transferred := service.Snapshot()
	if transferred.Jobs[0].AssignedRobot != "R3" || transferred.Jobs[0].Status != sim.JobAssigned {
		t.Fatal("incorrect transfer")
	}
	for _, id := range []string{approved, stale} {
		r = decide("approveProposal", id)
		if r["error"] == nil || r["executionAttempted"] != false || !reflect.DeepEqual(transferred, service.Snapshot()) {
			t.Fatal("unsafe duplicate/stale request", r)
		}
	}
	clock.Add(int64(2 * time.Minute))
	r = decide("approveProposal", expired)
	if r["state"] != "expired" || r["executionAttempted"] != false {
		t.Fatal("unsafe expired proposal", r)
	}
	for range 3 {
		if err := service.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	if service.Snapshot().Jobs[0].Status != sim.JobDone {
		t.Fatal("R3 did not deliver")
	}
	pool, _ := demoAuditPool(t, approved)
	audits := demoAudits(t, pool, approved)
	if len(audits) < 3 || audits[0].Subject != "proposer" || audits[1].Subject != "operator" || audits[2].Subject != "operator" || !audits[2].Accepted {
		t.Fatal("independent committed audit missing", audits)
	}
	t.Log("PASS: cookie gateway + real Go/PostgreSQL; pending unchanged, reject, approve, duplicate, stale, expiry, R3 delivery and independent committed audits; automated, not human approval")
}
