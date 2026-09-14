package ai

import (
	"testing"

	"foundry/internal/collision"
	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// path builds a waypoint animation through the given feet positions.
func path(id string, pts ...mgl32.Vec3) world.WaypointAnimation {
	a := world.WaypointAnimation{ID: id}
	for _, p := range pts {
		a.Points = append(a.Points, world.NewWaypoint(p))
	}
	return a
}

// runCollect steps the controllers and bodies, returning every waypoint and AI
// event recorded, and calls each(t) after every frame if given.
func runCollect(lvl *world.Level, p *world.Entity, seconds float32, each func(t float32) bool) ([]hooks.WaypointEvent, []hooks.AIEvent) {
	const dt = float32(1.0 / 60.0)
	var wps []hooks.WaypointEvent
	var evs []hooks.AIEvent
	for t := float32(0); t < seconds; t += dt {
		Update(lvl, dt, p)
		collision.EntityUpdate(lvl, dt, true, p)
		wps = append(wps, hooks.TakeWaypointEvents()...)
		evs = append(evs, hooks.TakeAIEvents()...)
		if each != nil && each(t) {
			break
		}
	}
	return wps, evs
}

func TestAnAgentWalksAPathStopByStopAndWaitsWhereTold(t *testing.T) {
	lvl := aiLevel(t)
	hooks.TakeWaypointEvents()
	a := path("route", mgl32.Vec3{4, 0, 4}, mgl32.Vec3{16, 0, 4}, mgl32.Vec3{16, 0, 16})
	a.Points[1].AI = &world.WaypointAI{Wait: 1.5}
	a.Points[2].Events = []string{"at_end"}
	lvl.WaypointAnimations = []world.WaypointAnimation{a}
	g := agent("guard", mgl32.Vec3{10, 0, 10})
	g.AI.Waypoints = "route"
	lvl.Entities = []world.Entity{g}

	var arrivedAt1, leftAt1 float32 = -1, -1
	wps, _ := runCollect(lvl, nil, 25, func(tm float32) bool {
		_, idx := Route(&lvl.Entities[0])
		if arrivedAt1 < 0 && idx == 1 && lvl.Entities[0].AIState.RouteArrived {
			arrivedAt1 = tm
		}
		if arrivedAt1 >= 0 && leftAt1 < 0 && idx == 2 {
			leftAt1 = tm
		}
		return false
	})

	var order []int
	ended := false
	for _, ev := range wps {
		if ev.Object != "guard" || ev.Animation != "route" {
			t.Fatalf("a waypoint event was addressed to %+v", ev)
		}
		if ev.Ended {
			ended = true
			continue
		}
		order = append(order, ev.Index)
		if ev.Index == 2 && (len(ev.Events) != 1 || ev.Events[0] != "at_end") {
			t.Errorf("the last point's own events did not come with its arrival: %+v", ev)
		}
	}
	if len(order) != 3 || order[0] != 0 || order[1] != 1 || order[2] != 2 || !ended {
		t.Fatalf("arrivals were %v (ended %v), want 0, 1, 2 then the end", order, ended)
	}
	if wait := leftAt1 - arrivedAt1; wait < 1.4 {
		t.Errorf("the guard stayed %.2fs at a point that says wait 1.5", wait)
	}
	if _, idx := Route(&lvl.Entities[0]); idx != -1 {
		t.Error("a path that does not loop is still being walked after its end")
	}
	if d := distXZ(Feet(lvl, &lvl.Entities[0]), mgl32.Vec3{16, 0, 16}); d > 1 {
		t.Errorf("the guard finished %v from the last point", d)
	}
}

func TestAVolumeStopIsReachedAnywhereInsideIt(t *testing.T) {
	lvl := aiLevel(t)
	hooks.TakeWaypointEvents()
	area := world.NewVolume("yard", mgl32.Vec3{15, 1, 15})
	area.HalfX, area.HalfY, area.HalfZ = 2, 1, 2
	lvl.Volumes = []world.Volume{area}
	a := path("route", mgl32.Vec3{4, 0, 4}, mgl32.Vec3{14, 0, 14})
	a.Points[1].AI = &world.WaypointAI{Volume: "yard", WaitForSignal: true}
	lvl.WaypointAnimations = []world.WaypointAnimation{a}
	lvl.Entities = []world.Entity{agent("guard", mgl32.Vec3{3, 0, 10})}
	FollowWaypoints(lvl, &lvl.Entities[0], &lvl.WaypointAnimations[0], false)

	runCollect(lvl, nil, 15, nil)
	g := &lvl.Entities[0]
	if !insideVolume(lvl, g, &lvl.Volumes[0], Feet(lvl, g)) {
		t.Fatalf("the guard is at %v, outside the volume its stop names", Feet(lvl, g))
	}
	if !Waiting(g) {
		t.Fatalf("a stop waiting for a signal is not waiting; mode %v", world.AIModeName(Mode(g)))
	}

	// Still there long after, until told.
	runCollect(lvl, nil, 3, nil)
	if _, idx := Route(g); idx != 1 {
		t.Fatal("the guard left a wait-for-signal stop by itself")
	}
	if !ContinueWaypoints(lvl, g) {
		t.Fatal("ai_continue was refused by a waiting agent")
	}
	wps, _ := runCollect(lvl, nil, 0.2, nil)
	if len(wps) == 0 || !wps[len(wps)-1].Ended {
		t.Errorf("the signal did not release the guard off the end of its path: %+v", wps)
	}
}

// coverLevel is the room with a long wall across it at z=10, x 4..16.
func coverLevel(t *testing.T) *world.Level {
	wall := world.NewBoxBrush("wall", mgl32.Vec3{10, 1.5, 10}, mgl32.Vec3{6, 1.5, 0.3}, "")
	lvl := aiLevel(t, wall)
	hooks.TakeWaypointEvents()
	return lvl
}

func TestAnAgentTakesCoverFromItsTargetAndStaysWhileThreatened(t *testing.T) {
	lvl := coverLevel(t)
	// A stop out in the open past the end of the wall, with cover a few metres
	// off behind the wall from a player on its far side.
	stop := mgl32.Vec3{19, 0, 8}
	a := path("assault", stop)
	a.Points[0].AI = &world.WaypointAI{TakeCover: true, CoverRadius: 7}
	lvl.WaypointAnimations = []world.WaypointAnimation{a}
	g := agent("soldier", mgl32.Vec3{2, 0, 2})
	g.AI.Targets = []string{"player"}
	g.AI.FOV = 360
	g.AI.RequireSight = false // it knows where the player is either way
	g.AI.SightRange = 40
	g.AI.LoseTime = 100
	g.AI.Waypoints = "assault"
	lvl.Entities = []world.Entity{g}
	p := player(t, mgl32.Vec3{10, 0, 17})
	if HiddenFrom(lvl, &lvl.Entities[0], stop, eyeOf(lvl, p), p.ID) {
		t.Fatal("the stop itself is hidden from the player — the test would pass without the soldier moving")
	}

	_, evs := runCollect(lvl, p, 12, nil)
	e := &lvl.Entities[0]
	if d := distXZ(Feet(lvl, e), stop); d < 1 {
		t.Errorf("the soldier took cover %v from an exposed stop; it has not moved to any", d)
	}
	if count(evs, "cover_taken") != 1 {
		t.Fatalf("cover_taken was posted %d times; events %v, guard at %v mode %v",
			count(evs, "cover_taken"), evs, Feet(lvl, e), world.AIModeName(Mode(e)))
	}
	if !InCover(e) || Mode(e) != world.AIInCover {
		t.Fatalf("the soldier is not in cover: mode %v", world.AIModeName(Mode(e)))
	}
	if !HiddenFrom(lvl, e, Feet(lvl, e), eyeOf(lvl, p), p.ID) {
		t.Errorf("the soldier's cover at %v does not hide it from the player at %v", Feet(lvl, e), p.Position)
	}
	if f := Feet(lvl, e); f.Z() > 9.7 {
		t.Errorf("the soldier took cover on the player's side of the wall: %v", f)
	}

	// The player walks round the end of the wall: the cover is broken and the
	// soldier finds more.
	p.Position = mgl32.Vec3{1.5, 0.9, 6}
	_, evs = runCollect(lvl, p, 6, nil)
	if count(evs, "cover_lost") == 0 {
		t.Errorf("the player walking round the wall did not break cover: %v", evs)
	}
}

func TestCoverWithNoTargetHidesFromTheWayTheArrowFaces(t *testing.T) {
	lvl := coverLevel(t)
	a := path("post", mgl32.Vec3{10, 0, 6})
	// Arrow +X turned to face +Z: toward the wall and past it.
	a.Points[0].Rotation = mgl32.Vec3{0, -1.5707964, 0}
	a.Points[0].AI = &world.WaypointAI{TakeCover: true, Wait: 30}
	lvl.WaypointAnimations = []world.WaypointAnimation{a}
	g := agent("soldier", mgl32.Vec3{3, 0, 3})
	g.AI.Waypoints = "post"
	lvl.Entities = []world.Entity{g}

	fwd := lvl.WaypointAnimations[0].Forward(lvl, 0)
	if fwd.Z() < 0.99 {
		t.Fatalf("the arrow faces %v; the test means it to face +Z", fwd)
	}
	_, evs := runCollect(lvl, nil, 10, nil)
	e := &lvl.Entities[0]
	if count(evs, "cover_taken") != 1 || !InCover(e) {
		t.Fatalf("no cover taken with no target: events %v, mode %v", evs, world.AIModeName(Mode(e)))
	}
	threat := mgl32.Vec3{10, 1.6, 6 + coverThreatDistance}
	if !HiddenFrom(lvl, e, Feet(lvl, e), threat, "") {
		t.Errorf("cover at %v does not hide from the way the arrow faces", Feet(lvl, e))
	}
}

func TestHoldKeepsToThePathWhileEngaging(t *testing.T) {
	lvl := aiLevel(t)
	hooks.TakeWaypointEvents()
	a := path("sweep", mgl32.Vec3{4, 0, 10}, mgl32.Vec3{16, 0, 10})
	// Both legs: the soldier starts on the first point, and a point's setting
	// governs arriving at it.
	a.Points[0].AI = &world.WaypointAI{Engage: world.AIEngageHold}
	a.Points[1].AI = &world.WaypointAI{Engage: world.AIEngageHold}
	lvl.WaypointAnimations = []world.WaypointAnimation{a}
	g := agent("soldier", mgl32.Vec3{4, 0, 10})
	g.AI.Targets = []string{"player"}
	g.AI.FOV = 360
	g.AI.AttackRange = 8
	g.AI.Waypoints = "sweep"
	lvl.Entities = []world.Entity{g}
	// Close beside the sweep, never on it.
	p := player(t, mgl32.Vec3{10, 0, 14})

	wps, evs := runCollect(lvl, p, 10, nil)
	reached := false
	for _, ev := range wps {
		if ev.Index == 1 && !ev.Ended {
			reached = true
		}
	}
	if !reached {
		t.Fatalf("the soldier left its path for the player on a hold leg; it is at %v", Feet(lvl, &lvl.Entities[0]))
	}
	if count(evs, "target_in_range") == 0 {
		t.Error("holding to the path, the soldier never engaged a player in range")
	}
}

func TestALoopingPathStartsAgain(t *testing.T) {
	lvl := aiLevel(t)
	hooks.TakeWaypointEvents()
	lvl.WaypointAnimations = []world.WaypointAnimation{path("loop", mgl32.Vec3{5, 0, 5}, mgl32.Vec3{15, 0, 5})}
	g := agent("guard", mgl32.Vec3{10, 0, 10})
	g.AI.Waypoints = "loop"
	g.AI.WaypointsLoop = true
	lvl.Entities = []world.Entity{g}

	wps, _ := runCollect(lvl, nil, 20, nil)
	n := 0
	for _, ev := range wps {
		if !ev.Ended && ev.Index == 0 {
			n++
		}
	}
	if n < 2 {
		t.Fatalf("the first point was reached %d times in twenty seconds of a loop", n)
	}
	if _, idx := Route(&lvl.Entities[0]); idx < 0 {
		t.Error("a looping path stopped being walked")
	}
}

func TestATypesPathIsFoundByItsName(t *testing.T) {
	lvl := aiLevel(t)
	lvl.WaypointAnimations = []world.WaypointAnimation{path(world.EntityWaypointAnimationID("guard", "beat"), mgl32.Vec3{5, 0, 5})}
	g := agent("guard", mgl32.Vec3{10, 0, 10})
	if a := ResolveWaypoints(lvl, &g, "beat"); a == nil || a.ID != "guard.beat" {
		t.Fatalf("the name a type gives its path resolved to %+v", a)
	}
}
