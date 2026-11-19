package visitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWarehouseEventReplayAndSnapshotReconciliation(t *testing.T) {
	run, err := warehouseTestRun(42)
	if err != nil {
		t.Fatal(err)
	}
	c := &demoController{run: run, ready: true, running: true}
	c.ensureWarehouseSnapshot()
	for range warehouseEventHistory + 5 {
		if err := run.step(context.Background()); err != nil {
			t.Fatal(err)
		}
		c.publishWarehouse("tick")
	}
	if len(run.events.frames) != warehouseEventHistory {
		t.Fatalf("history escaped its bound: %d", len(run.events.frames))
	}
	latest := uint64(warehouseEventHistory + 6)
	for _, cursor := range []string{"", run.generation + ":1", run.generation + ":999", "different:20", run.generation + ":invalid"} {
		batch, _ := c.warehouseEventBatch(run, warehouseCursor(run.generation, cursor))
		if len(batch) != 1 || batch[0].id != latest || batch[0].kind != "snapshot" {
			t.Fatalf("cursor %q did not give one latest reset snapshot: %+v", cursor, batch)
		}
		var frame warehouseFrame
		if err := json.Unmarshal(batch[0].data, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.RuntimeID != run.runtimeID || frame.Generation != run.generation || frame.EventID != latest || frame.Tick != frame.Snapshot.Tick || frame.Tick != int(latest-1) || len(frame.Snapshot.Robots) != 11 {
			t.Fatalf("inconsistent event metadata: %+v", frame)
		}
	}
	batch, changed := c.warehouseEventBatch(run, latest-2)
	if len(batch) != 2 || batch[0].id != latest-1 || batch[1].id != latest || batch[0].kind != "tick" || batch[1].kind != "tick" {
		t.Fatalf("retained history was not replayed in order: %+v", batch)
	}
	if idle, _ := c.warehouseEventBatch(run, latest); len(idle) != 0 {
		t.Fatal("caught-up stream received a duplicate event")
	}
	c.publishWarehouse("state")
	select {
	case <-changed:
	default:
		t.Fatal("new state did not wake stream subscribers")
	}
}

func TestWarehouseEventStreamIntegration(t *testing.T) {
	g, _ := visitorUnitGateway(t)
	server := httptest.NewUnstartedServer(g)
	defer server.Close()
	server.Config.WriteTimeout = 150 * time.Millisecond
	g.host = server.Listener.Addr().String()
	g.origin = "http://" + g.host
	server.Start()
	openWarehouse := func(seed int64) (*http.Cookie, *demoRun) {
		t.Helper()
		cookie := openVisitor(t, g)
		session := g.manager.lookup(cookie.Value)
		run, err := warehouseTestRun(seed)
		if err != nil {
			t.Fatal(err)
		}
		session.mu.Lock()
		session.controller.run, session.controller.interactive = run, true
		session.controller.selectFactory = func(context.Context, string) (*demoRun, error) { return warehouseTestRun(seed) }
		session.mu.Unlock()
		return cookie, run
	}
	a, runA := openWarehouse(42)
	b, runB := openWarehouse(99)

	connect := func(cookie *http.Cookie, cursor string, want int) *http.Response {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		t.Cleanup(cancel)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/demo/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if cursor != "" {
			req.Header.Set("Last-Event-ID", cursor)
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		if res.StatusCode != want {
			t.Fatalf("stream status %d, want %d", res.StatusCode, want)
		}
		return res
	}
	connect(nil, "", http.StatusUnauthorized).Body.Close()
	stream := connect(a, "", http.StatusOK)
	defer stream.Body.Close()
	if stream.Header.Get("Content-Type") != "text/event-stream" || stream.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected stream headers: %v", stream.Header)
	}
	reader := bufio.NewScanner(stream.Body)
	first := readWarehouseFrame(t, reader, "snapshot", runA.generation, 1)
	if first.State != "paused" || first.Tick != 0 || first.Snapshot.Seed != 42 {
		t.Fatalf("first stream frame did not contain the paused warehouse: %+v", first)
	}
	if w := visitorRequest(g, a, "/demo/play", runA.generation, "{}", nil); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	playing := readWarehouseFrame(t, reader, "state", runA.generation, 2)
	update := readWarehouseFrame(t, reader, "tick", runA.generation, 3)
	if playing.State != "running" || playing.Tick != 0 || update.Tick != 1 || first.RuntimeID != runA.runtimeID || update.RuntimeID != first.RuntimeID {
		t.Fatal("stream did not deliver control and tick updates in order")
	}

	other := connect(b, runA.generation+":3", http.StatusOK)
	defer other.Body.Close()
	otherReader := bufio.NewScanner(other.Body)
	isolated := readWarehouseFrame(t, otherReader, "snapshot", runB.generation, 1)
	if isolated.RuntimeID != runB.runtimeID || isolated.Snapshot.Seed != 99 || isolated.Tick != 0 {
		t.Fatal("foreign cursor crossed the visitor boundary")
	}

	replay := connect(a, runA.generation+":1", http.StatusOK)
	defer replay.Body.Close()
	replayed := readWarehouseFrame(t, bufio.NewScanner(replay.Body), "state", runA.generation, 2)
	if replayed.Tick != playing.Tick || replayed.State != playing.State {
		t.Fatal("reconnecting did not replay the retained state")
	}
	connect(a, "", http.StatusTooManyRequests).Body.Close()
	if w := visitorRequest(g, a, "/demo/restart", runA.generation, "{}", nil); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	for reader.Scan() {
		if line := reader.Text(); strings.HasPrefix(line, "id: ") && !strings.HasPrefix(line, "id: "+runA.generation+":") {
			t.Fatal("an old stream received a replacement generation")
		}
	}
	if err := reader.Err(); err != nil {
		t.Fatalf("restart did not promptly close the prior generation stream: %v", err)
	}
	session := g.manager.lookup(b.Value)
	session.mu.Lock()
	session.deadline = time.Now().Add(-time.Second)
	session.mu.Unlock()
	for otherReader.Scan() {
		if strings.HasPrefix(otherReader.Text(), "id: ") {
			t.Fatal("expired paused session received an unexpected frame")
		}
	}
	if err := otherReader.Err(); err != nil {
		t.Fatalf("expiry did not promptly close its stream: %v", err)
	}
}

func readWarehouseFrame(t *testing.T, reader *bufio.Scanner, kind, generation string, id uint64) warehouseFrame {
	t.Helper()
	for reader.Scan() {
		line := reader.Text()
		if strings.HasPrefix(line, ":") || line == "" {
			continue
		}
		if line != fmt.Sprintf("id: %s:%d", generation, id) || !reader.Scan() || reader.Text() != "event: "+kind || !reader.Scan() {
			t.Fatalf("unexpected event frame starting with %q, scanner error %v", line, reader.Err())
		}
		data, ok := strings.CutPrefix(reader.Text(), "data: ")
		var frame warehouseFrame
		if !ok || json.Unmarshal([]byte(data), &frame) != nil || frame.EventID != id || frame.Generation != generation || frame.Tick != frame.Snapshot.Tick {
			t.Fatalf("invalid event payload: %s", data)
		}
		return frame
	}
	t.Fatalf("stream ended awaiting %s event %d: %v", kind, id, reader.Err())
	return warehouseFrame{}
}
