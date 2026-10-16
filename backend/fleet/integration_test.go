package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
	"github.com/jackc/pgx/v5"
)

func integrationPool(t *testing.T) *db.Pool {
	t.Helper()
	url := os.Getenv("FLEETOPS_TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("FLEETOPS_REQUIRE_DB") == "1" {
			t.Fatal("FLEETOPS_TEST_DATABASE_URL is required")
		}
		t.Skip("FLEETOPS_TEST_DATABASE_URL is unavailable")
	}
	cfg := db.DefaultConfig(url)
	admin, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	cfg.Schema = "fleetops_test_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{cfg.Schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	pool, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}

func integrationService(t *testing.T) (*Service, *countedCommander, *db.Pool, *time.Time) {
	t.Helper()
	pool := integrationPool(t)
	simulator, err := sim.New(testConfig(), 42)
	if err != nil {
		t.Fatal(err)
	}
	commander := &countedCommander{Sim: simulator}
	now := testTime
	s, err := newService(commander, simulator.Tick, pool, func() time.Time { return now }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	return s, commander, pool, &now
}

func submit(t *testing.T, s *Service) Result {
	t.Helper()
	r, err := s.Submit(context.Background(), chargingRequest, testTime.Add(time.Minute))
	if err != nil || !r.AuditCommitted || r.Proposal.State != proposal.Pending {
		t.Fatalf("submit: %+v, %v", r, err)
	}
	return r
}

func persisted(t *testing.T, pool *db.Pool, id string) (proposal.Proposal, Execution) {
	t.Helper()
	var payload []byte
	var execution Execution
	if err := pool.QueryRow(context.Background(), "SELECT payload,execution FROM fleet_proposals WHERE id=$1", id).Scan(&payload, &execution); err != nil {
		t.Fatal(err)
	}
	var p proposal.Proposal
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	return p, execution
}

func auditCount(t *testing.T, pool *db.Pool, id, attempt string, accepted bool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt=$2 AND accepted=$3", id, attempt, accepted).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIntegrationChargingVerticalSlice(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	created := submit(t, s)
	p, execution := persisted(t, pool, created.Proposal.ID)
	if p != created.Proposal || execution != NotExecuted || auditCount(t, pool, p.ID, "create", true) != 1 {
		t.Fatal("submission was not durably recorded")
	}
	before := s.Snapshot()
	r, err := s.Decide(trustedContext(), p.ID, proposal.Approve, testOperator)
	if err != nil || !r.AuditCommitted || !r.ExecutionAttempted || r.Execution != Succeeded || r.Proposal.State != proposal.Approved || commander.calls != 1 {
		t.Fatalf("approve: %+v, %v, calls=%d", r, err, commander.calls)
	}
	after := s.Snapshot()
	if after.Robots[0].Status != sim.RobotCharging || after.Stations[0].OccupiedBy != "R1" || before.Robots[0].Battery != after.Robots[0].Battery {
		t.Fatal("SetCharging state transition missing")
	}
	p, execution = persisted(t, pool, p.ID)
	if p != r.Proposal || execution != Succeeded || auditCount(t, pool, p.ID, "approve", true) != 1 || auditCount(t, pool, p.ID, "execute", true) != 1 {
		t.Fatal("committed approval/execution audit missing")
	}
	r, err = s.Decide(trustedContext(), p.ID, proposal.Approve, testOperator)
	if !errors.Is(err, proposal.ErrInvalidTransition) || !r.AuditCommitted || r.ExecutionAttempted || commander.calls != 1 || auditCount(t, pool, p.ID, "approve", false) != 1 {
		t.Fatalf("duplicate approval: %+v, %v", r, err)
	}
}

func TestIntegrationPublicServiceOwnsSimulator(t *testing.T) {
	t.Parallel()
	pool := integrationPool(t)
	s, err := New(testConfig(), 42, pool, func() time.Time { return testTime }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	r := submit(t, s)
	if _, err := s.Decide(trustedContext(), r.Proposal.ID, proposal.Approve, testOperator); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Robots[0].Status != sim.RobotCharging {
		t.Fatal("public facade did not execute")
	}
}

func TestIntegrationConcurrentApprovalsExecuteOnce(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	id := submit(t, s).Proposal.ID
	var workers sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator)
			results <- err
		}()
	}
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, proposal.ErrInvalidTransition) {
			t.Fatal(err)
		}
	}
	if successes != 1 || commander.calls != 1 || auditCount(t, pool, id, "approve", false) != 11 || auditCount(t, pool, id, "execute", true) != 1 {
		t.Fatalf("successes=%d calls=%d", successes, commander.calls)
	}
}

func TestIntegrationRejectedExpiredStaleAndUnauthorized(t *testing.T) {
	for _, name := range []string{"rejected", "expired", "stale", "unauthorized"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, commander, pool, now := integrationService(t)
			id := submit(t, s).Proposal.ID
			ctx := trustedContext()
			want := proposal.ErrInvalidTransition
			switch name {
			case "rejected":
				r, err := s.Decide(ctx, id, proposal.Reject, testOperator)
				if err != nil || r.Proposal.State != proposal.Rejected || !r.AuditCommitted || auditCount(t, pool, id, "reject", true) != 1 {
					t.Fatalf("reject: %+v, %v", r, err)
				}
			case "expired":
				*now = testTime.Add(time.Minute)
				want = proposal.ErrExpired
			case "stale":
				for range 71 {
					if err := s.Tick(); err != nil {
						t.Fatal(err)
					}
				}
				want = safety.ErrBatteryBelowMinimum
			case "unauthorized":
				ctx = context.Background()
				want = ErrUnauthorized
			}
			before := s.Snapshot()
			r, err := s.Decide(ctx, id, proposal.Approve, testOperator)
			if !errors.Is(err, want) || !r.AuditCommitted || r.ExecutionAttempted || commander.calls != 0 || !reflect.DeepEqual(before, s.Snapshot()) || auditCount(t, pool, id, "approve", false) != 1 {
				t.Fatalf("denied approval: %+v, %v, calls=%d", r, err, commander.calls)
			}
			stored, _ := persisted(t, pool, id)
			if name == "expired" && stored.State != proposal.Expired {
				t.Fatal("expiration was not persisted")
			}
		})
	}
}

func TestIntegrationBatteryBoundaryAndFailedSubmission(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	for range 70 {
		if err := s.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	if s.Snapshot().Robots[0].Battery != 30 {
		t.Fatal("boundary fixture is not at 30%")
	}
	r := submit(t, s)
	if _, err := s.Decide(trustedContext(), r.Proposal.ID, proposal.Approve, testOperator); err != nil {
		t.Fatal(err)
	}
	if commander.calls != 1 {
		t.Fatal("30% boundary did not execute")
	}
	invalid, err := s.Submit(context.Background(), safety.SetChargingRequest{RobotID: "missing", StationID: "S1"}, testTime.Add(time.Minute))
	if !errors.Is(err, sim.ErrUnknownRobot) || !invalid.AuditCommitted || auditCount(t, pool, invalid.Proposal.ID, "create", false) != 1 {
		t.Fatalf("invalid submission: %+v, %v", invalid, err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM fleet_proposals WHERE id=$1", invalid.Proposal.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid proposal was admitted")
	}
}

func TestIntegrationRevalidatesAtExecutionBoundary(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	id := submit(t, s).Proposal.ID
	commander.staleAtRead = 3 // submission, approval preflight, execution preflight.
	r, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator)
	if !errors.Is(err, safety.ErrBatteryBelowMinimum) || !r.AuditCommitted || r.ExecutionAttempted || commander.calls != 0 || auditCount(t, pool, id, "execute", false) != 1 {
		t.Fatalf("execution revalidation: %+v, %v", r, err)
	}
}

func TestIntegrationFailedExecutionIsDurablyReported(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	id := submit(t, s).Proposal.ID
	before := s.Snapshot()
	commander.fail = true
	r, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator)
	if !errors.Is(err, ErrExecution) || !r.AuditCommitted || !r.ExecutionAttempted || r.Execution != Failed || commander.calls != 1 || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatalf("execution failure: %+v, %v", r, err)
	}
	_, execution := persisted(t, pool, id)
	if execution != Failed || auditCount(t, pool, id, "execute", false) != 1 {
		t.Fatal("execution failure was not persisted")
	}
	if _, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator); !errors.Is(err, proposal.ErrInvalidTransition) || commander.calls != 1 {
		t.Fatal("failed execution was retried")
	}
}

func TestIntegrationPersistenceFailureBeforeExecution(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	id := submit(t, s).Proposal.ID
	_, err := pool.Exec(context.Background(), `CREATE FUNCTION deny_approval() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.attempt='approve' THEN RAISE EXCEPTION 'test approval persistence failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER deny_approval BEFORE INSERT ON fleet_audit FOR EACH ROW EXECUTE FUNCTION deny_approval();`)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	r, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator)
	if !errors.Is(err, ErrPersistence) || r.AuditCommitted || r.ExecutionAttempted || commander.calls != 0 || !s.Poisoned() || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatalf("pre-execution persistence failure: %+v, %v", r, err)
	}
	p, _ := persisted(t, pool, id)
	if p.State != proposal.Pending || auditCount(t, pool, id, "approve", true) != 0 {
		t.Fatal("uncommitted approval was reported as durable")
	}
}

func TestIntegrationCommitFailureAfterMutationPoisonsRuntime(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	id := submit(t, s).Proposal.ID
	_, err := pool.Exec(context.Background(), `CREATE FUNCTION fail_execution_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.attempt='execute' THEN RAISE EXCEPTION 'test deferred commit failure'; END IF; RETURN NEW; END $$;
 CREATE CONSTRAINT TRIGGER fail_execution_commit AFTER INSERT ON fleet_audit DEFERRABLE INITIALLY DEFERRED
 FOR EACH ROW EXECUTE FUNCTION fail_execution_commit();`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator)
	if !errors.Is(err, ErrPersistence) || !errors.Is(err, ErrPoisoned) || r.AuditCommitted || !r.ExecutionAttempted || r.Execution != Unknown || !s.Poisoned() || commander.calls != 1 {
		t.Fatalf("ambiguous mutation: %+v, %v", r, err)
	}
	if s.Snapshot().Robots[0].Status != sim.RobotCharging {
		t.Fatal("fixture did not mutate before COMMIT failed")
	}
	p, execution := persisted(t, pool, id)
	if p.State != proposal.Pending || execution != NotExecuted || auditCount(t, pool, id, "execute", true) != 0 {
		t.Fatal("rolled-back audit claimed durable execution")
	}
	before := s.Snapshot()
	if err := s.Tick(); !errors.Is(err, ErrPoisoned) {
		t.Fatal("poisoned runtime ticked")
	}
	r, err = s.Decide(trustedContext(), id, proposal.Approve, testOperator)
	if !errors.Is(err, ErrPoisoned) || !r.AuditCommitted || r.ExecutionAttempted || commander.calls != 1 || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("poisoned runtime retried execution")
	}
	if auditCount(t, pool, id, "approve", false) != 1 {
		t.Fatal("poisoned refusal was not audited when persistence was available")
	}
}

func TestIntegrationForeignRuntimeCannotReplayProposal(t *testing.T) {
	t.Parallel()
	s, commander, pool, _ := integrationService(t)
	id := submit(t, s).Proposal.ID
	other, err := New(testConfig(), 42, pool, func() time.Time { return testTime }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	r, err := other.Decide(trustedContext(), id, proposal.Approve, testOperator)
	if !errors.Is(err, ErrForeignRuntime) || !r.AuditCommitted || r.ExecutionAttempted || other.Snapshot().Robots[0].Status != sim.RobotIdle {
		t.Fatalf("foreign runtime: %+v, %v", r, err)
	}
	if _, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator); err != nil || commander.calls != 1 {
		t.Fatalf("owning runtime: %v", err)
	}
}
