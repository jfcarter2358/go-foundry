package editor

// editor_translate_gizmo.go — three-axis drag gizmo for the 3D editors.
//
// Whatever is selected (sector vertices, the spawn point, or a whole chunk)
// gets three arms — X red, Y green, Z blue — drawn at a constant on-screen size
// around it. Dragging an arm slides the selection along that world axis only.
//
// The drag maps the mouse to a position along the axis by taking the closest
// point between the axis line and the cursor's view ray, and applies the offset
// from where the drag started to the values captured at mouse-down. Working
// from a snapshot rather than accumulating per-event deltas keeps a long drag
// exact, and makes snap-to-grid land on absolute grid multiples.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// gizmoKind* are the values EditorState.GizmoKind takes: what the translate
// gizmo is currently attached to.
const (
	gizmoKindNone     = ""
	gizmoKindVertices = "vertices"
	gizmoKindFace     = "face"
	gizmoKindSpawn    = "spawn"
	gizmoKindChunk    = "chunk"
	gizmoKindEntity   = "entity"
	gizmoKindVolume   = "volume"
	gizmoKindSprite   = "sprite"
	gizmoKindEmitter  = "emitter"
	gizmoKindSound    = "sound"
	gizmoKindScenery  = "scenery"
	gizmoKindLight    = "light"
	gizmoKindPrefab   = "prefab"
)

const (
	// gizmoArmPx is the on-screen length of each axis arm, in pixels.
	gizmoArmPx float32 = 70
	// gizmoTipHalfPx is half the on-screen size of the grab box at an arm's tip.
	gizmoTipHalfPx float32 = 5
	// gizmoPickPx is how close to an arm a click counts as grabbing it.
	gizmoPickPx float32 = 10
	// gizmoMinAxisDot guards against dragging an axis pointing almost straight
	// at the camera, where the mouse-to-axis mapping explodes.
	gizmoMinAxisDot float32 = 0.999
)

// gizmoAxes are the three world axes the gizmo can drag along, and their colors.
var gizmoAxes = [3]mgl32.Vec3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
var gizmoAxisColors = [3]mgl32.Vec3{{1, 0.25, 0.25}, {0.3, 1, 0.3}, {0.35, 0.5, 1}}

// gizmoEffectiveKind returns what the gizmo drags right now. The 2D modes have
// no 3D gizmo at all.
func gizmoEffectiveKind() string {
	if Editor.Mode == Mode3D {
		return Editor.GizmoKind
	}
	return gizmoKindNone
}

// translateGizmoOrigin returns where the gizmo sits for the current selection.
func translateGizmoOrigin() (mgl32.Vec3, bool) {
	switch gizmoEffectiveKind() {
	case gizmoKindVertices:
		return selectedVerticesCenter()

	case gizmoKindFace:
		return selectedFacesCenter()

	case gizmoKindSpawn:
		if s := activeGizmoSpawn(); s != nil {
			return s.Position, true
		}

	case gizmoKindEntity:
		if e := activeGizmoEntity(); e != nil {
			return e.Position, true
		}

	case gizmoKindVolume:
		if v := activeGizmoVolume(); v != nil {
			return v.Position, true
		}

	case gizmoKindScenery:
		if sc := activeGizmoScenery(); sc != nil {
			return sc.Position, true
		}

	case gizmoKindEmitter:
		if em := activeGizmoEmitter(); em != nil {
			return em.Position, true
		}

	case gizmoKindSound:
		if sd := activeGizmoSound(); sd != nil {
			// The authored position, not the world one: the gizmo drags what
			// the panel shows and the level file stores, so an attached sound
			// is dragged in its carrier's space.
			return sd.Position, true
		}

	case gizmoKindSprite:
		if sp := activeGizmoSprite(); sp != nil {
			// The authored position, not the attached one: the gizmo drags what
			// the panel shows and the level file stores. A sprite riding an
			// entity is dragged in that entity's space, which is the only
			// meaning a drag could have there.
			return sp.Position, true
		}

	case gizmoKindLight:
		if pl := activeGizmoPointLight(); pl != nil {
			return pl.Position, true
		}

	case gizmoKindPrefab:
		if p := activeGizmoPrefab(); p != nil {
			return p.Position, true
		}

	case gizmoKindChunk:
		if ch := activeGizmoChunk(); ch != nil {
			// The chunk's Origin field can sit far from its geometry, so anchor
			// the gizmo on the middle of the chunk's bounds instead — carried into
			// world space, since MinBound/MaxBound are chunk-local.
			return ch.LocalToWorld(ch.MinBound.Add(ch.MaxBound).Mul(0.5)), true
		}
	}
	return mgl32.Vec3{}, false
}

// activeGizmoSpawn returns the level's spawn point when it is the selection,
// or nil.
func activeGizmoSpawn() *world.SpawnPoint {
	if !Editor.SpawnSelected || Editor.level == nil {
		return nil
	}
	return Editor.level.Spawn
}

// activeGizmoChunk returns the chunk when it is the selection, or nil — the
// same "is this actually selected, not merely active" gate the other
// activeGizmoX functions apply, via selectedChunk (editor_chunk.go).
func activeGizmoChunk() *world.Chunk {
	return selectedChunk()
}

// activeGizmoPrefab is the selected prefab instance, when it is the gizmo's
// target. Gated on the kind for the reason every other accessor here is: an
// index left over from a selection that has since been replaced must not be
// dragged by a gizmo attached to something else.
func activeGizmoPrefab() *world.PrefabInstance {
	if gizmoEffectiveKind() != gizmoKindPrefab {
		return nil
	}
	return activePrefab()
}

// gizmoArmLength returns the world length of an arm at the gizmo's distance, so
// the gizmo covers the same number of pixels however far away it is.
func gizmoArmLength(origin mgl32.Vec3) float32 {
	return worldSizeForPixels(origin, gizmoArmPx)
}

// axisParamAtMouse returns how far along axis (from origin, in world units) the
// cursor's view ray comes closest. ok is false when the axis is too close to
// edge-on for the mapping to be usable.
func axisParamAtMouse(origin, axis mgl32.Vec3, mx, my int32) (float32, bool) {
	rayO, rayD := screenRay(mx, my)
	return axisParamForRay(rayO, rayD, origin, axis)
}

// axisParamForRay is axisParamAtMouse's body against an explicit ray, so a
// window with its own camera (the model editor) gets the same axis-drag
// mapping without going through the main window's screenRay.
func axisParamForRay(rayO, rayD, origin, axis mgl32.Vec3) (float32, bool) {
	b := rayD.Dot(axis)
	if float32(math.Abs(float64(b))) > gizmoMinAxisDot {
		return 0, false
	}
	w0 := rayO.Sub(origin)
	// Closest point between the view ray and the axis line; s is the parameter
	// along the axis (both directions are unit length, so the denominator is
	// 1 - (d·a)²).
	return (axis.Dot(w0) - b*rayD.Dot(w0)) / (1 - b*b), true
}

// pickTranslateAxis returns the axis whose arm a click at (mx, my) grabs, or -1.
func pickTranslateAxis(mx, my int32) int {
	origin, ok := translateGizmoOrigin()
	if !ok {
		return -1
	}
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	originScreen, ok := projectToScreen(projView, origin)
	if !ok {
		return -1
	}
	armLen := gizmoArmLength(origin)
	cursor := mgl32.Vec2{float32(mx), float32(my)}

	best := -1
	bestD := gizmoPickPx
	for i, axis := range gizmoAxes {
		tipScreen, ok := projectToScreen(projView, origin.Add(axis.Mul(armLen)))
		if !ok {
			continue
		}
		d := distToSegment2D(cursor.X(), cursor.Y(),
			originScreen.X(), originScreen.Y(), tipScreen.X(), tipScreen.Y())
		if d <= bestD {
			bestD = d
			best = i
		}
	}
	return best
}

// gizmoSuppressed reports whether the gizmo should be inert because the next
// click already means something else. The arms are hidden in these states, so
// they must not be clickable either.
func gizmoSuppressed() bool {
	return Editor.SpawnPlacementMode || Editor.EntityPlacementMode ||
		Editor.VolumePlacementMode ||
		// The brush too: its arms would sit over the ghost and swallow the
		// click that was meant to stamp one.
		Editor.BrushActive
}

// translateGizmoMouseDown starts an axis drag if the click grabbed an arm.
// Returns true when the click was consumed, so it does not also re-select.
//
// An arm under the cursor consumes the click even when the drag cannot start —
// an axis pointing almost straight at the camera, say. The arms are drawn on
// top of everything and sit right where the selection's own handles are, so a
// click aimed at one must never fall through to picking: throwing away the
// selection the gizmo exists to move is never what that click meant.
func translateGizmoMouseDown(t sdl.MouseButtonEvent) bool {
	if t.Button != sdl.ButtonLeft || gizmoSuppressed() {
		return false
	}
	axis := pickTranslateAxis(t.X, t.Y)
	if axis < 0 {
		return false
	}
	origin, ok := translateGizmoOrigin()
	if !ok {
		return true
	}
	s, ok := axisParamAtMouse(origin, gizmoAxes[axis], t.X, t.Y)
	if !ok {
		return true
	}

	if !captureGizmoDragStart() {
		return true
	}
	Editor.GizmoDragging = true
	Editor.GizmoDragTool = editorToolMove
	Editor.GizmoDragAxis = axis
	Editor.GizmoDragStartS = s
	Editor.GizmoDragOrigin = origin
	return true
}

// captureGizmoDragStart snapshots the values the drag will offset, and pushes an
// undo entry once for the whole drag (not per motion event).
func captureGizmoDragStart() bool {
	Editor.GizmoDragPoints = nil
	Editor.GizmoDragOffsets = nil

	switch gizmoEffectiveKind() {
	case gizmoKindVertices, gizmoKindFace:
		// One case for both: a face drag is a drag of the handles the face is
		// built from (editor_face_gizmo.go), and gizmoVertexSels is what says
		// which those are. The snapshot below is parallel to that list, so
		// every drag indexes into it the same way.
		lvl := Editor.level
		sels := gizmoVertexSels()
		if lvl == nil || len(sels) == 0 {
			return false
		}
		// The world snapshot is the whole of it. A sector vertex needed two
		// more — its shared chunk point's XZ and its per-surface height offset
		// — because those were the two stores a drag had to be folded back
		// into. A brush vertex is a position, so the position at mouse-down is
		// everything a drag has to remember.
		_ = lvl
		Editor.GizmoDragWorld = selectedVerticesWorld()
		EditorPushUndo()
		return true

	case gizmoKindSpawn:
		s := activeGizmoSpawn()
		if s == nil {
			return false
		}
		// The rotation goes in the snapshot too: the marker has no size, but it
		// does have a facing, and the rotate drag starts from it.
		Editor.GizmoDragStartPos = s.Position
		Editor.GizmoDragStartRot = s.Rotation
		EditorPushUndo()
		return true

	case gizmoKindEntity:
		e := activeGizmoEntity()
		if e == nil {
			return false
		}
		// All three of these are snapshotted, not just the position: the same
		// capture serves the rotate and scale drags, which start from the
		// entity's own orientation and size rather than from a point cloud.
		Editor.GizmoDragStartPos = e.Position
		Editor.GizmoDragStartRot = e.Rotation
		Editor.GizmoDragStartScale = e.EffectiveScale()
		EditorPushUndo()
		return true

	case gizmoKindVolume:
		v := activeGizmoVolume()
		if v == nil {
			return false
		}
		// A volume has no scale of its own — its size is its half-extents — so
		// the scale snapshot is left at identity and the scale gizmo resizes the
		// extents directly.
		Editor.GizmoDragStartPos = v.Position
		Editor.GizmoDragStartRot = v.Rotation
		Editor.GizmoDragStartScale = mgl32.Vec3{v.HalfX, v.HalfY, v.HalfZ}
		EditorPushUndo()
		return true

	case gizmoKindSprite:
		sp := activeGizmoSprite()
		if sp == nil {
			return false
		}
		// Like a volume, a sprite has no scale factor — its size is its width
		// and height — so the snapshot holds those and the scale gizmo resizes
		// the quad directly. Z is unused and parked at 1 so a uniform drag
		// cannot divide by zero.
		Editor.GizmoDragStartPos = sp.Position
		Editor.GizmoDragStartRot = sp.Rotation
		Editor.GizmoDragStartScale = mgl32.Vec3{sp.Width, sp.Height, 1}
		EditorPushUndo()
		return true

	case gizmoKindSound:
		sd := activeGizmoSound()
		if sd == nil {
			return false
		}
		// Nothing to scale: how far a sound carries is its two distances,
		// which are numbers in the panel rather than a shape to drag.
		Editor.GizmoDragStartPos = sd.Position
		Editor.GizmoDragStartRot = sd.Rotation
		Editor.GizmoDragStartScale = mgl32.Vec3{1, 1, 1}
		EditorPushUndo()
		return true

	case gizmoKindScenery:
		sc := activeGizmoScenery()
		if sc == nil {
			return false
		}
		// All three, as an entity does: a prop's scale is a genuine per-axis
		// factor on its model, so the scale gizmo drives it directly rather
		// than standing in for extents the way a volume's does.
		Editor.GizmoDragStartPos = sc.Position
		Editor.GizmoDragStartRot = sc.Rotation
		Editor.GizmoDragStartScale = sc.EffectiveScale()
		EditorPushUndo()
		return true

	case gizmoKindEmitter:
		em := activeGizmoEmitter()
		if em == nil {
			return false
		}
		// Nothing to scale: an emitter's size is its effect's emission shape,
		// which is the document's business rather than the placement's. The
		// snapshot is left at identity so the scale gizmo is a no-op on it.
		Editor.GizmoDragStartPos = em.Position
		Editor.GizmoDragStartRot = em.Rotation
		Editor.GizmoDragStartScale = mgl32.Vec3{1, 1, 1}
		EditorPushUndo()
		return true

	case gizmoKindLight:
		pl := activeGizmoPointLight()
		if pl == nil {
			return false
		}
		// Position only. A point light is a position and a radius — it has no
		// orientation to rotate and no size the scale gizmo could act on, so
		// the rotate and scale tools simply do not match this kind.
		Editor.GizmoDragStartPos = pl.Position
		EditorPushUndo()
		return true

	case gizmoKindPrefab:
		p := activeGizmoPrefab()
		if p == nil {
			return false
		}
		Editor.GizmoDragStartPos = p.Position
		EditorPushUndo()
		return true

	case gizmoKindChunk:
		ch := activeGizmoChunk()
		if ch == nil {
			return false
		}
		// All three snapshotted, as for an entity/volume: the same capture
		// serves the rotate and scale drags, which start from the chunk's own
		// Rotation/Scale rather than from a point cloud. Rotation is Y-only
		// (chunk.go's ModelMatrix has no X/Z rotation to speak of) and Scale is
		// uniform, so only one component of each carries real information —
		// but packing them into the same Vec3 fields every other kind uses
		// keeps this one case, not a parallel set of scalar fields.
		sc := ch.Scale
		if sc == 0 {
			sc = 1
		}
		Editor.GizmoDragStartPos = ch.Origin
		Editor.GizmoDragStartRot = mgl32.Vec3{0, ch.Rotation, 0}
		Editor.GizmoDragStartScale = mgl32.Vec3{sc, sc, sc}
		EditorPushUndo()
		return true
	}
	return false
}

// translateGizmoMouseUp ends a drag, whichever tool started it.
func translateGizmoMouseUp() {
	Editor.GizmoDragging = false
	Editor.GizmoDragAxis = -1
	Editor.GizmoDragPoints = nil
	Editor.GizmoDragOffsets = nil
	Editor.GizmoDragWorld = nil
}

// translateGizmoMouseMove applies the current cursor position to the drag.
func translateGizmoMouseMove(mx, my int32) {
	if !Editor.GizmoDragging {
		return
	}
	axis := Editor.GizmoDragAxis
	if axis < 0 || axis > 2 {
		return
	}
	s, ok := axisParamAtMouse(Editor.GizmoDragOrigin, gizmoAxes[axis], mx, my)
	if !ok {
		return
	}
	delta := s - Editor.GizmoDragStartS
	if delta == 0 {
		return
	}

	switch gizmoEffectiveKind() {
	case gizmoKindVertices, gizmoKindFace:
		dragVerticesAlongAxis(axis, delta)
	case gizmoKindSpawn:
		if s := activeGizmoSpawn(); s != nil {
			s.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
		}
	case gizmoKindEntity:
		if e := activeGizmoEntity(); e != nil {
			e.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
		}
	case gizmoKindVolume:
		if v := activeGizmoVolume(); v != nil {
			// No cage rebuild: it is built in volume-local space, so moving one
			// only changes the matrix it is drawn with.
			v.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
		}
	case gizmoKindScenery:
		if sc := activeGizmoScenery(); sc != nil {
			// Nothing to rebuild: the mesh is model-space and the draw reads
			// the transform out of the level each frame.
			sc.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
		}
	case gizmoKindEmitter:
		if em := activeGizmoEmitter(); em != nil {
			em.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
		}
	case gizmoKindSound:
		if sd := activeGizmoSound(); sd != nil {
			sd.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
		}
	case gizmoKindSprite:
		if sp := activeGizmoSprite(); sp != nil {
			// Nothing to rebuild at all: the sprite pass owns one shared quad
			// and reads the position straight out of the level each frame.
			sp.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
		}
	case gizmoKindLight:
		if pl := activeGizmoPointLight(); pl != nil {
			pl.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
			// Straight to the shader, per drag event: a light whose uniform is
			// only refreshed on mouse-up would be dragged with the lighting
			// left behind, which is the one thing you are watching while you
			// move it.
			world.UploadLights(Editor.level.Lights)
		}
	case gizmoKindPrefab:
		if p := activeGizmoPrefab(); p != nil {
			p.Position = axisMoved(Editor.GizmoDragStartPos, axis, delta)
			// Re-stamped per drag event, not on release: a prefab's entities,
			// volumes and lights are world-positioned with no parent to
			// inherit from, so nothing would appear to move until it ended.
			restampActivePrefab()
		}
	case gizmoKindChunk:
		if ch := activeGizmoChunk(); ch != nil {
			moveChunk(ch, axisMoved(Editor.GizmoDragStartPos, axis, delta))
		}
	}
}

// axisMoved returns start offset by delta along one axis, snapped to the grid
// step when snapping is on.
func axisMoved(start mgl32.Vec3, axis int, delta float32) mgl32.Vec3 {
	out := start
	out[axis] = gizmoSnap(start[axis] + delta)
	return out
}

// gizmoSnap rounds v to the editor's step size when snap-to-grid is enabled.
func gizmoSnap(v float32) float32 {
	if Editor.SnapToGrid && Editor.StepSize > 0 {
		return snapValue(v, Editor.StepSize)
	}
	return v
}

// dragVerticesAlongAxis moves every selected vertex by delta along one world
// axis, from its own mouse-down position.
//
// This used to be two entirely different edits sharing a function. A sector
// vertex's X and Z were a shared chunk point and its Y was a per-vertex offset
// from the sector's floor or ceiling height, so the Y arm and the XZ arms wrote
// different fields through different helpers, snapped against different bases,
// and each had its own note about which space the delta was in. A brush vertex
// is one position: all three arms do the same thing.
//
// The world→local conversion is still real, and still the only interesting
// part. Snapping is done in the CHUNK's space, not the world's, because that is
// the grid a brush's corners are authored on — and for a rotated, scaled or
// frame-owned chunk the two are not the same grid.
func dragVerticesAlongAxis(axis int, delta float32) {
	worldDelta := gizmoAxes[axis].Mul(delta)
	setSelectedVertexLocals(func(i int, sel vertexSel, _ mgl32.Vec3) mgl32.Vec3 {
		if i >= len(Editor.GizmoDragWorld) {
			return mgl32.Vec3{}
		}
		ch, _ := vertexSelBrush(sel)
		if ch == nil {
			return mgl32.Vec3{}
		}
		start := ch.WorldToLocal(Editor.GizmoDragWorld[i])
		// The fast path leaves the two untouched axes exactly alone, which is
		// what keeps grid-snap honest for the ordinary case: a chunk sitting
		// unrotated and unscaled at its origin. It is only legal when a
		// world-space delta IS a local one — i.e. the whole ModelMatrix is the
		// identity, not merely Rotation == 0. A scaled chunk moves at the
		// wrong rate through it (one world unit is 1/Scale local units), and a
		// frame-owned chunk can be rotated in the world by its vehicle while
		// its own Rotation is still 0, which sends a world-X drag diagonally
		// through its local space.
		if ch.ModelMatrix() == mgl32.Ident4() {
			out := start
			out[axis] = gizmoSnap(out[axis] + delta)
			return out
		}
		local := ch.WorldToLocalDirection(worldDelta)
		return mgl32.Vec3{
			gizmoSnap(start.X() + local.X()),
			gizmoSnap(start.Y() + local.Y()),
			gizmoSnap(start.Z() + local.Z()),
		}
	})
}

// renderTranslateGizmo draws the three axis arms for the current selection.
// The hovered or dragged arm turns yellow.
func renderTranslateGizmo(mx, my int32) {
	origin, ok := translateGizmoOrigin()
	if !ok {
		return
	}
	hover := -1
	if Editor.GizmoDragging {
		hover = Editor.GizmoDragAxis
	} else {
		hover = pickTranslateAxis(mx, my)
	}

	armLen := gizmoArmLength(origin)
	yellow := mgl32.Vec3{1, 1, 0}
	for i, axis := range gizmoAxes {
		col := gizmoAxisColors[i]
		if i == hover {
			col = yellow
		}
		tip := origin.Add(axis.Mul(armLen))
		gizmo.GizmoLine(origin, tip, col)
		gizmoBox(tip, gizmoTipHalfPx, col)
	}
}

// ---------- dispatch ----------
//
// The 3D viewport shows one gizmo at a time — Editor.GizmoTool says which, on
// the model editor's G/R/C shortcuts (editor_sel_tool.go). They are separate
// tools rather than one combined widget because their handles would sit on top
// of each other: a rotate ring passes through the tips of the move arms, and
// every click near the end of an axis would be a coin toss.
//
// Scale acts only on things with a size of their own. Rotate additionally acts
// on anything with a facing to rewrite — an entity, a volume, the spawn marker.
// A chunk has neither, so move is the only tool that means anything for it —
// and so does a water plane, whose "move" is one arm writing back the depth
// its owning sector holds (editor_water.go).

// editorGizmoDragging reports whether a drag is in progress. A drag owns the
// mouse until release.
func editorGizmoDragging() bool { return Editor.GizmoDragging }

// editorGizmoMouseDown routes a click to whichever gizmo is on screen.
func editorGizmoMouseDown(t sdl.MouseButtonEvent) bool {
	switch Editor.GizmoTool {
	case editorToolRotate:
		return rotateGizmoMouseDown(t)
	case editorToolScale:
		return scaleGizmoMouseDown(t)
	}
	return translateGizmoMouseDown(t)
}

// editorGizmoMouseMove applies motion to a drag in progress. It dispatches on
// the tool the *drag* belongs to, not the tool now selected: a tool switch
// cancels the drag first (editorSetGizmoTool), and routing on the live value
// would otherwise let a stale drag be finished by the wrong maths.
func editorGizmoMouseMove(mx, my int32) {
	if !Editor.GizmoDragging {
		return
	}
	switch Editor.GizmoDragTool {
	case editorToolRotate:
		rotateGizmoMouseMove(mx, my)
	case editorToolScale:
		scaleGizmoMouseMove(mx, my)
	default:
		translateGizmoMouseMove(mx, my)
	}
}

// editorGizmoMouseUp ends a drag.
func editorGizmoMouseUp() { translateGizmoMouseUp() }

// renderEditorGizmo draws the gizmo for the current selection.
func renderEditorGizmo(mx, my int32) {
	switch Editor.GizmoTool {
	case editorToolRotate:
		renderRotateGizmo(mx, my)
	case editorToolScale:
		renderScaleGizmo(mx, my)
	default:
		renderTranslateGizmo(mx, my)
	}
}
