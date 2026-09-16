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
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// PlayerMove is one frame of gameplay input to MovePlayer.
//
// The tuning constants are passed in rather than defined here because they are
// authored in the window layer alongside the keys that trigger them, and
// because a vehicle's occupant may eventually want different ones.
type PlayerMove struct {
	// Intent is the displacement the movement keys asked for this frame,
	// already scaled for frame time, in the same space as the player's
	// position. Horizontal motion is entirely this; vertical motion is this
	// plus gravity, which is why free-fly puts its up/down in here too.
	Intent mgl32.Vec3

	Jump    bool // a jump was requested this frame
	Gravity bool // gravity is on

	// DTScale is DeltaTime*60. The three constants below are per-frame values
	// tuned against a 60fps baseline, so everything vertical is scaled by it.
	DTScale float32

	GravityAccel float32
	MaxFallSpeed float32
	JumpVelocity float32
}

// MovePlayer advances the player by one frame, in whatever frame it is standing
// in, and returns whether it ended on the ground.
//
// It writes p.Position, p.Velocity[1] and the derived CurrentChunk, all in the
// player's own frame.
// The caller owns the camera and the gravity switches: this returns the ground
// flag rather than setting it, because whether "on the ground" is meaningful at
// all depends on gravity being on, and that is the caller's question.
//
// The resolve order — push out of every solid brush in the chunk, then try a
// step-up if that left the body short of where it meant to go — runs once per
// swept substep rather than once per frame, so a body moving faster than its
// own width still meets every brush on the way. At walking speed there is
// exactly one substep and this is unchanged from before.
func MovePlayer(p *world.Entity, lvl *world.Level, mv PlayerMove) (onGround bool) {
	if p == nil || lvl == nil {
		return false
	}

	// Where the player currently lives. Everything below is in this frame's
	// coordinates — its position, its velocity, the chunks it resolves against,
	// and which way is down. That is the whole of what makes walking around a
	// moving ship work: none of the arithmetic changes, only the space it is
	// expressed in.
	frame := lvl.Frames.FrameFor(p)

	// A seated occupant is constrained, not simulated. Pinning every frame
	// rather than once on sitting down is what keeps them in the chair while
	// the chair accelerates — and skipping the resolve is what stops the seat's
	// own geometry pushing them out of it.
	if p.Seat != "" {
		if v := world.FindEntity(lvl, p.Frame); v != nil && v.IsVehicle() {
			if st := v.Vehicle.Seat(p.Seat); st != nil {
				PinToSeat(p, st)
				p.UpdateLocation(lvl)
				return true
			}
		}
	}

	// Work out the whole frame's displacement first, then walk it.
	//
	// Apply the pending jump impulse before gravity so the jump frame is not
	// immediately reduced by one tick of it.
	if mv.Jump {
		p.Velocity[1] = mv.JumpVelocity
	}
	if mv.Gravity {
		p.Velocity[1] -= mv.GravityAccel * mv.DTScale
		if p.Velocity[1] < -mv.MaxFallSpeed {
			p.Velocity[1] = -mv.MaxFallSpeed
		}
	}
	disp := mv.Intent
	disp[1] += p.Velocity[1] * mv.DTScale

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

		// Landing and bonking: kill vertical velocity on contact either way.
		// Checked per step so a fast fall stops on the frame it arrives rather
		// than carrying its speed through the rest of the frame's steps.
		if mv.Gravity {
			if hitFloor {
				p.Velocity[1] = 0
				onGround = true
			}
			if hitCeiling && p.Velocity[1] > 0 {
				p.Velocity[1] = 0
			}
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
	return onGround
}
