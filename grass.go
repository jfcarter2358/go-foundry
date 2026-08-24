package main

// grass.go — GPU-scattered biome rendering (procedural grass, wildflowers,
// cattails & rock).
//
// Biomes are painted as BiomeSector polygons (see biome_sector.go), each
// carrying its own brush. Nothing is placed as a scene object: at render
// time a small template mesh (a grass blade, a wildflower, a cattail reed,
// or a rock) is drawn with glDrawArraysInstanced, and the vertex shader
// (biome_vert.glsl) generates every instance procedurally from
// gl_InstanceID — scattering it across a camera-centered lattice, testing
// point-in-polygon against the sector's own points (uploaded as a uniform
// array) to decide whether a candidate survives, and sampling a small
// per-sector height texture to drop it onto the structural floor beneath it
// (so scatter follows slopes/elevated floors instead of sitting flat). Cost
// is bounded by the camera ring, not world size.
//
// Grass sectors can additionally gather neighbouring lattice slots into
// plant-like tufts (BiomeBrush.GrassClump) — still the same instances and the
// same draw call, just redistributed and shaped in the vertex shader.
//
// Sand and Water sectors never scatter instances here — Sand is a bare
// ground patch (biome_ground.go) and Water auto-manages a companion
// water plane (biome_water.go). Every land kind (everything but
// Water) additionally renders a conforming, textured, lightly-displaced
// ground patch underneath its scatter, via RenderBiomes calling into
// biome_ground.go.
//
// Each BiomeSector renders as its own instanced pass, so overlapping sectors
// naturally layer (e.g. tall + short grass, or grass with rocks painted on
// top, over the same area) — there is no shared coverage grid to reconcile.
//
// This mirrors the self-contained GizmoProgram pattern (editor_gizmo.go): its
// own GL program and VAOs, never touching the main game shader's state.

import (
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// biomeVertexStride is floats per template vertex: aLocal(3) aNormal(3) aParam(1).
const biomeVertexStride = 7

// Lattice spacing (world units) between scatter slots for each biome's NEAR
// LOD tier (or its only tier, for Rock — see biomeLODTierCount). Painted
// density thins these out; spacing sets the maximum (fully-covered) instance
// count.
const (
	grassSpacing      = 0.35
	rockSpacing       = 2.5
	wildflowerSpacing = 0.6
	cattailSpacing    = 0.75
	mossSpacing       = 0.5 // denser than rock: small clumps, not boulders
)

// Mid/Far LOD tier lattice spacing for the kinds biomeLODTierCount gives 3
// tiers to. Each Far-tier instance is a multi-blade/stem "clump" template
// (buildGrassClumpMesh and friends) rather than one simplified instance, so
// its spacing is wide enough that one clump's own footprint roughly covers
// what several Near instances used to — a sparse far lattice still reads as
// dense scatter instead of thinning out. See grass.go's package doc comment.
const (
	grassMidSpacing   = 0.65
	grassClumpSpacing = 1.7

	wildflowerMidSpacing   = 1.0
	wildflowerClumpSpacing = 2.4

	cattailMidSpacing   = 1.3
	cattailClumpSpacing = 2.8

	mossMidSpacing   = 0.85
	mossClumpSpacing = 1.8
)

// Per-instance world size of a unit template.
const (
	grassBladeHeight = 0.55
)

// Caps on lattice side length so a huge draw range can't explode the instance
// count (gridN*gridN instances) — one trio per LOD-capable kind, Near/Mid/Far.
// Mid/Far caps are lower than Near's: each Far instance already represents
// several Near ones, so it needs far fewer of them to cover the same ground.
const (
	maxGrassGridN      = 320
	maxGrassMidGridN   = 220
	maxGrassClumpGridN = 130

	maxRockGridN = 130

	maxWildflowerGridN      = 260
	maxWildflowerMidGridN   = 180
	maxWildflowerClumpGridN = 100

	maxCattailGridN      = 260
	maxCattailMidGridN   = 180
	maxCattailClumpGridN = 100

	maxMossGridN      = 260
	maxMossMidGridN   = 180
	maxMossClumpGridN = 100
)

// Per-sector height texture resolution/cap.
const (
	biomeHeightTexelSize = float32(0.5)
	biomeMaxTexDim       = 2048
)

// rockMesh is a cached rock template VAO for a given equatorial side count.
type rockMesh struct {
	vao, vbo uint32
	verts    int32
}

// biomeHeightEntry is a cached per-sector height texture and the world-space
// AABB it covers.
type biomeHeightEntry struct {
	tex          uint32
	origin, size mgl32.Vec2
}

// biomeRenderer holds the GL objects and cached per-sector height textures for
// the biome pass.
//
// Three programs rather than one, all compiled from the same placement code
// (biome_scatter_common.glsl) so an instance lands in the same world position
// in every one of them — mirroring detailRenderer's scatter trio. Without that
// sharing, a blade's shadow would drift off the blade the moment either shader
// was tuned.
type biomeRenderer struct {
	color  progUniforms // instanced: color pass
	shadow progUniforms // instanced: directional cascade depth
	cube   progUniforms // instanced: point-light cubemap depth

	// Grass/wildflower/cattail/moss template meshes, ONE PER LOD TIER
	// (index 0=Near, 1=Mid, 2=Far — see biomeLODTierCount and grass.go's
	// package doc comment on the three-tier scatter). Appearance beyond mesh
	// choice is scaled by uniforms, so one mesh per kind per tier suffices.
	// Rock has no LOD tiers and keeps its existing single-VAO-per-side-count
	// cache below, untouched by LOD.
	bladeVAO, bladeVBO           [3]uint32
	bladeVerts                   [3]int32
	wildflowerVAO, wildflowerVBO [3]uint32
	wildflowerVerts              [3]int32
	cattailVAO, cattailVBO       [3]uint32
	cattailVerts                 [3]int32
	mossVAO, mossVBO             [3]uint32
	mossVerts                    [3]int32
	rockMeshes                   map[int32]rockMesh

	// Per-sector height texture cache, keyed by BiomeSector.ID. Rebuilt only
	// when the sector's HeightDirty flag is set.
	heightCache map[string]biomeHeightEntry

	initialized bool
}

// Template vertex attribute locations. Fixed with explicit layout qualifiers in
// every biome vertex shader rather than queried, because one VAO is drawn by
// all three programs and glGetAttribLocation is free to return different
// numbers for each — see uploadBiomeMesh.
const (
	biomeAttrLocal  = 0
	biomeAttrNormal = 1
	biomeAttrParam  = 2
)

var biomes biomeRenderer

// EvictBiomeHeightCache releases the cached height texture for a deleted
// biome sector. Call from the editor when a sector is removed.
func EvictBiomeHeightCache(id string) {
	e, ok := biomes.heightCache[id]
	if !ok {
		return
	}
	t := e.tex
	gl.DeleteTextures(1, &t)
	delete(biomes.heightCache, id)
}

// EvictBiomeSectorCaches releases every GPU resource derived from sector id —
// the scatter height texture, the ground patch mesh, and the companion water
// surface (if any). Call from the editor whenever a sector is deleted or its
// chunk is discarded.
func EvictBiomeSectorCaches(id string) {
	EvictBiomeHeightCache(id)
	EvictGroundPatchCache(id)
}

// GrassInit compiles the biome shader programs and builds the template meshes.
// Must be called once with the GL context current (see EditorInit).
func GrassInit() {
	scatShared := biomeScatterSnippet()
	mk := func(prog uint32) progUniforms { return progUniforms{prog: prog, loc: map[string]int32{}} }

	// Color pass. The two stages get the lighting block at different costs:
	// the vertex stage lights foliage with a 3x3-averaged shadow tap per
	// vertex, the fragment stage lights rock/moss with a 3x3 and full
	// point-light shadows. See biomeLightSnippet.
	//
	// The vertex stage used to sample a SINGLE hard tap (PCF 0) — cheaper,
	// but a boolean in/out test with nothing to smooth it: a blade near a
	// shadow edge would flip fully lit <-> fully shadowed from one frame to
	// the next as the cascade box (necessarily refit to the camera every
	// frame) nudged which texel it landed in, reading as a flicker across
	// the whole plant since one vertex's sample decides the WHOLE blade's
	// color. Averaging over a small neighbourhood turns that hard flip into
	// a soft ramp, so the same per-frame jitter now shifts the blade's
	// shadow a little rather than snapping it — still one evaluation POINT
	// per vertex, just no longer a coin flip, and still far cheaper than
	// fragment.glsl's own per-pixel filter.
	biomes.color = mk(mustBiomeProgram(
		biomeShaderPath("biome_vert.glsl"), biomeShaderPath("biome_frag.glsl"),
		scatShared+"\n"+biomeLightSnippet(1, false, false), biomeLightSnippet(1, true, false)))

	// Depth passes. Placement only — the depth-only fragment stages need no
	// lighting block at all.
	biomes.shadow = mk(mustBiomeProgram(
		biomeShaderPath("biome_shadow_vert.glsl"), biomeShaderPath("shadow_frag.glsl"),
		scatShared, ""))
	biomes.cube = mk(mustBiomeProgram(
		biomeShaderPath("biome_cube_vert.glsl"), biomeShaderPath("shadow_cube_frag.glsl"),
		scatShared, ""))

	// Uploads one LOD tier's template mesh into the given per-kind arrays —
	// see biomeRenderer's doc comment on why those are [3]uint32/[3]int32
	// rather than one VAO per kind.
	uploadTier := func(vao, vbo *[3]uint32, verts *[3]int32, tier int, mesh []float32) {
		verts[tier] = int32(len(mesh) / biomeVertexStride)
		vao[tier], vbo[tier] = uploadBiomeMesh(mesh)
	}

	uploadTier(&biomes.bladeVAO, &biomes.bladeVBO, &biomes.bladeVerts, 0, buildBladeMesh())
	uploadTier(&biomes.bladeVAO, &biomes.bladeVBO, &biomes.bladeVerts, 1, buildTaperedStemN(1.0, 1.0, lodStemSegs))
	uploadTier(&biomes.bladeVAO, &biomes.bladeVBO, &biomes.bladeVerts, 2, buildGrassClumpMesh())

	uploadTier(&biomes.wildflowerVAO, &biomes.wildflowerVBO, &biomes.wildflowerVerts, 0, buildWildflowerMesh())
	uploadTier(&biomes.wildflowerVAO, &biomes.wildflowerVBO, &biomes.wildflowerVerts, 1, buildWildflowerMeshSimple())
	uploadTier(&biomes.wildflowerVAO, &biomes.wildflowerVBO, &biomes.wildflowerVerts, 2, buildWildflowerClumpMesh())

	uploadTier(&biomes.cattailVAO, &biomes.cattailVBO, &biomes.cattailVerts, 0, buildCattailMesh())
	uploadTier(&biomes.cattailVAO, &biomes.cattailVBO, &biomes.cattailVerts, 1, buildCattailMeshSimple())
	uploadTier(&biomes.cattailVAO, &biomes.cattailVBO, &biomes.cattailVerts, 2, buildCattailClumpMesh())

	uploadTier(&biomes.mossVAO, &biomes.mossVBO, &biomes.mossVerts, 0, buildMossMesh())
	uploadTier(&biomes.mossVAO, &biomes.mossVBO, &biomes.mossVerts, 1, buildMossMeshMid())
	uploadTier(&biomes.mossVAO, &biomes.mossVBO, &biomes.mossVerts, 2, buildMossClumpMesh())

	biomes.rockMeshes = map[int32]rockMesh{}
	biomes.heightCache = map[string]biomeHeightEntry{}

	biomes.initialized = true

	GroundInit()
}

// rockMeshFor returns (building & caching if necessary) the rock template for the
// given equatorial side count.
func (br *biomeRenderer) rockMeshFor(sides int32) rockMesh {
	if sides < 3 {
		sides = defaultRockSides
	}
	if m, ok := br.rockMeshes[sides]; ok {
		return m
	}
	verts := buildRockMesh(int(sides))
	vao, vbo := uploadBiomeMesh(verts)
	m := rockMesh{vao: vao, vbo: vbo, verts: int32(len(verts) / biomeVertexStride)}
	br.rockMeshes[sides] = m
	return m
}

// grassTemplate/wildflowerTemplate/cattailTemplate/mossTemplate resolve an
// LOD tier (0=Near, 1=Mid, 2=Far) to the four things renderBiomeSector needs
// to bind and draw a scatter kind's instances with: its template VAO/vertex
// count and that tier's own lattice spacing/instance cap. Kept as one helper
// per kind, rather than folding tier selection into the big per-kind switch
// in renderBiomeSector, so that switch stays close to what it looked like
// before LOD existed.
func (br *biomeRenderer) grassTemplate(tier int) (vao uint32, verts int32, spacing float32, cap int32) {
	switch tier {
	case 1:
		return br.bladeVAO[1], br.bladeVerts[1], grassMidSpacing, maxGrassMidGridN
	case 2:
		return br.bladeVAO[2], br.bladeVerts[2], grassClumpSpacing, maxGrassClumpGridN
	default:
		return br.bladeVAO[0], br.bladeVerts[0], grassSpacing, maxGrassGridN
	}
}

func (br *biomeRenderer) wildflowerTemplate(tier int) (vao uint32, verts int32, spacing float32, cap int32) {
	switch tier {
	case 1:
		return br.wildflowerVAO[1], br.wildflowerVerts[1], wildflowerMidSpacing, maxWildflowerMidGridN
	case 2:
		return br.wildflowerVAO[2], br.wildflowerVerts[2], wildflowerClumpSpacing, maxWildflowerClumpGridN
	default:
		return br.wildflowerVAO[0], br.wildflowerVerts[0], wildflowerSpacing, maxWildflowerGridN
	}
}

func (br *biomeRenderer) cattailTemplate(tier int) (vao uint32, verts int32, spacing float32, cap int32) {
	switch tier {
	case 1:
		return br.cattailVAO[1], br.cattailVerts[1], cattailMidSpacing, maxCattailMidGridN
	case 2:
		return br.cattailVAO[2], br.cattailVerts[2], cattailClumpSpacing, maxCattailClumpGridN
	default:
		return br.cattailVAO[0], br.cattailVerts[0], cattailSpacing, maxCattailGridN
	}
}

func (br *biomeRenderer) mossTemplate(tier int) (vao uint32, verts int32, spacing float32, cap int32) {
	switch tier {
	case 1:
		return br.mossVAO[1], br.mossVerts[1], mossMidSpacing, maxMossMidGridN
	case 2:
		return br.mossVAO[2], br.mossVerts[2], mossClumpSpacing, maxMossClumpGridN
	default:
		return br.mossVAO[0], br.mossVerts[0], mossSpacing, maxMossGridN
	}
}

// boolToInt32 converts a Go bool to the 0/1 an int GLSL uniform expects
// (GLSL bool uniforms are set through glUniform1i either way).
func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// uploadBiomeMesh creates a VAO/VBO for a template mesh laid out as
// [aLocal.xyz, aNormal.xyz, aParam] per vertex.
func uploadBiomeMesh(verts []float32) (vao, vbo uint32) {
	gl.GenVertexArrays(1, &vao)
	gl.GenBuffers(1, &vbo)
	gl.BindVertexArray(vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.STATIC_DRAW)

	stride := int32(biomeVertexStride * 4)
	gl.EnableVertexAttribArray(biomeAttrLocal)
	gl.VertexAttribPointerWithOffset(biomeAttrLocal, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(biomeAttrNormal)
	gl.VertexAttribPointerWithOffset(biomeAttrNormal, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(biomeAttrParam)
	gl.VertexAttribPointerWithOffset(biomeAttrParam, 1, gl.FLOAT, false, stride, 6*4)

	gl.BindVertexArray(0)
	return vao, vbo
}

// buildBladeMesh returns a tapered grass blade as a triangle list, occupying
// the full unit height/width. Local y runs 0..1 (scaled to world height by
// bladeHeight in the shader); aParam carries the height fraction (0..1) so
// the shader can bend/light along the blade. Normal is unused for grass (lit
// as up-facing) but kept for a uniform vertex layout.
func buildBladeMesh() []float32 {
	return buildTaperedStem(1.0, 1.0)
}

// transformMeshXZ returns a copy of verts (biomeVertexStride-per-vertex
// layout) rotated by yaw around Y and translated by (dx, dz) in the local XZ
// plane — the tool every Far-tier "clump" template below uses to bake several
// sub-instances of an existing builder into one static mesh, the same
// "concatenate transformed copies" trick buildWildflowerMesh already uses to
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
	for i := 0; i < len(out); i += biomeVertexStride {
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
	for i := 0; i < len(base); i += biomeVertexStride {
		base[i+0] = base[i+0]*rx + cx
		base[i+1] = base[i+1]*ry + cy
		base[i+2] = base[i+2]*rz + cz
		base[i+6] = param
	}
	return base
}

// wildflowerHeadAnchor/cattailHeadAnchor are the fraction of the stem's
// world height (bladeHeight) the head sits above — MUST match the matching
// constants in biome_vert.glsl, which apply this offset after scaling the
// head by the (independently configurable) headScale uniform. The head's own
// local geometry (below) is left centered at the origin — radius-only, no
// baked translation — so the shader can grow/shrink it around a fixed anchor
// point without the anchor itself drifting.
//
// These double as the stem's own height (see buildWildflowerMesh /
// buildCattailMesh), so the stalk runs all the way up to the point its head
// hangs from and the head swallows the tip. The stem used to stop a little
// short — 0.75 under a 0.84 anchor, 0.88 under a 0.95 one — and the head's own
// radius was left to bridge that gap. It only just did at the default head
// sizes, and not at all once the gap grew or the head shrank: the gap scales
// with the stem's world height (bladeHeight) while the head is sized
// absolutely (headScale), so a tall reed or a small seed head left the corn
// dog floating over open air. Anchoring both to one number makes the joint
// hold at every combination instead of at the ones it was tuned for.
const (
	wildflowerHeadAnchor = 0.84
	cattailHeadAnchor    = 0.95
)

// Wildflower bloom proportions, applied to the mesh's unit bloom blob by the
// shader's bloomAspect uniform (see buildWildflowerMesh). Keeping these here
// rather than baked into the template is what lets one VAO serve both
// silhouettes — BiomeBrush.BloomSpike just selects which one is uploaded.
// wildflowerDiscAspect reproduces the radii the bloom used to be built with,
// so a brush with BloomSpike off looks exactly as it did before the toggle.
var (
	wildflowerDiscAspect  = mgl32.Vec3{0.85, 0.24, 0.85}
	wildflowerSpikeAspect = mgl32.Vec3{0.34, 1.15, 0.34}
)

// aParam tags identifying the parts of a foliage template to the shader (see
// the aParam-encoding note in biome_vert.glsl): the stem occupies [0,1], and
// each head part gets its own integer band so the shader can size and color
// it separately.
const (
	wildflowerBloomParam  = 2.0
	cattailHeadParam      = 3.0
	wildflowerCenterParam = 4.0
	wildflowerLeafParam   = 5.0
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
	wildflowerLeafLen   = float32(0.55) // radial reach
	wildflowerLeafWidth = float32(0.09) // half-width where it meets the stem
	wildflowerLeafBaseY = float32(0.05) // where it leaves the stem
	wildflowerLeafRise  = float32(0.34) // peak height of the arc above leafBaseY
	wildflowerLeafBend  = 2.6           // radians swept through that arc
	wildflowerLeafYaw   = 0.4           // radians, so the fan doesn't align with anything
)

// buildWildflowerLeaves returns the wildflower's basal leaves as a triangle
// list, tagged in the wildflowerLeafParam band so the shader can scale, color
// and (when BiomeBrush.StemLeaves is off) collapse them independently of the
// stem they ring. Proportions are documented on the constants above.
//
// Like the bloom center, the leaves live in the one shared template and are
// degenerated away when switched off rather than getting a VAO of their own —
// a zero-area triangle never reaches rasterization, so an unleafed wildflower
// pays for them in the vertex shader only.
func buildWildflowerLeaves() []float32 {
	var v []float32
	for k := 0; k < wildflowerLeafCount; k++ {
		a := float64(k)/wildflowerLeafCount*2*math.Pi + wildflowerLeafYaw
		// Radial axis (the leaf's length) and tangential axis (its width).
		dx, dz := float32(math.Cos(a)), float32(math.Sin(a))
		tx, tz := -dz, dx

		// at returns the leaf's centerline radius/height and its half-width a
		// fraction s of the way from the stem to the tip.
		at := func(s float32) (r, y, w float32) {
			r = wildflowerLeafLen * s
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
				0, 1, 0, wildflowerLeafParam+y)
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

// buildWildflowerMesh returns a thin, even-thickness green stem topped with a
// bloom and a small separate bloom center, ringed at its base by the optional
// leaves (BiomeBrush.StemLeaves, see buildWildflowerLeaves). The bloom is
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
// wildflowerDiscAspect/wildflowerSpikeAspect) and a fixed center aspect, so
// one template covers every combination of the bloom toggles. The center is
// always present in the mesh and collapsed to a degenerate (zero-area, so
// never rasterized) triangle in the shader when BloomCenter is off — cheaper
// and simpler than maintaining a second VAO for the two-part variant. The
// basal leaves are carried and switched off the same way.
func buildWildflowerMesh() []float32 {
	v := buildStraightStem(wildflowerHeadAnchor, 0.55)
	v = append(v, buildBlobMesh(6, 0, 0, 0, 1, 1, 1, wildflowerBloomParam)...)
	v = append(v, buildBlobMesh(5, 0, 0, 0, 1, 1, 1, wildflowerCenterParam)...)
	v = append(v, buildWildflowerLeaves()...)
	return v
}

// buildWildflowerMeshSimple is the Mid-tier wildflower: a reduced-segment
// stem (lodStemSegs) plus the bloom that makes it recognizable as a flower,
// with the optional center/leaves parts dropped ENTIRELY rather than
// degenerated by a uniform toggle the way Near's optional parts are — a real
// vertex-count saving instead of just a fill-rate one. Also the building
// block buildWildflowerClumpMesh bakes several copies of for the Far tier.
func buildWildflowerMeshSimple() []float32 {
	v := buildStraightStemN(wildflowerHeadAnchor, 0.55, lodStemSegs)
	v = append(v, buildBlobMesh(6, 0, 0, 0, 1, 1, 1, wildflowerBloomParam)...)
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

// buildCattailMesh returns a tall thin reed — an even-thickness stalk, not a
// tapered blade — topped with an elongated brown seed head (reusing the
// rock/blob generator, stretched along Y for the characteristic "corn dog"
// silhouette). See buildWildflowerMesh's note on independent head sizing.
//
// The elongation MUST live here in the template's own local radii. It used to
// come for free from the head sharing the stem's transform, which scales X/Z
// by bladeWidth (~0.03) and Y by bladeHeight (~1.1) — a ~37:1 stretch that
// turned even a squat blob into a spike. Once HeadScale made the head scale
// uniformly (so it grows around a fixed anchor instead of dragging the anchor
// with it), that free anisotropy was gone and the seed head flattened into a
// disc. Keep ry well above rx/rz or the corn dog silhouette is lost again.
func buildCattailMesh() []float32 {
	v := buildStraightStem(cattailHeadAnchor, 0.35)
	v = append(v, buildBlobMesh(7, 0, 0, 0, 0.30, 1.15, 0.30, cattailHeadParam)...)
	return v
}

// buildCattailMeshSimple is the Mid-tier cattail: a reduced-segment stem
// (lodStemSegs) plus a slightly cheaper seed head — see
// buildWildflowerMeshSimple's doc comment, same reasoning. Also the building
// block buildCattailClumpMesh bakes several copies of for the Far tier.
func buildCattailMeshSimple() []float32 {
	v := buildStraightStemN(cattailHeadAnchor, 0.35, lodStemSegs)
	v = append(v, buildBlobMesh(5, 0, 0, 0, 0.30, 1.15, 0.30, cattailHeadParam)...)
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

// buildMossMesh returns a small faceted clump — the same blob generator as a
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
func buildMossMesh() []float32 {
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

// uploadRG32F (re)uploads a two-channel float texture with the given
// filtering — used for the biome height/coverage texture (height in .r,
// slope-coverage in .g; see ensureBiomeHeight).
func uploadRG32F(tex uint32, w, h int, data []float32, linear bool) {
	filter := int32(gl.NEAREST)
	if linear {
		filter = gl.LINEAR
	}
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, filter)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, filter)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RG32F, int32(w), int32(h), 0, gl.RG, gl.FLOAT, gl.Ptr(data))
}

// biomeHeightTexDims picks a texture resolution covering [min,max] at
// ~biomeHeightTexelSize world units per texel, capped at biomeMaxTexDim.
func biomeHeightTexDims(min, max mgl32.Vec2) (w, h int, size mgl32.Vec2) {
	size = mgl32.Vec2{max.X() - min.X(), max.Y() - min.Y()}
	if size.X() < biomeHeightTexelSize {
		size[0] = biomeHeightTexelSize
	}
	if size.Y() < biomeHeightTexelSize {
		size[1] = biomeHeightTexelSize
	}
	w = int(size.X()/biomeHeightTexelSize) + 2
	h = int(size.Y()/biomeHeightTexelSize) + 2
	if w > biomeMaxTexDim {
		w = biomeMaxTexDim
	}
	if h > biomeMaxTexDim {
		h = biomeMaxTexDim
	}
	return
}

// ensureBiomeHeight returns the cached height/coverage texture for sector s,
// rebuilding it from current structural (and, for Grass/Rock/Sand/Moss,
// procedurally-displaced — see biome_displace.go) floor geometry if it's
// missing or HeightDirty. Two channels per texel: .r is the height blades/
// rocks/clumps drop onto, .g is the slope-coverage factor (0..1) the vertex
// shader multiplies into density so instances thin out and vanish past the
// brush's slope range instead of floating on/embedding in a cliff face.
func (br *biomeRenderer) ensureBiomeHeight(level *Level, s *BiomeSector) biomeHeightEntry {
	e, ok := br.heightCache[s.ID]
	if ok && !s.HeightDirty {
		return e
	}
	if !ok {
		gl.GenTextures(1, &e.tex)
	}
	min, max := s.Bounds()
	w, h, size := biomeHeightTexDims(min, max)
	// Same seed the ground patch's own mesh is built with, so both agree on
	// this sector's procedural ground displacement and scatter lands exactly
	// on the patch (see biomeSurfaceAt).
	seed := biomeSeed(s)
	data := make([]float32, w*h*2)
	for ty := 0; ty < h; ty++ {
		v := float32(ty) / float32(h-1)
		wz := min.Y() + v*size.Y()
		for tx := 0; tx < w; tx++ {
			u := float32(tx) / float32(w-1)
			wx := min.X() + u*size.X()
			height, coverage := biomeSurfaceAt(level, &s.Brush, seed, wx, wz)
			i := (ty*w + tx) * 2
			data[i] = height
			data[i+1] = coverage
		}
	}
	uploadRG32F(e.tex, w, h, data, true)
	e.origin = min
	e.size = size
	br.heightCache[s.ID] = e
	s.HeightDirty = false
	return e
}

// biomeLattice returns the lattice side length (gridN) and spacing needed to
// cover the full 2*range square centered on the camera. It prefers baseSpacing,
// but once that would exceed the instance cap it GROWS the spacing so the
// (capped) lattice still spans the whole range.
func biomeLattice(rng, baseSpacing float32, cap int32) (gridN int32, spacing float32) {
	spacing = baseSpacing
	if spacing <= 0 {
		spacing = 0.5
	}
	n := int32(2*rng/spacing) + 2
	if n < 1 {
		n = 1
	}
	if n > cap {
		n = cap
		if n > 1 {
			spacing = (2 * rng) / float32(n-1)
		}
	}
	return n, spacing
}

// resolvedBoundaryJitter applies the usual "0 means the kind's default"
// convention to BoundaryJitter (see BiomeBrush's own doc comment).
func resolvedBoundaryJitter(b *BiomeBrush) float32 {
	if !(b.BoundaryJitter > 0) { // also catches NaN out of a hand-edited save
		return defaultBoundaryJitter
	}
	return b.BoundaryJitter
}

// biomeScatterBounds returns the world box a sector's scatter can occupy this
// frame: its polygon bounds clipped to the camera ring (the scatter exists only
// within rng of the camera, however large the painted sector is), spanning the
// owning chunk vertically since instances are planted on that chunk's floors.
// Used to cull whole sectors out of a shadow cascade.
func biomeScatterBounds(ch *Chunk, s *BiomeSector, camPos mgl32.Vec3, rng float32) (mgl32.Vec3, mgl32.Vec3) {
	pmin, pmax := s.Bounds()
	minX, maxX := pmin.X(), pmax.X()
	minZ, maxZ := pmin.Y(), pmax.Y()
	if v := camPos.X() - rng; v > minX {
		minX = v
	}
	if v := camPos.X() + rng; v < maxX {
		maxX = v
	}
	if v := camPos.Z() - rng; v > minZ {
		minZ = v
	}
	if v := camPos.Z() + rng; v < maxZ {
		maxZ = v
	}
	// maxStemHeight bounds any instance's reach above the surface it sits on;
	// the chunk's own span already covers where that surface can be.
	return mgl32.Vec3{minX, ch.MinBound.Y() - maxStemHeight, minZ},
		mgl32.Vec3{maxX, ch.MaxBound.Y() + maxStemHeight, maxZ}
}

// biomeCastOuterBound returns the outer world-unit bound the CASTING tiers of
// a brush's scatter actually reach — the outer boundary of whichever tier
// BiomeShadows.CastLODTiers cuts off at, not the sector's full Range (which
// only Mid/Far, never-casting-by-default tiers reach beyond). Kinds with a
// single tier (Rock) always cast that whole tier, so this is just
// biomeSectorRange for them.
func biomeCastOuterBound(b *BiomeBrush) float32 {
	tierCount := biomeLODTierCount(b.Kind)
	if tierCount == 1 {
		return biomeSectorRange(b)
	}
	castTiers := BiomeShadows.CastLODTiers
	if castTiers > tierCount {
		castTiers = tierCount
	}
	// 0 (a zero-value/misconfigured settings struct) falls back to the full
	// range rather than casting nothing — AnyCast/CastsKind are what actually
	// gate casting off; this is only ever a cull-box bound.
	switch castTiers {
	case 1:
		return resolvedLOD1(b)
	case 2:
		return resolvedLOD2(b)
	default: // 3, or 0
		return biomeSectorRange(b)
	}
}

// forEachScatterSector visits every biome sector that draws scatter instances.
// castOnly restricts it to the kinds BiomeShadows is currently configured to
// cast from, and cull (when non-nil) drops sectors whose scatter can't reach
// the box being rendered — for a cascade pass that is the cascade's own light
// box, which is what stops a large level re-instancing every painted sector
// into every shadow map. castCap is only consulted when castOnly is set: the
// shadow-map's own reach (a cascade's split distance, or a point light's far
// plane), which combines with the sector's own range and BiomeShadows.CastRange
// (see castRangeFor) to decide both the cull box here and the per-instance
// castRange uniform in renderBiomeSector — the two MUST agree, or an instance
// culled out of the box here could still think it belongs in a map it was
// never drawn into, or vice versa.
func forEachScatterSector(level *Level, camPos mgl32.Vec3, castOnly bool, castCap float32, cull *Frustum, fn func(*BiomeSector)) {
	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		for si := range ch.BiomeSectors {
			s := &ch.BiomeSectors[si]
			if !s.Brush.Scatters() || len(s.Points) < 3 || s.Brush.Density <= 0 {
				continue
			}
			if castOnly && !BiomeShadows.CastsKind(s.Brush.Kind) {
				continue
			}
			if cull != nil {
				rng := biomeSectorRange(&s.Brush)
				if castOnly {
					// Use the outermost CASTING tier's own bound, not the
					// sector's full Range — a sector whose Far tier reaches
					// well past the last casting tier shouldn't inflate the
					// cull box for a shadow pass that will never draw it.
					rng = BiomeShadows.castRangeFor(biomeCastOuterBound(&s.Brush), castCap)
				}
				// BoundaryJitter can push an instance's effective distance up to
				// this many world units past its nominal boundary (see distJ in
				// biome_scatter_common.glsl) — pad the cull box to match, or a
				// jitter-extended instance near the true edge could be culled
				// out of a frustum that never expected it to reach that far.
				rng += resolvedBoundaryJitter(&s.Brush)
				bmin, bmax := biomeScatterBounds(ch, s, camPos, rng)
				if bmin.X() > bmax.X() || bmin.Z() > bmax.Z() {
					continue // sector lies entirely outside the camera ring
				}
				if !AABBInFrustum(*cull, bmin, bmax) {
					continue
				}
			}
			fn(s)
		}
	}
}

// RenderBiomes draws every chunk's painted biome sectors, in two layers:
// first the conforming ground patch under every land kind (biome_ground.go),
// then the GPU-instanced scatter (grass/rock/wildflower/cattail/moss) on top
// of it. Sand/Water sectors only render the ground pass — Sand has no scatter,
// Water's surface is a separate companion plane drawn by the water pass. Moss only scatters when its own MossScatter toggle is on
// (see (*BiomeBrush).Scatters) — otherwise it's ground-patch-only, like Sand.
//
// frame carries the directional cascade setup from RenderShadowPass, which the
// first two passes need in order to receive shadows the way world geometry
// does.
func RenderBiomes(level *Level, projView mgl32.Mat4, camPos mgl32.Vec3, frame ShadowFrame) {
	if !biomes.initialized || level == nil {
		return
	}
	timeSec := BiomeWindTime

	RenderBiomeGround(level, projView, camPos, frame)

	p := &biomes.color
	started := false
	forEachScatterSector(level, camPos, false, 0, nil, func(s *BiomeSector) {
		if !started {
			uploadBiomeLighting(p.prog, level, frame)
			pv := projView
			gl.UniformMatrix4fv(p.u("projView"), 1, false, &pv[0])
			beginBiomePass(p, camPos, timeSec)
			started = true
		}
		for tier := 0; tier < biomeLODTierCount(s.Brush.Kind); tier++ {
			renderBiomeSector(level, s, p, tier, false, 0)
		}
	})

	if started {
		endBiomePass(Program)
	}
}

// RenderBiomeScatterDirShadow draws the biome scatter into the currently-bound
// directional cascade layer using light-space matrix lsm. cascade is the slice
// index being rendered (0 = nearest) and cascadeSplit is the view-space distance
// that cascade's own light box reaches to (frame.Splits[cascade] in
// RenderShadowPass) — both together are what BiomeShadows.CastCascades and
// castRangeFor use to stop small scatter from being re-instanced into a cascade
// its shadow can't usefully reach, or thinned to a range the cascade's own box
// doesn't cover in the first place. Restores Shadows.Program on exit.
func RenderBiomeScatterDirShadow(level *Level, lsm mgl32.Mat4, cascade int, cascadeSplit float32, camPos mgl32.Vec3, timeSec float32, cull *Frustum) {
	if !biomes.initialized || level == nil || !BiomeShadows.AnyCast() {
		return
	}
	if cascade >= BiomeShadows.CastCascades {
		return
	}

	p := &biomes.shadow
	started := false
	forEachScatterSector(level, camPos, true, cascadeSplit, cull, func(s *BiomeSector) {
		if !started {
			gl.UseProgram(p.prog)
			m := lsm
			gl.UniformMatrix4fv(p.u("lightSpaceMatrix"), 1, false, &m[0])
			beginBiomePass(p, camPos, timeSec)
			started = true
		}
		// Only the tiers BiomeShadows.CastLODTiers allows in cast at all —
		// each with its own (cheaper, for Mid/Far) mesh, never Near's. See
		// BiomeShadowSettings.CastLODTiers's own doc comment.
		tiers := BiomeShadows.CastLODTiers
		if tiers < 1 {
			tiers = 1 // a zero-value/misconfigured settings struct still casts Near
		}
		if kindTiers := biomeLODTierCount(s.Brush.Kind); tiers > kindTiers {
			tiers = kindTiers
		}
		for tier := 0; tier < tiers; tier++ {
			renderBiomeSector(level, s, p, tier, true, cascadeSplit)
		}
	})

	if started {
		endBiomePass(Shadows.Program)
	}
}

// RenderBiomeScatterCubeShadow draws the biome scatter into the currently-bound
// point-light cubemap face. farPlane doubles as the cast cap here — a point
// light's own reach already bounds how far an instance could usefully cast into
// it, the same role a cascade's split distance plays in the directional pass.
// Restores Shadows.CubeProgram on exit.
func RenderBiomeScatterCubeShadow(level *Level, lsm mgl32.Mat4, lightPos mgl32.Vec3, farPlane float32, camPos mgl32.Vec3, timeSec float32, cull *Frustum) {
	if !biomes.initialized || level == nil || !BiomeShadows.AnyCast() {
		return
	}

	p := &biomes.cube
	started := false
	forEachScatterSector(level, camPos, true, farPlane, cull, func(s *BiomeSector) {
		if !started {
			gl.UseProgram(p.prog)
			m := lsm
			gl.UniformMatrix4fv(p.u("lightSpaceMatrix"), 1, false, &m[0])
			gl.Uniform3f(p.u("lightPos"), lightPos.X(), lightPos.Y(), lightPos.Z())
			gl.Uniform1f(p.u("farPlane"), farPlane)
			beginBiomePass(p, camPos, timeSec)
			started = true
		}
		// See RenderBiomeScatterDirShadow's matching comment on
		// BiomeShadows.CastLODTiers.
		tiers := BiomeShadows.CastLODTiers
		if tiers < 1 {
			tiers = 1
		}
		if kindTiers := biomeLODTierCount(s.Brush.Kind); tiers > kindTiers {
			tiers = kindTiers
		}
		for tier := 0; tier < tiers; tier++ {
			renderBiomeSector(level, s, p, tier, true, farPlane)
		}
	})

	if started {
		endBiomePass(Shadows.CubeProgram)
	}
}

// beginBiomePass uploads the placement uniforms shared by every sector this
// frame — the camera ring and the wind clock. p must already be the active
// program. Lighting is not touched here: the depth passes have none, and the
// color pass gets it from uploadBiomeLighting.
//
// The wind clock matters in the depth passes too. A blade sways in the color
// pass and would stand still in the shadow map without it, so its shadow would
// detach from it every time the wind moved.
func beginBiomePass(p *progUniforms, camPos mgl32.Vec3, timeSec float32) {
	gl.Uniform2f(p.u("camXZ"), camPos.X(), camPos.Z())
	gl.Uniform3f(p.u("camPos"), camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform1f(p.u("time"), timeSec)
	gl.Uniform1i(p.u("heightTex"), 0)
}

// biomeLODBounds resolves sector s's three LOD boundaries — the Near->Mid and
// Mid->Far distances plus the sector's own outer Range — via
// resolvedLOD1/resolvedLOD2/biomeSectorRange, so every caller (the per-tier
// render loops, the editor panel) agrees on exactly the same numbers a
// sector's draw calls are split across.
func biomeLODBounds(s *BiomeSector) (lod1, lod2, rng float32) {
	rng = biomeSectorRange(&s.Brush)
	lod1 = resolvedLOD1(&s.Brush)
	lod2 = resolvedLOD2(&s.Brush)
	return
}

// renderBiomeSector uploads one sector's polygon/brush/height uniforms to p
// and issues its instanced draw call for LOD tier `tier` (0=Near, 1=Mid,
// 2=Far — ignored, always treated as 0, for kinds with biomeLODTierCount==1
// like Rock). shadowPass shortens the castRange uniform using castCap (a
// cascade split distance or a point light's far plane — see the two
// Render*Shadow callers); everything else is identical between the color and
// depth passes, which is the point — the three programs share their
// placement code (biome_scatter_common.glsl) and must be fed identically for
// a blade's shadow to land on the blade.
func renderBiomeSector(level *Level, s *BiomeSector, p *progUniforms, tier int, shadowPass bool, castCap float32) {
	entry := biomes.ensureBiomeHeight(level, s)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, entry.tex)
	gl.Uniform2f(p.u("heightOrigin"), entry.origin.X(), entry.origin.Y())
	gl.Uniform2f(p.u("heightSize"), entry.size.X(), entry.size.Y())

	n := len(s.Points)
	if n > biomeMaxPolyVerts {
		n = biomeMaxPolyVerts
	}
	flat := make([]float32, n*2)
	for i := 0; i < n; i++ {
		flat[i*2] = s.Points[i].X()
		flat[i*2+1] = s.Points[i].Y()
	}
	gl.Uniform2fv(p.u("polyPts[0]"), int32(n), &flat[0])
	gl.Uniform1i(p.u("numPolyPts"), int32(n))
	polyMin, polyMax := s.Bounds()
	gl.Uniform2f(p.u("polyMin"), polyMin.X(), polyMin.Y())
	gl.Uniform2f(p.u("polyMax"), polyMax.X(), polyMax.Y())

	// This tier's own [inner, outer] distance band. Kinds with only one tier
	// (biomeLODTierCount==1, e.g. Rock) always get [0, rng] — bit-for-bit
	// today's single-tier behavior. LOD-capable kinds split rng into three
	// concentric bands at lod1/lod2 (biomeLODBounds); tier is clamped to
	// whatever the caller actually loops (0 for a single-tier kind).
	lod1, lod2, rng := biomeLODBounds(s)
	tierCount := biomeLODTierCount(s.Brush.Kind)
	inner, outer := float32(0), rng
	if tierCount == 3 {
		switch tier {
		case 0:
			outer = lod1
		case 1:
			inner, outer = lod1, lod2
		default: // 2
			inner = lod2
		}
	}

	// outer drives the lattice half-width and the outer-edge fade (`range`);
	// inner is the new inner-edge grow-in (`lodMin`) — see
	// biome_scatter_common.glsl's doc comments on both. castRange is the
	// shadow passes' extra rejection, applied after placement, relative to
	// THIS tier's own outer bound rather than the sector's full Range — see
	// the castRange uniform doc.
	gl.Uniform1f(p.u("range"), outer)
	gl.Uniform1f(p.u("lodMin"), inner)
	if shadowPass {
		gl.Uniform1f(p.u("castRange"), BiomeShadows.castRangeFor(outer, castCap))
	} else {
		gl.Uniform1f(p.u("castRange"), outer)
	}

	// Optional soft edges (see BiomeBrush's doc comments): the outer
	// camera-ring boundary always had a shrink-based fade (fadeFraction 0
	// disables it, matching the old hard `dist<=range` cutoff exactly); the
	// sector's own painted polygon edge is the newer one, off by default
	// (EdgeFadeDistance 0) since it costs an extra per-instance distance
	// query. Both feed the same shrink-to-a-point transition in the shader.
	//
	// outerIsLOD tells the shader which KIND of fade this tier's own outer
	// edge gets: RangeFadeFraction's shrink only ever means "the sector's own
	// Range" (the last tier, or a kind's only tier) — an inner tier's own
	// outer edge is an LOD split instead, and dithers rather than shrinks
	// (see biome_scatter_common.glsl's lodShrinkOut) so it doesn't gap
	// against the next tier's own dithered grow-in. rangeFadeFraction is
	// still just the user's own RangeFadeFraction unconditionally now — the
	// shader ignores it whenever outerIsLOD says this edge isn't Range.
	gl.Uniform1i(p.u("outerIsLOD"), boolToInt32(tier < tierCount-1))
	gl.Uniform1f(p.u("rangeFadeFraction"), clamp01(s.Brush.RangeFadeFraction))
	gl.Uniform1f(p.u("lodFadeFraction"), biomeLODFadeFraction)
	edgeFade := s.Brush.EdgeFadeDistance
	if edgeFade < 0 {
		edgeFade = 0
	}
	gl.Uniform1f(p.u("edgeFadeDistance"), edgeFade)
	gl.Uniform1f(p.u("boundaryJitter"), resolvedBoundaryJitter(&s.Brush))

	seed := biomeSeed(s)
	gl.Uniform2f(p.u("seed"), seed.X(), seed.Y())
	gl.Uniform1f(p.u("density"), s.Brush.Density)
	c := s.Brush.Color
	gl.Uniform3f(p.u("tintColor"), c.X(), c.Y(), c.Z())
	shapeKind := s.Brush.Kind
	if shapeKind == BiomeMoss {
		// Moss has no shader-side case of its own: it reuses Rock's
		// per-instance warp + per-fragment noise-bump/specular shading
		// (biome_vert/frag.glsl's shapeKind==1 path) unchanged — only the
		// template mesh (mossVAO: small, squashed clumps, see buildMossMesh)
		// and this sector's scatter density/spacing/scale differ.
		shapeKind = BiomeRock
	}
	gl.Uniform1i(p.u("shapeKind"), shapeKind)

	// Neutral bloom/clump state, overridden by the Wildflower and Grass cases
	// below. Uniforms persist across draw calls, so these must be reset for
	// every sector — otherwise a wildflower's bloom settings would leak onto
	// the next sector's cattail seed heads (which share the head code path),
	// and a clumped grass sector would gather the next sector's rocks into
	// tufts.
	gl.Uniform3f(p.u("bloomAspect"), 1, 1, 1)
	gl.Uniform1i(p.u("hasCenter"), 0)
	gl.Uniform1i(p.u("hasLeaves"), 0)
	gl.Uniform1f(p.u("leafScale"), 1)
	gl.Uniform1i(p.u("bloomSolid"), 0)
	gl.Uniform3f(p.u("centerColor"), 1, 1, 1)
	gl.Uniform1f(p.u("clump"), 0)

	switch s.Brush.Kind {
	case BiomeRock:
		// No LOD tiers (biomeLODTierCount==1) — tier is always 0, outer==rng.
		rockScale := s.Brush.RockScale
		if rockScale <= 0 {
			rockScale = defaultRockScale
		}
		rm := biomes.rockMeshFor(s.Brush.RockSides)
		n, sp := biomeLattice(outer, rockSpacing, maxRockGridN)
		gl.Uniform1f(p.u("rockScale"), rockScale)
		gl.Uniform1f(p.u("spacing"), sp)
		gl.Uniform1i(p.u("gridN"), n)
		gl.BindVertexArray(rm.vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, rm.verts, n*n)

	case BiomeMoss:
		clumpScale := s.Brush.RockScale
		if clumpScale <= 0 {
			clumpScale = defaultRockScale
		}
		vao, verts, baseSpacing, cap := biomes.mossTemplate(tier)
		n, sp := biomeLattice(outer, baseSpacing, cap)
		gl.Uniform1f(p.u("rockScale"), clumpScale)
		gl.Uniform1f(p.u("spacing"), sp)
		gl.Uniform1i(p.u("gridN"), n)
		gl.BindVertexArray(vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, verts, n*n)

	case BiomeWildflower:
		bh := s.Brush.GrassHeight
		if bh <= 0 {
			bh = defaultWildflowerHeight
		}
		if bh > maxStemHeight {
			bh = maxStemHeight
		}
		bw := s.Brush.GrassWidth
		if bw <= 0 {
			bw = defaultWildflowerWidth
		}
		hs := s.Brush.HeadScale
		if hs <= 0 {
			hs = defaultWildflowerHeadScale
		}
		aspect := wildflowerDiscAspect
		if s.Brush.BloomSpike {
			aspect = wildflowerSpikeAspect
		}
		cc := s.Brush.CenterColor
		if cc == (mgl32.Vec3{}) {
			cc = defaultCenterColor
		}
		vao, verts, baseSpacing, cap := biomes.wildflowerTemplate(tier)
		n, sp := biomeLattice(outer, baseSpacing, cap)
		gl.Uniform1f(p.u("bladeHeight"), bh)
		gl.Uniform1f(p.u("bladeWidth"), bw)
		gl.Uniform1f(p.u("headScale"), hs)
		gl.Uniform3f(p.u("bloomAspect"), aspect.X(), aspect.Y(), aspect.Z())
		// Mid/Far templates carry no center/leaf geometry at all (see
		// buildWildflowerMeshSimple) — leaving these at the neutral 0 the
		// reset above already set is belt-and-braces, not load-bearing.
		if tier == 0 {
			gl.Uniform1i(p.u("hasCenter"), boolToInt32(s.Brush.BloomCenter))
			gl.Uniform1i(p.u("hasLeaves"), boolToInt32(s.Brush.StemLeaves))
			gl.Uniform1f(p.u("leafScale"), resolvedLeafScale(s.Brush.LeafScale))
		}
		gl.Uniform1i(p.u("bloomSolid"), boolToInt32(s.Brush.BloomSolidColor))
		gl.Uniform3f(p.u("centerColor"), cc.X(), cc.Y(), cc.Z())
		gl.Uniform1f(p.u("spacing"), sp)
		gl.Uniform1i(p.u("gridN"), n)
		gl.BindVertexArray(vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, verts, n*n)

	case BiomeCattail:
		bh := s.Brush.GrassHeight
		if bh <= 0 {
			bh = defaultCattailHeight
		}
		bw := s.Brush.GrassWidth
		if bw <= 0 {
			bw = defaultCattailWidth
		}
		hs := s.Brush.HeadScale
		if hs <= 0 {
			hs = defaultCattailHeadScale
		}
		vao, verts, baseSpacing, cap := biomes.cattailTemplate(tier)
		n, sp := biomeLattice(outer, baseSpacing, cap)
		gl.Uniform1f(p.u("bladeHeight"), bh)
		gl.Uniform1f(p.u("bladeWidth"), bw)
		gl.Uniform1f(p.u("headScale"), hs)
		gl.Uniform1f(p.u("spacing"), sp)
		gl.Uniform1i(p.u("gridN"), n)
		gl.BindVertexArray(vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, verts, n*n)

	default: // BiomeGrass
		bh := s.Brush.GrassHeight
		if bh <= 0 {
			bh = defaultGrassHeight
		}
		if bh > maxStemHeight {
			bh = maxStemHeight
		}
		bw := s.Brush.GrassWidth
		if bw <= 0 {
			bw = defaultGrassWidth
		}
		vao, verts, baseSpacing, cap := biomes.grassTemplate(tier)
		n, sp := biomeLattice(outer, baseSpacing, cap)
		gl.Uniform1f(p.u("bladeHeight"), bh)
		gl.Uniform1f(p.u("bladeWidth"), bw)
		// Grass is the only kind that clumps, and only its Near tier — Mid/Far
		// tiers leave the neutral 0 set above (Mid is already a cheaper single
		// blade, Far is already a pre-baked group; the tuft-gather math in
		// biome_scatter_common.glsl assumes the Near tier's own tight lattice
		// spacing).
		if tier == 0 {
			gl.Uniform1f(p.u("clump"), clampGrassClump(s.Brush.GrassClump))
		}
		gl.Uniform1f(p.u("spacing"), sp)
		gl.Uniform1i(p.u("gridN"), n)
		gl.BindVertexArray(vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, verts, n*n)
	}
}

// endBiomePass restores GL state for the rest of the frame, rebinding restore
// as the active program — the main Program after the color pass, the relevant
// depth program after a shadow pass, matching what the caller interrupted.
func endBiomePass(restore uint32) {
	gl.BindVertexArray(0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.UseProgram(restore)
}
