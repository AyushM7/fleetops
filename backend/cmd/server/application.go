package main

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	api "github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

func application(ctx context.Context) (http.Handler, func(), error) {
	ticking, err := parseTickConfig(os.Getenv("FLEETOPS_AUTO_TICK"), os.Getenv("FLEETOPS_TICK_INTERVAL"))
	if err != nil {
		return nil, nil, err
	}
	credentials, err := auth.New(os.Getenv("FLEETOPS_AUTH_TOKENS"))
	if err != nil {
		return nil, nil, err
	}
	config, err := db.ConfigFromEnv()
	if err != nil {
		return nil, nil, err
	}
	pool, err := db.Open(ctx, config)
	if err != nil {
		return nil, nil, err
	}
	if err := pool.Migrate(ctx); err != nil {
		pool.Close()
		return nil, nil, err
	}
	cfg := sim.DefaultConfig()
	// An explicit demo profile places an idle robot at S1, without changing simulator defaults.
	if os.Getenv("FLEETOPS_CHARGING_DEMO") == "1" {
		cfg.RobotStarts[0] = cfg.Stations[0]
		cfg.InitialJobs = 0
		cfg.AutoDispatch = false
	}
	service, err := fleet.New(cfg, 42, pool, time.Now, func(ctx context.Context, operator proposal.OperatorIdentity) bool {
		p, ok := auth.FromContext(ctx)
		return ok && p.Role == auth.Operator && p.Subject == operator.Subject
	})
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/healthz", newHandler())
	mux.Handle("/graphql", api.NewHandler(service, credentials))
	stopTicks := startTicks(ctx, service.Tick, ticking)
	return mux, func() { stopTicks(); pool.Close() }, nil
}
