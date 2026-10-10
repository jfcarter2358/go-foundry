package gfx

// model_skin_gpu.go — the runtime's GPU skinning plumbing.
//
// The editor skins on the CPU (editor_model_pose.go); this is the other half of
// that split. Entities in a level can be many and are drawn three times a frame
// — colour, cascade shadows, point shadows — so their deformation belongs on the
// hardware, and putting it there also means the shadow passes deform from the
// same palette the colour pass does rather than needing their own CPU sweep.
//
// What lives here: loading the shared shader source, and the per-draw uniform
// upload. The vertex attributes themselves ride on Geometry (geometry.go), as a
// pair of parallel buffers beside Normals rather than a wider interleaved
// record — the 5-float stride is assumed by Draw, DrawWithTransform and
// drawGeometryShadow alike, and widening it would have to be right in all three.

import (
	"fmt"
	"foundry/internal/fileutil"
	"sync"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// SkinCommonSource returns resources/shaders/skin_common.glsl, cached after the
// first successful read.
//
// A failure is deliberately *not* cached. Caching one would mean a single early
// call made before ResourcesPath is set — which is exactly what a test binary
// does — permanently disables skinning for the rest of the process, and the
// symptom would be three shaders failing to compile on a function they can no
// longer see rather than the missing file that actually caused it.
var (
	skinShaderMu  sync.Mutex
	skinShaderSrc string
)

func SkinCommonSource() (string, error) {
	skinShaderMu.Lock()
	defer skinShaderMu.Unlock()
	if skinShaderSrc != "" {
		return skinShaderSrc, nil
	}
	path := fileutil.ShaderPath("skin_common.glsl")
	src, err := ReadShaderFile(path)
	if err != nil {
		return "", fmt.Errorf("read skinning shader: %w", err)
	}
	skinShaderSrc = src
	return src, nil
}

// InjectSkinning splices the shared skinning source into a vertex shader.
//
// The error is returned rather than swallowed: every program this is called for
// *uses* skinPosition, so continuing without it produces a compile failure
// naming a function instead of the file that could not be read.
func InjectSkinning(src string) (string, error) {
	shared, err := SkinCommonSource()
	if err != nil {
		return "", err
	}
	return InjectAfterVersion(src, shared), nil
}

// SkinUniforms caches one program's skinning uniform locations. Resolved lazily
// because the three programs are compiled at different points in startup.
//
// It also remembers what it last put in them this frame, so a draw that would
// write the same thing again — the next submesh of the same character, which
// shares its palette; the next unskinned wall — writes nothing. An entity is a
// handful of submeshes deformed by one palette, drawn in the colour pass and
// in every shadow map it falls in, and each of those draws used to send the
// whole palette (up to MaxSkinBones matrices) again.
type SkinUniforms struct {
	// prog and linkGen are what skinned and bones were looked up against. A
	// SkinUniforms is a package or pass variable that outlives its program —
	// a pass relinks its program with new defines (a graphics setting, the
	// half-res GI switch) and gets a different id or the same id with a
	// different layout — so the locations are looked up again whenever either
	// has moved, rather than once for the life of the process. A stale one
	// writes the skinning flag into whatever uniform now sits at its location.
	prog    uint32
	linkGen uint64
	skinned int32
	bones   int32

	// What the program holds, valid while epoch is skinEpoch: n == 0 is
	// "unskinned", otherwise the first n matrices of last.
	epoch uint64
	n     int
	last  [MaxSkinBones]mgl32.Mat4
}

// skinEpoch bounds how long a SkinUniforms' memory of its program is trusted:
// one frame (BeginSkinFrame). Within a frame nothing relinks a program, which
// is the one thing that could change its uniforms behind this file's back.
var skinEpoch uint64 = 1

// BeginSkinFrame starts a frame for ApplySkinUniforms' memory: the first draw
// through each program after it writes its skinning uniforms whatever they
// held. Called at the top of each frame's draw (render.DrawWorld).
func BeginSkinFrame() { skinEpoch++ }

func (u *SkinUniforms) resolve(prog uint32) {
	if u.prog == prog && u.linkGen == linkGeneration && prog != 0 {
		return
	}
	u.prog, u.linkGen = prog, linkGeneration
	u.epoch = 0 // what the old locations held says nothing about the new ones
	u.skinned = gl.GetUniformLocation(prog, gl.Str("skinned\x00"))
	u.bones = gl.GetUniformLocation(prog, gl.Str("bones\x00"))
}

// ApplySkinUniforms sets the skinning state for one draw call.
//
// It is called on *every* draw, including the unskinned case, for the same
// reason applyWaterUniforms is: the uniform is program state, so a wall drawn
// after an animated entity would otherwise inherit that entity's palette and
// bend around its bones. What it skips is only a write of exactly what the
// program already holds, compared by value — a palette is rewritten in place
// every tick, so which slice it is says nothing about what it holds.
func ApplySkinUniforms(u *SkinUniforms, prog uint32, palette []mgl32.Mat4) {
	u.resolve(prog)
	if u.skinned < 0 {
		return // program has no skinning path (the gizmo/editor shaders)
	}
	n := min(len(palette), MaxSkinBones)
	known := u.epoch == skinEpoch
	if n == 0 {
		if known && u.n == 0 {
			return
		}
		gl.Uniform1i(u.skinned, 0)
		u.epoch, u.n = skinEpoch, 0
		return
	}
	if known && u.n == n && samePalette(u.last[:n], palette[:n]) {
		return
	}
	if !known || u.n == 0 {
		gl.Uniform1i(u.skinned, 1)
	}
	if u.bones >= 0 {
		gl.UniformMatrix4fv(u.bones, int32(n), false, &palette[0][0])
	}
	copy(u.last[:n], palette[:n])
	u.epoch, u.n = skinEpoch, n
}

// samePalette reports whether a and b hold the same matrices.
func samePalette(a, b []mgl32.Mat4) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Per-program caches. Separate values rather than one shared struct because the
// same uniform name lands at a different location in each program.
var (
	worldSkinUniforms      SkinUniforms
	ShadowSkinUniforms     SkinUniforms
	ShadowCubeSkinUniforms SkinUniforms
)

// MaxSkinBones caps an armature's size, because the runtime uploads the bone
// palette as a plain mat4 array uniform and GL 4.3 guarantees only 1024 vertex
// uniform components. 48 mat4 is 768 of them, leaving room for the matrices and
// wave parameters vertex.glsl already carries. It must match MAX_BONES in
// resources/shaders/skin_common.glsl — model_shader_gl_test.go pins the pair.
//
// A std140 uniform block is the way past it if a model ever needs more: GL 4.3
// guarantees 16 KB per block, which is 256 matrices.
const MaxSkinBones = 48
