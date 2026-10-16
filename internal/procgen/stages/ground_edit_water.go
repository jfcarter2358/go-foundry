package stages

// ground_edit_water.go — ground_edit's ponds, lakes and marshes: carving
// each water layer, the bodies table, and the water meshes
// (docs/terrain-paint/PHASE_W2.md).
//
// BODIES AND THEIR KEYS. A body is one connected painted area of one water
// layer (terrain.CarveBasins). Its NAME is its anchor, the most inland
// sample, as world (x, z); the `bodies` param keys per-body settings by a
// point inside the body instead (`at`), and an entry applies to whichever
// body's painted area holds its point. So repainting a pond's edges, even
// moving its anchor, keeps its pinned level and material; only painting the
// point itself dry orphans the entry (water_bodies.yaml lists orphans, and
// the Generator's panel shows them). The panel's Lock writes the anchor of
// the moment as `at`, which is as far from every edge as the body allows.
//
// THE WATER. All layers' standing water is one terrain.WaterResult over the
// grid — what region_terrain's water path (region_terrain_water.go) meshes —
// split by material: each tile with water gets one flat mesh per material
// present in it, pond_<i>_<j>_<material>, cooked like region_terrain's
// water_<i>_<j> (the surface carried two cells under the bank so the ground,
// not the mesh's stepped edge, draws the shoreline; the tile's rectangle
// and the surface's outline locked so neighbours meet at every LOD) and
// placed with collision none, not solid, wearing the material. A key in the
// file name, as for the tiles, makes a changed surface a new model path.
//
// Also written: terrain.yaml's water bodies (the source's own, then the
// carved ones: kind pond, lake or marsh) and water_bodies.yaml, the table
// the editor's panel reads (layer, style, anchor, level, spill, locked,
// material, areas, depth).

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"

	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"

	"gopkg.in/yaml.v2"
)

// BodiesFile is ground_edit's table of water bodies.
const BodiesFile = "water_bodies.yaml"

// groundWaterVersion changes when a water mesh changes for the same cells.
const groundWaterVersion = "ground-water-1"

// GroundBody is one carved water body as water_bodies.yaml lists it.
type GroundBody struct {
	Layer string `yaml:"layer"`
	Style string `yaml:"style"`
	// Anchor is its most inland point (x, z): its name, and where Lock
	// pins it.
	Anchor [2]float64 `yaml:"anchor,flow"`
	// Level is the water surface; Spill the rim's lowest ground; Locked
	// whether a bodies entry pins the level (Entry: its index, -1 none).
	Level    float64 `yaml:"level"`
	Spill    float64 `yaml:"spill"`
	Locked   bool    `yaml:"locked"`
	Entry    int     `yaml:"entry"`
	Material string  `yaml:"material"`
	// Painted and Area are the painted and wet areas (m²).
	Painted  float64    `yaml:"painted"`
	Area     float64    `yaml:"area"`
	MaxDepth float64    `yaml:"max_depth"`
	Bounds   [4]float64 `yaml:"bounds,flow"`
}

// GroundBodies is water_bodies.yaml.
type GroundBodies struct {
	Bodies []GroundBody `yaml:"bodies"`
	// Orphans are the bodies entries (indices) that hold no body.
	Orphans []int `yaml:"orphans,omitempty"`
}

// groundWater is what the water layers carved.
type groundWater struct {
	bodies  []GroundBody
	water   *terrain.WaterResult
	orphans []int
}

func (w *groundWater) wet() bool { return w.water != nil && len(w.water.Bodies) > 0 }

// basinProfile is a water item's profile.
func basinProfile(it procgen.Params) terrain.BasinProfile {
	pr := terrain.DefaultBasinProfile()
	pr.Threshold = it.Float("threshold")
	pr.Bank, pr.ShelfDepth, pr.Shelf, pr.Depth = it.Float("bank"), it.Float("shelf_depth"), it.Float("shelf"), it.Float("depth")
	pr.Noise, pr.NoiseSize = it.Float("noise"), it.Float("noise_size")
	pr.Hummocks, pr.HummockSize = it.Float("hummocks"), it.Float("hummock_size")
	pr.BankSlope, pr.Reach, pr.MinArea = it.Float("bank_slope"), it.Float("reach"), it.Float("min_area")
	return pr
}

// carveGroundWater carves every water layer into g in list order and
// gathers the bodies (file comment).
func carveGroundWater(rc *procgen.RunContext, g *terrain.Grid, src *terrain.Terrain) (*groundWater, error) {
	p := rc.Params
	entries := p.Items("bodies")
	pins := make([]terrain.BasinLevel, len(entries))
	for i, e := range entries {
		at := e.Vec2("at")
		pins[i] = terrain.BasinLevel{At: [2]float64{at[0], at[1]}, Level: e.Float("level"), Auto: e.Bool("auto")}
	}
	n := len(g.H)
	nan := float32(math.NaN())
	out := &groundWater{water: &terrain.WaterResult{Level: make([]float32, n), Body: make([]int32, n)}}
	for k := range out.water.Level {
		out.water.Level[k], out.water.Body[k] = nan, -1
	}
	matched := make([]bool, len(entries))
	for wi, it := range p.Items("water") {
		name := it.Str("layer")
		l := rc.Doc.PaintLayer(name)
		if l.Empty() {
			continue
		}
		rect, ok := layerRect(l)
		if !ok {
			continue
		}
		pr := basinProfile(it)
		pr.Freeboard = p.Float("freeboard")
		pr.Floor = float64(src.Meta.Tiles.Base) + 0.25
		pr.Seed = noise.Hash64(rc.Seed, uint64(wi))
		res := terrain.CarveBasins(g, l.Sample, rect, pr, pins)
		style := it.Str("style")
		layerMat := it.Str("material")
		if layerMat == "" {
			layerMat = waterStyleMaterial[style]
		}
		if layerMat == "" {
			layerMat = "water"
		}
		base := int32(len(out.water.Bodies))
		for bi, b := range res.Bodies {
			mat := layerMat
			if b.Pin >= 0 {
				matched[b.Pin] = true
				if m := entries[b.Pin].Str("material"); m != "" {
					mat = m
				}
			}
			out.bodies = append(out.bodies, GroundBody{Layer: name, Style: style, Anchor: b.Anchor, Level: round3(b.Level),
				Spill: round3(b.Spill), Locked: b.Locked, Entry: b.Pin, Material: mat, Painted: math.Round(b.Painted),
				Area: math.Round(b.Area), MaxDepth: b.MaxDepth,
				Bounds: [4]float64{round3(b.Bounds[0]), round3(b.Bounds[1]), round3(b.Bounds[2]), round3(b.Bounds[3])}})
			wb := res.Water.Bodies[bi]
			out.water.Bodies = append(out.water.Bodies, wb)
		}
		// A later layer's water over an earlier one's: the higher surface.
		for k, b := range res.Water.Body {
			if b < 0 {
				continue
			}
			if out.water.Body[k] < 0 || res.Water.Level[k] > out.water.Level[k] {
				out.water.Body[k], out.water.Level[k] = base+b, res.Water.Level[k]
			}
		}
		if len(res.Bodies) > 0 {
			rc.Logf("water %s (%s): %d bodies, %d samples moved", name, style, len(res.Bodies), res.Changed)
		}
	}
	for i, m := range matched {
		if !m {
			out.orphans = append(out.orphans, i)
		}
	}
	if len(out.orphans) > 0 {
		rc.Logf("bodies entries holding no painted body: %v", out.orphans)
	}
	return out, nil
}

// groundWaterMeta is terrain.yaml's water section: the source's bodies,
// then the carved ones.
func groundWaterMeta(src *terrain.WaterMeta, ws *groundWater) *terrain.WaterMeta {
	if src == nil && !ws.wet() {
		return nil
	}
	wm := &terrain.WaterMeta{Mask: terrain.MaskWater}
	if src != nil {
		wm.Bodies = append(wm.Bodies, src.Bodies...)
	}
	for _, b := range ws.bodies {
		if b.Area <= 0 {
			continue
		}
		wm.Bodies = append(wm.Bodies, terrain.WaterBodyMeta{Kind: b.Style, Level: b.Level, Bounds: b.Bounds, Area: b.Area})
	}
	return wm
}

// writeBodiesFile writes water_bodies.yaml.
func writeBodiesFile(rc *procgen.RunContext, ws *groundWater) error {
	data, err := yaml.Marshal(GroundBodies{Bodies: ws.bodies, Orphans: ws.orphans})
	if err != nil {
		return err
	}
	return rc.WriteFile(BodiesFile, data)
}

// ReadGroundBodies reads a ground_edit stage's water_bodies.yaml from its
// output folder.
func ReadGroundBodies(path string) (*GroundBodies, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b GroundBodies
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", BodiesFile, err)
	}
	return &b, nil
}

// pondTileName is a water mesh's placement ID.
func pondTileName(ti, tj int, mat string) string { return fmt.Sprintf("pond_%d_%d_%s", ti, tj, mat) }

// writeGroundWater meshes, cooks and places the water per tile and
// material (file comment).
func writeGroundWater(rc *procgen.RunContext, g *terrain.Grid, tp tilePlan, sp procgen.Params, ws *groundWater, rivers map[int32]int32) error {
	if !ws.wet() {
		return nil
	}
	// The materials, and each one's share of the water.
	matOf := make([]string, len(ws.water.Bodies))
	var mats []string
	seen := map[string]bool{}
	for i := range matOf {
		matOf[i] = ws.bodies[i].Material
		if !seen[matOf[i]] {
			seen[matOf[i]] = true
			mats = append(mats, matOf[i])
		}
	}
	sort.Strings(mats)
	uv := float32(sp.Float("uv_scale"))
	no := false
	var placed []procgen.Placement
	var nTiles, nReused int
	for _, mat := range mats {
		sub := &terrain.WaterResult{Level: ws.water.Level, Body: make([]int32, len(ws.water.Body)), Bodies: ws.water.Bodies}
		for k, b := range ws.water.Body {
			sub.Body[k] = -1
			if b >= 0 && matOf[b] == mat {
				sub.Body[k] = b
			}
		}
		cells := waterCells(g, sub)
		// A river's ribbon owns the cells it covers (phase W3): the
		// pond's skirt under the bank gives way to it where it runs in.
		for c := range rivers {
			cells[c] = float32(math.NaN())
		}
		n := tp.count * tp.count
		out := make([]*procgen.Placement, n)
		reused := make([]bool, n)
		if err := rc.Parallel(n, func(k int) (ferr error) {
			ti, tj := k%tp.count, k/tp.count
			if !tileHasWater(cells, g, tp, ti, tj) {
				return nil
			}
			name := pondTileName(ti, tj, safeName(mat, "water"))
			key := groundWaterKey(rc, g, tp, cells, ti, tj, mat, uv)
			file := fmt.Sprintf("%s_%s.fnm", name, key[:10])
			pl := &procgen.Placement{ID: name, Model: rc.AssetPath(file), Position: tileCentre(g, tp, ti, tj), Collision: "none",
				Solid: &no, Chunk: procgen.ChunkAuto, Material: mat}
			if rc.ReuseTile(name, key, file) {
				out[k], reused[k] = pl, true
				return nil
			}
			b := waterTileMesh(g, tp, cells, ti, tj, uv)
			if b == nil {
				return nil
			}
			rc.BeginTile(name)
			defer func() {
				if ferr == nil {
					rc.SetTile(name, key)
				}
			}()
			ts := float32(tp.size)
			rect := [4]float32{-ts / 2, -ts / 2, ts / 2, ts / 2}
			if _, err := meshout.Cook(b.Mesh(), rc.Path(file), meshout.CookOptions{LockRect: &rect, LockBorder: true, CollisionMesh: true}); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			out[k] = pl
			return nil
		}); err != nil {
			return err
		}
		for k, pl := range out {
			if pl != nil {
				placed = append(placed, *pl)
				nTiles++
				if reused[k] {
					nReused++
				}
			}
		}
	}
	rc.Place(placed...)
	rc.Logf("water: %d bodies, %d surfaces in %d materials (%d reused)", len(ws.bodies), nTiles, len(mats), nReused)
	return nil
}

// tileHasWater reports whether any cell of tile (ti, tj) holds water.
func tileHasWater(cells []float32, g *terrain.Grid, tp tilePlan, ti, tj int) bool {
	for cj := tj * tp.per; cj < (tj+1)*tp.per; cj++ {
		for ci := ti * tp.per; ci < (ti+1)*tp.per; ci++ {
			if v := cells[cj*g.N+ci]; v == v {
				return true
			}
		}
	}
	return false
}

// groundWaterKey is a water mesh's piece key: its tile, material, UV scale
// and cells.
func groundWaterKey(rc *procgen.RunContext, g *terrain.Grid, tp tilePlan, cells []float32, ti, tj int, mat string, uv float32) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d,%d|%d|%d|%g|%g|%g|%g|%s|%g\n", ti, tj, tp.count, tp.per, tp.size, g.Cell, g.MinX, g.MinZ, mat, uv)
	var b [4]byte
	for cj := tj * tp.per; cj < (tj+1)*tp.per; cj++ {
		for ci := ti * tp.per; ci < (ti+1)*tp.per; ci++ {
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(cells[cj*g.N+ci]))
			h.Write(b[:])
		}
	}
	return groundPieceKey(rc, groundWaterVersion, fmt.Sprintf("%x", h.Sum(nil)))
}
