package world

// waypoint_follow_test.go — the kinds besides a brush that a waypoint
// animation can carry.
//
// The timing, the announcing and PoseAt are waypoint_test.go's, checked on a
// brush; they are one piece of code for every kind. What is asserted here is
// each kind's own half: which channels of a point it reads, which space its
// position is divided into, and what its arrivals are announced as.

import (
	"math"
	"reflect"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/hooks"

	"github.com/go-gl/mathgl/mgl32"
)

// followLevel is a level holding nothing but an animation called "path".
func followLevel(points ...Waypoint) *Level {
	hooks.TakeWaypointEvents()
	return &Level{WaypointAnimations: []WaypointAnimation{{ID: "path", Points: points}}}
}

func TestAnEntityTravelsAPathOnAllThreeChannels(t *testing.T) {
	to := wp(mgl32.Vec3{4, 0, 0}, 2)
	to.Rotation = mgl32.Vec3{0, math.Pi, 0}
	to.Scale = mgl32.Vec3{3, 1, 1}
	lvl := followLevel(to)
	lvl.Entities = []Entity{{ID: "crate", Scale: mgl32.Vec3{1, 1, 1}}}
	e := &lvl.Entities[0]
	PlayWaypoints(lvl, e, &lvl.WaypointAnimations[0])

	e.AdvanceWaypoints(lvl, 1)
	if !vecNear(e.Position, mgl32.Vec3{2, 0, 0}, 1e-4) || !nearF(e.Rotation.Y(), math.Pi/2, 1e-4) ||
		!vecNear(e.EffectiveScale(), mgl32.Vec3{2, 1, 1}, 1e-4) {
		t.Errorf("halfway, the entity is at %v turned %v scaled %v; want every channel half way",
			e.Position, e.Rotation, e.EffectiveScale())
	}
	e.AdvanceWaypoints(lvl, 1)
	if e.Waypoints != nil {
		t.Error("the entity is still following after the length ran out")
	}
	want := []hooks.WaypointEvent{
		{Kind: hooks.WaypointEntity, Object: "crate", Animation: "path", Index: 0},
		{Kind: hooks.WaypointEntity, Object: "crate", Animation: "path", Index: 0, Ended: true},
	}
	if got := hooks.TakeWaypointEvents(); !reflect.DeepEqual(got, want) {
		t.Errorf("announced %+v, want %+v — the arrivals must say they came from an entity", got, want)
	}
}

// A path and a tween both write the whole placement, so each drops the other,
// for an entity as for a brush.
func TestAnEntitysTweensAndWaypointsCancelEachOther(t *testing.T) {
	lvl := followLevel(wp(mgl32.Vec3{4, 0, 0}, 2))
	lvl.Entities = []Entity{{ID: "crate"}}
	e := &lvl.Entities[0]

	e.Ease(nil, TweenMove, mgl32.Vec3{0, 9, 0}, 1)
	PlayWaypoints(lvl, e, &lvl.WaypointAnimations[0])
	if e.Tweens.Active() {
		t.Error("sending the entity along a path left its tween running")
	}
	e.Ease(nil, TweenRotate, mgl32.Vec3{0, 1, 0}, 1)
	if e.Waypoints != nil {
		t.Error("easing the entity left it following the path")
	}
}

// Position is world space for every kind. An entity aboard a frame stores its
// position in the frame's coordinates, so the path has to be divided through
// the frame to meet the arrow.
func TestAnEntityAboardAFrameMeetsTheArrowInTheWorld(t *testing.T) {
	lvl := attachLevel(t) // npc aboard the ship at {100 0 0}, ten units forward
	lvl.WaypointAnimations = []WaypointAnimation{{ID: "path", Points: []Waypoint{wp(mgl32.Vec3{120, 0, 0}, 2)}}}
	npc := FindEntity(lvl, "npc")
	PlayWaypoints(lvl, npc, &lvl.WaypointAnimations[0])

	npc.AdvanceWaypoints(lvl, 1)
	if got := lvl.Frames.WorldPosition(npc); !vecNear(got, mgl32.Vec3{115, 0, 0}, 1e-4) {
		t.Errorf("halfway, the npc is at %v in the world, want {115 0 0}", got)
	}
	if !vecNear(npc.Position, mgl32.Vec3{15, 0, 0}, 1e-4) {
		t.Errorf("the npc's own position is %v, want {15 0 0} in the ship's coordinates", npc.Position)
	}
	hooks.TakeWaypointEvents()
}

// An attached sprite's Position is its carrier's local space, and the arrow is
// in the world's; the path is divided through the carrier every tick.
func TestAnAttachedSpriteMeetsTheArrowInTheWorld(t *testing.T) {
	lvl := attachLevel(t)
	s := NewSprite("torch", mgl32.Vec3{0, 2, 0})
	s.Attach = Attach{Entity: "npc"}
	lvl.Sprites = []Sprite{s}
	sp := &lvl.Sprites[0]
	start := sp.WorldPosition(lvl)
	lvl.WaypointAnimations = []WaypointAnimation{{ID: "path", Points: []Waypoint{wp(start.Add(mgl32.Vec3{0, 4, 0}), 2)}}}
	PlayWaypoints(lvl, sp, &lvl.WaypointAnimations[0])

	AdvanceWaypointFollowers(lvl, 1)
	if got := sp.WorldPosition(lvl); !vecNear(got, start.Add(mgl32.Vec3{0, 2, 0}), 1e-4) {
		t.Errorf("halfway, the torch is at %v in the world, want %v", got, start.Add(mgl32.Vec3{0, 2, 0}))
	}
	if !vecNear(sp.Position, mgl32.Vec3{0, 4, 0}, 1e-4) {
		t.Errorf("the torch's own position is %v, want {0 4 0} on the npc", sp.Position)
	}
	hooks.TakeWaypointEvents()
}

// A sprite has a size to grow: a point's scale multiplies its width by X and
// its height by Y, and everything that draws or bounds the quad sees it.
func TestASpriteOnAPathScalesItsQuad(t *testing.T) {
	to := wp(mgl32.Vec3{}, 2)
	to.Scale = mgl32.Vec3{3, 2, 1}
	lvl := followLevel(to)
	s := NewSprite("torch", mgl32.Vec3{})
	s.Width, s.Height = 2, 1
	lvl.Sprites = []Sprite{s}
	sp := &lvl.Sprites[0]
	rest := CaptureWaypointRest(sp)
	_, hiBefore, _ := sp.WorldAABB(lvl)
	PlayWaypoints(lvl, sp, &lvl.WaypointAnimations[0])

	AdvanceWaypointFollowers(lvl, 1)
	if got := sp.DrawnSize(); !nearF(got.X(), 4, 1e-4) || !nearF(got.Y(), 1.5, 1e-4) {
		t.Errorf("halfway to <3, 2> the 2×1 sprite is drawn %v, want 4×1.5", got)
	}
	if sp.Width != 2 || sp.Height != 1 {
		t.Errorf("the path rewrote the authored size to %v×%v; it should scale on top of it", sp.Width, sp.Height)
	}
	if _, hi, _ := sp.WorldAABB(lvl); hi.X() <= hiBefore.X() {
		t.Errorf("the grown sprite's bounds did not grow (%v, was %v), so culling would clip it", hi, hiBefore)
	}

	RestoreWaypointRest(sp, rest)
	if sp.DrawnSize() != (mgl32.Vec2{2, 1}) {
		t.Errorf("restored, the sprite is drawn %v, want its authored 2×1", sp.DrawnSize())
	}
	hooks.TakeWaypointEvents()
}

func TestAVolumeInAFrameMeetsTheArrowInTheWorld(t *testing.T) {
	lvl := attachLevel(t)
	v := NewVolume("hold", mgl32.Vec3{5, 0, 0})
	v.Attach.Frame = "ship"
	lvl.Volumes = []Volume{v}
	lvl.WaypointAnimations = []WaypointAnimation{{ID: "path", Points: []Waypoint{wp(mgl32.Vec3{105, 0, 10}, 1)}}}
	PlayWaypoints(lvl, &lvl.Volumes[0], &lvl.WaypointAnimations[0])

	AdvanceWaypointFollowers(lvl, 1)
	if got := lvl.Volumes[0].Position; !vecNear(got, mgl32.Vec3{5, 0, 10}, 1e-4) {
		t.Errorf("the volume's own position is %v, want {5 0 10} in the ship's coordinates", got)
	}
	hooks.TakeWaypointEvents()
}

// A light is a uniform, so the step says when one moved and the frame loop
// uploads again; nothing else moving is reason to.
func TestAPointLightMovesAndSaysSo(t *testing.T) {
	lvl := followLevel(wp(mgl32.Vec3{0, 4, 0}, 2))
	lvl.Sprites = []Sprite{NewSprite("torch", mgl32.Vec3{})}
	PlayWaypoints(lvl, &lvl.Sprites[0], &lvl.WaypointAnimations[0])
	if AdvanceWaypointFollowers(lvl, 0.5) {
		t.Error("only a sprite moved, and the step asked for the lights to be uploaded")
	}

	lvl.Lights.Points = []PointLight{{ID: "lamp"}}
	lamp := &lvl.Lights.Points[0]
	PlayWaypoints(lvl, lamp, &lvl.WaypointAnimations[0])
	if !AdvanceWaypointFollowers(lvl, 1) {
		t.Error("a point light moved without saying so")
	}
	if !vecNear(lamp.Position, mgl32.Vec3{0, 2, 0}, 1e-4) {
		t.Errorf("halfway, the lamp is at %v, want {0 2 0}", lamp.Position)
	}
	hooks.TakeWaypointEvents()
}

// The arrow points along +X, and a directional light shines the way its arrow
// points: DirLightEuler is the turn that makes those one direction.
func TestADirectionalLightShinesTheWayTheArrowPoints(t *testing.T) {
	for _, aim := range [][2]float32{{0, -45}, {90, -30}, {-135, 10}, {270, 80}} {
		m := EulerMatrixYXZ(DirLightEuler(aim[0], aim[1]))
		got := mgl32.Vec3{m[0], m[1], m[2]}
		want := gfx.DirectionFromAngles(mgl32.DegToRad(aim[0]), mgl32.DegToRad(aim[1]))
		if !vecNear(got, want, 1e-5) {
			t.Errorf("yaw %v pitch %v: the arrow points along %v, the light shines along %v", aim[0], aim[1], got, want)
		}
	}

	to := wp(mgl32.Vec3{}, 2)
	to.Rotation = DirLightEuler(90, -45)
	lvl := followLevel(to)
	lvl.Lights.Directional = []DirectionalLight{{ID: "sun", Pitch: -45}}
	sun := &lvl.Lights.Directional[0]
	sun.SyncDirection()
	PlayWaypoints(lvl, sun, &lvl.WaypointAnimations[0])

	if !AdvanceWaypointFollowers(lvl, 1) {
		t.Error("a directional light turned without saying so")
	}
	if !nearF(sun.Yaw, 45, 1e-3) || !nearF(sun.Pitch, -45, 1e-3) {
		t.Errorf("halfway from yaw 0 to 90 the sun is at yaw %v pitch %v, want 45 and -45", sun.Yaw, sun.Pitch)
	}
	if want := gfx.DirectionFromAngles(mgl32.DegToRad(45), mgl32.DegToRad(-45)); !vecNear(sun.Direction, want, 1e-4) {
		t.Errorf("the sun's direction is %v, want it synced to its angles (%v)", sun.Direction, want)
	}
	hooks.TakeWaypointEvents()
}

// A rest is the fields themselves, so it gives back the exact numbers — a yaw
// of 270 read back off a direction would come back as -90.
func TestARestComesBackToTheNumber(t *testing.T) {
	to := wp(mgl32.Vec3{}, 2)
	to.Rotation = DirLightEuler(0, 20)
	lvl := followLevel(to)
	lvl.Lights.Directional = []DirectionalLight{{ID: "sun", Yaw: 270, Pitch: -30}}
	sun := &lvl.Lights.Directional[0]
	sun.SyncDirection()

	rest := CaptureWaypointRest(sun)
	PlayWaypoints(lvl, sun, &lvl.WaypointAnimations[0])
	SeekWaypoints(lvl, sun, &lvl.WaypointAnimations[0], 1.5)
	if sun.Yaw == 270 && sun.Pitch == -30 {
		t.Fatal("seeking did not turn the sun, so the restore proves nothing")
	}
	RestoreWaypointRest(sun, rest)
	if sun.Yaw != 270 || sun.Pitch != -30 {
		t.Errorf("restored to yaw %v pitch %v, want exactly 270 and -30", sun.Yaw, sun.Pitch)
	}
}

// A chunk on a path takes its origin and its Y rotation only — not a point's
// X/Z rotation, which a chunk has nowhere to put, and not its scale, which
// stays a script's read-only field. Its AttachedChunk rider is carried along
// the same distance the chunk moves, the same as a chunk driven by Velocity.
func TestAChunkTravelsAPathAndCarriesItsRider(t *testing.T) {
	to := wp(mgl32.Vec3{4, 0, 0}, 2)
	to.Rotation = mgl32.Vec3{0.3, math.Pi / 2, 0.7}
	lvl := followLevel(to)
	lvl.Chunks = []Chunk{{ID: "lift", Scale: 1}}
	lvl.Entities = []Entity{{ID: "rider", AttachedChunk: "lift", Position: mgl32.Vec3{1, 0, 0}}}
	ch := &lvl.Chunks[0]
	PlayWaypoints(lvl, ch, &lvl.WaypointAnimations[0])

	AdvanceChunks(lvl, 1)
	if !vecNear(ch.Origin, mgl32.Vec3{2, 0, 0}, 1e-4) || !nearF(ch.Rotation, math.Pi/4, 1e-4) {
		t.Errorf("halfway, the chunk is at %v turned %v, want {2 0 0} and pi/4", ch.Origin, ch.Rotation)
	}
	if ch.Scale != 1 {
		t.Errorf("the path wrote the chunk's scale to %v; it should stay untouched", ch.Scale)
	}
	if got := lvl.Entities[0].Position; !vecNear(got, mgl32.Vec3{3, 0, 0}, 1e-4) {
		t.Errorf("the rider is at %v, want {3 0 0} — carried the same distance the chunk moved", got)
	}

	AdvanceChunks(lvl, 1)
	if ch.Waypoints != nil {
		t.Error("the chunk is still following after the length ran out")
	}
	want := []hooks.WaypointEvent{
		{Kind: hooks.WaypointChunk, Object: "lift", Animation: "path", Index: 0},
		{Kind: hooks.WaypointChunk, Object: "lift", Animation: "path", Index: 0, Ended: true},
	}
	if got := hooks.TakeWaypointEvents(); !reflect.DeepEqual(got, want) {
		t.Errorf("announced %+v, want %+v — the arrivals must say they came from a chunk", got, want)
	}
}

// A path and a tween both write the whole placement, so each drops the
// other, for a chunk as for an entity or a brush.
func TestAChunksTweensAndWaypointsCancelEachOther(t *testing.T) {
	lvl := followLevel(wp(mgl32.Vec3{4, 0, 0}, 2))
	lvl.Chunks = []Chunk{{ID: "lift"}}
	ch := &lvl.Chunks[0]

	ch.EaseOrigin(lvl, mgl32.Vec3{0, 9, 0}, 1)
	PlayWaypoints(lvl, ch, &lvl.WaypointAnimations[0])
	if ch.OriginTween != nil {
		t.Error("sending the chunk along a path left its tween running")
	}
	ch.EaseRotation(lvl, 1, 1)
	if ch.Waypoints != nil {
		t.Error("easing the chunk left it following the path")
	}
}

// Position is world space for every kind. A frame-owned chunk's Origin is in
// its carrier's coordinates, so the path has to be divided through the frame
// to meet the arrow — the same property TestAnEntityAboardAFrameMeetsTheArrowInTheWorld
// checks for an entity.
func TestAFrameOwnedChunkMeetsTheArrowInTheWorld(t *testing.T) {
	lvl := attachLevel(t) // the "hull" chunk rides the ship, parked at {100 0 0}
	lvl.WaypointAnimations = []WaypointAnimation{{ID: "path", Points: []Waypoint{wp(mgl32.Vec3{120, 0, 0}, 2)}}}
	hull := FindChunkByID(lvl, "hull")
	PlayWaypoints(lvl, hull, &lvl.WaypointAnimations[0])

	advanceWaypoints(lvl, hull, 1)
	if got := hull.LocalToWorld(mgl32.Vec3{}); !vecNear(got, mgl32.Vec3{110, 0, 0}, 1e-4) {
		t.Errorf("halfway, the hull is at %v in the world, want {110 0 0}", got)
	}
	if !vecNear(hull.Origin, mgl32.Vec3{10, 0, 0}, 1e-4) {
		t.Errorf("the hull's own origin is %v, want {10 0 0} in the ship's coordinates", hull.Origin)
	}
	hooks.TakeWaypointEvents()
}

func TestFindWaypointFollowerFindsEachKindByID(t *testing.T) {
	lvl := attachLevel(t)
	lvl.Sprites = []Sprite{NewSprite("torch", mgl32.Vec3{})}
	lvl.Emitters = []Emitter{NewEmitter("smoke", mgl32.Vec3{})}
	lvl.Sounds = []Sound{NewSound("hum", mgl32.Vec3{})}
	lvl.Volumes = []Volume{NewVolume("hold", mgl32.Vec3{})}
	lvl.Lights.Points = []PointLight{{ID: "lamp"}}
	lvl.Lights.Directional = []DirectionalLight{{ID: "sun"}}

	for _, c := range []struct {
		kind hooks.WaypointObject
		id   string
	}{
		{hooks.WaypointEntity, "npc"}, {hooks.WaypointChunk, "hull"}, {hooks.WaypointSprite, "torch"},
		{hooks.WaypointEmitter, "smoke"}, {hooks.WaypointSound, "hum"}, {hooks.WaypointVolume, "hold"},
		{hooks.WaypointLight, "lamp"}, {hooks.WaypointLight, "sun"},
	} {
		f := FindWaypointFollower(lvl, c.kind, c.id)
		if f == nil {
			t.Errorf("no %s called %q was found", hooks.WaypointObjectNames[c.kind], c.id)
			continue
		}
		if kind, id := WaypointSource(f); kind != c.kind || id != c.id {
			t.Errorf("looking up %s %q found %s %q", hooks.WaypointObjectNames[c.kind], c.id, hooks.WaypointObjectNames[kind], id)
		}
	}
	if f := FindWaypointFollower(lvl, hooks.WaypointSprite, "npc"); f != nil {
		t.Error("an entity's ID found a sprite")
	}
}
