package sdf

// fracture.go — joints and fractures: the planar breaks rock comes apart
// along.
//
// Rock is jointed. Cooling, unloading and tectonic stress crack it along
// planes, and weathering opens the cracks from the surface down: a sandstone
// cliff is a wall of blocks between vertical joints, a fin is the rock left
// between two of them, a boulder is a block that fell off along three. Two
// models:
//
//   - VORONOI JOINTS (Joints): the rock divided into the cells of jittered
//     points, each cell a convex block with flat faces, stretched per axis —
//     tall cells for columnar joints, flat ones for a bedded slab. The crack
//     is the distance to the nearest cell border, opened by Gap at the
//     surface and closing Depth into the rock, so it is V-shaped as an
//     opened joint is, its lips rounded by Round.
//   - JOINT SETS (JointSet): parallel planes along one normal, spaced and
//     jittered, slightly wavy — the orthogonal sets that cut Arches' fins
//     and the Needles' spires.
//
// Both report the block a point is in (BlockID), so a generator can vary a
// block's colour or relief by it.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// Joints is a Voronoi joint network.
type Joints struct {
	Seed uint64
	// Spacing is the cells' size along each axis, metres.
	Spacing Vec3
	// Jitter 0..1 is how far each cell's point wanders (1: fully random).
	Jitter float64
	// Gap is the crack's half-width at the surface; Depth how far in it
	// closes; Round the radius its lips are rounded over.
	Gap, Depth, Round float64
	// Rotation turns the network (the cells are axis-aligned before it).
	Rotation Mat3
	// Open, when set, opens each crack only where it is positive (fading in
	// over 0..OpenFade): real joints open in places and close in others,
	// and a network open everywhere reads as tiling.
	Open     *Field
	OpenFade float64
	// MinGap closes a crack where it would be narrower than this (a crack
	// finer than the mesh can hold is a dotted line, not a crack).
	MinGap float64
}

// openness is how open a crack is at p under an Open field (1 without one).
func openness(open *Field, fade float64, p Vec3) float64 {
	if open == nil {
		return 1
	}
	if fade <= 0 {
		fade = 0.3
	}
	return SmoothStep(0, fade, open.F(p))
}

// cellPoint is lattice cell c's jittered point, in world units (before the
// rotation).
func (j *Joints) cellPoint(cx, cy, cz int64) (Vec3, uint64) {
	h := noise.Hash64(j.Seed, uint64(cx), uint64(cy), uint64(cz))
	u := func(k uint) float64 { return float64(h>>(k*16)&0xffff)/65536 - 0.5 }
	return Vec3{
		(float64(cx) + 0.5 + j.Jitter*u(0)) * j.Spacing[0],
		(float64(cy) + 0.5 + j.Jitter*u(1)) * j.Spacing[1],
		(float64(cz) + 0.5 + j.Jitter*u(2)) * j.Spacing[2],
	}, h
}

// Border is the distance from p to the nearest cell border (the bisector
// plane between its cell's point and a neighbour's), and its cell's ID.
func (j *Joints) Border(p Vec3) (float64, uint64) {
	d, id, _ := j.BorderPair(p)
	return d, id
}

// BorderPair is Border, and the ID of the cell across that nearest border.
func (j *Joints) BorderPair(p Vec3) (float64, uint64, uint64) {
	q := p
	if j.Rotation != (Mat3{}) {
		q = j.Rotation.T().Apply(p)
	}
	ix := int64(math.Floor(q[0] / j.Spacing[0]))
	iy := int64(math.Floor(q[1] / j.Spacing[1]))
	iz := int64(math.Floor(q[2] / j.Spacing[2]))
	var pts [27]Vec3
	var ids [27]uint64
	best, bi := math.Inf(1), 0
	k := 0
	for dz := int64(-1); dz <= 1; dz++ {
		for dy := int64(-1); dy <= 1; dy++ {
			for dx := int64(-1); dx <= 1; dx++ {
				pt, id := j.cellPoint(ix+dx, iy+dy, iz+dz)
				pts[k], ids[k] = pt, id
				if d := pt.Sub(q).Dot(pt.Sub(q)); d < best {
					best, bi = d, k
				}
				k++
			}
		}
	}
	a := pts[bi]
	border := math.Inf(1)
	other := ids[bi]
	for i := range pts {
		if i == bi {
			continue
		}
		ab := pts[i].Sub(a)
		l := ab.Len()
		if l == 0 {
			continue
		}
		// Distance from q to the bisector plane, on a's side.
		d := a.Add(ab.Mul(0.5)).Sub(q).Dot(ab.Mul(1 / l))
		if d < border {
			border, other = d, ids[i]
		}
	}
	return border, ids[bi], other
}

// BlockValue is a value per cell (unit of its ID, 0..1) blended across the
// borders over width w: within w of a neighbour's border that neighbour's
// value counts, by (1 − distance/w)², against the cell's own weight of 1 —
// so on a border both sides see the same mix, and the result is continuous
// (bar where three cells meet, where it is nearly so).
func (j *Joints) BlockValue(p Vec3, w float64, unit func(id uint64) float64) float64 {
	q := p
	if j.Rotation != (Mat3{}) {
		q = j.Rotation.T().Apply(p)
	}
	ix := int64(math.Floor(q[0] / j.Spacing[0]))
	iy := int64(math.Floor(q[1] / j.Spacing[1]))
	iz := int64(math.Floor(q[2] / j.Spacing[2]))
	var pts [27]Vec3
	var ids [27]uint64
	best, bi := math.Inf(1), 0
	k := 0
	for dz := int64(-1); dz <= 1; dz++ {
		for dy := int64(-1); dy <= 1; dy++ {
			for dx := int64(-1); dx <= 1; dx++ {
				pt, id := j.cellPoint(ix+dx, iy+dy, iz+dz)
				pts[k], ids[k] = pt, id
				if d := pt.Sub(q).Dot(pt.Sub(q)); d < best {
					best, bi = d, k
				}
				k++
			}
		}
	}
	a := pts[bi]
	sum, wsum := unit(ids[bi]), 1.0
	for i := range pts {
		if i == bi {
			continue
		}
		ab := pts[i].Sub(a)
		l := ab.Len()
		if l == 0 {
			continue
		}
		d := a.Add(ab.Mul(0.5)).Sub(q).Dot(ab.Mul(1 / l))
		if d < w {
			kk := (1 - math.Max(d, 0)/w)
			kk *= kk
			sum += kk * unit(ids[i])
			wsum += kk
		}
	}
	return sum / wsum
}

// BlockDisplace pushes each cell of the network j out by its own amount
// (0..amp, from its ID): the face becomes blocks standing proud of and set
// back from their neighbours, each step a plane along the cell border —
// broken rock's relief. The step is blended over width w (BlockValue: both
// sides of a border meet at the mean of the blocks' heights), so the field
// stays continuous with a slope of about amp/w. Several networks at falling
// scales give the fractal fracturing of a real face.
func BlockDisplace(s Shape, j Joints, amp, w float64) Shape {
	f := s.F
	w = math.Max(w, 1e-6)
	box := s.Box
	if !box.IsInfinite() {
		box = box.Grow(amp)
	}
	unit := func(id uint64) float64 { return float64(noise.Mix64(id^0x424c4f43)>>11) / (1 << 53) }
	return Shape{
		F: func(p Vec3) float64 {
			d := f(p)
			if d > amp*1.05 || d < -amp*2 {
				return d - amp*0.5 // far from the surface: any value of the right sign
			}
			return d - amp*j.BlockValue(p, w, unit)
		},
		Box: box,
		Lip: s.Lip + 1.5*amp/w,
	}
}

// BlockID is the ID of the cell p is in.
func (j *Joints) BlockID(p Vec3) uint64 {
	_, id := j.Border(p)
	return id
}

// CutJoints opens the joint network j in s.
func CutJoints(s Shape, j Joints) Shape {
	f := s.F
	depth := math.Max(j.Depth, 1e-6)
	return Shape{
		F: func(p Vec3) float64 {
			d := f(p)
			if d > j.Gap+j.Round {
				return d // well outside: the crack cannot reach
			}
			o := openness(j.Open, j.OpenFade, p)
			if o == 0 || j.Gap*o < j.MinGap {
				return d
			}
			b, _ := j.Border(p)
			gap := j.Gap * o * Clamp(1+d/depth, 0, 1)
			return smaxK(d, gap-b, j.Round*o)
		},
		Box: s.Box,
		// The crack's width changes with the distance: a second unit of
		// slope where the taper is.
		Lip: lip(s.Lip) + j.Gap/depth + 1,
	}
}

// JointSet is a family of parallel joint planes.
type JointSet struct {
	Seed uint64
	// Normal is the planes' normal; Spacing their mean spacing, Jitter
	// 0..1 how irregular it is.
	Normal          Vec3
	Spacing, Jitter float64
	// Waviness is how far the planes wander (metres), over Wave cycles per
	// metre.
	Waviness, Wave float64
	// Gap, Depth, Round, Open and OpenFade are Joints's.
	Gap, Depth, Round float64
	Open              *Field
	OpenFade, MinGap  float64
}

// Plane is the distance from p to the nearest plane of the set, and which
// plane it is.
func (js *JointSet) Plane(p Vec3) (float64, int64) {
	n := js.Normal.Normalize()
	u := p.Dot(n)
	if js.Waviness > 0 {
		u += js.Waviness * noise.Gradient3(js.Seed^0x5741, p[0]*js.Wave, p[1]*js.Wave, p[2]*js.Wave)
	}
	k := int64(math.Floor(u / js.Spacing))
	best, bk := math.Inf(1), k
	for dk := int64(-1); dk <= 1; dk++ {
		h := noise.Hash64(js.Seed, uint64(k+dk))
		at := (float64(k+dk) + 0.5 + js.Jitter*(float64(h&0xffff)/65536-0.5)) * js.Spacing
		if d := math.Abs(u - at); d < best {
			best, bk = d, k+dk
		}
	}
	return best, bk
}

// CutJointSet opens the planes of js in s.
func CutJointSet(s Shape, js JointSet) Shape {
	f := s.F
	depth := math.Max(js.Depth, 1e-6)
	return Shape{
		F: func(p Vec3) float64 {
			d := f(p)
			if d > js.Gap+js.Round {
				return d
			}
			o := openness(js.Open, js.OpenFade, p)
			if o == 0 || js.Gap*o < js.MinGap {
				return d
			}
			b, _ := js.Plane(p)
			gap := js.Gap * o * Clamp(1+d/depth, 0, 1)
			return smaxK(d, gap-b, js.Round*o)
		},
		Box: s.Box,
		Lip: lip(s.Lip) + js.Gap/depth + 1 + 2.5*js.Waviness*js.Wave,
	}
}

// Fracture cuts s by the half-spaces planes (each the side its normal points
// away from), every new edge rounded over round, each face roughened by
// rough·noise of the given frequency — a block broken out of a bed.
func Fracture(s Shape, planes []Shape, round float64, rough Field, roughAmp float64) Shape {
	parts := []Shape{s}
	for _, pl := range planes {
		if roughAmp > 0 {
			pl = Displace(pl, rough, roughAmp)
		}
		parts = append(parts, pl)
	}
	out := SmoothIntersect(round, parts...)
	out.Box = s.Box
	return out
}
