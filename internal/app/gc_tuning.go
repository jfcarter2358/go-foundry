package app

// gc_tuning.go — how often the garbage collector runs while the game plays.
//
// Go's default (GOGC=100) collects whenever the heap has doubled since the last
// collection. A level's live heap is large and full of pointers — brush faces
// kept on the CPU for raycasts, models, nav grids, compiled surfaces — so every
// cycle's mark phase is long, and the assists it charges to allocating
// goroutines land on the main thread mid-frame. The frame itself allocates
// steadily (script calls, event queues), so at the default the collector is
// running most of the time a level is being played.
//
// Raising GOGC trades memory for fewer cycles: at 300 the heap may grow to four
// times what is live before a collection, so a cycle comes a third as often.
// The memory limit is the backstop that keeps that from running away: it is set
// from the live heap measured once a level has loaded, and is soft — past it the
// runtime collects more often rather than failing.
//
// Either knob set in the environment (GOGC, GOMEMLIMIT) wins: someone measuring
// with them set means them.

import (
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
)

// gameGCPercent is GOGC while a level plays.
const gameGCPercent = 300

// gcLimitFloor is the least the soft memory limit is ever set to, so a tiny
// level (the main menu) does not end up with a limit it brushes against as soon
// as the next level starts loading into it.
const gcLimitFloor = 2 << 30

// tuneGC applies gameGCPercent at startup, unless GOGC is set.
func tuneGC() {
	if _, set := os.LookupEnv("GOGC"); set {
		return
	}
	debug.SetGCPercent(gameGCPercent)
}

// retuneGCAfterLoad runs once a level has been installed, behind the frame the
// loading screen goes away in. It collects what the outgoing level left — the
// one moment a full collection costs nothing anyone sees, rather than the first
// cycle landing a few seconds into play — and then sets the soft memory limit
// from what the new level actually keeps live.
func retuneGCAfterLoad() {
	runtime.GC()
	if _, set := os.LookupEnv("GOMEMLIMIT"); set {
		return
	}
	s := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return
	}
	live := int64(s[0].Value.Uint64())
	// Room for the heap GOGC lets grow (live × (1 + GOGC/100)), plus headroom
	// for what the runtime maps beyond the heap.
	limit := live*(1+gameGCPercent/100) + 1<<30
	if limit < gcLimitFloor {
		limit = gcLimitFloor
	}
	debug.SetMemoryLimit(limit)
	log.Printf("gc: live heap %d MB after load, GOGC=%d, soft memory limit %d MB",
		live>>20, gameGCPercent, limit>>20)
}
