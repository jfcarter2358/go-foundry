package pack

// editor.go — stage the editor itself for distribution: what a Foundry release
// on GitHub carries, and what `task dist-editor` builds.
//
// An editor install holds no project. It is the development build, the engine
// files that belong to it, and the release binaries its Release window wraps a
// game around; the projects it opens live wherever their authors keep them
// (internal/project). So, like a game, it can be replaced whole on an upgrade.
//
//	foundry[.exe]         the development build
//	SDL2.dll ...          the runtime libraries it loads, beside it (Windows)
//	.foundrypack          the stamp, KIND='editor'
//	resources/shaders/    the shaders it was built against, which it reads in
//	                      place of any project's (fileutil.EngineShadersDir)
//	docs/guide/           the authoring guide, which the editor reads as well as
//	                      the author: it is what the Claude Code panel's docs tool
//	                      serves (editor_claude_docs.go)
//	runtimes/             the release game binaries and the installer program

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
)

// RuntimesDirName is the runtimes/ folder of a project, and of an editor
// install: the prebuilt release binaries and installer program.
const RuntimesDirName = "runtimes"

// EditorTitle is the editor's name in an install: its folder, its Start Menu
// entry and its Add/Remove Programs entry.
const EditorTitle = "Foundry Editor"

// EditorOptions is one editor install to stage.
type EditorOptions struct {
	// Out is the install tree to write. Created, and emptied if it exists.
	Out string
	// Exe is the development build, for GOOS.
	Exe string
	// ExeName is what to call it in the tree. Empty means Exe's own name.
	ExeName string
	GOOS    string
	// Version is the Foundry version being packed.
	Version string
	// Shaders is the directory of shaders the build was made from.
	Shaders string
	// Guide is the docs/guide directory to carry along. Empty leaves it out, and
	// the panel's docs tool then says the install has no guide rather than
	// pretending the subject is undocumented.
	Guide string
	// Libs are runtime libraries to put beside the editor. Those not for GOOS
	// are skipped.
	Libs []string
	// Runtimes go in the tree's runtimes/.
	Runtimes []string
	Logf     func(format string, args ...any)
}

// StageEditor stages an editor install.
func StageEditor(o EditorOptions) (Result, error) {
	if o.Out == "" || o.Exe == "" || o.Shaders == "" {
		return Result{}, fmt.Errorf("an editor install needs the editor, its shaders and an output directory")
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	if o.ExeName == "" {
		o.ExeName = filepath.Base(o.Exe)
	}
	if _, err := os.Stat(filepath.Join(o.Shaders, "fragment.glsl")); err != nil {
		return Result{}, fmt.Errorf("%s does not look like the engine's shaders: %w", o.Shaders, err)
	}
	if err := refuseDangerousOutput(o.Out); err != nil {
		return Result{}, err
	}
	if err := os.RemoveAll(o.Out); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(o.Out, 0755); err != nil {
		return Result{}, err
	}

	p := &packer{src: o.Shaders, out: o.Out, goos: o.GOOS, logf: o.Logf}
	if err := p.copyTo(o.Exe, o.ExeName); err != nil {
		return Result{}, fmt.Errorf("editor: %w", err)
	}
	for _, lib := range o.Libs {
		if !belongsOnPlatform(filepath.Base(lib), o.GOOS) {
			continue
		}
		if err := p.copyTo(lib, filepath.Base(lib)); err != nil {
			return Result{}, err
		}
	}
	entries, err := os.ReadDir(o.Shaders)
	if err != nil {
		return Result{}, err
	}
	for _, e := range entries {
		if e.IsDir() || IsDevelopmentFile(e.Name()) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := p.copyTo(filepath.Join(o.Shaders, e.Name()), filepath.Join(fileutil.ResourcesDirName, "shaders", e.Name())); err != nil {
			return Result{}, err
		}
	}
	if o.Guide != "" {
		chapters := fileutil.ScanFilesByExt(o.Guide, ".md")
		if len(chapters) == 0 {
			o.Logf("no guide found at %s — the install will have no docs/", o.Guide)
		}
		for _, name := range chapters {
			if err := p.copyTo(filepath.Join(o.Guide, name), filepath.Join("docs", "guide", name)); err != nil {
				return Result{}, fmt.Errorf("guide: %w", err)
			}
		}
	}
	for _, rt := range o.Runtimes {
		if err := p.copyTo(rt, filepath.Join(RuntimesDirName, filepath.Base(rt))); err != nil {
			return Result{}, fmt.Errorf("runtimes: %w", err)
		}
	}

	stamp := Stamp{Title: EditorTitle, Version: o.Version, Exe: o.ExeName, Kind: KindEditor}
	if err := WriteStamp(o.Out, stamp); err != nil {
		return Result{}, err
	}
	o.Logf("staged %d files, %.1f MB, into %s", p.copied, float64(p.bytes)/(1024*1024), o.Out)
	return Result{Stamp: stamp, Files: p.copied, Bytes: p.bytes}, nil
}
