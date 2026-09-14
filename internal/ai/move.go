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
// A level with no navigation at all — open space, nothing built — is steered
// straight at the goal instead. An agent with no gravity follows the path's
// heights as well as its turns, which is what makes a hovering drone take the
// stairs rather than the floor under them.

import (
	"math"

	"foundry/internal/hooks"
	"foundry/internal/nav"
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
		if !plan(lvl, e, st, feet, goal) {
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

	steer(e, spec, feet, target, last, speedScale, faceMove, dt)
	return followMoving
}

// plan finds a path and installs it. A level with no navigation to speak of
// gets a straight line.
func plan(lvl *world.Level, e *world.Entity, st *world.AIState, feet, goal mgl32.Vec3) bool {
	st.RepathTimer = repathInterval
	st.PathGoal = goal
	st.PathIndex = 0
	st.StuckTimer = 0
	st.BestDist = float32(math.MaxFloat32)

	grid := nav.For(lvl, ProfileFor(lvl, e))
	if grid == nil || len(grid.Nodes) == 0 {
		st.Path = append(st.Path[:0], goal)
		st.HasPath, st.PathPartial = true, false
		return true
	}
	p, ok := grid.FindPath(feet, goal)
	if !ok {
		st.HasPath = false
		st.Path = st.Path[:0]
		return false
	}
	st.Path = append(st.Path[:0], p.Points...)
	st.HasPath = true
	st.PathPartial = p.Partial
	return true
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
func steer(e *world.Entity, spec *world.AISpec, feet, target mgl32.Vec3, last bool, speedScale float32, faceMove bool, dt float32) {
	dir := mgl32.Vec3{target.X() - feet.X(), 0, target.Z() - feet.Z()}
	dist := dir.Len()
	if speedScale <= 0 {
		speedScale = 1
	}
	speed := spec.Speed * speedScale
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
		desired = dir.Mul(speed / dist)
	}
	flat := approach(mgl32.Vec3{e.Velocity.X(), 0, e.Velocity.Z()}, desired, accelStep(spec, dt))
	e.Velocity[0], e.Velocity[2] = flat.X(), flat.Z()
	if !e.HasGravity() {
		dy := target.Y() - feet.Y()
		vy := clampf(dy*4, -speed, speed)
		e.Velocity[1] = approach1(e.Velocity.Y(), vy, accelStep(spec, dt))
	}
	if faceMove && spec.FaceMovement && desired.Len() > 0.05 {
		turnTo(e, yawToward(desired), spec.TurnRate*dt)
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
