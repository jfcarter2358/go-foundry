package harness

// frame_state_gl_test.go — the GL state DrawWorld's passes assume, checked
// against GL's own answer on a real frame.
//
//	FOUNDRY_GL_TEST=1 go test -run TestTheFrameStateTheWorldPassesAssume ./internal/harness/
//
// Inside a frame with a declared target the passes no longer ask GL what is
// bound or enabled — asking stalls the CPU on the GPU (render/frame_target.go)
// — and trust a convention instead. A pass that broke the convention would
// leave the next one restoring the wrong state, which looks like anything from
// a missing shadow to an invisible world. So a frame that runs every pass that
// trusts it (the sun's cascades, the clouds' shadow, GI, the sky, the
// volumetric march, and the HUD after them) is drawn with CheckFrameState and
// gfx.CheckKnownGLState on, which make each of them ask as well and say where
// the answers differ.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/levelio"
	"foundry/internal/render"
	"foundry/internal/testenv"
	"foundry/internal/ui"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestTheFrameStateTheWorldPassesAssume(t *testing.T) {
	gltest.Context(t)
	dir := scratchProject(t, "framestate")
	prev := fileutil.EngineShadersDir
	fileutil.EngineShadersDir = filepath.Join(testenv.RepoRoot(t), "resources", "shaders")
	t.Cleanup(func() { fileutil.EngineShadersDir = prev })
	prevGI := gfx.GIQuality
	t.Cleanup(func() { gfx.GIQuality = prevGI })

	// A sun that casts, a sky with a deck of cloud over it and a HUD on top:
	// every pass that trusts the convention has something to do.
	hud, err := os.ReadFile(filepath.Join(testenv.RepoRoot(t), "resources", "ui", "hud.fui"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resources", "ui", "hud.fui"), hud, 0o644); err != nil {
		t.Fatal(err)
	}
	path := levelio.LevelPath("framestate")
	lvl, err := levelio.LoadLevelHeadless(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	sun := world.DirectionalLight{ID: "sun", Yaw: 35, Pitch: -52, Color: mgl32.Vec3{1, 1, 1},
		Brightness: 1, CastShadows: true, ShadowStrength: 1, Dynamic: true}
	sun.SyncDirection()
	lvl.Lights.Directional = []world.DirectionalLight{sun}
	clouds := world.DefaultSkyClouds()
	clouds.Altitude = 20
	lvl.Sky = &world.Sky{Kind: world.SkyGradient, Zenith: mgl32.Vec3{0.2, 0.4, 0.8},
		Horizon: mgl32.Vec3{0.7, 0.8, 0.9}, Ground: mgl32.Vec3{0.3, 0.3, 0.3},
		Tint: mgl32.Vec3{1, 1, 1}, Intensity: 1, Clouds: &clouds}
	lvl.UI = []string{"hud"}
	if err := levelio.SaveLevel(&lvl, path); err != nil {
		t.Fatalf("save: %v", err)
	}

	s, err := Open(Options{
		Project: dir, Level: "framestate",
		Draw: true, ShotW: 320, ShotH: 240, ShotDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	// After Open: standing the world up picks the tier for the GPU it finds,
	// and a software rasteriser gets none.
	gfx.GIQuality = gfx.GILow
	s.Teleport(mgl32.Vec3{5, 1, 5})
	s.RunFor(0.2)

	render.CheckFrameState, gfx.CheckKnownGLState = true, true
	defer func() { render.CheckFrameState, gfx.CheckKnownGLState = false, false }()
	render.FrameStateMismatches()
	gfx.KnownGLStateMismatches()
	// Two frames: the first builds what the passes create on first use, and
	// a pass that only goes wrong against the state the last frame left is
	// caught by the second.
	for i := 0; i < 2; i++ {
		if _, err := s.Shot("frame.png"); err != nil {
			t.Fatalf("Shot: %v", err)
		}
	}
	for _, m := range render.FrameStateMismatches() {
		t.Error(m)
	}
	for _, m := range gfx.KnownGLStateMismatches() {
		t.Error("after the world: " + m)
	}
	if len(ui.Game.Screens()) == 0 {
		t.Error("no HUD came up, so the screen-space pass went unchecked")
	}
	if !render.GIActive() {
		t.Log("GI did not run this frame (no compute on this driver?) — its pass went unchecked")
	}
	if render.DebugStats.VolumeDraws == 0 {
		t.Error("the cloud deck was not marched, so the volumetric pass went unchecked")
	}
}
