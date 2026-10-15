package genlevel

// region_splice.go — splicing one environment region's placements
// (docs/regions/PHASE_4.md).
//
// A level generation doc's expanded stages carry their region in their
// IDs (gen.<doc>.r_<region>_<stage>.<id>), so a run of one region —
// `foundry gen run <doc> -region <id>`, which runs just that region's
// expanded stages and whatever shared stage they need that is stale — can
// splice just what it made: SpliceRaw with Options.Scope set removes and
// adds only entries under the scope's prefixes, and every other region's
// placements stay in the level exactly as they were.
//
// RegionScope builds the scope: each named region's expanded stages
// (procgen.RegionStages — whole stage IDs, not the bare
// gen.<doc>.r_<region>_ prefix, which would also take region "a_b"'s
// stages for region "a"), and every one of the doc's own stages (the
// shared ones: terrain tiles, the environment) that the run actually ran,
// so their new placements land too.

import (
	"foundry/internal/procgen"
)

// RegionScope is the splice scope of a run of the named regions (keys or
// level volume IDs) of d: StagePrefix of each region's expanded stages and
// of each of d's own stages in res.Ran.
func RegionScope(d *procgen.Doc, res *procgen.Result, regions []string) ([]string, error) {
	out := []string{} // never nil: a nil scope is everything

	for _, r := range regions {
		ids, err := procgen.RegionStages(d, r)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			out = append(out, procgen.StagePrefix(d.Name, id))
		}
	}
	if res != nil {
		for _, id := range res.Ran {
			if d.StageByID(id) != nil {
				out = append(out, procgen.StagePrefix(d.Name, id))
			}
		}
	}
	return out, nil
}

// inScope reports whether id is under one of scope's prefixes (every id
// is, with no scope).
func inScope(id string, scope []string) bool {
	if scope == nil {
		return true
	}
	for _, p := range scope {
		if len(id) >= len(p) && id[:len(p)] == p {
			return true
		}
	}
	return false
}
