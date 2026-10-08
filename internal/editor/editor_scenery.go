package editor

// editor_scenery.go — the scenery brush, and the props it stamps.
//
// Every other placement mode in this editor is a one-shot: arm it, click once,
// it disarms and selects what it made, and the author goes back to the panel to
// arm it again. That is right for the things there are three of in a level — a
// spawn point, a trigger volume, an ambient sound.
//
// It is exactly wrong for props. A path wants forty rocks along it, and forty
// round trips through a panel is not a workflow. So the brush is the opposite
// arrangement on purpose:
//
//   - it stays armed until it is put away, so clicking places another one;
//   - a ghost of the model follows the cursor onto whatever surface is under
//     it, so what lands is what was already being looked at rather than a
//     surprise to be dragged into place afterwards;
//   - the ghost is what carries the rotation and scale, so those are dialled in
//     once and every stamp inherits them;
//   - and a stamp does *not* select what it made. Selecting would fight the
//     brush for the next click, and the thing to do after placing a rock is
//     place another rock.
//
// Undo is per stamp rather than per gesture, which matches the click: one
// Ctrl+Z takes back one rock.
//
// The ghost's mesh is uploaded once when the model changes and reused for every
// frame it is drawn — see brushGhost. It is a world.Scenery like the ones it
// places, so there is exactly one code path deciding how a model sits on a
// transform, and the preview cannot disagree with the result.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/render"
	"foundry/internal/world"
	"math"
	"math/rand"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// ---------- selection ----------

// activeScenery returns the selected prop, or nil.
func activeScenery() *world.Scenery {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveSceneryIdx < 0 || Editor.ActiveSceneryIdx >= len(lvl.Scenery) {
		return nil
	}
	return &lvl.Scenery[Editor.ActiveSceneryIdx]
}

// editorSelectScenery makes the prop at idx the selection, giving it the gizmo
// and opening its properties section.
func editorSelectScenery(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Scenery) {
		return
	}
	editorClearObjectSelections()
	Editor.ActiveSceneryIdx = idx
	Editor.GizmoKind = gizmoKindScenery
	Editor.PanelFocus = panelFocusScenery
}

// editorClearScenerySelection drops the prop from the selection, taking the
// gizmo with it so it cannot keep dragging something no longer selected.
func editorClearScenerySelection() {
	Editor.ActiveSceneryIdx = -1
	if Editor.GizmoKind == gizmoKindScenery {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoScenery returns the prop the gizmo is attached to, or nil. The
// gizmo kind is checked as well as the index, so a stale selection left by
// another tool cannot silently be dragged.
func activeGizmoScenery() *world.Scenery {
	if Editor.GizmoKind != gizmoKindScenery {
		return nil
	}
	return activeScenery()
}

// ---------- the brush ----------

// brushGhost is the preview: a real world.Scenery, uploaded once per model
// change and moved by its transform every frame.
//
// A package-level var rather than a field of EditorState because it owns GPU
// handles, and EditorState is reset wholesale in several places that have no
// business freeing a VAO.
var brushGhost world.Scenery

// brushGhostAlpha is how solid the preview is. Solid enough to read the model's
// shape against the ground, transparent enough that it never looks like
// something already placed.
const brushGhostAlpha = 0.45

// brushGhostPos is where the ghost was last drawn — the point the next stamp
// lands on. Recorded during the draw because that is the one place per frame
// the ray is cast, and the click reads it rather than casting a second one that
// could disagree with what the author was looking at.
var brushGhostPos mgl32.Vec3

// brushGhostValid says whether brushGhostPos is from this frame. False means
// the cursor was over the panel, or the brush is not armed, and a click must
// not stamp anything.
var brushGhostValid bool

// editorArmBrush points the brush at a model and switches it on. An empty path
// puts it away, since a brush with no model has nothing to stamp.
func editorArmBrush(path string) {
	Editor.BrushModel = path
	if path == "" {
		editorDisarmBrush()
		return
	}
	Editor.BrushActive = true
	// The one-shot modes and the brush are mutually exclusive: both answer the
	// next left-click, and two of them armed at once would make which one wins
	// a question about the order of the checks in Editor3DHandleClick.
	Editor.EntityPlacementMode = false
	Editor.VolumePlacementMode = false
	Editor.SpritePlacementMode = false
	Editor.DecalPlacementMode = false
	Editor.EmitterPlacementMode = false
	Editor.SoundPlacementMode = false
	Editor.EffectPlacementMode = false
	Editor.TreePlacementMode = false
	Editor.SpawnPlacementMode = false
	Editor.WaypointPlacementMode = false
	editorDisarmTerrainTool()
	editorSetStatus("Brush: %s — click to place, Escape to stop", path)
}

// editorDisarmBrush puts the brush away and releases the ghost's mesh.
func editorDisarmBrush() {
	Editor.BrushActive = false
	brushGhostValid = false
	brushGhost.Delete()
	brushGhost.Model = ""
}

// editorBrushScale returns the ghost's scale, treating an unset value as 1 so a
// brush that has never been touched stamps the model at its authored size.
func editorBrushScale() float32 {
	if Editor.BrushScale <= 0 {
		return 1
	}
	return Editor.BrushScale
}

// brushYawStep is how far one press of [ or ] turns the ghost. Fifteen degrees
// is coarse enough that a few presses go somewhere and fine enough that a fence
// post can be squared up; Shift takes it down to a third of that.
const brushYawStep = float32(15)
const brushFineYawStep = float32(5)

// brushScaleStep is the factor one press of - or = applies. Multiplicative, so
// the same press does the same visible amount at any size.
const brushScaleStep = float32(1.1)

// editorBrushYaw turns the ghost by n steps, fine when shift is held.
func editorBrushYaw(steps float32, fine bool) {
	step := brushYawStep
	if fine {
		step = brushFineYawStep
	}
	y := mgl32.RadToDeg(Editor.BrushRotation.Y()) + steps*step
	// Wrapped so the panel's field does not creep off to 3600 degrees over a
	// session of scattering.
	for y > 180 {
		y -= 360
	}
	for y < -180 {
		y += 360
	}
	Editor.BrushRotation[1] = mgl32.DegToRad(y)
}

// editorBrushScaleBy multiplies the ghost's size, clamped to something that can
// still be seen and still be clicked.
func editorBrushScaleBy(f float32) {
	s := editorBrushScale() * f
	if s < 0.05 {
		s = 0.05
	}
	if s > 100 {
		s = 100
	}
	Editor.BrushScale = s
}

// brushRotationFor returns the rotation one stamp takes: the ghost's, with a
// random yaw substituted when the brush is set to scatter.
//
// Random yaw is the difference between forty rocks and one rock forty times.
// It is applied at the stamp rather than to the ghost, because a ghost that
// span on every mouse move would be unusable for the case the brush is *not*
// scattering — lining a crate up against a wall.
func brushRotationFor() mgl32.Vec3 {
	r := Editor.BrushRotation
	if Editor.BrushRandomYaw {
		r[1] = rand.Float32() * 2 * math.Pi
	}
	return r
}

// brushStampID is the base ID a stamp takes: the model's own file name, so a
// path of models/barrel.fmdl gives barrel, barrel_2, barrel_3 rather than
// scenery, scenery_2.
func brushStampID(path string) string {
	if name := world.ModelNameForPath(path); name != "" && name != "model" {
		return name
	}
	return "scenery"
}

// brushStamp places a copy of the brush's model at pos and reports whether it
// did. The brush stays armed — see the file comment.
func brushStamp(pos mgl32.Vec3) bool {
	lvl := Editor.level
	if lvl == nil || Editor.BrushModel == "" {
		return false
	}
	EditorPushUndo()

	s := world.NewScenery(
		world.UniqueSceneryID(lvl, brushStampID(Editor.BrushModel)),
		Editor.BrushModel, pos)
	s.Rotation = brushRotationFor()
	sc := editorBrushScale()
	s.Scale = mgl32.Vec3{sc, sc, sc}
	s.Solid = Editor.BrushSolid
	if err := s.Load(); err != nil {
		// The placement is still real — an author can repoint it at another
		// mesh in the panel — but say so, or a brush stamping nothing at all
		// looks like a broken click.
		editorSetStatus("Scenery %s: %v", s.ID, err)
	}
	lvl.Scenery = append(lvl.Scenery, s)
	editorSetStatus("Placed %s (%d in level)", s.ID, len(lvl.Scenery))
	return true
}

// editorBrushHandleClick stamps at the cursor when the brush is armed, and
// reports whether it consumed the click.
//
// It uses the point the ghost was drawn at rather than casting a fresh ray, so
// what lands is exactly what was being previewed. A frame where the ghost was
// not drawn — the cursor over the side panel, the model still loading — places
// nothing rather than guessing.
func editorBrushHandleClick() bool {
	if !Editor.BrushActive {
		return false
	}
	if !brushGhostValid {
		return true // consumed: a click while brushing is never a selection
	}
	brushStamp(brushGhostPos)
	return true
}

// editorBrushHandleKey owns the keys that only mean something while the brush
// is armed: turn it, resize it, put it away. Reports whether it consumed one.
//
// Bracket keys for rotation and minus/equals for size, which is what every tool
// with a brush in it binds them to. They are checked before the editor's own
// single-letter shortcuts, and only while brushing, so nothing else loses a
// binding to them.
func editorBrushHandleKey(scancode sdl.Scancode, shift bool) bool {
	if !Editor.BrushActive {
		return false
	}
	switch scancode {
	case sdl.SCANCODE_LEFTBRACKET:
		editorBrushYaw(-1, shift)
	case sdl.SCANCODE_RIGHTBRACKET:
		editorBrushYaw(1, shift)
	case sdl.SCANCODE_MINUS:
		editorBrushScaleBy(1 / brushScaleStep)
	case sdl.SCANCODE_EQUALS:
		editorBrushScaleBy(brushScaleStep)
	case sdl.SCANCODE_ESCAPE:
		editorDisarmBrush()
		editorSetStatus("Brush off")
	default:
		return false
	}
	return true
}

// ---------- the ghost ----------

// editorBrushSyncGhost makes the preview match the brush's model, uploading it
// when it has changed. Called from the draw, which is the only place a live GL
// context is guaranteed.
func editorBrushSyncGhost() {
	if brushGhost.Model == Editor.BrushModel {
		return
	}
	brushGhost.Delete()
	brushGhost.ID = "brush"
	brushGhost.Model = Editor.BrushModel
	// The ghost is in no level's scenery list, so the GPU-driven cull would
	// never see it: a cooked mesh's ghost draws the classic way.
	brushGhost.NoNanite = true
	if brushGhost.Model == "" {
		return
	}
	if err := brushGhost.Load(); err != nil {
		editorSetStatus("Brush model %s: %v", brushGhost.Model, err)
		return
	}
	brushGhost.SetAlpha(brushGhostAlpha)
}

// RenderSceneryGhost draws the brush's preview where the cursor is pointing.
//
// Editor-only, and called from the colour pass through draw — the frame loop's
// drawOrQueue — so the translucent ghost sorts against the world's own
// transparent surfaces instead of blending over them regardless of depth.
//
// This is also where the stamp point for the next click is decided: one ray per
// frame, recorded, and read by the click. Casting again in the handler would be
// a second answer to the same question, and the two would differ on any frame
// the world moved underneath a still cursor.
func RenderSceneryGhost(draw func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool)) {
	brushGhostValid = false
	if !Editor.Active || Editor.Mode != Mode3D || !Editor.BrushActive || Editor.level == nil {
		return
	}
	// Where the cursor is, asked of SDL rather than remembered from an event —
	// the same source the gizmo's hover test uses, so the ghost and the handles
	// can never disagree about where the pointer is.
	//
	// Over the side panel the ray would be cast through a pixel the viewport
	// does not own, so the ghost is hidden rather than parked somewhere
	// arbitrary — which is also the signal a click reads.
	mx, my, _ := sdl.GetMouseState()
	if imgui.CurrentIO().WantCaptureMouse() || mx >= panelLeftEdgeX() {
		return
	}
	editorBrushSyncGhost()
	if !brushGhost.HasGeometry() {
		return
	}

	brushGhostPos = editor3DPointAt(mx, my)
	brushGhostValid = true

	brushGhost.Position = brushGhostPos
	brushGhost.Rotation = Editor.BrushRotation
	sc := editorBrushScale()
	brushGhost.Scale = mgl32.Vec3{sc, sc, sc}

	model := brushGhost.ModelMatrix()
	for i := range brushGhost.Submeshes {
		draw(&brushGhost.Submeshes[i], model, false)
	}
}

// DrawBrushGhostOutline traces the ghost's footprint through the gizmo line
// buffer, which draws with the depth test off.
//
// The translucent mesh alone is easy to lose against busy ground, and where the
// prop's base actually is — which is what an author is aiming — is the one
// thing a semi-transparent solid says least clearly. Drawn from the model's own
// bounds, so it is the placement's real extent rather than a fixed-size marker.
func DrawBrushGhostOutline() {
	if !brushGhostValid || !brushGhost.HasBounds() {
		return
	}
	c := brushGhost.BoundCenter()
	h := brushGhost.BoundHalfExtents()
	// The base of the volume, not its middle: a ring at the centre of a tree
	// floats in the air halfway up its trunk.
	y := c.Y() - h.Y()
	rot := world.EulerMatrixYXZ(brushGhost.Rotation)
	corner := func(sx, sz float32) mgl32.Vec3 {
		r := rot.Mul4x1(mgl32.Vec4{sx * h.X(), 0, sz * h.Z(), 0})
		return mgl32.Vec3{c.X() + r.X(), y, c.Z() + r.Z()}
	}
	pts := [4]mgl32.Vec3{corner(-1, -1), corner(1, -1), corner(1, 1), corner(-1, 1)}
	for i := 0; i < 4; i++ {
		gizmo.GizmoLine(pts[i], pts[(i+1)%4], brushGhostColor)
	}
	// A stub along the ghost's own +X, so a yaw that has been dialled in is
	// visible before the model is committed — most props are near enough
	// symmetric to hide a 90-degree error until there are thirty of them.
	f := rot.Mul4x1(mgl32.Vec4{1, 0, 0, 0})
	reach := h.X()
	if h.Z() > reach {
		reach = h.Z()
	}
	base := mgl32.Vec3{c.X(), y, c.Z()}
	gizmo.GizmoLine(base, base.Add(mgl32.Vec3{f.X(), 0, f.Z()}.Mul(reach*1.5)), brushGhostColor)
}

// brushGhostColor is the preview's outline — cyan, which nothing else in the
// viewport uses, so the ghost is never mistaken for a selection (yellow) or for
// the spawn marker (orange).
var brushGhostColor = mgl32.Vec3{0.2, 0.9, 1}

// ---------- duplicate / delete ----------

// duplicateActiveScenery copies the selected prop beside itself and selects the
// copy, offset rather than coincident for the reason a duplicated entity is:
// two props in exactly the same place cannot be told apart, let alone clicked
// apart.
func duplicateActiveScenery() {
	lvl := Editor.level
	src := activeScenery()
	if lvl == nil || src == nil {
		return
	}
	EditorPushUndo()

	// The source's Submeshes hold live GPU handles; a struct copy would alias
	// them, and deleting either prop would leave the other drawing from a
	// released VAO. Copy the authored fields and upload afresh.
	cp := world.NewScenery(world.UniqueSceneryID(lvl, src.ID), src.Model,
		src.Position.Add(mgl32.Vec3{sceneryDuplicateOffset, 0, sceneryDuplicateOffset}))
	cp.Rotation = src.Rotation
	cp.Scale = src.Scale
	cp.Solid = src.Solid
	if err := cp.Load(); err != nil {
		editorSetStatus("Scenery %s: %v", cp.ID, err)
	}
	lvl.Scenery = append(lvl.Scenery, cp)
	editorSelectScenery(len(lvl.Scenery) - 1)
	editorSetStatus("Duplicated scenery %q", cp.ID)
}

// sceneryDuplicateOffset is how far a duplicate lands from its original, in
// world units — far enough to be clickable at a normal working zoom.
const sceneryDuplicateOffset = float32(1.0)

// deleteActiveScenery removes the selected prop and releases its GPU buffers.
func deleteActiveScenery() {
	lvl := Editor.level
	idx := Editor.ActiveSceneryIdx
	if lvl == nil || idx < 0 || idx >= len(lvl.Scenery) {
		return
	}
	EditorPushUndo()
	id := lvl.Scenery[idx].ID
	lvl.Scenery[idx].Delete()
	lvl.Scenery = append(lvl.Scenery[:idx:idx], lvl.Scenery[idx+1:]...)
	editorClearScenerySelection()
	editorSetStatus("Deleted scenery %q", id)
}

// editorSceneryReload re-reads a prop's model from disk — what the panel calls
// after changing the path, and what picks up a .fmdl re-saved by the model
// editor.
func editorSceneryReload(s *world.Scenery) {
	if s == nil {
		return
	}
	if err := s.Load(); err != nil {
		editorSetStatus("Scenery %s: %v", s.ID, err)
		return
	}
	editorSetStatus("Reloaded %q", s.ID)
}

// ---------- picking ----------

// pickSceneryRayT returns the distance at which a picking ray crosses a prop,
// or +Inf when it misses.
//
// The bounding box is tested first and the triangles only if it hits. That is
// an optimisation everywhere else in this file's neighbourhood could do
// without, and cannot be here: the brush exists to make levels with hundreds of
// props in them, and a click that walked every triangle of every one would be a
// visible stall.
func pickSceneryRayT(origin, dir mgl32.Vec3, s *world.Scenery) float32 {
	if bmin, bmax, ok := s.WorldAABB(); ok {
		if hit, _ := rayAABBIntersect(origin, dir, bmin, bmax); !hit {
			return float32(math.MaxFloat32)
		}
	}
	best := float32(math.MaxFloat32)
	model := s.ModelMatrix()
	for i := range s.Submeshes {
		if t := pickModelGeometryT(origin, dir, &s.Submeshes[i], model); t < best {
			best = t
		}
	}
	if best < float32(math.MaxFloat32) {
		return best
	}
	// Nothing uploaded: the volume is all there is to aim at, and a prop whose
	// model failed to load must stay selectable or it can never be repaired.
	if bmin, bmax, ok := s.WorldAABB(); ok {
		if hit, t := rayAABBIntersect(origin, dir, bmin, bmax); hit {
			return t
		}
	}
	return float32(math.MaxFloat32)
}

// ---------- selection mark ----------

// scenerySelColor is the selected prop's outline — the same yellow every other
// selection in the viewport uses.
var scenerySelColor = mgl32.Vec3{1, 1, 0}

// drawSelectedSceneryOutline cages the selected prop through the gizmo line
// buffer, which draws with depth testing off — so the mark survives the prop
// being behind a wall, as a selected face's outline does.
//
// The cage is the model's own bounds under the placement's rotation, which is
// also exactly the volume collision uses. Seeing the two agree is worth as much
// as seeing what is selected.
func drawSelectedSceneryOutline() {
	s := activeScenery()
	if s == nil || !s.HasBounds() {
		return
	}
	c := s.BoundCenter()
	h := s.BoundHalfExtents()
	rot := world.EulerMatrixYXZ(s.Rotation)
	corner := func(sx, sy, sz float32) mgl32.Vec3 {
		r := rot.Mul4x1(mgl32.Vec4{sx * h.X(), sy * h.Y(), sz * h.Z(), 0})
		return c.Add(mgl32.Vec3{r.X(), r.Y(), r.Z()})
	}
	// Bottom face, top face, then the four uprights joining them.
	lo := [4]mgl32.Vec3{corner(-1, -1, -1), corner(1, -1, -1), corner(1, -1, 1), corner(-1, -1, 1)}
	hi := [4]mgl32.Vec3{corner(-1, 1, -1), corner(1, 1, -1), corner(1, 1, 1), corner(-1, 1, 1)}
	for i := 0; i < 4; i++ {
		gizmo.GizmoLine(lo[i], lo[(i+1)%4], scenerySelColor)
		gizmo.GizmoLine(hi[i], hi[(i+1)%4], scenerySelColor)
		gizmo.GizmoLine(lo[i], hi[i], scenerySelColor)
	}
}

// ---------- menu helpers ----------

// editor3DMoveSceneryTo puts the selected prop at pos — the context menu's
// "move here", matching every other object's.
func editor3DMoveSceneryTo(pos mgl32.Vec3) {
	s := activeScenery()
	if s == nil {
		return
	}
	EditorPushUndo()
	s.Position = pos
}

// editor3DToggleScenerySolid flips the selected prop's collision — the context
// menu's checkbox, undoable like every other menu edit.
func editor3DToggleScenerySolid() {
	s := activeScenery()
	if s == nil {
		return
	}
	EditorPushUndo()
	s.Solid = !s.Solid
}

// editorSceneryBrushMenuItem builds the shared menu's brush row.
//
// It names the model the brush is pointed at rather than opening a submenu of
// every mesh on disk: the panel's picker is where a model is chosen, and a menu
// that re-offered the whole list would be a second place to choose one that
// could disagree with the first — the same reasoning editorAddEntityMenuItem
// gives for the entity catalog.
func editorSceneryBrushMenuItem() ctxMenuItem {
	if Editor.BrushActive {
		return ctxMenuItem{
			Label:   "Stop brushing " + world.ModelNameForPath(Editor.BrushModel),
			Do:      editorDisarmBrush,
			Tooltip: "Put the scenery brush away. Escape does the same.",
		}
	}
	if Editor.BrushModel == "" {
		return ctxMenuItem{
			Label:    "Brush scenery",
			Disabled: true,
			Tooltip:  "Pick a model under Add... > Scenery first.",
		}
	}
	return ctxMenuItem{
		Label:   "Brush " + world.ModelNameForPath(Editor.BrushModel),
		Do:      func() { editorArmBrush(Editor.BrushModel) },
		Tooltip: "A ghost follows the cursor and every click drops a copy.\nStays on until Escape.",
	}
}

// sceneryDrawnCount reports how many of the level's props the current view is
// drawing, for the panel's own line about what a scatter is costing. Read from
// the renderer rather than counted here, so the number is the one that actually
// went through the frustum.
func sceneryDrawnCount() int { return render.DebugStats.Scenery }
