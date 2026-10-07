package gfx

// scene_block.go — the scene's lights, shadows and fog as one std140 uniform
// buffer (resources/shaders/scene_block.glsl).
//
// Every program that shades — the world program, sprites, the biome passes,
// terrain, foliage, Nanite's material pass, the volumetric march, the GI trace
// — reads the same lights, the same shadow cascades and the same fog. They used
// to be loose uniforms written into each program separately, every frame: a
// few hundred glUniform calls per program, five or more programs, and a rule
// that every program had to remember to call each Upload*To. Now the writers
// (world.UploadLightsTo, world.UploadFogTo, render.UploadShadowUniformsTo and
// render.UploadPointShadowUniformsTo) fill SceneBlock on the CPU and Flush
// sends it — once, and only when something in it changed — to a buffer every
// program reads through binding point SceneBlockBinding.
//
// The writers still take a program, for two reasons. They still bind it
// (callers draw through it straight after, as they always have), and a program
// can be pointed at a block other than the scene's: the material editor's
// preview copy of the world program lights its sphere with a lookdev rig, not
// the level, so it reads PreviewScene (UsePreviewScene); and the volumetric
// preview, which shares the march's program with the level, swaps BlankScene
// in for the length of its draw (PushScene/PopScene).
//
// Uniform-buffer bindings are per GL context, unlike the buffers themselves.
// The main context binds on its first Flush and on every DrawWorld; a
// secondary window binds both buffers when its context is made current
// (BindSceneBlocks), so a program drawing there reads what it read when these
// were program uniforms: the level's state.

import (
	"unsafe"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Binding points. scene_block.glsl declares binding 0; a program moved to the
// preview block is re-pointed at 1 with glUniformBlockBinding.
const (
	SceneBlockBinding        = 0
	PreviewSceneBlockBinding = 1
)

// The array sizes scene_block.glsl is declared with. world.MaxDirLights,
// world.MaxPointLights, world.MaxFogVolumes and render.ShadowCascades must
// equal these (gfx cannot import either package; their tests assert it).
const (
	SceneMaxDirLights   = 4
	SceneMaxPointLights = 8
	SceneShadowCascades = 3
	SceneMaxFogVolumes  = 8
)

// SceneBlock is scene_block.glsl's SceneBlock in std140 layout, member for
// member. Every field is 4-byte, so Go inserts no padding of its own and the
// offsets below are the sums of the sizes before them; the std140 padding is
// written out — a vec3 or a scalar array element is a [4] whose tail is unused
// (vec3 elements leave [3] for the next scalar only outside arrays), and a
// mat3 is three vec4 columns. TestSceneBlockMatchesTheShader checks each
// offset against the linked program's.
type SceneBlock struct {
	// ---- lights ----
	GlobalLightColor      [3]float32 // 0
	GlobalLightBrightness float32    // 12
	LightWrap             float32    // 16
	AmbientHemisphere     float32    // 20
	NumDirLights          int32      // 24
	NumPointLights        int32      // 28

	DirLightDir        [SceneMaxDirLights][4]float32 // 32
	DirLightColor      [SceneMaxDirLights][4]float32 // 96
	DirLightBrightness [SceneMaxDirLights][4]float32 // 160
	DirLightDynamic    [SceneMaxDirLights][4]int32   // 224

	PointLightPos        [SceneMaxPointLights][4]float32 // 288
	PointLightColor      [SceneMaxPointLights][4]float32 // 416
	PointLightBrightness [SceneMaxPointLights][4]float32 // 544
	PointLightRadius     [SceneMaxPointLights][4]float32 // 672
	PointLightDynamic    [SceneMaxPointLights][4]int32   // 800

	// ---- directional shadows ----
	CascadeMatrix          [SceneMaxDirLights * SceneShadowCascades]mgl32.Mat4 // 928
	CascadeBias            [SceneMaxDirLights * SceneShadowCascades][4]float32 // 1696
	CascadeTexel           [SceneMaxDirLights * SceneShadowCascades][4]float32 // 1888
	CascadeSplit           [SceneShadowCascades][4]float32                     // 2080
	DirLightShadowLayer    [SceneMaxDirLights][4]int32                         // 2128
	DirLightCastShadows    [SceneMaxDirLights][4]int32                         // 2192 (GLSL bool)
	DirLightShadowStrength [SceneMaxDirLights][4]float32                       // 2256
	BakedSunLight          int32                                               // 2320
	CloudShadowOn          int32                                               // 2324
	CloudShadowLight       int32                                               // 2328
	FogMode                int32                                               // 2332

	// ---- point shadows ----
	PointLightCastShadows    [SceneMaxPointLights][4]int32   // 2336 (GLSL bool)
	PointLightShadowStrength [SceneMaxPointLights][4]float32 // 2464
	PointLightFarPlane       [SceneMaxPointLights][4]float32 // 2592

	CloudShadowMatrix mgl32.Mat4 // 2720

	// ---- fog ----
	FogColor         [3]float32                        // 2784
	FogStart         float32                           // 2796
	FogCamPos        [3]float32                        // 2800
	FogEnd           float32                           // 2812
	FogDensity       float32                           // 2816
	FogMaxOpacity    float32                           // 2820
	NumFogVolumes    int32                             // 2824
	ShadowNoiseFrame int32                             // 2828 (the slot before the mat3 array's 16-byte alignment)
	FogVolBasis      [SceneMaxFogVolumes][3][4]float32 // 2832
	FogVolCenter     [SceneMaxFogVolumes][4]float32    // 3216
	FogVolShape      [SceneMaxFogVolumes][4]int32      // 3344
	FogVolColor      [SceneMaxFogVolumes][4]float32    // 3472
	FogVolDensity    [SceneMaxFogVolumes][4]float32    // 3600
	FogVolMaxOpacity [SceneMaxFogVolumes][4]float32    // 3728
}

// SceneBlockSize is the block's size in bytes: 3856.
const SceneBlockSize = int(unsafe.Sizeof(SceneBlock{}))

// SceneBuffer is one SceneBlock on the CPU and the GL buffer it is sent to.
type SceneBuffer struct {
	// Block is what the writers fill. Flush sends it.
	Block SceneBlock

	sent    SceneBlock // what the buffer holds, valid once synced is set
	synced  bool
	buf     uint32
	binding uint32
}

// The scene's block, the material preview's, and the empty one the
// volumetric preview draws with. See the file comment.
var (
	Scene        = &SceneBuffer{binding: SceneBlockBinding}
	PreviewScene = &SceneBuffer{binding: PreviewSceneBlockBinding}
	BlankScene   = &SceneBuffer{binding: SceneBlockBinding}
)

// ensure makes the GL buffer on first use. A GL context must be current.
func (s *SceneBuffer) ensure() {
	if s.buf != 0 {
		return
	}
	gl.GenBuffers(1, &s.buf)
	gl.BindBuffer(gl.UNIFORM_BUFFER, s.buf)
	gl.BufferData(gl.UNIFORM_BUFFER, SceneBlockSize, nil, gl.DYNAMIC_DRAW)
	gl.BindBuffer(gl.UNIFORM_BUFFER, 0)
	s.synced = false
}

// Flush sends Block to the buffer if it differs from what the buffer holds,
// and binds the buffer to its binding point when it does. A frame in which
// nothing changed — or the fourth writer of the same lights this frame — costs
// a comparison and no GL at all.
func (s *SceneBuffer) Flush() {
	if glBlocked("SceneBuffer.Flush") {
		return
	}
	s.ensure()
	if s.synced && s.sent == s.Block {
		return
	}
	gl.BindBuffer(gl.UNIFORM_BUFFER, s.buf)
	gl.BufferSubData(gl.UNIFORM_BUFFER, 0, SceneBlockSize, unsafe.Pointer(&s.Block))
	gl.BindBuffer(gl.UNIFORM_BUFFER, 0)
	s.sent, s.synced = s.Block, true
	s.Bind()
}

// Bind binds the buffer to its binding point in the current context.
func (s *SceneBuffer) Bind() { s.BindAt(s.binding) }

// BindAt binds the buffer to an arbitrary binding point — PushScene's way of
// standing BlankScene in for the scene at SceneBlockBinding.
func (s *SceneBuffer) BindAt(binding uint32) {
	if glBlocked("SceneBuffer.Bind") {
		return
	}
	s.ensure()
	gl.BindBufferBase(gl.UNIFORM_BUFFER, binding, s.buf)
}

// BindSceneBlocks binds the scene's and the preview's buffers in the current
// context. For a secondary window's context, once it is current: bindings do
// not cross contexts the way the buffers do.
func BindSceneBlocks() {
	Scene.Bind()
	PreviewScene.Bind()
}

// previewScenePrograms are the programs re-pointed at PreviewScene.
var previewScenePrograms = map[uint32]bool{}

// UsePreviewScene points prog's SceneBlock at PreviewScene's binding, so what
// the writers are handed for prog lands there rather than in the scene's.
// Idempotent; a relink (ForgetUniformLocations) undoes it, as a fresh link
// takes the shader's own binding.
func UsePreviewScene(prog uint32) {
	if prog == 0 || previewScenePrograms[prog] {
		return
	}
	idx := gl.GetUniformBlockIndex(prog, gl.Str("SceneBlock\x00"))
	if idx != gl.INVALID_INDEX {
		gl.UniformBlockBinding(prog, idx, PreviewSceneBlockBinding)
	}
	previewScenePrograms[prog] = true
}

// sceneOverride is PushScene's stand-in, or nil.
var sceneOverride *SceneBuffer

// PushScene makes every writer target b, whatever program it is handed, and
// binds b where the scene's block is read, until PopScene. For a pass that
// draws through the level's own programs with state that is not the level's.
func PushScene(b *SceneBuffer) {
	sceneOverride = b
	b.BindAt(SceneBlockBinding)
}

// PopScene ends PushScene and binds the scene's buffer back.
func PopScene() {
	sceneOverride = nil
	Scene.Bind()
}

// SceneFor is the block the writers fill for prog.
func SceneFor(prog uint32) *SceneBuffer {
	if sceneOverride != nil {
		return sceneOverride
	}
	if previewScenePrograms[prog] {
		return PreviewScene
	}
	return Scene
}
