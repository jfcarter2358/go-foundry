package editor

import (
	"testing"

	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// uvGizmoTopFaceSelection selects the top of a box at the origin under the
// gizmo tests' camera, with a texture override on, UV mode on and tool in hand.
func uvGizmoTopFaceSelection(t *testing.T, tool editorGizmoTool) (mgl32.Mat4, *world.BrushMesh, *world.BrushFace) {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	projView := gizmo3DVertexSelection(t)
	Editor.SelectedVertices = nil
	setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFaceTop)})
	Editor.UVMode = true
	Editor.GizmoTool = tool
	Editor.GizmoDragUV = false
	f := faceSelFace(primaryFaceSel())
	f.TextureOverride = true
	f.OverrideTexture = "tiles"
	f.OverrideUVRotation = 0.4
	return projView, &Editor.level.Chunks[0].Brushes[0].BrushMesh, f
}

func uvScreen(t *testing.T, projView mgl32.Mat4, p mgl32.Vec3) (int32, int32) {
	t.Helper()
	s, ok := projectToScreen(projView, p)
	if !ok {
		t.Fatalf("%v did not project", p)
	}
	return int32(s.X()), int32(s.Y())
}

// uvGizmoDragPx drags from one pixel to another through the level editor's
// dispatch and returns the drag's frame at mouse-down.
func uvGizmoDragPx(t *testing.T, ax, ay, bx, by int32) uvGizmoFrame {
	t.Helper()
	down := sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: ax, Y: ay}
	if !editorGizmoMouseDown(down) {
		t.Fatal("the click missed the UV gizmo")
	}
	if !Editor.GizmoDragging || !Editor.GizmoDragUV {
		t.Fatal("the click did not start a UV drag")
	}
	fr, _ := uvGizmoFrameNow()
	fr.origin = Editor.GizmoDragOrigin
	editorGizmoMouseMove(bx, by)
	editorGizmoMouseUp()
	if Editor.GizmoDragging || Editor.GizmoDragUV {
		t.Fatal("the drag survived mouse up")
	}
	return fr
}

func uvNear2(a, b mgl32.Vec2) bool { return a.Sub(b).Len() < 2e-3 }

// Dragging the move square slides the override so the texel grabbed follows
// the cursor, and leaves the painted texture's own transform alone.
func TestUVGizmoMoveCarriesTheTexelUnderTheCursor(t *testing.T) {
	defer saveEditorSelection()()
	projView, m, f := uvGizmoTopFaceSelection(t, editorToolMove)
	before, _ := m.DrawnUVMap(f)

	fr, ok := uvGizmoFrameNow()
	if !ok {
		t.Fatal("no UV gizmo on a selected face")
	}
	ax, ay := uvScreen(t, projView, fr.origin)
	bx, by := uvScreen(t, projView, fr.origin.Add(mgl32.Vec3{0.4, 0, 0.3}))
	uvGizmoDragPx(t, ax, ay, bx, by)
	from, _ := uvGizmoPlaneHit(fr, ax, ay)
	to, _ := uvGizmoPlaneHit(fr, bx, by)

	after, _ := m.DrawnUVMap(f)
	if !uvNear2(after.At(to), before.At(from)) {
		t.Errorf("the grabbed texel %v is now at %v under the cursor", before.At(from), after.At(to))
	}
	if f.UVOffset != (mgl32.Vec2{}) || f.UVRotation != 0 {
		t.Errorf("the painted transform moved under an override: %v, %v", f.UVOffset, f.UVRotation)
	}
}

// A move arm slides the texture along its own axis only.
func TestUVGizmoMoveArmKeepsTheOtherAxis(t *testing.T) {
	defer saveEditorSelection()()
	projView, _, f := uvGizmoTopFaceSelection(t, editorToolMove)

	fr, ok := uvGizmoFrameNow()
	if !ok {
		t.Fatal("no UV gizmo on a selected face")
	}
	grab := fr.origin.Add(fr.u.Mul(gizmoArmLength(fr.origin) * 0.7))
	ax, ay := uvScreen(t, projView, grab)
	bx, by := uvScreen(t, projView, grab.Add(mgl32.Vec3{0.5, 0, 0.5}))
	uvGizmoDragPx(t, ax, ay, bx, by)

	if f.OverrideUVOffset.X() == 0 {
		t.Error("the U arm did not move the texture")
	}
	if f.OverrideUVOffset.Y() != 0 {
		t.Errorf("the U arm moved V to %v", f.OverrideUVOffset.Y())
	}
}

// Turning the ring turns the texture with the cursor, about the gizmo's centre.
func TestUVGizmoRotateTurnsTheTextureAboutTheCentre(t *testing.T) {
	defer saveEditorSelection()()
	projView, m, f := uvGizmoTopFaceSelection(t, editorToolRotate)
	before, _ := m.DrawnUVMap(f)
	startRot := f.OverrideUVRotation

	fr, ok := uvGizmoFrameNow()
	if !ok {
		t.Fatal("no UV gizmo on a selected face")
	}
	u, v := modelRingBasis(fr.normal)
	r := gizmoArmLength(fr.origin)
	ax, ay := uvScreen(t, projView, modelRingPoint(fr.origin, u, v, r, 0.3))
	bx, by := uvScreen(t, projView, modelRingPoint(fr.origin, u, v, r, 1.2))
	uvGizmoDragPx(t, ax, ay, bx, by)

	if f.OverrideUVRotation == startRot {
		t.Fatal("the ring did not turn the texture")
	}
	after, _ := m.DrawnUVMap(f)
	if !uvNear2(after.At(fr.origin), before.At(fr.origin)) {
		t.Errorf("the centre's texel moved from %v to %v", before.At(fr.origin), after.At(fr.origin))
	}
	a0, _ := editorRotationAngleAt(ax, ay, fr.origin, fr.normal)
	a1, _ := editorRotationAngleAt(bx, by, fr.origin, fr.normal)
	p := fr.origin.Add(u.Mul(0.3))
	moved := rotatePointAbout(p, fr.origin, fr.normal, a1-a0)
	if !uvNear2(after.At(moved), before.At(p)) {
		t.Errorf("the texel at %v went to %v, want it carried round to %v",
			before.At(p), after.At(moved), moved)
	}
}

// The centre box grows the texture both ways about the centre: dragging twice
// as far from it halves the UV scale.
func TestUVGizmoScaleCentreGrowsTheTextureUniformly(t *testing.T) {
	defer saveEditorSelection()()
	projView, m, f := uvGizmoTopFaceSelection(t, editorToolScale)
	before, _ := m.DrawnUVMap(f)

	fr, ok := uvGizmoFrameNow()
	if !ok {
		t.Fatal("no UV gizmo on a selected face")
	}
	ox, oy := uvScreen(t, projView, fr.origin)
	s0, _ := uvGizmoMeasure(fr, editorToolScale, uvHandleFree, ox+4, oy)
	s1, _ := uvGizmoMeasure(fr, editorToolScale, uvHandleFree, ox+8, oy)
	uvGizmoDragPx(t, ox+4, oy, ox+8, oy)

	if want := s0 / s1; !uvNear2(f.OverrideUVScale, mgl32.Vec2{want, want}) || want > 0.6 {
		t.Errorf("scale = %v, want both near %v", f.OverrideUVScale, want)
	}
	if f.OverrideUVRotation != 0.4 {
		t.Errorf("scaling turned the texture to %v", f.OverrideUVRotation)
	}
	after, _ := m.DrawnUVMap(f)
	if !uvNear2(after.At(fr.origin), before.At(fr.origin)) {
		t.Errorf("the centre's texel moved from %v to %v", before.At(fr.origin), after.At(fr.origin))
	}
}

// A scale arm stretches one axis only.
func TestUVGizmoScaleArmStretchesOneAxis(t *testing.T) {
	defer saveEditorSelection()()
	projView, _, f := uvGizmoTopFaceSelection(t, editorToolScale)

	fr, ok := uvGizmoFrameNow()
	if !ok {
		t.Fatal("no UV gizmo on a selected face")
	}
	arm := gizmoArmLength(fr.origin) * modelScaleArmFrac
	ax, ay := uvScreen(t, projView, fr.origin.Add(fr.sv.Mul(arm*0.6)))
	bx, by := uvScreen(t, projView, fr.origin.Add(fr.sv.Mul(arm*1.2)))
	uvGizmoDragPx(t, ax, ay, bx, by)

	if s := f.OverrideUVScale; s.X() != 1 || s.Y() < 0.4 || s.Y() > 0.6 {
		t.Errorf("scale = %v, want U untouched and V near half", s)
	}
}

// U toggles the mode and ends a drag; in UV mode the geometry gizmo is gone, so
// no click can move the face's corners.
func TestUVModeTogglesAndKeepsGeometryStill(t *testing.T) {
	defer saveEditorSelection()()
	projView, m, _ := uvGizmoTopFaceSelection(t, editorToolMove)
	Editor.UVMode = false

	editorToggleUVMode()
	if !Editor.UVMode {
		t.Fatal("toggling did not enter UV mode")
	}
	corners := append([]mgl32.Vec3(nil), m.Verts...)
	origin, _ := selectedFacesCenter()
	for _, axis := range gizmoAxes {
		x, y := uvScreen(t, projView, origin.Add(axis.Mul(gizmoArmLength(origin)*0.8)))
		if editorGizmoMouseDown(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: x, Y: y}) && !Editor.GizmoDragUV {
			t.Fatal("a geometry drag started in UV mode")
		}
		editorGizmoMouseMove(x+30, y+30)
		editorGizmoMouseUp()
	}
	for i := range corners {
		if m.Verts[i] != corners[i] {
			t.Fatalf("corner %d moved in UV mode", i)
		}
	}

	Editor.GizmoDragging, Editor.GizmoDragUV = true, true
	editorToggleUVMode()
	if Editor.UVMode || Editor.GizmoDragging || Editor.GizmoDragUV {
		t.Error("toggling out of UV mode left the mode or the drag on")
	}
}

// The entity editor runs the same tools against its own workspace: its
// selection, its drag state and its undo stack, leaving the level editor's
// alone. The window's camera is stood in for by the gizmo tests' view, since a
// test has no window to take one from.
func TestUVGizmoRotatesAFaceInTheEntityEditor(t *testing.T) {
	b := entityGizmoTestGeometry(t)
	projView := setupGizmoView()
	w := &entityWS
	prevRay, prevPV, prevCam, prevSize, prevYY := w.rayFn, w.projViewFn, w.camPosFn, w.viewSizeFn, w.projYYFn
	prevMode := w.UVMode
	t.Cleanup(func() {
		w.rayFn, w.projViewFn, w.camPosFn, w.viewSizeFn, w.projYYFn = prevRay, prevPV, prevCam, prevSize, prevYY
		w.UVMode = prevMode
	})
	w.rayFn, w.projViewFn, w.camPosFn, w.viewSizeFn, w.projYYFn = nil, nil, nil, nil, nil

	setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFaceTop)})
	f := &b.Faces[boxFaceTop]
	f.TextureOverride = true
	f.OverrideTexture = "tiles"
	entityToggleUVMode()
	entityGizmoTool = editorToolRotate
	levelDragging := Editor.GizmoDragging
	EntityDefClearUndoHistory()

	fr, ok := uvGizmoFrameNow()
	if !ok {
		t.Fatal("no UV gizmo on a face selected in the entity editor")
	}
	u, v := modelRingBasis(fr.normal)
	r := gizmoArmLength(fr.origin)
	ax, ay := uvScreen(t, projView, modelRingPoint(fr.origin, u, v, r, 0))
	bx, by := uvScreen(t, projView, modelRingPoint(fr.origin, u, v, r, 1))
	if !entityGizmoMouseDown(ax, ay) {
		t.Fatal("the click missed the UV ring")
	}
	if !entityGizmoDragging() || !entityWS.GizmoDragUV {
		t.Fatal("the UV drag did not start in the entity editor")
	}
	entityGizmoMouseMove(bx, by)
	entityGizmoMouseUp()

	if f.OverrideUVRotation == 0 {
		t.Error("the drag did not turn the override")
	}
	if entityWS.GizmoDragging || entityWS.GizmoDragUV || entityGizmoDragging() {
		t.Error("the drag survived mouse up")
	}
	if Editor.GizmoDragging != levelDragging {
		t.Error("the entity editor's drag changed the level editor's gizmo state")
	}
	if n := entityUndoStack.len(); n != 1 {
		t.Errorf("the drag pushed %d undo entries, want 1", n)
	}
}
