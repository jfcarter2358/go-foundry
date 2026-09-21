package pack

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/game"
)

// The prune is the one part of packing that can produce a broken install rather
// than a large one, so what is guarded here is reachability in both directions:
// that a live asset survives however the content happens to name it, and that a
// dead one does not.

// writeTree lays out a synthetic content root and returns its path.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func keepSet(t *testing.T, root string, m game.Manifest) map[string]bool {
	t.Helper()
	idx, err := indexAssets(root, contentDirs)
	if err != nil {
		t.Fatal(err)
	}
	return reachable(root, idx, manifestRoots(m))
}

// The five naming shapes the engine's own resolvers accept, each in the
// position content actually writes it in.
func TestEveryWayAnAssetIsNamedIsFollowed(t *testing.T) {
	root := writeTree(t, map[string]string{
		"ui/theme.fth":   "font: title\n",
		"ui/loading.fui": "id: loading\n",
		"ui/pause.fui":   "id: pause\nscript: scripts/pause.flang\n",
		"levels/start.yaml": `
ui: [hud]
chunks:
  - sectors:
      - floor: {texture: grass}
sprites:
  - texture: sprites/spark.png
entities:
  - type: crate
volumes:
  - load_level: cellar
scripts: [doors/sliding]
`,
		"ui/hud.fui":                  "id: hud\n",
		"textures/grass.png":          "PNG",
		"textures/sprites/spark.png":  "PNG",
		"entities/crate.yaml":         "name: crate\nmodel: models/crate.fmdl\n",
		"models/crate.fmdl":           "textures: [wood]\n",
		"textures/wood.png":           "PNG",
		"levels/cellar.yaml":          "chunks: []\n",
		"scripts/doors/sliding.flang": "def main() -> null\n  play_sound(\"click\")\nend\n",
		"scripts/pause.flang":         "def main() -> null\n  return\nend\n",
		"sounds/click.wav":            "RIFF",
		"textures/default.png":        "PNG",
	})

	keep := keepSet(t, root, game.Manifest{Title: "T", StartLevel: "start"})

	for _, want := range []string{
		"levels/start.yaml",           // the manifest's start level, by name
		"ui/hud.fui",                  // a screen named bare in the level's ui list
		"textures/grass.png",          // a texture named bare
		"textures/sprites/spark.png",  // and one named by install-relative path
		"entities/crate.yaml",         // an entity by type name
		"models/crate.fmdl",           // a model by path, from inside the entity
		"textures/wood.png",           // a texture named from inside the model
		"levels/cellar.yaml",          // a level a volume exits into
		"scripts/doors/sliding.flang", // a script named relative to scripts/
		"sounds/click.wav",            // a sound named from inside that script
		"ui/theme.fth",                // and the roots named from Go, not from content
		"ui/loading.fui",
		"ui/pause.fui",
	} {
		if !keep[want] {
			t.Errorf("%s was pruned but is reachable", want)
		}
	}
}

func TestUnreachableAssetsArePruned(t *testing.T) {
	root := writeTree(t, map[string]string{
		"ui/theme.fth":         "\n",
		"ui/loading.fui":       "\n",
		"textures/default.png": "PNG",
		"levels/start.yaml":    "chunks: []\n",
		// None of these are named from anywhere.
		"levels/scratch.yaml": "chunks: []\n",
		"textures/unused.png": "PNG",
		"ui/oldmenu.fui":      "\n",
		"sounds/unused.wav":   "RIFF",
	})

	keep := keepSet(t, root, game.Manifest{Title: "T", StartLevel: "start"})

	for _, gone := range []string{"levels/scratch.yaml", "textures/unused.png", "ui/oldmenu.fui", "sounds/unused.wav"} {
		if keep[gone] {
			t.Errorf("%s was kept but nothing reaches it", gone)
		}
	}
}

// There is no start screen in the manifest: a main menu is listed on a level,
// and not necessarily the first one. A game that opens on an intro reaches its
// menu two hops out — start level, a script's load_level, that level's ui list
// — and the menu is exactly the screen most likely to be reachable from nothing
// else.
func TestMenuOnALaterLevelIsKept(t *testing.T) {
	root := writeTree(t, map[string]string{
		"ui/theme.fth":         "\n",
		"ui/loading.fui":       "\n",
		"textures/default.png": "PNG",
		"levels/intro.yaml":    "scripts: [intro]\n",
		"scripts/intro.flang":  "def main() -> null\n  load_level(\"town\")\nend\n",
		"levels/town.yaml":     "ui: [menu]\n",
		"ui/menu.fui":          "background: menu_bg\n",
		"textures/menu_bg.png": "PNG",
	})

	keep := keepSet(t, root, game.Manifest{Title: "T", StartLevel: "intro"})
	if !keep["ui/menu.fui"] {
		t.Error("a menu listed only on a level the intro loads was pruned")
	}
	if !keep["textures/menu_bg.png"] {
		t.Error("a texture named only by the menu was pruned")
	}
}

// Content references each other in cycles all the time — a level whose script
// loads that same level, two screens that open one another. The walk has to
// terminate.
func TestCyclesTerminate(t *testing.T) {
	root := writeTree(t, map[string]string{
		"ui/theme.fth":         "\n",
		"ui/loading.fui":       "\n",
		"textures/default.png": "PNG",
		"levels/start.yaml":    "scripts: [loop]\n",
		"scripts/loop.flang":   "def main() -> null\n  load_level(\"start\")\nend\n",
	})

	keep := keepSet(t, root, game.Manifest{Title: "T", StartLevel: "start"})
	if !keep["scripts/loop.flang"] {
		t.Error("the level's script was pruned")
	}
}

// The output directory is deleted before it is written, and this tool runs from
// Makefiles with paths built by concatenation.
func TestDangerousOutputIsRefused(t *testing.T) {
	if err := refuseDangerousOutput("/"); err == nil {
		t.Error("a filesystem root was accepted as -out")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if err := refuseDangerousOutput(home); err == nil {
			t.Error("the home directory was accepted as -out")
		}
	}

	// A directory with contents that are plainly not an install tree.
	occupied := t.TempDir()
	if err := os.WriteFile(filepath.Join(occupied, "thesis.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := refuseDangerousOutput(occupied); err == nil {
		t.Error("a directory of unrelated files was accepted as -out")
	}

	// One this tool made before is fine to empty again.
	installed := t.TempDir()
	if err := os.WriteFile(filepath.Join(installed, game.FileName), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := refuseDangerousOutput(installed); err != nil {
		t.Errorf("an existing install tree was refused: %v", err)
	}
	// Including one whose pack was interrupted before its stamp, which has only
	// the manifest it staged into resources/ to go by.
	interrupted := writeTree(t, map[string]string{"game": "\x7fELF", "resources/game.yaml": "x"})
	if err := refuseDangerousOutput(interrupted); err != nil {
		t.Errorf("an unstamped install tree was refused: %v", err)
	}

	// And one that does not exist yet is the normal case.
	if err := refuseDangerousOutput(filepath.Join(t.TempDir(), "new")); err != nil {
		t.Errorf("a fresh path was refused: %v", err)
	}
}

func TestExecutableIsNamedAfterTheGame(t *testing.T) {
	for _, tc := range []struct{ title, exe, want string }{
		{"Bob's Quest", "build/foundry", "bobs-quest"},
		{"Bob's Quest", "build/foundry.exe", "bobs-quest.exe"},
		{"  Deep   Space  ", "foundry", "deep-space"},
		{"日本語", "foundry", "foundry"}, // nothing ASCII survives: fall back
	} {
		if got := defaultExeName(game.Manifest{Title: tc.title}, tc.exe); got != tc.want {
			t.Errorf("defaultExeName(%q, %q) = %q, want %q", tc.title, tc.exe, got, tc.want)
		}
	}
}

// A content tree carries the runtime libraries for every platform the project
// ships to; a pack carries only the ones its own platform can load.
func TestPlatformLibrariesAreFilteredByTarget(t *testing.T) {
	for _, tc := range []struct {
		name, goos string
		want       bool
	}{
		{"SDL2.dll", "windows", true},
		{"SDL2.dll", "linux", false},
		{"libSDL2.dylib", "darwin", true},
		{"libSDL2.dylib", "windows", false},
		{"game.yaml", "linux", true},
		{"game.yaml", "windows", true},
		{"LICENSE", "linux", true},
		{"CHANGELOG.md", "windows", true},
	} {
		if got := belongsOnPlatform(tc.name, tc.goos); got != tc.want {
			t.Errorf("belongsOnPlatform(%q, %q) = %v, want %v", tc.name, tc.goos, got, tc.want)
		}
	}
}

// A normal map is never named by any content file: the engine derives its
// name from the albedo's and loads it alongside. So it has to answer to the
// albedo's names here, or a release ships every texture with its relief
// pruned away — a failure that would only show up as "the game looks flatter
// than the editor did".
func TestNormalMapsRideAlongWithTheirAlbedo(t *testing.T) {
	root := writeTree(t, map[string]string{
		"ui/theme.fth":         "\n",
		"ui/loading.fui":       "\n",
		"ui/pause.fui":         "\n",
		"textures/default.png": "PNG",
		"levels/start.yaml":    "chunks: [{walls: [{texture: brick}]}]\n",
		"textures/brick.png":   "PNG",
		// Named by nothing; reachable only through brick.
		"textures/brick_normal.png": "PNG",
		// And one whose albedo nothing reaches, which must still be pruned.
		"textures/unused.png":        "PNG",
		"textures/unused_normal.png": "PNG",
	})

	keep := keepSet(t, root, game.Manifest{Title: "T", StartLevel: "start"})

	if !keep["textures/brick_normal.png"] {
		t.Error("a reachable texture's normal map was pruned")
	}
	if keep["textures/unused_normal.png"] {
		t.Error("an unreachable texture's normal map was kept")
	}
}

// A script switches sky by base name — `sky_cubemap = "dusk"` — and the six
// faces it expands to are named nowhere in the content.
func TestCubemapFacesAreReachedByTheirBaseName(t *testing.T) {
	faces := []string{"px", "nx", "py", "ny", "pz", "nz"}
	files := map[string]string{
		"ui/theme.fth":         "\n",
		"ui/loading.fui":       "\n",
		"ui/pause.fui":         "\n",
		"textures/default.png": "PNG",
		"levels/start.yaml":    "scripts: [sky]\n",
		"scripts/sky.flang":    "@level.prop.sky_cubemap = \"dusk\"\n",
	}
	for _, f := range faces {
		files["textures/dusk_"+f+".png"] = "PNG"
		files["textures/unused_"+f+".png"] = "PNG"
	}
	keep := keepSet(t, writeTree(t, files), game.Manifest{Title: "T", StartLevel: "start"})

	for _, f := range faces {
		if !keep["textures/dusk_"+f+".png"] {
			t.Errorf("cubemap face dusk_%s named by its base was pruned", f)
		}
		if keep["textures/unused_"+f+".png"] {
			t.Errorf("unreachable cubemap face unused_%s was kept", f)
		}
	}
}

// A ship's hull is reached from the entity type that carries it, not from any
// level — the one reference chain that runs through chunks/ (entity_chunk.go).
func TestCarriedChunkGeometryIsFollowed(t *testing.T) {
	root := writeTree(t, map[string]string{
		"ui/theme.fth":         "\n",
		"ui/loading.fui":       "\n",
		"textures/default.png": "PNG",
		"levels/start.yaml":    "entities:\n  - type: shuttle\n",
		"entities/shuttle.yaml": `
id: shuttle
chunks:
  - name: hull
    file: shuttle_hull
`,
		"chunks/shuttle_hull.yaml": "sectors:\n  - floor: {texture: deckplate}\n",
		"textures/deckplate.png":   "PNG",
		// Nothing names this one.
		"chunks/scratch.yaml": "sectors: []\n",
	})

	keep := keepSet(t, root, game.Manifest{Title: "T", StartLevel: "start"})

	if !keep["chunks/shuttle_hull.yaml"] {
		t.Error("the chunk an entity type carries was pruned; the ship would have no floor")
	}
	if !keep["textures/deckplate.png"] {
		t.Error("a texture named only from inside a carried chunk was pruned")
	}
	if keep["chunks/scratch.yaml"] {
		t.Error("an unreferenced chunk was kept")
	}
}

// A level's compiled form is what a release build loads, and nothing names it:
// the manifest and every load_level say a level's NAME, and which extension
// that means is chosen by the build. So it has to be pulled in beside the
// source rather than found by the token scan.
func TestACompiledLevelIsPackedBesideItsSource(t *testing.T) {
	root := writeTree(t, map[string]string{
		"ui/theme.fth":         "font: title\n",
		"ui/loading.fui":       "id: loading\n",
		"ui/pause.fui":         "id: pause\n",
		"textures/default.png": "x",
		"levels/start.yaml":    "chunks: []\n",
		"levels/start.fbsp":    "compiled bytes",
		"levels/stale.fbsp":    "a compiled level nothing reaches",
	})
	keep := keepSet(t, root, game.Manifest{StartLevel: "start"})

	if !keep["levels/start.fbsp"] {
		t.Error("the start level's compiled form was not packed; a release build would find no level at all")
	}
	if keep["levels/stale.fbsp"] {
		t.Error("a compiled level nothing reaches was packed")
	}
}

// And the source it was compiled from is dropped: a release build cannot read
// one, so shipping it is dead weight and ships the level's source to whoever
// installs the game.
func TestAnAuthoredLevelIsDroppedOnceItHasBeenCompiled(t *testing.T) {
	root := writeTree(t, map[string]string{
		"levels/start.yaml": "chunks: []\n",
		"levels/start.fbsp": "compiled bytes",
		"levels/draft.yaml": "chunks: []\n",
	})
	if !IsSupersededLevelSource(root, "levels/start.yaml") {
		t.Error("a level with a compiled form beside it was kept")
	}
	// One that was never compiled still ships: the failure then belongs to the
	// engine's own "recompile the level" message rather than to a packer that
	// dropped the only copy.
	if IsSupersededLevelSource(root, "levels/draft.yaml") {
		t.Error("a level with no compiled form was dropped")
	}
	// An entity definition is .yaml too, and is not a level.
	if IsSupersededLevelSource(root, "entities/crate.yaml") {
		t.Error("an entity definition was treated as a level source")
	}
}
