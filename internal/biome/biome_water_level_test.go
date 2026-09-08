package biome

// biome_water_level_test.go — where a water plane ends up for a given depth.
//
// positionBiomeWaterSurface is the whole of what raising or lowering the
// surface does (the mesh around it is flat at local y=0), so it is where the
// signed-depth behaviour and the cull box that has to follow it are checked.
// It does no GL work, unlike the rebuild it was split out of, which is what
// makes this testable without a context.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// waterChunk is squareChunk with a Water sector painted inside it at the given
// depth, plus the empty surface the sync would have appended for it.
func waterChunk(depth float32) (*world.Level, *world.Chunk, *world.BiomeSector, *world.WaterSurface) {
	ch := squareChunk("ground", 0, 40, 8)
	ch.BiomeSectors = []world.BiomeSector{{
		ID:     "lake",
		Brush:  world.BiomeBrush{Kind: world.BiomeWater, WaterDepth: depth},
		Points: []mgl32.Vec2{{10, 10}, {30, 10}, {30, 30}, {10, 30}},
	}}
	ch.WaterSurfaces = []world.WaterSurface{{ID: "lake", ChunkID: "ground"}}
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	c := &lvl.Chunks[0]
	return lvl, c, &c.BiomeSectors[0], &c.WaterSurfaces[0]
}

// TestWaterSurfaceSitsBelowTheFloor is the ordinary case, and pins which way
// the sign points: depth is measured DOWN, so a positive one digs.
func TestWaterSurfaceSitsBelowTheFloor(t *testing.T) {
	lvl, ch, bs, w := waterChunk(1.5)
	bindLevel(t, lvl)

	positionBiomeWaterSurface(lvl, ch, bs, w)

	if got, want := w.Position.Y(), float32(8-1.5); got != want {
		t.Fatalf("surface Y = %g, want %g (floor 8 minus depth 1.5)", got, want)
	}
}

// TestNegativeWaterDepthRaisesTheSurface is the feature: a plane lifted above
// the floor it was sampled against, flooding flat ground rather than filling a
// dug basin.
func TestNegativeWaterDepthRaisesTheSurface(t *testing.T) {
	lvl, ch, bs, w := waterChunk(-2)
	bindLevel(t, lvl)

	positionBiomeWaterSurface(lvl, ch, bs, w)

	if got, want := w.Position.Y(), float32(10); got != want {
		t.Fatalf("surface Y = %g, want %g (floor 8 raised by 2)", got, want)
	}
}

// TestUnsetWaterDepthUsesTheDefault covers a sector painted before the depth
// was ever written — 0 is the sentinel, not a level flush with the floor.
func TestUnsetWaterDepthUsesTheDefault(t *testing.T) {
	lvl, ch, bs, w := waterChunk(0)
	bindLevel(t, lvl)

	positionBiomeWaterSurface(lvl, ch, bs, w)

	if got, want := w.Position.Y(), 8-world.DefaultWaterDepth; got != want {
		t.Fatalf("surface Y = %g, want %g", got, want)
	}
}

// TestRaisedWaterCullBoxStillReachesTheLakebed is the trap a signed depth
// sets: the box is centred on the surface but has to contain the floor the
// surface was offset from, or a raised plane is culled away while the ground
// under it is still on screen.
func TestRaisedWaterCullBoxStillReachesTheLakebed(t *testing.T) {
	for _, depth := range []float32{3, -3} {
		lvl, ch, bs, w := waterChunk(depth)
		bindLevel(t, lvl)

		positionBiomeWaterSurface(lvl, ch, bs, w)
		lo, hi := w.LocalAABB()

		const floorY = float32(8)
		if lo.Y() > floorY || hi.Y() < floorY {
			t.Errorf("depth %g: bounds [%g,%g] do not contain the floor at %g",
				depth, lo.Y(), hi.Y(), floorY)
		}
	}
}

// TestWaterLevelDirtyDoesNotRebuildTheMesh is the cheap path: a depth-only
// edit slides the plane and leaves its geometry alone, which is what lets a
// gizmo drag and a script-driven flood write it every frame.
func TestWaterLevelDirtyDoesNotRebuildTheMesh(t *testing.T) {
	lvl, ch, bs, w := waterChunk(1)
	bindLevel(t, lvl)

	positionBiomeWaterSurface(lvl, ch, bs, w)
	// Stand in for an uploaded mesh. The sync must not touch it, and must not
	// take the rebuild branch it would take for a Vao of 0.
	w.Geometry.Vao = 42
	before := w.Position

	bs.Brush.WaterDepth = 4
	bs.WaterLevelDirty = true
	syncChunkWaterSurfaces(lvl, ch)

	if w.Geometry.Vao != 42 {
		t.Error("a depth-only change rebuilt the geometry")
	}
	if w.Position.Y() == before.Y() {
		t.Fatalf("surface did not move: still at Y %g", w.Position.Y())
	}
	if got, want := w.Position.Y(), float32(8-4); got != want {
		t.Errorf("surface Y = %g, want %g", got, want)
	}
	if bs.WaterLevelDirty {
		t.Error("sync left WaterLevelDirty set, so it would run again next frame")
	}
}
