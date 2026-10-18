//go:build fleetops_test

package fleet_test

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/graphql/model"
	"github.com/AyushM7/fleetops/backend/sim"
)

type demoGuidance struct {
	Stage        string `json:"stage"`
	Decision     string `json:"decision,omitempty"`
	ProposalID   string `json:"proposalId,omitempty"`
	DecisionTick *int   `json:"decisionTick,omitempty"`
	DeliveryTick *int   `json:"deliveryTick,omitempty"`
}

func (s *demoScenario) guidance() *demoGuidance {
	copy := *s.guide
	return &copy
}

func (s *demoScenario) inspectLeadup(ctx context.Context) (*model.FleetState, bool, error) {
	state, err := (&graphql.Resolver{Service: s.service}).Query().FleetState(ctx)
	if err != nil {
		return nil, false, err
	}
	s.guide.Stage = "observe"
	for _, incident := range state.ActiveIncidents {
		if incident.RobotID == "R1" && incident.Type == "traffic_blocked" {
			s.guide.Stage = "incident"
		}
	}
	if state.Tick >= 42 {
		s.guide.Stage = "analyze"
	}
	return state, state.Tick < 42, nil
}

func (s *demoScenario) guidedStep(ctx context.Context) error {
	before := s.service.Snapshot()
	if s.evidence.ProposalID == "" && before.Tick >= 42 {
		return errors.New("decision boundary cannot be bypassed")
	}
	if err := s.service.Tick(); err != nil {
		return err
	}
	state := s.service.Snapshot()
	if state.Tick <= 42 {
		s.evidence.Progress = append(s.evidence.Progress, state)
	}
	if state.Tick == 42 {
		return s.reachDecision(ctx)
	}
	if s.guide.Decision == "approved" && s.guide.DeliveryTick == nil {
		for _, job := range state.Jobs {
			if job.ID == "J1" && job.Status == sim.JobDone && job.AssignedRobot == "R3" {
				tick := state.Tick
				s.guide.DeliveryTick = &tick
				s.evidence.DeliveredTick = &tick
			}
		}
	}
	return nil
}

func (s *demoScenario) reachDecision(ctx context.Context) error {
	state := s.service.Snapshot()
	// Equality includes seed, assignments, faults, waiting, positions and battery.
	if !reflect.DeepEqual(state, s.target) {
		return errors.New("guided intervention state differs from the verified fixture")
	}
	view, err := s.service.ReadState(ctx)
	if err != nil || view.Poisoned {
		return errors.New("guided intervention unavailable")
	}
	for _, candidate := range view.ReassignmentCandidates {
		if candidate.JobID == "J1" && candidate.FromRobotID == "R1" && candidate.ToRobotID == "R3" {
			s.reached, s.guide.Stage = true, "analyze"
			tick := state.Tick
			s.guide.DecisionTick, s.evidence.DecisionPoint = &tick, &state
			return nil
		}
	}
	return errors.New("Go-admissible recovery candidate missing at decision boundary")
}

func (s *demoScenario) prepareGuided(ctx context.Context) error {
	if !s.reached || s.attempted {
		return nil
	}
	// This is trusted pump work under the lifecycle gate, never a browser read.
	// Record the attempt before invoking the proposer; unknown submissions cannot retry.
	s.attempted = true
	if err := s.reachDecision(ctx); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := s.installProposal(ctx); err != nil {
		return err
	}
	s.guide.Stage, s.guide.Decision = "decide", "pending"
	return s.capture(ctx)
}
