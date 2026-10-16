package sim

import (
	"github.com/AyushM7/fleetops/backend/routing"
	"github.com/AyushM7/fleetops/backend/scheduler"
)

func (s *Sim) assignPending(skip []bool) {
	if !s.cfg.AutoDispatch {
		return
	}
	var idle []scheduler.RobotInfo
	robots := make(map[int]int)
	for i, robot := range s.state.Robots {
		if skip[i] || robot.Status != RobotIdle || robot.JobID != "" {
			continue
		}
		number := idNumber(robot.ID)
		robots[number] = i
		idle = append(idle, scheduler.RobotInfo{Num: number, Pos: scheduler.Cell{X: robot.X, Y: robot.Y}, Battery: robot.Battery})
	}
	var pending []scheduler.JobInfo
	distance := s.dispatchDistance()
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
		pending = append(pending, scheduler.JobInfo{Num: number, From: scheduler.Cell{X: job.From.X, Y: job.From.Y}, To: scheduler.Cell{X: job.To.X, Y: job.To.Y}, ExitCost: exit})
	}
	assignments := scheduler.Assign(idle, pending, distance, scheduler.Params{
		MinBattery: s.cfg.MinAssignBattery, ReserveBattery: s.cfg.ReserveBattery, MovingDrain: s.cfg.MovingDrain,
	})
	for _, assignment := range assignments {
		robot, job := &s.state.Robots[robots[assignment.RobotNum]], &s.state.Jobs[jobs[assignment.JobNum]]
		job.Status, job.AssignedRobot = JobAssigned, robot.ID
		robot.Status, robot.JobID = RobotMoving, job.ID
		s.prepareNavigation(robot, job.From)
	}
}

func (s *Sim) dispatchDistance() scheduler.Distance {
	blocked := make(map[Point]bool)
	for _, robot := range s.state.Robots {
		if robot.Status != RobotOffline && robot.Status != RobotFaulted && robot.Status != RobotCharging {
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
