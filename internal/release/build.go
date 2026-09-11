package release

// build.go — a release of a project directory, in one call.
//
// What `make release-linux` and `make release-windows` do, for any project
// rather than only the engine's own resources/: compile every level, get a game
// binary for each platform, stage the content around it, and archive the
// result. Run by the editor's Release window, as a child process (job.go), and
// by `foundry build-release` in a terminal.
//
// The project is wherever the development binary is running from, because its
// resources/ is where every asset path in the engine already resolves
// (fileutil.ResourcesPath) — the directory the editor saves levels into is by
// construction the directory a release of them is built from.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"foundry/internal/compile"
	"foundry/internal/fileutil"
	"foundry/internal/game"
	"foundry/internal/pack"
	"foundry/internal/setup"
)

// Command is the first argument that turns a development binary into the
// release builder instead of the editor.
const Command = "build-release"

// The prefixes a build's output lines start with. The Release window colours
// lines by them and reads the files a build made back out of them.
const (
	StepPrefix     = "==> "
	WarningPrefix  = "WARNING: "
	ArtifactPrefix = "artifact: "
	FailedPrefix   = "FAILED: "
)

// Options is one build.
type Options struct {
	Root    string // the project directory: the one holding resources/
	Config  Config
	Verbose bool      // name every file packed and pruned
	Out     io.Writer // progress; nil discards it
}

// Build builds a release and returns the paths of the files it made, which on
// failure are the ones it made before failing.
func Build(ctx context.Context, o Options) ([]string, error) {
	b := &builder{ctx: ctx, cfg: o.Config, verbose: o.Verbose, out: o.Out}
	if b.out == nil {
		b.out = io.Discard
	}
	return b.run(o.Root)
}

type builder struct {
	ctx     context.Context
	cfg     Config
	verbose bool
	out     io.Writer

	root      string
	resources string // root's resources/: the content that is compiled and packed
	outDir    string
	source    string
	artifacts []string
}

func (b *builder) logf(format string, args ...any) { fmt.Fprintf(b.out, format+"\n", args...) }
func (b *builder) step(format string, args ...any) { b.logf(StepPrefix+format, args...) }
func (b *builder) warn(format string, args ...any) { b.logf(WarningPrefix+format, args...) }

func (b *builder) artifact(path string) {
	b.artifacts = append(b.artifacts, path)
	b.logf(ArtifactPrefix+"%s", path)
}

func (b *builder) run(root string) ([]string, error) {
	var err error
	if b.root, err = filepath.Abs(root); err != nil {
		return nil, err
	}
	b.resources = fileutil.ResourcesDir(b.root)
	if err := b.cfg.Validate(); err != nil {
		return nil, err
	}
	targets, _ := b.cfg.Targets()

	// Everything that can be wrong without compiling anything is checked before
	// compiling anything. Compiling is the slow part, and a missing game binary
	// reported after it is ten minutes spent finding out something a stat
	// would have said.
	m, err := game.Load(game.ProjectPath(b.root))
	if err != nil {
		return nil, err
	}
	b.outDir = b.cfg.OutputDir(b.root)
	if err := pack.CheckOutput(b.resources, b.outDir); err != nil {
		return nil, err
	}
	if b.source, err = FindSource(b.root, b.cfg.Source); err != nil {
		return nil, err
	}
	if b.source == "" {
		for _, p := range targets {
			if _, err := os.Stat(PrebuiltPlayer(b.root, p)); err != nil {
				return nil, missingPlayer(b.root, p)
			}
			if p.GOOS == "windows" {
				if _, err := os.Stat(PrebuiltSetupStub(b.root)); err != nil {
					return nil, missingSetupStub(b.root)
				}
			}
		}
	}

	labels := make([]string, len(targets))
	for i, p := range targets {
		labels[i] = p.Label
	}
	b.step("Building %s %s for %s", m.Title, m.Version, strings.Join(labels, " and "))
	b.logf("project:     %s", b.root)
	b.logf("output:      %s", b.outDir)
	if b.source != "" {
		b.logf("game binary: built from %s", b.source)
	} else {
		b.logf("game binary: prebuilt, from %s", filepath.Join(b.root, RuntimesDirName))
	}

	// Levels, from disk. What the editor has open and has not saved is not in
	// the build; the Release window says so beside its Build button.
	b.step("Compiling levels")
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = b.resources
	defer func() { fileutil.ResourcesPath = prev }()
	opt := compile.Options{Light: compile.DefaultLightOptions()}
	if failed := CompileLevels(nil, opt, !b.cfg.AllowLeaks, false, b.logf); failed > 0 {
		if !b.cfg.AllowLeaks {
			return nil, fmt.Errorf("%d level(s) failed to compile (a leak is a failure unless leaks are allowed)", failed)
		}
		return nil, fmt.Errorf("%d level(s) failed to compile", failed)
	}

	for _, p := range targets {
		if err := b.ctx.Err(); err != nil {
			return b.artifacts, err
		}
		if err := b.platform(p); err != nil {
			return b.artifacts, fmt.Errorf("%s: %w", p.Label, err)
		}
	}
	return b.artifacts, nil
}

// platform builds everything for one target.
func (b *builder) platform(p Platform) error {
	b.step("%s: game binary", p.Label)
	exe, err := b.player(p)
	if err != nil {
		return err
	}

	b.step("%s: staging", p.Label)
	stage := filepath.Join(b.outDir, "install-"+p.GOOS)
	res, err := pack.Pack(pack.Options{
		Src:     b.resources,
		Out:     stage,
		Exe:     exe,
		GOOS:    p.GOOS,
		Prune:   b.cfg.Prune,
		Verbose: b.verbose,
		Logf:    b.logf,
	})
	if err != nil {
		return err
	}

	// Named as the Makefile names them, so a release is the same files
	// whichever way it was built.
	slug := strings.TrimSuffix(res.Stamp.Exe, p.ExeExt())
	version := res.Stamp.Version
	if version == "" {
		version = game.Default().Version
	}
	base := slug + "-" + version

	b.step("%s: archiving", p.Label)
	switch p.GOOS {
	case "linux":
		// install.sh rides inside the tarball, for everywhere dpkg is not.
		if err := pack.WriteLinuxInstallScript(stage); err != nil {
			return err
		}
		archive := filepath.Join(b.outDir, base+"-"+p.String()+".tar.gz")
		if err := pack.TarGz(stage, archive, base, res.Stamp.Exe); err != nil {
			return err
		}
		b.artifact(archive)
		return b.installer(p, ".deb", pack.DebAvailable(), filepath.Join(b.outDir, base+"-"+p.GOARCH+".deb"),
			func(out string) error { return pack.Deb(b.ctx, stage, out, b.out) })
	case "windows":
		archive := filepath.Join(b.outDir, base+"-"+p.String()+".zip")
		if err := pack.Zip(stage, archive, base, res.Stamp.Exe); err != nil {
			return err
		}
		b.artifact(archive)
		// The installer, always: Foundry's own, which needs no tool on this
		// machine to make — see internal/setup.
		stub, err := b.setupStub()
		if err != nil {
			return err
		}
		b.step("%s: Setup.exe", p.Label)
		out := filepath.Join(b.outDir, base+"-setup.exe")
		if err := setup.Write(stub, stage, out); err != nil {
			return err
		}
		b.artifact(out)
	default:
		return fmt.Errorf("no archive format for %s", p.GOOS)
	}
	return nil
}

// installer builds one installer that needs a tool, or says why it could not.
//
// A missing tool is a warning rather than a failure: the archive beside it is
// already a complete release — a Linux tarball carries install.sh — and a
// build that threw that away because dpkg-deb is not installed would be worse
// than one that said so.
func (b *builder) installer(p Platform, what string, unavailable error, out string, build func(out string) error) error {
	if unavailable != nil {
		b.warn("%s: no %s — %v (the archive's install.sh still installs it)", p.Label, what, unavailable)
		return nil
	}
	b.step("%s: %s", p.Label, what)
	if err := build(out); err != nil {
		return err
	}
	b.artifact(out)
	return nil
}

// player is the game binary for a platform: built from source when there is
// source to build it from, the prebuilt one otherwise, and checked either way.
func (b *builder) player(p Platform) (string, error) {
	path := PrebuiltPlayer(b.root, p)
	if b.source != "" {
		path = filepath.Join(b.outDir, PlayerFileName(p))
		b.logf("go build -tags release, in %s", b.source)
		if err := BuildPlayer(b.ctx, b.source, p, path, b.out); err != nil {
			return "", err
		}
	} else if _, err := os.Stat(path); err != nil {
		return "", missingPlayer(b.root, p)
	}
	warning, err := CheckPlayer(path, p)
	if err != nil {
		return "", err
	}
	if warning != "" {
		b.warn("%s", warning)
	}
	b.logf("using %s", path)
	return path, nil
}

// setupStub is the Windows installer program to append the install tree to:
// built from source when there is source, the prebuilt one otherwise, and
// checked either way.
func (b *builder) setupStub() (string, error) {
	path := PrebuiltSetupStub(b.root)
	if b.source != "" {
		path = filepath.Join(b.outDir, SetupStubFileName)
		if err := BuildSetupStub(b.ctx, b.source, path, b.out); err != nil {
			return "", err
		}
	} else if _, err := os.Stat(path); err != nil {
		return "", missingSetupStub(b.root)
	}
	if err := CheckSetupStub(path); err != nil {
		return "", err
	}
	return path, nil
}

func missingSetupStub(root string) error {
	return fmt.Errorf("no installer program for Windows: expected %s — build it from a Foundry checkout with `make runtime-windows RUNTIMES_DIR=%s`, or set a Foundry source directory to build it from",
		PrebuiltSetupStub(root), filepath.Join(root, RuntimesDirName))
}

func missingPlayer(root string, p Platform) error {
	return fmt.Errorf("no game binary for %s: expected %s — build one from a Foundry checkout with `make runtime-%s RUNTIMES_DIR=%s`, or set a Foundry source directory to build it from",
		p.Label, PrebuiltPlayer(root, p), p.Name, filepath.Join(root, RuntimesDirName))
}

// Main is `foundry build-release`: a release of the project at root, from a
// terminal. It returns the process's exit code.
//
// A setting not given on the command line comes from the project's
// release.yaml, so a terminal build does what the Release window last did.
func Main(root string, args []string) int {
	fs := flag.NewFlagSet(Command, flag.ContinueOnError)
	project := fs.String("root", root, "project directory: the one holding resources/")
	platforms := fs.String("platforms", "", "platforms to build, comma-separated: "+platformNames())
	out := fs.String("out", "", "output directory, relative to the project")
	prune := fs.Bool("prune", true, "ship only the assets the game can reach from game.yaml")
	// Accepted and ignored, so a script written when installers were optional
	// still runs.
	fs.Bool("installers", true, "no longer needed: installers are always built")
	allowLeaks := fs.Bool("allow-leaks", false, "ship a level that leaks rather than failing the build")
	source := fs.String("source", "", "Foundry checkout to build the game binary from, instead of "+RuntimesDirName+"/")
	verbose := fs.Bool("v", false, "name every file packed and pruned")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "foundry %s — build a release of this project.\n\n"+
			"Settings not given here come from %s in the project, which the editor's\n"+
			"Release window writes.\n\nFlags:\n", Command, ConfigFileName)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}

	// Anything the engine's own packages log while compiling belongs in the
	// same stream as the build's progress, and without timestamps.
	log.SetFlags(0)
	log.SetOutput(os.Stdout)

	cfg, err := LoadConfig(*project)
	if err != nil {
		fmt.Println(FailedPrefix + err.Error())
		return 1
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "platforms":
			cfg.Platforms = strings.FieldsFunc(*platforms, func(r rune) bool { return r == ',' || r == ' ' })
		case "out":
			cfg.Output = *out
		case "prune":
			cfg.Prune = *prune
		case "allow-leaks":
			cfg.AllowLeaks = *allowLeaks
		case "source":
			cfg.Source = *source
		}
	})

	start := time.Now()
	artifacts, err := Build(context.Background(), Options{Root: *project, Config: cfg, Verbose: *verbose, Out: os.Stdout})
	if err != nil {
		fmt.Println(FailedPrefix + err.Error())
		return 1
	}
	fmt.Printf("%sBuilt %d file(s) in %s\n", StepPrefix, len(artifacts), time.Since(start).Round(time.Second))
	return 0
}

// Args is the command line that builds c for the project at root — what the
// Release window runs. Every setting is passed, so the build does what the
// window showed whatever release.yaml says.
func Args(root string, c Config) []string {
	return []string{
		Command,
		"-root", root,
		"-platforms", strings.Join(c.Platforms, ","),
		"-out", c.Output,
		"-prune=" + strconv.FormatBool(c.Prune),
		"-allow-leaks=" + strconv.FormatBool(c.AllowLeaks),
		"-source", c.Source,
	}
}
