package engrave

// tie.go — ties.
//
// A tie curves away from the stem: under a stem-up note, over a stem-down
// one; in multi-voice music up for the upper voices and down for the
// lower; in a chord the upper half ties up, the lower half down and the
// middle note away from the stem. An outer tie (the chord's top tie curving
// up, its bottom one curving down, a single note's) on the side away from
// the stems runs from over or under the notehead to over or under the next;
// any other starts after the heads (and dots) and ends before the next
// heads, a little toward its side. A tie into a bar outside the layout
// runs on to the barline; one coming in from before it starts at the head
// of the first bar.
//
// The shape is a crescent: two cubic Béziers, the outer one higher by the
// font's tieMidpointThickness, meeting at endpoints tieEndpointThickness
// apart. It arches more as it grows longer, up to a space.

import (
	"math"

	"foundry/internal/score/glyph"
)

// tieUp is the way the tie from a chord's i-th note (from the bottom)
// curves.
func tieUp(x *ev, i int) bool {
	if x.forced {
		return x.voice%2 == 0
	}
	n := len(x.notes)
	if n == 1 || n%2 == 1 && i == n/2 {
		return !x.up
	}
	return i >= n/2
}

// drawTies draws every tie on a staff.
func (e *engraver) drawTies(sc *staffCtx) {
	for bi, sb := range sc.bars {
		for _, v := range sb.voices {
			for _, x := range v {
				if x.grace || len(x.notes) == 0 {
					continue
				}
				for i, n := range x.notes {
					up := tieUp(x, i)
					if n.tie {
						if n.tieTo != nil && n.tieEv.col != nil {
							e.drawTie(sc, x, n, n.tieEv, n.tieTo, up)
						} else {
							// To the barline: the next note is past the layout
							// (or missing).
							e.drawTieOut(sc, x, n, up, sb.bar.x1)
						}
					}
					if bi == 0 && n.tieIn && n.tieFrom != nil && n.tieFrom.ev.ref.Bar < 0 {
						e.drawTieIn(sc, x, n, up)
					}
				}
			}
		}
	}
}

// stemSide reports a tie on an event's stem side.
func stemSide(x *ev, up bool) bool { return x.hasStem && x.up == up }

// outerTie reports the chord's outermost tie on its side.
func outerTie(x *ev, n *nt, up bool) bool {
	if up {
		return n == x.notes[len(x.notes)-1]
	}
	return n == x.notes[0]
}

// tieStart is where a tie leaves a note.
func (e *engraver) tieStart(x *ev, n *nt, up bool) Point {
	dir := 1.0
	if up {
		dir = -1
	}
	s := x.size
	if outerTie(x, n, up) && !stemSide(x, up) && x.dots == 0 {
		return Point{X: x.x + n.dx + x.headW*0.62, Y: n.y + dir*0.55*s}
	}
	_, r := headSpan(x)
	xs := x.x + r + 0.15*s
	if x.dots > 0 {
		xs = x.x + x.dotX + float64(x.dots-1)*dotSpacing*s + e.f.Width("augmentationDot")*s + 0.12
	}
	if stemSide(x, up) && x.up {
		xs = math.Max(xs, x.stemX+0.2)
	}
	return Point{X: xs, Y: n.y + dir*0.22*s}
}

// tieEnd is where a tie meets the note it ties into.
func (e *engraver) tieEnd(x *ev, n *nt, up bool) Point {
	dir := 1.0
	if up {
		dir = -1
	}
	s := x.size
	if outerTie(x, n, up) && !stemSide(x, up) {
		return Point{X: x.x + n.dx + x.headW*0.38, Y: n.y + dir*0.55*s}
	}
	l, _ := headSpan(x)
	return Point{X: x.x + l - 0.15*s, Y: n.y + dir*0.22*s}
}

func (e *engraver) drawTie(sc *staffCtx, a *ev, na *nt, b *ev, nb *nt, up bool) {
	p0 := e.tieStart(a, na, up)
	p1 := e.tieEnd(b, nb, up)
	if p1.X-p0.X < 0.6 {
		p1.X = p0.X + 0.6
	}
	ref := a.ref
	ref.Note = na.idx
	e.add(sc, a, Item{Kind: ItemPath, Role: RoleTie, Ref: ref, Path: e.tiePath(p0, p1, up)})
}

func (e *engraver) drawTieOut(sc *staffCtx, a *ev, na *nt, up bool, barX float64) {
	p0 := e.tieStart(a, na, up)
	p1 := Point{X: math.Max(barX-0.3, p0.X+1.2), Y: p0.Y}
	ref := a.ref
	ref.Note = na.idx
	e.add(sc, a, Item{Kind: ItemPath, Role: RoleTie, Ref: ref, Path: e.tiePath(p0, p1, up)})
}

func (e *engraver) drawTieIn(sc *staffCtx, b *ev, nb *nt, up bool) {
	p1 := e.tieEnd(b, nb, up)
	p0 := Point{X: math.Min(b.sb.bar.content+0.2, p1.X-1.2), Y: p1.Y}
	ref := b.ref
	ref.Note = nb.idx
	e.add(sc, b, Item{Kind: ItemPath, Role: RoleTie, Ref: ref, Path: e.tiePath(p0, p1, up)})
}

// tiePath is a tie's crescent from p0 to p1.
func (e *engraver) tiePath(p0, p1 Point, up bool) []glyph.Segment {
	return crescent(p0, p1, up, tieHeight(p1.X-p0.X), e.d.TieMidpointThickness, e.d.TieEndpointThickness)
}

// tieHeight is how high a tie of a length arches.
func tieHeight(l float64) float64 { return math.Min(1.0, math.Max(0.35, 0.22+0.09*l)) }

// crescent is the filled shape between two arcs from p0 to p1 bulging
// toward up (y down: up is negative y) by h at the middle, mid thick there
// and end thick at the ends.
func crescent(p0, p1 Point, up bool, h, mid, end float64) []glyph.Segment {
	dir := 1.0
	if up {
		dir = -1
	}
	dx, dy := p1.X-p0.X, p1.Y-p0.Y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return nil
	}
	// The normal toward the bulge.
	nx, ny := -dy/l, dx/l
	if ny*dir < 0 {
		nx, ny = -nx, -ny
	}
	at := func(t, off float64) Point { return Point{X: p0.X + dx*t + nx*off, Y: p0.Y + dy*t + ny*off} }
	ko := (h + mid/2) / 0.75 // a cubic's apex is 3/4 of its control points' height
	ki := (h - mid/2) / 0.75
	return []glyph.Segment{
		{Op: glyph.MoveTo, P: [3]Point{at(0, end/2)}},
		{Op: glyph.CubeTo, P: [3]Point{at(0.25, ko), at(0.75, ko), at(1, end/2)}},
		{Op: glyph.LineTo, P: [3]Point{at(1, -end/2)}},
		{Op: glyph.CubeTo, P: [3]Point{at(0.75, ki), at(0.25, ki), at(0, -end/2)}},
	}
}
