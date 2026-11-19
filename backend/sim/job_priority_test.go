package sim

import "testing"

func TestWarehouseV2PriorityJobLifecycleIntegration(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.AutoDispatch, cfg.NewJobEvery = false, 5
	s := newTestSim(t, cfg, 42)
	for range cfg.NewJobEvery {
		s.Tick()
	}
	state := s.Snapshot()
	normal, priority, recent := state.Jobs[0], state.Jobs[3], state.Jobs[8]
	route, err := s.PlanRoute(priority.From, priority.To)
	if err != nil || normal.Type != JobNormal || normal.DeadlineTick != 0 ||
		priority.Type != JobPriority || priority.CreatedTick != 0 ||
		priority.DeadlineTick != len(route)+priorityDeadlineSlack ||
		priority.QueueAgeTicks != state.Tick || recent.ID != "J9" || recent.CreatedTick != state.Tick || recent.QueueAgeTicks != 0 {
		t.Fatalf("unexpected priority generation or queue age: normal=%+v priority=%+v recent=%+v route=%v", normal, priority, recent, err)
	}
	for i := range s.state.Jobs {
		job := &s.state.Jobs[i]
		if job.Type == JobPriority {
			job.DeadlineTick = 20
		}
	}
	for s.state.Tick < 20 {
		s.Tick()
	}
	onTime := &s.state.Jobs[3]
	completeV2Job(t, s, onTime)
	if onTime.Outcome != JobOnTime || onTime.CompletedTick != 20 || s.state.PriorityDeadlinesMet != 1 || s.state.PriorityDeadlinesMissed != 0 {
		t.Fatalf("delivery at the deadline must count as on time: %+v", *onTime)
	}
	s.Tick()
	late := &s.state.Jobs[7]
	if late.Status != JobPending || !late.DeadlineMissed || s.state.PriorityDeadlinesMissed != 1 {
		t.Fatalf("overdue work must remain queued and record one miss: %+v", *late)
	}
	s.Tick()
	s.Tick()
	completeV2Job(t, s, late)
	if late.Outcome != JobLate || !late.DeadlineMissed || late.CompletedTick != 23 || s.state.PriorityDeadlinesMissed != 1 {
		t.Fatalf("late delivery must preserve the original miss: %+v", *late)
	}
}

func TestWarehouseV2JobAgeAndCustodyIntegration(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs, cfg.NewJobEvery = 0, 10000
	s := newTestSim(t, cfg, 1)
	s.state.Tick = priorityHeadStartTicks + 4
	s.state.Jobs = []JobState{
		{ID: "J1", Type: JobNormal, CreatedTick: 1, From: Point{0, 11}, To: Point{1, 11}, Status: JobPending},
		{ID: "J2", Type: JobPriority, CreatedTick: s.state.Tick, From: Point{0, 11}, To: Point{1, 11}, Status: JobPending, DeadlineTick: s.state.Tick + 30},
	}
	for i := 1; i < len(s.state.Robots); i++ {
		s.state.Robots[i].Status = RobotOffline
	}
	s.Tick()
	job := s.Snapshot().Jobs[0]
	if job.Status != JobAssigned || job.AssignedRobot != "R1" || job.AssignedTick != priorityHeadStartTicks+5 || job.QueueAgeTicks != priorityHeadStartTicks+4 || job.CargoRobotID != "" {
		t.Fatalf("oldest feasible job must get the available robot and record its queue wait: %+v", job)
	}
	s.Tick()
	job = s.Snapshot().Jobs[0]
	if job.Status != JobInProgress || job.CargoRobotID != "R1" || job.QueueAgeTicks != priorityHeadStartTicks+4 {
		t.Fatalf("pickup must establish custody and freeze queue age: %+v", job)
	}
	s.Tick()
	job = s.Snapshot().Jobs[0]
	if job.Status != JobDone || job.CargoRobotID != "" || job.QueueAgeTicks != priorityHeadStartTicks+4 || job.Outcome != JobDelivered || job.CompletedTick != priorityHeadStartTicks+7 {
		t.Fatalf("delivery must clear custody and record the outcome: %+v", job)
	}
}

func TestWarehouseV2PriorityDispatchRespectsEligibilityIntegration(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs, cfg.NewJobEvery = 0, 10000
	s := newTestSim(t, cfg, 1)
	s.state.Jobs = []JobState{
		{ID: "J1", Type: JobNormal, From: Point{0, 11}, To: Point{1, 11}, Status: JobPending},
		{ID: "J2", Type: JobPriority, From: Point{0, 11}, To: Point{2, 11}, Status: JobPending, DeadlineTick: 35},
		{ID: "J3", Type: JobPriority, From: Point{19, 11}, To: Point{18, 11}, Status: JobPending, DeadlineTick: 25},
		{ID: "J4", Type: JobNormal, From: Point{15, 11}, To: Point{19, 10}, Status: JobInProgress, AssignedRobot: "R3"},
		{ID: "J5", Type: JobPriority, From: Point{0, 11}, To: Point{1, 11}, Status: JobPending, DeadlineTick: 1},
		{ID: "J6", Type: JobPriority, From: Point{0, 11}, To: Point{3, 11}, Status: JobPending, DeadlineTick: 3},
	}
	s.state.Tick = 1
	for i := 1; i < warehouseTransportCount; i++ {
		s.state.Robots[i].Status = RobotOffline
	}
	r2, r3, r4 := &s.state.Robots[1], &s.state.Robots[2], &s.state.Robots[3]
	r2.Status, r2.Battery = RobotIdle, cfg.ChargeBelow
	r3.Status, r3.JobID, r3.X, r3.Y = RobotMoving, "J4", 15, 11
	r4.X, r4.Y = 19, 11
	s.Tick()
	state := s.Snapshot()
	if state.Jobs[1].Status != JobAssigned || state.Jobs[1].AssignedRobot != "R1" ||
		state.Jobs[0].Status != JobPending || state.Jobs[2].Status != JobPending ||
		state.Jobs[4].Status != JobPending || state.Jobs[5].Status != JobPending {
		t.Fatalf("urgency must favor eligible jobs whose delivery can still meet its deadline: %+v", state.Jobs)
	}
	if state.Jobs[3].CargoRobotID != "R3" || r2.JobID != "" || state.Robots[10].JobID != "" {
		t.Fatalf("priority used a charging, committed, or maintenance robot: %+v", state.Robots)
	}
}
