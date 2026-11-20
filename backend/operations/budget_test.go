package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
)

func testDatabase(t *testing.T) *db.Pool {
	t.Helper()
	connection := os.Getenv("FLEETOPS_TEST_DATABASE_URL")
	if connection == "" && os.Getenv("FLEETOPS_REQUIRE_DB") != "1" {
		t.Skip("FLEETOPS_TEST_DATABASE_URL not configured")
	}
	ctx := context.Background()
	adminConfig := db.DefaultConfig(connection)
	// Dropping a database can wait for a checkpoint after other integration tests write WAL.
	adminConfig.QueryTimeout = 30 * time.Second
	admin, err := db.Open(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	suffix := make([]byte, 12)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "fleetops_operations_test_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP DATABASE "+name); err != nil {
			t.Error(err)
		}
	})
	uri, err := url.Parse(connection)
	if err != nil {
		t.Fatal(err)
	}
	uri.Path = "/" + name
	pool, err := db.Open(ctx, db.DefaultConfig(uri.String()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func runtimeFixture(t *testing.T) string {
	t.Helper()
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	return "P" + hex.EncodeToString(random)
}

func TestReservationUsesWorstReviewedPricing(t *testing.T) {
	p := pricing{minInputRate, minOutputRate}
	if !p.valid() || p.cost(maxInputTokens, maxOutputTokens) != 289384 || p.cost(1, 0) != 1 ||
		(pricing{1, 1}).valid() || (pricing{}).valid() {
		t.Fatal("invalid or non-conservative model price bound")
	}
}

func TestDailyLedgerIsAtomicAcrossVisitorsIntegration(t *testing.T) {
	ctx := context.Background()
	dbPool := testDatabase(t)
	p := pricing{minInputRate, minOutputRate}
	first, err := openLedger(ctx, dbPool, p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openLedger(ctx, dbPool, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbPool.Exec(ctx, `INSERT INTO fleetops_operations.days (utc_day,micro_usd,frozen)
		VALUES ((clock_timestamp() AT TIME ZONE 'UTC')::date-1,5000000,true)`); err != nil {
		t.Fatal(err)
	}
	runtime := []string{runtimeFixture(t), runtimeFixture(t)}
	granted := make(chan reservation, 32)
	var tasks sync.WaitGroup
	for i := range 32 {
		tasks.Go(func() {
			r, err := []*ledger{first, second}[i%2].reserve(ctx, runtime[i%2])
			switch {
			case err == nil:
				granted <- r
			case !errors.Is(err, ErrBudget):
				t.Error(err)
			}
		})
	}
	tasks.Wait()
	close(granted)
	want := int(dailyMicroUSD / p.cost(maxInputTokens, maxOutputTokens))
	if len(granted) != want {
		t.Fatalf("shared budget granted %d reservations, want %d", len(granted), want)
	}
	r := <-granted
	if _, err := first.settle(ctx, r, nil); !errors.Is(err, ErrUsage) {
		t.Fatalf("unknown bill should remain reserved: %v", err)
	}
	if _, err := first.reserve(ctx, runtime[0]); !errors.Is(err, ErrBudget) {
		t.Fatalf("uncertain request released budget: %v", err)
	}
	report := &usage{Model: modelID, Input: 100, Output: 100}
	if cost, err := first.settle(ctx, r, report); err != nil || cost != 110 {
		t.Fatalf("verified usage settlement: %d %v", cost, err)
	}
	if _, err := second.settle(ctx, r, report); !errors.Is(err, ErrUsage) {
		t.Fatalf("duplicate settlement was credited: %v", err)
	}
	next, err := second.reserve(ctx, runtime[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.settle(ctx, next, &usage{Model: modelID, Input: maxInputTokens + 1, Output: 1}); !errors.Is(err, ErrUsage) {
		t.Fatalf("out-of-range usage accepted: %v", err)
	}
	var frozen bool
	if err := dbPool.QueryRow(ctx, `SELECT frozen FROM fleetops_operations.days
		WHERE utc_day=(clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&frozen); err != nil || !frozen {
		t.Fatalf("unexpected provider usage did not freeze the current UTC day: %v", err)
	}
}

func TestManagerRefusesModelWithoutCommittedReservationIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testDatabase(t)
	ledger, err := openLedger(ctx, pool, pricing{minInputRate, minOutputRate})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	m := Manager{ledger: ledger, slots: make(chan struct{}, 2), generate: func(ctx context.Context, raw []byte, id int64) (modelResult, error) {
		calls.Add(1)
		var reserved int64
		if err := pool.QueryRow(ctx, "SELECT reserved_micro_usd FROM fleetops_operations.requests WHERE id=$1", id).Scan(&reserved); err != nil || reserved <= 0 {
			t.Errorf("request started without committed reservation: %d %v", reserved, err)
		}
		return modelResult{}, context.DeadlineExceeded
	}}
	runtime := runtimeFixture(t)
	unknown := m.Decide(ctx, runtime, fleet.OperationsObservation{})
	if calls.Load() != 1 || unknown.RequestID == 0 || unknown.Status != "model_unavailable" || unknown.AccountedMicroUSD != nil {
		t.Fatalf("unverified model result was treated as confirmed: %+v", unknown)
	}
	if _, err := pool.Exec(ctx, "DROP TABLE fleetops_operations.requests"); err != nil {
		t.Fatal(err)
	}
	denied := m.Decide(ctx, runtime, fleet.OperationsObservation{})
	if denied.Status != "ledger_unavailable" || calls.Load() != 1 {
		t.Fatalf("ledger failure did not stop calls: %+v", denied)
	}
}

func TestManagerRetainsUnverifiedReservationsAndReportsWorkerFailuresIntegration(t *testing.T) {
	ctx := context.Background()
	pool := testDatabase(t)
	ledger, err := openLedger(ctx, pool, pricing{minInputRate, minOutputRate})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		status string
		result modelResult
	}{
		{"model_unavailable", modelResult{Failure: "model_unavailable"}},
		{"invalid_output", modelResult{Failure: "invalid_output"}},
		{"usage_unconfirmed", modelResult{Decision: &fleet.OperationsDecision{Kind: "abstain", Reason: "no_safe_option"}}},
	} {
		t.Run(test.status, func(t *testing.T) {
			calls := 0
			m := Manager{ledger: ledger, slots: make(chan struct{}, 2), generate: func(context.Context, []byte, int64) (modelResult, error) {
				calls++
				return test.result, nil
			}}
			out := m.Decide(ctx, runtimeFixture(t), fleet.OperationsObservation{})
			if calls != 1 || out.Status != test.status || out.RequestID == 0 || out.Decision != nil ||
				out.AccountedMicroUSD != nil || out.Usage != nil {
				t.Fatalf("unverified worker result: calls=%d outcome=%+v", calls, out)
			}
			var reserved int64
			var settled bool
			if err := pool.QueryRow(ctx, "SELECT reserved_micro_usd, settled_at IS NOT NULL FROM fleetops_operations.requests WHERE id=$1",
				out.RequestID).Scan(&reserved, &settled); err != nil || settled || reserved != out.ReservedMicroUSD || reserved <= 0 {
				t.Fatalf("unverified request did not retain its reservation: reserved=%d settled=%t error=%v", reserved, settled, err)
			}
		})
	}
}
