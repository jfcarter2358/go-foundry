package gfx

// geometry.go — Geometry, the core GPU vertex-buffer type shared by walls,
// floors, ceilings, water surfaces, and extrusions. Split out of wall.go, where
// it used to sit alongside the unrelated Wall/Extrusion/TerrainPoint types
// — see RESTRUCTURE_PROPOSAL.md §5 Phase A.

import (
	"fmt"
	"foundry/internal/geom"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Geometry holds GPU-side vertex data and associated handles.
// Vertex format: 5 floats per vertex [X, Y, Z, U, V].
//
// Vertices are stored in whatever space their owner authored them in — for
// everything hanging off a Chunk that means CHUNK-LOCAL, carried into the
// world by the model matrix the draw call supplies (Chunk.ModelMatrix). This
// type does not know or care which; it never had a "geometry is world space,
// model is always identity" guarantee to give, and since chunk transforms
// became live it would be wrong if it did.
//
// Smooth surfaces additionally carry Normals — one per vertex, in a buffer of
// their own rather than interleaved, so Vertices stays the 5-float record that
// picking, the shadow passes and the detail renderer all index directly.
type Geometry struct {
	Texture string
	// BlendTexture/BlendMask, when both set, mix a second texture over
	// Texture at draw time — see applyBlendUniforms. Empty (the default) means
	// no blending, at no extra GPU cost.
	BlendTexture               string
	BlendMask                  string
	TextureUniform             int32
	TextureCoordinateAttribute uint32
	Vertices                   []float32
	VertexAttribute            uint32
	Smooth                     bool    // shade from interpolated vertex normals instead of flat facets
	SmoothAngle                float32 // crease threshold in degrees; 0 = DefaultSmoothAngle
	Normals                    []float32
	NormalAttribute            uint32
	// Skinning inputs, set before Load on a submesh built from a rigged model:
	// four bone indices and four weights per *source* vertex, which Load
	// de-indexes alongside Vertices. Nil on everything else, which is what keeps
	// the extra buffers off every wall in the level.
	SkinIdx, SkinWt [][]float32
	skinIdx, skinWt []float32
	Ibo, Wbo        uint32
	// VertexLight is baked per-vertex lighting, set before Load and parallel to
	// the *source* vertices, which Load de-indexes alongside Vertices. Only a
	// compiled level's static surfaces have it; on everything else it is nil
	// and the fragment stage lights the surface the ordinary way.
	VertexLight [][]float32
	vertexLight []float32
	Lbo         uint32
	// BonePalette is the per-frame pose, one delta per bone. The entity owns the
	// backing array and updates it in place, so this slice header can be handed
	// to every submesh once at load and never touched again — which is what lets
	// the sorted transparent queue draw an animated entity without extra
	// bookkeeping. Empty means "not skinned".
	BonePalette  []mgl32.Mat4
	Model        mgl32.Mat4
	ModelUniform int32
	Normal       mgl32.Vec3
	// Position is the point this mesh's vertices were built relative to, kept
	// as a record of that. Nothing reads it: the caller's model matrix already
	// carries the same translation, so anything wanting the mesh's location in
	// space takes it from there (see the transparent sort key in app.go, which
	// used to multiply this in a second time and sorted at twice the offset).
	// Do not reintroduce it into a transform.
	Position mgl32.Vec3
	Yaw      float32
	Alpha    float32 // 0.0 = fully transparent, 1.0 = fully opaque
	Water    WaterParams
	Vao      uint32
	Vbo      uint32
	Nbo      uint32 // normal buffer; 0 when the surface is flat-shaded
}

// SmoothFlag returns the value of the shader's `smoothShading` uniform for this
// geometry: 1 only when smoothing is on *and* normals were actually generated,
// so a surface whose mesh produced none still falls back to the derivative
// normal rather than shading off an empty attribute.
func (g *Geometry) SmoothFlag() int32 {
	if g.Smooth && len(g.Normals) > 0 {
		return 1
	}
	return 0
}

// NormalColorFlag is the value of the shader's `normalColor` uniform for this
// geometry: 1 when it names no texture at all.
//
// An unpainted surface is the ordinary state of a brush that has just been
// blocked out, not an error, so it gets a look of its own rather than a
// placeholder image: the fragment stage paints it with its own normal, which
// makes every plane of a solid a different colour and reads as shape. The
// alternative — binding whatever Textures[""] resolves to, which is texture
// object 0 — samples black through the whole lighting model.
func (g *Geometry) NormalColorFlag() int32 {
	if g.Texture == "" {
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

// applyBlendUniforms pushes this geometry's second-texture blend state to the
// shader. Every draw call sets it, including the disabled case, so a blended
// surface never leaks its blendTex/blendMask binding onto the geometry drawn
// after it. Mirrors applyWaterUniforms.
func (g *Geometry) applyBlendUniforms() {
	if g.BlendTexture == "" || g.BlendMask == "" {
		gl.Uniform1i(HasBlendUniform, 0)
		return
	}
	gl.Uniform1i(HasBlendUniform, 1)
	gl.ActiveTexture(gl.TEXTURE0 + BlendTexUnit)
	gl.BindTexture(gl.TEXTURE_2D, Textures[g.BlendTexture])
	gl.Uniform1i(BlendTexUniform, BlendTexUnit)
	gl.ActiveTexture(gl.TEXTURE0 + BlendMaskUnit)
	gl.BindTexture(gl.TEXTURE_2D, Textures[g.BlendMask])
	gl.Uniform1i(BlendMaskUniform, BlendMaskUnit)
	gl.ActiveTexture(gl.TEXTURE0)
}

// applyNormalUniforms binds this geometry's normal map, if its texture has
// one. Which textures do is a property of the art, not of the surface (see
// texture_normal.go), so unlike the blend pair there is nothing per-geometry
// to configure: painting a bump channel into brick.ftex gives every brick
// wall in the game relief at once.
func (g *Geometry) applyNormalUniforms() {
	BindNormalMap(g.Texture, HasNormalMapUniform, NormalMapUniform)
}

// GobEncode and GobDecode make a Geometry serialise as NOTHING.
//
// A compiled level is stored with gob, and the structures it stores — a chunk,
// a brush — carry their render geometry as a field. None of it can be written:
// Vao/Vbo/Nbo are handles that belong to one GL context and one run, the
// uniform locations belong to one compiled program, and Vertices is a
// de-indexed copy of a mesh that Load rebuilds from the source polygon in a
// fraction of the space. Encoding them would produce a file several times
// larger whose contents are, at best, ignored and, at worst, bound as if they
// were live handles.
//
// Doing it here rather than by defining a parallel "storable" copy of every
// type that contains one: a parallel type is a second thing to keep in step,
// and the last engine that had one drifted.
func (g Geometry) GobEncode() ([]byte, error) { return nil, nil }

// GobDecode leaves the geometry zero — the caller reloads it from its source.
func (g *Geometry) GobDecode([]byte) error { return nil }

// GeometryUploadsEnabled gates the GPU half of Geometry.Load. It is true in the
// running engine and always will be; tests turn it off so that editor operations
// which rebuild geometry as a side effect can be exercised without a window.
//
// The split is real rather than a test affordance: the CPU half computes the
// mesh, its normals and its bounds, all of which non-rendering code legitimately
// reads. Only the upload needs a context.
var GeometryUploadsEnabled = true

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

	// The baked light is de-indexed the same way, for the same reason.
	g.vertexLight = nil
	if len(g.VertexLight) > 0 {
		g.vertexLight = make([]float32, 0, len(indices)*3)
		for _, idx := range indices {
			if idx < len(g.VertexLight) && len(g.VertexLight[idx]) == 3 {
				g.vertexLight = append(g.vertexLight, g.VertexLight[idx]...)
			} else {
				// A short or ragged input would tint the surface rather than
				// fail; black is the one value that is obviously wrong.
				g.vertexLight = append(g.vertexLight, 0, 0, 0)
			}
		}
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
		g.Normals = geom.ComputeSmoothNormals(vertices, indices, g.SmoothAngle)
	}

	// Reloading in place is the normal case — an editor drag, a script
	// animating a floor — and the handles below are generated fresh every
	// time, so the previous upload has to go back to the driver here or every
	// rebuild strands a VAO and a VBO. A script animating one sector's floor
	// rebuilds every gap in its chunk on every frame (RebuildGaps), which turns
	// that into thousands of orphaned objects in a few seconds. Done before the
	// empty-mesh return below, so a surface that rebuilds to nothing releases
	// what it had rather than keeping it alive with no way left to reach it.
	if GeometryUploadsEnabled {
		g.Delete()
	}

	if len(g.Vertices) == 0 {
		return nil
	}

	g.Model = mgl32.Ident4()

	// Everything above builds the mesh on the CPU; everything below hands it to
	// the GPU. Only the second half needs a live GL context, which is what lets
	// the editor's structural operations — splitting an edge, merging sectors —
	// be exercised headlessly. See withoutGeometryUploads in the tests.
	if !GeometryUploadsEnabled {
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

	// Smooth surfaces get a second buffer in the same VAO. The previous one was
	// released above, with the rest of the old upload.
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
	if len(g.skinIdx) > 0 {
		g.loadSkinBuffer(&g.Ibo, "boneIdx", g.skinIdx)
		g.loadSkinBuffer(&g.Wbo, "boneWt", g.skinWt)
	}

	// Baked light, likewise in its own buffer in the same VAO.
	if len(g.vertexLight) > 0 {
		if loc := gl.GetAttribLocation(Program, gl.Str("vertLight\x00")); loc >= 0 {
			gl.GenBuffers(1, &g.Lbo)
			gl.BindBuffer(gl.ARRAY_BUFFER, g.Lbo)
			gl.BufferData(gl.ARRAY_BUFFER, len(g.vertexLight)*4, gl.Ptr(g.vertexLight), gl.STATIC_DRAW)
			gl.EnableVertexAttribArray(uint32(loc))
			gl.VertexAttribPointerWithOffset(uint32(loc), 3, gl.FLOAT, false, 3*4, 0)
		}
	}

	return nil
}

// BakedLightFlag is the uniform value for this surface: 1 when its lighting is
// already on its vertices, 0 when the fragment stage has to work it out.
//
// Read from the BUFFER rather than from VertexLight, because a surface built
// with baked light in a headless process — a test, the compiler — has the data
// and no buffer, and telling the shader to read an attribute that was never
// enabled would light it black.
func (g *Geometry) BakedLightFlag() int32 {
	if g.Lbo != 0 {
		return 1
	}
	return 0
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

func (g *Geometry) deleteBakedLightBuffer() {
	if g.Lbo != 0 {
		gl.DeleteBuffers(1, &g.Lbo)
		g.Lbo = 0
	}
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
	g.deleteBakedLightBuffer()
}

// Unload releases the GPU handles AND drops the CPU-side vertex data, leaving
// a zero-length mesh that Draw skips.
//
// Delete alone is not enough for anything that is going away rather than being
// re-uploaded in place: Vertices is a full copy of the mesh in ordinary Go
// memory — the shadow, picking and detail passes all index it — so a chunk
// unloaded with Delete would free its buffers and keep its megabytes. See
// Chunk.ReleaseGeometry, which is the caller this exists for.
func (g *Geometry) Unload() {
	g.Delete()
	g.Vertices = nil
	g.Normals = nil
	g.SkinIdx, g.SkinWt = nil, nil
	g.VertexLight, g.vertexLight = nil, nil
	g.BonePalette = nil
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
	gl.Uniform1i(SmoothUniform, g.SmoothFlag())
	gl.Uniform1i(BakedLightUniform, g.BakedLightFlag())
	gl.Uniform1i(NormalColorUniform, g.NormalColorFlag())
	g.applyWaterUniforms()
	g.applyBlendUniforms()
	g.applyNormalUniforms()
	ApplySkinUniforms(&worldSkinUniforms, Program, g.BonePalette)

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
	gl.Uniform1i(SmoothUniform, g.SmoothFlag())
	gl.Uniform1i(BakedLightUniform, g.BakedLightFlag())
	gl.Uniform1i(NormalColorUniform, g.NormalColorFlag())
	g.applyWaterUniforms()
	g.applyBlendUniforms()
	g.applyNormalUniforms()
	ApplySkinUniforms(&worldSkinUniforms, Program, g.BonePalette)
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
	gl.Uniform1i(BakedLightUniform, 0)
	// Never normal-coloured, whatever this geometry's texture is: a line has no
	// facet for the fragment stage to take a normal from, so the derivative
	// would be noise. A marker's colour comes from its texture and its alpha.
	gl.Uniform1i(NormalColorUniform, 0)
	gl.Uniform1i(IsWaterUniform, 0)
	gl.Uniform1i(HasNormalMapUniform, 0)
	ApplySkinUniforms(&worldSkinUniforms, Program, nil)
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
