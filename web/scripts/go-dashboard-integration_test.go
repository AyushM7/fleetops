//go:build fleetops_test

package fleet_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
	"github.com/jackc/pgx/v5"
)

// This file is copied into an ephemeral backend test checkout by the web harness.
// It never changes repository backend files or introduces a production endpoint.
type dashboardFixture struct {
	*fleet.Service
	layout fleet.LayoutView
}

func (s dashboardFixture) ReadState(ctx context.Context) (fleet.StateView, error) {
	view, err := s.Service.ReadState(ctx)
	// The existing P4-03 private fixture bypasses fleet.New's layout constructor.
	// Supply its actual simulator cells; production New already supplies layout.
	if view.Layout.Width == 0 {
		view.Layout = s.layout
	}
	return view, err
}

func dashboardPool(t *testing.T) *db.Pool {
	t.Helper()
	cfg := db.DefaultConfig(os.Getenv("FLEETOPS_TEST_DATABASE_URL"))
	admin, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	cfg.Schema = "fleetops_p404_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{cfg.Schema}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	pool, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}

func dashboardCredentials(t *testing.T) (*auth.Authenticator, string) {
	t.Helper()
	credentials := []auth.Credential{}
	tokens := map[string]string{}
	for _, name := range []string{"viewer", "proposer", "operator", "self"} {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			t.Fatal(err)
		}
		token := hex.EncodeToString(random[:])
		tokens[name] = token
		hash := sha256.Sum256([]byte(token))
		role, subject := auth.Role(name), name
		if name == "self" {
			role, subject = auth.Operator, "proposer"
		}
		credentials = append(credentials, auth.Credential{Subject: subject, Role: role, SHA256: hex.EncodeToString(hash[:])})
	}
	raw, _ := json.Marshal(credentials)
	a, err := auth.New(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	tokenJSON, _ := json.Marshal(tokens)
	return a, string(tokenJSON)
}

func TestDashboardHTTPIntegration(t *testing.T) {
	for _, profile := range []string{"charging", "reassignment", "agent"} {
		t.Run(profile, func(t *testing.T) {
			var now atomic.Int64
			now.Store(time.Now().UTC().UnixNano())
			clock := func() time.Time { return time.Unix(0, now.Load()).UTC() }
			var service *fleet.Service
			layout := fleet.LayoutView{}
			if profile == "reassignment" || profile == "agent" {
				service = fleet.NewReassignmentTestService(t, clock)
				simulator, err := sim.NewReassignmentTestFixture()
				if err != nil {
					t.Fatal(err)
				}
				layout.Width, layout.Height = 8, 8
				for y := 0; y < 8; y++ {
					for x := 0; x < 8; x++ {
						layout.Cells = append(layout.Cells, fleet.CellView{X: x, Y: y, Kind: string(simulator.CellAt(sim.Point{X: x, Y: y}))})
					}
				}
			} else {
				cfg := sim.DefaultConfig()
				cfg.InitialJobs, cfg.AutoDispatch = 0, false
				cfg.RobotStarts[0] = cfg.Stations[0]
				var err error
				service, err = fleet.New(cfg, 42, dashboardPool(t), clock, func(ctx context.Context, identity proposal.OperatorIdentity) bool {
					p, ok := auth.FromContext(ctx)
					return ok && p.Role == auth.Operator && p.Subject == identity.Subject
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			a, tokens := dashboardCredentials(t)
			server := httptest.NewServer(graphql.NewHandler(dashboardFixture{Service: service, layout: layout}, a))
			defer server.Close()
			script := "check-backend.mjs"
			if profile == "agent" {
				script = "check-agent-backend.mjs"
			}
			cmd := exec.Command("node", filepath.Join(os.Getenv("FLEETOPS_P404_WEB_ROOT"), "scripts", script))
			cmd.Env = append(os.Environ(), "FLEETOPS_P404_URL="+server.URL, "FLEETOPS_P404_TOKENS="+tokens, "FLEETOPS_P404_PROFILE="+profile)
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(output)
			for scanner.Scan() {
				switch scanner.Text() {
				case "ADVANCE":
					if err := service.Tick(); err != nil {
						t.Error(err)
					}
					_, _ = fmt.Fprintln(input, "OK")
				case "EXPIRE":
					now.Add(int64(2 * time.Minute))
					_, _ = fmt.Fprintln(input, "OK")
				default:
					t.Log(scanner.Text())
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal("frontend HTTP contract:", err)
			}
		})
	}
}
