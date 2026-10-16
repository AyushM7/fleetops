package main

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"
)

var errTickConfig = errors.New("invalid automatic ticking configuration: expected a boolean and a positive Go duration")

type tickConfig struct {
	enabled  bool
	interval time.Duration
}

func parseTickConfig(enabled, interval string) (tickConfig, error) {
	cfg := tickConfig{interval: 500 * time.Millisecond}
	var err error
	if enabled != "" {
		cfg.enabled, err = strconv.ParseBool(enabled)
		if err != nil {
			return tickConfig{}, errTickConfig
		}
	}
	if interval != "" {
		cfg.interval, err = time.ParseDuration(interval)
		if err != nil || cfg.interval <= 0 {
			return tickConfig{}, errTickConfig
		}
	}
	return cfg, nil
}

// startTicks owns one worker. Stop cancels and joins it before resources close;
// an in-flight service operation may finish, but no tick runs after stop returns.
func startTicks(ctx context.Context, tick func() error, cfg tickConfig) func() {
	if !cfg.enabled {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	ticker := time.NewTicker(cfg.interval)
	go func() {
		defer close(done)
		defer ticker.Stop()
		if err := runTicks(ctx, tick, ticker.C); err != nil {
			log.Print("automatic ticking stopped: fleet runtime unavailable")
		}
	}()
	return func() { cancel(); <-done }
}

// Timestamps only pace calls; they never enter simulator state. Slow operations
// can cause time.Ticker to drop pulses; there is no wall-clock catch-up mutation.
func runTicks(ctx context.Context, tick func() error, pulses <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, open := <-pulses:
			if !open || ctx.Err() != nil {
				return nil
			}
			if err := tick(); err != nil {
				return err
			}
		}
	}
}
