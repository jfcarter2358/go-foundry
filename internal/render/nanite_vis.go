package render

// nanite_vis.go — the visibility buffer and its material pass. Phase 4 of the
// Nanite plan (docs/nanite/PHASE_4.md), component 8.
//
// Until Phase 4 what the GPU-driven cull drew (compiled surfaces, chunk
// brushes, cooked meshes; nanite_cull.go) was shaded as it was drawn: every
// fragment that passed the depth test ran the whole light model, so a pixel
// paid once per surface that reached it in draw order, and Phase 1 added a
// depth prepass (nanite_prepass.go) to bring that back to about once. In a
// scene-target frame (taa.go) it is now drawn twice over in a different way:
//
//  1. THE ID PASS, in place of the colour draws: the same culled commands,
//     drawn through vertex.glsl and a fragment stage that writes, instead of
//     a colour, which triangle won the pixel (nanite_vis_frag.glsl) — 32 bits
//     into the scene target's COLOR1. Before each pass draws, its commands
//     are numbered (nanite_vis_number_comp.glsl): each gets a RECORD — where
//     its triangles start, its base vertex, its instance, its material bin —
//     and its base instance becomes the record's number, which the vertex
//     stage reads the record by (an instanced attribute at 11-12) and the ID
//     carries in its top 20 bits; the triangle within the command is
//     gl_PrimitiveID, the bottom 12 (a command is at most 32 clusters, which
//     the merge is capped at: naniteVisMergeClusters).
//  2. THE MATERIAL PASS, once after the cull's last pass (naniteVisShade):
//     a full-screen pass writes each pixel's bin (a batch's material group;
//     nanite_vis_classify_frag.glsl), a compute pass lists the 64x64 tiles
//     each bin has pixels in (nanite_vis_tiles_comp.glsl), and each bin is
//     drawn as its tiles (one indirect instanced draw), shading only its own
//     pixels: the triangle fetched back, its corners run through vertex.glsl,
//     the rasteriser's interpolation redone at the pixel, and fragment.glsl's
//     own main run on that (nanite_material_frag.glsl; how the two files are
//     reused unchanged is nanite_vis_source.go). One shading a pixel,
//     however much was drawn behind it.
//
// Everything else in the opaque world (terrain, entities, classic scenery,
// moved brushes, the editor's markers) still draws the forward way, before
// and after and in between the ID passes. Which of the two owns a pixel at
// the end is the STENCIL: an ID draw writes 1 where it passes the depth test,
// every other draw of the opaque block 0, so the last thing to win the pixel
// decides, and the material pass shades only stencil 1. The passes after the
// material pass — foliage, scatter, cutout sprites, decals, the sky, the
// transparents, the volumetrics, the viewmodel — draw over the shaded colour
// against the ID pass's depth, which is the forward path's depth to the bit.
//
// What the material pass does not shade — a water or blended material in a
// batch (prepassable says which) — is drawn the forward way right after its
// batch's ID draw, and keeps its own pixels by the stencil like the rest.
//
// FOUNDRY_NANITE_VISBUFFER=0 keeps the forward draws (and the Phase 1 depth
// prepass) in a scene-target frame too; a frame that is not one (MSAA, the
// editor) never uses the visibility buffer. A batch the frame cannot number
// (more than naniteVisMaxRecords commands, naniteVisMaxBins bins, or a buffer
// past the driver's largest storage block) is drawn the forward way.

import (
	"cmp"
	"fmt"
	"log"
	"slices"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// NaniteVisBuffer switches the visibility buffer; see the file comment.
var NaniteVisBuffer = gfx.EnvFlag("FOUNDRY_NANITE_VISBUFFER", true)

// NaniteVisDense numbers only the commands that draw, from one GPU counter
// the frame's passes share (nanite_vis_number_comp.glsl), instead of
// reserving a record for every command slot a pass might fill. Reserved by
// slot, the generated canyon's overview (6,607 placements, 330k tiles)
// passed the 2^20 records an ID can name in its first pass, so the occlusion
// pass went the forward way and the compute rasteriser was refused every
// cluster (docs/canyon/PERF.md). FOUNDRY_NANITE_VIS_DENSE=0: by slot.
var NaniteVisDense = gfx.EnvFlag("FOUNDRY_NANITE_VIS_DENSE", true)

const (
	// naniteVisMaxRecords is the most commands a frame can number: the ID's
	// top 20 bits, less the zero that means "nothing here".
	naniteVisMaxRecords = 1<<20 - 2
	// naniteVisMaxBins is the most material bins a frame shades
	// (nanite_vis_tiles_comp.glsl's MAX_BIN_WORDS·32).
	naniteVisMaxBins = 8192
	// naniteVisTile is the material pass's tile, in pixels.
	naniteVisTile = 64
	// naniteVisMergeClusters caps a merged command at 32 clusters of at most
	// 128 triangles: 4096, the ID's 12 bits of triangle.
	naniteVisMergeClusters = 32
	// naniteVisRecordBytes is a record's size: two uvec4.
	naniteVisRecordBytes = 32
)

// Texture units the material pass reads the IDs and bins on: free in the
// world program (shadow.go, gi.go, texture_normal.go, cloud_shadow.go).
const (
	naniteVisIDUnit  = 14
	naniteVisBinUnit = 19
)

// naniteVisCountShading makes the material pass count the pixels it shades
// into naniteVisShadedPixels: a query and a wait, for the tests only.
var (
	naniteVisCountShading bool
	naniteVisShadedPixels int
)

// visGroup is one of a batch's groups as the visibility buffer sees it.
type visGroup struct {
	mat *gfx.Material
	vao uint32
	// The group's buffers, which the material pass reads the triangle from:
	// positions and UVs, normals (0: none), baked light (0: none), indices,
	// and vertex colours (0: none; set only for a group whose material has
	// VertexColors).
	vbo, nbo, lbo, ebo uint32
	cbo                uint32
	baked              bool
	// instanced: the VAO carries the cull's instance matrices at 7-10 (the
	// cooked-mesh pool), which the ID draw must not fetch — its base
	// instance is a record number.
	instanced bool
	// forward draws the group the forward way, for a material the material
	// pass does not shade; the cull's commands are this pass's.
	forward func()
}

// visBin is a material bin: one group of one batch.
type visBin struct {
	mat                     *gfx.Material
	vbo, nbo, lbo, ebo, ins uint32
	cbo                     uint32
	baked, shade            bool
	// drawn: an ID pass drew its group this frame; a bin no pass drew has
	// no pixels and is not shaded at all.
	drawn bool
}

type naniteVisPass struct {
	tried, ok bool
	vis       progUniforms
	number    progUniforms
	classify  progUniforms
	tiles     progUniforms
	reset     progUniforms
	material  progUniforms
	matSkin   gfx.SkinUniforms
	// matSkins is each material program's skinning locations (frameUniforms).
	matSkins map[uint32]*gfx.SkinUniforms
	// The level, frame key and placement count warmMaterialVariants built for.
	warmed    *world.Level
	warmedKey uint32
	warmedN   int
	vao       uint32
	maxBlock  int64
	// Buffers: the frame's records, the bins' indirect draws and tile
	// lists, and a stand-in for an absent normal or light buffer.
	records, binDraws, tileLists, dummy uint32
	// next is the dense numbering's counter (NaniteVisDense): one uint.
	next                           uint32
	recordCap, binCap, tileListCap int
	// Framebuffers over the scene target's attachments: the bins (with the
	// stencil, to test against) and the colour.
	classifyFBO, colourFBO uint32
	fboGen                 uint64 // the scene target's gen they were made for
	// The bins as depth (nanite_vis_bindepth_frag.glsl), the material
	// draws' depth test, and its framebuffer.
	binDepth, binDepthFBO uint32
	binDepthProg          progUniforms
	// The material program's per-bin locations, resolved once.
	matLocs                                      gfx.SurfaceLocs
	matBaked, matHasNormals, matHasLight, matBin int32
	// The material pass's source and its specialised programs, by key
	// (nanite_vis_variants.go); generic is the program above, viewed as one.
	matSrc   string
	variants map[uint64]*visMatProg
	generic  *visMatProg
	// binKeys is each shaded bin's variant key, this frame.
	binKeys []uint64

	// The frame.
	active      bool
	used        bool
	recordsUsed int
	bins        []visBin
	binBase     map[*cullBatch]int
	warned      bool
	// cov is the ID pass's coverage state as last set (groupCoverage).
	cov visCoverage
	// order is the material pass's bins in the order it shades them.
	order []int
}

var naniteVis naniteVisPass

func (v *naniteVisPass) ready() bool {
	if v.tried {
		return v.ok
	}
	v.tried = true
	if err := v.build(); err != nil {
		log.Printf("nanite: visibility buffer unavailable: %v — cooked meshes and compiled surfaces are shaded as they are drawn", err)
		return false
	}
	v.ok = true
	return true
}

func (v *naniteVisPass) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	vert, frag, err := gfx.WorldProgramSources(fileutil.ShaderPath("vertex.glsl"), fileutil.ShaderPath("fragment.glsl"))
	if err != nil {
		return err
	}
	visVert, err := visVertexSource(vert)
	if err != nil {
		return err
	}
	prog, err := gfx.CompileProgramFromSources(visVert, mustReadShader(biomeShaderPath("nanite_vis_frag.glsl")))
	if err != nil {
		return fmt.Errorf("ID pass: %v", err)
	}
	v.vis = progUniforms{prog: prog, loc: map[string]int32{}}

	matFrag, err := materialFragmentSource(vert, frag, mustReadShader(biomeShaderPath("nanite_material_frag.glsl")))
	if err != nil {
		return err
	}
	matFrag = giHalfResSource(matFrag) // GI at half resolution (gi_halfres.go)
	v.matSrc = matFrag
	prog, err = gfx.CompileProgramFromSources(mustReadShader(biomeShaderPath("nanite_material_vert.glsl")), matFrag)
	if err != nil {
		return fmt.Errorf("material pass: %v", err)
	}
	v.material = progUniforms{prog: prog, loc: map[string]int32{}}
	v.matLocs = gfx.NewSurfaceLocs(prog)
	v.matBaked, v.matHasNormals = v.material.U("bakedLight"), v.material.U("hasNormals")
	v.matHasLight, v.matBin = v.material.U("hasLight"), v.material.U("bin")

	prog, err = gfx.CompileProgramFromSources(mustReadShader(biomeShaderPath("taa_fullscreen_vert.glsl")),
		mustReadShader(biomeShaderPath("nanite_vis_classify_frag.glsl")))
	if err != nil {
		return fmt.Errorf("classify: %v", err)
	}
	v.classify = progUniforms{prog: prog, loc: map[string]int32{}}
	prog, err = gfx.CompileProgramFromSources(mustReadShader(biomeShaderPath("taa_fullscreen_vert.glsl")),
		mustReadShader(biomeShaderPath("nanite_vis_bindepth_frag.glsl")))
	if err != nil {
		return fmt.Errorf("bin depth: %v", err)
	}
	v.binDepthProg = progUniforms{prog: prog, loc: map[string]int32{}}

	comp := func(src string) (progUniforms, error) {
		cs, cerr := gfx.CompileShader(src, gl.COMPUTE_SHADER)
		if cerr != nil {
			return progUniforms{}, cerr
		}
		p, lerr := linkStages(cs)
		if lerr != nil {
			return progUniforms{}, lerr
		}
		return progUniforms{prog: p, loc: map[string]int32{}}, nil
	}
	if v.number, err = comp(mustReadShader(biomeShaderPath("nanite_vis_number_comp.glsl"))); err != nil {
		return fmt.Errorf("numbering: %v", err)
	}
	tiles := mustReadShader(biomeShaderPath("nanite_vis_tiles_comp.glsl"))
	head := fmt.Sprintf("#version 430\n#define MAX_BIN_WORDS %d\n", naniteVisMaxBins/32)
	body := reVersion.ReplaceAllString(tiles, "")
	if v.tiles, err = comp(head + body); err != nil {
		return fmt.Errorf("tiles: %v", err)
	}
	if v.reset, err = comp(head + "#define TILES_RESET\n" + body); err != nil {
		return fmt.Errorf("tiles reset: %v", err)
	}
	gl.GenVertexArrays(1, &v.vao)
	gl.GenBuffers(1, &v.dummy)
	gl.GenBuffers(1, &v.next)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.next)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, 16, nil, gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.dummy)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, 64, nil, gl.STATIC_DRAW)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.GetInteger64v(gl.MAX_SHADER_STORAGE_BLOCK_SIZE, &v.maxBlock)
	v.binBase = map[*cullBatch]int{}
	return nil
}

// naniteVisBegin starts a frame's visibility buffer: on in a scene-target
// frame, with the switch on, outside the Nanite debug views (which draw the
// cull's work their own way).
func naniteVisBegin() {
	v := &naniteVis
	// The frame that just ended asked for so many records: read back a frame
	// or two later, it decides whether this one hands its overflow to the
	// forward path (nanite_vis_overflow.go).
	naniteVisOverflow.note(v.next, v.used && v.ready())
	v.active = sceneFrameActive() && NaniteVisBuffer && !naniteDebugActive() && naniteCull.ok && v.ready()
	v.used = false
	v.recordsUsed = 0
	DebugStats.NaniteVisBins = 0
	DebugStats.NaniteSWClusters, DebugStats.NaniteSWMode = 0, 0
	if v.active && naniteSW.on() {
		DebugStats.NaniteSWMode = naniteSW.mode
	}
	v.bins = v.bins[:0]
	clear(v.binBase)
	if v.active && NaniteVisDense {
		// The frame's first record.
		zero := [4]uint32{}
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.next)
		gl.BufferSubData(gl.SHADER_STORAGE_BUFFER, 0, 16, gl.Ptr(&zero[0]))
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	}
	if v.active {
		// The opaque block's stencil rule: whatever wins a pixel's depth
		// test sets its stencil, an ID draw to 1 and everything else to 0.
		gl.Enable(gl.STENCIL_TEST)
		gl.StencilMask(0xff)
		gl.StencilFunc(gl.ALWAYS, 0, 0xff)
		gl.StencilOp(gl.KEEP, gl.KEEP, gl.REPLACE)
	}
}

// naniteVisActive reports whether the cull's batches draw into the
// visibility buffer this frame.
func naniteVisActive() bool { return naniteVis.active }

// admits reports whether the frame can take one pass of b's groups with
// extra records beyond its commands' (the compute rasteriser's list): the
// bins, the tile lists' storage block, the records and every buffer the
// material pass reads. warn logs (once) why not. drawBatch asks it, and so
// does the rasteriser before the cull lists anything (cullBatch.admitSW): a
// cluster listed for a batch the frame then refused would draw nowhere.
func (v *naniteVisPass) admits(b *cullBatch, groups []visGroup, extra int, warn bool) bool {
	say := func(format string, args ...any) bool {
		if warn {
			v.warn(format, args...)
		}
		return false
	}
	if !v.active || len(groups) != len(b.groups) || b.slots == 0 {
		return false
	}
	if !naniteClustersFitVis() {
		return say("a cluster over %d triangles", naniteVisClusterTris)
	}
	if _, ok := v.binBase[b]; !ok {
		if len(v.bins)+len(groups) > naniteVisMaxBins {
			return say("more than %d material bins", naniteVisMaxBins)
		}
		// The bins' tile lists are one storage block, a screen's worth of
		// tiles a bin (ensureBins sizes it at least 256 bins).
		tiles := int64((scene.w+naniteVisTile-1)/naniteVisTile) * int64((scene.h+naniteVisTile-1)/naniteVisTile)
		if lists := int64(max(len(v.bins)+len(groups), 256)) * tiles * 4; v.maxBlock > 0 && lists > v.maxBlock {
			return say("tile lists larger than the driver's largest storage block (%d MB)", v.maxBlock>>20)
		}
	}
	// Dense numbering takes records only for what draws, and drops what
	// would pass the last one, so there is nothing to reserve.
	if !NaniteVisDense && v.recordsUsed+b.visSpan(naniteCull.compactable())+extra > naniteVisMaxRecords {
		return say("more than %d commands", naniteVisMaxRecords)
	}
	if !b.visGroupsFit(groups, v.maxBlock) {
		return say("a buffer larger than the driver's largest storage block (%d MB)", v.maxBlock>>20)
	}
	return true
}

// drawBatch draws one pass of b's groups into the visibility buffer — the
// ones the material pass can shade; the rest the forward way — and, when
// the cull listed clusters for it, runs the compute rasteriser over them
// (nanite_swraster.go). False when the frame cannot take the batch, for the
// caller to draw it the forward way.
func (v *naniteVisPass) drawBatch(b *cullBatch, groups []visGroup) bool {
	sw := b.swCap
	b.swCap = 0
	if !v.admits(b, groups, sw, true) {
		if sw > 0 {
			// admitSW asked the same question before the cull; nothing
			// between should change the answer.
			log.Printf("nanite: visibility buffer refused a batch the compute rasteriser had listed %d clusters for", sw)
		}
		return false
	}
	base, ok := v.binBase[b]
	if !ok {
		base = len(v.bins)
		v.binBase[b] = base
		for _, g := range groups {
			v.bins = append(v.bins, visBin{mat: g.mat, vbo: g.vbo, nbo: g.nbo, lbo: g.lbo, ebo: g.ebo, cbo: g.cbo, ins: b.instBuf,
				baked: g.baked, shade: frameSurface(g.mat).prepass})
		}
	}
	ProfileBegin("nanite: vis")
	span := b.visSpan(b.compacted)
	if NaniteVisDense {
		v.ensureRecords(naniteVisMaxRecords)
	} else {
		v.ensureRecords(v.recordsUsed + span + sw)
	}
	b.uploadVisPassTable(groups)

	// Number the pass's commands.
	cmd := b.cmdBuf
	if b.compacted {
		cmd = b.packedBuf
	}
	n := &v.number
	gl.UseProgram(n.prog)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 5, cmd)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, v.records)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 2, b.visGroupBuf)
	gl.Uniform1ui(n.U("slots"), uint32(b.slots))
	gl.Uniform1ui(n.U("groupCount"), uint32(len(b.groups)))
	gl.Uniform1ui(n.U("recordBase"), uint32(v.recordsUsed))
	gl.Uniform1ui(n.U("binBase"), uint32(base))
	gl.Uniform1ui(n.U("dense"), uint32(boolToInt32(NaniteVisDense)))
	gl.Uniform1ui(n.U("packedCounts"), uint32(boolToInt32(b.compacted)))
	gl.Uniform1ui(n.U("maxRecords"), naniteVisRecordLimit)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 3, v.next)
	if b.compacted {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 4, b.countBuf)
	} else {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 4, v.dummy)
	}
	gl.DispatchCompute(uint32((b.slots+63)/64), 1, 1)
	// The commands, the records the ID draws read as attributes, and a
	// later pass's copy of the records when the list grows.
	gl.MemoryBarrier(gl.COMMAND_BARRIER_BIT | gl.VERTEX_ATTRIB_ARRAY_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT |
		gl.BUFFER_UPDATE_BARRIER_BIT)
	for i := uint32(0); i < 6; i++ {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, i, 0)
	}

	// The ID draws.
	p := &v.vis
	gl.UseProgram(p.prog)
	gl.UniformMatrix4fv(p.U("projection"), 1, false, &gfx.Projection[0])
	gl.UniformMatrix4fv(p.U("camera"), 1, false, &gfx.Cam.Matrix[0])
	gl.Uniform1i(p.U("instanced"), 1)
	gl.Uniform1i(p.U("tex"), 0)
	gl.Uniform1i(p.U("blendTex"), gfx.BlendTexUnit)
	gl.Uniform1i(p.U("blendMask"), gfx.BlendMaskUnit)
	gl.Uniform1i(p.U("alphaSharpen"), 1)
	gl.Uniform1i(p.U("coverageDither"), coverageDither())
	// The draw-distance fades, dithered here (nanite_draw_distance.go).
	gl.Uniform3f(p.U("drawEye"), gfx.Cam.Position[0], gfx.Cam.Position[1], gfx.Cam.Position[2])
	gl.Uniform1i(p.U("drawFadeOn"), boolToInt32(DrawDistances && !naniteDebugActive()))
	gl.Uniform1f(p.U("time"), gfx.MaterialScrollTime)
	// Never water: a water material is not prepassable, and is drawn the
	// forward way below.
	gl.Uniform1i(p.U("isWater"), 0)
	gfx.ApplySkinUniforms(&v.matSkin, p.prog, nil)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, b.instBuf)
	gl.StencilFunc(gl.ALWAYS, 1, 0xff)
	drawBuffersOnly(sceneAttachVisID)
	// The coverage state the groups set is set only where it differs from
	// the group before's: the uniforms are this program's own, but the
	// textures are bound to units anything since the last pass may have
	// rebound, so the record of them starts over every pass.
	v.cov = visCoverage{}
	gl.ActiveTexture(gl.TEXTURE0)
	// One indirect buffer for every group of the pass, and the records'
	// attributes set once for each run of groups drawn from one vertex array
	// (the cooked meshes' groups share theirs, and a compiled level's share
	// its pool's), not once a group: a group's own cost used to be thirty-odd
	// driver calls.
	b.bindIndirect()
	var bound visVAOState
	forward := false
	for gi := range groups {
		g := &groups[gi]
		if !b.groupLive(gi) {
			continue
		}
		fs := frameSurface(g.mat)
		if !fs.prepass {
			forward = true
			continue
		}
		v.bins[base+gi].drawn = true
		v.groupCoverage(&fs.st)
		bound.bind(g, v.records)
		b.drawGroupBound(gi)
	}
	bound.release()
	b.unbindIndirect()
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, 0)
	// The clusters the cull listed for the compute rasteriser, into the same
	// IDs and depth, their records after the commands'.
	if sw > 0 && len(groups) > 0 {
		naniteSW.rasterBatch(b, &groups[0], v.recordsUsed+span, base, sw)
	}
	drawBuffersColour()
	gl.StencilFunc(gl.ALWAYS, 0, 0xff)
	// What the frame's records could not number, the forward way, while the
	// frame is at the records' limit (nanite_vis_overflow.go).
	if naniteVisOverflow.overflowRisk() {
		ProfileBegin("nanite: vis overflow")
		naniteVisOverflow.drawOverflow(b, groups, cmd)
		ProfileEnd()
	}
	gl.UseProgram(gfx.Program)
	ProfileEnd()

	// What the material pass does not shade, the forward way.
	if forward {
		for gi := range groups {
			if b.groupLive(gi) && !frameSurface(groups[gi].mat).prepass && groups[gi].forward != nil {
				groups[gi].forward()
			}
		}
		gl.BindVertexArray(0)
	}
	v.recordsUsed += span + sw
	v.used = true
	return true
}

// visVAOState is the vertex array the ID pass has bound, with the records'
// attributes set on it, and whether its instance matrix is switched off.
type visVAOState struct {
	vao       uint32
	instanced bool
}

// bind readies g's vertex array for the ID draws, unless it is the one
// already readied: the instance matrix off (the ID draw's base instance is a
// record number, not an instance), the record attributes on.
func (s *visVAOState) bind(g *visGroup, records uint32) {
	if s.vao != 0 && s.vao == g.vao && s.instanced == g.instanced {
		return
	}
	s.release()
	gl.BindVertexArray(g.vao)
	if g.instanced {
		for k := uint32(0); k < 4; k++ {
			gl.DisableVertexAttribArray(gfx.AttribInstanceModel + k)
		}
	}
	gl.BindBuffer(gl.ARRAY_BUFFER, records)
	for k := uint32(0); k < 2; k++ {
		gl.EnableVertexAttribArray(naniteSlotAttrib + k)
		gl.VertexAttribIPointerWithOffset(naniteSlotAttrib+k, 4, gl.UNSIGNED_INT, naniteVisRecordBytes, uintptr(16*k))
		gl.VertexAttribDivisor(naniteSlotAttrib+k, 1)
	}
	s.vao, s.instanced = g.vao, g.instanced
}

// release puts the bound vertex array back as every other pass expects it:
// no record attributes, and the instance matrix on where it has one.
func (s *visVAOState) release() {
	if s.vao == 0 {
		return
	}
	for k := uint32(0); k < 2; k++ {
		gl.DisableVertexAttribArray(naniteSlotAttrib + k)
		gl.VertexAttribDivisor(naniteSlotAttrib+k, 0)
	}
	if s.instanced {
		for k := uint32(0); k < 4; k++ {
			gl.EnableVertexAttribArray(gfx.AttribInstanceModel + k)
		}
	}
	*s = visVAOState{}
}

// visCoverage is the coverage state the ID pass last set: its uniforms, and
// the textures on unit 0 and the blend pair's units. valid is false at the
// start of a pass, when nothing is known.
type visCoverage struct {
	valid                     bool
	alpha, cutoff, uvRot      float32
	normalColor, hasBlend     int32
	uvOff, uvScaleBias        [2]float32
	tex, blendTex, blendMask  uint32
	texSet, blendSet, maskSet bool
}

// groupCoverage sets the ID pass's coverage uniforms for a group in a
// material of state st — what decides whether a fragment is the surface's
// (nanite_prepass.go's material, which this mirrors) — wherever they differ
// from what is already set. Texture unit 0 is active on entry and on return.
func (v *naniteVisPass) groupCoverage(st *gfx.SurfaceState) {
	p := &v.vis
	c := &v.cov
	first := !c.valid
	c.valid = true
	if first || c.alpha != st.Alpha {
		c.alpha = st.Alpha
		gl.Uniform1f(p.U("alpha"), st.Alpha)
	}
	if first || c.cutoff != st.CutoffBias {
		c.cutoff = st.CutoffBias
		gl.Uniform1f(p.U("alphaCutoffBias"), st.CutoffBias)
	}
	if nc := st.NormalColorFlag(); first || c.normalColor != nc {
		c.normalColor = nc
		gl.Uniform1i(p.U("normalColor"), nc)
	}
	if off := [2]float32{st.UVOffset.X(), st.UVOffset.Y()}; first || c.uvOff != off {
		c.uvOff = off
		gl.Uniform2f(p.U("uvOffset"), off[0], off[1])
	}
	if sb := [2]float32{st.UVScale.X() - 1, st.UVScale.Y() - 1}; first || c.uvScaleBias != sb {
		c.uvScaleBias = sb
		gl.Uniform2f(p.U("uvScaleBias"), sb[0], sb[1])
	}
	if first || c.uvRot != st.UVRotation {
		c.uvRot = st.UVRotation
		gl.Uniform1f(p.U("uvRotation"), st.UVRotation)
	}
	hasBlend := st.BlendTexture != "" && st.BlendMask != ""
	if hb := boolToInt32(hasBlend); first || c.hasBlend != hb {
		c.hasBlend = hb
		gl.Uniform1i(p.U("hasBlend"), hb)
	}
	if hasBlend {
		if t := gfx.Textures[st.BlendTexture]; !c.blendSet || c.blendTex != t {
			c.blendTex, c.blendSet = t, true
			gl.ActiveTexture(gl.TEXTURE0 + gfx.BlendTexUnit)
			gl.BindTexture(gl.TEXTURE_2D, t)
			gl.ActiveTexture(gl.TEXTURE0)
		}
		if t := gfx.Textures[st.BlendMask]; !c.maskSet || c.blendMask != t {
			c.blendMask, c.maskSet = t, true
			gl.ActiveTexture(gl.TEXTURE0 + gfx.BlendMaskUnit)
			gl.BindTexture(gl.TEXTURE_2D, t)
			gl.ActiveTexture(gl.TEXTURE0)
		}
	}
	if t := gfx.Textures[st.Texture]; !c.texSet || c.tex != t {
		c.tex, c.texSet = t, true
		gl.BindTexture(gl.TEXTURE_2D, t)
	}
}

// ensureRecords grows the record buffer to hold n, keeping what this frame
// already numbered (earlier passes' records are read by the material pass).
func (v *naniteVisPass) ensureRecords(n int) {
	if n <= v.recordCap && v.records != 0 {
		return
	}
	cap := max(n+n/2, 1<<16)
	var buf uint32
	gl.GenBuffers(1, &buf)
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, buf)
	gl.BufferData(gl.COPY_WRITE_BUFFER, cap*naniteVisRecordBytes, nil, gl.DYNAMIC_COPY)
	if v.records != 0 {
		if v.recordsUsed > 0 {
			gl.BindBuffer(gl.COPY_READ_BUFFER, v.records)
			gl.CopyBufferSubData(gl.COPY_READ_BUFFER, gl.COPY_WRITE_BUFFER, 0, 0, v.recordsUsed*naniteVisRecordBytes)
			gl.BindBuffer(gl.COPY_READ_BUFFER, 0)
		}
		gl.DeleteBuffers(1, &v.records)
	}
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
	v.records, v.recordCap = buf, cap
}

func (v *naniteVisPass) warn(format string, args ...any) {
	if v.warned {
		return
	}
	v.warned = true
	log.Printf("nanite: visibility buffer: "+format+" in a frame — the rest is drawn the forward way", args...)
}

// visSpan is how many records one pass of b takes: every command slot, or —
// packed, with a bound (cullBatch.drawMax) — only as many as the bound lets
// it have. chicago's props are 856,184 slots, so two camera passes numbered
// slot by slot overran the 2^20 an ID can name and the second pass went the
// forward way every frame.
func (b *cullBatch) visSpan(packed bool) int {
	if !packed || b.drawMax == nil {
		return b.slots
	}
	n := 0
	for gi, g := range b.groups {
		n += int(min(g.count, b.drawMax[gi]))
	}
	return n
}

// uploadVisPassTable writes this pass's group table for the numbering pass
// (nanite_vis_number_comp.glsl): each group's first slot, how many slots it
// numbers, whether it is drawn, and its first record — packed records when
// the pass is bounded, its slot otherwise. Into the buffer visGroupsFit made.
func (b *cullBatch) uploadVisPassTable(groups []visGroup) {
	bounded := b.compacted && b.drawMax != nil
	table := make([]uint32, 0, 4*len(groups))
	off := uint32(0)
	for gi, g := range groups {
		first, count := uint32(b.groups[gi].first), uint32(b.groups[gi].count)
		rec := first
		if bounded {
			count = uint32(min(b.groups[gi].count, b.drawMax[gi]))
			rec = off
			off += count
		}
		table = append(table, first, count, uint32(boolToInt32(frameSurface(g.mat).prepass)), rec)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.visGroupBuf)
	gl.BufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(table)*4, gl.Ptr(table))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

// visGroupsFit uploads the batch's group table for the numbering pass when
// its groups changed, and reports whether every buffer the material pass
// would read is within the driver's largest storage block.
func (b *cullBatch) visGroupsFit(groups []visGroup, maxBlock int64) bool {
	sig := uint64(14695981039346656037)
	for gi, g := range groups {
		sig = mix(sig, uint64(b.groups[gi].first)<<32|uint64(b.groups[gi].count))
		sig = mix(sig, uint64(g.vbo)<<32|uint64(g.ebo))
		sig = mix(sig, uint64(g.nbo)<<32|uint64(g.lbo))
		sig = mix(sig, uint64(g.cbo))
		sig = mix(sig, uint64(boolToInt32(frameSurface(g.mat).prepass)))
	}
	if b.visGroupBuf != 0 && sig == b.visGroupSig {
		return b.visFits
	}
	b.visGroupSig = sig
	table := make([]uint32, 0, 4*len(groups))
	for gi, g := range groups {
		table = append(table, uint32(b.groups[gi].first), uint32(b.groups[gi].count), uint32(boolToInt32(frameSurface(g.mat).prepass)), uint32(b.groups[gi].first))
	}
	if b.visGroupBuf == 0 {
		gl.GenBuffers(1, &b.visGroupBuf)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.visGroupBuf)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, max(len(table), 4)*4, gl.Ptr(append(table, 0, 0, 0, 0)), gl.STATIC_DRAW)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	// The sizes, asked once a change rather than a frame.
	b.visFits = true
	size := func(buf uint32) int64 {
		if buf == 0 {
			return 0
		}
		var n int64
		gl.BindBuffer(gl.COPY_READ_BUFFER, buf)
		gl.GetBufferParameteri64v(gl.COPY_READ_BUFFER, gl.BUFFER_SIZE, &n)
		gl.BindBuffer(gl.COPY_READ_BUFFER, 0)
		return n
	}
	seen := map[uint32]bool{}
	for _, g := range groups {
		for _, buf := range []uint32{g.vbo, g.nbo, g.lbo, g.ebo, g.cbo} {
			if buf == 0 || seen[buf] {
				continue
			}
			seen[buf] = true
			if maxBlock > 0 && size(buf) > maxBlock {
				b.visFits = false
			}
		}
	}
	return b.visFits
}

// naniteVisShade is the material pass: after the cull's last pass of the
// frame (naniteOcclusionPass), before anything else draws over the opaque
// world. frame is the frame's shadows, now the time its clock.
func naniteVisShade(lvl *world.Level, frame ShadowFrame, now float64) {
	v := &naniteVis
	if !v.active {
		return
	}
	defer func() {
		gl.Disable(gl.STENCIL_TEST)
		gl.StencilFunc(gl.ALWAYS, 0, 0xff)
		gl.StencilOp(gl.KEEP, gl.KEEP, gl.KEEP)
		v.active = false
	}()
	if !v.used || len(v.bins) == 0 {
		return
	}
	ProfileBegin("nanite: material")
	defer ProfileEnd()
	w, h := scene.w, scene.h
	v.ensureFBOs()
	tilesX, tilesY := (w+naniteVisTile-1)/naniteVisTile, (h+naniteVisTile-1)/naniteVisTile
	v.ensureBins(len(v.bins), int(tilesX*tilesY))

	// 1. Each pixel's bin, where the stencil says the ID pass still owns it.
	ProfileBegin("nanite: classify")
	gl.BindFramebuffer(gl.FRAMEBUFFER, v.classifyFBO)
	gl.Viewport(0, 0, w, h)
	gl.Disable(gl.DEPTH_TEST)
	gl.DepthMask(false)
	gl.StencilFunc(gl.EQUAL, 1, 0xff)
	gl.StencilOp(gl.KEEP, gl.KEEP, gl.KEEP)
	c := &v.classify
	gl.UseProgram(c.prog)
	gl.ActiveTexture(gl.TEXTURE0 + naniteVisIDUnit)
	gl.BindTexture(gl.TEXTURE_2D, scene.visID)
	gl.Uniform1i(c.U("visIDs"), naniteVisIDUnit)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, v.records)
	gl.BindVertexArray(v.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	gl.Disable(gl.STENCIL_TEST)

	// 2. Each bin's tiles.
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 3, v.binDraws)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 6, v.tileLists)
	r := &v.reset
	gl.UseProgram(r.prog)
	gl.Uniform1ui(r.U("binCount"), uint32(len(v.bins)))
	gl.DispatchCompute(uint32((len(v.bins)+63)/64), 1, 1)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT | gl.FRAMEBUFFER_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT)
	t := &v.tiles
	gl.UseProgram(t.prog)
	gl.ActiveTexture(gl.TEXTURE0 + naniteVisBinUnit)
	gl.BindTexture(gl.TEXTURE_2D, scene.bin)
	gl.Uniform1i(t.U("bins"), naniteVisBinUnit)
	gl.Uniform2i(t.U("size"), w, h)
	gl.Uniform1ui(t.U("binCount"), uint32(len(v.bins)))
	gl.Uniform1ui(t.U("tilesX"), uint32(tilesX))
	gl.Uniform1ui(t.U("tileCap"), uint32(tilesX*tilesY))
	gl.DispatchCompute(uint32(tilesX), uint32(tilesY), 1)
	gl.MemoryBarrier(gl.COMMAND_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT)
	ProfileEnd()

	// 3. The bins as depth, for the hardware to pick each bin's pixels.
	gl.BindFramebuffer(gl.FRAMEBUFFER, v.binDepthFBO)
	gl.Viewport(0, 0, w, h)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthFunc(gl.ALWAYS)
	gl.DepthMask(true)
	gl.ClearBufferfv(gl.DEPTH, 0, &[]float32{1}[0])
	bd := &v.binDepthProg
	gl.UseProgram(bd.prog)
	gl.Uniform1i(bd.U("bins"), naniteVisBinUnit)
	gl.BindVertexArray(v.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)

	// GI's probe light at half resolution, from the depth now final
	// (gi_halfres.go).
	giHalfResRun()

	// 4. Each bin's pixels, shaded: its tiles at its depth, GL_EQUAL.
	gl.BindFramebuffer(gl.FRAMEBUFFER, v.colourFBO)
	gl.Viewport(0, 0, w, h)
	gl.DepthFunc(gl.EQUAL)
	gl.DepthMask(false)
	// A program's frame uniforms, the first time this frame shades with it
	// (each bin's variant, nanite_vis_variants.go).
	ready := func(m *visMatProg) {
		if m.frame == drawFrameNo {
			gl.UseProgram(m.prog)
			return
		}
		m.frame = drawFrameNo
		v.frameUniforms(m.prog, lvl, frame, now)
		giHalfResBindTo(m.prog)
		gl.UseProgram(m.prog)
		gl.Uniform1i(m.U("visIDs"), naniteVisIDUnit)
		gl.Uniform1i(m.U("binIDs"), naniteVisBinUnit)
		gl.Uniform2f(m.U("viewSize"), float32(w), float32(h))
		gl.Uniform1f(m.U("derivScale"), upscaleDerivScale())
		gl.Uniform1ui(m.U("tileCap"), uint32(tilesX*tilesY))
		gl.Uniform1ui(m.U("tilesX"), uint32(tilesX))
	}
	gl.ActiveTexture(gl.TEXTURE0 + naniteVisIDUnit)
	gl.BindTexture(gl.TEXTURE_2D, scene.visID)
	gl.ActiveTexture(gl.TEXTURE0 + naniteVisBinUnit)
	gl.BindTexture(gl.TEXTURE_2D, scene.bin)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, v.records)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 6, v.tileLists)
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, v.binDraws)
	gl.BindVertexArray(v.vao)
	shaded := 0
	var query uint32
	if naniteVisCountShading {
		// How many pixels the shading ran for (a sample passes only when
		// the bin test did not discard it), for the tests.
		gl.GenQueries(1, &query)
		gl.BeginQuery(gl.SAMPLES_PASSED, query)
	}
	// Each bin's pixels are its own, so the order the bins are shaded in
	// changes nothing on screen; they go in the order that changes the least
	// state between them — by material, then by buffers — and what a bin
	// shares with the one before (its material's every uniform and texture,
	// its flags, its buffers) is not set again.
	v.order = v.order[:0]
	frameKey := visFrameKey(lvl, frame)
	v.warmMaterialVariants(lvl, frameKey)
	v.binKeys = slices.Grow(v.binKeys[:0], len(v.bins))[:len(v.bins)]
	for bi := range v.bins {
		if bn := &v.bins[bi]; bn.shade && bn.drawn {
			v.order = append(v.order, bi)
			st := &frameSurface(bn.mat).st
			v.binKeys[bi] = uint64(visShadeKey(st, bn.baked)|frameKey) | visSplatLayout(st)
		}
	}
	slices.SortStableFunc(v.order, func(a, b int) int {
		// One program at a time: the variant first.
		if c := cmp.Compare(v.binKeys[a], v.binKeys[b]); c != 0 {
			return c
		}
		return v.binLess(a, b)
	})
	var bound [8]uint32
	bind := func(i int, buf uint32) {
		if bound[i] != buf {
			bound[i] = buf
			gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, uint32(i), buf)
		}
	}
	// What the loop leaves bound and set, for the next bin to compare with:
	// a copy, since outside a frame frameSurface answers in one scratch state.
	var applied gfx.SurfaceState
	appliedOK := false
	baked, hasNormals, hasLight := int32(-1), int32(-1), int32(-1)
	gl.ActiveTexture(gl.TEXTURE0)
	var m *visMatProg
	keyOpen := false
	for _, bi := range v.order {
		bn := &v.bins[bi]
		st := &frameSurface(bn.mat).st
		if p := v.materialProgram(v.binKeys[bi]); p != m {
			// A new program: nothing the last one was sent carries over.
			if naniteMatProfileKeys {
				// Each variant's share of the pass, for the F3 profile
				// and the bench (FOUNDRY_NANITE_MAT_PROFILE=1).
				if keyOpen {
					ProfileEnd()
				}
				ProfileBegin(fmt.Sprintf("mat %#06x", v.binKeys[bi]&0xffffffff))
				keyOpen = true
			}
			m = p
			ready(m)
			appliedOK = false
			baked, hasNormals, hasLight = -1, -1, -1
		}
		if !appliedOK || applied != *st {
			st.ApplyLocs(&m.locs)
			gl.ActiveTexture(gl.TEXTURE0)
			gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[st.Texture])
			applied, appliedOK = *st, true
		}
		if f := boolToInt32(bn.baked); f != baked {
			baked = f
			gl.Uniform1i(m.baked, f)
		}
		if f := boolToInt32(bn.nbo != 0); f != hasNormals {
			hasNormals = f
			gl.Uniform1i(m.hasNormals, f)
		}
		if f := boolToInt32(bn.lbo != 0); f != hasLight {
			hasLight = f
			gl.Uniform1i(m.hasLight, f)
		}
		gl.Uniform1ui(m.binLoc, uint32(bi))
		bind(0, bn.ins)
		bind(2, bn.ebo)
		bind(3, bn.vbo)
		bind(4, orDummy(bn.nbo, v.dummy))
		bind(5, orDummy(bn.lbo, v.dummy))
		// Binding 6 is the tile lists, bound above for every bin.
		bind(7, orDummy(bn.cbo, v.dummy))
		gl.DrawArraysIndirect(gl.TRIANGLES, gl.PtrOffset(bi*16))
		shaded++
	}
	if keyOpen {
		ProfileEnd()
	}
	DebugStats.NaniteVisBins = shaded
	if query != 0 {
		gl.EndQuery(gl.SAMPLES_PASSED)
		gl.Finish()
		var n uint32
		gl.GetQueryObjectuiv(query, gl.QUERY_RESULT, &n)
		gl.DeleteQueries(1, &query)
		naniteVisShadedPixels = int(n)
	}
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, 0)
	for i := uint32(0); i < 8; i++ {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, i, 0)
	}
	gl.ActiveTexture(gl.TEXTURE0 + naniteVisIDUnit)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.ActiveTexture(gl.TEXTURE0 + naniteVisBinUnit)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindVertexArray(0)

	gl.BindFramebuffer(gl.FRAMEBUFFER, scene.fbo)
	gl.Viewport(0, 0, w, h)
	drawBuffersColour()
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthFunc(gl.LEQUAL)
	gl.DepthMask(true)
	gl.UseProgram(gfx.Program)
}

// binLess orders the material pass's bins: by texture, so bins of one
// material come together, then by vertex buffer, so bins of one batch do.
func (v *naniteVisPass) binLess(a, b int) int {
	x, y := &v.bins[a], &v.bins[b]
	if x.mat != y.mat {
		// One at a time: outside a frame frameSurface answers in one
		// scratch state.
		tx := frameSurface(x.mat).st.Texture
		ty := frameSurface(y.mat).st.Texture
		if c := strings.Compare(tx, ty); c != 0 {
			return c
		}
	}
	if x.vbo != y.vbo {
		return cmp.Compare(x.vbo, y.vbo)
	}
	return cmp.Compare(a, b)
}

func orDummy(buf, dummy uint32) uint32 {
	if buf == 0 {
		return dummy
	}
	return buf
}

// frameUniforms sends the material program what fragment.glsl reads that is
// not per material: the matrices, the lights, the shadows (the cascades, the
// clouds, live GI: UploadShadowUniformsTo hands on all three), the point
// lights' cubes and the fog — the calls the world program's own uploads are.
func (v *naniteVisPass) frameUniforms(p uint32, lvl *world.Level, frame ShadowFrame, now float64) {
	lights := world.SceneLights(lvl)
	world.UploadLightsTo(p, lights, false)
	world.UploadFogTo(p, frameFog(lvl), frameVolumes(lvl), lvl)
	ambTilesBindTo(p)
	UploadShadowUniformsTo(p, lvl.Lights, frame)
	UploadPointShadowUniformsTo(p, lights)
	// The caustics on a bed under water (water_caustics.go).
	uploadWaterCausticsTo(p)
	gl.UseProgram(p)
	loc := func(name string) int32 { return gfx.UniformLoc(p, name+"\x00") }
	gl.UniformMatrix4fv(loc("projection"), 1, false, &gfx.Projection[0])
	gl.UniformMatrix4fv(loc("camera"), 1, false, &gfx.Cam.Matrix[0])
	gl.Uniform3f(loc("camPos"), gfx.Cam.Position.X(), gfx.Cam.Position.Y(), gfx.Cam.Position.Z())
	gl.Uniform1f(loc("time"), float32(now))
	gl.Uniform1i(loc("instanced"), 1)
	gl.Uniform1i(loc("alphaSharpen"), 0)
	gl.Uniform1i(loc("coverageDither"), 0)
	gl.Uniform1i(loc("tex"), 0)
	// Each material program its own cache: one shared by the variants looked
	// both locations up again at every switch of program, every frame.
	sk := v.matSkins[p]
	if sk == nil {
		if v.matSkins == nil {
			v.matSkins = map[uint32]*gfx.SkinUniforms{}
		}
		sk = &gfx.SkinUniforms{}
		v.matSkins[p] = sk
	}
	gfx.ApplySkinUniforms(sk, p, nil)
	ident := mgl32.Ident4()
	gl.UniformMatrix4fv(loc("model"), 1, false, &ident[0])
}

// ensureFBOs points the material pass's framebuffers at the scene target's
// current attachments.
func (v *naniteVisPass) ensureFBOs() {
	if v.classifyFBO == 0 {
		gl.GenFramebuffers(1, &v.classifyFBO)
		gl.GenFramebuffers(1, &v.colourFBO)
	}
	if v.fboGen == scene.gen {
		return
	}
	v.fboGen = scene.gen
	if v.binDepth != 0 {
		gl.DeleteTextures(1, &v.binDepth)
	}
	if v.binDepthFBO == 0 {
		gl.GenFramebuffers(1, &v.binDepthFBO)
	}
	gl.GenTextures(1, &v.binDepth)
	gl.BindTexture(gl.TEXTURE_2D, v.binDepth)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.DEPTH_COMPONENT32F, scene.w, scene.h, 0, gl.DEPTH_COMPONENT, gl.FLOAT, nil)
	setNearestClamp()
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.BindFramebuffer(gl.FRAMEBUFFER, v.binDepthFBO)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.TEXTURE_2D, v.binDepth, 0)
	gl.DrawBuffer(gl.NONE)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("nanite: visibility buffer: bin depth framebuffer incomplete (0x%x)", st)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, v.classifyFBO)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, scene.bin, 0)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_STENCIL_ATTACHMENT, gl.TEXTURE_2D, scene.depth, 0)
	gl.DrawBuffer(gl.COLOR_ATTACHMENT0)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("nanite: visibility buffer: bin framebuffer incomplete (0x%x)", st)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, v.colourFBO)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, scene.colour, 0)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.TEXTURE_2D, v.binDepth, 0)
	gl.DrawBuffer(gl.COLOR_ATTACHMENT0)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("nanite: visibility buffer: colour framebuffer incomplete (0x%x)", st)
	}
}

// ensureBins sizes the bins' indirect draws and tile lists.
func (v *naniteVisPass) ensureBins(bins, tiles int) {
	if v.binDraws == 0 {
		gl.GenBuffers(1, &v.binDraws)
		gl.GenBuffers(1, &v.tileLists)
	}
	if bins > v.binCap {
		v.binCap = max(bins, 256)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.binDraws)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, v.binCap*16, nil, gl.DYNAMIC_COPY)
	}
	if need := max(bins, 256) * tiles; need > v.tileListCap {
		v.tileListCap = need
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, v.tileLists)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, need*4, nil, gl.DYNAMIC_COPY)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}
