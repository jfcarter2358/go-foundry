package gfx

// water_textures.go — the water path's generated detail map
// (docs/terrain-paint/PHASE_W1.md, "Detail normals" and "Foam").
//
// Poly Haven has no water normals, foam or caustics, so the water's fine
// detail is generated: one 256² RGBA8 tile, made at first use from a fixed
// seed (deterministic: the same bytes on every machine, which a test pins by
// hash), uploaded with mipmaps, repeating and anisotropic like any world
// texture, and registered in Textures as WaterDetailTexture — the name a
// water surface wears as its albedo (water_draw.go says why).
//
// Its channels:
//
//	R, G  the SLOPE of a tileable ripple height field, d/dx and d/dz, as
//	      0.5 + slope * waterDetailSlopeEncode. Slopes rather than normals
//	      because slopes add: water.glsl samples the tile three times, at
//	      different sizes, headings and drift speeds, sums the three slopes
//	      and the swell's, and builds one normal from the total — the usual
//	      "2–3 scrolling normal maps" from one map without three textures.
//	      The field is a spectrum of waves on integer wave vectors (so it
//	      tiles exactly), amplitudes falling as |k|^-1.9 like a capillary/
//	      short-gravity ripple spectrum, random phases, a mild preferred
//	      heading so the ripples read as wind-driven.
//	B     FOAM: a bubbly network — the edges of a tileable Worley pattern at
//	      two scales, broken by noise. water.glsl thresholds it against the
//	      foam mask, so thin foam shows only the network's strongest lines.
//	A     a smooth tileable noise, for breaking up the foam band and the
//	      caustics so neither repeats visibly.

import (
	"math"
	"math/rand"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// WaterDetailTexture is the generated detail map's name in Textures.
const WaterDetailTexture = "__water_detail"

// waterDetailSize is the tile's edge, texels.
const waterDetailSize = 256

// waterDetailSlopeEncode maps a slope to the 0..1 range of a channel:
// MUST match WATER_DETAIL_SLOPE in water.glsl (its reciprocal).
const waterDetailSlopeEncode = 0.25

// WaterDetailPixels generates the detail map's RGBA8 bytes, row-major,
// waterDetailSize² texels. Pure and deterministic.
func WaterDetailPixels() []byte {
	n := waterDetailSize
	sx, sz := waterRippleSlopes(n, 20261015)
	foam := waterFoamPattern(n, 7)
	noise := tileNoise(n, 8, 99)
	out := make([]byte, n*n*4)
	for i := 0; i < n*n; i++ {
		out[i*4+0] = unitByte(0.5 + sx[i]*waterDetailSlopeEncode)
		out[i*4+1] = unitByte(0.5 + sz[i]*waterDetailSlopeEncode)
		out[i*4+2] = unitByte(foam[i])
		out[i*4+3] = unitByte(noise[i])
	}
	return out
}

func unitByte(v float64) byte {
	return byte(math.Round(math.Min(math.Max(v, 0), 1) * 255))
}

// waterRippleSlopes is the ripple field's x and z slopes over an n² tile
// one unit across, scaled so their RMS is 1.
func waterRippleSlopes(n int, seed int64) (sx, sz []float64) {
	r := rand.New(rand.NewSource(seed))
	type wave struct{ kx, kz, amp, phase float64 }
	var waves []wave
	wind := [2]float64{math.Cos(0.4), math.Sin(0.4)}
	for kx := -18; kx <= 18; kx++ {
		for kz := -18; kz <= 18; kz++ {
			k := math.Hypot(float64(kx), float64(kz))
			if k < 2 || k > 18 {
				continue
			}
			// A third of the vectors, picked at random, keeps it a few
			// hundred waves: cheap at init and still irregular.
			if r.Float64() > 0.35 {
				continue
			}
			align := (float64(kx)*wind[0] + float64(kz)*wind[1]) / k
			dir := 0.45 + 0.55*align*align
			waves = append(waves, wave{float64(kx), float64(kz), math.Pow(k, -1.9) * dir * (0.6 + 0.8*r.Float64()),
				r.Float64() * 2 * math.Pi})
		}
	}
	sx, sz = make([]float64, n*n), make([]float64, n*n)
	// cos(2π(kx·x + kz·z)/n + φ) per texel, built from per-row and
	// per-column tables so each wave costs a multiply-add a texel.
	cx, sxT := make([]float64, n), make([]float64, n)
	cz, szT := make([]float64, n), make([]float64, n)
	for _, w := range waves {
		for i := 0; i < n; i++ {
			a := 2 * math.Pi * w.kx * float64(i) / float64(n)
			cx[i], sxT[i] = math.Cos(a), math.Sin(a)
			b := 2*math.Pi*w.kz*float64(i)/float64(n) + w.phase
			cz[i], szT[i] = math.Cos(b), math.Sin(b)
		}
		gx, gz := w.amp*2*math.Pi*w.kx, w.amp*2*math.Pi*w.kz
		for j := 0; j < n; j++ {
			for i := 0; i < n; i++ {
				// h = amp·sin(a+b); dh/dx = amp·2πkx·cos(a+b).
				c := cx[i]*cz[j] - sxT[i]*szT[j]
				sx[j*n+i] += gx * c
				sz[j*n+i] += gz * c
			}
		}
	}
	var ss float64
	for i := range sx {
		ss += sx[i]*sx[i] + sz[i]*sz[i]
	}
	rms := math.Sqrt(ss / float64(2*len(sx)))
	if rms > 0 {
		for i := range sx {
			sx[i] /= rms
			sz[i] /= rms
		}
	}
	return sx, sz
}

// waterFoamPattern is the bubbly foam network, 0..1, over an n² tile.
func waterFoamPattern(n int, seed int64) []float64 {
	a := worleyEdges(n, 9, seed)
	b := worleyEdges(n, 23, seed+1)
	breakup := tileNoise(n, 16, seed+2)
	out := make([]float64, n*n)
	for i := range out {
		v := 0.65*a[i] + 0.55*b[i]
		v *= 0.55 + 0.9*breakup[i]
		out[i] = math.Min(math.Max(v, 0), 1)
	}
	return out
}

// worleyEdges is 1 on the borders between cells of a tileable Worley
// pattern of cells² jittered points, falling to 0 inside them.
func worleyEdges(n, cells int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	pts := make([][2]float64, cells*cells)
	for i := range pts {
		pts[i] = [2]float64{r.Float64(), r.Float64()}
	}
	out := make([]float64, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			px := float64(i) / float64(n) * float64(cells)
			pz := float64(j) / float64(n) * float64(cells)
			cx, cz := int(px), int(pz)
			f1, f2 := 9.0, 9.0
			for dz := -1; dz <= 1; dz++ {
				for dx := -1; dx <= 1; dx++ {
					gx, gz := cx+dx, cz+dz
					p := pts[((gz+cells)%cells)*cells+(gx+cells)%cells]
					d := math.Hypot(float64(gx)+p[0]-px, float64(gz)+p[1]-pz)
					if d < f1 {
						f1, f2 = d, f1
					} else if d < f2 {
						f2 = d
					}
				}
			}
			e := f2 - f1
			out[j*n+i] = math.Exp(-e * e * 60)
		}
	}
	return out
}

// tileNoise is smooth value noise on a cells² lattice, tiling over n², 0..1.
func tileNoise(n, cells int, seed int64) []float64 {
	r := rand.New(rand.NewSource(seed))
	lat := make([]float64, cells*cells)
	for i := range lat {
		lat[i] = r.Float64()
	}
	at := func(x, z int) float64 { return lat[((z%cells+cells)%cells)*cells+(x%cells+cells)%cells] }
	out := make([]float64, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			fx := float64(i) / float64(n) * float64(cells)
			fz := float64(j) / float64(n) * float64(cells)
			x0, z0 := int(fx), int(fz)
			tx, tz := fx-float64(x0), fz-float64(z0)
			tx, tz = tx*tx*(3-2*tx), tz*tz*(3-2*tz)
			a := at(x0, z0) + (at(x0+1, z0)-at(x0, z0))*tx
			b := at(x0, z0+1) + (at(x0+1, z0+1)-at(x0, z0+1))*tx
			out[j*n+i] = a + (b-a)*tz
		}
	}
	return out
}

// EnsureWaterDetailTexture uploads the detail map once, with the GL context
// current, and registers it as WaterDetailTexture.
func EnsureWaterDetailTexture() {
	if Textures[WaterDetailTexture] != 0 {
		return
	}
	px := WaterDetailPixels()
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, waterDetailSize, waterDetailSize, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(px))
	gl.GenerateMipmap(gl.TEXTURE_2D)
	ApplyWorldTextureFilterTo(gl.TEXTURE_2D)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	Textures[WaterDetailTexture] = tex
}
