//go:build fleetops_test

package fleet_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/jackc/pgx/v5"
)

type visitorAPI struct {
	client *http.Client
	origin string
}

func (v visitorAPI) request(path, generation, tick, body string) (int, []byte, error) {
	method := "POST"
	if path == "/" {
		method = "GET"
	}
	r, err := http.NewRequest(method, v.origin+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	r.Header.Set("Origin", v.origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-FleetOps-Demo", "1")
	r.Header.Set("X-FleetOps-Generation", generation)
	r.Header.Set("X-FleetOps-Tick", tick)
	response, err := v.client.Do(r)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	return response.StatusCode, raw, err
}

func (v visitorAPI) must(t *testing.T, path, generation, tick, body string) []byte {
	t.Helper()
	code, raw, err := v.request(path, generation, tick, body)
	if err != nil || code != 200 {
		t.Fatalf("%s: code=%d error=%v body=%s", path, code, err, raw)
	}
	return raw
}

func (v visitorAPI) status(t *testing.T) demoStatus {
	t.Helper()
	var status demoStatus
	if err := json.Unmarshal(v.must(t, "/demo/status", "", "", "{}"), &status); err != nil || !status.Ready || status.Snapshot == nil {
		t.Fatal("invalid status", err)
	}
	return status
}

func visitorIntegration(t *testing.T) (*visitorGateway, visitorFactory, visitorAPI, visitorAPI) {
	t.Helper()
	root := os.Getenv("FLEETOPS_P405_ROOT")
	if root == "" {
		t.Skip("run evals/phase8/verify.ps1 for actual PostgreSQL/adapter integration")
	}
	cfg := db.DefaultConfig(os.Getenv("FLEETOPS_TEST_DATABASE_URL"))
	admin, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	f := visitorFactory{admin: admin, config: cfg, evidenceDir: visitorTestEvidenceDir(t)}
	m := newVisitorManager(f.create)
	t.Cleanup(func() {
		if err := m.close(); err != nil {
			t.Error(err)
		}
	})
	g, err := newVisitorGateway(m, filepath.Join(root, "web", "dist"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(g)
	g.origin = "http://" + server.Listener.Addr().String()
	server.Start()
	t.Cleanup(server.Close)
	newClient := func() visitorAPI {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		v := visitorAPI{client: &http.Client{Jar: jar, Timeout: 90 * time.Second}, origin: server.URL}
		v.must(t, "/", "", "", "")
		return v
	}
	return g, f, newClient(), newClient()
}

func visitorDecision(kind, id string) string {
	raw, _ := json.Marshal(map[string]any{"query": `mutation($id:ID!){` + kind + `Proposal(id:$id){state execution executionAttempted error}}`, "variables": map[string]string{"id": id}})
	return string(raw)
}

func visitorSchema(t *testing.T, v visitorAPI, generation string) string {
	t.Helper()
	var response struct {
		Data struct {
			PendingProposals []struct{ SubmittedBy string }
		}
	}
	raw := v.must(t, "/graphql", generation, "", `{"query":"{pendingProposals{submittedBy}}"}`)
	if json.Unmarshal(raw, &response) != nil || len(response.Data.PendingProposals) != 1 {
		t.Fatal(string(raw))
	}
	return "fleetops_pub01_" + strings.Split(response.Data.PendingProposals[0].SubmittedBy, ":")[0]
}

func TestPUB01TwoVisitorIntegration(t *testing.T) {
	g, f, a, b := visitorIntegration(t)
	firstA, firstB := a.status(t), b.status(t)
	if firstA.Evidence.ProposalSource != demoSourceGoScripted || firstB.Evidence.ProposalSource != demoSourceGoScripted {
		t.Fatal("public proposal source is not Go-scripted")
	}
	if firstA.Generation == firstB.Generation || firstA.Evidence.RuntimeID == firstB.Evidence.RuntimeID || firstA.Snapshot.ObservationID == firstB.Snapshot.ObservationID || firstA.Evidence.ProposalID == firstB.Evidence.ProposalID {
		t.Fatal("identities shared")
	}
	schemaA, schemaB := visitorSchema(t, a, firstA.Generation), visitorSchema(t, b, firstB.Generation)
	if schemaA == schemaB {
		t.Fatal("database or operator identity shared")
	}
	a.must(t, "/", "", "", "")
	if got := a.status(t); !reflect.DeepEqual(got, firstA) {
		t.Fatal("refresh reset state")
	}
	foreignID := firstA.Evidence.ProposalID
	raw := b.must(t, "/graphql", firstB.Generation, "", fmt.Sprintf(`{"query":"{proposal(id:\"%s\"){id submittedBy state}}"}`, foreignID))
	if !strings.Contains(string(raw), `"proposal":null`) {
		t.Fatal("foreign proposal leaked", string(raw))
	}
	for _, kind := range []string{"approve", "reject"} {
		raw = b.must(t, "/graphql", firstB.Generation, "", visitorDecision(kind, foreignID))
		if !strings.Contains(string(raw), `"executionAttempted":false`) || strings.Contains(string(raw), `"error":null`) {
			t.Fatal("foreign decision accepted", string(raw))
		}
	}
	if got := a.status(t); !reflect.DeepEqual(firstA, got) {
		t.Fatal("foreign decision mutated A")
	}
	// A database-backed incomplete AGENT-05 record is a test fixture, never live inference.
	agentID := "A" + demoRandom()[:32]
	quotedA := pgx.Identifier{schemaA}.Sanitize()
	if _, err := f.admin.Exec(context.Background(), `INSERT INTO `+quotedA+`.fleet_agent_runs(id,runtime_id,observation_id,owner_subject,started_at,metadata,facts) VALUES($1,$2,$3,'fixture-proposer',now(),'{"version":"agent-run-v1","reportedSource":"mocked"}','{"tick":42}')`, agentID, firstA.Evidence.RuntimeID, firstA.Snapshot.ObservationID); err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf(`{"query":"{agentRun(id:\"%s\"){id runtimeId data} agentRuns(limit:20){id}}"}`, agentID)
	if raw := a.must(t, "/graphql", firstA.Generation, "", query); !strings.Contains(string(raw), agentID) {
		t.Fatal("own provenance missing", string(raw))
	}
	if raw := b.must(t, "/graphql", firstB.Generation, "", query); strings.Contains(string(raw), agentID) || strings.Contains(string(raw), firstA.Evidence.RuntimeID) {
		t.Fatal("foreign provenance leaked", string(raw))
	}
	if raw := a.must(t, "/graphql", firstA.Generation, "", visitorDecision("approve", foreignID)); !strings.Contains(string(raw), `"execution":"succeeded"`) {
		t.Fatal("own approval failed", string(raw))
	}
	if got := b.status(t); !reflect.DeepEqual(firstB, got) {
		t.Fatal("A approval changed B")
	}
	a.must(t, "/demo/step", firstA.Generation, fmt.Sprint(firstA.Snapshot.Tick), "{}")
	if got := a.status(t); got.Snapshot.Tick != firstA.Snapshot.Tick+1 {
		t.Fatal("A did not step")
	}
	if got := b.status(t); !reflect.DeepEqual(firstB, got) {
		t.Fatal("A step moved B")
	}
	a.must(t, "/demo/scenario/healthy-operations", firstA.Generation, "", "{}")
	healthy := a.status(t)
	if healthy.ScenarioID != "healthy-operations" || healthy.Snapshot.Tick != 0 || healthy.Generation == firstA.Generation {
		t.Fatal("scenario failed")
	}
	if got := b.status(t); !reflect.DeepEqual(firstB, got) {
		t.Fatal("A scenario changed B")
	}
	a.must(t, "/demo/restart", healthy.Generation, "", "{}")
	restarted := a.status(t)
	if restarted.Generation == healthy.Generation || restarted.Snapshot.Tick != 0 || restarted.Snapshot.ObservationID == healthy.Snapshot.ObservationID {
		t.Fatal("restart reused runtime")
	}
	if raw := a.must(t, "/graphql", restarted.Generation, "", visitorDecision("approve", foreignID)); !strings.Contains(string(raw), `"executionAttempted":false`) || strings.Contains(string(raw), `"error":null`) {
		t.Fatal("old proposal executed in new runtime", string(raw))
	}
	if code, _, _ := a.request("/graphql", firstA.Generation, "", visitorDecision("approve", foreignID)); code != 409 {
		t.Fatal("retired generation accepted", code)
	}
	if got := b.status(t); !reflect.DeepEqual(firstB, got) {
		t.Fatal("A restart replaced B")
	}
	var accepted int
	if err := f.admin.QueryRow(context.Background(), `SELECT count(*) FROM `+pgx.Identifier{schemaB}.Sanitize()+`.fleet_audit WHERE accepted AND attempt IN ('approve','execute')`).Scan(&accepted); err != nil || accepted != 0 {
		t.Fatal("A approval executed in B", accepted, err)
	}
	// Advance the server deadline under its ownership gate, then await real worker cleanup.
	var sessionA *visitorSession
	g.manager.mu.Lock()
	for _, s := range g.manager.sessions {
		if s.controller.run.evidence.RuntimeID != firstB.Evidence.RuntimeID {
			sessionA = s
		}
	}
	g.manager.mu.Unlock()
	sessionA.mu.Lock()
	sessionA.deadline = time.Now().Add(-time.Second)
	sessionA.mu.Unlock()
	if code, _, _ := a.request("/demo/status", "", "", "{}"); code != 401 {
		t.Fatal("expired session accepted", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var exists bool
		if err := f.admin.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, schemaA).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expired schema not cleaned")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := b.status(t); !reflect.DeepEqual(firstB, got) {
		t.Fatal("expiry affected B")
	}
	t.Log("PASS: distinct runtimes/observations, refresh, private proposals/provenance/audits, independent approval/step/scenario/restart, expiry and schema cleanup; scripted Go presets, no agent runtime or model inference")
}

func TestPUB01ConcurrentRestartsIntegration(t *testing.T) {
	_, _, a, b := visitorIntegration(t)
	var wg sync.WaitGroup
	for _, client := range []visitorAPI{a, b} {
		wg.Go(func() {
			seen := map[string]bool{}
			for range 3 {
				status := client.status(t)
				if seen[status.Generation] {
					t.Error("reused generation")
					return
				}
				seen[status.Generation] = true
				client.must(t, "/demo/restart", status.Generation, "", "{}")
				if code, _, err := client.request("/demo/restart", status.Generation, "", "{}"); err != nil || code != 409 {
					t.Error("stale restart accepted", code, err)
				}
			}
		})
	}
	wg.Wait()
	if a.status(t).Generation == b.status(t).Generation {
		t.Fatal("concurrent runtimes shared")
	}
}

func TestPUB01DatabasePoisonIsolationIntegration(t *testing.T) {
	_, f, a, b := visitorIntegration(t)
	sa, sb := a.status(t), b.status(t)
	schema := pgx.Identifier{visitorSchema(t, a, sa.Generation)}.Sanitize()
	if _, err := f.admin.Exec(context.Background(), "ALTER TABLE "+schema+".fleet_audit RENAME TO unavailable_audit"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.admin.Exec(context.Background(), "ALTER TABLE "+schema+".unavailable_audit RENAME TO fleet_audit"); err != nil {
			t.Error(err)
		}
	}()
	raw := a.must(t, "/graphql", sa.Generation, "", visitorDecision("approve", sa.Evidence.ProposalID))
	if strings.Contains(string(raw), `"error":null`) || strings.Contains(string(raw), `"execution":"succeeded"`) {
		t.Fatal("database failure reported execution", string(raw))
	}
	if !a.status(t).Snapshot.Poisoned {
		t.Fatal("uncertain runtime not poisoned")
	}
	if code, _, _ := a.request("/demo/restart", sa.Generation, "", "{}"); code != 503 {
		t.Fatal("capture failure published replacement", code)
	}
	if got := b.status(t); !reflect.DeepEqual(sb, got) {
		t.Fatal("poison or database failure crossed sessions")
	}
	if raw := b.must(t, "/graphql", sb.Generation, "", visitorDecision("approve", sb.Evidence.ProposalID)); !strings.Contains(string(raw), `"execution":"succeeded"`) {
		t.Fatal("B cannot execute after A failure", string(raw))
	}
}

func TestPUB01FourPresetsIntegration(t *testing.T) {
	_, _, a, b := visitorIntegration(t)
	baselineB := b.status(t)
	for _, scenario := range []struct {
		id         string
		tick       int
		actionable bool
	}{
		{"routine-charging", 1000, true}, {"healthy-operations", 0, false},
		{"blocked-no-candidate", 42, false}, {"blocked-reassignment", 42, true},
	} {
		t.Run(scenario.id, func(t *testing.T) {
			before := a.status(t)
			a.must(t, "/demo/scenario/"+scenario.id, before.Generation, "", "{}")
			status := a.status(t)
			expectedSource := demoSourceNone
			if scenario.actionable {
				expectedSource = demoSourceGoScripted
			}
			if status.Evidence.ProposalSource != expectedSource {
				t.Fatal("preset provenance is incorrect", status.Evidence.ProposalSource)
			}
			if status.ScenarioID != scenario.id || status.Snapshot.Tick != scenario.tick || (status.Evidence.ProposalID != "") != scenario.actionable {
				t.Fatal("preset behavior changed", status.ScenarioID, status.Snapshot.Tick)
			}
			if scenario.id == "blocked-no-candidate" && len(status.Snapshot.ReassignmentCandidates) != 0 {
				t.Fatal("unsafe transfer admitted")
			}
			if scenario.actionable {
				if code, _, _ := a.request("/demo/step", status.Generation, fmt.Sprint(scenario.tick), "{}"); code != 409 {
					t.Fatal("ticked before human decision", code)
				}
				if raw := a.must(t, "/graphql", status.Generation, "", visitorDecision("approve", status.Evidence.ProposalID)); !strings.Contains(string(raw), `"execution":"succeeded"`) {
					t.Fatal(string(raw))
				}
			}
			a.must(t, "/demo/step", status.Generation, fmt.Sprint(scenario.tick), "{}")
			after := a.status(t)
			if after.Snapshot.Tick != scenario.tick+1 {
				t.Fatal("preset step failed")
			}
			if scenario.id == "routine-charging" && (after.Snapshot.Robots[0].Battery != 52 || after.Snapshot.Stations[0].OccupiedBy != "R1") {
				t.Fatal("charging behavior changed")
			}
			if scenario.id == "blocked-reassignment" && after.Snapshot.Jobs[0].AssignedRobot != "R3" {
				t.Fatal("reassignment behavior changed")
			}
			if !reflect.DeepEqual(b.status(t), baselineB) {
				t.Fatal("A preset mutated B")
			}
		})
	}
}

func visitorTestEvidenceDir(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("FLEETOPS_PUB01_EVIDENCE"); root != "" {
		path := filepath.Join(root, t.Name()+"-"+demoRandom()[:16])
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return t.TempDir()
}

func visitorArchiveFixture(t *testing.T) (visitorFactory, *db.Pool, visitorSchemaOwner, string) {
	t.Helper()
	if os.Getenv("FLEETOPS_P405_ROOT") == "" {
		t.Skip("required PostgreSQL acceptance harness unavailable")
	}
	cfg := db.DefaultConfig(os.Getenv("FLEETOPS_TEST_DATABASE_URL"))
	admin, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	dir := visitorTestEvidenceDir(t)
	f := visitorFactory{admin: admin, config: cfg, evidenceDir: dir}
	owner := visitorSchemaOwner{name: "fleetops_pub01_" + demoRandom()[:32]}
	quoted := pgx.Identifier{owner.name}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(context.Background(), `SELECT oid FROM pg_namespace WHERE nspname=$1`, owner.name).Scan(&owner.oid); err != nil {
		t.Fatal(err)
	}
	cfg.Schema = owner.name
	pool, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, tokens, identity, err := visitorCredentials()
	if err != nil {
		t.Fatal(err)
	}
	factory := demoFactory{pool: pool, auth: a, tokens: tokens, evidenceDir: dir, proposerSubject: identity + ":proposer", operatorSubject: identity + ":operator", proposalSource: demoSourceGoScripted,
		proposeRuntime: func(ctx context.Context, handler http.Handler, path string) (string, error) {
			return visitorProposal(ctx, handler, tokens["proposer"], path)
		}}
	if _, err := factory.create(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f, pool, owner, dir
}

func assertVisitorSchemaRetained(t *testing.T, f visitorFactory, pool *db.Pool, owner visitorSchemaOwner) {
	t.Helper()
	if err := pool.Ping(context.Background()); err == nil {
		t.Fatal("visitor pool was not shut down")
	}
	if err := f.confirmOwner(context.Background(), owner); err != nil {
		t.Fatal("owned schema not retained", err)
	}
	var proposals int
	if err := f.admin.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{owner.name}.Sanitize()+".fleet_proposals").Scan(&proposals); err != nil || proposals != 1 {
		t.Fatal("retained committed proposal unavailable", proposals, err)
	}
}

func cleanupVisitorArchiveFixture(t *testing.T, f visitorFactory, owner visitorSchemaOwner, dir string) {
	t.Helper()
	cfg := f.config
	cfg.Schema = owner.name
	pool, err := db.Open(context.Background(), cfg)
	if err != nil {
		t.Error(err)
		return
	}
	f.archiveCommit = nil
	if err := f.release(pool, owner, dir); err != nil {
		t.Error("fixture retained; verified archival cleanup failed", err)
	}
}

func TestPUB01DatabaseArchiveFailuresRetainEvidence(t *testing.T) {
	for _, fault := range []string{"database-read", "write-failure", "truncated-readback", "uncertain-commit", "ownership-mismatch"} {
		t.Run(fault, func(t *testing.T) {
			f, pool, owner, dir := visitorArchiveFixture(t)
			defer cleanupVisitorArchiveFixture(t, f, owner, dir)
			target := owner
			switch fault {
			case "database-read":
				quoted := pgx.Identifier{owner.name}.Sanitize()
				if _, err := f.admin.Exec(context.Background(), "ALTER TABLE "+quoted+".fleet_audit RENAME TO unavailable_audit"); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := f.admin.Exec(context.Background(), "ALTER TABLE "+quoted+".unavailable_audit RENAME TO fleet_audit"); err != nil {
						t.Error(err)
					}
				}()
			case "write-failure":
				f.archiveCommit = func(string, []byte) error { return errors.New("archive write failed") }
			case "truncated-readback":
				f.archiveCommit = func(path string, raw []byte) error { return commitVisitorArchive(path, raw[:len(raw)/2]) }
			case "uncertain-commit":
				f.archiveCommit = func(path string, raw []byte) error {
					if err := commitVisitorArchive(path, raw); err != nil {
						return err
					}
					return errors.New("archive acknowledgement uncertain")
				}
			case "ownership-mismatch":
				target.oid++
			}
			var cleanupErr error
			if fault == "uncertain-commit" {
				captureErr := errors.New("controller capture failed with uncertain archive")
				c := unitController()
				c.run.capture = func(context.Context) error { return captureErr }
				s := &visitorSession{controller: c, operator: demoRandom(), cleanup: func() error { return f.release(pool, target, dir) }}
				cleanupErr = s.close()
				if !errors.Is(cleanupErr, captureErr) || !s.closed || s.operator != "" {
					t.Fatal("combined capture/archive failure did not revoke capabilities or report capture error", cleanupErr)
				}
			} else {
				cleanupErr = f.release(pool, target, dir)
			}
			if !errors.Is(cleanupErr, errVisitorRetained) {
				t.Fatal("uncertain archive did not retain schema", cleanupErr)
			}
			assertVisitorSchemaRetained(t, f, pool, owner)
		})
	}
}

func TestPUB01ControllerCaptureFailureAllowsVerifiedDatabaseArchive(t *testing.T) {
	f, pool, owner, dir := visitorArchiveFixture(t)
	c := unitController()
	c.run.capture = func(context.Context) error { return errors.New("controller capture failed") }
	s := &visitorSession{controller: c, operator: demoRandom(), cleanup: func() error { return f.release(pool, owner, dir) }}
	if err := s.close(); err == nil || errors.Is(err, errVisitorRetained) {
		t.Fatal("controller failure or verified archive outcome misreported", err)
	}
	if !s.closed || s.operator != "" || pool.Ping(context.Background()) == nil {
		t.Fatal("closed visitor retained capabilities or pool")
	}
	var exists bool
	if err := f.admin.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, owner.name).Scan(&exists); err != nil || exists {
		t.Fatal("verified archived disposable schema not removed", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "database-final-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal("verified database archive missing", files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil || verifyVisitorArchive(raw, owner) != nil {
		t.Fatal("database archive incomplete", err)
	}
	var archive visitorArchive
	if json.Unmarshal(raw, &archive) != nil || len(archive.Proposals) != 1 || len(archive.Audit) != 1 {
		t.Fatal("committed workflow evidence lost")
	}
}

func TestPUB01ProposalSourceClassification(t *testing.T) {
	for _, source := range []demoProposalSource{demoSourceGoScripted, demoSourceMastra, demoSourceRecorded, demoSourceLive} {
		if got := (demoFactory{proposalSource: source}).source("proposal"); got != source {
			t.Fatal("source reclassified", got)
		}
	}
	for _, source := range []demoProposalSource{"", "unrecognized"} {
		f := demoFactory{proposalSource: source, proposeRuntime: func(context.Context, http.Handler, string) (string, error) { return "", nil }}
		if f.source("proposal") != demoSourceUnknown || f.source("") != demoSourceNone {
			t.Fatal("unknown source or no-action misclassified")
		}
	}
	if (demoFactory{}).source("proposal") != demoSourceMastra {
		t.Fatal("existing deterministic Mastra source lost")
	}
}
