package main

// grass.go — GPU-scattered biome rendering (procedural grass & rock).
//
// Biomes are painted per grid cell in the world builder (WorldGrid.GrassDensity /
// RockDensity). Nothing is placed as a scene object: at render time a small
// template mesh (a grass blade or a rock) is drawn with glDrawArraysInstanced,
// and the vertex shader (biome_vert.glsl) generates every instance procedurally
// from gl_InstanceID — scattering it across a camera-centered lattice, sampling a
// biome mask texture to decide survival/density, and a heightmap texture to drop
// it onto the terrain. Cost is bounded by the camera ring, not the world size.
//
// This mirrors the self-contained GizmoProgram pattern (editor_gizmo.go): its own
// GL program and VAOs, never touching the main game shader's state.

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
	rockWorldScale   = 0.35
)

// Caps on lattice side length so a huge draw range can't explode the instance
// count (gridN*gridN instances).
const (
	maxGrassGridN = 320
	maxRockGridN  = 130
)

// biomeRenderer holds the GL objects and cached grid textures for the biome pass.
type biomeRenderer struct {
	program uint32

	// Template meshes.
	bladeVAO, bladeVBO uint32
	bladeVerts         int32
	rockVAO, rockVBO   uint32
	rockVerts          int32

	// Per-grid textures (regenerated when the grid or its data changes).
	maskTex   uint32 // RG32F cols x rows: r=grass, g=rock coverage
	heightTex uint32 // R32F (cols+1)x(rows+1): terrain heights
	curGrid   *WorldGrid
	dirty     bool
	haveGrass bool
	haveRock  bool
	rockSides int32 // equatorial vertex count baked into the current rock VBO

	// Cached uniform locations.
	uProjView, uCamXZ, uSpacing, uGridN, uRange           int32
	uGridOrigin, uCellSize, uCols, uRows                  int32
	uTime, uIsRock, uChannel, uBladeHeight, uRockScale    int32
	uBladeWidth                                           int32
	uHeightTex, uMaskTex, uAmbient, uSunDir, uSunColor    int32
	aLocal, aNormal, aParam                               uint32

	initialized bool
}

var biomes biomeRenderer

// MarkBiomesDirty flags the biome mask/height textures for regeneration on the
// next frame. Call after any world-builder stroke (biome paint, or height edits
// that move the surface the scatter sits on) or range change.
func MarkBiomesDirty() { biomes.dirty = true }

// sliceHasNonzero reports whether s contains any value != 0. Used to decide
// whether painted biome coverage is worth persisting / drawing.
func sliceHasNonzero(s []float32) bool {
	for _, v := range s {
		if v != 0 {
			return true
		}
	}
	return false
}

// GrassInit compiles the biome shader program and builds the blade/rock template
// meshes. Must be called once with the GL context current (see EditorInit).
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
	biomes.uGridOrigin = gl.GetUniformLocation(p, gl.Str("gridOrigin\x00"))
	biomes.uCellSize = gl.GetUniformLocation(p, gl.Str("cellSize\x00"))
	biomes.uCols = gl.GetUniformLocation(p, gl.Str("cols\x00"))
	biomes.uRows = gl.GetUniformLocation(p, gl.Str("rows\x00"))
	biomes.uTime = gl.GetUniformLocation(p, gl.Str("time\x00"))
	biomes.uIsRock = gl.GetUniformLocation(p, gl.Str("isRock\x00"))
	biomes.uChannel = gl.GetUniformLocation(p, gl.Str("channel\x00"))
	biomes.uBladeHeight = gl.GetUniformLocation(p, gl.Str("bladeHeight\x00"))
	biomes.uBladeWidth = gl.GetUniformLocation(p, gl.Str("bladeWidth\x00"))
	biomes.uRockScale = gl.GetUniformLocation(p, gl.Str("rockScale\x00"))
	biomes.uHeightTex = gl.GetUniformLocation(p, gl.Str("heightTex\x00"))
	biomes.uMaskTex = gl.GetUniformLocation(p, gl.Str("maskTex\x00"))
	biomes.uAmbient = gl.GetUniformLocation(p, gl.Str("ambient\x00"))
	biomes.uSunDir = gl.GetUniformLocation(p, gl.Str("sunDir\x00"))
	biomes.uSunColor = gl.GetUniformLocation(p, gl.Str("sunColor\x00"))

	biomes.aLocal = uint32(gl.GetAttribLocation(p, gl.Str("aLocal\x00")))
	biomes.aNormal = uint32(gl.GetAttribLocation(p, gl.Str("aNormal\x00")))
	biomes.aParam = uint32(gl.GetAttribLocation(p, gl.Str("aParam\x00")))

	blade := buildBladeMesh()
	biomes.bladeVerts = int32(len(blade) / biomeVertexStride)
	biomes.bladeVAO, biomes.bladeVBO = uploadBiomeMesh(blade)

	biomes.rockSides = defaultRockSides
	rock := buildRockMesh(int(biomes.rockSides))
	biomes.rockVerts = int32(len(rock) / biomeVertexStride)
	biomes.rockVAO, biomes.rockVBO = uploadBiomeMesh(rock)

	gl.GenTextures(1, &biomes.maskTex)
	gl.GenTextures(1, &biomes.heightTex)

	biomes.initialized = true
	biomes.dirty = true
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
		addFace(top, eq[i], eq[j])   // upper cap, CCW seen from outside
		addFace(bot, eq[j], eq[i])   // lower cap
	}
	return v
}

// rebuildRockMesh regenerates the rock template VBO for a new equatorial vertex
// count. Called when WorldGrid.RockSides changes.
func rebuildRockMesh(sides int32) {
	if sides < 3 {
		sides = 3
	}
	if biomes.rockVAO != 0 {
		gl.DeleteVertexArrays(1, &biomes.rockVAO)
	}
	if biomes.rockVBO != 0 {
		gl.DeleteBuffers(1, &biomes.rockVBO)
	}
	rock := buildRockMesh(int(sides))
	biomes.rockVerts = int32(len(rock) / biomeVertexStride)
	biomes.rockVAO, biomes.rockVBO = uploadBiomeMesh(rock)
	biomes.rockSides = sides
}

// activeBiomeGrid returns the first world grid in the level (biomes assume a
// single world chunk), or nil if there is none.
func activeBiomeGrid(level *Level) *WorldGrid {
	if level == nil {
		return nil
	}
	for i := range level.Chunks {
		if level.Chunks[i].WorldGrid != nil {
			return level.Chunks[i].WorldGrid
		}
	}
	return nil
}

// updateBiomeTextures (re)uploads the mask and height textures from the grid.
func updateBiomeTextures(g *WorldGrid) {
	cw, ch := g.Cols, g.Rows

	// Mask: RG32F, one texel per cell (r=grass, g=rock).
	mask := make([]float32, cw*ch*2)
	biomes.haveGrass = false
	biomes.haveRock = false
	for i := 0; i < cw*ch; i++ {
		var gr, rk float32
		if i < len(g.GrassDensity) {
			gr = g.GrassDensity[i]
		}
		if i < len(g.RockDensity) {
			rk = g.RockDensity[i]
		}
		if gr > 0 {
			biomes.haveGrass = true
		}
		if rk > 0 {
			biomes.haveRock = true
		}
		mask[i*2] = gr
		mask[i*2+1] = rk
	}
	gl.BindTexture(gl.TEXTURE_2D, biomes.maskTex)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RG32F, int32(cw), int32(ch), 0, gl.RG, gl.FLOAT, gl.Ptr(mask))

	// Height: R32F, one texel per grid vertex. g.Heights already matches
	// row-major (cols+1)x(rows+1).
	hw, hh := cw+1, ch+1
	heights := g.Heights
	if len(heights) < hw*hh {
		// Defensive: pad if the slice is short (shouldn't happen).
		heights = append(append([]float32(nil), heights...), make([]float32, hw*hh-len(heights))...)
	}
	gl.BindTexture(gl.TEXTURE_2D, biomes.heightTex)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.R32F, int32(hw), int32(hh), 0, gl.RED, gl.FLOAT, gl.Ptr(heights))

	gl.BindTexture(gl.TEXTURE_2D, 0)
}

// biomeLattice returns the lattice side length (gridN) and spacing needed to
// cover the full 2*range square centered on the camera. It prefers baseSpacing,
// but once that would exceed the instance cap it GROWS the spacing so the
// (capped) lattice still spans the whole range — otherwise a large range would
// leave the lattice fixed-size and off-center, shrinking/sliding the coverage.
// The tradeoff at extreme ranges is sparser scatter, never a smaller area.
func biomeLattice(rng, baseSpacing float32, cap int32) (gridN int32, spacing float32) {
	spacing = baseSpacing
	if spacing <= 0 {
		spacing = 0.5
	}
	// +2 slots of margin so the snap-to-spacing in the shader still covers both
	// edges of the range.
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

// RenderBiomes draws the painted grass/rock scatter for the level's world grid,
// if any. Uses its own GL program; restores the main program on exit. Call in
// the opaque render pass (depth test on, blend off).
func RenderBiomes(level *Level, projView mgl32.Mat4, camPos mgl32.Vec3, timeSec float32) {
	if !biomes.initialized {
		return
	}
	g := activeBiomeGrid(level)
	if g == nil {
		return
	}
	if g != biomes.curGrid || biomes.dirty {
		updateBiomeTextures(g)
		biomes.curGrid = g
		biomes.dirty = false
	}
	if !biomes.haveGrass && !biomes.haveRock {
		return
	}

	gl.UseProgram(biomes.program)

	// Shared uniforms.
	pv := projView
	gl.UniformMatrix4fv(biomes.uProjView, 1, false, &pv[0])
	gl.Uniform2f(biomes.uCamXZ, camPos.X(), camPos.Z())
	gl.Uniform2f(biomes.uGridOrigin, g.Origin.X(), g.Origin.Y())
	gl.Uniform1f(biomes.uCellSize, g.CellSize)
	gl.Uniform1i(biomes.uCols, int32(g.Cols))
	gl.Uniform1i(biomes.uRows, int32(g.Rows))
	gl.Uniform1f(biomes.uTime, timeSec)

	// Appearance from the grid, falling back to defaults for old/zeroed values.
	bladeHeight := g.GrassHeight
	if bladeHeight <= 0 {
		bladeHeight = defaultGrassHeight
	}
	bladeWidth := g.GrassWidth
	if bladeWidth <= 0 {
		bladeWidth = defaultGrassWidth
	}
	gl.Uniform1f(biomes.uBladeHeight, bladeHeight)
	gl.Uniform1f(biomes.uBladeWidth, bladeWidth)
	gl.Uniform1f(biomes.uRockScale, rockWorldScale)

	// Rebuild the rock template if the configured face count changed.
	rockSides := g.RockSides
	if rockSides < 3 {
		rockSides = defaultRockSides
	}
	if rockSides != biomes.rockSides {
		rebuildRockMesh(rockSides)
	}

	// Lighting from the level (global ambient + first directional light).
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

	// Bind textures: unit 0 = height, unit 1 = mask.
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, biomes.heightTex)
	gl.Uniform1i(biomes.uHeightTex, 0)
	gl.ActiveTexture(gl.TEXTURE1)
	gl.BindTexture(gl.TEXTURE_2D, biomes.maskTex)
	gl.Uniform1i(biomes.uMaskTex, 1)

	grassRange := g.GrassRange
	if grassRange <= 0 {
		grassRange = defaultGrassRange
	}
	rockRange := g.RockRange
	if rockRange <= 0 {
		rockRange = defaultRockRange
	}

	// Grass pass.
	if biomes.haveGrass {
		n, sp := biomeLattice(grassRange, grassSpacing, maxGrassGridN)
		gl.Uniform1i(biomes.uChannel, 0)
		gl.Uniform1i(biomes.uIsRock, 0)
		gl.Uniform1f(biomes.uSpacing, sp)
		gl.Uniform1i(biomes.uGridN, n)
		gl.Uniform1f(biomes.uRange, grassRange)
		gl.BindVertexArray(biomes.bladeVAO)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, biomes.bladeVerts, n*n)
	}

	// Rock pass.
	if biomes.haveRock {
		n, sp := biomeLattice(rockRange, rockSpacing, maxRockGridN)
		gl.Uniform1i(biomes.uChannel, 1)
		gl.Uniform1i(biomes.uIsRock, 1)
		gl.Uniform1f(biomes.uSpacing, sp)
		gl.Uniform1i(biomes.uGridN, n)
		gl.Uniform1f(biomes.uRange, rockRange)
		gl.BindVertexArray(biomes.rockVAO)
		gl.DrawArraysInstanced(gl.TRIANGLES, 0, biomes.rockVerts, n*n)
	}

	// Restore state for the rest of the frame.
	gl.BindVertexArray(0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.UseProgram(Program)
}
