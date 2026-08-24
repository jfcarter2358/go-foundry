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

// editor3DSelKind is what a 3D click has left selected. The five are mutually
// exclusive by construction: every selection path in Editor3DHandleClick and
// setVertexSelection clears the other four.
type editor3DSelKind int

const (
	sel3DNone editor3DSelKind = iota
	sel3DVertices
	sel3DSurface // a sector's floor, ceiling, wall or portal
	sel3DEntity
	sel3DPrimitive
)

func editor3DSelection() editor3DSelKind {
	lvl := Editor.level
	if lvl == nil {
		return sel3DNone
	}
	switch {
	case editor3DActiveEntity() != nil:
		return sel3DEntity
	case editor3DActivePrimitiveIdx() >= 0:
		return sel3DPrimitive
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
	case sel3DEntity:
		return fmt.Sprintf("Entity — %s", editor3DActiveEntity().ID)

	case sel3DPrimitive:
		_, p := editor3DActivePrimitive()
		return fmt.Sprintf("Primitive — %s (%s)", p.ID, primitiveTypeName(p.Kind))

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

func editor3DActiveEntity() *Entity {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveEntityIdx < 0 || Editor.ActiveEntityIdx >= len(lvl.Entities) {
		return nil
	}
	return &lvl.Entities[Editor.ActiveEntityIdx]
}

// editor3DActivePrimitiveIdx is the selected primitive's index, or -1. Split
// out from editor3DActivePrimitive because several callers want the index for
// the reload helpers, which take it rather than the primitive.
func editor3DActivePrimitiveIdx() int {
	lvl := Editor.level
	if lvl == nil || Editor.ActivePrimitiveIdx < 0 ||
		Editor.ActiveChunkIdx < 0 || Editor.ActiveChunkIdx >= len(lvl.Chunks) {
		return -1
	}
	if Editor.ActivePrimitiveIdx >= len(lvl.Chunks[Editor.ActiveChunkIdx].Primitives) {
		return -1
	}
	return Editor.ActivePrimitiveIdx
}

func editor3DActivePrimitive() (*Chunk, *Primitive) {
	idx := editor3DActivePrimitiveIdx()
	if idx < 0 {
		return nil, nil
	}
	ch := &Editor.level.Chunks[Editor.ActiveChunkIdx]
	return ch, &ch.Primitives[idx]
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

// editor3DSurfaceRefs resolves the selected surface. False when the selection
// is not a surface, or when its indices no longer point at live geometry.
func editor3DSurfaceRefs() (surface3DRefs, bool) {
	ch, s := editor3DActiveSector()
	if ch == nil || s == nil || Editor.ActiveSurface == "" {
		return surface3DRefs{}, false
	}
	r := surface3DRefs{ch: ch, s: s, kind: Editor.ActiveSurface}

	switch Editor.ActiveSurface {
	case "floor":
		r.Texture, r.Smooth = &s.Floor.Texture, &s.Floor.Smooth
		r.On, r.DoubleSided, r.Terrain = &s.Floor.Enabled, &s.Floor.DoubleSided, &s.Floor.Terrain
	case "ceiling":
		r.Texture, r.Smooth = &s.Ceiling.Texture, &s.Ceiling.Smooth
		r.On, r.DoubleSided, r.Terrain = &s.Ceiling.Enabled, &s.Ceiling.DoubleSided, &s.Ceiling.Terrain
	case "wall":
		if Editor.ActiveEdgeIdx < 0 || Editor.ActiveEdgeIdx >= len(s.Walls) {
			return surface3DRefs{}, false
		}
		w := &s.Walls[Editor.ActiveEdgeIdx]
		r.Texture, r.Smooth = &w.Texture, &w.Smooth
		r.On, r.DoubleSided, r.Collision, r.Terrain = &w.Visible, &w.DoubleSided, &w.Collision, &w.Terrain
	case "portal":
		if Editor.ActiveEdgeIdx < 0 || Editor.ActiveEdgeIdx >= len(s.Portals) {
			return surface3DRefs{}, false
		}
		p := &s.Portals[Editor.ActiveEdgeIdx]
		// A portal is a hole: its only surface is the optional gap quad, so
		// that is what "texture" means here, and there is nothing to smooth.
		r.Texture, r.On, r.DoubleSided = &p.GapTexture, &p.Visible, &p.DoubleSided
	default:
		return surface3DRefs{}, false
	}
	return r, true
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

// editor3DSetSurfaceTexture repaints the selected surface.
func editor3DSetSurfaceTexture(name string) {
	r, ok := editor3DSurfaceRefs()
	if !ok || r.Texture == nil {
		return
	}
	_ = ensureTextureLoaded(name)
	EditorPushUndo()
	*r.Texture = name
	r.rebuild()
}

// editor3DToggleSurfaceFlag flips one of the selected surface's booleans. The
// caller picks the field out of the refs, so this stays one undo-and-rebuild
// path rather than one per flag.
func editor3DToggleSurfaceFlag(pick func(surface3DRefs) *bool) {
	r, ok := editor3DSurfaceRefs()
	if !ok {
		return
	}
	field := pick(r)
	if field == nil {
		return
	}
	EditorPushUndo()
	*field = !*field
	r.rebuild()
}

// editor3DConvertSelectedEdge turns the selected wall into a portal or back.
func editor3DConvertSelectedEdge(to string) {
	r, ok := editor3DSurfaceRefs()
	if !ok || (r.kind != "wall" && r.kind != "portal") || r.kind == to {
		return
	}
	EditorPushUndo()
	convertEdge(r.ch, r.s, r.kind, Editor.ActiveEdgeIdx, to)
	Editor.ActiveSurface = Editor.ActiveEdgeKind
	_ = reloadSector(r.ch, r.s)
	r.ch.RebuildGaps()
	editorSetStatus("Converted the edge to a %s", to)
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
	case sel3DVertices, sel3DEntity, sel3DPrimitive:
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

	case sel3DEntity:
		e := editor3DActiveEntity()
		EditorPushUndo()
		e.Position = snapVec3(e.Position, step)
		editorSetStatus("Snapped %s to the grid", e.ID)

	case sel3DPrimitive:
		ch, p := editor3DActivePrimitive()
		EditorPushUndo()
		p.Position = snapVec3(p.Position, step)
		rebuildPrimitiveIndices(ch)
		editorSetStatus("Snapped %s to the grid", p.ID)
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

// ---------- entities and primitives ----------

// editor3DSetEntityTexture repaints the selected entity's untextured faces.
// The change has to go back through the model reader — which submesh the
// fallback applies to is decided at load time.
func editor3DSetEntityTexture(name string) {
	e := editor3DActiveEntity()
	if e == nil {
		return
	}
	EditorPushUndo()
	e.TexturePath = name
	if err := e.Load(); err != nil {
		editorSetStatus("Entity %q: %v", e.ID, err)
	}
}

func editor3DSetPrimitiveTexture(name string) {
	ch, p := editor3DActivePrimitive()
	if ch == nil {
		return
	}
	_ = ensureTextureLoaded(name)
	EditorPushUndo()
	p.Texture = name
	reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
}

func editor3DSetPrimitiveKind(kind PrimitiveType) {
	ch, p := editor3DActivePrimitive()
	if ch == nil || p.Kind == kind {
		return
	}
	EditorPushUndo()
	p.Kind = kind
	reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
}

// editor3DTogglePrimitiveFlag flips one of the selected primitive's booleans
// and rebuilds it, on the same shape as editor3DToggleSurfaceFlag.
func editor3DTogglePrimitiveFlag(pick func(*Primitive) *bool, rebuild bool) {
	ch, p := editor3DActivePrimitive()
	if ch == nil {
		return
	}
	field := pick(p)
	if field == nil {
		return
	}
	EditorPushUndo()
	*field = !*field
	if rebuild {
		reloadPrimitive(ch, Editor.ActivePrimitiveIdx)
	}
}

// editor3DFaceCamera points the selected spawn marker where the camera looks.
func editor3DFaceCamera() {
	_, p := editor3DActivePrimitive()
	if p == nil || !isSpawnPoint(p) {
		return
	}
	EditorPushUndo()
	p.Rotation = spawnRotationFromCamera()
}

// editor3DAddPrimitiveAt drops a primitive of the named type at pos. It goes
// through the catalog's own placement path, so a primitive added from the menu
// is identical to one placed from the panel — same defaults, same undo, same
// "it ends up selected".
func editor3DAddPrimitiveAt(typeName string, pos mgl32.Vec3) {
	prev := Editor.PrimPlacementType
	Editor.PrimPlacementType = typeName
	placeSelectedPrimitiveAt(pos)
	Editor.PrimPlacementType = prev
}

// ---------- panel ----------

// editor3DFocusPanel asks the side panel to open and scroll to the selection's
// own section — the menu's way of saying "and now show me the numbers".
func editor3DFocusPanel(section string) { Editor.PanelFocus = section }
