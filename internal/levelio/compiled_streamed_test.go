package levelio

// compiled_streamed_test.go — a streamed chunk through the compile and back:
// baked, stored disabled, loaded with its surfaces already hidden, and shown by
// flipping the flag; and a moving chunk left live.

import (
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// streamedLevel is the sealed room with a streamed area inside its shell and a
// lift beside it.
func streamedLevel() *world.Level {
	lvl := sealedLevel()
	lvl.Chunks = append(lvl.Chunks,
		world.Chunk{ID: "area", Scale: 1, Disabled: true, Streamed: true,
			Brushes: []world.Brush{world.NewBoxBrush("crate", mgl32.Vec3{2, 0.5, 2}, mgl32.Vec3{0.5, 0.5, 0.5}, "concrete")}},
		world.Chunk{ID: "lift", Scale: 1, Moves: true,
			Brushes: []world.Brush{world.NewBoxBrush("deck", mgl32.Vec3{-2, 0.25, -2}, mgl32.Vec3{1, 0.25, 1}, "concrete")}},
	)
	return lvl
}

func TestAStreamedChunkIsBakedHiddenAndShownByItsFlag(t *testing.T) {
	path := compileToDisk(t, streamedLevel())
	testenv.WithoutGeometryUploads(t)
	lvl, err := LoadCompiledLevel(path)
	if err != nil {
		t.Fatalf("LoadCompiledLevel: %v", err)
	}

	var area, lift *world.Chunk
	for ci := range lvl.Chunks {
		switch lvl.Chunks[ci].ID {
		case "area":
			area = &lvl.Chunks[ci]
		case "lift":
			lift = &lvl.Chunks[ci]
		}
	}
	if area == nil || lift == nil {
		t.Fatal("a chunk was dropped in the compile")
	}
	if !area.Compiled || area.IsEnabled() {
		t.Fatalf("the streamed chunk came back Compiled=%v enabled=%v, want baked and still off", area.Compiled, area.IsEnabled())
	}
	if lift.Compiled {
		t.Error("the moving chunk was baked; its surfaces would stay behind")
	}

	areaSurfaces := 0
	for i := range lvl.Static.Surfaces {
		if lvl.Static.Surfaces[i].Chunk != "area" {
			continue
		}
		areaSurfaces++
		// Hidden from the moment the load finished, before any frame's
		// SyncChunkGeometry — or the area flashes up as the level appears.
		if !lvl.Static.Hidden(i) {
			t.Fatalf("surface %d of the streamed chunk is visible straight after load", i)
		}
	}
	if areaSurfaces == 0 {
		t.Fatal("the streamed chunk has no compiled surfaces")
	}
	for i := range lvl.Static.Surfaces {
		if lvl.Static.Surfaces[i].Chunk == "lift" {
			t.Fatal("the moving chunk has compiled surfaces")
		}
	}

	world.SetChunkEnabled(area, true)
	world.SyncChunkGeometry(&lvl)
	for i := range lvl.Static.Surfaces {
		if lvl.Static.Surfaces[i].Chunk == "area" && lvl.Static.Hidden(i) {
			t.Fatal("switching the streamed chunk on left its surfaces hidden")
		}
	}
}

func TestStreamedAndMovesRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	raw, err := LevelToRaw(streamedLevel())
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string][2]bool{}
	for _, rc := range raw.Chunks {
		flags[rc.ID] = [2]bool{rc.Streamed, rc.Moves}
	}
	if flags["area"] != [2]bool{true, false} || flags["lift"] != [2]bool{false, true} || flags["static"] != [2]bool{} {
		t.Errorf("markers on the way out: %v", flags)
	}
}
