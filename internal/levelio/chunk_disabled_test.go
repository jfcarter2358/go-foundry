package levelio

// chunk_disabled_test.go — `disabled: true` on a chunk, through the writer and
// the loader.
//
// The load side is the interesting half. A disabled chunk is built at load like
// any other, so switching it on costs no stall — unless it sets
// `unload_when_disabled`, in which case nothing is uploaded for it until a
// script switches it on, and that produces the chunk the loader would have.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

// disabledChunkLevel is one box brush in one chunk, whose disabled state the
// caller chooses.
const disabledChunkLevelYAML = `chunks:
  - id: area
    disabled: %DISABLED%
    unload_when_disabled: %UNLOAD%
    origin: {x: 0, y: 0, z: 0}
    rotation: 0
    scale: 1
    brushes:
      - id: room
        verts: [0,0,0, 10,0,0, 0,4,0, 10,4,0, 0,0,10, 10,0,10, 0,4,10, 10,4,10]
        faces:
          - {indices: [0,4,6,2], texture: default}
          - {indices: [5,1,3,7], texture: default}
          - {indices: [0,1,5,4], texture: default}
          - {indices: [2,6,7,3], texture: default}
          - {indices: [1,0,2,3], texture: default}
          - {indices: [4,5,7,6], texture: default}
`

// withStubTextures registers the one texture the fixture names, so the loader's
// existence check passes without a textures/ folder.
func withStubTextures(t *testing.T) {
	t.Helper()
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	if gfx.TextureNames == nil {
		gfx.TextureNames = map[string]string{}
	}
	for _, n := range []string{"", "default"} {
		gfx.Textures[n] = 1
		gfx.TextureNames[n] = n
	}
}

func writeDisabledChunkLevel(t *testing.T, disabled string) string {
	t.Helper()
	return writeChunkLevel(t, disabled, "false")
}

func writeChunkLevel(t *testing.T, disabled, unload string) string {
	t.Helper()
	src := strings.ReplaceAll(disabledChunkLevelYAML, "%DISABLED%", disabled)
	src = strings.ReplaceAll(src, "%UNLOAD%", unload)
	path := filepath.Join(t.TempDir(), "areas.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func brushHasMesh(b *world.Brush) bool {
	for fi := range b.Faces {
		if len(b.Faces[fi].Geometry.Vertices) > 0 {
			return true
		}
	}
	return false
}

func TestDisabledChunkIsBuiltAtLoad(t *testing.T) {
	// Built behind the loading screen, so a script switching it on mid-play
	// costs no stall.
	testenv.WithoutGeometryUploads(t)
	withStubTextures(t)

	off, err := LoadLevel(writeDisabledChunkLevel(t, "true"))
	if err != nil {
		t.Fatalf("disabled load: %v", err)
	}
	ch := &off.Chunks[0]
	if ch.IsEnabled() {
		t.Fatal("`disabled: true` loaded as an enabled chunk")
	}
	if ch.Unloaded || !brushHasMesh(&ch.Brushes[0]) {
		t.Error("a chunk loaded disabled was not built")
	}
	if world.SyncChunkGeometry(&off) {
		t.Error("the first frame's reconcile reported a change the load already indexed")
	}
	gen := world.BrushMeshGeneration()
	if !world.SetChunkEnabled(ch, true) {
		t.Fatal("switching the chunk on reported no change")
	}
	if world.BrushMeshGeneration() != gen {
		t.Error("switching a built chunk on rebuilt its meshes")
	}
}

func TestUnloadWhenDisabledChunkIsNeverBuiltAtLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	withStubTextures(t)

	on, err := LoadLevel(writeChunkLevel(t, "false", "true"))
	if err != nil {
		t.Fatalf("enabled load: %v", err)
	}
	if !brushHasMesh(&on.Chunks[0].Brushes[0]) {
		t.Fatal("the fixture builds no geometry even when enabled")
	}

	off, err := LoadLevel(writeChunkLevel(t, "true", "true"))
	if err != nil {
		t.Fatalf("disabled load: %v", err)
	}
	ch := &off.Chunks[0]
	if !ch.UnloadWhenDisabled {
		t.Fatal("`unload_when_disabled: true` did not load")
	}
	if !ch.Unloaded {
		t.Error("a chunk loaded disabled with unload_when_disabled is not marked unloaded, so nothing would ever build it")
	}
	if brushHasMesh(&ch.Brushes[0]) {
		t.Error("a chunk loaded disabled with unload_when_disabled uploaded its geometry anyway")
	}

	// The brushes themselves are still there to be switched on — the skipped
	// work is the upload, not the parse.
	if len(ch.Brushes) != 1 || len(ch.Brushes[0].Verts) != 8 {
		t.Fatalf("got %d brushes and %d verts, want 1 and 8", len(ch.Brushes), len(ch.Brushes[0].Verts))
	}
	if !world.SetChunkEnabled(ch, true) {
		t.Fatal("switching the chunk on reported no change")
	}
	if !brushHasMesh(&ch.Brushes[0]) {
		t.Error("switching the chunk on built no geometry")
	}

	raw, err := LevelToRaw(&off)
	if err != nil {
		t.Fatal(err)
	}
	if !raw.Chunks[0].UnloadWhenDisabled {
		t.Error("unload_when_disabled was dropped on the way out")
	}
}

func TestChunkDisabledRoundTrips(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	withStubTextures(t)

	lvl, err := LoadLevel(writeDisabledChunkLevel(t, "true"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("to raw: %v", err)
	}
	if !raw.Chunks[0].Disabled {
		t.Error("the flag was dropped on the way out")
	}

	// An enabled chunk writes no key, so adding this changed no existing level.
	lvl.Chunks[0].Disabled = false
	raw, err = LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("to raw: %v", err)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "disabled:") {
		t.Error("an enabled chunk wrote a `disabled:` key")
	}
}
