package fleet

import (
	"context"

	"github.com/AyushM7/fleetops/backend/db"
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
	Layout   LayoutView
	Tick     int
	Robots   []RobotView
	Stations []StationView
	Poisoned bool
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
	view := StateView{Tick: snapshot.Tick, Poisoned: s.poisoned, Robots: []RobotView{}, Stations: []StationView{}}
	view.Layout = s.layout
	view.Layout.Cells = append([]CellView{}, s.layout.Cells...)
	for _, r := range snapshot.Robots {
		view.Robots = append(view.Robots, RobotView{r.ID, r.X, r.Y, r.Battery, string(r.Status), r.JobID, r.WaitTicks, r.Blocked})
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
