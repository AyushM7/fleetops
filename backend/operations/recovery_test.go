package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"
)

const recoveryTestRequest = `{"model":"gpt-6-luna","input":[{"role":"user","content":"review"}],"store":false,"service_tier":"default","max_output_tokens":6000,"tools":[{"type":"function","name":"readIncident"}]}`

func TestRecoveryRequestTransportBounds(t *testing.T) {
	good := recoveryTestRequest
	if !validRecoveryRequest([]byte(good)) {
		t.Fatal("approved request denied")
	}
	for _, change := range [][2]string{{"readIncident", "submitProposal"}, {`"store":false`, `"store":true`}, {"6000", "6001"}, {`"input":`, `"previous_response_id":"x","input":`}} {
		if validRecoveryRequest([]byte(strings.Replace(good, change[0], change[1], 1))) {
			t.Fatal("invalid request admitted", change)
		}
	}
}

type recoveryTestTransport func(*http.Request) (*http.Response, error)

func (f recoveryTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRecoveryProviderDiagnosesTransportFailures(t *testing.T) {
	ctx := context.Background()
	expired, expire := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer expire()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		res  *http.Response
		want recoveryProviderFailure
	}{
		{"timeout before headers", expired, nil, recoveryProviderFailure{stage: "before_headers", reason: "timeout"}},
		{"cancelled before headers", cancelled, nil, recoveryProviderFailure{stage: "before_headers", reason: "cancelled"}},
		{"http status", ctx, &http.Response{StatusCode: 429, Header: http.Header{"X-Request-Id": {"req_test_123"}}, Body: io.NopCloser(strings.NewReader("private body"))},
			recoveryProviderFailure{stage: "http_status", status: 429, requestID: "req_test_123"}},
		{"truncated response", ctx, &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": {"unsafe identifier"}}, Body: io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))},
			recoveryProviderFailure{stage: "response_body", reason: "io", status: 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: recoveryTestTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.GetBody != nil || req.Header.Get("Idempotency-Key") != "" || req.Method != http.MethodPost {
					t.Error("provider request is replayable")
				}
				return tc.res, req.Context().Err()
			})}
			body, err := sendRecoveryRequest(tc.ctx, client, "unused-fixture", []byte(recoveryTestRequest))
			var got recoveryProviderFailure
			if len(body) != 0 || calls != 1 || !errors.As(err, &got) || got != tc.want {
				t.Fatal("incorrect provider diagnostic or request count", got, calls, err)
			}
		})
	}
}

func TestUncappedRecoveryLedgerIntegration(t *testing.T) {
	ctx := context.Background()
	entry := filepath.Join(t.TempDir(), "worker.js")
	if err := os.WriteFile(entry, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEETOPS_OPERATIONS_AGENT", entry)
	t.Setenv("FLEETOPS_OPERATIONS_PROTOCOL", "v3")
	t.Setenv("FLEETOPS_OPERATIONS_INPUT_MICRO_USD_PER_MILLION", "275000")
	t.Setenv("FLEETOPS_OPERATIONS_OUTPUT_MICRO_USD_PER_MILLION", "825000")
	t.Setenv(providerKeyVariable, "offline-fixture")
	t.Setenv("FLEETOPS_OPERATIONS_DAILY_REQUESTS", "")
	t.Setenv("FLEETOPS_OPERATIONS_DAILY_MICRO_USD", "")
	manager, err := Open(ctx, testDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	l := manager.ledger
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	const visitors = 8
	var wg sync.WaitGroup
	for i := range visitors {
		runtime := runtimeFixture(t)
		run := "A" + runtime[1:]
		wg.Go(func() {
			for ordinal := range recoveryRequests {
				request, err := l.reserveRecovery(ctx, runtime, run, ordinal == 0)
				if err != nil {
					t.Error(err)
					return
				}
				if ordinal == recoveryRequests-1 {
					// A send with an uncertain response remains accounted at its estimate.
					return
				}
				u := providerUsage{ID: fmt.Sprintf("resp_uncapped_%d_%d", i, ordinal), usage: usage{Model: modelID, Input: 20, Output: 10}}
				if err := l.settleRecovery(ctx, request, u); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	var total, responses, settledTokens int
	if err := l.db.QueryRow(ctx, `SELECT count(*), count(provider_response_id),
		count(*) FILTER (WHERE input_tokens=20 AND output_tokens=10 AND accounted_micro_usd>0)
		FROM fleetops_operations.requests`).Scan(&total, &responses, &settledTokens); err != nil ||
		total != visitors*recoveryRequests || responses != visitors*(recoveryRequests-1) || settledTokens != responses {
		t.Fatal(total, responses, settledTokens, err)
	}
	var recorded, expected int64
	if err := l.db.QueryRow(ctx, `SELECT (SELECT sum(micro_usd) FROM fleetops_operations.days),
		sum(COALESCE(accounted_micro_usd,reserved_micro_usd)) FROM fleetops_operations.requests`).Scan(&recorded, &expected); err != nil ||
		recorded != expected || recorded <= int64(visitors)*l.pricing.cost(maxInputTokens, recoveryOutput) {
		t.Fatal(recorded, expected, err)
	}
}

func TestRecoveryBrokerRunsCompiledMastraIntegration(t *testing.T) {
	entry := os.Getenv("FLEETOPS_TEST_V3_WORKER")
	if entry == "" {
		t.Skip("compiled V3 worker unavailable")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l, err := openLedger(ctx, testDatabase(t), pricing{minInputRate, minOutputRate})
	if err != nil {
		t.Fatal(err)
	}
	for _, incident := range []string{"motor_failure", "sensor_failure", "critical_battery", "traffic_blocked"} {
		t.Run(incident, func(t *testing.T) {
			sends := 0
			robot := map[string]any{"id": "R4", "x": 6, "y": 1, "battery": 64, "status": "faulted", "jobId": "", "waitTicks": 0, "blocked": false}
			robots := []any{robot}
			jobs, candidates := []any{}, []any{}
			selected := map[string]any{"jobId": "J10", "fromRobotId": "R4", "toRobotId": "R2"}
			if incident == "traffic_blocked" {
				robot["status"], robot["blocked"], robot["jobId"] = "moving", true, "J10"
				robots = append(robots, map[string]any{"id": "R2", "x": 7, "y": 1, "battery": 80, "status": "idle", "jobId": "", "waitTicks": 0, "blocked": false})
				jobs = append(jobs, map[string]any{"id": "J10", "status": "assigned", "assignedRobot": "R4", "fromX": 5, "fromY": 1, "toX": 3, "toY": 1})
				candidates = append(candidates, map[string]any{"jobId": "J10", "fromRobotId": "R4", "toRobotId": "R2", "pickupSteps": 2, "deliverySteps": 2, "chargingExitSteps": 3, "requiredBattery": 12})
			}
			observation := strings.Repeat("a", 64)
			ref := map[string]any{"scope": "snapshot", "tick": 4, "robotId": "R4", "type": incident}
			snapshot, _ := json.Marshal(map[string]any{"observationId": observation, "tick": 4, "poisoned": false, "robots": robots, "stations": []any{}, "activeIncidents": []any{
				map[string]any{"robotId": "R4", "type": incident, "severity": "high", "telemetry": robot},
			}, "jobs": jobs, "reassignmentCandidates": candidates})
			respond := func(_ context.Context, request []byte) ([]byte, error) {
				sends++
				tools := map[string]any{"readIncident": map[string]any{"reference": ref}, "listRecoveryCandidates": map[string]any{"reference": ref}}
				if incident == "traffic_blocked" {
					tools["readRobots"] = map[string]any{"ids": []string{"R4", "R2"}}
					tools["readJobs"] = map[string]any{"ids": []string{"J10"}}
				}
				var output []any
				if sends == 1 {
					output = append(output, map[string]any{"type": "reasoning", "id": "rs_test", "summary": []any{}, "encrypted_content": "fixture-ciphertext"})
					for i, name := range []string{"readIncident", "listRecoveryCandidates", "readRobots", "readJobs"} {
						args, ok := tools[name]
						if !ok {
							continue
						}
						raw, _ := json.Marshal(args)
						output = append(output, map[string]any{"type": "function_call", "id": fmt.Sprintf("fc_%d", i), "call_id": fmt.Sprintf("call_%d", i), "name": name, "arguments": string(raw), "status": "completed"})
					}
				} else if sends == 2 {
					if err := verifyRecoveryContinuation(request, len(tools)); err != nil {
						t.Error(err)
						return nil, err
					}
					decision := map[string]any{"kind": "abstain", "reason": "no_supported_incident"}
					if incident == "traffic_blocked" {
						decision = map[string]any{"kind": "select", "selected": selected, "basis": "admitted_option"}
					}
					intent, _ := json.Marshal(map[string]any{"version": "recovery-decision-v3", "objective": "compare_tradeoffs", "relevantIncidents": []any{map[string]string{"robotId": "R4", "type": incident}}, "comparisons": []any{}, "decision": decision})
					output = []any{map[string]any{"type": "message", "role": "assistant", "id": "msg_test", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": string(intent), "annotations": []any{}}}}}
				} else {
					return nil, errors.New("unexpected extra send")
				}
				return json.Marshal(map[string]any{"id": fmt.Sprintf("resp_%s_%d", incident, sends), "object": "response", "model": modelID, "created_at": 1, "status": "completed", "output": output,
					"usage": map[string]any{"input_tokens": 200, "output_tokens": 30, "total_tokens": 230, "input_tokens_details": map[string]int{"cached_tokens": 0}, "output_tokens_details": map[string]int{"reasoning_tokens": 0}}})
			}
			worker := &recoveryWorker{node: node, entrypoint: entry, send: respond}
			runtime := runtimeFixture(t)
			ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()
			input := RecoveryInput{ID: "A" + runtime[1:], RuntimeID: runtime, ObservationID: observation, Snapshot: snapshot}
			completion, err := worker.investigate(ctx, input, l)
			if err != nil || completion.Status != "accepted" || completion.Decision == nil || sends != 2 {
				t.Fatal("mocked provider failed", sends, completion, err)
			}
			want := "abstain"
			if incident == "traffic_blocked" {
				want = "select"
			}
			if completion.Decision.Kind != want {
				t.Fatal(completion.Decision)
			}
			var count int
			if err := l.db.QueryRow(ctx, "SELECT count(*) FROM fleetops_operations.requests WHERE recovery_run_id=$1 AND settled_at IS NOT NULL", input.ID).Scan(&count); err != nil || count != 2 {
				t.Fatal(count, err)
			}
			if incident == "traffic_blocked" {
				for _, failure := range []string{"response_lost", "cancel_worker", "invalid_usage", "incomplete"} {
					t.Run(failure, func(t *testing.T) {
						sends = 0
						testRecoverySecondRequestFailure(t, worker, input, respond, failure)
					})
				}
			}
		})
	}
}

func testRecoverySecondRequestFailure(t *testing.T, worker *recoveryWorker, input RecoveryInput,
	respond func(context.Context, []byte) ([]byte, error), failure string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	l, err := openLedger(ctx, testDatabase(t), pricing{minInputRate, minOutputRate})
	if err != nil {
		t.Fatal(err)
	}
	copy := *worker
	var calls int
	copy.send = func(ctx context.Context, request []byte) ([]byte, error) {
		calls++
		body, err := respond(ctx, request)
		if calls != 2 || err != nil {
			return body, err
		}
		switch failure {
		case "response_lost":
			return nil, recoveryProviderFailure{stage: "response_body", reason: "io"}
		case "cancel_worker":
			cancel()
			return nil, recoveryProviderFailure{stage: "before_headers", reason: "cancelled"}
		case "invalid_usage":
			return []byte(`{"id":"resp_invalid","model":"gpt-6-luna","status":"completed"}`), nil
		default:
			return []byte(strings.Replace(string(body), `"status":"completed","usage":`, `"status":"incomplete","usage":`, 1)), nil
		}
	}
	got, err := copy.investigate(ctx, input, l)
	if err == nil || got.Decision != nil || calls != 2 {
		t.Fatal("incomplete investigation completed or retried", got, calls, err)
	}
	var settled, retained, retainedMicroUSD int64
	audit := context.Background()
	err = l.db.QueryRow(audit, `SELECT count(*) FILTER (WHERE settled_at IS NOT NULL), count(*) FILTER (WHERE settled_at IS NULL),
		COALESCE(sum(reserved_micro_usd) FILTER (WHERE settled_at IS NULL), 0) FROM fleetops_operations.requests WHERE recovery_run_id=$1`, input.ID).
		Scan(&settled, &retained, &retainedMicroUSD)
	wantSettled, wantRetained, wantMicros := int64(1), int64(1), int64(293700)
	if failure == "incomplete" {
		wantSettled, wantRetained, wantMicros = 2, 0, 0
	}
	if err != nil || settled != wantSettled || retained != wantRetained || retainedMicroUSD != wantMicros {
		t.Fatal("unexpected settled and retained requests", settled, retained, retainedMicroUSD, err)
	}
	if _, err := l.reserveRecovery(audit, input.RuntimeID, input.ID, true); !errors.Is(err, errRecoveryReplay) {
		t.Fatal("retry could restart the same investigation", err)
	}
}

func TestRecoveryBrokerDeniesDuplicateRequestFrameIntegration(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node worker runtime unavailable")
	}
	entry := filepath.Join(t.TempDir(), "duplicate.mjs")
	frame := `{"request":` + recoveryTestRequest + `}`
	script := `import { createInterface } from "node:readline";
const lines = createInterface({ input: process.stdin })[Symbol.asyncIterator]();
await lines.next();
console.log(` + fmt.Sprintf("%q", frame) + `);
await lines.next();
console.log(` + fmt.Sprintf("%q", frame) + `);
await lines.next();`
	if err := os.WriteFile(entry, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	l, err := openLedger(ctx, testDatabase(t), pricing{minInputRate, minOutputRate})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := 0
	w := recoveryWorker{node: node, entrypoint: entry, send: func(context.Context, []byte) ([]byte, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return []byte(`{"id":"resp_duplicate_fixture","model":"gpt-6-luna","status":"completed","usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}}`), nil
	}}
	runtime := runtimeFixture(t)
	input := RecoveryInput{ID: "A" + runtime[1:], RuntimeID: runtime, Snapshot: json.RawMessage(`{}`)}
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			got, err := w.investigate(ctx, input, l)
			if err == nil || got.Decision != nil {
				t.Error("duplicate completed", got, err)
			}
			errs <- err
		}()
	}
	first, second := <-errs, <-errs
	if calls != 1 || errors.Is(first, errRecoveryReplay) == errors.Is(second, errRecoveryReplay) {
		t.Fatal("broker did not stop duplicate frame and replay", calls, first, second)
	}
	var reserved int
	if err := l.db.QueryRow(ctx, "SELECT count(*) FROM fleetops_operations.requests WHERE recovery_run_id=$1", input.ID).Scan(&reserved); err != nil || reserved != 1 {
		t.Fatal("duplicate frame was reserved", reserved, err)
	}
}

func verifyRecoveryContinuation(request []byte, tools int) error {
	var body struct {
		Input []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Encrypted string `json:"encrypted_content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(request, &body); err != nil {
		return err
	}
	var reasoning bool
	calls := make(map[string]bool)
	for _, item := range body.Input {
		switch item.Type {
		case "item_reference":
			return errors.New("stateless continuation depends on a stored item")
		case "reasoning":
			reasoning = item.Encrypted == "fixture-ciphertext"
		case "function_call":
			calls[item.CallID] = false
		case "function_call_output":
			if answered, ok := calls[item.CallID]; !ok || answered {
				return errors.New("orphaned or duplicate tool output")
			}
			calls[item.CallID] = true
		}
	}
	if !reasoning || len(calls) != tools {
		return errors.New("stateless continuation omitted reasoning or tool calls")
	}
	for _, answered := range calls {
		if !answered {
			return errors.New("stateless continuation omitted a tool output")
		}
	}
	return nil
}
