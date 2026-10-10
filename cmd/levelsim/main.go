// Command levelsim is `foundry sim` under its old name.
//
// The harness it drove now lives in internal/harness, and the tick it runs is
// internal/sim — the same frame the engine runs. This binary is kept because the
// recipes in the project's notes and generators name it; new work should use
// `foundry sim`, which takes the same flags.
package main

import (
	"os"

	"foundry/internal/harness"
)

func main() {
	os.Exit(harness.SimMain(cwd(), os.Args[1:], os.Stdout))
}

// cwd is the project to work on when neither -root nor -resources says: the
// directory the command was run from, which is what `go run ./cmd/levelsim`
// from a project means.
func cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}
