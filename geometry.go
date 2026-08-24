package main

// geometry.go — Geometry, the core GPU vertex-buffer type shared by walls,
// floors, ceilings, water surfaces, and extrusions. Split out of wall.go, where
// it used to sit alongside the unrelated Wall/Extrusion/TerrainPoint types
// — see RESTRUCTURE_PROPOSAL.md §5 Phase A.

import (
	"fmt"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Geometry holds GPU-side vertex data and associated handles.
// Vertex format: 5 floats per vertex [X, Y, Z, U, V].
// All geometry is stored in world space; model matrix is always identity.
//
// Smooth surfaces additionally carry Normals — one per vertex, in a buffer of
// their own rather than interleaved, so Vertices stays the 5-float record that
// picking, the shadow passes and the detail renderer all index directly.
type Geometry struct {
	Texture                    string
	TextureUniform             int32
	TextureCoordinateAttribute uint32
	Vertices                   []float32
	VertexAttribute            uint32
	Smooth                     bool    // shade from interpolated vertex normals instead of flat facets
	SmoothAngle                float32 // crease threshold in degrees; 0 = defaultSmoothAngle
	Normals                    []float32
	NormalAttribute            uint32
	// Skinning inputs, set before Load on a submesh built from a rigged model:
	// four bone indices and four weights per *source* vertex, which Load
	// de-indexes alongside Vertices. Nil on everything else, which is what keeps
	// the extra buffers off every wall in the level.
	SkinIdx, SkinWt [][]float32
	skinIdx, skinWt []float32
	Ibo, Wbo        uint32
	// BonePalette is the per-frame pose, one delta per bone. The entity owns the
	// backing array and updates it in place, so this slice header can be handed
	// to every submesh once at load and never touched again — which is what lets
	// the sorted transparent queue draw an animated entity without extra
	// bookkeeping. Empty means "not skinned".
	BonePalette  []mgl32.Mat4
	Model        mgl32.Mat4
	ModelUniform int32
	Normal       mgl32.Vec3
	Position     mgl32.Vec3
	Yaw          float32
	Alpha        float32 // 0.0 = fully transparent, 1.0 = fully opaque
	Water        WaterParams
	Vao          uint32
	Vbo          uint32
	Nbo          uint32 // normal buffer; 0 when the surface is flat-shaded
}

// smoothFlag returns the value of the shader's `smoothShading` uniform for this
// geometry: 1 only when smoothing is on *and* normals were actually generated,
// so a surface whose mesh produced none still falls back to the derivative
// normal rather than shading off an empty attribute.
func (g *Geometry) smoothFlag() int32 {
	if g.Smooth && len(g.Normals) > 0 {
		return 1
	}
	return 0
}

// applyWaterUniforms pushes this geometry's water settings to the shader.
// Every draw call sets them, including the disabled case, so a water plane
// never leaks its wave/fresnel state onto the geometry drawn after it.
func (g *Geometry) applyWaterUniforms() {
	if !g.Water.Enabled {
		gl.Uniform1i(IsWaterUniform, 0)
		return
	}
	gl.Uniform1i(IsWaterUniform, 1)
	gl.Uniform1f(WaveAmplitudeUniform, g.Water.WaveAmplitude)
	gl.Uniform1f(WaveLengthUniform, g.Water.WaveLength)
	gl.Uniform1f(WaveSpeedUniform, g.Water.WaveSpeed)
	gl.Uniform1f(ReflectivityUniform, g.Water.Reflectivity)
	gl.Uniform3f(ReflectColorUniform,
		g.Water.ReflectColor.X(), g.Water.ReflectColor.Y(), g.Water.ReflectColor.Z())
}

// geometryUploadsEnabled gates the GPU half of Geometry.Load. It is true in the
// running engine and always will be; tests turn it off so that editor operations
// which rebuild geometry as a side effect can be exercised without a window.
//
// The split is real rather than a test affordance: the CPU half computes the
// mesh, its normals and its bounds, all of which non-rendering code legitimately
// reads. Only the upload needs a context.
var geometryUploadsEnabled = true

// Load uploads vertices (indexed by indices) to the GPU.
// vertices: slice of [X,Y,Z,U,V] vertex definitions.
// indices: triangle list referencing vertices.
func (g *Geometry) Load(vertices [][]float32, indices []int, texture string, position mgl32.Vec3) error {
	verts := []float32{}
	for _, idx := range indices {
		if idx < 0 || idx >= len(vertices) {
			return fmt.Errorf("Geometry.Load: index %d out of bounds (len=%d)", idx, len(vertices))
		}
		verts = append(verts, vertices[idx]...)
	}

	// The skin attributes are de-indexed in the same order, so record k of the
	// vertex buffer and record k of these describe the same corner.
	g.skinIdx, g.skinWt = nil, nil
	if len(g.SkinIdx) > 0 && len(g.SkinWt) > 0 {
		g.skinIdx = make([]float32, 0, len(indices)*4)
		g.skinWt = make([]float32, 0, len(indices)*4)
		for _, idx := range indices {
			if idx < len(g.SkinIdx) && idx < len(g.SkinWt) {
				g.skinIdx = append(g.skinIdx, g.SkinIdx[idx]...)
				g.skinWt = append(g.skinWt, g.SkinWt[idx]...)
			} else {
				// Unreachable given the bounds check above, but a short buffer
				// would silently bind the wrong bones rather than fail.
				g.skinIdx = append(g.skinIdx, 0, 0, 0, 0)
				g.skinWt = append(g.skinWt, 0, 0, 0, 0)
			}
		}
	}

	// Phase 0 fix: assign verts before using it in BufferData
	g.Vertices = verts
	g.Texture = texture
	g.Position = position
	if g.Alpha == 0 {
		g.Alpha = 1.0
	}

	// Normals are derived from the indexed mesh, before de-indexing loses which
	// triangles shared a vertex — smooth.go welds by position anyway, but the
	// index list is what defines the triangles in the first place.
	g.Normals = nil
	if g.Smooth {
		g.Normals = computeSmoothNormals(vertices, indices, g.SmoothAngle)
	}

	if len(g.Vertices) == 0 {
		return nil
	}

	g.Model = mgl32.Ident4()

	// Everything above builds the mesh on the CPU; everything below hands it to
	// the GPU. Only the second half needs a live GL context, which is what lets
	// the editor's structural operations — splitting an edge, merging sectors —
	// be exercised headlessly. See withoutGeometryUploads in the tests.
	if !geometryUploadsEnabled {
		return nil
	}

	g.ModelUniform = gl.GetUniformLocation(Program, gl.Str("model\x00"))
	gl.UniformMatrix4fv(g.ModelUniform, 1, false, &g.Model[0])

	g.TextureUniform = gl.GetUniformLocation(Program, gl.Str("tex\x00"))
	gl.Uniform1i(g.TextureUniform, 0)

	gl.GenVertexArrays(1, &g.Vao)
	gl.BindVertexArray(g.Vao)

	gl.GenBuffers(1, &g.Vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, g.Vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(g.Vertices)*4, gl.Ptr(g.Vertices), gl.STATIC_DRAW)

	g.VertexAttribute = uint32(gl.GetAttribLocation(Program, gl.Str("vert\x00")))
	gl.EnableVertexAttribArray(g.VertexAttribute)
	gl.VertexAttribPointerWithOffset(g.VertexAttribute, 3, gl.FLOAT, false, 5*4, 0)

	g.TextureCoordinateAttribute = uint32(gl.GetAttribLocation(Program, gl.Str("vertTexCoord\x00")))
	gl.EnableVertexAttribArray(g.TextureCoordinateAttribute)
	gl.VertexAttribPointerWithOffset(g.TextureCoordinateAttribute, 2, gl.FLOAT, false, 5*4, 3*4)

	// Smooth surfaces get a second buffer in the same VAO. Reloading a surface
	// runs Load again, so release the previous one first.
	if g.Nbo != 0 {
		gl.DeleteBuffers(1, &g.Nbo)
		g.Nbo = 0
	}
	if len(g.Normals) > 0 {
		if loc := gl.GetAttribLocation(Program, gl.Str("vertNormal\x00")); loc >= 0 {
			g.NormalAttribute = uint32(loc)
			gl.GenBuffers(1, &g.Nbo)
			gl.BindBuffer(gl.ARRAY_BUFFER, g.Nbo)
			gl.BufferData(gl.ARRAY_BUFFER, len(g.Normals)*4, gl.Ptr(g.Normals), gl.STATIC_DRAW)
			gl.EnableVertexAttribArray(g.NormalAttribute)
			gl.VertexAttribPointerWithOffset(g.NormalAttribute, 3, gl.FLOAT, false, 3*4, 0)
		} else {
			g.Normals = nil // shader has no normal input; stay flat-shaded
		}
	}

	// Skinning attributes get their own buffers in the same VAO, for the same
	// reason normals do: Vertices stays the 5-float record that picking, the
	// shadow passes and the detail renderer all index directly.
	g.deleteSkinBuffers()
	if len(g.skinIdx) > 0 {
		g.loadSkinBuffer(&g.Ibo, "boneIdx", g.skinIdx)
		g.loadSkinBuffer(&g.Wbo, "boneWt", g.skinWt)
	}

	return nil
}

// loadSkinBuffer uploads one vec4-per-vertex skinning attribute.
func (g *Geometry) loadSkinBuffer(buf *uint32, attr string, data []float32) {
	loc := gl.GetAttribLocation(Program, gl.Str(attr+"\x00"))
	if loc < 0 {
		return // shader was built without the skinning path
	}
	gl.GenBuffers(1, buf)
	gl.BindBuffer(gl.ARRAY_BUFFER, *buf)
	gl.BufferData(gl.ARRAY_BUFFER, len(data)*4, gl.Ptr(data), gl.STATIC_DRAW)
	gl.EnableVertexAttribArray(uint32(loc))
	gl.VertexAttribPointerWithOffset(uint32(loc), 4, gl.FLOAT, false, 4*4, 0)
}

func (g *Geometry) deleteSkinBuffers() {
	if g.Ibo != 0 {
		gl.DeleteBuffers(1, &g.Ibo)
		g.Ibo = 0
	}
	if g.Wbo != 0 {
		gl.DeleteBuffers(1, &g.Wbo)
		g.Wbo = 0
	}
}

// IsSkinned reports whether this submesh carries bone attributes.
func (g *Geometry) IsSkinned() bool { return g.Ibo != 0 && g.Wbo != 0 }

// Delete releases the GPU resources (VAO and VBO) held by this geometry.
func (g *Geometry) Delete() {
	if g.Vao != 0 {
		gl.DeleteVertexArrays(1, &g.Vao)
		g.Vao = 0
	}
	if g.Vbo != 0 {
		gl.DeleteBuffers(1, &g.Vbo)
		g.Vbo = 0
	}
	if g.Nbo != 0 {
		gl.DeleteBuffers(1, &g.Nbo)
		g.Nbo = 0
	}
	g.deleteSkinBuffers()
}

// IsTransparent returns true if this geometry has a non-opaque alpha value,
// or its texture has any semi-transparent texel (see texture_alpha.go). Both
// route the surface into the sorted, back-to-front blended pass instead of
// the opaque one.
func (g *Geometry) IsTransparent() bool {
	return (g.Alpha > 0 && g.Alpha < 1.0) || textureHasAlpha[g.Texture]
}

// Draw renders the geometry using the current OpenGL state.
func (g Geometry) Draw() {
	if len(g.Vertices) == 0 {
		return
	}
	model := mgl32.Ident4()

	gl.UseProgram(Program)
	gl.UniformMatrix4fv(g.ModelUniform, 1, false, &model[0])
	gl.Uniform1f(AlphaUniform, g.Alpha)
	gl.Uniform1i(SmoothUniform, g.smoothFlag())
	g.applyWaterUniforms()
	applySkinUniforms(&worldSkinUniforms, Program, g.BonePalette)

	gl.BindVertexArray(g.Vao)

	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture])

	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}

// DrawWithTransform renders the geometry using a caller-supplied model matrix.
func (g Geometry) DrawWithTransform(model mgl32.Mat4) {
	if len(g.Vertices) == 0 {
		return
	}
	gl.UseProgram(Program)
	gl.UniformMatrix4fv(g.ModelUniform, 1, false, &model[0])
	gl.Uniform1f(AlphaUniform, g.Alpha)
	gl.Uniform1i(SmoothUniform, g.smoothFlag())
	g.applyWaterUniforms()
	applySkinUniforms(&worldSkinUniforms, Program, g.BonePalette)
	gl.BindVertexArray(g.Vao)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture])
	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}

// DrawLinesWithTransform renders the geometry as a line list rather than as
// triangles, using a caller-supplied model matrix.
//
// Editor markers that describe a region rather than a surface — a volume's cage
// (volume_render.go) — want lines: a translucent solid would sort into the
// transparent pass and fight with the water already there, and two overlapping
// regions drawn as solids are unreadable where two drawn as wireframes are not.
func (g Geometry) DrawLinesWithTransform(model mgl32.Mat4) {
	if len(g.Vertices) == 0 {
		return
	}
	gl.UseProgram(Program)
	gl.UniformMatrix4fv(g.ModelUniform, 1, false, &model[0])
	gl.Uniform1f(AlphaUniform, g.Alpha)
	gl.Uniform1i(SmoothUniform, 0)
	gl.Uniform1i(IsWaterUniform, 0)
	applySkinUniforms(&worldSkinUniforms, Program, nil)
	gl.BindVertexArray(g.Vao)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture])
	gl.DrawArrays(gl.LINES, 0, int32(len(g.Vertices)/5))
}

// ---------- water ----------
//
// WaterParams is a field of Geometry, so it lives beside it rather than with
// the WaterSurface primitive that also carries one. Reading the YAML form is
// the level schema's job and stays in water_surface.go.

// WaterParams controls the animated-water shader path. Enabled gates every
// water effect in the shaders.
//
// Waves are evaluated from world XZ, not mesh UV, so a wave keeps the same
// physical size whether it is on a 4-unit pond or a 400-unit lake — widening a
// painted water sector makes it cover more area rather than stretching the
// swell.
type WaterParams struct {
	Enabled       bool
	WaveAmplitude float32    // peak vertical displacement, world units
	WaveLength    float32    // world units per wave cycle
	WaveSpeed     float32    // wave cycles per second
	Reflectivity  float32    // 0 = plain texture, 1 = fully mirror-like at grazing angles
	ReflectColor  mgl32.Vec3 // environment/sky colour mixed in by the fresnel term
}

// DefaultWaterParams returns the starting look for a newly painted water sector.
func DefaultWaterParams() WaterParams {
	return WaterParams{
		Enabled:       true,
		WaveAmplitude: 0.06,
		WaveLength:    3.0,
		WaveSpeed:     0.35,
		Reflectivity:  0.45,
		ReflectColor:  mgl32.Vec3{0.55, 0.72, 0.92},
	}
}
