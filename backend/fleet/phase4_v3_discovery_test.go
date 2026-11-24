//go:build fleetops_test

package fleet

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/AyushM7/fleetops/backend/sim"
)

func TestPhase4V3ObservationDiscovery(t *testing.T) {
	if os.Getenv("FLEETOPS_PHASE4_DISCOVER") != "1" {
		t.Skip("opt in to Phase 4 scenario discovery")
	}
	for _, profile := range []string{"regular", "busy", "charging", "unresponsive", "physical-fault", "two-faults", "critical-battery"} {
		for _, seed := range []int64{2, 7, 42, 99} {
			t.Run(fmt.Sprintf("%s/seed%d", profile, seed), func(t *testing.T) {
				discoverPhase4Observations(t, profile, seed)
			})
		}
	}
	for _, profile := range []string{"single-eligible", "recipient-threshold", "exact-mission-energy", "headroom-tie", "charging-recipient",
		"cargo-in-transit", "source-energy-denial", "route-altered", "pickup-and-energy-conflict", "battery-scarcity"} {
		variant := "phase4/lab-" + profile + "/42/42"
		simulator, err := sim.NewAgent07TestFixture(variant)
		if err != nil {
			t.Fatal(err)
		}
		state := simulator.Snapshot()
		incidents, err := currentIncidents(state)
		if err != nil {
			t.Fatal(err)
		}
		class, facts := phase4ObservationClass(simulator, state, incidents)
		t.Logf("PHASE4_OBSERVATION %s class=%s %s", strings.TrimPrefix(variant, "phase4/"), class, facts)
	}
}

func discoverPhase4Observations(t *testing.T, profile string, seed int64) {
	t.Helper()
	simulator, err := sim.NewPhase4V3Prelude(profile, seed)
	if err != nil {
		t.Fatal(err)
	}
	last := make(map[string]int)
	for tick := 0; tick <= 450; tick++ {
		state := simulator.Snapshot()
		incidents, err := currentIncidents(state)
		if err != nil {
			t.Fatal(err)
		}
		if len(incidents) <= 3 {
			class, facts := phase4ObservationClass(simulator, state, incidents)
			if prior, seen := last[class]; !seen || tick-prior >= 70 {
				t.Logf("PHASE4_OBSERVATION %s/%d/%d class=%s %s", profile, seed, tick, class, facts)
				last[class] = tick
			}
		}
		simulator.Tick()
	}
}

func phase4ObservationClass(simulator *sim.Sim, state sim.State, incidents []IncidentView) (string, string) {
	var kinds, candidates []string
	blocked, charging, minPickup, maxHeadroom := 0, 0, 1<<30, -1<<30
	minOptions, maxOptions := 0, 0
	intersects := false
	for _, incident := range incidents {
		kinds = append(kinds, incident.RobotID+":"+incident.Type)
		if incident.Type == "traffic_blocked" {
			blocked++
		}
	}
	var metrics [][2]int
	for _, robot := range state.Robots {
		if robot.Status == sim.RobotCharging {
			charging++
		}
		for _, job := range state.Jobs {
			plan, err := simulator.PreviewReassignment(job.ID, job.AssignedRobot, robot.ID)
			if err != nil {
				continue
			}
			headroom := robot.Battery - max(30, plan.RequiredBattery)
			metrics = append(metrics, [2]int{plan.PickupSteps, headroom})
			candidates = append(candidates, fmt.Sprintf("%s:%s>%s:%d/%d", job.ID, job.AssignedRobot, robot.ID, plan.PickupSteps, headroom))
			minPickup = min(minPickup, plan.PickupSteps)
			maxHeadroom = max(maxHeadroom, headroom)
		}
	}
	for _, metric := range metrics {
		if metric[0] == minPickup {
			minOptions++
		}
		if metric[1] == maxHeadroom {
			maxOptions++
		}
		intersects = intersects || metric == ([2]int{minPickup, maxHeadroom})
	}
	class := fmt.Sprintf("inc%d-block%d-can%d-charge%d-tie%t-%t-tradeoff%t", len(incidents), blocked,
		min(len(candidates), 4), min(charging, 2), minOptions > 1, maxOptions > 1, len(candidates) > 1 && !intersects)
	return class, fmt.Sprintf("inc=%s can=%s", strings.Join(kinds, ","), strings.Join(candidates, ","))
}
