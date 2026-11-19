package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
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

func automaticBatch(t *testing.T, s *Service, now time.Time) AutomaticCommand {
	t.Helper()
	view, err := s.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return AutomaticCommand{
		RuntimeID: s.RuntimeID(), ObservationID: view.ObservationID, ExpiresAt: now.Add(automaticMaxTTL),
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
	s, pool, now, ctx := automaticFixture(t)
	cmd := automaticBatch(t, s, *now)
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
	s, pool, now, ctx := automaticFixture(t)
	before := s.Snapshot()
	cmd := automaticBatch(t, s, *now)
	rejected, err := s.ExecuteAutomatic(context.Background(), cmd)
	if !errors.Is(err, ErrUnauthorized) || rejected.Execution != NotExecuted || !rejected.AuditCommitted {
		t.Fatalf("unauthenticated attempt: %+v %v", rejected, err)
	}
	automaticAuditStatus(t, pool, rejected.AuditID, "rejected", "")
	cmd.AssignJobBatch.Assignments[0].JobID = "J99999"
	rejected, err = s.ExecuteAutomatic(ctx, cmd)
	if !errors.Is(err, sim.ErrAssignmentConflict) || rejected.Execution != NotExecuted || !rejected.AuditCommitted ||
		!reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatalf("unsafe attempt mutated state or missed its audit: %+v %v", rejected, err)
	}
	automaticAuditStatus(t, pool, rejected.AuditID, "rejected", "test-host")
}

func TestAutomaticAuditUncertaintyPoisonsRuntimeIntegration(t *testing.T) {
	s, pool, now, ctx := automaticFixture(t)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_automatic_finish() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test-only audit failure'; END; $$
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER fail_finish BEFORE UPDATE ON fleet_automatic_audit
		FOR EACH ROW EXECUTE FUNCTION fail_automatic_finish()`); err != nil {
		t.Fatal(err)
	}
	cmd := automaticBatch(t, s, *now)
	result, err := s.ExecuteAutomatic(ctx, cmd)
	if !errors.Is(err, ErrPersistence) || result.Execution != Unknown || result.AuditCommitted || !s.Poisoned() ||
		s.Snapshot().Jobs[0].AssignedRobot != "R1" || !errors.Is(s.Tick(), ErrPoisoned) {
		t.Fatalf("uncertain audit did not fail-stop: %+v %v", result, err)
	}
	automaticAuditStatus(t, pool, result.AuditID, "pending", "test-host")
}
