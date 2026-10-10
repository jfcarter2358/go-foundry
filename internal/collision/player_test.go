package collision

// player_test.go — MovePlayer, which used to be inlined in the main loop.
//
// The last test here is the one that matters for what comes next: MovePlayer is
// handed the chunks it resolves against and never asks a level where anything
// is, so the same walk produces the same *relative* result wherever those
// chunks happen to sit. That is what a vehicle interior will rely on.

import (
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// walkMove is the gameplay tuning the shipped build uses, so these tests are
// against the real numbers rather than convenient ones.
func walkMove(intent mgl32.Vec3) PlayerMove {
	return PlayerMove{
		Intent:       intent,
		Gravity:      true,
		DT:           1.0 / 60,
		GravityAccel: world.DefaultGravity,
		MaxFallSpeed: world.DefaultMaxFallSpeed,
		JumpVelocity: world.PlayerJumpVelocity,
	}
}

// settle runs enough frames for a dropped player to come to rest.
func settle(p *world.Entity, lvl *world.Level, n int) (onGround bool) {
	for i := 0; i < n; i++ {
		onGround = MovePlayer(p, lvl, walkMove(mgl32.Vec3{})).OnGround
	}
	return onGround
}

func newTestPlayer(pos mgl32.Vec3) *world.Entity { return world.NewPlayer(pos) }

func TestMovePlayerFallsAndLandsOnTheFloor(t *testing.T) {
	lvl := collisionTestLevel() // floors at y=0, ceilings at y=8
	p := newTestPlayer(mgl32.Vec3{5, 6, 5})

	if onGround := MovePlayer(p, lvl, walkMove(mgl32.Vec3{})).OnGround; onGround {
		t.Error("reported on the ground on the first frame of a 6-unit fall")
	}

	onGround := settle(p, lvl, 400)
	if !onGround {
		t.Fatalf("never landed; ended at y=%v", p.Position.Y())
	}
	// pushOutOfChunkBrushes treats the position as the body centre, so a
	// resting player sits one half-height above the floor — plus the small
	// separation margin the push-apart leaves beyond exact contact (see
	// collision.go's separationEpsilon).
	if got, want := p.Position.Y(), float32(0)+p.BoundHalfY; got < want || got > want+0.01 {
		t.Errorf("rested at y=%v, want %v (+ up to 0.01 separation margin)", got, want)
	}
	if p.Velocity[1] != 0 {
		t.Errorf("vertical velocity survived the landing: %v", p.Velocity[1])
	}
}

func TestMovePlayerAppliesIntentHorizontally(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settle(p, lvl, 5)

	start := p.Position
	MovePlayer(p, lvl, walkMove(mgl32.Vec3{0.5, 0, 0}))

	if got := p.Position.X() - start.X(); got < 0.49 || got > 0.51 {
		t.Errorf("moved %v in X, want ~0.5", got)
	}
	if got := p.Position.Z() - start.Z(); got != 0 {
		t.Errorf("drifted %v in Z with no Z intent", got)
	}
}

func TestMovePlayerJumpLeavesTheGround(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settle(p, lvl, 5)

	mv := walkMove(mgl32.Vec3{})
	mv.Jump = true
	onGround := MovePlayer(p, lvl, mv).OnGround
	if onGround {
		t.Error("still on the ground on the frame the jump was applied")
	}
	if p.Position.Y() <= 0.9 {
		t.Errorf("jump did not lift the player: y=%v", p.Position.Y())
	}

	// And it comes back down.
	if !settle(p, lvl, 400) {
		t.Error("never landed after the jump")
	}
}

func TestMovePlayerFreeFlyIgnoresGravityAndGround(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 4, 5})

	mv := walkMove(mgl32.Vec3{0, 0.25, 0})
	mv.Gravity = false
	onGround := MovePlayer(p, lvl, mv).OnGround

	if onGround {
		t.Error("free-fly reported standing on the ground")
	}
	if got := p.Position.Y(); got != 4.25 {
		t.Errorf("free-fly rose to %v, want 4.25 (the intent, and nothing else)", got)
	}
}

// step is the shipped MovementSpeed. The tests below walk at it deliberately:
// the resolvers are position-based rather than swept, so a step large enough to
// clear a wall *and* leave the chunk in one frame escapes the level entirely —
// the lookup then finds no chunk and nothing resolves. That is a real gap, and
// it is what the swept-collision work exists to close; it is not something this
// extraction changed, so it is not what these tests are guarding.
const step = float32(0.1)

func TestMovePlayerIsStoppedByAWall(t *testing.T) {
	lvl := collisionTestLevel() // two rooms span x in [0,20], walls on the outside
	p := newTestPlayer(mgl32.Vec3{1, 0.9, 5})
	settle(p, lvl, 5)

	// Walk into the x=0 wall for far longer than it takes to reach it.
	for i := 0; i < 200; i++ {
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{-step, 0, 0}))
	}
	if p.Position.X() < p.BoundHalfX {
		t.Errorf("walked through the wall to x=%v", p.Position.X())
	}
}

func TestMovePlayerRecordsWhereItIs(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settle(p, lvl, 5)
	if p.CurrentChunk != lvl.Chunks[0].ID {
		t.Errorf("CurrentChunk = %q, want %q", p.CurrentChunk, lvl.Chunks[0].ID)
	}
}

// TestMovePlayerForgetsAChunkItHasLeft: CurrentChunk is where the body IS, and
// a body that has walked off the end of every chunk is in none of them.
//
// It is not a cosmetic field. "Aboard a frame and inside none of its chunks" is
// the whole of how collision knows a body has walked off a ship
// (steppedOffFrame), so a CurrentChunk that could be set but never cleared left
// a passenger aboard a hold they had walked out of — carried around by a ship
// they were no longer standing in, with the level's own geometry no longer
// solid to them.
func TestMovePlayerForgetsAChunkItHasLeft(t *testing.T) {
	lvl := collisionTestLevel() // two rooms span x in [0,20]
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settle(p, lvl, 5)
	if p.CurrentChunk == "" {
		t.Fatalf("did not start in a chunk at all")
	}

	// Put it well outside every chunk and let one frame of walking derive
	// where that is. Teleported rather than walked, because the walls are
	// there precisely to stop a body doing this on foot.
	p.Position = mgl32.Vec3{500, 0.9, 500}
	MovePlayer(p, lvl, walkMove(mgl32.Vec3{}))
	if p.CurrentChunk != "" {
		t.Errorf("CurrentChunk = %q for a body standing nowhere near it", p.CurrentChunk)
	}
}

// TestMovePlayerIsIndependentOfWhereTheChunkSits is the invariant a moving
// vehicle interior rests on: MovePlayer resolves against the chunks it is
// handed, in whatever space those chunks are expressed in. Translate the whole
// world and the same walk must produce the same walk.
func TestMovePlayerIsIndependentOfWhereTheChunkSits(t *testing.T) {
	const dx, dz = float32(1000), float32(-750)

	here := collisionTestLevel()
	there := collisionTestLevel()
	shiftChunkXZ(&there.Chunks[0], dx, dz)
	// The spatial hash indexes by position, so moving the geometry invalidates
	// it — the same rebuild the engine does whenever a footprint moves.
	if err := world.RebuildIndexes(there); err != nil {
		t.Fatal(err)
	}

	a := newTestPlayer(mgl32.Vec3{5, 6, 5})
	b := newTestPlayer(mgl32.Vec3{5 + dx, 6, 5 + dz})

	steps := []mgl32.Vec3{
		{0, 0, 0}, {step, 0, 0}, {step, 0, step / 2}, {0, 0, step},
		{-step, 0, step / 2}, {0, 0, 0}, {step, 0, -step},
	}
	for i := 0; i < 120; i++ {
		step := steps[i%len(steps)]
		ga := MovePlayer(a, here, walkMove(step)).OnGround
		gb := MovePlayer(b, there, walkMove(step)).OnGround
		if ga != gb {
			t.Fatalf("step %d: ground flags diverged (%v vs %v)", i, ga, gb)
		}
	}

	want := a.Position.Add(mgl32.Vec3{dx, 0, dz})
	if !vecNear(b.Position, want, 1e-3) {
		t.Errorf("the same walk ended at %v in the shifted world, want %v", b.Position, want)
	}
	if a.CurrentChunk == "" || b.CurrentChunk == "" {
		t.Errorf("lost track of the chunk: %q vs %q", a.CurrentChunk, b.CurrentChunk)
	}
}

// TestMovePlayerTreatsChunkOriginTheSameAsShiftedPoints is the same invariant
// as TestMovePlayerIsIndependentOfWhereTheChunkSits, but placing the chunk via
// its live Origin (Chunk.ModelMatrix) rather than by rewriting its points —
// the two must agree exactly, since a moved chunk's collision no longer comes
// from where its points sit but from chunkLocal/chunkWorld carrying the
// player's position into and out of that chunk's own local space around each
// resolve. This is the specific gap that made the spatial hash alone
// insufficient: finding the right chunk is not the same as resolving against
// it in the right space.
func TestMovePlayerTreatsChunkOriginTheSameAsShiftedPoints(t *testing.T) {
	const dx, dz = float32(1000), float32(-750)

	shifted := collisionTestLevel()
	shiftChunkXZ(&shifted.Chunks[0], dx, dz)
	if err := world.RebuildIndexes(shifted); err != nil {
		t.Fatal(err)
	}

	placed := collisionTestLevel()
	placed.Chunks[0].Origin = mgl32.Vec3{dx, 0, dz}
	if err := world.RebuildIndexes(placed); err != nil {
		t.Fatal(err)
	}

	a := newTestPlayer(mgl32.Vec3{5 + dx, 6, 5 + dz})
	b := newTestPlayer(mgl32.Vec3{5 + dx, 6, 5 + dz})

	steps := []mgl32.Vec3{
		{0, 0, 0}, {step, 0, 0}, {step, 0, step / 2}, {0, 0, step},
		{-step, 0, step / 2}, {0, 0, 0}, {step, 0, -step},
	}
	for i := 0; i < 120; i++ {
		s := steps[i%len(steps)]
		ga := MovePlayer(a, shifted, walkMove(s)).OnGround
		gb := MovePlayer(b, placed, walkMove(s)).OnGround
		if ga != gb {
			t.Fatalf("step %d: ground flags diverged (shifted=%v origin=%v)", i, ga, gb)
		}
	}

	if !vecNear(a.Position, b.Position, 1e-3) {
		t.Errorf("moving via shifted points ended at %v, moving via Origin ended at %v", a.Position, b.Position)
	}
	if a.CurrentChunk == "" || b.CurrentChunk == "" {
		t.Errorf("lost track of the chunk: %q (shifted) vs %q (Origin)", a.CurrentChunk, b.CurrentChunk)
	}
}

// TestMovePlayerTreatsChunkOriginAndRotationTheSameAsShiftedRotatedPoints
// exercises the resolvers' rotation path specifically: a translation-only
// Origin never touches chunkLocal/chunkWorld's inverse-rotate step, so this is
// the one that actually proves wall pushes and floor sampling work in a
// rotated chunk's own local space, not just its translated one.
func TestMovePlayerTreatsChunkOriginAndRotationTheSameAsShiftedRotatedPoints(t *testing.T) {
	rotated := collisionTestLevel()
	rotated.Chunks[0].Rotation = float32(math.Pi / 2)
	if err := world.RebuildIndexes(rotated); err != nil {
		t.Fatal(err)
	}

	// The unrotated level's own points, carried through the same rotation the
	// chunk now applies live — so a in the plain level and b in the rotated one
	// start, and stay, at the same physical place relative to the room.
	ch := &rotated.Chunks[0]
	worldStart := ch.LocalToWorld(mgl32.Vec3{5, 6, 5})

	plain := collisionTestLevel()
	a := newTestPlayer(mgl32.Vec3{5, 6, 5})
	b := newTestPlayer(worldStart)

	steps := []mgl32.Vec3{
		{0, 0, 0}, {step, 0, 0}, {step, 0, step / 2}, {0, 0, step},
		{-step, 0, step / 2}, {0, 0, 0}, {step, 0, -step},
	}
	for i := 0; i < 120; i++ {
		s := steps[i%len(steps)]
		// Rotate the intent along with the chunk, or "walk +X" in the plain
		// level would ask the rotated one to walk into a different wall.
		rs := ch.LocalToWorld(s).Sub(ch.LocalToWorld(mgl32.Vec3{}))
		ga := MovePlayer(a, plain, walkMove(s)).OnGround
		gb := MovePlayer(b, rotated, walkMove(rs)).OnGround
		if ga != gb {
			t.Fatalf("step %d: ground flags diverged (plain=%v rotated=%v)", i, ga, gb)
		}
	}

	want := ch.LocalToWorld(a.Position)
	if !vecNear(b.Position, want, 1e-3) {
		t.Errorf("the rotated walk ended at %v, want %v (the plain walk carried through the rotation)", b.Position, want)
	}
	if a.CurrentChunk == "" || b.CurrentChunk == "" {
		t.Errorf("lost track of the chunk: %q (plain) vs %q (rotated)", a.CurrentChunk, b.CurrentChunk)
	}
}

// shiftChunkXZ translates a chunk's whole footprint, which for these GL-free
// test chunks means every brush's verts — everything MovePlayer reads.
func shiftChunkXZ(ch *world.Chunk, dx, dz float32) {
	d3 := mgl32.Vec3{dx, 0, dz}
	for bi := range ch.Brushes {
		verts := ch.Brushes[bi].Verts
		for i := range verts {
			verts[i] = verts[i].Add(d3)
		}
	}
	ch.RecomputeBounds()
}

// ---------- momentum ----------
//
// The player's velocity is its momentum now — a full vector rather than a
// single falling number — and it is what an explosion, a launch pad and the
// `impulse` verb all reach for. These cover the four things that makes true:
// it is integrated, it is added to the keys rather than replaced by them, it
// dies against the ground, and it is spent on the walls it runs into.

// settleGrounded is settle with the ground flag fed back in, the way the frame
// loop does it — momentum damping needs to know whether the body is on its feet.
func settleGrounded(p *world.Entity, lvl *world.Level, n int) (onGround bool) {
	for i := 0; i < n; i++ {
		mv := walkMove(mgl32.Vec3{})
		mv.Grounded = onGround
		onGround = MovePlayer(p, lvl, mv).OnGround
	}
	return onGround
}

// A written velocity carries the player, and it is measured in units a SECOND —
// the same units an entity's has always been, and the same units the property's
// own documentation has always claimed.
func TestAWrittenVelocityCarriesThePlayerInUnitsPerSecond(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 4, 5})
	p.Velocity = mgl32.Vec3{6, 0, 0}

	mv := walkMove(mgl32.Vec3{})
	mv.Gravity = false
	start := p.Position.X()
	MovePlayer(p, lvl, mv)

	// One frame of 1/60s at 6 units a second is 0.1 units.
	if got := p.Position.X() - start; got < 0.09 || got > 0.11 {
		t.Errorf("a velocity of 6 moved the player %v in one 60fps frame, want ~0.1", got)
	}
}

// Momentum and the movement keys are added, not mixed. The keys are a
// displacement that lasts one frame; the momentum survives.
func TestMomentumAddsToTheKeysRatherThanReplacingThem(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 4, 5})
	p.Velocity = mgl32.Vec3{6, 0, 0}

	mv := walkMove(mgl32.Vec3{0.05, 0, 0})
	mv.Gravity = false
	start := p.Position.X()
	MovePlayer(p, lvl, mv)

	// 0.05 from the key plus 0.1 from the momentum.
	if got := p.Position.X() - start; got < 0.14 || got > 0.16 {
		t.Errorf("key and momentum together moved %v, want ~0.15", got)
	}
}

// A shove is a shove, not a permanent slide: standing on something bleeds the
// horizontal momentum away within a fraction of a second. Without this, one
// blast would leave the player skating for the rest of the level.
func TestMomentumDiesAgainstTheGround(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settleGrounded(p, lvl, 5)

	p.Velocity[0] = 10
	settleGrounded(p, lvl, 60) // one second
	if got := p.Velocity.X(); got != 0 {
		t.Errorf("after a second on the ground the shove is still worth %v, want 0", got)
	}
}

// …and in the air it survives, or a thrown body would visibly slow down in
// mid-flight.
func TestMomentumSurvivesInTheAir(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 6, 5})

	// Slow enough that half a second of it stays well inside the room: this is
	// about what the drag does to the number, not about hitting a wall.
	const thrown = float32(2)
	p.Velocity[0] = thrown
	for i := 0; i < 30; i++ { // half a second of fall
		MovePlayer(p, lvl, walkMove(mgl32.Vec3{}))
	}
	if got := p.Velocity.X(); got < thrown*0.88 {
		t.Errorf("half a second of falling cost the throw %v of its %v units a second", thrown-got, thrown)
	}
}

// Momentum pressing into a wall is spent on the wall rather than stored up and
// released the moment the body slides clear of it.
func TestMomentumIntoAWallIsSpentOnIt(t *testing.T) {
	lvl := collisionTestLevel() // a room with walls around it
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settleGrounded(p, lvl, 5)

	// Straight at −X hard enough to reach the wall inside a few frames.
	p.Velocity[0] = -40
	grounded := true
	for i := 0; i < 40; i++ {
		mv := walkMove(mgl32.Vec3{})
		mv.Grounded = grounded
		grounded = MovePlayer(p, lvl, mv).OnGround
	}
	if got := p.Velocity.X(); got < -1 {
		t.Errorf("the body is pinned against the wall still carrying %v into it", got)
	}
}

// Landing reports how fast the body arrived, which is the only moment that
// number exists — the resolve zeroes the fall on the frame it stops it. This is
// what `:landed:` hands a fall-damage rule.
func TestLandingReportsTheImpactSpeedOnce(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 7, 5})

	var landed int
	var impact float32
	grounded := false
	for i := 0; i < 400; i++ {
		mv := walkMove(mgl32.Vec3{})
		mv.Grounded = grounded
		res := MovePlayer(p, lvl, mv)
		grounded = res.OnGround
		if res.Landed {
			landed++
			impact = res.Impact
		}
	}
	if landed != 1 {
		t.Fatalf("a single fall reported landing %d times, want 1", landed)
	}
	// Falling about 5 units under the engine's gravity: v = sqrt(2gd) ≈ 19.
	if impact < 15 || impact > 22 {
		t.Errorf("landed at %v units a second after a ~5 unit drop, want about 19", impact)
	}
}

// Landing stops a fall whether or not gravity is on. Gating it on gravity is
// what used to leave a player caught mid-fall by a gravity switch sinking
// through the floor at whatever speed the fall had reached.
func TestAWeightlessBodyStillStopsOnTheFloor(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 4, 5})
	p.Velocity[1] = -20

	mv := walkMove(mgl32.Vec3{})
	mv.Gravity = false
	for i := 0; i < 200; i++ {
		MovePlayer(p, lvl, mv)
	}
	if p.Velocity.Y() != 0 {
		t.Errorf("a weightless body still sinking at %v after reaching the floor", p.Velocity.Y())
	}
	if got := p.Position.Y(); got < p.BoundHalfY-0.01 {
		t.Errorf("it came to rest at y=%v, below the floor at %v", got, p.BoundHalfY)
	}
}

// Free-fly is the vertical keys and nothing else: no pull, and no momentum
// coasting the camera away after a shove.
func TestFreeFlyDropsMomentumRatherThanCoasting(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 4, 5})
	p.Velocity = mgl32.Vec3{10, 0, 0}

	mv := walkMove(mgl32.Vec3{})
	mv.Fly = true
	for i := 0; i < 60; i++ {
		MovePlayer(p, lvl, mv)
	}
	if got := p.Velocity.X(); got != 0 {
		t.Errorf("a second of free-fly left %v of the shove, want 0", got)
	}
	if p.Velocity.Y() != 0 {
		t.Errorf("free-fly accumulated a fall speed of %v", p.Velocity.Y())
	}
}

// The gravity actually applied is the one handed in, not a constant: this is
// what a level's `gravity:` and a volume's override reach the player through.
func TestThePlayerFallsAtTheGravityItIsGiven(t *testing.T) {
	lvl := collisionTestLevel()
	fast := newTestPlayer(mgl32.Vec3{5, 6, 5})
	slow := newTestPlayer(mgl32.Vec3{5, 6, 5})

	mvSlow := walkMove(mgl32.Vec3{})
	mvSlow.GravityAccel = world.DefaultGravity / 6 // a moon

	for i := 0; i < 20; i++ {
		MovePlayer(fast, lvl, walkMove(mgl32.Vec3{}))
		MovePlayer(slow, lvl, mvSlow)
	}
	if slow.Position.Y() <= fast.Position.Y() {
		t.Errorf("after 20 frames the moon body is at %v and the earth body at %v; the moon one should still be higher",
			slow.Position.Y(), fast.Position.Y())
	}
}

// A body that is moving has a velocity, and the property that reports it has to
// say so. The first cut of this kept the movement keys out of Velocity
// entirely, which made `@player.prop.velocity` read <0,0,0> for someone running
// down a corridor — a property that lies is worse than one that is missing.
func TestWalkingShowsUpInTheVelocity(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settleGrounded(p, lvl, 5)

	// 0.1 units in a 1/60s frame is six units a second.
	mv := walkMove(mgl32.Vec3{0.1, 0, 0})
	mv.Grounded = true
	MovePlayer(p, lvl, mv)

	if got := p.Velocity.X(); got < 5.9 || got > 6.1 {
		t.Errorf("walking at 6 units a second reads back as %v", got)
	}
}

// …and it stops the frame the key comes up, because the keys are a request
// rather than something the body keeps. This is the whole reason the walk is
// tracked apart from the momentum instead of simply being integrated.
func TestReleasingTheWalkKeyStopsTheBodyAtOnce(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settleGrounded(p, lvl, 5)

	mv := walkMove(mgl32.Vec3{0.1, 0, 0})
	mv.Grounded = true
	for i := 0; i < 20; i++ {
		MovePlayer(p, lvl, mv)
	}

	stop := walkMove(mgl32.Vec3{})
	stop.Grounded = true
	at := p.Position.X()
	MovePlayer(p, lvl, stop)

	if got := p.Velocity.X(); got != 0 {
		t.Errorf("the frame after the key came up the body still reads %v", got)
	}
	if got := p.Position.X() - at; got > 1e-4 {
		t.Errorf("it coasted %v further after the key came up", got)
	}
}

// Walking and a shove are added, and BOTH are visible in the velocity: a blast
// that catches a running player does not cancel their run, and their run does
// not cancel the blast.
func TestAShoveAndAWalkBothShowUpInTheVelocity(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 0.9, 5})
	settleGrounded(p, lvl, 5)

	mv := walkMove(mgl32.Vec3{0.1, 0, 0}) // six a second in +X
	mv.Grounded = true
	MovePlayer(p, lvl, mv)

	// A shove in +Z, the way `impulse` applies one: added to what is there.
	p.Velocity[2] += 9
	MovePlayer(p, lvl, mv)

	if got := p.Velocity.X(); got < 5.9 || got > 6.1 {
		t.Errorf("the walk reads %v after the shove, want ~6", got)
	}
	if got := p.Velocity.Z(); got < 6 {
		t.Errorf("the shove reads %v after a frame of walking, want most of 9", got)
	}
}

// Writing the velocity sets the whole of it, and reading it straight back
// agrees — even while the movement keys are held, which is the case the split
// between carried momentum and the keys could most easily get wrong.
func TestWritingTheVelocityWhileWalkingRoundTrips(t *testing.T) {
	lvl := collisionTestLevel()
	p := newTestPlayer(mgl32.Vec3{5, 4, 5})

	mv := walkMove(mgl32.Vec3{0.1, 0, 0})
	mv.Gravity = false
	MovePlayer(p, lvl, mv) // establishes a walk contribution of 6 in X

	p.Velocity = mgl32.Vec3{0, 0, 0} // "stop"
	MovePlayer(p, lvl, mv)

	if got := p.Velocity.X(); got < -0.2 || got > 0.2 {
		t.Errorf("after writing a standstill the body reads %v, want ~0", got)
	}
}
