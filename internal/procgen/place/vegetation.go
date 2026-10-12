package place

// vegetation.go — where a biome may grow, as rectangles.
//
// The vegetation stage grows the engine's own biomes (biomes/*.fbio:
// desert, badlands, mesa...) through `kind: biome` scatter volumes, which
// grow their species on whatever ground the column cast finds — since phase
// 1 that includes mesh-collision scenery, so the terrain's tiles. A volume
// is a box, so the masks are honoured by WHICH boxes there are: the ground
// is cut into cells, each weighed by a scatter Rule's bands (slope, height,
// flow, deposition, cliff, the cliff-base term, clumping), and the cells
// over the threshold merged into rectangles — a run of cells along a row,
// carried down the rows while the same run continues, never longer than
// MaxSize a side. Each rectangle is one volume, its density the rule's
// scaled by the cells' mean weight, its height spanning the ground under it
// so the cast from its top meets the ground.

import (
	"math"
	"sort"

	"foundry/internal/procgen/terrain"
)

// Patch is one rectangle of growing ground.
type Patch struct {
	// MinX..MaxZ is its XZ rectangle; MinY..MaxY the ground's height range
	// under it.
	MinX, MinZ, MaxX, MaxZ float64
	MinY, MaxY             float64
	// Weight is its cells' mean weight (0..1.5).
	Weight float64
	Cells  int
}

// PatchSettings configure Patches.
type PatchSettings struct {
	// Cell is the grid's side (metres); Threshold the weight a cell needs;
	// MaxSize the longest a rectangle's side may be (metres).
	Cell, Threshold, MaxSize float64
	// Samples is the weight samples a cell side (the cell's weight is their
	// mean).
	Samples int
	Seed    uint64
	// CliffThreshold is the cliff level the rule's cliff-base term reads.
	CliffThreshold float64
}

// Patches is the rectangles of g where rule r's weight is over the
// threshold.
func Patches(g *Ground, r *Rule, s PatchSettings, pool terrain.Pool) ([]Patch, error) {
	if s.Cell <= 0 {
		s.Cell = 16
	}
	if s.Samples <= 0 {
		s.Samples = 4
	}
	if s.MaxSize <= 0 {
		s.MaxSize = 128
	}
	if s.CliffThreshold <= 0 {
		s.CliffThreshold = 0.5
	}
	var cf *CliffField
	if r.CliffBase > 0 && g.HasMask(terrain.MaskCliff) {
		cf = g.CliffField(s.CliffThreshold)
	}
	b := g.Bounds()
	nx := int(math.Floor((b[2] - b[0]) / s.Cell))
	nz := int(math.Floor((b[3] - b[1]) / s.Cell))
	if nx <= 0 || nz <= 0 {
		return nil, nil
	}
	w := make([]float64, nx*nz)
	lo := make([]float64, nx*nz)
	hi := make([]float64, nx*nz)
	err := pool.Parallel(nz, func(j int) error {
		for i := 0; i < nx; i++ {
			sum := 0.0
			l, h := math.Inf(1), math.Inf(-1)
			for sj := 0; sj < s.Samples; sj++ {
				for si := 0; si < s.Samples; si++ {
					x := b[0] + (float64(i)+(float64(si)+0.5)/float64(s.Samples))*s.Cell
					z := b[1] + (float64(j)+(float64(sj)+0.5)/float64(s.Samples))*s.Cell
					sum += r.Weight(g, cf, s.Seed, x, z)
					y := g.Height(x, z)
					l, h = math.Min(l, y), math.Max(h, y)
				}
			}
			// The corners too, for the height range.
			for _, c := range [][2]float64{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
				y := g.Height(b[0]+(float64(i)+c[0])*s.Cell, b[1]+(float64(j)+c[1])*s.Cell)
				l, h = math.Min(l, y), math.Max(h, y)
			}
			k := j*nx + i
			w[k] = sum / float64(s.Samples*s.Samples)
			lo[k], hi[k] = l, h
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	maxCells := max(int(s.MaxSize/s.Cell), 1)
	var out []Patch
	opens := map[[2]int]*patchRun{}
	for j := 0; j <= nz; j++ {
		// This row's runs.
		var runs [][2]int
		if j < nz {
			for i := 0; i < nx; {
				if w[j*nx+i] < s.Threshold {
					i++
					continue
				}
				i0 := i
				for i < nx && w[j*nx+i] >= s.Threshold && i-i0 < maxCells {
					i++
				}
				runs = append(runs, [2]int{i0, i})
			}
		}
		next := map[[2]int]*patchRun{}
		for _, rn := range runs {
			o := opens[rn]
			if o != nil && j-o.j0 < maxCells {
				delete(opens, rn)
			} else {
				o = &patchRun{j0: j, p: Patch{
					MinX: b[0] + float64(rn[0])*s.Cell, MaxX: b[0] + float64(rn[1])*s.Cell,
					MinZ: b[1] + float64(j)*s.Cell, MinY: math.Inf(1), MaxY: math.Inf(-1)}}
			}
			o.p.MaxZ = b[1] + float64(j+1)*s.Cell
			for i := rn[0]; i < rn[1]; i++ {
				k := j*nx + i
				o.p.Weight += w[k]
				o.p.Cells++
				o.p.MinY = math.Min(o.p.MinY, lo[k])
				o.p.MaxY = math.Max(o.p.MaxY, hi[k])
			}
			next[rn] = o
		}
		// Whatever did not continue is finished, in a fixed order.
		for _, o := range sortedRuns(opens) {
			out = append(out, o.p)
		}
		opens = next
	}
	for i := range out {
		out[i].Weight /= float64(max(out[i].Cells, 1))
	}
	return out, nil
}

// patchRun is a rectangle being grown down the rows: its first row and the
// patch so far.
type patchRun struct {
	j0 int
	p  Patch
}

// sortedRuns is a map's runs ordered by key (row-run start, then end), so
// the patches come out in the same order every run.
func sortedRuns(m map[[2]int]*patchRun) []*patchRun {
	keys := make([][2]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a][0] != keys[b][0] {
			return keys[a][0] < keys[b][0]
		}
		return keys[a][1] < keys[b][1]
	})
	out := make([]*patchRun, len(keys))
	for i, k := range keys {
		out[i] = m[k]
	}
	return out
}
