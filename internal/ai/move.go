package ai

// move.go — following a path by setting a velocity.
//
// The path comes from the level's navigation grid (internal/nav), sized to the
// agent's bounds. The agent aims at one corner at a time, moves on to the next
// once close, and eases to a stop at the last. It re-plans when the goal has
// moved far enough to matter — a chased target — and when it has made no
// headway for a while, which is what a door shut in its face or a crowd in a
// corridor looks like from inside the controller.
//
// A level with no navigation at all — open space, nothing to build on — is
// steered straight at the goal instead. A level whose navigation is still being
// built in the background (AsyncNav) is not that: its agents hold still. An agent with no gravity follows the path's
// heights as well as its turns, which is what makes a hovering drone take the
// stairs rather than the floor under them.

import (
	"math"

	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

type followResult int

const (
	followMoving followResult = iota
	followArrived
	// followBlocked is the end of a partial path: as close as it gets.
	followBlocked
	// followFailed is no path at all.
	followFailed
	// followHolding is waiting for navigation to be built (ai.AsyncNav): the
	// agent stands still, and nothing about the walk is reported as over.
	followHolding
)

// follow steers the agent one tick along a path to goal.
func follow(lvl *world.Level, e *world.Entity, st *world.AIState, goal mgl32.Vec3, arrive, speedScale float32, faceMove bool, dt float32) followResult {
	spec := e.AI
	feet := feetOf(lvl, e)

	// Held after a failure (tick counts it down), so a hopeless request is
	// not re-planned every frame.
	if st.FailTimer > 0 {
		brake(e, spec, dt)
		return followFailed
	}

	st.RepathTimer -= dt
	// A goal that has drifted a fifth of the way to it — a chased target — is
	// worth re-planning for once the timer allows. One that has jumped by more
	// than the arrive radius is a different goal, and is planned for at once:
	// a path that ends within reach of the old goal can report arriving
	// forever while never reaching the new one.
	shift := goal.Sub(st.PathGoal).Len()
	jumped := shift > arrive*0.5
	drifted := shift > maxf(0.5, 0.2*goal.Sub(feet).Len())
	if !st.HasPath || jumped || (drifted && st.RepathTimer <= 0) {
		switch plan(lvl, e, st, feet, goal) {
		case planHold:
			brake(e, spec, dt)
			return followHolding
		case planFailed:
			st.FailTimer = failHold
			hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_path_failed", Agent: e.ID})
			brake(e, spec, dt)
			return followFailed
		}
	}

	cornerRadius := maxf(arrive*0.75, 0.2)
	for st.PathIndex < len(st.Path)-1 && distXZ(feet, st.Path[st.PathIndex]) <= cornerRadius {
		st.PathIndex++
		st.BestDist = float32(math.MaxFloat32)
		st.StuckCount = 0
	}
	target := st.Path[st.PathIndex]
	last := st.PathIndex == len(st.Path)-1
	d := distXZ(feet, target)

	if last && d <= arrive {
		brake(e, spec, dt)
		if st.PathPartial {
			return followBlocked
		}
		st.StuckCount = 0
		return followArrived
	}

	// No headway toward the corner for stuckTime is a re-plan; too many of
	// those in a row is a failure, held for a moment before trying again.
	if d < st.BestDist-0.05 {
		st.BestDist = d
		st.StuckTimer = 0
	} else if st.StuckTimer += dt; st.StuckTimer > stuckTime {
		st.StuckTimer = 0
		st.StuckCount++
		st.HasPath = false
		if st.StuckCount > maxStuckRepaths {
			st.StuckCount = 0
			st.FailTimer = failHold
			hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_path_failed", Agent: e.ID})
			brake(e, spec, dt)
			return followFailed
		}
	}

	steer(lvl, e, spec, feet, target, last, speedScale, faceMove, dt)
	return followMoving
}

type planResult int

const (
	planOK planResult = iota
	planFailed
	// planHold is no grid yet (ai.AsyncNav): not a failure, and above all not
	// the straight line — an agent steered straight at its goal while the
	// level's navigation builds walks into the walls.
	planHold
)

// plan finds a path and installs it. A level with no navigation to speak of
// gets a straight line.
func plan(lvl *world.Level, e *world.Entity, st *world.AIState, feet, goal mgl32.Vec3) planResult {
	st.RepathTimer = repathInterval
	st.PathGoal = goal
	st.PathIndex = 0
	st.StuckTimer = 0
	st.BestDist = float32(math.MaxFloat32)

	grid := gridFor(lvl, e)
	if grid == nil && AsyncNav {
		st.HasPath = false
		st.Path = st.Path[:0]
		return planHold
	}
	if grid == nil || len(grid.Nodes) == 0 {
		st.Path = append(st.Path[:0], goal)
		st.HasPath, st.PathPartial = true, false
		return planOK
	}
	p, ok := grid.FindPath(feet, goal)
	if !ok {
		st.HasPath = false
		st.Path = st.Path[:0]
		return planFailed
	}
	st.Path = append(st.Path[:0], p.Points...)
	st.HasPath = true
	st.PathPartial = p.Partial
	return planOK
}

// clearPath forgets the current path, so the next move plans afresh.
func clearPath(st *world.AIState) {
	st.HasPath = false
	st.Path = st.Path[:0]
	st.PathIndex = 0
	st.PathPartial = false
	st.StuckTimer = 0
	st.BestDist = float32(math.MaxFloat32)
}

// steer accelerates the agent toward a corner, and turns it to face the way it
// is going.
//
// The steering is worked in world space, where the path is. An agent aboard a
// frame keeps its Velocity in that frame's coordinates, so the velocity is
// carried out to be steered and back in to be stored.
func steer(lvl *world.Level, e *world.Entity, spec *world.AISpec, feet, target mgl32.Vec3, last bool, speedScale float32, faceMove bool, dt float32) {
	var frame *world.Frame
	if e.Frame != "" {
		frame = lvl.Frames.FrameFor(e)
	}
	vel := frame.DirToWorld(e.Velocity)
	dir := mgl32.Vec3{target.X() - feet.X(), 0, target.Z() - feet.Z()}
	dist := dir.Len()
	if speedScale <= 0 {
		speedScale = 1
	}
	speed := spec.Speed * speedScale
	// Soft ground (sand, snow) drags at a walker's stride: the speed it asks
	// for, not the one it has, so the drag is the same at any acceleration
	// (world.WalkerDrag, from the sim's footsteps).
	if e.HasGravity() {
		speed *= 1 - world.WalkerDrag(e.ID)
	}
	if last && spec.Accel > 0 {
		// Arrive rather than overshoot: slow down over the distance it takes to
		// stop from full speed.
		stopping := maxf(speed*speed/(2*spec.Accel), 0.2)
		if dist < stopping {
			speed *= dist / stopping
		}
	}
	var desired mgl32.Vec3
	if dist > 1e-4 {
		desired = avoid(lvl, e, feet, dir.Mul(speed/dist))
	}
	flat := approach(mgl32.Vec3{vel.X(), 0, vel.Z()}, desired, accelStep(spec, dt))
	vel[0], vel[2] = flat.X(), flat.Z()
	if !e.HasGravity() {
		dy := target.Y() - feet.Y()
		vy := clampf(dy*4, -speed, speed)
		vel[1] = approach1(vel.Y(), vy, accelStep(spec, dt))
	}
	e.Velocity = frame.DirFromWorld(vel)
	if faceMove && spec.FaceMovement && desired.Len() > 0.05 {
		turnTo(lvl, e, yawToward(desired), spec.TurnRate*dt)
	}
}

// brake sheds the agent's walking speed, and its climb when it has no gravity
// to do that for it.
func brake(e *world.Entity, spec *world.AISpec, dt float32) {
	step := accelStep(spec, dt)
	flat := approach(mgl32.Vec3{e.Velocity.X(), 0, e.Velocity.Z()}, mgl32.Vec3{}, step)
	e.Velocity[0], e.Velocity[2] = flat.X(), flat.Z()
	if !e.HasGravity() {
		e.Velocity[1] = approach1(e.Velocity.Y(), 0, step)
	}
}

// accelStep is how much speed may change this tick. No acceleration at all
// means instant.
func accelStep(spec *world.AISpec, dt float32) float32 {
	if spec.Accel <= 0 {
		return float32(math.MaxFloat32)
	}
	return spec.Accel * dt
}

func approach(v, want mgl32.Vec3, step float32) mgl32.Vec3 {
	d := want.Sub(v)
	l := d.Len()
	if l <= step || l < 1e-6 {
		return want
	}
	return v.Add(d.Mul(step / l))
}

func approach1(v, want, step float32) float32 {
	switch {
	case want > v+step:
		return v + step
	case want < v-step:
		return v - step
	}
	return want
}

func clampf(v, lo, hi float32) float32 {
	return maxf(lo, minf(hi, v))
}

// avoidRange is how far ahead, in seconds of walking, an agent looks for bodies
// in its way, over and above a fixed arm's length.
const (
	avoidAhead   = float32(0.6)
	avoidReach   = float32(1.2)
	avoidSideGap = float32(0.3)
)

// avoid bends a walking velocity round the bodies in its way. A path is planned
// on the level's shape alone, so two squadmates swapping sides, or a soldier
// walking back through its own squad, would each walk straight into the other
// and stand there pushing. Instead a body that is ahead and close to the line
// is passed: on the side it is not on, or — dead ahead — on the right, which
// both of two bodies meeting head-on do, so they part rather than mirror each
// other. Bodies already overlapping are eased apart. The speed is kept; only
// the heading bends, and the path takes over again once the way is clear.
func avoid(lvl *world.Level, e *world.Entity, feet, desired mgl32.Vec3) mgl32.Vec3 {
	speed := desired.Len()
	if speed < 0.05 || !e.CollidesWithEntities() {
		return desired
	}
	fwd := desired.Mul(1 / speed)
	left := mgl32.Vec3{fwd.Z(), 0, -fwd.X()}
	r := bodyRadius(e)
	look := avoidReach + speed*avoidAhead
	var lateral float32
	var apart mgl32.Vec3
	consider := func(o *world.Entity) {
		if o == e || o == nil || !o.HasBounds() || !o.CollidesWithEntities() || o.HasSolidBrushes() || o.Frame != e.Frame {
			return
		}
		of := feetOf(lvl, o)
		d := mgl32.Vec3{of.X() - feet.X(), 0, of.Z() - feet.Z()}
		if absf(of.Y()-feet.Y()) > 2 {
			return
		}
		rs := r + bodyRadius(o)
		dist := d.Len()
		if dist > look+rs {
			return
		}
		if dist < rs+0.1 && dist > 1e-3 {
			apart = apart.Sub(d.Mul((rs + 0.1 - dist) / dist))
		}
		ahead := d.Dot(fwd)
		side := d.Dot(left)
		if ahead <= 0 || ahead > look+rs || absf(side) > rs+avoidSideGap {
			return
		}
		// Something going our way at least as fast is not in the way.
		if o.Velocity.Dot(fwd) > speed*0.8 {
			return
		}
		w := 1 - ahead/(look+rs)
		switch {
		case side > 0.05:
			lateral -= w // it is on the left: pass it on the right
		case side < -0.05:
			lateral += w
		default:
			lateral -= w // dead ahead: keep right
		}
	}
	for i := range lvl.Entities {
		consider(&lvl.Entities[i])
	}
	consider(world.PlayerEntity)
	if lateral == 0 && apart == (mgl32.Vec3{}) {
		return desired
	}
	lateral = clampf(lateral, -1.5, 1.5)
	bent := fwd.Add(left.Mul(lateral)).Add(apart.Mul(2))
	if l := bent.Len(); l > 1e-4 {
		return bent.Mul(speed / l)
	}
	return desired
}

// bodyRadius is how wide a body is across the ground.
func bodyRadius(e *world.Entity) float32 {
	h := e.BoundHalfExtents()
	return maxf(h.X(), h.Z())
}
