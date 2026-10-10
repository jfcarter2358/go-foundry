package synth

// cli.go — `gen-samples`: regenerate the stock library from its recipes.
//
// The library is checked in, so this runs when a recipe changes, not on
// every build. -only regenerates a few by name, which is the loop while
// tuning one sound.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"foundry/internal/tracker"
)

// Rate is the library's sample rate.
const Rate = 48000

// Main runs `gen-samples [-out dir] [-only name,...] [-list]` and returns an
// exit code.
func Main(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("gen-samples", flag.ContinueOnError)
	fs.SetOutput(out)
	dir := fs.String("out", "", "output directory (default: the project's sample library)")
	only := fs.String("only", "", "comma-separated recipe names to generate (default: all)")
	list := fs.Bool("list", false, "list the recipes and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *list {
		for i, r := range Recipes {
			fmt.Fprintf(out, "%3d  %-28s %s\n", i+1, r.LibraryName(), r.Desc)
		}
		return 0
	}
	if *dir == "" {
		*dir = tracker.SamplesDir()
	}
	var todo []*Recipe
	if *only == "" {
		for i := range Recipes {
			todo = append(todo, &Recipes[i])
		}
	} else {
		for _, name := range strings.Split(*only, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			r := ByName(name)
			if r == nil {
				fmt.Fprintf(out, "gen-samples: no recipe %q (try -list)\n", name)
				return 1
			}
			todo = append(todo, r)
		}
	}
	start := time.Now()
	var total int64
	for i, r := range todo {
		if err := writeRecipe(*dir, Rate, r, nil); err != nil {
			fmt.Fprintf(out, "gen-samples: %v\n", err)
			return 1
		}
		if st, err := os.Stat(filepath.Join(*dir, r.Category, r.Name+".wav")); err == nil {
			total += st.Size()
		}
		fmt.Fprintf(out, "[%3d/%d] %s\n", i+1, len(todo), r.LibraryName())
	}
	fmt.Fprintf(out, "gen-samples: %d samples, %.1f MB in %s -> %s\n",
		len(todo), float64(total)/(1<<20), time.Since(start).Round(time.Millisecond), *dir)
	return 0
}
