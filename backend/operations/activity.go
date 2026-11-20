package operations

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/AyushM7/fleetops/backend/fleet"
)

const activityTable = `CREATE TABLE IF NOT EXISTS fleetops_operations.activity (
	runtime_id text NOT NULL,
	observation_id text NOT NULL,
	observed_at timestamptz NOT NULL,
	stage smallint NOT NULL CHECK (stage BETWEEN 1 AND 3),
	payload jsonb NOT NULL,
	PRIMARY KEY (runtime_id, observation_id)
)`

// Activity holds only verified fleet references, typed proposals and observed outcomes.
type Activity struct {
	ObservationID     string                    `json:"observationId"`
	ObservedAt        time.Time                 `json:"observedAt"`
	Tick              int                       `json:"tick"`
	Stage             string                    `json:"stage"`
	Status            string                    `json:"status"`
	ProposedAt        *time.Time                `json:"proposedAt,omitempty"`
	FinishedAt        *time.Time                `json:"finishedAt,omitempty"`
	Decision          *fleet.OperationsDecision `json:"decision,omitempty"`
	ModelStatus       string                    `json:"modelStatus,omitempty"`
	Model             string                    `json:"model,omitempty"`
	RequestID         int64                     `json:"requestId,omitempty"`
	ReservedMicroUSD  int64                     `json:"reservedMicroUsd,omitempty"`
	AccountedMicroUSD *int64                    `json:"accountedMicroUsd,omitempty"`
	Usage             *usage                    `json:"usage,omitempty"`
	Go                *fleet.AutomaticResult    `json:"go,omitempty"`
	GoReason          string                    `json:"goReason,omitempty"`
	Recorded          bool                      `json:"recorded"`
}

func (a *Activity) SetModel(out Outcome) {
	a.ModelStatus, a.Model, a.RequestID = out.Status, out.Model, out.RequestID
	a.ReservedMicroUSD, a.AccountedMicroUSD, a.Usage = out.ReservedMicroUSD, out.AccountedMicroUSD, out.Usage
}

func (m *Manager) RecordActivity(ctx context.Context, runtime string, activity Activity) error {
	unavailable := errors.New("operations audit write unconfirmed")
	if !runtimePattern.MatchString(runtime) || !runtimePattern.MatchString(activity.ObservationID) || activity.ObservedAt.IsZero() {
		return unavailable
	}
	phase := map[string]int16{"observed": 1, "proposed": 2, "finished": 3}[activity.Stage]
	if phase == 0 {
		return unavailable
	}
	raw, err := json.Marshal(activity)
	if err != nil || len(raw) > 4096 {
		return unavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if phase == 1 {
		_, err := m.ledger.db.Exec(ctx, `INSERT INTO fleetops_operations.activity
			(runtime_id,observation_id,observed_at,stage,payload) VALUES ($1,$2,$3,1,$4)`,
			runtime, activity.ObservationID, activity.ObservedAt, raw)
		return err
	}
	tag, err := m.ledger.db.Exec(ctx, `UPDATE fleetops_operations.activity SET stage=$3,payload=$4
		WHERE runtime_id=$1 AND observation_id=$2 AND stage<$3`, runtime, activity.ObservationID, phase, raw)
	if err != nil || tag.RowsAffected() != 1 {
		return errors.Join(unavailable, err)
	}
	return nil
}
