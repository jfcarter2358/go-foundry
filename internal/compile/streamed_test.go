package compile

// streamed_test.go — the two chunk markers that decide what a compile bakes
// beyond "enabled and in world space": Streamed, which bakes a disabled chunk,
// and Moves, which keeps an enabled one out.

import (
	"strings"
	"testing"

	"foundry/internal/bsp"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestAStreamedChunkIsBakedThoughItStartsDisabled(t *testing.T) {
	lvl := levelOf(world.NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete"))
	lvl.Chunks[0].Disabled = true
	lvl.Chunks[0].Streamed = true
	if !Bakes(&lvl.Chunks[0]) {
		t.Fatal("Bakes refused a streamed chunk")
	}
	if faces := mustCSG(t, lvl); len(faces) != 6 {
		t.Errorf("got %d faces from a streamed box, want its 6", len(faces))
	}
}

func TestAMovingChunkIsNotBaked(t *testing.T) {
	lvl := levelOf(world.NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete"))
	lvl.Chunks[0].Moves = true
	if Bakes(&lvl.Chunks[0]) {
		t.Fatal("Bakes accepted a chunk marked moves")
	}
	if faces := mustCSG(t, lvl); len(faces) != 0 {
		t.Errorf("got %d faces from a moving chunk; its surfaces would stay put while its collision moved", len(faces))
	}
	// Streamed does not override it: a streamed lift still moves.
	lvl.Chunks[0].Streamed = true
	if Bakes(&lvl.Chunks[0]) {
		t.Error("a chunk marked both streamed and moves was baked")
	}
}

func TestValidationFollowsTheMarkers(t *testing.T) {
	// Dynamic: a plain concave brush no longer breaks validation on its own
	// (it auto-decomposes), so this needs the one shape that still does —
	// see TestADynamicConcaveBrushStopsTheCompile.
	dentedDynamic := dentedBox()
	dentedDynamic.Dynamic = true

	lvl := levelOf(dentedDynamic)
	lvl.Chunks[0].Moves = true
	if err := Validate(lvl); err != nil {
		t.Errorf("a moving chunk was validated: %v", err)
	}
	lvl = levelOf(dentedDynamic)
	lvl.Chunks[0].Disabled, lvl.Chunks[0].Streamed = true, true
	if err := Validate(lvl); err == nil {
		t.Error("a streamed chunk with a concave dynamic brush passed validation; it is compiled")
	}
}

func TestTheSealWrapsAStreamedChunk(t *testing.T) {
	lvl := &world.Level{Chunks: []world.Chunk{
		{ID: "static", Scale: 1, Brushes: []world.Brush{boxFromTo("floor", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 1, 10})}},
		{ID: "later", Scale: 1, Disabled: true, Streamed: true, Brushes: []world.Brush{
			boxFromTo("far", mgl32.Vec3{-500, 0, 0}, mgl32.Vec3{-499, 1, 1}),
		}},
		{ID: "lift", Scale: 1, Moves: true, Brushes: []world.Brush{
			boxFromTo("up", mgl32.Vec3{0, 0, 500}, mgl32.Vec3{1, 1, 501}),
		}},
	}}
	lo, hi := FitSeal(lvl).Bounds()
	if lo.X() > -500-SealPadding+1e-3 {
		t.Errorf("the seal stops at x=%v; a streamed chunk is compiled and has to be inside it", lo.X())
	}
	if hi.Z() > 10+SealPadding+1e-3 {
		t.Errorf("the seal reaches z=%v; a moving chunk is not compiled and must not stretch it", hi.Z())
	}
}

// Two chunks that overlap, one of them streamed: the compile removes surface
// from one with the other's solid and says so.
func TestStreamedChunksThatClipEachOtherAreReported(t *testing.T) {
	overlap := func(streamed bool) Stats {
		lvl := &world.Level{Chunks: []world.Chunk{
			{ID: "hangar", Scale: 1, Brushes: []world.Brush{boxFromTo("deck", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 1, 10})}},
			{ID: "ground", Scale: 1, Disabled: streamed, Streamed: streamed, Brushes: []world.Brush{
				boxFromTo("rock", mgl32.Vec3{5, -2, 5}, mgl32.Vec3{15, 0.5, 15}),
			}},
		}}
		r, err := Compile(lvl, Options{SkipLight: true})
		if err != nil {
			t.Fatal(err)
		}
		return r.Stats
	}
	if s := overlap(false); len(s.StreamedClips) != 0 {
		t.Errorf("two always-present chunks clipping each other were reported: %+v", s.StreamedClips)
	}
	s := overlap(true)
	pairs := map[string]bool{}
	for _, c := range s.StreamedClips {
		pairs[c.FaceChunk+"<"+c.SolidChunk] = true
		if c.Faces == 0 || c.Area <= 0 {
			t.Errorf("an empty clip was reported: %+v", c)
		}
	}
	if !pairs["hangar<ground"] || !pairs["ground<hangar"] {
		t.Errorf("reported %v, want both chunks clipped by the other", pairs)
	}
	if w := s.StreamedClipWarning(); !strings.Contains(w, "hangar") || !strings.Contains(w, "ground") {
		t.Errorf("the warning does not name the chunks: %q", w)
	}
}

// The surface a streamed neighbour's solid buries is kept as cover, not
// dropped: with the neighbour switched off the zone at the edge of a streaming
// window would otherwise show a hole. Between two always-present chunks it is
// buried for good, as before.
func TestSurfaceBuriedByAStreamedNeighbourIsKeptAsCover(t *testing.T) {
	compileOverlap := func(streamed bool) *Result {
		lvl := &world.Level{Chunks: []world.Chunk{
			{ID: "near", Scale: 1, Disabled: streamed, Streamed: streamed, Brushes: []world.Brush{
				boxFromTo("slab", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 1, 10}),
			}},
			{ID: "far", Scale: 1, Disabled: streamed, Streamed: streamed, Brushes: []world.Brush{
				boxFromTo("rock", mgl32.Vec3{5, -2, 5}, mgl32.Vec3{15, 3, 15}),
			}},
		}}
		r, err := Compile(lvl, Options{SkipLight: true})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	coverArea := func(r *Result, chunk, by string) float32 {
		a := float32(0)
		for i := range r.Tree.Faces {
			f := &r.Tree.Faces[i]
			if f.Chunk == chunk && len(f.CoveredBy) == 1 && f.CoveredBy[0] == by {
				a += bsp.PolygonArea(f.Points)
			}
		}
		return a
	}

	if r := compileOverlap(false); coverArea(r, "near", "far")+coverArea(r, "far", "near") != 0 {
		t.Error("two always-present chunks produced cover surface")
	}
	r := compileOverlap(true)
	// Inside the rock: the slab's top and bottom, 5x5 each, and two sides of 5x1.
	if got := coverArea(r, "near", "far"); got < 60-1e-3 || got > 60+1e-3 {
		t.Errorf("near's cover under far is %.3f sq, want 60", got)
	}
	if coverArea(r, "far", "near") == 0 {
		t.Error("far's surface inside near's slab was dropped rather than kept as cover")
	}
	for li := range r.Tree.Leaves {
		for _, fi := range r.Tree.Leaves[li].Faces {
			if r.Tree.Faces[fi].CoveredBy != nil {
				t.Fatal("a cover face was put in a cell; it is buried as far as the tree knows")
			}
		}
	}
}
