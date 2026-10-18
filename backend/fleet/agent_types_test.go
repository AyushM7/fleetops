package fleet

import (
	"strings"
	"testing"
)

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
