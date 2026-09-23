package harness

// cmd_check.go — `foundry check`, the lint that knows the level.
//
// It answers the question that costs the most time otherwise: "does the thing I
// just generated actually load, and do the ids my scripts name exist?" The level
// loader validates as it goes — a missing texture, a brush the engine will not
// accept, an entity type that is not there — and script.LintScript, GIVEN A REAL
// LEVEL, resolves every `@id` and `#tag` a script mentions against it. Between
// them they catch the whole class of mistake that otherwise shows up as a blank
// wall or a silent no-op three minutes into a playthrough.
//
//	foundry check level1
//
// The level name is what the loader takes: `level1`, not a path. Every .flang
// under scripts/ is linted, not only the ones this level attaches, because the
// level-aware checks are the point and a script is usually written for one
// level; -scripts narrows it. Every .fui is loaded too: a screen that does not
// load is a screen a script asks for and does not get, and show_screen resolves
// by name at the moment it is called, so nothing before then would notice.
//
// It exits non-zero on any problem, which is what makes it usable from a
// generator's own build step.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/cliflag"
	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/levelio"
	"foundry/internal/script"
	"foundry/internal/ui"
	"foundry/internal/world"
)

// CheckCommand is the verb: `foundry check`.
const CheckCommand = "check"

// CheckMain lints one or more levels and the scripts and screens beside them.
func CheckMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet(CheckCommand, flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	res := fs.String("resources", "", "the resources/ directory itself, if it is not <root>/resources")
	only := fs.String("scripts", "", "comma-separated substrings; lint only scripts matching one")
	fs.Usage = func() {
		fmt.Fprintf(out, "foundry %s <level> [level...] — load a level and lint its scripts against it.\n\nFlags:\n", CheckCommand)
		fs.PrintDefaults()
	}
	levels, err := cliflag.Parse(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(levels) == 0 {
		fs.Usage()
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

	bad := 0
	var lvl world.Level
	for _, name := range levels {
		l, err := levelio.LoadLevelHeadless(levelio.LevelPath(name))
		if err != nil {
			fmt.Fprintf(out, "LEVEL %s: FAILED TO LOAD\n  %v\n", name, err)
			bad++
			continue
		}
		lvl = l
		fmt.Fprintf(out, "LEVEL %s: loaded — %d chunks, %d entities, %d volumes, %d lights, %d sounds, %d emitters\n",
			name, len(l.Chunks), len(l.Entities), len(l.Volumes),
			len(l.Lights.Points)+len(l.Lights.Directional), len(l.Sounds), len(l.Emitters))
	}

	// Every screen too.
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
			fmt.Fprintf(out, "SCREEN %s: %v\n", rel, err)
			bad++
		} else {
			fmt.Fprintf(out, "SCREEN %s: ok (%s)\n", rel, sc.ID)
		}
	}

	// Lint against the last level loaded — with several, the last one wins,
	// which is what a single-level check wants and is honest about.
	var want []string
	if *only != "" {
		want = strings.Split(*only, ",")
	}
	var paths []string
	scripts := fileutil.ScriptsDir()
	filepath.Walk(scripts, func(p string, info os.FileInfo, err error) error {
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
		rel, _ := filepath.Rel(scripts, p)
		src, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(out, "SCRIPT %s: %v\n", rel, err)
			bad++
			continue
		}
		f, errs := flang.Parse(p, src)
		if len(errs) > 0 {
			fmt.Fprintf(out, "SCRIPT %s: %d PARSE ERRORS\n", rel, len(errs))
			for _, e := range errs {
				fmt.Fprintf(out, "  %d:%d %s\n", e.Pos.Line, e.Pos.Col, e.Msg)
			}
			bad++
			continue
		}
		// Imports bring names into scope; without them every call to an
		// imported function reads as an unknown one.
		scope, params := scopeNames(f, p)
		issues := script.LintScript(&lvl, f, scope, params)
		if len(issues) == 0 {
			fmt.Fprintf(out, "SCRIPT %s: ok\n", rel)
			continue
		}
		fmt.Fprintf(out, "SCRIPT %s: %d issues\n", rel, len(issues))
		for _, s := range issues {
			fmt.Fprintf(out, "  %d:%d %s\n", s.Pos.Line, s.Pos.Col, s.Msg)
			bad++
		}
	}
	if bad > 0 {
		fmt.Fprintf(out, "\n%d problems\n", bad)
		return 1
	}
	fmt.Fprintln(out, "\nall clear")
	return 0
}

// scopeNames is the command's own scriptScopeNames: every function and param
// name this file and its imports define.
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
