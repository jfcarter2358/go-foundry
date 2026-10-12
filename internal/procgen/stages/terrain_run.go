package stages

// terrain_run.go — the terrain stage's run: generate, write, tile, cook,
// place.
//
//  1. terrain.Generate on the run's worker pool (the first half of the
//     progress bar when cooking).
//  2. The images (terrain.Encode, side by side) and terrain.yaml, the
//     metadata later stages read (internal/procgen/terrain/meta.go has the
//     layout and the encodings).
//  3. TILES. The field is cut into tile_size squares, each a CLOSED mesh
//     (meshout.HeightGrid: the top, skirts down to one common base, a bottom)
//     so `collision: mesh` makes each one parity solid for nav. A tile's mesh
//     is in tile-local coordinates about its centre and the placement puts
//     it there, so every tile has the same X/Z extent.
//  4. THE COOK, with three things that keep neighbours meeting exactly:
//     - LockRect on the tile's rectangle: the rim never simplifies, so
//       every LOD of a tile meets every LOD of its neighbour;
//     - one QUANTISATION STEP for every tile (meshout.CookOptions.
//       PositionStep), chosen from the tallest tile, so shared seam vertices
//       snap to the same grid point — a taller tile would otherwise get a
//       coarser grid and open the seam by up to half a step;
//     - tile-local coordinates that are exact in float32 on both sides of
//       a seam (sample positions are multiples of the resolution, tile
//       centres multiples of half a tile).
//     terrain_seam_test.go checks the seam after quantisation at several
//     DAG cut errors.
//  5. MEMORY AND TIME. A tile that collides as its mesh is cooked mesh-only
//     (meshout.CookOptions.CollisionMesh): no convex decomposition, which
//     was 40% of a dense tile's cook and nearly all of its memory. A cook
//     holds its mesh and its DAG while it works, measured per triangle
//     (cookBytesPerTriangle); cooks run side by side only as far as
//     cook_memory allows (at least one).
//  6. Placements: `tile_<i>_<j>` at the tile's centre, collision as chosen,
//     `chunk: auto` (resolved by the splice against the level's chunks).

import (
	"fmt"
	"math"
	"runtime"
	"sync/atomic"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/terrain"

	"gopkg.in/yaml.v2"
)

// cookBytesPerTriangle is a tile cook's peak memory per source triangle,
// measured (docs/canyon/PHASE_1.md) on 0.5 m tiles of 134k triangles: the
// import pool, the cluster DAG, the SDF, the collision BVH and the pages,
// with Go's garbage headroom — and, unless the cook is mesh-only, the convex
// decomposition, which is most of it (1.1 GB a tile with it, 130 MB
// without).
const (
	cookBytesPerTriangle         = 8500
	cookMeshOnlyBytesPerTriangle = 1000
)

// tilePlan is the tile grid of a field.
type tilePlan struct {
	count, per, step int     // tiles a side, cells a tile, samples per vertex
	size             float64 // metres
	base             float32
	quant            float32 // the shared quantisation step
}

func planTiles(p procgen.Params, g *terrain.Grid) tilePlan {
	tp := tilePlan{size: p.Float("tile_size"), step: max(p.Int("mesh_step"), 1)}
	tp.count, _ = whole(g.Size(), tp.size)
	tp.per, _ = whole(tp.size, g.Cell)
	lo, hi := g.Range()
	// The common base: a few metres under the lowest point, on a whole metre.
	tp.base = float32(math.Floor(float64(lo) - max(4, 0.02*float64(hi-lo))))
	// The step every tile can reach: from the largest extent any tile has.
	ext := max(float64(tp.size), float64(hi-tp.base))
	tp.quant = float32(math.Ldexp(1, int(math.Floor(math.Log2(ext)))-18))
	return tp
}

// tileName is tile (i, j)'s file stem and placement ID.
func tileName(i, j int) string { return fmt.Sprintf("tile_%d_%d", i, j) }

// tileGridAt is tile (ti, tj) as a HeightGrid in tile-local coordinates, and
// its centre in the world.
func tileGridAt(g *terrain.Grid, tp tilePlan, ti, tj int, uvScale float32, terrainUV bool) (meshout.HeightGrid, procgen.Point3) {
	ts := float32(tp.size)
	cx := float32(g.MinX) + ts*(float32(ti)+0.5)
	cz := float32(g.MinZ) + ts*(float32(tj)+0.5)
	n := tp.per / tp.step
	// World UVs are the WORLD position's, so a tiling texture runs on across
	// the seams; terrain UVs put the whole field on 0..1.
	uvOff := [2]float32{cx * uvScale, cz * uvScale}
	if terrainUV {
		size := float32(g.Size())
		uvScale = 1 / size
		uvOff = [2]float32{(cx - float32(g.MinX)) / size, (cz - float32(g.MinZ)) / size}
	}
	hg := meshout.HeightGrid{
		X0: -ts / 2, Z0: -ts / 2, SizeX: ts, SizeZ: ts, NX: n, NZ: n,
		Closed: true, Base: tp.base, UVScale: uvScale, UVOffset: uvOff,
		Height: func(i, j int) float32 {
			return g.At(ti*tp.per+i*tp.step, tj*tp.per+j*tp.step)
		},
	}
	return hg, procgen.Point3{X: cx, Y: 0, Z: cz}
}

// tileTriangles is a tile's triangle count: the top, the skirts, the bottom.
func tileTriangles(tp tilePlan) int {
	n := tp.per / tp.step
	return 2*n*n + 8*n + 4*n
}

// cookConcurrency is how many tiles may cook at once within budgetMB.
func cookConcurrency(tris, budgetMB, workers int, meshOnly bool) int {
	per := float64(tris) * cookBytesPerTriangle
	if meshOnly {
		per = float64(tris) * cookMeshOnlyBytesPerTriangle
	}
	k := int(float64(budgetMB) * (1 << 20) / per)
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	return min(max(k, 1), workers)
}

func runTerrain(rc *procgen.RunContext) error {
	p := rc.Params
	s := terrainSettings(&rc.Env)
	cook := p.Bool("cook")
	share := float32(1)
	if cook {
		share = 0.45
	}
	t0 := time.Now()
	res, err := terrain.Generate(&s, &rc.Env, func(_ terrain.Phase, done float32) { rc.Progress(done * share) })
	if err != nil {
		return err
	}
	g := res.Grid
	lo, hi := g.Range()
	rc.Logf("field %d² at %g m, heights %.1f .. %.1f m, %d droplets, %s", g.S(), g.Cell, lo, hi, res.Droplets,
		time.Since(t0).Round(time.Millisecond))
	for _, ph := range []terrain.Phase{terrain.PhaseShape, terrain.PhaseCanyons, terrain.PhaseDrainage, terrain.PhaseStrata,
		terrain.PhaseDroplets, terrain.PhaseThermal, terrain.PhaseMasks} {
		rc.Logf("  %-9s %s", ph, res.Timings[ph].Round(time.Millisecond))
	}

	meta := terrain.NewMeta(&s, res)
	t1 := time.Now()
	files, err := terrain.Encode(res, meta, &rc.Env)
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(files) {
		if err := rc.WriteFile(name, files[name]); err != nil {
			return err
		}
	}
	files = nil
	rc.Logf("images written in %s", time.Since(t1).Round(time.Millisecond))

	tp := planTiles(p, g)
	tris := tileTriangles(tp)
	meta.Tiles = terrain.TilesMeta{Size: tp.size, Count: tp.count, Base: tp.base, Triangles: tris}
	if cook {
		meta.Tiles.Step = tp.quant
	}
	ext := ".glb"
	if cook {
		ext = ".fnm"
	}
	for tj := 0; tj < tp.count; tj++ {
		for ti := 0; ti < tp.count; ti++ {
			meta.Tiles.Files = append(meta.Tiles.Files, tileName(ti, tj)+ext)
		}
	}
	if err := writeTiles(rc, g, tp, tris, cook, share); err != nil {
		return err
	}
	// The backdrop round the map (terrain_far.go), when asked for.
	if err := writeFar(rc, &s, res, &meta, tp, cook); err != nil {
		return err
	}
	rc.Artifact = terrain.FromResult(res, meta, rc.Dir)
	data, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	return rc.WriteFile(terrain.MetaFile, data)
}

// writeTiles builds, cooks (or writes) and places every tile.
func writeTiles(rc *procgen.RunContext, g *terrain.Grid, tp tilePlan, tris int, cook bool, share float32) error {
	p := rc.Params
	n := tp.count * tp.count
	collision := p.Str("collision")
	// A tile placed to collide as its mesh (or auto, which picks mesh at a
	// tile's size) never needs hulls: skip the decomposition.
	meshOnly := collision == "mesh" || collision == "auto"
	conc := cookConcurrency(tris, p.Int("cook_memory"), rc.Workers, meshOnly)
	if cook {
		rc.Logf("%d tiles of %g m, %d triangles each, cooking %d at a time on a %.3g mm grid",
			n, tp.size, tris, conc, tp.quant*1000)
	}
	sem := make(chan struct{}, conc)
	tex := p.Str("texture")
	uv := float32(p.Float("uv_scale"))
	t0 := time.Now()
	var done atomic.Int64
	var skipped int
	type outcome struct{ skipped bool }
	results := make([]outcome, n)
	err := rc.Parallel(n, func(k int) error {
		sem <- struct{}{}
		defer func() { <-sem }()
		if err := rc.Err(); err != nil {
			return err
		}
		ti, tj := k%tp.count, k/tp.count
		hg, centre := tileGridAt(g, tp, ti, tj, uv, p.Str("uv_mode") == "terrain")
		var b meshout.Builder
		b.Slot("ground", tex)
		hg.Build(&b)
		m := b.Mesh()
		name := tileName(ti, tj)
		if !cook {
			return meshout.WriteGLB(rc.Path(name+".glb"), m)
		}
		rect := hg.Rect()
		sk, err := meshout.Cook(m, rc.Path(name+".fnm"), meshout.CookOptions{LockRect: &rect, PositionStep: tp.quant,
			CollisionMesh: meshOnly})
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		results[k] = outcome{sk}
		rc.Progress(share + (1-share)*float32(done.Add(1))/float32(n))
		rc.Place(procgen.Placement{
			ID: name, Model: rc.AssetPath(name + ".fnm"), Position: centre,
			Collision: collision, Chunk: procgen.ChunkAuto, Material: p.Str("material"),
		})
		return nil
	})
	if err != nil {
		return err
	}
	for _, r := range results {
		if r.skipped {
			skipped++
		}
	}
	if cook {
		rc.Logf("tiles cooked in %s (%d up to date)", time.Since(t0).Round(time.Millisecond), skipped)
	}
	rc.Progress(1)
	return nil
}

// sortedKeys is m's keys in order, so files are written in a fixed order.
func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// tilePreviewMesh is a tile's top surface as a preview mesh, in world space.
func tilePreviewMesh(name string, g *terrain.Grid, tp tilePlan, ti, tj int) procgen.PreviewMesh {
	hg, c := tileGridAt(g, tp, ti, tj, 1, false)
	hg.Closed = false
	hg.X0 += c.X
	hg.Z0 += c.Z
	var b meshout.Builder
	hg.Build(&b)
	m := b.Mesh()
	// Painted with the albedo hint layer: UVs span the whole field.
	pm := procgen.PreviewMesh{Name: name, Indices: m.Indices, Image: "Albedo"}
	pm.Positions = make([][3]float32, len(m.Positions))
	pm.Normals = make([][3]float32, len(m.Normals))
	pm.UVs = make([][2]float32, len(m.Positions))
	size := float32(g.Size())
	for i := range m.Positions {
		pm.Positions[i] = [3]float32(m.Positions[i])
		pm.Normals[i] = [3]float32(m.Normals[i])
		pm.UVs[i] = [2]float32{(m.Positions[i][0] - float32(g.MinX)) / size, (m.Positions[i][2] - float32(g.MinZ)) / size}
	}
	return pm
}
