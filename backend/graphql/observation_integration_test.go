package graphql

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIntegrationHTTPObservationBinding(t *testing.T) {
	for _, mode := range []string{"current", "missing", "null", "empty", "fabricated", "foreign runtime", "read-submit window", "operator compatibility", "viewer with current identity"} {
		t.Run(mode, func(t *testing.T) {
			api := integrationAPI(t)
			state := api.request(t, "proposer", `{fleetState{observationId tick}}`, nil)["fleetState"].(map[string]any)
			identity := state["observationId"].(string)
			if len(identity) != 64 {
				t.Fatal("production read did not expose an opaque identity")
			}
			input := map[string]any{"robotId": "R1", "stationId": "S1", "expiresAt": time.Unix(0, api.now.Load()).Add(time.Minute).UTC().Format(time.RFC3339Nano), "expectedObservationId": identity}
			role := "proposer"
			switch mode {
			case "missing":
				delete(input, "expectedObservationId")
			case "null":
				input["expectedObservationId"] = nil
			case "empty":
				input["expectedObservationId"] = ""
			case "fabricated":
				input["expectedObservationId"] = strings.Repeat("0", 64)
			case "foreign runtime":
				other := integrationAPI(t)
				if !reflect.DeepEqual(api.service.Snapshot(), other.service.Snapshot()) {
					t.Fatal("foreign runtime did not have identical telemetry")
				}
				foreign := other.request(t, "proposer", `{fleetState{observationId}}`, nil)["fleetState"].(map[string]any)
				input["expectedObservationId"] = foreign["observationId"]
			case "read-submit window":
				// The final client observation is fixed; Go changes state before the mutation reaches its lock.
				if err := api.service.Tick(); err != nil {
					t.Fatal(err)
				}
			case "operator compatibility":
				role = "operator"
				delete(input, "expectedObservationId")
			case "viewer with current identity":
				role = "viewer"
			}
			before := api.service.Snapshot()
			row := api.request(t, role, `mutation($input:SetChargingInput!){submitProposal(input:$input)`+outcome+`}`, map[string]any{"input": input})["submitProposal"].(map[string]any)
			if !reflect.DeepEqual(before, api.service.Snapshot()) || row["executionAttempted"] != false || row["auditCommitted"] != true {
				t.Fatal("submission mutated state or omitted audit", row)
			}
			if mode == "current" || mode == "operator compatibility" {
				if row["error"] != nil || row["state"] != "pending" {
					t.Fatal("current or compatible operator submission failed", row)
				}
			} else {
				denied(t, row)
				if mode != "viewer with current identity" && row["error"] != "observation is missing or stale; request a new analysis" {
					t.Fatal("identity failure was not reported distinctly", row)
				}
			}
			var audits int
			if err := api.pool.QueryRow(context.Background(), `SELECT count(*) FROM fleet_audit WHERE proposal_id=$1 AND attempt='create'`, row["proposalId"]).Scan(&audits); err != nil || audits != 1 {
				t.Fatal("production mutation not audited exactly once", audits, err)
			}
		})
	}
}

func TestIntegrationHTTPReassignmentCannotOmitObservation(t *testing.T) {
	api := integrationAPI(t)
	input := map[string]any{"jobId": "missing", "fromRobotId": "R1", "toRobotId": "R2", "expiresAt": time.Unix(0, api.now.Load()).Add(time.Minute).UTC().Format(time.RFC3339Nano)}
	query := `mutation($input:ReassignUnstartedJobInput!){submitReassignment(input:$input)` + outcome + `}`
	missing := api.request(t, "proposer", query, map[string]any{"input": input})["submitReassignment"].(map[string]any)
	denied(t, missing)
	if missing["error"] != "observation is missing or stale; request a new analysis" {
		t.Fatal("reassignment bypassed observation binding", missing)
	}
	state := api.request(t, "proposer", `{fleetState{observationId}}`, nil)["fleetState"].(map[string]any)
	input["expectedObservationId"] = state["observationId"]
	invalid := api.request(t, "proposer", query, map[string]any{"input": input})["submitReassignment"].(map[string]any)
	denied(t, invalid)
	if invalid["error"] != "request denied" {
		t.Fatal("matching identity did not reach independent candidate preflight", invalid)
	}
}
