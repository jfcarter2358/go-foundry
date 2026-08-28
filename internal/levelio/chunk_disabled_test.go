package levelio

// chunk_disabled_test.go — `disabled: true` on a chunk, through the writer and
// the loader.
//
// The load side is the interesting half. A disabled chunk is not merely hidden:
// its geometry is never built at all, which is what makes writing the flag into
// a level worth doing for one that streams its areas in. So the assertion is
// not just that the flag survives, but that nothing was uploaded for it — and
// that switching it on afterwards produces the chunk the loader would have.

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

// disabledChunkLevel is one square sector in one chunk, whose disabled state
// the caller chooses.
const disabledChunkLevelYAML = `chunks:
  - id: area
    disabled: %DISABLED%
    origin: {x: 0, y: 0, z: 0}
    rotation: 0
    scale: 1
    points:
      P0: {x: 0, y: 0}
      P1: {x: 10, y: 0}
      P2: {x: 10, y: 10}
      P3: {x: 0, y: 10}
    sectors:
      - id: room
        floor: {enabled: true, height: 0, texture: default}
        ceiling: {enabled: true, height: 4, texture: default}
        edges:
          - {from: P0, to: P1, kind: wall, texture: default, collision: true}
          - {from: P1, to: P2, kind: wall, texture: default, collision: true}
          - {from: P2, to: P3, kind: wall, texture: default, collision: true}
          - {from: P3, to: P0, kind: wall, texture: default, collision: true}
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
	src := strings.ReplaceAll(disabledChunkLevelYAML, "%DISABLED%", disabled)
	path := filepath.Join(t.TempDir(), "areas.yaml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func sectorHasMesh(s *world.Sector) bool {
	if len(s.Floor.Geometry.Vertices) > 0 || len(s.Ceiling.Geometry.Vertices) > 0 {
		return true
	}
	for wi := range s.Walls {
		if len(s.Walls[wi].Geometry.Vertices) > 0 {
			return true
		}
	}
	return false
}

func TestDisabledChunkIsNeverBuiltAtLoad(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	withStubTextures(t)

	on, err := LoadLevel(writeDisabledChunkLevel(t, "false"))
	if err != nil {
		t.Fatalf("enabled load: %v", err)
	}
	if !sectorHasMesh(&on.Chunks[0].Sectors[0]) {
		t.Fatal("the fixture builds no geometry even when enabled")
	}

	off, err := LoadLevel(writeDisabledChunkLevel(t, "true"))
	if err != nil {
		t.Fatalf("disabled load: %v", err)
	}
	ch := &off.Chunks[0]
	if ch.IsEnabled() {
		t.Fatal("`disabled: true` loaded as an enabled chunk")
	}
	if !ch.Unloaded {
		t.Error("a chunk loaded disabled is not marked unloaded, so nothing would ever build it")
	}
	if sectorHasMesh(&ch.Sectors[0]) {
		t.Error("a chunk loaded disabled uploaded its geometry anyway")
	}

	// The sectors themselves are still there to be switched on — the skipped
	// work is the upload, not the parse.
	if len(ch.Sectors) != 1 || len(ch.Points) != 4 {
		t.Fatalf("got %d sectors and %d points, want 1 and 4", len(ch.Sectors), len(ch.Points))
	}
	if !world.SetChunkEnabled(ch, true) {
		t.Fatal("switching the chunk on reported no change")
	}
	if !sectorHasMesh(&ch.Sectors[0]) {
		t.Error("switching the chunk on built no geometry")
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
