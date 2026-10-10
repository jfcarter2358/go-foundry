package world

// volume_objective.go — a place an agent is sent to do something.
//
// "Go and stand at the console, then hack it." "Walk to the barricade and wait
// there for five seconds." "Patrol these three rooms." Each of those is a
// region, a duration and a name for what was done, and the region is the part
// that belongs to the level — which is why the block goes on a volume, rather
// than on the agent that visits it.
//
// An agent whose `ai:` block lists the volume walks to a point inside it,
// stays there for Duration (playing Animation, when it has one), and then the
// action is done: `:objective_complete:` and the Action's own atom are posted to
// both the agent and the volume. So the volume's script is where "the console
// has been hacked" lives, and the agent's is where "I hacked a console" does —
// and neither has to poll the other.
//
// The region itself stays an ordinary volume. It still fires `:enter:` and
// `:exit:`, can still be fog, and is still switched off by Enabled — which
// takes its objective with it.

import (
	"sort"
	"strings"
)

// VolumeObjective marks a volume as a place agents are sent to act.
type VolumeObjective struct {
	// Enabled switches the objective off while keeping the rest. A volume's own
	// Enabled does too.
	Enabled bool

	// Action names what performing it is, as an atom without its colons:
	// `hack` posts `:hack:`. Empty posts only `:objective_complete:`.
	Action string
	// Duration is how long, in seconds, an agent stays inside to perform it.
	// Zero completes it on arrival.
	Duration float32
	// Animation plays on the agent while it performs, if it has one by that
	// name.
	Animation string

	// Capacity is how many agents may be working it at once, walking to it
	// included. Zero is no limit.
	Capacity int
	// AgentTag, when set, restricts it to agents carrying that tag — a door
	// only engineers repair.
	AgentTag string
	// Face turns a performing agent to the volume's own facing (its local -Z),
	// so a console is worked from in front.
	Face bool
	// Once switches the objective off after its first completion.
	Once bool
}

// DefaultVolumeObjective is what ticking the box starts with: performed on
// arrival, by anyone, as often as asked.
func DefaultVolumeObjective() VolumeObjective {
	return VolumeObjective{Enabled: true, Duration: 1}
}

// IsObjective reports whether the volume is currently a usable objective.
func (v *Volume) IsObjective() bool {
	return v != nil && v.Objective != nil && v.Objective.Enabled && v.Enabled && v.HasBounds()
}

// ObjectiveAcceptsAgent reports whether the objective is one this agent may
// take, as far as the volume is concerned. Capacity is the controller's
// business, since only it knows who else is on the way.
func (v *Volume) ObjectiveAcceptsAgent(e *Entity) bool {
	if !v.IsObjective() {
		return false
	}
	return v.Objective.AgentTag == "" || e.HasTag(v.Objective.AgentTag)
}

// ActionAtom is the objective's action with any colons an author typed trimmed
// off, so `:hack:` and `hack` post the same message.
func (o *VolumeObjective) ActionAtom() string {
	if o == nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(o.Action), ":")
}

// RawVolumeObjective is the YAML form. Disabled rather than Enabled, on
// RawVolumeFog's rule: `objective: {}` is an objective.
type RawVolumeObjective struct {
	Disabled  bool     `yaml:"disabled,omitempty"`
	Action    string   `yaml:"action,omitempty"`
	Duration  *float32 `yaml:"duration,omitempty"` // default 1
	Animation string   `yaml:"animation,omitempty"`
	Capacity  int      `yaml:"capacity,omitempty"`
	AgentTag  string   `yaml:"agent_tag,omitempty"`
	Face      bool     `yaml:"face,omitempty"`
	Once      bool     `yaml:"once,omitempty"`
}

// VolumeObjectiveFromRaw converts the YAML form; nil stays nil.
func VolumeObjectiveFromRaw(r *RawVolumeObjective) *VolumeObjective {
	if r == nil {
		return nil
	}
	o := DefaultVolumeObjective()
	o.Enabled = !r.Disabled
	o.Action = r.Action
	if r.Duration != nil {
		o.Duration = *r.Duration
	}
	o.Animation = r.Animation
	o.Capacity = r.Capacity
	if o.Capacity < 0 {
		o.Capacity = 0
	}
	o.AgentTag = r.AgentTag
	o.Face = r.Face
	o.Once = r.Once
	return &o
}

// VolumeObjectiveToRaw is the inverse.
func VolumeObjectiveToRaw(o *VolumeObjective) *RawVolumeObjective {
	if o == nil {
		return nil
	}
	r := &RawVolumeObjective{
		Disabled:  !o.Enabled,
		Action:    o.Action,
		Animation: o.Animation,
		Capacity:  o.Capacity,
		AgentTag:  o.AgentTag,
		Face:      o.Face,
		Once:      o.Once,
	}
	if o.Duration != DefaultVolumeObjective().Duration {
		d := o.Duration
		r.Duration = &d
	}
	return r
}

// ObjectiveActionAtoms lists the action atoms the level's objectives post,
// sorted and once each — what the script linter takes for a design rather than
// a misspelt engine event, as it does a waypoint's own events.
func ObjectiveActionAtoms(lvl *Level) []string {
	if lvl == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for i := range lvl.Volumes {
		if atom := lvl.Volumes[i].Objective.ActionAtom(); atom != "" && !seen[atom] {
			seen[atom] = true
			out = append(out, atom)
		}
	}
	sort.Strings(out)
	return out
}
