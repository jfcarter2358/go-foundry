package terrain

// path_lay.go — laying paths: the stations, the flatten and the masks
// (path.go, steps 1 and 2).
//
// STATIONS. A spec's points are resampled every half cell along the
// river code's Catmull-Rom curve (resampleCurve), so a path, like a river,
// is the smooth curve through its knots whatever their spacing, and its
// width is interpolated between them. Each station records the ground under
// it and whether water stands there (a pond's body, a river's wet sample).
//
// THE TARGET is the centreline ground smoothed along the length — a box
// filter of Smooth metres (at least the width), three passes, each a mean
// over the DRY stations in reach only. A path dipping into a ford keeps the
// banks' levels either side rather than being dragged down to the bed.
// With Steps, where the target climbs faster than MaxGrade it is pulled
// toward treads StepRise apart (blended in over half the grade again, so a
// gentle stretch is never stepped).
//
// NEAREST STATION, per grid sample within reach of a path: the distance to
// the polyline through the stations (the two segments either side of the
// nearest one, so the distance is exact to the curve's chords, not to the
// half-cell stations), its side, and the fractional station there. Each
// path keeps its own map; masks take the max over paths, the direction the
// strongest path's, the flatten the most strongly weighted path's target.
//
// THE CROSS-SECTION at distance d from the centreline, hw the half width:
//
//	surface  1 inside he = hw·(1 + Edge·n), easing out over a soft ring
//	         (0.08 hw + 0.12 m) either side of he; n is an fBm of EdgeSize
//	         metres in world space, so the edge is worn and ragged;
//	wear     1 inside 0.6 he, easing to 0 at he + Wear·(1 + 0.4 n'):
//	         grass thins toward the centreline and a soft band of thinner,
//	         shorter grass runs either side;
//	clear    1 inside he + 0.4 m, easing to 0 at he + 1.2 m;
//	dir      strength 1 inside he, easing to 0 where the wear ends;
//	flatten  weight 1 inside hw (no noise: the bed is regular), easing to
//	         0 at hw + Blend, times the fade toward water.
//
// each scaled by the style's peak (PathStyles). Under water — a wet sample
// of a pond or a river — surface, wear and clear are 0; in a river's
// channel (its bed share above a trace) nothing is flattened.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// pathRec is a sample's nearest point on one path.
type pathRec struct {
	d, u float64 // distance, and signed across (+ left of the tangent)
	f    float64 // fractional station
}

// LayPaths lays every spec on g (in place: the flatten) and returns the
// paths and their masks (file comment). water is the basins' standing
// water and rivers the carved rivers (either nil: none).
func LayPaths(g *Grid, specs []PathSpec, water *WaterResult, rivers *RiverResult) *PathResult {
	res := &PathResult{Masks: map[string][]float32{}}
	n := len(g.H)
	for _, name := range PathMaskNames {
		res.Masks[name] = make([]float32, n)
	}
	dx, dz := res.Masks[MaskPathDirX], res.Masks[MaskPathDirZ]
	for k := range dx {
		dx[k], dz[k] = 0.5, 0.5
	}
	wat := pathWater{water: water, rivers: rivers}
	for si := range specs {
		if p, ok := buildPath(g, &specs[si], si, wat); ok {
			res.Paths = append(res.Paths, p)
		}
	}
	if len(res.Paths) == 0 {
		return res
	}
	// Per sample: the strongest direction so far, and the flatten's
	// weight and target.
	dirS := map[int32]float64{}
	type flat struct{ w, t float64 }
	flats := map[int32]flat{}
	for pi := range res.Paths {
		p := &res.Paths[pi]
		sp := &specs[p.Spec]
		sty := PathStyles[p.Style]
		sh := sp.Shape
		eseed := noise.Hash64(sp.Seed, 0x65646765)
		wseed := noise.Hash64(sp.Seed, 0x77656172)
		surf := res.Masks[PathSurfaceMask(p.Style)]
		wear, clear := res.Masks[MaskPathWear], res.Masks[MaskPathClear]
		esz := math.Max(sh.EdgeSize, 0.1)
		for k32, rec := range pathNearest(g, p, sh) {
			k := int(k32)
			st := p.at(rec.f * p.Step)
			hw := st.Width / 2
			x, z := g.X(k%g.S()), g.Z(k/g.S())
			nE := noise.FBM2(eseed, x/esz, z/esz, 3, 0.5)
			nW := noise.FBM2(wseed, x/(2*esz), z/(2*esz), 2, 0.5)
			he := math.Max(hw*(1+sh.Edge*nE), 0.25*hw)
			soft := 0.08*hw + 0.12
			d := rec.d
			_, wet, channel := wat.at(k)
			outer := he + math.Max(sh.Wear*(1+0.4*nW), 0.2)
			if !wet {
				if v := float32(sty.Surface * (1 - smoothstep(he-soft, he+soft, d))); v > surf[k] {
					surf[k] = v
				}
				if v := float32(sty.Wear * (1 - smoothstep(0.6*he, outer, d))); v > wear[k] {
					wear[k] = v
				}
				if v := float32(sty.Clear * (1 - smoothstep(he+0.4, he+1.2, d))); v > clear[k] {
					clear[k] = v
				}
			}
			if s := 1 - smoothstep(he, outer, d); s > 0 && s > dirS[k32] {
				dirS[k32] = s
				dx[k] = float32(0.5 + 0.5*st.TX*s)
				dz[k] = float32(0.5 + 0.5*st.TZ*s)
			}
			if sh.Flatten && !wet && !channel {
				w := (1 - smoothstep(hw, hw+math.Max(sh.Blend, g.Cell*0.5), d)) * smoothstep(0.5, 2.5, st.WetDist)
				if w > 0 && w > flats[k32].w {
					flats[k32] = flat{w, st.Target}
				}
			}
		}
	}
	for k32, f := range flats {
		k := int(k32)
		h := float64(g.H[k])
		nh := h + (f.t-h)*f.w
		if math.Abs(nh-h) > 1e-4 {
			g.H[k] = float32(nh)
			res.Changed++
		}
	}
	return res
}

// buildPath resamples a spec into stations, marks the water under them and
// solves the target (file comment); false for a path under two stations.
func buildPath(g *Grid, sp *PathSpec, si int, wat pathWater) (Path, bool) {
	var pts [][2]float64
	var ws, ds []float64
	for i, q := range sp.Points {
		if n := len(pts); n > 0 && math.Hypot(q[0]-pts[n-1][0], q[1]-pts[n-1][1]) < 1e-6 {
			continue
		}
		pts = append(pts, q)
		w := 1.5
		if len(sp.Width) > 0 {
			w = math.Max(sp.Width[min(i, len(sp.Width)-1)], 0.2)
		}
		ws = append(ws, w)
		ds = append(ds, 0)
	}
	if len(pts) < 2 {
		return Path{}, false
	}
	step := g.Cell * 0.5
	rs := resampleCurve(pts, ws, ds, step)
	if len(rs) < 2 {
		return Path{}, false
	}
	headings(rs)
	p := Path{Name: sp.Name, Style: sp.Style, Spec: si, Step: step}
	if _, ok := PathStyles[p.Style]; !ok {
		p.Style = PathDirt
	}
	st := make([]PathStation, len(rs))
	for i, r := range rs {
		st[i] = PathStation{X: r.X, Z: r.Z, S: r.S, TX: r.TX, TZ: r.TZ, Width: r.Width, Level: math.NaN()}
		st[i].Ground = float64(g.Sample(r.X, r.Z))
		if k := sampleIndex(g, r.X, r.Z); k >= 0 {
			if lv, wet, _ := wat.at(k); wet {
				st[i].Wet, st[i].Level = true, lv
			}
		}
	}
	// WetDist both ways, and the crossings.
	last := math.Inf(-1)
	for i := range st {
		if st[i].Wet {
			last = st[i].S
		}
		st[i].WetDist = st[i].S - last
	}
	last = math.Inf(1)
	for i := len(st) - 1; i >= 0; i-- {
		if st[i].Wet {
			last = st[i].S
		}
		st[i].WetDist = math.Min(st[i].WetDist, last-st[i].S)
	}
	for i := range st {
		if st[i].Wet && (i == 0 || !st[i-1].Wet) {
			p.Crossings++
		}
	}
	pathTarget(st, sp.Shape, step)
	p.Stations = st
	p.Length = st[len(st)-1].S
	return p, true
}

// pathTarget sets each station's Target (file comment).
func pathTarget(st []PathStation, sh PathShape, step float64) {
	n := len(st)
	meanW := 0.0
	for _, s := range st {
		meanW += s.Width
	}
	meanW /= float64(n)
	r := max(int(math.Round(math.Max(sh.Smooth, meanW)/step/2)), 1)
	cur := make([]float64, n)
	dry := make([]bool, n)
	anyDry := false
	for i, s := range st {
		cur[i] = s.Ground
		dry[i] = !s.Wet
		anyDry = anyDry || dry[i]
	}
	if anyDry {
		for pass := 0; pass < 3; pass++ {
			next := make([]float64, n)
			for i := range cur {
				sum, w := 0.0, 0.0
				for j := max(i-r, 0); j <= min(i+r, n-1); j++ {
					if dry[j] {
						sum += cur[j]
						w++
					}
				}
				next[i] = cur[i]
				if w > 0 {
					next[i] = sum / w
				}
			}
			cur = next
		}
	}
	if sh.Steps && sh.StepRise > 0 && sh.MaxGrade > 0 {
		smooth := append([]float64(nil), cur...)
		for i := range cur {
			a, b := smooth[max(i-1, 0)], smooth[min(i+1, n-1)]
			grade := math.Abs(b-a) / (step * float64(min(i+1, n-1)-max(i-1, 0)))
			f := smoothstep(sh.MaxGrade, sh.MaxGrade*1.5, grade)
			if f > 0 {
				q := math.Round(cur[i]/sh.StepRise) * sh.StepRise
				cur[i] += (q - cur[i]) * f
			}
		}
	}
	for i := range st {
		st[i].Target = cur[i]
	}
}

// pathNearest is, for every grid sample within reach of p, its nearest
// point on p's polyline (file comment).
func pathNearest(g *Grid, p *Path, sh PathShape) map[int32]pathRec {
	out := map[int32]pathRec{}
	st := p.Stations
	s := g.S()
	for i := range st {
		hw := st[i].Width / 2
		reach := hw*(1+math.Abs(sh.Edge)) + math.Max(math.Max(sh.Blend, sh.Wear*1.4), 1.2) + g.Cell
		i0 := max(int(math.Floor((st[i].X-reach-g.MinX)/g.Cell)), 0)
		i1 := min(int(math.Ceil((st[i].X+reach-g.MinX)/g.Cell)), s-1)
		j0 := max(int(math.Floor((st[i].Z-reach-g.MinZ)/g.Cell)), 0)
		j1 := min(int(math.Ceil((st[i].Z+reach-g.MinZ)/g.Cell)), s-1)
		for j := j0; j <= j1; j++ {
			z := g.Z(j)
			for ii := i0; ii <= i1; ii++ {
				x := g.X(ii)
				if math.Hypot(x-st[i].X, z-st[i].Z) > reach {
					continue
				}
				best := pathRec{d: math.Inf(1)}
				for _, a := range [2]int{i - 1, i} {
					if a < 0 || a+1 >= len(st) {
						continue
					}
					ax, az := st[a].X, st[a].Z
					bx, bz := st[a+1].X, st[a+1].Z
					vx, vz := bx-ax, bz-az
					l2 := vx*vx + vz*vz
					t := 0.0
					if l2 > 1e-12 {
						t = math.Max(0, math.Min(1, ((x-ax)*vx+(z-az)*vz)/l2))
					}
					px, pz := ax+vx*t, az+vz*t
					d := math.Hypot(x-px, z-pz)
					if d < best.d {
						u := d
						if vx*(z-az)-vz*(x-ax) < 0 {
							u = -d
						}
						best = pathRec{d: d, u: u, f: float64(a) + t}
					}
				}
				if len(st) == 1 {
					best = pathRec{d: math.Hypot(x-st[0].X, z-st[0].Z)}
				}
				k := int32(j*s + ii)
				if old, ok := out[k]; !ok || best.d < old.d {
					out[k] = best
				}
			}
		}
	}
	return out
}
