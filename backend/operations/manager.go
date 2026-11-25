package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/fleet"
)

const (
	observationLimit    = 48 * 1024
	providerKeyVariable = "OPENAI_API_KEY"
)

type Manager struct {
	ledger   *ledger
	recovery *recoveryWorker
	slots    chan struct{}
	generate func(context.Context, []byte, int64) (modelResult, error)
}

type modelResult struct {
	Decision *fleet.OperationsDecision `json:"decision,omitempty"`
	Failure  string                    `json:"failure,omitempty"`
	Usage    *usage                    `json:"usage,omitempty"`
}

type Outcome struct {
	Decision          *fleet.OperationsDecision `json:"decision,omitempty"`
	Status            string                    `json:"status"`
	Model             string                    `json:"model,omitempty"`
	RequestID         int64                     `json:"requestId,omitempty"`
	ReservedMicroUSD  int64                     `json:"reservedMicroUsd,omitempty"`
	AccountedMicroUSD *int64                    `json:"accountedMicroUsd,omitempty"`
	Usage             *usage                    `json:"usage,omitempty"`
}

func Open(ctx context.Context, pool *db.Pool) (*Manager, error) {
	unavailable := errors.New("operations model or price configuration unavailable")
	entrypoint := os.Getenv("FLEETOPS_OPERATIONS_AGENT")
	apiKey := strings.TrimSpace(os.Getenv(providerKeyVariable))
	inputRate, inputErr := strconv.ParseInt(os.Getenv("FLEETOPS_OPERATIONS_INPUT_MICRO_USD_PER_MILLION"), 10, 64)
	outputRate, outputErr := strconv.ParseInt(os.Getenv("FLEETOPS_OPERATIONS_OUTPUT_MICRO_USD_PER_MILLION"), 10, 64)
	if !filepath.IsAbs(entrypoint) || apiKey == "" || inputErr != nil || outputErr != nil {
		return nil, unavailable
	}
	info, err := os.Stat(entrypoint)
	if err != nil || !info.Mode().IsRegular() {
		return nil, unavailable
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, unavailable
	}
	ledger, err := openLedger(ctx, pool, pricing{inputRate, outputRate})
	if err != nil {
		return nil, err
	}
	if os.Getenv("FLEETOPS_OPERATIONS_PROTOCOL") == "v3" {
		if !ledger.pricing.validFor(recoveryOutput) {
			return nil, unavailable
		}
		return &Manager{ledger: ledger, slots: make(chan struct{}, 2), recovery: &recoveryWorker{node: node, entrypoint: entrypoint, send: recoveryProvider(apiKey)}}, nil
	}
	return &Manager{ledger: ledger, slots: make(chan struct{}, 2),
		generate: func(ctx context.Context, observation []byte, reservationID int64) (modelResult, error) {
			cmd := exec.CommandContext(ctx, node, entrypoint)
			credential := providerKeyVariable + "=" + apiKey
			permit := "FLEETOPS_OPERATIONS_RESERVATION=" + strconv.FormatInt(reservationID, 10)
			cmd.Env = []string{credential, permit}
			cmd.Stdin = bytes.NewReader(observation)
			cmd.WaitDelay = time.Second
			var stdout limitedOutput
			cmd.Stdout = &stdout
			var result modelResult
			if err := cmd.Run(); err != nil || fleet.DecodeAgentJSON(stdout.String(), &result) != nil {
				return modelResult{}, errors.New("operations model worker did not return a verified response")
			}
			return result, nil
		},
	}, nil
}

type limitedOutput struct{ bytes.Buffer }

func (out *limitedOutput) Write(p []byte) (int, error) {
	if out.Len()+len(p) > 8*1024 {
		return 0, io.ErrShortWrite
	}
	return out.Buffer.Write(p)
}

func (m *Manager) Decide(ctx context.Context, runtimeID string, observation fleet.OperationsObservation) Outcome {
	if m.generate == nil {
		return Outcome{Status: "model_unavailable"}
	}
	raw, err := json.Marshal(observation)
	if err != nil || len(raw) > observationLimit {
		return Outcome{Status: "observation_unavailable"}
	}
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	default:
		return Outcome{Status: "model_busy"}
	}
	r, err := m.ledger.reserve(ctx, runtimeID)
	if err != nil {
		if errors.Is(err, ErrBudget) {
			return Outcome{Status: "budget_denied"}
		}
		return Outcome{Status: "ledger_unavailable"}
	}
	out := Outcome{Status: "model_unavailable", Model: modelID, RequestID: r.id, ReservedMicroUSD: r.maximum}
	call, cancel := context.WithTimeout(ctx, requestTimeout)
	result, err := m.generate(call, raw, r.id)
	cancel()
	if err != nil {
		return out
	}
	// A pause/stop can cancel execution but never credits an unpaid estimate.
	settlement, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer stop()
	if result.Failure == "billing_anomaly" {
		out.Status = "usage_unconfirmed"
		if err := m.ledger.freeze(settlement, r); err != nil {
			out.Status = "ledger_unavailable"
		}
		return out
	}
	if result.Usage == nil && (result.Failure == "model_unavailable" || result.Failure == "invalid_output") {
		out.Status = result.Failure
		return out
	}
	cost, err := m.ledger.settle(settlement, r, result.Usage)
	if err != nil {
		out.Status = "usage_unconfirmed"
		return out
	}
	out.AccountedMicroUSD, out.Usage = &cost, result.Usage
	switch {
	case result.Failure == "invalid_output":
		out.Status = "invalid_output"
	case result.Decision != nil && result.Failure == "":
		out.Status, out.Decision = "decided", result.Decision
	}
	return out
}
