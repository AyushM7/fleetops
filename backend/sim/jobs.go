package sim

import (
	"fmt"
	"slices"
	"strconv"
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
		} else {
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
