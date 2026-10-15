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
