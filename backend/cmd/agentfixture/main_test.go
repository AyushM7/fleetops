//go:build fleetops_test

package main

import "testing"

func TestFixtureCannotBindPubliclyOrWithoutOptIn(t *testing.T) {
	t.Setenv("PORT", "18081")
	t.Setenv("FLEETOPS_AGENT_FIXTURE_ENABLE", "")
	if _, err := fixtureAddress(); err == nil {
		t.Fatal("fixture accepted missing explicit opt-in")
	}
	t.Setenv("FLEETOPS_AGENT_FIXTURE_ENABLE", "1")
	address, err := fixtureAddress()
	if err != nil || address != "127.0.0.1:18081" {
		t.Fatal("fixture not restricted to loopback", address, err)
	}
	for _, port := range []string{"0", "65536", "127.0.0.1:18081"} {
		t.Setenv("PORT", port)
		if _, err := fixtureAddress(); err == nil {
			t.Fatal("invalid fixture port accepted")
		}
	}
}
