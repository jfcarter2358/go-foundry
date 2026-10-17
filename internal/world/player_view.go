package world

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// player_view.go — how the player is seen: the camera mode, and the body a
// third-person camera looks at.
//
// The engine was first person only: the camera is the player's eye and the
// only thing of the player ever drawn is the viewmodel in front of it. A level
// may now ask for a third-person camera instead (`player: camera:`), which
// swings out behind and over the shoulder on a boom, and give the player a
// body (`player: body:`) — model attachments exactly like an entity type's,
// drawn in the world pass with shadows, turned to face the way it walks and
// animated by its own motion.
//
// Both blocks are optional and their zero values are exactly what every level
// had before them: first person, no body. The camera's own maths is
// internal/sim's (third_person.go); the body's clip choice is ChooseLocomotion
// below, kept here and pure so it can be tested without a model.

// The camera modes.
const (
	CameraFirstPerson = "first_person"
	CameraThirdPerson = "third_person"
)

// PlayerCamera is the level's `player: camera:` block, resolved: every field
// holds the number in force. The zero value is "the level said nothing", which
// Effective reads as DefaultPlayerCamera — so a Level built as a struct
// literal (tests, the editor's new level) stays first person.
type PlayerCamera struct {
	// Mode is CameraFirstPerson or CameraThirdPerson; "" is first person.
	Mode string
	// Distance is the boom's length behind the pivot, in world units, before
	// anything pulls it in.
	Distance float32
	// Height is the pivot's height above the body's centre (world units),
	// which is PlayerEntity.Position. The first-person eye is EyeHeight (1.7)
	// above it.
	Height float32
	// Shoulder slides the pivot sideways, to the camera's right (negative is
	// the left shoulder), so the body stands off to one side of the reticle.
	Shoulder float32
	// PitchMin and PitchMax limit how far the view looks down and up, in
	// degrees, while the camera is third person.
	PitchMin, PitchMax float32
	// Radius is how far the camera keeps from whatever it is pulled in by.
	Radius float32
	// Smoothing is how quickly the boom eases back out after something
	// stopped pulling it in, as the fraction of the gap closed per second
	// (exponential). Pulling in is never eased: a camera that eased into a
	// wall would show its inside.
	Smoothing float32
	// HideDistance is the boom length below which the body is hidden, so a
	// camera squeezed against the player's back does not look out through
	// the inside of its head.
	HideDistance float32
}

// DefaultPlayerCamera is what an absent block, or an absent key, means.
func DefaultPlayerCamera() PlayerCamera {
	return PlayerCamera{
		Mode:         CameraFirstPerson,
		Distance:     4.8,
		Height:       1.55,
		Shoulder:     -0.8,
		PitchMin:     -65,
		PitchMax:     70,
		Radius:       0.3,
		Smoothing:    5,
		HideDistance: 0.9,
	}
}

// Effective is the camera in force: the defaults when nothing was said.
func (c PlayerCamera) Effective() PlayerCamera {
	if c == (PlayerCamera{}) {
		return DefaultPlayerCamera()
	}
	if c.Mode == "" {
		c.Mode = CameraFirstPerson
	}
	return c
}

// ThirdPerson reports whether this camera swings out behind the body.
func (c PlayerCamera) ThirdPerson() bool { return c.Mode == CameraThirdPerson }

// ValidCameraMode reports whether s names a camera mode.
func ValidCameraMode(s string) bool {
	return s == CameraFirstPerson || s == CameraThirdPerson
}

// RawPlayerCamera is the YAML form. Every number is a pointer so an absent key
// follows the default rather than freezing today's at zero — shoulder 0 (a
// centred camera) and pitch 0 are both things an author can mean.
type RawPlayerCamera struct {
	// Mode is first_person (default) or third_person.
	Mode string `yaml:"mode,omitempty"`
	// Distance is the boom length behind the pivot, in world units. Default 4.8.
	Distance *float32 `yaml:"distance,omitempty"`
	// Height is the pivot's height above the body's centre. Default 1.55 (the
	// first-person eye is 1.7).
	Height *float32 `yaml:"height,omitempty"`
	// Shoulder slides the pivot to the camera's right; negative is the left
	// shoulder (the body stands right of the reticle), 0 centres the body.
	// Default -0.8.
	Shoulder *float32 `yaml:"shoulder,omitempty"`
	// PitchMin and PitchMax limit the look in degrees while third person.
	// Defaults -65 and 70.
	PitchMin *float32 `yaml:"pitch_min,omitempty"`
	PitchMax *float32 `yaml:"pitch_max,omitempty"`
	// Radius is how far the camera keeps from a wall that pulls it in.
	// Default 0.3.
	Radius *float32 `yaml:"radius,omitempty"`
	// Smoothing is how fast the boom eases back out once a wall stops pulling
	// it in (fraction of the gap closed per second). Default 5.
	Smoothing *float32 `yaml:"smoothing,omitempty"`
	// HideDistance is the boom length under which the body is hidden.
	// Default 0.9.
	HideDistance *float32 `yaml:"hide_distance,omitempty"`
}

func pickF(p *float32, def float32) float32 {
	if p != nil {
		return *p
	}
	return def
}

// ptrIfNot is a pointer to v unless it is the default.
func ptrIfNot(v, def float32) *float32 {
	if v == def {
		return nil
	}
	return &v
}

// PlayerCameraFromRaw resolves the block against the defaults.
func PlayerCameraFromRaw(r *RawPlayerCamera) PlayerCamera {
	if r == nil {
		return PlayerCamera{}
	}
	d := DefaultPlayerCamera()
	c := PlayerCamera{
		Mode:         d.Mode,
		Distance:     pickF(r.Distance, d.Distance),
		Height:       pickF(r.Height, d.Height),
		Shoulder:     pickF(r.Shoulder, d.Shoulder),
		PitchMin:     pickF(r.PitchMin, d.PitchMin),
		PitchMax:     pickF(r.PitchMax, d.PitchMax),
		Radius:       pickF(r.Radius, d.Radius),
		Smoothing:    pickF(r.Smoothing, d.Smoothing),
		HideDistance: pickF(r.HideDistance, d.HideDistance),
	}
	if r.Mode != "" {
		c.Mode = r.Mode
	}
	return c
}

// PlayerCameraToRaw writes back only what differs from the default, and nil
// when nothing does — which keeps a level that never had the block without one.
func PlayerCameraToRaw(c PlayerCamera) *RawPlayerCamera {
	c = c.Effective()
	d := DefaultPlayerCamera()
	if c == d {
		return nil
	}
	r := &RawPlayerCamera{
		Distance:     ptrIfNot(c.Distance, d.Distance),
		Height:       ptrIfNot(c.Height, d.Height),
		Shoulder:     ptrIfNot(c.Shoulder, d.Shoulder),
		PitchMin:     ptrIfNot(c.PitchMin, d.PitchMin),
		PitchMax:     ptrIfNot(c.PitchMax, d.PitchMax),
		Radius:       ptrIfNot(c.Radius, d.Radius),
		Smoothing:    ptrIfNot(c.Smoothing, d.Smoothing),
		HideDistance: ptrIfNot(c.HideDistance, d.HideDistance),
	}
	if c.Mode != d.Mode {
		r.Mode = c.Mode
	}
	return r
}

// ---------- the body ----------

// PlayerBody is the level's `player: body:` block: what the third-person
// camera sees of the player. Zero is no body.
type PlayerBody struct {
	// Models are attachments exactly like an entity type's `models:`. The
	// body entity's origin is the player's FEET, facing −Z like any entity,
	// so a model authored facing +Z mounts with `rotation: {y: π}`.
	Models []EntityModel
	// TurnRate is how fast the body turns to face the way it is moving, in
	// degrees a second. 0 is the default.
	TurnRate float32
	// Locomotion names the clips motion picks between.
	Locomotion PlayerLocomotion
}

// DefaultBodyTurnRate is PlayerBody.TurnRate's default, degrees a second.
const DefaultBodyTurnRate = float32(600)

// EffectiveTurnRate is the turn rate in radians a second.
func (b PlayerBody) EffectiveTurnRate() float32 {
	r := b.TurnRate
	if r <= 0 {
		r = DefaultBodyTurnRate
	}
	return r * math.Pi / 180
}

// PlayerLocomotion is the clip each state of motion plays. A name is first
// looked up among the attachment's own `animations:` (so speed/loop/blend
// apply) and otherwise taken as a clip in the model. "" is the default, which
// matches the `human` skeleton's clips.
type PlayerLocomotion struct {
	Idle, Walk, Run, Sprint, Jump, JumpRun, Fall, CrouchIdle, CrouchWalk string
	// WalkSpeed and RunSpeed are the ground speeds, in world units a second,
	// the walk and run clips were authored at: playback is scaled by how fast
	// the body is really going, so the feet do not skate. 0 is the default.
	WalkSpeed, RunSpeed float32
}

// DefaultLocomotion is the `human` skeleton's clip set, with its walk and run
// speeds at the 1.5 scale every human character in the project is placed at.
func DefaultLocomotion() PlayerLocomotion {
	return PlayerLocomotion{
		Idle: "idle", Walk: "walk", Run: "run", Sprint: "run",
		Jump: "jump_up", JumpRun: "jump_run", Fall: "jet_fall",
		CrouchIdle: "crouch_idle", CrouchWalk: "crouch_walk",
		WalkSpeed: 1.8, RunSpeed: 4.95,
	}
}

// Effective fills every unset field from DefaultLocomotion.
func (l PlayerLocomotion) Effective() PlayerLocomotion {
	d := DefaultLocomotion()
	s := func(v *string, def string) {
		if *v == "" {
			*v = def
		}
	}
	s(&l.Idle, d.Idle)
	s(&l.Walk, d.Walk)
	s(&l.Run, d.Run)
	s(&l.Sprint, d.Sprint)
	s(&l.Jump, d.Jump)
	s(&l.JumpRun, d.JumpRun)
	s(&l.Fall, d.Fall)
	s(&l.CrouchIdle, d.CrouchIdle)
	s(&l.CrouchWalk, d.CrouchWalk)
	if l.WalkSpeed <= 0 {
		l.WalkSpeed = d.WalkSpeed
	}
	if l.RunSpeed <= 0 {
		l.RunSpeed = d.RunSpeed
	}
	return l
}

// LocomotionState is what the body is doing, as ChooseLocomotion reads it.
type LocomotionState struct {
	// Speed is horizontal ground speed, world units a second.
	Speed float32
	// OnGround is whether the body is standing on something.
	OnGround bool
	// VerticalSpeed is the body's vertical velocity, + up.
	VerticalSpeed float32
	// AirTime is how long the body has been off the ground, seconds.
	AirTime float32
	// Crouched and Sprinting are the player's stances.
	Crouched, Sprinting bool
	// Jumped is whether this air time began with a jump (rather than walking
	// off an edge).
	Jumped bool
}

// Locomotion thresholds.
const (
	// LocomotionStillSpeed is the speed under which the body is standing.
	LocomotionStillSpeed = float32(0.35)
	// LocomotionFallDelay is how long the body may be off the ground without a
	// jump before it plays the fall: a step down off a curb is not a fall.
	LocomotionFallDelay = float32(0.25)
)

// LocomotionChoice is a clip and the playback rate it should run at.
type LocomotionChoice struct {
	Clip string
	Rate float32
	// Fallbacks are tried in order if Clip is not in the model.
	Fallbacks []string
}

// ChooseLocomotion picks the clip for a state of motion. l should be
// Effective already.
func ChooseLocomotion(l PlayerLocomotion, s LocomotionState) LocomotionChoice {
	if !s.OnGround {
		rising := s.VerticalSpeed > 0.5
		if s.Jumped && (rising || s.AirTime < 0.35) {
			if s.Speed > l.WalkSpeed*1.5 {
				return LocomotionChoice{Clip: l.JumpRun, Rate: 1, Fallbacks: []string{l.Jump}}
			}
			return LocomotionChoice{Clip: l.Jump, Rate: 1}
		}
		if s.Jumped || s.AirTime >= LocomotionFallDelay {
			return LocomotionChoice{Clip: l.Fall, Rate: 1, Fallbacks: []string{l.Jump, l.Idle}}
		}
		// Just off an edge: carry on with what the legs were doing.
	}
	if s.Crouched {
		if s.Speed < LocomotionStillSpeed {
			return LocomotionChoice{Clip: l.CrouchIdle, Rate: 1, Fallbacks: []string{l.Idle}}
		}
		return LocomotionChoice{Clip: l.CrouchWalk, Rate: clipRate(s.Speed, l.WalkSpeed*0.5),
			Fallbacks: []string{l.Walk}}
	}
	if s.Speed < LocomotionStillSpeed {
		return LocomotionChoice{Clip: l.Idle, Rate: 1}
	}
	// Walk until the speed is closer to the run's than to the walk's.
	runAt := (l.WalkSpeed + l.RunSpeed) / 2
	switch {
	case s.Sprinting && s.Speed >= runAt:
		return LocomotionChoice{Clip: l.Sprint, Rate: clipRate(s.Speed, l.RunSpeed*1.25),
			Fallbacks: []string{l.Run, l.Walk}}
	case s.Speed >= runAt:
		return LocomotionChoice{Clip: l.Run, Rate: clipRate(s.Speed, l.RunSpeed), Fallbacks: []string{l.Walk}}
	default:
		return LocomotionChoice{Clip: l.Walk, Rate: clipRate(s.Speed, l.WalkSpeed)}
	}
}

// clipRate scales a cycle to the speed it is moving at, within a range where
// a cycle still reads as itself.
func clipRate(speed, authored float32) float32 {
	if authored <= 0 {
		return 1
	}
	r := speed / authored
	if r < 0.6 {
		r = 0.6
	}
	if r > 1.6 {
		r = 1.6
	}
	return r
}

// RawPlayerBody is the YAML form of PlayerBody.
type RawPlayerBody struct {
	// Models are the body's attachments, as an entity type's `models:`. The
	// body's origin is the player's feet, facing −Z.
	Models []RawEntityModel `yaml:"models,omitempty"`
	// TurnRate is how fast the body turns to face its movement, degrees a
	// second. Default 600.
	TurnRate float32 `yaml:"turn_rate,omitempty"`
	// Locomotion names the clips motion picks between; every key defaults to
	// the `human` skeleton's clip of that name.
	Locomotion *RawPlayerLocomotion `yaml:"locomotion,omitempty"`
}

// RawPlayerLocomotion is the YAML form of PlayerLocomotion.
type RawPlayerLocomotion struct {
	Idle       string `yaml:"idle,omitempty"`        // default idle
	Walk       string `yaml:"walk,omitempty"`        // default walk
	Run        string `yaml:"run,omitempty"`         // default run
	Sprint     string `yaml:"sprint,omitempty"`      // default run
	Jump       string `yaml:"jump,omitempty"`        // default jump_up
	JumpRun    string `yaml:"jump_run,omitempty"`    // default jump_run: a jump taken at a run
	Fall       string `yaml:"fall,omitempty"`        // default jet_fall
	CrouchIdle string `yaml:"crouch_idle,omitempty"` // default crouch_idle
	CrouchWalk string `yaml:"crouch_walk,omitempty"` // default crouch_walk
	// WalkSpeed and RunSpeed are the ground speeds (units a second) the walk
	// and run clips were authored at, so playback can match the real speed.
	// Defaults 1.8 and 4.95: the human clips at a 1.5 scale.
	WalkSpeed float32 `yaml:"walk_speed,omitempty"`
	RunSpeed  float32 `yaml:"run_speed,omitempty"`
}

// PlayerBodyFromRaw reads the block; nil is no body.
func PlayerBodyFromRaw(r *RawPlayerBody) PlayerBody {
	var b PlayerBody
	if r == nil {
		return b
	}
	for _, rm := range r.Models {
		b.Models = append(b.Models, entityModelFromRaw(rm))
	}
	b.TurnRate = r.TurnRate
	if l := r.Locomotion; l != nil {
		b.Locomotion = PlayerLocomotion{
			Idle: l.Idle, Walk: l.Walk, Run: l.Run, Sprint: l.Sprint,
			Jump: l.Jump, JumpRun: l.JumpRun, Fall: l.Fall,
			CrouchIdle: l.CrouchIdle, CrouchWalk: l.CrouchWalk,
			WalkSpeed: l.WalkSpeed, RunSpeed: l.RunSpeed,
		}
	}
	return b
}

// PlayerBodyToRaw writes the block back; nil for no body.
func PlayerBodyToRaw(b PlayerBody) *RawPlayerBody {
	if len(b.Models) == 0 && b.TurnRate == 0 && b.Locomotion == (PlayerLocomotion{}) {
		return nil
	}
	r := &RawPlayerBody{TurnRate: b.TurnRate}
	for i := range b.Models {
		r.Models = append(r.Models, entityModelToRaw(&b.Models[i]))
	}
	if l := b.Locomotion; l != (PlayerLocomotion{}) {
		r.Locomotion = &RawPlayerLocomotion{
			Idle: l.Idle, Walk: l.Walk, Run: l.Run, Sprint: l.Sprint,
			Jump: l.Jump, JumpRun: l.JumpRun, Fall: l.Fall,
			CrouchIdle: l.CrouchIdle, CrouchWalk: l.CrouchWalk,
			WalkSpeed: l.WalkSpeed, RunSpeed: l.RunSpeed,
		}
	}
	return r
}

// ---------- the runtime body ----------

// PlayerBodyEntity is the third-person body: an engine-owned entity, like
// PlayerEntity not in Level.Entities, carrying the level's body models. Its
// Position is the player's feet and Rotation.Y its facing, both written every
// frame by internal/sim; Visible is whether it is drawn at all — only while a
// third-person camera is in play — which every world pass reads through
// render's entity walk (render.drawnEntity). Nil when nothing has installed it.
var PlayerBodyEntity *Entity

// PlayerBodyID is the body entity's ID, for the animation events it posts.
const PlayerBodyID = "player_body"

// PlayerThirdPerson is whether the frame's camera is the third-person boom —
// set by internal/sim's camera placement, read by the viewmodel pass (which
// draws nothing while it is) and anything else that cares whether the camera
// is the player's eye.
var PlayerThirdPerson bool

// DrawnPlayerBody is the body when it should be drawn this frame, else nil.
func DrawnPlayerBody() *Entity {
	b := PlayerBodyEntity
	if b == nil || !b.Visible || len(b.Models) == 0 {
		return nil
	}
	return b
}

// NewPlayerBody is an empty, hidden body entity. It carries no bounds on
// purpose: an entity without them is never culled, and one character is not
// worth a box that would have to follow whatever scale its models are at.
func NewPlayerBody() *Entity {
	return &Entity{ID: PlayerBodyID, Scale: mgl32.Vec3{1, 1, 1}}
}
