package ai

import (
	"testing"

	"foundry/internal/collision"
	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// Two agents sent to swap places along the same line would, walking straight,
// meet chest to chest and stand there pushing — paths know the level, not
// each other. Both have to get through.
func TestTwoAgentsSwappingPlacesHeadOnBothArrive(t *testing.T) {
	lvl := aiLevel(t)
	a, b := agent("a", mgl32.Vec3{4, 0, 10}), agent("b", mgl32.Vec3{16, 0, 10})
	a.Collision.Entities, b.Collision.Entities = true, true
	lvl.Entities = []world.Entity{a, b}
	MoveTo(lvl, &lvl.Entities[0], mgl32.Vec3{16, 0, 10}, false)
	MoveTo(lvl, &lvl.Entities[1], mgl32.Vec3{4, 0, 10}, false)

	var events []hooks.AIEvent
	const dt = float32(1.0 / 60.0)
	for i := 0; i < 60*10 && count(events, "ai_arrived") < 2; i++ {
		Update(lvl, dt, nil)
		collision.EntityUpdate(lvl, dt, true, nil)
		events = append(events, hooks.TakeAIEvents()...)
	}
	if n := count(events, "ai_arrived"); n != 2 {
		t.Fatalf("%d of 2 arrived in ten seconds; they are at %v and %v", n,
			Feet(lvl, &lvl.Entities[0]), Feet(lvl, &lvl.Entities[1]))
	}
}

// A file of four walking through a file of four coming the other way, in a
// room — the squad doubling back through itself.
func TestTwoFilesCrossingGetThrough(t *testing.T) {
	lvl := aiLevel(t)
	for k := 0; k < 4; k++ {
		z := float32(7 + 2*k)
		l, r := agent("l"+string(rune('0'+k)), mgl32.Vec3{3, 0, z}), agent("r"+string(rune('0'+k)), mgl32.Vec3{17, 0, z})
		l.Collision.Entities, r.Collision.Entities = true, true
		lvl.Entities = append(lvl.Entities, l, r)
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		goal := e.Position
		goal[0] = 20 - goal[0]
		MoveTo(lvl, e, goal, false)
	}
	var events []hooks.AIEvent
	const dt = float32(1.0 / 60.0)
	for i := 0; i < 60*15 && count(events, "ai_arrived") < 8; i++ {
		Update(lvl, dt, nil)
		collision.EntityUpdate(lvl, dt, true, nil)
		events = append(events, hooks.TakeAIEvents()...)
	}
	if n := count(events, "ai_arrived"); n != 8 {
		t.Errorf("%d of 8 crossed in fifteen seconds", n)
	}
}
