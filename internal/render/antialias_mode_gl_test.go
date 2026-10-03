package render

// antialias_mode_gl_test.go — the "off" anti-aliasing mode (gfx.AAModeOff):
// a game frame drawn with the window's multisampling and alpha-to-coverage
// off, and both put back for what draws after it; the editor's own views
// keep them.
//
//	FOUNDRY_GL_TEST=1 go test -run AntiAliasOff ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/gltest"

	"github.com/go-gl/gl/v4.3-core/gl"
)

func TestAntiAliasOffFrameDropsTheWindowSamples(t *testing.T) {
	gltest.Context(t)
	mode, granted, enabled := gfx.AntiAliasMode, AntiAliasGrantedSamples, AntiAliasEnabled
	t.Cleanup(func() {
		gfx.AntiAliasMode, AntiAliasGrantedSamples, AntiAliasEnabled = mode, granted, enabled
		aaOffFrame = false
		SetAlphaToCoverage(false)
		setMultisampleGLState()
	})
	// A window with samples, as a development build keeps for its editor.
	AntiAliasGrantedSamples, AntiAliasEnabled = 4, true
	setMultisampleGLState()

	for _, c := range []struct {
		name   string
		mode   gfx.AAMode
		editor bool
		want   bool // multisampling and alpha-to-coverage inside the frame
	}{
		{"off", gfx.AAModeOff, false, false},
		{"off, the editor's view", gfx.AAModeOff, true, true},
		{"msaa", gfx.AAModeMSAA, false, true},
	} {
		gfx.AntiAliasMode = c.mode
		beginAAModeFrame(WorldDraw{Editor: c.editor})
		SetAlphaToCoverage(true)
		if got := gl.IsEnabled(gl.MULTISAMPLE); got != c.want {
			t.Errorf("%s: GL_MULTISAMPLE in the frame is %v, want %v", c.name, got, c.want)
		}
		if alphaToCoverageOn != c.want {
			t.Errorf("%s: alpha-to-coverage in the frame is %v, want %v", c.name, alphaToCoverageOn, c.want)
		}
		SetAlphaToCoverage(false)
		endAAModeFrame()
		if !gl.IsEnabled(gl.MULTISAMPLE) {
			t.Errorf("%s: GL_MULTISAMPLE not put back after the frame", c.name)
		}
	}
}
