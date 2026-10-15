package stages

// region_terrain_water.go — standing water in a region terrain
// (docs/regions/PRESETS.md, "Engine work", item 4): seas, flooded wetlands
// and ponds, placed as water surfaces the region terrain owns.
//
// WHAT IS WET is worked out by terrain.FindWater over the finished (eroded)
// union: every region whose preset terrain stage says `water: true` stands
// under water wherever its ground is below `water_level` (a coast's sea, a
// wetland's water table), and with `ponds: true` its closed hollows fill to
// just below their rims. A sample belongs to the region with the largest
// share there. Off for every region (every preset before this), nothing
// here runs and the stage's output is what it always was.
//
// HOW IT IS AUTHORED. A level's water is a surface wearing a material with
// the `water` effect (resources/materials/water.fmat: "put a brush face on
// this and it is water"); there is no water volume. A generator places
// scenery, not brushes, so each terrain tile with water in it gets a flat
// mesh at the water's height — water_<i>_<j>, in tile-local coordinates
// like the tile, cooked to Nanite and placed at the tile's centre with
// collision none and not solid — wearing:
//
//   - the stage's `water_material` when it names one, else
//   - gen_<doc>_<stage>_water, written here: the `water` effect over the
//     `water_texture` (default `water`, the engine's own), with the stage's
//     wave settings.
//
// A cell (four samples) gets water when any of its corners is under it, at
// the highest of their levels, and so do the cells within waterShoreCells
// of one: the plane runs on under the bank, so the ground hides its edge
// and the shoreline is where the ground rises through the water. Each cell
// is two triangles over shared corners — no T-junctions, which the cook's
// position quantisation would open into cracks — and the cook simplifies
// the flat interior away. The cook locks the tile's rectangle and the
// surface's own outline, so neighbouring tiles' water meets at every LOD
// and no LOD pulls the edge out from under the bank.
//
// Also written: mask_water.png (the depth under the surface over
// terrain.WaterMaskDepth, listed in terrain.yaml's masks so
// Terrain.Mask("water") reads it — the ground material's wet rule does),
// and terrain.yaml's `water:` (each body: sea or pond, level, bounds, area,
// region). The placements are the stage's own, so a splice by its prefix
// replaces them with the tiles; region_material's per-tile pass never
// touches them (it re-dresses only placements wearing its material).

import (
	"fmt"
	"math"
	"sync/atomic"

	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
)

// regionWaterParams are the stage's water parameters (Added).
func regionWaterParams() []procgen.ParamDef {
	return []procgen.ParamDef{
		{Name: "water_material", Added: true, Type: procgen.ParamString, Label: "Water material", Default: procgen.String(""),
			Doc: "The material the water surfaces wear (any .fmat with a water effect). Empty: the stage writes " +
				"gen_<generator>_<stage>_water from the texture and waves below. Only regions whose preset asks for water " +
				"(the terrain stage's Water at a level / Fill ponds) get any."},
		{Name: "water_texture", Added: true, Type: procgen.ParamTexture, Label: "Water texture", Default: procgen.Texture("water"),
			Doc: "The written water material's texture."},
		{Name: "water_waves", Added: true, Type: procgen.ParamFloat, Label: "Wave height (m)", Default: procgen.Float(0.06),
			Min: 0, Max: 2, Step: 0.01, Doc: "The written water material's swell."},
	}
}

// regionWater is the union's standing water, or nil when no region's
// preset asks for any.
func regionWater(plan *unionPlan, res *terrain.Result) *terrain.WaterResult {
	rules := make([]terrain.WaterRule, len(plan.pres))
	any := false
	for i, pt := range plan.pres {
		rules[i] = waterRule(pt.params)
		any = any || rules[i].HasLevel || rules[i].Ponds
	}
	if !any {
		return nil
	}
	return terrain.FindWater(res.Grid, res.Dominant, rules)
}

// waterMeta is terrain.yaml's water section.
func waterMeta(plan *unionPlan, w *terrain.WaterResult) *terrain.WaterMeta {
	wm := &terrain.WaterMeta{Mask: terrain.MaskWater}
	for _, bi := range w.SortBodies() {
		b := w.Bodies[bi]
		kind := "sea"
		if b.Pond {
			kind = "pond"
		}
		wm.Bodies = append(wm.Bodies, terrain.WaterBodyMeta{Kind: kind, Level: round3(b.Level),
			Bounds: [4]float64{round3(b.Bounds[0]), round3(b.Bounds[1]), round3(b.Bounds[2]), round3(b.Bounds[3])},
			Area:   math.Round(b.Area), Region: plan.set.Regions[b.Region].Key})
	}
	return wm
}

// waterTileName is tile (i, j)'s water mesh's stem and placement ID.
func waterTileName(i, j int) string { return fmt.Sprintf("water_%d_%d", i, j) }

// waterShoreCells is how many cells past the last wet one the surface runs
// on under the bank: the shoreline is then where the ground rises through
// the water, not the mesh's stepped edge, even where a distant LOD of the
// ground sits a little lower than its samples.
const waterShoreCells = 2

// waterCells is every cell's water height (the cell whose corner is sample
// (ci, cj); g.N² of them), NaN where dry: the highest level of its wet
// corners, then grown waterShoreCells cells outward (each grown cell the
// highest level beside it).
func waterCells(g *terrain.Grid, w *terrain.WaterResult) []float32 {
	s, n := g.S(), g.N
	nan := float32(math.NaN())
	lv := make([]float32, n*n)
	for cj := 0; cj < n; cj++ {
		for ci := 0; ci < n; ci++ {
			v := nan
			for _, d := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
				k := (cj+d[1])*s + ci + d[0]
				if w.Body[k] >= 0 && (v != v || w.Level[k] > v) {
					v = w.Level[k]
				}
			}
			lv[cj*n+ci] = v
		}
	}
	for step := 0; step < waterShoreCells; step++ {
		next := append([]float32(nil), lv...)
		for cj := 0; cj < n; cj++ {
			for ci := 0; ci < n; ci++ {
				if lv[cj*n+ci] == lv[cj*n+ci] {
					continue
				}
				v := nan
				for dj := -1; dj <= 1; dj++ {
					for di := -1; di <= 1; di++ {
						i, j := ci+di, cj+dj
						if i < 0 || j < 0 || i >= n || j >= n {
							continue
						}
						if q := lv[j*n+i]; q == q && (v != v || q > v) {
							v = q
						}
					}
				}
				next[cj*n+ci] = v
			}
		}
		lv = next
	}
	return lv
}

// waterTileMesh is tile (ti, tj)'s water surface in tile-local coordinates
// (nil: dry), its UVs world-projected at uv per metre.
func waterTileMesh(g *terrain.Grid, tp tilePlan, cells []float32, ti, tj int, uv float32) *meshout.Builder {
	ts := float32(tp.size)
	cx := float32(g.MinX) + ts*(float32(ti)+0.5)
	cz := float32(g.MinZ) + ts*(float32(tj)+0.5)
	cell := float32(g.Cell)
	var b *meshout.Builder
	up := mgl32.Vec3{0, 1, 0}
	// Shared corners, keyed by (corner, level): one cell is two triangles
	// over four shared vertices, so the surface is one connected sheet with
	// no T-junction for the cook's position quantisation to open into a
	// crack (the cook then simplifies the flat interior away).
	type key struct {
		i, j int
		y    float32
	}
	index := map[key]uint32{}
	vert := func(ci, cj int, y float32) uint32 {
		k := key{ci, cj, y}
		if v, ok := index[k]; ok {
			return v
		}
		x := float32(ci-ti*tp.per)*cell - ts/2
		z := float32(cj-tj*tp.per)*cell - ts/2
		v := b.Vertex(mgl32.Vec3{x, y, z}, up, mgl32.Vec2{(x + cx) * uv, (z + cz) * uv})
		index[k] = v
		return v
	}
	for cj := tj * tp.per; cj < (tj+1)*tp.per; cj++ {
		for ci := ti * tp.per; ci < (ti+1)*tp.per; ci++ {
			lv := cells[cj*g.N+ci]
			if lv != lv {
				continue
			}
			if b == nil {
				b = &meshout.Builder{}
				b.Slot("water", "")
			}
			// Counter-clockwise from above, as the terrain's top.
			a := vert(ci, cj, lv)
			c := vert(ci, cj+1, lv)
			d := vert(ci+1, cj+1, lv)
			e := vert(ci+1, cj, lv)
			b.Tri(a, c, d)
			b.Tri(a, d, e)
		}
	}
	return b
}

// writeWater writes the water mask's meta, the material and every tile's
// water surface, and places them. The mask file itself is written with the
// other images by the caller.
func writeWater(rc *procgen.RunContext, g *terrain.Grid, tp tilePlan, w *terrain.WaterResult, cook bool) (string, error) {
	p := rc.Params
	mat := p.Str("water_material")
	if mat == "" {
		mat = genMaterialName(rc, "water")
		d := &material.Doc{Name: mat, Domain: material.DomainSurface, Texture: p.Str("water_texture"),
			Doc: "Generated water: the region terrain's seas and ponds"}
		e := d.AddEffect(material.KindWater)
		e.Set("wave_amplitude", material.FloatValue(float32(p.Float("water_waves"))))
		if err := writeMaterial(rc, d); err != nil {
			return "", err
		}
	}
	uv := float32(p.Float("uv_scale"))
	no := false
	n := tp.count * tp.count
	placed := make([]*procgen.Placement, n)
	cells := waterCells(g, w)
	var tris atomic.Int64
	if err := rc.Parallel(n, func(k int) error {
		ti, tj := k%tp.count, k/tp.count
		b := waterTileMesh(g, tp, cells, ti, tj, uv)
		if b == nil {
			return nil
		}
		name := waterTileName(ti, tj)
		tris.Add(int64(b.TriangleCount()))
		m := b.Mesh()
		pl := &procgen.Placement{ID: name, Position: tileCentre(g, tp, ti, tj), Collision: "none", Solid: &no,
			Chunk: procgen.ChunkAuto, Material: mat}
		if !cook {
			// Uncooked, as the tiles: written, not placed.
			return meshout.WriteGLB(rc.Path(name+".glb"), m)
		}
		ts := float32(tp.size)
		rect := [4]float32{-ts / 2, -ts / 2, ts / 2, ts / 2}
		if _, err := meshout.Cook(m, rc.Path(name+".fnm"), meshout.CookOptions{LockRect: &rect, LockBorder: true, CollisionMesh: true}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		pl.Model = rc.AssetPath(name + ".fnm")
		placed[k] = pl
		return nil
	}); err != nil {
		return "", err
	}
	count := 0
	for _, pl := range placed {
		if pl != nil {
			rc.Place(*pl)
			count++
		}
	}
	rc.Logf("water: %d bodies, %d tiles with water surfaces (%d triangles), material %s", len(w.Bodies), count, tris.Load(), mat)
	return mat, nil
}
