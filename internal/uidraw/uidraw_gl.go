package uidraw

// uidraw_gl.go — the GL objects and the draw.
//
// Modelled line for line on imguibackend.ImguiRenderDrawData: the same
// twenty-byte vertex, the same inline shader pair, the same exhaustive state
// save and restore. That renderer has already been through every scissor and
// state-leak bug this one could have, and matching it is how those fixes carry
// over rather than being rediscovered.
//
// The shader is built inline rather than read from resources/shaders/ because
// the UI has to be able to draw before any asset directory is guaranteed to
// exist — a loading screen covering the load of the very thing that would have
// contained it — and because it is twelve lines.

import (
	"fmt"
	"image"

	"foundry/internal/gfx"
	"foundry/internal/ui"
	"foundry/internal/uifont"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const vertexStride = 20 // 2 floats position, 2 floats uv, 4 bytes colour

var (
	program  uint32
	vao      uint32
	vbo, ebo uint32
	whiteTex uint32

	uProjMtx int32
	uTexture int32
	aPos     uint32
	aUV      uint32
	aColor   uint32

	initialised bool
)

// VAOOverride, when non-zero, is the vertex array this pass binds instead of
// its own.
//
// Vertex array objects are never shared between GL contexts, though buffers,
// programs and textures are. A secondary window therefore supplies one of its
// own for the duration of its frame — the arrangement gizmo.GizmoVAOOverride
// and render.SpriteVAOOverride already use, for the same reason. A bare VAO
// carries no attribute state, which is why the pointers are re-specified on
// every draw rather than once at init.
var VAOOverride uint32

// Init compiles the program and creates the shared buffers and the white pixel.
// Safe to call every frame; it does its work once.
func Init() error {
	if initialised {
		return nil
	}

	vertSrc := "#version 410\n" +
		"uniform mat4 ProjMtx;\n" +
		"in vec2 Position;\n" +
		"in vec2 UV;\n" +
		"in vec4 Color;\n" +
		"out vec2 Frag_UV;\n" +
		"out vec4 Frag_Color;\n" +
		"void main() {\n" +
		"  Frag_UV = UV; Frag_Color = Color;\n" +
		"  gl_Position = ProjMtx * vec4(Position.xy, 0, 1);\n" +
		"}\n"
	fragSrc := "#version 410\n" +
		"uniform sampler2D Texture;\n" +
		"in vec2 Frag_UV;\n" +
		"in vec4 Frag_Color;\n" +
		"out vec4 Out_Color;\n" +
		"void main() { Out_Color = Frag_Color * texture(Texture, Frag_UV.st); }\n"

	vert, err := gfx.CompileShader(vertSrc, gl.VERTEX_SHADER)
	if err != nil {
		return fmt.Errorf("ui vertex shader: %w", err)
	}
	frag, err := gfx.CompileShader(fragSrc, gl.FRAGMENT_SHADER)
	if err != nil {
		return fmt.Errorf("ui fragment shader: %w", err)
	}
	program = gl.CreateProgram()
	gl.AttachShader(program, vert)
	gl.AttachShader(program, frag)
	gl.LinkProgram(program)
	gl.DeleteShader(vert)
	gl.DeleteShader(frag)

	uProjMtx = gl.GetUniformLocation(program, gl.Str("ProjMtx\x00"))
	uTexture = gl.GetUniformLocation(program, gl.Str("Texture\x00"))
	aPos = uint32(gl.GetAttribLocation(program, gl.Str("Position\x00")))
	aUV = uint32(gl.GetAttribLocation(program, gl.Str("UV\x00")))
	aColor = uint32(gl.GetAttribLocation(program, gl.Str("Color\x00")))

	gl.GenBuffers(1, &vbo)
	gl.GenBuffers(1, &ebo)
	gl.GenVertexArrays(1, &vao)

	createWhitePixel()
	initialised = true
	return nil
}

// White is the one-by-one opaque texture a flat fill samples.
//
// It exists so there is one shader with no mode uniform: a solid rectangle, an
// image and a glyph all come out of `Color * texture(Tex, UV)`, which means
// they also all batch together whenever the clip allows.
func White() uint32 { return whiteTex }

func createWhitePixel() {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Pix[0], img.Pix[1], img.Pix[2], img.Pix[3] = 255, 255, 255, 255

	gl.GenTextures(1, &whiteTex)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, whiteTex)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, 1, 1, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(img.Pix))
}

// SyncFontAtlases queues every glyph page whose Rev has outrun what the GPU
// last saw.
//
// Must be called immediately before gfx.DrainTextureUploads, and both well
// before the UI draws — that ordering is what lets a glyph seen for the first
// time this frame be resident by the time it is drawn. A glyph baked *after*
// this point still draws as a box for one frame; see Atlas.advanceOf.
func SyncFontAtlases() {
	for _, a := range uifont.Faces() {
		if a.Rev == a.UploadedRev {
			continue
		}
		gfx.QueueUITextureUpload(a.TexName, a.Pix)
		a.UploadedRev = a.Rev
	}
}

// Render issues a built list against a framebuffer of the given pixel size.
//
// scale is canvas units per device pixel; it is folded into the projection so
// that the list itself stays in canvas units, which is what makes the editor's
// preview and the game's HUD come out identical at different window sizes.
func Render(l *List, fbW, fbH int32, scale float32) {
	if !initialised || l.Empty() || fbW <= 0 || fbH <= 0 {
		return
	}

	var lastProgram, lastTex, lastVAO, lastArrayBuf, lastElemBuf int32
	gl.GetIntegerv(gl.CURRENT_PROGRAM, &lastProgram)
	gl.GetIntegerv(gl.TEXTURE_BINDING_2D, &lastTex)
	gl.GetIntegerv(gl.VERTEX_ARRAY_BINDING, &lastVAO)
	gl.GetIntegerv(gl.ARRAY_BUFFER_BINDING, &lastArrayBuf)
	gl.GetIntegerv(gl.ELEMENT_ARRAY_BUFFER_BINDING, &lastElemBuf)
	var lastScissor, lastViewport [4]int32
	gl.GetIntegerv(gl.SCISSOR_BOX, &lastScissor[0])
	gl.GetIntegerv(gl.VIEWPORT, &lastViewport[0])
	cullWas := gl.IsEnabled(gl.CULL_FACE)
	blendWas := gl.IsEnabled(gl.BLEND)
	depthWas := gl.IsEnabled(gl.DEPTH_TEST)
	scissorWas := gl.IsEnabled(gl.SCISSOR_TEST)
	var depthMaskWas bool
	gl.GetBooleanv(gl.DEPTH_WRITEMASK, &depthMaskWas)

	gl.Enable(gl.BLEND)
	gl.BlendEquation(gl.FUNC_ADD)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
	gl.Disable(gl.CULL_FACE)
	gl.Disable(gl.DEPTH_TEST)
	gl.DepthMask(false)
	gl.Enable(gl.SCISSOR_TEST)
	gl.Viewport(0, 0, fbW, fbH)

	// Canvas units in, clip space out: (0,0) top-left, growing right and down.
	w := float32(fbW) / scale
	h := float32(fbH) / scale
	ortho := mgl32.Mat4{
		2 / w, 0, 0, 0,
		0, -2 / h, 0, 0,
		0, 0, -1, 0,
		-1, 1, 0, 1,
	}

	gl.UseProgram(program)
	gl.Uniform1i(uTexture, 0)
	gl.UniformMatrix4fv(uProjMtx, 1, false, &ortho[0])

	bind := vao
	if VAOOverride != 0 {
		bind = VAOOverride
	}
	gl.BindVertexArray(bind)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, ebo)

	gl.EnableVertexAttribArray(aPos)
	gl.VertexAttribPointerWithOffset(aPos, 2, gl.FLOAT, false, vertexStride, 0)
	gl.EnableVertexAttribArray(aUV)
	gl.VertexAttribPointerWithOffset(aUV, 2, gl.FLOAT, false, vertexStride, 8)
	gl.EnableVertexAttribArray(aColor)
	gl.VertexAttribPointerWithOffset(aColor, 4, gl.UNSIGNED_BYTE, true, vertexStride, 16)

	gl.BufferData(gl.ARRAY_BUFFER, len(l.Verts)*vertexStride, gl.Ptr(l.Verts), gl.STREAM_DRAW)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(l.Idx)*bytesPerIndex, gl.Ptr(l.Idx), gl.STREAM_DRAW)

	gl.ActiveTexture(gl.TEXTURE0)
	for _, cmd := range l.Cmds {
		if cmd.IdxCount == 0 {
			continue
		}
		if !setScissor(cmd.Clip, scale, fbH) {
			continue
		}
		gl.BindTexture(gl.TEXTURE_2D, cmd.Tex)
		gl.DrawElementsWithOffset(gl.TRIANGLES, int32(cmd.IdxCount), gl.UNSIGNED_INT,
			uintptr(cmd.IdxOffset*bytesPerIndex))
	}

	gl.UseProgram(uint32(lastProgram))
	gl.BindTexture(gl.TEXTURE_2D, uint32(lastTex))
	gl.BindVertexArray(uint32(lastVAO))
	gl.BindBuffer(gl.ARRAY_BUFFER, uint32(lastArrayBuf))
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, uint32(lastElemBuf))
	gl.Scissor(lastScissor[0], lastScissor[1], lastScissor[2], lastScissor[3])
	gl.Viewport(lastViewport[0], lastViewport[1], lastViewport[2], lastViewport[3])
	setEnabled(gl.CULL_FACE, cullWas)
	setEnabled(gl.BLEND, blendWas)
	setEnabled(gl.DEPTH_TEST, depthWas)
	setEnabled(gl.SCISSOR_TEST, scissorWas)
	gl.DepthMask(depthMaskWas)
}

// setScissor converts a clip rect from canvas units to the scissor box, which
// GL measures from the *bottom* left. Reports false when nothing is left of it,
// so a fully clipped command is skipped rather than issued with a negative box.
func setScissor(clip ui.Rect, scale float32, fbH int32) bool {
	x := int32(clip.X * scale)
	y := int32(clip.Y * scale)
	w := int32(clip.W*scale + 0.5)
	h := int32(clip.H*scale + 0.5)
	if w <= 0 || h <= 0 {
		return false
	}
	gl.Scissor(x, fbH-(y+h), w, h)
	return true
}

func setEnabled(cap uint32, on bool) {
	if on {
		gl.Enable(cap)
	} else {
		gl.Disable(cap)
	}
}

// Delete releases the GL objects. Called on shutdown; the per-window override
// VAOs belong to their windows and are not touched here.
func Delete() {
	if !initialised {
		return
	}
	gl.DeleteVertexArrays(1, &vao)
	gl.DeleteBuffers(1, &vbo)
	gl.DeleteBuffers(1, &ebo)
	gl.DeleteTextures(1, &whiteTex)
	gl.DeleteProgram(program)
	vao, vbo, ebo, whiteTex, program = 0, 0, 0, 0, 0
	initialised = false
}
