package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
)

func observationProposer(t *testing.T) context.Context {
	t.Helper()
	token := strings.Repeat("test-observation-proposer", 3)
	digest := sha256.Sum256([]byte(token))
	raw, _ := json.Marshal([]auth.Credential{{Subject: "observation-agent", Role: auth.Proposer, SHA256: hex.EncodeToString(digest[:])}})
	a, err := auth.New(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := a.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func assertObservationDenied(t *testing.T, s *Service, pool *db.Pool, before StateView, expected []string) {
	t.Helper()
	r, err := s.Submit(observationProposer(t), chargingRequest, testTime.Add(time.Minute), expected...)
	if !errors.Is(err, ErrObservation) || !r.AuditCommitted || r.ExecutionAttempted || r.Execution != NotExecuted {
		t.Fatal("expected committed observation denial", r, err)
	}
	after, _ := s.ReadState(context.Background())
	if !reflect.DeepEqual(before, after) {
		t.Fatal("denial changed simulator or observation")
	}
	var audits, proposals int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='create' AND NOT accepted AND reason=$2 AND operator_subject='observation-agent'`, r.Proposal.ID, ErrObservation.Error()).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM fleet_proposals WHERE id=$1`, r.Proposal.ID).Scan(&proposals); err != nil || audits != 1 || proposals != 0 {
		t.Fatal("denial was not audited without admission", audits, proposals, err)
	}
}

func TestIntegrationObservationBinding(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "fabricated", "foreign runtime", "tick", "same tick command"} {
		t.Run(mode, func(t *testing.T) {
			s, _, pool, _ := integrationService(t)
			observation, _ := s.ReadState(context.Background())
			expected := []string{observation.ObservationID}
			switch mode {
			case "missing":
				expected = nil
			case "empty":
				expected[0] = ""
			case "fabricated":
				expected[0] = strings.Repeat("0", 64)
			case "foreign runtime":
				other, _, _, _ := integrationService(t)
				foreign, _ := other.ReadState(context.Background())
				if !reflect.DeepEqual(observation.Robots, foreign.Robots) || observation.Tick != foreign.Tick {
					t.Fatal("foreign fixture telemetry differs")
				}
				expected[0] = foreign.ObservationID
			case "tick":
				if err := s.Tick(); err != nil {
					t.Fatal(err)
				}
			case "same tick command":
				pending := submit(t, s)
				if _, err := s.Decide(trustedContext(), pending.Proposal.ID, proposal.Approve, testOperator); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.ReadState(context.Background())
			assertObservationDenied(t, s, pool, before, expected)
		})
	}
}

func TestIntegrationObservationCurrentPreflightAndApproval(t *testing.T) {
	s, commander, _, _ := integrationService(t)
	ctx := observationProposer(t)
	observation, _ := s.ReadState(ctx)
	r, err := s.Submit(ctx, chargingRequest, testTime.Add(time.Minute), observation.ObservationID)
	if err != nil || r.Proposal.State != proposal.Pending || !r.AuditCommitted || r.ExecutionAttempted || commander.calls != 0 {
		t.Fatal("current observation did not produce only a pending proposal", r, err)
	}
	same, _ := s.ReadState(ctx)
	if !reflect.DeepEqual(observation, same) {
		t.Fatal("proposal admission changed simulator observation")
	}
	if _, err := s.Decide(ctx, r.Proposal.ID, proposal.Approve, proposal.OperatorIdentity{Subject: "observation-agent"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("identity incorrectly conferred approval authority", err)
	}
	// External fixture mutation deliberately leaves the token current to test independent preflight.
	if err := commander.InjectLowBattery("R1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(ctx, chargingRequest, testTime.Add(time.Minute), observation.ObservationID); err == nil {
		t.Fatal("matching observation bypassed creation preflight")
	}
	if _, err := s.Decide(trustedContext(), r.Proposal.ID, proposal.Approve, testOperator); err == nil || commander.calls != 0 {
		t.Fatal("approval bypassed independent preflight", err)
	}
}

func TestIntegrationObservationLockCoversAdmission(t *testing.T) {
	s, _, pool, _ := integrationService(t)
	ctx := observationProposer(t)
	observation, _ := s.ReadState(ctx)
	transact := s.transact
	entered, release := make(chan struct{}), make(chan struct{})
	s.transact = func(ctx context.Context, body func(*db.Tx) error) error {
		close(entered)
		<-release
		return transact(ctx, body)
	}
	submitted := make(chan error, 1)
	go func() {
		_, err := s.Submit(ctx, chargingRequest, testTime.Add(time.Minute), observation.ObservationID)
		submitted <- err
	}()
	<-entered
	if s.mu.TryLock() {
		s.mu.Unlock()
		close(release)
		<-submitted
		t.Fatal("submission did not retain the service lock through admission")
	}
	ticked := make(chan error, 1)
	go func() { ticked <- s.Tick() }()
	close(release)
	if err := <-submitted; err != nil {
		t.Fatal(err)
	}
	if err := <-ticked; err != nil {
		t.Fatal(err)
	}
	s.transact = transact
	// This identity was the final client read; the serialized tick wins before this submit.
	before, _ := s.ReadState(ctx)
	assertObservationDenied(t, s, pool, before, []string{observation.ObservationID})
}

func TestIntegrationObservationAuditFailurePoisons(t *testing.T) {
	s, _, _, _ := integrationService(t)
	before := s.Snapshot()
	s.transact = func(context.Context, func(*db.Tx) error) error { return db.ErrQuery }
	r, err := s.Submit(observationProposer(t), chargingRequest, testTime.Add(time.Minute), "invalid")
	if !errors.Is(err, ErrPersistence) || !s.Poisoned() || r.AuditCommitted || r.Execution != Unknown || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("audit uncertainty was not fail closed", r, err)
	}
}
