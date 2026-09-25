package harness

// shot.go — the headless session's eyes.
//
// A run that can only read positions can tell you the door opened; it cannot
// tell you the door is inside out, that the room is pitch black, or that the
// muzzle flash is behind the gun. Those are the bugs a person spots in a
// second and a watch list never spots at all, so a session can draw.
//
// What it draws is the frame, not an approximation of it: render.DrawWorld is
// the same call app.Run makes, and the HUD on top of it is uidraw.RenderScreen
// over the same screen stack the game has up. The two differences from the
// window are that there is no window (an offscreen FBO through internal/glctx)
// and no editor.
//
// THE THREAD is the catch, and it is not a small one: a GL context is current
// on one OS thread, and glctx.Acquire locks the goroutine to it. Everything a
// drawing session does must therefore happen on the goroutine that opened it —
// which for `foundry play serve` means the session's own goroutine, with the
// HTTP handlers posting work to it rather than touching it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/collision"
	"foundry/internal/gfx"
	"foundry/internal/glctx"
	"foundry/internal/levelio"
	"foundry/internal/platform"
	"foundry/internal/render"
	"foundry/internal/ui"
	"foundry/internal/uidraw"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
)

// standUpDrawing makes the offscreen context and everything that draws through
// it. Called from Open, before the level is loaded, because the load is what
// uploads the level's meshes and textures and there has to be a context for
// them to go to.
func (s *Session) standUpDrawing() error {
	w, h := int32(s.opts.ShotW), int32(s.opts.ShotH)
	if err := glctx.Acquire(w, h); err != nil {
		return fmt.Errorf("no GL context (%v) — drawing needs a display and a GL 4.1 driver; "+
			"run without -draw to play with no pictures", err)
	}
	// The window the projection and the UI layout are measured against. There
	// is no window, but every one of them reads these two numbers.
	platform.WinWidth, platform.WinHeight = w, h
	s.opts.ViewW, s.opts.ViewH = float32(w), float32(h)

	if err := render.StandUpWorld(w, h); err != nil {
		return fmt.Errorf("stand up the world: %w", err)
	}
	ui.LoadActiveTheme()
	if err := uidraw.Init(); err != nil {
		return fmt.Errorf("ui renderer: %w", err)
	}
	target, err := glctx.NewTarget(w, h)
	if err != nil {
		return err
	}
	s.target = target
	return nil
}

// installDrawnLevel is what a load does on the GPU side, and it is app's
// installLevel minus the parts that belong to a running game: the chunk
// batches and the raycast trees built now rather than on the frame that first
// needs them, the lights uploaded, and the player's own arms loaded.
func (s *Session) installDrawnLevel() error {
	render.SyncChunkBatches(s.Level)
	collision.WarmRaycasts(s.Level)
	world.UploadLights(s.Level.Lights)
	if err := levelio.InstallPlayerModels(s.Level); err != nil {
		// Worth saying and not worth stopping for: a level whose viewmodel
		// fails to load still plays, with no hands.
		s.note("viewmodel", err.Error())
	}
	return nil
}

// Shot draws the player's view and writes it to a .png, answering where it
// went. An empty name is numbered after the ones before it.
//
// It is one frame of the game as the player sees it: the world through
// render.DrawWorld and then every screen the game has up, in the stack's own
// order. The editor's markers are not drawn, because this is not the editor —
// a volume's cage or a spawn arrow in a play test would be a picture of
// something the player never sees.
func (s *Session) Shot(name string) (string, error) {
	if s.target == nil {
		return "", fmt.Errorf("this session was opened without -draw, so it cannot take a picture")
	}
	path := s.shotPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}

	s.target.Bind()
	gl.Viewport(0, 0, int32(s.opts.ShotW), int32(s.opts.ShotH))

	// The drains a frame does before it draws: glyph pages baked by the UI
	// tick, and every texture edit or animated-texture step queued since the
	// last one. Without them a HUD's new text draws as black boxes — see the
	// two-pass dance in cmd_render.go, which is this same problem.
	gfx.AdvanceAnimatedTextures(s.T)
	uidraw.SyncFontAtlases()
	gfx.DrainTextureUploads()

	render.DrawWorld(s.Level, render.WorldDraw{Now: s.T})

	// The game's own screens over it, bottom to top, exactly as app.Run draws
	// them. Twice, for the glyphs: the first pass is what discovers which
	// characters this HUD needs, and a character seen for the first time draws
	// as a box until its page has been baked and uploaded.
	var list uidraw.List
	s.drawScreens(&list)
	uidraw.SyncFontAtlases()
	gfx.DrainTextureUploads()
	s.drawScreens(&list)

	img := s.target.Image()
	s.target.Unbind()
	if err := glctx.SavePNG(path, img); err != nil {
		return "", err
	}
	s.shots++
	return path, nil
}

func (s *Session) drawScreens(list *uidraw.List) {
	for _, sc := range ui.Game.Screens() {
		uidraw.RenderScreen(list, sc, ui.Game.Interaction(), int32(s.opts.ShotW), int32(s.opts.ShotH))
	}
}

// shotPath is where a picture goes: the name as given if it is a path, and a
// numbered one in the session's shot directory otherwise. Numbered rather than
// stamped with the time, so a run's pictures sort in the order they were taken.
func (s *Session) shotPath(name string) string {
	if name != "" && (filepath.IsAbs(name) || strings.ContainsRune(name, filepath.Separator)) {
		return name
	}
	if name == "" {
		name = fmt.Sprintf("shot-%03d.png", s.shots+1)
	}
	if !strings.HasSuffix(name, ".png") {
		name += ".png"
	}
	dir := s.opts.ShotDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "foundry-play")
	}
	return filepath.Join(dir, name)
}

// closeDrawing releases what standUpDrawing made. The context itself stays —
// glctx keeps one per process on purpose — but the target is this session's.
func (s *Session) closeDrawing() {
	if s.target != nil {
		s.target.Delete()
		s.target = nil
		glctx.Release()
	}
}
