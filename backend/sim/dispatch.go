package sim

import (
	"github.com/AyushM7/fleetops/backend/routing"
	"github.com/AyushM7/fleetops/backend/scheduler"
)

const priorityHeadStartTicks = 20

func (s *Sim) assignPending(skip []bool) {
	if !s.cfg.AutoDispatch {
		return
	}
	var idle []scheduler.RobotInfo
	robots := make(map[int]int)
	for i, robot := range s.state.Robots {
		if skip[i] || !s.canAcceptJob(robot) {
			continue
		}
		number := idNumber(robot.ID)
		robots[number] = i
		idle = append(idle, scheduler.RobotInfo{Num: number, Pos: scheduler.Cell{X: robot.X, Y: robot.Y}, Battery: robot.Battery})
	}
	if len(idle) == 0 {
		return
	}
	var pending []scheduler.JobInfo
	distance := s.travelDistance(s.cfg.MaintenanceBay != nil)
	jobs := make(map[int]int)
	for i, job := range s.state.Jobs {
		if job.Status != JobPending {
			continue
		}
		number := idNumber(job.ID)
		jobs[number] = i
		exit := 0
		if s.cfg.AutoCharge {
			exit = s.chargingExitDistance(job.To, "", distance)
		}
		info := scheduler.JobInfo{Num: number, From: scheduler.Cell{X: job.From.X, Y: job.From.Y}, To: scheduler.Cell{X: job.To.X, Y: job.To.Y}, ExitCost: exit, Rank: job.CreatedTick}
		if remaining := job.DeadlineTick - s.state.Tick; job.Type == JobPriority && remaining >= 0 && remaining <= priorityDeadlineSlack {
			if delivery, reachable := distance(info.From, info.To); reachable && delivery <= remaining {
				// New priority arrivals cannot overtake jobs older than this fixed advantage.
				info.Rank -= priorityHeadStartTicks
				info.DeadlineTick = job.DeadlineTick
			}
		}
		pending = append(pending, info)
	}
	assignments := scheduler.Assign(idle, pending, distance, s.assignmentParams())
	for _, assignment := range assignments {
		robot, job := &s.state.Robots[robots[assignment.RobotNum]], &s.state.Jobs[jobs[assignment.JobNum]]
		s.beginJob(robot, job)
	}
}

func (s *Sim) canAcceptJob(robot RobotState) bool {
	return robot.Role != RoleMaintenance && robot.Status == RobotIdle && robot.JobID == "" &&
		(s.cfg.MaintenanceBay == nil || s.occupiedStation(robot.ID) == nil && (!s.cfg.AutoCharge || !s.needsCharge(robot)))
}

func (s *Sim) assignmentParams() scheduler.Params {
	return scheduler.Params{
		MinBattery: s.cfg.MinAssignBattery, ReserveBattery: s.cfg.ReserveBattery, MovingDrain: s.cfg.MovingDrain,
	}
}

func (s *Sim) beginJob(robot *RobotState, job *JobState) {
	job.Status, job.AssignedRobot = JobAssigned, robot.ID
	if s.cfg.MaintenanceBay != nil {
		job.AssignedTick = s.state.Tick
	}
	robot.Status, robot.JobID = RobotMoving, job.ID
	s.prepareNavigation(robot, job.From)
}

func (s *Sim) dispatchDistance() scheduler.Distance {
	return s.travelDistance(false)
}

func (s *Sim) travelDistance(liveOccupancy bool) scheduler.Distance {
	blocked := make(map[Point]bool)
	for _, robot := range s.state.Robots {
		if !liveOccupancy && robot.Status != RobotOffline && robot.Status != RobotFaulted && robot.Status != RobotCharging {
			continue
		}
		position := Point{X: robot.X, Y: robot.Y}
		blocked[position] = true
		if s.cfg.onAisle(position) {
			for y := s.cfg.AisleYMin; y <= s.cfg.AisleYMax; y++ {
				blocked[Point{X: robot.X, Y: y}] = true
			}
		}
	}
	type leg struct{ from, to scheduler.Cell }
	type result struct {
		distance  int
		reachable bool
	}
	cache := make(map[leg]result)
	return func(from, to scheduler.Cell) (int, bool) {
		key := leg{from: from, to: to}
		if found, ok := cache[key]; ok {
			return found.distance, found.reachable
		}
		route, err := routing.FindPath(routingGrid{cfg: s.cfg}, routing.Cell{X: from.X, Y: from.Y}, routing.Cell{X: to.X, Y: to.Y}, routing.Options{
			Blocked: func(cell routing.Cell) bool { return blocked[Point{X: cell.X, Y: cell.Y}] },
		})
		found := result{distance: len(route), reachable: err == nil}
		cache[key] = found
		return found.distance, found.reachable
	}
}
