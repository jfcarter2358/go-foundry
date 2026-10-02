package nanite

// quant.go — how a cooked mesh's vertices are quantised, and the bit packing
// its pages are written in. Phase 3 of the Nanite plan (docs/nanite/PHASE_3.md),
// component 3.
//
// # One grid for the whole mesh
//
// Every position is an integer on ONE grid per mesh: q = round(p / Step),
// with Step a power of two. Two clusters that share a corner therefore share
// its integers exactly, however each one packs them — which is what keeps the
// paged mesh as crack-free as the float pool it came from. Step is chosen
// from the mesh's size (PositionBits: the grid is at least 2^PositionBits
// steps across the mesh's largest extent, about 8 µm on a two-metre rock at
// the default 18) and never finer than the float32 the decoder rebuilds can
// hold: every |q| stays under 2^23, so q·Step is exactly a float32 and the
// decode is exact, on any machine.
//
// UVs are integers on a grid of UVStep (2^-UVBits of a unit, a quarter of a
// texel of a 4096 texture at the default 14), and normals are OCTAHEDRAL —
// the unit sphere folded onto a square — with NormalBits a component (about
// 0.03° at the default 12).
//
// # Snapping
//
// The cook QUANTISES ITS POOL before it does anything else with it
// (Quantize): every position, UV and normal is replaced by what its integers
// decode to, and the integers are kept. Everything after — the weld's
// identities, the clusters' bounds, the DAG's simplification and errors, the
// collision cut, the distance field — is then made from exactly the numbers
// the runtime will decode, and the page writer packs the kept integers rather
// than re-quantising floats (an octahedral normal does not always survive a
// second round trip bit for bit). What the cook holds in memory and what a
// load decodes are the same floats.
//
// # Bit packing
//
// A page's clusters are written with bitWriter: each field at the fewest bits
// its range needs (a cluster's positions relative to its own lowest corner,
// at the bits its own extent needs on each axis), least significant bit
// first, a cluster starting on a byte. bitReader reads them back and reports
// a short or overrun stream as an error rather than a panic, since a page is
// read from a file that may have been replaced under a running game.

import (
	"errors"
	"math"
	"math/bits"

	"github.com/go-gl/mathgl/mgl32"
)

// The default quantisation (Quant).
const (
	DefaultPositionBits = 18
	DefaultUVBits       = 14
	DefaultNormalBits   = 12
)

// Quant is a mesh's quantisation.
type Quant struct {
	// Step is the position grid, a power of two (in the mesh's units).
	Step float32
	// UVStep is the UV grid, a power of two.
	UVStep float32
	// NormalBits is each octahedral component's bits.
	NormalBits uint8
}

// NewQuant chooses the grids for a mesh whose positions span lo..hi and
// whose UVs reach maxUV in magnitude; zero bit counts take the defaults.
func NewQuant(lo, hi mgl32.Vec3, maxUV float32, posBits, uvBits, normalBits int) Quant {
	if posBits <= 0 {
		posBits = DefaultPositionBits
	}
	if uvBits <= 0 {
		uvBits = DefaultUVBits
	}
	if normalBits <= 0 {
		normalBits = DefaultNormalBits
	}
	normalBits = min(normalBits, 16)
	ext := float64(max(hi[0]-lo[0], hi[1]-lo[1], hi[2]-lo[2]))
	maxAbs := 0.0
	for k := 0; k < 3; k++ {
		maxAbs = max(maxAbs, math.Abs(float64(lo[k])), math.Abs(float64(hi[k])))
	}
	e := -20
	if ext > 0 {
		e = int(math.Floor(math.Log2(ext))) - posBits
	}
	// Never finer than 2^23 steps from the origin: q·Step must be a float32
	// exactly.
	if maxAbs > 0 {
		e = max(e, int(math.Ceil(math.Log2(maxAbs)))-23)
	}
	eu := -uvBits
	if maxUV > 0 {
		eu = max(eu, int(math.Ceil(math.Log2(float64(maxUV))))-23)
	}
	return Quant{Step: float32(math.Ldexp(1, e)), UVStep: float32(math.Ldexp(1, eu)), NormalBits: uint8(normalBits)}
}

// PosQ is a position coordinate's integer.
func (q Quant) PosQ(v float32) int32 { return int32(math.RoundToEven(float64(v) / float64(q.Step))) }

// Pos is an integer's position coordinate: exact (see the file comment).
func (q Quant) Pos(i int32) float32 { return float32(i) * q.Step }

// UVQ is a UV coordinate's integer.
func (q Quant) UVQ(v float32) int32 { return int32(math.RoundToEven(float64(v) / float64(q.UVStep))) }

// UV is an integer's UV coordinate.
func (q Quant) UV(i int32) float32 { return float32(i) * q.UVStep }

// NormalQ is a normal's octahedral integers. A zero normal is taken as +Z.
func (q Quant) NormalQ(n mgl32.Vec3) [2]uint16 {
	x, y, z := float64(n[0]), float64(n[1]), float64(n[2])
	s := math.Abs(x) + math.Abs(y) + math.Abs(z)
	if s == 0 || math.IsNaN(s) {
		x, y, z, s = 0, 0, 1, 1
	}
	x, y, z = x/s, y/s, z/s
	if z < 0 {
		x, y = (1-math.Abs(y))*sign(x), (1-math.Abs(x))*sign(y)
	}
	m := float64(uint32(1)<<q.NormalBits - 1)
	enc := func(v float64) uint16 {
		return uint16(min(max(math.RoundToEven((v*0.5+0.5)*m), 0), m))
	}
	return [2]uint16{enc(x), enc(y)}
}

// Normal is octahedral integers' unit normal.
func (q Quant) Normal(o [2]uint16) mgl32.Vec3 {
	// Every product is converted before it is summed, which forbids the
	// compiler a fused multiply-add: the decode must give the same bits on
	// every machine (the cook's and the player's).
	m := float64(uint32(1)<<q.NormalBits - 1)
	x := float64(float64(o[0])/m*2) - 1
	y := float64(float64(o[1])/m*2) - 1
	z := 1 - math.Abs(x) - math.Abs(y)
	if z < 0 {
		x, y = float64((1-math.Abs(y))*sign(x)), float64((1-math.Abs(x))*sign(y))
	}
	l := math.Sqrt(float64(x*x) + float64(y*y) + float64(z*z))
	return mgl32.Vec3{float32(x / l), float32(y / l), float32(z / l)}
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// QVertex is one pool vertex's integers.
type QVertex struct {
	Pos    [3]int32
	UV     [2]int32
	Normal [2]uint16
}

// Quantize snaps a pool — 5 floats a vertex (x, y, z, u, v) and 3 normal
// floats a vertex — to q in place, and returns each vertex's integers.
func (q Quant) Quantize(verts, normals []float32) []QVertex {
	nv := len(verts) / 5
	out := make([]QVertex, nv)
	for v := 0; v < nv; v++ {
		var qv QVertex
		for k := 0; k < 3; k++ {
			qv.Pos[k] = q.PosQ(verts[5*v+k])
			verts[5*v+k] = q.Pos(qv.Pos[k])
		}
		for k := 0; k < 2; k++ {
			qv.UV[k] = q.UVQ(verts[5*v+3+k])
			verts[5*v+3+k] = q.UV(qv.UV[k])
		}
		var n mgl32.Vec3
		if 3*v+2 < len(normals) {
			n = mgl32.Vec3{normals[3*v], normals[3*v+1], normals[3*v+2]}
		}
		qv.Normal = q.NormalQ(n)
		if 3*v+2 < len(normals) {
			d := q.Normal(qv.Normal)
			normals[3*v], normals[3*v+1], normals[3*v+2] = d[0], d[1], d[2]
		}
		out[v] = qv
	}
	return out
}

// bitsFor is the bits an unsigned range of span (max - min) needs.
func bitsFor(span uint32) uint8 { return uint8(bits.Len32(span)) }

// ---------- bit packing ----------

// bitWriter packs fields least significant bit first.
type bitWriter struct {
	buf []byte
	acc uint64
	n   uint
}

// write appends the low nbits (<= 32) of v.
func (w *bitWriter) write(v uint32, nbits uint8) {
	if nbits == 0 {
		return
	}
	w.acc |= uint64(v&uint32(uint64(1)<<nbits-1)) << w.n
	w.n += uint(nbits)
	for w.n >= 8 {
		w.buf = append(w.buf, byte(w.acc))
		w.acc >>= 8
		w.n -= 8
	}
}

// align pads to the next byte.
func (w *bitWriter) align() {
	if w.n > 0 {
		w.buf = append(w.buf, byte(w.acc))
		w.acc, w.n = 0, 0
	}
}

func (w *bitWriter) writeI32(v int32) { w.write(uint32(v), 32) }

// errShort is a stream that ends before its fields do.
var errShort = errors.New("page data is truncated or damaged")

// bitReader reads what bitWriter wrote.
type bitReader struct {
	buf []byte
	pos int
	acc uint64
	n   uint
}

func (r *bitReader) read(nbits uint8) (uint32, error) {
	if nbits == 0 {
		return 0, nil
	}
	for r.n < uint(nbits) {
		if r.pos >= len(r.buf) {
			return 0, errShort
		}
		r.acc |= uint64(r.buf[r.pos]) << r.n
		r.pos++
		r.n += 8
	}
	v := uint32(r.acc & (uint64(1)<<nbits - 1))
	r.acc >>= nbits
	r.n -= uint(nbits)
	return v, nil
}

func (r *bitReader) align() { r.acc, r.n = 0, 0 }

func (r *bitReader) readI32() (int32, error) {
	v, err := r.read(32)
	return int32(v), err
}
