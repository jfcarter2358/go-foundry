package main

// biome_shadow_decal.go — fake ground-contact shadow for Grass/Wildflower/
// Cattail scatter (see BiomeBrush.FakeShadowStrength/FakeShadowSpread).
//
// Real per-instance shadow casting exists for these kinds (BiomeShadows.
// CastFoliage) but is off by default: a blade thinner than a shadow map texel
// aliases into flickering speckle more than it convinces, and casting it at
// all means re-instancing the whole scatter into every shadow map it appears
// in. This is the cheap alternative — not a shadow MAP at all, just a small
// decal mesh, built once per sector and cached exactly like the ground patch
// it sits on top of.
//
// Two things it darkens, matching where the two-part problem actually was:
//   - the sector's own ground patch (biome_ground.go) already darkens itself,
//     via the fakeShadowStrength uniform in ground_frag.glsl — no new
//     geometry needed there, since the patch already conforms to exactly the
//     right surface;
//   - the STRUCTURAL floor surrounding the sector, which the ground patch
//     mesh never reaches (it stops exactly at the painted polygon edge). This
//     file's ring mesh is the part that reaches past that edge — sampling the
//     real structural surface (floorHeightNormalUnderLevel, not
//     biomeSurfaceAt: outside the sector's own territory, there is no biome
//     noise to apply) so it drapes over whatever's actually there.
//
// Self-contained GL program, like biomeRenderer/groundRenderer: never touches
// the main game shader's state, and has no lighting of its own at all — see
// biome_decal_frag.glsl's doc comment for why that's the point, not an
// oversight.

import (
	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// decalVertexStride is floats per vertex: aPos(3) aWeight(1).
const decalVertexStride = 4

// biomeDecalRingSegment is the target arc-length (world units) between ring
// vertices along the sector boundary — independent of FakeShadowSpread (the
// ring's outward reach), this just bounds how faceted a long straight edge or
// a tight curve looks. Coarser than the ground patch's own
// groundPatchCellSize: the decal is a soft blended gradient, not textured
// geometry, so it doesn't need the same resolution to read cleanly.
const biomeDecalRingSegment = float32(2.0)

// biomeDecalYBias lifts the decal ring above whatever it's drawn over — the
// ground patch (already at +groundPatchYBias) on the inner edge, the bare
// structural floor on the outer edge — by enough to avoid z-fighting with
// either without the two visibly stepping apart at the shared inner boundary.
const biomeDecalYBias = float32(0.02)

// decalEntry is a cached per-sector halo mesh.
type decalEntry struct {
	vao, vbo uint32
	verts    int32
	spread   float32 // the FakeShadowSpread this mesh was built for
}

// decalRenderer holds the GL program and cached per-sector meshes for the fake
// ground-contact shadow halo.
type decalRenderer struct {
	program uint32
	cache   map[string]decalEntry

	uProjView, uStrength int32
	aPos, aWeight        uint32

	initialized bool
}

var biomeDecals decalRenderer

// DecalInit compiles the decal shader program. Must be called once with the GL
// context current — GrassInit calls it alongside GroundInit.
func DecalInit() {
	biomeDecals.program = mustBiomeProgram(
		biomeShaderPath("biome_decal_vert.glsl"), biomeShaderPath("biome_decal_frag.glsl"), "", "")

	p := biomeDecals.program
	biomeDecals.uProjView = gl.GetUniformLocation(p, gl.Str("projView\x00"))
	biomeDecals.uStrength = gl.GetUniformLocation(p, gl.Str("strength\x00"))
	biomeDecals.aPos = uint32(gl.GetAttribLocation(p, gl.Str("aPos\x00")))
	biomeDecals.aWeight = uint32(gl.GetAttribLocation(p, gl.Str("aWeight\x00")))

	biomeDecals.cache = map[string]decalEntry{}
	biomeDecals.initialized = true
}

// EvictBiomeDecalCache releases the cached halo mesh for a deleted biome
// sector. Folded into EvictBiomeSectorCaches (grass.go) alongside the height
// texture and ground patch it's derived from the same way.
func EvictBiomeDecalCache(id string) {
	e, ok := biomeDecals.cache[id]
	if !ok {
		return
	}
	gl.DeleteVertexArrays(1, &e.vao)
	gl.DeleteBuffers(1, &e.vbo)
	delete(biomeDecals.cache, id)
}

// resolvedFakeShadowSpread applies the usual "0 means the kind's default"
// convention to FakeShadowSpread.
func resolvedFakeShadowSpread(v float32) float32 {
	if !(v > 0) { // also catches NaN out of a hand-edited save
		return defaultFakeShadowSpread
	}
	return v
}

// rotateRight90 rotates a 2D vector -90 degrees (clockwise). For a CCW
// polygon's directed edge a->b, this points OUTWARD — the interior is to the
// LEFT of every directed edge in a CCW winding, so right-of-direction is away
// from it.
func rotateRight90(v mgl32.Vec2) mgl32.Vec2 {
	return mgl32.Vec2{v.Y(), -v.X()}
}

// buildBiomeDecalMesh builds the ring-strip vertex buffer (see
// decalVertexStride) for sector s: for every densified boundary edge, a quad
// between the sector's own edge (weight 1, height on the sector's own
// conforming surface via biomeSurfaceAt) and a point offset outward by spread
// along that vertex's outward normal (weight 0, height on the bare structural
// floor via floorHeightNormalUnderLevel — no biome noise: that ground belongs
// to whatever's actually there, not to this sector).
//
// An outer point with no structural floor beneath it (open air, or off the
// edge of built geometry) collapses to its inner point instead of extending
// into nothing — the segment's quad still exists but has zero area, so it
// draws nothing rather than a halo hanging in space.
func buildBiomeDecalMesh(level *Level, s *BiomeSector, spread float32) []float32 {
	boundary := ccwPolygon(s.Points)
	if len(boundary) < 3 {
		return nil
	}
	boundary = densifyPolygon(boundary, biomeDecalRingSegment)
	n := len(boundary)
	if n < 3 {
		return nil
	}

	seed := biomeSeed(s)
	out := make([]float32, 0, n*2*decalVertexStride*3) // 2 tris per edge, 3 verts each

	innerAt := func(p mgl32.Vec2) mgl32.Vec3 {
		h, _ := biomeSurfaceAt(level, &s.Brush, seed, p.X(), p.Y())
		return mgl32.Vec3{p.X(), h + biomeDecalYBias, p.Y()}
	}
	outerAt := func(inner mgl32.Vec3, p mgl32.Vec2) mgl32.Vec3 {
		h, _, sec := floorHeightNormalUnderLevel(level, p.X(), p.Y())
		if sec == nil {
			return inner // no floor out here — collapse the quad to zero area
		}
		return mgl32.Vec3{p.X(), h + biomeDecalYBias, p.Y()}
	}
	outwardNormalAt := func(i int) mgl32.Vec2 {
		prev := boundary[(i-1+n)%n]
		cur := boundary[i]
		next := boundary[(i+1)%n]
		nPrev := rotateRight90(cur.Sub(prev))
		nNext := rotateRight90(next.Sub(cur))
		sum := nPrev.Add(nNext)
		if sum.Len() < 1e-8 {
			// Prev/next edges exactly cancel (a 180-degree fold) — fall back to
			// just one of them rather than dividing by zero.
			sum = nNext
			if sum.Len() < 1e-8 {
				return mgl32.Vec2{0, 0}
			}
		}
		return sum.Normalize()
	}

	innerPts := make([]mgl32.Vec3, n)
	outerPts := make([]mgl32.Vec3, n)
	for i := range boundary {
		inner := innerAt(boundary[i])
		innerPts[i] = inner
		outward := outwardNormalAt(i)
		outerPts[i] = outerAt(inner, boundary[i].Add(outward.Mul(spread)))
	}

	emit := func(p mgl32.Vec3, w float32) {
		out = append(out, p.X(), p.Y(), p.Z(), w)
	}
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		pi, po := innerPts[i], outerPts[i]
		pj, qo := innerPts[j], outerPts[j]
		// Two triangles per boundary edge: (inner_i, outer_i, inner_j) and
		// (outer_i, outer_j, inner_j) — the standard ring-strip quad split.
		emit(pi, 1)
		emit(po, 0)
		emit(pj, 1)
		emit(po, 0)
		emit(qo, 0)
		emit(pj, 1)
	}
	return out
}

// ensureBiomeDecal returns the cached halo mesh for sector s, rebuilding it if
// missing, GroundDirty (the same trigger the ground patch mesh it drapes
// alongside uses — both are derived from the sector's points and the
// structural floor under and around them), or if FakeShadowSpread changed
// since the cached build (checked directly rather than relying on every caller
// to remember to set GroundDirty for it).
func ensureBiomeDecal(level *Level, s *BiomeSector) decalEntry {
	spread := resolvedFakeShadowSpread(s.Brush.FakeShadowSpread)
	e, ok := biomeDecals.cache[s.ID]
	if ok && !s.GroundDirty && e.spread == spread {
		return e
	}
	verts := buildBiomeDecalMesh(level, s, spread)
	if !ok {
		gl.GenVertexArrays(1, &e.vao)
		gl.GenBuffers(1, &e.vbo)
	}
	gl.BindVertexArray(e.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, e.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.DYNAMIC_DRAW)

	stride := int32(decalVertexStride * 4)
	gl.EnableVertexAttribArray(biomeDecals.aPos)
	gl.VertexAttribPointerWithOffset(biomeDecals.aPos, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(biomeDecals.aWeight)
	gl.VertexAttribPointerWithOffset(biomeDecals.aWeight, 1, gl.FLOAT, false, stride, 3*4)
	gl.BindVertexArray(0)

	e.verts = int32(len(verts) / decalVertexStride)
	e.spread = spread
	biomeDecals.cache[s.ID] = e
	return e
}

// RenderBiomeShadowDecal draws every eligible sector's fake ground-contact
// shadow halo (fakesGroundShadow(kind) && FakeShadowStrength > 0 &&
// FakeShadowSpread's resolved value > 0 — the strength gate is the per-sector
// on/off switch, since 0 is a legitimate "don't want this" the same way
// GrassClump's 0 is). Multiply-blended over whatever opaque geometry already
// drew (ground patches, structural floor) with depth testing on but depth
// writes off, so the halo is hidden behind nearer geometry (a wall foot, a
// rock) without punching a hole in the depth buffer for anything after it.
// Uses its own GL program; restores the main program and default blend/depth
// state on exit.
//
// Call after RenderBiomeGround and the scatter pass (RenderBiomes calls this
// last), so the halo darkens the ground patch and any scatter already drawn on
// top of it rather than being drawn under and then covered up.
func RenderBiomeShadowDecal(level *Level, projView mgl32.Mat4) {
	if !biomeDecals.initialized || level == nil {
		return
	}

	started := false
	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		for si := range ch.BiomeSectors {
			s := &ch.BiomeSectors[si]
			if len(s.Points) < 3 || !fakesGroundShadow(s.Brush.Kind) {
				continue
			}
			strength := clamp01(s.Brush.FakeShadowStrength)
			if strength <= 0 || resolvedFakeShadowSpread(s.Brush.FakeShadowSpread) <= 0 {
				continue
			}
			entry := ensureBiomeDecal(level, s)
			if entry.verts == 0 {
				continue
			}
			if !started {
				gl.UseProgram(biomeDecals.program)
				pv := projView
				gl.UniformMatrix4fv(biomeDecals.uProjView, 1, false, &pv[0])
				gl.Enable(gl.BLEND)
				gl.BlendFunc(gl.ZERO, gl.SRC_COLOR)
				gl.DepthMask(false)
				started = true
			}
			gl.Uniform1f(biomeDecals.uStrength, strength)
			gl.BindVertexArray(entry.vao)
			gl.DrawArrays(gl.TRIANGLES, 0, entry.verts)
		}
	}

	if started {
		gl.BindVertexArray(0)
		gl.DepthMask(true)
		gl.Disable(gl.BLEND)
		gl.UseProgram(Program)
	}
}
