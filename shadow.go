package main

import (
	"fmt"
	"log"
	"math"
	"os"
	"strings"

	"github.com/go-gl/gl/v4.1-core/gl"
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
// cascades hold valid depth there. fragment.glsl mirrors this as
// SHADOW_CASCADE_BLEND; change both together.
const ShadowCascadeBlend = 0.12

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
	// units GL 4.1 guarantees. The array costs one unit for all of them.
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
	return linkProgram(vertPath, fragPath, &Shadows.Program)
}

func loadCubeShadowProgram(vertPath, fragPath string) error {
	return linkProgram(vertPath, fragPath, &Shadows.CubeProgram)
}

func linkProgram(vertPath, fragPath string, out *uint32) error {
	vertSrc, err := readShaderFile(vertPath)
	if err != nil {
		return err
	}
	fragSrc, err := readShaderFile(fragPath)
	if err != nil {
		return err
	}
	vs, err := CompileShader(vertSrc, gl.VERTEX_SHADER)
	if err != nil {
		return err
	}
	fs, err := CompileShader(fragSrc, gl.FRAGMENT_SHADER)
	if err != nil {
		return err
	}
	*out = gl.CreateProgram()
	gl.AttachShader(*out, vs)
	gl.AttachShader(*out, fs)
	gl.LinkProgram(*out)

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

func readShaderFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(strings.TrimRight(string(data), "\n"), "\r\n", "\n"), nil
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

	gl.GenTextures(1, &Shadows.CascadeArray)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, Shadows.CascadeArray)
	gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.DEPTH_COMPONENT24,
		ShadowMapSize, ShadowMapSize, layers, 0,
		gl.DEPTH_COMPONENT, gl.FLOAT, nil)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_BORDER)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_BORDER)
	borderColor := [4]float32{1, 1, 1, 1}
	gl.TexParameterfv(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_BORDER_COLOR, &borderColor[0])
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)

	if Shadows.CascadeFBO == 0 {
		gl.GenFramebuffers(1, &Shadows.CascadeFBO)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, Shadows.CascadeFBO)
	gl.FramebufferTextureLayer(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, Shadows.CascadeArray, 0, 0)
	gl.DrawBuffer(gl.NONE)
	gl.ReadBuffer(gl.NONE)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("WARNING: cascade shadow FBO incomplete (status=0x%X)", st)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)

	log.Printf("cascade shadow array allocated (tex=%d, fbo=%d, %dx%d x %d layers)",
		Shadows.CascadeArray, Shadows.CascadeFBO, ShadowMapSize, ShadowMapSize, layers)
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
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, 0)
	return tex
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
		Shadows.PointMaps = Shadows.PointMaps[:len(Shadows.PointMaps)-1]
	}
}

func createPointShadowMap() PointShadowMap {
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
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	return pm
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
	if abs32(lightDir.Y()) > 0.99 {
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
func RenderShadowPass(lights LightSet, level *Level, view, projection mgl32.Mat4) ShadowFrame {
	tanHalfV, aspect, near := perspectiveParams(projection)

	numDir := len(lights.Directional)
	if numDir > MaxDirLights {
		numDir = MaxDirLights
	}
	frame := newShadowFrame(len(lights.Directional), near)

	shadowCount := 0
	for i := 0; i < numDir; i++ {
		if lights.Directional[i].CastShadows {
			shadowCount++
		}
	}
	EnsureCascadeArray(int32(shadowCount * ShadowCascades))
	if shadowCount == 0 || Shadows.CascadeArray == 0 {
		return frame
	}

	gl.UseProgram(Shadows.Program)
	var prevViewport [4]int32
	gl.GetIntegerv(gl.VIEWPORT, &prevViewport[0])
	gl.Viewport(0, 0, ShadowMapSize, ShadowMapSize)
	gl.Disable(gl.CULL_FACE)
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(1.1, 4.0)
	gl.BindFramebuffer(gl.FRAMEBUFFER, Shadows.CascadeFBO)

	layer := int32(0)
	for i := 0; i < numDir; i++ {
		dl := lights.Directional[i]
		if !dl.CastShadows {
			continue
		}
		frame.Lights[i].BaseLayer = layer

		for c := 0; c < ShadowCascades; c++ {
			sliceNear := cascadeSliceNear(c, near, frame.Splits)
			corners := frustumSliceCorners(view, tanHalfV, aspect, sliceNear, frame.Splits[c])
			lsm, depth := CascadeLightMatrix(dl.Direction, corners)
			frame.Lights[i].Cascades[c] = DirCascade{Matrix: lsm, Bias: ShadowWorldBias / depth}

			gl.FramebufferTextureLayer(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT,
				Shadows.CascadeArray, 0, layer)
			gl.Clear(gl.DEPTH_BUFFER_BIT)
			gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])

			// Cull to the cascade's own box rather than the camera frustum: a
			// caster behind the player can still drop a shadow into view, and
			// the box already extends toward the light to include it.
			cull := ExtractFrustum(lsm)
			renderSceneShadow(level, Shadows.ModelUniform, &cull)
			// Cast displaced/scattered detail (drawn via its own tess/instanced
			// programs so bumps and rocks cast accurate shadows); restores Shadows.Program.
			RenderDetailDirShadow(level, lsm, &cull)
			// Cast painted biome scatter, re-instanced through its own placement
			// code so a blade's shadow lands on the blade; gated per kind and
			// limited to the nearest cascades by BiomeShadows. Restores
			// Shadows.Program.
			RenderBiomeScatterDirShadow(level, lsm, c, frame.Splits[c], Cam.Position, BiomeWindTime, &cull)
			layer++
		}
	}

	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	gl.Viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3])
	gl.UseProgram(Program)

	return frame
}

// RenderPointShadowPass renders cubemap shadow maps for point lights.
func RenderPointShadowPass(lights LightSet, level *Level) {
	shadowCount := 0
	for _, pl := range lights.Points {
		if pl.CastShadows {
			shadowCount++
		}
	}
	EnsurePointShadowMaps(shadowCount)

	if shadowCount == 0 {
		return
	}

	gl.UseProgram(Shadows.CubeProgram)
	var prevViewport [4]int32
	gl.GetIntegerv(gl.VIEWPORT, &prevViewport[0])
	gl.Viewport(0, 0, PointShadowMapSize, PointShadowMapSize)
	gl.Disable(gl.CULL_FACE)
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(1.1, 4.0)

	mapIdx := 0
	for _, pl := range lights.Points {
		if !pl.CastShadows {
			continue
		}
		if mapIdx >= len(Shadows.PointMaps) {
			break
		}

		pm := Shadows.PointMaps[mapIdx]
		farPlane := pl.Radius
		proj := mgl32.Perspective(mgl32.DegToRad(90.0), 1.0, 0.1, farPlane)
		views := cubeFaceViews(pl.Position)

		gl.Uniform3f(Shadows.CubeLightPos, pl.Position[0], pl.Position[1], pl.Position[2])
		gl.Uniform1f(Shadows.CubeFarPlane, farPlane)

		gl.BindFramebuffer(gl.FRAMEBUFFER, pm.FBO)
		for face := 0; face < 6; face++ {
			gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT,
				uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+face), pm.Cubemap, 0)
			gl.Clear(gl.DEPTH_BUFFER_BIT)

			lsm := proj.Mul4(views[face])
			gl.UniformMatrix4fv(Shadows.CubeLSMUniform, 1, false, &lsm[0])

			cull := ExtractFrustum(lsm)
			renderSceneShadow(level, Shadows.CubeModelUniform, &cull)
			// Cast displaced/scattered detail into this cube face; restores Shadows.CubeProgram.
			RenderDetailCubeShadow(level, lsm, pl.Position, farPlane, &cull)
			// Cast painted biome scatter into this cube face; restores Shadows.CubeProgram.
			RenderBiomeScatterCubeShadow(level, lsm, pl.Position, farPlane, Cam.Position, BiomeWindTime, &cull)
		}
		mapIdx++
	}

	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	gl.Viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3])
	gl.UseProgram(Program)
}

// renderSceneShadow draws scene geometry into the currently bound FBO using the
// given model uniform location. cull, when non-nil, frustum-culls chunks,
// sectors, entities and primitives against it — for the cascade pass that is the
// cascade's own light box, which is what keeps a large level from re-drawing
// every sector once per cascade.
func renderSceneShadow(level *Level, modelUniform int32, cull *Frustum) {
	identity := mgl32.Ident4()
	gl.UniformMatrix4fv(modelUniform, 1, false, &identity[0])

	for ci := range level.Chunks {
		chunk := &level.Chunks[ci]
		if cull != nil && !AABBInFrustum(*cull, chunk.MinBound, chunk.MaxBound) {
			continue
		}
		for si := range chunk.Sectors {
			sector := &chunk.Sectors[si]
			if cull != nil {
				secMin, secMax := sector.WorldAABB()
				if !AABBInFrustum(*cull, secMin, secMax) {
					continue
				}
			}
			if sector.Floor.Enabled && !sector.Floor.Hidden {
				// Displaced faces cast via the detail tess program instead (see
				// RenderDetailDirShadow / RenderDetailCubeShadow), so skip the flat mesh.
				if !sector.Floor.Detail.HasDisplace() {
					drawGeometryShadow(&sector.Floor.Geometry)
				}
				for eg := range sector.Floor.ExtrusionGeometries {
					drawGeometryShadow(&sector.Floor.ExtrusionGeometries[eg])
				}
			}
			if sector.Ceiling.Enabled {
				if !sector.Ceiling.Detail.HasDisplace() {
					drawGeometryShadow(&sector.Ceiling.Geometry)
				}
				for eg := range sector.Ceiling.ExtrusionGeometries {
					drawGeometryShadow(&sector.Ceiling.ExtrusionGeometries[eg])
				}
			}
			for wi := range sector.Walls {
				w := &sector.Walls[wi]
				if w.Visible {
					if !w.Detail.HasDisplace() {
						drawGeometryShadow(&w.Geometry)
					}
					for eg := range w.ExtrusionGeometries {
						drawGeometryShadow(&w.ExtrusionGeometries[eg])
					}
				}
			}
			for pi := range sector.Portals {
				p := &sector.Portals[pi]
				if p.Visible && p.DrawGap {
					drawGeometryShadow(&p.FloorGap)
					drawGeometryShadow(&p.CeilGap)
				}
			}
		}
	}

	// Entities (dynamic objects)
	for ei := range level.Entities {
		e := &level.Entities[ei]
		if !e.Visible {
			continue
		}
		if cull != nil {
			if eMin, eMax, ok := e.WorldAABB(); ok && !AABBInFrustum(*cull, eMin, eMax) {
				continue
			}
		}
		model := e.ModelMatrix()
		gl.UniformMatrix4fv(modelUniform, 1, false, &model[0])
		drawGeometryShadow(&e.Geometry)
	}

	// Primitives (static procedural objects)
	for ci := range level.Chunks {
		chunk := &level.Chunks[ci]
		if cull != nil && !AABBInFrustum(*cull, chunk.MinBound, chunk.MaxBound) {
			continue
		}
		for pi := range chunk.Primitives {
			p := &chunk.Primitives[pi]
			if cull != nil {
				pMin, pMax := p.WorldAABB()
				if !AABBInFrustum(*cull, pMin, pMax) {
					continue
				}
			}
			model := p.ModelMatrix()
			gl.UniformMatrix4fv(modelUniform, 1, false, &model[0])
			drawGeometryShadow(&p.Geometry)
		}
	}
}

func drawGeometryShadow(g *Geometry) {
	if len(g.Vertices) == 0 || g.Alpha < 0.05 {
		return
	}
	gl.BindVertexArray(g.Vao)
	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}

// ---------- Uniform upload ----------

// CascadeTexUnit is the texture unit the directional cascade array is bound to.
// All directional shadows share it; point-light cubemaps start after it.
const CascadeTexUnit = 1

// UploadShadowUniforms sends directional shadow data to the main shader.
func UploadShadowUniforms(lights LightSet, frame ShadowFrame) {
	UploadShadowUniformsTo(Program, lights, frame)
}

// UploadShadowUniformsTo sends directional shadow data (cascade matrices, split
// distances, per-cascade biases, cast flags, strengths and the cascade array
// sampler) to prog. Binds prog as active.
func UploadShadowUniformsTo(prog uint32, lights LightSet, frame ShadowFrame) {
	gl.UseProgram(prog)
	uniformLoc := func(name string) int32 { return progUniformLoc(prog, name) }

	for c := 0; c < ShadowCascades; c++ {
		splitName := fmt.Sprintf("cascadeSplit[%d]\x00", c)
		gl.Uniform1f(uniformLoc(splitName), frame.Splits[c])
	}

	// The array texture is always bound, even with no casting lights: an
	// unassigned sampler2DArray defaults to unit 0, which holds the albedo
	// sampler2D — a type collision the driver is free to handle badly.
	gl.ActiveTexture(gl.TEXTURE0 + CascadeTexUnit)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, Shadows.CascadeArray)
	gl.Uniform1i(uniformLoc("shadowMapArray\x00"), CascadeTexUnit)

	for i := 0; i < MaxDirLights; i++ {
		layer := int32(-1)
		strength := float32(0)
		cast := int32(0)
		var cascades [ShadowCascades]DirCascade

		if i < len(frame.Lights) && i < len(lights.Directional) {
			dl := lights.Directional[i]
			strength = dl.ShadowStrength
			cascades = frame.Lights[i].Cascades
			if dl.CastShadows && frame.Lights[i].BaseLayer >= 0 {
				layer = frame.Lights[i].BaseLayer
				cast = 1
			}
		}

		castName := fmt.Sprintf("dirLightCastShadows[%d]\x00", i)
		gl.Uniform1i(uniformLoc(castName), cast)
		strengthName := fmt.Sprintf("dirLightShadowStrength[%d]\x00", i)
		gl.Uniform1f(uniformLoc(strengthName), strength)
		layerName := fmt.Sprintf("dirLightShadowLayer[%d]\x00", i)
		gl.Uniform1i(uniformLoc(layerName), layer)

		for c := 0; c < ShadowCascades; c++ {
			idx := i*ShadowCascades + c
			m := cascades[c]
			if m.Matrix == (mgl32.Mat4{}) {
				m.Matrix = mgl32.Ident4()
			}
			matName := fmt.Sprintf("cascadeMatrix[%d]\x00", idx)
			gl.UniformMatrix4fv(uniformLoc(matName), 1, false, &m.Matrix[0])
			biasName := fmt.Sprintf("cascadeBias[%d]\x00", idx)
			gl.Uniform1f(uniformLoc(biasName), m.Bias)
		}
	}
	gl.ActiveTexture(gl.TEXTURE0)
}

// UploadPointShadowUniforms sends point light shadow cubemaps to the main shader.
// Texture units start after directional shadow maps (1..MaxDirLights are dir, then point).
func UploadPointShadowUniforms(lights LightSet) {
	UploadPointShadowUniformsTo(Program, lights)
}

// UploadPointShadowUniformsTo sends point-light cubemap shadow data to prog.
func UploadPointShadowUniformsTo(prog uint32, lights LightSet) {
	gl.UseProgram(prog)
	uniformLoc := func(name string) int32 { return progUniformLoc(prog, name) }

	baseUnit := int32(CascadeTexUnit + 1) // unit 0 = albedo, unit 1 = dir cascade array

	// Bind the dummy cubemap to ALL pointShadowMap slots (not just active
	// lights) so that every samplerCube uniform references a unit with a valid
	// cubemap. Unassigned samplerCube uniforms default to unit 0 which holds
	// the diffuse sampler2D — a type mismatch that causes undefined behavior.
	for i := 0; i < MaxPointLights; i++ {
		texUnit := baseUnit + int32(i)
		gl.ActiveTexture(uint32(gl.TEXTURE0 + texUnit))
		gl.BindTexture(gl.TEXTURE_CUBE_MAP, Shadows.DummyCubemap)
		smName := fmt.Sprintf("pointShadowMap[%d]\x00", i)
		gl.Uniform1i(uniformLoc(smName), texUnit)
	}

	numPt := len(lights.Points)
	if numPt > MaxPointLights {
		numPt = MaxPointLights
	}
	mapIdx := 0
	for i := 0; i < numPt; i++ {
		pl := lights.Points[i]
		castName := fmt.Sprintf("pointLightCastShadows[%d]\x00", i)
		castVal := int32(0)
		if pl.CastShadows {
			castVal = 1
		}
		gl.Uniform1i(uniformLoc(castName), castVal)

		strengthName := fmt.Sprintf("pointLightShadowStrength[%d]\x00", i)
		gl.Uniform1f(uniformLoc(strengthName), pl.ShadowStrength)

		farName := fmt.Sprintf("pointLightFarPlane[%d]\x00", i)
		gl.Uniform1f(uniformLoc(farName), pl.Radius)

		if pl.CastShadows && mapIdx < len(Shadows.PointMaps) {
			texUnit := baseUnit + int32(i)
			gl.ActiveTexture(uint32(gl.TEXTURE0 + texUnit))
			gl.BindTexture(gl.TEXTURE_CUBE_MAP, Shadows.PointMaps[mapIdx].Cubemap)
			mapIdx++
		}
	}
	gl.ActiveTexture(gl.TEXTURE0)
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
