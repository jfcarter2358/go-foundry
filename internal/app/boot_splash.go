package app

// boot_splash.go — the progress bar the window shows while the engine starts.
//
// Between the window appearing and the first frame of the loop, Run compiles
// every shader, stands up the passes, loads the start level and opens the
// editor — seconds of work during which nothing used to be drawn or pumped, so
// the window sat black and the OS called it "not responding". bootStep draws
// one frame of this screen and drains the event queue between those stages.
//
// Drawn with internal/uidraw, whose shader is inline and needs nothing from
// resources/, so it can come up straight after gl.Init. The text goes through
// the game's theme font, which falls back to the compiled-in bitmap face.
//
// Each stage's duration is logged ("boot: <stage> 812 ms"), which is how the
// next slow startup gets found.

import (
	"log"
	"os"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/ui"
	"foundry/internal/uidraw"
	"foundry/internal/uifont"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/veandco/go-sdl2/sdl"
)

var splash struct {
	on       bool
	list     uidraw.List
	label    string
	frac     float32
	began    time.Time // when the current stage started
	start    time.Time // when the splash came up
	lastDraw time.Time
}

// bootSplashBegin puts the splash up. Called once the GL context and the UI
// renderer exist.
func bootSplashBegin() {
	splash.on = true
	splash.start = time.Now()
	bootStep(0, "Starting graphics")
}

// bootStep ends the current stage and starts the next: logs how long the last
// one took, then draws the bar at frac with label under it.
func bootStep(frac float32, label string) {
	if !splash.on {
		return
	}
	now := time.Now()
	if splash.label != "" {
		log.Printf("boot: %s %d ms", splash.label, now.Sub(splash.began).Milliseconds())
	}
	splash.label, splash.began = label, now
	bootProgress(frac, label, true)
}

// bootProgress redraws the bar without ending the stage, for work that reports
// its own progress (the start level's load job). Throttled unless force, so a
// tight loop calling it is not bound by the swap.
func bootProgress(frac float32, detail string, force bool) {
	if !splash.on {
		return
	}
	if frac > splash.frac {
		splash.frac = min(frac, 1)
	}
	now := time.Now()
	if !force && now.Sub(splash.lastDraw) < 33*time.Millisecond {
		return
	}
	splash.lastDraw = now
	pumpBootEvents()
	drawBootSplash(detail)
}

// bootSplashEnd logs the last stage and the total. The frame loop's first swap
// replaces what is on screen.
func bootSplashEnd() {
	if !splash.on {
		return
	}
	now := time.Now()
	log.Printf("boot: %s %d ms", splash.label, now.Sub(splash.began).Milliseconds())
	log.Printf("boot: window to first frame %d ms", now.Sub(splash.start).Milliseconds())
	splash.on = false
}

// pumpBootEvents keeps the window answering the OS. Everything but a close is
// dropped — Run drains the queue again before the loop for the same reason —
// and a close ends the process, since there is nothing yet to save.
func pumpBootEvents() {
	sdl.PumpEvents()
	for e := sdl.PollEvent(); e != nil; e = sdl.PollEvent() {
		if _, ok := e.(*sdl.QuitEvent); ok {
			log.Printf("window closed during startup (%s)", splash.label)
			os.Exit(0)
		}
	}
}

func drawBootSplash(detail string) {
	winW, winH := platform.Window.GetSize()
	fbW, fbH := platform.Window.GLGetDrawableSize()
	if winW <= 0 || winH <= 0 || fbW <= 0 || fbH <= 0 {
		return
	}
	scale := float32(fbW) / float32(winW)
	w, h := float32(winW), float32(winH)

	theme := ui.ActiveTheme
	name, size := theme.FontFor(ui.TextSpec{})
	text := uifont.Face(name, size)
	title := uifont.Face(name, size*1.6)

	// Measuring bakes the glyphs; uploading them before the list is built is
	// what gives AddText a texture that already holds them.
	uifont.Measure(title, platform.WinTitle)
	uifont.Measure(text, detail)
	uidraw.SyncFontAtlases()
	gfx.DrainTextureUploads()

	l := &splash.list
	l.Reset(ui.Rect{W: w, H: h})
	white := uidraw.White()

	barW := min(w*0.4, 520)
	barH := float32(6)
	bar := ui.Rect{X: (w - barW) / 2, Y: h * 0.62, W: barW, H: barH}

	l.AddText(title, gfx.Textures[title.TexName], ui.Rect{X: 0, Y: bar.Y - 24 - title.LineHeight, W: w, H: title.LineHeight},
		platform.WinTitle, ui.AlignCenter, ui.AlignMiddle, false, uidraw.PackColor(theme.TextColor))
	l.AddRect(bar, white, uidraw.PackColor(theme.BarBack))
	fill := bar
	fill.W *= splash.frac
	l.AddRect(fill, white, uidraw.PackColor(theme.BarFill))
	l.AddText(text, gfx.Textures[text.TexName], ui.Rect{X: 0, Y: bar.Y + barH + 14, W: w, H: text.LineHeight},
		detail, ui.AlignCenter, ui.AlignMiddle, false, uidraw.PackColor(theme.TextMuted))

	// The default framebuffer, cleared to the panel colour; the clear colour
	// and binding are put back for whatever set them.
	var prevFB int32
	var prevClear [4]float32
	gl.GetIntegerv(gl.DRAW_FRAMEBUFFER_BINDING, &prevFB)
	gl.GetFloatv(gl.COLOR_CLEAR_VALUE, &prevClear[0])
	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, 0)
	gl.Disable(gl.SCISSOR_TEST)
	bg := theme.PanelColor
	gl.ClearColor(bg[0], bg[1], bg[2], 1)
	gl.Clear(gl.COLOR_BUFFER_BIT)
	uidraw.Render(l, fbW, fbH, scale)
	platform.Window.GLSwap()
	gl.ClearColor(prevClear[0], prevClear[1], prevClear[2], prevClear[3])
	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, uint32(prevFB))
}
