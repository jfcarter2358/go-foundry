package terrain

// edge.go — height compatibility between regions (docs/regions/PRESETS.md,
// "Engine work", item 5).
//
// Two regions whose providers stand at very different heights meet across
// their transition band as a ramp: the blend averages a mountain range and a
// plain into an escarpment of slope ~ΔH·1.5/band (docs/regions/PHASE_2.md,
// "Escarpments"). Real mountains rise OUT of the country round them: the
// foothills start at the plain's level and the range climbs inside its own
// ground. Edge says so for a provider:
//
//	h' = Level + fade · (h − Level),   fade = smoothstep(0, Rise, d)
//
// where d is the distance (metres, on the union grid) from the sample to the
// nearest sample this region does NOT own outright — a band sample, or one
// another region owns. So across the band, where its neighbours have a
// share, the region stands at Level (the shared base its neighbours are
// authored to meet), and its relief grows back over Rise metres inside its
// own footprint. An edge of the region that meets no other region (the
// union's outer edge) is not a border, so nothing fades there and the far
// backdrop continues the full relief.
//
// It runs on the provider's evaluated base shape (mesas, landforms, all of
// it) before its canyons are carved and before anything blends or erodes,
// so the erosion over the union sees one continuous surface and wears the
// rise into foothills.
//
// Off (Rise 0, the default) it never runs. A union of one provider owns
// every sample, so d is infinite, fade is exactly 1 and the sample is left
// as it was: the one-region identity holds whatever Edge says.

import "math"

// Edge is a provider's fade toward its neighbours' level.
type Edge struct {
	// Level is the height the region stands at where it meets another, and
	// Rise the distance (metres) inside its own ground over which its relief
	// grows back from it. Rise 0 is off.
	Level, Rise float64
}

// fadeEdges applies provider r's Edge to its field pf (see the file
// comment), reading the union model m for which samples it owns.
func fadeEdges(e Edge, r int, pf *providerField, g *Grid, m *fieldModel, pool Pool) error {
	if e.Rise <= 0 || m == nil {
		return nil
	}
	ps := pf.g.S()
	us := g.S()
	inf := float32(math.Inf(1))
	dist := make([]float32, ps*ps)
	any := false
	for b := 0; b < ps; b++ {
		for a := 0; a < ps; a++ {
			i, j := pf.i0+a, pf.j0+b
			d := inf
			if i < us && j < us {
				if rr := m.reg[j*us+i]; rr >= mixed || int(rr) != r {
					d = 0
					any = true
				}
			}
			dist[b*ps+a] = d
		}
	}
	if !any {
		return nil
	}
	chamfer(dist, ps)
	cell := float32(g.Cell)
	return rows(pool, ps, func(b int) {
		for a := 0; a < ps; a++ {
			k := b*ps + a
			f := smooth01(float64(dist[k]*cell) / e.Rise)
			if f >= 1 {
				continue
			}
			pf.g.H[k] = float32(e.Level + f*(float64(pf.g.H[k])-e.Level))
		}
	})
}

// smooth01 is smoothstep(0, 1, t).
func smooth01(t float64) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	return t * t * (3 - 2*t)
}

// chamfer turns d (0 at sources, +Inf elsewhere, s×s) into each sample's
// distance in cells to the nearest source: two passes of the 1/√2 chamfer
// (within 8% of the Euclidean distance, which a fade does not notice).
func chamfer(d []float32, s int) {
	const diag = float32(math.Sqrt2)
	at := func(i, j int) float32 { return d[j*s+i] }
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			v := d[j*s+i]
			if i > 0 {
				v = min(v, at(i-1, j)+1)
			}
			if j > 0 {
				v = min(v, at(i, j-1)+1)
				if i > 0 {
					v = min(v, at(i-1, j-1)+diag)
				}
				if i+1 < s {
					v = min(v, at(i+1, j-1)+diag)
				}
			}
			d[j*s+i] = v
		}
	}
	for j := s - 1; j >= 0; j-- {
		for i := s - 1; i >= 0; i-- {
			v := d[j*s+i]
			if i+1 < s {
				v = min(v, at(i+1, j)+1)
			}
			if j+1 < s {
				v = min(v, at(i, j+1)+1)
				if i+1 < s {
					v = min(v, at(i+1, j+1)+diag)
				}
				if i > 0 {
					v = min(v, at(i-1, j+1)+diag)
				}
			}
			d[j*s+i] = v
		}
	}
}
