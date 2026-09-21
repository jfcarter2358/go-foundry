package ai

import (
	"testing"

	"foundry/internal/collision"
	"foundry/internal/hooks"
	"foundry/internal/leveltest"
	"foundry/internal/nav"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// aiLevel is a 20×20 hollow room, floor at 0 and ceiling at 4, with whatever
// else the test adds — built the way a loaded level is, bounds, spatial index
// and CPU meshes (which line-of-sight rays hit) included.
func aiLevel(t *testing.T, extra ...world.Brush) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	ch := &world.Chunk{ID: "main"}
	ch.Brushes = world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{20, 4, 20}, 0.5, "concrete")
	ch.Brushes = append(ch.Brushes, extra...)
	leveltest.LoadGeometry(t, ch)
	lvl := &world.Level{Chunks: []world.Chunk{*ch}}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	ResetSession()
	hooks.TakeAIEvents()
	t.Cleanup(func() {
		ResetSession()
		hooks.TakeAIEvents()
	})
	return lvl
}

// agent is a player-sized walker standing at feet, controller on with its
// defaults.
func agent(id string, feet mgl32.Vec3) world.Entity {
	spec := world.DefaultAISpec()
	return world.Entity{
		ID:           id,
		Position:     feet,
		Scale:        mgl32.Vec3{1, 1, 1},
		BoundingKind: world.BoundingCylinder,
		BoundHalfX:   0.3, BoundHalfY: 0.9, BoundHalfZ: 0.3,
		BoundOffset: mgl32.Vec3{0, 0.9, 0},
		Visible:     true,
		Collision:   world.EntityCollision{World: true, Gravity: true, Movable: true},
		AI:          &spec,
	}
}

// player is the engine's player with its feet at feet, installed as the
// level's player the way a level load installs it.
func player(t *testing.T, feet mgl32.Vec3) *world.Entity {
	t.Helper()
	prev := world.PlayerEntity
	t.Cleanup(func() { world.PlayerEntity = prev })
	p := world.NewPlayer(feet.Add(mgl32.Vec3{0, 0.9, 0}))
	world.PlayerEntity = p
	return p
}

// run advances the controllers and the bodies they drive for seconds of play,
// collecting every event the controllers post.
func run(lvl *world.Level, p *world.Entity, seconds float32, events *[]hooks.AIEvent) {
	const dt = float32(1.0 / 60.0)
	for t := float32(0); t < seconds; t += dt {
		Update(lvl, dt, p)
		collision.EntityUpdate(lvl, dt, true, p)
		*events = append(*events, hooks.TakeAIEvents()...)
	}
}

func count(events []hooks.AIEvent, atom string) int {
	n := 0
	for _, ev := range events {
		if ev.Atom == atom {
			n++
		}
	}
	return n
}

func TestNavigationAndCollisionAgreeOnTheSteepestFloor(t *testing.T) {
	if nav.MaxSlopeCos != collision.MaxSlopeCos {
		t.Errorf("nav.MaxSlopeCos = %v, collision.MaxSlopeCos = %v: agents would be routed onto slopes they slide off",
			nav.MaxSlopeCos, collision.MaxSlopeCos)
	}
}

func TestAnAgentWalksAroundAPillarToWhereItIsSent(t *testing.T) {
	pillar := world.NewBoxBrush("pillar", mgl32.Vec3{10, 2, 10}, mgl32.Vec3{3, 2, 3}, "concrete")
	lvl := aiLevel(t, pillar)
	lvl.Entities = []world.Entity{agent("guard", mgl32.Vec3{10, 0, 3})}
	goal := mgl32.Vec3{10, 0, 17}

	if !MoveTo(lvl, &lvl.Entities[0], goal) {
		t.Fatal("MoveTo called the far side of a pillar unreachable")
	}
	var events []hooks.AIEvent
	const dt = float32(1.0 / 60.0)
	for i := 0; i < 60*12; i++ {
		Update(lvl, dt, nil)
		collision.EntityUpdate(lvl, dt, true, nil)
		events = append(events, hooks.TakeAIEvents()...)
		f := Feet(lvl, &lvl.Entities[0])
		if f.X() > 7.1 && f.X() < 12.9 && f.Z() > 7.1 && f.Z() < 12.9 {
			t.Fatalf("the guard is inside the pillar at %v", f)
		}
		if count(events, "ai_arrived") > 0 {
			break
		}
	}
	if count(events, "ai_arrived") != 1 {
		t.Fatalf("the guard never arrived; it is at %v, events %v", Feet(lvl, &lvl.Entities[0]), events)
	}
	if d := distXZ(Feet(lvl, &lvl.Entities[0]), goal); d > 0.6 {
		t.Errorf("ai_arrived was posted %v from the goal", d)
	}
	if Mode(&lvl.Entities[0]) == world.AIMoving {
		t.Error("the command outlived its arrival")
	}
}

func TestAnAgentClimbsAStepOnItsWay(t *testing.T) {
	platform := world.NewBoxBrush("platform", mgl32.Vec3{15, 0.2, 10}, mgl32.Vec3{5, 0.2, 10}, "concrete")
	lvl := aiLevel(t, platform)
	lvl.Entities = []world.Entity{agent("guard", mgl32.Vec3{3, 0, 10})}
	MoveTo(lvl, &lvl.Entities[0], mgl32.Vec3{16, 0.4, 10})

	var events []hooks.AIEvent
	run(lvl, nil, 8, &events)
	f := Feet(lvl, &lvl.Entities[0])
	if f.X() < 12 || f.Y() < 0.35 {
		t.Fatalf("the guard did not get up a 0.4 step: it is at %v (events %v)", f, events)
	}
}

func TestAnAgentChasesTheTargetItSeesAndEngagesInRange(t *testing.T) {
	lvl := aiLevel(t)
	g := agent("guard", mgl32.Vec3{10, 0, 16})
	g.AI.Targets = []string{"player"}
	lvl.Entities = []world.Entity{g}
	// Straight ahead: a guard faces -Z.
	p := player(t, mgl32.Vec3{10, 0, 6})

	var events []hooks.AIEvent
	run(lvl, p, 0.5, &events)
	if count(events, "target_acquired") != 1 {
		t.Fatalf("a player ten metres straight ahead was not noticed: %v", events)
	}
	if got := Target(lvl, &lvl.Entities[0]); got != p {
		t.Fatalf("the guard's target is %v, want the player", got)
	}

	run(lvl, p, 6, &events)
	f := Feet(lvl, &lvl.Entities[0])
	if d := f.Sub(Feet(lvl, p)).Len(); d > g.AI.AttackRange+0.1 {
		t.Fatalf("the guard stopped %v from the player, want within its attack range %v", d, g.AI.AttackRange)
	}
	if n := count(events, "target_in_range"); n < 2 {
		t.Errorf("target_in_range was posted %d times over several seconds in range, want it repeated", n)
	}
	if Mode(&lvl.Entities[0]) != world.AIEngaging {
		t.Errorf("mode is %v, want engaging", world.AIModeName(Mode(&lvl.Entities[0])))
	}
}

func TestAWallAndTheFieldOfViewBothHideATarget(t *testing.T) {
	wall := world.NewBoxBrush("wall", mgl32.Vec3{10, 2, 11}, mgl32.Vec3{6, 2, 0.25}, "concrete")
	lvl := aiLevel(t, wall)
	g := agent("guard", mgl32.Vec3{10, 0, 16})
	g.AI.Targets = []string{"player"}
	lvl.Entities = []world.Entity{g}

	behindWall := player(t, mgl32.Vec3{10, 0, 6})
	if Perceives(lvl, &lvl.Entities[0], behindWall) {
		t.Error("the guard saw through a wall")
	}
	lvl.Entities[0].AI.RequireSight = false
	if !Perceives(lvl, &lvl.Entities[0], behindWall) {
		t.Error("with sight not required, a player in range and in view was not noticed — the test proves nothing")
	}
	lvl.Entities[0].AI.RequireSight = true

	behindGuard := player(t, mgl32.Vec3{10, 0, 19.5})
	behindGuard.Position[0] = 13 // outside hearing range, directly behind
	if Perceives(lvl, &lvl.Entities[0], behindGuard) {
		t.Error("the guard saw a player behind it with a 120-degree field of view")
	}
	lvl.Entities[0].AI.FOV = 360
	if !Perceives(lvl, &lvl.Entities[0], behindGuard) {
		t.Error("a 360-degree guard did not see the player behind it")
	}

	close := player(t, mgl32.Vec3{10.5, 0, 17.5})
	lvl.Entities[0].AI.FOV = 10
	if !Perceives(lvl, &lvl.Entities[0], close) {
		t.Error("a player within hearing range was not noticed")
	}
}

// Hearing notices a target through a wall and keeps track of it, but a heard
// target is not a visible one: nothing may open fire on it. Every enemy type
// hears a few metres, and used to shoot a player on the far side of a thin wall.
func TestATargetHeardThroughAWallIsNotEngaged(t *testing.T) {
	wall := world.NewBoxBrush("wall", mgl32.Vec3{10, 2, 14}, mgl32.Vec3{6, 2, 0.1}, "concrete")
	lvl := aiLevel(t, wall)
	g := agent("guard", mgl32.Vec3{10, 0, 15})
	g.AI.Targets = []string{"player"}
	g.AI.OnTarget = world.AIWatch // stay put: this is about sight, not the chase
	g.AI.HearingRange = 6
	g.AI.FOV = 360
	g.AI.AttackRange = 10
	lvl.Entities = []world.Entity{g}
	p := player(t, mgl32.Vec3{10, 0, 12.5})

	var events []hooks.AIEvent
	run(lvl, p, 3, &events)
	if count(events, "target_acquired") != 1 {
		t.Fatalf("a player heard through a wall was not noticed: %v", events)
	}
	if n := count(events, "target_in_range"); n != 0 {
		t.Fatalf("the guard engaged a player it could only hear through a wall (%d target_in_range)", n)
	}
	if Target(lvl, &lvl.Entities[0]) != p {
		t.Error("a heard target was not kept")
	}

	// Past the end of the wall, on the guard's side: now it is a line of fire.
	p.Position = mgl32.Vec3{19, 0.9, 14.5}
	run(lvl, p, 1, &events)
	if count(events, "target_in_range") == 0 {
		t.Fatalf("the guard did not engage a player in plain sight: %v", events)
	}
}

func TestATargetOutOfSightIsLostAfterLoseTime(t *testing.T) {
	wall := world.NewBoxBrush("wall", mgl32.Vec3{5, 2, 10}, mgl32.Vec3{5, 2, 0.25}, "concrete")
	lvl := aiLevel(t, wall)
	g := agent("guard", mgl32.Vec3{15, 0, 16})
	g.AI.Targets = []string{"player"}
	g.AI.OnTarget = world.AIWatch // stay put: this is about sight, not the chase
	g.AI.LoseTime = 1
	lvl.Entities = []world.Entity{g}
	p := player(t, mgl32.Vec3{15, 0, 8})

	var events []hooks.AIEvent
	run(lvl, p, 0.5, &events)
	if count(events, "target_acquired") != 1 {
		t.Fatalf("the player was not noticed: %v", events)
	}
	// Behind the wall's far side, well out of hearing.
	p.Position = mgl32.Vec3{3, 0.9, 5}
	run(lvl, p, 0.6, &events)
	if count(events, "target_lost") != 0 {
		t.Fatal("the target was given up before LoseTime")
	}
	run(lvl, p, 1.0, &events)
	if count(events, "target_lost") != 1 {
		t.Fatalf("the target was not given up after LoseTime out of sight: %v", events)
	}
	if Target(lvl, &lvl.Entities[0]) != nil {
		t.Error("the lost target is still the guard's target")
	}
}

func objectiveVolume(id string, centre mgl32.Vec3, obj world.VolumeObjective) world.Volume {
	v := world.NewVolume(id, centre)
	v.HalfX, v.HalfY, v.HalfZ = 1.5, 1, 1.5
	v.Objective = &obj
	return v
}

func TestAnAgentPerformsAnObjectiveAndBothSidesHear(t *testing.T) {
	lvl := aiLevel(t)
	obj := world.DefaultVolumeObjective()
	obj.Action = ":hack:"
	obj.Duration = 1
	lvl.Volumes = []world.Volume{objectiveVolume("console", mgl32.Vec3{15, 1, 15}, obj)}
	g := agent("engineer", mgl32.Vec3{4, 0, 4})
	g.AI.Objectives = []string{"console"}
	lvl.Entities = []world.Entity{g}

	var events []hooks.AIEvent
	run(lvl, nil, 12, &events)

	order := []string{}
	for _, ev := range events {
		switch ev.Atom {
		case "objective_reached", "hack", "objective_complete", "objectives_done":
			order = append(order, ev.Atom)
			if ev.Atom != "objectives_done" && (ev.Objective != "console" || !ev.ToObjective) {
				t.Errorf(":%s: was not addressed to the console as well as the agent: %+v", ev.Atom, ev)
			}
		}
	}
	want := []string{"objective_reached", "hack", "objective_complete", "objectives_done"}
	if len(order) != len(want) {
		t.Fatalf("objective events were %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("objective events were %v, want %v", order, want)
		}
	}
	if !lvl.Volumes[0].ContainsWorldPoint(lvl, Feet(lvl, &lvl.Entities[0]).Add(mgl32.Vec3{0, 0.05, 0})) {
		t.Errorf("the engineer performed the objective from %v, outside the volume", Feet(lvl, &lvl.Entities[0]))
	}
}

func TestAPatrolLoopsThroughItsObjectivesInOrder(t *testing.T) {
	lvl := aiLevel(t)
	obj := world.DefaultVolumeObjective()
	obj.Duration = 0.2
	for _, v := range []struct {
		id string
		at mgl32.Vec3
	}{{"a", mgl32.Vec3{4, 1, 4}}, {"b", mgl32.Vec3{16, 1, 4}}, {"c", mgl32.Vec3{16, 1, 16}}} {
		vol := objectiveVolume(v.id, v.at, obj)
		vol.Tags = []string{"route"}
		lvl.Volumes = append(lvl.Volumes, vol)
	}
	g := agent("guard", mgl32.Vec3{10, 0, 10})
	g.AI.Objectives = []string{"#route"}
	g.AI.Loop = true
	lvl.Entities = []world.Entity{g}

	var events []hooks.AIEvent
	run(lvl, nil, 30, &events)
	var done []string
	for _, ev := range events {
		if ev.Atom == "objective_complete" {
			done = append(done, ev.Objective)
		}
	}
	if len(done) < 4 {
		t.Fatalf("only %v were completed in 30 seconds", done)
	}
	for i, id := range done {
		if want := []string{"a", "b", "c"}[i%3]; id != want {
			t.Fatalf("patrol order was %v, want a, b, c repeating", done)
		}
	}
	if count(events, "objectives_done") != 0 {
		t.Error("a looping patrol reported its objectives done")
	}
}

func TestCapacityMakesASecondAgentWaitItsTurn(t *testing.T) {
	lvl := aiLevel(t)
	obj := world.DefaultVolumeObjective()
	obj.Duration = 3
	obj.Capacity = 1
	lvl.Volumes = []world.Volume{objectiveVolume("post", mgl32.Vec3{10, 1, 10}, obj)}
	a, b := agent("a", mgl32.Vec3{3, 0, 3}), agent("b", mgl32.Vec3{17, 0, 17})
	a.AI.Objectives = []string{"post"}
	b.AI.Objectives = []string{"post"}
	lvl.Entities = []world.Entity{a, b}

	var events []hooks.AIEvent
	const dt = float32(1.0 / 60.0)
	for i := 0; i < 60*20; i++ {
		Update(lvl, dt, nil)
		collision.EntityUpdate(lvl, dt, true, nil)
		events = append(events, hooks.TakeAIEvents()...)
		if ObjectiveAgents(lvl, &lvl.Volumes[0]) > 1 {
			t.Fatalf("two agents are working an objective with capacity 1 at %.2fs", float32(i)*dt)
		}
	}
	if n := count(events, "objective_complete"); n != 2 {
		t.Errorf("objective_complete was posted %d times, want once for each agent in turn", n)
	}
}

func TestOnceSwitchesAnObjectiveOffAfterItIsDone(t *testing.T) {
	lvl := aiLevel(t)
	obj := world.DefaultVolumeObjective()
	obj.Duration = 0
	obj.Once = true
	lvl.Volumes = []world.Volume{objectiveVolume("switch", mgl32.Vec3{8, 1, 8}, obj)}
	g := agent("guard", mgl32.Vec3{4, 0, 4})
	g.AI.Objectives = []string{"switch"}
	g.AI.Loop = true
	lvl.Entities = []world.Entity{g}

	var events []hooks.AIEvent
	run(lvl, nil, 8, &events)
	if n := count(events, "objective_complete"); n != 1 {
		t.Fatalf("a once-only objective was completed %d times", n)
	}
	if lvl.Volumes[0].Objective.Enabled {
		t.Error("the once-only objective is still enabled after being done")
	}
}

func TestADisabledControllerStopsTheAgent(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{agent("guard", mgl32.Vec3{3, 0, 3})}
	MoveTo(lvl, &lvl.Entities[0], mgl32.Vec3{17, 0, 17})
	var events []hooks.AIEvent
	run(lvl, nil, 0.5, &events)
	if v := lvl.Entities[0].Velocity; v.X()*v.X()+v.Z()*v.Z() < 0.1 {
		t.Fatal("the guard is not walking — the test proves nothing")
	}

	lvl.Entities[0].AI.Enabled = false
	run(lvl, nil, 0.1, &events)
	if v := lvl.Entities[0].Velocity; v.X() != 0 || v.Z() != 0 {
		t.Errorf("a disabled guard is still moving at %v", v)
	}
	if Mode(&lvl.Entities[0]) != world.AIDisabled {
		t.Errorf("mode is %v, want disabled", world.AIModeName(Mode(&lvl.Entities[0])))
	}
}

func TestANewSessionStartsEveryAgentAfresh(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{agent("guard", mgl32.Vec3{3, 0, 3})}
	MoveTo(lvl, &lvl.Entities[0], mgl32.Vec3{17, 0, 17})
	var events []hooks.AIEvent
	run(lvl, nil, 0.2, &events)

	ResetSession()
	run(lvl, nil, 0.2, &events)
	if lvl.Entities[0].AIState.Command {
		t.Error("a command from the last play session carried into the next")
	}
}
