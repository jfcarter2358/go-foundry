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
// It writes p.Position, p.Velocity[1] and the derived CurrentChunk/CurrentSector,
// all in the player's own frame.
// The caller owns the camera and the gravity switches: this returns the ground
// flag rather than setting it, because whether "on the ground" is meaningful at
// all depends on gravity being on, and that is the caller's question.
//
// The resolve order — walls, then slopes, then the two entry refusals, then a
// re-find, then the floor/ceiling
// clamp — is the order the resolvers were written against and is load-bearing.
// The re-find in the middle is not redundant: sliding along a slope can carry
// the player into a different sector, and clamping against the one it started
// in would drop it through the floor of the one it ended in.
//
// That chain runs once per swept substep rather than once per frame, so a body
// moving faster than its own width still meets every wall on the way. At
// walking speed there is exactly one substep and this is unchanged.
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
	// only thing in this loop that has to ask the level a question about the
	// player rather than about the geometry under it.
	stepHeight := lvl.Player.EffectiveStepHeight(p)

	steps, disp := SweepPlan(p.BoundHalfExtents(), disp)
	stepDisp := disp.Mul(1 / float32(steps))

	newPos := p.Position
	var chunk *world.Chunk
	var sector *world.Sector
	for i := 0; i < steps; i++ {
		stepFrom := newPos
		newPos = newPos.Add(stepDisp)

		// The resolvers below work in the found chunk's own local space (see
		// chunkLocal); newPos/stepFrom stay in the player's frame everywhere else.
		chunk, sector = frame.ChunkAndSectorAt(lvl, newPos)
		localPos := ResolveWallCollision(p, chunk, sector, chunkLocal(chunk, newPos))
		localPos = ResolveSlopeCollision(p, chunk, sector, chunkLocal(chunk, stepFrom), localPos)
		// Third horizontal constraint, alongside walls and steep slopes: the
		// outside of a ceiling. Without it a body walking off a height into a
		// lower room's roof is admitted and then yanked down to under that
		// ceiling by ResolveFloorCollision — a teleport that reads as falling
		// through the world. See ResolveCeilingEntry.
		localPos = ResolveCeilingEntry(p, chunk, sector, chunkLocal(chunk, stepFrom), localPos)
		// Fourth: the inside of a floor. A portal draws a riser between two
		// sectors at different heights but is not a wall, so without this the
		// body walks through that riser and is clamped up onto the floor behind
		// it. See ResolveStepEntry.
		localPos = ResolveStepEntry(p, chunk, sector, chunkLocal(chunk, stepFrom), localPos, stepHeight)
		newPos = chunkWorld(chunk, localPos)

		chunk, sector = frame.ChunkAndSectorAt(lvl, newPos)
		localPos = chunkLocal(chunk, newPos)
		hitFloor, hitCeiling := ResolveFloorCollision(p, chunk, sector, &localPos)
		newPos = chunkWorld(chunk, localPos)

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

	if chunk != nil {
		p.CurrentChunk = chunk.ID
	}
	if sector != nil {
		p.CurrentSector = sector.ID
	} else {
		p.CurrentSector = ""
	}
	return onGround
}
