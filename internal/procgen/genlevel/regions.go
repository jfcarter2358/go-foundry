package genlevel

// regions.go — the level's region volumes into its level generation doc
// (docs/regions/SPEC.md, "Sync, not live reads").
//
// The level is the source of truth for environment regions: an author shapes
// them as region volumes in the level editor. The level generation doc's
// `regions` stage holds a COPY in its params, because a stage's hash may
// depend only on its params — a run that read the level live could not tell
// whether it was stale, and the doc on disk would not record what it made.
// So every caller syncs before it runs (the editor's genBeginRun, `foundry
// gen run`) and before it previews with the level open: SyncRegions writes
// the level's regions into the stage, sorted by id, and says whether that
// changed anything.
//
// The footprint goes over in WORLD XZ (through the volume's position and
// rotation; a region ignores its attachment), rounded to the millimetre so a
// float32 round trip does not churn the doc. A doc with no regions stage is
// left alone: syncing is harmless to call for any document.

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"sort"

	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
	"foundry/internal/world"
)

// RegionsStage is the doc's first enabled-or-not regions stage, or nil.
func RegionsStage(d *procgen.Doc) *procgen.Stage {
	for i := range d.Stages {
		if d.Stages[i].Kind == regions.StageKind {
			return &d.Stages[i]
		}
	}
	return nil
}

// RegionItems is the level's region volumes as the regions stage's list
// items, sorted by id.
func RegionItems(raw *world.RawLevel) ([]procgen.Params, error) {
	type entry struct {
		id   string
		item procgen.Params
	}
	var es []entry
	for i := range raw.Volumes {
		rv := raw.Volumes[i]
		if rv.Region == nil {
			continue
		}
		v, err := world.VolumeFromRaw(rv)
		if err != nil {
			return nil, err
		}
		g := v.Region
		var pts [][]float64
		for _, p := range v.RegionWorldFootprint() {
			pts = append(pts, []float64{mm(p[0]), mm(p[1])})
		}
		anchor, anchored := v.RegionWorldAnchor()
		item := procgen.Params{
			regions.FieldID:          procgen.String(v.ID),
			regions.FieldEnvironment: procgen.String(g.Environment),
			regions.FieldGenerator:   procgen.String(g.Generator),
			regions.FieldPriority:    procgen.Int(g.Priority),
			regions.FieldTransition:  procgen.Float(mm(float64(g.Transition))),
			regions.FieldAnchored:    procgen.Bool(anchored),
			regions.FieldAnchor:      procgen.Vec2(mm(anchor[0]), mm(anchor[1])),
			regions.FieldFootprint:   procgen.Spline(pts...),
		}
		es = append(es, entry{v.ID, item})
	}
	sort.SliceStable(es, func(a, b int) bool { return es[a].id < es[b].id })
	out := make([]procgen.Params, len(es))
	for i, e := range es {
		out[i] = e.item
	}
	return out, nil
}

// mm rounds to the millimetre.
func mm(x float64) float64 { return math.Round(x*1000) / 1000 }

// SyncRegions copies raw's region volumes into d's regions stage. changed
// reports whether the stage's params are now different; a doc with no
// regions stage is untouched (false, nil).
func SyncRegions(d *procgen.Doc, raw *world.RawLevel) (changed bool, err error) {
	s := RegionsStage(d)
	if s == nil || raw == nil {
		return false, nil
	}
	items, err := RegionItems(raw)
	if err != nil {
		return false, fmt.Errorf("regions: %w", err)
	}
	k := procgen.KindByName(s.Kind)
	if k == nil {
		return false, fmt.Errorf("this build has no %s stage kind", s.Kind)
	}
	def := k.Param(regions.ParamRegions)
	next := def.Clamp(procgen.List(items...))
	if s.Params == nil {
		s.Params = procgen.Params{}
	}
	cur, ok := s.Params[regions.ParamRegions]
	if !ok {
		cur = def.Default
	}
	if def.Clamp(cur).Equal(next) {
		return false, nil
	}
	s.Params[regions.ParamRegions] = next
	return true, nil
}

// SyncRegionsFromFile is SyncRegions from the level file at path. A missing
// file syncs nothing (the level is not made yet).
func SyncRegionsFromFile(d *procgen.Doc, path string) (bool, error) {
	if RegionsStage(d) == nil {
		return false, nil
	}
	raw, err := ReadRawLevel(path)
	if err != nil {
		if fileMissing(err) {
			return false, nil
		}
		return false, err
	}
	return SyncRegions(d, &raw)
}

func fileMissing(err error) bool { return errors.Is(err, fs.ErrNotExist) }
