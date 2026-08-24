package main

// entity_render.go — drawing placed entities.
//
// Everything else in the world is uploaded in world space and drawn with an
// identity model matrix (see Geometry's doc comment). Entities cannot be: they
// move, and re-uploading a mesh every time something walked would defeat the
// point of having it on the GPU. So an entity's submeshes stay in model space
// and each draw carries the entity's matrix — which is also what makes an
// attachment's own offset/rotation/scale free, since it is one more matrix
// composed under the entity's.
//
// The colour pass and both shadow passes share the same walk, so an entity
// cannot be visible and shadowless (or the reverse) by accident. Deformation is
// shared for the same reason: all three read one bone palette per attachment,
// so an animated model and its shadow are the same pose by construction.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// entityDrawable reports whether an attachment should be drawn at all: its
// entity is visible, it is visible, and it has something uploaded.
func entityDrawable(e *world.Entity, m *world.EntityModel) bool {
	return e.Visible && m.Visible && m.HasGeometry()
}

// entityInFrustum reports whether an entity survives frustum culling.
//
// An entity with no authored bounds is never culled. Its bounding volume is
// what culling has to work from, and treating an absent one as zero-sized would
// make an unbounded entity invisible everywhere except dead centre of the view.
func entityInFrustum(e *world.Entity, cull *Frustum) bool {
	if cull == nil {
		return true
	}
	min, max, ok := e.WorldAABB()
	if !ok {
		return true
	}
	return AABBInFrustum(*cull, min, max)
}

// RenderEntities walks the level's entities for the colour pass, handing each
// attachment's submeshes to draw with the matrix that places them.
//
// draw is main.go's drawOrQueue: it takes transparent geometry into the sorted
// back-to-front queue and draws the rest immediately, so an entity with a
// translucent attachment sorts against the level's own transparent surfaces
// rather than in a pass of its own.
func RenderEntities(lvl *world.Level, cull *Frustum, draw func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool)) {
	if lvl == nil {
		return
	}
	for ei := range lvl.Entities {
		e := &lvl.Entities[ei]
		if !e.Visible || !entityInFrustum(e, cull) {
			continue
		}
		world := e.ModelMatrix()
		for mi := range e.Models {
			m := &e.Models[mi]
			if !entityDrawable(e, m) {
				continue
			}
			model := world.Mul4(m.LocalMatrix())
			for si := range m.Submeshes {
				// Single-sided: a model is a closed solid, and drawing its back
				// faces would double every draw call to show what the front
				// faces already cover.
				draw(&m.Submeshes[si], model, false)
			}
		}
	}
}

// RenderEntitiesShadow draws the level's entities into whichever shadow map is
// currently bound. prog and skin identify the bound program — the cascade and
// cube passes resolve the skinning uniforms to different locations, so the
// cache cannot be a single global.
//
// It restores the identity model matrix on the way out. The shadow walker sets
// that once and leaves it (renderSceneShadow), so an entity's matrix left in
// place would drag every wall drawn afterwards along with it.
func RenderEntitiesShadow(lvl *world.Level, prog uint32, skin *gfx.SkinUniforms, modelUniform int32, cull *Frustum) {
	if lvl == nil || len(lvl.Entities) == 0 {
		return
	}
	drewAny := false
	for ei := range lvl.Entities {
		e := &lvl.Entities[ei]
		if !e.Visible || !entityInFrustum(e, cull) {
			continue
		}
		world := e.ModelMatrix()
		for mi := range e.Models {
			m := &e.Models[mi]
			if !entityDrawable(e, m) {
				continue
			}
			model := world.Mul4(m.LocalMatrix())
			gl.UniformMatrix4fv(modelUniform, 1, false, &model[0])
			drewAny = true
			for si := range m.Submeshes {
				drawGeometryShadow(&m.Submeshes[si], prog, skin)
			}
		}
	}
	if drewAny {
		identity := mgl32.Ident4()
		gl.UniformMatrix4fv(modelUniform, 1, false, &identity[0])
	}
}
