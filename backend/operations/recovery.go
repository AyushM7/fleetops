package operations

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"time"

	"github.com/AyushM7/fleetops/backend/fleet"
)

const (
	recoveryRequests      = 6
	recoveryOutput        = 6000
	recoveryRequestBytes  = 256 << 10
	recoveryResponseBytes = 1 << 20
)

type RecoveryInput struct {
	ID            string          `json:"id"`
	RuntimeID     string          `json:"runtimeId"`
	ObservationID string          `json:"observationId"`
	Snapshot      json.RawMessage `json:"snapshot"`
}

type recoveryWorker struct {
	node, entrypoint string
	send             func(context.Context, []byte) ([]byte, error)
}

type workerMessage struct {
	Request    json.RawMessage        `json:"request"`
	Completion *fleet.AgentCompletion `json:"completion"`
}

func (m *Manager) RecoveryEnabled() bool { return m.recovery != nil }

func (m *Manager) InvestigateRecovery(ctx context.Context, input RecoveryInput) (fleet.AgentCompletion, error) {
	if m.recovery == nil {
		return fleet.AgentCompletion{}, ErrBudget
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		return fleet.AgentCompletion{}, errors.New("operations worker capacity exhausted")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return m.recovery.investigate(ctx, input, m.ledger)
}

func (w *recoveryWorker) investigate(ctx context.Context, input RecoveryInput, ledger *ledger) (fleet.AgentCompletion, error) {
	failed := errors.New("recovery worker did not complete")
	cmd := exec.CommandContext(ctx, w.node, w.entrypoint)
	// No provider credential reaches the worker. Provider transport is mediated here.
	cmd.Env = []string{"FLEETOPS_OPERATIONS_PROTOCOL=v3"}
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fleet.AgentCompletion{}, failed
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fleet.AgentCompletion{}, failed
	}
	if cmd.Start() != nil {
		return fleet.AgentCompletion{}, failed
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	enc := json.NewEncoder(stdin)
	if len(input.Snapshot) > observationLimit || enc.Encode(input) != nil {
		return fleet.AgentCompletion{}, failed
	}
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 4096), recoveryRequestBytes+32768)
	var requests int
	var totals providerUsage
	seen := make(map[[sha256.Size]byte]bool, recoveryRequests)
	for scan.Scan() {
		var message workerMessage
		if decodeRecoveryJSON(scan.Text(), &message) != nil {
			return fleet.AgentCompletion{}, failed
		}
		if message.Completion != nil {
			if len(message.Request) != 0 || message.Completion.Requests != requests || (message.Completion.Status == "accepted" && !verifiedRecoveryUsage(*message.Completion, requests, totals)) {
				return fleet.AgentCompletion{}, failed
			}
			_ = stdin.Close()
			if scan.Scan() || scan.Err() != nil || cmd.Wait() != nil {
				return fleet.AgentCompletion{}, failed
			}
			return *message.Completion, nil
		}
		if requests >= recoveryRequests || !validRecoveryRequest(message.Request) {
			return fleet.AgentCompletion{}, failed
		}
		digest := sha256.Sum256(message.Request)
		if seen[digest] {
			logRecoveryFailure(input.ID, requests+1, recoveryProviderFailure{stage: "duplicate_request"})
			return fleet.AgentCompletion{}, failed
		}
		seen[digest] = true
		r, err := ledger.reserveRecovery(ctx, input.RuntimeID, input.ID, requests == 0)
		if err != nil {
			if errors.Is(err, errRecoveryReplay) {
				logRecoveryFailure(input.ID, requests+1, recoveryProviderFailure{stage: "duplicate_run"})
			}
			return fleet.AgentCompletion{}, err
		}
		requests++
		body, err := w.send(ctx, message.Request)
		if err != nil {
			detail := recoveryProviderFailure{stage: "transport"}
			_ = errors.As(err, &detail)
			logRecoveryFailure(input.ID, requests, detail)
			return fleet.AgentCompletion{}, failed
		}
		response, ok := readProviderUsage(body)
		accounting, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		if !ok {
			logRecoveryFailure(input.ID, requests, recoveryProviderFailure{stage: "response_usage"})
			if ledger.freeze(accounting, r) != nil {
				logRecoveryFailure(input.ID, requests, recoveryProviderFailure{stage: "ledger_freeze"})
			}
			stop()
			return fleet.AgentCompletion{}, ErrUsage
		}
		err = ledger.settleRecovery(accounting, r, response)
		stop()
		if err != nil {
			logRecoveryFailure(input.ID, requests, recoveryProviderFailure{stage: "ledger_settlement"})
			return fleet.AgentCompletion{}, err
		}
		if response.status != "completed" {
			logRecoveryFailure(input.ID, requests, recoveryProviderFailure{stage: "response_incomplete"})
			return fleet.AgentCompletion{}, failed
		}
		totals.Input += response.Input
		totals.Output += response.Output
		if totals.Input > 32000 || totals.Output > 12000 || totals.Input+totals.Output > 44000 {
			return fleet.AgentCompletion{}, ErrUsage
		}
		if enc.Encode(json.RawMessage(body)) != nil {
			return fleet.AgentCompletion{}, failed
		}
	}
	return fleet.AgentCompletion{}, failed
}

func verifiedRecoveryUsage(completion fleet.AgentCompletion, requests int, totals providerUsage) bool {
	reported := completion.Usage
	return requests > 0 && completion.Tools > 0 && completion.ActualModel == "openai/"+modelID &&
		reported.Status == "reported" && reported.InputTokens != nil && int64(*reported.InputTokens) == totals.Input &&
		reported.OutputTokens != nil && int64(*reported.OutputTokens) == totals.Output
}

func decodeRecoveryJSON(raw string, dst any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(dst) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid recovery protocol frame")
	}
	return nil
}

func validRecoveryRequest(raw json.RawMessage) bool {
	if len(raw) == 0 || len(raw) > recoveryRequestBytes {
		return false
	}
	var request struct {
		Model     string          `json:"model"`
		Input     json.RawMessage `json:"input"`
		Store     *bool           `json:"store"`
		Tier      string          `json:"service_tier"`
		Output    int             `json:"max_output_tokens"`
		Stream    bool            `json:"stream"`
		Text      json.RawMessage `json:"text"`
		Reasoning json.RawMessage `json:"reasoning"`
		Choice    json.RawMessage `json:"tool_choice"`
		Include   json.RawMessage `json:"include"`
		Tools     []struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Strict      *bool           `json:"strict"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"tools"`
	}
	if decodeRecoveryJSON(string(raw), &request) != nil || request.Model != modelID || request.Store == nil || *request.Store ||
		request.Tier != "default" || request.Output < 16 || request.Output > recoveryOutput || request.Stream || len(request.Input) == 0 || len(request.Tools) > 4 {
		return false
	}
	for _, tool := range request.Tools {
		if tool.Type != "function" {
			return false
		}
		switch tool.Name {
		case "readIncident", "listRecoveryCandidates", "readRobots", "readJobs":
		default:
			return false
		}
	}
	return true
}

type providerUsage struct {
	ID     string
	status string
	usage
}

func readProviderUsage(body []byte) (providerUsage, bool) {
	var response struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Status string `json:"status"`
		Usage  *struct {
			Input        int64 `json:"input_tokens"`
			Output       int64 `json:"output_tokens"`
			Total        int64 `json:"total_tokens"`
			InputDetails struct {
				Cached     *int64 `json:"cached_tokens"`
				CacheWrite *int64 `json:"cache_creation_input_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil || response.Usage == nil || response.Model != modelID || !providerResponseID.MatchString(response.ID) {
		return providerUsage{}, false
	}
	u := response.Usage
	if u.Input < 1 || u.Input > maxInputTokens || u.Output < 0 || u.Output > recoveryOutput || u.Total != u.Input+u.Output {
		return providerUsage{}, false
	}
	return providerUsage{ID: response.ID, status: response.Status, usage: usage{Model: response.Model, Input: u.Input, Output: u.Output, Cached: u.InputDetails.Cached, CacheWrite: u.InputDetails.CacheWrite}}, true
}

type recoveryProviderFailure struct {
	stage, reason, requestID string
	status                   int
}

func (f recoveryProviderFailure) Error() string { return "recovery provider failure: " + f.stage }

func logRecoveryFailure(run string, request int, f recoveryProviderFailure) {
	slog.Warn("operations_v3_provider_failure", "run_id", run, "request_ordinal", request,
		"stage", f.stage, "reason", f.reason, "http_status", f.status, "provider_request_id", f.requestID)
}

func recoveryTransportReason(err error) string {
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &network) && network.Timeout():
		return "timeout"
	default:
		return "io"
	}
}

var recoveryRequestID = regexp.MustCompile(`^req_[A-Za-z0-9_-]{1,120}$`)

func recoveryProvider(key string) func(context.Context, []byte) ([]byte, error) {
	// A non-rewindable body also prevents transparent retries after a broken connection.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, request []byte) ([]byte, error) {
		return sendRecoveryRequest(ctx, client, key, request)
	}
}

func sendRecoveryRequest(ctx context.Context, client *http.Client, key string, request []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/responses", io.NopCloser(bytes.NewReader(request)))
	if err != nil {
		return nil, recoveryProviderFailure{stage: "request_setup"}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, recoveryProviderFailure{stage: "before_headers", reason: recoveryTransportReason(err)}
	}
	defer res.Body.Close()
	failure := recoveryProviderFailure{status: res.StatusCode}
	if id := res.Header.Get("X-Request-Id"); recoveryRequestID.MatchString(id) {
		failure.requestID = id
	}
	if res.StatusCode != http.StatusOK {
		failure.stage = "http_status"
		return nil, failure
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, recoveryResponseBytes+1))
	if err != nil {
		failure.stage, failure.reason = "response_body", recoveryTransportReason(err)
		return nil, failure
	}
	if len(body) > recoveryResponseBytes {
		failure.stage, failure.reason = "response_body", "size_limit"
		return nil, failure
	}
	return body, nil
}
