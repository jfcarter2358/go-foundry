package hooks

// save_hooks.go — a script asking to save, load, reach a checkpoint, retry or
// restart.
//
// Requests rather than calls, for load_level's reason and one of their own.
// Loading tears down the runtime running the handler that asked. And a save
// taken from inside a handler would catch the world halfway through the tick's
// drain — some of a cascade's messages delivered and the rest still queued —
// which is not a state the game can be put back into. The frame loop acts on
// these between frames, when the last tick has settled.

// SaveRequestKind is what a SaveRequest asks for.
type SaveRequestKind uint8

const (
	// SaveToSlot writes the game to a slot.
	SaveToSlot SaveRequestKind = iota + 1
	// LoadFromSlot replaces the running game with a slot's.
	LoadFromSlot
	// Checkpoint records a checkpoint: an autosave, and where Retry returns to.
	Checkpoint
	// Retry puts the player back at the last checkpoint, or restarts the level
	// when there has not been one.
	Retry
	// RestartLevel loads the running level again from scratch.
	RestartLevel
)

// SaveRequest is one pending request. Name is the slot for a save or a load
// and the checkpoint's name for a checkpoint.
type SaveRequest struct {
	Kind SaveRequestKind
	Name string
}

var saveRequests []SaveRequest

// RequestSave queues a request for the frame loop.
func RequestSave(r SaveRequest) {
	if r.Kind != 0 {
		saveRequests = append(saveRequests, r)
	}
}

// TakeSaveRequests returns the pending requests in the order they were made,
// and clears them.
func TakeSaveRequests() []SaveRequest {
	out := saveRequests
	saveRequests = nil
	return out
}

// lastCheckpoint is the name of the checkpoint retry() would return to, or ""
// — kept by the frame loop, which owns the checkpoint itself, and read by
// `@level.prop.last_checkpoint` so a pause menu can grey out a button.
var lastCheckpoint string

// SetLastCheckpoint records which checkpoint retry() returns to.
func SetLastCheckpoint(name string) { lastCheckpoint = name }

// LastCheckpoint is the checkpoint retry() returns to, or "".
func LastCheckpoint() string { return lastCheckpoint }
