// Package noise3d builds the tileable 3D noise volumes the volumetric passes
// sample: cloud volumes, the sky's cloud deck and volume particle layers.
//
// It is pure — no GL — for the reason internal/particle is: the generator is
// arithmetic, and arithmetic is testable headlessly. render uploads the bytes
// this returns as GL_TEXTURE_3D with GL_REPEAT, so the one property that
// matters more than any other is that every volume TILES: texel size-1 must be
// as continuous with texel 0 as any two neighbours are, or a cloud shows a flat
// seam every repeat of its noise.
//
// Generated at startup rather than baked into resources/, because a 64³ volume
// is a few tens of milliseconds across the machine's cores, and a file the
// engine reads rather than one a level names is a file the release packer's
// content scan cannot see.
//
// Two volumes, after the arrangement Guerrilla's Horizon Zero Dawn clouds made
// the standard one:
//
//	Shape  — two channels. R is Perlin-Worley: billowy Perlin fbm carved by
//	         inverted Worley, which is what gives a cloud rounded lobes rather
//	         than the smeared blobs Perlin alone makes. G is a higher-frequency
//	         Worley fbm, mixed in for mid-scale structure.
//	Detail — one channel of high-frequency Worley fbm, used to erode the edges
//	         a shape sample leaves soft, which is what turns a lobe into wisps.
package noise3d

import (
	"math"
	"runtime"
	"sync"
)

// ShapeSize and DetailSize are the edge lengths the renderer uploads.
const (
	ShapeSize  = 64
	DetailSize = 32
)

// Shape returns a size³ volume with two channels per texel, R then G, in
// x-fastest order: index (z*size*size + y*size + x) * 2.
func Shape(size int) []uint8 {
	r := make([]float64, size*size*size)
	g := make([]float64, size*size*size)
	fill(size, func(x, y, z float64, i int) {
		p := perlinFBM(x, y, z, 4, 3, 101)
		w := worleyFBM(x, y, z, 4, 11)
		// Perlin-Worley: remap the Perlin value from [w-1, 1] to [0, 1]. Where
		// the Worley cells are bright (inside a lobe) the floor rises toward 0
		// and the Perlin survives; between cells it drops and carves the gap.
		r[i] = clamp01((p - (w - 1)) / (2 - w))
		g[i] = worleyFBM(x, y, z, 8, 23)
	})
	out := make([]uint8, size*size*size*2)
	quantise(r, out, 2, 0)
	quantise(g, out, 2, 1)
	return out
}

// Detail returns a size³ volume of one channel.
func Detail(size int) []uint8 {
	d := make([]float64, size*size*size)
	fill(size, func(x, y, z float64, i int) {
		d[i] = worleyFBM(x, y, z, 4, 37)
	})
	out := make([]uint8, size*size*size)
	quantise(d, out, 1, 0)
	return out
}

// fill calls fn for every texel, with the texel's centre in [0, 1)³, split
// across the machine's cores by z slice.
func fill(size int, fn func(x, y, z float64, i int)) {
	workers := runtime.NumCPU()
	if workers > size {
		workers = size
	}
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	slices := make(chan int, size)
	for z := 0; z < size; z++ {
		slices <- z
	}
	close(slices)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inv := 1 / float64(size)
			for z := range slices {
				fz := (float64(z) + 0.5) * inv
				for y := 0; y < size; y++ {
					fy := (float64(y) + 0.5) * inv
					base := (z*size + y) * size
					for x := 0; x < size; x++ {
						fn((float64(x)+0.5)*inv, fy, fz, base+x)
					}
				}
			}
		}()
	}
	wg.Wait()
}

// quantise stretches one channel to the full 0..255 range and writes it into
// dst at the given channel of a stride-channel layout.
//
// Stretched over the channel's own min and max rather than clamped from a
// theoretical range, because neither Perlin nor Worley fbm reaches its
// theoretical bounds, and a volume that only spans 60..190 wastes most of what
// the shader's coverage remap has to work with. A global affine map keeps the
// tiling exact.
func quantise(src []float64, dst []uint8, stride, channel int) {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range src {
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	span := hi - lo
	if span < 1e-12 {
		span = 1
	}
	for i, v := range src {
		dst[i*stride+channel] = uint8(math.Round((v - lo) / span * 255))
	}
}

// ---------- the noise functions ----------
//
// Both are periodic: the lattice coordinates are wrapped modulo the frequency
// before being hashed, so a function sampled over [0, 1) at frequency f
// repeats exactly at 1. That, and not anything done to the bytes afterwards, is
// what makes the uploaded texture tile.

// perlinFBM is octaves of periodic gradient noise starting at base cells per
// repeat, each octave twice the frequency and half the amplitude, mapped to
// roughly [0, 1].
func perlinFBM(x, y, z float64, base, octaves int, seed uint32) float64 {
	sum, amp, norm := 0.0, 1.0, 0.0
	f := base
	for o := 0; o < octaves; o++ {
		sum += amp * perlin(x*float64(f), y*float64(f), z*float64(f), f, seed+uint32(o)*977)
		norm += amp
		amp *= 0.5
		f *= 2
	}
	return clamp01(sum/norm*0.5 + 0.5)
}

// worleyFBM is three octaves of inverted periodic Worley noise at base, 2*base
// and 4*base cells per repeat, weighted 0.625/0.25/0.125 — bright in the middle
// of a cell and dark at its borders, which is the shape a billow has.
func worleyFBM(x, y, z float64, base int, seed uint32) float64 {
	return 0.625*worley(x, y, z, base, seed) +
		0.25*worley(x, y, z, base*2, seed+131) +
		0.125*worley(x, y, z, base*4, seed+263)
}

// perlin is periodic gradient noise with the given number of cells per repeat,
// in about [-1, 1].
func perlin(x, y, z float64, period int, seed uint32) float64 {
	xi, yi, zi := math.Floor(x), math.Floor(y), math.Floor(z)
	fx, fy, fz := x-xi, y-yi, z-zi
	ix, iy, iz := int(xi), int(yi), int(zi)

	u, v, w := fade(fx), fade(fy), fade(fz)
	corner := func(dx, dy, dz int) float64 {
		h := hash(wrap(ix+dx, period), wrap(iy+dy, period), wrap(iz+dz, period), seed)
		return gradDot(h, fx-float64(dx), fy-float64(dy), fz-float64(dz))
	}
	x00 := lerp(corner(0, 0, 0), corner(1, 0, 0), u)
	x10 := lerp(corner(0, 1, 0), corner(1, 1, 0), u)
	x01 := lerp(corner(0, 0, 1), corner(1, 0, 1), u)
	x11 := lerp(corner(0, 1, 1), corner(1, 1, 1), u)
	return lerp(lerp(x00, x10, v), lerp(x01, x11, v), w)
}

// worley is inverted periodic cellular noise: 1 at a feature point, falling to
// 0 a cell's width away. One jittered feature point per cell.
func worley(x, y, z float64, period int, seed uint32) float64 {
	px, py, pz := x*float64(period), y*float64(period), z*float64(period)
	cx, cy, cz := int(math.Floor(px)), int(math.Floor(py)), int(math.Floor(pz))
	best := math.Inf(1)
	for dz := -1; dz <= 1; dz++ {
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				nx, ny, nz := cx+dx, cy+dy, cz+dz
				h := hash(wrap(nx, period), wrap(ny, period), wrap(nz, period), seed)
				fx := float64(nx) + unit(h)
				fy := float64(ny) + unit(h*0x9E3779B1+1)
				fz := float64(nz) + unit(h*0x85EBCA77+2)
				ex, ey, ez := fx-px, fy-py, fz-pz
				if d := ex*ex + ey*ey + ez*ez; d < best {
					best = d
				}
			}
		}
	}
	return 1 - clamp01(math.Sqrt(best))
}

// ---------- small helpers ----------

// hash mixes a wrapped lattice point and a seed into 32 well-distributed bits.
// A fixed integer mix rather than math/rand, so the volume is identical on
// every machine and every run.
func hash(x, y, z int, seed uint32) uint32 {
	h := seed*0x27d4eb2d ^ uint32(x)*0x165667b1 ^ uint32(y)*0xd3a2646c ^ uint32(z)*0xfd7046c5
	h ^= h >> 15
	h *= 0x2c1b3c6d
	h ^= h >> 12
	h *= 0x297a2d39
	h ^= h >> 15
	return h
}

// unit maps a hash to [0, 1).
func unit(h uint32) float64 {
	h ^= h >> 16
	h *= 0x7feb352d
	h ^= h >> 15
	return float64(h&0xFFFFFF) / float64(1<<24)
}

// gradDot dots one of twelve cube-edge gradients with the offset, which is
// Perlin's improved-noise gradient set.
func gradDot(h uint32, x, y, z float64) float64 {
	switch h % 12 {
	case 0:
		return x + y
	case 1:
		return -x + y
	case 2:
		return x - y
	case 3:
		return -x - y
	case 4:
		return x + z
	case 5:
		return -x + z
	case 6:
		return x - z
	case 7:
		return -x - z
	case 8:
		return y + z
	case 9:
		return -y + z
	case 10:
		return y - z
	default:
		return -y - z
	}
}

func wrap(i, n int) int {
	i %= n
	if i < 0 {
		i += n
	}
	return i
}

func fade(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
