package gfx

// material_surface.go — wearing a .fmat on a world-program draw.
//
// A Geometry or a Batch names a material the way it names a texture: by name,
// in MaterialName. What that material says is OVERLAID on the surface's own
// fields rather than replacing them, so a material carrying nothing but a
// scroll rate leaves the wall's texture, alpha and smoothing exactly as the
// level authored them. SurfaceState below is the result of that overlay, and it
// is the only thing the draw calls read.
//
// Why an overlay and not a swap: a material is shared. `conveyor.fmat` is worn
// by nine belts of five different textures, and if naming it meant taking its
// (empty) texture too, every one of them would go untextured. The Has* flags on
// material.Surface are exactly this distinction — see material_resolve.go.
//
// The resolution is done per draw rather than cached on the geometry. It is a
// read-locked map lookup and a dozen branches, paid only by surfaces that name
// a material at all: material.Get returns nil immediately for the empty name
// that everything else carries. Resolving live is also what makes the material
// editor's slider move the water in the level window on the next frame, with
// nothing to invalidate.

import (
	"foundry/internal/material"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// SurfaceState is everything a material can change about a draw through the
// world program: the surface's own settings with its material's overlaid.
type SurfaceState struct {
	Texture      string
	Alpha        float32
	Unlit        bool
	Water        WaterParams
	BlendTexture string
	BlendMask    string
	// NormalMap is the texture whose <name>_normal is bound, which is the
	// surface's own texture unless a material named another.
	NormalMap string
	// UVOffset slides the texture; UVScale and UVRotation are the material's
	// fixed transform, identity at {1,1} and 0.
	UVOffset   mgl32.Vec2
	UVScale    mgl32.Vec2
	UVRotation float32 // radians
	// CutoffBias moves the alpha-cutout threshold away from the shader's own
	// 0.5. Zero — and therefore every surface with no cutout effect — is
	// exactly what fragment.glsl did before materials existed.
	CutoffBias float32
	// Smooth and SmoothAngle are the surface's crease settings; a material's
	// smooth effect forces them on.
	Smooth      bool
	SmoothAngle float32
	// Normals is whether the mesh carries per-vertex normals at all, which is
	// what SmoothFlag needs on top of Smooth.
	Normals bool
	// Tint multiplies the albedo. The zero Vec3 is read as WHITE, not as black
	// — the uniform behind it is a bias from white (TintBiasUniform), so that a
	// surface which has never heard of tinting is untinted. Nothing but a decal
	// sets it today; see world/decal.go.
	Tint mgl32.Vec3
	// Blend is how the framebuffer blend will combine the fragment, which the
	// shading stage needs told — see SurfaceBlend.
	Blend SurfaceBlend
}

// identityUV is what UVScale means "unchanged".
var identityUV = mgl32.Vec2{1, 1}

// overlay applies a resolved material to a surface state. Shared by Geometry
// and Material so the two cannot drift on which effects win what.
func (s *SurfaceState) overlay(m material.Surface) {
	if m.Texture != "" {
		s.Texture = m.Texture
		s.NormalMap = m.Texture
	}
	if m.Unlit {
		s.Unlit = true
	}
	if m.HasAlpha {
		s.Alpha = m.Alpha
	}
	if m.HasCutout {
		s.CutoffBias = m.CutoutThreshold - DefaultAlphaCutoff
	}
	if m.HasBlend {
		s.BlendTexture, s.BlendMask = m.BlendTexture, m.BlendMask
	}
	if m.HasNormalMap {
		s.NormalMap = m.NormalMapFor(s.Texture)
	}
	if m.HasScroll {
		// The scroll RATE lives on the material; how far it has slid so far is
		// the wearer's, advanced by the tick. Adding rather than replacing lets
		// a material scroll a surface that is already scrolling — a belt on a
		// moving vehicle.
		s.UVOffset = s.UVOffset.Add(materialScrollOffset(m.UVScroll))
	}
	if m.HasUVTransform {
		s.UVOffset = s.UVOffset.Add(m.UVOffset)
		s.UVScale = mgl32.Vec2{s.UVScale.X() * m.UVScale.X(), s.UVScale.Y() * m.UVScale.Y()}
		s.UVRotation += mgl32.DegToRad(m.UVRotation)
	}
	if m.HasWater {
		s.Water = WaterParams{
			Enabled:       true,
			WaveAmplitude: m.Water.WaveAmplitude,
			WaveLength:    m.Water.WaveLength,
			WaveSpeed:     m.Water.WaveSpeed,
			Reflectivity:  m.Water.Reflectivity,
			ReflectColor:  m.Water.ReflectColor,
		}
	}
	if m.HasSmooth {
		s.Smooth = true
		if m.SmoothAngle > 0 {
			s.SmoothAngle = m.SmoothAngle
		}
	}
}

// DefaultAlphaCutoff is fragment.glsl's own cutout threshold — the halfway
// point, which is the level set that best reproduces a hard 0/255 silhouette
// once mip filtering has turned it into a ramp. A material's threshold is sent
// as a BIAS from it so that an unset uniform (zero, which is what GL gives a
// program that never writes it) is the engine's long-standing behaviour.
const DefaultAlphaCutoff = float32(0.5)

// MaterialScrollTime is how long the world has been running, in seconds — the
// clock a material's own uv_scroll rate is integrated against.
//
// A material has nowhere to keep per-surface state: the same document is worn
// by every belt in the level, and one of them cannot be allowed to advance a
// counter the others share. So its scroll is a pure function of the time,
// wrapped to [0,1) to keep the float precise however long the game has been
// up — which also means every surface wearing one material scrolls in step,
// which is what an author of a row of conveyors wants anyway. A surface with
// its OWN scroll (Geometry.UVOffset, advanced per tick by the entity that owns
// it) is unaffected and still drifts independently.
//
// Set once a frame beside TimeUniform.
var MaterialScrollTime float32

func materialScrollOffset(rate mgl32.Vec2) mgl32.Vec2 {
	return mgl32.Vec2{wrapUnit(rate.X() * MaterialScrollTime), wrapUnit(rate.Y() * MaterialScrollTime)}
}

// wrapUnit folds a coordinate into [0,1). A texture repeats, so the whole part
// is invisible, and dropping it keeps the float's precision where it matters
// after an hour of play.
func wrapUnit(v float32) float32 {
	v -= float32(int64(v))
	if v < 0 {
		v++
	}
	return v
}

// ---------- Geometry ----------

// SurfaceState is g's draw state with its material overlaid.
func (g *Geometry) SurfaceState() SurfaceState {
	s := SurfaceState{
		Texture:      g.Texture,
		Alpha:        g.Alpha,
		Unlit:        g.Unlit,
		Water:        g.Water,
		BlendTexture: g.BlendTexture,
		BlendMask:    g.BlendMask,
		NormalMap:    g.Texture,
		UVOffset:     g.UVOffset,
		UVScale:      identityUV,
		Smooth:       g.Smooth,
		SmoothAngle:  g.SmoothAngle,
		Normals:      len(g.Normals) > 0,
		Tint:         g.Tint,
		Blend:        g.Blend,
	}
	if m, ok := material.ResolveSurface(g.MaterialName); ok {
		s.overlay(m)
	}
	return s
}

// ---------- Material (a batch) ----------

// SurfaceState is the batch's draw state with its material overlaid. A batch is
// always lit and never scrolls of its own accord — those are a model
// attachment's business — so the two differ from Geometry's starting point.
func (m *Material) SurfaceState() SurfaceState {
	s := SurfaceState{
		Texture:      m.Texture,
		Alpha:        m.Alpha,
		BlendTexture: m.BlendTexture,
		BlendMask:    m.BlendMask,
		NormalMap:    m.Texture,
		UVScale:      identityUV,
		Smooth:       m.Smooth,
		Normals:      m.Normals,
	}
	if res, ok := material.ResolveSurface(m.MaterialName); ok {
		s.overlay(res)
	}
	return s
}

// ---------- flags and uniforms ----------

// SmoothFlag is the shader's `smoothShading` for this state: it has normals,
// and either its author asked for them or the Smooth Lighting setting does.
func (s *SurfaceState) SmoothFlag() int32 {
	if s.Normals && (s.Smooth || SmoothLighting) {
		return 1
	}
	return 0
}

// NormalColorFlag is the untextured path: 1 when nothing named a texture.
func (s *SurfaceState) NormalColorFlag() int32 {
	if s.Texture == "" {
		return 1
	}
	return 0
}

// UnlitFlag is the shader's `unlit`.
func (s *SurfaceState) UnlitFlag() int32 {
	if s.Unlit {
		return 1
	}
	return 0
}

// IsTransparent routes the surface into the sorted, back-to-front blended pass
// rather than the opaque one — Geometry.IsTransparent's rule, asked of the
// state a material has already had its say in, so that a material's `unlit` or
// its alpha sends the surface to the right pass.
func (s *SurfaceState) IsTransparent() bool {
	return s.Unlit || (s.Alpha > 0 && s.Alpha < 1.0) || textureHasAlpha[s.Texture]
}

// Apply pushes everything in the state that is a uniform. Every field is
// written on every draw, including the off cases, so no surface can leak its
// settings onto the one drawn after it — the rule applyWaterUniforms and
// applyBlendUniforms already followed, extended to the three uniforms
// materials added.
//
// It does NOT bind the albedo or set the model matrix: those differ between
// Geometry.Draw and Batch.Draw, and both do them either side of this call.
func (s *SurfaceState) Apply() {
	gl.Uniform1f(AlphaUniform, s.Alpha)
	gl.Uniform1i(SmoothUniform, s.SmoothFlag())
	gl.Uniform1i(NormalColorUniform, s.NormalColorFlag())
	gl.Uniform1i(UnlitUniform, s.UnlitFlag())
	ApplyUVUniforms(s.UVOffset, s.UVScale, s.UVRotation)
	gl.Uniform1f(AlphaCutoffBiasUniform, s.CutoffBias)
	applyTintUniform(TintBiasUniform, s.Tint)
	gl.Uniform1i(SurfaceBlendUniform, int32(s.Blend))
	applyWaterUniforms(&s.Water)
	applyBlendUniforms(s.BlendTexture, s.BlendMask)
	BindNormalMap(s.NormalMap, HasNormalMapUniform, NormalMapUniform)
}

// ApplyUVUniforms sets the three texture-coordinate uniforms together.
//
// Together, because they must be: the scale is carried to the shader as a BIAS
// from 1 so that an unwritten uniform is the identity, and a caller that set
// the offset while forgetting the scale would leave the previous draw's tiling
// on this one. Routing every caller through one function is what makes that
// impossible rather than merely discouraged.
func ApplyUVUniforms(offset, scale mgl32.Vec2, rotation float32) {
	gl.Uniform2f(UVOffsetUniform, offset.X(), offset.Y())
	gl.Uniform2f(UVScaleBiasUniform, scale.X()-1, scale.Y()-1)
	gl.Uniform1f(UVRotationUniform, rotation)
}

// ResetUVUniforms is ApplyUVUniforms with nothing applied: the texture where
// the vertices put it. For the draw paths that have no material of their own —
// a debug line list, a gizmo.
func ResetUVUniforms() { ApplyUVUniforms(mgl32.Vec2{}, identityUV, 0) }

// applyTintUniform sends a tint as its bias from white.
//
// The ZERO Vec3 is read as white first, which is the whole reason this is a
// function rather than three Uniform3f arguments: every Geometry in the engine
// but a decal's leaves Tint at its zero value, and subtracting one from that
// would send (-1,-1,-1) and paint the entire level black. So the zero is
// normalised to white and only then biased, which makes the bias zero — the same
// value GL gives a uniform nothing ever wrote.
//
// A tint of exactly black is still expressible, as the Vec3 (0,0,0) cannot be
// distinguished from "unset" and so must be spelled with a tiny non-zero
// component; that is the trade, and it is the same one UVScaleBiasUniform makes.
func applyTintUniform(loc int32, tint mgl32.Vec3) {
	t := TintOrWhite(tint)
	gl.Uniform3f(loc, t.X()-1, t.Y()-1, t.Z()-1)
}

// TintOrWhite reads a tint field the way the renderer does: the zero Vec3 means
// white. For the callers that have to hand a real colour to something other than
// the uniform — the editor's swatch, a batch key.
func TintOrWhite(tint mgl32.Vec3) mgl32.Vec3 {
	if tint == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return tint
}

// ---------- drawing through another copy of the world program ----------

// ApplyTo is Apply against a program that is not the global Program: the
// material editor's preview compiles a second copy of the world shader so its
// uniforms cannot land in the one the level window is drawing with (see
// render.PrepareWorldProgramForPreview).
//
// It resolves every location through the per-program memo rather than through
// the cached globals, which belong to Program alone — the cost is a map lookup
// per uniform, paid by one preview surface a frame rather than by every wall in
// the level, which is why Apply does not simply call this.
func (s *SurfaceState) ApplyTo(prog uint32) {
	loc := func(name string) int32 { return UniformLoc(prog, name) }
	gl.Uniform1f(loc("alpha\x00"), s.Alpha)
	gl.Uniform1i(loc("smoothShading\x00"), s.SmoothFlag())
	gl.Uniform1i(loc("normalColor\x00"), s.NormalColorFlag())
	gl.Uniform1i(loc("unlit\x00"), s.UnlitFlag())
	gl.Uniform2f(loc("uvOffset\x00"), s.UVOffset.X(), s.UVOffset.Y())
	gl.Uniform2f(loc("uvScaleBias\x00"), s.UVScale.X()-1, s.UVScale.Y()-1)
	gl.Uniform1f(loc("uvRotation\x00"), s.UVRotation)
	gl.Uniform1f(loc("alphaCutoffBias\x00"), s.CutoffBias)
	applyTintUniform(loc("tintBias\x00"), s.Tint)
	gl.Uniform1i(loc("surfaceBlend\x00"), int32(s.Blend))

	if !s.Water.Enabled {
		gl.Uniform1i(loc("isWater\x00"), 0)
	} else {
		gl.Uniform1i(loc("isWater\x00"), 1)
		gl.Uniform1f(loc("waveAmplitude\x00"), s.Water.WaveAmplitude)
		gl.Uniform1f(loc("waveLength\x00"), s.Water.WaveLength)
		gl.Uniform1f(loc("waveSpeed\x00"), s.Water.WaveSpeed)
		gl.Uniform1f(loc("reflectivity\x00"), s.Water.Reflectivity)
		gl.Uniform3f(loc("reflectColor\x00"),
			s.Water.ReflectColor.X(), s.Water.ReflectColor.Y(), s.Water.ReflectColor.Z())
	}

	if s.BlendTexture == "" || s.BlendMask == "" {
		gl.Uniform1i(loc("hasBlend\x00"), 0)
	} else {
		gl.Uniform1i(loc("hasBlend\x00"), 1)
		gl.ActiveTexture(gl.TEXTURE0 + BlendTexUnit)
		gl.BindTexture(gl.TEXTURE_2D, Textures[s.BlendTexture])
		gl.Uniform1i(loc("blendTex\x00"), BlendTexUnit)
		gl.ActiveTexture(gl.TEXTURE0 + BlendMaskUnit)
		gl.BindTexture(gl.TEXTURE_2D, Textures[s.BlendMask])
		gl.Uniform1i(loc("blendMask\x00"), BlendMaskUnit)
		gl.ActiveTexture(gl.TEXTURE0)
	}

	BindNormalMap(s.NormalMap, loc("hasNormalMap\x00"), loc("normalMap\x00"))
}

// DrawThroughProgram draws the geometry with its material applied, through a
// caller-supplied copy of the world program under a caller-supplied model
// matrix. The program's per-frame state — camera, projection, lights, shadow
// samplers — is the caller's business and must already be set.
//
// The preview's door into the real shading path. Everything a material can
// change is read off the same SurfaceState the level's own draws use, so the
// swatch and the wall cannot disagree.
func (g *Geometry) DrawThroughProgram(prog uint32, model mgl32.Mat4) {
	if len(g.Vertices) == 0 || g.Vao == 0 {
		return
	}
	st := g.SurfaceState()
	gl.UseProgram(prog)
	gl.UniformMatrix4fv(UniformLoc(prog, "model\x00"), 1, false, &model[0])
	st.ApplyTo(prog)
	gl.BindVertexArray(g.Vao)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[st.Texture])
	gl.Uniform1i(UniformLoc(prog, "tex\x00"), 0)
	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}
