//go:build fleetops_test

package fleet

import (
	"reflect"
	"testing"

	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestDemoScenarioPresets(t *testing.T) {
	states := map[string]sim.State{}
	for _, id := range []string{"blocked-reassignment", "routine-charging", "healthy-operations", "blocked-no-candidate"} {
		t.Run(id, func(t *testing.T) {
			a, width, height, err := demoSimulator(id)
			if err != nil {
				t.Fatal(err)
			}
			b, _, _, err := demoSimulator(id)
			if err != nil || !reflect.DeepEqual(a.Snapshot(), b.Snapshot()) {
				t.Fatal("initial state not reproducible", err)
			}
			initial := a.Snapshot()
			states[id] = initial
			switch id {
			case "routine-charging":
				r, station := initial.Robots[0], initial.Stations[0]
				if initial.Tick != 1000 || r.Battery != 50 || r.X != station.X || r.Y != station.Y || r.Status != sim.RobotIdle || station.OccupiedBy != "" || len(initial.Jobs) != 0 {
					t.Fatal("charging setup changed", initial)
				}
				req := safety.SetChargingRequest{RobotID: "R1", StationID: "S1"}
				if err := safety.PreflightSetCharging(a, req); err != nil {
					t.Fatal("charging not admitted", err)
				}
				if err := a.SetCharging("R1", "S1"); err != nil {
					t.Fatal(err)
				}
				a.Tick()
				if a.Snapshot().Robots[0].Battery != 52 || a.Snapshot().Stations[0].OccupiedBy != "R1" {
					t.Fatal("charging effects missing")
				}
			case "healthy-operations":
				if initial.Tick != 0 || width != 20 || height != 12 || len(initial.Jobs) != 8 || len(initial.Robots) != 6 {
					t.Fatal("active warehouse setup changed")
				}
				for _, r := range initial.Robots {
					if r.Blocked || r.Status == sim.RobotOffline || r.Status == sim.RobotFaulted {
						t.Fatal("unhealthy startup", r)
					}
				}
				moved, done := false, false
				for range 40 {
					a.Tick()
					for i, r := range a.Snapshot().Robots {
						moved = moved || r.X != initial.Robots[i].X || r.Y != initial.Robots[i].Y
					}
					for _, j := range a.Snapshot().Jobs {
						done = done || j.Status == sim.JobDone
					}
				}
				if !moved || !done {
					t.Fatal("active operations did not move and deliver")
				}
			default:
				if initial.Tick != 42 || width != 8 || height != 8 || !initial.Robots[0].Blocked || initial.Robots[1].Status != sim.RobotOffline {
					t.Fatal("blocked fixture changed")
				}
				if id == "blocked-no-candidate" {
					for _, r := range initial.Robots[2:] {
						if r.Battery >= 30 {
							t.Fatal("recipient eligible", r)
						}
					}
				}
			}
			for range 100 {
				b.Tick()
			}
			repeat, _, _, _ := demoSimulator(id)
			for range 100 {
				repeat.Tick()
			}
			if !reflect.DeepEqual(b.Snapshot(), repeat.Snapshot()) {
				t.Fatal("progression not deterministic")
			}
		})
	}
	if reflect.DeepEqual(states["blocked-reassignment"], states["blocked-no-candidate"]) {
		t.Fatal("no-candidate not distinct")
	}
	if _, _, _, err := demoSimulator("visitor-config"); err == nil {
		t.Fatal("arbitrary preset accepted")
	}
}
