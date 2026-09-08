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
	clearVertexSelection()
	clearFaceSelection()
}

// editorSelectBrush makes one brush the selection, from the Explorer's row.
//
// The chunk it belongs to becomes active — a brush is only addressable through
// one — but not *selected*: the panel is being asked for the brush's
// properties, not the chunk's.
//
// The selection is expressed as the brush's first face, because a face is what
// the selection unit is: everything downstream (the properties panel, the
// outline, the gizmo's corner handles) reads Editor.SelectedFaces, and a
// separate "brush is selected" flag would be a second source of truth for the
// same thing. A brush with no faces cannot be selected at all, which is
// correct — there would be nothing to show or grab.
func editorSelectBrush(chunkIdx, brushIdx int) {
	lvl := Editor.level
	if lvl == nil || chunkIdx < 0 || chunkIdx >= len(lvl.Chunks) {
		return
	}
	ch := &lvl.Chunks[chunkIdx]
	if brushIdx < 0 || brushIdx >= len(ch.Brushes) || len(ch.Brushes[brushIdx].Faces) == 0 {
		return
	}
	Editor.ActiveChunkIdx = chunkIdx
	setFaceSelection([]faceSel{brushFaceSel(chunkIdx, brushIdx, 0)})
	Editor.PanelFocus = panelFocusBrush
}
