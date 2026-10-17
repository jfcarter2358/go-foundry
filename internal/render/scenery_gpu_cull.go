package render

// scenery_gpu_cull.go — the classic scenery's instanced draw, culled on the
// GPU.
//
// scenery_instanced.go groups the placements by what they draw and draws each
// group with one instanced call; each pass culled every member on the CPU and
// uploaded the survivors' matrices. A level stamping thousands of props paid
// that in the camera pass and again in every cascade and cube face: a box
// test and a 64-byte copy a placement a pass, and a buffer re-specified each
// time.
//
// Here the placements' matrices and boxes go up ONCE, and again only in a
// frame where one has moved (the instancer's own per-placement cache already
// knows which). Each pass then runs scenery_cull_comp.glsl over them — the
// same box test, a stable compaction into the instance buffer the groups'
// vertex arrays already read, and each group's instance count and first
// instance written into an indirect command — and draws every group with
// glDrawElementsIndirect (or Arrays): the same instances in the same order the
// CPU's fill left, so the same picture.
//
// The camera pass still tests each placement's box on the CPU, for
// DebugStats.Scenery's count of what is in view — a test a placement, where
// the cost was a test, a copy and an upload a placement a pass. The
// transparent groups' colour pass is still the CPU's, one placement at a time
// into the sorted queue, as it must be.
//
// FOUNDRY_SCENERY_GPU_CULL=0 keeps the CPU path; so does a driver without the
// programs.

import (
	"fmt"
	"log"
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// SceneryGPUCull switches the GPU path; see the file comment.
var SceneryGPUCull = gfx.EnvFlag("FOUNDRY_SCENERY_GPU_CULL", true)

// sceneryGPUInstance is one placement as scenery_cull_comp.glsl reads it.
type sceneryGPUInstance struct {
	Model  mgl32.Mat4
	Lo, Hi mgl32.Vec4
}

// sceneryCullPrograms are the cull's four stages.
type sceneryCullPrograms struct {
	tried, ok                       bool
	flags, blocks, scatter, command progUniforms
}

var sceneryCull sceneryCullPrograms

func (p *sceneryCullPrograms) ready() bool {
	if p.tried {
		return p.ok
	}
	p.tried = true
	if err := p.build(); err != nil {
		log.Printf("scenery: GPU cull unavailable: %v — culled on the CPU", err)
		return false
	}
	p.ok = true
	return true
}

func (p *sceneryCullPrograms) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	src := mustReadShader(biomeShaderPath("scenery_cull_comp.glsl"))
	for _, s := range []struct {
		define string
		out    *progUniforms
	}{{"SC_FLAGS", &p.flags}, {"SC_BLOCKS", &p.blocks}, {"SC_SCATTER", &p.scatter}, {"SC_COMMANDS", &p.command}} {
		cs, cerr := gfx.CompileShader("#version 430\n#define "+s.define+"\n"+src, gl.COMPUTE_SHADER)
		if cerr != nil {
			return fmt.Errorf("%s: %v", s.define, cerr)
		}
		prog, lerr := linkStages(cs)
		if lerr != nil {
			return fmt.Errorf("%s: %v", s.define, lerr)
		}
		*s.out = progUniforms{prog: prog, loc: map[string]int32{}}
	}
	return nil
}

// sceneryGPU is the instancer's GPU half: the placements, their groups, the
// cull's scratch and the two passes' commands.
type sceneryGPU struct {
	// built is the instancer signature the tables were made for.
	built     bool
	builtSig  uint64
	count     int
	instances uint32
	members   uint32
	groups    uint32
	local     uint32
	blocks    uint32
	// colour and shadow are the indirect commands, five words a group: the
	// representative's draw for the colour pass, the shadow caster's for the
	// shadow passes.
	colour, shadow uint32
	data           []sceneryGPUInstance
	// uploaded is the frame the instances were last brought up to date in.
	uploaded uint64
	// counted is, per group, whether its survivors count toward
	// DebugStats.Scenery (its submesh is its placements' first drawn one).
	counted []bool
}

// gpuActive is whether the instancer culls on the GPU.
func (in *sceneryInstancer) gpuActive() bool {
	return SceneryGPUCull && gfx.GeometryUploadsEnabled && sceneryCull.ready()
}

// syncGPU brings the GPU tables up to date with the grouping, and the
// instances with the placements: once a frame inside one (every call outside),
// uploading only when a placement has moved since.
func (in *sceneryInstancer) syncGPU() {
	gp := &in.gpu
	fresh := !gp.built || gp.builtSig != in.sig
	if fresh {
		in.buildGPU()
	} else if drawFrameOpen && gp.uploaded == drawFrameNo {
		return
	}
	gp.uploaded = drawFrameNo
	moved := fresh
	for gi := range in.groups {
		for _, m := range in.groups[gi].members {
			if in.refresh(int(m)) {
				moved = true
			}
		}
	}
	if !moved {
		return
	}
	// The lot: the members of a group are scattered through the level's
	// list, and a frame that moves one usually moves several.
	k := 0
	for gi := range in.groups {
		for _, m := range in.groups[gi].members {
			gp.data[k] = in.gpuInstance(int(m))
			k++
		}
	}
	if len(gp.data) > 0 {
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, gp.instances)
		gl.BufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(gp.data)*int(unsafe.Sizeof(sceneryGPUInstance{})), unsafe.Pointer(&gp.data[0]))
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	}
}

// gpuInstance is placement i as the cull reads it.
func (in *sceneryInstancer) gpuInstance(i int) sceneryGPUInstance {
	b := &in.boxes[i]
	ok := float32(0)
	if b.ok {
		ok = 1
	}
	// The draw-distance cuts ride the two w's (classicDrawCuts).
	camCut, shadowCut := classicDrawCuts(&in.lvl.Scenery[i])
	ok = -(1 + shadowCut)
	if b.ok {
		ok = 1 + shadowCut
	}
	return sceneryGPUInstance{Model: in.mats[i], Lo: b.lo.Vec4(ok), Hi: b.hi.Vec4(camCut)}
}

// classicDrawCuts are a classic placement's per-instance cuts
// (world/scenery_draw.go): the distance it stops being drawn at, and the one
// it stops casting at, its hash already in them; 0 for none. The record
// carries them in hi.w and, offset by one, in lo.w (whose sign is "has a
// box"), which scenery_cull_comp.glsl tests from drawEye.
func classicDrawCuts(s *world.Scenery) (camCut, shadowCut float32) {
	if !DrawDistances {
		return 0, 0
	}
	k := 1 - world.DrawFadeBand + world.DrawFadeBand*s.DrawHash()
	if w := s.MeshWindow(); w.To > 0 {
		camCut = w.To * k
	}
	if w := s.MeshShadowWindow(); w.To > 0 {
		shadowCut = w.To * k
	}
	return camCut, shadowCut
}

// buildGPU makes the tables for the current grouping: the members' groups,
// the groups' runs and kinds, the commands' fixed words, and room for every
// placement in the instance buffer.
func (in *sceneryInstancer) buildGPU() {
	gp := &in.gpu
	gp.built, gp.builtSig = true, in.sig
	n := 0
	for gi := range in.groups {
		n += len(in.groups[gi].members)
	}
	gp.count = n
	gp.data = make([]sceneryGPUInstance, n)
	memberGroup := make([]uint32, 0, n)
	info := make([]uint32, 0, 4*len(in.groups))
	colour := make([]uint32, 5*len(in.groups))
	shadow := make([]uint32, 5*len(in.groups))
	gp.counted = make([]bool, len(in.groups))
	base := 0
	for gi := range in.groups {
		g := &in.groups[gi]
		rep := in.lvl.Scenery[g.rep]
		geo := &rep.Submeshes[g.sub]
		caster := geo.ShadowCaster()
		indexed := boolToInt32(geo.Indices != nil && geo.Ebo != 0)
		info = append(info, uint32(base), uint32(len(g.members)), uint32(boolToInt32(g.key.transparent)), uint32(indexed))
		for range g.members {
			memberGroup = append(memberGroup, uint32(gi))
		}
		base += len(g.members)
		colour[5*gi] = sceneryDrawCount(geo)
		shadow[5*gi] = sceneryDrawCount(caster)
		if boolToInt32(caster.Indices != nil && caster.Ebo != 0) != indexed {
			// The caster's draw must take the same command form as the
			// group's; one that does not is drawn whole from its own
			// representative instead (never, for a stand-in made from the
			// mesh it stands for).
			shadow[5*gi] = colour[5*gi]
		}
		gp.counted[gi] = g.sub == firstDrawnSub(&in.lvl.Scenery[g.rep])
	}
	ensure := func(buf *uint32, data unsafe.Pointer, size int) {
		if *buf == 0 {
			gl.GenBuffers(1, buf)
		}
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, *buf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, max(size, 16), data, gl.DYNAMIC_DRAW)
	}
	ptr := func(s []uint32) unsafe.Pointer {
		if len(s) == 0 {
			return nil
		}
		return unsafe.Pointer(&s[0])
	}
	ensure(&gp.instances, nil, n*int(unsafe.Sizeof(sceneryGPUInstance{})))
	ensure(&gp.members, ptr(memberGroup), len(memberGroup)*4)
	ensure(&gp.groups, ptr(info), len(info)*4)
	ensure(&gp.local, nil, n*4)
	ensure(&gp.blocks, nil, ((n+255)/256+1)*4)
	ensure(&gp.colour, ptr(colour), len(colour)*4)
	ensure(&gp.shadow, ptr(shadow), len(shadow)*4)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	// The instance buffer the groups' vertex arrays read: room for all.
	gl.BindBuffer(gl.ARRAY_BUFFER, in.buf)
	gl.BufferData(gl.ARRAY_BUFFER, max(n*64, 64), nil, gl.DYNAMIC_DRAW)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
}

// sceneryDrawCount is the first word of g's command: its index count, or its
// vertex count.
func sceneryDrawCount(g *gfx.Geometry) uint32 {
	if g.Indices != nil && g.Ebo != 0 {
		return uint32(len(g.Indices))
	}
	return uint32(len(g.Vertices) / 5)
}

// gpuCull culls every placement against cull (nil: none) into the instance
// buffer and the pass's commands: the colour pass's, without the transparent
// groups, or the shadow passes'. Returns the command buffer.
func (in *sceneryInstancer) gpuCull(cull *Frustum, shadows bool) uint32 {
	gp := &in.gpu
	cmds := gp.colour
	if shadows {
		cmds = gp.shadow
	}
	if gp.count == 0 {
		return cmds
	}
	blocks := (gp.count + 255) / 256
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, gp.instances)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, gp.members)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 2, gp.groups)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 3, gp.local)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 4, gp.blocks)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 5, in.buf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 6, cmds)
	common := func(p *progUniforms) {
		gl.UseProgram(p.prog)
		gl.Uniform1ui(p.U("count"), uint32(gp.count))
		gl.Uniform1ui(p.U("blockCount"), uint32(blocks))
		gl.Uniform1ui(p.U("groupCount"), uint32(len(in.groups)))
	}
	p := &sceneryCull.flags
	common(p)
	gl.Uniform1i(p.U("skipTransparent"), boolToInt32(!shadows))
	var planes [6]mgl32.Vec4
	if cull != nil {
		planes = cull.Planes
	} else {
		for i := range planes {
			planes[i] = mgl32.Vec4{0, 0, 0, 1}
		}
	}
	gl.Uniform4fv(p.U("planes"), 6, &planes[0][0])
	gl.Uniform3f(p.U("drawEye"), gfx.Cam.Position[0], gfx.Cam.Position[1], gfx.Cam.Position[2])
	gl.Uniform1i(p.U("shadowPass"), boolToInt32(shadows))
	gl.DispatchCompute(uint32(blocks), 1, 1)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	common(&sceneryCull.blocks)
	gl.DispatchCompute(1, 1, 1)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	common(&sceneryCull.scatter)
	gl.DispatchCompute(uint32(blocks), 1, 1)
	common(&sceneryCull.command)
	gl.DispatchCompute(uint32((len(in.groups)+63)/64), 1, 1)
	gl.MemoryBarrier(gl.COMMAND_BARRIER_BIT | gl.VERTEX_ATTRIB_ARRAY_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT)
	for i := uint32(0); i < 7; i++ {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, i, 0)
	}
	gl.UseProgram(gfx.Program)
	return cmds
}

// drawIndirect issues group gi's draw from cmds, bound as the indirect buffer,
// with whatever program and VAO the caller has bound.
func (in *sceneryInstancer) drawIndirect(gi int, g *gfx.Geometry) {
	off := gl.PtrOffset(gi * 20)
	if g.Indices != nil && g.Ebo != 0 {
		gl.DrawElementsIndirect(gl.TRIANGLES, gl.UNSIGNED_INT, off)
		return
	}
	gl.DrawArraysIndirect(gl.TRIANGLES, off)
}

// sceneryWorldSkin is the world program's skinning locations, for the
// instanced draws below (gfx keeps its own, unexported).
var sceneryWorldSkin gfx.SkinUniforms

// drawInstancedIndirect is gfx.Geometry.DrawInstanced with the instances and
// their count taken from group gi's indirect command: the same uniforms, the
// same texture, the same vertex array.
func (in *sceneryInstancer) drawInstancedIndirect(gi int, g *gfx.Geometry) {
	if len(g.Vertices) == 0 {
		return
	}
	st := g.SurfaceState()
	gl.UseProgram(gfx.Program)
	gl.Uniform1i(gfx.InstancedUniform, 1)
	gl.Uniform1i(gfx.BakedLightUniform, g.BakedLightFlag())
	st.Apply()
	gfx.ApplySkinUniforms(&sceneryWorldSkin, gfx.Program, nil)
	gl.BindVertexArray(g.Vao)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[st.Texture])
	in.drawIndirect(gi, g)
	gl.Uniform1i(gfx.InstancedUniform, 0)
}

// renderSceneryGPU is renderSceneryInstanced's GPU path.
func renderSceneryGPU(in *sceneryInstancer, lvl *world.Level, cull *Frustum, draw func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool)) int {
	in.syncGPU()
	cmds := in.gpuCull(cull, false)
	// The count, the CPU's: a box a placement, from the instancer's cache.
	visible := 0
	for gi := range in.groups {
		g := &in.groups[gi]
		if g.key.transparent || !in.gpu.counted[gi] {
			continue
		}
		for _, m := range g.members {
			if in.visible(int(m), cull) {
				visible++
			}
		}
	}
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, cmds)
	for gi := range in.groups {
		g := &in.groups[gi]
		if g.key.transparent {
			continue
		}
		in.drawInstancedIndirect(gi, &lvl.Scenery[g.rep].Submeshes[g.sub])
	}
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, 0)
	// The transparent groups one placement at a time, sorted with the rest
	// of the frame's: the CPU's, as always.
	for gi := range in.groups {
		g := &in.groups[gi]
		if !g.key.transparent {
			continue
		}
		for _, m := range g.members {
			s := &lvl.Scenery[m]
			if !in.visible(int(m), cull) {
				continue
			}
			draw(&s.Submeshes[g.sub], in.matrix(int(m)), false)
			if g.sub == firstDrawnSub(s) {
				visible++
			}
		}
	}
	return visible
}

// renderSceneryShadowGPU is renderSceneryShadowInstanced's GPU path; prog's
// `instanced` is set for the draws and cleared after.
func renderSceneryShadowGPU(in *sceneryInstancer, lvl *world.Level, prog uint32, skin *gfx.SkinUniforms, cull *Frustum) {
	in.syncGPU()
	cmds := in.gpuCull(cull, true)
	gl.UseProgram(prog)
	gfx.ApplySkinUniforms(skin, prog, nil)
	loc := gfx.UniformLoc(prog, "instanced\x00")
	gl.Uniform1i(loc, 1)
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, cmds)
	for gi := range in.groups {
		g := &in.groups[gi]
		geo := &lvl.Scenery[g.rep].Submeshes[g.sub]
		if !geo.CastsShadow() {
			continue
		}
		c := geo.ShadowCaster()
		if boolToInt32(c.Indices != nil && c.Ebo != 0) != boolToInt32(geo.Indices != nil && geo.Ebo != 0) {
			c = geo
		}
		gl.BindVertexArray(c.Vao)
		in.drawIndirect(gi, c)
	}
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, 0)
	gl.Uniform1i(loc, 0)
}

// releaseGPU lets the GPU half's buffers go.
func (in *sceneryInstancer) releaseGPU() {
	gp := &in.gpu
	for _, b := range []*uint32{&gp.instances, &gp.members, &gp.groups, &gp.local, &gp.blocks, &gp.colour, &gp.shadow} {
		if *b != 0 {
			gl.DeleteBuffers(1, b)
			*b = 0
		}
	}
	*gp = sceneryGPU{}
}
