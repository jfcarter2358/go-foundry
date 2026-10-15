package genlevel

// regions_seed.go — a template's region layout written INTO its level
// (docs/regions/PHASE_6.md, "One command").
//
// The level is the source of truth for environment regions (regions.go):
// every run first copies the level's region volumes into the world doc's
// regions stage. A template that ships a layout — `canyon_savannah`'s two
// regions — would have that layout wiped by the first sync, because the
// level it targets has no region volumes yet (or does not exist). So the
// regions stage has a one-shot flag, `seed_level`: while it is set and the
// target level holds NO region volume, the stage's own list is written into
// the level as region volumes (each footprint stored relative to its
// volume's position, the box fitted, as the level editor makes them), and
// the flag is cleared. From then on the level leads as for any world doc.
//
// The flag clears whether or not anything was written: a level that already
// has regions keeps them (an author's layout always wins over a template's),
// and a level an author later empties of regions stays empty. The flag is an
// `Added` parameter, so clearing it returns the stage's hash to the one it
// would have had without it — a first run hashes exactly as a doc made with
// `gen world` and the same regions would.

import (
	"fmt"

	"foundry/internal/fileutil"
	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// ParamSeedLevel is the regions stage's one-shot "write my list into the
// level" flag.
const ParamSeedLevel = regions.ParamSeedLevel

// WantsSeed reports whether d's regions stage asks to seed its level.
func WantsSeed(d *procgen.Doc) bool {
	s := RegionsStage(d)
	if s == nil || s.Params == nil {
		return false
	}
	v, ok := s.Params[ParamSeedLevel]
	return ok && v.Bool
}

// ClearSeed drops the flag (the stage then reads its default, off).
func ClearSeed(d *procgen.Doc) {
	if s := RegionsStage(d); s != nil && s.Params != nil {
		delete(s.Params, ParamSeedLevel)
	}
}

// HasRegionVolumes reports whether raw holds any region volume.
func HasRegionVolumes(raw *world.RawLevel) bool {
	if raw == nil {
		return false
	}
	for i := range raw.Volumes {
		if raw.Volumes[i].Region != nil {
			return true
		}
	}
	return false
}

// SeedVolumes is d's regions stage list as region volumes, in list order:
// position = the footprint's bounds centre at height y, the footprint
// relative to it, the box fitted. IDs already in taken get a numeric suffix.
func SeedVolumes(d *procgen.Doc, y float32, taken map[string]bool) ([]world.Volume, error) {
	s := RegionsStage(d)
	if s == nil {
		return nil, nil
	}
	if taken == nil {
		taken = map[string]bool{}
	}
	var out []world.Volume
	for i, it := range s.Params.Items(regions.ParamRegions) {
		pts := it.Points(regions.FieldFootprint)
		if len(pts) < 3 {
			return nil, fmt.Errorf("region %d (%s): a footprint needs at least 3 points", i+1, it.Str(regions.FieldID))
		}
		id := it.Str(regions.FieldID)
		if id == "" {
			id = fmt.Sprintf("region_%d", i+1)
		}
		id = uniqueID(taken, id)
		taken[id] = true
		fp := make([]regions.Point, len(pts))
		for k, p := range pts {
			fp[k] = regions.Point{p[0], p[1]}
		}
		b := regions.PolygonBounds(fp)
		cx, cz := (b.MinX+b.MaxX)/2, (b.MinZ+b.MaxZ)/2
		v := world.NewVolume(id, mgl32.Vec3{float32(cx), y, float32(cz)})
		v.HalfY = world.DefaultRegionHalfY
		reg := &world.VolumeRegion{
			Environment: it.Str(regions.FieldEnvironment),
			Generator:   it.Str(regions.FieldGenerator),
			Priority:    it.Int(regions.FieldPriority),
			Transition:  float32(it.Float(regions.FieldTransition)),
		}
		if reg.Transition <= 0 {
			reg.Transition = world.DefaultRegionTransition
		}
		for _, p := range fp {
			reg.Footprint = append(reg.Footprint, [2]float32{float32(p[0] - cx), float32(p[1] - cz)})
		}
		if it.Bool(regions.FieldAnchored) {
			a := it.Vec2(regions.FieldAnchor)
			reg.Anchor = &[2]float32{float32(a[0] - cx), float32(a[1] - cz)}
		}
		v.Region = reg
		v.FitRegion()
		out = append(out, v)
	}
	return out, nil
}

// SeedLevelRegions writes d's region list into raw when d asks to seed and
// raw has no region volume, and clears the flag either way (while d asks).
// It returns the IDs written; nil with a cleared flag means the level
// already had its own regions.
func SeedLevelRegions(d *procgen.Doc, raw *world.RawLevel) ([]string, error) {
	if !WantsSeed(d) {
		return nil, nil
	}
	if HasRegionVolumes(raw) {
		ClearSeed(d)
		return nil, nil
	}
	taken := map[string]bool{}
	for i := range raw.Volumes {
		taken[raw.Volumes[i].ID] = true
	}
	vs, err := SeedVolumes(d, seedHeight(d), taken)
	if err != nil {
		return nil, err
	}
	var ids []string
	for i := range vs {
		raw.Volumes = append(raw.Volumes, world.VolumeToRaw(&vs[i]))
		ids = append(ids, vs[i].ID)
	}
	ClearSeed(d)
	return ids, nil
}

// seedHeight is the Y the seeded volumes stand at: a region is flat
// authoring data (its box only has to be pickable), so it sits at zero,
// where generated ground starts.
func seedHeight(*procgen.Doc) float32 { return 0 }

// SeedLevelFile is SeedLevelRegions on the level file at path, which it
// writes when regions were added — making the level (empty but for them)
// when it does not exist yet, as a template's first splice would.
func SeedLevelFile(d *procgen.Doc, path string) ([]string, error) {
	if !WantsSeed(d) {
		return nil, nil
	}
	raw, err := ReadRawLevel(path)
	if fileMissing(err) {
		raw, err = world.RawLevel{Chunks: []world.RawChunk{}}, nil
	}
	if err != nil {
		return nil, err
	}
	ids, err := SeedLevelRegions(d, &raw)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return nil, err
	}
	return ids, fileutil.WriteFileAtomic(path, data)
}
