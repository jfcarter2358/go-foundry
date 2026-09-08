package levelio

// compiled_level_test.go — the artefact survives the round trip, and carries
// the right half of the level.
//
// The interesting assertions are not about gob, which either works or does not:
// they are about the PARTITION of a level into what got baked and what stayed
// authored. Get that wrong in one direction and a vehicle's interior vanishes;
// get it wrong in the other and a chunk is drawn twice, once compiled and once
// live.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/compile"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// sealedLevel is a shell with somewhere to stand in it, plus a moving chunk and
// a switched-off one so the partition has something to get wrong.
func sealedLevel() *world.Level {
	room := world.NewHollowRoom("room", mgl32.Vec3{-4, 0, -4}, mgl32.Vec3{4, 4, 4}, 0.5, "concrete")
	return &world.Level{
		Chunks: []world.Chunk{
			{ID: "static", Scale: 1, Brushes: room},
			{ID: "ship", Scale: 1, FrameOwner: "dropship",
				Brushes: world.NewHollowRoom("hold", mgl32.Vec3{40, 0, 0}, mgl32.Vec3{44, 4, 4}, 0.5, "metal")},
			{ID: "later", Scale: 1, Disabled: true,
				Brushes: []world.Brush{world.NewBoxBrush("block", mgl32.Vec3{80, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete")}},
		},
		Spawn:  &world.SpawnPoint{Position: mgl32.Vec3{0, 1, 0}},
		Lights: world.LightSet{Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.3}},
	}
}

func compileFixture(t *testing.T) *CompiledLevel {
	t.Helper()
	c, err := CompileLevel(sealedLevel(), compile.DefaultOptions())
	if err != nil {
		t.Fatalf("CompileLevel: %v", err)
	}
	return c
}

func TestACompiledLevelSurvivesTheRoundTrip(t *testing.T) {
	c := compileFixture(t)
	path := filepath.Join(t.TempDir(), "test"+CompiledExt)
	if err := SaveCompiledLevel(c, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := LoadCompiledLevel(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(back.Faces) != len(c.Faces) || len(back.Leaves) != len(c.Leaves) {
		t.Fatalf("%d/%d faces and %d/%d cells came back", len(back.Faces), len(c.Faces), len(back.Leaves), len(c.Leaves))
	}
	// The tree still answers: the middle of the room is open, and the far side
	// of a wall is not.
	tr := back.Tree()
	if tr.Leaves[tr.FindLeaf(mgl32.Vec3{0, 2, 0})].Solid {
		t.Error("the middle of the room came back solid after a round trip")
	}
	if !tr.Leaves[tr.FindLeaf(mgl32.Vec3{4.25, 2, 0})].Solid {
		t.Error("the inside of a wall came back open after a round trip")
	}
	// And so does the visibility.
	inside := tr.FindLeaf(mgl32.Vec3{0, 2, 0})
	if !back.PVS.Visible(inside, inside) {
		t.Error("a cell cannot see itself after a round trip")
	}
	// The baked light came with it.
	for fi := range back.Faces {
		if len(back.Faces[fi].Light) != len(back.Faces[fi].Points) {
			t.Fatalf("face %d lost its baked light in the round trip", fi)
		}
	}
}

// The partition: a chunk the compile baked is gone from the authored half, and
// a chunk it could not bake is still there.
func TestOnlyTheChunksTheCompileCouldNotBakeStayAuthored(t *testing.T) {
	c := compileFixture(t)
	got := map[string]bool{}
	for _, rc := range c.Raw.Chunks {
		got[rc.ID] = true
	}
	if got["static"] {
		t.Error("a chunk that was compiled is still in the authored half; it would be drawn twice")
	}
	if !got["ship"] {
		t.Error("a frame-owned chunk was dropped; a vehicle's interior cannot be baked into world space")
	}
	if !got["later"] {
		t.Error("a disabled chunk was dropped; nothing would be left for a script to switch on")
	}
	// Everything else the level is made of comes through untouched.
	if c.Raw.Spawn == nil {
		t.Error("the spawn did not survive the compile")
	}
	if c.Raw.Lights.Global.Brightness == 0 {
		t.Error("the lights did not survive the compile")
	}
}

// A file from a different version of the compiler is rejected by name rather
// than misread: gob would happily decode most changes into the current structs
// and mean something else.
func TestAStaleCompiledLevelIsRejected(t *testing.T) {
	c := compileFixture(t)
	c.Version = CompiledVersion + 1
	path := filepath.Join(t.TempDir(), "stale"+CompiledExt)
	if err := SaveCompiledLevel(c, path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCompiledLevel(path); err == nil {
		t.Error("a compiled level from another version loaded without complaint")
	}
}

func TestSomethingElseEntirelyIsNotACompiledLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notreally"+CompiledExt)
	if err := os.WriteFile(path, []byte("chunks:\n  - id: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCompiledLevel(path); err == nil {
		t.Error("a YAML level loaded as a compiled one")
	}
}

// The compiled file sits beside the level it came from, under the same name.
func TestTheCompiledFileSitsBesideItsLevel(t *testing.T) {
	if got, want := CompiledPath("/levels/canyon"+LevelExt), "/levels/canyon"+CompiledExt; got != want {
		t.Errorf("CompiledPath = %q, want %q", got, want)
	}
	if got, want := CompiledPath("/levels/canyon"), "/levels/canyon"+CompiledExt; got != want {
		t.Errorf("CompiledPath without an extension = %q, want %q", got, want)
	}
}
