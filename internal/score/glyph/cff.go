package glyph

// cff.go — the font's outlines, read from its CFF table directly.
//
// golang.org/x/image/font/sfnt maps code points to glyphs well, but its
// Type 2 charstring interpreter has no flex or flex1 operators, and
// Bravura draws 97 glyphs with them — among them the 1, 4, 5 and 7 of
// every time signature, tuplet number and fingering. So the outlines are
// read here: the CFF header, its INDEXes, the top and private DICTs, the
// subroutines, and every charstring operator (Adobe Technical Note 5177).
// TestCFFMatchesSFNT checks every glyph sfnt can read comes out the same.

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// cffFont is a parsed CFF table: what interpreting a charstring needs.
type cffFont struct {
	charStrings [][]byte
	gsubrs      [][]byte
	subrs       [][]byte
}

// parseCFF finds and reads the CFF table of an OpenType file.
func parseCFF(otf []byte) (*cffFont, error) {
	if len(otf) < 12 {
		return nil, errors.New("cff: file too short")
	}
	n := int(binary.BigEndian.Uint16(otf[4:]))
	var table []byte
	for i := 0; i < n; i++ {
		rec := 12 + 16*i
		if rec+16 > len(otf) {
			return nil, errors.New("cff: bad table directory")
		}
		if string(otf[rec:rec+4]) == "CFF " {
			off := int(binary.BigEndian.Uint32(otf[rec+8:]))
			ln := int(binary.BigEndian.Uint32(otf[rec+12:]))
			if off < 0 || ln < 0 || off+ln > len(otf) {
				return nil, errors.New("cff: table out of range")
			}
			table = otf[off : off+ln]
		}
	}
	if table == nil {
		return nil, errors.New("cff: no CFF table (TrueType outlines are not supported)")
	}
	if len(table) < 4 {
		return nil, errors.New("cff: header too short")
	}
	at := int(table[2])               // header size
	_, at, err := cffIndex(table, at) // names
	if err != nil {
		return nil, err
	}
	tops, at, err := cffIndex(table, at)
	if err != nil || len(tops) == 0 {
		return nil, fmt.Errorf("cff: top DICT: %v", err)
	}
	_, at, err = cffIndex(table, at) // strings
	if err != nil {
		return nil, err
	}
	f := &cffFont{}
	if f.gsubrs, _, err = cffIndex(table, at); err != nil {
		return nil, err
	}
	top, err := cffDict(tops[0])
	if err != nil {
		return nil, err
	}
	if _, cid := top[0x0c1e]; cid {
		return nil, errors.New("cff: CID-keyed fonts are not supported")
	}
	cs, ok := top[17]
	if !ok || len(cs) != 1 {
		return nil, errors.New("cff: no CharStrings")
	}
	if f.charStrings, _, err = cffIndex(table, int(cs[0])); err != nil {
		return nil, err
	}
	if pv, ok := top[18]; ok && len(pv) == 2 {
		size, off := int(pv[0]), int(pv[1])
		if off < 0 || size < 0 || off+size > len(table) {
			return nil, errors.New("cff: private DICT out of range")
		}
		priv, err := cffDict(table[off : off+size])
		if err != nil {
			return nil, err
		}
		if s, ok := priv[19]; ok && len(s) == 1 {
			if f.subrs, _, err = cffIndex(table, off+int(s[0])); err != nil {
				return nil, err
			}
		}
	}
	return f, nil
}

// cffIndex reads an INDEX at an offset: its items and the offset after it.
func cffIndex(b []byte, at int) ([][]byte, int, error) {
	if at < 0 || at+2 > len(b) {
		return nil, 0, errors.New("cff: INDEX out of range")
	}
	count := int(binary.BigEndian.Uint16(b[at:]))
	if count == 0 {
		return nil, at + 2, nil
	}
	if at+3 > len(b) {
		return nil, 0, errors.New("cff: INDEX out of range")
	}
	osz := int(b[at+2])
	if osz < 1 || osz > 4 {
		return nil, 0, errors.New("cff: bad INDEX offset size")
	}
	offs := at + 3
	data := offs + (count+1)*osz - 1 // offsets are 1-based from here
	if data >= len(b)+1 || offs+(count+1)*osz > len(b) {
		return nil, 0, errors.New("cff: INDEX out of range")
	}
	read := func(i int) int {
		v := 0
		for k := 0; k < osz; k++ {
			v = v<<8 | int(b[offs+i*osz+k])
		}
		return v
	}
	items := make([][]byte, count)
	for i := 0; i < count; i++ {
		s, e := data+read(i), data+read(i+1)
		if s < 0 || e < s || e > len(b) {
			return nil, 0, errors.New("cff: INDEX item out of range")
		}
		items[i] = b[s:e]
	}
	return items, data + read(count), nil
}

// cffDict reads a DICT: operator (12 x as 0x0c00|x) → operands.
func cffDict(b []byte) (map[int][]float64, error) {
	out := map[int][]float64{}
	var ops []float64
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c <= 21:
			op := int(c)
			i++
			if c == 12 {
				if i >= len(b) {
					return nil, errors.New("cff: DICT ends in an escape")
				}
				op = 0x0c00 | int(b[i])
				i++
			}
			out[op] = ops
			ops = nil
		case c == 28:
			if i+3 > len(b) {
				return nil, errors.New("cff: DICT operand cut short")
			}
			ops = append(ops, float64(int16(binary.BigEndian.Uint16(b[i+1:]))))
			i += 3
		case c == 29:
			if i+5 > len(b) {
				return nil, errors.New("cff: DICT operand cut short")
			}
			ops = append(ops, float64(int32(binary.BigEndian.Uint32(b[i+1:]))))
			i += 5
		case c == 30:
			// A real: nibbles to 0xf. Only offsets and sizes are read
			// from these dicts, never reals, so its value is skipped.
			i++
			for i < len(b) {
				x := b[i]
				i++
				if x&0x0f == 0x0f || x>>4 == 0x0f {
					break
				}
			}
			ops = append(ops, 0)
		case c >= 32 && c <= 246:
			ops = append(ops, float64(int(c)-139))
			i++
		case c >= 247 && c <= 250:
			if i+2 > len(b) {
				return nil, errors.New("cff: DICT operand cut short")
			}
			ops = append(ops, float64((int(c)-247)*256+int(b[i+1])+108))
			i += 2
		case c >= 251 && c <= 254:
			if i+2 > len(b) {
				return nil, errors.New("cff: DICT operand cut short")
			}
			ops = append(ops, float64(-(int(c)-251)*256-int(b[i+1])-108))
			i += 2
		default:
			return nil, fmt.Errorf("cff: bad DICT byte %d", c)
		}
	}
	return out, nil
}

func subrBias(n int) int {
	switch {
	case n < 1240:
		return 107
	case n < 33900:
		return 1131
	}
	return 32768
}

// cffPath is an outline in font units, y up.
type cffPath struct {
	segs   []Segment
	x, y   float64
	sx, sy float64 // the open contour's start
	open   bool
	stack  []float64
	nStems int
	width  bool // the width operand has been seen (or ruled out)
	depth  int
	done   bool
}

// closeContour ends the open contour with a line back to its start, as
// sfnt does.
func (p *cffPath) closeContour() {
	if p.open && (p.x != p.sx || p.y != p.sy) {
		p.segs = append(p.segs, Segment{Op: LineTo, P: [3]Point{{X: p.sx, Y: p.sy}}})
	}
	p.open = false
}

func (p *cffPath) moveTo(dx, dy float64) {
	p.closeContour()
	p.x += dx
	p.y += dy
	p.segs = append(p.segs, Segment{Op: MoveTo, P: [3]Point{{X: p.x, Y: p.y}}})
	p.sx, p.sy = p.x, p.y
	p.open = true
}

func (p *cffPath) lineTo(dx, dy float64) {
	p.x += dx
	p.y += dy
	p.segs = append(p.segs, Segment{Op: LineTo, P: [3]Point{{X: p.x, Y: p.y}}})
}

func (p *cffPath) curveTo(dxa, dya, dxb, dyb, dxc, dyc float64) {
	a := Point{X: p.x + dxa, Y: p.y + dya}
	b := Point{X: a.X + dxb, Y: a.Y + dyb}
	c := Point{X: b.X + dxc, Y: b.Y + dyc}
	p.x, p.y = c.X, c.Y
	p.segs = append(p.segs, Segment{Op: CubeTo, P: [3]Point{a, b, c}})
}

// glyph interprets glyph i's charstring.
func (f *cffFont) glyph(i int) ([]Segment, error) {
	if i < 0 || i >= len(f.charStrings) {
		return nil, fmt.Errorf("cff: no glyph %d", i)
	}
	p := &cffPath{}
	if err := f.run(p, f.charStrings[i]); err != nil {
		return nil, err
	}
	p.closeContour()
	return p.segs, nil
}

// takeWidth drops the optional width operand the first stack-clearing
// operator may carry: present when the stack holds one more (or, for
// stems, an odd count) than the operator takes.
func (p *cffPath) takeWidth(odd bool, want int) {
	if p.width {
		return
	}
	p.width = true
	if (odd && len(p.stack)%2 == 1) || (!odd && len(p.stack) > want) {
		p.stack = p.stack[1:]
	}
}

func (f *cffFont) run(p *cffPath, cs []byte) error {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > 10 {
		return errors.New("cff: subroutines nested too deep")
	}
	need := func(n int) error {
		if len(p.stack) < n {
			return errors.New("cff: charstring stack underflow")
		}
		return nil
	}
	for i := 0; i < len(cs) && !p.done; {
		c := cs[i]
		switch {
		case c == 28:
			if i+3 > len(cs) {
				return errors.New("cff: charstring operand cut short")
			}
			p.stack = append(p.stack, float64(int16(binary.BigEndian.Uint16(cs[i+1:]))))
			i += 3
			continue
		case c >= 32 && c <= 246:
			p.stack = append(p.stack, float64(int(c)-139))
			i++
			continue
		case c >= 247 && c <= 250:
			if i+2 > len(cs) {
				return errors.New("cff: charstring operand cut short")
			}
			p.stack = append(p.stack, float64((int(c)-247)*256+int(cs[i+1])+108))
			i += 2
			continue
		case c >= 251 && c <= 254:
			if i+2 > len(cs) {
				return errors.New("cff: charstring operand cut short")
			}
			p.stack = append(p.stack, float64(-(int(c)-251)*256-int(cs[i+1])-108))
			i += 2
			continue
		case c == 255:
			if i+5 > len(cs) {
				return errors.New("cff: charstring operand cut short")
			}
			p.stack = append(p.stack, float64(int32(binary.BigEndian.Uint32(cs[i+1:])))/65536)
			i += 5
			continue
		}
		i++
		s := p.stack
		switch c {
		case 1, 3, 18, 23: // hstem vstem hstemhm vstemhm
			p.takeWidth(true, 0)
			p.nStems += len(p.stack) / 2
		case 19, 20: // hintmask cntrmask: an implied vstem, then the mask
			p.takeWidth(true, 0)
			p.nStems += len(p.stack) / 2
			i += (p.nStems + 7) / 8
		case 21: // rmoveto
			p.takeWidth(false, 2)
			if err := need(2); err != nil {
				return err
			}
			s = p.stack
			p.moveTo(s[0], s[1])
		case 22: // hmoveto
			p.takeWidth(false, 1)
			if err := need(1); err != nil {
				return err
			}
			p.moveTo(p.stack[0], 0)
		case 4: // vmoveto
			p.takeWidth(false, 1)
			if err := need(1); err != nil {
				return err
			}
			p.moveTo(0, p.stack[0])
		case 5: // rlineto
			for k := 0; k+1 < len(s); k += 2 {
				p.lineTo(s[k], s[k+1])
			}
		case 6, 7: // hlineto vlineto: alternating
			h := c == 6
			for _, v := range s {
				if h {
					p.lineTo(v, 0)
				} else {
					p.lineTo(0, v)
				}
				h = !h
			}
		case 8: // rrcurveto
			for k := 0; k+5 < len(s); k += 6 {
				p.curveTo(s[k], s[k+1], s[k+2], s[k+3], s[k+4], s[k+5])
			}
		case 24: // rcurveline
			k := 0
			for ; k+5 < len(s)-2; k += 6 {
				p.curveTo(s[k], s[k+1], s[k+2], s[k+3], s[k+4], s[k+5])
			}
			if k+1 < len(s) {
				p.lineTo(s[k], s[k+1])
			}
		case 25: // rlinecurve
			k := 0
			for ; k+1 < len(s)-6; k += 2 {
				p.lineTo(s[k], s[k+1])
			}
			if k+5 < len(s) {
				p.curveTo(s[k], s[k+1], s[k+2], s[k+3], s[k+4], s[k+5])
			}
		case 26: // vvcurveto
			k, dx1 := 0, 0.0
			if len(s)%2 == 1 {
				dx1, k = s[0], 1
			}
			for ; k+3 < len(s); k += 4 {
				p.curveTo(dx1, s[k], s[k+1], s[k+2], 0, s[k+3])
				dx1 = 0
			}
		case 27: // hhcurveto
			k, dy1 := 0, 0.0
			if len(s)%2 == 1 {
				dy1, k = s[0], 1
			}
			for ; k+3 < len(s); k += 4 {
				p.curveTo(s[k], dy1, s[k+1], s[k+2], s[k+3], 0)
				dy1 = 0
			}
		case 30, 31: // vhcurveto hvcurveto: alternating starts
			h := c == 31
			for k := 0; k+3 < len(s); k += 4 {
				extra := 0.0
				if len(s)-(k+4) == 1 {
					extra = s[k+4]
				}
				if h {
					p.curveTo(s[k], 0, s[k+1], s[k+2], extra, s[k+3])
				} else {
					p.curveTo(0, s[k], s[k+1], s[k+2], s[k+3], extra)
				}
				h = !h
			}
		case 10, 29: // callsubr callgsubr
			if err := need(1); err != nil {
				return err
			}
			subs := f.subrs
			if c == 29 {
				subs = f.gsubrs
			}
			n := int(p.stack[len(p.stack)-1]) + subrBias(len(subs))
			p.stack = p.stack[:len(p.stack)-1]
			if n < 0 || n >= len(subs) {
				return fmt.Errorf("cff: no subroutine %d", n)
			}
			if err := f.run(p, subs[n]); err != nil {
				return err
			}
			continue // the subroutine's operands stay on the stack
		case 11: // return
			return nil
		case 14: // endchar
			p.takeWidth(false, 0)
			p.done = true
		case 12:
			if i >= len(cs) {
				return errors.New("cff: charstring ends in an escape")
			}
			e := cs[i]
			i++
			switch e {
			case 35: // flex
				if err := need(12); err != nil {
					return err
				}
				p.curveTo(s[0], s[1], s[2], s[3], s[4], s[5])
				p.curveTo(s[6], s[7], s[8], s[9], s[10], s[11])
			case 34: // hflex
				if err := need(7); err != nil {
					return err
				}
				p.curveTo(s[0], 0, s[1], s[2], s[3], 0)
				p.curveTo(s[4], 0, s[5], -s[2], s[6], 0)
			case 36: // hflex1
				if err := need(9); err != nil {
					return err
				}
				p.curveTo(s[0], s[1], s[2], s[3], s[4], 0)
				p.curveTo(s[5], 0, s[6], s[7], s[8], -(s[1] + s[3] + s[7]))
			case 37: // flex1
				if err := need(11); err != nil {
					return err
				}
				dx := s[0] + s[2] + s[4] + s[6] + s[8]
				dy := s[1] + s[3] + s[5] + s[7] + s[9]
				p.curveTo(s[0], s[1], s[2], s[3], s[4], s[5])
				if abs(dx) > abs(dy) {
					p.curveTo(s[6], s[7], s[8], s[9], s[10], -dy)
				} else {
					p.curveTo(s[6], s[7], s[8], s[9], -dx, s[10])
				}
			default:
				return fmt.Errorf("cff: charstring operator 12 %d is not supported", e)
			}
		default:
			return fmt.Errorf("cff: charstring operator %d is not supported", c)
		}
		p.stack = p.stack[:0]
	}
	return nil
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
