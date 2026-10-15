package sim

import (
	"fmt"
	"strconv"
)

func (s *Sim) addJob() {
	if len(s.state.Jobs) >= s.cfg.MaxJobs {
		return
	}
	from := s.rng.Intn(len(s.jobCells))
	to := s.rng.Intn(len(s.jobCells) - 1)
	// Skip the source index so each job uses exactly two draws and distinct cells.
	if to >= from {
		to++
	}
	s.state.Jobs = append(s.state.Jobs, JobState{
		ID:   fmt.Sprintf("J%d", len(s.state.Jobs)+1),
		From: s.jobCells[from], To: s.jobCells[to], Status: JobPending,
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
