package editor

// editor_compile.go — the Compile button.
//
// Compiling a level is a build step, and cmd/foundrycompile is the way to run
// it over everything. This is the other way: the level that is open, right now,
// as it stands in memory. It matters because the compiler is the only thing
// that will tell an author whether their level is SEALED, and finding that out
// at package time — after the walls have been forgotten about — is finding out
// too late.
//
// What it compiles is the live level rather than the file on disk, so a level
// with unsaved edits reports on the edits. The artefact still goes beside the
// saved file, because that is where a release build looks for it.

import (
	"fmt"
	"log"
	"path/filepath"

	"foundry/internal/compile"
	"foundry/internal/levelio"
)

// EditorCompileLevel compiles the open level and writes the artefact beside it.
//
// The status line gets a summary; the log gets the leaks, one per line, because
// each of them names a place to go and look and the status line is one line.
func EditorCompileLevel() (string, error) {
	lvl := Editor.level
	if lvl == nil {
		return "", fmt.Errorf("no level open")
	}
	c, err := levelio.CompileLevel(lvl, compile.DefaultOptions())
	if err != nil {
		return "", err
	}
	out := levelio.CompiledPath(Editor.SavePath)
	if err := levelio.SaveCompiledLevel(c, out); err != nil {
		return "", err
	}

	log.Printf("compiled %s: %s", filepath.Base(out), c.Stats.Summary())
	for _, leak := range c.Stats.Leaks {
		log.Printf("LEAK: %s is at %v, outside the level — something in the shell does not meet",
			leak.What, leak.Position)
	}

	msg := fmt.Sprintf("Compiled to %s — %d cells, %.1f visible on average",
		filepath.Base(out), c.Stats.InteriorLeaves, c.Stats.AvgVisibleLeaves)
	if n := len(c.Stats.Leaks); n > 0 {
		// Named on the status line rather than only in the log, because a
		// leaked level compiles to something that looks fine until it is
		// played: the geometry is all there, and only the culling is wrong.
		msg = fmt.Sprintf("Compiled to %s, but the level LEAKS in %d place(s) — see the log",
			filepath.Base(out), n)
	}
	return msg, nil
}
