package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
)

var ErrAgentRecord = errors.New("invalid agent provenance")
var agentID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var modelID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}/[A-Za-z0-9._:-]{1,96}$`)

type AgentMetadata struct {
	Version        string `json:"version"`
	Contract       string `json:"contract"`
	Objective      string `json:"objective"`
	Source         string `json:"reportedSource"`
	RequestedModel string `json:"requestedModel"`
}
type AgentKey struct {
	JobID       string `json:"jobId"`
	FromRobotID string `json:"fromRobotId"`
	ToRobotID   string `json:"toRobotId"`
}
type AgentCandidate struct {
	Key     AgentKey       `json:"key"`
	Metrics map[string]int `json:"metrics"`
}
type AgentFacts struct {
	Incidents  []AgentIncident  `json:"incidents"`
	Tick       int              `json:"tick"`
	Poisoned   bool             `json:"poisoned"`
	Blocked    bool             `json:"blocked"`
	Candidates []AgentCandidate `json:"candidates"`
}
type AgentIncident struct {
	RobotID  string `json:"robotId"`
	Type     string `json:"type"`
	Severity string `json:"severity"`
}
type AgentComparison struct {
	Left       AgentKey `json:"left"`
	Right      AgentKey `json:"right"`
	Metric     string   `json:"metric"`
	LeftValue  int      `json:"leftValue"`
	RightValue int      `json:"rightValue"`
	Relation   string   `json:"relation"`
}
type AgentDecision struct {
	Kind        string            `json:"kind"`
	Selected    *AgentKey         `json:"selected"`
	Basis       string            `json:"basis"`
	Reason      string            `json:"reason"`
	Comparisons []AgentComparison `json:"comparisons"`
}
type AgentUsage struct {
	Status       string `json:"status"`
	InputTokens  *int   `json:"inputTokens"`
	OutputTokens *int   `json:"outputTokens"`
	TotalTokens  *int   `json:"totalTokens"`
}
type AgentCompletion struct {
	Steps           []AgentStep    `json:"steps"`
	SubmissionCheck string         `json:"submissionCheck"`
	Status          string         `json:"status"`
	Failure         string         `json:"failure"`
	LatencyMS       int            `json:"latencyMs"`
	Requests        int            `json:"modelRequests"`
	Tools           int            `json:"toolExecutions"`
	Usage           AgentUsage     `json:"reportedUsage"`
	ActualModel     string         `json:"reportedActualModel"`
	Decision        *AgentDecision `json:"decision"`
}
type AgentStep struct {
	Kind string `json:"kind"`
	Tool string `json:"tool"`
}
type AgentSubmission struct {
	ExpiresAt time.Time `json:"expiresAt"`
}
type AgentFinish struct {
	RuntimeID     string           `json:"runtimeId"`
	ObservationID string           `json:"observationId"`
	Completion    AgentCompletion  `json:"completion"`
	Submit        *AgentSubmission `json:"submit"`
}
type AgentReceipt struct {
	RunID    string `json:"runId"`
	Recorded bool   `json:"recorded"`
	Result   Result `json:"result"`
	Denial   string `json:"denial"`
}
type AgentRun struct {
	SubmissionOutcome string           `json:"serverSubmissionOutcome"`
	ServerElapsedMS   *int64           `json:"serverElapsedMs"`
	ID                string           `json:"id"`
	RuntimeID         string           `json:"runtimeId"`
	ObservationID     string           `json:"observationId"`
	StartedAt         time.Time        `json:"startedAt"`
	CompletedAt       *time.Time       `json:"completedAt"`
	Metadata          AgentMetadata    `json:"agentReportedMetadata"`
	Facts             AgentFacts       `json:"serverVerifiedObservation"`
	Completion        *AgentCompletion `json:"agentReportedCompletion"`
	DecisionVerified  bool             `json:"serverVerifiedDecision"`
	CostStatus        string           `json:"estimatedCostStatus"`
	ProposalID        *string          `json:"proposalId"`
	Proposal          *ProposalView    `json:"-"`
}

func DecodeAgentJSON(text string, target any) error {
	if len(text) > 32768 {
		return ErrAgentRecord
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrAgentRecord
	}
	return nil
}
func allowed(value string, values ...string) bool {
	for _, item := range values {
		if value == item {
			return true
		}
	}
	return false
}
func (m AgentMetadata) valid() bool {
	lower := strings.ToLower(m.RequestedModel)
	return m.Version == "agent-run-v1" && m.Contract == "recovery-decision-v2" &&
		allowed(m.Objective, "compare_tradeoffs", "minimize_pickup_steps", "preserve_battery_headroom") &&
		allowed(m.Source, "deterministic", "mocked", "recorded", "live-model") && (m.RequestedModel == "" || modelID.MatchString(m.RequestedModel)) &&
		!strings.Contains(lower, "sk-") && !strings.Contains(lower, "secret") && !strings.Contains(lower, "bearer") && !strings.Contains(lower, "token")
}
func (c AgentCompletion) valid() bool {
	if len(c.Steps) > 14 {
		return false
	}
	requests, tools := 0, 0
	for _, step := range c.Steps {
		if step.Kind == "request" && step.Tool == "" {
			requests++
		} else if step.Kind == "tool" && allowed(step.Tool, "readIncident", "readRobots", "readJobs", "listRecoveryCandidates") {
			tools++
		} else {
			return false
		}
	}
	if requests > c.Requests || tools > c.Tools {
		return false
	}
	if !allowed(c.SubmissionCheck, "disabled", "not_requested", "current", "snapshot_changed", "unavailable") {
		return false
	}
	if !allowed(c.Status, "accepted", "rejected", "failed") || !allowed(c.Failure, "none", "grounding_rejected", "timeout", "cancelled", "budget_exhausted", "invalid_tool", "invalid_usage", "investigation_failed", "snapshot_changed", "snapshot_unavailable") ||
		c.LatencyMS < 0 || c.LatencyMS > 3600000 || c.Requests < 0 || c.Requests > 6 || c.Tools < 0 || c.Tools > 8 {
		return false
	}
	if c.Status == "accepted" {
		if c.Decision == nil || c.Failure != "none" {
			return false
		}
	} else if c.Decision != nil || c.Failure == "none" {
		return false
	}
	if c.ActualModel != "" && (!modelID.MatchString(c.ActualModel) || strings.Contains(strings.ToLower(c.ActualModel), "sk-") || strings.Contains(strings.ToLower(c.ActualModel), "secret") || strings.Contains(strings.ToLower(c.ActualModel), "bearer") || strings.Contains(strings.ToLower(c.ActualModel), "token")) {
		return false
	}
	u := c.Usage
	if u.Status == "unavailable" {
		return u.InputTokens == nil && u.OutputTokens == nil && u.TotalTokens == nil
	}
	return allowed(u.Status, "reported", "partial") && u.InputTokens != nil && u.OutputTokens != nil && u.TotalTokens != nil &&
		*u.InputTokens >= 0 && *u.OutputTokens >= 0 && *u.TotalTokens >= *u.InputTokens+*u.OutputTokens && *u.TotalTokens <= 10000000
}
func canonicalAgentJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return bytes.TrimSpace(data)
}
