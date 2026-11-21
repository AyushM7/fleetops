package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/AyushM7/fleetops/backend/sim"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("simrun", flag.ContinueOnError)
	flags.SetOutput(stderr)
	seed := flags.Int64("seed", 42, "random seed")
	ticks := flags.Int("ticks", 200, "number of ticks (0–100000)")
	layout := flags.String("layout", "default", "layout (default|warehouse|warehouse-v2)")
	repairTicks := flags.Int("repair-ticks", 0, "repair duration in ticks (0 disables V2 maintenance, omit for layout default)")
	var breakdowns []sim.ScheduledBreakdown
	flags.Func("breakdown", "schedule a V2 breakdown as TICK:ROBOT:TYPE (repeat in tick order)", func(value string) error {
		fields := strings.Split(value, ":")
		if len(fields) != 3 {
			return fmt.Errorf("breakdown must be TICK:ROBOT:TYPE")
		}
		tick, err := strconv.Atoi(fields[0])
		if err != nil {
			return fmt.Errorf("breakdown tick must be an integer")
		}
		breakdowns = append(breakdowns, sim.ScheduledBreakdown{Tick: tick, RobotID: fields[1], Type: sim.FaultType(fields[2])})
		return nil
	})
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "simrun: unexpected positional arguments")
		return 2
	}
	if *ticks < 0 || *ticks > 100000 {
		fmt.Fprintln(stderr, "simrun: ticks must be between 0 and 100000")
		return 2
	}
	cfg := sim.DefaultConfig()
	switch *layout {
	case "default":
	case "warehouse":
		cfg = sim.WarehouseConfig()
	case "warehouse-v2":
		cfg = sim.WarehouseV2Config()
	default:
		fmt.Fprintln(stderr, "simrun: layout must be default, warehouse, or warehouse-v2")
		return 2
	}
	cfg.ScheduledBreakdowns = breakdowns
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "repair-ticks" {
			cfg.MaintenanceRepairTicks = *repairTicks
		}
	})
	s, err := sim.New(cfg, *seed)
	if err != nil {
		fmt.Fprintf(stderr, "simrun: create simulator: %v\n", err)
		return 1
	}
	for i := 0; i < *ticks; i++ {
		s.Tick()
	}
	data, err := json.MarshalIndent(s.Snapshot(), "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "simrun: encode snapshot: %v\n", err)
		return 1
	}
	data = append(data, '\n')
	n, err := stdout.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		fmt.Fprintf(stderr, "simrun: write snapshot: %v\n", err)
		return 1
	}
	return 0
}
