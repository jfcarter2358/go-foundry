package procgen

// incremental.go — stages that keep their files across runs and reuse the
// tiles that did not change (docs/regions/SPEC.md, "Incremental
// regeneration").
//
// The run engine's unit of freshness is the stage: a stale stage loses its
// manifest entry and every file before it runs again. That is right for a
// stage whose output is one thing, and wasteful for a terrain whose output
// is forty cooked tiles of which an edit at the far end changed four. A kind
// marked StageKind.Incremental opts out of the wipe:
//
//   - When it reruns (not under Force, which rebuilds from nothing), the
//     engine keeps its files on disk and hands it the previous manifest
//     entry as RunContext.Previous: the files, and Tiles, the per-tile
//     hashes it recorded last time.
//   - While it runs, the manifest entry on disk claims NOTHING fresh: no
//     hash and no tiles, only the file list (so a later prune still finds
//     them). A run killed outright leaves exactly that: the next run
//     rebuilds every tile, which is slow but never wrong.
//   - The stage computes each tile's hash from that tile's inputs and calls
//     ReuseTile(name, hash, files...): when the previous run recorded the
//     same hash and every file is still there, the files join this run's
//     outputs untouched and the stage skips the tile's work. Otherwise it
//     calls BeginTile(name) BEFORE it writes the tile's files and
//     SetTile(name, hash) once they are written.
//   - On success the new entry (hash, files, tiles) is recorded and the
//     previous files this run neither wrote nor reused are deleted.
//   - On failure or cancellation the entry records the previous tiles the
//     run never touched plus the ones it finished (their files are whole),
//     still with no stage hash — the stage is stale, and the next run
//     reuses only tiles that are certainly what their hash says.
//
// Tile names are the stage's own (region_terrain uses tile_<i>_<j>); a hash
// is any string the stage chooses.

import (
	"os"
	"path"
	"path/filepath"
)

// PreviousTile is the hash the previous run recorded for a tile, if any.
func (rc *RunContext) PreviousTile(name string) (string, bool) {
	if rc.Previous == nil {
		return "", false
	}
	h, ok := rc.Previous.Tiles[name]
	return h, ok
}

// ReuseTile keeps a tile from the previous run when its recorded hash is
// hash and every one of its files (names in the stage's folder) is still on
// disk: the files are recorded as this run's outputs, the tile's hash
// carried over, and true returned. Safe from Parallel workers.
func (rc *RunContext) ReuseTile(name, hash string, files ...string) bool {
	if prev, ok := rc.PreviousTile(name); !ok || prev != hash || hash == "" {
		return false
	}
	for _, f := range files {
		if !fileExists(filepath.Join(rc.Dir, filepath.FromSlash(f))) {
			return false
		}
	}
	for _, f := range files {
		rc.Path(f)
	}
	rc.mu.Lock()
	if rc.tiles == nil {
		rc.tiles = map[string]string{}
	}
	rc.tiles[name] = hash
	rc.mu.Unlock()
	return true
}

// BeginTile says the tile's files are about to be (re)written: if the run
// fails from here, the previous run's hash for it no longer vouches for
// them.
func (rc *RunContext) BeginTile(name string) {
	rc.mu.Lock()
	if rc.touched == nil {
		rc.touched = map[string]bool{}
	}
	rc.touched[name] = true
	rc.mu.Unlock()
}

// SetTile records a tile's hash once its files are written.
func (rc *RunContext) SetTile(name, hash string) {
	rc.mu.Lock()
	if rc.tiles == nil {
		rc.tiles = map[string]string{}
	}
	rc.tiles[name] = hash
	rc.mu.Unlock()
}

// tileHashes is the tiles recorded this run, or nil.
func (rc *RunContext) tileHashes() map[string]string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if len(rc.tiles) == 0 {
		return nil
	}
	out := make(map[string]string, len(rc.tiles))
	for k, v := range rc.tiles {
		out[k] = v
	}
	return out
}

// survivingTiles is what a failed incremental run may still vouch for: the
// previous tiles it never touched, and the ones it finished.
func (rc *RunContext) survivingTiles() map[string]string {
	out := map[string]string{}
	rc.mu.Lock()
	if rc.Previous != nil {
		for k, v := range rc.Previous.Tiles {
			if !rc.touched[k] {
				out[k] = v
			}
		}
	}
	for k, v := range rc.tiles {
		out[k] = v
	}
	rc.mu.Unlock()
	if len(out) == 0 {
		return nil
	}
	return out
}

// unionFiles is a ∪ b, a's order then b's new ones.
func unionFiles(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range [][]string{a, b} {
		for _, f := range l {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// removeStale deletes the files of prev not in keep (and their folders when
// that empties them, never the stage's own folder's parent).
func removeStale(dir string, prev, keep []string) {
	k := map[string]bool{}
	for _, f := range keep {
		k[f] = true
	}
	var gone []string
	for _, f := range prev {
		if !k[f] {
			gone = append(gone, f)
		}
	}
	ManifestStage{Files: gone}.removeFiles(dir)
	// removeFiles removes only the immediate folders; a stage's nested
	// folders (an erosion cache) may need one more level.
	for _, f := range gone {
		if d := path.Dir(path.Dir(f)); d != "." && d != "/" {
			os.Remove(filepath.Join(dir, filepath.FromSlash(d)))
		}
	}
}
