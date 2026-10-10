package ai

// follow.go — keeping up with a leader, as a squadmate does.
//
// An agent whose spec names a leader (Follow) keeps a place in a loose group
// behind it: everyone following the same leader is spread across an arc about
// a spacing back, in the order the level lists them, each nudged off its even
// share by an amount fixed to its ID so the group looks gathered rather than
// drilled. Behind is measured against the way the
// leader has been heading, which is read off its movement rather than its
// facing, so it works the same for the player (whose facing is a camera) as
// for an agent, and a leader turning on the spot does not send its squad
// running round it.
//
// Nobody moves for small things. A follower stands where it is until its slot
// has drifted a good way off, then walks — or runs, once it has fallen well
// behind. While the leader keeps going it aims a little ahead of its slot and
// keeps walking; once the leader stops, it settles on the slot and stands.
// The slot is only re-picked once the leader's movement has shifted it far
// enough to be worth a new path.
//
// # Fighting
//
// A follower with a target fights it the way its spec says — a watcher stands
// and shoots — for as long as its leader stays within FollowLeash. Past that it
// regroups: it heads back to its slot with its weapon still on the target,
// posting :target_in_range: as it goes, and fights from where it stands again
// once it has closed half the distance.

import (
	"hash/fnv"
	"math"
	"strings"

	"foundry/internal/nav"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// leaderMovingSpeed is how fast a leader must be going, in units per
	// second, for its movement to say which way it is heading.
	leaderMovingSpeed = float32(0.4)
	// leaderTurnRate is how fast, in radians a second, the group swings
	// round to a new heading — slow enough that a leader strafing or stepping
	// back does not send its squad running round it.
	leaderTurnRate = float32(2.5)
	// runGapSlots and walkGapSlots are, in spacings, how far behind a
	// follower breaks into a run and how close it must get to walk again.
	runGapSlots  = float32(3)
	walkGapSlots = float32(1.5)
	// followLead is how far ahead, in seconds of the leader's travel, a
	// follower aims while the leader is on the move — enough that it walks
	// alongside rather than arriving, stopping and setting off again.
	followLead = float32(0.6)
	// leaderVelSmoothing is how quickly, per second, the leader's measured
	// velocity follows its movement.
	leaderVelSmoothing = float32(8)
)

// leaderOf is the entity an agent keeps up with, or nil for none — including
// a leader that has gone, is down, or is the agent itself.
func leaderOf(lvl *world.Level, e *world.Entity, player *world.Entity) *world.Entity {
	id := strings.TrimPrefix(strings.TrimSpace(e.AI.Follow), "@")
	if id == "" {
		return nil
	}
	l := targetEntity(lvl, id, player)
	if l == e || !targetable(l, player) {
		return nil
	}
	return l
}

// followSpacing is the agent's group spacing, never zero.
func followSpacing(spec *world.AISpec) float32 {
	if spec.FollowDistance > 0 {
		return spec.FollowDistance
	}
	return world.DefaultAISpec().FollowDistance
}

// trackLeader keeps the agent's picture of its leader current: where it is and
// which way it has been heading. Every tick, whatever the agent is doing, so a
// follower that has been fighting knows where its squad has gone.
func trackLeader(lvl *world.Level, e *world.Entity, st *world.AIState, leader *world.Entity, dt float32) {
	feet := feetOf(lvl, leader)
	if st.FollowLeader != leader.ID {
		forgetLeader(st)
		st.FollowLeader = leader.ID
		st.FollowLast = feet
		// Until it moves, a leader that is an entity heads the way it faces.
		// The player's facing is its camera's, which navigation cannot see,
		// so it waits for the player to walk somewhere.
		if leader != world.PlayerEntity {
			fwd := forwardXZ(lvl, leader)
			st.FollowHeading = yawToward(mgl32.Vec3{fwd.X(), 0, fwd.Y()})
			st.FollowHeaded = true
		}
		return
	}
	step := feet.Sub(st.FollowLast)
	st.FollowLast = feet
	if dt <= 0 {
		return
	}
	step[1] = 0
	// A leader that jumped — crossed a frame, respawned, was teleported by a
	// script — is not moving at hundreds of metres a second.
	if step.Len() > 5 {
		st.FollowLeaderVel = mgl32.Vec3{}
		return
	}
	k := minf(1, leaderVelSmoothing*dt)
	st.FollowLeaderVel = st.FollowLeaderVel.Add(step.Mul(1 / dt).Sub(st.FollowLeaderVel).Mul(k))
	if step.Len()/dt < leaderMovingSpeed {
		return
	}
	want := yawToward(step)
	if !st.FollowHeaded {
		st.FollowHeading, st.FollowHeaded = want, true
		return
	}
	diff := world.WrapAngle(want - st.FollowHeading)
	maxStep := leaderTurnRate * dt
	if diff > maxStep {
		diff = maxStep
	} else if diff < -maxStep {
		diff = -maxStep
	}
	st.FollowHeading = world.WrapAngle(st.FollowHeading + diff)
}

// leaderMoving reports whether the agent's leader is on the move.
func leaderMoving(st *world.AIState) bool {
	return st.FollowLeaderVel.Len() >= leaderMovingSpeed
}

// forgetLeader clears everything kept about a leader.
func forgetLeader(st *world.AIState) {
	st.FollowLeader = ""
	st.FollowHeaded = false
	st.FollowLeaderVel = mgl32.Vec3{}
	st.FollowHasGoal = false
	st.FollowMoving, st.FollowRunning, st.FollowRegroup = false, false, false
}

// squadPlace is the agent's place among everyone following the same leader,
// counted in the order the level lists them, and how many of them there are.
func squadPlace(lvl *world.Level, e *world.Entity, leader *world.Entity) (k, n int) {
	k = -1
	for i := range lvl.Entities {
		o := &lvl.Entities[i]
		if o == e {
			k = n
			n++
			continue
		}
		if o.AI == nil || !o.AI.Enabled || o.AI.Follow == "" {
			continue
		}
		if leaderOf(lvl, o, nil) == leader {
			n++
		}
	}
	if k < 0 {
		k, n = n, n+1
	}
	return k, n
}

const (
	// groupStep is the bearing, in radians, between neighbouring followers'
	// places, and groupSpread the widest the group fans out across, measured
	// through the point directly behind the leader.
	groupStep   = float32(40 * math.Pi / 180)
	groupSpread = float32(160 * math.Pi / 180)
	// groupJitter is how far, in radians, each follower's own bearing strays
	// from its even share, and groupDepthJitter how far its distance strays as
	// a fraction of the spacing — what keeps a group from reading as a fan
	// drawn with a protractor.
	groupJitter      = float32(6 * math.Pi / 180)
	groupDepthJitter = float32(0.2)
)

// groupPlace is where in the group the k'th of n followers stands: a bearing
// off directly behind the leader, in radians, and a distance back. Each
// follower's is nudged by an amount fixed to its ID, so the group is uneven but
// every member keeps the same spot in it from tick to tick. A big group that
// has fanned out as wide as it goes staggers alternate members a little further
// back.
func groupPlace(id string, k, n int, spacing float32) (bearing, dist float32) {
	spread := groupStep * float32(n-1)
	if spread > groupSpread {
		spread = groupSpread
	}
	if n > 1 {
		bearing = -spread/2 + spread*float32(k)/float32(n-1)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	sum := h.Sum32()
	j1 := float32(sum&0xffff)/0xffff*2 - 1
	j2 := float32(sum>>16)/0xffff*2 - 1
	bearing += j1 * groupJitter
	dist = spacing * (1 + j2*groupDepthJitter)
	if groupStep*float32(n-1) > groupSpread && k%2 == 1 {
		dist += spacing * 0.6
	}
	return bearing, dist
}

// groupOffset carries a place in the group out to world space, for a leader
// heading yaw: behind the leader is its heading turned half round.
func groupOffset(bearing, dist, yaw float32) mgl32.Vec3 {
	a := float64(yaw + math.Pi + bearing)
	return mgl32.Vec3{float32(-math.Sin(a)), 0, float32(-math.Cos(a))}.Mul(dist)
}

// slotOffset is where the agent stands relative to its leader in the open.
func slotOffset(id string, k, n int, spacing, yaw float32) mgl32.Vec3 {
	b, d := groupPlace(id, k, n, spacing)
	return groupOffset(b, d, yaw)
}

// placeFallbacks are the variations of a follower's place tried, in order,
// when the place itself is somewhere it cannot stand or cannot get to from the
// leader: the same bearing, then closer in toward directly behind, then
// nearer. Scaled from the follower's own place, so a squad squeezed by a wall
// still stands apart rather than all making for the one spot.
var placeFallbacks = [][2]float32{{1, 1}, {0.5, 1}, {1, 0.6}, {0.5, 0.6}, {0.2, 0.8}}

// pickSlot is the point the agent should stand at, and how close counts as
// there. Its place in the group is put on the navigation grid — or the first
// variation of it that is on the grid and a short walk from the leader, which
// keeps a squad following down a corridor out of the rooms either side. With
// none of them any good, the agent makes for the leader itself and stops a
// spacing short.
func pickSlot(lvl *world.Level, e *world.Entity, st *world.AIState, leader *world.Entity, arrive float32) (mgl32.Vec3, float32) {
	spacing := followSpacing(e.AI)
	lf := feetOf(lvl, leader)
	if !st.FollowHeaded {
		return lf, spacing
	}
	k, n := squadPlace(lvl, e, leader)
	bearing, dist := groupPlace(e.ID, k, n, spacing)
	lead := mgl32.Vec3{}
	if leaderMoving(st) {
		lead = st.FollowLeaderVel.Mul(followLead)
	}

	grid := gridFor(lvl, e)
	if grid == nil || len(grid.Nodes) == 0 {
		return lf.Add(groupOffset(bearing, dist, st.FollowHeading)).Add(lead), arrive
	}
	ln, ok := grid.Locate(lf, 2)
	if !ok {
		return lf, spacing
	}
	for _, f := range placeFallbacks {
		slot := lf.Add(groupOffset(bearing*f[0], dist*f[1], st.FollowHeading)).Add(lead)
		sn, ok := grid.Locate(slot, spacing*0.4)
		if !ok || grid.Nodes[sn].Region != grid.Nodes[ln].Region {
			continue
		}
		if !grid.WalkableLine(sn, ln) && !shortWalk(grid, lf, slot) {
			continue
		}
		return mgl32.Vec3{slot.X(), grid.Nodes[sn].Floor, slot.Z()}, arrive
	}
	return lf, spacing
}

// shortWalk reports whether the walk between two points is not much longer
// than the straight line — so a place on bumpy ground next to the leader is
// taken, and one on the far side of a wall is not.
func shortWalk(grid *nav.Grid, from, to mgl32.Vec3) bool {
	p, ok := grid.FindPath(from, to)
	if !ok || p.Partial {
		return false
	}
	length, at := float32(0), from
	for _, q := range p.Points {
		length += distXZ(at, q)
		at = q
	}
	return length <= distXZ(from, to)*1.5+1
}

// followIntent keeps the agent in its place behind its leader.
func followIntent(lvl *world.Level, e *world.Entity, st *world.AIState, leader *world.Entity, arrive float32) intent {
	spec := e.AI
	spacing := followSpacing(spec)
	feet := feetOf(lvl, e)

	// Re-pick the slot once the leader has carried it far enough from the one
	// being walked to — or on the first tick — rather than every tick, which
	// would re-plan the path every few frames of a walk. Once the leader
	// stands still the slot is picked closely, so the squad settles where it
	// belongs rather than wherever the last re-pick left it.
	moving := leaderMoving(st)
	lf := feetOf(lvl, leader)
	raw := lf
	if st.FollowHeaded {
		k, n := squadPlace(lvl, e, leader)
		raw = lf.Add(slotOffset(e.ID, k, n, spacing, st.FollowHeading))
	}
	repick, slack := float32(0.25), maxf(spacing*0.5, 1)
	if moving {
		raw = raw.Add(st.FollowLeaderVel.Mul(followLead))
		repick, slack = maxf(spacing*0.3, 0.5), 0.5
	}
	if !st.FollowHasGoal || distXZ(raw, st.FollowGoal) > repick || absf(raw.Y()-st.FollowGoal.Y()) > 1 {
		st.FollowGoal, st.FollowStop = pickSlot(lvl, e, st, leader, arrive)
		st.FollowHasGoal = true
	}
	goal, stop := st.FollowGoal, st.FollowStop

	gap := distXZ(feet, goal)
	if !st.FollowMoving && (gap > stop+slack || absf(feet.Y()-goal.Y()) > 1.5) {
		st.FollowMoving = true
	}
	if !st.FollowMoving {
		st.FollowRunning = false
		in := intent{mode: world.AIEscorting}
		if st.FollowHeaded {
			in.faceYawOn, in.faceYaw = true, st.FollowHeading
		}
		return in
	}

	if spec.RunSpeed > spec.Speed && spec.Speed > 0 {
		switch {
		case gap > spacing*runGapSlots:
			st.FollowRunning = true
		case gap < spacing*walkGapSlots:
			st.FollowRunning = false
		}
	} else {
		st.FollowRunning = false
	}
	in := intent{mode: world.AIEscorting, move: true, goal: goal, arrive: stop}
	if st.FollowRunning {
		in.speedScale = spec.RunSpeed / spec.Speed
		in.moveAnim = spec.RunAnim
	}
	return in
}

// regroupIntent breaks a follower off a fight its leader has left behind. ok is
// false while the leader is still close enough to fight where it stands.
func regroupIntent(lvl *world.Level, e *world.Entity, st *world.AIState, leader, tgt *world.Entity, dt, arrive float32) (intent, bool) {
	leash := e.AI.FollowLeash
	if leash <= 0 {
		st.FollowRegroup = false
		return intent{}, false
	}
	dist := distXZ(feetOf(lvl, e), feetOf(lvl, leader))
	switch {
	case st.FollowRegroup && dist <= maxf(leash*0.5, followSpacing(e.AI)*2):
		st.FollowRegroup = false
	case !st.FollowRegroup && dist > leash:
		st.FollowRegroup = true
		st.FollowMoving = false
	}
	if !st.FollowRegroup {
		return intent{}, false
	}
	in := followIntent(lvl, e, st, leader, arrive)
	in.mode = world.AIRegrouping
	if st.TargetVisible {
		in.faceYawOn = false
		in.face, in.facePos = true, st.LastKnown
		if in.move {
			in.faceWhileMoving = true
		}
	}
	return in, true
}

// ---------- what a script may do ----------

// SetFollow makes the agent keep up with leader, or with nobody when leader is
// nil. Reports whether the agent has a controller and the leader is not itself.
func SetFollow(lvl *world.Level, e, leader *world.Entity) bool {
	if lvl == nil || e == nil || e.AI == nil || leader == e {
		return false
	}
	st := state(lvl, e)
	forgetLeader(st)
	switch {
	case leader == nil:
		e.AI.Follow = ""
	case leader == world.PlayerEntity:
		e.AI.Follow = "player"
	default:
		e.AI.Follow = leader.ID
	}
	return true
}

// Leader is the entity the agent keeps up with, or nil.
func Leader(lvl *world.Level, e *world.Entity) *world.Entity {
	if lvl == nil || e == nil || e.AI == nil {
		return nil
	}
	return leaderOf(lvl, e, nil)
}
