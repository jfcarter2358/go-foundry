package main

// biome_displace.go — bridges painted BiomeSectors to a covering floor's
// procedural displacement detail (detail.go/detail_cpu.go), so Grass/Sand/
// Rock/Moss painted over a displaced (rocky/broken) floor drop onto its
// *actual* bumpy surface instead of the flat structural floor, and thin out
// past a configurable slope — e.g. Grass only on a rock outcrop's flatter
// mesa tops, Rock only on its steeper faces. See [[biome-scatter-feature]].
//
// Wildflower/Cattail/Water never follow displacement — painting them over a
// displaced floor keeps the old flat behavior, unaffected by any of this.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// Default slope-angle gating (degrees from world-up) for a land kind — see
// slopeCoverage.
//
// Every kind defaults to the FULL 0..90 range, i.e. no restriction at all, so
// a freshly-painted sector behaves exactly as it did before slope gating
// existed and the panel's sliders are purely opt-in. An earlier revision
// shipped opinionated per-kind defaults (Rock starting at 20 degrees so it
// would zone itself onto steeper faces than Grass); that was invented policy,
// and once gating applied to ordinary sloped/terrain floors rather than just
// procedurally-displaced ones, it silently deleted Rock from every flat floor
// in the level. Zoning by slope is still entirely possible — it's just
// something the user asks for by moving a slider.
const (
	defaultMinSlope = float32(0)
	defaultMaxSlope = float32(90)
)

// slopeFeatherDegrees is how many degrees on either side of
// [MinSlopeAngle,MaxSlopeAngle] the coverage smoothsteps across, so the
// biome layer thins out gradually instead of stopping in a hard, visible
// ring around the qualifying area.
const slopeFeatherDegrees = float32(4)

// biomeFollowsDisplacement reports whether kind's ground patch / scatter
// should conform to (and be slope-gated by) a covering floor's procedural
// displacement, rather than always sitting on the flat structural floor.
// Scoped to the kinds that plausibly grow over broken/rocky ground.
func biomeFollowsDisplacement(kind int32) bool {
	switch kind {
	case BiomeGrass, BiomeRock, BiomeSand, BiomeMoss:
		return true
	default:
		return false
	}
}

// resolvedMinSlope/resolvedMaxSlope apply the codebase's usual "0 means use
// the default" convention (SandHeight, WaterDepth, HeadScale, ... all follow
// it) to the brush's slope-range fields. The default range covers everything,
// so an unset pair gates nothing — see the defaultMinSlope note.
//
// They take kind so a future kind-specific default has one obvious home, and
// so call sites read the same as the rest of the brush's resolvers.
func resolvedMinSlope(kind int32, v float32) float32 {
	if v > 0 {
		return v
	}
	return defaultMinSlope
}

func resolvedMaxSlope(kind int32, v float32) float32 {
	if v > 0 {
		return v
	}
	return defaultMaxSlope
}

// slopeCoverage returns how fully a surface with normal n is covered by a
// brush of kind restricted to [minAngle,maxAngle] degrees from world-up,
// smoothstepped slopeFeatherDegrees on either side of the range. 1 = fully
// covered, 0 = fully excluded, in between across the transition band.
//
// n need NOT be unit length — it is normalized here. That is load-bearing, not
// politeness: InterpolateSurfaceHeight (via collision.go's triangleNormal)
// hands back a raw cross product, whose length scales with the triangle's
// area. A 16x16 floor quad yields something like (0,256,-128). Reading .Y off
// that and clamping into acos's domain pins every such normal at 0 degrees, so
// gating silently believed every sloped or terrain floor in the level was
// perfectly level. Normalizing is a no-op for the already-unit detail normal.
func slopeCoverage(kind int32, n mgl32.Vec3, minAngle, maxAngle float32) float32 {
	minA := resolvedMinSlope(kind, minAngle)
	maxA := resolvedMaxSlope(kind, maxAngle)

	l := n.Len()
	if l < 1e-8 {
		return 1 // degenerate normal: no basis to exclude anything on
	}
	y := n.Y() / l
	if y > 1 {
		y = 1
	} else if y < -1 {
		y = -1
	}
	angle := float32(math.Acos(float64(y))) * 180 / math.Pi

	lo := float32(1)
	if minA > 0 {
		lo = smoothstep32(minA-slopeFeatherDegrees, minA+slopeFeatherDegrees, angle)
	}
	hi := float32(1)
	if maxA < 90 {
		hi = 1 - smoothstep32(maxA-slopeFeatherDegrees, maxA+slopeFeatherDegrees, angle)
	}
	return clamp01(lo * hi)
}

// biomeSurfaceAt is the single definition of where a biome sector's terrain
// actually is at world XZ, resolved in one chunk/sector lookup:
//
//	height   — the world Y of the *rendered* surface: the top face of the
//	           ground patch. That's the covering floor's height (displaced,
//	           when the floor carries active displacement detail and the kind
//	           follows it — else flat), plus the patch's own z-fight bias and
//	           procedural noise displacement.
//	coverage — 0..1 slope gating, from slopeCoverage of the surface's ACTUAL
//	           normal at this point.
//
// Both consumers go through this and both honour coverage: ensureBiomeHeight's
// height/coverage texture (thinning the scatter) and buildGroundPatchMesh
// (baking it per-vertex, thresholded per-pixel in ground_frag.glsl so the
// painted layer's texture stops at the slope limit too). The patch used to
// ignore coverage on the theory that gating it would "tear holes in the
// terrain" — but a Moss sector is patch-only unless MossScatter is on, so that
// made the slope sliders visibly do nothing at all, and stopping the layer at
// an angle is the whole point of the feature. Keeping it one function is the
// point. The two used to compute height separately and
// disagree: the patch applied groundPatchYBias + groundDisplacement while the
// scatter height did not, so every instance was planted on the bare floor
// *underneath* the visible patch — floating where the patch noise rose,
// sinking where it fell. Tall geometry (a 0.55 grass blade) hid it; a thin
// flat moss clump is shorter than the noise amplitude and read as discs
// hanging in mid-air.
//
// The normal fed to slopeCoverage is whatever actually shapes the ground here,
// which is deliberately broader than procedural detail: a floor can be sloped
// by terrain mode or per-vertex offsets alone (InterpolateSurfaceHeight
// returns a real normal for those), and gating used to consult the detail
// normal only — so on an ordinary ramp or sculpted terrain the slope sliders
// did nothing at all.
func biomeSurfaceAt(level *Level, brush *BiomeBrush, seed mgl32.Vec2, wx, wz float32) (height, coverage float32) {
	flat, rawNormal, sector := floorHeightNormalUnderLevel(level, wx, wz)

	// InterpolateSurfaceHeight's normal is an un-normalized cross product (see
	// slopeCoverage's note); rotateNormalBetween needs unit inputs to derive a
	// correct axis/angle, so normalize once here.
	structuralNormal := mgl32.Vec3{0, 1, 0}
	if l := rawNormal.Len(); l > 1e-8 {
		structuralNormal = rawNormal.Mul(1 / l)
	}

	base, normal := flat, structuralNormal
	follows := sector != nil && biomeFollowsDisplacement(brush.Kind)
	if follows && sector.Floor.Detail.HasDisplace() {
		dh, dn := detailSurfaceAt(&sector.Floor, wx, wz, flat)
		base = dh
		// dn is expressed about the face's *average* plane (that's the frame
		// detailNormalAt differentiates in), so on a bumpy terrain floor it
		// knows about the procedural bumps but not the local ground slope
		// underneath them. Rotating it out of the average plane and into the
		// local structural normal composes the two; for the common case of a
		// planar floor the two frames coincide and this is the identity.
		normal = rotateNormalBetween(dispNormalFor(&sector.Floor.Geometry), structuralNormal, dn)
	}

	// Scoped to the same kinds the panel exposes the sliders for, so a stale
	// persisted angle can't gate a kind whose UI can't show or clear it.
	coverage = 1
	if follows {
		coverage = slopeCoverage(brush.Kind, normal, brush.MinSlopeAngle, brush.MaxSlopeAngle)
	}
	return base + groundPatchYBias + groundDisplacement(brush.Kind, brush, seed, wx, wz), coverage
}

// rotateNormalBetween applies the minimal rotation that takes unit vector
// `from` onto unit vector `to`, to the vector n (Rodrigues). Used to move a
// normal computed in one face frame into another.
func rotateNormalBetween(from, to, n mgl32.Vec3) mgl32.Vec3 {
	axis := from.Cross(to)
	s := axis.Len()
	c := from.Dot(to)
	if s < 1e-6 {
		if c >= 0 {
			return n // already aligned
		}
		return n.Mul(-1) // antiparallel: a 180-degree flip about any perpendicular axis
	}
	k := axis.Mul(1 / s)
	theta := math.Atan2(float64(s), float64(c))
	cosT, sinT := float32(math.Cos(theta)), float32(math.Sin(theta))
	return n.Mul(cosT).
		Add(k.Cross(n).Mul(sinT)).
		Add(k.Mul(k.Dot(n) * (1 - cosT)))
}
