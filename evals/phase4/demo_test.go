//go:build fleetops_test

package fleet_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

// Compiled only in a temporary test checkout alongside P4-04's test helpers.
type demoAudit struct {
	Attempt, Subject, Reason string
	Accepted                 bool
}
type demoEvidence struct {
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

func TestPhase4AcceptanceDemo(t *testing.T) {
	if os.Getenv("FLEETOPS_P405_SUBMIT") != "1" {
		t.Fatal("explicit -Submit is required")
	}
	root, runDir := os.Getenv("FLEETOPS_P405_ROOT"), os.Getenv("FLEETOPS_P405_RUN_DIR")
	main := fleet.NewReassignmentTestService(t, time.Now)
	baseline := fleet.NewReassignmentTestService(t, time.Now)
	initial := main.Snapshot()
	if !reflect.DeepEqual(initial, baseline.Snapshot()) {
		t.Fatal("initial snapshots differ")
	}
	for range 200 {
		if err := baseline.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	if baseline.Snapshot().Jobs[0].Status != sim.JobAssigned || !baseline.Snapshot().Robots[0].Blocked {
		t.Fatal("baseline unexpectedly recovered")
	}
	view, err := main.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range view.ActiveIncidents {
		if i.RobotID == "R1" && i.Type == "traffic_blocked" && i.Severity == "high" {
			found = true
		}
	}
	if !found {
		t.Fatal("Go did not detect the seeded blocked condition")
	}
	simulator, err := sim.NewReassignmentTestFixture()
	if err != nil {
		t.Fatal(err)
	}
	layout := fleet.LayoutView{Width: 8, Height: 8}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			layout.Cells = append(layout.Cells, fleet.CellView{X: x, Y: y, Kind: string(simulator.CellAt(sim.Point{X: x, Y: y}))})
		}
	}
	a, tokenJSON := dashboardCredentials(t)
	var tokens map[string]string
	if err := json.Unmarshal([]byte(tokenJSON), &tokens); err != nil {
		t.Fatal(err)
	}
	backendHandler := graphql.NewHandler(dashboardFixture{Service: main, layout: layout}, a)
	mux := http.NewServeMux()
	mux.Handle("/graphql", backendHandler)

	server := httptest.NewServer(mux)
	defer server.Close()
	if os.Getenv("FLEETOPS_LOCAL_DEMO") != "1" {
		t.Fatal("explicit -LocalDemo is required")
	}
	gateway, err := newDemoGateway(backendHandler, tokens["operator"], filepath.Join(root, "web", "dist"))
	if err != nil {
		t.Fatal(err)
	}
	browser := httptest.NewUnstartedServer(gateway)
	gateway.origin = "http://" + browser.Listener.Addr().String()
	browser.Config.ReadHeaderTimeout = 5 * time.Second
	browser.Config.IdleTimeout = 30 * time.Second
	browser.Config.MaxHeaderBytes = 16384
	browser.Start()
	defer browser.Close()
	defer gateway.close()
	privateFile := filepath.Join(runDir, "session.json")
	saveDemoJSON(t, privateFile, map[string]string{"url": browser.URL})
	defer os.Remove(privateFile)
	cmd := exec.Command("node", filepath.Join(root, "agent", "scripts", "phase4-demo-reasoning.mjs"), "--submit")
	cmd.Env = append(os.Environ(), "FLEETOPS_P405_URL="+server.URL+"/graphql", "FLEETOPS_P405_PROPOSER_TOKEN="+tokens["proposer"], "FLEETOPS_P405_REASONING_FILE="+filepath.Join(runDir, "reasoning.json"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("local Mastra test adapter failed: %v", err)
	}
	var submitted struct {
		ProposalID               string
		LocalCalls, PaidRequests int
	}
	if err := json.Unmarshal(output, &submitted); err != nil || submitted.ProposalID == "" || submitted.LocalCalls != 1 || submitted.PaidRequests != 0 {
		t.Fatal("invalid adapter result", err)
	}
	if !reflect.DeepEqual(initial, main.Snapshot()) {
		t.Fatal("submission changed simulator")
	}
	pool, schema := demoAuditPool(t, submitted.ProposalID)
	beforeAudit := demoAudits(t, pool, submitted.ProposalID)
	if len(beforeAudit) != 1 || beforeAudit[0].Attempt != "create" || !beforeAudit[0].Accepted || beforeAudit[0].Subject != "proposer" {
		t.Fatal("unexpected preapproval audit", beforeAudit)
	}
	evidence := demoEvidence{ModelMode: "deterministic local Mastra adapter; NOT live LLM", ProposalID: submitted.ProposalID, AuditSchema: schema, NoExecutionBeforeApproval: true, Initial: initial, Baseline: baseline.Snapshot(), Audit: beforeAudit}
	evidencePath := filepath.Join(runDir, "evidence.json")
	saveDemoJSON(t, evidencePath, evidence)
	if os.Getenv("FLEETOPS_P405_CHECK_ONLY") == "1" {
		evidence.CheckOnly = true
		saveDemoJSON(t, evidencePath, evidence)
		t.Log("PASS: real Go incident, identical blocked baseline, local Mastra adapter, opted-in pending proposal, committed create audit, no execution; human approval NOT tested")
		return
	}
	fmt.Printf("READY: %s · proposal %s · deterministic local adapter, NOT live LLM\n", browser.URL, submitted.ProposalID)
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(runDir, "stop")); err == nil {
			return
		}
		if _, err := os.Stat(filepath.Join(runDir, "outage")); err == nil {
			browser.Close()
		}
		row, err := main.ReadProposal(context.Background(), submitted.ProposalID)
		if err != nil {
			t.Fatal(err)
		}
		if row != nil && row.Proposal.State == proposal.Approved && row.Execution == fleet.Succeeded {
			evidence.OperatorDecisionObserved = true
			evidence.Decision = "approved"
			break
		}
		if row != nil && (row.Proposal.State == proposal.Rejected || row.Proposal.State == proposal.Expired) {
			evidence.Decision = string(row.Proposal.State)
			evidence.OperatorDecisionObserved = row.Proposal.State == proposal.Rejected
			evidence.Audit = demoAudits(t, pool, submitted.ProposalID)
			saveDemoJSON(t, evidencePath, evidence)
			fmt.Println("REVIEW CLOSED: rejected or expired; no recovery execution. Create stop file to shut down.")
			waitDemoStop(runDir, deadline, browser.Close)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !evidence.OperatorDecisionObserved {
		t.Fatal("human approval not observed within 20 minutes")
	}
	evidence.Progress = append(evidence.Progress, main.Snapshot())
	for range 3 {
		time.Sleep(2 * time.Second)
		if err := main.Tick(); err != nil {
			t.Fatal(err)
		}
		evidence.Progress = append(evidence.Progress, main.Snapshot())
	}
	for range 197 {
		if err := main.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	evidence.Final = main.Snapshot()
	evidence.Audit = demoAudits(t, pool, submitted.ProposalID)
	if evidence.Final.Tick != evidence.Baseline.Tick || evidence.Final.Jobs[0].Status != sim.JobDone || evidence.Final.Jobs[0].AssignedRobot != "R3" {
		t.Fatal("recovery did not beat identical baseline")
	}
	approved, executed := false, false
	for _, a := range evidence.Audit {
		if a.Accepted && a.Subject == "operator" {
			approved = approved || a.Attempt == "approve"
			executed = executed || a.Attempt == "execute"
		}
	}
	if !approved || !executed {
		t.Fatal("committed independent operator approval/execution audits missing")
	}
	saveDemoJSON(t, evidencePath, evidence)
	fmt.Println("RECOVERY VERIFIED: R3 delivered J1; baseline remains assigned/blocked at tick 242; committed create/approve/execute audits found. Create stop file to shut down.")
	waitDemoStop(runDir, deadline, browser.Close)
}

func waitDemoStop(runDir string, deadline time.Time, closeServer func()) {
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(runDir, "stop")); err == nil {
			return
		}
		if _, err := os.Stat(filepath.Join(runDir, "outage")); err == nil {
			closeServer()
		}
		time.Sleep(250 * time.Millisecond)
	}
}
