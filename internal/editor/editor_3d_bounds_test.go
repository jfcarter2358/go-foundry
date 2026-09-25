package editor

// editor_3d_bounds_test.go — the bounds view, tested through the walk that
// both the drawing pass and the legend go through, so the two cannot disagree.

import (
	"math"
	"strings"
	"testing"

	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// boundsLevel is one of every kind the view covers, each with authored bounds,
// in a chunk that is also the active one so its brushes count.
func boundsLevel() *world.Level {
	return &world.Level{
		Chunks: []world.Chunk{{
			ID:       "c0",
			MinBound: mgl32.Vec3{0, 0, 0},
			MaxBound: mgl32.Vec3{4, 3, 4},
			Brushes: []world.Brush{
				world.NewBoxBrush("b0", mgl32.Vec3{2, 1.5, 2}, mgl32.Vec3{2, 1.5, 2}, "concrete"),
			},
		}},
		Entities: []world.Entity{{
			ID: "e0", Position: mgl32.Vec3{10, 0, 0},
			BoundHalfX: 1, BoundHalfY: 2, BoundHalfZ: 1,
		}},
		Scenery: []world.Scenery{{
			ID: "s0", Position: mgl32.Vec3{20, 0, 0},
			LocalMin: mgl32.Vec3{-1, 0, -1}, LocalMax: mgl32.Vec3{1, 2, 1},
		}},
		Volumes: []world.Volume{{
			ID: "v0", Position: mgl32.Vec3{30, 0, 0},
			HalfX: 1, HalfY: 1, HalfZ: 1, Enabled: true,
		}},
		Sprites: []world.Sprite{{
			ID: "sp0", Position: mgl32.Vec3{40, 0, 0}, Width: 2, Height: 2,
		}},
		Decals: []world.Decal{{
			ID: "d0", Position: mgl32.Vec3{50, 0, 0}, Width: 1, Height: 1, Depth: 1,
		}},
	}
}

// boundsTally walks a level the way the view does and counts what came back.
func boundsTally(lvl *world.Level) [boundsKindCount]int {
	var n [boundsKindCount]int
	editorBoundsWalk(lvl, func(kind editorBoundsKind, _, _ mgl32.Vec3) { n[kind]++ })
	return n
}

// ---------- the switch ----------

// The view is an overlay you turn on, so a fresh editor has it off and nothing
// is queued until it is asked for.
func TestBoundsViewIsOffByDefault(t *testing.T) {
	var fresh EditorState
	if fresh.ShowBounds {
		t.Error("a fresh editor starts with the bounds view on")
	}
}

func TestBoundsViewDrawsNothingWhileOff(t *testing.T) {
	defer saveEditorState()()
	Editor.level = boundsLevel()
	Editor.ActiveChunkIdx = 0
	Editor.ShowBounds = false

	gizmo.Buf = gizmo.Buf[:0]
	drawEditorBounds()
	if got := len(gizmoSegments()); got != 0 {
		t.Errorf("bounds view off queued %d segments, want 0", got)
	}

	editorSetShowBounds(true)
	drawEditorBounds()
	if got := len(gizmoSegments()); got == 0 {
		t.Error("bounds view on queued nothing")
	}
}

// ---------- what gets a box ----------

// Every kind with authored bounds gets exactly one box, and a box is twelve
// segments — so the level above is 7 boxes' worth of lines.
func TestBoundsViewBoxesEveryKind(t *testing.T) {
	defer saveEditorState()()
	Editor.level = boundsLevel()
	Editor.ActiveChunkIdx = 0

	n := boundsTally(Editor.level)
	for kind := editorBoundsKind(0); kind < boundsKindCount; kind++ {
		want := 1
		if kind == boundsKindEmitter {
			want = 0 // none placed
		}
		if n[kind] != want {
			t.Errorf("%s drew %d boxes, want %d", editorBoundsStyles[kind].Name, n[kind], want)
		}
	}

	Editor.ShowBounds = true
	gizmo.Buf = gizmo.Buf[:0]
	drawEditorBounds()
	if got, want := len(gizmoSegments()), 7*12; got != want {
		t.Errorf("queued %d segments, want %d (7 boxes)", got, want)
	}
}

// The box drawn is the one the engine culls with, not a box of the editor's
// own devising: the volume's is its position plus and minus its half-extents.
func TestBoundsViewDrawsTheEngineBox(t *testing.T) {
	defer saveEditorState()()
	Editor.level = boundsLevel()
	Editor.ActiveChunkIdx = -1 // no brushes, so the volume's box is easy to find

	var lo, hi mgl32.Vec3
	found := false
	editorBoundsWalk(Editor.level, func(kind editorBoundsKind, a, b mgl32.Vec3) {
		if kind == boundsKindVolume {
			lo, hi, found = a, b, true
		}
	})
	if !found {
		t.Fatal("no volume box")
	}
	wantLo, wantHi := mgl32.Vec3{29, -1, -1}, mgl32.Vec3{31, 1, 1}
	if !vec3Close(lo, wantLo, 1e-4) || !vec3Close(hi, wantHi, 1e-4) {
		t.Errorf("volume box = %v..%v, want %v..%v", lo, hi, wantLo, wantHi)
	}
}

// An object with nothing authored is skipped rather than drawn as a point: an
// unbounded entity is not a zero-sized one, and a dot at its feet would claim
// a volume it does not have.
func TestBoundsViewSkipsUnauthoredBounds(t *testing.T) {
	defer saveEditorState()()
	lvl := boundsLevel()
	lvl.Entities = append(lvl.Entities, world.Entity{ID: "e1", Position: mgl32.Vec3{50, 0, 0}})
	lvl.Sprites = append(lvl.Sprites, world.Sprite{ID: "sp1", Position: mgl32.Vec3{60, 0, 0}})
	Editor.level = lvl
	Editor.ActiveChunkIdx = 0

	n := boundsTally(lvl)
	if n[boundsKindEntity] != 1 {
		t.Errorf("entities drew %d boxes, want 1 (the extent-less one skipped)", n[boundsKindEntity])
	}
	if n[boundsKindSprite] != 1 {
		t.Errorf("sprites drew %d boxes, want 1 (the sizeless one skipped)", n[boundsKindSprite])
	}
}

// The sentinel range a chunk with no sectors still holds is inverted, and
// carried through a moved chunk's matrix it becomes infinities. Neither is a
// box, and drawing either is twelve lines to the horizon.
func TestBoundsViewRejectsSentinelChunkBounds(t *testing.T) {
	defer saveEditorState()()
	const big = 3.4e38
	lvl := &world.Level{Chunks: []world.Chunk{
		{ID: "empty", MinBound: mgl32.Vec3{big, big, big}, MaxBound: mgl32.Vec3{-big, -big, -big}},
		{ID: "moved", Origin: mgl32.Vec3{1, 0, 0}, Scale: 1,
			MinBound: mgl32.Vec3{big, big, big}, MaxBound: mgl32.Vec3{-big, -big, -big}},
	}}
	Editor.level = lvl
	Editor.ActiveChunkIdx = -1

	if n := boundsTally(lvl); n[boundsKindChunk] != 0 {
		t.Errorf("sentinel chunks drew %d boxes, want 0", n[boundsKindChunk])
	}
}

// Brushes are the one kind taken per-chunk rather than level-wide, so changing
// which chunk is active changes which brushes are boxed.
func TestBoundsViewBoxesOnlyTheActiveChunksBrushes(t *testing.T) {
	defer saveEditorState()()
	lvl := boundsLevel()
	lvl.Chunks = append(lvl.Chunks, world.Chunk{
		ID:       "c1",
		MinBound: mgl32.Vec3{0, 0, 0},
		MaxBound: mgl32.Vec3{4, 3, 4},
		Brushes: []world.Brush{
			world.NewBoxBrush("b1", mgl32.Vec3{2, 1.5, 2}, mgl32.Vec3{2, 1.5, 2}, "concrete"),
			world.NewBoxBrush("b2", mgl32.Vec3{2, 1.5, 2}, mgl32.Vec3{1, 1, 1}, "concrete"),
		},
	})
	Editor.level = lvl

	Editor.ActiveChunkIdx = 0
	if n := boundsTally(lvl); n[boundsKindBrush] != 1 {
		t.Errorf("chunk 0 active: %d brush boxes, want 1", n[boundsKindBrush])
	}
	Editor.ActiveChunkIdx = 1
	if n := boundsTally(lvl); n[boundsKindBrush] != 2 {
		t.Errorf("chunk 1 active: %d brush boxes, want 2", n[boundsKindBrush])
	}
	Editor.ActiveChunkIdx = -1
	if n := boundsTally(lvl); n[boundsKindBrush] != 0 {
		t.Errorf("no chunk active: %d brush boxes, want 0", n[boundsKindBrush])
	}
	// Both chunks still get their own box either way.
	if n := boundsTally(lvl); n[boundsKindChunk] != 2 {
		t.Errorf("%d chunk boxes, want 2", n[boundsKindChunk])
	}
}

func TestBoundsViewOnANilLevelDrawsNothing(t *testing.T) {
	defer saveEditorState()()
	Editor.level = nil
	Editor.ShowBounds = true
	gizmo.Buf = gizmo.Buf[:0]
	drawEditorBounds()
	if got := len(gizmoSegments()); got != 0 {
		t.Errorf("nil level queued %d segments, want 0", got)
	}
}

// ---------- the legend ----------

// The legend counts off the same walk that draws, so the two cannot drift: a
// kind that drew every object it has says so plainly, and one that skipped some
// says how many it had.
func TestBoundsLegendReportsTheGap(t *testing.T) {
	defer saveEditorState()()
	lvl := boundsLevel()
	lvl.Entities = append(lvl.Entities, world.Entity{ID: "e1", Position: mgl32.Vec3{50, 0, 0}})
	Editor.level = lvl
	Editor.ActiveChunkIdx = 0

	tally := editorBoundsCount(lvl)
	if tally.Drawn[boundsKindEntity] != 1 || tally.Total[boundsKindEntity] != 2 {
		t.Fatalf("entity tally = %d of %d, want 1 of 2",
			tally.Drawn[boundsKindEntity], tally.Total[boundsKindEntity])
	}
	row := editorBoundsLegendRow(boundsKindEntity, tally)
	if !strings.Contains(row, "1 of 2") {
		t.Errorf("entity legend row = %q, want it to say 1 of 2", row)
	}
	if row := editorBoundsLegendRow(boundsKindVolume, tally); strings.Contains(row, " of ") {
		t.Errorf("volume legend row = %q, want a plain count when nothing was skipped", row)
	}
}

// ---------- the guard ----------

func TestBoundsUsableRejectsWhatIsNotABox(t *testing.T) {
	inf := float32(math.Inf(1))
	cases := []struct {
		name   string
		lo, hi mgl32.Vec3
		want   bool
	}{
		{"ordinary", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 1, 1}, true},
		{"flat on one axis", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 0, 1}, true},
		{"degenerate", mgl32.Vec3{1, 2, 3}, mgl32.Vec3{1, 2, 3}, false},
		{"inverted", mgl32.Vec3{1, 1, 1}, mgl32.Vec3{0, 0, 0}, false},
		{"infinite", mgl32.Vec3{-inf, -inf, -inf}, mgl32.Vec3{inf, inf, inf}, false},
	}
	for _, c := range cases {
		if got := editorBoundsUsable(c.lo, c.hi); got != c.want {
			t.Errorf("%s: editorBoundsUsable(%v, %v) = %v, want %v", c.name, c.lo, c.hi, got, c.want)
		}
	}
}
