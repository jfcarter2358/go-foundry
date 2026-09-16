package ai

// route.go — walking a waypoint animation, and taking cover along it.
//
// An agent told to follow a waypoint animation walks it one point at a time:
// it finds its way to the point on the navigation grid, does what the point's
// AI block says there (world/waypoint_ai.go), and moves on. A point with no
// block is a corner to pass. A point naming a volume is an area: the agent goes
// to a spot inside it it can reach, the way it goes to an objective.
//
// Arriving at a point posts the same :waypoint: — and the point's own events —
// that a brush sent along the path with play_waypoints gets, and walking off
// the end posts :waypoints_end:, so a script written against the one reads the
// other.
//
// # Taking cover
//
// At a point that says to, the agent looks for somewhere near it where it is
// hidden: a spot it can reach within the point's cover radius (or inside its
// volume) where nothing in the level's geometry lies between the threat's eye
// and either the agent's chest or its head. The threat is its target when it
// has one; with none, it is whatever lies the way the point's arrow faces, so
// an author can say "behind this wall, facing the door" before anyone has come
// through the door. Of the hidden spots, the one closest to the agent — and
// then to the point — wins.
//
// The check that the spot still hides it is repeated twice a second. A threat
// that has walked round the wall has broken the cover: the agent posts
// :cover_lost: and looks again.
//
// A point that takes cover holds the agent there for its Wait and for as long
// after as it still has a target — hiding until the threat is gone is what
// taking cover is for.

import (
	"strings"

	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// coverRecheck is how often the agent checks its cover still hides it.
	coverRecheck = float32(0.5)
	// coverSamples bounds a search: each candidate costs two rays.
	coverSamples = 48
	// coverThreatDistance is how far along a point's arrow an unseen threat is
	// assumed to be.
	coverThreatDistance = float32(15)
)

// routeRef is the stop an agent is walking to or waiting at.
type routeRef struct {
	anim *world.WaypointAnimation
	idx  int
}

func (r routeRef) ai() *world.WaypointAI {
	if r.anim == nil || r.idx < 0 || r.idx >= len(r.anim.Points) {
		return nil
	}
	return r.anim.Points[r.idx].AI
}

// ResolveWaypoints finds the animation an agent's name for one means: a level
// animation's ID, or a path the agent's own type carries.
func ResolveWaypoints(lvl *world.Level, e *world.Entity, name string) *world.WaypointAnimation {
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	if name == "" || lvl == nil {
		return nil
	}
	if a := world.FindWaypointAnimation(lvl, name); a != nil {
		return a
	}
	if e != nil {
		return world.FindWaypointAnimation(lvl, world.EntityWaypointAnimationID(e.ID, name))
	}
	return nil
}

// currentRoute is the stop the agent is on, starting the spec's path on the
// first tick that has one.
func currentRoute(lvl *world.Level, e *world.Entity, st *world.AIState) routeRef {
	if st.RouteDone {
		return routeRef{}
	}
	if st.Route == "" {
		a := ResolveWaypoints(lvl, e, e.AI.Waypoints)
		if a == nil || len(a.Points) == 0 {
			return routeRef{}
		}
		startRoute(st, a, e.AI.WaypointsLoop, false)
	}
	a := world.FindWaypointAnimation(lvl, st.Route)
	if a == nil || len(a.Points) == 0 {
		// Deleted, or emptied, from under the agent: it has nothing to walk.
		endRoute(st)
		return routeRef{}
	}
	if st.RouteStop >= len(a.Points) {
		st.RouteStop = len(a.Points) - 1
	}
	return routeRef{anim: a, idx: st.RouteStop}
}

// startRoute begins walking an animation from its first point.
func startRoute(st *world.AIState, a *world.WaypointAnimation, loop, forced bool) {
	st.Route, st.RouteForced, st.RouteLoop, st.RouteDone = a.ID, forced, loop, false
	st.RouteStop = 0
	resetStop(st)
}

// endRoute stops walking, for good until something starts a path again.
func endRoute(st *world.AIState) {
	st.Route, st.RouteForced, st.RouteDone = "", false, true
	resetStop(st)
}

// resetStop forgets everything about the stop being walked to.
func resetStop(st *world.AIState) {
	st.RoutePicked, st.RouteArrived, st.RouteSignal = false, false, false
	st.RouteWait = 0
	st.HasCover, st.InCover, st.CoverTimer = false, false, 0
	clearPath(st)
}

// routeIntent walks to the current stop, does what it says, and moves on.
// tgt is the target the agent is holding to the path against, or nil.
func routeIntent(lvl *world.Level, e *world.Entity, st *world.AIState, route routeRef, tgt *world.Entity, dt, arrive float32) intent {
	a, idx := route.anim, route.idx
	p := &a.Points[idx]
	b := p.AI
	feet := feetOf(lvl, e)

	var vol *world.Volume
	if b != nil && b.Volume != "" {
		if v := world.FindVolume(lvl, b.Volume); v != nil && v.Enabled && v.HasBounds() {
			vol = v
		}
	}
	if !st.RoutePicked {
		if vol != nil {
			st.RoutePoint = ObjectivePoint(lvl, e, vol)
		} else {
			st.RoutePoint = a.WorldPoint(lvl, idx)
		}
		st.RoutePicked = true
	}
	speed := b.EffectiveSpeedScale()

	if !st.RouteArrived {
		// A point the agent only passes is a corner, reached from a little
		// farther off, so a patrol does not slow to a stop at each one.
		reach := arrive
		if !b.Stops() {
			reach = maxf(arrive, 0.75)
		}
		height := ProfileFor(lvl, e).Height
		arrived := distXZ(feet, st.RoutePoint) <= reach && absf(feet.Y()-st.RoutePoint.Y()) <= height+0.5
		if !arrived && vol != nil && insideVolume(lvl, e, vol, feet) && (!b.Stops() || st.StuckCount > 0) {
			arrived = true
		}
		if !arrived && st.HasPath && st.PathPartial && st.PathIndex >= len(st.Path)-1 &&
			distXZ(feet, st.Path[len(st.Path)-1]) <= arrive {
			// As near as the grid goes. The stop is done from here rather than
			// holding the whole path on a point nobody can reach.
			hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_path_failed", Agent: e.ID})
			arrived = true
		}
		if !arrived {
			return intent{mode: world.AIFollowing, move: true, goal: st.RoutePoint, arrive: reach, speedScale: speed}
		}
		st.RouteArrived = true
		st.RouteSignal = false
		st.RouteWait = 0
		if b != nil {
			st.RouteWait = b.Wait
		}
		hooks.RecordWaypointEvent(hooks.WaypointEvent{
			Kind: hooks.WaypointEntity, Object: e.ID, Animation: a.ID, Index: idx, Events: p.Events,
		})
	}

	if b != nil && b.TakeCover {
		if in, ok := coverIntent(lvl, e, st, route, vol, tgt, dt, arrive, speed); ok {
			return in
		}
	}

	if st.RouteWait > 0 {
		st.RouteWait -= dt
	}
	threatened := b != nil && b.TakeCover && st.Target != ""
	waiting := b != nil && (st.RouteWait > 0 || (b.WaitForSignal && !st.RouteSignal) || threatened)
	if waiting {
		in := intent{mode: world.AIWaiting, anim: b.Animation}
		if st.InCover {
			in.mode = world.AIInCover
		}
		if b.Face {
			fwd := a.Forward(lvl, idx)
			if fwd.X()*fwd.X()+fwd.Z()*fwd.Z() > 1e-6 {
				in.faceYawOn, in.faceYaw = true, yawToward(fwd)
			}
		}
		return in
	}

	advanceRoute(e, st, a)
	return intent{mode: world.AIFollowing}
}

// advanceRoute moves the agent on to the next point, or off the end.
func advanceRoute(e *world.Entity, st *world.AIState, a *world.WaypointAnimation) {
	st.RouteStop++
	resetStop(st)
	if st.RouteStop < len(a.Points) {
		return
	}
	hooks.RecordWaypointEvent(hooks.WaypointEvent{
		Kind: hooks.WaypointEntity, Object: e.ID, Animation: a.ID, Index: len(a.Points) - 1, Ended: true,
	})
	// A single point nobody stops at would be arrived at, and ended, every
	// frame; looping one is not a path.
	if st.RouteLoop && (len(a.Points) > 1 || a.Points[0].AI.Stops()) {
		st.RouteStop = 0
		return
	}
	endRoute(st)
}

// coverIntent keeps the agent in cover at the current stop. ok is false when it
// is already there, or no cover is to be had, and the stop carries on as a wait
// in the open.
func coverIntent(lvl *world.Level, e *world.Entity, st *world.AIState, route routeRef, vol *world.Volume, tgt *world.Entity, dt, arrive, speed float32) (intent, bool) {
	if tgt == nil {
		tgt = targetEntity(lvl, st.Target, nil)
	}
	threat, threatID := threatPoint(lvl, st, route, vol, tgt)

	st.CoverTimer -= dt
	if !st.HasCover || st.CoverTimer <= 0 {
		st.CoverTimer = coverRecheck
		if !st.HasCover || !HiddenFrom(lvl, e, st.CoverPoint, threat, threatID) {
			if st.InCover {
				st.InCover = false
				hooks.RecordAIEvent(hooks.AIEvent{Atom: "cover_lost", Agent: e.ID})
			}
			centre, radius := st.RoutePoint, route.ai().EffectiveCoverRadius()
			st.CoverPoint, st.HasCover = FindCover(lvl, e, centre, radius, vol, threat, threatID)
		}
	}
	if !st.HasCover {
		return intent{}, false
	}
	if distXZ(feetOf(lvl, e), st.CoverPoint) > arrive {
		return intent{mode: world.AITakingCover, move: true, goal: st.CoverPoint, arrive: arrive, speedScale: speed}, true
	}
	if !st.InCover {
		st.InCover = true
		hooks.RecordAIEvent(hooks.AIEvent{Atom: "cover_taken", Agent: e.ID})
	}
	return intent{}, false
}

// threatPoint is the eye cover hides from: the target's, or a point along the
// stop's arrow when there is no target to hide from.
func threatPoint(lvl *world.Level, st *world.AIState, route routeRef, vol *world.Volume, tgt *world.Entity) (mgl32.Vec3, string) {
	if tgt != nil {
		return eyeOf(lvl, tgt), tgt.ID
	}
	origin := st.RoutePoint
	if vol != nil {
		origin = vol.WorldPosition(lvl)
		origin[1] = st.RoutePoint.Y()
	}
	fwd := route.anim.Forward(lvl, route.idx)
	fwd[1] = 0
	if l := fwd.Len(); l > 1e-6 {
		fwd = fwd.Mul(1 / l)
	} else {
		fwd = mgl32.Vec3{1, 0, 0}
	}
	return origin.Add(fwd.Mul(coverThreatDistance)).Add(mgl32.Vec3{0, 1.6, 0}), ""
}

// HiddenFrom reports whether an agent standing at spot would be out of sight of
// an eye at threat: nothing but level geometry between that eye and both the
// agent's chest and its head. threatID names the entity the eye belongs to, if
// any, whose own brushes do not count.
func HiddenFrom(lvl *world.Level, e *world.Entity, spot, threat mgl32.Vec3, threatID string) bool {
	h := ProfileFor(lvl, e).Height
	ids := []string{e.ID}
	if threatID != "" {
		ids = append(ids, threatID)
	}
	for _, up := range [2]float32{0.55, 0.9} {
		if LineOfSight(lvl, threat, spot.Add(mgl32.Vec3{0, h * up, 0}), ids...) {
			return false
		}
	}
	return true
}

// FindCover looks for the best spot near centre — within radius of it, or
// inside vol when one is given — that the agent can reach and that hides it
// from threat. ok is false when there is none.
func FindCover(lvl *world.Level, e *world.Entity, centre mgl32.Vec3, radius float32, vol *world.Volume, threat mgl32.Vec3, threatID string) (mgl32.Vec3, bool) {
	grid := gridFor(lvl, e)
	if grid == nil || len(grid.Nodes) == 0 {
		return mgl32.Vec3{}, false
	}
	feet := feetOf(lvl, e)
	region := grid.RegionAt(feet)

	var lo, hi mgl32.Vec3
	var accept func(mgl32.Vec3) bool
	if vol != nil {
		var ok bool
		if lo, hi, ok = vol.WorldAABB(lvl); !ok {
			return mgl32.Vec3{}, false
		}
		half := ProfileFor(lvl, e).Height / 2
		accept = func(p mgl32.Vec3) bool {
			return vol.ContainsWorldPoint(lvl, p.Add(mgl32.Vec3{0, 0.05, 0})) ||
				vol.ContainsWorldPoint(lvl, p.Add(mgl32.Vec3{0, half, 0}))
		}
	} else {
		r := mgl32.Vec3{radius, radius, radius}
		lo, hi = centre.Sub(r), centre.Add(r)
		accept = func(p mgl32.Vec3) bool { return distXZ(p, centre) <= radius }
	}

	best, bestScore, found := mgl32.Vec3{}, float32(0), false
	for _, p := range grid.SamplePoints(lo, hi, region, accept, coverSamples, rng) {
		score := distXZ(feet, p) + 0.35*distXZ(centre, p)
		if found && score >= bestScore {
			continue
		}
		if !HiddenFrom(lvl, e, p, threat, threatID) {
			continue
		}
		best, bestScore, found = p, score, true
	}
	return best, found
}

// ---------- what a script may do ----------

// FollowWaypoints sends an agent along an animation from its first point,
// ahead of its objectives and of any path its spec names. A nil animation stops
// it following anything. Reports whether the agent has a controller.
func FollowWaypoints(lvl *world.Level, e *world.Entity, a *world.WaypointAnimation, loop bool) bool {
	if lvl == nil || e == nil || e.AI == nil {
		return false
	}
	st := state(lvl, e)
	if a == nil {
		endRoute(st)
		return true
	}
	// A path it was being sent along by play_waypoints owns the body, and
	// would keep it from walking this one.
	world.StopWaypoints(e)
	startRoute(st, a, loop, true)
	return true
}

// ContinueWaypoints releases an agent from the stop it is waiting at — its
// Wait cut short, or the signal it is waiting for given. Reports whether it was
// waiting at one.
func ContinueWaypoints(lvl *world.Level, e *world.Entity) bool {
	if lvl == nil || e == nil || e.AI == nil || e.AIState == nil || e.AIState.Session != session {
		return false
	}
	st := e.AIState
	if st.Route == "" || !st.RouteArrived {
		return false
	}
	st.RouteSignal = true
	st.RouteWait = 0
	return true
}

// Route is the animation an agent is walking and the index of the point it is
// walking to or waiting at, or "" and -1.
func Route(e *world.Entity) (string, int) {
	if e == nil || e.AIState == nil || e.AIState.Session != session || e.AIState.Route == "" {
		return "", -1
	}
	return e.AIState.Route, e.AIState.RouteStop
}

// InCover reports whether an agent is hidden at a cover stop.
func InCover(e *world.Entity) bool {
	return e != nil && e.AIState != nil && e.AIState.Session == session && e.AIState.InCover
}

// Waiting reports whether an agent is waiting at a stop of its path.
func Waiting(e *world.Entity) bool {
	if e == nil || e.AIState == nil || e.AIState.Session != session {
		return false
	}
	m := e.AIState.Mode
	return m == world.AIWaiting || m == world.AIInCover
}
