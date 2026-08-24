package script

import "foundry/internal/world"

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
var PlaySession struct {
	Authored world.RawLevel
	Valid    bool
	Dirty    bool
}

// PlaySessionMarkDirty records that something has written authored state. Called
// from every mutating host method.
func PlaySessionMarkDirty() {
	if PlaySession.Valid {
		PlaySession.Dirty = true
	}
}

// PlaySessionDirty reports whether the current play session has changed anything.
func PlaySessionDirty() bool { return PlaySession.Valid && PlaySession.Dirty }
