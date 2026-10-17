package stages

// ground_edit_path_stones.go — the paths' stones as placements
// (docs/terrain-paint/PHASE_P4.md; the layout is procgen/terrain
// path_stones.go).
//
// Each stone the layout made (terrain.PathStone: centre, yaw along the
// path, length along, width across, thickness) becomes one of a rocks
// stage's variants — the path's own `rocks` for its flagstones or stepping
// stones, else the stage's `path_rocks`, which also gives every ford its
// stones — picked by a hash of its position: for a flagstone or a stepping
// stone from the flatter half of the variants (by height over footprint),
// for a ford stone from all of them; half are turned end for end, so one
// scan reads as many stones. Then it is:
//
//   - SCALED to the stone: a placement's scale is per axis, so the rock's
//     own footprint is stretched to the stone's width (model X, across) and
//     length (model Z, along) and its height to the thickness — any scanned
//     rock becomes a flagstone of the size the row wants;
//   - GROUND-ALIGNED: the ground's plane under the stone, from the final
//     heights at its four edge midpoints, tilts it (a flagstone on a
//     slope lies on the slope);
//   - SUNK: its base is put below the ground at every corner of its
//     footprint by half its thickness (a stepping stone a little less), so
//     no corner floats and its top stands a few centimetres proud; a rough
//     spot buries one side more, as a bedded stone is. A scanned rock is
//     domed, so the outline it shows at the ground is smaller than its
//     footprint: each is scaled 12% past its layout rectangle (stoneGrow)
//     so the joints between flagstones stay joints. A ford
//     stone sits 10 cm into the bed and is made tall enough that its top
//     stands 7 cm above the water there.
//
// Flagstones are collision none (the flattened ground under them is what a
// walker stands on: no stepping over joints); stepping and ford stones
// collide (auto: as the level picks for the variant), so one can stand on
// a ford stone above the water. IDs are stone_<path>_<nnnn> in layout order.

import (
	"fmt"
	"math"
	"sort"

	"foundry/internal/procgen"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/place"
	"foundry/internal/procgen/terrain"
)

// How much of a stone's thickness stands proud of the ground, a ford
// stone's bed sink (m), and how much wider than its layout rectangle a
// stone's rock is scaled: a scanned rock is domed, so the outline it shows
// at the ground is smaller than its footprint, and the joints would gape.
const (
	flagProud = 0.5
	stepProud = 0.55
	fordSink  = 0.1
	stoneGrow = 1.12
)

// placePathStones places the paths' stones (file comment) and returns why
// none were placed when the paths wanted some ("" otherwise).
func placePathStones(rc *procgen.RunContext, g *terrain.Grid, gp *groundPaths) (string, error) {
	if !gp.any() {
		return "", nil
	}
	stones := terrain.PathStones(g, gp.specs, gp.res)
	if len(stones) == 0 {
		return "", nil
	}
	// Each path's rocks for its flagstones or stepping stones (its own, or
	// the stage's), the stage's for its fords (or its own without them).
	stageRef := rc.Params.Str("path_rocks")
	own := map[string]string{}
	for i, sp := range gp.specs {
		own[sp.Name] = rc.Params.Items("paths")[gp.entries[i]].Str("rocks")
	}
	refFor := func(st *terrain.PathStone) string {
		o := own[st.Path]
		if o != "" && (st.Kind != terrain.StonesFord || stageRef == "") {
			return o
		}
		return stageRef
	}
	type rockSet struct {
		vs   []place.Variant
		flat []int
		mat  string
	}
	sets := map[string]*rockSet{}
	rocksOf := func(ref string) (*rockSet, error) {
		if rs, ok := sets[ref]; ok {
			return rs, nil
		}
		vs, art, err := runVariants(rc, ref)
		if err != nil {
			return nil, err
		}
		rs := &rockSet{vs: vs}
		if art != nil && art.Manifest != nil {
			rs.mat = art.Manifest.Material
		}
		// The flatter half of the variants, for flagstones and stepping
		// stones.
		rs.flat = make([]int, len(vs))
		for i := range rs.flat {
			rs.flat[i] = i
		}
		aspect := func(v *place.Variant) float64 {
			return v.Height / math.Max(math.Max(v.Extent[0], v.Extent[2]), 1e-3)
		}
		sort.SliceStable(rs.flat, func(a, b int) bool { return aspect(&vs[rs.flat[a]]) < aspect(&vs[rs.flat[b]]) })
		rs.flat = rs.flat[:max(1, (len(rs.flat)+1)/2)]
		sets[ref] = rs
		return rs, nil
	}
	counts := map[string]int{}
	pls := make([]procgen.Placement, 0, len(stones))
	missing := 0
	for _, st := range stones {
		ref := refFor(&st)
		if ref == "" {
			missing++
			continue
		}
		rs, err := rocksOf(ref)
		if err != nil {
			return "", err
		}
		h := noise.Hash64(rc.Seed, math.Float64bits(st.X), math.Float64bits(st.Z))
		vi := rs.flat[int(h%uint64(len(rs.flat)))]
		if st.Kind == terrain.StonesFord {
			vi = int(h % uint64(len(rs.vs)))
		}
		// Half the stones turned end for end: one scan reads as many.
		if h>>40&1 == 1 {
			st.Yaw += math.Pi
		}
		pl, ok := seatPathStone(g, st, &rs.vs[vi])
		if !ok {
			continue
		}
		counts[st.Path]++
		pl.ID = fmt.Sprintf("stone_%s_%04d", st.Path, counts[st.Path])
		pl.Material = rs.mat
		pls = append(pls, pl)
		// Its footprint for the placement stages (ground_obstacles.go).
		gp.obstacles = append(gp.obstacles, obstacleDisc(st.X, st.Z, 0.25*(st.Length+st.Width)))
		if gp.stones[st.Path] == nil {
			gp.stones[st.Path] = map[string]int{}
		}
		gp.stones[st.Path][st.Kind]++
	}
	rc.Place(pls...)
	refs := make([]string, 0, len(sets))
	for r := range sets {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	rc.Logf("paths: %d stones placed from %v", len(pls), refs)
	if missing > 0 {
		note := fmt.Sprintf("%d stones laid out but not placed: choose Path rocks (a rocks or polyhaven_rocks stage)", missing)
		rc.Logf("paths: %s", note)
		return note, nil
	}
	return "", nil
}

// seatPathStone is stone st as variant v, scaled, aligned and sunk on g
// (file comment). False for a degenerate variant.
func seatPathStone(g *terrain.Grid, st terrain.PathStone, v *place.Variant) (procgen.Placement, bool) {
	ex, ez := v.Extent[0], v.Extent[2]
	if ex <= 1e-4 || ez <= 1e-4 || v.Height <= 1e-4 {
		return procgen.Placement{}, false
	}
	// The footprint's centre in the rock's frame (Min/Max when known).
	cx, cz := 0.0, 0.0
	if v.Max != v.Min {
		cx, cz = (v.Min[0]+v.Max[0])/2, (v.Min[2]+v.Max[2])/2
		ex, ez = v.Max[0]-v.Min[0], v.Max[2]-v.Min[2]
	}
	sx, sz := stoneGrow*st.Width/ex, stoneGrow*st.Length/ez
	// The ground's plane under the stone.
	sn, cs := math.Sincos(st.Yaw)
	along := [2]float64{sn, cs}
	across := [2]float64{cs, -sn}
	hAt := func(x, z float64) float64 { return float64(g.Sample(x, z)) }
	hl, hw := st.Length/2, st.Width/2
	gA := (hAt(st.X+along[0]*hl, st.Z+along[1]*hl) - hAt(st.X-along[0]*hl, st.Z-along[1]*hl)) / st.Length
	gC := (hAt(st.X+across[0]*hw, st.Z+across[1]*hw) - hAt(st.X-across[0]*hw, st.Z-across[1]*hw)) / st.Width
	ta := [3]float64{along[0], gA, along[1]}
	tc := [3]float64{across[0], gC, across[1]}
	n := [3]float64{tc[1]*ta[2] - tc[2]*ta[1], tc[2]*ta[0] - tc[0]*ta[2], tc[0]*ta[1] - tc[1]*ta[0]}
	if n[1] < 0 {
		n = [3]float64{-n[0], -n[1], -n[2]}
	}
	l := math.Sqrt(n[0]*n[0] + n[1]*n[1] + n[2]*n[2])
	n = [3]float64{n[0] / l, n[1] / l, n[2] / l}
	rot := place.FromTo([3]float64{0, 1, 0}, n).Mul(place.RotY(st.Yaw))
	// Where the footprint's centre and corners land (scaled, turned).
	rs := func(x, z float64) [3]float64 { return rot.Apply([3]float64{x * sx, 0, z * sz}) }
	oc := rs(cx, cz)
	lowest := math.Inf(1)
	for _, c := range [][2]float64{{0, 0}, {-1, -1}, {1, -1}, {-1, 1}, {1, 1}, {0, -1}, {0, 1}, {-1, 0}, {1, 0}} {
		o := rs(cx+c[0]*ex/2, cz+c[1]*ez/2)
		gx, gz := st.X+o[0]-oc[0], st.Z+o[2]-oc[2]
		lowest = math.Min(lowest, hAt(gx, gz)-o[1])
	}
	var y, thick float64
	switch st.Kind {
	case terrain.StonesFord:
		y = lowest - fordSink
		top := st.Level + terrain.FordProud
		if math.IsNaN(st.Level) {
			top = hAt(st.X, st.Z) + stepProud*st.Thick
		}
		thick = math.Max(top-y, st.Thick)
	case terrain.StonesStep:
		thick = st.Thick
		y = lowest - thick*(1-stepProud)
	default:
		thick = st.Thick
		y = lowest - thick*(1-flagProud)
	}
	sy := thick / v.Height
	pos := [3]float64{st.X - oc[0], y, st.Z - oc[2]}
	e := rot.EulerYXZ()
	pl := procgen.Placement{
		Model:    v.Model,
		Position: procgen.Point3{X: float32(pos[0]), Y: float32(pos[1]), Z: float32(pos[2])},
		Rotation: procgen.Point3{X: float32(e[0]), Y: float32(e[1]), Z: float32(e[2])},
		Scale:    &procgen.Point3{X: float32(sx), Y: float32(sy), Z: float32(sz)},
		Chunk:    procgen.ChunkAuto,
	}
	if st.Kind == terrain.StonesFlag {
		pl.Collision = "none"
	}
	return pl, true
}
