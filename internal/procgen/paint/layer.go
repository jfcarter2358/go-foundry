package paint

// layer.go — a layer's tiles, sampling, and its content hashes.
//
// A Layer is its declaration and a sparse map of tiles. Tiles are shared
// COPY-ON-WRITE between a layer and its clones: Clone copies the map of
// pointers and freezes every tile in it; the next stroke on either side
// copies a frozen tile before it writes. So the editor can hand a run or a
// preview a snapshot of the paint (one map copy) and go on painting while
// the snapshot is read on another goroutine.
//
// HASHES. A tile's hash is sha256 over the layer's kind, the tile's size and
// its samples as its file stores them, cached until the tile is written. A
// layer's hash folds its declaration and every tile's (key, hash) in key
// order. A stage that reads a layer folds the layer's hash into its own
// (procgen's plan), so painting reruns it, and painting another layer does
// not. Phase P2 will hash per terrain tile with TileHash and
// TilesOverlapping instead.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"sync/atomic"
)

// TileKey names a tile: it covers x in [I·TileSize, (I+1)·TileSize) and z
// in [J·TileSize, (J+1)·TileSize).
type TileKey struct{ I, J int }

func (k TileKey) String() string { return fmt.Sprintf("%d_%d", k.I, k.J) }

// Less orders keys by J then I (rows), the order hashes and saves walk.
func (k TileKey) Less(o TileKey) bool {
	if k.J != o.J {
		return k.J < o.J
	}
	return k.I < o.I
}

// TileAt is the key of the tile holding world point (x, z).
func TileAt(x, z float64) TileKey {
	return TileKey{int(math.Floor(x / TileSize)), int(math.Floor(z / TileSize))}
}

// Rect is the tile's world rectangle: min x, min z, max x, max z.
func (k TileKey) Rect() [4]float64 {
	x, z := float64(k.I)*TileSize, float64(k.J)*TileSize
	return [4]float64{x, z, x + TileSize, z + TileSize}
}

// TilesOverlapping is every tile key whose square meets the rectangle (min
// x, min z, max x, max z; a rectangle ending exactly on a tile edge does
// not reach the next tile), rows then columns.
func TilesOverlapping(minX, minZ, maxX, maxZ float64) []TileKey {
	i0, j0 := int(math.Floor(minX/TileSize)), int(math.Floor(minZ/TileSize))
	i1, j1 := int(math.Ceil(maxX/TileSize))-1, int(math.Ceil(maxZ/TileSize))-1
	var out []TileKey
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			out = append(out, TileKey{i, j})
		}
	}
	return out
}

// Tile is one tile's samples: N×N texels of C channels, row-major (z rows,
// x columns), channels interleaved. Values are already quantised.
type Tile struct {
	N, C int
	V    []float32

	frozen atomic.Bool
	// hash caches the content hash; reset by a write. Atomic: two
	// snapshots on two goroutines may hash one shared frozen tile.
	hash atomic.Pointer[string]
}

func newTile(n, c int) *Tile { return &Tile{N: n, C: c, V: make([]float32, n*n*c)} }

// copyTile is a writable copy.
func (t *Tile) copyTile() *Tile {
	return &Tile{N: t.N, C: t.C, V: append([]float32(nil), t.V...)}
}

// zero reports whether every sample is 0.
func (t *Tile) zero() bool {
	for _, v := range t.V {
		if v != 0 {
			return false
		}
	}
	return true
}

// Layer is a declared layer and its tiles.
type Layer struct {
	Def   LayerDef
	tiles map[TileKey]*Tile
	// rev counts writes, for an editor overlay that caches per tile.
	rev uint64
}

// NewLayer is an empty layer.
func NewLayer(def LayerDef) *Layer { return &Layer{Def: def, tiles: map[TileKey]*Tile{}} }

// Clone is a snapshot sharing the tiles copy-on-write. Nil for nil.
func (l *Layer) Clone() *Layer {
	if l == nil {
		return nil
	}
	out := &Layer{Def: l.Def, tiles: make(map[TileKey]*Tile, len(l.tiles)), rev: l.rev}
	for k, t := range l.tiles {
		t.frozen.Store(true)
		out.tiles[k] = t
	}
	return out
}

// Rev is a counter bumped by every write.
func (l *Layer) Rev() uint64 {
	if l == nil {
		return 0
	}
	return l.rev
}

// Empty reports whether the layer holds no tiles.
func (l *Layer) Empty() bool { return l == nil || len(l.tiles) == 0 }

// Keys is every stored tile's key, in key order.
func (l *Layer) Keys() []TileKey {
	if l == nil {
		return nil
	}
	out := make([]TileKey, 0, len(l.tiles))
	for k := range l.tiles {
		out = append(out, k)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Less(out[b]) })
	return out
}

// Tile is the stored tile at k, or nil. Read it, never write it.
func (l *Layer) Tile(k TileKey) *Tile {
	if l == nil {
		return nil
	}
	return l.tiles[k]
}

// writable is the tile at k ready to write: created when missing, copied
// when frozen.
func (l *Layer) writable(k TileKey) *Tile {
	t := l.tiles[k]
	switch {
	case t == nil:
		t = newTile(l.Def.TexelsPerTile(), l.Def.Channels())
		l.tiles[k] = t
	case t.frozen.Load():
		t = t.copyTile()
		l.tiles[k] = t
	}
	t.hash.Store(nil)
	return t
}

// prune deletes the tiles among keys that are all zero.
func (l *Layer) prune(keys []TileKey) {
	for _, k := range keys {
		if t := l.tiles[k]; t != nil && t.zero() {
			delete(l.tiles, k)
		}
	}
}

// SetTile stores t at k (nil, or all zero, deletes it). For loading and
// undo; t becomes the layer's.
func (l *Layer) SetTile(k TileKey, t *Tile) {
	l.rev++
	if t == nil || t.zero() {
		delete(l.tiles, k)
		return
	}
	l.tiles[k] = t
}

// Snapshot is the tiles at keys as they stand (nil where there is none),
// frozen so later strokes copy rather than change them: what an undo step
// keeps.
func (l *Layer) Snapshot(keys []TileKey) map[TileKey]*Tile {
	out := make(map[TileKey]*Tile, len(keys))
	for _, k := range keys {
		t := l.tiles[k]
		if t != nil {
			t.frozen.Store(true)
		}
		out[k] = t
	}
	return out
}

// Restore puts a Snapshot back.
func (l *Layer) Restore(snap map[TileKey]*Tile) {
	for k, t := range snap {
		if t != nil {
			t.frozen.Store(true)
		}
		l.SetTile(k, t)
	}
}

// ---------- sampling ----------

// texel is channel c of the texel at global texel coordinates (gi, gj), 0
// where no tile is stored.
func (l *Layer) texel(gi, gj, c int) float64 {
	n := l.Def.TexelsPerTile()
	ti, tj := floorDiv(gi, n), floorDiv(gj, n)
	t := l.tiles[TileKey{ti, tj}]
	if t == nil {
		return 0
	}
	li, lj := gi-ti*n, gj-tj*n
	return float64(t.V[(lj*n+li)*t.C+c])
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// Sample is channel 0 at world point (x, z), bilinear between texel
// centres; 0 for a nil layer or where nothing is painted.
func (l *Layer) Sample(x, z float64) float64 { return l.SampleChannel(x, z, 0) }

// SampleChannel is channel c at (x, z) (direction's x is 0, z is 1).
func (l *Layer) SampleChannel(x, z float64, c int) float64 {
	if l == nil || len(l.tiles) == 0 || c >= l.Def.Channels() {
		return 0
	}
	r := l.Def.Res()
	fx, fz := x/r-0.5, z/r-0.5
	i0, j0 := math.Floor(fx), math.Floor(fz)
	tx, tz := fx-i0, fz-j0
	gi, gj := int(i0), int(j0)
	a := l.texel(gi, gj, c)
	b := l.texel(gi+1, gj, c)
	cc := l.texel(gi, gj+1, c)
	d := l.texel(gi+1, gj+1, c)
	return (a*(1-tx)+b*tx)*(1-tz) + (cc*(1-tx)+d*tx)*tz
}

// Direction is the direction layer's vector at (x, z); its length is the
// strength.
func (l *Layer) Direction(x, z float64) [2]float64 {
	return [2]float64{l.SampleChannel(x, z, 0), l.SampleChannel(x, z, 1)}
}

// ---------- hashes ----------

// TileHash is the stored tile's content hash, "" when there is none.
func (l *Layer) TileHash(k TileKey) string {
	if l == nil {
		return ""
	}
	t := l.tiles[k]
	if t == nil {
		return ""
	}
	if h := t.hash.Load(); h != nil {
		return *h
	}
	h := l.computeTileHash(t)
	t.hash.Store(&h)
	return h
}

func (l *Layer) computeTileHash(t *Tile) string {
	h := sha256.New()
	h.Write([]byte("paint-tile\x00" + l.Def.Kind + "\x00"))
	var b [8]byte
	binary.LittleEndian.PutUint32(b[:4], uint32(t.N))
	binary.LittleEndian.PutUint32(b[4:], uint32(t.C))
	h.Write(b[:])
	h.Write(encodeSamples(l.Def, t))
	return hex.EncodeToString(h.Sum(nil))
}

// TileHashes is every stored tile's hash by key.
func (l *Layer) TileHashes() map[TileKey]string {
	out := map[TileKey]string{}
	for _, k := range l.Keys() {
		out[k] = l.TileHash(k)
	}
	return out
}

// Hash is the layer's content hash: its declaration and every tile's.
func (l *Layer) Hash() string {
	if l == nil {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "paint-layer\x00%s\x00%s\x00%g\x00", l.Def.Name, l.Def.Kind, l.Def.Res())
	for _, k := range l.Keys() {
		fmt.Fprintf(h, "%s=%s\n", k, l.TileHash(k))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// encodeSamples is the tile's samples as its file stores them, row-major:
// a byte per sample for the grey kinds, two big-endian bytes for height,
// two bytes (x, z) for direction.
func encodeSamples(d LayerDef, t *Tile) []byte {
	switch d.Kind {
	case KindHeight:
		out := make([]byte, 2*len(t.V))
		for i, v := range t.V {
			binary.BigEndian.PutUint16(out[2*i:], encodeHeight(float64(v)))
		}
		return out
	case KindDirection:
		out := make([]byte, len(t.V))
		for i, v := range t.V {
			out[i] = encodeDir(float64(v))
		}
		return out
	}
	out := make([]byte, len(t.V))
	for i, v := range t.V {
		out[i] = encodeUnit(float64(v))
	}
	return out
}
