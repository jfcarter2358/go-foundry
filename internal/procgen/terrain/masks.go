package terrain

// masks.go — what later phases read off the finished field.
//
// Every mask is (N+1)² values in 0..1, sample-aligned with the heights:
//
//	flow           drainage area, log-scaled: 0 at 4 m² (a lone sample) to 1
//	               at a quarter of a square kilometre — washes and gully lines.
//	               Routed by priority flood on the final surface, then widened
//	               a little so a channel is more than one sample wide.
//	slope          the surface angle over 90°.
//	deposition     what erosion laid down, d/(d+1) of d metres: 0.5 is a metre
//	               of sand or scree — wash floors, playas, talus aprons.
//	cliff          slope past the cliff angle, feathered over ±6°.
//	strata         the strata list index of the layer at the surface, stored
//	               as index/255 so its 8-bit PNG holds the index exactly.
//	strata_height  the position within that layer, 0 at its base, 1 at its top.
//	curvature      0.5 flat; above is concave (hollows, cavities, gully
//	               floors), below convex (ridges, rims, ledge lips).
//
// And the ALBEDO HINT: each sample's rock colour from its layer, varied by a
// little noise, darkened on cliff faces (desert varnish), and blended to the
// sand colour by deposition and flow (washes, aprons). It is a hint for the
// material phases and the shaded-relief preview, not a final texture.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// Mask names, in the order the metadata lists them.
const (
	MaskFlow         = "flow"
	MaskSlope        = "slope"
	MaskDeposition   = "deposition"
	MaskCliff        = "cliff"
	MaskStrata       = "strata"
	MaskStrataHeight = "strata_height"
	MaskCurvature    = "curvature"
)

// MaskNames is every mask, in order.
var MaskNames = []string{MaskFlow, MaskSlope, MaskDeposition, MaskCliff, MaskStrata, MaskStrataHeight, MaskCurvature}

// MaskDocs describe each mask, for the metadata and the docs.
var MaskDocs = map[string]string{
	MaskFlow:         "drainage area, log-scaled: 0 at 4 m², 1 at 0.25 km²",
	MaskSlope:        "surface angle / 90°",
	MaskDeposition:   "deposited material d/(d+1), d in metres (0.5 = 1 m)",
	MaskCliff:        "slope past the cliff angle, feathered ±6°",
	MaskStrata:       "strata list index of the surface layer (8-bit value = index)",
	MaskStrataHeight: "position within the surface layer, 0 bottom .. 1 top",
	MaskCurvature:    "0.5 flat, > 0.5 concave (cavity), < 0.5 convex (ridge)",
}

// flowAreaMin and flowAreaMax are the flow mask's log range, square metres.
const (
	flowAreaMin = 4
	flowAreaMax = 250000
)

// computeMasks fills res's masks, strata index and albedo from the field.
// The flow, slope, deposition and curvature are the surface's own; the
// cliff angle, the strata layer and the colours are the material model's —
// for a band sample the cliff mask and the albedo blend the regions' by
// weight, and the strata index and height are the dominant region's (an
// index cannot be blended: it indexes that region's strata list, and the
// biome masks say whose).
func computeMasks(fd *field, res *Result, pool Pool) error {
	g := fd.g
	sz := g.S()
	n := sz * sz
	m := map[string][]float32{}
	for _, name := range MaskNames {
		m[name] = make([]float32, n)
	}
	res.Masks = m
	res.StrataIndex = make([]uint8, n)
	res.Albedo = make([]uint8, n*3)
	cell := float32(g.Cell)

	// Flow, on the final surface.
	fl := newFlood(n)
	fl.run(g.H, sz, 1e-4*cell)
	area := make([]float32, n)
	if fd.mfdFlow {
		fl.accumulateMFD(area, sz)
	} else {
		fl.accumulate(area)
	}
	lmin, lmax := math.Log(flowAreaMin), math.Log(flowAreaMax)
	raw := m[MaskFlow]
	ca := float64(cell * cell)
	for k, a := range area {
		raw[k] = float32(noise.Clamp((math.Log(float64(a)*ca)-lmin)/(lmax-lmin), 0, 1))
	}
	if fd.mfdFlow {
		// A closed hollow the flood filled — a canyon's trench ending in a
		// plain, a basin in a band — is a playa, not a channel: its filled
		// surface is a flat the routed area fans across from the spill
		// point in straight rays. Under more than a few decimetres of fill
		// there is no wash, and neither is there on ground flatter than
		// about half a degree (a pan, a fan's toe), where the routing is
		// only the flood's forced gradient.
		for j := 0; j < sz; j++ {
			for i := 0; i < sz; i++ {
				k := j*sz + i
				f := 1.0
				if d := fl.filled[k] - g.H[k]; d > 0 {
					f = 1 - noise.SmoothStep(0.1, 0.8, float64(d))
				}
				il, ir := max(i-2, 0), min(i+2, sz-1)
				jl, jr := max(j-2, 0), min(j+2, sz-1)
				gx := float64(g.H[j*sz+ir]-g.H[j*sz+il]) / float64(cell*float32(ir-il))
				gz := float64(g.H[jr*sz+i]-g.H[jl*sz+i]) / float64(cell*float32(jr-jl))
				f *= noise.SmoothStep(0.002, 0.012, math.Hypot(gx, gz))
				raw[k] *= float32(f)
			}
		}
	}
	fl = nil
	area = nil
	widen(raw, sz, max(int(math.Round(2.5/g.Cell)), 1))

	cr := max(int(math.Round(2/g.Cell)), 1)
	cspan := float32(cr) * cell
	model := fd.m
	return rows(pool, sz, func(j int) {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			il, ir := max(i-1, 0), min(i+1, sz-1)
			jl, jr := max(j-1, 0), min(j+1, sz-1)
			gx := (g.H[j*sz+ir] - g.H[j*sz+il]) / (cell * float32(ir-il))
			gz := (g.H[jr*sz+i] - g.H[jl*sz+i]) / (cell * float32(jr-jl))
			ang := math.Atan(math.Hypot(float64(gx), float64(gz))) * 180 / math.Pi
			m[MaskSlope][k] = float32(ang / 90)
			d := fd.deposit[k]
			m[MaskDeposition][k] = d / (d + 1)
			// Curvature: a Laplacian over about 2 m.
			lap := (g.At(i+cr, j) + g.At(i-cr, j) + g.At(i, j+cr) + g.At(i, j-cr) - 4*g.H[k]) / (cspan * cspan)
			m[MaskCurvature][k] = float32(0.5 + 0.5*math.Tanh(float64(lap)*3))

			if r := model.reg[k]; r < mixed {
				mat := model.mats[r]
				m[MaskCliff][k] = float32(noise.SmoothStep(mat.cliffLo, mat.cliffHi, ang))
				idx, u := mat.c.layer(float64(g.H[k] + model.off[k]))
				res.StrataIndex[k] = uint8(min(idx, 255))
				m[MaskStrata][k] = float32(min(idx, 255)) / 255
				m[MaskStrataHeight][k] = float32(u)
				r, gg, b := albedoAt(mat, idx, g.X(i), g.Z(j), float64(m[MaskCliff][k]), float64(m[MaskDeposition][k]), float64(raw[k]))
				res.Albedo[3*k] = to8(r)
				res.Albedo[3*k+1] = to8(gg)
				res.Albedo[3*k+2] = to8(b)
				continue
			}
			// A band sample.
			cliff := float32(0)
			for _, e := range model.entries(k) {
				mat := model.mats[e.r]
				cliff += e.w * float32(noise.SmoothStep(mat.cliffLo, mat.cliffHi, ang))
			}
			m[MaskCliff][k] = cliff
			dr, doff := model.dominant(k)
			idx, u := model.mats[dr].c.layer(float64(g.H[k] + doff))
			res.StrataIndex[k] = uint8(min(idx, 255))
			m[MaskStrata][k] = float32(min(idx, 255)) / 255
			m[MaskStrataHeight][k] = float32(u)
			var col [3]float64
			for _, e := range model.entries(k) {
				mat := model.mats[e.r]
				li, _ := mat.c.layer(float64(g.H[k] + e.off))
				r, gg, b := albedoAt(mat, li, g.X(i), g.Z(j), float64(cliff), float64(m[MaskDeposition][k]), float64(raw[k]))
				w := float64(e.w)
				col[0] += w * r
				col[1] += w * gg
				col[2] += w * b
			}
			res.Albedo[3*k] = to8(col[0])
			res.Albedo[3*k+1] = to8(col[1])
			res.Albedo[3*k+2] = to8(col[2])
		}
	})
}

// albedoAt is a material's albedo hint for layer idx at world (x, z) given
// the sample's cliff, deposition and flow mask values: the layer colour,
// varied by noise, darkened on cliffs, blended to sand by deposits and
// washes.
func albedoAt(mat *material, idx int, x, z, cl, dep, flow float64) (float64, float64, float64) {
	col := mat.c.layers[idx].Color
	lx, lz := mat.local(x, z)
	v := 1 + 0.12*mat.vary.At2(lx, lz)
	r, gg, b := col[0]*v, col[1]*v, col[2]*v
	r, gg, b = r*(1-0.3*cl), gg*(1-0.33*cl), b*(1-0.3*cl)
	sd := noise.Clamp(dep*1.4+flow*0.5*(1-cl), 0, 1)
	sand := mat.sand
	r += (sand[0] - r) * sd
	gg += (sand[1] - gg) * sd
	b += (sand[2] - b) * sd
	return r, gg, b
}

func to8(v float64) uint8 { return uint8(noise.Clamp(v, 0, 1)*255 + 0.5) }

// widen spreads a mask's lines over radius r: each sample becomes the larger
// of itself and its neighbourhood's mean scaled so a lone line keeps most of
// its value — channels a few samples wide, not one.
func widen(v []float32, s, r int) {
	tmp := make([]float32, len(v))
	blur := make([]float32, len(v))
	inv := 1 / float32(2*r+1)
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			sum := float32(0)
			for d := -r; d <= r; d++ {
				sum += v[j*s+min(max(i+d, 0), s-1)]
			}
			tmp[j*s+i] = sum * inv
		}
	}
	for j := 0; j < s; j++ {
		for i := 0; i < s; i++ {
			sum := float32(0)
			for d := -r; d <= r; d++ {
				sum += tmp[min(max(j+d, 0), s-1)*s+i]
			}
			blur[j*s+i] = sum * inv
		}
	}
	for k := range v {
		v[k] = max(v[k], min(blur[k]*1.8, 1))
	}
}

// Relief is a shaded-relief picture of g coloured by albedo (RGB per sample,
// or nil for a height tint), as RGBA: the low sun from the north-west that
// cartographers use, a sky fill and a touch of height so tops read as tops.
func Relief(g *Grid, albedo []uint8) []uint8 {
	sz := g.S()
	out := make([]uint8, sz*sz*4)
	cell := float32(g.Cell)
	lx, ly, lz := float32(-0.55), float32(0.62), float32(-0.55)
	ll := float32(math.Sqrt(float64(lx*lx + ly*ly + lz*lz)))
	lx, ly, lz = lx/ll, ly/ll, lz/ll
	lo, hi := g.Range()
	span := hi - lo
	if span == 0 {
		span = 1
	}
	for j := 0; j < sz; j++ {
		for i := 0; i < sz; i++ {
			il, ir := max(i-1, 0), min(i+1, sz-1)
			jl, jr := max(j-1, 0), min(j+1, sz-1)
			dx := (g.H[j*sz+ir] - g.H[j*sz+il]) / (cell * float32(ir-il))
			dz := (g.H[jr*sz+i] - g.H[jl*sz+i]) / (cell * float32(jr-jl))
			nl := float32(math.Sqrt(float64(dx*dx + 1 + dz*dz)))
			nx, ny, nz := -dx/nl, 1/nl, -dz/nl
			sun := max(nx*lx+ny*ly+nz*lz, 0)
			sky := 0.5 + 0.5*ny
			lit := sun*0.82 + sky*0.28
			k := j*sz + i
			t := (g.H[k] - lo) / span
			var r, gg, b float32
			if albedo != nil {
				r, gg, b = float32(albedo[3*k])/255, float32(albedo[3*k+1])/255, float32(albedo[3*k+2])/255
			} else {
				r, gg, b = 0.55+0.35*t, 0.45+0.15*t, 0.32+0.05*t
			}
			lit *= 0.92 + 0.16*t
			o := k * 4
			out[o] = uint8(min(r*lit*1.15, 1) * 255)
			out[o+1] = uint8(min(gg*lit*1.15, 1) * 255)
			out[o+2] = uint8(min(b*lit*1.15, 1) * 255)
			out[o+3] = 255
		}
	}
	return out
}
