package sim

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/AyushM7/fleetops/backend/scheduler"
)

const (
	retainedCompletedJobs = 100
	priorityJobEvery      = 4
	priorityDeadlineSlack = 40
)

func (c Config) jobEndpoints() []Point {
	cells := c.JobCells()
	if c.MaintenanceBay != nil {
		cells = slices.DeleteFunc(cells, func(p Point) bool {
			// Reserve narrow passages and aisle mouths for continuous travel.
			return c.narrowPassage(p) || c.onAisle(p) ||
				c.onAisle(Point{p.X, p.Y - 1}) || c.onAisle(Point{p.X, p.Y + 1})
		})
	}
	return cells
}

func (s *Sim) addJob() {
	count := len(s.state.Jobs)
	if s.cfg.MaintenanceBay != nil {
		count, _ = countJobs(s.state.Jobs)
		count = s.reconsiderDeferredJobs(count)
	}
	if count >= s.cfg.MaxJobs {
		return
	}
	from := s.rng.Intn(len(s.jobCells))
	to := s.rng.Intn(len(s.jobCells) - 1)
	// Skip the source index so each job uses exactly two draws and distinct cells.
	if to >= from {
		to++
	}
	number := len(s.state.Jobs) + 1
	if s.cfg.MaintenanceBay != nil {
		s.jobSequence++
		number = s.jobSequence
	}
	job := JobState{
		ID: fmt.Sprintf("J%d", number), From: s.jobCells[from], To: s.jobCells[to],
		Status: JobPending,
	}
	if s.cfg.MaintenanceBay != nil {
		job.Type, job.CreatedTick = JobNormal, s.state.Tick
		if number%priorityJobEvery == 0 {
			job.Type = JobPriority
			// Include the shortest delivery leg before allowing time for pickup and traffic.
			route, _ := s.PlanRoute(job.From, job.To)
			job.DeadlineTick = job.CreatedTick + len(route) + priorityDeadlineSlack
		}
	}
	s.state.Jobs = append(s.state.Jobs, job)
}

func (s *Sim) trackJobDeadline(job *JobState) {
	if job.Type == JobPriority && !job.DeadlineMissed && s.state.Tick > job.DeadlineTick {
		job.DeadlineMissed = true
		s.state.PriorityDeadlinesMissed++
	}
}

func (s *Sim) completeJob(job *JobState) {
	s.state.CompletedJobs++
	if job.Type == "" {
		return
	}
	job.CompletedTick = s.state.Tick
	job.Outcome = JobDelivered
	if job.Type == JobPriority {
		s.trackJobDeadline(job)
		job.Outcome = JobLate
		if !job.DeadlineMissed {
			job.Outcome = JobOnTime
			s.state.PriorityDeadlinesMet++
		}
	}
}

func countJobs(jobs []JobState) (active, completed int) {
	for _, job := range jobs {
		if job.Status == JobDone {
			completed++
		} else if !job.Deferred {
			active++
		}
	}
	return active, completed
}

func (s *Sim) trimCompletedJobs() {
	if len(s.state.Jobs) <= retainedCompletedJobs {
		return
	}
	_, completed := countJobs(s.state.Jobs)
	overflow := completed - retainedCompletedJobs
	if overflow <= 0 {
		return
	}
	s.state.Jobs = slices.DeleteFunc(s.state.Jobs, func(job JobState) bool {
		if overflow == 0 || job.Status != JobDone {
			return false
		}
		overflow--
		return true
	})
}

// idNumber reads an internally generated entity ID; malformed suffixes return zero.
func idNumber(id string) int {
	if len(id) < 2 {
		return 0
	}
	number, err := strconv.Atoi(id[1:])
	if err != nil || number < 1 {
		return 0
	}
	return number
}

func lowestPendingJob(jobs []JobState) *JobState {
	var lowest *JobState
	for i := range jobs {
		job := &jobs[i]
		if job.Status == JobPending && (lowest == nil || idNumber(job.ID) < idNumber(lowest.ID)) {
			lowest = job
		}
	}
	return lowest
}

const maxDeferredJobs = 100

// Only a full queue with no assigned or carried work can defer an unserviceable job.
// Deferred jobs retain their identity, priority and age, and take the next
// available slot before a new job if their route and charge become feasible.
func (s *Sim) reconsiderDeferredJobs(active int) int {
	if !s.cfg.AutoDispatch || !s.cfg.AutoCharge {
		return active
	}
	var deferred int
	allPending := true
	for _, job := range s.state.Jobs {
		if job.Deferred {
			deferred++
		}
		allPending = allPending && job.Status != JobAssigned && job.Status != JobInProgress
	}
	if active >= s.cfg.MaxJobs && (!allPending || deferred >= maxDeferredJobs) || active < s.cfg.MaxJobs && deferred == 0 {
		return active
	}
	projection := s.optimisticMission()
	if active >= s.cfg.MaxJobs {
		for i := len(s.state.Jobs) - 1; i >= 0; i-- {
			job := &s.state.Jobs[i]
			if job.Status == JobPending && !job.Deferred && !projection(*job) {
				job.Deferred = true
				active--
				break
			}
		}
	}
	for i := range s.state.Jobs {
		job := &s.state.Jobs[i]
		if active >= s.cfg.MaxJobs {
			break
		}
		if job.Deferred && projection(*job) {
			job.Deferred = false
			active++
		}
	}
	return active
}

func (s *Sim) optimisticMission() func(JobState) bool {
	projected := *s
	projected.state.Robots = slices.DeleteFunc(slices.Clone(s.state.Robots), func(robot RobotState) bool {
		return robot.Status != RobotFaulted && robot.Status != RobotOffline
	})
	distance := projected.travelDistance(false)
	var origins []scheduler.RobotInfo
	var docks []scheduler.Cell
	for _, dock := range s.state.Stations {
		occupant := s.faultRobot(dock.OccupiedBy)
		if occupant != nil && (occupant.Status == RobotFaulted || occupant.Status == RobotOffline) {
			continue
		}
		point := scheduler.Cell{X: dock.X, Y: dock.Y}
		docks = append(docks, point)
		origins = append(origins, scheduler.RobotInfo{Pos: point, Battery: s.cfg.ChargeRelease})
	}
	for _, robot := range s.state.Robots {
		if robot.Role != RoleMaintenance && robot.Status != RobotFaulted && robot.Status != RobotOffline {
			origins = append(origins, scheduler.RobotInfo{Pos: scheduler.Cell{X: robot.X, Y: robot.Y}, Battery: robot.Battery})
		}
	}
	return func(job JobState) bool {
		from, to := scheduler.Cell{X: job.From.X, Y: job.From.Y}, scheduler.Cell{X: job.To.X, Y: job.To.Y}
		delivery, ok := distance(from, to)
		if !ok {
			return false
		}
		exit := -1
		for _, dock := range docks {
			if steps, ok := distance(to, dock); ok && (exit < 0 || steps < exit) {
				exit = steps
			}
		}
		if exit < 0 {
			return false
		}
		for _, origin := range origins {
			if pickup, ok := distance(origin.Pos, from); ok && origin.Battery >= (pickup+delivery+exit)*s.cfg.MovingDrain+s.cfg.ReserveBattery {
				return true
			}
		}
		return false
	}
}
