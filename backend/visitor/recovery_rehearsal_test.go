//go:build fleetops_test && fleetops_rehearsal

package visitor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/graphql/model"
	"github.com/AyushM7/fleetops/backend/operations"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
	"github.com/jackc/pgx/v5"
)

func TestCompiledV3NormalOperations(t *testing.T) {
	live := normalVerificationLive(t)
	entry := os.Getenv("FLEETOPS_TEST_V3_WORKER")
	if entry == "" {
		t.Fatal("FLEETOPS_TEST_V3_WORKER must point to the compiled worker")
	}
	type verificationCase struct {
		name       string
		tick       int
		loseSecond bool
		reject     bool
	}
	cases := []verificationCase{{name: "intervention", tick: 33}, {name: "abstention", tick: 1}, {name: "second_request_lost", tick: 33, loseSecond: true}}
	visuals := !live && os.Getenv("FLEETOPS_REHEARSAL_VISUALS") == "1"
	if visuals {
		cases = append(cases, verificationCase{name: "rejection", tick: 33, reject: true})
	}
	for _, tc := range cases {
		if live && tc.loseSecond {
			continue
		}
		passed := t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			pool := openRehearsalPool(t, ctx)
			a, tokens, _, err := visitorCredentials()
			if err != nil {
				t.Fatal(err)
			}
			var service *fleet.Service
			if tc.tick == 33 {
				service, err = fleet.NewVisitorWarehouse(99, pool)
			} else {
				cfg := sim.WarehouseV2Config()
				cfg.InitialJobs = 0
				cfg.ScheduledBreakdowns = []sim.ScheduledBreakdown{{Tick: 1, RobotID: "R4", Type: sim.FaultMotorFailure}}
				service, err = fleet.New(cfg, 99, pool, time.Now, func(context.Context, proposal.OperatorIdentity) bool { return false })
			}
			if err != nil {
				t.Fatal(err)
			}
			for range tc.tick {
				if err := service.Tick(); err != nil {
					t.Fatal(err)
				}
			}
			before := service.Snapshot()
			state, err := (&graphql.Resolver{Service: service}).Query().FleetState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			mock := newRecoveryRehearsal(t, state, tc.tick == 33)
			source := "mocked"
			var broker *operations.Manager
			if live {
				source = "live-model"
				broker, err = operations.Open(ctx, pool)
			} else {
				broker, err = operations.OpenRecoveryRehearsal(ctx, pool, entry, func(ctx context.Context, req []byte) ([]byte, error) {
					body, err := mock.reply(ctx, req)
					if tc.loseSecond && mock.sends == 2 {
						return nil, errors.New("fixture lost the second response")
					}
					return body, err
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			scenario := &demoScenario{service: service, factory: demoFactory{auth: a, tokens: tokens}}
			inputs := make(chan operations.RecoveryInput, 1)
			manager := &recoveryOperations{scenario: scenario, source: source, reviewed: make(map[string]bool), investigate: func(ctx context.Context, input operations.RecoveryInput) (fleet.AgentCompletion, error) {
				inputs <- input
				if input.ObservationID != state.ObservationID {
					return fleet.AgentCompletion{}, errors.New("rehearsal observation changed")
				}
				return broker.InvestigateRecovery(ctx, input)
			}}
			run := &demoRun{runtimeID: service.RuntimeID(), warehouse: service.Snapshot, operations: &runtimeOperations{service: service, recovery: manager}}
			c := &demoController{run: run, running: true, ready: true}
			c.mu.Lock()
			c.startOperations(ctx, time.Now())
			cycle := c.operations
			c.mu.Unlock()
			if cycle == nil {
				t.Fatal("normal controller did not start the investigation")
			}
			select {
			case <-cycle.done:
			case <-time.After(34 * time.Second):
				t.Fatal("investigation did not finish")
			}
			input := <-inputs
			if (!live && mock.sends != 2) || !reflect.DeepEqual(before, service.Snapshot()) {
				t.Fatal("verification must preserve the pre-approval state and return two responses offline")
			}
			op, err := a.Authenticate(ctx, tokens["operator"])
			if err != nil {
				t.Fatal(err)
			}
			rows, err := service.ReadAgentRuns(op, input.ID, 1)
			if tc.loseSecond {
				if err != nil || len(rows) != 1 || rows[0].Completion.Status != "failed" || rows[0].DecisionVerified || rows[0].ProposalID != nil {
					t.Fatal("lost response must leave a failed receipt without a proposal", rows, err)
				}
				var settled, uncertain, executed int
				err = pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE settled_at IS NOT NULL), count(*) FILTER (WHERE settled_at IS NULL),
					(SELECT count(*) FROM fleet_audit WHERE attempt='execute') FROM fleetops_operations.requests WHERE recovery_run_id=$1`, input.ID).
					Scan(&settled, &uncertain, &executed)
				if err != nil || settled != 1 || uncertain != 1 || executed != 0 {
					t.Fatal("lost response accounting or execution mismatch", settled, uncertain, executed, err)
				}
				c.mu.Lock()
				c.startOperations(ctx, time.Now())
				duplicate := c.operations
				c.mu.Unlock()
				if duplicate != nil {
					t.Fatal("the same incident was investigated again after failure")
				}
				t.Logf("source=%s run=%s status=failed settled=%d uncertain=%d executions=%d state_unchanged=true", source, input.ID, settled, uncertain, executed)
				return
			}
			settled := logNormalVerificationRequests(t, ctx, pool, input.ID, source)
			if err != nil || len(rows) != 1 || !rows[0].DecisionVerified || rows[0].Completion.Decision == nil {
				t.Fatalf("no Go-verified decision: %+v (%v)", rows, err)
			}
			if settled != rows[0].Completion.Requests || (!live && settled != 2) {
				t.Fatal("durable settlement count does not match the verified completion", settled)
			}
			if tc.tick == 1 {
				var commands int
				err := pool.QueryRow(ctx, "SELECT count(*) FROM fleet_audit WHERE attempt='execute'").Scan(&commands)
				if rows[0].Completion.Decision.Kind != "abstain" || rows[0].ProposalID != nil || err != nil || commands != 0 {
					t.Fatal("abstention failed", rows[0].Completion, commands, err)
				}
				t.Logf("source=%s run=%s tick=%d R4=%s decision=abstain requests=%d executions=0", source, input.ID, before.Tick, sim.FaultMotorFailure, settled)
				if visuals {
					awaitRehearsalFault(t, scenario, run, state, tokens["operator"])
				}
				return
			}
			if rows[0].ProposalID == nil {
				t.Fatalf("selection had no pending proposal: %+v", rows[0])
			}
			token := "Bearer " + tokens["operator"]
			report, err := scenario.warehouseRecoveryHistory(ctx, token)
			if pending := report.Proposals; err != nil || len(pending) != 1 || pending[0].State != "pending" || pending[0].Selection != mock.selected {
				t.Fatal("pending proposal mismatch", pending, err)
			}
			proposalID := *rows[0].ProposalID
			if tc.reject {
				awaitRehearsalBrowser(t, scenario, run, state, tokens["operator"], input.ID, proposal.Reject)
				var count int
				err := pool.QueryRow(ctx, "SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='execute'", proposalID).Scan(&count)
				if err != nil || count != 0 || !reflect.DeepEqual(before, service.Snapshot()) {
					t.Fatal("browser rejection changed the simulation", count, err)
				}
				return
			}
			if os.Getenv("FLEETOPS_REHEARSAL_BROWSER") == "1" {
				awaitRehearsalBrowser(t, scenario, run, state, tokens["operator"], input.ID, proposal.Approve)
			} else {
				result, err := scenario.decideWarehouseRecovery(ctx, token, input.ID, proposal.Approve)
				if err != nil || result.Execution != fleet.Succeeded || !result.AuditCommitted {
					t.Fatal("independent operator approval failed", result, err)
				}
			}
			if _, err := scenario.decideWarehouseRecovery(ctx, token, input.ID, proposal.Approve); err == nil {
				t.Fatal("duplicate approval admitted")
			}
			var count int
			var receiptID int64
			err = pool.QueryRow(ctx, "SELECT count(*), COALESCE(min(id), 0) FROM fleet_audit WHERE proposal_id=$1 AND attempt='execute'", proposalID).Scan(&count, &receiptID)
			after := service.Snapshot()
			job := slices.IndexFunc(after.Jobs, func(job sim.JobState) bool { return job.ID == "J10" })
			if err != nil || count != 1 || job < 0 || after.Jobs[job].AssignedRobot != "R2" {
				t.Fatal("expected exactly one J10 reassignment to R2", count, err)
			}
			t.Logf("source=%s run=%s proposal=%s receipt=%d tick=%d job=J10 owner=R2 requests=%d executions=1", source, input.ID, proposalID, receiptID, after.Tick, settled)
		})
		if live && !passed {
			return
		}
	}
}

func normalVerificationLive(t *testing.T) bool {
	t.Helper()
	lock := os.Getenv("FLEETOPS_V3_LIVE_ATTEMPT")
	if lock == "" {
		return false
	}
	plan, planErr := os.ReadFile(os.Getenv("FLEETOPS_V3_PLAN_PATH"))
	claim, lockErr := os.ReadFile(lock)
	if planErr != nil || lockErr != nil || strings.TrimSpace(string(claim)) != fmt.Sprintf("%x", sha256.Sum256(plan)) ||
		os.Getenv("FLEETOPS_REHEARSAL_BROWSER") != "1" || os.Getenv("FLEETOPS_OPERATIONS_PROTOCOL") != "v3" {
		t.Fatal("live verification requires the exact plan attempt and independent browser approval")
	}
	return true
}

func logNormalVerificationRequests(t *testing.T, ctx context.Context, pool *db.Pool, runID, source string) int {
	t.Helper()
	var total, settled int
	var reserved int64
	var evidence string
	err := pool.QueryRow(ctx, `SELECT count(*), count(provider_response_id) FILTER (WHERE settled_at IS NOT NULL),
		COALESCE(sum(reserved_micro_usd), 0), COALESCE(json_agg(json_build_object('model', model, 'responseId', provider_response_id,
		'inputTokens', input_tokens, 'outputTokens', output_tokens, 'reservedMicroUSD', reserved_micro_usd,
		'accountedMicroUSD', accounted_micro_usd) ORDER BY id)::text, '[]')
		FROM fleetops_operations.requests WHERE recovery_run_id=$1`, runID).Scan(&total, &settled, &reserved, &evidence)
	t.Logf("source=%s run=%s providerRequests=%s", source, runID, evidence)
	if err != nil || total < 1 || total > 6 || settled != total || reserved > 6*293_700 {
		t.Fatalf("provider ledger did not reconcile within the case cap: total=%d settled=%d reserved=%d error=%v", total, settled, reserved, err)
	}
	return settled
}

func serveRehearsalBrowser(t *testing.T, scenario *demoScenario, run *demoRun, state *model.FleetState, operator string, observed chan struct{}) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forBrowser := &demoRun{
		runtimeID: run.runtimeID, generation: demoRandom(), scenarioID: warehouseScenario, warehouse: run.warehouse,
		recovery: scenario.decideWarehouseRecovery, recoveryHistory: scenario.warehouseRecoveryHistory,
		capture: func(context.Context) error { return nil },
	}
	forBrowser.evidence = &demoExplanation{Generation: forBrowser.generation, RuntimeID: run.runtimeID,
		ScenarioID: warehouseScenario, Observation: state}
	controller := &demoController{run: forBrowser, ready: true, interactive: true, speed: "1"}
	manager := newVisitorManager(func(context.Context) (*visitorSession, error) {
		return &visitorSession{controller: controller, operator: operator, cleanup: func() error { return nil }}, nil
	})
	origin := "http://" + listener.Addr().String()
	gateway, err := newVisitorGateway(manager, Config{Mode: "local", Origin: origin, AssetsDir: integrationAssets(t)})
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	handler := http.Handler(gateway)
	if observed != nil {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/__rehearsal_observed" && r.Header.Get("Origin") == origin {
				w.WriteHeader(http.StatusNoContent)
				select {
				case observed <- struct{}{}:
				default:
				}
				return
			}
			gateway.ServeHTTP(w, r)
		})
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return origin
}

func awaitRehearsalFault(t *testing.T, scenario *demoScenario, run *demoRun, state *model.FleetState, operator string) {
	t.Helper()
	checked := make(chan struct{}, 1)
	t.Logf("BROWSER_FIXTURE_FAULT %s", serveRehearsalBrowser(t, scenario, run, state, operator, checked))
	select {
	case <-checked:
		t.Log("BROWSER_FIXTURE_FAULT_OBSERVED")
	case <-time.After(2 * time.Minute):
		t.Fatal("browser did not confirm the fault presentation")
	}
}

func awaitRehearsalBrowser(t *testing.T, scenario *demoScenario, run *demoRun, state *model.FleetState, operator, runID string, decision proposal.Decision) {
	t.Helper()
	origin := serveRehearsalBrowser(t, scenario, run, state, operator, nil)
	wantState, wantExecution := "approved", fleet.Succeeded
	if decision == proposal.Reject {
		wantState, wantExecution = "rejected", fleet.NotExecuted
		t.Logf("BROWSER_FIXTURE_REJECTION %s pending=%s", origin, runID)
	} else {
		t.Logf("BROWSER_FIXTURE_READY %s pending=%s", origin, runID)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		report, err := scenario.warehouseRecoveryHistory(context.Background(), "Bearer "+operator)
		if err != nil {
			t.Fatal(err)
		}
		if items := report.Proposals; len(items) == 1 && items[0].State == wantState && items[0].Execution == string(wantExecution) {
			t.Logf("BROWSER_FIXTURE_%s", strings.ToUpper(wantState))
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("browser operator did not record the requested decision", decision)
}

func openRehearsalPool(t *testing.T, ctx context.Context) *db.Pool {
	t.Helper()
	dsn := os.Getenv("FLEETOPS_TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("FLEETOPS_REQUIRE_DB") != "1" {
		t.Fatal("isolated rehearsal database required")
	}
	cfg := db.DefaultConfig(dsn)
	admin, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	cfg.Schema = "fleetops_rehearsal_" + demoRandom()[:24]
	schema := pgx.Identifier{cfg.Schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	pool, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}

type recoveryRehearsal struct {
	selected fleet.AgentKey
	caseID   string
	calls    []any
	intent   map[string]any
	sends    int
}

func newRecoveryRehearsal(t *testing.T, state *model.FleetState, selectJob bool) *recoveryRehearsal {
	t.Helper()
	rehearsal := &recoveryRehearsal{caseID: strings.ReplaceAll(t.Name(), "/", "_")}
	var incidentKeys []map[string]string
	for _, incident := range state.ActiveIncidents {
		ref := map[string]any{"scope": "snapshot", "tick": state.Tick, "robotId": incident.RobotID, "type": incident.Type}
		rehearsal.addCall("readIncident", map[string]any{"reference": ref})
		rehearsal.addCall("listRecoveryCandidates", map[string]any{"reference": ref})
		incidentKeys = append(incidentKeys, map[string]string{"robotId": incident.RobotID, "type": incident.Type})
	}
	if len(incidentKeys) == 0 {
		t.Fatal("fixture did not produce an incident")
	}
	keys := make([]fleet.AgentKey, 0, len(state.ReassignmentCandidates))
	var robots, jobs []string
	for _, item := range state.ReassignmentCandidates {
		key := fleet.AgentKey{JobID: item.JobID, FromRobotID: item.FromRobotID, ToRobotID: item.ToRobotID}
		keys = append(keys, key)
		for _, id := range []string{key.FromRobotID, key.ToRobotID} {
			if !slices.Contains(robots, id) {
				robots = append(robots, id)
			}
		}
		if !slices.Contains(jobs, key.JobID) {
			jobs = append(jobs, key.JobID)
		}
		if key.JobID == "J10" && key.ToRobotID == "R2" {
			rehearsal.selected = key
		}
	}
	decision := map[string]any{"kind": "abstain", "reason": "no_supported_incident"}
	comparisons := []any{}
	if selectJob {
		if rehearsal.selected.JobID == "" {
			t.Fatal("exact intervention fixture no longer admits J10 → R2")
		}
		rehearsal.addCall("readRobots", map[string]any{"ids": robots})
		rehearsal.addCall("readJobs", map[string]any{"ids": jobs})
		decision = map[string]any{"kind": "select", "selected": rehearsal.selected, "basis": "admitted_option"}
		for _, key := range keys {
			if key != rehearsal.selected {
				comparisons = append(comparisons, map[string]any{"left": rehearsal.selected, "right": key, "metric": "pickupSteps"})
			}
		}
	} else if len(keys) != 0 || len(incidentKeys) != 1 || incidentKeys[0]["type"] != "motor_failure" || incidentKeys[0]["robotId"] != "R4" {
		t.Fatal("exact abstention fixture changed", incidentKeys, keys)
	}
	if len(rehearsal.calls) > 8 {
		t.Fatalf("fixture needs %d tools; worker allows eight", len(rehearsal.calls))
	}
	rehearsal.intent = map[string]any{"version": "recovery-decision-v3", "objective": "compare_tradeoffs",
		"relevantIncidents": incidentKeys, "comparisons": comparisons, "decision": decision}
	return rehearsal
}

func (r *recoveryRehearsal) addCall(name string, args map[string]any) {
	id := fmt.Sprintf("fixture_%d", len(r.calls))
	raw, _ := json.Marshal(args)
	r.calls = append(r.calls, map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": "call_" + id,
		"name": name, "arguments": string(raw), "status": "completed"})
}

func (r *recoveryRehearsal) reply(_ context.Context, _ []byte) ([]byte, error) {
	r.sends++
	output := r.calls
	switch r.sends {
	case 1:
	case 2:
		raw, err := json.Marshal(r.intent)
		if err != nil {
			return nil, err
		}
		output = []any{map[string]any{"type": "message", "role": "assistant", "id": "msg_fixture", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": string(raw), "annotations": []any{}}}}}
	default:
		return nil, errors.New("unexpected offline provider request")
	}
	return json.Marshal(map[string]any{
		"id": fmt.Sprintf("resp_fixture_%s_%d", r.caseID, r.sends), "object": "response", "model": "gpt-6-luna", "created_at": 1, "status": "completed", "output": output,
		"usage": map[string]any{"input_tokens": 200, "output_tokens": 30, "total_tokens": 230,
			"input_tokens_details": map[string]int{"cached_tokens": 0}, "output_tokens_details": map[string]int{"reasoning_tokens": 0}},
	})
}
