package edit

import (
	"testing"

	"foundry/internal/score"
)

func TestDynamicOverrides(t *testing.T) {
	s := score.New("t")
	s.Lines = append(s.Lines, score.NewLine("A", "x", "treble"))
	s.Bars = 4
	pos := func(bar int, q int64) score.Pos { return score.Pos{Bar: bar, Offset: score.F(q, 4)} }
	if err := SetDynamicOverride(s, 0, pos(1, 0), pos(3, 0), 0.3); err != nil {
		t.Fatal(err)
	}
	// A second one over the middle splits the first around it.
	if err := SetDynamicOverride(s, 0, pos(1, 2), pos(2, 2), 0.8); err != nil {
		t.Fatal(err)
	}
	got := s.Lines[0].Overrides
	want := []score.DynOverride{{From: pos(1, 0), To: pos(1, 2), Level: 0.3}, {From: pos(1, 2), To: pos(2, 2), Level: 0.8}, {From: pos(2, 2), To: pos(3, 0), Level: 0.3}}
	if len(got) != len(want) {
		t.Fatalf("overrides %+v", got)
	}
	for i := range want {
		if got[i].From.Bar != want[i].From.Bar || !got[i].From.Offset.Eq(want[i].From.Offset) || got[i].To.Bar != want[i].To.Bar ||
			!got[i].To.Offset.Eq(want[i].To.Offset) || got[i].Level != want[i].Level {
			t.Errorf("override %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	ClearDynamicOverrides(s, 0, pos(1, 1), pos(4, 0))
	if o := s.Lines[0].Overrides; len(o) != 1 || o[0].To.Bar != 1 || !o[0].To.Offset.Eq(score.F(1, 4)) {
		t.Errorf("after clearing: %+v", o)
	}
	for _, bad := range []func() error{
		func() error { return SetDynamicOverride(s, 3, pos(1, 0), pos(2, 0), 0.5) },
		func() error { return SetDynamicOverride(s, 0, pos(2, 0), pos(2, 0), 0.5) },
		func() error { return SetDynamicOverride(s, 0, pos(1, 0), pos(2, 0), 1.5) },
	} {
		if bad() == nil {
			t.Error("a bad override was taken")
		}
	}
	if issues := s.Validate(nil); len(issues) > 0 {
		t.Errorf("issues: %v", issues)
	}
}
