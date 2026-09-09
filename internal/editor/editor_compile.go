package editor

// editor_compile.go — the Compile button, and looking at what it made.
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
//
// # And then Preview, which is the other half of the same problem
//
// The numbers say whether a level is sealed and roughly how much the visibility
// set buys. They say nothing about whether it LOOKS right, and a compiled level
// can be wrong in ways only the eye catches: a seam where a texture restarts, a
// surface CSG removed that should not have gone, a shadow baked into the wrong
// place. Without a way to look, the first sight of a compiled level is the
// shipped build.
//
// So Preview compiles into memory and hands the result to the level itself —
// lvl.Static, with the baked chunks marked Compiled — which is exactly the
// state a release build loads into. Every pass then draws it the release way
// with no special case anywhere, because there is no special case: the editor
// is showing the same thing, from the same structures.
//
// Editing while it is on would show you the last compile rather than the edit,
// so any structural change drops it (dropCompiledPreview, called from
// EditorPushUndo).

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
	reportCompile(filepath.Base(out), &c.Stats)

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

// EditorSetCompiledPreview turns the compiled view on or off.
func EditorSetCompiledPreview(on bool) (string, error) {
	lvl := Editor.level
	if lvl == nil {
		return "", fmt.Errorf("no level open")
	}
	if !on {
		dropCompiledPreview()
		return "Preview off — showing the brushes again", nil
	}

	// Compiled fresh every time rather than reusing the .fbsp on disk: the
	// point is to look at what the level IS, and the file may be older than the
	// edits.
	c, err := levelio.CompileLevel(lvl, compile.DefaultOptions())
	if err != nil {
		return "", err
	}
	static, err := levelio.StaticGeometryFrom(c)
	if err != nil {
		return "", err
	}

	dropCompiledPreview() // whatever was up before
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !compile.Bakes(ch) {
			continue
		}
		// Its surfaces are in the static list now, so its own meshes are a
		// second copy of them — released, exactly as a compiled load never
		// builds them in the first place.
		ch.Compiled = true
		ch.ReleaseGeometry()
		ch.Unloaded = false
	}
	lvl.Static = static

	reportCompile("preview", &c.Stats)
	msg := fmt.Sprintf("Preview: %d surfaces from %d faces, %d cells",
		len(static.Surfaces), c.Stats.SourceFaces, c.Stats.InteriorLeaves)
	if n := len(c.Stats.Leaks); n > 0 {
		msg = fmt.Sprintf("Preview: the level LEAKS in %d place(s), so nothing is culled — see the log", n)
	}
	return msg, nil
}

// dropCompiledPreview puts the brushes back.
//
// Safe to call when no preview is up, which is what lets every structural edit
// call it without asking.
func dropCompiledPreview() {
	lvl := Editor.level
	if lvl == nil || lvl.Static == nil {
		return
	}
	lvl.Static.Release()
	lvl.Static = nil
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.Compiled {
			continue
		}
		ch.Compiled = false
		ch.ReloadGeometry()
	}
}

// reportCompile puts the stats and every leak in the log.
func reportCompile(what string, s *compile.Stats) {
	log.Printf("compiled %s: %s", what, s.Summary())
	for _, leak := range s.Leaks {
		log.Printf("LEAK: %s is at %v, outside the level — something in the shell does not meet",
			leak.What, leak.Position)
	}
}

// EditorPreviewingCompiled reports whether the compiled view is up.
//
// Read off the level rather than kept as a flag beside it: the level is
// replaced wholesale by an open, a load and an undo, and a flag next to it
// would survive all three saying something that is no longer true.
func EditorPreviewingCompiled() bool {
	return Editor.level != nil && Editor.level.Static != nil
}
