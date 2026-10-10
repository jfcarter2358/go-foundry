package editor

// editor_score_gl_test.go — the Score window drawn through a real context:
// the glyph atlas on the GPU, the score on paper, the toolbar's glyph
// buttons and the keypad, read back as pixels.
//
//	FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
//	SCORE_SHOT=/tmp/score.png FOUNDRY_GL_TEST=1 go test -run TestScoreGL ./internal/editor/
//
// The second form also writes the frame as a PNG, which is how the window's
// look is checked by eye without an editor session.

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/glext"
	"foundry/internal/gltest"
	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/scoredraw"
	"foundry/internal/testenv"
	"foundry/internal/uiwin"

	"github.com/go-gl/gl/v4.3-core/gl"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

func TestScoreGLWindowDraws(t *testing.T) {
	gltest.Context(t)
	prevRes := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	prevEd, prevRend, prevWin, prevKeys := scEd, scoreRend, scoreEditorWindow, scoreKeys
	t.Cleanup(func() {
		fileutil.ResourcesPath = prevRes
		scEd, scoreRend, scoreEditorWindow, scoreKeys = prevEd, prevRend, prevWin, prevKeys
		scoreList = nil
	})
	s, err := score.LoadFile(filepath.Join(fileutil.ResourcesPath, "sounds", "score_demo.fscore"))
	if err != nil {
		t.Fatal(err)
	}
	scEd = newScoreState(s)
	scoreKeys = scoreSibeliusKeymap()
	scoreRend = scoredraw.New()
	defer scoreRend.Release()
	const W, H = 1400, 900
	scoreEditorWindow = &uiwin.UIWindow{W: W, H: H}
	scoreShowKeypad = true
	scoreZoom = 9

	prevCtx, hadCtx := imgui.CurrentContext()
	imguibackend.ImGuiInit()
	ctx, _ := imgui.CurrentContext()
	defer func() {
		ctx.Destroy()
		if hadCtx == nil && prevCtx != nil {
			prevCtx.SetCurrent()
		}
	}()
	var vao, vbo, ebo uint32
	gl.GenVertexArrays(1, &vao)
	gl.GenBuffers(1, &vbo)
	gl.GenBuffers(1, &ebo)

	var fbo, rbo uint32
	gl.GenFramebuffers(1, &fbo)
	gl.GenRenderbuffers(1, &rbo)
	gl.BindRenderbuffer(gl.RENDERBUFFER, rbo)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.RGBA8, W, H)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.RENDERBUFFER, rbo)
	defer func() {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		gl.DeleteFramebuffers(1, &fbo)
		gl.DeleteRenderbuffers(1, &rbo)
	}()

	// A selection, note input with the pointer over the flute's staff (the
	// ghost), and a note of the violins selected.
	scEd.clickBar(2, 5, false, false)
	scEd.clickBar(3, 6, false, true)
	l := scoreLayout()
	scEd.selectEvent(edit.Loc{Line: 0, Bar: 5, Event: 1}, -1)
	scEd.Input = true
	sb := l.Staves[0]
	bb := l.Bar(3)
	mouse := imgui.Vec2{X: scoreHeaderW + scorePad + float32(bb.XAt(0.25))*scoreZoom, Y: editorTopBarH + scoreRulerH + scorePad + float32(sb.Top+0.5)*scoreZoom}

	frame := func() {
		io := imgui.CurrentIO()
		io.SetDisplaySize(imgui.Vec2{X: W, Y: H})
		io.SetDeltaTime(1.0 / 60)
		io.SetMousePosition(mouse)
		imgui.NewFrame()
		scoreToolbar(W)
		scoreViewPane(imgui.Vec2{X: 0, Y: editorTopBarH}, imgui.Vec2{X: W, Y: H - editorTopBarH - scoreStatusH})
		scoreStatusBar(imgui.Vec2{X: 0, Y: H - scoreStatusH}, W)
		scoreKeypadPanel(W, H)
		imgui.Render()
		gl.Viewport(0, 0, W, H)
		gl.ClearColor(0.09, 0.10, 0.12, 1)
		gl.Clear(gl.COLOR_BUFFER_BIT)
		imguibackend.ImguiRenderDrawData(imgui.RenderedDrawData(), vao, vbo, ebo)
	}
	frame()
	frame()
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("GL error 0x%x", e)
	}
	pix := make([]uint8, W*H*4)
	glext.ReadPixels(0, 0, W, H, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&pix[0]))
	// GL hands back straight colour; a window has no alpha, so the image
	// is opaque (as an image.RGBA it would be read as premultiplied, and the
	// PNG encoder's divide by alpha turns every antialiased edge to noise).
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		copy(img.Pix[y*W*4:(y+1)*W*4], pix[(H-1-y)*W*4:(H-y)*W*4])
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	if path := os.Getenv("SCORE_SHOT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	// The paper is paper, and there is ink on it: dark pixels inside the
	// music area, on the first staff's lines.
	paper, ink := 0, 0
	y0 := int(editorTopBarH+scoreRulerH+scorePad) + int(float32(sb.Top)*scoreZoom)
	for y := y0; y < y0+int(4*scoreZoom); y++ {
		for x := int(scoreHeaderW) + 20; x < 900; x++ {
			c := img.RGBAAt(x, y)
			switch {
			case c.R > 240 && c.G > 240 && c.B > 230:
				paper++
			case c.R < 110 && c.G < 110 && c.B < 120:
				ink++
			}
		}
	}
	if paper < 1000 || ink < 200 {
		t.Errorf("first staff: %d paper pixels, %d ink pixels", paper, ink)
	}
}
