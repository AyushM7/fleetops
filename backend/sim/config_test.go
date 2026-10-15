package sim

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func cfgRobotOnStation() Config {
	cfg := DefaultConfig()
	cfg.RobotStarts[0] = Point{0, 0}
	cfg.RobotStarts[1] = Point{19, 0}
	return cfg
}

func cfgOneJobCell() Config {
	cfg := DefaultConfig()
	cfg.Width, cfg.Height = 4, 4
	cfg.AisleColumns = []int{3}
	cfg.AisleYMin, cfg.AisleYMax = 3, 3
	cfg.Stations = []Point{{0, 0}, {1, 0}, {2, 0}, {3, 0}}
	cfg.RobotStarts = append([]Point{}, cfg.Stations...)
	cfg.RobotStarts = append(cfg.RobotStarts, Point{3, 3})
	for y := 1; y < cfg.Height; y++ {
		for x := 0; x < cfg.Width; x++ {
			if p := (Point{x, y}); p != (Point{3, 3}) {
				cfg.Walls = append(cfg.Walls, p)
			}
		}
	}
	return cfg
}

func TestValidateInvalid(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"V1 width below", func(c *Config) { c.Width = 3 }},
		{"V1 width above", func(c *Config) { c.Width = 51 }},
		{"V1 height below", func(c *Config) { c.Height = 3 }},
		{"V1 height above", func(c *Config) { c.Height = 51 }},
		{"V2 no aisles", func(c *Config) { c.AisleColumns = nil }},
		{"V2 duplicate aisles", func(c *Config) { c.AisleColumns = []int{4, 4} }},
		{"V2 unordered aisles", func(c *Config) { c.AisleColumns = []int{9, 4} }},
		{"V2 negative aisle", func(c *Config) { c.AisleColumns = []int{-1} }},
		{"V2 aisle beyond grid", func(c *Config) { c.AisleColumns = []int{20} }},
		{"V3 negative minimum", func(c *Config) { c.AisleYMin = -1 }},
		{"V3 reversed bounds", func(c *Config) { c.AisleYMin = 11 }},
		{"V3 maximum beyond grid", func(c *Config) { c.AisleYMax = 12 }},
		{"V4 no stations", func(c *Config) { c.Stations = nil }},
		{"V4 five stations", func(c *Config) { c.Stations = []Point{{0, 0}, {1, 0}, {2, 0}, {3, 0}, {4, 0}} }},
		{"V4 duplicate stations", func(c *Config) { c.Stations[1] = c.Stations[0] }},
		{"V4 station out of bounds", func(c *Config) { c.Stations[0] = Point{-1, 0} }},
		{"V4 station on aisle", func(c *Config) { c.Stations[0] = Point{4, 1} }},
		{"V4 station on wall", func(c *Config) {
			c.Walls = []Point{{2, 2}}
			c.Stations[0] = Point{2, 2}
		}},
		{"V5 duplicate walls", func(c *Config) { c.Walls = []Point{{2, 2}, {2, 2}} }},
		{"V5 wall out of bounds", func(c *Config) { c.Walls = []Point{{0, 12}} }},
		{"V5 wall on aisle", func(c *Config) { c.Walls = []Point{{4, 1}} }},
		{"V5 wall on station", func(c *Config) { c.Walls = []Point{{0, 0}} }},
		{"V6 duplicate starts", func(c *Config) { c.RobotStarts[1] = c.RobotStarts[0] }},
		{"V6 start on wall", func(c *Config) { c.Walls = []Point{{1, 11}} }},
		{"V6 start out of bounds", func(c *Config) { c.RobotStarts[0] = Point{20, 11} }},
		{"V6 four robots", func(c *Config) { c.RobotStarts = c.RobotStarts[:4] }},
		{"V6 nine robots", func(c *Config) { c.RobotStarts = append(c.RobotStarts, Point{0, 11}, Point{2, 11}, Point{4, 11}) }},
		{"V7 zero moving drain", func(c *Config) { c.MovingDrain = 0 }},
		{"V7 negative moving drain", func(c *Config) { c.MovingDrain = -1 }},
		{"V7 excessive moving drain", func(c *Config) { c.MovingDrain = 101 }},
		{"V7 zero idle interval", func(c *Config) { c.IdleDrainEvery = 0 }},
		{"V7 negative idle interval", func(c *Config) { c.IdleDrainEvery = -1 }},
		{"V7 zero charge gain", func(c *Config) { c.ChargeGain = 0 }},
		{"V7 negative charge gain", func(c *Config) { c.ChargeGain = -1 }},
		{"V7 excessive charge gain", func(c *Config) { c.ChargeGain = 101 }},
		{"V7 zero delay", func(c *Config) { c.UnresponsiveDelay = 0 }},
		{"V7 negative delay", func(c *Config) { c.UnresponsiveDelay = -1 }},
		{"V8 negative injection", func(c *Config) { c.LowBatteryInjectLevel = -1 }},
		{"V8 equal thresholds", func(c *Config) { c.LowBatteryInjectLevel = c.MinAssignBattery }},
		{"V8 injection above assignment", func(c *Config) { c.LowBatteryInjectLevel = 31 }},
		{"V8 zero assignment", func(c *Config) { c.MinAssignBattery = 0 }},
		{"V8 negative assignment", func(c *Config) { c.MinAssignBattery = -1 }},
		{"V8 excessive assignment", func(c *Config) { c.MinAssignBattery = 101 }},
		{"V9 zero interval", func(c *Config) { c.NewJobEvery = 0 }},
		{"V9 negative interval", func(c *Config) { c.NewJobEvery = -1 }},
		{"V10 zero max jobs", func(c *Config) { c.MaxJobs = 0 }},
		{"V10 negative max jobs", func(c *Config) { c.MaxJobs = -1 }},
		{"V10 excessive max jobs", func(c *Config) { c.MaxJobs = 201 }},
		{"V10 negative initial jobs", func(c *Config) { c.InitialJobs = -1 }},
		{"V10 initial above max", func(c *Config) { c.InitialJobs = c.MaxJobs + 1 }},
		{"V11 one job cell", func(c *Config) { *c = cfgOneJobCell() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tc.change(&cfg)
			before := cfg.Clone()
			err := cfg.Validate()
			if !errors.Is(err, ErrInvalidConfig) || err.Error() == ErrInvalidConfig.Error() {
				t.Fatalf("Validate() = %v, want explained ErrInvalidConfig", err)
			}
			if again := cfg.Validate(); again == nil || again.Error() != err.Error() {
				t.Fatalf("nondeterministic validation: %v then %v", err, again)
			}
			if !reflect.DeepEqual(cfg, before) {
				t.Fatal("validation mutated invalid input")
			}
		})
	}
}

func TestValidateValid(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"defaults", func(c *Config) {}},
		{"robot on aisle", func(c *Config) { c.RobotStarts[0] = Point{4, 1} }},
		{"robots on stations", func(c *Config) { *c = cfgRobotOnStation() }},
		{"one station", func(c *Config) { c.Stations = c.Stations[:1] }},
		{"eight robots", func(c *Config) { c.RobotStarts = append(c.RobotStarts, Point{0, 11}, Point{2, 11}) }},
		{"maximum dimensions", func(c *Config) { c.Width, c.Height = 50, 50 }},
		{"aisle boundary columns", func(c *Config) { c.AisleColumns = []int{0, 19} }},
		{"full height aisle", func(c *Config) { c.AisleYMin, c.AisleYMax = 0, 11 }},
		{"minimum rates", func(c *Config) {
			c.MovingDrain, c.IdleDrainEvery, c.ChargeGain, c.UnresponsiveDelay = 1, 1, 1, 1
			c.NewJobEvery, c.MaxJobs, c.InitialJobs = 1, 1, 0
			c.LowBatteryInjectLevel, c.MinAssignBattery = 0, 1
		}},
		{"maximum bounded rates", func(c *Config) {
			c.MovingDrain, c.ChargeGain = 100, 100
			c.LowBatteryInjectLevel, c.MinAssignBattery = 99, 100
			c.MaxJobs, c.InitialJobs = 200, 200
		}},
		{"two job cells", func(c *Config) {
			*c = cfgOneJobCell()
			c.Walls = c.Walls[1:]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tc.change(&cfg)
			before := cfg.Clone()
			for attempt := 0; attempt < 2; attempt++ {
				if err := cfg.Validate(); err != nil {
					t.Fatalf("Validate() = %v", err)
				}
			}
			if !reflect.DeepEqual(cfg, before) {
				t.Fatal("validation mutated valid input")
			}
		})
	}
}

func TestValidateJobCellMinimum(t *testing.T) {
	cfg := cfgOneJobCell()
	if cells := cfg.JobCells(); !reflect.DeepEqual(cells, []Point{{3, 3}}) {
		t.Fatalf("fixture job cells = %v, want only (3,3)", cells)
	}
	err := cfg.Validate()
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "at least two eligible job cells") {
		t.Fatalf("expected V11 failure, got %v", err)
	}
	cfg.Walls = cfg.Walls[1:]
	if len(cfg.JobCells()) != 2 || cfg.Validate() != nil {
		t.Fatal("freeing one wall must satisfy V11 with all other rules unchanged")
	}
}
