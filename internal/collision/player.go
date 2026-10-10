package collision

// player.go — moving the player, once, wherever it happens to be standing.
//
// This used to be inlined in the main loop, which was fine while there was
// exactly one place a player could stand: the level, in world coordinates.
// Pulling it out here is what lets a second caller drive the same walking
// against a different set of chunks — a vehicle's interior, resolved in that
// vehicle's local space — without either of them re-deriving how walking works.
//
// Two things are deliberately parameters rather than globals. The gameplay
// switches (gravity, jump) live in the window layer, and reading them from here
// would drag SDL into a package that has no business linking it. And the *space*
// is a parameter: MovePlayer never asks the level where the player is, it is
// told which chunks to resolve against. Those are the same decision — this
// routine knows how a body walks, and nothing about where.

import (
	"math"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// PlayerMove is one frame of gameplay input to MovePlayer.
//
// The tuning numbers are passed in rather than defined here because they are
// resolved against the level the player is standing in — gravity is the level's
// to set, a volume's to override and the body's to scale (world/gravity.go) —
// and this package resolves nothing.
type PlayerMove struct {
	// Intent is the displacement the movement keys asked for this frame,
	// already scaled for frame time, in the same space as the player's
	// position. It is *not* momentum: releasing the key ends it on the same
	// frame, which is what makes walking feel like walking. Free-fly puts its
	// up/down in here too, for the same reason.
	//
	// What the player carries between frames is Entity.Velocity, which this is
	// added to rather than folded into — see MovePlayer.
	Intent mgl32.Vec3

	Jump    bool // a jump was requested this frame
	Gravity bool // gravity is on
	// Fly is free-fly: the vertical keys move the body and nothing pulls on
	// it. Separate from Gravity because the two questions are genuinely
	// different — a level may want the player weightless but still moved only
	// by what pushes them, which is zero-g rather than a noclip camera.
	Fly bool
	// Grounded is whether the player was standing on something at the end of
	// last frame. Only momentum damping reads it: a body on its feet sheds a
	// shove against the floor, a body in the air keeps it (see dampMomentum).
	Grounded bool

	// DT is the frame's length in seconds. Everything below is expressed per
	// second, as an entity's velocity has always been; the player's used to be
	// per-frame-at-60fps, and the two disagreeing was why `@player.prop.velocity`
	// read back a number sixty times smaller than its own documentation.
	DT float32

	// GravityAccel is the pull already resolved for this body, in units/s².
	// MaxFallSpeed caps the fall, and JumpVelocity is the launch speed.
	GravityAccel float32
	MaxFallSpeed float32
	JumpVelocity float32
}

// PlayerMoveResult is what one frame of walking produced.
type PlayerMoveResult struct {
	// OnGround is whether the body ended the frame standing on something.
	OnGround bool
	// Landed is whether that is new this frame — the body was in the air when
	// the frame began and is not now. Impact is the downward speed it arrived
	// at, in units per second, read the instant before the resolve zeroes it.
	// That is what a fall-damage rule wants and the only place it exists.
	Landed bool
	Impact float32
}

// Momentum damping, in reciprocal seconds. A body standing on something sheds
// what it is carrying quickly — a shove is a shove, not a permanent slide, and
// friction against a floor is the one thing everybody expects to exist. A body
// in the air barely sheds anything, because an arc that visibly slowed down in
// mid-flight reads as broken.
//
// Applied to the horizontal only. Vertical momentum is gravity's and the
// terminal speed's business, and damping it as well would make the fall rate
// depend on how long the fall had been going.
const (
	playerGroundDrag = float32(12)
	playerAirDrag    = float32(0.2)
)

// MovePlayer advances the player by one frame, in whatever frame it is standing
// in, and reports what that frame did.
//
// It writes p.Position, all of p.Velocity and the derived CurrentChunk, all in
// the player's own frame.
// The caller owns the camera and the gravity switches: this returns the ground
// flag rather than setting it, because whether "on the ground" is meaningful at
// all depends on gravity being on, and that is the caller's question.
//
// Velocity is the player's MOMENTUM, and it is a full vector rather than the
// single falling number it used to be. That is the whole of what makes an
// explosion throw the player, a launch pad launch them, and `impulse(player,
// ...)` mean the same thing it means for every other body: a write to
// @player.prop.velocity sets the momentum, and the physics here carries on from
// it — integrating it, bleeding it off against the ground, spending it on the
// walls it runs into and adding gravity to it. It is NOT where walking lives:
// the keys are Intent, a displacement that exists only for the frame it was
// pressed on, and the two are summed rather than mixed. Without that split,
// releasing the movement key would leave the player skating.
//
// The resolve order — push out of every solid brush in the chunk, then try a
// step-up if that left the body short of where it meant to go — runs once per
// swept substep rather than once per frame, so a body moving faster than its
// own width still meets every brush on the way. At walking speed there is
// exactly one substep and this is unchanged from before.
func MovePlayer(p *world.Entity, lvl *world.Level, mv PlayerMove) (res PlayerMoveResult) {
	if p == nil || lvl == nil {
		return res
	}

	// Where the player currently lives. Everything below is in this frame's
	// coordinates — its position, its velocity, the chunks it resolves against,
	// and which way is down. That is the whole of what makes walking around a
	// moving ship work: none of the arithmetic changes, only the space it is
	// expressed in.
	frame := lvl.Frames.FrameFor(p)
	// The player moves before the entities do, so it starts the placement
	// broadphase's tick too (scenery_index.go), and reads ahead the
	// collision meshes it is coming near.
	Epoch()
	prefetchBodies(lvl, p, false)

	// A seated occupant is constrained, not simulated. Pinning every frame
	// rather than once on sitting down is what keeps them in the chair while
	// the chair accelerates — and skipping the resolve is what stops the seat's
	// own geometry pushing them out of it.
	if p.Seat != "" {
		if v := world.FindEntity(lvl, p.Frame); v != nil && v.IsVehicle() {
			if st := v.Vehicle.Seat(p.Seat); st != nil {
				// PinToSeat clears both halves of the velocity, so the first
				// frame after standing up has no stale key contribution to
				// subtract — see Entity.WalkVelocity.
				PinToSeat(p, st)
				p.UpdateLocation(lvl)
				return PlayerMoveResult{OnGround: true}
			}
		}
	}

	// Take last frame's key contribution back out, leaving what the body is
	// carrying on its own.
	//
	// Velocity holds the whole truth between frames — momentum plus whatever
	// the keys were asking for — because that is what `@player.prop.velocity`
	// has to report, and a property that reads zero for a body visibly running
	// down a corridor is a property nobody can use. But only the carried part
	// may be integrated, damped and spent on walls: the keys are a continuous
	// request that ends the frame they are released, not something the body
	// keeps. This subtraction is the seam between those two readings, and
	// WalkVelocity (entity.go) is what makes it exact rather than a guess.
	//
	// It is also why a script write lands correctly with no special case: a
	// script writing the total says "the body is now doing this", and taking
	// the key part back out of that leaves precisely the momentum that, with
	// the keys still held, adds back up to what was asked for.
	p.Velocity = p.Velocity.Sub(p.WalkVelocity)

	// Work out the whole frame's displacement first, then walk it.
	//
	// Apply the pending jump impulse before gravity so the jump frame is not
	// immediately reduced by one tick of it.
	if mv.Jump {
		p.Velocity[1] = mv.JumpVelocity
	}
	// Free-fly is not weightlessness: the vertical keys are doing the moving,
	// so nothing pulls and nothing is carried. Gravity being off on its own
	// still leaves the body drifting on whatever momentum it has, which is what
	// makes zero-g a different thing from a noclip camera.
	if mv.Gravity && !mv.Fly {
		applyPlayerGravity(p, mv)
	}
	dampMomentum(p, mv)

	// Carried momentum and this frame's intent are added, never merged: the
	// first survives the frame and the second does not. Both are displacements
	// by the time they get here — Intent already is one, and the velocity is
	// turned into one by dt.
	disp := mv.Intent.Add(p.Velocity.Mul(mv.DT))

	// Split the move so no single step outruns the body. At walking speed this
	// is one step and the arithmetic below is exactly what it always was; only
	// something moving fast enough to step past a wall pays for more.
	// How tall a riser this body walks up is the level's to say, not this
	// package's — see world.PlayerSettings. Resolved once here rather than per
	// substep because it cannot change inside a frame, and because it is the
	// only thing in this loop that asks the level a question rather than the
	// geometry under the body.
	stepHeight := lvl.Player.EffectiveStepHeight()
	radius := p.BoundHalfX
	if p.BoundHalfZ > radius {
		radius = p.BoundHalfZ
	}

	steps, disp := SweepPlan(p.BoundHalfExtents(), disp)
	stepDisp := disp.Mul(1 / float32(steps))

	newPos := p.Position
	var chunks []*world.Chunk
	for i := 0; i < steps; i++ {
		intended := newPos.Add(stepDisp)

		// The resolvers below work in each found chunk's own local space (see
		// chunkLocal); newPos/intended stay in the player's frame everywhere
		// else.
		//
		// Every chunk the move's box overlaps, from where the body stood to
		// where it is going and a step's height up: a body on the seam between
		// two chunks is solid against the floor and walls of both, and one
		// whose raw target lands outside the destination chunk's bounds —
		// pushing through its outer wall, say — still meets the brushes of the
		// chunk it was standing in. A carried frame keeps its one chunk.
		chunks = solidChunksFor(lvl, frame, newPos, intended,
			mgl32.Vec3{radius, p.BoundHalfY, radius}, stepHeight, chunks[:0])

		// Everything the body is solid against, as one pass, in the frame's own
		// coordinates: the chunks' brushes — each resolved in its chunk's local
		// space, which is where its verts are — and then every entity's own
		// solid, resolved in that entity's.
		//
		// The second half is not in this frame's chunks at all: those brushes
		// are in the entity's coordinates and so are absent from the spatial
		// hash (entity_brushes.go). Without it the player would walk through a
		// ship's hull on the way to its hold.
		//
		// Bundled into one function because the step-up below has to retry
		// against exactly the same set. A threshold at a ship's door is a riser
		// like any other, and while the retry knew only about the chunk it
		// found the raised probe unobstructed, took the step, and was shoved
		// straight back out by the hull — so an entity's interior could be
		// walked into only if its floor was flush with the ground outside.
		resolve := func(pos mgl32.Vec3) (mgl32.Vec3, bool, bool) {
			pos, hitFloor, hitCeiling := pushOutOfChunksBrushes(chunks, pos, radius, p.BoundHalfY)
			// Cooked meshes in the hulls and mesh modes (scenery_mesh.go),
			// for a body in world space.
			if frame.IsWorld() {
				var sf, sc bool
				pos, sf, sc = pushOutOfScenery(lvl, pos, radius, p.BoundHalfY)
				hitFloor, hitCeiling = hitFloor || sf, hitCeiling || sc
			}
			out, eFloor, eCeiling := resolveAgainstEntityBrushes(
				lvl, p, frame, pos, radius, p.BoundHalfY)
			return out, hitFloor || eFloor, hitCeiling || eCeiling
		}

		// Resolved once flat, then retried from a step's height up if that left
		// the body short of where it was going. Both inside the substep loop,
		// so a fast approach meets a wall on the step it reaches it and the
		// ground flags below are the ones that step actually produced.
		resolved, hitFloor, hitCeiling := resolve(intended)
		newPos = StepUp(resolve, mgl32.Vec2{intended.X(), intended.Z()}, resolved, stepHeight)

		// Momentum the resolve refused is spent on whatever refused it, rather
		// than stored up to be released the moment the body slides clear of the
		// wall. Only the component pressing INTO the surface goes: a body
		// launched along a corridor keeps the speed it is sliding with. This is
		// entityPushOut's rule, applied to the player.
		spendBlockedMomentum(p, intended, newPos)

		// Landing and bonking: kill vertical velocity on contact either way,
		// whether or not gravity is on. A body that arrived here falling has to
		// stop falling — momentum from an impulse lands exactly as a fall does,
		// and gating this on gravity is what used to leave a player caught
		// mid-fall by a gravity switch sinking through the floor forever.
		// Checked per step so a fast fall stops on the frame it arrives rather
		// than carrying its speed through the rest of the frame's steps.
		if hitFloor {
			if p.Velocity.Y() < 0 && !res.OnGround {
				// The speed it arrived at, read before it is zeroed — the only
				// moment it exists. First contact only: a later substep would
				// report the 0 this one just wrote.
				res.Landed, res.Impact = !mv.Grounded, -p.Velocity.Y()
			}
			if p.Velocity.Y() < 0 {
				p.Velocity[1] = 0
			}
			res.OnGround = true
		}
		if hitCeiling && p.Velocity.Y() > 0 {
			p.Velocity[1] = 0
		}
	}

	p.Position = newPos

	// A sealed frame portal has no wall geometry, so it has to be resolved
	// against explicitly or a misaligned doorway is simply a hole in the hull.
	p.Position = ResolveSealedPortals(lvl, p, p.Position)
	newPos = p.Position

	// Where the body ENDED, asked fresh, rather than whichever chunk the last
	// substep happened to resolve against.
	//
	// Those are not the same question. The loop above falls back to the chunk
	// the body was already standing in when its target lands outside every one
	// of them, because that chunk still has the brushes to push back with — so
	// the resolve's answer is never nil once a body has stood anywhere, and
	// taking it here meant CurrentChunk could never become empty again.
	// "Aboard, and inside none of the frame's chunks" is exactly what tells
	// collision a body has walked off a ship (steppedOffFrame), so a body that
	// walked out of a hold stayed aboard it for good, carried around by a ship
	// it was no longer standing in and with the world's own geometry no longer
	// solid to it.
	p.UpdateLocation(lvl)

	// …and put the keys back in, so what anything reads between now and the
	// next move is what the body is actually doing. A player walking forward at
	// six units a second reads six units a second.
	p.WalkVelocity = walkVelocity(mv)
	p.Velocity = p.Velocity.Add(p.WalkVelocity)
	return res
}

// walkVelocity is this frame's movement keys expressed as a speed rather than
// as the one-frame displacement they arrive as.
//
// A zero-length frame has no speed to report — dividing by it would produce an
// infinity that then poisons Velocity permanently — and a paused or
// single-stepped frame is exactly that case.
func walkVelocity(mv PlayerMove) mgl32.Vec3 {
	if mv.DT <= 0 {
		return mgl32.Vec3{}
	}
	return mv.Intent.Mul(1 / mv.DT)
}

// applyPlayerGravity accelerates the body along -Y and holds it to its terminal
// speed. Entity.ApplyGravity's arithmetic, kept separate because the player's
// pull and cap are handed in already resolved rather than read off its own
// collision flags — the player's live on the level's `player:` block.
func applyPlayerGravity(p *world.Entity, mv PlayerMove) {
	max := mv.MaxFallSpeed
	if max <= 0 {
		max = world.DefaultMaxFallSpeed
	}
	v := p.Velocity.Y() - mv.GravityAccel*mv.DT
	switch {
	case v < -max:
		v = -max
	case v > max && mv.GravityAccel < 0:
		// Only an UPWARD gravity is capped upward; a jump and a launch pad are
		// velocities gravity did not put there and must not be clipped.
		v = max
	}
	p.Velocity[1] = v
}

// dampMomentum bleeds the horizontal momentum away, fast on the ground and
// almost not at all in the air.
//
// Exponential rather than a subtraction per frame, so the result does not
// depend on the frame rate: a 30fps frame sheds exactly what two 60fps frames
// would. Flying damps as if grounded — a free-fly camera that kept coasting
// after a shove would be a camera nobody could aim.
func dampMomentum(p *world.Entity, mv PlayerMove) {
	if p.Velocity.X() == 0 && p.Velocity.Z() == 0 {
		return
	}
	drag := playerAirDrag
	if mv.Grounded || mv.Fly {
		drag = playerGroundDrag
	}
	k := float32(math.Exp(float64(-drag * mv.DT)))
	p.Velocity[0] *= k
	p.Velocity[2] *= k
	// Below a millimetre a second there is nothing left to carry, and letting
	// it tail off asymptotically leaves `@player.prop.velocity` reading a
	// non-zero number for a body that is plainly standing still.
	if p.Velocity.X()*p.Velocity.X()+p.Velocity.Z()*p.Velocity.Z() < 1e-6 {
		p.Velocity[0], p.Velocity[2] = 0, 0
	}
}

// spendBlockedMomentum removes the part of the momentum that is pressing into
// whatever stopped the body, leaving the part that is sliding along it.
//
// The push-out direction is recovered from the move itself — where the body
// meant to go against where it ended — because that is the only normal
// available here: the resolvers report "something was hit", not what or which
// way it faced. Horizontal only: the vertical case is landing and head-bonking,
// which the caller handles from the flags it already has.
func spendBlockedMomentum(p *world.Entity, intended, actual mgl32.Vec3) {
	push := mgl32.Vec3{actual.X() - intended.X(), 0, actual.Z() - intended.Z()}
	l := push.Len()
	if l < 1e-6 {
		return
	}
	n := push.Mul(1 / l)
	if into := p.Velocity.Dot(n); into < 0 {
		p.Velocity = p.Velocity.Sub(n.Mul(into))
	}
}
