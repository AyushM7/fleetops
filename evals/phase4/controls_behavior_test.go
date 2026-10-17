//go:build fleetops_test

package fleet_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/graphql/model"
	"github.com/AyushM7/fleetops/backend/sim"
)

func stepRequest(c *demoController, generation string, tick int) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/demo/step", strings.NewReader("{}"))
	r.Header.Set("X-FleetOps-Generation", generation)
	r.Header.Set("X-FleetOps-Tick", strconv.Itoa(tick))
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	return w
}

func timeController(t *testing.T) (*demoController, *sim.Sim) {
	t.Helper()
	s, err := sim.NewReassignmentTestFixture()
	if err != nil {
		t.Fatal(err)
	}
	c := unitController()
	c.interactive, c.speed = true, "1"
	c.run.inspect = func(context.Context) (*model.FleetState, bool, error) {
		return &model.FleetState{Tick: s.Snapshot().Tick}, true, nil
	}
	c.run.step = func(context.Context) error { s.Tick(); return nil }
	return c, s
}

func pulse(c *demoController, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.advanceInteractive(context.Background(), now)
}

func TestDemoControlsPacingAndStep(t *testing.T) {
	var states []sim.State
	for _, speed := range []string{"0.5", "1", "2"} {
		c, s := timeController(t)
		generation := c.run.generation
		if w := controllerRequest(c, "/demo/speed/"+speed, generation); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		if controllerRequest(c, "/demo/play", generation).Code != 200 {
			t.Fatal("play failed")
		}
		due := c.nextTick
		for range 10 {
			controllerRequest(c, "/demo/play", generation)
		}
		if !due.Equal(c.nextTick) {
			t.Fatal("duplicate Play changed schedule")
		}
		if err := pulse(c, due.Add(-time.Nanosecond)); err != nil || s.Snapshot().Tick != 42 {
			t.Fatal("early tick", err)
		}
		if err := pulse(c, due); err != nil || s.Snapshot().Tick != 43 {
			t.Fatal("missing genuine tick", err)
		}
		if c.nextTick.Sub(due) != demoInterval(speed) {
			t.Fatal("wrong pace")
		}
		if stepRequest(c, generation, 43).Code != 409 {
			t.Fatal("step while running")
		}
		controllerRequest(c, "/demo/pause", generation)
		for range 20 {
			if err := pulse(c, due.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
		if s.Snapshot().Tick != 43 {
			t.Fatal("tick after pause acknowledgement")
		}
		if w := stepRequest(c, generation, 43); w.Code != 200 || s.Snapshot().Tick != 44 {
			t.Fatal("step is not exactly one tick", w.Body.String())
		}
		if stepRequest(c, generation, 43).Code != 409 {
			t.Fatal("duplicate/stale Step accepted")
		}
		states = append(states, s.Snapshot())
	}
	if !reflect.DeepEqual(states[0], states[1]) || !reflect.DeepEqual(states[1], states[2]) {
		t.Fatal("speed changed simulator semantics")
	}
	for _, invalid := range []string{"/demo/speed/3", "/demo/speed/0", "/demo/speed/1.0", "/demo/speed/NaN"} {
		if isTimeControl(invalid) {
			t.Fatal("arbitrary rate allowed", invalid)
		}
	}
}

func TestDemoControlsFailClosed(t *testing.T) {
	for _, state := range []string{"pending", "poisoned", "unknown"} {
		t.Run(state, func(t *testing.T) {
			c, s := timeController(t)
			c.run.inspect = func(context.Context) (*model.FleetState, bool, error) {
				if state == "unknown" {
					return nil, false, errors.New("database unavailable")
				}
				return &model.FleetState{Tick: 42, Poisoned: state == "poisoned"}, false, nil
			}
			for _, path := range []string{"/demo/play", "/demo/step"} {
				if controllerRequest(c, path, c.run.generation).Code < 400 {
					t.Fatal("gate bypassed")
				}
			}
			if s.Snapshot().Tick != 42 {
				t.Fatal("blocked state ticked")
			}
		})
	}
}

func TestDemoControlsDrainPauseAndRestart(t *testing.T) {
	for _, operation := range []string{"/demo/pause", "/demo/restart"} {
		t.Run(operation, func(t *testing.T) {
			c, s := timeController(t)
			generation := c.run.generation
			controllerRequest(c, "/demo/play", generation)
			entered, release, tickDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			c.run.step = func(context.Context) error { close(entered); <-release; s.Tick(); return nil }
			go func() { tickDone <- pulse(c, time.Now().Add(time.Hour)) }()
			<-entered
			response := make(chan *httptest.ResponseRecorder, 1)
			go func() { response <- controllerRequest(c, operation, generation) }()
			select {
			case <-response:
				t.Fatal("acknowledged before tick drained")
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			if err := <-tickDone; err != nil {
				t.Fatal(err)
			}
			select {
			case w := <-response:
				if w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			case <-time.After(time.Second):
				t.Fatal("deadlock")
			}
			for range 10 {
				if err := pulse(c, time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			if s.Snapshot().Tick != 43 || c.running {
				t.Fatal("retired/paused run advanced")
			}
			if operation == "/demo/restart" {
				for _, path := range []string{"/demo/play", "/demo/pause", "/demo/step", "/demo/speed/2"} {
					if controllerRequest(c, path, generation).Code != 409 {
						t.Fatal("stale control accepted")
					}
				}
			}
		})
	}
}

func TestDemoControlsRepeatedLifecycle(t *testing.T) {
	c, _ := timeController(t)
	makeRun := c.factory
	c.factory = func(ctx context.Context) (*demoRun, error) {
		run, err := makeRun(ctx)
		run.inspect = func(context.Context) (*model.FleetState, bool, error) { return &model.FleetState{Tick: 42}, true, nil }
		run.step = func(context.Context) error { return nil }
		return run, err
	}
	for range 100 {
		g := c.run.generation
		for _, path := range []string{"/demo/play", "/demo/pause", "/demo/play", "/demo/restart"} {
			if w := controllerRequest(c, path, g); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
		}
		if c.running || c.speed != "1" || !c.nextTick.IsZero() {
			t.Fatal("replacement retained scheduler state")
		}
	}
	if err := c.close(); err != nil {
		t.Fatal(err)
	}
	if controllerRequest(c, "/demo/play", c.run.generation).Code != 503 {
		t.Fatal("closed scheduler resumed")
	}
}

func TestDemoControlsConcurrentPulsesAndSteps(t *testing.T) {
	c, s := timeController(t)
	g := c.run.generation
	controllerRequest(c, "/demo/play", g)
	due := c.nextTick
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			if err := pulse(c, due); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if s.Snapshot().Tick != 43 {
		t.Fatal("duplicate automatic ticks")
	}
	controllerRequest(c, "/demo/pause", g)
	results := make(chan int, 32)
	for range 32 {
		workers.Go(func() { results <- stepRequest(c, g, 43).Code })
	}
	workers.Wait()
	close(results)
	successes := 0
	for code := range results {
		if code == 200 {
			successes++
		} else if code != 409 {
			t.Fatal(code)
		}
	}
	if successes != 1 || s.Snapshot().Tick != 44 {
		t.Fatal("simultaneous Steps advanced extra ticks", successes)
	}
}

func TestDemoControlsUnknownTickNeverRetries(t *testing.T) {
	c, s := timeController(t)
	c.run.step = func(context.Context) error { s.Tick(); return errors.New("acknowledgement unknown") }
	g := c.run.generation
	if stepRequest(c, g, 42).Code != 503 {
		t.Fatal("unknown Step acknowledged")
	}
	for range 10 {
		if stepRequest(c, g, 43).Code != 503 || controllerRequest(c, "/demo/play", g).Code != 503 {
			t.Fatal("unknown outcome reused")
		}
		if err := c.advance(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if s.Snapshot().Tick != 43 {
		t.Fatal("unknown tick retried")
	}
}

func TestDemoControlsSpeedChangeWhileRunning(t *testing.T) {
	c, s := timeController(t)
	g := c.run.generation
	controllerRequest(c, "/demo/play", g)
	for _, speed := range []string{"2", "0.5", "1"} {
		if w := controllerRequest(c, "/demo/speed/"+speed, g); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		if !c.running || s.Snapshot().Tick != 42 {
			t.Fatal("speed changed state or ticked")
		}
		if err := pulse(c, c.nextTick.Add(-time.Nanosecond)); err != nil || s.Snapshot().Tick != 42 {
			t.Fatal("speed caused an early tick", err)
		}
	}
	late := c.nextTick.Add(time.Hour)
	if err := pulse(c, late); err != nil || s.Snapshot().Tick != 43 {
		t.Fatal("late pulse caught up multiple ticks", err)
	}
	if err := pulse(c, late); err != nil || s.Snapshot().Tick != 43 {
		t.Fatal("repeated pulse duplicated tick", err)
	}
}

func TestDemoControlsUnconfirmedStepSnapshot(t *testing.T) {
	c, s := timeController(t)
	inspect := c.run.inspect
	reads := 0
	c.run.inspect = func(ctx context.Context) (*model.FleetState, bool, error) {
		reads++
		if reads > 1 {
			return nil, false, errors.New("status read failed after tick")
		}
		return inspect(ctx)
	}
	if w := stepRequest(c, c.run.generation, 42); w.Code != 503 {
		t.Fatal("unconfirmed resulting state acknowledged", w.Body.String())
	}
	if c.ready || c.running || s.Snapshot().Tick != 43 {
		t.Fatal("unknown snapshot was retried or left available")
	}
}

func TestDemoControlsCancelledObservationDoesNotStopRuntime(t *testing.T) {
	c, s := timeController(t)
	inspect := c.run.inspect
	c.run.inspect = func(ctx context.Context) (*model.FleetState, bool, error) {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		return inspect(ctx)
	}
	g := c.run.generation
	controllerRequest(c, "/demo/play", g)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/demo/status", strings.NewReader("{}")).WithContext(ctx)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	if w.Code != 503 || !c.ready || !c.running {
		t.Fatal("cancelled read changed simulation lifecycle")
	}
	if err := pulse(c, c.nextTick); err != nil || s.Snapshot().Tick != 43 {
		t.Fatal("cancelled browser read stopped time", err)
	}
}

func TestDemoControlsWaitForReadAndRejectCompetingStep(t *testing.T) {
	c, s := timeController(t)
	inspect := c.run.inspect
	entered, release := make(chan struct{}), make(chan struct{})
	c.run.inspect = func(ctx context.Context) (*model.FleetState, bool, error) {
		c.run.inspect = inspect
		close(entered)
		<-release
		return inspect(ctx)
	}
	readDone := make(chan struct{})
	go func() { controllerRequest(c, "/demo/status", ""); close(readDone) }()
	<-entered
	stepDone := make(chan *httptest.ResponseRecorder, 1)
	g := c.run.generation
	go func() { stepDone <- stepRequest(c, g, 42) }()
	deadline := time.Now().Add(time.Second)
	for !c.stepping.Load() {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("Step did not enter")
		}
		time.Sleep(time.Millisecond)
	}
	if stepRequest(c, g, 42).Code != 409 {
		t.Fatal("competing Step queued")
	}
	select {
	case <-stepDone:
		t.Fatal("read caused a premature control response")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-readDone
	select {
	case w := <-stepDone:
		if w.Code != 200 || s.Snapshot().Tick != 43 {
			t.Fatal("read prevented Step", w.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("read/control deadlock")
	}
}
