package engrave

// chord.go — one column of one staff: noteheads, seconds, voices side by
// side, accidentals, dots, rests and grace notes, placed relative to the
// column's x before the bar is spaced.
//
// Noteheads sit with their left edge on the column. In a chord a second
// (or a unison) cannot share a side of the stem: walking from the stem's
// end of the chord (the bottom for a stem up), a note a step from one on
// the normal side goes on the other, past the stem. Two voices at one
// onset sit side by side when their notes are a second apart or cross —
// the lower voice moves right — and share a head when they meet on a
// unison of the same value.
//
// Accidentals for every voice at the column stack together (stackAcc), to
// the left of the leftmost head, then the column's grace notes to the left
// of those.

import (
	"math"
	"sort"

	"foundry/internal/score"
)

// headGlyph is the notehead for a value.
func headGlyph(b score.DurBase) string {
	switch {
	case b <= score.Breve:
		return "noteheadDoubleWhole"
	case b == score.WholeNote:
		return "noteheadWhole"
	case b == score.Half:
		return "noteheadHalf"
	}
	return "noteheadBlack"
}

// DrumHeadGlyph is the notehead a percussion staff draws a drum's shape
// with at a value: what an editor's ghost note shows.
func DrumHeadGlyph(h score.DrumHead, b score.DurBase) string { return drumHeadGlyph(h, b) }

// drumHeadGlyph is a percussion notehead shape for a value.
func drumHeadGlyph(h score.DrumHead, b score.DurBase) string {
	var name string
	switch h {
	case score.DrumHeadX:
		name = "noteheadX"
	case score.DrumHeadCircleX:
		name = "noteheadCircleX"
	case score.DrumHeadDiamond:
		name = "noteheadDiamond"
	case score.DrumHeadTriangle:
		name = "noteheadTriangleUp"
	default:
		return headGlyph(b)
	}
	switch {
	case b <= score.Breve:
		return name + "DoubleWhole"
	case b == score.WholeNote:
		return name + "Whole"
	case b == score.Half:
		return name + "Half"
	case h == score.DrumHeadCircleX:
		return name // SMuFL's black circle-x has no suffix
	}
	return name + "Black"
}

// headOrigin is how far left of the head's oval its glyph starts (a breve's
// side bars), at size 1.
func (e *engraver) headOrigin(head string) float64 {
	if p, ok := e.f.Anchor(head, "noteheadOrigin"); ok {
		return p.X
	}
	return 0
}

// accGlyph is the accidental for an alteration.
func accGlyph(alter int) string {
	switch alter {
	case -2:
		return "accidentalDoubleFlat"
	case -1:
		return "accidentalFlat"
	case 1:
		return "accidentalSharp"
	case 2:
		return "accidentalDoubleSharp"
	}
	return "accidentalNatural"
}

// arrangeHeads sets each note's offset. Walking from the stem's end of the
// chord, each note takes the first column — the normal side, then past the
// stem, then (for an altered unison beside a second, E Fb F) one further —
// where it is more than a step from the last note already there. A cluster
// C D E F alternates sides, as it should.
func (e *engraver) arrangeHeads(x *ev) {
	n := x.notes
	if len(n) == 0 {
		return
	}
	off := x.headW - e.d.StemThickness*x.size
	if !x.hasStem {
		off = x.headW
	}
	down := x.hasStem && !x.up
	if down {
		off = -off
	}
	var last []int // per column, the last staff position placed there
	for k := range n {
		i := k
		if down {
			i = len(n) - 1 - k
		}
		col := 0
		for ; col < len(last); col++ {
			if absInt(n[i].pos-last[col]) > 1 {
				break
			}
		}
		if col == len(last) {
			last = append(last, 0)
		}
		last[col] = n[i].pos
		n[i].dx = float64(col) * off
	}
}

// headSpan is a chord's heads, left and right, relative to its x.
func headSpan(x *ev) (l, r float64) {
	if len(x.notes) == 0 {
		return 0, x.headW
	}
	l, r = math.Inf(1), math.Inf(-1)
	for _, n := range x.notes {
		l, r = math.Min(l, n.dx), math.Max(r, n.dx+x.headW)
	}
	return l, r
}

// lowHigh are a chord's lowest and highest staff positions.
func lowHigh(x *ev) (int, int) {
	return x.notes[0].pos, x.notes[len(x.notes)-1].pos
}

// arrangeColumn places one staff's events at a column and says how far
// their ink reaches left and right of the column's x.
func (e *engraver) arrangeColumn(sb *staffBar, c *column, evs []*ev) (left, right float64) {
	var chords, rests []*ev
	graces := map[int][]*ev{}
	for _, x := range evs {
		switch {
		case x.grace:
			if len(x.notes) > 0 {
				graces[x.voice] = append(graces[x.voice], x)
			}
		case len(x.notes) > 0:
			chords = append(chords, x)
		case x.rest:
			rests = append(rests, x)
		}
	}
	lo, hi := 0.0, 0.0
	have := false
	ink := func(l, r float64) {
		if !have {
			lo, hi, have = l, r, true
			return
		}
		lo, hi = math.Min(lo, l), math.Max(hi, r)
	}

	// Heads, and voices side by side.
	sort.SliceStable(chords, func(i, j int) bool { return chords[i].voice < chords[j].voice })
	for i, x := range chords {
		e.arrangeHeads(x)
		x.shift = 0
		for _, p := range chords[:i] {
			if s, ok := e.voiceShift(p, x); ok {
				x.shift = math.Max(x.shift, s)
			}
		}
	}
	headsLeft, headsRight := 0.0, 0.0
	for _, x := range chords {
		l, r := headSpan(x)
		headsLeft = math.Min(headsLeft, x.shift+l)
		headsRight = math.Max(headsRight, x.shift+r)
	}
	for _, x := range chords {
		l, r := headSpan(x)
		ink(x.shift+l, x.shift+r)
		// Dots: one column for the whole column, after every voice's heads.
		if x.dots > 0 {
			x.dotX = headsRight - x.shift + dotGap*x.size
			e.dotRows(x)
			ink(x.shift+x.dotX, x.shift+x.dotX+float64(x.dots-1)*dotSpacing+e.f.Width("augmentationDot"))
		}
		// A flag on an up stem reaches right of the head.
		if x.hasStem && x.up && x.beam == nil && x.base >= score.Eighth {
			sx := x.headW - e.d.StemThickness
			ink(x.shift, x.shift+sx+e.f.Width(flagGlyph(x.base, true)))
		}
	}

	// Accidentals, all voices together.
	var accs []*accItem
	for _, x := range chords {
		for _, n := range x.notes {
			if n.acc {
				accs = append(accs, e.newAcc(n, 1))
			}
		}
	}
	accLeft := headsLeft
	if len(accs) > 0 {
		accLeft = stackAcc(accs, headsLeft-accGap)
		ink(accLeft, accLeft)
	}

	// An arpeggio line left of the chord and its accidentals.
	arpEdge := math.Min(accLeft, headsLeft)
	for _, x := range chords {
		if ok, _ := arpeggio(x); ok {
			x.arpX = math.Min(accLeft, headsLeft) - arpGap - arpWidth
			arpEdge = math.Min(arpEdge, x.arpX)
			ink(x.arpX, x.arpX)
		}
	}

	// Rests, out of the other voices' way (and of each other); a rest's
	// dot clear of the chords' dots.
	var placed []*ev
	for _, x := range rests {
		e.placeRest(x, chords, placed, sb.forced)
		placed = append(placed, x)
		w := e.f.Width(restGlyph(x.base))
		ink(x.restX, x.restX+w)
		if x.dots > 0 {
			x.restDotY = restDotY(x)
			dx0 := x.restX + w + dotGap
			dx1 := dx0 + float64(x.dots-1)*dotSpacing + e.f.Width("augmentationDot")
			dir := -1.0
			if x.voice%2 == 1 {
				dir = 1
			}
			if headClash(chords, x.restDotY, dx0, dx1) {
				// Out to the column's dot line, after every head.
				dx1 += headsRight + dotGap - dx0
				dx0 = headsRight + dotGap
			}
			x.restDotX = dx0 - x.restX
			for tries := 0; tries < 6 && e.dotClash(chords, x.restDotY, dx0, dx1); tries++ {
				x.restDotY += dir
			}
			ink(x.restX, dx1)
		}
	}

	// Grace notes, leftwards from everything else.
	edge := arpEdge
	if len(chords) == 0 && len(rests) == 0 && c.end {
		edge = 0
	}
	// Each voice's run left of the one before, so two voices' graces at
	// one onset never meet.
	vs := make([]int, 0, len(graces))
	for v := range graces {
		vs = append(vs, v)
	}
	sort.Ints(vs)
	for _, v := range vs {
		l := e.layoutGraces(graces[v], edge-graceGap)
		ink(l, l)
		edge = l + 0.3 - graceGap/2
	}
	if !have {
		return 0, 0
	}
	return math.Max(0, -lo), math.Max(0, hi)
}

// dotClash reports a chord's dot at row y between x0 and x1 (relative to
// the column).
func (e *engraver) dotClash(chords []*ev, y, x0, x1 float64) bool {
	w := e.f.Width("augmentationDot")
	for _, c := range chords {
		for _, row := range c.dotRows {
			cy := 4 - float64(row)/2
			cx0 := c.shift + c.dotX
			cx1 := cx0 + float64(c.dots-1)*dotSpacing*c.size + w*c.size
			if math.Abs(cy-y) < 0.45 && cx0 < x1+0.1 && x0 < cx1+0.1 {
				return true
			}
		}
	}
	return false
}

// headClash reports a notehead at row y between x0 and x1.
func headClash(chords []*ev, y, x0, x1 float64) bool {
	for _, c := range chords {
		for _, n := range c.notes {
			hx := c.shift + n.dx
			if math.Abs(n.y-y) < 0.7 && hx < x1+0.1 && x0 < hx+c.headW+0.1 {
				return true
			}
		}
	}
	return false
}

// voiceShift is how far right chord x must move to clear chord p of an
// earlier voice at the same onset, and whether it must.
func (e *engraver) voiceShift(p, x *ev) (float64, bool) {
	pl, ph := lowHigh(p)
	xl, xh := lowHigh(x)
	if xl > ph+1 || xh < pl-1 {
		return 0, false // a third or more apart
	}
	// A unison of the same value on the outer notes shares a head.
	if p.up && !x.up && xh == pl && x.notes[len(x.notes)-1].p == p.notes[0].p &&
		x.head == p.head && x.dots == p.dots {
		return 0, false
	}
	_, pr := headSpan(p)
	xl0, _ := headSpan(x)
	return p.shift + pr - xl0, true
}

// dotRows puts a dot in a space beside each note: a note on a line takes
// the space above (below for a lower voice), and no two notes share one.
func (e *engraver) dotRows(x *ev) {
	taken := map[int]bool{}
	x.dotRows = x.dotRows[:0]
	down := x.forced && x.voice%2 == 1
	for i := len(x.notes) - 1; i >= 0; i-- {
		p := x.notes[i].pos
		if p%2 == 0 {
			if down {
				p--
			} else {
				p++
			}
		}
		for taken[p] {
			p -= 2
		}
		taken[p] = true
		x.dotRows = append(x.dotRows, p)
	}
}

// ---------- accidentals ----------

// accItem is one accidental being placed.
type accItem struct {
	n        *nt
	name     string
	size     float64
	w        float64 // with any parentheses
	top, bot float64
	x        float64 // left edge, relative to the column
}

func (e *engraver) newAcc(n *nt, size float64) *accItem {
	a := &accItem{n: n, name: accGlyph(int(n.p.Alter)), size: size}
	b, _ := e.f.Box(a.name)
	a.w = b.W() * size
	a.top, a.bot = n.y+b.Min.Y*size, n.y+b.Max.Y*size
	if n.parens {
		pl, _ := e.f.Box("accidentalParensLeft")
		pr, _ := e.f.Box("accidentalParensRight")
		a.w += (pl.W() + pr.W()) * size
		a.top = math.Min(a.top, n.y+pl.Min.Y*size)
		a.bot = math.Max(a.bot, n.y+pl.Max.Y*size)
	}
	return a
}

// stackAcc places accidentals right-aligned against right in as few
// columns as do not collide, taking them top, bottom, second from the top,
// second from the bottom… (the order Behind Bars gives), and returns the
// leftmost edge.
func stackAcc(accs []*accItem, right float64) float64 {
	sort.SliceStable(accs, func(i, j int) bool { return accs[i].n.y < accs[j].n.y })
	order := make([]*accItem, 0, len(accs))
	for i, j := 0, len(accs)-1; i <= j; i, j = i+1, j-1 {
		order = append(order, accs[i])
		if i != j {
			order = append(order, accs[j])
		}
	}
	var cols [][]*accItem
	for _, a := range order {
		placed := false
		for ci := range cols {
			clash := false
			for _, b := range cols[ci] {
				if a.top < b.bot-0.05 && b.top < a.bot-0.05 {
					clash = true
					break
				}
			}
			if !clash {
				cols[ci] = append(cols[ci], a)
				placed = true
				break
			}
		}
		if !placed {
			cols = append(cols, []*accItem{a})
		}
	}
	edge := right
	for _, col := range cols {
		w := 0.0
		for _, a := range col {
			w = math.Max(w, a.w)
		}
		for _, a := range col {
			a.x = edge - a.w
			a.n.accX = a.x
		}
		edge -= w + accColumnGap
	}
	return edge + accColumnGap
}

// ---------- grace notes ----------

// layoutGraces sets one voice's grace notes before a column, right to left
// from right (relative to the column), and returns their left edge.
func (e *engraver) layoutGraces(gs []*ev, right float64) float64 {
	edge := right
	beamed := len(gs) > 1 && gs[0].beam != nil
	for i := len(gs) - 1; i >= 0; i-- {
		g := gs[i]
		e.arrangeHeads(g)
		l, r := headSpan(g)
		w := r
		if g.hasStem && g.up && g.beam == nil && g.base >= score.Eighth {
			w = math.Max(w, g.headW-e.d.StemThickness*g.size+e.f.Width(flagGlyph(g.base, true))*g.size)
		}
		if g.dots > 0 {
			g.dotX = r + dotGap*g.size
			e.dotRows(g)
			w = math.Max(w, g.dotX+float64(g.dots)*dotSpacing*g.size)
		}
		g.shift = edge - w
		left := g.shift + l
		var accs []*accItem
		for _, n := range g.notes {
			if n.acc {
				accs = append(accs, e.newAcc(n, g.size))
			}
		}
		if len(accs) > 0 {
			// Relative to the grace's own x, then the left edge back to
			// the column's frame.
			left = math.Min(left, g.shift+stackAcc(accs, l-accGap*g.size))
		}
		gap := 0.25
		if beamed {
			gap = 0.35
		}
		edge = left - gap
	}
	return edge
}
