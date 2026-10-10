package editor

// editor_material_sky.go — telling the open level that a sky material moved.
//
// Every other domain resolves its material at draw time, so an edit shows up
// on the next frame with nothing to invalidate. The sky does not: a sky
// material is FOLDED INTO the level's Sky when the level loads, because
// `ease_sky` blends two complete Sky values over time and a sky whose numbers
// changed under it would ease toward a moving target (world/sky_material.go).
//
// So the one thing the editor has to do by hand is tell the level to fold it in
// again. world.SyncSkyMaterial does that once a frame when the library's
// generation has moved; this only has to reset the stamp for the case where it
// has not — a save that produced an identical document, or a level opened after
// the material was already loaded.

import "foundry/internal/world"

func invalidateSkyMaterialForEditor() { world.InvalidateSkyMaterial() }
