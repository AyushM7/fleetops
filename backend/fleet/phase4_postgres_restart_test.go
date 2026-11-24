//go:build fleetops_test

package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/proposal"
)

func TestIntegrationPhase4PostgresRestartRetainsApprovalBoundary(t *testing.T) {
	dir := os.Getenv("FLEETOPS_PHASE4_RESTART_SIGNALS")
	if dir == "" {
		t.Skip("requires the disposable Phase 4 PostgreSQL restart orchestrator")
	}
	service, pool, run, finish := agentRunFixture(t)
	proposer := agentContext(t, "agent-owner", auth.Proposer)
	before := service.Snapshot()
	receipt, err := service.FinishAgentRun(proposer, run.ID, finish)
	if err != nil || !receipt.Recorded || receipt.Result.Proposal.State != proposal.Pending {
		t.Fatalf("failed to persist the pending proposal: %v, %v", receipt, err)
	}
	phase4RestartSignal(t, dir, "ready", "offline")
	ctx, cancel := context.WithTimeout(proposer, 3*time.Second)
	_, err = service.ReadAgentRuns(ctx, run.ID, 1)
	cancel()
	if err == nil || !reflect.DeepEqual(before, service.Snapshot()) {
		t.Fatal("database outage manufactured a read or changed simulator state")
	}
	phase4RestartSignal(t, dir, "outage-verified", "online")

	rows, err := service.ReadAgentRuns(proposer, run.ID, 1)
	if err != nil || len(rows) != 1 || !rows[0].DecisionVerified || rows[0].ProposalID == nil || *rows[0].ProposalID != receipt.Result.Proposal.ID {
		t.Fatalf("restart lost the durable run/proposal link: %v, %v", rows, err)
	}
	replay, err := service.FinishAgentRun(proposer, run.ID, finish)
	if err != nil || replay.Result.Proposal.ID != receipt.Result.Proposal.ID || !reflect.DeepEqual(before, service.Snapshot()) {
		t.Fatalf("replayed completion created an intervention: %v, %v", replay, err)
	}
	operator := proposal.OperatorIdentity{Subject: "phase4-restart-reviewer"}
	operatorContext := agentContext(t, operator.Subject, auth.Operator)
	result, err := service.Decide(operatorContext, receipt.Result.Proposal.ID, proposal.Approve, operator)
	if err != nil || !result.AuditCommitted || result.Execution != Succeeded {
		t.Fatalf("separate approval did not execute once after restart: %v, %v", result, err)
	}
	var executions int
	err = pool.QueryRow(proposer, `SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='execute'`, receipt.Result.Proposal.ID).Scan(&executions)
	if err != nil || executions != 1 {
		t.Fatalf("expected one execution audit entry, got %d: %v", executions, err)
	}
}

func phase4RestartSignal(t *testing.T, dir, send, wait string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, send), nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, wait)); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("restart orchestrator did not signal %s", wait)
}
