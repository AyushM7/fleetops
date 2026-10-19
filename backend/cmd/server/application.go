package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/AyushM7/fleetops/backend/visitor"
)

func application(ctx context.Context) (http.Handler, func() error, error) {
	ticking, err := parseTickConfig(os.Getenv("FLEETOPS_AUTO_TICK"), os.Getenv("FLEETOPS_TICK_INTERVAL"))
	if err != nil {
		return nil, nil, err
	}
	if ticking.enabled {
		return nil, nil, errTickConfig
	}
	config, err := visitor.ConfigFromEnv()
	if err != nil {
		return nil, nil, err
	}
	app, err := visitor.New(ctx, config)
	if err != nil {
		return nil, nil, err
	}
	return app, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return app.Shutdown(ctx)
	}, nil
}
