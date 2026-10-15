// Package genlevel puts a generator's placements into a level: the splice by
// ID prefix the procedural generators' design rests on.
//
// Every entry a generator places carries the ID prefix gen.<generator>.
// (procgen.IDPrefix), followed by its stage's ID and its own. A splice
// removes every scenery and volume entry with the generator's prefix, then
// appends the run's — so a rerun replaces exactly what the last run placed,
// a stage removed from the document takes its placements with it, and
// everything hand-made (anything without the prefix) is left alone and in
// its order. Naming a hand-made entry with a gen. prefix is the one way to
// lose it; the guide says so.
//
// Two routes in, for the two places a level lives:
//
//   - SpliceRaw edits a world.RawLevel — the editor's in-memory level, taken
//     with editorLevelToRaw and put back through the editor's undoable
//     snapshot restore (editor_generator_splice.go);
//   - SpliceFile edits a level file on disk, for `foundry gen run` and for
//     a target level that is not open.
//
// It sits beside internal/procgen, not in it, because it needs
// internal/world's level schema and the core must stay below world.
package genlevel

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/world"

	"gopkg.in/yaml.v2"
)

// Options tune a splice.
type Options struct {
	// ChunkAt resolves a placement whose Chunk is procgen.ChunkAuto: the
	// chunk to stream with at a position, or "". Nil resolves every one to
	// "" (always in) — what a file splice does, having no built level to
	// measure chunks against.
	ChunkAt func(x, y, z float32) string
	// Settings are the level-wide settings the run owns (settings.go):
	// SpliceResult takes them from the result when this is nil.
	Settings procgen.LevelSettings
	// Scope, when not nil, limits the splice to entries whose IDs start
	// with one of these prefixes (each under the generator's): only those
	// are removed from the level, and only the run's entries under them
	// are added. RegionScope makes one for a run of some regions
	// (region_splice.go). Nil: every entry of the generator.
	Scope []string
}

// Stats is what a splice changed.
type Stats struct {
	RemovedScenery, AddedScenery int
	RemovedVolumes, AddedVolumes int
	// Settings are the level-wide settings written (settings.go), space
	// separated: a string, so Stats stays comparable.
	Settings string
}

func (s Stats) String() string {
	out := fmt.Sprintf("scenery -%d +%d, volumes -%d +%d",
		s.RemovedScenery, s.AddedScenery, s.RemovedVolumes, s.AddedVolumes)
	if len(s.Settings) > 0 {
		out += ", settings " + s.Settings
	}
	return out
}

// Changed reports whether the splice did anything. (SpliceFile asks the
// file instead: settings rewritten to the values they already held are no
// change.)
func (s Stats) Changed() bool {
	return s.RemovedScenery+s.AddedScenery+s.RemovedVolumes+s.AddedVolumes+len(s.Settings) > 0
}

// ToRawScenery converts a placement, ID unchanged.
func ToRawScenery(p procgen.Placement, opt Options) world.RawScenery {
	r := world.RawScenery{
		ID:        p.ID,
		Model:     p.Model,
		Position:  world.RawVec3{X: p.Position.X, Y: p.Position.Y, Z: p.Position.Z},
		Rotation:  world.RawVec3{X: p.Rotation.X, Y: p.Rotation.Y, Z: p.Rotation.Z},
		Solid:     p.Solid,
		Collision: p.Collision,
		Chunk:     p.Chunk,
		Material:  p.Material,

		DrawDistance:     p.DrawDistance,
		ShadowDistance:   p.ShadowDistance,
		Impostor:         p.Impostor,
		ImpostorDistance: p.ImpostorDistance,
	}
	if p.Scale != nil {
		r.Scale = &world.RawVec3{X: p.Scale.X, Y: p.Scale.Y, Z: p.Scale.Z}
	}
	if len(p.Materials) > 0 {
		r.Materials = make(map[string]string, len(p.Materials))
		for k, v := range p.Materials {
			r.Materials[k] = v
		}
	}
	if p.Wind != nil {
		r.Wind = &world.RawSceneryWind{Bend: p.Wind.Bend, Flutter: p.Wind.Flutter}
	}
	if r.Chunk == procgen.ChunkAuto {
		r.Chunk = ""
		if opt.ChunkAt != nil {
			r.Chunk = opt.ChunkAt(p.Position.X, p.Position.Y, p.Position.Z)
		}
	}
	return r
}

// ToRawVolume converts a volume entry through the level's own YAML schema,
// strictly: a key a level volume does not have is an error here rather than
// a volume that silently lacks it.
func ToRawVolume(v procgen.VolumeEntry) (world.RawVolume, error) {
	data, err := yaml.Marshal(map[string]interface{}(v))
	if err != nil {
		return world.RawVolume{}, err
	}
	var r world.RawVolume
	if err := yaml.UnmarshalStrict(data, &r); err != nil {
		id, _ := v["id"].(string)
		return world.RawVolume{}, fmt.Errorf("volume %s: %w", id, err)
	}
	return r, nil
}

// SpliceRaw replaces the generator's entries in raw with scenery and
// volumes, whose IDs must already carry the prefix (procgen.Result's
// Placements and Volumes do).
func SpliceRaw(raw *world.RawLevel, generator string, scenery []procgen.Placement, volumes []procgen.VolumeEntry, opt Options) (Stats, error) {
	prefix := procgen.IDPrefix(generator)
	var st Stats
	// Settings first: every key is decoded before any is written, so a bad
	// one fails the splice before anything moves.
	if len(opt.Settings) > 0 {
		// A shallow copy, with its own directional lights: the sun is set
		// in place.
		probe := *raw
		probe.Lights.Directional = append([]world.RawDirLight(nil), raw.Lights.Directional...)
		if _, err := SpliceSettings(&probe, opt.Settings); err != nil {
			return st, err
		}
	}
	// Convert first, so a bad volume fails the splice before anything moves.
	newVols := make([]world.RawVolume, 0, len(volumes))
	for _, v := range volumes {
		r, err := ToRawVolume(v)
		if err != nil {
			return st, err
		}
		if !strings.HasPrefix(r.ID, prefix) {
			return st, fmt.Errorf("volume %q does not carry the generator's prefix %q", r.ID, prefix)
		}
		if !inScope(r.ID, opt.Scope) {
			continue
		}
		newVols = append(newVols, r)
	}
	for _, p := range scenery {
		if !strings.HasPrefix(p.ID, prefix) {
			return st, fmt.Errorf("placement %q does not carry the generator's prefix %q", p.ID, prefix)
		}
	}

	keptSc := raw.Scenery[:0:0]
	for _, s := range raw.Scenery {
		if strings.HasPrefix(s.ID, prefix) && inScope(s.ID, opt.Scope) {
			st.RemovedScenery++
			continue
		}
		keptSc = append(keptSc, s)
	}
	for _, p := range scenery {
		if !inScope(p.ID, opt.Scope) {
			continue
		}
		keptSc = append(keptSc, ToRawScenery(p, opt))
		st.AddedScenery++
	}
	if len(keptSc) == 0 {
		keptSc = nil
	}
	raw.Scenery = keptSc

	keptV := raw.Volumes[:0:0]
	for _, v := range raw.Volumes {
		if strings.HasPrefix(v.ID, prefix) && inScope(v.ID, opt.Scope) {
			st.RemovedVolumes++
			continue
		}
		keptV = append(keptV, v)
	}
	keptV = append(keptV, newVols...)
	st.AddedVolumes = len(newVols)
	if len(keptV) == 0 {
		keptV = nil
	}
	raw.Volumes = keptV
	set, err := SpliceSettings(raw, opt.Settings)
	if err != nil {
		return st, err
	}
	st.Settings = strings.Join(set, " ")
	return st, nil
}

// SpliceResult is SpliceRaw with a run's placements and level settings.
func SpliceResult(raw *world.RawLevel, res *procgen.Result, opt Options) (Stats, error) {
	if opt.Settings == nil {
		opt.Settings = res.LevelSettings()
	}
	return SpliceRaw(raw, res.Generator, res.Placements(), res.Volumes(), opt)
}

// Count is how many of raw's scenery and volume entries the generator owns.
func Count(raw *world.RawLevel, generator string) (scenery, volumes int) {
	prefix := procgen.IDPrefix(generator)
	for _, s := range raw.Scenery {
		if strings.HasPrefix(s.ID, prefix) {
			scenery++
		}
	}
	for _, v := range raw.Volumes {
		if strings.HasPrefix(v.ID, prefix) {
			volumes++
		}
	}
	return
}

// ReadRawLevel reads a level file as the loader does (lenient YAML).
func ReadRawLevel(path string) (world.RawLevel, error) {
	var raw world.RawLevel
	data, err := os.ReadFile(path)
	if err != nil {
		return raw, err
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return raw, fmt.Errorf("%s: %w", path, err)
	}
	return raw, nil
}

// SpliceFile splices into the level file at path and writes it back
// (atomically, keeping a .bak) — but only when something changed, so a
// rerun that placed the same entries does not rewrite the file.
//
// The level goes through world.RawLevel and back, as an editor save does:
// comments and keys the schema does not know are not kept.
func SpliceFile(path, generator string, scenery []procgen.Placement, volumes []procgen.VolumeEntry, opt Options) (Stats, error) {
	raw, err := ReadRawLevel(path)
	if errors.Is(err, fs.ErrNotExist) {
		// A generator that names a level not yet made (a template's first
		// run) makes it: an empty level the splice then fills.
		raw, err = world.RawLevel{Chunks: []world.RawChunk{}}, nil
	}
	if err != nil {
		return Stats{}, err
	}
	before, err := yaml.Marshal(raw)
	if err != nil {
		return Stats{}, err
	}
	st, err := SpliceRaw(&raw, generator, scenery, volumes, opt)
	if err != nil {
		return st, err
	}
	after, err := yaml.Marshal(raw)
	if err != nil {
		return st, err
	}
	if string(before) == string(after) {
		return Stats{}, nil
	}
	return st, fileutil.WriteFileAtomic(path, after)
}
