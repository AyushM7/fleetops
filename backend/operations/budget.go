package operations

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/jackc/pgx/v5"
)

const (
	modelID         = "gpt-6-luna"
	maxInputTokens  = 1_050_000
	maxOutputTokens = 768
	dailyMicroUSD   = 5_000_000
	requestTimeout  = 8 * time.Second
	utcCutoff       = 20 * time.Second
	// Allow for long-context pricing, cache writes and regional processing.
	minInputRate  = 275_000
	minOutputRate = 825_000
)

var (
	ErrBudget      = errors.New("shared operations budget cannot admit another request")
	ErrUsage       = errors.New("model usage is unavailable or outside the reviewed limits")
	runtimePattern = regexp.MustCompile(`^P[0-9a-f]{32}$`)
)

type pricing struct{ input, output int64 }

func (p pricing) valid() bool {
	return p.input >= minInputRate && p.output >= minOutputRate &&
		p.input <= dailyMicroUSD && p.output <= dailyMicroUSD && p.cost(maxInputTokens, maxOutputTokens) <= dailyMicroUSD
}

func (p pricing) cost(input, output int64) int64 {
	return (input*p.input + output*p.output + 999_999) / 1_000_000
}

type usage struct {
	Model  string `json:"model"`
	Input  int64  `json:"inputTokens"`
	Output int64  `json:"outputTokens"`
}

type ledger struct {
	db      *db.Pool
	pricing pricing
}

func openLedger(ctx context.Context, pool *db.Pool, price pricing) (*ledger, error) {
	if pool == nil || !price.valid() {
		return nil, ErrBudget
	}
	err := pool.Transaction(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec("SELECT pg_advisory_xact_lock(38834255091307)"); err != nil {
			return err
		}
		for _, query := range []string{
			"CREATE SCHEMA IF NOT EXISTS fleetops_operations",
			activityTable,
			`CREATE TABLE IF NOT EXISTS fleetops_operations.days (
				utc_day date PRIMARY KEY,
				micro_usd bigint NOT NULL CHECK (micro_usd BETWEEN 0 AND 5000000),
				frozen boolean NOT NULL DEFAULT false
			)`,
			`CREATE TABLE IF NOT EXISTS fleetops_operations.requests (
				id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
				utc_day date NOT NULL REFERENCES fleetops_operations.days,
				runtime_id text NOT NULL,
				model text NOT NULL,
				started_at timestamptz NOT NULL,
				reserved_micro_usd bigint NOT NULL CHECK (reserved_micro_usd > 0),
				settled_at timestamptz,
				input_tokens bigint,
				output_tokens bigint,
				accounted_micro_usd bigint,
				CHECK (accounted_micro_usd BETWEEN 0 AND reserved_micro_usd)
			)`,
		} {
			if _, err := tx.Exec(query); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &ledger{db: pool, pricing: price}, nil
}

type reservation struct {
	id, maximum int64
}

func (l *ledger) reserve(ctx context.Context, runtime string) (reservation, error) {
	if !runtimePattern.MatchString(runtime) {
		return reservation{}, ErrBudget
	}
	maximum := l.pricing.cost(maxInputTokens, maxOutputTokens)
	var id int64
	err := l.db.Transaction(ctx, func(tx *db.Tx) error {
		var now time.Time
		if err := tx.QueryRow("SELECT clock_timestamp()").Scan(&now); err != nil {
			return err
		}
		now = now.UTC()
		if now.Hour() == 23 && now.Minute() == 59 && now.Second() >= 60-int(utcCutoff/time.Second) {
			return ErrBudget
		}
		day := now.Format(time.DateOnly)
		if _, err := tx.Exec(`INSERT INTO fleetops_operations.days (utc_day,micro_usd)
			VALUES ($1::text::date,0) ON CONFLICT DO NOTHING`, day); err != nil {
			return err
		}
		tag, err := tx.Exec(`UPDATE fleetops_operations.days SET micro_usd=micro_usd+$2
			WHERE utc_day=$1::text::date AND NOT frozen AND micro_usd <= $3`, day, maximum, int64(dailyMicroUSD)-maximum)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrBudget
		}
		return tx.QueryRow(`INSERT INTO fleetops_operations.requests
			(utc_day,runtime_id,model,started_at,reserved_micro_usd)
			VALUES ($1::text::date,$2,$3,$4,$5) RETURNING id`, day, runtime, modelID, now, maximum).Scan(&id)
	})
	if err != nil {
		return reservation{}, err
	}
	return reservation{id: id, maximum: maximum}, nil
}

// Missing evidence leaves the reservation charged. Only a confirmed,
// single settlement can return unused budget.
func (l *ledger) settle(ctx context.Context, r reservation, u *usage) (int64, error) {
	if u == nil || u.Model != modelID || u.Input < 1 || u.Output < 0 {
		return 0, ErrUsage
	}
	if u.Input > maxInputTokens || u.Output > maxOutputTokens {
		return 0, errors.Join(ErrUsage, l.freeze(ctx, r))
	}
	actual := l.pricing.cost(u.Input, u.Output)
	err := l.db.Transaction(ctx, func(tx *db.Tx) error {
		var day string
		var reserved int64
		err := tx.QueryRow(`UPDATE fleetops_operations.requests
			SET settled_at=clock_timestamp(),input_tokens=$2,output_tokens=$3,accounted_micro_usd=$4
			WHERE id=$1 AND settled_at IS NULL RETURNING utc_day::text,reserved_micro_usd`,
			r.id, u.Input, u.Output, actual).Scan(&day, &reserved)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && reserved != r.maximum {
			return ErrUsage
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE fleetops_operations.days SET micro_usd=micro_usd-$2+$3
			WHERE utc_day=$1::text::date`, day, reserved, actual)
		return err
	})
	if err != nil {
		return 0, err
	}
	return actual, nil
}

func (l *ledger) freeze(ctx context.Context, r reservation) error {
	_, err := l.db.Exec(ctx, `UPDATE fleetops_operations.days d SET frozen=true
		FROM fleetops_operations.requests r WHERE r.id=$1 AND d.utc_day=r.utc_day`, r.id)
	return err
}
