//go:build fleetops_test

package sim

import (
	"reflect"
	"testing"
)

func TestAgent07FixturesRepeatAndTick(t *testing.T) {
	for _, variant := range []string{"blocked", "non-first", "tradeoff", "tie", "none", "healthy", "poisoned"} {
		t.Run(variant, func(t *testing.T) {
			a, err := NewAgent07TestFixture(variant)
			if err != nil {
				t.Fatal(err)
			}
			b, err := NewAgent07TestFixture(variant)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(a.Snapshot(), b.Snapshot()) {
				t.Fatal("non-repeatable initial conditions")
			}
			for range 200 {
				a.Tick()
				b.Tick()
			}
			if !reflect.DeepEqual(a.Snapshot(), b.Snapshot()) {
				t.Fatal("non-repeatable horizon")
			}
		})
	}
	if _, err := NewAgent07TestFixture("invented"); err == nil {
		t.Fatal("unknown fixture admitted")
	}
}

func TestAgent07CandidateOrderingAndTies(t *testing.T) {
	for _, variant := range []string{"non-first", "tie", "none", "tradeoff"} {
		s, err := NewAgent07TestFixture(variant)
		if err != nil {
			t.Fatal(err)
		}
		steps := map[string]int{}
		headroom := map[string]int{}
		for _, robot := range s.Snapshot().Robots {
			plan, err := s.PreviewReassignment("J1", "R1", robot.ID)
			if err == nil {
				steps[robot.ID] = plan.PickupSteps
				threshold := max(30, plan.RequiredBattery)
				headroom[robot.ID] = robot.Battery - threshold
			}
		}
		switch variant {
		case "non-first":
			if len(steps) != 3 || steps["R3"] != 6 || steps["R4"] != 1 {
				t.Fatal(steps)
			}
		case "tie":
			if len(steps) != 3 || steps["R3"] != 1 || steps["R4"] != 1 {
				t.Fatal(steps)
			}
		case "none":
			if len(steps) != 0 {
				t.Fatal(steps)
			}
		case "tradeoff":
			if steps["R3"] >= steps["R4"] || headroom["R3"] >= headroom["R4"] {
				t.Fatal(steps, headroom)
			}
		}
	}
}
