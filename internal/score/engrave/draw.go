package engrave

// draw.go — turning the placed music into items.
//
// Each staff draws into its own list with the top line at y = 0; finish
// stacks the staves and moves each list down to its staff. What spans
// staves — the system's opening barline, barlines through a grand staff,
// braces, bar numbers — is drawn after the stacking (drawSpanning).

import (
	"math"
	"strconv"

	"foundry/internal/score"
	"foundry/internal/score/glyph"
)

// draw draws every staff.
func (e *engraver) draw() {
	endX := e.bars[len(e.bars)-1].x1 + barlineWidth(e.d, e.barlineBefore(len(e.bars)))
	t := e.d.StaffLineThickness
	for _, sc := range e.staves {
		ref := Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: 0, Event: -1, Note: -1}
		for i := 0; i < 5; i++ {
			y := float64(i)
			e.addStaff(sc, Item{Kind: ItemLine, Role: RoleStaff, Ref: ref, P: Point{X: 0, Y: y}, Q: Point{X: endX, Y: y}, Width: t})
		}
		e.drawStart(sc)
		for bi, bc := range e.bars {
			sb := sc.bars[bi]
			if bi > 0 {
				e.drawBarPrefix(sc, bi)
			}
			bref := ref
			bref.Bar = bc.bar
			for _, it := range barlineItems(e.d, e.barlineBefore(bi+1), bc.x1, -t/2, 4+t/2, bref) {
				e.addStaff(sc, it)
			}
			for _, v := range sb.voices {
				for _, x := range v {
					e.drawEvent(sc, x)
				}
			}
			for _, g := range sb.beams {
				e.drawBeam(sc, g)
			}
			if sb.empty {
				e.drawImpliedRest(sc, sb)
			}
		}
		e.drawTies(sc)
		for _, sb := range sc.bars {
			e.drawTuplets(sc, sb)
		}
	}
}

// addStaff adds an item to a staff.
func (e *engraver) addStaff(sc *staffCtx, it Item) { sc.items = append(sc.items, it) }

// add adds an event's item to its staff and grows the event's box.
func (e *engraver) add(sc *staffCtx, x *ev, it Item) {
	sc.items = append(sc.items, it)
	x.box = x.box.Union(itemBounds(e.f, &it))
}

// glyphItem is a glyph by name at a point.
func (e *engraver) glyphItem(role Role, ref Ref, name string, x, y, size float64) Item {
	r, _ := e.f.Codepoint(name)
	return Item{Kind: ItemGlyph, Role: role, Ref: ref, Glyph: name, Rune: r, P: Point{X: x, Y: y}, Size: size}
}

// polyItem is a filled polygon.
func polyItem(role Role, ref Ref, pts ...Point) Item {
	p := make([]glyph.Segment, 0, len(pts))
	for i, q := range pts {
		op := glyph.LineTo
		if i == 0 {
			op = glyph.MoveTo
		}
		p = append(p, glyph.Segment{Op: op, P: [3]Point{q}})
	}
	return Item{Kind: ItemPath, Role: role, Ref: ref, Path: p}
}

// drawEvent draws one event.
func (e *engraver) drawEvent(sc *staffCtx, x *ev) {
	switch {
	case x.space || x.col == nil:
		return
	case x.barRest:
		e.drawBarRest(sc, x)
		return
	case x.rest:
		g := restGlyph(x.base)
		rx := x.x + x.restX
		e.add(sc, x, e.glyphItem(RoleRest, x.ref, g, rx, x.restY, 1))
		dx := rx + x.restDotX
		for k := 0; k < x.dots; k++ {
			e.add(sc, x, e.glyphItem(RoleDot, x.ref, "augmentationDot", dx+float64(k)*dotSpacing, x.restDotY, 1))
		}
		return
	}
	s := x.size
	hg := headGlyph(x.base)
	o := e.headOrigin(x.base) * s
	for _, n := range x.notes {
		ref := x.ref
		ref.Note = n.idx
		e.add(sc, x, e.glyphItem(RoleNotehead, ref, hg, x.x+n.dx-o, n.y, s))
		if n.acc {
			base := x.col.x
			if x.grace {
				base = x.x
			}
			e.drawAcc(sc, x, n, base+n.accX, ref)
		}
	}
	e.drawLedgers(sc, x)
	for k := 0; k < x.dots; k++ {
		for _, row := range x.dotRows {
			e.add(sc, x, e.glyphItem(RoleDot, x.ref, "augmentationDot", x.x+x.dotX+float64(k)*dotSpacing*s, 4-float64(row)/2, s))
		}
	}
	if x.hasStem && x.beam == nil {
		e.drawStem(sc, x)
	}
}

// drawAcc draws a note's accidental with its left edge at ax.
func (e *engraver) drawAcc(sc *staffCtx, x *ev, n *nt, ax float64, ref Ref) {
	s := x.size
	g := accGlyph(int(n.p.Alter))
	if n.parens {
		pl, _ := e.f.Box("accidentalParensLeft")
		e.add(sc, x, e.glyphItem(RoleAccidental, ref, "accidentalParensLeft", ax-pl.Min.X*s, n.y, s))
		ax += pl.W() * s
	}
	b, _ := e.f.Box(g)
	e.add(sc, x, e.glyphItem(RoleAccidental, ref, g, ax-b.Min.X*s, n.y, s))
	if n.parens {
		pr, _ := e.f.Box("accidentalParensRight")
		e.add(sc, x, e.glyphItem(RoleAccidental, ref, "accidentalParensRight", ax+b.W()*s-pr.Min.X*s, n.y, s))
	}
}

// drawLedgers draws a chord's ledger lines: one at every line between the
// staff and its furthest note, as wide as the heads that need it.
func (e *engraver) drawLedgers(sc *staffCtx, x *ev) {
	if len(x.notes) == 0 {
		return
	}
	s := x.size
	ext := e.d.LegerLineExtension * s
	th := e.d.LegerLineThickness * s
	lo, hi := lowHigh(x)
	ledger := func(L int, needs func(p int) bool) {
		x0, x1 := math.Inf(1), math.Inf(-1)
		left := ext
		for _, n := range x.notes {
			if needs(n.pos) {
				x0 = math.Min(x0, x.x+n.dx)
				x1 = math.Max(x1, x.x+n.dx+x.headW)
			}
			// An accidental beside the line keeps the line short of it.
			if n.acc && absInt(n.pos-L) <= 1 {
				left = math.Min(left, accGap*s*0.6)
			}
		}
		y := 4 - float64(L)/2
		e.add(sc, x, Item{Kind: ItemLine, Role: RoleLedger, Ref: x.ref, P: Point{X: x0 - left, Y: y}, Q: Point{X: x1 + ext, Y: y}, Width: th})
	}
	for L := -2; L >= lo; L -= 2 {
		ledger(L, func(p int) bool { return p <= L })
	}
	for L := 10; L <= hi; L += 2 {
		ledger(L, func(p int) bool { return p >= L })
	}
}

// barRestGlyph is a bar rest's glyph and its y: a whole rest, a breve
// rest in a bar of two whole notes or more.
func barRestGlyph(bc *barCtx) (string, float64) {
	if !bc.length.Less(score.Whole(2)) {
		return "restDoubleWhole", 2
	}
	return "restWhole", 1
}

// drawBarRest centres a bar rest in its bar.
func (e *engraver) drawBarRest(sc *staffCtx, x *ev) {
	bc := x.sb.bar
	g, y := barRestGlyph(bc)
	if x.sb.forced {
		if x.voice%2 == 0 {
			y -= 1
		} else {
			y += 2
		}
	}
	w := e.f.Width(g)
	rx := (bc.content+bc.x1)/2 - w/2
	x.x = rx
	e.add(sc, x, e.glyphItem(RoleRest, x.ref, g, rx, y, 1))
}

// drawImpliedRest shows an empty bar as a faint bar rest.
func (e *engraver) drawImpliedRest(sc *staffCtx, sb *staffBar) {
	bc := sb.bar
	g, y := barRestGlyph(bc)
	w := e.f.Width(g)
	it := e.glyphItem(RoleRest, Ref{Line: sc.line, Staff: sc.staff, Voice: 0, Bar: bc.bar, Event: -1, Note: -1}, g, (bc.content+bc.x1)/2-w/2, y, 1)
	it.Faint = true
	e.addStaff(sc, it)
}

// drawSpanning draws what crosses staves, once they are stacked.
func (e *engraver) drawSpanning() {
	t := e.d.StaffLineThickness
	first, last := e.staves[0], e.staves[len(e.staves)-1]
	if len(e.staves) > 1 {
		for _, it := range barlineItems(e.d, barSingle, 0, first.top-t/2, last.top+4+t/2, NoRef) {
			e.items = append(e.items, it)
		}
	}
	for i, sc := range e.staves {
		if !sc.first || sc.nStaves < 2 {
			continue
		}
		j := i
		for j+1 < len(e.staves) && e.staves[j+1].line == sc.line && !e.staves[j+1].first {
			j++
		}
		if j == i {
			continue
		}
		top, bot := sc.top, e.staves[j].top+4
		// The brace, stretched to the grand staff.
		h := bot - top
		size := math.Min(math.Max(h/4*0.55, 1), 2.4)
		b, _ := e.f.Box("brace")
		it := e.glyphItem(RoleBrace, Ref{Line: sc.line, Staff: -1, Voice: -1, Event: -1, Note: -1}, "brace", -braceGap-b.W()*size, bot, size)
		it.ScaleY = h / (b.H() * size)
		e.items = append(e.items, it)
		// Barlines through the gaps.
		for bi, bc := range e.bars {
			for k := i; k < j; k++ {
				for _, it := range barlineItems(e.d, e.barlineBefore(bi+1), bc.x1, e.staves[k].top+4, e.staves[k+1].top, Ref{Line: sc.line, Staff: -1, Voice: -1, Bar: bc.bar, Event: -1, Note: -1}) {
					e.items = append(e.items, it)
				}
			}
		}
	}
	e.drawBarNumbers()
}

// drawBarNumbers numbers bars above the top staff.
func (e *engraver) drawBarNumbers() {
	if e.o.BarNumbers < 0 {
		return
	}
	top := e.staves[0]
	y := top.top - math.Max(1.4, -top.ext.Min.Y+0.5)
	for i, bc := range e.bars {
		show := i == 0 && bc.bar > 1 || i > 0 && e.o.BarNumbers > 0 && bc.bar%e.o.BarNumbers == 0
		if !show {
			continue
		}
		x := bc.x0
		e.items = append(e.items, Item{Kind: ItemText, Role: RoleBarNumber, Ref: Ref{Line: -1, Staff: -1, Voice: -1, Bar: bc.bar, Event: -1, Note: -1},
			P: Point{X: x, Y: y}, Text: strconv.Itoa(bc.bar), Font: TextItalic, Size: 1.6})
	}
}
