package collision

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// terrainLevel is a level whose only geometry is one chunk of block terrain,
// 40x16x40 blocks of 1 unit whose top is at chunk Y 0 for a column of
// height 8, shaped by height (in blocks, per column).
func terrainLevel(origin mgl32.Vec3, rotation float32, smooth bool, height func(x, z int) int) *world.Level {
	tr := world.NewTerrain(mgl32.Vec3{-20, -8, -20}, 1, 40, 16, 40)
	for z := 0; z < tr.SizeZ; z++ {
		for x := 0; x < tr.SizeX; x++ {
			h := height(x-20, z-20)
			for y := 0; y < h && y < tr.SizeY; y++ {
				tr.SetBlock(x, y, z, 1)
			}
		}
	}
	tr.Smooth = smooth
	tr.Touch()
	ch := world.Chunk{ID: "ground", Origin: origin, Rotation: rotation, Scale: 1, Terrain: tr}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	lvl.Player.StepHeight = stepUpAllowance
	if err := world.RebuildIndexes(lvl); err != nil {
		panic(err)
	}
	return lvl
}

func eachTerrainMode(t *testing.T, fn func(t *testing.T, smooth bool)) {
	for _, smooth := range []bool{false, true} {
		name := "blocks"
		if smooth {
			name = "smooth"
		}
		t.Run(name, func(t *testing.T) { fn(t, smooth) })
	}
}

// rolling is gentle hills: never more than one block up or down between
// neighbouring columns.
func rolling(x, z int) int {
	return 8 + int(math.Round(1.5*math.Sin(float64(x)*0.25)+math.Cos(float64(z)*0.2)))
}

func flat(x, z int) int { return 8 }

// groundY is the world Y of the surface under world (wx, wz), from above.
func groundY(lvl *world.Level, wx, wz float32) float32 {
	ch := &lvl.Chunks[0]
	l := chunkLocal(ch, mgl32.Vec3{wx, 50, wz})
	y, _, ok := ch.Terrain.GroundBelow(l, 200)
	if !ok {
		return float32(math.Inf(-1))
	}
	return chunkWorld(ch, mgl32.Vec3{l.X(), y, l.Z()}).Y()
}

func TestAPlayerStandsOnTerrainWithNoBrushesInTheChunk(t *testing.T) {
	eachTerrainMode(t, func(t *testing.T, smooth bool) {
		lvl := terrainLevel(mgl32.Vec3{3, 2, -1}, 0.4, smooth, rolling)
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
		if f := feet(p); f < g-0.02 || f > g+1.05 {
			t.Fatalf("feet at %v, ground under the centre at %v", f, g)
		}
	})
}

func TestWalkingOverSmoothTerrainDoesNotBounceOrSink(t *testing.T) {
	lvl := terrainLevel(mgl32.Vec3{}, 0, true, rolling)
	p := world.NewPlayer(mgl32.Vec3{-12, 5, 0.5})
	for i := 0; i < 90; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	prev := p.Position.Y()
	for i := 0; i < 250; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{0.08, 0, 0.02}, false))
		if dy := p.Position.Y() - prev; dy > 0.2 || dy < -0.2 {
			t.Fatalf("frame %d moved %v vertically", i, dy)
		}
		// The footprint rests on the highest ground under it, which on a
		// slope is up to a radius' worth of rise above the ground under the
		// centre; downhill the body floats a frame between contacts.
		if f, g := feet(p), groundY(lvl, p.Position.X(), p.Position.Z()); f < g-0.05 || f > g+0.6 {
			t.Fatalf("frame %d: feet %v, ground %v", i, f, g)
		}
		prev = p.Position.Y()
	}
	if p.Position.X() < 5 {
		t.Fatalf("only reached x=%v", p.Position.X())
	}
}

func TestASteepTerrainCliffIsAWall(t *testing.T) {
	eachTerrainMode(t, func(t *testing.T, smooth bool) {
		cliff := func(x, z int) int {
			if x >= 2 {
				return 14
			}
			return 8
		}
		lvl := terrainLevel(mgl32.Vec3{}, 0, smooth, cliff)
		p := world.NewPlayer(mgl32.Vec3{-4, 2, 0.5})
		for i := 0; i < 200; i++ {
			MovePlayer(p, lvl, stepMove(mgl32.Vec3{0.1, 0, 0}, false))
		}
		if feet(p) > 0.5 {
			t.Fatalf("climbed a 6-unit cliff: feet at %v", feet(p))
		}
		if p.Position.X() > 1.8 {
			t.Fatalf("walked into the cliff: x=%v", p.Position.X())
		}
	})
}

func TestAPlayerFallsDownAMinedShaft(t *testing.T) {
	eachTerrainMode(t, func(t *testing.T, smooth bool) {
		lvl := terrainLevel(mgl32.Vec3{}, 0, smooth, flat)
		tr := lvl.Chunks[0].Terrain
		for z := 16; z < 24; z++ {
			for x := 16; x < 24; x++ {
				for y := 0; y < 8; y++ {
					tr.SetBlock(x, y, z, 0)
				}
			}
		}
		p := world.NewPlayer(mgl32.Vec3{0, 2, 0})
		for i := 0; i < 120; i++ {
			MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
		}
		if feet(p) > -1 {
			t.Fatalf("stood over a shaft: feet at %v", feet(p))
		}
	})
}

// A body in a cave stands on the cave's floor, under its roof — not lifted
// up through the roof onto the ground above.
func TestAPlayerStaysInACave(t *testing.T) {
	eachTerrainMode(t, func(t *testing.T, smooth bool) {
		lvl := terrainLevel(mgl32.Vec3{}, 0, smooth, func(x, z int) int { return 14 })
		tr := lvl.Chunks[0].Terrain
		for z := 12; z < 28; z++ {
			for x := 12; x < 28; x++ {
				for y := 4; y < 9; y++ {
					tr.SetBlock(x, y, z, 0)
				}
			}
		}
		// Cave floor at chunk Y 4-8 = -4, roof at 1.
		p := world.NewPlayer(mgl32.Vec3{0, -2.5, 0})
		var res PlayerMoveResult
		for i := 0; i < 120; i++ {
			res = MovePlayer(p, lvl, stepMove(mgl32.Vec3{0.03, 0, 0}, i < 80 && i%40 == 0))
		}
		if !res.OnGround {
			t.Fatal("not standing in the cave")
		}
		if f := feet(p); f < -4.1 || f > -3.2 {
			t.Fatalf("feet at %v, cave floor at -4", f)
		}
		if p.Position.Y()+p.BoundHalfY > 1.05 {
			t.Fatalf("head at %v, above the cave roof at 1", p.Position.Y()+p.BoundHalfY)
		}
	})
}

func TestRaycastsHitTerrain(t *testing.T) {
	eachTerrainMode(t, func(t *testing.T, smooth bool) {
		lvl := terrainLevel(mgl32.Vec3{2, 1, 0}, 0.7, smooth, rolling)
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
	})
}
