package visitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/proposal"
)

func TestWarehouseV3OperatorHandoffIntegration(t *testing.T) {
	t.Run("approve", func(t *testing.T) {
		testWarehouseV3OperatorDecision(t, proposal.Approve, "approved", fleet.Succeeded, 1)
	})
	t.Run("reject", func(t *testing.T) {
		testWarehouseV3OperatorDecision(t, proposal.Reject, "rejected", fleet.NotExecuted, 0)
	})
}

func testWarehouseV3OperatorDecision(t *testing.T, decisionAction proposal.Decision, wantState string, wantExecution fleet.Execution, wantExecutions int) {
	t.Helper()
	_, pool, _, _ := visitorArchiveFixture(t)
	ctx := context.Background()
	a, tokens, _, err := visitorCredentials()
	if err != nil {
		t.Fatal(err)
	}
	service, err := fleet.NewVisitor("blocked-reassignment", pool)
	if err != nil {
		t.Fatal(err)
	}
	s := &demoScenario{service: service, factory: demoFactory{auth: a, tokens: tokens}}
	proposer, err := a.Authenticate(ctx, tokens["proposer"])
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.ReadState(proposer)
	if err != nil {
		t.Fatal(err)
	}
	const contract = "recovery-decision-v3"
	run, err := service.StartAgentRun(proposer, view.ObservationID, fleet.AgentMetadata{Version: "agent-run-v1", Contract: contract, Objective: "compare_tradeoffs", Source: "mocked", RequestedModel: "offline/test"})
	if err != nil || len(run.Facts.Candidates) == 0 {
		t.Fatalf("could not start test V3: %v", err)
	}
	pick := run.Facts.Candidates[len(run.Facts.Candidates)-1]
	decision := &fleet.AgentDecision{Kind: "select", Selected: &pick.Key, Basis: "admitted_option", Comparisons: []fleet.AgentComparison{}}
	for _, other := range run.Facts.Candidates {
		if other.Key == pick.Key {
			continue
		}
		left, right := pick.Metrics["pickupSteps"], other.Metrics["pickupSteps"]
		relation := "equal"
		if left < right {
			relation = "less"
		} else if left > right {
			relation = "greater"
		}
		decision.Comparisons = append(decision.Comparisons, fleet.AgentComparison{Left: pick.Key, Right: other.Key, Metric: "pickupSteps", LeftValue: left, RightValue: right, Relation: relation})
	}
	before := service.Snapshot()
	receipt, err := service.FinishAgentRun(proposer, run.ID, fleet.AgentFinish{
		RuntimeID: run.RuntimeID, ObservationID: run.ObservationID,
		Completion: fleet.AgentCompletion{Status: "accepted", Failure: "none", SubmissionCheck: "current", Usage: fleet.AgentUsage{Status: "unavailable"}, Decision: decision},
		Submit:     &fleet.AgentSubmission{ExpiresAt: time.Now().UTC().Add(time.Minute)},
	})
	if err != nil || !receipt.Recorded || receipt.Result.Proposal.State != proposal.Pending || !reflect.DeepEqual(before, service.Snapshot()) {
		t.Fatalf("invalid pending V3 receipt: %+v %v", receipt, err)
	}
	if _, err := s.decideWarehouseRecovery(ctx, "Bearer "+tokens["proposer"], run.ID, proposal.Approve); !errors.Is(err, fleet.ErrUnauthorized) {
		t.Fatalf("proposer acquired approval authority: %v", err)
	}
	session := &visitorSession{operator: tokens["operator"], controller: &demoController{
		ready: true, run: &demoRun{generation: "warehouse-test", runtimeID: run.RuntimeID, warehouse: service.Snapshot, recovery: s.decideWarehouseRecovery, recoveryHistory: s.warehouseRecoveryHistory},
	}}
	session.deadline.Store(new(time.Now().Add(time.Minute)))
	history := func() []warehouseRecoveryItem {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/demo/recovery", strings.NewReader("{}"))
		req.Header.Set("X-FleetOps-Generation", "warehouse-test")
		session.serve(w, req)
		var rows []warehouseRecoveryItem
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 1 {
			t.Fatalf("bad recovery list: %d %s", w.Code, w.Body)
		}
		return rows
	}
	if item := history()[0]; item.RunID != run.ID || item.State != "pending" || item.Selection != pick.Key || item.Metrics["requiredBattery"] != pick.Metrics["requiredBattery"] {
		t.Fatalf("incorrect pending evidence: %+v", item)
	}
	path := "/demo/recovery/" + run.ID + "/" + string(decisionAction)
	if !visitorOperation(path) || visitorOperation(path+"/extra") || visitorOperation("/demo/recovery/invalid/approve") {
		t.Fatal("invalid recovery routing")
	}
	call := func(generation string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		req.Header.Set("X-FleetOps-Generation", generation)
		session.serve(w, req)
		return w
	}
	if stale := call("wrong-session"); stale.Code != http.StatusConflict || !reflect.DeepEqual(before, service.Snapshot()) {
		t.Fatalf("stale visitor request admitted: %d", stale.Code)
	}
	result := call("warehouse-test")
	var payload fleet.Result
	if result.Code != http.StatusOK || json.Unmarshal(result.Body.Bytes(), &payload) != nil || !payload.AuditCommitted || payload.Execution != wantExecution {
		t.Fatalf("warehouse decision failed: %d %s", result.Code, result.Body)
	}
	if item := history()[0]; item.State != wantState || item.Execution != string(wantExecution) {
		t.Fatalf("missing execution receipt: %+v", item)
	}
	if decisionAction == proposal.Reject && !reflect.DeepEqual(before, service.Snapshot()) {
		t.Fatal("rejection changed the warehouse snapshot")
	}
	if repeated := call("warehouse-test"); repeated.Code != http.StatusConflict {
		t.Fatalf("repeat decision returned %d", repeated.Code)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='execute'", receipt.Result.Proposal.ID).Scan(&count); err != nil || count != wantExecutions {
		t.Fatalf("execution count %d: %v", count, err)
	}
}
