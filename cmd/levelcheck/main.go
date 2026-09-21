// Command levelcheck loads a level with no window and lints its scripts
// against it.
//
// It answers the question that costs the most time otherwise: "does the thing I
// just generated actually load, and do the ids my scripts name exist?" The
// level loader validates as it goes — a missing texture, a brush the engine
// will not accept, an entity type that is not there — and `script.LintScript`,
// given a real level, resolves every `@id` and `#tag` a script mentions against
// it. Between them they catch the whole class of mistake that otherwise shows
// up as a blank wall or a silent no-op three minutes into a playthrough.
//
//	go run ./cmd/levelcheck -resources <install>/resources <level name> [more...]
//
// The level name is what the loader takes: `level1`, not a path.
//
// Every .flang under scripts/ is linted, not only the ones this level attaches,
// because the level-aware checks are the point and a script is usually written
// for one level. Pass -scripts to narrow it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/levelio"
	"foundry/internal/script"
	"foundry/internal/ui"
	"foundry/internal/world"
)

type loader struct{ root string }

func (l loader) Load(fromFile, importPath string) ([]byte, string, error) {
	resolved, err := script.ResolveScriptImport(fromFile, importPath)
	if err != nil {
		return nil, "", err
	}
	src, err := os.ReadFile(resolved)
	return src, resolved, err
}

func main() {
	res := flag.String("resources", "", "the install's resources/ directory (required)")
	only := flag.String("scripts", "", "comma-separated substrings; lint only scripts matching one")
	flag.Parse()
	if *res == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: levelcheck -resources <dir> <level> [level...]")
		os.Exit(2)
	}
	abs, err := filepath.Abs(*res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fileutil.ResourcesPath = abs

	bad := 0
	var lvl world.Level
	for _, name := range flag.Args() {
		l, err := levelio.LoadLevelHeadless(levelio.LevelPath(name))
		if err != nil {
			fmt.Printf("LEVEL %s: FAILED TO LOAD\n  %v\n", name, err)
			bad++
			continue
		}
		lvl = l
		fmt.Printf("LEVEL %s: loaded — %d chunks, %d entities, %d volumes, %d lights, %d sounds, %d emitters\n",
			name, len(l.Chunks), len(l.Entities), len(l.Volumes),
			len(l.Lights.Points)+len(l.Lights.Directional), len(l.Sounds), len(l.Emitters))
	}

	// Every screen too. A .fui that does not load is a screen a script asks for
	// and does not get — show_screen resolves by name against resources/ui/ at
	// the moment it is called, so nothing before then would have noticed.
	uiDir := filepath.Join(abs, "ui")
	var uis []string
	filepath.Walk(uiDir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".fui") {
			uis = append(uis, p)
		}
		return nil
	})
	sort.Strings(uis)
	for _, p := range uis {
		rel, _ := filepath.Rel(uiDir, p)
		if sc, err := ui.LoadScreen(p); err != nil {
			fmt.Printf("SCREEN %s: %v\n", rel, err)
			bad++
		} else {
			fmt.Printf("SCREEN %s: ok (%s)\n", rel, sc.ID)
		}
	}

	// Lint against the last level loaded — with several, the last one wins,
	// which is what a single-level check wants and is honest about.
	var want []string
	if *only != "" {
		want = strings.Split(*only, ",")
	}
	var paths []string
	root := fileutil.ScriptsDir()
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".flang") {
			return nil
		}
		if len(want) > 0 {
			hit := false
			for _, w := range want {
				if strings.Contains(p, strings.TrimSpace(w)) {
					hit = true
				}
			}
			if !hit {
				return nil
			}
		}
		paths = append(paths, p)
		return nil
	})
	sort.Strings(paths)

	for _, p := range paths {
		rel, _ := filepath.Rel(root, p)
		src, err := os.ReadFile(p)
		if err != nil {
			fmt.Printf("SCRIPT %s: %v\n", rel, err)
			bad++
			continue
		}
		f, errs := flang.Parse(p, src)
		if len(errs) > 0 {
			fmt.Printf("SCRIPT %s: %d PARSE ERRORS\n", rel, len(errs))
			for _, e := range errs {
				fmt.Printf("  %d:%d %s\n", e.Pos.Line, e.Pos.Col, e.Msg)
			}
			bad++
			continue
		}
		// Imports bring names into scope; without them every call to an
		// imported function reads as an unknown one.
		scope, params := scopeNames(f, p)
		issues := script.LintScript(&lvl, f, scope, params)
		if len(issues) == 0 {
			fmt.Printf("SCRIPT %s: ok\n", rel)
			continue
		}
		fmt.Printf("SCRIPT %s: %d issues\n", rel, len(issues))
		for _, s := range issues {
			fmt.Printf("  %d:%d %s\n", s.Pos.Line, s.Pos.Col, s.Msg)
			bad++
		}
	}
	if bad > 0 {
		fmt.Printf("\n%d problems\n", bad)
		os.Exit(1)
	}
	fmt.Println("\nall clear")
}

// scopeNames is cmd-side scriptScopeNames: every function and param name this
// file and its imports define.
func scopeNames(f *flang.File, path string) (map[string]bool, map[string]bool) {
	funcs, params, seen := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var walk func(file *flang.File, from string)
	walk = func(file *flang.File, from string) {
		for _, g := range file.Funcs {
			funcs[g.Name] = true
		}
		for _, d := range file.Params {
			params[d.Name] = true
		}
		for _, imp := range file.Imports {
			resolved, err := script.ResolveScriptImport(from, imp.Path)
			if err != nil || seen[resolved] {
				continue
			}
			seen[resolved] = true
			src, err := os.ReadFile(resolved)
			if err != nil {
				continue
			}
			sub, errs := flang.Parse(resolved, src)
			if len(errs) == 0 {
				walk(sub, resolved)
			}
		}
	}
	walk(f, path)
	return funcs, params
}
