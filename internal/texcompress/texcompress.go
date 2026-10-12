// Package texcompress turns decoded world textures into GPU block-compressed
// mip chains, and keeps the results on disk so the work is done once per file.
//
// Why it exists: imported texture sets are 2k-4k, and the engine uploads
// textures as uncompressed RGBA8 with a driver-built mip chain — a 4096²
// texture is about 85 MB of video memory, a 2048² one about 21 MB. Block
// compression stores every 4x4 texel block in 16 bytes, a quarter of RGBA8,
// and the GPU samples it directly, so a scene's texture memory shrinks by
// four without the shader noticing (beyond the normal-map note below).
//
// Two formats, one per kind of texture:
//
//   - BC7 (BPTC, GL_COMPRESSED_RGBA_BPTC_UNORM) for albedos. Only mode 6 is
//     encoded: one subset, RGBA endpoints of 7 bits plus a shared per-endpoint
//     bit, 4-bit indices. That is the mode that suits smooth photographic
//     content best for the least encoder, and every decoder supports all
//     modes, so a mode-6-only stream is a valid BC7 stream. See bc7.go.
//   - BC5 (RGTC2, GL_COMPRESSED_RG_RGTC2) for normal maps: two independent
//     BC4 channels carrying the normal's x and y. The z is not stored; the
//     shader rebuilds it as sqrt(1 - x² - y²) (fragment.glsl, hasNormalMap
//     == 2). See bc5.go.
//
// The mip chain is built here on the CPU with a box filter (mip.go), because
// glGenerateMipmap cannot generate levels of a compressed texture, and each
// level is encoded in parallel over its block rows (encode.go). The finished
// chain is cached by a hash of the source file's bytes (cache.go), so a
// second load reads the blocks off disk and uploads them as they are.
//
// This package is pure CPU and must not import gfx (internal/layering_test.go):
// the GL side — whether compression is on, the upload, the fallback — lives
// in gfx/texture_compress.go.
package texcompress

import "fmt"

// Format is a block-compressed GPU format this package can produce.
type Format uint32

const (
	// BC7 is GL_COMPRESSED_RGBA_BPTC_UNORM: RGBA, 16 bytes a block.
	BC7 Format = 1
	// BC5 is GL_COMPRESSED_RG_RGTC2: two channels (RG), 16 bytes a block.
	BC5 Format = 2
)

// String is the format's short name, as the F3 overlay and logs print it.
func (f Format) String() string {
	switch f {
	case BC7:
		return "BC7"
	case BC5:
		return "BC5"
	}
	return fmt.Sprintf("Format(%d)", uint32(f))
}

// BlockBytes is the size of one 4x4 block. Both formats use 16.
const BlockBytes = 16

// EncoderVersion is part of every cache key: bump it whenever an encoder's
// output changes (a better fit, a new mode), so caches written by the old
// encoder are not served as if they were the new one's.
const EncoderVersion = 1

// Level is one mip level: its size in texels and its blocks, row-major,
// ceil(W/4) x ceil(H/4) of them.
type Level struct {
	W, H int
	Data []byte
}

// Texture is a whole encoded mip chain, level 0 first and down to 1x1.
type Texture struct {
	Format Format
	Levels []Level
}

// Bytes is the total size of every level's blocks — what the texture takes in
// video memory once uploaded.
func (t *Texture) Bytes() int64 {
	var n int64
	for _, l := range t.Levels {
		n += int64(len(l.Data))
	}
	return n
}

// LevelBytes is how many bytes a w x h level of a block format takes:
// partial blocks at the right and bottom edges are whole blocks in storage.
func LevelBytes(w, h int) int {
	return ((w + 3) / 4) * ((h + 3) / 4) * BlockBytes
}
