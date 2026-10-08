package editor

// editor_object_set.go — Ctrl+click multi-selection of placed objects, and
// Ctrl+C / Ctrl+V for objects and brushes.
//
// Every non-brush object is selected through its own Active*Idx, one at a
// time, and the panel shows that one (editor_select_object.go). A set is laid
// over that rather than replacing it: the last object Ctrl+clicked is still
// selected the ordinary way, so the panel shows its properties, and the set
// beside it is what the gizmo, Delete and Copy act on.
//
// The set lives only while the gizmo is gizmoKindGroup. Everything that
// invalidates object indices — an undo, a level load, a delete, picking
// something else — either resets the gizmo kind or goes through
// editorClearObjectSelections, so a set left behind with stale indices is never
// read: objectSet() reports it empty.
//
// The clipboard holds the Raw* form of each object, the shape the level file
// and undo use, so a copy shares nothing with its source — no GPU handles, no
// script data — and survives the source being deleted before the paste.

import (
	"sort"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// gizmoKindGroup is several placed objects at once. Only the move gizmo
// carries it: rotate and scale mean something different for every kind in it.
const gizmoKindGroup = "group"

// objRef names one placed object by kind and index into its level slice.
type objRef struct {
	kind pickKind
	idx  int
}

// objSet is the multi-selection. Read it through objectSet().
var objSet []objRef

// objSetKinds are the kinds a set can hold: the indexed objects with a plain
// world position. Spawn, seal and terrain are one-per-level or not objects;
// waypoints take Ctrl+click for laying a path; lights are capped at eight
// and edited from their own list.
var objSetKinds = map[pickKind]bool{
	pickEntity: true, pickVolume: true, pickSprite: true, pickDecal: true,
	pickScenery: true, pickEmitter: true, pickSound: true, pickEffect: true,
	pickTree: true,
}

// objectSet returns the live multi-selection, or nil.
func objectSet() []objRef {
	if Editor.GizmoKind != gizmoKindGroup || ws().level != Editor.level {
		return nil
	}
	out := objSet[:0:0]
	for _, r := range objSet {
		if objPos(r) != nil {
			out = append(out, r)
		}
	}
	return out
}

// editorClearObjectSet drops the multi-selection.
func editorClearObjectSet() {
	objSet = nil
	if Editor.GizmoKind == gizmoKindGroup {
		Editor.GizmoKind = gizmoKindNone
	}
}

// objPos is the object's authored position, or nil when r names nothing.
func objPos(r objRef) *mgl32.Vec3 {
	lvl := Editor.level
	if lvl == nil || r.idx < 0 {
		return nil
	}
	switch r.kind {
	case pickEntity:
		if r.idx < len(lvl.Entities) {
			return &lvl.Entities[r.idx].Position
		}
	case pickVolume:
		if r.idx < len(lvl.Volumes) {
			return &lvl.Volumes[r.idx].Position
		}
	case pickSprite:
		if r.idx < len(lvl.Sprites) {
			return &lvl.Sprites[r.idx].Position
		}
	case pickDecal:
		if r.idx < len(lvl.Decals) {
			return &lvl.Decals[r.idx].Position
		}
	case pickScenery:
		if r.idx < len(lvl.Scenery) {
			return &lvl.Scenery[r.idx].Position
		}
	case pickEmitter:
		if r.idx < len(lvl.Emitters) {
			return &lvl.Emitters[r.idx].Position
		}
	case pickSound:
		if r.idx < len(lvl.Sounds) {
			return &lvl.Sounds[r.idx].Position
		}
	case pickEffect:
		if r.idx < len(lvl.Effects) {
			return &lvl.Effects[r.idx].Position
		}
	case pickTree:
		if r.idx < len(lvl.Trees) {
			return &lvl.Trees[r.idx].Position
		}
	}
	return nil
}

// activeIdxField is the Active*Idx that selects one object of kind.
func activeIdxField(kind pickKind) *int {
	switch kind {
	case pickEntity:
		return &Editor.ActiveEntityIdx
	case pickVolume:
		return &Editor.ActiveVolumeIdx
	case pickSprite:
		return &Editor.ActiveSpriteIdx
	case pickDecal:
		return &Editor.ActiveDecalIdx
	case pickScenery:
		return &Editor.ActiveSceneryIdx
	case pickEmitter:
		return &Editor.ActiveEmitterIdx
	case pickSound:
		return &Editor.ActiveSoundIdx
	case pickEffect:
		return &Editor.ActiveEffectIdx
	case pickTree:
		return &Editor.ActiveTreeIdx
	}
	return nil
}

// activeObjRef is the one object selected the ordinary way, if it is a kind a
// set can hold.
func activeObjRef() (objRef, bool) {
	for k := range objSetKinds {
		f := activeIdxField(k)
		if f == nil || *f < 0 {
			continue
		}
		r := objRef{kind: k, idx: *f}
		if objPos(r) != nil {
			return r, true
		}
	}
	return objRef{}, false
}

// selectedObjRefs is what Delete and Copy act on: the set, or the one object.
func selectedObjRefs() []objRef {
	if set := objectSet(); len(set) > 0 {
		return set
	}
	if r, ok := activeObjRef(); ok {
		return []objRef{r}
	}
	return nil
}

// editorToggleObjectSelection adds r to the selection, or takes it back out —
// a Ctrl+click on an object. The object selected before it joins the set, so
// click one tree, Ctrl+click another, and both are selected.
func editorToggleObjectSelection(r objRef) {
	set := append([]objRef(nil), selectedObjRefs()...)
	found := false
	for i, s := range set {
		if s == r {
			set = append(set[:i], set[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		set = append(set, r)
	}
	editorSetObjectSelection(set)
}

// editorSetObjectSelection makes set the selection. The last member is
// selected the ordinary way so the panel shows it.
func editorSetObjectSelection(set []objRef) {
	if len(set) == 0 {
		editor3DClearSelection()
		return
	}
	editor3DSelectNonBrushHit(pickHit{kind: set[len(set)-1].kind, idx: set[len(set)-1].idx})
	if len(set) > 1 {
		objSet = set
		Editor.GizmoKind = gizmoKindGroup
		editorSetStatus("%d objects selected", len(set))
	}
}

// objectSetCenter is where the group's gizmo sits: the average position.
func objectSetCenter() (mgl32.Vec3, bool) {
	set := objectSet()
	if len(set) == 0 {
		return mgl32.Vec3{}, false
	}
	var sum mgl32.Vec3
	for _, r := range set {
		sum = sum.Add(*objPos(r))
	}
	return sum.Mul(1 / float32(len(set))), true
}

// captureObjectSetDragStart snapshots every member's position for a move.
func captureObjectSetDragStart() bool {
	set := objectSet()
	c, ok := objectSetCenter()
	if !ok {
		return false
	}
	Editor.GizmoDragWorld = make([]mgl32.Vec3, len(set))
	for i, r := range set {
		Editor.GizmoDragWorld[i] = *objPos(r)
	}
	Editor.GizmoDragStartPos = c
	EditorPushUndo()
	return true
}

// dragObjectSet moves every member by the snapped offset of the centre, so
// the group keeps its layout.
func dragObjectSet(axis int, delta float32) {
	d := axisMoved(Editor.GizmoDragStartPos, axis, delta).Sub(Editor.GizmoDragStartPos)
	for i, r := range objectSet() {
		if i < len(Editor.GizmoDragWorld) {
			*objPos(r) = Editor.GizmoDragWorld[i].Add(d)
		}
	}
}

// drawObjectSetOutlines outlines every member with its kind's own outline,
// pointing the kind's Active*Idx at each in turn.
func drawObjectSetOutlines() {
	for _, r := range objectSet() {
		f := activeIdxField(r.kind)
		prev := *f
		*f = r.idx
		switch r.kind {
		case pickVolume:
			drawSelectedVolumeOutline()
		case pickSprite:
			drawSelectedSpriteOutline(ws().camPos())
		case pickDecal:
			drawSelectedDecalOutline()
		case pickScenery:
			drawSelectedSceneryOutline()
		case pickTree:
			drawSelectedTreeOutline()
		}
		*f = prev
	}
}

// deleteObjectSet deletes every member under one undo entry. Highest index
// first within each kind, so the indices still to go stay valid.
func deleteObjectSet() {
	set := append([]objRef(nil), objectSet()...)
	if len(set) == 0 {
		return
	}
	EditorPushUndo()
	sort.Slice(set, func(i, j int) bool { return set[i].idx > set[j].idx })
	withUndoRedirect(func() {}, func() {
		for _, r := range set {
			*activeIdxField(r.kind) = r.idx
			switch r.kind {
			case pickEntity:
				deleteActiveEntity()
			case pickVolume:
				deleteActiveVolume()
			case pickSprite:
				deleteActiveSprite()
			case pickDecal:
				deleteActiveDecal()
			case pickScenery:
				deleteActiveScenery()
			case pickEmitter:
				deleteActiveEmitter()
			case pickSound:
				deleteActiveSound()
			case pickEffect:
				deleteActiveEffect()
			case pickTree:
				deleteActiveTree()
			}
		}
	})
	editorClearObjectSelections()
	editorSetStatus("Deleted %d objects", len(set))
}

// ---------- copy / paste ----------

// clipBrush is a copied brush and the chunk it came from.
type clipBrush struct {
	chunkID string
	brush   world.Brush
}

// objClipboard is what Ctrl+C took.
type objClipboard struct {
	entities []world.RawEntity
	volumes  []world.RawVolume
	sprites  []world.RawSprite
	decals   []world.RawDecal
	emitters []world.RawEmitter
	scenery  []world.RawScenery
	trees    []world.RawTree
	sounds   []world.RawSound
	effects  []world.RawEffect
	brushes  []clipBrush
	// anchor is the point that lands under the cursor on paste: the copied
	// things' centre in X and Z, and their lowest point in Y, so a pasted
	// group stands on what the cursor is over rather than sinking into it.
	anchor mgl32.Vec3
	count  int
}

var editorClipboard *objClipboard

// editorCopySelection copies the selected objects and brushes.
func editorCopySelection() {
	lvl := Editor.level
	if lvl == nil || ws().level != lvl {
		return
	}
	cb := &objClipboard{}
	var sum mgl32.Vec3
	minY := float32(0)
	n := 0
	addPoint := func(p mgl32.Vec3, low float32) {
		sum = sum.Add(p)
		if n == 0 || low < minY {
			minY = low
		}
		n++
	}

	for _, r := range selectedObjRefs() {
		switch r.kind {
		case pickEntity:
			e := lvl.Entities[r.idx]
			if e.FromPrefab != "" {
				continue
			}
			e.Volumes = world.CarriedVolumeTemplates(lvl, &lvl.Entities[r.idx])
			var def *world.EntityDef
			if e.Type != "" {
				def, _ = world.LoadEntityDef(e.Type)
			}
			cb.entities = append(cb.entities, world.EntityToRaw(&e, def))
		case pickVolume:
			v := &lvl.Volumes[r.idx]
			if !editorVolumeIsTheLevels(v) {
				continue
			}
			cb.volumes = append(cb.volumes, world.VolumeToRaw(v))
		case pickSprite:
			cb.sprites = append(cb.sprites, world.SpriteToRaw(&lvl.Sprites[r.idx]))
		case pickDecal:
			cb.decals = append(cb.decals, world.DecalToRaw(&lvl.Decals[r.idx]))
		case pickScenery:
			cb.scenery = append(cb.scenery, world.SceneryToRaw(&lvl.Scenery[r.idx]))
		case pickEmitter:
			em := &lvl.Emitters[r.idx]
			if !editorEmitterIsTheLevels(em) {
				continue
			}
			cb.emitters = append(cb.emitters, world.EmitterToRaw(em))
		case pickSound:
			cb.sounds = append(cb.sounds, world.SoundToRaw(&lvl.Sounds[r.idx]))
		case pickEffect:
			cb.effects = append(cb.effects, world.EffectToRaw(&lvl.Effects[r.idx]))
		case pickTree:
			cb.trees = append(cb.trees, world.TreeToRaw(&lvl.Trees[r.idx]))
		default:
			continue
		}
		p := *objPos(r)
		addPoint(p, p.Y())
	}

	for _, s := range ws().SelectedBrushes {
		if !brushSelValid(s) {
			continue
		}
		ch := &lvl.Chunks[s.chunkIdx]
		b := &ch.Brushes[s.brushIdx]
		if len(b.Verts) == 0 {
			continue
		}
		cb.brushes = append(cb.brushes, clipBrush{chunkID: ch.ID, brush: world.CloneBrush(b, "")})
		var c mgl32.Vec3
		low := float32(0)
		for i, v := range b.Verts {
			w := ch.LocalToWorld(v)
			c = c.Add(w)
			if i == 0 || w.Y() < low {
				low = w.Y()
			}
		}
		addPoint(c.Mul(1/float32(len(b.Verts))), low)
	}

	if n == 0 {
		editorSetStatus("Nothing selected to copy")
		return
	}
	c := sum.Mul(1 / float32(n))
	cb.anchor = mgl32.Vec3{c.X(), minY, c.Z()}
	cb.count = n
	editorClipboard = cb
	editorSetStatus("Copied %s", plural(n, "object"))
}

// editorPasteClipboard places a copy of the clipboard under the cursor, or
// beside the originals when the cursor is not over the viewport, and selects
// what it placed.
func editorPasteClipboard() {
	lvl := Editor.level
	cb := editorClipboard
	if lvl == nil || cb == nil || ws().level != lvl {
		return
	}
	target := cb.anchor.Add(mgl32.Vec3{1, 0, 1})
	mx, my, _ := sdl.GetMouseState()
	if !imgui.CurrentIO().WantCaptureMouse() && !ws().cursorOverPanel(mx) {
		target = editor3DPointAt(mx, my)
	}
	editorPasteClipboardAt(target)
}

// editorPasteClipboardAt is the paste with the clipboard's anchor landing on
// target.
func editorPasteClipboardAt(target mgl32.Vec3) {
	lvl := Editor.level
	cb := editorClipboard
	if lvl == nil || cb == nil {
		return
	}
	d := target.Sub(cb.anchor)

	EditorPushUndo()
	var placed []objRef

	for _, re := range cb.entities {
		e, err := world.EntityFromRaw(re, world.LoadEntityDef)
		if err != nil {
			editorSetStatus("Paste: %v", err)
			continue
		}
		e.ID = world.UniqueEntityID(lvl, e.ID)
		e.Position = e.Position.Add(d)
		if err := e.Load(); err != nil {
			editorSetStatus("Entity %s: %v", e.ID, err)
		}
		lvl.Entities = append(lvl.Entities, e)
		editorMaterializeEntityContent(lvl, &lvl.Entities[len(lvl.Entities)-1])
		placed = append(placed, objRef{pickEntity, len(lvl.Entities) - 1})
	}
	for _, rv := range cb.volumes {
		v, err := world.VolumeFromRaw(rv)
		if err != nil {
			editorSetStatus("Paste: %v", err)
			continue
		}
		v.ID = world.UniqueVolumeID(lvl, v.ID)
		v.Position = v.Position.Add(d)
		if err := v.Load(); err != nil {
			editorSetStatus("Volume %s: %v", v.ID, err)
		}
		lvl.Volumes = append(lvl.Volumes, v)
		placed = append(placed, objRef{pickVolume, len(lvl.Volumes) - 1})
	}
	for _, rs := range cb.sprites {
		sp, err := world.SpriteFromRaw(rs)
		if err != nil {
			editorSetStatus("Paste: %v", err)
			continue
		}
		sp.ID = world.UniqueSpriteID(lvl, sp.ID)
		sp.Position = sp.Position.Add(d)
		_ = sp.EnsureSpriteTexture()
		lvl.Sprites = append(lvl.Sprites, sp)
		placed = append(placed, objRef{pickSprite, len(lvl.Sprites) - 1})
	}
	for _, rd := range cb.decals {
		dc, err := world.DecalFromRaw(rd)
		if err != nil {
			editorSetStatus("Paste: %v", err)
			continue
		}
		dc.ID = world.UniqueDecalID(lvl, dc.ID)
		dc.Position = dc.Position.Add(d)
		_ = dc.EnsureDecalTexture()
		lvl.Decals = append(lvl.Decals, dc)
		placed = append(placed, objRef{pickDecal, len(lvl.Decals) - 1})
	}
	for _, rem := range cb.emitters {
		em, err := world.EmitterFromRaw(rem)
		if err != nil {
			editorSetStatus("Paste: %v", err)
			continue
		}
		em.ID = world.UniqueEmitterID(lvl, em.ID)
		em.Position = em.Position.Add(d)
		if err := em.Load(); err != nil {
			editorSetStatus("Emitter %s: %v", em.ID, err)
		}
		lvl.Emitters = append(lvl.Emitters, em)
		placed = append(placed, objRef{pickEmitter, len(lvl.Emitters) - 1})
	}
	for _, rsc := range cb.scenery {
		sc := world.SceneryFromRaw(rsc)
		sc.ID = world.UniqueSceneryID(lvl, sc.ID)
		sc.Position = sc.Position.Add(d)
		if err := sc.Load(); err != nil {
			editorSetStatus("%v", err)
		}
		lvl.Scenery = append(lvl.Scenery, sc)
		placed = append(placed, objRef{pickScenery, len(lvl.Scenery) - 1})
	}
	for _, rt := range cb.trees {
		t := world.TreeFromRaw(rt)
		t.ID = world.UniqueTreeID(lvl, t.ID)
		t.Position = t.Position.Add(d)
		lvl.Trees = append(lvl.Trees, t)
		placed = append(placed, objRef{pickTree, len(lvl.Trees) - 1})
	}
	for _, rs := range cb.sounds {
		sd, err := world.SoundFromRaw(rs)
		if err != nil {
			editorSetStatus("Paste: %v", err)
			continue
		}
		sd.ID = world.UniqueSoundID(lvl, sd.ID)
		sd.Position = sd.Position.Add(d)
		lvl.Sounds = append(lvl.Sounds, sd)
		placed = append(placed, objRef{pickSound, len(lvl.Sounds) - 1})
	}
	for _, re := range cb.effects {
		fx, err := world.EffectFromRaw(re)
		if err != nil {
			editorSetStatus("Paste: %v", err)
			continue
		}
		fx.ID = world.UniqueEffectID(lvl, fx.ID)
		fx.Position = fx.Position.Add(d)
		lvl.Effects = append(lvl.Effects, fx)
		placed = append(placed, objRef{pickEffect, len(lvl.Effects) - 1})
	}

	// Brushes go back into the chunk they came from, or the active one if
	// that has gone, moved by the same world offset carried into its space.
	var placedBrushes []brushSel
	for _, cbb := range cb.brushes {
		ci := -1
		for i := range lvl.Chunks {
			if lvl.Chunks[i].ID == cbb.chunkID {
				ci = i
				break
			}
		}
		if ci < 0 {
			ci = Editor.ActiveChunkIdx
		}
		if ci < 0 || ci >= len(lvl.Chunks) {
			continue
		}
		ch := &lvl.Chunks[ci]
		local := ch.WorldToLocal(cb.anchor.Add(d)).Sub(ch.WorldToLocal(cb.anchor))
		b := world.CloneBrush(&cbb.brush, "")
		b.ID = world.UniqueBrushID(ch, b.ID)
		for i := range b.Verts {
			b.Verts[i] = b.Verts[i].Add(local)
		}
		if b.Detail != nil {
			for i := range b.Detail.Verts {
				b.Detail.Verts[i] = b.Detail.Verts[i].Add(local)
			}
		}
		appendBrush(ch, b)
		placedBrushes = append(placedBrushes, brushSel{chunkIdx: ci, brushIdx: len(ch.Brushes) - 1})
	}

	// Select what was pasted: the objects as a set, or the brushes if that is
	// all there was. A mix keeps the objects — the two are separate
	// selections, and either alone can be dragged off the originals.
	switch {
	case len(placed) > 0:
		editorSetObjectSelection(placed)
	case len(placedBrushes) > 0:
		editorClearObjectSelections()
		Editor.EditContext = editorObjectMode
		setBrushSelection(placedBrushes)
		Editor.PanelFocus = panelFocusBrush
	}
	editorSetStatus("Pasted %s", plural(len(placed)+len(placedBrushes), "object"))
}
