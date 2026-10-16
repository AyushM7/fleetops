package sim

import (
	"reflect"
	"testing"
)

func TestSimulatorRoles(t *testing.T) {
	cfg := cfgRobotOnStation()
	s := newTestSim(t, cfg, 42)
	var reader Reader = s
	var commander Commander = s
	var driver Driver = s
	want := s.Snapshot()
	for _, role := range []Reader{reader, commander, driver} {
		if !reflect.DeepEqual(role.Snapshot(), want) || role.CellAt(Point{0, 0}) != CellStation {
			t.Fatal("role reads differ from simulator reads")
		}
		route, err := role.PlanRoute(Point{1, 0}, Point{0, 0})
		if err != nil || !reflect.DeepEqual(route, []Point{{0, 0}}) {
			t.Fatalf("role PlanRoute = %v, %v", route, err)
		}
		if occupant, err := role.AisleOccupant(0); err != nil || occupant != "" {
			t.Fatalf("role AisleOccupant = %q, %v", occupant, err)
		}
		if aisles, err := role.OccupiedAislesOnRoute(route, "R1"); err != nil || aisles == nil || len(aisles) != 0 {
			t.Fatalf("role OccupiedAislesOnRoute = %v, %v", aisles, err)
		}
	}
	if err := commander.SetCharging("R1", "S1"); err != nil {
		t.Fatal(err)
	}
	for _, inject := range []func() error{
		func() error { return driver.InjectLowBattery("R1") },
		func() error { return driver.InjectUnresponsive("R1") },
		func() error { return driver.InjectAisleConflict("R2", "R3", 0) },
	} {
		if err := inject(); err != nil {
			t.Fatal(err)
		}
	}
	got := reader.Snapshot()
	if got.Robots[0].Status != RobotCharging || got.Robots[0].Battery != cfg.LowBatteryInjectLevel || got.Stations[0].OccupiedBy != "R1" || len(got.Faults) != 3 {
		t.Fatalf("role operations produced unexpected state: %+v", got)
	}
}
