//go:build fleetops_test

package fleet_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/graphql/model"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestDemoScenariosPostgres(t *testing.T) {
	root := os.Getenv("FLEETOPS_P405_ROOT")
	if root == "" {
		t.Skip("requires built deterministic proposer")
	}
	pool := dashboardPool(t)
	a, raw := dashboardCredentials(t)
	var tokens map[string]string
	if err := json.Unmarshal([]byte(raw), &tokens); err != nil {
		t.Fatal(err)
	}
	f := demoFactory{pool: pool, auth: a, tokens: tokens, root: root, evidenceDir: t.TempDir()}
	run, err := f.create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := &demoController{run: run, ready: true, interactive: true, speed: "1", factory: f.create, selectFactory: f.createScenario, record: func(string, string) error { return nil }}
	defer func() {
		if err := c.close(); err != nil {
			t.Error(err)
		}
	}()
	pending := func() string {
		t.Helper()
		var id string
		if err := pool.QueryRow(context.Background(), `SELECT id FROM fleet_proposals WHERE state='pending' ORDER BY (payload->>'CreatedAt')::timestamptz DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	call := func(query string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"query": query})
		r := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-FleetOps-Generation", c.run.generation)
		r.Header.Set("Authorization", "Bearer "+tokens["operator"])
		w := httptest.NewRecorder()
		c.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		return w.Body.String()
	}
	oldID := pending()
	oldGeneration := c.run.generation
	for _, id := range []string{"routine-charging", "healthy-operations", "blocked-no-candidate", "blocked-reassignment"} {
		t.Run(id, func(t *testing.T) {
			before := c.run.generation
			w := controllerRequest(c, "/demo/scenario/"+id, before)
			if w.Code != 200 || c.run.generation == before || c.run.scenarioID != id || c.running {
				t.Fatal("switch failed", w.Body.String())
			}
			status, err := c.status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			initial := status.Snapshot
			e := status.Evidence
			if e == nil || e.Generation != c.run.generation || e.RuntimeID == "" || e.ScenarioID != id || !reflect.DeepEqual(e.Observation, initial) {
				t.Fatal("missing or mismatched original evidence")
			}
			if controllerRequest(c, "/demo/play", before).Code != 409 || controllerRequest(c, "/demo/scenario/healthy-operations", before).Code != 409 {
				t.Fatal("stale generation accepted")
			}
			rows := call(`{pendingProposals{id action currentRuntime}}`)
			actionable := id == "routine-charging" || id == "blocked-reassignment"
			if actionable {
				if e.ProposalID == "" {
					t.Fatal("proposal evidence not linked")
				}
				if status.State != "awaiting_decision" || !strings.Contains(rows, `"currentRuntime":true`) || stepRequest(c, c.run.generation, initial.Tick).Code != 409 {
					t.Fatal("missing decision gate", rows)
				}
				proposalID := pending()
				outcome := call(`mutation{approveProposal(id:"` + proposalID + `"){state execution auditCommitted executionAttempted error}}`)
				if !strings.Contains(outcome, `"execution":"succeeded"`) || !strings.Contains(outcome, `"auditCommitted":true`) {
					t.Fatal(outcome)
				}
			} else if status.State != "paused" || !strings.Contains(rows, `"pendingProposals":[]`) {
				t.Fatal("unnecessary proposal", rows, status.State)
			}
			if !actionable && e.ProposalID != "" {
				t.Fatal("fabricated no-action proposal evidence")
			}
			if w := stepRequest(c, c.run.generation, initial.Tick); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			if controllerRequest(c, "/demo/play", c.run.generation).Code != 200 {
				t.Fatal("play failed")
			}
			for _, speed := range []string{"0.5", "1", "2"} {
				if controllerRequest(c, "/demo/speed/"+speed, c.run.generation).Code != 200 {
					t.Fatal("speed failed")
				}
				if err := pulse(c, c.nextTick); err != nil {
					t.Fatal(err)
				}
			}
			if controllerRequest(c, "/demo/pause", c.run.generation).Code != 200 {
				t.Fatal("pause failed")
			}
			status, err = c.status(context.Background())
			if err != nil || status.Snapshot.Tick != initial.Tick+4 {
				t.Fatal("incorrect tick progression", err)
			}
			if !reflect.DeepEqual(status.Evidence.Observation, initial) || status.Evidence.Generation != e.Generation {
				t.Fatal("original evidence changed after ticks")
			}
			if id == "routine-charging" && (status.Snapshot.Robots[0].Battery <= initial.Robots[0].Battery || status.Snapshot.Robots[0].Status != "charging" || status.Snapshot.Stations[0].OccupiedBy != "R1") {
				t.Fatal("real charging effects missing")
			}
			if id == "healthy-operations" && reflect.DeepEqual(initial.Robots, status.Snapshot.Robots) {
				t.Fatal("healthy robots did not move")
			}
			if id == "blocked-reassignment" && status.Snapshot.Jobs[0].Status != string(sim.JobDone) {
				t.Fatal("blocked recovery did not deliver")
			}
			// Restart must preserve both scenario identity and the initial simulator state.
			if w := controllerRequest(c, "/demo/restart", c.run.generation); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			reset, err := c.status(context.Background())
			if err != nil || reset.Snapshot == nil || reset.ScenarioID != id {
				t.Fatal("restart changed selected scenario", err)
			}
			initialState, resetState := *initial, *reset.Snapshot
			if initialState.ObservationID == resetState.ObservationID {
				t.Fatal("restart reused the previous observation identity")
			}
			initialState.ObservationID, resetState.ObservationID = "", ""
			if !reflect.DeepEqual(initialState, resetState) {
				t.Fatal("restart changed the deterministic warehouse state")
			}
			if reset.Evidence == nil || reset.Evidence.Generation == e.Generation || reset.Evidence.RuntimeID == e.RuntimeID ||
				(actionable && reset.Evidence.ProposalID == e.ProposalID) {
				t.Fatal("old evidence reused after restart")
			}
			if !actionable { // Leave a running scenario for the next selection to drain.
				if controllerRequest(c, "/demo/play", c.run.generation).Code != 200 {
					t.Fatal("play before switch failed")
				}
			}
		})
	}
	if controllerRequest(c, "/graphql", oldGeneration).Code != 409 {
		t.Fatal("retired runtime reachable")
	}
	stale := call(`mutation{approveProposal(id:"` + oldID + `"){executionAttempted auditCommitted error}}`)
	if !strings.Contains(stale, `"executionAttempted":false`) || !strings.Contains(stale, `"auditCommitted":true`) {
		t.Fatal("old proposal executed", stale)
	}
	var proposals, runtimes, executions int
	if err := pool.QueryRow(context.Background(), `SELECT count(*), count(DISTINCT runtime_id) FROM fleet_proposals`).Scan(&proposals, &runtimes); err != nil || proposals != 5 || runtimes != 5 {
		t.Fatal("history or identity lost", proposals, runtimes, err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM fleet_audit WHERE attempt='execute' AND accepted`).Scan(&executions); err != nil || executions != 2 {
		t.Fatal("automatic execution or missing audits", executions, err)
	}
	if controllerRequest(c, "/demo/scenario/arbitrary", c.run.generation).Code != 400 {
		t.Fatal("arbitrary scenario accepted")
	}
	t.Log("PASS: four actual presets; charging and delivery; no-action inboxes; all speeds, Play/Pause/Step; fresh identities; selected restarts; pending/running switches; stale commands/proposals; historical PostgreSQL audits")
}

func TestDemoControllerScenarioDrainsTick(t *testing.T) {
	c := unitController()
	c.interactive = true
	c.running = true
	c.run.scenarioID = "healthy-operations"
	entered, release := make(chan struct{}), make(chan struct{})
	c.run.inspect = func(context.Context) (*model.FleetState, bool, error) { return &model.FleetState{}, true, nil }
	c.run.step = func(context.Context) error { close(entered); <-release; return nil }
	c.selectFactory = func(ctx context.Context, id string) (*demoRun, error) {
		next, err := c.factory(ctx)
		if err == nil {
			next.scenarioID = id
		}
		return next, err
	}
	tickDone := make(chan struct{})
	go func() { defer close(tickDone); _ = pulse(c, time.Now()) }()
	<-entered
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- controllerRequest(c, "/demo/scenario/routine-charging", c.run.generation) }()
	select {
	case <-done:
		t.Fatal("switch did not drain tick")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-tickDone
	select {
	case w := <-done:
		if w.Code != 200 || c.running {
			t.Fatal("switch failed", w.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("worker leaked")
	}
}

func TestDemoControllerSelectionFailsClosed(t *testing.T) {
	for _, phase := range []string{"capture", "factory", "identity", "scenario", "record"} {
		t.Run(phase, func(t *testing.T) {
			c := unitController()
			old := c.run
			old.scenarioID = "blocked-reassignment"
			c.running = true
			failure := errors.New("uncertain persistence")
			c.selectFactory = func(ctx context.Context, id string) (*demoRun, error) {
				if phase == "factory" {
					return nil, failure
				}
				if phase == "identity" {
					return old, nil
				}
				next, err := c.factory(ctx)
				next.scenarioID = id
				if phase == "scenario" {
					next.scenarioID = "healthy-operations"
				}
				return next, err
			}
			if phase == "capture" {
				c.run.capture = func(context.Context) error { return failure }
			}
			if phase == "record" {
				c.record = func(string, string) error { return failure }
			}
			if w := controllerRequest(c, "/demo/scenario/routine-charging", old.generation); w.Code != 503 || c.ready || c.running || c.run != old {
				t.Fatal("selection failure reused old run", w.Body.String())
			}
			if controllerRequest(c, "/graphql", old.generation).Code != 503 {
				t.Fatal("failed run accessible")
			}
		})
	}
}
