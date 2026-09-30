package editor

// editor_terrain_brush.go — what one frame of a terrain brush stroke does to
// the grid. Pure functions of the terrain and the brush, so they are tested
// without a window; editor_terrain.go owns the tool, the mouse and the undo.
//
// Everything here is in the terrain's own (chunk-local) space: the tool
// carries the cursor's hit point and the brush radius in before calling.

import (
	"math"

	"foundry/internal/world"
)

// terrainBrushMode is what a stroke does.
type terrainBrushMode int

const (
	terrainRaise terrainBrushMode = iota
	terrainLower
	terrainSmooth
	terrainFlatten
	terrainPaint
	terrainHole
	terrainFill
)

// terrainBrushModes is the toolbar order, which is also the number key each
// mode answers to (1..7) while the tool is armed.
var terrainBrushModes = []terrainBrushMode{
	terrainRaise, terrainLower, terrainSmooth, terrainFlatten, terrainPaint, terrainHole, terrainFill,
}

func (m terrainBrushMode) String() string {
	switch m {
	case terrainRaise:
		return "Raise"
	case terrainLower:
		return "Lower"
	case terrainSmooth:
		return "Smooth"
	case terrainFlatten:
		return "Flatten"
	case terrainPaint:
		return "Paint"
	case terrainHole:
		return "Hole"
	case terrainFill:
		return "Fill"
	}
	return "?"
}

// inverse is the mode Shift turns this one into for the length of a stroke.
func (m terrainBrushMode) inverse() terrainBrushMode {
	switch m {
	case terrainRaise:
		return terrainLower
	case terrainLower:
		return terrainRaise
	case terrainHole:
		return terrainFill
	case terrainFill:
		return terrainHole
	}
	return m
}

// terrainBrush is one application of the brush: where, how big, how hard.
type terrainBrush struct {
	mode terrainBrushMode
	// cx, cz is the centre and radius the reach, in chunk-local units.
	cx, cz, radius float32
	// strength is the panel's 0..1 dial; rates below scale it to units per
	// second.
	strength float32
	// hardness is the fraction of the radius at full strength, 0..1; beyond
	// it the effect falls away smoothly to nothing at the edge.
	hardness float32
	// layer is the texture layer Paint paints.
	layer int
	// target is the local height Flatten pulls toward.
	target float32
	// dt is the frame's length in seconds: the continuous modes are rates,
	// so holding the mouse still keeps raising at the same speed whatever
	// the frame rate.
	dt float32
}

// How fast the continuous modes work at strength 1, per second.
const (
	terrainRaiseRate   = 6 // units of height per second at the centre
	terrainSmoothRate  = 8 // fraction of the way to the neighbours' mean
	terrainFlattenRate = 8 // fraction of the way to the target
	terrainPaintRate   = 4 // fraction of the way to the painted layer
)

// falloff is the brush's weight at distance d from its centre: 1 inside the
// hard core, easing to 0 at the edge.
func (b *terrainBrush) falloff(d float32) float32 {
	if b.radius <= 0 || d >= b.radius {
		return 0
	}
	t := d / b.radius
	h := b.hardness
	if t <= h {
		return 1
	}
	u := (t - h) / (1 - h)
	// smoothstep, inverted
	return 1 - u*u*(3-2*u)
}

// applyTerrainBrush applies b to t and reports whether anything changed and
// the vertex rectangle it touched (for Terrain.TouchRect). It does not Touch;
// the caller does, once, for the stroke's frame.
func applyTerrainBrush(t *world.Terrain, b terrainBrush) (changed bool, x0, z0, x1, z1 int) {
	gx := (b.cx - t.Origin.X()) / t.CellSize
	gz := (b.cz - t.Origin.Z()) / t.CellSize
	gr := b.radius / t.CellSize
	x0 = clampTerrainIndex(int(math.Floor(float64(gx-gr))), t.CellsX)
	z0 = clampTerrainIndex(int(math.Floor(float64(gz-gr))), t.CellsZ)
	x1 = clampTerrainIndex(int(math.Ceil(float64(gx+gr))), t.CellsX)
	z1 = clampTerrainIndex(int(math.Ceil(float64(gz+gr))), t.CellsZ)
	if gx+gr < 0 || gz+gr < 0 || gx-gr > float32(t.CellsX) || gz-gr > float32(t.CellsZ) {
		return false, 0, 0, 0, 0
	}

	dist := func(x, z float32) float32 {
		dx := t.Origin.X() + x*t.CellSize - b.cx
		dz := t.Origin.Z() + z*t.CellSize - b.cz
		return float32(math.Sqrt(float64(dx*dx + dz*dz)))
	}

	switch b.mode {
	case terrainRaise, terrainLower:
		rate := b.strength * terrainRaiseRate * b.dt
		if b.mode == terrainLower {
			rate = -rate
		}
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				if w := b.falloff(dist(float32(x), float32(z))); w > 0 {
					t.Heights[t.Index(x, z)] += rate * w
					changed = true
				}
			}
		}

	case terrainSmooth:
		// Read the neighbours from a copy, so the order the loop visits the
		// vertices in does not lean the result one way.
		k := min32(1, b.strength*terrainSmoothRate*b.dt)
		type update struct {
			i int
			h float32
		}
		var ups []update
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				w := b.falloff(dist(float32(x), float32(z)))
				if w <= 0 {
					continue
				}
				sum, n := float32(0), float32(0)
				for dz := -1; dz <= 1; dz++ {
					for dx := -1; dx <= 1; dx++ {
						nx, nz := x+dx, z+dz
						if nx < 0 || nz < 0 || nx > t.CellsX || nz > t.CellsZ {
							continue
						}
						sum += t.Height(nx, nz)
						n++
					}
				}
				h := t.Height(x, z)
				ups = append(ups, update{t.Index(x, z), h + (sum/n-h)*k*w})
			}
		}
		for _, u := range ups {
			if t.Heights[u.i] != u.h {
				t.Heights[u.i] = u.h
				changed = true
			}
		}

	case terrainFlatten:
		k := min32(1, b.strength*terrainFlattenRate*b.dt)
		target := b.target - t.Origin.Y()
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				w := b.falloff(dist(float32(x), float32(z)))
				if w <= 0 {
					continue
				}
				i := t.Index(x, z)
				if h := t.Heights[i] + (target-t.Heights[i])*k*w; h != t.Heights[i] {
					t.Heights[i] = h
					changed = true
				}
			}
		}

	case terrainPaint:
		k := min32(1, b.strength*terrainPaintRate*b.dt)
		for z := z0; z <= z1; z++ {
			for x := x0; x <= x1; x++ {
				w := b.falloff(dist(float32(x), float32(z)))
				if w <= 0 {
					continue
				}
				before := t.Weight(x, z, b.layer)
				t.PaintWeight(x, z, b.layer, k*w)
				if t.Weight(x, z, b.layer) != before {
					changed = true
				}
			}
		}

	case terrainHole, terrainFill:
		// Whole cells, by their centres, and no falloff: a cell is a hole or
		// it is not.
		hole := b.mode == terrainHole
		reached := false
		set := func(x, z int) {
			reached = true
			if t.IsHole(x, z) != hole {
				t.SetHole(x, z, hole)
				changed = true
			}
		}
		for z := max(z0-1, 0); z < min(z1+1, t.CellsZ); z++ {
			for x := max(x0-1, 0); x < min(x1+1, t.CellsX); x++ {
				if dist(float32(x)+0.5, float32(z)+0.5) <= b.radius {
					set(x, z)
				}
			}
		}
		// A brush smaller than a cell reaches no cell's centre; it still
		// means the cell under it.
		if !reached {
			if cx, cz, _, _, ok := t.CellAt(b.cx, b.cz); ok {
				set(cx, cz)
			}
		}
		// The cells' corners are the rectangle the renderer re-uploads.
		x0, z0 = max(x0-1, 0), max(z0-1, 0)
		x1, z1 = min(x1+1, t.CellsX), min(z1+1, t.CellsZ)
	}
	return changed, x0, z0, x1, z1
}

func clampTerrainIndex(v, hi int) int {
	if v < 0 {
		return 0
	}
	if v > hi {
		return hi
	}
	return v
}
