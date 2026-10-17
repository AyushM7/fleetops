package proposal_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

type reassignmentFixtureReader struct {
	*fixtureReader
	previewErr error
}

func (r *reassignmentFixtureReader) PreviewReassignment(string, string, string) (sim.ReassignmentPlan, error) {
	return sim.ReassignmentPlan{}, r.previewErr
}

func TestReassignmentLifecycleImmutableAndRevalidated(t *testing.T) {
	request := safety.ReassignUnstartedJobRequest{JobID: "J1", FromRobotID: "R1", ToRobotID: "R3"}
	reader := &reassignmentFixtureReader{fixtureReader: &fixtureReader{state: sim.State{Robots: []sim.RobotState{{ID: "R1", Battery: 100}, {ID: "R3", Battery: 100}}}}}
	lifecycle, err := proposal.New(reader, func() time.Time { return start })
	if err != nil {
		t.Fatal(err)
	}
	before := reader.state
	created, err := lifecycle.CreateReassignment("recovery", request, start.Add(time.Minute))
	if err != nil || created.Proposal.State != proposal.Pending || created.Proposal.Action != proposal.ReassignUnstartedJob {
		t.Fatal(created, err)
	}
	created.Proposal.Reassignment.ToRobotID = "tampered"
	reader.previewErr = sim.ErrReassignmentRoute
	denied, err := lifecycle.Decide("recovery", proposal.Approve, operator)
	if !errors.Is(err, sim.ErrReassignmentRoute) || denied.Proposal.State != proposal.Pending || denied.Proposal.Reassignment != request {
		t.Fatal("stale or mutable proposal", denied, err)
	}
	reader.previewErr = nil
	approved, err := lifecycle.Decide("recovery", proposal.Approve, operator)
	if err != nil || approved.Proposal.State != proposal.Approved || !reflect.DeepEqual(before, reader.state) {
		t.Fatal("lifecycle executed or changed request", approved, err)
	}
	if _, err := lifecycle.Decide("recovery", proposal.Approve, operator); !errors.Is(err, proposal.ErrInvalidTransition) {
		t.Fatal("duplicate approval accepted", err)
	}
	mixed := approved.Proposal
	mixed.Request = safety.SetChargingRequest{RobotID: "R1", StationID: "S1"}
	if err := proposal.Preflight(reader, mixed); !errors.Is(err, proposal.ErrInvalidAction) {
		t.Fatal("mixed payload accepted", err)
	}
	mixed.Action = "unsupported"
	if err := proposal.Preflight(reader, mixed); !errors.Is(err, proposal.ErrInvalidAction) {
		t.Fatal("unknown command accepted", err)
	}
}
