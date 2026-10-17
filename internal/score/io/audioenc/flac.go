package audioenc

// flac.go — a FLAC encoder in Go.
//
// FLAC is lossless: a file decodes to exactly the samples given it. Each
// block of 4096 samples a channel is predicted and only what the
// prediction misses (the residual) is stored, Rice-coded:
//
//   - The stereo pair is coded as left/right, left/side, right/side or
//     mid/side, whichever is smallest for the block (side = left − right,
//     one bit wider).
//   - Each channel's block is a constant (silence costs a few bytes), a
//     fixed polynomial predictor of order 0–4, or a linear predictor (LPC)
//     of order up to 12 from the block's autocorrelation (Levinson–Durbin
//     over a Tukey-windowed copy), its coefficients quantised to 14 bits
//     with error feedback — whichever codes smallest; verbatim when nothing
//     beats the samples themselves.
//   - The residual is split into 2^k partitions (k chosen per block) each
//     with its own Rice parameter, found exactly by counting bits.
//
// STREAMINFO carries the true frame sizes, the sample count and the MD5 of
// the audio, so a decoder can check what it got (DecodeFLAC does, and
// flacdec.go is how the tests check every file this writes); a
// VORBIS_COMMENT block carries the title and artist.

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"
)

// flacBlock is the samples a channel per frame.
const flacBlock = 4096

// flacMaxLPC is the highest LPC order tried; flacLPCPrecision the bits of
// a quantised coefficient.
const (
	flacMaxLPC       = 12
	flacLPCPrecision = 14
)

// bitWriter packs bits most significant first.
type bitWriter struct {
	buf  []byte
	acc  uint64
	nacc uint
}

func (w *bitWriter) write(v uint64, n uint) {
	for n > 0 {
		take := min(n, 56-w.nacc)
		w.acc = w.acc<<take | (v>>(n-take))&(1<<take-1)
		w.nacc += take
		n -= take
		for w.nacc >= 8 {
			w.buf = append(w.buf, byte(w.acc>>(w.nacc-8)))
			w.nacc -= 8
		}
	}
}

func (w *bitWriter) writeSigned(v int64, n uint) { w.write(uint64(v)&(1<<n-1), n) }

// unary writes q zeros and a one.
func (w *bitWriter) unary(q uint64) {
	for q >= 32 {
		w.write(0, 32)
		q -= 32
	}
	w.write(1, uint(q)+1)
}

// align pads to a byte with zeros.
func (w *bitWriter) align() {
	if w.nacc > 0 {
		w.write(0, 8-w.nacc)
	}
}

func (w *bitWriter) bytes() []byte { return w.buf }

// crc8 is FLAC's frame header CRC (polynomial x^8+x^2+x+1).
func crc8(b []byte) byte {
	var c byte
	for _, x := range b {
		c ^= x
		for i := 0; i < 8; i++ {
			if c&0x80 != 0 {
				c = c<<1 ^ 0x07
			} else {
				c <<= 1
			}
		}
	}
	return c
}

// crc16 is FLAC's frame CRC (polynomial x^16+x^15+x^2+1).
func crc16(b []byte) uint16 {
	var c uint16
	for _, x := range b {
		c ^= uint16(x) << 8
		for i := 0; i < 8; i++ {
			if c&0x8000 != 0 {
				c = c<<1 ^ 0x8005
			} else {
				c <<= 1
			}
		}
	}
	return c
}

// utf8Number is a frame number as FLAC writes it: UTF-8's coding of a
// number, up to 36 bits.
func utf8Number(n uint64) []byte {
	switch {
	case n < 0x80:
		return []byte{byte(n)}
	case n < 0x800:
		return []byte{0xC0 | byte(n>>6), 0x80 | byte(n&0x3F)}
	case n < 0x10000:
		return []byte{0xE0 | byte(n>>12), 0x80 | byte(n>>6&0x3F), 0x80 | byte(n&0x3F)}
	case n < 0x200000:
		return []byte{0xF0 | byte(n>>18), 0x80 | byte(n>>12&0x3F), 0x80 | byte(n>>6&0x3F), 0x80 | byte(n&0x3F)}
	case n < 0x4000000:
		return []byte{0xF8 | byte(n>>24), 0x80 | byte(n>>18&0x3F), 0x80 | byte(n>>12&0x3F), 0x80 | byte(n>>6&0x3F), 0x80 | byte(n&0x3F)}
	}
	return []byte{0xFC | byte(n>>30&1), 0x80 | byte(n>>24&0x3F), 0x80 | byte(n>>18&0x3F), 0x80 | byte(n>>12&0x3F), 0x80 | byte(n>>6&0x3F), 0x80 | byte(n&0x3F)}
}

// quantize turns float samples into integers of a bit depth, clipped.
func quantize(data []float32, bitsPer int) []int32 {
	out := make([]int32, len(data))
	scale := float64(int64(1)<<(bitsPer-1) - 1)
	for i, v := range data {
		x := float64(v)
		if x != x {
			x = 0
		}
		x = math.Max(-1, math.Min(1, x))
		out[i] = int32(math.Round(x * scale))
	}
	return out
}

// EncodeFLAC writes interleaved float samples as a FLAC file: channels at
// rate, 16 or 24 bits a sample.
func EncodeFLAC(data []float32, channels, rate, bitsPer int, tags Tags) ([]byte, error) {
	if channels < 1 || channels > 8 {
		return nil, fmt.Errorf("flac: %d channels", channels)
	}
	if rate < 1 || rate > 655350 {
		return nil, fmt.Errorf("flac: a rate of %d Hz", rate)
	}
	if bitsPer != 16 && bitsPer != 24 {
		return nil, fmt.Errorf("flac: %d bits a sample (16 or 24)", bitsPer)
	}
	pcm := quantize(data, bitsPer)
	frames := len(pcm) / channels
	pcm = pcm[:frames*channels]

	// The MD5 is of the samples as little-endian signed integers.
	h := md5.New()
	bps := bitsPer / 8
	raw := make([]byte, 0, 8192)
	for _, v := range pcm {
		for b := 0; b < bps; b++ {
			raw = append(raw, byte(v>>(8*b)))
		}
		if len(raw) >= 8192-bps {
			h.Write(raw)
			raw = raw[:0]
		}
	}
	h.Write(raw)
	var sum [16]byte
	copy(sum[:], h.Sum(nil))

	var body bytes.Buffer
	minFrame, maxFrame := uint32(math.MaxUint32), uint32(0)
	ch := make([][]int64, channels)
	for c := range ch {
		ch[c] = make([]int64, flacBlock)
	}
	for at, num := 0, uint64(0); at < frames; at, num = at+flacBlock, num+1 {
		n := min(flacBlock, frames-at)
		for c := range ch {
			ch[c] = ch[c][:n]
			for i := 0; i < n; i++ {
				ch[c][i] = int64(pcm[(at+i)*channels+c])
			}
		}
		f := encodeFrame(ch, n, num, rate, bitsPer)
		body.Write(f)
		minFrame = min(minFrame, uint32(len(f)))
		maxFrame = max(maxFrame, uint32(len(f)))
	}
	if frames == 0 {
		minFrame = 0
	}

	var out bytes.Buffer
	out.WriteString("fLaC")
	// STREAMINFO.
	si := &bitWriter{}
	// The block sizes leave out the last block, which may be shorter.
	si.write(flacBlock, 16)
	si.write(flacBlock, 16)
	si.write(uint64(minFrame), 24)
	si.write(uint64(maxFrame), 24)
	si.write(uint64(rate), 20)
	si.write(uint64(channels-1), 3)
	si.write(uint64(bitsPer-1), 5)
	si.write(uint64(frames), 36)
	block := append(si.bytes(), sum[:]...)
	writeMeta(&out, 0, false, block)
	writeMeta(&out, 4, true, vorbisComment(tags))
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

// writeMeta writes a metadata block: its last-block flag, type and length.
func writeMeta(out *bytes.Buffer, kind byte, last bool, b []byte) {
	hdr := kind
	if last {
		hdr |= 0x80
	}
	out.WriteByte(hdr)
	out.Write([]byte{byte(len(b) >> 16), byte(len(b) >> 8), byte(len(b))})
	out.Write(b)
}

// vorbisComment is a VORBIS_COMMENT block (little-endian lengths, as
// Vorbis has them).
func vorbisComment(t Tags) []byte {
	var b bytes.Buffer
	le := func(v int) { binary.Write(&b, binary.LittleEndian, uint32(v)) }
	vendor := Vendor
	le(len(vendor))
	b.WriteString(vendor)
	kv := t.pairs()
	le(len(kv))
	for _, p := range kv {
		s := p[0] + "=" + p[1]
		le(len(s))
		b.WriteString(s)
	}
	return b.Bytes()
}

// encodeFrame codes one block of every channel.
func encodeFrame(ch [][]int64, n int, num uint64, rate, bitsPer int) []byte {
	// The stereo decorrelations, tried in turn.
	type assign struct {
		code  uint64
		chans [][]int64
		bps   []int
	}
	cands := []assign{}
	plain := assign{code: uint64(len(ch) - 1), chans: ch, bps: make([]int, len(ch))}
	for i := range plain.bps {
		plain.bps[i] = bitsPer
	}
	cands = append(cands, plain)
	if len(ch) == 2 {
		l, r := ch[0], ch[1]
		side := make([]int64, n)
		mid := make([]int64, n)
		for i := 0; i < n; i++ {
			side[i] = l[i] - r[i]
			mid[i] = (l[i] + r[i]) >> 1
		}
		cands = append(cands,
			assign{code: 8, chans: [][]int64{l, side}, bps: []int{bitsPer, bitsPer + 1}},
			assign{code: 9, chans: [][]int64{side, r}, bps: []int{bitsPer + 1, bitsPer}},
			assign{code: 10, chans: [][]int64{mid, side}, bps: []int{bitsPer, bitsPer + 1}})
	}
	// Each channel's best subframe, planned once whichever pair wins.
	plans := map[*int64]subframe{}
	plan := func(x []int64, bps int) subframe {
		key := &x[0]
		if sf, ok := plans[key]; ok {
			return sf
		}
		sf := planSubframe(x, bps)
		plans[key] = sf
		return sf
	}
	best, bestBits := 0, math.MaxInt
	for i, a := range cands {
		total := 0
		for c, x := range a.chans {
			total += plan(x, a.bps[c]).bits
		}
		if total < bestBits {
			best, bestBits = i, total
		}
	}
	a := cands[best]

	// The header.
	hw := &bitWriter{}
	hw.write(0x3FFE, 14)
	hw.write(0, 1) // reserved
	hw.write(0, 1) // fixed block size
	var bsCode uint64
	switch {
	case n == flacBlock:
		bsCode = 12
	case n <= 256:
		bsCode = 6
	default:
		bsCode = 7
	}
	hw.write(bsCode, 4)
	var rateCode uint64
	switch rate {
	case 44100:
		rateCode = 9
	case 48000:
		rateCode = 10
	case 96000:
		rateCode = 11
	case 32000:
		rateCode = 8
	case 22050:
		rateCode = 6
	case 88200:
		rateCode = 1
	case 176400:
		rateCode = 2
	case 192000:
		rateCode = 3
	default:
		rateCode = 0 // from STREAMINFO
	}
	hw.write(rateCode, 4)
	hw.write(a.code, 4)
	switch bitsPer {
	case 16:
		hw.write(4, 3)
	case 24:
		hw.write(6, 3)
	}
	hw.write(0, 1)
	for _, b := range utf8Number(num) {
		hw.write(uint64(b), 8)
	}
	switch bsCode {
	case 6:
		hw.write(uint64(n-1), 8)
	case 7:
		hw.write(uint64(n-1), 16)
	}
	hw.write(uint64(crc8(hw.bytes())), 8)

	fw := &bitWriter{buf: append([]byte(nil), hw.bytes()...)}
	for c, x := range a.chans {
		writeSubframe(fw, x, a.bps[c], plan(x, a.bps[c]))
	}
	fw.align()
	crc := crc16(fw.bytes())
	fw.write(uint64(crc), 16)
	return fw.bytes()
}

// subframe is how a channel's block is coded: constant, verbatim, a
// fixed predictor or LPC, with its residual's partitions and parameters,
// and what it costs in bits.
type subframe struct {
	kind  int // subConstant, subVerbatim, subFixed, subLPC
	order int
	coefs []int32
	shift int
	res   []int64 // the residual after the warm-up samples
	rice  ricePlan
	bits  int
}

const (
	subConstant = iota
	subVerbatim
	subFixed
	subLPC
)

// planSubframe finds the predictor that codes a block smallest. Costs are
// estimated the way the reference encoder estimates them (a partition's
// bits from its sum), which is within a few bits of the exact count and
// a tenth of the work.
func planSubframe(x []int64, bps int) subframe {
	n := len(x)
	constant := true
	for _, v := range x[1:] {
		if v != x[0] {
			constant = false
			break
		}
	}
	if constant {
		return subframe{kind: subConstant, bits: 8 + bps}
	}
	best := subframe{kind: subVerbatim, bits: 8 + n*bps}
	res := make([]int64, n)
	consider := func(sf subframe) {
		if sf.bits < best.bits {
			sf.res = append([]int64(nil), res[sf.order:]...)
			best = sf
		}
	}
	for order := 0; order <= 4 && order < n; order++ {
		fixedResidual(x, order, res)
		rp, ok := planRice(res[order:], n, order, bps)
		if !ok {
			continue
		}
		consider(subframe{kind: subFixed, order: order, rice: rp, bits: 8 + order*bps + rp.bits})
	}
	if n > flacMaxLPC*2 {
		lpcs := lpcCoefficients(x, flacMaxLPC)
		for order := 1; order <= len(lpcs); order++ {
			coefs, shift, ok := quantizeLPC(lpcs[order-1], flacLPCPrecision)
			if !ok {
				continue
			}
			lpcResidual(x, coefs, shift, res)
			rp, ok := planRice(res[order:], n, order, bps)
			if !ok {
				continue
			}
			consider(subframe{kind: subLPC, order: order, coefs: coefs, shift: shift, rice: rp,
				bits: 8 + order*bps + 4 + 5 + order*flacLPCPrecision + rp.bits})
		}
	}
	return best
}

// writeSubframe writes a planned subframe.
func writeSubframe(w *bitWriter, x []int64, bps int, sf subframe) {
	w.write(0, 1)
	switch sf.kind {
	case subConstant:
		w.write(0, 6)
		w.write(0, 1)
		w.writeSigned(x[0], uint(bps))
		return
	case subVerbatim:
		w.write(1, 6)
		w.write(0, 1)
		for _, v := range x {
			w.writeSigned(v, uint(bps))
		}
		return
	case subFixed:
		w.write(uint64(8|sf.order), 6)
	case subLPC:
		w.write(uint64(32|(sf.order-1)), 6)
	}
	w.write(0, 1)
	for i := 0; i < sf.order; i++ {
		w.writeSigned(x[i], uint(bps))
	}
	if sf.kind == subLPC {
		w.write(flacLPCPrecision-1, 4)
		w.writeSigned(int64(sf.shift), 5)
		for _, c := range sf.coefs {
			w.writeSigned(int64(c), flacLPCPrecision)
		}
	}
	writeRice(w, sf.res, len(x), sf.order, sf.rice)
}

// fixedResidual is what a fixed predictor of an order misses.
func fixedResidual(x []int64, order int, res []int64) {
	for i := order; i < len(x); i++ {
		switch order {
		case 0:
			res[i] = x[i]
		case 1:
			res[i] = x[i] - x[i-1]
		case 2:
			res[i] = x[i] - 2*x[i-1] + x[i-2]
		case 3:
			res[i] = x[i] - 3*x[i-1] + 3*x[i-2] - x[i-3]
		case 4:
			res[i] = x[i] - 4*x[i-1] + 6*x[i-2] - 4*x[i-3] + x[i-4]
		}
	}
}

// lpcResidual is what quantised LPC coefficients miss.
func lpcResidual(x []int64, coefs []int32, shift int, res []int64) {
	order := len(coefs)
	for i := order; i < len(x); i++ {
		var sum int64
		for j, c := range coefs {
			sum += int64(c) * x[i-j-1]
		}
		res[i] = x[i] - sum>>uint(shift)
	}
}

// lpcCoefficients are the LPC coefficients of every order up to max, from
// the block's autocorrelation under a Tukey(0.5) window.
func lpcCoefficients(x []int64, maxOrder int) [][]float64 {
	n := len(x)
	wx := make([]float64, n)
	taper := n / 4 // half of 0.5 of the block at each end
	for i, v := range x {
		w := 1.0
		switch {
		case i < taper:
			w = 0.5 * (1 - math.Cos(math.Pi*float64(i)/float64(taper)))
		case i >= n-taper:
			w = 0.5 * (1 - math.Cos(math.Pi*float64(n-1-i)/float64(taper)))
		}
		wx[i] = float64(v) * w
	}
	r := make([]float64, maxOrder+1)
	for lag := 0; lag <= maxOrder; lag++ {
		var s float64
		for i := lag; i < n; i++ {
			s += wx[i] * wx[i-lag]
		}
		r[lag] = s
	}
	if r[0] == 0 {
		return nil
	}
	// Levinson–Durbin.
	var out [][]float64
	a := make([]float64, maxOrder+1)
	tmp := make([]float64, maxOrder+1)
	errv := r[0]
	for m := 1; m <= maxOrder; m++ {
		acc := r[m]
		for j := 1; j < m; j++ {
			acc -= a[j] * r[m-j]
		}
		k := acc / errv
		copy(tmp, a)
		a[m] = k
		for j := 1; j < m; j++ {
			a[j] = tmp[j] - k*tmp[m-j]
		}
		errv *= 1 - k*k
		out = append(out, append([]float64(nil), a[1:m+1]...))
		if errv <= 0 {
			break
		}
	}
	return out
}

// quantizeLPC quantises coefficients to a precision with error feedback,
// choosing the shift so the largest uses the bits.
func quantizeLPC(lp []float64, precision int) ([]int32, int, bool) {
	cmax := 0.0
	for _, c := range lp {
		cmax = math.Max(cmax, math.Abs(c))
	}
	if cmax <= 0 || math.IsNaN(cmax) || math.IsInf(cmax, 0) {
		return nil, 0, false
	}
	_, exp := math.Frexp(cmax)
	shift := precision - 1 - exp
	if shift > 15 {
		shift = 15
	}
	if shift < 0 {
		return nil, 0, false
	}
	qmax := int64(1)<<(precision-1) - 1
	out := make([]int32, len(lp))
	errv := 0.0
	for i, c := range lp {
		errv += c * float64(int64(1)<<uint(shift))
		q := int64(math.Round(errv))
		q = max(-qmax-1, min(qmax, q))
		errv -= float64(q)
		out[i] = int32(q)
	}
	return out, shift, true
}

// ricePlan is a residual's partitioning: the partition order and each
// partition's Rice parameter (method 1, five-bit parameters, for audio
// past 16 bits), and the bits it is estimated to take.
type ricePlan struct {
	method uint64
	order  int
	ks     []int
	bits   int
}

// planRice chooses the partition order and parameters for a residual
// (len n-order). It reports false when a value cannot be coded (a
// residual past 32 bits).
func planRice(res []int64, n, order, bps int) (ricePlan, bool) {
	method, maxK := uint64(0), 14
	if bps > 16 {
		method, maxK = 1, 30
	}
	paramBits := 4 + int(method)
	// Sums of the zigzagged residual at the finest partitioning; coarser
	// ones add them in pairs.
	top := 0
	for po := 1; po <= 8; po++ {
		if n%(1<<po) != 0 || n>>po <= order {
			break
		}
		top = po
	}
	parts := 1 << top
	sums := make([]uint64, parts)
	counts := make([]int, parts)
	i := 0
	for p := 0; p < parts; p++ {
		size := n >> top
		if p == 0 {
			size -= order
		}
		for _, r := range res[i : i+size] {
			if r > math.MaxInt32 || r < math.MinInt32 {
				return ricePlan{}, false
			}
			sums[p] += uint64(r<<1) ^ uint64(r>>63)
		}
		counts[p] = size
		i += size
	}
	best := ricePlan{method: method, bits: math.MaxInt}
	for po := top; po >= 0; po-- {
		ks := make([]int, len(sums))
		total := paramBits * len(sums)
		for p := range sums {
			k, b := riceEstimate(sums[p], counts[p], maxK)
			ks[p] = k
			total += b
		}
		if total < best.bits {
			best = ricePlan{method: method, order: po, ks: ks, bits: total}
		}
		if po > 0 {
			next := make([]uint64, len(sums)/2)
			nc := make([]int, len(sums)/2)
			for p := range next {
				next[p] = sums[2*p] + sums[2*p+1]
				nc[p] = counts[2*p] + counts[2*p+1]
			}
			sums, counts = next, nc
		}
	}
	return best, true
}

// riceEstimate is the parameter for a partition of count values summing
// to sum, and its bits: count·(k+1) + sum>>k.
func riceEstimate(sum uint64, count, maxK int) (int, int) {
	if count == 0 {
		return 0, 0
	}
	k := 0
	if mean := sum / uint64(count); mean > 0 {
		k = bits.Len64(mean) - 1
	}
	k = min(k, maxK)
	cost := func(k int) int { return count*(k+1) + int(sum>>uint(k)) }
	best, bb := k, cost(k)
	for _, c := range []int{k - 1, k + 1} {
		if c >= 0 && c <= maxK {
			if b := cost(c); b < bb {
				best, bb = c, b
			}
		}
	}
	return best, bb
}

// writeRice writes a residual as planned.
func writeRice(w *bitWriter, res []int64, n, order int, rp ricePlan) {
	w.write(rp.method, 2)
	w.write(uint64(rp.order), 4)
	paramBits := uint(4 + rp.method)
	start := 0
	for p, k := range rp.ks {
		size := n >> rp.order
		if p == 0 {
			size -= order
		}
		w.write(uint64(k), paramBits)
		for _, r := range res[start : start+size] {
			v := uint64(r<<1) ^ uint64(r>>63)
			w.unary(v >> uint(k))
			if k > 0 {
				w.write(v&(1<<uint(k)-1), uint(k))
			}
		}
		start += size
	}
}

// errFLAC is a file DecodeFLAC cannot read.
var errFLAC = errors.New("flac: not a FLAC stream this decoder reads")
