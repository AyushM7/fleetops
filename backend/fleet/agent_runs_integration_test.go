//go:build fleetops_test

package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func agentContext(t *testing.T, subject string, role auth.Role) context.Context {
	t.Helper()
	token := strings.Repeat("isolated-agent-test-", 3)
	hash := sha256.Sum256([]byte(token))
	raw, _ := json.Marshal([]auth.Credential{{Subject: subject, Role: role, SHA256: hex.EncodeToString(hash[:])}})
	a, err := auth.New(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := a.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
func agentRunFixture(t *testing.T) (*Service, *db.Pool, *AgentRun, AgentFinish) {
	t.Helper()
	pool := integrationPool(t)
	simulator, err := sim.NewReassignmentTestFixture()
	if err != nil {
		t.Fatal(err)
	}
	s, err := newService(simulator, simulator.Tick, pool, func() time.Time { return testTime }, func(ctx context.Context, op proposal.OperatorIdentity) bool {
		p, ok := auth.FromContext(ctx)
		return ok && p.Role == auth.Operator && p.Subject == op.Subject
	})
	if err != nil {
		t.Fatal(err)
	}
	view, _ := s.ReadState(context.Background())
	run, err := s.StartAgentRun(agentContext(t, "agent-owner", auth.Proposer), view.ObservationID, AgentMetadata{"agent-run-v1", "recovery-decision-v2", "compare_tradeoffs", "mocked", "offline/test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Facts.Candidates) < 1 {
		t.Fatal("missing admitted candidates")
	}
	chosen := run.Facts.Candidates[len(run.Facts.Candidates)-1]
	decision := &AgentDecision{Kind: "select", Selected: &chosen.Key, Basis: "admitted_option", Comparisons: []AgentComparison{}}
	for _, other := range run.Facts.Candidates {
		if other.Key == chosen.Key {
			continue
		}
		l, r := chosen.Metrics["pickupSteps"], other.Metrics["pickupSteps"]
		relation := "equal"
		if l < r {
			relation = "less"
		} else if l > r {
			relation = "greater"
		}
		decision.Comparisons = append(decision.Comparisons, AgentComparison{chosen.Key, other.Key, "pickupSteps", l, r, relation})
	}
	finish := AgentFinish{RuntimeID: run.RuntimeID, ObservationID: run.ObservationID, Completion: AgentCompletion{Status: "accepted", Failure: "none", SubmissionCheck: "current", LatencyMS: 12, Requests: 4, Tools: 6, Usage: AgentUsage{Status: "unavailable"}, Decision: decision}, Submit: &AgentSubmission{ExpiresAt: testTime.Add(time.Minute)}}
	return s, pool, run, finish
}
func TestIntegrationAgentRunAtomicProposalAndApproval(t *testing.T) {
	s, pool, run, input := agentRunFixture(t)
	ctx := agentContext(t, "agent-owner", auth.Proposer)
	before := s.Snapshot()
	receipt, err := s.FinishAgentRun(ctx, run.ID, input)
	if err != nil || !receipt.Recorded || receipt.Result.Proposal.State != proposal.Pending || receipt.Result.ExecutionAttempted {
		t.Fatal(receipt, err)
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("agent executed a command")
	}
	var linked int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM fleet_agent_runs a JOIN fleet_proposals p ON a.proposal_id=p.id WHERE a.id=$1 AND p.submitted_by='agent-owner' AND a.completed_at IS NOT NULL`, run.ID).Scan(&linked); err != nil || linked != 1 {
		t.Fatal(linked, err)
	}
	rows, err := s.ReadAgentRuns(ctx, run.ID, 1)
	if err != nil || len(rows) != 1 || !rows[0].DecisionVerified || rows[0].Proposal == nil || rows[0].CostStatus != "unavailable" {
		t.Fatal(rows, err)
	}
	if _, err = s.Decide(agentContext(t, "agent-owner", auth.Operator), receipt.Result.Proposal.ID, proposal.Approve, proposal.OperatorIdentity{Subject: "agent-owner"}); !errors.Is(err, ErrSelfApproval) {
		t.Fatal(err)
	}
	if _, err = s.Decide(agentContext(t, "reviewer", auth.Operator), receipt.Result.Proposal.ID, proposal.Approve, proposal.OperatorIdentity{Subject: "reviewer"}); err != nil {
		t.Fatal(err)
	}
}
func TestIntegrationAgentRunIdempotencyAndUncertainty(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "concurrent_duplicate", true: "lost_commit_ack"}[uncertain], func(t *testing.T) {
			s, pool, run, input := agentRunFixture(t)
			ctx := agentContext(t, "agent-owner", auth.Proposer)
			if uncertain {
				s.transact = func(ctx context.Context, body func(*db.Tx) error) error {
					if err := pool.Transaction(ctx, body); err != nil {
						return err
					}
					return db.ErrQuery
				}
				r, err := s.FinishAgentRun(ctx, run.ID, input)
				if !errors.Is(err, ErrPersistence) || r.Recorded || !s.Poisoned() {
					t.Fatal(r, err)
				}
				s.transact = pool.Transaction
			}
			var wg sync.WaitGroup
			receipts := make(chan AgentReceipt, 8)
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, err := s.FinishAgentRun(ctx, run.ID, input)
					if err != nil {
						t.Error(err)
					}
					receipts <- r
				}()
			}
			wg.Wait()
			close(receipts)
			ids := map[string]bool{}
			for r := range receipts {
				if !r.Recorded {
					t.Fatal(r)
				}
				ids[r.Result.Proposal.ID] = true
			}
			var count int
			pool.QueryRow(ctx, `SELECT count(*) FROM fleet_proposals`).Scan(&count)
			if count != 1 || len(ids) != 1 {
				t.Fatal("duplicate proposals", count, ids)
			}
			input.Completion.LatencyMS++
			if _, err := s.FinishAgentRun(ctx, run.ID, input); !errors.Is(err, ErrAgentRecord) {
				t.Fatal("altered replay accepted", err)
			}
		})
	}
}
func TestIntegrationAgentRunPersistenceFailureRollsBackProposal(t *testing.T) {
	s, pool, run, input := agentRunFixture(t)
	ctx := agentContext(t, "agent-owner", auth.Proposer)
	before := s.Snapshot()
	_, err := pool.Exec(ctx, `CREATE FUNCTION fail_agent_complete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-bearer-secret'; END $$; CREATE CONSTRAINT TRIGGER fail_agent_complete AFTER UPDATE ON fleet_agent_runs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fail_agent_complete()`)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.FinishAgentRun(ctx, run.ID, input)
	if !errors.Is(err, ErrPersistence) || receipt.Recorded || !s.Poisoned() || strings.Contains(err.Error(), "secret") {
		t.Fatal(receipt, err)
	}
	var count int
	pool.QueryRow(ctx, `SELECT count(*) FROM fleet_proposals`).Scan(&count)
	if count != 0 || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("rollback crossed command boundary", count)
	}
	var completed *time.Time
	pool.QueryRow(ctx, `SELECT completed_at FROM fleet_agent_runs WHERE id=$1`, run.ID).Scan(&completed)
	if completed != nil {
		t.Fatal("completion survived rollback")
	}
}
func TestIntegrationAgentRunRejectsInvalidAndForeignRecords(t *testing.T) {
	for _, mode := range []string{"missing runtime", "foreign runtime", "missing observation", "foreign observation", "forged candidate", "false comparison", "unrelated owner", "unknown run", "extra proposal association", "bad usage"} {
		t.Run(mode, func(t *testing.T) {
			s, _, run, input := agentRunFixture(t)
			ctx := agentContext(t, "agent-owner", auth.Proposer)
			before := s.Snapshot()
			id := run.ID
			switch mode {
			case "missing runtime":
				input.RuntimeID = ""
			case "foreign runtime":
				input.RuntimeID = "foreign"
			case "missing observation":
				input.ObservationID = ""
			case "foreign observation":
				input.ObservationID = strings.Repeat("f", 64)
			case "forged candidate":
				input.Completion.Decision.Selected.ToRobotID = "fabricated"
			case "false comparison":
				if len(input.Completion.Decision.Comparisons) > 0 {
					input.Completion.Decision.Comparisons[0].LeftValue++
				} else {
					input.Completion.Decision.Comparisons = append(input.Completion.Decision.Comparisons, AgentComparison{})
				}
			case "unrelated owner":
				ctx = agentContext(t, "other", auth.Proposer)
			case "unknown run":
				id = "Aunknown"
			case "extra proposal association":
				var out AgentFinish
				if DecodeAgentJSON(`{"proposalId":"unrelated","runtimeId":"x"}`, &out) == nil {
					t.Fatal("forged field accepted")
				}
				return
			case "bad usage":
				input.Completion.Usage.Status = "estimated"
			}
			r, err := s.FinishAgentRun(ctx, id, input)
			if !errors.Is(err, ErrAgentRecord) || r.Recorded || !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatal(r, err)
			}
		})
	}
}
func TestIntegrationAgentRunFailuresAbstentionAndPrivacy(t *testing.T) {
	for _, failure := range []string{"timeout", "budget_exhausted", "grounding_rejected", "investigation_failed", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			s, _, run, input := agentRunFixture(t)
			ctx := agentContext(t, "agent-owner", auth.Proposer)
			input.Submit = nil
			input.Completion.SubmissionCheck = "not_requested"
			input.Completion.Status = "failed"
			if failure == "grounding_rejected" {
				input.Completion.Status = "rejected"
			}
			input.Completion.Failure = failure
			input.Completion.Decision = nil
			r, err := s.FinishAgentRun(ctx, run.ID, input)
			if err != nil || !r.Recorded || r.Result.Proposal.ID != "" {
				t.Fatal(r, err)
			}
			rows, err := s.ReadAgentRuns(ctx, run.ID, 1)
			if err != nil || len(rows) != 1 || rows[0].ProposalID != nil || rows[0].Completion.Status == "accepted" {
				t.Fatal(rows, err)
			}
			for _, subject := range []string{"other", "viewer"} {
				role := auth.Proposer
				if subject == "viewer" {
					role = auth.Viewer
				}
				rows, err := s.ReadAgentRuns(agentContext(t, subject, role), run.ID, 1)
				if len(rows) != 0 || (subject == "viewer" && !errors.Is(err, ErrUnauthorized)) {
					t.Fatal("private record leaked", rows, err)
				}
			}
			s.runtimeID = "new-runtime"
			rows, err = s.ReadAgentRuns(ctx, run.ID, 1)
			if err != nil || len(rows) != 0 {
				t.Fatal("foreign runtime leaked", rows, err)
			}
		})
	}
	s, _, _, _ := integrationService(t)
	ctx := agentContext(t, "agent-owner", auth.Proposer)
	view, _ := s.ReadState(ctx)
	run, err := s.StartAgentRun(ctx, view.ObservationID, AgentMetadata{"agent-run-v1", "recovery-decision-v2", "compare_tradeoffs", "mocked", "offline/test"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.FinishAgentRun(ctx, run.ID, AgentFinish{RuntimeID: run.RuntimeID, ObservationID: run.ObservationID, Completion: AgentCompletion{Status: "accepted", Failure: "none", SubmissionCheck: "not_requested", Usage: AgentUsage{Status: "unavailable"}, Decision: &AgentDecision{Kind: "abstain", Reason: "no_supported_incident", Comparisons: []AgentComparison{}}}})
	if err != nil || !r.Recorded || r.Result.Proposal.ID != "" {
		t.Fatal(r, err)
	}
}
func TestIntegrationAgentRunStaleObservationIsDurableDenial(t *testing.T) {
	s, _, run, input := agentRunFixture(t)
	ctx := agentContext(t, "agent-owner", auth.Proposer)
	if err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	r, err := s.FinishAgentRun(ctx, run.ID, input)
	if err != nil || !r.Recorded || r.Denial != "stale_observation" || r.Result.ExecutionAttempted || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal(r, err)
	}
	if _, err := s.StartAgentRun(ctx, run.ObservationID, run.Metadata); !errors.Is(err, ErrObservation) {
		t.Fatal(err)
	}
}
