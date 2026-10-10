package editor

import (
	"math"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/engrave"
)

// scoreHitScore is two lines of 4/4: a flute with a melody and a triplet in
// bar 2, and an empty cello.
func scoreHitScore(t *testing.T) *score.Score {
	t.Helper()
	s := score.New("t")
	s.Bars = 3
	fl := score.NewLine("Flute", "woodwinds/flute", "treble")
	v, err := score.ParseVoice("| qE4 qE5 hG5+B5 | 3{eD5 eE5 eF5} qG5 hr | R |")
	if err != nil {
		t.Fatal(err)
	}
	fl.Staves[0].Voices = []*score.Voice{v}
	s.Lines = []*score.Line{fl, score.NewLine("Cello", "strings/cello_section", "bass")}
	return s
}

// itemCentre is the middle of the first item drawing an event's note.
func itemCentre(t *testing.T, l *engrave.List, role engrave.Role, loc edit.Loc, note int) engrave.Point {
	t.Helper()
	for i := range l.Items {
		it := &l.Items[i]
		if it.Role == role && scoreSameEvent(it.Ref, loc) && (note < 0 || it.Ref.Note == note) {
			b := engrave.ItemBounds(it)
			return engrave.Point{X: (b.Min.X + b.Max.X) / 2, Y: (b.Min.Y + b.Max.Y) / 2}
		}
	}
	t.Fatalf("no %v item for %+v note %d", role, loc, note)
	return engrave.Point{}
}

func TestScoreHitNotesAndRests(t *testing.T) {
	s := scoreHitScore(t)
	l := engrave.Engrave(s, engrave.Options{BarNumbers: -1})
	// The B5 of the chord in bar 1.
	loc := edit.Loc{Bar: 1, Event: 2}
	p := itemCentre(t, l, engrave.RoleNotehead, loc, 1)
	ref, ok := scoreHitEvent(l, p)
	if !ok || !scoreSameEvent(ref, loc) || ref.Note != 1 {
		t.Errorf("hit B5 = %+v %v", ref, ok)
	}
	// A little off the head still finds it.
	ref, ok = scoreHitEvent(l, engrave.Point{X: p.X + 0.5, Y: p.Y + 0.2})
	if !ok || !scoreSameEvent(ref, loc) {
		t.Errorf("near hit = %+v %v", ref, ok)
	}
	// The rest in bar 2.
	rl := edit.Loc{Bar: 2, Event: 4}
	ref, ok = scoreHitEvent(l, itemCentre(t, l, engrave.RoleRest, rl, -1))
	if !ok || !scoreSameEvent(ref, rl) || ref.Note != -1 {
		t.Errorf("rest = %+v %v", ref, ok)
	}
	// Empty paper above the staff, and the cello's implied bar rest (faint,
	// not in the score), hit nothing.
	if _, ok := scoreHitEvent(l, engrave.Point{X: p.X, Y: l.Staves[0].Top - 6}); ok {
		t.Error("hit empty paper")
	}
	for i := range l.Items {
		it := &l.Items[i]
		if it.Faint {
			b := engrave.ItemBounds(it)
			if ref, ok := scoreHitEvent(l, engrave.Point{X: (b.Min.X + b.Max.X) / 2, Y: (b.Min.Y + b.Max.Y) / 2}); ok && ref.Line == it.Ref.Line {
				t.Errorf("hit a faint item: %+v", ref)
			}
		}
	}
	// Stems: the low E4 is up, the high chord down.
	if up, ok := scoreStemUp(l, edit.Loc{Bar: 1, Event: 0}); !ok || !up {
		t.Errorf("E4 stem up = %v %v", up, ok)
	}
	if up, ok := scoreStemUp(l, loc); !ok || up {
		t.Errorf("G5+B5 stem up = %v %v", up, ok)
	}
}

func TestScorePointerTarget(t *testing.T) {
	s := scoreHitScore(t)
	l := engrave.Engrave(s, engrave.Options{BarNumbers: -1})
	q := score.Duration{Base: score.Quarter}
	bb := l.Bar(1)
	sb := l.Staves[0]
	// On the E5 of bar 1, on the E line's y: the E5's onset, the diatonic E5.
	e5 := itemCentre(t, l, engrave.RoleNotehead, edit.Loc{Bar: 1, Event: 1}, 0)
	tg, ok := scorePointerTarget(l, s, 0, q, e5)
	if !ok || tg.Line != 0 || tg.Bar != 1 || !tg.At.Eq(score.F(1, 4)) || tg.Diatonic != pitchD("E5") {
		t.Errorf("on E5: %+v %v", tg, ok)
	}
	if math.Abs(tg.Y-sb.Y(pitchD("E5"))) > 1e-9 || math.Abs(tg.X-bb.XAt(0.25)) > 1e-9 {
		t.Errorf("ghost at %v,%v", tg.X, tg.Y)
	}
	// Between beats 3 and 4 with an eighth: the eighth grid.
	e := score.Duration{Base: score.Eighth}
	x := bb.XAt(0.625)
	tg, _ = scorePointerTarget(l, s, 0, e, engrave.Point{X: x, Y: sb.Top + 2})
	if !tg.At.Eq(score.F(5, 8)) {
		t.Errorf("eighth grid at 5/8: %s", tg.At)
	}
	// Inside bar 2's triplet only its own notes are places: x halfway
	// between the triplet's first two notes snaps to one of them, never to
	// the eighth grid's 1/8.
	b2 := l.Bar(2)
	mid := (b2.XAt(0) + b2.XAt(1.0/12)) / 2
	tg, _ = scorePointerTarget(l, s, 0, e, engrave.Point{X: mid + 0.01, Y: sb.Top + 2})
	if !tg.At.Eq(score.F(1, 12)) && !tg.At.IsZero() {
		t.Errorf("in the triplet: %s", tg.At)
	}
	// Voice 2 of the same staff has no events: the grid alone.
	tg, _ = scorePointerTarget(l, s, 1, e, engrave.Point{X: b2.XAt(0.125), Y: sb.Top + 2})
	if !tg.At.Eq(score.F(1, 8)) {
		t.Errorf("voice 2: %s", tg.At)
	}
	// A whole note's place is the bar's start: the grid stops a value short of the end.
	w := score.Duration{Base: score.WholeNote}
	tg, _ = scorePointerTarget(l, s, 1, w, engrave.Point{X: bb.XAt(0.9), Y: sb.Top + 2})
	if !tg.At.Eq(score.F(7, 8)) {
		t.Errorf("whole: %s", tg.At)
	}
	// The cello's staff: line 1, its own clef.
	cs := l.Staves[1]
	tg, ok = scorePointerTarget(l, s, 0, q, engrave.Point{X: bb.XAt(0), Y: cs.Top + 4})
	if !ok || tg.Line != 1 || tg.Diatonic != pitchD("G2") {
		t.Errorf("cello bottom line: %+v %v", tg, ok)
	}
	// Far from any staff, nothing.
	if _, ok := scorePointerTarget(l, s, 0, q, engrave.Point{X: bb.XAt(0), Y: cs.Top + 40}); ok {
		t.Error("a target far below the last staff")
	}
}

func TestScoreHitBarsAndBoxes(t *testing.T) {
	s := scoreHitScore(t)
	l := engrave.Engrave(s, engrave.Options{BarNumbers: -1})
	b3 := l.Bar(3)
	line, bar, ok := scoreHitBar(l, engrave.Point{X: (b3.X0 + b3.X1) / 2, Y: l.Staves[1].Top + 2})
	if !ok || line != 1 || bar != 3 {
		t.Errorf("bar hit = %d %d %v", line, bar, ok)
	}
	if _, _, ok := scoreHitBar(l, engrave.Point{X: b3.X1 + 50, Y: l.Staves[1].Top + 2}); ok {
		t.Error("hit past the last bar")
	}
	r, ok := scoreBarsRect(l, 0, 1, 2, 3)
	if !ok || r.Min.X != l.Bar(2).X0 || r.Max.X != b3.X1 || r.Min.Y >= l.Staves[0].Top || r.Max.Y <= l.Staves[1].Top+4 {
		t.Errorf("bars rect = %+v", r)
	}
	if er, ok := scoreEventRect(l, edit.Loc{Bar: 1, Event: 0}); !ok || er.Empty() {
		t.Errorf("event rect = %+v %v", er, ok)
	}
}
