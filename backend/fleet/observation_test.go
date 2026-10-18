package fleet

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"testing"

	"github.com/AyushM7/fleetops/backend/proposal"
)

func TestObservationIdentityRuntimeAndRevision(t *testing.T) {
	s, other := unitService(t), unitService(t)
	a, err := s.ReadState(context.Background())
	if err != nil || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(a.ObservationID) {
		t.Fatal("missing opaque identity", err)
	}
	b, _ := s.ReadState(context.Background())
	c, _ := other.ReadState(context.Background())
	if a.ObservationID != b.ObservationID || a.ObservationID == c.ObservationID || !reflect.DeepEqual(s.Snapshot(), other.Snapshot()) {
		t.Fatal("identity must be stable for a state and distinct across identical runtimes")
	}
	if err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	b, _ = s.ReadState(context.Background())
	if a.ObservationID == b.ObservationID || b.Tick != a.Tick+1 {
		t.Fatal("tick did not invalidate observation")
	}
}

func TestObservationInvalidatedBySameTickCommand(t *testing.T) {
	s := unitService(t)
	a, _ := s.ReadState(context.Background())
	s.mu.Lock()
	err := s.executeCommand(proposal.Proposal{Action: proposal.SetCharging, Request: chargingRequest})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.ReadState(context.Background())
	if a.Tick != b.Tick || a.ObservationID == b.ObservationID || reflect.DeepEqual(a.Robots, b.Robots) {
		t.Fatal("same-tick command did not invalidate observation")
	}
}

func TestObservationFailureAndOverflowFailClosed(t *testing.T) {
	s := unitService(t)
	a, _ := s.ReadState(context.Background())
	s.mu.Lock()
	err := s.executeCommand(proposal.Proposal{Action: proposal.SetCharging})
	s.mu.Unlock()
	b, _ := s.ReadState(context.Background())
	if err == nil || a.ObservationID == b.ObservationID {
		t.Fatal("failed command attempt must invalidate previous observation")
	}
	s.revision = ^uint64(0)
	before := s.Snapshot()
	if !errors.Is(s.Tick(), ErrPoisoned) || !s.Poisoned() || !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("revision overflow was allowed to mutate or reuse a revision")
	}
}

func TestObservationTickPanicPoisonsRuntime(t *testing.T) {
	s := unitService(t)
	before, _ := s.ReadState(context.Background())
	s.tick = func() { panic("test tick panic") }
	func() {
		defer func() {
			if recover() == nil {
				t.Error("tick panic was swallowed")
			}
		}()
		_ = s.Tick()
	}()
	after, _ := s.ReadState(context.Background())
	if !after.Poisoned || before.ObservationID == after.ObservationID {
		t.Fatal("partial tick failure did not invalidate observation and poison runtime")
	}
}
