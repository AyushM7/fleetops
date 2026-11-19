package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/jackc/pgx/v5"
)

type automaticReceipt struct {
	id                           int64
	subject, fingerprint, status string
}

func (s *Service) stageAutomatic(ctx context.Context, tx *db.Tx, command AutomaticCommand, subject string, payload []byte, received time.Time, denied error) (int64, *automaticReceipt, error, error) {
	var key, fingerprint *string
	var prior *automaticReceipt
	if denied == nil {
		hash := sha256.Sum256(payload)
		digest := hex.EncodeToString(hash[:])
		existing, err := s.automaticReceipt(tx, command.IdempotencyKey)
		if err != nil {
			return 0, nil, nil, err
		}
		switch {
		case existing == nil:
			key, fingerprint = &command.IdempotencyKey, &digest
			denied = s.automaticGate(ctx, command, subject, s.clock().UTC())
		case existing.subject != subject || existing.fingerprint != digest:
			denied = ErrAutomaticKeyConflict
		default:
			prior = existing
		}
	}
	status, reason := "pending", ""
	var finished *time.Time
	var replayOf *int64
	if denied != nil {
		status, reason, finished = "rejected", denied.Error(), &received
	} else if prior != nil {
		status, finished, replayOf = "replayed", &received, &prior.id
	}
	var id int64
	err := tx.QueryRow(`INSERT INTO fleet_automatic_audit
		(runtime_id, subject, received_at, finished_at, command, status, reason, command_key, fingerprint, replay_of)
		VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9,$10) RETURNING id`,
		s.runtimeID, subject, received, finished, payload, status, reason, key, fingerprint, replayOf).Scan(&id)
	return id, prior, denied, err
}

func (s *Service) automaticReceipt(tx *db.Tx, key string) (*automaticReceipt, error) {
	var prior automaticReceipt
	err := tx.QueryRow(`SELECT id,subject,fingerprint,status FROM fleet_automatic_audit
		WHERE runtime_id=$1 AND command_key=$2`, s.runtimeID, key).Scan(&prior.id, &prior.subject, &prior.fingerprint, &prior.status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &prior, err
}
