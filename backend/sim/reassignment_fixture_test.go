//go:build fleetops_test

package sim_test

import (
	"reflect"
	"testing"

	"github.com/AyushM7/fleetops/backend/sim"
)

func TestGuidedReassignmentInitialAndProgression(t *testing.T) {
	a,err := sim.NewReassignmentInitialFixture(); if err != nil { t.Fatal(err) }
	b,err := sim.NewReassignmentInitialFixture(); if err != nil { t.Fatal(err) }
	initial := a.Snapshot()
	if initial.Tick != 0 || initial.Seed != 42 || initial.Robots[1].Status != sim.RobotOffline || initial.Robots[0].Blocked || initial.Robots[0].WaitTicks != 0 || initial.Jobs[0].AssignedRobot != "R1" { t.Fatal("untruthful initial fixture",initial) }
	for tick:=1;tick<=42;tick++ {
		a.Tick();b.Tick(); state:=a.Snapshot()
		if !reflect.DeepEqual(state,b.Snapshot()) { t.Fatalf("non-deterministic tick %d",tick) }
		if state.Robots[0].WaitTicks != tick || state.Robots[0].Blocked != (tick>=10) || state.Robots[1].Status != sim.RobotOffline { t.Fatalf("unexpected progression at %d",tick) }
		for i,robot := range state.Robots { if robot.X != initial.Robots[i].X || robot.Y != initial.Robots[i].Y { t.Fatalf("fixture movement changed at %d: %+v",tick,robot) } }
	}
	direct,err:=sim.NewReassignmentTestFixture();if err!=nil { t.Fatal(err) }
	if !reflect.DeepEqual(a.Snapshot(),direct.Snapshot()) || a.Snapshot().Robots[2].Battery != 98 { t.Fatal("historical intervention fixture changed") }
	t.Log("observed: R2 offline at tick 0; fixed positions; R1 wait 0→42, Blocked at 10; R3 battery 100→98; exact direct tick-42 equality")
}
