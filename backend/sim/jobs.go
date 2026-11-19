package sim

import (
	"fmt"
	"slices"
	"strconv"
)

const retainedCompletedJobs = 100

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
	s.state.Jobs = append(s.state.Jobs, JobState{
		ID:   fmt.Sprintf("J%d", number),
		From: s.jobCells[from], To: s.jobCells[to], Status: JobPending,
	})
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
