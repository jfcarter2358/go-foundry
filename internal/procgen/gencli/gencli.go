// Package gencli is `foundry gen`: the procedural generators from the
// command line, running exactly the code the editor's Generator window runs
// (procgen.Run, then genlevel's splice).
//
//	foundry gen run canyon                  run what is stale, splice into its level
//	foundry gen run canyon.fgen -force      rerun every stage
//	foundry gen run canyon -stage ground    just that stage (and stale upstream)
//	foundry gen run canyon -no-splice       leave the level alone
//	foundry gen check canyon                validate, and say what a run would run
//	foundry gen list                        stage kinds, their params, templates
//	foundry gen list -kind noise_preview    one kind in full
//	foundry gen new canyon -template noise_demo
//
// A document is named by its name (resources/generators/<name>.fgen) or by a
// path. Every command takes -root, the project directory.
//
// `run` splices into the target level's FILE, resolving `chunk: auto` against
// that level's chunks as the editor's splice does (fileSpliceOptions).
package gencli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"foundry/internal/cliflag"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/levelio"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	_ "foundry/internal/procgen/stages" // the built-in stage kinds
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// Command is the verb: `foundry gen`.
const Command = "gen"

// Main runs `foundry gen`. root is the project directory.
func Main(root string, args []string, out io.Writer) int {
	if len(args) == 0 {
		usage(out)
		return 2
	}
	switch args[0] {
	case "run":
		return runMain(root, args[1:], out)
	case "check":
		return checkMain(root, args[1:], out)
	case "list":
		return listMain(root, args[1:], out)
	case "new":
		return newMain(root, args[1:], out)
	}
	usage(out)
	return 2
}

func usage(out io.Writer) {
	fmt.Fprintf(out, `foundry %[1]s run <name|file.fgen> [-force] [-stage id]... [-no-splice] [-workers N]
foundry %[1]s check <name|file.fgen>
foundry %[1]s list [-kind name]
foundry %[1]s new <name> [-template name] [-force]
`, Command)
}

// stageList is a repeatable -stage flag.
type stageList []string

func (s *stageList) String() string     { return strings.Join(*s, ",") }
func (s *stageList) Set(v string) error { *s = append(*s, v); return nil }

// resolveDoc finds a document by path or by name.
func resolveDoc(arg string) string {
	if _, err := os.Stat(arg); err == nil {
		return arg
	}
	if !strings.HasSuffix(arg, procgen.Ext) {
		if p := procgen.DocPath(arg); fileExists(p) {
			return p
		}
	}
	if p := fileutil.ResolveAssetPath(arg); fileExists(p) {
		return p
	}
	return procgen.DocPath(strings.TrimSuffix(arg, procgen.Ext))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func parse(fs *flag.FlagSet, args []string) ([]string, int, bool) {
	rest, err := cliflag.Parse(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, 0, false
		}
		return nil, 2, false
	}
	return rest, 0, true
}

func runMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("gen run", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	force := fs.Bool("force", false, "rerun every stage, fresh or not")
	noSplice := fs.Bool("no-splice", false, "do not write placements into the target level")
	workers := fs.Int("workers", 0, "goroutines per stage; 0 is every core")
	quiet := fs.Bool("q", false, "print only the summary")
	var only stageList
	fs.Var(&only, "stage", "run only this stage (and the stale stages it needs); repeatable")
	rest, code, ok := parse(fs, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		usage(out)
		return 2
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)
	path := resolveDoc(rest[0])
	d, err := procgen.ReadDoc(path)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	opts := procgen.Options{Force: *force, Only: only, Workers: *workers}
	if !*quiet {
		opts.Log = func(line string) { fmt.Fprintln(out, line) }
	}
	res, err := procgen.Run(ctx, d, opts)
	if err != nil {
		fmt.Fprintf(out, "%s: %v\n", d.Name, err)
		return 1
	}
	fmt.Fprintf(out, "%s: ran %d, up to date %d, %d placements, %s -> %s\n", d.Name, len(res.Ran), len(res.Skipped),
		len(res.Placements())+len(res.Volumes()), res.Elapsed.Round(time.Millisecond), fileutil.RelAssetPath(res.OutDir))
	if d.Level == "" || *noSplice {
		return 0
	}
	lp := levelio.LevelPath(d.Level)
	sopt := fileSpliceOptions(lp, res.Placements(), out)
	sopt.Settings = res.LevelSettings()
	st, err := genlevel.SpliceFile(lp, d.Name, res.Placements(), res.Volumes(), sopt)
	if err != nil {
		fmt.Fprintf(out, "%s: splice into %s: %v\n", d.Name, d.Level, err)
		return 1
	}
	if st.Changed() {
		fmt.Fprintf(out, "%s: spliced into %s (%s)\n", d.Name, fileutil.RelAssetPath(lp), st)
	} else {
		fmt.Fprintf(out, "%s: %s already holds these placements\n", d.Name, fileutil.RelAssetPath(lp))
	}
	return 0
}

func checkMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("gen check", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	rest, code, ok := parse(fs, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		usage(out)
		return 2
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)
	d, err := procgen.ReadDoc(resolveDoc(rest[0]))
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if probs := d.Validate(); len(probs) > 0 {
		for _, p := range probs {
			fmt.Fprintf(out, "%s: %s\n", d.Name, p)
		}
		return 1
	}
	stale, err := procgen.StaleStages(d, procgen.Options{})
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if len(stale) == 0 {
		fmt.Fprintf(out, "%s: ok, every stage up to date\n", d.Name)
	} else {
		fmt.Fprintf(out, "%s: ok; a run would run %s\n", d.Name, strings.Join(stale, ", "))
	}
	return 0
}

func listMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("gen list", flag.ContinueOnError)
	fs.SetOutput(out)
	kind := fs.String("kind", "", "show one stage kind")
	_, code, ok := parse(fs, args)
	if !ok {
		return code
	}
	_ = root
	if *kind != "" {
		k := procgen.KindByName(*kind)
		if k == nil {
			fmt.Fprintf(out, "no stage kind %q\n", *kind)
			return 1
		}
		fmt.Fprint(out, procgen.KindReference(k))
		return 0
	}
	fmt.Fprintln(out, "Stage kinds:")
	for _, k := range procgen.Kinds() {
		fmt.Fprintf(out, "  %-16s %-10s %s\n", k.Name, k.Category, firstSentence(k.Doc))
	}
	fmt.Fprintln(out, "\nTemplates:")
	for _, t := range procgen.Templates() {
		fmt.Fprintf(out, "  %-16s %s\n", t.Name, firstSentence(t.Doc))
	}
	fmt.Fprintf(out, "\n`foundry %s list -kind <name>` for a kind's parameters.\n", Command)
	return 0
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

func newMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("gen new", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	template := fs.String("template", procgen.BlankTemplate, "the template to start from (gen list shows them)")
	force := fs.Bool("force", false, "overwrite a generator that already exists")
	rest, code, ok := parse(fs, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		usage(out)
		return 2
	}
	name := strings.TrimSuffix(filepath.Base(rest[0]), procgen.Ext)
	if !procgen.ValidDocName(name) {
		fmt.Fprintf(out, "%q: a generator's name is lower-case letters, digits and underscores, starting with a letter\n", name)
		return 2
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)
	d, err := procgen.NewFromTemplate(*template, name)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	path := procgen.DocPath(name)
	if fileExists(path) && !*force {
		fmt.Fprintf(out, "%s exists (-force to overwrite)\n", fileutil.RelAssetPath(path))
		return 1
	}
	if err := procgen.WriteDoc(path, d); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintf(out, "wrote %s (template %s)\n", fileutil.RelAssetPath(path), *template)
	return 0
}

// fileSpliceOptions resolves `chunk: auto` for a splice into the level file
// at path, as the editor's splice does against the open level: each such
// placement takes the chunk whose footprint it stands in
// (world.SceneryChunkAt). Only the level's chunks are built (no uploads: a
// CLI has no GL context), and only when some placement asks.
func fileSpliceOptions(path string, placements []procgen.Placement, out io.Writer) genlevel.Options {
	auto := false
	for _, p := range placements {
		auto = auto || p.Chunk == procgen.ChunkAuto
	}
	if !auto {
		return genlevel.Options{}
	}
	geo, tex := gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled
	gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = false, false
	chunks, err := levelio.LoadChunks(path)
	gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = geo, tex
	if err != nil {
		fmt.Fprintf(out, "chunk: auto left unresolved: %v\n", err)
		return genlevel.Options{}
	}
	if len(chunks) == 0 {
		return genlevel.Options{}
	}
	lvl := &world.Level{Chunks: chunks}
	return genlevel.Options{ChunkAt: func(x, y, z float32) string {
		return world.SceneryChunkAt(lvl, mgl32.Vec3{x, y, z})
	}}
}
