package phimport

// frame.go — a scan in the placement frame phase 2's rocks use, and its
// manifest entry (docs/canyon/PHASE_2.md, "The manifest"):
//
//	+Y up; the lowest point on y = 0; the footprint's bounding box centred on
//	the Y axis; a piece with a BACK has it on z = 0 facing −Z, the rock along
//	+Z, centred in X.
//
// Poly Haven's glTFs are Y-up metres (glTF's convention), so only the
// translation and, for a back, a turn about Y are needed.
//
// # Estimating the back
//
// A cliff or rock-face scan is photographed from the front: what it has of a
// back is a rough, flattish closure (or nothing — an open shell). Either way
// the AREA-WEIGHTED HORIZONTAL NORMAL — Σ area·(nx, nz) over the triangles —
// points out of the face: the front's normals all lean one way and the back,
// being small or flat, cannot cancel them. A boulder scanned all round sums
// to about zero. (The direction is the triangle's vertex normals', its
// weight its area.) So:
//
//	front = normalize(Σ A·n_h), confidence = |Σ A·n_h| / Σ A·|n_h|
//
// and the piece has a back (−front) when the confidence is at least
// BackConfidence. The user can override: none, or the back's direction in
// the file (−z, +z, −x, +x), or a heading in degrees.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"foundry/internal/model"
	"foundry/internal/procgen/rocks"

	"github.com/go-gl/mathgl/mgl32"
)

// BackConfidence is how one-sided the horizontal normals must be for "auto"
// to give a piece a back.
const BackConfidence = 0.25

// BackChoices are the back param's values besides a number of degrees.
var BackChoices = []string{"auto", "none", "-z", "+z", "-x", "+x"}

// FrameResult is what normalising did.
type FrameResult struct {
	// HasBack says the piece has a back (now facing −Z on z = 0).
	HasBack bool
	// Yaw is the turn about +Y applied, radians.
	Yaw float64
	// Confidence is the estimate's (0 when it was given).
	Confidence float64
	// Offset is the translation applied after the turn.
	Offset mgl32.Vec3
}

// horizontalFront is the area-weighted horizontal normal's direction and
// the confidence in it.
func horizontalFront(m *model.ImportMesh) (fx, fz, conf float64) {
	var sx, sz, tot float64
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a, b, c := m.Positions[m.Indices[t]], m.Positions[m.Indices[t+1]], m.Positions[m.Indices[t+2]]
		n := b.Sub(a).Cross(c.Sub(a)) // length = 2·area
		if len(m.Normals) == len(m.Positions) {
			// The authored normals say which way is out (a winding can be
			// either way round in a scan); the cross product gives the area.
			vn := m.Normals[m.Indices[t]].Add(m.Normals[m.Indices[t+1]]).Add(m.Normals[m.Indices[t+2]])
			if l := vn.Len(); l > 1e-6 {
				n = vn.Mul(n.Len() / l)
			}
		}
		nx, nz := float64(n[0]), float64(n[2])
		sx += nx
		sz += nz
		tot += math.Hypot(nx, nz)
	}
	l := math.Hypot(sx, sz)
	if l == 0 || tot == 0 {
		return 0, 1, 0
	}
	return sx / l, sz / l, l / tot
}

// parseBack is the back param: (heading of the back in the file, set, auto).
// The heading is the back direction's angle from +Z toward +X.
func parseBack(s string) (heading float64, set, auto bool, err error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return 0, false, true, nil
	case "none":
		return 0, false, false, nil
	case "-z":
		return math.Pi, true, false, nil
	case "+z", "z":
		return 0, true, false, nil
	case "-x":
		return -math.Pi / 2, true, false, nil
	case "+x", "x":
		return math.Pi / 2, true, false, nil
	}
	d, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "°"), 64)
	if err != nil {
		return 0, false, false, fmt.Errorf("back %q: want auto, none, -z, +z, -x, +x or degrees", s)
	}
	return d * math.Pi / 180, true, false, nil
}

// Normalise puts m into the placement frame in place. back is the param
// (auto, none, ±x/±z, degrees); allowBack false never gives a back (plants,
// boulders).
func Normalise(m *model.ImportMesh, back string, allowBack bool) (FrameResult, error) {
	var fr FrameResult
	heading, set, auto, err := parseBack(back)
	if err != nil {
		return fr, err
	}
	if auto && allowBack {
		fx, fz, conf := horizontalFront(m)
		fr.Confidence = conf
		if conf >= BackConfidence {
			set = true
			heading = math.Atan2(-fx, -fz) // the back is opposite the front
		}
	}
	if set {
		// Turn so the back points to −Z: the back's heading h (from +Z
		// toward +X) must become π. A turn by θ about +Y takes heading h to
		// h + θ (with x' = x cosθ + z sinθ, z' = −x sinθ + z cosθ).
		fr.HasBack = true
		fr.Yaw = math.Remainder(math.Pi-heading, 2*math.Pi)
		c, s := float32(math.Cos(fr.Yaw)), float32(math.Sin(fr.Yaw))
		for i, p := range m.Positions {
			m.Positions[i] = mgl32.Vec3{p[0]*c + p[2]*s, p[1], -p[0]*s + p[2]*c}
		}
		for i, n := range m.Normals {
			m.Normals[i] = mgl32.Vec3{n[0]*c + n[2]*s, n[1], -n[0]*s + n[2]*c}
		}
	}
	lo, hi := bounds(m)
	off := mgl32.Vec3{-(lo[0] + hi[0]) / 2, -lo[1], -(lo[2] + hi[2]) / 2}
	if fr.HasBack {
		off[2] = -lo[2]
	}
	for i := range m.Positions {
		m.Positions[i] = m.Positions[i].Add(off)
	}
	fr.Offset = off
	return fr, nil
}

func bounds(m *model.ImportMesh) (lo, hi mgl32.Vec3) {
	for i, p := range m.Positions {
		if i == 0 {
			lo, hi = p, p
			continue
		}
		for k := 0; k < 3; k++ {
			lo[k] = min(lo[k], p[k])
			hi[k] = max(hi[k], p[k])
		}
	}
	return lo, hi
}

// Measure is m's manifest entry (the rocks stage's VariantMeta, so phase 3's
// scatter and cliff_wall read a scan exactly as a generated rock). The
// caller fills Name, File, Collision, PlaceCollision and the timings.
func Measure(m *model.ImportMesh, hasBack bool) rocks.VariantMeta {
	v := rocks.VariantMeta{Triangles: m.TriangleCount(), Vertices: len(m.Positions)}
	lo, hi := bounds(m)
	v.Min, v.Max = [3]float32(lo), [3]float32(hi)
	for k := 0; k < 3; k++ {
		v.Extent[k] = hi[k] - lo[k]
	}
	v.Height = hi[1]
	v.Size = float64(max(v.Extent[0], v.Extent[1], v.Extent[2]))
	radius := func(ylo, yhi float32) float32 {
		r := 0.0
		for _, p := range m.Positions {
			if p[1] >= ylo && p[1] <= yhi {
				r = math.Max(r, math.Hypot(float64(p[0]), float64(p[2])))
			}
		}
		return float32(r)
	}
	v.Radius = radius(lo[1]-1, hi[1]+1)
	v.BaseBand = float32(math.Min(0.12*float64(v.Height), 0.25*float64(max(v.Extent[0], v.Extent[2]))))
	v.FootprintRadius = radius(lo[1]-1, v.BaseBand)
	for k := 0; k < 3; k++ {
		v.SphereCenter[k] = (lo[k] + hi[k]) / 2
	}
	for _, p := range m.Positions {
		v.SphereRadius = max(v.SphereRadius, p.Sub(mgl32.Vec3(v.SphereCenter)).Len())
	}
	var ups []float32
	for i, n := range m.Normals {
		if n[1] > 0.8 {
			ups = append(ups, m.Positions[i][1])
		}
	}
	if len(ups) > 0 {
		sort.Slice(ups, func(a, b int) bool { return ups[a] < ups[b] })
		v.Top = ups[min(len(ups)-1, len(ups)*9/10)]
	} else {
		v.Top = v.Height
	}
	if hasBack {
		v.Back = &[3]float32{0, 0, -1}
	}
	vol := 0.0
	for t := 0; t+2 < len(m.Indices); t += 3 {
		a, b, c := m.Positions[m.Indices[t]], m.Positions[m.Indices[t+1]], m.Positions[m.Indices[t+2]]
		vol += float64(a.Dot(b.Cross(c))) / 6
	}
	v.Volume = float32(math.Abs(vol)) // an open scan's "volume" is only indicative
	return v
}
