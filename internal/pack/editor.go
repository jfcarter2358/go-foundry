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
//	licenses/             third-party licenses for what the editor embeds
//	                      (Bravura-OFL.txt: the Score editor's music font)
//	codecs/               the OGG Vorbis and MP3 encoders the Score editor's
//	                      exports load at run time (libogg, libvorbis,
//	                      libvorbisenc, libmp3lame), their licences and a
//	                      README saying where their source is; never linked,
//	                      and never in a game (docs/score/CODECS.md)

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/score/glyph/bravura"
)

// LicensesDirName is the folder of third-party licenses an editor install
// carries.
const LicensesDirName = "licenses"

// thirdPartyLicenses are the licenses of what the editor binary embeds that
// must travel with it: Bravura, the Score editor's music font (SIL OFL 1.1,
// which asks that the license accompany every copy of the font). The
// release game runtime does not link the font.
var thirdPartyLicenses = map[string]string{
	bravura.LicenseFileName: bravura.License,
}

// CodecsDirName is an editor install's folder of separately loaded codec
// libraries (internal/score/io/audioenc looks there first).
const CodecsDirName = "codecs"

// codecsReadme is codecs/README.txt: what the libraries are, their
// licences, where their source is, and that they can be replaced — which
// the LGPL asks of a program that uses LAME without publishing its own
// source under it.
const codecsReadme = `The Score editor's OGG Vorbis and MP3 encoders
==============================================

These libraries are loaded by the Foundry editor when it exports audio as
OGG Vorbis or MP3 (File > Export in the Score editor, or
foundry score export-audio). The editor is not linked against them: it
runs without them (WAV and FLAC are its own), and games built with it
never contain them.

  libogg-0.dll, libvorbis-0.dll, libvorbisenc-2.dll
      Xiph.Org's Ogg and Vorbis reference libraries, BSD-3-Clause
      (libogg-COPYING.txt, libvorbis-COPYING.txt).
      Source: https://xiph.org/downloads/ (libogg 1.3.6, libvorbis 1.3.7)

  libmp3lame-0.dll
      LAME, the MP3 encoder, GNU Library General Public License version 2
      (LAME-COPYING.txt). Source: https://lame.sourceforge.io/ (LAME 3.100),
      used unmodified.

These are MSYS2's mingw64 builds (https://packages.msys2.org/). Any build
of the same libraries with the same file names may be put here instead.
The editor looks for them in the folder the FOUNDRY_CODECS environment
variable names, then in this folder, then beside the editor, then where
the system keeps its libraries; on Linux that is usually where they come
from (the libvorbisenc2 and libmp3lame0 packages).
`

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
	// Codecs is a directory of the OGG and MP3 encoder libraries and their
	// licences for the tree's codecs/ (packaging/codecs/fetch-windows.sh
	// makes the Windows one). Empty leaves the folder out: the editor then
	// uses the system's libraries, or says they are missing.
	Codecs string
	Logf   func(format string, args ...any)
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
	if o.GOOS == "windows" {
		if err := checkDLLs(filepath.Join(o.Out, o.ExeName), "Pass it with -lib."); err != nil {
			return Result{}, err
		}
	}
	if err := p.stageCodecs(o); err != nil {
		return Result{}, err
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
	for name, text := range thirdPartyLicenses {
		dst := filepath.Join(o.Out, LicensesDirName, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(dst, []byte(text), 0644); err != nil {
			return Result{}, fmt.Errorf("licenses: %w", err)
		}
		p.copied++
		p.bytes += int64(len(text))
	}

	stamp := Stamp{Title: EditorTitle, Version: o.Version, Exe: o.ExeName, Kind: KindEditor}
	if err := WriteStamp(o.Out, stamp); err != nil {
		return Result{}, err
	}
	o.Logf("staged %d files, %.1f MB, into %s", p.copied, float64(p.bytes)/(1024*1024), o.Out)
	return Result{Stamp: stamp, Files: p.copied, Bytes: p.bytes}, nil
}

// stageCodecs copies the codec libraries for GOOS and every other file
// (the licences) into codecs/, with the README, and on Windows checks each
// DLL finds what it imports there or in Windows itself.
func (p *packer) stageCodecs(o EditorOptions) error {
	if o.Codecs == "" {
		o.Logf("no -codecs folder: the install's Score editor exports OGG and MP3 only with the system's libraries")
		return nil
	}
	entries, err := os.ReadDir(o.Codecs)
	if err != nil {
		return fmt.Errorf("codecs: %w", err)
	}
	libs := 0
	var dlls []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !belongsOnPlatform(e.Name(), o.GOOS) {
			continue
		}
		dst := filepath.Join(CodecsDirName, e.Name())
		if err := p.copyTo(filepath.Join(o.Codecs, e.Name()), dst); err != nil {
			return fmt.Errorf("codecs: %w", err)
		}
		if isPlatformLib(e.Name()) || strings.Contains(e.Name(), ".so.") {
			libs++
			if strings.EqualFold(filepath.Ext(e.Name()), ".dll") {
				dlls = append(dlls, filepath.Join(p.out, dst))
			}
		}
	}
	if libs == 0 {
		return fmt.Errorf("codecs: %s holds no libraries for %s", o.Codecs, o.GOOS)
	}
	readme := filepath.Join(p.out, CodecsDirName, "README.txt")
	if err := os.WriteFile(readme, []byte(codecsReadme), 0644); err != nil {
		return fmt.Errorf("codecs: %w", err)
	}
	p.copied++
	p.bytes += int64(len(codecsReadme))
	for _, d := range dlls {
		missing, err := MissingDLLs(d)
		if err != nil {
			return fmt.Errorf("codecs: %w", err)
		}
		if len(missing) > 0 {
			return fmt.Errorf("codecs/%s imports %s, which is neither in codecs/ nor part of Windows", filepath.Base(d), strings.Join(missing, ", "))
		}
	}
	return nil
}
