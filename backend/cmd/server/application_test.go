package main

import (
	"context"
	"testing"
)

func TestApplicationRequiresExplicitVisitorConfiguration(t *testing.T) {
	t.Setenv("FLEETOPS_MODE", "")
	handler, cleanup, err := application(context.Background())
	if err == nil || handler != nil || cleanup != nil {
		t.Fatal("application accepted missing visitor configuration")
	}
}

func TestApplicationRejectsSharedTickOverride(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "t"} {
		t.Setenv("FLEETOPS_AUTO_TICK", value)
		_, _, err := application(context.Background())
		if err != errTickConfig {
			t.Fatal("shared automatic tick override accepted", value, err)
		}
	}
}
