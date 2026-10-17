package audioenc

// flacdec.go — reading FLAC back: every subframe kind, the stereo
// decorrelations, both residual coding methods, and every check the format
// carries (each frame header's CRC-8, each frame's CRC-16, the stream's
// MD5). It is how the tests know a file this package wrote decodes to
// exactly what went in, and how `foundry score export-audio -verify`
// checks one; it reads fixed-blocksize streams of up to 32 bits, which is
// every FLAC encoder's default output.

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
)

// FLACStream is a decoded FLAC file.
type FLACStream struct {
	Rate, Channels, Bits int
	// Samples are interleaved, as integers of Bits bits.
	Samples []int32
	// Tags are the VORBIS_COMMENT block's entries ("TITLE=…").
	Tags   []string
	Vendor string
}

type bitReader struct {
	b   []byte
	pos int // bits read
}

func (r *bitReader) read(n uint) (uint64, error) {
	var v uint64
	for n > 0 {
		if r.pos>>3 >= len(r.b) {
			return 0, fmt.Errorf("flac: truncated frame")
		}
		bit := r.b[r.pos>>3] >> (7 - uint(r.pos&7)) & 1
		v = v<<1 | uint64(bit)
		r.pos++
		n--
	}
	return v, nil
}

func (r *bitReader) signed(n uint) (int64, error) {
	v, err := r.read(n)
	if err != nil || n == 0 {
		return 0, err
	}
	if v&(1<<(n-1)) != 0 {
		return int64(v) - int64(1)<<n, nil
	}
	return int64(v), nil
}

func (r *bitReader) unary() (uint64, error) {
	var q uint64
	for {
		b, err := r.read(1)
		if err != nil {
			return 0, err
		}
		if b == 1 {
			return q, nil
		}
		q++
	}
}

// DecodeFLAC reads a FLAC file and checks it.
func DecodeFLAC(b []byte) (*FLACStream, error) {
	if !bytes.HasPrefix(b, []byte("fLaC")) {
		return nil, errFLAC
	}
	p := 4
	st := &FLACStream{}
	var total uint64
	var sum []byte
	var blockSize int
	for {
		if p+4 > len(b) {
			return nil, fmt.Errorf("flac: truncated metadata")
		}
		last := b[p]&0x80 != 0
		kind := b[p] & 0x7F
		n := int(b[p+1])<<16 | int(b[p+2])<<8 | int(b[p+3])
		p += 4
		if p+n > len(b) {
			return nil, fmt.Errorf("flac: truncated metadata block")
		}
		blk := b[p : p+n]
		switch kind {
		case 0:
			r := &bitReader{b: blk}
			minB, _ := r.read(16)
			maxB, _ := r.read(16)
			r.read(24)
			r.read(24)
			rate, _ := r.read(20)
			ch, _ := r.read(3)
			bps, _ := r.read(5)
			tot, err := r.read(36)
			if err != nil || len(blk) < 34 {
				return nil, fmt.Errorf("flac: short STREAMINFO")
			}
			if minB != maxB {
				return nil, fmt.Errorf("flac: variable block sizes are not read")
			}
			st.Rate, st.Channels, st.Bits, total, blockSize = int(rate), int(ch)+1, int(bps)+1, tot, int(maxB)
			sum = blk[18:34]
		case 4:
			if len(blk) < 8 {
				return nil, fmt.Errorf("flac: short VORBIS_COMMENT")
			}
			vl := int(binary.LittleEndian.Uint32(blk))
			if 4+vl+4 > len(blk) {
				return nil, fmt.Errorf("flac: bad VORBIS_COMMENT")
			}
			st.Vendor = string(blk[4 : 4+vl])
			q := 4 + vl
			cnt := int(binary.LittleEndian.Uint32(blk[q:]))
			q += 4
			for i := 0; i < cnt; i++ {
				if q+4 > len(blk) {
					return nil, fmt.Errorf("flac: bad VORBIS_COMMENT")
				}
				l := int(binary.LittleEndian.Uint32(blk[q:]))
				q += 4
				if q+l > len(blk) {
					return nil, fmt.Errorf("flac: bad VORBIS_COMMENT")
				}
				st.Tags = append(st.Tags, string(blk[q:q+l]))
				q += l
			}
		}
		p += n
		if last {
			break
		}
	}
	if st.Channels == 0 {
		return nil, fmt.Errorf("flac: no STREAMINFO")
	}
	st.Samples = make([]int32, 0, int(total)*st.Channels)
	for p < len(b) {
		used, err := st.frame(b[p:], blockSize)
		if err != nil {
			return nil, fmt.Errorf("flac: frame at byte %d: %w", p, err)
		}
		p += used
	}
	if uint64(len(st.Samples)) != total*uint64(st.Channels) {
		return nil, fmt.Errorf("flac: %d samples, STREAMINFO says %d", len(st.Samples)/st.Channels, total)
	}
	h := md5.New()
	bps := (st.Bits + 7) / 8
	buf := make([]byte, 0, 8192)
	for _, v := range st.Samples {
		for k := 0; k < bps; k++ {
			buf = append(buf, byte(v>>(8*k)))
		}
		if len(buf) >= 8192-bps {
			h.Write(buf)
			buf = buf[:0]
		}
	}
	h.Write(buf)
	if !bytes.Equal(h.Sum(nil), sum) && !bytes.Equal(sum, make([]byte, 16)) {
		return nil, fmt.Errorf("flac: the audio's MD5 is not the one STREAMINFO holds")
	}
	return st, nil
}

// frame decodes one frame and returns its length in bytes.
func (st *FLACStream) frame(b []byte, blockSize int) (int, error) {
	r := &bitReader{b: b}
	sync, _ := r.read(14)
	if sync != 0x3FFE {
		return 0, fmt.Errorf("no frame sync")
	}
	r.read(1)
	if strat, _ := r.read(1); strat != 0 {
		return 0, fmt.Errorf("variable block size")
	}
	bsCode, _ := r.read(4)
	rateCode, _ := r.read(4)
	chCode, _ := r.read(4)
	ssCode, _ := r.read(3)
	r.read(1)
	// The frame number, UTF-8 coded.
	first, _ := r.read(8)
	extra := 0
	for m := uint64(0x80); first&m != 0 && m > 1; m >>= 1 {
		extra++
	}
	if extra > 0 {
		extra--
	}
	for i := 0; i < extra; i++ {
		r.read(8)
	}
	n := blockSize
	switch {
	case bsCode == 1:
		n = 192
	case bsCode >= 2 && bsCode <= 5:
		n = 576 << (bsCode - 2)
	case bsCode == 6:
		v, _ := r.read(8)
		n = int(v) + 1
	case bsCode == 7:
		v, _ := r.read(16)
		n = int(v) + 1
	case bsCode >= 8:
		n = 256 << (bsCode - 8)
	}
	switch rateCode {
	case 12:
		r.read(8)
	case 13, 14:
		r.read(16)
	}
	bits := st.Bits
	switch ssCode {
	case 1:
		bits = 8
	case 2:
		bits = 12
	case 4:
		bits = 16
	case 5:
		bits = 20
	case 6:
		bits = 24
	case 7:
		bits = 32
	}
	hdrLen := r.pos / 8
	crc, _ := r.read(8)
	if byte(crc) != crc8(b[:hdrLen]) {
		return 0, fmt.Errorf("header CRC-8 does not match")
	}
	chans := st.Channels
	if chCode >= 8 && chCode <= 10 {
		chans = 2
	} else if int(chCode)+1 != st.Channels {
		return 0, fmt.Errorf("channel assignment %d for a %d-channel stream", chCode, st.Channels)
	}
	out := make([][]int64, chans)
	for c := 0; c < chans; c++ {
		bps := bits
		if chCode == 8 && c == 1 || chCode == 9 && c == 0 || chCode == 10 && c == 1 {
			bps++ // the side channel
		}
		x, err := decodeSubframe(r, n, bps)
		if err != nil {
			return 0, fmt.Errorf("channel %d: %w", c, err)
		}
		out[c] = x
	}
	if r.pos&7 != 0 {
		r.read(8 - uint(r.pos&7))
	}
	end := r.pos / 8
	if end+2 > len(b) {
		return 0, fmt.Errorf("truncated frame footer")
	}
	if uint16(b[end])<<8|uint16(b[end+1]) != crc16(b[:end]) {
		return 0, fmt.Errorf("frame CRC-16 does not match")
	}
	switch chCode {
	case 8: // left, side
		for i := 0; i < n; i++ {
			out[1][i] = out[0][i] - out[1][i]
		}
	case 9: // side, right
		for i := 0; i < n; i++ {
			out[0][i] = out[0][i] + out[1][i]
		}
	case 10: // mid, side
		for i := 0; i < n; i++ {
			m, s := out[0][i]<<1|out[1][i]&1, out[1][i]
			out[0][i], out[1][i] = (m+s)>>1, (m-s)>>1
		}
	}
	for i := 0; i < n; i++ {
		for c := 0; c < chans; c++ {
			st.Samples = append(st.Samples, int32(out[c][i]))
		}
	}
	return end + 2, nil
}

func decodeSubframe(r *bitReader, n, bps int) ([]int64, error) {
	if pad, _ := r.read(1); pad != 0 {
		return nil, fmt.Errorf("bad subframe padding")
	}
	kind, _ := r.read(6)
	wasted := 0
	if w, _ := r.read(1); w == 1 {
		k, err := r.unary()
		if err != nil {
			return nil, err
		}
		wasted = int(k) + 1
		bps -= wasted
	}
	x := make([]int64, n)
	switch {
	case kind == 0:
		v, err := r.signed(uint(bps))
		if err != nil {
			return nil, err
		}
		for i := range x {
			x[i] = v
		}
	case kind == 1:
		for i := range x {
			v, err := r.signed(uint(bps))
			if err != nil {
				return nil, err
			}
			x[i] = v
		}
	case kind >= 8 && kind <= 12:
		order := int(kind - 8)
		for i := 0; i < order; i++ {
			v, err := r.signed(uint(bps))
			if err != nil {
				return nil, err
			}
			x[i] = v
		}
		if err := readResidual(r, x, order); err != nil {
			return nil, err
		}
		for i := order; i < n; i++ {
			switch order {
			case 1:
				x[i] += x[i-1]
			case 2:
				x[i] += 2*x[i-1] - x[i-2]
			case 3:
				x[i] += 3*x[i-1] - 3*x[i-2] + x[i-3]
			case 4:
				x[i] += 4*x[i-1] - 6*x[i-2] + 4*x[i-3] - x[i-4]
			}
		}
	case kind >= 32:
		order := int(kind-32) + 1
		for i := 0; i < order; i++ {
			v, err := r.signed(uint(bps))
			if err != nil {
				return nil, err
			}
			x[i] = v
		}
		prec, _ := r.read(4)
		if prec == 15 {
			return nil, fmt.Errorf("bad LPC precision")
		}
		shift, _ := r.signed(5)
		if shift < 0 {
			return nil, fmt.Errorf("negative LPC shift")
		}
		coefs := make([]int64, order)
		for i := range coefs {
			c, err := r.signed(uint(prec) + 1)
			if err != nil {
				return nil, err
			}
			coefs[i] = c
		}
		if err := readResidual(r, x, order); err != nil {
			return nil, err
		}
		for i := order; i < n; i++ {
			var s int64
			for j, c := range coefs {
				s += c * x[i-j-1]
			}
			x[i] += s >> uint(shift)
		}
	default:
		return nil, fmt.Errorf("reserved subframe type %d", kind)
	}
	if wasted > 0 {
		for i := range x {
			x[i] <<= uint(wasted)
		}
	}
	return x, nil
}

// readResidual reads a Rice-coded residual into x[order:].
func readResidual(r *bitReader, x []int64, order int) error {
	method, _ := r.read(2)
	if method > 1 {
		return fmt.Errorf("reserved residual coding %d", method)
	}
	pbits := uint(4 + method)
	escape := uint64(1)<<pbits - 1
	po, _ := r.read(4)
	n := len(x)
	parts := 1 << po
	if n%parts != 0 || n>>po < order {
		return fmt.Errorf("bad partition order %d", po)
	}
	i := order
	for p := 0; p < parts; p++ {
		size := n >> po
		if p == 0 {
			size -= order
		}
		k, err := r.read(pbits)
		if err != nil {
			return err
		}
		if k == escape {
			nb, _ := r.read(5)
			for j := 0; j < size; j++ {
				v, err := r.signed(uint(nb))
				if err != nil {
					return err
				}
				x[i] = v
				i++
			}
			continue
		}
		for j := 0; j < size; j++ {
			q, err := r.unary()
			if err != nil {
				return err
			}
			low, err := r.read(uint(k))
			if err != nil {
				return err
			}
			u := q<<k | low
			x[i] = int64(u>>1) ^ -int64(u&1)
			i++
		}
	}
	return nil
}
