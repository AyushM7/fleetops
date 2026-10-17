//go:build fleetops_test

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/AyushM7/fleetops/backend/proposal"
)

type request struct {
	ID          string  `json:"id"`
	InitialHash string  `json:"initialHash"`
	Action      *action `json:"action"`
}

func runRequests(requests []request, decision proposal.Decision) ([]experiment, error) {
	if len(requests) != len(scenarios()) {
		return nil, fmt.Errorf("one recommendation per scenario required")
	}
	results := []experiment{}
	for _, spec := range scenarios() {
		var matching []request
		for _, r := range requests {
			if r.ID == spec.ID {
				matching = append(matching, r)
			}
		}
		if len(matching) != 1 {
			return nil, fmt.Errorf("missing or duplicate scenario %s", spec.ID)
		}
		if matching[0].InitialHash == "" {
			return nil, fmt.Errorf("initial observation hash required")
		}
		r, err := runExperiment(spec, matching[0].Action, decision, matching[0].InitialHash)
		if err != nil {
			return nil, err
		}
		if r.InitialHash != matching[0].InitialHash {
			return nil, fmt.Errorf("stale observation hash for %s", spec.ID)
		}
		results = append(results, r)
	}
	return results, nil
}
func command(args []string, input io.Reader, output io.Writer) error {
	encoder := json.NewEncoder(output)
	if len(args) == 1 && args[0] == "observe" {
		observations, err := observeScenarios()
		if err != nil {
			return err
		}
		return encoder.Encode(observations)
	}
	if len(args) != 3 || args[0] != "run" || args[1] != "--test-operator" || (args[2] != "approve" && args[2] != "reject") {
		return fmt.Errorf("use observe, or run --test-operator approve|reject (isolated test workflow only)")
	}
	var requests []request
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&requests); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("extra input after recommendations")
	}
	results, err := runRequests(requests, proposal.Decision(args[2]))
	if err != nil {
		return err
	}
	return encoder.Encode(results)
}
func main() {
	if err := command(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
