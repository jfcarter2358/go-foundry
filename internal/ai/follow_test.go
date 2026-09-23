package ai

import (
	"testing"

	"foundry/internal/collision"
	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// walkLeader moves a scripted leader in a straight line at speed, a tick at a
// time, running the controllers around it, then lets everything settle.
func walkLeader(lvl *world.Level, leader int, to mgl32.Vec3, speed, settle float32, each func()) []hooks.AIEvent {
	const dt = float32(1.0 / 60.0)
	var events []hooks.AIEvent
	for t := float32(0); t < 30; t += dt {
		l := &lvl.Entities[leader]
		d := to.Sub(l.Position)
		if d.Len() <= speed*dt {
			l.Position = to
			break
		}
		l.Position = l.Position.Add(d.Normalize().Mul(speed * dt))
		Update(lvl, dt, nil)
		collision.EntityUpdate(lvl, dt, true, nil)
		events = append(events, hooks.TakeAIEvents()...)
		if each != nil {
			each()
		}
	}
	for t := float32(0); t < settle; t += dt {
		Update(lvl, dt, nil)
		collision.EntityUpdate(lvl, dt, true, nil)
		events = append(events, hooks.TakeAIEvents()...)
		if each != nil {
			each()
		}
	}
	return events
}

// scriptedLeader is a body something other than a controller moves.
func scriptedLeader(id string, feet mgl32.Vec3) world.Entity {
	l := agent(id, feet)
	l.AI = nil
	l.Collision = world.EntityCollision{}
	return l
}

func follower(id string, feet mgl32.Vec3, leader string) world.Entity {
	f := agent(id, feet)
	f.AI.Follow = leader
	return f
}

func TestFollowersGatherInASpreadGroupBehindTheirLeader(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{
		scriptedLeader("lead", mgl32.Vec3{10, 0, 3}),
		follower("alpha", mgl32.Vec3{3, 0, 2}, "lead"),
		follower("bravo", mgl32.Vec3{17, 0, 2}, "@lead"),
		follower("charlie", mgl32.Vec3{10, 0, 1}, "lead"),
	}
	walkLeader(lvl, 0, mgl32.Vec3{10, 0, 15}, 1.5, 5, nil)

	lead := Feet(lvl, &lvl.Entities[0])
	spacing := world.DefaultAISpec().FollowDistance
	var feet []mgl32.Vec3
	for i := 1; i < len(lvl.Entities); i++ {
		e := &lvl.Entities[i]
		f := Feet(lvl, e)
		feet = append(feet, f)
		// The leader walked +Z, so behind is -Z.
		if f.Z() > lead.Z()-0.3 {
			t.Errorf("%s stands at %v, not behind its leader at %v", e.ID, f, lead)
		}
		if d := distXZ(f, lead); d < spacing*0.6 || d > spacing*1.9 {
			t.Errorf("%s is %.2f from its leader, want about %.1f", e.ID, d, spacing)
		}
		if Mode(e) != world.AIEscorting {
			t.Errorf("%s is %s, want escorting", e.ID, world.AIModeName(Mode(e)))
		}
	}
	// Spread out, not lined up: nobody on top of anybody, and the group at least
	// a spacing wide across the leader's path.
	lo, hi := feet[0], feet[0]
	for i := range feet {
		for j := i + 1; j < len(feet); j++ {
			if d := distXZ(feet[i], feet[j]); d < 1 {
				t.Errorf("followers %d and %d are %.2f apart", i, j, d)
			}
		}
		lo = mgl32.Vec3{minf(lo.X(), feet[i].X()), 0, minf(lo.Z(), feet[i].Z())}
		hi = mgl32.Vec3{maxf(hi.X(), feet[i].X()), 0, maxf(hi.Z(), feet[i].Z())}
	}
	if width := hi.X() - lo.X(); width < spacing {
		t.Errorf("the group is %.2f wide across the leader's path — strung out in a line: %v", width, feet)
	}
}

func TestAFollowerThatHasFallenBehindRuns(t *testing.T) {
	lvl := aiLevel(t)
	f := follower("squaddie", mgl32.Vec3{3, 0, 3}, "lead")
	f.AI.Speed, f.AI.RunSpeed, f.AI.MoveAnim, f.AI.RunAnim = 1.2, 3.3, "walk", "run"
	lvl.Entities = []world.Entity{scriptedLeader("lead", mgl32.Vec3{17, 0, 17}), f}

	ran, walked := false, false
	walkLeader(lvl, 0, mgl32.Vec3{17, 0, 16}, 1, 12, func() {
		st := lvl.Entities[1].AIState
		if st.FollowRunning && st.Anim == "run" {
			ran = true
		}
		if ran && !st.FollowRunning && st.Anim == "walk" {
			walked = true
		}
	})
	if !ran {
		t.Error("a follower 20 m behind never broke into a run")
	}
	if !walked {
		t.Error("the follower never dropped back to a walk as it closed in")
	}
	if d := distXZ(Feet(lvl, &lvl.Entities[1]), Feet(lvl, &lvl.Entities[0])); d > 4.5 {
		t.Errorf("the follower ended %.2f from its leader", d)
	}
}

func TestAFollowerFightsWhereItStandsUntilItsLeaderLeavesItBehind(t *testing.T) {
	lvl := aiLevel(t)
	f := follower("squaddie", mgl32.Vec3{10, 0, 7}, "lead")
	f.AI.Targets = []string{"foe"}
	f.AI.OnTarget = world.AIWatch
	f.AI.AttackRange = 30
	f.AI.FOV = 360
	f.AI.FollowLeash = 6
	lvl.Entities = []world.Entity{
		scriptedLeader("lead", mgl32.Vec3{11, 0, 6}),
		f,
		scriptedLeader("foe", mgl32.Vec3{10, 0, 18}),
	}

	// A short walk keeps the leader inside the leash: the follower stays put
	// and fights.
	start := Feet(lvl, &lvl.Entities[1])
	events := walkLeader(lvl, 0, mgl32.Vec3{12, 0, 4}, 1.5, 1, nil)
	if d := distXZ(Feet(lvl, &lvl.Entities[1]), start); d > 0.3 {
		t.Errorf("the follower moved %.2f with its leader still inside the leash", d)
	}
	if count(events, "target_in_range") == 0 {
		t.Fatal("the follower never engaged its target")
	}

	regrouped := false
	events = walkLeader(lvl, 0, mgl32.Vec3{3, 0, 1}, 3, 6, func() {
		if Mode(&lvl.Entities[1]) == world.AIRegrouping {
			regrouped = true
		}
	})
	if !regrouped {
		t.Fatal("the leader went past the leash and the follower never regrouped")
	}
	if d := distXZ(Feet(lvl, &lvl.Entities[1]), Feet(lvl, &lvl.Entities[0])); d > 6 {
		t.Errorf("the follower is still %.2f from its leader", d)
	}
	if count(events, "target_in_range") == 0 {
		t.Error("the follower stopped engaging while it regrouped")
	}
}

func TestATargetThatGoesDownIsLostEvenWhenAScriptChoseIt(t *testing.T) {
	lvl := aiLevel(t)
	hunter := agent("hunter", mgl32.Vec3{5, 0, 5})
	hunter.AI.OnTarget = world.AIWatch
	hunter.AI.Targets = []string{"#foe"}
	foe := agent("foe", mgl32.Vec3{5, 0, 12})
	foe.Tags = []string{"foe"}
	foe.AI.OnTarget = world.AIIgnore
	asleep := agent("asleep", mgl32.Vec3{6, 0, 12})
	asleep.Tags = []string{"foe"}
	asleep.AI.Enabled = false
	lvl.Entities = []world.Entity{hunter, foe, asleep}

	for _, c := range Candidates(lvl, &lvl.Entities[0], nil) {
		if c.ID == "asleep" {
			t.Error("an agent whose controller is off is a candidate target")
		}
	}
	if !SetTarget(lvl, &lvl.Entities[0], &lvl.Entities[1]) {
		t.Fatal("SetTarget refused")
	}
	var events []hooks.AIEvent
	run(lvl, nil, 0.5, &events)
	lvl.Entities[1].AI.Enabled = false // the combat script's death
	run(lvl, nil, 0.5, &events)
	if Target(lvl, &lvl.Entities[0]) != nil {
		t.Errorf("the hunter still holds %s after it went down", lvl.Entities[0].AIState.Target)
	}
	if count(events, "target_lost") != 1 {
		t.Errorf("target_lost posted %d times, want 1", count(events, "target_lost"))
	}
}

// hullEntity is a 6×6 room built out of an entity's own brushes, floor flush
// with the level's at y=0, open on its +X side — a parked ship with a door.
func hullEntity(lvl *world.Level, id string, at mgl32.Vec3) {
	e := world.NewBlankEntity(id)
	e.Position = at
	e.Scale = mgl32.Vec3{1, 1, 1}
	room := world.NewHollowRoom("hull", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{6, 3, 6}, 0.5, "concrete")
	for i := range room {
		lo, hi := room[i].LocalAABB()
		if lo.X() >= 5.9 && hi.Y() > 0.1 && hi.Y()-lo.Y() > 1 {
			continue // the +X wall
		}
		e.Brushes = append(e.Brushes, room[i])
	}
	lvl.Entities = append(lvl.Entities, e)
	ch := world.Chunk{ID: world.EntityGeometryChunkID(id), Scale: 1, FrameOwner: id}
	ch.FromEntity = id
	ch.Brushes = world.CloneEntityBrushes(e.Brushes)
	ch.RecomputeBounds()
	lvl.Chunks = append(lvl.Chunks, ch)
}

// runFramed is run with the frame publish the app does after EntityUpdate.
func runFramed(lvl *world.Level, seconds float32, events *[]hooks.AIEvent, until func() bool) {
	const dt = float32(1.0 / 60.0)
	for t := float32(0); t < seconds; t += dt {
		Update(lvl, dt, nil)
		collision.EntityUpdate(lvl, dt, true, nil)
		world.RefreshFrames(lvl, lvl.Frames)
		*events = append(*events, hooks.TakeAIEvents()...)
		if until != nil && until() {
			return
		}
	}
}

func TestAnAgentWalksAboardAParkedHullRidesItAndWalksOff(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{agent("squaddie", mgl32.Vec3{16, 0, 7})}
	hullEntity(lvl, "ship", mgl32.Vec3{4, 0, 4})
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	world.RefreshFrames(lvl, lvl.Frames)
	a := func() *world.Entity { return &lvl.Entities[0] }
	ship := func() *world.Entity { return &lvl.Entities[1] }

	// In through the open side, to the back of the hold.
	inside := mgl32.Vec3{5.5, 0, 7}
	if !MoveTo(lvl, a(), inside, false) {
		t.Fatal("the hold of a parked hull is not reachable")
	}
	var events []hooks.AIEvent
	runFramed(lvl, 10, &events, func() bool { return count(events, "ai_arrived") > 0 })
	if count(events, "ai_arrived") == 0 {
		t.Fatalf("never arrived in the hold; at %v, frame %q, events %v", Feet(lvl, a()), a().Frame, events)
	}
	if a().Frame != "ship" {
		t.Fatalf("stood in the hold without boarding it: frame %q", a().Frame)
	}

	// The ship lifts and flies: the agent is carried, and holds where it
	// stands even with somewhere to go.
	local := a().Position
	ship().Velocity = mgl32.Vec3{0, 1, 3}
	MoveTo(lvl, a(), mgl32.Vec3{16, 0, 16}, false)
	runFramed(lvl, 1.5, &events, nil)
	if a().Frame != "ship" {
		t.Fatalf("fell out of a moving ship: frame %q", a().Frame)
	}
	if d := a().Position.Sub(local).Len(); d > 0.3 {
		t.Errorf("walked %.2f about a moving ship's hold", d)
	}

	// Down again, turned: out it walks, to where it was told.
	ship().Velocity = mgl32.Vec3{}
	ship().Position = mgl32.Vec3{4, 0, 4}
	ship().Rotation = mgl32.Vec3{0, 0.3, 0}
	world.RefreshFrames(lvl, lvl.Frames)
	events = events[:0]
	runFramed(lvl, 15, &events, func() bool { return count(events, "ai_arrived") > 0 })
	if count(events, "ai_arrived") == 0 {
		t.Fatalf("never walked out; at %v, frame %q, events %v", Feet(lvl, a()), a().Frame, events)
	}
	if a().Frame != "" {
		t.Errorf("arrived outside still aboard: frame %q", a().Frame)
	}
}
