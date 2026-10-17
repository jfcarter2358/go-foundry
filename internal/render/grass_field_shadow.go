package render

// grass_field_shadow.go — the grass instancer's shadows (grass_field.go).
//
// Grass casts only near the camera: within gfx.GrassShadowDistance (14 m by
// default, 0 off), and only the kinds whose rule asked to (KindCasts). A
// sward's self-shadowing beyond that is what the blades' own base darkening
// and the ground's texture already paint, and every cast clump is drawn
// again per cascade (or per virtual-shadow-map page rectangle it touches),
// so the range is the cost knob. Each cell within reach casts its kinds'
// coarsest mesh (LOD 2, or the last it has): a shadow is soft enough that nobody
// counts its blades. (The mid mesh cost ~1 ms on the GTX 1070 at 18 m, the
// coarsest about half that.) The instances cast are the ones on screen — the same
// prefix of each run and the same per-instance fade by CAMERA distance
// (grassfield_common.glsl) — so a clump that has thinned out of view does
// not leave its shadow behind.
//
// The fields are the colour pass's reach list from the frame before (the
// shadow passes run ahead of it), which is what the camera can see grass
// in; on the first frame there is none and nothing casts.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// grassShadowReach is how far grass casts this frame (0: not at all).
func grassShadowReach() float32 {
	d := gfx.ClampGrassShadowDistance(gfx.GrassShadowDistance)
	if d <= 0 || !grassFields.initialized || len(grassFields.visible) == 0 {
		return 0
	}
	return d
}

// grassShadowBox is the world box grass casts from this frame — the
// shadow reach round the camera, its height the visible casting fields' —
// or false when it casts nothing. The virtual shadow map tracks it as a
// mover (the grass sways); the box FOLLOWS THE CAMERA, so the tracker also
// hands in last frame's box when this one differs (vsm_track.go), or the
// clumps cast round where the camera was would stay in those pages.
func grassShadowBox() (lo, hi mgl32.Vec3, ok bool) {
	reach := grassShadowReach()
	if reach <= 0 {
		return lo, hi, false
	}
	cam := gfx.Cam.Position
	ylo, yhi := float32(1e30), float32(-1e30)
	any := false
	for _, f := range grassFields.visible {
		// Only fields that can cast at all: a sward of non-casting kinds
		// (the tundra's sedge, the coast's dune grass) draws nothing into
		// the map, and a box for it only redrew pages for nothing.
		if f.field == nil || f.kit == nil || !f.kit.anyCasts() {
			continue
		}
		ylo = min(ylo, f.field.Origin[1])
		yhi = max(yhi, f.field.Origin[1]+f.field.Size[1]+f.top)
		any = true
	}
	if !any {
		return lo, hi, false
	}
	return mgl32.Vec3{cam[0] - reach, ylo, cam[2] - reach}, mgl32.Vec3{cam[0] + reach, yhi, cam[2] + reach}, true
}

// anyCasts reports whether any of the kit's groups casts a shadow.
func (k *grassKitGPU) anyCasts() bool {
	for i := range k.groups {
		if k.groups[i].casts {
			return true
		}
	}
	return false
}

// RenderGrassFieldsDirShadow draws the casting grass near the camera into
// the bound shadow target with light-space matrix lsm, culled by cull.
// Cascades past the second are skipped: the reach is a few tens of metres.
// Restores Shadows.Program on exit.
func RenderGrassFieldsDirShadow(lvl *world.Level, lsm mgl32.Mat4, cascade int, cull *Frustum) {
	r := &grassFields
	reach := grassShadowReach()
	if reach <= 0 || lvl == nil || cascade > 1 {
		return
	}
	cam := gfx.Cam.Position
	fo := grassFalloff()
	r.cmds, r.batches = r.cmds[:0], r.batches[:0]
	for _, f := range r.visible {
		if f.field == nil {
			continue
		}
		r.gather(f, cam, cull, fo, reach, true)
	}
	if len(r.cmds) == 0 {
		return
	}
	p := &r.shadow
	gl.UseProgram(p.prog)
	gl.UniformMatrix4fv(p.U("lightSpaceMatrix"), 1, false, &lsm[0])
	commonGrassUniforms(p, cam, fo)
	r.upload()
	r.drawBatches(p)
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, 0)
	gl.BindVertexArray(0)
	gl.UseProgram(Shadows.Program)
}
