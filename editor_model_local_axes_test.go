package main

import (
	"foundry/internal/model"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// withScaleFrame restores the world/local switch after a test.
func withScaleFrame(t *testing.T, local bool) {
	t.Helper()
	prev := modelScaleLocal
	modelScaleLocal = local
	t.Cleanup(func() { modelScaleLocal = prev })
}

// scaleArmMouse is scaleHandleMouse against whichever frame the gizmo is in.
func scaleArmMouse(t *testing.T, axis int, frac float32) (int32, int32) {
	t.Helper()
	origin, ok := modelGizmoOrigin()
	if !ok {
		t.Fatal("no gizmo origin")
	}
	basis := modelScaleDragBasis()
	w, h, _ := modelViewport()
	armLen := modelGizmoArmLength(origin) * modelScaleArmFrac
	sp, ok := projectToScreenIn(modelProjView(), origin.Add(basis[axis].Mul(armLen*frac)), w, h)
	if !ok {
		t.Fatal("scale handle is off screen")
	}
	return int32(sp.X()), int32(sp.Y())
}

func TestSecondCSwitchesTheScaleFrame(t *testing.T) {
	m := selectTiltedFace(t)
	withScaleFrame(t, false)
	_ = m

	// The first press only raises the tool.
	modelSetGizmoTool(modelToolMove)
	modelScaleToolKey()
	if modelGizmoTool != modelToolScale {
		t.Fatal("C did not raise the scale tool")
	}
	if modelScaleLocal {
		t.Error("C raised the tool and switched the frame in one press")
	}

	modelScaleToolKey()
	if !modelScaleLocal {
		t.Fatal("the second C did not switch to the local frame")
	}
	basis, local := modelScaleBasis()
	if !local {
		t.Fatal("the scale gizmo is still in the world frame")
	}
	// Axis 2 is the surface normal, and the other two lie in the face.
	want := mgl32.Vec3{0, -1, 1}.Normalize()
	if math.Abs(math.Abs(float64(basis[2].Dot(want)))-1) > 1e-5 {
		t.Errorf("local axis 2 is %v, want the face normal %v", basis[2], want)
	}
	for i := 0; i < 2; i++ {
		if math.Abs(float64(basis[i].Dot(basis[2]))) > 1e-5 {
			t.Errorf("local axis %d is not perpendicular to the normal: %v", i, basis[i])
		}
	}

	modelScaleToolKey()
	if modelScaleLocal {
		t.Error("a third C did not switch back to the world frame")
	}
}

func TestLocalBasisIsOrthonormal(t *testing.T) {
	selectTiltedFace(t)
	b, ok := modelLocalBasis()
	if !ok {
		t.Fatal("a face selection has no local frame")
	}
	for i, axis := range b {
		if math.Abs(float64(axis.Len())-1) > 1e-5 {
			t.Errorf("axis %d has length %v, want 1", i, axis.Len())
		}
	}
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 3; j++ {
			if math.Abs(float64(b[i].Dot(b[j]))) > 1e-5 {
				t.Errorf("axes %d and %d are not perpendicular: %v", i, j, b[i].Dot(b[j]))
			}
		}
	}
}

func TestVertexSelectionKeepsTheWorldFrame(t *testing.T) {
	m := tiltedQuadDoc()
	withModelDoc(t, m)
	withScaleFrame(t, true)
	modelSelection.Mode = modelSelVertex
	modelSelection.Verts = []int{0, 1}

	if _, ok := modelLocalBasis(); ok {
		t.Error("a vertex selection claimed a local frame; a point has no direction")
	}
	// The switch may be left on: the tool falls back rather than vanishing.
	basis, local := modelScaleBasis()
	if local || basis != worldBasis() {
		t.Errorf("got frame %v (local=%v), want the world axes", basis, local)
	}
}

func TestLocalScaleThickensAlongTheNormal(t *testing.T) {
	m := selectTiltedFace(t)
	withScaleFrame(t, true)
	modelGizmoTool = modelToolScale

	// Give the face some thickness to scale, and select the whole slab: a lone
	// cap is flat against its own normal, so scaling it that way has nothing to
	// stretch.
	modelStartExtrude()
	modelNudgeSelection(1, 1)
	all := make([]int, len(m.Faces))
	for i := range all {
		all[i] = i
	}
	modelSelection.Faces = all
	modelUVInvalidate()

	origin, _ := modelGizmoOrigin()
	normal := mgl32.Vec3{0, -1, 1}.Normalize()
	before := append([]mgl32.Vec3(nil), m.Verts...)

	// Pull the normal arm out to twice its distance from the centre.
	mx, my := scaleArmMouse(t, 2, 1)
	if !modelScaleMouseDown(mx, my) {
		t.Fatal("the scale gizmo did not grab the normal arm")
	}
	if modelGizmo.Axis != 2 {
		t.Fatalf("grabbed handle %d, want the normal one", modelGizmo.Axis)
	}
	dx, dy := scaleArmMouse(t, 2, 2)
	modelScaleMouseMove(dx, dy)
	modelGizmoMouseUp()

	for vi := range m.Verts {
		d0, d1 := before[vi].Sub(origin), m.Verts[vi].Sub(origin)
		// Doubled through the normal, untouched across the surface. The mouse
		// target is whole pixels, so the factor lands near two rather than on it.
		if math.Abs(float64(d1.Dot(normal)-d0.Dot(normal)*2)) > 0.05 {
			t.Errorf("vertex %d: depth %v -> %v, want it doubled", vi, d0.Dot(normal), d1.Dot(normal))
		}
		inPlane := d1.Sub(normal.Mul(d1.Dot(normal))).Sub(d0.Sub(normal.Mul(d0.Dot(normal))))
		if inPlane.Len() > 1e-4 {
			t.Errorf("vertex %d moved %v across the surface; a normal scale must not", vi, inPlane)
		}
	}
}

func TestWorldScaleIsUnchangedByTheNewFrameCode(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	withScaleFrame(t, false)
	top := selectTopFaceForScale(t, m)
	origin, _ := modelGizmoOrigin()
	before := append([]mgl32.Vec3(nil), m.Verts...)

	mx, my := scaleArmMouse(t, 0, 1)
	if !modelScaleMouseDown(mx, my) {
		t.Fatal("the scale gizmo did not grab the X handle")
	}
	dx, dy := scaleArmMouse(t, 0, 2)
	modelScaleMouseMove(dx, dy)
	modelGizmoMouseUp()

	for _, vi := range m.Faces[top].Verts {
		wantX := origin.X() + (before[vi].X()-origin.X())*2
		if math.Abs(float64(m.Verts[vi].X()-wantX)) > 0.05 {
			t.Errorf("corner %d x = %v, want about %v", vi, m.Verts[vi].X(), wantX)
		}
		if math.Abs(float64(m.Verts[vi].Y()-before[vi].Y())) > 1e-5 ||
			math.Abs(float64(m.Verts[vi].Z()-before[vi].Z())) > 1e-5 {
			t.Errorf("corner %d moved off the X axis: %v -> %v", vi, before[vi], m.Verts[vi])
		}
	}
}

// The frame a drag started in has to survive the drag: it is derived from the
// vertices the drag is moving, and an arm that swings out from under the cursor
// mid-gesture is unusable.
func TestScaleFrameIsFrozenForTheDurationOfADrag(t *testing.T) {
	selectTiltedFace(t)
	withScaleFrame(t, true)
	modelGizmoTool = modelToolScale

	mx, my := scaleArmMouse(t, 0, 1)
	if !modelScaleMouseDown(mx, my) {
		t.Fatal("the scale gizmo did not grab an arm")
	}
	start := modelGizmo.StartBasis

	// Flip the switch mid-drag; the drag must not notice.
	modelScaleLocal = false
	if got := modelScaleDragBasis(); got != start {
		t.Errorf("the drag frame changed mid-drag: %v -> %v", start, got)
	}
	modelGizmoMouseUp()
	if got := modelScaleDragBasis(); got != worldBasis() {
		t.Errorf("after the drag the frame is %v, want the world axes the switch now says", got)
	}
}
