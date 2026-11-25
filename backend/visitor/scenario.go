package visitor

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	pool                         *db.Pool
	auth                         *auth.Authenticator
	tokens                       map[string]string
	evidenceDir, proposerSubject string
	operations                   operationsDecider
	recordOperations             operationsRecorder
	recovery                     recoveryInvestigator
	warehouseChargeTarget        int
}

type demoScenario struct {
	factory                                            demoFactory
	service                                            *fleet.Service
	generation, runtimeID, dir, scenarioID, proposalID string
	initial                                            sim.State
}

func demoRandom() string {
	id, err := visitorRandom()
	if err != nil {
		panic("visitor entropy unavailable")
	}
	return id
}

func (f demoFactory) create(ctx context.Context) (*demoRun, error) {
	return f.createScenario(ctx, "blocked-reassignment")
}

func (f demoFactory) createScenario(ctx context.Context, id string) (*demoRun, error) {
	var service *fleet.Service
	var err error
	if id == warehouseScenario {
		var seed [8]byte
		if _, err := rand.Read(seed[:]); err != nil {
			return nil, err
		}
		// Keep the replay seed exact in browser JSON numbers.
		service, err = fleet.NewVisitorWarehouseWithChargeTarget(int64(binary.LittleEndian.Uint64(seed[:])&((1<<53)-1)), f.pool, f.warehouseChargeTarget)
	} else {
		service, err = fleet.NewVisitor(id, f.pool)
	}
	if err != nil {
		return nil, err
	}
	s := &demoScenario{factory: f, service: service, generation: demoRandom(), runtimeID: service.RuntimeID(), scenarioID: id, initial: service.Snapshot()}
	s.dir = filepath.Join(f.evidenceDir, s.generation)
	if err := os.Mkdir(s.dir, 0700); err != nil {
		return nil, err
	}
	handler := graphql.NewHandler(service, f.auth)
	if id == "blocked-reassignment" || id == "routine-charging" {
		s.proposalID, err = visitorProposal(ctx, handler, f.tokens["proposer"], s.dir)
		if err != nil {
			return nil, err
		}
		if err := s.verifySubmission(ctx); err != nil {
			return nil, err
		}
	}
	if !reflect.DeepEqual(s.initial, service.Snapshot()) {
		return nil, errors.New("proposal submission mutated simulator")
	}
	observation, err := (&graphql.Resolver{Service: service}).Query().FleetState(ctx)
	if err != nil {
		return nil, err
	}
	if id == "blocked-no-candidate" && len(observation.ReassignmentCandidates) != 0 {
		return nil, errors.New("no-candidate preset admitted transfer")
	}
	if err := s.capture(ctx); err != nil {
		return nil, err
	}
	source := demoSourceNone
	if s.proposalID != "" {
		source = demoSourceGoScripted
	}
	run := &demoRun{scenarioID: id, generation: s.generation, runtimeID: s.runtimeID, handler: handler, capture: s.capture,
		inspect: s.inspect, step: func(context.Context) error { return service.Tick() },
		evidence: &demoExplanation{Generation: s.generation, RuntimeID: s.runtimeID, ScenarioID: id,
			ProposalID: s.proposalID, ProposalSource: source, Observation: observation}}
	if id == warehouseScenario {
		run.warehouse = service.Snapshot
		run.recovery = s.decideWarehouseRecovery
		run.recoveryHistory = s.warehouseRecoveryHistory
		if f.recovery != nil {
			run.operations = &runtimeOperations{service: service, recovery: &recoveryOperations{scenario: s, investigate: f.recovery, source: "live-model", reviewed: make(map[string]bool)}}
		} else if f.operations != nil {
			run.operations, _ = newRuntimeOperations(service, f.operations)
			if run.operations != nil {
				run.operations.record = f.recordOperations
			}
		}
	}
	return run, nil
}

func (s *demoScenario) verifySubmission(ctx context.Context) error {
	var runtime string
	if err := s.factory.pool.QueryRow(ctx, `SELECT runtime_id FROM fleet_proposals WHERE id=$1 AND state='pending' AND execution='none' AND submitted_by=$2`, s.proposalID, s.factory.proposerSubject).Scan(&runtime); err != nil {
		return err
	}
	var audits int
	if err := s.factory.pool.QueryRow(ctx, `SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='create' AND accepted AND operator_subject=$2`, s.proposalID, s.factory.proposerSubject).Scan(&audits); err != nil || audits != 1 || runtime != s.runtimeID {
		return errors.New("committed proposer audit or runtime unconfirmed")
	}
	return nil
}

func (s *demoScenario) inspect(ctx context.Context) (*model.FleetState, bool, error) {
	state, err := (&graphql.Resolver{Service: s.service}).Query().FleetState(ctx)
	if err != nil || s.proposalID == "" {
		return state, true, err
	}
	row, err := s.service.ReadProposal(ctx, s.proposalID)
	if err != nil {
		return nil, false, err
	}
	if row == nil || !row.CurrentRuntime {
		return nil, false, errors.New("active proposal missing")
	}
	decided := row.Proposal.State == proposal.Rejected || row.Proposal.State == proposal.Approved && row.Execution == fleet.Succeeded
	return state, decided, nil
}

func (s *demoScenario) capture(ctx context.Context) error {
	if err := s.factory.pool.Ping(ctx); err != nil {
		// Disconnected idle pool connections can survive until first acquisition.
		// Only this read-only probe is retried, within the existing deadline.
		if ctx.Err() != nil {
			return err
		}
		if err := s.factory.pool.Ping(ctx); err != nil {
			return err
		}
	}
	// The final database archive includes every generation and denied attempt.
	// Controller captures preserve simulator state without duplicating DB history.
	return writeDemoEvidence(s.dir, "capture", struct {
		Generation, RuntimeID, ScenarioID, ProposalID, ProposalSource string
		CapturedAt                                                    time.Time
		Initial, Final                                                sim.State
		Poisoned                                                      bool
	}{s.generation, s.runtimeID, s.scenarioID, s.proposalID, string(sourceForProposal(s.proposalID)), time.Now().UTC(), s.initial, s.service.Snapshot(), s.service.Poisoned()})
}

func sourceForProposal(id string) demoProposalSource {
	if id == "" {
		return demoSourceNone
	}
	return demoSourceGoScripted
}

// The response collector is private and bounded by the small fixed internal
// GraphQL selections. Production never starts an auxiliary HTTP listener.
type responseBuffer struct {
	header http.Header
	status int
	body   []byte
}

func (w *responseBuffer) Header() http.Header    { return w.header }
func (w *responseBuffer) WriteHeader(status int) { w.status = status }
func (w *responseBuffer) Write(raw []byte) (int, error) {
	if len(w.body)+len(raw) > 65536 {
		return 0, errors.New("internal response exceeded limit")
	}
	w.body = append(w.body, raw...)
	return len(raw), nil
}

var errWarehouseRecoveryUnavailable = errors.New("warehouse recovery proposal unavailable")

func warehouseRecoveryCommand(path string) (string, proposal.Decision, bool) {
	tail, ok := strings.CutPrefix(path, "/demo/recovery/")
	if !ok {
		return "", "", false
	}
	id, action, ok := strings.Cut(tail, "/")
	if !ok || len(id) != 33 || id[0] != 'A' || id != "A"+strings.ToLower(id[1:]) {
		return "", "", false
	}
	if _, err := hex.DecodeString(id[1:]); err != nil {
		return "", "", false
	}
	switch action {
	case "approve":
		return id, proposal.Approve, true
	case "reject":
		return id, proposal.Reject, true
	default:
		return "", "", false
	}
}

func (s *demoScenario) decideWarehouseRecovery(ctx context.Context, header, runID string, decision proposal.Decision) (fleet.Result, error) {
	operatorCtx, principal, err := s.recoveryOperator(ctx, header)
	if err != nil {
		return fleet.Result{}, err
	}
	runs, err := s.service.ReadAgentRuns(operatorCtx, runID, 1)
	if err != nil {
		return fleet.Result{}, err
	}
	if len(runs) != 1 || runs[0].Metadata.Contract != "recovery-decision-v3" ||
		!runs[0].DecisionVerified || runs[0].ProposalID == nil {
		return fleet.Result{}, errWarehouseRecoveryUnavailable
	}
	return s.service.DecideRecoveryProposal(operatorCtx, *runs[0].ProposalID, runID, decision, proposal.OperatorIdentity{Subject: principal.Subject})
}

func (s *demoScenario) recoveryOperator(ctx context.Context, header string) (context.Context, auth.Principal, error) {
	token, present := strings.CutPrefix(header, "Bearer ")
	if !present {
		return nil, auth.Principal{}, fleet.ErrUnauthorized
	}
	operatorCtx, err := s.factory.auth.Authenticate(ctx, token)
	if err != nil {
		return nil, auth.Principal{}, fleet.ErrUnauthorized
	}
	principal, ok := auth.FromContext(operatorCtx)
	if !ok || principal.Role != auth.Operator {
		return nil, auth.Principal{}, fleet.ErrUnauthorized
	}
	return operatorCtx, principal, nil
}

type warehouseRecoveryItem struct {
	RunID     string                `json:"runId"`
	State     string                `json:"state"`
	Execution string                `json:"execution"`
	Selection fleet.AgentKey        `json:"selection"`
	Basis     string                `json:"basis"`
	Metrics   map[string]int        `json:"metrics"`
	Incidents []fleet.AgentIncident `json:"incidents"`
}

type warehouseRecoveryReport struct {
	Live           bool                             `json:"live"`
	Proposals      []warehouseRecoveryItem          `json:"proposals"`
	Investigations []warehouseRecoveryInvestigation `json:"investigations"`
}

type warehouseRecoveryInvestigation struct {
	RunID             string `json:"runId"`
	Source            string `json:"source"`
	Status            string `json:"status"`
	ProviderConfirmed bool   `json:"providerConfirmed"`
}

func recoveryInvestigation(run fleet.AgentRun) warehouseRecoveryInvestigation {
	result := warehouseRecoveryInvestigation{RunID: run.ID, Source: run.Metadata.Source, Status: "investigating"}
	if c := run.Completion; c != nil {
		result.ProviderConfirmed = run.Metadata.Source == "live-model" && c.Status == "accepted" &&
			c.Requests > 0 && c.ActualModel == "openai/gpt-6-luna" && c.Usage.Status == "reported"
	}
	switch c := run.Completion; {
	case c == nil:
	case c.Status == "failed":
		result.Status = "failed"
		if c.Failure == "cancelled" {
			result.Status = "cancelled"
		}
	case !run.DecisionVerified || c.Decision == nil:
		result.Status = "validation_rejected"
	case c.Decision.Kind == "abstain":
		result.Status = "abstained"
	case run.ProposalID == nil:
		result.Status = "proposal_not_admitted"
	default:
		result.Status = "proposal_admitted"
	}
	return result
}

func (s *demoScenario) warehouseRecoveryHistory(ctx context.Context, header string) (warehouseRecoveryReport, error) {
	operatorCtx, _, err := s.recoveryOperator(ctx, header)
	if err != nil {
		return warehouseRecoveryReport{}, err
	}
	runs, err := s.service.ReadAgentRuns(operatorCtx, "", 20)
	if err != nil {
		return warehouseRecoveryReport{}, err
	}
	report := warehouseRecoveryReport{
		Live: s.factory.recovery != nil, Proposals: make([]warehouseRecoveryItem, 0, len(runs)),
		Investigations: make([]warehouseRecoveryInvestigation, 0, len(runs)),
	}
	for _, run := range runs {
		if run.Metadata.Contract != "recovery-decision-v3" {
			continue
		}
		report.Investigations = append(report.Investigations, recoveryInvestigation(run))
		if !run.DecisionVerified ||
			run.Proposal == nil || run.Completion == nil || run.Completion.Decision == nil || run.Completion.Decision.Selected == nil {
			continue
		}
		selection := *run.Completion.Decision.Selected
		item := warehouseRecoveryItem{
			RunID: run.ID, State: string(run.Proposal.Proposal.State), Execution: string(run.Proposal.Execution),
			Selection: selection, Basis: run.Completion.Decision.Basis, Incidents: []fleet.AgentIncident{},
		}
		if run.Proposal.Proposal.State == proposal.Pending && !time.Now().Before(run.Proposal.Proposal.ExpiresAt) {
			item.State = string(proposal.Expired)
		}
		for _, candidate := range run.Facts.Candidates {
			if candidate.Key == selection {
				item.Metrics = candidate.Metrics
				break
			}
		}
		for _, incident := range run.Facts.Incidents {
			if incident.RobotID == selection.FromRobotID {
				item.Incidents = append(item.Incidents, incident)
			}
		}
		report.Proposals = append(report.Proposals, item)
	}
	return report, nil
}
