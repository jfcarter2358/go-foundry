package stages

// scatter_incremental.go — scatter's per-tile reruns
// (docs/terrain-paint/PHASE_P2.md; the tiles themselves are
// place/scatter_tiled.go, the engine side procgen/incremental.go and
// procgen/paint_incremental.go).
//
// A scatter that reads paint — a rule's paint band, an exclude layer, or
// anything above it that reads a layer (a ground stage, an avoid stage) —
// scatters in TILED mode: 64 m world tiles, the paint tiles, each a pure
// function of its own inputs and its earlier-phase neighbours' rocks. Each
// tile is cached in the stage's folder as tiles/t_<i>_<j>.bin (the exact
// instances and the tile's counts, place.EncodeScatterTile) under the key
// place builds from:
//
//   - the tile's own inputs, scatterTileKey: the stage's base hash (rules,
//     variants, terrain, avoid settings, seed — nothing a stroke moves),
//     the tile, and the paint its candidates can read (Env.PaintKey over
//     the tile: its own layers within a texel, upstream-only layers within
//     procgen.UpstreamPaintReach);
//   - and the output hashes of its earlier-phase neighbours.
//
// A stroke therefore re-scatters the tiles it painted, and their later-
// phase neighbours only while the rocks keep changing.
//
// FOOTPRINTS. A tiled scatter hands its readers (a grass_field's avoid)
// its footprints rounded as footprints.yaml stores them, and says so
// (RunContext.ArtifactAsLoaded): a reader's pieces are then the same whether
// the scatter ran this time or was read back, and a stroke that re-scatters
// a tile does not make the grass rebuild every field.
//
// IDs. A tiled scatter numbers each rule's rocks per tile:
// <rule>_<i>_<j>_<nnnn> (a negative coordinate spelt m<n>, so an ID stays a
// plain word), so a re-scattered tile's rocks never renumber another
// tile's. The placements of an untouched tile are byte-identical from one
// run to the next, which is what lets the editor's live splice replace only
// the placements that changed.
//
// Tiled mode changes where rocks land compared with the classic scatter
// (the keep step no longer spans the whole ground), so it is ON only for a
// scatter that reads paint — every document from before paint existed,
// and every scatter that reads none, scatters exactly as it did. A scatter
// whose largest footprint reaches half a tile (64 m tiles: a 16 m boulder at
// spacing 1) cannot be tiled: it falls back to the classic scatter, whole,
// and says so in the log.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/place"
)

// scatterTileVersion changes when a tile's rocks change for the same
// inputs.
const scatterTileVersion = "scatter-tile-1"

// runScatterPlace is place.Scatter, tiled and cached when the stage reads
// paint (file comment).
// obs is the ground_edit's placed rocks (ground_obstacles.go), folded into
// each tile's key.
func runScatterPlace(rc *procgen.RunContext, g *place.Ground, set place.ScatterSettings, obs []place.Disc) (*place.ScatterResult, error) {
	if !rc.ReadsPaint() {
		return place.Scatter(g, set, &rc.Env)
	}
	tiled := set
	tiled.Tiles = paint.TileSize
	tiled.TileKey = func(i, j int) string { return scatterTileKey(rc, i, j, obs) }
	tiled.Cache = scatterTileCache{rc: rc}
	res, err := place.Scatter(g, tiled, &rc.Env)
	if errors.Is(err, place.ErrTiledReach) {
		rc.Logf("not tiled (%v): the classic scatter, rerun whole after every stroke", err)
		return place.Scatter(g, set, &rc.Env)
	}
	if err != nil {
		return nil, err
	}
	reused := 0
	for _, t := range res.Tiles {
		if t.Reused {
			reused++
		}
	}
	rc.Logf("tiled: %d tiles, %d reused (nothing they read changed)", len(res.Tiles), reused)
	return res, nil
}

// scatterTileKey is tile (i, j)'s own inputs' key.
func scatterTileKey(rc *procgen.RunContext, i, j int, obs []place.Disc) string {
	k := paint.TileKey{I: i, J: j}
	// The placed rocks that reach the tile or a rock of it (phase W4): ""
	// adds nothing, so a tile far from every one keeps its key.
	if ok := obstacleKey(obs, k.Rect(), scatterObstaclePad); ok != "" {
		return rc.PieceKey(scatterTileVersion, k.String(), rc.PaintKey(k.Rect(), 0), ok)
	}
	return rc.PieceKey(scatterTileVersion, k.String(), rc.PaintKey(k.Rect(), 0))
}

// scatterObstaclePad is how far outside a tile a placed rock may stand and
// still change it (a rock of the tile's footprint reaching out to it).
const scatterObstaclePad = 8.0

// scatterTileName is tile (i, j)'s name in the manifest, and its file.
func scatterTileName(i, j int) (string, string) {
	name := fmt.Sprintf("t_%d_%d", i, j)
	return name, "tiles/" + name + ".bin"
}

// scatterTileCache keeps tiles in the stage's folder.
type scatterTileCache struct{ rc *procgen.RunContext }

func (c scatterTileCache) Load(i, j int, key string) (*place.ScatterTile, bool) {
	name, file := scatterTileName(i, j)
	if prev, ok := c.rc.PreviousTile(name); !ok || prev != key {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(c.rc.Dir, filepath.FromSlash(file)))
	if err != nil {
		return nil, false
	}
	t, err := place.DecodeScatterTile(data)
	if err != nil || !c.rc.ReuseTile(name, key, file) {
		return nil, false
	}
	return t, true
}

func (c scatterTileCache) Store(t *place.ScatterTile, key string) error {
	if key == "" {
		return nil
	}
	name, file := scatterTileName(t.I, t.J)
	c.rc.BeginTile(name)
	if err := c.rc.WriteFile(file, place.EncodeScatterTile(t)); err != nil {
		return err
	}
	c.rc.SetTile(name, key)
	return nil
}

// scatterIDs is a tiled scatter's placement IDs, one per instance (file
// comment); nil for the classic scatter, which numbers by rule.
func scatterIDs(res *place.ScatterResult, rules []place.Rule) []string {
	if res.Tiles == nil {
		return nil
	}
	out := make([]string, 0, len(res.Instances))
	for _, t := range res.Tiles {
		counts := make([]int, len(rules))
		for k := range t.Instances {
			in := &t.Instances[k]
			counts[in.Rule]++
			out = append(out, fmt.Sprintf("%s_%s_%s_%04d", rules[in.Rule].Name, tileCoord(t.I), tileCoord(t.J), counts[in.Rule]))
		}
	}
	return out
}

// tileCoord spells a tile coordinate for an ID: 3, or m2 for -2.
func tileCoord(v int) string {
	if v < 0 {
		return fmt.Sprintf("m%d", -v)
	}
	return fmt.Sprint(v)
}

// roundedFootprints is discs as footprints.yaml stores (and Load reads)
// them: every number to the centimetre.
func roundedFootprints(discs []place.Disc) *FootprintsArtifact {
	a := &FootprintsArtifact{Discs: make([]place.Disc, len(discs))}
	for i, d := range discs {
		a.Discs[i] = place.Disc{X: round2(d.X), Z: round2(d.Z), R: round2(d.R)}
	}
	return a
}
