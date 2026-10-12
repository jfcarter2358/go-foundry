package terrain

// detail.go — the fine texture erosion leaves behind and a heightfield
// generator does not otherwise make.
//
// At a metre a sample the eroded surface is still too smooth: real ground
// has a texture set by its rock. So, scaled by Amplitude:
//
//   - HARD layers (by the strata hardness at the sample) get slickrock: low
//     ridged swells, cut by a crack network (the borders of a Voronoi field
//     at JointSpacing, stretched along one bearing so the joints run in a
//     set) — the jointed sandstone pavements of every mesa top and bench;
//   - SOFT layers get a crumbly fine bumpiness, half as high;
//   - DEPOSITS (sand, scree, wash floors) stay smooth: the texture fades out
//     with the deposition.
//
// Octaves shorter than two samples are left out rather than aliased, so a
// coarse preview simply shows less of it.

import (
	"foundry/internal/procgen/noise"
)

func detail(s *Settings, g *Grid, c *compiledStrata, off, deposit []float32, pool Pool) error {
	d := s.Detail
	if d.Amplitude <= 0 {
		return nil
	}
	sz := g.S()
	minWave := 2 * g.Cell
	swell := noise.Fractal{Seed: noise.Hash64(s.Seed, 0x534C_4B52), Octaves: 3, Frequency: 1 / 14.0, Gain: 0.5, Shape: noise.ShapeRidged}
	crumb := noise.Fractal{Seed: noise.Hash64(s.Seed, 0x4352_554D), Octaves: 3, Frequency: 1 / 5.0, Gain: 0.55}
	crackSeed := noise.Hash64(s.Seed, 0x4352_4B53)
	js := max(d.JointSpacing, 0.5)
	useSwell := 14 > minWave
	useCrumb := 5 > minWave
	useCrack := js*0.4 > minWave
	if !useSwell && !useCrumb && !useCrack {
		return nil
	}
	amp := float32(d.Amplitude)
	return rows(pool, sz, func(j int) {
		z := g.Z(j)
		for i := 0; i < sz; i++ {
			k := j*sz + i
			x := g.X(i)
			hard := c.hardness(float64(g.H[k] + off[k]))
			dep := deposit[k]
			smooth := 1 - dep/(dep+0.25)
			if smooth < 0.02 {
				continue
			}
			v := float32(0)
			if useSwell {
				v += hard * 0.6 * float32(swell.At2(x, z)+1) * 0.5
			}
			if useCrack {
				// Joints run in a set: the cells are stretched 2.5:1 along
				// one bearing, so the cracks are long and roughly parallel
				// with shorter cross-joints, not flagstones.
				u := (x*0.8 + z*0.6) / (js * 2.5)
				w := (-x*0.6 + z*0.8) / js
				cl := noise.Cellular2(crackSeed, u, w, 1, noise.MetricEuclidean)
				crack := 1 - noise.SmoothStep(0, 0.05, cl.Edge())
				v -= hard * 0.7 * float32(crack)
			}
			if useCrumb {
				v += (1 - hard) * 0.5 * float32(crumb.At2(x, z))
			}
			g.H[k] += amp * v * smooth
		}
	})
}
