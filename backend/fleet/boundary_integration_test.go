package fleet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/proposal"
)

func TestIntegrationExecutionBoundaryExpiryAndAuthorization(t *testing.T) {
	for _, name := range []string{"expiry", "revoked"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, commander, pool, now := integrationService(t)
			id := submit(t, s).Proposal.ID
			checks := 0
			s.authorize = func(ctx context.Context, operator proposal.OperatorIdentity) bool {
				checks++
				if checks == 2 {
					if name == "expiry" {
						*now = testTime.Add(time.Minute)
					} else {
						return false
					}
				}
				return authorized(ctx, operator)
			}
			want := proposal.ErrExpired
			if name == "revoked" {
				want = ErrUnauthorized
			}
			r, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator)
			if !errors.Is(err, want) || !r.AuditCommitted || r.ExecutionAttempted || commander.calls != 0 || auditCount(t, pool, id, "execute", false) != 1 {
				t.Fatalf("execution boundary: %+v, %v", r, err)
			}
			p, _ := persisted(t, pool, id)
			if name == "expiry" && p.State != proposal.Expired {
				t.Fatal("boundary expiration was not persisted")
			}
			_, err = s.Decide(trustedContext(), id, proposal.Approve, testOperator)
			if name == "revoked" && !errors.Is(err, proposal.ErrInvalidTransition) {
				t.Fatalf("consumed approval was replayed or canonical view diverged: %v", err)
			}
			if commander.calls != 0 {
				t.Fatal("denied boundary was retried")
			}
		})
	}
}

func TestIntegrationWriteFailureAfterMutationPoisonsRuntime(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	id := submit(t, s).Proposal.ID
	_, err := pool.Exec(context.Background(), `CREATE FUNCTION fail_execution_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.attempt='execute' THEN RAISE EXCEPTION 'test post-execution write failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER fail_execution_update BEFORE UPDATE ON fleet_audit FOR EACH ROW EXECUTE FUNCTION fail_execution_update();`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator)
	if !errors.Is(err, ErrPersistence) || !s.Poisoned() || r.AuditCommitted || !r.ExecutionAttempted || r.Execution != Unknown || commander.calls != 1 {
		t.Fatalf("post-mutation failure: %+v, %v", r, err)
	}
	stored, _ := persisted(t, pool, id)
	if stored.State != proposal.Pending || auditCount(t, pool, id, "execute", true) != 0 {
		t.Fatal("uncommitted execution was claimed durable")
	}
}

func TestIntegrationSubmissionCommitFailureIsNotAcknowledged(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	_, err := pool.Exec(context.Background(), `CREATE FUNCTION fail_submission_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 RAISE EXCEPTION 'test submission commit failure'; RETURN NEW; END $$;
 CREATE CONSTRAINT TRIGGER fail_submission_commit AFTER INSERT ON fleet_proposals DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION fail_submission_commit();`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Submit(context.Background(), chargingRequest, testTime.Add(time.Minute))
	if !errors.Is(err, ErrPersistence) || !s.Poisoned() || r.AuditCommitted || r.ExecutionAttempted || commander.calls != 0 {
		t.Fatalf("submission commit failure: %+v, %v", r, err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM fleet_proposals").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed submission was persisted")
	}
	if auditCount(t, pool, r.Proposal.ID, "create", true) != 0 {
		t.Fatal("uncommitted submission audit claimed durable")
	}
}

func TestIntegrationUnknownAndTamperedProposalsAreAudited(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	r, err := s.Decide(trustedContext(), "missing", proposal.Approve, testOperator)
	if !errors.Is(err, proposal.ErrUnknownProposal) || !r.AuditCommitted || auditCount(t, pool, "missing", "approve", false) != 1 {
		t.Fatalf("unknown proposal: %+v, %v", r, err)
	}
	id := submit(t, s).Proposal.ID
	_, err = pool.Exec(context.Background(), `UPDATE fleet_proposals SET payload=jsonb_set(payload,'{Request,StationID}','"S2"') WHERE id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.Decide(trustedContext(), id, proposal.Approve, testOperator)
	if !errors.Is(err, ErrProposalMismatch) || !r.AuditCommitted || r.ExecutionAttempted || commander.calls != 0 || auditCount(t, pool, id, "approve", false) != 1 {
		t.Fatalf("tampered proposal: %+v, %v", r, err)
	}
}

func TestIntegrationCompletedProposalRemainsNonExecutableAfterExpiry(t *testing.T) {
	t.Parallel()
	s, commander, pool, now := integrationService(t)
	id := submit(t, s).Proposal.ID
	if _, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator); err != nil {
		t.Fatal(err)
	}
	*now = testTime.Add(time.Minute)
	r, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator)
	if !errors.Is(err, proposal.ErrExpired) || !r.AuditCommitted || r.ExecutionAttempted || s.Poisoned() || commander.calls != 1 {
		t.Fatalf("completed expiration: %+v, %v", r, err)
	}
	p, execution := persisted(t, pool, id)
	if p.State != proposal.Expired || execution != Succeeded {
		t.Fatal("expiration erased the historical execution outcome")
	}
}
