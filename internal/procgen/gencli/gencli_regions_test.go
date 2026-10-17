package gencli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

// gen world makes the level doc with the level's regions in it (once), gen
// env writes presets only when absent, and gen run syncs a moved region into
// the doc before it runs.
func TestGenWorldEnvAndRunSync(t *testing.T) {
	root := t.TempDir()
	prev := fileutil.ResourcesPath
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	res := filepath.Join(root, "resources")
	os.MkdirAll(filepath.Join(res, "levels"), 0o755)
	writeLevel := func(x float32) {
		lvl := world.RawLevel{Volumes: []world.RawVolume{{ID: "west", Position: world.RawVec3{X: x},
			Half: world.RawVec3{X: 100, Y: 10, Z: 100},
			Region: &world.RawVolumeRegion{Environment: "desert_canyon",
				Footprint: [][2]float32{{-100, -100}, {100, -100}, {100, 100}, {-100, 100}}}}}}
		data, _ := yaml.Marshal(lvl)
		os.WriteFile(filepath.Join(res, "levels", "arena.yaml"), data, 0o644)
	}
	writeLevel(-100)

	var out bytes.Buffer
	if code := Main(root, []string{"world", "arena"}, &out); code != 0 {
		t.Fatalf("world: %d\n%s", code, out.String())
	}
	docPath := filepath.Join(res, "generators", "arena_world.fgen")
	d, err := procgen.ReadDoc(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if d.Level != "arena" || len(d.Stages[0].Params.Items("regions")) != 1 {
		t.Fatalf("world doc %+v", d)
	}
	for _, env := range []string{"desert_canyon", "savannah"} {
		if _, err := os.Stat(filepath.Join(res, "environments", env+".fgen")); err != nil {
			t.Errorf("gen world did not write the %s preset: %v", env, err)
		}
	}
	out.Reset()
	if code := Main(root, []string{"world", "arena"}, &out); code != 0 || !strings.Contains(out.String(), "exists") {
		t.Errorf("second world: %d %s", code, out.String())
	}

	// env write leaves an edited preset alone; env list shows it.
	sav := filepath.Join(res, "environments", "savannah.fgen")
	os.WriteFile(sav, []byte("doc: mine\nseed: 5\nstages: []\n"), 0o644)
	out.Reset()
	if code := Main(root, []string{"env", "write", "-all"}, &out); code != 0 || !strings.Contains(out.String(), "never overwritten") {
		t.Errorf("env write: %d %s", code, out.String())
	}
	if got, _ := os.ReadFile(sav); !strings.Contains(string(got), "doc: mine") {
		t.Error("env write overwrote an edited preset")
	}
	out.Reset()
	if code := Main(root, []string{"env", "list"}, &out); code != 0 || !strings.Contains(out.String(), "savannah") ||
		!strings.Contains(out.String(), "edited copy") {
		t.Errorf("env list: %d %s", code, out.String())
	}
	if code := Main(root, []string{"env", "write", "nope"}, &out); code == 0 {
		t.Error("writing an unknown preset succeeded")
	}

	// Move the region in the level; run syncs it into the doc on disk.
	writeLevel(-150)
	out.Reset()
	// -stage ground: the template's region_material would download the
	// presets' Poly Haven textures, which a test must not.
	if code := Main(root, []string{"run", "arena_world", "-q", "-no-splice", "-stage", "ground"}, &out); code != 0 {
		t.Fatalf("run: %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "synced 1 region(s)") {
		t.Errorf("run did not say it synced:\n%s", out.String())
	}
	d, _ = procgen.ReadDoc(docPath)
	if p := d.Stages[0].Params.Items("regions")[0].Points("footprint")[0]; p[0] != -250 {
		t.Errorf("the doc on disk has footprint %v, want the moved region's", p)
	}
}

// `gen env list` and `gen list` show every built-in environment with its
// overlay colour: the two from the regions phases and the nine of
// docs/regions/PRESETS.md step 2.
func TestEnvListShowsEveryBuiltInWithItsColour(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"desert_canyon", "savannah", "pine_forest", "coast", "alpine", "dunes", "volcanic", "tundra",
		"wetland", "tallgrass_prairie", "oak_savannah"}
	for _, verb := range [][]string{{"env", "list", "-root", root}, {"list"}} {
		var out bytes.Buffer
		if code := Main(root, verb, &out); code != 0 {
			t.Fatalf("%v: %d %s", verb, code, out.String())
		}
		lines := strings.Split(out.String(), "\n")
		for _, n := range names {
			found := false
			for _, l := range lines {
				f := strings.Fields(l)
				if len(f) > 1 && f[0] == n && strings.Contains(l, "#") {
					found = true
				}
			}
			if !found {
				t.Errorf("%v does not list %s with a colour:\n%s", verb, n, out.String())
			}
		}
	}
}
