package main

// script_play.go — keeping play from dirtying the authored level.
//
// A script writes attributes and properties. Without this, pressing `~` to play,
// letting a door open and a crate take damage, and pressing `~` again would
// leave the level permanently changed — and the next Ctrl+Z would restore a
// snapshot taken *before* the play session, silently discarding whatever editor
// work came before it.
//
// So: snapshot on entering play, restore on leaving. The snapshot is a RawLevel
// rather than a struct copy for the reason editor_undo.go already gives —
// runtime geometry holds live GL buffer handles that a struct copy would *alias*
// rather than duplicate, so freeing the copy would free the original's buffers.
// That is also why Chunk.Clone zeroes its GPU handles. Reusing levelToRaw and
// rawToLevel means one serialisation path, already proven by the undo stack.
//
// Restoring is gated on something having actually been written, so a play
// session that changed nothing costs nothing to leave.

// playSession is the authored level, held for the duration of a play session.
var playSession struct {
	authored RawLevel
	valid    bool
	dirty    bool
}

// PlaySessionBegin snapshots the level before play starts.
func PlaySessionBegin(lvl *Level) {
	playSession.valid = false
	playSession.dirty = false
	if lvl == nil {
		return
	}
	raw, err := levelToRaw(lvl)
	if err != nil {
		// Without a snapshot there is no way back, so the honest thing is to
		// say so rather than to let a script write into an unprotected level.
		ScriptLog.Warnf("", "cannot snapshot the level for play (%v); script writes will not be undone", err)
		return
	}
	playSession.authored = raw
	playSession.valid = true
}

// PlaySessionMarkDirty records that something has written authored state. Called
// from every mutating host method.
func PlaySessionMarkDirty() {
	if playSession.valid {
		playSession.dirty = true
	}
}

// PlaySessionDirty reports whether the current play session has changed anything.
func PlaySessionDirty() bool { return playSession.valid && playSession.dirty }

// PlaySessionEnd restores the authored level, if a script changed it, and
// reports whether it actually did.
//
// It deliberately does not call ApplySpawnPoint: leaving play must put the
// *level* back, not move the editor camera to the spawn. Nor does it clear the
// editor's selection — a restore invalidates every index-based selection, but
// saying so is the caller's job, because this file sits below the editor and
// must not reach up into it.
func PlaySessionEnd(lvl *Level) (restored bool) {
	defer func() {
		playSession.valid = false
		playSession.dirty = false
		playSession.authored = RawLevel{}
	}()
	if lvl == nil || !playSession.valid || !playSession.dirty {
		return false
	}
	if err := applyRawLevel(playSession.authored, lvl); err != nil {
		ScriptLog.Errorf("", "could not restore the level after play: %v", err)
		return false
	}
	ScriptLog.Infof("", "restored the level after play; script changes were discarded")
	return true
}
