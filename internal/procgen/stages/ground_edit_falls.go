package stages

// ground_edit_falls.go — ground_edit's waterfalls (docs/terrain-paint/
// PHASE_W4.md): the falling sheet over every fall the rivers made, and the
// mist at its foot.
//
// W3 found the falls (terrain.Waterfall: lip, foot, heading, width, now the
// current over the lip) and carved them as a step in the surface with a
// plunge pool; the ribbon over a fall was just the cells pitched from the
// lip's level to the foot's. W4 adds what makes a fall read as one:
//
//   - THE SHEET: terrain.Waterfall.Curtain — a grid from a short level
//     lead-in over the lip down a ballistic curve to a little below the
//     pool — meshed, cooked and placed as falls_<river>_<n>, wearing
//     `falls_material` (water_falls: the water effect with `falls` set,
//     which water.glsl shades as falling water: streaks scrolling down the
//     sheet, aerated white at the lip and foot, translucent). The vertex
//     colour carries the sheet's own data, not a flow map: red the
//     aeration, green the edge (0 at the sides, so they fray), blue the
//     progress down the fall; alpha 255 (fragment.glsl reads alpha as
//     cavity). UV is across (m) × down the sheet (m), which the streaks
//     scroll along. Collision none, not solid: the player walks through
//     a waterfall. Each is keyed by its geometry (a changed fall is a new
//     model path; an unchanged one is reused, as the ribbons are).
//   - THE MIST: `falls_mist` (waterfall_mist.fpar, shipped in
//     resources/particles) placed as emitters across the foot where the
//     sheet meets the pool — one per three metres of the sheet's width —
//     their rate scaled by the drop (a small cascade step breathes, a tall
//     fall billows): mist_<river>_<n>_<k>, spliced with the generator's
//     prefix like every placement (procgen.Emitter).
//   - FOAM in the plunge pool: the ribbon's vertex foam already rises at a
//     fall's lip and in its pool (terrain/river_hydro.go); W4 adds rings
//     round where the sheet lands (fallsFoam), so the pool churns white at
//     the foot and calms outward.
//
// The notch the sheet drops through is cut by the carve itself
// (terrain/river_falls.go), so the ground is already clear of the sheet.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	"foundry/internal/model"
	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/terrain"

	"github.com/go-gl/mathgl/mgl32"
)

// groundFallsVersion changes when a sheet changes for the same fall.
const groundFallsVersion = "ground-falls-1"

// fallsMistSpacing is the sheet width each mist emitter covers (m).
const fallsMistSpacing = 3.0

// fallsNames is each fall's placement ID stem, falls_<river>_<n> (n from 1
// along each river, in the order the result lists them).
func fallsNames(falls []terrain.Waterfall) []string {
	counts := map[string]int{}
	out := make([]string, len(falls))
	for i, f := range falls {
		counts[f.River]++
		out[i] = fmt.Sprintf("falls_%s_%d", safeName(f.River, "river"), counts[f.River])
	}
	return out
}

// fallsFoam adds the plunge pools' foam rings to the samples round where
// each fall's sheet lands (file comment): before the ribbons are meshed, so
// their vertex colours carry it.
func fallsFoam(g *terrain.Grid, rv *groundRivers) {
	if !rv.any() {
		return
	}
	s := g.S()
	for _, f := range rv.res.Falls {
		c := f.Curtain(g.Cell)
		dl := math.Hypot(f.DirX, f.DirZ)
		if dl < 1e-9 {
			continue
		}
		tx, tz := f.DirX/dl, f.DirZ/dl
		cx, cz := f.X+tx*c.Out, f.Z+tz*c.Out
		// The churn reaches about the sheet's half-width plus a share of
		// the drop round the landing, rings rippling outward in it.
		drop := f.Lip - f.Foot
		R := c.FootWidth/2 + noise.Clamp(0.6*drop, 0.5, 4)
		i0 := max(int(math.Floor((cx-R-g.MinX)/g.Cell)), 0)
		i1 := min(int(math.Ceil((cx+R-g.MinX)/g.Cell)), s-1)
		j0 := max(int(math.Floor((cz-R-g.MinZ)/g.Cell)), 0)
		j1 := min(int(math.Ceil((cz+R-g.MinZ)/g.Cell)), s-1)
		for j := j0; j <= j1; j++ {
			for i := i0; i <= i1; i++ {
				k := int32(j*s + i)
				smp, ok := rv.res.Samples[k]
				if !ok || smp.Surface != smp.Surface {
					continue
				}
				dx, dz := g.X(i)-cx, g.Z(j)-cz
				// Distance from the landing LINE (the sheet's width), not
				// its centre: a wide fall foams along its whole foot.
				along := dx*tx + dz*tz
				across := math.Max(math.Abs(dz*tx-dx*tz)-c.FootWidth/2, 0)
				d := math.Hypot(along, across)
				if d > R {
					continue
				}
				fr := d / R
				ring := 0.75 + 0.25*math.Cos(fr*3*2*math.Pi)
				v := float32(noise.Clamp(1.3*(1-noise.SmoothStep(0.1, 1, fr))*ring, 0, 1))
				if v > smp.Foam {
					smp.Foam = v
					rv.res.Samples[k] = smp
				}
			}
		}
	}
}

// fallsVertexColor is a sheet vertex's colour (file comment).
func fallsVertexColor(aer, edge, prog float64) [4]uint8 {
	enc := func(v float64) uint8 { return uint8(math.Round(noise.Clamp(v, 0, 1) * 255)) }
	return [4]uint8{enc(aer), enc(edge), enc(prog), 255}
}

// fallsMesh is a sheet as a mesh, in coordinates relative to org.
func fallsMesh(c *terrain.FallsCurtain, org [3]float64) *model.ImportMesh {
	var b meshout.Builder
	b.Slot("water", "")
	at := func(r, k int) int { return r*c.Cols + k }
	pos := func(i int) mgl32.Vec3 {
		p := c.P[i]
		return mgl32.Vec3{float32(p[0] - org[0]), float32(p[1] - org[1]), float32(p[2] - org[2])}
	}
	colors := make([][4]uint8, 0, len(c.P))
	for r := 0; r < c.Rows; r++ {
		for k := 0; k < c.Cols; k++ {
			i := at(r, k)
			// The sheet's normal: across × down, facing out over the pool.
			r0, r1 := max(r-1, 0), min(r+1, c.Rows-1)
			k0, k1 := max(k-1, 0), min(k+1, c.Cols-1)
			down := pos(at(r1, k)).Sub(pos(at(r0, k)))
			across := pos(at(r, k1)).Sub(pos(at(r, k0)))
			n := across.Cross(down)
			if n.Len() < 1e-9 {
				n = mgl32.Vec3{0, 1, 0}
			} else {
				n = n.Normalize()
			}
			b.Vertex(pos(i), n, mgl32.Vec2{float32(c.U[i]), float32(c.V[i])})
			colors = append(colors, fallsVertexColor(c.Aeration[i], c.Edge[i], c.Progress[i]))
		}
	}
	for r := 0; r+1 < c.Rows; r++ {
		for k := 0; k+1 < c.Cols; k++ {
			a, d := uint32(at(r, k)), uint32(at(r, k+1))
			e, f := uint32(at(r+1, k+1)), uint32(at(r+1, k))
			// Front faces out over the pool (the normal's side).
			b.Tri(a, d, e)
			b.Tri(a, e, f)
		}
	}
	m := b.Mesh()
	m.Colors = colors
	return m
}

// fallsKey is a sheet's piece key: its material and every vertex.
func fallsKey(rc *procgen.RunContext, c *terrain.FallsCurtain, mat string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d,%d|%s\n", c.Rows, c.Cols, mat)
	var b [8]byte
	for i, p := range c.P {
		for _, v := range [...]float64{p[0], p[1], p[2], c.U[i], c.V[i], c.Aeration[i], c.Edge[i], c.Progress[i]} {
			binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
			h.Write(b[:])
		}
	}
	return groundPieceKey(rc, groundFallsVersion, fmt.Sprintf("%x", h.Sum(nil)))
}

// writeGroundFalls meshes, cooks and places every fall's sheet, and places
// its mist (file comment).
func writeGroundFalls(rc *procgen.RunContext, g *terrain.Grid, rv *groundRivers) error {
	if !rv.any() || len(rv.res.Falls) == 0 {
		return nil
	}
	falls := rv.res.Falls
	names := fallsNames(falls)
	mat := rc.Params.Str("falls_material")
	mist := rc.Params.Str("falls_mist")
	no := false
	out := make([]*procgen.Placement, len(falls))
	reused := make([]bool, len(falls))
	if err := rc.Parallel(len(falls), func(x int) (ferr error) {
		f := falls[x]
		c := f.Curtain(g.Cell)
		name := names[x]
		key := fallsKey(rc, &c, mat)
		file := fmt.Sprintf("%s_%s.fnm", name, key[:10])
		org := [3]float64{f.X, f.Foot, f.Z}
		pl := &procgen.Placement{ID: name, Model: rc.AssetPath(file),
			Position: procgen.Point3{X: float32(org[0]), Y: float32(org[1]), Z: float32(org[2])}, Collision: "none",
			Solid: &no, Chunk: procgen.ChunkAuto, Material: mat}
		if rc.ReuseTile(name, key, file) {
			out[x], reused[x] = pl, true
			return nil
		}
		rc.BeginTile(name)
		defer func() {
			if ferr == nil {
				rc.SetTile(name, key)
			}
		}()
		if _, err := meshout.Cook(fallsMesh(&c, org), rc.Path(file), meshout.CookOptions{LockBorder: true}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		out[x] = pl
		return nil
	}); err != nil {
		return err
	}
	var placed []procgen.Placement
	nr := 0
	for x, pl := range out {
		placed = append(placed, *pl)
		if reused[x] {
			nr++
		}
	}
	rc.Place(placed...)
	ems := fallsMist(g, falls, names, mist)
	rc.PlaceEmitter(ems...)
	rc.Logf("waterfalls: %d sheets (%d reused), %d mist emitters", len(placed), nr, len(ems))
	return nil
}

// fallsMist is the mist emitters across each fall's foot (file comment):
// none when effect is empty.
func fallsMist(g *terrain.Grid, falls []terrain.Waterfall, names []string, effect string) []procgen.Emitter {
	if effect == "" {
		return nil
	}
	var out []procgen.Emitter
	for fi, f := range falls {
		c := f.Curtain(g.Cell)
		dl := math.Hypot(f.DirX, f.DirZ)
		tx, tz := 1.0, 0.0
		if dl > 1e-9 {
			tx, tz = f.DirX/dl, f.DirZ/dl
		}
		n := max(int(math.Round(c.FootWidth/fallsMistSpacing)), 1)
		drop := f.Lip - f.Foot
		rate := noise.Clamp(0.3+0.25*drop, 0.3, 2.5)
		if f.Cascade {
			rate *= 0.6
		}
		yaw := math.Atan2(tx, tz)
		for k := 0; k < n; k++ {
			a := (float64(k)+0.5)/float64(n)*c.FootWidth - c.FootWidth/2
			px := f.X + tx*(c.Out+0.3) - tz*a
			pz := f.Z + tz*(c.Out+0.3) + tx*a
			out = append(out, procgen.Emitter{ID: fmt.Sprintf("mist_%s_%d", names[fi][len("falls_"):], k+1), Effect: effect,
				Position:  procgen.Point3{X: float32(round2(px)), Y: float32(round2(f.Foot + 0.15)), Z: float32(round2(pz))},
				Rotation:  procgen.Point3{Y: float32(round3(yaw))},
				RateScale: float32(round2(rate))})
		}
	}
	return out
}
