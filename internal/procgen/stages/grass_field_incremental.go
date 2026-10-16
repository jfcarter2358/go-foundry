package stages

// grass_field_incremental.go — grass_field's per-tile reruns
// (docs/terrain-paint/PHASE_P2.md; the engine side is procgen/incremental.go
// and procgen/paint_incremental.go).
//
// grass_field is an Incremental kind. Its fields are one per terrain tile
// and each is a pure function of the tile's rectangle: place.Grass decides
// every clump from its own lattice cell (position, weight, kind, scale and
// tint are hashes of the cell and noise at the point), so nothing outside
// the rectangle — beyond the bilinear reach of a paint sample, and the
// avoid stage's footprints — can change it. A tile's key is therefore:
//
//   - the stage's base hash (its params, seed and upstream base hashes:
//     the kit, the rules, the terrain, the water, the avoid stage's
//     settings), which no stroke moves;
//   - its name and rectangle;
//   - the paint its clumps can read (Env.PaintKey over the rectangle):
//     the stage's own layers within a texel, and a layer only the avoid
//     stage reads (a scatter that reads paint) within
//     procgen.UpstreamPaintReach, since those footprints follow paint in
//     their own neighbourhood.
//
// A tile whose key matches last run's is not placed again: its .fgrass is
// kept untouched, its volume is last run's (PreviousVolumes, byte for byte
// what a full run writes, since a full run writes the same entry from the
// same field), and its counts come from the field's runs. A tile that held
// no clump last time and whose key matches stays empty without a file.
// The kit is rebuilt every run (it is the same bytes for the same base
// hash, and the fields name it by path).

import (
	"fmt"
	"os"
	"path/filepath"

	"foundry/internal/grassfield"
	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
)

// grassTileVersion changes when a field's bytes change for the same inputs.
const grassTileVersion = "grass-tile-1"

// grassTileOut is one tile's field as the run gathers them.
type grassTileOut struct {
	name string
	vol  procgen.VolumeEntry
	n    int
	rule map[int]int
}

// grassTileKey is the tile's input key (file comment).
// obs is the ground_edit's placed rocks (ground_obstacles.go): those that
// reach the tile are folded in (none: the key is as it always was).
func grassTileKey(rc *procgen.Env, name string, rect [4]float64, obs ...place.Disc) string {
	parts := []string{name, fmt.Sprintf("%g,%g,%g,%g", rect[0], rect[1], rect[2], rect[3]), rc.PaintKey(rect, 0)}
	if ok := obstacleKey(obs, rect, 0.5); ok != "" {
		parts = append(parts, ok)
	}
	return rc.PieceKey(grassTileVersion, parts...)
}

// reuseGrassTile keeps last run's tile when its key matches: (the tile,
// true) for one with a field, (nil, true) for one that was empty, false
// when it must be placed again.
func reuseGrassTile(rc *procgen.RunContext, name, key string) (*grassTileOut, bool, error) {
	if prev, ok := rc.PreviousTile(name); !ok || prev != key {
		return nil, false, nil
	}
	vol, had := rc.PreviousVolumes()[name]
	if !had {
		// Empty last time, and nothing it reads has changed.
		return nil, rc.ReuseTile(name, key), nil
	}
	file := name + grassfield.FieldExt
	data, err := os.ReadFile(filepath.Join(rc.Dir, file))
	if err != nil {
		return nil, false, nil // gone: place it again
	}
	f, err := grassfield.DecodeField(data)
	if err != nil {
		return nil, false, nil
	}
	if !rc.ReuseTile(name, key, file) {
		return nil, false, nil
	}
	o := &grassTileOut{name: name, vol: vol, n: f.Count(), rule: map[int]int{}}
	for r, run := range f.Runs {
		if run.Count > 0 && f.Kinds > 0 {
			o.rule[r%f.Kinds] += int(run.Count)
		}
	}
	return o, true, nil
}
