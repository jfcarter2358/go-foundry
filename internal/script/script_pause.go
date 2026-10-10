package script

// script_pause.go — `@level.prop.paused`, the pause that stops scripts too.
//
// A pausing screen (`pauses: true`) freezes the player, the vehicles, the
// entities and the particles, and deliberately leaves scripts running. That is
// right for a main menu with something animating behind it, and wrong for a
// pause menu, where a `:tick:` counting down a fuse or an `after` spawning the
// next wave carries straight on underneath. This is the other half: a script
// writes the switch, and the frame loop and the event pass both honour it.
//
// The line runs between the level and the screen stack. Everything the level
// would hear stops — `:tick:`, `:spawn:`, the derived events, timers coming
// due, `time()` — and everything on the stack carries on, because the menu
// that lifts the pause is made of scripts. A message a menu sends into the
// level is still delivered: pausing stops the clock, not the language, and a
// Restart button has to be able to tell the level so.
//
// Held rather than dropped: an engine event recorded just before the pause (a
// collision, a clip's end) waits in its buffer and is delivered on the first
// tick after it. The world stands still meanwhile — the frame loop stops the
// entities' clips as well as their motion — so nothing piles up behind it.

import "foundry/internal/hooks"

// GamePaused reports whether a script has paused the game. The frame loop reads
// it alongside the screen stack's own Pauses.
func GamePaused() bool { return scriptEvents.paused }

// postPausedEvents is ScriptEventsTick while the game is paused: the screen
// stack's half of the event pass, and none of the level's.
func postPausedEvents(rt *ScriptRuntime, dt float32) {
	refs := scriptedUIRefs()
	postSpawnEvents(rt, refs)
	postUIEvents(rt)
	// The world's pointer events are the level's, and the level is stopped:
	// dropped rather than kept, or every click made on the pause menu's
	// backdrop would land on the world the moment it lifted.
	hooks.TakePointerEvents()
	postTimerEvents(rt)
	postTickEvents(rt, refs, dt)
}
