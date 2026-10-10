package editor

// editor_entity_editor_brushes.go — the entity editor's geometry, and the
// workspace that lets the level editor's brush tools shape it.
//
// An entity type is brushes and models (world/entity_brush.go). The models were
// always edited here; the brushes used to be a *file name* pointing at
// chunks/<something>.yaml, so "give this ship a hold" meant leaving this window,
// building a chunk in the level editor, exporting it, coming back and picking
// it from a list. Now the brushes are the type's own and are shaped right here,
// with the level editor's own stamps, extrude, inset, subdivide, object/edit
// mode, picking, gizmos and face panel — not a second implementation of them,
// the same functions, pointed at a different workspace. See
// editor_workspace.go for how that pointing works.
//
// # The scratch level
//
// Every one of those tools resolves geometry through `ws().level`: a level, a
// chunk index, a brush index. So this window has a level — one chunk, no
// entities, no volumes, no lights — whose brushes are the type's. It is never
// saved, never played and never seen; it exists so that "chunk 0, brush 3, face
// 2" means something here in exactly the way it means something in a level.
//
// That chunk is also what the viewport DRAWS. There is no separate preview
// build for the solid half of a type any more: the brushes being edited are the
// brushes on screen, uploaded once and reloaded per edit by the same tools that
// reload a level's. A model attachment still previews through Instantiate (see
// editor_entity_editor_render.go), because a model genuinely is a template with
// GPU state stamped from it; a brush is not.
//
// # Which copy is authoritative
//
// The scratch chunk's brushes are the live ones — uploaded, dragged, extruded.
// entityDef.Brushes is the *document's* copy: plain data, no GPU handles, and
// what undo snapshots and Save writes. entityCommitBrushes copies the live set
// back into the document once at the end of every frame.
//
// End of frame rather than at the moment of each edit, and that ordering is
// load-bearing: a tool pushes undo BEFORE it edits, and at that moment the
// document still holds last frame's brushes — which is exactly the state undo
// has to restore to. Committing eagerly would snapshot the edit as its own
// undo point and make Ctrl+Z a no-op.

import (
	"fmt"

	"foundry/internal/levelio"
	"foundry/internal/render"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// entityGeomChunkID is what the scratch chunk calls itself. It never reaches a
// level file; the name only shows up in the outline and in a status line.
const entityGeomChunkID = "geometry"

// entityGeomLevel is the one-chunk scratch level the brush tools edit. Nil
// until the first document is seeded.
var entityGeomLevel *world.Level

// entityGeomDirty says the DOCUMENT's brushes have been replaced under the live
// ones — a new type, an opened file, an undo — so the scratch level has to be
// rebuilt from them.
//
// A flag rather than comparing the two lists every frame. They differ for the
// whole of any frame that edits geometry (the commit is at the end of it), so a
// comparison would re-seed mid-drag, throw away the selection and re-upload
// every buffer; only the paths that actually replace the document know the
// difference, and this is them saying so.
var entityGeomDirty = true

// entityInvalidateGeometry marks the scratch level for rebuild on the next
// frame. Called from wherever entityDef is assigned rather than mutated.
func entityInvalidateGeometry() { entityGeomDirty = true }

// entityWS is this window's geometry-editing state — the counterpart of
// Editor.brushWorkspace, over entityGeomLevel instead of the open level.
//
// The function fields are what make the shared tools work through this window's
// camera, undo stack and status line rather than the main window's. See
// editor_workspace.go.
var entityWS = brushWorkspace{
	ActiveBrushIdx: -1,
	ActiveFaceIdx:  -1,
	StepSize:       0.25,
	SnapToGrid:     true,
	DetailBrush:    brushSel{chunkIdx: -1, brushIdx: -1},

	rayFn: func(sx, sy int32) (mgl32.Vec3, mgl32.Vec3) {
		w, h, ok := entityViewport()
		if !ok {
			return mgl32.Vec3{}, mgl32.Vec3{0, 0, -1}
		}
		return entityCam.ray(sx, sy, w, h)
	},
	projViewFn: func() mgl32.Mat4 {
		w, h, ok := entityViewport()
		if !ok {
			return mgl32.Ident4()
		}
		return entityCam.proj(w, h).Mul4(entityCam.view())
	},
	camPosFn: func() mgl32.Vec3 { return entityCam.position() },
	viewSizeFn: func() (int32, int32) {
		w, h, _ := entityViewport()
		return w, h
	},
	projYYFn: func() float32 {
		w, h, ok := entityViewport()
		if !ok {
			return 1
		}
		return entityCam.proj(w, h)[5]
	},
	undoFn:   func() { EntityDefPushUndo() },
	statusFn: func(format string, args ...any) { entityStatus = fmt.Sprintf(format, args...) },
	// This workspace is only ever the active one while the entity editor's own
	// draw or event handler is running (entityUseWorkspace), so being asked at
	// all means the viewport is live. There is no "playing" state to stand down
	// for the way the level editor has; what there can be is no document, and
	// then there is nothing to click on.
	editingFn: func() bool { return entityDef != nil },
	// The brush stamp is the only click-to-place tool in this window, so there
	// is nothing else to report as armed and nothing else to put away.
	placementArmedFn: func() bool { return false },
	disarmOthersFn:   func() {},
	// A type has no spawn marker, entities, volumes, sprites, props, emitters,
	// sounds, prefabs or lights — so there is nothing to clear, and clearing
	// the level editor's would deselect whatever the main window was showing
	// every time a brush is picked here.
	clearLevelObjectSelectionsFn: func() {},
	panelEdgeFn: func() int32 {
		w, _, ok := entityViewport()
		if !ok {
			return 0
		}
		return w - int32(entityPanelWidth)
	},
	changedFn: entityGeometryEdited,
}

// entityUseWorkspace makes this window's geometry state the one the shared
// tools act on, and returns the restore. Called as `defer entityUseWorkspace()()`
// at the top of the window's draw and its event handler — the only two places
// this window is ever entered.
func entityUseWorkspace() func() { return entityWS.use() }

// entityGeomChunk is the scratch chunk, or nil before the first seed.
func entityGeomChunk() *world.Chunk {
	if entityGeomLevel == nil || len(entityGeomLevel.Chunks) == 0 {
		return nil
	}
	return &entityGeomLevel.Chunks[0]
}

// ---------- seeding and committing ----------

// entitySeedGeometry rebuilds the scratch level from the document's brushes and
// uploads them.
//
// Called whenever the document is replaced wholesale — a new type, an opened
// file, an undo — rather than after an ordinary edit, which mutates the live
// brushes in place. Must run with this window's GL context current: it releases
// the old buffers and mints new ones. See the header of
// editor_entity_editor_render.go for why that matters.
func entitySeedGeometry() {
	entityGeomDirty = false
	// Every brush comes back from the document at rest, so a path preview that
	// was holding one is holding nothing now; the next tick poses it afresh.
	entityWaypointPreview.Posed = false
	entityReleaseGeometry()
	if entityDef == nil {
		return
	}

	ch := world.Chunk{ID: entityGeomChunkID, Scale: 1}
	ch.Brushes = world.CloneEntityBrushes(entityDef.Brushes)
	tex := map[string]bool{}
	for i := range ch.Brushes {
		for fi := range ch.Brushes[i].Faces {
			if t := ch.Brushes[i].Faces[fi].Texture; t != "" {
				tex[t] = true
			}
		}
	}
	if err := levelio.EnsureChunkTextures(tex); err != nil {
		// A missing texture costs a face its skin, not the type its shape.
		entityStatus = err.Error()
	}
	for i := range ch.Brushes {
		if err := ch.Brushes[i].Load(); err != nil {
			entityStatus = fmt.Sprintf("brush %s: %v", ch.Brushes[i].ID, err)
		}
	}
	ch.RecomputeBounds()

	entityGeomLevel = &world.Level{Chunks: []world.Chunk{ch}}
	// The frame set has to exist even for a level with no frames in it: the
	// shared picking and transform paths ask the level for one, and a nil there
	// is a crash rather than an empty answer.
	_ = world.RebuildIndexes(entityGeomLevel)

	entityWS.level = entityGeomLevel
	entityWS.ActiveChunkIdx = 0
	entityClearGeometrySelection()
}

// entityReleaseGeometry frees the scratch chunk's buffers and forgets it. Must
// run with this window's GL context current.
func entityReleaseGeometry() {
	if entityGeomLevel != nil {
		for i := range entityGeomLevel.Chunks {
			entityGeomLevel.Chunks[i].ReleaseGeometry()
		}
		render.ReleaseChunkBatches(entityGeomLevel)
	}
	entityGeomLevel = nil
	entityWS.level = nil
	entityClearGeometrySelection()
}

// entityClearGeometrySelection empties every geometry selection this workspace
// holds, so a stale index cannot outlive the brushes it named.
func entityClearGeometrySelection() {
	entityWS.SelectedBrushes = nil
	entityWS.SelectedFaces = nil
	entityWS.SelectedVertices = nil
	entityWS.ActiveBrushIdx = -1
	entityWS.ActiveFaceIdx = -1
	entityWS.GizmoKind = gizmoKindNone
	entityWS.DetailBrush = brushSel{chunkIdx: -1, brushIdx: -1}
}

// entityCommitBrushes copies the live brushes back into the document, if they
// have changed.
//
// Called once at the end of every frame — see the file comment for why not
// sooner. The comparison is over the serialized form, which is exactly the set
// of properties Save would write, so a frame that only moved the camera commits
// nothing and does not mark the document dirty.
func entityCommitBrushes() {
	ch := entityGeomChunk()
	if entityDef == nil || ch == nil {
		return
	}
	// Not while the document has been replaced under the live brushes. Every
	// path that replaces one — the Open dialog, New, an undo — runs from a
	// panel, a menu or a dialog, and all of those are drawn AFTER
	// entityPreviewSync and BEFORE this. So on that one frame the live chunk
	// still holds the OLD document's brushes, and copying them forward would
	// overwrite the type that was just opened with the one that was just
	// closed — emptying it, when what was closed was the blank new type. The
	// re-seed at the top of the next frame is what puts the two back in step,
	// and until it has run there is nothing here worth taking. See
	// entityGeomDirty.
	if entityGeomDirty {
		return
	}
	if world.EntityBrushesEqual(entityDef.Brushes, ch.Brushes) {
		return
	}
	entityDef.Brushes = world.CloneEntityBrushes(ch.Brushes)
	entityMarkDirty()
}

// entityGeometryEdited is the workspace's changedFn: what the shared tools call
// after they have rewritten geometry.
//
// It only has to invalidate what is DERIVED from the brushes — the camera
// framing box and the bounds cage. The brushes themselves are already the ones
// on screen, and the document copy is taken at the end of the frame.
func entityGeometryEdited() {
	if ch := entityGeomChunk(); ch != nil {
		ch.RecomputeBounds()
	}
}

// ---------- what the outline and the toolbar do to brushes ----------

// entityBrushCount is how many brushes the type holds, for the outline header.
func entityBrushCount() int {
	if ch := entityGeomChunk(); ch != nil {
		return len(ch.Brushes)
	}
	if entityDef != nil {
		return len(entityDef.Brushes)
	}
	return 0
}

// entityBrushAt is the nth brush of the type, or nil.
func entityBrushAt(i int) *world.Brush {
	ch := entityGeomChunk()
	if ch == nil || i < 0 || i >= len(ch.Brushes) {
		return nil
	}
	return &ch.Brushes[i]
}

// entitySelectBrush points both the outline and the shared object-mode
// selection at one brush, so clicking a row in the list and clicking the solid
// in the viewport leave the window in the same state.
func entitySelectBrush(i int) {
	if entityBrushAt(i) == nil {
		return
	}
	entitySel = entityEditorSelection{Kind: entitySelBrush, Idx: i}
	entityWS.EditContext = editorObjectMode
	editorSelectBrushObject(brushSel{chunkIdx: 0, brushIdx: i}, false)
}

// entityRemoveBrush deletes one brush and releases its buffers.
func entityRemoveBrush(i int) {
	ch := entityGeomChunk()
	if ch == nil || i < 0 || i >= len(ch.Brushes) {
		return
	}
	EntityDefPushUndo()
	id := ch.Brushes[i].ID
	ch.Brushes[i].Release()
	ch.Brushes = append(ch.Brushes[:i:i], ch.Brushes[i+1:]...)
	ch.RecomputeBounds()
	entityClearGeometrySelection()
	entitySel = entityEditorSelection{}
	entityMarkDirty()
	entityStatus = fmt.Sprintf("Removed brush %s", id)
}

// entityDeleteSelectedBrushes removes every brush the viewport has selected,
// falling back to the outline's cursor when the viewport has nothing.
//
// One function for the Delete key and the menu's Delete row, because a row
// labelled "Delete (3 brushes)" and a key that took one would be two answers to
// the same question. Selecting a FACE counts: the solid it belongs to is what
// the 3D view deletes for the same key, and the two windows share the tools.
func entityDeleteSelectedBrushes() {
	if len(entityWS.SelectedBrushes) == 0 && len(entityWS.SelectedFaces) == 0 {
		if entitySel.Kind == entitySelBrush {
			entityRemoveBrush(entitySel.Idx)
		}
		return
	}
	EntityDefPushUndo()
	editorDeleteSelectedBrushes()
	entitySel = entityEditorSelection{}
	entityGeometryEdited()
	entityMarkDirty()
}

// ---------- importing geometry ----------

// entityImportChunkFile copies the brushes out of a chunk file under chunks/
// into the type.
//
// A COPY, where the chunk attachment this replaces was a live link. The link
// let one hull back several types and meant editing one file reached all of
// them; what it cost was that a type could not be edited as one thing, which is
// the whole point of this window. Importing keeps the one thing an author
// actually reached for it for — "I already built this in the level editor" —
// and gives up the sharing, which nothing in the shipped content used.
func entityImportChunkFile(name string) error {
	if entityDef == nil {
		return fmt.Errorf("no type open")
	}
	rc, err := levelio.LoadChunkFromFile(levelio.ChunkFilePath(name))
	if err != nil {
		return err
	}
	if len(rc.Brushes) == 0 {
		return fmt.Errorf("%s holds no brushes", name)
	}
	EntityDefPushUndo()

	ch := entityGeomChunk()
	if ch == nil {
		entitySeedGeometry()
		ch = entityGeomChunk()
		if ch == nil {
			return fmt.Errorf("could not make somewhere to import into")
		}
	}
	// The file's own placement described where it sat in whatever level
	// exported it, which has no meaning here — an imported brush lands in the
	// entity's own space, at the coordinates it was authored at. That is the
	// same thing ImportChunkIntoLevel does with a chunk's origin.
	n := 0
	for _, rb := range rc.Brushes {
		b, err := world.BrushFromRaw(rb)
		if err != nil {
			return err
		}
		b.ID = world.UniqueEntityBrushID(ch.Brushes, b.ID)
		ch.Brushes = append(ch.Brushes, b)
		n++
	}

	tex := map[string]bool{}
	for i := len(ch.Brushes) - n; i < len(ch.Brushes); i++ {
		for fi := range ch.Brushes[i].Faces {
			if t := ch.Brushes[i].Faces[fi].Texture; t != "" {
				tex[t] = true
			}
		}
	}
	if err := levelio.EnsureChunkTextures(tex); err != nil {
		entityStatus = err.Error()
	}
	for i := len(ch.Brushes) - n; i < len(ch.Brushes); i++ {
		if err := ch.Brushes[i].Load(); err != nil {
			entityStatus = fmt.Sprintf("brush %s: %v", ch.Brushes[i].ID, err)
		}
	}
	ch.RecomputeBounds()
	entityMarkDirty()
	entityStatus = fmt.Sprintf("Imported %d brush(es) from %s", n, name)
	return nil
}
