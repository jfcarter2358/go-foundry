package regions

// adjacent.go — which regions touch, and which touches the adjacency table
// forbids (docs/regions/SPEC.md, "Adjacency rules").
//
// Two regions are ADJACENT when their weights overlap: somewhere both own
// more than AdjacentThreshold (1%) of the ground. That is the honest test —
// two footprints a little apart still blend across the gap (the nearest-
// region fill), and two that overlap under a higher-priority third may never
// meet at all — and it is cheap on a coarse raster: FindAdjacent samples
// Set.Weights (the very function the terrain blends by) on a grid of at
// most MaxSide samples a side over the set's bounds, in parallel rows, and
// records for each pair the sample where the smaller of the two shares is
// largest (Where: the middle of their seam, near enough, for a warning to
// point at).
//
// Each adjacent pair is then looked up in the table: a forbidden one carries
// its suggested intermediate environment, and becomes a WARNING — never an
// error: `foundry gen run` and `gen check` print it, the regions stage's
// Warn hook reports it, the Generator lists it and the level editor's region
// inspector shows it, each with "Insert intermediate region"
// (intermediate.go) where the table names one. The run proceeds: a desert
// against a tundra is an authoring mistake to hear about, not a broken
// world.
//
// A coarse raster can miss a contact thinner than a sample (two regions
// whose bands brush for a few metres at a corner). The default spacing
// (at most 8 m, at most 256 samples a side) is far finer than any band, so
// a real seam is always seen.

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// AdjacentThreshold is the share both regions must exceed at a sample for
// them to count as touching.
const AdjacentThreshold = 0.01

// Adjacent is one pair of regions whose weights overlap.
type Adjacent struct {
	// A < B index Set.Regions; the IDs and environments are copied out so a
	// warning outlives the set.
	A, B       int
	IDA, IDB   string
	EnvA, EnvB string
	// Rule is the table's rule for the pair (HasRule false: none).
	Rule    Rule
	HasRule bool
	// Where is a world point in the overlap (where the smaller share is
	// largest); Overlap that smaller share, 0..0.5.
	Where   Point
	Overlap float64
}

// Forbidden reports whether the table forbids this pair.
func (a Adjacent) Forbidden() bool { return a.HasRule && a.Rule.Forbidden }

// Suggestion is the environment to insert between a forbidden pair, or "".
func (a Adjacent) Suggestion() string {
	if !a.Forbidden() {
		return ""
	}
	return a.Rule.Intermediate
}

// Warning is the forbidden pair as one line for a log or an inspector.
func (a Adjacent) Warning() string {
	why := a.Rule.Note
	if why == "" {
		why = "the adjacency table forbids " + a.EnvA + " next to " + a.EnvB
	}
	s := fmt.Sprintf("regions %s (%s) and %s (%s) touch near (%.0f, %.0f): %s", a.IDA, a.EnvA, a.IDB, a.EnvB,
		a.Where[0], a.Where[1], why)
	if c := a.Suggestion(); c != "" {
		s += "; Insert intermediate region adds " + c + " between them"
	}
	return s
}

// AdjacentOptions tune the raster. Zero values are the defaults.
type AdjacentOptions struct {
	// Cell is the largest sample spacing, metres (default 8); MaxSide the
	// most samples along the longer side (default 256).
	Cell    float64
	MaxSide int
	Workers int
}

func (o AdjacentOptions) grid(r Rect) Grid {
	cell, side := o.Cell, o.MaxSide
	if cell <= 0 {
		cell = 8
	}
	if side <= 0 {
		side = 256
	}
	return GridOver(r, cell, side)
}

// FindAdjacent is every pair of regions whose weights overlap, sorted by
// (A, B), each looked up in adj (nil: no rules). ctx may be nil.
func FindAdjacent(ctx context.Context, s *Set, adj *Adjacency, opt AdjacentOptions) ([]Adjacent, error) {
	if s == nil || s.Len() < 2 {
		return nil, nil
	}
	g := opt.grid(s.Bounds())
	workers := opt.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	type best struct {
		ov    float64
		where Point
	}
	var mu sync.Mutex
	found := map[[2]int]best{}
	var next atomic.Int64
	var wg sync.WaitGroup
	var cancelled atomic.Bool
	for w := 0; w < min(workers, g.H); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := map[[2]int]best{}
			var buf []W
			for {
				j := int(next.Add(1) - 1)
				if j >= g.H {
					break
				}
				if ctx != nil && ctx.Err() != nil {
					cancelled.Store(true)
					break
				}
				for i := 0; i < g.W; i++ {
					x, z := g.At(i, j)
					buf = s.Weights(x, z, buf)
					for p := 0; p < len(buf); p++ {
						if buf[p].Weight <= AdjacentThreshold {
							continue
						}
						for q := p + 1; q < len(buf); q++ {
							if buf[q].Weight <= AdjacentThreshold {
								continue
							}
							k := [2]int{buf[p].Region, buf[q].Region}
							ov := min(buf[p].Weight, buf[q].Weight)
							if b, ok := local[k]; !ok || ov > b.ov {
								local[k] = best{ov, Point{x, z}}
							}
						}
					}
				}
			}
			mu.Lock()
			for k, b := range local {
				// The larger overlap wins; ties go to the lower point, so the
				// answer does not depend on which goroutine saw it.
				if c, ok := found[k]; !ok || b.ov > c.ov || (b.ov == c.ov && lessPoint(b.where, c.where)) {
					found[k] = b
				}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if cancelled.Load() && ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	out := make([]Adjacent, 0, len(found))
	for k, b := range found {
		ra, rb := &s.Regions[k[0]], &s.Regions[k[1]]
		a := Adjacent{A: k[0], B: k[1], IDA: ra.ID, IDB: rb.ID, EnvA: ra.Environment, EnvB: rb.Environment,
			Where: b.where, Overlap: b.ov}
		a.Rule, a.HasRule = adj.Lookup(ra.Environment, rb.Environment)
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out, nil
}

func lessPoint(a, b Point) bool {
	if a[1] != b[1] {
		return a[1] < b[1]
	}
	return a[0] < b[0]
}

// ForbiddenPairs is the forbidden ones of a FindAdjacent answer.
func ForbiddenPairs(adj []Adjacent) []Adjacent {
	var out []Adjacent
	for _, a := range adj {
		if a.Forbidden() {
			out = append(out, a)
		}
	}
	return out
}

// Warnings is FindAdjacent's forbidden pairs as warning lines.
func Warnings(ctx context.Context, s *Set, adj *Adjacency) ([]string, error) {
	pairs, err := FindAdjacent(ctx, s, adj, AdjacentOptions{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range ForbiddenPairs(pairs) {
		out = append(out, a.Warning())
	}
	return out, nil
}
