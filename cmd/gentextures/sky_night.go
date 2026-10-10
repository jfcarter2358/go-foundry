package main

// sky_night.go — a burning city at night in the rain, for level3.
//
// A low, heavy overcast covers the whole dome. Its zenith is a very dark
// blue-grey; towards the horizon the cloud base is lit from underneath a dull
// sodium orange-brown by the city's streetlights and fires. Round the whole
// horizon runs a skyline silhouette — two rings of blocky buildings, the far one
// hazed into the glow — with a handful of distant fires glowing among it and
// lighting the smoke and cloud above them. Below the horizon it is dark ground.
//
// Everything is a function of direction alone, for the reason sky.go gives, so
// the six faces meet with no seams:
//
//   The skyline is a ring of buildings indexed by azimuth quantised into a whole
//   number of buildings per turn, so building N-1 wraps into building 0 with no
//   seam where atan2 does. Heights come from skyHash of the index.
//
//   Fires are points in (azimuth, elevation); their distance is measured with a
//   wrapped azimuth difference, so one sitting on the atan2 cut is not halved.
//
//   The cloud deck is skyCloudPlane's flat deck, noise sampled in its plane.

import "math"

var (
	nightZenith   = skyVec{0.030, 0.036, 0.048}
	nightCloudLo  = skyVec{0.050, 0.054, 0.064} // cloud base in no light but its own
	nightSodium   = skyVec{0.50, 0.28, 0.12}    // the city's light on the cloud base
	nightFire     = skyVec{1.00, 0.46, 0.12}
	nightFireCore = skyVec{1.00, 0.78, 0.42}
	nightBuilding = skyVec{0.016, 0.016, 0.020}
	nightGround   = skyVec{0.018, 0.017, 0.018}
)

// nightFirePt is one distant fire: where it sits and how big and bright.
type nightFirePt struct {
	az, elev, size, heat float64
}

// nightFires are the burning buildings on the skyline. Azimuths in radians.
var nightFires = []nightFirePt{
	{az: 0.35, elev: 0.004, size: 1.0, heat: 1.0},
	{az: 1.42, elev: 0.010, size: 0.6, heat: 0.7},
	{az: 2.30, elev: 0.002, size: 1.3, heat: 1.1},
	{az: -2.95, elev: 0.006, size: 0.7, heat: 0.8},
	{az: -1.80, elev: 0.003, size: 1.1, heat: 0.9},
	{az: -0.72, elev: 0.012, size: 0.5, heat: 0.6},
	{az: 3.05, elev: 0.008, size: 0.5, heat: 0.6},
}

// nightWrap folds an azimuth difference into -pi..pi.
func nightWrap(a float64) float64 {
	a = math.Mod(a+math.Pi, 2*math.Pi)
	if a < 0 {
		a += 2 * math.Pi
	}
	return a - math.Pi
}

// nightSkyline is the elevation (radians) of a building ring's roofline at
// azimuth az: count buildings per turn, heights between lo and hi, a few taller
// towers, and roofs with small steps so they are not all flat slabs.
func nightSkyline(az float64, count int, lo, hi float64, seed int) float64 {
	f := (az + math.Pi) / (2 * math.Pi) * float64(count)
	i := int(math.Floor(f))
	fr := f - math.Floor(f)
	idx := ((i % count) + count) % count
	h := lo + (hi-lo)*math.Pow(skyHash(idx, seed), 1.6)
	// Towers: one building in eight or so stands well above the rest.
	if skyHash(idx, seed+17) > 0.88 {
		h += (hi - lo) * (0.8 + 1.2*skyHash(idx, seed+23))
		// with a narrower top storey
		if fr < 0.2 || fr > 0.8 {
			h -= (hi - lo) * 0.35
		}
	}
	// A setback or roof plant on some.
	if skyHash(idx, seed+5) > 0.6 && fr > 0.55 && fr < 0.85 {
		h += (hi - lo) * 0.12
	}
	// Some gaps (streets): a building one in six is much lower.
	if skyHash(idx, seed+41) > 0.83 {
		h = lo * 0.4
	}
	return h
}

// nightFireGlow is the fires' light at (az, elev): the halo on the air and
// cloud, and a hot core at the fire itself.
func nightFireGlow(az, elev float64) (halo, core float64) {
	for _, f := range nightFires {
		da := nightWrap(az - f.az)
		de := elev - f.elev
		// The halo is wide and reaches higher than it spreads sideways: the
		// fire lights its own smoke and the cloud straight above it.
		r2 := (da*da)/(0.10*0.10*f.size) + (de*de)/(0.14*0.14*f.size)
		halo += f.heat * math.Exp(-r2)
		// The core: a flickering band hugging the roofline.
		flick := 0.6 + 0.4*skyFbm(da*90+f.az*13, de*60, 3)
		c2 := (da*da)/(0.035*0.035*f.size) + (de*de)/(0.022*0.022*f.size)
		core += f.heat * flick * math.Exp(-c2)
	}
	return
}

// nightColor is the whole night sky for one direction.
func nightColor(d skyVec) skyVec {
	d = d.unit()
	horiz := math.Hypot(d.X, d.Z)
	elev := math.Atan2(d.Y, horiz)
	az := math.Atan2(d.Z, d.X)
	e := math.Max(elev, 0)

	halo, core := nightFireGlow(az, elev)

	// The city's glow on the cloud base: strongest at the horizon, reaching a
	// fair way up, and uneven round the ring (some districts are dark, some
	// burning). The unevenness is noise sampled round a circle so it wraps.
	ring := skyFbm(math.Cos(az)*1.6+7.3, math.Sin(az)*1.6-2.1, 3)
	cityGlow := (0.55 + 0.9*ring) * math.Exp(-e/0.30)
	glow := cityGlow + 0.9*halo

	// ---- sky / cloud deck above the skyline ----
	sky := skyMix(nightZenith, nightCloudLo, math.Exp(-e/0.5))
	if d.Y > 0 {
		u, v := skyCloudPlane(d, 1.0)
		// Two scales: big rolls in the overcast and their ragged texture. The
		// lit sample a little towards the horizon gives the rolls a lit underside.
		n := skyFbm(u*0.35+3.1, v*0.35-8.4, 6)
		rag := skyFbm(u*1.4-12.0, v*1.4+5.5, 4)
		dens := skySmoothstep(0.25, 0.75, n*0.75+rag*0.25)
		// Brightness of the deck: the underside of a thicker roll is darker in
		// the middle and lit at its edge, as rain cloud is.
		shade := 0.55 + 0.9*dens
		sky = sky.scale(shade)
		lit := nightSodium.scale(glow * (0.45 + 0.75*dens))
		sky = sky.add(lit)
		// Rain curtains: near the horizon the air thickens and softens the
		// cloud's structure into the glow.
		haze := math.Exp(-e / 0.06)
		flat := nightCloudLo.add(nightSodium.scale(glow * 0.8))
		sky = skyMix(sky, flat, haze*0.7)
	} else {
		sky = nightCloudLo.add(nightSodium.scale(glow * 0.8))
	}

	// ---- skyline ----
	// Far ring first (lower, hazier), then the near ring in front of it.
	far := nightSkyline(az, 220, 0.008, 0.034, 101)
	near := nightSkyline(az, 130, 0.004, 0.055, 211)

	c := sky
	if elev < far {
		// Far buildings are hazed into the glow behind them, a little lighter
		// than the near ones.
		bc := skyMix(nightBuilding, nightCloudLo.add(nightSodium.scale(glow*0.55)), 0.45)
		c = bc
	}
	if elev < near {
		bc := nightBuilding
		// Glow along the base of the silhouette where the streets burn.
		bc = bc.add(nightSodium.scale(0.05 * glow * math.Exp(-math.Max(elev+0.01, 0)/0.01)))
		// A few lit windows: a sparse grid of tiny cells on the facades.
		wx := (az + math.Pi) / (2 * math.Pi) * 2600
		wy := elev * 900
		wi, wj := int(math.Floor(wx)), int(math.Floor(wy))
		fx, fy := wx-math.Floor(wx), wy-math.Floor(wy)
		if fx > 0.25 && fx < 0.75 && fy > 0.3 && fy < 0.8 && skyHash(wi, wj+977) > 0.985 && elev > 0.001 && elev < near-0.002 {
			warm := skyHash(wi+3, wj)
			bc = bc.add(skyMix(skyVec{0.30, 0.20, 0.10}, skyVec{0.12, 0.16, 0.22}, warm).scale(0.6))
		}
		c = bc
	}
	if elev < math.Min(far, near) && elev < 0 {
		// Below the horizon: dark streets and rubble, fading to ground.
		g := skyMix(c, nightGround, skySmoothstep(0, 0.12, -elev))
		c = g.add(nightSodium.scale(0.10 * glow * math.Exp(elev/0.02)))
	}

	// ---- fires ----
	// The core burns in front of the far ring and just over the near roofs;
	// the silhouettes cut it at the bottom, which is what makes it sit IN the
	// city. Above the roofline the core rises into lit smoke.
	if core > 0.001 {
		cut := 1.0
		if elev < near-0.004 {
			cut = 0.15 // behind a near building: only a glint through it
		}
		heat := skyClamp(core*cut, 0, 1.6)
		fc := skyMix(nightFire, nightFireCore, skySmoothstep(0.6, 1.4, heat))
		c = c.add(fc.scale(heat * 0.9))
	}
	// The fire halo also lights the air in front of the silhouettes a touch,
	// as rain does.
	c = c.add(nightFire.scale(0.03 * halo))

	return c
}

// genSkyNight writes level3's burning-city night skybox.
func genSkyNight() { genSkyCube("sky_night", nightColor) }
