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
// third voice's rest goes beyond the first's).
func (e *engraver) placeRest(x *ev, chords, placed []*ev, forced bool) {
	g := restGlyph(x.base)
	b, _ := e.f.Box(g)
	x.restX = (e.f.Width("noteheadBlack") - b.W()) / 2
	y := restHome(x.base)
	if !forced || x.e.Has("up") || x.e.Has("down") {
		if x.e.Has("up") {
			y -= 2
		} else if x.e.Has("down") {
			y += 2
		}
		x.restY = y
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
		if c.voice != x.voice && c.voice%2 == x.voice%2 && c.hasStem && c.up == up && len(c.notes) > 0 {
			if beside == nil || c.shift > beside.shift {
				beside = c
			}
		}
	}
	x0, x1 := x.restX, x.restX+b.W()
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
		x0, x1 = x.restX, x.restX+b.W()
	}
	// Clear the other voices' heads at this onset, and their stems where
	// they point the way the rest moves (a third voice's, beside the first).
	for tries := 0; tries < 16; tries++ {
		top, bot := y+b.Min.Y, y+b.Max.Y
		clash := false
		for _, c := range chords {
			if c.voice == x.voice {
				continue
			}
			for _, n := range c.notes {
				if beside != nil {
					// Beside a stem, a head counts where it is.
					if hx := c.shift + n.dx; hx > x1+0.1 || hx+c.headW < x0-0.1 {
						continue
					}
				}
				if n.y+0.5 > top+0.1 && n.y-0.5 < bot-0.1 {
					clash = true
				}
			}
			if beside != nil {
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
	x.restY = math.Round(y*2) / 2
}

// restDotY is where a rest's dot goes: in the space its body is in.
func restDotY(x *ev) float64 {
	if x.base == score.WholeNote {
		return x.restY + 0.5
	}
	return x.restY - 0.5
}
