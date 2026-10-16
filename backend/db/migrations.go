package db

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var (
	ErrInvalidMigration = errors.New("invalid migration filename, version, or empty SQL")
	ErrDuplicateVersion = errors.New("duplicate migration version")
	ErrChecksumMismatch = errors.New("applied migration checksum or filename changed")
	ErrMigrationHistory = errors.New("applied migration is missing from embedded history")
	ErrMigrationOrder   = errors.New("cannot insert a migration below the applied version")
	ErrMigration        = errors.New("database migration failed")
	ErrMigrationLock    = errors.New("database migration lock failed")
	migrationName       = regexp.MustCompile(`^([0-9]+)_[a-z0-9_]+\.sql$`)
)

type migration struct {
	version  int64
	name     string
	sql      string
	checksum string
}

func loadMigrations(source fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(source, "migrations")
	if err != nil {
		return nil, ErrInvalidMigration
	}
	var migrations []migration
	seen := make(map[int64]bool)
	for _, entry := range entries {
		parts := migrationName.FindStringSubmatch(entry.Name())
		if entry.IsDir() || parts == nil {
			return nil, ErrInvalidMigration
		}
		version, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, ErrInvalidMigration
		}
		if seen[version] {
			return nil, ErrDuplicateVersion
		}
		seen[version] = true
		data, err := fs.ReadFile(source, "migrations/"+entry.Name())
		if err != nil || strings.TrimSpace(string(data)) == "" {
			return nil, ErrInvalidMigration
		}
		digest := sha256.Sum256(data)
		migrations = append(migrations, migration{version: version, name: entry.Name(), sql: string(data), checksum: hex.EncodeToString(digest[:])})
	}
	slices.SortFunc(migrations, func(a, b migration) int {
		if a.version < b.version {
			return -1
		}
		if a.version > b.version {
			return 1
		}
		return 0
	})
	return migrations, nil
}

// Migrate applies embedded forward-only SQL. Migration authors must not include
// transaction-control statements or operations forbidden inside transactions.
func (p *Pool) Migrate(ctx context.Context) error {
	migrations, err := loadMigrations(migrationFiles)
	if err != nil {
		return err
	}
	return p.migrate(ctx, migrations)
}

const createHistory = `CREATE TABLE IF NOT EXISTS schema_migrations (
 version bigint PRIMARY KEY CHECK (version > 0),
 name text NOT NULL,
 checksum text NOT NULL CHECK (length(checksum) = 64),
 applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
)`

func (p *Pool) migrate(ctx context.Context, migrations []migration) (err error) {
	acquireCtx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()
	conn, err := p.pool.Acquire(acquireCtx)
	if err != nil {
		return safeError(ErrMigrationLock, err)
	}
	// One session owns the lock across all per-migration transactions. The same
	// schema in this database shares a key; isolated schemas need not block.
	digest := sha256.Sum256([]byte("fleetops:migrations:" + p.schema))
	key := int64(binary.BigEndian.Uint64(digest[:8]))
	if _, err = conn.Exec(acquireCtx, "SELECT pg_advisory_lock($1)", key); err != nil {
		closeConnection(conn)
		return safeError(ErrMigrationLock, err)
	}
	defer func() { err = errors.Join(err, p.unlock(conn, key)) }()
	return p.migrateLocked(ctx, conn, migrations)
}

func (p *Pool) unlock(conn *pgxpool.Conn, key int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), p.queryTimeout)
	defer cancel()
	var released bool
	err := conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", key).Scan(&released)
	if err != nil || !released {
		closeConnection(conn)
		return ErrMigrationLock
	}
	conn.Release()
	return nil
}

func closeConnection(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = conn.Conn().Close(ctx)
	conn.Release()
}

func (p *Pool) migrateLocked(ctx context.Context, conn *pgxpool.Conn, migrations []migration) error {
	queryCtx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()
	if _, err := conn.Exec(queryCtx, createHistory); err != nil {
		return safeError(ErrMigration, err)
	}
	applied, err := readHistory(queryCtx, conn)
	if err != nil {
		return err
	}
	if err := validateHistory(migrations, applied); err != nil {
		return err
	}
	for _, m := range migrations {
		if _, exists := applied[m.version]; exists {
			continue
		}
		if err := p.apply(ctx, conn, m); err != nil {
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
	}
	return nil
}

func readHistory(ctx context.Context, conn *pgxpool.Conn) (map[int64]migration, error) {
	rows, err := conn.Query(ctx, "SELECT version, name, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, safeError(ErrMigration, err)
	}
	defer rows.Close()
	applied := make(map[int64]migration)
	for rows.Next() {
		var m migration
		if err := rows.Scan(&m.version, &m.name, &m.checksum); err != nil {
			return nil, safeError(ErrMigration, err)
		}
		applied[m.version] = m
	}
	return applied, safeError(ErrMigration, rows.Err())
}

func validateHistory(migrations []migration, applied map[int64]migration) error {
	remaining := make(map[int64]migration, len(applied))
	var highest int64
	for version, m := range applied {
		remaining[version] = m
		highest = max(highest, version)
	}
	for _, m := range migrations {
		previous, exists := remaining[m.version]
		if exists {
			if previous.checksum != m.checksum || previous.name != m.name {
				return ErrChecksumMismatch
			}
			delete(remaining, m.version)
		} else if m.version <= highest {
			return ErrMigrationOrder
		}
	}
	if len(remaining) != 0 {
		return ErrMigrationHistory
	}
	return nil
}

func (p *Pool) apply(ctx context.Context, conn *pgxpool.Conn, m migration) error {
	queryCtx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()
	tx, err := conn.Begin(queryCtx)
	if err != nil {
		return safeError(ErrMigration, err)
	}
	defer func() {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), p.queryTimeout)
		defer rollbackCancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	if _, err := tx.Exec(queryCtx, m.sql); err != nil {
		return safeError(ErrMigration, err)
	}
	if _, err := tx.Exec(queryCtx, "INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)", m.version, m.name, m.checksum); err != nil {
		return safeError(ErrMigration, err)
	}
	return safeError(ErrMigration, tx.Commit(queryCtx))
}
