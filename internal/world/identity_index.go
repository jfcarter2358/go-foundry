package world

// identity_index.go — minting many IDs in one level without rescanning it.
//
// uniqueLevelID asks LevelIDTaken, which walks every list in the level, for
// each candidate. That is right for the editor minting one object, and it is
// quadratic for a loader adding thousands: a level with 10k generated
// scenery placements spent its load re-walking the 9,999 already added for
// each new one (BenchmarkLoadManyScenery measures it).
//
// LevelIDIndex is the same namespace taken once into a set. Unique answers
// what uniqueLevelID would — the same base, the same _N suffixes, the same
// reserved names — and records its answer, so the next call sees it. The
// index knows only what it was built from and what it handed out: anything
// added to the level by another route meanwhile is invisible to it, so it is
// for a bounded batch (a load, a paste, a splice), not for keeping.

import "fmt"

// LevelIDIndex is a snapshot of the IDs a level uses.
type LevelIDIndex struct {
	taken map[string]bool
	// next remembers, per base, the suffix to try first, so a base minted
	// thousands of times ("scenery") does not retry _2, _3, ... each time.
	next map[string]int
}

// NewLevelIDIndex takes lvl's IDs (nil is an empty level).
func NewLevelIDIndex(lvl *Level) *LevelIDIndex {
	ix := &LevelIDIndex{taken: map[string]bool{}, next: map[string]int{}}
	eachLevelID(lvl, func(id string) bool {
		if id != "" {
			ix.taken[id] = true
		}
		return false
	})
	return ix
}

// Taken reports what LevelIDTaken would, against the snapshot.
func (ix *LevelIDIndex) Taken(id string) bool {
	return id == "" || reservedLevelIDs[id] || ix.taken[id]
}

// Add records id as used.
func (ix *LevelIDIndex) Add(id string) {
	if id != "" {
		ix.taken[id] = true
	}
}

// Unique is uniqueLevelID against the snapshot — base, or base_N for the
// first free N >= 2 — and records the answer. An empty base is fallback.
func (ix *LevelIDIndex) Unique(base, fallback string) string {
	if base == "" {
		base = fallback
	}
	if base == "" {
		base = "object"
	}
	if !ix.Taken(base) {
		ix.Add(base)
		return base
	}
	n := max(ix.next[base], 2)
	for ; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !ix.Taken(id) {
			ix.next[base] = n + 1
			ix.Add(id)
			return id
		}
	}
}
