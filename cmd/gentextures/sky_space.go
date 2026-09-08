package main

// sky_space.go — the orbital skybox: stars, the galactic band, and an
// Earth-like planet filling the space below.
//
// Same construction as sky.go's daylight sky and for the same reason — one
// continuous function of a 3D direction, so the twelve cube edges agree by
// construction — but the content is analytic rather than painted twice over:
//
//   The planet is a real ray-sphere intersection, not a disc with a gradient on
//   it. That is what gives the surface its foreshortening towards the limb, and
//   it is what lets the same sun direction light the planet and cast the glow
//   around its edge without either being drawn by hand.
//
//   The stars are a 3D cell lattice sampled through the direction, which is the
//   only way to scatter points evenly over a cube map without the four side
//   faces having visibly more of them than the two poles.
//
// It shares the daylight sky's sun direction, so a level can swap between the
// two without moving its own sun: the planet's terminator and the day sky's
// shadows fall the same way.

import "math"

// ---------- placement ----------

var (
	// The planet sits straight down. "Underneath" is the whole composition:
	// the level's floor is the deck of something in orbit, and what is over the
	// railing is the planet.
	spacePlanetDir = skyVec{0, -1, 0}

	// spacePlanetRadius is the planet's angular radius, in radians. 58 degrees
	// is chosen to spill past the -Y face (whose corners reach 54.7) so the limb
	// carries onto the bottom of all four side faces — a planet that stopped
	// exactly at the down face would read as a picture on the floor.
	spacePlanetRadius = 58.0 * math.Pi / 180

	// The planet's own north, tilted well off the view axis so the ice cap
	// lands near the upper limb instead of dead centre under the camera.
	spacePlanetAxis = skyVec{0.44, 0.55, -0.71}.unit()

	// The same sun as the daylight sky — see skySunDir. Up and to the
	// west-north, which puts the planet's terminator across its lower-east
	// limb and leaves the face turned towards the level lit.
	spaceSunColor = skyVec{1.00, 0.97, 0.92}

	// Deep space is not black: a little of it is unresolved starlight, and a
	// true black background makes every gradient near the planet band.
	spaceVoid = skyVec{0.012, 0.014, 0.024}

	// The galactic band's pole. The band itself is the great circle
	// perpendicular to this, so tilting it is what runs it diagonally across
	// the faces rather than around the horizon like a fence.
	spaceBandPole = skyVec{0.36, 0.30, -0.88}.unit()
)

// ---------- 3D value noise ----------
//
// The 2D noise in sky.go works on a flat cloud deck; nothing here is flat. The
// star lattice, the nebulae and the planet's continents are all functions of a
// point on or near a sphere, and sampling 2D noise through a spherical mapping
// is exactly how a texture ends up pinched at the poles.

func skyHash3(ix, iy, iz, salt int) float64 {
	h := uint32(ix)*374761393 + uint32(iy)*668265263 + uint32(iz)*2147483647 + uint32(salt)*1274126177
	h = (h ^ (h >> 13)) * 1274126177
	h ^= h >> 16
	h = (h ^ (h >> 15)) * 2246822519
	h ^= h >> 13
	return float64(h) / float64(^uint32(0))
}

func skyValueNoise3(p skyVec, salt int) float64 {
	fx, fy, fz := math.Floor(p.X), math.Floor(p.Y), math.Floor(p.Z)
	i, j, k := int(fx), int(fy), int(fz)
	tx, ty, tz := p.X-fx, p.Y-fy, p.Z-fz
	ux := tx * tx * (3 - 2*tx)
	uy := ty * ty * (3 - 2*ty)
	uz := tz * tz * (3 - 2*tz)

	c := func(di, dj, dk int) float64 { return skyHash3(i+di, j+dj, k+dk, salt) }
	x00 := c(0, 0, 0)*(1-ux) + c(1, 0, 0)*ux
	x10 := c(0, 1, 0)*(1-ux) + c(1, 1, 0)*ux
	x01 := c(0, 0, 1)*(1-ux) + c(1, 0, 1)*ux
	x11 := c(0, 1, 1)*(1-ux) + c(1, 1, 1)*ux
	y0 := x00*(1-uy) + x10*uy
	y1 := x01*(1-uy) + x11*uy
	return y0*(1-uz) + y1*uz
}

func skyFbm3(p skyVec, octaves, salt int) float64 {
	sum, amp, norm := 0.0, 0.5, 0.0
	for o := 0; o < octaves; o++ {
		sum += amp * skyValueNoise3(p, salt)
		norm += amp
		amp *= 0.5
		p = p.scale(2.03)
		// Offset as well as scaled, so the octaves do not all share the lattice
		// point at the origin — which shows as a knot in the noise.
		p = p.add(skyVec{17.3, -9.1, 23.7})
	}
	return sum / norm
}

// skyWarp3 is a vector of three noise fields, used to push a sample point
// around before the field it feeds is evaluated. Domain warping is what turns
// smooth fbm blobs into coastlines and cloud swirls — the detail comes from the
// distortion, not from more octaves.
func skyWarp3(p skyVec, salt int) skyVec {
	return skyVec{
		skyFbm3(p, 3, salt) - 0.5,
		skyFbm3(p, 3, salt+31) - 0.5,
		skyFbm3(p, 3, salt+67) - 0.5,
	}
}

// ---------- stars ----------

// spaceStarCells is how many lattice cells span one unit of direction space.
// Every cell near the unit sphere carries at most one star, so this is the star
// count dial: the shell holds roughly 4*pi*n^2 cells.
const spaceStarCells = 34.0

// skyStars is the star field in a direction: a 3x3x3 neighbourhood of lattice
// cells, each holding one candidate star, accumulated as soft points.
//
// Soft on purpose. A star is far smaller than a texel at this resolution, and a
// hard point would alias into a crawling speckle the moment the camera turned;
// a Gaussian a couple of texels wide resolves cleanly and still reads as a
// point of light.
func skyStars(d skyVec, bandBoost float64) skyVec {
	var out skyVec
	base := skyVec{
		math.Floor(d.X * spaceStarCells),
		math.Floor(d.Y * spaceStarCells),
		math.Floor(d.Z * spaceStarCells),
	}
	for dk := -1; dk <= 1; dk++ {
		for dj := -1; dj <= 1; dj++ {
			for di := -1; di <= 1; di++ {
				ci := int(base.X) + di
				cj := int(base.Y) + dj
				ck := int(base.Z) + dk

				// Most cells hold nothing: thinning here is cheaper than
				// drawing thousands of stars too faint to see, and it is what
				// leaves the sky with dark gaps between them.
				keep := skyHash3(ci, cj, ck, 5)
				if keep > 0.34+0.5*bandBoost {
					continue
				}

				star := skyVec{
					float64(ci) + skyHash3(ci, cj, ck, 1),
					float64(cj) + skyHash3(ci, cj, ck, 2),
					float64(ck) + skyHash3(ci, cj, ck, 3),
				}.scale(1 / spaceStarCells).unit()

				cosA := skyClamp(d.dot(star), -1, 1)
				if cosA < 0.99985 { // ~1 degree; beyond that the Gaussian is nothing
					continue
				}
				ang := math.Acos(cosA)

				// Magnitude: the sixth power is what makes most stars faint and
				// a handful bright, rather than a uniform spray of mid-greys.
				mag := math.Pow(skyHash3(ci, cj, ck, 4), 6)
				// Never narrower than about a texel at this face resolution: a
				// sub-texel Gaussian is reconstructed differently by the two
				// grids either side of a cube edge, and shimmers as the camera
				// turns rather than sitting still like a star.
				sigma := 0.0027 + 0.0022*mag
				core := math.Exp(-(ang * ang) / (2 * sigma * sigma))
				// A second, much wider and weaker lobe: the bloom that makes
				// the bright ones look bright instead of merely larger.
				halo := 0.10 * mag * math.Exp(-(ang*ang)/(2*(4*sigma)*(4*sigma)))

				// Colour by temperature — blue-white through white to amber.
				temp := skyHash3(ci, cj, ck, 7)
				var tint skyVec
				if temp < 0.5 {
					tint = skyMix(skyVec{0.72, 0.82, 1.00}, skyVec{1, 1, 1}, temp*2)
				} else {
					tint = skyMix(skyVec{1, 1, 1}, skyVec{1.00, 0.80, 0.60}, (temp-0.5)*2)
				}

				out = out.add(tint.scale((core + halo) * (0.25 + 1.15*mag)))
			}
		}
	}
	return out
}

// ---------- the galaxy ----------

// skyGalaxy is the milky band and the faint nebulae in it: a broad glow along
// one great circle, structured by noise and cut by dust lanes.
func skyGalaxy(d skyVec) (skyVec, float64) {
	// Distance from the band's plane, as a Gaussian across it.
	t := d.dot(spaceBandPole)
	band := math.Exp(-(t * t) / (2 * 0.20 * 0.20))
	if band < 0.002 {
		return skyVec{}, 0
	}

	// Two scales of structure: the large one breaks the band into clumps, the
	// small one gives it grain. Warped, so the clumps stretch along the band
	// instead of sitting in it like beads.
	w := skyWarp3(d.scale(2.2), 11).scale(0.45)
	clump := skyFbm3(d.scale(2.6).add(w), 5, 13)
	grain := skyFbm3(d.scale(9.0).add(w), 4, 17)
	glow := band * skyClamp(0.35+1.5*(clump-0.42), 0, 1) * (0.65 + 0.5*grain)

	// Dust lanes: dark, and cutting across the band rather than along it, which
	// is what stops the whole thing reading as an airbrushed stripe.
	dust := skySmoothstep(0.42, 0.70, skyFbm3(d.scale(3.4).add(skyVec{5, 5, 5}), 5, 23))
	glow *= 1 - 0.78*dust*band

	// The band's own colour, cool in the thin parts and warmer in the core.
	col := skyMix(skyVec{0.42, 0.50, 0.72}, skyVec{0.86, 0.82, 0.70}, skyClamp(glow*1.4, 0, 1))

	// A couple of faint emission nebulae, tinted away from the band's own
	// colour so they read as something else rather than as brighter galaxy.
	neb := skySmoothstep(0.58, 0.86, skyFbm3(d.scale(1.7).add(skyVec{31, 12, 7}), 4, 29)) * band
	col = col.add(skyVec{0.55, 0.18, 0.42}.scale(neb * 0.55))
	neb2 := skySmoothstep(0.62, 0.90, skyFbm3(d.scale(2.1).add(skyVec{2, 44, 19}), 4, 37)) * band
	col = col.add(skyVec{0.10, 0.38, 0.44}.scale(neb2 * 0.5))

	return col.scale(glow * 0.42), band
}

// ---------- the planet ----------

// spacePlanetSurface is the albedo at a point on the planet, given the surface
// normal (which, for a unit sphere, IS the point).
func spacePlanetSurface(n skyVec) (albedo skyVec, ocean, land float64) {
	lat := n.dot(spacePlanetAxis) // -1..1, the planet's own latitude

	// Continents. The warp is doing the real work: fbm alone gives round blobs,
	// and it is the distortion that turns their edges into coastlines.
	w := skyWarp3(n.scale(4.4), 101).scale(0.5)
	h := skyFbm3(n.scale(3.1).add(w), 7, 103)
	// Push the poles up a little so the ice caps sit on land rather than on
	// open water, and squeeze the equator down so the oceans join up.
	h += 0.05 * (lat * lat)

	const seaLevel = 0.585
	if h < seaLevel {
		// Ocean, deepening away from the shelf.
		depth := skySmoothstep(seaLevel, seaLevel-0.13, h)
		albedo = skyMix(skyVec{0.07, 0.24, 0.42}, skyVec{0.015, 0.055, 0.20}, depth)
		ocean = 1
	} else {
		e := skySmoothstep(seaLevel, seaLevel+0.22, h) // 0 at the coast, 1 inland/high
		// Biome by latitude: green in the temperate belts, sand across the two
		// desert bands, tundra towards the poles.
		desert := skySmoothstep(0.16, 0.30, math.Abs(lat)) * (1 - skySmoothstep(0.34, 0.52, math.Abs(lat)))
		green := skyVec{0.13, 0.28, 0.11}
		dry := skyVec{0.55, 0.46, 0.29}
		rock := skyVec{0.36, 0.33, 0.28}
		// A second field breaks each belt up, so no biome is a clean stripe.
		v := skyFbm3(n.scale(7.5).add(skyVec{60, 3, 21}), 4, 107)
		albedo = skyMix(green, dry, skyClamp(desert*1.2+0.35*(v-0.5), 0, 1))
		albedo = skyMix(albedo, rock, skySmoothstep(0.45, 0.95, e))
		land = 1
	}

	// Ice: the caps, plus snow creeping down onto high ground.
	ice := skySmoothstep(0.74, 0.88, math.Abs(lat)+0.10*(skyFbm3(n.scale(6), 3, 109)-0.5))
	if land > 0 {
		ice = math.Max(ice, skySmoothstep(0.55, 0.80, math.Abs(lat))*skySmoothstep(0.62, 0.95, h))
	}
	albedo = skyMix(albedo, skyVec{0.90, 0.93, 0.97}, ice)
	return albedo, ocean * (1 - ice), land
}

// spacePlanetClouds is the cloud deck's coverage at a surface point. The shear
// about the planet's axis is what banks the systems into bands and spirals
// instead of leaving them as isotropic blobs.
func spacePlanetClouds(n skyVec) float64 {
	lat := n.dot(spacePlanetAxis)
	// Rotate about the axis by an amount that varies with latitude — a cheap
	// stand-in for differential rotation, and the whole reason the deck reads
	// as weather.
	ang := 2.4 * (1 - lat*lat) * (0.5 + 0.5*lat)
	c, s := math.Cos(ang), math.Sin(ang)
	a := spacePlanetAxis
	// Rodrigues' rotation of n about a by ang.
	cross := skyVec{a.Y*n.Z - a.Z*n.Y, a.Z*n.X - a.X*n.Z, a.X*n.Y - a.Y*n.X}
	p := n.scale(c).add(cross.scale(s)).add(a.scale(a.dot(n) * (1 - c)))

	w := skyWarp3(p.scale(5.5), 211).scale(0.55)
	f := skyFbm3(p.scale(4.6).add(w), 7, 213)
	cov := skySmoothstep(0.47, 0.69, f)
	// A thinner, higher sheet so the deck has two levels to it.
	cov = math.Max(cov, 0.45*skySmoothstep(0.58, 0.86, skyFbm3(p.scale(9.5).add(w), 5, 217)))
	// Storm-free belt either side of the equator, and heavier cloud in the
	// mid-latitudes, which is roughly what a rotating atmosphere does.
	cov *= 0.55 + 0.75*skySmoothstep(0.02, 0.35, math.Abs(lat))
	return skyClamp(cov, 0, 1)
}

// spacePlanet shades the planet in a direction, returning its colour and how
// much of the sky it covers there — 0 well outside the disc, 1 on it, and a
// soft edge between, which is what antialiases the limb beyond what
// supersampling alone manages against a near-black background.
func spacePlanet(d skyVec, sunDir skyVec) (skyVec, float64) {
	// Centre of the planet, at the distance its angular radius implies for a
	// unit-radius sphere.
	dist := 1 / math.Sin(spacePlanetRadius)
	p := spacePlanetDir.scale(dist)

	ang := math.Acos(skyClamp(d.dot(spacePlanetDir), -1, 1))

	// The atmosphere's outer halo, outside the disc entirely: brightest where
	// the limb below it is in daylight, which is what makes the lit edge glow
	// blue and leaves the night edge dark.
	var halo skyVec
	if out := ang - spacePlanetRadius; out > 0 && out < 0.16 {
		// The normal at the limb nearest this direction: the outward radial
		// component of the view direction.
		limb := d.add(spacePlanetDir.scale(-d.dot(spacePlanetDir))).unit()
		lit := skySmoothstep(-0.12, 0.35, limb.dot(sunDir))
		g := math.Exp(-out/0.022)*0.75 + math.Exp(-out/0.075)*0.18
		halo = skyVec{0.32, 0.55, 1.00}.scale(g * lit)
	}

	// The near intersection of the view ray with the planet. b <= 0 is the
	// half of the sky facing AWAY from the planet: the ray still meets the
	// sphere, but behind the camera, and taking that hit draws a second planet
	// overhead — which is exactly what this used to do.
	b := d.dot(p)
	disc := b*b - (dist*dist - 1)
	if b <= 0 || disc <= 0 {
		return halo, 0
	}
	t := b - math.Sqrt(disc)
	if t <= 0 {
		return halo, 0
	}
	hit := d.scale(t)
	n := hit.add(p.scale(-1)) // unit: the sphere has radius 1

	albedo, ocean, land := spacePlanetSurface(n)

	// Daylight. The terminator is softened over a few degrees rather than being
	// the hard line a pure Lambert dot gives — a real one is blurred by the
	// atmosphere, and a hard one looks like a shading bug.
	lam := n.dot(sunDir)
	lit := skySmoothstep(-0.10, 0.22, lam)

	// Clouds sit above the surface, so they are composited over it before the
	// light hits both.
	cov := spacePlanetClouds(n)
	surf := skyMix(albedo, skyVec{0.94, 0.95, 0.97}, cov*0.92)

	col := surf.scale(0.06 + 1.15*lit*math.Max(lam, 0.05))

	// Sun glint off open water: the one specular highlight on the planet, and
	// what stops the oceans reading as flat paint.
	if ocean > 0.01 && lit > 0.01 {
		h := sunDir.add(d.scale(-1)).unit()
		// A tight exponent on purpose: the planet subtends 116 degrees here, so
		// a lobe wide enough to look right on a distant world projects into a
		// blown-out white continent on this one.
		spec := math.Pow(math.Max(n.dot(h), 0), 600)
		col = col.add(spaceSunColor.scale(spec * ocean * (1 - cov) * lit * 0.7))
	}

	// Rayleigh haze towards the limb — the blue rind on the day side, thickest
	// where the line of sight grazes the atmosphere.
	rim := math.Pow(1-skyClamp(n.dot(d.scale(-1)), 0, 1), 2.6)
	col = col.add(skyVec{0.30, 0.50, 0.95}.scale(rim * lit * 0.75))

	// Night side: city lights, clustered on land near the coasts, dimmed by
	// whatever cloud is over them.
	if lit < 0.5 && land > 0.5 {
		cluster := skySmoothstep(0.55, 0.85, skyFbm3(n.scale(9.0), 4, 307))
		grid := skySmoothstep(0.80, 0.97, skyFbm3(n.scale(46.0), 2, 311))
		col = col.add(skyVec{1.00, 0.76, 0.42}.scale(cluster * grid * (1 - lit) * (1 - 0.8*cov) * 0.55))
	}

	// The disc's own edge, softened by about a texel's worth of angle.
	cover := 1 - skySmoothstep(spacePlanetRadius-0.0016, spacePlanetRadius+0.0016, ang)
	return col.add(halo.scale(1 - cover)), cover
}

// ---------- the sky ----------

// spaceColor is the whole orbital sky in one function.
func spaceColor(d skyVec) skyVec {
	d = d.unit()

	// Background: the void, with a very slight large-scale mottle so it is not
	// a flat field of one value.
	col := spaceVoid.scale(0.85 + 0.4*skyFbm3(d.scale(1.4), 3, 401))

	// The galaxy, then the stars — denser inside the band, where the naked eye
	// would be resolving part of what makes the band bright in the first place.
	galaxy, band := skyGalaxy(d)
	col = col.add(galaxy)
	col = col.add(skyStars(d, band))

	// The sun. In vacuum there is nothing to scatter it, so the glow is tight:
	// a hard little disc and a bloom that falls away fast, rather than the wide
	// warm haze the daylight sky has.
	ang := math.Acos(skyClamp(d.dot(skySunDir), -1, 1))
	sun := (1-skySmoothstep(0.0085, 0.0125, ang))*3.0 +
		math.Exp(-ang/0.030)*0.9 +
		math.Exp(-ang/0.16)*0.05
	col = col.add(spaceSunColor.scale(sun))

	// The planet last, over everything: it is the nearest thing in the sky, so
	// it occludes the stars behind it rather than blending with them.
	planet, cover := spacePlanet(d, skySunDir)
	if cover > 0 {
		col = skyMix(col, planet, cover)
	} else {
		col = col.add(planet) // outside the disc, this is only the halo
	}
	return col
}

// genSkySpace writes the orbital skybox.
func genSkySpace() { genSkyCube("sky_space", spaceColor) }
