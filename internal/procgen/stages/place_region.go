package stages

// place_region.go — the placement stages as environment-region stages
// (docs/regions/SPEC.md, "Biome-weighted placement"; docs/regions/PHASE_4.md).
//
// A level generation doc expands each region's preset: every stage of a
// PerRegion kind runs once per region as r_<region>_<stage>, over the
// level doc's shared union terrain (region_terrain) and region material
// (region_material) — the preset's own terrain and terrain_material
// stages are rewritten to them (procgen/regional.go) — with Env.Region
// set. These kinds are PerRegion:
//
//	scatter          every candidate's weight × the region's weight there;
//	                 world-aligned 32 m blocks, seeded by world block
//	                 (place.Region; why nothing clips to the band
//	                 rectangle is in place/region.go)
//	vegetation       each cell's weight × the region's weight; the cell
//	                 grid from a world multiple of the cell
//	cliff_wall       columns where the region's weight is at least ½ (two
//	                 regions' walls meet in the band's middle, never both
//	                 on one face); the
//	                 preset's splines, region rectangle and focus are in the
//	                 preset's coordinates, moved by the region's anchor
//	wall_sheet       the shell's stand-out × the region's weight: a wall
//	                 sinks back into the terrain across the band
//	rocks            generated rock variants, cooked once per preset
//	                 stage and parameters into a shared cache (below)
//	polyhaven_rocks, polyhaven_plants, polyhaven_textures
//	                 imports into resources/models|textures/polyhaven/<id>
//	                 as always (shared by construction), and an import
//	                 already made in this process is reused (polyhaven.go)
//
// drift stays RegionalNone: it recooks the terrain's tiles, which belong to
// the shared region_terrain, so a region cannot own them; the expansion
// skips it with a log line.
//
// Without Env.Region (every classic document) nothing here applies and
// every stage's output is what it was, to the byte (tests pin it).
//
// THE SHARED ROCKS CACHE. Two regions growing one preset would otherwise
// generate and cook its rocks twice (the region's seed differs). A rocks
// stage expanded for a region instead seeds its variants from the preset
// stage alone — the doc's seed, the preset stage's ID and its seed param,
// not the region — and keeps them under <out>/_shared/rocks_<hash>/, the
// hash over the kind, its version, that seed and the resolved params. The
// first region to run makes them; every other region with the same preset
// stage and parameters finds them there and only writes its manifest (and
// its material), whose variants point at the shared files. A stage edit is
// a new hash, so a new folder; the old ones stay until deleted (the run
// engine prunes stage folders, and _shared is no stage's).

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"sync"

	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
)

// GroundSources are the stage kinds a placement stage's `ground` may name:
// a terrain's ground material, or the region material of a level
// generation doc (whose artifact is the same TerrainMaterialArtifact,
// with eight layers when its environments need them).
var GroundSources = []string{TerrainMaterialKind, RegionMaterialKind}

// placeRegion is the run's region as place sees it; nil for a classic run.
func placeRegion(env *procgen.Env) *place.Region {
	r := env.Region
	if r == nil {
		return nil
	}
	return &place.Region{Weight: r.Weight}
}

// regionWallKeep is cliff_wall's column filter for a region (nil: none).
func regionWallKeep(env *procgen.Env) func(x, z float64) bool {
	reg := placeRegion(env)
	if reg == nil {
		return nil
	}
	return func(x, z float64) bool { return reg.At(x, z) >= 0.5 }
}

// presetPoint is a point of the preset's own coordinates in the world: the
// region's anchor added (zero for a classic run).
func presetPoint(env *procgen.Env, p [2]float64) [2]float64 {
	if env.Region == nil {
		return p
	}
	return [2]float64{p[0] + env.Region.Anchor[0], p[1] + env.Region.Anchor[1]}
}

// presetStageID is the preset stage an expanded stage came from: its ID
// without the r_<region>_ prefix (the ID itself for a classic run).
func presetStageID(env *procgen.Env) string {
	if env.Region == nil || env.Stage == nil {
		if env.Stage == nil {
			return ""
		}
		return env.Stage.ID
	}
	return strings.TrimPrefix(env.Stage.ID, procgen.RegionStageID(env.Region.Key, ""))
}

// sharedAssetSeed is an expanded asset stage's seed without its region:
// the doc's seed, the preset stage's ID and the kind's seed params —
// procgen.StageSeed as the preset itself would seed the stage.
func sharedAssetSeed(env *procgen.Env) uint64 {
	var docSeed int64
	if env.Doc != nil {
		docSeed = env.Doc.Seed
	}
	return procgen.StageSeed(docSeed, presetStageID(env), env.Kind.Params, env.Params)
}

// sharedAssetDir is where an expanded asset stage's region-independent
// outputs live: <out>/_shared/<kind>_<hash>, absolute and resources-
// relative, the hash over the kind, its version, the shared seed and the
// resolved params (so a parameter edit is a new folder).
func sharedAssetDir(rc *procgen.RunContext, seed uint64) (dir, rel string) {
	h := sha256.New()
	h.Write([]byte(procgen.StageHash(rc.Kind, seed, rc.Params, nil)))
	key := rc.Kind.Name + "_" + hex.EncodeToString(h.Sum(nil))[:16]
	root := filepath.Dir(rc.Dir)
	relRoot := rc.RelDir[:max(strings.LastIndex(rc.RelDir, "/"), 0)]
	return filepath.Join(root, "_shared", key), relRoot + "/_shared/" + key
}

// sharedAssetLocks serialises the makers of one shared folder (a run is
// serial, but the editor and a CLI run may share a process's packages).
var sharedAssetLocks sync.Map

func sharedAssetLock(dir string) *sync.Mutex {
	m, _ := sharedAssetLocks.LoadOrStore(dir, &sync.Mutex{})
	return m.(*sync.Mutex)
}
