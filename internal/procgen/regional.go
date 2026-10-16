package procgen

// regional.go — environment regions in the run engine: plan-time expansion.
//
// A level generation doc (resources/generators/<level>_world.fgen,
// docs/regions/SPEC.md) grows several environments at once. Its `regions`
// stage holds the level's region volumes; each region names an environment
// PRESET — an ordinary .fgen (environments.go) whose stages say how that
// environment is made. The shared stages of the level doc (the regions
// themselves, phase 2's region_terrain and region_material, the
// environment) run once over the union; every other stage of each region's
// preset runs once PER REGION.
//
// That second half is this file. When a document is planned, every stage
// whose kind has an Expand hook (the regions stage) is asked for its
// regions, each with its preset document and a RegionContext. Then, for each
// region in the order the hook gives (sorted by key), each enabled stage of
// its preset is looked at by its kind's Regional mode:
//
//   - RegionalShared: served by a shared stage of the level doc; not
//     expanded, and nothing is said.
//   - RegionalNone (the default): not expanded, with a log line — a kind
//     that cannot work per region yet (drift, which recooks terrain tiles).
//   - RegionalPerRegion: expanded, appended after every stage the document
//     lists, as `r_<region>_<stage>`, with
//   - its stage refs rewritten: a ref to a preset stage of a Shared kind
//     goes to the level doc's stage whose kind lists that kind in SharedFor
//     (the preset's terrain → region_terrain; with no such stage the preset
//     stage is skipped with a log line); a ref to another expanded
//     stage of the same region goes to its r_<region>_<ref>; a ref to a
//     stage that was not expanded skips this one too, with a log line;
//   - Env.Region set, so the stage can weight and clip its work;
//   - its seed derived from (doc seed, region key, preset stage ID) and
//     the kind's seed params (RegionStageSeed), so two regions growing the
//     same preset differ and editing one never reseeds another;
//   - its hash the ordinary StageHash over its rewritten params and its
//     refs' hashes, plus the defining stage's hash (so a region edit —
//     whose weights every region's share depends on — reruns them).
//
// A kind marked OnDemand (the asset stages: rocks, Poly Haven imports) is
// expanded only when a kept expanded stage of the same region reads it, so
// a region whose placements are skipped downloads and cooks nothing.
//
// A document with no stage of an Expand kind plans exactly as it did before
// this file existed: same stages, same seeds, same hashes (a test pins a
// real document's hashes).

import (
	"fmt"
	"strings"

	"foundry/internal/procgen/noise"
)

// RegionalMode is how a kind takes part in environment regions.
type RegionalMode int

const (
	// RegionalNone: a preset stage of this kind is skipped, with a log line.
	RegionalNone RegionalMode = iota
	// RegionalPerRegion: expanded once per region.
	RegionalPerRegion
	// RegionalShared: one stage of the level doc serves every region.
	RegionalShared
)

// RegionalModeNames are the modes' spellings, for listings.
var RegionalModeNames = []string{"none", "per-region", "shared"}

// RegionContext is the region an expanded stage works for.
type RegionContext struct {
	// Key is the region's stage-ID-safe key (r_<Key>_<stage>); ID the level
	// volume's own ID.
	Key, ID string
	// Environment is the preset's environment name; Preset where the preset
	// document came from (a path, or "built-in <name>"), for logs.
	Environment, Preset string
	// Weight is the region's share of world point (x, z), 0..1, the regions
	// package's composited, normalised weight. Safe for concurrent use.
	Weight func(x, z float64) float64
	// Bounds is the footprint's bounding rectangle; Band is it grown by half
	// the transition band and the boundary warp's reach — everything the
	// region's own blend reaches. A placement stage clips its work to Band
	// (beyond it the region owns ground only as the nearest region to a gap
	// nobody claims).
	Bounds, Band Rect
	// Anchor is where the preset's own coordinates (canyon paths, focus
	// points) are measured from, world XZ.
	Anchor [2]float64
}

// RegionExpansion is one region an Expand hook reports: where it is, and the
// preset document whose stages it grows.
type RegionExpansion struct {
	Region RegionContext
	Preset *Doc
}

// RegionStageID is the ID an expanded stage takes: r_<region>_<stage>.
func RegionStageID(regionKey, stageID string) string { return "r_" + regionKey + "_" + stageID }

// RegionStagePrefix is the level ID prefix every placement of one region's
// expanded stages carries: gen.<doc>.r_<region>_ — what a region-scoped
// splice removes (phase 4).
func RegionStagePrefix(doc, regionKey string) string { return IDPrefix(doc) + "r_" + regionKey + "_" }

// RegionStageSeed is an expanded stage's seed: the level doc's seed, the
// region's key and the PRESET stage's ID, then the kind's seed params —
// StageSeed's recipe with the region folded in.
func RegionStageSeed(docSeed int64, regionKey, presetStageID string, defs []ParamDef, p Params) uint64 {
	words := []uint64{uint64(docSeed), noise.HashString("region\x00" + regionKey), noise.HashString(presetStageID)}
	for i := range defs {
		if defs[i].Type == ParamSeed {
			words = append(words, uint64(int64(p[defs[i].Name].Num)))
		}
	}
	return noise.Hash64(words...)
}

// RewriteStageRefs is p with every stage ref (top level and inside list
// items) passed through fn; a ref fn maps to "" is left as it was. p is not
// modified.
func RewriteStageRefs(defs []ParamDef, p Params, fn func(ref string) string) Params {
	out := p.Clone()
	var walk func(defs []ParamDef, p Params)
	walk = func(defs []ParamDef, p Params) {
		for i := range defs {
			d := &defs[i]
			v, ok := p[d.Name]
			if !ok {
				continue
			}
			switch d.Type {
			case ParamStage:
				if v.Str != "" {
					if to := fn(v.Str); to != "" {
						v.Str = to
						p[d.Name] = v
					}
				}
			case ParamList:
				for _, it := range v.Items {
					walk(d.Fields, it)
				}
			}
		}
	}
	walk(defs, out)
	return out
}

// expandRegions adds every Expand stage's regions' expanded stages to a plan
// (see the file comment). byID holds the planned stages by ID and gains the
// new ones. notes are the log lines for skipped preset stages.
func expandRegions(d *Doc, planned []*plannedStage, byID map[string]*plannedStage) ([]*plannedStage, []string, error) {
	var notes []string
	var extra []*plannedStage
	for _, def := range planned {
		if def.kind.Expand == nil {
			continue
		}
		exps, err := def.kind.Expand(d, def.stage, def.params)
		if err != nil {
			return nil, nil, fmt.Errorf("stage %s: %w", def.stage.ID, err)
		}
		for ei := range exps {
			ex := exps[ei]
			add, n, err := expandRegion(d, def, &ex, byID)
			notes = append(notes, n...)
			if err != nil {
				return nil, nil, err
			}
			extra = append(extra, add...)
		}
	}
	return extra, notes, nil
}

// expandRegion expands one region's preset.
func expandRegion(d *Doc, def *plannedStage, ex *RegionExpansion, byID map[string]*plannedStage) ([]*plannedStage, []string, error) {
	reg := &ex.Region
	preset := ex.Preset
	if preset == nil {
		return nil, nil, fmt.Errorf("stage %s: region %s has no preset", def.stage.ID, reg.Key)
	}
	if !ValidStageID("r_" + reg.Key) {
		return nil, nil, fmt.Errorf("stage %s: region key %q cannot be part of a stage ID", def.stage.ID, reg.Key)
	}
	where := fmt.Sprintf("[%s] region %s (%s)", def.stage.ID, reg.Key, reg.Environment)
	var notes []string
	// Which preset stages expand.
	expandable := map[string]bool{}
	for i := range preset.Stages {
		s := &preset.Stages[i]
		if s.Disabled {
			continue
		}
		k := KindByName(s.Kind)
		switch {
		case k == nil:
			notes = append(notes, fmt.Sprintf("%s: preset stage %s: this build has no stage kind %q; skipped", where, s.ID, s.Kind))
		case k.Regional == RegionalPerRegion:
			expandable[s.ID] = true
		case k.Regional == RegionalNone:
			notes = append(notes, fmt.Sprintf("%s: preset stage %s (%s) cannot run per region yet; skipped", where, s.ID, s.Kind))
		}
	}
	var out []*plannedStage
	done := map[string]string{} // preset stage ID -> expanded ID, for those expanded so far
	for i := range preset.Stages {
		s := &preset.Stages[i]
		if !expandable[s.ID] {
			continue
		}
		k := KindByName(s.Kind)
		in := Resolve(k.Params, s.Params)
		var missing string
		var failure error
		mapped := RewriteStageRefs(k.Params, in, func(ref string) string {
			if missing != "" || failure != nil {
				return ""
			}
			if to, ok := done[ref]; ok {
				return to
			}
			t := preset.StageByID(ref)
			var tk *StageKind
			if t != nil {
				tk = KindByName(t.Kind)
			}
			if t == nil || t.Disabled || tk == nil {
				missing = ref
				return ""
			}
			if tk.Regional == RegionalShared {
				if sub := sharedStageFor(d, byID, tk.Name); sub != "" {
					return sub
				}
				failure = fmt.Errorf("%s: preset stage %s refers to %s (%s), and the level doc has no enabled stage standing in for %s",
					strings.TrimPrefix(where, "["+def.stage.ID+"] "), s.ID, ref, tk.Name, tk.Name)
				return ""
			}
			missing = ref
			return ""
		})
		if failure != nil {
			// Skipped, not refused: a level doc without (say) a region
			// material still plans, its regions' stages that need one wait
			// for it (docs/regions/PHASE_4.md).
			notes = append(notes, fmt.Sprintf("[%s] %v; skipped", def.stage.ID, failure))
			continue
		}
		if missing != "" {
			notes = append(notes, fmt.Sprintf("%s: preset stage %s needs %s, which is not expanded; skipped", where, s.ID, missing))
			continue
		}
		id := RegionStageID(reg.Key, s.ID)
		if !ValidStageID(id) {
			return nil, notes, fmt.Errorf("stage %s: region %s: %q is not a valid stage ID", def.stage.ID, reg.Key, id)
		}
		if byID[id] != nil || d.StageByID(id) != nil {
			return nil, notes, fmt.Errorf("stage %s: region %s: the expanded stage ID %s is already taken", def.stage.ID, reg.Key, id)
		}
		st := &Stage{Kind: k.Name, ID: id, Params: mapped}
		p := Resolve(k.Params, mapped)
		if k.Validate != nil {
			if probs := k.Validate(d, st, p); len(probs) > 0 {
				return nil, notes, fmt.Errorf("stage %s (region %s, preset stage %s): %s", id, reg.Key, s.ID, strings.Join(probs, "; "))
			}
		}
		ps := &plannedStage{stage: st, kind: k, params: p, refs: StageRefs(k.Params, p), region: reg}
		ps.seed = RegionStageSeed(d.Seed, reg.Key, s.ID, k.Params, p)
		var up, bup []string
		for _, r := range ps.refs {
			u := byID[r]
			if u == nil {
				return nil, notes, fmt.Errorf("stage %s: refers to %s, which is not planned", id, r)
			}
			up = append(up, u.hash)
			bup = append(bup, u.base)
		}
		// The defining stage last: the region list (every region's weight
		// depends on all of them) and the preset's location.
		if !containsStr(ps.refs, def.stage.ID) {
			ps.refs = append(ps.refs, def.stage.ID)
		}
		up = append(up, "region:"+def.hash)
		bup = append(bup, "region:"+def.base)
		planPaint(ps, k, p, bup, byID)
		// The LEVEL doc's paint layers it reads (paint_doc.go).
		up = foldPaint(d, k.Params, p, up)
		ps.hash = StageHash(k, ps.seed, p, up)
		byID[id] = ps
		done[s.ID] = id
		out = append(out, ps)
	}
	return pruneOnDemand(out, byID), notes, nil
}

// pruneOnDemand drops the OnDemand stages of one region's expansion that no
// other kept stage of it refers to, directly or through others (byID
// forgets them too).
func pruneOnDemand(out []*plannedStage, byID map[string]*plannedStage) []*plannedStage {
	need := map[string]bool{}
	var mark func(p *plannedStage)
	mark = func(p *plannedStage) {
		for _, r := range p.refs {
			if u := byID[r]; u != nil && u.region != nil && !need[r] {
				need[r] = true
				mark(u)
			}
		}
	}
	for _, p := range out {
		if !p.kind.OnDemand {
			mark(p)
		}
	}
	kept := out[:0]
	for _, p := range out {
		if p.kind.OnDemand && !need[p.stage.ID] {
			delete(byID, p.stage.ID)
			continue
		}
		kept = append(kept, p)
	}
	return kept
}

// sharedStageFor is the ID of the first enabled stage of d (planned, so above
// any expanded one) whose kind stands in for preset kind name, or "".
func sharedStageFor(d *Doc, byID map[string]*plannedStage, name string) string {
	for i := range d.Stages {
		s := &d.Stages[i]
		p := byID[s.ID]
		if p == nil || p.region != nil {
			continue
		}
		for _, k := range p.kind.SharedFor {
			if k == name {
				return s.ID
			}
		}
	}
	return ""
}

// RegionStages is the IDs of the stages d's plan expands for one region,
// named by its key or its level volume's ID, in plan order — what a run of
// just that region (Options.Only) and a splice of just its placements
// (genlevel.RegionScope) take. An error when d does not plan or has no
// such region; a region whose preset expands nothing has none.
func RegionStages(d *Doc, region string) ([]string, error) {
	ps, err := plan(d)
	if err != nil {
		return nil, err
	}
	var out []string
	found := false
	for _, p := range ps {
		if p.region == nil || (p.region.Key != region && p.region.ID != region) {
			continue
		}
		found = true
		out = append(out, p.stage.ID)
	}
	if !found {
		// A region with nothing to expand is still a region: look for it.
		for _, p := range ps {
			if p.kind.Expand == nil {
				continue
			}
			exps, err := p.kind.Expand(d, p.stage, p.params)
			if err != nil {
				return nil, err
			}
			for _, ex := range exps {
				if ex.Region.Key == region || ex.Region.ID == region {
					return nil, nil
				}
			}
		}
		return nil, fmt.Errorf("no region %q in %s", region, d.Name)
	}
	return out, nil
}
