package gfx

// wind_shader.go — wind on static meshes: resources/shaders/wind_common.glsl
// spliced into the stages that place a cooked vertex, and the WindBlock
// uniform buffer they read the wind from.
//
// Same seam InjectSkinning and InjectFog are. The wind itself — direction,
// strength, clock — is one std140 block at WindBlockBinding rather than
// loose uniforms, because it is read by a dozen programs (the world program,
// the depth prepass, the visibility buffer's two passes, both shadow
// programs, the cull's compute stages, the compute rasteriser) and every one
// of them must see the same value in a frame, or a swaying branch sheds its
// shadow and its depth. Written once a frame (Wind.Set + Flush), bound once
// a frame and in every secondary context (BindSceneBlocks).

import (
	"fmt"
	"foundry/internal/fileutil"
	"strings"
	"sync"
	"unsafe"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// WindBlockBinding is wind_common.glsl's uniform block binding.
const WindBlockBinding = 2

// Instance attribute locations of a placement's wind, beside its matrix
// (AttribInstanceModel, 7-10): its mesh's bounding sphere and its wind
// (wind_common.glsl's windOffset). 11 and 12 are the visibility buffer's
// command record, so these take 13 and 14.
const (
	AttribInstanceSphere = 13
	AttribInstanceWind   = 14
)

var (
	windShaderMu  sync.Mutex
	windShaderSrc string
)

// WindCommonSource returns wind_common.glsl, reading it once. A failure is
// not cached, for SkinCommonSource's reason.
func WindCommonSource() (string, error) {
	windShaderMu.Lock()
	defer windShaderMu.Unlock()
	if windShaderSrc != "" {
		return windShaderSrc, nil
	}
	src, err := ReadShaderFile(fileutil.ShaderPath("wind_common.glsl"))
	if err != nil {
		return "", fmt.Errorf("read wind shader: %w", err)
	}
	windShaderSrc = src
	return src, nil
}

// InjectWind splices the shared wind source into a shader after its
// #version line (and after any #extension lines a compute stage carries,
// which GLSL wants before any declaration).
func InjectWind(src string) (string, error) {
	shared, err := WindCommonSource()
	if err != nil {
		return "", err
	}
	// The end of the leading run of #version / #extension lines.
	at := 0
	for at < len(src) {
		end := strings.IndexByte(src[at:], '\n')
		if end < 0 {
			end = len(src) - at
		}
		line := strings.TrimSpace(src[at : at+end])
		if !strings.HasPrefix(line, "#version") && !strings.HasPrefix(line, "#extension") {
			break
		}
		at += end + 1
	}
	if at == 0 {
		return InjectAfterVersion(src, shared), nil
	}
	at = min(at, len(src))
	return src[:at] + "\n" + shared + "\n" + src[at:], nil
}

// windBlock is WindBlock in std140: one vec4.
type windBlock struct {
	Now [4]float32
}

// WindBuffer is the WindBlock uniform buffer.
type WindBuffer struct {
	block, sent windBlock
	synced      bool
	buf         uint32
}

// Wind is the scene's wind, as every static mesh's vertex stage reads it.
var Wind WindBuffer

// Set records this frame's wind: its direction across the ground (need not
// be unit), strength (0: still) and clock in seconds.
func (w *WindBuffer) Set(dir mgl32.Vec2, strength, clock float32) {
	if l := dir.Len(); l > 1e-6 {
		dir = dir.Mul(1 / l)
	} else {
		dir = mgl32.Vec2{1, 0}
	}
	w.block.Now = [4]float32{dir[0], dir[1], max(strength, 0), clock}
}

// Strength is the strength last Set.
func (w *WindBuffer) Strength() float32 { return w.block.Now[2] }

func (w *WindBuffer) ensure() {
	if w.buf != 0 {
		return
	}
	gl.GenBuffers(1, &w.buf)
	gl.BindBuffer(gl.UNIFORM_BUFFER, w.buf)
	gl.BufferData(gl.UNIFORM_BUFFER, int(unsafe.Sizeof(windBlock{})), unsafe.Pointer(&w.block), gl.DYNAMIC_DRAW)
	gl.BindBuffer(gl.UNIFORM_BUFFER, 0)
	w.sent, w.synced = w.block, true
}

// Flush sends what was Set, if it changed, and binds the buffer.
func (w *WindBuffer) Flush() {
	if glBlocked("WindBuffer.Flush") {
		return
	}
	w.ensure()
	if !w.synced || w.sent != w.block {
		gl.BindBuffer(gl.UNIFORM_BUFFER, w.buf)
		gl.BufferSubData(gl.UNIFORM_BUFFER, 0, int(unsafe.Sizeof(windBlock{})), unsafe.Pointer(&w.block))
		gl.BindBuffer(gl.UNIFORM_BUFFER, 0)
		w.sent, w.synced = w.block, true
	}
	w.Bind()
}

// Bind binds the buffer at WindBlockBinding in the current context.
func (w *WindBuffer) Bind() {
	if glBlocked("WindBuffer.Bind") {
		return
	}
	w.ensure()
	gl.BindBufferBase(gl.UNIFORM_BUFFER, WindBlockBinding, w.buf)
}
