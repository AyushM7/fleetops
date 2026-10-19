package visitor

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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
	service, err := fleet.NewVisitor(id, f.pool)
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
	return &demoRun{scenarioID: id, generation: s.generation, handler: handler, capture: s.capture,
		inspect: s.inspect, step: func(context.Context) error { return service.Tick() },
		evidence: &demoExplanation{Generation: s.generation, RuntimeID: s.runtimeID, ScenarioID: id,
			ProposalID: s.proposalID, ProposalSource: source, Observation: observation}}, nil
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
