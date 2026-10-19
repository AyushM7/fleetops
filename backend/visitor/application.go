package visitor

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
)

type Application struct {
	gateway  *visitorGateway
	admin    *db.Pool
	shutdown sync.Once
	stopped  chan struct{}
	closeErr error
}

// New is the normal server's public path. Config accepts no service, clock,
// proposer, simulator, archive callback or authorization overrides.
func New(ctx context.Context, config Config) (*Application, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(config.EvidenceDir, 0700); err != nil {
		return nil, errors.New("private evidence directory unavailable")
	}
	assets, assetsErr := filepath.EvalSymlinks(config.AssetsDir)
	evidence, evidenceErr := filepath.EvalSymlinks(config.EvidenceDir)
	if assetsErr != nil || evidenceErr != nil {
		return nil, errors.New("visitor directories unavailable")
	}
	config.AssetsDir, config.EvidenceDir = assets, evidence
	if err := config.validate(); err != nil {
		return nil, err
	}
	if info, err := os.Stat(config.EvidenceDir); err != nil || info.Mode().Perm()&0077 != 0 && config.Mode == "production" {
		return nil, errors.New("evidence directory must be private")
	}
	if err := storageReady(config.EvidenceDir); err != nil {
		return nil, err
	}
	cfg := config.Database
	cfg.MaxConns = 2
	admin, err := db.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var retained int
	err = admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE left(nspname,17)='fleetops_visitor_' AND nspowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)`).Scan(&retained)
	if err != nil || retained != 0 {
		admin.Close()
		return nil, errors.New("visitor database reconciliation required")
	}
	factory := visitorFactory{admin: admin, config: cfg, evidenceDir: config.EvidenceDir}
	manager := newVisitorManager(factory.create)
	manager.admit = func(ctx context.Context) error {
		if err := storageReady(config.EvidenceDir); err != nil {
			return err
		}
		return admin.Ping(ctx)
	}
	gateway, err := newVisitorGateway(manager, config)
	if err != nil {
		admin.Close()
		return nil, err
	}
	slog.Info("visitor_application_ready", "capacity", visitorLimit, "mode", config.Mode)
	app := &Application{gateway: gateway, admin: admin, stopped: make(chan struct{})}
	go func() {
		select {
		case <-app.stopped:
			return
		case <-ctx.Done():
			deadline, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := app.Shutdown(deadline); err != nil {
				slog.Error("visitor_shutdown_incomplete", "reconciliation_required", true)
			}
		}
	}()
	return app, nil
}

func (a *Application) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.gateway.ServeHTTP(w, r) }

// Shutdown revokes admission and all sessions before draining. On timeout it
// never drops a schema to force progress; bounded cleanup can finish in flight.
func (a *Application) Shutdown(ctx context.Context) error {
	a.shutdown.Do(func() {
		a.gateway.manager.revoke()
		go func() {
			a.closeErr = a.gateway.manager.close()
			a.admin.Close()
			close(a.stopped)
		}()
	})
	select {
	case <-ctx.Done():
		return errors.New("visitor shutdown incomplete; evidence reconciliation required")
	case <-a.stopped:
		if a.closeErr != nil {
			return errors.New("visitor cleanup incomplete; inspect archival evidence")
		}
		return nil
	}
}
