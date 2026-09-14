package collision

import (
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// settledPlayer drops a standing player into lvl at x,z and lets it land.
func settledPlayer(t *testing.T, lvl *world.Level, x, z float32) *world.Entity {
	t.Helper()
	prev := world.PlayerEntity
	p := world.NewPlayer(mgl32.Vec3{x, 1.5, z})
	world.PlayerEntity = p
	t.Cleanup(func() { world.PlayerEntity = prev; world.ResetPlayerStance() })
	world.ResetPlayerStance()
	for i := 0; i < 120; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	return p
}

// Crouching shortens the body without lifting the feet off the floor or
// sinking them into it, and the eye eases down rather than cutting.
func TestCrouchKeepsTheFeetAndLowersTheEye(t *testing.T) {
	lvl := stepRoom(6)
	p := settledPlayer(t, lvl, 0, 0)
	feet := p.Position.Y() - p.BoundHalfY

	UpdatePlayerCrouch(p, lvl, true, 1.0/60)
	if !world.PlayerCrouched() {
		t.Fatal("holding crouch did not crouch")
	}
	if got := p.Position.Y() - p.BoundHalfY; got < feet-1e-4 || got > feet+1e-4 {
		t.Errorf("feet moved from %v to %v", feet, got)
	}
	firstEye := world.PlayerEyeHeight() + p.Position.Y()
	standingEye := world.EyeHeight + p.Position.Y() + (world.StandHalfHeight - world.CrouchHalfHeight)
	// One frame of easing closes StanceEyeRate/60 of the 1.0 gap — about 0.2 —
	// rather than the whole metre a cut would.
	if d := standingEye - firstEye; d > 0.21 || d < 0 {
		t.Errorf("the eye jumped on the first frame: %v, was %v", firstEye, standingEye)
	}
	for i := 0; i < 60; i++ {
		UpdatePlayerCrouch(p, lvl, true, 1.0/60)
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	if got := world.PlayerEyeHeight(); got != world.CrouchEyeHeight {
		t.Errorf("eye settled at %v above the body, want %v", got, world.CrouchEyeHeight)
	}
	if got := p.Position.Y() - p.BoundHalfY; got < feet-0.01 || got > feet+0.01 {
		t.Errorf("crouched body rests with its feet at %v, want %v", got, feet)
	}

	for i := 0; i < 60; i++ {
		UpdatePlayerCrouch(p, lvl, false, 1.0/60)
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{}, false))
	}
	if world.PlayerCrouched() || world.PlayerEyeHeight() != world.EyeHeight {
		t.Errorf("letting go did not stand up (half %v, eye %v)", p.BoundHalfY, world.PlayerEyeHeight())
	}
}

// Under something lower than a standing body, letting go of crouch keeps the
// player crouched instead of pushing them up through it.
func TestCrouchCannotStandUpUnderALowCeiling(t *testing.T) {
	// A slab whose underside is 1.3 above the floor: room for the crouched
	// body (1.0 tall), none for the standing one (1.8).
	slab := world.NewBoxBrush("slab", mgl32.Vec3{3, 1.55, 0}, mgl32.Vec3{1.5, 0.25, 2}, "concrete")
	lvl := stepRoom(6, slab)
	p := settledPlayer(t, lvl, 0, 0)

	UpdatePlayerCrouch(p, lvl, true, 1.0/60)
	for i := 0; i < 90; i++ {
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{0.05, 0, 0}, false))
	}
	if p.Position.X() < 2.5 {
		t.Fatalf("crouched body did not get under the slab (x %v)", p.Position.X())
	}
	UpdatePlayerCrouch(p, lvl, false, 1.0/60)
	if !world.PlayerCrouched() {
		t.Error("stood up into the slab")
	}

	for i := 0; i < 120; i++ {
		UpdatePlayerCrouch(p, lvl, false, 1.0/60)
		MovePlayer(p, lvl, stepMove(mgl32.Vec3{-0.05, 0, 0}, false))
	}
	if world.PlayerCrouched() {
		t.Errorf("still crouched after walking out from under the slab (x %v)", p.Position.X())
	}
}

// Sprinting and crouching are two speeds for the same legs: a crouched player
// cannot start a sprint, and resetting the stance ends one.
func TestSprintIsRefusedWhileCrouched(t *testing.T) {
	lvl := stepRoom(6)
	p := settledPlayer(t, lvl, 0, 0)
	world.SetPlayerSprinting(true)
	if !world.PlayerSprinting() {
		t.Fatal("standing player could not sprint")
	}
	world.ResetPlayerStance()
	if world.PlayerSprinting() {
		t.Error("a stance reset kept the sprint")
	}
	UpdatePlayerCrouch(p, lvl, true, 1.0/60)
	world.SetPlayerSprinting(true)
	if world.PlayerSprinting() {
		t.Error("crouched player started a sprint")
	}
}
