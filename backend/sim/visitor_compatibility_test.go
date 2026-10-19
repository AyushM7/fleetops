//go:build fleetops_test

package sim

import (
	"reflect"
	"testing"
)

func TestVisitorPresetsMatchPUB01Integration(t *testing.T) {
	for _, id := range []string{"blocked-reassignment", "blocked-no-candidate"} {
		old, err := NewReassignmentTestFixture()
		if err != nil {
			t.Fatal(err)
		}
		if id == "blocked-no-candidate" {
			for _, robot := range []string{"R3", "R4", "R5"} {
				if err := old.InjectLowBattery(robot); err != nil {
					t.Fatal(err)
				}
			}
		}
		next, _, _, err := NewVisitorPreset(id)
		if err != nil || !reflect.DeepEqual(old.Snapshot(), next.Snapshot()) {
			t.Fatal("PUB-01 preset changed", id, err)
		}
	}
	for _, id := range []string{"routine-charging", "healthy-operations"} {
		cfg := WarehouseConfig()
		if id == "routine-charging" {
			cfg = DefaultConfig()
			cfg.InitialJobs, cfg.MaxJobs, cfg.NewJobEvery = 0, 1, 10000
			cfg.RobotStarts[0] = cfg.Stations[0]
		}
		old, err := New(cfg, 42)
		if err != nil {
			t.Fatal(err)
		}
		if id == "routine-charging" {
			for range 1000 {
				old.Tick()
			}
		}
		next, _, _, err := NewVisitorPreset(id)
		if err != nil || !reflect.DeepEqual(old.Snapshot(), next.Snapshot()) {
			t.Fatal("PUB-01 preset changed", id, err)
		}
	}
}
