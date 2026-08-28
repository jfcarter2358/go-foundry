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
// Sectors and biome sectors get their own entry points here for the same
// reason: the Explorer can pick either, and both used to be set by assigning
// the index straight and hoping nothing else was still selected.

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
	editorClearChunkSelection()
	editorClearLightSelection()
	clearVertexSelection()
	clearFaceSelection()

	// The in-chunk indices too. They are not cleared by clearFaceSelection —
	// a face and its sector are separate selections, and a vertex sets the
	// sector index itself — so without this a sector picked three clicks ago
	// kept its properties block open under whatever was picked since.
	Editor.ActiveSectorIdx = -1
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveEdgeKind = ""
	Editor.ActiveBiomeSectorIdx = -1
	Editor.BiomeDragVertexIdx = -1
}

// editorSelectSector makes one sector the selection, from the Explorer's row.
//
// The chunk it belongs to becomes active — a sector is only addressable
// through one — but not *selected*: the panel is being asked for the sector's
// properties, not the chunk's.
func editorSelectSector(chunkIdx, sectorIdx int) {
	lvl := Editor.level
	if lvl == nil || chunkIdx < 0 || chunkIdx >= len(lvl.Chunks) {
		return
	}
	if sectorIdx < 0 || sectorIdx >= len(lvl.Chunks[chunkIdx].Sectors) {
		return
	}
	editorClearObjectSelections()

	Editor.ActiveChunkIdx = chunkIdx
	Editor.ActiveSectorIdx = sectorIdx
	Editor.PanelFocus = panelFocusSector
}

// editorSelectBiomeSector makes one painted biome sector the selection.
//
// The biome layer is switched on with it: the sector cannot be reshaped on the
// map while the layer is off, and picking one from the Explorer and then
// finding it unclickable in the view is the kind of dead end the layer toggle
// used to be.
func editorSelectBiomeSector(chunkIdx, idx int) {
	lvl := Editor.level
	if lvl == nil || chunkIdx < 0 || chunkIdx >= len(lvl.Chunks) {
		return
	}
	if idx < 0 || idx >= len(lvl.Chunks[chunkIdx].BiomeSectors) {
		return
	}
	editorClearObjectSelections()

	Editor.ActiveChunkIdx = chunkIdx
	Editor.ActiveBiomeSectorIdx = idx
	Editor.BiomeLayerActive = true
	Editor.PanelFocus = panelFocusBiome
}
