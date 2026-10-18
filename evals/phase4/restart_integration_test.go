//go:build fleetops_test

package fleet_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDemoRestartIntegration(t *testing.T) {
	root := os.Getenv("FLEETOPS_P405_ROOT")
	if root == "" {
		t.Skip("built deterministic proposer requires FLEETOPS_P405_ROOT")
	}
	pool := dashboardPool(t)
	a, raw := dashboardCredentials(t)
	var tokens map[string]string
	if err := json.Unmarshal([]byte(raw), &tokens); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := demoFactory{pool: pool, auth: a, tokens: tokens, root: root, evidenceDir: dir}
	run, err := f.create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := &demoController{run: run, ready: true, factory: f.create, selectFactory: f.createScenario, record: func(g, outcome string) error {
		return writeDemoEvidence(dir, "lifecycle", map[string]string{"generation": g, "outcome": outcome})
	}}
	defer func() {
		if err := c.close(); err != nil {
			t.Error(err)
		}
	}()
	g, err := newDemoGateway(c, tokens["operator"], filepath.Join(root, "web", "dist"))
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()
	g.origin = "http://127.0.0.1:12345"
	r := httptest.NewRequest("GET", g.origin+"/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	cookie := w.Result().Cookies()[0]
	request := func(path, generation, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", g.origin+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:5000"
		r.AddCookie(cookie)
		r.Header.Set("Origin", g.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-FleetOps-Demo", "1")
		r.Header.Set("X-FleetOps-Generation", generation)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		return w
	}
	query := `{"query":"{fleetState{tick poisoned robots{id jobId status x y battery} jobs{id assignedRobot status}} pendingProposals{id currentRuntime state execution}}"}`
	initial := request("/graphql", run.generation, query)
	var first struct {
		Data struct {
			FleetState       json.RawMessage
			PendingProposals []struct{ ID string }
		}
	}
	if initial.Code != 200 || json.Unmarshal(initial.Body.Bytes(), &first) != nil || len(first.Data.PendingProposals) != 1 {
		t.Fatal(initial.Body.String())
	}
	oldID := first.Data.PendingProposals[0].ID
	decision := func(id, kind string) string {
		raw, _ := json.Marshal(map[string]any{"query": `mutation($id:ID!){` + kind + `Proposal(id:$id){state execution auditCommitted executionAttempted error}}`, "variables": map[string]string{"id": id}})
		return string(raw)
	}
	for i := 0; i < 3; i++ {
		oldGeneration := c.run.generation
		kind := "reject"
		if i == 1 {
			kind = "approve"
		}
		var id string
		if err := pool.QueryRow(context.Background(), `SELECT id FROM fleet_proposals ORDER BY (payload->>'CreatedAt')::timestamptz DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		result := request("/graphql", oldGeneration, decision(id, kind))
		if result.Code != 200 || !strings.Contains(result.Body.String(), `"auditCommitted":true`) {
			t.Fatal(result.Body.String())
		}
		if kind == "approve" {
			deadline := time.Now().Add(12 * time.Second)
			for {
				if err := c.advance(context.Background()); err != nil {
					t.Fatal(err)
				}
				state := request("/graphql", oldGeneration, query)
				if strings.Contains(state.Body.String(), `"tick":242`) && strings.Contains(state.Body.String(), `"status":"done"`) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("approved scenario failed to progress", state.Body.String())
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		w := request("/demo/restart", oldGeneration, "{}")
		if w.Code != 200 || c.run.generation == oldGeneration {
			t.Fatal("restart failed", w.Body.String())
		}
		if request("/graphql", oldGeneration, decision(id, "approve")).Code != 409 || request("/demo/restart", oldGeneration, "{}").Code != 409 {
			t.Fatal("stale generation accepted")
		}
		var current struct {
			Data struct {
				FleetState       json.RawMessage
				PendingProposals []struct {
					ID               string
					State, Execution string
					CurrentRuntime   bool
				}
			}
		}
		w = request("/graphql", c.run.generation, query)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &current) != nil || !reflect.DeepEqual(first.Data.FleetState, current.Data.FleetState) || len(current.Data.PendingProposals) != 1 {
			t.Fatal("fixture was not fresh", w.Body.String())
		}
		p := current.Data.PendingProposals[0]
		if p.ID == id || !p.CurrentRuntime || p.State != "pending" || p.Execution != "none" {
			t.Fatal("replacement auto-approved or reused proposal", p)
		}
	}
	w = request("/graphql", c.run.generation, decision(oldID, "approve"))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"executionAttempted":false`) || !strings.Contains(w.Body.String(), `"auditCommitted":true`) || strings.Contains(w.Body.String(), `"error":null`) {
		t.Fatal("stale ID executed", w.Body.String())
	}
	var count, runtimes int
	if err := pool.QueryRow(context.Background(), `SELECT count(*),count(DISTINCT runtime_id) FROM fleet_proposals`).Scan(&count, &runtimes); err != nil || count != 4 || runtimes != 4 {
		t.Fatal("historical records lost or runtime reused", count, runtimes, err)
	}
	var approved, executed int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE attempt='approve' AND accepted),count(*) FILTER (WHERE attempt='execute' AND accepted) FROM fleet_audit`).Scan(&approved, &executed); err != nil || approved != 1 || executed != 1 {
		t.Fatal("restart crossed human approval boundary", approved, executed, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) < 4 {
		t.Fatal("history not captured", err)
	}
	// Persistence unavailable: even after a decision, no replacement may be
	// published when full historical records cannot be captured.
	if _, err := pool.Exec(context.Background(), `ALTER TABLE fleet_audit RENAME TO unavailable_audit`); err != nil {
		t.Fatal(err)
	}
	restored := false
	defer func() {
		if restored {
			return
		}
		if _, err := pool.Exec(context.Background(), `ALTER TABLE unavailable_audit RENAME TO fleet_audit`); err != nil {
			t.Error(err)
		}
	}()
	var pendingID string
	if err := pool.QueryRow(context.Background(), `SELECT id FROM fleet_proposals WHERE state='pending' ORDER BY (payload->>'CreatedAt')::timestamptz DESC LIMIT 1`).Scan(&pendingID); err != nil {
		t.Fatal(err)
	}
	unknown := request("/graphql", c.run.generation, decision(pendingID, "approve"))
	if !strings.Contains(unknown.Body.String(), `"auditCommitted":false`) {
		t.Fatal("persistence failure was not exposed", unknown.Body.String())
	}
	if request("/demo/restart", c.run.generation, "{}").Code != 503 || c.ready {
		t.Fatal("capture failure advertised success")
	}
	if request("/graphql", c.run.generation, query).Code != 503 {
		t.Fatal("failed run remained reachable")
	}
	if _, err := pool.Exec(context.Background(), `ALTER TABLE unavailable_audit RENAME TO fleet_audit`); err != nil {
		t.Fatal(err)
	}
	restored = true
	poisonedGeneration := c.run.generation
	if request("/demo/scenario/healthy-operations", poisonedGeneration, "{}").Code != 200 || c.run.scenarioID != "healthy-operations" || c.run.generation == poisonedGeneration {
		t.Fatal("could not replace captured poisoned runtime")
	}
	var poisonCaptured bool
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "capture-") {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var capture struct{ Poisoned bool }
			if err := json.Unmarshal(raw, &capture); err != nil {
				return err
			}
			poisonCaptured = poisonCaptured || capture.Poisoned
		}
		return nil
	}); err != nil || !poisonCaptured {
		t.Fatal("uncertain history was not captured", err)
	}
	t.Log("PASS: three real restarts, fresh runtimes/proposals/tick-42 state, reject/approve, stale generations/IDs, committed historical audits, no automatic execution, failed capture closes access; deterministic adapter only, no paid calls")
}
