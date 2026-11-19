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
	s.state.Tick = 4
	s.state.Jobs = []JobState{
		{ID: "J1", Type: JobNormal, CreatedTick: 1, From: Point{0, 11}, To: Point{1, 11}, Status: JobPending},
		{ID: "J2", Type: JobPriority, CreatedTick: 4, From: Point{0, 11}, To: Point{1, 11}, Status: JobPending, DeadlineTick: 50},
	}
	for i := 1; i < len(s.state.Robots); i++ {
		s.state.Robots[i].Status = RobotOffline
	}
	s.Tick()
	job := s.Snapshot().Jobs[0]
	if job.Status != JobAssigned || job.AssignedRobot != "R1" || job.AssignedTick != 5 || job.QueueAgeTicks != 4 || job.CargoRobotID != "" {
		t.Fatalf("oldest feasible job must get the available robot and record its queue wait: %+v", job)
	}
	s.Tick()
	job = s.Snapshot().Jobs[0]
	if job.Status != JobInProgress || job.CargoRobotID != "R1" || job.QueueAgeTicks != 4 {
		t.Fatalf("pickup must establish custody and freeze queue age: %+v", job)
	}
	s.Tick()
	job = s.Snapshot().Jobs[0]
	if job.Status != JobDone || job.CargoRobotID != "" || job.QueueAgeTicks != 4 || job.Outcome != JobDelivered || job.CompletedTick != 7 {
		t.Fatalf("delivery must clear custody and record the outcome: %+v", job)
	}
}
