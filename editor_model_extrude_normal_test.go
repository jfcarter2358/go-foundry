package main

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// tiltedQuadDoc is one face lying in the plane y = z, so its normal points
// along none of the world axes. That is the case the feature exists for: on an
// axis-aligned face the world arms already do the right thing, and a test on
// one could not tell the two modes apart.
func tiltedQuadDoc() *ModelDoc {
	m := &ModelDoc{
		Verts: []mgl32.Vec3{{0, 0, 0}, {1, 0, 0}, {1, 1, 1}, {0, 1, 1}},
		Faces: []ModelFace{{
			Verts:   []int{0, 1, 2, 3},
			UVs:     []mgl32.Vec2{{0, 0}, {1, 0}, {1, 1}, {0, 1}},
			Texture: "default",
		}},
	}
	m.invalidate()
	return m
}

// selectTiltedFace installs the tilted quad with its one face selected.
func selectTiltedFace(t *testing.T) *ModelDoc {
	t.Helper()
	m := tiltedQuadDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	withGridSnap(t, false, 0.1)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{0}
	return m
}

// moveArmMouse returns a screen point at distance `along` from the gizmo origin
// on the arm the move gizmo is currently offering as axis i.
func moveArmMouse(t *testing.T, i int, along float32) (int32, int32) {
	t.Helper()
	origin, ok := modelGizmoOrigin()
	if !ok {
		t.Fatal("no gizmo origin")
	}
	dir, ok := modelMoveAxisDir(i)
	if !ok {
		t.Fatalf("no move arm %d", i)
	}
	w, h, _ := modelViewport()
	sp, ok := projectToScreenIn(modelProjView(), origin.Add(dir.Mul(along)), w, h)
	if !ok {
		t.Fatal("the arm is off screen")
	}
	return int32(sp.X()), int32(sp.Y())
}

func TestSecondExtrudeAimsTheGizmoAlongTheNormal(t *testing.T) {
	m := selectTiltedFace(t)

	modelStartExtrude()
	faces := len(m.Faces)
	if dirs, _ := modelMoveAxes(); len(dirs) != 3 {
		t.Fatalf("a fresh extrude offers %d arms, want the three world axes", len(dirs))
	}

	// The second press re-aims rather than stacking a second extrude on top.
	modelStartExtrude()
	if len(m.Faces) != faces {
		t.Errorf("the second E added %d faces; it should only re-aim the first extrude",
			len(m.Faces)-faces)
	}
	dirs, _ := modelMoveAxes()
	if len(dirs) != 1 {
		t.Fatalf("aimed at the normal, but the gizmo still offers %d arms", len(dirs))
	}
	want := mgl32.Vec3{0, -1, 1}.Normalize()
	if math.Abs(float64(dirs[0].Dot(want))-1) > 1e-5 {
		t.Errorf("the arm points %v, want the face normal %v", dirs[0], want)
	}

	// A third press puts it back on the world axes, again without extruding.
	modelStartExtrude()
	if len(m.Faces) != faces {
		t.Errorf("the third E added geometry; it should only toggle back")
	}
	if dirs, _ := modelMoveAxes(); len(dirs) != 3 {
		t.Errorf("toggling back left %d arms, want 3", len(dirs))
	}
}

func TestDraggingTheNormalArmMovesAlongTheNormal(t *testing.T) {
	m := selectTiltedFace(t)
	modelStartExtrude()
	modelStartExtrude() // aim along the normal

	cap0 := append([]int(nil), m.Faces[modelSelection.Faces[0]].Verts...)
	before := append([]mgl32.Vec3(nil), m.Verts...)
	normal := mgl32.Vec3{0, -1, 1}.Normalize()

	// Grab the arm a little way out and pull it a unit further along itself.
	mx, my := moveArmMouse(t, 0, modelGizmoArmLength(mgl32.Vec3{}))
	if !modelGizmoMouseDown(mx, my) {
		t.Fatal("the gizmo did not grab the normal arm")
	}
	dx, dy := moveArmMouse(t, 0, modelGizmoArmLength(mgl32.Vec3{})+1)
	modelGizmoMouseMove(dx, dy)
	modelGizmoMouseUp()

	for _, vi := range cap0 {
		offset := m.Verts[vi].Sub(before[vi])
		// The mouse target is quantised to whole pixels, so the distance lands
		// near one rather than on it; the direction is what must be exact.
		if math.Abs(float64(offset.Len()-1)) > 0.05 {
			t.Errorf("corner %d moved %v, want about 1 unit", vi, offset.Len())
		}
		if math.Abs(float64(offset.Normalize().Dot(normal))-1) > 1e-4 {
			t.Errorf("corner %d moved along %v, want the normal %v", vi, offset.Normalize(), normal)
		}
	}

	// The sides it walled in must have picked up that depth, the same as any
	// other extrude drag.
	for _, fi := range modelExtrudePending.Sides {
		if got := uvVSpan(&m.Faces[fi]); math.Abs(float64(got-1)) > 0.05 {
			t.Errorf("side %d spans %v in V, want the depth it was dragged to", fi, got)
		}
	}
}

func TestStackingAnExtrudeKeepsTheAim(t *testing.T) {
	m := selectTiltedFace(t)
	modelStartExtrude()
	modelStartExtrude() // aim along the normal
	faces := len(m.Faces)

	// Pull the cap out and stack a second segment on it (Shift+E).
	modelNudgeSelection(1, 1)
	modelExtrudeAgain()

	if len(m.Faces) <= faces {
		t.Fatalf("Shift+E added %d faces, want a second extrude", len(m.Faces)-faces)
	}
	if !modelExtrudePending.AlongNormal {
		t.Error("the new extrude fell back to the world axes; the aim should carry over")
	}
	if dirs, _ := modelMoveAxes(); len(dirs) != 1 {
		t.Errorf("the new extrude offers %d arms, want the single normal one", len(dirs))
	}
}

// The reported bug: extrude, drag out the depth, then press E to re-aim. That
// press has to change the arms, not silently stack an invisible zero-depth
// extrude — from the outside, that reads as E doing nothing at all.
func TestEAfterADragStillReAims(t *testing.T) {
	m := selectTiltedFace(t)
	modelStartExtrude()
	modelNudgeSelection(1, 1) // set the depth, as a gizmo drag would
	faces := len(m.Faces)

	modelStartExtrude()

	if len(m.Faces) != faces {
		t.Errorf("E added %d faces, want it to re-aim the extrude it is on",
			len(m.Faces)-faces)
	}
	dirs, _ := modelMoveAxes()
	if len(dirs) != 1 {
		t.Fatalf("the gizmo still offers %d arms, want the single normal one", len(dirs))
	}
	want := mgl32.Vec3{0, -1, 1}.Normalize()
	if math.Abs(float64(dirs[0].Dot(want))-1) > 1e-5 {
		t.Errorf("the arm points %v, want the face normal %v", dirs[0], want)
	}
}

// E with nothing extrudable selected has to say so — a silent no-op is
// indistinguishable from a key that is not bound at all.
func TestExtrudeWithoutFacesReportsWhy(t *testing.T) {
	m := tiltedQuadDoc()
	withModelDoc(t, m)
	modelSelection.Mode = modelSelVertex
	modelSelection.Verts = []int{0}
	modelStatus = ""

	modelStartExtrude()

	if modelStatus == "" {
		t.Error("E in vertex mode said nothing")
	}
	if len(m.Faces) != 1 {
		t.Errorf("E in vertex mode changed the mesh: %d faces", len(m.Faces))
	}
}

func TestSelectingElsewhereDropsTheNormalAim(t *testing.T) {
	m := selectTiltedFace(t)
	modelStartExtrude()
	modelStartExtrude()

	// Any other face — one of the sides just created.
	modelSelection.Faces = []int{modelExtrudePending.Sides[0]}
	if _, ok := modelExtrudeNormalAxis(); ok {
		t.Error("the normal aim survived a selection change")
	}
	if dirs, _ := modelMoveAxes(); len(dirs) != 3 {
		t.Errorf("a different face got %d arms, want the world axes back", len(dirs))
	}
	_ = m
}

func TestNormalDragSnapsTheDepthNotTheComponents(t *testing.T) {
	m := selectTiltedFace(t)
	withGridSnap(t, true, 0.5)
	modelStartExtrude()
	modelStartExtrude()

	cap0 := append([]int(nil), m.Faces[modelSelection.Faces[0]].Verts...)
	before := append([]mgl32.Vec3(nil), m.Verts...)
	normal := mgl32.Vec3{0, -1, 1}.Normalize()

	mx, my := moveArmMouse(t, 0, modelGizmoArmLength(mgl32.Vec3{}))
	if !modelGizmoMouseDown(mx, my) {
		t.Fatal("the gizmo did not grab the normal arm")
	}
	dx, dy := moveArmMouse(t, 0, modelGizmoArmLength(mgl32.Vec3{})+1.1)
	modelGizmoMouseMove(dx, dy)
	modelGizmoMouseUp()

	for _, vi := range cap0 {
		offset := m.Verts[vi].Sub(before[vi])
		// Snapped along the normal: a whole number of steps of travel, and
		// still exactly on the normal rather than pulled onto a world grid line.
		if math.Abs(float64(offset.Len()-1)) > 1e-4 {
			t.Errorf("corner %d travelled %v, want the snapped depth 1", vi, offset.Len())
		}
		if math.Abs(float64(offset.Normalize().Dot(normal))-1) > 1e-4 {
			t.Errorf("corner %d left the normal: %v", vi, offset.Normalize())
		}
	}
}

func TestExtrudeAfterAnUndoIsAnExtrudeNotARetarget(t *testing.T) {
	m := selectTiltedFace(t)
	modelStartExtrude()
	faces := len(m.Faces)

	// Undo puts the face back exactly where it was, so "has it been dragged?"
	// alone would call the restored face a still-pending extrude and re-aim it.
	ModelUndo()
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{0}

	modelStartExtrude()
	if len(modelDoc.Faces) != faces {
		t.Errorf("E after an undo produced %d faces, want the extrude back at %d",
			len(modelDoc.Faces), faces)
	}
}
