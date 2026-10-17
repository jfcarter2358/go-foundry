package edit

// dynamics.go — the dynamics lane's overrides.
//
// A segment of a line's dynamics curve dragged up or down in the lane
// becomes a score.DynOverride: the level held flat between two written
// points, the printed marks untouched. Setting one over others replaces
// them where they overlap and keeps the rest of each; clearing a span
// gives that part of the line back to its marks.

import (
	"fmt"
	"sort"

	"foundry/internal/score"
)

// SetDynamicOverride holds a line's level at level (the marks' scale, 0..1)
// from one written point to another.
func SetDynamicOverride(s *score.Score, line int, from, to score.Pos, level float32) error {
	if line < 0 || line >= len(s.Lines) {
		return fmt.Errorf("no line %d", line+1)
	}
	if !score.PosLess(from, to) {
		return fmt.Errorf("an override needs a span")
	}
	if level < 0 || level > 1 {
		return fmt.Errorf("level %.2f is outside 0..1", level)
	}
	ClearDynamicOverrides(s, line, from, to)
	l := s.Lines[line]
	l.Overrides = append(l.Overrides, score.DynOverride{From: from, To: to, Level: level})
	sortOverrides(l)
	return nil
}

// ClearDynamicOverrides gives a span of a line back to its marks: the
// overrides inside it go, the ones across its ends are cut back to them.
func ClearDynamicOverrides(s *score.Score, line int, from, to score.Pos) {
	if line < 0 || line >= len(s.Lines) {
		return
	}
	l := s.Lines[line]
	starts := s.BarStarts()
	a, b := s.PosFrac(from, starts), s.PosFrac(to, starts)
	var out []score.DynOverride
	for _, o := range l.Overrides {
		oa, ob := s.PosFrac(o.From, starts), s.PosFrac(o.To, starts)
		if !oa.Less(b) || !a.Less(ob) {
			out = append(out, o) // clear of the span
			continue
		}
		if oa.Less(a) {
			out = append(out, score.DynOverride{From: o.From, To: s.PosAt(a, starts), Level: o.Level})
		}
		if b.Less(ob) {
			out = append(out, score.DynOverride{From: s.PosAt(b, starts), To: o.To, Level: o.Level})
		}
	}
	l.Overrides = out
	sortOverrides(l)
}

func sortOverrides(l *score.Line) {
	sort.SliceStable(l.Overrides, func(i, j int) bool { return score.PosLess(l.Overrides[i].From, l.Overrides[j].From) })
}
