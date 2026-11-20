package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestAutomaticPrivateHTTPRuntimeBoundaryIntegration(t *testing.T) {
	s, pool, now, ctx := automaticFixture(t)
	cfg := sim.WarehouseV2Config()
	cfg.AutoDispatch = false
	other, err := New(cfg, 42, pool, func() time.Time { return *now }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	credentials := make([]auth.Credential, 0, 3)
	tokens := make(map[string]string)
	for _, entry := range []struct {
		subject, runtime string
		role             auth.Role
	}{
		{"local", s.RuntimeID(), auth.Automatic}, {"foreign", other.RuntimeID(), auth.Automatic}, {"legacy", "", auth.Operator},
	} {
		token := strings.Repeat("isolated-command-fixture-"+entry.subject, 2)
		digest := sha256.Sum256([]byte(token))
		credentials = append(credentials, auth.Credential{Subject: entry.subject, RuntimeID: entry.runtime, Role: entry.role, SHA256: hex.EncodeToString(digest[:])})
		tokens[entry.subject] = token
	}
	config, _ := json.Marshal(credentials)
	authenticator, err := auth.New(string(config))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAutomaticHandler(s, authenticator)
	request := func(handler http.Handler, method, path, subject, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token := tokens[subject]; token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := request(NewAutomaticHandler(other, authenticator), "GET", "/state", "foreign", ""); w.Code != 403 {
		t.Fatalf("new runtime enabled automatic actions by default: %d", w.Code)
	}
	observed := request(handler, "GET", "/state", "local", "")
	var view AutomaticObservation
	if observed.Code != 200 || json.Unmarshal(observed.Body.Bytes(), &view) != nil || view.RuntimeID != s.RuntimeID() ||
		view.Versions["J1"] == 0 || view.Versions["R1"] == 0 {
		t.Fatalf("host cannot obtain a current versioned observation: %d %s", observed.Code, observed.Body.String())
	}
	cmd := AutomaticCommand{
		RuntimeID: view.RuntimeID, ObservationID: view.ObservationID, ExpiresAt: view.ExpiresAt,
		IdempotencyKey: "http-command-key", Preconditions: map[string]uint64{"J1": view.Versions["J1"], "R1": view.Versions["R1"]},
		AssignJobBatch: &sim.AssignJobBatchRequest{Assignments: []sim.JobAssignment{{JobID: "J1", RobotID: "R1"}}},
	}
	payload, _ := json.Marshal(cmd)
	for _, tc := range []struct {
		method, path, subject, body string
		status                      int
	}{
		{"GET", "/state", "foreign", "", 403},
		{"POST", "/commands", "foreign", string(payload), 403},
		{"POST", "/commands", "legacy", string(payload), 403},
		{"POST", "/commands", "", string(payload), 401},
		{"POST", "/tick", "local", "{}", 404},
		{"POST", "/fault", "local", "{}", 404},
		{"POST", "/commands", "local", `{"injectFault":{"robotId":"R1"}}`, 400},
		{"POST", "/commands", "local", `{"runtimeId":"a","runtimeID":"b"}`, 400},
		{"POST", "/commands", "local", strings.Repeat(" ", automaticBodyLimit+1), 413},
	} {
		w := request(handler, tc.method, tc.path, tc.subject, tc.body)
		if w.Code != tc.status {
			t.Fatalf("unexpected authorization result for %s %s as %q: %d %s", tc.method, tc.path, tc.subject, w.Code, w.Body.String())
		}
	}
	if s.Snapshot().Jobs[0].AssignedRobot != "" || other.Snapshot().Jobs[0].AssignedRobot != "" {
		t.Fatal("denied HTTP requests mutated simulator state")
	}
	var rejected int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM fleet_automatic_audit WHERE status='rejected' AND command='{}'").Scan(&rejected); err != nil || rejected != 5 {
		t.Fatalf("malformed or unauthorized authenticated requests were not safely audited: count=%d %v", rejected, err)
	}
	if err := s.SetAutomaticCommandsEnabled(false); err != nil {
		t.Fatal(err)
	}
	if w := request(handler, "POST", "/commands", "local", string(payload)); w.Code != 403 {
		t.Fatalf("disabled command returned %d", w.Code)
	}
	if err := s.SetAutomaticCommandsEnabled(true); err != nil {
		t.Fatal(err)
	}
	if w := request(handler, "POST", "/commands", "local", string(payload)); w.Code != 409 {
		t.Fatalf("old observation survived disable: %d", w.Code)
	}
	observed = request(handler, "GET", "/state", "local", "")
	if observed.Code != 200 || json.Unmarshal(observed.Body.Bytes(), &view) != nil {
		t.Fatalf("could not refresh host observation: %d", observed.Code)
	}
	cmd.ObservationID, cmd.ExpiresAt, cmd.IdempotencyKey = view.ObservationID, view.ExpiresAt, "http-command-fresh"
	cmd.Preconditions = map[string]uint64{"J1": view.Versions["J1"], "R1": view.Versions["R1"]}
	payload, _ = json.Marshal(cmd)
	w := request(handler, "POST", "/commands", "local", string(payload))
	var response automaticHTTPResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Receipt == nil ||
		response.Receipt.Execution != Succeeded || !response.Receipt.AuditCommitted || s.Snapshot().Jobs[0].AssignedRobot != "R1" {
		t.Fatalf("authorized command failed: %d %s", w.Code, w.Body.String())
	}
}

func TestAutomaticRejectsWhenAttemptCannotBePersistedIntegration(t *testing.T) {
	s, pool, _, ctx := automaticFixture(t)
	cmd := automaticBatch(t, s, ctx)
	before := s.Snapshot()
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_automatic_stage() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test-only database failure'; END; $$
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER fail_stage BEFORE INSERT ON fleet_automatic_audit
		FOR EACH ROW EXECUTE FUNCTION fail_automatic_stage()`); err != nil {
		t.Fatal(err)
	}
	result, err := s.ExecuteAutomatic(ctx, cmd)
	if !errors.Is(err, ErrPersistence) || result.AuditCommitted || result.Execution != Unknown || !s.Poisoned() ||
		!reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatalf("failed audit admission allowed mutation: %+v %v", result, err)
	}
}

func TestAutomaticConcurrentConflictingAssignmentsIntegration(t *testing.T) {
	s, pool, _, ctx := automaticFixture(t)
	view, err := s.ReadAutomaticState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	errorsSeen := make(chan error, 2)
	var workers sync.WaitGroup
	for _, job := range []string{"J1", "J2"} {
		command := AutomaticCommand{
			RuntimeID: view.RuntimeID, ObservationID: view.ObservationID, ExpiresAt: view.ExpiresAt,
			IdempotencyKey: "conflict-" + job,
			Preconditions:  map[string]uint64{job: view.Versions[job], "R1": view.Versions["R1"]},
			AssignJobBatch: &sim.AssignJobBatchRequest{Assignments: []sim.JobAssignment{{JobID: job, RobotID: "R1"}}},
		}
		workers.Go(func() {
			result, err := s.ExecuteAutomatic(ctx, command)
			if !result.AuditCommitted {
				t.Errorf("conflicting attempt missing durable audit: %+v %v", result, err)
			}
			errorsSeen <- err
		})
	}
	workers.Wait()
	close(errorsSeen)
	applied, stale := 0, 0
	for err := range errorsSeen {
		if err == nil {
			applied++
		} else if errors.Is(err, ErrObservation) {
			stale++
		} else {
			t.Fatalf("unexpected conflict result: %v", err)
		}
	}
	var recorded int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM fleet_automatic_audit WHERE status='applied'").Scan(&recorded)
	if err != nil || applied != 1 || stale != 1 || recorded != 1 {
		t.Fatalf("conflicting commands did not serialize: applied=%d stale=%d records=%d %v", applied, stale, recorded, err)
	}
}

func TestAutomaticReassignmentTransfersUnstartedV2JobIntegration(t *testing.T) {
	pool := integrationPool(t)
	s, err := New(sim.WarehouseV2Config(), 99, pool, func() time.Time { return testTime }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetAutomaticCommandsEnabled(true); err != nil {
		t.Fatal(err)
	}
	for range 33 {
		if err := s.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	ctx := automaticContext(t, s.RuntimeID())
	observed, err := s.ObserveOperations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const job, source, recipient = "J10", "R7", "R2"
	command, err := observed.Prepare(OperationsDecision{
		Kind: "reassign", Reassignment: &safety.ReassignUnstartedJobRequest{JobID: job, FromRobotID: source, ToRobotID: recipient},
	})
	if err != nil {
		t.Fatalf("eligible reassignment was not offered: %v", err)
	}
	result, err := s.ExecuteAutomatic(ctx, *command)
	state := s.Snapshot()
	reassigned := state.Jobs[9]
	if err != nil || result.Execution != Succeeded || !result.AuditCommitted || reassigned.ID != job ||
		reassigned.Status != sim.JobAssigned || reassigned.AssignedRobot != recipient || reassigned.CargoRobotID != "" ||
		state.Robots[6].JobID != "" || state.Robots[1].JobID != job {
		t.Fatalf("reassignment did not transfer the unstarted job: %+v %+v %v", result, reassigned, err)
	}
	automaticAuditStatus(t, pool, result.AuditID, "applied", "test-host")
}
