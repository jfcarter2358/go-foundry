package material

// material_resolve.go — turning a material into the per-object state the
// engine already reads.
//
// This is what makes the whole change safe. A material does NOT introduce a
// second way of drawing anything: it is resolved into exactly the fields the
// renderer read before it existed — gfx.Geometry's Unlit/Alpha/Water/Blend,
// a sprite's Blend/Billboard/Tint, a particle layer's VolumeLook, the sky's
// gradient and deck. The draw path is untouched, and a level that names no
// material behaves to the bit as it did.
//
// Every resolved struct follows the same rule about ABSENCE: a material
// overrides only what it actually declares. An effect the document does not
// carry leaves the wearer's own setting alone, which is what lets a
// `scrolling.fmat` that says nothing but `uv_scroll` be worn by two walls
// wearing different art and different alpha. That is why each optional group
// has a Has* flag rather than relying on a zero value — a scroll rate of zero
// and no scroll effect at all are different answers, and so are an alpha of 0
// and no alpha effect.
//
// The four resolvers live in one file because they are one idea four times;
// splitting them would hide how alike they are.

import (
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// ---------- surfaces ----------

// Water mirrors gfx.WaterParams. Declared here rather than imported because
// internal/gfx imports this package and not the other way round — the same
// reason gfx.BillboardMode is not in internal/world.
type Water struct {
	WaveAmplitude float32
	WaveLength    float32
	WaveSpeed     float32
	Reflectivity  float32
	ReflectColor  mgl32.Vec3
}

// Surface is a DomainSurface material resolved: what a brush face, a compiled
// surface, a model submesh or an entity attachment should be drawn with.
type Surface struct {
	// Texture is the albedo the material supplies, or "" to keep the wearer's
	// own.
	Texture string

	// Unlit is the presence of the unlit effect — the effect has no parameters,
	// so its being there IS the flag.
	Unlit bool

	HasAlpha bool
	Alpha    float32

	HasCutout       bool
	CutoutThreshold float32

	HasBlend     bool
	BlendTexture string
	BlendMask    string

	// HasNormalMap is the presence of the effect; NormalMap is the texture it
	// named, empty for "the material texture's own <name>_normal", which is
	// what the engine looked for on its own before materials existed.
	HasNormalMap bool
	NormalMap    string

	HasScroll bool
	UVScroll  mgl32.Vec2

	HasUVTransform bool
	UVOffset       mgl32.Vec2
	UVScale        mgl32.Vec2
	UVRotation     float32 // degrees, as authored; the caller converts

	HasWater bool
	Water    Water

	HasSmooth   bool
	SmoothAngle float32

	// Fx is the terrain and rock effects (material_resolve_fx.go).
	Fx SurfaceFx
}

// ResolveSurface reads the named material as a surface. The second result is
// false when there is no such material, or when it is not a surface one — a
// wall that names a sky is a mistake, and drawing it unchanged is the right
// way to show that.
func ResolveSurface(name string) (Surface, bool) {
	d := Get(name)
	if d == nil || d.Domain != DomainSurface {
		return Surface{}, false
	}
	return ResolveSurfaceDoc(d), true
}

// ResolveSurfaceDoc is ResolveSurface against a document in hand — what the
// material editor's preview uses, since the document it is showing is not in
// the library until it is saved.
func ResolveSurfaceDoc(d *Doc) Surface {
	s := Surface{Texture: d.Texture}
	s.Unlit = d.Has(KindUnlit)

	if e := d.Effect(KindAlpha); e != nil {
		s.HasAlpha, s.Alpha = true, e.Float("value")
	}
	if e := d.Effect(KindCutout); e != nil {
		s.HasCutout, s.CutoutThreshold = true, e.Float("threshold")
	}
	if e := d.Effect(KindBlend); e != nil {
		s.BlendTexture, s.BlendMask = e.Texture("texture"), e.Texture("mask")
		// Both or neither: the shader mixes by the mask's luminance and has
		// nothing to mix toward without the second texture, so half a blend is
		// no blend rather than a black wall.
		s.HasBlend = s.BlendTexture != "" && s.BlendMask != ""
	}
	if e := d.Effect(KindNormalMap); e != nil {
		s.HasNormalMap, s.NormalMap = true, e.Texture("map")
	}
	if e := d.Effect(KindUVScroll); e != nil {
		s.HasScroll, s.UVScroll = true, e.Vec2("rate")
	}
	if e := d.Effect(KindUVTransform); e != nil {
		s.HasUVTransform = true
		s.UVOffset = e.Vec2("offset")
		s.UVScale = e.Vec2("scale")
		s.UVRotation = e.Float("rotation")
	}
	if e := d.Effect(KindWater); e != nil {
		s.HasWater = true
		s.Water = Water{
			WaveAmplitude: e.Float("wave_amplitude"),
			WaveLength:    e.Float("wave_length"),
			WaveSpeed:     e.Float("wave_speed"),
			Reflectivity:  e.Float("reflectivity"),
			ReflectColor:  e.Color("reflect_color"),
		}
	}
	if e := d.Effect(KindSmooth); e != nil {
		s.HasSmooth, s.SmoothAngle = true, e.Float("angle")
	}
	resolveSurfaceFx(d, &s)
	return s
}

// NormalMapFor is the normal map a resolved surface should sample, given the
// texture it ends up wearing: the one the material named, or the texture's own
// <name>_normal when it named none. Empty means no normal map — the answer for
// a surface whose material carries no normal_map effect at all, which is the
// default and which leaves the engine's own "is there a <name>_normal.png
// beside it" lookup to decide, exactly as before.
func (s *Surface) NormalMapFor(texture string) string {
	if !s.HasNormalMap {
		return ""
	}
	if s.NormalMap != "" {
		return s.NormalMap
	}
	if s.Texture != "" {
		return s.Texture
	}
	return texture
}

// ---------- sprites and particles ----------

// Quad is a DomainSprite or DomainParticle material resolved: a billboard's
// draw state. One struct for both because a particle layer and a level sprite
// are the same quad with different owners — which is why gfx.SpriteBlend and
// gfx.BillboardMode already live below both of them.
type Quad struct {
	Texture string

	HasBlendMode bool
	BlendMode    string // one of gfx.SpriteBlendNames

	HasBillboard bool
	Billboard    string // one of gfx.BillboardModeNames

	// HasTint covers both numbers: Tint is the colour already multiplied by the
	// intensity, since nothing downstream has a place to put the two
	// separately.
	HasTint bool
	Tint    mgl32.Vec3

	HasAlpha bool
	Alpha    float32

	Lit         bool
	CastShadows bool

	HasSheet  bool
	SheetCols int
	SheetRows int

	// HasVolume and Volume are the particle domain's volumetric look. Always
	// false on a sprite material: the effect is not in DomainSprite.
	HasVolume bool
	Volume    VolumeLook
}

// VolumeLook mirrors particle.VolumeLook, minus its two ramps — a ramp is a
// curve over a particle's life rather than a material parameter, and it stays
// on the layer.
type VolumeLook struct {
	Density      float32
	NoiseScale   float32
	Displacement float32
	Billow       float32
	Erosion      float32
	Albedo       mgl32.Vec3
	Absorption   float32
	Emission     float32
}

// ResolveQuad reads the named material as a sprite or particle quad. Both
// domains are accepted from either caller: a material that says nothing but
// "additive, full billboard" describes both, and refusing to let a sprite wear
// it would be pedantry.
func ResolveQuad(name string) (Quad, bool) {
	d := Get(name)
	if d == nil || (d.Domain != DomainSprite && d.Domain != DomainParticle) {
		return Quad{}, false
	}
	return ResolveQuadDoc(d), true
}

// ResolveQuadDoc is ResolveQuad against a document in hand.
func ResolveQuadDoc(d *Doc) Quad {
	q := Quad{Texture: d.Texture}
	if e := d.Effect(KindBlendMode); e != nil {
		q.HasBlendMode, q.BlendMode = true, e.Enum("mode")
	}
	if e := d.Effect(KindBillboard); e != nil {
		q.HasBillboard, q.Billboard = true, e.Enum("mode")
	}
	if e := d.Effect(KindTint); e != nil {
		q.HasTint = true
		q.Tint = e.Color("color").Mul(e.Float("intensity"))
	}
	if e := d.Effect(KindAlpha); e != nil {
		q.HasAlpha, q.Alpha = true, e.Float("value")
	}
	q.Lit = d.Has(KindLit)
	q.CastShadows = d.Has(KindCastShadows)
	if e := d.Effect(KindSpriteSheet); e != nil {
		q.HasSheet = true
		q.SheetCols = int(e.Float("cols"))
		q.SheetRows = int(e.Float("rows"))
	}
	if e := d.Effect(KindVolume); e != nil {
		q.HasVolume = true
		q.Volume = VolumeLook{
			Density:      e.Float("density"),
			NoiseScale:   e.Float("noise_scale"),
			Displacement: e.Float("displacement"),
			Billow:       e.Float("billow"),
			Erosion:      e.Float("erosion"),
			Albedo:       e.Color("albedo"),
			Absorption:   e.Float("absorption"),
			Emission:     e.Float("emission"),
		}
	}
	return q
}

// ---------- sky ----------

// Clouds is a cloud deck or a cloud volume's look, resolved. It mirrors
// world.SkyClouds (which embeds world.CloudLook); the three deck-only fields
// are ignored by a volume, exactly as they are there.
type Clouds struct {
	Color       mgl32.Vec3
	Coverage    float32
	Density     float32
	Scale       float32
	Detail      float32
	Erosion     float32
	Wind        mgl32.Vec3
	Absorption  float32
	Ambient     float32
	CastShadows bool

	Altitude  float32
	Thickness float32
	Range     float32
}

// Fog mirrors world.VolumeFog.
type Fog struct {
	Color      mgl32.Vec3
	Density    float32
	MaxOpacity float32
}

// SkyFaces is how many textures a cubemap takes; the order is GL's own, the
// same order world.SkyFaceOrder uses, so the slice index IS the face offset.
const SkyFaces = 6

// skyFaceParams are the cubemap effect's six parameters in that order.
var skyFaceParams = [SkyFaces]string{"px", "nx", "py", "ny", "pz", "nz"}

// Sky is a DomainSky material resolved.
type Sky struct {
	HasGradient bool
	Zenith      mgl32.Vec3
	Horizon     mgl32.Vec3
	Ground      mgl32.Vec3

	HasCubemap bool
	Faces      [SkyFaces]string
	Rotation   float32

	HasTint   bool
	Tint      mgl32.Vec3
	Intensity float32

	HasClouds bool
	Clouds    Clouds

	HasFog bool
	Fog    Fog
}

// ResolveSky reads the named material as a sky.
func ResolveSky(name string) (Sky, bool) {
	d := Get(name)
	if d == nil || d.Domain != DomainSky {
		return Sky{}, false
	}
	return ResolveSkyDoc(d), true
}

// ResolveSkyDoc is ResolveSky against a document in hand.
func ResolveSkyDoc(d *Doc) Sky {
	var s Sky
	if e := d.Effect(KindSkyGradient); e != nil {
		s.HasGradient = true
		s.Zenith, s.Horizon, s.Ground = e.Color("zenith"), e.Color("horizon"), e.Color("ground")
	}
	if e := d.Effect(KindSkyCubemap); e != nil {
		s.HasCubemap = true
		for i, p := range skyFaceParams {
			s.Faces[i] = e.Texture(p)
		}
		fillCubemapFaces(&s.Faces)
		s.Rotation = e.Float("rotation")
	}
	if e := d.Effect(KindTint); e != nil {
		s.HasTint = true
		s.Tint, s.Intensity = e.Color("color"), e.Float("intensity")
	}
	if e := d.Effect(KindClouds); e != nil {
		s.HasClouds = true
		s.Clouds = Clouds{
			Color:       e.Color("color"),
			Coverage:    e.Float("coverage"),
			Density:     e.Float("density"),
			Scale:       e.Float("scale"),
			Detail:      e.Float("detail"),
			Erosion:     e.Float("erosion"),
			Wind:        e.Vec3("wind"),
			Absorption:  e.Float("absorption"),
			Ambient:     e.Float("ambient"),
			CastShadows: e.Bool("cast_shadows"),
			Altitude:    e.Float("altitude"),
			Thickness:   e.Float("thickness"),
			Range:       e.Float("range"),
		}
	}
	if e := d.Effect(KindFog); e != nil {
		s.HasFog = true
		s.Fog = Fog{
			Color:      e.Color("color"),
			Density:    e.Float("density"),
			MaxOpacity: e.Float("max_opacity"),
		}
	}
	return s
}

// fillCubemapFaces completes a cubemap from whichever face was named, by the
// <base>_px naming convention. Filling here rather than making the author type
// six near-identical names is the convenience world.SetFacesFromBase already
// offers in the editor — and, exactly as there, what ends up in the resolved
// material is six names spelled out, so the packer can see all six.
func fillCubemapFaces(faces *[SkyFaces]string) {
	base := ""
	for i, f := range faces {
		if f == "" {
			continue
		}
		suffix := "_" + skyFaceParams[i]
		if strings.HasSuffix(f, suffix) {
			base = strings.TrimSuffix(f, suffix)
			break
		}
	}
	if base == "" {
		return
	}
	for i := range faces {
		if faces[i] == "" {
			faces[i] = base + "_" + skyFaceParams[i]
		}
	}
}

// Textures lists every texture the document refers to — its own, plus the ones
// its effects name. The release packer and the unused-asset walk both need it:
// a texture reached only through a material is still a texture the game loads,
// and one that nothing reaches is still one that can be pruned.
func (d *Doc) Textures() []string {
	var out []string
	add := func(n string) {
		if n == "" {
			return
		}
		for _, have := range out {
			if have == n {
				return
			}
		}
		out = append(out, n)
	}
	add(d.Texture)
	for i := range d.Effects {
		e := &d.Effects[i]
		k := KindByName(e.Kind)
		if k == nil {
			continue
		}
		for j := range k.Params {
			if k.Params[j].Type == ParamTexture {
				add(e.Texture(k.Params[j].Name))
			}
		}
	}
	// A cubemap named by one face pulls the other five in with it.
	if e := d.Find(KindSkyCubemap); e != nil {
		var faces [SkyFaces]string
		for i, p := range skyFaceParams {
			faces[i] = e.Texture(p)
		}
		fillCubemapFaces(&faces)
		for _, f := range faces {
			add(f)
		}
	}
	return out
}
