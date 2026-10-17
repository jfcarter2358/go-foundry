package engrave

// spanner.go — what is drawn across notes, and the text beside them.
//
// Slurs (`slur` … `slur_end`, the end inclusive) curve over the notes when
// their stems are mixed or down and under them when every stem is up (the
// voice's side in multi-voice music); an end on a stem's side meets the
// stem's tip, otherwise it sits over or under the notehead and beyond any
// staccato or tenuto there. The curve rises over whatever it spans — notes,
// stems, beams, ties, articulations — by arching higher, and when it would
// have to arch too far, by lifting both ends. A slur differs from a tie in
// its shape (thicker, higher, from the heads' middles rather than their
// sides) and in what it joins: a tie joins two of one pitch, a slur a
// phrase.
//
// Glissandi run from a note's head to the next note's, straight or wavy
// (`gliss=wavy`), with "gliss." over a long one. Hairpins (`cresc`/`dim` …
// `hairpin_end`) open or close between their first and last notes; with a
// value they are "cresc."/"dim." text and dashes instead. Open-ended ones
// run to the next dynamic in the voice. Dynamics are their SMuFL glyphs
// under the notes they stand at; a run of dynamics and hairpins that touch
// shares one row, low enough for every note over it. In multi-voice music
// the upper voices' dynamics go over the staff when a lower voice has its
// own. Expression text (`expr=`) goes with the dynamics; technique text
// (`tech=`, "pizz.") and staff text (`text=`, `"dolce"`) above the staff.
//
// Ottava lines (score.Ottavas: the notes under one are drawn where they are
// written, engrave.go) are "8va" with dashes and a hook, over the staff
// (under it for 8vb and 15mb); pedal lines are "Ped." and a bracket line
// under the staff with a notch at each re-pedal and a hook at the end.

import (
	"math"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/glyph"
)

// evKey is an event of a voice: a bar and an index into its events.
type evKey struct{ bar, ev int }

// spanRun is one spanner over a voice, in score terms (it may start or end
// outside the layout).
type spanRun struct {
	a, z evKey
	// zExcl ends the run where event z starts (a hairpin running into the
	// next dynamic), rather than after z.
	zExcl bool
	dir   int    // a hairpin's: +1 crescendo, -1 diminuendo
	text  string // a text crescendo's words ("" for a hairpin)
	notch []evKey
}

// eachEvent calls f for a voice's events in order, the whole voice: a
// spanner across the layout's edge starts or ends outside it.
func (e *engraver) eachEvent(v *score.Voice, mainOnly bool, f func(k evKey, se *score.Event)) {
	for b := 1; b <= len(v.Measures); b++ {
		m := &v.Measures[b-1]
		for i := range m.Events {
			if mainOnly && m.Events[i].Grace != score.NotGrace {
				continue
			}
			f(evKey{b, i}, &m.Events[i])
		}
	}
}

// evAt is the engraved event of a voice at a key, or nil when it is outside
// the layout.
func (e *engraver) evAt(sc *staffCtx, vi int, k evKey) *ev {
	if k.bar < e.from || k.bar > e.to {
		return nil
	}
	sb := sc.bars[k.bar-e.from]
	if vi >= len(sb.voices) {
		return nil
	}
	for _, x := range sb.voices[vi] {
		if x.ref.Event == k.ev {
			return x
		}
	}
	return nil
}

// inLayout reports a run that reaches into the layout.
func (e *engraver) inLayout(r spanRun) bool { return r.a.bar <= e.to && r.z.bar >= e.from }

// layoutStart and layoutEnd are where a run from before the layout starts
// and one past it ends.
func (e *engraver) layoutStart() float64 { return e.bars[0].content + 0.2 }
func (e *engraver) layoutEnd() float64   { return e.bars[len(e.bars)-1].x1 - 0.3 }

// slurRuns are a voice's slurs, as the interpreter plays them: open at
// `slur`, closed at the next `slur_end` (inclusive); one left open runs to
// the voice's last event.
func (e *engraver) slurRuns(v *score.Voice) []spanRun {
	var out []spanRun
	open := false
	var a, last evKey
	e.eachEvent(v, false, func(k evKey, se *score.Event) {
		if se.Has("slur") && !open {
			open, a = true, k
		}
		if se.Has("slur_end") && open {
			if k != a {
				out = append(out, spanRun{a: a, z: k})
			}
			open = false
		}
		last = k
	})
	if open && last != a {
		out = append(out, spanRun{a: a, z: last})
	}
	return out
}

// hairpinRuns are a voice's hairpins and text crescendos.
func (e *engraver) hairpinRuns(v *score.Voice) []spanRun {
	var out []spanRun
	var cur *spanRun
	var last evKey
	closeAt := func(k evKey, excl bool) {
		if cur != nil {
			cur.z, cur.zExcl = k, excl
			if !(excl && k == cur.a) {
				out = append(out, *cur)
			}
			cur = nil
		}
	}
	e.eachEvent(v, true, func(k evKey, se *score.Event) {
		// A dynamic after the start ends an open-ended hairpin there.
		if cur != nil && k != cur.a {
			for _, mk := range se.Marks {
				if _, ok := score.Dynamics[mk.Name]; ok {
					closeAt(k, true)
					break
				}
			}
		}
		for _, mk := range se.Marks {
			switch mk.Name {
			case "cresc", "dim":
				closeAt(k, true)
				r := spanRun{a: k, dir: 1}
				if mk.Name == "dim" {
					r.dir = -1
				}
				switch mk.Value {
				case "":
				case "text":
					r.text = map[int]string{1: "cresc.", -1: "dim."}[r.dir]
				default:
					r.text = mk.Value
				}
				cur = &r
			case "hairpin_end":
				closeAt(k, false)
			}
		}
		last = k
	})
	if cur != nil {
		closeAt(last, false)
	}
	return out
}

// pedalRuns are a voice's pedal lines: down at `ped`, a re-pedal at `ped`
// while down, up after `ped_end`.
func (e *engraver) pedalRuns(v *score.Voice) []spanRun {
	var out []spanRun
	var cur *spanRun
	var last evKey
	e.eachEvent(v, true, func(k evKey, se *score.Event) {
		for _, mk := range se.Marks {
			switch mk.Name {
			case "ped":
				if cur != nil {
					cur.notch = append(cur.notch, k)
				} else {
					cur = &spanRun{a: k}
				}
			case "ped_end":
				if cur != nil {
					cur.z = k
					out = append(out, *cur)
					cur = nil
				}
			}
		}
		last = k
	})
	if cur != nil {
		cur.z = last
		out = append(out, *cur)
	}
	return out
}

// startX is where a run starting at an event starts: its head (or rest).
func startX(x *ev) float64 {
	if x.rest {
		return x.x + x.restX
	}
	l, _ := headSpan(x)
	return x.x + l
}

// endX is where a run ending at event z ends: just before the next note
// in z's bar (or before z's barline, never past it into the next bar's
// signatures), or with excl just before z itself.
func (e *engraver) endX(z *ev, excl bool) float64 {
	if excl {
		return startX(z) - 0.6
	}
	if n := z.nextEv; n != nil && n.col != nil && n.ref.Bar > 0 && n.sb.bar == z.sb.bar {
		return startX(n) - 0.6
	}
	_, r := headSpan(z)
	return math.Max(z.x+r+0.6, z.sb.bar.x1-0.5)
}

// endAtPos is where a line ending at a time ends: before the note there,
// or for a bar's start before the barline that opens it (not after the
// signatures that follow it); past the layout, its end.
func (e *engraver) endAtPos(p score.Pos, gap float64) float64 {
	if p.Bar > e.to+1 || (p.Bar == e.to+1 && p.Offset.Sign() > 0) {
		return e.layoutEnd()
	}
	if p.Offset.Sign() == 0 && p.Bar-1 >= e.from && p.Bar-1 <= e.to {
		bc := e.bars[p.Bar-1-e.from]
		x := bc.x1 - 0.5
		// Not short of the bar's last note.
		if n := len(bc.cols); n >= 2 {
			x = math.Max(x, bc.cols[n-2].x+e.f.Width("noteheadBlack")+0.3)
		}
		return x
	}
	return e.xAtPos(p) - gap
}

// ---------- slurs ----------

// drawSlurs draws every slur of a staff.
func (e *engraver) drawSlurs(sc *staffCtx) {
	for vi, v := range sc.st.Voices {
		for _, r := range e.slurRuns(v) {
			if !e.inLayout(r) {
				continue
			}
			e.drawSlur(sc, vi, r)
		}
	}
}

// slurEnd is where a slur meets an event, over (above) or under it; heads
// keeps it at the notehead even on the stem's side (a grace note's slur).
func (e *engraver) slurEnd(x *ev, above, heads bool) Point {
	dir := 1.0
	if above {
		dir = -1
	}
	if len(x.notes) == 0 {
		y := x.box.Max.Y + 0.4
		if above {
			y = x.box.Min.Y - 0.4
		}
		return Point{X: (x.box.Min.X + x.box.Max.X) / 2, Y: y}
	}
	if x.hasStem && x.up == above && !heads {
		// At the stem's end, beyond it.
		return Point{X: x.stemX, Y: beyondArt(x, x.tipY+dir*0.35*x.size, above)}
	}
	n := x.notes[0]
	if above {
		n = x.notes[len(x.notes)-1]
	}
	off := 0.75
	if heads {
		off = 0.6
	}
	return Point{X: x.x + n.dx + x.headW/2, Y: beyondArt(x, n.y+dir*off*x.size, above)}
}

// beyondArt moves a slur's end at y past the event's articulations on its
// side.
func beyondArt(x *ev, y float64, above bool) float64 {
	if above {
		return math.Min(y, x.artTop-0.25)
	}
	return math.Max(y, x.artBot+0.25)
}

func (e *engraver) drawSlur(sc *staffCtx, vi int, r spanRun) {
	a, z := e.evAt(sc, vi, r.a), e.evAt(sc, vi, r.z)
	// The side: the voice's in multi-voice music, else away from the stems
	// (over them when they are mixed).
	var evs []*ev
	for _, sb := range sc.bars {
		if vi >= len(sb.voices) {
			continue
		}
		for _, x := range sb.voices[vi] {
			k := evKey{x.ref.Bar, x.ref.Event}
			if !keyLess(k, r.a) && !keyLess(r.z, k) && !x.space {
				evs = append(evs, x)
			}
		}
	}
	if len(evs) == 0 {
		return
	}
	above := true
	forced := false
	allUp, stems := true, 0
	for _, x := range evs {
		if x.forced {
			forced = true
		}
		if x.hasStem {
			stems++
			allUp = allUp && x.up
		}
	}
	grace := a != nil && a.grace
	switch {
	case grace:
		// From a grace note: on its heads' side, to the note's head.
		above = !a.up
	case forced:
		above = vi%2 == 0
	case stems > 0:
		above = !allUp
	default:
		above = !evs[0].up
	}
	var p0, p1 Point
	if a != nil {
		p0 = e.slurEnd(a, above, grace)
	}
	if z != nil {
		p1 = e.slurEnd(z, above, grace)
		if grace {
			// Into the note's side, under (over) its head.
			l, _ := headSpan(z)
			p1.X = z.x + l + 0.15
		}
	}
	if a == nil {
		p0 = Point{X: e.layoutStart(), Y: p1.Y}
	}
	if z == nil {
		p1 = Point{X: e.layoutEnd(), Y: p0.Y}
	}
	if a != nil && z != nil && a == z {
		return
	}
	if p1.X-p0.X < 0.8 {
		p1.X = p0.X + 0.8
	}
	e.putSlur(sc, evs[0], p0, p1, above)
}

// keyLess orders event keys.
func keyLess(a, b evKey) bool { return a.bar < b.bar || a.bar == b.bar && a.ev < b.ev }

// putSlur draws a slur from p0 to p1 over (above) or under what is between,
// arching or lifting until it clears the ink there.
func (e *engraver) putSlur(sc *staffCtx, owner *ev, p0, p1 Point, above bool) {
	dx := p1.X - p0.X
	l := math.Hypot(dx, p1.Y-p0.Y)
	h := math.Min(2.0, math.Max(0.55, 0.35+0.09*l))
	hMax := math.Max(h, math.Min(3.5, 0.3*l))
	mid := e.d.SlurMidpointThickness
	const clear = 0.3
	// need(u) is how far from the chord the curve's inner edge must be at
	// each point of the span.
	type pt struct{ u, d float64 }
	var pts []pt
	for x := p0.X + 0.5; x < p1.X-0.5; x += skyStep {
		u := (x - p0.X) / dx
		cy := p0.Y + (p1.Y-p0.Y)*u
		var d float64
		if above {
			ob := sc.sky.topIn(x, x+skyStep, math.Inf(1))
			if math.IsInf(ob, 1) {
				continue
			}
			d = cy - (ob - clear)
		} else {
			ob := sc.sky.botIn(x, x+skyStep, math.Inf(-1))
			if math.IsInf(ob, -1) {
				continue
			}
			d = (ob + clear) - cy
		}
		pts = append(pts, pt{u, d + mid/2})
	}
	// The arch and the lift of both ends that clear every point at the
	// least cost: arching a little higher is cheaper than lifting the ends
	// (which takes the slur away from its notes), arching very high is not.
	best, bestLift, bestCost := h, 0.0, math.Inf(1)
	for hc := h; hc <= hMax+1e-9; hc += 0.1 {
		lift := 0.0
		for _, p := range pts {
			lift = math.Max(lift, p.d-hc*4*p.u*(1-p.u))
		}
		if cost := 1.6*lift + (hc - h); cost < bestCost-1e-9 {
			best, bestLift, bestCost = hc, lift, cost
		}
	}
	if above {
		p0.Y, p1.Y = p0.Y-bestLift, p1.Y-bestLift
	} else {
		p0.Y, p1.Y = p0.Y+bestLift, p1.Y+bestLift
	}
	need := best
	ref := owner.ref
	ref.Note = -1
	e.put(sc, nil, Item{Kind: ItemPath, Role: RoleSlur, Ref: ref,
		Path: crescent(p0, p1, above, need, mid, e.d.SlurEndpointThickness)})
}

// ---------- glissandi ----------

// glissMin is the least length of a glissando's line. Spacing keeps it
// (glissRods), so a slide between close notes is printed as it is played.
const glissMin = 1.6

// glissRun is one glissando of a voice, in score terms: from event a to the
// voice's next main event z. to says z is a chord the line can reach (not
// a rest, and not past the voice's end); either end may be outside the
// layout.
type glissRun struct {
	a, z evKey
	to   bool
	wavy bool
}

// glissRuns are a voice's glissandi, as the interpreter plays them: a
// `gliss` chord slides to the next main event when that is a chord.
func (e *engraver) glissRuns(v *score.Voice) []glissRun {
	var out []glissRun
	var prev *score.Event
	var pk evKey
	mark := func(se *score.Event) (wavy, ok bool) {
		if se.Kind != score.EventChord || len(se.Notes) == 0 {
			return false, false
		}
		for _, mk := range se.Marks {
			if mk.Name == "gliss" {
				return mk.Value == "wavy", true
			}
		}
		return false, false
	}
	e.eachEvent(v, true, func(k evKey, se *score.Event) {
		if prev != nil {
			if wavy, ok := mark(prev); ok {
				out = append(out, glissRun{a: pk, z: k, to: se.Kind == score.EventChord && len(se.Notes) > 0, wavy: wavy})
			}
		}
		prev, pk = se, k
	})
	if prev != nil {
		if wavy, ok := mark(prev); ok {
			out = append(out, glissRun{a: pk, wavy: wavy})
		}
	}
	return out
}

// glissFrom is where a glissando leaves its note, from the note's column:
// just past its heads, or past its dots.
func glissFrom(a *ev) float64 {
	if a.dots > 0 {
		return a.shift + a.dotX + float64(a.dots)*dotSpacing + 0.15
	}
	_, ar := headSpan(a)
	return a.shift + ar + 0.25
}

// glissTo is where a glissando meets its target, from the target's column:
// just before its heads, or its accidentals.
func glissTo(b *ev) float64 {
	bl, _ := headSpan(b)
	x1 := b.shift + bl - 0.25
	for _, n := range b.notes {
		if n.acc {
			x1 = math.Min(x1, n.accX-0.15)
		}
	}
	return x1
}

// glissEnds are the glissando's ends that sit on a layout event: its note
// and its target (nil when outside the layout, or with no target).
func (e *engraver) glissEnds(sc *staffCtx, vi int, r glissRun) (a, z *ev) {
	a = e.evAt(sc, vi, r.a)
	if a != nil && (a.col == nil || len(a.notes) == 0) {
		a = nil
	}
	if r.to {
		z = e.evAt(sc, vi, r.z)
		if z != nil && (z.col == nil || len(z.notes) == 0) {
			z = nil
		}
	}
	return a, z
}

// colIndex is a column's index in its bar.
func colIndex(bc *barCtx, c *column) int {
	for i, x := range bc.cols {
		if x == c {
			return i
		}
	}
	return -1
}

// rodDistance is the least distance (the rods) from column k of layout bar
// i to column m of bar j, i <= j: the gaps between, and each barline, bar
// head and lead crossed.
func (e *engraver) rodDistance(i, k, j, m int) float64 {
	d := 0.0
	for b := i; b <= j; b++ {
		bc := e.bars[b]
		from, to := 0, len(bc.rods)
		if b == i {
			from = k
		}
		if b == j {
			to = m
		}
		if b > i {
			d += e.barlineWidth(e.barlineBefore(b)) + bc.prefixW + bc.lead
		}
		for g := from; g < to && g < len(bc.rods); g++ {
			d += bc.rods[g]
		}
	}
	return d
}

// glissRods keeps room for every glissando's line: glissMin between its
// note's ink and its target's, and between the note and its bar's end when
// the target is in a later bar (a system may break there and the line runs
// to it). A system opening on a glissando's target has a lead long enough
// for the line coming in (glissLeadIn).
func (e *engraver) glissRods() {
	for _, sc := range e.staves {
		for vi, v := range sc.st.Voices {
			for _, r := range e.glissRuns(v) {
				a, z := e.glissEnds(sc, vi, r)
				if a == nil {
					continue
				}
				ai := r.a.bar - e.from
				abc := e.bars[ai]
				ka := colIndex(abc, a.col)
				last := len(abc.rods) - 1
				if ka < 0 || last < 0 {
					continue
				}
				from := glissFrom(a)
				if z == nil || r.z.bar != r.a.bar {
					// To the bar's end: the line to the next system (or the
					// stub of one going nowhere) ends 0.3 before it.
					if short := from + glissMin + 0.3 - e.rodDistance(ai, ka, ai, len(abc.cols)-1); short > 0 {
						abc.rods[last] += short
					}
				}
				if z == nil {
					continue
				}
				zi := r.z.bar - e.from
				kz := colIndex(e.bars[zi], z.col)
				if kz < 0 || (zi == ai && kz <= ka) {
					continue
				}
				short := from - glissTo(z) + glissMin - e.rodDistance(ai, ka, zi, kz)
				switch {
				case short <= 0:
				case zi == ai:
					abc.rods[kz-1] += short // the gap before the target
				default:
					abc.rods[last] += short // the gap before the barline
				}
			}
		}
	}
	if len(e.bars) > 0 {
		e.bars[0].lead = math.Max(e.bars[0].lead, e.glissLeadIn(0))
	}
}

// glissLeadIn is the least lead layout bar bi needs when it opens a system
// with a glissando coming into it from the bar before: room for the line
// from the system's start (layoutStart) to the target.
func (e *engraver) glissLeadIn(bi int) float64 {
	bc := e.bars[bi]
	need := 0.0
	for _, sc := range e.staves {
		for vi, v := range sc.st.Voices {
			for _, r := range e.glissRuns(v) {
				if !r.to || r.z.bar != bc.bar || r.a.bar >= bc.bar {
					continue
				}
				_, z := e.glissEnds(sc, vi, r)
				if z == nil {
					continue
				}
				kz := colIndex(bc, z.col)
				if kz < 0 {
					continue
				}
				// layoutStart is 0.2 into the bar's content.
				need = math.Max(need, 0.2+glissMin-glissTo(z)-e.rodDistance(bi, 0, bi, kz))
			}
		}
	}
	return need
}

// drawGlissandi draws every glissando line of a staff. One across the
// layout's edge is drawn in two: to the end of the first system, halfway to
// its target's height, and on from the start of the next.
func (e *engraver) drawGlissandi(sc *staffCtx) {
	for vi, v := range sc.st.Voices {
		for _, r := range e.glissRuns(v) {
			a, z := e.glissEnds(sc, vi, r)
			switch {
			case a != nil && z != nil:
				e.drawGliss(sc, a, z, a.col.x+glissFrom(a), z.col.x+glissTo(z), nil, r.wavy, true, true)
			case a != nil && r.to && r.z.bar > e.to:
				// On to the next system: halfway to the target there.
				t := e.outsidePlaced(sc, vi, v, r.z)
				e.drawGliss(sc, a, a, a.col.x+glissFrom(a), e.layoutEnd(), glissHalfway(a, t, false), r.wavy, true, false)
			case a != nil:
				// To a rest or the voice's end: a short line, level.
				x0 := a.col.x + glissFrom(a)
				e.drawGliss(sc, a, a, x0, math.Min(x0+3, a.sb.bar.x1-0.3), nil, r.wavy, false, false)
			case z != nil && r.a.bar < e.from:
				// From the system before: from halfway to the note.
				s := e.outsidePlaced(sc, vi, v, r.a)
				e.drawGliss(sc, z, z, e.layoutStart(), z.col.x+glissTo(z), glissHalfway(s, z, true), r.wavy, false, true)
			}
		}
	}
}

// outsidePlaced is outsideEv with a tablature's notes on their strings.
func (e *engraver) outsidePlaced(sc *staffCtx, vi int, v *score.Voice, k evKey) *ev {
	x := e.outsideEv(sc, vi, v, k)
	if x != nil && sc.tab != nil {
		e.placeTab(sc, x, sc.tab.VoicePlaces(v))
	}
	return x
}

// glissHalfway pairs a glissando's notes from the top, the top note sliding
// to the top note, for a line broken by a system break: each pair's heights
// at its note and halfway, in the order the piece runs (in: from halfway to
// the target).
func glissHalfway(a, z *ev, in bool) [][2]float64 {
	if a == nil || z == nil || len(a.notes) == 0 || len(z.notes) == 0 {
		return nil
	}
	var out [][2]float64
	for i := 0; i < min(len(a.notes), len(z.notes)); i++ {
		ya, yz := a.notes[len(a.notes)-1-i].y, z.notes[len(z.notes)-1-i].y
		mid := (ya + yz) / 2
		if in {
			out = append(out, [2]float64{mid, yz})
		} else {
			out = append(out, [2]float64{ya, mid})
		}
	}
	return out
}

// drawGliss draws a glissando's lines from x0 to x1: from a's notes to z's,
// paired from the top, or the given pairs of heights (nil: a's notes,
// level). atA and atZ say which ends meet a notehead, where the line starts
// from the head's edge rather than its middle.
func (e *engraver) drawGliss(sc *staffCtx, a, z *ev, x0, x1 float64, pairs [][2]float64, wavy, atA, atZ bool) {
	ref := a.ref
	ref.Note = -1
	if pairs == nil {
		k := min(len(a.notes), len(z.notes))
		for i := 0; i < k; i++ {
			pairs = append(pairs, [2]float64{a.notes[len(a.notes)-1-i].y, z.notes[len(z.notes)-1-i].y})
		}
	}
	if len(pairs) == 0 || x1-x0 < 0.4 {
		return
	}
	th := 0.14
	for _, p := range pairs {
		// From the heads' edges toward each other.
		y0, y1 := p[0], p[1]
		if y1 != y0 {
			s := math.Copysign(0.25, y1-y0)
			if atA {
				y0 += s
			}
			if atZ {
				y1 -= s
			}
		}
		p0, p1 := Point{X: x0, Y: y0}, Point{X: x1, Y: y1}
		if wavy {
			e.put(sc, nil, Item{Kind: ItemPath, Role: RoleLine, Ref: ref, Path: wavyPath(p0, p1, 0.14, 0.7, th)})
		} else {
			e.put(sc, nil, Item{Kind: ItemLine, Role: RoleLine, Ref: ref, P: p0, Q: p1, Width: th})
		}
	}
	// "gliss." over a long one, level, just over the line at its middle.
	if p := pairs[0]; x1-x0 > 4.5 {
		size := 1.2
		w := TextWidthFont("gliss.", TextItalic, size)
		mx := (x0 + x1) / 2
		lineY := func(x float64) float64 { return p[0] + (p[1]-p[0])*(x-x0)/(x1-x0) }
		base := math.Min(lineY(mx-w/2), lineY(mx+w/2)) - 0.25*size - 0.2
		e.put(sc, nil, Item{Kind: ItemText, Role: RoleText, Ref: ref, P: Point{X: mx - w/2, Y: base}, Text: "gliss.", Font: TextItalic, Size: size})
	}
}

// ---------- dynamics, hairpins, expression text ----------

// dynamicGlyphs are the SMuFL glyphs a dynamic is drawn with.
var dynamicGlyphs = map[string][]string{
	"pppp": {"dynamicPPPP"}, "ppp": {"dynamicPPP"}, "pp": {"dynamicPP"}, "p": {"dynamicPiano"},
	"mp": {"dynamicMP"}, "mf": {"dynamicMF"}, "f": {"dynamicForte"}, "ff": {"dynamicFF"},
	"fff": {"dynamicFFF"}, "ffff": {"dynamicFFFF"},
	"sf": {"dynamicSforzando1"}, "sfz": {"dynamicSforzato"}, "fz": {"dynamicForzando"},
	"rf": {"dynamicRinforzando1"}, "rfz": {"dynamicRinforzando2"},
	"sffz": {"dynamicSforzando", "dynamicForte", "dynamicForte", "dynamicZ"},
	"fp":   {"dynamicFortePiano"}, "sfp": {"dynamicSforzandoPiano"}, "sfpp": {"dynamicSforzandoPianissimo"},
	"niente": {"dynamicNiente"},
}

// DynamicGlyphs are the glyphs a dynamic is drawn with, nil for a name
// that is not a dynamic (for an editor's buttons).
func DynamicGlyphs(name string) []string { return dynamicGlyphs[name] }

// dynamicBox is a dynamic's ink from its first glyph's origin on the
// baseline.
func (e *engraver) dynamicBox(name string) Rect {
	var r Rect
	for i, g := range dynamicGlyphs[name] {
		b, _ := e.f.Box(g)
		r = r.Union(b.Offset(Point{X: e.dynamicGlyphX(name, i), Y: 0}))
	}
	return r
}

// dynamicGlyphX is the origin of a dynamic's i-th glyph from its first's:
// letters set box to box, a little into each other as italics are.
func (e *engraver) dynamicGlyphX(name string, i int) float64 {
	gs := dynamicGlyphs[name]
	x := 0.0
	for k := 1; k <= i; k++ {
		prev, _ := e.f.Box(gs[k-1])
		cur, _ := e.f.Box(gs[k])
		x += prev.Max.X - cur.Min.X - 0.12
	}
	return x
}

// dynItem is one thing in a staff's dynamics row: a dynamic, a hairpin, a
// text crescendo, expression text.
type dynItem struct {
	x0, x1 float64
	// hTop and hBot are how far it reaches above and below the row's
	// middle line.
	hTop, hBot float64
	above      bool
	draw       func(yc float64)
}

const (
	dynRowBelow  = 6.4 // the least y of a dynamics row's middle under the staff
	dynRowAbove  = -2.4
	dynClear     = 0.55 // the row's ink to the notes'
	hairOpening  = 1.0
	dynTextSize  = 1.7
	exprTextSize = 1.7
)

// drawDynamics draws a staff's dynamics, hairpins, text crescendos and
// expression text in rows.
func (e *engraver) drawDynamics(sc *staffCtx) {
	// Upper voices go over the staff when a lower voice has dynamics of
	// its own.
	lowerHas := false
	for vi, v := range sc.st.Voices {
		if vi%2 == 1 {
			e.eachEvent(v, true, func(k evKey, se *score.Event) {
				for _, mk := range se.Marks {
					if _, ok := score.Dynamics[mk.Name]; ok {
						lowerHas = true
					}
				}
			})
		}
	}
	// A sung staff has its dynamics over it (Gould): the words are under
	// it.
	vocal := e.vocal(sc)
	aboveFor := func(vi int) bool { return vocal || lowerHas && vi%2 == 0 }

	var items []*dynItem
	// Dynamics first: hairpins start and stop at them.
	dynL := map[*ev]float64{}
	dynR := map[*ev]float64{}
	for _, sb := range sc.bars {
		for vi, v := range sb.voices {
			for _, x := range v {
				if x.grace || x.space || x.col == nil {
					continue
				}
				var names []string
				for _, mk := range x.e.Marks {
					if _, ok := dynamicGlyphs[mk.Name]; ok {
						names = append(names, mk.Name)
					}
				}
				if len(names) == 0 {
					continue
				}
				// One after another, the first centred on the note.
				cx := x.x + e.dynCentre(x)
				first := e.dynamicBox(names[0])
				left := cx - (first.Min.X+first.Max.X)/2
				at := left
				for _, name := range names {
					name := name
					b := e.dynamicBox(name)
					gx := at
					x := x
					// The glyphs' baseline a little under the row's middle,
					// so their bodies centre on it.
					const drop = 0.35
					items = append(items, &dynItem{x0: gx + b.Min.X, x1: gx + b.Max.X, hTop: -(drop + b.Min.Y), hBot: drop + b.Max.Y, above: aboveFor(vi),
						draw: func(yc float64) {
							base := yc + drop
							for i, g := range dynamicGlyphs[name] {
								e.put(sc, nil, e.glyphItem(RoleDynamic, x.ref, g, gx+e.dynamicGlyphX(name, i), base, 1))
							}
						}})
					at = gx + b.Max.X + 0.3
				}
				dynL[x], dynR[x] = left+first.Min.X, at-0.3
			}
		}
	}
	// Hairpins and text crescendos.
	for vi, v := range sc.st.Voices {
		for _, r := range e.hairpinRuns(v) {
			if !e.inLayout(r) {
				continue
			}
			a, z := e.evAt(sc, vi, r.a), e.evAt(sc, vi, r.z)
			x0, x1 := e.layoutStart(), e.layoutEnd()
			var owner *ev
			if a != nil {
				owner = a
				x0 = startX(a)
				if rr, ok := dynR[a]; ok {
					x0 = rr + 0.4
				}
			}
			if z != nil {
				if owner == nil {
					owner = z
				}
				x1 = e.endX(z, r.zExcl)
				next := z
				if !r.zExcl {
					next = z.nextEv
				}
				if next != nil {
					if l, ok := dynL[next]; ok {
						x1 = math.Min(x1, l-0.4)
					}
				}
			}
			if owner == nil {
				continue
			}
			if x1-x0 < 1.0 {
				x1 = x0 + 1.0
			}
			ref := owner.ref
			ref.Note = -1
			r := r
			if r.text != "" {
				w := TextWidthFont(r.text, TextItalic, dynTextSize)
				items = append(items, &dynItem{x0: x0, x1: x1, hTop: 0.9, hBot: 0.9, above: aboveFor(vi), draw: func(yc float64) {
					base := yc + 0.45
					e.put(sc, nil, Item{Kind: ItemText, Role: RoleDynamic, Ref: ref, P: Point{X: x0, Y: base}, Text: r.text, Font: TextItalic, Size: dynTextSize})
					e.dashes(sc, RoleLine, ref, Point{X: x0 + w + 0.5, Y: yc}, Point{X: x1, Y: yc}, 0.12, 0.6, 0.6)
				}})
				continue
			}
			op := math.Min(hairOpening, 0.3*(x1-x0))
			th := e.d.HairpinThickness
			items = append(items, &dynItem{x0: x0, x1: x1, hTop: op / 2, hBot: op / 2, above: aboveFor(vi), draw: func(yc float64) {
				tip, mouth := x0, x1
				if r.dir < 0 {
					tip, mouth = x1, x0
				}
				e.put(sc, nil, Item{Kind: ItemLine, Role: RoleHairpin, Ref: ref, P: Point{X: tip, Y: yc}, Q: Point{X: mouth, Y: yc - op/2}, Width: th})
				e.put(sc, nil, Item{Kind: ItemLine, Role: RoleHairpin, Ref: ref, P: Point{X: tip, Y: yc}, Q: Point{X: mouth, Y: yc + op/2}, Width: th})
			}})
		}
	}
	// Expression text with the dynamics.
	for _, sb := range sc.bars {
		for vi, v := range sb.voices {
			for _, x := range v {
				if x.grace || x.space || x.col == nil {
					continue
				}
				for _, mk := range x.e.Marks {
					if mk.Name != "expr" || mk.Value == "" {
						continue
					}
					x0 := startX(x)
					if rr, ok := dynR[x]; ok {
						x0 = rr + 0.5
					}
					text, ref := mk.Value, x.ref
					w := TextWidthFont(text, TextItalic, exprTextSize)
					items = append(items, &dynItem{x0: x0, x1: x0 + w, hTop: 0.9, hBot: 0.9, above: aboveFor(vi), draw: func(yc float64) {
						e.put(sc, nil, Item{Kind: ItemText, Role: RoleText, Ref: ref, P: Point{X: x0, Y: yc + 0.45}, Text: text, Font: TextItalic, Size: exprTextSize})
					}})
					dynR[x] = x0 + w
				}
			}
		}
	}
	e.placeRows(sc, items)
}

// vocal reports a sung staff: its line has lyrics, or plays a choir or a
// voice from the library. A tablature or a drum staff with words under it
// is not sung and keeps its dynamics below.
func (e *engraver) vocal(sc *staffCtx) bool {
	if sc.line < 0 || sc.line >= len(e.s.Lines) || sc.tab != nil || sc.percussion {
		return false
	}
	l := e.s.Lines[sc.line]
	if strings.HasPrefix(l.Instrument, "choir/") || strings.HasPrefix(l.Instrument, "vocal/") {
		return true
	}
	return l.Verses() > 0
}

// dynCentre is where an event's first dynamic is centred, from its x.
func (e *engraver) dynCentre(x *ev) float64 {
	switch {
	case x.barRest:
		return 0.8
	case x.rest:
		return x.restX + e.f.Width(restGlyph(x.base))/2
	}
	return x.headW / 2
}

// dynExtent is how far an event's dynamics and expression text reach left
// and right of its x, and whether it starts a hairpin: spacing keeps two
// neighbours' dynamics apart, and room for a hairpin between them.
func (e *engraver) dynExtent(x *ev) (l, r float64, has, hairpin bool) {
	cx := e.dynCentre(x)
	at := 0.0
	for _, mk := range x.e.Marks {
		if _, ok := dynamicGlyphs[mk.Name]; ok {
			b := e.dynamicBox(mk.Name)
			if !has {
				at = cx - (b.Min.X+b.Max.X)/2
				l = at + b.Min.X
			}
			has = true
			r = at + b.Max.X
			at = r + 0.3
		}
		if (mk.Name == "cresc" || mk.Name == "dim") && mk.Value == "" {
			hairpin = true
		}
	}
	for _, mk := range x.e.Marks {
		if mk.Name == "expr" && mk.Value != "" {
			if !has {
				l, r = 0, 0
			}
			has = true
			r += 0.5 + TextWidthFont(mk.Value, TextItalic, exprTextSize)
		}
	}
	return l, r, has, hairpin
}

// placeRows puts dynamics-row items on rows: items that touch share one, as
// close to the staff as the ink under all of them allows.
func (e *engraver) placeRows(sc *staffCtx, items []*dynItem) {
	for _, above := range []bool{false, true} {
		var side []*dynItem
		for _, it := range items {
			if it.above == above {
				side = append(side, it)
			}
		}
		sortDyn(side)
		for i := 0; i < len(side); {
			j := i + 1
			end := side[i].x1
			for j < len(side) && side[j].x0 < end+1.2 {
				end = math.Max(end, side[j].x1)
				j++
			}
			group := side[i:j]
			var yc float64
			if above {
				yc = dynRowAbove
				for _, it := range group {
					yc = math.Min(yc, sc.sky.topIn(it.x0-0.3, it.x1+0.3, 0)-dynClear-it.hBot)
				}
			} else {
				yc = dynRowBelow
				for _, it := range group {
					yc = math.Max(yc, sc.sky.botIn(it.x0-0.3, it.x1+0.3, 4)+dynClear+it.hTop)
				}
			}
			for _, it := range group {
				it.draw(yc)
			}
			i = j
		}
	}
}

func sortDyn(s []*dynItem) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].x0 < s[j-1].x0; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// dashes draws a dashed line from p to q.
func (e *engraver) dashes(sc *staffCtx, role Role, ref Ref, p, q Point, width, dash, gap float64) {
	l := math.Hypot(q.X-p.X, q.Y-p.Y)
	if l <= 0 {
		return
	}
	ux, uy := (q.X-p.X)/l, (q.Y-p.Y)/l
	for t := 0.0; t < l; t += dash + gap {
		t1 := math.Min(t+dash, l)
		e.put(sc, nil, Item{Kind: ItemLine, Role: role, Ref: ref, P: Point{X: p.X + ux*t, Y: p.Y + uy*t}, Q: Point{X: p.X + ux*t1, Y: p.Y + uy*t1}, Width: width})
	}
}

// ---------- technique and staff text ----------

const staffTextSize = 1.6

// drawStaffText draws technique (`tech=`) and staff text (`text=`) over
// the staff (under it for a lower voice in multi-voice music), left-aligned
// with the note.
func (e *engraver) drawStaffText(sc *staffCtx) {
	for _, sb := range sc.bars {
		for _, v := range sb.voices {
			for _, x := range v {
				if x.grace || x.space || x.col == nil {
					continue
				}
				for _, mk := range x.e.Marks {
					if (mk.Name != "tech" && mk.Name != "text") || mk.Value == "" {
						continue
					}
					above := true
					if a, forced := markAbove(x); forced {
						above = a
					}
					x0 := startX(x)
					w := TextWidth(mk.Value, staffTextSize)
					var base float64
					if above {
						base = math.Min(sc.sky.topIn(x0, x0+w, 0)-0.5, -0.8) - 0.25*staffTextSize
					} else {
						base = math.Max(sc.sky.botIn(x0, x0+w, 4)+0.5, 4.8) + 0.75*staffTextSize
					}
					e.put(sc, nil, Item{Kind: ItemText, Role: RoleText, Ref: x.ref, P: Point{X: x0, Y: base}, Text: mk.Value, Font: TextPlain, Size: staffTextSize})
				}
			}
		}
	}
}

// ---------- ottava lines ----------

// ottavaGlyphs are the ottava marks' glyphs, and whether the line is over
// the staff.
var ottavaGlyphs = map[string]struct {
	glyph string
	above bool
}{
	"8va": {"ottavaAlta", true}, "15ma": {"quindicesimaAlta", true},
	"8vb": {"ottavaBassaVb", false}, "15mb": {"quindicesimaBassaMb", false},
}

// ottavaName is an ottava's mark by the octaves it moves.
func ottavaName(octaves int) string {
	switch octaves {
	case 2:
		return "15ma"
	case -1:
		return "8vb"
	case -2:
		return "15mb"
	}
	return "8va"
}

// xAtPos is a written position's x on the layout: between the columns of
// its bar; before the layout its start, after it its end.
func (e *engraver) xAtPos(p score.Pos) float64 {
	if p.Bar < e.from {
		return e.layoutStart()
	}
	if p.Bar > e.to {
		return e.layoutEnd()
	}
	bc := e.bars[p.Bar-e.from]
	off := p.Offset.Float()
	cs := bc.cols
	if off <= cs[0].offF {
		return cs[0].x
	}
	for i := 1; i < len(cs); i++ {
		if off <= cs[i].offF {
			a, c := cs[i-1], cs[i]
			if c.offF <= a.offF {
				return c.x
			}
			return a.x + (c.x-a.x)*(off-a.offF)/(c.offF-a.offF)
		}
	}
	return cs[len(cs)-1].x
}

// drawOttavas draws a staff's ottava lines.
func (e *engraver) drawOttavas(sc *staffCtx) {
	for _, o := range sc.ottavas {
		if o.From.Bar > e.to || score.PosLess(o.To, score.Pos{Bar: e.from}) || !score.PosLess(o.From, o.To) {
			continue
		}
		og := ottavaGlyphs[ottavaName(o.Octaves)]
		x0 := e.xAtPos(o.From) - 0.2
		// It closes inside the layout, or exactly at its end.
		closes := !score.PosLess(score.Pos{Bar: e.to + 1}, o.To)
		x1 := e.endAtPos(o.To, 0.7)
		b, _ := e.f.Box(og.glyph)
		// A line carried on from the system before is marked "(8va)" at
		// the new system's start (Gould: the parenthesised figure).
		continued := o.From.Bar < e.from
		var lp, rp glyph.Rect
		w := b.W()
		if continued {
			lp, _ = e.f.Box("octaveParensLeft")
			rp, _ = e.f.Box("octaveParensRight")
			w += lp.W() + rp.W() + 0.2
		}
		x1 = math.Max(x1, x0+w+0.8)
		ref := Ref{Line: sc.line, Staff: sc.staff, Voice: o.Voice, Bar: o.From.Bar, Event: -1, Note: -1}
		th := e.d.OctaveLineThickness
		var yc float64
		if og.above {
			yc = math.Min(sc.sky.topIn(x0, x1, 0)-0.6, -1.4) - b.H()/2
		} else {
			yc = math.Max(sc.sky.botIn(x0, x1, 4)+0.6, 5.4) + b.H()/2
		}
		gx := x0
		if continued {
			e.put(sc, nil, e.centred(RoleLine, ref, "octaveParensLeft", x0+lp.W()/2, yc, 1))
			gx = x0 + lp.W() + 0.1
		}
		e.put(sc, nil, e.centred(RoleLine, ref, og.glyph, gx+b.W()/2, yc, 1))
		if continued {
			e.put(sc, nil, e.centred(RoleLine, ref, "octaveParensRight", gx+b.W()+0.1+rp.W()/2, yc, 1))
		}
		// The dashes from the glyph's middle height, then the hook toward
		// the staff.
		e.dashes(sc, RoleLine, ref, Point{X: x0 + w + 0.3, Y: yc}, Point{X: x1, Y: yc}, th, 0.5, 0.45)
		hook := 1.0
		if !og.above {
			hook = -1.0
		}
		if closes {
			e.put(sc, nil, Item{Kind: ItemLine, Role: RoleLine, Ref: ref, P: Point{X: x1 - th/2, Y: yc}, Q: Point{X: x1 - th/2, Y: yc + hook}, Width: th})
		}
	}
}

// ---------- pedal lines ----------

// drawPedal draws a staff's pedal lines: "Ped.", the line, a notch at each
// re-pedal, the hook where it comes up.
func (e *engraver) drawPedal(sc *staffCtx) {
	for vi, v := range sc.st.Voices {
		for _, r := range e.pedalRuns(v) {
			if !e.inLayout(r) {
				continue
			}
			a, z := e.evAt(sc, vi, r.a), e.evAt(sc, vi, r.z)
			x0, x1 := e.layoutStart(), e.layoutEnd()
			var owner *ev
			if a != nil {
				x0, owner = startX(a)-0.2, a
			}
			if z != nil {
				x1 = e.endX(z, false)
				if owner == nil {
					owner = z
				}
			}
			if owner == nil {
				continue
			}
			ref := owner.ref
			ref.Note = -1
			th := e.d.PedalLineThickness
			pb, _ := e.f.Box("keyboardPedalPed")
			lineFrom := x0
			if a != nil {
				lineFrom = x0 + pb.W() + 0.25
			}
			x1 = math.Max(x1, lineFrom+0.6)
			yb := math.Max(sc.sky.botIn(x0, x1, 4)+0.6+pb.H(), 4+4.2)
			if a != nil {
				e.put(sc, nil, e.glyphItem(RoleLine, ref, "keyboardPedalPed", x0-pb.Min.X, yb-pb.Max.Y, 1))
			}
			at := lineFrom
			for _, k := range r.notch {
				n := e.evAt(sc, vi, k)
				if n == nil {
					continue
				}
				nx := startX(n)
				e.put(sc, nil, Item{Kind: ItemLine, Role: RoleLine, Ref: ref, P: Point{X: at, Y: yb}, Q: Point{X: nx - 0.35, Y: yb}, Width: th})
				e.put(sc, nil, Item{Kind: ItemLine, Role: RoleLine, Ref: ref, P: Point{X: nx - 0.35, Y: yb}, Q: Point{X: nx, Y: yb - 0.9}, Width: th})
				e.put(sc, nil, Item{Kind: ItemLine, Role: RoleLine, Ref: ref, P: Point{X: nx, Y: yb - 0.9}, Q: Point{X: nx + 0.35, Y: yb}, Width: th})
				at = nx + 0.35
			}
			e.put(sc, nil, Item{Kind: ItemLine, Role: RoleLine, Ref: ref, P: Point{X: at, Y: yb}, Q: Point{X: x1, Y: yb}, Width: th})
			if z != nil {
				e.put(sc, nil, Item{Kind: ItemLine, Role: RoleLine, Ref: ref, P: Point{X: x1 - th/2, Y: yb + th/2}, Q: Point{X: x1 - th/2, Y: yb - 1.0}, Width: th})
			}
		}
	}
}
