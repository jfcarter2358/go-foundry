package main

import (
	"foundry/internal/leveltest"
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// transformTestLevel is leveltest.SplitChunk (two square sectors sharing the edge
// between chunk points 1 and 2) wrapped in a level, with the editor pointed at
// it. Geometry is never rebuilt in these tests — that needs a GL context —
// so everything here goes through writeSelectedVertexWorld, the write half of
// setSelectedVertexWorld.
func transformTestLevel(t *testing.T) *world.Chunk {
	t.Helper()
	lvl := &world.Level{Chunks: []world.Chunk{*leveltest.SplitChunk()}}
	Editor.level = lvl
	Editor.ActiveChunkIdx = 0
	Editor.SelectedVertices = nil
	return &lvl.Chunks[0]
}

// vsel names one corner handle of the level above.
func vsel(ch *world.Chunk, si, vi int, isFloor bool) vertexSel {
	return vertexSel{
		chunkIdx:  0,
		sectorIdx: si,
		vertIdx:   vi,
		pointIdx:  ch.Sectors[si].PointIndices[vi],
		isFloor:   isFloor,
	}
}

func nearVec2(t *testing.T, what string, got, want mgl32.Vec2) {
	t.Helper()
	if math.Abs(float64(got.X()-want.X())) > 1e-3 || math.Abs(float64(got.Y()-want.Y())) > 1e-3 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// A world position handed back by a transform has to land in the two places a
// sector vertex is actually stored: its XZ on the shared chunk point, its Y as
// an offset from the sector's own floor (or ceiling) height.
func TestWriteSelectedVertexWorldSplitsPositionAcrossItsTwoStores(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)
	ch.Sectors[0].Floor.Height = 2

	Editor.SelectedVertices = []vertexSel{vsel(ch, 0, 0, true)}
	w := writeSelectedVertexWorld(func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return mgl32.Vec3{3, 5, 4}
	})

	nearVec2(t, "chunk point", ch.Points[0], mgl32.Vec2{3, 4})
	if got := ch.Sectors[0].Floor.VertexOffsets[0]; got != 3 {
		t.Errorf("floor offset = %v, want 3 (world 5 minus the sector's height 2)", got)
	}
	if _, ok := w.movedPoints[[2]int{0, 0}]; !ok {
		t.Error("the moved point was not reported for rebuilding")
	}
	if _, ok := w.touchedSectors[[2]int{0, 0}]; !ok {
		t.Error("the height change was not reported for rebuilding")
	}
}

// The floor and ceiling handles of one corner are two selections of a single
// shared XZ. Writing them in selection order would make the result depend on
// which was clicked first; averaging does not.
func TestWriteSelectedVertexWorldAveragesASharedPoint(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)

	Editor.SelectedVertices = []vertexSel{vsel(ch, 0, 0, true), vsel(ch, 0, 0, false)}
	want := []mgl32.Vec3{{2, 0, 0}, {4, 4, 8}}
	writeSelectedVertexWorld(func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return want[i]
	})

	nearVec2(t, "shared chunk point", ch.Points[0], mgl32.Vec2{3, 4})
	// Each handle still keeps its own height, which is per-vertex and unshared.
	if got := ch.Sectors[0].Floor.VertexOffsets[0]; got != 0 {
		t.Errorf("floor offset = %v, want 0", got)
	}
	if got := ch.Sectors[0].Ceiling.VertexOffsets[0]; got != 0 {
		t.Errorf("ceiling offset = %v, want 0 (world 4 minus the ceiling height 4)", got)
	}
}

// A transform that returns what was already there must report nothing to
// rebuild — a still cursor during a drag sends one of these on every motion
// event, and reloading the sector's geometry each time would be wasted work.
func TestWriteSelectedVertexWorldReportsNothingWhenNothingMoved(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)

	Editor.SelectedVertices = []vertexSel{vsel(ch, 0, 0, true), vsel(ch, 0, 1, true)}
	w := writeSelectedVertexWorld(func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return cur
	})
	if !w.empty() {
		t.Errorf("an identity transform reported changes: %+v", w)
	}
}

// A corner shared by two sectors is one chunk point, so moving it through
// either sector's handle moves it for both — which is what keeps the shared
// edge shared instead of tearing the sectors apart.
func TestWriteSelectedVertexWorldMovesSharedCornersOnce(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)

	// Sector A's vertex 1 and sector B's vertex 0 are both chunk point 1.
	Editor.SelectedVertices = []vertexSel{vsel(ch, 0, 1, true), vsel(ch, 1, 0, true)}
	if Editor.SelectedVertices[0].pointIdx != Editor.SelectedVertices[1].pointIdx {
		t.Fatal("test setup: the two handles are not the same chunk point")
	}
	w := writeSelectedVertexWorld(func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return cur.Add(mgl32.Vec3{1, 0, 1})
	})

	nearVec2(t, "shared corner", ch.Points[1], mgl32.Vec2{11, 1})
	if n := len(w.moves[0]); n != 1 {
		t.Errorf("the shared point was recorded %d times, want once", n)
	}
}

// ---------- the transforms ----------

func TestRotatePointAbout(t *testing.T) {
	center := mgl32.Vec3{1, 0, 1}
	// A quarter turn about Y takes +X to -Z (the mathgl/OpenGL convention).
	got := rotatePointAbout(mgl32.Vec3{3, 5, 1}, center, mgl32.Vec3{0, 1, 0}, mgl32.DegToRad(90))
	if math.Abs(float64(got.X()-1)) > 1e-4 || math.Abs(float64(got.Z()+1)) > 1e-4 {
		t.Errorf("rotated XZ = (%v,%v), want (1,-1)", got.X(), got.Z())
	}
	if math.Abs(float64(got.Y()-5)) > 1e-4 {
		t.Errorf("a turn about Y changed the height: %v", got.Y())
	}
	// A degenerate axis is a no-op rather than a division by nothing.
	p := mgl32.Vec3{7, 8, 9}
	if rotatePointAbout(p, center, mgl32.Vec3{}, 1) != p {
		t.Error("a zero axis moved the point")
	}
}

// Scaling a selection spreads it about its centre; it does not resize anything,
// because a sector corner has no size to resize.
func TestScalePointAboutSpreadsFromTheCentre(t *testing.T) {
	center := mgl32.Vec3{0, 0, 0}
	got := scalePointAbout(mgl32.Vec3{2, 3, 4}, center, mgl32.Vec3{2, 1, 1})
	if got != (mgl32.Vec3{4, 3, 4}) {
		t.Errorf("X-only scale = %v, want (4,3,4)", got)
	}
	// A point already at the centre stays there whatever the factor.
	if got := scalePointAbout(center, center, mgl32.Vec3{5, 5, 5}); got != center {
		t.Errorf("the centre moved under a scale: %v", got)
	}
}

func TestRotateXZAboutLeavesHeightAlone(t *testing.T) {
	got := rotateXZAbout(mgl32.Vec2{2, 0}, mgl32.Vec2{0, 0}, mgl32.DegToRad(90))
	nearVec2(t, "quarter turn", got, mgl32.Vec2{0, 2})
}

// The point of the rotate tool on a multi-selection: every corner swings about
// the group's shared centre, so the shape turns as a body instead of each
// corner spinning uselessly about itself.
func TestRotationTransformTurnsTheSelectionAboutItsSharedCentre(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)

	// Sector A's four corners: (0,0) (10,0) (10,10) (0,10), centred on (5,5).
	Editor.SelectedVertices = []vertexSel{
		vsel(ch, 0, 0, true), vsel(ch, 0, 1, true), vsel(ch, 0, 2, true), vsel(ch, 0, 3, true),
	}
	center, ok := selectedVerticesCenter()
	if !ok {
		t.Fatal("no centre for the selection")
	}
	nearVec2(t, "selection centre", mgl32.Vec2{center.X(), center.Z()}, mgl32.Vec2{5, 5})

	Editor.GizmoDragWorld = selectedVerticesWorld()
	writeSelectedVertexWorld(rotationTransform(mgl32.Vec3{0, 1, 0}, center, mgl32.DegToRad(90)))

	// A quarter turn of a square about its own centre is the square again, with
	// its corners permuted — so the outline is unchanged and no corner stayed.
	nearVec2(t, "corner 0", ch.Points[0], mgl32.Vec2{0, 10})
	nearVec2(t, "corner 1", ch.Points[1], mgl32.Vec2{0, 0})
	nearVec2(t, "corner 2", ch.Points[2], mgl32.Vec2{10, 0})
	nearVec2(t, "corner 3", ch.Points[3], mgl32.Vec2{10, 10})
}

// The scale tool's contract: selected corners move toward or away from the
// group's centre. The centre itself never moves, and the spacing changes by
// exactly the factor.
func TestScaleTransformSpreadsTheSelectionAboutItsCentre(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)

	Editor.SelectedVertices = []vertexSel{
		vsel(ch, 0, 0, true), vsel(ch, 0, 1, true), vsel(ch, 0, 2, true), vsel(ch, 0, 3, true),
	}
	center, _ := selectedVerticesCenter()
	Editor.GizmoDragWorld = selectedVerticesWorld()
	writeSelectedVertexWorld(scaleTransform(center, mgl32.Vec3{2, 1, 2}))

	nearVec2(t, "corner 0", ch.Points[0], mgl32.Vec2{-5, -5})
	nearVec2(t, "corner 2", ch.Points[2], mgl32.Vec2{15, 15})
	after, _ := selectedVerticesCenter()
	nearVec2(t, "centre after scaling", mgl32.Vec2{after.X(), after.Z()}, mgl32.Vec2{5, 5})
}

// The chunk points a selection does not name must be left where they are, or
// scaling one sector would drag its neighbour's far side with it.
func TestScaleTransformLeavesUnselectedCornersAlone(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)
	before := ch.Points[4]

	Editor.SelectedVertices = []vertexSel{vsel(ch, 0, 0, true)}
	center, _ := selectedVerticesCenter()
	Editor.GizmoDragWorld = selectedVerticesWorld()
	writeSelectedVertexWorld(scaleTransform(center.Add(mgl32.Vec3{5, 0, 5}), mgl32.Vec3{3, 3, 3}))

	if ch.Points[4] != before {
		t.Errorf("an unselected corner moved: %v, was %v", ch.Points[4], before)
	}
}

func TestSelectionCenterXZFallsBackToTheSpawnMarker(t *testing.T) {
	defer saveEditorSelection()()
	Editor.level = &world.Level{Spawn: &world.SpawnPoint{Position: mgl32.Vec3{4, 1, 6}}}
	Editor.SelectedVertices = nil
	Editor.SpawnSelected = true

	c, ok := selectionCenterXZ()
	if !ok {
		t.Fatal("the selected spawn marker has no centre")
	}
	nearVec2(t, "spawn centre", c, mgl32.Vec2{4, 6})

	Editor.SpawnSelected = false
	if _, ok := selectionCenterXZ(); ok {
		t.Error("nothing is selected, so there is no centre")
	}
}
