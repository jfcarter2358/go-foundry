package compile

// leak_opening_test.go — saying where the void gets in.
//
// A leak is reported by naming something the author placed — the spawn, an
// entity — and that thing is almost always exactly where they meant it to be.
// "The crate is outside the level" is then a report they can see is false. The
// report has to point at the gap instead.

import (
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func leaksOf(t *testing.T, lvl *world.Level) []Leak {
	t.Helper()
	r, err := Compile(lvl, Options{SkipLight: true})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return r.Stats.Leaks
}

// The corridor level with its north wall gone: two rooms that are plainly
// inside the level to anyone looking, open along one side to the void.
func TestALeakSaysWhereTheVoidGetsIn(t *testing.T) {
	crate := world.Entity{ID: "crate", Position: mgl32.Vec3{-15, 2, 0}}

	sealed := corridorLevel()
	sealed.Entities = []world.Entity{crate}
	if leaks := leaksOf(t, sealed); len(leaks) != 0 {
		t.Fatalf("the sealed fixture leaks: %+v", leaks)
	}

	lvl := corridorLevel()
	lvl.Chunks[0].Brushes = append(lvl.Chunks[0].Brushes[:5], lvl.Chunks[0].Brushes[6:]...) // the north wall
	lvl.Entities = []world.Entity{crate}
	leaks := leaksOf(t, lvl)
	if len(leaks) != 1 {
		t.Fatalf("leaks = %+v, want the crate", leaks)
	}
	l := leaks[0]
	if !l.HasOpening {
		t.Fatalf("the leak does not say where the void gets in: %s", l.Describe())
	}
	// The north wall stood from z=10 to z=11, between the floor and the ceiling.
	if o := l.Opening; o.Z() < 9.5 || o.Z() > 12.5 || o.Y() < 0 || o.Y() > 4 {
		t.Errorf("the gap is reported at %v, nowhere near the missing north wall", o)
	}
	if d := l.Describe(); !strings.Contains(d, "crate") || !strings.Contains(d, "gap near") {
		t.Errorf("Describe = %q", d)
	}
}

// Something out in open space has no gap to point at, and naming one would
// send the author looking for a hole that is not there.
func TestSomethingOutInTheVoidHasNoOpening(t *testing.T) {
	lvl := corridorLevel()
	lvl.Entities = []world.Entity{{ID: "lost", Position: mgl32.Vec3{0, 15, 0}}}
	leaks := leaksOf(t, lvl)
	if len(leaks) != 1 {
		t.Fatalf("leaks = %+v, want the lost entity", leaks)
	}
	if leaks[0].HasOpening {
		t.Errorf("an entity above the roof was given a gap at %v", leaks[0].Opening)
	}
}
