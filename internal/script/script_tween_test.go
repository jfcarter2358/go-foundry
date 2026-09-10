package script

// script_tween_test.go — move_to, rotate_to and scale_to.
//
// A tween is a placement write spread over time, so what is tested is the
// bargain world/tween.go strikes: the call moves nothing but starts the blend,
// the engine's own tick carries it, it lands exactly, the channels keep their
// own clocks, and whatever else would move a channel waits while it runs.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/collision"
	"foundry/internal/world"
)

func TestMoveToEasesADynamicBrushOverTime(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	b := &lvl.Chunks[0].Brushes[0]
	start := b.Position()
	to := start.Add(mgl32.Vec3{0, 4, 0})

	runScript(t, lvl, "", script("    p = @room.prop.position\n    move_to(@room, <p.x, p.y + 4.0, p.z>, 2.0)"))
	if !vecNear(b.Position(), start, 1e-5) {
		t.Fatalf("move_to moved the brush at the call, to %v; it should only start the blend", b.Position())
	}

	world.AdvanceBrushes(lvl, 1)
	if want := start.Add(mgl32.Vec3{0, 2, 0}); !vecNear(b.Position(), want, 1e-4) {
		t.Errorf("halfway through, the brush is at %v, want %v", b.Position(), want)
	}

	// Overshooting the end lands on it exactly and lets go.
	world.AdvanceBrushes(lvl, 1.5)
	if !vecNear(b.Position(), to, 1e-4) {
		t.Errorf("after the blend the brush is at %v, want %v", b.Position(), to)
	}
	if b.Tweens.Active() {
		t.Error("the brush is still easing after it arrived")
	}
}

// Zero seconds is the property write it amounts to, applied at the call — a
// script reading the position straight afterwards sees the new one.
func TestMoveToWithNoTimeMovesAtOnce(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	b := &lvl.Chunks[0].Brushes[0]

	runScript(t, lvl, "", script("    move_to(@room, <20.0, 3.0, -5.0>, 0)"))
	if !vecNear(b.Position(), mgl32.Vec3{20, 3, -5}, 1e-4) {
		t.Errorf("an instant move_to left the brush at %v, want <20,3,-5>", b.Position())
	}
	if b.Tweens.Active() {
		t.Error("an instant move_to left a tween behind")
	}
}

// Each channel keeps its own clock, and a rotation is blended as the numbers it
// is: 0 to 6 radians goes the long way round, and ends at 6 rather than wrapped.
func TestRotateToAndScaleToEaseOnTheirOwnClocks(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	b := &lvl.Chunks[0].Brushes[0]

	runScript(t, lvl, "", script("    rotate_to(@room, <0.0, 6.0, 0.0>, 2.0)\n    scale_to(@room, <3.0, 1.0, 1.0>, 1.0)"))

	world.AdvanceBrushes(lvl, 1)
	if y := b.Rotation.Y(); y < 2.999 || y > 3.001 {
		t.Errorf("halfway through the turn the rotation is %v, want 3", y)
	}
	if !vecNear(b.EffectiveScale(), mgl32.Vec3{3, 1, 1}, 1e-4) {
		t.Errorf("after its one second the scale is %v, want <3,1,1>", b.EffectiveScale())
	}
	if b.Tweens.Easing(world.TweenScale) || !b.Tweens.Easing(world.TweenRotate) {
		t.Errorf("scale should have arrived and rotation not: %+v", b.Tweens)
	}
	if !vecNear(b.Position(), b.Pivot(), 1e-4) {
		t.Errorf("turning and growing moved the brush's centre to %v", b.Position())
	}

	world.AdvanceBrushes(lvl, 1)
	if y := b.Rotation.Y(); y != 6 {
		t.Errorf("after the turn the rotation is %v, want exactly 6", y)
	}
}

// A rate waits while its channel is eased — including on the tick the blend
// lands, which must land exactly — and carries on from there afterwards.
func TestABrushVelocityWaitsWhileItIsEased(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	b := &lvl.Chunks[0].Brushes[0]
	start := b.Position()

	runScript(t, lvl, "", script("    @room.prop.velocity = <1.0, 0.0, 0.0>\n"+
		"    p = @room.prop.position\n    move_to(@room, <p.x, p.y + 4.0, p.z>, 1.0)"))

	world.AdvanceBrushes(lvl, 0.5)
	world.AdvanceBrushes(lvl, 0.5)
	if want := start.Add(mgl32.Vec3{0, 4, 0}); !vecNear(b.Position(), want, 1e-4) {
		t.Fatalf("the eased brush arrived at %v, want %v — its velocity ran during the blend", b.Position(), want)
	}

	world.AdvanceBrushes(lvl, 1)
	if want := start.Add(mgl32.Vec3{1, 4, 0}); !vecNear(b.Position(), want, 1e-4) {
		t.Errorf("a second after arriving the brush is at %v, want %v — its velocity did not resume", b.Position(), want)
	}
}

// A waypoint animation and a tween both write the placement, so each start
// cancels the other.
func TestTweensAndWaypointsCancelEachOther(t *testing.T) {
	lvl := dynamicBrushLevel(t)
	lvl.WaypointAnimations = append(lvl.WaypointAnimations, world.WaypointAnimation{
		ID: "lift_path", Points: []world.Waypoint{world.NewWaypoint(mgl32.Vec3{0, 10, 0})},
	})
	b := &lvl.Chunks[0].Brushes[0]

	runScript(t, lvl, "", script("    play_waypoints(@room, \"lift_path\")\n    move_to(@room, <0.0, 5.0, 0.0>, 1.0)"))
	if b.Waypoints != nil || !b.Tweens.Easing(world.TweenMove) {
		t.Errorf("move_to on a brush following waypoints: waypoints %v, tweens %+v; want the tween alone", b.Waypoints, b.Tweens)
	}

	runScript(t, lvl, "", script("    play_waypoints(@room, \"lift_path\")"))
	if b.Waypoints == nil || b.Tweens.Active() {
		t.Errorf("play_waypoints on an easing brush: waypoints %v, tweens %+v; want the animation alone", b.Waypoints, b.Tweens)
	}
}

// A chunk eases its origin, and carries an attached entity the same distance
// as any other move of it does.
func TestMoveToEasesAChunkAndItsRiders(t *testing.T) {
	lvl := boundedTestLevel(t)
	crate := &lvl.Entities[0]
	crate.AttachedChunk = "chunk"
	crate.Position = mgl32.Vec3{1, 0, 1}

	runScript(t, lvl, "", script("    move_to(@chunk, <10.0, 0.0, 0.0>, 2.0)"))
	world.AdvanceChunks(lvl, 1)
	if o := lvl.Chunks[0].Origin; !vecNear(o, mgl32.Vec3{5, 0, 0}, 1e-4) {
		t.Errorf("halfway through, the chunk's origin is %v, want <5,0,0>", o)
	}
	world.AdvanceChunks(lvl, 1)
	if o := lvl.Chunks[0].Origin; o != (mgl32.Vec3{10, 0, 0}) {
		t.Errorf("after the blend the chunk's origin is %v, want exactly <10,0,0>", o)
	}
	if lvl.Chunks[0].OriginTween != nil {
		t.Error("the chunk is still easing after it arrived")
	}
	if !vecNear(crate.Position, mgl32.Vec3{11, 0, 1}, 1e-4) {
		t.Errorf("the attached crate is at %v, want <11,0,1> — carried the whole ten metres", crate.Position)
	}
}

// An entity eases on EntityUpdate, and does not fall while it does: gravity
// that piled up during the blend would all be released the moment it ended.
func TestMoveToEasesAnEntityWithoutGravityPilingUp(t *testing.T) {
	lvl := boundedTestLevel(t)
	crate := &lvl.Entities[0]
	crate.Collision = world.EntityCollision{Gravity: true, Movable: true}

	runScript(t, lvl, "", script("    move_to(@crate, <0.0, 10.0, 0.0>, 1.0)\n    rotate_to(@crate, <0.0, 1.0, 0.0>, 1.0)"))

	collision.EntityUpdate(lvl, 0.5, true, nil)
	if !vecNear(crate.Position, mgl32.Vec3{0, 5, 0}, 1e-4) || !vecNear(crate.Rotation, mgl32.Vec3{0, 0.5, 0}, 1e-4) {
		t.Errorf("halfway through, the crate is at %v turned %v; want <0,5,0> turned <0,0.5,0>", crate.Position, crate.Rotation)
	}
	collision.EntityUpdate(lvl, 0.5, true, nil)
	if !vecNear(crate.Position, mgl32.Vec3{0, 10, 0}, 1e-4) {
		t.Errorf("after the blend the crate is at %v, want <0,10,0>", crate.Position)
	}
	if crate.Velocity.Y() != 0 {
		t.Errorf("the crate built up a fall speed of %v while it was being eased", crate.Velocity.Y())
	}

	// Let go, gravity takes over from rest.
	collision.EntityUpdate(lvl, 0.5, true, nil)
	if v := crate.Velocity.Y(); v >= 0 || v < -world.EntityGravityAccel*0.5-1e-3 {
		t.Errorf("one step after the blend the fall speed is %v, want one step of gravity", v)
	}
}

// Each refusal names what is wrong with the object rather than moving it.
func TestTweenVerbsRefuseWhatTheyCannotEase(t *testing.T) {
	cases := []struct {
		name, src, want string
		prep            func(*world.Level)
	}{
		{"static brush", "    move_to(@room, <1.0, 0.0, 0.0>, 1.0)", "not dynamic", nil},
		{"static brush scale", "    scale_to(@room, <2.0, 2.0, 2.0>, 1.0)", "not dynamic", nil},
		{"chunk rotation", "    rotate_to(@chunk, <0.0, 1.0, 0.0>, 1.0)", "read-only", nil},
		{"not a placeable", "    move_to(@lamp, <0.0, 1.0, 0.0>, 1.0)", "a brush, a chunk or an entity", nil},
		{"collapsed scale", "    scale_to(@room, <0.0, 1.0, 1.0>, 1.0)", "greater than zero",
			func(l *world.Level) { l.Chunks[0].Brushes[0].Dynamic = true }},
		{"vehicle", "    move_to(@crate, <0.0, 1.0, 0.0>, 1.0)", "vehicle",
			func(l *world.Level) { l.Entities[0].Vehicle = &world.VehicleSpec{} }},
		{"no seconds", "    move_to(@crate, <0.0, 1.0, 0.0>)", "number of seconds", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lvl := boundedTestLevel(t)
			if tc.prep != nil {
				tc.prep(lvl)
			}
			err := runScriptErr(t, lvl, "", script(tc.src))
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("failed with %q, want it to mention %q", err, tc.want)
			}
			if b := &lvl.Chunks[0].Brushes[0]; !b.AtRest() || b.Tweens.Active() {
				t.Error("the refused call moved or started easing the brush anyway")
			}
		})
	}
}

func TestLintFlagsTweenCallsThatWouldBeRefused(t *testing.T) {
	lvl := boundedTestLevel(t) // "room" is not dynamic
	wantOneIssue(t, lintSource(t, lvl, script("    move_to(@room, <0.0, 1.0, 0.0>, 1.0)")), "not dynamic")
	wantOneIssue(t, lintSource(t, lvl, script("    rotate_to(@chunk, <0.0, 1.0, 0.0>, 1.0)")), "read-only")
	wantOneIssue(t, lintSource(t, lvl, script("    scale_to(@lamp, <2.0, 2.0, 2.0>, 1.0)")), "a brush or an entity")

	lvl.Chunks[0].Brushes[0].Dynamic = true
	if got := lintSource(t, lvl, script("    move_to(@room, <0.0, 1.0, 0.0>, 1.0)\n    move_to(@chunk, <1.0, 0.0, 0.0>, 2)\n    scale_to(@crate, <2.0, 2.0, 2.0>, 0.5)")); len(got) != 0 {
		t.Errorf("calls that would succeed were flagged: %v", got)
	}
}
