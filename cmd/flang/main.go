// Command flang runs a .flang script from the command line.
//
// It exists so the language can be exercised without the engine — `flang
// fib.flang` is a far shorter loop than launching a game to find out whether a
// parser change broke something. The world it provides is the in-memory MapHost,
// which means addressing and messaging work well enough to try out, while the
// engine verbs report honestly that this host does not implement them.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"foundry/internal/flang"
)

// fileLoader resolves imports against the importing file's directory, rooted at
// the directory the entry script lives in.
type fileLoader struct{ root string }

func (l fileLoader) Load(fromFile, importPath string) ([]byte, string, error) {
	dir := filepath.Dir(fromFile)
	resolved := filepath.Clean(filepath.Join(dir, importPath))

	// An import must not escape the root, for the same reason the engine's does
	// not: a script tree that can read arbitrary files is a script tree that can
	// be handed to you by someone else.
	rel, err := filepath.Rel(l.root, resolved)
	if err != nil || rel == ".." || filepath.IsAbs(rel) ||
		(len(rel) > 2 && rel[:3] == ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("import %q leaves the script directory", importPath)
	}
	src, err := os.ReadFile(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("import %q: %w", importPath, err)
	}
	return src, resolved, nil
}

func main() {
	entry := flag.String("call", "main", "function to call")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: flang [-call name] script.flang\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	path := flag.Arg(0)
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	prog, errs := flang.Link(path, src, fileLoader{root: filepath.Dir(path)})
	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, errs)
		os.Exit(1)
	}

	host := flang.NewMapHost()
	in, clauseErrs := flang.NewInterp(prog, host, flang.Limits{})
	if len(clauseErrs) > 0 {
		fmt.Fprintln(os.Stderr, clauseErrs)
		os.Exit(1)
	}
	in.SetOutput(func(s string) { fmt.Println(s) })

	v, err := in.Call(*entry, nil, flang.Pos{File: path})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// A handler that returns null has nothing to say; printing "null" after
	// every run would be noise.
	if v != nil && v.Type() != flang.TypeNull {
		fmt.Println(v.String())
	}
}
