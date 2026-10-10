package main

// sky.go — the cube-face machinery every skybox here is built on, and the
// starter daylight sky itself. The orbital one is in sky_space.go.
//
// Unlike every other generator in this command, a skybox is not a 64x64 tile.
// It is six 512x512 faces that have to agree with each other along twelve
// shared edges, and the way they are made to agree is that all six are the SAME
// continuous function of a 3D direction — skyColor here, spaceColor there.
// Nothing is matched up afterwards; two texels either side of a cube edge look
// the same because they were asked about nearly the same direction.
//
// The face mapping is GL's own, inverted (skyFaceDir). Row 0 of the PNG is
// t=0, unflipped, because that is exactly how gfx.uploadCubemap hands the rows
// to TexImage2D — see its note on why flipping here is what produces a sky
// that is upside down on four faces and mirrored on the other two.
//
// The six files are named for world.SkyFaceSuffix, so a level picks a whole
// set up with Sky.SetFacesFromBase("sky_day").

import (
	"image"
	"image/color"
	"math"
	"sync"
)

// skyFaceSize is the resolution of one cube face. 512 is the same order as the
// engine's largest existing texture and gives the sun disc — a few thousandths
// of a radian across — enough texels to land on.
const skyFaceSize = 512

// skySamples is the NxN supersampling per texel. The sun's rim and the frayed
// cloud edges are the two places a single sample per texel shows, and 2x2 is
// enough for both at this resolution.
const skySamples = 2

// ---------- a little 3D vector, local to this file ----------

type skyVec struct{ X, Y, Z float64 }

func (a skyVec) add(b skyVec) skyVec    { return skyVec{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a skyVec) scale(s float64) skyVec { return skyVec{a.X * s, a.Y * s, a.Z * s} }
func (a skyVec) dot(b skyVec) float64   { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }

func (a skyVec) unit() skyVec {
	l := math.Sqrt(a.dot(a))
	if l == 0 {
		return a
	}
	return a.scale(1 / l)
}

func skyMix(a, b skyVec, t float64) skyVec { return a.scale(1 - t).add(b.scale(t)) }

func skyClamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func skySmoothstep(e0, e1, x float64) float64 {
	t := skyClamp((x-e0)/(e1-e0), 0, 1)
	return t * t * (3 - 2*t)
}

// ---------- palette ----------

var (
	// The three stops are world.DefaultSky()'s own, so a level toggling between
	// the gradient sky and this cubemap does not jump colour — which is the
	// whole point of a STARTER skybox: it should look like the default sky with
	// weather in it, not like different art.
	skyZenith  = skyVec{0.25, 0.45, 0.85}
	skyHorizon = skyVec{0.72, 0.82, 0.92}
	skyGround  = skyVec{0.18, 0.17, 0.16}

	// The sun sits opposite the direction a default directional light travels
	// (world.DefaultDirLightYaw 45, DefaultDirLightPitch -50), so the painted
	// sun and the level's own sun agree with Sky.Rotation left at 0. An author
	// who moves the light moves the disc back into place with that dial.
	skySunDir = skyVec{-0.4545, 0.766, -0.4545}.unit()

	skySunColor  = skyVec{1.00, 0.96, 0.86}
	skyCloudLit  = skyVec{1.00, 0.99, 0.96}
	skyCloudDark = skyVec{0.62, 0.66, 0.75}
)

// ---------- value noise ----------
//
// Hash-based rather than drawn from the seeded rng every other generator here
// shares. That is deliberate twice over: it keeps this generator out of that
// draw sequence (see the note in main), and it makes the cloud field a pure
// function of position, which is what lets two faces evaluate the same cloud
// from opposite sides of an edge and get the same answer.

func skyHash(ix, iy int) float64 {
	h := uint32(ix)*374761393 + uint32(iy)*668265263
	h = (h ^ (h >> 13)) * 1274126177
	h ^= h >> 16
	return float64(h) / float64(^uint32(0))
}

func skyValueNoise(x, y float64) float64 {
	ix, iy := math.Floor(x), math.Floor(y)
	fx, fy := x-ix, y-iy
	ux := fx * fx * (3 - 2*fx)
	uy := fy * fy * (3 - 2*fy)
	i, j := int(ix), int(iy)
	a, b := skyHash(i, j), skyHash(i+1, j)
	c, d := skyHash(i, j+1), skyHash(i+1, j+1)
	return (a*(1-ux)+b*ux)*(1-uy) + (c*(1-ux)+d*ux)*uy
}

func skyFbm(x, y float64, octaves int) float64 {
	sum, amp, norm := 0.0, 0.5, 0.0
	for o := 0; o < octaves; o++ {
		sum += amp * skyValueNoise(x, y)
		norm += amp
		amp *= 0.5
		// Rotated as well as scaled between octaves, so the lattices do not
		// stack up into a visible grid the way axis-aligned octaves do.
		x, y = (x*0.8-y*0.6)*2.02, (x*0.6+y*0.8)*2.02
	}
	return sum / norm
}

// skyCloudPlane projects a view direction onto a flat cloud deck at the given
// height. The 1/elevation stretch is what squeezes the deck into a band as the
// view approaches the horizon — the same thing a real cloud layer does, and
// what keeps the four side faces from reading as wallpaper.
func skyCloudPlane(d skyVec, height float64) (float64, float64) {
	t := height / math.Max(d.Y, 0.035)
	return d.X * t, d.Z * t
}

// skyCloudDensity is the deck's coverage in this direction, 0..1.
func skyCloudDensity(d skyVec) float64 {
	u, v := skyCloudPlane(d, 1.0)
	n := skyFbm(u*0.55+13.7, v*0.55-4.2, 6)
	// Everything below the threshold is clear sky; the soft ramp above it is
	// what frays the edges instead of cutting them.
	dens := skySmoothstep(0.50, 0.78, n)

	// A second, sparser sheet of high cirrus twice as far up and at a coarser
	// scale, so the dome has two depths in it rather than one.
	u2, v2 := skyCloudPlane(d, 2.4)
	dens = math.Max(dens, 0.45*skySmoothstep(0.55, 0.85, skyFbm(u2*0.22-31.1, v2*0.22+7.9, 4)))

	// Off entirely below the horizon, and faded out just above it so the deck
	// arrives in the haze rather than stopping at a line.
	return skyClamp(dens*skySmoothstep(0.005, 0.13, d.Y), 0, 1)
}

// skyColor is the entire sky in one function: gradient, sun, and clouds, for a
// direction that need not be normalised.
func skyColor(d skyVec) skyVec {
	d = d.unit()
	e := d.Y

	// Base gradient. The pow curve keeps the pale horizon band thin rather than
	// washing out the lower half of the dome.
	var base skyVec
	if e >= 0 {
		base = skyMix(skyHorizon, skyZenith, math.Pow(e, 0.42))
	} else {
		// Below the horizon: haze first, then down into the ground stop. This
		// is what is seen over a cliff edge or from a bridge, and nowhere else.
		base = skyMix(skyHorizon, skyGround, skySmoothstep(0.0, 0.28, -e))
	}

	// Glow around the sun, warming its whole side of the sky before the disc
	// itself is drawn. The broad second term is atmospheric haze: without it
	// the sky opposite the sun is as cold at the horizon as at the zenith.
	ang := math.Acos(skyClamp(d.dot(skySunDir), -1, 1))
	base = base.add(skySunColor.scale(math.Exp(-ang/0.30)*0.55 + math.Exp(-ang/1.10)*0.14))

	// The horizon ring picks up a little of that warmth too, strongest towards
	// the sun, so the band around the viewer is not uniformly cold.
	base = base.add(skySunColor.scale(0.10 * math.Exp(-math.Abs(e)/0.10) * math.Exp(-ang/1.6)))

	// Clouds, lit by sampling the deck again a short way towards the sun:
	// where that neighbouring sample is thinner, this piece of cloud is facing
	// the light, and where it is thicker this piece is in the deck's shadow.
	// It costs a second density evaluation and buys the deck its volume.
	if dens := skyCloudDensity(d); dens > 0.001 {
		lit := skyCloudDensity(d.add(skySunDir.scale(0.055)).unit())
		c := skyMix(skyCloudDark, skyCloudLit, skyClamp(0.55+1.1*(dens-lit), 0, 1))
		// Silver lining: thin cloud near the sun is lit through rather than on.
		c = c.add(skySunColor.scale(0.35 * math.Exp(-ang/0.55) * (1 - dens)))
		// Thin cloud is translucent, so the coverage doubles as its alpha.
		base = skyMix(base, c, dens*0.94)
	}

	// The disc last, so no cloud is ever drawn in front of the sun's core —
	// which would read as a hole in the sun rather than as cloud.
	base = base.add(skySunColor.scale((1 - skySmoothstep(0.0040, 0.0075, ang)) * 2.4))

	return base
}

// skyFaceDir is GL's cube-face convention inverted: the direction that face's
// (s,t) texel is sampled by. Faces are in world.SkyFaceOrder — +X, -X, +Y, -Y,
// +Z, -Z — which is also the order their GL face targets come in, so the index
// here is the same index everywhere else.
func skyFaceDir(face int, s, t float64) skyVec {
	sc, tc := 2*s-1, 2*t-1
	switch face {
	case 0: // +X, east
		return skyVec{1, -tc, -sc}
	case 1: // -X, west
		return skyVec{-1, -tc, sc}
	case 2: // +Y, up
		return skyVec{sc, 1, tc}
	case 3: // -Y, down
		return skyVec{sc, -1, -tc}
	case 4: // +Z, south
		return skyVec{sc, -tc, 1}
	default: // -Z, north
		return skyVec{-sc, -tc, -1}
	}
}

// skyEncode quantises a channel to 8 bits with a touch of ordered dither. The
// sky is the one surface in a level with no detail on it to hide banding, and a
// smooth dome across 8 bits bands visibly without this.
func skyEncode(v float64, x, y int) uint8 {
	d := (float64((x*7+y*13)%16)/16 - 0.5) / 255
	return uint8(skyClamp(v+d, 0, 1)*255 + 0.5)
}

// skyFaceSuffix mirrors world.SkyFaceSuffix — the names Sky.SetFacesFromBase
// expects to find. Spelled out here rather than imported so this command stays
// a standalone image generator with no engine dependencies.
var skyFaceSuffix = [6]string{"px", "nx", "py", "ny", "pz", "nz"}

// genSkyCube writes the six faces of one cubemap, naming them base_px, base_nx
// and so on — world.SkyFaceSuffix, which is what Sky.SetFacesFromBase expects
// to find. shade is the sky: a colour per direction, and the only thing that
// differs between one sky in this command and the next.
//
// One face per goroutine. These are the only textures here big enough for it to
// matter, and the shading functions read nothing but their argument, so the six
// faces have nothing to share.
func genSkyCube(base string, shade func(skyVec) skyVec) {
	imgs := [6]*image.NRGBA{}
	var wg sync.WaitGroup
	for face := 0; face < 6; face++ {
		wg.Add(1)
		go func(face int) {
			defer wg.Done()
			img := image.NewNRGBA(image.Rect(0, 0, skyFaceSize, skyFaceSize))
			for y := 0; y < skyFaceSize; y++ {
				for x := 0; x < skyFaceSize; x++ {
					var acc skyVec
					for sy := 0; sy < skySamples; sy++ {
						for sx := 0; sx < skySamples; sx++ {
							s := (float64(x) + (float64(sx)+0.5)/skySamples) / skyFaceSize
							t := (float64(y) + (float64(sy)+0.5)/skySamples) / skyFaceSize
							acc = acc.add(shade(skyFaceDir(face, s, t)))
						}
					}
					c := acc.scale(1 / float64(skySamples*skySamples))
					img.SetNRGBA(x, y, color.NRGBA{
						R: skyEncode(c.X, x, y),
						G: skyEncode(c.Y, x, y),
						B: skyEncode(c.Z, x, y),
						A: 255,
					})
				}
			}
			imgs[face] = img
		}(face)
	}
	wg.Wait()

	// Written on the way out rather than from each goroutine, so a face is
	// never left half-encoded on disk if another one panics.
	for face := 0; face < 6; face++ {
		save(base+"_"+skyFaceSuffix[face], imgs[face])
	}
}

// genSkyDay writes the starter daylight skybox.
func genSkyDay() { genSkyCube("sky_day", skyColor) }
