package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	api "github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/graphql/model"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestRuntimeGraphQLFreshSnapshotsAndJobCompletion(t *testing.T) {
	// Dynamics and reads do not access PostgreSQL; no proposal writes are attempted.
	service, err := fleet.New(sim.DefaultConfig(), 42, &db.Pool{}, time.Now, func(context.Context, proposal.OperatorIdentity) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(random[:])
	digest := sha256.Sum256([]byte(token))
	config, _ := json.Marshal([]auth.Credential{{Subject: "runtime-reader", Role: auth.Viewer, SHA256: hex.EncodeToString(digest[:])}})
	credentials, err := auth.New(string(config))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.NewHandler(service, credentials))
	defer server.Close()
	query := func() *model.FleetState {
		t.Helper()
		request, err := http.NewRequest("POST", server.URL, strings.NewReader(`{"query":"{fleetState{layout{width height cells{x y kind}} tick robots{id x y status jobId}}}"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result struct {
			Data   struct{ FleetState *model.FleetState }
			Errors []any
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || len(result.Errors) > 0 || result.Data.FleetState == nil {
			t.Fatal("snapshot query failed")
		}
		return result.Data.FleetState
	}
	initial := query()
	if initial.Layout.Width != 20 || initial.Layout.Height != 12 || len(initial.Layout.Cells) != 240 || initial.Layout.Cells[0].Kind != "station" {
		t.Fatal("GraphQL layout differs from simulator configuration")
	}
	ctx, cancel := context.WithCancel(context.Background())
	pulses := make(chan time.Time)
	advanced := make(chan error, 1)
	finished := make(chan error, 1)
	go func() {
		finished <- runTicks(ctx, func() error { err := service.Tick(); advanced <- err; return err }, pulses)
	}()
	defer func() {
		cancel()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	for tick := 1; tick <= 100; tick++ {
		pulses <- time.Unix(int64(tick), 0)
		if err := <-advanced; err != nil {
			t.Fatal(err)
		}
		if tick != 1 && tick != 100 {
			continue
		}
		current := query()
		snapshot := service.Snapshot()
		if current.Tick != tick {
			t.Fatalf("query returned stale tick %d, want %d", current.Tick, tick)
		}
		for i, robot := range current.Robots {
			want := snapshot.Robots[i]
			if robot.ID != want.ID || robot.X != want.X || robot.Y != want.Y || robot.JobID != want.JobID || robot.Status != string(want.Status) {
				t.Fatal("query differs from live snapshot")
			}
		}
		if tick == 1 && current.Robots[0].X == initial.Robots[0].X && current.Robots[0].Y == initial.Robots[0].Y {
			t.Fatal("robot did not move")
		}
	}
	completed := 0
	for _, job := range service.Snapshot().Jobs {
		if job.Status == sim.JobDone {
			completed++
		}
	}
	if completed == 0 {
		t.Fatal("no jobs completed")
	}
	t.Logf("live snapshot tick=100 completedJobs=%d", completed)
}
