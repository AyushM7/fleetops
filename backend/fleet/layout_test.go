package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/AyushM7/fleetops/backend/db"
	"github.com/AyushM7/fleetops/backend/sim"
)

func TestLayoutUsesOwnedSimulatorConfiguration(t *testing.T) {
	cfg := sim.DefaultConfig()
	cfg.Walls = []sim.Point{{X: 2, Y: 2}}
	service, err := New(cfg, 42, &db.Pool{}, func() time.Time { return testTime }, authorized)
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if view.Layout.Width != cfg.Width || view.Layout.Height != cfg.Height || len(view.Layout.Cells) != cfg.Width*cfg.Height {
		t.Fatal("incorrect dimensions")
	}
	for _, cell := range view.Layout.Cells {
		if cell.Kind != string(cfg.CellTypeAt(sim.Point{X: cell.X, Y: cell.Y})) {
			t.Fatal("fabricated layout cell")
		}
	}
	cfg.Walls[0] = sim.Point{X: 3, Y: 3}
	view.Layout.Cells[0].Kind = "edited"
	again, err := service.ReadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.Layout.Cells[0].Kind != "station" || again.Layout.Cells[2+2*cfg.Width].Kind != "wall" {
		t.Fatal("layout shares caller memory")
	}
}
