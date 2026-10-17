package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/incident"
	"github.com/AyushM7/fleetops/backend/safety"
	"github.com/AyushM7/fleetops/backend/sim"
)

type incidentReadFixture struct {
	*readOnlyFixture
	view      fleet.StateView
	err       error
	reads     atomic.Int64
	mutations atomic.Int64
}

func (f *incidentReadFixture) ReadState(context.Context) (fleet.StateView, error) {
	f.reads.Add(1)
	return f.view, f.err
}
func (f *incidentReadFixture) Submit(context.Context, safety.SetChargingRequest, time.Time) (fleet.Result, error) {
	f.mutations.Add(1)
	return fleet.Result{}, errors.New("unexpected fixture mutation")
}

func incidentHTTPFixture(t *testing.T) *incidentReadFixture {
	t.Helper()
	state := sim.State{Tick: 123, Robots: []sim.RobotState{
		{ID: "R1", Battery: 14, JobID: "J1", Status: sim.RobotMoving},
		{ID: "R2", Battery: 100, Status: sim.RobotOffline},
		{ID: "R3", Battery: 100, Blocked: true, WaitTicks: 40},
		{ID: "R4", Battery: 100, Blocked: true, WaitTicks: 41},
		{ID: "R5", Battery: 100, Status: sim.RobotMoving, JobID: "J5"},
		{ID: "R6", Battery: 100, WaitTicks: 100},
		{ID: "R7", Battery: 14},
		{ID: "R8", Battery: 15, JobID: "J8"},
		{ID: "R9", Battery: 29, JobID: "J9"},
		{ID: "R10", Battery: 30, JobID: "J10"},
	}}
	conditions, _, err := incident.Reconcile(nil, state, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := &incidentReadFixture{readOnlyFixture: &readOnlyFixture{}, view: fleet.StateView{Tick: state.Tick, Robots: []fleet.RobotView{}, Stations: []fleet.StationView{}, ActiveIncidents: []fleet.IncidentView{}}}
	byID := map[string]fleet.RobotView{}
	for _, r := range state.Robots {
		v := fleet.RobotView{ID: r.ID, X: r.X, Y: r.Y, Battery: r.Battery, Status: string(r.Status), JobID: r.JobID, WaitTicks: r.WaitTicks, Blocked: r.Blocked}
		f.view.Robots = append(f.view.Robots, v)
		byID[r.ID] = v
	}
	for _, c := range conditions {
		f.view.ActiveIncidents = append(f.view.ActiveIncidents, fleet.IncidentView{RobotID: c.RobotID, Type: string(c.Type), Severity: string(c.Severity), Telemetry: byID[c.RobotID]})
	}
	return f
}

const incidentReadQuery = `query { fleetState { tick robots { id battery jobId status blocked waitTicks } activeIncidents { robotId type severity telemetry { id battery jobId status blocked waitTicks } } } }`

func TestIncidentGraphQLAuthenticationAndReadOnlyData(t *testing.T) {
	a, tokens := authFixture(t)
	f := incidentHTTPFixture(t)
	handler := NewHandler(f, a)
	for _, role := range []string{"viewer", "proposer", "operator", "invalid", "missing"} {
		t.Run(role, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"query": incidentReadQuery})
			req := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			token := tokens[role]
			if role == "invalid" {
				token = strings.Repeat("x", 64)
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if role == "invalid" || role == "missing" {
				if response.Code != http.StatusUnauthorized {
					t.Fatal(response.Code)
				}
				return
			}
			var got struct {
				Data struct {
					FleetState struct {
						Tick            int
						ActiveIncidents []struct {
							RobotID, Type, Severity string
							Telemetry               struct {
								ID                 string
								Battery, WaitTicks int
								JobID, Status      string
								Blocked            bool
							}
						}
					}
				}
				Errors []any
			}
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || len(got.Errors) != 0 || got.Data.FleetState.Tick != 123 || len(got.Data.FleetState.ActiveIncidents) != 6 {
				t.Fatalf("unexpected response: %s", response.Body.String())
			}
			for i, want := range f.view.ActiveIncidents {
				v := got.Data.FleetState.ActiveIncidents[i]
				if v.RobotID != want.RobotID || v.Type != want.Type || v.Severity != want.Severity || v.Telemetry.ID != want.Telemetry.ID || v.Telemetry.Battery != want.Telemetry.Battery || v.Telemetry.WaitTicks != want.Telemetry.WaitTicks || v.Telemetry.Blocked != want.Telemetry.Blocked {
					t.Fatal("incident telemetry changed in transport")
				}
			}
		})
	}
	if f.reads.Load() != 3 || f.mutations.Load() != 0 {
		t.Fatal("authentication bypass or read caused a mutation")
	}
}

func TestIncidentGraphQLSanitizesDetectorFailure(t *testing.T) {
	a, tokens := authFixture(t)
	f := incidentHTTPFixture(t)
	f.err = errors.New("private detector failure")
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(`{"query":"{fleetState{activeIncidents{type}}}"}`))
	req.Header.Set("Authorization", "Bearer "+tokens["proposer"])
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	NewHandler(f, a).ServeHTTP(response, req)
	if !strings.Contains(response.Body.String(), "workflow unavailable") || strings.Contains(response.Body.String(), "private") {
		t.Fatal(response.Body.String())
	}
}

// Opt-in cross-language check: the trusted fixture never becomes a production endpoint.
func TestAgentIncidentHTTPClient(t *testing.T) {
	if os.Getenv("FLEETOPS_AGENT_CONTRACT_CHECK") != "1" {
		t.Skip("run agent/scripts/verify-integration.ps1 for the Node contract check")
	}
	a, tokens := authFixture(t)
	f := incidentHTTPFixture(t)
	server := httptest.NewServer(NewHandler(f, a))
	defer server.Close()
	script, err := filepath.Abs("../../agent/scripts/check-incident-fixture.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", script)
	cmd.Env = append(os.Environ(), "FLEETOPS_INCIDENT_FIXTURE_URL="+server.URL, "FLEETOPS_INCIDENT_FIXTURE_TOKEN="+tokens["proposer"])
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("agent contract check failed: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	if f.mutations.Load() != 0 {
		t.Fatal("read-only incident analysis submitted a mutation")
	}
}
