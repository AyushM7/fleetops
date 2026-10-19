package sim

import "errors"

// NewVisitorPreset constructs only fixed, server-owned initial states. It does
// not accept visitor configuration or expose a mutation capability over HTTP.
func NewVisitorPreset(id string) (*Sim, int, int, error) {
	cfg := WarehouseConfig()
	switch id {
	case "healthy-operations":
	case "routine-charging":
		cfg = DefaultConfig()
		cfg.InitialJobs, cfg.MaxJobs, cfg.NewJobEvery = 0, 1, 10000
		cfg.RobotStarts[0] = cfg.Stations[0]
	case "blocked-reassignment", "blocked-no-candidate":
		return blockedVisitorPreset(id)
	default:
		return nil, 0, 0, errors.New("unsupported visitor preset")
	}
	s, err := New(cfg, 42)
	if err == nil && id == "routine-charging" {
		for range 1000 {
			s.Tick()
		}
	}
	return s, cfg.Width, cfg.Height, err
}

func blockedVisitorPreset(id string) (*Sim, int, int, error) {
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
		return nil, 0, 0, err
	}
	s.state.Robots[0].Status, s.state.Robots[0].JobID = RobotMoving, "J1"
	s.state.Robots[1].Status = RobotOffline
	s.state.Jobs = []JobState{{ID: "J1", From: Point{4, 4}, To: Point{6, 4}, Status: JobAssigned, AssignedRobot: "R1"}}
	for range 42 {
		s.Tick()
	}
	if id == "blocked-no-candidate" {
		for _, robot := range []string{"R3", "R4", "R5"} {
			if err := s.InjectLowBattery(robot); err != nil {
				return nil, 0, 0, err
			}
		}
	}
	return s, cfg.Width, cfg.Height, nil
}
