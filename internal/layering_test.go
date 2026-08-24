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
	"geom":      {"world", "gfx", "render", "editor", "app"},
	"gfx":       {"world", "render", "editor", "app", "uiwin"},
	"world":     {"render", "collision", "script", "levelio", "editor", "app", "uiwin"},
	"model":     {"world", "render", "editor", "app", "uiwin"},
	"texture":   {"world", "render", "editor", "app", "uiwin", "textureui"},
	"detail":    {"render", "editor", "app", "uiwin"},
	"biome":     {"render", "editor", "app", "uiwin"},
	"collision": {"render", "editor", "app", "uiwin"},
	"render":    {"editor", "app", "uiwin"},
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
