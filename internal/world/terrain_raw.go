package world

// terrain_raw.go — a terrain's YAML form.
//
// The grid's arrays are written inline, packed: bytes, zlib, base64, one
// scalar each. Inline rather than a sidecar file because every path that
// carries a level around carries a RawLevel — undo snapshots, the
// play-session snapshot, the YAML text editor, prefab and chunk files, the
// compiler, the release packer's reference walk — and none of them would know
// to bring a second file along. Packed rather than a YAML list because a
// 128x32x128 terrain is half a million blocks; ground is long runs of the
// same byte, which zlib turns into a sliver.

import (
	"bytes"
	"maps"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// RawTerrain is the YAML representation of a chunk's block terrain: a
// regular grid of blocks in the chunk's own space, each air or made of one of
// up to four texture layers, with biomes painted on their faces. See
// world.Terrain.
type RawTerrain struct {
	// Origin is the chunk-local position of the grid's first corner (its
	// minimum X, Y and Z).
	Origin RawVec3 `yaml:"origin"`
	// BlockSize is the edge of one block, in chunk units.
	BlockSize float32 `yaml:"block_size"`
	// SizeX is the grid's width in blocks, along X.
	SizeX int `yaml:"size_x"`
	// SizeY is the grid's height in blocks, along Y.
	SizeY int `yaml:"size_y"`
	// SizeZ is the grid's depth in blocks, along Z.
	SizeZ int `yaml:"size_z"`
	// Smooth draws the terrain as a smooth surface (a dual-contoured surface
	// net over the blocks) instead of as blocks. Collision, rays and
	// navigation follow whichever is drawn.
	Smooth bool `yaml:"smooth,omitempty"`
	// Smoothing is how widely the smooth surface blends the blocks, in
	// blocks, 0.5 to 8: the width a floor's height is averaged over. Higher
	// rolls gentler hills into one slope; lower keeps more of the blocks'
	// shape. Cliffs, tunnels and ledges survive either way. Omitted means 3.
	Smoothing float32 `yaml:"smoothing,omitempty"`
	// Layers names up to four textures; a block's material picks one.
	// Empty (or all names empty) draws the terrain in its normal colour, like
	// an untextured face.
	Layers []string `yaml:"layers,omitempty"`
	// TextureScale is how many units one repeat of a layer texture spans.
	// Omitted means 2.
	TextureScale float32 `yaml:"texture_scale,omitempty"`
	// Blocks packs one byte per block — 0 air, 1..4 the texture layer it is
	// made of — row by row along X, then up Y, then along Z, zlib-compressed
	// and base64-encoded. Omitted means all air. Written by the editor; not
	// meant to be typed.
	Blocks string `yaml:"blocks,omitempty"`
	// Biomes is the palette painted onto faces; a face stores its entry's
	// position in the list, counting from 1.
	Biomes []RawTerrainBiome `yaml:"biomes,omitempty"`
	// Paint packs which faces are painted with which biome: for each painted
	// face in order, the gap from the previous face's number (block index * 6
	// + direction) as a varint, then its biome's position; zlib-compressed and
	// base64-encoded. Written by the editor; not meant to be typed.
	Paint string `yaml:"paint,omitempty"`
	// FoliageSeed varies where every plant grows. Omitted means 0.
	FoliageSeed uint32 `yaml:"foliage_seed,omitempty"`
}

// RawTerrainBiome is one entry of a terrain's biome palette.
type RawTerrainBiome struct {
	// Preset names what grows there: a biome file in biomes/ or a built-in —
	// meadow, forest, pine, wetland, scrub, cliff, scree, jungle, desert,
	// beach, tundra, badlands, volcanic, savannah, mesa, glacier, ice_plains
	// or rocky_cliff.
	Preset string `yaml:"preset"`
	// Density scales how thickly everything grows. Omitted means 1.
	Density float32 `yaml:"density,omitempty"`
	// Tint multiplies every colour in the biome. Omitted means none.
	Tint *RawVec3 `yaml:"tint,omitempty"`
	// GroundLayer, 1..4, draws painted faces with that texture layer instead
	// of their block's. Omitted means the block's own.
	GroundLayer int `yaml:"ground_layer,omitempty"`
	// Amounts scales how much of each kind of thing grows here, by shape —
	// {grass: 0.5, rock: 2, broadleaf: 0}: 0 none, 1 as the biome has it, up
	// to 4. Shapes not listed grow as the biome has them.
	Amounts map[string]float32 `yaml:"amounts,omitempty"`
}

// TerrainFromRaw decodes a RawTerrain. nil in, nil out.
func TerrainFromRaw(r *RawTerrain) (*Terrain, error) {
	if r == nil {
		return nil, nil
	}
	for _, s := range [3]int{r.SizeX, r.SizeY, r.SizeZ} {
		if s < 1 || s > TerrainMaxSize {
			return nil, fmt.Errorf("terrain: %dx%dx%d blocks; each side must be 1..%d", r.SizeX, r.SizeY, r.SizeZ, TerrainMaxSize)
		}
	}
	if r.SizeX*r.SizeY*r.SizeZ > TerrainMaxBlocks {
		return nil, fmt.Errorf("terrain: %dx%dx%d is more than %d blocks", r.SizeX, r.SizeY, r.SizeZ, TerrainMaxBlocks)
	}
	if !(r.BlockSize > 0) {
		return nil, fmt.Errorf("terrain: block_size %v must be positive", r.BlockSize)
	}
	if len(r.Layers) > TerrainLayerCount {
		return nil, fmt.Errorf("terrain: %d layers; at most %d", len(r.Layers), TerrainLayerCount)
	}
	if len(r.Biomes) > TerrainMaxBiomes {
		return nil, fmt.Errorf("terrain: %d biomes; at most %d", len(r.Biomes), TerrainMaxBiomes)
	}
	t := &Terrain{
		Origin:       mgl32.Vec3{r.Origin.X, r.Origin.Y, r.Origin.Z},
		BlockSize:    r.BlockSize,
		SizeX:        r.SizeX,
		SizeY:        r.SizeY,
		SizeZ:        r.SizeZ,
		Smooth:       r.Smooth,
		Smoothing:    r.Smoothing,
		TextureScale: r.TextureScale,
		FoliageSeed:  r.FoliageSeed,
	}
	copy(t.Layers[:], r.Layers)
	for _, rb := range r.Biomes {
		b := TerrainBiome{Preset: rb.Preset, Density: rb.Density, GroundLayer: rb.GroundLayer}
		for k, v := range rb.Amounts {
			if s, ok := FoliageShapeByKey(k); ok {
				b.SetAmount(s, v)
			}
		}
		if rb.Tint != nil {
			b.Tint = mgl32.Vec3{rb.Tint.X, rb.Tint.Y, rb.Tint.Z}
		}
		t.Biomes = append(t.Biomes, b)
	}
	if r.Blocks != "" {
		b, err := unpackTerrainBytes(r.Blocks, t.BlockCount())
		if err != nil {
			return nil, fmt.Errorf("terrain blocks: %w", err)
		}
		t.Blocks = b
	}
	if r.Paint != "" {
		b, err := unpackTerrainBytes(r.Paint, -1)
		if err != nil {
			return nil, fmt.Errorf("terrain paint: %w", err)
		}
		t.Paint = map[int32]uint8{}
		rd := bytes.NewReader(b)
		key := int64(-1)
		for rd.Len() > 0 {
			gap, err := binary.ReadUvarint(rd)
			if err != nil {
				return nil, fmt.Errorf("terrain paint: %w", err)
			}
			slot, err := rd.ReadByte()
			if err != nil {
				return nil, fmt.Errorf("terrain paint: %w", err)
			}
			key += int64(gap) + 1
			if key > int64(t.BlockCount())*6 {
				return nil, fmt.Errorf("terrain paint: face %d is past the grid", key)
			}
			t.Paint[int32(key)] = slot
		}
	}
	t.Normalize()
	return t, nil
}

// TerrainToRaw encodes a terrain. nil in, nil out.
func TerrainToRaw(t *Terrain) *RawTerrain {
	if t == nil {
		return nil
	}
	r := &RawTerrain{
		Origin:       RawVec3{X: t.Origin.X(), Y: t.Origin.Y(), Z: t.Origin.Z()},
		BlockSize:    t.BlockSize,
		SizeX:        t.SizeX,
		SizeY:        t.SizeY,
		SizeZ:        t.SizeZ,
		Smooth:       t.Smooth,
		Smoothing:    t.Smoothing,
		TextureScale: t.TextureScale,
		FoliageSeed:  t.FoliageSeed,
	}
	last := -1
	for i, l := range t.Layers {
		if l != "" {
			last = i
		}
	}
	if last >= 0 {
		r.Layers = append([]string(nil), t.Layers[:last+1]...)
	}
	for _, b := range t.Biomes {
		rb := RawTerrainBiome{Preset: b.Preset, Density: b.Density, GroundLayer: b.GroundLayer, Amounts: maps.Clone(b.Amounts)}
		if b.Tint != (mgl32.Vec3{}) {
			rb.Tint = &RawVec3{X: b.Tint[0], Y: b.Tint[1], Z: b.Tint[2]}
		}
		r.Biomes = append(r.Biomes, rb)
	}
	for _, v := range t.Blocks {
		if v != 0 {
			r.Blocks = packTerrainBytes(t.Blocks)
			break
		}
	}
	if len(t.Paint) > 0 {
		keys := make([]int32, 0, len(t.Paint))
		for k := range t.Paint {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		var buf []byte
		prev := int64(-1)
		for _, k := range keys {
			buf = binary.AppendUvarint(buf, uint64(int64(k)-prev-1))
			buf = append(buf, t.Paint[k])
			prev = int64(k)
		}
		r.Paint = packTerrainBytes(buf)
	}
	return r
}

// packTerrainBytes is zlib at its default level, not its best: this runs for
// every undo snapshot and every dirty check of the level, not only on save,
// and the best level buys almost nothing on runs of identical bytes.
func packTerrainBytes(b []byte) string {
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.DefaultCompression)
	zw.Write(b)
	zw.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// unpackTerrainBytes reverses packTerrainBytes. want < 0 accepts any length.
func unpackTerrainBytes(s string, want int) ([]byte, error) {
	z, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	zr, err := zlib.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	limit := int64(TerrainMaxBlocks) * 8
	if want >= 0 {
		// One byte past what the grid needs, so a mis-sized array is an
		// error rather than silently truncated.
		limit = int64(want) + 1
	}
	b, err := io.ReadAll(io.LimitReader(zr, limit))
	if err != nil {
		return nil, err
	}
	if want >= 0 && len(b) != want {
		return nil, fmt.Errorf("%d bytes, the grid needs %d", len(b), want)
	}
	return b, nil
}
