package sim

import (
	"errors"
	"maps"
	"reflect"
	"testing"
)

func assertChargingFailure(t *testing.T, s *Sim, robotID, stationID string, want error) {
	t.Helper()
	before, cfg := s.Snapshot(), s.cfg.Clone()
	countdowns := maps.Clone(s.unresponsiveTicks)
	if err := s.SetCharging(robotID, stationID); !errors.Is(err, want) {
		t.Fatalf("SetCharging(%q, %q) = %v, want %v", robotID, stationID, err, want)
	}
	if !reflect.DeepEqual(s.Snapshot(), before) || !reflect.DeepEqual(s.cfg, cfg) || !reflect.DeepEqual(s.unresponsiveTicks, countdowns) {
		t.Fatal("failed command changed state, config, or countdowns")
	}
}

func TestSetChargingErrors(t *testing.T) {
	for _, tc := range []struct {
		name, robot, station string
		status               RobotStatus
		job, occupant        string
		want                 error
	}{
		{"unknown robot first", "missing", "missing", RobotMoving, "J1", "R1", ErrUnknownRobot},
		{"unknown station before busy", "R1", "missing", RobotMoving, "J1", "", ErrUnknownStation},
		{"occupied before busy and position", "R2", "S1", RobotMoving, "J1", "R1", ErrStationOccupied},
		{"occupied before position", "R2", "S1", RobotIdle, "", "R1", ErrStationOccupied},
		{"moving", "R2", "S1", RobotMoving, "", "", ErrRobotBusy},
		{"charging", "R2", "S1", RobotCharging, "", "", ErrRobotBusy},
		{"faulted", "R2", "S1", RobotFaulted, "", "", ErrRobotBusy},
		{"offline", "R2", "S1", RobotOffline, "", "", ErrRobotBusy},
		{"idle with job", "R2", "S1", RobotIdle, "J1", "", ErrRobotBusy},
		{"not at station", "R2", "S1", RobotIdle, "", "", ErrNotAtStation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSim(t, cfgRobotOnStation(), 1)
			for i := range s.state.Robots {
				if s.state.Robots[i].ID == tc.robot {
					s.state.Robots[i].Status, s.state.Robots[i].JobID = tc.status, tc.job
				}
			}
			s.state.Stations[0].OccupiedBy = tc.occupant
			assertChargingFailure(t, s, tc.robot, tc.station, tc.want)
		})
	}
}

func TestChargingCycle(t *testing.T) {
	cfg := cfgRobotOnStation()
	s := newTestSim(t, cfg, 1)
	s.state.Robots[0].Battery = 15
	before := s.Snapshot()
	if err := s.SetCharging("R1", "S1"); err != nil {
		t.Fatal(err)
	}
	before.Robots[0].Status = RobotCharging
	before.Stations[0].OccupiedBy = "R1"
	if !reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatal("successful command changed more than status and occupancy")
	}
	assertChargingFailure(t, s, "R1", "S1", ErrStationOccupied)
	assertInvariants(t, s, cfg)
	for tick := 1; tick <= 43; tick++ {
		s.Tick()
		want := RobotState{ID: "R1", Battery: min(100, 15+2*tick), Status: RobotCharging}
		occupant := "R1"
		if tick == 43 {
			want.Status, occupant = RobotIdle, ""
		}
		if s.state.Robots[0] != want || s.state.Stations[0].OccupiedBy != occupant || len(s.state.Faults) != 0 {
			t.Fatalf("tick %d: robot %+v, station %+v, faults %v", tick, s.state.Robots[0], s.state.Stations[0], s.state.Faults)
		}
		assertInvariants(t, s, cfg)
	}
}

func TestChargingFullBattery(t *testing.T) {
	cfg := cfgRobotOnStation()
	s := newTestSim(t, cfg, 1)
	s.state.Tick = 19 // Completion must skip even a scheduled idle drain.
	if err := s.SetCharging("R1", "S1"); err != nil {
		t.Fatal(err)
	}
	assertInvariants(t, s, cfg)
	s.Tick()
	want := RobotState{ID: "R1", Battery: 100, Status: RobotIdle}
	if s.state.Robots[0] != want || s.state.Stations[0].OccupiedBy != "" || len(s.state.Faults) != 0 {
		t.Fatalf("full battery completion: %+v, %+v", s.state.Robots[0], s.state.Stations[0])
	}
	assertInvariants(t, s, cfg)
}

func TestChargingCountdown(t *testing.T) {
	for _, battery := range []int{15, 99} {
		cfg := cfgRobotOnStation()
		s := newTestSim(t, cfg, 1)
		s.state.Robots[0].Battery = battery
		setJobCells(t, s, "J1", Point{0, 11}, Point{19, 11})
		if err := s.SetCharging("R1", "S1"); err != nil {
			t.Fatal(err)
		}
		delay := cfg.UnresponsiveDelay
		if battery == 99 {
			delay = 1 // Expiry must win over a charge that would free the station.
		}
		s.unresponsiveTicks["R1"] = delay
		assertInvariants(t, s, cfg)
		for tick := 1; tick <= 20; tick++ {
			s.Tick()
			want := RobotState{ID: "R1", Battery: battery + 2*min(tick, delay-1), Status: RobotCharging}
			if tick >= delay {
				want.Status = RobotOffline
			}
			if s.state.Robots[0] != want || s.state.Stations[0].OccupiedBy != "R1" || s.unresponsiveTicks["R1"] != max(0, delay-tick) || len(s.state.Faults) != 0 {
				t.Fatalf("battery %d tick %d: robot %+v, station %+v", battery, tick, s.state.Robots[0], s.state.Stations[0])
			}
			assertInvariants(t, s, cfg)
		}
		if s.state.Robots[1].Status != RobotMoving || s.state.Robots[1].JobID == "" {
			t.Fatal("fixture requires R2 to be busy")
		}
		assertChargingFailure(t, s, "R2", "S1", ErrStationOccupied)
		assertChargingFailure(t, s, "R1", "S1", ErrStationOccupied)
	}
}

func TestChargingStationsIndependent(t *testing.T) {
	cfg := cfgRobotOnStation()
	cfg.ChargeGain = 7
	s := newTestSim(t, cfg, 1)
	s.state.Robots[0].Battery, s.state.Robots[1].Battery = 99, 15
	for _, pair := range [][2]string{{"R1", "S1"}, {"R2", "S2"}} {
		if err := s.SetCharging(pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	s.Tick()
	if s.state.Robots[0].Battery != 100 || s.state.Robots[0].Status != RobotIdle || s.state.Stations[0].OccupiedBy != "" {
		t.Fatal("S1 did not finish charging")
	}
	if s.state.Robots[1].Battery != 22 || s.state.Robots[1].Status != RobotCharging || s.state.Stations[1].OccupiedBy != "R2" {
		t.Fatal("S1 completion changed S2 or ignored configured ChargeGain")
	}
	assertInvariants(t, s, cfg)
}
