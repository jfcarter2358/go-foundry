package gfx

// texture_compress.go — uploading world textures block-compressed, and
// leaving out their top mip levels.
//
// The CPU half — the mip chain, the BC7/BC5 encoders, the disk cache — is
// internal/texcompress; this file is what decides a texture qualifies, runs
// that off the main thread where it can, and puts the result on the GPU.
//
// Which textures: only those LoadTexture reads from a file (level loads,
// streaming, the pickers), with TextureCompression on, both sides at least
// TextureCompressMinSize and multiples of four (a level-0 size that is not
// cannot be specified in blocks portably; such textures stay RGBA8). A
// <name>_normal map is BC5, everything else BC7. The texture editor's live
// re-uploads (UpdateTextureFromRGBA: a brush stroke a frame, animated
// textures, rescans) are always RGBA8 — encoding per stroke would cost far
// more than it saves, and the edit is replaced as soon as the file is saved
// and next loaded.
//
// Where the work runs: PrefetchTextures' workers decode a level's textures in
// parallel, and with compression on the same worker goes straight on to
// texcompress.LoadOrEncode, so a level load's encodes (or, from the second
// load on, cache reads) overlap each other and the rest of the load. A texture
// nobody prefetched is prepared inline in LoadTexture; the encode itself is
// parallel across blocks either way.
//
// The upload: every level of the cached chain from the first one kept
// (TextureSkipMips), glCompressedTexImage2D one by one, then one glGetError.
// GL 4.3 core requires both formats, but a driver that refuses one anyway
// (an INVALID_ENUM, or a driver bug on a level size) gets the texture
// uploaded as RGBA8 on the spot, the format is remembered as unusable so no
// later texture tries it (or spends an encode on it), and that is logged
// once. The chain ends at 1x1, so the texture is mipmap-complete without any
// change to GL_TEXTURE_MAX_LEVEL — which also means a later live edit through
// UpdateTextureFromRGBA (TexImage2D level 0 + glGenerateMipmap) rebuilds it as
// an ordinary RGBA8 texture with no stale state left over.
//
// The shader side: BC5 holds only x and y, so the normal map's z is rebuilt
// in fragment.glsl when BindNormalMap sets hasNormalMap to 2 rather than 1
// (texture_normal.go) — no new uniform, no new texture call, so the Nanite
// visibility-buffer rewrite of fragment.glsl needs nothing new.

import (
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-gl/gl/v4.3-core/gl"

	"foundry/internal/texcompress"
)

// compressionRefused holds the formats a driver has refused, as bits of
// 1<<Format. Written on the main thread by the upload, read by the prefetch
// workers so they do not encode for a format that will not be used.
var compressionRefused atomic.Uint32

// TextureCompressionStats counts what compression has done this run, for the
// tests and the measurements: chains served from the disk cache, chains
// encoded, and uploads that fell back to RGBA8.
var TextureCompressionStats struct {
	CacheHits, Encodes, Fallbacks atomic.Int64
}

// compressionFormatFor is the format file's decoded image would be compressed
// in, and false when it is not to be compressed. enabled is
// TextureCompression as the caller saw it on the main thread.
func compressionFormatFor(file string, rgba *image.RGBA, enabled bool) (texcompress.Format, bool) {
	if !enabled || rgba == nil {
		return 0, false
	}
	w, h := rgba.Rect.Dx(), rgba.Rect.Dy()
	if min(w, h) < TextureCompressMinSize || w%4 != 0 || h%4 != 0 {
		return 0, false
	}
	f := texcompress.BC7
	if IsNormalMapName(strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))) {
		f = texcompress.BC5
	}
	if compressionRefused.Load()&(1<<f) != 0 {
		return 0, false
	}
	return f, true
}

var compressLogOnce sync.Map

// logCompressOnce logs msg the first time key is seen.
func logCompressOnce(key, format string, args ...any) {
	if _, seen := compressLogOnce.LoadOrStore(key, true); !seen {
		log.Printf(format, args...)
	}
}

// prepareCompressed is file's compressed chain — from the cache, or encoded
// and cached — or nil when it does not qualify or anything went wrong (the
// texture then uploads RGBA8, as it always has). Pure CPU: safe on a worker.
func prepareCompressed(file string, rgba *image.RGBA, enabled bool) *texcompress.Texture {
	f, ok := compressionFormatFor(file, rgba, enabled)
	if !ok || texcompress.CoverageTexture(file) {
		// A coverage-preserving chain uploads RGBA8 (uploadRGBA8).
		return nil
	}
	src, err := os.ReadFile(file)
	if err != nil {
		log.Printf("texture compression: %s: %v (uploading uncompressed)", file, err)
		return nil
	}
	t, hit, storeErr, err := texcompress.LoadOrEncode(src, rgba, f, 0)
	if storeErr != nil {
		logCompressOnce("store", "texture compression: cannot write the cache in %s: %v (encoding every load)", texcompress.CacheDir(), storeErr)
	}
	if err != nil {
		log.Printf("texture compression: %s: %v (uploading uncompressed)", file, err)
		return nil
	}
	if hit {
		TextureCompressionStats.CacheHits.Add(1)
	} else {
		TextureCompressionStats.Encodes.Add(1)
	}
	return t
}

// glCompressedFormat is f's GL internal format, and the form it is counted as.
func glCompressedFormat(f texcompress.Format) (uint32, TextureForm) {
	if f == texcompress.BC5 {
		return gl.COMPRESSED_RG_RGTC2, TextureBC5
	}
	return gl.COMPRESSED_RGBA_BPTC_UNORM, TextureBC7
}

// uploadCompressed specifies t's levels, from skip on, into the texture bound
// to GL_TEXTURE_2D, and records its memory under name. False when the driver
// refused it; the caller then uploads RGBA8 over the same object.
func uploadCompressed(name string, t *texcompress.Texture, skip int) bool {
	skip = min(skip, len(t.Levels)-1)
	internal, form := glCompressedFormat(t.Format)
	// Errors left by earlier, unrelated calls must not be blamed on this
	// upload. Bounded: a lost context returns errors forever.
	for i := 0; i < 8 && gl.GetError() != gl.NO_ERROR; i++ {
	}
	var bytes int64
	for i, l := range t.Levels[skip:] {
		gl.CompressedTexImage2D(gl.TEXTURE_2D, int32(i), internal, int32(l.W), int32(l.H), 0,
			int32(len(l.Data)), gl.Ptr(l.Data))
		bytes += int64(len(l.Data))
	}
	if e := gl.GetError(); e != gl.NO_ERROR {
		compressionRefused.Or(1 << t.Format)
		TextureCompressionStats.Fallbacks.Add(1)
		logCompressOnce("refused"+t.Format.String(),
			"texture compression: the driver refused %v (GL error 0x%x on %q); %v textures upload uncompressed from now on",
			t.Format, e, name, t.Format)
		return false
	}
	noteTextureVRAM(name, bytes, form)
	return true
}

// uploadRGBA8 is the uncompressed upload into the texture bound to
// GL_TEXTURE_2D: skip levels dropped by downsampling on the CPU first (so the
// big levels are never allocated), then level 0 and the driver's chain. With
// skip 0 it is exactly the upload this engine has always made.
func uploadRGBA8(name string, rgba *image.RGBA, skip int) {
	for i := 0; i < skip; i++ {
		rgba = texcompress.Downsample(rgba)
	}
	w, h := rgba.Rect.Dx(), rgba.Rect.Dy()
	if texcompress.CoverageTexture(name) {
		// A thin cutout that asked to keep its coverage at distance
		// (texcompress/mip_coverage.go): the chain is built here.
		for i, l := range texcompress.CoverageMipChain(rgba) {
			gl.TexImage2D(gl.TEXTURE_2D, int32(i), gl.RGBA, int32(l.Rect.Dx()), int32(l.Rect.Dy()), 0, gl.RGBA,
				gl.UNSIGNED_BYTE, gl.Ptr(l.Pix))
		}
		noteTextureVRAM(name, rgba8ChainBytes(w, h), TextureRGBA8)
		return
	}
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(w), int32(h), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(rgba.Pix))
	generateWorldMipmaps()
	noteTextureVRAM(name, rgba8ChainBytes(w, h), TextureRGBA8)
}

// probeTextureCompression logs whether the driver lists the two formats.
// Informational only: GL_COMPRESSED_TEXTURE_FORMATS lists "general-purpose"
// formats and drivers commonly leave the specialised RGTC (and sometimes
// BPTC) out while supporting them, so the upload's own error check — not this
// list — is what decides.
func probeTextureCompression() {
	var n int32
	gl.GetIntegerv(gl.NUM_COMPRESSED_TEXTURE_FORMATS, &n)
	listed := map[int32]bool{}
	if n > 0 && n < 4096 {
		fmts := make([]int32, n)
		gl.GetIntegerv(gl.COMPRESSED_TEXTURE_FORMATS, &fmts[0])
		for _, f := range fmts {
			listed[f] = true
		}
	}
	log.Printf("texture compression: driver lists BC7 %v, BC5 %v (%d formats listed); cache %s",
		listed[gl.COMPRESSED_RGBA_BPTC_UNORM], listed[gl.COMPRESSED_RG_RGTC2], n, texcompress.CacheDir())
}
