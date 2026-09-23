package ai

import (
	"testing"

	"foundry/internal/collision"
	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// fighter is agent with on_target: fight against the player, seeing all round
// so the tests are about moving, not noticing.
func fighter(id string, feet mgl32.Vec3) world.Entity {
	e := agent(id, feet)
	e.AI.Targets = []string{"player"}
	e.AI.OnTarget = world.AIFight
	e.AI.SightRange = 60
	e.AI.FOV = 360
	e.AI.AttackRange = 30
	e.AI.FightRange = 8
	e.AI.Reposition = 1
	e.AI.LoseTime = 30
	e.AI.DodgeChance = 0
	return e
}

func TestAFighterMovesBetweenFiringPositions(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{fighter("soldier", mgl32.Vec3{10, 0, 4})}
	p := player(t, mgl32.Vec3{10, 0, 14})
	e := &lvl.Entities[0]

	var events []hooks.AIEvent
	start := Feet(lvl, e)
	travelled := float32(0)
	last := start
	const dt = float32(1.0 / 60.0)
	for i := 0; i < 60*12; i++ {
		Update(lvl, dt, p)
		collision.EntityUpdate(lvl, dt, true, p)
		evs := hooks.TakeAIEvents()
		events = append(events, evs...)
		f := Feet(lvl, e)
		travelled += distXZ(f, last)
		last = f
		for _, ev := range evs {
			if ev.Atom == "ai_in_position" && !Sees(lvl, e, p) {
				t.Errorf("stood to fight at %v with no view of the player", f)
			}
		}
	}
	if n := count(events, "ai_maneuver"); n < 3 {
		t.Errorf("made %d moves in twelve seconds; a fighter should keep moving", n)
	}
	if count(events, "ai_in_position") < 2 {
		t.Errorf("never settled into a firing position: events %v", events)
	}
	if travelled < 5 {
		t.Errorf("travelled only %.1f m in twelve seconds of fighting", travelled)
	}
	if count(events, "target_in_range") == 0 {
		t.Error("never told its script the target was in range, so it would never shoot")
	}
}

func TestAFighterDivesWhenThePlayerAimsAtIt(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{fighter("soldier", mgl32.Vec3{10, 0, 6})}
	e := &lvl.Entities[0]
	e.AI.DodgeChance = 0.99
	e.AI.Reposition = 60 // no moves of its own to confuse the dive with
	p := player(t, mgl32.Vec3{10, 0, 16})
	t.Cleanup(ClearPlayerAim)

	var events []hooks.AIEvent
	start := Feet(lvl, e)
	const dt = float32(1.0 / 60.0)
	for i := 0; i < 60*2 && count(events, "ai_dodge") == 0; i++ {
		eye := eyeOf(lvl, p)
		SetPlayerAim(eye, chestOf(lvl, e).Sub(eye))
		Update(lvl, dt, p)
		collision.EntityUpdate(lvl, dt, true, p)
		events = append(events, hooks.TakeAIEvents()...)
	}
	if count(events, "ai_dodge") == 0 {
		t.Fatal("aimed at for two seconds with dodge_chance 0.99 and never dove")
	}
	ClearPlayerAim()
	run(lvl, p, 1.2, &events)
	moved := Feet(lvl, e).Sub(start)
	if absf(moved.X()) < 1.5 {
		t.Errorf("the dive carried it %v: it should go across the line of fire (along X)", moved)
	}
	if count(events, "ai_in_position") == 0 {
		t.Error("never got up from the dive")
	}
}

func TestNoDiveWhileTheAimIsElsewhere(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{fighter("soldier", mgl32.Vec3{10, 0, 6})}
	e := &lvl.Entities[0]
	e.AI.DodgeChance = 0.99
	e.AI.Reposition = 60
	p := player(t, mgl32.Vec3{10, 0, 16})
	t.Cleanup(ClearPlayerAim)
	SetPlayerAim(eyeOf(lvl, p), mgl32.Vec3{1, 0, 0})

	var events []hooks.AIEvent
	run(lvl, p, 2, &events)
	if n := count(events, "ai_dodge"); n != 0 {
		t.Errorf("dove %d times with the player looking away", n)
	}
}

func TestADodgeFromAScriptNeedsNoTarget(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{agent("soldier", mgl32.Vec3{10, 0, 10})}
	e := &lvl.Entities[0]
	var events []hooks.AIEvent
	run(lvl, nil, 0.1, &events)

	grenade := mgl32.Vec3{10, 0, 11}
	if !Dodge(lvl, e, grenade, true) {
		t.Fatal("Dodge refused an idle agent in an open room")
	}
	if Dodge(lvl, e, grenade, true) {
		t.Error("a second Dodge started mid-dive")
	}
	run(lvl, nil, 1.2, &events)
	if d := Feet(lvl, e).Sub(grenade); d.Z() > -1.5 {
		t.Errorf("dove to %v; it should have gone away from the grenade (toward -Z)", Feet(lvl, e))
	}
	if count(events, "ai_dodge") != 1 || count(events, "ai_in_position") != 1 {
		t.Errorf("events %v: want one ai_dodge and one ai_in_position", events)
	}
}

// A full dive: the spec's timing, not the engine's sidestep. It is still down,
// and not yet in position, long after the quick dive would have been up.
func TestAFullDiveStaysDownForItsSpecsTime(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{agent("soldier", mgl32.Vec3{10, 0, 10})}
	e := &lvl.Entities[0]
	e.AI.DodgeDistance, e.AI.DodgeWindup, e.AI.DodgeRecover = 4, 0.2, 1.5
	var events []hooks.AIEvent
	run(lvl, nil, 0.1, &events)

	grenade := mgl32.Vec3{10, 0, 11}
	if !Dodge(lvl, e, grenade, true) {
		t.Fatal("Dodge refused an idle agent in an open room")
	}
	run(lvl, nil, 1.2, &events)
	if d := Feet(lvl, e).Sub(grenade); d.Z() > -3.5 {
		t.Errorf("dove to %v; a 4 m dive should have carried it well past -3.5 on Z", Feet(lvl, e))
	}
	if count(events, "ai_in_position") != 0 {
		t.Errorf("up after 1.2 s; the dive is 0.2 + 0.5 + 1.5 s long")
	}
	landed := Feet(lvl, e)
	run(lvl, nil, 1.0, &events)
	if count(events, "ai_in_position") != 1 {
		t.Errorf("events %v: want it up (ai_in_position) by 2.2 s", events)
	}
	if d := Feet(lvl, e).Sub(landed); d.Len() > 0.3 {
		t.Errorf("slid %v while down; it should lie where it landed", d)
	}
}

func TestAFighterGoesLookingForATargetBehindAPillar(t *testing.T) {
	pillar := world.NewBoxBrush("pillar", mgl32.Vec3{10, 2, 10}, mgl32.Vec3{4, 2, 1}, "concrete")
	lvl := aiLevel(t, pillar)
	lvl.Entities = []world.Entity{fighter("soldier", mgl32.Vec3{10, 0, 5})}
	e := &lvl.Entities[0]
	p := player(t, mgl32.Vec3{10, 0, 13})
	if Sees(lvl, e, p) {
		t.Fatal("the pillar does not hide the player; the test proves nothing")
	}
	SetTarget(lvl, e, p)

	var events []hooks.AIEvent
	const dt = float32(1.0 / 60.0)
	found := false
	for i := 0; i < 60*10 && !found; i++ {
		Update(lvl, dt, p)
		collision.EntityUpdate(lvl, dt, true, p)
		events = append(events, hooks.TakeAIEvents()...)
		found = Sees(lvl, e, p)
	}
	if !found {
		t.Fatalf("never got a view of the player round the pillar; it is at %v, events %v", Feet(lvl, e), events)
	}
}

func TestOnlyOneFighterFlanksATargetAtATime(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{
		fighter("a", mgl32.Vec3{6, 0, 3}),
		fighter("b", mgl32.Vec3{10, 0, 3}),
		fighter("c", mgl32.Vec3{14, 0, 3}),
	}
	for i := range lvl.Entities {
		lvl.Entities[i].AI.Aggression = 1
		lvl.Entities[i].AI.FightRange = 5
	}
	p := player(t, mgl32.Vec3{10, 0, 16})
	flanked := false
	const dt = float32(1.0 / 60.0)
	for i := 0; i < 60*15; i++ {
		Update(lvl, dt, p)
		collision.EntityUpdate(lvl, dt, true, p)
		hooks.TakeAIEvents()
		n := 0
		for j := range lvl.Entities {
			if lvl.Entities[j].AIState.FightMove == fightFlank {
				n++
			}
		}
		if n > 1 {
			t.Fatalf("%d fighters flanking the one target at once", n)
		}
		flanked = flanked || n == 1
	}
	if !flanked {
		t.Error("three aggressive fighters in fifteen seconds and nobody flanked")
	}
}

func TestAFollowerFightsWithinItsLeash(t *testing.T) {
	lvl := aiLevel(t)
	squaddie := fighter("squaddie", mgl32.Vec3{10, 0, 4})
	squaddie.AI.Targets = []string{"#enemy"}
	squaddie.AI.Follow = "player"
	squaddie.AI.FollowLeash = 6
	squaddie.AI.Aggression = 1
	foe := agent("foe", mgl32.Vec3{10, 0, 17})
	foe.Tags = []string{"enemy"}
	foe.AI.Enabled = false
	foe.AI = nil
	lvl.Entities = []world.Entity{squaddie, foe}
	p := player(t, mgl32.Vec3{10, 0, 3})
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}

	var events []hooks.AIEvent
	const dt = float32(1.0 / 60.0)
	for i := 0; i < 60*12; i++ {
		Update(lvl, dt, p)
		collision.EntityUpdate(lvl, dt, true, p)
		events = append(events, hooks.TakeAIEvents()...)
		st := lvl.Entities[0].AIState
		if st.FightMove != fightHold && distXZ(st.FightGoal, Feet(lvl, p)) > 6*0.7+0.01 {
			t.Fatalf("heading for %v, %.1f m from its leader: past the leash's reach", st.FightGoal, distXZ(st.FightGoal, Feet(lvl, p)))
		}
	}
	if count(events, "ai_maneuver") == 0 {
		t.Error("a follower with a foe in view never moved to fight it")
	}
}

func TestTheStrafeClipFollowsTheWayTheBodyMovesAgainstItsFacing(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{fighter("soldier", mgl32.Vec3{10, 0, 10})}
	e := &lvl.Entities[0]
	e.AI.StrafeLeftAnim, e.AI.StrafeRightAnim, e.AI.BackAnim = "sl", "sr", "back"
	// Facing -Z, its left is -X.
	for _, c := range []struct {
		vel       mgl32.Vec3
		cur, want string
	}{
		{mgl32.Vec3{0, 0, -2}, "", "walk"},
		{mgl32.Vec3{-2, 0, 0}, "", "sl"},
		{mgl32.Vec3{2, 0, 0}, "", "sr"},
		{mgl32.Vec3{0, 0, 2}, "", "back"},
		// On the diagonal, whatever plays is kept.
		{mgl32.Vec3{-1.4, 0, -1.45}, "walk", "walk"},
		{mgl32.Vec3{-1.45, 0, -1.4}, "sl", "sl"},
		// Too slow to say which way it is going: unchanged.
		{mgl32.Vec3{0.1, 0, 0}, "back", "back"},
	} {
		e.Velocity = c.vel
		if got := strafeAnim(lvl, e, "walk", c.cur); got != c.want {
			t.Errorf("moving %v playing %q: got %q, want %q", c.vel, c.cur, got, c.want)
		}
	}
}
