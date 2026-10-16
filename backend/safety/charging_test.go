package safety_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

// Only Reader methods are available; unexpected reads panic through the nil
// embedded interface. Snapshot deliberately shares slices to expose mutations.
type snapshotReader struct {
	sim.Reader
	state sim.State
	calls int
}

func (r *snapshotReader) Snapshot() sim.State {
	r.calls++
	return r.state
}

func chargingState() sim.State {
	return sim.State{
		Tick: 12,
		Robots: []sim.RobotState{
			{ID: "R1", X: 0, Y: 0, Battery: 30, Status: sim.RobotIdle},
			{ID: "R2", X: 3, Y: 3, Battery: 80, Status: sim.RobotIdle},
		},
		Stations: []sim.StationState{{ID: "S1", X: 0, Y: 0}, {ID: "S2", X: 9, Y: 0}},
		Jobs:     []sim.JobState{{ID: "J1", Status: sim.JobPending}},
		Faults:   []sim.FaultState{{ID: "F1", Type: sim.FaultLowBattery, Robots: []string{"R2"}}},
		Aisles:   []sim.AisleState{{ID: 0}},
	}
}

func stateJSON(t *testing.T, state sim.State) string {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPreflightSetCharging(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*sim.State, *safety.SetChargingRequest)
		want   error
	}{
		{name: "valid boundary 30"},
		{name: "battery 31", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].Battery = 31 }},
		{name: "battery 100", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].Battery = 100 }},
		{name: "battery 29", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].Battery = 29 }, want: safety.ErrBatteryBelowMinimum},
		{name: "battery zero", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].Battery = 0 }, want: safety.ErrBatteryBelowMinimum},
		{name: "unknown robot", change: func(_ *sim.State, r *safety.SetChargingRequest) { r.RobotID = "missing" }, want: sim.ErrUnknownRobot},
		{name: "empty robot", change: func(_ *sim.State, r *safety.SetChargingRequest) { r.RobotID = "" }, want: sim.ErrUnknownRobot},
		{name: "unknown station", change: func(_ *sim.State, r *safety.SetChargingRequest) { r.StationID = "missing" }, want: sim.ErrUnknownStation},
		{name: "empty station", change: func(_ *sim.State, r *safety.SetChargingRequest) { r.StationID = "" }, want: sim.ErrUnknownStation},
		{name: "faulted", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].Status = sim.RobotFaulted }, want: sim.ErrRobotUnavailable},
		{name: "offline", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].Status = sim.RobotOffline }, want: sim.ErrRobotUnavailable},
		{name: "moving", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].Status = sim.RobotMoving }, want: sim.ErrRobotBusy},
		{name: "charging", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].Status = sim.RobotCharging }, want: sim.ErrRobotBusy},
		{name: "unrecognized status", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].Status = "unknown" }, want: sim.ErrRobotBusy},
		{name: "idle assigned", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].JobID = "J1" }, want: sim.ErrRobotBusy},
		{name: "wrong x", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].X++ }, want: sim.ErrNotAtStation},
		{name: "wrong y", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[0].Y++ }, want: sim.ErrNotAtStation},
		{name: "occupied by another", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Stations[0].OccupiedBy = "R2" }, want: sim.ErrStationOccupied},
		{name: "occupied by self", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Stations[0].OccupiedBy = "R1" }, want: sim.ErrStationOccupied},
		{name: "reserved by another", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Stations[0].ReservedBy = "R2" }, want: sim.ErrStationReserved},
		{name: "own reservation matches command", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Stations[0].ReservedBy = "R1" }},
		{name: "physically occupied", change: func(s *sim.State, _ *safety.SetChargingRequest) { s.Robots[1].X, s.Robots[1].Y = 0, 0 }, want: sim.ErrStationOccupied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := chargingState()
			request := safety.SetChargingRequest{RobotID: "R1", StationID: "S1"}
			if tc.change != nil {
				tc.change(&state, &request)
			}
			reader := &snapshotReader{state: state}
			before := stateJSON(t, state)
			if err := safety.PreflightSetCharging(reader, request); !errors.Is(err, tc.want) {
				t.Fatalf("preflight = %v, want %v", err, tc.want)
			}
			if reader.calls != 1 || stateJSON(t, reader.state) != before {
				t.Fatalf("snapshot calls = %d; preflight must read once and change nothing", reader.calls)
			}
		})
	}
}

func TestDeterministicRejection(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		base := chargingState()
		base.Robots[0].Status, base.Robots[0].Battery = sim.RobotOffline, 29
		base.Robots[0].JobID, base.Robots[0].X = "J1", 2
		base.Stations[0].OccupiedBy, base.Stations[0].ReservedBy = "R2", "R2"
		// Construct slices from maps to exercise different input iteration orders.
		robots := map[string]sim.RobotState{"R1": base.Robots[0], "R2": base.Robots[1]}
		stations := map[string]sim.StationState{"S1": base.Stations[0], "S2": base.Stations[1]}
		base.Robots, base.Stations = nil, nil
		for _, robot := range robots {
			base.Robots = append(base.Robots, robot)
		}
		for _, station := range stations {
			base.Stations = append(base.Stations, station)
		}
		for _, tc := range []struct {
			request safety.SetChargingRequest
			want    error
		}{
			{request: safety.SetChargingRequest{RobotID: "missing", StationID: "missing"}, want: sim.ErrUnknownRobot},
			{request: safety.SetChargingRequest{RobotID: "R1", StationID: "missing"}, want: sim.ErrUnknownStation},
			{request: safety.SetChargingRequest{RobotID: "R1", StationID: "S1"}, want: sim.ErrRobotUnavailable},
		} {
			if err := safety.PreflightSetCharging(&snapshotReader{state: base}, tc.request); !errors.Is(err, tc.want) {
				t.Fatalf("iteration %d: preflight = %v, want %v", iteration, err, tc.want)
			}
		}
	}
}

func TestRejectionPrecedence(t *testing.T) {
	state := chargingState()
	state.Robots[0].Status, state.Robots[0].Battery = sim.RobotOffline, 29
	state.Robots[0].JobID, state.Robots[0].X = "J1", 2
	state.Stations[0].OccupiedBy, state.Stations[0].ReservedBy = "R2", "R2"
	request := safety.SetChargingRequest{RobotID: "R1", StationID: "S1"}
	for _, tc := range []struct {
		resolve func()
		want    error
	}{
		{resolve: func() {}, want: sim.ErrRobotUnavailable},
		{resolve: func() { state.Robots[0].Status = sim.RobotIdle }, want: safety.ErrBatteryBelowMinimum},
		{resolve: func() { state.Robots[0].Battery = 30 }, want: sim.ErrStationOccupied},
		{resolve: func() { state.Stations[0].OccupiedBy = "" }, want: sim.ErrStationReserved},
		{resolve: func() { state.Stations[0].ReservedBy = "" }, want: sim.ErrRobotBusy},
		{resolve: func() { state.Robots[0].JobID = "" }, want: sim.ErrNotAtStation},
		{resolve: func() { state.Robots[0].X = 0 }},
	} {
		tc.resolve()
		if err := safety.PreflightSetCharging(&snapshotReader{state: state}, request); !errors.Is(err, tc.want) {
			t.Fatalf("preflight = %v, want %v", err, tc.want)
		}
	}
}

func TestPreflightDoesNotChangeSimulatorOrReplay(t *testing.T) {
	cfg := sim.DefaultConfig()
	cfg.RobotStarts[0] = cfg.Stations[0]
	subject, err := sim.New(cfg, 42)
	if err != nil {
		t.Fatal(err)
	}
	control, err := sim.New(cfg, 42)
	if err != nil {
		t.Fatal(err)
	}
	// This wrapper exposes no Commander or Driver methods, even though the
	// underlying simulator supports them.
	reader := struct{ sim.Reader }{Reader: subject}
	for tick := 0; tick < 100; tick++ {
		before := subject.Snapshot()
		for _, request := range []safety.SetChargingRequest{
			{RobotID: "R1", StationID: "S1"},
			{RobotID: "missing", StationID: "S1"},
		} {
			err := safety.PreflightSetCharging(reader, request)
			if tick == 0 && request.RobotID == "R1" && err != nil {
				t.Fatalf("initial preflight = %v, want nil", err)
			}
		}
		if !reflect.DeepEqual(before, subject.Snapshot()) {
			t.Fatalf("preflight mutated simulator at tick %d", tick)
		}
		subject.Tick()
		control.Tick()
		if !reflect.DeepEqual(subject.Snapshot(), control.Snapshot()) {
			t.Fatalf("preflight changed subsequent replay at tick %d", tick+1)
		}
	}
}
