package fleet

import (
	"errors"
	"strings"
	"testing"
)

func TestAgentFactsPhysicalIncidentsRequireV3(t *testing.T) {
	for _, kind := range []string{"motor_failure", "sensor_failure", "critical_battery"} {
		view := StateView{ActiveIncidents: []IncidentView{{RobotID: "M1", Type: kind, Severity: "high"}}}
		if _, err := agentFacts(view, "recovery-decision-v2"); !errors.Is(err, ErrAgentRecord) {
			t.Fatalf("V2 accepted %s: %v", kind, err)
		}
		facts, err := agentFacts(view, "recovery-decision-v3")
		if err != nil || len(facts.Incidents) != 1 || facts.Incidents[0].Type != kind || facts.Blocked || len(facts.Candidates) != 0 {
			t.Fatalf("V3 did not preserve %s as non-traffic evidence: %+v, %v", kind, facts, err)
		}
		if !verifyAgentDecision(&AgentDecision{Kind: "abstain", Reason: "no_supported_incident"}, facts, "compare_tradeoffs") {
			t.Fatalf("V3 could not record an abstention for %s", kind)
		}
	}
}

func TestAgentProvenanceStrictRedactionAndBounds(t *testing.T) {
	for _, raw := range []string{
		`{"version":"agent-run-v1","apiKey":"sk-private"}`,
		`{"prompt":"bearer private"}`, `{"toolOutputs":["private"]}`, `{"proposalId":"someone-else"}`,
		`{"status":"failed","failure":"raw secret"}`, strings.Repeat("x", 32769), `{} {}`,
	} {
		var completion AgentFinish
		if DecodeAgentJSON(raw, &completion) == nil {
			t.Fatal("unsafe input accepted", raw[:min(40, len(raw))])
		}
	}
	base := AgentMetadata{"agent-run-v1", "recovery-decision-v2", "compare_tradeoffs", "live-model", "openai/example"}
	if !base.valid() {
		t.Fatal("valid metadata denied")
	}
	v3 := base
	v3.Contract = "recovery-decision-v3"
	if !v3.valid() {
		t.Fatal("versioned recovery metadata denied")
	}
	for _, model := range []string{"sk-private", "openai/sk-private", "openai/bearer-secret", "openai/token", "openai/" + strings.Repeat("x", 129)} {
		m := base
		m.RequestedModel = model
		if m.valid() {
			t.Fatal("sensitive model identity accepted")
		}
	}
	for _, field := range []string{"version", "contract", "objective", "source"} {
		m := base
		switch field {
		case "version":
			m.Version = "unknown"
		case "contract":
			m.Contract = "unknown"
		case "objective":
			m.Objective = "maximize_profit"
		case "source":
			m.Source = "verified-openai"
		}
		if m.valid() {
			t.Fatal("unsupported metadata", field)
		}
	}
}
