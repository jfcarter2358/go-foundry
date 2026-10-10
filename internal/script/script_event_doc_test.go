package script

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// eventPostSources are the files that actually post engine events.
var eventPostSources = []string{"script_events.go", "script_actions.go", "script_pointer.go"}

// TestEventDocsMatchPosts checks the reference against the code that posts.
//
// The posting code names its atoms inline rather than reading the doc table —
// putting a lookup in the per-frame path to save duplicating a dozen strings
// would be the wrong trade — so nothing but a test keeps the two in step, and
// the failure it prevents is a silent one: an event the engine sends that the
// F1 reference never mentions.
//
// It reads the source with go/ast rather than by pattern-matching text, because
// the calls wrap across lines and a regular expression that tolerates that also
// happily runs past the end of a call and reports the next string literal in
// the file. Posts whose atom is a variable (`:anim_start:` and `:anim_end:`,
// which share one call site) are invisible to this and are covered by
// TestEventDocsWellFormed's table instead.
func TestEventDocsMatchPosts(t *testing.T) {
	documented := map[string]bool{}
	for _, d := range EventDocs() {
		documented[strings.Trim(d.Atom, ":")] = true
	}

	fset := token.NewFileSet()
	seen := map[string]bool{}
	for _, name := range eventPostSources {
		// The package's own directory is a test's working directory.
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			// rt.post(ref, "atom", ...) and rt.postFrom — and the pointer
			// events' postPointer(rt, lvl, "atom", ...), which fans one event
			// out to several listeners.
			atomArg := -1
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				if fn.Sel.Name == "post" || fn.Sel.Name == "postFrom" {
					atomArg = 1
				}
			case *ast.Ident:
				if fn.Name == "postPointer" && len(call.Args) > 2 {
					atomArg = 2
				}
			}
			if atomArg < 0 {
				return true
			}
			lit, ok := call.Args[atomArg].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			atom, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			seen[atom] = true
			return true
		})
	}

	if len(seen) == 0 {
		t.Fatal("found no posted atoms at all — has the post() call shape changed?")
	}
	for atom := range seen {
		if !documented[atom] {
			t.Errorf("the engine posts :%s: but script_event_doc.go does not list it", atom)
		}
	}
}

// TestEventDocsWellFormed keeps the table renderable: atoms written as they are
// matched, and every column filled in.
func TestEventDocsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range EventDocs() {
		if !strings.HasPrefix(d.Atom, ":") || !strings.HasSuffix(d.Atom, ":") || len(d.Atom) < 3 {
			t.Errorf("event %q is not written as an atom", d.Atom)
		}
		if seen[d.Atom] {
			t.Errorf("event %q is listed twice", d.Atom)
		}
		seen[d.Atom] = true
		if d.PostedTo == "" || d.Args == "" || d.When == "" {
			t.Errorf("event %q has an incomplete entry: %+v", d.Atom, d)
		}
	}
	// The two the AST scan cannot see, because their call site takes the atom
	// from a variable.
	for _, atom := range []string{":anim_start:", ":anim_end:"} {
		if !seen[atom] {
			t.Errorf("%s is posted by postAnimEvents but is not in the table", atom)
		}
	}
	if got, want := len(EventAtoms()), len(EventDocs()); got != want {
		t.Errorf("EventAtoms returned %d atoms for %d events", got, want)
	}
}
