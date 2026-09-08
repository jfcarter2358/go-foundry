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
// same validation.
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
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/compile"
	"foundry/internal/fileutil"
	"foundry/internal/levelio"
)

func main() {
	root := flag.String("root", "resources", "install root holding levels/, textures/ and entities/")
	grid := flag.Float64("grid", float64(compile.DefaultLightOptions().GridStep),
		"how far apart baked light samples are, in world units; faces are cut on a grid this size before lighting")
	noLight := flag.Bool("no-light", false, "skip the light bake")
	strict := flag.Bool("strict", false, "fail the build on a leaking level")
	quiet := flag.Bool("quiet", false, "print only failures")
	flag.Parse()

	log.SetFlags(0)
	abs, err := filepath.Abs(*root)
	if err != nil {
		log.Fatalf("%v", err)
	}
	fileutil.ExecutablePath = abs

	paths, err := levelPaths(flag.Args())
	if err != nil {
		log.Fatalf("%v", err)
	}
	if len(paths) == 0 {
		log.Fatalf("no levels found under %s", levelio.LevelsDir())
	}

	opt := compile.Options{SkipLight: *noLight}
	opt.Light = compile.DefaultLightOptions()
	opt.Light.GridStep = float32(*grid)

	failed := 0
	for _, path := range paths {
		if err := compileOne(path, opt, *strict, *quiet); err != nil {
			log.Printf("FAIL %s: %v", filepath.Base(path), err)
			failed++
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// levelPaths resolves the names on the command line, or finds every level under
// the root when there are none.
func levelPaths(args []string) ([]string, error) {
	if len(args) > 0 {
		out := make([]string, len(args))
		for i, a := range args {
			out[i] = levelio.LevelPath(a)
		}
		return out, nil
	}
	dir := levelio.LevelsDir()
	names := levelio.ScanLevelFiles(dir)
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(dir, n)
	}
	return out, nil
}

func compileOne(path string, opt compile.Options, strict, quiet bool) error {
	c, err := levelio.CompileLevelFile(path, opt)
	if err != nil {
		return err
	}
	out := levelio.CompiledPath(path)
	if err := levelio.SaveCompiledLevel(c, out); err != nil {
		return err
	}

	name := strings.TrimSuffix(filepath.Base(path), levelio.LevelExt)
	if !quiet {
		log.Printf("%s: %s", name, c.Stats.Summary())
	}
	for _, leak := range c.Stats.Leaks {
		log.Printf("%s: LEAK — %s is at %v, which is outside the level. Something in the shell does not meet.",
			name, leak.What, leak.Position)
	}
	if strict && len(c.Stats.Leaks) > 0 {
		return fmt.Errorf("%d leaks", len(c.Stats.Leaks))
	}
	return nil
}
