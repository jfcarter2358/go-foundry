package editor

// editor_detail_test.go — detail mode, and the one promise it makes.
//
// Everything here is ultimately the same assertion from a different angle: the
// hull does not move. That is the whole feature — an author shapes what a wall
// looks like and the player still walks past the wall that was there — so it is
// asserted after every operation the mode offers rather than once.

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// detailTestBrush puts the editor in detail mode on the first brush of the
// shared fixture and returns it, along with a copy of its hull to compare
// against afterwards.
func detailTestBrush(t *testing.T) (*world.Brush, []mgl32.Vec3) {
	t.Helper()
	ch := brushTestLevel(t)
	editorEnterDetailMode(brushSel{chunkIdx: 0, brushIdx: 0})
	if !editorInDetailMode() {
		t.Fatal("editorEnterDetailMode did not enter detail mode")
	}
	b := &ch.Brushes[0]
	if b.Detail == nil {
		t.Fatal("entering detail mode did not seed a detail mesh")
	}
	return b, append([]mgl32.Vec3(nil), b.Verts...)
}

// selectDetailFace selects one face of the detail mesh, which is what every
// tool acts on.
func selectDetailFace(fi int) {
	setFaceSelection([]faceSel{brushDetailFaceSel(0, 0, fi)})
}

func TestEnteringDetailModeSeedsACopyOfTheHull(t *testing.T) {
	defer saveEditorState()()
	b, hull := detailTestBrush(t)

	if len(b.Detail.Verts) != len(hull) || len(b.Detail.Faces) != len(b.Faces) {
		t.Fatalf("seeded mesh is %d verts/%d faces, hull is %d/%d",
			len(b.Detail.Verts), len(b.Detail.Faces), len(hull), len(b.Faces))
	}
	for i := range hull {
		if !vecNear(b.Detail.Verts[i], hull[i], 1e-6) {
			t.Fatalf("seeded vertex %d is %v, hull's is %v", i, b.Detail.Verts[i], hull[i])
		}
	}
	// A copy, not an alias: editing one must not write the other.
	b.Detail.Verts[0] = mgl32.Vec3{99, 99, 99}
	if b.Verts[0] == b.Detail.Verts[0] {
		t.Error("the detail mesh shares its vertex storage with the hull")
	}
}

// TestDetailExtrudeLeavesTheHullAlone is the feature, stated once: the model
// editor's extrude — the one that duplicates the face and walls in the sides,
// which is exactly what edit mode is forbidden to do — runs on the detail mesh
// and the solid underneath is untouched.
func TestDetailExtrudeLeavesTheHullAlone(t *testing.T) {
	defer saveEditorState()()
	b, hull := detailTestBrush(t)

	selectDetailFace(boxFaceTop)
	facesBefore := len(b.Detail.Faces)
	detailExtrudeSelection(0.5)

	if len(b.Detail.Faces) <= facesBefore {
		t.Fatalf("extrude added no faces: %d before, %d after", facesBefore, len(b.Detail.Faces))
	}
	if len(b.Verts) != len(hull) {
		t.Fatalf("the hull gained vertices: %d, was %d", len(b.Verts), len(hull))
	}
	for i := range hull {
		if !vecNear(b.Verts[i], hull[i], 1e-6) {
			t.Errorf("hull vertex %d moved to %v, was %v", i, b.Verts[i], hull[i])
		}
	}
	// And the cap really did move, or the assertion above proves nothing.
	top := float32(-1e9)
	for _, v := range b.Detail.Verts {
		if v.Y() > top {
			top = v.Y()
		}
	}
	if top < 2.4 {
		t.Errorf("the extruded cap is at y=%.3f; the box's top was y=2 and it was pushed 0.5", top)
	}
}

func TestDetailSubdivideKeepsTextures(t *testing.T) {
	defer saveEditorState()()
	b, _ := detailTestBrush(t)
	b.Detail.Faces[boxFaceTop].Texture = "grass"
	b.Detail.Faces[boxFaceTop].Alpha = 0.5

	selectDetailFace(boxFaceTop)
	before := len(b.Detail.Faces)
	detailSubdivideSelection()

	if len(b.Detail.Faces) <= before {
		t.Fatalf("subdivide produced no new faces: %d, was %d", len(b.Detail.Faces), before)
	}
	grass := 0
	for i := range b.Detail.Faces {
		f := &b.Detail.Faces[i]
		if f.Texture != "grass" {
			continue
		}
		grass++
		if f.Alpha != 0.5 {
			t.Errorf("face %d kept the texture but not the alpha (%v)", i, f.Alpha)
		}
	}
	if grass < 4 {
		t.Errorf("only %d children kept the parent's texture; a quad splits into four", grass)
	}
	// Every other face is still concrete: a split must not repaint the mesh.
	for i := range b.Detail.Faces {
		if tex := b.Detail.Faces[i].Texture; tex != "grass" && tex != "concrete" {
			t.Errorf("face %d came back textured %q", i, tex)
		}
	}
}

func TestDetailDeleteRemovesOnlyDetailFaces(t *testing.T) {
	defer saveEditorState()()
	b, hull := detailTestBrush(t)

	selectDetailFace(boxFaceTop)
	detailDeleteSelection()

	if len(b.Detail.Faces) != 5 {
		t.Errorf("detail mesh has %d faces after deleting one of six", len(b.Detail.Faces))
	}
	if len(b.Faces) != 6 || len(b.Verts) != len(hull) {
		t.Errorf("the hull lost geometry: %d faces, %d verts", len(b.Faces), len(b.Verts))
	}
}

// TestMovingABrushCarriesItsDetail is the other half of "the two meshes are
// separate": separate does not mean unattached. An object-mode drag has to
// take the ledge with the wall.
func TestMovingABrushCarriesItsDetail(t *testing.T) {
	defer saveEditorState()()
	b, _ := detailTestBrush(t)
	// Something recognisable in the detail mesh, so the assertion is about a
	// vertex the hull does not also have.
	b.Detail.Verts[0] = mgl32.Vec3{0.25, 0.5, 0.75}
	editorExitDetailMode()

	setBrushSelection([]brushSel{{chunkIdx: 0, brushIdx: 0}})
	hullBefore := append([]mgl32.Vec3(nil), b.Verts...)
	detailBefore := append([]mgl32.Vec3(nil), b.Detail.Verts...)

	const dx = 3
	writeSelectedVertexWorld(func(_ int, _ vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		return cur.Add(mgl32.Vec3{dx, 0, 0})
	})

	for i := range hullBefore {
		if !vecNear(b.Verts[i], hullBefore[i].Add(mgl32.Vec3{dx, 0, 0}), 1e-5) {
			t.Fatalf("hull vertex %d did not move with the brush", i)
		}
	}
	for i := range detailBefore {
		want := detailBefore[i].Add(mgl32.Vec3{dx, 0, 0})
		if !vecNear(b.Detail.Verts[i], want, 1e-5) {
			t.Fatalf("detail vertex %d is at %v, want %v — the detail was left behind",
				i, b.Detail.Verts[i], want)
		}
	}
}

// TestDetailModeClicksReachOnlyTheDetailedBrush is what makes the mode safe to
// work in: while carving a surface, no click can reach a solid.
func TestDetailModeClicksReachOnlyTheDetailedBrush(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	editorEnterDetailMode(brushSel{chunkIdx: 0, brushIdx: 0})

	if m, detail, ok := pickMeshFor(0, 0, &ch.Brushes[0]); !ok || !detail || m != ch.Brushes[0].Detail {
		t.Errorf("the detailed brush offers %v (detail=%v, ok=%v); want its detail mesh",
			m, detail, ok)
	}
	if _, _, ok := pickMeshFor(0, 1, &ch.Brushes[1]); ok {
		t.Error("the neighbouring brush is still clickable in detail mode")
	}
}

// TestEditModeStillReachesTheHullOfADetailedBrush is the case that has to keep
// working or a detailed brush becomes uneditable: its hull is not uploaded any
// more, so a picker that only knows about GPU buffers would find nothing.
func TestEditModeStillReachesTheHullOfADetailedBrush(t *testing.T) {
	defer saveEditorState()()
	ch := brushTestLevel(t)
	editorEnterDetailMode(brushSel{chunkIdx: 0, brushIdx: 0})
	editorExitDetailMode()
	editorSetEditContext(editorEditMode)

	m, detail, ok := pickMeshFor(0, 0, &ch.Brushes[0])
	if !ok || detail || m != &ch.Brushes[0].BrushMesh {
		t.Fatalf("edit mode offers detail=%v ok=%v; want the hull", detail, ok)
	}
	// And the hull's faces still answer a ray, through the CPU fallback: the
	// box spans (0,0,0)–(2,2,2), so a ray straight down the middle from above
	// must meet its top face.
	origin, dir := mgl32.Vec3{1, 5, 1}, mgl32.Vec3{0, -1, 0}
	tHit := pickBrushFaceT(origin, dir, m, &m.Faces[boxFaceTop], mgl32.Ident4())
	if tHit > 1e6 {
		t.Error("the hull of a detailed brush cannot be clicked in edit mode")
	}
}

func TestLeavingDetailModeSelectsTheBrush(t *testing.T) {
	defer saveEditorState()()
	detailTestBrush(t)

	editorExitDetailMode()
	if editorInDetailMode() {
		t.Fatal("still in detail mode")
	}
	if len(Editor.SelectedBrushes) != 1 || Editor.SelectedBrushes[0].brushIdx != 0 {
		t.Errorf("came out with %v selected; want the brush that was being detailed",
			Editor.SelectedBrushes)
	}
}

// TestDetailKeysDoNotFireOutsideDetailMode guards the shared letters: E, B and
// Delete all mean something else in edit mode, and the detail handler is asked
// first.
func TestDetailKeysDoNotFireOutsideDetailMode(t *testing.T) {
	defer saveEditorState()()
	brushTestLevel(t)
	editorSetEditContext(editorEditMode)
	setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFaceTop)})

	for _, sc := range []sdl.Scancode{sdl.SCANCODE_E, sdl.SCANCODE_B, sdl.SCANCODE_DELETE} {
		if detailMeshHandleKey(sc, false) {
			t.Errorf("scancode %v was consumed by detail mode while in edit mode", sc)
		}
	}
}

// TestSelectUnitKeysStayInDetailMode guards the shortcut that would otherwise
// throw the mode away: 1, 2 and 3 take an author INTO edit mode from object
// mode, and the detail mesh has all three units of its own.
func TestSelectUnitKeysStayInDetailMode(t *testing.T) {
	defer saveEditorState()()
	detailTestBrush(t)

	editorEnterEditUnit(editorSelPoint)
	if !editorInDetailMode() {
		t.Fatal("pressing 1 left detail mode")
	}
	if Editor.SelMode != editorSelPoint {
		t.Errorf("select mode is %v, want Point", Editor.SelMode)
	}

	editorEnterEditUnit(editorSelFace)
	if !editorInDetailMode() || Editor.SelMode != editorSelFace {
		t.Errorf("pressing 3 left detail mode (%v) or missed the unit (%v)",
			Editor.EditContext, Editor.SelMode)
	}
}

// TestDetailSmoothRoundsTheSurfaceOnly covers the one tool that acts on the
// whole mesh rather than on a selection — and the one whose face mapping is a
// rule about the operation rather than about indices it hands back.
func TestDetailSmoothRoundsTheSurfaceOnly(t *testing.T) {
	defer saveEditorState()()
	b, hull := detailTestBrush(t)
	b.Detail.Faces[boxFaceTop].Texture = "grass"

	detailSmoothSelection()

	// A cube's six quads become one quad per corner: twenty-four.
	if len(b.Detail.Faces) != 24 {
		t.Errorf("smoothing a six-quad mesh gave %d faces, want 24", len(b.Detail.Faces))
	}
	if len(b.Faces) != 6 || len(b.Verts) != len(hull) {
		t.Fatalf("the hull changed: %d faces, %d verts", len(b.Faces), len(b.Verts))
	}
	// The top face's four children kept its texture; nothing else took it.
	grass := 0
	for i := range b.Detail.Faces {
		if b.Detail.Faces[i].Texture == "grass" {
			grass++
		}
	}
	if grass != 4 {
		t.Errorf("%d faces came back textured grass, want the top face's 4 children", grass)
	}
	// And it actually rounded: a cube's corners pull in toward the centre.
	for _, v := range b.Detail.Verts {
		if v.X() < -0.01 || v.Y() < -0.01 || v.Z() < -0.01 {
			t.Fatalf("vertex %v is outside the original box; smoothing should shrink it", v)
		}
	}
}
