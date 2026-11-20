package fleet

import (
	"context"
	"slices"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/sim"
)

const maxAutomaticReads = 32

type AutomaticObservation struct {
	RuntimeID     string            `json:"runtimeId"`
	ObservationID string            `json:"observationId"`
	ExpiresAt     time.Time         `json:"expiresAt"`
	Versions      map[string]uint64 `json:"versions"`
	Snapshot      sim.State         `json:"snapshot"`
}

type commandObservation struct {
	id, subject     string
	issued, expires time.Time
	versions        map[string]uint64
}

type commandEntity struct {
	version uint64
	robot   sim.RobotState
	job     sim.JobState
}

func (s *Service) ReadAutomaticState(ctx context.Context) (AutomaticObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAutomaticStateLocked(ctx)
}

func (s *Service) readAutomaticStateLocked(ctx context.Context) (AutomaticObservation, error) {
	principal, _ := auth.FromContext(ctx)
	if err := s.automaticAccess(principal, s.runtimeID); err != nil {
		return AutomaticObservation{}, err
	}
	if s.poisoned {
		return AutomaticObservation{}, ErrPoisoned
	}
	if err := ctx.Err(); err != nil {
		return AutomaticObservation{}, err
	}
	now := s.clock().UTC()
	if now.IsZero() {
		return AutomaticObservation{}, ErrAutomaticExpired
	}
	id, err := newID()
	if err != nil {
		return AutomaticObservation{}, err
	}
	snapshot := s.commander.Snapshot()
	s.refreshCommandVersions(snapshot)
	versions := make(map[string]uint64, len(s.automaticVersions))
	detached := make(map[string]uint64, len(s.automaticVersions))
	for id, entity := range s.automaticVersions {
		versions[id], detached[id] = entity.version, entity.version
	}
	s.automaticReads = slices.DeleteFunc(s.automaticReads, func(read commandObservation) bool { return !now.Before(read.expires) })
	if len(s.automaticReads) == maxAutomaticReads {
		s.automaticReads = slices.Delete(s.automaticReads, 0, 1)
	}
	expires := now.Add(automaticMaxTTL)
	s.automaticReads = append(s.automaticReads, commandObservation{id: id, subject: principal.Subject, issued: now, expires: expires, versions: versions})
	return AutomaticObservation{RuntimeID: s.runtimeID, ObservationID: id, ExpiresAt: expires, Versions: detached, Snapshot: snapshot}, nil
}

func (s *Service) refreshCommandVersions(snapshot sim.State) {
	next := make(map[string]commandEntity, len(snapshot.Robots)+len(snapshot.Jobs))
	add := func(id string, current commandEntity) {
		previous := s.automaticVersions[id]
		if previous.version != 0 && previous.robot == current.robot && previous.job == current.job {
			current.version = previous.version
		} else {
			current.version = s.revision + 1
		}
		next[id] = current
	}
	for _, robot := range snapshot.Robots {
		// The blocked flag is the admission boundary. Wait counters and display
		// labels alone must not invalidate an otherwise unchanged robot.
		robot.WaitTicks, robot.Activity = 0, ""
		add(robot.ID, commandEntity{robot: robot})
	}
	for _, job := range snapshot.Jobs {
		if job.Status != sim.JobDone {
			job.QueueAgeTicks = 0
			add(job.ID, commandEntity{job: job})
		}
	}
	s.automaticVersions = next
}

func (s *Service) checkAutomaticObservation(command AutomaticCommand, subject string, now time.Time) error {
	index := slices.IndexFunc(s.automaticReads, func(read commandObservation) bool { return read.id == command.ObservationID && read.subject == subject })
	if index < 0 {
		return ErrObservation
	}
	read := s.automaticReads[index]
	if now.Before(read.issued) || !now.Before(read.expires) || command.ExpiresAt.After(read.expires) {
		return ErrAutomaticExpired
	}
	s.refreshCommandVersions(s.commander.Snapshot())
	for id, version := range command.Preconditions {
		if read.versions[id] != version || s.automaticVersions[id].version != version {
			return ErrObservation
		}
	}
	return nil
}
