package fleet

import (
	"context"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/incident"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

// Read DTOs contain no simulator capabilities. Returned values are detached copies.
type RobotView struct {
	ID            string
	X, Y, Battery int
	Status, JobID string
	WaitTicks     int
	Blocked       bool
}
type StationView struct {
	ID                     string
	X, Y                   int
	OccupiedBy, ReservedBy string
}
type StateView struct {
	ObservationID          string
	Layout                 LayoutView
	Tick                   int
	Robots                 []RobotView
	Stations               []StationView
	Poisoned               bool
	ActiveIncidents        []IncidentView
	Jobs                   []JobView
	ReassignmentCandidates []ReassignmentCandidateView
}

type JobView struct {
	ID, Status, AssignedRobot string
	FromX, FromY, ToX, ToY    int
}

type ReassignmentCandidateView struct {
	JobID, FromRobotID, ToRobotID                                  string
	PickupSteps, DeliverySteps, ChargingExitSteps, RequiredBattery int
}

// IncidentView describes a current condition, without persistent episode identity.
type IncidentView struct {
	RobotID   string
	Type      string
	Severity  string
	Telemetry RobotView
}

func robotView(r sim.RobotState) RobotView {
	return RobotView{r.ID, r.X, r.Y, r.Battery, string(r.Status), r.JobID, r.WaitTicks, r.Blocked}
}

func currentIncidents(snapshot sim.State) ([]IncidentView, error) {
	// No history is retained: IDs allocated by Reconcile are deliberately not exposed.
	conditions, _, err := incident.Reconcile(nil, snapshot, 0)
	if err != nil {
		return nil, err
	}
	robots := make(map[string]RobotView, len(snapshot.Robots))
	for _, r := range snapshot.Robots {
		robots[r.ID] = robotView(r)
	}
	views := make([]IncidentView, 0, len(conditions))
	for _, condition := range conditions {
		views = append(views, IncidentView{RobotID: condition.RobotID, Type: string(condition.Type), Severity: string(condition.Severity), Telemetry: robots[condition.RobotID]})
	}
	return views, nil
}

type CellView struct {
	X, Y int
	Kind string
}
type LayoutView struct {
	Width, Height int
	Cells         []CellView
}

func layoutFromConfig(cfg sim.Config) LayoutView {
	layout := LayoutView{Width: cfg.Width, Height: cfg.Height, Cells: make([]CellView, 0, cfg.Width*cfg.Height)}
	for y := 0; y < cfg.Height; y++ {
		for x := 0; x < cfg.Width; x++ {
			layout.Cells = append(layout.Cells, CellView{x, y, string(cfg.CellTypeAt(sim.Point{X: x, Y: y}))})
		}
	}
	return layout
}

type ProposalView struct {
	Proposal       proposal.Proposal
	Execution      Execution
	SubmittedBy    string
	CurrentRuntime bool
}

func (s *Service) ReadState(context.Context) (StateView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.commander.Snapshot()
	incidents, err := currentIncidents(snapshot)
	if err != nil {
		return StateView{}, err
	}
	view := StateView{ObservationID: s.observationID(), Tick: snapshot.Tick, Poisoned: s.poisoned, Robots: []RobotView{}, Stations: []StationView{}}
	view.ActiveIncidents = incidents
	view.Jobs = make([]JobView, 0, len(snapshot.Jobs))
	view.ReassignmentCandidates = []ReassignmentCandidateView{}
	for _, j := range snapshot.Jobs {
		view.Jobs = append(view.Jobs, JobView{j.ID, string(j.Status), j.AssignedRobot, j.From.X, j.From.Y, j.To.X, j.To.Y})
		if s.poisoned || j.Status != sim.JobAssigned {
			continue
		}
		for _, recipient := range snapshot.Robots {
			if recipient.Status != sim.RobotIdle || recipient.JobID != "" {
				continue
			}
			plan, err := s.commander.PreviewReassignment(j.ID, j.AssignedRobot, recipient.ID)
			if err == nil {
				view.ReassignmentCandidates = append(view.ReassignmentCandidates, ReassignmentCandidateView{j.ID, j.AssignedRobot, recipient.ID, plan.PickupSteps, plan.DeliverySteps, plan.ChargingExitSteps, plan.RequiredBattery})
			}
		}
	}
	view.Layout = s.layout
	view.Layout.Cells = append([]CellView{}, s.layout.Cells...)
	for _, r := range snapshot.Robots {
		view.Robots = append(view.Robots, robotView(r))
	}
	for _, r := range snapshot.Stations {
		view.Stations = append(view.Stations, StationView{r.ID, r.X, r.Y, r.OccupiedBy, r.ReservedBy})
	}
	return view, nil
}
func (s *Service) ReadProposal(ctx context.Context, id string) (*ProposalView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var view *ProposalView
	err := s.transact(ctx, func(tx *db.Tx) error {
		row, err := s.load(tx, id)
		if err != nil {
			return err
		}
		if row == nil {
			return nil
		}
		view = &ProposalView{row.proposal, row.execution, row.submittedBy, row.runtimeID == s.runtimeID}
		return nil
	})
	if err != nil {
		return nil, ErrPersistence
	}
	return view, nil
}
