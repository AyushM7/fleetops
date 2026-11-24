package visitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AyushM7/fleetops/backend/operations"
	"github.com/AyushM7/fleetops/backend/sim"
)

const (
	warehouseEventHistory = 32
	warehouseStreamLimit  = 2
	warehouseHeartbeat    = 5 * time.Second
	warehouseWriteTimeout = 3 * time.Second
)

type warehouseFrame struct {
	RuntimeID          string                `json:"runtimeId"`
	Generation         string                `json:"generation"`
	EventID            uint64                `json:"eventId"`
	Tick               int                   `json:"tick"`
	State              string                `json:"state"`
	OperationsMode     string                `json:"operationsMode"`
	OperationsActivity []operations.Activity `json:"operationsActivity,omitempty"`
	Snapshot           sim.State             `json:"snapshot"`
}

type warehouseEvent struct {
	id   uint64
	kind string
	data []byte
}

// The controller gate owns the history. Readers copy event headers and write
// the immutable JSON outside the gate so a slow stream cannot delay ticks.
type warehouseEvents struct {
	frames  []warehouseEvent
	changed chan struct{}
}

func (e *warehouseEvents) signal() {
	if e.changed != nil {
		close(e.changed)
		e.changed = make(chan struct{})
	}
}

func (c *demoController) ensureWarehouseSnapshot() {
	if len(c.run.events.frames) == 0 {
		c.publishWarehouse("snapshot")
	}
}

func (c *demoController) publishWarehouse(kind string) {
	run := c.run
	if run.warehouse == nil {
		return
	}
	events := &run.events
	id := uint64(1)
	if n := len(events.frames); n > 0 {
		id = events.frames[n-1].id + 1
	}
	snapshot := run.warehouse()
	data, err := json.Marshal(warehouseFrame{
		RuntimeID: run.runtimeID, Generation: run.generation, EventID: id, Tick: snapshot.Tick,
		State: c.warehouseState(), Snapshot: snapshot, OperationsMode: run.warehouseOperationsMode(),
		OperationsActivity: run.operationsActivity,
	})
	if err != nil {
		c.ready, c.running = false, false
		events.signal()
		return
	}
	if len(events.frames) == warehouseEventHistory {
		copy(events.frames, events.frames[1:])
		events.frames = events.frames[:len(events.frames)-1]
	}
	events.frames = append(events.frames, warehouseEvent{id, kind, data})
	if events.changed == nil {
		events.changed = make(chan struct{})
	} else {
		events.signal()
	}
}

func warehouseCursor(generation, raw string) uint64 {
	scope, sequence, ok := strings.Cut(raw, ":")
	if !ok || scope != generation {
		return 0
	}
	id, err := strconv.ParseUint(sequence, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func (c *demoController) warehouseEventBatch(run *demoRun, cursor uint64) ([]warehouseEvent, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.ready || c.run != run {
		return nil, nil
	}
	events := &run.events
	oldest, latest := events.frames[0].id, events.frames[len(events.frames)-1]
	if cursor == 0 || cursor < oldest-1 || cursor > latest.id {
		latest.kind = "snapshot"
		return []warehouseEvent{latest}, events.changed
	}
	offset := int(cursor + 1 - oldest)
	return slices.Clone(events.frames[offset:]), events.changed
}

func (g *visitorGateway) warehouseEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Last-Event-ID")) > 1 {
		http.Error(w, "warehouse event request denied", http.StatusForbidden)
		return
	}
	session := g.session(r)
	if session == nil {
		http.Error(w, "visitor session required", http.StatusUnauthorized)
		return
	}
	session.serveWarehouseEvents(w, r)
}

func (s *visitorSession) serveWarehouseEvents(w http.ResponseWriter, r *http.Request) {
	if !s.tryRequest(w) {
		return
	}
	deadline := s.expiresAt()
	if s.closed || !time.Now().Before(deadline) {
		s.mu.Unlock()
		http.Error(w, "visitor session expired", http.StatusUnauthorized)
		return
	}
	if s.streams >= warehouseStreamLimit {
		s.mu.Unlock()
		http.Error(w, "visitor stream capacity reached", http.StatusTooManyRequests)
		return
	}
	s.streams++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.streams--
		s.mu.Unlock()
	}()

	c := s.controller
	c.mu.Lock()
	run := c.run
	if c.closed || !c.ready || run.warehouse == nil {
		c.mu.Unlock()
		http.Error(w, "warehouse event stream unavailable", http.StatusConflict)
		return
	}
	c.ensureWarehouseSnapshot()
	c.mu.Unlock()
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	s.writeWarehouseStream(ctx, run, warehouseCursor(run.generation, r.Header.Get("Last-Event-ID")), w)
}

func (s *visitorSession) writeWarehouseStream(ctx context.Context, run *demoRun, cursor uint64, w http.ResponseWriter) {
	controller := http.NewResponseController(w)
	heartbeat := time.NewTicker(warehouseHeartbeat)
	defer heartbeat.Stop()
	if err := writeWarehouseEvent(controller, w, run.generation, warehouseEvent{}); err != nil {
		return
	}
	for ctx.Err() == nil {
		batch, changed := s.controller.warehouseEventBatch(run, cursor)
		if changed == nil {
			return
		}
		for _, event := range batch {
			if ctx.Err() != nil || writeWarehouseEvent(controller, w, run.generation, event) != nil {
				return
			}
			cursor = event.id
		}
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-changed:
		case <-heartbeat.C:
			if err := writeWarehouseEvent(controller, w, run.generation, warehouseEvent{}); err != nil {
				return
			}
		}
	}
}

func writeWarehouseEvent(controller *http.ResponseController, w io.Writer, generation string, event warehouseEvent) error {
	if err := controller.SetWriteDeadline(time.Now().Add(warehouseWriteTimeout)); err == nil {
		defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	} else if !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	var err error
	if event.id == 0 {
		_, err = io.WriteString(w, ": keep-alive\n\n")
	} else {
		_, err = fmt.Fprintf(w, "id: %s:%d\nevent: %s\ndata: %s\n\n", generation, event.id, event.kind, event.data)
	}
	if err != nil {
		return err
	}
	return controller.Flush()
}
