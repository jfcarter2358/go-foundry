// Package packaging carries the Linux installer scripts into the programs that
// run them.
//
// The Taskfile reads packaging/linux straight off the disk, from the repository
// it runs in. A release built from inside the editor has no repository to read
// them from — the editor runs from a game's own project directory, which is
// somewhere else entirely — so the same files are embedded here, and
// internal/pack writes them out beside the tree it is packaging. One copy of
// each script, whichever way the build is run.
//
// windows/installer.nsi is not embedded: the editor's release build makes its
// Setup.exe with Foundry's own installer (internal/setup), and only the
// Taskfile's installer-windows task still runs NSIS.
package packaging

import _ "embed"

// LinuxInstallScript is install.sh, which ships inside the Linux tarball.
//
//go:embed linux/install.sh
var LinuxInstallScript []byte

// DebScript is make-deb.sh, which turns a staged Linux tree into a .deb.
//
//go:embed linux/make-deb.sh
var DebScript []byte
