package gfx

// texture_vram.go — how much video memory the world's textures take.
//
// GL has no portable way to ask how much memory a texture object holds, and
// the extensions that answer it (NVX_gpu_memory_info, ATI_meminfo) answer for
// the whole device, so the engine counts for itself: every world-texture
// upload — a file load through uploadTexture, a live edit or reload through
// UpdateTextureFromRGBA — records the bytes its levels take and the form they
// are in, keyed by texture name, replacing whatever that name held before;
// ReleaseTexture forgets the name. The F3 overlay (render/debug.go) prints
// the total, and the format split is what tells a player's report of "out of
// video memory" apart from a texture set that simply never compressed.
//
// The count is of the texel storage itself — every mip level, block-padded
// for the compressed formats — not the driver's own alignment or bookkeeping,
// so it is a slight under-estimate of what the driver allocates. The UI's
// glyph pages and the renderer's own targets (shadow maps, the G-buffer) are
// not world textures and are not counted.
//
// The form is also what BindNormalMap reads to tell the shader a normal map is
// two-channel BC5 (TextureFormatOf).

import (
	"sort"
	"sync"
)

// TextureForm is how a world texture's texels are stored on the GPU.
type TextureForm uint8

const (
	// TextureRGBA8 is uncompressed 8-bit RGBA with a driver-built chain.
	TextureRGBA8 TextureForm = iota
	// TextureBC7 is BPTC: RGBA, a byte a texel.
	TextureBC7
	// TextureBC5 is RGTC2: a normal map's x and y, a byte a texel.
	TextureBC5
)

func (f TextureForm) String() string {
	switch f {
	case TextureBC7:
		return "BC7"
	case TextureBC5:
		return "BC5"
	}
	return "RGBA8"
}

// textureVRAMEntry is one texture's accounting.
type textureVRAMEntry struct {
	bytes int64
	form  TextureForm
}

var textureVRAM = struct {
	sync.Mutex
	byName map[string]textureVRAMEntry
}{byName: map[string]textureVRAMEntry{}}

// noteTextureVRAM records name as holding bytes in form, replacing any earlier
// record.
func noteTextureVRAM(name string, bytes int64, form TextureForm) {
	textureVRAM.Lock()
	textureVRAM.byName[name] = textureVRAMEntry{bytes: bytes, form: form}
	textureVRAM.Unlock()
}

// forgetTextureVRAM drops name's record.
func forgetTextureVRAM(name string) {
	textureVRAM.Lock()
	delete(textureVRAM.byName, name)
	textureVRAM.Unlock()
}

// TextureFormatOf is the form the named texture was last uploaded in;
// RGBA8 for a name with no record.
func TextureFormatOf(name string) TextureForm {
	textureVRAM.Lock()
	defer textureVRAM.Unlock()
	return textureVRAM.byName[name].form
}

// TextureVRAMStats is the overlay's summary of the world textures.
type TextureVRAMStats struct {
	Bytes int64 // every level of every counted texture
	Count int   // textures counted
	BC7   int   // of which BC7
	BC5   int   // of which BC5
}

// TextureVRAM sums the records.
func TextureVRAM() TextureVRAMStats {
	textureVRAM.Lock()
	defer textureVRAM.Unlock()
	var s TextureVRAMStats
	for _, e := range textureVRAM.byName {
		s.Bytes += e.bytes
		s.Count++
		switch e.form {
		case TextureBC7:
			s.BC7++
		case TextureBC5:
			s.BC5++
		}
	}
	return s
}

// TextureVRAMBytes is TextureVRAM().Bytes.
func TextureVRAMBytes() int64 { return TextureVRAM().Bytes }

// rgba8ChainBytes is what an RGBA8 texture of w x h with a full mip chain to
// 1x1 takes.
func rgba8ChainBytes(w, h int) int64 {
	var n int64
	for {
		n += int64(w) * int64(h) * 4
		if w == 1 && h == 1 {
			return n
		}
		w, h = max(1, w/2), max(1, h/2)
	}
}

// TextureVRAMEach is every recorded texture's name and bytes, largest first
// (the bench's texture listing; docs/adventure/PHASE_F.md).
func TextureVRAMEach() []TextureVRAMItem {
	textureVRAM.Lock()
	defer textureVRAM.Unlock()
	out := make([]TextureVRAMItem, 0, len(textureVRAM.byName))
	for n, e := range textureVRAM.byName {
		out = append(out, TextureVRAMItem{Name: n, Bytes: e.bytes})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Bytes != out[b].Bytes {
			return out[a].Bytes > out[b].Bytes
		}
		return out[a].Name < out[b].Name
	})
	return out
}

// TextureVRAMItem is one texture's share of TextureVRAM.
type TextureVRAMItem struct {
	Name  string
	Bytes int64
}
