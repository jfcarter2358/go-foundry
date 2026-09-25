package world

// gravity_test.go — the three things that get a say in how hard a body falls,
// and the rule for resolving them (gravity.go).

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func gravityVolume(id string, at mgl32.Vec3, half float32, g float32) Volume {
	return Volume{
		ID: id, Position: at, Enabled: true,
		HalfX: half, HalfY: half, HalfZ: half,
		Gravity: &g,
	}
}

// A level that never mentions gravity falls at the engine's rate. This is the
// case every level shipped so far is in, and a Level struct literal — which
// tests and the editor build constantly — must be in it too.
func TestALevelThatSaysNothingFallsAtTheDefault(t *testing.T) {
	if got := LevelGravity(nil); got != DefaultGravity {
		t.Errorf("a nil level falls at %v, want %v", got, DefaultGravity)
	}
	if got := LevelGravity(&Level{}); got != DefaultGravity {
		t.Errorf("a bare level falls at %v, want %v", got, DefaultGravity)
	}
}

// Zero is a value, not an absence: a level asking for zero-g must not be read
// as a level asking for nothing. This is the whole reason the field is a
// pointer.
func TestZeroGravityIsNotTheSameAsUnset(t *testing.T) {
	zero := float32(0)
	if got := LevelGravity(&Level{Gravity: &zero}); got != 0 {
		t.Errorf("a level set to zero-g falls at %v, want 0", got)
	}
}

// A volume replaces the level's pull inside its region and nowhere else.
func TestAVolumeOverridesGravityOnlyInside(t *testing.T) {
	moon := float32(6)
	lvl := &Level{
		Gravity: &[]float32{40}[0],
		Volumes: []Volume{gravityVolume("chamber", mgl32.Vec3{10, 0, 0}, 2, moon)},
	}
	if got := GravityAt(lvl, mgl32.Vec3{10, 0, 0}); got != moon {
		t.Errorf("inside the chamber gravity is %v, want %v", got, moon)
	}
	if got := GravityAt(lvl, mgl32.Vec3{100, 0, 0}); got != 40 {
		t.Errorf("outside it gravity is %v, want the level's 40", got)
	}
}

// Disabling the volume hands the region straight back to the level, which is
// what lets a script switch a room's gravity by arming the trigger.
func TestADisabledGravityVolumeStopsOverriding(t *testing.T) {
	lvl := &Level{Volumes: []Volume{gravityVolume("chamber", mgl32.Vec3{}, 5, 0)}}
	if got := GravityAt(lvl, mgl32.Vec3{}); got != 0 {
		t.Fatalf("test setup: the enabled chamber gives %v, want 0", got)
	}
	lvl.Volumes[0].Enabled = false
	if got := GravityAt(lvl, mgl32.Vec3{}); got != DefaultGravity {
		t.Errorf("a disabled chamber still gives %v, want the level's %v", got, DefaultGravity)
	}
}

// Overlapping regions are normal — a low-gravity chamber authored inside the
// room volume that dims the lights — and the more specific one has to win, or
// the answer would depend on the order the level happens to list them in.
func TestTheSmallestContainingGravityVolumeWins(t *testing.T) {
	inner, outer := float32(2), float32(20)
	for _, order := range [][]Volume{
		{gravityVolume("room", mgl32.Vec3{}, 50, outer), gravityVolume("pocket", mgl32.Vec3{}, 3, inner)},
		{gravityVolume("pocket", mgl32.Vec3{}, 3, inner), gravityVolume("room", mgl32.Vec3{}, 50, outer)},
	} {
		lvl := &Level{Volumes: order}
		if got := GravityAt(lvl, mgl32.Vec3{}); got != inner {
			t.Errorf("listed as %q first, the pocket gives %v, want %v", order[0].ID, got, inner)
		}
	}
}

// The body's own knob multiplies the place's rather than replacing it, which is
// what lets a feather be half as heavy wherever it happens to be.
func TestGravityForScalesWhateverThePlaceSays(t *testing.T) {
	moon := float32(6)
	lvl := &Level{Volumes: []Volume{gravityVolume("chamber", mgl32.Vec3{}, 5, moon)}}
	e := &Entity{ID: "feather"}
	e.Collision.GravityScale = 0.5

	if got := GravityFor(lvl, e); got != moon*0.5 {
		t.Errorf("inside the chamber the feather falls at %v, want %v", got, moon*0.5)
	}
	e.Position = mgl32.Vec3{100, 0, 0}
	if got := GravityFor(lvl, e); got != DefaultGravity*0.5 {
		t.Errorf("outside it the feather falls at %v, want %v", got, DefaultGravity*0.5)
	}
}

// Zero is "nobody set this", because a struct literal written before the field
// existed has to keep falling. Switching gravity off is the flag, not the scale.
func TestAnUnsetGravityScaleIsOne(t *testing.T) {
	var c EntityCollision
	if got := c.EffectiveGravityScale(); got != 1 {
		t.Errorf("an unset scale reads %v, want 1", got)
	}
	c.GravityScale = -1
	if got := c.EffectiveGravityScale(); got != -1 {
		t.Errorf("a negative scale reads %v, want -1 (it falls upward)", got)
	}
}

// A terminal speed of zero would be a body that never falls and eventually
// sinks through the floor, so it reads as the default instead.
func TestAnUnsetMaxFallSpeedIsTheDefault(t *testing.T) {
	var c EntityCollision
	if got := c.EffectiveMaxFallSpeed(); got != DefaultMaxFallSpeed {
		t.Errorf("an unset cap reads %v, want %v", got, DefaultMaxFallSpeed)
	}
	c.MaxFallSpeed = -5
	if got := c.EffectiveMaxFallSpeed(); got != DefaultMaxFallSpeed {
		t.Errorf("a negative cap reads %v, want %v", got, DefaultMaxFallSpeed)
	}
}

// The body's own cap is what holds the fall, not the engine's.
func TestApplyGravityHoldsTheBodysOwnTerminalSpeed(t *testing.T) {
	e := &Entity{}
	e.Collision = EntityCollision{Gravity: true, Movable: true, MaxFallSpeed: 4}
	for i := 0; i < 200; i++ {
		e.ApplyGravity(0.1, DefaultGravity)
	}
	if e.Velocity.Y() < -4-1e-4 {
		t.Errorf("fall speed %v passed the body's own cap of 4", e.Velocity.Y())
	}
	if e.Velocity.Y() > -4+1e-4 {
		t.Errorf("fall speed %v never reached the cap of 4", e.Velocity.Y())
	}
}

// An upward gravity is capped upward too, or a ceiling-walk region would
// accelerate a body forever. A jump is not capped — that speed is not gravity's.
func TestUpwardGravityIsCappedButAJumpIsNot(t *testing.T) {
	e := &Entity{}
	e.Collision = EntityCollision{Gravity: true, Movable: true, MaxFallSpeed: 5}
	for i := 0; i < 200; i++ {
		e.ApplyGravity(0.1, -DefaultGravity)
	}
	if e.Velocity.Y() > 5+1e-4 {
		t.Errorf("climb speed %v passed the cap of 5", e.Velocity.Y())
	}

	launched := &Entity{}
	launched.Collision = EntityCollision{Gravity: true, Movable: true, MaxFallSpeed: 5}
	launched.Velocity[1] = 40
	launched.ApplyGravity(1.0/60, DefaultGravity)
	if launched.Velocity.Y() < 39 {
		t.Errorf("a launch of 40 was clipped to %v by the FALL cap", launched.Velocity.Y())
	}
}

// The `player:` block is the player's definition, so its two knobs have to
// reach the body the same way its script and attributes do.
func TestApplyPlayerScriptCarriesTheGravityKnobs(t *testing.T) {
	prev := PlayerEntity
	t.Cleanup(func() { PlayerEntity = prev })
	PlayerEntity = &Entity{ID: "player"}

	ApplyPlayerScript(&Level{Player: PlayerSettings{GravityScale: 0.25, MaxFallSpeed: 9}})
	if got := PlayerEntity.Collision.EffectiveGravityScale(); got != 0.25 {
		t.Errorf("the player's scale is %v, want 0.25", got)
	}
	if got := PlayerEntity.Collision.EffectiveMaxFallSpeed(); got != 9 {
		t.Errorf("the player's cap is %v, want 9", got)
	}

	// A second session must not inherit what a script left behind.
	PlayerEntity.Collision.GravityScale = 3
	ApplyPlayerScript(&Level{})
	if got := PlayerEntity.Collision.EffectiveGravityScale(); got != 1 {
		t.Errorf("a level that says nothing left the scale at %v, want 1", got)
	}
}

// ---------- the `collision:` block ----------

// An instance writes back only what differs from its type, so a barrel that
// takes its type's gravity does not acquire a `collision:` key by being saved —
// and one that overrides the scale keeps that override across a round trip.
func TestTheCollisionBlockRoundTripsTheGravityKnobs(t *testing.T) {
	base := EntityCollision{World: true, Gravity: true, Movable: true, GravityScale: 1, MaxFallSpeed: 30}

	if raw := entityCollisionToRaw(base, base); raw != nil {
		t.Errorf("an instance identical to its type wrote a collision block: %+v", raw)
	}

	feather := base
	feather.GravityScale = 0.2
	feather.MaxFallSpeed = 3
	raw := entityCollisionToRaw(feather, base)
	if raw == nil {
		t.Fatal("an instance that overrides both knobs wrote no collision block")
	}
	if raw.GravityScale == nil || *raw.GravityScale != 0.2 {
		t.Errorf("gravity_scale written as %v, want 0.2", raw.GravityScale)
	}
	if raw.MaxFallSpeed == nil || *raw.MaxFallSpeed != 3 {
		t.Errorf("max_fall_speed written as %v, want 3", raw.MaxFallSpeed)
	}
	if raw.World != nil || raw.Gravity != nil || raw.Movable != nil {
		t.Errorf("the flags it did not touch were written anyway: %+v", raw)
	}

	if back := entityCollisionFromRaw(base, raw); back != feather {
		t.Errorf("read back as %+v, want %+v", back, feather)
	}
}

// A `collision:` block that says nothing about the two knobs leaves whatever it
// is folded onto alone — the merge rule every other flag in the block follows.
func TestACollisionBlockWithoutTheKnobsLeavesThemAlone(t *testing.T) {
	base := EntityCollision{Movable: true, GravityScale: 0.5, MaxFallSpeed: 7}
	on := true
	got := entityCollisionFromRaw(base, &RawEntityCollision{Gravity: &on})
	if got.GravityScale != 0.5 || got.MaxFallSpeed != 7 {
		t.Errorf("an unrelated override reset the knobs to %v and %v", got.GravityScale, got.MaxFallSpeed)
	}
	if !got.Gravity {
		t.Error("the override the block DID carry was not applied")
	}
}
