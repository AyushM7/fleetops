package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTickConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, interval string
		want                    tickConfig
		invalid                 bool
	}{
		{name: "default disabled", want: tickConfig{interval: 500 * time.Millisecond}},
		{name: "enabled default", enabled: "1", want: tickConfig{true, 500 * time.Millisecond}},
		{name: "custom", enabled: "true", interval: "20ms", want: tickConfig{true, 20 * time.Millisecond}},
		{name: "explicit disabled", enabled: "0", interval: "1s", want: tickConfig{false, time.Second}},
		{name: "invalid flag", enabled: "yes", invalid: true},
		{name: "invalid duration", interval: "fast", invalid: true},
		{name: "zero", interval: "0s", invalid: true},
		{name: "negative", interval: "-1s", invalid: true},
		{name: "overflow", interval: "999999999999999999999h", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTickConfig(tc.enabled, tc.interval)
			if tc.invalid {
				if !errors.Is(err, errTickConfig) {
					t.Fatal("invalid config accepted", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("config=%+v error=%v", got, err)
			}
		})
	}
}

func TestRunTicksCancellationAndFailure(t *testing.T) {
	t.Run("canceled queued pulses", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		pulses := make(chan time.Time, 1)
		pulses <- time.Now()
		if err := runTicks(ctx, func() error { t.Fatal("canceled worker ticked"); return nil }, pulses); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("tick failure stops loop", func(t *testing.T) {
		pulses := make(chan time.Time, 2)
		pulses <- time.Now()
		pulses <- time.Now()
		failure := errors.New("poisoned fixture")
		calls := 0
		err := runTicks(context.Background(), func() error { calls++; return failure }, pulses)
		if err != failure || calls != 1 {
			t.Fatal("worker retried failed tick", calls, err)
		}
	})
	t.Run("closed source", func(t *testing.T) {
		pulses := make(chan time.Time)
		close(pulses)
		if err := runTicks(context.Background(), func() error { t.Fatal("closed source ticked"); return nil }, pulses); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTickerDisabledAndStopsCleanly(t *testing.T) {
	stop := startTicks(context.Background(), func() error { t.Error("disabled ticker fired"); return nil }, tickConfig{})
	stop()
	stop()
	var calls atomic.Int32
	first := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop = startTicks(ctx, func() error {
		calls.Add(1)
		select {
		case first <- struct{}{}:
		default:
		}
		return nil
	}, tickConfig{true, time.Millisecond})
	defer stop()
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("enabled ticker did not fire")
	}
	cancel()
	stop()
	stop()
	frozen := calls.Load()
	time.Sleep(10 * time.Millisecond)
	if calls.Load() != frozen {
		t.Fatal("worker ticked after stop returned")
	}
}

func TestTickerStopJoinsInFlightOperation(t *testing.T) {
	entered, release, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	stop := startTicks(context.Background(), func() error { close(entered); <-release; return nil }, tickConfig{true, time.Millisecond})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("tick did not enter")
	}
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned while tick was in flight")
	case <-time.After(20 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not join worker")
	}
}

func TestApplicationRejectsInvalidTickConfiguration(t *testing.T) {
	t.Setenv("FLEETOPS_AUTO_TICK", "yes")
	_, _, err := application(context.Background())
	if !errors.Is(err, errTickConfig) {
		t.Fatal("invalid runtime config did not fail startup", err)
	}
}
