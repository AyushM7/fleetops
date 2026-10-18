//go:build fleetops_test

package fleet_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/AyushM7/fleetops/backend/auth"
	"github.com/AyushM7/fleetops/backend/fleet"
	"github.com/AyushM7/fleetops/backend/graphql"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIntegrationAgentRunMastraHTTP(t *testing.T) {
	if os.Getenv("FLEETOPS_AGENT_CONTRACT_CHECK") != "1" {
		t.Skip("opt in to the built-agent HTTP contract check")
	}
	now := time.Now().UTC()
	service := fleet.NewReassignmentTestService(t, func() time.Time { return now })
	before := service.Snapshot()
	token := strings.Repeat("local-agent05-fixture-", 3)
	hash := sha256.Sum256([]byte(token))
	raw, _ := json.Marshal([]auth.Credential{{Subject: "agent05", Role: auth.Proposer, SHA256: hex.EncodeToString(hash[:])}})
	credentials, err := auth.New(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(graphql.NewHandler(service, credentials))
	defer server.Close()
	script, err := filepath.Abs("../../agent/scripts/check-agent-run-fixture.mjs")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("node", script)
	command.Env = append(os.Environ(), "FLEETOPS_AGENT_FIXTURE_URL="+server.URL, "FLEETOPS_AGENT_FIXTURE_TOKEN="+token)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("agent provenance contract failed: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	if !reflect.DeepEqual(before, service.Snapshot()) {
		t.Fatal("agent mutated simulator before human approval")
	}
	ctx, err := credentials.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := service.ReadAgentRuns(ctx, "", 20)
	if err != nil || len(runs) != 1 || runs[0].ProposalID == nil {
		t.Fatal(runs, err)
	}
}
