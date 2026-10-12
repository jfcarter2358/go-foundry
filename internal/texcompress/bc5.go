package texcompress

// bc5.go — BC5 (RGTC2) for normal maps: two BC4 channels side by side.
//
// A tangent-space normal map has three channels but only two degrees of
// freedom — the normal is unit length and points out of the surface — so BC5
// keeps x (red) and y (green) at full BC4 precision, eight interpolated
// levels between two 8-bit endpoints per 4x4 block for each, and drops z. The
// shader rebuilds it as z = sqrt(max(0, 1 - x² - y²)) when the map is BC5
// (fragment.glsl, hasNormalMap == 2; gfx.BindNormalMap sets the 2). That is
// far better than packing a normal into BC1/BC7's correlated colour
// endpoints, which bends normals visibly on curved relief.
//
// A BC4 block is two endpoint bytes and sixteen 3-bit codes, least
// significant bit first. With r0 > r1 the codes mean r0, r1 and six evenly
// spaced values between (from r0's end); r0 <= r1 selects a six-value ramp
// plus explicit 0 and 255, which this encoder uses only for solid blocks,
// where every code is 0 and so means r0.
//
// The fit is the block's range, then a small search insetting each end by up
// to two steps — the extremes are often outliers whose exact value costs the
// other fourteen texels more than it saves — keeping the lowest squared error.

// bc4Palette is the eight values a BC4 block's codes decode to.
func bc4Palette(r0, r1 int32) (p [8]int32) {
	p[0], p[1] = r0, r1
	if r0 > r1 {
		for k := int32(1); k <= 6; k++ {
			p[k+1] = ((7-k)*r0 + k*r1 + 3) / 7
		}
		return
	}
	for k := int32(1); k <= 4; k++ {
		p[k+1] = ((5-k)*r0 + k*r1 + 2) / 5
	}
	p[6], p[7] = 0, 255
	return
}

// encodeBC4Block encodes 16 single-channel values into out (8 bytes).
func encodeBC4Block(v *[16]int32, out []byte) {
	lo, hi := int32(255), int32(0)
	for _, x := range v {
		lo, hi = min(lo, x), max(hi, x)
	}
	if lo == hi {
		out[0], out[1] = byte(hi), byte(hi)
		for i := 2; i < 8; i++ {
			out[i] = 0
		}
		return
	}
	var bestCodes [16]uint8
	bestR0, bestR1, bestErr := hi, lo, int64(1<<62)
	for di := int32(0); di <= 2; di++ {
		for dj := int32(0); dj <= 2; dj++ {
			r0, r1 := hi-di, lo+dj
			if r0 <= r1 {
				continue
			}
			pal := bc4Palette(r0, r1)
			var codes [16]uint8
			var err int64
			for i, x := range v {
				bk, be := 0, int32(1<<30)
				for k, p := range pal {
					d := x - p
					if d < 0 {
						d = -d
					}
					if d < be {
						bk, be = k, d
					}
				}
				codes[i] = uint8(bk)
				err += int64(be) * int64(be)
			}
			if err < bestErr {
				bestErr, bestR0, bestR1, bestCodes = err, r0, r1, codes
			}
		}
	}
	out[0], out[1] = byte(bestR0), byte(bestR1)
	var bits uint64
	for i, c := range bestCodes {
		bits |= uint64(c) << (3 * i)
	}
	for i := 0; i < 6; i++ {
		out[2+i] = byte(bits >> (8 * i))
	}
}

// decodeBC4Block decodes 8 bytes into 16 values.
func decodeBC4Block(b []byte, out *[16]uint8) {
	pal := bc4Palette(int32(b[0]), int32(b[1]))
	var bits uint64
	for i := 0; i < 6; i++ {
		bits |= uint64(b[2+i]) << (8 * i)
	}
	for i := 0; i < 16; i++ {
		out[i] = uint8(pal[(bits>>(3*i))&7])
	}
}

// encodeBC5Block encodes the red and green of 16 RGBA texels into out (16
// bytes): red's BC4 block, then green's.
func encodeBC5Block(px *[16][4]int32, out []byte) {
	var r, g [16]int32
	for i := range px {
		r[i], g[i] = px[i][0], px[i][1]
	}
	encodeBC4Block(&r, out[0:8])
	encodeBC4Block(&g, out[8:16])
}

// DecodeBC5Block decodes one block into 16 (red, green) pairs, row-major.
func DecodeBC5Block(b []byte, out *[16][2]uint8) {
	var r, g [16]uint8
	decodeBC4Block(b[0:8], &r)
	decodeBC4Block(b[8:16], &g)
	for i := 0; i < 16; i++ {
		out[i] = [2]uint8{r[i], g[i]}
	}
}
