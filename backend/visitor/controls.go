package visitor

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

func isTimeControl(path string) bool {
	switch path {
	case "/demo/play", "/demo/pause", "/demo/step", "/demo/speed/0.5", "/demo/speed/1", "/demo/speed/2":
		return true
	}
	return false
}

func demoInterval(speed string) time.Duration {
	switch speed {
	case "0.5":
		return 4 * time.Second
	case "2":
		return time.Second
	default:
		return 2 * time.Second
	}
}

// The existing single harness pulse calls this under the lifecycle mutex.
// No catch-up ticks, per-request timers, or worker per Play are created.
func (c *demoController) advanceInteractive(ctx context.Context, now time.Time) error {
	if !c.running || now.Before(c.nextTick) {
		return nil
	}
	status, err := c.status(ctx)
	if err != nil || status.State != "running" {
		c.running = false
		return err
	}
	if err := c.run.step(ctx); err != nil {
		c.ready, c.running = false, false
		return err
	}
	c.ticks++
	if c.ticks >= 1200 {
		c.ready, c.running = false, false
	}
	c.nextTick = now.Add(demoInterval(c.speed))
	return nil
}

func (c *demoController) status(ctx context.Context) (demoStatus, error) {
	status := demoStatus{Generation: c.run.generation, Ready: c.ready && !c.closed, ScenarioID: c.run.scenarioID}
	if !c.interactive {
		return status, nil
	}
	status.State, status.Speed = "unavailable", c.speed
	if status.Speed == "" {
		status.Speed = "1"
	}
	if !status.Ready {
		return status, nil
	}
	snapshot, decided, err := c.run.inspect(ctx)
	if err != nil {
		// Poll cleanup can cancel this read. Observations cannot revoke ownership.
		status.Ready = false
		return status, err
	}
	status.Snapshot = snapshot
	status.Evidence = c.run.evidence
	if snapshot.Poisoned {
		c.running = false
		return status, nil
	}
	status.State = "paused"
	if !decided {
		status.State, c.running = "awaiting_decision", false
	} else if c.running {
		status.State = "running"
	}
	return status, nil
}

func (c *demoController) writeStatus(w http.ResponseWriter, ctx context.Context) error {
	status, err := c.status(ctx)
	if err != nil {
		http.Error(w, "simulation status unconfirmed; refresh without retrying the operation", 503)
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
	return nil
}

func (c *demoController) control(w http.ResponseWriter, r *http.Request) {
	// Reads share the lifecycle gate; only competing Steps must be rejected.
	if r.URL.Path == "/demo/step" {
		if !c.stepping.CompareAndSwap(false, true) {
			http.Error(w, "Step in progress; refresh status before retrying", 409)
			return
		}
		defer c.stepping.Store(false)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.Header.Get("X-FleetOps-Generation") != c.run.generation {
		http.Error(w, "scenario changed; refresh status", 409)
		return
	}
	if !c.interactive || !c.ready || c.closed || c.busy.Load() {
		http.Error(w, "simulation unavailable", 503)
		return
	}
	status, err := c.status(r.Context())
	if err != nil || status.State == "unavailable" {
		http.Error(w, "simulation unavailable; reconcile unknown outcomes", 503)
		return
	}
	if c.applyControl(w, r, status) {
		if err := c.writeStatus(w, r.Context()); err != nil {
			c.ready, c.running = false, false
		}
	}
}

func (c *demoController) applyControl(w http.ResponseWriter, r *http.Request, status demoStatus) bool {
	now := time.Now()
	switch r.URL.Path {
	case "/demo/play", "/demo/step":
		if status.State == "awaiting_decision" {
			http.Error(w, "human decision required before ticking", 409)
			return false
		}
		if r.URL.Path == "/demo/step" {
			return c.stepOnce(w, r, status, now)
		}
		if !c.running {
			c.running, c.nextTick = true, now.Add(demoInterval(c.speed))
		}
	case "/demo/pause":
		// Holding this gate drains the active tick; queued pulses recheck running.
		c.running = false
	default:
		switch r.URL.Path {
		case "/demo/speed/0.5":
			c.speed = "0.5"
		case "/demo/speed/1":
			c.speed = "1"
		case "/demo/speed/2":
			c.speed = "2"
		default:
			http.NotFound(w, r)
			return false
		}
		c.nextTick = now.Add(demoInterval(c.speed))
	}
	return true
}

func (c *demoController) stepOnce(w http.ResponseWriter, r *http.Request, status demoStatus, now time.Time) bool {
	if c.running || r.Header.Get("X-FleetOps-Tick") != strconv.Itoa(status.Snapshot.Tick) || now.Sub(c.lastStep) < 200*time.Millisecond {
		http.Error(w, "Step requires a fresh paused tick; refresh status", 409)
		return false
	}
	c.lastStep = now
	if err := c.run.step(r.Context()); err != nil {
		c.ready, c.running = false, false
		http.Error(w, "Step outcome unknown; refresh status without retrying", 503)
		return false
	}
	c.ticks++
	if c.ticks >= 1200 {
		c.ready, c.running = false, false
	}
	return true
}
