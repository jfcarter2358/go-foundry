package harness

// cmd_check_decal_test.go — `foundry check`'s decal report, and the reason it
// needs a guard of its own.
//
// checkDecals is the ONLY place outside the frame loop that builds a decal's
// mesh, and a decal's mesh is real GPU geometry: Geometry.Load looks up the world
// program's model uniform before it uploads, which on a headless run is a call
// into a driver that was never initialised — a segfault, not an error. The check
// switches uploads off around the build for exactly that reason, and this test is
// what stops the switch being dropped, since every other test in the package runs
// with uploads already off for its own reasons.

import (
	"bytes"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// decalCheckLevel is a floor slab with two decals: one laid on it and one left
// in mid-air.
func decalCheckLevel(t *testing.T) *world.Level {
	t.Helper()
	prev := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = false
	b := world.NewBoxBrush("floor", mgl32.Vec3{0, -0.25, 0}, mgl32.Vec3{4, 0.25, 4}, "concrete")
	if err := b.Load(); err != nil {
		gfx.GeometryUploadsEnabled = prev
		t.Fatalf("brush load: %v", err)
	}
	gfx.GeometryUploadsEnabled = prev

	ch := world.Chunk{ID: "room", Brushes: []world.Brush{b}}
	ch.RecomputeBounds()

	onFloor := world.NewDecal("blast_mark", mgl32.Vec3{0, 0.01, 0}, mgl32.Vec3{0, 1, 0})
	onFloor.Width, onFloor.Height = 2, 2
	floating := world.NewDecal("floating", mgl32.Vec3{0, 40, 0}, mgl32.Vec3{0, 1, 0})

	return &world.Level{
		Chunks: []world.Chunk{ch},
		Decals: []world.Decal{onFloor, floating},
	}
}

func TestCheckDecalsReportsOnlyTheOnesThatProjectOntoNothing(t *testing.T) {
	lvl := decalCheckLevel(t)

	var out bytes.Buffer
	bad := checkDecals(&out, "decaltest", lvl)

	if bad != 1 {
		t.Fatalf("problems: got %d, want 1\n%s", bad, out.String())
	}
	if got := out.String(); !bytes.Contains([]byte(got), []byte("floating")) {
		t.Errorf("the report does not name the floating decal:\n%s", got)
	}
	if got := out.String(); bytes.Contains([]byte(got), []byte("blast_mark")) {
		t.Errorf("the decal lying on the floor was reported as projecting onto nothing:\n%s", got)
	}
}

// The regression: the check must not need a GL context. This asserts the state
// it runs under rather than the crash, because the crash is a segfault in cgo
// that no test can recover from — so what is pinned is that uploads were off
// while the mesh was built, and that they were put back.
func TestCheckDecalsBuildsWithoutAGLContext(t *testing.T) {
	prev := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = true
	t.Cleanup(func() { gfx.GeometryUploadsEnabled = prev })

	lvl := decalCheckLevel(t)
	var out bytes.Buffer
	checkDecals(&out, "decaltest", lvl)

	if !gfx.GeometryUploadsEnabled {
		t.Error("checkDecals left geometry uploads switched off for whatever ran next")
	}
	// The mesh was built, and its buffers were never asked for.
	d := world.FindDecal(lvl, "blast_mark")
	if d == nil || len(d.Pieces) == 0 {
		t.Fatal("the decal on the floor built no mesh")
	}
	for i := range d.Pieces {
		if d.Pieces[i].Geometry.Vao != 0 {
			t.Errorf("piece %d has a VAO, so the build went to the GPU on a headless run", i)
		}
		if len(d.Pieces[i].Geometry.Vertices) == 0 {
			t.Errorf("piece %d has no CPU vertices, so nothing was built at all", i)
		}
	}
}
