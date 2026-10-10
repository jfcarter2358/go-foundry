package ai

// fight.go — on_target: fight, a target fought the way a soldier fights it.
//
// A watcher stands where it is and turns; a chaser walks up to arm's length.
// Neither looks like a firefight. A fighter stands its ground and shoots for a
// few seconds, then moves: a step or two to the side, a push forward, a few
// paces back, a run round the flank, a dash to the nearest crate it can shoot
// over. Every spot it moves to is one it could stand and shoot from — on the
// navigation grid, a short walk away, clear of its comrades' spots, and with a
// line of sight from its standing eye to the target — so a move is never a
// walk into a wall or out of the fight.
//
// Which move is decided by distance first and temperament second. Well inside
// FightRange it backs off; well outside it closes. In between it rolls against
// Aggression: an aggressive agent advances and flanks, a cautious one takes
// cover, and either strafes. Only one agent at a time flanks any one target,
// so a squad pins and one of it goes round.
//
// A target it has lost sight of is searched for rather than stared at: it
// moves to somewhere near where it was last seen that has a view of that spot,
// which is round the side of whatever the target ducked behind.
//
// Diving aside is the one move not chosen on its clock. When the player draws
// a bead on it — the camera's aim passing close by its body — it may dive
// (DodgeChance, a chance per second of being aimed at), and a script can make
// it dive from anything (Dodge, `ai_dodge`) — a grenade, a near miss. A dive is
// a short, fast, straight burst across the line of fire, not a path: the
// body is thrown, and collision stops it at a wall.
//
// None of it fires a shot. :target_in_range: goes out to the script while the
// target is in sight and range, whether the agent is standing or strafing, and
// the script decides what that means. What the script also hears is when a
// move starts (:ai_maneuver:), when it stops (:ai_in_position:, and
// :cover_taken: when the spot was cover) and when a dive starts (:ai_dodge:),
// which is what it needs to stand a crouched body up before it walks and pose
// it again when it stops.

import (
	"math"

	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// The moves, as AIState.FightMove holds them.
const (
	fightHold = iota
	fightStrafe
	fightAdvance
	fightRetreat
	fightFlank
	fightCover
	fightSearch
)

const (
	// dodgeDistance is how far a dive carries a body, and dodgeSpeed how fast.
	// dodgeWindup is how long it crouches before it leaves the ground and
	// dodgeRecover how long it stays down after landing — both the quick dive
	// clips' own timing (0.75 s: loaded by 0.12, landed by 0.5), so the feet
	// neither slide at the start nor skate at the end. A type with a longer
	// dive says so in its spec (dodgeTiming); these are what zero means.
	dodgeDistance = float32(3.0)
	dodgeSpeed    = float32(8.0)
	dodgeWindup   = float32(0.12)
	dodgeRecover  = float32(0.25)
	// fightRunBeyond is how long a move has to be, in units, for the agent to
	// run it rather than walk it with its gun up.
	fightRunBeyond = float32(5)
	// fightCrowd is how close two agents' spots may be.
	fightCrowd = float32(2.2)
	// fightSearchDelay is how long a target has to have been out of sight for
	// the agent to go looking rather than wait for it to show again.
	fightSearchDelay = float32(0.8)
	// fightTries is how many spots each kind of move tries before giving up.
	fightTries = 6
	// fightCoverDuck is the share of its height a cover spot hides — a body
	// crouched behind something waist high.
	fightCoverDuck = float32(0.6)
)

// playerAim is where the player is looking from and along, as the app sets it
// each frame (SetPlayerAim) — how a fighter knows it is being aimed at. The
// controller has no camera of its own to read.
var playerAim struct {
	on       bool
	eye, dir mgl32.Vec3
}

// SetPlayerAim tells the controllers where the player is aiming this frame:
// their eye and the unit direction they look.
func SetPlayerAim(eye, dir mgl32.Vec3) {
	if l := dir.Len(); l > 1e-6 {
		playerAim.on, playerAim.eye, playerAim.dir = true, eye, dir.Mul(1/l)
		return
	}
	playerAim.on = false
}

// ClearPlayerAim forgets the player's aim: no fighter dives for it.
func ClearPlayerAim() { playerAim.on = false }

// fightRange is the distance the agent likes to fight from.
func fightRange(spec *world.AISpec) float32 {
	if spec.FightRange > 0 {
		return spec.FightRange
	}
	if spec.AttackRange > 0 {
		return spec.AttackRange * 0.5
	}
	return 10
}

// fightIntent is what a fighter does this tick about a target it has. leader,
// when it has one, is who it must not stray too far from.
func fightIntent(lvl *world.Level, e *world.Entity, st *world.AIState, tgt, leader *world.Entity, dt, arrive float32) intent {
	spec := e.AI
	if st.FightFor != tgt.ID {
		// A new fight: stand and shoot for a moment before the first move, so a
		// squad that has just spotted someone opens fire rather than scattering.
		st.FightFor = tgt.ID
		st.FightMove = fightHold
		st.FightTimer = reposition(spec) * (0.3 + 0.5*rng.Float32())
		st.FightUnseen = 0
	}
	if st.TargetVisible {
		st.FightUnseen = 0
	} else {
		st.FightUnseen += dt
	}
	if st.TargetVisible && st.DodgeCool <= 0 && spec.DodgeChance > 0 && aimedAt(lvl, e, tgt) {
		p := 1 - float32(math.Pow(float64(1-minf(spec.DodgeChance, 0.999)), float64(dt)))
		if rng.Float32() < p && startDodge(lvl, e, st, eyeOf(lvl, tgt), false) {
			in, _ := dodgeIntent(lvl, e, st, 0)
			return in
		}
	}

	if st.FightMove != fightHold {
		st.FightAge += dt
		// A move is given up when it has taken far longer than it should —
		// pushed about by a crowd, or walking a path much longer than the
		// line — or, for any move but a search, when the target has gone out
		// of sight for long enough that the spot is no good for shooting from.
		limit := 2 + 3*distXZ(st.FightFrom, st.FightGoal)/maxf(spec.Speed, 0.5)
		lost := st.FightMove != fightSearch && st.FightMove != fightCover && st.FightMove != fightFlank &&
			st.FightUnseen > 1.5
		if st.FightAge > limit || lost {
			endFightMove(e, st)
		} else {
			return moveIntent(lvl, e, st)
		}
	}

	stand := intent{mode: world.AIWatching, face: true, facePos: st.LastKnown}
	if st.InRange {
		stand.mode = world.AIEngaging
	}
	if spec.AimAnim != "" && hasAnim(e, spec.AimAnim) {
		stand.anim = spec.AimAnim
	}

	st.FightTimer -= dt
	if st.FightTimer > 0 {
		return stand
	}
	// Whatever happens, the next look is a while off: a move that could not be
	// found is not looked for again every frame.
	st.FightTimer = reposition(spec) * (0.6 + 0.8*rng.Float32())

	if !st.TargetVisible {
		if st.FightUnseen < fightSearchDelay {
			st.FightTimer = 0.3
			return stand
		}
		if goal, ok := searchSpot(lvl, e, st, tgt, leader); ok {
			beginFightMove(lvl, e, st, fightSearch, goal)
			return moveIntent(lvl, e, st)
		}
		// Nowhere with a view of it: go to where it was last seen.
		if distXZ(feetOf(lvl, e), st.LastKnown) > maxf(arrive*2, 1.5) && withinLeash(lvl, e, st.LastKnown, leader) {
			beginFightMove(lvl, e, st, fightSearch, st.LastKnown)
			return moveIntent(lvl, e, st)
		}
		return stand
	}

	for _, kind := range chooseMoves(lvl, e, st, tgt) {
		if goal, ok := fightSpot(lvl, e, st, tgt, leader, kind); ok {
			beginFightMove(lvl, e, st, kind, goal)
			return moveIntent(lvl, e, st)
		}
	}
	st.FightTimer = 0.5 + rng.Float32()
	return stand
}

// reposition is the agent's hold time between moves, never zero.
func reposition(spec *world.AISpec) float32 {
	if spec.Reposition > 0 {
		return spec.Reposition
	}
	return world.DefaultAISpec().Reposition
}

// chooseMoves is the moves worth trying now, best first.
func chooseMoves(lvl *world.Level, e *world.Entity, st *world.AIState, tgt *world.Entity) []int {
	spec := e.AI
	d := distXZ(feetOf(lvl, e), st.LastKnown)
	pref := fightRange(spec)
	agg := clampf(spec.Aggression, 0, 1)
	switch {
	case d < pref*0.45:
		return []int{fightRetreat, fightStrafe}
	case d > pref*1.5:
		if rng.Float32() < agg*0.5 && !someoneFlanking(lvl, e, tgt) {
			return []int{fightFlank, fightAdvance, fightStrafe}
		}
		return []int{fightAdvance, fightStrafe}
	}
	r := rng.Float32()
	switch {
	case r < agg*0.35 && !someoneFlanking(lvl, e, tgt):
		return []int{fightFlank, fightStrafe}
	case r < agg*0.35+(1-agg)*0.4:
		return []int{fightCover, fightStrafe}
	case d > pref && r < agg*0.35+(1-agg)*0.4+agg*0.3:
		return []int{fightAdvance, fightStrafe}
	}
	return []int{fightStrafe}
}

// beginFightMove sets a move under way and tells the script.
func beginFightMove(lvl *world.Level, e *world.Entity, st *world.AIState, kind int, goal mgl32.Vec3) {
	feet := feetOf(lvl, e)
	st.FightMove = kind
	st.FightGoal = goal
	st.FightFrom = feet
	st.FightAge = 0
	st.FightRun = kind == fightFlank || kind == fightSearch && distXZ(feet, goal) > 2.5 ||
		distXZ(feet, goal) > fightRunBeyond
	clearPath(st)
	hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_maneuver", Agent: e.ID})
}

// endFightMove is the agent at its spot, or giving up on it: it stands and
// shoots, for longer when the spot was cover.
func endFightMove(e *world.Entity, st *world.AIState) {
	kind := st.FightMove
	if kind == fightHold {
		return
	}
	st.FightMove = fightHold
	st.FightRun = false
	hold := reposition(e.AI) * (0.6 + 0.8*rng.Float32())
	if kind == fightCover {
		hold *= 1.6
	}
	st.FightTimer = hold
	hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_in_position", Agent: e.ID})
	if kind == fightCover {
		hooks.RecordAIEvent(hooks.AIEvent{Atom: "cover_taken", Agent: e.ID})
	}
}

// moveIntent walks the move under way: gun up and on the target over a short
// one, when the agent has the clips to walk sideways and backwards; a run,
// facing where it is going, over a long one.
func moveIntent(lvl *world.Level, e *world.Entity, st *world.AIState) intent {
	spec := e.AI
	mode := world.AIManeuvering
	switch st.FightMove {
	case fightFlank:
		mode = world.AIFlanking
	case fightSearch:
		mode = world.AISearching
	}
	in := intent{mode: mode, move: true, goal: st.FightGoal, arrive: maxf(spec.ArriveRadius, 0.4)}
	if st.FightRun {
		in.speedScale, in.moveAnim = runLeg(spec)
		return in
	}
	if canStrafe(e) {
		in.face, in.facePos, in.faceWhileMoving = true, st.LastKnown, true
	}
	return in
}

// fightSpot finds a spot for one kind of move, or reports there is none.
func fightSpot(lvl *world.Level, e *world.Entity, st *world.AIState, tgt, leader *world.Entity, kind int) (mgl32.Vec3, bool) {
	spec := e.AI
	feet := feetOf(lvl, e)
	tf := st.LastKnown
	toT := mgl32.Vec3{tf.X() - feet.X(), 0, tf.Z() - feet.Z()}
	d := toT.Len()
	if d < 1e-3 {
		toT, d = mgl32.Vec3{0, 0, -1}, 0
	} else {
		toT = toT.Mul(1 / d)
	}
	side := mgl32.Vec3{-toT.Z(), 0, toT.X()}
	pref := fightRange(spec)

	if kind == fightCover {
		p, ok := FindCoverWith(lvl, e, feet, 9, nil, eyeOf(lvl, tgt), tgt.ID, CoverOpts{Duck: fightCoverDuck, Peek: true})
		if !ok || distXZ(p, feet) < 1 || crowded(lvl, e, p) || !withinLeash(lvl, e, p, leader) {
			return mgl32.Vec3{}, false
		}
		return p, true
	}

	sign := float32(1)
	if rng.Float32() < 0.5 {
		sign = -1
	}
	if kind == fightFlank {
		sign = flankSide(lvl, e, tgt, feet, tf)
	}
	for i := 0; i < fightTries; i++ {
		var p mgl32.Vec3
		switch kind {
		case fightStrafe:
			s := sign
			if i%2 == 1 {
				s = -sign
			}
			p = feet.Add(side.Mul(s * (2 + 3.5*rng.Float32()))).Add(toT.Mul((rng.Float32() - 0.5) * 2))
		case fightAdvance:
			step := clampf(d-pref*0.8, 3, 6+4*rng.Float32())
			p = feet.Add(toT.Mul(step)).Add(side.Mul((rng.Float32() - 0.5) * 5))
		case fightRetreat:
			p = feet.Sub(toT.Mul(3 + 3*rng.Float32())).Add(side.Mul((rng.Float32() - 0.5) * 4))
		case fightFlank:
			// Round the target, a right angle or so off the line the agent
			// is on now, at about the range it likes.
			b := math.Atan2(float64(feet.X()-tf.X()), float64(feet.Z()-tf.Z()))
			turn := float64(sign) * (70 + 40*float64(rng.Float32())) * math.Pi / 180
			if i >= fightTries/2 {
				turn *= 0.6
			}
			r := clampf(pref, 6, maxf(d, 6))
			p = mgl32.Vec3{tf.X() + r*float32(math.Sin(b+turn)), feet.Y(), tf.Z() + r*float32(math.Cos(b+turn))}
		}
		if q, ok := acceptSpot(lvl, e, tgt, leader, feet, p, kind != fightFlank || i < fightTries/2); ok {
			return q, true
		}
	}
	return mgl32.Vec3{}, false
}

// searchSpot is somewhere near where the target was last seen with a view of
// that spot, nearest the agent first.
func searchSpot(lvl *world.Level, e *world.Entity, st *world.AIState, tgt, leader *world.Entity) (mgl32.Vec3, bool) {
	grid := gridFor(lvl, e)
	if grid == nil || len(grid.Nodes) == 0 {
		return mgl32.Vec3{}, false
	}
	feet := feetOf(lvl, e)
	lk := st.LastKnown
	r := clampf(fightRange(e.AI)*0.6, 5, 14)
	lo, hi := lk.Sub(mgl32.Vec3{r, 4, r}), lk.Add(mgl32.Vec3{r, 4, r})
	eye := ProfileFor(lvl, e).Height * 0.9
	look := lk.Add(mgl32.Vec3{0, 1, 0})
	best, bestD, found := mgl32.Vec3{}, float32(math.MaxFloat32), false
	for _, p := range grid.SamplePoints(lo, hi, grid.RegionAt(feet), func(p mgl32.Vec3) bool {
		return distXZ(p, lk) <= r && distXZ(p, lk) >= 2
	}, 14, rng) {
		dd := distXZ(p, feet)
		if dd >= bestD || dd < 1.5 || crowded(lvl, e, p) || !withinLeash(lvl, e, p, leader) {
			continue
		}
		if !LineOfSight(lvl, p.Add(mgl32.Vec3{0, eye, 0}), look, e.ID, tgt.ID) {
			continue
		}
		best, bestD, found = p, dd, true
	}
	return best, found
}

// acceptSpot puts a candidate spot on the grid and reports whether the agent
// could fight from it: walkable, a short walk from where it stands, clear of
// its comrades' spots, within its leader's reach and — when sight is asked for
// — with the target in view from its standing eye there.
func acceptSpot(lvl *world.Level, e, tgt, leader *world.Entity, feet, p mgl32.Vec3, sight bool) (mgl32.Vec3, bool) {
	grid := gridFor(lvl, e)
	if grid != nil && len(grid.Nodes) > 0 {
		fn, ok := grid.Locate(feet, 1)
		if !ok {
			return mgl32.Vec3{}, false
		}
		n, ok := grid.Locate(p, 0.8)
		if !ok || grid.Nodes[n].Region != grid.Nodes[fn].Region {
			return mgl32.Vec3{}, false
		}
		p = mgl32.Vec3{p.X(), grid.Nodes[n].Floor, p.Z()}
		if absf(p.Y()-feet.Y()) > 4 {
			return mgl32.Vec3{}, false
		}
		if !grid.WalkableLine(fn, n) && !shortWalk(grid, feet, p) {
			return mgl32.Vec3{}, false
		}
	}
	if distXZ(p, feet) < 1 || crowded(lvl, e, p) || !withinLeash(lvl, e, p, leader) {
		return mgl32.Vec3{}, false
	}
	if sight {
		eye := p.Add(mgl32.Vec3{0, ProfileFor(lvl, e).Height * 0.9, 0})
		if !LineOfSight(lvl, eye, chestOf(lvl, tgt), e.ID, tgt.ID) {
			return mgl32.Vec3{}, false
		}
	}
	return p, true
}

// withinLeash reports whether a spot keeps a follower close enough to its
// leader: well inside the leash, so a fight never walks it into regrouping.
func withinLeash(lvl *world.Level, e *world.Entity, p mgl32.Vec3, leader *world.Entity) bool {
	if leader == nil || e.AI.FollowLeash <= 0 {
		return true
	}
	return distXZ(p, feetOf(lvl, leader)) <= e.AI.FollowLeash*0.7
}

// crowded reports whether another agent stands at, or is making for, a spot
// too close to p.
func crowded(lvl *world.Level, e *world.Entity, p mgl32.Vec3) bool {
	for i := range lvl.Entities {
		o := &lvl.Entities[i]
		if o == e || o.AI == nil || !o.AI.Enabled {
			continue
		}
		if distXZ(feetOf(lvl, o), p) < fightCrowd {
			return true
		}
		if s := o.AIState; s != nil && s.Session == session && s.FightMove != fightHold && distXZ(s.FightGoal, p) < fightCrowd {
			return true
		}
	}
	return false
}

// someoneFlanking reports whether another agent is already working round the
// side of this target.
func someoneFlanking(lvl *world.Level, e, tgt *world.Entity) bool {
	for i := range lvl.Entities {
		o := &lvl.Entities[i]
		if o == e || o.AIState == nil || o.AIState.Session != session {
			continue
		}
		if o.AIState.FightMove == fightFlank && o.AIState.Target == tgt.ID {
			return true
		}
	}
	return false
}

// flankSide is which way round the target to go, +1 or -1: away from the side
// the agent's comrades fighting the same target are on, so the flank opens a
// new angle rather than joining the one it has.
func flankSide(lvl *world.Level, e, tgt *world.Entity, feet, tf mgl32.Vec3) float32 {
	toMe := mgl32.Vec3{feet.X() - tf.X(), 0, feet.Z() - tf.Z()}
	right := mgl32.Vec3{toMe.Z(), 0, -toMe.X()}
	lean := float32(0)
	for i := range lvl.Entities {
		o := &lvl.Entities[i]
		if o == e || o.AIState == nil || o.AIState.Session != session || o.AIState.Target != tgt.ID {
			continue
		}
		lean += feetOf(lvl, o).Sub(tf).Dot(right)
	}
	switch {
	case lean > 0.5:
		return -1
	case lean < -0.5:
		return 1
	case rng.Float32() < 0.5:
		return -1
	}
	return 1
}

// ---------- diving ----------

// aimedAt reports whether the player, when tgt is the player, is aiming close
// enough to the agent's body for it to notice.
func aimedAt(lvl *world.Level, e, tgt *world.Entity) bool {
	if !playerAim.on || tgt != world.PlayerEntity {
		return false
	}
	to := chestOf(lvl, e).Sub(playerAim.eye)
	along := to.Dot(playerAim.dir)
	if along < 2 || along > 70 {
		return false
	}
	off := to.Sub(playerAim.dir.Mul(along)).Len()
	return off < ProfileFor(lvl, e).Radius*2+0.35+along*0.012
}

// startDodge throws the agent aside from threat — across its line, or, with
// away, straight off from it — onto ground it can land on. Reports whether
// there was anywhere to go.
func startDodge(lvl *world.Level, e *world.Entity, st *world.AIState, threat mgl32.Vec3, away bool) bool {
	feet := feetOf(lvl, e)
	off := mgl32.Vec3{feet.X() - threat.X(), 0, feet.Z() - threat.Z()}
	if off.Len() < 1e-3 {
		off = mgl32.Vec3{1, 0, 0}
	}
	off = off.Normalize()
	side := mgl32.Vec3{-off.Z(), 0, off.X()}
	if rng.Float32() < 0.5 {
		side = side.Mul(-1)
	}
	var dirs []mgl32.Vec3
	if away {
		dirs = []mgl32.Vec3{off, off.Add(side).Normalize(), off.Sub(side).Normalize(), side, side.Mul(-1)}
	} else {
		dirs = []mgl32.Vec3{side, side.Mul(-1), side.Add(off.Mul(0.5)).Normalize(), side.Mul(-1).Add(off.Mul(0.5)).Normalize()}
	}
	grid := gridFor(lvl, e)
	full, windup, recover := dodgeTiming(e.AI)
	for _, dir := range dirs {
		for _, dist := range [2]float32{full, full * 0.6} {
			goal := feet.Add(dir.Mul(dist))
			if grid != nil && len(grid.Nodes) > 0 {
				fn, ok := grid.Locate(feet, 1)
				if !ok {
					return false
				}
				n, ok := grid.Locate(goal, 0.5)
				if !ok || absf(grid.Nodes[n].Floor-feet.Y()) > 1 || !grid.WalkableLine(fn, n) {
					continue
				}
				goal[1] = grid.Nodes[n].Floor
			}
			fwd := forwardXZ(lvl, e)
			left := mgl32.Vec2{fwd.Y(), -fwd.X()}
			st.DodgeLeft = left.Dot(mgl32.Vec2{dir.X(), dir.Z()}) > 0
			st.DodgeGoal = goal
			st.DodgeTime = windup + dist/dodgeSpeed + recover
			st.DodgeAge = 0
			st.DodgeCool = e.AI.DodgeCooldown
			st.FightMove, st.FightRun = fightHold, false
			clearPath(st)
			hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_dodge", Agent: e.ID})
			return true
		}
	}
	return false
}

// dodgeIntent carries a dive under way through one tick, and counts down the
// wait before the next. ok is false with no dive under way. Ahead of anything
// else the agent decides, a script's command included: a dive is over in a
// moment, and whatever it interrupted carries on after it.
func dodgeIntent(lvl *world.Level, e *world.Entity, st *world.AIState, dt float32) (intent, bool) {
	if st.DodgeCool > 0 {
		st.DodgeCool -= dt
	}
	if st.DodgeTime <= 0 {
		return intent{}, false
	}
	st.DodgeTime -= dt
	st.DodgeAge += dt
	if st.DodgeTime <= 0 {
		st.DodgeTime = 0
		st.FightTimer = reposition(e.AI) * (0.2 + 0.3*rng.Float32())
		hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_in_position", Agent: e.ID})
		return intent{}, false
	}
	in := intent{mode: world.AIDodging, anim: dodgeAnim(e, st.DodgeLeft)}
	_, windup, recover := dodgeTiming(e.AI)
	down := st.DodgeTime <= recover
	// Down on the ground it does not swivel to keep its gun on the target: a
	// body lying in the dirt turning on the spot is a turntable, not a soldier.
	if st.Target != "" && !(down && recover > dodgeRecover) {
		in.face, in.facePos = true, st.LastKnown
	}
	if st.DodgeAge >= windup && !down && distXZ(feetOf(lvl, e), st.DodgeGoal) > 0.2 {
		in.dodge, in.goal = true, st.DodgeGoal
	}
	return in, true
}

// dodgeTiming is a spec's dive: how far, how long gathering itself, and how
// long down once it lands — each the engine's quick dive where the spec says 0.
func dodgeTiming(spec *world.AISpec) (dist, windup, recover float32) {
	dist, windup, recover = dodgeDistance, dodgeWindup, dodgeRecover
	if spec == nil {
		return
	}
	if spec.DodgeDistance > 0 {
		dist = spec.DodgeDistance
	}
	if spec.DodgeWindup > 0 {
		windup = spec.DodgeWindup
	}
	if spec.DodgeRecover > 0 {
		recover = spec.DodgeRecover
	}
	return
}

// dash drives the body straight at goal at dive speed, no easing up to it:
// a dive is a body thrown, not a walk. It slows only over the last stretch.
func dash(lvl *world.Level, e *world.Entity, goal mgl32.Vec3) {
	var frame *world.Frame
	if e.Frame != "" {
		frame = lvl.Frames.FrameFor(e)
	}
	feet := feetOf(lvl, e)
	dir := mgl32.Vec3{goal.X() - feet.X(), 0, goal.Z() - feet.Z()}
	dist := dir.Len()
	vel := frame.DirToWorld(e.Velocity)
	if dist < 1e-3 {
		vel[0], vel[2] = 0, 0
	} else {
		v := dir.Mul(minf(dodgeSpeed, dist*10) / dist)
		vel[0], vel[2] = v.X(), v.Z()
	}
	e.Velocity = frame.DirFromWorld(vel)
}

// dodgeAnim is the dive clip for a side; failing that the aim, so a type
// with no dives at least throws itself aside with its gun up.
func dodgeAnim(e *world.Entity, left bool) string {
	name := e.AI.DodgeRightAnim
	if left {
		name = e.AI.DodgeLeftAnim
	}
	if name != "" && hasAnim(e, name) {
		return name
	}
	return e.AI.AimAnim
}

// Dodge makes an agent dive aside from a point now, across the line from it or,
// with away, straight off from it — what a script's ai_dodge does. It needs no
// fight to be on: a grenade is dived from by anyone. Reports whether the agent
// dove, which it does not when it is already diving, is being moved by
// something else, or has nowhere to land.
func Dodge(lvl *world.Level, e *world.Entity, from mgl32.Vec3, away bool) bool {
	if lvl == nil || e == nil || e.AI == nil || !e.AI.Enabled || !canSteer(e) {
		return false
	}
	st := state(lvl, e)
	if st.DodgeTime > 0 {
		return false
	}
	return startDodge(lvl, e, st, from, away)
}

// ---------- walking sideways ----------

// hasAnim reports whether any of the entity's attachments has an animation by
// that name.
func hasAnim(e *world.Entity, name string) bool {
	for i := range e.Models {
		if e.Models[i].Animation(name) != nil {
			return true
		}
	}
	return false
}

// canStrafe reports whether the agent has the clips to walk sideways and
// backwards with its gun on a target.
func canStrafe(e *world.Entity) bool {
	s := e.AI
	return s.StrafeLeftAnim != "" && s.StrafeRightAnim != "" && s.BackAnim != "" &&
		hasAnim(e, s.StrafeLeftAnim) && hasAnim(e, s.StrafeRightAnim) && hasAnim(e, s.BackAnim)
}

// strafeAnim is the clip for walking the way the body is moving relative to
// the way it faces: its walk forward, a strafe to either side, its backpedal
// backward. cur is the clip playing now, kept until the heading is well into
// another's quarter so a heading on a boundary does not flicker between two.
func strafeAnim(lvl *world.Level, e *world.Entity, walk, cur string) string {
	s := e.AI
	var frame *world.Frame
	if e.Frame != "" {
		frame = lvl.Frames.FrameFor(e)
	}
	v := frame.DirToWorld(e.Velocity)
	flat := mgl32.Vec2{v.X(), v.Z()}
	if flat.Len() < 0.3 {
		return cur
	}
	flat = flat.Normalize()
	fwd := forwardXZ(lvl, e)
	left := mgl32.Vec2{fwd.Y(), -fwd.X()}
	f, l := flat.Dot(fwd), flat.Dot(left)
	pick := walk
	switch {
	case f < -0.707:
		pick = s.BackAnim
	case l > 0.707:
		pick = s.StrafeLeftAnim
	case l < -0.707:
		pick = s.StrafeRightAnim
	}
	// Hysteresis: within about ten degrees of the boundary, keep what plays.
	if cur != "" && cur != pick {
		const band = 0.17
		switch cur {
		case walk:
			if f > 0.707-band {
				return cur
			}
		case s.BackAnim:
			if f < -0.707+band {
				return cur
			}
		case s.StrafeLeftAnim:
			if l > 0.707-band {
				return cur
			}
		case s.StrafeRightAnim:
			if l < -0.707+band {
				return cur
			}
		}
	}
	return pick
}
