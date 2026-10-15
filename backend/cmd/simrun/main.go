package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

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
	s, err := sim.New(sim.DefaultConfig(), *seed)
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
