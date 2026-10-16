// Package db provides PostgreSQL connections and forward-only migrations.
package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrConfig  = errors.New("invalid database configuration")
	ErrConnect = errors.New("database connection failed")
	ErrQuery   = errors.New("database query failed")
	schemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
)

type Config struct {
	DatabaseURL    string `json:"-"`
	Schema         string
	ConnectTimeout time.Duration
	QueryTimeout   time.Duration
	MaxConns       int32
}

// String and GoString keep connection credentials out of ordinary diagnostics.
func (c Config) String() string   { return "db.Config{DatabaseURL:[REDACTED]}" }
func (c Config) GoString() string { return c.String() }

func DefaultConfig(databaseURL string) Config {
	return Config{DatabaseURL: databaseURL, Schema: "public", ConnectTimeout: 5 * time.Second, QueryTimeout: 5 * time.Second, MaxConns: 8}
}

func ConfigFromEnv() (Config, error) {
	cfg := DefaultConfig(os.Getenv("FLEETOPS_DATABASE_URL"))
	return cfg, cfg.validate()
}

func (c Config) validate() error {
	if strings.TrimSpace(c.DatabaseURL) == "" || !schemaName.MatchString(c.Schema) || c.ConnectTimeout <= 0 || c.QueryTimeout < time.Millisecond || c.MaxConns <= 0 {
		return ErrConfig
	}
	return nil
}

type Pool struct {
	pool         *pgxpool.Pool
	schema       string
	queryTimeout time.Duration
}

func poolConfig(cfg Config) (*pgxpool.Config, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	parsed, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, ErrConfig
	}
	parsed.MaxConns = cfg.MaxConns
	parsed.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	parsed.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{cfg.Schema}.Sanitize()
	parsed.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(cfg.QueryTimeout.Milliseconds(), 10)
	return parsed, nil
}

// Open verifies connectivity before returning a bounded connection pool.
// Driver errors are sanitized rather than embedding connection strings or SQL.
func Open(ctx context.Context, cfg Config) (*Pool, error) {
	parsed, err := poolConfig(cfg)
	if err != nil {
		return nil, err
	}
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectCtx, parsed)
	if err != nil {
		return nil, safeError(ErrConnect, err)
	}
	if err = pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, safeError(ErrConnect, err)
	}
	return &Pool{pool: pool, schema: cfg.Schema, queryTimeout: cfg.QueryTimeout}, nil
}

func (p *Pool) Close() { p.pool.Close() }

func (p *Pool) Ping(ctx context.Context) error {
	queryCtx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()
	return safeError(ErrConnect, p.pool.Ping(queryCtx))
}

func (p *Pool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	queryCtx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	defer cancel()
	tag, err := p.pool.Exec(queryCtx, sql, args...)
	return tag, safeError(ErrQuery, err)
}

type Row struct {
	row    pgx.Row
	cancel context.CancelFunc
}

// QueryRow must be followed by Scan, which releases the row and its context.
func (p *Pool) QueryRow(ctx context.Context, sql string, args ...any) *Row {
	queryCtx, cancel := context.WithTimeout(ctx, p.queryTimeout)
	return &Row{row: p.pool.QueryRow(queryCtx, sql, args...), cancel: cancel}
}

func (r *Row) Scan(dest ...any) error {
	defer r.cancel()
	err := r.row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgx.ErrNoRows
	}
	return safeError(ErrQuery, err)
}

// Never wrap a driver error: it may contain a password, SQL, or server detail.
// Preserve only safe context sentinels for callers that need cancellation logic.
func safeError(kind, cause error) error {
	if cause == nil {
		return nil
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(cause, sentinel) {
			return fmt.Errorf("%w: %w", kind, sentinel)
		}
	}
	return kind
}
