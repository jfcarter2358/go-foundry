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
	endX := e.layoutEndX()
	t := e.d.StaffLineThickness
	for _, sc := range e.staves {
		if sc.tab != nil {
			e.drawTabStaff(sc)
			continue
		}
		ref := Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: 0, Event: -1, Note: -1}
		for _, y := range sc.lineYs() {
			e.addStaff(sc, Item{Kind: ItemLine, Role: RoleStaff, Ref: ref, P: Point{X: 0, Y: y}, Q: Point{X: endX, Y: y}, Width: t})
		}
		by0, by1 := sc.barY()
		e.drawStart(sc)
		if bc := e.bars[0]; e.repeatStarts(bc.bar) {
			for _, it := range e.barlineItems(barRepeatStart, bc.content+startRepeatGap, by0-t/2, by1+t/2, Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: bc.bar, Event: -1, Note: -1}, true) {
				e.addStaff(sc, it)
			}
		}
		e.drawClefChanges(sc)
		for bi, bc := range e.bars {
			sb := sc.bars[bi]
			if bi > 0 {
				e.drawBarPrefix(sc, bi)
			}
			bref := ref
			bref.Bar = bc.bar
			for _, it := range e.barlineItems(e.barlineBefore(bi+1), bc.x1, by0-t/2, by1+t/2, bref, true) {
				e.addStaff(sc, it)
			}
			if bc.multi > 0 || bc.folded {
				if bc.multi > 0 {
					e.drawMultiRest(sc, bc)
				}
				continue
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
		e.drawTail(sc)
		e.drawTies(sc)
		for _, sb := range sc.bars {
			if sb.bar.multi == 0 && !sb.bar.folded {
				e.drawTuplets(sc, sb)
			}
		}
		e.drawExpression(sc)
	}
}

// tailLayout is where the cautionary key and time go after the last
// barline (Options.Cautionary).
type tailLayout struct{ keyX, timeX, w float64 }

func (e *engraver) tailPositions() tailLayout {
	return e.tailAfter(e.to, e.nextKey, e.nextFifths, e.nextMeter, e.nextMeterSig)
}

// tailAfter lays out the cautionary signatures after a bar for the
// changes the next one makes.
func (e *engraver) tailAfter(bar int, key bool, fifths int, meter bool, m score.Meter) tailLayout {
	var tl tailLayout
	if !key && !meter {
		return tl
	}
	x := 0.0
	if key {
		kw := 0.0
		for _, sc := range e.staves {
			kw = max(kw, e.keyWidth(e.tailKeyAccs(sc, bar, fifths)))
		}
		if kw > 0 {
			tl.keyX = 0.6
			x = tl.keyX + kw
		}
	}
	if meter {
		tl.timeX = x + 0.6
		if x > 0 {
			tl.timeX = x + 0.7
		}
		x = tl.timeX + e.timeWidth(m)
	}
	if x > 0 {
		tl.w = x + 0.5
	}
	return tl
}

// tailKeyAccs are the cautionary key change a staff shows after a bar:
// naturals for what goes, then the new key, in the next bar's clef.
func (e *engraver) tailKeyAccs(sc *staffCtx, bar, fifths int) []keyAcc {
	if sc.percussion {
		return nil
	}
	return keyChangeAccs(sc.clefAtBar(bar+1), writtenFifths(e.s.KeyAt(bar).Fifths, sc.iv), writtenFifths(fifths, sc.iv))
}

// drawTail draws a staff's cautionary key and time after the last
// barline.
func (e *engraver) drawTail(sc *staffCtx) {
	if e.tailW == 0 {
		return
	}
	tl := e.tailPositions()
	x0 := e.bars[len(e.bars)-1].x1 + e.barlineWidth(e.barlineBefore(len(e.bars)))
	ref := Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: e.to + 1, Event: -1, Note: -1}
	if e.nextKey {
		e.drawKey(sc, ref, x0+tl.keyX, e.tailKeyAccs(sc, e.to, e.nextFifths))
	}
	if e.nextMeter {
		e.drawTime(sc, ref, x0+tl.timeX, e.nextMeterSig)
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
		// Off the staff on its own ledger line, centred where the plain
		// rest would be.
		if lg := legerRest(g, x.restY); lg != g {
			rx += (e.f.Width(g) - e.f.Width(lg)) / 2
			g = lg
			e.restLedgers(sc, x, rx, e.f.Width(g), x.restY)
		}
		e.add(sc, x, e.glyphItem(RoleRest, x.ref, g, rx, x.restY, 1))
		dx := rx + x.restDotX
		for k := 0; k < x.dots; k++ {
			e.add(sc, x, e.glyphItem(RoleDot, x.ref, "augmentationDot", dx+float64(k)*dotSpacing, x.restDotY, 1))
		}
		return
	}
	s := x.size
	o := e.headOrigin(x.head) * s
	for _, n := range x.notes {
		ref := x.ref
		ref.Note = n.idx
		hg := x.head
		if n.head != "" {
			hg = n.head
		}
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
	e.drawArpeggio(sc, x)
	for k := 0; k < x.dots; k++ {
		for _, row := range x.dotRows {
			e.add(sc, x, e.glyphItem(RoleDot, x.ref, "augmentationDot", x.x+x.dotX+float64(k)*dotSpacing*s, 4-float64(row)/2+x.crossY, s))
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
	if len(x.notes) == 0 || sc.lines < 5 {
		return // a one-line staff's notes sit by the line, with none
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
		y := 4 - float64(L)/2 + x.crossY
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

// drawBarRest centres a bar rest in its bar, in multi-voice music above
// or below the middle for its voice and clear of the other voices' notes.
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
	y = e.barRestY(sc, x, g, rx, rx+w, y)
	x.x = rx
	if lg := legerRest(g, y); lg != g {
		rx += (w - e.f.Width(lg)) / 2
		g = lg
		e.restLedgers(sc, x, rx, e.f.Width(g), y)
	}
	x.restY = y
	e.add(sc, x, e.glyphItem(RoleRest, x.ref, g, rx, y, 1))
}

// restLedgers are the ledger lines between the staff and a whole or half
// rest further off it than its own (the glyph draws the one it hangs
// from or sits on).
func (e *engraver) restLedgers(sc *staffCtx, x *ev, rx, w, y float64) {
	th := e.d.LegerLineThickness
	for L := 5.0; L < y-0.01; L++ {
		e.add(sc, x, Item{Kind: ItemLine, Role: RoleLedger, Ref: x.ref, P: Point{X: rx, Y: L}, Q: Point{X: rx + w, Y: L}, Width: th})
	}
	for L := -1.0; L > y+0.01; L-- {
		e.add(sc, x, Item{Kind: ItemLine, Role: RoleLedger, Ref: x.ref, P: Point{X: rx, Y: L}, Q: Point{X: rx + w, Y: L}, Width: th})
	}
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
		f0, _ := first.barY()
		_, l1 := last.barY()
		for _, it := range e.barlineItems(barSingle, 0, first.top+f0-t/2, last.top+l1+t/2, NoRef, false) {
			e.items = append(e.items, it)
		}
	}
	for i, sc := range e.staves {
		if !sc.first || sc.nStaves < 2 {
			continue
		}
		j := i
		for j+1 < len(e.staves) && e.staves[j+1].line == sc.line && !e.staves[j+1].first && e.staves[j+1].tab == nil {
			j++ // (a linked tablature is not braced to its notation)
		}
		if j == i {
			continue
		}
		top, bot := sc.top, e.staves[j].top+e.staves[j].height()
		// The brace, stretched to the grand staff.
		h := bot - top
		size := math.Min(math.Max(h/4*0.55, 1), 2.4)
		b, _ := e.f.Box("brace")
		it := e.glyphItem(RoleBrace, Ref{Line: sc.line, Staff: -1, Voice: -1, Event: -1, Note: -1}, "brace", -braceGap-b.W()*size, bot, size)
		it.ScaleY = h / (b.H() * size)
		e.items = append(e.items, it)
		// Barlines through the gaps.
		if bc := e.bars[0]; e.repeatStarts(bc.bar) {
			for k := i; k < j; k++ {
				for _, it := range e.barlineItems(barRepeatStart, bc.content+startRepeatGap, e.staves[k].top+e.staves[k].height(), e.staves[k+1].top, NoRef, false) {
					e.items = append(e.items, it)
				}
			}
		}
		for bi, bc := range e.bars {
			for k := i; k < j; k++ {
				for _, it := range e.barlineItems(e.barlineBefore(bi+1), bc.x1, e.staves[k].top+e.staves[k].height(), e.staves[k+1].top, Ref{Line: sc.line, Staff: -1, Voice: -1, Bar: bc.bar, Event: -1, Note: -1}, false) {
					e.items = append(e.items, it)
				}
			}
		}
	}
	e.drawBrackets()
	e.drawBarNumbers()
	e.drawSystemMarks()
}

// drawBarNumbers numbers bars above the top staff.
func (e *engraver) drawBarNumbers() {
	if e.o.BarNumbers < 0 {
		return
	}
	top := e.staves[0]
	y := top.top - math.Max(1.4, -top.ext.Min.Y+0.5)
	for i, bc := range e.bars {
		show := i == 0 && bc.bar > 1 || i > 0 && e.o.BarNumbers > 0 && bc.bar%e.o.BarNumbers == 0 && !bc.folded
		if !show {
			continue
		}
		x := bc.x0
		e.items = append(e.items, Item{Kind: ItemText, Role: RoleBarNumber, Ref: Ref{Line: -1, Staff: -1, Voice: -1, Bar: bc.bar, Event: -1, Note: -1},
			P: Point{X: x, Y: y}, Text: strconv.Itoa(bc.bar), Font: TextItalic, Size: 1.6})
	}
}
