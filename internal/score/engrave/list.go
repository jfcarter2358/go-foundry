package engrave

// list.go — the display list: what the engraver produces and every backend
// draws.
//
// A List is flat: glyphs at a point and size, straight lines with a
// thickness, filled paths (beams, ties, brackets' hooks), and text with a
// font and style. Every coordinate is in staff spaces, y down, from the top
// left of the layout. A backend scales by its pixels (or points) per space
// and draws the items in order: internal/score/engrave/raster to an image,
// the editor through imgui (internal/score/scoredraw), the PDF writer in
// phase 5.
//
// Items carry a Ref saying what in the score they draw, so the editor can
// colour a selection and hit-test a click without engraving twice, and a
// Role saying what kind of thing they are (a staff line is not selectable).
// Beside the items the List keeps the geometry an editor needs to map the
// pointer back to music: where each staff is and what its clef makes of a
// line or space (StaffBox), where each bar is (BarBox), and where each
// event's onset falls (EventBox, Columns).

import (
	"math"

	"foundry/internal/score/glyph"
)

// Point and Rect are glyph's: staff spaces, y down.
type (
	Point = glyph.Point
	Rect  = glyph.Rect
)

// ItemKind is what an item draws.
type ItemKind uint8

const (
	// ItemGlyph is a SMuFL glyph with its origin at P.
	ItemGlyph ItemKind = iota
	// ItemLine is a straight line from P to Q, Width thick, square ends
	// flush with P and Q.
	ItemLine
	// ItemPath is a filled closed path (nonzero winding).
	ItemPath
	// ItemText is a run of text with its baseline's left end at P (see
	// Align).
	ItemText
)

// Role is what kind of thing an item is, for colouring and hit tests.
type Role uint8

const (
	RoleStaff Role = iota // staff lines, ledger lines
	RoleBarline
	RoleClef
	RoleKey
	RoleTime
	RoleNotehead
	RoleAccidental
	RoleStem
	RoleFlag
	RoleBeam
	RoleDot
	RoleRest
	RoleTie
	RoleTuplet
	RoleBrace
	RoleBarNumber
	RoleArticulation
	RoleDynamic
	RoleText
	RoleLedger
	// RoleSlur, RoleHairpin and RoleLine (glissando, ottava and pedal
	// lines, dashes) are drawn across notes; RoleOrnament and RoleFermata
	// sit over one; the rest stand over the system: tempo marks, rehearsal
	// marks, endings (voltas) and the navigation marks (segno, coda, D.S.).
	RoleSlur
	RoleHairpin
	RoleLine
	RoleOrnament
	RoleFermata
	RoleTempo
	RoleRehearsal
	RoleEnding
	RoleJump
)

// Ref says which part of the score an item draws. Indices are 0-based
// except Bar (1-based, as the score counts); -1 is "not this level": a
// staff line has Line and Staff but no Voice. Note indexes the event's
// Notes in written order.
type Ref struct {
	Line, Staff, Voice int
	Bar                int
	Event, Note        int
}

// NoRef is an item that draws nothing in particular (a brace, the system's
// first barline).
var NoRef = Ref{-1, -1, -1, 0, -1, -1}

// Item is one thing to draw.
type Item struct {
	Kind ItemKind
	Role Role
	Ref  Ref

	// P is the glyph's origin, the line's start or the text's anchor.
	P Point
	// Q is the line's end.
	Q Point
	// Width is a line's thickness.
	Width float64

	// Glyph is the SMuFL name and Rune its code point.
	Glyph string
	Rune  rune
	// Size scales a glyph or text: 1 for normal glyphs (an em of four
	// spaces), less for grace notes. ScaleY, when not zero, stretches a
	// glyph vertically by that much more (a brace over a grand staff).
	Size   float64
	ScaleY float64

	// Path is a filled outline (ItemPath), absolute.
	Path []glyph.Segment

	// Text, its font and how it hangs from P.
	Text  string
	Font  TextFont
	Align Align

	// Faint marks something drawn for orientation rather than written: the
	// bar rest an empty bar shows. An editor may draw it lighter.
	Faint bool
}

// TextFont is the face a text item asks for; a backend maps it onto
// whatever serif it has.
type TextFont uint8

const (
	TextPlain TextFont = iota
	TextItalic
	TextBold
	TextBoldItalic
)

// Align is where a text item's anchor sits on its baseline.
type Align uint8

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
)

// StaffBox is one staff on the layout.
type StaffBox struct {
	Line, Staff int
	// Top is the top line's y; the staff runs to Top+4 (Lines-1 spaces).
	Top float64
	// Clef is the staff's clef and Bottom the diatonic number (score.Pitch
	// .Diatonic) its bottom line stands for; what a click Y means is
	// Bottom + round((Top+4-Y)*2).
	Clef   string
	Bottom int
	// Fifths is the key signature shown at the layout's start (written,
	// after transposition).
	Fifths int
	// Transposed reports a staff shown at written pitch: what is clicked
	// is written pitch and must be moved to concert before it is stored.
	Transposed bool
	// Extent is the staff's ink, top to bottom.
	Extent Rect
	// Changes are the clef changes inside the layout, left to right: from
	// X on the staff reads in Clef, its bottom line Bottom.
	Changes []ClefSpan
}

// ClefSpan is where a staff's clef changes on the layout.
type ClefSpan struct {
	X      float64
	Clef   string
	Bottom int
}

// Diatonic is what a y means on the staff, in its opening clef.
func (b StaffBox) Diatonic(y float64) int {
	return b.Bottom + int(math.Round((b.Top+4-y)*2))
}

// Y is where a diatonic position sits, in the opening clef.
func (b StaffBox) Y(diatonic int) float64 { return b.Top + 4 - float64(diatonic-b.Bottom)/2 }

// ClefAt is the clef and bottom line in force at an x of the layout.
func (b StaffBox) ClefAt(x float64) (string, int) {
	clef, bottom := b.Clef, b.Bottom
	for _, c := range b.Changes {
		if x >= c.X {
			clef, bottom = c.Clef, c.Bottom
		}
	}
	return clef, bottom
}

// DiatonicAt is what a y means on the staff at an x (after any clef change
// before it).
func (b StaffBox) DiatonicAt(x, y float64) int {
	_, bottom := b.ClefAt(x)
	return bottom + int(math.Round((b.Top+4-y)*2))
}

// YAt is where a diatonic position sits at an x.
func (b StaffBox) YAt(x float64, diatonic int) float64 {
	_, bottom := b.ClefAt(x)
	return b.Top + 4 - float64(diatonic-bottom)/2
}

// LaneBox is the room left under a line (Options.Below): the editor draws
// the line's dynamics lane there.
type LaneBox struct {
	Line        int
	Top, Height float64
}

// BarBox is one bar on the layout: from its opening barline X0 to its
// closing one X1. Content is where the music starts after any clef, key or
// time signature at its head.
type BarBox struct {
	Bar     int
	X0, X1  float64
	Content float64
	// Columns are the bar's onsets, in time order, with their x (the left
	// edge of a normal notehead at that onset); the last is the bar's end,
	// at X1. Interpolating between them maps any offset in the bar to x.
	Columns []Column
}

// Column is one onset of a bar.
type Column struct {
	// Offset is the time from the bar's start, in whole notes, as a float
	// (exact times are in the score; this is for mapping).
	Offset float64
	X      float64
}

// XAt maps an offset in the bar (whole notes) to x, linearly between
// columns.
func (b BarBox) XAt(offset float64) float64 {
	cs := b.Columns
	if len(cs) == 0 {
		return b.Content
	}
	if offset <= cs[0].Offset {
		return cs[0].X
	}
	for i := 1; i < len(cs); i++ {
		if offset <= cs[i].Offset {
			a, c := cs[i-1], cs[i]
			if c.Offset <= a.Offset {
				return c.X
			}
			return a.X + (c.X-a.X)*(offset-a.Offset)/(c.Offset-a.Offset)
		}
	}
	return cs[len(cs)-1].X
}

// OffsetAt maps an x in the bar back to an offset (whole notes).
func (b BarBox) OffsetAt(x float64) float64 {
	cs := b.Columns
	if len(cs) == 0 {
		return 0
	}
	if x <= cs[0].X {
		return cs[0].Offset
	}
	for i := 1; i < len(cs); i++ {
		if x <= cs[i].X {
			a, c := cs[i-1], cs[i]
			if c.X <= a.X {
				return c.Offset
			}
			return a.Offset + (c.Offset-a.Offset)*(x-a.X)/(c.X-a.X)
		}
	}
	return cs[len(cs)-1].Offset
}

// EventBox is one engraved event: where it is and what it covers.
type EventBox struct {
	Ref Ref
	// X is the onset's x (a normal notehead's left edge), Box the event's
	// ink (noteheads, accidentals, stem, flag, dots; for a rest the rest).
	X   float64
	Box Rect
	// Offset is the event's start in its bar, in whole notes.
	Offset float64
}

// List is an engraved layout.
type List struct {
	Items []Item
	// Width and Height are the layout's size: everything is inside
	// (0,0)-(Width,Height).
	Width, Height float64

	Staves []StaffBox
	Bars   []BarBox
	Events []EventBox
	Lanes  []LaneBox
}

// Bounds is the ink of every item (glyph boxes from the font's metadata).
func (l *List) Bounds() Rect {
	f := glyph.Bravura()
	var r Rect
	for i := range l.Items {
		r = r.Union(itemBounds(f, &l.Items[i]))
	}
	return r
}

// Translate moves every item and box by d.
func (l *List) Translate(d Point) {
	for i := range l.Items {
		it := &l.Items[i]
		it.P, it.Q = it.P.Add(d), it.Q.Add(d)
		for k := range it.Path {
			for j := range it.Path[k].P {
				it.Path[k].P[j] = it.Path[k].P[j].Add(d)
			}
		}
	}
	for i := range l.Staves {
		l.Staves[i].Top += d.Y
		l.Staves[i].Extent = l.Staves[i].Extent.Offset(d)
		for k := range l.Staves[i].Changes {
			l.Staves[i].Changes[k].X += d.X
		}
	}
	for i := range l.Lanes {
		l.Lanes[i].Top += d.Y
	}
	for i := range l.Bars {
		b := &l.Bars[i]
		b.X0, b.X1, b.Content = b.X0+d.X, b.X1+d.X, b.Content+d.X
		for k := range b.Columns {
			b.Columns[k].X += d.X
		}
	}
	for i := range l.Events {
		l.Events[i].X += d.X
		l.Events[i].Box = l.Events[i].Box.Offset(d)
	}
}

// BarAt is the bar box under an x, or nil.
func (l *List) BarAt(x float64) *BarBox {
	for i := range l.Bars {
		if x >= l.Bars[i].X0 && x < l.Bars[i].X1 {
			return &l.Bars[i]
		}
	}
	return nil
}

// Bar finds a bar's box by number, or nil.
func (l *List) Bar(bar int) *BarBox {
	for i := range l.Bars {
		if l.Bars[i].Bar == bar {
			return &l.Bars[i]
		}
	}
	return nil
}

// StaffAt is the staff nearest a y, or nil for a list with none.
func (l *List) StaffAt(y float64) *StaffBox {
	var best *StaffBox
	bd := math.Inf(1)
	for i := range l.Staves {
		s := &l.Staves[i]
		d := 0.0
		switch {
		case y < s.Top:
			d = s.Top - y
		case y > s.Top+4:
			d = y - s.Top - 4
		}
		if d < bd {
			best, bd = s, d
		}
	}
	return best
}

// ItemBounds is an item's ink (a glyph's box from the font's metadata, a
// text's estimated).
func ItemBounds(it *Item) Rect { return itemBounds(glyph.Bravura(), it) }

// itemBounds is an item's ink.
func itemBounds(f *glyph.Font, it *Item) Rect {
	switch it.Kind {
	case ItemGlyph:
		b, _ := f.Box(it.Glyph)
		sy := it.Size
		if it.ScaleY != 0 {
			sy *= it.ScaleY
		}
		return Rect{Min: Point{X: it.P.X + b.Min.X*it.Size, Y: it.P.Y + b.Min.Y*sy}, Max: Point{X: it.P.X + b.Max.X*it.Size, Y: it.P.Y + b.Max.Y*sy}}
	case ItemLine:
		h := it.Width / 2
		return lineQuadBounds(it.P, it.Q, h)
	case ItemPath:
		var r Rect
		first := true
		for _, s := range it.Path {
			n := map[glyph.SegOp]int{glyph.MoveTo: 1, glyph.LineTo: 1, glyph.QuadTo: 2, glyph.CubeTo: 3}[s.Op]
			for k := 0; k < n; k++ {
				p := s.P[k]
				if first {
					r, first = Rect{Min: p, Max: p}, false
					continue
				}
				r.Min.X, r.Min.Y = math.Min(r.Min.X, p.X), math.Min(r.Min.Y, p.Y)
				r.Max.X, r.Max.Y = math.Max(r.Max.X, p.X), math.Max(r.Max.Y, p.Y)
			}
		}
		return r
	case ItemText:
		w := TextWidthFont(it.Text, it.Font, it.Size)
		x := it.P.X
		switch it.Align {
		case AlignCenter:
			x -= w / 2
		case AlignRight:
			x -= w
		}
		return Rect{Min: Point{X: x, Y: it.P.Y - 0.75*it.Size}, Max: Point{X: x + w, Y: it.P.Y + 0.25*it.Size}}
	}
	return Rect{}
}

// lineQuadBounds is the box of a line h either side of P→Q.
func lineQuadBounds(p, q Point, h float64) Rect {
	dx, dy := q.X-p.X, q.Y-p.Y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return Rect{Min: Point{X: p.X - h, Y: p.Y - h}, Max: Point{X: p.X + h, Y: p.Y + h}}
	}
	nx, ny := -dy/l*h, dx/l*h
	r := Rect{Min: Point{X: p.X + nx, Y: p.Y + ny}, Max: Point{X: p.X + nx, Y: p.Y + ny}}
	for _, c := range []Point{{X: p.X - nx, Y: p.Y - ny}, {X: q.X + nx, Y: q.Y + ny}, {X: q.X - nx, Y: q.Y - ny}} {
		r.Min.X, r.Min.Y = math.Min(r.Min.X, c.X), math.Min(r.Min.Y, c.Y)
		r.Max.X, r.Max.Y = math.Max(r.Max.X, c.X), math.Max(r.Max.Y, c.Y)
	}
	return r
}
