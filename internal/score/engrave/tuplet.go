package engrave

// tuplet.go — tuplet brackets and numbers.
//
// A tuplet shows its number ("3"), or its ratio ("5:3") when the "in the
// time of" is not the usual one. The number goes on the stem side; when
// the tuplet's notes are exactly one beam it sits by the beam's middle and
// needs no bracket, otherwise a bracket spans the tuplet from its first
// note's head to its last one's, clear of every note and stem between,
// outside the staff, with hooks toward the notes and a gap for the number.
// Nested tuplets are drawn inside out, each outer one clearing the inner
// ones' brackets.

import (
	"math"
	"sort"
	"strconv"

	"foundry/internal/score"
)

// tupletSpan is one tuplet in one voice's bar.
type tupletSpan struct {
	voice    int
	index    int // 1-based into the measure's tuplets
	depth    int
	num, den int
	evs      []*ev // main events inside, in order
}

// tupletSpans collects a voice's tuplets in a bar.
func (e *engraver) tupletSpans(sb *staffBar, vi int, v []*ev) {
	m := sb.meas[vi]
	for ti, t := range m.Tuplets {
		sp := &tupletSpan{voice: vi, index: ti + 1, num: t.Num, den: t.Den}
		for p := t.Parent; p > 0 && p <= len(m.Tuplets); p = m.Tuplets[p-1].Parent {
			sp.depth++
		}
		for _, x := range v {
			if x.grace {
				continue
			}
			for _, c := range x.tuplets {
				if c == ti+1 {
					sp.evs = append(sp.evs, x)
					break
				}
			}
		}
		if len(sp.evs) > 0 {
			sb.tuplets = append(sb.tuplets, sp)
		}
	}
}

// tupletGlyphs are a tuplet's number as glyphs.
func tupletGlyphs(num, den int) []string {
	var out []string
	for _, c := range strconv.Itoa(num) {
		out = append(out, "tuplet"+string(c))
	}
	if den != score.DefaultTupletDen(num) {
		out = append(out, "tupletColon")
		for _, c := range strconv.Itoa(den) {
			out = append(out, "tuplet"+string(c))
		}
	}
	return out
}

// drawTuplets draws a staff bar's tuplets, innermost first.
func (e *engraver) drawTuplets(sc *staffCtx, sb *staffBar) {
	sps := append([]*tupletSpan(nil), sb.tuplets...)
	sort.SliceStable(sps, func(i, j int) bool { return sps[i].depth > sps[j].depth })
	for _, x := range allEvents(sb) {
		x.tupTop, x.tupBot = x.box.Min.Y, x.box.Max.Y
		x.tupInTop, x.tupInBot = math.Inf(1), math.Inf(-1)
		if x.box.Empty() {
			x.tupTop, x.tupBot = 2, 2
		}
	}
	for _, sp := range sps {
		e.drawTuplet(sc, sb, sp)
	}
}

func allEvents(sb *staffBar) []*ev {
	var out []*ev
	for _, v := range sb.voices {
		out = append(out, v...)
	}
	return out
}

func (e *engraver) drawTuplet(sc *staffCtx, sb *staffBar, sp *tupletSpan) {
	evs := sp.evs
	first, last := evs[0], evs[len(evs)-1]
	ref := first.ref
	ref.Note = -1

	// The side: the stems', the voice's in multi-voice music.
	ups, downs := 0, 0
	for _, x := range evs {
		if x.hasStem {
			if x.up {
				ups++
			} else {
				downs++
			}
		}
	}
	above := ups >= downs
	if sb.forced {
		above = sp.voice%2 == 0
	}

	// One beam over exactly these notes: the number by the beam.
	var g *beamGroup
	if first.beam != nil && !first.grace {
		g = first.beam
		for _, x := range evs {
			if x.beam != g {
				g = nil
				break
			}
		}
		if g != nil && (len(g.evs) != len(evs) || g.up != above) {
			g = nil
		}
	}

	gs := tupletGlyphs(sp.num, sp.den)
	nw := e.runWidth(gs)
	nb, _ := e.f.Box("tuplet3")
	mid := (nb.Min.Y + nb.Max.Y) / 2 // the digits' middle, from the baseline
	th := e.d.TupletBracketThickness
	dir := -1.0
	if !above {
		dir = 1
	}
	extreme := func(x *ev) float64 {
		if above {
			return x.tupTop
		}
		return x.tupBot
	}

	if g != nil {
		xa, xb := first.stemX, last.stemX
		xm := (xa + xb) / 2
		yb := g.yAt(xm)
		h := (nb.Max.Y - nb.Min.Y) / 2
		var cy float64 // the number's middle: by the beam, past any inner tuplet
		if above {
			cy = yb - 0.4 - h
			for _, x := range evs {
				cy = math.Min(cy, x.tupInTop-0.4-h)
			}
		} else {
			cy = yb + 0.4 + h
			for _, x := range evs {
				cy = math.Max(cy, x.tupInBot+0.4+h)
			}
		}
		e.drawTupletNumber(sc, first, gs, xm-nw/2, cy-mid)
		top, bot := cy-h, cy+h
		for _, x := range evs {
			x.tupTop, x.tupBot = math.Min(x.tupTop, top), math.Max(x.tupBot, bot)
			x.tupInTop, x.tupInBot = math.Min(x.tupInTop, top), math.Max(x.tupInBot, bot)
		}
		return
	}

	// A bracket: from the first head's left to the last's right.
	l0, _ := headSpan(first)
	_, r1 := headSpan(last)
	xa := first.x + l0 - 0.25
	xb := last.x + r1 + 0.25
	if first.rest {
		xa = first.x + first.restX - 0.25
	}
	if last.rest {
		xb = last.x + last.restX + e.f.Width(restGlyph(last.base)) + 0.25
	}
	if last.hasStem && last.up && above {
		xb = math.Max(xb, last.stemX+0.25)
	}
	clear := 0.9
	ya := extreme(first) + dir*clear
	yb := extreme(last) + dir*clear
	// No steeper than the notes' own line, and level when they are close.
	if xb-xa > 0 {
		s := (yb - ya) / (xb - xa)
		s = math.Max(-0.25, math.Min(0.25, s))
		yb = ya + s*(xb-xa)
	}
	yAt := func(x float64) float64 {
		if xb == xa {
			return ya
		}
		return ya + (yb-ya)*(x-xa)/(xb-xa)
	}
	// Clear everything in between, and stay outside the staff.
	shift := 0.0
	for _, x := range evs {
		need := extreme(x) + dir*clear
		got := yAt(x.x + x.headW/2)
		if above {
			shift = math.Max(shift, got-need)
		} else {
			shift = math.Max(shift, need-got)
		}
	}
	if above {
		shift = math.Max(shift, math.Max(ya, yb)-(-1.0))
	} else {
		shift = math.Max(shift, 5.0-math.Min(ya, yb))
	}
	ya += dir * shift
	yb += dir * shift

	xm := (xa + xb) / 2
	ym := yAt(xm)
	gap := nw/2 + 0.35
	hook := -dir * 0.7
	e.addStaff(sc, Item{Kind: ItemLine, Role: RoleTuplet, Ref: ref, P: Point{X: xa, Y: ya + hook}, Q: Point{X: xa, Y: ya - dir*th/2}, Width: th})
	e.addStaff(sc, Item{Kind: ItemLine, Role: RoleTuplet, Ref: ref, P: Point{X: xa - th/2, Y: ya}, Q: Point{X: xm - gap, Y: yAt(xm - gap)}, Width: th})
	e.addStaff(sc, Item{Kind: ItemLine, Role: RoleTuplet, Ref: ref, P: Point{X: xm + gap, Y: yAt(xm + gap)}, Q: Point{X: xb + th/2, Y: yb}, Width: th})
	e.addStaff(sc, Item{Kind: ItemLine, Role: RoleTuplet, Ref: ref, P: Point{X: xb, Y: yb - dir*th/2}, Q: Point{X: xb, Y: yb + hook}, Width: th})
	e.drawTupletNumber(sc, first, gs, xm-nw/2, ym-mid)
	h := (nb.Max.Y - nb.Min.Y) / 2
	top := math.Min(ya, yb) - h
	bot := math.Max(ya, yb) + h
	for _, x := range evs {
		x.tupTop, x.tupBot = math.Min(x.tupTop, top), math.Max(x.tupBot, bot)
		x.tupInTop, x.tupInBot = math.Min(x.tupInTop, top), math.Max(x.tupInBot, bot)
	}
}

// drawTupletNumber draws a tuplet's number with its left edge at x, its
// baseline at y.
func (e *engraver) drawTupletNumber(sc *staffCtx, x *ev, gs []string, cx, y float64) {
	ref := x.ref
	ref.Note = -1
	e.drawRun(sc, nil, RoleTuplet, ref, gs, cx, y)
}
