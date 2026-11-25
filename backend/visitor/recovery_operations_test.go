package visitor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/operations"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestNormalRecoveryManagerAdvancesDuringInvestigationIntegration(t *testing.T) {
	f, pool, owner, dir := visitorArchiveFixture(t)
	t.Cleanup(func() { cleanupVisitorArchiveFixture(t, f, owner, dir) })
	cfg := sim.WarehouseV2Config()
	cfg.InitialJobs = 0
	cfg.ScheduledBreakdowns = []sim.ScheduledBreakdown{{Tick: 1, RobotID: "R4", Type: sim.FaultMotorFailure}}
	service, err := fleet.New(cfg, 99, pool, time.Now, func(context.Context, proposal.OperatorIdentity) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	a, tokens, _, err := visitorCredentials()
	if err != nil {
		t.Fatal(err)
	}
	scenario := &demoScenario{service: service, factory: demoFactory{auth: a, tokens: tokens}}
	called := make(chan operations.RecoveryInput, 2)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	worker := &recoveryOperations{scenario: scenario, source: "mocked", reviewed: make(map[string]bool), investigate: func(ctx context.Context, input operations.RecoveryInput) (fleet.AgentCompletion, error) {
		called <- input
		select {
		case <-release:
		case <-ctx.Done():
			return fleet.AgentCompletion{}, ctx.Err()
		}
		return fleet.AgentCompletion{Status: "accepted", Failure: "none", SubmissionCheck: "disabled", Usage: fleet.AgentUsage{Status: "unavailable"}, Decision: &fleet.AgentDecision{Kind: "abstain", Reason: "no_supported_incident"}}, nil
	}}
	run := &demoRun{operations: &runtimeOperations{service: service, recovery: worker}, warehouse: service.Snapshot, step: func(context.Context) error { return service.Tick() }}
	c := &demoController{run: run, ready: true, running: true}
	now := time.Now()
	for i := 1; i <= 4; i++ {
		c.mu.Lock()
		err := c.advanceInteractive(context.Background(), now.Add(time.Duration(i)*time.Second))
		c.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	var input operations.RecoveryInput
	select {
	case input = <-called:
	case <-time.After(3 * time.Second):
		t.Fatal("motor incident did not reach normal manager")
	}
	if !strings.Contains(string(input.Snapshot), `"motor_failure"`) || service.Snapshot().Tick != 4 {
		t.Fatal("investigation did not keep ticking")
	}
	c.mu.Lock()
	done := c.operations.done
	c.mu.Unlock()
	close(release)
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("did not persist abstention")
	}
	operator, err := a.Authenticate(context.Background(), tokens["operator"])
	if err != nil {
		t.Fatal(err)
	}
	rows, err := service.ReadAgentRuns(operator, input.ID, 1)
	if err != nil || len(rows) != 1 || !rows[0].DecisionVerified || rows[0].ProposalID != nil {
		t.Fatal(rows, err)
	}
	report, err := scenario.warehouseRecoveryHistory(context.Background(), "Bearer "+tokens["operator"])
	if err != nil || report.Live || len(report.Proposals) != 0 || len(report.Investigations) != 1 ||
		report.Investigations[0] != (warehouseRecoveryInvestigation{RunID: input.ID, Source: "mocked", Status: "abstained"}) {
		t.Fatal(report, err)
	}
	c.mu.Lock()
	err = c.advanceInteractive(context.Background(), now.Add(6*time.Second))
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("same active incident was investigated twice")
	default:
	}
}

func TestRecoveryInvestigationDistinguishesProviderConfirmation(t *testing.T) {
	run := fleet.AgentRun{Metadata: fleet.AgentMetadata{Source: "live-model"}}
	if got := recoveryInvestigation(run); got.ProviderConfirmed || got.Status != "investigating" {
		t.Fatal(got)
	}
	run.Completion = &fleet.AgentCompletion{Status: "failed", Failure: "invalid_usage"}
	if got := recoveryInvestigation(run); got.ProviderConfirmed || got.Status != "failed" {
		t.Fatal(got)
	}
	run.Completion = &fleet.AgentCompletion{
		Status: "accepted", ActualModel: "openai/gpt-6-luna", Requests: 2,
		Usage: fleet.AgentUsage{Status: "reported"}, Decision: &fleet.AgentDecision{Kind: "abstain"},
	}
	run.DecisionVerified = true
	if got := recoveryInvestigation(run); !got.ProviderConfirmed || got.Status != "abstained" {
		t.Fatal(got)
	}
	run.Metadata.Source = "mocked"
	if got := recoveryInvestigation(run); got.ProviderConfirmed || got.Status != "abstained" {
		t.Fatal(got)
	}
}

func TestNormalRecoveryManagerHandsSelectionToIndependentOperatorIntegration(t *testing.T) {
	f, pool, owner, dir := visitorArchiveFixture(t)
	t.Cleanup(func() { cleanupVisitorArchiveFixture(t, f, owner, dir) })
	ctx := context.Background()
	a, tokens, _, err := visitorCredentials()
	if err != nil {
		t.Fatal(err)
	}
	service, err := fleet.NewVisitor("blocked-reassignment", pool)
	if err != nil {
		t.Fatal(err)
	}
	scenario := &demoScenario{service: service, factory: demoFactory{auth: a, tokens: tokens}}
	started := make(chan string, 1)
	manager := &recoveryOperations{scenario: scenario, source: "mocked", reviewed: make(map[string]bool), investigate: func(ctx context.Context, input operations.RecoveryInput) (fleet.AgentCompletion, error) {
		started <- input.ID
		rows, err := service.ReadAgentRuns(ctx, input.ID, 1)
		if err != nil || len(rows) != 1 || len(rows[0].Facts.Candidates) == 0 {
			return fleet.AgentCompletion{}, errors.New("missing deterministic test offer")
		}
		pick := rows[0].Facts.Candidates[0]
		decision := &fleet.AgentDecision{Kind: "select", Selected: &pick.Key, Basis: "admitted_option"}
		for _, other := range rows[0].Facts.Candidates[1:] {
			l, r := pick.Metrics["pickupSteps"], other.Metrics["pickupSteps"]
			relation := "equal"
			if l < r {
				relation = "less"
			} else if l > r {
				relation = "greater"
			}
			decision.Comparisons = append(decision.Comparisons, fleet.AgentComparison{Left: pick.Key, Right: other.Key, Metric: "pickupSteps", LeftValue: l, RightValue: r, Relation: relation})
		}
		return fleet.AgentCompletion{Status: "accepted", Failure: "none", SubmissionCheck: "disabled", Usage: fleet.AgentUsage{Status: "unavailable"}, Decision: decision}, nil
	}}
	run := &demoRun{runtimeID: service.RuntimeID(), operations: &runtimeOperations{service: service, recovery: manager}, warehouse: service.Snapshot}
	c := &demoController{run: run, ready: true, running: true}
	before := service.Snapshot()
	c.mu.Lock()
	c.startOperations(ctx, time.Now())
	cycle := c.operations
	c.mu.Unlock()
	if cycle == nil {
		t.Fatal("incident did not enter the normal controller")
	}
	select {
	case <-cycle.done:
	case <-time.After(4 * time.Second):
		t.Fatal("investigation did not finish")
	}
	id := <-started
	if !reflect.DeepEqual(before, service.Snapshot()) {
		t.Fatal("worker changed the simulation")
	}
	report, err := scenario.warehouseRecoveryHistory(ctx, "Bearer "+tokens["operator"])
	if pending := report.Proposals; err != nil || len(pending) != 1 || pending[0].RunID != id || pending[0].State != "pending" {
		t.Fatal(pending, err)
	}
	got, err := scenario.decideWarehouseRecovery(ctx, "Bearer "+tokens["operator"], id, proposal.Approve)
	if err != nil || got.Execution != fleet.Succeeded || !got.AuditCommitted {
		t.Fatal(got, err)
	}
	if _, err := scenario.decideWarehouseRecovery(ctx, "Bearer "+tokens["operator"], id, proposal.Approve); err == nil {
		t.Fatal("replayed approval")
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='execute'", got.Proposal.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
