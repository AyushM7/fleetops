package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
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
		!(pricing{6_000_000, 7_000_000}).valid() ||
		(pricing{math.MaxInt64, minOutputRate}).valid() || (pricing{minInputRate, math.MaxInt64}).valid() ||
		(pricing{1, 1}).valid() || (pricing{}).valid() {
		t.Fatal("invalid or non-conservative model price bound")
	}
}

func TestStandardSettlementPricesReportedTokenClasses(t *testing.T) {
	cached, written := int64(1663), int64(2)
	for _, tc := range []struct {
		usage usage
		want  int64
		valid bool
	}{
		{usage{Input: 1666, Output: 29, Cached: &cached}, 32, true},
		{usage{Input: 100, Output: 100}, 60, true},
		{usage{Input: 272001, Output: 100}, 54476, true},
		{usage{Input: 1664, Output: 29, Cached: &cached, CacheWrite: &written}, 0, false},
		{usage{Input: 1, CacheWrite: &written}, 0, false},
	} {
		if got, ok := tc.usage.estimatedStandardCost(); got != tc.want || ok != tc.valid {
			t.Errorf("estimatedStandardCost(%+v) = %d,%t, want %d,%t", tc.usage, got, ok, tc.want, tc.valid)
		}
	}
}

func TestDailyLedgerIsAtomicAcrossVisitorsIntegration(t *testing.T) {
	ctx := context.Background()
	dbPool := testDatabase(t)
	if _, err := dbPool.Exec(ctx, "CREATE SCHEMA fleetops_operations"); err != nil {
		t.Fatal(err)
	}
	if _, err := dbPool.Exec(ctx, `CREATE TABLE fleetops_operations.days (
		utc_day date PRIMARY KEY, micro_usd bigint NOT NULL CHECK (micro_usd BETWEEN 0 AND 5000000),
		frozen boolean NOT NULL DEFAULT false)`); err != nil {
		t.Fatal(err)
	}
	if _, err := dbPool.Exec(ctx, `INSERT INTO fleetops_operations.days (utc_day,micro_usd,frozen)
		VALUES ((clock_timestamp() AT TIME ZONE 'UTC')::date-1,5000000,true)`); err != nil {
		t.Fatal(err)
	}
	p := pricing{minInputRate, minOutputRate}
	first, err := openLedger(ctx, dbPool, p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openLedger(ctx, dbPool, p)
	if err != nil {
		t.Fatal(err)
	}
	var priorTotal int64
	var priorFrozen bool
	if err := dbPool.QueryRow(ctx, `SELECT micro_usd,frozen FROM fleetops_operations.days
		WHERE utc_day=(clock_timestamp() AT TIME ZONE 'UTC')::date-1`).Scan(&priorTotal, &priorFrozen); err != nil || priorTotal != 5_000_000 || !priorFrozen {
		t.Fatalf("migration changed historical ledger totals: %d %t %v", priorTotal, priorFrozen, err)
	}
	if _, err := dbPool.Exec(ctx, `UPDATE fleetops_operations.days SET micro_usd=-1`); err == nil {
		t.Fatal("migration allowed a negative ledger total")
	}
	runtime := []string{runtimeFixture(t), runtimeFixture(t)}
	granted := make(chan reservation, 32)
	var tasks sync.WaitGroup
	for i := range 32 {
		tasks.Go(func() {
			r, err := []*ledger{first, second}[i%2].reserve(ctx, runtime[i%2])
			if err != nil {
				t.Error(err)
				return
			}
			granted <- r
		})
	}
	tasks.Wait()
	close(granted)
	const want = 32
	if len(granted) != want {
		t.Fatalf("shared ledger granted %d reservations, want %d", len(granted), want)
	}
	r := <-granted
	if _, err := first.settle(ctx, r, nil); !errors.Is(err, ErrUsage) {
		t.Fatalf("unknown bill should remain reserved: %v", err)
	}
	var total int64
	if err := dbPool.QueryRow(ctx, `SELECT micro_usd FROM fleetops_operations.days
		WHERE utc_day=(clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&total); err != nil || total != want*r.maximum {
		t.Fatalf("shared ledger lost a reservation: %d, want %d, error %v", total, want*r.maximum, err)
	}
	cached, written := int64(80), int64(10)
	report := &usage{Model: modelID, Input: 100, Output: 100, Cached: &cached, CacheWrite: &written}
	const settledCost = 54
	if cost, err := first.settle(ctx, r, report); err != nil || cost != settledCost {
		t.Fatalf("verified usage settlement: %d %v", cost, err)
	}
	var storedCached, storedWrite int64
	var basis string
	if err := dbPool.QueryRow(ctx, `SELECT cached_input_tokens,cache_write_input_tokens,pricing_basis
		FROM fleetops_operations.requests WHERE id=$1`, r.id).Scan(&storedCached, &storedWrite, &basis); err != nil ||
		storedCached != cached || storedWrite != written || basis != standardPricingBasis {
		t.Fatalf("settlement did not preserve cache usage and rate basis: %d %d %q %v", storedCached, storedWrite, basis, err)
	}
	if _, err := second.settle(ctx, r, report); !errors.Is(err, ErrUsage) {
		t.Fatalf("duplicate settlement was credited: %v", err)
	}
	if err := dbPool.QueryRow(ctx, `SELECT micro_usd FROM fleetops_operations.days
		WHERE utc_day=(clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&total); err != nil || total != (want-1)*r.maximum+settledCost {
		t.Fatalf("shared ledger settlement total: %d, error %v", total, err)
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
	if _, err := second.reserve(ctx, runtime[1]); !errors.Is(err, ErrBudget) {
		t.Fatalf("frozen ledger admitted another request: %v", err)
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
