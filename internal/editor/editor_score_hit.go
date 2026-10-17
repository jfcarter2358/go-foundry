package editor

// editor_score_hit.go — what the pointer is over in the Score editor, with no
// ImGui in it.
//
// The engraver's display list carries the geometry for this (engrave/list.go):
// staff boxes say what a y means on each staff, bar boxes map x to an offset
// through each bar's columns, and every item names the note it draws. All of
// it is in staff spaces; the view converts the pointer before asking.
//
// A click in note input lands on a beat, not on a pixel: the offset is
// snapped to the onsets already in the voice and to a grid of the current
// value (an eighth at most, a 64th at least), whichever is nearest on
// screen — nearest in x, because spacing is not proportional to time. Grid
// points inside a tuplet are left out; the tuplet's own notes are the places
// to write there.

import (
	"math"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/engrave"
)

// scoreTarget is the place a note-input click would write.
type scoreTarget struct {
	Line, Staff int
	Bar         int
	At          score.Frac
	// Diatonic is the written staff position under the pointer.
	Diatonic int
	// X and Y are where the note would be drawn (staff spaces).
	X, Y float64
	// Tab is a place on a tablature: String (1 the highest) is the string
	// under the pointer, and Diatonic means nothing.
	Tab    bool
	String int
}

// scorePointerTarget maps a point on the layout to a place to write: the
// staff under it (within four spaces), the bar, the snapped offset and the
// staff position. ok is false away from every staff or bar.
func scorePointerTarget(l *engrave.List, s *score.Score, voice int, dur score.Duration, p engrave.Point) (scoreTarget, bool) {
	var t scoreTarget
	sb := l.StaffAt(p.Y)
	if sb == nil || p.Y < sb.Top-4.5 || p.Y > sb.Top+8.5 {
		return t, false
	}
	bb := l.BarAt(p.X)
	if bb == nil {
		return t, false
	}
	t.Line, t.Staff, t.Bar = sb.Line, sb.Staff, bb.Bar
	t.At = scoreSnap(s, t.Line, t.Staff, voice, t.Bar, dur, bb, p.X)
	t.X = bb.XAt(t.At.Float())
	if sb.Tab {
		t.Tab, t.String = true, sb.StringAt(p.Y)
		t.Y = sb.StringY(t.String)
		return t, true
	}
	// The clef in force where the note would go (a clef change before it).
	t.Diatonic = sb.DiatonicAt(t.X, p.Y)
	t.Y = sb.YAt(t.X, t.Diatonic)
	return t, true
}

// scoreSnap picks the offset a click at x means in a bar.
func scoreSnap(s *score.Score, line, staff, voice, bar int, dur score.Duration, bb *engrave.BarBox, x float64) score.Frac {
	barLen := s.BarLength(bar)
	var cands []score.Frac
	type span struct{ a, z score.Frac }
	var tuplets []span
	if v := edit.Voice(s, line, staff, voice); v != nil {
		m := v.Measure(bar)
		offs, _ := m.Offsets(barLen)
		for i, e := range m.Events {
			if e.Grace != score.NotGrace {
				continue
			}
			cands = append(cands, offs[i])
			if e.Tuplet > 0 {
				end := offs[i].Add(m.Actual(e, barLen))
				if n := len(tuplets); n > 0 && !tuplets[n-1].z.Less(offs[i]) {
					tuplets[n-1].z = score.MaxFrac(tuplets[n-1].z, end)
				} else {
					tuplets = append(tuplets, span{offs[i], end})
				}
			}
		}
	}
	g := dur.Value()
	g = score.MaxFrac(score.F(1, 64), score.MinFrac(g, score.F(1, 8)))
	last := barLen.Sub(score.MinFrac(dur.Value(), g))
	for at := (score.Frac{}); !last.Less(at); at = at.Add(g) {
		inside := false
		for _, sp := range tuplets {
			if sp.a.Less(at) && at.Less(sp.z) {
				inside = true
				break
			}
		}
		if !inside {
			cands = append(cands, at)
		}
	}
	best, bd := score.Frac{}, math.Inf(1)
	for _, c := range cands {
		if !c.Less(barLen) {
			continue
		}
		if d := math.Abs(bb.XAt(c.Float()) - x); d < bd {
			best, bd = c, d
		}
	}
	return best
}

// scoreHitEvent is the note or rest under a point: the item (a notehead
// first, then a rest) whose ink is within a third of a space of it,
// nearest first. Faint items are not in the score and are never hit.
func scoreHitEvent(l *engrave.List, p engrave.Point) (engrave.Ref, bool) {
	const reach = 0.35
	best, bestD, bestHead := engrave.Ref{}, math.Inf(1), false
	for i := range l.Items {
		it := &l.Items[i]
		if it.Faint || it.Ref.Voice < 0 || it.Ref.Bar <= 0 || it.Ref.Event < 0 {
			continue
		}
		head := it.Role == engrave.RoleNotehead
		if !head && it.Role != engrave.RoleRest {
			continue
		}
		b := engrave.ItemBounds(it)
		dx := math.Max(0, math.Max(b.Min.X-p.X, p.X-b.Max.X))
		dy := math.Max(0, math.Max(b.Min.Y-p.Y, p.Y-b.Max.Y))
		d := math.Hypot(dx, dy)
		if d > reach {
			continue
		}
		// Distance to the middle breaks ties between overlapping heads.
		cx, cy := (b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2
		d += 0.01 * math.Hypot(p.X-cx, p.Y-cy)
		if (head && !bestHead) || (head == bestHead && d < bestD) {
			best, bestD, bestHead = it.Ref, d, head
		}
	}
	return best, !math.IsInf(bestD, 1)
}

// scoreHitBar is the line and bar under a point: a staff within two spaces
// above or below its lines.
func scoreHitBar(l *engrave.List, p engrave.Point) (line, bar int, ok bool) {
	line, _, bar, ok = scoreHitBarStaff(l, p)
	return line, bar, ok
}

// scoreHitBarStaff is scoreHitBar with the staff of the line as well.
func scoreHitBarStaff(l *engrave.List, p engrave.Point) (line, staff, bar int, ok bool) {
	sb := l.StaffAt(p.Y)
	if sb == nil || p.Y < sb.Top-2.5 || p.Y > sb.BottomY()+2.5 {
		return 0, 0, 0, false
	}
	bb := l.BarAt(p.X)
	if bb == nil {
		return 0, 0, 0, false
	}
	return sb.Line, sb.Staff, bb.Bar, true
}

// scoreRefLoc is a display-list reference as an event address.
func scoreRefLoc(r engrave.Ref) edit.Loc {
	return edit.Loc{Line: r.Line, Staff: r.Staff, Voice: r.Voice, Bar: r.Bar, Event: r.Event}
}

// scoreSameEvent reports an item drawing the event at l.
func scoreSameEvent(r engrave.Ref, l edit.Loc) bool {
	return r.Line == l.Line && r.Staff == l.Staff && r.Voice == l.Voice && r.Bar == l.Bar && r.Event == l.Event
}

// scoreStemUp reports how an event's stem is drawn: up when the stem
// reaches above its heads.
func scoreStemUp(l *engrave.List, loc edit.Loc) (up, ok bool) {
	headTop := math.Inf(1)
	var stem *engrave.Item
	for i := range l.Items {
		it := &l.Items[i]
		if !scoreSameEvent(it.Ref, loc) {
			continue
		}
		switch it.Role {
		case engrave.RoleNotehead:
			headTop = math.Min(headTop, engrave.ItemBounds(it).Min.Y)
		case engrave.RoleStem:
			stem = it
		}
	}
	if stem == nil || math.IsInf(headTop, 1) {
		return false, false
	}
	return math.Min(stem.P.Y, stem.Q.Y) < headTop, true
}

// scoreEventRect is an event's ink on the layout.
func scoreEventRect(l *engrave.List, loc edit.Loc) (engrave.Rect, bool) {
	for _, eb := range l.Events {
		if scoreSameEvent(eb.Ref, loc) {
			return eb.Box, true
		}
	}
	return engrave.Rect{}, false
}

// scoreBarsRect is the box around bars a..z of lines lo..hi: barline to
// barline, a space and a half above the top staff and below the bottom one.
func scoreBarsRect(l *engrave.List, lo, hi, a, z int) (engrave.Rect, bool) {
	ba, bz := l.Bar(a), l.Bar(z)
	if ba == nil || bz == nil {
		return engrave.Rect{}, false
	}
	top, bottom := math.Inf(1), math.Inf(-1)
	for _, sb := range l.Staves {
		if sb.Line < lo || sb.Line > hi {
			continue
		}
		top, bottom = math.Min(top, sb.Top), math.Max(bottom, sb.BottomY())
	}
	if math.IsInf(top, 1) {
		return engrave.Rect{}, false
	}
	return engrave.Rect{Min: engrave.Point{X: ba.X0, Y: top - 1.5}, Max: engrave.Point{X: bz.X1, Y: bottom + 1.5}}, true
}
