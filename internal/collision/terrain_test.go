package collision

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// terrainLevel is a level whose only geometry is one chunk of terrain, 40x40
// cells of 1 unit centred on the chunk origin, shaped by height.
func terrainLevel(origin mgl32.Vec3, rotation float32, height func(x, z float32) float32) *world.Level {
	tr := world.NewTerrain(mgl32.Vec3{-20, 0, -20}, 1, 40, 40)
	for z := 0; z <= tr.CellsZ; z++ {
		for x := 0; x <= tr.CellsX; x++ {
			tr.Heights[tr.Index(x, z)] = height(float32(x)-20, float32(z)-20)
		}
	}
	ch := world.Chunk{ID: "ground", Origin: origin, Rotation: rotation, Scale: 1, Terrain: tr}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	lvl.Player.StepHeight = stepUpAllowance
	if err := world.RebuildIndexes(lvl); err != nil {
		panic(err)
	}
	return lvl
}

func rolling(x, z float32) float32 {
	return 0.8*float32(math.Sin(float64(x)*0.35)) + 0.6*float32(math.Cos(float64(z)*0.3))
}

func groundY(lvl *world.Level, wx, wz float32) float32 {
	ch := &lvl.Chunks[0]
	l := chunkLocal(ch, mgl32.Vec3{wx, 0, wz})
	h, _, _ := ch.Terrain.HeightAt(l.X(), l.Z())
	return chunkWorld(ch, mgl32.Vec3{l.X(), h, l.Z()}).Y()
}

func TestAPlayerStandsOnTerrainWithNoBrushesInTheChunk(t *testing.T) {
	lvl := terrainLevel(mgl32.Vec3{3, 2, -1}, 0.4, rolling)
	p := world.NewPlayer(mgl32.Vec3{1, 8, 2})
	var res PlayerMoveResult
	for i := 0; i < 120; i++ {
		res = MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	if !res.OnGround {
		t.Fatal("not on the ground after settling")
	}
	// A cylinder rests on the highest ground under its footprint, so its
	// soles are at or a little above the ground under its centre.
	g := groundY(lvl, p.Position.X(), p.Position.Z())
	if f := feet(p); f < g-0.01 || f > g+0.5 {
		t.Fatalf("feet at %v, ground under the centre at %v", f, g)
	}
}

func TestWalkingOverRollingTerrainDoesNotBounceOrSink(t *testing.T) {
	lvl := terrainLevel(mgl32.Vec3{}, 0, rolling)
	p := world.NewPlayer(mgl32.Vec3{-12, 5, 0.5})
	for i := 0; i < 90; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	prev := p.Position.Y()
	for i := 0; i < 250; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{0.08, 0, 0.02}, false))
		if dy := p.Position.Y() - prev; dy > 0.15 || dy < -0.15 {
			t.Fatalf("frame %d moved %v vertically", i, dy)
		}
		// Downhill the body briefly outpaces gravity and floats a frame or
		// two between contacts — brush floors do the same, there is no
		// ground snap — but never by more than a hand, and never sinks in.
		if f, g := feet(p), groundY(lvl, p.Position.X(), p.Position.Z()); f < g-0.02 || f > g+0.25 {
			t.Fatalf("frame %d: feet %v, ground %v", i, f, g)
		}
		prev = p.Position.Y()
	}
	if p.Position.X() < 5 {
		t.Fatalf("only reached x=%v", p.Position.X())
	}
}

func TestASteepTerrainCliffIsAWall(t *testing.T) {
	cliff := func(x, z float32) float32 {
		if x >= 2 {
			return 6
		}
		return 0
	}
	lvl := terrainLevel(mgl32.Vec3{}, 0, cliff)
	p := world.NewPlayer(mgl32.Vec3{-4, 2, 0.5})
	for i := 0; i < 200; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{0.1, 0, 0}, false))
	}
	if feet(p) > 0.5 {
		t.Fatalf("climbed a 6-unit cliff: feet at %v", feet(p))
	}
	if p.Position.X() > 1.5 {
		t.Fatalf("walked into the cliff: x=%v", p.Position.X())
	}
}

func TestAPlayerFallsThroughATerrainHole(t *testing.T) {
	lvl := terrainLevel(mgl32.Vec3{}, 0, func(x, z float32) float32 { return 0 })
	tr := lvl.Chunks[0].Terrain
	for z := 17; z < 23; z++ {
		for x := 17; x < 23; x++ {
			tr.SetHole(x, z, true)
		}
	}
	p := world.NewPlayer(mgl32.Vec3{0, 2, 0})
	for i := 0; i < 120; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	if feet(p) > -1 {
		t.Fatalf("stood over a hole: feet at %v", feet(p))
	}
}

func TestRaycastsHitTerrain(t *testing.T) {
	lvl := terrainLevel(mgl32.Vec3{2, 1, 0}, 0.7, rolling)
	for _, xz := range [][2]float32{{0, 0}, {5, -3}, {-7, 8}} {
		want := groundY(lvl, xz[0], xz[1])
		res := Raycast(lvl, mgl32.Vec3{xz[0], 30, xz[1]}, mgl32.Vec3{0, -1, 0}, DefaultRayFilter(100))
		h, ok := res.First()
		if !ok || !res.Blocked {
			t.Fatalf("ray at %v hit nothing", xz)
		}
		if got := 30 - h.T; math.Abs(float64(got-want)) > 1e-3 {
			t.Fatalf("ray at %v stopped at y=%v, ground at %v", xz, got, want)
		}
		if h.Kind != RayHitTerrain || h.Chunk != "ground" {
			t.Fatalf("hit %+v, want the ground chunk's terrain", h)
		}
		if !LineBlocked(lvl, mgl32.Vec3{xz[0], 30, xz[1]}, mgl32.Vec3{xz[0], want - 1, xz[1]}, nil) {
			t.Fatal("a line through the ground was not blocked")
		}
	}
}
