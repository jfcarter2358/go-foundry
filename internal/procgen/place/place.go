// Package place puts generated rocks on generated ground: the placement half
// of the procedural canyon (docs/canyon/PHASE_3.md), behind the scatter,
// cliff_wall and vegetation stages in internal/procgen/stages.
//
// It is pure computation, like internal/procgen/terrain and rocks beside it:
// it reads a terrain (heights and masks, terrain.Terrain) and the rocks
// stages' variants (their manifest entries, as Variant), and answers
// INSTANCES — a variant, a position, a rotation, a uniform scale and a
// collision mode — which the stages turn into level placements. It imports
// only the terrain package and procgen/noise (internal/layering_test.go
// asserts it), so a test can place ten thousand rocks without a GL context
// or a level.
//
// The three generators:
//
//   - Scatter (scatter.go): rule lists. Each rule weighs the ground by its
//     masks (slope, height, flow, deposition, curvature, cliff, the distance
//     below a cliff), clumps the weight with noise, throws candidates at the
//     density it asks for and keeps them by a Poisson-disk test against every
//     rock already placed, biggest first; sizes follow a power law (many
//     small, few big); each kept rock is tilted toward the ground's normal,
//     yawed and tilted at random, and seated so no part of its base floats.
//   - Walls (wall.go, trace.go): cliff chunks stacked up a wall, in tiers,
//     along a spline or along the foot of every cliff band traced from the
//     cliff mask, backs pushed into the heightfield's stretched wall so the
//     chunks' faces stand in front of it, ledge slabs between tiers.
//   - Vegetation (vegetation.go): the cells of the ground a rule's masks
//     allow, merged into rectangles, for the vegetation stage's biome
//     volumes.
//
// Everything is deterministic: the randomness of a candidate, a column or a
// cell is keyed by the stage's seed and the item's index or coordinates,
// never by which goroutine ran it, so the same document places the same
// rocks on any worker count (tested).
//
// THE ROCKS' FRAME is phase 2's (internal/procgen/rocks, meta.go): +Y up, the
// lowest point at y = 0, the footprint centred on the Y axis; a cliff chunk's
// flat back on z = 0 facing −Z, the rock along +Z. A placement at a ground
// point therefore puts the rock ON it, and a chunk turned so +Z points out of
// a wall has its back to the wall.
package place

import (
	"math"
)

// Variant is one placeable mesh: a rocks stage's variant as its manifest
// describes it (the stage fills it from rocks.VariantMeta). Lengths are in
// the mesh's own units, before a placement's scale.
type Variant struct {
	// Name is the variant's stem (boulder_03); Model the placement's model
	// path (resources-relative).
	Name, Model string
	// Size is the preset's main dimension; Extent the bounding box's size,
	// Height its top (the base is at 0).
	Size   float64
	Extent [3]float64
	Height float64
	// Radius bounds it about +Y; FootprintRadius is what touches the ground
	// (the cylinder of what lies within BaseBand of the base).
	Radius, FootprintRadius, BaseBand float64
	// Top is its upper surface's height (where the next tier sits).
	Top float64
	// Back says it has a back to stand against a wall (a cliff chunk);
	// BackDir is the direction the back faces, in the mesh's frame (zero:
	// −Z, the generated chunks'). A scanned cliff's back is not a plane:
	// the wall code takes the bounding box's face across BackDir as its
	// approximate plane and buries it by WallSettings.Inset.
	Back    bool
	BackDir [3]float64
	// Min and Max bound the mesh in its own frame (zero: from Extent,
	// footprint centred, base at 0).
	Min, Max [3]float64
	// FlatBase says its base was cut flat.
	FlatBase bool
	// Collision is what its cook built (hulls or mesh), PlaceCollision what
	// its stage asks a placement for (hulls, mesh or none).
	Collision, PlaceCollision string
	// Triangles is its level-0 triangle count (the preview's budget, the
	// stage's cost estimate).
	Triangles int
	// BackProfile is how its back surface stands off its box's back plane
	// (backprofile.go), for seating an open-backed scan by its silhouette;
	// nil seats it by the box.
	BackProfile *BackProfile
}

// Instance is one placed rock.
type Instance struct {
	// Rule is the rule (or wall, or tier) it came from, by index; Variant
	// the variant's index in that rule's list.
	Rule, Variant int
	// Pos is its origin in the world; Rot its rotation (row-major, applied
	// to the mesh before Pos); Scale its uniform scale.
	Pos   [3]float64
	Rot   Mat3
	Scale float64
	// Radius is its footprint's radius in the world (spacing, overlap).
	Radius float64
	// Collision is the placement's collision mode.
	Collision string
	// Kind is free for the generator: a wall's chunk or ledge.
	Kind string
}

// Euler is the instance's rotation as a placement's Euler angles.
func (in *Instance) Euler() [3]float64 { return in.Rot.EulerYXZ() }

// Disc is a footprint on the ground: what a later stage keeps clear of.
type Disc struct {
	X, Z, R float64
}

// Disc is the instance's footprint.
func (in *Instance) Disc() Disc { return Disc{in.Pos[0], in.Pos[2], in.Radius} }

// ---------- rotations ----------

// Mat3 is a 3×3 rotation, row-major: m[r][c].
type Mat3 [3][3]float64

// Identity is the identity rotation.
func Identity() Mat3 { return Mat3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}} }

// Mul is a·b (b applied first).
func (a Mat3) Mul(b Mat3) Mat3 {
	var o Mat3
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			o[r][c] = a[r][0]*b[0][c] + a[r][1]*b[1][c] + a[r][2]*b[2][c]
		}
	}
	return o
}

// Apply is m·v.
func (m Mat3) Apply(v [3]float64) [3]float64 {
	return [3]float64{
		m[0][0]*v[0] + m[0][1]*v[1] + m[0][2]*v[2],
		m[1][0]*v[0] + m[1][1]*v[1] + m[1][2]*v[2],
		m[2][0]*v[0] + m[2][1]*v[1] + m[2][2]*v[2],
	}
}

// RotY, RotX and RotZ are rotations about the axes by a radians (right
// handed, as mgl32.HomogRotate3D* builds them).
func RotY(a float64) Mat3 {
	s, c := math.Sincos(a)
	return Mat3{{c, 0, s}, {0, 1, 0}, {-s, 0, c}}
}

func RotX(a float64) Mat3 {
	s, c := math.Sincos(a)
	return Mat3{{1, 0, 0}, {0, c, -s}, {0, s, c}}
}

func RotZ(a float64) Mat3 {
	s, c := math.Sincos(a)
	return Mat3{{c, -s, 0}, {s, c, 0}, {0, 0, 1}}
}

// RotAxis is a rotation by a radians about the unit axis.
func RotAxis(axis [3]float64, a float64) Mat3 {
	s, c := math.Sincos(a)
	t := 1 - c
	x, y, z := axis[0], axis[1], axis[2]
	return Mat3{
		{t*x*x + c, t*x*y - s*z, t*x*z + s*y},
		{t*x*y + s*z, t*y*y + c, t*y*z - s*x},
		{t*x*z - s*y, t*y*z + s*x, t*z*z + c},
	}
}

// FromTo is the shortest rotation taking unit vector a to unit vector b.
func FromTo(a, b [3]float64) Mat3 {
	ax := cross(a, b)
	s := length(ax)
	c := dot(a, b)
	if s < 1e-9 {
		if c > 0 {
			return Identity()
		}
		// Opposite: half a turn about any perpendicular.
		p := cross(a, [3]float64{1, 0, 0})
		if length(p) < 1e-6 {
			p = cross(a, [3]float64{0, 0, 1})
		}
		return RotAxis(normalize(p), math.Pi)
	}
	return RotAxis(scale(ax, 1/s), math.Atan2(s, c))
}

// EulerYXZ is the rotation as Euler angles (x, y, z) in radians such that
// RotY(y)·RotX(x)·RotZ(z) is m — the order world.EulerMatrixYXZ composes a
// scenery placement's rotation in.
func (m Mat3) EulerYXZ() [3]float64 {
	sx := -m[1][2]
	sx = math.Max(-1, math.Min(1, sx))
	x := math.Asin(sx)
	var y, z float64
	if math.Abs(sx) < 0.999999 {
		y = math.Atan2(m[0][2], m[2][2])
		z = math.Atan2(m[1][0], m[1][1])
	} else {
		// Gimbal lock: fold z into y.
		y = math.Atan2(-m[2][0], m[0][0])
		z = 0
	}
	return [3]float64{x, y, z}
}

// ---------- small vector helpers ----------

func dot(a, b [3]float64) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

func cross(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

func length(a [3]float64) float64 { return math.Sqrt(dot(a, a)) }

func scale(a [3]float64, k float64) [3]float64 { return [3]float64{a[0] * k, a[1] * k, a[2] * k} }

func add(a, b [3]float64) [3]float64 { return [3]float64{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }

func normalize(a [3]float64) [3]float64 {
	l := length(a)
	if l == 0 {
		return [3]float64{0, 1, 0}
	}
	return scale(a, 1/l)
}

func smooth(e0, e1, x float64) float64 {
	if e0 == e1 {
		if x < e0 {
			return 0
		}
		return 1
	}
	t := math.Max(0, math.Min(1, (x-e0)/(e1-e0)))
	return t * t * (3 - 2*t)
}

// band is 1 inside [lo, hi], falling to 0 over soft beyond each end; a
// range that covers the whole domain (lo <= dlo, hi >= dhi) is always 1, so
// an edge of the domain is never faded.
func band(x, lo, hi, soft, dlo, dhi float64) float64 {
	w := 1.0
	if lo > dlo {
		w *= smooth(lo-soft, lo, x)
	}
	if hi < dhi {
		w *= 1 - smooth(hi, hi+soft, x)
	}
	return w
}
