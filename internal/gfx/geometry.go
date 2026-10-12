package gfx

// geometry.go — Geometry, the core GPU vertex-buffer type shared by walls,
// floors, ceilings, water surfaces, and extrusions. Split out of wall.go, where
// it used to sit alongside the unrelated Wall/Extrusion/TerrainPoint types
// — see RESTRUCTURE_PROPOSAL.md §5 Phase A.

import (
	"fmt"
	"foundry/internal/geom"
	"foundry/internal/material"

	"github.com/go-gl/gl/v4.3-core/gl"
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
	// MaterialName is the .fmat this surface wears, or "" for none — which is
	// what nearly every surface in a level carries. What the material says is
	// overlaid on the fields below rather than replacing them; see
	// material_surface.go.
	MaterialName string
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
	// AutoSmooth builds the normals even with Smooth off, so the player's
	// Smooth Lighting setting (SmoothLighting) can smooth the surface. Set by
	// the world's own surfaces — brush faces, compiled surfaces, models — and
	// left off everything the setting has no business reshading.
	AutoSmooth bool
	// VertexNormals, when set before Load, are the normals — one per *source*
	// vertex, three floats each — and Load de-indexes them instead of welding
	// its own. For a surface that is one piece of a bigger smoothed whole: a
	// brush face, whose corners average with the brush's other faces
	// (world.BrushMesh.Load), which this mesh alone cannot see.
	VertexNormals   [][]float32
	Normals         []float32
	NormalAttribute uint32
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
	// VertexLightSmooth is the same bake taken with the smoothed normals, for
	// when the surface shades smooth (ShadesSmooth). Nil where the two would
	// agree — a flat face nothing smooths into — and then VertexLight serves
	// both.
	VertexLightSmooth [][]float32
	vertexLightSmooth []float32
	Lbo               uint32
	// VertexColors is per-vertex colour, set before Load and parallel to the
	// *source* vertices, four floats each (RGBA in 0..1), which Load
	// de-indexes alongside Vertices: the albedo is multiplied by RGB and A is
	// the cavity/ambient occlusion (fragment.glsl's vertexColors). Only a
	// generated mesh has it (a cooked .fnm drawn the classic way,
	// docs/canyon/PHASE_2.md); everything else leaves it nil, uploads no
	// buffer and draws exactly as before.
	VertexColors [][]float32
	colors       []float32
	Cbo          uint32
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
	// Unlit draws the texture's own colour, ignoring every light and shadow —
	// for things that give off light rather than reflect it, like a beam. It
	// also sends the surface to the blended pass; see IsTransparent.
	Unlit bool
	// UVOffset slides the texture across the surface: added to every texture
	// coordinate as it draws. An entity attachment's scroll hands it over
	// (world.EntityModel.UVScroll); nothing else sets it.
	UVOffset mgl32.Vec2
	// Tint multiplies the albedo. The ZERO value is white, not black — see
	// applyTintUniform, which is what makes "a field nobody sets" mean "no
	// tint". A projected decal's pieces are what set it (world/decal.go); no
	// other geometry in the engine does.
	Tint mgl32.Vec3
	// Blend is how the framebuffer blend will combine this mesh, which the
	// shading stage has to know — see SurfaceBlend. Set by a decal's pieces
	// alongside Tint; the zero value is the ordinary case.
	Blend SurfaceBlend
	Water WaterParams
	Vao   uint32
	Vbo   uint32
	Nbo   uint32 // normal buffer; 0 when the surface is flat-shaded

	// CPUOnly makes Load build the mesh and stop there: no buffers of its own.
	// For geometry some other owner uploads in bulk — a brush face, whose
	// vertices are drawn out of its chunk's batch (render/chunk_batch.go). The
	// CPU half is untouched, so everything that reads Vertices — raycasts,
	// picking, the scatter ground — sees no difference.
	CPUOnly bool

	// Indexed, set before Load, makes Load weld the mesh's corners back into
	// a shared vertex pool and draw it with an index buffer (indexed_mesh.go)
	// — as long as the IndexedModels switch is on. Set by the model loaders
	// (Scenery, entity attachments, the model editor); everything else stays
	// on the de-indexed soup the batches concatenate.
	//
	// While Indices is non-nil, Vertices, Normals and the skin and light
	// records are the POOL, one record per shared vertex, not one per corner:
	// anything that walks Vertices as a triangle list must go through
	// ForEachTriangle or TriangleVertices instead, which read both shapes.
	Indexed bool
	// Indices is the uint32 triangle list into Vertices, or nil for the
	// de-indexed soup. Uploaded to Ebo, which the VAO holds.
	Indices []uint32
	Ebo     uint32

	// ShadowLOD, when set, is a coarser stand-in for this mesh that the
	// shadow passes draw instead of it (ShadowCaster): a cooked Nanite mesh's
	// fallback cut, for a placement drawn the classic way, whose full detail
	// in every cascade and every point-light face was the whole cost of a
	// dense prop's shadow (world.Scenery.loadNanite, docs/nanite/PHASE_4.md).
	// Its own buffers; Delete and Unload release them with this mesh's.
	ShadowLOD *Geometry

	// share, when set, is the set of buffers this geometry draws from and
	// does not own alone: other instances of the same model draw from them
	// too (UploadShared). Delete gives up this geometry's claim and frees the
	// buffers only when it was the last.
	share *MeshShare
}

// ShadowCaster is the mesh the shadow passes draw for g: its ShadowLOD when
// it has one uploaded, else g itself.
func (g *Geometry) ShadowCaster() *Geometry {
	if g.ShadowLOD != nil && g.ShadowLOD.Vao != 0 && len(g.ShadowLOD.Vertices) > 0 {
		return g.ShadowLOD
	}
	return g
}

// SmoothLighting is the player's Smooth Lighting setting: while it is on,
// every surface that carries normals (AutoSmooth) shades from them whether or
// not its author ticked Smooth; while it is off only the ticked ones do. On by
// default. Saved to graphics.yaml (app/graphics_settings.go); set through
// SetSmoothLighting so the save and the renderer's batches hear about it.
var SmoothLighting = true

// GraphicsSettingsChanged is raised by SetSmoothLighting and cleared by the
// frame loop once it has saved graphics.yaml.
var GraphicsSettingsChanged bool

// SetSmoothLighting changes the setting. The static batches pick their baked
// light by it, and notice the change by comparing against what they were
// built with, so nothing else has to be told.
func SetSmoothLighting(on bool) {
	if SmoothLighting == on {
		return
	}
	SmoothLighting = on
	GraphicsSettingsChanged = true
}

// ShadesSmooth is whether the surface shades from interpolated normals: it has
// some, and its author asked for it, its material does, or the Smooth Lighting
// setting does.
//
// The material is consulted HERE rather than only where the uniform is set,
// because this answer picks more than the uniform: BakedLightData chooses
// between a compiled surface's flat and smoothed bakes by it, and a surface
// shading smooth off the flat bake is lit for a shape it no longer has.
func (g *Geometry) ShadesSmooth() bool {
	if len(g.Normals) == 0 {
		return false
	}
	if g.Smooth || SmoothLighting {
		return true
	}
	m, ok := material.ResolveSurface(g.MaterialName)
	return ok && m.HasSmooth
}

// SmoothFlag returns the value of the shader's `smoothShading` uniform for this
// geometry: 1 only when it shades smooth *and* normals were actually generated,
// so a surface whose mesh produced none still falls back to the derivative
// normal rather than shading off an empty attribute.
func (g *Geometry) SmoothFlag() int32 {
	if g.ShadesSmooth() {
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
	st := g.SurfaceState()
	return st.NormalColorFlag()
}

// applyWaterUniforms pushes a surface's water settings to the shader. Every
// draw call sets them, including the disabled case, so a water plane never
// leaks its wave/fresnel state onto the geometry drawn after it. Called from
// SurfaceState.Apply, which is the one place a world-program draw sets its
// per-surface uniforms.
func applyWaterUniforms(w *WaterParams) {
	if !w.Enabled {
		gl.Uniform1i(IsWaterUniform, 0)
		return
	}
	gl.Uniform1i(IsWaterUniform, 1)
	gl.Uniform1f(WaveAmplitudeUniform, w.WaveAmplitude)
	gl.Uniform1f(WaveLengthUniform, w.WaveLength)
	gl.Uniform1f(WaveSpeedUniform, w.WaveSpeed)
	gl.Uniform1f(ReflectivityUniform, w.Reflectivity)
	gl.Uniform3f(ReflectColorUniform,
		w.ReflectColor.X(), w.ReflectColor.Y(), w.ReflectColor.Z())
}

// applyBlendUniforms pushes a surface's second-texture blend state to the
// shader. Every draw call sets it, including the disabled case, so a blended
// surface never leaks its blendTex/blendMask binding onto the geometry drawn
// after it. Mirrors applyWaterUniforms.
func applyBlendUniforms(blendTexture, blendMask string) {
	if blendTexture == "" || blendMask == "" {
		gl.Uniform1i(HasBlendUniform, 0)
		return
	}
	gl.Uniform1i(HasBlendUniform, 1)
	gl.ActiveTexture(gl.TEXTURE0 + BlendTexUnit)
	gl.BindTexture(gl.TEXTURE_2D, Textures[blendTexture])
	gl.Uniform1i(BlendTexUniform, BlendTexUnit)
	gl.ActiveTexture(gl.TEXTURE0 + BlendMaskUnit)
	gl.BindTexture(gl.TEXTURE_2D, Textures[blendMask])
	gl.Uniform1i(BlendMaskUniform, BlendMaskUnit)
	gl.ActiveTexture(gl.TEXTURE0)
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
	// Sized for the usual 5-float record up front: growing it by append was
	// a measurable share of building a large compiled level's surfaces.
	verts := make([]float32, 0, len(indices)*5)
	for _, idx := range indices {
		if idx < 0 || idx >= len(vertices) {
			return fmt.Errorf("Geometry.Load: index %d out of bounds (len=%d)", idx, len(vertices))
		}
		verts = append(verts, vertices[idx]...)
	}

	// The baked light is de-indexed the same way, for the same reason.
	g.vertexLight = deindexLight(g.VertexLight, indices)
	g.colors = deindexColors(g.VertexColors, indices)
	g.vertexLightSmooth = deindexLight(g.VertexLightSmooth, indices)

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
	g.Indices = nil
	g.Texture = texture
	g.Position = position
	if g.Alpha == 0 {
		g.Alpha = 1.0
	}

	// Normals are derived from the indexed mesh, before de-indexing loses which
	// triangles shared a vertex — smooth.go welds by position anyway, but the
	// index list is what defines the triangles in the first place.
	g.Normals = nil
	if len(g.VertexNormals) > 0 {
		g.Normals = deindexVec3(g.VertexNormals, indices)
	} else if g.Smooth || g.AutoSmooth {
		g.Normals = geom.ComputeSmoothNormals(vertices, indices, g.SmoothAngle)
	}

	// A model welds its corners back into a shared pool here, now that every
	// per-corner attribute — normals included, which is what keeps a crease
	// split — is laid out. See indexed_mesh.go for why this is invisible.
	if g.Indexed && IndexedModels && len(g.Vertices) > 0 {
		g.weld()
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
	if !GeometryUploadsEnabled || g.CPUOnly {
		return nil
	}
	g.upload()
	return nil
}

// upload hands a mesh Load has built to the GPU: a VAO and a buffer per
// attribute it has.
func (g *Geometry) upload() {
	// No uniform is written here and no location is looked up by name: a
	// load is not a draw, no program need be bound, and on a translated driver
	// the name lookups per face were most of the cost of building a big chunk.
	// The model location is the program's, cached once (WorldModelUniform);
	// the attribute locations are pinned by layout qualifiers (Attrib*).
	g.ModelUniform = WorldModelUniform()
	g.VertexAttribute = AttribVert
	g.TextureCoordinateAttribute = AttribTexCoord

	gl.GenVertexArrays(1, &g.Vao)
	gl.BindVertexArray(g.Vao)

	gl.GenBuffers(1, &g.Vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, g.Vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(g.Vertices)*4, gl.Ptr(g.Vertices), gl.STATIC_DRAW)
	BindVertexLayout()

	// The index buffer is VAO state: bound here, with the VAO bound, it is
	// what every later DrawElements on this VAO reads.
	if len(g.Indices) > 0 {
		gl.GenBuffers(1, &g.Ebo)
		gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, g.Ebo)
		gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(g.Indices)*4, gl.Ptr(g.Indices), gl.STATIC_DRAW)
	}

	// Smooth surfaces get a second buffer in the same VAO. The previous one was
	// released above, with the rest of the old upload.
	if len(g.Normals) > 0 {
		g.NormalAttribute = AttribNormal
		gl.GenBuffers(1, &g.Nbo)
		gl.BindBuffer(gl.ARRAY_BUFFER, g.Nbo)
		gl.BufferData(gl.ARRAY_BUFFER, len(g.Normals)*4, gl.Ptr(g.Normals), gl.STATIC_DRAW)
		gl.EnableVertexAttribArray(AttribNormal)
		gl.VertexAttribPointerWithOffset(AttribNormal, 3, gl.FLOAT, false, 3*4, 0)
	}

	// Skinning attributes get their own buffers in the same VAO, for the same
	// reason normals do: Vertices stays the 5-float record that picking, the
	// shadow passes and the detail renderer all index directly.
	if len(g.skinIdx) > 0 {
		loadVec4Buffer(&g.Ibo, AttribBoneIdx, g.skinIdx)
		loadVec4Buffer(&g.Wbo, AttribBoneWt, g.skinWt)
	}

	// Baked light, likewise in its own buffer in the same VAO.
	if light := g.BakedLightData(); len(light) > 0 {
		gl.GenBuffers(1, &g.Lbo)
		gl.BindBuffer(gl.ARRAY_BUFFER, g.Lbo)
		gl.BufferData(gl.ARRAY_BUFFER, len(light)*4, gl.Ptr(light), gl.STATIC_DRAW)
		BindLightLayout()
	}

	// Vertex colours, the same again.
	if len(g.colors) > 0 {
		g.Cbo = newBuffer(g.colors)
		BindColorLayout()
	}
}

// ---------- shared meshes ----------
//
// A level full of soldiers is dozens of entities wearing the same three
// models. Each used to build its own copy of the mesh — weld, smooth normals,
// a VAO and four buffers — out of the same document, so twenty marines were
// twenty identical uploads. A model's mesh is a function of the file and of
// the few settings that change what is built (world.modelMeshKey); everything
// else that varies per instance — the model matrix, alpha, the texture name,
// the material, the pose palette, the UV scroll — is a uniform or a field
// read at draw time, never the buffers. So instances of one mesh share them.
//
// Sharing is by REFERENCE COUNT on the buffers, kept here because Delete is
// the one place every owner already gives a geometry up: an instance's Delete
// drops its claim, and the buffers go back to the driver with the last one.
// Nothing else holds a claim, so a mesh no instance uses is freed, not cached
// forever.
//
// Each instance still has a VAO of its own, pointing at the shared buffers.
// A VAO is the one GL object a shared context does not share (uiwin's
// windows), so a shared one would draw in the context that happened to make
// it and nowhere else; buffers are shared between them, and a VAO is a dozen
// calls with no data behind it.
//
// The CPU arrays (Vertices, Indices, Normals, the skin and light records) are
// shared too and are READ-ONLY to every instance, which is what they already
// are for a model: nothing writes into an entity model's mesh in place; an
// edit reloads it.

// MeshShare is one uploaded mesh's buffers, drawn from by several geometries.
// Opaque: the caller keeps a *MeshShare per mesh it can share and passes its
// address to UploadShared, which keeps it current.
type MeshShare struct {
	refs                         int
	vbo, nbo, ebo, ibo, wbo, lbo uint32
	cbo                          uint32
}

// Live is whether s still holds buffers some geometry draws from.
func (s *MeshShare) Live() bool { return s != nil && s.refs > 0 }

// UploadShared puts g — a mesh Load built with CPUOnly set, or a value copy
// of one — on the GPU: drawing from *live's buffers when *live still has
// some, or uploading its own, which become *live. Either way g gets a VAO of
// its own. With uploads disabled it only clears CPUOnly; the CPU arrays are
// shared regardless.
func (g *Geometry) UploadShared(live **MeshShare) {
	g.CPUOnly = false
	g.share = nil
	g.Vao, g.Vbo, g.Nbo, g.Ebo, g.Ibo, g.Wbo, g.Lbo, g.Cbo = 0, 0, 0, 0, 0, 0, 0, 0
	if !GeometryUploadsEnabled || len(g.Vertices) == 0 {
		return
	}
	s := *live
	if s == nil || s.refs <= 0 {
		s = &MeshShare{}
		s.vbo = newBuffer(g.Vertices)
		if len(g.Indices) > 0 {
			s.ebo = newIndexBuffer(g.Indices)
		}
		if len(g.Normals) > 0 {
			s.nbo = newBuffer(g.Normals)
		}
		if len(g.skinIdx) > 0 {
			s.ibo, s.wbo = newBuffer(g.skinIdx), newBuffer(g.skinWt)
		}
		if light := g.BakedLightData(); len(light) > 0 {
			s.lbo = newBuffer(light)
		}
		if len(g.colors) > 0 {
			s.cbo = newBuffer(g.colors)
		}
		*live = s
	}
	s.refs++
	g.share = s
	g.Vbo, g.Nbo, g.Ebo, g.Ibo, g.Wbo, g.Lbo = s.vbo, s.nbo, s.ebo, s.ibo, s.wbo, s.lbo
	g.Cbo = s.cbo

	// The same VAO upload makes, over buffers that already hold the data.
	g.ModelUniform = WorldModelUniform()
	g.VertexAttribute = AttribVert
	g.TextureCoordinateAttribute = AttribTexCoord
	gl.GenVertexArrays(1, &g.Vao)
	gl.BindVertexArray(g.Vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, g.Vbo)
	BindVertexLayout()
	if g.Ebo != 0 {
		gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, g.Ebo)
	}
	if g.Nbo != 0 {
		g.NormalAttribute = AttribNormal
		gl.BindBuffer(gl.ARRAY_BUFFER, g.Nbo)
		gl.EnableVertexAttribArray(AttribNormal)
		gl.VertexAttribPointerWithOffset(AttribNormal, 3, gl.FLOAT, false, 3*4, 0)
	}
	if g.Ibo != 0 {
		bindVec4Buffer(g.Ibo, AttribBoneIdx)
		bindVec4Buffer(g.Wbo, AttribBoneWt)
	}
	if g.Lbo != 0 {
		gl.BindBuffer(gl.ARRAY_BUFFER, g.Lbo)
		BindLightLayout()
	}
	if g.Cbo != 0 {
		gl.BindBuffer(gl.ARRAY_BUFFER, g.Cbo)
		BindColorLayout()
	}
}

// newBuffer uploads data to a new buffer object and returns its name. Through
// the ARRAY_BUFFER target whatever the buffer is for: a buffer has no type,
// and binding an index buffer with no VAO bound is not core-profile GL.
func newBuffer(data []float32) uint32 {
	var b uint32
	gl.GenBuffers(1, &b)
	gl.BindBuffer(gl.ARRAY_BUFFER, b)
	gl.BufferData(gl.ARRAY_BUFFER, len(data)*4, gl.Ptr(data), gl.STATIC_DRAW)
	return b
}

func newIndexBuffer(data []uint32) uint32 {
	var b uint32
	gl.GenBuffers(1, &b)
	gl.BindBuffer(gl.ARRAY_BUFFER, b)
	gl.BufferData(gl.ARRAY_BUFFER, len(data)*4, gl.Ptr(data), gl.STATIC_DRAW)
	return b
}

// bindVec4Buffer is loadVec4Buffer for a buffer that already holds its data.
func bindVec4Buffer(buf uint32, loc uint32) {
	gl.BindBuffer(gl.ARRAY_BUFFER, buf)
	gl.EnableVertexAttribArray(loc)
	gl.VertexAttribPointerWithOffset(loc, 4, gl.FLOAT, false, 4*4, 0)
}

// SharedWith reports how many geometries draw from g's buffers, counting g:
// 0 for one with none, 1 for one that owns its buffers alone.
func (g *Geometry) SharedWith() int {
	if g.share != nil {
		return g.share.refs
	}
	if g.Vbo != 0 {
		return 1
	}
	return 0
}

// releaseShare frees g's own VAO and gives up its claim on the shared
// buffers, freeing those too when it was the last.
func (g *Geometry) releaseShare() {
	s := g.share
	g.share = nil
	if g.Vao != 0 {
		gl.DeleteVertexArrays(1, &g.Vao)
	}
	g.Vao, g.Vbo, g.Nbo, g.Ebo, g.Ibo, g.Wbo, g.Lbo, g.Cbo = 0, 0, 0, 0, 0, 0, 0, 0
	s.refs--
	if s.refs > 0 {
		return
	}
	for _, b := range []*uint32{&s.vbo, &s.nbo, &s.ebo, &s.ibo, &s.wbo, &s.lbo, &s.cbo} {
		if *b != 0 {
			gl.DeleteBuffers(1, b)
			*b = 0
		}
	}
}

// Vertex attribute locations of the world program, pinned by layout qualifiers
// in vertex.glsl (and skin_common.glsl for the bones). Every program that draws
// a Geometry's VAO — the world, shadow and cube-shadow programs — declares the
// same ones, which is what lets one VAO feed all of them.
const (
	AttribVert     = 0
	AttribTexCoord = 1
	AttribNormal   = 2
	AttribBoneIdx  = 3
	AttribBoneWt   = 4
	AttribLight    = 5
	// AttribLightIndirect is the same baked-light buffer read again from its
	// fifth float: the indirect part of the colour (see LightStride).
	AttribLightIndirect = 6
	// AttribColor is the per-vertex colour and cavity (vertex.glsl's
	// vertColor; VertexColorsUniform gates reading it). The last location GL
	// guarantees: 7-10 are the instance matrix, 11-12 the visibility
	// buffer's records, 13-14 the instance sphere and wind.
	AttribColor = 15
)

// BindColorLayout points the colour attribute at four floats a vertex in the
// ARRAY_BUFFER currently bound, in the VAO currently bound.
func BindColorLayout() {
	gl.EnableVertexAttribArray(AttribColor)
	gl.VertexAttribPointerWithOffset(AttribColor, 4, gl.FLOAT, false, 4*4, 0)
}

// BindPackedColorLayout points the colour attribute at one RGBA8 word a
// vertex (normalised): the cooked-mesh pool's layout (render/nanite_scenery.go).
func BindPackedColorLayout() {
	gl.EnableVertexAttribArray(AttribColor)
	gl.VertexAttribPointerWithOffset(AttribColor, 4, gl.UNSIGNED_BYTE, true, 4, 0)
}

// deindexColors lays per-source-vertex RGBA rows out per corner, four floats
// each; a missing or short row is opaque white (no change to the albedo, no
// occlusion). Nil in, nil out.
func deindexColors(rows [][]float32, indices []int) []float32 {
	if len(rows) == 0 {
		return nil
	}
	out := make([]float32, 0, len(indices)*4)
	for _, idx := range indices {
		if idx < len(rows) && len(rows[idx]) == 4 {
			out = append(out, rows[idx]...)
		} else {
			out = append(out, 1, 1, 1, 1)
		}
	}
	return out
}

// BindLightLayout points the two baked-light attributes at the LightStride
// record in the ARRAY_BUFFER currently bound, in the VAO currently bound.
func BindLightLayout() {
	gl.EnableVertexAttribArray(AttribLight)
	gl.VertexAttribPointerWithOffset(AttribLight, 4, gl.FLOAT, false, LightStride*4, 0)
	gl.EnableVertexAttribArray(AttribLightIndirect)
	gl.VertexAttribPointerWithOffset(AttribLightIndirect, 3, gl.FLOAT, false, LightStride*4, 4*4)
}

// BindVertexLayout points attributes 0 and 1 at the 5-float [X,Y,Z,U,V] record
// in the ARRAY_BUFFER currently bound, in the VAO currently bound.
func BindVertexLayout() {
	gl.EnableVertexAttribArray(AttribVert)
	gl.VertexAttribPointerWithOffset(AttribVert, 3, gl.FLOAT, false, 5*4, 0)
	gl.EnableVertexAttribArray(AttribTexCoord)
	gl.VertexAttribPointerWithOffset(AttribTexCoord, 2, gl.FLOAT, false, 5*4, 3*4)
}

// worldModel caches the world program's `model` location. Program is fixed
// after startup; the cache still re-reads when it changes, which the GL tests
// do between programs, and LoadProgram clears it for a relink that reuses an id.
var worldModel struct {
	prog  uint32
	loc   int32
	valid bool
}

// WorldModelUniform is the `model` uniform location in Program.
func WorldModelUniform() int32 {
	if !worldModel.valid || worldModel.prog != Program {
		worldModel.prog, worldModel.valid = Program, true
		worldModel.loc = gl.GetUniformLocation(Program, gl.Str("model\x00"))
	}
	return worldModel.loc
}

// BakedLightData is the de-indexed baked light Load built from VertexLight —
// or from VertexLightSmooth, while the surface shades smooth and has one —
// LightStride floats per vertex of Vertices, or nil. For a batch uploading many
// surfaces' light together; it follows ShadesSmooth, so a batch built before
// the setting changed holds the other answer (render/static_batch.go rebuilds).
func (g *Geometry) BakedLightData() []float32 {
	if len(g.vertexLightSmooth) > 0 && g.ShadesSmooth() {
		return g.vertexLightSmooth
	}
	return g.vertexLight
}

// LightStride is the floats per vertex of baked light: the colour, then the
// baked sun's share (compile.Face.SunShare), which the fragment stage scales
// the sun's light by to take back off what a live shadow covers, then the
// indirect part of the colour (compile.Face.Indirect), which live GI takes
// back off before adding its own (render/gi.go).
const LightStride = 7

// deindexLight lays baked-light rows out per corner, as deindexVec3 does
// normals, LightStride floats each. A shorter row — a colour with no share, or
// a colour and share with no indirect part, as a test builds one — is padded
// with zeros: nothing to take back off.
func deindexLight(rows [][]float32, indices []int) []float32 {
	if len(rows) == 0 {
		return nil
	}
	out := make([]float32, 0, len(indices)*LightStride)
	var pad [LightStride]float32
	for _, idx := range indices {
		if idx < len(rows) && len(rows[idx]) >= 3 && len(rows[idx]) <= LightStride {
			out = append(out, rows[idx]...)
			out = append(out, pad[:LightStride-len(rows[idx])]...)
		} else {
			out = append(out, pad[:]...) // see deindexVec3
		}
	}
	return out
}

// deindexVec3 lays per-source-vertex triples out per corner, in the order Load
// de-indexes Vertices. Nil in, nil out.
func deindexVec3(rows [][]float32, indices []int) []float32 {
	if len(rows) == 0 {
		return nil
	}
	out := make([]float32, 0, len(indices)*3)
	for _, idx := range indices {
		if idx < len(rows) && len(rows[idx]) == 3 {
			out = append(out, rows[idx]...)
		} else {
			// A short or ragged input would tint or bend the surface rather
			// than fail; zero is the one value that is obviously wrong (and
			// a zero normal falls back to the facet in the shader).
			out = append(out, 0, 0, 0)
		}
	}
	return out
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

// loadVec4Buffer uploads one vec4-per-vertex attribute — a skinning input.
func loadVec4Buffer(buf *uint32, loc uint32, data []float32) {
	gl.GenBuffers(1, buf)
	gl.BindBuffer(gl.ARRAY_BUFFER, *buf)
	gl.BufferData(gl.ARRAY_BUFFER, len(data)*4, gl.Ptr(data), gl.STATIC_DRAW)
	gl.EnableVertexAttribArray(loc)
	gl.VertexAttribPointerWithOffset(loc, 4, gl.FLOAT, false, 4*4, 0)
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

// Delete releases the GPU resources (VAO and VBO) held by this geometry — or,
// for one sharing its buffers with other instances (UploadShared), its claim
// on them, which frees them only when it was the last.
func (g *Geometry) Delete() {
	if g.share != nil {
		g.releaseShare()
		if g.ShadowLOD != nil {
			g.ShadowLOD.Delete()
		}
		return
	}
	g.deleteOwned()
	if g.ShadowLOD != nil {
		g.ShadowLOD.Delete()
	}
}

// deleteOwned frees the handles g holds, as their only owner.
func (g *Geometry) deleteOwned() {
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
	if g.Ebo != 0 {
		gl.DeleteBuffers(1, &g.Ebo)
		g.Ebo = 0
	}
	g.deleteSkinBuffers()
	g.deleteBakedLightBuffer()
	if g.Cbo != 0 {
		gl.DeleteBuffers(1, &g.Cbo)
		g.Cbo = 0
	}
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
	g.Indices = nil
	g.Normals = nil
	g.SkinIdx, g.SkinWt = nil, nil
	g.VertexLight, g.vertexLight = nil, nil
	g.VertexLightSmooth, g.vertexLightSmooth = nil, nil
	g.VertexNormals = nil
	g.VertexColors, g.colors = nil, nil
	g.BonePalette = nil
	g.ShadowLOD = nil
}

// IsTransparent returns true if this geometry has a non-opaque alpha value,
// or its texture has any semi-transparent texel (see texture_alpha.go). Both
// route the surface into the sorted, back-to-front blended pass instead of
// the opaque one.
//
// An unlit surface always is. It is a glow, and a glow's texture alpha is
// how it fades off at its edges — which only the blended pass honours, and a
// texture loaded from a PNG is never scanned for alpha (textureHasAlpha is the
// texture editor's to record). An opaque texel blended is still opaque.
func (g *Geometry) IsTransparent() bool {
	st := g.SurfaceState()
	return st.IsTransparent()
}

// UnlitFlag is the value of the shader's `unlit` uniform for this geometry.
func (g *Geometry) UnlitFlag() int32 {
	if g.Unlit {
		return 1
	}
	return 0
}

// Draw renders the geometry using the current OpenGL state.
func (g Geometry) Draw() {
	if len(g.Vertices) == 0 {
		return
	}
	model := mgl32.Ident4()

	st := g.SurfaceState()
	gl.UseProgram(Program)
	gl.UniformMatrix4fv(WorldModelUniform(), 1, false, &model[0])
	gl.Uniform1i(BakedLightUniform, g.BakedLightFlag())
	st.Apply()
	ApplySkinUniforms(&worldSkinUniforms, Program, g.BonePalette)

	gl.BindVertexArray(g.Vao)

	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[st.Texture])

	g.DrawTriangles()
}

// DrawWithTransform renders the geometry using a caller-supplied model matrix.
func (g Geometry) DrawWithTransform(model mgl32.Mat4) {
	if len(g.Vertices) == 0 {
		return
	}
	st := g.SurfaceState()
	gl.UseProgram(Program)
	gl.UniformMatrix4fv(WorldModelUniform(), 1, false, &model[0])
	gl.Uniform1i(BakedLightUniform, g.BakedLightFlag())
	st.Apply()
	ApplySkinUniforms(&worldSkinUniforms, Program, g.BonePalette)
	gl.BindVertexArray(g.Vao)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[st.Texture])
	g.DrawTriangles()
}

// AttribInstanceModel is the first of the four locations (7..10) an instanced
// draw's per-instance model matrix takes — vertex.glsl's instanceModel.
const AttribInstanceModel = 7

// InstancedUniform is the world program's `instanced` location: 1 while a
// draw takes its model matrices from AttribInstanceModel rather than `model`.
var InstancedUniform int32

// AttachInstanceMatrices points vao's instance-matrix attributes at buf: one
// column-major mat4 per instance, advancing once per instance. The VAO keeps
// the buffer's name, so buf may be refilled with BufferData without coming
// back here.
func AttachInstanceMatrices(vao, buf uint32) {
	gl.BindVertexArray(vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, buf)
	for i := uint32(0); i < 4; i++ {
		loc := AttribInstanceModel + i
		gl.EnableVertexAttribArray(loc)
		gl.VertexAttribPointerWithOffset(loc, 4, gl.FLOAT, false, 64, uintptr(i*16))
		gl.VertexAttribDivisor(loc, 1)
	}
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
}

// DrawInstanced draws count copies of the geometry through the world program,
// each with its own model matrix: instances first..first+count of the buffer
// AttachInstanceMatrices gave this geometry's VAO. Every other piece of state
// is DrawWithTransform's, so an instanced prop looks exactly like one drawn
// alone.
func (g Geometry) DrawInstanced(first, count int) {
	if len(g.Vertices) == 0 || count <= 0 {
		return
	}
	st := g.SurfaceState()
	gl.UseProgram(Program)
	gl.Uniform1i(InstancedUniform, 1)
	gl.Uniform1i(BakedLightUniform, g.BakedLightFlag())
	st.Apply()
	ApplySkinUniforms(&worldSkinUniforms, Program, nil)
	gl.BindVertexArray(g.Vao)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[st.Texture])
	g.DrawTrianglesInstanced(first, count)
	gl.Uniform1i(InstancedUniform, 0)
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
	gl.UniformMatrix4fv(WorldModelUniform(), 1, false, &model[0])
	gl.Uniform1f(AlphaUniform, g.Alpha)
	gl.Uniform1i(SmoothUniform, 0)
	gl.Uniform1i(BakedLightUniform, 0)
	// Never normal-coloured, whatever this geometry's texture is: a line has no
	// facet for the fragment stage to take a normal from, so the derivative
	// would be noise. A marker's colour comes from its texture and its alpha.
	gl.Uniform1i(NormalColorUniform, 0)
	gl.Uniform1i(UnlitUniform, g.UnlitFlag())
	// No material: a line list wears none, and these are reset rather than
	// merely left alone so the previous draw's tiling and cutout cannot ride
	// along. See ApplyUVUniforms.
	ResetUVUniforms()
	gl.Uniform1f(AlphaCutoffBiasUniform, 0)
	applyTintUniform(TintBiasUniform, mgl32.Vec3{})
	gl.Uniform1i(SurfaceBlendUniform, int32(SurfaceBlendNormal))
	gl.Uniform1i(VertexColorsUniform, 0)
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
