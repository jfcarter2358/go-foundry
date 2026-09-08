package render

// scatter.go — GPU-scattered foliage filling a placed volume.
//
// Nothing here is placed as a scene object. At render time a small template
// mesh (a grass blade, a wildflower, a cattail reed, a rock, a moss clump —
// see scatter_mesh.go) is drawn with glDrawArraysInstanced, and the vertex
// shader generates every instance procedurally from gl_InstanceID: scattering
// it across a camera-centred lattice, testing it against the volume's own
// shape to decide whether it survives, and sampling a small per-volume height
// texture to drop it onto the geometry beneath. Cost is bounded by the camera
// ring, not by world size or by how large the volume is.
//
// # What changed from the painted version
//
// Scatter used to be a polygon drawn on a sector's floor. Two things follow
// from it being a volume instead, and they are the whole of the difference:
//
//   - The survival test is the volume's own SHAPE — a box, a sphere or a
//     cylinder in its own rotated frame — where it used to be a ray-cast
//     point-in-polygon against up to 64 uploaded points. It is cheaper, and it
//     is three uniforms instead of an array.
//   - The height texture is baked by casting rays DOWN through the level's
//     brushes (internal/biome), where it used to look up the floor of the
//     sector the polygon was painted on. A volume is placed in space and does
//     not know what is underneath it until something looks.
//
// Everything else — the lattice, the LOD tiers, the wind, the boundary jitter,
// the three-program shadow plumbing — is what it was.
//
// # Why three programs
//
// The color pass, the directional cascade depth pass and the point-light
// cubemap depth pass are separate programs compiled from the SAME placement
// code (biome_scatter_common.glsl), mirroring detailRenderer's scatter trio.
// Without that sharing a blade's shadow would drift off the blade the moment
// either shader was tuned.
//
// This mirrors the self-contained GizmoProgram pattern: its own GL program and
// VAOs, never touching the main game shader's state.

import (
	"math"

	"foundry/internal/biome"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Lattice spacing (world units) between scatter slots for each kind's NEAR LOD
// tier — or its only tier, for Rock. Density thins these out; spacing sets the
// maximum, fully-covered instance count.
const (
	grassSpacing      = 0.35
	rockSpacing       = 2.5
	wildflowerSpacing = 0.6
	cattailSpacing    = 0.75
	mossSpacing       = 0.5 // denser than rock: small clumps, not boulders
)

// Mid/Far LOD tier spacing for the kinds with three tiers. Each Far-tier
// instance is a multi-blade clump template rather than one simplified instance,
// so its spacing is wide enough that one clump's own footprint roughly covers
// what several Near instances used to — a sparse far lattice still reads as
// dense scatter instead of thinning out.
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

// Caps on lattice side length so a huge draw range cannot explode the instance
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

// Per-volume height texture resolution and cap.
const (
	scatterHeightTexelSize = float32(0.5)
	scatterMaxTexDim       = 2048
)

// scatterDropMargin is how far above and below a volume the height bake looks
// for ground, in world units.
//
// Above, because a volume is usually placed to ENCLOSE the vegetation rather
// than to sit exactly on the floor, and a ray starting at its own top would
// miss ground that pokes up inside it. Below, because a volume floated a little
// clear of the ground it means is the ordinary authoring mistake, and refusing
// to find a floor two metres down would make the feature look broken rather
// than make the placement look wrong.
const scatterDropMargin = float32(8)

// rockMesh is a cached rock template VAO for a given equatorial side count.
type rockMesh struct {
	vao, vbo uint32
	verts    int32
}

// scatterHeightEntry is a cached per-volume height texture and the world-space
// XZ box it covers.
type scatterHeightEntry struct {
	tex          uint32
	origin, size mgl32.Vec2
	// key is what the bake was made from. A volume that has not moved, resized
	// or been re-tuned re-uses the texture; anything else re-bakes, which is a
	// few thousand ray casts and is not something to do per frame.
	key scatterHeightKey
}

// scatterHeightKey is everything about a volume that changes where its
// instances land. Deliberately not the whole VolumeScatter: colour, density and
// the bloom options change what is drawn, not where the ground is, and
// re-baking for them would make every slider drag in the editor cost a bake.
type scatterHeightKey struct {
	pos, half, rot   mgl32.Vec3
	shape            world.VolumeShape
	minSlope, maxSlp float32
	// geom is a fingerprint of the level's own geometry, so that editing the
	// floor under a volume re-bakes it. See scatterGeometryFingerprint.
	geom uint64
}

// scatterRenderer holds the GL objects and cached per-volume height textures.
type scatterRenderer struct {
	color  progUniforms // instanced: color pass
	shadow progUniforms // instanced: directional cascade depth
	cube   progUniforms // instanced: point-light cubemap depth

	// Template meshes, ONE PER LOD TIER (0=Near, 1=Mid, 2=Far). Appearance
	// beyond mesh choice is scaled by uniforms, so one mesh per kind per tier
	// suffices. Rock has no LOD tiers and keeps a single VAO per side count.
	bladeVAO, bladeVBO           [3]uint32
	bladeVerts                   [3]int32
	wildflowerVAO, wildflowerVBO [3]uint32
	wildflowerVerts              [3]int32
	cattailVAO, cattailVBO       [3]uint32
	cattailVerts                 [3]int32
	mossVAO, mossVBO             [3]uint32
	mossVerts                    [3]int32
	rockMeshes                   map[int32]rockMesh

	// Per-volume height texture cache, keyed by Volume.ID.
	heightCache map[string]scatterHeightEntry

	initialized bool
}

var scatters scatterRenderer

// EvictScatterCache releases the cached height texture for a deleted volume.
// Call from the editor when a volume is removed or its level replaced.
func EvictScatterCache(id string) {
	e, ok := scatters.heightCache[id]
	if !ok {
		return
	}
	t := e.tex
	gl.DeleteTextures(1, &t)
	delete(scatters.heightCache, id)
}

// GrassInit compiles the scatter shader programs and builds the template
// meshes. Must be called once with the GL context current.
//
// The name is what InitWorldPasses calls and what the release-build test
// asserts on; it is kept rather than renamed with the feature, so that a build
// standing every world pass up stays one list in one place.
func GrassInit() {
	scatShared := biomeScatterSnippet()
	mk := func(prog uint32) progUniforms { return progUniforms{prog: prog, loc: map[string]int32{}} }

	// Color pass. The two stages get the lighting block at different costs:
	// the vertex stage lights foliage with a 3x3-averaged shadow tap per
	// vertex, the fragment stage lights rock and moss with a 3x3 plus full
	// point-light shadows.
	//
	// The vertex stage averages rather than taking a single hard tap. One tap
	// is a boolean in/out test with nothing to smooth it: a blade near a shadow
	// edge flips fully lit to fully shadowed between frames as the cascade box
	// (refit to the camera every frame) nudges which texel it lands in, and
	// since one vertex decides the WHOLE blade's colour that reads as the plant
	// flickering. Averaging turns the flip into a ramp.
	scatters.color = mk(mustBiomeProgram(
		biomeShaderPath("biome_vert.glsl"), biomeShaderPath("biome_frag.glsl"),
		scatShared+"\n"+biomeLightSnippet(1, false, false), biomeLightSnippet(1, true, false)))

	// Depth passes. Placement only — the depth-only fragment stages need no
	// lighting block at all.
	scatters.shadow = mk(mustBiomeProgram(
		biomeShaderPath("biome_shadow_vert.glsl"), biomeShaderPath("shadow_frag.glsl"),
		scatShared, ""))
	scatters.cube = mk(mustBiomeProgram(
		biomeShaderPath("biome_cube_vert.glsl"), biomeShaderPath("shadow_cube_frag.glsl"),
		scatShared, ""))

	uploadTier := func(vao, vbo *[3]uint32, verts *[3]int32, tier int, mesh []float32) {
		verts[tier] = int32(len(mesh) / ScatterVertexStride)
		vao[tier], vbo[tier] = uploadScatterMesh(mesh)
	}

	uploadTier(&scatters.bladeVAO, &scatters.bladeVBO, &scatters.bladeVerts, 0, BuildBladeMesh())
	uploadTier(&scatters.bladeVAO, &scatters.bladeVBO, &scatters.bladeVerts, 1, buildTaperedStemN(1.0, 1.0, lodStemSegs))
	uploadTier(&scatters.bladeVAO, &scatters.bladeVBO, &scatters.bladeVerts, 2, buildGrassClumpMesh())

	uploadTier(&scatters.wildflowerVAO, &scatters.wildflowerVBO, &scatters.wildflowerVerts, 0, BuildWildflowerMesh())
	uploadTier(&scatters.wildflowerVAO, &scatters.wildflowerVBO, &scatters.wildflowerVerts, 1, buildWildflowerMeshSimple())
	uploadTier(&scatters.wildflowerVAO, &scatters.wildflowerVBO, &scatters.wildflowerVerts, 2, buildWildflowerClumpMesh())

	uploadTier(&scatters.cattailVAO, &scatters.cattailVBO, &scatters.cattailVerts, 0, BuildCattailMesh())
	uploadTier(&scatters.cattailVAO, &scatters.cattailVBO, &scatters.cattailVerts, 1, buildCattailMeshSimple())
	uploadTier(&scatters.cattailVAO, &scatters.cattailVBO, &scatters.cattailVerts, 2, buildCattailClumpMesh())

	uploadTier(&scatters.mossVAO, &scatters.mossVBO, &scatters.mossVerts, 0, BuildMossMesh())
	uploadTier(&scatters.mossVAO, &scatters.mossVBO, &scatters.mossVerts, 1, buildMossMeshMid())
	uploadTier(&scatters.mossVAO, &scatters.mossVBO, &scatters.mossVerts, 2, buildMossClumpMesh())

	scatters.rockMeshes = map[int32]rockMesh{}
	scatters.heightCache = map[string]scatterHeightEntry{}
	scatters.initialized = true

	// The world package releases a volume's derived GPU resources when a level
	// is replaced, but it sits below this one and cannot name the cache.
	// Installed here rather than at package init because there is nothing to
	// evict until the cache exists.
	world.EvictBiomeCaches = EvictScatterCache
}

// rockMeshFor returns (building and caching if necessary) the rock template for
// a given equatorial side count.
func (sr *scatterRenderer) rockMeshFor(sides int32) rockMesh {
	if sides < 3 {
		sides = world.DefaultRockSides
	}
	if m, ok := sr.rockMeshes[sides]; ok {
		return m
	}
	mesh := buildRockMesh(int(sides))
	m := rockMesh{verts: int32(len(mesh) / ScatterVertexStride)}
	m.vao, m.vbo = uploadScatterMesh(mesh)
	sr.rockMeshes[sides] = m
	return m
}

func (sr *scatterRenderer) grassTemplate(tier int) (vao uint32, verts int32, spacing float32, cap int32) {
	switch tier {
	case 1:
		return sr.bladeVAO[1], sr.bladeVerts[1], grassMidSpacing, maxGrassMidGridN
	case 2:
		return sr.bladeVAO[2], sr.bladeVerts[2], grassClumpSpacing, maxGrassClumpGridN
	default:
		return sr.bladeVAO[0], sr.bladeVerts[0], grassSpacing, maxGrassGridN
	}
}

func (sr *scatterRenderer) wildflowerTemplate(tier int) (vao uint32, verts int32, spacing float32, cap int32) {
	switch tier {
	case 1:
		return sr.wildflowerVAO[1], sr.wildflowerVerts[1], wildflowerMidSpacing, maxWildflowerMidGridN
	case 2:
		return sr.wildflowerVAO[2], sr.wildflowerVerts[2], wildflowerClumpSpacing, maxWildflowerClumpGridN
	default:
		return sr.wildflowerVAO[0], sr.wildflowerVerts[0], wildflowerSpacing, maxWildflowerGridN
	}
}

func (sr *scatterRenderer) cattailTemplate(tier int) (vao uint32, verts int32, spacing float32, cap int32) {
	switch tier {
	case 1:
		return sr.cattailVAO[1], sr.cattailVerts[1], cattailMidSpacing, maxCattailMidGridN
	case 2:
		return sr.cattailVAO[2], sr.cattailVerts[2], cattailClumpSpacing, maxCattailClumpGridN
	default:
		return sr.cattailVAO[0], sr.cattailVerts[0], cattailSpacing, maxCattailGridN
	}
}

func (sr *scatterRenderer) mossTemplate(tier int) (vao uint32, verts int32, spacing float32, cap int32) {
	switch tier {
	case 1:
		return sr.mossVAO[1], sr.mossVerts[1], mossMidSpacing, maxMossMidGridN
	case 2:
		return sr.mossVAO[2], sr.mossVerts[2], mossClumpSpacing, maxMossClumpGridN
	default:
		return sr.mossVAO[0], sr.mossVerts[0], mossSpacing, maxMossGridN
	}
}

// ---------- the height/coverage texture ----------

// uploadRG32F uploads a two-channel float texture.
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

// scatterHeightTexDims picks a resolution covering [min,max] at roughly
// scatterHeightTexelSize world units per texel, capped.
func scatterHeightTexDims(min, max mgl32.Vec2) (w, h int, size mgl32.Vec2) {
	size = mgl32.Vec2{max.X() - min.X(), max.Y() - min.Y()}
	if size.X() < scatterHeightTexelSize {
		size[0] = scatterHeightTexelSize
	}
	if size.Y() < scatterHeightTexelSize {
		size[1] = scatterHeightTexelSize
	}
	w = int(size.X()/scatterHeightTexelSize) + 2
	h = int(size.Y()/scatterHeightTexelSize) + 2
	if w > scatterMaxTexDim {
		w = scatterMaxTexDim
	}
	if h > scatterMaxTexDim {
		h = scatterMaxTexDim
	}
	return
}

// scatterFootprint is the XZ box a volume's instances can occupy — the volume's
// own bounds, which is what the shape test will reject outside of anyway.
//
// In the volume's OWN frame, not the world's: everything the shader tests a
// candidate against runs in that frame (see uploadScatterFrame), so a volume
// riding a moving ship bakes its texture once rather than re-baking it every
// time the ship turns.
func scatterFootprint(v *world.Volume) (mgl32.Vec2, mgl32.Vec2) {
	if !v.HasBounds() {
		return mgl32.Vec2{}, mgl32.Vec2{}
	}
	h := v.HalfExtents()
	if v.Rotation != (mgl32.Vec3{}) {
		// A rotated shape's extents' length as a uniform radius, so the box
		// stays valid at any orientation — the same widening Volume.WorldAABB
		// applies, and for the same reason.
		r := h.Len()
		h = mgl32.Vec3{r, r, r}
	}
	return mgl32.Vec2{v.Position.X() - h.X(), v.Position.Z() - h.Z()},
		mgl32.Vec2{v.Position.X() + h.X(), v.Position.Z() + h.Z()}
}

// scatterGeometryFingerprint is a cheap summary of the level's brush geometry,
// so that editing the floor under a scatter volume re-bakes its height texture.
//
// It is a count-and-bounds hash rather than a real content hash: walking every
// vertex of every brush once a frame would cost more than the bake it is trying
// to avoid. What it catches is a brush being added, removed, moved, resized or
// reshaped — which is every edit that changes where the ground is. What it does
// not catch is an edit that leaves a chunk's bounds and brush count exactly as
// they were; the editor calls EvictScatterCache on an explicit rebuild, which is
// the backstop.
func scatterGeometryFingerprint(lvl *world.Level) uint64 {
	h := uint64(14695981039346656037)
	mix := func(x uint64) { h ^= x; h *= 1099511628211 }
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() {
			continue
		}
		mix(uint64(len(ch.Brushes)))
		lo, hi := ch.WorldBounds()
		for _, f := range []float32{lo.X(), lo.Y(), lo.Z(), hi.X(), hi.Y(), hi.Z()} {
			mix(uint64(math.Float32bits(f)))
		}
	}
	return h
}

// ensureScatterHeight returns the cached height/coverage texture for volume v,
// re-baking it when the volume or the geometry under it has changed.
//
// Two channels per texel: .r is the Y an instance drops onto, .g is the
// slope-coverage factor in [0,1] the vertex shader multiplies into density, so
// instances thin out and vanish past the authored slope range instead of
// standing on a cliff face. A column that finds no ground at all bakes a
// coverage of 0, which is what grows nothing under an overhang.
func (sr *scatterRenderer) ensureScatterHeight(lvl *world.Level, v *world.Volume, geomFP uint64) scatterHeightEntry {
	key := scatterHeightKey{
		pos:      v.Position,
		half:     v.HalfExtents(),
		rot:      v.Rotation,
		shape:    v.Shape,
		minSlope: world.ResolvedMinSlope(v.Scatter),
		maxSlp:   world.ResolvedMaxSlope(v.Scatter),
		geom:     geomFP,
	}
	e, ok := sr.heightCache[v.ID]
	if ok && e.key == key {
		return e
	}
	if !ok {
		gl.GenTextures(1, &e.tex)
	}
	min, max := scatterFootprint(v)
	w, h, size := scatterHeightTexDims(min, max)

	// The column starts above the volume and reaches below it — see
	// scatterDropMargin. Both in the volume's own frame, like the footprint.
	half := v.HalfExtents()
	topY := v.Position.Y() + half.Y() + scatterDropMargin
	drop := 2*half.Y() + 2*scatterDropMargin

	// The ray is cast in the volume's frame, so a volume riding a ship finds
	// that ship's own decks. SurfaceUnder walks every chunk, and a frame-owned
	// chunk's geometry is expressed in exactly this frame.
	data := make([]float32, w*h*2)
	for ty := 0; ty < h; ty++ {
		tv := float32(ty) / float32(h-1)
		wz := min.Y() + tv*size.Y()
		for tx := 0; tx < w; tx++ {
			tu := float32(tx) / float32(w-1)
			wx := min.X() + tu*size.X()
			s := biome.SurfaceUnder(lvl, wx, topY, wz, drop)
			i := (ty*w + tx) * 2
			if !s.Found {
				data[i], data[i+1] = topY, 0
				continue
			}
			data[i] = s.Height
			data[i+1] = biome.SlopeCoverage(v.Scatter, s.Normal)
		}
	}
	uploadRG32F(e.tex, w, h, data, true)
	e.origin, e.size, e.key = min, size, key
	sr.heightCache[v.ID] = e
	return e
}

// ---------- lattice and bounds ----------

// scatterLattice returns the lattice side length (gridN) and spacing needed to
// cover the full 2*range square centred on the camera. It prefers baseSpacing,
// but once that would exceed the instance cap it GROWS the spacing so the
// capped lattice still spans the whole range.
func scatterLattice(rng, baseSpacing float32, cap int32) (gridN int32, spacing float32) {
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

// scatterBounds is the world box a volume's instances can occupy this frame:
// its own bounds clipped to the camera ring, since the scatter exists only
// within rng of the camera however large the volume is. Used to cull whole
// volumes out of a shadow cascade.
func scatterBounds(v *world.Volume, camPos mgl32.Vec3, rng float32) (mgl32.Vec3, mgl32.Vec3) {
	lo, hi, ok := v.WorldAABB()
	if !ok {
		return mgl32.Vec3{1, 1, 1}, mgl32.Vec3{} // empty: min > max
	}
	minX, maxX := lo.X(), hi.X()
	minZ, maxZ := lo.Z(), hi.Z()
	if t := camPos.X() - rng; t > minX {
		minX = t
	}
	if t := camPos.X() + rng; t < maxX {
		maxX = t
	}
	if t := camPos.Z() - rng; t > minZ {
		minZ = t
	}
	if t := camPos.Z() + rng; t < maxZ {
		maxZ = t
	}
	// An instance can stand up to MaxStemHeight above the surface it sits on,
	// and the bake reaches scatterDropMargin below the volume for ground, so
	// the vertical span is the volume's own plus both.
	return mgl32.Vec3{minX, lo.Y() - scatterDropMargin, minZ},
		mgl32.Vec3{maxX, hi.Y() + world.MaxStemHeight, maxZ}
}

// scatterCastOuterBound is the outer world-unit bound the CASTING tiers of a
// volume's scatter actually reach — the outer boundary of whichever tier
// BiomeShadows.CastLODTiers cuts off at, not the volume's full Range, which
// only the never-casting-by-default Mid/Far tiers reach beyond.
func scatterCastOuterBound(s *world.VolumeScatter) float32 {
	tierCount := world.ScatterLODTierCount(s.Kind)
	if tierCount == 1 {
		return world.ScatterRange(s)
	}
	castTiers := BiomeShadows.CastLODTiers
	if castTiers > tierCount {
		castTiers = tierCount
	}
	// 0 (a zero-value settings struct) falls back to the full range rather than
	// casting nothing — AnyCast/CastsKind are what actually gate casting off;
	// this is only ever a cull-box bound.
	switch castTiers {
	case 1:
		return world.ResolvedLOD1(s)
	case 2:
		return world.ResolvedLOD2(s)
	default:
		return world.ScatterRange(s)
	}
}

// forEachScatterVolume visits every volume that draws scatter instances.
//
// castOnly restricts it to the kinds BiomeShadows is configured to cast from,
// and cull (when non-nil) drops volumes whose scatter cannot reach the box being
// rendered — for a cascade pass that is the cascade's own light box, which is
// what stops a large level re-instancing every volume into every shadow map.
// castCap is only consulted when castOnly is set: the shadow map's own reach,
// which combines with the volume's range and BiomeShadows.CastRange to decide
// both the cull box here and the per-instance castRange uniform. The two MUST
// agree, or an instance culled out of the box here could still think it belongs
// in a map it was never drawn into.
func forEachScatterVolume(level *world.Level, camPos mgl32.Vec3, castOnly bool, castCap float32, cull *Frustum, fn func(v *world.Volume)) {
	for vi := range level.Volumes {
		v := &level.Volumes[vi]
		s := v.Scatter
		if s == nil || !s.Enabled || !v.Enabled || !v.HasBounds() || s.Density <= 0 {
			continue
		}
		if castOnly && !BiomeShadows.CastsKind(s.Kind) {
			continue
		}
		if cull != nil {
			rng := world.ScatterRange(s)
			if castOnly {
				// The outermost CASTING tier's own bound, not the volume's full
				// Range — a volume whose Far tier reaches well past the last
				// casting tier should not inflate the cull box for a pass that
				// will never draw it.
				rng = BiomeShadows.castRangeFor(scatterCastOuterBound(s), castCap)
			}
			// BoundaryJitter can push an instance's effective distance this far
			// past its nominal boundary, so the cull box is padded to match or
			// a jitter-extended instance near the true edge is culled out of a
			// frustum that never expected it to reach that far.
			rng += world.ResolvedBoundaryJitter(s)
			bmin, bmax := scatterBounds(v, camPos, rng)
			if bmin.X() > bmax.X() || bmin.Z() > bmax.Z() {
				continue // the volume lies entirely outside the camera ring
			}
			if !AABBInFrustum(*cull, bmin, bmax) {
				continue
			}
		}
		fn(v)
	}
}

// ---------- render entry points ----------

// RenderScatterVolumes draws every scatter volume's instanced foliage.
//
// frame carries the directional cascade setup from RenderShadowPass, which the
// color pass needs in order to receive shadows the way world geometry does.
func RenderScatterVolumes(level *world.Level, projView mgl32.Mat4, camPos mgl32.Vec3, frame ShadowFrame) {
	if !scatters.initialized || level == nil {
		return
	}
	timeSec := BiomeWindTime
	geomFP := scatterGeometryFingerprint(level)

	p := &scatters.color
	started := false
	frameM := mgl32.Ident4()
	forEachScatterVolume(level, camPos, false, 0, nil, func(v *world.Volume) {
		if !started {
			uploadBiomeLighting(p.prog, level, frame)
			pv := projView
			gl.UniformMatrix4fv(p.U("projView"), 1, false, &pv[0])
			beginScatterPass(p, camPos, timeSec)
			frameM = mgl32.Ident4()
			gl.UniformMatrix4fv(p.U("model"), 1, false, &frameM[0])
			gl.Uniform2f(p.U("camXZ"), camPos.X(), camPos.Z())
			started = true
		}
		uploadScatterFrame(p, &frameM, level, v, camPos)
		for tier := 0; tier < world.ScatterLODTierCount(v.Scatter.Kind); tier++ {
			renderScatterVolume(level, v, p, tier, false, 0, geomFP)
		}
	})

	if started {
		endScatterPass(gfx.Program)
	}
}

// RenderScatterDirShadow draws the scatter into the currently-bound directional
// cascade layer using light-space matrix lsm. cascade is the slice index being
// rendered (0 = nearest) and cascadeSplit is the view-space distance that
// cascade's own light box reaches to — together they are what stops small
// scatter from being re-instanced into a cascade its shadow cannot usefully
// reach. Restores Shadows.Program on exit.
func RenderScatterDirShadow(level *world.Level, lsm mgl32.Mat4, cascade int, cascadeSplit float32, camPos mgl32.Vec3, timeSec float32, cull *Frustum) {
	if !scatters.initialized || level == nil || !BiomeShadows.AnyCast() {
		return
	}
	if cascade >= BiomeShadows.CastCascades {
		return
	}
	geomFP := scatterGeometryFingerprint(level)

	p := &scatters.shadow
	started := false
	frameM := mgl32.Ident4()
	forEachScatterVolume(level, camPos, true, cascadeSplit, cull, func(v *world.Volume) {
		if !started {
			gl.UseProgram(p.prog)
			m := lsm
			gl.UniformMatrix4fv(p.U("lightSpaceMatrix"), 1, false, &m[0])
			beginScatterPass(p, camPos, timeSec)
			frameM = mgl32.Ident4()
			gl.UniformMatrix4fv(p.U("model"), 1, false, &frameM[0])
			gl.Uniform2f(p.U("camXZ"), camPos.X(), camPos.Z())
			started = true
		}
		uploadScatterFrame(p, &frameM, level, v, camPos)
		for tier := 0; tier < castTiersFor(v.Scatter); tier++ {
			renderScatterVolume(level, v, p, tier, true, cascadeSplit, geomFP)
		}
	})

	if started {
		endScatterPass(Shadows.Program)
	}
}

// RenderScatterCubeShadow draws the scatter into the currently-bound point-light
// cubemap face. farPlane doubles as the cast cap — a point light's own reach
// already bounds how far an instance could usefully cast into it, the same role
// a cascade's split distance plays. Restores Shadows.CubeProgram on exit.
func RenderScatterCubeShadow(level *world.Level, lsm mgl32.Mat4, lightPos mgl32.Vec3, farPlane float32, camPos mgl32.Vec3, timeSec float32, cull *Frustum) {
	if !scatters.initialized || level == nil || !BiomeShadows.AnyCast() {
		return
	}
	geomFP := scatterGeometryFingerprint(level)

	p := &scatters.cube
	started := false
	frameM := mgl32.Ident4()
	forEachScatterVolume(level, camPos, true, farPlane, cull, func(v *world.Volume) {
		if !started {
			gl.UseProgram(p.prog)
			m := lsm
			gl.UniformMatrix4fv(p.U("lightSpaceMatrix"), 1, false, &m[0])
			gl.Uniform3f(p.U("lightPos"), lightPos.X(), lightPos.Y(), lightPos.Z())
			gl.Uniform1f(p.U("farPlane"), farPlane)
			beginScatterPass(p, camPos, timeSec)
			frameM = mgl32.Ident4()
			gl.UniformMatrix4fv(p.U("model"), 1, false, &frameM[0])
			gl.Uniform2f(p.U("camXZ"), camPos.X(), camPos.Z())
			started = true
		}
		uploadScatterFrame(p, &frameM, level, v, camPos)
		for tier := 0; tier < castTiersFor(v.Scatter); tier++ {
			renderScatterVolume(level, v, p, tier, true, farPlane, geomFP)
		}
	})

	if started {
		endScatterPass(Shadows.CubeProgram)
	}
}

// castTiersFor is how many LOD tiers cast into a shadow map: whichever of
// BiomeShadows.CastLODTiers and the kind's own tier count is smaller, and never
// fewer than one — a zero-value settings struct still casts Near.
func castTiersFor(s *world.VolumeScatter) int {
	tiers := BiomeShadows.CastLODTiers
	if tiers < 1 {
		tiers = 1
	}
	if kindTiers := world.ScatterLODTierCount(s.Kind); tiers > kindTiers {
		tiers = kindTiers
	}
	return tiers
}

// beginScatterPass uploads the placement uniforms shared by every volume this
// frame — the camera position and the wind clock. p must already be the active
// program.
//
// camXZ (the camera-ring lattice origin) is deliberately NOT uploaded here.
// Unlike camPos, which wants the true world camera always (rock specular), it
// has to ride the same frame as the volume it is tested against, so it is
// carried in step with `model` by uploadScatterFrame instead. Callers upload an
// initial identity `model` plus a world-space camXZ right after this, matching
// a world volume exactly, then let uploadScatterFrame take over.
//
// The wind clock matters in the depth passes too. A blade sways in the color
// pass and would stand still in the shadow map without it, so its shadow would
// detach from it every time the wind moved.
func beginScatterPass(p *progUniforms, camPos mgl32.Vec3, timeSec float32) {
	gl.Uniform3f(p.U("camPos"), camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform1f(p.U("time"), timeSec)
	gl.Uniform1i(p.U("heightTex"), 0)
}

// uploadScatterFrame uploads p's `model` and `camXZ` for the frame a volume
// rides, but only when that frame differs from *current — mirroring
// uploadDetailModel's change-tracking, so a level of world-space volumes pays
// for one upload per program per frame rather than one per volume.
//
// A volume riding a vehicle describes its region in that vehicle's coordinates
// (Volume.Frame), and so does the height texture baked for it, so the shader's
// whole placement runs in that frame and `model` carries the result back out.
func uploadScatterFrame(p *progUniforms, current *mgl32.Mat4, lvl *world.Level, v *world.Volume, camPos mgl32.Vec3) {
	f := lvl.Frames.Frame(v.Frame)
	m := mgl32.Ident4()
	local := camPos
	if !f.IsWorld() {
		m = f.WorldMatrix()
		local = f.FromWorld(camPos)
	}
	if m == *current {
		return
	}
	*current = m
	gl.UniformMatrix4fv(p.U("model"), 1, false, &m[0])
	gl.Uniform2f(p.U("camXZ"), local.X(), local.Z())
}

// renderScatterVolume uploads one volume's shape, appearance and height
// uniforms to p and issues its instanced draw call for LOD tier `tier`
// (0=Near, 1=Mid, 2=Far — ignored, always 0, for a single-tier kind like Rock).
//
// shadowPass shortens the castRange uniform using castCap; everything else is
// identical between the color and depth passes, which is the point — the three
// programs share their placement code and must be fed identically for a blade's
// shadow to land on the blade.
func renderScatterVolume(lvl *world.Level, v *world.Volume, p *progUniforms, tier int, shadowPass bool, castCap float32, geomFP uint64) {
	s := v.Scatter
	entry := scatters.ensureScatterHeight(lvl, v, geomFP)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, entry.tex)
	gl.Uniform2f(p.U("heightOrigin"), entry.origin.X(), entry.origin.Y())
	gl.Uniform2f(p.U("heightSize"), entry.size.X(), entry.size.Y())

	// The volume's own shape, which replaces the painted polygon: a centre,
	// half-extents, a kind, and the rotation that carries a candidate point
	// into the volume's frame.
	half := v.HalfExtents()
	gl.Uniform3f(p.U("volCenter"), v.Position.X(), v.Position.Y(), v.Position.Z())
	gl.Uniform3f(p.U("volHalf"), half.X(), half.Y(), half.Z())
	// The INVERSE rotation, because every use is world→volume: the test carries
	// the point in rather than carrying the shape out.
	inv := world.EulerMatrixYXZ(v.Rotation).Inv()
	gl.UniformMatrix4fv(p.U("volInvRot"), 1, false, &inv[0])
	gl.Uniform1i(p.U("volShape"), int32(v.Shape))

	// This tier's own [inner, outer] distance band. A single-tier kind always
	// gets [0, rng]; LOD-capable kinds split rng into three concentric bands.
	rng := world.ScatterRange(s)
	lod1, lod2 := world.ResolvedLOD1(s), world.ResolvedLOD2(s)
	tierCount := world.ScatterLODTierCount(s.Kind)
	inner, outer := float32(0), rng
	if tierCount == 3 {
		switch tier {
		case 0:
			outer = lod1
		case 1:
			inner, outer = lod1, lod2
		default:
			inner = lod2
		}
	}

	gl.Uniform1f(p.U("range"), outer)
	gl.Uniform1f(p.U("lodMin"), inner)
	if shadowPass {
		gl.Uniform1f(p.U("castRange"), BiomeShadows.castRangeFor(outer, castCap))
	} else {
		gl.Uniform1f(p.U("castRange"), outer)
	}

	// outerIsLOD tells the shader which KIND of fade this tier's outer edge
	// gets. RangeFadeFraction's shrink only ever means the volume's own Range —
	// the last tier, or a kind's only tier. An inner tier's outer edge is an LOD
	// split instead, and dithers rather than shrinks so it does not gap against
	// the next tier's own dithered grow-in.
	gl.Uniform1i(p.U("outerIsLOD"), boolToInt32(tier < tierCount-1))
	gl.Uniform1f(p.U("rangeFadeFraction"), geom.Clamp01(s.RangeFadeFraction))
	gl.Uniform1f(p.U("lodFadeFraction"), world.ScatterLODFadeFraction)
	edgeFade := s.EdgeFadeDistance
	if edgeFade < 0 {
		edgeFade = 0
	}
	gl.Uniform1f(p.U("edgeFadeDistance"), edgeFade)
	gl.Uniform1f(p.U("boundaryJitter"), world.ResolvedBoundaryJitter(s))

	seed := scatterSeed(v)
	gl.Uniform2f(p.U("seed"), seed.X(), seed.Y())
	gl.Uniform1f(p.U("density"), s.Density)
	c := s.Color
	gl.Uniform3f(p.U("tintColor"), c.X(), c.Y(), c.Z())

	shapeKind := s.Kind
	if shapeKind == world.ScatterMoss {
		// Moss has no shader-side case of its own: it reuses Rock's
		// per-instance warp and per-fragment noise-bump shading unchanged. Only
		// the template mesh and this volume's density/spacing/scale differ.
		shapeKind = world.ScatterRock
	}
	gl.Uniform1i(p.U("shapeKind"), shapeKind)

	// Neutral bloom/clump state, overridden by the Wildflower and Grass cases
	// below. Uniforms persist across draw calls, so these must be reset for
	// every volume — otherwise a wildflower's bloom settings leak onto the next
	// volume's cattail seed heads (which share the head code path), and a
	// clumped grass volume gathers the next one's rocks into tufts.
	gl.Uniform3f(p.U("bloomAspect"), 1, 1, 1)
	gl.Uniform1i(p.U("hasCenter"), 0)
	gl.Uniform1i(p.U("hasLeaves"), 0)
	gl.Uniform1f(p.U("leafScale"), 1)
	gl.Uniform1i(p.U("bloomSolid"), 0)
	gl.Uniform3f(p.U("centerColor"), 1, 1, 1)
	gl.Uniform1f(p.U("clump"), 0)

	switch s.Kind {
	case world.ScatterRock:
		rockScale := s.RockScale
		if rockScale <= 0 {
			rockScale = world.DefaultRockScale
		}
		rm := scatters.rockMeshFor(s.RockSides)
		n, sp := scatterLattice(outer, rockSpacing, maxRockGridN)
		gl.Uniform1f(p.U("rockScale"), rockScale)
		gl.Uniform1f(p.U("spacing"), sp)
		gl.Uniform1i(p.U("gridN"), n)
		gl.BindVertexArray(rm.vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, rm.verts, n*n)

	case world.ScatterMoss:
		clumpScale := s.RockScale
		if clumpScale <= 0 {
			clumpScale = world.DefaultRockScale
		}
		vao, verts, baseSpacing, cap := scatters.mossTemplate(tier)
		n, sp := scatterLattice(outer, baseSpacing, cap)
		gl.Uniform1f(p.U("rockScale"), clumpScale)
		gl.Uniform1f(p.U("spacing"), sp)
		gl.Uniform1i(p.U("gridN"), n)
		gl.BindVertexArray(vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, verts, n*n)

	case world.ScatterWildflower:
		bh := stemHeight(s.GrassHeight, world.DefaultWildflowerHeight)
		bw := s.GrassWidth
		if bw <= 0 {
			bw = world.DefaultWildflowerWidth
		}
		hs := s.HeadScale
		if hs <= 0 {
			hs = world.DefaultWildflowerHeadScale
		}
		aspect := WildflowerDiscAspect
		if s.BloomSpike {
			aspect = WildflowerSpikeAspect
		}
		cc := s.CenterColor
		if cc == (mgl32.Vec3{}) {
			cc = world.DefaultCenterColor
		}
		vao, verts, baseSpacing, cap := scatters.wildflowerTemplate(tier)
		n, sp := scatterLattice(outer, baseSpacing, cap)
		gl.Uniform1f(p.U("bladeHeight"), bh)
		gl.Uniform1f(p.U("bladeWidth"), bw)
		gl.Uniform1f(p.U("headScale"), hs)
		gl.Uniform3f(p.U("bloomAspect"), aspect.X(), aspect.Y(), aspect.Z())
		// Mid/Far templates carry no centre or leaf geometry at all, so leaving
		// these at the neutral 0 the reset above already set is belt-and-braces
		// rather than load-bearing.
		if tier == 0 {
			gl.Uniform1i(p.U("hasCenter"), boolToInt32(s.BloomCenter))
			gl.Uniform1i(p.U("hasLeaves"), boolToInt32(s.StemLeaves))
			gl.Uniform1f(p.U("leafScale"), world.ResolvedLeafScale(s.LeafScale))
		}
		gl.Uniform1i(p.U("bloomSolid"), boolToInt32(s.BloomSolidColor))
		gl.Uniform3f(p.U("centerColor"), cc.X(), cc.Y(), cc.Z())
		gl.Uniform1f(p.U("spacing"), sp)
		gl.Uniform1i(p.U("gridN"), n)
		gl.BindVertexArray(vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, verts, n*n)

	case world.ScatterCattail:
		// Not routed through stemHeight: a reed reads fine taller than the cap
		// a blade of grass is held to.
		bh := s.GrassHeight
		if bh <= 0 {
			bh = world.DefaultCattailHeight
		}
		bw := s.GrassWidth
		if bw <= 0 {
			bw = world.DefaultCattailWidth
		}
		hs := s.HeadScale
		if hs <= 0 {
			hs = world.DefaultCattailHeadScale
		}
		vao, verts, baseSpacing, cap := scatters.cattailTemplate(tier)
		n, sp := scatterLattice(outer, baseSpacing, cap)
		gl.Uniform1f(p.U("bladeHeight"), bh)
		gl.Uniform1f(p.U("bladeWidth"), bw)
		gl.Uniform1f(p.U("headScale"), hs)
		gl.Uniform1f(p.U("spacing"), sp)
		gl.Uniform1i(p.U("gridN"), n)
		gl.BindVertexArray(vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, verts, n*n)

	default: // ScatterGrass
		bh := stemHeight(s.GrassHeight, world.DefaultGrassHeight)
		bw := s.GrassWidth
		if bw <= 0 {
			bw = world.DefaultGrassWidth
		}
		vao, verts, baseSpacing, cap := scatters.grassTemplate(tier)
		n, sp := scatterLattice(outer, baseSpacing, cap)
		gl.Uniform1f(p.U("bladeHeight"), bh)
		gl.Uniform1f(p.U("bladeWidth"), bw)
		// Grass is the only kind that clumps, and only its Near tier — Mid is
		// already a cheaper single blade, Far is already a pre-baked group, and
		// the tuft-gather math assumes Near's own tight lattice spacing.
		if tier == 0 {
			gl.Uniform1f(p.U("clump"), world.ClampGrassClump(s.GrassClump))
		}
		gl.Uniform1f(p.U("spacing"), sp)
		gl.Uniform1i(p.U("gridN"), n)
		gl.BindVertexArray(vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, verts, n*n)
	}
}

// stemHeight applies the "0 means the kind's default" convention and the shared
// cap.
func stemHeight(v, def float32) float32 {
	if v <= 0 {
		v = def
	}
	if v > world.MaxStemHeight {
		v = world.MaxStemHeight
	}
	return v
}

// scatterSeed derives a per-volume seed from its ID, so two volumes overlapping
// in world space do not scatter identically — tall grass and short grass over
// the same ground land on different spots instead of stacking.
func scatterSeed(v *world.Volume) mgl32.Vec2 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(v.ID); i++ {
		h ^= uint64(v.ID[i])
		h *= 1099511628211
	}
	return mgl32.Vec2{
		float32(h&0xffff) * (1.0 / 65536.0) * 100,
		float32((h>>16)&0xffff) * (1.0 / 65536.0) * 100,
	}
}

// endScatterPass restores GL state for the rest of the frame, rebinding restore
// as the active program — the main Program after the color pass, the relevant
// depth program after a shadow pass, matching what the caller interrupted.
func endScatterPass(restore uint32) {
	gl.BindVertexArray(0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.UseProgram(restore)
}
