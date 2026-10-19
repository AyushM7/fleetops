package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"time"
)

func probe(ctx context.Context, client *http.Client, endpoint, origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("invalid probe origin")
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("invalid probe endpoint")
	}
	r.Host = u.Host
	r.Header.Set("X-Forwarded-Proto", "https")
	response, err := client.Do(r)
	if err != nil {
		return errors.New("probe unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("probe unhealthy")
	}
	return nil
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if probe(ctx, client, "http://127.0.0.1:8080/healthz", os.Getenv("FLEETOPS_PUBLIC_ORIGIN")) != nil {
		os.Exit(1)
	}
}
