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

// ---------- weapon input ----------
//
// The viewmodel's fire/throw/switch buttons, mirroring the interact key's
// shape above with one difference: fire needs both edges, not just the press,
// because a held trigger and a released one are different script events (an
// automatic weapon fires on a timer while down, and has to know when to stop).
// Throw and weapon-switch are plain presses, same as interact.

// fireEdges is one held button's presses (true) and releases (false) since
// the last drain, in the order they happened.
//
// A list rather than a "pressed" and a "released" flag. Two flags lose the
// order: the release of one click and the press of the next landing in the
// same frame read back as "pressed, then released", and the script is told the
// trigger is up while the player is holding it — an automatic weapon stops, a
// scope drops. They also fold two quick clicks inside one frame into one.
type fireEdges []bool

var primaryFireEdges, secondaryFireEdges fireEdges

// ScriptPrimaryFireRequest records the primary fire button's edge: down=true
// on press, down=false on release.
func ScriptPrimaryFireRequest(down bool) { primaryFireEdges = append(primaryFireEdges, down) }

// TakeScriptPrimaryFireEdges returns and clears the pending edges, oldest
// first: true for a press, false for a release.
func TakeScriptPrimaryFireEdges() []bool { return takeFireEdges(&primaryFireEdges) }

// ScriptSecondaryFireRequest is ScriptPrimaryFireRequest for the secondary
// fire button.
func ScriptSecondaryFireRequest(down bool) { secondaryFireEdges = append(secondaryFireEdges, down) }

// TakeScriptSecondaryFireEdges is TakeScriptPrimaryFireEdges for the
// secondary fire button.
func TakeScriptSecondaryFireEdges() []bool { return takeFireEdges(&secondaryFireEdges) }

func takeFireEdges(e *fireEdges) []bool {
	out := []bool(*e)
	*e = nil
	return out
}

// scriptThrowPressed is the grenade-throw key, an edge for the same reason
// scriptInteractPressed is.
var scriptThrowPressed bool

// ScriptThrowRequest records that the throw key went down.
func ScriptThrowRequest() { scriptThrowPressed = true }

// ScriptThrowPressed reports and clears the pending press.
func ScriptThrowPressed() bool {
	out := scriptThrowPressed
	scriptThrowPressed = false
	return out
}

// scriptWeaponSlotRequests are pending weapon-switch presses, queued rather
// than a single pending slot so two slot keys hit in the same frame both
// reach the script rather than the second silently overwriting the first.
var scriptWeaponSlotRequests []int

// ScriptWeaponSlotRequest records that the given slot's key went down.
func ScriptWeaponSlotRequest(slot int) {
	scriptWeaponSlotRequests = append(scriptWeaponSlotRequests, slot)
}

// TakeScriptWeaponSlotRequests returns and clears the pending slot presses.
func TakeScriptWeaponSlotRequests() []int {
	out := scriptWeaponSlotRequests
	scriptWeaponSlotRequests = nil
	return out
}

// scriptReloadPressed is the reload key, an edge like the throw key.
var scriptReloadPressed bool

// ScriptReloadRequest records that the reload key went down.
func ScriptReloadRequest() { scriptReloadPressed = true }

// ScriptReloadPressed reports and clears the pending press.
func ScriptReloadPressed() bool {
	out := scriptReloadPressed
	scriptReloadPressed = false
	return out
}

// scriptWeaponCycleRequests are pending next/previous-weapon steps (+1 or -1),
// queued for the same reason slot presses are: a wheel flicked twice in one
// frame is two steps, not one.
var scriptWeaponCycleRequests []int

// ScriptWeaponCycleRequest records one step through the weapons: +1 for the
// next, -1 for the previous.
func ScriptWeaponCycleRequest(step int) {
	scriptWeaponCycleRequests = append(scriptWeaponCycleRequests, step)
}

// TakeScriptWeaponCycleRequests returns and clears the pending steps.
func TakeScriptWeaponCycleRequests() []int {
	out := scriptWeaponCycleRequests
	scriptWeaponCycleRequests = nil
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

// ---------- waypoint animations ----------

// WaypointObject is the kind of object travelling a waypoint animation. The
// engine only says which list its ID is in; the script package turns that into
// the matching ref kind.
type WaypointObject uint8

const (
	WaypointBrush WaypointObject = iota
	WaypointEntity
	WaypointChunk
	WaypointSprite
	WaypointEmitter
	WaypointSound
	WaypointLight
	WaypointVolume
)

// WaypointObjectNames is each kind's name, indexed by the kind itself: the
// word a script's ref kind uses for it, and what the editor labels it with.
var WaypointObjectNames = [...]string{
	WaypointBrush:   "brush",
	WaypointEntity:  "entity",
	WaypointChunk:   "chunk",
	WaypointSprite:  "sprite",
	WaypointEmitter: "emitter",
	WaypointSound:   "sound",
	WaypointLight:   "light",
	WaypointVolume:  "volume",
}

// WaypointEvent is one object reaching one point of the waypoint animation it
// is travelling, or reaching the end of it.
type WaypointEvent struct {
	// Kind and Object name what arrived: which kind of object, and its ID.
	Kind      WaypointObject
	Object    string
	Animation string
	// Index is the point reached, counting from zero. For Ended it is the last
	// point's index.
	Index int
	Ended bool
	// Events are the custom atoms the point reached carries, to be posted to
	// the brush after `:waypoint:`. Always empty for Ended: the last point's
	// events went out with its own arrival.
	Events []string
}

var waypointEvents []WaypointEvent

// RecordWaypointEvent notes an arrival.
//
// Gated on play for RecordAnimEvent's reason: nothing a brush does on a path
// while the editor is up may queue script events for the next play session to
// deliver. The editor's preview seeks rather than steps, so it never gets here,
// but the step it would be one call away from does.
func RecordWaypointEvent(ev WaypointEvent) {
	if EditorActive() {
		return
	}
	waypointEvents = append(waypointEvents, ev)
}

// TakeWaypointEvents returns this frame's arrivals and clears the buffer.
func TakeWaypointEvents() []WaypointEvent {
	out := waypointEvents
	waypointEvents = nil
	return out
}

// ---------- AI controllers ----------

// AIEvent is one thing an AI controller did that its entity's script — and,
// for an objective, the objective volume's — will want to hear about: a target
// seen or lost, an arrival, an objective reached or finished.
//
// A drain-once buffer on the vehicle events' reasoning: the controller sits
// below scripting and must not reach up into it.
type AIEvent struct {
	// Atom is the message, without its colons.
	Atom string
	// Agent is the entity whose controller it is.
	Agent string
	// Target is the entity the event is about, for the target events.
	Target string
	// Objective is the volume the event is about, for the objective events.
	Objective string
	// ToObjective posts the event to the objective volume as well as the agent.
	ToObjective bool
}

var aiEvents []AIEvent

// RecordAIEvent notes a controller event. Gated on play for RecordAnimEvent's
// reason, although nothing runs a controller in the editor today.
func RecordAIEvent(ev AIEvent) {
	if EditorActive() {
		return
	}
	aiEvents = append(aiEvents, ev)
}

// TakeAIEvents returns this frame's controller events and clears the buffer.
func TakeAIEvents() []AIEvent {
	out := aiEvents
	aiEvents = nil
	return out
}

// ---------- the player's footing ----------
//
// Leaving and touching the ground, for the player's `:landed:` and
// `:left_ground:`. Recorded by the frame that moved the player because it is
// the only place that knows the transition AND the speed it happened at: the
// resolve zeroes the fall the instant it stops it, so a script reading the
// velocity afterwards always finds zero.
//
// A level rather than an edge buffer, on purpose. There is one player and at
// most one transition per frame, so a queue would only make it possible to
// miss one; and holding the state here is what lets the runtime derive the
// edge itself, which is the only way `:left_ground:` can fire for a player who
// walked off a ledge rather than jumping — nothing presses a key for that.

// PlayerGround is the player's footing as of the last move.
type PlayerGround struct {
	// OnGround is whether the body is standing on something.
	OnGround bool
	// Landed is whether that became true this frame, and Impact is how fast it
	// was falling when it did, in world units per second — what a fall-damage
	// rule is written against.
	Landed bool
	Impact float32
	// Known is false before anything has moved the player, which is the editor
	// and the frames before a level finishes loading. The runtime treats it as
	// "no answer yet" and posts nothing, rather than reading the zero value as
	// a player hanging in mid-air and firing a spurious `:left_ground:`.
	Known bool
}

var playerGround PlayerGround

// RecordPlayerGround notes what one frame of walking did to the player's
// footing. Called from the sim's player step.
func RecordPlayerGround(onGround, landed bool, impact float32) {
	playerGround = PlayerGround{OnGround: onGround, Landed: landed, Impact: impact, Known: true}
}

// CurrentPlayerGround reports the last recorded footing. Read, not drained:
// the runtime compares it against what it last saw, so a frame the script tick
// does not run must not lose the state.
func CurrentPlayerGround() PlayerGround { return playerGround }

// ResetPlayerGround forgets the footing, so the next level — or the next entry
// into play — starts with no answer rather than with the last one. Without it a
// player who was airborne when the level ended posts a `:landed:` on the first
// frame of the next one.
func ResetPlayerGround() { playerGround = PlayerGround{} }
