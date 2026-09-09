package editor

// editor_3d_actions.go — the 3D editor's selection-scoped operations.
//
// These are the actions the right-click menu (editor_3d_context_menu.go)
// offers, kept out of it for the same reason the model editor's are: the menu
// should describe *what* is on offer, not carry a second implementation of it
// beside the panel's. Each one resolves the selection itself, brackets its own
// undo, and rebuilds whatever geometry it invalidated.
//
// Nothing here reaches for a selection it was not given: every function starts
// from the Editor.Active* indices, so a stale menu row — one built a frame
// before a chunk was deleted — finds nothing and does nothing rather than
// editing whatever moved into that index.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/world"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// ---------- what is selected ----------

// editor3DSelKind is what a 3D click has left selected. They are mutually
// exclusive by construction: every selection path in Editor3DHandleClick and
// setVertexSelection clears the others.
type editor3DSelKind int

const (
	sel3DNone editor3DSelKind = iota
	sel3DVertices
	sel3DSurface // one or more brush faces (edit mode)
	sel3DBrush   // one or more whole brushes (object mode)
	sel3DSpawn
	sel3DEntity
	sel3DVolume
	sel3DPrefab
	sel3DSprite
	sel3DScenery
	sel3DEmitter
	sel3DSound
)

func editor3DSelection() editor3DSelKind {
	lvl := Editor.level
	if lvl == nil {
		return sel3DNone
	}
	switch {
	case editor3DActiveSpawn() != nil:
		return sel3DSpawn
	case activeEntity() != nil:
		return sel3DEntity
	case activeVolume() != nil:
		return sel3DVolume
	case activePrefab() != nil:
		return sel3DPrefab
	case activeSprite() != nil:
		return sel3DSprite
	case activeScenery() != nil:
		return sel3DScenery
	case activeEmitter() != nil:
		return sel3DEmitter
	case activeSound() != nil:
		return sel3DSound
	case len(Editor.SelectedVertices) > 0:
		return sel3DVertices
	case len(Editor.SelectedBrushes) > 0:
		if brushSelValid(Editor.SelectedBrushes[0]) {
			return sel3DBrush
		}
	case len(Editor.SelectedFaces) > 0:
		if faceSelFace(Editor.SelectedFaces[0]) != nil {
			return sel3DSurface
		}
	}
	return sel3DNone
}

func editor3DHasSelection() bool { return editor3DSelection() != sel3DNone }

// editor3DSelectionSummary names the selection for the menu's heading.
func editor3DSelectionSummary() string {
	switch editor3DSelection() {
	case sel3DSpawn:
		return "Spawn point"

	case sel3DEntity:
		e := activeEntity()
		if e.Type != "" {
			return fmt.Sprintf("Entity %s (%s)", e.ID, e.Type)
		}
		return fmt.Sprintf("Entity %s", e.ID)

	case sel3DVolume:
		v := activeVolume()
		return fmt.Sprintf("Volume %s (%s)", v.ID, world.VolumeShapeName(v.Shape))

	case sel3DPrefab:
		p := activePrefab()
		if p.File == "" {
			return fmt.Sprintf("Prefab %s (no file)", p.ID)
		}
		return fmt.Sprintf("Prefab %s (%s)", p.ID, p.File)

	case sel3DSprite:
		sp := activeSprite()
		return fmt.Sprintf("Sprite %s (%s)", sp.ID, gfx.BillboardModeName(sp.Billboard))

	case sel3DScenery:
		sc := activeScenery()
		if sc.Model == "" {
			return fmt.Sprintf("Scenery %s (no model)", sc.ID)
		}
		return fmt.Sprintf("Scenery %s (%s)", sc.ID, world.ModelNameForPath(sc.Model))

	case sel3DEmitter:
		em := activeEmitter()
		if em.Effect == "" {
			return fmt.Sprintf("Emitter %s (no effect)", em.ID)
		}
		return fmt.Sprintf("Emitter %s (%s)", em.ID, em.Effect)

	case sel3DSound:
		sd := activeSound()
		if sd.Clip == "" {
			return fmt.Sprintf("Sound %s (no clip)", sd.ID)
		}
		return fmt.Sprintf("Sound %s (%s)", sd.ID, sd.Clip)

	case sel3DVertices:
		if n := len(Editor.SelectedVertices); n > 1 {
			return fmt.Sprintf("%d vertices", n)
		}
		primary := Editor.SelectedVertices[0]
		_, b := vertexSelBrush(primary)
		if b == nil {
			return fmt.Sprintf("Vertex #%d", primary.vertIdx)
		}
		return fmt.Sprintf("Vertex #%d — brush %s", primary.vertIdx, b.ID)

	case sel3DSurface:
		if n := selectedFaceCount(); n > 1 {
			return fmt.Sprintf("%d faces", n)
		}
		primary := Editor.SelectedFaces[0]
		_, b := faceSelBrush(primary)
		if b == nil {
			return fmt.Sprintf("Face #%d", primary.faceIdx)
		}
		return fmt.Sprintf("Face #%d — brush %s", primary.faceIdx, b.ID)

	case sel3DBrush:
		if n := len(Editor.SelectedBrushes); n > 1 {
			return fmt.Sprintf("%d brushes", n)
		}
		primary := Editor.SelectedBrushes[0]
		_, b := vertexSelBrush(vertexSel{chunkIdx: primary.chunkIdx, brushIdx: primary.brushIdx})
		if b == nil {
			return "Brush"
		}
		return fmt.Sprintf("Brush %s", b.ID)
	}
	return "Nothing selected"
}

// ---------- selection accessors ----------

// editor3DActiveSpawn is the level's spawn point when it is what a click left
// selected, or nil.
func editor3DActiveSpawn() *world.SpawnPoint {
	lvl := Editor.level
	if lvl == nil || !Editor.SpawnSelected {
		return nil
	}
	return lvl.Spawn
}

func editor3DActiveBrush() (*world.Chunk, *world.Brush) {
	lvl := ws().level
	if lvl == nil || ws().ActiveChunkIdx < 0 || ws().ActiveChunkIdx >= len(lvl.Chunks) {
		return nil, nil
	}
	ch := &lvl.Chunks[ws().ActiveChunkIdx]
	if ws().ActiveBrushIdx < 0 || ws().ActiveBrushIdx >= len(ch.Brushes) {
		return ch, nil
	}
	return ch, &ch.Brushes[ws().ActiveBrushIdx]
}

// ---------- the selected surface ----------

// surface3DRefs points at the fields of the selected face that the menu can
// toggle.
//
// A sector's six kinds of face each had a different set of these — a portal
// had no texture of its own, a floor no collision flag — so the struct carried
// nil pointers and the menu greyed rows out per kind. Every brush face has
// every field, so they are all non-nil and the menu has no kinds to special-
// case.
//
// The pointers are into the level's slices and are only good for the frame they
// were taken in, which is all they need to be: the menu rebuilds its rows every
// frame it is open, and a row's action runs during that same frame.
type surface3DRefs struct {
	ch *world.Chunk
	b  *world.Brush
	f  *world.BrushFace

	Texture     *string
	Smooth      *bool
	On          *bool // drawn: the inverse of the face's own NoDraw
	DoubleSided *bool
	Collision   *bool // the inverse of NoCollide
}

// editor3DSurfaceRefs resolves the primary selected face — the one the side
// panel shows and the one a toggle reads its "before" value from. False when
// the selection is empty, or when its indices no longer point at live
// geometry.
func editor3DSurfaceRefs() (surface3DRefs, bool) {
	if len(ws().SelectedFaces) == 0 {
		return surface3DRefs{}, false
	}
	return editor3DSurfaceRefsFor(ws().SelectedFaces[0])
}

// editor3DSurfaceRefsFor resolves one named face's editable fields.
func editor3DSurfaceRefsFor(sel faceSel) (surface3DRefs, bool) {
	ch, b := faceSelBrush(sel)
	f := faceSelFace(sel)
	if ch == nil || b == nil || f == nil {
		return surface3DRefs{}, false
	}
	return surface3DRefs{
		ch: ch, b: b, f: f,
		Texture:     &f.Texture,
		Smooth:      &f.Smooth,
		DoubleSided: &f.DoubleSided,
		// NoDraw/NoCollide are stored the way round that makes the zero value
		// the common case (drawn, solid); the menu asks the other way round,
		// so these two are handled by name rather than by pointer — see
		// faceDrawn/faceSolid below.
	}, true
}

// faceDrawn and faceSolid read the two inverted flags the way the menu asks
// about them, and their setters write them back inverted. Kept as functions
// rather than pointers into the struct because the sense differs from what is
// stored: a face's zero value is drawn and solid.
func faceDrawn(f *world.BrushFace) bool       { return !f.NoDraw }
func faceSolid(f *world.BrushFace) bool       { return !f.NoCollide }
func setFaceDrawn(f *world.BrushFace, v bool) { f.NoDraw = !v }
func setFaceSolid(f *world.BrushFace, v bool) { f.NoCollide = !v }

// editor3DForEachSelectedFace applies fn to every selected face and then
// rebuilds the geometry it invalidated.
//
// The two halves are separate because they answer to different constraints. The
// edit is pure bookkeeping over the level's slices; the rebuild re-uploads
// meshes and needs a GL context. Keeping them apart also stops a sector with
// eight selected walls from being reloaded eight times — it is rebuilt once,
// after every face on it has been written.
func editor3DForEachSelectedFace(fn func(surface3DRefs)) int {
	n := applySelectedFaceEdits(fn)
	rebuildSelectedFaceBrushes()
	return n
}

// applySelectedFaceEdits runs fn over every selected face's refs, in selection
// order, and reports how many it reached. It rebuilds nothing.
//
// The refs are resolved one at a time rather than collected up front because
// they are pointers into the level's slices — see surface3DRefs' own note about
// how long they stay good for.
func applySelectedFaceEdits(fn func(surface3DRefs)) int {
	touched := 0
	for _, sel := range ws().SelectedFaces {
		r, ok := editor3DSurfaceRefsFor(sel)
		if !ok {
			continue
		}
		fn(r)
		touched++
	}
	return touched
}

// rebuildSelectedFaceBrushes reloads every brush the face selection touches,
// once each — eight selected faces of one brush cost one rebuild, not eight.
func rebuildSelectedFaceBrushes() {
	lvl := ws().level
	if lvl == nil {
		return
	}
	done := map[[2]int]struct{}{}
	chunks := map[int]struct{}{}
	for _, sel := range ws().SelectedFaces {
		_, b := faceSelBrush(sel)
		if b == nil {
			continue
		}
		key := [2]int{sel.chunkIdx, sel.brushIdx}
		if _, seen := done[key]; seen {
			continue
		}
		done[key] = struct{}{}
		_ = b.Load()
		chunks[sel.chunkIdx] = struct{}{}
	}
	for ci := range chunks {
		if ci >= 0 && ci < len(lvl.Chunks) {
			lvl.Chunks[ci].RecomputeBounds()
		}
	}
	// Once for the whole selection rather than once per chunk — see
	// editorChunkGeometryChanged for what this is and why omitting it reads as
	// collision being off.
	world.RebuildSpatialHash(lvl)
}

// rebuild is the bookkeeping every face edit owes: rebuild the brush's
// geometry, and roll its bounds up to the chunk in case the edit moved them —
// and re-bucket the chunk, since those bounds are what the spatial hash is
// keyed on.
func (r surface3DRefs) rebuild() {
	_ = r.b.Load()
	editorChunkGeometryChanged(r.ch)
}

// editor3DSetSurfaceTexture repaints every selected surface — retexturing a
// room being the reason faces became multi-selectable in the first place.
// Surfaces with no texture of their own (a portal's gap quad aside) are skipped
// rather than blocking the rest.
func editor3DSetSurfaceTexture(name string) {
	if _, ok := editor3DSurfaceRefs(); !ok {
		return
	}
	_ = gfx.EnsureTextureLoaded(name)
	ws().pushUndo()
	n := editor3DForEachSelectedFace(func(r surface3DRefs) {
		if r.Texture != nil {
			*r.Texture = name
		}
	})
	if name == "" {
		ws().setStatus("Cleared the texture on %d face(s)", n)
	} else {
		ws().setStatus("Textured %d face(s) with %s", n, name)
	}
}

// editor3DToggleSurfaceFlag flips one of the selected surfaces' booleans. The
// caller picks the field out of the refs, so this stays one undo-and-rebuild
// path rather than one per flag.
//
// Across a multi-selection every face is set to the *same* value — the opposite
// of the primary's — rather than each being flipped independently. Independent
// flips would scatter a mixed selection into the same mess it started as, and
// "turn collision off for these twelve walls" is what the action is for.
func editor3DToggleSurfaceFlag(pick func(surface3DRefs) *bool) {
	r, ok := editor3DSurfaceRefs()
	if !ok {
		return
	}
	field := pick(r)
	if field == nil {
		return
	}
	want := !*field
	ws().pushUndo()
	editor3DForEachSelectedFace(func(r surface3DRefs) {
		if f := pick(r); f != nil {
			*f = want
		}
	})
}

// editor3DToggleFaceDrawn and editor3DToggleFaceSolid flip the two inverted
// flags across the selection. They are separate from editor3DToggleSurfaceFlag
// because the sense differs from what is stored — NoDraw/NoCollide are the way
// round that makes the zero value the common case (drawn, solid), while the
// menu asks the other way round — so there is no *bool in the refs to hand it.
//
// Like the flag toggle, every face is set to the same value: the opposite of
// the primary's, rather than each flipped independently. "Turn collision off
// for these twelve faces" is what the action is for.
func editor3DToggleFaceDrawn() {
	r, ok := editor3DSurfaceRefs()
	if !ok {
		return
	}
	want := !faceDrawn(r.f)
	ws().pushUndo()
	editor3DForEachSelectedFace(func(r surface3DRefs) { setFaceDrawn(r.f, want) })
}

func editor3DToggleFaceSolid() {
	r, ok := editor3DSurfaceRefs()
	if !ok {
		return
	}
	want := !faceSolid(r.f)
	ws().pushUndo()
	editor3DForEachSelectedFace(func(r surface3DRefs) { setFaceSolid(r.f, want) })
}

// A brush face is not convertible into anything: the wall/portal split it
// replaced was a sector's way of saying "this edge is open", and open space is
// now simply the absence of a brush. Cutting a doorway is a geometry edit —
// carve the brush, or move one out of the way — not a flag on a face.

// ---------- vertices ----------

// editor3DSelectBrushVertices selects every corner of the brush the current
// selection belongs to — the whole solid, ready to be moved or scaled as one.
//
// This is the brush counterpart of selecting a sector's floor ring. A sector
// needed the ring restricted to one side, because a floor handle and the
// ceiling handle above it shared a chunk point and moving both would warp the
// room at double speed. A brush owns each corner outright, so there is nothing
// to halve: taking all of them is exactly right.
func editor3DSelectBrushVertices() {
	ci, bi := activeBrushIndices()
	if bi < 0 {
		return
	}
	ch, b := editor3DActiveBrush()
	if ch == nil || b == nil {
		return
	}
	// Whichever mesh the current context addresses: the hull in edit mode, the
	// detail one while its surface is being carved. Same resolution a click
	// goes through, so "select all" selects what a click could have.
	m, detail, ok := pickMeshFor(ci, bi, b)
	if !ok || m == nil || len(m.Verts) == 0 {
		return
	}
	sels := make([]vertexSel, 0, len(m.Verts))
	for vi := range m.Verts {
		sels = append(sels, vertexSel{chunkIdx: ci, brushIdx: bi, vertIdx: vi, detail: detail})
	}
	setVertexSelection(sels)
	ws().setStatus("Selected %d vertices of %s", len(sels), b.ID)
}

// activeBrushIndices resolves which brush the selection is working on, from
// whichever kind of selection is live: object mode names one outright, and in
// edit mode a face and a vertex each name their own. Returns a brush index of
// -1 when none does.
func activeBrushIndices() (chunkIdx, brushIdx int) {
	if len(ws().SelectedBrushes) > 0 {
		b := ws().SelectedBrushes[0]
		return b.chunkIdx, b.brushIdx
	}
	if len(ws().SelectedFaces) > 0 {
		f := ws().SelectedFaces[0]
		return f.chunkIdx, f.brushIdx
	}
	if len(ws().SelectedVertices) > 0 {
		v := ws().SelectedVertices[0]
		return v.chunkIdx, v.brushIdx
	}
	return ws().ActiveChunkIdx, ws().ActiveBrushIdx
}

// ---------- snapping ----------

// editor3DCanSnap reports whether the selection is something snapping can move.
func editor3DCanSnap() bool {
	switch editor3DSelection() {
	case sel3DVertices, sel3DBrush, sel3DSpawn, sel3DEntity, sel3DVolume,
		sel3DPrefab, sel3DSprite, sel3DScenery, sel3DEmitter, sel3DSound:
		return Editor.StepSize > 0
	}
	return false
}

// editor3DSnapSelectionToGrid rounds the selection onto the grid, whether or
// not "snap to grid" is on — that switch decides what *dragging* does, and this
// is the way to line up something already placed off the grid.
func editor3DSnapSelectionToGrid() {
	step := Editor.StepSize
	if step <= 0 {
		return
	}
	switch editor3DSelection() {
	case sel3DVertices:
		EditorPushUndo()
		setSelectedVertexLocals(func(_ int, _ vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
			return snapVec3(cur, step)
		})
		editorSetStatus("Snapped %d vertices to the grid", len(Editor.SelectedVertices))

	case sel3DBrush:
		// Every corner of every selected brush, through the same write path a
		// vertex selection uses — gizmoVertexSels is already reporting them
		// (editor_brush_sel.go), so this needs no loop of its own. Rounding
		// each corner rather than the brush's centre is what makes a
		// box-shaped brush land ON the grid rather than merely aligned to it.
		EditorPushUndo()
		setSelectedVertexLocals(func(_ int, _ vertexSel, cur mgl32.Vec3) mgl32.Vec3 {
			return snapVec3(cur, step)
		})
		editorSetStatus("Snapped %d brush(es) to the grid", len(Editor.SelectedBrushes))

	case sel3DSpawn:
		sp := editor3DActiveSpawn()
		EditorPushUndo()
		sp.Position = snapVec3(sp.Position, step)
		editorSetStatus("Snapped the spawn point to the grid")

	case sel3DEntity:
		e := activeEntity()
		EditorPushUndo()
		e.Position = snapVec3(e.Position, step)
		editorSetStatus("Snapped entity %q to the grid", e.ID)

	case sel3DVolume:
		// The position only. A volume's size is its half-extents, and rounding
		// those onto the grid as well would resize a trigger someone asked to
		// have moved.
		v := activeVolume()
		EditorPushUndo()
		v.Position = snapVec3(v.Position, step)
		editorSetStatus("Snapped volume %q to the grid", v.ID)

	case sel3DPrefab:
		// The position only. A prefab's scale is a factor on the whole group,
		// and rounding it onto the world grid would resize something someone
		// asked to have moved.
		p := activePrefab()
		EditorPushUndo()
		p.Position = snapVec3(p.Position, step)
		restampActivePrefab()
		editorSetStatus("Snapped prefab %q to the grid", p.ID)

	case sel3DSprite:
		// The position only, for the reason a volume's size is left alone: a
		// sprite's size is its width and height, and rounding those onto the
		// grid would resize a quad someone asked to have moved.
		sp := activeSprite()
		EditorPushUndo()
		sp.Position = snapVec3(sp.Position, step)
		editorSetStatus("Snapped sprite %q to the grid", sp.ID)

	case sel3DScenery:
		// The position only, on the volume's rule: a prop's scale is a factor
		// on its model, and rounding that onto the world grid would resize
		// something someone asked to have moved.
		sc := activeScenery()
		EditorPushUndo()
		sc.Position = snapVec3(sc.Position, step)
		editorSetStatus("Snapped scenery %q to the grid", sc.ID)

	case sel3DEmitter:
		em := activeEmitter()
		EditorPushUndo()
		em.Position = snapVec3(em.Position, step)
		editorSetStatus("Snapped emitter %q to the grid", em.ID)

	case sel3DSound:
		sd := activeSound()
		EditorPushUndo()
		sd.Position = snapVec3(sd.Position, step)
		editorSetStatus("Snapped sound %q to the grid", sd.ID)
	}
}

// snapVec3 rounds every component onto a multiple of step.
func snapVec3(v mgl32.Vec3, step float32) mgl32.Vec3 {
	return mgl32.Vec3{
		snapValue(v.X(), step),
		snapValue(v.Y(), step),
		snapValue(v.Z(), step),
	}
}

// ---------- spawn point ----------

// editor3DFaceCamera points the spawn marker where the camera looks.
func editor3DFaceCamera() {
	sp := editor3DActiveSpawn()
	if sp == nil {
		return
	}
	EditorPushUndo()
	sp.Rotation = spawnRotationFromCamera()
}

// editor3DAddSpawnPointAt drops the level's spawn point at pos. It goes through
// the same placement path the panel's button uses, so a spawn placed from the
// menu is identical to one placed from the panel — same undo, same "it ends up
// selected".
func editor3DAddSpawnPointAt(pos mgl32.Vec3) { placeSpawnPointAt(pos) }

// ---------- entities ----------

// editor3DAddEntityAt drops an instance of the catalog's current definition at
// pos, the same way the panel's placement button does. The menu offers it as
// "add one here" rather than arming a mode, since the menu was opened at a
// point and that point is where it should land.
func editor3DAddEntityAt(typeName string, pos mgl32.Vec3) {
	Editor.EntityPlaceType = typeName
	Editor.EntityPlacementMode = true
	placeEntityAt(pos)
}

// ---------- panel ----------

// editor3DFocusPanel asks the side panel to open and scroll to the selection's
// own section — the menu's way of saying "and now show me the numbers".
func editor3DFocusPanel(section string) { Editor.PanelFocus = section }

// editor3DSelectBrushFaces replaces the selection with every face of the brush
// the primary belongs to.
//
// This is the shortcut behind the reason faces became multi-selectable —
// retexturing a whole solid, or turning its collision off, without ctrl+
// clicking each side in turn and missing one behind the camera.
func editor3DSelectBrushFaces() {
	ci, bi := activeBrushIndices()
	ch, b := editor3DActiveBrush()
	if ch == nil || b == nil {
		return
	}
	m, detail, ok := pickMeshFor(ci, bi, b)
	if !ok || m == nil || len(m.Faces) == 0 {
		return
	}
	sels := make([]faceSel, 0, len(m.Faces))
	for fi := range m.Faces {
		sels = append(sels, faceSel{chunkIdx: ci, brushIdx: bi, faceIdx: fi, detail: detail})
	}
	setFaceSelection(sels)
	ws().setStatus("Selected %d faces of %s", len(sels), b.ID)
}

// editor3DSelectBrushSideFaces is editor3DSelectBrushFaces narrowed to the
// upright faces — the common case, since a solid's sides usually want one
// texture and its top and bottom another.
//
// "Upright" is a threshold on the face normal's Y component rather than an
// authored kind, because a brush face has no kind: a wedge's slope is a side
// or a roof depending only on how steep it is.
func editor3DSelectBrushSideFaces() {
	ci, bi := activeBrushIndices()
	ch, b := editor3DActiveBrush()
	if ch == nil || b == nil {
		return
	}
	m, detail, ok := pickMeshFor(ci, bi, b)
	if !ok || m == nil || len(m.Faces) == 0 {
		return
	}
	sels := make([]faceSel, 0, len(m.Faces))
	for fi := range m.Faces {
		n := m.FaceNormal(&m.Faces[fi])
		if math.Abs(float64(n.Y())) > uprightFaceMaxY {
			continue
		}
		sels = append(sels, faceSel{chunkIdx: ci, brushIdx: bi, faceIdx: fi, detail: detail})
	}
	if len(sels) == 0 {
		return
	}
	setFaceSelection(sels)
	editorSetStatus("Selected %d side faces of %s", len(sels), b.ID)
}

// uprightFaceMaxY is how far a face normal may tilt off horizontal and still
// count as a side: cos(60°), so anything steeper than a 30° slope from vertical
// is a wall rather than a floor or a roof. The same threshold the collision
// resolver uses to decide what can be walked up (internal/collision).
const uprightFaceMaxY = 0.5

// editorDeleteSelectedBrushes removes every brush the face selection names,
// highest index first so the earlier removals do not renumber the later ones.
// Caller brackets the undo.
func editorDeleteSelectedBrushes() {
	lvl := ws().level
	if lvl == nil {
		return
	}
	// Deduplicated: eight selected faces of one brush delete one brush. Both
	// selections feed it, so the same call serves object mode (where the brush
	// itself is selected) and edit mode (where one of its faces is).
	perChunk := map[int]map[int]struct{}{}
	mark := func(chunkIdx, brushIdx int) {
		if perChunk[chunkIdx] == nil {
			perChunk[chunkIdx] = map[int]struct{}{}
		}
		perChunk[chunkIdx][brushIdx] = struct{}{}
	}
	for _, sel := range ws().SelectedBrushes {
		if brushSelValid(sel) {
			mark(sel.chunkIdx, sel.brushIdx)
		}
	}
	for _, sel := range ws().SelectedFaces {
		if _, b := faceSelBrush(sel); b == nil {
			continue
		}
		mark(sel.chunkIdx, sel.brushIdx)
	}
	n := 0
	for ci, brushes := range perChunk {
		ch := &lvl.Chunks[ci]
		idxs := make([]int, 0, len(brushes))
		for bi := range brushes {
			idxs = append(idxs, bi)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(idxs)))
		for _, bi := range idxs {
			if bi < 0 || bi >= len(ch.Brushes) {
				continue
			}
			ch.Brushes[bi].Release()
			ch.Brushes = append(ch.Brushes[:bi], ch.Brushes[bi+1:]...)
			n++
		}
		editorChunkGeometryChanged(ch)
	}
	clearBrushSelection()
	clearFaceSelection()
	clearVertexSelection()
	ws().setStatus("Deleted %d brush(es)", n)
}
