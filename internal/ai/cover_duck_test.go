package ai

import (
	"testing"

	"foundry/internal/collision"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// lowWallLevel is the room with a waist-high wall across it: 1.1 m, under a
// standing agent's chest (1.8 m tall) and over a crouched one's head.
func lowWallLevel(t *testing.T) *world.Level {
	wall := world.NewBoxBrush("low", mgl32.Vec3{10, 0.55, 10}, mgl32.Vec3{6, 0.55, 0.3}, "")
	return aiLevel(t, wall)
}

func TestDuckedCoverFindsAFiringPositionBehindLowCover(t *testing.T) {
	lvl := lowWallLevel(t)
	lvl.Entities = []world.Entity{agent("soldier", mgl32.Vec3{10, 0, 6})}
	e := &lvl.Entities[0]
	p := player(t, mgl32.Vec3{10, 0, 17})
	threat := eyeOf(lvl, p)
	near := mgl32.Vec3{10, 0, 8}

	if _, ok := FindCoverWith(lvl, e, near, 4, nil, threat, p.ID, CoverOpts{}); ok {
		t.Fatal("a waist-high wall hid a standing agent: the ducked search would pass without ducking")
	}
	spot, ok := FindCoverWith(lvl, e, near, 4, nil, threat, p.ID, CoverOpts{Duck: 0.6, Peek: true})
	if !ok {
		t.Fatal("no ducked firing position found behind the low wall")
	}
	if spot.Z() > 9.7 {
		t.Errorf("the firing position %v is on the player's side of the wall", spot)
	}
	if !hiddenAt(lvl, e, spot, threat, p.ID, 0.6) {
		t.Errorf("the spot %v does not hide a crouched body", spot)
	}
	eye := spot.Add(mgl32.Vec3{0, ProfileFor(lvl, e).Height * 0.9, 0})
	if !LineOfSight(lvl, eye, threat, e.ID, p.ID) {
		t.Errorf("the spot %v has no shot from standing: it is a hole, not a firing position", spot)
	}
}

func TestACrouchedBodyPresentsLessToRoundsAndSight(t *testing.T) {
	lvl := aiLevel(t)
	lvl.Entities = []world.Entity{agent("soldier", mgl32.Vec3{10, 0, 10})}
	e := &lvl.Entities[0]
	f := collision.DefaultRayFilter(40)
	head := func() bool {
		res := collision.Raycast(lvl, mgl32.Vec3{10, 1.6, 2}, mgl32.Vec3{0, 0, 1}, f)
		h, ok := res.First()
		return ok && h.Kind == collision.RayHitEntity && h.ID == e.ID
	}
	shin := func() bool {
		res := collision.Raycast(lvl, mgl32.Vec3{10, 0.5, 2}, mgl32.Vec3{0, 0, 1}, f)
		h, ok := res.First()
		return ok && h.Kind == collision.RayHitEntity && h.ID == e.ID
	}
	if !head() || !shin() {
		t.Fatalf("a standing body was missed: head %v shin %v", head(), shin())
	}
	standingEye := eyeOf(lvl, e)

	e.HitHeight = 0.6
	if head() {
		t.Error("a round at head height met a crouched body")
	}
	if !shin() {
		t.Error("a round at shin height missed a crouched body")
	}
	if eye := eyeOf(lvl, e); eye.Y() >= standingEye.Y()-0.5 {
		t.Errorf("a crouched body looks from %v, barely under its standing eye %v", eye, standingEye)
	}
	if ProfileFor(lvl, e).Height != 1.8 {
		t.Errorf("crouching changed the navigation profile to %v; it must plan on the whole bounds", ProfileFor(lvl, e))
	}
}

func TestARunCommandRunsWithTheRunAnimation(t *testing.T) {
	distance := func(run bool) (float32, string) {
		lvl := aiLevel(t)
		g := agent("guard", mgl32.Vec3{2, 0, 10})
		g.AI.Speed, g.AI.RunSpeed, g.AI.RunAnim, g.AI.MoveAnim = 1.5, 4.5, "run", "walk"
		lvl.Entities = []world.Entity{g}
		MoveTo(lvl, &lvl.Entities[0], mgl32.Vec3{18, 0, 10}, run)
		runFor(lvl, 1.5)
		e := &lvl.Entities[0]
		return Feet(lvl, e).X() - 2, e.AIState.Anim
	}
	walked, walkAnim := distance(false)
	ran, runAnim := distance(true)
	if ran < walked*1.8 {
		t.Errorf("ran %v m against a walk of %v m in the same time", ran, walked)
	}
	if walkAnim != "walk" || runAnim != "run" {
		t.Errorf("animations walk %q, run %q; want walk and run", walkAnim, runAnim)
	}
}

func runFor(lvl *world.Level, seconds float32) {
	const dt = float32(1.0 / 60.0)
	for t := float32(0); t < seconds; t += dt {
		Update(lvl, dt, nil)
		collision.EntityUpdate(lvl, dt, true, nil)
	}
}
