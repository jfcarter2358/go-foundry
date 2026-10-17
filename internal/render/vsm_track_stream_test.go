package render

// vsm_track_stream_test.go — the virtual shadow map's tracker and a streamed
// cooked mesh's residency (nanite_stream_gen.go; docs/regions/PRESETS_4.md,
// bug 2a).
//
// A static shadow page is drawn again only when the tracker hands in a box
// over it. A streamed mesh changes what it casts whenever its resident pages
// change, with nothing about the placement moving; the tracker must see
// that, or a page drawn while the mesh was coarse keeps the coarse shadow
// for good (the biome showcase's polygonal "fan" shadows on open prairie:
// terrain tiles' root clusters cast onto the fine ground). No GL: the
// streamer here is the bookkeeping alone.

import (
	"testing"

	"foundry/internal/nanite"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestVSMTrackerRedrawsAPlacementWhoseStreamedMeshChanged(t *testing.T) {
	prev := naniteScenery.stream
	t.Cleanup(func() { naniteScenery.stream = prev })

	m := &nanite.Mesh{Lo: mgl32.Vec3{-1, 0, -1}, Hi: mgl32.Vec3{1, 2, 1}}
	sm := &streamMesh{m: m}
	naniteScenery.stream = &naniteStreamer{meshes: []*streamMesh{sm}}

	s := world.NewScenery("tile", "tile.fnm", mgl32.Vec3{10, 0, 10})
	s.Nanite, s.NaniteGPU = m, true
	s.LocalMin, s.LocalMax = m.Lo, m.Hi
	lvl := &world.Level{Scenery: []world.Scenery{s}}
	lo, hi, ok := lvl.Scenery[0].WorldAABBFrom(sceneryModel(lvl, &lvl.Scenery[0]))
	if !ok {
		t.Fatal("the placement has no box")
	}
	box := vsmBox{lo, hi, true}

	var tr vsmTracker
	if all, _ := tr.collect(lvl, mgl32.Vec3{}, 100); !all {
		t.Fatal("a new level did not mark every page stale")
	}
	if _, boxes := tr.collect(lvl, mgl32.Vec3{}, 100); boxIn(boxes, box) {
		t.Fatal("a still placement whose mesh did not change was handed in")
	}

	// The streamer took pages in: the mesh moved to another band, and held
	// it long enough to count.
	sm.band, sm.bandSince = 3, 0
	naniteScenery.stream.frame = streamGenSettle - 1
	if _, boxes := tr.collect(lvl, mgl32.Vec3{}, 100); boxIn(boxes, box) {
		t.Fatal("a band that has not settled redrew the placement's pages")
	}
	naniteScenery.stream.frame = streamGenSettle
	_, boxes := tr.collect(lvl, mgl32.Vec3{}, 100)
	if !boxIn(boxes, box) {
		t.Fatal("the placement's box was not handed in after its mesh's resident pages changed: " +
			"its shadow pages keep what the mesh cast before")
	}
	if _, boxes := tr.collect(lvl, mgl32.Vec3{}, 100); boxIn(boxes, box) {
		t.Error("the placement is handed in again with nothing changed since")
	}
}

func TestStreamerMeshGen(t *testing.T) {
	a, b := &nanite.Mesh{}, &nanite.Mesh{}
	s := &naniteStreamer{meshes: []*streamMesh{{m: a, gen: 3}}}
	s.frame = 1000
	if g := s.meshGen(a); g != 3 {
		t.Errorf("meshGen(a) = %d, want 3", g)
	}
	if g := s.meshGen(b); g != 0 {
		t.Errorf("a mesh not in the pool has generation %d, want 0", g)
	}
	var none *naniteStreamer
	if g := none.meshGen(a); g != 0 {
		t.Errorf("no streamer: generation %d, want 0", g)
	}
}

func TestResidencyBand(t *testing.T) {
	// Two root pages and eight others.
	pages := make([]streamPage, 10)
	pages[0].root, pages[1].root = true, true
	res := make([]bool, 10)
	res[0], res[1] = true, true
	if b := residencyBand(res, pages); b != 0 {
		t.Errorf("roots alone: band %d, want 0", b)
	}
	res[2] = true
	one := residencyBand(res, pages)
	if one == 0 {
		t.Error("a page past the roots is still band 0")
	}
	res[3] = true
	if b := residencyBand(res, pages); b < one {
		t.Errorf("more resident fell to band %d from %d", b, one)
	}
	for i := range res {
		res[i] = true
	}
	if b := residencyBand(res, pages); b != streamGenBands {
		t.Errorf("everything resident: band %d, want %d", b, streamGenBands)
	}
	// A page or two of eight hundred is the same band.
	pages = make([]streamPage, 802)
	pages[0].root, pages[1].root = true, true
	res = make([]bool, 802)
	for i := 0; i < 400; i++ {
		res[i] = true
	}
	a := residencyBand(res, pages)
	res[400], res[401] = true, true
	if b := residencyBand(res, pages); b != a {
		t.Errorf("two pages of 800 moved the band %d -> %d", a, b)
	}
}
