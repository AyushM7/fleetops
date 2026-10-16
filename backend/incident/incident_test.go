package incident_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/AyushM7/fleetops/backend/incident"
	"github.com/AyushM7/fleetops/backend/sim"
)

func reconcile(t *testing.T, previous []incident.Incident, state sim.State, counter uint64) ([]incident.Incident, uint64) {
	t.Helper()
	got, next, err := incident.Reconcile(previous, state, counter)
	if err != nil {
		t.Fatal(err)
	}
	return got, next
}

func TestOpenResolveAndRecurrence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		active   sim.RobotState
		kind     incident.Type
		severity incident.Severity
	}{
		{name: "battery", active: sim.RobotState{ID: "R1", Battery: 29, JobID: "J1"}, kind: incident.LowBattery, severity: incident.Medium},
		{name: "offline", active: sim.RobotState{ID: "R1", Status: sim.RobotOffline}, kind: incident.RobotUnresponsive, severity: incident.High},
		{name: "traffic", active: sim.RobotState{ID: "R1", Blocked: true, WaitTicks: 40}, kind: incident.TrafficBlocked, severity: incident.Medium},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := sim.State{Robots: []sim.RobotState{tc.active}}
			opened, counter := reconcile(t, nil, state, 40)
			want := incident.Incident{ID: "I41", RobotID: "R1", Type: tc.kind, Status: incident.Open, Severity: tc.severity}
			if len(opened) != 1 || opened[0] != want || counter != 41 {
				t.Fatalf("opened = %+v, counter = %d", opened, counter)
			}
			repeated, next := reconcile(t, opened, state, counter)
			if !reflect.DeepEqual(repeated, opened) || next != counter {
				t.Fatal("repeated snapshot duplicated or changed incidents")
			}
			state.Robots[0] = sim.RobotState{ID: "R1", Battery: 100, Status: sim.RobotIdle}
			resolved, next := reconcile(t, opened, state, counter)
			want.Status = incident.Resolved
			if len(resolved) != 1 || resolved[0] != want || next != counter {
				t.Fatalf("resolved = %+v, counter = %d", resolved, next)
			}
			again, next := reconcile(t, resolved, state, counter)
			if !reflect.DeepEqual(again, resolved) || next != counter {
				t.Fatal("resolved episode changed on repeated clear state")
			}
			state.Robots[0] = tc.active
			recurred, next := reconcile(t, resolved, state, counter)
			if len(recurred) != 2 || recurred[0] != want || recurred[1].ID != "I42" || recurred[1].Status != incident.Open || next != 42 {
				t.Fatalf("recurrence = %+v, counter = %d", recurred, next)
			}
		})
	}
}

func TestBatterySeverityAndJobBoundaries(t *testing.T) {
	for _, tc := range []struct {
		battery int
		want    incident.Severity
	}{
		{battery: 0, want: incident.High}, {battery: 14, want: incident.High},
		{battery: 15, want: incident.Medium}, {battery: 29, want: incident.Medium},
		{battery: 30}, {battery: 31}, {battery: 100},
	} {
		for _, status := range []sim.RobotStatus{sim.RobotMoving, sim.RobotCharging, sim.RobotIdle, sim.RobotFaulted} {
			for _, job := range []string{"", "J1"} {
				state := sim.State{Robots: []sim.RobotState{{ID: "R1", Battery: tc.battery, Status: status, JobID: job}}}
				got, counter := reconcile(t, nil, state, 0)
				if tc.want == "" || job == "" {
					if len(got) != 0 || counter != 0 {
						t.Fatalf("job=%q status=%s battery=%d: %+v", job, status, tc.battery, got)
					}
				} else if len(got) != 1 || got[0].Severity != tc.want || got[0].Type != incident.LowBattery || counter != 1 {
					t.Fatalf("job=%q status=%s battery=%d: %+v", job, status, tc.battery, got)
				}
			}
		}
	}
}

func TestWaitSeverityRequiresBlockedFlag(t *testing.T) {
	for _, wait := range []int{0, 10, 39, 40, 41, 108} {
		for _, blocked := range []bool{false, true} {
			got, counter := reconcile(t, nil, sim.State{Robots: []sim.RobotState{{ID: "R1", Battery: 100, Blocked: blocked, WaitTicks: wait}}}, 0)
			if !blocked {
				if len(got) != 0 || counter != 0 {
					t.Fatal("waiting without Blocked opened an incident")
				}
				continue
			}
			severity := incident.Medium
			if wait > 40 {
				severity = incident.High
			}
			if len(got) != 1 || got[0].Type != incident.TrafficBlocked || got[0].Severity != severity {
				t.Fatalf("blocked=%v wait=%d: %+v", blocked, wait, got)
			}
		}
	}
}

func TestSeverityChangesKeepEpisodeIdentity(t *testing.T) {
	for _, kind := range []incident.Type{incident.LowBattery, incident.TrafficBlocked} {
		t.Run(string(kind), func(t *testing.T) {
			robot := sim.RobotState{ID: "R1", Battery: 29, JobID: "J1"}
			if kind == incident.TrafficBlocked {
				robot.Battery, robot.JobID, robot.Blocked, robot.WaitTicks = 100, "", true, 40
			}
			state := sim.State{Robots: []sim.RobotState{robot}}
			got, counter := reconcile(t, nil, state, 0)
			state.Robots[0].Battery, state.Robots[0].WaitTicks = 14, 41
			high, next := reconcile(t, got, state, counter)
			if len(high) != 1 || high[0].ID != got[0].ID || high[0].Severity != incident.High || next != counter {
				t.Fatal("escalation changed identity")
			}
			state.Robots[0] = robot
			medium, next := reconcile(t, high, state, counter)
			if !reflect.DeepEqual(medium, got) || next != counter {
				t.Fatal("de-escalation changed identity")
			}
		})
	}
}

func TestNumericOrderingAndIndependentConditions(t *testing.T) {
	state := sim.State{Robots: []sim.RobotState{
		{ID: "R10", Status: sim.RobotOffline, Battery: 14, JobID: "J10", Blocked: true, WaitTicks: 41},
		{ID: "R2", Status: sim.RobotOffline, Battery: 29, JobID: "J2", Blocked: true, WaitTicks: 40},
		{ID: "R1", Status: sim.RobotOffline, Battery: 100},
	}}
	got, counter := reconcile(t, nil, state, 0)
	want := []incident.Incident{
		{ID: "I1", RobotID: "R1", Type: incident.RobotUnresponsive, Status: incident.Open, Severity: incident.High},
		{ID: "I2", RobotID: "R2", Type: incident.LowBattery, Status: incident.Open, Severity: incident.Medium},
		{ID: "I3", RobotID: "R2", Type: incident.RobotUnresponsive, Status: incident.Open, Severity: incident.High},
		{ID: "I4", RobotID: "R2", Type: incident.TrafficBlocked, Status: incident.Open, Severity: incident.Medium},
		{ID: "I5", RobotID: "R10", Type: incident.LowBattery, Status: incident.Open, Severity: incident.High},
		{ID: "I6", RobotID: "R10", Type: incident.RobotUnresponsive, Status: incident.Open, Severity: incident.High},
		{ID: "I7", RobotID: "R10", Type: incident.TrafficBlocked, Status: incident.Open, Severity: incident.High},
	}
	if !reflect.DeepEqual(got, want) || counter != 7 {
		t.Fatalf("ordered incidents = %+v, counter = %d", got, counter)
	}
	for range 20 {
		slices.Reverse(state.Robots)
		again, next := reconcile(t, nil, state, 0)
		if !reflect.DeepEqual(again, want) || next != counter {
			t.Fatal("input robot order changed IDs or output")
		}
	}
}

func TestFaultHistoryDoesNotInventActiveConditions(t *testing.T) {
	aisle := 0
	state := sim.State{Robots: []sim.RobotState{{ID: "R1", Battery: 100, Status: sim.RobotMoving}}, Faults: []sim.FaultState{
		{ID: "F1", Type: sim.FaultRobotUnresponsive, Robots: []string{"R1"}},
		{ID: "F2", Type: sim.FaultLowBattery, Robots: []string{"R1"}},
		{ID: "F3", Type: sim.FaultAisleConflict, Robots: []string{"R1", "R2"}, Aisle: &aisle},
	}}
	got, counter := reconcile(t, nil, state, 0)
	if len(got) != 0 || counter != 0 {
		t.Fatal("historical fault created an active incident")
	}
	state.Robots[0].Status = sim.RobotOffline
	got, counter = reconcile(t, nil, state, 0)
	if len(got) != 1 || got[0].Type != incident.RobotUnresponsive {
		t.Fatal("offline status did not create unresponsive incident")
	}
	state.Robots[0].Status = sim.RobotMoving
	got, _ = reconcile(t, got, state, counter)
	if got[0].Status != incident.Resolved {
		t.Fatal("historical fault kept a cleared condition open")
	}
}

func TestResolvedHistoryAndAbsentRobots(t *testing.T) {
	previous := []incident.Incident{
		{ID: "I10", RobotID: "R2", Type: incident.LowBattery, Status: incident.Open, Severity: incident.High},
		{ID: "I9", RobotID: "R2", Type: incident.LowBattery, Status: incident.Resolved, Severity: incident.Medium},
		{ID: "I2", RobotID: "R1", Type: incident.TrafficBlocked, Status: incident.Resolved, Severity: incident.High},
	}
	got, counter := reconcile(t, previous, sim.State{}, 10)
	if counter != 10 || len(got) != 3 || got[0].ID != "I2" || got[1].ID != "I9" || got[2].ID != "I10" || got[2].Status != incident.Resolved || got[2].Severity != incident.High {
		t.Fatalf("history = %+v", got)
	}
}

func TestInvalidInputsAndCounterExhaustion(t *testing.T) {
	valid := incident.Incident{ID: "I1", RobotID: "R1", Type: incident.LowBattery, Status: incident.Open, Severity: incident.Medium}
	for _, tc := range []struct {
		name     string
		previous []incident.Incident
		state    sim.State
		counter  uint64
		want     error
	}{
		{name: "duplicate robots", state: sim.State{Robots: []sim.RobotState{{ID: "R1"}, {ID: "R1"}}}, want: incident.ErrInvalidSnapshot},
		{name: "non-numeric robot", state: sim.State{Robots: []sim.RobotState{{ID: "robot"}}}, want: incident.ErrInvalidSnapshot},
		{name: "noncanonical robot", state: sim.State{Robots: []sim.RobotState{{ID: "R01"}}}, want: incident.ErrInvalidSnapshot},
		{name: "duplicate incident IDs", previous: []incident.Incident{valid, valid}, counter: 1, want: incident.ErrInvalidHistory},
		{name: "duplicate open keys", previous: []incident.Incident{valid, {ID: "I2", RobotID: "R1", Type: incident.LowBattery, Status: incident.Open, Severity: incident.High}}, counter: 2, want: incident.ErrInvalidHistory},
		{name: "unknown history type", previous: []incident.Incident{{ID: "I1", RobotID: "R1", Type: "aisle_conflict", Status: incident.Open, Severity: incident.High}}, counter: 1, want: incident.ErrInvalidHistory},
		{name: "counter behind history", previous: []incident.Incident{valid}, want: incident.ErrInvalidCounter},
		{name: "overflow", state: sim.State{Robots: []sim.RobotState{{ID: "R1", Status: sim.RobotOffline}}}, counter: ^uint64(0), want: incident.ErrCounterExhausted},
		{name: "partial overflow", state: sim.State{Robots: []sim.RobotState{{ID: "R1", Status: sim.RobotOffline}, {ID: "R2", Status: sim.RobotOffline}}}, counter: ^uint64(0) - 1, want: incident.ErrCounterExhausted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, next, err := incident.Reconcile(tc.previous, tc.state, tc.counter)
			if !errors.Is(err, tc.want) || got != nil || next != tc.counter {
				t.Fatalf("result = %+v, counter = %d, error = %v", got, next, err)
			}
		})
	}
}

func TestNoInputMutationAndDetachedOutput(t *testing.T) {
	previous := []incident.Incident{{ID: "I1", RobotID: "R10", Type: incident.LowBattery, Status: incident.Open, Severity: incident.Medium}}
	state := sim.State{Robots: []sim.RobotState{{ID: "R10", Battery: 14, JobID: "J1"}, {ID: "R2", Status: sim.RobotOffline}}}
	beforeHistory, beforeRobots := slices.Clone(previous), slices.Clone(state.Robots)
	got, _ := reconcile(t, previous, state, 1)
	if !reflect.DeepEqual(previous, beforeHistory) || !reflect.DeepEqual(state.Robots, beforeRobots) {
		t.Fatal("reconciliation mutated inputs")
	}
	got[0].RobotID, got[1].Severity = "changed", incident.High
	if !reflect.DeepEqual(previous, beforeHistory) || !reflect.DeepEqual(state.Robots, beforeRobots) {
		t.Fatal("result aliases inputs")
	}
}

func TestDeterministicReconciliationAcrossMapOrders(t *testing.T) {
	robots := map[string]sim.RobotState{
		"R10": {ID: "R10", Status: sim.RobotOffline},
		"R2":  {ID: "R2", Battery: 14, JobID: "J2", Blocked: true, WaitTicks: 41},
		"R1":  {ID: "R1", Battery: 100},
	}
	history := map[string]incident.Incident{
		"I10": {ID: "I10", RobotID: "R10", Type: incident.RobotUnresponsive, Status: incident.Open, Severity: incident.High},
		"I2":  {ID: "I2", RobotID: "R2", Type: incident.LowBattery, Status: incident.Resolved, Severity: incident.Medium},
		"I1":  {ID: "I1", RobotID: "R1", Type: incident.TrafficBlocked, Status: incident.Open, Severity: incident.High},
	}
	var want []incident.Incident
	var wantCounter uint64
	for iteration := 0; iteration < 100; iteration++ {
		var previous []incident.Incident
		var state sim.State
		for _, record := range history {
			previous = append(previous, record)
		}
		for _, robot := range robots {
			state.Robots = append(state.Robots, robot)
		}
		got, next := reconcile(t, previous, state, 10)
		if iteration == 0 {
			want, wantCounter = got, next
		}
		if !reflect.DeepEqual(got, want) || next != wantCounter {
			t.Fatal("map input order changed reconciliation")
		}
		repeated, repeatedCounter := reconcile(t, got, state, next)
		if !reflect.DeepEqual(repeated, got) || repeatedCounter != next {
			t.Fatal("repeated state created duplicates")
		}
	}
}

func TestSimulatorSnapshotAndReplayUnchanged(t *testing.T) {
	cfg := sim.DefaultConfig()
	subject, err := sim.New(cfg, 42)
	if err != nil {
		t.Fatal(err)
	}
	control, err := sim.New(cfg, 42)
	if err != nil {
		t.Fatal(err)
	}
	var previous []incident.Incident
	var counter uint64
	for tick := 0; tick < 100; tick++ {
		if tick == 20 {
			if err := subject.InjectUnresponsive("R1"); err != nil {
				t.Fatal(err)
			}
			if err := control.InjectUnresponsive("R1"); err != nil {
				t.Fatal(err)
			}
		}
		before := subject.Snapshot()
		previous, counter = reconcile(t, previous, before, counter)
		if !reflect.DeepEqual(before, subject.Snapshot()) {
			t.Fatalf("snapshot mutated at tick %d", tick)
		}
		subject.Tick()
		control.Tick()
		if !reflect.DeepEqual(subject.Snapshot(), control.Snapshot()) {
			t.Fatalf("simulator replay diverged at tick %d", tick+1)
		}
	}
	if len(previous) == 0 {
		t.Fatal("replay fixture did not exercise an incident")
	}
}
