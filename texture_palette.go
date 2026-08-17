package main

// texture_palette.go — the global 256-entry indexed palette shared by every
// texture, and the RGBA→index quantiser used at flatten time.
//
// Every texture layer stores 1-byte palette indices (see texture_doc.go);
// compositing happens in RGBA so opacity/blend modes still work, and the
// result is quantised back through this palette so the engine never sees an
// off-palette colour. Slot 0 is transparent by convention.

import (
	"fmt"
	"image/color"
)

// globalPalette is the palette every open TexDoc composites against. Loaded
// at startup by LoadGlobalPalette (texture_file.go); editing a slot is a live
// global operation — see texture_editor.go.
var globalPalette TexPalette = defaultPaletteRGBA

// DefaultPalette returns the compiled-in palette, derived by median-cut
// clustering of every texel across resources/textures/*.png so a fresh
// checkout's default art doesn't get reshuffled the first time it's opened
// in the texture editor.
func DefaultPalette() TexPalette {
	return defaultPaletteRGBA
}

// colorKey packs an RGBA color into a single uint32 for use as a map key.
func colorKey(c color.RGBA) uint32 {
	return uint32(c.R)<<24 | uint32(c.G)<<16 | uint32(c.B)<<8 | uint32(c.A)
}

// absInt returns the absolute value of an int.
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// QuantizeNearest maps c to the nearest palette entry: alpha distance is
// minimised first, then RGB distance among entries tied on alpha, then the
// lowest index breaks any remaining tie. An exact match always returns its
// own index. This is the uncached path — see quantCache for the memoised one
// used by Flatten.
func QuantizeNearest(pal *TexPalette, c color.RGBA) uint8 {
	bestA := -1
	for i := 0; i < texPaletteSz; i++ {
		ad := absInt(int(pal[i].A) - int(c.A))
		if bestA == -1 || ad < bestA {
			bestA = ad
		}
	}

	best := -1
	bestRGB := -1
	for i := 0; i < texPaletteSz; i++ {
		p := pal[i]
		ad := absInt(int(p.A) - int(c.A))
		if ad != bestA {
			continue
		}
		dr := int(p.R) - int(c.R)
		dg := int(p.G) - int(c.G)
		db := int(p.B) - int(c.B)
		rgbDist := dr*dr + dg*dg + db*db
		if best == -1 || rgbDist < bestRGB {
			best = i
			bestRGB = rgbDist
		}
	}
	return uint8(best)
}

// quantCache memoises QuantizeNearest results against one palette for the
// lifetime of a single flatten/remap operation. Callers should allocate a
// fresh cache per operation rather than share one across palette edits.
type quantCache struct {
	pal   *TexPalette
	cache map[uint32]uint8
}

func newQuantCache(pal *TexPalette) *quantCache {
	return &quantCache{pal: pal, cache: make(map[uint32]uint8)}
}

// Quantize returns the memoised nearest-index for c.
func (q *quantCache) Quantize(c color.RGBA) uint8 {
	key := colorKey(c)
	if v, ok := q.cache[key]; ok {
		return v
	}
	idx := QuantizeNearest(q.pal, c)
	q.cache[key] = idx
	return idx
}

// RemapIndices re-quantises indices (drawn from srcPal) against dstPal,
// resolving each source index to its RGBA color and finding the nearest
// entry in the destination palette. Used when opening a .ftex whose embedded
// palette differs from the current global one and the user chooses to remap
// the art rather than adopt the file's palette.
func RemapIndices(srcPal *TexPalette, indices []uint8, dstPal *TexPalette) []uint8 {
	out := make([]uint8, len(indices))
	q := newQuantCache(dstPal)
	// Memoise by source index too — a document only ever references at most
	// 256 distinct source colors, cheaper than re-quantising every texel.
	bySrcIdx := make(map[uint8]uint8, texPaletteSz)
	for i, srcIdx := range indices {
		if mapped, ok := bySrcIdx[srcIdx]; ok {
			out[i] = mapped
			continue
		}
		mapped := q.Quantize(srcPal[srcIdx])
		bySrcIdx[srcIdx] = mapped
		out[i] = mapped
	}
	return out
}

// ---------- global palette management ----------
//
// These operate purely on globalPalette + file I/O, with no dependency on
// texEditorState (moved here from texture_editor.go, the interactive-editing-
// session file, where they didn't belong).

// PaletteMismatch reports whether doc's embedded palette differs from the
// current global one — checked on open, per the plan's palette section.
func PaletteMismatch(d *TexDoc) bool {
	return d.Pal != globalPalette
}

// AdoptDocPaletteGlobally makes d's embedded palette the new global one.
func AdoptDocPaletteGlobally(d *TexDoc) error {
	globalPalette = d.Pal
	return WritePalette(palettePath(), &globalPalette)
}

// RemapDocToGlobalPalette re-quantises d's layer indices from its own
// (mismatched) palette onto the current global one, nearest colour per
// index, and adopts the global palette as its own.
func RemapDocToGlobalPalette(d *TexDoc) {
	for i := range d.Layers {
		d.Layers[i].Idx = RemapIndices(&d.Pal, d.Layers[i].Idx, &globalPalette)
	}
	d.Pal = globalPalette
	d.MarkDirty()
}

// SetPaletteColor updates one global palette slot and persists it. Indices
// never move on a palette edit, so any document already bound to a live
// texture just needs SyncLiveTexture to pick up the new color — the caller
// (texture_editor_ui.go) does that for the currently open document.
// Already-saved PNGs need RebuildAllTextures.
func SetPaletteColor(idx uint8, c color.RGBA) error {
	globalPalette[idx] = c
	return WritePalette(palettePath(), &globalPalette)
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
		if d.Pal != globalPalette {
			RemapDocToGlobalPalette(d)
		}
		if err := SaveTexDoc(d, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		RegisterTexture(name)
		flat := d.Flatten()
		UpdateTextureHasAlpha(name, &d.Pal, flat)
		QueueTextureUpload(name, rgbaFromPaletted(flat))
	}
	return errs
}

// defaultPaletteRGBA is the compiled-in default palette: 256 entries produced
// by median-cut clustering of every texel across resources/textures/*.png
// (see the generator invoked once during Phase 1 implementation), so a fresh
// checkout's existing art keeps looking like itself instead of being
// reshuffled onto an arbitrary ramp the first time it's opened. Slot 0 is
// forced fully transparent.
var defaultPaletteRGBA = [texPaletteSz]color.RGBA{
	{0, 0, 0, 0},
	{23, 5, 4, 255}, {131, 101, 33, 255}, {130, 118, 94, 255}, {43, 38, 46, 255},
	{135, 142, 122, 255}, {83, 112, 51, 255}, {136, 157, 42, 255}, {199, 185, 131, 255},
	{62, 73, 31, 255}, {44, 53, 61, 255}, {145, 110, 32, 255}, {154, 119, 35, 255},
	{88, 116, 77, 255}, {169, 182, 89, 255}, {160, 126, 37, 255}, {55, 82, 76, 255},
	{165, 137, 38, 255}, {171, 99, 45, 255}, {85, 114, 96, 255}, {174, 99, 47, 255},
	{90, 117, 105, 255}, {200, 187, 146, 255}, {102, 88, 74, 255}, {178, 134, 45, 255},
	{75, 92, 33, 255}, {64, 34, 7, 255}, {158, 167, 134, 255}, {166, 152, 51, 255},
	{119, 129, 114, 255}, {71, 95, 46, 255}, {201, 191, 154, 255}, {116, 95, 28, 255},
	{61, 93, 84, 255}, {99, 122, 110, 255}, {154, 138, 106, 255}, {210, 194, 96, 255},
	{87, 64, 60, 255}, {183, 159, 47, 255}, {204, 194, 159, 255}, {73, 45, 45, 255},
	{109, 127, 115, 255}, {183, 115, 47, 255}, {79, 103, 92, 255}, {203, 195, 163, 255},
	{48, 30, 8, 255}, {105, 93, 28, 255}, {211, 200, 165, 255}, {101, 127, 112, 255},
	{188, 118, 49, 255}, {185, 175, 67, 255}, {44, 111, 159, 255}, {118, 121, 50, 255},
	{163, 169, 142, 255}, {47, 118, 170, 255}, {132, 116, 45, 255}, {214, 200, 167, 255},
	{191, 121, 50, 255}, {164, 155, 124, 255}, {83, 103, 98, 255}, {209, 200, 185, 255},
	{193, 122, 51, 255}, {204, 204, 204, 255}, {220, 208, 131, 255}, {170, 167, 91, 255},
	{98, 74, 17, 255}, {103, 130, 115, 255}, {195, 123, 52, 255}, {144, 121, 46, 255},
	{84, 64, 46, 255}, {109, 137, 73, 255}, {77, 48, 12, 255}, {87, 105, 102, 255},
	{196, 124, 53, 255}, {108, 117, 52, 255}, {140, 154, 74, 255}, {45, 113, 162, 255},
	{255, 140, 0, 255}, {56, 78, 65, 255}, {39, 100, 144, 255}, {101, 129, 114, 255},
	{134, 145, 93, 255}, {153, 121, 49, 255}, {212, 201, 146, 255}, {223, 208, 108, 255},
	{175, 175, 147, 255}, {121, 121, 93, 255}, {86, 99, 34, 255}, {91, 107, 105, 255},
	{81, 96, 65, 255}, {46, 18, 5, 255}, {119, 109, 28, 255}, {45, 112, 161, 255},
	{89, 119, 52, 255}, {166, 159, 76, 255}, {100, 128, 114, 255}, {170, 145, 41, 255},
	{99, 97, 44, 255}, {132, 142, 62, 255}, {160, 138, 49, 255}, {195, 187, 94, 255},
	{75, 99, 31, 255}, {218, 201, 170, 255}, {101, 116, 30, 255}, {170, 141, 46, 255},
	{216, 200, 154, 255}, {199, 175, 64, 255}, {179, 177, 149, 255}, {166, 135, 54, 255},
	{42, 106, 153, 255}, {170, 138, 54, 255}, {114, 126, 92, 255}, {92, 106, 106, 255},
	{99, 126, 52, 255}, {43, 108, 156, 255}, {133, 130, 34, 255}, {48, 41, 13, 255},
	{179, 164, 68, 255}, {131, 132, 56, 255}, {51, 64, 47, 255}, {141, 152, 58, 255},
	{175, 146, 42, 255}, {159, 167, 95, 255}, {144, 143, 57, 255}, {233, 218, 159, 255},
	{238, 223, 163, 255}, {153, 156, 136, 255}, {108, 103, 80, 255}, {81, 104, 31, 255},
	{43, 109, 157, 255}, {173, 157, 114, 255}, {93, 109, 107, 255}, {38, 13, 11, 255},
	{86, 75, 30, 255}, {81, 96, 75, 255}, {154, 144, 56, 255}, {183, 180, 152, 255},
	{67, 52, 20, 255}, {186, 173, 130, 255}, {76, 57, 27, 255}, {87, 123, 52, 255},
	{77, 101, 32, 255}, {91, 74, 45, 255}, {119, 126, 29, 255}, {185, 182, 154, 255},
	{117, 123, 78, 255}, {119, 134, 56, 255}, {167, 157, 50, 255}, {133, 109, 48, 255},
	{227, 210, 145, 255}, {195, 181, 78, 255}, {100, 120, 94, 255}, {184, 184, 157, 255},
	{183, 184, 101, 255}, {144, 116, 52, 255}, {122, 143, 74, 255}, {135, 142, 112, 255},
	{99, 87, 64, 255}, {93, 125, 52, 255}, {173, 173, 165, 255}, {49, 67, 64, 255},
	{174, 143, 47, 255}, {233, 216, 130, 255}, {138, 165, 75, 255}, {52, 52, 29, 255},
	{136, 133, 108, 255}, {144, 138, 35, 255}, {105, 79, 30, 255}, {132, 123, 55, 255},
	{210, 198, 130, 255}, {78, 103, 32, 255}, {140, 155, 130, 255}, {166, 145, 52, 255},
	{166, 159, 131, 255}, {71, 140, 46, 255}, {121, 142, 116, 255}, {67, 78, 45, 255},
	{153, 135, 54, 255}, {157, 146, 115, 255}, {238, 224, 165, 255}, {233, 216, 112, 255},
	{103, 137, 32, 255}, {234, 220, 155, 255}, {145, 134, 58, 255}, {182, 182, 182, 255},
	{211, 195, 111, 255}, {168, 160, 53, 255}, {87, 134, 51, 255}, {175, 120, 47, 255},
	{108, 122, 79, 255}, {87, 98, 43, 255}, {68, 92, 63, 255}, {178, 172, 108, 255},
	{150, 156, 43, 255}, {154, 136, 37, 255}, {235, 222, 167, 255}, {109, 132, 53, 255},
	{166, 176, 78, 255}, {134, 108, 51, 255}, {72, 113, 33, 255}, {96, 113, 46, 255},
	{174, 182, 101, 255}, {87, 115, 30, 255}, {117, 147, 33, 255}, {218, 204, 177, 255},
	{145, 164, 58, 255}, {217, 203, 163, 255}, {117, 139, 75, 255}, {152, 169, 75, 255},
	{131, 115, 33, 255}, {177, 169, 132, 255}, {80, 101, 85, 255}, {195, 188, 110, 255},
	{88, 61, 25, 255}, {168, 164, 58, 255}, {64, 40, 21, 255}, {157, 153, 95, 255},
	{48, 31, 25, 255}, {109, 105, 90, 255}, {33, 10, 6, 255}, {220, 215, 207, 255},
	{88, 57, 15, 255}, {132, 113, 59, 255}, {229, 218, 182, 255}, {137, 145, 128, 255},
	{185, 180, 79, 255}, {183, 176, 142, 255}, {45, 19, 22, 255}, {189, 167, 63, 255},
	{66, 57, 30, 255}, {120, 146, 55, 255}, {236, 223, 146, 255}, {74, 75, 32, 255},
	{161, 140, 38, 255}, {105, 93, 81, 255}, {121, 136, 95, 255}, {116, 100, 42, 255},
	{100, 80, 46, 255}, {144, 127, 54, 255}, {141, 157, 92, 255}, {160, 141, 60, 255},
	{203, 187, 79, 255}, {164, 160, 138, 255}, {101, 118, 106, 255}, {73, 59, 46, 255},
	{107, 79, 17, 255}, {48, 51, 16, 255}, {138, 150, 111, 255}, {108, 97, 64, 255},
	{166, 173, 61, 255}, {134, 131, 96, 255}, {144, 124, 34, 255}, {170, 168, 132, 255},
	{93, 75, 66, 255}, {181, 171, 91, 255}, {212, 183, 66, 255},
}
