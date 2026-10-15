package sim

import (
	"fmt"
	"slices"
)

type Config struct {
	Width                 int
	Height                int
	AisleColumns          []int
	AisleYMin             int
	AisleYMax             int
	Walls                 []Point
	Stations              []Point
	RobotStarts           []Point
	InitialJobs           int
	NewJobEvery           int
	MaxJobs               int
	MovingDrain           int
	IdleDrainEvery        int
	ChargeGain            int
	MinAssignBattery      int
	UnresponsiveDelay     int
	LowBatteryInjectLevel int
	AutoDispatch          bool
}

func DefaultConfig() Config {
	return Config{
		Width:                 20,
		Height:                12,
		AisleColumns:          []int{4, 9, 14},
		AisleYMin:             1,
		AisleYMax:             10,
		Walls:                 []Point{},
		Stations:              []Point{{0, 0}, {19, 0}},
		RobotStarts:           []Point{{1, 11}, {3, 11}, {5, 11}, {7, 11}, {9, 11}, {11, 11}},
		InitialJobs:           8,
		NewJobEvery:           10,
		MaxJobs:               20,
		MovingDrain:           1,
		IdleDrainEvery:        20,
		ChargeGain:            2,
		MinAssignBattery:      30,
		UnresponsiveDelay:     3,
		LowBatteryInjectLevel: 15,
		AutoDispatch:          true,
	}
}

func (c Config) InBounds(p Point) bool {
	return p.X >= 0 && p.X < c.Width && p.Y >= 0 && p.Y < c.Height
}

func (c Config) CellTypeAt(p Point) CellType {
	switch {
	case !c.InBounds(p):
		return CellOutOfBounds
	case slices.Contains(c.Walls, p):
		return CellWall
	case slices.Contains(c.Stations, p):
		return CellStation
	case p.Y >= c.AisleYMin && p.Y <= c.AisleYMax && slices.Contains(c.AisleColumns, p.X):
		return CellAisle
	default:
		return CellFree
	}
}

func (c Config) AisleColumn(id int) (int, bool) {
	if id < 0 || id >= len(c.AisleColumns) {
		return 0, false
	}
	return c.AisleColumns[id], true
}

func (c Config) JobCells() []Point {
	cells := make([]Point, 0)
	for y := 0; y < c.Height; y++ {
		for x := 0; x < c.Width; x++ {
			p := Point{x, y}
			if cell := c.CellTypeAt(p); cell == CellFree || cell == CellAisle {
				cells = append(cells, p)
			}
		}
	}
	return cells
}

func (c Config) Clone() Config {
	c.AisleColumns = slices.Clone(c.AisleColumns)
	c.Walls = slices.Clone(c.Walls)
	c.Stations = slices.Clone(c.Stations)
	c.RobotStarts = slices.Clone(c.RobotStarts)
	return c
}

func (c Config) Validate() error {
	if c.Width < 4 || c.Width > 50 || c.Height < 4 || c.Height > 50 {
		return fmt.Errorf("%w: width and height must each be in [4,50]", ErrInvalidConfig)
	}
	if len(c.AisleColumns) == 0 {
		return fmt.Errorf("%w: at least one aisle column is required", ErrInvalidConfig)
	}
	for i, column := range c.AisleColumns {
		if column < 0 || column >= c.Width || (i > 0 && column <= c.AisleColumns[i-1]) {
			return fmt.Errorf("%w: aisle columns must be strictly ascending and within the grid", ErrInvalidConfig)
		}
	}
	if c.AisleYMin < 0 || c.AisleYMin > c.AisleYMax || c.AisleYMax >= c.Height {
		return fmt.Errorf("%w: aisle Y bounds must satisfy 0 <= min <= max < height", ErrInvalidConfig)
	}
	if err := c.validateLayout(); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value int
		max   int
	}{
		{"MovingDrain", c.MovingDrain, 100},
		{"IdleDrainEvery", c.IdleDrainEvery, 0},
		{"ChargeGain", c.ChargeGain, 100},
		{"UnresponsiveDelay", c.UnresponsiveDelay, 0},
	} {
		if field.value < 1 || (field.max != 0 && field.value > field.max) {
			return fmt.Errorf("%w: %s must be positive%s", ErrInvalidConfig, field.name, limitDescription(field.max))
		}
	}
	if c.LowBatteryInjectLevel < 0 || c.LowBatteryInjectLevel >= c.MinAssignBattery || c.MinAssignBattery > 100 {
		return fmt.Errorf("%w: battery thresholds must satisfy 0 <= injection < assignment <= 100", ErrInvalidConfig)
	}
	if c.NewJobEvery < 1 {
		return fmt.Errorf("%w: NewJobEvery must be at least 1", ErrInvalidConfig)
	}
	if c.MaxJobs < 1 || c.MaxJobs > 200 || c.InitialJobs < 0 || c.InitialJobs > c.MaxJobs {
		return fmt.Errorf("%w: MaxJobs must be in [1,200] and InitialJobs in [0,MaxJobs]", ErrInvalidConfig)
	}
	if len(c.JobCells()) < 2 {
		return fmt.Errorf("%w: at least two eligible job cells are required", ErrInvalidConfig)
	}
	return nil
}

func limitDescription(max int) string {
	if max == 0 {
		return ""
	}
	return fmt.Sprintf(" and at most %d", max)
}

func (c Config) validateLayout() error {
	if len(c.Stations) < 1 || len(c.Stations) > 4 {
		return fmt.Errorf("%w: station count must be in [1,4]", ErrInvalidConfig)
	}
	if len(c.RobotStarts) < 5 || len(c.RobotStarts) > 8 {
		return fmt.Errorf("%w: robot start count must be in [5,8]", ErrInvalidConfig)
	}
	for _, group := range []struct {
		name   string
		points []Point
	}{
		{"stations", c.Stations},
		{"walls", c.Walls},
		{"robot starts", c.RobotStarts},
	} {
		seen := make(map[Point]bool, len(group.points))
		for _, p := range group.points {
			if !c.InBounds(p) || seen[p] {
				return fmt.Errorf("%w: %s positions must be unique and in bounds: %v", ErrInvalidConfig, group.name, p)
			}
			seen[p] = true
		}
	}
	for _, p := range c.Stations {
		if c.onAisle(p) || slices.Contains(c.Walls, p) {
			return fmt.Errorf("%w: station %v overlaps an aisle or wall", ErrInvalidConfig, p)
		}
	}
	for _, p := range c.Walls {
		if c.onAisle(p) || slices.Contains(c.Stations, p) {
			return fmt.Errorf("%w: wall %v overlaps an aisle or station", ErrInvalidConfig, p)
		}
	}
	for _, p := range c.RobotStarts {
		if slices.Contains(c.Walls, p) {
			return fmt.Errorf("%w: robot start %v is on a wall", ErrInvalidConfig, p)
		}
	}
	return nil
}

func (c Config) onAisle(p Point) bool {
	// CellTypeAt prioritizes walls and stations, so overlap checks need raw aisle geometry.
	return p.Y >= c.AisleYMin && p.Y <= c.AisleYMax && slices.Contains(c.AisleColumns, p.X)
}
