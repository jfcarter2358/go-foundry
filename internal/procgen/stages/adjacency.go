package stages

// adjacency.go — the project's adjacency table, and what the regions stage
// does with it (docs/regions/SPEC.md, "Adjacency rules"; the table and the
// file format are procgen/regions' adjacency.go and adjacency_file.go).
//
// ProjectAdjacency is the built-in table with the project's
// resources/environments/adjacency.yaml laid over it — read when it changes
// (keyed by the file's size and modification time), so the plan, the
// preview and every warning share one parse.
//
// The regions stage uses it twice:
//
//   - PAIR WIDTHS. Before it builds its Set, every region left at the
//     default transition takes the table's width for each neighbouring
//     environment (regions.ResolvePairTransitions). The resolved widths are
//     what the weights use, so they go into the stage's hash
//     (regionsPairHash, the kind's HashExtra): editing a width in
//     adjacency.yaml reruns the regions stage, and through it everything
//     downstream; editing a rule for environments the level does not use
//     changes nothing. They are written to regions.yaml, so a load needs no
//     table.
//   - WARNINGS. The kind's Warn hook lists every forbidden pair whose
//     weights overlap (regions.FindAdjacent on a coarse raster). `foundry
//     gen run` / `gen check` print them, the run logs them, and the run
//     proceeds: a warning, never an error.
//
// A broken adjacency.yaml is an error the regions stage's Validate reports
// (with the file's own message), so a run never silently ignores it.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/regions"
)

// AdjacencyPath is the project's override file.
func AdjacencyPath() string {
	return filepath.Join(procgen.EnvironmentsDir(), regions.AdjacencyFileName)
}

var adjacencyCache struct {
	sync.Mutex
	path  string
	size  int64
	mod   time.Time
	table *regions.Adjacency
	err   error
}

// ProjectAdjacency is the built-in table with the project's adjacency.yaml
// (if any) laid over it. The table is shared: do not change it.
func ProjectAdjacency() (*regions.Adjacency, error) {
	path := AdjacencyPath()
	st, statErr := os.Stat(path)
	c := &adjacencyCache
	c.Lock()
	defer c.Unlock()
	if statErr != nil {
		if c.path != path || c.table == nil || c.size != -1 {
			c.path, c.size, c.mod, c.table, c.err = path, -1, time.Time{}, regions.DefaultAdjacency(), nil
		}
		return c.table, c.err
	}
	if c.path == path && c.size == st.Size() && c.mod.Equal(st.ModTime()) && (c.table != nil || c.err != nil) {
		return c.table, c.err
	}
	c.path, c.size, c.mod = path, st.Size(), st.ModTime()
	data, err := os.ReadFile(path)
	if err == nil {
		var f *regions.AdjacencyFile
		if f, err = regions.ParseAdjacencyFile(data); err == nil {
			c.table, c.err = f.Apply(regions.DefaultAdjacency()), nil
			return c.table, nil
		}
	}
	c.table, c.err = nil, fmt.Errorf("%s: %w", procgen.EnvironmentsDirName+"/"+regions.AdjacencyFileName, err)
	return nil, c.err
}

// resolvedRegions is the regions stage's list with the project's pair
// widths resolved.
func resolvedRegions(p procgen.Params) ([]regions.Region, error) {
	rs := regionsFromParams(p)
	adj, err := ProjectAdjacency()
	if err != nil {
		return nil, err
	}
	regions.ResolvePairTransitions(rs, adj)
	return rs, nil
}

// regionsPairHash is the regions stage's HashExtra: every region's resolved
// pair widths, canonically ordered; "" when none, which the planner folds in
// as nothing — a world whose regions take no pair width keeps the hash it
// had before adjacency rules existed.
func regionsPairHash(d *procgen.Doc, s *procgen.Stage, p procgen.Params) (string, error) {
	rs, err := resolvedRegions(p)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, r := range rs {
		if len(r.PairTransitions) == 0 {
			continue
		}
		envs := make([]string, 0, len(r.PairTransitions))
		for e := range r.PairTransitions {
			envs = append(envs, e)
		}
		sort.Strings(envs)
		var kv []string
		for _, e := range envs {
			kv = append(kv, fmt.Sprintf("%s=%g", e, r.PairTransitions[e]))
		}
		parts = append(parts, regions.Key(r.ID)+":"+strings.Join(kv, ","))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";"), nil
}

// regionsWarnings is the regions stage's Warn hook: the forbidden pairs
// that touch.
func regionsWarnings(d *procgen.Doc, s *procgen.Stage, p procgen.Params) []string {
	set, err := regionSet(p, regionsSeed(d, s, p))
	if err != nil || set.Len() < 2 {
		return nil // Validate reports a broken list
	}
	adj, err := ProjectAdjacency()
	if err != nil {
		return nil
	}
	out, err := regions.Warnings(context.Background(), set, adj)
	if err != nil {
		return nil
	}
	return out
}

// RegionAdjacency is a regions stage's touching pairs (forbidden or not),
// with the set they index — the Generator's region list and the level
// editor's inspector ask it. ctx may be nil.
func RegionAdjacency(ctx context.Context, d *procgen.Doc, s *procgen.Stage) (*regions.Set, []regions.Adjacent, error) {
	p := procgen.Resolve(regionsParams(), s.Params)
	set, err := regionSet(p, regionsSeed(d, s, p))
	if err != nil {
		return nil, nil, err
	}
	adj, err := ProjectAdjacency()
	if err != nil {
		return set, nil, err
	}
	pairs, err := regions.FindAdjacent(ctx, set, adj, regions.AdjacentOptions{})
	return set, pairs, err
}

// RegionWarp is the boundary meander a level's world generator gives its
// regions: from its regions stage when d has one, else the stage's
// defaults with seed 0. The level editor's adjacency check uses it, so its
// warnings match a run's.
func RegionWarp(d *procgen.Doc) regions.Warp {
	if d != nil {
		for i := range d.Stages {
			s := &d.Stages[i]
			if s.Kind == RegionsKind {
				p := procgen.Resolve(regionsParams(), s.Params)
				return regionWarp(p, regionsSeed(d, s, p))
			}
		}
	}
	p := procgen.Resolve(regionsParams(), nil)
	return regionWarp(p, 0)
}
