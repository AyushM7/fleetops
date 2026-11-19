package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

func automaticFixture(t *testing.T) (*Service, *db.Pool, *time.Time, context.Context) {
	t.Helper()
	pool := integrationPool(t)
	cfg := sim.WarehouseV2Config()
	cfg.AutoDispatch = false
	now := testTime
	s, err := New(cfg, 42, pool, func() time.Time { return now }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAutomaticCommandsEnabled(true); err != nil {
		t.Fatal(err)
	}
	return s, pool, &now, automaticContext(t, s.RuntimeID())
}

func automaticContext(t *testing.T, runtimeID string) context.Context {
	t.Helper()
	token := strings.Repeat("isolated-automation-fixture-", 2)
	hash := sha256.Sum256([]byte(token))
	raw, err := json.Marshal([]auth.Credential{{Subject: "test-host", Role: auth.Automatic, RuntimeID: runtimeID, SHA256: hex.EncodeToString(hash[:])}})
	if err != nil {
		t.Fatal(err)
	}
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

func automaticBatch(t *testing.T, s *Service, ctx context.Context) AutomaticCommand {
	t.Helper()
	view, err := s.ReadAutomaticState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return AutomaticCommand{
		RuntimeID: view.RuntimeID, ObservationID: view.ObservationID, ExpiresAt: view.ExpiresAt,
		IdempotencyKey: "assign-test-key",
		Preconditions:  map[string]uint64{"J1": view.Versions["J1"], "R1": view.Versions["R1"]},
		AssignJobBatch: &sim.AssignJobBatchRequest{Assignments: []sim.JobAssignment{{JobID: "J1", RobotID: "R1"}}},
	}
}

func automaticAuditStatus(t *testing.T, pool *db.Pool, id int64, want, subject string) {
	t.Helper()
	var status, actualSubject string
	var finished bool
	err := pool.QueryRow(context.Background(), "SELECT status, subject, finished_at IS NOT NULL FROM fleet_automatic_audit WHERE id=$1", id).Scan(&status, &actualSubject, &finished)
	if err != nil || status != want || actualSubject != subject || finished != (want != "pending") {
		t.Fatalf("audit status=%q subject=%q finished=%v error=%v, want status %q subject %q", status, actualSubject, finished, err, want, subject)
	}
}

func TestAutomaticExecutionWithoutApprovalIntegration(t *testing.T) {
	s, pool, _, ctx := automaticFixture(t)
	cmd := automaticBatch(t, s, ctx)
	result, err := s.ExecuteAutomatic(ctx, cmd)
	if err != nil || result.Execution != Succeeded || !result.AuditCommitted || s.Snapshot().Jobs[0].AssignedRobot != "R1" {
		t.Fatalf("automatic execution: %+v %v", result, err)
	}
	automaticAuditStatus(t, pool, result.AuditID, "applied", "test-host")
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM fleet_proposals").Scan(&count); err != nil || count != 0 {
		t.Fatalf("automatic execution created an approval: count=%d %v", count, err)
	}
}

func TestAutomaticDenialsAreAuditedIntegration(t *testing.T) {
	s, pool, _, ctx := automaticFixture(t)
	before := s.Snapshot()
	cmd := automaticBatch(t, s, ctx)
	rejected, err := s.ExecuteAutomatic(context.Background(), cmd)
	if !errors.Is(err, ErrUnauthorized) || rejected.Execution != NotExecuted || !rejected.AuditCommitted {
		t.Fatalf("unauthenticated attempt: %+v %v", rejected, err)
	}
	automaticAuditStatus(t, pool, rejected.AuditID, "rejected", "")
	read, err := s.ReadAutomaticState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cmd.ObservationID, cmd.ExpiresAt = read.ObservationID, read.ExpiresAt
	cmd.AssignJobBatch = nil
	cmd.ReassignUnstartedJob = &safety.ReassignUnstartedJobRequest{JobID: "J1", FromRobotID: "R1", ToRobotID: "R2"}
	cmd.Preconditions["R2"] = read.Versions["R2"]
	rejected, err = s.ExecuteAutomatic(ctx, cmd)
	if !errors.Is(err, sim.ErrReassignment) || rejected.Execution != NotExecuted || !rejected.AuditCommitted ||
		!reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatalf("unsafe attempt mutated state or missed its audit: %+v %v", rejected, err)
	}
	automaticAuditStatus(t, pool, rejected.AuditID, "rejected", "test-host")
}

func TestAutomaticAuditUncertaintyPoisonsRuntimeIntegration(t *testing.T) {
	s, pool, _, ctx := automaticFixture(t)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_automatic_finish() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test-only audit failure'; END; $$
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER fail_finish BEFORE UPDATE ON fleet_automatic_audit
		FOR EACH ROW EXECUTE FUNCTION fail_automatic_finish()`); err != nil {
		t.Fatal(err)
	}
	cmd := automaticBatch(t, s, ctx)
	result, err := s.ExecuteAutomatic(ctx, cmd)
	if !errors.Is(err, ErrPersistence) || result.Execution != Unknown || result.AuditCommitted || !s.Poisoned() ||
		s.Snapshot().Jobs[0].AssignedRobot != "R1" || !errors.Is(s.Tick(), ErrPoisoned) {
		t.Fatalf("uncertain audit did not fail-stop: %+v %v", result, err)
	}
	automaticAuditStatus(t, pool, result.AuditID, "pending", "test-host")
	replay, err := s.ExecuteAutomatic(ctx, cmd)
	if !errors.Is(err, ErrPoisoned) || replay.Execution != Unknown || replay.AuditCommitted || replay.PriorAuditID != result.AuditID {
		t.Fatalf("an uncertain command was retried or resolved prematurely: %+v %v", replay, err)
	}
	automaticAuditStatus(t, pool, replay.AuditID, "replayed", "test-host")
}

func TestAutomaticVersionsAndReplaysAcrossTicksIntegration(t *testing.T) {
	s, pool, now, ctx := automaticFixture(t)
	safe := automaticBatch(t, s, ctx)
	stale := safe
	stale.IdempotencyKey = "different-attempt"
	if err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Second)
	applied, err := s.ExecuteAutomatic(ctx, safe)
	if err != nil || applied.Execution != Succeeded {
		t.Fatalf("unrelated tick invalidated the command: %+v %v", applied, err)
	}
	before := s.Snapshot()
	denied, err := s.ExecuteAutomatic(ctx, stale)
	if !errors.Is(err, ErrObservation) || denied.Execution != NotExecuted || !denied.AuditCommitted ||
		!reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatalf("changed entity allowed a stale command: %+v %v", denied, err)
	}
	changed := safe
	changed.ExpiresAt = safe.ExpiresAt.Add(-time.Second)
	if _, err := s.ExecuteAutomatic(ctx, changed); !errors.Is(err, ErrAutomaticKeyConflict) {
		t.Fatalf("different request reused a key: %v", err)
	}

	*now = safe.ExpiresAt.Add(time.Second)
	replayed, err := s.ExecuteAutomatic(ctx, safe)
	if err != nil || !replayed.AuditCommitted || replayed.Execution != Succeeded || replayed.PriorAuditID != applied.AuditID ||
		!reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatalf("retry after expiry did not reuse its durable result: %+v %v", replayed, err)
	}
	automaticAuditStatus(t, pool, replayed.AuditID, "replayed", "test-host")
}

func TestAutomaticRevalidatesTTLAtExecutionIntegration(t *testing.T) {
	s, pool, now, ctx := automaticFixture(t)
	cmd := automaticBatch(t, s, ctx)
	transact := s.transact
	writes := 0
	s.transact = func(ctx context.Context, body func(*db.Tx) error) error {
		err := transact(ctx, body)
		writes++
		if writes == 1 {
			*now = cmd.ExpiresAt
		}
		return err
	}
	before := s.Snapshot()
	result, err := s.ExecuteAutomatic(ctx, cmd)
	if !errors.Is(err, ErrAutomaticExpired) || result.Execution != NotExecuted || !result.AuditCommitted ||
		!reflect.DeepEqual(s.Snapshot(), before) || writes != 2 {
		t.Fatalf("expiry at the execution boundary was ignored: %+v %v", result, err)
	}
	automaticAuditStatus(t, pool, result.AuditID, "rejected", "test-host")
}

func TestAutomaticConcurrentDuplicateExecutesOnceIntegration(t *testing.T) {
	s, pool, _, ctx := automaticFixture(t)
	cmd := automaticBatch(t, s, ctx)
	const attempts = 6
	output := make(chan AutomaticResult, attempts)
	var workers sync.WaitGroup
	for range attempts {
		workers.Go(func() {
			result, err := s.ExecuteAutomatic(ctx, cmd)
			if err != nil || result.Execution != Succeeded || !result.AuditCommitted {
				t.Errorf("concurrent execution: %+v %v", result, err)
			}
			output <- result
		})
	}
	workers.Wait()
	close(output)
	executions := 0
	for receipt := range output {
		if receipt.PriorAuditID == 0 {
			executions++
		}
	}
	var applied, replays int
	err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='applied'),
		count(*) FILTER (WHERE status='replayed') FROM fleet_automatic_audit`).Scan(&applied, &replays)
	if err != nil || applied != 1 || replays != attempts-1 || executions != 1 {
		t.Fatalf("duplicate received a second execution: receipts=%d applied=%d replays=%d %v", executions, applied, replays, err)
	}
}
