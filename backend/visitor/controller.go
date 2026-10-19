package visitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AyushM7/fleetops/backend/graphql/model"
)

type demoRun struct {
	scenarioID string
	generation string
	handler    http.Handler
	capture    func(context.Context) error
	advance    func(context.Context) error
	inspect    func(context.Context) (*model.FleetState, bool, error)
	step       func(context.Context) error
	evidence   *demoExplanation
}

// Read-only local-demo provenance. This never authorizes a command.
type demoExplanation struct {
	Generation     string             `json:"generation"`
	RuntimeID      string             `json:"runtimeId"`
	ScenarioID     string             `json:"scenarioId"`
	ProposalID     string             `json:"proposalId"`
	ProposalSource demoProposalSource `json:"proposalSource"`
	Observation    *model.FleetState  `json:"observation"`
}

type demoProposalSource string

const (
	demoSourceGoScripted demoProposalSource = "go-scripted"
	demoSourceMastra     demoProposalSource = "mastra-deterministic"
	demoSourceRecorded   demoProposalSource = "recorded-model"
	demoSourceLive       demoProposalSource = "live-model"
	demoSourceNone       demoProposalSource = "none"
	demoSourceUnknown    demoProposalSource = "unknown"
)

type demoStatus struct {
	ScenarioID string            `json:"scenarioId,omitempty"`
	Generation string            `json:"generation"`
	Ready      bool              `json:"ready"`
	State      string            `json:"state,omitempty"`
	Speed      string            `json:"speed,omitempty"`
	Snapshot   *model.FleetState `json:"snapshot,omitempty"`
	Evidence   *demoExplanation  `json:"evidence,omitempty"`
}

// All browser forwarding, progress ticks, capture and replacement share this
// gate. No caller retains a handler/service reference outside the gate.
type demoController struct {
	mu            sync.Mutex
	busy          atomic.Bool
	stepping      atomic.Bool
	run           *demoRun
	ready         bool
	closed        bool
	factory       func(context.Context) (*demoRun, error)
	selectFactory func(context.Context, string) (*demoRun, error)
	record        func(string, string) error
	interactive   bool
	running       bool
	speed         string
	nextTick      time.Time
	lastStep      time.Time
	ticks         int
}

func (c *demoController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isTimeControl(r.URL.Path) {
		c.control(w, r)
		return
	}
	if r.URL.Path == "/demo/restart" || strings.HasPrefix(r.URL.Path, "/demo/scenario/") {
		c.restart(w, r)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.URL.Path == "/demo/status" {
		w.Header().Set("Content-Type", "application/json")
		c.writeStatus(w, r.Context())
		return
	}
	if r.Header.Get("X-FleetOps-Generation") != c.run.generation {
		http.Error(w, "scenario changed; refresh the demo session", http.StatusConflict)
		return
	}
	if !c.ready || c.closed {
		http.Error(w, "scenario unavailable; evidence capture or restart failed", http.StatusServiceUnavailable)
		return
	}
	c.run.handler.ServeHTTP(w, r)
}

func (c *demoController) restart(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/demo/scenario/")
	selecting := r.URL.Path != "/demo/restart"
	if selecting && (!allowedScenario(id) || c.selectFactory == nil) {
		http.Error(w, "unsupported scenario", http.StatusBadRequest)
		return
	}
	if !c.busy.CompareAndSwap(false, true) {
		http.Error(w, "restart already in progress; refresh status", http.StatusConflict)
		return
	}
	defer c.busy.Store(false)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		http.Error(w, "demo closed", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("X-FleetOps-Generation") != c.run.generation {
		if err := c.record(c.run.generation, "restart denied: stale generation"); err != nil {
			c.ready = false
			http.Error(w, "lifecycle evidence could not be saved", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "scenario changed; refresh the demo session", http.StatusConflict)
		return
	}
	// HTTP disconnects cannot cancel capture after ownership has been revoked.
	// A failed replacement leaves the old runtime inaccessible, including poison.
	c.ready = false
	c.running = false
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !selecting {
		id = c.run.scenarioID
	}
	if err := c.replaceScenario(ctx, id); err != nil {
		_ = c.record(c.run.generation, "restart failed; old runtime stopped; reconciliation may be required")
		http.Error(w, "restart failed; old runtime stopped; historical outcome is not reconciled; refresh status before retrying", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(demoStatus{Generation: c.run.generation, Ready: true, ScenarioID: c.run.scenarioID})
}

func allowedScenario(id string) bool {
	switch id {
	case "blocked-reassignment", "routine-charging", "healthy-operations", "blocked-no-candidate":
		return true
	}
	return false
}

func (c *demoController) replace(ctx context.Context) error {
	return c.replaceScenario(ctx, c.run.scenarioID)
}

func (c *demoController) replaceScenario(ctx context.Context, id string) error {
	if err := c.record(c.run.generation, "restart requested; target scenario "+id+"; draining complete"); err != nil {
		return err
	}
	if err := c.run.capture(ctx); err != nil {
		return err
	}
	var next *demoRun
	var err error
	if c.selectFactory != nil {
		next, err = c.selectFactory(ctx, id)
	} else {
		next, err = c.factory(ctx)
	}
	if err != nil {
		return err
	}
	if next.generation == "" || next.generation == c.run.generation {
		return errors.New("replacement identity is not fresh")
	}
	if c.selectFactory != nil && next.scenarioID != id {
		return errors.New("replacement scenario does not match selection")
	}
	if err := c.record(next.generation, "replacement ready; previous evidence captured; no automatic approval"); err != nil {
		return err
	}
	c.run, c.ready = next, true
	c.ticks = 0
	c.running, c.speed, c.nextTick, c.lastStep = false, "1", time.Time{}, time.Time{}
	return nil
}

func (c *demoController) advance(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready || c.closed {
		return nil
	}
	if c.interactive {
		return c.advanceInteractive(ctx, time.Now())
	}
	if err := c.run.advance(ctx); err != nil {
		c.ready = false
		return err
	}
	return nil
}

func (c *demoController) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed, c.ready, c.running = true, false, false
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.run.capture(ctx)
}

// Exclusive files plus Sync prevent replacing earlier evidence or acknowledging
// a capture that only reached a userspace buffer. A failed file is never success.
func writeDemoEvidence(dir, prefix string, value any) error {
	if err := storageReady(dir); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, prefix+"-"+demoRandom()+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
