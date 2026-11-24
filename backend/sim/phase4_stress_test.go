package sim

import (
	"encoding/json"
	"flag"
	"fmt"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/scheduler"
)

var runPhase4Stress = flag.Bool("phase4-stress", false, "run full-fleet Phase 4 endurance workloads")
var phase4StressTicks = flag.Int("phase4-stress-ticks", 25000, "Phase 4 endurance horizon (5000, 10000, or 25000)")
var phase4StressTarget = flag.Int("phase4-stress-target", 85, "diagnostic charge target for charging-parking and the seed-2 opposing deliveries test (85 or 100)")

type phase4Workload struct {
	name        string
	configure   func(*Config)
	prepareTick func(*Sim)
	adjustJobs  func(*Sim, int)
}

func phase4StressWorkloads() []phase4Workload {
	return []phase4Workload{
		{name: "normal"},
		{name: "high-arrivals", configure: func(c *Config) { c.NewJobEvery = 2 }},
		{name: "queue-saturation", configure: func(c *Config) {
			c.InitialJobs, c.MaxJobs, c.NewJobEvery = 200, 200, 1
		}},
		{name: "priority-tight-deadlines", configure: func(c *Config) { c.NewJobEvery = 2 }, adjustJobs: phase4TightDeadlines},
		{name: "charging-parking", configure: func(c *Config) {
			c.MovingDrain, c.ChargeGain, c.ParkingGraceTicks, c.NewJobEvery = 2, 1, 0, 3
			c.ScheduledBreakdowns = []ScheduledBreakdown{{200, "R3", FaultCriticalBattery}, {5000, "R8", FaultCriticalBattery}}
		}},
		{name: "simultaneous-faults", configure: func(c *Config) {
			c.NewJobEvery = 3
			c.ScheduledBreakdowns = []ScheduledBreakdown{
				{48, "R1", FaultMotorFailure}, {48, "R4", FaultSensorFailure}, {200, "R5", FaultRobotUnresponsive},
				{600, "R8", FaultSensorFailure}, {1300, "R9", FaultCriticalBattery},
				{5200, "R2", FaultMotorFailure}, {5200, "R6", FaultSensorFailure},
			}
		}},
		{name: "idle-then-burst", configure: func(c *Config) {
			c.InitialJobs, c.MaxJobs, c.NewJobEvery = 0, 80, 100_000
		}, prepareTick: func(s *Sim) {
			s.cfg.NewJobEvery = 100_000
			if (s.state.Tick+1)%5000 >= 2500 && (s.state.Tick+1)%5000 < 2700 {
				s.cfg.NewJobEvery = 1
			}
		}},
	}
}

func phase4TightDeadlines(s *Sim, previousSequence int) {
	for i := len(s.state.Jobs) - 1; i >= 0; i-- {
		job := &s.state.Jobs[i]
		if idNumber(job.ID) <= previousSequence {
			return
		}
		job.Type = JobPriority
		job.DeadlineTick = job.CreatedTick + 8
	}
}

type phase4StressMetrics struct {
	JobsCreated            int                        `json:"jobsCreated"`
	JobsCompleted          int                        `json:"jobsCompleted"`
	DeliveriesPer1000Ticks float64                    `json:"deliveriesPer1000Ticks"`
	PriorityOnTime         int                        `json:"priorityOnTime"`
	PriorityMissed         int                        `json:"priorityMissed"`
	MaxActiveJobs          int                        `json:"maxActiveJobs"`
	MaxPendingJobs         int                        `json:"maxPendingJobs"`
	PendingAtCheckpoint    int                        `json:"pendingAtCheckpoint"`
	DeferredAtCheckpoint   int                        `json:"deferredAtCheckpoint"`
	SaturatedTicks         int                        `json:"saturatedTicks"`
	MaxRobotWait           int                        `json:"maxRobotWait"`
	TrafficWaitRobotTicks  int                        `json:"trafficWaitRobotTicks"`
	ChargerUseTicks        map[string]int             `json:"chargerUseTicks"`
	ChargeWaitRobotTicks   int                        `json:"chargeWaitRobotTicks"`
	ParkingRobotTicks      int                        `json:"parkingRobotTicks"`
	MaintenanceAttempts    int                        `json:"maintenanceAttempts"`
	MaintenanceOutcomes    map[MaintenanceOutcome]int `json:"maintenanceOutcomes"`
	MinimumBattery         int                        `json:"minimumBattery"`
	DeadlockTicks          int                        `json:"deadlockTicks"`
	LongestDeliveryGap     int                        `json:"longestBackloggedDeliveryGap"`
	ActiveFaults           int                        `json:"activeFaults"`
	RetainedFaults         int                        `json:"retainedFaults"`
	PeakSampledHeapBytes   uint64                     `json:"peakSampledHeapBytes"`
	HeapBytes              uint64                     `json:"heapBytes"`
	TickP50Micros          float64                    `json:"tickP50Micros"`
	TickP95Micros          float64                    `json:"tickP95Micros"`
	TickMaxMicros          float64                    `json:"tickMaxMicros"`
	WallMilliseconds       int64                      `json:"wallMilliseconds"`
	lastDeliveryTick       int
	lastCompleted          int
	maintenanceSeen        int
}

func TestPhase4FleetStress(t *testing.T) {
	if !*runPhase4Stress {
		t.Skip("requires -phase4-stress")
	}
	if !slices.Contains([]int{5000, 10000, 25000}, *phase4StressTicks) {
		t.Fatal("choose a predeclared Phase 4 endurance horizon")
	}
	if *phase4StressTarget != 85 && *phase4StressTarget != 100 {
		t.Fatal("choose 85 or 100 for the charging-parking diagnostic")
	}
	for _, workload := range phase4StressWorkloads() {
		for _, seed := range []int64{2, 42} {
			t.Run(fmt.Sprintf("%s/seed%d", workload.name, seed), func(t *testing.T) {
				runPhase4Workload(t, workload, seed)
			})
		}
	}
}

func TestPhase4Seed2LongRunCycleEvidence(t *testing.T) {
	if !*runPhase4Stress {
		t.Skip("requires -phase4-stress")
	}
	s := newTestSim(t, WarehouseV2Config(), 2)
	for tick := 1; tick <= 4000; tick++ {
		s.Tick()
		if tick != 3499 && tick != 3501 && tick != 3525 && tick != 4000 {
			continue
		}
		frame := diagSnapshot(s)
		t.Logf("PHASE4_CYCLE tick=%d cycles=%s robots=%s stations=%s routes=%s dependencies=%s", tick,
			diagJSON(frame.State.TrafficDeadlocks), diagJSON(frame.State.Robots), diagJSON(frame.State.Stations),
			diagJSON(frame.Routes), diagJSON(diagDependencies(s)))
	}
}

func TestPhase4Seed2LowBatteryChargeTripYieldsToDelivery(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.AutoDispatch = false
	cfg.InitialJobs = 0
	s := newTestSim(t, cfg, 2)
	r2, r9 := s.faultRobot("R2"), s.faultRobot("R9")
	r2.X, r2.Y, r2.Battery = 2, 10, 13
	r9.X, r9.Y, r9.Battery, r9.Status, r9.JobID = 0, 7, 17, RobotMoving, "J258"
	dropoff := Point{1, 11}
	s.state.Jobs = []JobState{{ID: "J258", From: Point{1, 6}, To: dropoff, Status: JobInProgress, AssignedRobot: "R9"}}
	s.navigation[r9.ID] = robotNavigation{target: dropoff, route: []Point{{0, 8}, {0, 9}, {1, 9}, {1, 10}, dropoff}}
	station := &s.state.Stations[0]
	route, ok := s.chargingRoute(*r2, *station)
	if !ok {
		t.Fatal("fixture must admit the direct charging route")
	}
	s.beginChargeTrip(r2, station, route)
	s.Tick()
	if got := s.faultRobot("R2"); got.X != 2 || got.Y != 10 || got.Battery != 13 {
		t.Fatalf("R2 entered the opposing route before J258 cleared it: %+v", got)
	}
	for range 24 {
		s.Tick()
	}
	r2 = s.faultRobot("R2")
	job := jobByID(t, s.Snapshot(), "J258")
	if job.Status != JobDone || job.AssignedRobot != "R9" || r2.Status != RobotCharging ||
		r2.X != station.X || r2.Y != station.Y || r2.Battery <= 0 {
		t.Fatalf("opposing missions failed to progress: job=%+v R2=%+v", job, r2)
	}
}

func runPhase4Workload(t *testing.T, workload phase4Workload, seed int64) {
	t.Helper()
	cfg := WarehouseV2Config()
	if workload.configure != nil {
		workload.configure(&cfg)
	}
	if workload.name == "charging-parking" {
		cfg.ChargeRelease = *phase4StressTarget
	}
	s := newTestSim(t, cfg, seed)
	if len(s.state.Robots) != 11 || len(s.state.Stations) != 4 {
		t.Fatal("stress workload requires the full V2 fleet")
	}
	if workload.adjustJobs != nil {
		workload.adjustJobs(s, 0)
	}
	metrics := phase4StressMetrics{MinimumBattery: 100, ChargerUseTicks: make(map[string]int), MaintenanceOutcomes: make(map[MaintenanceOutcome]int)}
	horizon := *phase4StressTicks
	timings := make([]time.Duration, 0, horizon)
	previous := s.Snapshot()
	started := time.Now()
	for tick := 1; tick <= horizon; tick++ {
		if workload.prepareTick != nil {
			workload.prepareTick(s)
		}
		previousSequence := s.jobSequence
		tickStart := time.Now()
		s.Tick()
		timings = append(timings, time.Since(tickStart))
		if workload.adjustJobs != nil {
			workload.adjustJobs(s, previousSequence)
		}
		state := s.Snapshot()
		if err := checkInvariants(state, s.cfg, s.navigation); err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
		if err := checkRecoveryInvariants(s); err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
		checkMaintenanceSoakMovement(t, previous, state, s.cfg)
		checkPhase4StressTransitions(t, s, previous, state)
		metrics.observe(state, s.jobSequence, s.cfg.MaxJobs)
		if workload.name == "charging-parking" && (tick == 5000 || tick == horizon) {
			logChargingQueue(t, s, cfg, tick)
		}
		if tick%100 == 0 {
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			metrics.HeapBytes = memory.HeapAlloc
			metrics.PeakSampledHeapBytes = max(metrics.PeakSampledHeapBytes, memory.HeapAlloc)
		}
		if tick == 5000 || tick == 10000 || tick == horizon {
			metrics.checkpoint(t, workload.name, seed, tick, timings, time.Since(started))
		}
		previous = state
	}
	t.Logf("PHASE4_FINAL workload=%s seed=%d cycles=%s robots=%s stations=%s", workload.name, seed,
		diagJSON(previous.TrafficDeadlocks), diagJSON(previous.Robots), diagJSON(previous.Stations))
}

func checkPhase4StressTransitions(t *testing.T, s *Sim, previous, state State) {
	t.Helper()
	for i, robot := range state.Robots {
		before := previous.Robots[i]
		if robot.X == before.X && robot.Y == before.Y {
			continue
		}
		for j := i + 1; j < len(state.Robots); j++ {
			other, otherBefore := state.Robots[j], previous.Robots[j]
			if robot.X == otherBefore.X && robot.Y == otherBefore.Y && other.X == before.X && other.Y == before.Y {
				t.Fatalf("tick %d edge swap between %s and %s", state.Tick, robot.ID, other.ID)
			}
		}
	}
	if previous.Maintenance == nil || state.Maintenance == nil {
		return
	}
	oldWork, work := previous.Maintenance, state.Maintenance
	if len(work.History) < len(oldWork.History) || !slices.Equal(oldWork.History, work.History[:len(oldWork.History)]) {
		t.Fatalf("tick %d rewrote maintenance history", state.Tick)
	}
	if oldWork.Active != nil && work.Active != nil {
		checkMaintenanceSoakRepair(t, previous, state, *oldWork.Active, *work.Active, s)
	}
}

func (m *phase4StressMetrics) observe(state State, created, jobCap int) {
	m.JobsCreated, m.JobsCompleted = created, state.CompletedJobs
	m.PriorityOnTime, m.PriorityMissed = state.PriorityDeadlinesMet, state.PriorityDeadlinesMissed
	m.RetainedFaults = len(state.Faults)
	active, _ := countJobs(state.Jobs)
	m.MaxActiveJobs = max(m.MaxActiveJobs, active)
	if active == jobCap {
		m.SaturatedTicks++
	}
	m.PendingAtCheckpoint, m.DeferredAtCheckpoint = 0, 0
	for _, job := range state.Jobs {
		if job.Status == JobPending {
			m.PendingAtCheckpoint++
			if job.Deferred {
				m.DeferredAtCheckpoint++
			}
		}
	}
	m.MaxPendingJobs = max(m.MaxPendingJobs, m.PendingAtCheckpoint)
	if active == 0 && !slices.ContainsFunc(state.Jobs, func(job JobState) bool { return job.Deferred }) || m.lastCompleted != state.CompletedJobs {
		m.lastDeliveryTick = state.Tick
	}
	m.lastCompleted = state.CompletedJobs
	m.LongestDeliveryGap = max(m.LongestDeliveryGap, state.Tick-m.lastDeliveryTick)
	if len(state.TrafficDeadlocks) > 0 {
		m.DeadlockTicks++
	}
	m.ActiveFaults = 0
	for _, robot := range state.Robots {
		m.MinimumBattery = min(m.MinimumBattery, robot.Battery)
		m.MaxRobotWait = max(m.MaxRobotWait, robot.WaitTicks)
		if robot.Activity == ActivityGoingToPark || robot.Activity == ActivityParked {
			m.ParkingRobotTicks++
		}
		if robot.WaitTicks > 0 {
			m.TrafficWaitRobotTicks++
			if robot.Activity == ActivityGoingToCharge {
				m.ChargeWaitRobotTicks++
			}
		}
		if robot.ActiveFault != "" {
			m.ActiveFaults++
		}
	}
	for _, station := range state.Stations {
		if station.OccupiedBy != "" {
			m.ChargerUseTicks[station.ID]++
		}
	}
	if work := state.Maintenance; work != nil {
		for _, attempt := range work.History[m.maintenanceSeen:] {
			m.MaintenanceOutcomes[attempt.Outcome]++
		}
		m.maintenanceSeen = len(work.History)
		m.MaintenanceAttempts = m.maintenanceSeen
		if work.Active != nil {
			m.MaintenanceAttempts++
		}
	}
}

func (m phase4StressMetrics) checkpoint(t *testing.T, name string, seed int64, tick int, timings []time.Duration, elapsed time.Duration) {
	t.Helper()
	sorted := slices.Clone(timings)
	slices.Sort(sorted)
	m.DeliveriesPer1000Ticks = float64(m.JobsCompleted) * 1000 / float64(tick)
	m.TickP50Micros = float64(sorted[(len(sorted)-1)*50/100]) / float64(time.Microsecond)
	m.TickP95Micros = float64(sorted[(len(sorted)-1)*95/100]) / float64(time.Microsecond)
	m.TickMaxMicros = float64(sorted[len(sorted)-1]) / float64(time.Microsecond)
	m.WallMilliseconds = elapsed.Milliseconds()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PHASE4_STRESS workload=%s seed=%d ticks=%d %s", name, seed, tick, data)
}

func logChargingQueue(t *testing.T, s *Sim, cfg Config, tick int) {
	t.Helper()
	projected := *s
	projected.state = s.Snapshot()
	projected.state.Robots = slices.DeleteFunc(projected.state.Robots, func(robot RobotState) bool {
		return robot.Status != RobotFaulted && robot.Status != RobotOffline
	})
	distance := projected.travelDistance(false)
	feasible, unreachable, energyLimited, pending := 0, 0, 0, 0
	best, within85, within100 := -1, 0, 0
	for _, job := range s.state.Jobs {
		if job.Status != JobPending {
			continue
		}
		pending++
		from := scheduler.Cell{X: job.From.X, Y: job.From.Y}
		to := scheduler.Cell{X: job.To.X, Y: job.To.Y}
		delivery, connected := distance(from, to)
		pickup, exit := -1, -1
		for _, dock := range cfg.Stations {
			cell := scheduler.Cell{X: dock.X, Y: dock.Y}
			if steps, ok := distance(cell, from); ok && (pickup < 0 || steps < pickup) {
				pickup = steps
			}
			if steps, ok := distance(to, cell); ok && (exit < 0 || steps < exit) {
				exit = steps
			}
		}
		if !connected || pickup < 0 || exit < 0 {
			unreachable++
			continue
		}
		required := (pickup+delivery+exit)*cfg.MovingDrain + cfg.ReserveBattery
		if best < 0 || required < best {
			best = required
		}
		if required <= 85 {
			within85++
		}
		if required <= 100 {
			within100++
		}
		t.Logf("required tick=%d job=%s units=%d", tick, job.ID, required)
		if required > cfg.ChargeRelease {
			energyLimited++
		} else {
			feasible++
		}
	}
	t.Logf("tick=%d stations=%+v robots=%+v", tick, s.state.Stations, s.state.Robots)
	actual := s.travelDistance(true)
	for _, job := range s.state.Jobs {
		if job.Status != JobPending {
			continue
		}
		from := scheduler.Cell{X: job.From.X, Y: job.From.Y}
		to := scheduler.Cell{X: job.To.X, Y: job.To.Y}
		delivery, reached := distance(from, to)
		if !reached {
			continue
		}
		var dockOptions []string
		for _, dock := range s.state.Stations {
			point := scheduler.Cell{X: dock.X, Y: dock.Y}
			pickup, ok := distance(point, from)
			if !ok {
				continue
			}
			for _, exit := range s.state.Stations {
				leg, ok := distance(to, scheduler.Cell{X: exit.X, Y: exit.Y})
				if !ok {
					continue
				}
				energy := (pickup+delivery+leg)*cfg.MovingDrain + cfg.ReserveBattery
				if energy <= cfg.ChargeRelease {
					dockOptions = append(dockOptions, fmt.Sprintf("%s→%s=%d", dock.ID, exit.ID, energy))
				}
			}
		}
		if len(dockOptions) == 0 {
			continue
		}
		exit := s.chargingExitDistance(job.To, "", actual)
		mission, travel := actual(from, to)
		var eligible []string
		for _, robot := range s.state.Robots {
			if !s.canAcceptJob(robot) {
				continue
			}
			pickup, ok := actual(scheduler.Cell{X: robot.X, Y: robot.Y}, from)
			if ok && travel && exit >= 0 && robot.Battery >= (pickup+mission+exit)*cfg.MovingDrain+cfg.ReserveBattery {
				eligible = append(eligible, robot.ID)
			}
		}
		t.Logf("tick=%d job=%s from=%v to=%v feasibleDockPairs=%v liveExit=%d liveRoute=%t parkedCandidates=%v", tick, job.ID, job.From, job.To, dockOptions, exit, travel, eligible)
	}

	t.Logf("tick=%d deliveries=%d pending=%d feasibleFromSomeDock=%d unreachable=%d overCharge=%d within85=%d within100=%d bestReachableEnergy=%d chargeRelease=%d faulted=%v",
		tick, s.state.CompletedJobs, pending, feasible, unreachable, energyLimited, within85, within100, best, cfg.ChargeRelease, projected.state.Robots)
}
