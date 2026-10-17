package engrave

// tab.go — tablature: a fretted instrument's staff as its strings.
//
// A staff with a tuning (score.Tab) is engraved twice over one score staff:
// the notation, and under it a tablature staff of one line a string, the
// highest on top, tabGap apart — or the tablature alone (Tab.Only). Each
// note is a fret number on its string, where score.Tab.Place puts it: a
// string a note, the hand's stretch small, near where the hand was for the
// note before, the writer's str= choices kept. The string line breaks
// round the number so it reads cleanly; a note tied over from the one
// before shows its fret in parentheses; a note the tuning cannot reach is
// left off.
//
// Linked under notation, the tablature carries the fret numbers alone (the
// rhythm is the notation's). Alone, it carries the rhythm under itself:
// stems down from below the bottom string, beams flat at their tips as the
// meter groups them, flags, dots and rests, the way guitar books print a
// tab with rhythm. The music around the notes — dynamics, articulations,
// slurs, lyrics — goes on the notation staff; a tablature on its own
// carries the dynamics, technique and staff text, chord symbols and
// lyrics, and the marks drawn at the notes: articulations, slurs,
// ornaments and fermatas over the strings, tremolo strokes on its stems,
// an arpeggio beside the numbers, tuplets under the rhythm.
//
// Every fret number Refs its note on the score staff, so a click on the
// tablature selects the note it shows, and an edit made on either view is
// an edit of the music both show.

import (
	"math"
	"sort"
	"strconv"

	"foundry/internal/score"
)

const (
	tabGap = 1.5 // between a tablature's strings
	// tabGapAbove is the least room between a notation staff's bottom line
	// and the tablature linked under it.
	tabGapAbove = 4.0
	tabFretSize = 1.35 // a fret number's em, in spaces
	tabStemTop  = 0.6  // below the bottom string, where a rhythm stem starts
	tabStem     = 2.6  // a rhythm stem's length (a half note's is shorter)
)

// placeTab puts an event's notes on their strings: where the voice,
// walked from its start, puts them (score.Tab.VoicePlaces), so a system
// break does not move the hand.
func (e *engraver) placeTab(sc *staffCtx, x *ev, places [][][]score.TabNote) {
	b, i := x.ref.Bar, x.ref.Event
	if b < 0 {
		b = -b // an event before the layout
	}
	if len(x.notes) == 0 || b < 1 || b > len(places) || i >= len(places[b-1]) || len(places[b-1][i]) != len(x.e.Notes) {
		return
	}
	res := places[b-1][i]
	for _, n := range x.notes {
		r := res[n.idx]
		n.str, n.fret, n.tabOK = r.String, r.Fret, r.OK
		n.y = float64(r.String-1) * tabGap
		// pos counts down the strings so the rest of the engraver's
		// orderings (lowest first) read the lowest string first.
		n.pos = sc.lines - r.String
	}
	sort.SliceStable(x.notes, func(i, j int) bool { return x.notes[i].pos < x.notes[j].pos })
}

// fretText is what a tablature note prints: its fret, in parentheses when
// it is tied over from the note before.
func fretText(n *nt) string {
	t := strconv.Itoa(n.fret)
	if n.tieIn {
		t = "(" + t + ")"
	}
	return t
}

// tabHeadW is the width a fret number is centred over: a black notehead's,
// so the numbers stand where the notation's heads do.
func (e *engraver) tabHeadW() float64 { return e.f.Width("noteheadBlack") }

// arrangeTab is a tablature column's ink either side of the column's x:
// the widest fret number there, centred over a notehead's width.
func (e *engraver) arrangeTab(evs []*ev) (left, right float64) {
	hw := e.tabHeadW()
	for _, x := range evs {
		x.shift = 0
		x.headW = hw
		w := 0.0
		for _, n := range x.notes {
			if n.tabOK {
				w = math.Max(w, TextWidth(fretText(n), tabFretSize))
			}
		}
		if e.tabRhythm(x.sb.st) && x.dots > 0 {
			w = math.Max(w, hw+0.5+float64(x.dots)*dotSpacing)
		}
		l := w/2 - hw/2
		if ok, _ := arpeggio(x); ok && e.tabRhythm(x.sb.st) {
			l += arpGap + arpWidth
		}
		left = math.Max(left, l)
		right = math.Max(right, hw/2+w/2)
	}
	return left, right
}

// tabRhythm reports a tablature that carries its own rhythm: one with no
// notation over it.
func (e *engraver) tabRhythm(sc *staffCtx) bool { return sc.tabOnly }

// tabClef is the TAB clef glyph for a tablature, and the size that makes
// it most of the staff's height.
func (e *engraver) tabClef(sc *staffCtx) (string, float64) {
	g := "6stringTabClef"
	if sc.lines <= 4 {
		g = "4stringTabClef"
	}
	b, _ := e.f.Box(g)
	size := 1.0
	if b.H() > 0 {
		size = math.Min(1.4, sc.height()*0.8/b.H())
	}
	return g, size
}

// drawTabStaff draws a tablature: its strings broken round the fret
// numbers, the TAB clef, time signatures, barlines, the numbers, and on a
// tablature alone the rhythm.
func (e *engraver) drawTabStaff(sc *staffCtx) {
	endX := e.layoutEndX()
	t := e.d.StaffLineThickness
	ref := Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: 0, Event: -1, Note: -1}
	y0, y1 := sc.barY()

	// The opening: clef and time.
	sl := e.startPositions()
	g, size := e.tabClef(sc)
	b, _ := e.f.Box(g)
	sref := ref
	sref.Bar = e.bars[0].bar
	e.addStaff(sc, e.glyphItem(RoleClef, sref, g, sl.clefX, sc.mid()-(b.Min.Y+b.Max.Y)/2*size, size))
	if e.bars[0].showTime {
		e.drawTime(sc, sref, sl.timeX, e.bars[0].meter)
	}
	if bc := e.bars[0]; e.repeatStarts(bc.bar) {
		for _, it := range e.barlineItems(barRepeatStart, bc.content+startRepeatGap, y0-t/2, y1+t/2, sref, true) {
			e.addStaff(sc, e.tabDots(sc, it))
		}
	}

	// Gaps in each string for the numbers on it.
	gaps := make([][][2]float64, sc.lines+1)
	for bi, bc := range e.bars {
		sb := sc.bars[bi]
		if bi > 0 {
			e.drawBarPrefix(sc, bi)
		}
		bref := ref
		bref.Bar = bc.bar
		for _, it := range e.barlineItems(e.barlineBefore(bi+1), bc.x1, y0-t/2, y1+t/2, bref, true) {
			e.addStaff(sc, e.tabDots(sc, it))
		}
		if bc.multi > 0 || bc.folded {
			if bc.multi > 0 && sc.tabOnly {
				e.drawMultiRest(sc, bc)
			}
			continue
		}
		for _, v := range sb.voices {
			for _, x := range v {
				if x.space || x.col == nil || x.grace {
					continue
				}
				if x.rest {
					if sc.tabOnly {
						e.drawTabRest(sc, x)
					}
					continue
				}
				for _, n := range x.notes {
					if !n.tabOK {
						continue
					}
					ft := fretText(n)
					w := TextWidth(ft, tabFretSize)
					cx := x.x + x.headW/2
					nref := x.ref
					nref.Note = n.idx
					e.add(sc, x, Item{Kind: ItemText, Role: RoleNotehead, Ref: nref, P: Point{X: cx, Y: n.y + 0.36*tabFretSize},
						Text: ft, Font: TextPlain, Size: tabFretSize, Align: AlignCenter})
					gaps[n.str] = append(gaps[n.str], [2]float64{cx - w/2 - 0.12, cx + w/2 + 0.12})
				}
				if sc.tabOnly {
					e.drawTabStem(sc, x)
				}
			}
		}
		if sc.tabOnly {
			for _, g := range sb.beams {
				e.drawTabBeam(sc, g)
			}
			if sb.empty {
				e.drawImpliedRest(sc, sb)
			}
		}
	}
	e.drawTail(sc)

	// The strings, broken round the numbers.
	for i, y := range sc.lineYs() {
		str := i + 1
		gs := gaps[str]
		sort.Slice(gs, func(a, b int) bool { return gs[a][0] < gs[b][0] })
		x := 0.0
		for _, gp := range gs {
			if gp[0] > x {
				e.addStaff(sc, Item{Kind: ItemLine, Role: RoleStaff, Ref: ref, P: Point{X: x, Y: y}, Q: Point{X: gp[0], Y: y}, Width: t})
			}
			x = math.Max(x, gp[1])
		}
		if x < endX {
			e.addStaff(sc, Item{Kind: ItemLine, Role: RoleStaff, Ref: ref, P: Point{X: x, Y: y}, Q: Point{X: endX, Y: y}, Width: t})
		}
	}
	sc.sky = e.skyOf(sc.items, -8, endX+8)
	e.drawTabTuning(sc)
	if sc.tabOnly {
		// Alone, the tablature carries what is drawn at the notes too:
		// tuplets under the rhythm, tremolo strokes on its stems, and over
		// the strings the articulations, slurs, ornaments and fermatas;
		// an arpeggio beside the fret numbers.
		for _, sb := range sc.bars {
			if sb.bar.multi == 0 && !sb.bar.folded {
				e.drawTabTuplets(sc, sb)
			}
		}
		for _, sb := range sc.bars {
			for _, v := range sb.voices {
				for _, x := range v {
					if x.space || x.col == nil || x.grace || x.rest {
						continue
					}
					e.drawTabArticulations(sc, x)
					e.drawTabTremolo(sc, x)
					e.drawTabArpeggio(sc, x)
				}
			}
		}
		e.drawTabSlurs(sc)
		for _, sb := range sc.bars {
			for _, v := range sb.voices {
				for _, x := range v {
					if !x.space && x.col != nil && !x.grace {
						e.drawOrnaments(sc, x)
					}
				}
			}
		}
		// Alone, the tablature carries the line's words and dynamics.
		e.drawLyrics(sc)
		e.drawDynamics(sc)
		e.drawStaffText(sc)
		if sc.first {
			e.drawChordSymbols(sc)
		}
	}
}

// tabDots moves a repeat barline's dots from a five-line staff's second
// and third spaces to either side of a tablature's middle.
func (e *engraver) tabDots(sc *staffCtx, it Item) Item {
	if it.Kind == ItemGlyph && it.Glyph == "repeatDot" {
		it.P.Y += sc.mid() - 2
	}
	return it
}

// tabStemX is where a tablature rhythm stem stands: under the middle of
// the fret numbers.
func (e *engraver) tabStemX(x *ev) float64 { return x.x + x.headW/2 }

// tabStemEnd is a rhythm stem's tip: shorter for a half note.
func (e *engraver) tabStemEnd(sc *staffCtx, x *ev) float64 {
	top := sc.height() + tabStemTop
	if x.base == score.Half {
		return top + tabStem*0.6
	}
	return top + tabStem
}

// drawTabStem draws a tablature note's rhythm: its stem (none for a whole
// note), a flag when it is not beamed, and its dots.
func (e *engraver) drawTabStem(sc *staffCtx, x *ev) {
	if !x.hasStem && x.dots == 0 {
		return
	}
	sx := e.tabStemX(x)
	top := sc.height() + tabStemTop
	tip := e.tabStemEnd(sc, x)
	th := e.d.StemThickness
	if x.hasStem {
		e.add(sc, x, Item{Kind: ItemLine, Role: RoleStem, Ref: x.ref, P: Point{X: sx, Y: top}, Q: Point{X: sx, Y: tip}, Width: th})
		x.stemX, x.tipY = sx, tip
		if x.base >= score.Eighth && x.beam == nil {
			fg := flagGlyph(x.base, false)
			a, _ := e.f.Anchor(fg, "stemDownSW")
			e.add(sc, x, e.glyphItem(RoleFlag, x.ref, fg, sx-th/2-a.X, tip-a.Y, 1))
		}
	}
	for k := 0; k < x.dots; k++ {
		e.add(sc, x, e.glyphItem(RoleDot, x.ref, "augmentationDot", sx+0.35+float64(k)*dotSpacing, tip-0.6, 1))
	}
}

// drawTabBeam draws a beam group's beams flat at the stems' tips: the
// primary across the group, each further beam over the notes that have it
// (a lone one's a stub toward the note it belongs with).
func (e *engraver) drawTabBeam(sc *staffCtx, g *beamGroup) {
	if len(g.evs) < 2 {
		return
	}
	th := e.d.BeamThickness
	tip := sc.height() + tabStemTop + tabStem
	beam := func(x0, x1, y float64, ref Ref) {
		e.addStaff(sc, polyItem(RoleBeam, ref, Point{X: x0, Y: y - th}, Point{X: x1, Y: y - th}, Point{X: x1, Y: y}, Point{X: x0, Y: y}))
	}
	ref := g.evs[0].ref
	beam(e.tabStemX(g.evs[0]), e.tabStemX(g.evs[len(g.evs)-1]), tip, ref)
	maxLevel := 1
	for _, x := range g.evs {
		maxLevel = max(maxLevel, beamCount(x.base))
	}
	for lv := 2; lv <= maxLevel; lv++ {
		y := tip - float64(lv-1)*beamStep
		for i := 0; i < len(g.evs); {
			if beamCount(g.evs[i].base) < lv {
				i++
				continue
			}
			j := i
			for j+1 < len(g.evs) && beamCount(g.evs[j+1].base) >= lv {
				j++
			}
			x0, x1 := e.tabStemX(g.evs[i]), e.tabStemX(g.evs[j])
			if i == j {
				// A partial beam toward the note it belongs with.
				if i > 0 {
					x0 = x1 - 0.9
				} else {
					x1 = x0 + 0.9
				}
			}
			beam(x0, x1, y, g.evs[i].ref)
			i = j + 1
		}
	}
}

// drawTabRest draws a rest on a tablature with its own rhythm, in the
// middle of the strings.
func (e *engraver) drawTabRest(sc *staffCtx, x *ev) {
	if x.barRest {
		bc := x.sb.bar
		g, _ := barRestGlyph(bc)
		w := e.f.Width(g)
		x.x = (bc.content+bc.x1)/2 - w/2
		e.add(sc, x, e.glyphItem(RoleRest, x.ref, g, x.x, sc.mid()-1, 1))
		return
	}
	g := restGlyph(x.base)
	b, _ := e.f.Box(g)
	e.add(sc, x, e.glyphItem(RoleRest, x.ref, g, x.x, sc.mid()-(b.Min.Y+b.Max.Y)/2, 1))
	for k := 0; k < x.dots; k++ {
		e.add(sc, x, e.glyphItem(RoleDot, x.ref, "augmentationDot", x.x+b.Max.X+0.3+float64(k)*dotSpacing, sc.mid()-0.5, 1))
	}
}

// tabFretTop is how high over its string line a tablature note's fret
// number reaches.
const tabFretTop = 0.75

// tabTop is the top of an event's fret numbers.
func tabTop(x *ev) float64 {
	top := math.Inf(1)
	for _, n := range x.notes {
		if n.tabOK {
			top = math.Min(top, n.y-tabFretTop)
		}
	}
	if math.IsInf(top, 1) {
		return 0
	}
	return top
}

// drawTabArticulations stacks an event's articulations over the strings,
// above its fret numbers and clear of the staff's top line.
func (e *engraver) drawTabArticulations(sc *staffCtx, x *ev) {
	arts := eventArticulations(x.e)
	if len(arts) == 0 || len(x.notes) == 0 {
		return
	}
	cx := x.x + x.headW/2
	edge := math.Min(tabTop(x), -0.3)
	for i, a := range arts {
		b, _ := e.f.Box(a.above)
		gap := 0.3
		if i > 0 {
			gap = 0.2
		}
		cy := math.Min(edge-gap-b.H()/2, sc.sky.topIn(cx-b.W()/2, cx+b.W()/2, math.Inf(1))-0.12-b.H()/2)
		r := e.put(sc, x, e.centred(RoleArticulation, x.ref, a.above, cx, cy, 1))
		edge = r.Min.Y
	}
}

// drawTabTremolo draws tremolo strokes on a rhythm stem (trem=n), or
// between two (trem2=n); a stemless note's go under its numbers.
func (e *engraver) drawTabTremolo(sc *staffCtx, x *ev) {
	top := sc.height() + tabStemTop
	for _, mk := range x.e.Marks {
		n, ok := score.TremoloStrokes(mk.Value)
		if !ok {
			continue
		}
		switch mk.Name {
		case "trem":
			g := []string{"tremolo1", "tremolo2", "tremolo3", "tremolo4"}[n-1]
			b, _ := e.f.Box(g)
			cx, cy := e.tabStemX(x), top+0.5+b.H()/2
			if x.hasStem {
				k := 0.5
				if x.base >= score.Eighth {
					k = 0.4
				}
				cy = math.Min(top+(x.tipY-top)*k, x.tipY-0.5-b.H()/2)
				cy = math.Max(cy, top+0.3+b.H()/2)
			}
			e.put(sc, x, e.centred(RoleArticulation, x.ref, g, cx, cy, 1))
		case "trem2":
			y := x.nextEv
			if y == nil || y.col == nil || len(y.notes) == 0 {
				continue
			}
			th := e.d.BeamThickness * 0.9
			xa, xb := e.tabStemX(x)+0.3, e.tabStemX(y)-0.3
			if xb-xa < 0.6 {
				continue
			}
			mid := top + tabStem*0.5
			total := float64(n-1)*beamStep + th
			for k := 0; k < n; k++ {
				y0 := mid - total/2 + float64(k)*beamStep
				e.put(sc, x, polyItem(RoleBeam, x.ref, Point{X: xa, Y: y0}, Point{X: xb, Y: y0}, Point{X: xb, Y: y0 + th}, Point{X: xa, Y: y0 + th}))
			}
		}
	}
}

// drawTabArpeggio draws an arpeggio's wavy line just left of a chord's
// fret numbers, from its top string to its bottom one.
func (e *engraver) drawTabArpeggio(sc *staffCtx, x *ev) {
	ok, arrow := arpeggio(x)
	if !ok {
		return
	}
	top, bot, w := math.Inf(1), math.Inf(-1), 0.0
	for _, n := range x.notes {
		if n.tabOK {
			top, bot = math.Min(top, n.y-0.6), math.Max(bot, n.y+0.6)
			w = math.Max(w, TextWidth(fretText(n), tabFretSize))
		}
	}
	if math.IsInf(top, 1) {
		return
	}
	cx := x.x + x.headW/2 - w/2 - arpGap - arpWidth/2
	y0, y1 := top, bot
	if arrow == "up" {
		y0 += 0.45
	}
	if arrow == "down" {
		y1 -= 0.45
	}
	e.add(sc, x, Item{Kind: ItemPath, Role: RoleArticulation, Ref: x.ref, Path: wavyPath(Point{X: cx, Y: y1}, Point{X: cx, Y: y0}, 0.16, 0.75, 0.14)})
	head := func(tipY, dir float64) {
		e.add(sc, x, polyItem(RoleArticulation, x.ref, Point{X: cx, Y: tipY}, Point{X: cx + 0.3, Y: tipY - dir*0.55}, Point{X: cx - 0.3, Y: tipY - dir*0.55}))
	}
	switch arrow {
	case "up":
		head(top, -1)
	case "down":
		head(bot, 1)
	}
}

// drawTabSlurs draws a tablature's slurs over the fret numbers they join
// (a guitarist's hammer-ons and pull-offs read the same way).
func (e *engraver) drawTabSlurs(sc *staffCtx) {
	for vi, v := range sc.st.Voices {
		for _, r := range e.slurRuns(v) {
			if !e.inLayout(r) {
				continue
			}
			a, z := e.evAt(sc, vi, r.a), e.evAt(sc, vi, r.z)
			if a != nil && a == z {
				continue
			}
			end := func(x *ev) Point {
				return Point{X: x.x + x.headW/2, Y: math.Min(tabTop(x), 0) - 0.3}
			}
			var p0, p1 Point
			owner := a
			if a != nil {
				p0 = end(a)
			}
			if z != nil {
				p1 = end(z)
				if owner == nil {
					owner = z
				}
			}
			if owner == nil {
				continue
			}
			if a == nil {
				p0 = Point{X: e.layoutStart(), Y: p1.Y}
			}
			if z == nil {
				p1 = Point{X: e.layoutEnd(), Y: p0.Y}
			}
			if p1.X-p0.X < 0.8 {
				p1.X = p0.X + 0.8
			}
			e.putSlur(sc, owner, p0, p1, true)
		}
	}
}

// drawTabTuplets draws a bar's tuplets under a tablature's rhythm: the
// number under a beam that joins exactly the tuplet's notes, else a
// bracket hooked up toward the stems; an inner tuplet nearer the stems.
func (e *engraver) drawTabTuplets(sc *staffCtx, sb *staffBar) {
	sps := append([]*tupletSpan(nil), sb.tuplets...)
	sort.SliceStable(sps, func(i, j int) bool { return sps[i].depth > sps[j].depth })
	tip := sc.height() + tabStemTop + tabStem
	low := map[*ev]float64{}
	nb, _ := e.f.Box("tuplet3")
	h := (nb.Max.Y - nb.Min.Y) / 2
	mid := (nb.Min.Y + nb.Max.Y) / 2
	th := e.d.TupletBracketThickness
	for _, sp := range sps {
		evs := sp.evs
		if len(evs) == 0 {
			continue
		}
		first, last := evs[0], evs[len(evs)-1]
		ref := first.ref
		ref.Note = -1
		xa, xb := e.tabStemX(first), e.tabStemX(last)
		if first.rest {
			xa = first.x + first.restX
		}
		if last.rest {
			xb = last.x + last.restX + e.f.Width(restGlyph(last.base))
		}
		y := tip + 0.5
		for _, x := range evs {
			if v, ok := low[x]; ok {
				y = math.Max(y, v+0.3)
			}
			if x.rest {
				y = math.Max(y, x.box.Max.Y+0.3)
			}
		}
		beamed := first.beam != nil && len(first.beam.evs) == len(evs)
		for _, x := range evs {
			beamed = beamed && x.beam == first.beam
		}
		gs := tupletGlyphs(sp.num, sp.den)
		nw := e.runWidth(gs)
		xm := (xa + xb) / 2
		cy := y + h
		if !beamed {
			xa, xb = xa-0.4, xb+0.4
			cy = y + 0.7
			gap := nw/2 + 0.35
			e.addStaff(sc, Item{Kind: ItemLine, Role: RoleTuplet, Ref: ref, P: Point{X: xa, Y: cy - 0.7}, Q: Point{X: xa, Y: cy + th/2}, Width: th})
			e.addStaff(sc, Item{Kind: ItemLine, Role: RoleTuplet, Ref: ref, P: Point{X: xa - th/2, Y: cy}, Q: Point{X: xm - gap, Y: cy}, Width: th})
			e.addStaff(sc, Item{Kind: ItemLine, Role: RoleTuplet, Ref: ref, P: Point{X: xm + gap, Y: cy}, Q: Point{X: xb + th/2, Y: cy}, Width: th})
			e.addStaff(sc, Item{Kind: ItemLine, Role: RoleTuplet, Ref: ref, P: Point{X: xb, Y: cy + th/2}, Q: Point{X: xb, Y: cy - 0.7}, Width: th})
		}
		e.drawTupletNumber(sc, first, gs, xm-nw/2, cy-mid)
		for _, x := range evs {
			low[x] = cy + h
		}
	}
}

// tabTuneSize is the em of the tuning and capo heading.
const tabTuneSize = 1.4

// drawTabTuning heads a tablature at the start of the music with what a
// player must set up before playing: the strings tuned away from the
// instrument's standard tuning as circled string numbers ("⑥ = D", a
// whole tuning when most strings move) and the capo ("Capo 2"), over the
// tablature at the system's left, as guitar books print them.
func (e *engraver) drawTabTuning(sc *staffCtx) {
	if sc.tab == nil || len(e.bars) == 0 || e.bars[0].bar != 1 {
		return
	}
	strs, all := sc.tab.Retuning()
	if len(strs) == 0 && sc.tab.Capo <= 0 {
		return
	}
	type piece struct {
		text, glyph string
		w           float64
	}
	var ps []piece
	text := func(t string) { ps = append(ps, piece{text: t, w: TextWidth(t, tabTuneSize)}) }
	circled := func(n int) {
		g := "guitarString" + strconv.Itoa(n)
		ps = append(ps, piece{glyph: g, w: e.f.Width(g)*tabTuneSize/2 + 0.15})
	}
	name := func(p score.Pitch) string {
		return string(score.Steps[p.Step]) + map[int8]string{-2: "𝄫", -1: "♭", 1: "♯", 2: "𝄪"}[p.Alter]
	}
	if all {
		text("Tuning: ")
		for i, r := range strs {
			if i > 0 {
				text(" ")
			}
			text(name(r.Pitch))
		}
	} else {
		for i, r := range strs {
			if i > 0 {
				text("   ")
			}
			circled(r.String)
			text(" = " + name(r.Pitch))
		}
	}
	if sc.tab.Capo > 0 {
		if len(ps) > 0 {
			text("     ")
		}
		text("Capo " + strconv.Itoa(sc.tab.Capo))
	}
	w := 0.0
	for _, p := range ps {
		w += p.w
	}
	x := e.startPositions().clefX
	base := math.Min(sc.sky.topIn(x, x+w, 0)-0.6, -0.9)
	ref := Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: 1, Event: -1, Note: -1}
	for _, p := range ps {
		if p.glyph != "" {
			b, _ := e.f.Box(p.glyph)
			size := tabTuneSize / 2
			// Centred on the letters' middle (x-height).
			y := base - 0.36*tabTuneSize - (b.Min.Y+b.Max.Y)/2*size
			e.addStaff(sc, e.glyphItem(RoleText, ref, p.glyph, x-b.Min.X*size, y, size))
		} else {
			e.addStaff(sc, Item{Kind: ItemText, Role: RoleText, Ref: ref, P: Point{X: x, Y: base}, Text: p.text, Font: TextPlain, Size: tabTuneSize})
		}
		x += p.w
	}
	sc.sky = e.skyOf(sc.items, -8, e.layoutEndX()+8)
}
