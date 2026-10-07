package render

import (
	"fmt"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/world"
	"log"
	"math"
	"strings"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// ShadowMapSize is the resolution of one directional cascade layer (square).
const ShadowMapSize = 2048

// PointShadowMapSize is the resolution of each cubemap face for point lights.
const PointShadowMapSize = 1024

// ShadowCascades is the number of cascade slices rendered per shadow-casting
// directional light. A single fixed-size ortho box can either cover a large
// level or stay sharp up close, never both; splitting the view frustum into
// cascades gives each slice its own box so near shadows keep their detail while
// distant geometry is still covered. fragment.glsl mirrors this as
// SHADOW_CASCADES; change both together.
const ShadowCascades = 3

// ShadowMaxDistance is how far from the camera directional shadows are
// rendered. Beyond it surfaces are lit with no shadow term, faded in over
// shadowFadeFraction of the last cascade so the cutoff isn't a visible line.
const ShadowMaxDistance = 300.0

// ShadowCascadeSplitLambda blends the cascade split distances between a uniform
// (0.0) and a logarithmic (1.0) distribution. Fully logarithmic wastes the far
// cascade on a thin shell; fully uniform starves the near cascade.
const ShadowCascadeSplitLambda = 0.75

// shadowSplitNear is the near distance feeding the logarithmic term of the split
// distribution. The camera's real near plane (0.1) would put the first split
// only a couple of units out.
const shadowSplitNear = 1.0

// ShadowCascadeBlend is the fraction of a cascade's depth range over which it
// cross-fades into the next one, hiding the resolution seam at the split. Each
// cascade's box is extended back over the preceding blend band so both
// cascades hold valid depth there. fragment.glsl and biome_light.glsl mirror
// this as SHADOW_CASCADE_BLEND; change all three together.
//
// A quarter: at 0.12 the band was 3.6 m at the first split and the hand-over
// read as a line across the ground. The filter matching (DirCascade.Texel)
// does most of the work; the band only has to hide the difference in depth
// resolution that is left.
const ShadowCascadeBlend = 0.25

// shadowFadeFraction is the fraction of ShadowMaxDistance over which the shadow
// term fades to zero as it approaches the limit. Applied by fragment.glsl, which
// mirrors it as SHADOW_FADE_FRACTION; kept here as the documented pair.
const shadowFadeFraction = 0.15

// ShadowCasterExtrusion is how far a cascade's light frustum is pulled back
// toward the light beyond the slice it covers, so geometry standing between the
// light and the slice still casts into it.
const ShadowCasterExtrusion = 150.0

// ShadowWorldBias is the depth-comparison bias in world units. Each cascade
// converts it into its own normalized depth range, so the near cascade isn't
// stuck with the far cascade's much coarser bias.
const ShadowWorldBias = 0.075

// PointShadowMap holds GPU resources for a single point-light cubemap shadow.
type PointShadowMap struct {
	FBO     uint32
	Cubemap uint32
	// The static layer (point_shadow_cache.go): the depth of everything that
	// does not move, kept apart so a frame whose only change is a character
	// walking past can start from a copy of it rather than redraw the room.
	// Made the first time the light has anything moving in reach; zero until.
	StaticFBO     uint32
	StaticCubemap uint32
}

// ShadowState holds the shadow mapping pipeline state.
type ShadowState struct {
	// Directional light shadow program
	Program      uint32
	ModelUniform int32
	LSMUniform   int32

	// CascadeArray is a depth texture array holding ShadowCascades consecutive
	// layers per shadow-casting directional light; CascadeFBO renders one layer
	// at a time via glFramebufferTextureLayer.
	//
	// One array texture rather than a sampler2D per cascade is not a
	// convenience: MaxDirLights * ShadowCascades separate samplers plus the
	// point-light cubemaps and the albedo would exceed the 16 fragment texture
	// units GL 4.3 guarantees. The array costs one unit for all of them.
	CascadeArray  uint32
	CascadeFBO    uint32
	CascadeLayers int32

	// Point light cubemap shadow program
	CubeProgram      uint32
	CubeModelUniform int32
	CubeLSMUniform   int32
	CubeLightPos     int32
	CubeFarPlane     int32
	PointMaps        []PointShadowMap

	// DummyCubemap is a 1x1 cubemap bound to unused pointShadowMap units so
	// that samplerCube uniforms never reference a texture unit holding a 2D
	// texture (which is an invalid operation in OpenGL and causes undefined
	// behavior on many drivers).
	DummyCubemap uint32
}

var Shadows ShadowState

// InitShadows compiles the depth-only shaders and prepares the shadow pipeline.
func InitShadows(shadowVertPath, shadowFragPath, cubeVertPath, cubeFragPath string) error {
	if err := loadShadowProgram(shadowVertPath, shadowFragPath); err != nil {
		return fmt.Errorf("shadow program: %w", err)
	}
	Shadows.ModelUniform = gl.GetUniformLocation(Shadows.Program, gl.Str("model\x00"))
	Shadows.LSMUniform = gl.GetUniformLocation(Shadows.Program, gl.Str("lightSpaceMatrix\x00"))
	log.Printf("dir shadow program linked OK (id=%d)", Shadows.Program)

	if err := loadCubeShadowProgram(cubeVertPath, cubeFragPath); err != nil {
		return fmt.Errorf("cube shadow program: %w", err)
	}
	Shadows.CubeModelUniform = gl.GetUniformLocation(Shadows.CubeProgram, gl.Str("model\x00"))
	Shadows.CubeLSMUniform = gl.GetUniformLocation(Shadows.CubeProgram, gl.Str("lightSpaceMatrix\x00"))
	Shadows.CubeLightPos = gl.GetUniformLocation(Shadows.CubeProgram, gl.Str("lightPos\x00"))
	Shadows.CubeFarPlane = gl.GetUniformLocation(Shadows.CubeProgram, gl.Str("farPlane\x00"))
	log.Printf("cube shadow program linked OK (id=%d)", Shadows.CubeProgram)

	// Create a 1x1 dummy cubemap so unused pointShadowMap sampler uniforms
	// always reference a valid cubemap texture instead of defaulting to unit 0
	// (which holds a sampler2D). A samplerCube reading from a unit with a 2D
	// texture bound is an invalid GL operation and causes undefined behavior.
	Shadows.DummyCubemap = createDummyCubemap()
	log.Printf("dummy cubemap created (id=%d)", Shadows.DummyCubemap)

	return nil
}

func loadShadowProgram(vertPath, fragPath string) error {
	return LinkProgram(vertPath, fragPath, &Shadows.Program)
}

func loadCubeShadowProgram(vertPath, fragPath string) error {
	return LinkProgram(vertPath, fragPath, &Shadows.CubeProgram)
}

func LinkProgram(vertPath, fragPath string, out *uint32) error {
	vertSrc, err := gfx.ReadShaderFile(vertPath)
	if err != nil {
		return err
	}
	// Both shadow passes skin, or an animated entity would cast the silhouette
	// of its rest pose while the mesh moved.
	vertSrc, err = gfx.InjectSkinning(vertSrc)
	if err != nil {
		return err
	}
	fragSrc, err := gfx.ReadShaderFile(fragPath)
	if err != nil {
		return err
	}
	vs, err := gfx.CompileShader(vertSrc, gl.VERTEX_SHADER)
	if err != nil {
		return err
	}
	fs, err := gfx.CompileShader(fragSrc, gl.FRAGMENT_SHADER)
	if err != nil {
		return err
	}
	*out = gl.CreateProgram()
	gl.AttachShader(*out, vs)
	gl.AttachShader(*out, fs)
	gl.LinkProgram(*out)
	gfx.ForgetUniformLocations(*out)

	var status int32
	gl.GetProgramiv(*out, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var logLen int32
		gl.GetProgramiv(*out, gl.INFO_LOG_LENGTH, &logLen)
		infoLog := strings.Repeat("\x00", int(logLen+1))
		gl.GetProgramInfoLog(*out, logLen, nil, gl.Str(infoLog))
		return fmt.Errorf("program link: %s", infoLog)
	}
	gl.DeleteShader(vs)
	gl.DeleteShader(fs)
	return nil
}

// ---------- Directional cascade shadow maps ----------

// EnsureCascadeArray (re)allocates the cascade depth array so it holds exactly
// layers layers. Layers are only allocated for lights that actually cast, so a
// level with a single sun pays for ShadowCascades layers rather than
// MaxDirLights * ShadowCascades.
func EnsureCascadeArray(layers int32) {
	if Shadows.CascadeLayers == layers {
		return
	}
	if Shadows.CascadeArray != 0 {
		gl.DeleteTextures(1, &Shadows.CascadeArray)
		Shadows.CascadeArray = 0
	}
	Shadows.CascadeLayers = layers
	if layers <= 0 {
		return
	}

	Shadows.CascadeArray = newCascadeTexture(layers)

	if Shadows.CascadeFBO == 0 {
		gl.GenFramebuffers(1, &Shadows.CascadeFBO)
	}
	prevFBO := boundFramebuffer()
	gl.BindFramebuffer(gl.FRAMEBUFFER, Shadows.CascadeFBO)
	gl.FramebufferTextureLayer(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, Shadows.CascadeArray, 0, 0)
	gl.DrawBuffer(gl.NONE)
	gl.ReadBuffer(gl.NONE)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("WARNING: cascade shadow FBO incomplete (status=0x%X)", st)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, prevFBO)

	log.Printf("cascade shadow array allocated (tex=%d, fbo=%d, %dx%d x %d layers)",
		Shadows.CascadeArray, Shadows.CascadeFBO, ShadowMapSize, ShadowMapSize, layers)
}

// newCascadeTexture is a comparison-sampled depth array of layers layers.
func newCascadeTexture(layers int32) uint32 {
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, tex)
	gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.DEPTH_COMPONENT24,
		ShadowMapSize, ShadowMapSize, layers, 0,
		gl.DEPTH_COMPONENT, gl.FLOAT, nil)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	// Comparison sampling: the shaders bind this as a sampler2DArrayShadow and
	// pass the fragment's own depth as the reference, so each tap comes back as
	// the BILINEARLY FILTERED fraction of its four texels that the fragment is
	// in front of, not a yes/no answer for one texel.
	//
	// That is what LINEAR filtering above is for. A depth texture sampled
	// without compare mode averages stored depths and then compares once, which
	// is meaningless — the average of "1 m away" and "40 m away" is a surface
	// that isn't there — so every tap was effectively a hard nearest-texel test
	// and shadow edges landed on texel boundaries. Close to the camera, where a
	// cascade-0 texel is centimetres across but tens of screen pixels wide,
	// those boundaries are the blocky staircase you see on a nearby model.
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_COMPARE_MODE, gl.COMPARE_REF_TO_TEXTURE)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_COMPARE_FUNC, gl.LEQUAL)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_BORDER)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_BORDER)
	// Border depth 1.0 is the far plane, so a lookup that falls outside the
	// cascade box compares as lit rather than shadowed.
	borderColor := [4]float32{1, 1, 1, 1}
	gl.TexParameterfv(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_BORDER_COLOR, &borderColor[0])
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)
	return tex
}

// createDummyCubemap allocates a 1x1 cubemap with depth=1.0 on all faces.
func createDummyCubemap() uint32 {
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, tex)
	pixel := []float32{1.0}
	for face := 0; face < 6; face++ {
		gl.TexImage2D(uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+face), 0, gl.DEPTH_COMPONENT24,
			1, 1, 0, gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr(pixel))
	}
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_R, gl.CLAMP_TO_EDGE)
	// Compared like the real ones, so the samplerCubeShadow it stands in for
	// reads a defined "lit" rather than whatever an unset compare mode gives.
	pointShadowCompare()
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, 0)
	return tex
}

// pointShadowCompare makes the bound cubemap a comparison texture: a
// samplerCubeShadow tap hands the hardware the fragment's own distance and
// gets back the filtered compare of the 2x2 texels around it
// (point_shadow.glsl). LEQUAL is "lit": the fragment is no farther than the
// nearest caster the face saw. A depth blit (the static layer's copy) ignores
// the compare mode.
func pointShadowCompare() {
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_COMPARE_MODE, gl.COMPARE_REF_TO_TEXTURE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_COMPARE_FUNC, gl.LEQUAL)
}

// ---------- Point light cubemap shadow maps ----------

func EnsurePointShadowMaps(count int) {
	for len(Shadows.PointMaps) < count {
		pm := createPointShadowMap()
		Shadows.PointMaps = append(Shadows.PointMaps, pm)
		log.Printf("created point shadow map %d (cube=%d, fbo=%d)", len(Shadows.PointMaps)-1, pm.Cubemap, pm.FBO)
	}
	for len(Shadows.PointMaps) > count {
		pm := Shadows.PointMaps[len(Shadows.PointMaps)-1]
		gl.DeleteFramebuffers(1, &pm.FBO)
		gl.DeleteTextures(1, &pm.Cubemap)
		if pm.StaticFBO != 0 {
			gl.DeleteFramebuffers(1, &pm.StaticFBO)
			gl.DeleteTextures(1, &pm.StaticCubemap)
		}
		Shadows.PointMaps = Shadows.PointMaps[:len(Shadows.PointMaps)-1]
	}
	// A deleted map's name can be handed out again, so what the old one held
	// must not be credited to the new one (point_shadow_cache.go).
	if len(pointShadowStates) > count {
		pointShadowStates = pointShadowStates[:count]
	}
}

func createPointShadowMap() PointShadowMap {
	var pm PointShadowMap
	pm.Cubemap, pm.FBO = createDepthCube()
	return pm
}

// createDepthCube makes one depth cubemap and a framebuffer to render its faces
// through.
func createDepthCube() (cube, fbo uint32) {
	var pm PointShadowMap
	gl.GenTextures(1, &pm.Cubemap)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, pm.Cubemap)
	for face := 0; face < 6; face++ {
		gl.TexImage2D(uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+face), 0, gl.DEPTH_COMPONENT24,
			PointShadowMapSize, PointShadowMapSize, 0,
			gl.DEPTH_COMPONENT, gl.FLOAT, nil)
	}
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_R, gl.CLAMP_TO_EDGE)
	// LINEAR with a compare: the hardware's 2x2 percentage-closer filter.
	pointShadowCompare()

	prevFBO := boundFramebuffer()
	gl.GenFramebuffers(1, &pm.FBO)
	gl.BindFramebuffer(gl.FRAMEBUFFER, pm.FBO)
	// Attach the first face initially; we'll switch per-face during rendering.
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT,
		gl.TEXTURE_CUBE_MAP_POSITIVE_X, pm.Cubemap, 0)
	gl.DrawBuffer(gl.NONE)
	gl.ReadBuffer(gl.NONE)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("WARNING: point shadow FBO incomplete (status=0x%X)", st)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, prevFBO)
	return pm.Cubemap, pm.FBO
}

// cubeFaceViews returns the 6 view matrices for a cubemap centered at pos.
// Order: +X, -X, +Y, -Y, +Z, -Z (matches GL_TEXTURE_CUBE_MAP_POSITIVE_X + i).
func cubeFaceViews(pos mgl32.Vec3) [6]mgl32.Mat4 {
	return [6]mgl32.Mat4{
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{1, 0, 0}), mgl32.Vec3{0, -1, 0}),
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{-1, 0, 0}), mgl32.Vec3{0, -1, 0}),
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{0, 1, 0}), mgl32.Vec3{0, 0, 1}),
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{0, -1, 0}), mgl32.Vec3{0, 0, -1}),
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{0, 0, 1}), mgl32.Vec3{0, -1, 0}),
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{0, 0, -1}), mgl32.Vec3{0, -1, 0}),
	}
}

// ---------- Light-space matrix helpers ----------

// DirCascade is one cascade of one directional light's shadow.
type DirCascade struct {
	Matrix mgl32.Mat4
	Bias   float32 // ShadowWorldBias expressed in this cascade's depth range
	// Texel is one texel of this cascade in world units. Each cascade's box is
	// several times the last one's, so a fixed filter footprint in texels made
	// shadow edges jump that many times softer at every split; the shaders
	// widen a cascade's filter toward the next one's texel as the split nears
	// (cascadeTexel), so the two agree where they hand over.
	Texel float32
}

// DirShadow is a directional light's full cascade set. BaseLayer indexes the
// first of its ShadowCascades layers in Shadows.CascadeArray, or -1 when the
// light casts no shadows.
type DirShadow struct {
	BaseLayer int32
	Cascades  [ShadowCascades]DirCascade
}

// ShadowFrame is one frame's directional shadow setup: produced by
// RenderShadowPass, consumed by UploadShadowUniformsTo.
type ShadowFrame struct {
	Splits [ShadowCascades]float32 // view-space distance where each cascade ends
	Lights []DirShadow             // parallel to LightSet.Directional
	// BakedSunCast says directional light BakedSun is a compiled level's
	// baked sun (world.LightSet.BakedSun) and its cascades were drawn this
	// frame, so baked surfaces re-shadow their share of it by them
	// (fragment.glsl, bakedSunLight). A zero frame has neither.
	BakedSunCast bool
	BakedSun     int
	// VSM is the sun's virtual shadow map when this frame drew one in place
	// of that light's cascades (vsm.go); nil otherwise.
	VSM *VSMFrame
}

// newShadowFrame returns a frame with every light marked as non-casting, so the
// early-out paths still hand back something the uniform upload can consume.
func newShadowFrame(numDir int, near float32) ShadowFrame {
	f := ShadowFrame{
		Splits: CascadeSplits(near, ShadowMaxDistance),
		Lights: make([]DirShadow, numDir),
	}
	for i := range f.Lights {
		f.Lights[i].BaseLayer = -1
		for c := range f.Lights[i].Cascades {
			f.Lights[i].Cascades[c].Matrix = mgl32.Ident4()
		}
	}
	return f
}

// CascadeSplits returns the view-space distance at which each cascade ends,
// using the practical split scheme — a lambda-weighted mix of a logarithmic and
// a uniform distribution.
func CascadeSplits(near, far float32) [ShadowCascades]float32 {
	var out [ShadowCascades]float32
	logNear := near
	if logNear < shadowSplitNear {
		logNear = shadowSplitNear
	}
	if far <= logNear {
		far = logNear * 2
	}
	for i := 0; i < ShadowCascades; i++ {
		p := float64(i+1) / float64(ShadowCascades)
		logSplit := float64(logNear) * math.Pow(float64(far/logNear), p)
		uniSplit := float64(near) + float64(far-near)*p
		out[i] = float32(ShadowCascadeSplitLambda*logSplit + (1-ShadowCascadeSplitLambda)*uniSplit)
	}
	// Pin the last split exactly, so the shader's "beyond the last split means
	// unshadowed" test lines up with ShadowMaxDistance.
	out[ShadowCascades-1] = far
	return out
}

// cascadeSliceNear returns the near distance of cascade c. Cascades after the
// first reach back over the preceding cascade's cross-fade band so both hold
// valid depth where the shader blends between them.
func cascadeSliceNear(c int, near float32, splits [ShadowCascades]float32) float32 {
	if c == 0 {
		return near
	}
	prev := splits[c-1]
	prevPrev := near
	if c > 1 {
		prevPrev = splits[c-2]
	}
	return prev - ShadowCascadeBlend*(prev-prevPrev)
}

// perspectiveParams recovers the vertical half-FOV tangent, aspect ratio and
// near distance from an mgl32.Perspective matrix. Reading them back off the
// matrix keeps the cascade fit correct without plumbing the projection's
// authoring parameters (and any future window resize) through to the shadow code.
func perspectiveParams(p mgl32.Mat4) (tanHalfV, aspect, near float32) {
	tanHalfV, aspect, near = 1, 1, 0.1
	if p[5] != 0 {
		tanHalfV = 1 / p[5]
	}
	if p[0] != 0 {
		aspect = p[5] / p[0]
	}
	if d := p[10] - 1; d != 0 {
		near = p[14] / d
	}
	return
}

// frustumSliceCorners returns the 8 world-space corners of the view frustum
// between near and far. view is the camera (world→view) matrix.
func frustumSliceCorners(view mgl32.Mat4, tanHalfV, aspect, near, far float32) [8]mgl32.Vec3 {
	inv := view.Inv()
	var out [8]mgl32.Vec3
	i := 0
	for _, z := range [2]float32{near, far} {
		h := z * tanHalfV
		w := h * aspect
		for _, sy := range [2]float32{-1, 1} {
			for _, sx := range [2]float32{-1, 1} {
				p := inv.Mul4x1(mgl32.Vec4{sx * w, sy * h, -z, 1})
				out[i] = mgl32.Vec3{p[0], p[1], p[2]}
				i++
			}
		}
	}
	return out
}

// CascadeLightMatrix fits a light-space orthographic box around a frustum slice
// and returns its light-space matrix plus the box's depth range (world units,
// needed to normalize the depth bias).
//
// The box is sized from the slice's bounding *sphere*, not its light-space AABB:
// a sphere is invariant under camera rotation, so shadow texels stay put on
// surfaces as the player turns instead of swimming. Snapping the box origin to
// the texel grid then kills the remaining shimmer from camera translation.
func CascadeLightMatrix(dir mgl32.Vec3, corners [8]mgl32.Vec3) (mgl32.Mat4, float32) {
	lightDir := dir.Normalize()

	var center mgl32.Vec3
	for _, c := range corners {
		center = center.Add(c)
	}
	center = center.Mul(1.0 / float32(len(corners)))

	var radius float32
	for _, c := range corners {
		if d := c.Sub(center).Len(); d > radius {
			radius = d
		}
	}
	// Quantize the radius so a sub-texel change in slice size can't resize the
	// box — and thus shift every texel in it — from one frame to the next.
	radius = float32(math.Ceil(float64(radius)*16)) / 16
	if radius < 1 {
		radius = 1
	}

	up := mgl32.Vec3{0, 1, 0}
	if geom.Abs32(lightDir.Y()) > 0.99 {
		up = mgl32.Vec3{0, 0, 1}
	}
	// Depth runs from the light-side plane (0) through the far side of the
	// sphere, with ShadowCasterExtrusion of headroom in front of it for
	// off-slice casters.
	depth := 2*radius + ShadowCasterExtrusion
	eye := center.Sub(lightDir.Mul(radius + ShadowCasterExtrusion))
	view := mgl32.LookAtV(eye, center, up)
	lsm := mgl32.Ortho(-radius, radius, -radius, radius, 0, depth).Mul4(view)

	// Texel snap: nudge the projection in NDC so the slice centre lands exactly
	// on a shadow-map texel.
	p := lsm.Mul4x1(mgl32.Vec4{center.X(), center.Y(), center.Z(), 1})
	half := float32(ShadowMapSize) * 0.5
	tx, ty := p.X()*half, p.Y()*half
	dx := (float32(math.Round(float64(tx))) - tx) / half
	dy := (float32(math.Round(float64(ty))) - ty) / half
	lsm = mgl32.Translate3D(dx, dy, 0).Mul4(lsm)

	return lsm, depth
}

// ---------- Shadow passes ----------

// RenderShadowPass renders the directional cascade shadow maps for the current
// camera. view is the camera (world→view) matrix and projection its perspective
// matrix; the cascades are fitted to slices of that frustum, so shadows follow
// the view instead of stopping at a fixed radius around the camera.
func RenderShadowPass(lights world.LightSet, level *world.Level, view, projection mgl32.Mat4) ShadowFrame {
	tanHalfV, aspect, near := perspectiveParams(projection)

	numDir := len(lights.Directional)
	if numDir > world.MaxDirLights {
		numDir = world.MaxDirLights
	}
	frame := newShadowFrame(len(lights.Directional), near)
	// The sun's virtual shadow map, drawn in place of its cascades (vsm.go,
	// Nanite Phase 5): the light it took, or -1.
	vsmLight := vsmShadowPass(lights, level, view, projection, &frame)

	shadowCount := 0
	for i := 0; i < numDir; i++ {
		if lights.Directional[i].CastsShadows() && i != vsmLight {
			shadowCount++
		}
	}
	EnsureCascadeArray(int32(shadowCount * ShadowCascades))
	if shadowCount == 0 || Shadows.CascadeArray == 0 {
		return frame
	}
	// A compiled level's baked surfaces carry the sun's share, and swap it
	// for the sun these cascades shadow (see ShadowFrame.BakedSun).
	if level != nil && level.Static != nil {
		if s := lights.BakedSun(); s >= 0 && s < numDir {
			frame.BakedSunCast, frame.BakedSun = true, s
		}
	}

	gl.UseProgram(Shadows.Program)
	// Known, not asked: see frame_target.go.
	prevViewport := boundViewport()
	prevFBO := boundFramebuffer()
	gl.Viewport(0, 0, ShadowMapSize, ShadowMapSize)
	gl.Disable(gl.CULL_FACE)
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(1.1, 4.0)
	gl.BindFramebuffer(gl.FRAMEBUFFER, Shadows.CascadeFBO)

	layer := int32(0)
	for i := 0; i < numDir; i++ {
		dl := lights.Directional[i]
		if !dl.CastsShadows() || i == vsmLight {
			continue
		}
		frame.Lights[i].BaseLayer = layer

		for c := 0; c < ShadowCascades; c++ {
			sliceNear := cascadeSliceNear(c, near, frame.Splits)
			corners := frustumSliceCorners(view, tanHalfV, aspect, sliceNear, frame.Splits[c])
			lsm, depth := CascadeLightMatrix(dl.Direction, corners)
			// The box is 2r across, r being what the depth was built from.
			texel := (depth - ShadowCasterExtrusion) / ShadowMapSize
			frame.Lights[i].Cascades[c] = DirCascade{Matrix: lsm, Bias: ShadowWorldBias / depth, Texel: texel}

			gl.FramebufferTextureLayer(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT,
				Shadows.CascadeArray, 0, layer)
			gl.Clear(gl.DEPTH_BUFFER_BIT)
			gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])

			// Cull to the cascade's own box rather than the camera frustum: a
			// caster behind the player can still drop a shadow into view, and
			// the box already extends toward the light to include it.
			cull := ExtractFrustum(lsm)
			renderSceneShadow(level, Shadows.Program, &gfx.ShadowSkinUniforms, Shadows.ModelUniform, &cull)
			// Cast shadow-casting sprites, turned to face the LIGHT rather than
			// the camera — a camera-facing quad rendered from here would be
			// edge-on and cast nothing. Restores Shadows.Program.
			ProfileBegin("sprites")
			RenderSpriteDirShadow(level, lsm, dl.Direction, gfx.Cam.Position, &cull)
			ProfileEnd()
			// Cast the scatter volumes' foliage. The cascade index and its own
			// split distance both go in: they are what stop a blade of grass
			// from being re-instanced into a cascade whose shadow could never
			// usefully reach it. Restores Shadows.Program.
			ProfileBegin("scatter")
			RenderScatterDirShadow(level, lsm, c, frame.Splits[c], gfx.Cam.Position, BiomeWindTime, &cull)
			ProfileEnd()
			// Terrain foliage's trees, shrubs and rocks. Restores
			// Shadows.Program.
			ProfileBegin("foliage")
			RenderFoliageDirShadow(level, lsm, c, &cull)
			ProfileEnd()
			layer++
		}
	}

	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.BindFramebuffer(gl.FRAMEBUFFER, prevFBO)
	gl.Viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3])
	gl.UseProgram(gfx.Program)

	return frame
}

// RenderPointShadowPass renders cubemap shadow maps for point lights.
func RenderPointShadowPass(lights world.LightSet, level *world.Level) {
	shadowCount := 0
	for _, pl := range lights.Points {
		if pl.CastsShadows() {
			shadowCount++
		}
	}
	EnsurePointShadowMaps(shadowCount)
	DebugStats.PointShadowRenders, DebugStats.PointShadows = 0, shadowCount

	if shadowCount == 0 {
		return
	}

	gl.UseProgram(Shadows.CubeProgram)
	// Known, not asked: see frame_target.go.
	prevViewport := boundViewport()
	prevFBO := boundFramebuffer()
	gl.Viewport(0, 0, PointShadowMapSize, PointShadowMapSize)
	gl.Disable(gl.CULL_FACE)
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(1.1, 4.0)

	mapIdx := 0
	for _, pl := range lights.Points {
		if !pl.CastsShadows() {
			continue
		}
		if mapIdx >= len(Shadows.PointMaps) {
			break
		}

		pm := &Shadows.PointMaps[mapIdx]
		// The six faces' matrices and frusta, made once and shared by the
		// planner and the draws, so a face is signed by exactly what it draws.
		lsms, culls := pointShadowFaces(pl)
		// What to redraw, if anything: nothing when no caster in reach has
		// changed, the moving casters over a copy of the static layer in just
		// the faces whose casters have, everything when the static world has.
		// See point_shadow_cache.go.
		plan := planPointShadowIn(mapIdx, level, pl, pm, &culls)
		if plan.keep() {
			mapIdx++
			continue
		}
		DebugStats.PointShadowRenders++
		farPlane := pl.Radius

		gl.Uniform3f(Shadows.CubeLightPos, pl.Position[0], pl.Position[1], pl.Position[2])
		gl.Uniform1f(Shadows.CubeFarPlane, farPlane)

		if plan.static {
			// The static layer into its own cube, to be copied from until
			// the static world next changes.
			gl.BindFramebuffer(gl.FRAMEBUFFER, pm.StaticFBO)
			for face := 0; face < 6; face++ {
				gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT,
					uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+face), pm.StaticCubemap, 0)
				gl.Clear(gl.DEPTH_BUFFER_BIT)
				gl.UniformMatrix4fv(Shadows.CubeLSMUniform, 1, false, &lsms[face][0])
				renderSceneShadowLayers(level, Shadows.CubeProgram, &gfx.ShadowCubeSkinUniforms,
					Shadows.CubeModelUniform, &culls[face], shadowStaticLayer)
			}
		}

		layers := shadowMovingLayer
		if plan.all {
			layers = shadowAllLayers
		}
		gl.BindFramebuffer(gl.FRAMEBUFFER, pm.FBO)
		for face := 0; face < 6; face++ {
			if plan.faces&(1<<face) == 0 {
				continue // nothing this face sees has changed
			}
			target := uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X + face)
			if layers == shadowAllLayers {
				gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, target, pm.Cubemap, 0)
				gl.Clear(gl.DEPTH_BUFFER_BIT)
			} else {
				// Start from the static layer's depth rather than a clear.
				gl.BindFramebuffer(gl.READ_FRAMEBUFFER, pm.StaticFBO)
				gl.FramebufferTexture2D(gl.READ_FRAMEBUFFER, gl.DEPTH_ATTACHMENT, target, pm.StaticCubemap, 0)
				gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, pm.FBO)
				gl.FramebufferTexture2D(gl.DRAW_FRAMEBUFFER, gl.DEPTH_ATTACHMENT, target, pm.Cubemap, 0)
				gl.BlitFramebuffer(0, 0, PointShadowMapSize, PointShadowMapSize,
					0, 0, PointShadowMapSize, PointShadowMapSize, gl.DEPTH_BUFFER_BIT, gl.NEAREST)
				gl.BindFramebuffer(gl.FRAMEBUFFER, pm.FBO)
			}

			lsm := lsms[face]
			gl.UniformMatrix4fv(Shadows.CubeLSMUniform, 1, false, &lsm[0])

			cull := &culls[face]
			renderSceneShadowLayers(level, Shadows.CubeProgram, &gfx.ShadowCubeSkinUniforms,
				Shadows.CubeModelUniform, cull, layers)
			// Sprites into this cube face. A point light has a position, so the
			// quads converge on it exactly as they converge on the eye in the
			// colour pass. Restores Shadows.CubeProgram.
			ProfileBegin("sprites")
			RenderSpriteCubeShadow(level, lsm, pl.Position, farPlane, cull)
			ProfileEnd()
			// Scatter into this cube face, with the light's own far plane as
			// the cast cap. Restores Shadows.CubeProgram.
			ProfileBegin("scatter")
			RenderScatterCubeShadow(level, lsm, pl.Position, farPlane, gfx.Cam.Position, BiomeWindTime, cull)
			ProfileEnd()
		}
		mapIdx++
	}

	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.BindFramebuffer(gl.FRAMEBUFFER, prevFBO)
	gl.Viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3])
	gl.UseProgram(gfx.Program)
}

// renderSceneShadow draws scene geometry into the currently bound FBO using the
// given model uniform location. cull, when non-nil, frustum-culls chunks and
// sectors against it — for the cascade pass that is the cascade's own light
// box, which is what keeps a large level from re-drawing every sector once per
// cascade.
// renderSceneShadow walks the level for one shadow pass. prog and skin identify
// which program is bound: both passes share this walker but resolve the skinning
// uniforms to different locations, so the cache cannot be a single global.
func renderSceneShadow(level *world.Level, prog uint32, skin *gfx.SkinUniforms, modelUniform int32, cull *Frustum) {
	renderSceneShadowLayers(level, prog, skin, modelUniform, cull, shadowAllLayers)
}

// shadowLayers picks which half of a scene walk to draw: the world's
// geometry, which only an edit, a streamed chunk or a moving chunk changes, or
// the things that move on their own — entities and scenery. A point light's
// cubemap keeps the first and redraws the second (point_shadow_cache.go);
// sprites and scatter are drawn by the passes themselves and count as moving.
//
// The world's half is itself two: a compiled level's baked static surfaces,
// and the chunks still drawn live.
type shadowLayers uint8

const (
	shadowCompiledLayer shadowLayers = 1 << iota
	shadowChunkLayer
	shadowMovingLayer
	shadowStaticLayer = shadowCompiledLayer | shadowChunkLayer
	shadowAllLayers   = shadowStaticLayer | shadowMovingLayer
)

func renderSceneShadowLayers(level *world.Level, prog uint32, skin *gfx.SkinUniforms, modelUniform int32, cull *Frustum, layers shadowLayers) {
	identity := mgl32.Ident4()
	// The model matrix used to be uploaded once, here, because every piece of
	// geometry was pre-baked into world space. Chunk geometry is chunk-local
	// now, so the upload moved inside the loop — but it is tracked rather than
	// re-sent per chunk, since a level of unmoved, unrotated, unscaled chunks
	// would otherwise pay for a uniform upload per chunk to say nothing changed.
	current := identity
	gl.UniformMatrix4fv(modelUniform, 1, false, &current[0])
	// Brush batches carry no skin, and nothing between here and the entity walk
	// sets it; drawGeometryShadow sets it per surface for everything else.
	gfx.ApplySkinUniforms(skin, prog, nil)

	// A compiled level's static surfaces, which are already in world space —
	// hence the identity that is still bound.
	//
	// Culled by the shadow frustum only, deliberately NOT by the camera's
	// visibility set: a wall the player cannot see still casts the shadow that
	// falls across the floor they can. The PVS answers "what can be seen from
	// here", and a shadow caster is precisely the thing that matters while
	// unseen.
	if layers&shadowCompiledLayer != 0 {
		ProfileBegin("compiled")
		shadowStaticSurfaces(level, prog, cull)
		ProfileEnd()
	}

	ProfileBegin("chunks")
	for ci := range level.Chunks {
		if layers&shadowChunkLayer == 0 {
			break
		}
		chunk := &level.Chunks[ci]
		// Terrain first, and ahead of the compiled skip below: the compiler
		// never bakes a terrain, so a compiled chunk's ground is live
		// geometry that casts from here like any other (terrain.go).
		if terrainDrawable(chunk) {
			shadowChunkTerrain(chunk, cull, modelUniform, &current)
		}
		// A compiled chunk cast above, out of the compiled list — except for
		// its dynamic brushes, which the compiler left out of the bake because
		// they move and which are therefore still live geometry here. Same
		// rule as the colour pass (internal/app).
		if chunk.Compiled && !chunk.HasDynamicBrushes() {
			continue
		}
		if !chunk.IsEnabled() {
			continue // switched off; see world/chunk_enable.go
		}
		if cull != nil {
			chunkMin, chunkMax := chunk.WorldBounds()
			if !AABBInFrustum(*cull, chunkMin, chunkMax) {
				continue
			}
		}
		// Out of the chunk's batches, one call per material for the unmoved
		// brushes that survive the cull. The drawn mesh casts the shadow, so a
		// wall's detail casts the shadow its bumps and reveals actually make.
		shadowChunkBrushes(level, chunk, chunk.ModelMatrix(), cull, prog, modelUniform, &current)
	}
	ProfileEnd()

	// Put the identity back before the entity walk, which sets its own matrices
	// and restores the identity when it is done — leaving a frame's transform
	// bound here would apply a ship's motion to every entity in the level.
	if current != identity {
		gl.UniformMatrix4fv(modelUniform, 1, false, &identity[0])
	}

	// Placed entities, after the world walk: they are the only thing here that
	// needs a model matrix of its own, and RenderEntitiesShadow puts the
	// identity back when it is done.
	if layers&shadowMovingLayer == 0 {
		return
	}
	ProfileBegin("entities")
	RenderEntitiesShadow(level, prog, skin, modelUniform, cull)
	ProfileEnd()
	// And the scenery, which needs a matrix of its own for the same reason and
	// restores the identity for the same one.
	ProfileBegin("scenery")
	RenderSceneryShadow(level, prog, skin, modelUniform, cull)
	ProfileEnd()
	// Cooked Nanite placements, culled cluster by cluster against the
	// light's frustum (nanite_scenery.go). Restores prog.
	ProfileBegin("nanite: scenery")
	renderNaniteSceneryShadow(level, prog, cull)
	ProfileEnd()
}

func drawGeometryShadow(g *gfx.Geometry, prog uint32, skin *gfx.SkinUniforms) {
	if len(g.Vertices) == 0 || g.Alpha < 0.05 {
		return
	}
	// The shadow passes deform from the same palette the colour pass uses, so an
	// animated entity's silhouette tracks its mesh.
	gfx.ApplySkinUniforms(skin, prog, g.BonePalette)
	// A coarser stand-in where the mesh has one (gfx.Geometry.ShadowLOD).
	c := g.ShadowCaster()
	gl.BindVertexArray(c.Vao)
	c.DrawTriangles()
}

// ---------- Uniform upload ----------

// CascadeTexUnit is the texture unit the directional cascade array is bound to.
// All directional shadows share it; point-light cubemaps start after it.
const CascadeTexUnit = 1

// shadowUniformNames are the per-program sampler uniforms' names,
// NUL-terminated and built once: the eight cubemap samplers are set on every
// lit program every frame.
var shadowUniformNames = func() (n struct {
	pointMap [world.MaxPointLights]string
}) {
	for i := range n.pointMap {
		n.pointMap[i] = fmt.Sprintf("pointShadowMap[%d]\x00", i)
	}
	return n
}()

// UploadShadowUniforms sends directional shadow data to the main shader.
func UploadShadowUniforms(lights world.LightSet, frame ShadowFrame) {
	UploadShadowUniformsTo(gfx.Program, lights, frame)
}

// UploadShadowUniformsTo hands the directional shadow state — cascade
// matrices, split distances, per-cascade biases, cast flags, strengths, the
// baked sun, and the clouds' shadow — to the scene block prog reads
// (gfx/scene_block.go), and binds prog's own samplers: the cascade array, the
// cloud map, GI's atlases and the virtual shadow map. Binds prog as active.
//
// The block is shared, so for the second and later program in a frame the
// first half finds nothing changed and costs a comparison; the samplers are
// the part that is still per program, because a sampler cannot live in a
// block.
func UploadShadowUniformsTo(prog uint32, lights world.LightSet, frame ShadowFrame) {
	gl.UseProgram(prog)
	s := gfx.SceneFor(prog)
	packDirShadows(&s.Block, lights, frame)
	// The clouds' shadow rides here because every program that can read it
	// already makes this call — see cloud_shadow.go. So does live GI, for the
	// same reason (gi.go).
	packCloudShadow(&s.Block)
	s.Flush()

	// The array texture is always bound, even with no casting lights: an
	// unassigned sampler2DArray defaults to unit 0, which holds the albedo
	// sampler2D — a type collision the driver is free to handle badly.
	gl.ActiveTexture(gl.TEXTURE0 + CascadeTexUnit)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, Shadows.CascadeArray)
	gl.Uniform1i(world.ProgUniformLoc(prog, "shadowMapArray\x00"), CascadeTexUnit)

	bindCloudShadowTo(prog)
	uploadGITo(prog)
	// And the sun's virtual shadow map, or word that there is none (vsm.go).
	uploadVSMTo(prog, frame)
	gl.ActiveTexture(gl.TEXTURE0)
}

// packDirShadows writes frame's cascades for lights into the scene block b.
// No GL.
func packDirShadows(b *gfx.SceneBlock, lights world.LightSet, frame ShadowFrame) {
	for c := 0; c < ShadowCascades; c++ {
		b.CascadeSplit[c] = [4]float32{frame.Splits[c]}
	}

	// Which light's baked share the cascades re-shadow; -1 for none.
	sun := int32(-1)
	if frame.BakedSunCast {
		sun = int32(frame.BakedSun)
	}
	b.BakedSunLight = sun

	for i := 0; i < world.MaxDirLights; i++ {
		layer := int32(-1)
		strength := float32(0)
		cast := int32(0)
		var cascades [ShadowCascades]DirCascade

		if i < len(frame.Lights) && i < len(lights.Directional) {
			dl := lights.Directional[i]
			strength = dl.ShadowStrength
			cascades = frame.Lights[i].Cascades
			if dl.CastsShadows() && frame.Lights[i].BaseLayer >= 0 {
				layer = frame.Lights[i].BaseLayer
				cast = 1
			}
		}
		b.DirLightCastShadows[i] = [4]int32{cast}
		b.DirLightShadowStrength[i] = [4]float32{strength}
		b.DirLightShadowLayer[i] = [4]int32{layer}

		for c := 0; c < ShadowCascades; c++ {
			idx := i*ShadowCascades + c
			m := cascades[c]
			if m.Matrix == (mgl32.Mat4{}) {
				m.Matrix = mgl32.Ident4()
			}
			b.CascadeMatrix[idx] = m.Matrix
			b.CascadeBias[idx] = [4]float32{m.Bias}
			b.CascadeTexel[idx] = [4]float32{m.Texel}
		}
	}
}

// UploadPointShadowUniforms sends point light shadow cubemaps to the main shader.
// Texture units start after directional shadow maps (1..MaxDirLights are dir, then point).
func UploadPointShadowUniforms(lights world.LightSet) {
	UploadPointShadowUniformsTo(gfx.Program, lights)
}

// UploadPointShadowUniformsTo hands the point lights' shadow switches,
// strengths and far planes to the scene block prog reads, and binds prog's
// eight cubemap samplers.
func UploadPointShadowUniformsTo(prog uint32, lights world.LightSet) {
	gl.UseProgram(prog)
	s := gfx.SceneFor(prog)
	packPointShadows(&s.Block, lights)
	s.Flush()

	uniformLoc := func(name string) int32 { return world.ProgUniformLoc(prog, name) }
	baseUnit := int32(CascadeTexUnit + 1) // unit 0 = albedo, unit 1 = dir cascade array
	n := &shadowUniformNames

	// Bind the dummy cubemap to ALL pointShadowMap slots (not just active
	// lights) so that every samplerCube uniform references a unit with a valid
	// cubemap. Unassigned samplerCube uniforms default to unit 0 which holds
	// the diffuse sampler2D — a type mismatch that causes undefined behavior.
	for i := 0; i < world.MaxPointLights; i++ {
		texUnit := baseUnit + int32(i)
		gl.ActiveTexture(uint32(gl.TEXTURE0 + texUnit))
		gl.BindTexture(gl.TEXTURE_CUBE_MAP, Shadows.DummyCubemap)
		gl.Uniform1i(uniformLoc(n.pointMap[i]), texUnit)
	}

	numPt := min(len(lights.Points), world.MaxPointLights)
	mapIdx := 0
	for i := 0; i < numPt; i++ {
		if lights.Points[i].CastsShadows() && mapIdx < len(Shadows.PointMaps) {
			texUnit := baseUnit + int32(i)
			gl.ActiveTexture(uint32(gl.TEXTURE0 + texUnit))
			gl.BindTexture(gl.TEXTURE_CUBE_MAP, Shadows.PointMaps[mapIdx].Cubemap)
			mapIdx++
		}
	}
	gl.ActiveTexture(gl.TEXTURE0)
}

// packPointShadows writes the point lights' shadow parameters into the scene
// block b. No GL. Slots past the light count are zeroed, as PackLights does.
func packPointShadows(b *gfx.SceneBlock, lights world.LightSet) {
	// The taps' turn (point_shadow.glsl): a new one each TAA frame, so the
	// history resolves eight taps into many; held still otherwise.
	b.ShadowNoiseFrame = coverageDither()
	numPt := min(len(lights.Points), world.MaxPointLights)
	for i := 0; i < world.MaxPointLights; i++ {
		if i >= numPt {
			b.PointLightCastShadows[i], b.PointLightShadowStrength[i], b.PointLightFarPlane[i] = [4]int32{}, [4]float32{}, [4]float32{}
			continue
		}
		pl := &lights.Points[i]
		cast := int32(0)
		if pl.CastsShadows() {
			cast = 1
		}
		b.PointLightCastShadows[i] = [4]int32{cast}
		b.PointLightShadowStrength[i] = [4]float32{pl.ShadowStrength}
		b.PointLightFarPlane[i] = [4]float32{pl.Radius}
	}
}
