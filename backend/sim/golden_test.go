package sim

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "regenerate the simulator golden snapshot")

var warehouseGoldenProposal = flag.String("warehouse-golden-proposal", "", "write a warehouse snapshot proposal for owner review without updating the fixture")

func TestGoldenSnapshot(t *testing.T) {
	cfg := DefaultConfig()
	s := newTestSim(t, cfg, 42)
	for i := 0; i < 200; i++ {
		s.Tick()
	}
	assertInvariants(t, s, cfg)
	got, err := json.MarshalIndent(s.Snapshot(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	repeat := newTestSim(t, cfg, 42)
	for i := 0; i < 200; i++ {
		repeat.Tick()
	}
	repeated, err := json.MarshalIndent(repeat.Snapshot(), "", "  ")
	if err != nil || !bytes.Equal(got, append(repeated, '\n')) {
		t.Fatalf("repeated seed-42/tick-200 JSON differs: %v", err)
	}
	path := filepath.Join("testdata", "golden_seed42_tick200.json")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("snapshot differs from golden fixture; review changes before explicitly regenerating with -update")
	}
}

func TestWarehouseGoldenSnapshot(t *testing.T) {
	cfg := WarehouseConfig()
	s, repeat := newTestSim(t, cfg, 42), newTestSim(t, cfg, 42)
	for tick := 0; tick < 300; tick++ {
		s.Tick()
		repeat.Tick()
		assertInvariants(t, s, cfg)
	}
	got, err := json.MarshalIndent(s.Snapshot(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	repeated, err := json.MarshalIndent(repeat.Snapshot(), "", "  ")
	if err != nil || !bytes.Equal(got, append(repeated, '\n')) {
		t.Fatalf("repeated warehouse seed-42/tick-300 JSON differs: %v", err)
	}
	path := filepath.Join("testdata", "golden_warehouse_seed42_tick300.json")
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		if *warehouseGoldenProposal != "" {
			if err := os.WriteFile(*warehouseGoldenProposal, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Fatal("warehouse snapshot differs from golden fixture; review changes before regenerating with -update")
	}
}
