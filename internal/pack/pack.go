package pack

// pack.go — stage a release build into an install tree.
//
// A Foundry install is an executable with a resources/ folder beside it: every
// asset path in the engine resolves under resources/ (fileutil.ResourcesPath),
// so "packaging" is mostly a matter of putting the right files in the right
// folder next to the right binary. This is what decides which files those are.
// cmd/foundrypack is its command line, for the Makefile; internal/release runs
// it for the editor's Release window.
//
// The tree a pack writes:
//
//	<game>[.exe]    the release executable, named after the game
//	SDL2.dll ...    the platform's runtime libraries, where its loader looks
//	.foundrypack    the stamp the installers read
//	resources/      everything else: game.yaml and the content directories
//
// Two modes, and the difference between them is the whole point:
//
//	Prune=false  copy the content tree wholesale. What `make linux` has always
//	             done, and the right answer while a project is being built.
//	Prune=true   copy only what the game can actually reach from its manifest.
//	             A repository's resources/ accumulates: half-finished levels,
//	             textures nothing samples any more, the model somebody was
//	             trying out in March. None of it should ship, and none of it
//	             should have to be deleted by hand to not ship.
//
// The pruning walk is in refs.go. What is worth knowing here is that it is
// deliberately generous — it errs towards including a file it is unsure about —
// because a pack that carries a stale texture is a bigger download, and a pack
// that drops a live one is a broken game.

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/game"
)

// Options is one pack.
type Options struct {
	// Src is the content directory to pack from: a project's resources/ — the
	// engine's own repository's, or that of the project an editor is running
	// from. It is staged as the install tree's resources/.
	Src string
	// Out is the install tree to write. Created, and emptied if it exists.
	Out string
	// Exe is the built release executable to copy in.
	Exe string
	// ExeName is what to call the executable in the tree. Empty means the
	// manifest's title, lowercased and hyphenated (defaultExeName).
	ExeName string
	// GOOS is the platform the pack is for, which decides which runtime
	// libraries belong beside the executable. Empty means this machine's.
	GOOS string
	// Prune copies only the assets reachable from the manifest.
	Prune bool
	// Manifest is game.yaml's path. Empty means the one in Src.
	Manifest string
	// Verbose names every file copied and every asset pruned.
	Verbose bool
	// Logf receives progress. Nil means log.Printf.
	Logf func(format string, args ...any)
}

// Stamp is what a pack records about itself in StampName, and what the
// installers downstream are built from.
type Stamp struct {
	Title   string
	Version string
	Exe     string
	DataDir string
}

// Result is what a pack made.
type Result struct {
	Stamp Stamp
	Files int
	Bytes int64
}

// Pack stages one install tree.
func Pack(o Options) (Result, error) {
	if o.Src == "" || o.Out == "" || o.Exe == "" {
		return Result{}, fmt.Errorf("a pack needs a content directory, an executable and an output directory")
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.Logf == nil {
		o.Logf = log.Printf
	}

	manifestPath := o.Manifest
	if manifestPath == "" {
		manifestPath = filepath.Join(o.Src, game.FileName)
	}
	m, err := game.Load(manifestPath)
	if err != nil {
		// Fatal even in the un-pruned mode, where nothing here needs the
		// manifest's contents: a pack whose manifest will not parse produces an
		// install that cannot boot, and finding that out now beats finding it
		// out from a player.
		return Result{}, err
	}

	p := &packer{src: o.Src, out: o.Out, goos: o.GOOS, verbose: o.Verbose, manifest: m, logf: o.Logf}
	return p.run(o.Exe, o.ExeName, o.Prune)
}

// alwaysDirs are copied whole, however the pack was invoked.
//
// Each is here because its contents are named from Go rather than from content:
// shaders by the string constants in internal/render and internal/gfx, fonts by
// a theme that may name any of them and by a fallback that names none. There is
// nothing for a walk over the content to find, so there is nothing to prune.
var alwaysDirs = []string{"shaders", "fonts"}

// contentDirs are the directories a prune walks. Order is display order.
//
// chunks/ is here because an entity definition may carry world geometry by
// naming a file in it (world/entity_chunk.go) — a ship's hull is reached from
// entities/shuttle.yaml, not from any level. The token scan follows that name
// like any other, and the chunk file is itself YAML, so the textures inside it
// come along transitively.
var contentDirs = []string{"levels", "chunks", "ui", "scripts", "entities", "models", "textures", "particles", "sounds", "video"}

type packer struct {
	src      string
	out      string
	goos     string
	verbose  bool
	manifest game.Manifest
	logf     func(format string, args ...any)

	copied int
	bytes  int64
}

func (p *packer) run(exePath, exeName string, prune bool) (Result, error) {
	if err := p.reset(); err != nil {
		return Result{}, err
	}

	// The executable first, so that a pack interrupted half way still looks
	// obviously unfinished rather than looking like a content-only install.
	if exeName == "" {
		exeName = defaultExeName(p.manifest, exePath)
	}
	if err := copyFile(exePath, filepath.Join(p.out, exeName), 0755); err != nil {
		return Result{}, fmt.Errorf("executable: %w", err)
	}
	p.logf("executable: %s", exeName)

	// Loose files at the top of the content tree: the manifest itself, and the
	// runtime libraries a platform needs beside the binary (SDL2.dll on
	// Windows). Copied by being there rather than by being listed, because what
	// ships with a game is a property of the project, not of this tool — except
	// for the ones that belong to one platform, which is what belongsOnPlatform
	// sorts out, and the ones that belong to the editor, which is what
	// IsDevelopmentFile does.
	if err := p.copyLooseFiles(); err != nil {
		return Result{}, err
	}

	for _, dir := range alwaysDirs {
		if err := p.copyDir(dir, nil); err != nil {
			return Result{}, err
		}
	}

	var keep map[string]bool
	if prune {
		idx, err := indexAssets(p.src, contentDirs)
		if err != nil {
			return Result{}, err
		}
		keep = reachable(p.src, idx, manifestRoots(p.manifest))
	}

	for _, dir := range contentDirs {
		if err := p.copyDir(dir, keep); err != nil {
			return Result{}, err
		}
	}

	// The stamp is written last, so a pack interrupted part way through does
	// not leave behind a directory this tool would later agree to empty.
	stamp := Stamp{
		Title:   p.manifest.Title,
		Version: p.manifest.Version,
		Exe:     exeName,
		DataDir: p.manifest.DataDirName(),
	}
	if err := p.writeStamp(stamp); err != nil {
		return Result{}, err
	}

	p.logf("packed %d files, %.1f MB, into %s", p.copied, float64(p.bytes)/(1024*1024), p.out)
	return Result{Stamp: stamp, Files: p.copied, Bytes: p.bytes}, nil
}

// StampName is the file that marks a directory as an install tree this tool
// made, and tells the installer scripts what is in it.
const StampName = ".foundrypack"

// writeStamp records what was packed, in a form a shell script can source.
//
// The installers downstream — the NSIS script on Windows, the .deb and the
// shell installer on Linux — all need the same four facts, and all of them
// would otherwise have to re-derive them from the manifest with their own
// half-copy of DataDirName and defaultExeName. Writing them once, from the code
// that decided them, is what keeps a title with an apostrophe in it from
// meaning one thing here and another in a shortcut.
func (p *packer) writeStamp(s Stamp) error {
	var b strings.Builder
	b.WriteString("# Written by foundrypack. Sourced by the installer scripts.\n")
	fmt.Fprintf(&b, "TITLE=%s\n", shellQuote(s.Title))
	fmt.Fprintf(&b, "VERSION=%s\n", shellQuote(s.Version))
	fmt.Fprintf(&b, "EXE=%s\n", shellQuote(s.Exe))
	fmt.Fprintf(&b, "DATA_DIR=%s\n", shellQuote(s.DataDir))
	return os.WriteFile(filepath.Join(p.out, StampName), []byte(b.String()), 0644)
}

// shellQuote wraps a value in single quotes so that a title containing spaces,
// or an apostrophe, survives being sourced.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// reset empties the output directory.
//
// Emptying rather than merging, because a stale file in an install tree is
// invisible: it does not conflict with anything and it does not fail to copy,
// it simply ships. The guards below are the price of doing that safely.
func (p *packer) reset() error {
	if err := CheckOutput(p.src, p.out); err != nil {
		return err
	}
	if err := refuseDangerousOutput(p.out); err != nil {
		return err
	}
	if err := os.RemoveAll(p.out); err != nil {
		return err
	}
	return os.MkdirAll(p.out, 0755)
}

// CheckOutput rejects an output directory that would take the content being
// packed down with it.
//
// A project holds its content in resources/, with a game.yaml inside, which is
// the very thing refuseDangerousOutput takes as proof that a directory is an old
// install tree — so pointing the output at the project, or at resources/ itself,
// would have emptied it. The same goes for any directory above the content, and
// for one inside a content directory, which a pack would copy into itself.
//
// Exported so a caller about to spend minutes compiling levels can find this
// out before it starts rather than after.
func CheckOutput(src, out string) error {
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if within(absSrc, absOut) {
		return fmt.Errorf("refusing to write to %q: the content being packed is inside it", absOut)
	}
	for _, dir := range append(append([]string{}, alwaysDirs...), contentDirs...) {
		if within(absOut, filepath.Join(absSrc, dir)) {
			return fmt.Errorf("refusing to write to %q: it is inside %s/, which is itself packed", absOut, dir)
		}
	}
	return nil
}

// within reports whether path is dir or somewhere below it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// refuseDangerousOutput rejects an output directory this tool has no business
// deleting.
//
// It deletes its output directory, and it is going to be run from Makefiles and
// CI with a path built by string concatenation, and from an editor with a path
// somebody typed. An empty or short path, a root, a repository, or a directory
// that does not look like something this tool made is worth refusing outright.
func refuseDangerousOutput(out string) error {
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if abs == string(filepath.Separator) || filepath.Dir(abs) == abs {
		return fmt.Errorf("refusing to write to %q: that is a filesystem root", abs)
	}
	if home, err := os.UserHomeDir(); err == nil && abs == home {
		return fmt.Errorf("refusing to write to %q: that is your home directory", abs)
	}
	// An existing directory is only emptied when it already looks like an
	// install tree — a stamp, a manifest, or nothing much at all.
	entries, err := os.ReadDir(abs)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// A repository is never an install tree, whatever else is in it. Checked
	// before the manifest, because a game's project directory has one of those
	// too.
	for _, e := range entries {
		if e.Name() == ".git" {
			return fmt.Errorf("refusing to empty %q: it is a repository, not an install tree", abs)
		}
	}
	// The stamp is written last, so a pack interrupted part way is recognised by
	// its manifest instead: in resources/, or at the top of a tree packed before
	// content moved into resources/.
	for _, e := range entries {
		if e.Name() == game.FileName || e.Name() == StampName {
			return nil
		}
	}
	if _, err := os.Stat(filepath.Join(abs, fileutil.ResourcesDirName, game.FileName)); err == nil {
		return nil
	}
	if len(entries) > 0 {
		return fmt.Errorf("refusing to empty %q: it has %d entries and no %s or %s, so it does not look like an install tree this tool made",
			abs, len(entries), StampName, game.FileName)
	}
	return nil
}

// defaultExeName is what the binary is called in the install tree.
//
// The game's title, lowercased and hyphenated, rather than "foundry": what the
// player sees in a task manager and in a shortcut's target should be the name
// of their game. The source's extension is kept, which is what carries .exe
// across from a Windows cross-build.
func defaultExeName(m game.Manifest, exePath string) string {
	ext := filepath.Ext(exePath)
	name := strings.ToLower(m.Title)
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r == ' ', r == '_', r == '-':
			return '-'
		}
		return -1
	}, name)
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	name = strings.Trim(name, "-")
	if name == "" {
		return filepath.Base(exePath)
	}
	return name + ext
}

// developmentFiles are what a development build writes into the directory it
// runs from, by name.
//
// They land beside the executable, outside resources/, so a pack that reads
// resources/ does not normally meet them. It still keeps them out by name
// wherever they turn up — a copied project, a layout from before content moved
// into resources/ — because shipping the editor's window layout in somebody's
// game is the kind of mistake nobody looks for.
var developmentFiles = map[string]bool{
	"imgui.ini":    true, // the editor's window layout
	"foundry.log":  true, // the development build's log (fileutil.StatePath)
	"release.yaml": true, // the Release window's settings (internal/release)
}

// IsDevelopmentFile reports whether a loose file at the top of a content tree
// belongs to the editor rather than the game.
//
// A *.bak is one wherever it is: fileutil.WriteFileAtomic leaves the previous
// version of every file it saves beside it, and a previous version is never
// what the game loads.
func IsDevelopmentFile(name string) bool {
	lower := strings.ToLower(name)
	return developmentFiles[lower] || strings.HasSuffix(lower, ".bak")
}

// copyLooseFiles copies the files sitting directly in the content root: into
// the tree's resources/, except for runtime libraries, which go beside the
// executable because that is where the platform's loader looks for them.
//
// A library is also taken from the directory holding the content root. That is
// the project directory, where a Windows editor needs SDL2.dll beside itself to
// run at all — so it is where a project keeps its only copy.
func (p *packer) copyLooseFiles() error {
	entries, err := os.ReadDir(p.src)
	if err != nil {
		return err
	}
	libs := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if IsDevelopmentFile(e.Name()) {
			p.logf("skipping %s: the editor's, not the game's", e.Name())
			continue
		}
		if !belongsOnPlatform(e.Name(), p.goos) {
			p.logf("skipping %s: not for %s", e.Name(), p.goos)
			continue
		}
		// The development binary itself, sitting in the project directory it
		// edits. Shipping it would ship the editor — the one thing a release
		// build exists to leave out — so any program that is not a platform
		// library is skipped, by what it is rather than by what it is called.
		if !isPlatformLib(e.Name()) && isNativeExecutable(filepath.Join(p.src, e.Name())) {
			p.logf("skipping %s: a program, and the only one a pack ships is the game's", e.Name())
			continue
		}
		if isPlatformLib(e.Name()) {
			libs[strings.ToLower(e.Name())] = true
			if err := p.copyTo(filepath.Join(p.src, e.Name()), e.Name()); err != nil {
				return err
			}
			continue
		}
		if err := p.copy(e.Name()); err != nil {
			return err
		}
	}
	return p.copyProjectLibs(libs)
}

// copyProjectLibs copies the runtime libraries for this platform from the
// directory holding the content root, beside the executable, skipping any the
// content root already supplied.
func (p *packer) copyProjectLibs(have map[string]bool) error {
	src, err := filepath.Abs(p.src)
	if err != nil {
		return err
	}
	project := filepath.Dir(src)
	entries, err := os.ReadDir(project)
	if err != nil {
		// Not the content, so not a reason to fail a pack; a missing library is
		// the platform's loader's to report.
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !isPlatformLib(name) || have[strings.ToLower(name)] || !belongsOnPlatform(name, p.goos) {
			continue
		}
		if err := p.copyTo(filepath.Join(project, name), name); err != nil {
			return err
		}
	}
	return nil
}

// platformLibExts maps a native library extension onto the one GOOS it is any
// use on.
//
// A content tree holds the runtime libraries for every platform the project
// ships to — resources/SDL2.dll sits beside the shaders whether or not the
// build in hand is for Windows. Shipping a Linux .deb with a DLL in it is not
// harmful, just wrong, and it is exactly the kind of wrong that nobody notices
// until a reviewer asks what it is doing there.
var platformLibExts = map[string]string{
	".dll":   "windows",
	".dylib": "darwin",
	".so":    "linux",
}

// belongsOnPlatform reports whether a loose file at the top of the content tree
// should be copied into a pack for goos.
//
// Only native libraries are filtered. Everything else — the manifest, a licence
// file, a README — belongs on every platform, and a rule that guessed otherwise
// would be a rule that silently dropped somebody's changelog.
func belongsOnPlatform(name, goos string) bool {
	want, isLib := platformLibExts[strings.ToLower(filepath.Ext(name))]
	return !isLib || want == goos
}

func isPlatformLib(name string) bool {
	_, ok := platformLibExts[strings.ToLower(filepath.Ext(name))]
	return ok
}

// isNativeExecutable reports whether a file is a compiled program for any
// platform the engine builds for, by its magic number rather than its name: a
// Linux build of the editor is called plain "foundry".
func isNativeExecutable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	switch string(magic[:]) {
	case "\x7fELF", // Linux
		"\xfe\xed\xfa\xce", "\xfe\xed\xfa\xcf", "\xce\xfa\xed\xfe", "\xcf\xfa\xed\xfe", "\xca\xfe\xba\xbe": // macOS
		return true
	}
	return string(magic[:2]) == "MZ" // Windows
}

// copyDir copies one directory. When keep is non-nil it is a prune, and only
// the slash-separated paths it contains are copied.
func (p *packer) copyDir(dir string, keep map[string]bool) error {
	root := filepath.Join(p.src, dir)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	}

	var dropped []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(p.src, path)
		if rerr != nil {
			return rerr
		}
		slash := filepath.ToSlash(rel)
		if IsDevelopmentFile(filepath.Base(slash)) {
			if p.verbose {
				p.logf("  skipped %s", slash)
			}
			return nil
		}
		if keep != nil && !keep[slash] && !alwaysKeep(slash) {
			dropped = append(dropped, slash)
			return nil
		}
		// An authored level whose compiled form is beside it, which is what the
		// game loads. Dropped whether or not this is a pruning pack: it is not
		// "unreachable", it is unreadable by the binary being shipped.
		if IsSupersededLevelSource(p.src, slash) {
			dropped = append(dropped, slash)
			return nil
		}
		return p.copy(rel)
	})
	if err != nil {
		return err
	}

	if len(dropped) > 0 {
		sort.Strings(dropped)
		// Named rather than counted when asked for, and always counted: a
		// prune that silently drops something is the one failure mode of this
		// tool that produces a broken game rather than a large one.
		p.logf("%s: pruned %d unreachable file(s)", dir, len(dropped))
		if p.verbose {
			for _, d := range dropped {
				p.logf("  pruned %s", d)
			}
		}
	}
	return nil
}

// alwaysKeep is the handful of files a prune must never drop: documentation
// that explains an otherwise-empty directory, and the READMEs that keep those
// directories in version control at all.
func alwaysKeep(slash string) bool {
	base := strings.ToLower(filepath.Base(slash))
	return base == "readme.md" || base == ".gitkeep"
}

// copy stages a file from the content root at the same path under the tree's
// resources/.
func (p *packer) copy(rel string) error {
	return p.copyTo(filepath.Join(p.src, rel), filepath.Join(fileutil.ResourcesDirName, rel))
}

// copyTo stages the file at from as to, a path relative to the top of the tree.
func (p *packer) copyTo(from, to string) error {
	info, err := os.Stat(from)
	if err != nil {
		return err
	}
	if err := copyFile(from, filepath.Join(p.out, to), info.Mode().Perm()); err != nil {
		return err
	}
	p.copied++
	p.bytes += info.Size()
	if p.verbose {
		p.logf("  %s", filepath.ToSlash(to))
	}
	return nil
}

func copyFile(from, to string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
