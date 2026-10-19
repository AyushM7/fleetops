//go:build fleetops_test

package fleet_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/graphql/model"
	"github.com/jackc/pgx/v5"
)

type visitorFactory struct {
	admin         *db.Pool
	config        db.Config
	evidenceDir   string
	archiveCommit func(string, []byte) error
}

func visitorCredentials() (*auth.Authenticator, map[string]string, string, error) {
	random, err := visitorRandom()
	if err != nil {
		return nil, nil, "", err
	}
	identity := random[:32]
	credentials := []auth.Credential{}
	tokens := map[string]string{}
	for _, role := range []auth.Role{auth.Proposer, auth.Operator} {
		token, err := visitorRandom()
		if err != nil {
			return nil, nil, "", err
		}
		tokens[string(role)] = token
		digest := sha256.Sum256([]byte(token))
		credentials = append(credentials, auth.Credential{Subject: identity + ":" + string(role), Role: role, SHA256: hex.EncodeToString(digest[:])})
	}
	raw, err := json.Marshal(credentials)
	if err != nil {
		return nil, nil, "", err
	}
	a, err := auth.New(string(raw))
	return a, tokens, identity, err
}

func (f visitorFactory) create(ctx context.Context) (*visitorSession, error) {
	a, tokens, identity, err := visitorCredentials()
	if err != nil {
		return nil, err
	}
	cfg := f.config
	cfg.Schema, cfg.MaxConns = "fleetops_pub01_"+identity, 2
	quoted := pgx.Identifier{cfg.Schema}.Sanitize()
	dir := filepath.Join(f.evidenceDir, cfg.Schema)
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, err
	}
	if _, err := f.admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		return nil, err
	}
	schema := visitorSchemaOwner{name: cfg.Schema}
	if err := f.admin.QueryRow(ctx, `SELECT oid FROM pg_namespace WHERE nspname=$1 AND nspowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)`, cfg.Schema).Scan(&schema.oid); err != nil {
		return nil, retainVisitorSchema(schema, err)
	}
	pool, err := db.Open(ctx, cfg)
	if err != nil {
		return nil, retainVisitorSchema(schema, err)
	}
	if err := pool.Migrate(ctx); err != nil {
		pool.Close()
		return nil, retainVisitorSchema(schema, err)
	}
	release := func() error { return f.release(pool, schema, dir) }
	factory := demoFactory{pool: pool, auth: a, tokens: tokens, evidenceDir: dir,
		proposerSubject: identity + ":proposer", operatorSubject: identity + ":operator"}
	factory.modelMode = "scripted fixed demo preset; no model inference"
	factory.proposalSource = demoSourceGoScripted
	factory.proposeRuntime = func(ctx context.Context, handler http.Handler, dir string) (string, error) {
		return visitorProposal(ctx, handler, tokens["proposer"], dir)
	}
	run, err := factory.create(ctx)
	if err != nil {
		return nil, errors.Join(err, release())
	}
	c := &demoController{run: run, ready: true, interactive: true, speed: "1", factory: factory.create,
		selectFactory: factory.createScenario, record: func(generation, outcome string) error {
			return writeDemoEvidence(dir, "lifecycle", map[string]string{"generation": generation, "outcome": outcome})
		}}
	return &visitorSession{controller: c, operator: tokens["operator"], cleanup: release}, nil
}

func visitorGraphQL(ctx context.Context, handler http.Handler, token, query string) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, err
	}
	r := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body))).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var response struct{ Errors []json.RawMessage }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Errors) != 0 {
		return nil, errors.New("scripted preset proposal unavailable")
	}
	return w.Body.Bytes(), nil
}

// Fixed evidence is submitted as the independent proposer through the same
// authenticated GraphQL, observation, preflight and persistence gates as an agent.
func visitorProposal(ctx context.Context, handler http.Handler, token, dir string) (string, error) {
	raw, err := visitorGraphQL(ctx, handler, token, `{fleetState{observationId reassignmentCandidates{jobId fromRobotId toRobotId}}}`)
	if err != nil {
		return "", err
	}
	var observed struct {
		Data struct{ FleetState model.FleetState }
	}
	if json.Unmarshal(raw, &observed) != nil {
		return "", errors.New("preset observation unavailable")
	}
	state := observed.Data.FleetState
	observation, _ := json.Marshal(state.ObservationID)
	expires, _ := json.Marshal(time.Now().Add(30 * time.Minute).UTC())
	input := `robotId:"R1",stationId:"S1"`
	operation := "submitProposal"
	if len(state.ReassignmentCandidates) > 0 {
		c := state.ReassignmentCandidates[0]
		job, _ := json.Marshal(c.JobID)
		from, _ := json.Marshal(c.FromRobotID)
		to, _ := json.Marshal(c.ToRobotID)
		input, operation = "jobId:"+string(job)+",fromRobotId:"+string(from)+",toRobotId:"+string(to), "submitReassignment"
	}
	query := "mutation{" + operation + "(input:{" + input + ",expiresAt:" + string(expires) + ",expectedObservationId:" + string(observation) + "}){proposalId state execution executionAttempted error}}"
	raw, err = visitorGraphQL(ctx, handler, token, query)
	if err != nil {
		return "", err
	}
	var response struct {
		Data map[string]model.MutationResult
	}
	if json.Unmarshal(raw, &response) != nil {
		return "", errors.New("preset result unavailable")
	}
	result := response.Data[operation]
	if result.ProposalID == "" || result.State != "pending" || result.Execution != "none" || result.ExecutionAttempted || result.Error != nil {
		return "", errors.New("preset proposal not confirmed pending")
	}
	err = writeDemoEvidence(dir, "scripted-proposal", map[string]any{"modelMode": "scripted fixed demo preset; no model inference", "paidRequests": 0, "proposalId": result.ProposalID, "observationId": state.ObservationID})
	return result.ProposalID, err
}

func (f visitorFactory) drop(schema visitorSchemaOwner) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.confirmOwner(ctx, schema); err != nil {
		return err
	}
	_, err := f.admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema.name}.Sanitize()+" CASCADE")
	return err
}

// The private schema also isolates historical and denied-attempt records whose
// existing readers are intentionally database-wide in the single-owner demo.
func (f visitorFactory) release(pool *db.Pool, schema visitorSchemaOwner, dir string) error {
	err := func() error {
		defer pool.Close()
		return f.archive(pool, schema, dir)
	}()
	if err != nil {
		return retainVisitorSchema(schema, err)
	}
	if err := f.drop(schema); err != nil {
		return retainVisitorSchema(schema, err)
	}
	return nil
}
