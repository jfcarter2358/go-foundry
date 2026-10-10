package hooks

// load_hooks.go — how far through loading a level the engine is.
//
// Here rather than in internal/levelio for the reason every other buffer in
// this package is here: internal/script sits below levelio and cannot import
// it, and a loading screen's bar binds to `@level.prop.load_progress`.
//
// Unlike the event buffers this is read rather than drained. A progress
// fraction is a state, not something that happened, and a widget polling it
// every frame must get the same answer every frame until it changes.

var (
	loadProgress float32
	loadStage    string
)

// SetLoadProgress records how far a level load has got, and what it is doing.
func SetLoadProgress(fraction float32, stage string) {
	loadProgress, loadStage = fraction, stage
}

// LoadProgress is the fraction, from 0 to 1.
func LoadProgress() float32 { return loadProgress }

// LoadStage is a one-line description of the current step.
func LoadStage() string { return loadStage }

// ClearLoadProgress puts it back to "nothing is loading", which reads as a
// finished load rather than as one that never started — a bar left at zero
// after the level is up would look stuck.
func ClearLoadProgress() { loadProgress, loadStage = 1, "" }

// pendingLevelLoad is a level a script has asked for, drained by the frame
// loop.
//
// A request rather than a call, for the reason every buffer here is: loading a
// level tears down the runtime that is currently executing the script making
// the request. Recording it and letting the loop act on it between frames is
// the difference between a level change and a crash.
var pendingLevelLoad string

// RequestLevelLoad asks the frame loop to load a level by name.
func RequestLevelLoad(name string) {
	if name != "" {
		pendingLevelLoad = name
	}
}

// TakeLevelLoadRequest returns a pending request and clears it.
func TakeLevelLoadRequest() (string, bool) {
	name := pendingLevelLoad
	pendingLevelLoad = ""
	return name, name != ""
}
