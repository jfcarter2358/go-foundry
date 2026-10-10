package harness

// cmd_ai.go — `foundry ai`, the check and the listing for decision trees.
//
// The same question `foundry check` answers for a level, asked of a behaviour:
// does this document compile, and does everything in it mean something?
//
//	foundry ai check                  every .fai in resources/ai/
//	foundry ai check soldier          just that one
//	foundry ai check soldier -v       and print every tree and node
//
// It exits non-zero on any problem, so a generator or a build step can gate on
// it, and it needs no window and no level — a tree is a document, and the only
// thing it has to be checked against is the vocabulary.
//
// `-script <path>` additionally checks the calls: a `call` node naming a
// function that the script beside it does not define is the one mistake this
// cannot otherwise see, because the document has no idea which script it will
// run next to. Point it at the script the entity type names and the check
// becomes complete.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/aitree"
	"foundry/internal/cliflag"
	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/script"
)

// AICommand is the verb: `foundry ai`.
const AICommand = "ai"

// AIMain runs `foundry ai <subcommand>`.
func AIMain(root string, args []string, out io.Writer) int {
	if len(args) == 0 {
		aiUsage(out)
		return 2
	}
	switch args[0] {
	case "check":
		return aiCheckMain(root, args[1:], out)
	case "list":
		return aiListMain(root, args[1:], out)
	}
	aiUsage(out)
	return 2
}

func aiUsage(out io.Writer) {
	fmt.Fprintf(out, `foundry %s <command> — decision trees (%s).

  check [name...]   compile them and report every problem
  list              list the documents in %s/ and what each answers
`, AICommand, aitree.AIExt, aitree.AIDirName)
}

// aiCheckMain compiles documents and prints their diagnostics.
func aiCheckMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("ai check", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	res := fs.String("resources", "", "the resources/ directory itself, if it is not <root>/resources")
	scriptPath := fs.String("script", "", "also check that every `call` names a function this .flang defines")
	verbose := fs.Bool("v", false, "print every tree and node, not only the problems")
	fs.Usage = func() {
		fmt.Fprintf(out, "foundry ai check [name...] — compile decision trees and report what is wrong.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	names, err := cliflag.Parse(fs, args)
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

	if len(names) == 0 {
		names = aitree.ScanAIFiles()
		if len(names) == 0 {
			fmt.Fprintf(out, "no %s files under %s\n", aitree.AIExt, aitree.AIDir())
			return 0
		}
	}

	funcs := map[string]bool{}
	if *scriptPath != "" {
		var ok bool
		funcs, ok = aiScriptFunctions(*scriptPath, out)
		if !ok {
			return 1
		}
	}

	bad := 0
	for _, name := range names {
		if aiCheckOne(name, funcs, *scriptPath, *verbose, out) {
			bad++
		}
	}
	if bad > 0 {
		fmt.Fprintf(out, "\n%d of %d behaviour(s) have problems\n", bad, len(names))
		return 1
	}
	fmt.Fprintf(out, "\n%d behaviour(s), no problems\n", len(names))
	return 0
}

// aiCheckOne compiles one document. It reports whether anything was wrong.
func aiCheckOne(name string, funcs map[string]bool, scriptPath string, verbose bool, out io.Writer) bool {
	path := aitree.AIPath(name)
	doc, err := aitree.LoadAI(path)
	if err != nil {
		fmt.Fprintf(out, "%s: %v\n", name, err)
		return true
	}
	prog := aitree.Compile(doc)

	roots, helpers, nodes := 0, 0, 0
	for _, t := range doc.Trees {
		if t.IsHelper() {
			helpers++
		} else {
			roots++
		}
		nodes += len(t.Nodes)
	}
	fmt.Fprintf(out, "%s — %d root(s), %d helper(s), %d node(s), %d parameter(s)\n",
		name, roots, helpers, nodes, len(doc.Params))

	if verbose {
		for _, t := range doc.Trees {
			fmt.Fprintf(out, "  %s", t.Title())
			if len(t.Args) > 0 {
				fmt.Fprintf(out, "(%s)", strings.Join(t.Args, ", "))
			}
			fmt.Fprintf(out, "  root=%s\n", t.Root)
			for _, n := range t.Nodes {
				fmt.Fprintf(out, "    %-8s %s\n", n.ID, aiNodeLine(n))
			}
		}
	}

	problems := 0
	for _, d := range prog.Diags {
		kind := "error"
		if d.Warning {
			kind = "warning"
		} else {
			problems++
		}
		fmt.Fprintf(out, "  %s: %s\n", kind, d.Error())
	}

	// The calls, when there is a script to check them against.
	if scriptPath != "" {
		for _, miss := range aiMissingCalls(doc, funcs) {
			fmt.Fprintf(out, "  error: %s calls %s, which %s does not define\n",
				miss.where, miss.fn, filepath.Base(scriptPath))
			problems++
		}
	}
	return problems > 0
}

// aiNodeLine is one node as `foundry ai check -v` prints it: what it does, and
// where it goes.
func aiNodeLine(n *aitree.Node) string {
	var b strings.Builder
	switch n.Kind {
	case aitree.NodeNote:
		return "note  " + strings.ReplaceAll(n.Note, "\n", " ")
	case aitree.NodeCondition:
		b.WriteString("if    " + aiCallLine(n.Check))
	default:
		b.WriteString("do    " + aiCallLine(n.Do))
	}
	for _, l := range n.Links() {
		to := l.To
		if to == "" {
			to = "-"
		}
		b.WriteString("  " + l.Port + "=" + to)
	}
	return b.String()
}

func aiCallLine(c aitree.Call) string {
	parts := []string{c.Verb}
	v := aitree.VerbOf(c.Verb)
	if v != nil {
		for _, f := range v.Fields {
			if t := c.Fields[f.Name]; t != "" {
				parts = append(parts, f.Name+"="+t)
			}
		}
	}
	if len(c.Args) > 0 {
		parts = append(parts, "("+strings.Join(c.Args, ", ")+")")
	}
	return strings.Join(parts, " ")
}

// aiMissingCall is one `call` node naming a function that is not there.
type aiMissingCall struct{ where, fn string }

// aiMissingCalls finds every `call` whose function the script does not define.
func aiMissingCalls(doc *aitree.Doc, funcs map[string]bool) []aiMissingCall {
	var out []aiMissingCall
	for _, t := range doc.Trees {
		for _, n := range t.Nodes {
			if n.Kind != aitree.NodeAction || n.Do.Verb != "call" {
				continue
			}
			fn := n.Do.Field("function")
			if fn == "" || funcs[fn] {
				continue
			}
			out = append(out, aiMissingCall{where: t.Title() + " " + n.ID, fn: fn})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].where < out[j].where })
	return out
}

// aiScriptFunctions links a script and returns the names it defines.
func aiScriptFunctions(path string, out io.Writer) (map[string]bool, bool) {
	full := fileutil.ScriptPath(path)
	src, err := script.ReadScriptFile(full)
	if err != nil {
		fmt.Fprintf(out, "%s: %v\n", path, err)
		return nil, false
	}
	prog, errs := flang.Link(full, src, script.ImportLoader{})
	for _, e := range errs {
		fmt.Fprintf(out, "%s: %s\n", path, e.Msg)
	}
	if prog == nil {
		return nil, false
	}
	names := make(map[string]bool, len(prog.Funcs))
	for n := range prog.Funcs {
		names[n] = true
	}
	return names, true
}

// aiListMain prints what each document answers, which is the quickest way to
// see which behaviour handles what.
func aiListMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("ai list", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	res := fs.String("resources", "", "the resources/ directory itself, if it is not <root>/resources")
	if _, err := cliflag.Parse(fs, args); err != nil {
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

	names := aitree.ScanAIFiles()
	for _, name := range names {
		doc, err := aitree.LoadAI(aitree.AIPath(name))
		if err != nil {
			fmt.Fprintf(out, "%-20s %v\n", name, err)
			continue
		}
		fmt.Fprintf(out, "%-20s %s\n", name, strings.Join(doc.Events(), ", "))
	}
	if len(names) == 0 {
		fmt.Fprintf(out, "no %s files under %s\n", aitree.AIExt, aitree.AIDir())
	}
	return 0
}
