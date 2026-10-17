package world

// fog_shafts.go — light shafts: the sun's light scattered by dusty air near
// the camera, SHADOWED, so a slot canyon's sunlit patches and a cave mouth
// stream visible beams (docs/adventure/PHASE_B.md).
//
// The atmosphere (fog_atmosphere.go) is integrated in closed form and is
// unshadowed: haze in a canyon's shade glows as brightly as haze in the sun.
// Shafts are the part of the air that is marched instead — render/shafts.go
// takes Steps jittered samples from the eye to the first surface (at most
// Distance away), each lit by the sun through the shadow map (the virtual
// shadow map, or the cascades) and the clouds' shadow, plus a little of the
// sky's ambient (cut by ambient volumes, volume_ambient.go), and composites
// the light added and the light taken away over the frame.
//
// The medium is Density per metre at Base, thinning by e every 1/Falloff
// metres up (0: the same everywhere), plus each ambient volume's Dust inside
// it. Off unless a level's `fog:` block has a `shafts:` block, and
// FOUNDRY_SHAFTS=0 switches the pass off whatever the level says.

import (
	"github.com/go-gl/mathgl/mgl32"
)

// FogShafts is a level's light shafts.
type FogShafts struct {
	Enabled bool
	// Density is extinction per metre at Base: 0.01 takes away 63% over
	// 100 m, which is a dusty canyon.
	Density float32
	// Distance is how far from the eye the march reaches, in metres.
	Distance float32
	// Steps is the samples a ray takes (8–64).
	Steps int
	// Color tints the light the dust scatters (white: the sun's own).
	Color mgl32.Vec3
	// Base and Falloff shape the density with height: thinning by e every
	// 1/Falloff metres above Base. Falloff 0 is uniform.
	Base, Falloff float32
	// Anisotropy is the Henyey-Greenstein g of the scattering: toward 1 the
	// beams glow looking toward the sun and fade looking away.
	Anisotropy float32
	// Strength multiplies the sunlight scattered; Ambient the sky light
	// (the level's ambient) the dust scatters, which keeps shaded dust from
	// reading as black smoke.
	Strength, Ambient float32
}

// Light-shaft defaults and limits.
const (
	FogShaftsDefaultDensity    float32 = 0.01
	FogShaftsDefaultDistance   float32 = 150
	FogShaftsDefaultSteps              = 24
	FogShaftsDefaultAnisotropy float32 = 0.5
	FogShaftsDefaultAmbient    float32 = 0.5
	FogShaftsMinSteps                  = 4
	FogShaftsMaxSteps                  = 64
)

// DefaultFogShafts is a switched-on dusty air.
func DefaultFogShafts() FogShafts {
	return FogShafts{
		Enabled:    true,
		Density:    FogShaftsDefaultDensity,
		Distance:   FogShaftsDefaultDistance,
		Steps:      FogShaftsDefaultSteps,
		Color:      mgl32.Vec3{1, 1, 1},
		Anisotropy: FogShaftsDefaultAnisotropy,
		Strength:   1,
		Ambient:    FogShaftsDefaultAmbient,
	}
}

// Clamp puts every field in the range the march assumes.
func (s *FogShafts) Clamp() {
	s.Density = clampF(s.Density, 0, 1)
	s.Distance = clampF(s.Distance, 1, 2000)
	if s.Steps < FogShaftsMinSteps {
		s.Steps = FogShaftsMinSteps
	}
	if s.Steps > FogShaftsMaxSteps {
		s.Steps = FogShaftsMaxSteps
	}
	for i := 0; i < 3; i++ {
		s.Color[i] = clampF(s.Color[i], 0, 4)
	}
	s.Base = clampF(s.Base, -1e5, 1e5)
	s.Falloff = clampF(s.Falloff, 0, 10)
	s.Anisotropy = clampF(s.Anisotropy, -0.95, 0.95)
	s.Strength = clampF(s.Strength, 0, 16)
	s.Ambient = clampF(s.Ambient, 0, 4)
}

// ShaftsOn reports whether the level asks for light shafts: a fog that is
// on, with its shafts on and some dust in the air.
func (f *Fog) ShaftsOn() bool {
	return f != nil && f.Enabled && f.Shafts.Enabled && f.Shafts.Density > 0
}

// RawFogShafts is the `fog: shafts:` block. Every number is a pointer: absent
// takes the default.
type RawFogShafts struct {
	// Disabled keeps the numbers with the shafts switched off.
	Disabled bool `yaml:"disabled,omitempty"`
	// Density is extinction per metre at base; default 0.01 (63% over 100 m).
	Density *float32 `yaml:"density,omitempty"`
	// Distance is how far from the eye the march reaches, metres; default 150.
	Distance *float32 `yaml:"distance,omitempty"`
	// Steps is the samples a ray takes, 4 to 64; default 24.
	Steps *int `yaml:"steps,omitempty"`
	// Color tints the light the dust scatters; default white.
	Color *RawColor `yaml:"color,omitempty"`
	// Base is the height density is measured at; default 0.
	Base float32 `yaml:"base,omitempty"`
	// Falloff thins the dust by e every 1/falloff metres above base; 0
	// (default) is uniform.
	Falloff *float32 `yaml:"falloff,omitempty"`
	// Anisotropy is the scattering's Henyey-Greenstein g; default 0.6.
	Anisotropy *float32 `yaml:"anisotropy,omitempty"`
	// Strength multiplies the scattered sunlight; default 1.
	Strength *float32 `yaml:"strength,omitempty"`
	// Ambient is how much sky light the dust scatters; default 0.5.
	Ambient *float32 `yaml:"ambient,omitempty"`
}

// shaftsFromRaw reads the block into f.Shafts (off when absent).
func (f *Fog) shaftsFromRaw(r *RawFog) {
	f.Shafts = DefaultFogShafts()
	f.Shafts.Enabled = false
	sr := r.Shafts
	if sr == nil {
		return
	}
	s := &f.Shafts
	s.Enabled = !sr.Disabled
	if sr.Density != nil {
		s.Density = *sr.Density
	}
	if sr.Distance != nil {
		s.Distance = *sr.Distance
	}
	if sr.Steps != nil {
		s.Steps = *sr.Steps
	}
	if sr.Color != nil {
		s.Color = rawVec3(sr.Color)
	}
	s.Base = sr.Base
	if sr.Falloff != nil {
		s.Falloff = *sr.Falloff
	}
	if sr.Anisotropy != nil {
		s.Anisotropy = *sr.Anisotropy
	}
	if sr.Strength != nil {
		s.Strength = *sr.Strength
	}
	if sr.Ambient != nil {
		s.Ambient = *sr.Ambient
	}
}

// shaftsToRaw writes the block back, or nothing for shafts that are off and
// at their defaults — a level without them saves as it did.
func (f *Fog) shaftsToRaw(r *RawFog) {
	def := DefaultFogShafts()
	def.Enabled = false
	if !f.Shafts.Enabled && f.Shafts == def {
		return
	}
	s := f.Shafts
	steps := s.Steps
	r.Shafts = &RawFogShafts{
		Disabled:   !s.Enabled,
		Density:    ptrF(s.Density),
		Distance:   ptrF(s.Distance),
		Steps:      &steps,
		Color:      rawColorOf(s.Color),
		Base:       s.Base,
		Falloff:    ptrF(s.Falloff),
		Anisotropy: ptrF(s.Anisotropy),
		Strength:   ptrF(s.Strength),
		Ambient:    ptrF(s.Ambient),
	}
}
