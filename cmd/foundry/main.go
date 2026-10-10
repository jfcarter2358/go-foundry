package main

// cmd/foundry — the engine's entry point.
//
// Everything here has to happen before anything else in the process, which is
// the only reason it is not in internal/app with the rest of the bootstrap.

import (
	"runtime"

	"foundry/internal/app"
)

// The texture editor drives a second SDL window and GL context on the same OS
// thread as the main loop (internal/uiwin). SDL/GL calls must stay pinned to one
// OS thread — without this, the Go scheduler is free to migrate the goroutine
// between them and the "current" GL context becomes whichever thread last
// touched it.
//
// It is in init() rather than at the top of main() because the runtime may run
// other package init functions on this goroutine first, and in cmd/foundry
// rather than in internal/app because *when* it runs is the whole point: an
// init in a library package fires in import order, which is not something the
// engine should depend on.
func init() {
	runtime.LockOSThread()
}

func main() {
	app.Run()
}
