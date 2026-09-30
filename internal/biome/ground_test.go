package biome

// ground_test.go — where a scatter instance lands.
//
// The interesting cases are all about which surface wins, because a level is
// full of surfaces a blade of grass must not grow on: the underside of a floor,
// the inside of a wall, a decorative brush a body walks through.

import (
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// groundLevel is one chunk holding a flat slab whose top face is at y=2,
// spanning x,z in [-5,5].
func groundLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	ch := world.Chunk{ID: "c0"}
	ch.Brushes = []world.Brush{
		world.NewBoxBrush("slab", mgl32.Vec3{0, 1, 0}, mgl32.Vec3{5, 1, 5}, "concrete"),
	}
	for bi := range ch.Brushes {
		if err := ch.Brushes[bi].Load(); err != nil {
			t.Fatalf("loading: %v", err)
		}
	}
	ch.RecomputeBounds()
	return &world.Level{Chunks: []world.Chunk{ch}}
}

func TestSurfaceUnderFindsTheTopFace(t *testing.T) {
	lvl := groundLevel(t)

	s := SurfaceUnder(lvl, "",0, 10, 0, 20)
	if !s.Found {
		t.Fatal("no ground under the middle of the slab")
	}
	if got := s.Height; got < 1.99 || got > 2.01 {
		t.Errorf("ground at y=%v, want the slab's top face at 2", got)
	}
	if !vecNear(s.Normal, mgl32.Vec3{0, 1, 0}, 1e-4) {
		t.Errorf("normal = %v, want straight up", s.Normal)
	}
}

// Off the edge of every brush there is nothing to stand on, and the caller has
// to be able to tell that from "the ground is at y=0" — a field of grass hanging
// at the world origin under a raised platform is a worse answer than a bare
// patch.
func TestSurfaceUnderReportsNothingOffTheEdge(t *testing.T) {
	lvl := groundLevel(t)

	if s := SurfaceUnder(lvl, "",50, 10, 0, 20); s.Found {
		t.Errorf("found ground at y=%v well outside the slab", s.Height)
	}
}

// A ray that stops short of the floor finds nothing, which is what bounds the
// bake to the volume's own neighbourhood instead of the whole level's depth.
func TestSurfaceUnderRespectsTheDrop(t *testing.T) {
	lvl := groundLevel(t)

	if s := SurfaceUnder(lvl, "",0, 100, 0, 10); s.Found {
		t.Errorf("a 10-unit ray from y=100 reached the slab at y=2 (got %v)", s.Height)
	}
	if s := SurfaceUnder(lvl, "",0, 100, 0, 99); !s.Found {
		t.Error("a 99-unit ray from y=100 did not reach the slab at y=2")
	}
}

// Only faces that could be walked on. A wall's triangles are on the column too —
// every enclosed room has four of them around it — and landing an instance on
// one would grow grass sideways out of it.
func TestSurfaceUnderIgnoresDownwardFaces(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch := world.Chunk{ID: "c0"}
	// A ceiling slab, and nothing under it. Its downward face is the only
	// surface on the column.
	ch.Brushes = []world.Brush{
		world.NewBoxBrush("ceiling", mgl32.Vec3{0, 10, 0}, mgl32.Vec3{5, 1, 5}, "concrete"),
	}
	if err := ch.Brushes[0].Load(); err != nil {
		t.Fatalf("loading: %v", err)
	}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}

	// From below the ceiling: its underside is the only thing the ray could
	// meet, and it must not count.
	if s := SurfaceUnder(lvl, "",0, 8, 0, 20); s.Found {
		t.Errorf("a downward-facing surface was taken as ground (y=%v)", s.Height)
	}
	// From above it, the top face is ground as usual — so the rejection is
	// about the face's facing, not about the brush.
	if s := SurfaceUnder(lvl, "",0, 20, 0, 20); !s.Found || s.Height < 10.99 {
		t.Errorf("the ceiling's own top face was not ground: %+v", s)
	}
}

// A non-solid brush is decorative geometry a body walks through. Grass growing
// on it would be growing on nothing.
func TestSurfaceUnderSkipsNonSolidBrushes(t *testing.T) {
	lvl := groundLevel(t)
	lvl.Chunks[0].Brushes[0].Solid = false

	if s := SurfaceUnder(lvl, "",0, 10, 0, 20); s.Found {
		t.Errorf("found ground on a non-solid brush (y=%v)", s.Height)
	}
}

// A NoDraw face is invisible, and a NoCollide one is walk-through. Neither is
// somewhere to plant anything.
func TestSurfaceUnderSkipsHiddenAndWalkThroughFaces(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(f *world.BrushFace)
	}{
		{"NoDraw", func(f *world.BrushFace) { f.NoDraw = true }},
		{"NoCollide", func(f *world.BrushFace) { f.NoCollide = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lvl := groundLevel(t)
			b := &lvl.Chunks[0].Brushes[0]
			for fi := range b.Faces {
				if b.FaceNormal(&b.Faces[fi]).Y() > 0.5 {
					tc.set(&b.Faces[fi])
				}
			}
			if s := SurfaceUnder(lvl, "",0, 10, 0, 20); s.Found {
				t.Errorf("a %s face was taken as ground (y=%v)", tc.name, s.Height)
			}
		})
	}
}

// The HIGHEST surface below the start, not the first one found — a platform
// over a floor is what an instance stands on.
func TestSurfaceUnderTakesTheHighestSurface(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch := world.Chunk{ID: "c0"}
	ch.Brushes = []world.Brush{
		world.NewBoxBrush("floor", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{5, 1, 5}, "concrete"),
		world.NewBoxBrush("platform", mgl32.Vec3{0, 5, 0}, mgl32.Vec3{2, 1, 2}, "concrete"),
	}
	for bi := range ch.Brushes {
		if err := ch.Brushes[bi].Load(); err != nil {
			t.Fatalf("loading: %v", err)
		}
	}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}

	// Over the platform: its top, at y=6.
	if s := SurfaceUnder(lvl, "",0, 20, 0, 40); !s.Found || s.Height < 5.99 || s.Height > 6.01 {
		t.Errorf("over the platform: got %+v, want the platform top at 6", s)
	}
	// Beside it: the floor's top, at y=1.
	if s := SurfaceUnder(lvl, "",4, 20, 4, 40); !s.Found || s.Height < 0.99 || s.Height > 1.01 {
		t.Errorf("beside the platform: got %+v, want the floor top at 1", s)
	}
}

// Brushes are skipped on their own boxes before any face is tested, and a
// column exactly on the seam between two abutting brushes sits on the edge of
// both boxes — the reject must keep both, or the higher step is lost there.
// A bake query made once must answer the same as the per-column call.
func TestSurfaceUnderOnASeamTestsBothBrushes(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	ch := world.Chunk{ID: "c0"}
	ch.Brushes = []world.Brush{
		world.NewBoxBrush("low", mgl32.Vec3{-2, 0, 0}, mgl32.Vec3{2, 1, 2}, "concrete"),
		world.NewBoxBrush("high", mgl32.Vec3{2, 1, 0}, mgl32.Vec3{2, 2, 2}, "concrete"),
	}
	for bi := range ch.Brushes {
		if err := ch.Brushes[bi].Load(); err != nil {
			t.Fatalf("loading: %v", err)
		}
	}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}

	if s := SurfaceUnder(lvl, "", 0, 20, 0, 40); !s.Found || s.Height < 2.99 || s.Height > 3.01 {
		t.Errorf("on the seam: got %+v, want the high brush's top at 3", s)
	}
	q := NewGroundQuery(lvl, "")
	for _, x := range []float32{-3, -1, 0, 1, 3, 5} {
		if a, b := SurfaceUnder(lvl, "", x, 20, 0, 40), q.SurfaceUnder(x, 20, 0, 40); a != b {
			t.Errorf("x=%v: SurfaceUnder %+v, GroundQuery %+v", x, a, b)
		}
	}
}

// A chunk switched off is not drawn and not solid, so it is not ground either.
func TestSurfaceUnderSkipsDisabledChunks(t *testing.T) {
	lvl := groundLevel(t)
	lvl.Chunks[0].Disabled = true

	if s := SurfaceUnder(lvl, "",0, 10, 0, 20); s.Found {
		t.Errorf("found ground in a disabled chunk (y=%v)", s.Height)
	}
}

// A chunk placed away from the origin is found where it IS, not where its
// authored coordinates would put it.
func TestSurfaceUnderFollowsThePlacement(t *testing.T) {
	lvl := groundLevel(t)
	lvl.Chunks[0].Origin = mgl32.Vec3{100, 20, -50}
	lvl.Chunks[0].Scale = 1

	if s := SurfaceUnder(lvl, "",0, 10, 0, 20); s.Found {
		t.Errorf("found the moved chunk at its authored position (y=%v)", s.Height)
	}
	s := SurfaceUnder(lvl, "",100, 40, -50, 40)
	if !s.Found {
		t.Fatal("did not find the moved chunk where it was placed")
	}
	if s.Height < 21.99 || s.Height > 22.01 {
		t.Errorf("ground at y=%v, want the slab's top carried to 22", s.Height)
	}
}

// An entity's geometry is in the entity's frame. To a world-space volume under
// it, that is a ship hovering overhead rather than ground — taking its floor as
// the highest surface planted grass up on it, well outside the volume. And to a
// volume riding the ship, the ship's floor is found where it sits in the ship's
// own coordinates, wherever the ship currently is in the world.
func TestSurfaceUnderOnlySeesItsOwnFrame(t *testing.T) {
	lvl := groundLevel(t) // world slab, top at y=2
	ship := world.Chunk{ID: "ship.geometry", FrameOwner: "ship"}
	ship.Brushes = []world.Brush{
		world.NewBoxBrush("hold", mgl32.Vec3{0, 5, 0}, mgl32.Vec3{2, 1, 2}, "concrete"),
	}
	if err := ship.Brushes[0].Load(); err != nil {
		t.Fatalf("loading: %v", err)
	}
	ship.RecomputeBounds()
	// Wherever the ship is flying right now; the hold's floor is at y=6 in its
	// own coordinates and y=10 in the world's.
	ship.Xform = mgl32.Translate3D(0, 4, 0)
	lvl.Chunks = append(lvl.Chunks, ship)

	if s := SurfaceUnder(lvl, "", 0, 20, 0, 40); !s.Found || s.Height < 1.99 || s.Height > 2.01 {
		t.Errorf("world volume under the ship: got %+v, want the world slab at 2", s)
	}
	if s := SurfaceUnder(lvl, "world", 0, 20, 0, 40); !s.Found || s.Height < 1.99 || s.Height > 2.01 {
		t.Errorf(`the reserved "world" frame name: got %+v, want the world slab at 2`, s)
	}
	if s := SurfaceUnder(lvl, "ship", 0, 20, 0, 40); !s.Found || s.Height < 5.99 || s.Height > 6.01 {
		t.Errorf("volume riding the ship: got %+v, want the hold floor at its own y=6", s)
	}
	if s := SurfaceUnder(lvl, "ship", 4, 20, 4, 40); s.Found {
		t.Errorf("volume riding the ship found world ground beside the hold (y=%v)", s.Height)
	}
}

// ---------- the slope gate ----------

func TestSlopeCoverageGatesOnAngle(t *testing.T) {
	s := world.DefaultVolumeScatter()
	s.MinSlopeAngle = 0
	s.MaxSlopeAngle = 40

	if got := SlopeCoverage(&s, mgl32.Vec3{0, 1, 0}); got < 0.99 {
		t.Errorf("flat ground has coverage %v, want ~1", got)
	}
	// Straight up a wall is 90 degrees, well past the gate.
	if got := SlopeCoverage(&s, mgl32.Vec3{1, 0, 0}); got > 0.01 {
		t.Errorf("a vertical face has coverage %v, want ~0", got)
	}
	// Feathered rather than a hard cut: the limit itself sits mid-ramp.
	at45 := SlopeCoverage(&s, mgl32.Vec3{1, 1, 0})
	if at45 > 0.5 {
		t.Errorf("a 45-degree slope past a 40-degree limit has coverage %v", at45)
	}
}

// A cross-product normal's length scales with the triangle's area, so reading
// .Y off one without normalising pins every surface at 0 degrees and the gate
// silently believes the whole level is flat.
func TestSlopeCoverageNormalisesItsInput(t *testing.T) {
	s := world.DefaultVolumeScatter()
	s.MaxSlopeAngle = 40

	unit := SlopeCoverage(&s, mgl32.Vec3{1, 0, 0})
	scaled := SlopeCoverage(&s, mgl32.Vec3{256, 0, 0})
	if unit != scaled {
		t.Errorf("a scaled normal gave %v where the unit one gave %v", scaled, unit)
	}
}

// A degenerate normal has no basis to exclude anything on, so it admits rather
// than rejecting — a bare patch from a maths edge case is worse than a blade in
// a slightly wrong place.
func TestSlopeCoverageAdmitsADegenerateNormal(t *testing.T) {
	s := world.DefaultVolumeScatter()
	if got := SlopeCoverage(&s, mgl32.Vec3{}); got != 1 {
		t.Errorf("a zero normal gave coverage %v, want 1", got)
	}
}

func vecNear(a, b mgl32.Vec3, eps float32) bool {
	for i := 0; i < 3; i++ {
		d := a[i] - b[i]
		if d < -eps || d > eps {
			return false
		}
	}
	return true
}

func TestTerrainIsGroundForScatter(t *testing.T) {
	for _, smooth := range []bool{false, true} {
		tr := world.NewTerrain(mgl32.Vec3{-8, -4, -8}, 1, 16, 12, 16)
		tr.Generate(world.TerrainFill{Height: 5, Hills: 2, HillSize: 6, Top: 1, Under: 1, Deep: 1})
		// A cave under part of it: the column must find the ground on top,
		// and where the roof is mined away, the cave floor.
		for x := 2; x < 12; x++ {
			for z := 2; z < 12; z++ {
				for y := 1; y < 3; y++ {
					tr.SetBlock(x, y, z, 0)
				}
			}
		}
		tr.Smooth = smooth
		tr.Touch()
		ch := world.Chunk{ID: "g", Origin: mgl32.Vec3{0, 2, 0}, Rotation: 0.3, Scale: 2, Terrain: tr}
		ch.RecomputeBounds()
		lvl := &world.Level{Chunks: []world.Chunk{ch}}
		c := &lvl.Chunks[0]
		for _, p := range [][2]float32{{1, 1}, {-3, 4}, {5, -2}} {
			s := SurfaceUnder(lvl, "", p[0], 50, p[1], 100)
			inv := c.LocalPlacementMatrix().Inv()
			l := inv.Mul4x1(mgl32.Vec4{p[0], 50, p[1], 1}).Vec3()
			h, _, ok := tr.GroundBelow(l, 1000)
			if !ok {
				t.Fatalf("test point %v is off the terrain", p)
			}
			want := c.LocalPlacementMatrix().Mul4x1(mgl32.Vec4{l.X(), h, l.Z(), 1}).Y()
			if !s.Found || s.Height < want-1e-3 || s.Height > want+1e-3 {
				t.Fatalf("smooth=%v at %v: surface %+v, terrain at %v", smooth, p, s, want)
			}
		}
	}
}
