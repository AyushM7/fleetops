//go:build fleetops_test

package sim

// NewReassignmentTestFixture is compiled only by explicitly tagged test builds.
// It is never part of the production server or exposed through fleet/GraphQL.
func NewReassignmentTestFixture() (*Sim, error) {
	cfg := DefaultConfig()
	cfg.Width, cfg.Height = 8, 8
	cfg.AisleColumns, cfg.AisleYMin, cfg.AisleYMax = []int{6}, 1, 2
	cfg.Stations = []Point{{7, 0}}
	cfg.RobotStarts = []Point{{0, 3}, {2, 3}, {4, 3}, {7, 7}, {5, 7}}
	cfg.InitialJobs, cfg.MaxJobs, cfg.NewJobEvery = 0, 1, 10000
	cfg.AutoDispatch, cfg.AutoCharge, cfg.Recovery = true, true, true
	for y := 0; y < cfg.Height; y++ {
		if y != 3 {
			cfg.Walls = append(cfg.Walls, Point{2, y})
		}
	}
	s, err := New(cfg, 42)
	if err != nil {
		return nil, err
	}
	s.state.Robots[0].Status, s.state.Robots[0].JobID = RobotMoving, "J1"
	s.state.Robots[1].Status = RobotOffline
	s.state.Jobs = []JobState{{ID: "J1", From: Point{4, 4}, To: Point{6, 4}, Status: JobAssigned, AssignedRobot: "R1"}}
	for range 42 {
		s.Tick()
	}
	return s, nil
}
