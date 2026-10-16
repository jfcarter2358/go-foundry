package gencli

// gencli_paint_test.go — `foundry gen paint` against a scratch project: a
// stroke declares the layer and writes its tiles, a run reads them, and the
// next stroke makes only the stage reading the layer stale.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
)

const paintTestDoc = `seed: 5
stages:
- stage: terrain
  id: ground
  params:
    size: 64
    resolution: 1
    tile_size: 32
    drainage_iterations: 4
    talus_iterations: 4
- stage: rocks
  id: boulders
  params:
    variants: 1
    triangles: 300
- stage: scatter
  id: rubble
  params:
    terrain: ground
    rules:
    - name: rubble
      rocks: boulders
      density: 200
      slope: [0, 90]
      cliff: [0, 1]
      paint: lush
`

func TestGenPaintStrokesAndRerunsOnlyItsReaders(t *testing.T) {
	root := t.TempDir()
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	gens := filepath.Join(root, "resources", "generators")
	os.MkdirAll(gens, 0o755)
	docPath := filepath.Join(gens, "pt.fgen")
	if err := os.WriteFile(docPath, []byte(paintTestDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// The layer is not declared yet: a run refuses.
	if code := Main(root, []string{"check", "pt"}, &out); code == 0 {
		t.Errorf("check passed with an undeclared layer:\n%s", out.String())
	}
	out.Reset()
	if code := Main(root, []string{"paint", "pt", "-layer", "lush", "-disc", "5,5,8"}, &out); code != 0 {
		t.Fatalf("paint: %d\n%s", code, out.String())
	}
	d, err := procgen.ReadDoc(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if def := d.PaintDef("lush"); def == nil || def.Kind != "density" {
		t.Fatalf("layer not declared: %+v", d.Paint)
	}
	if v := d.PaintLayer("lush").Sample(5.25, 5.25); v != 1 {
		t.Errorf("painted %.3f at the disc's centre", v)
	}
	if _, err := os.Stat(filepath.Join(gens, "pt.paint", "lush", "0_0.png")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Main(root, []string{"run", "pt", "-q", "-workers", "4"}, &out); code != 0 {
		t.Fatalf("run: %d\n%s", code, out.String())
	}
	out.Reset()
	if code := Main(root, []string{"paint", "pt", "-layer", "lush", "-line", "-20,-20,-10,-20,3"}, &out); code != 0 {
		t.Fatalf("paint: %d\n%s", code, out.String())
	}
	out.Reset()
	Main(root, []string{"check", "pt"}, &out)
	if !strings.Contains(out.String(), "a run would run rubble\n") {
		t.Errorf("check after a stroke:\n%s", out.String())
	}
	out.Reset()
	if code := Main(root, []string{"paint", "pt", "-list"}, &out); code != 0 || !strings.Contains(out.String(), "lush  density") {
		t.Errorf("list: %s", out.String())
	}
}
