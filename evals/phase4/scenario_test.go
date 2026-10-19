//go:build fleetops_test

package fleet_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/graphql/model"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

type demoFactory struct {
	pool                             *db.Pool
	auth                             *auth.Authenticator
	tokens                           map[string]string
	root, evidenceDir                string
	proposerSubject, operatorSubject string
	proposeRuntime                   func(context.Context, http.Handler, string) (string, error)
	modelMode                        string
	proposalSource                   demoProposalSource
}

func (f demoFactory) subject(role string) string {
	if role == "proposer" && f.proposerSubject != "" {
		return f.proposerSubject
	}
	if role == "operator" && f.operatorSubject != "" {
		return f.operatorSubject
	}
	return role
}

type demoScenario struct {
	factory                    demoFactory
	service                    *fleet.Service
	generation, runtimeID, dir string
	evidence                   demoEvidence
	nextTick                   time.Time
}

func (f demoFactory) create(ctx context.Context) (*demoRun, error) {
	return f.createScenario(ctx, "blocked-reassignment")
}

func (f demoFactory) createScenario(ctx context.Context, id string) (*demoRun, error) {
	scenario, err := f.prepareScenario(id)
	if err != nil {
		return nil, err
	}
	handler := graphql.NewHandler(scenario.service, f.auth)
	if err := scenario.prepareProposal(ctx, handler); err != nil {
		return nil, err
	}
	if err := scenario.capture(ctx); err != nil {
		return nil, err
	}
	observation, err := (&graphql.Resolver{Service: scenario.service}).Query().FleetState(ctx)
	if err != nil {
		return nil, err
	}
	return &demoRun{scenarioID: id, generation: scenario.generation, handler: handler, capture: scenario.capture, advance: scenario.advance,
		inspect: scenario.inspect, step: func(context.Context) error { return scenario.service.Tick() },
		evidence: &demoExplanation{Generation: scenario.generation, RuntimeID: scenario.runtimeID, ScenarioID: id,
			ProposalID: scenario.evidence.ProposalID, ProposalSource: f.source(scenario.evidence.ProposalID), Observation: observation}}, nil
}

func (f demoFactory) source(proposalID string) demoProposalSource {
	if proposalID == "" {
		return demoSourceNone
	}
	if f.proposalSource == "" && f.proposeRuntime == nil {
		return demoSourceMastra
	}
	switch f.proposalSource {
	case demoSourceGoScripted, demoSourceMastra, demoSourceRecorded, demoSourceLive:
		return f.proposalSource
	default:
		return demoSourceUnknown
	}
}

func (f demoFactory) prepareScenario(id string) (*demoScenario, error) {
	if !allowedScenario(id) {
		return nil, errors.New("unsupported scenario")
	}
	service, err := fleet.NewDemoScenarioService(id, f.pool, time.Now)
	if err != nil {
		return nil, err
	}
	evidence := demoEvidence{ScenarioID: id, ModelMode: "deterministic local Mastra adapter; NOT live LLM", NoExecutionBeforeApproval: true, Initial: service.Snapshot()}
	if id == "blocked-reassignment" {
		evidence, err = demoBaseline(service.Snapshot())
		if err != nil {
			return nil, err
		}
		evidence.ScenarioID = id
	}
	if f.modelMode != "" {
		evidence.ModelMode = f.modelMode
	}
	scenario := &demoScenario{factory: f, service: service, generation: demoRandom(), evidence: evidence}
	scenario.dir = filepath.Join(f.evidenceDir, scenario.generation)
	if err := os.Mkdir(scenario.dir, 0700); err != nil {
		return nil, err
	}
	scenario.runtimeID = fleet.DemoRuntimeID(service)
	return scenario, nil
}

func (s *demoScenario) prepareProposal(ctx context.Context, handler http.Handler) error {
	proposalID := ""
	id := s.evidence.ScenarioID
	var err error
	if id == "blocked-reassignment" || id == "routine-charging" {
		proposalID, err = s.factory.propose(ctx, handler, s.dir)
		if err != nil {
			return err
		}
	}
	if !reflect.DeepEqual(s.evidence.Initial, s.service.Snapshot()) {
		return errors.New("proposal submission mutated simulator")
	}
	s.evidence.ProposalID = proposalID
	view, err := s.service.ReadState(ctx)
	if err != nil {
		return err
	}
	if id == "blocked-no-candidate" && len(view.ReassignmentCandidates) != 0 {
		return errors.New("no-candidate preset admitted a transfer")
	}
	if proposalID != "" {
		return s.verifySubmission(ctx)
	}
	return nil
}

func (s *demoScenario) verifySubmission(ctx context.Context) error {
	var runtime string
	if err := s.factory.pool.QueryRow(ctx, `SELECT runtime_id FROM fleet_proposals WHERE id=$1 AND state='pending' AND execution='none' AND submitted_by=$2`, s.evidence.ProposalID, s.factory.subject("proposer")).Scan(&runtime); err != nil {
		return err
	}
	var audits int
	if err := s.factory.pool.QueryRow(ctx, `SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='create' AND accepted AND operator_subject=$2`, s.evidence.ProposalID, s.factory.subject("proposer")).Scan(&audits); err != nil || audits != 1 || runtime != s.runtimeID {
		return errors.New("committed proposer audit or runtime not confirmed")
	}
	return nil
}

func (s *demoScenario) inspect(ctx context.Context) (*model.FleetState, bool, error) {
	if s.evidence.ProposalID == "" {
		state, err := (&graphql.Resolver{Service: s.service}).Query().FleetState(ctx)
		return state, true, err
	}
	row, err := s.service.ReadProposal(ctx, s.evidence.ProposalID)
	if err != nil {
		return nil, false, err
	}
	if row == nil || !row.CurrentRuntime {
		return nil, false, errors.New("active proposal missing")
	}
	state, err := (&graphql.Resolver{Service: s.service}).Query().FleetState(ctx)
	// Expiry alone is not a human decision. A failed approval cannot open this gate.
	decided := row.Proposal.State == proposal.Rejected || (row.Proposal.State == proposal.Approved && row.Execution == fleet.Succeeded)
	return state, decided, err
}

func demoBaseline(initial sim.State) (demoEvidence, error) {
	baseline, err := sim.NewReassignmentTestFixture()
	if err != nil || !reflect.DeepEqual(initial, baseline.Snapshot()) {
		return demoEvidence{}, errors.New("matched baseline setup failed")
	}
	for range 200 {
		baseline.Tick()
	}
	if baseline.Snapshot().Jobs[0].Status != sim.JobAssigned || !baseline.Snapshot().Robots[0].Blocked {
		return demoEvidence{}, errors.New("baseline unexpectedly recovered")
	}
	return demoEvidence{ModelMode: "deterministic local Mastra adapter; NOT live LLM", NoExecutionBeforeApproval: true, Initial: initial, Baseline: baseline.Snapshot()}, nil
}

func (f demoFactory) propose(ctx context.Context, handler http.Handler, dir string) (string, error) {
	if f.proposeRuntime != nil {
		return f.proposeRuntime(ctx, handler, dir)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	server := httptest.NewServer(handler)
	defer server.Close()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(f.root, "agent", "scripts", "phase4-demo-reasoning.mjs"), "--submit")
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "FLEETOPS_P405_URL="+server.URL+"/graphql", "FLEETOPS_P405_PROPOSER_TOKEN="+f.tokens["proposer"], "FLEETOPS_P405_REASONING_FILE="+filepath.Join(dir, "reasoning.json"))
	output, err := cmd.Output()
	if err != nil {
		return "", errors.New("deterministic proposer failed; inspect historical database evidence")
	}
	var submitted struct {
		ProposalID               string
		LocalCalls, PaidRequests int
	}
	if err := json.Unmarshal(output, &submitted); err != nil || submitted.ProposalID == "" || submitted.LocalCalls != 1 || submitted.PaidRequests != 0 {
		return "", errors.New("invalid proposer result")
	}
	return submitted.ProposalID, nil
}

func (s *demoScenario) capture(ctx context.Context) error {
	if s.evidence.ProposalID == "" {
		return s.factory.capture(ctx, s.dir, s.generation, s.runtimeID, s.service, s.evidence)
	}
	row, err := s.service.ReadProposal(ctx, s.evidence.ProposalID)
	if err != nil {
		return err
	}
	if row == nil {
		return errors.New("proposal missing")
	}
	s.evidence.Decision = string(row.Proposal.State)
	s.evidence.OperatorDecisionObserved = row.Proposal.State == proposal.Approved || row.Proposal.State == proposal.Rejected
	return s.factory.capture(ctx, s.dir, s.generation, s.runtimeID, s.service, s.evidence)
}

func (s *demoScenario) advance(ctx context.Context) error {
	if s.evidence.ScenarioID != "blocked-reassignment" {
		return nil
	}
	row, err := s.service.ReadProposal(ctx, s.evidence.ProposalID)
	if err != nil {
		return err
	}
	if row == nil {
		return errors.New("proposal missing")
	}
	if row.Proposal.State != proposal.Approved || row.Execution != fleet.Succeeded || s.service.Snapshot().Tick >= 242 {
		return nil
	}
	if s.nextTick.IsZero() {
		s.nextTick = time.Now().Add(2 * time.Second)
	}
	if time.Now().Before(s.nextTick) {
		return nil
	}
	count := 1
	if s.service.Snapshot().Tick >= 45 {
		count = 197
	}
	for range count {
		if err := s.service.Tick(); err != nil {
			return err
		}
	}
	if s.service.Snapshot().Tick == 242 {
		if err := s.verifyRecovery(ctx); err != nil {
			return err
		}
	}
	s.evidence.Progress = append(s.evidence.Progress, s.service.Snapshot())
	s.nextTick = time.Now().Add(2 * time.Second)
	return s.capture(ctx)
}

func (s *demoScenario) verifyRecovery(ctx context.Context) error {
	state := s.service.Snapshot()
	if state.Jobs[0].Status != sim.JobDone || state.Jobs[0].AssignedRobot != "R3" {
		return errors.New("recovery did not match verified outcome")
	}
	var approved, executed int
	if err := s.factory.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE attempt='approve'),count(*) FILTER (WHERE attempt='execute') FROM fleet_audit WHERE proposal_id=$1 AND accepted AND operator_subject=$2`, s.evidence.ProposalID, s.factory.subject("operator")).Scan(&approved, &executed); err != nil || approved != 1 || executed != 1 {
		return errors.New("independent committed approval/execution audits not confirmed")
	}
	return nil
}

func (f demoFactory) capture(ctx context.Context, dir, generation, runtimeID string, service *fleet.Service, evidence demoEvidence) error {
	var database json.RawMessage
	// One statement captures full committed records, including denied and partial
	// replacement attempts. The controller has drained every simulator operation.
	if err := f.pool.QueryRow(ctx, `SELECT jsonb_build_object(
 'proposals',(SELECT coalesce(jsonb_agg(to_jsonb(p) ORDER BY id),'[]') FROM fleet_proposals p),
 'audit',(SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY id),'[]') FROM fleet_audit a))`).Scan(&database); err != nil {
		return err
	}
	evidence.Final = service.Snapshot()
	return writeDemoEvidence(dir, "capture", struct {
		Generation, RuntimeID string
		CapturedAt            time.Time
		Poisoned              bool
		Warning               string
		Evidence              demoEvidence
		Database              json.RawMessage
	}{generation, runtimeID, time.Now().UTC(), service.Poisoned(), "Committed database records only. Unknown outcomes require manual reconciliation; restart does not repair history.", evidence, database})
}

func TestPhase4AcceptanceDemo(t *testing.T) {
	if os.Getenv("FLEETOPS_P405_SUBMIT") != "1" || os.Getenv("FLEETOPS_LOCAL_DEMO") != "1" {
		t.Fatal("explicit -Submit and -LocalDemo required")
	}
	root, runDir := os.Getenv("FLEETOPS_P405_ROOT"), os.Getenv("FLEETOPS_P405_RUN_DIR")
	evidenceDir := filepath.Join(runDir, "history", demoRandom())
	if err := os.MkdirAll(evidenceDir, 0700); err != nil {
		t.Fatal(err)
	}
	a, raw := dashboardCredentials(t)
	var tokens map[string]string
	if err := json.Unmarshal([]byte(raw), &tokens); err != nil {
		t.Fatal(err)
	}
	// This launcher owns an isolated database. Never register schema deletion:
	// failed evidence capture must leave committed records available for recovery.
	pool, err := db.Open(context.Background(), db.DefaultConfig(os.Getenv("FLEETOPS_TEST_DATABASE_URL")))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	factory := demoFactory{pool: pool, auth: a, tokens: tokens, root: root, evidenceDir: evidenceDir}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	run, err := factory.create(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	c := &demoController{run: run, ready: true, interactive: true, speed: "1", factory: factory.create, selectFactory: factory.createScenario, record: func(generation, outcome string) error {
		return writeDemoEvidence(evidenceDir, "lifecycle", struct {
			Generation, Outcome string
			At                  time.Time
		}{generation, outcome, time.Now().UTC()})
	}}
	defer func() {
		if err := c.close(); err != nil {
			t.Error("FINAL EVIDENCE CAPTURE FAILED; do not infer persisted outcomes:", err)
		}
	}()
	gateway, err := newDemoGateway(c, tokens["operator"], filepath.Join(root, "web", "dist"))
	if err != nil {
		t.Fatal(err)
	}
	browser := httptest.NewUnstartedServer(gateway)
	gateway.origin = "http://" + browser.Listener.Addr().String()
	browser.Config.ReadHeaderTimeout, browser.Config.IdleTimeout = 5*time.Second, 30*time.Second
	browser.Config.MaxHeaderBytes = 16384
	browser.Start()
	defer gateway.close()
	defer browser.Close()
	privateFile := filepath.Join(runDir, "session.json")
	saveDemoJSON(t, privateFile, map[string]string{"url": browser.URL})
	defer os.Remove(privateFile)
	if os.Getenv("FLEETOPS_P405_CHECK_ONLY") == "1" {
		t.Log("PASS: fresh tick-42 fixture, deterministic proposer, committed pending proposal, no execution; human review NOT tested")
		return
	}
	fmt.Printf("READY: %s · Restart Scenario enabled · evidence %s\n", browser.URL, evidenceDir)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(20 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(runDir, "stop")); err == nil {
				return
			}
			if _, err := os.Stat(filepath.Join(runDir, "outage")); err == nil {
				return
			}
			if err := c.advance(context.Background()); err != nil {
				t.Log("scenario stopped; restart or reconcile:", err)
			}
		}
	}
}

// Verify the fixed trusted fixture without allocating another database service.
func TestDemoTrustedBaseline(t *testing.T) {
	s, err := sim.NewReassignmentTestFixture()
	if err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Tick != 42 {
		t.Fatal("fixture no longer starts at tick 42")
	}
	for range 200 {
		s.Tick()
	}
	state := s.Snapshot()
	if state.Tick != 242 || state.Jobs[0].Status != sim.JobAssigned || !state.Robots[0].Blocked {
		t.Fatal("baseline unexpectedly recovered")
	}
}
