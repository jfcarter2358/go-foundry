package app

// boot_failure.go — a game that cannot start says why, on screen and in a log
// the message box names.
//
// A Windows release is built -H windowsgui: it has no console, so anything it
// writes to stderr is lost, and a log.Fatalf leaves a player looking at a game
// that opened and closed. Every way out of startup goes through fatalStartup,
// and a Go runtime crash — which never reaches the log package, and may happen
// on any goroutine — is pointed at the same file by openBootLog.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"

	"foundry/internal/platform"

	"github.com/veandco/go-sdl2/sdl"
)

// bootLogPath is the log file this run writes, once it is open, for a failure
// message to point at.
var bootLogPath string

// openBootLog creates the log at path, or — when the data directory will not
// take it — in the temporary folder, so a failure still has somewhere to go.
// The Go runtime's own crash reports (unrecovered panics on any goroutine,
// faults in C code) are sent to it too.
func openBootLog(path string) (*os.File, error) {
	f, err := os.Create(path)
	if err != nil {
		fallback := filepath.Join(os.TempDir(), filepath.Base(path))
		var ferr error
		if f, ferr = os.Create(fallback); ferr != nil {
			return nil, err
		}
		path = fallback
	}
	bootLogPath = path
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	return f, nil
}

// fatalStartup logs why the engine cannot go on, shows the player the same in
// a message box with advice and the log's path, and exits.
//
// SDL's message box needs neither SDL_Init nor a window, so this works from
// the first line of Run onward.
func fatalStartup(msg, advice string) {
	log.Printf("FATAL: %s", msg)
	text := msg
	if advice != "" {
		text += "\n\n" + advice
	}
	if bootLogPath != "" {
		text += "\n\nThe full log is at:\n" + bootLogPath
	}
	_ = sdl.ShowSimpleMessageBox(sdl.MESSAGEBOX_ERROR, platform.WinTitle, text, nil)
	os.Exit(1)
}

// fatalStartupf is fatalStartup with no advice, for a formatted message.
func fatalStartupf(format string, args ...any) {
	fatalStartup(fmt.Sprintf(format, args...), "")
}

// recoverToLog is deferred at the top of Run: a panic on the main goroutine is
// logged with its stack and reported like any other startup failure.
func recoverToLog() {
	if r := recover(); r != nil {
		log.Printf("PANIC: %v\n%s", r, debug.Stack())
		fatalStartup(fmt.Sprintf("The game crashed: %v", r), "")
	}
}
