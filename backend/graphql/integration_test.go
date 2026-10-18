package graphql

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"

	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/proposal"
	"github.com/AyushM7/fleetops/backend/sim"
	"github.com/jackc/pgx/v5"
)

func integrationPool(t *testing.T) *db.Pool {
	t.Helper()
	url := os.Getenv("FLEETOPS_TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("FLEETOPS_REQUIRE_DB") == "1" {
			t.Fatal("FLEETOPS_TEST_DATABASE_URL is required")
		}
		t.Skip("FLEETOPS_TEST_DATABASE_URL is unavailable")
	}
	cfg := db.DefaultConfig(url)
	admin, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	cfg.Schema = "fleetops_test_" + hex.EncodeToString(random[:])
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

type testAPI struct {
	server  *httptest.Server
	service *fleet.Service
	pool    *db.Pool
	tokens  map[string]string
	now     atomic.Int64
}

func authFixture(t *testing.T) (*auth.Authenticator, map[string]string) {
	t.Helper()
	tokens := map[string]string{}
	credentials := []auth.Credential{}
	for _, subject := range []string{"viewer", "proposer", "operator", "self"} {
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			t.Fatal(err)
		}
		token := hex.EncodeToString(random[:])
		tokens[subject] = token
		hash := sha256.Sum256([]byte(token))
		role := auth.Role(subject)
		identity := subject
		if subject == "self" {
			role = auth.Operator
			identity = "proposer"
		}
		credentials = append(credentials, auth.Credential{Subject: identity, Role: role, SHA256: hex.EncodeToString(hash[:])})
	}
	raw, _ := json.Marshal(credentials)
	a, err := auth.New(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return a, tokens
}
func integrationAPI(t *testing.T) *testAPI {
	t.Helper()
	a, tokens := authFixture(t)
	api := &testAPI{pool: integrationPool(t), tokens: tokens}
	api.now.Store(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC).UnixNano())
	cfg := sim.DefaultConfig()
	cfg.RobotStarts[0] = cfg.Stations[0]
	cfg.InitialJobs = 0
	cfg.AutoDispatch = false
	service, err := fleet.New(cfg, 42, api.pool, func() time.Time { return time.Unix(0, api.now.Load()).UTC() }, func(ctx context.Context, id proposal.OperatorIdentity) bool {
		p, ok := auth.FromContext(ctx)
		return ok && p.Role == auth.Operator && p.Subject == id.Subject
	})
	if err != nil {
		t.Fatal(err)
	}
	api.service = service
	api.server = httptest.NewServer(NewHandler(service, a))
	t.Cleanup(api.server.Close)
	return api
}
func (api *testAPI) request(t *testing.T, role, query string, variables map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	req, err := http.NewRequest("POST", api.server.URL, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+api.tokens[role])
	response, err := api.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || result["errors"] != nil {
		t.Fatalf("GraphQL response: %d %+v", response.StatusCode, result)
	}
	return result["data"].(map[string]any)
}

const outcome = ` { proposalId state execution auditCommitted executionAttempted error }`

func (api *testAPI) submit(t *testing.T, role string) map[string]any {
	t.Helper()
	state := api.request(t, role, `{fleetState{observationId}}`, nil)["fleetState"].(map[string]any)
	return api.request(t, role, `mutation($input:SetChargingInput!){submitProposal(input:$input)`+outcome+`}`, map[string]any{"input": map[string]any{"robotId": "R1", "stationId": "S1", "expiresAt": time.Unix(0, api.now.Load()).Add(time.Minute).UTC().Format(time.RFC3339Nano), "expectedObservationId": state["observationId"]}})["submitProposal"].(map[string]any)
}
func (api *testAPI) decide(t *testing.T, role, action, id string) map[string]any {
	t.Helper()
	return api.request(t, role, `mutation($id:ID!){`+action+`(id:$id)`+outcome+`}`, map[string]any{"id": id})[action].(map[string]any)
}
func denied(t *testing.T, result map[string]any) {
	t.Helper()
	if result["error"] == nil || result["executionAttempted"] != false || result["auditCommitted"] != true {
		t.Fatalf("expected committed denial: %+v", result)
	}
}
func TestIntegrationHTTPCharging(t *testing.T) {
	api := integrationAPI(t)
	p := api.submit(t, "proposer")
	if p["error"] != nil || p["auditCommitted"] != true {
		t.Fatal(p)
	}
	id := p["proposalId"].(string)
	read := api.request(t, "viewer", `query($id:ID!){proposal(id:$id){id state submittedBy currentRuntime}}`, map[string]any{"id": id})["proposal"].(map[string]any)
	if read["state"] != "pending" || read["submittedBy"] != "proposer" || read["currentRuntime"] != true {
		t.Fatal(read)
	}
	for _, role := range []string{"viewer", "proposer", "self"} {
		denied(t, api.decide(t, role, "approveProposal", id))
	}
	if api.service.Snapshot().Robots[0].Status != sim.RobotIdle {
		t.Fatal("denied approval executed")
	}
	result := api.decide(t, "operator", "approveProposal", id)
	if result["error"] != nil || result["execution"] != "succeeded" || result["executionAttempted"] != true || result["auditCommitted"] != true {
		t.Fatal(result)
	}
	state := api.request(t, "viewer", `{fleetState{robots{id status} stations{id occupiedBy}}}`, nil)["fleetState"].(map[string]any)
	if state["robots"].([]any)[0].(map[string]any)["status"] != "charging" {
		t.Fatal(state)
	}
	denied(t, api.decide(t, "operator", "approveProposal", id))
	var count int
	var execution string
	if err := api.pool.QueryRow(context.Background(), `SELECT execution FROM fleet_proposals WHERE id=$1`, id).Scan(&execution); err != nil || execution != "succeeded" {
		t.Fatal("persisted outcome", err)
	}
	if err := api.pool.QueryRow(context.Background(), `SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='execute' AND accepted`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("execution audit", count, err)
	}
}
func TestIntegrationAuthorizationMatrix(t *testing.T) {
	for _, role := range []string{"viewer", "proposer", "operator"} {
		t.Run(role, func(t *testing.T) {
			api := integrationAPI(t)
			api.request(t, role, `{fleetState{tick} proposal(id:"absent"){id}}`, nil)
			created := api.submit(t, role)
			if role == "viewer" {
				denied(t, created)
				return
			}
			if created["error"] != nil {
				t.Fatal(created)
			}
			id := created["proposalId"].(string)
			for _, actor := range []string{"viewer", "proposer"} {
				denied(t, api.decide(t, actor, "rejectProposal", id))
			}
			rejected := api.decide(t, "operator", "rejectProposal", id)
			if rejected["state"] != "rejected" || rejected["error"] != nil {
				t.Fatal(rejected)
			}
			denied(t, api.decide(t, "operator", "approveProposal", id))
			if api.service.Snapshot().Robots[0].Status != sim.RobotIdle {
				t.Fatal("rejection mutated simulator")
			}
		})
	}
}
func TestIntegrationExpiredAndRestart(t *testing.T) {
	api := integrationAPI(t)
	created := api.submit(t, "proposer")
	id := created["proposalId"].(string)
	api.now.Add(int64(time.Minute))
	denied(t, api.decide(t, "operator", "approveProposal", id))
	if api.service.Snapshot().Robots[0].Status != sim.RobotIdle {
		t.Fatal("expired executed")
	}
	fresh := api.submit(t, "proposer")
	id = fresh["proposalId"].(string)

	// Restart with independently configured authentication and the same database.
	a, tokens := authFixture(t)
	api.tokens = tokens
	cfg := sim.DefaultConfig()
	cfg.RobotStarts[0] = cfg.Stations[0]
	cfg.InitialJobs = 0
	cfg.AutoDispatch = false
	restarted, err := fleet.New(cfg, 42, api.pool, func() time.Time { return time.Unix(0, api.now.Load()).UTC() }, func(ctx context.Context, id proposal.OperatorIdentity) bool {
		p, ok := auth.FromContext(ctx)
		return ok && p.Role == auth.Operator && p.Subject == id.Subject
	})
	if err != nil {
		t.Fatal(err)
	}
	api.server.Close()
	api.server = httptest.NewServer(NewHandler(restarted, a))
	defer api.server.Close()
	read := api.request(t, "viewer", `query($id:ID!){proposal(id:$id){currentRuntime}}`, map[string]any{"id": id})["proposal"].(map[string]any)
	if read["currentRuntime"] != false {
		t.Fatal(read)
	}
	denied(t, api.decide(t, "operator", "approveProposal", id))
	if restarted.Snapshot().Robots[0].Status != sim.RobotIdle {
		t.Fatal("prior runtime executed")
	}
}
func TestIntegrationConcurrentHTTPApprovals(t *testing.T) {
	api := integrationAPI(t)
	id := api.submit(t, "proposer")["proposalId"].(string)
	var workers sync.WaitGroup
	var executions atomic.Int32
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			result := api.decide(t, "operator", "approveProposal", id)
			if result["executionAttempted"] == true {
				executions.Add(1)
			}
		})
	}
	workers.Wait()
	if executions.Load() != 1 {
		t.Fatal("execution count", executions.Load())
	}
	var count int
	if err := api.pool.QueryRow(context.Background(), `SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='execute' AND accepted`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent execution audit", count, err)
	}
}
func TestIntegrationHTTPPersistenceFailure(t *testing.T) {
	api := integrationAPI(t)
	id := api.submit(t, "proposer")["proposalId"].(string)
	api.pool.Close()
	result := api.decide(t, "operator", "approveProposal", id)
	if result["error"] == nil || result["auditCommitted"] != false || result["executionAttempted"] != false || !api.service.Poisoned() {
		t.Fatal(result)
	}
	if api.service.Snapshot().Robots[0].Status != sim.RobotIdle {
		t.Fatal("database failure executed")
	}
}
