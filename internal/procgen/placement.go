package procgen

// placement.go — what a stage puts into a level.
//
// A stage does not touch a level. It emits Placements (static scenery: a
// mesh, a transform, how it collides, which chunk it streams with) and, where
// it needs them, raw volume entries, and the run engine writes them to the
// stage's placements file beside its other outputs. procgen/genlevel then
// splices every enabled stage's placements into the target level, each ID
// prefixed gen.<generator>.<stage>., replacing whatever carried the
// generator's prefix before.
//
// Placement mirrors world.RawScenery field for field, and its YAML keys are
// the same, but is declared here so this package stays below internal/world.
// A volume is carried as the loose map a level file would hold (VolumeEntry):
// volumes have dozens of kind-specific keys, and genlevel decodes the map
// into world.RawVolume through the level's own YAML schema, so a stage can
// write any volume the level format can say without this package mirroring
// that schema too.

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

// Point3 is a position, Euler rotation or scale, keyed x/y/z like a level's.
type Point3 struct {
	X float32 `yaml:"x"`
	Y float32 `yaml:"y"`
	Z float32 `yaml:"z"`
}

// Placement is one static scenery placement.
type Placement struct {
	// ID is the stage's own name for it, unique within the stage (the run
	// numbers any left blank). The level sees StagePrefix + ID.
	ID string `yaml:"id"`
	// Model is the mesh, resources-relative: usually one of the stage's own
	// outputs (RunContext.AssetPath), a cooked .fnm.
	Model    string `yaml:"model"`
	Position Point3 `yaml:"position"`
	// Rotation is Euler radians, applied Y then X then Z, as scenery's is.
	Rotation Point3  `yaml:"rotation,omitempty"`
	Scale    *Point3 `yaml:"scale,omitempty"`
	// Solid nil is solid.
	Solid *bool `yaml:"solid,omitempty"`
	// Collision is auto (empty), box, hulls, mesh or none.
	Collision string `yaml:"collision,omitempty"`
	// Materials maps a cooked mesh's slot names to textures.
	Materials map[string]string `yaml:"materials,omitempty"`
	// Material is the .fmat every slot wears (world.Scenery.Material), or
	// empty for none — the terrain_material stage's splat on a tile, a rock's
	// ground blend.
	Material string `yaml:"material,omitempty"`
	// Chunk is the chunk the placement streams with, or empty for always in.
	// ChunkAuto asks the splice to pick the chunk whose footprint holds the
	// position (world.SceneryChunkAt), where it can see the level's chunks.
	Chunk string `yaml:"chunk,omitempty"`
	// Wind is the sway, or nil for still.
	Wind *PlacementWind `yaml:"wind,omitempty"`
	// DrawDistance and ShadowDistance are how far from the camera it is
	// drawn and casts (world.Scenery's; 0: no limit, as drawn); Impostor
	// its far cards and ImpostorDistance where they take over
	// (docs/regions/PRESETS_5.md).
	DrawDistance     float32 `yaml:"draw_distance,omitempty"`
	ShadowDistance   float32 `yaml:"shadow_distance,omitempty"`
	Impostor         string  `yaml:"impostor,omitempty"`
	ImpostorDistance float32 `yaml:"impostor_distance,omitempty"`
}

// ChunkAuto is Placement.Chunk's "pick it for me".
const ChunkAuto = "auto"

// PlacementWind is a placement's wind: key.
type PlacementWind struct {
	Bend    float32 `yaml:"bend,omitempty"`
	Flutter float32 `yaml:"flutter,omitempty"`
}

// Emitter is one particle emitter a stage places: a `.fpar` effect at a
// point, as a level's `emitters:` entry (world.RawEmitter) holds it, with
// the few keys a generator needs. A waterfall's mist at its plunge pool is
// the first (ground_edit, docs/terrain-paint/PHASE_W4.md). Its ID is
// prefixed like a placement's, and the splice replaces the generator's
// emitters as it replaces its scenery.
type Emitter struct {
	ID string `yaml:"id"`
	// Effect names a document under particles/, without the extension.
	Effect   string `yaml:"effect"`
	Position Point3 `yaml:"position"`
	// Rotation is Euler radians (it aims the emission shape).
	Rotation Point3 `yaml:"rotation,omitempty"`
	// RateScale multiplies every layer's rate (0: as authored).
	RateScale float32 `yaml:"rate_scale,omitempty"`
}

// Sound is one positional sound a stage places: a looping clip under
// sounds/ at a point, as a level's `sounds:` entry (world.RawSound) holds
// it, with the keys a generator needs. A river's flow, a rapids' rush, a
// waterfall's roar and a pond's lapping are the first (ground_edit, phase
// W5, docs/terrain-paint/PHASE_W5.md). Prefixed like a placement's, and the
// splice replaces the generator's sounds as it replaces its emitters.
type Sound struct {
	ID string `yaml:"id"`
	// Clip names a file under sounds/, without the extension.
	Clip     string `yaml:"clip"`
	Position Point3 `yaml:"position"`
	// Gain and Pitch multiply the clip's (0: 1). RefDistance and
	// MaxDistance are the full-gain and silent radii (0: the engine's).
	Gain        float32 `yaml:"gain,omitempty"`
	Pitch       float32 `yaml:"pitch,omitempty"`
	RefDistance float32 `yaml:"ref_distance,omitempty"`
	MaxDistance float32 `yaml:"max_distance,omitempty"`
}

// VolumeEntry is one level volume as its YAML map (see the file comment).
// Its "id" key is the stage's own name for it, prefixed like a placement's.
type VolumeEntry map[string]interface{}

// LevelSettings are level-wide settings a stage owns in its target level —
// the environment stage's fog, sky, view distance and sun — by top-level
// level key, each value the YAML a level file would hold there. Like a
// VolumeEntry it is a loose map that genlevel decodes through the level's
// own schema (genlevel.SpliceSettings says which keys and how). A later
// stage's key replaces an earlier one's.
type LevelSettings map[string]interface{}

// placementsFile is a stage's placements on disk.
type placementsFile struct {
	Scenery []Placement   `yaml:"scenery,omitempty"`
	Volumes []VolumeEntry `yaml:"volumes,omitempty"`
	// Emitters are the stage's particle emitters (Emitter).
	Emitters []Emitter `yaml:"emitters,omitempty"`
	// Sounds are the stage's positional sounds (Sound).
	Sounds []Sound       `yaml:"sounds,omitempty"`
	Level  LevelSettings `yaml:"level,omitempty"`
	// Supersedes is the earlier stages' placements this stage replaces, by
	// stage ID (RunContext.Supersede).
	Supersedes map[string][]string `yaml:"supersedes,omitempty"`
}

// PlacementsFileName is the file a stage's placements are written to, inside
// its output folder.
const PlacementsFileName = "placements.yaml"

func writePlacements(path string, f placementsFile) error {
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return writeFileAtomicNoBackup(path, data)
}

func readPlacements(path string) (placementsFile, error) {
	var f placementsFile
	data, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return f, nil
}

// numberPlacements gives every placement and volume left without an ID one,
// and makes the ones it has unique within the stage, so a splice never has to
// rename anything it was given.
func numberPlacements(scenery []Placement, volumes []VolumeEntry) {
	seen := map[string]bool{}
	next := 0
	fresh := func(base string) string {
		if base != "" && !seen[base] {
			seen[base] = true
			return base
		}
		if base == "" {
			base = "p"
		}
		for {
			next++
			id := fmt.Sprintf("%s%d", base, next)
			if !seen[id] {
				seen[id] = true
				return id
			}
		}
	}
	for i := range scenery {
		scenery[i].ID = fresh(scenery[i].ID)
	}
	for i := range volumes {
		id, _ := volumes[i]["id"].(string)
		if id == "" {
			id = fresh("v")
		} else {
			id = fresh(id)
		}
		volumes[i]["id"] = id
	}
}

// writeFileAtomicNoBackup writes through a temporary file and a rename, like
// fileutil.WriteFileAtomic, without the .bak: a generator's outputs are
// rebuilt on demand, and a backup of each would double the output folder.
func writeFileAtomicNoBackup(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	return os.Rename(name, path)
}
