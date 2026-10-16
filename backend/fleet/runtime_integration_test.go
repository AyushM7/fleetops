package fleet

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/proposal"
)

func TestIntegrationTickCannotInterleaveApproval(t *testing.T) {
	s, commander, pool, _ := integrationService(t)
	id := submit(t, s).Proposal.ID
	entered, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	s.authorize = func(ctx context.Context, operator proposal.OperatorIdentity) bool {
		first.Do(func() { close(entered); <-release })
		return authorized(ctx, operator)
	}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	decided := make(chan error, 1)
	go func() { _, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator); decided <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("approval did not enter authorization")
	}
	tickStarted, ticked := make(chan struct{}), make(chan error, 1)
	go func() { close(tickStarted); ticked <- s.Tick() }()
	<-tickStarted
	select {
	case err := <-ticked:
		t.Fatalf("Tick interleaved approval: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-decided; err != nil {
		t.Fatal(err)
	}
	if err := <-ticked; err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Tick != 1 || commander.calls != 1 || auditCount(t, pool, id, "execute", true) != 1 {
		t.Fatal("serialized approval/tick outcome incorrect")
	}
}

func TestIntegrationTickCommandReplayDeterministic(t *testing.T) {
	pool := integrationPool(t)
	first, err := New(testConfig(), 42, pool, func() time.Time { return testTime }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(testConfig(), 42, pool, func() time.Time { return testTime }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	for tick := 0; tick < 100; tick++ {
		for _, s := range []*Service{first, second} {
			if tick == 5 {
				id := submit(t, s).Proposal.ID
				if _, err := s.Decide(trustedContext(), id, proposal.Approve, testOperator); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Tick(); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(first.Snapshot(), second.Snapshot()) {
			t.Fatalf("same tick/command sequence diverged at tick %d", tick+1)
		}
	}
}
