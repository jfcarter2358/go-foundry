package engrave

// clefkey.go — what stands at the head of a staff: clefs, key signatures,
// time signatures; and barlines.
//
// Staff positions count half spaces up from the bottom line (0 is the
// bottom line, 8 the top). A clef fixes which pitch the bottom line is
// (clefBottom) and which line its glyph curls round. Key signatures put
// their sharps and flats in the traditional zig-zag (treble F C G D A E B
// for sharps from the top line), shifted for the clef, with the tenor
// clef's own pattern for sharps; a change of key first cancels with
// naturals what the new key drops.

import (
	"strconv"

	"foundry/internal/score"
)

// clefBottom is the diatonic number (score.Pitch.Diatonic) of a clef's
// bottom line.
func clefBottom(clef string) int {
	switch clef {
	case "bass":
		return 2*7 + 4 // G2
	case "alto":
		return 3*7 + 3 // F3
	case "tenor":
		return 3*7 + 1 // D3
	case "treble_8vb":
		return 3*7 + 2 // E3
	case "treble_8va":
		return 5*7 + 2 // E5
	case "bass_8vb":
		return 1*7 + 4 // G1
	case "bass_8va":
		return 3*7 + 4 // G3
	}
	return 4*7 + 2 // E4: treble, and the percussion staff's neutral lines
}

// clefGlyph is a clef's glyph and the y its origin goes on.
func clefGlyph(clef string) (string, float64) {
	switch clef {
	case "bass":
		return "fClef", 1
	case "alto":
		return "cClef", 2
	case "tenor":
		return "cClef", 1
	case "treble_8vb":
		return "gClef8vb", 3
	case "treble_8va":
		return "gClef8va", 3
	case "bass_8vb":
		return "fClef8vb", 1
	case "bass_8va":
		return "fClef8va", 1
	case "percussion":
		return "unpitchedPercussionClef1", 2
	}
	return "gClef", 3
}

// Treble-clef staff positions of the key signature's accidentals, in the
// order they are added.
var (
	sharpPos      = [7]int{8, 5, 9, 6, 3, 7, 4}
	flatPos       = [7]int{4, 7, 3, 6, 2, 5, 1}
	tenorSharpPos = [7]int{2, 6, 3, 7, 4, 8, 5}
)

// keyShift is how far a clef moves the treble pattern.
func keyShift(clef string) int {
	switch clef {
	case "bass", "bass_8vb", "bass_8va":
		return -2
	case "alto":
		return -1
	case "tenor":
		return 1
	}
	return 0
}

// keyAcc is one accidental of a key signature.
type keyAcc struct {
	glyph string
	pos   int
}

// keyAccs are a key signature's accidentals on a clef.
func keyAccs(clef string, fifths int) []keyAcc {
	if clef == "percussion" {
		return nil
	}
	var out []keyAcc
	sh := keyShift(clef)
	for i := 0; i < min(fifths, 7); i++ {
		p := sharpPos[i] + sh
		if clef == "tenor" {
			p = tenorSharpPos[i]
		}
		out = append(out, keyAcc{"accidentalSharp", p})
	}
	for i := 0; i < min(-fifths, 7); i++ {
		out = append(out, keyAcc{"accidentalFlat", flatPos[i] + sh})
	}
	return out
}

// keyChangeAccs are what a change from one key to another shows: naturals
// for what is dropped, then the new key.
func keyChangeAccs(clef string, from, to int) []keyAcc {
	old := keyAccs(clef, from)
	var out []keyAcc
	keep := 0
	if from > 0 && to > 0 || from < 0 && to < 0 {
		keep = min(absInt(from), absInt(to))
	}
	for i := keep; i < len(old); i++ {
		out = append(out, keyAcc{"accidentalNatural", old[i].pos})
	}
	return append(out, keyAccs(clef, to)...)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// keyStep is the advance from one key-signature accidental to the next.
func (e *engraver) keyStep(g string) float64 { return e.f.Width(g) + 0.08 }

// keyWidth is a run of key accidentals' width.
func (e *engraver) keyWidth(accs []keyAcc) float64 {
	w := 0.0
	for i, a := range accs {
		if i == len(accs)-1 {
			w += e.f.Width(a.glyph)
		} else {
			w += e.keyStep(a.glyph)
		}
	}
	if len(accs) > 0 && accs[0].glyph == "accidentalNatural" && len(accs) > 1 && accs[len(accs)-1].glyph != "accidentalNatural" {
		w += 0.3 // a little air between the cancellation and the new key
	}
	return w
}

// timeRun is one line of a time signature: digits and pluses.
func timeRun(groups []int) []string {
	var out []string
	for i, g := range groups {
		if i > 0 {
			out = append(out, "timeSigPlusSmall") // SMuFL's plus inside a numerator
		}
		for _, c := range strconv.Itoa(g) {
			out = append(out, "timeSig"+string(c))
		}
	}
	return out
}

// runWidth is a run of numerals' width, set ink to ink numeralGap apart
// (the font's advances let a 1 or a plus touch its neighbours).
func (e *engraver) runWidth(gs []string) float64 {
	w := 0.0
	for i, g := range gs {
		w += e.f.Width(g)
		if i > 0 {
			w += numeralSep(gs[i-1], g)
		}
	}
	return w
}

// numeralSep is the space between two numerals of a run: more round a
// plus or colon than between digits.
func numeralSep(a, b string) float64 {
	if a == "timeSigPlusSmall" || b == "timeSigPlusSmall" || a == "tupletColon" || b == "tupletColon" {
		return 0.12
	}
	return numeralGap
}

// numeralGap is the space between a time signature's or tuplet's numerals.
const numeralGap = 0.15

// drawRun draws a run of numerals with its left edge at x.
func (e *engraver) drawRun(sc *staffCtx, x *ev, role Role, ref Ref, gs []string, cx, y float64) {
	for i, g := range gs {
		if i > 0 {
			cx += numeralSep(gs[i-1], g)
		}
		b, _ := e.f.Box(g)
		it := e.glyphItem(role, ref, g, cx-b.Min.X, y, 1)
		if x != nil {
			e.add(sc, x, it)
		} else {
			e.addStaff(sc, it)
		}
		cx += b.W()
	}
}

// timeWidth is a time signature's width.
func (e *engraver) timeWidth(m score.Meter) float64 {
	switch m.Symbol {
	case "common":
		return e.f.Width("timeSigCommon")
	case "cut":
		return e.f.Width("timeSigCutCommon")
	}
	return max(e.runWidth(timeRun(m.Groups)), e.runWidth(timeRun([]int{m.Den})))
}

// drawTime draws a time signature with its left edge at x.
func (e *engraver) drawTime(sc *staffCtx, ref Ref, x float64, m score.Meter) {
	switch m.Symbol {
	case "common":
		e.addStaff(sc, e.glyphItem(RoleTime, ref, "timeSigCommon", x, 2, 1))
		return
	case "cut":
		e.addStaff(sc, e.glyphItem(RoleTime, ref, "timeSigCutCommon", x, 2, 1))
		return
	}
	w := e.timeWidth(m)
	for _, row := range []struct {
		gs []string
		y  float64
	}{{timeRun(m.Groups), 1}, {timeRun([]int{m.Den}), 3}} {
		e.drawRun(sc, nil, RoleTime, ref, row.gs, x+(w-e.runWidth(row.gs))/2, row.y)
	}
}

// drawKey draws key accidentals from x.
func (e *engraver) drawKey(sc *staffCtx, ref Ref, x float64, accs []keyAcc) {
	for i, a := range accs {
		if i > 0 && accs[i-1].glyph == "accidentalNatural" && a.glyph != "accidentalNatural" {
			x += 0.3
		}
		e.addStaff(sc, e.glyphItem(RoleKey, ref, a.glyph, x, 4-float64(a.pos)/2, 1))
		x += e.keyStep(a.glyph)
	}
}

// startLayout is where the start's signatures go, the same on every staff
// so they line up down the system.
type startLayout struct {
	clefX, keyX, timeX, end float64
}

func (e *engraver) startPositions() startLayout {
	var sl startLayout
	sl.clefX = 0.7
	clefW, keyW := 0.0, 0.0
	for _, sc := range e.staves {
		g, _ := clefGlyph(sc.clef)
		clefW = max(clefW, e.f.Width(g))
		keyW = max(keyW, e.keyWidth(keyAccs(sc.clef, writtenFifths(e.bars[0].fifths, sc.iv))))
	}
	sl.keyX = sl.clefX + clefW + 0.6
	sl.end = sl.clefX + clefW
	if keyW > 0 {
		sl.end = sl.keyX + keyW
	}
	sl.timeX = sl.end + 0.7
	if keyW == 0 {
		sl.timeX = sl.end + 0.9
	}
	if e.bars[0].showTime {
		sl.end = sl.timeX + e.timeWidth(e.bars[0].meter)
	}
	return sl
}

// startPrefix is where the first bar's music may start.
func (e *engraver) startPrefix() float64 { return e.startPositions().end }

// drawStart draws a staff's clef, key and time at the layout's start.
func (e *engraver) drawStart(sc *staffCtx) {
	sl := e.startPositions()
	ref := Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: e.bars[0].bar, Event: -1, Note: -1}
	g, y := clefGlyph(sc.clef)
	e.addStaff(sc, e.glyphItem(RoleClef, ref, g, sl.clefX, y, 1))
	e.drawKey(sc, ref, sl.keyX, keyAccs(sc.clef, writtenFifths(e.bars[0].fifths, sc.iv)))
	if e.bars[0].showTime {
		e.drawTime(sc, ref, sl.timeX, e.bars[0].meter)
	}
}

// barPrefix is a bar's head after its barline: a key change and a time
// change.
type barPrefix struct {
	keyX, timeX, w float64
}

func (e *engraver) barPrefixLayout(bc *barCtx) barPrefix {
	var bp barPrefix
	x := 0.0
	if bc.showKey {
		kw := 0.0
		for _, sc := range e.staves {
			kw = max(kw, e.keyWidth(keyChangeAccs(sc.clefAtBar(bc.bar), writtenFifths(bc.prevFifths, sc.iv), writtenFifths(bc.fifths, sc.iv))))
		}
		if kw > 0 {
			bp.keyX = x + 0.6
			x = bp.keyX + kw
		}
	}
	if bc.showTime {
		bp.timeX = x + 0.6
		if x > 0 {
			bp.timeX = x + 0.7
		}
		x = bp.timeX + e.timeWidth(bc.meter)
	}
	bp.w = x
	return bp
}

// barPrefixWidth is the room a bar's head takes after its barline.
func (e *engraver) barPrefixWidth(bc *barCtx) float64 { return e.barPrefixLayout(bc).w }

// drawBarPrefix draws a key or time change at a bar's head.
func (e *engraver) drawBarPrefix(sc *staffCtx, bi int) {
	bc := e.bars[bi]
	if bc.prefixW == 0 {
		return
	}
	bp := e.barPrefixLayout(bc)
	x0 := bc.x0 + e.barlineWidth(e.barlineBefore(bi))
	ref := Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: bc.bar, Event: -1, Note: -1}
	if bc.showKey {
		e.drawKey(sc, ref, x0+bp.keyX, keyChangeAccs(sc.clefAtBar(bc.bar), writtenFifths(bc.prevFifths, sc.iv), writtenFifths(bc.fifths, sc.iv)))
	}
	if bc.showTime {
		e.drawTime(sc, ref, x0+bp.timeX, bc.meter)
	}
}

// barlineItems are a barline's strokes with its left edge at x, from y0 to
// y1. dots puts a repeat barline's dots in the second and third spaces (y
// staff-local: only for a barline drawn on one staff).
func (e *engraver) barlineItems(k barlineKind, x, y0, y1 float64, ref Ref, dots bool) []Item {
	d := e.d
	line := func(cx, w float64) Item {
		return Item{Kind: ItemLine, Role: RoleBarline, Ref: ref, P: Point{X: cx, Y: y0}, Q: Point{X: cx, Y: y1}, Width: w}
	}
	thin, thick := d.ThinBarlineThickness, d.ThickBarlineThickness
	dotW := e.f.Width("repeatDot")
	db, _ := e.f.Box("repeatDot")
	dotCol := func(left float64) []Item {
		if !dots {
			return nil
		}
		var out []Item
		for _, y := range []float64{1.5, 2.5} {
			out = append(out, e.glyphItem(RoleBarline, ref, "repeatDot", left-db.Min.X, y-(db.Min.Y+db.Max.Y)/2, 1))
		}
		return out
	}
	switch k {
	case barDouble:
		return []Item{line(x+thin/2, thin), line(x+thin+d.BarlineSeparation+thin/2, thin)}
	case barFinal:
		return []Item{line(x+thin/2, thin), line(x+thin+d.ThinThickBarlineSeparation+thick/2, thick)}
	case barRepeatEnd:
		// :|| — dots, thin, thick.
		out := dotCol(x)
		cx := x + dotW + d.RepeatBarlineDotSeparation
		return append(out, line(cx+thin/2, thin), line(cx+thin+d.ThinThickBarlineSeparation+thick/2, thick))
	case barRepeatStart:
		// ||: — thick, thin, dots.
		out := []Item{line(x+thick/2, thick), line(x+thick+d.ThinThickBarlineSeparation+thin/2, thin)}
		return append(out, dotCol(x+thick+d.ThinThickBarlineSeparation+thin+d.RepeatBarlineDotSeparation)...)
	case barRepeatBoth:
		// :||: — dots, thin, thick, thin, dots.
		out := dotCol(x)
		cx := x + dotW + d.RepeatBarlineDotSeparation
		out = append(out, line(cx+thin/2, thin))
		cx += thin + d.ThinThickBarlineSeparation
		out = append(out, line(cx+thick/2, thick))
		cx += thick + d.ThinThickBarlineSeparation
		out = append(out, line(cx+thin/2, thin))
		return append(out, dotCol(cx+thin+d.RepeatBarlineDotSeparation)...)
	}
	return []Item{line(x+thin/2, thin)}
}

// ---------- clef changes inside a staff ----------

// clefMark is a clef change drawn inside the layout: smaller than the
// staff's opening clef, just before the column it takes effect at — or, at
// a bar's start, just before that bar's barline, at the end of the bar
// before (atEnd), as engravers place it.
type clefMark struct {
	clef  string
	col   *column
	bar   int // the layout bar whose column it stands in
	atEnd bool
	// inkLeft is how far the staff's other ink at the column reaches left
	// of it: the clef goes beyond that.
	inkLeft float64
	x       float64 // the glyph's origin, once drawn
}

// clefChangeGap is the room between a clef change and what follows it.
const clefChangeGap = 0.35

// clefChangeGlyph is a clef change's glyph and size: SMuFL's smaller
// change clefs, or the full clef scaled down where there is none.
func clefChangeGlyph(clef string) (string, float64) {
	switch clef {
	case "treble":
		return "gClefChange", 1
	case "bass":
		return "fClefChange", 1
	case "alto", "tenor":
		return "cClefChange", 1
	}
	g, _ := clefGlyph(clef)
	return g, 0.75
}

// clefChangeWidth is the room a clef change takes.
func (e *engraver) clefChangeWidth(clef string) float64 {
	g, size := clefChangeGlyph(clef)
	return e.f.Width(g)*size + clefChangeGap
}

// collectClefChanges finds every staff's clef changes inside the layout
// and gives each its column.
func (e *engraver) collectClefChanges() {
	for _, sc := range e.staves {
		if sc.percussion || len(sc.st.Clefs) == 0 {
			continue
		}
		cur := sc.clef
		for _, ch := range sc.st.Clefs {
			at := ch.At
			if l := e.s.BarLength(at.Bar); at.Bar >= 1 && !at.Offset.Less(l) {
				at = score.Pos{Bar: at.Bar + 1, Offset: at.Offset.Sub(l)}
			}
			if at.Bar < e.from || at.Bar > e.to || (at.Bar == e.from && at.Offset.IsZero()) {
				if !score.PosLess(score.Pos{Bar: e.from}, at) {
					cur = ch.Clef // in force at the layout's start already
				}
				continue
			}
			if ch.Clef == cur {
				continue
			}
			cur = ch.Clef
			bi := at.Bar - e.from
			cm := &clefMark{clef: ch.Clef}
			if at.Offset.IsZero() {
				pb := e.bars[bi-1]
				cm.col, cm.bar, cm.atEnd = pb.cols[len(pb.cols)-1], pb.bar, true
			} else {
				cm.col, cm.bar = e.bars[bi].colFrom(at.Offset), e.bars[bi].bar
			}
			sc.clefMarks = append(sc.clefMarks, cm)
		}
	}
}

// drawClefChanges draws a staff's clef changes, once x is known.
func (e *engraver) drawClefChanges(sc *staffCtx) {
	for _, cm := range sc.clefMarks {
		g, size := clefChangeGlyph(cm.clef)
		_, y := clefGlyph(cm.clef)
		w := e.f.Width(g) * size
		cm.x = cm.col.x - cm.inkLeft - clefChangeGap - w
		if cm.atEnd {
			cm.x = cm.col.x - cm.inkLeft - 0.3 - w
		}
		ref := Ref{Line: sc.line, Staff: sc.staff, Voice: -1, Bar: cm.bar, Event: -1, Note: -1}
		e.addStaff(sc, e.glyphItem(RoleClef, ref, g, cm.x, y, size))
	}
}
