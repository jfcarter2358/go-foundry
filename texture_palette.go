package main

// texture_palette.go — global-palette persistence: loading/saving the active
// palette and rebuilding on-disk .ftex files against it. The palette data
// model, quantiser, and TexDoc live in internal/texdoc (pure-Go, no file I/O
// or GPU dependency); this file is the package-main glue that needs both
// file I/O (texture_file.go) and the GPU upload queue (texture.go), which
// internal/texdoc deliberately doesn't depend on.

import (
	"fmt"
	"image/color"

	"foundry/internal/gfx"
	"foundry/internal/texdoc"
)

// ---------- global palette management ----------

// PaletteMismatch reports whether doc's embedded palette differs from the
// current global one — checked on open, per the plan's palette section.
func PaletteMismatch(d *texdoc.TexDoc) bool {
	return d.Pal != texdoc.GlobalPalette
}

// AdoptDocPaletteGlobally makes d's embedded palette the new global one.
func AdoptDocPaletteGlobally(d *texdoc.TexDoc) error {
	texdoc.GlobalPalette = d.Pal
	return WritePalette(palettePath(), &texdoc.GlobalPalette)
}

// RemapDocToGlobalPalette re-quantises d's layer indices from its own
// (mismatched) palette onto the current global one, nearest colour per
// index, and adopts the global palette as its own.
func RemapDocToGlobalPalette(d *texdoc.TexDoc) {
	for i := range d.Layers {
		d.Layers[i].Idx = texdoc.RemapIndices(&d.Pal, d.Layers[i].Idx, &texdoc.GlobalPalette)
	}
	d.Pal = texdoc.GlobalPalette
	d.MarkDirty()
}

// SetPaletteColor updates one global palette slot and persists it. Indices
// never move on a palette edit, so any document already bound to a live
// texture just needs SyncLiveTexture to pick up the new color — the caller
// (texture_editor_ui.go) does that for the currently open document.
// Already-saved PNGs need RebuildAllTextures.
func SetPaletteColor(idx uint8, c color.RGBA) error {
	texdoc.GlobalPalette[idx] = c
	return WritePalette(palettePath(), &texdoc.GlobalPalette)
}

// RebuildAllTextures walks every .ftex on disk, re-flattens it against the
// current global palette (remapping first if its embedded palette doesn't
// match), rewrites its PNG, and re-registers + re-uploads it. Returns every
// per-file error rather than stopping at the first one, so one bad file
// doesn't block the rest of the art from rebuilding.
func RebuildAllTextures() []error {
	var errs []error
	for _, name := range ScanFtexFiles() {
		d, err := ReadTexDoc(ftexPath(name))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		if d.Pal != texdoc.GlobalPalette {
			RemapDocToGlobalPalette(d)
		}
		if err := SaveTexDoc(d, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		gfx.RegisterTexture(name)
		flat := d.Flatten()
		gfx.UpdateTextureHasAlpha(name, &d.Pal, flat)
		gfx.QueueTextureUpload(name, rgbaFromPaletted(flat))
	}
	return errs
}
