package procgen

// manifest.go — what the output folder holds, and from which hash.
//
// <output>/manifest.yaml records, per stage, the hash its outputs were made
// from and every file it wrote (relative to the output folder). It is the
// whole of the incremental-run machinery's memory: a stage whose current hash
// matches its entry, and whose files are all still on disk, is skipped.
//
// The manifest is rewritten after every stage, not once at the end, so a run
// that is cancelled or fails half way keeps the stages it finished. A stage
// that did not finish loses its entry before it starts writing, so its
// half-made files can never be mistaken for a fresh result.
//
//	version: 1
//	generator: desert_canyon
//	stages:
//	  ground:
//	    kind: terrain
//	    hash: 3f2a...
//	    files: [ground/heightmap.png, ground/placements.yaml, ground/tile_0_0.fnm]
//	    tiles: {tile_0_0: 9c1e...}   # an Incremental stage only (incremental.go)

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

// ManifestFileName is the manifest's name inside the output folder.
const ManifestFileName = "manifest.yaml"

// ManifestVersion is the manifest format's version; a manifest of another is
// treated as empty (everything reruns).
const ManifestVersion = 1

// Manifest is the output folder's record.
type Manifest struct {
	Version   int                      `yaml:"version"`
	Generator string                   `yaml:"generator,omitempty"`
	Stages    map[string]ManifestStage `yaml:"stages"`
}

// ManifestStage is one stage's entry.
type ManifestStage struct {
	Kind  string   `yaml:"kind"`
	Hash  string   `yaml:"hash"`
	Files []string `yaml:"files,flow"`
	// Tiles is an Incremental stage's per-tile hashes, by tile name
	// (incremental.go): what each of its tiles was made from, so the next
	// run reuses a tile whose inputs did not change.
	Tiles map[string]string `yaml:"tiles,omitempty"`
}

// ReadManifest reads dir's manifest. A missing, unreadable or other-version
// one is an empty manifest, not an error: the cost is a rerun.
func ReadManifest(dir string) *Manifest {
	m := &Manifest{Version: ManifestVersion, Stages: map[string]ManifestStage{}}
	data, err := os.ReadFile(filepath.Join(dir, ManifestFileName))
	if err != nil {
		return m
	}
	var got Manifest
	if yaml.Unmarshal(data, &got) != nil || got.Version != ManifestVersion {
		return m
	}
	if got.Stages == nil {
		got.Stages = map[string]ManifestStage{}
	}
	return &got
}

// Write saves the manifest into dir.
func (m *Manifest) Write(dir string) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return writeFileAtomicNoBackup(filepath.Join(dir, ManifestFileName), data)
}

// fresh reports whether entry describes outputs made from hash that are all
// still on disk under dir.
func (e ManifestStage) fresh(dir, hash string) bool {
	if e.Hash != hash {
		return false
	}
	for _, f := range e.Files {
		if !fileExists(filepath.Join(dir, filepath.FromSlash(f))) {
			return false
		}
	}
	return true
}

// removeFiles deletes an entry's files, and its stage folder if that leaves
// it empty. Errors are ignored: a file already gone is the goal.
func (e ManifestStage) removeFiles(dir string) {
	dirs := map[string]bool{}
	for _, f := range e.Files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		os.Remove(p)
		dirs[filepath.Dir(p)] = true
	}
	for d := range dirs {
		if d != dir {
			os.Remove(d) // only succeeds when empty
		}
	}
}
