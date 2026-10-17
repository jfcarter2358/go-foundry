package gencli

// gencli_adjacency_test.go — phase 5's GATE through the command line: a
// project's adjacency.yaml forbids desert_canyon next to savannah (with a
// stub `steppe` preset as the intermediate); `gen check` warns about the
// level's two regions; one InsertIntermediate on the level file adds the
// steppe between them; after the next sync `gen check` is clean. And `gen
// env adjacency` prints the effective table.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/stages"
	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

func TestGateForbiddenPairWarnsAndOneInsertResolvesIt(t *testing.T) {
	root := t.TempDir()
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	res := filepath.Join(root, "resources")
	envDir := filepath.Join(res, "environments")
	os.MkdirAll(filepath.Join(res, "levels"), 0o755)
	os.MkdirAll(envDir, 0o755)
	os.WriteFile(filepath.Join(envDir, "adjacency.yaml"), []byte(`
pairs:
  - between: [desert_canyon, savannah]
    forbidden: true
    intermediate: steppe
    note: no grass straight off the canyon rim
`), 0o644)
	sq := [][2]float32{{-300, -300}, {300, -300}, {300, 300}, {-300, 300}}
	lvl := world.RawLevel{Volumes: []world.RawVolume{
		{ID: "canyon", Position: world.RawVec3{X: -300}, Half: world.RawVec3{X: 300, Y: 25, Z: 300},
			Region: &world.RawVolumeRegion{Environment: "desert_canyon", Transition: 150, Footprint: sq}},
		{ID: "grass", Position: world.RawVec3{X: 300}, Half: world.RawVec3{X: 300, Y: 25, Z: 300},
			Region: &world.RawVolumeRegion{Environment: "savannah", Transition: 150, Footprint: sq}},
	}}
	levelPath := filepath.Join(res, "levels", "arena.yaml")
	data, _ := yaml.Marshal(lvl)
	os.WriteFile(levelPath, data, 0o644)

	var out bytes.Buffer
	if code := Main(root, []string{"world", "arena"}, &out); code != 0 {
		t.Fatalf("world: %d\n%s", code, out.String())
	}
	// The stub intermediate preset: the savannah under another name.
	sav, err := os.ReadFile(filepath.Join(envDir, "savannah.fgen"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(envDir, "steppe.fgen"), sav, 0o644)

	out.Reset()
	if code := Main(root, []string{"env", "adjacency"}, &out); code != 0 {
		t.Fatalf("env adjacency: %d\n%s", code, out.String())
	}
	if s := out.String(); !strings.Contains(s, "forbidden (insert steppe)") || !strings.Contains(s, "adjacency.yaml") {
		t.Errorf("env adjacency does not show the override:\n%s", s)
	}

	out.Reset()
	if code := Main(root, []string{"check", "arena_world"}, &out); code != 0 {
		t.Fatalf("check: %d\n%s", code, out.String())
	}
	if s := out.String(); !strings.Contains(s, "warning") || !strings.Contains(s, "canyon (desert_canyon) and grass (savannah)") ||
		!strings.Contains(s, "Insert intermediate region adds steppe") {
		t.Fatalf("check did not warn about the forbidden pair:\n%s", s)
	}

	// One insert, on the level file.
	raw, err := genlevel.ReadRawLevel(levelPath)
	if err != nil {
		t.Fatal(err)
	}
	adj, err := stages.ProjectAdjacency()
	if err != nil {
		t.Fatal(err)
	}
	ids, err := genlevel.InsertIntermediate(&raw, adj, "canyon", "grass")
	if err != nil || len(ids) != 1 {
		t.Fatalf("insert: %v %v", ids, err)
	}
	data, _ = yaml.Marshal(raw)
	os.WriteFile(levelPath, data, 0o644)

	out.Reset()
	if code := Main(root, []string{"check", "arena_world"}, &out); code != 0 {
		t.Fatalf("check after: %d\n%s", code, out.String())
	}
	if s := out.String(); strings.Contains(s, "warning") {
		t.Fatalf("still warns after the insertion:\n%s", s)
	}
}
