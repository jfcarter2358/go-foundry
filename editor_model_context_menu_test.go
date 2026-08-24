package main

// The context menu is built as data (modelContextMenuItems) precisely so these
// tests can ask "what does a face selection offer?" without a GL context or an
// ImGui frame. What is asserted here is the rule the menu exists to enforce:
// the rows follow the selection, and clicking one does the same thing the
// panel's button would.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// withModelMenuState isolates the globals the menu reads and writes that the
// other model-editor helpers do not already cover.
func withModelMenuState(t *testing.T) {
	t.Helper()
	prevPress, prevWeight := modelRightPress, modelAssignWeight
	prevPose, prevClip, prevPlaying := modelPoseMode, modelPoseClip, modelPosePlaying
	modelRightPress = ctxGesture{}
	modelAssignWeight = 1
	t.Cleanup(func() {
		if modelPoseMode {
			modelSetPoseMode(false)
		}
		modelRightPress, modelAssignWeight = prevPress, prevWeight
		modelPoseMode, modelPoseClip, modelPosePlaying = prevPose, prevClip, prevPlaying
	})
}

// findMenuItem walks the tree depth-first for an exact label.
func findMenuItem(items []ctxMenuItem, label string) (ctxMenuItem, bool) {
	for _, it := range items {
		if it.Label == label {
			return it, true
		}
		if found, ok := findMenuItem(it.Sub, label); ok {
			return found, true
		}
	}
	return ctxMenuItem{}, false
}

// findMenuPrefix is findMenuItem for the rows whose labels carry a count —
// "Assign 4 point(s) to bone" and friends.
func findMenuPrefix(items []ctxMenuItem, prefix string) (ctxMenuItem, bool) {
	for _, it := range items {
		if strings.HasPrefix(it.Label, prefix) {
			return it, true
		}
		if found, ok := findMenuPrefix(it.Sub, prefix); ok {
			return found, true
		}
	}
	return ctxMenuItem{}, false
}

func mustMenuItem(t *testing.T, items []ctxMenuItem, label string) ctxMenuItem {
	t.Helper()
	it, ok := findMenuItem(items, label)
	if !ok {
		t.Fatalf("no %q row in the menu", label)
	}
	return it
}

func mustMenuPrefix(t *testing.T, items []ctxMenuItem, prefix string) ctxMenuItem {
	t.Helper()
	it, ok := findMenuPrefix(items, prefix)
	if !ok {
		t.Fatalf("no row starting %q in the menu", prefix)
	}
	return it
}

func TestContextMenuFaceModeOffersFaceOps(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{0}

	items := modelContextMenuItems()
	for _, label := range []string{"Extrude", "Subdivide", "Delete", "Smooth", "UV"} {
		if it := mustMenuItem(t, items, label); it.Disabled {
			t.Errorf("%q should be live with a face selected", label)
		}
	}
	if it := mustMenuItem(t, items, "Flip U"); it.Do == nil {
		t.Error("the UV submenu should carry clickable rows")
	}
}

func TestContextMenuGreysOutFaceOpsWithNothingSelected(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	modelSelection.Mode = modelSelFace
	modelClearMeshSelection()

	items := modelContextMenuItems()
	for _, label := range []string{"Extrude", "Subdivide", "Delete"} {
		if it := mustMenuItem(t, items, label); !it.Disabled {
			t.Errorf("%q should be greyed out with nothing selected", label)
		}
	}
	if it := mustMenuItem(t, items, "Deselect all"); !it.Disabled {
		t.Error("Deselect all should be greyed out with nothing selected")
	}
	// The rows that do not need a selection stay live, or the menu would be
	// dead on arrival after a click on empty space.
	if it := mustMenuItem(t, items, "Box"); it.Disabled {
		t.Error("Add > Box should be live whatever is selected")
	}
}

func TestContextMenuVertexModeDropsFaceOps(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	modelSelection.Mode = modelSelVertex
	modelSelection.Verts = []int{0, 1}

	items := modelContextMenuItems()
	if _, ok := findMenuItem(items, "Subdivide"); ok {
		t.Error("Subdivide is a face op and has no business in the vertex menu")
	}
	// Weights are the one thing a bare vertex selection can do, so they must be
	// offered — greyed out here, because this document has no armature.
	it := mustMenuPrefix(t, items, "Assign 2 point(s) to bone")
	if !it.Disabled {
		t.Error("assigning should be greyed out with no bones in the document")
	}
}

func TestContextMenuAssignsPointsToTheBoneClicked(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	spine := m.AddBone(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})
	m.RenameBone(spine, "spine")

	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{0}
	verts := modelSelectedMeshVerts()

	items := modelContextMenuItems()
	assign := mustMenuPrefix(t, items, "Assign 4 point(s) to bone")
	if assign.Disabled {
		t.Fatal("assigning should be live with points selected and a bone to bind to")
	}
	row := mustMenuItem(t, assign.Sub, "spine")
	row.Do()

	for _, vi := range verts {
		if w := m.WeightOf(vi, spine); w != 1 {
			t.Fatalf("vertex %d weight %v, want 1 after assigning from the menu", vi, w)
		}
	}
	if !ModelCanUndo() {
		t.Error("a menu assign must be undoable, exactly as the panel's button is")
	}
}

func TestContextMenuEdgeModeAssignsThroughItsVertices(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	arm := m.AddBone(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})

	e := m.Edges()[0]
	modelSelection.Mode = modelSelEdge
	modelSelection.Edges = []edgeKey{e}

	items := modelContextMenuItems()
	assign := mustMenuPrefix(t, items, "Assign 2 point(s) to bone")
	mustMenuItem(t, assign.Sub, m.Bones[arm].Name).Do()

	for _, vi := range []int{e.A, e.B} {
		if w := m.WeightOf(vi, arm); w != 1 {
			t.Fatalf("edge endpoint %d weight %v, want 1", vi, w)
		}
	}
}

func TestContextMenuBoneModeOffersArmatureActions(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	root := m.AddBone(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})
	m.RenameBone(root, "root")
	child := m.AddBone(mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 2, 0})
	m.RenameBone(child, "child")

	// A mesh selection made before switching to Bone mode is what the panel's
	// assign workflow relies on, and the menu has to see it too.
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{0}
	modelSetSelMode(modelSelBone)
	modelSelection.Bones = []int{child}
	modelSelection.BoneJoint = modelBoneTail

	items := modelContextMenuItems()
	for _, label := range []string{"Extrude bone", "Delete bone(s)", "Parent to", "Gizmo moves"} {
		if it := mustMenuItem(t, items, label); it.Disabled {
			t.Errorf("%q should be live with a bone selected", label)
		}
	}
	if _, ok := findMenuItem(items, "Subdivide"); ok {
		t.Error("mesh ops have no business in the bone menu")
	}

	parent := mustMenuItem(t, items, "Parent to")
	if _, ok := findMenuItem(parent.Sub, "child"); ok {
		t.Error("a bone should not be offered as its own parent")
	}
	mustMenuItem(t, parent.Sub, "(root)")
	mustMenuItem(t, parent.Sub, "root").Do()
	if m.Bones[child].Parent != root {
		t.Fatalf("parent is %d, want %d after picking it from the menu", m.Bones[child].Parent, root)
	}

	joint := mustMenuItem(t, items, "Gizmo moves")
	if tail, _ := findMenuItem(joint.Sub, "Tail"); !tail.Checked {
		t.Error("the joint the gizmo is on should be ticked")
	}

	assign := mustMenuPrefix(t, items, "Assign 4 point(s) to child")
	if assign.Disabled {
		t.Fatal("bone mode should still assign the mesh selection made before the switch")
	}
	assign.Do()
	if n := modelBoneWeightedCount(child); n != 4 {
		t.Fatalf("child moves %d points, want 4", n)
	}
}

func TestContextMenuPoseModeSwapsInKeyframeActions(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	m.AddBone(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})
	modelPoseClip = m.AddClip("walk")
	modelSetPoseMode(true)
	modelSelection.Bones = []int{0}

	items := modelContextMenuItems()
	for _, label := range []string{"Key pose", "Clear rotation", "Reset whole pose", "Leave pose mode"} {
		if it := mustMenuItem(t, items, label); it.Disabled {
			t.Errorf("%q should be live while posing a clip", label)
		}
	}
	// Rest-document edits are not on offer: the mesh on screen is a
	// deformation, and editing it would write somewhere the user cannot see.
	for _, label := range []string{"Delete bone(s)", "Extrude bone", "Subdivide"} {
		if _, ok := findMenuItem(items, label); ok {
			t.Errorf("%q should not be offered in pose mode", label)
		}
	}
	if mode := mustMenuItem(t, items, "Vertex"); !mode.Disabled {
		t.Error("the mesh selection modes are locked while posing")
	}
}

func TestContextMenuHeadingNamesTheSelection(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{0, 1}

	items := modelContextMenuItems()
	if len(items) == 0 || items[0].Label != "Face select — 2 faces" {
		t.Fatalf("heading is %q, want the selection spelled out", items[0].Label)
	}
	if items[0].Do != nil {
		t.Error("the heading is a caption, not a clickable row")
	}
}

func TestSelectAllFollowsTheMode(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)
	m.AddBone(mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})

	modelSelection.Mode = modelSelFace
	modelSelectAll()
	if len(modelSelection.Faces) != len(m.Faces) {
		t.Fatalf("selected %d of %d faces", len(modelSelection.Faces), len(m.Faces))
	}
	if len(modelSelection.Bones) != 0 {
		t.Error("select all in face mode should leave the armature alone")
	}

	modelSelection.Mode = modelSelBone
	modelSelectAll()
	if len(modelSelection.Bones) != len(m.Bones) {
		t.Fatalf("selected %d of %d bones", len(modelSelection.Bones), len(m.Bones))
	}
	if len(modelSelection.Faces) != len(m.Faces) {
		t.Error("select all in bone mode should leave the mesh selection alone")
	}

	modelDeselectAll()
	if len(modelSelection.Bones) != 0 {
		t.Error("deselect all should clear the bones in bone mode")
	}
	if len(modelSelection.Faces) != len(m.Faces) {
		t.Error("deselect all in bone mode should leave the mesh selection alone")
	}
}

// ---------- the gesture ----------

func TestRightClickOpensTheMenuAndRightDragDoesNot(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	withModelMenuState(t)
	modelSelection.Mode = modelSelFace
	modelSelection.Faces = []int{0}

	// A click that wobbles by a pixel is still a click.
	modelContextPressed(400, 300)
	modelContextMoved(401, 300)
	if !modelContextReleased(401, 300) {
		t.Fatal("a right-click that did not drag should open the menu")
	}
	if !modelRightPress.pending || modelRightPress.menuX != 401 {
		t.Fatalf("menu pending=%v at %d, want it queued at the cursor",
			modelRightPress.pending, modelRightPress.menuX)
	}

	modelRightPress.pending = false
	modelContextPressed(400, 300)
	modelContextMoved(460, 340)
	modelContextMoved(461, 341)
	if modelContextReleased(461, 341) {
		t.Fatal("a right-drag is a look, and must not end in a menu")
	}
	if modelRightPress.pending {
		t.Error("no menu should be queued after a drag")
	}
}

func TestRightClickSelectsUnderTheCursorOnlyWhenNothingIsSelected(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelViewport(t)
	withModelMenuState(t)
	modelSelection.Mode = modelSelFace

	// The face most squarely facing the camera, so its centre is a safe place
	// to aim a click.
	eye := modelCam.position()
	front, bestDot := -1, float32(0)
	for fi := range m.Faces {
		n := m.FaceNormal(fi)
		toEye := eye.Sub(m.FaceCenter(fi)).Normalize()
		if d := n.Dot(toEye); d > bestDot {
			front, bestDot = fi, d
		}
	}
	if front < 0 {
		t.Fatal("no face turned towards the camera")
	}
	w, h, ok := modelViewport()
	if !ok {
		t.Fatal("no viewport")
	}
	sp, ok := projectToScreenIn(modelProjView(), m.FaceCenter(front), w, h)
	if !ok {
		t.Fatal("the face centre is off screen")
	}
	mx, my := int32(sp.X()), int32(sp.Y())

	modelContextPressed(mx, my)
	modelContextReleased(mx, my)
	if len(modelSelection.Faces) != 1 || modelSelection.Faces[0] != front {
		t.Fatalf("selection is %v, want the face under the cursor (%d)", modelSelection.Faces, front)
	}

	// With something already selected the click is a menu and nothing else —
	// a right-click beside a careful multi-face selection must not lose it.
	modelSelection.Faces = []int{0, 1, 2}
	modelContextPressed(mx, my)
	modelContextReleased(mx, my)
	if len(modelSelection.Faces) != 3 {
		t.Fatalf("selection is %v, want the three faces left as they were", modelSelection.Faces)
	}
}

// The texture a model wears and the atlas it is unwrapped onto are document
// properties, so their rows stay live whatever is selected — including with
// nothing selected at all, which is when someone is most likely to be reaching
// for "give this model a texture".
func TestContextMenuDocumentRowsIgnoreSelection(t *testing.T) {
	m := NewCubeDoc()
	withModelDoc(t, m)
	withModelMenuState(t)

	for _, clear := range []bool{true, false} {
		modelSelection.Mode = modelSelFace
		if clear {
			modelClearMeshSelection()
		} else {
			modelSelection.Faces = []int{0}
		}
		items := modelContextMenuItems()
		for _, label := range []string{"Texture", "Unwrap && Paint..."} {
			if it := mustMenuItem(t, items, label); it.Disabled {
				t.Errorf("%q greyed out (selection cleared=%v); it is a document row", label, clear)
			}
		}
	}
}
