package terrain

// landform.go — the landforms a field provider can grow besides canyon
// country (docs/regions/PRESETS.md, "Engine work", item 3): ridged
// mountains with glacial valleys, a wind-shaped dune field, a coastline,
// volcanic cones with lava lobes, frost-heave hummocks with ponds.
//
// Each is part of the BASE SHAPE (shape.go's baseShaper): a pure function of
// position in the provider's own coordinates, evaluated before anything
// erodes, so drainage, droplets and talus work them over like any other
// ground — scree aprons form under the ridges by the talus angle, the
// dunes' lee faces relax to the angle of repose, the volcano's flanks gully
// — and the far backdrop (far.go), which samples the same baseShaper,
// continues them past the map's edge: the sea runs on to the horizon.
//
// EVERY ONE IS OFF BY DEFAULT. A zero Landform leaves baseShaper.at's
// classic arithmetic untouched (baseShaper.lf is nil, and the function
// returns before any of this runs), so the canyon goldens, the one-region
// identity and every existing preset are the same bytes.
//
// Order of application, at a point whose classic base height is h:
//
//  1. RIDGES add a ridged multifractal (sharp crests, broad valleys) of
//     Ridge.Height; Valleys raises the normalised ridge field to a power,
//     which flattens the low ground into wide, flat-floored troughs between
//     steep walls — a cheap stand-in for glacial U-valleys (no ice is
//     simulated);
//  2. DUNES add an asymmetric wave across the wind: a long gentle stoss
//     slope up to the crest, a short steep lee (slip face) down it, the
//     crests bent by noise (Sinuosity) and their height modulated by a slow
//     noise so the field breaks into dune trains and interdune flats
//     (Cover), with wind ripples on the stoss side as detail;
//  3. HUMMOCKS add a cellular field of low domes (frost heave), and PONDS
//     cut flat-floored hollows where a slow noise passes its cover
//     threshold — closed depressions the region terrain's water pass fills
//     (stages/region_terrain_water.go);
//  4. VOLCANOES add cones (the max over the vents, so neighbours merge),
//     each with a crater cut into its summit and lava lobes — flat-topped
//     tongues with steep lobate fronts — running down its flanks and out
//     over the plain. The vents are exported (Vents, Result.Vents,
//     Meta.Vents) for the ground material's ash rule;
//  5. the COAST re-profiles everything by the signed distance d from a
//     shoreline (inland positive): the sea floor shelving to Depth below
//     SeaLevel offshore, a beach rising BeachRise over Beach metres, an
//     optional wave-cut cliff, then the land's own relief returning over
//     Inland metres. The shoreline is a straight line Offset metres inside
//     one side of the region's footprint rectangle (meandered by noise), or
//     a drawn line (the sea on its right, walking it in order).
//
// All lengths are metres and angles degrees, so the landforms hold their
// size at any resolution, like the rest of the provider.

import (
	"math"
	"sort"

	"foundry/internal/procgen/noise"
)

// Landform is a provider's extra landforms; the zero value is none.
type Landform struct {
	Ridge    Ridge
	Dunes    Dunes
	Hummocks Hummocks
	Volcano  Volcano
	Coast    Coast
	// Stamps are authored landforms (stamps.go), applied last, in order.
	Stamps []Stamp
}

// Ridge is ridged mountains.
type Ridge struct {
	// Height is the crests' rise above the lowland; 0 is no mountains.
	Height float64
	// Scale is the ranges' typical spacing, metres.
	Scale   float64
	Octaves int
	// Valleys flattens the valley floors toward a U profile, 0..1.
	Valleys float64
	// Warp bends the ranges, metres.
	Warp float64
}

// Dunes is a dune field.
type Dunes struct {
	// Height is a dune's crest above the interdune flat; 0 is no dunes.
	Height float64
	// Wavelength is the crest-to-crest spacing, metres.
	Wavelength float64
	// Wind is the bearing the wind blows toward, degrees: 0 is +X, 90 +Z.
	Wind float64
	// Asymmetry is how much steeper the lee face is than the stoss slope,
	// 0 (symmetric) .. 1 (a slip face at the angle of repose).
	Asymmetry float64
	// Sinuosity bends the crests, 0..1.
	Sinuosity float64
	// Cover is the share of the ground under dunes, 0..1; the rest is
	// interdune flats.
	Cover float64
	// RippleHeight and RippleWavelength are the wind ripples, metres.
	RippleHeight, RippleWavelength float64
}

// Hummocks is frost-heave hummocks and thermokarst ponds.
type Hummocks struct {
	// Height is a hummock's rise and Spacing their spacing, metres; 0
	// height is none.
	Height, Spacing float64
	// Ponds is the share of the ground in pond hollows, 0..1; PondDepth
	// their depth and PondScale their typical size, metres.
	Ponds, PondDepth, PondScale float64
}

// Volcano is volcanic cones with craters and lava lobes.
type Volcano struct {
	// Count is how many cones are placed (seeded, inside the region's
	// footprint rectangle) when Vents is empty; 0 and no Vents is none.
	Count int
	// Vents are explicit vent positions (provider coordinates).
	Vents [][2]float64
	// Height and Radius are a cone's.
	Height, Radius float64
	// Crater is the crater's radius as a share of the cone's, CraterDepth
	// its depth below the rim.
	Crater, CraterDepth float64
	// Lobes per cone, each LobeLength long, LobeWidth wide and
	// LobeThickness thick (metres).
	Lobes                                int
	LobeLength, LobeWidth, LobeThickness float64
}

// Coast sides.
const (
	CoastOff  = ""
	CoastLine = "line"
)

// CoastSides are the UI spellings of the shoreline's placement: off, a side
// of the footprint rectangle the sea lies toward, or a drawn line.
var CoastSides = []string{"off", "+x", "-x", "+z", "-z", "line"}

// Coast is a shoreline.
type Coast struct {
	// Side is "+x", "-x", "+z" or "-z" (the sea lies toward that side of
	// the region's footprint rectangle), "line" (along Line), or ""/"off".
	Side string
	// Line is the drawn shoreline (provider coordinates); the sea is on its
	// right, walking it in order.
	Line [][2]float64
	// SeaLevel is the water's height, metres.
	SeaLevel float64
	// Offset is how far inside the footprint's side the shoreline runs, and
	// Meander how far it wanders off that line, metres.
	Offset, Meander float64
	// Beach is the beach's (or, with a cliff, the wave-cut platform's)
	// width and BeachRise its rise above the sea, metres.
	Beach, BeachRise float64
	// Shelf is how far offshore the sea floor reaches Depth below the sea.
	Shelf, Depth float64
	// Cliff is a wave-cut cliff's height (0: none) behind the beach and
	// CliffWidth its horizontal run.
	Cliff, CliffWidth float64
	// Inland is how far inland the land's own relief takes to return.
	Inland float64
}

// On reports whether the coast is drawn.
func (c *Coast) On() bool {
	switch c.Side {
	case "+x", "-x", "+z", "-z":
		return true
	case CoastLine:
		return len(c.Line) >= 2
	}
	return false
}

// Any reports whether any landform is on.
func (l *Landform) Any() bool {
	return l.Ridge.Height > 0 || l.Dunes.Height > 0 || l.Hummocks.Height > 0 || l.Hummocks.Ponds > 0 ||
		l.volcanoOn() || l.Coast.On() || l.stampsOn()
}

func (l *Landform) volcanoOn() bool {
	return l.Volcano.Height > 0 && l.Volcano.Radius > 0 && (l.Volcano.Count > 0 || len(l.Volcano.Vents) > 0)
}

// Vent is a volcanic vent: its position and its cone's radius.
type Vent struct {
	X, Z, Radius float64
}

// landformShaper is a provider's landforms, compiled.
type landformShaper struct {
	lf   Landform
	base float64 // the lowland's mean height (Base.Height)

	ridge, ridgeWarp noise.Fractal

	duneDir                 [2]float64
	duneBend, duneAmp       noise.Fractal
	duneRippleBend          noise.Fractal
	hummockSeed             uint64
	pond, pondEdge          noise.Fractal
	pondCut                 float64
	vents                   []Vent
	lobes                   []lavaLobe
	coastBend, coastFloor   noise.Fractal
	coastLine               [][2]float64
	coastAxis               int     // 0 x, 1 z
	coastSign, coastShoreAt float64 // shoreline coordinate along the axis; sign +1: sea toward +axis
	flank                   noise.Fractal
	stamps                  []*stampShaper
}

// lavaLobe is one lava tongue: from (x, z) along unit (dx, dz).
type lavaLobe struct {
	x, z, dx, dz      float64
	length, halfWidth float64
	thickness         float64
	seed              uint64
}

// newLandformShaper compiles s's landforms over the measured rectangle
// over (provider coordinates); nil when none is on.
func newLandformShaper(s *Settings, over Rect) *landformShaper {
	lf := s.Landform
	if !lf.Any() {
		return nil
	}
	ls := &landformShaper{lf: lf, base: s.Base.Height}
	h := func(k uint64) uint64 { return noise.Hash64(s.Seed, 0x4C46_0000+k) } // "LF"
	if r := lf.Ridge; r.Height > 0 {
		sc := math.Max(r.Scale, 10)
		ls.ridge = noise.Fractal{Seed: h(1), Octaves: max(r.Octaves, 1), Frequency: 1 / sc, Gain: 0.5, Shape: noise.ShapeRidged}
		ls.ridgeWarp = noise.Fractal{Seed: h(2), Octaves: 3, Frequency: 0.5 / sc}
	}
	if d := lf.Dunes; d.Height > 0 {
		wl := math.Max(d.Wavelength, 4)
		a := d.Wind * math.Pi / 180
		ls.duneDir = [2]float64{math.Cos(a), math.Sin(a)}
		ls.duneBend = noise.Fractal{Seed: h(3), Octaves: 3, Frequency: 1 / (3 * wl)}
		ls.duneAmp = noise.Fractal{Seed: h(4), Octaves: 3, Frequency: 1 / (5 * wl)}
		ls.duneRippleBend = noise.Fractal{Seed: h(5), Octaves: 2, Frequency: 1 / (8 * math.Max(d.RippleWavelength, 0.5))}
	}
	if hm := lf.Hummocks; hm.Height > 0 || hm.Ponds > 0 {
		ls.hummockSeed = h(6)
		ps := math.Max(hm.PondScale, 4)
		ls.pond = noise.Fractal{Seed: h(7), Octaves: 3, Frequency: 1 / ps, Gain: 0.45}
		ls.pondEdge = noise.Fractal{Seed: h(8), Octaves: 2, Frequency: 4 / ps}
		// The cut is MEASURED (the pond noise's quantile over the region's
		// rectangle, as the mesa field's tiers are), so Ponds is the share of
		// the ground in hollows whatever the noise's spread.
		ls.pondCut = quantile2(func(x, z float64) float64 { return ls.pond.At2(x, z) + 0.12*ls.pondEdge.At2(x, z) },
			over, 1-noise.Clamp(hm.Ponds, 0, 1))
	}
	if lf.volcanoOn() {
		ls.vents = placeVents(s, over)
		ls.flank = noise.Fractal{Seed: h(9), Octaves: 3, Frequency: 1, Gain: 0.5, Shape: noise.ShapeRidged}
		ls.lobes = lavaLobes(s, ls.vents)
	}
	ls.stamps = compileStamps(lf.Stamps)
	if c := lf.Coast; c.On() {
		ls.coastBend = noise.Fractal{Seed: h(10), Octaves: 3, Frequency: 1 / math.Max(4*c.Shelf, 200)}
		ls.coastFloor = noise.Fractal{Seed: h(11), Octaves: 3, Frequency: 1 / 90}
		switch c.Side {
		case "+x":
			ls.coastAxis, ls.coastSign, ls.coastShoreAt = 0, 1, over.MaxX-c.Offset
		case "-x":
			ls.coastAxis, ls.coastSign, ls.coastShoreAt = 0, -1, over.MinX+c.Offset
		case "+z":
			ls.coastAxis, ls.coastSign, ls.coastShoreAt = 1, 1, over.MaxZ-c.Offset
		case "-z":
			ls.coastAxis, ls.coastSign, ls.coastShoreAt = 1, -1, over.MinZ+c.Offset
		case CoastLine:
			ls.coastLine = c.Line
		}
	}
	return ls
}

// at is the landforms applied to classic base height h at (x, z).
func (ls *landformShaper) at(x, z, h float64) float64 {
	lf := &ls.lf
	if r := lf.Ridge; r.Height > 0 {
		wx, wz := x, z
		if r.Warp > 0 {
			wx, wz = noise.Warp2(ls.ridgeWarp, x, z, r.Warp)
		}
		v := noise.Clamp((ls.ridge.At2(wx, wz)+1)*0.5, 0, 1)
		if vl := noise.Clamp(r.Valleys, 0, 1); vl > 0 {
			// Glacial troughs: the low part of the range (below f) becomes
			// a flat floor, softly (a smooth max with 0), and the walls
			// above it steepen toward a U.
			f := 0.5 * vl
			t := (v - f) / (1 - f)
			const k = 0.04
			t = 0.5 * (t + math.Sqrt(t*t+k*k))
			v = math.Pow(t, 1+vl)
		}
		h += r.Height * v
	}
	if lf.Dunes.Height > 0 {
		h += ls.dune(x, z)
	}
	if hm := lf.Hummocks; hm.Height > 0 || hm.Ponds > 0 {
		h += ls.hummock(x, z)
	}
	if len(ls.vents) > 0 {
		h += ls.volcano(x, z)
	}
	if lf.Coast.On() {
		h = ls.coast(x, z, h)
	}
	for _, st := range ls.stamps {
		h = st.at(x, z, h)
	}
	return h
}

// dune is the dune field's height at (x, z) (file comment, step 2).
func (ls *landformShaper) dune(x, z float64) float64 {
	d := &ls.lf.Dunes
	wl := math.Max(d.Wavelength, 4)
	s := x*ls.duneDir[0] + z*ls.duneDir[1]
	s += noise.Clamp(d.Sinuosity, 0, 1) * wl * 0.9 * ls.duneBend.At2(x, z)
	f := s / wl
	f -= math.Floor(f)
	// The stoss slope runs up with the wind over share a of the wavelength,
	// the lee face down over the rest.
	a := 0.5 + 0.38*noise.Clamp(d.Asymmetry, 0, 1)
	// No steeper than a slip face: the smoothstep's steepest slope is 1.5
	// rise over run, held to the angle of repose (34°).
	a = math.Min(a, 1-1.5*d.Height/(wl*math.Tan(34*math.Pi/180)))
	a = math.Max(a, 0.5)
	var p float64
	lee := f >= a
	if !lee {
		u := f / a
		p = u * u * (3 - 2*u)
	} else {
		u := (1 - f) / (1 - a)
		p = u * u * (3 - 2*u)
	}
	// Dune trains and interdune flats: a slow noise above the cover cut.
	c := noise.Clamp(ls.duneAmp.At2(x, z)*0.75+0.5, 0, 1)
	cov := noise.Clamp(d.Cover, 0, 1)
	amp := noise.SmoothStep(1-cov-0.18, 1-cov+0.18, c)
	h := d.Height * amp * p
	if d.RippleHeight > 0 && d.RippleWavelength > 0 {
		rs := s + d.RippleWavelength*1.5*ls.duneRippleBend.At2(x, z)
		r := 0.5 + 0.5*math.Sin(2*math.Pi*rs/d.RippleWavelength)
		k := 1.0
		if lee {
			k = 0.25 // the slip face is too steep to hold ripples
		}
		h += d.RippleHeight * r * k * (0.4 + 0.6*amp)
	}
	return h
}

// hummock is the hummocks' rise less the ponds' hollows at (x, z).
func (ls *landformShaper) hummock(x, z float64) float64 {
	hm := &ls.lf.Hummocks
	pond := 0.0
	if hm.Ponds > 0 && hm.PondDepth > 0 {
		v := ls.pond.At2(x, z) + 0.12*ls.pondEdge.At2(x, z)
		// The FBM is roughly symmetric about 0 with most of its mass inside
		// ±0.5: cut it so about Ponds of the ground lies in hollows.
		pond = noise.SmoothStep(ls.pondCut, ls.pondCut+0.06, v)
	}
	out := -hm.PondDepth * pond
	if hm.Height > 0 && hm.Spacing > 0 {
		c := noise.Cellular2(ls.hummockSeed, x/hm.Spacing, z/hm.Spacing, 0.85, noise.MetricEuclidean)
		dome := noise.SmoothStep(0.62, 0.0, c.F1) * (0.55 + 0.45*noise.CellUnit(c.ID))
		out += hm.Height * dome * (1 - pond)
	}
	return out
}

// placeVents is s's vents in provider coordinates: the explicit ones, or
// Count seeded inside over (inset by most of a radius) at least 1.2 radii
// apart (dart throwing; fewer when they will not fit).
func placeVents(s *Settings, over Rect) []Vent {
	v := s.Landform.Volcano
	var out []Vent
	if len(v.Vents) > 0 {
		for _, p := range v.Vents {
			out = append(out, Vent{X: p[0], Z: p[1], Radius: v.Radius})
		}
		return out
	}
	rng := noise.NewRNG(noise.Hash64(s.Seed, 0x564F_4C43)) // "VOLC"
	in := math.Min(0.6*v.Radius, 0.45*math.Min(over.MaxX-over.MinX, over.MaxZ-over.MinZ))
	x0, x1 := over.MinX+in, over.MaxX-in
	z0, z1 := over.MinZ+in, over.MaxZ-in
	for k := 0; k < v.Count; k++ {
		for try := 0; try < 64; try++ {
			x := x0 + (x1-x0)*rng.Float64()
			z := z0 + (z1-z0)*rng.Float64()
			ok := true
			for _, q := range out {
				if math.Hypot(x-q.X, z-q.Z) < 1.2*v.Radius {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, Vent{X: x, Z: z, Radius: v.Radius * (0.8 + 0.4*rng.Float64())})
				break
			}
		}
	}
	return out
}

// lavaLobes is every vent's lava tongues: each leaves its cone's upper
// flank at a seeded bearing and runs LobeLength (±40%) outward.
func lavaLobes(s *Settings, vents []Vent) []lavaLobe {
	v := s.Landform.Volcano
	if v.Lobes <= 0 || v.LobeLength <= 0 || v.LobeThickness <= 0 {
		return nil
	}
	var out []lavaLobe
	for i, vt := range vents {
		rng := noise.NewRNG(noise.Hash64(s.Seed, 0x4C41_5641, uint64(i))) // "LAVA"
		a0 := 2 * math.Pi * rng.Float64()
		for k := 0; k < v.Lobes; k++ {
			a := a0 + 2*math.Pi*(float64(k)+0.35*(rng.Float64()-0.5))/float64(v.Lobes)
			dx, dz := math.Cos(a), math.Sin(a)
			r0 := vt.Radius * v.Crater * 1.2
			out = append(out, lavaLobe{x: vt.X + dx*r0, z: vt.Z + dz*r0, dx: dx, dz: dz,
				length:    v.LobeLength * (0.6 + 0.8*rng.Float64()),
				halfWidth: math.Max(v.LobeWidth, 2) / 2, thickness: v.LobeThickness * (0.75 + 0.5*rng.Float64()),
				seed: noise.Hash64(s.Seed, 0x4C4F_4245, uint64(i), uint64(k))})
		}
	}
	return out
}

// volcano is the cones' and lobes' rise at (x, z).
func (ls *landformShaper) volcano(x, z float64) float64 {
	v := &ls.lf.Volcano
	best := 0.0
	cr := noise.Clamp(v.Crater, 0, 0.6)
	for _, vt := range ls.vents {
		dx, dz := x-vt.X, z-vt.Z
		r := math.Hypot(dx, dz)
		if r >= vt.Radius*1.15 {
			continue
		}
		ang := math.Atan2(dz, dx)
		// A lumpy outline and gullied flanks: ridged noise around the cone.
		u := r / vt.Radius * (1 + 0.08*ls.flank.At2(math.Cos(ang)*3+vt.X*0.01, math.Sin(ang)*3))
		if u >= 1 {
			continue
		}
		cone := math.Pow(1-u, 1.6)
		h := v.Height * vt.Radius / v.Radius * cone
		if cr > 0 && u < cr {
			// The crater: a bowl from the rim down CraterDepth.
			rim := v.Height * vt.Radius / v.Radius * math.Pow(1-cr, 1.6)
			q := u / cr
			h = rim - v.CraterDepth*(1-q*q)
		} else {
			h *= 1 + 0.05*ls.flank.At2(ang*6/math.Pi, r/25)
		}
		best = math.Max(best, h)
	}
	lava := 0.0
	for i := range ls.lobes {
		lava = math.Max(lava, ls.lobes[i].at(x, z))
	}
	return best + lava
}

// at is the lobe's thickness at (x, z): a flat-topped tongue with steep
// lobate margins and a steep, rounded front.
func (l *lavaLobe) at(x, z float64) float64 {
	return l.thickness * lobeShape(x-l.x, z-l.z, l.dx, l.dz, l.length, l.halfWidth, l.seed)
}

// lobeShape is a lobe's outline at (px, pz) from its start, 0..1 (1 on its
// flat top): what lavaLobe.at scales by its thickness, and what
// LobeMeta.Cover answers with, so the ground material's lava rule lies on
// exactly the tongues the heightfield raised.
func lobeShape(px, pz, dx, dz, length, halfWidth float64, seed uint64) float64 {
	l := struct {
		dx, dz, length, halfWidth float64
		seed                      uint64
	}{dx, dz, length, halfWidth, seed}
	a := px*l.dx + pz*l.dz
	if a < -l.halfWidth || a > l.length+l.halfWidth {
		return 0
	}
	b := -px*l.dz + pz*l.dx
	q := noise.Clamp(a/l.length, 0, 1)
	// The tongue wanders and its edge is lobate.
	b -= l.halfWidth * 1.2 * noise.FBM2(l.seed, q*3, 0.5, 2, 0.5)
	w := l.halfWidth * (0.55 + 0.6*math.Sqrt(math.Sin(math.Pi*math.Min(q*1.1, 1)))) * (1 + 0.25*noise.FBM2(l.seed+1, a/30, b/30, 2, 0.5))
	if w <= 0 {
		return 0
	}
	side := noise.SmoothStep(1, 0.8, math.Abs(b)/w)
	front := noise.SmoothStep(l.length+0.6*l.halfWidth, l.length-0.4*l.halfWidth, a+0.4*l.halfWidth*noise.FBM2(l.seed+2, b/20, 0, 2, 0.5))
	back := noise.SmoothStep(-l.halfWidth, 0, a)
	return side * front * back * (0.9 + 0.1*noise.FBM2(l.seed+3, a/12, b/12, 2, 0.5))
}

// LobeMeta is one lava lobe in WORLD coordinates (terrain.yaml `lobes:`):
// its start on the cone's flank, unit direction, length, half width and
// outline seed — enough to answer its outline (Cover) as the heightfield
// shaped it.
type LobeMeta struct {
	X         float64 `yaml:"x"`
	Z         float64 `yaml:"z"`
	DX        float64 `yaml:"dx"`
	DZ        float64 `yaml:"dz"`
	Length    float64 `yaml:"length"`
	HalfWidth float64 `yaml:"half_width"`
	Seed      uint64  `yaml:"seed"`
}

// Cover is the lobe's outline at world (x, z), 0..1 (1 on its top).
func (l *LobeMeta) Cover(x, z float64) float64 {
	px, pz := x-l.X, z-l.Z
	// Cheap reject: past the lobe's length and its widest wander.
	if a := px*l.DX + pz*l.DZ; a < -l.HalfWidth || a > l.Length+l.HalfWidth {
		return 0
	}
	if b := -px*l.DZ + pz*l.DX; math.Abs(b) > 4*l.HalfWidth {
		return 0
	}
	return lobeShape(px, pz, l.DX, l.DZ, l.Length, l.HalfWidth, l.Seed)
}

// Lobes is s's lava lobes in WORLD coordinates for a provider anchored at
// anchor measured over the world rectangle over (as Vents); nil without
// volcanoes or lobes.
func Lobes(s *Settings, over Rect, anchor [2]float64) []LobeMeta {
	if !s.Landform.volcanoOn() {
		return nil
	}
	local := Rect{MinX: over.MinX - anchor[0], MinZ: over.MinZ - anchor[1], MaxX: over.MaxX - anchor[0], MaxZ: over.MaxZ - anchor[1]}
	var out []LobeMeta
	for _, l := range lavaLobes(s, placeVents(s, local)) {
		out = append(out, LobeMeta{X: l.x + anchor[0], Z: l.z + anchor[1], DX: l.dx, DZ: l.dz, Length: l.length,
			HalfWidth: l.halfWidth, Seed: l.seed})
	}
	return out
}

// coastDist is the signed distance from the shoreline at (x, z): positive
// inland, negative out to sea.
func (ls *landformShaper) coastDist(x, z float64) float64 {
	c := &ls.lf.Coast
	if ls.coastLine != nil {
		return lineSide(ls.coastLine, x, z) + c.Meander*ls.coastBend.At2(x, z)
	}
	pos := x
	if ls.coastAxis == 1 {
		pos = z
	}
	return ls.coastSign*(ls.coastShoreAt-pos) + c.Meander*ls.coastBend.At2(x, z)
}

// coast re-profiles h by the distance from the shoreline (file comment,
// step 5).
func (ls *landformShaper) coast(x, z, h float64) float64 {
	c := &ls.lf.Coast
	d := ls.coastDist(x, z)
	if d < 0 {
		// The shelf: down to Depth below the sea over Shelf metres, a little
		// uneven.
		fl := noise.SmoothStep(0, math.Max(c.Shelf, 1), -d)
		return c.SeaLevel - math.Max(c.Depth, 0)*fl - 0.4*fl*(ls.coastFloor.At2(x, z)+1)
	}
	beach := math.Max(c.Beach, 0)
	rise := 0.0
	if beach > 0 {
		rise = c.BeachRise * math.Min(d/beach, 1)
	}
	end := beach
	if c.Cliff > 0 {
		cw := math.Max(c.CliffWidth, 1)
		rise += c.Cliff * noise.SmoothStep(beach, beach+cw, d)
		end += cw
	}
	land := noise.SmoothStep(end, end+math.Max(c.Inland, 1), d)
	return c.SeaLevel + rise + land*(h-ls.base)
}

// lineSide is the signed distance from polyline pts to (x, z): positive
// on its left (inland), negative on its right (the sea), taken from the
// nearest segment; past the ends the end segments run on.
func lineSide(pts [][2]float64, x, z float64) float64 {
	best := math.Inf(1)
	sign := 1.0
	n := len(pts)
	for k := 0; k+1 < n; k++ {
		ax, az := pts[k][0], pts[k][1]
		bx, bz := pts[k+1][0], pts[k+1][1]
		ex, ez := bx-ax, bz-az
		l2 := ex*ex + ez*ez
		if l2 < 1e-12 {
			continue
		}
		t := ((x-ax)*ex + (z-az)*ez) / l2
		lo, hi := 0.0, 1.0
		if k == 0 {
			lo = math.Inf(-1)
		}
		if k == n-2 {
			hi = math.Inf(1)
		}
		t = math.Min(math.Max(t, lo), hi)
		px, pz := ax+t*ex, az+t*ez
		d := math.Hypot(x-px, z-pz)
		if d < best {
			best = d
			// Left of the direction of travel (cross product > 0 with +Z
			// down the screen, X right) is inland.
			cr := ex*(z-az) - ez*(x-ax)
			sign = 1
			if cr > 0 {
				sign = -1
			}
		}
	}
	return sign * best
}

// Vents is s's volcanic vents in WORLD coordinates for a provider anchored
// at anchor whose mesa field is measured over the world rectangle over
// (the classic terrain: CentredSquare(Size), anchor zero); nil when it has
// no volcanoes.
func Vents(s *Settings, over Rect, anchor [2]float64) []Vent {
	if !s.Landform.volcanoOn() {
		return nil
	}
	local := Rect{MinX: over.MinX - anchor[0], MinZ: over.MinZ - anchor[1], MaxX: over.MaxX - anchor[0], MaxZ: over.MaxZ - anchor[1]}
	vs := placeVents(s, local)
	for i := range vs {
		vs[i].X += anchor[0]
		vs[i].Z += anchor[1]
	}
	return vs
}

// quantile2 is the q-quantile (0..1) of f sampled on a 64² lattice over r.
func quantile2(f func(x, z float64) float64, r Rect, q float64) float64 {
	const k = 64
	vals := make([]float64, 0, k*k)
	w, h := r.MaxX-r.MinX, r.MaxZ-r.MinZ
	for j := 0; j < k; j++ {
		for i := 0; i < k; i++ {
			vals = append(vals, f(r.MinX+w*(float64(i)+0.5)/k, r.MinZ+h*(float64(j)+0.5)/k))
		}
	}
	sort.Float64s(vals)
	return vals[min(max(int(q*float64(len(vals))), 0), len(vals)-1)]
}
