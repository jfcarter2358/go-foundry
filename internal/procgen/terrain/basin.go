package terrain

// basin.go — painted ponds, lakes and marshes carved into a finished
// heightfield (docs/terrain-paint/PHASE_W2.md; the `ground_edit` stage,
// stages/ground_edit*.go, is what calls it).
//
// FindWater (water.go) answers where water WOULD stand on ground the
// terrain generator made. A painted pond is the other way round: the author
// says where the water is, and the ground has to be made to hold it. So a
// basin is a pure function of the heights, the painted area (a 0..1 field,
// a `water` paint layer) and a profile:
//
//  1. THE AREA. Samples whose painted value is at least Threshold, split
//     into 8-connected components. Each component is one body; one smaller
//     than MinArea is left alone (a speck of overspray is not a pond).
//
//  2. THE SHORE DISTANCE. Every sample near a component gets a SIGNED
//     distance to the painted outline, metres, positive inside: an exact
//     Euclidean distance transform (Felzenszwalb's, separable) each way,
//     then rounded by two 3×3 box passes, so the outline is a smooth curve
//     through the brush's edge rather than a staircase of samples (a first
//     try refined it by the painted value's slope, (v − threshold)/|∇v|;
//     where the value saturates at 0 or 1 that has no slope, and the bank
//     came out stepped).
//
//  3. THE LEVEL. By default the SPILL height: the lowest ground on the rim
//     (the ring of samples within a cell and a half outside the outline),
//     less Freeboard, so the water can never run out over the rim it was
//     measured on. Or an explicit level, matched to the body by a world
//     point inside it (BasinLevel.At): a key that survives repainting the
//     edges, since a body is whichever component holds the point.
//
//  4. THE BED, inside the outline: the water surface less a depth that
//     shelves with distance from the shore —
//
//	depth(d) = ShelfDepth·S(0, Bank, d)                       the bank
//	         + (Depth − ShelfDepth)·S(Bank + Shelf, … , d)    the deep middle
//
//     (S a smoothstep; the deep part runs to three quarters of the way to
//     the most inland point, so the middle is a bowl, not a cone). A small
//     body scales the bank and shelf down and is shallower: a 6 m pond is
//     not 2.5 m deep. Low noise roughens the bed (none at the waterline, so
//     the shore stays on the outline), and Hummocks — a marsh — add bumps
//     biased upward, so some break the surface as islands. The ground is
//     only ever LOWERED inside: a natural hollow already deeper than the
//     profile stays.
//
//  5. THE BANK, outside the outline, within Reach: ground higher than
//     level + Freeboard + BankSlope·distance is cut down to it (a cut bank
//     on the uphill side of a pond on a slope, instead of a cliff), faded
//     out over the last 40% of Reach. With an explicit level above the rim,
//     a berm holds the water: the rim is raised to level + Freeboard and
//     falls away outside at BankSlope.
//
//  6. THE WATER is then FLOODED from the body's submerged inside samples
//     through every 8-connected sample below the level, so the surface
//     covers exactly what is under it and nothing past the rim (the rim is
//     above the level by construction). Each sample under water records the
//     body and the level — the same WaterResult FindWater builds, so the
//     region terrain's water path meshes and masks it unchanged.

import (
	"math"
	"sort"

	"foundry/internal/procgen/noise"
)

// BasinProfile is how one kind of body is carved (file comment). Lengths
// and heights are metres.
type BasinProfile struct {
	// Threshold is the painted value from which a sample is water area.
	Threshold float64
	// Bank is the run from the waterline to the shelf; ShelfDepth the
	// shelf's depth and Shelf its width; Depth the deepest point.
	Bank, ShelfDepth, Shelf, Depth float64
	// Noise roughens the bed by up to this much over NoiseSize.
	Noise, NoiseSize float64
	// Hummocks (a marsh) are bumps of up to this height over HummockSize,
	// biased upward so some stand clear of the water.
	Hummocks, HummockSize float64
	// BankSlope is the outer bank's steepest rise per metre; Reach how far
	// outside the outline it may cut or fill.
	BankSlope, Reach float64
	// MinArea (m²): smaller components are not bodies.
	MinArea float64
	// Freeboard is how far below its rim an automatic level stands.
	Freeboard float64
	// Floor is the lowest the bed may go (the tiles' closed base, plus a
	// little); -Inf for none.
	Floor float64
	Seed  uint64
}

// DefaultBasinProfile is a pond's.
func DefaultBasinProfile() BasinProfile {
	return BasinProfile{Threshold: 0.5, Bank: 3, ShelfDepth: 0.4, Shelf: 3, Depth: 2.2, Noise: 0.12, NoiseSize: 7,
		HummockSize: 4, BankSlope: 0.3, Reach: 14, MinArea: 6, Freeboard: 0.05, Floor: math.Inf(-1)}
}

// BasinLevel pins the level of the body holding world point At — or, with
// Auto, only names the body (its level stays the spill height; the stage
// keys its own per-body settings, a water material, by it).
type BasinLevel struct {
	At    [2]float64
	Level float64
	Auto  bool
}

// Basin is one carved body.
type Basin struct {
	// Anchor is its most inland sample (world XZ): a stable name for it,
	// and the point a pinned level is written at.
	Anchor [2]float64
	// Level is the water surface; Spill the rim's lowest ground before
	// carving (an automatic level is Spill − Freeboard); Locked whether
	// Level came from a BasinLevel (Pin is its index).
	Level, Spill float64
	Locked       bool
	Pin          int
	// Painted is the painted area (m²); Area the water's (m²) and Samples
	// its sample count; Bounds the water's world [min x, min z, max x, max
	// z]; MaxDepth the deepest water.
	Painted  float64
	Area     float64
	Samples  int
	Bounds   [4]float64
	MaxDepth float64
}

// BasinResult is what CarveBasins did.
type BasinResult struct {
	Bodies []Basin
	// Water is the standing water of the bodies: per sample, the level and
	// the body (an index into Bodies).
	Water *WaterResult
	// Changed is the samples the carve moved (their count).
	Changed int
	// Orphans are the pins that fell in no body (repainted away).
	Orphans []int
}

// CarveBasins carves every painted body into g (in place) and returns them
// (file comment). area is the painted value at a world point; rect (min x,
// min z, max x, max z) bounds where it can be non-zero, so a kilometre
// terrain with one small pond samples only round the pond.
func CarveBasins(g *Grid, area func(x, z float64) float64, rect [4]float64, prof BasinProfile, pins []BasinLevel) *BasinResult {
	s := g.S()
	n := s * s
	cell := g.Cell
	nan := float32(math.NaN())
	res := &BasinResult{Water: &WaterResult{Level: make([]float32, n), Body: make([]int32, n)}}
	for k := range res.Water.Level {
		res.Water.Level[k] = nan
		res.Water.Body[k] = -1
	}
	// The window of samples the paint can reach, plus the bank's reach.
	pad := int(math.Ceil(prof.Reach/cell)) + 3
	wi0 := max(int(math.Floor((rect[0]-g.MinX)/cell))-1, 0)
	wj0 := max(int(math.Floor((rect[1]-g.MinZ)/cell))-1, 0)
	wi1 := min(int(math.Ceil((rect[2]-g.MinX)/cell))+1, s-1)
	wj1 := min(int(math.Ceil((rect[3]-g.MinZ)/cell))+1, s-1)
	if wi1 < wi0 || wj1 < wj0 {
		return res
	}
	// 1. The painted values and the components.
	val := map[int]float32{}
	at := func(i, j int) float32 {
		if i < wi0 || j < wj0 || i > wi1 || j > wj1 {
			return 0
		}
		return val[j*s+i]
	}
	for j := wj0; j <= wj1; j++ {
		for i := wi0; i <= wi1; i++ {
			if v := area(g.X(i), g.Z(j)); v > 0 {
				val[j*s+i] = float32(v)
			}
		}
	}
	thr := float32(prof.Threshold)
	comp := map[int]int{}
	type component struct {
		members        []int
		i0, j0, i1, j1 int
	}
	var comps []component
	keys := make([]int, 0, len(val))
	for k, v := range val {
		if v >= thr {
			keys = append(keys, k)
		}
	}
	sort.Ints(keys)
	for _, k0 := range keys {
		if _, ok := comp[k0]; ok {
			continue
		}
		c := component{i0: k0 % s, j0: k0 / s, i1: k0 % s, j1: k0 / s}
		id := len(comps)
		comp[k0] = id
		stack := []int{k0}
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			c.members = append(c.members, k)
			i, j := k%s, k/s
			c.i0, c.j0, c.i1, c.j1 = min(c.i0, i), min(c.j0, j), max(c.i1, i), max(c.j1, j)
			for d := 0; d < 8; d++ {
				ni, nj := i+nbDI[d], j+nbDJ[d]
				if ni < 0 || nj < 0 || ni >= s || nj >= s {
					continue
				}
				q := nj*s + ni
				if _, ok := comp[q]; ok || at(ni, nj) < thr {
					continue
				}
				comp[q] = id
				stack = append(stack, q)
			}
		}
		comps = append(comps, c)
	}

	// Each body, carved in order of its anchor (so the result is the same
	// whatever order the map handed the components out).
	type carved struct {
		b               Basin
		comp            int
		wi0, wj0, w, h  int
		sd              []float32
		inside          []int
		fixedLevelBerm  bool
		level           float32
	}
	var bodies []carved
	for ci, c := range comps {
		if float64(len(c.members))*cell*cell < prof.MinArea {
			continue
		}
		// 2. Signed distance over the component's window.
		x0, z0 := max(c.i0-pad, 0), max(c.j0-pad, 0)
		x1, z1 := min(c.i1+pad, s-1), min(c.j1+pad, s-1)
		w, h := x1-x0+1, z1-z0+1
		inC := make([]bool, w*h)
		for _, k := range c.members {
			inC[(k/s-z0)*w+(k%s-x0)] = true
		}
		dIn := edt(inC, w, h, false) // inside: squared distance to the nearest outside sample
		dOut := edt(inC, w, h, true) // outside: to the nearest inside sample
		sd := make([]float32, w*h)
		for q := range sd {
			if inC[q] {
				sd[q] = float32((math.Sqrt(float64(dIn[q])) - 0.5) * cell)
			} else {
				sd[q] = -float32((math.Sqrt(float64(dOut[q])) - 0.5) * cell)
			}
		}
		// The transform's zero line is a staircase between samples: two
		// 3×3 box passes round it into the brush's outline (a blurred
		// distance field is still one, slope about 1).
		sd = boxSmooth(boxSmooth(sd, w, h), w, h)
		for q := range sd {
			// Keep each sample on its own side of the outline.
			if inC[q] {
				sd[q] = max(sd[q], 0)
			} else {
				sd[q] = min(sd[q], -1e-3)
			}
		}
		// The anchor: the most inland sample.
		best, bestK := float32(-1), c.members[0]
		for _, k := range c.members {
			q := (k/s-z0)*w + (k%s - x0)
			if sd[q] > best || (sd[q] == best && k < bestK) {
				best, bestK = sd[q], k
			}
		}
		b := Basin{Anchor: [2]float64{round2(g.X(bestK % s)), round2(g.Z(bestK / s))}, Pin: -1,
			Painted: float64(len(c.members)) * cell * cell}
		bodies = append(bodies, carved{b: b, comp: ci, wi0: x0, wj0: z0, w: w, h: h, sd: sd, inside: c.members})
	}
	sort.SliceStable(bodies, func(a, b int) bool {
		pa, pb := bodies[a].b.Anchor, bodies[b].b.Anchor
		if pa[1] != pb[1] {
			return pa[1] < pb[1]
		}
		return pa[0] < pb[0]
	})
	// Pins to bodies: the component holding the sample nearest each pin.
	pinOf := map[int]int{}
	for pi, p := range pins {
		i := int(math.Round((p.At[0] - g.MinX) / cell))
		j := int(math.Round((p.At[1] - g.MinZ) / cell))
		ci, ok := -1, false
		if i >= 0 && j >= 0 && i < s && j < s {
			ci, ok = comp[j*s+i]
		}
		matched := false
		if ok {
			for bi := range bodies {
				if bodies[bi].comp == ci {
					if _, taken := pinOf[bi]; !taken {
						pinOf[bi] = pi
						matched = true
					}
					break
				}
			}
		}
		if !matched {
			res.Orphans = append(res.Orphans, pi)
		}
	}

	changed := make(map[int]bool)
	floor := float32(prof.Floor)
	for bi := range bodies {
		cb := &bodies[bi]
		x0, z0, w, sd := cb.wi0, cb.wj0, cb.w, cb.sd
		// 3. The level.
		rim := float32(math.Inf(1))
		for q := range sd {
			if sd[q] < 0 && sd[q] >= -float32(1.5*cell) {
				rim = min(rim, g.H[(z0+q/w)*s+x0+q%w])
			}
		}
		if math.IsInf(float64(rim), 1) {
			for _, k := range cb.inside {
				rim = min(rim, g.H[k])
			}
		}
		fb := float32(prof.Freeboard)
		level := rim - fb
		if pi, ok := pinOf[bi]; ok {
			cb.b.Pin = pi
			if !pins[pi].Auto {
				level = float32(pins[pi].Level)
				cb.b.Locked = true
				cb.fixedLevelBerm = level+fb > rim
			}
		}
		cb.level = level
		cb.b.Level, cb.b.Spill = float64(level), float64(rim)

		// 4. The profile, scaled to the body.
		maxD := 0.0
		for _, k := range cb.inside {
			maxD = math.Max(maxD, float64(sd[(k/s-z0)*w+(k%s-x0)]))
		}
		bank, shelf := prof.Bank, prof.Shelf
		total := bank + shelf
		if maxD < 1.25*total && total > 0 {
			f := maxD / (1.25 * total)
			bank, shelf = bank*f, shelf*f
		}
		deep := prof.ShelfDepth + (prof.Depth-prof.ShelfDepth)*noise.SmoothStep(0, 2*total, maxD)
		run := math.Max(maxD-bank-shelf, cell)
		seed := noise.Hash64(prof.Seed, 0xba51)
		bedAt := func(x, z, d float64) float32 {
			bankW := noise.SmoothStep(0, math.Max(bank, 1e-6), d)
			depth := prof.ShelfDepth*bankW + (deep-prof.ShelfDepth)*noise.SmoothStep(bank+shelf, bank+shelf+0.75*run, d)
			if prof.Noise > 0 {
				depth -= prof.Noise * bankW * noise.FBM2(seed, x/math.Max(prof.NoiseSize, 0.5), z/math.Max(prof.NoiseSize, 0.5), 3, 0.5)
			}
			if prof.Hummocks > 0 {
				hs := math.Max(prof.HummockSize, 0.5)
				v := noise.FBM2(seed^0x4b4d, x/hs, z/hs, 2, 0.5)
				depth -= prof.Hummocks * noise.SmoothStep(0, math.Max(0.5*bank, 1e-6), d) * (v + 0.35)
			}
			return level - float32(depth)
		}
		slope := float32(prof.BankSlope)
		reach := prof.Reach
		for q := range sd {
			i, j := x0+q%w, z0+q/w
			k := j*s + i
			d := float64(sd[q])
			old := g.H[k]
			nv := old
			if d >= 0 {
				nv = min(old, bedAt(g.X(i), g.Z(j), d))
			} else if dist := -d; dist <= reach {
				fade := float32(1 - noise.SmoothStep(0.6*reach, reach, dist))
				// 5. The bank: cut what stands above the slope from the
				// waterline; with a pinned level over the rim, a berm.
				if cap := level + fb + slope*float32(dist); nv > cap {
					nv -= (nv - cap) * fade
				}
				if cb.fixedLevelBerm {
					berm := level + fb - slope*float32(max(dist-1.5*cell, 0))
					if nv < berm {
						nv += (berm - nv) * fade
					}
				}
			}
			if nv < floor {
				nv = floor
			}
			if nv != old {
				g.H[k] = nv
				changed[k] = true
			}
		}
	}
	res.Changed = len(changed)

	// 6. The water, flooded from each body's submerged inside.
	cell2 := cell * cell
	for bi := range bodies {
		cb := &bodies[bi]
		lv := cb.level
		x0, z0, w, h := cb.wi0, cb.wj0, cb.w, cb.h
		inWin := func(i, j int) bool { return i >= x0 && j >= z0 && i < x0+w && j < z0+h }
		id := int32(len(res.Bodies))
		var stack []int
		for _, k := range cb.inside {
			if g.H[k] < lv && res.Water.Body[k] < 0 {
				res.Water.Body[k], res.Water.Level[k] = id, lv
				stack = append(stack, k)
			}
		}
		b := cb.b
		b.Bounds = [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			i, j := k%s, k/s
			x, z := g.X(i), g.Z(j)
			b.Bounds = [4]float64{math.Min(b.Bounds[0], x), math.Min(b.Bounds[1], z), math.Max(b.Bounds[2], x), math.Max(b.Bounds[3], z)}
			b.Samples++
			b.MaxDepth = math.Max(b.MaxDepth, float64(lv-g.H[k]))
			for d := 0; d < 8; d++ {
				ni, nj := i+nbDI[d], j+nbDJ[d]
				if !inWin(ni, nj) {
					continue
				}
				q := nj*s + ni
				if res.Water.Body[q] >= 0 || g.H[q] >= lv {
					continue
				}
				res.Water.Body[q], res.Water.Level[q] = id, lv
				stack = append(stack, q)
			}
		}
		if b.Samples == 0 {
			b.Bounds = [4]float64{}
		}
		b.Area = float64(b.Samples) * cell2
		b.MaxDepth = round2(b.MaxDepth)
		res.Bodies = append(res.Bodies, b)
		res.Water.Bodies = append(res.Water.Bodies, WaterBody{Pond: true, Level: float64(lv), Region: -1, Bounds: b.Bounds,
			Area: b.Area, Samples: b.Samples})
	}
	return res
}

// boxSmooth is v (w×h) under a 3×3 box, the window's edge clamped.
func boxSmooth(v []float32, w, h int) []float32 {
	out := make([]float32, len(v))
	for j := 0; j < h; j++ {
		for i := 0; i < w; i++ {
			var sum float32
			for dj := -1; dj <= 1; dj++ {
				for di := -1; di <= 1; di++ {
					sum += v[min(max(j+dj, 0), h-1)*w+min(max(i+di, 0), w-1)]
				}
			}
			out[j*w+i] = sum / 9
		}
	}
	return out
}

// edt is a squared Euclidean distance (in samples) per sample: with
// outside false, each inside sample's distance to the nearest outside one
// (outside samples read 0); with outside true, each outside sample's to the
// nearest inside one (inside samples read 0). Felzenszwalb & Huttenlocher's
// separable transform: exact, linear in the samples. The inside transform
// counts the window's edge as outside (a body painted off the map's edge
// shelves toward it).
func edt(in []bool, w, h int, outside bool) []float32 {
	if !outside {
		// The window's border is outside: transform with a ring of outside
		// samples round it, and cut the ring off.
		pw, ph := w+2, h+2
		pin := make([]bool, pw*ph)
		for j := 0; j < h; j++ {
			copy(pin[(j+1)*pw+1:(j+1)*pw+1+w], in[j*w:(j+1)*w])
		}
		pd := edtRaw(pin, pw, ph, false)
		out := make([]float32, w*h)
		for j := 0; j < h; j++ {
			copy(out[j*w:(j+1)*w], pd[(j+1)*pw+1:(j+1)*pw+1+w])
		}
		return out
	}
	return edtRaw(in, w, h, true)
}

// edtRaw is edt with nothing beyond the window.
func edtRaw(in []bool, w, h int, outside bool) []float32 {
	const inf = 1e20
	f := make([]float64, w*h)
	for q := range f {
		target := !in[q]
		if outside {
			target = in[q]
		}
		if target {
			f[q] = 0
		} else {
			f[q] = inf
		}
	}
	n := max(w, h)
	buf := make([]float64, n)
	out := make([]float64, n)
	v := make([]int, n)
	z := make([]float64, n+1)
	for j := 0; j < h; j++ {
		copy(buf[:w], f[j*w:(j+1)*w])
		edt1(buf[:w], out[:w], v, z)
		copy(f[j*w:(j+1)*w], out[:w])
	}
	for i := 0; i < w; i++ {
		for j := 0; j < h; j++ {
			buf[j] = f[j*w+i]
		}
		edt1(buf[:h], out[:h], v, z)
		for j := 0; j < h; j++ {
			f[j*w+i] = out[j]
		}
	}
	r := make([]float32, w*h)
	for q := range f {
		r[q] = float32(f[q])
	}
	return r
}

// edt1 is the one-dimensional squared distance transform of f into d (the
// lower envelope of the parabolas rooted at each sample; v and z are
// scratch of len(f) and len(f)+1).
func edt1(f, d []float64, v []int, z []float64) {
	n := len(f)
	k := 0
	v[0] = 0
	z[0], z[1] = math.Inf(-1), math.Inf(1)
	cross := func(q, p int) float64 {
		return ((f[q] + float64(q*q)) - (f[p] + float64(p*p))) / float64(2*q-2*p)
	}
	for q := 1; q < n; q++ {
		sx := cross(q, v[k])
		for sx <= z[k] {
			k--
			sx = cross(q, v[k])
		}
		k++
		v[k] = q
		z[k] = sx
		z[k+1] = math.Inf(1)
	}
	k = 0
	for q := 0; q < n; q++ {
		for z[k+1] < float64(q) {
			k++
		}
		p := v[k]
		d[q] = float64((q-p)*(q-p)) + f[p]
	}
}
