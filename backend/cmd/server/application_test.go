package main

import (
	"context"
	"testing"
)

func TestApplicationRequiresHashConfiguration(t *testing.T) {
	t.Setenv("FLEETOPS_AUTH_TOKENS", "")
	handler, cleanup, err := application(context.Background())
	if err == nil || handler != nil || cleanup != nil {
		t.Fatal("application accepted missing authentication")
	}
}
