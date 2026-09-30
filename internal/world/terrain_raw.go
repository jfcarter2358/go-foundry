package world

// terrain_raw.go — a terrain's YAML form.
//
// The grid's arrays are written inline, packed: bytes (for heights, a
// quantised delta coding — see encodeTerrainHeights), zlib, base64, one
// scalar each. Inline rather than a sidecar file because every
// path that carries a level around carries a RawLevel — undo snapshots, the
// play-session snapshot, the YAML text editor, prefab and chunk files, the
// compiler, the release packer's reference walk — and none of them would know
// to bring a second file along. Packed rather than a YAML list because a
// 128x128 terrain is 16,641 heights: as a list that is a level file nobody
// can scroll through, and a flat or gently sculpted grid zlibs to a sliver.

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"io"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// RawTerrain is the YAML representation of a chunk's heightfield ground: a
// regular grid of heights in the chunk's own space, with a per-cell hole
// mask and up to four blended texture layers painted per vertex. See
// world.Terrain.
type RawTerrain struct {
	// Origin is the chunk-local position of the grid's first corner (its
	// minimum X and Z); heights are measured up from its Y.
	Origin RawVec3 `yaml:"origin"`
	// CellSize is the edge of one square cell, in chunk units.
	CellSize float32 `yaml:"cell_size"`
	// CellsX is the grid's width in cells, along X.
	CellsX int `yaml:"cells_x"`
	// CellsZ is the grid's depth in cells, along Z.
	CellsZ int `yaml:"cells_z"`
	// Layers names up to four textures, blended by the painted weights. The
	// first is what unpainted ground shows. Empty (or all names empty) draws
	// the terrain in its normal colour, like an untextured face.
	Layers []string `yaml:"layers,omitempty"`
	// TextureScale is how many units one repeat of a layer texture spans.
	// Omitted means 2.
	TextureScale float32 `yaml:"texture_scale,omitempty"`
	// Heights packs one height per vertex, (cells_x+1)*(cells_z+1) of them row
	// by row along X, in 1/1024ths of a unit: each the difference from the
	// vertex before it in its row (the one above, at a row's start),
	// zigzag-encoded, split into four byte planes, zlib-compressed and
	// base64-encoded. Omitted means flat. Written by the editor; not meant to
	// be typed.
	Heights string `yaml:"heights,omitempty"`
	// Holes packs one byte per cell (non-zero = hole), zlib-compressed and
	// base64-encoded. Omitted means no holes.
	Holes string `yaml:"holes,omitempty"`
	// Weights packs four bytes per vertex — one weight per layer, summing to
	// 255 — zlib-compressed and base64-encoded. Omitted means every vertex is
	// entirely the first layer.
	Weights string `yaml:"weights,omitempty"`
}

// TerrainFromRaw decodes a RawTerrain. nil in, nil out.
func TerrainFromRaw(r *RawTerrain) (*Terrain, error) {
	if r == nil {
		return nil, nil
	}
	if r.CellsX < 1 || r.CellsZ < 1 || r.CellsX > TerrainMaxCells || r.CellsZ > TerrainMaxCells {
		return nil, fmt.Errorf("terrain: %dx%d cells; each side must be 1..%d", r.CellsX, r.CellsZ, TerrainMaxCells)
	}
	if !(r.CellSize > 0) {
		return nil, fmt.Errorf("terrain: cell_size %v must be positive", r.CellSize)
	}
	if len(r.Layers) > TerrainLayerCount {
		return nil, fmt.Errorf("terrain: %d layers; at most %d", len(r.Layers), TerrainLayerCount)
	}
	t := &Terrain{
		Origin:       mgl32.Vec3{r.Origin.X, r.Origin.Y, r.Origin.Z},
		CellSize:     r.CellSize,
		CellsX:       r.CellsX,
		CellsZ:       r.CellsZ,
		TextureScale: r.TextureScale,
	}
	copy(t.Layers[:], r.Layers)
	nv := t.VertexCount()

	if r.Heights != "" {
		b, err := unpackTerrainBytes(r.Heights, nv*4)
		if err != nil {
			return nil, fmt.Errorf("terrain heights: %w", err)
		}
		t.Heights = decodeTerrainHeights(b, t.VertsX())
	}
	if r.Holes != "" {
		b, err := unpackTerrainBytes(r.Holes, t.CellCount())
		if err != nil {
			return nil, fmt.Errorf("terrain holes: %w", err)
		}
		t.Holes = b
	}
	if r.Weights != "" {
		b, err := unpackTerrainBytes(r.Weights, nv*TerrainLayerCount)
		if err != nil {
			return nil, fmt.Errorf("terrain weights: %w", err)
		}
		t.Weights = b
	}
	t.Normalize()
	return t, nil
}

// TerrainToRaw encodes a terrain. nil in, nil out. Arrays that say nothing —
// all-flat heights, no holes, all layer 0 — are omitted.
func TerrainToRaw(t *Terrain) *RawTerrain {
	if t == nil {
		return nil
	}
	r := &RawTerrain{
		Origin:       RawVec3{X: t.Origin.X(), Y: t.Origin.Y(), Z: t.Origin.Z()},
		CellSize:     t.CellSize,
		CellsX:       t.CellsX,
		CellsZ:       t.CellsZ,
		TextureScale: t.TextureScale,
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
	flat := true
	for _, h := range t.Heights {
		if h != 0 {
			flat = false
			break
		}
	}
	if !flat {
		r.Heights = packTerrainBytes(encodeTerrainHeights(t.Heights, t.VertsX()))
	}
	if t.HasHoles() {
		r.Holes = packTerrainBytes(t.Holes)
	}
	if t.Weights != nil && !allLayerZero(t.Weights) {
		r.Weights = packTerrainBytes(t.Weights)
	}
	return r
}

// terrainHeightStep is the resolution heights are saved at, in units: a
// millimetre, a thousand times finer than anything a body or an eye can tell,
// and what turns a sculpted grid's noisy float mantissas into small integers
// that compress.
const terrainHeightStep = 1.0 / 1024

// encodeTerrainHeights is the Heights byte layout: quantised, delta-coded
// along rows, zigzagged, split into byte planes. Neighbouring heights on
// ground are close, so the deltas are small and the high planes nearly all
// zero, which is what zlib is good at; raw floats compressed about a third
// as well.
func encodeTerrainHeights(h []float32, rowLen int) []byte {
	n := len(h)
	out := make([]byte, n*4)
	q := func(i int) int64 { return int64(math.Round(float64(h[i]) / terrainHeightStep)) }
	for i := 0; i < n; i++ {
		var prev int64
		if i%rowLen != 0 {
			prev = q(i - 1)
		} else if i >= rowLen {
			prev = q(i - rowLen)
		}
		d := int32(q(i) - prev)
		u := uint32(d<<1) ^ uint32(d>>31)
		out[i] = byte(u)
		out[n+i] = byte(u >> 8)
		out[2*n+i] = byte(u >> 16)
		out[3*n+i] = byte(u >> 24)
	}
	return out
}

func decodeTerrainHeights(b []byte, rowLen int) []float32 {
	n := len(b) / 4
	q := make([]int32, n)
	h := make([]float32, n)
	for i := 0; i < n; i++ {
		u := uint32(b[i]) | uint32(b[n+i])<<8 | uint32(b[2*n+i])<<16 | uint32(b[3*n+i])<<24
		d := int32(u>>1) ^ -int32(u&1)
		var prev int32
		if i%rowLen != 0 {
			prev = q[i-1]
		} else if i >= rowLen {
			prev = q[i-rowLen]
		}
		q[i] = prev + d
		h[i] = float32(q[i]) * terrainHeightStep
	}
	return h
}

func allLayerZero(w []uint8) bool {
	for i := 0; i+3 < len(w); i += TerrainLayerCount {
		if w[i] != 255 {
			return false
		}
	}
	return true
}

// packTerrainBytes is zlib at its default level, not its best: on a sculpted
// 512x512 grid the best level saves under 1% for ten times the time, and this
// runs for every undo snapshot and every dirty check of the level, not only
// on save.
func packTerrainBytes(b []byte) string {
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, zlib.DefaultCompression)
	zw.Write(b)
	zw.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

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
	// Read one byte past what the grid needs, so a mis-sized array is an
	// error rather than silently truncated.
	b, err := io.ReadAll(io.LimitReader(zr, int64(want)+1))
	if err != nil {
		return nil, err
	}
	if len(b) != want {
		return nil, fmt.Errorf("%d bytes, the grid needs %d", len(b), want)
	}
	return b, nil
}
