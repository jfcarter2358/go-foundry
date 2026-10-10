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

// placeRest sets a rest's x (centred on a normal notehead) and y.
func (e *engraver) placeRest(x *ev, chords []*ev, forced bool) {
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
	// Clear the other voices' heads at this onset.
	for tries := 0; tries < 8; tries++ {
		top, bot := y+b.Min.Y, y+b.Max.Y
		clash := false
		for _, c := range chords {
			if c.voice == x.voice {
				continue
			}
			for _, n := range c.notes {
				if n.y+0.5 > top+0.1 && n.y-0.5 < bot-0.1 {
					clash = true
				}
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
