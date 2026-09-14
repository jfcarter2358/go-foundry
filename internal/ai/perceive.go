package ai

// perceive.go — what an agent notices.
//
// A target is noticed by being close enough to hear (HearingRange, any
// direction, through anything) or by being seen: within SightRange, inside the
// field of view, and — when RequireSight is set — with nothing solid on the line
// from the agent's eye to the target's chest.
//
// Two rules keep a target from flickering. The agent keeps the target it has
// for as long as it can still notice it, even if something else on its list is
// now nearer; and a target it has lost sight of is only given up after
// LoseTime, during which the agent goes to where it last saw it. A guard that
// forgot someone the instant they stepped behind a pillar would not be a guard.

import (
	"math"
	"strings"

	"foundry/internal/collision"
	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// perceive updates an agent's target.
func perceive(lvl *world.Level, e *world.Entity, st *world.AIState, dt float32, player *world.Entity) {
	cur := targetEntity(lvl, st.Target, player)
	if st.Target != "" && cur == nil {
		// Destroyed, or a level load took it: gone is lost.
		loseTarget(e, st)
	}
	// Between looks, a target in sight is tracked continuously: the look
	// decides whether it is in sight, not where it is.
	if cur != nil && st.TargetVisible {
		st.LastKnown = feetOf(lvl, cur)
	}

	st.PerceiveTimer -= dt
	if st.PerceiveTimer > 0 {
		return
	}
	st.PerceiveTimer += perceiveInterval
	if st.PerceiveTimer <= 0 {
		st.PerceiveTimer = perceiveInterval
	}

	if cur != nil && Perceives(lvl, e, cur) {
		st.TargetVisible = true
		st.SinceSeen = 0
		st.LastKnown = feetOf(lvl, cur)
		return
	}
	if !st.TargetForced {
		if best := nearestPerceived(lvl, e, player, cur); best != nil {
			if cur != nil {
				loseTarget(e, st)
			}
			st.Target = best.ID
			st.TargetVisible = true
			st.SinceSeen = 0
			st.LastKnown = feetOf(lvl, best)
			hooks.RecordAIEvent(hooks.AIEvent{Atom: "target_acquired", Agent: e.ID, Target: best.ID})
			return
		}
	}
	if cur == nil {
		return
	}
	st.TargetVisible = false
	st.SinceSeen += perceiveInterval
	// A target a script chose is kept however long it is out of sight: the
	// script said to hunt it, and only the script says to stop.
	if !st.TargetForced && st.SinceSeen > e.AI.LoseTime {
		loseTarget(e, st)
	}
}

// loseTarget drops the agent's target and says so.
func loseTarget(e *world.Entity, st *world.AIState) {
	if st.Target == "" {
		return
	}
	hooks.RecordAIEvent(hooks.AIEvent{Atom: "target_lost", Agent: e.ID, Target: st.Target})
	st.Target = ""
	st.TargetForced = false
	st.TargetVisible = false
	st.InRange = false
	st.SinceSeen = 0
	clearPath(st)
}

// nearestPerceived is the closest entity on the agent's Targets list it can
// notice right now, skipping except.
func nearestPerceived(lvl *world.Level, e *world.Entity, player, except *world.Entity) *world.Entity {
	var best *world.Entity
	bestD := float32(math.MaxFloat32)
	eye := eyeOf(lvl, e)
	for _, c := range Candidates(lvl, e, player) {
		if c == except {
			continue
		}
		d := chestOf(lvl, c).Sub(eye).Len()
		if d >= bestD {
			continue
		}
		if Perceives(lvl, e, c) {
			best, bestD = c, d
		}
	}
	return best
}

// Candidates lists every entity an agent's Targets name, once each, never the
// agent itself.
func Candidates(lvl *world.Level, e *world.Entity, player *world.Entity) []*world.Entity {
	if e == nil || e.AI == nil {
		return nil
	}
	if player == nil {
		player = world.PlayerEntity
	}
	var out []*world.Entity
	seen := map[*world.Entity]bool{}
	add := func(c *world.Entity) {
		if c != nil && c != e && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, t := range e.AI.Targets {
		t = strings.TrimSpace(t)
		switch {
		case t == "":
		case t == "player" || t == "@player":
			add(player)
		case strings.HasPrefix(t, "#"):
			for _, c := range world.FindEntitiesByTag(lvl, t[1:]) {
				add(c)
			}
		default:
			add(world.FindEntity(lvl, strings.TrimPrefix(t, "@")))
		}
	}
	return out
}

// Perceives reports whether agent e would notice target t now: by hearing it,
// or by seeing it inside its range and field of view with, when its spec asks,
// nothing solid in between.
func Perceives(lvl *world.Level, e, t *world.Entity) bool {
	if e == nil || t == nil || e.AI == nil {
		return false
	}
	spec := e.AI
	eye := eyeOf(lvl, e)
	to := chestOf(lvl, t)
	d := to.Sub(eye)
	dist := d.Len()
	if dist <= spec.HearingRange {
		return true
	}
	if dist > spec.SightRange {
		return false
	}
	if spec.FOV < 360 {
		flat := mgl32.Vec2{d.X(), d.Z()}
		if flat.Len() > 1e-3 {
			fwd := forwardXZ(e)
			cos := fwd.Dot(flat.Normalize())
			half := float64(spec.FOV) / 2 * math.Pi / 180
			if float64(cos) < math.Cos(half) {
				return false
			}
		}
	}
	if spec.RequireSight && !LineOfSight(lvl, eye, to, e.ID, t.ID) {
		return false
	}
	return true
}

// LineOfSight reports whether nothing in the level's geometry lies between two
// points. ignore names entities the ray passes through — the looker and the
// looked-at — along with every brush each of them carries: a body drawn out of
// brushes looks out from inside them, and would otherwise be blind.
//
// It asks collision.LineBlocked, which stops at the first thing in the way
// rather than finding the nearest — every agent in a level asks this several
// times a second, and "is anything there" is all the question needs.
func LineOfSight(lvl *world.Level, from, to mgl32.Vec3, ignore ...string) bool {
	if to.Sub(from).Len() < 1e-4 {
		return true
	}
	var skip map[string]bool
	for _, id := range ignore {
		e := world.FindEntity(lvl, id)
		if e == nil || len(e.Brushes) == 0 {
			continue
		}
		if skip == nil {
			skip = map[string]bool{}
		}
		for i := range e.Brushes {
			skip[world.EntityBrushID(id, e.Brushes[i].ID)] = true
		}
	}
	return !collision.LineBlocked(lvl, from, to, skip)
}

// forwardXZ is the way an entity faces, flattened: its local -Z, the axis a
// vehicle drives along, turned by its yaw.
func forwardXZ(e *world.Entity) mgl32.Vec2 {
	yaw := float64(e.Rotation.Y())
	return mgl32.Vec2{float32(-math.Sin(yaw)), float32(-math.Cos(yaw))}
}

// yawToward is the yaw that faces an entity's -Z along d.
func yawToward(d mgl32.Vec3) float32 {
	return float32(math.Atan2(float64(-d.X()), float64(-d.Z())))
}

// turnTo turns an entity's yaw toward want by at most maxStep radians, the
// short way round.
func turnTo(e *world.Entity, want, maxStep float32) {
	cur := e.Rotation.Y()
	diff := world.WrapAngle(want - cur)
	if maxStep > 0 {
		if diff > maxStep {
			diff = maxStep
		} else if diff < -maxStep {
			diff = -maxStep
		}
	}
	e.Rotation[1] = world.WrapAngle(cur + diff)
}
