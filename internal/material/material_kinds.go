package material

// material_kinds.go — the registry of effect kinds, and the built-in ones.
//
// An effect KIND is the schema: its name, which domains may carry it, and the
// parameters it declares with their types, ranges and defaults. An Effect (see
// material_doc.go) is one instance of a kind on one document.
//
// Everything downstream is driven off this table and nothing else:
//
//   - the .fmat reader converts each YAML scalar by the ParamDef's type, so a
//     document needs no type annotations of its own;
//   - the .fmat writer omits any parameter still equal to its default, which is
//     what keeps a file down to what was actually changed;
//   - the material editor builds its whole parameter panel from ParamDefs — a
//     new parameter is a row here and no UI code at all;
//   - Doc.Validate names a parameter no kind declares, so a typo is caught when
//     the document is saved rather than by a surface that quietly does nothing.
//
// The kinds below are the engine's existing effects, moved here. Each one's
// doc comment says where it used to live and what still reads it, because the
// per-object field is NOT gone: a material resolves into it (material_surface.go
// and its siblings), so a level that names no material draws exactly as before.
//
// Adding an effect kind to the engine is this table plus one line in the
// resolver for its domain. Nothing else.

import (
	"sort"
	"sync"

	"github.com/go-gl/mathgl/mgl32"
)

// The built-in effect kind names. Referred to by constant rather than by
// literal everywhere else so that a rename is a compile error rather than an
// effect that silently stops resolving.
const (
	// Surface.
	KindUnlit       = "unlit"
	KindAlpha       = "alpha"
	KindCutout      = "cutout"
	KindBlend       = "blend"
	KindNormalMap   = "normal_map"
	KindUVScroll    = "uv_scroll"
	KindUVTransform = "uv_transform"
	KindWater       = "water"
	KindSmooth      = "smooth"
	KindPBR         = "pbr"

	// Sprite and particle.
	KindBlendMode   = "blend_mode"
	KindBillboard   = "billboard"
	KindTint        = "tint"
	KindLit         = "lit"
	KindCastShadows = "cast_shadows"
	KindSpriteSheet = "sprite_sheet"
	KindVolume      = "volume"

	// Sky.
	KindSkyGradient = "sky_gradient"
	KindSkyCubemap  = "sky_cubemap"
	KindClouds      = "clouds"
	KindFog         = "fog"
)

// ParamDef declares one parameter of an effect kind.
type ParamDef struct {
	// Name is the YAML key and the map key in Effect.Params.
	Name string
	Type ParamType
	// Label is what the editor puts beside the widget; Doc is its tooltip and
	// what `foundry check` prints. Both are for people — nothing resolves off
	// them.
	Label string
	Doc   string
	// Default is the value an effect that does not set this parameter reads.
	// Its Type must match Type; RegisterKind checks.
	Default Value
	// Min and Max bound the editor's drag widgets and are what Clamp enforces.
	// Equal (both zero, typically) means unbounded, which is right for a scroll
	// rate that may be negative and a wind that may be anything.
	Min, Max float32
	// Choices are a ParamEnum's legal values, in the order the editor offers
	// them. The first is the fallback for a name this build does not know.
	Choices []string
}

// Kind is one effect kind: a name, the domains it belongs to, and its schema.
type Kind struct {
	Name string
	// Domains is every domain that may carry the kind. A kind used outside them
	// still loads and is still saved — it is simply reported by Doc.Validate
	// and ignored by the resolvers, because refusing to load a document is a
	// worse outcome than drawing it without one effect.
	Domains []Domain
	// Label and Doc are the editor's heading and blurb for the effect.
	Label string
	Doc   string
	// Params is the schema, in the order the editor lays them out.
	Params []ParamDef
}

// Param returns the named parameter's definition, or nil.
func (k *Kind) Param(name string) *ParamDef {
	for i := range k.Params {
		if k.Params[i].Name == name {
			return &k.Params[i]
		}
	}
	return nil
}

// InDomain reports whether the kind belongs in d.
func (k *Kind) InDomain(d Domain) bool {
	for _, kd := range k.Domains {
		if kd == d {
			return true
		}
	}
	return false
}

// ---------- the registry ----------

var (
	kindMu sync.RWMutex
	kinds  = map[string]*Kind{}
)

// RegisterKind adds a kind to the registry. Called from this file's init for
// the built-ins; exported so a test can add one without reaching into the map.
// A kind whose default does not match its declared type is a programming error
// and panics, since it would otherwise surface as a parameter that reads zero
// no matter what the document says.
func RegisterKind(k *Kind) {
	for i := range k.Params {
		p := &k.Params[i]
		if p.Default.Type != p.Type {
			panic("material: kind " + k.Name + " parameter " + p.Name +
				": default is a " + ParamTypeName(p.Default.Type) +
				", declared " + ParamTypeName(p.Type))
		}
	}
	kindMu.Lock()
	defer kindMu.Unlock()
	kinds[k.Name] = k
}

// KindByName returns a registered kind, or nil for one this build does not
// know — which is not an error anywhere: the document keeps it, the writer
// writes it back, and only Doc.Validate mentions it.
func KindByName(name string) *Kind {
	kindMu.RLock()
	defer kindMu.RUnlock()
	return kinds[name]
}

// KindsFor lists every kind legal in a domain, by name, for the material
// editor's "Add effect" menu. Sorted so the menu does not shuffle between
// runs — a map's iteration order would.
func KindsFor(d Domain) []*Kind {
	kindMu.RLock()
	defer kindMu.RUnlock()
	var out []*Kind
	for _, k := range kinds {
		if k.InDomain(d) {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AllKinds lists every registered kind, sorted. For the documentation
// generator and for `foundry check`.
func AllKinds() []*Kind {
	kindMu.RLock()
	defer kindMu.RUnlock()
	out := make([]*Kind, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Clamp puts a value back inside its definition's range. Applied on load and
// by the editor, so the resolvers never hand the shader a number it does not
// assume — the job CloudLook.Clamp and VolumeFog.Clamp already do for their
// own fields.
func (p *ParamDef) Clamp(v Value) Value {
	if p.Min == p.Max {
		return v
	}
	n := 1
	switch p.Type {
	case ParamVec2:
		n = 2
	case ParamVec3, ParamColor:
		n = 3
	case ParamBool, ParamTexture, ParamEnum:
		return v
	}
	for i := 0; i < n; i++ {
		if v.Num[i] < p.Min {
			v.Num[i] = p.Min
		}
		if v.Num[i] > p.Max {
			v.Num[i] = p.Max
		}
	}
	return v
}

// ---------- the built-in kinds ----------

// surfaceOnly, spriteLike and so on name the domain sets the table below uses,
// so that "which domains is this in" reads as a phrase rather than as a slice
// literal repeated twenty times.
var (
	surfaceOnly  = []Domain{DomainSurface}
	spriteLike   = []Domain{DomainSprite, DomainParticle}
	quadAndSky   = []Domain{DomainSprite, DomainParticle, DomainSky}
	particleOnly = []Domain{DomainParticle}
	skyOnly      = []Domain{DomainSky}
)

func init() {
	// ----- surface -----

	// Was Geometry.Unlit, set from a brush face's `unlit:` and an entity
	// attachment's. Still is: ResolveSurface writes it.
	RegisterKind(&Kind{
		Name: KindUnlit, Domains: surfaceOnly, Label: "Unlit",
		Doc: "Draw the texture's own colour — no light, no shadow, no clamp to what " +
			"the scene's lights allow. Fog still applies. For things that give off " +
			"light rather than reflect it: a beam, a readout, a sign. It also sends " +
			"the surface to the blended pass.",
	})

	// Was Geometry.Alpha / Material.Alpha, the per-geometry multiplier the
	// `alpha` uniform carries.
	RegisterKind(&Kind{
		Name: KindAlpha, Domains: []Domain{DomainSurface, DomainSprite, DomainParticle},
		Label: "Alpha",
		Doc: "A constant multiplier on the surface's alpha. Below 1 sends it to the " +
			"sorted transparent pass, where alpha means blending rather than coverage.",
		Params: []ParamDef{{
			Name: "value", Type: ParamFloat, Label: "Alpha",
			Doc:     "1 is the texture unchanged; 0 is invisible.",
			Default: FloatValue(1), Min: 0, Max: 1,
		}},
	})

	// The alpha-cutout threshold, which was the `alphaCutoff` constant in
	// fragment.glsl and therefore the same 0.5 for every surface in the game.
	RegisterKind(&Kind{
		Name: KindCutout, Domains: surfaceOnly, Label: "Cutout",
		Doc: "Discard texels below a threshold instead of blending them, giving a hard " +
			"silhouette that writes depth and needs no sorting. The opaque pass " +
			"antialiases the edge with alpha-to-coverage.",
		Params: []ParamDef{{
			Name: "threshold", Type: ParamFloat, Label: "Threshold",
			Doc: "Where the edge sits once mip filtering has turned a hard 0/255 alpha " +
				"into a ramp. 0.5 — the halfway point — reproduces the original " +
				"silhouette; lower fattens it, higher eats it.",
			Default: FloatValue(0.5), Min: 0.01, Max: 0.99,
		}},
	})

	// Was Geometry.BlendTexture/BlendMask — "texture splatting", applied by
	// applyBlendUniforms.
	RegisterKind(&Kind{
		Name: KindBlend, Domains: surfaceOnly, Label: "Blend (second texture)",
		Doc: "Mix a second texture over the first, using a mask texture's per-pixel " +
			"luminance as the mix factor: 0 is the base texture, 1 the blend one. Any " +
			"texture can serve as the mask — only its luminance is read.",
		Params: []ParamDef{
			{Name: "texture", Type: ParamTexture, Label: "Blend texture",
				Doc: "The texture mixed over the base one.", Default: TextureValue("")},
			{Name: "mask", Type: ParamTexture, Label: "Mask",
				Doc:     "Luminance picks between the two. Both must be set for the blend to run.",
				Default: TextureValue("")},
		},
	})

	// Normal mapping was implicit: the engine looked for <texture>_normal.png
	// beside the albedo and there was no per-surface say in it at all. Naming
	// it here is what lets one material borrow another texture's relief, and
	// what lets a surface refuse the relief its texture ships with.
	RegisterKind(&Kind{
		Name: KindNormalMap, Domains: surfaceOnly, Label: "Normal map",
		Doc: "Light the surface with a tangent-space normal map. Left empty it is the " +
			"map the material's own texture ships with (<texture>_normal), which is " +
			"what a texture with a painted bump channel gets anyway — name one to " +
			"borrow another texture's relief instead.",
		Params: []ParamDef{{
			Name: "map", Type: ParamTexture, Label: "Map",
			Doc:     "Empty: the material texture's own <name>_normal.",
			Default: TextureValue(""),
		}},
	})

	// Was EntityModel.UVScroll, which only an entity attachment had — a tank's
	// tracks. As a material any surface in the engine can have it.
	RegisterKind(&Kind{
		Name: KindUVScroll, Domains: surfaceOnly, Label: "UV scroll",
		Doc: "Slide the texture across the surface as it draws, in UV units a second. " +
			"A conveyor, a track belt, a waterfall, a starfield.",
		Params: []ParamDef{{
			Name: "rate", Type: ParamVec2, Label: "Rate",
			Doc:     "U and V units a second. Negative runs the other way.",
			Default: Vec2Value(mgl32.Vec2{0, 0}),
		}},
	})

	// The face's own UVOffset/UVScale/UVRotation, as a material so that a tiling
	// can be named and reused rather than typed into every face's panel.
	RegisterKind(&Kind{
		Name: KindUVTransform, Domains: surfaceOnly, Label: "UV transform",
		Doc: "A fixed offset, scale and rotation applied to the texture coordinates. " +
			"Composed on top of whatever the surface itself is set to, so a material " +
			"can carry a tiling without overwriting a face's own placement.",
		Params: []ParamDef{
			{Name: "offset", Type: ParamVec2, Label: "Offset", Default: Vec2Value(mgl32.Vec2{0, 0})},
			{Name: "scale", Type: ParamVec2, Label: "Scale",
				Doc:     "Repeats across the surface. 1,1 is the texture once.",
				Default: Vec2Value(mgl32.Vec2{1, 1})},
			{Name: "rotation", Type: ParamFloat, Label: "Rotation",
				Doc: "Degrees.", Default: FloatValue(0), Min: -360, Max: 360},
		},
	})

	// Was gfx.WaterParams on a sector's surface, filled in through the water
	// section of the surface panel. The defaults are DefaultWaterParams' to the
	// digit, so a material with a bare `water` effect and a water sector with
	// its box ticked are the same surface.
	RegisterKind(&Kind{
		Name: KindWater, Domains: surfaceOnly, Label: "Water",
		Doc: "Water: depth colour, refraction and soft shorelines from the scene under it, sky and " +
			"screen-space reflection, a GGX sun glint, drifting ripples, shore foam and caustics on " +
			"the bed. The parameters are material_kinds_water.go's (docs/guide/28-materials.md).",
		Params: waterParamDefs(),
	})

	// Was Geometry.Smooth/SmoothAngle, ticked per surface in the editor.
	RegisterKind(&Kind{
		Name: KindSmooth, Domains: surfaceOnly, Label: "Smooth shading",
		Doc: "Shade from interpolated vertex normals rather than flat facets, whatever " +
			"the player's Smooth Lighting setting says. Edges sharper than the crease " +
			"angle stay hard.",
		Params: []ParamDef{{
			Name: "angle", Type: ParamFloat, Label: "Crease angle",
			Doc:     "Degrees. 0 uses the engine's default.",
			Default: FloatValue(0), Min: 0, Max: 180,
		}},
	})

	// New with the lighting pass (docs/lighting/PLAN.md, phase 1): how the
	// surface reflects light, for the GGX specular every lit surface takes
	// (specular.glsl). A surface without it is a rough dielectric at these
	// same defaults. The ARM map (<texture>_arm, AO / roughness / metal, as
	// Poly Haven ships them) multiplies roughness and metallic per texel when
	// the texture has one, as glTF's factors multiply its maps.
	RegisterKind(&Kind{
		Name: KindPBR, Domains: surfaceOnly, Label: "Physically based",
		Doc: "How shiny the surface is: its roughness, whether it is a metal, and the " +
			"strength of a non-metal's reflection. A texture's <name>_arm map (AO, " +
			"roughness, metal) multiplies them per texel.",
		Params: []ParamDef{
			{Name: "roughness", Type: ParamFloat, Label: "Roughness",
				Doc:     "0 is a mirror, 1 a matte surface with only a broad sheen.",
				Default: FloatValue(DefaultPBRRoughness), Min: 0, Max: 1},
			{Name: "metallic", Type: ParamFloat, Label: "Metallic",
				Doc:     "1 is a metal: no diffuse colour, and its reflection takes the albedo's tint.",
				Default: FloatValue(0), Min: 0, Max: 1},
			{Name: "specular", Type: ParamFloat, Label: "Specular",
				Doc:     "A non-metal's reflectance at normal incidence: 0.5 is the 4% of most materials, 1 is 8% (gems, wet stone).",
				Default: FloatValue(DefaultPBRSpecular), Min: 0, Max: 1},
			{Name: "arm", Type: ParamTexture, Label: "ARM map",
				Doc:     "An AO / roughness / metal texture to use instead of <texture>_arm. Empty looks for that.",
				Default: TextureValue("")},
		},
	})

	// ----- sprite and particle -----

	// Was gfx.SpriteBlend, on world.Sprite.Blend and particle.Layer.Blend.
	RegisterKind(&Kind{
		Name: KindBlendMode, Domains: spriteLike, Label: "Blend mode",
		Doc: "Which pass the quad draws in, not merely how it is sorted.",
		Params: []ParamDef{{
			Name: "mode", Type: ParamEnum, Label: "Mode",
			Doc: "cutout: opaque with a hard alpha edge, writes depth, antialiased by " +
				"alpha-to-coverage. alpha: sorted back-to-front, no depth write. " +
				"additive: adds light and never occludes, so it needs no sorting.",
			Default: EnumValue("cutout"),
			Choices: []string{"cutout", "alpha", "additive"},
		}},
	})

	// Was gfx.BillboardMode, on the same two.
	RegisterKind(&Kind{
		Name: KindBillboard, Domains: spriteLike, Label: "Billboard",
		Doc: "How the quad is oriented against the viewer.",
		Params: []ParamDef{{
			Name: "mode", Type: ParamEnum, Label: "Mode",
			Doc: "full: faces the viewer on both axes — a glow, a puff, a spark. " +
				"y-axis: keeps world up and only yaws — anything standing on the " +
				"ground. fixed: does not billboard, leaving the authored rotation.",
			Default: EnumValue("full"),
			Choices: []string{"full", "y-axis", "fixed"},
		}},
	})

	// Was Sprite.Tint, and Sky.Tint/Sky.Intensity — the same two numbers under
	// two names in two places.
	RegisterKind(&Kind{
		Name: KindTint, Domains: quadAndSky, Label: "Tint",
		Doc: "Multiply whatever colour the texture or the gradient produced, so one set " +
			"of art can serve several times of day.",
		Params: []ParamDef{
			{Name: "color", Type: ParamColor, Label: "Colour",
				Doc: "White is no tint.", Default: ColorValue(mgl32.Vec3{1, 1, 1}), Min: 0, Max: 1},
			{Name: "intensity", Type: ParamFloat, Label: "Intensity",
				Doc:     "Scales the tint. Above 1 brightens.",
				Default: FloatValue(1), Min: 0, Max: 8},
		},
	})

	// Was Sprite.Lit.
	RegisterKind(&Kind{
		Name: KindLit, Domains: spriteLike, Label: "Lit",
		Doc: "Take the full scene lighting and shadows. Off by default, which is right " +
			"for anything emissive — a flame lit by the sun looks wrong.",
	})

	// Was Sprite.CastShadows.
	RegisterKind(&Kind{
		Name: KindCastShadows, Domains: spriteLike, Label: "Cast shadows",
		Doc: "Put the quad in the depth passes, where it faces the light rather than the " +
			"camera. Only convincing for the solid ones.",
	})

	// Was Sprite.SheetCols/SheetRows — a property of the art, which is exactly
	// what a material is for.
	RegisterKind(&Kind{
		Name: KindSpriteSheet, Domains: spriteLike, Label: "Sprite sheet",
		Doc: "Divide the texture into a grid of cells; whatever wears the material picks " +
			"one with its own frame number. Zero on either axis is the whole texture.",
		Params: []ParamDef{
			{Name: "cols", Type: ParamFloat, Label: "Columns",
				Default: FloatValue(0), Min: 0, Max: 256},
			{Name: "rows", Type: ParamFloat, Label: "Rows",
				Default: FloatValue(0), Min: 0, Max: 256},
		},
	})

	// Was particle.VolumeLook, the `volume:` block of a .fpar layer. The two
	// ramps stay on the layer: a ramp is a curve over a particle's life, not a
	// material parameter, and there is nowhere in a scalar schema to put one.
	RegisterKind(&Kind{
		Name: KindVolume, Domains: particleOnly, Label: "Volumetric",
		Doc: "Draw the layer's particles as soft, noise-displaced balls of participating " +
			"medium in the volumetric march rather than as quads. The erosion and " +
			"emission ramps stay on the layer itself.",
		Params: []ParamDef{
			{Name: "density", Type: ParamFloat, Label: "Density",
				Doc:     "Extinction per world unit at a ball's core.",
				Default: FloatValue(4), Min: 0, Max: 100},
			{Name: "noise_scale", Type: ParamFloat, Label: "Noise scale",
				Doc: "Repeats of the noise across one ball's radius. Low is a few big " +
					"lumps; high is a cauliflower.",
				Default: FloatValue(1.2), Min: 0.01, Max: 40},
			{Name: "displacement", Type: ParamFloat, Label: "Displacement",
				Doc: "How far the noise pushes the surface in and out, as a fraction of " +
					"the radius. 0 is a smooth sphere of smoke.",
				Default: FloatValue(0.35), Min: 0, Max: 1},
			{Name: "billow", Type: ParamFloat, Label: "Billow",
				Doc: "How fast the noise rolls outward from the centre — what makes a " +
					"fireball churn as it grows rather than inflate.",
				Default: FloatValue(0.6), Min: 0, Max: 10},
			{Name: "erosion", Type: ParamFloat, Label: "Erosion",
				Doc:     "How hard the detail noise eats the edges.",
				Default: FloatValue(0.3), Min: 0, Max: 1},
			{Name: "albedo", Type: ParamColor, Label: "Albedo",
				Doc:     "The colour of the smoke — the light it scatters back.",
				Default: ColorValue(mgl32.Vec3{0.45, 0.43, 0.4}), Min: 0, Max: 1},
			{Name: "absorption", Type: ParamFloat, Label: "Absorption",
				Doc: "How much light the ball's own mass takes out on the way from the " +
					"sun, which is what shades a plume's underside.",
				Default: FloatValue(1), Min: 0, Max: 8},
			{Name: "emission", Type: ParamFloat, Label: "Emission",
				Doc:     "How brightly it glows as light of its own. 0 is plain smoke.",
				Default: FloatValue(0), Min: 0, Max: 50},
		},
	})

	// ----- sky -----

	// Was Sky.Zenith/Horizon/Ground.
	RegisterKind(&Kind{
		Name: KindSkyGradient, Domains: skyOnly, Label: "Sky gradient",
		Doc: "The three stops of a gradient sky: straight up, the ring around the " +
			"viewer, and straight down. Ground is seen wherever the level has no " +
			"floor under the camera.",
		Params: []ParamDef{
			{Name: "zenith", Type: ParamColor, Label: "Zenith",
				Default: ColorValue(mgl32.Vec3{0.25, 0.45, 0.85}), Min: 0, Max: 1},
			{Name: "horizon", Type: ParamColor, Label: "Horizon",
				Default: ColorValue(mgl32.Vec3{0.72, 0.82, 0.95}), Min: 0, Max: 1},
			{Name: "ground", Type: ParamColor, Label: "Ground",
				Default: ColorValue(mgl32.Vec3{0.20, 0.18, 0.16}), Min: 0, Max: 1},
		},
	})

	// Was Sky.Faces plus Sky.Rotation. The six names are GL's own face order, so
	// the slice index IS the face offset — the convention world.SkyFaceOrder set.
	RegisterKind(&Kind{
		Name: KindSkyCubemap, Domains: skyOnly, Label: "Sky cubemap",
		Doc: "Six textures around the viewer, in the engine's Y-up, +X-is-east " +
			"convention. Setting the +X face alone fills the rest by the <base>_px " +
			"naming convention when the material is resolved.",
		Params: []ParamDef{
			{Name: "px", Type: ParamTexture, Label: "+X (east)", Default: TextureValue("")},
			{Name: "nx", Type: ParamTexture, Label: "-X (west)", Default: TextureValue("")},
			{Name: "py", Type: ParamTexture, Label: "+Y (up)", Default: TextureValue("")},
			{Name: "ny", Type: ParamTexture, Label: "-Y (down)", Default: TextureValue("")},
			{Name: "pz", Type: ParamTexture, Label: "+Z (south)", Default: TextureValue("")},
			{Name: "nz", Type: ParamTexture, Label: "-Z (north)", Default: TextureValue("")},
			{Name: "rotation", Type: ParamFloat, Label: "Rotation",
				Doc: "Degrees about +Y. Which way the art faces is a property of the " +
					"art; this lines it up with the level's own sun without repainting it.",
				Default: FloatValue(0), Min: -360, Max: 360},
		},
	})

	// Was world.SkyClouds (the deck) and world.CloudLook (a cloud volume) — the
	// same look under two owners, which is why one kind serves both.
	RegisterKind(&Kind{
		Name: KindClouds, Domains: skyOnly, Label: "Clouds",
		Doc: "A volumetric cloud deck, marched rather than drawn at infinity, so a " +
			"mountain can push up through it. The same parameters describe a cloud " +
			"volume placed in the level; altitude, thickness and range are read only " +
			"by the deck.",
		Params: []ParamDef{
			{Name: "color", Type: ParamColor, Label: "Colour",
				Doc:     "The cloud's albedo. White is a cumulus; a grey is a storm.",
				Default: ColorValue(mgl32.Vec3{1, 1, 1}), Min: 0, Max: 1},
			{Name: "coverage", Type: ParamFloat, Label: "Coverage",
				Doc: "How much of the region the noise may fill. Low is scattered puffs " +
					"with sky between; high is an overcast mass.",
				Default: FloatValue(0.45), Min: 0, Max: 1},
			{Name: "density", Type: ParamFloat, Label: "Density",
				Doc:     "Extinction per world unit in the thickest part.",
				Default: FloatValue(0.06), Min: 0, Max: 10},
			{Name: "scale", Type: ParamFloat, Label: "Scale",
				Doc: "World units per repeat of the base noise — roughly the size of the " +
					"largest lobes.",
				Default: FloatValue(450), Min: 0.5, Max: 100000},
			{Name: "detail", Type: ParamFloat, Label: "Detail",
				Doc:     "The same, for the erosion noise that breaks their edges into wisps.",
				Default: FloatValue(70), Min: 0.5, Max: 100000},
			{Name: "erosion", Type: ParamFloat, Label: "Erosion",
				Doc:     "0 is soft cotton; 1 is ragged.",
				Default: FloatValue(0.35), Min: 0, Max: 1},
			{Name: "wind", Type: ParamVec3, Label: "Wind",
				Doc: "World units a second. It scrolls the noise through the cloud, not " +
					"the region: a cloud volume stays where it was placed.",
				Default: Vec3Value(mgl32.Vec3{6, 0, 2}), Min: -1000, Max: 1000},
			{Name: "absorption", Type: ParamFloat, Label: "Absorption",
				Doc: "How much light the cloud's mass takes out on the way from the sun, " +
					"which is what shades its underside.",
				Default: FloatValue(1), Min: 0, Max: 8},
			{Name: "ambient", Type: ParamFloat, Label: "Ambient",
				Doc:     "Multiplies the sky light filling the shadowed side.",
				Default: FloatValue(1), Min: 0, Max: 8},
			{Name: "cast_shadows", Type: ParamBool, Label: "Cast shadows",
				Doc:     "Put the cloud's shadow on the ground, sprites and scatter under it.",
				Default: BoolValue(true)},
			{Name: "altitude", Type: ParamFloat, Label: "Altitude",
				Doc:     "The deck's base, in world units. Deck only.",
				Default: FloatValue(180), Min: -100000, Max: 100000},
			{Name: "thickness", Type: ParamFloat, Label: "Thickness",
				Doc: "Deck only.", Default: FloatValue(70), Min: 1, Max: 100000},
			{Name: "range", Type: ParamFloat, Label: "Range",
				Doc: "How far from the eye the deck is marched before it fades into the " +
					"horizon. It bounds the cost of a view along the deck, not merely " +
					"the look of one. Deck only.",
				Default: FloatValue(3000), Min: 10, Max: 100000},
		},
	})

	// Was world.VolumeFog, the `fog:` block on a volume.
	RegisterKind(&Kind{
		Name: KindFog, Domains: skyOnly, Label: "Fog",
		Doc: "Participating medium filling a region: extinction per world unit travelled " +
			"inside it, so it means the same thing whatever size the volume is.",
		Params: []ParamDef{
			{Name: "color", Type: ParamColor, Label: "Colour",
				Default: ColorValue(mgl32.Vec3{0.62, 0.68, 0.76}), Min: 0, Max: 1},
			{Name: "density", Type: ParamFloat, Label: "Density",
				Doc:     "Extinction per world unit inside the region.",
				Default: FloatValue(0.15), Min: 0, Max: 10},
			{Name: "max_opacity", Type: ParamFloat, Label: "Max opacity",
				Doc:     "Caps the blend, so a haze never entirely hides what stands in it.",
				Default: FloatValue(1), Min: 0, Max: 1},
		},
	})
}
