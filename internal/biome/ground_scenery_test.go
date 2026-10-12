package biome

// ground_scenery_test.go — a column finds mesh-collision scenery: the
// procedural terrain's tiles are ground to scatter and biome volumes.

import (
	"math"
	"testing"

	"foundry/internal/meshcollide"
	"foundry/internal/nanitecook"
	"foundry/internal/procgen/meshout"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// slopeHeight is the test tile's surface: a gentle tilt and a bump.
func slopeHeight(x, z float32) float32 {
	return 3 + 0.1*x + float32(math.Sin(float64(z)*0.3))
}

// tileScenery is a closed 16 m heightfield tile, cooked, placed at pos with
// the given collision mode.
func tileScenery(t *testing.T, pos mgl32.Vec3, mode meshcollide.Mode) world.Scenery {
	t.Helper()
	g := meshout.HeightGrid{X0: -8, Z0: -8, SizeX: 16, SizeZ: 16, NX: 32, NZ: 32, Closed: true, Base: -4,
		Height: func(i, j int) float32 { return slopeHeight(-8+float32(i)*0.5, -8+float32(j)*0.5) }}
	var b meshout.Builder
	b.Slot("ground", "")
	g.Build(&b)
	m := b.Mesh()
	cooked, err := nanitecook.CookMesh(m, "tile", nanitecook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lo, hi := b.Bounds()
	return world.Scenery{ID: "tile", Nanite: cooked, Position: pos, Solid: true, Collision: mode, LocalMin: lo, LocalMax: hi}
}

func TestSurfaceUnderFindsMeshScenery(t *testing.T) {
	at := mgl32.Vec3{100, 20, -50}
	lvl := &world.Level{Scenery: []world.Scenery{tileScenery(t, at, meshcollide.ModeMesh)}}
	q := NewGroundQuery(lvl, "")
	for _, p := range [][2]float32{{0, 0}, {3.2, -5.1}, {-7.5, 6.6}} {
		s := q.SurfaceUnder(at.X()+p[0], 100, at.Z()+p[1], 200)
		if !s.Found {
			t.Fatalf("nothing under the tile at local %v", p)
		}
		want := at.Y() + slopeHeight(p[0], p[1])
		// The collision cut is within a few centimetres of the drawn mesh, and
		// the mesh is linear between half-metre samples.
		if d := s.Height - want; d > 0.08 || d < -0.08 {
			t.Errorf("local %v: ground at %.3f, want %.3f", p, s.Height, want)
		}
		if s.Normal.Y() < 0.9 || s.Normal.X() > 0 {
			t.Errorf("local %v: normal %v, want up and tilted toward -X", p, s.Normal)
		}
	}
	// Off the tile: nothing. Below the column's reach: nothing.
	if s := q.SurfaceUnder(at.X()+20, 100, at.Z(), 200); s.Found {
		t.Errorf("found ground at %v off the tile", s.Height)
	}
	if s := q.SurfaceUnder(at.X(), 100, at.Z(), 50); s.Found {
		t.Errorf("found ground at %v beyond the drop", s.Height)
	}
}

func TestSurfaceUnderSkipsSceneryThatIsNotGround(t *testing.T) {
	at := mgl32.Vec3{0, 0, 0}
	for name, s := range map[string]world.Scenery{
		"collision none":  tileScenery(t, at, meshcollide.ModeNone),
		"collision hulls": tileScenery(t, at, meshcollide.ModeHulls),
	} {
		lvl := &world.Level{Scenery: []world.Scenery{s}}
		if got := SurfaceUnder(lvl, "", 0, 50, 0, 100); got.Found {
			t.Errorf("%s: found ground at %v", name, got.Height)
		}
	}
	off := tileScenery(t, at, meshcollide.ModeMesh)
	off.ChunkOff = true
	if got := SurfaceUnder(&world.Level{Scenery: []world.Scenery{off}}, "", 0, 50, 0, 100); got.Found {
		t.Errorf("a placement whose chunk is off is ground at %v", got.Height)
	}
	// A column in another frame does not meet world scenery.
	on := tileScenery(t, at, meshcollide.ModeMesh)
	if got := SurfaceUnder(&world.Level{Scenery: []world.Scenery{on}}, "ship", 0, 50, 0, 100); got.Found {
		t.Errorf("a ship-frame column found world scenery at %v", got.Height)
	}
}

func TestSurfaceUnderPicksTheHigherOfBrushAndScenery(t *testing.T) {
	lvl := groundLevel(t) // a slab with its top at y=2 over [-5,5]
	lvl.Scenery = []world.Scenery{tileScenery(t, mgl32.Vec3{0, 10, 0}, meshcollide.ModeMesh)}
	s := SurfaceUnder(lvl, "", 0, 50, 0, 100)
	if want := 10 + slopeHeight(0, 0); !s.Found || math.Abs(float64(s.Height-want)) > 0.08 {
		t.Errorf("ground at %v (found %v), want the tile's %v above the slab", s.Height, s.Found, want)
	}
	// Under the tile's base the column starts below it, and finds the slab.
	if s := SurfaceUnder(lvl, "", 0, 5, 0, 100); !s.Found || math.Abs(float64(s.Height-2)) > 0.01 {
		t.Errorf("from under the tile: ground at %v (found %v), want the slab at 2", s.Height, s.Found)
	}
}
