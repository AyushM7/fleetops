//go:build fleetops_test

package fleet_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/sim"
)

// Compiled only in a temporary test checkout alongside P4-04's test helpers.
type demoAudit struct {
	Attempt, Subject, Reason string
	Accepted                 bool
}
type demoEvidence struct {
	ScenarioID                string
	ModelMode                 string
	Decision                  string
	ProposalID                string
	AuditSchema               string
	NoExecutionBeforeApproval bool
	OperatorDecisionObserved  bool
	CheckOnly                 bool
	Initial, Baseline, Final  sim.State
	Progress                  []sim.State
	Audit                     []demoAudit
}

func saveDemoJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func demoAuditPool(t *testing.T, id string) (*db.Pool, string) {
	t.Helper()
	cfg := db.DefaultConfig(os.Getenv("FLEETOPS_TEST_DATABASE_URL"))
	admin, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var schemas []string
	var raw []byte
	if err := admin.QueryRow(context.Background(), `SELECT coalesce(jsonb_agg(nspname),'[]'::jsonb) FROM pg_namespace WHERE nspname LIKE 'fleetops_test_%'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &schemas); err != nil {
		t.Fatal(err)
	}
	for _, schema := range schemas {
		cfg.Schema = schema
		pool, err := db.Open(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		err = pool.QueryRow(context.Background(), `SELECT count(*) FROM fleet_proposals WHERE id=$1`, id).Scan(&count)
		if err == nil && count == 1 {
			t.Cleanup(pool.Close)
			return pool, schema
		}
		pool.Close()
	}
	t.Fatal("proposal audit schema not found")
	return nil, ""
}

func demoAudits(t *testing.T, pool *db.Pool, id string) []demoAudit {
	t.Helper()
	result := []demoAudit{}
	var raw []byte
	err := pool.QueryRow(context.Background(), `SELECT coalesce(jsonb_agg(jsonb_build_object('Attempt',attempt,'Subject',operator_subject,'Accepted',accepted,'Reason',reason) ORDER BY id),'[]'::jsonb) FROM fleet_audit WHERE proposal_id=$1`, id).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
