package ai

// objective.go — walking to a volume and doing what it is for.
//
// An agent's Objectives name volumes; the ones carrying an objective block are
// its work. It takes one — in list order, nearest first, or at random — picks a
// spot inside it that it can actually reach, walks there, and performs it for
// the volume's Duration. Then it is done, the agent and the volume both hear
// about it, and the agent takes the next.
//
// A sequence is indexed over every volume the list names, usable or not, and a
// volume that is switched off is stepped over rather than dropped from the
// list. Dropping it would renumber everything after it, and a patrol whose
// first stop disables itself (Once) would skip its second.

import (
	"math"
	"strings"

	"foundry/internal/hooks"
	"foundry/internal/nav"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// objectiveIntent works the agent's current objective, taking a new one when it
// has none. ok is false when there is nothing to do.
func objectiveIntent(lvl *world.Level, e *world.Entity, st *world.AIState, dt, arrive float32) (intent, bool) {
	vol := world.FindVolume(lvl, st.Objective)
	if st.Objective != "" && (vol == nil || !vol.ObjectiveAcceptsAgent(e)) {
		// Switched off, deleted, or retagged out from under the agent.
		dropObjective(st)
		vol = nil
	}
	if vol == nil {
		vol = pickObjective(lvl, e, st)
		if vol == nil {
			return intent{}, false
		}
	}
	obj := vol.Objective
	feet := feetOf(lvl, e)

	if st.Performing {
		in := intent{mode: world.AIPerforming, anim: obj.Animation}
		if obj.Face {
			m := vol.WorldMatrix(lvl)
			fwd := m.Mul4x1(mgl32.Vec4{0, 0, -1, 0})
			in.faceYawOn = true
			in.faceYaw = yawToward(mgl32.Vec3{fwd.X(), 0, fwd.Z()})
		}
		if st.PerformTimer -= dt; st.PerformTimer <= 0 {
			completeObjective(lvl, e, st, vol)
			return intent{mode: world.AIIdle}, true
		}
		return in, true
	}

	if arrivedAtObjective(lvl, e, st, vol, feet, arrive) {
		st.Performing = true
		st.PerformTimer = obj.Duration
		clearPath(st)
		hooks.RecordAIEvent(hooks.AIEvent{Atom: "objective_reached", Agent: e.ID, Objective: vol.ID, ToObjective: true})
		if obj.Duration <= 0 {
			completeObjective(lvl, e, st, vol)
			return intent{mode: world.AIIdle}, true
		}
		return intent{mode: world.AIPerforming, anim: obj.Animation}, true
	}

	if st.HasPath && st.PathPartial && st.PathIndex >= len(st.Path)-1 &&
		distXZ(feet, st.Path[len(st.Path)-1]) <= arrive {
		// As near as the grid goes, and still not there: this objective cannot
		// be reached from here. Stepped over, so one misplaced volume does not
		// freeze a patrol.
		hooks.RecordAIEvent(hooks.AIEvent{Atom: "ai_path_failed", Agent: e.ID})
		skipObjective(e, st, vol)
		return intent{mode: world.AIIdle}, true
	}
	return intent{mode: world.AITravelling, move: true, goal: st.ObjectivePoint, arrive: arrive}, true
}

// arrivedAtObjective reports whether the agent is at its spot in the volume, or
// inside the volume with its walk over.
func arrivedAtObjective(lvl *world.Level, e *world.Entity, st *world.AIState, vol *world.Volume, feet mgl32.Vec3, arrive float32) bool {
	if distXZ(feet, st.ObjectivePoint) <= arrive && absf(feet.Y()-st.ObjectivePoint.Y()) < 1 {
		return true
	}
	walkOver := !st.HasPath || st.PathIndex >= len(st.Path)-1
	return walkOver && st.FailTimer <= 0 && insideVolume(lvl, e, vol, feet) && st.StuckCount > 0
}

// insideVolume tests the agent against a volume at its feet and at its middle,
// so a region lying flat on the floor and one floating at head height both
// count a body standing in it.
func insideVolume(lvl *world.Level, e *world.Entity, vol *world.Volume, feet mgl32.Vec3) bool {
	if vol.ContainsWorldPoint(lvl, feet.Add(mgl32.Vec3{0, 0.05, 0})) {
		return true
	}
	return vol.ContainsWorldPoint(lvl, lvl.Frames.WorldBoundCenter(e))
}

// pickObjective chooses the agent's next objective and assigns it, or returns
// nil when there is none to take now.
func pickObjective(lvl *world.Level, e *world.Entity, st *world.AIState) *world.Volume {
	spec := e.AI
	if st.ObjectivesFinished || len(spec.Objectives) == 0 {
		return nil
	}
	list := objectiveVolumes(lvl, spec.Objectives)
	if len(list) == 0 {
		return nil
	}

	if spec.Order == world.AIOrderSequence {
		n := len(list)
		for tries := 0; tries <= n; tries++ {
			if st.SeqIndex >= n {
				if !spec.Loop {
					finishObjectives(e, st)
					return nil
				}
				st.SeqIndex = 0
			}
			v := list[st.SeqIndex]
			if !v.ObjectiveAcceptsAgent(e) {
				st.SeqIndex++
				continue
			}
			if !hasRoom(lvl, v, e) {
				return nil // its turn will come
			}
			return assignObjective(lvl, e, st, v)
		}
		return nil
	}

	pending := func() []*world.Volume {
		var out []*world.Volume
		for _, v := range list {
			if v.ObjectiveAcceptsAgent(e) && !contains(st.Done, v.ID) {
				out = append(out, v)
			}
		}
		return out
	}
	open := pending()
	if len(open) == 0 {
		if !spec.Loop || len(st.Done) == 0 {
			if len(st.Done) > 0 {
				finishObjectives(e, st)
			}
			return nil
		}
		st.Done = nil
		if open = pending(); len(open) == 0 {
			return nil
		}
	}
	var free []*world.Volume
	for _, v := range open {
		if hasRoom(lvl, v, e) {
			free = append(free, v)
		}
	}
	if len(free) == 0 {
		return nil
	}
	pick := free[0]
	if spec.Order == world.AIOrderRandom {
		pick = free[rng.Intn(len(free))]
	} else {
		feet := feetOf(lvl, e)
		best := float32(math.MaxFloat32)
		for _, v := range free {
			if d := v.WorldPosition(lvl).Sub(feet).Len(); d < best {
				pick, best = v, d
			}
		}
	}
	return assignObjective(lvl, e, st, pick)
}

// assignObjective makes vol the agent's objective and picks where in it to
// stand.
func assignObjective(lvl *world.Level, e *world.Entity, st *world.AIState, vol *world.Volume) *world.Volume {
	st.Objective = vol.ID
	st.Performing = false
	st.PerformTimer = 0
	st.ObjectivePoint = ObjectivePoint(lvl, e, vol)
	st.StuckCount = 0
	clearPath(st)
	return vol
}

// ObjectivePoint picks a spot inside a volume that an agent can reach: a
// navigation node at random among those inside the region and in the same
// connected area as the agent. Random, so three guards sent to one room spread
// out across it rather than queueing for its exact centre. Falls back to the
// volume's centre when no node qualifies.
func ObjectivePoint(lvl *world.Level, e *world.Entity, vol *world.Volume) mgl32.Vec3 {
	centre := vol.WorldPosition(lvl)
	grid := nav.For(lvl, ProfileFor(lvl, e))
	if grid == nil || len(grid.Nodes) == 0 {
		return centre
	}
	lo, hi, ok := vol.WorldAABB(lvl)
	if !ok {
		return centre
	}
	half := float32(0)
	if e.HasBounds() {
		half = e.BoundHalfExtents().Y()
	}
	region := grid.RegionAt(feetOf(lvl, e))
	p, found := grid.RandomPoint(lo, hi, region, func(p mgl32.Vec3) bool {
		return vol.ContainsWorldPoint(lvl, p.Add(mgl32.Vec3{0, 0.05, 0})) ||
			vol.ContainsWorldPoint(lvl, p.Add(mgl32.Vec3{0, half, 0}))
	}, rng)
	if !found {
		return centre
	}
	return p
}

// completeObjective finishes the agent's objective and tells everyone.
func completeObjective(lvl *world.Level, e *world.Entity, st *world.AIState, vol *world.Volume) {
	if atom := vol.Objective.ActionAtom(); atom != "" {
		hooks.RecordAIEvent(hooks.AIEvent{Atom: atom, Agent: e.ID, Objective: vol.ID, ToObjective: true})
	}
	hooks.RecordAIEvent(hooks.AIEvent{Atom: "objective_complete", Agent: e.ID, Objective: vol.ID, ToObjective: true})
	if vol.Objective.Once {
		vol.Objective.Enabled = false
	}
	skipObjective(e, st, vol)
}

// skipObjective moves the agent past its current objective, done or not.
func skipObjective(e *world.Entity, st *world.AIState, vol *world.Volume) {
	if !st.ObjectiveForced {
		if e.AI.Order == world.AIOrderSequence {
			st.SeqIndex++
		} else {
			st.Done = append(st.Done, vol.ID)
		}
	}
	dropObjective(st)
}

// dropObjective clears the agent's objective without counting it done.
func dropObjective(st *world.AIState) {
	st.Objective = ""
	st.ObjectiveForced = false
	st.Performing = false
	st.PerformTimer = 0
	clearPath(st)
}

// finishObjectives marks an unlooped list complete, once.
func finishObjectives(e *world.Entity, st *world.AIState) {
	if st.ObjectivesFinished {
		return
	}
	st.ObjectivesFinished = true
	hooks.RecordAIEvent(hooks.AIEvent{Atom: "objectives_done", Agent: e.ID})
}

// hasRoom reports whether an objective has capacity for one more agent, not
// counting e itself.
func hasRoom(lvl *world.Level, vol *world.Volume, e *world.Entity) bool {
	limit := vol.Objective.Capacity
	if limit <= 0 {
		return true
	}
	n := 0
	for i := range lvl.Entities {
		o := &lvl.Entities[i]
		if o == e || o.AIState == nil || o.AIState.Session != session {
			continue
		}
		if o.AIState.Objective == vol.ID {
			n++
		}
	}
	return n < limit
}

// objectiveVolumes expands an Objectives list into the volumes it names that
// carry an objective block, usable or not, in list order and once each.
func objectiveVolumes(lvl *world.Level, refs []string) []*world.Volume {
	var out []*world.Volume
	seen := map[*world.Volume]bool{}
	add := func(v *world.Volume) {
		if v != nil && v.Objective != nil && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, r := range refs {
		r = strings.TrimSpace(r)
		switch {
		case r == "":
		case strings.HasPrefix(r, "#"):
			for _, v := range world.FindVolumesByTag(lvl, r[1:]) {
				add(v)
			}
		default:
			add(world.FindVolume(lvl, strings.TrimPrefix(r, "@")))
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
