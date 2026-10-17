package texcompress

// bc7.go — a BC7 (BPTC) encoder that writes mode 6 only, and its decoder.
//
// BC7 has eight block modes trading subsets, endpoint precision and index
// precision. Mode 6 is the single-subset one with the richest endpoints: RGBA
// endpoints of 7 bits per channel, each endpoint with one extra "p-bit" shared
// by its four channels (so effectively 8 bits with a tied low bit), and a
// 4-bit index per texel choosing one of 16 points on the line between them.
// On smooth photographic content — the ground, rock and bark sets this exists
// for — a block rarely holds two unrelated colours, and mode 6 alone reaches
// 40+ dB; the multi-subset modes earn their keep only on hard edges between
// distinct colours, which is where this encoder is weakest (a known gap; a
// mode 1/3 search would be the next step if it ever shows).
//
// The fit, per block:
//
//  1. The principal axis of the 16 texels in RGBA (power iteration on the
//     covariance), and the texels' extent along it, give a first pair of
//     endpoints.
//  2. Each candidate pair is quantized under all four p-bit combinations,
//     indices are assigned (projection onto the quantized line, then the
//     nearest of the three palette entries around it), and the combination
//     with the least squared error wins.
//  3. Refinement: with the indices fixed, the endpoints that minimise the
//     squared error are a 2x2 least-squares solve per channel; those are
//     quantized and re-indexed in turn, and kept only if they do better.
//
// Bit layout of a mode-6 block, least significant bit first: 7 mode bits
// (0000001), R0 R1 G0 G1 B0 B1 A0 A1 at 7 bits each, P0, P1, then 16 indices
// of 4 bits except the first ("anchor") index, which has 3 — its top bit is
// implied 0, which the encoder guarantees by swapping the endpoints (and
// mirroring every index) whenever texel 0 would need an index of 8 or more.

import "math"

// bc7Weights4 are BC7's interpolation weights for 4-bit indices, out of 64.
var bc7Weights4 = [16]int32{0, 4, 9, 13, 17, 21, 26, 30, 34, 38, 43, 47, 51, 55, 60, 64}

// bc7NearestWeight maps t in 0..64 (position along the endpoint line, in
// 64ths) to the index whose weight is nearest it.
var bc7NearestWeight = func() (lut [65]uint8) {
	for t := range lut {
		best, bestD := 0, int32(1<<30)
		for i, w := range bc7Weights4 {
			d := w - int32(t)
			if d < 0 {
				d = -d
			}
			if d < bestD {
				best, bestD = i, d
			}
		}
		lut[t] = uint8(best)
	}
	return
}()

// bc7Interp is BC7's integer interpolation of one channel.
func bc7Interp(e0, e1, w int32) int32 {
	return ((64-w)*e0 + w*e1 + 32) >> 6
}

// bc7Candidate is one quantized endpoint pair with its p-bits and indices.
type bc7Candidate struct {
	q0, q1 [4]int32 // 7-bit endpoints
	p0, p1 int32    // p-bits
	idx    [16]uint8
	err    int64
}

// endpoints is the candidate's endpoints as the decoder sees them, 8 bits.
func (c *bc7Candidate) endpoints() (e0, e1 [4]int32) {
	for i := 0; i < 4; i++ {
		e0[i] = c.q0[i]<<1 | c.p0
		e1[i] = c.q1[i]<<1 | c.p1
	}
	return
}

// encodeBC7Block encodes 16 RGBA texels (row-major) into out (16 bytes).
func encodeBC7Block(px *[16][4]int32, out []byte) {
	// The extent of the block, and the trivial solid case.
	var lo, hi [4]int32
	for c := 0; c < 4; c++ {
		lo[c], hi[c] = 255, 0
	}
	for i := range px {
		for c := 0; c < 4; c++ {
			v := px[i][c]
			lo[c] = min(lo[c], v)
			hi[c] = max(hi[c], v)
		}
	}

	e0, e1 := bc7InitialEndpoints(px, lo, hi)
	best := bc7Quantize(px, e0, e1)

	// Refinement passes: least-squares endpoints for the indices in hand.
	for iter := 0; iter < 2 && best.err > 0; iter++ {
		f0, f1, ok := bc7LeastSquares(px, &best.idx)
		if !ok {
			break
		}
		c := bc7Quantize(px, f0, f1)
		if c.err >= best.err {
			break
		}
		best = c
	}
	bc7Pack(&best, out)
}

// bc7InitialEndpoints is the principal-axis fit: the mean, the axis of most
// variance, and the texels' extremes along it.
func bc7InitialEndpoints(px *[16][4]int32, lo, hi [4]int32) (e0, e1 [4]float64) {
	var mean [4]float64
	for i := range px {
		for c := 0; c < 4; c++ {
			mean[c] += float64(px[i][c])
		}
	}
	for c := 0; c < 4; c++ {
		mean[c] /= 16
	}
	var cov [4][4]float64
	for i := range px {
		var d [4]float64
		for c := 0; c < 4; c++ {
			d[c] = float64(px[i][c]) - mean[c]
		}
		for a := 0; a < 4; a++ {
			for b := a; b < 4; b++ {
				cov[a][b] += d[a] * d[b]
			}
		}
	}
	for a := 0; a < 4; a++ {
		for b := 0; b < a; b++ {
			cov[a][b] = cov[b][a]
		}
	}
	// Start the power iteration from the bounding box's diagonal, which is
	// already close to the axis on most blocks.
	var axis [4]float64
	for c := 0; c < 4; c++ {
		axis[c] = float64(hi[c] - lo[c])
	}
	if axis == [4]float64{} {
		return mean, mean
	}
	for it := 0; it < 6; it++ {
		var n [4]float64
		for a := 0; a < 4; a++ {
			for b := 0; b < 4; b++ {
				n[a] += cov[a][b] * axis[b]
			}
		}
		l := math.Sqrt(n[0]*n[0] + n[1]*n[1] + n[2]*n[2] + n[3]*n[3])
		if l < 1e-9 {
			break
		}
		for c := 0; c < 4; c++ {
			axis[c] = n[c] / l
		}
	}
	l := math.Sqrt(axis[0]*axis[0] + axis[1]*axis[1] + axis[2]*axis[2] + axis[3]*axis[3])
	if l < 1e-9 {
		return mean, mean
	}
	for c := 0; c < 4; c++ {
		axis[c] /= l
	}
	tmin, tmax := math.Inf(1), math.Inf(-1)
	for i := range px {
		var t float64
		for c := 0; c < 4; c++ {
			t += (float64(px[i][c]) - mean[c]) * axis[c]
		}
		tmin = math.Min(tmin, t)
		tmax = math.Max(tmax, t)
	}
	for c := 0; c < 4; c++ {
		e0[c] = mean[c] + axis[c]*tmin
		e1[c] = mean[c] + axis[c]*tmax
	}
	return e0, e1
}

// bc7LeastSquares is the endpoint pair minimising the squared error for the
// given indices, or false when the indices do not pin one down (all equal).
func bc7LeastSquares(px *[16][4]int32, idx *[16]uint8) (e0, e1 [4]float64, ok bool) {
	var aa, ab, bb float64
	var ap, bp [4]float64
	for i := range px {
		b := float64(bc7Weights4[idx[i]]) / 64
		a := 1 - b
		aa += a * a
		ab += a * b
		bb += b * b
		for c := 0; c < 4; c++ {
			v := float64(px[i][c])
			ap[c] += a * v
			bp[c] += b * v
		}
	}
	det := aa*bb - ab*ab
	if math.Abs(det) < 1e-9 {
		return e0, e1, false
	}
	for c := 0; c < 4; c++ {
		e0[c] = (bb*ap[c] - ab*bp[c]) / det
		e1[c] = (aa*bp[c] - ab*ap[c]) / det
	}
	return e0, e1, true
}

// bc7Quantize is the best of the four p-bit quantizations of a float endpoint
// pair, indices assigned.
func bc7Quantize(px *[16][4]int32, e0, e1 [4]float64) bc7Candidate {
	var best bc7Candidate
	best.err = math.MaxInt64
	for p := int32(0); p < 4; p++ {
		var c bc7Candidate
		c.p0, c.p1 = p&1, p>>1
		for ch := 0; ch < 4; ch++ {
			c.q0[ch] = quant7(e0[ch], c.p0)
			c.q1[ch] = quant7(e1[ch], c.p1)
		}
		bc7AssignIndices(px, &c)
		if c.err < best.err {
			best = c
		}
	}
	return best
}

// quant7 is the 7-bit value whose (v<<1|p) is nearest f.
func quant7(f float64, p int32) int32 {
	q := int32(math.Floor((f-float64(p))/2 + 0.5))
	return min(max(q, 0), 127)
}

// bc7AssignIndices picks each texel's index on c's quantized line and
// totals the squared error.
func bc7AssignIndices(px *[16][4]int32, c *bc7Candidate) {
	e0, e1 := c.endpoints()
	var pal [16][4]int32
	for k := 0; k < 16; k++ {
		for ch := 0; ch < 4; ch++ {
			pal[k][ch] = bc7Interp(e0[ch], e1[ch], bc7Weights4[k])
		}
	}
	var d [4]int32
	var dd int32
	for ch := 0; ch < 4; ch++ {
		d[ch] = e1[ch] - e0[ch]
		dd += d[ch] * d[ch]
	}
	c.err = 0
	for i := range px {
		guess := 0
		if dd > 0 {
			var dot int32
			for ch := 0; ch < 4; ch++ {
				dot += (px[i][ch] - e0[ch]) * d[ch]
			}
			t := (dot*64 + dd/2) / dd
			guess = int(bc7NearestWeight[min(max(t, 0), 64)])
		}
		bestK, bestE := guess, int64(math.MaxInt64)
		for k := max(guess-1, 0); k <= min(guess+1, 15); k++ {
			var e int64
			for ch := 0; ch < 4; ch++ {
				v := int64(px[i][ch] - pal[k][ch])
				e += v * v
			}
			if e < bestE {
				bestK, bestE = k, e
			}
		}
		c.idx[i] = uint8(bestK)
		c.err += bestE
	}
}

// bitWriter appends bits least-significant first into a 128-bit block.
type bitWriter struct {
	lo, hi uint64
	n      uint
}

func (w *bitWriter) put(v uint64, bits uint) {
	for b := uint(0); b < bits; b++ {
		bit := (v >> b) & 1
		if w.n < 64 {
			w.lo |= bit << w.n
		} else {
			w.hi |= bit << (w.n - 64)
		}
		w.n++
	}
}

func (w *bitWriter) bytes(out []byte) {
	for i := 0; i < 8; i++ {
		out[i] = byte(w.lo >> (8 * i))
		out[8+i] = byte(w.hi >> (8 * i))
	}
}

// bc7Pack writes c as a mode-6 block, fixing up the anchor index.
func bc7Pack(c *bc7Candidate, out []byte) {
	if c.idx[0] >= 8 {
		c.q0, c.q1 = c.q1, c.q0
		c.p0, c.p1 = c.p1, c.p0
		for i := range c.idx {
			c.idx[i] = 15 - c.idx[i]
		}
	}
	var w bitWriter
	w.put(1<<6, 7)
	for ch := 0; ch < 4; ch++ {
		w.put(uint64(c.q0[ch]), 7)
		w.put(uint64(c.q1[ch]), 7)
	}
	w.put(uint64(c.p0), 1)
	w.put(uint64(c.p1), 1)
	w.put(uint64(c.idx[0]), 3)
	for i := 1; i < 16; i++ {
		w.put(uint64(c.idx[i]), 4)
	}
	w.bytes(out)
}

// bitReader reads bits least-significant first from a 128-bit block.
type bitReader struct {
	lo, hi uint64
	n      uint
}

func newBitReader(b []byte) *bitReader {
	r := &bitReader{}
	for i := 0; i < 8; i++ {
		r.lo |= uint64(b[i]) << (8 * i)
		r.hi |= uint64(b[8+i]) << (8 * i)
	}
	return r
}

func (r *bitReader) get(bits uint) uint64 {
	var v uint64
	for b := uint(0); b < bits; b++ {
		var bit uint64
		if r.n < 64 {
			bit = (r.lo >> r.n) & 1
		} else {
			bit = (r.hi >> (r.n - 64)) & 1
		}
		v |= bit << b
		r.n++
	}
	return v
}

// DecodeBC7Block decodes one mode-6 block into 16 RGBA texels, row-major. It
// returns false for any other mode — this package only ever writes mode 6, and
// the decoder exists for the tests and for measuring quality.
func DecodeBC7Block(b []byte, out *[16][4]uint8) bool {
	r := newBitReader(b)
	if r.get(7) != 1<<6 {
		return false
	}
	var q0, q1 [4]int32
	for ch := 0; ch < 4; ch++ {
		q0[ch] = int32(r.get(7))
		q1[ch] = int32(r.get(7))
	}
	p0, p1 := int32(r.get(1)), int32(r.get(1))
	var idx [16]int
	idx[0] = int(r.get(3))
	for i := 1; i < 16; i++ {
		idx[i] = int(r.get(4))
	}
	for i := 0; i < 16; i++ {
		for ch := 0; ch < 4; ch++ {
			out[i][ch] = uint8(bc7Interp(q0[ch]<<1|p0, q1[ch]<<1|p1, bc7Weights4[idx[i]]))
		}
	}
	return true
}
