package imguibackend

// ImGui SDL2 + OpenGL 4.1 backend for the Foundry level editor.
//
// Platform backend  : translates SDL2 events → imgui IO
// Renderer backend  : uploads imgui draw data to OpenGL 4.1

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"unsafe"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// ---------- globals ----------

var ImguiContext *imgui.Context

// SharedFontAtlas is the main context's font atlas, captured once it's built
// (ImGuiInit) and handed to every secondary UIWindow's CreateContext call
// (window.go). Despite the imgui-go v4 doc comment ("all contexts share a
// same FontAtlas by default"), a freshly created context does NOT actually
// come with a built atlas — passing nil here asserts on the first NewFrame
// ("Font Atlas not built!"). Passing the main atlas explicitly is what
// actually makes glyphs available (and shared, so imguiFontTexture stays a
// single GL object every window's renderer can bind).
var SharedFontAtlas imgui.FontAtlas

// imgui OpenGL resources
var (
	imguiShaderProgram  uint32
	imguiAttribLocation struct {
		ProjMtx  int32
		Texture  int32
		Position uint32
		UV       uint32
		Color    uint32
	}
	imguiFontTexture             uint32
	imguiVAO, imguiVBO, imguiEBO uint32
)

// ---------- Init / Shutdown ----------

// ImGuiInit creates the imgui context, maps keys, and uploads the font atlas.
// Must be called after the OpenGL context is current.
func ImGuiInit() {
	ImguiContext = imgui.CreateContext(nil)
	io := imgui.CurrentIO()
	io.SetIniFilename("") // disable imgui.ini writes
	ImguiConfigureIO(io)

	imguiCreateDeviceObjects()
	SharedFontAtlas = io.Fonts()
}

// ImguiConfigureIO wires SDL scancodes to imgui key indices and gives the
// context the system clipboard. Shared by the main window's ImGuiInit and every
// secondary UIWindow's imgui context (window.go) — both need the same setup,
// since each context has its own IO, and a window that got one half of this and
// not the other would be the sort of bug nobody looks for.
func ImguiConfigureIO(io imgui.IO) {
	io.KeyMap(imgui.KeyTab, int(sdl.SCANCODE_TAB))
	io.KeyMap(imgui.KeyLeftArrow, int(sdl.SCANCODE_LEFT))
	io.KeyMap(imgui.KeyRightArrow, int(sdl.SCANCODE_RIGHT))
	io.KeyMap(imgui.KeyUpArrow, int(sdl.SCANCODE_UP))
	io.KeyMap(imgui.KeyDownArrow, int(sdl.SCANCODE_DOWN))
	io.KeyMap(imgui.KeyPageUp, int(sdl.SCANCODE_PAGEUP))
	io.KeyMap(imgui.KeyPageDown, int(sdl.SCANCODE_PAGEDOWN))
	io.KeyMap(imgui.KeyHome, int(sdl.SCANCODE_HOME))
	io.KeyMap(imgui.KeyEnd, int(sdl.SCANCODE_END))
	io.KeyMap(imgui.KeyInsert, int(sdl.SCANCODE_INSERT))
	io.KeyMap(imgui.KeyDelete, int(sdl.SCANCODE_DELETE))
	io.KeyMap(imgui.KeyBackspace, int(sdl.SCANCODE_BACKSPACE))
	io.KeyMap(imgui.KeySpace, int(sdl.SCANCODE_SPACE))
	io.KeyMap(imgui.KeyEnter, int(sdl.SCANCODE_RETURN))
	io.KeyMap(imgui.KeyEscape, int(sdl.SCANCODE_ESCAPE))
	io.KeyMap(imgui.KeyKeyPadEnter, int(sdl.SCANCODE_KP_ENTER))
	io.KeyMap(imgui.KeyA, int(sdl.SCANCODE_A))
	io.KeyMap(imgui.KeyC, int(sdl.SCANCODE_C))
	io.KeyMap(imgui.KeyV, int(sdl.SCANCODE_V))
	io.KeyMap(imgui.KeyX, int(sdl.SCANCODE_X))
	io.KeyMap(imgui.KeyY, int(sdl.SCANCODE_Y))
	io.KeyMap(imgui.KeyZ, int(sdl.SCANCODE_Z))
	io.KeyCtrl(int(sdl.SCANCODE_LCTRL), int(sdl.SCANCODE_RCTRL))
	io.KeyShift(int(sdl.SCANCODE_LSHIFT), int(sdl.SCANCODE_RSHIFT))
	io.KeyAlt(int(sdl.SCANCODE_LALT), int(sdl.SCANCODE_RALT))

	// Without this, ImGui falls back to a clipboard of its own that never
	// leaves the process — Ctrl+C here would be invisible to every other
	// program on the desktop. See editor_clipboard.go.
	io.SetClipboard(SdlClipboard{})
}

// ImGuiShutdown destroys all OpenGL resources and the imgui context.
func ImGuiShutdown() {
	imguiDestroyDeviceObjects()
	ImguiContext.Destroy()
}

// ---------- Per-frame ----------

// ImGuiNewFrame must be called once per frame before any imgui calls.
// It feeds the SDL2 state into the imgui IO for the main window.
//
// Mouse position comes from the global cursor position minus the main
// window's screen position, not sdl.GetMouseState (which is relative to
// whichever window currently has focus) — with a secondary UIWindow open,
// that would report wrong coordinates for the window that isn't focused. See
// uiWindowNewFrame (window.go) for the equivalent on a secondary window.
func ImGuiNewFrame(dt float32) {
	io := imgui.CurrentIO()
	gx, gy, mbuttons := sdl.GetGlobalMouseState()
	wx, wy := platform.Window.GetPosition()
	ImguiNewFrameCommon(io, float32(gx-wx), float32(gy-wy), mbuttons, platform.WinWidth, platform.WinHeight, dt)
}

// ImguiNewFrameCommon applies display size, delta time and mouse state to io
// and starts the imgui frame. Shared by ImGuiNewFrame (main window) and
// uiWindowNewFrame (window.go) so the two can't drift apart.
func ImguiNewFrameCommon(io imgui.IO, mouseX, mouseY float32, mbuttons sdl.ButtonMask, w, h int32, dt float32) {
	io.SetDisplaySize(imgui.Vec2{X: float32(w), Y: float32(h)})
	io.SetDeltaTime(dt)
	io.SetMousePosition(imgui.Vec2{X: mouseX, Y: mouseY})
	io.SetMouseButtonDown(0, mbuttons&sdl.ButtonLMask != 0)
	io.SetMouseButtonDown(1, mbuttons&sdl.ButtonRMask != 0)
	io.SetMouseButtonDown(2, mbuttons&sdl.ButtonMMask != 0)
	imgui.NewFrame()
}

// ImGuiProcessEvent translates one SDL2 event into imgui IO.
// Returns true if imgui consumed the event.
func ImGuiProcessEvent(event sdl.Event) bool {
	io := imgui.CurrentIO()
	switch t := event.(type) {
	case sdl.MouseWheelEvent:
		if t.X > 0 {
			io.AddMouseWheelDelta(1, 0)
		} else if t.X < 0 {
			io.AddMouseWheelDelta(-1, 0)
		}
		if t.Y > 0 {
			io.AddMouseWheelDelta(0, 1)
		} else if t.Y < 0 {
			io.AddMouseWheelDelta(0, -1)
		}
		return io.WantCaptureMouse()
	case sdl.KeyboardEvent:
		sc := int(t.Keysym.Scancode)
		if t.Type == sdl.KEYDOWN {
			io.KeyPress(sc)
		} else {
			io.KeyRelease(sc)
		}
		return io.WantCaptureKeyboard()
	case sdl.TextInputEvent:
		io.AddInputCharacters(string(t.GetText()))
		return io.WantCaptureKeyboard()
	}
	return false
}

// ImGuiRender finalises the imgui frame and issues OpenGL draw calls for the
// main window. Secondary windows go through RenderSecondaryWindows
// (window.go), which calls ImguiRenderDrawData directly with their own
// VAO/VBO/EBO and size.
func ImGuiRender() {
	imgui.Render()
	ImguiRenderDrawData(imgui.RenderedDrawData(), imguiVAO, imguiVBO, imguiEBO)
}

// ---------- OpenGL renderer ----------

func imguiCreateDeviceObjects() {
	// Build shader
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
		panic(fmt.Sprintf("imgui vertex shader: %v", err))
	}
	frag, err := gfx.CompileShader(fragSrc, gl.FRAGMENT_SHADER)
	if err != nil {
		panic(fmt.Sprintf("imgui fragment shader: %v", err))
	}
	imguiShaderProgram = gl.CreateProgram()
	gl.AttachShader(imguiShaderProgram, vert)
	gl.AttachShader(imguiShaderProgram, frag)
	gl.LinkProgram(imguiShaderProgram)
	gl.DeleteShader(vert)
	gl.DeleteShader(frag)

	imguiAttribLocation.ProjMtx = gl.GetUniformLocation(imguiShaderProgram, gl.Str("ProjMtx\x00"))
	imguiAttribLocation.Texture = gl.GetUniformLocation(imguiShaderProgram, gl.Str("Texture\x00"))
	imguiAttribLocation.Position = uint32(gl.GetAttribLocation(imguiShaderProgram, gl.Str("Position\x00")))
	imguiAttribLocation.UV = uint32(gl.GetAttribLocation(imguiShaderProgram, gl.Str("UV\x00")))
	imguiAttribLocation.Color = uint32(gl.GetAttribLocation(imguiShaderProgram, gl.Str("Color\x00")))

	gl.GenBuffers(1, &imguiVBO)
	gl.GenBuffers(1, &imguiEBO)
	gl.GenVertexArrays(1, &imguiVAO)

	imguiCreateFontsTexture()
}

func imguiCreateFontsTexture() {
	io := imgui.CurrentIO()
	image := io.Fonts().TextureDataRGBA32()
	gl.GenTextures(1, &imguiFontTexture)
	gl.BindTexture(gl.TEXTURE_2D, imguiFontTexture)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.PixelStorei(gl.UNPACK_ROW_LENGTH, 0)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA,
		int32(image.Width), int32(image.Height), 0,
		gl.RGBA, gl.UNSIGNED_BYTE, image.Pixels)
	io.Fonts().SetTextureID(imgui.TextureID(imguiFontTexture))
}

func imguiDestroyDeviceObjects() {
	if imguiVAO != 0 {
		gl.DeleteVertexArrays(1, &imguiVAO)
		imguiVAO = 0
	}
	if imguiVBO != 0 {
		gl.DeleteBuffers(1, &imguiVBO)
		imguiVBO = 0
	}
	if imguiEBO != 0 {
		gl.DeleteBuffers(1, &imguiEBO)
		imguiEBO = 0
	}
	if imguiShaderProgram != 0 {
		gl.DeleteProgram(imguiShaderProgram)
		imguiShaderProgram = 0
	}
	if imguiFontTexture != 0 {
		gl.DeleteTextures(1, &imguiFontTexture)
		imgui.CurrentIO().Fonts().SetTextureID(0)
		imguiFontTexture = 0
	}
}

// ImguiRenderDrawData issues the GL draw calls for one imgui frame into
// whichever window's context is currently current, using that window's own
// vao/vbo/ebo (VAOs are never shared across GL contexts). Framebuffer size
// comes from drawData itself (set via io.SetDisplaySize before NewFrame), not
// from WinWidth/WinHeight, so this already works unmodified for a secondary
// window — only the vertex array/buffer objects need to vary per window.
func ImguiRenderDrawData(drawData imgui.DrawData, vao, vbo, ebo uint32) {
	if !drawData.Valid() {
		return
	}

	displayPos := drawData.DisplayPos()
	displaySize := drawData.DisplaySize()
	fbWidth := int32(displaySize.X)
	fbHeight := int32(displaySize.Y)
	if fbWidth == 0 || fbHeight == 0 {
		return
	}

	// Save GL state
	var lastProgram int32
	gl.GetIntegerv(gl.CURRENT_PROGRAM, &lastProgram)
	var lastTex int32
	gl.GetIntegerv(gl.TEXTURE_BINDING_2D, &lastTex)
	var lastVAO int32
	gl.GetIntegerv(gl.VERTEX_ARRAY_BINDING, &lastVAO)
	var lastArrayBuf int32
	gl.GetIntegerv(gl.ARRAY_BUFFER_BINDING, &lastArrayBuf)
	var lastScissorBox [4]int32
	gl.GetIntegerv(gl.SCISSOR_BOX, &lastScissorBox[0])
	var lastViewport [4]int32
	gl.GetIntegerv(gl.VIEWPORT, &lastViewport[0])
	cullFaceEnabled := gl.IsEnabled(gl.CULL_FACE)
	blendEnabled := gl.IsEnabled(gl.BLEND)
	depthTestEnabled := gl.IsEnabled(gl.DEPTH_TEST)
	scissorEnabled := gl.IsEnabled(gl.SCISSOR_TEST)

	// Setup render state
	gl.Enable(gl.BLEND)
	gl.BlendEquation(gl.FUNC_ADD)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
	gl.Disable(gl.CULL_FACE)
	gl.Disable(gl.DEPTH_TEST)
	gl.Enable(gl.SCISSOR_TEST)

	gl.Viewport(0, 0, fbWidth, fbHeight)

	// Orthographic projection: (0,0) top-left, (W,H) bottom-right
	L := displayPos.X
	R := displayPos.X + displaySize.X
	T := displayPos.Y
	B := displayPos.Y + displaySize.Y
	ortho := mgl32.Mat4{
		2.0 / (R - L), 0, 0, 0,
		0, 2.0 / (T - B), 0, 0,
		0, 0, -1, 0,
		(R + L) / (L - R), (T + B) / (B - T), 0, 1,
	}

	gl.UseProgram(imguiShaderProgram)
	gl.Uniform1i(imguiAttribLocation.Texture, 0)
	gl.UniformMatrix4fv(imguiAttribLocation.ProjMtx, 1, false, &ortho[0])

	gl.BindVertexArray(vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, ebo)

	// Vertex layout: [pos xy (8B)] [uv xy (8B)] [color rgba (4B)] = 20 bytes
	vertexSize, posOffset, uvOffset, colOffset := imgui.VertexBufferLayout()
	indexSize, _ := imgui.IndexBufferLayout(), 0

	gl.EnableVertexAttribArray(imguiAttribLocation.Position)
	gl.VertexAttribPointerWithOffset(imguiAttribLocation.Position, 2, gl.FLOAT, false,
		int32(vertexSize), uintptr(posOffset))

	gl.EnableVertexAttribArray(imguiAttribLocation.UV)
	gl.VertexAttribPointerWithOffset(imguiAttribLocation.UV, 2, gl.FLOAT, false,
		int32(vertexSize), uintptr(uvOffset))

	gl.EnableVertexAttribArray(imguiAttribLocation.Color)
	gl.VertexAttribPointerWithOffset(imguiAttribLocation.Color, 4, gl.UNSIGNED_BYTE, true,
		int32(vertexSize), uintptr(colOffset))

	for _, list := range drawData.CommandLists() {
		// Upload vertices
		vertexBufPtr, vertexBufSize := list.VertexBuffer()
		gl.BufferData(gl.ARRAY_BUFFER, vertexBufSize, vertexBufPtr, gl.STREAM_DRAW)

		// Upload indices
		indexBufPtr, indexBufSize := list.IndexBuffer()
		gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, indexBufSize, indexBufPtr, gl.STREAM_DRAW)

		idxType := uint32(gl.UNSIGNED_SHORT)
		if indexSize == 4 {
			idxType = gl.UNSIGNED_INT
		}

		for _, cmd := range list.Commands() {
			if cmd.HasUserCallback() {
				cmd.CallUserCallback(list)
				continue
			}
			clipRect := cmd.ClipRect()
			// Scissor rect in OpenGL coords (y flipped)
			x := int32(clipRect.X - displayPos.X)
			y := int32(float32(fbHeight) - (clipRect.W - displayPos.Y))
			w := int32(clipRect.Z - clipRect.X)
			h := int32(clipRect.W - clipRect.Y)
			if w <= 0 || h <= 0 {
				continue
			}
			gl.Scissor(x, y, w, h)
			gl.ActiveTexture(gl.TEXTURE0)
			gl.BindTexture(gl.TEXTURE_2D, uint32(cmd.TextureID()))
			gl.DrawElementsBaseVertex(
				gl.TRIANGLES,
				int32(cmd.ElementCount()),
				idxType,
				unsafe.Add(nil, int(cmd.IndexOffset())*indexSize),
				int32(cmd.VertexOffset()),
			)
		}
	}

	// Restore GL state
	gl.UseProgram(uint32(lastProgram))
	gl.BindTexture(gl.TEXTURE_2D, uint32(lastTex))
	gl.BindVertexArray(uint32(lastVAO))
	gl.BindBuffer(gl.ARRAY_BUFFER, uint32(lastArrayBuf))
	gl.Scissor(lastScissorBox[0], lastScissorBox[1], lastScissorBox[2], lastScissorBox[3])
	gl.Viewport(lastViewport[0], lastViewport[1], lastViewport[2], lastViewport[3])
	if blendEnabled {
		gl.Enable(gl.BLEND)
	} else {
		gl.Disable(gl.BLEND)
	}
	if cullFaceEnabled {
		gl.Enable(gl.CULL_FACE)
	} else {
		gl.Disable(gl.CULL_FACE)
	}
	if depthTestEnabled {
		gl.Enable(gl.DEPTH_TEST)
	} else {
		gl.Disable(gl.DEPTH_TEST)
	}
	if scissorEnabled {
		gl.Enable(gl.SCISSOR_TEST)
	} else {
		gl.Disable(gl.SCISSOR_TEST)
	}
}
