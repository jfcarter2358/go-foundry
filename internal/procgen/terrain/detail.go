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

// detail adds the texture to the field: each region's own (its amplitude,
// its noises, at its own hardness), blended across a band by weight.
func detail(fd *field, pool Pool) error {
	g, deposit := fd.g, fd.deposit
	minWave := 2 * g.Cell
	on := false
	for _, mat := range fd.m.mats {
		on = on || (mat.detailAmp > 0 && (14 > minWave || 5 > minWave || mat.jointSp*0.4 > minWave))
	}
	if !on {
		return nil
	}
	sz := g.S()
	m := fd.m
	return rows(pool, sz, func(j int) {
		z := g.Z(j)
		for i := 0; i < sz; i++ {
			k := j*sz + i
			x := g.X(i)
			dep := deposit[k]
			smooth := 1 - dep/(dep+0.25)
			if smooth < 0.02 {
				continue
			}
			if r := m.reg[k]; r < mixed {
				mat := m.mats[r]
				if mat.detailAmp <= 0 {
					continue
				}
				hard := mat.c.hardness(float64(g.H[k] + m.off[k]))
				v, ok := detailAt(mat, x, z, hard, minWave)
				if ok {
					g.H[k] += mat.detailAmp * v * smooth
				}
				continue
			}
			sum, any := float32(0), false
			h := g.H[k]
			for _, e := range m.entries(k) {
				mat := m.mats[e.r]
				if mat.detailAmp <= 0 {
					continue
				}
				hard := mat.c.hardness(float64(h + e.off))
				if v, ok := detailAt(mat, x, z, hard, minWave); ok {
					sum += e.w * mat.detailAmp * v
					any = true
				}
			}
			if any {
				g.H[k] += sum * smooth
			}
		}
	})
}

// detailAt is a material's texture at world (x, z) for a layer of the given
// hardness (before its amplitude), and false when every octave would alias
// at this resolution.
func detailAt(mat *material, x, z float64, hard float32, minWave float64) (float32, bool) {
	useSwell := 14 > minWave
	useCrumb := 5 > minWave
	useCrack := mat.jointSp*0.4 > minWave
	if !useSwell && !useCrumb && !useCrack {
		return 0, false
	}
	x, z = mat.local(x, z)
	v := float32(0)
	if useSwell {
		v += hard * 0.6 * float32(mat.swell.At2(x, z)+1) * 0.5
	}
	if useCrack {
		// Joints run in a set: the cells are stretched 2.5:1 along one
		// bearing, so the cracks are long and roughly parallel with shorter
		// cross-joints, not flagstones.
		js := mat.jointSp
		u := (x*0.8 + z*0.6) / (js * 2.5)
		w := (-x*0.6 + z*0.8) / js
		cl := noise.Cellular2(mat.crackSeed, u, w, 1, noise.MetricEuclidean)
		crack := 1 - noise.SmoothStep(0, 0.05, cl.Edge())
		v -= hard * 0.7 * float32(crack)
	}
	if useCrumb {
		v += (1 - hard) * 0.5 * float32(mat.crumb.At2(x, z))
	}
	return v, true
}
