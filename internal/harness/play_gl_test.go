package harness

// play_gl_test.go — a play session with its eyes open, on a real GPU.
//
//	FOUNDRY_GL_TEST=1 go test -run TestAPlaySession ./internal/harness/
//
// The thing being proved is the whole of shot.go: that a session opened with
// Draw stands the world up, loads the level onto the GPU, and comes back with
// the room in the picture. Every part of that has failed silently at least
// once — an empty picture is what a level drawn into the wrong framebuffer, a
// mesh never uploaded and a camera never placed all look like — so the
// assertion is deliberately the crudest possible one: the room is not one
// flat colour.

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/render"
	"foundry/internal/testenv"

	"github.com/go-gl/mathgl/mgl32"
)

func TestAPlaySessionDrawsTheRoomItIsStandingIn(t *testing.T) {
	gltest.Context(t)
	dir := scratchProject(t, "shotroom")
	// The shaders belong to the binary rather than to the project (see
	// fileutil.EngineShadersDir), and a scratch project has none — so the
	// session reads the repository's, exactly as an installed editor reads
	// the ones beside itself.
	prev := fileutil.EngineShadersDir
	fileutil.EngineShadersDir = filepath.Join(testenv.RepoRoot(t), "resources", "shaders")
	t.Cleanup(func() { fileutil.EngineShadersDir = prev })

	s, err := Open(Options{
		Project: dir, Level: "shotroom",
		Draw: true, ShotW: 320, ShotH: 240, ShotDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// The middle of the room, which leveltest builds spanning 0..10 — dropped
	// at the origin the body would be standing inside the far wall.
	s.Teleport(mgl32.Vec3{5, 1, 5})
	s.RunFor(0.5)

	path, err := s.Shot("room.png")
	if err != nil {
		t.Fatalf("Shot: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	r, g, b, a := img.At(160, 120).RGBA()
	t.Logf("centre pixel %d,%d,%d,%d; drew chunks=%d brushes=%d calls=%d; cam %v dir %v",
		r>>8, g>>8, b>>8, a>>8, render.DebugStats.ChunksDrawn, render.DebugStats.BrushesDrawn,
		render.DebugStats.BrushDrawCalls, gfx.Cam.Position, gfx.Cam.Direction)
	if flat(img) {
		t.Errorf("the picture is one flat colour: the session drew no world at all (%s)", path)
	}
}

// flat reports whether every pixel of an image is the same colour, which is
// what "nothing was drawn" looks like from the outside.
func flat(img image.Image) bool {
	b := img.Bounds()
	r0, g0, b0, a0 := img.At(b.Min.X, b.Min.Y).RGBA()
	for y := b.Min.Y; y < b.Max.Y; y += 3 {
		for x := b.Min.X; x < b.Max.X; x += 3 {
			r, g, bb, a := img.At(x, y).RGBA()
			if r != r0 || g != g0 || bb != b0 || a != a0 {
				return false
			}
		}
	}
	return true
}
