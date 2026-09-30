package harness

// cmd_biomes.go — `foundry biomes`, the terrain biome files (world/foliage_file.go).
//
//	foundry biomes list                every biome and where it comes from
//	foundry biomes check [name...]     parse the files and check their textures
//	foundry biomes export [name...]    write the built-ins out as files to edit
//	foundry biomes schema              the file format
//
// `check` exits non-zero on any problem, so a generator can gate on it.
// `export` will not overwrite a file that is already there unless told to
// (-force): the file is the project's copy and may already have been edited.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"foundry/internal/cliflag"
	"foundry/internal/fileutil"
	"foundry/internal/world"
)

// BiomesCommand is the verb: `foundry biomes`.
const BiomesCommand = "biomes"

// BiomesMain runs `foundry biomes <subcommand>`.
func BiomesMain(root string, args []string, out io.Writer) int {
	if len(args) == 0 {
		biomesUsage(out)
		return 2
	}
	sub := args[0]
	if sub == "schema" {
		fmt.Fprint(out, world.BiomeSchemaText())
		return 0
	}
	if sub != "list" && sub != "check" && sub != "export" {
		biomesUsage(out)
		return 2
	}
	fs := flag.NewFlagSet("biomes "+sub, flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	res := fs.String("resources", "", "the resources/ directory itself, if it is not <root>/resources")
	force := fs.Bool("force", false, "export: overwrite files that already exist")
	names, err := cliflag.Parse(fs, args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dir := *res
	if dir == "" {
		dir = fileutil.ResourcesDir(*project)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fileutil.ResourcesPath = abs
	_, loadErrs := world.LoadBiomes(world.BiomesDir())

	switch sub {
	case "list":
		for _, p := range world.FoliagePresetsSorted() {
			fmt.Fprintf(out, "%-12s %-18s ground %-18s %2d species  (%s)\n", p.Name, p.Label,
				orDash(p.GroundTexture), len(p.Species), world.BiomeSource(p.Name))
		}
		for _, e := range loadErrs {
			fmt.Fprintln(out, "ERROR", e)
		}
		if len(loadErrs) > 0 {
			return 1
		}
		return 0

	case "check":
		return biomesCheck(names, loadErrs, out)

	default: // export
		if len(names) == 0 {
			for _, p := range world.FoliagePresetsSorted() {
				if world.BuiltinFoliagePreset(p.Name) != nil {
					names = append(names, p.Name)
				}
			}
		}
		if err := os.MkdirAll(world.BiomesDir(), 0o755); err != nil {
			fmt.Fprintln(out, err)
			return 1
		}
		bad := 0
		for _, name := range names {
			path := world.BiomePath(name)
			if _, err := os.Stat(path); err == nil && !*force {
				fmt.Fprintf(out, "skip  %s (already there; -force to overwrite)\n", path)
				continue
			}
			p := world.BuiltinFoliagePreset(name)
			if p == nil {
				p = world.FoliagePresetByName(name)
			}
			if p == nil {
				fmt.Fprintf(out, "ERROR no biome called %q\n", name)
				bad++
				continue
			}
			data, err := world.MarshalBiome(p)
			if err == nil {
				err = fileutil.WriteFileAtomic(path, data)
			}
			if err != nil {
				fmt.Fprintf(out, "ERROR %s: %v\n", name, err)
				bad++
				continue
			}
			fmt.Fprintf(out, "wrote %s\n", path)
		}
		if bad > 0 {
			return 1
		}
		return 0
	}
}

func biomesCheck(names []string, loadErrs []error, out io.Writer) int {
	bad := len(loadErrs)
	for _, e := range loadErrs {
		fmt.Fprintln(out, "ERROR", e)
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	n := 0
	for _, p := range world.FoliagePresetsSorted() {
		if len(want) > 0 && !want[p.Name] {
			continue
		}
		n++
		if msg := world.BiomeTextureProblem(p); msg != "" {
			fmt.Fprintf(out, "ERROR %s: %s\n", p.Name, msg)
			bad++
		}
	}
	if bad > 0 {
		fmt.Fprintf(out, "\n%d problem(s) in %d biome(s)\n", bad, n)
		return 1
	}
	fmt.Fprintf(out, "%d biome(s), no problems\n", n)
	return 0
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func biomesUsage(out io.Writer) {
	fmt.Fprintf(out, `foundry %s <command> — terrain biomes (%s/<name>%s).

  list               every biome, its ground texture and where it comes from
  check [name...]    parse the files and check their ground textures exist
  export [name...]   write the built-in biomes out as files to edit (-force to overwrite)
  schema             the biome file format
`, BiomesCommand, world.BiomesDirName, world.BiomeExt)
}
