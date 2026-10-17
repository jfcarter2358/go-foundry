package gfx

// batch.go — many meshes of one material in one vertex buffer.
//
// A Geometry is one surface with buffers of its own, which is the right shape
// for a model's submesh and the wrong one for a level: a big area is thousands
// of brush faces, and one VAO and VBO apiece is thousands of GL objects to
// create when it loads and thousands of draw calls every pass. A Batch is the
// other shape — the vertices of every surface sharing a material, concatenated,
// with the caller keeping track of which range is whose so it can still cull
// and move them one at a time and draw what is left with glMultiDrawArrays.
//
// The vertex layout is Geometry's exactly ([X,Y,Z,U,V] at locations 0 and 1,
// normals at 2), so every program that draws a Geometry draws a Batch.

import (
	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Material is the draw state a batch shares across all of its surfaces: what a
// Geometry sets per draw, set once per batch.
type Material struct {
	Texture string
	// MaterialName is the .fmat the batch's surfaces wear. Part of the material
	// identity, so faces wearing different materials land in different
	// batches — which they must, since the material sets uniforms the batch
	// applies once for all of them.
	MaterialName string
	BlendTexture string
	BlendMask    string
	Alpha        float32
	// Normals is whether the batch carries per-vertex normals; Smooth is
	// whether its surfaces' author asked for them to shade smooth regardless
	// of the Smooth Lighting setting. Geometry.ShadesSmooth, split in two.
	Normals bool
	Smooth  bool
	// Baked is whether it carries baked light — a compiled level's static
	// surfaces, whose static lights are already on their vertices.
	Baked bool
	// VertexColors is whether the vertices it draws carry colours (attribute
	// AttribColor) for the shader to multiply in: a cooked mesh made with
	// them (docs/canyon/PHASE_2.md). Part of the identity like the rest, so
	// a coloured mesh and a plain one never share a draw.
	VertexColors bool
}

// IsTransparent is Geometry.IsTransparent for a material. Decided at draw time
// rather than when the batch is built, because whether a texture has alpha can
// change while the game runs — the texture editor repaints one.
func (m *Material) IsTransparent() bool {
	st := m.SurfaceState()
	return st.IsTransparent()
}

// NormalColorFlag is Geometry.NormalColorFlag for a material.
func (m *Material) NormalColorFlag() int32 {
	st := m.SurfaceState()
	return st.NormalColorFlag()
}

// SmoothFlag is Geometry.SmoothFlag for a material.
func (m *Material) SmoothFlag() int32 {
	st := m.SurfaceState()
	return st.SmoothFlag()
}

// Batch is one material's vertices on the GPU.
type Batch struct {
	Material
	Vao, Vbo, Nbo, Lbo uint32
	// Verts is how many vertices the buffer holds.
	Verts int32
	// Ebo, when the batch has one, is an index list over the vertex buffer —
	// the GPU-driven cull's cluster order (render/nanite_cull.go). VAO state,
	// like Geometry's. The ranges DrawRanges takes still address the vertex
	// buffer directly and ignore it.
	Ebo     uint32
	Indices int32
}

// Upload replaces the batch's contents. verts is the 5-float record, normals
// three floats per vertex and light LightStride (each nil unless the material
// is smooth, or baked). The buffers are kept and re-filled rather than re-created when the
// batch already has them.
func (b *Batch) Upload(verts, normals, light []float32) {
	b.Verts = int32(len(verts) / 5)
	if !GeometryUploadsEnabled {
		return
	}
	if b.Vao == 0 {
		gl.GenVertexArrays(1, &b.Vao)
	}
	gl.BindVertexArray(b.Vao)
	if b.Vbo == 0 {
		gl.GenBuffers(1, &b.Vbo)
	}
	gl.BindBuffer(gl.ARRAY_BUFFER, b.Vbo)
	if len(verts) > 0 {
		gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, gl.Ptr(verts), gl.STATIC_DRAW)
	} else {
		gl.BufferData(gl.ARRAY_BUFFER, 0, nil, gl.STATIC_DRAW)
	}
	BindVertexLayout()

	if len(normals) > 0 {
		if b.Nbo == 0 {
			gl.GenBuffers(1, &b.Nbo)
		}
		gl.BindBuffer(gl.ARRAY_BUFFER, b.Nbo)
		gl.BufferData(gl.ARRAY_BUFFER, len(normals)*4, gl.Ptr(normals), gl.STATIC_DRAW)
		gl.EnableVertexAttribArray(AttribNormal)
		gl.VertexAttribPointerWithOffset(AttribNormal, 3, gl.FLOAT, false, 3*4, 0)
	} else if b.Nbo != 0 {
		gl.DisableVertexAttribArray(AttribNormal)
		gl.DeleteBuffers(1, &b.Nbo)
		b.Nbo = 0
	}
	if len(light) > 0 {
		if b.Lbo == 0 {
			gl.GenBuffers(1, &b.Lbo)
		}
		gl.BindBuffer(gl.ARRAY_BUFFER, b.Lbo)
		gl.BufferData(gl.ARRAY_BUFFER, len(light)*4, gl.Ptr(light), gl.STATIC_DRAW)
		BindLightLayout()
	} else if b.Lbo != 0 {
		gl.DisableVertexAttribArray(AttribLight)
		gl.DisableVertexAttribArray(AttribLightIndirect)
		gl.DeleteBuffers(1, &b.Lbo)
		b.Lbo = 0
	}
	gl.BindVertexArray(0)
}

// Delete releases the batch's GL objects.
func (b *Batch) Delete() {
	if b.Vao != 0 {
		gl.DeleteVertexArrays(1, &b.Vao)
		b.Vao = 0
	}
	if b.Vbo != 0 {
		gl.DeleteBuffers(1, &b.Vbo)
		b.Vbo = 0
	}
	if b.Nbo != 0 {
		gl.DeleteBuffers(1, &b.Nbo)
		b.Nbo = 0
	}
	if b.Lbo != 0 {
		gl.DeleteBuffers(1, &b.Lbo)
		b.Lbo = 0
	}
	if b.Ebo != 0 {
		gl.DeleteBuffers(1, &b.Ebo)
		b.Ebo = 0
	}
	b.Verts, b.Indices = 0, 0
}

// UploadIndices gives the batch an index list over its vertex buffer (nil
// drops it). Upload must have made the VAO first.
func (b *Batch) UploadIndices(indices []uint32) {
	b.Indices = int32(len(indices))
	if !GeometryUploadsEnabled || b.Vao == 0 {
		return
	}
	if len(indices) == 0 {
		if b.Ebo != 0 {
			gl.BindVertexArray(b.Vao)
			gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, 0)
			gl.BindVertexArray(0)
			gl.DeleteBuffers(1, &b.Ebo)
			b.Ebo = 0
		}
		return
	}
	gl.BindVertexArray(b.Vao)
	if b.Ebo == 0 {
		gl.GenBuffers(1, &b.Ebo)
	}
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, b.Ebo)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(indices)*4, gl.Ptr(indices), gl.STATIC_DRAW)
	gl.BindVertexArray(0)
}

// Draw renders the given ranges through the world program under model,
// setting the material's uniforms once for all of them — everything
// Geometry.DrawWithTransform sets per surface.
func (b *Batch) Draw(model mgl32.Mat4, firsts, counts []int32) {
	if len(firsts) == 0 || b.Vao == 0 {
		return
	}
	b.Bind(model)
	b.DrawRanges(firsts, counts)
}

// Bind readies the world program to draw the batch under model — every
// uniform Draw sets, the texture and the VAO — and draws nothing. For a caller
// issuing its own draws against the batch: the GPU-driven cull's indirect
// draws over Ebo.
func (b *Batch) Bind(model mgl32.Mat4) {
	baked := int32(0)
	if b.Baked && b.Lbo != 0 {
		baked = 1
	}
	b.Material.BindWorld(model, baked, false)
	gl.BindVertexArray(b.Vao)
}

// BindWorld sets the world program up for a draw in this material: the model
// matrix (ignored by an instanced draw, which reads its own per instance),
// the baked-light flag, every uniform the surface state sets, no skin, and the
// texture. The VAO is the caller's. A chunk or static batch is lit and still
// unless its material says otherwise — unlit, water and scroll all used to be
// a model attachment's business alone, and a .fmat is how a wall gets at them.
func (m *Material) BindWorld(model mgl32.Mat4, baked int32, instanced bool) {
	st := m.SurfaceState()
	gl.UseProgram(Program)
	gl.UniformMatrix4fv(WorldModelUniform(), 1, false, &model[0])
	gl.Uniform1i(BakedLightUniform, baked)
	// Only ever switched ON here: every instanced draw puts it back to 0
	// after itself (DrawInstanced, the Nanite scenery), so a batch's draw
	// leaves it alone exactly as it always has.
	if instanced {
		gl.Uniform1i(InstancedUniform, 1)
	}
	st.Apply()
	ApplySkinUniforms(&worldSkinUniforms, Program, nil)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[st.Texture])
}

// DrawRanges issues the draw for the given ranges against whatever program
// and uniforms the caller has already set up, binding only the batch's VAO.
// For the shadow passes and any other program that shares the vertex layout.
func (b *Batch) DrawRanges(firsts, counts []int32) {
	if len(firsts) == 0 || b.Vao == 0 {
		return
	}
	gl.BindVertexArray(b.Vao)
	if len(firsts) == 1 {
		gl.DrawArrays(gl.TRIANGLES, firsts[0], counts[0])
		return
	}
	gl.MultiDrawArrays(gl.TRIANGLES, &firsts[0], &counts[0], int32(len(firsts)))
}
