package pack

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/game"
)

// The unused-asset report feeds a Delete button, so what is guarded here is
// mostly the other direction from the prune's tests: that nothing live is ever
// reported, whichever way it is named, and that the report says why a dead file
// looks alive to a grep.

func unusedTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"game.yaml":            "title: T\nstart_level: start\n",
		"ui/theme.fth":         "",
		"ui/loading.fui":       "",
		"ui/pause.fui":         "",
		"textures/default.png": "",
		"textures/README.md":   "why this directory exists",
		"levels/start.yaml":    "chunks:\n  - floor: {texture: grass}\nscripts: [counter]\n",
		"textures/grass.png":   "",
		"scripts/counter.flang": `
def counter_texture(n int) -> str
    return format("ammo_{}", n)
end
`,
	}
	for k, v := range extra {
		files[k] = v
	}
	return writeTree(t, files)
}

func unusedPaths(t *testing.T, r UnusedReport) []string {
	t.Helper()
	var out []string
	for _, a := range r.Unused {
		out = append(out, a.Path)
	}
	return out
}

func TestUnusedReportsOnlyTheDeadFiles(t *testing.T) {
	root := unusedTree(t, map[string]string{
		"textures/ammo_01.png":  "", // built by format() — alive
		"textures/palette.pal":  "", // the texture editor's — never offered
		"textures/stale.png":    "12345",
		"levels/start.yaml.bak": "", // a backup answers to start.yaml's names
		"entities/old.yaml":     "model: models/old.fmdl\n",
		"models/old.fmdl":       "texture: old_skin\n",
		"textures/old_skin.png": "",
		"levels/testmap.yaml":   "chunks:\n  - floor: {texture: testonly}\n",
		"textures/testonly.png": "",
	})
	r, err := FindUnused(UnusedOptions{Src: root, AllLevels: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"entities/old.yaml",
		"levels/start.yaml.bak",
		"models/old.fmdl",
		"textures/old_skin.png",
		"textures/stale.png",
	}
	if got := unusedPaths(t, r); !reflect.DeepEqual(got, want) {
		t.Fatalf("unused = %v, want %v", got, want)
	}
	if r.UnusedBytes != 5+int64(len("model: models/old.fmdl\n")+len("texture: old_skin\n")) {
		t.Errorf("UnusedBytes = %d", r.UnusedBytes)
	}

	// The chain is reported as a chain: the skin is only named by the model,
	// which is only named by the entity, which nothing names.
	by := map[string][]string{}
	for _, a := range r.Unused {
		by[a.Path] = a.ReferencedBy
	}
	if !reflect.DeepEqual(by["textures/old_skin.png"], []string{"models/old.fmdl"}) ||
		!reflect.DeepEqual(by["models/old.fmdl"], []string{"entities/old.yaml"}) ||
		len(by["entities/old.yaml"]) != 0 {
		t.Errorf("ReferencedBy = %v", by)
	}

	// Counting only what the start level leads to adds the test map and what
	// only it uses.
	r, err = FindUnused(UnusedOptions{Src: root})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(unusedPaths(t, r), " ")
	for _, p := range []string{"levels/testmap.yaml", "textures/testonly.png"} {
		if !strings.Contains(got, p) {
			t.Errorf("from the start level, %s should be unused: %s", p, got)
		}
	}
}

// A counter that swaps textures by number names none of them, and a pruned
// release used to ship without every one but the fixed ends of its range.
func TestNamesBuiltAtRuntimeAreReached(t *testing.T) {
	root := writeTree(t, map[string]string{
		"levels/start.yaml": "scripts: [hud]\n",
		"scripts/hud.flang": `
icon = format("{}_{}_icon", weapon, corner)
sfx = "pickup_" + kind
log("enemy " + name + " died")
junk = format("{}_{}", a, b)
`,
		"textures/rifle_tl_icon.png": "",
		"sounds/pickup_ammo.wav":     "",
		"textures/enemy_skin.png":    "", // "enemy " has a space: not a name
		"textures/some_thing.png":    "", // "{}_{}" is too little to go on
	})
	keep := keepSet(t, root, game.Manifest{StartLevel: "start"})
	for p, want := range map[string]bool{
		"textures/rifle_tl_icon.png": true,
		"sounds/pickup_ammo.wav":     true,
		"textures/enemy_skin.png":    false,
		"textures/some_thing.png":    false,
	} {
		if keep[p] != want {
			t.Errorf("reached %s = %v, want %v", p, keep[p], want)
		}
	}
}

func TestNamePatternMatches(t *testing.T) {
	for _, c := range []struct {
		pat  namePattern
		name string
		want bool
	}{
		{namePattern{"ammo_", ""}, "ammo_07", true},
		{namePattern{"ammo_", ""}, "ammo_", false}, // the hole holds something
		{namePattern{"ammo_", ""}, "textures/ammo_07", false},
		{namePattern{"", "_icon"}, "rifle_tl_icon", true},
		{namePattern{"a_", "_b", "_c"}, "a_x_b_y_c", true},
		{namePattern{"a_", "_b", "_c"}, "a__b_y_c", false},
	} {
		if got := c.pat.matches(c.name); got != c.want {
			t.Errorf("%q matches %q = %v, want %v", c.pat, c.name, got, c.want)
		}
	}
}

func TestDeleteUnusedStaysInsideTheContent(t *testing.T) {
	root := unusedTree(t, map[string]string{"textures/old/deep/stale.png": ""})
	done, err := DeleteUnused(root, []string{"textures/old/deep/stale.png"})
	if err != nil || len(done) != 1 {
		t.Fatalf("delete: %v %v", done, err)
	}
	if _, err := os.Stat(filepath.Join(root, "textures", "old")); !os.IsNotExist(err) {
		t.Error("the directories the file left empty should go with it")
	}
	if _, err := os.Stat(filepath.Join(root, "textures")); err != nil {
		t.Error("the content directory itself must stay")
	}
	for _, bad := range []string{"game.yaml", "textures", "shaders/world.glsl", "../outside", "textures/../../outside"} {
		if _, err := DeleteUnused(root, []string{bad}); err == nil {
			t.Errorf("deleting %q should be refused", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "game.yaml")); err != nil {
		t.Error("game.yaml was deleted")
	}
}

func TestUnusedCommandPrintsTheReport(t *testing.T) {
	project := t.TempDir()
	src := unusedTree(t, map[string]string{"textures/stale.png": ""})
	if err := os.Rename(src, filepath.Join(project, "resources")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := UnusedMain(project, nil, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "textures/stale.png") || !strings.Contains(out.String(), "1 of ") {
		t.Errorf("output:\n%s", out.String())
	}
}
