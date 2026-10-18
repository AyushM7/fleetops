//go:build fleetops_test

package fleet_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
)

type agent07Scenario struct {
	ID, Variant, Objective, Expected, Quality, Probe string
	Comparable                                       bool
}
type agent07Config struct {
	Live                 bool
	TestHarnessApprovals bool
	Scenarios            []string
	Trials, MaxElapsedMs int
}
type agent07Outcome struct {
	StartTick           int    `json:"startTick"`
	EndTick             int    `json:"endTick"`
	CompletedDeliveries int    `json:"completedDeliveries"`
	CompletionTick      *int   `json:"completionTick"`
	UnresolvedJobs      int    `json:"unresolvedJobs"`
	JobProgress         string `json:"jobProgress"`
	BlockedRobotSamples int    `json:"blockedRobotSamples"`
	AggregateBattery    int    `json:"aggregateBatteryPercentagePoints"`
}
type agent07Branch struct {
	Source                  string          `json:"source"`
	ScenarioID              string          `json:"scenarioId"`
	Policy                  string          `json:"policy"`
	Trial                   int             `json:"trial"`
	InitialHash             string          `json:"initialSimulatorStateSHA256"`
	InitialObservationID    string          `json:"initialObservationId"`
	Initial                 sim.State       `json:"initialState"`
	Decision                json.RawMessage `json:"decision"`
	GoValidation            string          `json:"goValidation"`
	ServerVerified          *bool           `json:"serverVerifiedDecision"`
	PersistedRun            *fleet.AgentRun `json:"persistedRun"`
	Approval                string          `json:"approval"`
	Execution               fleet.Execution `json:"execution"`
	SuccessfulReassignments int             `json:"successfulReassignments"`
	InterventionFailures    int             `json:"interventionFailures"`
	Outcome                 *agent07Outcome `json:"simulatorOutcome"`
	NotComparable           string          `json:"notComparable"`
	StopReason              string          `json:"stopReason"`
}

func agent07Auth(t *testing.T) (*auth.Authenticator, string, string) {
	t.Helper()
	credentials := []auth.Credential{}
	tokens := []string{}
	for _, role := range []auth.Role{auth.Proposer, auth.Operator} {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			t.Fatal(err)
		}
		token := hex.EncodeToString(random[:])
		hash := sha256.Sum256([]byte(token))
		tokens = append(tokens, token)
		credentials = append(credentials, auth.Credential{Subject: "agent07-test-harness-" + string(role), Role: role, SHA256: hex.EncodeToString(hash[:])})
	}
	raw, _ := json.Marshal(credentials)
	a, err := auth.New(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return a, tokens[0], tokens[1]
}
func agent07Measure(t *testing.T, s *fleet.Service, initial sim.State, horizon int) *agent07Outcome {
	t.Helper()
	o := &agent07Outcome{StartTick: initial.Tick, JobProgress: "no target job"}
	done := map[string]bool{}
	for _, j := range initial.Jobs {
		if j.Status == sim.JobDone {
			done[j.ID] = true
		}
	}
	for range horizon {
		if err := s.Tick(); err != nil {
			t.Fatal(err)
		}
		state := s.Snapshot()
		for _, r := range state.Robots {
			if r.Blocked {
				o.BlockedRobotSamples++
			}
		}
		for _, j := range state.Jobs {
			if j.Status == sim.JobDone && !done[j.ID] {
				done[j.ID] = true
				o.CompletedDeliveries++
				if j.ID == "J1" {
					tick := state.Tick
					o.CompletionTick = &tick
				}
			}
		}
	}
	final := s.Snapshot()
	o.EndTick = final.Tick
	for _, r := range final.Robots {
		o.AggregateBattery += r.Battery
	}
	for _, j := range final.Jobs {
		if j.Status != sim.JobDone {
			o.UnresolvedJobs++
		}
		if j.ID == "J1" {
			o.JobProgress = string(j.Status) + "; owner=" + j.AssignedRobot
		}
	}
	return o
}
func agent07Server(t *testing.T, service *fleet.Service, a *auth.Authenticator, stale bool) *httptest.Server {
	t.Helper()
	handler := graphql.NewHandler(service, a)
	var changed atomic.Bool
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Error(err)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		if stale && bytes.Contains(body, []byte("FinishAgentRun")) && changed.CompareAndSwap(false, true) {
			if err := service.Tick(); err != nil {
				t.Error(err)
			}
		}
		handler.ServeHTTP(w, r)
	}))
}
func agent07Child(t *testing.T, scenario, policy, rawConfig, endpoint, token string, deadline time.Time) ([]byte, bool) {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	root, err := filepath.Abs("../../evals")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(root, "scripts", "agent07.mjs"), "--fixture-trial")
	command.Env = append(os.Environ(), "FLEETOPS_AGENT07_CONFIG="+rawConfig, "FLEETOPS_AGENT07_ENDPOINT="+endpoint+"/graphql", "FLEETOPS_AGENT07_PROPOSER="+token,
		"FLEETOPS_AGENT07_SCENARIO="+scenario, "FLEETOPS_AGENT07_POLICY="+policy, "FLEETOPS_AGENT07_DEADLINE="+deadline.Format(time.RFC3339Nano))
	// Neither stderr nor raw process/provider errors are published as run evidence.
	output, err := command.Output()
	return output, err == nil
}
func agent07Unknown(scenario agent07Scenario, policy string, live bool) json.RawMessage {
	source := "deterministic"
	if policy == "v2-investigation" {
		source = "mocked"
		if live {
			source = "live-model"
		}
	}
	data, _ := json.Marshal(map[string]any{"scenarioId": scenario.ID, "policy": policy, "objective": scenario.Objective, "source": source, "observationProvenance": "authenticated-go-runtime",
		"initialObservationId": nil, "initialStateHash": nil, "candidateFacts": []any{}, "incidentFacts": []any{}, "requestedModel": nil, "status": "generation-failed", "failure": "subprocess_unknown",
		"selected": nil, "abstention": nil, "quality": map[string]any{"admitted": false, "regret": nil, "dominated": nil, "objectiveMet": false}, "completion": nil, "tools": nil, "cost": nil,
		"modelCalls": map[string]any{"localAdapterFailures": nil, "providerHTTPAttempts": nil, "failedHTTPResponses": nil, "uncertainHTTPResults": nil},
		"latencyMs":  nil, "proposal": nil, "provenance": nil, "groundingAccepted": false, "expectationMet": false, "submissionAttempts": nil})
	return data
}
func TestAgent07UnknownCompletionEvidence(t *testing.T) {
	var evidence map[string]any
	if json.Unmarshal(agent07Unknown(agent07Scenario{ID: "unknown"}, "v2-investigation", false), &evidence) != nil {
		t.Fatal("invalid unknown projection")
	}
	if evidence["status"] != "generation-failed" || evidence["submissionAttempts"] != nil || evidence["completion"] != nil || evidence["proposal"] != nil || evidence["latencyMs"] != nil {
		t.Fatal(evidence)
	}
}
func agent07Approve(t *testing.T, service *fleet.Service, a *auth.Authenticator, operatorToken, id string, branch *agent07Branch) {
	t.Helper()
	operatorCtx, err := a.Authenticate(context.Background(), operatorToken)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Decide(operatorCtx, id, proposal.Approve, proposal.OperatorIdentity{Subject: "agent07-test-harness-operator"})
	branch.Approval, branch.Execution = "test-harness approval; NOT actual human approval", result.Execution
	if err != nil || result.Execution != fleet.Succeeded {
		branch.InterventionFailures++
		branch.StopReason = "test approval/execution failed or uncertain; no retry or further execution"
	} else {
		branch.SuccessfulReassignments++
	}
	if service.Poisoned() {
		branch.StopReason = "runtime reconciliation required after execution; no retry"
	}
}
func agent07RunBranch(t *testing.T, scenario agent07Scenario, policy string, trial int, cfg agent07Config, rawConfig string, deadline time.Time) agent07Branch {
	t.Helper()
	service := fleet.NewAgent07TestService(t, scenario.Variant, func() time.Time { return time.Now().UTC() })
	initial := service.Snapshot()
	initialJSON, _ := json.Marshal(initial)
	hash := sha256.Sum256(initialJSON)
	view, err := service.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	branch := agent07Branch{ScenarioID: scenario.ID, Policy: policy, Trial: trial, Initial: initial, InitialHash: hex.EncodeToString(hash[:]), InitialObservationID: view.ObservationID,
		GoValidation: "not_requested", Approval: "not_requested", Execution: fleet.NotExecuted}
	branch.Source = "deterministic"
	if policy == "v2-investigation" {
		branch.Source = "mocked"
		if cfg.Live {
			branch.Source = "agent-reported-live-model"
		}
	}
	if policy != "no-intervention" {
		a, proposerToken, operatorToken := agent07Auth(t)
		// Deterministically inject the tick AFTER the final client read but BEFORE
		// production Go acquires its finish/admission lock. No timing sleeps.
		server := agent07Server(t, service, a, scenario.ID == "stale")
		defer server.Close()
		output, completed := agent07Child(t, scenario.ID, policy, rawConfig, server.URL, proposerToken, deadline)
		if !completed {
			branch.Decision = agent07Unknown(scenario, policy, cfg.Live)
			branch.GoValidation = "unknown"
			branch.NotComparable = "Unknown trial process; no retry, approval or execution"
			return branch
		}
		var decision struct {
			Status             string
			Failure            *string
			SubmissionAttempts int
			ExpectationMet     bool
			Proposal           *struct{ Status, ProposalID string }
			Provenance         *struct{ RunID, Status string }
		}
		if len(output) > 200000 || json.Unmarshal(output, &decision) != nil {
			t.Fatal("invalid bounded trial report")
		}
		branch.Decision = json.RawMessage(output)
		if decision.Proposal != nil && decision.Proposal.Status == "unknown" || decision.Provenance != nil && decision.Provenance.Status == "unknown" {
			branch.GoValidation = "unknown"
			branch.NotComparable = "Unknown completion/submission; no retry, approval or execution"
			return branch
		}
		if decision.SubmissionAttempts > 1 {
			t.Fatal("submission retried")
		}
		if scenario.ID != "stale" && !reflect.DeepEqual(initial, service.Snapshot()) {
			t.Fatal("model mutated simulator before controlled approval")
		}
		if decision.Provenance != nil && decision.Provenance.Status == "recorded" {
			proposerCtx, _ := a.Authenticate(context.Background(), proposerToken)
			runs, err := service.ReadAgentRuns(proposerCtx, decision.Provenance.RunID, 1)
			if err != nil || len(runs) != 1 {
				t.Fatal("durable provenance missing")
			}
			verified := runs[0].DecisionVerified
			branch.ServerVerified = &verified
			branch.PersistedRun = &runs[0]
		}
		if decision.Proposal != nil && decision.Proposal.Status == "pending_approval" {
			branch.GoValidation = "pending_admitted"
			if policy == "v2-investigation" && (decision.Status != "accepted" || branch.ServerVerified == nil || !*branch.ServerVerified) {
				t.Fatal("unverified pending recommendation")
			}
			if policy == "v2-investigation" && (branch.PersistedRun.ProposalID == nil || *branch.PersistedRun.ProposalID != decision.Proposal.ProposalID) {
				t.Fatal("proposal/provenance association mismatch")
			}
			if !cfg.TestHarnessApprovals {
				t.Fatal("evaluation submitted a proposal despite default disablement")
			}
			agent07Approve(t, service, a, operatorToken, decision.Proposal.ProposalID, &branch)
		} else if decision.Proposal != nil && decision.Proposal.Status == "rejected" {
			branch.GoValidation = "denied"
		}
		if scenario.ID == "stale" && decision.SubmissionAttempts != 0 && branch.GoValidation != "denied" {
			t.Fatal("stale observation admitted")
		}
		if scenario.ID == "stale" {
			expected, err := sim.NewAgent07TestFixture(scenario.Variant)
			if err != nil {
				t.Fatal(err)
			}
			expected.Tick()
			if !reflect.DeepEqual(expected.Snapshot(), service.Snapshot()) {
				t.Fatal("stale denial changed state beyond scheduled tick")
			}
		}
		if scenario.ID == "false-evidence" && !cfg.Live && decision.Status != "rejected" {
			t.Fatal("false evidence was not rejected")
		}
		if scenario.ID == "invalid-tool" && !cfg.Live && (decision.Failure == nil || *decision.Failure != "invalid_tool") {
			t.Fatal("unsafe tool was not blocked")
		}
		if !cfg.Live && policy == "v2-investigation" && !decision.ExpectationMet {
			t.Errorf("scripted control expectation failed: %s", scenario.ID)
		}
	}
	if branch.StopReason != "" {
		branch.NotComparable = branch.StopReason
		return branch
	}
	if scenario.Comparable && cfg.TestHarnessApprovals {
		branch.Outcome = agent07Measure(t, service, service.Snapshot(), 200)
	} else {
		branch.NotComparable = "No comparable intervention window (poisoned/stale/negative probe or submission-disabled live trial); no speedup claimed"
	}
	return branch
}
func TestAgent07PairedEvaluation(t *testing.T) {
	if os.Getenv("FLEETOPS_AGENT07_CHECK") != "1" {
		t.Skip("opt in to isolated agent07 evaluation")
	}
	database, err := url.Parse(os.Getenv("FLEETOPS_TEST_DATABASE_URL"))
	if err != nil || database == nil || (database.Hostname() != "127.0.0.1" && database.Hostname() != "localhost" && database.Hostname() != "::1") || (!strings.HasPrefix(database.Path, "/fleetops_agent07") && !strings.HasPrefix(database.Path, "/fleetops_test")) {
		t.Fatal("isolated localhost test database required")
	}
	rawConfig := os.Getenv("FLEETOPS_AGENT07_CONFIG")
	var cfg agent07Config
	if json.Unmarshal([]byte(rawConfig), &cfg) != nil || cfg.Trials < 1 || cfg.Trials > 3 || len(cfg.Scenarios) == 0 || len(cfg.Scenarios) > 12 || cfg.MaxElapsedMs < 1000 || cfg.MaxElapsedMs > 600000 {
		t.Fatal("invalid controlled evaluation configuration")
	}
	raw, err := os.ReadFile("../../evals/fixtures/agent07-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version, FixtureVersion string
		Seed                    int
		HorizonTicks            int
		Scenarios               []agent07Scenario
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version != "agent07-v1" || manifest.Seed != 42 || manifest.HorizonTicks != 200 {
		t.Fatal("unsupported fixture manifest")
	}
	deadline := time.Now().Add(time.Duration(cfg.MaxElapsedMs) * time.Millisecond)
	rows := []agent07Branch{}
	seen := map[string]bool{}
	stoppedReason := ""
	for _, id := range cfg.Scenarios {
		var selected *agent07Scenario
		for _, s := range manifest.Scenarios {
			if s.ID == id {
				copy := s
				selected = &copy
			}
		}
		if selected == nil || seen[id] {
			t.Fatal("unknown/duplicate scenario")
		}
		seen[id] = true
		for trial := 1; trial <= cfg.Trials; trial++ {
			policies := []string{"v2-investigation"}
			if selected.Comparable && cfg.TestHarnessApprovals {
				policies = []string{"no-intervention", "first-eligible", "minimum-pickup", "maximum-headroom", "v2-investigation"}
			}
			initialHash := ""
			for _, policy := range policies {
				if time.Now().After(deadline) {
					stoppedReason = "elapsed ceiling; no retry"
					goto finished
				}
				branch := agent07RunBranch(t, *selected, policy, trial, cfg, rawConfig, deadline)
				if initialHash != "" && initialHash != branch.InitialHash {
					t.Fatal("paired initial state hash mismatch")
				}
				initialHash = branch.InitialHash
				rows = append(rows, branch)
				if branch.GoValidation == "unknown" {
					if !cfg.Live {
						t.Error("offline trial completion unknown; retained in attempted denominator; no retry")
					}
					stoppedReason = "unknown child completion; no retry or execution"
					goto finished
				}
				if branch.StopReason != "" {
					stoppedReason = branch.StopReason
					goto finished
				}
				if cfg.Live && policy == "v2-investigation" {
					var d struct {
						Status  string
						Failure *string
					}
					_ = json.Unmarshal(branch.Decision, &d)
					if d.Status != "accepted" || d.Failure != nil {
						stoppedReason = "live trial failed/rejected/unknown; no automatic retry"
						goto finished
					}
				}
			}
		}
	}
finished:
	inferenceEvidence := "not_measured; offline scripted adapters only"
	if cfg.Live {
		inferenceEvidence = "agent-reported live-model mode; see per-trial HTTP attempts, failures and completion evidence; not independent provider attestation"
	}
	attempted := 0
	for _, row := range rows {
		if row.Policy == "v2-investigation" {
			attempted++
		}
	}
	report := map[string]any{"version": "agent07-v1", "fixtureVersion": manifest.FixtureVersion, "mode": "go-backed-isolated-evaluation", "seed": 42, "horizonTicks": 200,
		"setup": "sim.NewAgent07TestFixture: deterministic tick-42 blocked prelude; tagged initial position/battery variants before runtime creation; no measurement faults", "measurementFaults": []any{},
		"concurrencySchedule": "stale control only: one simulator tick after final client read and before Go finish admission lock", "fixtureManifest": manifest,
		"plannedInvestigationTrials": len(cfg.Scenarios) * cfg.Trials, "attemptedInvestigationTrials": attempted, "notAttemptedInvestigationTrials": len(cfg.Scenarios)*cfg.Trials - attempted, "stoppedReason": stoppedReason,
		"requestedLiveMode": cfg.Live, "actualLLM": inferenceEvidence, "actualHumanApproval": "not_measured; explicitly controlled test-harness approvals only", "rows": rows}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	destination := os.Getenv("FLEETOPS_AGENT07_REPORT")
	if strings.TrimSpace(destination) == "" {
		t.Fatal("isolated output path required")
	}
	if err := os.WriteFile(destination, append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
