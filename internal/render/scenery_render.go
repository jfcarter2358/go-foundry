package render

// scenery_render.go — drawing placed scenery.
//
// Entity rendering with everything an entity needs and a prop does not taken
// out: no frame to compose under, no attachment list, no bone palette, no
// visibility flag. What is left is one matrix and a handful of submeshes, which
// is the whole point of the type — see world/scenery.go.
//
// The colour pass and the shadow pass share the walk, for the reason the entity
// passes do: a prop cannot be visible and shadowless (or the reverse) by
// accident.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// SceneryInFrustum reports whether a placement survives frustum culling.
//
// One that has not loaded is never culled — it has no bounds to cull by, and
// treating an absent volume as zero-sized would make a prop whose model failed
// to load invisible everywhere except dead centre of the view, which is exactly
// where an author would not look for it.
func SceneryInFrustum(s *world.Scenery, cull *Frustum) bool {
	if cull == nil {
		return true
	}
	min, max, ok := s.WorldAABB()
	if !ok {
		return true
	}
	return AABBInFrustum(*cull, min, max)
}

// RenderScenery walks the level's scenery for the colour pass.
//
// draw is the frame loop's drawOrQueue, so a prop with a translucent texture
// sorts against the world's own transparent surfaces rather than in a pass of
// its own — glass panes and foliage cards both depend on that.
func RenderScenery(lvl *world.Level, cull *Frustum, draw func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool)) {
	DebugStats.Scenery = 0
	if lvl == nil {
		return
	}
	if sceneryInstancing && len(lvl.Scenery) > 0 {
		DebugStats.Scenery = renderSceneryInstanced(lvl, cull, draw)
		return
	}
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if s.NaniteGPU || !s.HasGeometry() || !SceneryInFrustum(s, cull) {
			continue
		}
		model := s.ModelMatrix()
		for si := range s.Submeshes {
			// Single-sided: a model is a closed solid, and drawing its back
			// faces would double every draw call to show what the front faces
			// already cover.
			draw(&s.Submeshes[si], model, false)
		}
		DebugStats.Scenery++
	}
}

// RenderSceneryShadow draws the level's scenery into whichever shadow map is
// currently bound. prog and skin identify the bound program, which the cascade
// and cube passes resolve to different locations.
//
// It restores the identity model matrix on the way out, for the reason
// RenderEntitiesShadow does: the shadow walker sets that once and leaves it, so
// a matrix left bound here would drag every wall drawn afterwards along with a
// barrel's transform.
func RenderSceneryShadow(lvl *world.Level, prog uint32, skin *gfx.SkinUniforms, modelUniform int32, cull *Frustum) {
	if lvl == nil || len(lvl.Scenery) == 0 {
		return
	}
	if sceneryInstancing {
		// The instanced draw never touches modelUniform, so the identity
		// the walker left there stays put.
		renderSceneryShadowInstanced(lvl, prog, skin, cull)
		return
	}
	drewAny := false
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if s.NaniteGPU || !s.HasGeometry() || !SceneryInFrustum(s, cull) {
			continue
		}
		model := s.ModelMatrix()
		gl.UniformMatrix4fv(modelUniform, 1, false, &model[0])
		drewAny = true
		for si := range s.Submeshes {
			drawGeometryShadow(&s.Submeshes[si], prog, skin)
		}
	}
	if drewAny {
		identity := mgl32.Ident4()
		gl.UniformMatrix4fv(modelUniform, 1, false, &identity[0])
	}
}
