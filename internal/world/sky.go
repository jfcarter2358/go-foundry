package world

// sky.go — the level's sky.
//
// Level-scoped and optional, exactly like Fog beside it: at most one, no ID, no
// position, and a level with no `sky:` block gets what it always got — the flat
// clear colour behind the geometry. That is what keeps every existing level
// looking the way it did.
//
// Two modes behind one type rather than two:
//
//   Cubemap is the skybox proper — six textures, sampled by view direction.
//
//   Gradient is three colours up the dome and no art at all. It is not a
//   fallback for a cubemap that failed to load (though it is also that); it is
//   the mode most levels want, because "the horizon should stop being a flat
//   grey wall" is a lighting decision, not an art request, and because fog only
//   looks right against a sky that shares its colour — see Fog.SkyBlend.

import "github.com/go-gl/mathgl/mgl32"

// SkyKind is what the sky is made of. The values are the skyKind uniform
// sky_frag.glsl switches on and must match it.
type SkyKind int32

const (
	SkyGradient SkyKind = 0
	SkyCubemap  SkyKind = 1
)

// SkyKindName and SkyKindFromName are the YAML spelling, a name rather than the
// integer for the reason FogModeName is.
func SkyKindName(k SkyKind) string {
	if k == SkyCubemap {
		return "cubemap"
	}
	return "gradient"
}

func SkyKindFromName(s string) SkyKind {
	if s == "cubemap" {
		return SkyCubemap
	}
	return SkyGradient
}

// SkyKindNames is every kind in the order the editor offers them.
var SkyKindNames = []string{"gradient", "cubemap"}

// SkyFaces is how many textures a cubemap takes, and SkyFaceOrder/SkyFaceSuffix
// name them.
//
// The order is GL's own — POSITIVE_X, NEGATIVE_X, POSITIVE_Y, NEGATIVE_Y,
// POSITIVE_Z, NEGATIVE_Z — so the slice index IS the face offset the upload
// adds to GL_TEXTURE_CUBE_MAP_POSITIVE_X, with no mapping table in between to
// get backwards.
const SkyFaces = 6

// SkyFaceOrder is what the editor labels each slot, in the engine's Y-up,
// +X-is-east convention.
var SkyFaceOrder = [SkyFaces]string{"+X (east)", "-X (west)", "+Y (up)", "-Y (down)", "+Z (south)", "-Z (north)"}

// SkyFaceSuffix is the naming convention SetFacesFromBase fills the six slots
// with. It is a convenience, not a resolution rule: what a level FILE holds is
// six texture names spelled out, which is what lets the packer (cmd/foundrypack)
// see them. A base name would name nothing it could index.
var SkyFaceSuffix = [SkyFaces]string{"px", "nx", "py", "ny", "pz", "nz"}

// Sky is a level's sky.
type Sky struct {
	Kind SkyKind
	// Faces are the cubemap's six texture names, in SkyFaceOrder. Kept when the
	// kind is switched to gradient, so flipping between the two to compare them
	// does not cost the art.
	Faces [SkyFaces]string

	// Gradient stops: straight up, the ring around the viewer, and straight
	// down. Ground is seen wherever the level has no floor under the camera —
	// over a cliff edge, or from a bridge.
	Zenith  mgl32.Vec3
	Horizon mgl32.Vec3
	Ground  mgl32.Vec3

	// Tint multiplies whichever mode produced the colour and Intensity scales
	// it, so one set of sky art can serve a level at several times of day.
	Tint      mgl32.Vec3
	Intensity float32

	// Rotation turns the sky about +Y, in degrees. Which way a cubemap's art
	// faces is a property of the art, and this is how it is lined up with the
	// level's own sun without repainting it.
	Rotation float32

	// Clouds is the volumetric cloud deck, or nil for a clear sky — see
	// cloud.go. Drawn by the volumetric pass rather than by sky_frag.glsl,
	// because a deck is at an altitude rather than at infinity: a mountain has
	// to be able to push up through it.
	Clouds *SkyClouds

	// TransitionFrom, TransitionSeconds and TransitionElapsed are runtime-only
	// — never touched by SkyFromRaw/SkyToRaw — so a level file never carries a
	// transition mid-flight and a fresh load is always fully eased in.
	//
	// TransitionFrom is the complete sky state a script's ease_sky (see
	// SCRIPTING.md and BeginTransition below) eased away from; nil means this
	// sky is not mid-transition, which is the state a settled sky and a level
	// with no sky-changing script both spend nearly all their time in. Every
	// field above it — kind, faces, colours, tint, all of it — can differ
	// between it and the sky it sits on, which is what lets a script blend a
	// cubemap into a gradient, or a bright noon into a tinted dusk cubemap, in
	// one call rather than needing a special case for which fields changed.
	TransitionFrom    *Sky
	TransitionSeconds float32
	TransitionElapsed float32
}

// DefaultSky is a clear daylight gradient — what a level gets the moment an
// author adds a sky, with no art and nothing to configure.
func DefaultSky() Sky {
	return Sky{
		Kind:      SkyGradient,
		Zenith:    mgl32.Vec3{0.25, 0.45, 0.85},
		Horizon:   mgl32.Vec3{0.72, 0.82, 0.92},
		Ground:    mgl32.Vec3{0.18, 0.17, 0.16},
		Tint:      mgl32.Vec3{1, 1, 1},
		Intensity: 1,
	}
}

// Clamp puts every field back inside the range sky_frag.glsl assumes. Colours
// are NOT clamped to 1: a sky brighter than white is how an overexposed horizon
// is authored, and Intensity is the same knob from the other side.
func (s *Sky) Clamp() {
	if s == nil {
		return
	}
	if s.Kind != SkyCubemap {
		s.Kind = SkyGradient
	}
	s.Intensity = clampF(s.Intensity, 0, 16)
	for i := 0; i < 3; i++ {
		s.Zenith[i] = clampF(s.Zenith[i], 0, 16)
		s.Horizon[i] = clampF(s.Horizon[i], 0, 16)
		s.Ground[i] = clampF(s.Ground[i], 0, 16)
		s.Tint[i] = clampF(s.Tint[i], 0, 16)
	}
	s.Clouds.Clamp()
}

// BeginTransition arms an ease: from this call until `seconds` have ticked
// past (see SkyUpdate), RenderSky blends FROM the sky exactly as it looks
// right now TOWARD whatever sky_* properties a script writes next — see
// ease_sky in SCRIPTING.md, which is the only thing that calls this.
//
// A duration of 0 or less cancels rather than arming: whatever transition was
// already running completes instantly, which is the same "not mid-transition"
// state a sky that has never eased is in, and is a saner reading of
// `ease_sky(0)` than a divide by zero.
//
// Calling this again while a transition is already in flight does not splice
// the two — the new ease starts from the state the FIRST one was heading
// toward, not from wherever the blend had actually reached, so retriggering
// faster than a transition finishes costs one visible snap rather than a
// second field of interpolation math to keep in step with sky_frag.glsl's
// own. A script easing every few seconds never notices; one hammering
// ease_sky every tick would, which is not a use this is for.
func (s *Sky) BeginTransition(seconds float32) {
	if s == nil {
		return
	}
	if seconds <= 0 {
		s.TransitionFrom = nil
		return
	}
	from := *s
	from.TransitionFrom = nil
	// The deck is a pointer, and the writes that follow an ease_sky change it
	// in place (skySet in script_props_level.go). A snapshot sharing it would
	// ease from the destination to the destination.
	if s.Clouds != nil {
		c := *s.Clouds
		from.Clouds = &c
	}
	s.TransitionFrom = &from
	s.TransitionSeconds = seconds
	s.TransitionElapsed = 0
}

// TransitionBlend is how far into an eased transition this sky has ticked: 0
// the instant BeginTransition ran, 1 once it has settled. Sky states with no
// transition running report 1 — fully at rest on the one state there is —
// which is what lets RenderSky ask for this unconditionally rather than
// nil-checking TransitionFrom itself.
func (s *Sky) TransitionBlend() float32 {
	if s == nil || s.TransitionFrom == nil || s.TransitionSeconds <= 0 {
		return 1
	}
	return clampF(s.TransitionElapsed/s.TransitionSeconds, 0, 1)
}

// SkyUpdate advances the level's sky transition by dt, the same way
// EmitterUpdate and SoundUpdate advance theirs — RenderSky only ever reads the
// sky, so something has to be what makes time actually pass for it. A no-op
// on a level with no sky or no transition in flight.
//
// playing is the same gate EmitterUpdate and SoundUpdate take: only a
// running script can ever start a transition (ScriptTick shares the gate), but
// one already in flight should still hold rather than keep easing while the
// editor has stepped in mid-play, the same way a moving chunk or an emitter
// does.
func SkyUpdate(lvl *Level, dt float32, playing bool) {
	if lvl == nil || lvl.Sky == nil || lvl.Sky.TransitionFrom == nil || !playing {
		return
	}
	s := lvl.Sky
	s.TransitionElapsed += dt
	if s.TransitionElapsed >= s.TransitionSeconds {
		// Settled: drop the snapshot so every frame after this one draws the
		// plain single-state sky again — one cubemap sample instead of two —
		// rather than sitting at blend 1 forever paying for a mix that always
		// resolves to its second half.
		s.TransitionFrom = nil
	}
}

// HasCubemap reports whether this sky can actually be drawn as one: the kind
// asks for it AND all six faces are named. Five faces is not a cubemap, and the
// renderer falls back to the gradient rather than binding an incomplete texture.
func (s *Sky) HasCubemap() bool {
	if s == nil || s.Kind != SkyCubemap {
		return false
	}
	for _, f := range s.Faces {
		if f == "" {
			return false
		}
	}
	return true
}

// SetFacesFromBase fills the six slots from one name using SkyFaceSuffix —
// "dusk" becomes dusk_px, dusk_nx, ... An empty base clears them.
func (s *Sky) SetFacesFromBase(base string) {
	for i := range s.Faces {
		if base == "" {
			s.Faces[i] = ""
			continue
		}
		s.Faces[i] = base + "_" + SkyFaceSuffix[i]
	}
}

// HorizonColor is the colour the sky meets the ground at: the gradient's own
// horizon stop, tinted. It is what an unfogged level clears to and what the
// editor offers as the fog colour, so the two sides of the horizon start out
// agreeing. A cubemap has no single such colour, so it answers with the tint
// itself rather than pretending to sample one.
func (s *Sky) HorizonColor() mgl32.Vec3 {
	if s == nil {
		return mgl32.Vec3{}
	}
	base := s.Horizon
	if s.Kind == SkyCubemap {
		base = mgl32.Vec3{1, 1, 1}
	}
	return mgl32.Vec3{
		base.X() * s.Tint.X() * s.Intensity,
		base.Y() * s.Tint.Y() * s.Intensity,
		base.Z() * s.Tint.Z() * s.Intensity,
	}
}

// ---------- YAML ----------

// SkyFromRaw reads the optional sky block; nil in, nil out, for the reason
// FogFromRaw gives.
func SkyFromRaw(r *RawSky) *Sky {
	if r == nil {
		return nil
	}
	s := DefaultSky()
	s.Kind = SkyKindFromName(r.Kind)
	for i := 0; i < SkyFaces && i < len(r.Faces); i++ {
		s.Faces[i] = r.Faces[i]
	}
	if r.Zenith != nil {
		s.Zenith = mgl32.Vec3{r.Zenith.R, r.Zenith.G, r.Zenith.B}
	}
	if r.Horizon != nil {
		s.Horizon = mgl32.Vec3{r.Horizon.R, r.Horizon.G, r.Horizon.B}
	}
	if r.Ground != nil {
		s.Ground = mgl32.Vec3{r.Ground.R, r.Ground.G, r.Ground.B}
	}
	if r.Tint != nil {
		s.Tint = mgl32.Vec3{r.Tint.R, r.Tint.G, r.Tint.B}
	}
	if r.Intensity != nil {
		s.Intensity = *r.Intensity
	}
	s.Rotation = r.Rotation
	s.Clouds = SkyCloudsFromRaw(r.Clouds)
	s.Clamp()
	return &s
}

// SkyToRaw writes the block back. The six faces go out as a list only when the
// sky actually names any, so a gradient sky does not carry six empty strings
// around, and the gradient stops go out always — see FogToRaw's note on why
// these are not "absent means default" keys.
func SkyToRaw(s *Sky) *RawSky {
	if s == nil {
		return nil
	}
	c := *s
	c.Clamp()
	var faces []string
	for _, f := range c.Faces {
		if f != "" {
			faces = append([]string(nil), c.Faces[:]...)
			break
		}
	}
	intensity := c.Intensity
	return &RawSky{
		Kind:      SkyKindName(c.Kind),
		Faces:     faces,
		Zenith:    &RawColor{R: c.Zenith.X(), G: c.Zenith.Y(), B: c.Zenith.Z()},
		Horizon:   &RawColor{R: c.Horizon.X(), G: c.Horizon.Y(), B: c.Horizon.Z()},
		Ground:    &RawColor{R: c.Ground.X(), G: c.Ground.Y(), B: c.Ground.Z()},
		Tint:      &RawColor{R: c.Tint.X(), G: c.Tint.Y(), B: c.Tint.Z()},
		Intensity: &intensity,
		Rotation:  c.Rotation,
		Clouds:    SkyCloudsToRaw(c.Clouds),
	}
}
