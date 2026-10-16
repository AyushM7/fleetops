package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("FLEETOPS_DATABASE_URL", "")
	if _, err := ConfigFromEnv(); !errors.Is(err, ErrConfig) {
		t.Fatalf("missing URL: %v", err)
	}
	t.Setenv("FLEETOPS_DATABASE_URL", "postgres://localhost/fleetops")
	cfg, err := ConfigFromEnv()
	if err != nil || cfg.DatabaseURL != "postgres://localhost/fleetops" {
		t.Fatalf("config: %v", err)
	}
	parsed, err := poolConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.MaxConns != 8 || parsed.ConnConfig.ConnectTimeout != 5*time.Second || parsed.ConnConfig.RuntimeParams["statement_timeout"] != "5000" || parsed.ConnConfig.RuntimeParams["search_path"] != `"public"` {
		t.Fatal("pool bounds or timeouts not configured")
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{name: "empty URL", change: func(c *Config) { c.DatabaseURL = " " }},
		{name: "invalid URL", change: func(c *Config) { c.DatabaseURL = "postgres://user:%xx@host/db" }},
		{name: "invalid schema", change: func(c *Config) { c.Schema = "public;DROP SCHEMA public" }},
		{name: "empty schema", change: func(c *Config) { c.Schema = "" }},
		{name: "connect timeout", change: func(c *Config) { c.ConnectTimeout = 0 }},
		{name: "query timeout", change: func(c *Config) { c.QueryTimeout = time.Nanosecond }},
		{name: "pool size", change: func(c *Config) { c.MaxConns = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig("postgres://localhost/fleetops")
			tc.change(&cfg)
			if _, err := poolConfig(cfg); !errors.Is(err, ErrConfig) {
				t.Fatalf("config: %v", err)
			}
		})
	}
}

func TestDiagnosticsDoNotLeakSecrets(t *testing.T) {
	// A synthetic marker verifies redaction without using any real credential.
	const marker = "synthetic-sensitive-marker"
	cfg := DefaultConfig("postgres://user:" + marker + "@localhost/db")
	for _, output := range []string{fmt.Sprint(cfg), fmt.Sprintf("%#v", cfg), safeError(ErrQuery, errors.New(marker)).Error()} {
		if strings.Contains(output, marker) {
			t.Fatal("diagnostic exposed credential marker")
		}
	}
	data, err := json.Marshal(cfg)
	if err != nil || strings.Contains(string(data), marker) {
		t.Fatal("JSON exposed credential marker")
	}
	cfg.DatabaseURL = "postgres://user:" + marker + "%xx@localhost/db"
	_, err = Open(context.Background(), cfg)
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatal("parse error exposed credential marker")
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if err := safeError(ErrQuery, cause); !errors.Is(err, cause) || !errors.Is(err, ErrQuery) {
			t.Fatal("cancellation sentinel lost")
		}
	}
}
