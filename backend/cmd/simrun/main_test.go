package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/AyushM7/fleetops/backend/sim"
)

func TestRunValidArguments(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		seed  int64
		ticks int
	}{
		{"defaults", nil, 42, 200},
		{"explicit", []string{"-seed", "-7", "-ticks", "5"}, -7, 5},
		{"zero ticks", []string{"-ticks=0"}, 42, 0},
		{"maximum ticks", []string{"-ticks=100000"}, 42, 100000},
		{"minimum seed", []string{"-seed=-9223372036854775808", "-ticks=0"}, -9223372036854775808, 0},
		{"maximum seed", []string{"-seed=9223372036854775807", "-ticks=0"}, 9223372036854775807, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr.String())
			}
			var state sim.State
			if err := json.Unmarshal(stdout.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if state.Tick != tc.ticks || state.Seed != tc.seed {
				t.Fatalf("tick/seed = %d/%d, want %d/%d", state.Tick, state.Seed, tc.ticks, tc.seed)
			}
			expected, err := sim.New(sim.DefaultConfig(), tc.seed)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tc.ticks; i++ {
				expected.Tick()
			}
			want, err := json.MarshalIndent(expected.Snapshot(), "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(stdout.Bytes(), append(want, '\n')) {
				t.Fatal("stdout differs from the requested indented snapshot")
			}
		})
	}
}

func TestRunInvalidArguments(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		message string
	}{
		{[]string{"-unknown"}, "flag provided but not defined"},
		{[]string{"-ticks"}, "flag needs an argument"},
		{[]string{"-seed"}, "flag needs an argument"},
		{[]string{"-ticks=abc"}, "invalid value"},
		{[]string{"-ticks=1.5"}, "invalid value"},
		{[]string{"-seed=abc"}, "invalid value"},
		{[]string{"-seed=9223372036854775808"}, "invalid value"},
		{[]string{"-seed=-9223372036854775809"}, "invalid value"},
		{[]string{"-ticks=-1"}, "ticks must be between"},
		{[]string{"-ticks=100001"}, "ticks must be between"},
		{[]string{"extra"}, "unexpected positional"},
		{[]string{"--", "extra"}, "unexpected positional"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.message) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunDeterministicAndIndependent(t *testing.T) {
	var first, second, stderr bytes.Buffer
	args := []string{"-seed=7", "-ticks=3"}
	if code := run(args, &first, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	if code := run(args, &second, &stderr); code != 0 || stderr.Len() != 0 || !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("repeated run produced different output")
	}
	var defaults bytes.Buffer
	if code := run(nil, &defaults, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatal(code, stderr.String())
	}
	var state sim.State
	if err := json.Unmarshal(defaults.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.Seed != 42 || state.Tick != 200 {
		t.Fatal("run retained flags from a previous invocation")
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	return len(p) - 1, nil
}

func TestRunWriterFailure(t *testing.T) {
	for _, tc := range []struct {
		writer  io.Writer
		message string
	}{
		{failingWriter{errors.New("writer failed")}, "writer failed"},
		{failingWriter{}, "short write"},
	} {
		var stderr bytes.Buffer
		if code := run([]string{"-ticks=0"}, tc.writer, &stderr); code != 1 || !strings.Contains(stderr.String(), tc.message) {
			t.Fatalf("exit %d, stderr %q", code, stderr.String())
		}
	}
}
