package ai

// async_nav_test.go — controllers while navigation builds in the background.

import (
	"testing"

	"foundry/internal/collision"
	"foundry/internal/hooks"
	"foundry/internal/nav"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func withAsyncNav(t *testing.T) {
	t.Helper()
	prev := AsyncNav
	AsyncNav = true
	t.Cleanup(func() {
		nav.Wait()
		AsyncNav = prev
	})
}

func TestAnAgentHoldsStillUntilItsNavigationIsBuilt(t *testing.T) {
	pillar := world.NewBoxBrush("pillar", mgl32.Vec3{10, 2, 10}, mgl32.Vec3{3, 2, 3}, "concrete")
	lvl := aiLevel(t, pillar)
	withAsyncNav(t)
	lvl.Entities = []world.Entity{agent("guard", mgl32.Vec3{10, 0, 3})}
	start := Feet(lvl, &lvl.Entities[0])
	goal := mgl32.Vec3{10, 0, 17}

	MoveTo(lvl, &lvl.Entities[0], goal)
	const dt = float32(1.0 / 60.0)
	var events []hooks.AIEvent
	// Nothing has been built, and a finished build is only installed at the
	// next sync: however fast the goroutine is, the first tick has no grid.
	Update(lvl, dt, nil)
	collision.EntityUpdate(lvl, dt, true, nil)
	events = append(events, hooks.TakeAIEvents()...)
	nav.Wait()
	e := &lvl.Entities[0]
	if d := Feet(lvl, e).Sub(start); d.X()*d.X()+d.Z()*d.Z() > 1e-4 {
		t.Errorf("the guard moved %v with no navigation built; it should hold, not walk a straight line at the pillar", d)
	}
	if n := count(events, "ai_path_failed"); n != 0 {
		t.Errorf("waiting for navigation posted %d ai_path_failed", n)
	}
	if e.AIState == nil || !e.AIState.Command {
		t.Fatal("the command was dropped while waiting for navigation")
	}

	for i := 0; i < 60*12 && count(events, "ai_arrived") == 0; i++ {
		Update(lvl, dt, nil)
		collision.EntityUpdate(lvl, dt, true, nil)
		events = append(events, hooks.TakeAIEvents()...)
		f := Feet(lvl, e)
		if f.X() > 7.1 && f.X() < 12.9 && f.Z() > 7.1 && f.Z() < 12.9 {
			t.Fatalf("the guard is inside the pillar at %v", f)
		}
	}
	if count(events, "ai_arrived") != 1 {
		t.Fatalf("the guard never arrived once navigation was built; it is at %v, events %v", Feet(lvl, e), events)
	}
}
