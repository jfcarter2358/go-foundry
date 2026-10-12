package terrain

// droplet.go — particle hydraulic erosion at full resolution.
//
// The classic droplet model (Hans Beyer's thesis, popularised by Sebastian
// Lague): a droplet lands at a random point and runs downhill for Lifetime
// metres, carrying sediment. Each step it
//
//   - turns toward the downhill gradient, keeping Inertia of its heading;
//   - moves one cell; its capacity is fall · speed · water · Capacity (with
//     a MinSlope floor so it still carries on the flat);
//   - over capacity, or moving uphill, it DEPOSITS (into the four samples
//     around it); under capacity it ERODES, from a disc of Radius metres,
//     never deeper than the fall it just made (so it cannot dig a pit);
//   - speeds up with the fall (Gravity) and evaporates.
//
// Erosion is scaled by the strata's erodibility at the sample's height:
// droplets strip the soft layers and leave the hard ones standing, which
// sharpens the ledges terrace made and cuts rills into the slopes between
// them. All the heights are metres and every rate is per metre of fall, so
// the result depends on the landforms and not much on the resolution.
//
// # Parallel, and the same on any number of workers
//
// Droplets interact only through the heights they read and write, within a
// few cells of their path. The map is cut into square blocks of B cells,
// B = 2·(steps + radius + 2), and coloured as a 2 x 2 checkerboard. A
// droplet starting in a block may wander at most M = (B - 2·radius - 4)/2
// cells outside it — further, it dies — so two blocks of one colour, B cells
// apart, never touch the same sample: every block of a colour runs at once,
// one goroutine per block, with no locks. The colours run one after another,
// for Passes rounds.
//
// Inside a block the droplets run in order from an RNG keyed by (seed,
// pass, block). Nothing depends on which goroutine takes which block, so the
// result is identical on 1 worker or 64.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// brushTap is one sample of the erosion brush.
type brushTap struct {
	di, dj int
	w      float32
}

func makeBrush(r float64) []brushTap {
	ri := int(math.Ceil(r))
	var taps []brushTap
	sum := float32(0)
	for dj := -ri; dj <= ri; dj++ {
		for di := -ri; di <= ri; di++ {
			d := math.Hypot(float64(di), float64(dj))
			if d >= r {
				continue
			}
			w := float32(r - d)
			taps = append(taps, brushTap{di, dj, w})
			sum += w
		}
	}
	if len(taps) == 0 {
		return []brushTap{{0, 0, 1}}
	}
	for i := range taps {
		taps[i].w /= sum
	}
	return taps
}

// heightGrad is the bilinear height and gradient (metres per cell) at
// fractional sample coordinates (x, z); the caller keeps them in [0, s-1).
func heightGrad(h []float32, s int, x, z float64) (float32, float32, float32) {
	i, j := int(x), int(z)
	u, v := float32(x-float64(i)), float32(z-float64(j))
	k := j*s + i
	a, b, c, d := h[k], h[k+1], h[k+s], h[k+s+1]
	gx := (b-a)*(1-v) + (d-c)*v
	gz := (c-a)*(1-u) + (d-b)*u
	ht := a*(1-u)*(1-v) + b*u*(1-v) + c*(1-u)*v + d*u*v
	return ht, gx, gz
}

// droplets erodes g; it returns how many droplets ran.
func droplets(s *Settings, g *Grid, c *compiledStrata, off, deposit []float32, pool Pool, progress func(float32)) (int, error) {
	d := s.Droplets
	if d.Density <= 0 || d.Lifetime <= 0 {
		return 0, nil
	}
	sz := g.S()
	cell := float32(g.Cell)
	steps := max(int(math.Round(d.Lifetime/g.Cell)), 4)
	r := max(d.Radius/g.Cell, 1.5)
	brush := makeBrush(r)
	ri := int(math.Ceil(r))
	B := 2 * (steps + ri + 2)
	M := (B - 2*ri - 4) / 2
	nb := (sz + B - 1) / B
	passes := max(d.Passes, 1)
	total := int(math.Round(d.Density * g.Size() * g.Size()))
	if total <= 0 {
		return 0, nil
	}
	// Each block's share is its area's.
	perPass := float64(total) / float64(passes)
	inertia := float32(min(max(d.Inertia, 0), 1))
	capF := float32(d.Capacity)
	minSlope := float32(d.MinSlope)
	cellArea := cell * cell
	// Per metre run: a step is one cell long.
	eroRate := float32(min(max(d.Erosion*g.Cell, 0), 1))
	depRate := float32(min(max(d.Deposition*g.Cell, 0), 1))
	evap := float32(1 - math.Pow(1-min(max(d.Evaporation, 0), 1), g.Cell))
	grav := float32(d.Gravity)
	h := g.H
	ran := 0
	type block struct{ bi, bj int }
	var colours [4][]block
	for bj := 0; bj < nb; bj++ {
		for bi := 0; bi < nb; bi++ {
			colours[(bj%2)*2+bi%2] = append(colours[(bj%2)*2+bi%2], block{bi, bj})
		}
	}
	counts := make([]int, nb*nb)
	for bj := 0; bj < nb; bj++ {
		for bi := 0; bi < nb; bi++ {
			w := min(B, sz-1-bi*B)
			hh := min(B, sz-1-bj*B)
			if w <= 0 || hh <= 0 {
				continue
			}
			counts[bj*nb+bi] = int(math.Round(perPass * float64(w*hh) / float64((sz-1)*(sz-1))))
		}
	}
	for _, n := range counts {
		ran += n * passes
	}
	doneRounds := 0
	for pass := 0; pass < passes; pass++ {
		for col := 0; col < 4; col++ {
			blocks := colours[col]
			err := pool.Parallel(len(blocks), func(bk int) error {
				b := blocks[bk]
				n := counts[b.bj*nb+b.bi]
				if n == 0 {
					return nil
				}
				rng := noise.NewRNG(noise.Hash64(s.Seed, 0x4452_4F50, uint64(pass), uint64(b.bj*nb+b.bi)))
				bx0, bz0 := float64(b.bi*B), float64(b.bj*B)
				bw := float64(min(B, sz-1-b.bi*B))
				bh := float64(min(B, sz-1-b.bj*B))
				// The droplets' allowed region, kept 1 + brush clear of the
				// edge so bilinear reads and brush writes stay in the grid.
				lo := float64(ri + 1)
				hiLim := float64(sz-2-ri) - 1e-3
				rx0 := max(bx0-float64(M), lo)
				rz0 := max(bz0-float64(M), lo)
				rx1 := min(bx0+bw+float64(M), hiLim)
				rz1 := min(bz0+bh+float64(M), hiLim)
				for q := 0; q < n; q++ {
					if q&255 == 0 {
						if err := pool.Err(); err != nil {
							return err
						}
					}
					x := bx0 + rng.Float64()*bw
					z := bz0 + rng.Float64()*bh
					if x < rx0 || x > rx1 || z < rz0 || z > rz1 {
						continue
					}
					var dx, dz float32
					vel, water, sed := float32(1), float32(1), float32(0)
					for st := 0; st < steps; st++ {
						ht, gx, gz := heightGrad(h, sz, x, z)
						dx = dx*inertia - gx*(1-inertia)
						dz = dz*inertia - gz*(1-inertia)
						l := float32(math.Sqrt(float64(dx*dx + dz*dz)))
						if l < 1e-9 {
							a := rng.Float64() * 2 * math.Pi
							dx, dz = float32(math.Cos(a)), float32(math.Sin(a))
						} else {
							dx, dz = dx/l, dz/l
						}
						nx, nz := x+float64(dx), z+float64(dz)
						i, j := int(x), int(z)
						u, v := float32(x-float64(i)), float32(z-float64(j))
						k := j*sz + i
						if nx < rx0 || nx > rx1 || nz < rz0 || nz > rz1 {
							// Leaving the block's reach: drop the load here.
							depositAt(h, deposit, sz, k, u, v, sed, ht)
							break
						}
						nh, _, _ := heightGrad(h, sz, nx, nz)
						dh := nh - ht
						slope := -dh / cell
						// Capacity and load are VOLUMES (m³), and the rates
						// are per metre run, so a finer grid takes more,
						// smaller steps to the same effect.
						capacity := max(slope, minSlope) * vel * water * capF
						if sed > capacity || dh > 0 {
							var amt float32
							if dh > 0 {
								amt = min(dh*cellArea, sed)
							} else {
								amt = (sed - capacity) * depRate
							}
							left := depositAt(h, deposit, sz, k, u, v, amt/cellArea, ht)
							sed -= amt - left*cellArea
						} else {
							er := c.erodibility(float64(ht + off[k]))
							amt := (capacity - sed) * eroRate * er
							if amt > 0 {
								// From the brush about the nearest sample,
								// never cutting a sample below where the
								// droplet is going: no pits.
								kc := int(z+0.5)*sz + int(x+0.5)
								m := amt / cellArea
								for _, t := range brush {
									q := kc + t.dj*sz + t.di
									take := min(m*t.w, max(h[q]-nh, 0))
									h[q] -= take
									sed += take * cellArea
								}
							}
						}
						// Speed from the fall, with drag: it settles at about
						// sqrt(4.3·slope·Gravity) rather than growing without
						// end down a long slope.
						vel = float32(math.Sqrt(float64(max(vel*vel+slope*grav, 0)))) * 0.9
						water *= 1 - evap
						x, z = nx, nz
					}
				}
				return nil
			})
			if err != nil {
				return 0, err
			}
			doneRounds++
			if progress != nil {
				progress(float32(doneRounds) / float32(passes*4))
			}
		}
	}
	return ran, nil
}

// depositAt adds amt to the four samples around a droplet at height ht,
// raising none of them above it (no spikes), and returns what it could not
// place.
func depositAt(h, deposit []float32, s, k int, u, v, amt, ht float32) float32 {
	if amt <= 0 {
		return 0
	}
	w := [4]float32{(1 - u) * (1 - v), u * (1 - v), (1 - u) * v, u * v}
	idx := [4]int{k, k + 1, k + s, k + s + 1}
	left := amt
	for q := 0; q < 4; q++ {
		a := min(amt*w[q], max(ht-h[idx[q]], 0))
		h[idx[q]] += a
		deposit[idx[q]] += a
		left -= a
	}
	return max(left, 0)
}
