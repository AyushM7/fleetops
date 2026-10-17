package fleet

import (
	"context"
	"reflect"
	"testing"

	"github.com/AyushM7/fleetops/backend/incident"
	"github.com/AyushM7/fleetops/backend/sim"
)

// Only trusted tests supply synthetic snapshots; no runtime injection endpoint exists.
type incidentCommander struct {
	sim.Commander
	state sim.State
	reads int
}

func (c *incidentCommander) Snapshot() sim.State { c.reads++; return c.state }

func TestCurrentIncidentsUseOneDetachedReadOnlySnapshot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		robot    sim.RobotState
		kind     incident.Type
		severity incident.Severity
	}{
		{"low high", sim.RobotState{ID: "R1", Battery: 14, JobID: "J1"}, incident.LowBattery, incident.High},
		{"low boundary", sim.RobotState{ID: "R1", Battery: 15, JobID: "J1"}, incident.LowBattery, incident.Medium},
		{"low medium", sim.RobotState{ID: "R1", Battery: 29, JobID: "J1"}, incident.LowBattery, incident.Medium},
		{"minimum normal", sim.RobotState{ID: "R1", Battery: 30, JobID: "J1"}, "", ""},
		{"low without job", sim.RobotState{ID: "R1", Battery: 14}, "", ""},
		{"offline", sim.RobotState{ID: "R1", Battery: 100, Status: sim.RobotOffline}, incident.RobotUnresponsive, incident.High},
		{"blocked zero", sim.RobotState{ID: "R1", Battery: 100, Blocked: true}, incident.TrafficBlocked, incident.Medium},
		{"blocked boundary", sim.RobotState{ID: "R1", Battery: 100, Blocked: true, WaitTicks: 40}, incident.TrafficBlocked, incident.Medium},
		{"blocked high", sim.RobotState{ID: "R1", Battery: 100, Blocked: true, WaitTicks: 41}, incident.TrafficBlocked, incident.High},
		{"waiting normally", sim.RobotState{ID: "R1", Battery: 100, WaitTicks: 100}, "", ""},
		{"healthy job", sim.RobotState{ID: "R1", Battery: 100, JobID: "J1", Status: sim.RobotMoving}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := unitService(t)
			c := &incidentCommander{state: sim.State{Tick: 123, Robots: []sim.RobotState{tc.robot}}}
			s.commander = c
			s.tick = func() { t.Fatal("read ticked simulator") }
			view, err := s.ReadState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if c.reads != 1 || view.Tick != 123 {
				t.Fatal("read was not one coherent snapshot")
			}
			if tc.kind == "" {
				if view.ActiveIncidents == nil || len(view.ActiveIncidents) != 0 {
					t.Fatal(view.ActiveIncidents)
				}
			} else {
				want := IncidentView{RobotID: "R1", Type: string(tc.kind), Severity: string(tc.severity), Telemetry: view.Robots[0]}
				if len(view.ActiveIncidents) != 1 || view.ActiveIncidents[0] != want {
					t.Fatal(view.ActiveIncidents)
				}
			}
			again, err := s.ReadState(context.Background())
			if err != nil || !reflect.DeepEqual(view, again) {
				t.Fatal("non-deterministic read", err)
			}
			view.Robots[0].Battery = 0
			if len(view.ActiveIncidents) > 0 {
				view.ActiveIncidents[0].Telemetry.Battery = 0
			}
			if c.state.Robots[0] != tc.robot {
				t.Fatal("read DTO aliases simulator")
			}
			c.state.Robots = nil
			cleared, err := s.ReadState(context.Background())
			if err != nil || len(cleared.ActiveIncidents) != 0 {
				t.Fatal("read retained incident history", err)
			}
		})
	}
}

func TestCurrentIncidentsOrderingAndInvalidSnapshot(t *testing.T) {
	s := unitService(t)
	c := &incidentCommander{state: sim.State{Robots: []sim.RobotState{
		{ID: "R10", Battery: 100, Status: sim.RobotOffline},
		{ID: "R2", Battery: 14, JobID: "J1", Blocked: true, WaitTicks: 41},
	}}}
	s.commander = c
	view, err := s.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"R2/low_battery", "R2/traffic_blocked", "R10/robot_unresponsive"}
	if len(view.ActiveIncidents) != len(want) {
		t.Fatal(view.ActiveIncidents)
	}
	for i, v := range view.ActiveIncidents {
		if v.RobotID+"/"+v.Type != want[i] {
			t.Fatal(view.ActiveIncidents)
		}
	}
	c.state.Robots[0], c.state.Robots[1] = c.state.Robots[1], c.state.Robots[0]
	again, err := s.ReadState(context.Background())
	if err != nil || !reflect.DeepEqual(view.ActiveIncidents, again.ActiveIncidents) {
		t.Fatal("order changed", err)
	}
	c.state.Robots[0].ID = "invalid"
	if _, err := s.ReadState(context.Background()); err == nil {
		t.Fatal("invalid detector snapshot accepted")
	}
}
