package terrain

// water.go — standing water over a finished heightfield (docs/regions/
// PRESETS.md, "Engine work", item 4): where a region's preset asks for a
// WATER LEVEL (a sea at the coast, a flooded wetland) and where its ground
// holds PONDS (closed hollows: thermokarst ponds, kettle lakes, a wetland's
// pools), which surface stands at what height.
//
// It runs on the ERODED heights (after every pass), so what it fills is the
// ground the player walks on, and it is a pure function of the grid, the
// per-sample owner and each region's water settings:
//
//   - LEVEL. A sample whose owning region (the one with the largest share)
//     has a water level and whose ground is below it is under that level.
//     No connectivity is asked for: low ground in a wetland floods at the
//     wetland's level whether or not it reaches the sea, which is what a
//     water table does.
//   - PONDS. The heights are priority-flooded with no slope forced across
//     flats (FillPits): a closed hollow fills to its SPILL height, the
//     lowest point of its rim. Each connected hollow (8-connected samples
//     the fill raised) is a candidate pond; it is kept when the region
//     owning its deepest sample asks for ponds, it is at least MinDepth deep
//     and at least MinArea in area. Its surface stands Freeboard below the
//     spill height, so the water never spills over the rim it was measured
//     by and the shore is a line on the ground rather than a sheet on the
//     lip.
//   - A sample under both takes the higher surface.
//
// Bodies are the connected water areas of one surface height each: one per
// region level ("sea") however many pieces it is in, one per pond. The
// stage (stages/region_terrain_water.go) meshes the surfaces per terrain
// tile and places them wearing a water material, writes the depth mask
// (MaskWater) the ground material's wet rule reads, and lists the bodies
// in terrain.yaml.

import (
	"math"
	"sort"
)

// WaterRule is one region's water settings.
type WaterRule struct {
	// Level, when HasLevel, is the water table: ground below it is under
	// water.
	HasLevel bool
	Level    float64
	// Ponds fills the region's closed hollows that are at least MinDepth
	// deep and MinArea (m²) in area.
	Ponds    bool
	MinDepth float64
	MinArea  float64
}

// WaterBody is one surface.
type WaterBody struct {
	// Pond: a filled hollow; otherwise a region's level.
	Pond   bool
	Level  float64
	Region int
	// Bounds are its samples' world [min x, min z, max x, max z]; Area its
	// area (m²) and Samples its sample count.
	Bounds  [4]float64
	Area    float64
	Samples int
}

// WaterResult is the standing water on a grid.
type WaterResult struct {
	// Level is each sample's water surface height, NaN where dry; Body
	// the index into Bodies (-1 dry).
	Level  []float32
	Body   []int32
	Bodies []WaterBody
}

// Depth is sample k's water depth (0 dry).
func (w *WaterResult) Depth(g *Grid, k int) float32 {
	if w.Body[k] < 0 {
		return 0
	}
	return max(w.Level[k]-g.H[k], 0)
}

// Wet reports whether any sample is under water.
func (w *WaterResult) Wet() bool { return len(w.Bodies) > 0 }

// pondFreeboard is how far below its spill height a pond's surface stands.
const pondFreeboard = 0.05

// FindWater is the standing water on g (see the file comment). owner(k) is
// the region owning sample k (the largest share); rules are by region.
func FindWater(g *Grid, owner func(k int) int, rules []WaterRule) *WaterResult {
	s := g.S()
	n := s * s
	nan := float32(math.NaN())
	w := &WaterResult{Level: make([]float32, n), Body: make([]int32, n)}
	for k := range w.Level {
		w.Level[k] = nan
		w.Body[k] = -1
	}
	rule := func(r int) *WaterRule {
		if r < 0 || r >= len(rules) {
			return nil
		}
		return &rules[r]
	}
	// Ponds first, then levels on top where higher.
	anyPonds := false
	for i := range rules {
		anyPonds = anyPonds || rules[i].Ponds
	}
	if anyPonds {
		filled := g.Clone()
		fillPits(filled)
		comp := make([]int32, n)
		for k := range comp {
			comp[k] = -1
		}
		var stack []int
		cell2 := g.Cell * g.Cell
		for k0 := 0; k0 < n; k0++ {
			if comp[k0] >= 0 || filled.H[k0]-g.H[k0] <= 1e-4 {
				continue
			}
			// One hollow: flood its raised samples.
			id := int32(k0)
			stack = append(stack[:0], k0)
			comp[k0] = id
			members := []int{}
			deep, deepK := float32(0), k0
			spill := filled.H[k0]
			for len(stack) > 0 {
				k := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				members = append(members, k)
				if d := filled.H[k] - g.H[k]; d > deep {
					deep, deepK = d, k
				}
				spill = max(spill, filled.H[k])
				i, j := k%s, k/s
				for d := 0; d < 8; d++ {
					ni, nj := i+nbDI[d], j+nbDJ[d]
					if ni < 0 || nj < 0 || ni >= s || nj >= s {
						continue
					}
					q := nj*s + ni
					if comp[q] >= 0 || filled.H[q]-g.H[q] <= 1e-4 {
						continue
					}
					comp[q] = id
					stack = append(stack, q)
				}
			}
			r := owner(deepK)
			ru := rule(r)
			if ru == nil || !ru.Ponds || float64(deep) < ru.MinDepth || float64(len(members))*cell2 < ru.MinArea {
				continue
			}
			level := spill - pondFreeboard
			b := WaterBody{Pond: true, Level: float64(level), Region: r}
			bi := int32(len(w.Bodies))
			for _, k := range members {
				if g.H[k] < level {
					w.Level[k] = level
					w.Body[k] = bi
				}
			}
			w.Bodies = append(w.Bodies, b)
		}
	}
	// Levels: one body per region.
	levelBody := map[int]int32{}
	for k := 0; k < n; k++ {
		r := owner(k)
		ru := rule(r)
		if ru == nil || !ru.HasLevel || float64(g.H[k]) >= ru.Level {
			continue
		}
		lv := float32(ru.Level)
		if w.Body[k] >= 0 && w.Level[k] >= lv {
			continue
		}
		bi, ok := levelBody[r]
		if !ok {
			bi = int32(len(w.Bodies))
			levelBody[r] = bi
			w.Bodies = append(w.Bodies, WaterBody{Level: ru.Level, Region: r})
		}
		w.Level[k] = lv
		w.Body[k] = bi
	}
	// Each body's extent and area; bodies left with no sample (a pond the
	// sea swallowed) are dropped and the rest renumbered in order.
	for b := range w.Bodies {
		w.Bodies[b].Bounds = [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	}
	for k := 0; k < n; k++ {
		b := w.Body[k]
		if b < 0 {
			continue
		}
		wb := &w.Bodies[b]
		x, z := g.X(k%s), g.Z(k/s)
		wb.Bounds = [4]float64{math.Min(wb.Bounds[0], x), math.Min(wb.Bounds[1], z), math.Max(wb.Bounds[2], x), math.Max(wb.Bounds[3], z)}
		wb.Samples++
	}
	remap := make([]int32, len(w.Bodies))
	var kept []WaterBody
	for b, wb := range w.Bodies {
		remap[b] = -1
		if wb.Samples == 0 {
			continue
		}
		wb.Area = float64(wb.Samples) * g.Cell * g.Cell
		remap[b] = int32(len(kept))
		kept = append(kept, wb)
	}
	for k := range w.Body {
		if b := w.Body[k]; b >= 0 {
			w.Body[k] = remap[b]
			if remap[b] < 0 {
				w.Level[k] = nan
			}
		}
	}
	w.Bodies = kept
	return w
}

// fillPits raises every closed hollow of g to its spill height (a priority
// flood with no slope forced across flats).
func fillPits(g *Grid) {
	s := g.S()
	f := newFlood(s * s)
	f.run(g.H, s, 0)
	copy(g.H, f.filled)
}

// FillPits is g's heights with every closed hollow raised to its spill
// height (a copy; g is unchanged).
func FillPits(g *Grid) []float32 {
	c := g.Clone()
	fillPits(c)
	return c.H
}

// WaterMaskValues is the depth mask (0..1 over WaterMaskDepth metres) of w.
func WaterMaskValues(g *Grid, w *WaterResult) []float32 {
	out := make([]float32, len(g.H))
	for k := range out {
		if d := w.Depth(g, k); d > 0 {
			out[k] = min(d/float32(WaterMaskDepth), 1)
		}
	}
	return out
}

// SortBodies is the bodies' indices, largest first (ties by index): the
// order a listing shows them in.
func (w *WaterResult) SortBodies() []int {
	idx := make([]int, len(w.Bodies))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return w.Bodies[idx[a]].Samples > w.Bodies[idx[b]].Samples })
	return idx
}
