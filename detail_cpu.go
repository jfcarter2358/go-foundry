package main

// detail_cpu.go — CPU-side evaluation of the procedural face-displacement
// field, ported 1:1 from detail_common.glsl (same hashes, same wave sum,
// same edge taper) so code that never touches the GPU — biome_ground.go's
// ground-patch mesh, grass.go's scatter height/coverage texture — can drop
// onto a displaced face's *actual* surface and read its *actual* local slope,
// instead of drifting from what the tessellator draws. See
// [[procedural-face-detail]] / [[biome-scatter-feature]].
//
// Every function here takes the same per-face inputs the GLSL uniforms would
// carry (dispNormal, boundary edges, dispNormalEps — the last two already
// exist as plain Go functions in detail_render.go, no GL calls involved) plus
// a world position, and is a pure function of those — exactly like its GLSL
// counterpart, so two callers evaluating the same face at the same position
// always agree.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// smoothstep32 mirrors GLSL's smoothstep(edge0, edge1, x).
func smoothstep32(edge0, edge1, x float32) float32 {
	if edge0 == edge1 {
		if x < edge0 {
			return 0
		}
		return 1
	}
	t := clamp01((x - edge0) / (edge1 - edge0))
	return t * t * (3 - 2*t)
}

func fract32(v float32) float32 {
	return v - float32(math.Floor(float64(v)))
}

// detailHash13 mirrors d_hash13 (detail_common.glsl).
func detailHash13(p mgl32.Vec3) float32 {
	p = mgl32.Vec3{
		fract32(p.X()*0.3183099 + 0.1),
		fract32(p.Y()*0.3183099 + 0.2),
		fract32(p.Z()*0.3183099 + 0.3),
	}
	d := p.X()*(p.Y()+19.19) + p.Y()*(p.Z()+19.19) + p.Z()*(p.X()+19.19)
	p = p.Add(mgl32.Vec3{d, d, d})
	return fract32((p.X() + p.Y()) * p.Z())
}

// detailValueNoise3 mirrors d_vnoise: trilinearly-interpolated 3D value noise
// in [0,1], smoothstepped per axis.
func detailValueNoise3(x mgl32.Vec3) float32 {
	i := mgl32.Vec3{float32(math.Floor(float64(x.X()))), float32(math.Floor(float64(x.Y()))), float32(math.Floor(float64(x.Z())))}
	f := x.Sub(i)
	u := mgl32.Vec3{f.X() * f.X() * (3 - 2*f.X()), f.Y() * f.Y() * (3 - 2*f.Y()), f.Z() * f.Z() * (3 - 2*f.Z())}

	n000 := detailHash13(i.Add(mgl32.Vec3{0, 0, 0}))
	n100 := detailHash13(i.Add(mgl32.Vec3{1, 0, 0}))
	n010 := detailHash13(i.Add(mgl32.Vec3{0, 1, 0}))
	n110 := detailHash13(i.Add(mgl32.Vec3{1, 1, 0}))
	n001 := detailHash13(i.Add(mgl32.Vec3{0, 0, 1}))
	n101 := detailHash13(i.Add(mgl32.Vec3{1, 0, 1}))
	n011 := detailHash13(i.Add(mgl32.Vec3{0, 1, 1}))
	n111 := detailHash13(i.Add(mgl32.Vec3{1, 1, 1}))

	nx00 := n000 + (n100-n000)*u.X()
	nx10 := n010 + (n110-n010)*u.X()
	nx01 := n001 + (n101-n001)*u.X()
	nx11 := n011 + (n111-n011)*u.X()
	nxy0 := nx00 + (nx10-nx00)*u.Y()
	nxy1 := nx01 + (nx11-nx01)*u.Y()
	return nxy0 + (nxy1-nxy0)*u.Z()
}

// detailFBM3 mirrors d_fbm: fractal value noise in [-1,1], octaves clamped to
// detail_common.glsl's DETAIL-side 1..6 (mirrored by DetailSpec.clamp()).
func detailFBM3(p mgl32.Vec3, octaves int) float32 {
	if octaves < 1 {
		octaves = 1
	}
	if octaves > 6 {
		octaves = 6
	}
	sum := float32(0)
	amp := float32(0.5)
	freq := float32(1.0)
	for i := 0; i < octaves; i++ {
		sum += amp * (detailValueNoise3(p.Mul(freq))*2 - 1)
		freq *= 2
		amp *= 0.5
	}
	return sum
}

// detailHash21 mirrors d_hash21, used only for a wave slot's phase.
func detailHash21(p mgl32.Vec2) float32 {
	v := math.Sin(float64(p.X()*127.1+p.Y()*311.7)) * 43758.5453
	return fract32(float32(v))
}

// detailPlaneBasis mirrors d_planeBasis: the face's canonical in-plane axes
// derived purely from its displacement normal n, so wave rotation is
// authored the same way on floors/ceilings/walls alike. Deliberately NOT the
// same basis faceBasisFor/computeFaceBasis returns (that one's tangent comes
// from the face's first edge, used only to align the scatter lattice) — the
// two are independent in the shaders too.
func detailPlaneBasis(n mgl32.Vec3) (t, b mgl32.Vec3) {
	if math.Abs(float64(n.Y())) < 0.9 {
		t = mgl32.Vec3{0, 1, 0}.Cross(n)
	} else {
		t = mgl32.Vec3{1, 0, 0}.Cross(n)
	}
	t = t.Normalize()
	b = n.Cross(t).Normalize()
	return
}

// detailWaveSumAt mirrors d_waveSum at world position pos.
func detailWaveSumAt(d *DetailSpec, n mgl32.Vec3, pos mgl32.Vec3) float32 {
	t, b := detailPlaneBasis(n)
	s := float32(math.Mod(float64(d.Seed), 1024.0))

	sum := float32(0)
	for i := 0; i < detailMaxWaves; i++ {
		w := d.Waves[i]
		if !w.Enabled || w.Amplitude == 0 || w.Frequency == 0 {
			continue
		}
		fi := float32(i)
		phase := (detailHash21(mgl32.Vec2{s, fi}) + fi/float32(detailMaxWaves)) * 2 * math.Pi
		rot := float64(w.Rotation) * math.Pi / 180
		dir := t.Mul(float32(math.Cos(rot))).Add(b.Mul(float32(math.Sin(rot))))
		sum += w.Amplitude * float32(math.Sin(float64(pos.Dot(dir)*w.Frequency*2*math.Pi+phase)))
	}
	return sum
}

// detailTaperAt mirrors detailTaper, given the face's boundary edges (see
// boundaryEdgesFor in detail_render.go).
func detailTaperAt(d *DetailSpec, edges [][2]mgl32.Vec3, pos mgl32.Vec3) float32 {
	if d.EdgeTaper <= 0 || len(edges) == 0 {
		return 1
	}
	md := float32(math.MaxFloat32)
	for _, e := range edges {
		if dist := distPointSeg(pos, e[0], e[1]); dist < md {
			md = dist
		}
	}
	return smoothstep32(0, d.EdgeTaper, md)
}

// distPointSeg mirrors d_distSeg: distance from p to segment a..b.
func distPointSeg(p, a, b mgl32.Vec3) float32 {
	ab := b.Sub(a)
	denom := ab.Dot(ab)
	if denom < 1e-8 {
		denom = 1e-8
	}
	t := clamp01(p.Sub(a).Dot(ab) / denom)
	return p.Sub(a.Add(ab.Mul(t))).Len()
}

// detailDisplaceAt mirrors detailDisplace: pos pushed along n by the summed
// waves plus (when enabled) seeded fractal noise, tapered at the face
// boundary.
func detailDisplaceAt(d *DetailSpec, n mgl32.Vec3, edges [][2]mgl32.Vec3, pos mgl32.Vec3) mgl32.Vec3 {
	disp := detailWaveSumAt(d, n, pos)
	if d.Noise {
		off := mgl32.Vec3{float32(d.Seed) * 13.1, float32(d.Seed) * 7.7, float32(d.Seed) * 3.3}
		disp += d.NoiseAmplitude * detailFBM3(pos.Mul(d.NoiseFrequency).Add(off), d.NoiseOctaves)
	}
	return pos.Add(n.Mul(disp * detailTaperAt(d, edges, pos)))
}

// detailNormalAt mirrors detailNormal: the displaced surface's unit normal at
// pos, by central-differencing detailDisplaceAt across the face plane. eps is
// the differencing step — pass dispNormalEps(d) (detail_render.go).
func detailNormalAt(d *DetailSpec, n mgl32.Vec3, edges [][2]mgl32.Vec3, eps float32, pos mgl32.Vec3) mgl32.Vec3 {
	t, b := detailPlaneBasis(n)
	e := eps
	if e < 1e-4 {
		e = 1e-4
	}
	du := detailDisplaceAt(d, n, edges, pos.Add(t.Mul(e))).Sub(detailDisplaceAt(d, n, edges, pos.Sub(t.Mul(e))))
	dv := detailDisplaceAt(d, n, edges, pos.Add(b.Mul(e))).Sub(detailDisplaceAt(d, n, edges, pos.Sub(b.Mul(e))))
	nn := du.Cross(dv)
	if l := nn.Len(); l > 1e-8 {
		return nn.Mul(1 / l)
	}
	return n
}

// detailSurfaceAt returns the displaced world height and unit normal for fl
// at world XZ (wx,wz), given the flat floor's own height baseHeight (from
// InterpolateSurfaceHeight — used as the Y component fed into the wave/noise
// field, matching the GPU's own use of the undisplaced vertex position).
// Callers must check fl.Detail.HasDisplace() first; an unconfigured spec
// would just return (baseHeight, {0,1,0}) here, which is a legitimate but
// pointless answer to bother computing.
func detailSurfaceAt(fl *Floor, wx, wz, baseHeight float32) (height float32, normal mgl32.Vec3) {
	d := &fl.Detail
	n := dispNormalFor(&fl.Geometry)
	edges := boundaryEdgesFor(&fl.Geometry)
	pos := mgl32.Vec3{wx, baseHeight, wz}
	disp := detailDisplaceAt(d, n, edges, pos)
	norm := detailNormalAt(d, n, edges, dispNormalEps(d), pos)
	return disp.Y(), norm
}
