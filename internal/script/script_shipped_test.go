package script

// script_shipped_test.go — the scripts that ship with the engine must parse.
//
// The same reasoning as entity_registry_test.go's check on the shipped entity
// definitions: an example that does not work is worse than no example, because
// it is the first thing an author copies. This also catches the case where a
// grammar change silently invalidates the documentation's own worked example.

import (
	"os"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
)

func TestShippedScriptsParse(t *testing.T) {
	names := fileutil.ScanScriptFilesIn("resources/scripts")
	if len(names) == 0 {
		t.Skip("no scripts shipped")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile("resources/scripts/" + name)
			if err != nil {
				t.Fatal(err)
			}
			f, errs := flang.Parse(name, src)
			if len(errs) > 0 {
				t.Fatalf("does not parse:\n%v", errs)
			}
			// A script with no functions is a comment file. Every one that ships
			// is meant to be an example of something.
			if len(f.Funcs) == 0 {
				t.Errorf("defines no functions")
			}
			// Clause ordering has to be unambiguous, or dispatch would be a
			// coin flip — and an ambiguous example teaches the wrong thing.
			prog := flang.NewProgram()
			if perrs := prog.Add(f); len(perrs) > 0 {
				t.Fatalf("link errors: %v", perrs)
			}
			if _, ierrs := flang.NewInterp(prog, nil, flang.Limits{}); len(ierrs) > 0 {
				t.Errorf("clause errors: %v", ierrs)
			}
		})
	}
}
