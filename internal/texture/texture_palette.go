package texture

// texture_palette.go — global-palette persistence: loading/saving the active
// palette and rebuilding on-disk .ftex files against it. The palette data
// model, quantiser, and TexDoc live in internal/texdoc (pure-Go, no file I/O
// or GPU dependency); this file is the package-main glue that needs both
// file I/O (texture_file.go) and the GPU upload queue (go), which
// internal/texdoc deliberately doesn't depend on.

import (
	"fmt"
	"image/color"
	"log"
	"os"
	"time"

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

// RemapDocToGlobalPalette re-quantises every frame's layer indices from d's
// own (mismatched) palette onto the current global one, nearest colour per
// index, and adopts the global palette as its own.
func RemapDocToGlobalPalette(d *texdoc.TexDoc) {
	d.EachLayer(func(l *texdoc.TexLayer) {
		l.Idx = texdoc.RemapIndices(&d.Pal, l.Idx, &texdoc.GlobalPalette)
	})
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

// ScanAnimatedTextures walks every .ftex on disk and registers the ones with
// more than one frame as animated textures the engine cycles through at
// runtime (gfx.AdvanceAnimatedTextures) — called once at startup, alongside
// gfx.ScanTextures, so a level can reference an animated texture without its
// editor ever having been opened this session.
//
// Only reads and flattens a file once it's confirmed to actually be
// animated: an ordinary static texture already has its <name>.png on disk
// (that's what gfx.ScanTextures/EnsureTextureLoaded load), so decoding its
// .ftex too at startup would just be wasted work repeated for every texture
// in the game. A file that fails to parse is logged and skipped rather than
// aborting startup over one bad asset — the same tolerance RebuildAllTextures
// gives a batch of .ftex files below.
func ScanAnimatedTextures() int { return scanAnimatedTextures(false) }

// RescanAnimatedTextures is the same walk for a project that has been running
// for a while: only the .ftex files that have actually changed since they were
// last read are parsed, and one that has gone from disk takes its animation
// with it. See texture_reload.go, which is what calls it.
func RescanAnimatedTextures() int { return scanAnimatedTextures(true) }

// ftexStamp is what was on disk the last time a .ftex was parsed.
type ftexStamp struct {
	mod  time.Time
	size int64
}

var ftexStamps = map[string]ftexStamp{}

// scanAnimatedTextures is the body of both walks. Returns how many animated
// textures it (re-)registered.
func scanAnimatedTextures(onlyChanged bool) int {
	n := 0
	seen := map[string]bool{}
	for _, name := range ScanFtexFiles() {
		seen[name] = true
		path := FtexPath(name)
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		stamp := ftexStamp{mod: st.ModTime(), size: st.Size()}
		if onlyChanged {
			if prev, ok := ftexStamps[name]; ok && prev == stamp {
				continue
			}
		}
		d, err := ReadTexDoc(path)
		if err != nil {
			log.Printf("WARNING: could not read %s.ftex for animation scan: %v", name, err)
			continue
		}
		ftexStamps[name] = stamp
		if d.FrameCount() <= 1 {
			// Not animated — and, on a rescan, possibly no longer animated:
			// a document whose frames have been taken back down to one would
			// otherwise keep cycling through the stack it was registered with.
			gfx.UnregisterAnimatedTexture(name)
			gfx.UnregisterAnimatedTexture(gfx.NormalMapName(name))
			continue
		}
		// Registers the animated normal map alongside the animated albedo. A
		// static texture needs nothing here either way: its <name>_normal.png
		// is already on disk and gfx.LoadTexture picks it up with the albedo.
		SyncAnimatedFrames(d, name)
		n++
	}
	for name := range ftexStamps {
		if seen[name] {
			continue
		}
		delete(ftexStamps, name)
		gfx.UnregisterAnimatedTexture(name)
		gfx.UnregisterAnimatedTexture(gfx.NormalMapName(name))
	}
	return n
}

// RebuildAllTextures walks every .ftex on disk, re-flattens it against the
// current global palette (remapping first if its embedded palette doesn't
// match), rewrites its PNG, and re-registers + re-uploads it. Returns every
// per-file error rather than stopping at the first one, so one bad file
// doesn't block the rest of the art from rebuilding.
func RebuildAllTextures() []error {
	var errs []error
	for _, name := range ScanFtexFiles() {
		d, err := ReadTexDoc(FtexPath(name))
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
		SyncLiveNormalMap(d, name)
		SyncAnimatedFrames(d, name)
		flat := d.Flatten()
		gfx.QueueTextureUpload(name, RgbaFromPaletted(flat))
	}
	return errs
}
