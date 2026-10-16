package fleet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
)

func pendingContext(t *testing.T, role auth.Role) context.Context {
	t.Helper()
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(random[:])
	digest := sha256.Sum256([]byte(token))
	config, _ := json.Marshal([]auth.Credential{{Subject: testOperator.Subject, Role: role, SHA256: hex.EncodeToString(digest[:])}})
	a, err := auth.New(string(config))
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := a.Authenticate(trustedContext(), token)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestPendingRequiresVerifiedOperatorBeforeDatabaseAccess(t *testing.T) {
	s := unitService(t)
	calls := 0
	s.transact = func(context.Context, func(*db.Tx) error) error { calls++; return db.ErrQuery }
	for _, ctx := range []context.Context{context.Background(), trustedContext(), pendingContext(t, auth.Viewer), pendingContext(t, auth.Proposer)} {
		if rows, err := s.ReadPendingProposals(ctx); !errors.Is(err, ErrUnauthorized) || rows != nil {
			t.Fatal("unauthorized inbox read", rows, err)
		}
	}
	if calls != 0 {
		t.Fatal("unauthorized read accessed database")
	}
	if _, err := s.ReadPendingProposals(pendingContext(t, auth.Operator)); !errors.Is(err, ErrPersistence) || s.Poisoned() {
		t.Fatal("read failure changed runtime", err)
	}
	s.authorize = func(context.Context, proposal.OperatorIdentity) bool { return false }
	if _, err := s.ReadPendingProposals(pendingContext(t, auth.Operator)); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("service authorizer bypassed")
	}
	if calls != 1 {
		t.Fatal("revoked operator accessed database")
	}
}

func TestIntegrationPendingFilteringOrderingAndLimit(t *testing.T) {
	s, commander, pool, now := integrationService(t)
	ctx := pendingContext(t, auth.Operator)
	empty, err := s.ReadPendingProposals(ctx)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("empty inbox", empty, err)
	}
	records := make([]proposal.Proposal, 0, 56)
	for i := 0; i < 56; i++ {
		*now = testTime.Add(time.Duration(i/2) * time.Second)
		result, err := s.Submit(context.Background(), chargingRequest, testTime.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, result.Proposal)
	}
	*now = testTime.Add(time.Minute)
	for _, p := range records[:2] {
		if _, err := s.Decide(trustedContext(), p.ID, proposal.Reject, testOperator); err != nil {
			t.Fatal(err)
		}
	}
	*now = testTime.Add(2 * time.Hour)
	if _, err := s.Decide(trustedContext(), records[2].ID, proposal.Reject, testOperator); !errors.Is(err, proposal.ErrExpired) {
		t.Fatal(err)
	}
	*now = testTime.Add(time.Minute)
	if _, err := s.Decide(trustedContext(), records[3].ID, proposal.Approve, testOperator); err != nil {
		t.Fatal(err)
	}
	foreign, err := New(testConfig(), 42, pool, func() time.Time { return testTime }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := foreign.Submit(context.Background(), chargingRequest, testTime.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	expected := append([]proposal.Proposal{}, records[4:]...)
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].CreatedAt.Equal(expected[j].CreatedAt) {
			return expected[i].ID < expected[j].ID
		}
		return expected[i].CreatedAt.Before(expected[j].CreatedAt)
	})
	before := s.Snapshot()
	var auditBefore int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM fleet_audit").Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ReadPendingProposals(ctx)
	if err != nil || len(rows) != 50 {
		t.Fatalf("result limit %d, %v", len(rows), err)
	}
	for i, row := range rows {
		if row.Proposal != expected[i] || !row.CurrentRuntime || row.Execution != NotExecuted || row.Proposal.ID == outsider.Proposal.ID {
			t.Fatal("inbox filtering/order incorrect")
		}
	}
	again, err := s.ReadPendingProposals(ctx)
	if err != nil || !reflect.DeepEqual(rows, again) {
		t.Fatal("unstable inbox ordering", err)
	}
	var auditAfter int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM fleet_audit").Scan(&auditAfter); err != nil {
		t.Fatal(err)
	}
	if auditBefore != auditAfter || !reflect.DeepEqual(before, s.Snapshot()) || commander.calls != 1 {
		t.Fatal("read changed simulator or audit")
	}
}
