package world

// waypoint_ai.go — what an AI agent does at each point of a waypoint animation.
//
// A waypoint animation is a path of placed arrows (waypoint.go). A dynamic
// brush or a sprite sent along one with `play_waypoints` glides through the
// points on a clock. An AI agent told to follow one — its `ai: waypoints:`, or
// `ai_follow_waypoints` — walks it instead: it finds its own way to each point
// on the level's navigation, at its own pace, and ignores the times.
//
// What a point can add for an agent is this block. With none, a point is
// somewhere to pass through. With one, the point says how to arrive and what to
// do there:
//
//   - run or creep on the leg to it (SpeedScale)
//   - whether to fight on the way, and how (Engage)
//   - wait there, for a while or until a script says go (Wait, WaitForSignal)
//   - take cover near it, hidden from whatever it is facing (TakeCover)
//   - face the way its arrow points, and play an animation while it waits
//
// A point may also name a VOLUME. The stop is then that area rather than the
// arrow: the agent goes to somewhere inside it that it can reach, and takes
// cover anywhere inside it. The arrow still says which way is forward, and is
// where the path is drawn through.
//
// Every setting a point carries governs the leg ARRIVING at it and the stop
// itself — "run to that crate and take cover behind it" is one point.

import "github.com/go-gl/mathgl/mgl32"

// AIEngageMode is how an agent treats a target on the leg to a point and while
// stopped there.
type AIEngageMode int

const (
	// AIEngageInherit does what the agent's own On Target says — except at a
	// point that takes cover, where it holds (see EffectiveEngage).
	AIEngageInherit AIEngageMode = iota
	// AIEngageChase leaves the path to chase, and comes back to it after.
	AIEngageChase
	// AIEngageHold keeps to the path, facing a target it can see and posting
	// :target_in_range: when one is close enough — firing on the move.
	AIEngageHold
	// AIEngageIgnore keeps to the path and does nothing about a target but
	// tell the script: a stealthy leg.
	AIEngageIgnore
)

// AIEngageModeNames is the YAML/UI name of each mode, indexed by the mode.
var AIEngageModeNames = []string{"inherit", "chase", "hold", "ignore"}

// AIEngageModeName returns a mode's name, defaulting to inherit.
func AIEngageModeName(m AIEngageMode) string {
	if int(m) < 0 || int(m) >= len(AIEngageModeNames) {
		return AIEngageModeNames[AIEngageInherit]
	}
	return AIEngageModeNames[m]
}

// AIEngageModeFromName is the inverse, defaulting to inherit.
func AIEngageModeFromName(name string) AIEngageMode {
	for i, n := range AIEngageModeNames {
		if n == name {
			return AIEngageMode(i)
		}
	}
	return AIEngageInherit
}

// DefaultCoverRadius is how far from a point an agent looks for cover when the
// point does not say.
const DefaultCoverRadius = float32(6)

// WaypointAI is one point's instructions to an agent following the path.
type WaypointAI struct {
	// Volume names a level volume that is the stop instead of the arrow: the
	// agent goes somewhere inside it. Empty for an ordinary point.
	Volume string
	// Wait is how long, in seconds, the agent stays once it arrives. Zero
	// passes straight through.
	Wait float32
	// WaitForSignal holds the agent here until a script calls ai_continue,
	// after Wait has run out.
	WaitForSignal bool
	// SpeedScale multiplies the agent's speed on the leg to this point. Zero
	// reads as 1.
	SpeedScale float32
	// Engage is how the agent treats a target on the leg and at the stop.
	Engage AIEngageMode
	// TakeCover sends the agent, once it arrives, to the nearest spot it can
	// reach within CoverRadius (or inside Volume) where it is hidden from the
	// threat: its target, or failing one, whatever lies the way the arrow
	// points.
	TakeCover bool
	// CoverRadius is how far from the point cover is looked for. Zero reads as
	// DefaultCoverRadius.
	CoverRadius float32
	// Face turns the agent the way the arrow points while it waits.
	Face bool
	// Animation plays on the agent while it waits here.
	Animation string
}

// EffectiveSpeedScale is SpeedScale with its zero read as 1.
func (w *WaypointAI) EffectiveSpeedScale() float32 {
	if w == nil || w.SpeedScale <= 0 {
		return 1
	}
	return w.SpeedScale
}

// EffectiveCoverRadius is CoverRadius with its zero read as the default.
func (w *WaypointAI) EffectiveCoverRadius() float32 {
	if w == nil || w.CoverRadius <= 0 {
		return DefaultCoverRadius
	}
	return w.CoverRadius
}

// EffectiveEngage is the engage mode that applies. A point that takes cover
// holds unless it says otherwise: an agent told to get behind that wall should
// not leave for the first enemy it sees.
func (w *WaypointAI) EffectiveEngage() AIEngageMode {
	if w == nil {
		return AIEngageInherit
	}
	if w.Engage == AIEngageInherit && w.TakeCover {
		return AIEngageHold
	}
	return w.Engage
}

// Stops reports whether an agent pauses at the point at all, rather than
// passing through it.
func (w *WaypointAI) Stops() bool {
	return w != nil && (w.Wait > 0 || w.WaitForSignal || w.TakeCover)
}

// Forward is the way a point's arrow faces in world space: its +X, turned by
// its rotation and by whatever carries the animation.
func (a *WaypointAnimation) Forward(lvl *Level, i int) mgl32.Vec3 {
	if a == nil || i < 0 || i >= len(a.Points) {
		return mgl32.Vec3{1, 0, 0}
	}
	m := a.SpaceMatrix(lvl).Mul4(EulerMatrixYXZ(a.Points[i].Rotation))
	v := m.Col(0).Vec3()
	if l := v.Len(); l > 1e-6 {
		v = v.Mul(1 / l)
	}
	return v
}

// WorldPoint is point i's position in world space.
func (a *WaypointAnimation) WorldPoint(lvl *Level, i int) mgl32.Vec3 {
	if a == nil || i < 0 || i >= len(a.Points) {
		return mgl32.Vec3{}
	}
	return mulPoint(a.SpaceMatrix(lvl), a.Points[i].Position)
}

// cloneWaypointAI copies a point's AI block.
func cloneWaypointAI(w *WaypointAI) *WaypointAI {
	if w == nil {
		return nil
	}
	c := *w
	return &c
}

// ---------- serialization ----------

// RawWaypointAI is the YAML form of a point's `ai:` block.
type RawWaypointAI struct {
	Volume        string  `yaml:"volume,omitempty"`
	Wait          float32 `yaml:"wait,omitempty"`
	WaitForSignal bool    `yaml:"wait_for_signal,omitempty"`
	SpeedScale    float32 `yaml:"speed_scale,omitempty"`
	Engage        string  `yaml:"engage,omitempty"` // inherit (default) | chase | hold | ignore
	TakeCover     bool    `yaml:"take_cover,omitempty"`
	CoverRadius   float32 `yaml:"cover_radius,omitempty"`
	Face          bool    `yaml:"face,omitempty"`
	Animation     string  `yaml:"animation,omitempty"`
}

// WaypointAIFromRaw converts the YAML form; nil stays nil.
func WaypointAIFromRaw(r *RawWaypointAI) *WaypointAI {
	if r == nil {
		return nil
	}
	w := &WaypointAI{
		Volume:        r.Volume,
		Wait:          r.Wait,
		WaitForSignal: r.WaitForSignal,
		SpeedScale:    r.SpeedScale,
		Engage:        AIEngageModeFromName(r.Engage),
		TakeCover:     r.TakeCover,
		CoverRadius:   r.CoverRadius,
		Face:          r.Face,
		Animation:     r.Animation,
	}
	if w.Wait < 0 {
		w.Wait = 0
	}
	if w.SpeedScale < 0 {
		w.SpeedScale = 0
	}
	if w.CoverRadius < 0 {
		w.CoverRadius = 0
	}
	return w
}

// WaypointAIToRaw is the inverse.
func WaypointAIToRaw(w *WaypointAI) *RawWaypointAI {
	if w == nil {
		return nil
	}
	r := &RawWaypointAI{
		Volume:        w.Volume,
		Wait:          w.Wait,
		WaitForSignal: w.WaitForSignal,
		SpeedScale:    w.SpeedScale,
		TakeCover:     w.TakeCover,
		CoverRadius:   w.CoverRadius,
		Face:          w.Face,
		Animation:     w.Animation,
	}
	if w.Engage != AIEngageInherit {
		r.Engage = AIEngageModeName(w.Engage)
	}
	return r
}
