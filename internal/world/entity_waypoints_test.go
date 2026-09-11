package world

// entity_waypoints_test.go — waypoint animations an entity's type carries.
//
// The promise that matters is the one a moving ship tests: a path written in
// the entity's space is travelled in it, so a hatch sent shut closes against the
// hull wherever the hull has gone — including on a tick where the entity has
// moved and its frame has not yet been republished, which is every tick
// AdvanceBrushes runs on.

import (
	"reflect"
	"testing"

	"foundry/internal/hooks"

	"github.com/go-gl/mathgl/mgl32"
)

// carriedLevel is a ship at {100 0 0}, turned a quarter about Y, that owns a
// chunk holding a dynamic hatch centred on its local {2 1 0}, and one carried
// path "ship.close" with a single point at local {0 1 0}, reached in 2 s.
func carriedLevel(t *testing.T) (*Level, *Chunk, *Brush) {
	t.Helper()
	hooks.TakeWaypointEvents()
	t.Cleanup(func() { hooks.TakeWaypointEvents() })
	hatch := NewBoxBrush("ship.hatch", mgl32.Vec3{2, 1, 0}, mgl32.Vec3{0.1, 1, 1}, "")
	hatch.Dynamic = true
	lvl := &Level{
		Chunks: []Chunk{{ID: "ship.geometry", FrameOwner: "ship", FromEntity: "ship", Scale: 1, Brushes: []Brush{hatch}}},
		Entities: []Entity{{
			ID: "ship", Position: mgl32.Vec3{100, 0, 0}, Rotation: mgl32.Vec3{0, mgl32.DegToRad(90), 0},
			Scale: mgl32.Vec3{1, 1, 1},
		}},
		WaypointAnimations: []WaypointAnimation{{
			ID: "ship.close", FromEntity: "ship",
			Points: []Waypoint{{Position: mgl32.Vec3{0, 1, 0}, Scale: mgl32.Vec3{1, 1, 1}, Time: 2}},
		}},
	}
	fs, err := BuildFrames(lvl)
	if err != nil {
		t.Fatalf("BuildFrames: %v", err)
	}
	lvl.Frames = fs
	RefreshFrames(lvl, fs)
	return lvl, &lvl.Chunks[0], &lvl.Chunks[0].Brushes[0]
}

func TestACarriedPathIsTravelledInItsEntitysSpace(t *testing.T) {
	lvl, ch, hatch := carriedLevel(t)
	anim := &lvl.WaypointAnimations[0]

	// The space is the one the hatch's chunk is drawn through.
	local := mgl32.Vec3{0, 1, 0}
	if got, want := mulPoint(anim.SpaceMatrix(lvl), local), ch.LocalToWorld(local); !vecNear(got, want, 1e-4) {
		t.Fatalf("the path's space carries %v to %v; the hatch's chunk carries it to %v", local, got, want)
	}

	PlayWaypoints(lvl, BrushWaypointFollower(ch, hatch), anim)
	advanceBrushWaypoints(lvl, ch, hatch, 1)
	if got := hatch.Position(); !vecNear(got, mgl32.Vec3{1, 1, 0}, 1e-4) {
		t.Errorf("halfway, the hatch is at local %v; want {1 1 0}, halfway from where it stood to the point", got)
	}

	// The ship climbs, and the hatch steps before the frames are republished —
	// AdvanceBrushes' place in the tick. The hatch must not slide along the hull
	// by the distance the ship moved.
	lvl.Entities[0].Position = mgl32.Vec3{100, 50, 0}
	advanceBrushWaypoints(lvl, ch, hatch, 0.5)
	if got := hatch.Position(); !vecNear(got, mgl32.Vec3{0.5, 1, 0}, 1e-4) {
		t.Errorf("after the ship moved, the hatch is at local %v; want {0.5 1 0} — a carried path is travelled in the ship's space", got)
	}

	RefreshFrames(lvl, lvl.Frames)
	advanceBrushWaypoints(lvl, ch, hatch, 1)
	if got := hatch.Position(); !vecNear(got, local, 1e-4) {
		t.Errorf("at the end, the hatch is at local %v; want the point's %v", got, local)
	}
	want := mgl32.Vec3{100, 51, 0}
	if got := ch.LocalToWorld(hatch.Position()); !vecNear(got, want, 1e-3) {
		t.Errorf("at the end, the hatch is at world %v; want %v, where the climbing ship has the point", got, want)
	}
}

func TestAnEntityCannotFollowAPathItCarries(t *testing.T) {
	lvl, ch, hatch := carriedLevel(t)
	anim := &lvl.WaypointAnimations[0]
	if !WaypointFollowerCarriesAnimation(&lvl.Entities[0], anim) {
		t.Error("the ship was allowed to follow its own path, which would chase itself off")
	}
	if WaypointFollowerCarriesAnimation(BrushWaypointFollower(ch, hatch), anim) {
		t.Error("the ship's hatch was refused its own ship's path")
	}
	if got := WaypointFollowerCarrier(BrushWaypointFollower(ch, hatch)); got != "ship" {
		t.Errorf("the hatch's carrier is %q, want ship", got)
	}
}

func TestMaterialisingAndDroppingAnEntitysPaths(t *testing.T) {
	e := Entity{ID: "ship", WaypointAnimations: []WaypointAnimation{{
		ID: "close", Points: []Waypoint{{Position: mgl32.Vec3{0, 1, 0}, Scale: mgl32.Vec3{1, 1, 1}, Time: 1}},
	}}}
	lvl := &Level{WaypointAnimations: []WaypointAnimation{{ID: "lift"}}}

	if n := MaterializeEntityWaypointAnimations(lvl, &e); n != 1 {
		t.Fatalf("materialised %d paths, want 1", n)
	}
	a := FindWaypointAnimation(lvl, "ship.close")
	if a == nil || a.FromEntity != "ship" {
		t.Fatalf("no ship.close riding the ship among %+v", lvl.WaypointAnimations)
	}
	a.Points[0].Position = mgl32.Vec3{9, 9, 9}
	if e.WaypointAnimations[0].Points[0].Position != (mgl32.Vec3{0, 1, 0}) {
		t.Error("the live path shares its points with the entity's template")
	}
	if got := FindCarriedWaypointAnimationNamed(lvl, "close"); got != a {
		t.Errorf("looking up the carried name found %v, want ship.close", got)
	}

	if n := DropEntityWaypointAnimations(lvl, "ship"); n != 1 {
		t.Errorf("dropped %d paths, want 1", n)
	}
	if len(lvl.WaypointAnimations) != 1 || lvl.WaypointAnimations[0].ID != "lift" {
		t.Errorf("after the drop the level holds %+v; want only its own lift", lvl.WaypointAnimations)
	}
}

func TestATypesPathsRoundTripAndPlacementsDoNotRestateThem(t *testing.T) {
	raw := RawEntityDef{ID: "ship", WaypointAnimations: []RawWaypointAnimation{{
		ID: "close", Length: 3,
		Points: []RawWaypoint{{Position: RawVec3{X: 1, Y: 2}, Time: 2, Events: []string{"shut"}}},
	}}}
	def, err := entityDefFromRaw(raw, "ship")
	if err != nil {
		t.Fatalf("entityDefFromRaw: %v", err)
	}
	if back := entityDefToRaw(def); !reflect.DeepEqual(back.WaypointAnimations, raw.WaypointAnimations) {
		t.Errorf("the definition wrote back %+v, want %+v", back.WaypointAnimations, raw.WaypointAnimations)
	}

	e := def.Instantiate("ship_2")
	if len(e.WaypointAnimations) != 1 {
		t.Fatalf("the placement carries %d paths, want the type's 1", len(e.WaypointAnimations))
	}
	e.WaypointAnimations[0].Points[0].Time = 0.5
	if def.WaypointAnimations[0].Points[0].Time != 2 {
		t.Error("retiming a placement's path retimed the type's")
	}
	e.WaypointAnimations[0].Points[0].Time = 2
	if r := EntityToRaw(&e, def); len(r.WaypointAnimations) != 0 {
		t.Errorf("an untouched placement restated its type's paths: %+v", r.WaypointAnimations)
	}

	// A type that changes its path takes every placement with it.
	next := def.Clone()
	next.WaypointAnimations[0].Points[0].Time = 1
	fresh, err := RestampEntity(&e, def, next)
	if err != nil {
		t.Fatalf("RestampEntity: %v", err)
	}
	if got := fresh.WaypointAnimations[0].Points[0].Time; got != 1 {
		t.Errorf("the re-stamped placement's point is at %v s, want the new type's 1", got)
	}

	raw.WaypointAnimations = append(raw.WaypointAnimations, raw.WaypointAnimations[0])
	if _, err := entityDefFromRaw(raw, "ship"); err == nil {
		t.Error("a type with two paths called close loaded without complaint")
	}
}
