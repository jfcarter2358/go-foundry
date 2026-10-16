package paint

// region.go — what a stage needs to rerun only the ground a stroke changed
// (docs/terrain-paint/PHASE_P2.md, "Per-tile incremental reruns").
//
// A stage that works in pieces (a grass field per terrain tile, a scatter
// tile, a material's weights) keys each piece by the paint it can READ: the
// stored tiles whose texels a sample inside the piece's rectangle can touch.
// Sample is bilinear between texel centres, so a point within half a texel
// of a tile's edge already reads the neighbour's first texel; RectHash
// works out the texel range a sample in the rectangle can read and hashes
// those texels: a stored tile wholly in range by its content hash, a tile
// only partly in range by the texels of it in range. A tile that stores
// nothing, or nothing in range, contributes nothing, so painting a far
// corner leaves every other piece's key exactly as it was, and erasing a
// patch back to nothing restores the keys it changed.
//
// ChangedTiles answers the other direction for the editor and the tests:
// which tiles differ between two states of a layer (an undo step's before
// and after, a run's snapshot and the layer now).

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
)

// RectHash is the content key of everything a sample inside the rectangle
// (min x, min z, max x, max z) can read from the layer: the texels whose
// centres a bilinear sample there interpolates between. A stored tile
// wholly in reach contributes its content hash; one only partly in reach
// (a neighbour whose edge column a sample at the border reads) contributes
// a hash of just the texels in reach, so a stroke in the middle of a tile
// does not change its neighbours' keys. "" when no stored texel is in reach
// (also for a nil layer).
func (l *Layer) RectHash(minX, minZ, maxX, maxZ float64) string {
	if l.Empty() {
		return ""
	}
	r := l.Def.Res()
	n := l.Def.TexelsPerTile()
	// The global texel range a sample in the rectangle reads.
	gi0, gi1 := int(math.Floor(minX/r-0.5)), int(math.Floor(maxX/r-0.5))+1
	gj0, gj1 := int(math.Floor(minZ/r-0.5)), int(math.Floor(maxZ/r-0.5))+1
	h := sha256.New()
	any := false
	for _, k := range TilesOverlapping(float64(gi0)*r, float64(gj0)*r, float64(gi1+1)*r, float64(gj1+1)*r) {
		t := l.tiles[k]
		if t == nil {
			continue
		}
		// The tile's texels in range, local.
		li0, li1 := max(gi0-k.I*n, 0), min(gi1-k.I*n, n-1)
		lj0, lj1 := max(gj0-k.J*n, 0), min(gj1-k.J*n, n-1)
		if li0 > li1 || lj0 > lj1 {
			continue
		}
		if li0 == 0 && lj0 == 0 && li1 == n-1 && lj1 == n-1 {
			any = true
			fmt.Fprintf(h, "%s=%s\n", k, l.TileHash(k))
			continue
		}
		// A strip of zeros reads as no tile at all: creating a tile far
		// from the edge must not change a neighbour's key.
		zero := true
		for j := lj0; j <= lj1 && zero; j++ {
			for _, v := range t.V[(j*n+li0)*t.C : (j*n+li1+1)*t.C] {
				if v != 0 {
					zero = false
					break
				}
			}
		}
		if zero {
			continue
		}
		any = true
		fmt.Fprintf(h, "%s[%d,%d,%d,%d]=", k, li0, lj0, li1, lj1)
		var b [4]byte
		for j := lj0; j <= lj1; j++ {
			for i := li0; i <= li1; i++ {
				for c := 0; c < t.C; c++ {
					binary.LittleEndian.PutUint32(b[:], math.Float32bits(t.V[(j*n+i)*t.C+c]))
					h.Write(b[:])
				}
			}
		}
		h.Write([]byte{'\n'})
	}
	if !any {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ChangedTiles is every tile whose content differs between a and b (either
// may be nil, which stores nothing), in key order.
func ChangedTiles(a, b *Layer) []TileKey {
	seen := map[TileKey]bool{}
	var out []TileKey
	for _, l := range []*Layer{a, b} {
		for _, k := range l.Keys() {
			if seen[k] {
				continue
			}
			seen[k] = true
			if a.TileHash(k) != b.TileHash(k) {
				out = append(out, k)
			}
		}
	}
	sortKeys(out)
	return out
}

// SnapshotChanged is the keys of a snapshot pair (an undo step's before and
// after maps) whose tiles differ, in key order.
func SnapshotChanged(def LayerDef, before, after map[TileKey]*Tile) []TileKey {
	tmp := NewLayer(def)
	hash := func(t *Tile) string {
		if t == nil {
			return ""
		}
		return tmp.computeTileHash(t)
	}
	seen := map[TileKey]bool{}
	var out []TileKey
	for _, m := range []map[TileKey]*Tile{before, after} {
		for k := range m {
			if seen[k] {
				continue
			}
			seen[k] = true
			if hash(before[k]) != hash(after[k]) {
				out = append(out, k)
			}
		}
	}
	sortKeys(out)
	return out
}
