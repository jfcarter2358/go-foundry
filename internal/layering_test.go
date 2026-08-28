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
	"gfx":    {"world", "render", "editor", "app", "uiwin", "uifont", "ui", "uidraw"},
	// ui is the .fui document, the anchor layout solve, hit testing and the
	// screen stack, with no idea what a GPU or a level is. It is the same split
	// particle makes and for the same reason: the UI editor's live preview and
	// the running game must drive one solver, so it has to sit below both.
	"ui": {"gfx", "uidraw", "world", "render", "collision", "script", "levelio", "editor", "app", "uiwin"},
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
	// audio sits at particle's level: clips, a voice pool and a software
	// mixer, with no idea what a level is. Its entry is stricter than
	// particle's by one name — it must not reach gfx either. The listener is
	// four plain vectors that internal/app copies out of gfx.Cam once a
	// frame, and keeping gfx out is what makes the spatial maths a pure
	// function a test can call with no camera and no GL context.
	"audio":     {"gfx", "world", "render", "collision", "script", "levelio", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	"texture":   {"world", "render", "editor", "app", "uiwin", "textureui", "ui", "uidraw", "uifont"},
	"detail":    {"render", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	"biome":     {"render", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	"collision": {"render", "editor", "app", "uiwin", "ui", "uidraw", "uifont"},
	"render":    {"editor", "app", "uiwin", "uidraw"},
	"script":    {"editor", "app", "uiwin"},
	"levelio":   {"editor", "app", "uiwin"},
	"uiwin":     {"editor", "app"},
	"textureui": {"editor", "app"},
	"editor":    {"app"},
}

func TestPackagesDoNotDependUpward(t *testing.T) {
	for pkg, forbidden := range upward {
		out, err := exec.Command("go", "list", "-deps", "foundry/internal/"+pkg).Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pkg, err)
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
