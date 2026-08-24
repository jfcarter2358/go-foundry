package main

import (
	"foundry/internal/model"
	"foundry/internal/modeltest"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// scaleHandleMouse returns a screen point a fraction of the way along one
// scale arm. frac 1 is the grab box at the tip.
func scaleHandleMouse(t *testing.T, axis int, frac float32) (int32, int32) {
	t.Helper()
	origin, ok := modelGizmoOrigin()
	if !ok {
		t.Fatal("no gizmo origin")
	}
	w, h, _ := modelViewport()
	armLen := modelGizmoArmLength(origin) * modelScaleArmFrac
	p := origin.Add(gizmoAxes[axis].Mul(armLen * frac))
	sp, ok := projectToScreenIn(modelProjView(), p, w, h)
	if !ok {
		t.Fatal("scale handle is off screen")
	}
	return int32(sp.X()), int32(sp.Y())
}

// selectTopFace sets up a face selection with the scale tool active.
func selectTopFaceForScale(t *testing.T, m *model.ModelDoc) int {
	t.Helper()
	withModelViewport(t)
	withGridSnap(t, false, 0.1)
	modelGizmoTool = modelToolScale
	top := modeltest.CubeTopFace(t, m)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{top}
	return top
}

func TestScaleHandlesArePickable(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	selectTopFaceForScale(t, m)

	for axis := 0; axis < 3; axis++ {
		mx, my := scaleHandleMouse(t, axis, 1)
		if got := modelPickScaleHandle(mx, my); got != axis {
			t.Errorf("click on the %d arm's tip picked %d", axis, got)
		}
	}
}

func TestScaleCentreGrabsTheUniformHandle(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	selectTopFaceForScale(t, m)

	origin, _ := modelGizmoOrigin()
	w, h, _ := modelViewport()
	sp, _ := projectToScreenIn(modelProjView(), origin, w, h)

	// All three arms start here, so without the centre being tested first an
	// arm would always win a click on the middle of the widget.
	if got := modelPickScaleHandle(int32(sp.X()), int32(sp.Y())); got != modelScaleUniformAxis {
		t.Errorf("click at the centre picked %d, want the uniform handle", got)
	}
}

func TestScaleAlongOneAxisLeavesTheOthers(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	top := selectTopFaceForScale(t, m)
	origin, _ := modelGizmoOrigin()
	before := append([]mgl32.Vec3(nil), m.Verts...)

	// Grab the X tip and pull it out to twice its distance from the centre.
	mx, my := scaleHandleMouse(t, 0, 1)
	if !modelGizmoMouseDown(mx, my) {
		t.Fatal("the scale gizmo did not grab the X handle")
	}
	dx, dy := scaleHandleMouse(t, 0, 2)
	modelGizmoMouseMove(dx, dy)
	modelGizmoMouseUp()

	for _, vi := range m.Faces[top].Verts {
		// The drag target is quantised to whole pixels, so the factor lands
		// near two rather than exactly on it; a few percent is the resolution
		// the gesture actually has.
		wantX := origin.X() + (before[vi].X()-origin.X())*2
		if math.Abs(float64(m.Verts[vi].X()-wantX)) > 0.05 {
			t.Errorf("corner %d x = %v, want about %v", vi, m.Verts[vi].X(), wantX)
		}
		// Y and Z are untouched by an X-axis scale.
		if math.Abs(float64(m.Verts[vi].Y()-before[vi].Y())) > 1e-5 ||
			math.Abs(float64(m.Verts[vi].Z()-before[vi].Z())) > 1e-5 {
			t.Errorf("corner %d moved off the X axis: %v -> %v", vi, before[vi], m.Verts[vi])
		}
	}
	// Unselected geometry stays where it was.
	for _, vi := range []int{0, 1, 2, 3} {
		if !vec3Close(m.Verts[vi], before[vi], 1e-6) {
			t.Errorf("unselected vertex %d moved to %v", vi, m.Verts[vi])
		}
	}
}

func TestUniformScaleGrowsEveryAxisTogether(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	top := selectTopFaceForScale(t, m)
	origin, _ := modelGizmoOrigin()
	before := append([]mgl32.Vec3(nil), m.Verts...)

	// Start a little off centre — right at it there is no reference to be a
	// ratio of — then drag twice as far out.
	w, h, _ := modelViewport()
	sp, _ := projectToScreenIn(modelProjView(), origin, w, h)
	modelGizmoTool = modelToolScale
	if !modelGizmoMouseDown(int32(sp.X())+8, int32(sp.Y())) {
		t.Fatal("the scale gizmo did not grab the uniform handle")
	}
	if modelGizmo.Axis != modelScaleUniformAxis {
		t.Fatalf("grabbed handle %d, want the uniform one", modelGizmo.Axis)
	}
	modelGizmoMouseMove(int32(sp.X())+16, int32(sp.Y()))
	modelGizmoMouseUp()

	for _, vi := range m.Faces[top].Verts {
		wantOffset := before[vi].Sub(origin).Mul(2)
		got := m.Verts[vi].Sub(origin)
		if !vec3Close(got, wantOffset, 0.05) {
			t.Errorf("corner %d offset %v, want about %v", vi, got, wantOffset)
		}
	}
}

func TestScaleSnapsToTenths(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	top := selectTopFaceForScale(t, m)
	withGridSnap(t, true, 0.25)
	origin, _ := modelGizmoOrigin()
	v0 := m.Faces[top].Verts[0]
	startOffset := m.Verts[v0].Sub(origin).X()

	mx, my := scaleHandleMouse(t, 0, 1)
	if !modelGizmoMouseDown(mx, my) {
		t.Fatal("no grab")
	}
	// Deliberately not a round multiple.
	dx, dy := scaleHandleMouse(t, 0, 1.63)
	modelGizmoMouseMove(dx, dy)
	modelGizmoMouseUp()

	factor := float64(m.Verts[v0].Sub(origin).X() / startOffset)
	steps := factor / modelScaleSnapStep
	if math.Abs(steps-math.Round(steps)) > 1e-3 {
		t.Errorf("scaled by %.4f, which is not a multiple of %v", factor, modelScaleSnapStep)
	}
}

func TestScaleNeverMirrorsThroughZero(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	top := selectTopFaceForScale(t, m)
	origin, _ := modelGizmoOrigin()
	v0 := m.Faces[top].Verts[0]
	sign := m.Verts[v0].Sub(origin).X() > 0

	mx, my := scaleHandleMouse(t, 0, 1)
	if !modelGizmoMouseDown(mx, my) {
		t.Fatal("no grab")
	}
	// Drag well past the centre and out the other side, which is where a raw
	// ratio would go negative and turn the face inside out.
	dx, dy := scaleHandleMouse(t, 0, -3)
	modelGizmoMouseMove(dx, dy)
	modelGizmoMouseUp()

	if got := m.Verts[v0].Sub(origin).X() > 0; got != sign {
		t.Error("dragging through the centre mirrored the selection")
	}
}

func TestScaleLengthensABoneFromItsHead(t *testing.T) {
	m, bi := modeltest.SkinnedCube(t)
	withModelDoc(t, m)
	withModelViewport(t)
	withGridSnap(t, false, 0.1)
	modelGizmoTool = modelToolScale

	modelSelection.Mode = modelSelBone
	modelSelection.Bones = []int{bi}
	modelSelection.BoneJoint = modelBoneHead // pivot on the joint
	head := m.Bones[bi].Head
	length := m.BoneLength(bi)
	// A corner at the tail end of the bone. Vertex 0 would be the wrong probe:
	// it sits level with the head, and stretching a bone along its own axis
	// about the head leaves everything in that plane exactly where it is.
	probe := modeltest.CubeTopFace(t, m)
	probeVert := m.Faces[probe].Verts[0]
	vertBefore := m.Verts[probeVert]

	mx, my := scaleHandleMouse(t, 1, 1) // the bone runs up Y
	if !modelGizmoMouseDown(mx, my) {
		t.Fatal("the scale gizmo did not grab the Y handle")
	}
	dx, dy := scaleHandleMouse(t, 1, 2)
	modelGizmoMouseMove(dx, dy)
	modelGizmoMouseUp()

	if !vec3Close(m.Bones[bi].Head, head, 1e-4) {
		t.Errorf("head moved to %v; it was the pivot", m.Bones[bi].Head)
	}
	if got := m.BoneLength(bi); got <= length*1.5 {
		t.Errorf("bone length %v -> %v, want it roughly doubled", length, got)
	}
	if vec3Close(m.Verts[probeVert], vertBefore, 1e-4) {
		t.Errorf("the weighted mesh did not follow the scaled bone: %v", m.Verts[probeVert])
	}
	// It should have been carried along the bone, not sideways.
	if got := m.Verts[probeVert].Y(); got <= vertBefore.Y() {
		t.Errorf("probe vertex y %v -> %v, want it stretched away from the head",
			vertBefore.Y(), got)
	}
}

func TestSwitchingAwayCancelsAScaleDrag(t *testing.T) {
	m := model.NewCubeDoc()
	withModelDoc(t, m)
	selectTopFaceForScale(t, m)

	mx, my := scaleHandleMouse(t, 0, 1)
	if !modelGizmoMouseDown(mx, my) {
		t.Fatal("no grab")
	}
	modelSetGizmoTool(modelToolMove)

	if modelGizmoDragging() {
		t.Error("a scale was left in progress after switching tools")
	}
}
