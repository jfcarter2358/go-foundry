package main

// foundrypack — stage a release build into an install tree.
//
// A Foundry install is an executable with the content directories beside it:
// every asset path in the engine resolves against the executable's own
// directory (fileutil.ExecutablePath), so "packaging" is mostly a matter of
// putting the right files next to the right binary. This tool is what decides
// which files those are.
//
// Two modes, and the difference between them is the whole point:
//
//	-prune=false  copy the content tree wholesale. What `make linux` has always
//	              done, and the right answer while a project is being built.
//	-prune=true   copy only what the game can actually reach from its manifest.
//	              A repository's resources/ accumulates: half-finished levels,
//	              textures nothing samples any more, the model somebody was
//	              trying out in March. None of it should ship, and none of it
//	              should have to be deleted by hand to not ship.
//
// The pruning walk is in refs.go. What is worth knowing here is that it is
// deliberately generous — it errs towards including a file it is unsure about —
// because a pack that carries a stale texture is a bigger download, and a pack
// that drops a live one is a broken game.

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"foundry/internal/game"
)

func main() {
	var (
		src      = flag.String("src", "resources", "content directory to pack from")
		out      = flag.String("out", "", "install tree to write (required; created, and emptied if it exists)")
		exe      = flag.String("exe", "", "built game executable to copy in (required)")
		exeName  = flag.String("exe-name", "", "name to give the executable in the install tree (default: the manifest title, or the source name)")
		goos     = flag.String("goos", runtime.GOOS, "the platform this pack is for; decides which runtime libraries belong beside the executable")
		prune    = flag.Bool("prune", false, "copy only the assets reachable from the manifest")
		manifest = flag.String("manifest", "", "path to game.yaml (default: <src>/game.yaml)")
		verbose  = flag.Bool("v", false, "list every file copied, and every asset pruned")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "foundrypack — stage a Foundry release into an install tree.\n\nUsage:\n  foundrypack -exe build/foundry -out build/install [-prune]\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("foundrypack: ")

	if *out == "" || *exe == "" {
		flag.Usage()
		os.Exit(2)
	}

	manifestPath := *manifest
	if manifestPath == "" {
		manifestPath = filepath.Join(*src, game.FileName)
	}
	m, err := game.Load(manifestPath)
	if err != nil {
		// Fatal even in the un-pruned mode, where nothing here needs the
		// manifest's contents: a pack whose manifest will not parse produces an
		// install that cannot boot, and finding that out now beats finding it
		// out from a player.
		log.Fatalf("%v", err)
	}

	p := &packer{src: *src, out: *out, goos: *goos, verbose: *verbose, manifest: m}
	if err := p.run(*exe, *exeName, *prune); err != nil {
		log.Fatalf("%v", err)
	}
}

// alwaysDirs are copied whole, however the pack was invoked.
//
// Each is here because its contents are named from Go rather than from content:
// shaders by the string constants in internal/render and internal/gfx, detail
// presets by a directory scan at startup, fonts by a theme that may name any of
// them and by a fallback that names none. There is nothing for a walk over the
// content to find, so there is nothing to prune.
var alwaysDirs = []string{"shaders", "fonts", "detail_presets"}

// contentDirs are the directories a prune walks. Order is display order.
var contentDirs = []string{"levels", "ui", "scripts", "entities", "models", "textures", "particles", "sounds"}

type packer struct {
	src      string
	out      string
	goos     string
	verbose  bool
	manifest game.Manifest

	copied int
	bytes  int64
}

func (p *packer) run(exePath, exeName string, prune bool) error {
	if err := p.reset(); err != nil {
		return err
	}

	// The executable first, so that a pack interrupted half way still looks
	// obviously unfinished rather than looking like a content-only install.
	if exeName == "" {
		exeName = defaultExeName(p.manifest, exePath)
	}
	if err := copyFile(exePath, filepath.Join(p.out, exeName), 0755); err != nil {
		return fmt.Errorf("executable: %w", err)
	}
	log.Printf("executable: %s", exeName)

	// Loose files at the top of the content tree: the manifest itself, and the
	// runtime libraries a platform needs beside the binary (SDL2.dll on
	// Windows). Copied by being there rather than by being listed, because what
	// belongs beside the executable is a property of the project, not of this
	// tool — except for the ones that belong to one platform, which is what
	// belongsOnPlatform sorts out.
	if err := p.copyLooseFiles(); err != nil {
		return err
	}

	for _, dir := range alwaysDirs {
		if err := p.copyDir(dir, nil); err != nil {
			return err
		}
	}

	var keep map[string]bool
	if prune {
		idx, err := indexAssets(p.src, contentDirs)
		if err != nil {
			return err
		}
		keep = reachable(p.src, idx, manifestRoots(p.manifest))
	}

	for _, dir := range contentDirs {
		if err := p.copyDir(dir, keep); err != nil {
			return err
		}
	}

	// The stamp is written last, so a pack interrupted part way through does
	// not leave behind a directory this tool would later agree to empty.
	if err := p.writeStamp(exeName); err != nil {
		return err
	}

	log.Printf("packed %d files, %.1f MB, into %s", p.copied, float64(p.bytes)/(1024*1024), p.out)
	return nil
}

// stampName is the file that marks a directory as an install tree this tool
// made, and tells the installer scripts what is in it.
const stampName = ".foundrypack"

// writeStamp records what was packed, in a form a shell script can source.
//
// The installers downstream — the NSIS script on Windows, the .deb and the
// shell installer on Linux — all need the same four facts, and all of them
// would otherwise have to re-derive them from the manifest with their own
// half-copy of DataDirName and defaultExeName. Writing them once, from the code
// that decided them, is what keeps a title with an apostrophe in it from
// meaning one thing here and another in a shortcut.
func (p *packer) writeStamp(exeName string) error {
	var b strings.Builder
	b.WriteString("# Written by foundrypack. Sourced by the installer scripts.\n")
	fmt.Fprintf(&b, "TITLE=%s\n", shellQuote(p.manifest.Title))
	fmt.Fprintf(&b, "VERSION=%s\n", shellQuote(p.manifest.Version))
	fmt.Fprintf(&b, "EXE=%s\n", shellQuote(exeName))
	fmt.Fprintf(&b, "DATA_DIR=%s\n", shellQuote(p.manifest.DataDirName()))
	return os.WriteFile(filepath.Join(p.out, stampName), []byte(b.String()), 0644)
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
// it simply ships. The guard below is the price of doing that safely.
func (p *packer) reset() error {
	if err := refuseDangerousOutput(p.out); err != nil {
		return err
	}
	if err := os.RemoveAll(p.out); err != nil {
		return err
	}
	return os.MkdirAll(p.out, 0755)
}

// refuseDangerousOutput rejects an -out that this tool has no business deleting.
//
// It deletes its output directory, and it is going to be run from Makefiles and
// CI with a path built by string concatenation. An empty or short path, a root,
// or a directory that does not look like something this tool made is worth
// refusing outright.
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
	// install tree — an executable and a manifest, or nothing much at all.
	entries, err := os.ReadDir(abs)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == game.FileName || e.Name() == stampName {
			return nil
		}
	}
	if len(entries) > 0 {
		return fmt.Errorf("refusing to empty %q: it has %d entries and no %s, so it does not look like an install tree this tool made",
			abs, len(entries), game.FileName)
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

// copyLooseFiles copies the files sitting directly in the content root.
func (p *packer) copyLooseFiles() error {
	entries, err := os.ReadDir(p.src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !belongsOnPlatform(e.Name(), p.goos) {
			log.Printf("skipping %s: not for %s", e.Name(), p.goos)
			continue
		}
		if err := p.copy(e.Name()); err != nil {
			return err
		}
	}
	return nil
}

// copyDir copies one directory. When keep is non-nil it is a prune, and only
// the slash-separated paths it contains are copied.
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
		if keep != nil && !keep[slash] && !alwaysKeep(slash) {
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
		log.Printf("%s: pruned %d unreachable file(s)", dir, len(dropped))
		if p.verbose {
			for _, d := range dropped {
				log.Printf("  pruned %s", d)
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

func (p *packer) copy(rel string) error {
	from := filepath.Join(p.src, rel)
	to := filepath.Join(p.out, rel)
	info, err := os.Stat(from)
	if err != nil {
		return err
	}
	if err := copyFile(from, to, info.Mode().Perm()); err != nil {
		return err
	}
	p.copied++
	p.bytes += info.Size()
	if p.verbose {
		log.Printf("  %s", filepath.ToSlash(rel))
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
