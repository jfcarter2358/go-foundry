package terrain

// river_carve.go — cutting the rivers into the ground and the per-sample
// fields they leave (river.go, steps 4 and 5).
//
// NEAREST POINT. Each run's centreline segments are rasterised over the
// samples within their reach (half the width + floodplain + reach, or the
// skirt and point bar if wider): every sample keeps its nearest point on the
// run — the fractional station, the true distance and the signed distance
// across (+ left of the heading). A sample beyond a free end measures from
// that end, so a river's source is a rounded head — unless the river leaves
// or enters water there (a pond, a confluence), where the channel runs on
// open-ended into it rather than closing in a head that would stand as a
// dry sill between the river and the water. Sparse maps: only what a river
// reaches is touched.
//
// THE CUT, run by run (main stems first, as solved):
//
//   - within the banks (distance ≤ half-width): the bed is
//     surface − depth × (1 − u²), u the distance from the thalweg over the
//     distance from the thalweg to that side's bank — so the outer bank of
//     a bend (the thalweg near it) is steep and the inner one shelves (the
//     point bar). Lowered only.
//   - outside: ground above surface + bank height + 0.04 per metre over
//     the floodplain + BankSlope beyond it is cut down to that, faded out
//     over the last 40% of the reach.
//   - a levee where the ground beside the channel is lower than the water
//     (a river across a side slope): raised to 0.15 m over the surface for
//     a cell, falling away at BankSlope — never inside any river's banks.
//
// Samples under standing water (a pond the river runs into) are left as
// the basins made them. The bed is clamped above floor. Then each fall's
// NOTCH is cut (river_falls.go, phase W4).
//
// THE FIELDS. After every run is cut, each sample within its banks plus
// riverExtendCells (and the dressing's reach) takes the run whose banks it
// is deepest inside as its OWNER (a confluence: the main river's channel
// is the main river's). Its surface, current (the heading × the speed,
// slower toward the banks: × 0.45 + 0.55(1 − u²)), foam, UVs (across,
// along) and the bed and bar shares come from that run. A sample is WET
// when it is within the banks (and half a cell) and the carved ground is
// below the surface. Boulders then add foam round themselves and in a
// wake downstream, and take the bed height under them; a confluence adds
// foam round the tributary's mouth and a mixing line trailing down the
// main river (phase W4).

import (
	"math"

	"foundry/internal/procgen/noise"
)

// nearRec is a sample's nearest point on one run.
type nearRec struct {
	// f is the fractional station; dist the true distance; d the signed
	// distance across; head whether it is past a free end.
	f, dist, d float64
	head       bool
}

// runReach is how far from its centreline a run's carve and fields reach.
func runReach(r *riverRun, cell float64) []float64 {
	sh := r.spec.Shape
	out := make([]float64, len(r.st))
	for i, s := range r.st {
		hw := s.Width / 2
		out[i] = math.Max(math.Max(hw+sh.Floodplain+sh.Reach, hw+riverExtendCells*cell+cell), 1.7*hw) + cell
	}
	return out
}

// nearest rasterises a run's segments (file comment).
func nearest(g *Grid, r *riverRun) map[int32]nearRec {
	s := g.S()
	reach := runReach(r, g.Cell)
	out := map[int32]nearRec{}
	n := len(r.st)
	// An end in water is open (file comment).
	openStart := r.fromWater >= 0
	openEnd := r.intoWater >= 0 || r.into >= 0
	for i := 0; i+1 < n; i++ {
		a, b := r.st[i], r.st[i+1]
		R := math.Max(reach[i], reach[i+1])
		abx, abz := b.X-a.X, b.Z-a.Z
		l2 := abx*abx + abz*abz
		if l2 < 1e-12 {
			continue
		}
		l := math.Sqrt(l2)
		tx, tz := abx/l, abz/l
		i0 := max(int(math.Floor((math.Min(a.X, b.X)-R-g.MinX)/g.Cell)), 0)
		i1 := min(int(math.Ceil((math.Max(a.X, b.X)+R-g.MinX)/g.Cell)), s-1)
		j0 := max(int(math.Floor((math.Min(a.Z, b.Z)-R-g.MinZ)/g.Cell)), 0)
		j1 := min(int(math.Ceil((math.Max(a.Z, b.Z)+R-g.MinZ)/g.Cell)), s-1)
		for jj := j0; jj <= j1; jj++ {
			pz := g.Z(jj)
			for ii := i0; ii <= i1; ii++ {
				px := g.X(ii)
				rx, rz := px-a.X, pz-a.Z
				t := (rx*abx + rz*abz) / l2
				head := (i == 0 && t < 0 && !openStart) || (i == n-2 && t > 1 && !openEnd)
				// An open end runs straight on for a width and a half,
				// measured across, not round the end point.
				open := (i == 0 && t < 0 && openStart && -t*l <= 1.5*a.Width) ||
					(i == n-2 && t > 1 && openEnd && (t-1)*l <= 1.5*b.Width)
				t = math.Min(math.Max(t, 0), 1)
				qx, qz := a.X+abx*t, a.Z+abz*t
				dist := math.Hypot(px-qx, pz-qz)
				if open {
					dist = math.Abs(tx*rz - tz*rx)
				}
				if dist > R {
					continue
				}
				k := int32(jj*s + ii)
				if old, ok := out[k]; ok && old.dist <= dist {
					continue
				}
				out[k] = nearRec{f: float64(i) + t, dist: dist, d: tx*rz - tz*rx, head: head}
			}
		}
	}
	return out
}

// at is the run's station interpolated at fractional index f.
func (r *riverRun) at(f float64) RiverStation {
	n := len(r.st)
	i := min(max(int(math.Floor(f)), 0), n-1)
	if i >= n-1 {
		return r.st[n-1]
	}
	t := f - float64(i)
	a, b := r.st[i], r.st[i+1]
	m := func(x, y float64) float64 { return x + (y-x)*t }
	out := RiverStation{X: m(a.X, b.X), Z: m(a.Z, b.Z), S: m(a.S, b.S), Ground: m(a.Ground, b.Ground),
		Surface: m(a.Surface, b.Surface), Width: m(a.Width, b.Width), Depth: m(a.Depth, b.Depth), Offset: m(a.Offset, b.Offset),
		Bar: m(a.Bar, b.Bar), Riffle: m(a.Riffle, b.Riffle), Speed: m(a.Speed, b.Speed), Foam: m(a.Foam, b.Foam)}
	tx, tz := m(a.TX, b.TX), m(a.TZ, b.TZ)
	if l := math.Hypot(tx, tz); l > 1e-9 {
		out.TX, out.TZ = tx/l, tz/l
	}
	// A fall is a step, not a ramp: the surface is the lip's to the lip
	// station and the foot's after it, which the stations already hold.
	return out
}

// section is where a sample sits in a run's cross-section: its distance
// from the thalweg over the distance to that side's bank (u, 0..1+), and
// the half-width.
func section(s *RiverStation, rec *nearRec) (u, hw float64) {
	hw = math.Max(s.Width/2, 1e-3)
	if rec.head {
		return rec.dist / hw, hw
	}
	side := rec.d - s.Offset
	hs := hw - s.Offset
	if side < 0 {
		hs = hw + s.Offset
	}
	return math.Abs(side) / math.Max(hs, 1e-3), hw
}

// carveRuns cuts every run and fills res's samples (file comment).
func carveRuns(g *Grid, runs []*riverRun, water *WaterResult, floor float64, res *RiverResult) {
	cell := g.Cell
	recs := make([]map[int32]nearRec, len(runs))
	for ri, r := range runs {
		recs[ri] = nearest(g, r)
	}
	underWater := func(k int32) bool { return water != nil && water.Body[k] >= 0 }
	// Every river's banks, so no levee rises inside another's channel.
	inChannel := map[int32]bool{}
	for ri, r := range runs {
		for k, rec := range recs[ri] {
			s := r.at(rec.f)
			if rec.dist <= s.Width/2+0.5*cell {
				inChannel[k] = true
			}
		}
	}
	changed := map[int32]bool{}
	fl := float32(floor)
	for ri, r := range runs {
		sh := r.spec.Shape
		for k, rec := range recs[ri] {
			if underWater(k) {
				continue
			}
			s := r.at(rec.f)
			u, hw := section(&s, &rec)
			old := g.H[k]
			nv := old
			if rec.dist <= hw {
				bed := float32(s.Surface - s.Depth*(1-math.Min(u*u, 1)))
				nv = min(nv, bed)
			} else {
				eb := rec.dist - hw
				fp := sh.Floodplain
				cp := float32(s.Surface + sh.BankHeight + 0.04*math.Min(eb, fp) + sh.BankSlope*math.Max(eb-fp, 0))
				reach := math.Max(fp+sh.Reach, cell)
				fade := float32(1 - noise.SmoothStep(0.6*reach, reach, eb))
				if nv > cp {
					nv -= (nv - cp) * fade
				}
				if !inChannel[k] {
					lev := float32(s.Surface + 0.15 - sh.BankSlope*math.Max(eb-cell, 0))
					if nv < lev {
						nv += (lev - nv) * fade
					}
				}
			}
			if nv < fl {
				nv = fl
			}
			if nv != old {
				g.H[k] = nv
				changed[k] = true
			}
		}
	}
	// The notch each fall drops through (river_falls.go, phase W4).
	var falls []Waterfall
	for _, r := range runs {
		falls = append(falls, r.falls...)
	}
	notchFalls(g, falls, water, fl, changed)
	res.Changed = len(changed)

	// The fields.
	score := map[int32]float64{}
	for ri, r := range runs {
		for k, rec := range recs[ri] {
			s := r.at(rec.f)
			u, hw := section(&s, &rec)
			sc := rec.dist - hw
			if old, ok := score[k]; ok && old <= sc {
				continue
			}
			barReach := 1.6 * hw
			bedFade := hw + math.Max(1, 0.25*s.Width)
			skirt := hw + riverExtendCells*cell
			if rec.dist > math.Max(math.Max(skirt, bedFade), barReach) {
				continue
			}
			score[k] = sc
			smp := RiverSample{Owner: int32(ri), Surface: float32(math.NaN())}
			if underWater(k) {
				// A pond's own water and bed: the pond's (an open end
				// reaches into it).
				res.Samples[k] = smp
				continue
			}
			if rec.dist <= skirt {
				smp.Surface = float32(s.Surface)
				lat := 0.45 + 0.55*(1-math.Min(u*u, 1))
				smp.FlowX, smp.FlowZ = float32(s.TX*s.Speed*lat), float32(s.TZ*s.Speed*lat)
				smp.Foam = float32(s.Foam * math.Max(lat, 0.6))
				smp.U, smp.V = float32(rec.d), float32(s.S)
				smp.Wet = rec.dist <= hw+0.5*cell && g.H[k] < smp.Surface && !underWater(k)
			}
			smp.Bed = float32(1 - noise.SmoothStep(hw, bedFade, rec.dist))
			if s.Offset != 0 && !rec.head && (rec.d > 0) != (s.Offset > 0) {
				a := math.Abs(rec.d)
				smp.Bar = float32(s.Bar * noise.SmoothStep(0.15*hw, 0.6*hw, a) * (1 - noise.SmoothStep(hw, barReach, a)))
			}
			res.Samples[k] = smp
		}
	}
	// Confluence foam: round each tributary's mouth, and (phase W4) a
	// mixing line trailing down the main river from it, fading over a few
	// of its widths — the turbulence where two currents meet. The water
	// shader reads foam as white water and ripples it harder (water.glsl).
	for _, r := range runs {
		if r.into < 0 {
			continue
		}
		last := r.st[len(r.st)-1]
		rad := math.Max(last.Width, 1.5)
		splash(g, res, last.X, last.Z, rad, 0.45, 0, 0, 0)
		m := runs[r.into]
		ms := m.st[min(max(r.intoAt, 0), len(m.st)-1)]
		mixingLine(g, res, last.X, last.Z, ms.TX, ms.TZ, last.Width, confluenceWake*math.Max(ms.Width, last.Width))
	}
	// Boulders: the bed under them, foam round them and in their wake.
	for _, r := range runs {
		for bi := range r.boulders {
			b := &r.boulders[bi]
			b.Y = float64(g.Sample(b.X, b.Z))
			b.X, b.Z, b.Size, b.Y = round2(b.X), round2(b.Z), round2(b.Size), round3(b.Y)
			// The heading at the boulder: its nearest station's.
			bestD, tx, tz := math.Inf(1), 1.0, 0.0
			for _, s := range r.st {
				if d := math.Hypot(s.X-b.X, s.Z-b.Z); d < bestD {
					bestD, tx, tz = d, s.TX, s.TZ
				}
			}
			splash(g, res, b.X, b.Z, 0.7*b.Size, 0.9, tx, tz, 4*b.Size)
		}
	}
}

// confluenceWake is how many of the wider river's widths a confluence's
// mixing line trails downstream.
const confluenceWake = 5.0

// mixingLine adds a confluence's mixing line: from the tributary's mouth
// (x, z) down the main river's heading (tx, tz) for length metres, a band
// starting half the tributary's width across and widening, its foam 0.7 at
// the mouth fading to nothing — a trail, not a disc at the mouth.
func mixingLine(g *Grid, res *RiverResult, x, z, tx, tz, width, length float64) {
	s := g.S()
	R := length + width
	i0 := max(int(math.Floor((x-R-g.MinX)/g.Cell)), 0)
	i1 := min(int(math.Ceil((x+R-g.MinX)/g.Cell)), s-1)
	j0 := max(int(math.Floor((z-R-g.MinZ)/g.Cell)), 0)
	j1 := min(int(math.Ceil((z+R-g.MinZ)/g.Cell)), s-1)
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			k := int32(j*s + i)
			smp, ok := res.Samples[k]
			if !ok || smp.Surface != smp.Surface {
				continue
			}
			dx, dz := g.X(i)-x, g.Z(j)-z
			along := dx*tx + dz*tz
			if along <= 0 || along >= length {
				continue
			}
			across := math.Abs(dz*tx - dx*tz)
			w := 0.5*width + 0.15*along
			if across >= w {
				continue
			}
			v := 0.7 * (1 - along/length) * (1 - noise.SmoothStep(0.4*w, w, across))
			if v > float64(smp.Foam) {
				smp.Foam = float32(v)
				res.Samples[k] = smp
			}
		}
	}
}

// splash adds foam round (x, z) within rad (at strength f), and in a wake
// of length wake downstream along (tx, tz), to every sample with a surface.
func splash(g *Grid, res *RiverResult, x, z, rad, f, tx, tz, wake float64) {
	s := g.S()
	R := rad + wake
	i0 := max(int(math.Floor((x-R-g.MinX)/g.Cell)), 0)
	i1 := min(int(math.Ceil((x+R-g.MinX)/g.Cell)), s-1)
	j0 := max(int(math.Floor((z-R-g.MinZ)/g.Cell)), 0)
	j1 := min(int(math.Ceil((z+R-g.MinZ)/g.Cell)), s-1)
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			k := int32(j*s + i)
			smp, ok := res.Samples[k]
			if !ok || smp.Surface != smp.Surface {
				continue
			}
			dx, dz := g.X(i)-x, g.Z(j)-z
			v := 0.0
			if d := math.Hypot(dx, dz); d < rad*1.3 {
				v = f * (1 - noise.SmoothStep(rad*0.6, rad*1.3, d))
			}
			if wake > 0 {
				along := dx*tx + dz*tz
				across := math.Abs(dz*tx - dx*tz)
				if along > 0 && along < wake {
					w := rad * (1 + along/wake)
					if across < w {
						v = math.Max(v, 0.65*f*(1-along/wake)*(1-across/w))
					}
				}
			}
			if v > float64(smp.Foam) {
				smp.Foam = float32(math.Min(v, 1))
				res.Samples[k] = smp
			}
		}
	}
}
