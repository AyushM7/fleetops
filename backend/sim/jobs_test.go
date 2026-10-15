package sim

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestJobDeterminismAndValidity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoDispatch = false
	cfg.Walls = []Point{{2, 2}, {3, 3}}
	first := newTestSim(t, cfg, 42)
	second := newTestSim(t, cfg, 42)
	different := newTestSim(t, cfg, 43)
	if reflect.DeepEqual(first.state.Jobs, different.state.Jobs) {
		t.Fatal("different seeds produced identical initial queues")
	}
	cells := cfg.JobCells()
	for tick := 0; tick <= 200; tick++ {
		if !reflect.DeepEqual(first.state.Jobs, second.state.Jobs) {
			t.Fatalf("same seed diverged at tick %d", tick)
		}
		for _, job := range first.state.Jobs {
			if job.From == job.To || !slices.Contains(cells, job.From) || !slices.Contains(cells, job.To) {
				t.Fatalf("ineligible or identical endpoints: %+v", job)
			}
		}
		if tick < 200 {
			first.Tick()
			second.Tick()
			different.Tick()
		}
	}
	if reflect.DeepEqual(first.state.Jobs, different.state.Jobs) {
		t.Fatal("different seeds produced identical final queues")
	}
}

func TestJobScheduleAndNumericOrder(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoDispatch = false
	s := newTestSim(t, cfg, 42)
	for tick := 0; tick <= 250; tick++ {
		want := min(8+tick/10, 20)
		if s.state.Tick != tick || len(s.state.Jobs) != want {
			t.Fatalf("tick %d: state tick=%d jobs=%d, want %d", tick, s.state.Tick, len(s.state.Jobs), want)
		}
		for i, job := range s.state.Jobs {
			if job.ID != fmt.Sprintf("J%d", i+1) || job.Status != JobPending || job.AssignedRobot != "" {
				t.Fatalf("tick %d: unexpected job at index %d: %+v", tick, i, job)
			}
		}
		if tick < 250 {
			s.Tick()
		}
	}
	before := slices.Clone(s.state.Jobs)
	s.addJob()
	if !reflect.DeepEqual(s.state.Jobs, before) {
		t.Fatal("job generator exceeded MaxJobs")
	}
}

func TestConfiguredJobIntervalAndLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoDispatch = false
	cfg.InitialJobs, cfg.NewJobEvery, cfg.MaxJobs = 0, 3, 2
	s := newTestSim(t, cfg, 1)
	for tick := 1; tick <= 12; tick++ {
		s.Tick()
		if len(s.state.Jobs) != min(tick/3, 2) {
			t.Fatalf("configured schedule ignored at tick %d", tick)
		}
	}
}

func TestNumericJobSelection(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want int
	}{
		{"R1", 1}, {"J2", 2}, {"J9", 9}, {"J10", 10}, {"S2", 2}, {"F10", 10},
		{"", 0}, {"J", 0}, {"Jbad", 0}, {"J0", 0}, {"J-1", 0},
	} {
		if got := idNumber(tc.id); got != tc.want {
			t.Errorf("idNumber(%q) = %d, want %d", tc.id, got, tc.want)
		}
	}
	jobs := []JobState{{ID: "J10", Status: JobPending}, {ID: "J2", Status: JobPending}, {ID: "J9", Status: JobPending}}
	before := slices.Clone(jobs)
	if got := lowestPendingJob(jobs); got != &jobs[1] || !reflect.DeepEqual(jobs, before) {
		t.Fatal("shuffled pending selection must choose J2 without reordering")
	}
	for _, status := range []JobStatus{JobAssigned, JobInProgress, JobDone} {
		jobs[1].Status = status
		if got := lowestPendingJob(jobs); got != &jobs[2] {
			t.Fatalf("status %s was not excluded; want J9", status)
		}
	}
	jobs[0].Status, jobs[2].Status = JobAssigned, JobInProgress
	if lowestPendingJob(jobs) != nil || lowestPendingJob(nil) != nil {
		t.Fatal("no pending jobs must return nil")
	}
}

func TestSnapshotJSONDeterminism(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AutoDispatch = false
	first := newTestSim(t, cfg, 42)
	second := newTestSim(t, cfg, 42)
	for tick := 0; tick <= 200; tick++ {
		if tick == 0 || tick == 200 {
			left, right := first.Snapshot(), second.Snapshot()
			if left.Tick != tick || left.Seed != 42 || stateJSON(t, left) != stateJSON(t, right) {
				t.Fatalf("snapshot JSON differs at tick %d", tick)
			}
		}
		if tick < 200 {
			first.Tick()
			second.Tick()
		}
	}
	data := stateJSON(t, first.Snapshot())
	j2 := strings.Index(data, `"id":"J2"`)
	j10 := strings.Index(data, `"id":"J10"`)
	if len(first.Snapshot().Jobs) != 20 || j2 < 0 || j10 < 0 || j2 >= j10 {
		t.Fatalf("snapshot jobs must retain numeric order through J20: %s", data)
	}
}
