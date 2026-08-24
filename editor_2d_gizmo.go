package main

// editor_2d_gizmo.go — the top-down map's move/rotate/scale gizmo.
//
// The 3D viewport's gizmos work in three axes against a perspective camera;
// this one works in two against an orthographic one, which makes it a different
// widget rather than the same one seen from above. It shows the same three
// tools (Editor.GizmoTool, shared with the 3D view) acting on the same
// selection (Editor.SelectedVertices, likewise shared), so a set of corners
// picked in either view can be moved from either view.
//
// Everything here is XZ. The map has no Y to drag along, so a selected vertex
// keeps the floor or ceiling height it had: dragging on the map moves the
// footprint, and heights stay a 3D-view (or panel) concern.
//
// Picking is done in screen pixels rather than world units so a handle is the
// same size to aim at however far the map is zoomed out.

import (
	"foundry/internal/geom"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	// gizmo2DArmPx is the on-screen length of each axis arm.
	gizmo2DArmPx float32 = 60
	// gizmo2DTipHalfPx is half the on-screen size of the grab box at an arm's tip.
	gizmo2DTipHalfPx float32 = 5
	// gizmo2DPlaneHalfPx is half the on-screen size of the square at the centre
	// of the move gizmo, which drags in both axes at once. The map is flat, so
	// unlike the 3D viewport there is one obvious plane to offer and no reason
	// to make every move a two-step affair.
	gizmo2DPlaneHalfPx float32 = 8
	// gizmo2DPickPx is how close to a handle a click counts as grabbing it.
	gizmo2DPickPx float32 = 10
	// gizmo2DRingSegments is how finely the rotate ring is drawn. It is picked
	// analytically (distance from the centre), so this only affects smoothness.
	gizmo2DRingSegments = 64
)

// gizmo2DPlaneAxis is the Axis value recorded for a free two-axis move. Like
// modelScaleUniformAxis it is deliberately out of range for gizmoAxes, and
// distinct from it so the two centre handles cannot be confused.
const gizmo2DPlaneAxis = 4

// gizmo2DAxes are the two world axes the map's gizmo works in, as indices into
// gizmoAxes and gizmoAxisColors — X and Z, skipping the Y the map cannot show.
var gizmo2DAxes = [2]int{0, 2}

// gizmo2DAxisDir returns the XZ direction of one of gizmo2DAxes' entries.
func gizmo2DAxisDir(axis int) mgl32.Vec2 {
	if axis == 2 {
		return mgl32.Vec2{0, 1}
	}
	return mgl32.Vec2{1, 0}
}

// ---------- what it acts on ----------

// gizmo2DSuppressed reports whether the map's gizmo should be inert (and so
// also undrawn) because the next click already means something else: an armed
// placement, or the biome layer, which owns every click while it is up.
func gizmo2DSuppressed() bool {
	return Editor.Mode != Mode2D || Editor.BiomeLayerActive || gizmoSuppressed()
}

// gizmo2DOrigin returns where the gizmo sits, or false when there is nothing
// for it to act on. Scale needs a set of points with an extent to spread, or
// something with a size of its own; the spawn marker has neither, but it does
// have a facing, so it moves and turns.
func gizmo2DOrigin() (mgl32.Vec2, bool) {
	if gizmo2DSuppressed() || !editorToolActsOnSelection() {
		return mgl32.Vec2{}, false
	}
	return selectionCenterXZ()
}

// gizmo2DArmLength returns the world length of an arm at the current zoom.
func gizmo2DArmLength() float32 {
	return screenToWorldSize(gizmo2DArmPx, editor2DOrtho())
}

// ---------- picking ----------

// pickGizmo2DHandle returns the handle a click at (mx, my) grabs, or -1: an
// entry of gizmo2DAxes for an arm, gizmo2DPlaneAxis for the move gizmo's
// centre square, and modelScaleUniformAxis for the scale gizmo's centre.
func pickGizmo2DHandle(mx, my int32) int {
	origin, ok := gizmo2DOrigin()
	if !ok {
		return -1
	}
	v := editor2DView()
	ox, oy := v.worldToScreen(origin.X(), origin.Y())
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	centreD := cursor.Sub(mgl32.Vec2{ox, oy}).Len()

	if Editor.GizmoTool == editorToolRotate {
		// The ring is a circle on screen — the map's projection has no
		// foreshortening — so "near the ring" is just "near that radius".
		if geom.Abs32(centreD-gizmo2DArmPx) <= gizmo2DPickPx {
			return gizmo2DAxes[0]
		}
		return -1
	}

	// Both other tools put a handle at the centre, and every arm starts there,
	// so the centre is tested first or it could never be hit.
	armFrac := float32(1)
	if Editor.GizmoTool == editorToolScale {
		if centreD <= modelScaleUniformPx {
			return modelScaleUniformAxis
		}
		armFrac = modelScaleArmFrac
	} else if centreD <= gizmo2DPlaneHalfPx {
		return gizmo2DPlaneAxis
	}

	armLen := gizmo2DArmLength() * armFrac
	best := -1
	bestD := gizmo2DPickPx
	for _, axis := range gizmo2DAxes {
		tip := origin.Add(gizmo2DAxisDir(axis).Mul(armLen))
		tx, ty := v.worldToScreen(tip.X(), tip.Y())
		if d := distToSegment2D(cursor.X(), cursor.Y(), ox, oy, tx, ty); d <= bestD {
			bestD = d
			best = axis
		}
	}
	return best
}

// ---------- the drag ----------

// editor2DGizmoDragging reports whether a map gizmo drag is in progress. A drag
// owns the mouse until release.
func editor2DGizmoDragging() bool { return Editor.Gizmo2DDragging }

// editor2DGizmoMouseDown starts a drag if the click grabbed a handle. Returns
// true when the click was consumed, so it does not also re-select — including
// when the grab is refused, for the reason translateGizmoMouseDown documents.
func editor2DGizmoMouseDown(t sdl.MouseButtonEvent) bool {
	if t.Button != sdl.ButtonLeft {
		return false
	}
	handle := pickGizmo2DHandle(t.X, t.Y)
	if handle < 0 {
		return false
	}
	origin, ok := gizmo2DOrigin()
	if !ok {
		return true
	}
	wx, wz := screenToWorld(t.X, t.Y)
	cursor := mgl32.Vec2{wx, wz}

	start, ok := gizmo2DReference(cursor, origin, handle)
	if !ok {
		return true
	}

	// One undo entry for the whole drag, as the 3D gizmos do.
	EditorPushUndo()
	Editor.Gizmo2DDragging = true
	Editor.Gizmo2DTool = Editor.GizmoTool
	Editor.Gizmo2DAxis = handle
	Editor.Gizmo2DCenter = origin
	Editor.Gizmo2DStartS = start
	Editor.Gizmo2DStartWorld = cursor
	Editor.Gizmo2DStartPts = selectedVerticesWorld()
	if s := activeGizmoSpawn(); s != nil {
		Editor.Gizmo2DStartSpawn = s.Position
		Editor.Gizmo2DStartSpawnRot = s.Rotation
	}
	if e := activeGizmoEntity(); e != nil {
		Editor.Gizmo2DStartEntity = e.Position
		Editor.Gizmo2DStartEntityRot = e.Rotation
		Editor.Gizmo2DStartEntityScale = e.EffectiveScale()
	}
	return true
}

// gizmo2DReference returns the measure the drag is an offset from, in the unit
// its tool works in: distance along the axis for move, an angle for rotate, and
// the reference a factor is a ratio of for scale. Free two-axis moves have no
// single measure and carry theirs in Gizmo2DStartWorld instead.
func gizmo2DReference(cursor, origin mgl32.Vec2, handle int) (float32, bool) {
	switch Editor.GizmoTool {
	case editorToolRotate:
		rel := cursor.Sub(origin)
		if rel.Len() < 1e-6 {
			return 0, false // dead centre: no angle to speak of
		}
		return float32(math.Atan2(float64(rel.Y()), float64(rel.X()))), true

	case editorToolScale:
		ref := gizmo2DScaleReference(cursor, origin, handle)
		// Too near the centre to be a ratio of anything: the first motion
		// event would multiply by a huge number.
		if geom.Abs32(ref) < scaleRefMin {
			return 0, false
		}
		return ref, true
	}
	if handle == gizmo2DPlaneAxis {
		return 0, true
	}
	return cursor.Sub(origin).Dot(gizmo2DAxisDir(handle)), true
}

// gizmo2DScaleReference is the cursor's position along a scale handle's axis,
// or its distance from the centre for the uniform handle.
func gizmo2DScaleReference(cursor, origin mgl32.Vec2, handle int) float32 {
	rel := cursor.Sub(origin)
	if handle == modelScaleUniformAxis {
		return rel.Len()
	}
	return rel.Dot(gizmo2DAxisDir(handle))
}

// editor2DGizmoMouseUp ends a drag.
func editor2DGizmoMouseUp() {
	Editor.Gizmo2DDragging = false
	Editor.Gizmo2DAxis = -1
	Editor.Gizmo2DStartPts = nil
}

// editor2DGizmoMouseMove applies the current cursor position to the drag.
// Every tool works from the mouse-down snapshot rather than from the previous
// frame, so a long drag stays exact and snapping lands on absolute stops.
func editor2DGizmoMouseMove(mx, my int32) {
	if !Editor.Gizmo2DDragging {
		return
	}
	wx, wz := screenToWorld(mx, my)
	cursor := mgl32.Vec2{wx, wz}
	origin := Editor.Gizmo2DCenter

	switch Editor.Gizmo2DTool {
	case editorToolRotate:
		rel := cursor.Sub(origin)
		if rel.Len() < 1e-6 {
			return
		}
		angle := float32(math.Atan2(float64(rel.Y()), float64(rel.X())))
		delta := geom.WrapAngle(angle - Editor.Gizmo2DStartS)
		if Editor.SnapToGrid {
			delta = snapValue(delta, mgl32.DegToRad(modelRotateSnapDeg))
		}
		if e := activeGizmoEntity(); e != nil {
			gizmo2DRotateEntity(e, delta)
			return
		}
		if s := activeGizmoSpawn(); s != nil {
			gizmo2DRotateSpawn(s, delta)
			return
		}
		gizmo2DApply(func(p mgl32.Vec2) mgl32.Vec2 { return rotateXZAbout(p, origin, delta) })

	case editorToolScale:
		f := scaleFactorBetween(Editor.Gizmo2DStartS,
			gizmo2DScaleReference(cursor, origin, Editor.Gizmo2DAxis))
		s := gizmo2DScaleVector(Editor.Gizmo2DAxis, f)
		if e := activeGizmoEntity(); e != nil {
			gizmo2DScaleEntity(e, s)
			return
		}
		gizmo2DApply(func(p mgl32.Vec2) mgl32.Vec2 {
			d := p.Sub(origin)
			return origin.Add(mgl32.Vec2{d.X() * s.X(), d.Y() * s.Y()})
		})

	default:
		delta := gizmo2DMoveDelta(cursor)
		gizmo2DApply(func(p mgl32.Vec2) mgl32.Vec2 { return p.Add(delta) })
	}
}

// gizmo2DMoveDelta is the XZ offset a move drag applies, snapped so the moved
// selection lands on absolute grid multiples rather than the offset doing so.
func gizmo2DMoveDelta(cursor mgl32.Vec2) mgl32.Vec2 {
	origin := Editor.Gizmo2DCenter
	if Editor.Gizmo2DAxis == gizmo2DPlaneAxis {
		want := origin.Add(cursor.Sub(Editor.Gizmo2DStartWorld))
		return mgl32.Vec2{gizmoSnap(want.X()), gizmoSnap(want.Y())}.Sub(origin)
	}
	dir := gizmo2DAxisDir(Editor.Gizmo2DAxis)
	s := cursor.Sub(origin).Dot(dir)
	want := origin.Add(dir.Mul(s - Editor.Gizmo2DStartS))
	return mgl32.Vec2{gizmoSnap(want.X()), gizmoSnap(want.Y())}.Sub(origin)
}

// gizmo2DScaleVector spreads a factor over the axes a scale handle drives.
func gizmo2DScaleVector(handle int, f float32) mgl32.Vec2 {
	if handle == modelScaleUniformAxis {
		return mgl32.Vec2{f, f}
	}
	out := mgl32.Vec2{1, 1}
	if handle == 2 {
		out[1] = f
	} else {
		out[0] = f
	}
	return out
}

// gizmo2DApply pushes an XZ transform through to whatever the gizmo is acting
// on. Heights are carried through untouched: the map cannot express them, so a
// drag up here must not quietly flatten a sloped floor.
func gizmo2DApply(fn func(mgl32.Vec2) mgl32.Vec2) {
	if len(Editor.SelectedVertices) > 0 {
		setSelectedVertexWorld(gizmo2DVertexTransform(fn))
		return
	}
	if e := activeGizmoEntity(); e != nil {
		p := Editor.Gizmo2DStartEntity
		xz := fn(mgl32.Vec2{p.X(), p.Z()})
		e.Position = mgl32.Vec3{xz.X(), p.Y(), xz.Y()}
		return
	}
	if s := activeGizmoSpawn(); s != nil {
		p := Editor.Gizmo2DStartSpawn
		xz := fn(mgl32.Vec2{p.X(), p.Z()})
		s.Position = mgl32.Vec3{xz.X(), p.Y(), xz.Y()}
	}
}

// gizmo2DRotateEntity turns the entity in place by a map-space angle.
//
// The map measures angles counter-clockwise from +X through +Z, while a Y Euler
// rotation carries +X toward -Z — so the entity's yaw takes the negated delta.
// It is the same sign the spawn marker's rotation is built with.
func gizmo2DRotateEntity(e *world.Entity, delta float32) {
	r := Editor.Gizmo2DStartEntityRot
	e.Rotation = mgl32.Vec3{r.X(), world.WrapAngle(r.Y() - delta), r.Z()}
}

// gizmo2DRotateSpawn swings the spawn marker's facing by a map-space angle.
// Only the yaw moves: the map has no pitch to drag, so a spawn aimed up or down
// keeps the pitch it was given in the 3D view or the panel. The sign is the
// entity's, and for the same reason gizmo2DRotateEntity gives.
func gizmo2DRotateSpawn(s *world.SpawnPoint, delta float32) {
	r := Editor.Gizmo2DStartSpawnRot
	s.Rotation = mgl32.Vec3{r.X(), world.WrapAngle(r.Y() - delta), r.Z()}
}

// gizmo2DScaleEntity resizes the entity in place. The map can only reach X and
// Z; Y is carried through untouched, for the same reason heights are elsewhere
// up here — a top-down drag must not silently flatten something.
func gizmo2DScaleEntity(e *world.Entity, s mgl32.Vec2) {
	start := Editor.Gizmo2DStartEntityScale
	e.Scale = mgl32.Vec3{
		world.EntityScaleClamp(start.X() * s.X()),
		start.Y(),
		world.EntityScaleClamp(start.Z() * s.Y()),
	}
}

// gizmo2DVertexTransform lifts a flat XZ transform into the position map the
// vertex writer wants, carrying each vertex's mouse-down height through
// untouched.
func gizmo2DVertexTransform(fn func(mgl32.Vec2) mgl32.Vec2) func(int, vertexSel, mgl32.Vec3) mgl32.Vec3 {
	start := Editor.Gizmo2DStartPts
	return func(i int, sel vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
		if i >= len(start) {
			return cur
		}
		p := start[i]
		xz := fn(mgl32.Vec2{p.X(), p.Z()})
		return mgl32.Vec3{xz.X(), p.Y(), xz.Y()}
	}
}

// ---------- drawing ----------

// editor2DCursor returns the cursor's position in the coordinates the map is
// drawn in, or a point far off-screen when the pointer is somewhere a click
// would not reach the map.
//
// It reads the position the map's own event handlers recorded rather than
// asking SDL: the map can be drawn into the pop-out window (editor_2d_window.go)
// whose coordinates are its own, and SDL reports the cursor relative to
// whichever window has focus. LastMouse2D is always in the right frame, because
// it was set by the window the events actually arrived at.
func editor2DCursor() (int32, int32) {
	off := int32(math.MinInt32 / 2)
	if imgui.CurrentIO().WantCaptureMouse() && !editor2DGizmoDragging() {
		return off, off
	}
	x, y := Editor.LastMouse2D[0], Editor.LastMouse2D[1]
	viewW, _ := editor2DViewportSize()
	// Over the side panel: the gizmo is not being aimed at, but a drag already
	// in progress keeps its handle lit when the cursor crosses the panel.
	if float32(x) >= viewW-Editor.PanelWidth && !editor2DGizmoDragging() {
		return off, off
	}
	return x, y
}

// Editor2DRenderGizmo draws the map's gizmo for the current selection, flat at
// y=0 so it reads against the map whatever heights the selection sits at.
func Editor2DRenderGizmo(mx, my int32) {
	origin, ok := gizmo2DOrigin()
	if !ok {
		return
	}
	hover := -1
	if Editor.Gizmo2DDragging {
		hover = Editor.Gizmo2DAxis
	} else {
		hover = pickGizmo2DHandle(mx, my)
	}
	yellow := mgl32.Vec3{1, 1, 0}
	neutral := mgl32.Vec3{0.85, 0.85, 0.9}

	if Editor.GizmoTool == editorToolRotate {
		col := gizmoAxisColors[1] // the Y axis it turns about — green, as in 3D
		if hover >= 0 {
			col = yellow
		}
		r := gizmo2DArmLength()
		prev := origin.Add(mgl32.Vec2{r, 0})
		for i := 1; i <= gizmo2DRingSegments; i++ {
			a := float64(i) / gizmo2DRingSegments * 2 * math.Pi
			p := origin.Add(mgl32.Vec2{r * float32(math.Cos(a)), r * float32(math.Sin(a))})
			gizmo2DLine(prev, p, col)
			prev = p
		}
		gizmo2DBox(origin, gizmo2DTipHalfPx, neutral)
		return
	}

	armFrac, tipPx := float32(1), gizmo2DTipHalfPx
	if Editor.GizmoTool == editorToolScale {
		armFrac, tipPx = modelScaleArmFrac, gizmo2DTipHalfPx*1.7
	}
	armLen := gizmo2DArmLength() * armFrac
	for _, axis := range gizmo2DAxes {
		col := gizmoAxisColors[axis]
		if axis == hover {
			col = yellow
		}
		tip := origin.Add(gizmo2DAxisDir(axis).Mul(armLen))
		gizmo2DLine(origin, tip, col)
		gizmo2DBox(tip, tipPx, col)
	}

	centreCol := neutral
	if hover == gizmo2DPlaneAxis || hover == modelScaleUniformAxis {
		centreCol = yellow
	}
	half := gizmo2DPlaneHalfPx
	if Editor.GizmoTool == editorToolScale {
		half = gizmo2DTipHalfPx * 1.2
	}
	gizmo2DBox(origin, half, centreCol)
}

// gizmo2DLine queues a map-space line at y=0.
func gizmo2DLine(a, b mgl32.Vec2, col mgl32.Vec3) {
	gizmo.GizmoLine(mgl32.Vec3{a.X(), 0, a.Y()}, mgl32.Vec3{b.X(), 0, b.Y()}, col)
}

// gizmo2DBox queues a square of constant on-screen size centred on p.
func gizmo2DBox(p mgl32.Vec2, halfPx float32, col mgl32.Vec3) {
	r := screenToWorldSize(halfPx, editor2DOrtho())
	c00 := mgl32.Vec2{p.X() - r, p.Y() - r}
	c10 := mgl32.Vec2{p.X() + r, p.Y() - r}
	c11 := mgl32.Vec2{p.X() + r, p.Y() + r}
	c01 := mgl32.Vec2{p.X() - r, p.Y() + r}
	gizmo2DLine(c00, c10, col)
	gizmo2DLine(c10, c11, col)
	gizmo2DLine(c11, c01, col)
	gizmo2DLine(c01, c00, col)
}
