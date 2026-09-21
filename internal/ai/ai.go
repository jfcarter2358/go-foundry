package ai

// ai.go — one tick of every AI controller in a level.
//
// Each agent, each tick, goes through the same three steps:
//
//  1. Notice. A few times a second (not every frame — a look is a ray cast
//     through the level) it checks what on its Targets list it can see, keeps
//     the target it has while it can, and gives one up after LoseTime out of
//     sight.
//  2. Decide. A script's command beats a target beats an objective beats
//     wandering, and whichever wins produces one intent: go to this point, or
//     stand still facing that way.
//  3. Act. An intent to go somewhere is a path on the level's navigation grid,
//     followed corner by corner by writing the entity's Velocity.
//
// Acting stops at Velocity on purpose. Everything after that — gravity, being
// stopped by a wall, pushed by a crate, lifted up a stair — is EntityUpdate's,
// which runs straight after this, so an agent is a body like any other body and
// nothing here has to know what a wall is.
//
// What the agent did that a script cares about goes out through the hooks
// buffer (hooks.RecordAIEvent) and arrives as a message in the same frame's
// script tick.

import (
	"math"
	"math/rand"

	"foundry/internal/hooks"
	"foundry/internal/nav"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// Tuning that is the controller's rather than the agent's: how often it looks,
// how often it may re-plan, and how long no headway counts as stuck.
const (
	perceiveInterval = float32(0.2)
	repathInterval   = float32(0.5)
	stuckTime        = float32(1.0)
	maxStuckRepaths  = 3
	failHold         = float32(1.0)
)

// session numbers a run of the controllers. A state stamped with an older one
// is from a play session that has ended, and is started afresh rather than
// resumed — see ResetSession.
var session uint64 = 1

// rng is the controllers' dice: where to wander, which objective at random,
// which spot in a volume to stand on. Seeded, so a test is repeatable.
var rng = rand.New(rand.NewSource(1))

// AsyncNav makes the controllers build navigation off the frame loop's thread:
// a level whose shape changes keeps its old grids until new ones are ready, and
// an agent with no grid at all yet holds still. Set by the app for play; left
// off, every request builds on the spot, which is what tests and tools expect.
// See nav.SyncAsync.
var AsyncNav bool

// gridFor is the navigation an agent's controller plans on this tick — nil only
// under AsyncNav, when none has been built for it yet.
//
// An agent aboard a frame gets nil, too, while the grid is stale: a ship that
// has just set down is in the level's shape but not yet in any grid, and a path
// planned on the old one would not know its hold is there.
func gridFor(lvl *world.Level, e *world.Entity) *nav.Grid {
	if AsyncNav {
		g, fresh := nav.ForReady(lvl, ProfileFor(lvl, e))
		if !fresh && e != nil && e.Frame != "" {
			return nil
		}
		return g
	}
	return nav.For(lvl, ProfileFor(lvl, e))
}

// ResetSession forgets every agent's runtime state and every built grid. Called
// wherever the script events are reset — a level load, entering and leaving
// play — since those are exactly the moments a guard should start its round
// again.
func ResetSession() {
	session++
	rng = rand.New(rand.NewSource(1))
	nav.Invalidate()
}

// CurrentSession is the session a state must carry to be resumed rather than
// started afresh. A save game restamps the agents' states it puts back with it,
// since the load that preceded the restore began a new session.
func CurrentSession() uint64 { return session }

// Update runs every controller in the level for one tick, and reports whether
// any of them wrote to its entity — which the caller needs, to mark a play
// session dirty.
//
// player may be nil; it is only ever something to hunt.
func Update(lvl *world.Level, dt float32, player *world.Entity) bool {
	if lvl == nil || dt <= 0 {
		return false
	}
	any := false
	for i := range lvl.Entities {
		if lvl.Entities[i].AI != nil {
			any = true
			break
		}
	}
	if !any {
		return false
	}
	if AsyncNav {
		nav.SyncAsync(lvl)
	} else {
		nav.Sync(lvl)
	}
	wrote := false
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.AI == nil {
			continue
		}
		if tick(lvl, e, i, dt, player) {
			wrote = true
		}
	}
	return wrote
}

// intent is what deciding produced for one tick.
type intent struct {
	mode world.AIMode
	// move is set when the agent wants to be somewhere else: at goal, within
	// arrive of it.
	move   bool
	goal   mgl32.Vec3
	arrive float32
	// face, when move is not set, is a point to turn toward.
	face    bool
	facePos mgl32.Vec3
	// faceYaw is a heading to turn to instead, when faceYawOn.
	faceYawOn bool
	faceYaw   float32
	// anim overrides the idle and move animations for this tick.
	anim string
	// speedScale multiplies the agent's speed for this move; zero reads as 1.
	speedScale float32
	// faceWhileMoving turns the agent to facePos as it walks, rather than the
	// way it is walking — holding a target in its sights along a path.
	faceWhileMoving bool
	// moveAnim replaces the agent's MoveAnim while it is actually moving —
	// running to catch up — and leaves its idle alone when it is not.
	moveAnim string
}

// stateFor returns an agent's runtime state, starting a fresh one on its first
// tick of a session.
func stateFor(lvl *world.Level, e *world.Entity, index int) *world.AIState {
	if e.AIState == nil || e.AIState.Session != session {
		e.AIState = &world.AIState{
			Session: session,
			Home:    feetOf(lvl, e),
			// Staggered, so a level of forty guards does not cast forty rays in
			// the same frame five times a second.
			PerceiveTimer: perceiveInterval * float32(index%7) / 7,
			BestDist:      float32(math.MaxFloat32),
		}
	}
	return e.AIState
}

// tick runs one agent.
func tick(lvl *world.Level, e *world.Entity, index int, dt float32, player *world.Entity) bool {
	spec := e.AI
	st := stateFor(lvl, e, index)

	if !spec.Enabled {
		if st.Mode == world.AIDisabled {
			return false
		}
		brake(e, spec, math.MaxFloat32)
		clearPath(st)
		st.Mode = world.AIDisabled
		// Only a pose the controller put there goes back to idle. A script
		// that switched the controller off to play something of its own — a
		// death, above all — keeps it; before, a body killed mid-walk was
		// stood back up in its idle the tick after it fell.
		if st.Anim != "" && !animShowing(e, st.Anim) {
			st.Anim = ""
		} else {
			setAnim(e, st, spec.IdleAnim)
		}
		return true
	}
	if st.Mode == world.AIDisabled {
		st.Mode = world.AIIdle
	}

	if st.FailTimer > 0 {
		st.FailTimer -= dt
	}

	perceive(lvl, e, st, dt, player)

	in := decide(lvl, e, st, dt, player)
	st.Mode = in.mode
	if st.RestYawOn {
		switch {
		case in.mode != world.AIIdle || in.move || st.RestFrame != e.Frame:
			st.RestYawOn = false
		case !in.face && !in.faceYawOn:
			in.faceYawOn, in.faceYaw = true, st.RestYaw+frameYaw(lvl, e)
		}
	}

	// Seated, or being moved by a script: the agent still notices and decides,
	// so its events keep coming, but whatever is moving it owns the body.
	if !canSteer(e) {
		return false
	}
	// Aboard something that is moving: it stays on its feet where it stands,
	// still turning to what it faces, and walks on once the thing under it
	// stops. Nothing it was doing is over — a path, a command, keeping up —
	// only paused, since navigation has no floor under it to plan on until
	// then (nav's navigable).
	if in.move && !world.FrameAtRest(lvl, e.Frame) {
		in.move = false
		if in.faceWhileMoving {
			in.face = true
		}
	}

	moving := false
	if in.move {
		res := follow(lvl, e, st, in.goal, in.arrive, in.speedScale, !in.faceWhileMoving, dt)
		moving = res == followMoving
		if in.faceWhileMoving {
			if d := in.facePos.Sub(feetOf(lvl, e)); d.X()*d.X()+d.Z()*d.Z() > 1e-4 {
				turnTo(lvl, e, yawToward(d), spec.TurnRate*dt)
			}
		}
		switch {
		case res == followHolding:
			// Navigation is still being built: nothing has ended, the walk
			// has not started yet.
		case in.mode == world.AIMoving && st.Command && res != followMoving:
			// A command ends however the walk does. One that could not be
			// carried out in full says so, rather than holding the agent at
			// the end of a partial path, outranking everything, for as long as
			// it lives.
			st.Command = false
			switch res {
			case followArrived:
				hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_arrived", Agent: e.ID})
			case followBlocked:
				hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_path_failed", Agent: e.ID})
			}
		case in.mode == world.AIWandering && res != followMoving:
			st.Wandering = false
			st.WanderTimer = spec.WanderPause * (0.5 + rng.Float32())
		case (in.mode == world.AIEscorting || in.mode == world.AIRegrouping) && res != followMoving && res != followHolding:
			// At the slot, or as near it as the level allows: stand until the
			// leader's movement opens the gap again. Behind a leader still on
			// the move it only brakes for the tick, and the slot's next re-pick
			// sets it walking again without waiting for a gap.
			if !leaderMoving(st) {
				st.FollowMoving, st.FollowRunning = false, false
			}
		}
	} else {
		clearPath(st)
		brake(e, spec, dt)
		switch {
		case in.faceYawOn:
			turnTo(lvl, e, in.faceYaw, spec.TurnRate*dt)
		case in.face:
			d := in.facePos.Sub(feetOf(lvl, e))
			if d.X()*d.X()+d.Z()*d.Z() > 1e-4 {
				turnTo(lvl, e, yawToward(d), spec.TurnRate*dt)
			}
		}
	}

	anim := spec.IdleAnim
	if moving {
		anim = spec.MoveAnim
		if in.moveAnim != "" {
			anim = in.moveAnim
		}
	}
	if in.anim != "" {
		anim = in.anim
	}
	setAnim(e, st, anim)
	// A body that is walking has to look like it. A script that posed it —
	// the combat script's aim, or its idle on losing a target — did so
	// without the controller hearing of it, so st.Anim still names the walk
	// and setAnim would leave the body sliding along in a standing pose.
	if moving && anim != "" && animDisplaced(e, anim) {
		e.PlayAnimation("", anim)
	}
	return true
}

// canSteer reports whether the controller may write the entity's motion.
//
// Standing aboard a frame is not in the way: the controller steers in world
// space and carries what it writes into the frame (steer, turnTo). Sitting in
// one of its seats is.
func canSteer(e *world.Entity) bool {
	return e.Seat == "" && !e.IsVehicle() &&
		e.Waypoints == nil && !e.Tweens.Easing(world.TweenMove)
}

// decide picks what the agent does this tick.
func decide(lvl *world.Level, e *world.Entity, st *world.AIState, dt float32, player *world.Entity) intent {
	spec := e.AI
	arrive := spec.ArriveRadius
	if arrive <= 0 {
		arrive = world.DefaultAISpec().ArriveRadius
	}

	leader := leaderOf(lvl, e, player)
	if leader != nil {
		trackLeader(lvl, e, st, leader, dt)
	} else if st.FollowLeader != "" {
		forgetLeader(st)
	}

	if st.Command {
		if distXZ(feetOf(lvl, e), st.CommandGoal) <= arrive && absf(feetOf(lvl, e).Y()-st.CommandGoal.Y()) < 2 {
			st.Command = false
			hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_arrived", Agent: e.ID})
		} else {
			return intent{mode: world.AIMoving, move: true, goal: st.CommandGoal, arrive: arrive}
		}
	}

	// A path the agent is walking may say how to treat a target on this leg,
	// and outranks the agent's own On Target while it does.
	route := currentRoute(lvl, e, st)
	mode := spec.OnTarget
	holdRoute := false
	if route.anim != nil {
		switch route.ai().EffectiveEngage() {
		case world.AIEngageChase:
			mode = world.AIChase
		case world.AIEngageHold:
			holdRoute = true
		case world.AIEngageIgnore:
			mode = world.AIIgnore
		}
	}

	tgt := targetEntity(lvl, st.Target, player)
	if tgt != nil && holdRoute {
		// Keep to the path, and fight from it.
		engageEvents(lvl, e, st, tgt, dt)
		in := routeIntent(lvl, e, st, route, tgt, dt, arrive)
		if st.TargetVisible && !st.InCover {
			in.face, in.facePos = true, st.LastKnown
			in.faceYawOn = false
			if in.move {
				in.faceWhileMoving = true
			}
		}
		return in
	}
	if tgt != nil && mode != world.AIIgnore {
		// A target interrupts an objective. Performing starts over once it is
		// dealt with, rather than finishing a half-done console. A path is
		// picked up again at the stop the agent was walking to.
		st.Performing = false
		st.Wandering = false
		st.RouteArrived, st.RoutePicked = false, false
		st.HasCover, st.InCover = false, false
		engageEvents(lvl, e, st, tgt, dt)
		if leader != nil && route.anim == nil {
			if in, ok := regroupIntent(lvl, e, st, leader, tgt, dt, arrive); ok {
				return in
			}
		}
		return engage(lvl, e, st, tgt, mode, arrive)
	}
	if st.InRange {
		st.InRange = false
	}
	st.FollowRegroup = false

	if route.anim != nil {
		return routeIntent(lvl, e, st, route, nil, dt, arrive)
	}
	if leader != nil {
		return followIntent(lvl, e, st, leader, arrive)
	}
	if in, ok := objectiveIntent(lvl, e, st, dt, arrive); ok {
		return in
	}
	return wanderIntent(lvl, e, st, dt, arrive)
}

// engageEvents posts :target_in_range: as a target comes within attack range in
// sight, and again every attack interval while it stays there.
func engageEvents(lvl *world.Level, e *world.Entity, st *world.AIState, tgt *world.Entity, dt float32) {
	spec := e.AI
	dist := feetOf(lvl, tgt).Sub(feetOf(lvl, e)).Len()
	if !st.TargetVisible || dist > spec.AttackRange {
		st.InRange = false
		return
	}
	if !st.InRange {
		st.InRange = true
		st.AttackTimer = spec.AttackInterval
		hooks.RecordAIEvent(hooks.AIEvent{Atom: "target_in_range", Agent: e.ID, Target: tgt.ID})
		return
	}
	if st.AttackTimer -= dt; st.AttackTimer <= 0 {
		hooks.RecordAIEvent(hooks.AIEvent{Atom: "target_in_range", Agent: e.ID, Target: tgt.ID})
		interval := spec.AttackInterval
		if interval < 0.05 {
			interval = 0.05
		}
		st.AttackTimer += interval
		if st.AttackTimer <= 0 {
			st.AttackTimer = interval
		}
	}
}

// engage is what an agent does about a target it has, chasing or watching it.
func engage(lvl *world.Level, e *world.Entity, st *world.AIState, tgt *world.Entity, mode world.AITargetMode, arrive float32) intent {
	spec := e.AI
	feet := feetOf(lvl, e)
	tfeet := feetOf(lvl, tgt)

	if mode == world.AIWatch {
		m := world.AIWatching
		if st.InRange {
			m = world.AIEngaging
		}
		return intent{mode: m, face: true, facePos: st.LastKnown}
	}
	if st.InRange {
		return intent{mode: world.AIEngaging, face: true, facePos: tfeet}
	}
	// Close the distance to where it was last seen, stopping short by the
	// attack range when it is still in sight so the agent does not walk into
	// the thing it is fighting.
	stop := arrive
	if st.TargetVisible && spec.AttackRange*0.8 > stop {
		stop = spec.AttackRange * 0.8
	}
	if distXZ(feet, st.LastKnown) <= stop {
		return intent{mode: world.AIChasing, face: true, facePos: st.LastKnown}
	}
	return intent{mode: world.AIChasing, move: true, goal: st.LastKnown, arrive: stop}
}

// wanderIntent strolls about the agent's home, or stands still.
func wanderIntent(lvl *world.Level, e *world.Entity, st *world.AIState, dt, arrive float32) intent {
	spec := e.AI
	if spec.WanderRadius <= 0 {
		st.Wandering = false
		return intent{mode: world.AIIdle}
	}
	feet := feetOf(lvl, e)
	if st.Wandering {
		if distXZ(feet, st.WanderGoal) <= arrive {
			st.Wandering = false
			st.WanderTimer = spec.WanderPause * (0.5 + rng.Float32())
			return intent{mode: world.AIIdle}
		}
		return intent{mode: world.AIWandering, move: true, goal: st.WanderGoal, arrive: arrive}
	}
	if st.WanderTimer -= dt; st.WanderTimer > 0 {
		return intent{mode: world.AIIdle}
	}
	st.WanderTimer = spec.WanderPause * (0.5 + rng.Float32())
	grid := gridFor(lvl, e)
	if grid == nil || len(grid.Nodes) == 0 {
		return intent{mode: world.AIIdle}
	}
	r := spec.WanderRadius
	home := st.Home
	lo := home.Sub(mgl32.Vec3{r, r, r})
	hi := home.Add(mgl32.Vec3{r, r, r})
	region := grid.RegionAt(feet)
	goal, ok := grid.RandomPoint(lo, hi, region, func(p mgl32.Vec3) bool {
		return distXZ(p, home) <= r
	}, rng)
	if !ok {
		return intent{mode: world.AIIdle}
	}
	st.Wandering = true
	st.WanderGoal = goal
	return intent{mode: world.AIWandering, move: true, goal: goal, arrive: arrive}
}

// setAnim starts an animation when the one the controller wants changes.
//
// Only what the controller itself started is ever stopped: an agent whose
// type has no idle animation keeps whatever its own clips were doing, rather
// than being snapped to its rest pose every time it stops walking.
func setAnim(e *world.Entity, st *world.AIState, name string) {
	if name == st.Anim {
		return
	}
	if name == "" {
		e.StopAnimations()
		st.Anim = ""
		return
	}
	e.PlayAnimation("", name)
	st.Anim = name
}

// animDisplaced reports whether something other than the controller has
// replaced the loop it chose — on the first attachment that has that
// animation, which is the one the controller's choice drives.
//
// A one-shot still running is not displaced: a flinch, a shot or a step up
// while walking plays out, and the walk comes back when it ends.
func animDisplaced(e *world.Entity, name string) bool {
	for i := range e.Models {
		m := &e.Models[i]
		want := m.Animation(name)
		if m.Anim == nil || want == nil {
			continue
		}
		if m.Anim.Playing && !m.Anim.Loop {
			return false
		}
		playing := m.Playing()
		if playing == "" {
			return true
		}
		// Compared by clip, since two names may share one.
		return m.Animation(playing).EffectiveClip() != want.EffectiveClip()
	}
	return false
}

// animShowing reports whether the named animation is the pose the first
// attachment that has it is showing — still playing, or a one-shot held on its
// last frame, which Playing no longer reports.
func animShowing(e *world.Entity, name string) bool {
	for i := range e.Models {
		m := &e.Models[i]
		want := m.Animation(name)
		if m.Anim == nil || want == nil {
			continue
		}
		a := m.Anim
		if a.Doc == nil || a.Clip < 0 || a.Clip >= len(a.Doc.Animations) {
			return false
		}
		return a.Doc.Animations[a.Clip].Name == want.EffectiveClip()
	}
	return false
}

// ---------- where a body is ----------

// feetOf is the world point an entity stands on: the bottom of its bounds,
// which is where collision rests it on a floor, and so the height the grid's
// floors are measured at.
func feetOf(lvl *world.Level, e *world.Entity) mgl32.Vec3 {
	if !e.HasBounds() {
		return lvl.Frames.WorldPosition(e)
	}
	c := lvl.Frames.WorldBoundCenter(e)
	c[1] -= e.BoundHalfExtents().Y()
	return c
}

// eyeOf is where an entity looks from: most of the way up its bounds.
// EyeOf is eyeOf for callers outside the controller — a script asking where an
// entity looks from gets the same answer an agent's own sight uses.
func EyeOf(lvl *world.Level, e *world.Entity) mgl32.Vec3 { return eyeOf(lvl, e) }

func eyeOf(lvl *world.Level, e *world.Entity) mgl32.Vec3 {
	c := lvl.Frames.WorldBoundCenter(e)
	if e.HasBounds() {
		c[1] += e.BoundHalfExtents().Y() * 0.8
	}
	return c
}

// chestOf is where an entity is looked at: above the middle of its bounds, so
// a waist-high wall hides a body's legs but not the body.
func chestOf(lvl *world.Level, e *world.Entity) mgl32.Vec3 {
	c := lvl.Frames.WorldBoundCenter(e)
	if e.HasBounds() {
		c[1] += e.BoundHalfExtents().Y() * 0.5
	}
	return c
}

// ProfileFor is the navigation profile an entity walks with: its bounds for
// size, its controller's step height, or the level's.
func ProfileFor(lvl *world.Level, e *world.Entity) nav.Params {
	p := nav.Params{Radius: 0.3, Height: 1.8}
	if e != nil && e.HasBounds() {
		h := e.BoundHalfExtents()
		p.Radius = maxf(h.X(), h.Z())
		p.Height = 2 * h.Y()
	}
	if e != nil && e.AI != nil {
		p.StepHeight = e.AI.StepHeight
	}
	if p.StepHeight <= 0 {
		p.StepHeight = world.DefaultStepHeight()
		if lvl != nil {
			p.StepHeight = lvl.Player.EffectiveStepHeight()
		}
	}
	return p
}

// targetable reports whether an entity is still something to hunt: in the
// level to be seen, and — when it runs a controller of its own — with that
// controller switched on. The second is how a combat script says an agent is
// dead, or asleep until its part of the level wakes (`ai_enabled = false`), so
// an agent on the other side stops shooting at a corpse. The player always is.
func targetable(e, player *world.Entity) bool {
	if e == nil {
		return false
	}
	if e == player || e == world.PlayerEntity {
		return true
	}
	return e.Visible && (e.AI == nil || e.AI.Enabled)
}

// targetEntity resolves a target ID, "player" included.
func targetEntity(lvl *world.Level, id string, player *world.Entity) *world.Entity {
	if id == "" {
		return nil
	}
	if id == "player" {
		if player != nil {
			return player
		}
		return world.PlayerEntity
	}
	return world.FindEntity(lvl, id)
}

// ---------- small maths ----------

func distXZ(a, b mgl32.Vec3) float32 {
	dx, dz := a.X()-b.X(), a.Z()-b.Z()
	return float32(math.Sqrt(float64(dx*dx + dz*dz)))
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
