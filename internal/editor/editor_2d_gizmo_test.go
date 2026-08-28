package editor

import (
	"foundry/internal/geom"
	"foundry/internal/platform"
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// setupGizmo2DView pins the map's pan/zoom and the window size so
// worldToScreen is deterministic: 20 px per world unit, centred on the origin,
// in an 800x600 viewport.
func setupGizmo2DView(t *testing.T) {
	t.Helper()
	prevW, prevH := platform.WinWidth, platform.WinHeight
	prevPan, prevZoom, prevPanel := Editor.Pan2D, Editor.Zoom2D, Editor.PanelWidth
	platform.WinWidth, platform.WinHeight = 800, 600
	Editor.Pan2D = mgl32.Vec2{0, 0}
	Editor.Zoom2D = 20
	Editor.PanelWidth = 360
	Editor.Mode = Mode2D
	Editor.BiomeLayerActive = false
	Editor.SpawnPlacementMode = false
	Editor.SurfaceEditing = false
	Editor.GizmoTool = editorToolMove
	Editor.Gizmo2DDragging = false
	Editor.Gizmo2DAxis = -1
	Editor.SnapToGrid = false
	Editor.StepSize = 0.25
	t.Cleanup(func() {
		platform.WinWidth, platform.WinHeight = prevW, prevH
		Editor.Pan2D, Editor.Zoom2D, Editor.PanelWidth = prevPan, prevZoom, prevPanel
	})
}

// gizmo2DTestSelection selects sector A's four corners, whose centre is (5,5).
func gizmo2DTestSelection(t *testing.T) *world.Chunk {
	t.Helper()
	ch := selectTestChunk(t)
	Editor.SelectedVertices = []vertexSel{
		vsel(ch, 0, 0, true), vsel(ch, 0, 1, true), vsel(ch, 0, 2, true), vsel(ch, 0, 3, true),
	}
	return ch
}

// screenAtWorld is the pixel a world XZ point lands on in the map.
func screenAtWorld(p mgl32.Vec2) (int32, int32) {
	sx, sy := editor2DView().worldToScreen(p.X(), p.Y())
	return int32(sx), int32(sy)
}

// With nothing selected there is no gizmo, so clicks must reach the picker
// rather than being swallowed by handles that are not on screen.
func TestGizmo2DNeedsSomethingToActOn(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)
	selectTestChunk(t)

	if _, ok := gizmo2DOrigin(); ok {
		t.Error("a gizmo appeared with nothing selected")
	}
	if pickGizmo2DHandle(400, 300) != -1 {
		t.Error("a click was grabbed with no gizmo on screen")
	}
}

// The biome layer owns every click while it is up, so the gizmo has to stand
// down rather than intercept the brush.
func TestGizmo2DStandsDownForTheBiomeLayer(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)
	gizmo2DTestSelection(t)

	if _, ok := gizmo2DOrigin(); !ok {
		t.Fatal("no gizmo for a vertex selection")
	}
	Editor.BiomeLayerActive = true
	defer func() { Editor.BiomeLayerActive = false }()
	if _, ok := gizmo2DOrigin(); ok {
		t.Error("the gizmo stayed up over the biome layer")
	}
}

// Scale spreads a selection about its own centre, which a lone spawn marker has
// no extent to do. Move and rotate both mean something for it: where it stands,
// and which way it looks.
func TestGizmo2DOffersMoveAndRotateForTheSpawnMarker(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)
	selectTestChunk(t)
	Editor.level.Spawn = &world.SpawnPoint{Position: mgl32.Vec3{2, 0, 3}}
	Editor.SpawnSelected = true

	for _, tool := range []editorGizmoTool{editorToolMove, editorToolRotate} {
		Editor.GizmoTool = tool
		if _, ok := gizmo2DOrigin(); !ok {
			t.Errorf("%v offered no gizmo for the selected spawn marker", tool)
		}
	}
	Editor.GizmoTool = editorToolScale
	if _, ok := gizmo2DOrigin(); ok {
		t.Error("scale offered a gizmo for a marker with no size")
	}
}

// The map has no pitch to drag, so turning the marker up here moves its yaw and
// leaves an aimed-up or aimed-down spawn as it was.
func TestGizmo2DRotateSpawnMovesOnlyTheYaw(t *testing.T) {
	defer saveEditorSelection()()
	s := &world.SpawnPoint{Position: mgl32.Vec3{2, 0, 3}, Rotation: mgl32.Vec3{0, 0.25, 0.4}}
	Editor.Gizmo2DStartSpawnRot = s.Rotation
	start := s.Position

	gizmo2DRotateSpawn(s, 0.5)

	if s.Position != start {
		t.Errorf("the marker moved while rotating: %v", s.Position)
	}
	// The map measures angles the other way round from a Y Euler rotation, so
	// a positive map delta subtracts from the yaw.
	if geom.Abs32(s.Rotation.Y()-(0.25-0.5)) > 1e-5 {
		t.Errorf("yaw: got %v, want %v", s.Rotation.Y(), 0.25-0.5)
	}
	if s.Rotation.X() != 0 || geom.Abs32(s.Rotation.Z()-0.4) > 1e-6 {
		t.Errorf("the map drag disturbed pitch or roll: %v", s.Rotation)
	}
}

// Each handle has to be grabbable where it is drawn: the arms along their
// length, the centre square at the middle.
func TestPickGizmo2DMoveHandles(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)
	gizmo2DTestSelection(t)

	origin, ok := gizmo2DOrigin()
	if !ok {
		t.Fatal("no gizmo origin")
	}
	armLen := gizmo2DArmLength()
	for _, axis := range gizmo2DAxes {
		mid := origin.Add(gizmo2DAxisDir(axis).Mul(armLen * 0.75))
		mx, my := screenAtWorld(mid)
		if got := pickGizmo2DHandle(mx, my); got != axis {
			t.Errorf("clicking the axis-%d arm picked %d", axis, got)
		}
	}

	// The centre is tested before the arms, since every arm starts there.
	mx, my := screenAtWorld(origin)
	if got := pickGizmo2DHandle(mx, my); got != gizmo2DPlaneAxis {
		t.Errorf("clicking the centre picked %d, want the free-move square", got)
	}

	// Well off the gizmo: nothing grabbed.
	if got := pickGizmo2DHandle(5, 5); got != -1 {
		t.Errorf("a click far from the gizmo picked %d", got)
	}
}

// The rotate ring is a circle on the map's projection, so it is grabbed at its
// radius and nowhere else — not at the centre it encloses.
func TestPickGizmo2DRotateRing(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)
	gizmo2DTestSelection(t)
	Editor.GizmoTool = editorToolRotate

	origin, _ := gizmo2DOrigin()
	on := origin.Add(mgl32.Vec2{gizmo2DArmLength(), 0})
	mx, my := screenAtWorld(on)
	if got := pickGizmo2DHandle(mx, my); got < 0 {
		t.Error("a click on the ring grabbed nothing")
	}
	cx, cy := screenAtWorld(origin)
	if got := pickGizmo2DHandle(cx, cy); got != -1 {
		t.Errorf("a click inside the ring picked %d, want nothing", got)
	}
}

func TestPickGizmo2DScaleHandles(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)
	gizmo2DTestSelection(t)
	Editor.GizmoTool = editorToolScale

	origin, _ := gizmo2DOrigin()
	cx, cy := screenAtWorld(origin)
	if got := pickGizmo2DHandle(cx, cy); got != modelScaleUniformAxis {
		t.Errorf("clicking the centre picked %d, want the uniform handle", got)
	}
	armLen := gizmo2DArmLength() * modelScaleArmFrac
	tip := origin.Add(gizmo2DAxisDir(2).Mul(armLen))
	mx, my := screenAtWorld(tip)
	if got := pickGizmo2DHandle(mx, my); got != 2 {
		t.Errorf("clicking the Z handle picked %d", got)
	}
}

// A move drag: grab the X arm, drag along it, and check the whole selection
// slid by that much in world X only, keeping its shape.
//
// The drag is driven through its own two halves — the delta the tool computes,
// and the write that applies it — rather than through editor2DGizmoMouseMove,
// which finishes by rebuilding the sector's geometry and so needs a GL context
// these tests have not got.
func TestGizmo2DMoveDragSlidesTheWholeSelection(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)
	ch := gizmo2DTestSelection(t)

	origin, _ := gizmo2DOrigin()
	grab := origin.Add(mgl32.Vec2{gizmo2DArmLength() * 0.75, 0})
	gx, gy := screenAtWorld(grab)
	if !editor2DGizmoMouseDown(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: gx, Y: gy}) {
		t.Fatal("a click on the X arm did not start a drag")
	}
	if !editor2DGizmoDragging() {
		t.Fatal("the drag did not register as in progress")
	}
	if Editor.Gizmo2DAxis != 0 {
		t.Fatalf("grabbed handle %d, want the X arm", Editor.Gizmo2DAxis)
	}
	if len(Editor.Gizmo2DStartPts) != 4 {
		t.Fatalf("the drag snapshotted %d corners, want 4", len(Editor.Gizmo2DStartPts))
	}

	// The move is relative to where the arm was grabbed, so dragging 3 units
	// further along X moves the selection by 3 — it does not jump onto the
	// cursor.
	delta := gizmo2DMoveDelta(grab.Add(mgl32.Vec2{3, 0}))
	nearVec2(t, "drag delta", delta, mgl32.Vec2{3, 0})
	writeSelectedVertexWorld(gizmo2DVertexTransform(func(p mgl32.Vec2) mgl32.Vec2 {
		return p.Add(delta)
	}))

	nearVec2(t, "corner 0", ch.Points[0], mgl32.Vec2{3, 0})
	nearVec2(t, "corner 2", ch.Points[2], mgl32.Vec2{13, 10})

	editor2DGizmoMouseUp()
	if editor2DGizmoDragging() {
		t.Error("the drag survived mouse up")
	}
}

// gizmo2DMoveDelta is where snapping lands: the *result* is put on the grid,
// not the offset, so a snapped drag reaches absolute grid multiples.
func TestGizmo2DMoveDeltaSnapsTheResult(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)

	Editor.Gizmo2DTool = editorToolMove
	Editor.Gizmo2DAxis = 0
	Editor.Gizmo2DCenter = mgl32.Vec2{1.1, 2}
	Editor.Gizmo2DStartS = 0

	Editor.SnapToGrid = false
	nearVec2(t, "unsnapped", gizmo2DMoveDelta(mgl32.Vec2{1.47, 2}), mgl32.Vec2{0.37, 0})

	Editor.SnapToGrid = true
	Editor.StepSize = 0.25
	// 1.1 + 0.37 = 1.47, which snaps to 1.5 — an offset of 0.4 from 1.1.
	got := gizmo2DMoveDelta(mgl32.Vec2{1.47, 2})
	if math.Abs(float64(got.X()-0.4)) > 1e-4 {
		t.Errorf("snapped delta X = %v, want 0.4 (landing on 1.5)", got.X())
	}
	if math.Abs(float64(got.Y())) > 1e-4 {
		t.Errorf("dragging X moved Z by %v", got.Y())
	}
}

// A free two-axis drag follows the cursor in both axes at once.
func TestGizmo2DMoveDeltaFollowsTheCursorOnThePlaneHandle(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)

	Editor.Gizmo2DTool = editorToolMove
	Editor.Gizmo2DAxis = gizmo2DPlaneAxis
	Editor.Gizmo2DCenter = mgl32.Vec2{5, 5}
	Editor.Gizmo2DStartWorld = mgl32.Vec2{6, 6}

	nearVec2(t, "plane delta", gizmo2DMoveDelta(mgl32.Vec2{9, 7}), mgl32.Vec2{3, 1})
}

func TestGizmo2DScaleVector(t *testing.T) {
	if got := gizmo2DScaleVector(modelScaleUniformAxis, 2); got != (mgl32.Vec2{2, 2}) {
		t.Errorf("uniform = %v, want (2,2)", got)
	}
	if got := gizmo2DScaleVector(0, 3); got != (mgl32.Vec2{3, 1}) {
		t.Errorf("X handle = %v, want (3,1)", got)
	}
	if got := gizmo2DScaleVector(2, 3); got != (mgl32.Vec2{1, 3}) {
		t.Errorf("Z handle = %v, want (1,3)", got)
	}
}

// Rotating on the map turns the group about its shared centre and leaves every
// height alone — the map has no Y, and a drag up here must not flatten a slope.
func TestGizmo2DRotateTurnsAboutTheCentreAndKeepsHeights(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)
	ch := gizmo2DTestSelection(t)
	ch.Sectors[0].Floor.VertexOffsets = []float32{1, 2, 3, 4}

	center, _ := selectionCenterXZ()
	Editor.Gizmo2DCenter = center
	Editor.Gizmo2DStartPts = selectedVerticesWorld()
	writeSelectedVertexWorld(gizmo2DVertexTransform(func(p mgl32.Vec2) mgl32.Vec2 {
		return rotateXZAbout(p, center, mgl32.DegToRad(90))
	}))

	nearVec2(t, "corner 0", ch.Points[0], mgl32.Vec2{10, 0})
	nearVec2(t, "corner 1", ch.Points[1], mgl32.Vec2{10, 10})
	for i, want := range []float32{1, 2, 3, 4} {
		if got := ch.Sectors[0].Floor.VertexOffsets[i]; math.Abs(float64(got-want)) > 1e-4 {
			t.Errorf("corner %d height offset = %v, want %v (unchanged)", i, got, want)
		}
	}
}

// Scaling on the map spreads the group about its centre. Nothing is resized —
// the corners simply end up further apart.
func TestGizmo2DScaleSpreadsAboutTheCentre(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)
	ch := gizmo2DTestSelection(t)

	center, _ := selectionCenterXZ()
	Editor.Gizmo2DCenter = center
	Editor.Gizmo2DStartPts = selectedVerticesWorld()
	s := gizmo2DScaleVector(modelScaleUniformAxis, 2)
	writeSelectedVertexWorld(gizmo2DVertexTransform(func(p mgl32.Vec2) mgl32.Vec2 {
		d := p.Sub(center)
		return center.Add(mgl32.Vec2{d.X() * s.X(), d.Y() * s.Y()})
	}))

	nearVec2(t, "corner 0", ch.Points[0], mgl32.Vec2{-5, -5})
	nearVec2(t, "corner 2", ch.Points[2], mgl32.Vec2{15, 15})
}

// A scale grab where the reference is ~0 has no ratio to be, so it is refused
// rather than allowed to fling the selection off on the first motion event.
func TestGizmo2DScaleRefusesAGrabAtTheCentre(t *testing.T) {
	defer saveEditorSelection()()
	setupGizmo2DView(t)
	gizmo2DTestSelection(t)
	Editor.GizmoTool = editorToolScale

	origin, _ := gizmo2DOrigin()
	cx, cy := screenAtWorld(origin)
	if !editor2DGizmoMouseDown(sdl.MouseButtonEvent{Button: sdl.ButtonLeft, X: cx, Y: cy}) {
		t.Fatal("a click on the uniform handle fell through to selection")
	}
	if editor2DGizmoDragging() {
		t.Error("a grab dead on the centre started a drag it cannot scale from")
	}
}
