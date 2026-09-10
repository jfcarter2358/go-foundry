package world

// waypoint.go — waypoint animations: a sequence of placed poses a dynamic brush
// can be sent through, each reached at its own moment.
//
// # What one is
//
// A WaypointAnimation is level content: an ID, a length in seconds, and an
// ordered list of points, each a position, a rotation, a scale and a time. The
// editor draws every point as an arrow joined to the next by a line, and lays
// the points out as keyframes on a timeline; nothing draws them during play and
// nothing collides with them. A script hands one to a dynamic brush with
// `play_waypoints`, and the brush travels through the points in order, arriving
// at each at its time, until the length has run out (brush_waypoints.go).
//
// # What a point's numbers mean for the brush
//
//   - Position is WORLD space, and it is where the brush's centre goes — the
//     point Brush.Position reports and Rotation turns about. The animation is
//     placed in the level, so that is the only space its arrows can be read
//     in; the brush's chunk transform (and a frame's, for a brush an entity
//     carries) is divided out every tick.
//   - Rotation and Scale are the brush's OWN placement channels, the numbers
//     `@brush.prop.rotation` and `@brush.prop.scale` read: a turn about the
//     brush's centre away from how it was authored, and a per-axis multiplier
//     on its authored size. For a brush whose chunk has not been turned — which
//     is every level brush unless somebody turned the chunk — that is exactly
//     the orientation the arrow shows.
//   - Time is when the brush ARRIVES at the point, in seconds from being sent.
//     The first point's time is therefore how long the brush takes to reach it
//     from wherever it was — the leg that belongs to no point — and a first
//     point at 0 starts the path on it. Times never run backwards along the
//     list: the list is the path, and no place on it can be reached before the
//     place ahead of it.
//
// # The length
//
// Length is how long the whole animation takes. It may run past the last
// point's time, and the brush holds the last point for the rest: the end is when
// the length runs out, not when the last point is reached, which is what lets a
// door wait open before the script hears it has finished. A length shorter than
// the last point's time reads as that time (EffectiveLength), so an animation
// never ends before it has arrived.
//
// Changing the length stretches every point's time with it (SetLength). "This
// should take four seconds, not two" is one edit that keeps the path's rhythm;
// moving one point's time is the other edit, and it moves only that point.
//
// # How a leg is travelled
//
// Position, rotation and scale are interpolated linearly by the fraction of the
// leg's time that has passed, so a brush that turns while it moves finishes the
// turn as it arrives rather than snapping to the point's orientation at the end.
// Two points at the same time make the leg between them instantaneous.
//
// The rotation is interpolated as the three numbers it is, not along the
// shortest arc: 0° to 270° is three quarters of a turn, and 0° to 720° is two
// whole ones. That is what makes a spinning platform expressible at all, and it
// keeps what the brush does readable straight off the panel.
//
// # Events
//
// A point can also carry events: atom names, each posted to the brush as
// `(:atom:, animation ref)` the moment it reaches that point, after the
// engine's own `:waypoint:`. That is what lets a script react to "the lift is at
// the top floor" by name rather than by counting indices, and keeps working when
// a point is inserted in front of it.

import (
	"sort"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// MinBrushScale is the smallest any scale axis may be. A brush scaled to
// nothing has a singular placement, which a raycast cannot invert and a
// collision hull has no depth in; a negative one turns the brush inside out,
// and the rasteriser's back-face cull would then draw its inside.
const MinBrushScale = float32(0.01)

// Waypoint is one stop of a WaypointAnimation. See the file comment for what
// each field means to the brush that reaches it.
type Waypoint struct {
	// Position is where the brush's centre goes, in world space.
	Position mgl32.Vec3
	// Rotation is the brush's own Euler rotation there, in radians.
	Rotation mgl32.Vec3
	// Scale multiplies the brush's authored size per axis. The zero value
	// reads as {1,1,1} — see EffectiveScale.
	Scale mgl32.Vec3
	// Time is when the brush arrives here, in seconds from being sent. Never
	// earlier than the point before it — see NormalizeTimes.
	Time float32
	// Events are the atoms posted to the brush when it arrives here, in this
	// order, each without its colons and each already a name flang can match
	// (NormalizeWaypointEventAtom).
	Events []string
}

// NewWaypoint returns a point at pos with the defaults a freshly placed one
// should have: no turn, no scale, and reached the moment the brush is sent.
func NewWaypoint(pos mgl32.Vec3) Waypoint {
	return Waypoint{Position: pos, Scale: mgl32.Vec3{1, 1, 1}}
}

// EffectiveScale is Scale with its zero value read as no scale at all.
func (w *Waypoint) EffectiveScale() mgl32.Vec3 { return effectiveScale3(w.Scale) }

// Pose is the point as a pose: where a brush arriving at it will be.
func (w *Waypoint) Pose() WaypointPose {
	return WaypointPose{Position: w.Position, Rotation: w.Rotation, Scale: w.EffectiveScale()}
}

// Matrix is the point's own transform — translate, rotate, scale, in the order
// Brush.Placement composes them — which is what the editor's arrow is drawn
// through, so the arrow turns and stretches the way the brush will.
func (w *Waypoint) Matrix() mgl32.Mat4 {
	s := w.EffectiveScale()
	return mgl32.Translate3D(w.Position.X(), w.Position.Y(), w.Position.Z()).
		Mul4(EulerMatrixYXZ(w.Rotation)).
		Mul4(mgl32.Scale3D(s.X(), s.Y(), s.Z()))
}

// WaypointAnimation is a named sequence of points a dynamic brush can travel.
type WaypointAnimation struct {
	// ID is unique across the level, like every other addressable object's,
	// and is what `play_waypoints(@door, @door_open)` names.
	ID string
	// Length is how long the animation takes, in seconds. See EffectiveLength.
	Length float32
	Points []Waypoint
}

// Clone returns an independent copy. The point list, and each point's event
// list, are the only things a shallow copy would share.
func (a *WaypointAnimation) Clone() WaypointAnimation {
	cp := WaypointAnimation{ID: a.ID, Length: a.Length, Points: append([]Waypoint(nil), a.Points...)}
	for i := range cp.Points {
		cp.Points[i].Events = cloneWaypointEvents(cp.Points[i].Events)
	}
	return cp
}

// cloneWaypointEvents copies an event list, keeping an empty one nil so a
// point with no events compares equal to one that never had any.
func cloneWaypointEvents(events []string) []string {
	if len(events) == 0 {
		return nil
	}
	return append([]string(nil), events...)
}

// NormalizeWaypointEventAtom reads what an author wrote as an event name as the
// atom it will be posted as: surrounding spaces and colons dropped, so
// `:door_open:` and `door_open` are the same event, and lowercased. ok is false
// for anything left that is not flang's `[a-z_]+` — an atom nobody could write
// a handler for.
func NormalizeWaypointEventAtom(s string) (atom string, ok bool) {
	atom = strings.ToLower(strings.Trim(s, " \t:"))
	if atom == "" {
		return "", false
	}
	for i := 0; i < len(atom); i++ {
		if c := atom[i]; !(c >= 'a' && c <= 'z' || c == '_') {
			return atom, false
		}
	}
	return atom, true
}

// WaypointEventAtoms lists every event atom any point in the level carries,
// sorted and without repeats — the custom atoms this level's waypoint
// animations can post.
func WaypointEventAtoms(lvl *Level) []string {
	if lvl == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for ai := range lvl.WaypointAnimations {
		for _, p := range lvl.WaypointAnimations[ai].Points {
			for _, atom := range p.Events {
				if !seen[atom] {
					seen[atom] = true
					out = append(out, atom)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// ---------- timing ----------

// EffectiveLength is how long the animation really takes: Length, stretched to
// the last point's time where it falls short of it.
func (a *WaypointAnimation) EffectiveLength() float32 {
	l := a.Length
	if n := len(a.Points); n > 0 && a.Points[n-1].Time > l {
		l = a.Points[n-1].Time
	}
	if l < 0 {
		return 0
	}
	return l
}

// NormalizeTimes restores what every reader of the times relies on: none below
// zero, none earlier than the point before, and a length that reaches the last.
// A level file is text anyone can edit, so the loader cannot assume it.
func (a *WaypointAnimation) NormalizeTimes() {
	prev := float32(0)
	for i := range a.Points {
		if a.Points[i].Time < prev {
			a.Points[i].Time = prev
		}
		prev = a.Points[i].Time
	}
	a.Length = a.EffectiveLength()
}

// SetLength changes how long the animation takes, stretching every point's time
// by the same factor so the path keeps its rhythm.
//
// An animation that took no time at all has no rhythm to keep, so its points
// are spread evenly across the new length instead — the first at the start and
// the last at the end, or a lone point at the end, so the brush travels to it.
func (a *WaypointAnimation) SetLength(l float32) {
	if l < 0 {
		l = 0
	}
	old := a.EffectiveLength()
	n := len(a.Points)
	for i := range a.Points {
		t := &a.Points[i].Time
		switch {
		case old > 0:
			*t *= l / old
		case n == 1:
			*t = l
		default:
			*t = l * float32(i) / float32(n-1)
		}
		// Float rounding must not leave a point a hair past the end, where
		// EffectiveLength would read it as a longer animation.
		if *t > l {
			*t = l
		}
	}
	a.Length = l
}

// SetPointTime moves point i to time t and returns the time it was given: held
// between its neighbours' times, since the order of the list is the order of
// the path, and at or above zero. A last point moved past the length stretches
// the length to it.
func (a *WaypointAnimation) SetPointTime(i int, t float32) float32 {
	if i < 0 || i >= len(a.Points) {
		return 0
	}
	lo := float32(0)
	if i > 0 {
		lo = a.Points[i-1].Time
	}
	if t < lo {
		t = lo
	}
	if i+1 < len(a.Points) && t > a.Points[i+1].Time {
		t = a.Points[i+1].Time
	}
	a.Points[i].Time = t
	if t > a.Length {
		a.Length = t
	}
	return t
}

// PoseAt is where a brush sent along the animation from start is t seconds
// later: on the leg whose span of time holds t, by the fraction of that span
// gone, or on the last point once it has been reached.
//
// The one pose function play and the editor share. The engine's step asks it
// for the time the brush has travelled; the editor's timeline asks it for the
// playhead's.
func (a *WaypointAnimation) PoseAt(start WaypointPose, t float32) WaypointPose {
	if t < 0 {
		t = 0
	}
	from, fromT := start, float32(0)
	for i := range a.Points {
		p := &a.Points[i]
		if t < p.Time {
			// fromT <= t < p.Time, so the span is never zero here: a leg of no
			// time has already been passed through by the loop.
			return LerpWaypointPose(from, p.Pose(), (t-fromT)/(p.Time-fromT))
		}
		from, fromT = p.Pose(), p.Time
	}
	return from
}

// WaypointPose is where a brush is, in the terms a waypoint is written in: its
// centre in world space, and its own rotation and scale.
type WaypointPose struct {
	Position mgl32.Vec3
	Rotation mgl32.Vec3
	Scale    mgl32.Vec3
}

// LerpWaypointPose is the pose t of the way from a to b. Every channel moves by
// the same fraction, which is what makes a brush finish its turn as it arrives.
func LerpWaypointPose(a, b WaypointPose, t float32) WaypointPose {
	lerp := func(x, y mgl32.Vec3) mgl32.Vec3 { return x.Add(y.Sub(x).Mul(t)) }
	return WaypointPose{
		Position: lerp(a.Position, b.Position),
		Rotation: lerp(a.Rotation, b.Rotation),
		Scale:    lerp(a.Scale, b.Scale),
	}
}

// ClampBrushScale keeps every axis of s at or above MinBrushScale, for
// every writer of a scale a brush will be given.
func ClampBrushScale(s mgl32.Vec3) mgl32.Vec3 {
	for i := range s {
		if s[i] < MinBrushScale {
			s[i] = MinBrushScale
		}
	}
	return s
}

// effectiveScale3 reads a zero scale vector as the identity — the convention
// Brush.Scale and Waypoint.Scale share, so that a value nobody set means
// unscaled rather than collapsed to a point.
func effectiveScale3(s mgl32.Vec3) mgl32.Vec3 {
	if s == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return s
}

// ---------- lookup ----------

// FindWaypointAnimation returns the animation with the given ID, or nil.
func FindWaypointAnimation(lvl *Level, id string) *WaypointAnimation {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.WaypointAnimations {
		if lvl.WaypointAnimations[i].ID == id {
			return &lvl.WaypointAnimations[i]
		}
	}
	return nil
}

// UniqueWaypointAnimationID mints an ID free across the whole level.
func UniqueWaypointAnimationID(lvl *Level, base string) string {
	if base == "" {
		base = "waypoints"
	}
	return uniqueLevelID(lvl, base)
}

// ---------- serialization ----------

// WaypointAnimationFromRaw builds an animation from its level-file form, with
// its times put in order (NormalizeTimes).
func WaypointAnimationFromRaw(r RawWaypointAnimation) WaypointAnimation {
	a := WaypointAnimation{ID: r.ID, Length: r.Length, Points: make([]Waypoint, 0, len(r.Points))}
	for _, rp := range r.Points {
		w := Waypoint{
			Position: mgl32.Vec3{rp.Position.X, rp.Position.Y, rp.Position.Z},
			Rotation: mgl32.Vec3{rp.Rotation.X, rp.Rotation.Y, rp.Rotation.Z},
			Scale:    mgl32.Vec3{1, 1, 1},
			Time:     rp.Time,
			Events:   waypointEventsFromRaw(rp.Events),
		}
		if rp.Scale != nil {
			w.Scale = ClampBrushScale(mgl32.Vec3{rp.Scale.X, rp.Scale.Y, rp.Scale.Z})
		}
		a.Points = append(a.Points, w)
	}
	a.NormalizeTimes()
	return a
}

// waypointEventsFromRaw reads a point's event list as the editor would have
// written it: each name normalised, so a hand-edited `":door_open:"` is the
// same event, with repeats and names no handler could match dropped.
func waypointEventsFromRaw(raw []string) []string {
	var out []string
	for _, s := range raw {
		atom, ok := NormalizeWaypointEventAtom(s)
		if !ok || containsWaypointEvent(out, atom) {
			continue
		}
		out = append(out, atom)
	}
	return out
}

// containsWaypointEvent reports whether an event list already has atom.
func containsWaypointEvent(events []string, atom string) bool {
	for _, e := range events {
		if e == atom {
			return true
		}
	}
	return false
}

// WaypointAnimationToRaw writes an animation back out. A point's scale is
// omitted at its default, its time at 0 (which is what absent reads as), and its
// events when it has none, so a path laid out at the start is a list of
// positions and nothing else.
func WaypointAnimationToRaw(a *WaypointAnimation) RawWaypointAnimation {
	out := RawWaypointAnimation{ID: a.ID, Length: a.Length, Points: make([]RawWaypoint, 0, len(a.Points))}
	for i := range a.Points {
		w := &a.Points[i]
		rp := RawWaypoint{
			Position: RawVec3{X: w.Position.X(), Y: w.Position.Y(), Z: w.Position.Z()},
			Rotation: RawVec3{X: w.Rotation.X(), Y: w.Rotation.Y(), Z: w.Rotation.Z()},
			Time:     w.Time,
			Events:   cloneWaypointEvents(w.Events),
		}
		if s := w.EffectiveScale(); s != (mgl32.Vec3{1, 1, 1}) {
			rp.Scale = &RawVec3{X: s.X(), Y: s.Y(), Z: s.Z()}
		}
		out.Points = append(out.Points, rp)
	}
	return out
}
