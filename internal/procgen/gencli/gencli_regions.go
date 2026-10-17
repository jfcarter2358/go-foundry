package gencli

// gencli_regions.go — the environment-regions commands (docs/regions/SPEC.md):
//
//	foundry gen world <level>          create resources/generators/<level>_world.fgen
//	                                   (the level_world template, its regions synced
//	                                   from the level), or say it exists; writes the
//	                                   built-in environment presets that have no file
//	foundry gen env list               every environment preset: built-in, file or both
//	foundry gen env write <name>       write a built-in preset to
//	foundry gen env write -all         resources/environments/<name>.fgen — only when
//	                                   no such file exists; an edited preset is never
//	                                   overwritten
//	foundry gen env adjacency          the effective adjacency table: the built-in
//	                                   rules with resources/environments/adjacency.yaml
//	                                   laid over them, as a table and as YAML in the
//	                                   override file's form
//
// And syncRegions, which `run` and `check` call: a document with a regions
// stage takes its target level's region volumes first.

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/levelio"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/stages"
	"foundry/internal/world"
)

// syncRegions copies d's target level's region volumes into its regions
// stage (if it has one), writing the document back to path when write is set
// and they changed. Non-zero on failure.
func syncRegions(d *procgen.Doc, path string, write bool, out io.Writer) int {
	if d.Level == "" || genlevel.RegionsStage(d) == nil {
		return 0
	}
	lp := levelio.LevelPath(d.Level)
	// A template's region layout goes into a level that has none first
	// (genlevel/regions_seed.go); a check only says so.
	seeded := genlevel.WantsSeed(d)
	if seeded && write {
		ids, err := genlevel.SeedLevelFile(d, lp)
		if err != nil {
			fmt.Fprintf(out, "%s: write the template's regions into %s: %v\n", d.Name, d.Level, err)
			return 1
		}
		if len(ids) > 0 {
			fmt.Fprintf(out, "%s: wrote %d region volume(s) into level %s: %s\n", d.Name, len(ids), d.Level, strings.Join(ids, ", "))
		}
	} else if seeded {
		// In memory only, exactly as a run would, so what the check plans
		// hashes as the run will.
		raw, err := genlevel.ReadRawLevel(lp)
		if err != nil {
			raw = world.RawLevel{}
		}
		ids, err := genlevel.SeedLevelRegions(d, &raw)
		if err != nil {
			fmt.Fprintf(out, "%s: the template's regions: %v\n", d.Name, err)
			return 1
		}
		if len(ids) > 0 {
			fmt.Fprintf(out, "%s: level %s has no region volumes; a run writes this template's %d region(s) into it (%s)\n",
				d.Name, d.Level, len(ids), strings.Join(ids, ", "))
			if _, err := genlevel.SyncRegions(d, &raw); err != nil {
				fmt.Fprintf(out, "%s: %v\n", d.Name, err)
				return 1
			}
			return 0
		}
	}
	changed, err := genlevel.SyncRegionsFromFile(d, lp)
	if err != nil {
		fmt.Fprintf(out, "%s: sync regions from %s: %v\n", d.Name, d.Level, err)
		return 1
	}
	if !changed && !(seeded && write) {
		return 0
	}
	n := len(genlevel.RegionsStage(d).Params.Items("regions"))
	if !write {
		fmt.Fprintf(out, "%s: the level's %d region(s) differ from the document's; a run syncs them\n", d.Name, n)
		return 0
	}
	if err := procgen.WriteDoc(path, d); err != nil {
		fmt.Fprintf(out, "%s: %v\n", d.Name, err)
		return 1
	}
	fmt.Fprintf(out, "%s: synced %d region(s) from level %s\n", d.Name, n, d.Level)
	return 0
}

func worldMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("gen world", flag.ContinueOnError)
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
	level := strings.TrimSuffix(rest[0], ".yaml")
	path, created, err := EnsureWorldDoc(level)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if !created {
		fmt.Fprintf(out, "%s exists; open it in the Generator, or `foundry gen run %s`\n",
			fileutil.RelAssetPath(path), procgen.NameFromPath(path))
		return 0
	}
	fmt.Fprintf(out, "wrote %s (template %s, level %s)\n", fileutil.RelAssetPath(path), stages.LevelWorldTemplate, level)
	written, err := procgen.WriteEnvironments()
	for _, p := range written {
		fmt.Fprintf(out, "wrote %s\n", fileutil.RelAssetPath(p))
	}
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	return 0
}

// EnsureWorldDoc makes level's level generation doc when it does not exist —
// the level_world template, targeting level, its regions synced from the
// level file — and returns its path; created is false for one already there
// (left untouched). The editor's "Generate world…" shares it.
func EnsureWorldDoc(level string) (path string, created bool, err error) {
	name := stages.LevelWorldDocName(level)
	path = procgen.DocPath(name)
	if fileExists(path) {
		return path, false, nil
	}
	d, err := procgen.NewFromTemplate(stages.LevelWorldTemplate, name)
	if err != nil {
		return path, false, err
	}
	d.Level = level
	if _, err := genlevel.SyncRegionsFromFile(d, levelio.LevelPath(level)); err != nil {
		return path, false, err
	}
	if err := procgen.WriteDoc(path, d); err != nil {
		return path, false, err
	}
	return path, true, nil
}

func envMain(root string, args []string, out io.Writer) int {
	if len(args) == 0 {
		usage(out)
		return 2
	}
	verb := args[0]
	fs := flag.NewFlagSet("gen env "+verb, flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	all := fs.Bool("all", false, "write every built-in preset that has no file")
	rest, code, ok := parse(fs, args[1:])
	if !ok {
		return code
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)
	switch verb {
	case "list":
		fmt.Fprintf(out, "environments (%d): name, label, overlay colour, source\n", len(procgen.EnvironmentNames()))
		for _, name := range procgen.EnvironmentNames() {
			e := procgen.EnvironmentByName(name)
			file := fileExists(procgen.EnvironmentPath(name))
			src := "file"
			switch {
			case e != nil && file:
				src = "file (edited copy of the built-in)"
			case e != nil:
				src = "built-in (no file yet)"
			}
			c := procgen.EnvironmentColor(name)
			label := name
			if e != nil {
				label = e.Label
			}
			fmt.Fprintf(out, "  %-18s %-18s %s  %s\n", name, label, hexColor(c), src)
		}
		return 0
	case "adjacency":
		return adjacencyMain(out)
	case "write":
		var names []string
		if *all {
			for _, e := range procgen.Environments() {
				names = append(names, e.Name)
			}
		} else if len(rest) == 1 {
			names = rest
		} else {
			usage(out)
			return 2
		}
		for _, n := range names {
			p, wrote, err := procgen.WriteEnvironment(n)
			if err != nil {
				fmt.Fprintln(out, err)
				return 1
			}
			if wrote {
				fmt.Fprintf(out, "wrote %s\n", fileutil.RelAssetPath(p))
			} else {
				fmt.Fprintf(out, "%s exists; left as it is (presets are never overwritten)\n", fileutil.RelAssetPath(p))
			}
		}
		return 0
	}
	usage(out)
	return 2
}

// adjacencyMain prints the effective adjacency table.
func adjacencyMain(out io.Writer) int {
	adj, err := stages.ProjectAdjacency()
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	src := "built-in only (no " + fileutil.RelAssetPath(stages.AdjacencyPath()) + ")"
	if fileExists(stages.AdjacencyPath()) {
		src = "built-in + " + fileutil.RelAssetPath(stages.AdjacencyPath())
	}
	fmt.Fprintf(out, "adjacency table: %s\n", src)
	for _, p := range adj.Pairs() {
		r, _ := adj.Rule(p)
		line := fmt.Sprintf("  %-18s %-18s %s", p.A, p.B, r.Describe())
		if r.Note != "" {
			line += "  — " + r.Note
		}
		fmt.Fprintln(out, line)
	}
	fmt.Fprintf(out, "unlisted pairs: allowed, each region's own transition (default %g m)\n\n", regions.DefaultTransition)
	data, err := adj.Encode()
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintf(out, "# as %s:\n%s", regions.AdjacencyFileName, data)
	return 0
}
