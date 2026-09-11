package levelio

// compiled_level_test.go — the artefact survives the round trip, and carries
// the right half of the level.
//
// Most of the assertions are about the PARTITION of a level into what got baked
// and what stayed authored. Get that wrong in one direction and a vehicle's
// interior vanishes; get it wrong in the other and a chunk is drawn twice, once
// compiled and once live.
//
// The rest are about the encoding, which does not simply work or not: gob loses
// a zero behind a pointer, and the authored half is made of those.

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"foundry/internal/compile"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// sealedLevel is a shell with somewhere to stand in it, plus a chunk that is
// switched off — which the compile must leave authored rather than baking, or
// there is nothing left for a script to switch on.
func sealedLevel() *world.Level {
	room := world.NewHollowRoom("room", mgl32.Vec3{-4, 0, -4}, mgl32.Vec3{4, 4, 4}, 0.5, "concrete")
	return &world.Level{
		Chunks: []world.Chunk{
			{ID: "static", Scale: 1, Brushes: room},
			{ID: "later", Scale: 1, Disabled: true,
				Brushes: []world.Brush{world.NewBoxBrush("block", mgl32.Vec3{80, 0, 0}, mgl32.Vec3{1, 1, 1}, "concrete")}},
		},
		Spawn:  &world.SpawnPoint{Position: mgl32.Vec3{0, 1, 0}},
		Lights: world.LightSet{Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.3}},
	}
}

// sealedLevelWithShip adds a frame-owned chunk, whose geometry moves and so
// cannot be baked into a world-space list.
//
// Only the in-memory tests use it: a frame owner has to name an entity that
// exists, and standing up a dropship on disk would make these tests about
// entity definitions rather than about the compile.
func sealedLevelWithShip() *world.Level {
	lvl := sealedLevel()
	lvl.Chunks = append(lvl.Chunks, world.Chunk{
		ID: "ship", Scale: 1, FrameOwner: "dropship",
		Brushes: world.NewHollowRoom("hold", mgl32.Vec3{40, 0, 0}, mgl32.Vec3{44, 4, 4}, 0.5, "hull_plate"),
	})
	return lvl
}

func compileFixture(t *testing.T) *CompiledLevel {
	t.Helper()
	c, err := CompileLevel(sealedLevelWithShip(), compile.DefaultOptions())
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
	back, err := ReadCompiledLevel(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(back.Faces) != len(c.Faces) || len(back.Tree.Leaves) != len(c.Tree.Leaves) {
		t.Fatalf("%d/%d faces and %d/%d cells came back",
			len(back.Faces), len(c.Faces), len(back.Tree.Leaves), len(c.Tree.Leaves))
	}
	if len(back.Chunks) != len(c.Chunks) {
		t.Fatalf("%d of %d chunks came back", len(back.Chunks), len(c.Chunks))
	}
	// The tree still answers: the middle of the room is open, and the far side
	// of a wall is not.
	tr := back.Tree
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
	if len(c.Raw.Chunks) != 0 {
		t.Errorf("%d chunks came through in the authored half; a compiled level stores them as runtime values",
			len(c.Raw.Chunks))
	}
	got := map[string]bool{}
	for i := range c.Chunks {
		got[c.Chunks[i].ID] = c.Chunks[i].Compiled
	}
	if _, ok := got["static"]; !ok {
		t.Error("the compiled chunk was dropped; collision would have nothing to resolve against")
	} else if !got["static"] {
		t.Error("the compiled chunk is not marked Compiled; it would be drawn twice")
	}
	if _, ok := got["ship"]; !ok {
		t.Error("a frame-owned chunk was dropped; a vehicle's interior cannot be baked into world space")
	} else if got["ship"] {
		t.Error("a frame-owned chunk is marked as compiled; its geometry moves and was never baked")
	}
	if _, ok := got["later"]; !ok {
		t.Error("a disabled chunk was dropped; nothing would be left for a script to switch on")
	} else if got["later"] {
		t.Error("a disabled chunk is marked as compiled; it was not in the world to bake")
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
	if _, err := ReadCompiledLevel(path); err == nil {
		t.Error("a compiled level from another version loaded without complaint")
	}
}

// Zero is a value, not an absence. The authored half keeps the two apart with
// pointers — a sprite's `alpha: 0` against no alpha at all, which means 1 — and
// gob, which sends neither a zero nor a pointer to one, handed every one of
// them back as absent: an intro logo authored invisible, to fade in, started
// the shipped game fully opaque.
func TestZeroValuedAuthoredFieldsSurviveTheRoundTrip(t *testing.T) {
	lvl := sealedLevel()
	lvl.Sprites = []world.Sprite{{
		ID: "logo", Position: mgl32.Vec3{0, 2, 0}, Width: 1, Height: 1,
		Alpha: 0, Visible: false,
	}}
	c, err := CompileLevel(lvl, compile.DefaultOptions())
	if err != nil {
		t.Fatalf("CompileLevel: %v", err)
	}
	path := filepath.Join(t.TempDir(), "zeroes"+CompiledExt)
	if err := SaveCompiledLevel(c, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := ReadCompiledLevel(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(back.Raw.Sprites) != 1 {
		t.Fatalf("%d sprites came back, want 1", len(back.Raw.Sprites))
	}
	s, err := world.SpriteFromRaw(back.Raw.Sprites[0])
	if err != nil {
		t.Fatal(err)
	}
	if s.Alpha != 0 {
		t.Errorf("a sprite authored at alpha 0 came back at alpha %v", s.Alpha)
	}
	if s.Visible {
		t.Error("a sprite authored hidden came back visible")
	}

	// And the authored half as a whole is what was written, not just the two
	// fields checked above.
	want, err := yaml.Marshal(c.Raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := yaml.Marshal(back.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the authored half changed in the round trip:\nwrote:\n%s\nread:\n%s", want, got)
	}
}

// What gob still encodes must hold none of the fields it cannot carry. A
// struct field that points at anything but a struct loses its zero value and
// decodes as nil; the authored half moved to YAML for exactly that, and this
// keeps a runtime struct from growing one and bringing the bug back through
// the other half.
func TestTheGobEncodedHalfHoldsNoPointersToScalars(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(tp reflect.Type, path string)
	walk = func(tp reflect.Type, path string) {
		switch tp.Kind() {
		case reflect.Ptr, reflect.Slice, reflect.Array, reflect.Map:
			walk(tp.Elem(), path)
			return
		case reflect.Struct:
		default:
			return
		}
		if seen[tp] {
			return
		}
		seen[tp] = true
		for i := 0; i < tp.NumField(); i++ {
			f := tp.Field(i)
			if !f.IsExported() {
				continue // gob does not encode it
			}
			if f.Type.Kind() == reflect.Ptr && f.Type.Elem().Kind() != reflect.Struct {
				t.Errorf("%s.%s is a %s; gob decodes a pointer to its zero value as nil", path, f.Name, f.Type)
			}
			walk(f.Type, tp.Name()+"."+f.Name)
		}
	}
	walk(reflect.TypeOf(compiledFile{}), "compiledFile")
}

func TestSomethingElseEntirelyIsNotACompiledLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notreally"+CompiledExt)
	if err := os.WriteFile(path, []byte("chunks:\n  - id: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCompiledLevel(path); err == nil {
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

// The command-line compiler's path, end to end: a level file on disk becomes a
// compiled level, with no GL context anywhere.
//
// Through the real loader rather than a second reading of the file, which is
// the point — a level that compiles has to be the same level that loads.
func TestALevelFileCompilesWithNoWindow(t *testing.T) {
	withResourcesRoot(t)

	path := filepath.Join(t.TempDir(), "headless"+LevelExt)
	if err := SaveLevel(sealedLevel(), path); err != nil {
		t.Fatalf("save: %v", err)
	}

	c, err := CompileLevelFile(path, compile.DefaultOptions())
	if err != nil {
		t.Fatalf("CompileLevelFile: %v", err)
	}
	if len(c.Faces) == 0 {
		t.Fatal("a level with a room in it compiled to no faces")
	}
	if c.Stats.InteriorLeaves == 0 {
		t.Error("a sealed room compiled to no interior cells; the void fill swallowed the level")
	}
	if len(c.Stats.Leaks) != 0 {
		t.Errorf("a sealed level reported leaks: %v", c.Stats.Leaks)
	}
	// The uploads went back on afterwards, or every level loaded after this one
	// in the same process would be invisible.
	if !gfx.GeometryUploadsEnabled || !gfx.TextureUploadsEnabled {
		t.Error("the headless load left the upload switches off")
	}
}

// A level with a hole in its shell compiles to something correct and useless,
// and says so rather than failing.
func TestALeakingLevelStillCompilesAndReportsIt(t *testing.T) {
	withResourcesRoot(t)

	lvl := sealedLevel()
	// Take the ceiling away, which is the whole roof of the only room.
	ch := &lvl.Chunks[0]
	ch.Brushes = append(ch.Brushes[:1], ch.Brushes[2:]...)
	// With the seal box switched off, so the level has to seal itself — and
	// with no roof, cannot.
	lvl.Seal = &world.Seal{Half: mgl32.Vec3{10, 10, 10}, Disabled: true}

	path := filepath.Join(t.TempDir(), "leaky"+LevelExt)
	if err := SaveLevel(lvl, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	c, err := CompileLevelFile(path, compile.DefaultOptions())
	if err != nil {
		t.Fatalf("a leaking level failed to compile rather than reporting the leak: %v", err)
	}
	if len(c.Stats.Leaks) == 0 {
		t.Error("a room with no roof reported no leaks")
	}
}
