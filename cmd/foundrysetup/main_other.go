//go:build !windows

// Command foundrysetup is a game's Windows installer and uninstaller; see
// main_windows.go. Anywhere else there is nothing for it to install into.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "foundrysetup is the Windows installer: build it with GOOS=windows (task runtime-windows) and let the release build append a game to it")
	os.Exit(2)
}
