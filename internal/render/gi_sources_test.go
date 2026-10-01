package render

// gi_sources_test.go — what live GI's distance field is built from, without a
// GPU: the hulls' planes, the models' baked grids, and which frames say
// something changed.

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// hullDist is gi_sdf_comp.glsl's, for checking the planes on the CPU.
func hullDist(s giSource, p mgl32.Vec3) float32 {
	d := float32(-1e9)
	for _, pl := range s.planes {
		d = max(d, mgl32.Vec3{pl[0], pl[1], pl[2]}.Dot(p)-pl[3])
	}
	return d
}

func boxChunk(dynamic bool) *world.Chunk {
	b := world.NewBoxBrush("box", mgl32.Vec3{1, 2, 3}, mgl32.Vec3{1, 1, 1}, "")
	b.Dynamic = dynamic
	return &world.Chunk{ID: "c", Brushes: []world.Brush{b}}
}

func TestABrushHullMeasuresItsOwnDistance(t *testing.T) {
	ch := boxChunk(false)
	srcs := brushSources(ch, 0, mgl32.Ident4())
	if len(srcs) != 1 || len(srcs[0].planes) != 6 {
		t.Fatalf("a box brush gave %d sources (want 1 of 6 planes)", len(srcs))
	}
	s := srcs[0]
	for _, c := range []struct {
		p    mgl32.Vec3
		want float32
	}{
		{mgl32.Vec3{1, 2, 3}, -1},      // the centre: a unit in from every face
		{mgl32.Vec3{1, 3.5, 3}, 0.5},   // above the top face
		{mgl32.Vec3{2.25, 2, 3}, 0.25}, // beside +X
	} {
		if got := hullDist(s, c.p); math.Abs(float64(got-c.want)) > 1e-4 {
			t.Errorf("at %v the hull says %v, want %v", c.p, got, c.want)
		}
	}
	// Off a corner the planes underestimate, never overestimate: a sphere
	// trace stepping by it cannot pass through the box.
	p := mgl32.Vec3{3, 4, 5}
	if got, exact := hullDist(s, p), float32(math.Sqrt(3)); got > exact+1e-4 {
		t.Errorf("off the corner the hull says %v, more than the true %v", got, exact)
	}
}

func TestABrushInAMovedChunkIsMeasuredWhereItIs(t *testing.T) {
	ch := boxChunk(false)
	m := mgl32.Translate3D(10, 0, 0).Mul4(mgl32.HomogRotate3DY(math.Pi / 2))
	s := brushSources(ch, 0, m)[0]
	centre := mulPoint(m, mgl32.Vec3{1, 2, 3})
	if got := hullDist(s, centre); math.Abs(float64(got+1)) > 1e-4 {
		t.Errorf("the moved box's centre is %v from its surface, want -1", got)
	}
	if !(s.lo[0] <= centre[0] && centre[0] <= s.hi[0]) {
		t.Errorf("the moved box's bounds %v..%v miss its centre %v", s.lo, s.hi, centre)
	}
}

func TestAGlassBrushIsLeftOutOfGI(t *testing.T) {
	ch := boxChunk(false)
	for i := range ch.Brushes[0].Faces {
		ch.Brushes[0].Faces[i].Alpha = 0.4
	}
	if srcs := brushSources(ch, 0, mgl32.Ident4()); len(srcs) != 0 {
		t.Errorf("a brush with no opaque face gave %d sources; glass neither blocks nor bounces", len(srcs))
	}
}

func TestAStillLevelReportsNoChangeAfterItsFirstFrame(t *testing.T) {
	lvl := &world.Level{Chunks: []world.Chunk{*boxChunk(false)}}
	s := newGISourceSet()
	s.collect(lvl)
	if !s.changed || len(s.dirty) == 0 || len(s.flat) != 1 {
		t.Fatalf("the first frame: changed %v, %d dirty, %d sources", s.changed, len(s.dirty), len(s.flat))
	}
	s.changed = false
	s.collect(lvl)
	if s.changed || len(s.dirty) != 0 {
		t.Errorf("nothing moved, yet the frame reports changed %v with %d dirty", s.changed, len(s.dirty))
	}
}

func TestMovingADynamicBrushDirtiesWhereItWasAndWhereItIs(t *testing.T) {
	lvl := &world.Level{Chunks: []world.Chunk{*boxChunk(true)}}
	s := newGISourceSet()
	s.collect(lvl)
	s.changed = false

	lvl.Chunks[0].Brushes[0].Offset = mgl32.Vec3{5, 0, 0}
	s.collect(lvl)
	if !s.changed || len(s.dirty) != 2 {
		t.Fatalf("a moved brush: changed %v, %d dirty bounds (want its old and new)", s.changed, len(s.dirty))
	}
	if s.dirty[0][0][0] > 1 || s.dirty[1][1][0] < 6 {
		t.Errorf("dirty bounds %v do not cover both the old box and the new one", s.dirty)
	}
}

func TestSwitchingAChunkOffRemovesItsSources(t *testing.T) {
	lvl := &world.Level{Chunks: []world.Chunk{*boxChunk(false)}}
	s := newGISourceSet()
	s.collect(lvl)
	lvl.Chunks[0].Disabled = true
	s.changed = false
	s.collect(lvl)
	if !s.changed || len(s.flat) != 0 || len(s.dirty) != 1 {
		t.Errorf("a switched-off chunk: changed %v, %d sources left, %d dirty", s.changed, len(s.flat), len(s.dirty))
	}
}

// cubeTris is a closed unit cube, centred on c.
func cubeTris(c mgl32.Vec3) []mgl32.Vec3 {
	b := world.NewBoxBrush("cube", c, mgl32.Vec3{0.5, 0.5, 0.5}, "")
	var out []mgl32.Vec3
	for fi := range b.Faces {
		pts := b.FacePoints(&b.Faces[fi])
		for i := 1; i+1 < len(pts); i++ {
			out = append(out, pts[0], pts[i], pts[i+1])
		}
	}
	return out
}

func TestAModelsGridIsNegativeInsideAndPositiveOut(t *testing.T) {
	g := bakeMeshSDF(cubeTris(mgl32.Vec3{}), giMeshRes)
	if g == nil {
		t.Fatal("no grid for a cube")
	}
	at := func(p mgl32.Vec3) float32 {
		q := p.Sub(g.Lo).Mul(1 / g.Cell)
		x, y, z := int(math.Round(float64(q[0]))), int(math.Round(float64(q[1]))), int(math.Round(float64(q[2])))
		return g.Data[(z*g.N+y)*g.N+x] * g.Cell
	}
	if d := at(mgl32.Vec3{}); d >= 0 {
		t.Errorf("the cube's centre reads %v; inside should be negative", d)
	}
	// The grid keeps two cells of air round the model; a point a cell and a
	// half above the top face is in it, and outside.
	above := 0.5 + 1.5*g.Cell
	if d := at(mgl32.Vec3{0, above, 0}); d <= 0 || math.Abs(float64(d-1.5*g.Cell)) > float64(g.Cell)*0.6 {
		t.Errorf("a cell and a half above the top reads %v, want about %v", d, 1.5*g.Cell)
	}
	if d := at(g.Lo); d <= 0 {
		t.Errorf("the grid's corner reads %v; it is outside", d)
	}
}

func TestPointTriangleDistance(t *testing.T) {
	a, b, c := mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0}
	for _, k := range []struct {
		p    mgl32.Vec3
		want float32
	}{
		{mgl32.Vec3{0.25, 0.25, 2}, 2},                 // over the face
		{mgl32.Vec3{-1, 0, 0}, 1},                      // past a corner
		{mgl32.Vec3{0.5, -3, 0}, 3},                    // past an edge
		{mgl32.Vec3{1, 1, 0}, float32(math.Sqrt(0.5))}, // past the long edge
	} {
		if got := pointTriangleDistance(k.p, a, b, c); math.Abs(float64(got-k.want)) > 1e-5 {
			t.Errorf("distance from %v: %v, want %v", k.p, got, k.want)
		}
	}
}

func TestTheFieldIndexFindsWhatTouchesABrick(t *testing.T) {
	near := giSource{lo: mgl32.Vec3{0, 0, 0}, hi: mgl32.Vec3{1, 1, 1}}
	far := giSource{lo: mgl32.Vec3{100, 0, 0}, hi: mgl32.Vec3{101, 1, 1}}
	huge := giSource{lo: mgl32.Vec3{-5000, -1, -5000}, hi: mgl32.Vec3{5000, 1, 5000}}
	var x giIndex
	x.build([]giSource{near, far, huge})
	got := x.query(mgl32.Vec3{0.5, 0.5, 0.5}, mgl32.Vec3{2, 2, 2}, nil)
	if len(got) != 2 || got[0] == 1 || got[1] == 1 {
		t.Errorf("a brick at the origin found sources %v; want the near one and the huge one", got)
	}
}

// A new level struct with the same chunk IDs — an editor undo — has to be
// diffed by content: the chunk's stamp only vouches for the chunk it was
// taken from.
func TestAdoptingANewLevelStructDiffsItsChunksByContent(t *testing.T) {
	two := &world.Level{Chunks: []world.Chunk{*boxChunk(false)}}
	two.Chunks[0].Brushes = append(two.Chunks[0].Brushes,
		world.NewBoxBrush("lid", mgl32.Vec3{1, 5, 3}, mgl32.Vec3{1, 0.25, 1}, ""))
	s := newGISourceSet()
	s.collect(two)

	one := &world.Level{Chunks: []world.Chunk{*boxChunk(false)}}
	s.forgetStamps()
	s.changed = false
	s.collect(one)
	if !s.changed || len(s.flat) != 1 || len(s.dirty) != 1 {
		t.Errorf("the lid's removal: changed %v, %d sources, %d dirty (want 1 and 1)", s.changed, len(s.flat), len(s.dirty))
	}
}

// A moving source is a change, but not to the static set the field's tree is
// built over — or every walking entity would rebuild it, which is what took a
// canyon of 3,800 brushes to 10 fps.
func TestMovingADynamicBrushLeavesTheStaticTreeAlone(t *testing.T) {
	ch := boxChunk(false)
	moving := world.NewBoxBrush("door", mgl32.Vec3{5, 0, 0}, mgl32.Vec3{1, 1, 1}, "")
	moving.Dynamic = true
	ch.Brushes = append(ch.Brushes, moving)
	lvl := &world.Level{Chunks: []world.Chunk{*ch}}
	s := newGISourceSet()
	s.collect(lvl)
	if !s.staticChanged || s.nStatic != 1 || len(s.flat) != 2 {
		t.Fatalf("first frame: staticChanged %v, %d static of %d", s.staticChanged, s.nStatic, len(s.flat))
	}
	s.changed, s.staticChanged = false, false

	lvl.Chunks[0].Brushes[1].Offset = mgl32.Vec3{0, 3, 0}
	s.collect(lvl)
	if !s.changed || s.staticChanged {
		t.Errorf("a moved door: changed %v, staticChanged %v (want true, false)", s.changed, s.staticChanged)
	}
	var x giIndex
	x.update(s.flat, s.nStatic, true)
	if got := x.query(mgl32.Vec3{4.5, 2.5, -0.5}, mgl32.Vec3{5.5, 3.5, 0.5}, nil); len(got) != 1 || got[0] != 1 {
		t.Errorf("the moved door is not found where it went: %v", got)
	}
}
