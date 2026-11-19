package sim

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestWarehouseV2Starts(t *testing.T) {
	cfg := WarehouseV2Config()
	s := newTestSim(t, cfg, 42)
	state := s.Snapshot()
	if len(state.Robots) != 11 || len(state.Stations) != 4 {
		t.Fatalf("got %d robots and %d stations, want 11 and 4", len(state.Robots), len(state.Stations))
	}
	for i, station := range state.Stations {
		p := Point{station.X, station.Y}
		if station.ID != fmt.Sprintf("C%d", i+1) || p != cfg.Stations[i] || s.CellAt(p) != CellStation {
			t.Fatalf("station %d has unexpected startup state: %+v", i+1, station)
		}
	}
	jobCells, starts := cfg.JobCells(), cfg.robotStarts()
	for i, robot := range state.Robots {
		p := Point{robot.X, robot.Y}
		maintenance := i == 10
		wantType := CellParking
		if maintenance {
			wantType = CellMaintenanceBay
		}
		if robot.ID != fmt.Sprintf("R%d", i+1) || robot.Maintenance != maintenance || p != starts[i] || s.CellAt(p) != wantType || robot.Status != RobotIdle || robot.Battery != 100 {
			t.Fatalf("robot %d has unexpected startup state: %+v", i+1, robot)
		}
		if slices.Contains(jobCells, p) {
			t.Fatalf("bay %v is eligible for pickup or delivery", p)
		}
		for _, station := range cfg.Stations {
			route, err := s.PlanRoute(p, station)
			if err != nil || len(route) == 0 || route[len(route)-1] != station {
				t.Fatalf("no route from %s's bay to %v: %v", robot.ID, station, err)
			}
			for _, step := range route[:len(route)-1] {
				if !slices.Contains(jobCells, step) {
					t.Fatalf("route from %s to %v uses resource cell %v for transit", robot.ID, station, step)
				}
			}
		}
	}
	*cfg.MaintenanceBay = Point{0, 0}
	if s.CellAt(Point{9, 0}) != CellMaintenanceBay || *WarehouseV2Config().MaintenanceBay != (Point{9, 0}) {
		t.Fatal("simulator and warehouse constructor must own independent maintenance bay configuration")
	}
}

func TestValidateWarehouseV2(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{"missing transport", func(c *Config) { c.RobotStarts = c.RobotStarts[:9] }, "V2 requires 10 transport starts and 4 stations"},
		{"missing charger", func(c *Config) { c.Stations = c.Stations[:3] }, "V2 requires 10 transport starts and 4 stations"},
		{"duplicate parking", func(c *Config) { c.RobotStarts[0] = c.RobotStarts[1] }, "robot starts positions must be unique"},
		{"duplicate chargers", func(c *Config) { c.Stations[0] = c.Stations[1] }, "stations positions must be unique"},
		{"maintenance bay overlaps parking", func(c *Config) { *c.MaintenanceBay = c.RobotStarts[0] }, "robot starts positions must be unique"},
		{"maintenance bay out of bounds", func(c *Config) { *c.MaintenanceBay = Point{-1, 0} }, "in bounds"},
		{"parking on a wall", func(c *Config) { c.RobotStarts[0] = Point{1, 12} }, "on a wall"},
		{"parking on a charger", func(c *Config) { c.RobotStarts[0] = c.Stations[0] }, "overlaps an aisle or station"},
		{"parking on an aisle", func(c *Config) { c.RobotStarts[0] = Point{4, 2} }, "overlaps an aisle or station"},
		{"parking disconnects the warehouse", func(c *Config) { c.RobotStarts[0] = Point{0, 11} }, "job cells must share one traversable component"},
		{"parking in a corridor", func(c *Config) { c.RobotStarts[0] = Point{1, 11} }, "must have exactly one corridor entrance"},
		{"isolated parking", func(c *Config) { c.Walls = append(c.Walls, Point{0, 11}) }, "must have exactly one corridor entrance"},
		{"isolated maintenance bay", func(c *Config) { c.Walls = append(c.Walls, Point{9, 1}) }, "must have exactly one corridor entrance"},
		{"isolated charger", func(c *Config) { c.Walls = append(c.Walls, Point{0, 0}, Point{2, 0}, Point{1, 1}) }, "V12 station"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := WarehouseV2Config()
			tc.change(&cfg)
			assertWarehouseValidation(t, cfg, tc.want)
		})
	}
}

func TestWarehouseV2KeepsMaintenanceOutOfTransport(t *testing.T) {
	cfg := WarehouseV2Config()
	cfg.InitialJobs = 0
	s := newTestSim(t, cfg, 42)
	s.state.Jobs = []JobState{{ID: "J1", From: Point{9, 1}, To: Point{9, 2}, Status: JobPending}}
	s.Tick()
	if assigned := s.state.Jobs[0].AssignedRobot; assigned == "" || assigned == "R11" || s.state.Robots[10].JobID != "" {
		t.Fatalf("transport job assigned to %q with R11 job %q", assigned, s.state.Robots[10].JobID)
	}

	s = newTestSim(t, cfg, 42)
	s.state.Jobs = []JobState{{ID: "J1", From: Point{9, 1}, To: Point{9, 2}, Status: JobAssigned, AssignedRobot: "R1"}}
	source := &s.state.Robots[0]
	source.Status, source.JobID, source.Blocked, source.WaitTicks = RobotMoving, "J1", true, cfg.BlockedAfter
	if _, err := s.PreviewReassignment("J1", "R1", "R2"); err != nil {
		t.Fatalf("transport robot should be eligible for reassignment: %v", err)
	}
	if err := s.ReassignUnstartedJob("J1", "R1", "R11"); !errors.Is(err, ErrReassignment) {
		t.Fatalf("reassignment to R11 = %v, want ErrReassignment", err)
	}
}

func TestWarehouseConfig(t *testing.T) {
	cfg := WarehouseConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 20 || cfg.Height != 12 || !slices.Equal(cfg.AisleColumns, []int{4, 9, 14}) || cfg.AisleYMin != 2 || cfg.AisleYMax != 9 {
		t.Fatalf("unexpected warehouse dimensions or aisles: %+v", cfg)
	}
	var walls []Point
	aisles := make(map[int]int)
	for y := 0; y < cfg.Height; y++ {
		for x := 0; x < cfg.Width; x++ {
			p := Point{X: x, Y: y}
			wantWall := y >= 2 && y <= 9 && (x == 2 || x == 3 || x >= 5 && x <= 8 || x >= 10 && x <= 13 || x >= 15 && x <= 17)
			if wantWall {
				walls = append(walls, p)
			}
			if (cfg.CellTypeAt(p) == CellWall) != wantWall {
				t.Fatalf("wrong wall geometry at %v", p)
			}
			if cfg.CellTypeAt(p) == CellAisle {
				if y < 2 || y > 9 {
					t.Fatalf("aisle extends past warehouse bounds at %v", p)
				}
				aisles[x]++
			}
		}
	}
	if len(walls) != 104 || !slices.Equal(cfg.Walls, walls) || !reflect.DeepEqual(aisles, map[int]int{4: 8, 9: 8, 14: 8}) {
		t.Fatalf("walls = %d, aisles = %v", len(cfg.Walls), aisles)
	}
	cells := cfg.JobCells()
	if len(cells) != 134 || cells[0] != (Point{1, 0}) || cells[len(cells)-1] != (Point{19, 11}) {
		t.Fatalf("unexpected warehouse job cells: %v", cells)
	}
	// Warehouse enables AutoCharge; all remaining non-layout settings retain defaults.
	want := DefaultConfig()
	want.AisleYMin, want.AisleYMax, want.Walls = 2, 9, walls
	want.AutoCharge, want.Recovery = true, true
	if !reflect.DeepEqual(cfg, want) {
		t.Fatal("warehouse changed non-layout defaults")
	}
}

func TestWarehouseConfigOwnership(t *testing.T) {
	cfg, want := WarehouseConfig(), WarehouseConfig()
	cfg.AisleColumns[0] = 0
	cfg.Stations[0] = Point{1, 0}
	cfg.RobotStarts[0] = Point{2, 11}
	cfg.Walls[0] = Point{0, 1}
	if !reflect.DeepEqual(WarehouseConfig(), want) {
		t.Fatal("warehouse calls share mutable slices")
	}
}

func TestWarehouseSettingsPreserveDynamics(t *testing.T) {
	cfg := cfgRobotOnStation()
	cfg.AutoDispatch = false
	baseline := newTestSim(t, cfg, 42)
	cfg.AutoCharge, cfg.ChargeBelow = true, 100
	configured := newTestSim(t, cfg, 42)
	for _, s := range []*Sim{baseline, configured} {
		requireInjection(t, s.InjectLowBattery("R1"))
	}
	for tick := 1; tick <= 44; tick++ {
		baseline.Tick()
		configured.Tick()
		old, charged := baseline.state.Robots[0], configured.state.Robots[0]
		if old.Status != RobotIdle || old.Battery != 15-tick/20 {
			t.Fatalf("disabled AutoCharge changed Phase 1 idle behavior: %+v", old)
		}
		wantStatus := RobotCharging
		if tick == 44 {
			wantStatus = RobotIdle
		}
		if charged.Status != wantStatus || charged.Battery != min(100, 15+2*(tick-1)) || configured.state.Stations[0].ReservedBy != "" {
			t.Fatalf("enabled AutoCharge cycle changed at tick %d: %+v", tick, charged)
		}
		assertInvariants(t, configured, configured.cfg)
	}
}

func TestWarehouseReserveBatteryAffectsDispatch(t *testing.T) {
	for _, cfg := range []Config{DefaultConfig(), WarehouseConfig()} {
		cfg.AutoCharge = false // Isolate reserve feasibility from the later charging pass.
		cfg.Recovery = false
		for _, reserve := range []int{21, 22} {
			cfg.ReserveBattery = reserve
			s := dispatchSim(t, cfg, Point{1, 1}, Point{6, 1}, Point{10, 1})
			s.state.Robots[0].Battery = 30
			s.Tick()
			r, job := s.state.Robots[0], s.state.Jobs[0]
			if reserve == 21 {
				if r.JobID != "J1" || r.Battery != 29 || r.X != 2 || job.Status != JobAssigned {
					t.Fatalf("exact reserve boundary rejected: robot=%+v job=%+v", r, job)
				}
			} else if r.JobID != "" || r.Battery != 30 || r.X != 1 || job.Status != JobPending {
				t.Fatalf("higher reserve did not prevent dispatch: robot=%+v job=%+v", r, job)
			}
			assertInvariants(t, s, s.cfg)
		}
	}
}

func TestValidateConnectivity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
		want   string
	}{
		{name: "default"},
		{name: "warehouse", change: func(c *Config) { *c = WarehouseConfig() }},
		{name: "robots on connected stations", change: func(c *Config) { *c = cfgRobotOnStation() }},
		{name: "isolated job cell", change: func(c *Config) { c.Walls = []Point{{0, 4}, {1, 3}, {1, 5}, {2, 4}} }, want: "V12 eligible job cells"},
		{name: "diagonal contact is insufficient", change: func(c *Config) { c.Walls = []Point{{0, 2}, {1, 1}, {2, 0}} }, want: "V12 eligible job cells"},
		{name: "station without access", change: func(c *Config) { c.Walls = []Point{{1, 0}, {0, 1}} }, want: "V12 station"},
		{name: "occupied station without access", change: func(c *Config) {
			*c = cfgRobotOnStation()
			c.Walls = []Point{{1, 0}, {0, 1}}
		}, want: "V12 station"},
		{name: "station cannot bridge components", change: func(c *Config) {
			c.Stations = []Point{{1, 1}, {19, 0}}
			c.Walls = []Point{{0, 1}, {2, 0}}
		}, want: "V12 eligible job cells"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			if tc.change != nil {
				tc.change(&cfg)
			}
			assertWarehouseValidation(t, cfg, tc.want)
		})
	}
}

func assertWarehouseValidation(t *testing.T, cfg Config, want string) {
	t.Helper()
	before := cfg.Clone()
	for attempt := 0; attempt < 2; attempt++ {
		err := cfg.Validate()
		if want == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), want) {
			t.Fatalf("Validate() = %v, want ErrInvalidConfig containing %q", err, want)
		}
	}
	if !reflect.DeepEqual(cfg, before) {
		t.Fatal("validation mutated configuration")
	}
}
