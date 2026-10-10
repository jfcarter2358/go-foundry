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
	"foundry/internal/world"
)

// compileKind is what a compile is for: the artefact on disk, or the preview.
type compileKind int

const (
	compileToFile compileKind = iota
	compileToPreview
)

// compileOutput is what the half of a compile that may run off the main
// thread (editorCompileRun) hands to the half that may not
// (editorCompileFinish).
type compileOutput struct {
	c   *levelio.CompiledLevel
	out string // the .fbsp written, for compileToFile
	err error
}

// EditorCompileLevel compiles the open level and writes the artefact beside it.
//
// The status line gets a summary; the log gets the leaks, one per line, because
// each of them names a place to go and look and the status line is one line.
//
// It holds the frame until it is done; the Build menu runs the same two halves
// behind a progress bar instead (editorBeginCompile).
func EditorCompileLevel() (string, error) {
	return editorCompileNow(compileToFile)
}

// EditorSetCompiledPreview turns the compiled view on or off.
func EditorSetCompiledPreview(on bool) (string, error) {
	if Editor.level == nil {
		return "", fmt.Errorf("no level open")
	}
	if !on {
		dropCompiledPreview()
		return "Preview off — showing the brushes again", nil
	}
	return editorCompileNow(compileToPreview)
}

// editorCompileNow is both halves of a compile, one after the other.
func editorCompileNow(kind compileKind) (string, error) {
	lvl := Editor.level
	if lvl == nil {
		return "", fmt.Errorf("no level open")
	}
	var res compileOutput
	// As authored: a point light held on a path would otherwise be baked
	// where the preview had it.
	withWaypointPreviewAtRest(func() { res = editorCompileRun(lvl, kind, Editor.SavePath, nil) })
	return editorCompileFinish(lvl, kind, res)
}

// editorCompileRun is the slow half of a compile: everything that only reads
// the level, plus writing the file. Safe off the main thread as long as nothing
// edits lvl meanwhile — which is what the progress modal is for.
//
// Compiled fresh every time, preview included, rather than reusing the .fbsp
// on disk: the point is to look at what the level IS, and the file may be
// older than the edits.
func editorCompileRun(lvl *world.Level, kind compileKind, savePath string, p compile.Progress) compileOutput {
	opt := compile.DefaultOptions()
	opt.Progress = p
	c, err := levelio.CompileLevel(lvl, opt)
	if err != nil {
		return compileOutput{err: err}
	}
	res := compileOutput{c: c}
	if kind == compileToFile {
		res.out = levelio.CompiledPath(savePath)
		if p != nil {
			p("Writing "+filepath.Base(res.out), 0)
		}
		res.err = levelio.SaveCompiledLevel(c, res.out)
	}
	return res
}

// editorCompileFinish is the main-thread half: the report, and for a preview
// the GL upload and handing the result to the level.
func editorCompileFinish(lvl *world.Level, kind compileKind, res compileOutput) (string, error) {
	if res.err != nil {
		return "", res.err
	}
	c := res.c
	if kind == compileToPreview {
		return editorInstallPreview(lvl, c)
	}

	base := filepath.Base(res.out)
	reportCompile(base, &c.Stats)
	if lvl.NoBake {
		log.Printf("compiled %s: lighting not baked (Build > Lighting > No bake) — lit live, with GI", base)
		if w := noBakeLightWarning(lvl.Lights); w != "" {
			log.Printf("WARNING: %s", w)
		}
	}

	msg := fmt.Sprintf("Compiled to %s — %d cells, %.1f visible on average",
		base, c.Stats.InteriorLeaves, c.Stats.AvgVisibleLeaves)
	if n := len(c.Stats.Leaks); n > 0 {
		// Named on the status line rather than only in the log, because a
		// leaked level compiles to something that looks fine until it is
		// played: the geometry is all there, and only the culling is wrong.
		msg = fmt.Sprintf("Compiled to %s, but the level LEAKS in %d place(s) — see the log", base, n)
	}
	return msg, nil
}

// editorInstallPreview puts a compile up as the compiled view.
func editorInstallPreview(lvl *world.Level, c *levelio.CompiledLevel) (string, error) {
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
		// Its BAKED surfaces are in the static list now, so their own meshes
		// are a second copy of them — released, exactly as a compiled load
		// never builds them in the first place. Its dynamic brushes keep
		// theirs: the compiler left them out of the static list on purpose,
		// so they are still drawn from their own buffers.
		ch.Compiled = true
		ch.ReleaseBakedGeometry()
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
		if ch.WantsGeometry() {
			ch.ReloadGeometry()
		}
	}
}

// reportCompile puts the stats and every leak in the log.
func reportCompile(what string, s *compile.Stats) {
	log.Printf("compiled %s: %s", what, s.Summary())
	for _, leak := range s.Leaks {
		log.Printf("LEAK: %s", leak.Describe())
	}
	if w := s.SealWarning(); w != "" {
		log.Printf("WARNING: %s", w)
	}
	if w := s.StreamedClipWarning(); w != "" {
		log.Printf("WARNING: %s", w)
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
