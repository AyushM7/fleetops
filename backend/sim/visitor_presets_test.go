package sim

import (
	"reflect"
	"testing"
)

func TestVisitorPresetsOwnTheirState(t *testing.T) {
	for _, id := range []string{"blocked-reassignment", "routine-charging", "healthy-operations", "blocked-no-candidate"} {
		t.Run(id, func(t *testing.T) {
			a, w, h, err := NewVisitorPreset(id)
			if err != nil || w <= 0 || h <= 0 {
				t.Fatal(err)
			}
			b, _, _, err := NewVisitorPreset(id)
			if err != nil {
				t.Fatal(err)
			}
			before := b.Snapshot()
			if !reflect.DeepEqual(a.Snapshot(), before) {
				t.Fatal("preset nondeterministic")
			}
			a.Tick()
			if !reflect.DeepEqual(b.Snapshot(), before) {
				t.Fatal("shared mutable visitor state")
			}
		})
	}
	if _, _, _, err := NewVisitorPreset("arbitrary-config"); err == nil {
		t.Fatal("untrusted preset accepted")
	}
}
