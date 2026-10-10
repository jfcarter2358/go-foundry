package video

// video_registry.go — the players that currently exist, and when they stop.
//
// A video widget is a description in a document; a player is an open file, a
// decode buffer and possibly a voice. The two have very different lifetimes: a
// screen is shown and hidden and reloaded, and a document is cloned every time
// somebody opens it, so the player cannot live on the node. It lives here,
// under a key its owner chooses, and is closed when nobody asks for it any more.
//
// "Nobody asks for it any more" is a generation stamp rather than a set of live
// keys handed in each frame. The caller is already walking every node it cares
// about, so Acquire can mark as it goes and Sweep can close whatever was not
// marked — no allocation per frame, and no way to forget to release something.

import "sync"

var (
	regMu   sync.Mutex
	players = map[string]*entry{}
	gen     uint64
)

type entry struct {
	p    *Player
	seen uint64
}

// Acquire returns the player for a key, opening the source the first time and
// re-opening it if the key has been pointed at a different video since.
//
// Marking is a side effect of asking, which is the point: a caller that walks
// its live widgets and acquires each one has, by the time it is done, told the
// registry exactly which players are still wanted.
func Acquire(key, source string) *Player {
	regMu.Lock()
	defer regMu.Unlock()

	e, ok := players[key]
	if ok && e.p.Source != source {
		// The widget has been repointed — by a script, or by the editor's
		// picker. The old file is closed rather than left open behind a player
		// nothing can reach.
		e.p.Close()
		ok = false
	}
	if !ok {
		e = &entry{p: Open(source)}
		players[key] = e
	}
	e.seen = gen
	return e.p
}

// Release closes one player and forgets it.
func Release(key string) {
	regMu.Lock()
	defer regMu.Unlock()
	if e, ok := players[key]; ok {
		e.p.Close()
		delete(players, key)
	}
}

// Sweep closes every player not acquired since the last Sweep, and starts a new
// generation.
//
// Called once a frame, after the walk that acquires. A screen that was hidden,
// a widget that was deleted and a whole stack that was reset all reach this the
// same way: nothing asked for the player, so it goes.
func Sweep() {
	regMu.Lock()
	defer regMu.Unlock()
	for k, e := range players {
		if e.seen != gen {
			e.p.Close()
			delete(players, k)
		}
	}
	gen++
}

// CloseAll closes every player. For shutdown, and for a test that wants a clean
// registry — it is the only thing in here that does not wait a generation.
func CloseAll() {
	regMu.Lock()
	defer regMu.Unlock()
	for k, e := range players {
		e.p.Close()
		delete(players, k)
	}
}

// Held reports whether a key still has a player. For a caller keeping its own
// per-player state — an uploaded texture revision — to sweep alongside this
// one rather than growing forever.
func Held(key string) bool {
	regMu.Lock()
	defer regMu.Unlock()
	_, ok := players[key]
	return ok
}

// OpenCount is how many players are held, for the debug overlay and for tests.
func OpenCount() int {
	regMu.Lock()
	defer regMu.Unlock()
	return len(players)
}
