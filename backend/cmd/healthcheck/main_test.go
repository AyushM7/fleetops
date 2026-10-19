package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbe(t *testing.T) {
	for _, status := range []int{200, 302, 403, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "demo.example.org" || r.Header.Get("X-Forwarded-Proto") != "https" || r.URL.Path != "/healthz" {
					t.Error("incorrect proxy contract")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			err := probe(context.Background(), server.Client(), server.URL+"/healthz", "https://demo.example.org")
			if (err == nil) != (status == 200) {
				t.Fatalf("status %d: error=%v", status, err)
			}
		})
	}
}

func TestProbeRejectsInvalidOriginAndCancelledRequest(t *testing.T) {
	if probe(context.Background(), http.DefaultClient, "http://127.0.0.1:1", "http://demo.example.org") == nil {
		t.Fatal("accepted plaintext origin")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if probe(ctx, http.DefaultClient, "http://127.0.0.1:1", "https://demo.example.org") == nil {
		t.Fatal("accepted cancelled request")
	}
}
