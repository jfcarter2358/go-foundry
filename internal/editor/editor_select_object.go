package editor

// editor_select_object.go — one selection at a time.
//
// The panel shows the selected object's properties and nothing else, which only
// works if there is exactly one selected object. Selecting a chunk or a light
// already dropped everything else; selecting an entity, a volume or the spawn
// point did not, so two properties blocks could be up at once and neither was
// wrong. Every select* function goes through editorClearObjectSelections first
// now, and the callers that used to clear by hand can stop.
//
// Brushes get their own entry point here for the same reason: the Explorer can
// pick one, and it used to be set by assigning the index straight and hoping
// nothing else was still selected.

// editorClearObjectSelections drops every selection there is: the spawn point,
// an entity, a volume, a chunk, a light, and the faces and vertices picked
// inside a chunk.
//
// All of them, because the panel below shows the selected object's properties
// and only those: a face left selected under a freshly-picked entity is a
// second properties block nobody asked for, and a gizmo attached to something
// that is no longer what is selected.
func editorClearObjectSelections() {
	// The level's own objects, which only the level editor has any of. Behind
	// the workspace because these are Editor fields outright: called from the
	// entity editor they would drop whatever the MAIN window had selected,
	// which reads as the level editor deselecting itself every time a brush is
	// picked in another window. See editor_workspace.go.
	ws().clearLevelObjectSelections()

	// The geometry selections belong to whichever workspace is asking, so these
	// are unconditional.
	clearVertexSelection()
	clearFaceSelection()
	clearBrushSelection()
}

// editorClearLevelObjectSelections is the level editor's half of the above, and
// what a workspace with no level objects overrides away.
func editorClearLevelObjectSelections() {
	editorClearSpawnSelection()
	editorClearEntitySelection()
	editorClearVolumeSelection()
	editorClearSpriteSelection()
	editorClearEmitterSelection()
	editorClearSoundSelection()
	editorClearScenerySelection()
	editorClearPrefabSelection()
	editorClearChunkSelection()
	editorClearLightSelection()
}

// editorSelectBrush makes one brush the selection, from the Explorer's row.
//
// The chunk it belongs to becomes active — a brush is only addressable through
// one — but not *selected*: the panel is being asked for the brush's
// properties, not the chunk's.
//
// Picking a row names the brush itself, which is an object-mode selection, so
// this switches context as well. Landing an author in edit mode with one face
// of the clicked brush highlighted would be a guess at which face they meant;
// selecting the solid is not a guess.
func editorSelectBrush(chunkIdx, brushIdx int) {
	lvl := Editor.level
	if lvl == nil || chunkIdx < 0 || chunkIdx >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[chunkIdx]
	if brushIdx < 0 || brushIdx >= len(ch.Brushes) {
		return
	}
	Editor.EditContext = editorObjectMode
	Editor.ActiveChunkIdx = chunkIdx
	editorSelectBrushObject(brushSel{chunkIdx: chunkIdx, brushIdx: brushIdx}, false)
}
