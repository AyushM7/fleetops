//go:build fleetops_test

package fleet_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/graphql/model"
)

// Explicit opt-in export uses the same trusted factories as the browser demo.
func TestDemo08ExportObservations(t *testing.T) {
	path := os.Getenv("FLEETOPS_DEMO08_EXPORT")
	if path == "" {
		t.Skip("set FLEETOPS_DEMO08_EXPORT to export read-only demo observations")
	}
	type observation struct {
		ID        string            `json:"id"`
		RuntimeID string            `json:"runtimeId"`
		SHA256    string            `json:"sha256"`
		Source    string            `json:"source"`
		Snapshot  *model.FleetState `json:"snapshot"`
	}
	rows := []observation{}
	for _, id := range []string{"blocked-reassignment", "blocked-no-candidate", "routine-charging", "healthy-operations"} {
		s, err := fleet.NewDemoScenarioService(id, &db.Pool{}, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		state, err := (&graphql.Resolver{Service: s}).Query().FleetState(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(raw)
		if state.Poisoned {
			t.Fatal("unexpected poisoned export")
		}
		rows = append(rows, observation{id, fleet.DemoRuntimeID(s), hex.EncodeToString(hash[:]), "backend/fleet/demo_export_test.go NewDemoScenarioService; seed 42; read-only; no proposals or execution", state})
	}
	if len(rows[0].Snapshot.ReassignmentCandidates) == 0 || len(rows[1].Snapshot.ReassignmentCandidates) != 0 || rows[2].Snapshot.Tick != 1000 || rows[3].Snapshot.Tick != 0 {
		t.Fatal("preset export changed")
	}
	raw, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("exported four actual DEMO-09 Go observations; no mutations, provider calls or database access")
}
