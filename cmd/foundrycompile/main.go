// Command foundrycompile turns authored levels into the form a shipped game
// loads.
//
// An authored level is a list of brushes. Making it drawable means running CSG,
// building a BSP, flooding visibility through the portals between its cells and
// baking the lighting onto its vertices — none of which a shipped game should
// be doing while the player waits. This is where that happens: once, as a build
// step, writing a .fbsp beside each .yaml.
//
// It runs with no window. The engine's loader is used as-is with its uploads
// switched off (levelio.LoadLevelHeadless), so a level that compiles here is a
// level that loads there — the same ID uniquing, the same prefab stamping, the
// same validation. The loop itself is internal/release's, which the editor's
// Release window runs too.
//
//	foundrycompile                      compile every level under resources/levels
//	foundrycompile canyon               compile one, by name
//	foundrycompile -root build/install-linux
//
// A LEAK is reported and does not fail the build. A level with a gap in its
// shell still compiles to something correct; it is just that the void outside
// floods in and most of the level ends up on the wrong side of it, so
// visibility buys nothing. Reporting it and letting the author look is the
// useful thing — see internal/compile/outside.go. -strict turns it into a
// failure, for a release build that should not ship one.
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"

	"foundry/internal/compile"
	"foundry/internal/fileutil"
	"foundry/internal/release"
)

func main() {
	root := flag.String("root", ".", "project or install directory: the one holding resources/")
	grid := flag.Float64("grid", float64(compile.DefaultLightOptions().GridStep),
		"how far apart baked light samples are, in world units; faces are cut on a grid this size before lighting")
	minGrid := flag.Float64("min-grid", float64(compile.DefaultLightOptions().MinGridStep),
		"finest light-bake sample spacing, used only where a shadow edge crosses (0 = never refine)")
	noLight := flag.Bool("no-light", false, "skip the light bake")
	strict := flag.Bool("strict", false, "fail the build on a leaking level")
	quiet := flag.Bool("quiet", false, "print only failures")
	flag.Parse()

	log.SetFlags(0)
	abs, err := filepath.Abs(*root)
	if err != nil {
		log.Fatalf("%v", err)
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(abs)

	opt := compile.Options{SkipLight: *noLight}
	opt.Light = compile.DefaultLightOptions()
	opt.Light.GridStep = float32(*grid)
	opt.Light.MinGridStep = float32(*minGrid)

	if failed := release.CompileLevels(flag.Args(), opt, *strict, *quiet, log.Printf); failed > 0 {
		os.Exit(1)
	}
}
