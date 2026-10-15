package terrain

// tiled.go — erosion in overlapping windows, for incremental regeneration
// (docs/regions/SPEC.md, "Erosion runs either globally or in TILES").
//
// Global erosion is one run over the whole union: a river can cross the
// world, and every sample depends on every other. That is right for a
// finished world and wrong for editing one: move a savannah's hills and the
// canyon three kilometres away regenerates (its droplets' RNG blocks, its
// drainage routing and the lead provider's knobs all shift). Tiled erosion
// trades the long reach for locality:
//
//   - the union is cut into WINDOWS of Window cells (a few terrain tiles —
//     the grid starts at the union's corner, so window edges fall on tile
//     edges), the last one absorbing a remainder too thin to fade;
//   - each window is eroded on its own, over itself plus Margin cells on
//     every side (made square, shifted inward where it meets the union's
//     edge): drainage routes to that square's edge, droplets and talus see
//     only it, and its knobs are its own lead provider's — the region owning
//     most of the window — so a region that does not reach the square
//     cannot change it;
//   - each window's SEED is Hash(Union.Seed, window i, j): fixed by where the
//     window is, not by what is in the world;
//   - the windows are CROSS-FADED: across each interior window boundary a
//     smoothstep ramp Margin cells wide hands one window's heights (and
//     deposits) to the next — a partition of unity, so a sample deep in a
//     window is that window's exactly, and the seam is a margin-wide blend
//     of two erosions of the same ground rather than a step. The discarded
//     half-margin at each square's edge is where its drainage and droplets
//     felt the edge.
//
// A window whose inputs did not change since a kept run is read back from
// Union.Cache instead of eroded (tiled_cache.go): the incremental half of
// tiled erosion.
//
// So a sample's height depends only on the windows whose squares hold it —
// its own and, inside a fade, its neighbour's — and through them only on the
// regions whose shares reach those squares. Long rivers may change course
// within a window; that is the accepted cost (ENVIRONMENT.md, "Erosion
// locality").

import (
	"fmt"
	"time"

	"foundry/internal/procgen/noise"
)

// windowSpans cuts n cells into windows of size w, a remainder thinner than
// minLast merged into the window before it: the cores' [start, end] sample
// indices.
func windowSpans(n, w, minLast int) [][2]int {
	var out [][2]int
	for a := 0; a < n; a += w {
		out = append(out, [2]int{a, min(a+w, n)})
	}
	if len(out) > 1 {
		if last := out[len(out)-1]; last[1]-last[0] < minLast {
			out = out[:len(out)-1]
			out[len(out)-1][1] = n
		}
	}
	return out
}

// fade is a window's weight at sample i along one axis: 1 inside its core,
// ramping over [b - h, b + h] at each interior boundary b.
func fade(i int, core [2]int, first, last bool, h float64) float32 {
	ramp := func(b int) float64 {
		if h <= 0 {
			if i >= b {
				return 1
			}
			return 0
		}
		return noise.SmoothStep(float64(b)-h, float64(b)+h, float64(i))
	}
	w := 1.0
	if !first {
		w *= ramp(core[0])
	}
	if !last {
		w *= 1 - ramp(core[1])
	}
	return float32(w)
}

// erodeTiled erodes fd window by window and cross-fades the results into
// it (file comment).
func erodeTiled(u *Union, fd *field, res *Result, pool Pool, progress func(float32)) error {
	g := fd.g
	fs := g.S()
	n := g.N
	M := max(u.Margin, 0)
	spans := windowSpans(n, max(u.Window, 4), M+1)
	h := float64(M) / 2
	acc := make([]float32, len(g.H))
	dacc := make([]float32, len(g.H))
	inner := func(p Phase, fn func(func(float32)) error) error {
		t0 := time.Now()
		err := fn(func(float32) {})
		res.Timings[p] += time.Since(t0)
		if err == nil {
			err = pool.Err()
		}
		return err
	}
	total := len(spans) * len(spans)
	done := 0
	for wj, cz := range spans {
		for wi, cx := range spans {
			// The window's square: core plus margin, square, inside the union.
			e0x, e1x := max(cx[0]-M, 0), min(cx[1]+M, n)
			e0z, e1z := max(cz[0]-M, 0), min(cz[1]+M, n)
			L := max(e1x-e0x, e1z-e0z)
			sx := min(e0x, n-L)
			sz := min(e0z, n-L)
			ss := L + 1
			sub := &Grid{N: L, Cell: g.Cell, MinX: g.X(sx), MinZ: g.Z(sz), H: make([]float32, ss*ss)}
			dep := make([]float32, ss*ss)
			idx := make([]int, 0, (cx[1]-cx[0]+1)*(cz[1]-cz[0]+1))
			for j := 0; j < ss; j++ {
				src := (sz+j)*fs + sx
				copy(sub.H[j*ss:(j+1)*ss], g.H[src:src+ss])
				copy(dep[j*ss:(j+1)*ss], fd.deposit[src:src+ss])
			}
			for j := cz[0]; j <= cz[1]; j++ {
				for i := cx[0]; i <= cx[1]; i++ {
					idx = append(idx, j*fs+i)
				}
			}
			lead := leadOf(fd.m, idx, len(fd.m.mats))
			k := knobsOf(&u.Providers[lead].Settings)
			k.seed = noise.Hash64(u.Seed, 0x5449_4C45, uint64(wi), uint64(wj))
			k.edgeAware = fd.k.edgeAware
			wf := &field{g: sub, m: fd.m.window(fs, sx, sz, ss), deposit: dep, k: k}
			firstX, lastX := wi == 0, wi == len(spans)-1
			firstZ, lastZ := wj == 0, wj == len(spans)-1
			// A window whose inputs are last run's is read back
			// (tiled_cache.go).
			var key [32]byte
			cacheable := u.Cache != nil
			if cacheable {
				key, cacheable = windowKey(u, wf, sx, sz, ss, wi, wj)
			}
			name := fmt.Sprintf("w_%d_%d", wi, wj)
			x0, x1 := fadeSpan(sx, ss, cx, firstX, lastX, h)
			z0, z1 := fadeSpan(sz, ss, cz, firstZ, lastZ, h)
			cached := false
			if cacheable {
				if data, ok := u.Cache.Load(name, key); ok {
					if nd, ok := decodeWindow(data, sub.H, dep, ss, x0, x1, z0, z1); ok {
						res.Droplets += nd
						cached = true
					}
				}
			}
			if cached {
				res.WindowsCached++
			} else {
				before := res.Droplets
				if err := erodeField(wf, res, inner, pool); err != nil {
					return err
				}
				res.WindowsEroded++
				if cacheable {
					if err := u.Cache.Store(name, key, encodeWindow(sub.H, dep, ss, x0, x1, z0, z1, res.Droplets-before)); err != nil {
						return err
					}
				}
			}
			// Cross-fade into the union.
			err := rows(pool, ss, func(j int) {
				fz := fade(sz+j, cz, firstZ, lastZ, h)
				if fz <= 0 {
					return
				}
				for i := 0; i < ss; i++ {
					w := fade(sx+i, cx, firstX, lastX, h) * fz
					if w <= 0 {
						continue
					}
					q := (sz+j)*fs + sx + i
					acc[q] += w * sub.H[j*ss+i]
					dacc[q] += w * dep[j*ss+i]
				}
			})
			if err != nil {
				return err
			}
			done++
			progress(float32(done) / float32(total))
		}
	}
	copy(g.H, acc)
	copy(fd.deposit, dacc)
	return nil
}
