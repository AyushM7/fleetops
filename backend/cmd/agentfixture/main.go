//go:build fleetops_test

// This loopback-only executable preserves the legacy agent contract fixture.
// Ordinary server/image builds exclude it; it is never a production mode.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

func fixtureAddress() (string, error) {
	if os.Getenv("FLEETOPS_AGENT_FIXTURE_ENABLE") != "1" {
		return "", errors.New("explicit test fixture opt-in required")
	}
	port, err := strconv.Atoi(os.Getenv("PORT"))
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("valid fixture PORT required")
	}
	return "127.0.0.1:" + strconv.Itoa(port), nil
}

func fixture(ctx context.Context) (http.Handler, func(), error) {
	authenticator, err := auth.New(os.Getenv("FLEETOPS_AUTH_TOKENS"))
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
	cfg.RobotStarts[0] = cfg.Stations[0]
	cfg.InitialJobs = 0
	cfg.AutoDispatch = false
	service, err := fleet.New(cfg, 42, pool, time.Now, func(ctx context.Context, id proposal.OperatorIdentity) bool {
		p, ok := auth.FromContext(ctx)
		return ok && p.Role == auth.Operator && p.Subject == id.Subject
	})
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/graphql", graphql.NewHandler(service, authenticator))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(200)
	})
	return mux, pool.Close, nil
}

func run(ctx context.Context) error {
	address, err := fixtureAddress()
	if err != nil {
		return err
	}
	handler, cleanup, err := fixture(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	stopped := make(chan error, 1)
	go func() { stopped <- server.ListenAndServe() }()
	select {
	case err := <-stopped:
		return err
	case <-ctx.Done():
	}
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(deadline); err != nil {
		server.Close()
		return err
	}
	if err := <-stopped; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if run(ctx) != nil {
		os.Exit(1)
	}
}
