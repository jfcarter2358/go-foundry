package ai

// query.go — what a script may ask of, and tell, a controller.
//
// Each of these is the engine half of a script verb or property
// (internal/script/script_actions_ai.go). They take the level and the entity
// rather than refs, and report plainly whether they did anything, so the
// script layer's job is only turning refs into entities and results into
// warnings.

import (
	"foundry/internal/nav"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// state is the agent's runtime state, started if this session has none yet —
// so a command given in a `:spawn:` handler, before the controller's first
// tick, is not lost when that tick starts it.
func state(lvl *world.Level, e *world.Entity) *world.AIState {
	for i := range lvl.Entities {
		if &lvl.Entities[i] == e {
			return stateFor(lvl, e, i)
		}
	}
	return stateFor(lvl, e, 0)
}

// MoveTo commands an agent to walk to a point, outranking its target and its
// objectives until it arrives, and reports whether the point is somewhere it
// can get to. Unreachable is still commanded: the agent walks as near as it
// can, which is usually what a script meant.
func MoveTo(lvl *world.Level, e *world.Entity, goal mgl32.Vec3) bool {
	if lvl == nil || e == nil || e.AI == nil {
		return false
	}
	st := state(lvl, e)
	st.Command = true
	st.CommandGoal = goal
	st.FailTimer = 0
	st.StuckCount = 0
	clearPath(st)
	grid := gridFor(lvl, e)
	if grid == nil || len(grid.Nodes) == 0 {
		return true
	}
	return grid.Reachable(feetOf(lvl, e), goal)
}

// Stop cancels a command and reports whether there was one. The agent goes
// back to whatever it would otherwise be doing.
func Stop(lvl *world.Level, e *world.Entity) bool {
	if lvl == nil || e == nil || e.AI == nil || e.AIState == nil || e.AIState.Session != session {
		return false
	}
	st := e.AIState
	had := st.Command
	st.Command = false
	clearPath(st)
	brake(e, e.AI, 1e9)
	return had
}

// SetTarget makes t the agent's target, chosen by a script and so kept however
// long it is out of sight. A nil t hands targeting back to what the agent can
// see. Reports whether the agent has a controller.
func SetTarget(lvl *world.Level, e, t *world.Entity) bool {
	if lvl == nil || e == nil || e.AI == nil {
		return false
	}
	st := state(lvl, e)
	if t == nil {
		st.Target, st.TargetForced, st.TargetVisible, st.InRange = "", false, false, false
		clearPath(st)
		return true
	}
	if t == e {
		return false
	}
	st.Target = t.ID
	st.TargetForced = true
	st.TargetVisible = Sees(lvl, e, t)
	st.LastKnown = feetOf(lvl, t)
	st.SinceSeen = 0
	st.InRange = false
	clearPath(st)
	return true
}

// SetObjective sends the agent to perform one objective now, ahead of its own
// list, and reports whether the volume is one it may take. A nil volume drops
// whatever objective it is working, and it takes its next as usual.
func SetObjective(lvl *world.Level, e *world.Entity, vol *world.Volume) bool {
	if lvl == nil || e == nil || e.AI == nil {
		return false
	}
	st := state(lvl, e)
	if vol == nil {
		dropObjective(st)
		return true
	}
	if !vol.ObjectiveAcceptsAgent(e) {
		return false
	}
	assignObjective(lvl, e, st, vol)
	st.ObjectiveForced = true
	st.ObjectivesFinished = false
	return true
}

// Target is the agent's current target, or nil.
func Target(lvl *world.Level, e *world.Entity) *world.Entity {
	if e == nil || e.AIState == nil || e.AIState.Session != session {
		return nil
	}
	return targetEntity(lvl, e.AIState.Target, nil)
}

// Mode is what the agent is doing, as a script reads it.
func Mode(e *world.Entity) world.AIMode {
	switch {
	case e == nil || e.AI == nil:
		return world.AIIdle
	case !e.AI.Enabled:
		return world.AIDisabled
	case e.AIState == nil || e.AIState.Session != session:
		return world.AIIdle
	}
	return e.AIState.Mode
}

// Destination is where the agent is walking, and whether it is walking
// anywhere.
func Destination(e *world.Entity) (mgl32.Vec3, bool) {
	if e == nil || e.AIState == nil || e.AIState.Session != session {
		return mgl32.Vec3{}, false
	}
	st := e.AIState
	if !st.HasPath || len(st.Path) == 0 {
		return mgl32.Vec3{}, false
	}
	return st.Path[len(st.Path)-1], true
}

// Objective is the volume the agent is working, or nil.
func Objective(lvl *world.Level, e *world.Entity) *world.Volume {
	if e == nil || e.AIState == nil || e.AIState.Session != session {
		return nil
	}
	return world.FindVolume(lvl, e.AIState.Objective)
}

// ObjectiveAgents counts the agents working an objective, walking to it
// included.
func ObjectiveAgents(lvl *world.Level, vol *world.Volume) int {
	if lvl == nil || vol == nil {
		return 0
	}
	n := 0
	for i := range lvl.Entities {
		st := lvl.Entities[i].AIState
		if st != nil && st.Session == session && st.Objective == vol.ID {
			n++
		}
	}
	return n
}

// FindPath is a route between two points for a body the size of e, or of the
// player when e is nil — what a script's `find_path` answers with.
func FindPath(lvl *world.Level, e *world.Entity, from, to mgl32.Vec3) (nav.Path, bool) {
	grid := nav.For(lvl, ProfileFor(lvl, e))
	if grid == nil || len(grid.Nodes) == 0 {
		return nav.Path{}, false
	}
	return grid.FindPath(from, to)
}

// Feet is where an entity stands, as navigation measures it.
func Feet(lvl *world.Level, e *world.Entity) mgl32.Vec3 { return feetOf(lvl, e) }

// Eye is where an entity looks from.
func Eye(lvl *world.Level, e *world.Entity) mgl32.Vec3 { return eyeOf(lvl, e) }
