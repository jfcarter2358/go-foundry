package render

// gi_sources_owned_test.go — the source diff with its groups owned by the
// chunks, entities and placements they come from.

import (
	"testing"

	"foundry/internal/world"
)

// An entity leaving the middle of the list takes its group with it — where
// it was is dirty — and the entities before it are left alone.
func TestRemovingAnEntityDirtiesOnlyWhereItWas(t *testing.T) {
	lvl := giBenchLevel(1, 4, 5)
	s := newGISourceSet()
	s.minModel = 0.1
	s.collect(lvl)
	before := len(s.flat)
	s.changed, s.staticChanged = false, false

	lvl.Entities = append(lvl.Entities[:2], lvl.Entities[3:]...)
	s.collect(lvl)
	if !s.changed || s.staticChanged {
		t.Fatalf("an entity removed: changed %v, staticChanged %v (want true, false)", s.changed, s.staticChanged)
	}
	if len(s.flat) != before-1 {
		t.Errorf("%d sources after the removal, want %d", len(s.flat), before-1)
	}
	// The entities after it moved up a place, so their places changed hands
	// and are re-derived; the two before it are untouched.
	for _, d := range s.dirty {
		if d[1][0] < 1.5 {
			t.Errorf("dirty bounds %v reach the entities that did not move", d)
		}
	}
}

// A brush turned dynamic leaves the static set for the moving one; the
// flattened list keeps every source.
func TestABrushTurnedDynamicLeavesTheStaticSet(t *testing.T) {
	lvl := giBenchLevel(1, 3, 0)
	s := newGISourceSet()
	s.collect(lvl)
	if s.nStatic != 3 || len(s.flat) != 3 {
		t.Fatalf("first frame: %d static of %d", s.nStatic, len(s.flat))
	}
	s.changed, s.staticChanged = false, false
	lvl.Chunks[0].Brushes[1].Dynamic = true
	world.MarkBrushMeshesChanged()
	s.collect(lvl)
	if !s.staticChanged || s.nStatic != 2 || len(s.flat) != 3 {
		t.Errorf("after the flip: staticChanged %v, %d static of %d (want true, 2 of 3)", s.staticChanged, s.nStatic, len(s.flat))
	}
	if s.count != 3 {
		t.Errorf("counted %d groups, want 3", s.count)
	}
}

// The flattened list is made in the level's order, so two sets collecting
// the same level agree source for source.
func TestTheFlattenedSourcesComeOutInTheLevelsOrder(t *testing.T) {
	lvl := giBenchLevel(6, 20, 10)
	a, b := newGISourceSet(), newGISourceSet()
	a.minModel, b.minModel = 0.1, 0.1
	a.collect(lvl)
	b.collect(lvl)
	if len(a.flat) != len(b.flat) || a.nStatic != b.nStatic {
		t.Fatalf("%d/%d sources against %d/%d", a.nStatic, len(a.flat), b.nStatic, len(b.flat))
	}
	for i := range a.flat {
		if a.flat[i].lo != b.flat[i].lo || a.flat[i].hi != b.flat[i].hi || a.flat[i].kind != b.flat[i].kind {
			t.Fatalf("source %d differs: %v..%v against %v..%v", i, a.flat[i].lo, a.flat[i].hi, b.flat[i].lo, b.flat[i].hi)
		}
	}
}
