package biome

// biome_displace.go — bridges painted BiomeSectors to a covering floor's
// procedural displacement detail (go/detail_cpu.go), so Grass/Sand/
// Rock/Moss painted over a displaced (rocky/broken) floor drop onto its
// *actual* bumpy surface instead of the flat structural floor, and thin out
// past a configurable slope — e.g. Grass only on a rock outcrop's flatter
// mesa tops, Rock only on its steeper faces. See [[biome-scatter-feature]].
//
// Wildflower/Cattail/Water never follow displacement — painting them over a
// displaced floor keeps the old flat behavior, unaffected by any of this.

import (
	"foundry/internal/detail"
	"foundry/internal/geom"
	"foundry/internal/world"
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

// SlopeFeatherDegrees is how many degrees on either side of
// [MinSlopeAngle,MaxSlopeAngle] the coverage smoothsteps across, so the
// biome layer thins out gradually instead of stopping in a hard, visible
// ring around the qualifying area.
const SlopeFeatherDegrees = float32(4)

// biomeFollowsDisplacement reports whether kind's ground patch / scatter
// should conform to (and be slope-gated by) a covering floor's procedural
// displacement, rather than always sitting on the flat structural floor.
// Scoped to the kinds that plausibly grow over broken/rocky ground.
func BiomeFollowsDisplacement(kind int32) bool {
	switch kind {
	case world.BiomeGrass, world.BiomeRock, world.BiomeSand, world.BiomeMoss:
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
func ResolvedMinSlope(kind int32, v float32) float32 {
	if v > 0 {
		return v
	}
	return defaultMinSlope
}

func ResolvedMaxSlope(kind int32, v float32) float32 {
	if v > 0 {
		return v
	}
	return defaultMaxSlope
}

// slopeCoverage returns how fully a surface with normal n is covered by a
// brush of kind restricted to [minAngle,maxAngle] degrees from world-up,
// smoothstepped SlopeFeatherDegrees on either side of the range. 1 = fully
// covered, 0 = fully excluded, in between across the transition band.
//
// n need NOT be unit length — it is normalized here. That is load-bearing, not
// politeness: InterpolateSurfaceHeight (via collision.go's TriangleNormal)
// hands back a raw cross product, whose length scales with the triangle's
// area. A 16x16 floor quad yields something like (0,256,-128). Reading .Y off
// that and clamping into acos's domain pins every such normal at 0 degrees, so
// gating silently believed every sloped or terrain floor in the level was
// perfectly level. Normalizing is a no-op for the already-unit detail normal.
func slopeCoverage(kind int32, n mgl32.Vec3, minAngle, maxAngle float32) float32 {
	minA := ResolvedMinSlope(kind, minAngle)
	maxA := ResolvedMaxSlope(kind, maxAngle)

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
		lo = detail.Smoothstep32(minA-SlopeFeatherDegrees, minA+SlopeFeatherDegrees, angle)
	}
	hi := float32(1)
	if maxA < 90 {
		hi = 1 - detail.Smoothstep32(maxA-SlopeFeatherDegrees, maxA+SlopeFeatherDegrees, angle)
	}
	return geom.Clamp01(lo * hi)
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
// disagree: the patch applied GroundPatchYBias + GroundDisplacement while the
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
func BiomeSurfaceAt(level *world.Level, brush *world.BiomeBrush, seed mgl32.Vec2, wx, wz float32) (height, coverage float32) {
	flat, rawNormal, sector := FloorHeightNormalUnderLevel(level, wx, wz)

	// InterpolateSurfaceHeight's normal is an un-normalized cross product (see
	// slopeCoverage's note); rotateNormalBetween needs unit inputs to derive a
	// correct axis/angle, so normalize once here.
	structuralNormal := mgl32.Vec3{0, 1, 0}
	if l := rawNormal.Len(); l > 1e-8 {
		structuralNormal = rawNormal.Mul(1 / l)
	}

	base, normal := flat, structuralNormal
	follows := sector != nil && BiomeFollowsDisplacement(brush.Kind)
	if follows && sector.Floor.Detail.HasDisplace() {
		dh, dn := detail.DetailSurfaceAt(&sector.Floor, wx, wz, flat)
		base = dh
		// dn is expressed about the face's *average* plane (that's the frame
		// detailNormalAt differentiates in), so on a bumpy terrain floor it
		// knows about the procedural bumps but not the local ground slope
		// underneath them. Rotating it out of the average plane and into the
		// local structural normal composes the two; for the common case of a
		// planar floor the two frames coincide and this is the identity.
		normal = rotateNormalBetween(detail.DispNormalFor(&sector.Floor.Geometry), structuralNormal, dn)
	}

	// Scoped to the same kinds the panel exposes the sliders for, so a stale
	// persisted angle can't gate a kind whose UI can't show or clear it.
	coverage = 1
	if follows {
		coverage = slopeCoverage(brush.Kind, normal, brush.MinSlopeAngle, brush.MaxSlopeAngle)
	}
	return base + GroundPatchYBias + GroundDisplacement(brush.Kind, brush, seed, wx, wz), coverage
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

// ---------- ground patch geometry ----------
//
// The displacement a ground patch bakes in, and the point set it is built
// from. Both are needed by the water plane too, which has to sit at the same
// height the ground would have — so neither can live in the ground mesher.

// GroundDisplacement returns the extra vertical offset (beyond the
// structural floor height) baked into the ground patch at world (wx,wz),
// keyed by the painting kind — this is the "volumetric displacement" Sand
// asked for, and the subtler unevenness every other land kind gets so its
// patch doesn't look like a flat cutout.
func GroundDisplacement(kind int32, brush *world.BiomeBrush, seed mgl32.Vec2, wx, wz float32) float32 {
	freq := brush.GroundFrequency
	oct := ClampOctaves(brush.GroundOctaves)

	switch kind {
	case world.BiomeSand:
		amp := brush.SandHeight
		if amp <= 0 {
			amp = world.DefaultSandHeight
		}
		if freq <= 0 {
			freq = world.DefaultSandFrequency
		}
		if brush.GroundOctaves <= 0 {
			oct = int(world.DefaultSandOctaves)
		}
		// Mounds only (groundFBM is already ~[0,1], so no *2-1 remap here) —
		// dunes rise from the structural floor, they never dip below it.
		// Signed noise here used to let a large SandHeight carve the patch
		// well below the actual floor mesh at a dune "valley", which read as
		// the sand sinking through the ground. The ripple layer is derived
		// from the primary dune frequency/octaves rather than exposing yet
		// more sliders.
		dunes := groundFBM(wx*freq+seed.X(), wz*freq+seed.Y(), oct) * amp
		rippleFreq := freq * 5.8
		rippleOct := oct - 1
		if rippleOct < 1 {
			rippleOct = 1
		}
		ripples := groundFBM(wx*rippleFreq+seed.X()*1.7, wz*rippleFreq+seed.Y()*1.7, rippleOct) * amp * 0.15
		return dunes + ripples

	case world.BiomeWater:
		amp := brush.GroundAmplitude
		if amp <= 0 {
			amp = world.DefaultWaterGroundAmplitude
		}
		if freq <= 0 {
			freq = world.DefaultWaterGroundFrequency
		}
		if brush.GroundOctaves <= 0 {
			oct = int(world.DefaultWaterGroundOctaves)
		}
		return (groundFBM(wx*freq+seed.X(), wz*freq+seed.Y(), oct)*2 - 1) * amp // gently uneven lakebed

	default: // Grass, Rock, Wildflower, Cattail, Moss
		amp := brush.GroundAmplitude
		if amp <= 0 {
			amp = world.DefaultGroundAmplitude
		}
		if freq <= 0 {
			freq = world.DefaultGroundFrequency
		}
		if brush.GroundOctaves <= 0 {
			oct = int(world.DefaultGroundOctaves)
		}
		return (groundFBM(wx*freq+seed.X(), wz*freq+seed.Y(), oct)*2 - 1) * amp
	}
}

// GroundPatchYBias lifts the patch a hair above the structural floor it
// conforms to, avoiding z-fighting between the two coincident surfaces.
const GroundPatchYBias = float32(0.015)

// biomeMeshPoints builds the point cloud buildGroundPatchMesh/
// BuildBiomeWaterMesh both triangulate: sector s's polygon boundary
// (densified so long edges get their own points to snap to, see
// DensifyPolygon) followed by an interior grid of points strictly inside it.
// Returns the densified boundary too — DelaunayTriangulate needs it both as
// input points and as the inside/outside test polygon.
func BiomeMeshPoints(s *world.BiomeSector) (boundary, allPts []mgl32.Vec2) {
	boundary = CcwPolygon(s.Points)
	if len(boundary) < 3 {
		return nil, nil
	}
	min, max := s.Bounds()
	nx, nz := GroundPatchGridDims(min, max)
	cellX := (max.X() - min.X()) / float32(nx)
	cellZ := (max.Y() - min.Y()) / float32(nz)
	cell := cellX
	if cellZ < cell {
		cell = cellZ
	}
	if cell <= 0 {
		cell = groundPatchCellSize
	}
	boundary = DensifyPolygon(boundary, cell)

	allPts = make([]mgl32.Vec2, 0, len(boundary)+nx*nz)
	allPts = append(allPts, boundary...)
	for iz := 1; iz < nz; iz++ {
		wz := min.Y() + float32(iz)*cellZ
		for ix := 1; ix < nx; ix++ {
			wx := min.X() + float32(ix)*cellX
			p := mgl32.Vec2{wx, wz}
			if geom.PointInPolygon(p, boundary) {
				allPts = append(allPts, p)
			}
		}
	}
	return boundary, allPts
}

// ---------- patch grid and noise ----------
//
// The grid a ground patch is tessellated on and the noise it is displaced by.
// The water plane has to land on the same grid at the same height, so neither
// can be private to the ground mesher.

// ccwPolygon returns pts in counter-clockwise winding (per SignedArea2D),
// reversing a copy if it isn't already — DelaunayTriangulate/EarClip both
// assume CCW. BiomeSector.Points is normalized to CCW when a sector is first
// drawn (finishBiomeSectorDraw), but nothing re-checks it after a vertex is
// dragged, so this is a defensive re-check rather than a known-redundant one.
func CcwPolygon(pts []mgl32.Vec2) []mgl32.Vec2 {
	if world.SignedArea2D(pts) >= 0 {
		return pts
	}
	out := make([]mgl32.Vec2, len(pts))
	for i, p := range pts {
		out[len(pts)-1-i] = p
	}
	return out
}

// ClampOctaves clamps a user-supplied octave count into groundFBM's sane
// range, defaulting a non-positive (unset) value to 1 — callers that need a
// kind-specific default override this afterward when brush.GroundOctaves<=0.
func ClampOctaves(n int32) int {
	if n < 1 {
		n = 1
	}
	if n > 6 {
		n = 6
	}
	return int(n)
}

// DensifyPolygon inserts evenly-spaced intermediate points along every edge
// of pts so no segment exceeds maxSeg, preserving the polygon's exact shape
// (every inserted point is exactly collinear with the edge it splits) and
// winding order.
//
// Feeding this — rather than just the sector's own hand-placed vertices —
// into DelaunayTriangulate as its boundary is what makes the mesh's edge
// follow the polygon exactly: with only the sparse hand-placed vertices as
// boundary points, Bowyer-Watson has nothing to stop it from routing a
// boundary edge through a long diagonal as a staircase of grid-aligned
// chords to nearby interior points instead of the actual polygon edge. Extra
// points ON that edge give it points to snap to along its own line instead.
func DensifyPolygon(pts []mgl32.Vec2, maxSeg float32) []mgl32.Vec2 {
	if len(pts) < 2 || maxSeg <= 0 {
		return pts
	}
	n := len(pts)
	out := make([]mgl32.Vec2, 0, n*2)
	for i := 0; i < n; i++ {
		a := pts[i]
		b := pts[(i+1)%n]
		out = append(out, a)
		d := b.Sub(a)
		steps := int(math.Ceil(float64(d.Len() / maxSeg))) // ceil, not floor: floor could leave a final segment > maxSeg
		if steps > densifyPolygonMaxStepsPerEdge {
			steps = densifyPolygonMaxStepsPerEdge
		}
		for k := 1; k < steps; k++ {
			t := float32(k) / float32(steps)
			out = append(out, a.Add(d.Mul(t)))
		}
	}
	return out
}

// groundFBM sums octaves of groundValueNoise into fractal roughness,
// returning a value roughly in [0,1].
func groundFBM(x, z float32, octaves int) float32 {
	amp := float32(0.55)
	freq := float32(1.0)
	sum := float32(0)
	norm := float32(0)
	for i := 0; i < octaves; i++ {
		sum += groundValueNoise(x*freq, z*freq) * amp
		norm += amp
		amp *= 0.5
		freq *= 2.1
	}
	if norm == 0 {
		return 0
	}
	return sum / norm
}

// groundPatchCellSize is the target grid spacing (world units) — coarse
// relative to the scatter/height-texture resolution, since this is real
// geometry (triangle count matters) rather than a texture sample.
const groundPatchCellSize = float32(2.0)

// GroundPatchGridDims picks the (cells-per-axis) grid resolution covering
// [min,max] at ~groundPatchCellSize world units per cell, capped at
// GroundPatchMaxDim.
func GroundPatchGridDims(lo, hi mgl32.Vec2) (nx, nz int) {
	sizeX := hi.X() - lo.X()
	sizeZ := hi.Y() - lo.Y()
	if sizeX < groundPatchCellSize {
		sizeX = groundPatchCellSize
	}
	if sizeZ < groundPatchCellSize {
		sizeZ = groundPatchCellSize
	}
	nx = int(sizeX/groundPatchCellSize) + 1
	nz = int(sizeZ/groundPatchCellSize) + 1
	nx = max(1, min(nx, GroundPatchMaxDim))
	nz = max(1, min(nz, GroundPatchMaxDim))
	return
}

// densifyPolygonMaxStepsPerEdge caps how many extra points DensifyPolygon
// inserts along any one edge — a safety valve against a pathologically long
// edge with a tiny maxSeg blowing up the triangulation's point count.
const densifyPolygonMaxStepsPerEdge = 200

// GroundPatchMaxDim caps the interior sample grid's cells-per-axis, bounding
// triangle count for a huge sector the same way the scatter passes bound
// instance count. Lower than a pure-grid approach would need, since
// Bowyer-Watson triangulation (see buildGroundPatchMesh) costs more per point.
const GroundPatchMaxDim = 64

// groundValueNoise is bilinearly-interpolated value noise built from
// groundHash, smoothstepped for a soft (not blocky) result.
func groundValueNoise(x, z float32) float32 {
	x0f, z0f := math.Floor(float64(x)), math.Floor(float64(z))
	x0, z0 := float32(x0f), float32(z0f)
	fx, fz := x-x0, z-z0
	fx = fx * fx * (3 - 2*fx)
	fz = fz * fz * (3 - 2*fz)
	h00 := groundHash(x0, z0)
	h10 := groundHash(x0+1, z0)
	h01 := groundHash(x0, z0+1)
	h11 := groundHash(x0+1, z0+1)
	a := h00 + (h10-h00)*fx
	b := h01 + (h11-h01)*fx
	return a + (b-a)*fz
}

// groundHash is a deterministic scalar hash of a world XZ position, in the
// same family as the shaders' hash functions but evaluated on the CPU since
// the ground patch's displacement is baked into the mesh at build time.
func groundHash(x, z float32) float32 {
	v := math.Sin(float64(x)*127.1+float64(z)*311.7) * 43758.5453
	_, frac := math.Modf(v)
	if frac < 0 {
		frac += 1
	}
	return float32(frac)
}
