package genlevel

// adjacency.go — the adjacency rules on a LEVEL (docs/regions/SPEC.md,
// phase 5): which of its region volumes touch where the table forbids it,
// and "Insert intermediate region", which adds a region volume that
// separates such a pair.
//
// Both work on the level's region volumes directly (the level is the source
// of truth), not on a level generation doc: the level editor's region
// inspector checks the open level as the author moves footprints, and the
// insertion is an edit of the level. They take the adjacency table and the
// boundary warp as arguments, because the project's table and the world
// generator's warp are procgen/stages' to load (stages.ProjectAdjacency,
// stages.RegionWarp), and this package does not import the stages.
//
// InsertIntermediate adds one region volume per separate contact
// (regions.PlanIntermediate: usually one): environment = the table's
// suggestion, priority one above both of the pair's, transition the default
// (so each pair takes the table's width — regions/pairband.go), footprint
// stored relative to the new volume's position (the centre of its bounds;
// height the mean of the two regions' volumes'), the box fitted round it.
// Its ID is <a>_<b>_<env>, with a numeric suffix when taken. The caller
// pushes the undo step (the editor) or writes the file (a tool).

import (
	"context"
	"fmt"
	"math"

	"foundry/internal/procgen/regions"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// LevelRegions is the level's region volumes as procgen/regions reads them —
// world-XZ footprints, the same numbers SyncRegions writes into a world
// doc — with the table's pair widths resolved (adj may be nil: none).
func LevelRegions(raw *world.RawLevel, adj *regions.Adjacency) ([]regions.Region, error) {
	items, err := RegionItems(raw)
	if err != nil {
		return nil, err
	}
	out := make([]regions.Region, 0, len(items))
	for _, it := range items {
		r := regions.Region{
			ID:          it.Str(regions.FieldID),
			Environment: it.Str(regions.FieldEnvironment),
			Generator:   it.Str(regions.FieldGenerator),
			Priority:    it.Int(regions.FieldPriority),
			Transition:  it.Float(regions.FieldTransition),
			HasAnchor:   it.Bool(regions.FieldAnchored),
			Anchor:      it.Vec2(regions.FieldAnchor),
		}
		r.Key = regions.Key(r.ID)
		for _, p := range it.Points(regions.FieldFootprint) {
			r.Footprint = append(r.Footprint, regions.Point{p[0], p[1]})
		}
		out = append(out, r)
	}
	regions.ResolvePairTransitions(out, adj)
	return out, nil
}

// LevelAdjacency is the level's touching region pairs (forbidden or not) and
// the set they index. A level whose regions do not form a valid set (a
// footprint with crossing edges) is an error. ctx may be nil.
func LevelAdjacency(ctx context.Context, raw *world.RawLevel, adj *regions.Adjacency, warp regions.Warp) (*regions.Set, []regions.Adjacent, error) {
	rs, err := LevelRegions(raw, adj)
	if err != nil {
		return nil, nil, err
	}
	set, err := regions.NewSet(rs, warp)
	if err != nil {
		return nil, nil, err
	}
	pairs, err := regions.FindAdjacent(ctx, set, adj, regions.AdjacentOptions{})
	return set, pairs, err
}

// InsertIntermediate adds to raw the region volume(s) that separate the
// forbidden pair of region volumes a and b (by volume ID), and returns their
// IDs (file comment).
func InsertIntermediate(raw *world.RawLevel, adj *regions.Adjacency, a, b string) ([]string, error) {
	rs, err := LevelRegions(raw, adj)
	if err != nil {
		return nil, err
	}
	// The plan is warp-independent (it works in the warped domain), so no
	// warp here.
	set, err := regions.NewSet(rs, regions.Warp{})
	if err != nil {
		return nil, err
	}
	ia, ib := set.Index(regions.Key(a)), set.Index(regions.Key(b))
	if ia < 0 || ib < 0 {
		return nil, fmt.Errorf("intermediate region: no region volume %q or %q", a, b)
	}
	plan, err := regions.PlanIntermediate(set, adj, ia, ib, regions.IntermediateOptions{})
	if err != nil {
		return nil, err
	}
	y := (volumeY(raw, a) + volumeY(raw, b)) / 2
	taken := map[string]bool{}
	for i := range raw.Volumes {
		taken[raw.Volumes[i].ID] = true
	}
	var ids []string
	for _, fp := range plan.Footprints {
		id := uniqueID(taken, regions.Key(a)+"_"+regions.Key(b)+"_"+plan.Environment)
		taken[id] = true
		b := regions.PolygonBounds(fp)
		cx, cz := (b.MinX+b.MaxX)/2, (b.MinZ+b.MaxZ)/2
		v := world.NewVolume(id, mgl32.Vec3{float32(cx), y, float32(cz)})
		v.HalfY = world.DefaultRegionHalfY
		reg := &world.VolumeRegion{Environment: plan.Environment, Priority: plan.Priority,
			Transition: world.DefaultRegionTransition}
		for _, p := range fp {
			reg.Footprint = append(reg.Footprint, [2]float32{float32(p[0] - cx), float32(p[1] - cz)})
		}
		v.Region = reg
		v.FitRegion()
		raw.Volumes = append(raw.Volumes, world.VolumeToRaw(&v))
		ids = append(ids, id)
	}
	return ids, nil
}

// volumeY is the height of the volume with the given ID (0 if none).
func volumeY(raw *world.RawLevel, id string) float32 {
	for i := range raw.Volumes {
		if raw.Volumes[i].ID == id {
			if v, err := world.VolumeFromRaw(raw.Volumes[i]); err == nil {
				return v.Position[1]
			}
		}
	}
	return 0
}

// uniqueID is base, or base_2, base_3... whichever is free.
func uniqueID(taken map[string]bool, base string) string {
	if !taken[base] {
		return base
	}
	for n := 2; n < math.MaxInt32; n++ {
		if id := fmt.Sprintf("%s_%d", base, n); !taken[id] {
			return id
		}
	}
	return base
}
