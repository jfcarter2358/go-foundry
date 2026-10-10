package render

// nfoliage_voxraster.go — the Nanite foliage's voxel bricks drawn by a
// compute rasteriser where the driver has 64-bit atomics. Phase 6 of the
// Nanite plan (docs/nanite/PHASE_6.md): the spec's "rasterised in their own
// bin (software raster when available, else ray-marched boxes)".
//
// A brick a few pixels across is the common case — a forest past a hundred
// metres, a meadow past twenty — and to the hardware it is a box of twelve
// triangles and a ray march in every pixel it covers, or a point sprite and
// the same march. Measured on the GTX 1070 (docs/nanite/PHASE_6.md) that was
// most of the foliage's frame on a forest bench level. The cull already puts
// such bricks on their clusters' SMALL commands (nfoliage_cull_comp.glsl);
// where this runs, those references are not drawn but rasterised in compute:
// a pass a thread a brick places each brick and lists its occupied voxels
// (nfoliage_voxlist_comp.glsl), then a pass a thread a voxel
// (nfoliage_voxraster_comp.glsl) writes the pixels each one's footprint
// covers by a 64-bit atomic max of depth and identity. A resolve (nfoliage_voxresolve_frag.glsl)
// over the screen box written then shades each pixel once — the ray march's
// shading — under the depth test, and clears the words for the next frame.
// Bricks big on screen keep the hardware's boxes.
//
// FOUNDRY_NANITE_FOLIAGE_SWRASTER: auto (the default: on with 64-bit
// atomics, else boxes and points), 1 (on, or a log line saying why not), 0
// (off). Without 64-bit atomics there is no two-pass form here, unlike Phase
// 5's: the ray-marched boxes are this path's fallback.

import (
	"fmt"
	"log"
	"os"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// NaniteFoliageSWRaster is the rasteriser's switch (see the file comment).
var NaniteFoliageSWRaster = func() string {
	if v := os.Getenv("FOUNDRY_NANITE_FOLIAGE_SWRASTER"); v != "" {
		return v
	}
	return "auto"
}()

// nfVoxRaster is the compute rasteriser.
type nfVoxRaster struct {
	tried, ok bool
	list      progUniforms
	dispatch  progUniforms
	raster    progUniforms
	resolve   progUniforms
	// setupBuf holds each small brick's place (nfSetupBytes a brick, room
	// for setupCap), listBuf a header (the count, the dropped, the raster's
	// dispatch) and listCap voxel entries. Both grow when a frame reports
	// it ran out (readback, a frame or two late).
	setupBuf, listBuf uint32
	setupCap, listCap int
	growSetup         bool
	readback          [3]readbackSlot
	rbNext            int
	// lastVoxels is the voxels the last read frame listed.
	lastVoxels int
	// vis is a 64-bit word a pixel; box the screen box written (four ints)
	// and boxTex a buffer texture over it for the resolve's vertex stage;
	// refsTex the reference list as texels for the resolve.
	vis, box, boxTex uint32
	refsTex          uint32
	w, h             int32
	vao              uint32
}

// init decides whether the rasteriser runs and builds it.
func (v *nfVoxRaster) init(r *nfRenderer) {
	v.ok = false
	v.tried = true
	switch NaniteFoliageSWRaster {
	case "0", "off", "false", "no":
		return
	}
	glext.Init()
	if !glext.Int64AtomicsAvailable() {
		if NaniteFoliageSWRaster != "auto" {
			log.Printf("foliage: compute rasteriser asked for, but the driver has no 64-bit atomics — bricks are ray-marched")
		}
		return
	}
	if err := v.build(); err != nil {
		log.Printf("foliage: compute rasteriser unavailable: %v — bricks are ray-marched", err)
		return
	}
	gl.GenBuffers(1, &v.box)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.box)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, 16, nil, gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.GenTextures(1, &v.boxTex)
	gl.BindTexture(gl.TEXTURE_BUFFER, v.boxTex)
	gl.TexBuffer(gl.TEXTURE_BUFFER, gl.RGBA32I, v.box)
	gl.GenTextures(1, &v.refsTex)
	gl.BindTexture(gl.TEXTURE_BUFFER, v.refsTex)
	gl.TexBuffer(gl.TEXTURE_BUFFER, gl.RG32UI, r.refBuf)
	gl.BindTexture(gl.TEXTURE_BUFFER, 0)
	gl.GenVertexArrays(1, &v.vao)
	v.ok = true
}

// int64Head is what a stage that keeps 64-bit words starts with.
const int64Head = "#extension GL_ARB_gpu_shader_int64 : enable\n#extension GL_NV_gpu_shader5 : enable\n" +
	"#extension GL_NV_shader_atomic_int64 : require\n"

func (v *nfVoxRaster) build() (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	for _, c := range []struct {
		name    string
		out     *progUniforms
		defines string
	}{{"nfoliage_voxlist_comp.glsl", &v.list, ""}, {"nfoliage_voxlist_comp.glsl", &v.dispatch, "#define NF_DISPATCH 1\n"},
		{"nfoliage_voxraster_comp.glsl", &v.raster, ""}} {
		src := "#version 430\n" + int64Head + c.defines + nfSnippet() + "\n" + mustReadShader(biomeShaderPath("nfoliage_voxel.glsl")) + "\n" +
			mustReadShader(biomeShaderPath(c.name))
		cs, cerr := gfx.CompileShader(src, gl.COMPUTE_SHADER)
		if cerr != nil {
			return fmt.Errorf("%s: %v", c.name, cerr)
		}
		prog, lerr := linkStages(cs)
		if lerr != nil {
			return fmt.Errorf("%s: %v", c.name, lerr)
		}
		*c.out = progUniforms{prog: prog, loc: map[string]int32{}}
	}
	frag := int64Head + nfSnippet() + "\n" + biomeLightSnippet(0, false, false) + "\n#define NF_VOXEL_SHADE 1\n" +
		mustReadShader(biomeShaderPath("nfoliage_voxel.glsl"))
	v.resolve = progUniforms{prog: mustBiomeProgram(biomeShaderPath("nanite_swresolve_vert.glsl"),
		biomeShaderPath("nfoliage_voxresolve_frag.glsl"), "", frag), loc: map[string]int32{}}
	return nil
}

func (v *nfVoxRaster) mode() string {
	if v.ok {
		return "compute raster (64-bit atomics)"
	}
	return "ray-marched boxes"
}

// ensure sizes the words to a w by h view, cleared.
func (v *nfVoxRaster) ensure(w, h int32) {
	if v.w == w && v.h == h && v.vis != 0 {
		return
	}
	v.w, v.h = w, h
	if v.vis == 0 {
		gl.GenBuffers(1, &v.vis)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.vis)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, int(w)*int(h)*8, nil, gl.DYNAMIC_COPY)
	zero := uint32(0)
	gl.ClearBufferData(gl.SHADER_STORAGE_BUFFER, gl.R32UI, gl.RED_INTEGER, gl.UNSIGNED_INT, gl.Ptr(&zero))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

// draw rasterises the camera pass's small bricks (the cull routed them only
// when the rasteriser runs) and resolves them.
func (v *nfVoxRaster) draw(r *nfRenderer, lvl *world.Level, view nfView, frame ShadowFrame, motion bool) {
	if !v.ok || view.shadow || r.groupCount[nfGroupSmall] == 0 {
		return
	}
	ProfileBegin("foliage: voxel raster")
	vw, vh := view.viewport[2], view.viewport[3]
	v.ensure(vw, vh)
	v.collect()
	v.room(r)
	// The shape table and the plant data every stage places by.
	for _, p := range []*progUniforms{&v.list, &v.raster} {
		gl.UseProgram(p.prog)
		r.bindVertexData(p)
		uploadFoliageParams(p.U("shapeRange"), p.U("shapeMore"), p.U("bucketInfo"), p.U("bucketImp"), p.U("shapeBark"))
		gl.Uniform1ui(p.U("setupCap"), uint32(v.setupCap))
		gl.Uniform1ui(p.U("listCap"), uint32(v.listCap))
	}
	gl.UseProgram(v.dispatch.prog)
	gl.Uniform1ui(v.dispatch.U("listCap"), uint32(v.listCap))
	// The list's header from zero, the box empty: min past the screen, max
	// before it.
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.listBuf)
	zero := make([]uint32, 8)
	gl.BufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(zero)*4, gl.Ptr(zero))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.box)
	empty := [4]int32{vw, vh, -1, -1}
	gl.ClearBufferData(gl.SHADER_STORAGE_BUFFER, gl.RGBA32I, gl.RGBA_INTEGER, gl.INT, gl.Ptr(&empty[0]))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 3, r.refBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 13, r.counterBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 14, v.vis)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 15, v.box)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 16, v.setupBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 17, v.listBuf)

	// A thread a small brick: place it, list its voxels.
	gl.UseProgram(v.list.prog)
	gl.BindBuffer(gl.DISPATCH_INDIRECT_BUFFER, r.counterBuf)
	gl.DispatchComputeIndirect(nfCtrVoxDisp * 4)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	// The list's length into the raster's dispatch.
	gl.UseProgram(v.dispatch.prog)
	gl.DispatchCompute(1, 1, 1)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT | gl.COMMAND_BARRIER_BIT)
	// A thread a voxel.
	p := &v.raster
	gl.UseProgram(p.prog)
	gl.UniformMatrix4fv(p.U("projView"), 1, false, &view.projView[0])
	gl.Uniform4f(p.U("viewport"), 0, 0, float32(vw), float32(vh))
	gl.Uniform1f(p.U("pxPerUnit"), view.pxPerUnit)
	_, _, near := perspectiveParams(gfx.Projection)
	gl.Uniform1f(p.U("nearClip"), max(near, 1e-3))
	gl.Uniform1i(p.U("coverageDither"), coverageDither())
	gl.BindBuffer(gl.DISPATCH_INDIRECT_BUFFER, v.listBuf)
	gl.DispatchComputeIndirect(16)
	gl.BindBuffer(gl.DISPATCH_INDIRECT_BUFFER, 0)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT)
	v.copyStats()
	ProfileEnd()

	ProfileBegin("foliage: voxel resolve")
	rs := &v.resolve
	uploadBiomeLighting(rs.prog, lvl, frame)
	uploadFoliageParams(rs.U("shapeRange"), rs.U("shapeMore"), rs.U("bucketInfo"), rs.U("bucketImp"), rs.U("shapeBark"))
	r.bindVertexData(rs)
	gl.Uniform2i(rs.U("viewSize"), vw, vh)
	gl.Uniform1i(rs.U("coverageDither"), coverageDither())
	gl.Uniform1i(rs.U("writeMotion"), boolToInt32(motion))
	if motion {
		vp, prev := taa.viewProj, taa.prevViewProj
		if !taa.prevValid {
			prev = vp
		}
		gl.UniformMatrix4fv(rs.U("motionViewProj"), 1, false, &vp[0])
		gl.UniformMatrix4fv(rs.U("motionPrevViewProj"), 1, false, &prev[0])
	}
	const boxUnit, refsUnit = nfBoxUnit, nfRefsUnit
	gl.Uniform1i(rs.U("swBox"), boxUnit)
	gl.Uniform1i(rs.U("nfRefs"), refsUnit)
	gl.ActiveTexture(gl.TEXTURE0 + boxUnit)
	gl.BindTexture(gl.TEXTURE_BUFFER, v.boxTex)
	gl.ActiveTexture(gl.TEXTURE0 + refsUnit)
	gl.BindTexture(gl.TEXTURE_BUFFER, v.refsTex)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindVertexArray(v.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, 6)
	gl.BindVertexArray(0)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	for _, i := range []uint32{3, 13, 14, 15, 16, 17} {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, i, 0)
	}
	ProfileEnd()
}

// A small brick's place for the raster (nfoliage_voxlist_comp.glsl's
// BrickSetup), and the room there is at first for bricks and for listed
// voxels.
const (
	nfSetupBytes = 80
	nfSetupCap0  = 1 << 16
	nfListCap0   = 1 << 20
)

// room sizes the setup and the list for what the last frames needed, with
// half again to spare.
func (v *nfVoxRaster) room(r *nfRenderer) {
	if want := max(nfSetupCap0, r.last.smallBricks+r.last.smallBricks/2); want > v.setupCap || v.growSetup {
		v.growSetup = false
		v.setupCap = max(want, v.setupCap*2)
		if v.setupBuf == 0 {
			gl.GenBuffers(1, &v.setupBuf)
		}
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.setupBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, v.setupCap*nfSetupBytes, nil, gl.DYNAMIC_COPY)
	}
	if want := max(nfListCap0, v.lastVoxels+v.lastVoxels/2); want > v.listCap {
		v.listCap = want
		if v.listBuf == 0 {
			gl.GenBuffers(1, &v.listBuf)
		}
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.listBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, 32+v.listCap*4, nil, gl.DYNAMIC_COPY)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

// copyStats copies the list's header into the readback ring.
func (v *nfVoxRaster) copyStats() {
	s := &v.readback[v.rbNext]
	if s.buf == 0 {
		for i := range v.readback {
			v.readback[i].init(32, false)
		}
	}
	if s.pending {
		return
	}
	s.copyFrom(v.listBuf, 32, uint32(foliage.frame))
	v.rbNext = (v.rbNext + 1) % len(v.readback)
}

// collect reads whichever headers have landed: how many voxels were listed,
// and whether any brick was dropped for want of room.
func (v *nfVoxRaster) collect() {
	for i := range v.readback {
		s := &v.readback[i]
		if !s.pending || !s.ready() {
			continue
		}
		w := s.read()
		s.done()
		s.pending = false
		if w == nil {
			continue
		}
		v.lastVoxels = int(w[0])
		if w[1] != 0 {
			v.growSetup = true
		}
	}
}

// release frees the rasteriser's GL objects.
func (v *nfVoxRaster) release() {
	for _, p := range []*progUniforms{&v.list, &v.dispatch, &v.raster, &v.resolve} {
		if p.prog != 0 {
			gl.DeleteProgram(p.prog)
		}
		*p = progUniforms{}
	}
	for _, b := range []*uint32{&v.vis, &v.box, &v.setupBuf, &v.listBuf} {
		if *b != 0 {
			gl.DeleteBuffers(1, b)
			*b = 0
		}
	}
	for _, t := range []*uint32{&v.boxTex, &v.refsTex} {
		if *t != 0 {
			gl.DeleteTextures(1, t)
			*t = 0
		}
	}
	if v.vao != 0 {
		gl.DeleteVertexArrays(1, &v.vao)
		v.vao = 0
	}
	for i := range v.readback {
		v.readback[i].release()
		v.readback[i] = readbackSlot{}
	}
	v.rbNext = 0
	v.w, v.h, v.setupCap, v.listCap = 0, 0, 0, 0
	v.ok, v.tried = false, false
}
