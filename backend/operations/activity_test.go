package operations

import (
	"context"
	"testing"
	"time"
)

func TestOperationsActivityAuditPreservesScopeAndProgressIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testDatabase(t)
	ledger, err := openLedger(ctx, pool, pricing{minInputRate, minOutputRate})
	if err != nil {
		t.Fatal(err)
	}
	manager := Manager{ledger: ledger}
	owner, other := runtimeFixture(t), runtimeFixture(t)
	observed := Activity{ObservationID: runtimeFixture(t), ObservedAt: time.Now().UTC(), Tick: 5,
		Stage: "observed", Status: "observed", Recorded: true}
	if err := manager.RecordActivity(ctx, owner, observed); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecordActivity(ctx, owner, observed); err == nil {
		t.Fatal("duplicate observation overwritten")
	}
	finished := observed
	finished.Stage, finished.Status = "finished", "budget_denied"
	if err := manager.RecordActivity(ctx, other, finished); err == nil {
		t.Fatal("a foreign runtime changed the audit")
	}
	if err := manager.RecordActivity(ctx, owner, finished); err != nil {
		t.Fatal(err)
	}
	if err := manager.RecordActivity(ctx, owner, finished); err == nil {
		t.Fatal("a completed audit was overwritten")
	}
	var stage int16
	var status string
	if err := pool.QueryRow(ctx, "SELECT stage,payload->>'status' FROM fleetops_operations.activity WHERE runtime_id=$1 AND observation_id=$2",
		owner, observed.ObservationID).Scan(&stage, &status); err != nil || stage != 3 || status != "budget_denied" {
		t.Fatalf("durable outcome was not recorded: %d %q %v", stage, status, err)
	}
}
