package stages

// region_terrain_water_far.go — the sea carried out over the far backdrop.
//
// A region terrain's water surfaces (region_terrain_water.go) cover only its
// own tiles, but the far backdrop (terrain_far.go) continues the landforms
// past the union's edge — a coast's sea floor runs on to the horizon. So a
// coast seen from its beach had water out to the union's edge and dry sea
// floor beyond it, ending in a straight line (docs/regions/PRESETS_2.md has
// the render).
//
// For every SEA body — a region's water level, not a pond, of a region whose
// preset has a COASTLINE (a wetland's flooded low ground is a level body
// too, but its backdrop is land: a sheet there drowned the horizon) — that
// reaches an edge of the union, this lays a flat sheet at its level over the backdrop
// beyond that edge: across the body's extent along the edge (out to the
// backdrop's corners where the body reaches the union's corner), and out to
// the backdrop's reach. The ±x sheets take the corners, the ±z sheets stop
// at the union's x extent, so no two sheets overlap (two coplanar blended
// sheets would double the water's tint). The far ground above the level
// hides the sheet as the inner tiles' banks hide theirs: a headland
// continued on the backdrop still stands out of the water.
//
// Placed as water_far_<side>_<k>, the stage's own placements like the tile
// water, wearing the same material; only when the stage cooks and made a
// backdrop. A sheet is a lattice of farWaterCell cells so its vertices sit
// near enough to the camera for the fog and the water shader's per-vertex
// terms.

import (
	"fmt"
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
)

// farWaterCell is the far sheets' lattice spacing, metres.
const farWaterCell = 64

// farWaterRect is one sheet: its world rectangle and level.
type farWaterRect struct {
	side                   string
	minX, minZ, maxX, maxZ float64
	level                  float64
}

// farWaterRects is the sheets for w's sea bodies over a backdrop far round
// the union grid g.
func farWaterRects(g *terrain.Grid, far *terrain.FarMeta, w *terrain.WaterResult, sea func(region int) bool) []farWaterRect {
	gx0, gz0 := g.MinX, g.MinZ
	gx1, gz1 := g.X(g.N), g.MinZ+float64(g.N)*g.Cell
	fx0, fz0, fx1, fz1 := far.Bounds.MinX, far.Bounds.MinZ, far.Bounds.MaxX, far.Bounds.MaxZ
	edge := 1.5 * g.Cell
	var out []farWaterRect
	for _, bi := range w.SortBodies() {
		b := w.Bodies[bi]
		if b.Pond || !sea(b.Region) {
			continue
		}
		bx0, bz0, bx1, bz1 := b.Bounds[0], b.Bounds[1], b.Bounds[2], b.Bounds[3]
		// Along an x side the sheet spans the body's z extent, to the
		// backdrop's corner where the body reaches the union's.
		zs := func() (float64, float64) {
			lo, hi := bz0, bz1
			if lo <= gz0+edge {
				lo = fz0
			}
			if hi >= gz1-edge {
				hi = fz1
			}
			return lo, hi
		}
		if bx1 >= gx1-edge && fx1 > gx1 {
			lo, hi := zs()
			out = append(out, farWaterRect{"px", gx1, lo, fx1, hi, b.Level})
		}
		if bx0 <= gx0+edge && fx0 < gx0 {
			lo, hi := zs()
			out = append(out, farWaterRect{"nx", fx0, lo, gx0, hi, b.Level})
		}
		if bz1 >= gz1-edge && fz1 > gz1 {
			out = append(out, farWaterRect{"pz", math.Max(bx0, gx0), gz1, math.Min(bx1, gx1), fz1, b.Level})
		}
		if bz0 <= gz0+edge && fz0 < gz0 {
			out = append(out, farWaterRect{"nz", math.Max(bx0, gx0), fz0, math.Min(bx1, gx1), gz0, b.Level})
		}
	}
	return out
}

// farWaterMesh is a sheet's lattice, centred on the rectangle's middle.
func farWaterMesh(r farWaterRect, uv float32) *meshout.Builder {
	w, d := r.maxX-r.minX, r.maxZ-r.minZ
	nx, nz := max(1, int(math.Ceil(w/farWaterCell))), max(1, int(math.Ceil(d/farWaterCell)))
	cx, cz := (r.minX+r.maxX)/2, (r.minZ+r.maxZ)/2
	b := &meshout.Builder{}
	b.Slot("water", "")
	up := mgl32.Vec3{0, 1, 0}
	idx := make([]uint32, (nx+1)*(nz+1))
	for j := 0; j <= nz; j++ {
		for i := 0; i <= nx; i++ {
			x := r.minX + w*float64(i)/float64(nx)
			z := r.minZ + d*float64(j)/float64(nz)
			idx[j*(nx+1)+i] = b.Vertex(mgl32.Vec3{float32(x - cx), float32(r.level), float32(z - cz)}, up,
				mgl32.Vec2{float32(x) * uv, float32(z) * uv})
		}
	}
	for j := 0; j < nz; j++ {
		for i := 0; i < nx; i++ {
			a, c := idx[j*(nx+1)+i], idx[(j+1)*(nx+1)+i]
			d, e := idx[(j+1)*(nx+1)+i+1], idx[j*(nx+1)+i+1]
			b.Tri(a, c, d)
			b.Tri(a, d, e)
		}
	}
	return b
}

// writeFarWater cooks and places the sea's sheets over the backdrop.
func writeFarWater(rc *procgen.RunContext, g *terrain.Grid, far *terrain.FarMeta, w *terrain.WaterResult, mat string,
	sea func(region int) bool) error {
	rects := farWaterRects(g, far, w, sea)
	if len(rects) == 0 {
		return nil
	}
	uv := float32(rc.Params.Float("uv_scale"))
	no := false
	for k, r := range rects {
		name := fmt.Sprintf("water_far_%s_%d", r.side, k)
		m := farWaterMesh(r, uv).Mesh()
		if _, err := meshout.Cook(m, rc.Path(name+".fnm"), meshout.CookOptions{LockBorder: true}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		rc.Place(procgen.Placement{ID: name, Model: rc.AssetPath(name + ".fnm"),
			Position:  procgen.Point3{X: float32((r.minX + r.maxX) / 2), Z: float32((r.minZ + r.maxZ) / 2)},
			Collision: "none", Solid: &no, Chunk: procgen.ChunkAuto, Material: mat})
	}
	rc.Logf("water: %d sheets over the backdrop", len(rects))
	return nil
}
