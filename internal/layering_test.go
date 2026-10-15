package internal

// layering_test.go — the package split, asserted.
//
// The whole point of the split is that a domain package cannot reach the
// editor, the app shell or a window. Nothing but a test enforces that: Go will
// happily let someone add the import, and the cycle only appears once two
// packages reach for each other. This fails on the first one.
//
// Package `internal` has no source of its own; this file exists only to hold
// the assertion somewhere `go test ./...` will run it.

import (
	"os/exec"
	"strings"
	"testing"
)

// upward lists, for each package, the packages it must never depend on —
// directly or transitively.
var upward = map[string][]string{
	"geom": {"world", "gfx", "render", "editor", "app"},
	// scriptdata is the tags/attributes/script/params triple, and the four
	// scalars an attribute holds. Both a level object and a UI widget carry
	// one, so it sits below both — it was in internal/world only because that
	// was the only thing that needed it at the time.
	"scriptdata": {"geom", "gfx", "flang", "ui", "world", "render", "editor", "app", "uiwin"},
	// uifont bakes .ttf files into glyph atlases and lays text out against
	// them. Pure CPU, and below gfx as well as everything above it: the layout
	// solve in internal/ui measures text, so a font metric has to be reachable
	// from a package that has never heard of a GPU. Metrics are data, so they
	// sit with the data rather than arriving through a function variable.
	"uifont": {"gfx", "world", "render", "editor", "app", "uiwin"},
	"gfx":    {"world", "render", "editor", "app", "uiwin", "uifont", "ui", "uidraw", "video"},
	// ui is the .fui document, the anchor layout solve, hit testing and the
	// screen stack, with no idea what a GPU or a level is. It is the same split
	// particle makes and for the same reason: the UI editor's live preview and
	// the running game must drive one solver, so it has to sit below both.
	"ui": {"gfx", "uidraw", "video", "world", "render", "collision", "script", "levelio", "editor", "app", "uiwin"},
	// uidraw is the screen-space pass. Above gfx, below world: every entry point
	// in internal/render takes a *world.Level, and a UI that needed a level
	// could not draw a main menu before one had loaded, nor the loading screen
	// that covers the load.
	"uidraw": {"world", "render", "collision", "script", "levelio", "editor", "app", "uiwin"},
	"world":  {"render", "collision", "script", "levelio", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	"model":  {"world", "render", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	// particle sits at model's level: the .fpar document and the CPU
	// simulation, with no idea what a level or a GPU is. internal/world
	// imports it, which is what forbids the reverse.
	"particle": {"world", "render", "collision", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	// noise3d builds the volumetric passes' noise volumes. Pure arithmetic, and
	// held below gfx so the tiling it promises is testable with no GL context.
	"noise3d": {"gfx", "world", "particle", "render", "collision", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	// audio sits at particle's level: clips, a voice pool and a software
	// mixer, with no idea what a level is. Its entry is stricter than
	// particle's by one name — it must not reach gfx either. The listener is
	// four plain vectors that internal/app copies out of gfx.Cam once a
	// frame, and keeping gfx out is what makes the spatial maths a pure
	// function a test can call with no camera and no GL context.
	"audio": {"gfx", "world", "render", "collision", "script", "levelio", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	// video sits just above audio, and is otherwise held to audio's rules: a
	// decoded frame is an image.RGBA and getting one onto a texture is
	// somebody else's job, which is what keeps every decision in it testable
	// with no GL context. It is allowed to reach audio, and only audio,
	// because a video's soundtrack has to go through the one mixer with the
	// one set of gain sliders — a cutscene that ignored the music slider
	// would be a bug reported as "the settings do nothing".
	"video":     {"gfx", "world", "render", "collision", "script", "levelio", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	"texture":   {"world", "render", "editor", "app", "uiwin", "textureui", "ui", "uidraw", "uifont"},
	"biome":     {"render", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	"collision": {"render", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	// nav builds a level's navigation grid from its brushes and answers path
	// queries over it. It reads a level and nothing that moves one, so it sits
	// below the resolver and the controllers both — a path is a question about
	// the level's shape, not about any body in it.
	"nav": {"collision", "ai", "render", "script", "levelio", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	// ai runs the controllers: it steers entities through the fields collision
	// already integrates, and posts its events through hooks, so it sits above
	// collision and below the script layer that addresses it.
	"ai":      {"render", "script", "levelio", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	"render":  {"editor", "app", "uiwin", "uidraw"},
	"script":  {"editor", "app", "uiwin"},
	"levelio": {"editor", "app", "uiwin"},
	// savefile is a save on disk, read by the script runtime (save_info) and
	// written by savegame, so it sits under both.
	"savefile":  {"script", "savegame", "levelio", "editor", "app", "uiwin", "render", "collision"},
	"savegame":  {"editor", "app", "uiwin"},
	"uiwin":     {"editor", "app"},
	"textureui": {"editor", "app"},
	// The Nanite mesh: cluster cuts a triangle list, meshsdf bakes a distance
	// grid, nanite is the cooked .fnm file. All three are pure CPU and sit
	// below gfx and model — pack reads a cooked mesh's slots to follow its
	// textures, and must not link OpenGL to do it. The cook, which imports
	// through model and welds through gfx, is nanitecook, above them — and
	// below the world and everything that draws one, which only ever read
	// the cooked file. meshcollide is the collision a cooked mesh carries
	// (its cut, tree and hulls): pure CPU like the rest, and below
	// collision and nav, which resolve and rasterise it.
	"cluster":     {"gfx", "model", "world", "render", "editor", "app", "uiwin"},
	"meshcollide": {"gfx", "model", "nanite", "nanitecook", "world", "collision", "nav", "render", "editor", "app", "uiwin"},
	"meshsdf":     {"gfx", "model", "world", "render", "editor", "app", "uiwin"},
	"nanite":      {"gfx", "model", "nanitecook", "world", "render", "editor", "app", "uiwin"},
	"nanitecook":  {"world", "levelio", "render", "editor", "app", "uiwin"},
	// impostor bakes a cooked mesh's far cards on the CPU (docs/regions/
	// PRESETS_5.md): beside the cook, below everything that draws or places
	// one — a generator stage bakes them headless.
	"impostor": {"nanitecook", "world", "levelio", "render", "editor", "app", "uiwin", "procgen"},
	// glext is GL extension detection and the readback wrappers that work
	// round a driver (glext/readback.go): above the GL binding, below
	// everything that draws.
	"glext": {"gfx", "world", "render", "editor", "app", "uiwin"},
	// pack stages an install tree and archives it: a tree of files and the
	// manifest, and nothing about how the engine loads either. Below gfx and
	// levelio, so cmd/foundrypack packs without linking OpenGL.
	"pack": {"gfx", "world", "levelio", "compile", "render", "script", "editor", "app", "uiwin", "release", "setup"},
	// setup is the Windows installer, which is shipped to players: it must
	// stay the standard library and fileutil, and nothing of the engine.
	"setup": {"gfx", "world", "levelio", "compile", "render", "script", "editor", "app", "uiwin", "release", "pack", "game", "ui", "audio"},
	// release is the pipeline the editor's Release window runs in a child
	// process. Above levelio, because it compiles levels; below the editor
	// that starts it.
	"release": {"editor", "app", "uiwin"},
	"editor":  {"app"},
	// procgen is the procedural generators' core — the .fgen document, the
	// stage registry and the run engine — and is pure CPU, below the world
	// and everything that draws: a stage's mesh output (procgen/meshout,
	// which cooks through nanitecook and so reaches gfx) and its level
	// splice (procgen/genlevel, which needs world's schema) live beside it,
	// and the core reaches neither. noise is below even that.
	"procgen":       {"gfx", "model", "nanitecook", "world", "levelio", "render", "editor", "app", "uiwin"},
	"procgen/noise": {"gfx", "model", "world", "render", "editor", "app", "uiwin", "procgen"},
	// procgen/terrain is the heightfield generator behind the terrain stage
	// and what later stages read its products through: pure computation,
	// so it stays below the engine as noise does (a scatter stage importing
	// it must not drag gfx in).
	"procgen/terrain": {"gfx", "model", "nanitecook", "world", "render", "editor", "app", "uiwin", "procgen"},
	// procgen/regions (environment regions: footprints, blend weights, the
	// adjacency table) is pure computation the core hands out as weight
	// functions and genlevel and the stages share: below both, as terrain is.
	"procgen/regions": {"gfx", "model", "nanitecook", "world", "render", "editor", "app", "uiwin", "procgen"},
	// procgen/sdf (signed-distance modelling and its mesher) and
	// procgen/rocks (the rock generator on it) are pure computation too:
	// the rocks stage turns their meshes into ImportMeshes and cooks them.
	"procgen/sdf":   {"gfx", "model", "nanitecook", "world", "render", "editor", "app", "uiwin", "procgen"},
	"procgen/rocks": {"gfx", "model", "nanitecook", "world", "render", "editor", "app", "uiwin", "procgen"},
	// procgen/cave (the cave stage's void, rock, cut and sky bake) is pure
	// computation on sdf and a terrain grid.
	"procgen/cave": {"gfx", "model", "nanitecook", "world", "render", "editor", "app", "uiwin", "procgen"},
	// procgen/ruins (the ruins stage's architecture library, layout and site
	// field) is pure computation on sdf and noise.
	"procgen/ruins": {"gfx", "model", "nanitecook", "world", "render", "editor", "app", "uiwin", "procgen"},
	// procgen/place (scatter, cliff walls, vegetation patches) reads a
	// terrain and rock variants and answers instances: pure computation,
	// the placement stages make level placements of them.
	"procgen/place": {"gfx", "model", "nanitecook", "world", "render", "editor", "app", "uiwin", "procgen"},
	// polyhaven is the Poly Haven API client (listings, cached md5-verified
	// downloads, thumbnails): pure Go over net/http. The importer
	// (polyhaven/phimport) cooks, so it may reach gfx through nanitecook;
	// the client and its catalog must not, so the editor's browser and a
	// headless tool share it.
	"polyhaven": {"gfx", "model", "nanitecook", "world", "render", "editor", "app", "uiwin", "procgen"},
	// texcompress (the BC7/BC5 encoders and their disk cache) is pure CPU
	// under gfx, which uploads what it makes.
	"texcompress": {"gfx", "world", "render", "editor", "app", "uiwin"},
	// grassfield is the Poly Haven grass instancer's files (the .fgk kit and
	// the per-tile .fgrass fields) and the distance rules the renderer and
	// its tests share: pure CPU, written by a procgen stage and read by
	// render, so below both and below the world.
	"grassfield": {"gfx", "model", "nanite", "nanitecook", "world", "render", "editor", "app", "uiwin", "procgen"},
}

func TestPackagesDoNotDependUpward(t *testing.T) {
	for pkg, forbidden := range upward {
		out, err := exec.Command("go", "list", "-deps", "foundry/internal/"+pkg).Output()
		if err != nil {
			// Most likely the package has been removed, and its entry here
			// with it should be.
			t.Fatalf("go list -deps %s: %v (if internal/%s is gone, delete its entry from upward)", pkg, err, pkg)
		}
		deps := map[string]bool{}
		for _, line := range strings.Split(string(out), "\n") {
			if name, ok := strings.CutPrefix(strings.TrimSpace(line), "foundry/internal/"); ok {
				deps[name] = true
			}
		}
		for _, bad := range forbidden {
			if deps[bad] {
				t.Errorf("internal/%s depends on internal/%s — the split runs the other way", pkg, bad)
			}
		}
	}
}

// The release game runtime plays scores (the model, the interpreter and
// the scheduler: a level's music) and links none of the Score editor's own
// code — the engraver and its font, the editing commands, the importers and
// exporters, the CLI. The release build is `-tags release` over cmd/foundry.
func TestTheReleaseLinksOnlyScorePlayback(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-tags", "release", "foundry/cmd/foundry").Output()
	if err != nil {
		t.Fatalf("go list -deps -tags release foundry/cmd/foundry: %v", err)
	}
	allowed := map[string]bool{"score": true, "score/interpret": true, "score/play": true}
	plays := false
	for _, line := range strings.Split(string(out), "\n") {
		name, ok := strings.CutPrefix(strings.TrimSpace(line), "foundry/internal/")
		if !ok {
			continue
		}
		if name == "editor" || strings.HasPrefix(name, "editor/") {
			t.Errorf("the release build links internal/%s", name)
		}
		if name == "score" || strings.HasPrefix(name, "score/") {
			if !allowed[name] {
				t.Errorf("the release build links internal/%s: only score playback belongs in the runtime", name)
			}
			plays = plays || name == "score/play"
		}
	}
	if !plays {
		t.Error("the release build does not link internal/score/play: a level's score would not play")
	}
}
