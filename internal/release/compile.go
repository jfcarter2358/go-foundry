package release

// compile.go — compiling every level, as a build step.
//
// Shared by cmd/foundrycompile and the release build, so that `make
// compile-levels` and the Release window cannot come to disagree about what a
// compiled level is or when a leak fails one.

import (
	"fmt"
	"path/filepath"
	"strings"

	"foundry/internal/compile"
	"foundry/internal/levelio"
	"foundry/internal/script"
)

// CompileLevels compiles levels under fileutil.ResourcesPath — the named ones,
// or every one when names is empty — writing a .fbsp beside each, and returns
// how many failed. A leak fails a level only when strict.
func CompileLevels(names []string, opt compile.Options, strict, quiet bool, logf func(format string, args ...any)) int {
	paths := levelPaths(names)
	if len(paths) == 0 {
		// Not an error, because this runs as part of every release build and a
		// project with no levels yet is a project, not a broken one. A game
		// whose manifest names a level that does not exist still fails, loudly,
		// at boot — which is where that belongs.
		logf("no levels under %s; nothing to compile", levelio.LevelsDir())
		return 0
	}

	failed := 0
	for _, path := range paths {
		if err := compileOne(path, opt, strict, quiet, logf); err != nil {
			logf("FAIL %s: %v", filepath.Base(path), err)
			failed++
		}
	}
	return failed
}

// levelPaths resolves the names given, or finds every level under the root
// when there are none.
func levelPaths(names []string) []string {
	if len(names) > 0 {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = levelio.LevelPath(n)
		}
		return out
	}
	dir := levelio.LevelsDir()
	files := levelio.ScanLevelFiles(dir)
	out := make([]string, len(files))
	for i, n := range files {
		out[i] = filepath.Join(dir, n)
	}
	return out
}

func compileOne(path string, opt compile.Options, strict, quiet bool, logf func(format string, args ...any)) error {
	lvl, err := levelio.LoadLevelHeadless(path)
	if err != nil {
		return err
	}
	// Before compiling, which consumes the level: a script moving a chunk the
	// compile bakes is only half-moved in the build this produces.
	moves := script.BakedChunkMoves(&lvl)
	c, err := levelio.CompileLevel(&lvl, opt)
	if err != nil {
		return err
	}
	out := levelio.CompiledPath(path)
	if err := levelio.SaveCompiledLevel(c, out); err != nil {
		return err
	}

	name := strings.TrimSuffix(filepath.Base(path), levelio.LevelExt)
	if !quiet {
		logf("%s: %s", name, c.Stats.Summary())
	}
	for _, leak := range c.Stats.Leaks {
		logf("%s: LEAK — %s", name, leak.Describe())
	}
	if w := c.Stats.SealWarning(); w != "" {
		logf(WarningPrefix+"%s: %s", name, w)
	}
	if w := c.Stats.StreamedClipWarning(); w != "" {
		logf(WarningPrefix+"%s: %s", name, w)
	}
	for _, m := range moves {
		logf(WarningPrefix+"%s: %s", name, m)
	}
	if strict && len(c.Stats.Leaks) > 0 {
		return fmt.Errorf("%d leaks", len(c.Stats.Leaks))
	}
	if strict && len(moves) > 0 {
		return fmt.Errorf("%d script writes move a baked chunk", len(moves))
	}
	return nil
}
