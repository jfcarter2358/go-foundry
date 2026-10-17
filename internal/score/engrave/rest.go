package engrave

// rest.go — rests.
//
// A rest's glyph is placed by its SMuFL origin: a whole rest hangs from the
// fourth line, a half rest sits on the middle line, a breve rest fills the
// space above it, and the shorter rests are centred on it. In multi-voice
// music a rest moves out of the way: up for the upper voices, down for the
// lower, two spaces (whole and half rests one, so they stay on a line),
// and further while it still overlaps another voice's notes at the
// same onset.

import (
	"math"

	"foundry/internal/score"
	"foundry/internal/score/glyph"
)

// restGlyph is the rest for a value.
func restGlyph(b score.DurBase) string {
	switch b {
	case score.Breve:
		return "restDoubleWhole"
	case score.WholeNote:
		return "restWhole"
	case score.Half:
		return "restHalf"
	case score.Quarter:
		return "restQuarter"
	case score.Eighth:
		return "rest8th"
	case score.D16:
		return "rest16th"
	case score.D32:
		return "rest32nd"
	case score.D64:
		return "rest64th"
	case score.D128:
		return "rest128th"
	}
	return "rest256th"
}

// restHome is a rest's origin y alone on a staff.
func restHome(b score.DurBase) float64 {
	if b == score.WholeNote {
		return 1
	}
	return 2
}

// restStep is how far a rest moves when it gets out of the way: whole and
// half rests a space at a time, so they still hang from or sit on a line.
func restStep(b score.DurBase) float64 {
	if b == score.WholeNote || b == score.Half || b == score.Breve {
		return 1
	}
	return 0.5
}

// placeRest sets a rest's x (centred on a normal notehead) and y, clear of
// the other voices' notes and of the rests already placed at the column (a
// third voice's rest goes beyond the first's). away are the staff's own
// chords at the column written on another staff (cross.go), their notes in
// this staff's coordinates: a stem of theirs may reach into it.
func (e *engraver) placeRest(x *ev, chords, away, placed []*ev, forced bool) {
	g := restGlyph(x.base)
	b, _ := e.f.Box(g)
	x.restX = (e.f.Width("noteheadBlack") - b.W()) / 2
	y := restHome(x.base)
	// Notes another staff writes here, and this staff's written there,
	// are no voice of this staff's: a rest never moves over or under them
	// (it would leave the staff for the other one) but stands beside them.
	var foreign []*ev
	for _, c := range chords {
		if c.sb.st != x.sb.st {
			foreign = append(foreign, c)
		}
	}
	for _, c := range away {
		e.arrangeHeads(c) // their staff may not have been arranged yet
		foreign = append(foreign, c)
	}
	if !forced || x.e.Has("up") || x.e.Has("down") {
		if x.e.Has("up") {
			y -= 2
		} else if x.e.Has("down") {
			y += 2
		}
		x.restY = y
		e.besideForeign(x, foreign, b)
		return
	}
	up := x.voice%2 == 0
	dir := 1.0
	if up {
		dir = -1
	}
	switch x.base {
	case score.WholeNote, score.Half, score.Breve:
		y += dir // a space, still on a line; further only to clear notes
	default:
		y += dir * 2
	}
	// A rest where another voice stemmed its way (voices 1 and 3, 2 and
	// 4) has a stemmed note stands beside that note, right of its stem, as
	// the voice's own notes would (voiceShift): stacked over the stem it
	// sat at the stem's tip, into the beam or the flag, and at a different
	// height at every note. Beside it, only heads it would cover move it.
	var beside *ev
	for _, c := range chords {
		if c.sb.st == x.sb.st && c.voice != x.voice && c.voice%2 == x.voice%2 && c.hasStem && c.up == up && len(c.notes) > 0 {
			if beside == nil || c.shift > beside.shift {
				beside = c
			}
		}
	}
	if beside != nil {
		_, r := headSpan(beside)
		x.restX = beside.shift + r + sameWayGap*beside.size
		for _, c := range chords {
			// Past the column's dots (one column for every voice).
			if c.dots > 0 {
				end := c.shift + c.dotX + float64(c.dots-1)*dotSpacing*c.size + e.f.Width("augmentationDot")*c.size
				x.restX = math.Max(x.restX, end+dotGap)
			}
		}
	}
	own := chords[:0:0]
	for _, c := range chords {
		if c.sb.st == x.sb.st {
			own = append(own, c)
		}
	}
	x.restY = e.clearRest(x, b, own, placed, y, dir, beside != nil)
	if e.besideForeign(x, foreign, b) {
		// Moved right, the rest goes back as near its voice's place as
		// this staff's own notes there let it.
		x.restY = e.clearRest(x, b, own, placed, y, dir, true)
	}
}

// clearRest walks a rest from y, a step at a time in dir, until it is
// clear of the other voices' heads at its onset and their stems where they
// point the way it moves, and of the rests already placed. beside means
// the rest stands right of the column's stems: a head then counts only
// where it is.
func (e *engraver) clearRest(x *ev, b glyph.Rect, chords, placed []*ev, y, dir float64, beside bool) float64 {
	up := dir < 0
	x0, x1 := x.restX, x.restX+b.W()
	for tries := 0; tries < 16; tries++ {
		top, bot := y+b.Min.Y, y+b.Max.Y
		clash := false
		for _, c := range chords {
			if c.voice == x.voice {
				continue
			}
			for _, n := range c.notes {
				if beside {
					if hx := c.shift + n.dx; hx > x1+0.1 || hx+c.headW < x0-0.1 {
						continue
					}
				}
				if n.y+0.5 > top+0.1 && n.y-0.5 < bot-0.1 {
					clash = true
				}
			}
			if beside {
				continue // the stems are left of it
			}
			if c.hasStem && c.up == up && len(c.notes) > 0 {
				low, high := c.notes[0].y, c.notes[len(c.notes)-1].y // y down: low is the larger
				s0, s1 := high, low+stemLength*c.size
				if up {
					s0, s1 = high-stemLength*c.size, low
				}
				if s1 > top+0.1 && s0 < bot-0.1 {
					clash = true
				}
			}
		}
		for _, r := range placed {
			if r.voice == x.voice {
				continue
			}
			rb, _ := e.f.Box(restGlyph(r.base))
			if r.restY+rb.Max.Y > top+0.1 && r.restY+rb.Min.Y < bot-0.1 {
				clash = true
			}
		}
		if !clash {
			break
		}
		y += dir * restStep(x.base)
	}
	return math.Round(y*2) / 2
}

// besideForeign moves a rest right of the notes of another staff at its
// column (foreign) when it would touch their heads or their stem, which
// reaches toward this staff: under or over them would put it on the other
// staff. It reports whether the rest moved.
func (e *engraver) besideForeign(x *ev, foreign []*ev, b glyph.Rect) bool {
	top, bot := x.restY+b.Min.Y, x.restY+b.Max.Y
	right, hit := 0.0, false
	for _, c := range foreign {
		if len(c.notes) == 0 {
			continue
		}
		clash := false
		for _, n := range c.notes {
			if n.y+0.5 > top+0.1 && n.y-0.5 < bot-0.1 {
				clash = true
			}
		}
		_, r := headSpan(c)
		if c.hasStem {
			low, high := c.notes[0].y, c.notes[len(c.notes)-1].y
			s0, s1 := high, low+stemLength*c.size
			if c.up {
				s0, s1 = high-stemLength*c.size, low
			}
			if s1 > top+0.1 && s0 < bot-0.1 {
				clash = true
			}
			// An up stem's flag reaches right of the heads.
			if c.up && c.beam == nil && c.base >= score.Eighth {
				r = math.Max(r, c.headW-e.d.StemThickness*c.size+e.f.Width(flagGlyph(c.base, true))*c.size)
			}
		}
		if clash {
			hit = true
			right = math.Max(right, c.shift+r+sameWayGap*c.size)
		}
	}
	if !hit || right <= x.restX {
		return false
	}
	x.restX = right
	return true
}

// barRestY moves a bar rest out of the other voices' way: from its
// voice's place in multi-voice music, a space at a time — up for the upper
// voices, down for the lower — while it touches a head, stem, flag or rest
// of theirs that stands under or beside it (rx0 to rx1). A bar rest alone
// on its staff moves only for the notes another staff writes there, away
// from them; a rest is never in a column, so placeRest never sees it.
func (e *engraver) barRestY(sc *staffCtx, x *ev, g string, rx0, rx1, y float64) float64 {
	b, _ := e.f.Box(g)
	var ink []inkRect
	// A space's margin: a rest at a note's height just past it still
	// reads as that note's voice.
	add := func(r inkRect) {
		if r.x1 > rx0-barRestMargin && r.x0 < rx1+barRestMargin {
			ink = append(ink, r)
		}
	}
	for vi, v := range x.sb.voices {
		if vi == x.voice {
			continue
		}
		for _, c := range v {
			switch {
			case c.col == nil || c.space || c.barRest:
			case len(c.notes) > 0:
				for _, r := range e.chordInk(c, 0) {
					add(r)
				}
			case c.rest:
				rb, _ := e.f.Box(restGlyph(c.base))
				cx := c.x + c.restX
				add(inkRect{cx + rb.Min.X, cx + rb.Max.X, c.restY + rb.Min.Y, c.restY + rb.Max.Y})
			}
		}
	}
	if bi := x.sb.bar.bar - e.from; bi >= 0 && bi < len(e.bars) {
		for _, c := range e.crossedInto(sc, bi) {
			if c.col != nil {
				for _, r := range e.chordInk(c, -c.crossY) {
					add(r)
				}
			}
		}
	}
	clash := func(y float64) bool {
		top, bot := y+b.Min.Y, y+b.Max.Y
		for _, r := range ink {
			if r.bot > top-0.05 && r.top < bot+0.05 {
				return true
			}
		}
		return false
	}
	if !clash(y) {
		return y
	}
	dir := 1.0
	if x.sb.forced {
		if x.voice%2 == 0 {
			dir = -1
		}
	} else {
		// Away from what is there.
		mid, n := 0.0, 0.0
		for _, r := range ink {
			mid, n = mid+(r.top+r.bot)/2, n+1
		}
		if mid/n > y+(b.Min.Y+b.Max.Y)/2 {
			dir = -1
		}
	}
	for tries := 0; tries < 16 && clash(y); tries++ {
		y += dir
	}
	return y
}

// barRestMargin is how far either side of a bar rest another voice's ink
// still moves it.
const barRestMargin = 1.0

// inkRect is a box of ink a bar rest keeps clear of (staff-local y).
type inkRect struct{ x0, x1, top, bot float64 }

// chordInk is a placed chord's heads and its stem with its flag or its
// beam's end, dy moving them into another staff's coordinates.
func (e *engraver) chordInk(c *ev, dy float64) []inkRect {
	var out []inkRect
	for _, n := range c.notes {
		hx := c.x + n.dx
		out = append(out, inkRect{hx, hx + c.headW, n.y - 0.5*c.size + dy, n.y + 0.5*c.size + dy})
	}
	if !c.hasStem || len(c.notes) == 0 {
		return out
	}
	sx := e.stemX(c)
	base := e.stemBase(c)
	tip := 0.0
	x1 := sx + e.d.StemThickness*c.size
	if c.beam != nil {
		e.layoutBeam(c.beam) // it only reads the placed x; drawBeam lays it out again
		tip = c.beam.yAt(sx)
	} else {
		tip = e.stemTip(c)
		if c.base >= score.Eighth {
			x1 = sx + e.f.Width(flagGlyph(c.base, c.up))*c.size
		}
	}
	out = append(out, inkRect{sx - e.d.StemThickness*c.size, x1, math.Min(base, tip) + dy, math.Max(base, tip) + dy})
	return out
}

// legerRest is a rest's glyph where it stands: a whole or half rest off
// the staff hangs from (or sits on) a short ledger line of its own.
func legerRest(g string, y float64) string {
	if y >= -0.01 && y <= 4.01 {
		return g
	}
	switch g {
	case "restWhole":
		return "restWholeLegerLine"
	case "restHalf":
		return "restHalfLegerLine"
	}
	return g
}

// restDotY is where a rest's dot goes: in the space its body is in.
func restDotY(x *ev) float64 {
	if x.base == score.WholeNote {
		return x.restY + 0.5
	}
	return x.restY - 0.5
}
