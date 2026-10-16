package material

// material_resolve_fx.go — the terrain and rock effects (material_kinds_
// terrain.go) resolved onto a Surface.
//
// Like every other surface effect they follow the overlay rule: present, the
// effect's settings; absent, nothing — the Has* flag stays false and the
// draw path never takes the branch. They are gathered in one struct,
// SurfaceFx, because gfx carries them to the shader as one block (gfx/
// material_fx.go) and a surface either wears none of them, which is nearly
// every surface in the engine, or a handful together.

import "github.com/go-gl/mathgl/mgl32"

// SplatLayer is one resolved splat layer.
type SplatLayer struct {
	Texture   string
	Scale     float32
	Triplanar bool
	Height    float32
	Tint      mgl32.Vec3
	// AntiTile lays the texture in shifted (top down, also turned) patches.
	AntiTile bool
	// Relief is the parallax depth (m) of a top-down layer; 0 none.
	Relief float32
	// NoStrata keeps the strata ramp and the bedding off this layer (its
	// `_strata` param off; the default, false, is every triplanar layer
	// banded as always).
	NoStrata bool
	// Soft is the layer's soft-ground kind (SoftSand, SoftSnow; "" firm),
	// SoftDepth its footprint depth in metres (0: the kind's own).
	Soft      string
	SoftDepth float32
}

// SurfaceFx is a surface's terrain and rock effects, resolved.
type SurfaceFx struct {
	HasTriplanar       bool
	TriplanarScale     float32
	TriplanarSharpness float32
	TriplanarUpright   bool

	// HasSplat is the effect with a weights texture and at least one layer.
	HasSplat     bool
	SplatWeights string
	SplatMin     mgl32.Vec2
	SplatMax     mgl32.Vec2
	SplatBlend   float32
	SplatNormal  float32
	SplatAO      float32
	// SplatLayers are the layers; 5–8 (index 4..7) take part only in
	// eight-layer mode (SplatEight).
	SplatLayers [SplatLayersMax]SplatLayer
	// SplatWeights2 is layers 5–8's weights; SplatEight is eight-layer mode:
	// it named and at least one of layers 5–8 set. Off, the splat is the
	// four-layer splat exactly (docs/regions/PHASE_3.md).
	SplatWeights2 string
	SplatEight    bool
	// SplatAtlas are the layers whose atlas the splat's layers are packed
	// into (its `atlas1..8` params; docs/regions/PHASE_6.md "Per-tile
	// materials"): a region terrain's four-layer tile material names its
	// eight-layer union's layers, so both sample the same atlas texels.
	// All empty — every splat but those — is the splat's own layers.
	SplatAtlas      [SplatAtlasMax]string
	SplatStrata     string
	SplatStrataLo   float32
	SplatStrataHi   float32
	SplatStrataAmt  float32
	SplatStrataWarp float32
	// SplatBedding is the bedding relief (0: none), SplatBeddingSpacing its
	// beds' thickness in metres, SplatWallNormal the side projections'
	// normal-map multiplier (1: as the others).
	SplatBedding        float32
	SplatBeddingSpacing float32
	SplatWallNormal     float32
	// SplatRipple is the sand ripples on one layer: strength (0: none),
	// wavelength in metres, the wind's bearing in degrees and the layer.
	SplatRipple, SplatRippleWave, SplatRippleAngle, SplatRippleLayer float32

	// HasGroundBlend is the effect with both of its maps named.
	HasGroundBlend bool
	GroundColor    string
	GroundHeight   string
	GroundMin      mgl32.Vec2
	GroundMax      mgl32.Vec2
	GroundLo       float32
	GroundHi       float32
	GroundBand     float32
	GroundBreakup  float32
	GroundStrength float32
	GroundContact  float32

	HasStreaks      bool
	StreakStrength  float32
	StreakScale     float32
	StreakLength    float32
	StreakColor     mgl32.Vec3
	StreakOverhang  float32
	HasDetailNormal bool
	DetailMap       string
	DetailScale     float32
	DetailStrength  float32
	DetailDistance  float32

	HasMacro    bool
	MacroScale  float32
	MacroAmount float32
	MacroWarmth float32
	// MacroVertical is how much the patches vary with height too.
	MacroVertical float32

	HasGrade        bool
	GradeMatch      float32
	GradeTarget     mgl32.Vec3
	GradeSource     mgl32.Vec3 // zero: the texture's own mean (gfx measures it)
	GradeHue        float32    // degrees
	GradeSaturation float32
	GradeTint       mgl32.Vec3
	GradeBrightness float32
}

// HasSoft reports whether the splat has a soft-ground layer it draws (one
// with a texture: layers 5–8 only in eight-layer mode, which resolve
// already cleared otherwise).
func (f *SurfaceFx) HasSoft() bool {
	if !f.HasSplat {
		return false
	}
	for i := range f.SplatLayers {
		if l := &f.SplatLayers[i]; l.Soft != "" && l.Texture != "" {
			return true
		}
	}
	return false
}

// Any reports whether the surface wears any of them.
func (f *SurfaceFx) Any() bool {
	return f.HasTriplanar || f.HasSplat || f.HasGroundBlend || f.HasStreaks || f.HasDetailNormal || f.HasMacro || f.HasGrade
}

// resolveSurfaceFx reads d's terrain and rock effects into s.Fx.
func resolveSurfaceFx(d *Doc, s *Surface) {
	f := &s.Fx
	if e := d.Effect(KindTriplanar); e != nil {
		f.HasTriplanar = true
		f.TriplanarScale, f.TriplanarSharpness, f.TriplanarUpright = e.Float("scale"), e.Float("sharpness"), e.Bool("upright")
	}
	if e := d.Effect(KindSplat); e != nil {
		f.SplatWeights = e.Texture("weights")
		f.SplatMin, f.SplatMax = e.Vec2("bounds_min"), e.Vec2("bounds_max")
		f.SplatBlend, f.SplatNormal, f.SplatAO = e.Float("blend"), e.Float("normal_strength"), e.Float("ao")
		layers, upper := 0, 0
		for i := 0; i < SplatLayersMax; i++ {
			l := SplatLayer{
				Texture:   e.Texture(SplatLayerParam(i, "")),
				Scale:     e.Float(SplatLayerParam(i, "scale")),
				Triplanar: e.Bool(SplatLayerParam(i, "triplanar")),
				Height:    e.Float(SplatLayerParam(i, "height")),
				Tint:      e.Color(SplatLayerParam(i, "tint")),
				AntiTile:  e.Bool(SplatLayerParam(i, "anti_tile")),
				Relief:    e.Float(SplatLayerParam(i, "relief")),
				NoStrata:  !e.Bool(SplatLayerParam(i, "strata")),
				SoftDepth: e.Float(SplatLayerParam(i, "soft_depth")),
			}
			if s := e.Enum(SplatLayerParam(i, "soft")); s == SoftSand || s == SoftSnow {
				l.Soft = s
			}
			if l.Texture != "" {
				if i < SplatLayers {
					layers++
				} else {
					upper++
				}
			}
			f.SplatLayers[i] = l
		}
		f.SplatWeights2 = e.Texture("weights2")
		f.SplatEight = f.SplatWeights2 != "" && upper > 0
		if f.SplatEight {
			layers += upper
		} else {
			// Not in eight-layer mode: layers 5–8 are not this splat's, and
			// leaving them out keeps it the four-layer state to the byte
			// (the vis pass compares states with !=).
			for i := SplatLayers; i < SplatLayersMax; i++ {
				f.SplatLayers[i] = SplatLayer{}
			}
			f.SplatWeights2 = ""
		}
		// The shared atlas: kept only when every layer this splat draws is
		// in it (otherwise the splat's own layers, as always).
		var atlas [SplatAtlasMax]string
		shared := false
		for i := range atlas {
			atlas[i] = e.Texture(SplatAtlasParam(i))
			shared = shared || atlas[i] != ""
		}
		if shared {
			for i := range f.SplatLayers {
				if tex := f.SplatLayers[i].Texture; tex != "" && !containsName(atlas[:], tex) {
					shared = false
					break
				}
			}
		}
		if shared {
			f.SplatAtlas = atlas
		}
		f.SplatStrata = e.Texture("strata")
		r := e.Vec2("strata_range")
		f.SplatStrataLo, f.SplatStrataHi = r.X(), r.Y()
		f.SplatStrataAmt, f.SplatStrataWarp = e.Float("strata_strength"), e.Float("strata_warp")
		f.SplatBedding, f.SplatBeddingSpacing = e.Float("bedding"), e.Float("bedding_spacing")
		f.SplatWallNormal = e.Float("wall_normal")
		f.SplatRipple, f.SplatRippleWave = e.Float("ripple"), e.Float("ripple_wavelength")
		f.SplatRippleAngle, f.SplatRippleLayer = e.Float("ripple_angle"), e.Float("ripple_layer")
		// Weights and something to weigh, or nothing: a splat with no
		// weights would paint the terrain its first layer and nothing else,
		// which is the plain texture with extra steps.
		f.HasSplat = f.SplatWeights != "" && layers > 0
	}
	if e := d.Effect(KindGroundBlend); e != nil {
		f.GroundColor, f.GroundHeight = e.Texture("ground"), e.Texture("height")
		f.GroundMin, f.GroundMax = e.Vec2("bounds_min"), e.Vec2("bounds_max")
		r := e.Vec2("height_range")
		f.GroundLo, f.GroundHi = r.X(), r.Y()
		f.GroundBand, f.GroundBreakup = e.Float("band"), e.Float("breakup")
		f.GroundStrength, f.GroundContact = e.Float("strength"), e.Float("contact")
		f.HasGroundBlend = f.GroundColor != "" && f.GroundHeight != ""
	}
	if e := d.Effect(KindStreaks); e != nil {
		f.HasStreaks = true
		f.StreakStrength, f.StreakScale, f.StreakLength = e.Float("strength"), e.Float("scale"), e.Float("length")
		f.StreakColor, f.StreakOverhang = e.Color("color"), e.Float("overhang")
	}
	if e := d.Effect(KindDetailNormal); e != nil {
		f.DetailMap = e.Texture("map")
		f.DetailScale, f.DetailStrength, f.DetailDistance = e.Float("scale"), e.Float("strength"), e.Float("distance")
		f.HasDetailNormal = f.DetailMap != ""
	}
	if e := d.Effect(KindMacroVariation); e != nil {
		f.HasMacro = true
		f.MacroScale, f.MacroAmount, f.MacroWarmth = e.Float("scale"), e.Float("strength"), e.Float("warmth")
		f.MacroVertical = e.Float("vertical")
	}
	if e := d.Effect(KindColorGrade); e != nil {
		f.HasGrade = true
		f.GradeMatch, f.GradeTarget, f.GradeSource = e.Float("match"), e.Color("target"), e.Color("source")
		f.GradeHue, f.GradeSaturation = e.Float("hue"), e.Float("saturation")
		f.GradeTint, f.GradeBrightness = e.Color("tint"), e.Float("brightness")
	}
}

// containsName reports whether names holds name.
func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
