package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Each caller gets a private schema, pool, and migration ledger. Cleanup drops
// only that generated schema, never the database or a Compose volume.
func testDatabase(t *testing.T) (*Pool, Config) {
	t.Helper()
	url := os.Getenv("FLEETOPS_TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("FLEETOPS_REQUIRE_DB") == "1" {
			t.Fatal("FLEETOPS_TEST_DATABASE_URL is required")
		}
		t.Skip("FLEETOPS_TEST_DATABASE_URL is unavailable")
	}
	cfg := DefaultConfig(url)
	admin, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("test database unavailable: %v", err)
	}
	t.Cleanup(admin.Close)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("cannot generate test schema identity")
	}
	cfg.Schema = "fleetops_test_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{cfg.Schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("test schema cleanup: %v", err)
		}
	})
	pool, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, cfg
}

func count(t *testing.T, pool *Pool, sql string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIntegrationEmbeddedMigrations(t *testing.T) {
	t.Parallel()
	pool, _ := testDatabase(t)
	for range 2 {
		if err := pool.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	embedded, err := loadMigrations(migrationFiles)
	if err != nil {
		t.Fatal(err)
	}
	if got := count(t, pool, "SELECT count(*) FROM schema_migrations"); got != len(embedded) {
		t.Fatalf("history rows = %d", got)
	}
	var checksum string
	if err := pool.QueryRow(context.Background(), "SELECT checksum FROM schema_migrations WHERE version = 1").Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	if checksum != embedded[0].checksum {
		t.Fatal("embedded checksum was not persisted")
	}
}

func TestIntegrationOrderingAndIdempotence(t *testing.T) {
	t.Parallel()
	pool, _ := testDatabase(t)
	migrations := fixtureMigrations(t, map[string]string{
		"10_second.sql": "INSERT INTO ordering VALUES (2);",
		"2_first.sql":   "CREATE TABLE ordering (n int); INSERT INTO ordering VALUES (1);",
	})
	for range 2 {
		if err := pool.migrate(context.Background(), migrations); err != nil {
			t.Fatal(err)
		}
	}
	if count(t, pool, "SELECT count(*) FROM ordering") != 2 || count(t, pool, "SELECT count(*) FROM schema_migrations") != 2 {
		t.Fatal("rerun was not idempotent")
	}
}

func TestIntegrationChecksumAndHistoryProtection(t *testing.T) {
	t.Parallel()
	pool, _ := testDatabase(t)
	base := fixtureMigrations(t, map[string]string{"2_first.sql": "CREATE TABLE original (n int);"})
	if err := pool.migrate(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  error
	}{
		{name: "modified", files: map[string]string{"2_first.sql": "CREATE TABLE original (n int);\n", "3_next.sql": "CREATE TABLE unexpected (n int);"}, want: ErrChecksumMismatch},
		{name: "renamed", files: map[string]string{"2_renamed.sql": "CREATE TABLE original (n int);"}, want: ErrChecksumMismatch},
		{name: "missing", files: map[string]string{"3_next.sql": "SELECT 1;"}, want: ErrMigrationHistory},
		{name: "backfill", files: map[string]string{"1_earlier.sql": "SELECT 1;", "2_first.sql": "CREATE TABLE original (n int);"}, want: ErrMigrationOrder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := pool.migrate(context.Background(), fixtureMigrations(t, tc.files)); !errors.Is(err, tc.want) {
				t.Fatalf("migrate = %v, want %v", err, tc.want)
			}
			if count(t, pool, "SELECT count(*) FROM schema_migrations") != 1 {
				t.Fatal("denied migration changed history")
			}
		})
	}
	var table *string
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass('unexpected')::text").Scan(&table); err != nil || table != nil {
		t.Fatal("checksum failure allowed a later migration")
	}
}

func TestIntegrationRollbackOnFailure(t *testing.T) {
	t.Parallel()
	pool, _ := testDatabase(t)
	files := map[string]string{
		"1_kept.sql":     "CREATE TABLE kept (n int);",
		"2_rollback.sql": "CREATE TABLE rolled_back (n int); INSERT INTO kept VALUES (1); SELECT 1/0;",
	}
	if err := pool.migrate(context.Background(), fixtureMigrations(t, files)); !errors.Is(err, ErrMigration) {
		t.Fatalf("failed migration: %v", err)
	}
	var table *string
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass('rolled_back')::text").Scan(&table); err != nil || table != nil {
		t.Fatal("failed migration DDL was not rolled back")
	}
	if count(t, pool, "SELECT count(*) FROM kept") != 0 || count(t, pool, "SELECT count(*) FROM schema_migrations") != 1 {
		t.Fatal("failed migration DML or history was not rolled back")
	}
	files["2_rollback.sql"] = "CREATE TABLE rolled_back (n int);"
	if err := pool.migrate(context.Background(), fixtureMigrations(t, files)); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}

func TestIntegrationConcurrentMigrations(t *testing.T) {
	t.Parallel()
	pool, cfg := testDatabase(t)
	other, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(other.Close)
	migrations := fixtureMigrations(t, map[string]string{
		"1_once.sql": "SELECT pg_sleep(0.1); CREATE TABLE once_only (n int); INSERT INTO once_only VALUES (1);",
		"2_next.sql": "INSERT INTO once_only VALUES (2);",
	})
	start := make(chan struct{})
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for i := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			selected := pool
			if i%2 == 0 {
				selected = other
			}
			results <- selected.migrate(context.Background(), migrations)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count(t, pool, "SELECT count(*) FROM once_only") != 2 || count(t, pool, "SELECT count(*) FROM schema_migrations") != 2 {
		t.Fatal("concurrent migrations ran more than once")
	}
}

func TestIntegrationSchemaIsolation(t *testing.T) {
	t.Parallel()
	first, firstCfg := testDatabase(t)
	second, secondCfg := testDatabase(t)
	if firstCfg.Schema == secondCfg.Schema {
		t.Fatal("schemas are not unique")
	}
	for i, pool := range []*Pool{first, second} {
		if err := pool.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), "CREATE TABLE isolated (value int)"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), "INSERT INTO isolated VALUES ($1)", i+1); err != nil {
			t.Fatal(err)
		}
	}
	if count(t, first, "SELECT sum(value) FROM isolated") != 1 || count(t, second, "SELECT sum(value) FROM isolated") != 2 {
		t.Fatal("schemas share state")
	}
}

func TestIntegrationTimeoutAndCancellation(t *testing.T) {
	t.Parallel()
	pool, cfg := testDatabase(t)
	cfg.QueryTimeout = 100 * time.Millisecond
	bounded, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bounded.Close)
	started := time.Now()
	var ignored any
	if err := bounded.QueryRow(context.Background(), "SELECT pg_sleep(2)").Scan(&ignored); !errors.Is(err, ErrQuery) || time.Since(started) > time.Second {
		t.Fatalf("query timeout: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pool.Ping(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ping cancellation: %v", err)
	}
	if _, err := Open(canceled, cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("open cancellation: %v", err)
	}
}

func TestIntegrationCanceledMigrationReleasesLock(t *testing.T) {
	t.Parallel()
	pool, _ := testDatabase(t)
	migrations := fixtureMigrations(t, map[string]string{"1_slow.sql": "CREATE TABLE canceled (n int); SELECT pg_sleep(2);"})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := pool.migrate(ctx, migrations); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("migration cancellation: %v", err)
	}
	// A subsequent run must acquire the lock and see no partial migration.
	migrations = fixtureMigrations(t, map[string]string{"1_slow.sql": "CREATE TABLE canceled (n int);"})
	if err := pool.migrate(context.Background(), migrations); err != nil {
		t.Fatalf("lock or transaction survived cancellation: %v", err)
	}
}
