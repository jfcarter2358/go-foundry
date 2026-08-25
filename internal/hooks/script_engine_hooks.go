package hooks

// script_engine_hooks.go — the three places the engine records something the
// scripting engine will want to hear about.
//
// Each is a small buffer written by engine code that knows nothing about
// scripts, and drained once a frame by script_events.go. The indirection is the
// point: EntityUpdate and the collision resolver stay about physics, and the
// event derivation stays in one file where the whole set can be read at once.
//
// All three are drained rather than read: taking the buffer empties it, so an
// event fires exactly once even if nothing is listening that frame.

// ---------- collisions ----------

// collisionPairs are the entity pairs the resolver pushed apart this frame.
var collisionPairs [][2]string

// RecordCollisionPair notes that two entities were separated. Called from
// ResolveEntityCollisions, which is the only place that knows which pair.
func RecordCollisionPair(a, b string) {
	if a == "" || b == "" {
		return
	}
	collisionPairs = append(collisionPairs, [2]string{a, b})
}

// TakeCollisionPairs returns this frame's pairs and clears the buffer.
func TakeCollisionPairs() [][2]string {
	out := collisionPairs
	collisionPairs = nil
	return out
}

// ---------- animation transitions ----------

// AnimEvent is one clip beginning or finishing on one attachment.
type AnimEvent struct {
	Entity    string
	Model     string // the attachment's name
	Animation string // what the game calls the clip, never the .fmdl's own name
	Ended     bool
}

var animEvents []AnimEvent

// EditorActive reports whether the editor is open, and is the only thing in
// this file that needs to know anything about the editor at all.
//
// It is a function the editor installs (EditorInit) rather than a read of
// editor state, so that this file — and the engine code that writes to its
// buffers — stays below the editor in the import graph. The default answers
// "no editor", which is correct for a shipped game and for every test.
var EditorActive = func() bool { return false }

// RecordAnimEvent notes a clip transition.
//
// Gated on play, and deliberately so: UpdateAnimations runs in the editor too —
// that is what lets a clip be previewed while placing the thing that plays it —
// and an editor preview must not fire script events.
func RecordAnimEvent(ev AnimEvent) {
	if EditorActive() {
		return
	}
	animEvents = append(animEvents, ev)
}

// TakeAnimEvents returns this frame's transitions and clears the buffer.
func TakeAnimEvents() []AnimEvent {
	out := animEvents
	animEvents = nil
	return out
}

// ---------- the interact key ----------

// scriptInteractPressed is set by the input handler on the press edge, and
// cleared by the first reader. An edge rather than a level, so holding the key
// fires once rather than every frame.
var scriptInteractPressed bool

// ScriptInteractRequest records that the interact key went down.
func ScriptInteractRequest() { scriptInteractPressed = true }

// ScriptInteractPressed reports and clears the pending press.
func ScriptInteractPressed() bool {
	out := scriptInteractPressed
	scriptInteractPressed = false
	return out
}

// ---------- vehicle events ----------

// VehicleEvent is one boarding or leaving, recorded by the engine so the script
// tick can turn it into a message.
//
// A drain-once buffer for the same reason the collision pairs above are:
// mounting happens in the collision package, which sits below scripting and
// must not reach up into it. The engine records what happened; the tick decides
// what it means.
type VehicleEvent struct {
	Atom     string // "mount" or "dismount"
	Vehicle  string
	Occupant string
	Seat     string
}

var vehicleEvents []VehicleEvent

// RecordVehicleEvent notes a boarding or a leaving.
func RecordVehicleEvent(ev VehicleEvent) {
	vehicleEvents = append(vehicleEvents, ev)
}

// TakeVehicleEvents returns and clears the events recorded since the last call.
func TakeVehicleEvents() []VehicleEvent {
	out := vehicleEvents
	vehicleEvents = nil
	return out
}
