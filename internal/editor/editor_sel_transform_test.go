package editor

import (
	"math"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// transformTestLevel is the two-box fixture wrapped in a level, with the editor
// pointed at it. Geometry is never rebuilt in these tests — that needs a GL
// context — so everything here goes through writeSelectedVertexWorld, the write
// half of setSelectedVertexWorld.
func transformTestLevel(t *testing.T) *world.Chunk {
	t.Helper()
	ch := brushTestLevel(t)
	Editor.SelectedVertices = nil
	return ch
}

// vsel names one corner of one brush of the fixture.
func vsel(bi, vi int) vertexSel {
	return vertexSel{chunkIdx: 0, brushIdx: bi, vertIdx: vi}
}

// boxCorners is every corner of one brush, which is what the rotate and scale
// transforms act on as a body.
func boxCorners(bi int) []vertexSel {
	out := make([]vertexSel, 8)
	for vi := range out {
		out[vi] = vsel(bi, vi)
	}
	return out
}

func nearVec3(t *testing.T, what string, got, want mgl32.Vec3) {
	t.Helper()
	if !vec3Close(got, want, 1e-3) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// ---------- the write-back ----------

// A brush vertex is a position, so a world position handed back by a transform
// is stored as-is (in the chunk's own space). A *sector* vertex was two stores
// — a shared chunk point's XZ plus a per-surface height offset — and the three
// tests this replaces were about the splitting, the averaging of a point named
// twice, and the reconciliation between them. None of that survives the format.
func TestWriteSelectedVertexWorldStoresThePositionAsIs(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)

	Editor.SelectedVertices = []vertexSel{vsel(0, 0)}
	w := writeSelectedVertexWorld(func(int, vertexSel, mgl32.Vec3) mgl32.Vec3 {
		return mgl32.Vec3{3, 5, 4}
	})

	nearVec3(t, "corner 0", ch.Brushes[0].Verts[0], mgl32.Vec3{3, 5, 4})
	if _, ok := w.touchedBrushes[[2]int{0, 0}]; !ok {
		t.Error("the moved brush was not recorded for rebuild")
	}
	if len(w.touchedBrushes) != 1 {
		t.Errorf("%d brushes recorded, want only the one that moved", len(w.touchedBrushes))
	}
}

// A write that lands on the value already there records nothing, so a still
// cursor mid-drag does not rebuild geometry sixty times a second.
func TestWriteSelectedVertexWorldReportsNothingWhenNothingMoved(t *testing.T) {
	defer saveEditorSelection()()
	transformTestLevel(t)

	Editor.SelectedVertices = []vertexSel{vsel(0, 0), vsel(0, 1)}
	w := writeSelectedVertexWorld(func(_ int, _ vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return cur
	})
	if !w.empty() {
		t.Errorf("a no-op write recorded %v", w.touchedBrushes)
	}
}

// A selection spanning two brushes rebuilds both, and only those.
func TestWriteSelectedVertexWorldRecordsEveryTouchedBrush(t *testing.T) {
	defer saveEditorSelection()()
	transformTestLevel(t)

	Editor.SelectedVertices = []vertexSel{vsel(0, 0), vsel(1, 0), vsel(1, 3)}
	w := writeSelectedVertexWorld(func(_ int, _ vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return cur.Add(mgl32.Vec3{0, 1, 0})
	})
	if len(w.touchedBrushes) != 2 {
		t.Errorf("%d brushes recorded, want 2", len(w.touchedBrushes))
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
// because a corner has no size to resize. Scaling a whole brush is exactly this
// applied to all of its corners.
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
	if !vec3Close(mgl32.Vec3{got.X(), 0, got.Y()}, mgl32.Vec3{0, 0, 2}, 1e-3) {
		t.Errorf("quarter turn = %v, want (0, 2)", got)
	}
}

// The point of the rotate tool on a multi-selection: every corner swings about
// the group's shared centre, so the shape turns as a body instead of each
// corner spinning uselessly about itself.
func TestRotationTransformTurnsTheSelectionAboutItsSharedCentre(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)

	// Brush "a" is the unit box centred on (1,1,1).
	Editor.SelectedVertices = boxCorners(0)
	center, ok := selectedVerticesCenter()
	if !ok {
		t.Fatal("no centre for the selection")
	}
	nearVec3(t, "selection centre", center, mgl32.Vec3{1, 1, 1})

	Editor.GizmoDragWorld = selectedVerticesWorld()
	writeSelectedVertexWorld(rotationTransform(mgl32.Vec3{0, 1, 0}, center, mgl32.DegToRad(90)))

	// A quarter turn of a cube about its own centre is the cube again, with its
	// corners permuted — so the centre and the extent are unchanged.
	after, _ := selectedVerticesCenter()
	nearVec3(t, "centre after rotating", after, mgl32.Vec3{1, 1, 1})
	// Corner 0 is (0,0,0); a quarter turn about Y through (1,1,1) takes it to
	// (0,0,2), since +X goes to -Z and -Z goes to +X.
	nearVec3(t, "corner 0", ch.Brushes[0].Verts[0], mgl32.Vec3{0, 0, 2})
}

// The scale tool's contract: selected corners move toward or away from the
// group's centre. The centre itself never moves, and the spacing changes by
// exactly the factor.
func TestScaleTransformSpreadsTheSelectionAboutItsCentre(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)

	Editor.SelectedVertices = boxCorners(0)
	center, _ := selectedVerticesCenter()
	Editor.GizmoDragWorld = selectedVerticesWorld()
	writeSelectedVertexWorld(scaleTransform(center, mgl32.Vec3{2, 1, 2}))

	// The unit box centred on (1,1,1), doubled in X and Z: x,z ∈ [-1,3].
	nearVec3(t, "corner 0", ch.Brushes[0].Verts[0], mgl32.Vec3{-1, 0, -1})
	nearVec3(t, "corner 7", ch.Brushes[0].Verts[7], mgl32.Vec3{3, 2, 3})
	after, _ := selectedVerticesCenter()
	nearVec3(t, "centre after scaling", after, mgl32.Vec3{1, 1, 1})
}

// The corners a selection does not name must be left where they are, or scaling
// one brush would drag its neighbour with it.
func TestScaleTransformLeavesUnselectedCornersAlone(t *testing.T) {
	defer saveEditorSelection()()
	ch := transformTestLevel(t)
	before := ch.Brushes[1].Verts[0]

	Editor.SelectedVertices = []vertexSel{vsel(0, 0)}
	center, _ := selectedVerticesCenter()
	Editor.GizmoDragWorld = selectedVerticesWorld()
	writeSelectedVertexWorld(scaleTransform(center.Add(mgl32.Vec3{5, 0, 5}), mgl32.Vec3{3, 3, 3}))

	if ch.Brushes[1].Verts[0] != before {
		t.Errorf("an unselected corner moved: %v, was %v", ch.Brushes[1].Verts[0], before)
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
	if !vec3Close(mgl32.Vec3{c.X(), 0, c.Y()}, mgl32.Vec3{4, 0, 6}, 1e-3) {
		t.Errorf("spawn centre = %v, want (4, 6)", c)
	}

	Editor.SpawnSelected = false
	if _, ok := selectionCenterXZ(); ok {
		t.Error("nothing is selected, so there is no centre")
	}
}
