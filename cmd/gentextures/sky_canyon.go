package main

// sky_canyon.go — the daylight sky over canyon country: sky_day above the
// horizon, and below it, instead of the haze-to-ground gradient, a granite
// plateau cut by canyons with mesas and ranges standing on it out to the haze.
//
// It is made for level1, whose canyon walls end in rock with nothing past it.
// From the dropship coming down, what lies past those rim tops is this sky, so
// the plateau is painted from the canyon's own materials (cn_cliff's grey
// granite, cn_grass's moss green, cn_dirt's tan). Its palette stops are those
// textures' average colours, darkened a little for being seen from far off, and
// they are what let the rim tops run on into the ground below the horizon
// without a visible seam.
//
// Above the horizon it IS skyColor: same gradient, sun, glow and cloud deck, so
// a script can ease between sky_day and this one and only the ground changes.
//
// The geometry is analytic, and every piece is a function of direction alone,
// for the reason sky.go gives:
//
//   The plateau is a ray-plane hit a nominal canyonEyeHeight under the eye. A
//   skybox is at infinity and has no parallax, so one height has to serve every
//   eye; the one picked is roughly where the dropship is while the ground is
//   first in view.
//
//   Mesas and ranges are rings of height profiles at fixed distances, nearest
//   first. The profile is noise sampled around a circle in azimuth, so it wraps
//   with no seam, and a nearer ring hides a farther one. That is the painter's
//   algorithm, and it costs a handful of noise calls a texel rather than a
//   heightfield march.
//
//   Distance becomes haze toward skyColor's own colour at the horizon, glow and
//   all. Far rock then takes the sky's warmth on the sun side and its cold on
//   the other, which is what joins the ranges to the sky.

import "math"

const (
	// canyonEyeHeight is how far above the plateau the sky is painted as seen
	// from, in metres.
	canyonEyeHeight = 45.0

	// canyonHaze is the distance over which the air hides about 63% of the
	// ground (1 - 1/e). It is short enough that the farthest range is mostly
	// sky-coloured, which is what makes it read as far.
	canyonHaze = 4200.0

	// canyonTexelAngle is roughly the angle one supersample covers, in radians
	// (a quarter turn across skyFaceSize texels, halved again by skySamples).
	// Noise octaves finer than the ground that angle spans at a hit are faded
	// out, or distant ground aliases into speckle.
	canyonTexelAngle = (math.Pi / 2) / skyFaceSize / skySamples
)

var (
	// Average colours of the level's canyon textures, a little darkened and
	// desaturated for distance.
	canyonGranite   = skyVec{0.43, 0.45, 0.47}
	canyonGraniteLo = skyVec{0.33, 0.35, 0.37}
	canyonMoss      = skyVec{0.22, 0.31, 0.13}
	canyonGrass     = skyVec{0.26, 0.37, 0.14}
	canyonDirt      = skyVec{0.43, 0.40, 0.35}
	// Canyon floors and cracks in shade: the colour cn_cliff's joints are
	// painted, taken bluer for sky fill.
	canyonShadow = skyVec{0.16, 0.18, 0.21}
	// Sky light on a face turned away from the sun. It is what stops a shaded
	// cliff from going black.
	canyonAmbient = skyVec{0.50, 0.56, 0.66}
)

// canyonRing is one ring of landforms at a fixed distance from the eye.
type canyonRing struct {
	dist float64 // metres
	// base and relief are metres above the plateau: the profile runs from base
	// to base+relief.
	base, relief float64
	// feature is roughly how wide one landform is, in metres of arc.
	feature float64
	// mesa squares the profile off into flat tops with steep sides, the way the
	// canyon's own rims are; 0 leaves it as rounded ridges and peaks.
	mesa float64
	// fill is how much of the ring stands at all: below it the profile drops
	// under the plateau, leaving a gap to see the next ring through.
	fill float64
	seed float64
}

// canyonRings are nearest first, which is the order they hide each other in.
var canyonRings = []canyonRing{
	// Buttes and rims a few hundred metres off, about the canyon's own height.
	{dist: 520, base: -6, relief: 34, feature: 140, mesa: 1.0, fill: 0.46, seed: 11.3},
	{dist: 1150, base: -8, relief: 70, feature: 260, mesa: 0.9, fill: 0.52, seed: 27.9},
	// A broken line of taller mesas.
	{dist: 2400, base: 10, relief: 150, feature: 480, mesa: 0.7, fill: 0.58, seed: 43.1},
	// Foothills, then the far range, peaked rather than flat.
	{dist: 5200, base: 60, relief: 380, feature: 900, mesa: 0.25, fill: 0.70, seed: 71.7},
	{dist: 11000, base: 250, relief: 1100, feature: 1700, mesa: 0.0, fill: 1.1, seed: 97.2},
}

// canyonFbm is skyFbm with every octave finer than footprint (in the same
// units as x, y) faded out.
func canyonFbm(x, y float64, octaves int, footprint float64) float64 {
	sum, amp, norm, wave := 0.0, 0.5, 0.0, 1.0
	for o := 0; o < octaves; o++ {
		// Replaced by its own average (0.5) as the wavelength shrinks past two
		// footprints, rather than dropped, so fading does not shift the mean.
		w := skySmoothstep(footprint, 2.5*footprint, wave)
		sum += amp * (0.5 + w*(skyValueNoise(x, y)-0.5))
		norm += amp
		amp *= 0.5
		wave *= 0.5
		x, y = (x*0.8-y*0.6)*2.02, (x*0.6+y*0.8)*2.02
	}
	return sum / norm
}

// canyonHorizonHaze is the colour the air fades distant ground towards: the
// sky's own colour just above the horizon in this direction, so the haze
// carries the sun glow round with it.
func canyonHorizonHaze(d skyVec) skyVec {
	h := skyVec{d.X, 0, d.Z}.unit()
	h.Y = 0.02
	return skyColorNoCloud(h.unit())
}

// canyonAerial fades a lit surface colour into the haze by distance.
func canyonAerial(c skyVec, d skyVec, dist float64) skyVec {
	return skyMix(c, canyonHorizonHaze(d), 1-math.Exp(-dist/canyonHaze))
}

// canyonProfile is ring r's height above the plateau at azimuth a, in metres.
func canyonProfile(r *canyonRing, a float64) float64 {
	// Noise sampled around a circle whose circumference, in noise units, is the
	// ring's own circumference in landforms, so the profile wraps with no seam
	// and a landform is feature metres wide wherever it is.
	rad := r.dist / r.feature
	x, y := math.Cos(a)*rad+r.seed, math.Sin(a)*rad-r.seed*0.7
	n := skyFbm(x, y, 5)
	// Where the ring stands at all, and how high.
	stand := skySmoothstep(1-r.fill-0.08, 1-r.fill+0.08, skyValueNoise(x*0.35+5.1, y*0.35-2.3)*0.6+n*0.4)
	h := skySmoothstep(0.30, 0.72, n)
	// Mesa: push the profile towards a few flat steps with steep risers.
	if r.mesa > 0 {
		steps := 3.0
		f := h * steps
		fl := math.Floor(f)
		terr := (fl + skySmoothstep(0.35, 0.65, f-fl)) / steps
		h = h + (terr-h)*r.mesa
	}
	return r.base + r.relief*h*stand - (1-stand)*(r.base+20)
}

// canyonRingColor shades a hit on ring r at azimuth a, height y above the
// plateau.
func canyonRingColor(r *canyonRing, d skyVec, a, y, top float64) skyVec {
	// The profile's slope along the ring picks which way a face is turned:
	// rising to the right faces left, and so on. Seen from the front, a cliff
	// face's normal is the reverse view direction swung round by that slope.
	// Differenced across a good fraction of a landform, so the lighting follows
	// the landform's shape and not the profile's finest octave, which lit column
	// by column reads as vertical stripes.
	da := 0.12 * r.feature / r.dist
	slope := (canyonProfile(r, a+da) - canyonProfile(r, a-da)) / (2 * da * r.dist)
	back := skyVec{-d.X, 0, -d.Z}.unit()
	side := skyVec{-back.Z, 0, back.X}
	n := back.add(side.scale(-skyClamp(slope, -3, 3) * 0.6)).unit()
	// Near the top edge the face rolls back into the cap.
	capT := skySmoothstep(r.relief*0.10, 0, top-y)
	n = skyMix(n, skyVec{0, 1, 0}, capT*0.8).unit()

	sun := skyClamp(n.dot(skySunDir), 0, 1)

	// Position along the ring, as a point on its circle rather than as arc
	// length, which would jump where the azimuth wraps.
	cx, cz := math.Cos(a)*r.dist, math.Sin(a)*r.dist
	fp := canyonTexelAngle * r.dist
	// Strata: horizontal banding in height, broken by gullies. The gullies
	// meander with height, so they read as erosion rather than as stripes.
	strata := canyonFbm(y*0.09+r.seed+cx*0.003, cz*0.003, 4, fp*0.09)
	gully := canyonFbm(cx*0.012+r.seed*3+(strata-0.5)*1.5, cz*0.012+y*0.006, 4, fp*0.012)
	albedo := skyMix(canyonGraniteLo, canyonGranite, skySmoothstep(0.3, 0.7, strata))
	albedo = skyMix(albedo, canyonDirt, 0.35*skySmoothstep(0.55, 0.8, canyonFbm(cx*0.008-r.seed, cz*0.008+y*0.01, 3, fp*0.008)))
	albedo = albedo.scale(0.88 + 0.22*gully)
	// Moss and grass on the caps, as on the canyon's own rim tops.
	albedo = skyMix(albedo, canyonMoss, capT*0.7)

	lit := skyVec{
		albedo.X * (canyonAmbient.X*0.35 + skySunColor.X*sun*1.15),
		albedo.Y * (canyonAmbient.Y*0.35 + skySunColor.Y*sun*1.15),
		albedo.Z * (canyonAmbient.Z*0.35 + skySunColor.Z*sun*1.15),
	}
	// The ring's foot sits in the shadow of the ground in front of it.
	lit = skyMix(lit, canyonShadow, 0.35*skySmoothstep(r.relief*0.25, 0, y-math.Min(r.base, 0)))
	return canyonAerial(lit, d, r.dist)
}

// canyonGroundHeight is the plateau's relief at (x, z), in metres, and canyon
// is 1 inside a canyon cut and 0 on open plateau.
func canyonGroundHeight(x, z, fp float64) (height, canyon float64) {
	// Canyons: the zero lines of a noise field, widened. Two scales, so there
	// are a few big winding ones and more small side cuts. The domain is warped
	// first so they wander and branch instead of looping like contour lines.
	wx := x + 90*(canyonFbm(x*0.003+17, z*0.003, 3, fp*0.003)-0.5)
	wz := z + 90*(canyonFbm(x*0.003, z*0.003-23, 3, fp*0.003)-0.5)
	c1 := math.Abs(canyonFbm(wx*0.0016+3.1, wz*0.0016-7.4, 5, fp*0.0016)-0.5) * 2
	c2 := math.Abs(canyonFbm(wx*0.0065-9.3, wz*0.0065+1.7, 4, fp*0.0065)-0.5) * 2
	// Rock grain down to a few metres, stronger on the open plateau than on
	// canyon floors, which fill with scree. It also breaks up the rim line, so
	// a canyon edge is ragged rock rather than a kerb.
	grain := canyonFbm(x*0.04+5, z*0.04-9, 5, fp*0.04)
	rag := (grain - 0.5) * 0.05
	// Steep-sided: the walls are a narrow band of the field, not a ramp.
	// Width wanders along each canyon.
	wide := 0.6 + 0.8*canyonFbm(x*0.004-31, z*0.004+8, 2, fp*0.004)
	cut1 := 1 - skySmoothstep(0.05*wide, 0.065*wide, c1+rag)
	cut2 := 1 - skySmoothstep(0.022*wide, 0.03*wide, c2+rag*0.6)
	roll := canyonFbm(x*0.004, z*0.004, 4, fp*0.004)
	cut := math.Max(cut1, cut2)
	depth := math.Max(cut1*30, cut2*14)
	return roll*12 + (grain-0.5)*6*(1-cut) - depth, cut
}

// canyonGroundColor shades the plateau where the view ray meets it.
func canyonGroundColor(d skyVec) skyVec {
	t := canyonEyeHeight / -d.Y
	x, z := d.X*t, d.Z*t
	dist := t
	// Ground a supersample covers, stretched along the view by the grazing
	// angle.
	fp := canyonTexelAngle * dist / math.Max(-d.Y, 0.02)

	h, cut := canyonGroundHeight(x, z, fp)
	// Normal from the relief. The step grows with the footprint so a distant
	// normal is an average rather than noise.
	e := math.Max(1.5, fp)
	hx, cutX := canyonGroundHeight(x+e, z, fp)
	hz, cutZ := canyonGroundHeight(x, z+e, fp)
	n := skyVec{-(hx - h) / e, 1, -(hz - h) / e}.unit()
	sun := skyClamp(n.dot(skySunDir), 0, 1)

	// Granite in strata-like mottling, as cn_cliff's tops show from above.
	rock := canyonFbm(x*0.03, z*0.03, 5, fp*0.03)
	albedo := skyMix(canyonGraniteLo, canyonGranite, skySmoothstep(0.3, 0.7, rock))
	albedo = skyMix(albedo, canyonDirt, 0.45*skySmoothstep(0.6, 0.78, canyonFbm(x*0.011-7, z*0.011+3, 4, fp*0.011)))

	// Moss in patches where the plateau is flat: rock first, as the canyon's
	// own rim tops are.
	patch := canyonFbm(x*0.005+41, z*0.005-13, 5, fp*0.005)
	flat := skySmoothstep(0.80, 0.95, n.Y)
	green := skySmoothstep(0.52, 0.64, patch+0.15*(rock-0.5)) * flat
	// Close in, the plateau stays mostly bare granite, so the level's own rock
	// rim runs straight on into it rather than meeting a lawn.
	green *= 0.25 + 0.75*skySmoothstep(120, 700, dist)
	// Canyon floors are grass between granite walls, like level1's own canyon:
	// the walls are the steep band of the cut, the floor its flat bottom.
	wall := skySmoothstep(0.02, 0.3, cut) * (1 - skySmoothstep(0.75, 0.95, cut))
	floor := skySmoothstep(0.8, 0.97, cut)
	green = math.Max(green*(1-wall), floor)
	albedo = skyMix(albedo, skyMix(canyonMoss, canyonGrass, skySmoothstep(0.4, 0.8, patch)), green*0.9)

	// Shrubs: dark clumps scattered in the green, thinning out onto the rock.
	shrub := skySmoothstep(0.64, 0.72, canyonFbm(x*0.35+71, z*0.35-3, 3, fp*0.35))
	albedo = skyMix(albedo, canyonMoss.scale(0.7), shrub*(0.1+0.3*green))

	light := skyVec{
		canyonAmbient.X*0.40 + skySunColor.X*sun*1.05,
		canyonAmbient.Y*0.40 + skySunColor.Y*sun*1.05,
		canyonAmbient.Z*0.40 + skySunColor.Z*sun*1.05,
	}
	lit := skyVec{albedo.X * light.X, albedo.Y * light.Y, albedo.Z * light.Z}
	// Canyon cuts are deep and narrow. Which wall faces the sun comes from the
	// cut's own gradient (the relief normal is too gentle to catch a wall a few
	// metres wide); the floor is half in the shade of whichever wall is not.
	// The wall faces away from where the cut deepens.
	wn := skyVec{-(cutX - cut), 0, -(cutZ - cut)}
	facing := 0.0
	if l := math.Sqrt(wn.dot(wn)); l > 1e-6 {
		facing = wn.scale(1 / l).dot(skyVec{skySunDir.X, 0, skySunDir.Z}.unit())
	}
	lit = lit.scale(1 + 0.35*facing*wall)
	lit = skyMix(lit, canyonShadow, wall*skySmoothstep(0.2, -0.6, facing)*0.8+floor*0.3)
	return canyonAerial(lit, d, dist)
}

// skyColorNoCloud is skyColor's gradient, sun glow and horizon warmth, with
// neither clouds nor the sun disc. It is what the haze on distant ground is
// made of.
func skyColorNoCloud(d skyVec) skyVec {
	e := math.Max(d.Y, 0)
	base := skyMix(skyHorizon, skyZenith, math.Pow(e, 0.42))
	ang := math.Acos(skyClamp(d.dot(skySunDir), -1, 1))
	base = base.add(skySunColor.scale(math.Exp(-ang/0.30)*0.55 + math.Exp(-ang/1.10)*0.14))
	base = base.add(skySunColor.scale(0.10 * math.Exp(-e/0.10) * math.Exp(-ang/1.6)))
	return base
}

// canyonColor is the whole canyon sky for one direction.
func canyonColor(d skyVec) skyVec {
	d = d.unit()
	horiz := math.Hypot(d.X, d.Z)
	elev := math.Atan2(d.Y, horiz)
	az := math.Atan2(d.Z, d.X)

	// Distance to the plateau along this ray, measured flat; infinite at and
	// above the horizon.
	ground := math.Inf(1)
	if d.Y < 0 {
		ground = canyonEyeHeight / math.Tan(-elev)
	}

	for i := range canyonRings {
		r := &canyonRings[i]
		if r.dist >= ground {
			break
		}
		// Height above the plateau this ray is at when it crosses the ring.
		y := canyonEyeHeight + r.dist*math.Tan(elev)
		top := canyonProfile(r, az)
		if y <= top {
			return canyonRingColor(r, d, az, y, top)
		}
	}
	if d.Y < 0 {
		return canyonGroundColor(d)
	}
	return skyColor(d)
}

// genSkyCanyon writes level1's canyon-country daylight skybox.
func genSkyCanyon() { genSkyCube("sky_canyon", canyonColor) }
