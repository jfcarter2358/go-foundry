package render

// biome_ground.go — conforming, textured, lightly-displaced ground patch
// rendered underneath every painted biome sector (except the water surface
// itself, which is a separate transparent surface — see biome_water.go;
// its patch here is the lakebed seen through it).
//
// Painting grass, rock, wildflowers, cattails or sand over open ground would
// otherwise leave the structural floor's own texture showing through
// unchanged — a flat, un-textured-to-match cutout under the scatter. Instead
// every land sector gets its own small mesh: a grid clipped to the sector's
// polygon, each vertex dropped onto the structural floor beneath it (like the
// scatter height sample) and then displaced a little further — barely for
// grass/rock/wildflower/cattail (just enough to read as uneven ground instead
// of a perfectly flat plane), a lot for Sand (dune mounds — the "volumetric
// displacement" sand asked for), and gently for Water's lakebed. It's drawn
// textured (dirt/rock/sand per kind, see GroundTextureFor) and lit by exactly
// the scene lighting model the structural floor takes, nothing added on top:
// the patch INTERSECTS that floor wherever the displacement crosses it, and any
// extra shading term of its own shows up there as two differently-lit floors
// meeting. See the header of ground_frag.glsl.
//
// Self-contained GL program, like biomeRenderer in grass.go — never touches
// the main game shader's state.

import (
	"foundry/internal/biome"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// GroundVertexStride is floats per vertex: aPos(3) aNormal(3) aUV(2)
// aCoverage(1).
const GroundVertexStride = 9

// groundEntry is a cached per-sector ground patch mesh.
type groundEntry struct {
	vao, vbo uint32
	verts    int32
	texture  string
}

// groundRenderer holds the GL program and cached per-sector meshes for the
// ground-patch pass.
type groundRenderer struct {
	program uint32

	cache map[string]groundEntry

	uProjView, uTex    int32
	aPos, aNormal, aUV uint32
	aCoverage          uint32

	initialized bool
}

var groundPatches groundRenderer

// GroundInit compiles the ground-patch shader program. Must be called once
// with the GL context current — GrassInit calls it.
//
// The patch is an ordinary opaque surface with an ordinary fragment count, so
// it takes the full-cost lighting block: 5x5 cascade PCF matching
// fragment.glsl's own filter, the cascade cross-fade band, and point-light
// cubemap shadows. Anything cheaper shows as a seam, since the patch's shadows
// have to line up with those on the structural floor it drapes over — and
// crosses. See biomeLightSnippet.
func GroundInit() {
	// The vertex stage takes the cheapest variant of the block — all it needs
	// from it is biomeViewDepth; declaring the cubemap samplers there too would
	// just hand the linker vertex texture units it has no use for.
	groundPatches.program = mustBiomeProgram(
		biomeShaderPath("ground_vert.glsl"), biomeShaderPath("ground_frag.glsl"),
		biomeLightSnippet(0, false, false), biomeLightSnippet(2, true, true))

	p := groundPatches.program
	groundPatches.uProjView = gl.GetUniformLocation(p, gl.Str("projView\x00"))
	groundPatches.uTex = gl.GetUniformLocation(p, gl.Str("tex\x00"))

	groundPatches.aPos = uint32(gl.GetAttribLocation(p, gl.Str("aPos\x00")))
	groundPatches.aNormal = uint32(gl.GetAttribLocation(p, gl.Str("aNormal\x00")))
	groundPatches.aUV = uint32(gl.GetAttribLocation(p, gl.Str("aUV\x00")))
	groundPatches.aCoverage = uint32(gl.GetAttribLocation(p, gl.Str("aCoverage\x00")))

	groundPatches.cache = map[string]groundEntry{}
	groundPatches.initialized = true
}

// EvictGroundPatchCache releases the cached mesh for a deleted biome sector.
func EvictGroundPatchCache(id string) {
	e, ok := groundPatches.cache[id]
	if !ok {
		return
	}
	gl.DeleteVertexArrays(1, &e.vao)
	gl.DeleteBuffers(1, &e.vbo)
	delete(groundPatches.cache, id)
}

// ResolveGroundTexture returns the texture to paint on brush's ground patch:
// brush.GroundTexture when the user picked one, else the kind's default
// (GroundTextureFor).
func ResolveGroundTexture(brush *world.BiomeBrush) string {
	if brush.GroundTexture != "" {
		return brush.GroundTexture
	}
	return world.GroundTextureFor(brush.Kind)
}

// resolveGroundUVScale returns the texture tiling baked into brush's ground
// patch UVs (world XZ * scale, matching a structural floor's Surface.UVScale
// semantics), substituting DefaultGroundUVScale for a non-positive component
// so a partly-set or pre-existing (zero) brush still tiles sanely. Written as
// `!(v > 0)` so a NaN out of a hand-edited save falls back too rather than
// poisoning every UV in the mesh.
func ResolveGroundUVScale(brush *world.BiomeBrush) mgl32.Vec2 {
	s := brush.GroundUVScale
	if !(s.X() > 0) {
		s[0] = world.DefaultGroundUVScale
	}
	if !(s.Y() > 0) {
		s[1] = world.DefaultGroundUVScale
	}
	return s
}

// BuildGroundPatchMesh builds the flat triangle-list vertex buffer (see
// GroundVertexStride) for sector s: a Delaunay triangulation of the sector's
// own (densified) polygon boundary plus an interior sample grid — see
// biomeMeshPoints — height-conformed to the structural floor plus
// biome.GroundDisplacement. Because the triangulation's boundary points sit
// exactly on the polygon's edges, the mesh's outer edge follows the painted
// shape precisely rather than the axis-aligned staircase a grid-quad clip
// would give a diagonal edge.
func BuildGroundPatchMesh(level *world.Level, s *world.BiomeSector) []float32 {
	boundary, allPts := biome.BiomeMeshPoints(s)
	if len(allPts) == 0 {
		return nil
	}
	triIdx := geom.DelaunayTriangulate(allPts, boundary)
	if len(triIdx) == 0 {
		return nil
	}

	seed := world.BiomeSeed(s)
	heights := make([]float32, len(allPts))
	coverage := make([]float32, len(allPts))
	for i, p := range allPts {
		// biomeSurfaceAt is the shared definition of where this sector's
		// terrain is — it already folds in biome.GroundPatchYBias and
		// biome.GroundDisplacement, and follows a covering floor's actual displaced
		// surface for Grass/Rock/Sand/Moss, so the patch drapes over
		// rock-outcrop bumps instead of sitting at the flat floor height
		// underneath them. The scatter height texture calls the exact same
		// function, which is what keeps instances planted on this patch.
		//
		// Coverage rides along as a vertex attribute and is thresholded
		// per-pixel in ground_frag.glsl, so the painted layer's *texture* stops
		// at the brush's slope range too — not just the scattered clumps on top
		// of it. Doing it in the fragment stage rather than by dropping
		// triangles here keeps the cutoff on the true slope contour instead of
		// quantizing it to this mesh's ~2-world-unit triangulation.
		heights[i], coverage[i] = biome.BiomeSurfaceAt(level, &s.Brush, seed, p.X(), p.Y())
	}

	uvScale := ResolveGroundUVScale(&s.Brush)
	out := make([]float32, 0, len(triIdx)*GroundVertexStride)
	for i := 0; i < len(triIdx); i += 3 {
		ia, ib, ic := triIdx[i], triIdx[i+1], triIdx[i+2]
		pa := mgl32.Vec3{allPts[ia].X(), heights[ia], allPts[ia].Y()}
		pb := mgl32.Vec3{allPts[ib].X(), heights[ib], allPts[ib].Y()}
		pc := mgl32.Vec3{allPts[ic].X(), heights[ic], allPts[ic].Y()}
		n := pb.Sub(pa).Cross(pc.Sub(pa))
		if n.Len() > 1e-8 {
			n = n.Normalize()
		} else {
			n = mgl32.Vec3{0, 1, 0}
		}
		if n.Y() < 0 {
			// Ground patches are never legitimately downward-facing; a
			// triangle picking up the "wrong" winding from the
			// triangulation (face culling is off for this pass, so it
			// still draws either way) would otherwise light as if seen
			// from underneath.
			n = n.Mul(-1)
		}
		for k, p := range [3]mgl32.Vec3{pa, pb, pc} {
			cov := coverage[[3]int{ia, ib, ic}[k]]
			out = append(out, p.X(), p.Y(), p.Z(), n.X(), n.Y(), n.Z(),
				p.X()*uvScale.X(), p.Z()*uvScale.Y(), cov)
		}
	}
	return out
}

// ensureGroundPatch returns the cached ground patch mesh for sector s,
// rebuilding it if missing or GroundDirty.
//
// This runs later in the frame than SyncBiomeWaterSurfaces (see main.go),
// so it's the one that clears GroundDirty once built — by the time it does,
// the water sync has already had its chance to see the sector dirty too.
func ensureGroundPatch(level *world.Level, s *world.BiomeSector) groundEntry {
	e, ok := groundPatches.cache[s.ID]
	tex := ResolveGroundTexture(&s.Brush)
	if ok && !s.GroundDirty && e.texture == tex {
		return e
	}
	verts := BuildGroundPatchMesh(level, s)
	s.GroundDirty = false
	if !ok {
		gl.GenVertexArrays(1, &e.vao)
		gl.GenBuffers(1, &e.vbo)
	}
	gl.BindVertexArray(e.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, e.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.DYNAMIC_DRAW)

	stride := int32(GroundVertexStride * 4)
	gl.EnableVertexAttribArray(groundPatches.aPos)
	gl.VertexAttribPointerWithOffset(groundPatches.aPos, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(groundPatches.aNormal)
	gl.VertexAttribPointerWithOffset(groundPatches.aNormal, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(groundPatches.aUV)
	gl.VertexAttribPointerWithOffset(groundPatches.aUV, 2, gl.FLOAT, false, stride, 6*4)
	gl.EnableVertexAttribArray(groundPatches.aCoverage)
	gl.VertexAttribPointerWithOffset(groundPatches.aCoverage, 1, gl.FLOAT, false, stride, 8*4)
	gl.BindVertexArray(0)

	e.verts = int32(len(verts) / GroundVertexStride)
	e.texture = tex
	groundPatches.cache[s.ID] = e
	return e
}

// RenderBiomeGround draws every chunk's painted biome sector's conforming
// ground patch (every kind, including Water's lakebed and every scatter
// kind's underlay). Uses its own GL program; restores the main program on
// exit. Call in the opaque render pass, before the scatter instances so
// blades/rocks draw on top of (not z-fighting with) their own ground.
//
// frame carries the directional cascade setup from RenderShadowPass. The patch
// is not itself drawn into the shadow maps — it is a thin drape over a
// structural floor that already casts, and giving two near-coincident surfaces
// to the same depth map buys acne rather than shadows. (Sand is the one kind
// whose dunes rise far enough to make that arguable.)
func RenderBiomeGround(level *world.Level, projView mgl32.Mat4, camPos mgl32.Vec3, frame ShadowFrame) {
	if !groundPatches.initialized || level == nil {
		return
	}

	started := false
	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		for si := range ch.BiomeSectors {
			s := &ch.BiomeSectors[si]
			if len(s.Points) < 3 {
				continue
			}
			entry := ensureGroundPatch(level, s)
			if entry.verts == 0 {
				continue
			}
			if !started {
				beginGroundPass(level, projView, frame)
				started = true
			}
			_ = gfx.EnsureTextureLoaded(entry.texture)
			gl.ActiveTexture(gl.TEXTURE0)
			gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[entry.texture])
			gl.BindVertexArray(entry.vao)
			gl.DrawArrays(gl.TRIANGLES, 0, entry.verts)
		}
	}

	if started {
		gl.BindVertexArray(0)
		gl.ActiveTexture(gl.TEXTURE0)
		gl.UseProgram(gfx.Program)
	}
}

// beginGroundPass activates the ground-patch program and uploads the
// uniforms shared by every patch this frame: the whole scene light and shadow
// state (uploadBiomeLighting — the same three Upload*To calls the main shader
// and the detail programs get) plus the patch pass's own two.
func beginGroundPass(level *world.Level, projView mgl32.Mat4, frame ShadowFrame) {
	uploadBiomeLighting(groundPatches.program, level, frame)
	pv := projView
	gl.UniformMatrix4fv(groundPatches.uProjView, 1, false, &pv[0])
	gl.Uniform1i(groundPatches.uTex, 0)
}
