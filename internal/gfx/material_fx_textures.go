package gfx

// material_fx_textures.go — the textures the terrain and rock effects sample
// (material_fx.go): the splat's two layer arrays, and the DATA maps a
// generator writes — splat weights, a terrain's ground colour and height, a
// strata ramp.
//
// SPLAT ATLASES. A splat's four layers each have an albedo, a normal map, a
// height map and an AO map: sixteen textures, and the material pass already
// holds 23 of the 32 samplers a fragment stage may. So the layers are packed
// into two textures, built here from the files: albedo RGB with the height
// in alpha, and normal RGB with the AO in alpha, each a 2×2 ATLAS of the
// four layers. Not 2D arrays, which would be the obvious answer: an array
// sampler cannot rest on unit 0 beside the albedo's sampler2D, and parked on
// a unit of its own it cost every draw in the level on Mesa's d3d12 driver
// (material_fx.go, BindSurfaceFxSamplers).
//
// EIGHT LAYERS (docs/regions/PHASE_3.md): a splat in eight-layer mode with
// more than four distinct layers packs them into 4×2 atlases on the same
// two units — four tiles across, two down — with the same gutters, the
// same mip and BC7 paths, cached by all eight names. Its layers are
// resampled to at most FxArray8MaxSize (1k by default, so the pair stays
// about the size of one four-layer 2k pair: 4352×2176 against 4352²).
// A splat of four layers or fewer — every four-layer material — is the
// 2×2 atlas exactly as before: the same key, the same pixels.
//
// A PALETTE (docs/regions/PRESETS_1.md): a region terrain whose environments
// bring more than eight distinct ground layers names up to 32 of them as
// every tile's shared atlas (`atlas1..32`); each tile's splat mixes at most
// eight. Nine or more distinct layers pack into an 8-across atlas, as many
// rows as they need (two for up to 16, four for 32), each layer at most
// FxArrayPaletteMaxSize (512 by default): the 8×4 is 4352×2176, the same
// texels as the eight-layer 1k pair, so a 32-layer world costs no more video
// memory than an eight-layer one. The shader addresses the rows by
// fxSplatRows. Eight names or fewer are keyed, packed and hashed exactly as
// before.
//
// Each layer is resampled to one size (the largest albedo's, capped by
// FxArrayMaxSize and lowered by the skip-mips setting the way the 2D textures
// are) and framed by a GUTTER of its own texels wrapped round (a 32nd of the
// size), so a repeat sampled at the tile's edge, and the mipmaps down to the
// gutter's width, read the layer and never its neighbour. Mipmaps,
// trilinear and anisotropic like every world texture. A layer's _height,
// _normal and _ao are optional: flat, mid-height and unoccluded stand in.
// Atlases are cached by their four names and shared by every material that
// names the same layers, and rebuilt when one of their files changes on disk
// (checked once a second).
//
// WHEN an atlas is built: only for a material something actually draws —
// at level load, by EnsureMaterialFxTextures for the materials the level's
// scenery wears, or else on the first draw (applySplat, which is also how
// the material editor's preview gets one). Not at startup: the library-wide
// EnsureMaterialTextures (material_textures.go) loads the world textures
// only, since a project's every splat material would otherwise cost its
// atlases' build and video memory whether any level used it or not.
//
// COMPRESSION. With TextureCompression on, both atlases go up as BC7 through
// the world textures' path (texture_compress.go, internal/texcompress): BC7
// rather than BC5 for the normal atlas because its alpha is the AO, as the
// albedo atlas's is the height. The disk cache is keyed by the packed atlas's
// own pixels, so a change to any layer file, the size cap or the packing
// re-keys it. Skip-mips then drops leading levels of the cached chain the
// way a world texture's does, so the atlas is packed at full size and one
// cache file serves every skip setting — which is the same atlas the
// uncompressed path packs small, whenever halving the full geometry gives
// the small one exactly (splatHalvesExactly; any power-of-two layer size).
// Off, the atlases upload RGBA8 exactly as they always have. The first encode
// of a 4352² pair costs about a second of every core, on the main thread
// where the atlas is built; later loads read it off disk.
//
// DATA MAPS are read exactly: decoded without the premultiplication
// image/draw would apply to a non-opaque PNG (a weights map's alpha is the
// fourth layer's weight, not coverage), never compressed, never mip-skipped,
// and — the height map — sampled with texelFetch, so neither filtering nor
// mipmaps can mix its high and low bytes. They are named like textures, or by
// a resources-relative path without .png, which is how a generator points a
// material at the maps it wrote into its own output folder.

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/draw"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/material"
	"foundry/internal/texcompress"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// FxArrayMaxSize caps a splat layer's edge in its atlas. 2048 keeps a
// four-layer pair at about 200 MB with mipmaps; FOUNDRY_SPLAT_MAX_SIZE
// overrides.
var FxArrayMaxSize = envInt("FOUNDRY_SPLAT_MAX_SIZE", 2048)

// FxArray8MaxSize caps a layer's edge in a 4×2 (eight-layer) atlas: 1024,
// so the pair is 4352×2176 — half the texels of a four-layer 2k pair —
// unless FOUNDRY_SPLAT_MAX_SIZE says otherwise.
var FxArray8MaxSize = envInt("FOUNDRY_SPLAT_MAX_SIZE", 1024)

// FxArrayPaletteMaxSize caps a layer's edge in a palette atlas (more than
// eight distinct layers, 8 across): 512, so 32 layers are 4352×2176 — the
// eight-layer pair's size — unless FOUNDRY_SPLAT_PALETTE_MAX_SIZE says
// otherwise.
var FxArrayPaletteMaxSize = envInt("FOUNDRY_SPLAT_PALETTE_MAX_SIZE", 512)

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// fxStampEvery is how often a cached map's files are checked for changes.
const fxStampEvery = time.Second

// fxStamp is a file's identity for "has it changed": its size and time.
type fxStamp struct {
	size int64
	mod  int64
}

func stampOf(path string) fxStamp {
	st, err := os.Stat(path)
	if err != nil {
		return fxStamp{}
	}
	return fxStamp{st.Size(), st.ModTime().UnixNano()}
}

// FxTexturePath is the file a material's texture reference reads: a texture
// name (textures/<name>.png, normal maps included, which the texture scan
// leaves out of TextureNames), or a resources-relative path without .png.
func FxTexturePath(ref string) (string, bool) {
	if ref == "" {
		return "", false
	}
	if p, ok := TextureFile(ref); ok {
		return p, true
	}
	// An absolute path without .png: a test's own maps.
	if filepath.IsAbs(ref) {
		if _, err := os.Stat(ref + ".png"); err == nil {
			return ref + ".png", true
		}
		return "", false
	}
	for _, p := range []string{
		filepath.Join(fileutil.TexturesDir(), ref+".png"),
		filepath.Join(fileutil.ResourcesPath, filepath.FromSlash(ref)+".png"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// decodeExact reads a PNG's bytes as stored: NRGBA's colour channels are not
// multiplied by its alpha (see the file comment).
func decodeExact(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	switch m := img.(type) {
	case *image.NRGBA:
		for y := 0; y < b.Dy(); y++ {
			copy(out.Pix[y*out.Stride:y*out.Stride+4*b.Dx()], m.Pix[(y)*m.Stride:(y)*m.Stride+4*b.Dx()])
		}
	case *image.RGBA:
		for y := 0; y < b.Dy(); y++ {
			copy(out.Pix[y*out.Stride:y*out.Stride+4*b.Dx()], m.Pix[(y)*m.Stride:(y)*m.Stride+4*b.Dx()])
		}
	default:
		draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	}
	return out, nil
}

// ---------- data maps ----------

type fxData struct {
	tex     uint32
	path    string
	stamp   fxStamp
	checked time.Time
	failed  bool
}

var fxDataMaps = map[string]*fxData{}

// fxDataTexture is the GL texture of data map ref, loading it on first use.
// nearest: no filtering and no mipmaps (the height map, read by texelFetch).
func fxDataTexture(ref string, nearest bool) (uint32, bool) {
	key := ref
	if nearest {
		key += "\x00n"
	}
	d := fxDataMaps[key]
	if d != nil && time.Since(d.checked) < fxStampEvery {
		return d.tex, !d.failed
	}
	path, ok := FxTexturePath(ref)
	if !ok {
		if d == nil {
			log.Printf("material: texture %q not found (a missing map turns its effect off)", ref)
			fxDataMaps[key] = &fxData{failed: true, checked: time.Now()}
		} else {
			d.checked = time.Now()
		}
		if d != nil && d.tex != 0 {
			return d.tex, true
		}
		return 0, false
	}
	st := stampOf(path)
	if d != nil && d.path == path && d.stamp == st {
		d.checked = time.Now()
		return d.tex, !d.failed
	}
	if d == nil {
		d = &fxData{}
		fxDataMaps[key] = d
	}
	d.path, d.stamp, d.checked = path, st, time.Now()
	img, err := decodeExact(path)
	if err != nil {
		log.Printf("material: %s: %v", path, err)
		d.failed = d.tex == 0
		return d.tex, d.tex != 0
	}
	if d.tex == 0 {
		gl.GenTextures(1, &d.tex)
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	gl.BindTexture(gl.TEXTURE_2D, d.tex)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(w), int32(h), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(img.Pix))
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 4)
	bytes := int64(w) * int64(h) * 4
	if nearest {
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAX_LEVEL, 0)
	} else {
		gl.GenerateMipmap(gl.TEXTURE_2D)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
		bytes = rgba8ChainBytes(w, h)
	}
	// Clamped: a terrain's maps end at its edge.
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	noteTextureVRAM("fx:"+key, bytes, TextureRGBA8)
	d.failed = false
	return d.tex, true
}

// fxDetailTexture is a detail_normal map's texture: the named texture's own
// normal map, or the name read as a normal map itself.
func fxDetailTexture(ref string) (uint32, bool, bool) {
	if _, known := TextureNames[ref]; known {
		if err := EnsureTextureLoaded(ref); err == nil && HasNormalMap(ref) {
			n := NormalMapName(ref)
			return Textures[n], TextureFormatOf(n) == TextureBC5, true
		}
	}
	t, ok := fxDataTexture(ref, false)
	return t, false, ok
}

// ---------- splat arrays ----------

// SplatArrays is one built pair of layer atlases.
type SplatArrays struct {
	// Albedo and Normal are 2D textures, Cols×Rows tiles of Size + 2·Gutter.
	Albedo, Normal uint32
	// Layer is each named layer's tile (x = n%Cols, y = n/Cols), -1 for
	// none: the eight of a splat, or the up to 32 of a palette.
	Layer [material.SplatAtlasMax]int
	// Cols is the atlas's tiles across: 2 (up to four distinct layers, the
	// classic 2×2), 4 (the eight-layer 4×2) or 8 (a palette); Rows its
	// tiles down: 2, or for a palette as many as it needs.
	Cols   int
	Rows   int
	Size   int
	Gutter int

	files   []string
	stamps  []fxStamp
	checked time.Time
	failed  bool
}

var splatArrays = map[[material.SplatAtlasMax]string]*SplatArrays{}

// SplatArraysFor is the arrays for four layer names (empty: no layer),
// built on first ask; nil when none of the layers can be read.
func SplatArraysFor(names [4]string) *SplatArrays {
	return SplatArraysFor8(splatNames8(names))
}

// splatNames8 is four names as the eight the cache is keyed by.
func splatNames8(names [4]string) [material.SplatLayersMax]string {
	var n [material.SplatLayersMax]string
	copy(n[:], names[:])
	return n
}

// SplatArraysFor8 is SplatArraysFor for up to eight layer names (an
// eight-layer splat; layers 5–8 empty is exactly SplatArraysFor).
func SplatArraysFor8(names [material.SplatLayersMax]string) *SplatArrays {
	var n [material.SplatAtlasMax]string
	copy(n[:], names[:])
	return SplatArraysForAtlas(n)
}

// SplatArraysForAtlas is the atlas pair for up to 32 layer names (a
// palette; names 9–32 empty is exactly SplatArraysFor8).
func SplatArraysForAtlas(names [material.SplatAtlasMax]string) *SplatArrays {
	a := splatArrays[names]
	if a != nil {
		if time.Since(a.checked) < fxStampEvery {
			if a.failed {
				return nil
			}
			return a
		}
		a.checked = time.Now()
		stale := false
		for i, f := range a.files {
			if stampOf(f) != a.stamps[i] {
				stale = true
				break
			}
		}
		if !stale {
			if a.failed {
				return nil
			}
			return a
		}
	}
	b, err := buildSplatArrays(names, a)
	if err != nil {
		log.Printf("material: splat layers %v: %v", strings.Join(names[:], ", "), err)
		if a == nil {
			a = &SplatArrays{failed: true, checked: time.Now()}
			// Rebuilt when one of the files it wanted appears.
			for _, n := range names {
				if p, ok := FxTexturePath(n); ok {
					a.files = append(a.files, p)
					a.stamps = append(a.stamps, stampOf(p))
				}
			}
			splatArrays[names] = a
		}
		a.checked = time.Now()
		if a.failed {
			return nil
		}
		return a
	}
	splatArrays[names] = b
	return b
}

// splatSources is one layer's four files.
type splatSources struct {
	albedo, height, normal, ao string
}

// buildSplatArrays decodes, resamples, packs and uploads the arrays,
// reusing old's GL objects when it is a rebuild.
func buildSplatArrays(names [material.SplatAtlasMax]string, old *SplatArrays) (*SplatArrays, error) {
	t0 := time.Now()
	splatBuilds++
	out := &SplatArrays{Cols: 2, Rows: 2, checked: time.Now()}
	for i := range out.Layer {
		out.Layer[i] = -1
	}
	var srcs []splatSources
	layerOf := map[string]int{}
	for i, n := range names {
		if n == "" {
			continue
		}
		if k, ok := layerOf[n]; ok {
			out.Layer[i] = k
			continue
		}
		p, ok := FxTexturePath(n)
		if !ok {
			log.Printf("material: splat layer %q not found", n)
			continue
		}
		s := splatSources{albedo: p}
		if q, ok := FxTexturePath(n + "_height"); ok {
			s.height = q
		}
		if q, ok := FxTexturePath(NormalMapName(n)); ok {
			s.normal = q
		}
		if q, ok := FxTexturePath(n + "_ao"); ok {
			s.ao = q
		}
		layerOf[n] = len(srcs)
		out.Layer[i] = len(srcs)
		srcs = append(srcs, s)
	}
	if len(srcs) == 0 {
		return nil, fmt.Errorf("no layer could be read")
	}
	// Up to four distinct layers: the 2×2 atlas. More: the 4×2, its layers
	// capped smaller (FxArray8MaxSize).
	maxSize := FxArrayMaxSize
	switch {
	case len(srcs) > material.SplatLayersMax:
		// A palette: eight across, as many rows as it takes (at least two,
		// so 9–16 layers are the 8×2).
		out.Cols = 8
		out.Rows = max((len(srcs)+7)/8, 2)
		maxSize = FxArrayPaletteMaxSize
	case len(srcs) > 4:
		out.Cols = 4
		maxSize = FxArray8MaxSize
	}
	// Decode every file side by side.
	type job struct {
		path string
		img  *image.RGBA
		err  error
	}
	var jobs []*job
	add := func(p string) *job {
		if p == "" {
			return nil
		}
		j := &job{path: p}
		jobs = append(jobs, j)
		out.files = append(out.files, p)
		out.stamps = append(out.stamps, stampOf(p))
		return j
	}
	type layerJobs struct{ albedo, height, normal, ao *job }
	lj := make([]layerJobs, len(srcs))
	for i, s := range srcs {
		lj[i] = layerJobs{add(s.albedo), add(s.height), add(s.normal), add(s.ao)}
	}
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j *job) {
			defer wg.Done()
			j.img, j.err = decodeExact(j.path)
		}(j)
	}
	wg.Wait()
	size := 0
	layers := make([]splatDecoded, len(lj))
	img := func(j *job) *image.RGBA {
		if j == nil || j.err != nil {
			return nil
		}
		return j.img
	}
	for i := range lj {
		if lj[i].albedo.err != nil {
			return nil, lj[i].albedo.err
		}
		b := lj[i].albedo.img.Bounds()
		size = max(size, b.Dx(), b.Dy())
		layers[i] = splatDecoded{lj[i].albedo.img, img(lj[i].height), img(lj[i].normal), img(lj[i].ao)}
	}
	size = min(size, maxSize)
	full, skip := size, 0
	if s := TextureSkipMips; s > 0 && size >= TextureSkipMipsMinSize {
		for k := 0; k < s && size/2 >= TextureSkipMipsFloor; k++ {
			size /= 2
			skip++
		}
	}
	out.Size = size
	out.Gutter = splatGutter(size)
	// What is packed: the skipped size, as always — except that a compressed
	// atlas whose skipped geometry is exactly its full one halved is packed
	// at full size and drops its leading levels at upload instead (see the
	// file comment), so its disk cache serves every skip setting.
	compress := splatCompressibleRows(size, out.Cols, out.Rows)
	pack, drop := size, 0
	if compress && skip > 0 && splatHalvesExactly(full, skip) {
		pack, drop = full, skip
	}
	albedo, normal, aw, ah := packSplatAtlasGridRows(layers, pack, out.Cols, out.Rows)
	if old != nil && !old.failed {
		out.Albedo, out.Normal = old.Albedo, old.Normal
	}
	key := fmt.Sprintf("fx:splat:%x", splatKeyHash(names))
	if compress {
		tc := time.Now()
		hits := 0
		for _, at := range []struct {
			tex  *uint32
			part string
			pix  []byte
		}{{&out.Albedo, "albedo", albedo}, {&out.Normal, "normal", normal}} {
			var hit bool
			if *at.tex, hit = uploadSplatAtlasCompressedWH(*at.tex, key+":"+at.part, aw, ah, at.pix, drop); hit {
				hits++
			}
		}
		log.Printf("material: splat atlases %d×%d as BC7 (%d of 2 from the cache, %d top levels dropped) in %s",
			aw>>drop, ah>>drop, hits, drop, time.Since(tc).Round(time.Millisecond))
	} else {
		out.Albedo = uploadAtlasWH(out.Albedo, aw, ah, albedo)
		out.Normal = uploadAtlasWH(out.Normal, aw, ah, normal)
		noteTextureVRAM(key+":albedo", rgba8ChainBytes(aw, ah), TextureRGBA8)
		noteTextureVRAM(key+":normal", rgba8ChainBytes(aw, ah), TextureRGBA8)
	}
	log.Printf("material: splat layers %s packed at %d² (atlas %d×%d) in %s", strings.Join(splatNamesShown(names), ", "), size,
		aw>>drop, ah>>drop, time.Since(t0).Round(time.Millisecond))
	return out, nil
}

// splatNamesShown is the names a log line lists: four for a four-layer
// splat (as it always printed), eight for an eight-layer one, the palette's
// named ones otherwise.
func splatNamesShown(names [material.SplatAtlasMax]string) []string {
	if splatUpperEmpty(names) {
		return names[:material.SplatLayers]
	}
	if splatPaletteEmpty(names) {
		return names[:material.SplatLayersMax]
	}
	var out []string
	for _, n := range names {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// splatUpperEmpty reports whether layers 5 on are unnamed: a four-layer
// splat.
func splatUpperEmpty(names [material.SplatAtlasMax]string) bool {
	for _, n := range names[material.SplatLayers:] {
		if n != "" {
			return false
		}
	}
	return true
}

// splatPaletteEmpty reports whether names 9 on are unnamed: a splat's own
// eight (or a shared atlas of eight), not a palette.
func splatPaletteEmpty(names [material.SplatAtlasMax]string) bool {
	for _, n := range names[material.SplatLayersMax:] {
		if n != "" {
			return false
		}
	}
	return true
}

// splatKeyHash is the atlas pair's VRAM-record key hash: a four-layer
// splat's is hashNames of its four, as it always was; eight names hash as
// they did before palettes; a palette hashes every name.
func splatKeyHash(names [material.SplatAtlasMax]string) uint64 {
	if splatUpperEmpty(names) {
		var four [4]string
		copy(four[:], names[:])
		return hashNames(four)
	}
	upto := material.SplatLayersMax
	if !splatPaletteEmpty(names) {
		upto = material.SplatAtlasMax
	}
	h := fnv.New64a()
	for _, n := range names[:upto] {
		h.Write([]byte(n))
		var z [1]byte
		h.Write(z[:])
	}
	return h.Sum64()
}

// splatDecoded is one layer's decoded files; all but albedo may be nil.
type splatDecoded struct {
	albedo, height, normal, ao *image.RGBA
}

// splatGutter is the gutter a layer packed at size gets.
func splatGutter(size int) int { return max(size/32, 2) }

// splatEdge is the edge of the atlas whose layers are packed at size.
func splatEdge(size int) int { return 2 * (size + 2*splatGutter(size)) }

// splatHalvesExactly reports whether the atlas packed at full, halved skip
// times, has exactly the geometry of the one packed at full>>skip: the same
// size and gutter, so the same tiles, and every tile and gutter boundary on
// an even texel at each step — which makes the box-filtered mip level texel
// for texel the atlas packed small from box-halved layers (resampleRGBA
// halves the same way). True of every power-of-two size; a size that is not
// packs small, as the uncompressed path does.
func splatHalvesExactly(full, skip int) bool {
	s := full >> skip
	return s<<skip == full && splatGutter(full) == splatGutter(s)<<skip
}

// splatCompressible reports whether an atlas of cols×2 tiles packed at size
// goes up as BC7: compression on and not refused by the driver, and an
// atlas big enough and a multiple of four (the world textures' own rule,
// texture_compress.go; the 4×2 is twice as wide, so its height decides).
func splatCompressible(size, cols int) bool {
	return splatCompressibleRows(size, cols, 2)
}

// splatCompressibleRows is splatCompressible for an atlas of cols×rows
// tiles (a palette's).
func splatCompressibleRows(size, cols, rows int) bool {
	if !TextureCompression || compressionRefused.Load()&(1<<texcompress.BC7) != 0 {
		return false
	}
	e := splatEdge(size)
	w := e / 2 * cols
	h := e / 2 * rows
	return e%4 == 0 && w%4 == 0 && h%4 == 0 && min(e, h) >= TextureCompressMinSize
}

// splatBuilds counts buildSplatArrays calls, for the test that startup no
// longer builds atlases.
var splatBuilds int

// packSplatAtlases packs the layers at size into the two 2×2 atlases'
// RGBA8 pixels, each layer into its tile with its gutter wrapped round.
func packSplatAtlases(layers []splatDecoded, size int) (albedo, normal []byte, edge int) {
	albedo, normal, edge, _ = packSplatAtlasGrid(layers, size, 2)
	return albedo, normal, edge
}

// packSplatAtlasGrid packs the layers at size into cols×2 atlases (2: the
// four-layer 2×2, byte for byte what packSplatAtlases always made; 4: the
// eight-layer 4×2), w×h texels, tile n at (n%cols, n/cols).
func packSplatAtlasGrid(layers []splatDecoded, size, cols int) (albedo, normal []byte, w, h int) {
	return packSplatAtlasGridRows(layers, size, cols, 2)
}

// packSplatAtlasGridRows is packSplatAtlasGrid for cols×rows tiles (a
// palette's 8 across).
func packSplatAtlasGridRows(layers []splatDecoded, size, cols, rows int) (albedo, normal []byte, w, h int) {
	g := splatGutter(size)
	tile := size + 2*g
	w, h = cols*tile, rows*tile
	edge := w
	albedo = make([]byte, w*h*4)
	normal = make([]byte, w*h*4)
	for i := range normal {
		normal[i] = 255 // a flat, unoccluded empty tile (x and y fixed below)
	}
	for p := 0; p < w*h; p++ {
		normal[4*p], normal[4*p+1] = 128, 128
	}
	for i := range layers {
		a := resampleRGBA(layers[i].albedo, size)
		var h, nm, ao *image.RGBA
		if l := layers[i].height; l != nil {
			h = resampleRGBA(l, size)
		}
		if l := layers[i].normal; l != nil {
			nm = resampleRGBA(l, size)
		}
		if l := layers[i].ao; l != nil {
			ao = resampleRGBA(l, size)
		}
		tx, ty := (i%cols)*tile, (i/cols)*tile
		for y := 0; y < tile; y++ {
			sy := ((y-g)%size + size) % size
			for x := 0; x < tile; x++ {
				sx := ((x-g)%size + size) % size
				o := 4 * (sy*size + sx)
				d := 4 * ((ty+y)*edge + tx + x)
				hv := uint8(128)
				if h != nil {
					hv = h.Pix[o]
				}
				albedo[d], albedo[d+1], albedo[d+2], albedo[d+3] = a.Pix[o], a.Pix[o+1], a.Pix[o+2], hv
				if nm != nil {
					normal[d], normal[d+1], normal[d+2] = nm.Pix[o], nm.Pix[o+1], nm.Pix[o+2]
				}
				if ao != nil {
					normal[d+3] = ao.Pix[o]
				}
			}
		}
	}
	return albedo, normal, w, h
}

func hashNames(names [4]string) uint64 {
	h := fnv.New64a()
	for _, n := range names {
		h.Write([]byte(n))
		var z [1]byte
		h.Write(z[:])
	}
	return h.Sum64()
}

// uploadAtlas uploads an edge² RGBA8 atlas into tex (new when 0), with
// mipmaps and the world textures' filtering.
func uploadAtlas(tex uint32, edge int, pix []byte) uint32 {
	return uploadAtlasWH(tex, edge, edge, pix)
}

// uploadAtlasWH is uploadAtlas for a w×h atlas (the eight-layer 4×2).
func uploadAtlasWH(tex uint32, w, h int, pix []byte) uint32 {
	if tex == 0 {
		gl.GenTextures(1, &tex)
	}
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(w), int32(h), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(pix))
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 4)
	gl.GenerateMipmap(gl.TEXTURE_2D)
	ApplyWorldTextureFilterTo(gl.TEXTURE_2D)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return tex
}

// uploadSplatAtlasCompressed uploads an edge² atlas as BC7 into tex (new
// when 0) — its chain from the disk cache, keyed by the packed pixels
// themselves so a change to any layer's file re-keys it, or encoded and
// cached — leaving out its first drop levels the way a world texture's skip
// does (uploadCompressed), and records it under name. hit reports a cache
// read. An encode error or a driver refusal uploads it RGBA8 instead,
// halved drop times first, which is the atlas the uncompressed path packs.
func uploadSplatAtlasCompressed(tex uint32, name string, edge int, pix []byte, drop int) (uint32, bool) {
	return uploadSplatAtlasCompressedWH(tex, name, edge, edge, pix, drop)
}

// uploadSplatAtlasCompressedWH is uploadSplatAtlasCompressed for a w×h
// atlas (the eight-layer 4×2; BC7's chain halves both sides alike).
func uploadSplatAtlasCompressedWH(tex uint32, name string, w, h int, pix []byte, drop int) (uint32, bool) {
	rgba := &image.RGBA{Pix: pix, Stride: 4 * w, Rect: image.Rect(0, 0, w, h)}
	t, hit, storeErr, err := texcompress.LoadOrEncode(pix, rgba, texcompress.BC7, 0)
	if storeErr != nil {
		logCompressOnce("store", "texture compression: cannot write the cache in %s: %v (encoding every load)", texcompress.CacheDir(), storeErr)
	}
	if err == nil {
		if hit {
			TextureCompressionStats.CacheHits.Add(1)
		} else {
			TextureCompressionStats.Encodes.Add(1)
		}
		if tex == 0 {
			gl.GenTextures(1, &tex)
		}
		gl.BindTexture(gl.TEXTURE_2D, tex)
		ok := uploadCompressed(name, t, drop)
		if ok {
			ApplyWorldTextureFilterTo(gl.TEXTURE_2D)
		}
		gl.BindTexture(gl.TEXTURE_2D, 0)
		if ok {
			return tex, hit
		}
	} else {
		log.Printf("texture compression: %s: %v (uploading uncompressed)", name, err)
	}
	for i := 0; i < drop; i++ {
		rgba = texcompress.Downsample(rgba)
	}
	ew, eh := rgba.Rect.Dx(), rgba.Rect.Dy()
	tex = uploadAtlasWH(tex, ew, eh, rgba.Pix)
	noteTextureVRAM(name, rgba8ChainBytes(ew, eh), TextureRGBA8)
	return tex, false
}

// resampleRGBA is img at size² (bilinear; a box average when shrinking by
// more than half, so a 4k layer packed at 2k is not aliased). img itself
// when it is already that size.
func resampleRGBA(img *image.RGBA, size int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	for w >= 2*size && h >= 2*size {
		img = halveRGBA(img)
		w, h = img.Bounds().Dx(), img.Bounds().Dy()
	}
	if w == size && h == size {
		return img
	}
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	sx, sy := float64(w)/float64(size), float64(h)/float64(size)
	for y := 0; y < size; y++ {
		fy := (float64(y)+0.5)*sy - 0.5
		y0 := int(fy)
		if fy < 0 {
			y0, fy = 0, 0
		}
		y1 := min(y0+1, h-1)
		ty := fy - float64(y0)
		for x := 0; x < size; x++ {
			fx := (float64(x)+0.5)*sx - 0.5
			x0 := int(fx)
			if fx < 0 {
				x0, fx = 0, 0
			}
			x1 := min(x0+1, w-1)
			tx := fx - float64(x0)
			for c := 0; c < 4; c++ {
				p00 := float64(img.Pix[y0*img.Stride+4*x0+c])
				p10 := float64(img.Pix[y0*img.Stride+4*x1+c])
				p01 := float64(img.Pix[y1*img.Stride+4*x0+c])
				p11 := float64(img.Pix[y1*img.Stride+4*x1+c])
				v := (p00*(1-tx)+p10*tx)*(1-ty) + (p01*(1-tx)+p11*tx)*ty
				out.Pix[y*out.Stride+4*x+c] = uint8(v + 0.5)
			}
		}
	}
	return out
}

func halveRGBA(img *image.RGBA) *image.RGBA {
	w, h := img.Bounds().Dx()/2, img.Bounds().Dy()/2
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			for c := 0; c < 4; c++ {
				s := int(img.Pix[(2*y)*img.Stride+8*x+c]) + int(img.Pix[(2*y)*img.Stride+8*x+4+c]) +
					int(img.Pix[(2*y+1)*img.Stride+8*x+c]) + int(img.Pix[(2*y+1)*img.Stride+8*x+4+c])
				out.Pix[y*out.Stride+4*x+c] = uint8((s + 2) / 4)
			}
		}
	}
	return out
}

// EnsureMaterialFxTextures builds or loads everything the named material's
// terrain and rock effects sample, so the first frame that draws it does not
// pay for it. For level load (world.Scenery), and harmless to call again.
func EnsureMaterialFxTextures(name string, texture string) {
	if name == "" || !GeometryUploadsEnabled {
		return
	}
	// The material's own texture, which overlays the surface's: nothing else
	// loads it for a placement (the level loads the textures it names).
	if d := material.Get(name); d != nil && d.Texture != "" {
		if err := EnsureTextureLoaded(d.Texture); err != nil {
			log.Printf("material %s: texture %q: %v", name, d.Texture, err)
		}
	}
	s, ok := resolveSurfaceFx(name)
	if !ok || !s.Any() {
		return
	}
	if s.HasSplat {
		SplatArraysForAtlas(splatAtlasNames(&s))
		fxDataTexture(s.SplatWeights, false)
		if s.SplatEight {
			fxDataTexture(s.SplatWeights2, false)
		}
		if s.SplatStrata != "" {
			fxDataTexture(s.SplatStrata, false)
		}
	}
	if s.HasGroundBlend {
		fxDataTexture(s.GroundColor, false)
		fxDataTexture(s.GroundHeight, true)
	}
	if s.HasDetailNormal {
		fxDetailTexture(s.DetailMap)
	}
	if s.HasGrade && s.GradeSource == (mgl32.Vec3{}) {
		TextureMeanColor(texture)
	}
}
