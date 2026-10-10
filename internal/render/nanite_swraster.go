package render

// nanite_swraster.go — the compute rasteriser for small triangles. Phase 5 of
// the Nanite plan (docs/nanite/PHASE_5.md), component 7.
//
// A dense cooked mesh seen from a little way off is triangles of a pixel or
// two, and the hardware rasteriser is built for bigger ones: it pays a fixed
// setup per triangle and shades in 2x2 quads, so below a few pixels it runs at
// its triangle rate, not its pixel rate. Nanite's answer is to rasterise those
// clusters in a compute shader, a thread a triangle, writing straight into
// the visibility buffer. Here:
//
//  1. ROUTING, in the cluster cull (nanite_cluster_common.glsl, swTake): in a
//     camera pass of a visibility-buffer frame, a cluster that is drawn and
//     whose sphere is under swPixels pixels across goes onto the batch's
//     software list instead of into a command. Only the groups the batch's
//     owner marked (cullBatch.swGroups: materials opaque to the last texel,
//     since the rasteriser decides no coverage; buffers shared by every
//     group, since one dispatch reads one set) and only clusters wholly in
//     front of the near plane by a margin (no clipping needed).
//  2. RASTER (nanite_swraster_comp.glsl), right after the batch's hardware ID
//     draws: each listed cluster gets a record of its own — reserved after
//     the batch's commands' records — and each of its triangles is walked by
//     one thread, writing the nearest depth and the ID with an atomic max.
//     With 64-bit atomics (glext.Int64AtomicsAvailable) depth and ID are one
//     word and one atomic; without them, asked for, two passes of 32-bit
//     atomics (depth, then the ID where the depth won).
//  3. RESOLVE (nanite_swresolve_*.glsl): a pass over the screen box the
//     raster wrote in, writing each covered pixel's depth and ID into the
//     scene target under the depth test, the stencil to 1 where it wins —
//     the ID pass's rule — and clearing the words for the next batch.
//
// Everything downstream — the Hi-Z build between the two occlusion passes,
// the material pass, TAA's motion, the later passes' depth — reads the scene
// target and cannot tell which rasteriser covered a pixel.
//
// # Switches
//
//	FOUNDRY_NANITE_SWRASTER=auto  (default) on with 64-bit atomics, off without
//	FOUNDRY_NANITE_SWRASTER=1     on: 64-bit atomics, else the two-pass form
//	FOUNDRY_NANITE_SWRASTER=32    on, the two-pass form even with 64-bit atomics
//	FOUNDRY_NANITE_SWRASTER=0     off: the hardware draws every triangle
//	FOUNDRY_NANITE_SWRASTER_PX=n  the largest cluster, in pixels across, it takes
//
// The two-pass form is not the default where 64-bit atomics are missing:
// rasterising every small triangle twice is measured slower than letting the
// hardware draw them (docs/nanite/PHASE_5.md). It is there for drivers like
// Mesa's D3D12, where it is the only form that can be tested.

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"foundry/internal/cluster"
	"foundry/internal/gfx"
	"foundry/internal/glext"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// The rasteriser's forms.
const (
	swModeOff = 0
	swMode32  = 32
	swMode64  = 64
)

// NaniteSWRaster is the switch as asked for: "auto", "1", "32", "64" or "0"
// (see the file comment). Read when the programs are built (and by tests,
// which rebuild them through naniteSW.reset).
var NaniteSWRaster = strings.ToLower(strings.TrimSpace(os.Getenv("FOUNDRY_NANITE_SWRASTER")))

// NaniteSWPixels is the largest cluster, its bounding sphere's diameter in
// pixels, the compute rasteriser takes. Capped at naniteSWMaxPixels: the
// rasteriser's edge functions are 32-bit products of coordinates in 1/256
// pixel, which a cluster any wider would overflow.
var NaniteSWPixels = envFloat("FOUNDRY_NANITE_SWRASTER_PX", 32)

const (
	naniteSWMaxPixels = 64
	// naniteSWListMax is the most clusters one pass lists: past it, the
	// hardware draws the rest. Its records come out of the frame's 2^20.
	naniteSWListMax = 1 << 16
	// naniteSWGroups is how many workgroups the raster is dispatched as,
	// each taking every naniteSWGroups-th listed cluster.
	naniteSWGroups = 512
	// naniteVisClusterTris is the most triangles a cluster may have for the
	// visibility buffer: its 12 bits of triangle number hold
	// naniteVisMergeClusters of them (nanite_vis.go), and the software
	// rasteriser's thread a triangle is a workgroup of this many.
	naniteVisClusterTris = 128
)

// The invariant the IDs rest on, checked where the compiler can: a merged
// command of naniteVisMergeClusters clusters of naniteVisClusterTris
// triangles numbers exactly the 12 bits, and the clusterer's default fits.
// Changing either constant fails the build here rather than numbering a
// triangle into its neighbour's record.
const (
	_ = uint(1<<12 - naniteVisMergeClusters*naniteVisClusterTris)
	_ = uint(naniteVisMergeClusters*naniteVisClusterTris - 1<<12)
	_ = uint(naniteVisClusterTris - cluster.DefaultMaxTriangles)
)

// naniteClusterTrisMax is the most triangles any cluster uploaded to a cull
// so far has had (toGPUCluster). A cooked file or a compiler option could
// make bigger ones; the visibility buffer then refuses every batch (the
// forward path draws them), rather than mis-number them.
var naniteClusterTrisMax uint32

// noteClusterTris records a cluster's triangle count.
func noteClusterTris(n uint32) {
	if n > naniteClusterTrisMax {
		naniteClusterTrisMax = n
		if n > naniteVisClusterTris {
			log.Printf("nanite: a cluster of %d triangles (more than %d): the visibility buffer is off", n, naniteVisClusterTris)
		}
	}
}

// naniteClustersFitVis is whether every cluster so far fits an ID.
func naniteClustersFitVis() bool { return naniteClusterTrisMax <= naniteVisClusterTris }

func envFloat(name string, def float32) float32 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(name)), 32); err == nil && v > 0 {
		return float32(v)
	}
	return def
}

// naniteSWRaster is the rasteriser's programs and its per-pixel buffers.
type naniteSWRaster struct {
	tried, ok bool
	mode      int
	raster    progUniforms
	resolve   progUniforms
	// The per-pixel words: one 64-bit buffer (a), or a depth (a) and an ID
	// (b) buffer of 32 bits.
	a, b uint32
	w, h int32
	// box is the screen box the raster wrote in (four ints), and boxTex a
	// buffer texture over it, which the resolve's vertex stage reads.
	box, boxTex uint32
	// dummy stands in at bindings 16 and 17 for a cull without a list.
	dummy uint32
}

var naniteSW naniteSWRaster

// wantedMode is the form the switch and the driver allow.
func wantedSWMode() int {
	glext.Init()
	has64 := glext.Int64AtomicsAvailable()
	switch NaniteSWRaster {
	case "0", "off", "false", "no":
		return swModeOff
	case "1", "on", "true", "yes":
		if has64 {
			return swMode64
		}
		return swMode32
	case "32":
		return swMode32
	case "64":
		if has64 {
			return swMode64
		}
		return swModeOff
	}
	if has64 {
		return swMode64
	}
	return swModeOff
}

// ready builds the programs the first time it is asked, and reports whether
// the rasteriser runs.
func (s *naniteSWRaster) ready() bool {
	if s.tried {
		return s.ok
	}
	s.tried = true
	if s.dummy == 0 {
		gl.GenBuffers(1, &s.dummy)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, s.dummy)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, 64, gl.Ptr(make([]byte, 64)), gl.STATIC_DRAW)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	}
	s.mode = wantedSWMode()
	if s.mode == swModeOff {
		return false
	}
	err := s.build(s.mode)
	if err != nil && s.mode == swMode64 && NaniteSWRaster != "64" {
		// The extension listed but the compiler would not take it.
		log.Printf("nanite: compute rasteriser: 64-bit form unavailable: %v", err)
		if NaniteSWRaster == "1" || NaniteSWRaster == "on" || NaniteSWRaster == "true" || NaniteSWRaster == "yes" {
			s.mode = swMode32
			err = s.build(s.mode)
		} else {
			s.mode = swModeOff
			return false
		}
	}
	if err != nil {
		log.Printf("nanite: compute rasteriser unavailable: %v — the hardware draws every triangle", err)
		s.mode = swModeOff
		return false
	}
	s.ok = true
	log.Printf("nanite: compute rasteriser on (%d-bit atomics, clusters under %.0f px)", s.mode, swPixels())
	return true
}

func (s *naniteSWRaster) build(mode int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	head := "#version 430\n"
	if mode == swMode64 {
		head += "#extension GL_ARB_gpu_shader_int64 : enable\n#extension GL_NV_gpu_shader5 : enable\n" +
			"#extension GL_NV_shader_atomic_int64 : require\n#define SW64\n"
	}
	rsrc, werr := gfx.InjectWind(head + mustReadShader(biomeShaderPath("nanite_swraster_comp.glsl")))
	if werr != nil {
		return fmt.Errorf("raster: %v", werr)
	}
	cs, cerr := gfx.CompileShader(rsrc, gl.COMPUTE_SHADER)
	if cerr != nil {
		return fmt.Errorf("raster: %v", cerr)
	}
	prog, lerr := linkStages(cs)
	if lerr != nil {
		return fmt.Errorf("raster: %v", lerr)
	}
	s.raster = progUniforms{prog: prog, loc: map[string]int32{}}
	prog, err = gfx.CompileProgramFromSources(mustReadShader(biomeShaderPath("nanite_swresolve_vert.glsl")),
		head+mustReadShader(biomeShaderPath("nanite_swresolve_frag.glsl")))
	if err != nil {
		return fmt.Errorf("resolve: %v", err)
	}
	s.resolve = progUniforms{prog: prog, loc: map[string]int32{}}
	return nil
}

// reset forgets the programs and buffers, so the next frame builds them for
// the switch as it now stands (tests and benchmarks).
func (s *naniteSWRaster) reset() {
	for _, p := range []*progUniforms{&s.raster, &s.resolve} {
		if p.prog != 0 {
			gl.DeleteProgram(p.prog)
		}
		*p = progUniforms{}
	}
	for _, b := range []*uint32{&s.a, &s.b, &s.box} {
		if *b != 0 {
			gl.DeleteBuffers(1, b)
			*b = 0
		}
	}
	if s.boxTex != 0 {
		gl.DeleteTextures(1, &s.boxTex)
		s.boxTex = 0
	}
	s.w, s.h = 0, 0
	s.tried, s.ok, s.mode = false, false, swModeOff
}

// on reports whether the rasteriser runs this frame.
func (s *naniteSWRaster) on() bool { return s.ready() }

func swPixels() float32 { return min(max(NaniteSWPixels, 1), naniteSWMaxPixels) }

// ensure sizes the per-pixel words to the scene target, cleared.
func (s *naniteSWRaster) ensure(w, h int32) {
	if s.w == w && s.h == h && s.a != 0 {
		return
	}
	s.w, s.h = w, h
	words := int(w) * int(h)
	alloc := func(buf *uint32, bytes int) {
		if *buf == 0 {
			gl.GenBuffers(1, buf)
		}
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, *buf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, bytes, nil, gl.DYNAMIC_COPY)
		zero := uint32(0)
		gl.ClearBufferData(gl.SHADER_STORAGE_BUFFER, gl.R32UI, gl.RED_INTEGER, gl.UNSIGNED_INT, gl.Ptr(&zero))
	}
	if s.mode == swMode64 {
		alloc(&s.a, words*8)
	} else {
		alloc(&s.a, words*4)
		alloc(&s.b, words*4)
	}
	if s.box == 0 {
		alloc(&s.box, 16)
		gl.GenTextures(1, &s.boxTex)
		gl.BindTexture(gl.TEXTURE_BUFFER, s.boxTex)
		gl.TexBuffer(gl.TEXTURE_BUFFER, gl.RGBA32I, s.box)
		gl.BindTexture(gl.TEXTURE_BUFFER, 0)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

// swEligible is whether a group in material m can be rasterised in compute:
// what the ID pass draws (prepassable), and opaque — no per-surface alpha, no
// blend, and a texture with no texel under full alpha (or none at all: the
// normal colour) — since coverage is the one thing the rasteriser does not
// decide.
func swEligible(m *gfx.Material) bool {
	if !prepassable(m) {
		return false
	}
	st := m.SurfaceState()
	if st.Alpha < 1 || st.BlendTexture != "" || st.BlendMask != "" {
		return false
	}
	if st.NormalColorFlag() != 0 {
		return true
	}
	return textureOpaque(st.Texture)
}

// textureOpaqueCache is textureOpaque's answer by GL texture name: one
// readback a texture, the first time a batch asks.
var textureOpaqueCache = map[uint32]bool{}

// textureOpaque reports whether every texel of the named texture's top level
// has full alpha.
func textureOpaque(name string) bool {
	tex := gfx.Textures[name]
	if tex == 0 {
		return false
	}
	if v, ok := textureOpaqueCache[tex]; ok {
		return v
	}
	var w, h int32
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, 0, gl.TEXTURE_WIDTH, &w)
	gl.GetTexLevelParameteriv(gl.TEXTURE_2D, 0, gl.TEXTURE_HEIGHT, &h)
	opaque := false
	if w > 0 && h > 0 {
		px := make([]uint8, int(w)*int(h)*4)
		glext.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
		opaque = true
		for i := 3; i < len(px); i += 4 {
			if px[i] != 255 {
				opaque = false
				break
			}
		}
	}
	gl.BindTexture(gl.TEXTURE_2D, 0)
	textureOpaqueCache[tex] = opaque
	return opaque
}

// setSWGroups marks which of the batch's groups the compute rasteriser may
// take (nil: none), uploaded at the next cull.
func (b *cullBatch) setSWGroups(ok []bool) {
	b.swGroups = ok
	b.swGroupsDirty = true
}

// swPrepare readies a camera pass's list, called by cull: the cap this pass
// lists up to (0: the rasteriser is off for it), the buffers bound.
func (b *cullBatch) swPrepare(v cullView, debug bool) {
	b.swCap = 0
	admit := b.swAdmit
	b.swAdmit = 0
	if v.camera && !debug && admit > 0 && b.swGroups != nil && naniteSW.on() && naniteVisActive() {
		b.swCap = admit
	}
	if b.swCap == 0 {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 16, naniteSW.dummy)
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 17, naniteSW.dummy)
		return
	}
	if b.swGroupBuf == 0 {
		gl.GenBuffers(1, &b.swGroupBuf)
		gl.GenBuffers(1, &b.swListBuf)
	}
	if b.swGroupsDirty {
		b.swGroupsDirty = false
		flags := make([]uint32, max(len(b.groups), len(b.swGroups), 1))
		for i, ok := range b.swGroups {
			if ok {
				flags[i] = 1
			}
		}
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.swGroupBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(flags)*4, gl.Ptr(flags), gl.STATIC_DRAW)
	}
	if need := 16 + b.swCap*32; need > b.swListSize {
		b.swListSize = need
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.swListBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, need, nil, gl.DYNAMIC_COPY)
	}
	// The count, from zero; the entries are only read up to it.
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.swListBuf)
	zero := uint32(0)
	gl.ClearBufferSubData(gl.SHADER_STORAGE_BUFFER, gl.R32UI, 0, 4, gl.RED_INTEGER, gl.UNSIGNED_INT, gl.Ptr(&zero))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 16, b.swListBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 17, b.swGroupBuf)
}

// swUniforms sends the routing's uniforms to a cluster stage.
func (b *cullBatch) swUniforms(p *progUniforms) {
	gl.Uniform1i(p.U("swOn"), boolToInt32(b.swCap > 0))
	if b.swCap == 0 {
		return
	}
	cam := gfx.Cam.Matrix
	fwd := mgl32.Vec3{-cam[2], -cam[6], -cam[10]}
	_, _, near := perspectiveParams(gfx.Projection)
	gl.Uniform3f(p.U("swForward"), fwd[0], fwd[1], fwd[2])
	gl.Uniform1f(p.U("swScale"), gfx.Projection[5]*float32(scene.h)*0.5)
	gl.Uniform1f(p.U("swPixels"), swPixels())
	gl.Uniform1f(p.U("swNear"), max(2*near, 0.05))
	gl.Uniform1ui(p.U("swCap"), uint32(b.swCap))
}

// swClusterCount is how many clusters the batch's tiles hold: the most one
// pass could list.
func (b *cullBatch) swClusterCount() int { return b.swClusters }

// admitSW decides, before a camera pass's cull, whether the compute
// rasteriser takes part in it, and how many clusters it may list: what the
// visibility buffer will accept from drawBatch, reserving that many records
// beyond the batch's commands. A cull that lists clusters for a drawBatch
// that then refused the batch would lose them, so the two ask the same
// question (naniteVisPass.admits).
func (b *cullBatch) admitSW(groups []visGroup) {
	b.swAdmit = 0
	if b.swGroups == nil || !naniteVisActive() || !naniteSW.on() {
		return
	}
	for i := 1; i < len(groups); i++ {
		if groups[i].vbo != groups[0].vbo || groups[i].ebo != groups[0].ebo {
			return
		}
	}
	v := &naniteVis
	cap := min(b.swClusterCount(), naniteSWListMax, naniteVisMaxRecords-v.recordsUsed-b.visSpan(naniteCull.compactable()))
	if cap <= 0 || !v.admits(b, groups, cap, false) {
		return
	}
	b.swAdmit = cap
}

// rasterBatch runs the compute rasteriser over the pass's list and resolves
// it into the scene target. Called by drawBatch after the hardware's ID
// draws, with the scene target bound and the ID pass's stencil rule set; g
// is any of the batch's groups (they share their buffers), recordBase the
// list's first record, binBase the batch's first bin, listCap the cap the
// cull listed up to.
func (s *naniteSWRaster) rasterBatch(b *cullBatch, g *visGroup, recordBase, binBase, listCap int) {
	ProfileBegin("nanite: sw raster")
	s.ensure(scene.w, scene.h)
	p := &s.raster
	gl.UseProgram(p.prog)
	gl.UniformMatrix4fv(p.U("projection"), 1, false, &gfx.Projection[0])
	gl.UniformMatrix4fv(p.U("camera"), 1, false, &gfx.Cam.Matrix[0])
	gl.Uniform2i(p.U("viewSize"), scene.w, scene.h)
	gl.Uniform1ui(p.U("recordBase"), uint32(recordBase))
	gl.Uniform1ui(p.U("binBase"), uint32(binBase))
	gl.Uniform1ui(p.U("swCap"), uint32(listCap))
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, b.instBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, naniteVis.records)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 2, g.ebo)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 3, g.vbo)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 16, b.swListBuf)
	// The box, empty: min past the screen, max before it.
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, s.box)
	empty := [4]int32{scene.w, scene.h, -1, -1}
	gl.ClearBufferData(gl.SHADER_STORAGE_BUFFER, gl.RGBA32I, gl.RGBA_INTEGER, gl.INT, gl.Ptr(&empty[0]))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 7, s.box)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 5, s.a)
	if s.mode != swMode64 {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 6, s.b)
	}
	groups := uint32(min(listCap, naniteSWGroups))
	if s.mode == swMode64 {
		gl.DispatchCompute(groups, 1, 1)
	} else {
		gl.Uniform1i(p.U("idPass"), 0)
		gl.DispatchCompute(groups, 1, 1)
		gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
		gl.Uniform1i(p.U("idPass"), 1)
		gl.DispatchCompute(groups, 1, 1)
	}
	// The words and the records for the resolve and the material pass, the
	// box for the resolve's vertex stage.
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT)
	ProfileEnd()

	ProfileBegin("nanite: sw resolve")
	r := &s.resolve
	gl.UseProgram(r.prog)
	gl.Uniform2i(r.U("viewSize"), scene.w, scene.h)
	gl.Uniform1i(r.U("swBox"), 0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_BUFFER, s.boxTex)
	drawBuffersOnly(sceneAttachVisID)
	gl.BindVertexArray(naniteVis.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, 6)
	gl.BindVertexArray(0)
	gl.BindTexture(gl.TEXTURE_BUFFER, 0)
	// The words it cleared, before the next batch's raster writes them; the
	// records, before the material pass reads them.
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	for _, i := range []uint32{0, 1, 2, 3, 5, 6, 7, 16} {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, i, 0)
	}
	ProfileEnd()
	if NaniteStats {
		b.countSW(listCap)
	}
}

// countSW adds the pass's listed clusters to DebugStats.NaniteSWClusters: a
// readback, for the debug overlay only (NaniteStats).
func (b *cullBatch) countSW(listCap int) {
	var n uint32
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.swListBuf)
	glext.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, 4, gl.Ptr(&n))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	DebugStats.NaniteSWClusters += int(min(n, uint32(listCap)))
}
