package collision

// scenery_mesh_test.go — a cooked mesh's own collision: the hulls and mesh
// modes (scenery_mesh.go), the placement broadphase (scenery_index.go), and
// the compound shape. They mirror the terrain, step-up, slide and raycast
// tests, because the claim is that a cooked rock is solid on exactly the
// terms a brush or a hillside is.

import (
	"math"
	"math/rand"
	"testing"

	"foundry/internal/meshcollide"
	"foundry/internal/nanite"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// ---------- fixtures ----------

// boxTris is a closed box, wound outward.
func boxTris(lo, hi mgl32.Vec3) ([]mgl32.Vec3, []uint32) {
	var pos []mgl32.Vec3
	for i := 0; i < 8; i++ {
		p := lo
		if i&1 != 0 {
			p[0] = hi[0]
		}
		if i&2 != 0 {
			p[1] = hi[1]
		}
		if i&4 != 0 {
			p[2] = hi[2]
		}
		pos = append(pos, p)
	}
	var idx []uint32
	for _, q := range [][4]uint32{{0, 4, 6, 2}, {1, 3, 7, 5}, {0, 1, 5, 4}, {2, 6, 7, 3}, {0, 2, 3, 1}, {4, 5, 7, 6}} {
		idx = append(idx, q[0], q[1], q[2], q[0], q[2], q[3])
	}
	return pos, idx
}

// sheetTris is an n×n grid of quads over [-half, half] in XZ, at height h,
// wound to face up.
func sheetTris(half float32, n int, h func(x, z float32) float32) ([]mgl32.Vec3, []uint32) {
	var pos []mgl32.Vec3
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			x := -half + 2*half*float32(i)/float32(n)
			z := -half + 2*half*float32(j)/float32(n)
			pos = append(pos, mgl32.Vec3{x, h(x, z), z})
		}
	}
	var idx []uint32
	w := uint32(n + 1)
	for j := uint32(0); j < uint32(n); j++ {
		for i := uint32(0); i < uint32(n); i++ {
			a := j*w + i
			idx = append(idx, a, a+w, a+1, a+1, a+w, a+w+1)
		}
	}
	return pos, idx
}

// bumpyRock is a closed, lumpy sphere of radius about r.
func bumpyRock(r float32, seed int64) ([]mgl32.Vec3, []uint32) {
	rng := rand.New(rand.NewSource(seed))
	// An octahedron subdivided three times, pushed out to the sphere.
	pos := []mgl32.Vec3{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}}
	idx := []uint32{0, 2, 4, 4, 2, 1, 1, 2, 5, 5, 2, 0, 4, 3, 0, 1, 3, 4, 5, 3, 1, 0, 3, 5}
	for l := 0; l < 3; l++ {
		mid := map[[2]uint32]uint32{}
		m := func(a, b uint32) uint32 {
			k := [2]uint32{min(a, b), max(a, b)}
			if v, ok := mid[k]; ok {
				return v
			}
			pos = append(pos, pos[a].Add(pos[b]).Normalize())
			mid[k] = uint32(len(pos) - 1)
			return mid[k]
		}
		var next []uint32
		for i := 0; i < len(idx); i += 3 {
			a, b, c := idx[i], idx[i+1], idx[i+2]
			ab, bc, ca := m(a, b), m(b, c), m(c, a)
			next = append(next, a, ab, ca, b, bc, ab, c, ca, bc, ab, bc, ca)
		}
		idx = next
	}
	for i := range pos {
		pos[i] = pos[i].Normalize().Mul(r * (1 + 0.15*float32(rng.Float64()-0.5)))
	}
	return pos, idx
}

func flatten(pos []mgl32.Vec3) []float32 {
	out := make([]float32, 0, 3*len(pos))
	for _, p := range pos {
		out = append(out, p[0], p[1], p[2])
	}
	return out
}

// cookedData cooks a mesh's collision, once per mesh: placements of one
// mesh share it, as placements of one .fnm do.
var cookedData = map[*mgl32.Vec3]*meshcollide.Data{}

// cooked is a placement of a mesh with cooked collision and nothing to draw:
// what world.Scenery.Load makes of a .fnm, minus the GPU half.
func cooked(t testing.TB, id string, pos, rot, scale mgl32.Vec3, mesh []mgl32.Vec3, idx []uint32, mode meshcollide.Mode) world.Scenery {
	t.Helper()
	// Pinned on: FOUNDRY_NANITE_COLLISION=0 in the environment would make
	// this a test of the box path.
	prevNC := world.NaniteCollision
	world.NaniteCollision = true
	t.Cleanup(func() { world.NaniteCollision = prevNC })
	d := cookedData[&mesh[0]]
	if d == nil {
		var err error
		d, err = meshcollide.Build(meshcollide.Input{Positions: flatten(mesh), Stride: 3, Indices: idx, TagNames: []string{"stone"}}, meshcollide.Settings{})
		if err != nil {
			t.Fatal(err)
		}
		cookedData[&mesh[0]] = d
	}
	lo, hi := mesh[0], mesh[0]
	for _, p := range mesh {
		lo = mgl32.Vec3{min(lo[0], p[0]), min(lo[1], p[1]), min(lo[2], p[2])}
		hi = mgl32.Vec3{max(hi[0], p[0]), max(hi[1], p[1]), max(hi[2], p[2])}
	}
	s := world.NewScenery(id, "models/"+id+".fnm", pos)
	s.Rotation, s.Scale = rot, scale
	s.Nanite = &nanite.Mesh{Lo: lo, Hi: hi, Collision: d}
	s.LocalMin, s.LocalMax = lo, hi
	s.Collision = mode
	return s
}

func one() mgl32.Vec3 { return mgl32.Vec3{1, 1, 1} }

// meshLevel is a level of scenery only, no chunks.
func meshLevel(sc ...world.Scenery) *world.Level {
	InvalidateScenery()
	lvl := &world.Level{Scenery: sc}
	lvl.Player.StepHeight = stepUpAllowance
	if err := world.RebuildIndexes(lvl); err != nil {
		panic(err)
	}
	return lvl
}

// groundUnder is the scenery's surface straight below (x, z), from above.
func groundUnder(lvl *world.Level, x, z float32) float32 {
	_, t, _, ok := sceneryRayNearest(lvl, mgl32.Vec3{x, 100, z}, mgl32.Vec3{0, -1, 0}, 500, nil, false)
	if !ok {
		return float32(math.Inf(-1))
	}
	return 100 - t
}

func hills(x, z float32) float32 {
	return 0.6*float32(math.Sin(float64(x)*0.35)) + 0.4*float32(math.Cos(float64(z)*0.3))
}

// ---------- the mesh mode: a sheet, like terrain ----------

func TestAPlayerStandsOnACookedSheet(t *testing.T) {
	pos, idx := sheetTris(15, 40, hills)
	lvl := meshLevel(cooked(t, "ground", mgl32.Vec3{}, mgl32.Vec3{}, one(), pos, idx, meshcollide.ModeAuto))
	if m := lvl.Scenery[0].CollisionMode(); m != meshcollide.ModeMesh {
		t.Fatalf("a 30 m sheet resolves to %v, want mesh", m)
	}
	p := world.NewPlayer(mgl32.Vec3{2, 4, 3})
	var res PlayerMoveResult
	for i := 0; i < 120; i++ {
		res = MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	g := groundUnder(lvl, 2, 3)
	if f := feet(p); f < g-0.02 || f > g+0.08 {
		t.Errorf("feet at %v, ground at %v", f, g)
	}
	if !res.OnGround {
		t.Error("standing on a cooked sheet is not on the ground")
	}
}

func TestWalkingOverACookedHillDoesNotBounceOrSink(t *testing.T) {
	pos, idx := sheetTris(15, 40, hills)
	// Turned and moved, so the probes really do run in the mesh's own space.
	lvl := meshLevel(cooked(t, "ground", mgl32.Vec3{1, -0.5, 2}, mgl32.Vec3{0, 0.7, 0}, one(), pos, idx, meshcollide.ModeMesh))
	p := world.NewPlayer(mgl32.Vec3{-8, 3, 2})
	for i := 0; i < 60; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	for i := 0; i < 220; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{0.07, 0, 0}, false))
		g := groundUnder(lvl, p.Position.X(), p.Position.Z())
		if f := feet(p); f < g-0.05 || f > g+0.25 {
			t.Fatalf("frame %d at x=%.2f: feet %v, ground %v", i, p.Position.X(), f, g)
		}
	}
	if p.Position.X() < 4 {
		t.Errorf("the walk stalled at x=%v", p.Position.X())
	}
}

// A cooked block too big for hulls is a cliff: its side is a wall, not a
// step, and its top is floor.
func TestACookedCliffIsAWallAndItsTopIsFloor(t *testing.T) {
	pos, idx := boxTris(mgl32.Vec3{0, 0, -5}, mgl32.Vec3{8, 3, 5})
	lvl := stepRoom(6)
	lvl.Scenery = []world.Scenery{cooked(t, "cliff", mgl32.Vec3{2, 0, 0}, mgl32.Vec3{}, one(), pos, idx, meshcollide.ModeAuto)}
	InvalidateScenery()
	if m := lvl.Scenery[0].CollisionMode(); m != meshcollide.ModeMesh {
		t.Fatalf("an 8 m block resolves to %v, want mesh", m)
	}
	p := walkAt(lvl, 120)
	if x := p.Position.X() + p.BoundHalfX; x > 2.02 {
		t.Errorf("walked into the cliff: front at x=%v, face at 2", x)
	}
	if f := feet(p); f > 0.05 {
		t.Errorf("climbed the cliff: feet at %v", f)
	}
	// Dropped onto the top, it stands there.
	q := world.NewPlayer(mgl32.Vec3{5, 5, 0})
	var res PlayerMoveResult
	for i := 0; i < 90; i++ {
		res = MovePlayer(q, lvl, stepMove(mgl32.Vec3{}, false))
	}
	if f := feet(q); f < 2.98 || f > 3.06 || !res.OnGround {
		t.Errorf("on the cliff's top: feet %v, on ground %v", f, res.OnGround)
	}
}

// ---------- step-up, in both modes ----------

func TestAPlayerStepsOntoACookedCurb(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode meshcollide.Mode
	}{{"hulls", meshcollide.ModeHulls}, {"mesh", meshcollide.ModeMesh}} {
		t.Run(tc.name, func(t *testing.T) {
			pos, idx := boxTris(mgl32.Vec3{2, 0, -2}, mgl32.Vec3{4, 0.4, 2})
			lvl := stepRoom(4)
			lvl.Scenery = []world.Scenery{cooked(t, "curb", mgl32.Vec3{}, mgl32.Vec3{}, one(), pos, idx, tc.mode)}
			InvalidateScenery()
			p := walkAt(lvl, 62)
			if got := feet(p); got < 0.39 || got > 0.46 {
				t.Errorf("feet rested at y=%v, want standing on the 0.4 curb", got)
			}
			if p.Position.X() < 2 {
				t.Errorf("never got onto the curb: x=%v", p.Position.X())
			}
		})
	}
}

func TestACookedWallTallerThanTheStepStopsThePlayer(t *testing.T) {
	for _, mode := range []meshcollide.Mode{meshcollide.ModeHulls, meshcollide.ModeMesh} {
		pos, idx := boxTris(mgl32.Vec3{2, 0, -2}, mgl32.Vec3{4, 2.5, 2})
		lvl := stepRoom(4)
		lvl.Player.StepHeight = 0.5
		lvl.Scenery = []world.Scenery{cooked(t, "wall", mgl32.Vec3{}, mgl32.Vec3{}, one(), pos, idx, mode)}
		InvalidateScenery()
		p := walkAt(lvl, 120)
		if f := feet(p); f > 0.05 || p.Position.X()+p.BoundHalfX > 2.02 {
			t.Errorf("%v: got onto or into a 2.5 m wall: pos %v", mode, p.Position)
		}
	}
}

// ---------- sliding along a hull ----------

// A body pushed into a wall at an angle keeps the part of its motion along
// the wall: a hull at a slant is slid along, not stuck to.
func TestAPlayerSlidesAlongASlantedHullWall(t *testing.T) {
	pos, idx := boxTris(mgl32.Vec3{-0.25, 0, -3}, mgl32.Vec3{0.25, 2, 3})
	lvl := stepRoom(4)
	lvl.Player.StepHeight = 0.3
	// A wall through x=1, turned 30 degrees about Y.
	yaw := float32(math.Pi / 6)
	lvl.Scenery = []world.Scenery{cooked(t, "wall", mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, yaw, 0}, one(), pos, idx, meshcollide.ModeHulls)}
	InvalidateScenery()
	p := world.NewPlayer(mgl32.Vec3{-1, 1, 0})
	for i := 0; i < 30; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	start := p.Position
	for i := 0; i < 90; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{0.06, 0, 0}, false))
	}
	// The wall's normal in XZ, and its run.
	n := mgl32.Vec3{float32(math.Cos(float64(yaw))), 0, -float32(math.Sin(float64(yaw)))}
	along := mgl32.Vec3{n.Z(), 0, -n.X()}
	moved := p.Position.Sub(start)
	if d := absf(moved.Dot(along)); d < 0.5 {
		t.Errorf("slid only %v along the wall (moved %v)", d, moved)
	}
	// Never inside: the body's centre stays a radius off the wall's face.
	face := mgl32.Vec3{1, 0, 0}.Sub(n.Mul(0.25))
	if gap := face.Sub(p.Position).Dot(n); gap < p.BoundHalfX-0.03 {
		t.Errorf("the body is %v from the wall's face, radius %v", gap, p.BoundHalfX)
	}
}

// ---------- entities ----------

// An entity with gravity falls onto a hulls-mode rock and rests on it,
// through resolveEntityWorld's chain.
func TestAnEntityFallsOntoACookedRock(t *testing.T) {
	pos, idx := bumpyRock(1.5, 2)
	lvl := meshLevel(cooked(t, "rock", mgl32.Vec3{0, 1.5, 0}, mgl32.Vec3{}, one(), pos, idx, meshcollide.ModeAuto))
	if m := lvl.Scenery[0].CollisionMode(); m != meshcollide.ModeHulls {
		t.Fatalf("a 3 m rock resolves to %v, want hulls", m)
	}
	e := walkerAt("crate", mgl32.Vec3{0.1, 6, 0.1})
	e.Collision.Gravity = true
	lvl.Entities = []world.Entity{e}
	for i := 0; i < 180; i++ {
		EntityUpdate(lvl, 1.0/60, true, nil)
	}
	got := lvl.Entities[0].Position.Y() - 0.9
	top := groundUnder(lvl, 0.1, 0.1)
	if got < top-0.2 || got > top+0.15 {
		t.Errorf("the crate's feet are at %v, the rock's top under it at %v", got, top)
	}
	if !lvl.Entities[0].Collision.OnGround {
		t.Error("the crate is not grounded on the rock")
	}
}

// A cooked placement can still ask for its box, and then it is the proxy
// box ResolveSceneryCollisions pushes apart from, not part of the chain.
func TestACookedPlacementInBoxModeIsTheOldBox(t *testing.T) {
	pos, idx := boxTris(mgl32.Vec3{-1, 0, -1}, mgl32.Vec3{1, 2, 1})
	lvl := meshLevel(cooked(t, "rock", mgl32.Vec3{}, mgl32.Vec3{}, one(), pos, idx, meshcollide.ModeBox))
	lvl.Entities = []world.Entity{walkerAt("guard", mgl32.Vec3{1.15, 1, 0})}
	if moved := ResolveSceneryCollisions(lvl, nil); len(moved) != 1 {
		t.Fatalf("box mode moved %v", moved)
	}
	if x := lvl.Entities[0].Position.X(); math.Abs(float64(x-1.3)) > 1e-4 {
		t.Errorf("pushed to x=%v, want 1.3", x)
	}
	// The same rock in hulls mode is not ResolveSceneryCollisions' business.
	lvl.Scenery[0].Collision = meshcollide.ModeHulls
	lvl.Entities[0].Position = mgl32.Vec3{1.15, 1, 0}
	InvalidateScenery()
	if moved := ResolveSceneryCollisions(lvl, nil); len(moved) != 0 {
		t.Errorf("hulls mode was pushed apart as a box: %v", moved)
	}
	out, _, _ := pushOutOfScenery(lvl, mgl32.Vec3{1.15, 1, 0}, 0.3, 0.9)
	if out.X() < 1.29 {
		t.Errorf("the chain left the body at %v", out)
	}
	// And none is nothing.
	lvl.Scenery[0].Collision = meshcollide.ModeNone
	InvalidateScenery()
	if out, _, _ := pushOutOfScenery(lvl, mgl32.Vec3{1.15, 1, 0}, 0.3, 0.9); out != (mgl32.Vec3{1.15, 1, 0}) {
		t.Errorf("none moved the body to %v", out)
	}
}

// ---------- rays ----------

// fieldOfRocks is placements of three shapes in every mode, turned and
// scaled at random.
func fieldOfRocks(t testing.TB, n int, seed int64) *world.Level {
	rng := rand.New(rand.NewSource(seed))
	rock, rockIdx := bumpyRock(1, 5)
	slab, slabIdx := boxTris(mgl32.Vec3{-1, 0, -0.3}, mgl32.Vec3{1, 1.5, 0.3})
	hill, hillIdx := sheetTris(3, 8, hills)
	var sc []world.Scenery
	for i := 0; i < n; i++ {
		pos := mgl32.Vec3{rng.Float32()*40 - 20, rng.Float32()*4 - 2, rng.Float32()*40 - 20}
		rot := mgl32.Vec3{rng.Float32() * 0.6, rng.Float32() * 6.3, rng.Float32() * 0.6}
		scale := mgl32.Vec3{0.5 + rng.Float32()*1.5, 0.5 + rng.Float32()*1.5, 0.5 + rng.Float32()*1.5}
		mode := []meshcollide.Mode{meshcollide.ModeHulls, meshcollide.ModeMesh, meshcollide.ModeBox}[i%3]
		var s world.Scenery
		switch i % 4 {
		case 0, 1:
			s = cooked(t, "rock", pos, rot, scale, rock, rockIdx, mode)
		case 2:
			s = cooked(t, "slab", pos, rot, scale, slab, slabIdx, mode)
		default:
			s = cooked(t, "hill", pos, rot, scale, hill, hillIdx, meshcollide.ModeMesh)
		}
		s.ID = s.ID + string(rune('a'+i%26)) + string(rune('a'+i/26))
		sc = append(sc, s)
	}
	return meshLevel(sc...)
}

// worldTris is every triangle a placement's collision presents, in world
// space: its mesh, its hulls' faces, or its box's.
func worldTris(lvl *world.Level, e *sceneryEntry) [][3]mgl32.Vec3 {
	tw := func(m mgl32.Mat4, p mgl32.Vec3) mgl32.Vec3 {
		r := m.Mul4x1(p.Vec4(1))
		return mgl32.Vec3{r.X(), r.Y(), r.Z()}
	}
	var out [][3]mgl32.Vec3
	switch e.mode {
	case meshcollide.ModeMesh:
		r := meshcollide.NewResident(e.data)
		for t := 0; t < e.data.TriangleCount(); t++ {
			a, b, c := r.Tri(t)
			out = append(out, [3]mgl32.Vec3{tw(e.m, a), tw(e.m, b), tw(e.m, c)})
		}
	case meshcollide.ModeHulls:
		for _, h := range e.data.Hulls {
			for k := 0; k+2 < len(h.Tris); k += 3 {
				out = append(out, [3]mgl32.Vec3{tw(e.m, h.Pts[h.Tris[k]]), tw(e.m, h.Pts[h.Tris[k+1]]), tw(e.m, h.Pts[h.Tris[k+2]])})
			}
		}
	case meshcollide.ModeBox:
		p := &e.proxy
		m := mgl32.Translate3D(p.Position.X(), p.Position.Y(), p.Position.Z()).Mul4(world.EulerMatrixYXZ(p.Rotation))
		bp, bi := boxTris(p.BoundHalfExtents().Mul(-1), p.BoundHalfExtents())
		for k := 0; k < len(bi); k += 3 {
			out = append(out, [3]mgl32.Vec3{tw(m, bp[bi[k]]), tw(m, bp[bi[k+1]]), tw(m, bp[bi[k+2]])})
		}
	}
	return out
}

// TestSceneryRaysMatchBruteForce is the raycast oracle for cooked scenery:
// what a cast stops at — through the placement broadphase, each placement
// in its own mode, the mesh through its tree in mesh space, a hull by its
// planes — is the nearest of every collision triangle of every placement,
// carried into the world and tested one by one.
func TestSceneryRaysMatchBruteForce(t *testing.T) {
	lvl := fieldOfRocks(t, 60, 9)
	ix := sceneryIndexFor(lvl)
	var tris [][3]mgl32.Vec3
	for i := range ix.entries {
		tris = append(tris, worldTris(lvl, &ix.entries[i])...)
	}
	rng := rand.New(rand.NewSource(4))
	hits := 0
	for i := 0; i < 1500; i++ {
		// From outside the field, aimed into it: a ray that starts inside a
		// solid stops at 0 by design, which the triangles cannot say.
		dir := mgl32.Vec3{float32(rng.NormFloat64()), float32(rng.NormFloat64()) * 0.4, float32(rng.NormFloat64())}.Normalize()
		target := mgl32.Vec3{rng.Float32()*40 - 20, rng.Float32()*4 - 2, rng.Float32()*40 - 20}
		origin := target.Sub(dir.Mul(60))
		want, found := float32(200), false
		for _, tr := range tris {
			if th, ok := rayTriangle(origin, dir, tr[0], tr[1], tr[2]); ok && th < want {
				want, found = th, true
			}
		}
		res := Raycast(lvl, origin, dir, RayFilter{Range: 200, Geometry: true})
		got := res.Stop.Sub(origin).Len()
		if res.Blocked != found || (found && absf(got-want) > 2e-3*max(1, want)) {
			t.Fatalf("ray %d: cast stopped %v (blocked %v), brute force %v (%v)", i, got, res.Blocked, want, found)
		}
		if LineBlocked(lvl, origin, origin.Add(dir.Mul(200)), nil) != found {
			t.Fatalf("ray %d: LineBlocked disagrees with the oracle", i)
		}
		if found {
			hits++
		}
	}
	if hits < 300 {
		t.Errorf("only %d of 1500 rays hit: the oracle tests too little", hits)
	}
}

// The broadphase finds exactly the placements a brute-force box test does.
func TestThePlacementBroadphaseMatchesBruteForce(t *testing.T) {
	lvl := fieldOfRocks(t, 300, 3)
	ix := sceneryIndexFor(lvl)
	rng := rand.New(rand.NewSource(8))
	for i := 0; i < 300; i++ {
		c := mgl32.Vec3{rng.Float32()*44 - 22, rng.Float32()*6 - 3, rng.Float32()*44 - 22}
		h := mgl32.Vec3{rng.Float32() * 3, rng.Float32() * 3, rng.Float32() * 3}
		got := map[int]bool{}
		ix.overlapping(c.Sub(h), c.Add(h), func(e *sceneryEntry) bool { got[e.idx] = true; return true })
		for k := range ix.entries {
			e := &ix.entries[k]
			if boxTouches(e.box, c.Sub(h), c.Add(h)) != got[e.idx] {
				t.Fatalf("box %d: placement %d disagrees", i, e.idx)
			}
		}
	}
}

// A level of many props trusts its index for the rest of a tick, sees an
// edit at the next one, and InvalidateScenery sees it at once; a level of a
// few checks every query.
func TestThePlacementIndexSeesEdits(t *testing.T) {
	lvl := fieldOfRocks(t, 100, 6)
	Epoch()
	a := sceneryIndexFor(lvl)
	lvl.Scenery[0].Position = lvl.Scenery[0].Position.Add(mgl32.Vec3{0, 50, 0})
	if b := sceneryIndexFor(lvl); b != a {
		t.Error("a big level's index was checked twice in one tick")
	}
	Epoch()
	if b := sceneryIndexFor(lvl); b == a {
		t.Error("the next tick did not see the edit")
	}
	c := sceneryIndexFor(lvl)
	lvl.Scenery[1].Solid = false
	InvalidateScenery()
	if d := sceneryIndexFor(lvl); d == c {
		t.Error("InvalidateScenery did not rebuild")
	}

	small := fieldOfRocks(t, 10, 6)
	e := sceneryIndexFor(small)
	small.Scenery[0].Position = small.Scenery[0].Position.Add(mgl32.Vec3{0, 50, 0})
	if f := sceneryIndexFor(small); f == e {
		t.Error("a small level's edit was not seen in the same tick")
	}
}

// ---------- particles ----------

func TestAParticleLandsOnACookedRock(t *testing.T) {
	for _, mode := range []meshcollide.Mode{meshcollide.ModeBox, meshcollide.ModeHulls, meshcollide.ModeMesh} {
		pos, idx := boxTris(mgl32.Vec3{-1, 0, -1}, mgl32.Vec3{1, 2, 1})
		lvl := meshLevel(cooked(t, "rock", mgl32.Vec3{3, 0, 0}, mgl32.Vec3{}, one(), pos, idx, mode))
		p, n, hit := ParticleCollide(lvl, mgl32.Vec3{3.2, 2.3, 0.1}, mgl32.Vec3{3.2, 1.9, 0.1}, 0.05)
		if !hit || absf(p.Y()-2.05) > 1e-3 || n.Y() < 0.99 {
			t.Errorf("%v: landed %v at %v, normal %v", mode, hit, p, n)
		}
		if _, _, hit := ParticleCollide(lvl, mgl32.Vec3{6, 2.3, 0}, mgl32.Vec3{6, 1.9, 0}, 0.05); hit {
			t.Errorf("%v: a particle beside the rock landed on it", mode)
		}
	}
}

// ---------- residency ----------

// A mesh is decoded when a query reaches it and let go after residentTTL
// ticks with none.
func TestMeshCollisionIsResidentByProximity(t *testing.T) {
	pos, idx := sheetTris(15, 10, hills)
	lvl := meshLevel(cooked(t, "ground", mgl32.Vec3{}, mgl32.Vec3{}, one(), pos, idx, meshcollide.ModeMesh))
	d := lvl.Scenery[0].CollisionData()
	resident := func() bool {
		residents.Lock()
		defer residents.Unlock()
		_, ok := residents.m[d]
		return ok
	}
	// A body far from it decodes nothing.
	pushOutOfScenery(lvl, mgl32.Vec3{100, 0, 100}, 0.3, 0.9)
	if resident() {
		t.Fatal("a body 100 m off decoded the mesh")
	}
	pushOutOfScenery(lvl, mgl32.Vec3{0, 0.5, 0}, 0.3, 0.9)
	if !resident() {
		t.Fatal("a body on the mesh did not decode it")
	}
	for i := 0; i < residentTTL+2; i++ {
		residentTick()
	}
	if resident() {
		t.Fatal("the mesh stayed resident with no body near")
	}
	if s := TakeSceneryStats(); s.Mesh != 1 || s.Decodes == 0 || s.Evictions == 0 {
		t.Errorf("stats %+v", s)
	}
}

// ---------- GJK against hulls ----------

// A ConvexHull placed by an affine matrix supports its farthest transformed
// vertex, so GJK/EPA, the sweep and the compound all see the placed solid.
func TestHullShapesUnderAPlacement(t *testing.T) {
	cube, _ := boxTris(mgl32.Vec3{-1, -1, -1}, mgl32.Vec3{1, 1, 1})
	// Stretched to 4 long in X, then turned a quarter about Y: 4 long in Z.
	m := mgl32.HomogRotate3DY(math.Pi / 2).Mul4(mgl32.Scale3D(2, 1, 1))
	hull := ConvexHull{Pts: cube, Xform: m}
	lo, hi := ShapeAABB(hull)
	if !vecNear(lo, mgl32.Vec3{-1, -1, -2}, 1e-5) || !vecNear(hi, mgl32.Vec3{1, 1, 2}, 1e-5) {
		t.Fatalf("placed hull spans %v..%v", lo, hi)
	}
	// A unit box overlapping its +X face by 0.25: pushed back along +X.
	box := Box{C: mgl32.Vec3{1.75, 0, 0}, Half: mgl32.Vec3{1, 1, 1}}
	n, depth, ok := Penetration(box, hull)
	if !ok || absf(depth-0.25) > 1e-3 || !vecNear(n, mgl32.Vec3{-1, 0, 0}, 1e-3) {
		t.Errorf("penetration %v %v %v, want 0.25 along -X", n, depth, ok)
	}
	// Overlapping its +Z end by 0.5 (the stretched axis).
	box.C = mgl32.Vec3{0, 0, 2.5}
	n, depth, ok = Penetration(box, hull)
	if !ok || absf(depth-0.5) > 1e-3 || !vecNear(n, mgl32.Vec3{0, 0, -1}, 1e-3) {
		t.Errorf("penetration %v %v %v, want 0.5 along -Z", n, depth, ok)
	}
	// Clear of it: no penetration.
	box.C = mgl32.Vec3{2.2, 0, 0}
	if Overlap(box, hull) {
		t.Error("a box clear of the hull overlaps it")
	}
	// A sphere swept at it along -X touches at x = 1 + radius.
	ball := Ellipsoid{C: mgl32.Vec3{5, 0, 0}, Half: mgl32.Vec3{0.5, 0.5, 0.5}, Rot: mgl32.Ident4()}
	tt, hit := SweepHit(ball, hull, mgl32.Vec3{-8, 0, 0})
	if !hit || absf(5-8*tt-1.5) > 2e-3 {
		t.Errorf("sweep hit %v at t=%v (x=%v), want x=1.5", hit, tt, 5-8*tt)
	}

	// Two hulls as a compound: the sweep meets the nearer, penetration is
	// the deeper part's, and the gap between them is not solid.
	far := ConvexHull{Pts: cube, Xform: mgl32.Translate3D(-6, 0, 0)}
	c := Compound{Parts: []Shape{hull, far}}
	tt, hit = SweepHitCompound(Ellipsoid{C: mgl32.Vec3{-3, 0, 0}, Half: mgl32.Vec3{0.5, 0.5, 0.5}, Rot: mgl32.Ident4()}, c, mgl32.Vec3{-8, 0, 0})
	if !hit || absf(-3-8*tt-(-4.5)) > 2e-3 {
		t.Errorf("compound sweep to x=%v, want the far hull's face at -4.5", -3-8*tt)
	}
	if c.Overlap(Box{C: mgl32.Vec3{-3, 0, 0}, Half: mgl32.Vec3{0.4, 0.4, 0.4}}) {
		t.Error("the gap between two parts is solid")
	}
	if _, d, ok := c.Penetration(Box{C: mgl32.Vec3{-6.6, 0, 0}, Half: mgl32.Vec3{0.5, 0.5, 0.5}}); !ok || absf(d-0.9) > 1e-3 {
		t.Errorf("compound penetration %v %v, want the far part's 0.9", d, ok)
	}
	if lo, hi, ok := c.AABB(); !ok || !vecNear(lo, mgl32.Vec3{-7, -1, -2}, 1e-5) || !vecNear(hi, mgl32.Vec3{1, 1, 2}, 1e-5) {
		t.Errorf("compound box %v..%v", lo, hi)
	}
}

func BenchmarkPushOutOfSceneryField(b *testing.B) {
	lvl := fieldOfRocks(b, 600, 1)
	rng := rand.New(rand.NewSource(1))
	pts := make([]mgl32.Vec3, 256)
	for i := range pts {
		pts[i] = mgl32.Vec3{rng.Float32()*40 - 20, rng.Float32()*4 - 2, rng.Float32()*40 - 20}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pushOutOfScenery(lvl, pts[i%len(pts)], 0.3, 0.9)
	}
}

// A player walking over a cooked hillside in the mesh mode, a frame at a
// time: what the probes cost a body on the ground, as against a body buried
// in a field of props (BenchmarkPushOutOfSceneryField).
func BenchmarkMovePlayerOnACookedHill(b *testing.B) {
	pos, idx := sheetTris(30, 120, hills) // 28,800 triangles
	lvl := meshLevel(cooked(b, "ground", mgl32.Vec3{}, mgl32.Vec3{}, one(), pos, idx, meshcollide.ModeMesh))
	p := world.NewPlayer(mgl32.Vec3{-25, 3, 0})
	for i := 0; i < 60; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := float32(0.07)
		if (i/600)%2 == 1 {
			dir = -dir
		}
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{dir, 0, 0}, false))
	}
}

// FOUNDRY_NANITE_COLLISION=0 (world.NaniteCollision) puts a cooked prop back
// on its box: the chain leaves it alone and the push-apart pass takes it.
func TestTheNaniteCollisionSwitchFallsBackToTheBox(t *testing.T) {
	prev := world.NaniteCollision
	t.Cleanup(func() { world.NaniteCollision = prev; InvalidateScenery() })
	pos, idx := boxTris(mgl32.Vec3{-1, 0, -1}, mgl32.Vec3{1, 2, 1})
	lvl := meshLevel(cooked(t, "rock", mgl32.Vec3{}, mgl32.Vec3{}, one(), pos, idx, meshcollide.ModeHulls))
	world.NaniteCollision = false
	InvalidateScenery()
	if m := lvl.Scenery[0].CollisionMode(); m != meshcollide.ModeBox {
		t.Fatalf("with the switch off a hulls prop resolves to %v", m)
	}
	lvl.Entities = []world.Entity{walkerAt("guard", mgl32.Vec3{1.15, 1, 0})}
	if moved := ResolveSceneryCollisions(lvl, nil); len(moved) != 1 {
		t.Errorf("the box pass did not take the prop: %v", moved)
	}
	if out, _, _ := pushOutOfScenery(lvl, mgl32.Vec3{1.15, 1, 0}, 0.3, 0.9); out != (mgl32.Vec3{1.15, 1, 0}) {
		t.Errorf("the chain still resolved the prop: %v", out)
	}
}
