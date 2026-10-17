package world

// fog_atmosphere.go — the air itself: height fog, aerial perspective and the
// sun's in-scattering (docs/canyon/PHASE_6.md).
//
// The distance fog in fog.go is a fade toward a colour by range. These are a
// medium: each has a density along the view ray that fog.glsl integrates in
// closed form (fogAtmosSegment), and what the eye gets is what survives of
// the surface plus what the air between adds. Three parts, each off unless a
// level's `fog:` block names it, so every level written before them renders
// exactly as it did:
//
//   - HEIGHT fog: dense at its base, thinning by e every 1/Falloff metres up.
//     Ground fog, haze lying in a canyon's floor.
//   - AERIAL perspective: haze that thickens with range — a 1/e of extinction
//     every Distance metres at its base, thinning upward over ScaleHeight —
//     with a per-channel Tint on the extinction, blue above red, so far rock
//     goes bluish before it goes pale, as Rayleigh scattering makes it.
//   - SUN in-scattering: a forward lobe around the sun's direction, coloured
//     by the sun (the level's first switched-on directional light), through
//     both media: haze looking into the sun glows.
//
// The sky takes the same integral along its rays (SkyHaze of it), out to
// SkyDistance, so a far cliff and the sky behind it end in the same air.
//
// A level that wants only the atmosphere, with no distance fade, sets the
// fog's mode to "none" (FogNone).

import (
	"math"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// FogHeight is exponential height fog.
type FogHeight struct {
	Enabled bool
	// Density is the extinction per metre at Base.
	Density float32
	// Base is the height Density is measured at. Below it the fog thickens
	// on the same curve.
	Base float32
	// Falloff is per metre: the density falls by e every 1/Falloff metres up.
	Falloff float32
	Color   mgl32.Vec3
}

// FogAerial is aerial perspective.
type FogAerial struct {
	Enabled bool
	// Distance is how far, at Base, the haze takes away 63% (1/e) of what is
	// behind it, in metres.
	Distance float32
	Color    mgl32.Vec3
	// Tint weighs the extinction per channel. Blue above red is Rayleigh's
	// tint; (1, 1, 1) is a grey, dust-only haze.
	Tint mgl32.Vec3
	// ScaleHeight is how far up the haze thins by e, in metres: a ray
	// climbing toward the zenith passes through far less of it than a level
	// one, which is what lets the sky keep its blue overhead.
	ScaleHeight float32
	Base        float32
}

// FogSun is the sun's in-scattering through the atmosphere.
type FogSun struct {
	Enabled bool
	// Strength scales the lobe: looking into the sun the haze brightens by
	// the sun's colour times Strength times (lobe − 1), looking away it dulls
	// by a little, and on average it is its own colour.
	Strength float32
	// Anisotropy is the lobe's Henyey-Greenstein g: 0 scatters evenly, near
	// 1 only straight on toward the sun.
	Anisotropy float32
	// Color multiplies the sun's own colour.
	Color mgl32.Vec3
}

// The atmosphere's defaults and ranges.
const (
	FogHeightDefaultDensity float32 = 0.02
	FogHeightDefaultFalloff float32 = 0.1

	FogAerialDefaultDistance    float32 = 2500
	FogAerialDefaultScaleHeight float32 = 1200
	FogAerialMinDistance        float32 = 1
	FogAerialMaxDistance        float32 = 1e6

	FogSunDefaultStrength   float32 = 0.05
	FogSunDefaultAnisotropy float32 = 0.7

	FogDefaultSkyHaze     float32 = 1
	FogDefaultSkyDistance float32 = 20000
)

// FogAerialDefaultTint is a mild Rayleigh tint: the blue channel's
// extinction a quarter above the green's, the red's a fifth below.
var FogAerialDefaultTint = mgl32.Vec3{0.8, 1, 1.25}

// DefaultFogHeight is height fog in fog colour c, switched on.
func DefaultFogHeight(c mgl32.Vec3) FogHeight {
	return FogHeight{Enabled: true, Density: FogHeightDefaultDensity, Falloff: FogHeightDefaultFalloff, Color: c}
}

// DefaultFogAerial is aerial perspective in haze colour c, switched on.
func DefaultFogAerial(c mgl32.Vec3) FogAerial {
	return FogAerial{Enabled: true, Distance: FogAerialDefaultDistance, Color: c,
		Tint: FogAerialDefaultTint, ScaleHeight: FogAerialDefaultScaleHeight}
}

// DefaultFogSun is the sun's lobe, switched on.
func DefaultFogSun() FogSun {
	return FogSun{Enabled: true, Strength: FogSunDefaultStrength, Anisotropy: FogSunDefaultAnisotropy,
		Color: mgl32.Vec3{1, 1, 1}}
}

// HasAtmosphere reports whether any part of the atmosphere is on: the
// shader's single fogAtmos switch.
func (f *Fog) HasAtmosphere() bool {
	return f != nil && f.Enabled && (f.Height.Enabled || f.Aerial.Enabled || f.Sun.Enabled)
}

// clampAtmosphere is Clamp's share for the atmosphere.
func (f *Fog) clampAtmosphere() {
	h := &f.Height
	h.Density = clampF(h.Density, 0, 10)
	h.Base = clampF(h.Base, -1e5, 1e5)
	h.Falloff = clampF(h.Falloff, 1e-4, 10)
	clampColor(&h.Color)

	a := &f.Aerial
	a.Distance = clampF(a.Distance, FogAerialMinDistance, FogAerialMaxDistance)
	a.ScaleHeight = clampF(a.ScaleHeight, 1, 1e6)
	a.Base = clampF(a.Base, -1e5, 1e5)
	clampColor(&a.Color)
	for i := 0; i < 3; i++ {
		a.Tint[i] = clampF(a.Tint[i], 0, 8)
	}

	s := &f.Sun
	s.Strength = clampF(s.Strength, 0, 16)
	s.Anisotropy = clampF(s.Anisotropy, -0.95, 0.95)
	for i := 0; i < 3; i++ {
		s.Color[i] = clampF(s.Color[i], 0, 4)
	}

	f.SkyHaze = clampF(f.SkyHaze, 0, 1)
	f.SkyDistance = clampF(f.SkyDistance, 1, 1e6)
}

func clampColor(c *mgl32.Vec3) {
	for i := 0; i < 3; i++ {
		c[i] = clampF(c[i], 0, 1)
	}
}

// ---------- YAML ----------

// atmosphereFromRaw reads the three blocks into f, whose Color is already the
// fog's: a part with no colour of its own takes it.
func (f *Fog) atmosphereFromRaw(r *RawFog) {
	f.SkyHaze, f.SkyDistance = FogDefaultSkyHaze, FogDefaultSkyDistance
	if r.SkyHaze != nil {
		f.SkyHaze = *r.SkyHaze
	}
	if r.SkyDistance != nil {
		f.SkyDistance = *r.SkyDistance
	}
	f.Height = DefaultFogHeight(f.Color)
	f.Height.Enabled = false
	if h := r.Height; h != nil {
		f.Height.Enabled = !h.Disabled
		if h.Density != nil {
			f.Height.Density = *h.Density
		}
		f.Height.Base = h.Base
		if h.Falloff != nil {
			f.Height.Falloff = *h.Falloff
		}
		if h.Color != nil {
			f.Height.Color = rawVec3(h.Color)
		}
	}
	f.Aerial = DefaultFogAerial(f.Color)
	f.Aerial.Enabled = false
	if a := r.Aerial; a != nil {
		f.Aerial.Enabled = !a.Disabled
		if a.Distance != nil {
			f.Aerial.Distance = *a.Distance
		}
		if a.Color != nil {
			f.Aerial.Color = rawVec3(a.Color)
		}
		if a.Tint != nil {
			f.Aerial.Tint = rawVec3(a.Tint)
		}
		if a.ScaleHeight != nil {
			f.Aerial.ScaleHeight = *a.ScaleHeight
		}
		f.Aerial.Base = a.Base
	}
	f.Sun = DefaultFogSun()
	f.Sun.Enabled = false
	if s := r.Sun; s != nil {
		f.Sun.Enabled = !s.Disabled
		if s.Strength != nil {
			f.Sun.Strength = *s.Strength
		}
		if s.Anisotropy != nil {
			f.Sun.Anisotropy = *s.Anisotropy
		}
		if s.Color != nil {
			f.Sun.Color = rawVec3(s.Color)
		}
	}
}

// atmosphereToRaw writes the blocks back: a part that is off and still at
// its defaults is left out, so a level that never had an atmosphere saves
// byte for byte as it did; one switched off with its numbers kept writes
// `disabled: true` and the numbers, as the fog itself does. A switched-off
// part's colour alone does not count: it follows the fog's on the way back
// in, and the fog's colour changing under it is not an edit to it.
func (f *Fog) atmosphereToRaw(r *RawFog) {
	hDef := withEnabled(DefaultFogHeight(f.Height.Color), false)
	aDef := withAerialEnabled(DefaultFogAerial(f.Aerial.Color), false)
	if f.Height.Enabled || f.Height != hDef {
		h := f.Height
		r.Height = &RawFogHeight{Disabled: !h.Enabled, Density: ptrF(h.Density), Base: h.Base,
			Falloff: ptrF(h.Falloff), Color: rawColorOf(h.Color)}
	}
	if f.Aerial.Enabled || f.Aerial != aDef {
		a := f.Aerial
		r.Aerial = &RawFogAerial{Disabled: !a.Enabled, Distance: ptrF(a.Distance), Color: rawColorOf(a.Color),
			Tint: rawColorOf(a.Tint), ScaleHeight: ptrF(a.ScaleHeight), Base: a.Base}
	}
	if f.Sun.Enabled || f.Sun != withSunEnabled(DefaultFogSun(), false) {
		s := f.Sun
		r.Sun = &RawFogSun{Disabled: !s.Enabled, Strength: ptrF(s.Strength), Anisotropy: ptrF(s.Anisotropy),
			Color: rawColorOf(s.Color)}
	}
	if r.Height != nil || r.Aerial != nil || r.Sun != nil ||
		f.SkyHaze != FogDefaultSkyHaze || f.SkyDistance != FogDefaultSkyDistance {
		r.SkyHaze, r.SkyDistance = ptrF(f.SkyHaze), ptrF(f.SkyDistance)
	}
}

func withEnabled(h FogHeight, on bool) FogHeight       { h.Enabled = on; return h }
func withAerialEnabled(a FogAerial, on bool) FogAerial { a.Enabled = on; return a }
func withSunEnabled(s FogSun, on bool) FogSun          { s.Enabled = on; return s }

func ptrF(v float32) *float32 { return &v }

func rawColorOf(c mgl32.Vec3) *RawColor { return &RawColor{R: c[0], G: c[1], B: c[2]} }

// ---------- GPU ----------

// fogSun is the light the in-scattering lobe is coloured by: the level's
// first switched-on directional light, as a unit vector toward it and its
// colour times its brightness. ok is false when there is none.
func fogSun(lvl *Level) (toSun, color mgl32.Vec3, ok bool) {
	if lvl == nil {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	for _, dl := range lvl.Lights.Directional {
		b := dl.EffectiveBrightness()
		if b <= 0 || dl.Direction.Len() < 1e-6 {
			continue
		}
		return dl.Direction.Normalize().Mul(-1), dl.Color.Mul(b), true
	}
	return mgl32.Vec3{}, mgl32.Vec3{}, false
}

// packAtmosphere writes f's atmosphere into the scene block, or switches it
// off there. No GL.
func packAtmosphere(b *gfx.SceneBlock, f *Fog, lvl *Level) {
	if !f.HasAtmosphere() {
		b.FogAtmos = 0
		b.FogHeightDensity, b.FogAerialDensity, b.FogSunColor = 0, 0, [3]float32{}
		return
	}
	b.FogAtmos = 1
	if h := f.Height; h.Enabled {
		b.FogHeightDensity, b.FogHeightBase, b.FogHeightFalloff = h.Density, h.Base, h.Falloff
		b.FogHeightColor = h.Color
	} else {
		b.FogHeightDensity = 0
	}
	if a := f.Aerial; a.Enabled {
		b.FogAerialDensity = 1 / max(a.Distance, FogAerialMinDistance)
		b.FogAerialFalloff = 1 / max(a.ScaleHeight, 1)
		b.FogAerialBase = a.Base
		b.FogAerialColor, b.FogAerialTint = a.Color, a.Tint
	} else {
		b.FogAerialDensity = 0
	}
	b.FogSunColor = [3]float32{}
	if s := f.Sun; s.Enabled {
		if dir, c, ok := fogSun(lvl); ok {
			b.FogSunDir = dir
			b.FogSunColor = mgl32.Vec3{c[0] * s.Color[0], c[1] * s.Color[1], c[2] * s.Color[2]}.Mul(s.Strength)
			b.FogSunG = s.Anisotropy
		}
	}
	b.FogSkyHaze, b.FogSkyDistance = f.SkyHaze, f.SkyDistance
}

// FogTransmittance is fog.glsl's atmosphere along a level segment of length d
// at height y, for a test or a tool that wants the number without a GPU: the
// fraction of what is behind it that survives, per channel. It mirrors
// fogAtmosSegment's extinction (not its colour), with the ray level.
func (f *Fog) FogTransmittance(y, d float32) mgl32.Vec3 {
	if !f.HasAtmosphere() {
		return mgl32.Vec3{1, 1, 1}
	}
	var th float64
	if f.Height.Enabled {
		th = float64(f.Height.Density) * float64(d) * math.Exp(-float64(f.Height.Falloff*(y-f.Height.Base)))
	}
	var out mgl32.Vec3
	for i := 0; i < 3; i++ {
		tau := th
		if f.Aerial.Enabled {
			k := 1 / float64(max(f.Aerial.ScaleHeight, 1))
			tau += float64(f.Aerial.Tint[i]) / float64(max(f.Aerial.Distance, FogAerialMinDistance)) * float64(d) *
				math.Exp(-k*float64(y-f.Aerial.Base))
		}
		out[i] = float32(max(math.Exp(-tau), 1-float64(f.MaxOpacity)))
	}
	return out
}

func rawVec3(c *RawColor) mgl32.Vec3 { return mgl32.Vec3{c.R, c.G, c.B} }
