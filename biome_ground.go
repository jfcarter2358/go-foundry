package main

// biome_ground.go — conforming, textured, lightly-displaced ground patch
// rendered underneath every painted biome sector (except the water surface
// itself, which is a separate transparent primitive — see biome_water.go;
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
// textured (dirt/rock/sand per kind, see groundTextureFor) and lit by exactly
// the scene lighting model the structural floor takes, nothing added on top:
// the patch INTERSECTS that floor wherever the displacement crosses it, and any
// extra shading term of its own shows up there as two differently-lit floors
// meeting. See the header of ground_frag.glsl.
//
// Self-contained GL program, like biomeRenderer in grass.go — never touches
// the main game shader's state.

import (
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// groundVertexStride is floats per vertex: aPos(3) aNormal(3) aUV(2)
// aCoverage(1).
const groundVertexStride = 9

// groundPatchCellSize is the target grid spacing (world units) — coarse
// relative to the scatter/height-texture resolution, since this is real
// geometry (triangle count matters) rather than a texture sample.
const groundPatchCellSize = float32(2.0)

// groundPatchMaxDim caps the interior sample grid's cells-per-axis, bounding
// triangle count for a huge sector the same way the scatter passes bound
// instance count. Lower than a pure-grid approach would need, since
// Bowyer-Watson triangulation (see buildGroundPatchMesh) costs more per point.
const groundPatchMaxDim = 64

// groundPatchYBias lifts the patch a hair above the structural floor it
// conforms to, avoiding z-fighting between the two coincident surfaces.
const groundPatchYBias = float32(0.015)

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

	uProjView, uTex     int32
	uFakeShadowStrength int32
	aPos, aNormal, aUV  uint32
	aCoverage           uint32

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
	groundPatches.uFakeShadowStrength = gl.GetUniformLocation(p, gl.Str("fakeShadowStrength\x00"))

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

// groundDisplacement returns the extra vertical offset (beyond the
// structural floor height) baked into the ground patch at world (wx,wz),
// keyed by the painting kind — this is the "volumetric displacement" Sand
// asked for, and the subtler unevenness every other land kind gets so its
// patch doesn't look like a flat cutout.
func groundDisplacement(kind int32, brush *BiomeBrush, seed mgl32.Vec2, wx, wz float32) float32 {
	freq := brush.GroundFrequency
	oct := clampOctaves(brush.GroundOctaves)

	switch kind {
	case BiomeSand:
		amp := brush.SandHeight
		if amp <= 0 {
			amp = defaultSandHeight
		}
		if freq <= 0 {
			freq = defaultSandFrequency
		}
		if brush.GroundOctaves <= 0 {
			oct = int(defaultSandOctaves)
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

	case BiomeWater:
		amp := brush.GroundAmplitude
		if amp <= 0 {
			amp = defaultWaterGroundAmplitude
		}
		if freq <= 0 {
			freq = defaultWaterGroundFrequency
		}
		if brush.GroundOctaves <= 0 {
			oct = int(defaultWaterGroundOctaves)
		}
		return (groundFBM(wx*freq+seed.X(), wz*freq+seed.Y(), oct)*2 - 1) * amp // gently uneven lakebed

	default: // Grass, Rock, Wildflower, Cattail, Moss
		amp := brush.GroundAmplitude
		if amp <= 0 {
			amp = defaultGroundAmplitude
		}
		if freq <= 0 {
			freq = defaultGroundFrequency
		}
		if brush.GroundOctaves <= 0 {
			oct = int(defaultGroundOctaves)
		}
		return (groundFBM(wx*freq+seed.X(), wz*freq+seed.Y(), oct)*2 - 1) * amp
	}
}

// clampOctaves clamps a user-supplied octave count into groundFBM's sane
// range, defaulting a non-positive (unset) value to 1 — callers that need a
// kind-specific default override this afterward when brush.GroundOctaves<=0.
func clampOctaves(n int32) int {
	if n < 1 {
		n = 1
	}
	if n > 6 {
		n = 6
	}
	return int(n)
}

// resolveGroundTexture returns the texture to paint on brush's ground patch:
// brush.GroundTexture when the user picked one, else the kind's default
// (groundTextureFor).
func resolveGroundTexture(brush *BiomeBrush) string {
	if brush.GroundTexture != "" {
		return brush.GroundTexture
	}
	return groundTextureFor(brush.Kind)
}

// resolveGroundUVScale returns the texture tiling baked into brush's ground
// patch UVs (world XZ * scale, matching a structural floor's Surface.UVScale
// semantics), substituting defaultGroundUVScale for a non-positive component
// so a partly-set or pre-existing (zero) brush still tiles sanely. Written as
// `!(v > 0)` so a NaN out of a hand-edited save falls back too rather than
// poisoning every UV in the mesh.
func resolveGroundUVScale(brush *BiomeBrush) mgl32.Vec2 {
	s := brush.GroundUVScale
	if !(s.X() > 0) {
		s[0] = defaultGroundUVScale
	}
	if !(s.Y() > 0) {
		s[1] = defaultGroundUVScale
	}
	return s
}

// groundPatchGridDims picks the (cells-per-axis) grid resolution covering
// [min,max] at ~groundPatchCellSize world units per cell, capped at
// groundPatchMaxDim.
func groundPatchGridDims(lo, hi mgl32.Vec2) (nx, nz int) {
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
	nx = max(1, min(nx, groundPatchMaxDim))
	nz = max(1, min(nz, groundPatchMaxDim))
	return
}

// densifyPolygonMaxStepsPerEdge caps how many extra points densifyPolygon
// inserts along any one edge — a safety valve against a pathologically long
// edge with a tiny maxSeg blowing up the triangulation's point count.
const densifyPolygonMaxStepsPerEdge = 200

// densifyPolygon inserts evenly-spaced intermediate points along every edge
// of pts so no segment exceeds maxSeg, preserving the polygon's exact shape
// (every inserted point is exactly collinear with the edge it splits) and
// winding order.
//
// Feeding this — rather than just the sector's own hand-placed vertices —
// into delaunayTriangulate as its boundary is what makes the mesh's edge
// follow the polygon exactly: with only the sparse hand-placed vertices as
// boundary points, Bowyer-Watson has nothing to stop it from routing a
// boundary edge through a long diagonal as a staircase of grid-aligned
// chords to nearby interior points instead of the actual polygon edge. Extra
// points ON that edge give it points to snap to along its own line instead.
func densifyPolygon(pts []mgl32.Vec2, maxSeg float32) []mgl32.Vec2 {
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

// ccwPolygon returns pts in counter-clockwise winding (per signedArea2D),
// reversing a copy if it isn't already — delaunayTriangulate/earClip both
// assume CCW. BiomeSector.Points is normalized to CCW when a sector is first
// drawn (finishBiomeSectorDraw), but nothing re-checks it after a vertex is
// dragged, so this is a defensive re-check rather than a known-redundant one.
func ccwPolygon(pts []mgl32.Vec2) []mgl32.Vec2 {
	if signedArea2D(pts) >= 0 {
		return pts
	}
	out := make([]mgl32.Vec2, len(pts))
	for i, p := range pts {
		out[len(pts)-1-i] = p
	}
	return out
}

// biomeMeshPoints builds the point cloud buildGroundPatchMesh/
// buildBiomeWaterMesh both triangulate: sector s's polygon boundary
// (densified so long edges get their own points to snap to, see
// densifyPolygon) followed by an interior grid of points strictly inside it.
// Returns the densified boundary too — delaunayTriangulate needs it both as
// input points and as the inside/outside test polygon.
func biomeMeshPoints(s *BiomeSector) (boundary, allPts []mgl32.Vec2) {
	boundary = ccwPolygon(s.Points)
	if len(boundary) < 3 {
		return nil, nil
	}
	min, max := s.Bounds()
	nx, nz := groundPatchGridDims(min, max)
	cellX := (max.X() - min.X()) / float32(nx)
	cellZ := (max.Y() - min.Y()) / float32(nz)
	cell := cellX
	if cellZ < cell {
		cell = cellZ
	}
	if cell <= 0 {
		cell = groundPatchCellSize
	}
	boundary = densifyPolygon(boundary, cell)

	allPts = make([]mgl32.Vec2, 0, len(boundary)+nx*nz)
	allPts = append(allPts, boundary...)
	for iz := 1; iz < nz; iz++ {
		wz := min.Y() + float32(iz)*cellZ
		for ix := 1; ix < nx; ix++ {
			wx := min.X() + float32(ix)*cellX
			p := mgl32.Vec2{wx, wz}
			if pointInPolygon(p, boundary) {
				allPts = append(allPts, p)
			}
		}
	}
	return boundary, allPts
}

// buildGroundPatchMesh builds the flat triangle-list vertex buffer (see
// groundVertexStride) for sector s: a Delaunay triangulation of the sector's
// own (densified) polygon boundary plus an interior sample grid — see
// biomeMeshPoints — height-conformed to the structural floor plus
// groundDisplacement. Because the triangulation's boundary points sit
// exactly on the polygon's edges, the mesh's outer edge follows the painted
// shape precisely rather than the axis-aligned staircase a grid-quad clip
// would give a diagonal edge.
func buildGroundPatchMesh(level *Level, s *BiomeSector) []float32 {
	boundary, allPts := biomeMeshPoints(s)
	if len(allPts) == 0 {
		return nil
	}
	triIdx := delaunayTriangulate(allPts, boundary)
	if len(triIdx) == 0 {
		return nil
	}

	seed := biomeSeed(s)
	heights := make([]float32, len(allPts))
	coverage := make([]float32, len(allPts))
	for i, p := range allPts {
		// biomeSurfaceAt is the shared definition of where this sector's
		// terrain is — it already folds in groundPatchYBias and
		// groundDisplacement, and follows a covering floor's actual displaced
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
		heights[i], coverage[i] = biomeSurfaceAt(level, &s.Brush, seed, p.X(), p.Y())
	}

	uvScale := resolveGroundUVScale(&s.Brush)
	out := make([]float32, 0, len(triIdx)*groundVertexStride)
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
// This runs later in the frame than SyncBiomeWaterPrimitives (see main.go),
// so it's the one that clears GroundDirty once built — by the time it does,
// the water sync has already had its chance to see the sector dirty too.
func ensureGroundPatch(level *Level, s *BiomeSector) groundEntry {
	e, ok := groundPatches.cache[s.ID]
	tex := resolveGroundTexture(&s.Brush)
	if ok && !s.GroundDirty && e.texture == tex {
		return e
	}
	verts := buildGroundPatchMesh(level, s)
	s.GroundDirty = false
	if !ok {
		gl.GenVertexArrays(1, &e.vao)
		gl.GenBuffers(1, &e.vbo)
	}
	gl.BindVertexArray(e.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, e.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.DYNAMIC_DRAW)

	stride := int32(groundVertexStride * 4)
	gl.EnableVertexAttribArray(groundPatches.aPos)
	gl.VertexAttribPointerWithOffset(groundPatches.aPos, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(groundPatches.aNormal)
	gl.VertexAttribPointerWithOffset(groundPatches.aNormal, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(groundPatches.aUV)
	gl.VertexAttribPointerWithOffset(groundPatches.aUV, 2, gl.FLOAT, false, stride, 6*4)
	gl.EnableVertexAttribArray(groundPatches.aCoverage)
	gl.VertexAttribPointerWithOffset(groundPatches.aCoverage, 1, gl.FLOAT, false, stride, 8*4)
	gl.BindVertexArray(0)

	e.verts = int32(len(verts) / groundVertexStride)
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
func RenderBiomeGround(level *Level, projView mgl32.Mat4, camPos mgl32.Vec3, frame ShadowFrame) {
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
			_ = ensureTextureLoaded(entry.texture)
			gl.ActiveTexture(gl.TEXTURE0)
			gl.BindTexture(gl.TEXTURE_2D, Textures[entry.texture])
			// 0 for every kind that doesn't fake a ground-contact shadow
			// (fakesGroundShadow) or has none painted (FakeShadowStrength==0) —
			// see the uniform's doc comment in ground_frag.glsl.
			strength := float32(0)
			if fakesGroundShadow(s.Brush.Kind) {
				strength = clamp01(s.Brush.FakeShadowStrength)
			}
			gl.Uniform1f(groundPatches.uFakeShadowStrength, strength)
			gl.BindVertexArray(entry.vao)
			gl.DrawArrays(gl.TRIANGLES, 0, entry.verts)
		}
	}

	if started {
		gl.BindVertexArray(0)
		gl.ActiveTexture(gl.TEXTURE0)
		gl.UseProgram(Program)
	}
}

// beginGroundPass activates the ground-patch program and uploads the
// uniforms shared by every patch this frame: the whole scene light and shadow
// state (uploadBiomeLighting — the same three Upload*To calls the main shader
// and the detail programs get) plus the patch pass's own two.
func beginGroundPass(level *Level, projView mgl32.Mat4, frame ShadowFrame) {
	uploadBiomeLighting(groundPatches.program, level, frame)
	pv := projView
	gl.UniformMatrix4fv(groundPatches.uProjView, 1, false, &pv[0])
	gl.Uniform1i(groundPatches.uTex, 0)
}
