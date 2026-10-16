package gfx

// material_fx_refresh.go — telling the material effects' data maps to look
// at their files now (docs/terrain-paint/PHASE_P2.md, "Live update").
//
// A splat's weights, ground colour and height maps are read once and kept
// (material_fx_textures.go's fxDataMaps), and their files are stat'ed at
// most once a second (fxStampEvery) to notice a rewrite. That is right for
// a frame loop, and a second too slow for the editor's live painting, which
// rewrites a terrain material's weights after a stroke and wants them on
// screen the next frame. RefreshFxDataMaps makes the next bind of every
// cached map check its file's stamp at once; a map whose file did not
// change costs one stat and stays as it is. Main GL thread, as every bind.

import "time"

// RefreshFxDataMaps makes every cached material data map re-check its file
// on its next bind.
func RefreshFxDataMaps() {
	for _, d := range fxDataMaps {
		d.checked = time.Time{}
	}
}
