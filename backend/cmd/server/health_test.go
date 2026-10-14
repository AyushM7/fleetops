package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParsePort(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  int
	}{
		{"default", "", 8080},
		{"custom", "9090", 9090},
		{"minimum", "1", 1},
		{"maximum", "65535", 65535},
		{"text", "abc", 0},
		{"zero", "0", 0},
		{"negative", "-1", 0},
		{"too large", "65536", 0},
		{"overflow", "999999999999999999999999", 0},
		{"spaces", " 8080 ", 0},
		{"decimal", "8080.5", 0},
		{"sign", "+8080", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePort(tc.value)
			if tc.want == 0 {
				if err == nil || !strings.Contains(err.Error(), "invalid PORT") {
					t.Fatalf("expected clear PORT error; got %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("parsePort = (%d, %v), want (%d, nil)", got, err, tc.want)
			}
		})
	}
}

func TestHealthGET(t *testing.T) {
	recorder := httptest.NewRecorder()
	newHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := recorder.Body.String(); got != `{"status":"ok"}` {
		t.Errorf("body = %q", got)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Status != "ok" {
		t.Errorf("JSON = %+v, error = %v", body, err)
	}
}

func TestHealthRejectsNonGET(t *testing.T) {
	for _, method := range []string{"POST", "HEAD", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT"} {
		t.Run(method, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			newHandler().ServeHTTP(recorder, httptest.NewRequest(method, "/healthz", nil))
			if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET" {
				t.Fatalf("status = %d, Allow = %q", recorder.Code, recorder.Header().Get("Allow"))
			}
		})
	}
}

type stubServer struct {
	started     chan struct{}
	result      chan error
	shutdownErr error
	deadline    time.Duration
	closed      bool
}

func (s *stubServer) ListenAndServe() error {
	close(s.started)
	return <-s.result
}

func (s *stubServer) Shutdown(ctx context.Context) error {
	deadline, _ := ctx.Deadline()
	s.deadline = time.Until(deadline)
	s.result <- http.ErrServerClosed
	return s.shutdownErr
}

func (s *stubServer) Close() error {
	s.closed = true
	return nil
}

func TestServeSignalShutdown(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := &stubServer{started: make(chan struct{}), result: make(chan error, 1)}
		failure := errors.New("shutdown failure")
		if fail {
			s.shutdownErr = failure
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-s.started; cancel() }()
		err := serve(ctx, s)
		cancel()
		if fail && (!errors.Is(err, failure) || !s.closed) {
			t.Fatalf("shutdown failure not propagated or force-closed: %v", err)
		}
		if !fail && (err != nil || s.closed) {
			t.Fatalf("clean shutdown = %v, closed = %v", err, s.closed)
		}
		if s.deadline <= 0 || s.deadline > 5*time.Second {
			t.Fatalf("shutdown deadline = %v, want positive and <= 5s", s.deadline)
		}
	}
}

func TestServeUnexpectedFailure(t *testing.T) {
	s := &stubServer{started: make(chan struct{}), result: make(chan error, 1)}
	failure := errors.New("listen failure")
	s.result <- failure
	if err := serve(context.Background(), s); !errors.Is(err, failure) {
		t.Fatalf("serve = %v, want listen failure", err)
	}
}
