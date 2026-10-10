package harness

// harness_test.go — the setup a headless run needs, checked on a real level.
//
// Every one of these used to be a lesson learned per scratch harness: the
// uploads have to stay off for the whole run rather than just the load, the
// player has to exist for containment to be decided the engine's way, and the
// tick has to be the frame's or a body never falls. They are assertions now.

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/levelio"
	"foundry/internal/leveltest"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// scratchProject writes a one-room level into a throwaway project and answers
// its directory. It goes through levelio.SaveLevel rather than a hand-written
// yaml so the fixture cannot drift from the format the engine reads.
func scratchProject(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	res := filepath.Join(dir, "resources")
	for _, sub := range []string{"levels", "textures", "scripts", "ui", "models", "entities"} {
		if err := os.MkdirAll(filepath.Join(res, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = res
	t.Cleanup(func() { fileutil.ResourcesPath = prev })

	// The room's faces name a texture, and the loader refuses a level naming one
	// it cannot find — so the fixture ships it. A 2x2 image is a texture as far
	// as the walk that finds it is concerned.
	writeTexture(t, filepath.Join(res, "textures", "concrete.png"))

	ch := leveltest.RoomChunk(t)
	lvl := &world.Level{Chunks: []world.Chunk{*ch}}
	// Something to see by. A room with no light in it is a black picture,
	// which a test that looks at what was drawn cannot tell from a room that
	// was never drawn at all.
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	if err := levelio.SaveLevel(lvl, levelio.LevelPath(name)); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	return dir
}

// writeTexture writes the smallest real .png that will do: a 2x2 checker.
//
// A checker rather than a flat square because a test that looks at what was
// DRAWN (play_gl_test.go) has to be able to tell a wall from an empty picture,
// and a flat texture under flat light comes out as one colour either way.
func writeTexture(t *testing.T, path string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < 4; i++ {
		v := byte(0xff)
		if i%3 == 0 {
			v = 0x20
		}
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = v, v, v, 0xff
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestOpenLoadsALevelWithNoWindow(t *testing.T) {
	dir := scratchProject(t, "scratch")
	s, err := Open(Options{Project: dir, Level: "scratch"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(s.Level.Chunks) == 0 {
		t.Error("the level loaded with no chunks")
	}
	// The whole point of Open over a hand-rolled load: these stay off for the
	// run, because `spawn` loads a model and a model load with no GL context
	// walks into glGetUniformLocation on a nil program and takes the process
	// with it.
	if gfx.GeometryUploadsEnabled || gfx.TextureUploadsEnabled {
		t.Error("the GL uploads are on after Open; a spawn during the run would segfault")
	}
	// And a player body exists, or containment — :enter:, :exit:, :interact: —
	// is decided by nothing at all.
	if world.PlayerEntity == nil {
		t.Fatal("no player body after Open")
	}
}

// The tick is the frame, and the plainest evidence of that is gravity: a body
// let go above a floor falls, which is the one thing no scripts-only harness
// ever did.
func TestTheFrameMakesThePlayerFall(t *testing.T) {
	dir := scratchProject(t, "scratch")
	s, err := Open(Options{Project: dir, Level: "scratch"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Teleport(mgl32.Vec3{0, 6, 0})
	start := world.PlayerEntity.Position.Y()
	s.RunFor(0.5)
	if got := world.PlayerEntity.Position.Y(); got >= start {
		t.Errorf("after half a second the player is at y %v, having started at %v: nothing fell, "+
			"so the tick is not running the frame's physics", got, start)
	}
}

// ScriptsOnly is the old cheap mode, and the contract is that it does NOT move a
// body. Stated as a test because the difference is invisible until a run's
// conclusion depends on it.
func TestScriptsOnlyMovesNoBody(t *testing.T) {
	dir := scratchProject(t, "scratch")
	s, err := Open(Options{Project: dir, Level: "scratch", ScriptsOnly: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Teleport(mgl32.Vec3{0, 6, 0})
	s.RunFor(0.5)
	if got := world.PlayerEntity.Position.Y(); got != 6 {
		t.Errorf("a scripts-only run moved the player to y %v; it runs nothing that moves a body", got)
	}
}

// An ease is stepped once per tick here too — the harness's own version of
// sim's tween_step_test, which is where the double step actually lived.
func TestAnEaseRunsAtItsAuthoredRate(t *testing.T) {
	dir := scratchProject(t, "scratch")
	s, err := Open(Options{Project: dir, Level: "scratch"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Level.Entities = append(s.Level.Entities, world.Entity{
		ID: "crate", Scale: mgl32.Vec3{1, 1, 1}, Visible: true,
	})
	if err := world.RebuildIndexes(s.Level); err != nil {
		t.Fatalf("RebuildIndexes: %v", err)
	}
	e := world.FindEntity(s.Level, "crate")
	e.Ease(s.Level.Frames, world.TweenMove, mgl32.Vec3{10, 0, 0}, 1)

	s.RunFor(0.5)
	if got := e.Position.X(); got < 4.5 || got > 5.5 {
		t.Errorf("half a second into a one-second ease the crate is at x %v, want about 5", got)
	}
}

// Watches answer in the strings the CLI prints, and Read is the same path, so a
// scenario's expectation and a printed line can never disagree.
func TestReadAnswersTheWatchPaths(t *testing.T) {
	dir := scratchProject(t, "scratch")
	s, err := Open(Options{Project: dir, Level: "scratch"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Teleport(mgl32.Vec3{1, 2, 3})
	if got, want := s.Read("player.position"), "<1.00, 2.00, 3.00>"; got != want {
		t.Errorf("player.position reads %q, want %q", got, want)
	}
	if got := s.Read("nothing_here.position"); got != "?" {
		t.Errorf("an unknown id reads %q, want %q", got, "?")
	}
}
