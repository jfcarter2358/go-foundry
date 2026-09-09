package render

// scatter_mesh.go — the template meshes a scatter volume instances.
//
// One small mesh per kind per LOD tier, uploaded once at startup and drawn
// with glDrawArraysInstanced; the vertex shader generates every instance's
// placement from gl_InstanceID (see scatter.go). Nothing here knows where a
// volume is or what it contains — these are shapes, and the placement code is
// the other file's business.
//
// Ported unchanged from the painted-biome renderer. The meshes were never the
// part that knew about polygons.

import (
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// ScatterVertexStride is floats per template vertex: aLocal(3) aNormal(3)
// aParam(1).
const ScatterVertexStride = 7

// Template vertex attribute locations. Fixed with explicit layout qualifiers in
// every scatter vertex shader rather than queried, because one VAO is drawn by
// all three programs and glGetAttribLocation is free to return different
// numbers for each.
const (
	scatterAttrLocal  = 0
	scatterAttrNormal = 1
	scatterAttrParam  = 2
)

// uploadScatterMesh creates a VAO/VBO for a template mesh laid out as
// [aLocal.xyz, aNormal.xyz, aParam] per vertex.
func uploadScatterMesh(verts []float32) (vao, vbo uint32) {
	gl.GenVertexArrays(1, &vao)
	gl.GenBuffers(1, &vbo)
	gl.BindVertexArray(vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.STATIC_DRAW)

	stride := int32(ScatterVertexStride * 4)
	gl.EnableVertexAttribArray(scatterAttrLocal)
	gl.VertexAttribPointerWithOffset(scatterAttrLocal, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(scatterAttrNormal)
	gl.VertexAttribPointerWithOffset(scatterAttrNormal, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(scatterAttrParam)
	gl.VertexAttribPointerWithOffset(scatterAttrParam, 1, gl.FLOAT, false, stride, 6*4)

	gl.BindVertexArray(0)
	return vao, vbo
}

// BuildBladeMesh returns a tapered grass blade as a triangle list, occupying
// the full unit height/width. Local y runs 0..1 (scaled to world height by
// bladeHeight in the shader); aParam carries the height fraction (0..1) so
// the shader can bend/light along the blade. Normal is unused for grass (lit
// as up-facing) but kept for a uniform vertex layout.
func BuildBladeMesh() []float32 {
	return buildTaperedStem(1.0, 1.0)
}

// transformMeshXZ returns a copy of verts (ScatterVertexStride-per-vertex
// layout) rotated by yaw around Y and translated by (dx, dz) in the local XZ
// plane — the tool every Far-tier "clump" template below uses to bake several
// sub-instances of an existing builder into one static mesh, the same
// "concatenate transformed copies" trick BuildWildflowerMesh already uses to
// combine stem + bloom + center + leaves, just applied to a whole sub-mesh at
// once instead of one part. Y and aParam (the 7th float, which encodes the
// sub-mesh's own part/height banding) are left untouched; the normal is
// rotated alongside the position so a rock/moss sub-blob's lighting stays
// consistent with where it actually ends up (foliage ignores its own normal
// entirely — see biome_scatter_common.glsl's bNormal = vec3(0,1,0) — so this
// is a no-op there, but load-bearing for buildMossClumpMesh).
func transformMeshXZ(verts []float32, dx, dz, yaw float32) []float32 {
	ca, sa := float32(math.Cos(float64(yaw))), float32(math.Sin(float64(yaw)))
	out := make([]float32, len(verts))
	copy(out, verts)
	for i := 0; i < len(out); i += ScatterVertexStride {
		x, z := out[i+0], out[i+2]
		out[i+0] = x*ca - z*sa + dx
		out[i+2] = x*sa + z*ca + dz
		nx, nz := out[i+3], out[i+5]
		out[i+3] = nx*ca - nz*sa
		out[i+5] = nx*sa + nz*ca
	}
	return out
}

// grassClumpBlades/grassClumpOffsetLocal shape the Far-tier "clump" template
// (buildGrassClumpMesh) that stands in for several individual blades at once
// — see grass.go's package doc comment on the three-tier LOD system.
// offsetLocal is in the SAME local-mesh units a blade's own width taper is
// (NOT world units): the vertex shader scales a stem's local.xz by the
// sector's bladeWidth uniform exactly like it scales the taper itself, so
// baking the offset at that same local scale is what keeps a clump's
// footprint sane at the kind's default Blade Width — it grows or shrinks a
// little with that slider the same way a single blade's own thickness does,
// rather than needing a dedicated uniform of its own.
const (
	grassClumpBlades      = 5
	grassClumpOffsetLocal = float32(4.0)
)

// buildGrassClumpMesh returns a small baked cluster of grassClumpBlades
// simplified (lodStemSegs) blades fanned around the origin at varied
// radii/heights — the Far tier's "single sprite", deliberately a GROUP of
// blades rather than one simplified blade, so a sparse far-tier lattice still
// reads as a full field instead of thinning out. Every sub-blade keeps its
// own aParam height fraction, so wind sway still animates it individually;
// the instance's own per-lattice yaw/scale then rotates/scales the whole
// clump as one rigid prop, exactly like any other template.
func buildGrassClumpMesh() []float32 {
	var v []float32
	for i := 0; i < grassClumpBlades; i++ {
		fi := float64(i)
		a := fi/grassClumpBlades*2*math.Pi + fi*0.7 // deliberately not evenly spaced
		radius := grassClumpOffsetLocal * (0.55 + 0.45*float32(math.Sin(fi*2.1+0.3)))
		dx := float32(math.Cos(a)) * radius
		dz := float32(math.Sin(a)) * radius
		heightFrac := 0.8 + 0.35*float32(math.Cos(fi*1.3))
		blade := buildTaperedStemN(heightFrac, 1.0, lodStemSegs)
		v = append(v, transformMeshXZ(blade, dx, dz, float32(a)+1.3)...)
	}
	return v
}

// stemSegs is how many quads a Near-tier blade/stem is split into along its
// length. Wind bend and the clump arc are evaluated per vertex, so this is
// what makes a bent stem read as a curve rather than a hinge. Mid/Far LOD
// tiers deliberately use far fewer (see lodStemSegs) — the segment count is
// what a distant blade's silhouette can least afford, since it's paid for by
// every scattered instance at once.
const stemSegs = 5

// lodStemSegs is the segment count for Mid-tier single-instance templates and
// each sub-blade of a Far-tier clump template (buildGrassClumpMesh and
// friends) — cheap enough that a Far clump's several sub-blades together
// still cost less than one Near-tier blade.
const lodStemSegs = 2

// buildTaperedStem returns a tapered blade as a triangle list, scaled to
// heightFrac of the unit height and widthFrac of the unit width, at the
// Near-tier's full stemSegs resolution. aParam carries the height fraction
// (0..heightFrac) for wind bend.
//
// The taper to a point is what makes this read as a BLADE, so it's grass's
// shape only — a wildflower or cattail stalk is an even-thickness stem with
// the interesting geometry at its top, and uses buildStraightStem instead.
func buildTaperedStem(heightFrac, widthFrac float32) []float32 {
	return buildStem(heightFrac, widthFrac, true, stemSegs)
}

// buildStraightStem returns a constant-width stem as a triangle list — the
// flower/reed stalk, at the Near-tier's full stemSegs resolution. widthFrac
// is the stem's half-width the whole way up rather than just at its base, so
// the panel's Stem Width slider reads as the thickness you actually see.
func buildStraightStem(heightFrac, widthFrac float32) []float32 {
	return buildStem(heightFrac, widthFrac, false, stemSegs)
}

// buildTaperedStemN/buildStraightStemN are buildTaperedStem/buildStraightStem
// with an explicit segment count, for the Mid/Far LOD tiers' cheaper
// templates (see lodStemSegs) — everything else about the shape is identical.
func buildTaperedStemN(heightFrac, widthFrac float32, segs int) []float32 {
	return buildStem(heightFrac, widthFrac, true, segs)
}

func buildStraightStemN(heightFrac, widthFrac float32, segs int) []float32 {
	return buildStem(heightFrac, widthFrac, false, segs)
}

// buildStem is the shared body of the four above: a quad column of segs
// segments, either tapering to a point at the tip or holding widthFrac all the
// way up.
func buildStem(heightFrac, widthFrac float32, taper bool, segs int) []float32 {
	if segs < 1 {
		segs = 1
	}
	fsegs := float32(segs)
	var v []float32
	push := func(x, y float32) {
		v = append(v, x, y, 0, 0, 0, 1, y) // aLocal, aNormal(0,0,1), aParam=y
	}
	halfWidth := func(i int) float32 {
		if !taper {
			return widthFrac
		}
		return (1 - float32(i)/fsegs) * widthFrac // tapering to 0 at the tip
	}
	for i := 0; i < segs; i++ {
		t0 := float32(i) / fsegs * heightFrac
		t1 := float32(i+1) / fsegs * heightFrac
		w0, w1 := halfWidth(i), halfWidth(i+1)
		// Quad (L0,R0,R1,L1) as two triangles.
		push(-w0, t0)
		push(w0, t0)
		push(w1, t1)
		push(-w0, t0)
		push(w1, t1)
		push(-w1, t1)
	}
	return v
}

// buildBlobMesh returns a small faceted blob (see buildRockMesh) scaled and
// translated for use as a template's "head" — a wildflower bloom or cattail
// seed head — every vertex tagged with param so the shader can color/light it
// differently from the stem it sits on (see biome_vert.glsl's aParam
// encoding: [0,1) stem, [2,3) wildflower head, [3,4) cattail head).
func buildBlobMesh(sides int, cx, cy, cz, rx, ry, rz, param float32) []float32 {
	base := buildRockMesh(sides)
	for i := 0; i < len(base); i += ScatterVertexStride {
		base[i+0] = base[i+0]*rx + cx
		base[i+1] = base[i+1]*ry + cy
		base[i+2] = base[i+2]*rz + cz
		base[i+6] = param
	}
	return base
}

// WildflowerHeadAnchor/CattailHeadAnchor are the fraction of the stem's
// world height (bladeHeight) the head sits above — MUST match the matching
// constants in biome_vert.glsl, which apply this offset after scaling the
// head by the (independently configurable) headScale uniform. The head's own
// local geometry (below) is left centered at the origin — radius-only, no
// baked translation — so the shader can grow/shrink it around a fixed anchor
// point without the anchor itself drifting.
//
// These double as the stem's own height (see BuildWildflowerMesh /
// BuildCattailMesh), so the stalk runs all the way up to the point its head
// hangs from and the head swallows the tip. The stem used to stop a little
// short — 0.75 under a 0.84 anchor, 0.88 under a 0.95 one — and the head's own
// radius was left to bridge that gap. It only just did at the default head
// sizes, and not at all once the gap grew or the head shrank: the gap scales
// with the stem's world height (bladeHeight) while the head is sized
// absolutely (headScale), so a tall reed or a small seed head left the corn
// dog floating over open air. Anchoring both to one number makes the joint
// hold at every combination instead of at the ones it was tuned for.
const (
	WildflowerHeadAnchor = 0.84
	CattailHeadAnchor    = 0.95
)

// Wildflower bloom proportions, applied to the mesh's unit bloom blob by the
// shader's bloomAspect uniform (see BuildWildflowerMesh). Keeping these here
// rather than baked into the template is what lets one VAO serve both
// silhouettes — BiomeBrush.BloomSpike just selects which one is uploaded.
// WildflowerDiscAspect reproduces the radii the bloom used to be built with,
// so a brush with BloomSpike off looks exactly as it did before the toggle.
var (
	WildflowerDiscAspect  = mgl32.Vec3{0.85, 0.24, 0.85}
	WildflowerSpikeAspect = mgl32.Vec3{0.34, 1.15, 0.34}
)

// aParam tags identifying the parts of a foliage template to the shader (see
// the aParam-encoding note in biome_vert.glsl): the stem occupies [0,1], and
// each head part gets its own integer band so the shader can size and color
// it separately.
const (
	WildflowerBloomParam  = 2.0
	CattailHeadParam      = 3.0
	WildflowerCenterParam = 4.0
	WildflowerLeafParam   = 5.0
)

// Basal-leaf geometry for a wildflower (BiomeBrush.StemLeaves). Every one of
// these is a fraction of the STEM'S OWN HEIGHT, on every axis including the
// radial ones — the shader scales the whole leaf uniformly by that height (see
// biome_vert.glsl's isLeaf branch), so a knee-high flower and an ankle-high one
// stay in proportion instead of the short one drowning under leaves sized for
// the tall one.
//
// The shape is a triangle — full width where it meets the stem, tapering to a
// point — swept along an arc that rises steeply off the base and then bends
// back over, so the leaf curves outward and the tip nods down rather than
// sticking out as a flat spike. leafBend is the angle swept through that arc:
// anything past pi/2 puts the peak before the tip, which is what produces the
// bend-back.
const (
	wildflowerLeafCount = 3
	wildflowerLeafSegs  = 3
	WildflowerLeafLen   = float32(0.55) // radial reach
	wildflowerLeafWidth = float32(0.09) // half-width where it meets the stem
	wildflowerLeafBaseY = float32(0.05) // where it leaves the stem
	wildflowerLeafRise  = float32(0.34) // peak height of the arc above leafBaseY
	wildflowerLeafBend  = 2.6           // radians swept through that arc
	wildflowerLeafYaw   = 0.4           // radians, so the fan doesn't align with anything
)

// BuildWildflowerLeaves returns the wildflower's basal leaves as a triangle
// list, tagged in the WildflowerLeafParam band so the shader can scale, color
// and (when BiomeBrush.StemLeaves is off) collapse them independently of the
// stem they ring. Proportions are documented on the constants above.
//
// Like the bloom center, the leaves live in the one shared template and are
// degenerated away when switched off rather than getting a VAO of their own —
// a zero-area triangle never reaches rasterization, so an unleafed wildflower
// pays for them in the vertex shader only.
func BuildWildflowerLeaves() []float32 {
	var v []float32
	for k := 0; k < wildflowerLeafCount; k++ {
		a := float64(k)/wildflowerLeafCount*2*math.Pi + wildflowerLeafYaw
		// Radial axis (the leaf's length) and tangential axis (its width).
		dx, dz := float32(math.Cos(a)), float32(math.Sin(a))
		tx, tz := -dz, dx

		// at returns the leaf's centerline radius/height and its half-width a
		// fraction s of the way from the stem to the tip.
		at := func(s float32) (r, y, w float32) {
			r = WildflowerLeafLen * s
			y = wildflowerLeafBaseY + wildflowerLeafRise*float32(math.Sin(float64(s)*wildflowerLeafBend))
			w = wildflowerLeafWidth * (1 - s)
			return
		}
		// side is -1/+1 for the two edges of the leaf at that point. aParam is
		// the leaf band plus the vertex's own height as a fraction of the stem,
		// which is what the shader bends by in the wind — so a leaf sways by
		// how high off the ground it is, like the stem does.
		push := func(r, y, w, side float32) {
			v = append(v, dx*r+tx*w*side, y, dz*r+tz*w*side,
				0, 1, 0, WildflowerLeafParam+y)
		}
		for i := 0; i < wildflowerLeafSegs; i++ {
			s0 := float32(i) / wildflowerLeafSegs
			s1 := float32(i+1) / wildflowerLeafSegs
			r0, y0, w0 := at(s0)
			r1, y1, w1 := at(s1)
			push(r0, y0, w0, -1)
			push(r0, y0, w0, 1)
			push(r1, y1, w1, 1)
			push(r0, y0, w0, -1)
			push(r1, y1, w1, 1)
			push(r1, y1, w1, -1)
		}
	}
	return v
}

// BuildWildflowerMesh returns a thin, even-thickness green stem topped with a
// bloom and a small separate bloom center, ringed at its base by the optional
// leaves (BiomeBrush.StemLeaves, see BuildWildflowerLeaves). The bloom is
// colored in-shader from a
// per-instance hue palette (tinted by the brush color) rather than baked into
// the mesh, so one sector reads as a mix of flower colors instead of a single
// hue — unless BloomSolidColor is on, in which case the brush color is used
// directly. Its world size is independently configurable
// (BiomeBrush.HeadScale) since its local geometry isn't scaled by the stem's
// bladeWidth/bladeHeight — see the anchor note.
//
// Both the bloom and the center are built as UNIT blobs: their actual
// proportions come from the shader's bloomAspect uniform (disc vs spike, see
// WildflowerDiscAspect/WildflowerSpikeAspect) and a fixed center aspect, so
// one template covers every combination of the bloom toggles. The center is
// always present in the mesh and collapsed to a degenerate (zero-area, so
// never rasterized) triangle in the shader when BloomCenter is off — cheaper
// and simpler than maintaining a second VAO for the two-part variant. The
// basal leaves are carried and switched off the same way.
func BuildWildflowerMesh() []float32 {
	v := buildStraightStem(WildflowerHeadAnchor, 0.55)
	v = append(v, buildBlobMesh(6, 0, 0, 0, 1, 1, 1, WildflowerBloomParam)...)
	v = append(v, buildBlobMesh(5, 0, 0, 0, 1, 1, 1, WildflowerCenterParam)...)
	v = append(v, BuildWildflowerLeaves()...)
	return v
}

// buildWildflowerMeshSimple is the Mid-tier wildflower: a reduced-segment
// stem (lodStemSegs) plus the bloom that makes it recognizable as a flower,
// with the optional center/leaves parts dropped ENTIRELY rather than
// degenerated by a uniform toggle the way Near's optional parts are — a real
// vertex-count saving instead of just a fill-rate one. Also the building
// block buildWildflowerClumpMesh bakes several copies of for the Far tier.
func buildWildflowerMeshSimple() []float32 {
	v := buildStraightStemN(WildflowerHeadAnchor, 0.55, lodStemSegs)
	v = append(v, buildBlobMesh(6, 0, 0, 0, 1, 1, 1, WildflowerBloomParam)...)
	return v
}

// wildflowerClumpCount/wildflowerClumpOffsetLocal shape the Far-tier clump
// template — see grassClumpOffsetLocal's doc comment for why the offset is
// expressed in local (not world) units.
const (
	wildflowerClumpCount       = 3
	wildflowerClumpOffsetLocal = float32(3.0)
)

// buildWildflowerClumpMesh bakes wildflowerClumpCount simplified flowers
// (buildWildflowerMeshSimple) together into one Far-tier "clump" prop — see
// buildGrassClumpMesh's doc comment for the reasoning (a group, not one
// simplified flower, so a sparse far lattice still reads as a full patch).
func buildWildflowerClumpMesh() []float32 {
	var v []float32
	for i := 0; i < wildflowerClumpCount; i++ {
		fi := float64(i)
		a := fi/wildflowerClumpCount*2*math.Pi + 0.6
		radius := wildflowerClumpOffsetLocal * (0.6 + 0.4*float32(math.Cos(fi*1.9)))
		dx := float32(math.Cos(a)) * radius
		dz := float32(math.Sin(a)) * radius
		v = append(v, transformMeshXZ(buildWildflowerMeshSimple(), dx, dz, float32(a)+2.1)...)
	}
	return v
}

// BuildCattailMesh returns a tall thin reed — an even-thickness stalk, not a
// tapered blade — topped with an elongated brown seed head (reusing the
// rock/blob generator, stretched along Y for the characteristic "corn dog"
// silhouette). See BuildWildflowerMesh's note on independent head sizing.
//
// The elongation MUST live here in the template's own local radii. It used to
// come for free from the head sharing the stem's transform, which scales X/Z
// by bladeWidth (~0.03) and Y by bladeHeight (~1.1) — a ~37:1 stretch that
// turned even a squat blob into a spike. Once HeadScale made the head scale
// uniformly (so it grows around a fixed anchor instead of dragging the anchor
// with it), that free anisotropy was gone and the seed head flattened into a
// disc. Keep ry well above rx/rz or the corn dog silhouette is lost again.
func BuildCattailMesh() []float32 {
	v := buildStraightStem(CattailHeadAnchor, 0.35)
	v = append(v, buildBlobMesh(7, 0, 0, 0, 0.30, 1.15, 0.30, CattailHeadParam)...)
	return v
}

// buildCattailMeshSimple is the Mid-tier cattail: a reduced-segment stem
// (lodStemSegs) plus a slightly cheaper seed head — see
// buildWildflowerMeshSimple's doc comment, same reasoning. Also the building
// block buildCattailClumpMesh bakes several copies of for the Far tier.
func buildCattailMeshSimple() []float32 {
	v := buildStraightStemN(CattailHeadAnchor, 0.35, lodStemSegs)
	v = append(v, buildBlobMesh(5, 0, 0, 0, 0.30, 1.15, 0.30, CattailHeadParam)...)
	return v
}

// cattailClumpReeds/cattailClumpOffsetLocal shape the Far-tier clump template
// — see grassClumpOffsetLocal's doc comment for why the offset is expressed
// in local (not world) units.
const (
	cattailClumpReeds       = 3
	cattailClumpOffsetLocal = float32(2.4)
)

// buildCattailClumpMesh bakes cattailClumpReeds simplified reeds
// (buildCattailMeshSimple) together into one Far-tier "clump" prop — see
// buildGrassClumpMesh's doc comment for the reasoning.
func buildCattailClumpMesh() []float32 {
	var v []float32
	for i := 0; i < cattailClumpReeds; i++ {
		fi := float64(i)
		a := fi/cattailClumpReeds*2*math.Pi + 1.1
		radius := cattailClumpOffsetLocal * (0.7 + 0.3*float32(math.Sin(fi*1.7)))
		dx := float32(math.Cos(a)) * radius
		dz := float32(math.Sin(a)) * radius
		v = append(v, transformMeshXZ(buildCattailMeshSimple(), dx, dz, float32(a)+0.5)...)
	}
	return v
}

// mossClumpSink is how far (in unit-template space, so it scales with the
// brush's clump size) the moss blob is pushed below the origin, leaving it
// mostly buried in the surface it sits on.
//
// This is load-bearing, not cosmetic. The instance is planted at a single
// height sampled from the height texture, but the clump is a wide, near-flat
// shape drawn with only a random yaw — no tilt toward the surface normal (it
// shares the rock path, and rocks are chunky enough not to care). On uneven
// ground a horizontal disc therefore cannot lie flush: its rim lifts off
// wherever the surface falls away, which is exactly the "discs floating in
// thin air" look. Sinking it means the visible cap is the only part above the
// surface, so the ground closes over the rim regardless of local slope, and
// residual sub-cell mismatch between the coarse patch mesh and the finer
// height texture is absorbed too.
const mossClumpSink = float32(0.12)

// BuildMossMesh returns a small faceted clump — the same blob generator as a
// rock (buildRockMesh), squashed into a low wide mound and sunk into the
// ground (see mossClumpSink) so it reads as moss growing on the surface
// rather than a boulder resting on it. Kept a mound rather than a flat disc
// on purpose: a disc reads as a floating plate the moment the ground under it
// isn't level.
//
// Drawn with shapeKind==1 (Rock) unchanged (see renderBiomeSector's BiomeMoss
// case), so it gets the same per-instance warp and per-fragment
// noise-bump/specular shading a rock does — the squashed, sunken silhouette
// is the only thing distinguishing it from scattered stones.
func BuildMossMesh() []float32 {
	return buildBlobMesh(6, 0, -mossClumpSink, 0, 1.15, 0.45, 1.15, 0)
}

// buildMossMeshMid is the Mid-tier moss clump: the same shape at a cheaper
// facet count (4 sides instead of Near's 6).
func buildMossMeshMid() []float32 {
	return buildBlobMesh(4, 0, -mossClumpSink, 0, 1.15, 0.45, 1.15, 0)
}

// mossFarClumpBlobs/mossFarClumpOffsetLocal shape the Far-tier "clump of
// clumps" template. Unlike the foliage kinds' offsetLocal constants, this one
// needs no "tuned for the kind's default width" caveat: moss draws through
// the rock path (shapeKind==1), whose rockScale uniform scales local x/y/z
// uniformly, so an offset baked in the same local units as the blob's own
// radius scales exactly in step with the brush's Clump Size slider.
const (
	mossFarClumpBlobs       = 3
	mossFarClumpOffsetLocal = float32(1.6)
)

// buildMossClumpMesh bakes mossFarClumpBlobs small blobs together into one
// Far-tier prop — see buildGrassClumpMesh's doc comment for the reasoning (a
// group of clumps, not one simplified clump, so a sparse far lattice still
// reads as continuous ground cover).
func buildMossClumpMesh() []float32 {
	var v []float32
	for i := 0; i < mossFarClumpBlobs; i++ {
		fi := float64(i)
		a := fi/mossFarClumpBlobs*2*math.Pi + 0.4
		dx := float32(math.Cos(a)) * mossFarClumpOffsetLocal
		dz := float32(math.Sin(a)) * mossFarClumpOffsetLocal
		blob := buildBlobMesh(5, 0, -mossClumpSink, 0, 0.9, 0.4, 0.9, 0)
		v = append(v, transformMeshXZ(blob, dx, dz, float32(a))...)
	}
	return v
}

// uploadRG32F (re)uploads a two-channel float texture with the given
// filtering — used for the biome height/coverage texture (height in .r,
// slope-coverage in .g; see ensureBiomeHeight).

// buildRockMesh returns a small faceted rock/blob as a triangle list with
// per-face normals for flat shading. It's a two-ring "barrel" over an n-gon
// cross-section (top apex → upper ring → lower ring → bottom apex); each
// ring's vertices are independently perturbed in both radius and height by a
// few summed sine harmonics at different phases. A single equatorial wobble
// (the old bipyramid) reads as a smooth "angular sphere" — two
// independently-wobbled rings break that symmetry into a lumpier, more
// rock-like silhouette. Per-instance variety on top of this shared template
// comes from a further warp in the vertex shader keyed off each instance's
// scatter hash, so no two scattered rocks are identical even though they
// share one VAO — detail from the shader, not the geometry.
func buildRockMesh(sides int) []float32 {
	if sides < 3 {
		sides = 3
	}
	top := mgl32.Vec3{0, 0.85, 0}
	bot := mgl32.Vec3{0, -0.55, 0}

	ring := func(y0, rBase float32, phaseOff float64) []mgl32.Vec3 {
		pts := make([]mgl32.Vec3, sides)
		for i := 0; i < sides; i++ {
			a := float64(i)/float64(sides)*2*math.Pi + phaseOff
			r := rBase * float32(1+0.22*math.Sin(a*3)+0.12*math.Sin(a*5+1.7))
			y := y0 + float32(0.10*math.Sin(a*2+0.6)+0.06*math.Cos(a*4))
			pts[i] = mgl32.Vec3{r * float32(math.Cos(a)), y, r * float32(math.Sin(a))}
		}
		return pts
	}
	upper := ring(0.25, 0.95, 0)
	lower := ring(-0.15, 0.8, 1.1)

	var v []float32
	addFace := func(a, b, c mgl32.Vec3) {
		n := b.Sub(a).Cross(c.Sub(a))
		if n.Len() > 1e-6 {
			n = n.Normalize()
		}
		for _, p := range [3]mgl32.Vec3{a, b, c} {
			v = append(v, p.X(), p.Y(), p.Z(), n.X(), n.Y(), n.Z(), 0)
		}
	}
	for i := 0; i < sides; i++ {
		j := (i + 1) % sides
		addFace(top, upper[i], upper[j])      // upper cap
		addFace(upper[i], lower[i], upper[j]) // side band, tri 1
		addFace(upper[j], lower[i], lower[j]) // side band, tri 2
		addFace(bot, lower[j], lower[i])      // lower cap
	}
	return v
}
