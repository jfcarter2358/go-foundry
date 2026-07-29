package main

// grass.go — GPU-scattered biome rendering (procedural grass & rock).
//
// Biomes are painted as BiomeSector polygons (see biome_sector.go), each
// carrying its own brush. Nothing is placed as a scene object: at render
// time a small template mesh (a grass blade or a rock) is drawn with
// glDrawArraysInstanced, and the vertex shader (biome_vert.glsl) generates
// every instance procedurally from gl_InstanceID — scattering it across a
// camera-centered lattice, testing point-in-polygon against the sector's own
// points (uploaded as a uniform array) to decide whether a candidate
// survives, and sampling a small per-sector height texture to drop it onto
// the structural floor beneath it (so scatter follows slopes/elevated
// floors instead of sitting flat). Cost is bounded by the camera ring, not
// world size.
//
// Each BiomeSector renders as its own instanced pass, so overlapping sectors
// naturally layer (e.g. tall + short grass over the same area) — there is no
// shared coverage grid to reconcile.
//
// This mirrors the self-contained GizmoProgram pattern (editor_gizmo.go): its
// own GL program and VAOs, never touching the main game shader's state.

import (
	"fmt"
	"math"
	"path/filepath"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// biomeVertexStride is floats per template vertex: aLocal(3) aNormal(3) aParam(1).
const biomeVertexStride = 7

// Lattice spacing (world units) between scatter slots for each biome. Painted
// density thins these out; spacing sets the maximum (fully-covered) blade count.
const (
	grassSpacing = 0.35
	rockSpacing  = 2.5
)

// Per-instance world size of a unit template.
const (
	grassBladeHeight = 0.55
)

// Caps on lattice side length so a huge draw range can't explode the instance
// count (gridN*gridN instances).
const (
	maxGrassGridN = 320
	maxRockGridN  = 130
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
type biomeRenderer struct {
	program uint32

	// Grass template mesh (appearance is scaled by uniforms, so one mesh suffices).
	bladeVAO, bladeVBO uint32
	bladeVerts         int32
	// Rock template meshes, cached by equatorial side count (mesh topology varies).
	rockMeshes map[int32]rockMesh

	// Per-sector height texture cache, keyed by BiomeSector.ID. Rebuilt only
	// when the sector's HeightDirty flag is set.
	heightCache map[string]biomeHeightEntry

	// Cached uniform locations.
	uProjView, uCamXZ, uSpacing, uGridN, uRange int32
	uTime, uIsRock, uBladeHeight, uRockScale    int32
	uBladeWidth, uDensity                       int32
	uHeightTex, uHeightOrigin, uHeightSize      int32
	uPolyPts, uNumPolyPts, uPolyMin, uPolyMax   int32
	uSeed, uTintColor                           int32
	uAmbient, uSunDir, uSunColor                int32
	aLocal, aNormal, aParam                     uint32

	initialized bool
}

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

// GrassInit compiles the biome shader program and builds the grass template mesh.
// Must be called once with the GL context current (see EditorInit).
func GrassInit() {
	var err error
	biomes.program, err = compileProgramFromFiles(
		filepath.Join(executablePath, "shaders/biome_vert.glsl"),
		filepath.Join(executablePath, "shaders/biome_frag.glsl"),
	)
	if err != nil {
		panic(fmt.Sprintf("biome shader: %v", err))
	}

	p := biomes.program
	biomes.uProjView = gl.GetUniformLocation(p, gl.Str("projView\x00"))
	biomes.uCamXZ = gl.GetUniformLocation(p, gl.Str("camXZ\x00"))
	biomes.uSpacing = gl.GetUniformLocation(p, gl.Str("spacing\x00"))
	biomes.uGridN = gl.GetUniformLocation(p, gl.Str("gridN\x00"))
	biomes.uRange = gl.GetUniformLocation(p, gl.Str("range\x00"))
	biomes.uTime = gl.GetUniformLocation(p, gl.Str("time\x00"))
	biomes.uIsRock = gl.GetUniformLocation(p, gl.Str("isRock\x00"))
	biomes.uBladeHeight = gl.GetUniformLocation(p, gl.Str("bladeHeight\x00"))
	biomes.uBladeWidth = gl.GetUniformLocation(p, gl.Str("bladeWidth\x00"))
	biomes.uRockScale = gl.GetUniformLocation(p, gl.Str("rockScale\x00"))
	biomes.uDensity = gl.GetUniformLocation(p, gl.Str("density\x00"))
	biomes.uHeightTex = gl.GetUniformLocation(p, gl.Str("heightTex\x00"))
	biomes.uHeightOrigin = gl.GetUniformLocation(p, gl.Str("heightOrigin\x00"))
	biomes.uHeightSize = gl.GetUniformLocation(p, gl.Str("heightSize\x00"))
	biomes.uPolyPts = gl.GetUniformLocation(p, gl.Str("polyPts[0]\x00"))
	biomes.uNumPolyPts = gl.GetUniformLocation(p, gl.Str("numPolyPts\x00"))
	biomes.uPolyMin = gl.GetUniformLocation(p, gl.Str("polyMin\x00"))
	biomes.uPolyMax = gl.GetUniformLocation(p, gl.Str("polyMax\x00"))
	biomes.uSeed = gl.GetUniformLocation(p, gl.Str("seed\x00"))
	biomes.uTintColor = gl.GetUniformLocation(p, gl.Str("tintColor\x00"))
	biomes.uAmbient = gl.GetUniformLocation(p, gl.Str("ambient\x00"))
	biomes.uSunDir = gl.GetUniformLocation(p, gl.Str("sunDir\x00"))
	biomes.uSunColor = gl.GetUniformLocation(p, gl.Str("sunColor\x00"))

	biomes.aLocal = uint32(gl.GetAttribLocation(p, gl.Str("aLocal\x00")))
	biomes.aNormal = uint32(gl.GetAttribLocation(p, gl.Str("aNormal\x00")))
	biomes.aParam = uint32(gl.GetAttribLocation(p, gl.Str("aParam\x00")))

	blade := buildBladeMesh()
	biomes.bladeVerts = int32(len(blade) / biomeVertexStride)
	biomes.bladeVAO, biomes.bladeVBO = uploadBiomeMesh(blade)

	biomes.rockMeshes = map[int32]rockMesh{}
	biomes.heightCache = map[string]biomeHeightEntry{}

	biomes.initialized = true
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

// uploadBiomeMesh creates a VAO/VBO for a template mesh laid out as
// [aLocal.xyz, aNormal.xyz, aParam] per vertex.
func uploadBiomeMesh(verts []float32) (vao, vbo uint32) {
	gl.GenVertexArrays(1, &vao)
	gl.GenBuffers(1, &vbo)
	gl.BindVertexArray(vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.STATIC_DRAW)

	stride := int32(biomeVertexStride * 4)
	gl.EnableVertexAttribArray(biomes.aLocal)
	gl.VertexAttribPointerWithOffset(biomes.aLocal, 3, gl.FLOAT, false, stride, 0)
	gl.EnableVertexAttribArray(biomes.aNormal)
	gl.VertexAttribPointerWithOffset(biomes.aNormal, 3, gl.FLOAT, false, stride, 3*4)
	gl.EnableVertexAttribArray(biomes.aParam)
	gl.VertexAttribPointerWithOffset(biomes.aParam, 1, gl.FLOAT, false, stride, 6*4)

	gl.BindVertexArray(0)
	return vao, vbo
}

// buildBladeMesh returns a tapered grass blade as a triangle list. Local x is
// normalized to ±1 at the base (the shader scales it by bladeWidth); y runs 0..1
// (scaled to world height by bladeHeight). aParam carries the height fraction so
// the shader can bend/light along the blade. Normal is unused for grass (lit as
// up-facing) but kept for a uniform vertex layout.
func buildBladeMesh() []float32 {
	const segs = 5
	var v []float32
	push := func(x, y float32) {
		v = append(v, x, y, 0, 0, 0, 1, y) // aLocal, aNormal(0,0,1), aParam=y
	}
	for i := 0; i < segs; i++ {
		t0 := float32(i) / segs
		t1 := float32(i+1) / segs
		w0 := 1 - t0 // normalized half-width, tapering to 0 at the tip
		w1 := 1 - t1
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

// buildRockMesh returns a small faceted rock as a triangle list with per-face
// normals for flat shading. It is a bipyramid over an n-gon equator: `sides`
// equatorial vertices give 2*sides triangular faces (sides=4 → octahedron).
// aParam is 0. The equator is slightly perturbed so rocks don't look like
// perfect crystals.
func buildRockMesh(sides int) []float32 {
	if sides < 3 {
		sides = 3
	}
	top := mgl32.Vec3{0, 0.8, 0}
	bot := mgl32.Vec3{0, -0.5, 0}

	// Equatorial ring, with a deterministic per-vertex radius wobble.
	eq := make([]mgl32.Vec3, sides)
	for i := 0; i < sides; i++ {
		a := float64(i) / float64(sides) * 2 * math.Pi
		r := float32(0.8 + 0.2*math.Sin(a*3)) // gentle, deterministic wobble
		eq[i] = mgl32.Vec3{r * float32(math.Cos(a)), 0, r * float32(math.Sin(a))}
	}

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
		addFace(top, eq[i], eq[j]) // upper cap, CCW seen from outside
		addFace(bot, eq[j], eq[i]) // lower cap
	}
	return v
}

// uploadR32F (re)uploads a single-channel float texture with the given filtering.
func uploadR32F(tex uint32, w, h int, data []float32, linear bool) {
	filter := int32(gl.NEAREST)
	if linear {
		filter = gl.LINEAR
	}
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, filter)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, filter)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.R32F, int32(w), int32(h), 0, gl.RED, gl.FLOAT, gl.Ptr(data))
}

// floorHeightUnderLevel returns the structural floor height (accounting for
// terrain-mode slope/displacement, via the same code path player collision
// uses) at world XZ (wx,wz), or 0 over open ground with no sector.
func floorHeightUnderLevel(level *Level, wx, wz float32) float32 {
	if level == nil {
		return 0
	}
	xz := mgl32.Vec2{wx, wz}
	for ci := range level.Chunks {
		c := &level.Chunks[ci]
		if s := FindSector(c, xz); s != nil {
			h, _, _ := InterpolateSurfaceHeight(c, s, xz, true)
			return h
		}
	}
	return 0
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

// ensureBiomeHeight returns the cached height texture for sector s, rebuilding
// it from current structural floor geometry if it's missing or HeightDirty.
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
	data := make([]float32, w*h)
	for ty := 0; ty < h; ty++ {
		v := float32(ty) / float32(h-1)
		wz := min.Y() + v*size.Y()
		for tx := 0; tx < w; tx++ {
			u := float32(tx) / float32(w-1)
			wx := min.X() + u*size.X()
			data[ty*w+tx] = floorHeightUnderLevel(level, wx, wz)
		}
	}
	uploadR32F(e.tex, w, h, data, true)
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

// RenderBiomes draws every chunk's painted biome sectors (grass/rock scatter).
// Uses its own GL program; restores the main program on exit. Call in the
// opaque render pass (depth test on, blend off).
func RenderBiomes(level *Level, projView mgl32.Mat4, camPos mgl32.Vec3, timeSec float32) {
	if !biomes.initialized || level == nil {
		return
	}

	started := false
	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		for si := range ch.BiomeSectors {
			s := &ch.BiomeSectors[si]
			if len(s.Points) < 3 || s.Brush.Density <= 0 {
				continue
			}
			if !started {
				beginBiomePass(level, projView, camPos, timeSec)
				started = true
			}
			renderBiomeSector(level, s)
		}
	}

	if started {
		endBiomePass()
	}
}

// beginBiomePass activates the biome program and uploads the uniforms shared
// by every sector this frame (camera, lighting).
func beginBiomePass(level *Level, projView mgl32.Mat4, camPos mgl32.Vec3, timeSec float32) {
	gl.UseProgram(biomes.program)

	pv := projView
	gl.UniformMatrix4fv(biomes.uProjView, 1, false, &pv[0])
	gl.Uniform2f(biomes.uCamXZ, camPos.X(), camPos.Z())
	gl.Uniform1f(biomes.uTime, timeSec)

	amb := level.Lights.Global.Color.Mul(level.Lights.Global.Brightness)
	gl.Uniform3f(biomes.uAmbient, amb.X(), amb.Y(), amb.Z())
	sunDir := mgl32.Vec3{-0.3, -1, -0.2}
	sunCol := mgl32.Vec3{0.7, 0.7, 0.7}
	if len(level.Lights.Directional) > 0 {
		d := level.Lights.Directional[0]
		if d.Direction.Len() > 1e-6 {
			sunDir = d.Direction.Normalize()
		}
		sunCol = d.Color.Mul(d.Brightness)
	}
	gl.Uniform3f(biomes.uSunDir, sunDir.X(), sunDir.Y(), sunDir.Z())
	gl.Uniform3f(biomes.uSunColor, sunCol.X(), sunCol.Y(), sunCol.Z())

	gl.Uniform1i(biomes.uHeightTex, 0)
}

// renderBiomeSector uploads one sector's polygon/brush/height uniforms and
// issues its instanced draw call.
func renderBiomeSector(level *Level, s *BiomeSector) {
	entry := biomes.ensureBiomeHeight(level, s)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, entry.tex)
	gl.Uniform2f(biomes.uHeightOrigin, entry.origin.X(), entry.origin.Y())
	gl.Uniform2f(biomes.uHeightSize, entry.size.X(), entry.size.Y())

	n := len(s.Points)
	if n > biomeMaxPolyVerts {
		n = biomeMaxPolyVerts
	}
	flat := make([]float32, n*2)
	for i := 0; i < n; i++ {
		flat[i*2] = s.Points[i].X()
		flat[i*2+1] = s.Points[i].Y()
	}
	gl.Uniform2fv(biomes.uPolyPts, int32(n), &flat[0])
	gl.Uniform1i(biomes.uNumPolyPts, int32(n))
	polyMin, polyMax := s.Bounds()
	gl.Uniform2f(biomes.uPolyMin, polyMin.X(), polyMin.Y())
	gl.Uniform2f(biomes.uPolyMax, polyMax.X(), polyMax.Y())

	seed := biomeSeed(s)
	gl.Uniform2f(biomes.uSeed, seed.X(), seed.Y())
	gl.Uniform1f(biomes.uDensity, s.Brush.Density)
	c := s.Brush.Color
	gl.Uniform3f(biomes.uTintColor, c.X(), c.Y(), c.Z())

	if s.Brush.Kind == BiomeRock {
		rng := s.Brush.Range
		if rng <= 0 {
			rng = defaultRockRange
		}
		rockScale := s.Brush.RockScale
		if rockScale <= 0 {
			rockScale = defaultRockScale
		}
		rm := biomes.rockMeshFor(s.Brush.RockSides)
		n, sp := biomeLattice(rng, rockSpacing, maxRockGridN)
		gl.Uniform1i(biomes.uIsRock, 1)
		gl.Uniform1f(biomes.uRockScale, rockScale)
		gl.Uniform1f(biomes.uSpacing, sp)
		gl.Uniform1i(biomes.uGridN, n)
		gl.Uniform1f(biomes.uRange, rng)
		gl.BindVertexArray(rm.vao)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, rm.verts, n*n)
	} else {
		rng := s.Brush.Range
		if rng <= 0 {
			rng = defaultGrassRange
		}
		bh := s.Brush.GrassHeight
		if bh <= 0 {
			bh = defaultGrassHeight
		}
		bw := s.Brush.GrassWidth
		if bw <= 0 {
			bw = defaultGrassWidth
		}
		n, sp := biomeLattice(rng, grassSpacing, maxGrassGridN)
		gl.Uniform1i(biomes.uIsRock, 0)
		gl.Uniform1f(biomes.uBladeHeight, bh)
		gl.Uniform1f(biomes.uBladeWidth, bw)
		gl.Uniform1f(biomes.uSpacing, sp)
		gl.Uniform1i(biomes.uGridN, n)
		gl.Uniform1f(biomes.uRange, rng)
		gl.BindVertexArray(biomes.bladeVAO)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, biomes.bladeVerts, n*n)
	}
}

// endBiomePass restores GL state for the rest of the frame.
func endBiomePass() {
	gl.BindVertexArray(0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.UseProgram(Program)
}
