package main

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
	"strings"

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
	sel3DSurface // a sector's floor, ceiling, wall or portal
	sel3DSpawn
	sel3DEntity
	sel3DVolume
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
	case len(Editor.SelectedVertices) > 0:
		return sel3DVertices
	case Editor.ActiveSurface != "":
		if _, s := editor3DActiveSector(); s != nil {
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
		return fmt.Sprintf("Volume %s (%s)", v.ID, volumeShapeName(v.Shape))

	case sel3DVertices:
		if n := len(Editor.SelectedVertices); n > 1 {
			return fmt.Sprintf("%d vertices", n)
		}
		primary := Editor.SelectedVertices[0]
		which := "ceiling"
		if primary.isFloor {
			which = "floor"
		}
		return fmt.Sprintf("Vertex #%d (%s)", primary.vertIdx, which)

	case sel3DSurface:
		if n := selectedFaceCount(); n > 1 {
			return fmt.Sprintf("%d faces", n)
		}
		_, s := editor3DActiveSector()
		if Editor.ActiveEdgeIdx >= 0 && Editor.ActiveEdgeKind != "" {
			return fmt.Sprintf("%s #%d — sector %s",
				editor3DKindLabel(Editor.ActiveEdgeKind), Editor.ActiveEdgeIdx, s.ID)
		}
		return fmt.Sprintf("%s — sector %s", editor3DKindLabel(Editor.ActiveSurface), s.ID)
	}
	return "Nothing selected"
}

// editor3DKindLabel capitalises a one-word kind name ("wall" → "Wall") for
// display in the menu's heading.
func editor3DKindLabel(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ---------- selection accessors ----------

// editor3DActiveSpawn is the level's spawn point when it is what a click left
// selected, or nil.
func editor3DActiveSpawn() *SpawnPoint {
	lvl := Editor.level
	if lvl == nil || !Editor.SpawnSelected {
		return nil
	}
	return lvl.Spawn
}

func editor3DActiveSector() (*Chunk, *Sector) {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return nil, nil
	}
	ch := &lvl.Chunks[Editor.ActiveChunkIdx]
	if Editor.ActiveSectorIdx < 0 || Editor.ActiveSectorIdx >= len(ch.Sectors) {
		return ch, nil
	}
	return ch, &ch.Sectors[Editor.ActiveSectorIdx]
}

// ---------- the selected surface ----------

// surface3DRefs points at the fields of the selected surface that the menu can
// toggle. A nil pointer means this surface has no such field — a portal has no
// texture of its own, a floor has no collision flag — and the menu greys the
// row out rather than special-casing every kind.
//
// The pointers are into the level's slices and are only good for the frame they
// were taken in, which is all they need to be: the menu rebuilds its rows every
// frame it is open, and a row's action runs during that same frame.
type surface3DRefs struct {
	ch   *Chunk
	s    *Sector
	kind string // "floor", "ceiling", "wall", "portal"

	Texture     *string
	Smooth      *bool
	On          *bool // Enabled on a floor/ceiling, Visible on a wall/portal
	DoubleSided *bool
	Collision   *bool
	Terrain     *bool
}

// editor3DSurfaceRefs resolves the primary selected surface — the one the side
// panel shows and the one a toggle reads its "before" value from. False when the
// selection is not a surface, or when its indices no longer point at live
// geometry.
func editor3DSurfaceRefs() (surface3DRefs, bool) {
	if len(Editor.SelectedFaces) == 0 {
		return surface3DRefs{}, false
	}
	return editor3DSurfaceRefsFor(Editor.SelectedFaces[0])
}

// editor3DSurfaceRefsFor resolves one named face's editable fields.
func editor3DSurfaceRefsFor(sel faceSel) (surface3DRefs, bool) {
	ch, s := faceSelSector(sel)
	if ch == nil || s == nil || sel.kind == "" {
		return surface3DRefs{}, false
	}
	r := surface3DRefs{ch: ch, s: s, kind: sel.kind}

	switch sel.kind {
	case "floor":
		r.Texture, r.Smooth = &s.Floor.Texture, &s.Floor.Smooth
		r.On, r.DoubleSided, r.Terrain = &s.Floor.Enabled, &s.Floor.DoubleSided, &s.Floor.Terrain
	case "ceiling":
		r.Texture, r.Smooth = &s.Ceiling.Texture, &s.Ceiling.Smooth
		r.On, r.DoubleSided, r.Terrain = &s.Ceiling.Enabled, &s.Ceiling.DoubleSided, &s.Ceiling.Terrain
	case "wall":
		if sel.idx < 0 || sel.idx >= len(s.Walls) {
			return surface3DRefs{}, false
		}
		w := &s.Walls[sel.idx]
		r.Texture, r.Smooth = &w.Texture, &w.Smooth
		r.On, r.DoubleSided, r.Collision, r.Terrain = &w.Visible, &w.DoubleSided, &w.Collision, &w.Terrain
	case "portal":
		if sel.idx < 0 || sel.idx >= len(s.Portals) {
			return surface3DRefs{}, false
		}
		p := &s.Portals[sel.idx]
		// A portal is a hole: its only surface is the optional gap quad, so
		// that is what "texture" means here, and there is nothing to smooth.
		r.Texture, r.On, r.DoubleSided = &p.GapTexture, &p.Visible, &p.DoubleSided
	default:
		return surface3DRefs{}, false
	}
	return r, true
}

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
	rebuildSelectedFaceSectors()
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
	for _, sel := range Editor.SelectedFaces {
		r, ok := editor3DSurfaceRefsFor(sel)
		if !ok {
			continue
		}
		fn(r)
		touched++
	}
	return touched
}

// rebuildSelectedFaceSectors reloads every sector the face selection touches,
// once each, and regenerates a chunk's portal gap quads when a portal was among
// them.
func rebuildSelectedFaceSectors() {
	lvl := Editor.level
	if lvl == nil {
		return
	}
	done := map[[2]int]struct{}{}
	gaps := map[int]struct{}{}
	for _, sel := range Editor.SelectedFaces {
		ch, s := faceSelSector(sel)
		if ch == nil || s == nil {
			continue
		}
		if sel.kind == "portal" {
			gaps[sel.chunkIdx] = struct{}{}
		}
		key := [2]int{sel.chunkIdx, sel.sectorIdx}
		if _, seen := done[key]; seen {
			continue
		}
		done[key] = struct{}{}
		_ = reloadSector(ch, s)
	}
	for ci := range gaps {
		if ci >= 0 && ci < len(lvl.Chunks) {
			lvl.Chunks[ci].RebuildGaps()
		}
	}
}

// editor3DRebuildSurface is the bookkeeping every surface edit owes: rebuild
// the sector, and the chunk's portal gap quads with it when the edit could have
// changed one.
func (r surface3DRefs) rebuild() {
	_ = reloadSector(r.ch, r.s)
	if r.kind == "portal" {
		r.ch.RebuildGaps()
	}
}

// editor3DSetSurfaceTexture repaints every selected surface — retexturing a
// room being the reason faces became multi-selectable in the first place.
// Surfaces with no texture of their own (a portal's gap quad aside) are skipped
// rather than blocking the rest.
func editor3DSetSurfaceTexture(name string) {
	if _, ok := editor3DSurfaceRefs(); !ok {
		return
	}
	_ = ensureTextureLoaded(name)
	EditorPushUndo()
	n := editor3DForEachSelectedFace(func(r surface3DRefs) {
		if r.Texture != nil {
			*r.Texture = name
		}
	})
	editorSetStatus("Textured %d face(s) with %s", n, name)
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
	EditorPushUndo()
	editor3DForEachSelectedFace(func(r surface3DRefs) {
		if f := pick(r); f != nil {
			*f = want
		}
	})
}

// editor3DConvertSelectedEdge turns every selected wall into a portal, or back.
//
// Conversion renumbers a sector's Walls and Portals, so the faces are tracked
// through it by *polygon* edge index instead — which the sector's outline fixes
// and a conversion cannot move. Each edge is re-resolved from that index right
// before it is converted, and the selection is rebuilt from the same indices
// afterwards, so a run of walls can be opened up in one action.
func editor3DConvertSelectedEdge(to string) {
	type edgeRef struct {
		chunkIdx, sectorIdx, polyIdx int
	}
	var refs []edgeRef
	for _, sel := range Editor.SelectedFaces {
		if !sel.isEdgeFace() || sel.kind == to {
			continue
		}
		_, s := faceSelSector(sel)
		if s == nil {
			continue
		}
		if pi := polyEdgeIdx(s, sel.kind, sel.idx); pi >= 0 {
			refs = append(refs, edgeRef{sel.chunkIdx, sel.sectorIdx, pi})
		}
	}
	if len(refs) == 0 {
		return
	}

	EditorPushUndo()
	for _, ref := range refs {
		ch, s := faceSelSector(faceSel{chunkIdx: ref.chunkIdx, sectorIdx: ref.sectorIdx})
		if s == nil || ref.polyIdx >= len(s.EdgeKinds) {
			continue
		}
		kind, idx := edgeKindAndIndex(s, ref.polyIdx)
		if kind == to {
			continue
		}
		convertEdge(ch, s, kind, idx, to)
		ch.RebuildGaps()
	}

	// Rebuild the selection from the polygon indices, which survived.
	sels := make([]faceSel, 0, len(refs))
	for _, ref := range refs {
		_, s := faceSelSector(faceSel{chunkIdx: ref.chunkIdx, sectorIdx: ref.sectorIdx})
		if s == nil || ref.polyIdx >= len(s.EdgeKinds) {
			continue
		}
		kind, idx := edgeKindAndIndex(s, ref.polyIdx)
		sels = append(sels, edgeFaceSel(ref.chunkIdx, ref.sectorIdx, kind, idx))
	}
	if len(sels) > 0 {
		setFaceSelection(sels)
	} else {
		clearFaceSelection()
	}
	editorSetStatus("Converted %d edge(s) to %s", len(refs), to)
}

// editor3DEditSurfaceIn2D opens the selected surface in the 2D surface editor —
// the panel's "Edit ... Surface" buttons, from the viewport.
func editor3DEditSurfaceIn2D() {
	r, ok := editor3DSurfaceRefs()
	if !ok || r.kind == "portal" {
		return
	}
	EnterSurface2D(r.kind)
}

// ---------- vertices ----------

// editor3DSelectSectorRing selects every vertex handle of the active sector on
// one side — the floor ring or the ceiling one, matching whichever the current
// selection is on.
//
// One ring, not both: a floor handle and the ceiling handle above it share a
// chunk point, so a selection holding both would move that point twice per drag
// and the sector would warp at double speed.
func editor3DSelectSectorRing() {
	ch, s := editor3DActiveSector()
	if ch == nil || s == nil || len(s.PointIndices) == 0 {
		return
	}
	isFloor := Editor.EditFloorVerts
	if len(Editor.SelectedVertices) > 0 {
		isFloor = Editor.SelectedVertices[0].isFloor
	}
	sels := make([]vertexSel, 0, len(s.PointIndices))
	for vi, pIdx := range s.PointIndices {
		if pIdx < 0 || pIdx >= len(ch.Points) {
			continue
		}
		sels = append(sels, vertexSel{
			chunkIdx:  Editor.ActiveChunkIdx,
			sectorIdx: Editor.ActiveSectorIdx,
			vertIdx:   vi,
			pointIdx:  pIdx,
			isFloor:   isFloor,
		})
	}
	if len(sels) == 0 {
		return
	}
	setVertexSelection(sels)
	editorSetStatus("Selected %d vertices of %s", len(sels), s.ID)
}

// ---------- snapping ----------

// editor3DCanSnap reports whether the selection is something snapping can move.
func editor3DCanSnap() bool {
	switch editor3DSelection() {
	case sel3DVertices, sel3DSpawn, sel3DEntity, sel3DVolume:
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
		setSelectedVertexPoints(func(_ int, _ vertexSel, cur mgl32.Vec2) mgl32.Vec2 {
			return mgl32.Vec2{snapValue(cur.X(), step), snapValue(cur.Y(), step)}
		})
		editorSetStatus("Snapped %d vertices to the grid", len(Editor.SelectedVertices))

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

// editor3DSelectSectorFaces replaces the selection with every face of the
// sector the primary belongs to: its floor, its ceiling, and every wall and
// portal around it.
//
// This is the shortcut behind the reason faces became multi-selectable —
// retexturing a room, or turning its collision off, without ctrl+clicking each
// wall in turn and missing one behind the camera.
func editor3DSelectSectorFaces() {
	ch, s := editor3DActiveSector()
	if ch == nil || s == nil {
		return
	}
	ci, si := Editor.ActiveChunkIdx, Editor.ActiveSectorIdx
	sels := []faceSel{surfaceFaceSel(ci, si, "floor"), surfaceFaceSel(ci, si, "ceiling")}
	for i := range s.Walls {
		sels = append(sels, edgeFaceSel(ci, si, "wall", i))
	}
	for i := range s.Portals {
		sels = append(sels, edgeFaceSel(ci, si, "portal", i))
	}
	setFaceSelection(sels)
	editorSetStatus("Selected %d faces of %s", len(sels), s.ID)
}

// editor3DSelectSectorWalls is editor3DSelectSectorFaces narrowed to the
// upright faces — the common case, since a room's walls usually want one
// texture and its floor another.
func editor3DSelectSectorWalls() {
	ch, s := editor3DActiveSector()
	if ch == nil || s == nil || len(s.Walls) == 0 {
		return
	}
	ci, si := Editor.ActiveChunkIdx, Editor.ActiveSectorIdx
	sels := make([]faceSel, 0, len(s.Walls))
	for i := range s.Walls {
		sels = append(sels, edgeFaceSel(ci, si, "wall", i))
	}
	setFaceSelection(sels)
	editorSetStatus("Selected %d walls of %s", len(sels), s.ID)
}
