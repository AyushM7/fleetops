package fleet

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

var testTime = time.Date(2026, 10, 16, 12, 0, 0, 123456789, time.UTC)
var chargingRequest = safety.SetChargingRequest{RobotID: "R1", StationID: "S1"}
var testOperator = proposal.OperatorIdentity{Subject: "operator-7"}

type operatorKey struct{}

func trustedContext() context.Context {
	return context.WithValue(context.Background(), operatorKey{}, testOperator.Subject)
}
func authorized(ctx context.Context, operator proposal.OperatorIdentity) bool {
	return ctx.Value(operatorKey{}) == operator.Subject && operator.Subject == testOperator.Subject
}

func testConfig() sim.Config {
	cfg := sim.DefaultConfig()
	cfg.RobotStarts[0] = cfg.Stations[0]
	cfg.InitialJobs, cfg.AutoDispatch, cfg.IdleDrainEvery = 0, false, 1
	return cfg
}

type countedCommander struct {
	*sim.Sim
	calls       int
	reads       int
	fail        bool
	staleAtRead int
}

func (c *countedCommander) Snapshot() sim.State {
	c.reads++
	state := c.Sim.Snapshot()
	if c.staleAtRead != 0 && c.reads >= c.staleAtRead {
		state.Robots[0].Battery = 29
	}
	return state
}

func (c *countedCommander) SetCharging(robot, station string) error {
	c.calls++
	if c.fail {
		return sim.ErrStationOccupied
	}
	return c.Sim.SetCharging(robot, station)
}

func unitService(t *testing.T) *Service {
	t.Helper()
	simulator, err := sim.New(testConfig(), 42)
	if err != nil {
		t.Fatal(err)
	}
	s, err := newService(simulator, simulator.Tick, &db.Pool{}, func() time.Time { return testTime }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	s.transact = func(context.Context, func(*db.Tx) error) error { return db.ErrQuery }
	return s
}

func TestServiceRequiresDependencies(t *testing.T) {
	for _, tc := range []struct {
		pool  *db.Pool
		clock proposal.Clock
		auth  Authorizer
	}{
		{clock: func() time.Time { return testTime }, auth: authorized},
		{pool: &db.Pool{}, auth: authorized},
		{pool: &db.Pool{}, clock: func() time.Time { return testTime }},
	} {
		if _, err := New(testConfig(), 42, tc.pool, tc.clock, tc.auth); !errors.Is(err, ErrConfig) {
			t.Fatal(err)
		}
	}
}

func TestPersistenceFailurePoisonsWithoutMutation(t *testing.T) {
	s := unitService(t)
	before := s.Snapshot()
	r, err := s.Submit(context.Background(), chargingRequest, testTime.Add(time.Minute))
	if !errors.Is(err, ErrPersistence) || !errors.Is(err, ErrPoisoned) || r.AuditCommitted || r.ExecutionAttempted || r.Execution != Unknown || !s.Poisoned() {
		t.Fatalf("failed persistence: %+v, %v", r, err)
	}
	if err := s.Tick(); !errors.Is(err, ErrPoisoned) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatal("failed persistence changed simulator")
	}
}

func TestTickAndSnapshotSerialize(t *testing.T) {
	s := unitService(t)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 20 {
				if err := s.Tick(); err != nil {
					t.Error(err)
				}
				_ = s.Snapshot()
			}
		}()
	}
	workers.Wait()
	if s.Snapshot().Tick != 160 {
		t.Fatal("serialized ticks were lost")
	}
}

func TestUnexpectedPanicPoisonsRecoveredRuntime(t *testing.T) {
	s := unitService(t)
	s.transact = func(context.Context, func(*db.Tx) error) error { panic("synthetic transaction panic") }
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed")
			}
		}()
		_, _ = s.Submit(context.Background(), chargingRequest, testTime.Add(time.Minute))
	}()
	if !s.Poisoned() || !errors.Is(s.Tick(), ErrPoisoned) {
		t.Fatal("recovered runtime was allowed to mutate")
	}
}
