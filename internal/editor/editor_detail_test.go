package editor

// editor_detail_test.go — detail mode, and the one promise it makes.
//
// Everything here is ultimately the same assertion from a different angle: the
// hull does not move. That is the whole feature — an author shapes what a wall
// looks like and the player still walks past the wall that was there — so it is
// asserted after every operation the mode offers rather than once.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/gizmo"
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

// ---------- what a click in detail mode actually reaches ----------
//
// The tests above check pickMeshFor, which is the question "which mesh does
// this brush offer". They passed while the feature was still broken, because
// the click never got as far as asking: Editor3DHandleClick sent everything
// that was not EDIT mode down the object-mode path, so detail mode picked up
// whole brushes, no detail face could be selected, and every tool reported that
// nothing was. These go through the dispatch instead.

// clickAtCentre runs the 3D view's own left-click handler at the middle of the
// viewport, which is where setupPickCamera aims.
func clickAtCentre(mx, my int32) {
	Editor3DHandleClick(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: mx, Y: my})
}

// detailPickLevel puts a box in front of the camera with its detail mesh
// seeded, and enters detail mode on it.
func detailPickLevel(t *testing.T) (*world.Brush, int32, int32) {
	t.Helper()
	mx, my := setupPickCamera()
	ch := brushGeomLevel(t, world.NewBoxBrush("wall",
		mgl32.Vec3{0, 0, -6}, mgl32.Vec3{4, 4, 0.5}, "concrete"))
	b := &ch.Brushes[0]
	editorEnterDetailMode(brushSel{chunkIdx: 0, brushIdx: 0})
	if err := b.Load(); err != nil {
		t.Fatalf("loading the detailed brush: %v", err)
	}
	return b, mx, my
}

// TestAClickInDetailModeSelectsADetailFace is the regression: a plain click has
// to land on the detail mesh, because that is what every tool in the mode acts
// on.
func TestAClickInDetailModeSelectsADetailFace(t *testing.T) {
	defer saveEditorState()()
	_, mx, my := detailPickLevel(t)

	clickAtCentre(mx, my)

	if len(ws().SelectedBrushes) > 0 {
		t.Fatal("the click picked up a whole brush; detail mode took the object-mode path")
	}
	if len(ws().SelectedFaces) != 1 {
		t.Fatalf("selected %d faces, want 1", len(ws().SelectedFaces))
	}
	if sel := ws().SelectedFaces[0]; !sel.detail {
		t.Errorf("selected face %+v is on the hull, want the detail mesh", sel)
	}
	if !editorInDetailMode() {
		t.Error("the click left detail mode")
	}
}

// TestExtrudeAfterAClickInDetailMode is the user-visible loop end to end: click
// a face, press E, and the surface moves while the solid does not.
func TestExtrudeAfterAClickInDetailMode(t *testing.T) {
	defer saveEditorState()()
	b, mx, my := detailPickLevel(t)
	hull := append([]mgl32.Vec3(nil), b.Verts...)
	facesBefore := len(b.Detail.Faces)

	clickAtCentre(mx, my)
	if !detailMeshHandleKey(sdl.SCANCODE_E, false) {
		t.Fatal("E was not consumed by detail mode")
	}

	if len(b.Detail.Faces) <= facesBefore {
		t.Fatalf("E added no faces: %d, was %d — nothing was selected to extrude",
			len(b.Detail.Faces), facesBefore)
	}
	for i := range hull {
		if b.Verts[i] != hull[i] {
			t.Fatalf("hull corner %d moved to %v, was %v", i, b.Verts[i], hull[i])
		}
	}

	// And B subdivides what E left selected, which is the next thing a hand
	// reaches for.
	facesBefore = len(b.Detail.Faces)
	if !detailMeshHandleKey(sdl.SCANCODE_B, false) {
		t.Fatal("B was not consumed by detail mode")
	}
	if len(b.Detail.Faces) <= facesBefore {
		t.Errorf("B added no faces: %d, was %d", len(b.Detail.Faces), facesBefore)
	}
}

// TestDetailModeDrawsItsOwnHandles covers the same class of bug one layer over:
// point and edge mode draw no handles in object mode, and detail mode is not
// object mode.
func TestDetailModeDrawsItsOwnHandles(t *testing.T) {
	defer saveEditorState()()
	detailPickLevel(t)
	editorSetSelMode(editorSelPoint)

	if !editorInMeshContext() {
		t.Error("detail mode does not count as a mesh context, so its corner handles are never drawn")
	}
}

// TestAPointClickInDetailModeSelectsADetailVertex covers the other two branches
// of the same dispatch: point and edge mode each own the click outright, so
// each had to stop being sent to object mode along with the rest of detail
// mode.
func TestAPointClickInDetailModeSelectsADetailVertex(t *testing.T) {
	defer saveEditorState()()
	b, _, _ := detailPickLevel(t)
	// A corner of the box, projected to where the cursor has to be to grab it.
	// The near face spans x,y in ±4 at z=-5.5, so its top-right corner is the
	// one to aim at.
	editorSetSelMode(editorSelPoint)
	editorSetWireframe(true) // every handle in range is selectable, not only the visible ones

	corner := mgl32.Vec3{4, 4, -5.5}
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	screen, ok := projectToScreen(projView, corner)
	if !ok {
		t.Fatal("the corner does not project onto the viewport")
	}

	clickAtCentre(int32(screen.X()), int32(screen.Y()))

	if len(ws().SelectedVertices) == 0 {
		t.Fatal("no vertex was selected; the click went to the object-mode path")
	}
	sel := ws().SelectedVertices[0]
	if !sel.detail {
		t.Errorf("selected vertex %+v is on the hull, want the detail mesh", sel)
	}
	if pos, ok := vertexSelPos(sel); !ok || !vecNear(pos, corner, 0.001) {
		t.Errorf("selected the vertex at %v, want the corner at %v", pos, corner)
	}
	_ = b
}

// ---------- inset ----------

// TestDetailInsetLeavesABorder is the operation the detail mesh exists for as
// much as extrude does: a frame, in the face's own plane, that the next extrude
// turns into a recess.
func TestDetailInsetLeavesABorder(t *testing.T) {
	defer saveEditorState()()
	b, hull := detailTestBrush(t)
	selectDetailFace(boxFaceTop)
	before := len(b.Detail.Faces)

	detailInsetSelection(0.25)

	// One ring quad per edge of the face that was inset.
	if got := len(b.Detail.Faces) - before; got != 4 {
		t.Errorf("inset added %d faces, want 4 ring quads", got)
	}
	// The inner face stays selected — inset then extrude is the workflow.
	if len(ws().SelectedFaces) != 1 || ws().SelectedFaces[0].faceIdx != boxFaceTop {
		t.Errorf("selection after inset is %+v, want the inner face", ws().SelectedFaces)
	}
	// The border is uniform: the box spans ±1, so a 0.25 border leaves ±0.75.
	for _, vi := range b.Detail.Faces[boxFaceTop].Indices {
		p := b.Detail.Verts[vi]
		if !nearlyEqual(abs32(p.X()-1), 0.75, 0.001) && !nearlyEqual(abs32(p.X()), 0.75, 0.001) {
			t.Errorf("inner corner %v is not 0.25 in from the face's edge", p)
		}
	}
	// And the solid did not move.
	for i := range hull {
		if b.Verts[i] != hull[i] {
			t.Errorf("hull corner %d moved to %v, was %v", i, b.Verts[i], hull[i])
		}
	}
}

// TestDetailInsetThenExtrudeIsARecess is the pair, end to end.
func TestDetailInsetThenExtrudeIsARecess(t *testing.T) {
	defer saveEditorState()()
	b, _ := detailTestBrush(t)
	selectDetailFace(boxFaceTop)

	detailInsetSelection(0.25)
	detailExtrudeSelection(-0.5)

	// The box's top was y=2; the recess floor is half a unit below it.
	floor := float32(1e9)
	for _, v := range b.Detail.Verts {
		if v.Y() < floor {
			floor = v.Y()
		}
	}
	if !nearlyEqual(floor, 0, 0.001) {
		t.Errorf("the mesh's lowest point is y=%v; the box's own floor is y=0", floor)
	}
	sunk := 0
	for _, v := range b.Detail.Verts {
		if nearlyEqual(v.Y(), 1.5, 0.001) {
			sunk++
		}
	}
	if sunk != 4 {
		t.Errorf("%d corners ended up at y=1.5, want the recess floor's 4", sunk)
	}
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// ---------- subdivide shape ----------

// TestSubdivideMakesQuadrantsNotTriangles covers both meshes at once. Two
// assertions, and the second is the reason the first is safe:
//
//   - the pieces of a split quad are four QUADS, not a fan of triangles to the
//     centre — a fan cannot be selected, dragged or textured as the quarters of
//     a panel, which is what "subdivide this wall" means;
//   - each neighbour that shared a cut edge gains the midpoint as a corner of
//     its own. That is the T-junction the fan was invented to dodge, repaired
//     instead: a square face beside a subdivided one comes back a pentagon, and
//     its surface has not moved.
func TestSubdivideMakesQuadrantsNotTriangles(t *testing.T) {
	defer saveEditorState()()

	// The detail mesh.
	b, _ := detailTestBrush(t)
	selectDetailFace(boxFaceTop)
	detailSubdivideSelection()

	pieces := ws().SelectedFaces
	if len(pieces) != 4 {
		t.Fatalf("splitting a quad gave %d pieces, want 4 quadrants", len(pieces))
	}
	for _, sel := range pieces {
		f := faceSelFace(sel)
		if f == nil {
			t.Fatalf("piece %+v does not resolve", sel)
		}
		if n := len(f.Indices); n != 4 {
			t.Errorf("piece %d has %d corners, want a quad", sel.faceIdx, n)
		}
	}
	if len(b.Detail.Faces) != 9 {
		t.Errorf("the detail mesh has %d faces, want 5 untouched sides plus 4 quarters",
			len(b.Detail.Faces))
	}
	// The four sides that shared an edge with the top absorbed its midpoint.
	pentagons := 0
	for i := range b.Detail.Faces {
		if len(b.Detail.Faces[i].Indices) == 5 {
			pentagons++
		}
	}
	if pentagons != 4 {
		t.Errorf("%d neighbours took the new midpoints, want the 4 that share an edge "+
			"with the face that was split — the rest are T-junctions", pentagons)
	}

	// And the solid, in edit mode, where the same split must also leave a brush
	// something the compiler will still accept.
	ch := brushTestLevel(t)
	editorSetEditContext(editorEditMode)
	setFaceSelection([]faceSel{brushFaceSel(0, 0, boxFaceTop)})
	brushSubdivideSelectedFaces()

	hull := &ch.Brushes[0]
	if len(hull.Faces) != 9 {
		t.Errorf("the brush has %d faces, want 5 untouched sides plus 4 quarters", len(hull.Faces))
	}
	for _, sel := range ws().SelectedFaces {
		f := faceSelFace(sel)
		if f == nil || len(f.Indices) != 4 {
			t.Errorf("hull piece %+v is not a quad", sel)
		}
	}
	if !hull.IsConvex() {
		t.Error("subdividing a box's face made it non-convex")
	}
}

// ---------- the seams ----------

// queuedGizmoLines is how many line segments the overlays put in the shared
// buffer, which is the only observable a headless test has for something that
// is ultimately drawn. Each line is two endpoints of six floats.
func queuedGizmoLines(draw func()) int {
	gizmo.Buf = gizmo.Buf[:0]
	draw()
	return len(gizmo.Buf) / 12
}

// TestDetailWiresOutlineEveryFace is the overlay that makes the mode usable
// after a subdivide: four coplanar quarters of a wall are one flat surface to
// look at, and without the seams there is no telling what a click will hit.
func TestDetailWiresOutlineEveryFace(t *testing.T) {
	defer saveEditorState()()
	b, _ := detailTestBrush(t)

	// A box: twelve edges, each drawn once however many faces share it.
	if n := queuedGizmoLines(drawDetailMeshWires); n != 12 {
		t.Errorf("a box's detail mesh drew %d lines, want its 12 edges once each", n)
	}

	// Subdividing a face adds the seams that are the whole point: four
	// quarters bring four midpoints and four spokes, and the four neighbours
	// that absorbed a midpoint each gain a corner.
	selectDetailFace(boxFaceTop)
	detailSubdivideSelection()
	after := queuedGizmoLines(drawDetailMeshWires)
	if after <= 12 {
		t.Errorf("after subdividing, %d lines — the new seams are not drawn", after)
	}
	if len(b.Detail.Faces) != 9 {
		t.Fatalf("subdivide left %d faces", len(b.Detail.Faces))
	}
}

// TestDetailWiresOnlyInDetailMode keeps the overlay out of the ordinary
// viewport: every brush's edges at once is a hairball, and nothing outside
// detail mode is clicking on a detail face.
func TestDetailWiresOnlyInDetailMode(t *testing.T) {
	defer saveEditorState()()
	detailTestBrush(t)
	editorExitDetailMode()

	if n := queuedGizmoLines(drawDetailMeshWires); n != 0 {
		t.Errorf("drew %d lines in object mode", n)
	}
	editorSetEditContext(editorEditMode)
	if n := queuedGizmoLines(drawDetailMeshWires); n != 0 {
		t.Errorf("drew %d lines in edit mode", n)
	}
}

// TestDetailWiresAreBiasedTowardTheCamera is the difference between an overlay
// that works and one that is invisible: an edge between two coplanar faces
// lies exactly on the surface, so drawn at its own depth it loses to the
// triangle it bounds.
func TestDetailWiresAreBiasedTowardTheCamera(t *testing.T) {
	defer saveEditorState()()
	setupPickCamera() // eye at the origin, looking down -Z

	p := mgl32.Vec3{0, 0, -10}
	got := nudgeTowardCamera(p)
	if got == p {
		t.Fatal("the point was not moved at all")
	}
	if got.Z() <= p.Z() {
		t.Errorf("moved to %v, which is further from the eye than %v", got, p)
	}
	// A pixel's worth, not a visible float: well under a hundredth of the ten
	// units to the surface.
	if d := got.Sub(p).Len(); d > 0.1 {
		t.Errorf("moved %v units toward the eye; that would visibly float", d)
	}
}
