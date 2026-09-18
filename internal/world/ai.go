package world

// ai.go — an entity that decides where to go by itself.
//
// An AI controller is an `ai:` block on an entity, the way a vehicle is a
// `vehicle:` block: the entity already has a transform, a velocity, bounds,
// collision, animations, tags and a script, which is everything a guard needs
// before anything about deciding starts. So the controller is a spec — how fast
// it walks, what it hunts, where it has been told to go — and a runtime state,
// and it drives the entity through the fields everything else already reads.
//
// It does exactly one thing to the entity each tick: it writes Velocity (and
// turns Rotation to face the way it is going). Gravity, world collision,
// entity collision and step-up are EntityUpdate's, unchanged, which is why an
// agent is stopped by a door it did not know was shut and pushed by a crate it
// walked into, with no AI code involved.
//
// Three behaviours ship, in the order they win:
//
//   - a script's command (`ai_move_to`), which is an instruction rather than a
//     preference, so it outranks everything
//   - a target: something on the Targets list the agent can see, which it
//     chases to AttackRange and then tells its script about
//   - its objectives: volumes carrying an objective block (volume_objective.go),
//     walked to and performed in turn
//
// (a waypoint path, and a leader to keep up with, sit between the target and
// the objectives, in that order — see internal/ai's decide)
//
// and with none of those, it wanders about where it started, or stands still.
//
// The logic that runs all this is internal/ai. What is here is only the data:
// the half a level and a definition author, and the half a tick keeps.

import "github.com/go-gl/mathgl/mgl32"

// AITargetMode is what an agent does about a target it has noticed.
type AITargetMode int

const (
	// AIChase walks to it and engages at AttackRange.
	AIChase AITargetMode = iota
	// AIWatch stays put and turns to face it — a turret, a sentry.
	AIWatch
	// AIIgnore notices it and tells the script, and nothing more: the
	// behaviour is entirely the script's.
	AIIgnore
)

// AITargetModeNames is the YAML/UI name of each mode, indexed by the mode.
var AITargetModeNames = []string{"chase", "watch", "ignore"}

// AITargetModeName returns a mode's name, defaulting out-of-range to chase.
func AITargetModeName(m AITargetMode) string {
	if int(m) < 0 || int(m) >= len(AITargetModeNames) {
		return AITargetModeNames[AIChase]
	}
	return AITargetModeNames[m]
}

// AITargetModeFromName is the inverse. An unknown name reads as chase, so a
// level written by a newer build still opens.
func AITargetModeFromName(name string) AITargetMode {
	for i, n := range AITargetModeNames {
		if n == name {
			return AITargetMode(i)
		}
	}
	return AIChase
}

// AIObjectiveOrder is how an agent chooses its next objective.
type AIObjectiveOrder int

const (
	// AIOrderSequence takes them in the order the list names them — a patrol.
	AIOrderSequence AIObjectiveOrder = iota
	// AIOrderNearest takes the closest one not yet done.
	AIOrderNearest
	// AIOrderRandom takes any one not yet done.
	AIOrderRandom
)

// AIObjectiveOrderNames is the YAML/UI name of each order, indexed by it.
var AIObjectiveOrderNames = []string{"sequence", "nearest", "random"}

// AIObjectiveOrderName returns an order's name, defaulting to sequence.
func AIObjectiveOrderName(o AIObjectiveOrder) string {
	if int(o) < 0 || int(o) >= len(AIObjectiveOrderNames) {
		return AIObjectiveOrderNames[AIOrderSequence]
	}
	return AIObjectiveOrderNames[o]
}

// AIObjectiveOrderFromName is the inverse, defaulting to sequence.
func AIObjectiveOrderFromName(name string) AIObjectiveOrder {
	for i, n := range AIObjectiveOrderNames {
		if n == name {
			return AIObjectiveOrder(i)
		}
	}
	return AIOrderSequence
}

// AISpec is the authored half of an AI controller.
//
// Every number is a plain value a script can change while running, on the
// vehicle drive spec's argument: a guard that runs faster once it is alerted
// needs Speed to move during play, or tuning is only a file format.
type AISpec struct {
	// Enabled switches the controller off without removing it. A disabled
	// agent stops where it is and does nothing — not even notice things.
	Enabled bool

	// ---------- moving ----------

	// Speed is how fast it walks, in units per second; Accel how quickly it
	// gets there and stops again.
	Speed float32
	Accel float32
	// TurnRate is how fast it turns to face the way it is going, in radians a
	// second.
	TurnRate float32
	// StepHeight is the tallest riser it walks up. Zero means the level's own
	// step height (PlayerSettings), which is what a level's stairs were built
	// for.
	StepHeight float32
	// ArriveRadius is how close counts as there.
	ArriveRadius float32
	// FaceMovement turns the entity to face the way it walks. Off for
	// something whose facing a script or an animation owns.
	FaceMovement bool

	// ---------- noticing ----------

	// Targets are what this agent hunts, each one of: `player`, an entity ID,
	// or `#tag` for every entity carrying it. Empty hunts nothing.
	Targets []string
	// SightRange is how far it sees, and FOV how wide, in degrees across the
	// whole cone; 360 sees all round.
	SightRange float32
	FOV        float32
	// RequireSight says a target has to be in the open — not behind a wall —
	// to be seen.
	RequireSight bool
	// HearingRange is how close something may get before it is noticed
	// whatever way the agent is facing and whatever is in between.
	HearingRange float32
	// LoseTime is how long, in seconds, the agent keeps after a target it can
	// no longer see before giving up: it goes to where it last saw it.
	LoseTime float32
	// OnTarget is what it does about one.
	OnTarget AITargetMode
	// AttackRange is how close it closes before stopping, and
	// AttackInterval how often, while it stays that close, the script hears
	// `:target_in_range:` again.
	AttackRange    float32
	AttackInterval float32

	// ---------- objectives ----------

	// Objectives are the volumes it works through, each an ID or `#tag`. Only
	// volumes carrying an enabled objective block count.
	Objectives []string
	Order      AIObjectiveOrder
	// Loop starts the list again once every objective on it is done. A patrol
	// is a looped sequence of objectives with a pause for a duration.
	Loop bool

	// ---------- a path ----------

	// Waypoints names a waypoint animation the agent walks, stop by stop, doing
	// at each point what the point's AI block says (waypoint_ai.go): a level
	// animation's ID, or the name of a path the agent's own type carries. It
	// outranks the objectives, which take over once a path that does not loop
	// is finished. Empty follows nothing.
	Waypoints string
	// WaypointsLoop walks the path again from its first point after its last.
	WaypointsLoop bool

	// ---------- a leader ----------

	// Follow names who the agent keeps up with, as a squadmate does: `player`
	// or an entity ID. Empty follows nobody. It ranks below a path — an agent
	// sent somewhere goes there, then falls back in — and above objectives and
	// wandering. A target is fought from wherever the agent is, until the
	// leader is FollowLeash away, when it regroups and fights on the move.
	Follow string
	// FollowDistance is how far behind the leader its followers gather, and
	// roughly how far apart, in units.
	FollowDistance float32
	// FollowLeash is how far from the leader a follower lets a fight take it
	// before it breaks off to catch up. Zero never breaks off.
	FollowLeash float32
	// RunSpeed is how fast it runs to catch up once it has fallen well behind,
	// and RunAnim what it plays while it does. A RunSpeed no faster than Speed
	// never runs.
	RunSpeed float32
	RunAnim  string

	// ---------- idle ----------

	// WanderRadius is how far from where it started it strolls about when it
	// has nothing to do. Zero stands still. WanderPause is roughly how long it
	// waits between strolls.
	WanderRadius float32
	WanderPause  float32

	// IdleAnim and MoveAnim are played on the entity's attachments as it stops
	// and starts, when it has animations by those names. Empty plays nothing.
	IdleAnim string
	MoveAnim string
}

// DefaultAISpec is an agent before any tuning: a walking, player-hunting-if-
// told-to, sighted guard. Placing one and pressing play should give something
// that moves, not something that needs a dozen numbers first.
func DefaultAISpec() AISpec {
	return AISpec{
		Enabled:        true,
		Speed:          3.5,
		Accel:          20,
		TurnRate:       8,
		ArriveRadius:   0.4,
		FaceMovement:   true,
		SightRange:     20,
		FOV:            120,
		RequireSight:   true,
		HearingRange:   2,
		LoseTime:       3,
		OnTarget:       AIChase,
		AttackRange:    1.5,
		AttackInterval: 1,
		Order:          AIOrderSequence,
		WanderPause:    3,
		FollowDistance: 2.5,
		FollowLeash:    15,
	}
}

// Clone deep-copies a spec. Unlike a vehicle's, an AI spec is never shared
// between instances: its numbers are writable during play, and one guard
// breaking into a run must not speed up every guard of its type.
func (s *AISpec) Clone() *AISpec {
	if s == nil {
		return nil
	}
	c := *s
	c.Targets = append([]string(nil), s.Targets...)
	c.Objectives = append([]string(nil), s.Objectives...)
	return &c
}

// Equal reports whether two specs say the same thing: whether diffing one
// against the other finds nothing to write.
func (s *AISpec) Equal(o *AISpec) bool {
	if s == nil || o == nil {
		return s == o
	}
	return AIToRaw(s, o) == nil
}

// HasAI reports whether an entity carries a controller.
func (e *Entity) HasAI() bool { return e != nil && e.AI != nil }

// AIDriven reports whether the controller is steering the entity this tick —
// what EntityUpdate reads to let it climb steps.
func (e *Entity) AIDriven() bool { return e != nil && e.AI != nil && e.AI.Enabled }

// ---------- runtime ----------

// AIMode is what an agent is doing, as a script reads it.
type AIMode int

const (
	AIIdle AIMode = iota
	AIMoving
	AIWandering
	AITravelling
	AIPerforming
	AIChasing
	AIEngaging
	AIWatching
	AIDisabled
	// AIFollowing is walking a waypoint path; AITakingCover is on the way to
	// cover at a stop, AIInCover is hidden there, and AIWaiting is waiting at
	// a stop in the open.
	AIFollowing
	AITakingCover
	AIInCover
	AIWaiting
	// AIEscorting is keeping up with its leader (Follow), and AIRegrouping is
	// breaking off a fight that has taken it past FollowLeash to catch up.
	AIEscorting
	AIRegrouping
)

// AIModeNames is each mode's name, indexed by the mode.
var AIModeNames = []string{
	"idle", "moving", "wandering", "travelling", "performing",
	"chasing", "engaging", "watching", "disabled",
	"following", "taking_cover", "in_cover", "waiting",
	"escorting", "regrouping",
}

// AIModeName returns a mode's name.
func AIModeName(m AIMode) string {
	if int(m) < 0 || int(m) >= len(AIModeNames) {
		return AIModeNames[AIIdle]
	}
	return AIModeNames[m]
}

// AIState is an agent's runtime half. Never serialised: all of it is this
// session's business, and a level reloaded puts every guard back at the start
// of its round.
//
// It has to survive gob all the same — a compiled level encodes its entities,
// and this rides along at its zero value (see script-tweens' note on the gob
// trap) — so it holds no pointers to scalars and no slices of pointers.
type AIState struct {
	// Session is the controller session this state belongs to. A state left
	// over from an earlier play session is reset rather than resumed.
	Session uint64
	Mode    AIMode
	// Home is where the agent was when its controller first ran: the centre of
	// its wandering.
	Home mgl32.Vec3

	// ---------- a script's command ----------

	Command     bool
	CommandGoal mgl32.Vec3

	// ---------- the target ----------

	// Target is the entity being hunted, by ID ("player" for the player), or
	// empty. TargetForced says a script chose it, so losing sight of it is
	// not losing it.
	Target        string
	TargetForced  bool
	TargetVisible bool
	// SinceSeen counts up while the target is out of sight, and LastKnown is
	// where it was when last seen.
	SinceSeen     float32
	LastKnown     mgl32.Vec3
	InRange       bool
	AttackTimer   float32
	PerceiveTimer float32

	// ---------- objectives ----------

	// Objective is the volume being worked, by ID, and ObjectivePoint the spot
	// in it the agent picked to stand on.
	Objective       string
	ObjectiveForced bool
	ObjectivePoint  mgl32.Vec3
	Performing      bool
	PerformTimer    float32
	// Done lists the objectives finished this round, for nearest and random
	// order; SeqIndex is the position in the list, for sequence order.
	Done               []string
	SeqIndex           int
	ObjectivesFinished bool

	// ---------- a waypoint path ----------

	// Route is the waypoint animation being walked, by level ID, and
	// RouteForced says a script chose it rather than the spec. RouteStop is
	// the index of the point being walked to or waited at.
	Route       string
	RouteForced bool
	RouteStop   int
	// RoutePoint is where in the stop the agent is going — the arrow, or a
	// spot it picked inside the stop's volume — once RoutePicked.
	RoutePoint   mgl32.Vec3
	RoutePicked  bool
	RouteArrived bool
	RouteWait    float32
	// RouteSignal is an ai_continue received for the current stop.
	RouteSignal bool
	// RouteDone is set once a path that does not loop has been walked, or a
	// script has stopped the agent following one; RouteLoop is whether the
	// path being walked starts again at its end.
	RouteDone bool
	RouteLoop bool
	// RestYaw is the heading an agent turns to and keeps while it stands idle
	// once a path has ended, on RestYawOn: the last arrow's, measured in the
	// frame named by RestFrame (empty for the world), so it keeps facing into
	// a hold however the ship under it turns. Anything but idling drops it.
	RestYaw   float32
	RestYawOn bool
	RestFrame string

	// CoverPoint is the spot the agent has chosen to hide at, once HasCover;
	// InCover once it is standing there. CoverTimer counts to the next check
	// that the spot still hides it.
	CoverPoint mgl32.Vec3
	HasCover   bool
	InCover    bool
	CoverTimer float32

	// ---------- a leader ----------

	// FollowLeader is the leader the fields below were kept for, by ID; a
	// different one starts them afresh. FollowLast is where its feet were last
	// tick, and FollowHeading the way it has been heading (a yaw, world space)
	// once FollowHeaded — which is what the group gathers behind.
	FollowLeader  string
	FollowLast    mgl32.Vec3
	FollowHeading float32
	FollowHeaded  bool
	// FollowLeaderVel is the leader's smoothed ground velocity, world space.
	FollowLeaderVel mgl32.Vec3
	// FollowGoal is the place in the group being walked to, re-picked only once
	// the leader's movement has shifted it far enough to be worth a new path.
	FollowGoal    mgl32.Vec3
	FollowStop    float32
	FollowHasGoal bool
	// FollowMoving is closing on the slot; FollowRunning is doing it at
	// RunSpeed; FollowRegroup is breaking off a fight to do it.
	FollowMoving  bool
	FollowRunning bool
	FollowRegroup bool

	// ---------- wandering ----------

	WanderTimer float32
	Wandering   bool
	WanderGoal  mgl32.Vec3

	// ---------- the path ----------

	Path        []mgl32.Vec3
	PathIndex   int
	PathGoal    mgl32.Vec3
	HasPath     bool
	PathPartial bool
	RepathTimer float32
	// StuckTimer and BestDist watch for a body making no headway along its
	// path — a shut door, a crowd — which is what re-plans it.
	StuckTimer float32
	BestDist   float32
	StuckCount int
	// FailTimer holds the agent still after a path could not be found, so a
	// hopeless request is not re-planned every frame.
	FailTimer float32

	// Anim is the animation the controller last started, so it starts one only
	// when it changes.
	Anim string
}

// FindAIAgents returns every level entity carrying a controller.
func FindAIAgents(lvl *Level) []*Entity {
	if lvl == nil {
		return nil
	}
	var out []*Entity
	for i := range lvl.Entities {
		if lvl.Entities[i].AI != nil {
			out = append(out, &lvl.Entities[i])
		}
	}
	return out
}

// ---------- serialization ----------

// RawAI is the YAML form of an AI block, on a definition and as an instance
// override alike. Pointers wherever an instance has to be able to say "leave
// the type's value alone" apart from "set it to zero", on RawDriveSpec's rule.
//
// Targets and Objectives are replaced whole when an instance names any. An
// instance cannot empty a list its type fills; `on_target: ignore` is how an
// instance stops hunting.
type RawAI struct {
	Enabled      *bool    `yaml:"enabled,omitempty"`
	Speed        *float32 `yaml:"speed,omitempty"`
	Accel        *float32 `yaml:"accel,omitempty"`
	TurnRate     *float32 `yaml:"turn_rate,omitempty"`
	StepHeight   *float32 `yaml:"step_height,omitempty"`
	ArriveRadius *float32 `yaml:"arrive_radius,omitempty"`
	FaceMovement *bool    `yaml:"face_movement,omitempty"`

	Targets        []string `yaml:"targets,omitempty"`
	SightRange     *float32 `yaml:"sight_range,omitempty"`
	FOV            *float32 `yaml:"fov,omitempty"`
	RequireSight   *bool    `yaml:"require_sight,omitempty"`
	HearingRange   *float32 `yaml:"hearing_range,omitempty"`
	LoseTime       *float32 `yaml:"lose_time,omitempty"`
	OnTarget       string   `yaml:"on_target,omitempty"` // chase (default) | watch | ignore
	AttackRange    *float32 `yaml:"attack_range,omitempty"`
	AttackInterval *float32 `yaml:"attack_interval,omitempty"`

	Objectives []string `yaml:"objectives,omitempty"`
	Order      string   `yaml:"order,omitempty"` // sequence (default) | nearest | random
	Loop       *bool    `yaml:"loop,omitempty"`

	Waypoints     *string `yaml:"waypoints,omitempty"`
	WaypointsLoop *bool   `yaml:"waypoints_loop,omitempty"`

	Follow         *string  `yaml:"follow,omitempty"`
	FollowDistance *float32 `yaml:"follow_distance,omitempty"`
	FollowLeash    *float32 `yaml:"follow_leash,omitempty"`
	RunSpeed       *float32 `yaml:"run_speed,omitempty"`
	RunAnim        *string  `yaml:"run_anim,omitempty"`

	WanderRadius *float32 `yaml:"wander_radius,omitempty"`
	WanderPause  *float32 `yaml:"wander_pause,omitempty"`

	IdleAnim *string `yaml:"idle_anim,omitempty"`
	MoveAnim *string `yaml:"move_anim,omitempty"`
}

// AIFromRaw overlays a raw block onto base — the definition's spec for an
// instance, or nil for the defaults — and returns the result. A nil block
// returns base unchanged (cloned), so an instance that says nothing keeps its
// type's controller and an entity of neither has none.
func AIFromRaw(base *AISpec, r *RawAI) *AISpec {
	if r == nil {
		return base.Clone()
	}
	var s AISpec
	if base != nil {
		s = *base.Clone()
	} else {
		s = DefaultAISpec()
	}
	setF := func(dst *float32, src *float32) {
		if src != nil {
			*dst = *src
		}
	}
	setB := func(dst *bool, src *bool) {
		if src != nil {
			*dst = *src
		}
	}
	setB(&s.Enabled, r.Enabled)
	setF(&s.Speed, r.Speed)
	setF(&s.Accel, r.Accel)
	setF(&s.TurnRate, r.TurnRate)
	setF(&s.StepHeight, r.StepHeight)
	setF(&s.ArriveRadius, r.ArriveRadius)
	setB(&s.FaceMovement, r.FaceMovement)
	if len(r.Targets) > 0 {
		s.Targets = append([]string(nil), r.Targets...)
	}
	setF(&s.SightRange, r.SightRange)
	setF(&s.FOV, r.FOV)
	setB(&s.RequireSight, r.RequireSight)
	setF(&s.HearingRange, r.HearingRange)
	setF(&s.LoseTime, r.LoseTime)
	if r.OnTarget != "" {
		s.OnTarget = AITargetModeFromName(r.OnTarget)
	}
	setF(&s.AttackRange, r.AttackRange)
	setF(&s.AttackInterval, r.AttackInterval)
	if len(r.Objectives) > 0 {
		s.Objectives = append([]string(nil), r.Objectives...)
	}
	if r.Order != "" {
		s.Order = AIObjectiveOrderFromName(r.Order)
	}
	setB(&s.Loop, r.Loop)
	if r.Waypoints != nil {
		s.Waypoints = *r.Waypoints
	}
	setB(&s.WaypointsLoop, r.WaypointsLoop)
	if r.Follow != nil {
		s.Follow = *r.Follow
	}
	setF(&s.FollowDistance, r.FollowDistance)
	setF(&s.FollowLeash, r.FollowLeash)
	setF(&s.RunSpeed, r.RunSpeed)
	if r.RunAnim != nil {
		s.RunAnim = *r.RunAnim
	}
	setF(&s.WanderRadius, r.WanderRadius)
	setF(&s.WanderPause, r.WanderPause)
	if r.IdleAnim != nil {
		s.IdleAnim = *r.IdleAnim
	}
	if r.MoveAnim != nil {
		s.MoveAnim = *r.MoveAnim
	}
	return &s
}

// AIToRaw writes only what s says differently from base — the definition's
// spec for an instance, or nil to diff against the defaults. It returns nil
// when s is nil, and nil when an instance matches its type exactly; a spec
// diffed against the defaults always returns a block, even an empty one,
// because `ai: {}` is how a level says an entity has a controller at all.
func AIToRaw(s, base *AISpec) *RawAI {
	if s == nil {
		return nil
	}
	againstDefaults := base == nil
	b := DefaultAISpec()
	if base != nil {
		b = *base
	}
	r := &RawAI{}
	any := false
	diffF := func(dst **float32, got, want float32) {
		if got != want {
			v := got
			*dst = &v
			any = true
		}
	}
	diffB := func(dst **bool, got, want bool) {
		if got != want {
			v := got
			*dst = &v
			any = true
		}
	}
	diffB(&r.Enabled, s.Enabled, b.Enabled)
	diffF(&r.Speed, s.Speed, b.Speed)
	diffF(&r.Accel, s.Accel, b.Accel)
	diffF(&r.TurnRate, s.TurnRate, b.TurnRate)
	diffF(&r.StepHeight, s.StepHeight, b.StepHeight)
	diffF(&r.ArriveRadius, s.ArriveRadius, b.ArriveRadius)
	diffB(&r.FaceMovement, s.FaceMovement, b.FaceMovement)
	if !stringsEqual(s.Targets, b.Targets) {
		r.Targets = append([]string(nil), s.Targets...)
		any = true
	}
	diffF(&r.SightRange, s.SightRange, b.SightRange)
	diffF(&r.FOV, s.FOV, b.FOV)
	diffB(&r.RequireSight, s.RequireSight, b.RequireSight)
	diffF(&r.HearingRange, s.HearingRange, b.HearingRange)
	diffF(&r.LoseTime, s.LoseTime, b.LoseTime)
	if s.OnTarget != b.OnTarget {
		r.OnTarget = AITargetModeName(s.OnTarget)
		any = true
	}
	diffF(&r.AttackRange, s.AttackRange, b.AttackRange)
	diffF(&r.AttackInterval, s.AttackInterval, b.AttackInterval)
	if !stringsEqual(s.Objectives, b.Objectives) {
		r.Objectives = append([]string(nil), s.Objectives...)
		any = true
	}
	if s.Order != b.Order {
		r.Order = AIObjectiveOrderName(s.Order)
		any = true
	}
	diffB(&r.Loop, s.Loop, b.Loop)
	if s.Waypoints != b.Waypoints {
		v := s.Waypoints
		r.Waypoints = &v
		any = true
	}
	diffB(&r.WaypointsLoop, s.WaypointsLoop, b.WaypointsLoop)
	if s.Follow != b.Follow {
		v := s.Follow
		r.Follow = &v
		any = true
	}
	diffF(&r.FollowDistance, s.FollowDistance, b.FollowDistance)
	diffF(&r.FollowLeash, s.FollowLeash, b.FollowLeash)
	diffF(&r.RunSpeed, s.RunSpeed, b.RunSpeed)
	if s.RunAnim != b.RunAnim {
		v := s.RunAnim
		r.RunAnim = &v
		any = true
	}
	diffF(&r.WanderRadius, s.WanderRadius, b.WanderRadius)
	diffF(&r.WanderPause, s.WanderPause, b.WanderPause)
	if s.IdleAnim != b.IdleAnim {
		v := s.IdleAnim
		r.IdleAnim = &v
		any = true
	}
	if s.MoveAnim != b.MoveAnim {
		v := s.MoveAnim
		r.MoveAnim = &v
		any = true
	}
	if !any && !againstDefaults {
		return nil
	}
	return r
}
