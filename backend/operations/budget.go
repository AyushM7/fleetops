package operations

import (
	"context"
	"errors"
	"math"
	"regexp"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/jackc/pgx/v5"
)

const (
	modelID         = "gpt-6-luna"
	maxInputTokens  = 1_050_000
	maxOutputTokens = 768
	requestTimeout  = 8 * time.Second
	utcCutoff       = 20 * time.Second
	// Allow for long-context pricing, cache writes and regional processing.
	minInputRate  = 275_000
	minOutputRate = 825_000

	standardInputRate      = 100_000
	standardCachedRate     = 10_000
	standardCacheWriteRate = 125_000
	standardOutputRate     = 500_000
	shortContextLimit      = 272_000
	standardPricingBasis   = "gpt-6-luna-standard-2026-11-21"
)

var (
	ErrBudget      = errors.New("shared operations budget cannot admit another request")
	ErrUsage       = errors.New("model usage is unavailable or outside the reviewed limits")
	runtimePattern = regexp.MustCompile(`^P[0-9a-f]{32}$`)
)

var recoveryRunID = regexp.MustCompile(`^A[0-9a-f]{32}$`)
var providerResponseID = regexp.MustCompile(`^resp_[A-Za-z0-9_-]{1,120}$`)

type pricing struct{ input, output int64 }

func (p pricing) valid() bool { return p.validFor(maxOutputTokens) }

func (p pricing) validFor(output int64) bool {
	// Reserve enough integer range for both token products and the rounding term.
	const maxProduct = math.MaxInt64 - 999_999
	return p.input >= minInputRate && p.output >= minOutputRate &&
		p.input <= maxProduct/maxInputTokens &&
		p.output <= (maxProduct-maxInputTokens*p.input)/output
}

func (p pricing) cost(input, output int64) int64 {
	return (input*p.input + output*p.output + 999_999) / 1_000_000
}

type usage struct {
	Model      string `json:"model"`
	Input      int64  `json:"inputTokens"`
	Output     int64  `json:"outputTokens"`
	Cached     *int64 `json:"cachedInputTokens,omitempty"`
	CacheWrite *int64 `json:"cacheWriteInputTokens,omitempty"`
}

func (u usage) estimatedStandardCost() (int64, bool) {
	var cached, written int64
	if u.Cached != nil {
		cached = *u.Cached
	}
	if u.CacheWrite != nil {
		written = *u.CacheWrite
	}
	if cached < 0 || cached > u.Input || written < 0 || written > u.Input-cached {
		return 0, false
	}
	inputCost := (u.Input-cached-written)*standardInputRate + cached*standardCachedRate + written*standardCacheWriteRate
	outputCost := u.Output * standardOutputRate
	if u.Input > shortContextLimit {
		inputCost *= 2
		outputCost = outputCost * 3 / 2
	}
	return (inputCost + outputCost + 999_999) / 1_000_000, true
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
				micro_usd bigint NOT NULL CONSTRAINT days_micro_usd_nonnegative CHECK (micro_usd >= 0),
				frozen boolean NOT NULL DEFAULT false
			)`,
			`DO $$
			BEGIN
				IF EXISTS (SELECT 1 FROM pg_constraint
					WHERE conrelid = 'fleetops_operations.days'::regclass AND conname = 'days_micro_usd_check') THEN
					ALTER TABLE fleetops_operations.days
						DROP CONSTRAINT days_micro_usd_check,
						ADD CONSTRAINT days_micro_usd_nonnegative CHECK (micro_usd >= 0);
				END IF;
			END $$`,
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
			`ALTER TABLE fleetops_operations.requests
				ADD COLUMN IF NOT EXISTS cached_input_tokens bigint CHECK (cached_input_tokens BETWEEN 0 AND input_tokens),
				ADD COLUMN IF NOT EXISTS cache_write_input_tokens bigint CHECK (cache_write_input_tokens BETWEEN 0 AND input_tokens-COALESCE(cached_input_tokens,0)),
				ADD COLUMN IF NOT EXISTS pricing_basis text,
 ADD COLUMN IF NOT EXISTS recovery_run_id text,
 ADD COLUMN IF NOT EXISTS provider_response_id text UNIQUE`,
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
	return l.reserveBounded(ctx, runtime, "", maxOutputTokens, 0, 0)
}

func (l *ledger) reserveRecovery(ctx context.Context, runtime, run string, requests, micros int64) (reservation, error) {
	if !recoveryRunID.MatchString(run) || requests < 1 || micros < 1 || !l.pricing.validFor(recoveryOutput) {
		return reservation{}, ErrBudget
	}
	return l.reserveBounded(ctx, runtime, run, recoveryOutput, requests, micros)
}

func (l *ledger) reserveBounded(ctx context.Context, runtime, run string, output, requests, micros int64) (reservation, error) {
	if !runtimePattern.MatchString(runtime) {
		return reservation{}, ErrBudget
	}
	maximum := l.pricing.cost(maxInputTokens, output)
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
		if run != "" {
			var spent, used int64
			var frozen bool
			if err := tx.QueryRow("SELECT micro_usd,frozen FROM fleetops_operations.days WHERE utc_day=$1::text::date FOR UPDATE", day).Scan(&spent, &frozen); err != nil {
				return err
			}
			if err := tx.QueryRow("SELECT count(*) FROM fleetops_operations.requests WHERE utc_day=$1::text::date", day).Scan(&used); err != nil {
				return err
			}
			if frozen || used >= requests || maximum > micros-spent {
				return ErrBudget
			}
		}
		tag, err := tx.Exec(`UPDATE fleetops_operations.days SET micro_usd=micro_usd+$2
			WHERE utc_day=$1::text::date AND NOT frozen`, day, maximum)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrBudget
		}
		return tx.QueryRow(`INSERT INTO fleetops_operations.requests
			(utc_day,runtime_id,model,started_at,reserved_micro_usd,recovery_run_id)
			VALUES ($1::text::date,$2,$3,$4,$5,NULLIF($6,'')) RETURNING id`, day, runtime, modelID, now, maximum, run).Scan(&id)
	})
	if err != nil {
		return reservation{}, err
	}
	return reservation{id: id, maximum: maximum}, nil
}

// Only one settlement releases the unused part of a reservation.
func (l *ledger) settle(ctx context.Context, r reservation, u *usage) (int64, error) {
	return l.settleBounded(ctx, r, u, maxOutputTokens, "")
}

func (l *ledger) settleRecovery(ctx context.Context, r reservation, u providerUsage) error {
	_, err := l.settleBounded(ctx, r, &u.usage, recoveryOutput, u.ID)
	return err
}

func (l *ledger) settleBounded(ctx context.Context, r reservation, u *usage, output int64, providerID string) (int64, error) {
	if u == nil || u.Model != modelID || u.Input < 1 || u.Output < 0 {
		return 0, ErrUsage
	}
	if u.Input > maxInputTokens || u.Output > output {
		return 0, errors.Join(ErrUsage, l.freeze(ctx, r))
	}
	accounted, valid := u.estimatedStandardCost()
	basis := standardPricingBasis
	if providerID != "" {
		// The provider need not report every billable cache class. V3
		// settles at the configured upper price for confirmed token counts.
		accounted = l.pricing.cost(u.Input, u.Output)
		basis = "v3_configured_upper_bound"
	}
	if !valid || accounted > r.maximum {
		return 0, errors.Join(ErrUsage, l.freeze(ctx, r))
	}
	err := l.db.Transaction(ctx, func(tx *db.Tx) error {
		var day string
		var reserved int64
		err := tx.QueryRow(`UPDATE fleetops_operations.requests
			SET settled_at=clock_timestamp(),input_tokens=$2,output_tokens=$3,accounted_micro_usd=$4,
				cached_input_tokens=$5,cache_write_input_tokens=$6,pricing_basis=$7,provider_response_id=NULLIF($8,'')
			WHERE id=$1 AND settled_at IS NULL RETURNING utc_day::text,reserved_micro_usd`,
			r.id, u.Input, u.Output, accounted, u.Cached, u.CacheWrite, basis, providerID).Scan(&day, &reserved)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && reserved != r.maximum {
			return ErrUsage
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE fleetops_operations.days SET micro_usd=micro_usd-$2+$3
			WHERE utc_day=$1::text::date`, day, reserved, accounted)
		return err
	})
	if err != nil {
		return 0, err
	}
	return accounted, nil
}

func (l *ledger) freeze(ctx context.Context, r reservation) error {
	_, err := l.db.Exec(ctx, `UPDATE fleetops_operations.days d SET frozen=true
		FROM fleetops_operations.requests r WHERE r.id=$1 AND d.utc_day=r.utc_day`, r.id)
	return err
}
