package main

import (
	"foundry/internal/script"
	"foundry/internal/world"
)

// level_play_snapshot.go — taking and restoring the authored level around a
// play session.
//
// The session itself (whether one is open, and whether anything has written to
// it) is script_play.go's, because that is what the script runtime marks and
// what the undo stack consults. The *snapshot* is here instead, with the rest
// of the level serialiser, because that is the only thing it does: a RawLevel
// out and a rebuilt Level back in, through exactly the path a save and a load
// take.
//
// Splitting them is what lets the script runtime stay below the serialiser.
// It writes a flag; it does not know a YAML document is involved.

// PlaySessionBegin snapshots the level before play starts.
func PlaySessionBegin(lvl *world.Level) {
	script.PlaySession.Valid = false
	script.PlaySession.Dirty = false
	if lvl == nil {
		return
	}
	raw, err := levelToRaw(lvl)
	if err != nil {
		// Without a snapshot there is no way back, so the honest thing is to
		// say so rather than to let a script write into an unprotected level.
		script.ScriptLog.Warnf("", "cannot snapshot the level for play (%v); script writes will not be undone", err)
		return
	}
	script.PlaySession.Authored = raw
	script.PlaySession.Valid = true
}

// PlaySessionEnd restores the authored level, if a script changed it, and
// reports whether it actually did.
//
// It deliberately does not call ApplySpawnPoint: leaving play must put the
// *level* back, not move the editor camera to the spawn. Nor does it clear the
// editor's selection — a restore invalidates every index-based selection, but
// saying so is the caller's job, because this file sits below the editor and
// must not reach up into it.
func PlaySessionEnd(lvl *world.Level) (restored bool) {
	defer func() {
		script.PlaySession.Valid = false
		script.PlaySession.Dirty = false
		script.PlaySession.Authored = world.RawLevel{}
	}()
	if lvl == nil || !script.PlaySession.Valid || !script.PlaySession.Dirty {
		return false
	}
	if err := applyRawLevel(script.PlaySession.Authored, lvl); err != nil {
		script.ScriptLog.Errorf("", "could not restore the level after play: %v", err)
		return false
	}
	script.ScriptLog.Infof("", "restored the level after play; script changes were discarded")
	return true
}
