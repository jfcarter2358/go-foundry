// Command levelcheck is `foundry check` under its old name.
//
// The lint itself lives in internal/harness (cmd_check.go). This binary is kept
// because the recipes in the project's notes and generators name it; new work
// should use `foundry check`, which takes the same flags.
package main

import (
	"os"

	"foundry/internal/harness"
)

func main() {
	os.Exit(harness.CheckMain(cwd(), os.Args[1:], os.Stdout))
}

func cwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}
