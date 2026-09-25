package world

// sky_material.go — a level's sky wearing a .fmat.
//
// The sky is the one domain where the material is APPLIED to the object rather
// than overlaid at draw time, and the reason is the transition: `ease_sky`
// blends two complete Sky values over a few seconds (Sky.TransitionFrom), and
// a sky whose numbers changed under it mid-blend would ease toward a moving
// target. So a sky material is folded into the Sky's own fields — on load, and
// again whenever the material library changes — and everything downstream, the
// easing included, carries on reading the fields it always did.
//
// The practical effect is the same as everywhere else: dragging a coverage
// slider in the material editor thins the clouds over the level in the next
// frame, because SyncSkyMaterial notices the generation and re-applies.

import "foundry/internal/material"

// ApplyMaterial folds s.Material into s. A sky with no material, or one naming
// a material that is not a sky, is left exactly as it is.
//
// Only the effects the material carries are applied, on the rule the rest of
// the system follows: a material with nothing but a `clouds` effect changes the
// deck and leaves the level's own gradient, tint and rotation alone.
func (s *Sky) ApplyMaterial() {
	if s == nil {
		return
	}
	m, ok := material.ResolveSky(s.Material)
	if !ok {
		return
	}
	if m.HasGradient {
		s.Kind = SkyGradient
		s.Zenith, s.Horizon, s.Ground = m.Zenith, m.Horizon, m.Ground
	}
	if m.HasCubemap {
		s.Kind = SkyCubemap
		for i := 0; i < SkyFaces; i++ {
			s.Faces[i] = m.Faces[i]
		}
		s.Rotation = m.Rotation
	}
	if m.HasTint {
		s.Tint, s.Intensity = m.Tint, m.Intensity
	}
	if m.HasClouds {
		c := s.Clouds
		if c == nil {
			c = &SkyClouds{}
			s.Clouds = c
		}
		c.Enabled = true
		c.Color = m.Clouds.Color
		c.Coverage = m.Clouds.Coverage
		c.Density = m.Clouds.Density
		c.Scale = m.Clouds.Scale
		c.Detail = m.Clouds.Detail
		c.Erosion = m.Clouds.Erosion
		c.Wind = m.Clouds.Wind
		c.Absorption = m.Clouds.Absorption
		c.Ambient = m.Clouds.Ambient
		c.CastShadows = m.Clouds.CastShadows
		c.Altitude = m.Clouds.Altitude
		c.Thickness = m.Clouds.Thickness
		c.Range = m.Clouds.Range
	}
	s.Clamp()
}

// skyMaterialGen is the material-library generation the level's sky was last
// applied at. Package-level rather than a field on Sky because Sky is
// serialised — into a save game, into a compiled level — and a cache stamp has
// no business in either.
var skyMaterialGen int

// SyncSkyMaterial re-applies the sky's material if the library has changed
// since it was last applied. Called once a frame; the common case is one
// integer compare.
//
// It re-applies on ANY library change rather than only on a change to this
// sky's own material, because the alternative is keeping a per-sky stamp for a
// saving of a few dozen float assignments once, on the frame an author moved a
// slider.
func SyncSkyMaterial(lvl *Level) {
	if lvl == nil || lvl.Sky == nil {
		return
	}
	gen := material.Generation()
	if gen == skyMaterialGen {
		return
	}
	skyMaterialGen = gen
	lvl.Sky.ApplyMaterial()
}

// InvalidateSkyMaterial forces the next SyncSkyMaterial to re-apply. For a
// level that has just loaded: the generation may not have moved, but the sky
// is a different one.
func InvalidateSkyMaterial() { skyMaterialGen = 0 }

// SkyMaterialTextures is every texture a sky material pulls in, for the
// packer. The renderer never needs it — the six faces are already in
// Sky.Faces by the time anything draws — but a level FILE that names only a
// material still has to have them packed beside it.
func SkyMaterialTextures(name string) []string {
	d := material.Get(name)
	if d == nil {
		return nil
	}
	return d.Textures()
}
