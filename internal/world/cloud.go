package world

// cloud.go — clouds, in the two places a level can have them.
//
// A CLOUD VOLUME is a placed region full of cloud: a volume with a `cloud:`
// block, exactly as a fog volume is a volume with a `fog:` block, and for the
// reason volume_fog.go gives — a region of cloud needs a shape, a rotation,
// half-extents, an ID, an `enabled` flag, a cage and gizmos, and a volume
// already has all six. A big flat box over the level is a cloud layer; a
// sphere is one cumulus.
//
// The SKY'S CLOUD DECK is the other half: cloud with no edge, a slab between
// two altitudes that runs out to the horizon. It belongs to the sky rather
// than to a volume because it has no position any more than the sky does, and
// because it is what `ease_sky` should be able to clear or thicken along with
// the colour of the dome behind it.
//
// Both are marched by the same shader (resources/shaders/volume_common.glsl),
// so what a cloud LOOKS like is one struct, CloudLook, embedded in each. What
// differs is only the shape: a volume fades out toward its own surface
// (Softness), and the deck has an altitude, a thickness and a draw range.
//
// Nothing here decides how a cloud is drawn — render/volumetric.go does. This
// file holds what the author set, keeps it inside the range the shader
// assumes, and moves it through YAML.

import "github.com/go-gl/mathgl/mgl32"

// CloudLook is everything about a cloud that is not its shape.
type CloudLook struct {
	// Color is the cloud's albedo: what fraction of the light reaching it is
	// scattered back out. White is a cumulus; a grey is a storm; a warm tint is
	// how a volcano's plume differs from weather.
	Color mgl32.Vec3
	// Coverage is how much of the region the noise is allowed to fill, 0 to 1.
	// Low is scattered puffs with sky between; high is an overcast mass. It is
	// the one number an author changes most, which is why it is separate from
	// Density rather than folded into it.
	Coverage float32
	// Density is the extinction per world unit inside the thickest part of the
	// cloud, the same unit a fog volume's Density is in.
	Density float32
	// Scale is how many world units one repeat of the base noise spans, which
	// is roughly the size of the largest lobes. Detail is the same for the
	// erosion noise that breaks their edges into wisps.
	Scale  float32
	Detail float32
	// Erosion is how hard the detail noise eats into the edges, 0 to 1. 0 is
	// soft cotton; 1 is ragged.
	Erosion float32
	// Wind scrolls the noise through the cloud, in world units per second, so a
	// cloud boils and drifts rather than hanging frozen. It moves the pattern,
	// not the region: a cloud volume stays where it was placed.
	Wind mgl32.Vec3
	// Absorption multiplies how much light the cloud's own mass takes out on
	// the way from the sun, which is what shades its underside. Above 1 is a
	// darker, heavier cloud.
	Absorption float32
	// Ambient multiplies the sky light filling the shadowed side.
	Ambient float32
	// CastShadows puts the cloud's shadow on the ground, sprites and scatter
	// under it — see render/cloud_shadow.go.
	CastShadows bool
}

// Cloud limits. Wide on purpose: a cloud is authored at the scale of whatever
// it hangs over, and that is a room as often as it is a valley.
const (
	CloudMaxDensity    float32 = 10
	CloudMaxAbsorption float32 = 8
	CloudMaxAmbient    float32 = 8
	CloudMinScale      float32 = 0.5
	CloudMaxScale      float32 = 100000
	CloudMaxWind       float32 = 1000
)

// DefaultCloudLook is a white, fair-weather cloud at the scale a volume is
// placed at. The deck's defaults start from it and scale it up.
func DefaultCloudLook() CloudLook {
	return CloudLook{
		Color:       mgl32.Vec3{1, 1, 1},
		Coverage:    0.6,
		Density:     0.3,
		Scale:       40,
		Detail:      10,
		Erosion:     0.35,
		Wind:        mgl32.Vec3{1.5, 0, 0.5},
		Absorption:  1,
		Ambient:     1,
		CastShadows: true,
	}
}

// Clamp puts every field back inside the range volume_common.glsl assumes.
func (c *CloudLook) Clamp() {
	if c == nil {
		return
	}
	for i := 0; i < 3; i++ {
		c.Color[i] = clampF(c.Color[i], 0, 1)
		c.Wind[i] = clampF(c.Wind[i], -CloudMaxWind, CloudMaxWind)
	}
	c.Coverage = clampF(c.Coverage, 0, 1)
	c.Density = clampF(c.Density, 0, CloudMaxDensity)
	c.Scale = clampF(c.Scale, CloudMinScale, CloudMaxScale)
	c.Detail = clampF(c.Detail, CloudMinScale, CloudMaxScale)
	c.Erosion = clampF(c.Erosion, 0, 1)
	c.Absorption = clampF(c.Absorption, 0, CloudMaxAbsorption)
	c.Ambient = clampF(c.Ambient, 0, CloudMaxAmbient)
}

// visible reports whether the look would put anything on screen at all.
func (c *CloudLook) visible() bool {
	return c.Density > 0 && c.Coverage > 0
}

// LerpCloudLook blends every number between a and b. The flag is b's: a
// shadow cannot be half cast.
func LerpCloudLook(a, b CloudLook, t float32) CloudLook {
	l := func(x, y float32) float32 { return x + (y-x)*t }
	v := func(x, y mgl32.Vec3) mgl32.Vec3 { return x.Add(y.Sub(x).Mul(t)) }
	return CloudLook{
		Color:       v(a.Color, b.Color),
		Coverage:    l(a.Coverage, b.Coverage),
		Density:     l(a.Density, b.Density),
		Scale:       l(a.Scale, b.Scale),
		Detail:      l(a.Detail, b.Detail),
		Erosion:     l(a.Erosion, b.Erosion),
		Wind:        v(a.Wind, b.Wind),
		Absorption:  l(a.Absorption, b.Absorption),
		Ambient:     l(a.Ambient, b.Ambient),
		CastShadows: b.CastShadows,
	}
}

func cloudLookFromRaw(r RawCloudLook, def CloudLook) CloudLook {
	c := def
	if r.Color != nil {
		c.Color = mgl32.Vec3{r.Color.R, r.Color.G, r.Color.B}
	}
	c.Coverage = float32Default(r.Coverage, def.Coverage)
	c.Density = float32Default(r.Density, def.Density)
	c.Scale = float32Default(r.Scale, def.Scale)
	c.Detail = float32Default(r.Detail, def.Detail)
	c.Erosion = float32Default(r.Erosion, def.Erosion)
	if r.Wind != nil {
		c.Wind = mgl32.Vec3{r.Wind.X, r.Wind.Y, r.Wind.Z}
	}
	c.Absorption = float32Default(r.Absorption, def.Absorption)
	c.Ambient = float32Default(r.Ambient, def.Ambient)
	c.CastShadows = !r.NoShadows
	return c
}

// cloudLookToRaw writes every number once there is a cloud at all, for the
// reason VolumeFogToRaw gives: a cloud that omitted the coverage it was tuned
// at would come back as a different cloud the day a default moved.
func cloudLookToRaw(c CloudLook) RawCloudLook {
	coverage, density, scale, detail := c.Coverage, c.Density, c.Scale, c.Detail
	erosion, absorption, ambient := c.Erosion, c.Absorption, c.Ambient
	return RawCloudLook{
		Color:      &RawColor{R: c.Color.X(), G: c.Color.Y(), B: c.Color.Z()},
		Coverage:   &coverage,
		Density:    &density,
		Scale:      &scale,
		Detail:     &detail,
		Erosion:    &erosion,
		Wind:       &RawVec3{X: c.Wind.X(), Y: c.Wind.Y(), Z: c.Wind.Z()},
		Absorption: &absorption,
		Ambient:    &ambient,
		NoShadows:  !c.CastShadows,
	}
}

// ---------- cloud volumes ----------

// VolumeCloud is the cloud filling a volume.
type VolumeCloud struct {
	// Enabled switches the cloud off while keeping the numbers — the same
	// distinction VolumeFog.Enabled draws against the volume's own Enabled.
	Enabled bool
	CloudLook
	// Softness is how far in from the region's surface the cloud fades up to
	// full, as a fraction of the half-extent. Near 0 the shape's walls show as
	// flat cut faces through the noise; the default rounds them off so a box of
	// cloud does not read as a box.
	Softness float32
}

// DefaultVolumeCloud is what ticking "full of cloud" gives a volume.
func DefaultVolumeCloud() VolumeCloud {
	return VolumeCloud{Enabled: true, CloudLook: DefaultCloudLook(), Softness: 0.3}
}

// Clamp puts every field back inside the range the shader assumes.
func (c *VolumeCloud) Clamp() {
	if c == nil {
		return
	}
	c.CloudLook.Clamp()
	c.Softness = clampF(c.Softness, 0.01, 1)
}

// HasCloud reports whether the volume puts any cloud on screen: the block, both
// switches, a real region, and a look that is not empty. The volume's own
// Enabled counts for the reason HasFog says it does.
func (v *Volume) HasCloud() bool {
	return v != nil && v.Cloud != nil && v.Cloud.Enabled && v.Enabled && v.HasBounds() &&
		v.Cloud.visible()
}

// CloudVolumes returns every volume with cloud to draw, in level order.
func CloudVolumes(vols []Volume) []*Volume {
	var out []*Volume
	for i := range vols {
		if vols[i].HasCloud() {
			out = append(out, &vols[i])
		}
	}
	return out
}

// MaxCloudClearVolumes is how many cloud-clearing regions the march carries.
// MUST match VOL_MAX_CLEARS in volume_frag.glsl.
//
// Four, because each one is a test on every sample of every cloud march, and
// the case that asked for them — a ship's hold, a cockpit, a hangar — wants
// one or two in view. Past it the later ones in level order among those in view
// are dropped, the truncation FogVolumes makes, and the cloud simply shows
// inside them.
const MaxCloudClearVolumes = 4

// ClearsCloudsNow reports whether the volume is keeping cloud out this frame:
// the flag, the volume's own Enabled, and a real region. A disabled volume
// contains nothing — HasFog's rule — and that includes the absence of cloud.
//
// A carried one counts like any other: it is a level volume by the time
// anything draws, and its basis and centre follow the carrier through
// WorldFogBasis and WorldPosition, so the clear region flies with the ship.
func (v *Volume) ClearsCloudsNow() bool {
	return v != nil && v.ClearsClouds && v.Enabled && v.HasBounds()
}

// VolumeCloudFromRaw reads the optional per-volume cloud block; nil in, nil
// out, so a plain trigger stays one across a save.
func VolumeCloudFromRaw(r *RawVolumeCloud) *VolumeCloud {
	if r == nil {
		return nil
	}
	def := DefaultVolumeCloud()
	c := VolumeCloud{
		Enabled:   !r.Disabled,
		CloudLook: cloudLookFromRaw(r.RawCloudLook, def.CloudLook),
		Softness:  float32Default(r.Softness, def.Softness),
	}
	c.Clamp()
	return &c
}

// VolumeCloudToRaw is the inverse.
func VolumeCloudToRaw(c *VolumeCloud) *RawVolumeCloud {
	if c == nil {
		return nil
	}
	cp := *c
	cp.Clamp()
	softness := cp.Softness
	return &RawVolumeCloud{
		Disabled:     !cp.Enabled,
		RawCloudLook: cloudLookToRaw(cp.CloudLook),
		Softness:     &softness,
	}
}

// ---------- the sky's cloud deck ----------

// SkyClouds is the deck: a layer of cloud between Altitude and
// Altitude+Thickness, running out to Range from the eye in every direction.
type SkyClouds struct {
	Enabled bool
	CloudLook
	// Altitude is the deck's base, in world units. The camera may be above it —
	// flying over weather is a thing a level can want — and the deck is drawn
	// the same way from either side.
	Altitude  float32
	Thickness float32
	// Range is how far from the eye the deck is marched before it fades into
	// the horizon. It bounds the cost of a view along the deck, not merely the
	// look of one.
	Range float32
}

// Deck limits.
const (
	SkyCloudMaxAltitude  float32 = 100000
	SkyCloudMinThickness float32 = 1
	SkyCloudMinRange     float32 = 10
)

// DefaultSkyClouds is a scattered fair-weather deck well above a level built
// at the engine's own default draw distance.
func DefaultSkyClouds() SkyClouds {
	look := DefaultCloudLook()
	look.Coverage = 0.45
	look.Density = 0.06
	look.Scale = 450
	look.Detail = 70
	look.Wind = mgl32.Vec3{6, 0, 2}
	return SkyClouds{Enabled: true, CloudLook: look, Altitude: 180, Thickness: 70, Range: 3000}
}

// Clamp puts every field back inside the range the shader assumes.
func (c *SkyClouds) Clamp() {
	if c == nil {
		return
	}
	c.CloudLook.Clamp()
	c.Altitude = clampF(c.Altitude, -SkyCloudMaxAltitude, SkyCloudMaxAltitude)
	c.Thickness = clampF(c.Thickness, SkyCloudMinThickness, SkyCloudMaxAltitude)
	c.Range = clampF(c.Range, SkyCloudMinRange, SkyCloudMaxAltitude)
}

// EffectiveClouds is the deck as it should be drawn this frame: the sky's own,
// or — while an ease_sky transition is running — a blend of the deck being
// eased away from and the one being eased toward.
//
// A deck is numbers, so unlike a cubemap it CAN be lerped, and blending the
// numbers is both cheaper and better-looking than marching two decks and
// mixing their pixels: clouds thin out and clear rather than one set of clouds
// ghosting through another. A side with no deck blends as the other side's
// deck at zero coverage, which is what clearing the sky looks like.
func (s *Sky) EffectiveClouds() (SkyClouds, bool) {
	if s == nil {
		return SkyClouds{}, false
	}
	to := s.Clouds
	toOn := to != nil && to.Enabled && to.visible()
	b := s.TransitionBlend()
	if s.TransitionFrom == nil || b >= 1 {
		if !toOn {
			return SkyClouds{}, false
		}
		return *to, true
	}
	from := s.TransitionFrom.Clouds
	fromOn := from != nil && from.Enabled && from.visible()
	switch {
	case toOn && fromOn:
		l := func(x, y float32) float32 { return x + (y-x)*b }
		return SkyClouds{
			Enabled:   true,
			CloudLook: LerpCloudLook(from.CloudLook, to.CloudLook, b),
			Altitude:  l(from.Altitude, to.Altitude),
			Thickness: l(from.Thickness, to.Thickness),
			Range:     l(from.Range, to.Range),
		}, true
	case toOn:
		c := *to
		c.Coverage *= b
		return c, c.Coverage > 0
	case fromOn:
		c := *from
		c.Coverage *= 1 - b
		return c, c.Coverage > 0
	}
	return SkyClouds{}, false
}

// SkyCloudsFromRaw reads the optional deck block.
func SkyCloudsFromRaw(r *RawSkyClouds) *SkyClouds {
	if r == nil {
		return nil
	}
	def := DefaultSkyClouds()
	c := SkyClouds{
		Enabled:   !r.Disabled,
		CloudLook: cloudLookFromRaw(r.RawCloudLook, def.CloudLook),
		Altitude:  float32Default(r.Altitude, def.Altitude),
		Thickness: float32Default(r.Thickness, def.Thickness),
		Range:     float32Default(r.Range, def.Range),
	}
	c.Clamp()
	return &c
}

// SkyCloudsToRaw is the inverse.
func SkyCloudsToRaw(c *SkyClouds) *RawSkyClouds {
	if c == nil {
		return nil
	}
	cp := *c
	cp.Clamp()
	altitude, thickness, rng := cp.Altitude, cp.Thickness, cp.Range
	return &RawSkyClouds{
		Disabled:     !cp.Enabled,
		RawCloudLook: cloudLookToRaw(cp.CloudLook),
		Altitude:     &altitude,
		Thickness:    &thickness,
		Range:        &rng,
	}
}
