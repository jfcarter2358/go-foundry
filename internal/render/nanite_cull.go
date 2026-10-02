package render

// nanite_cull.go — the GPU-driven cluster cull. Phase 1 of the Nanite plan
// (docs/nanite/PHASE_1.md): components 1 and 6, and the first part of 5.
//
// What is culled is a CLUSTER (internal/cluster): ~128 neighbouring triangles
// with a sphere and a normal cone. A compiled level's surfaces are cut into
// clusters by the compiler (world/static_clusters.go) and a cooked Nanite
// mesh carries its own (internal/nanite); either way the clusters of one draw
// group are contiguous runs of that group's index buffer, so a visible cluster
// is one DrawElementsIndirectCommand and a group's visible clusters are one
// glMultiDrawElementsIndirect.
//
// # A batch
//
// A cullBatch is what one pass culls and draws: a list of INSTANCES (a
// placement's matrix and the sphere its mesh fits in — a compiled level is one
// instance, the identity, marked always-visible), a list of TILES (a run of one
// instance's clusters, and the command slots it writes), and the GROUPS those
// slots fall into, one per material. The cull is two compute stages:
//
//  1. nanite_instance_cull_comp.glsl — a thread per instance: its sphere
//     against the frustum and, in the occlusion pass, the Hi-Z pyramid.
//  2. nanite_cluster_cull_comp.glsl — a workgroup per tile, a thread per
//     cluster: the instance's verdict, the cluster's sphere against the
//     frustum, its normal cone, and the Hi-Z pyramid. Every slot is written,
//     a culled cluster as a zero count, so the command list is never cleared
//     — and since the slots are fixed, the draw order is the same every
//     frame, which is what keeps a capture deterministic.
//
// Then a merge pass joins each run of neighbouring survivors into one command
// (nanite_merge_comp.glsl) and, where the driver has the indirect-count draw
// and the pass expects mostly empty slots, a packing pass moves the survivors
// to the front of each group (compactCommands); then one indirect multi-draw
// per group. Nothing is read back (outside FOUNDRY_NANITE_STATS).
//
// # Two-pass occlusion
//
// The camera's view is drawn in two passes (component 6). Pass 1, at the top
// of the opaque world, draws only the clusters that were visible LAST frame
// and still pass the frustum and cone. The rest of the opaque world draws
// over it; then the Hi-Z pyramid is built from the depth so far (hiz.go) and
// pass 2 tests every cluster against it, draws the ones pass 1 did not, and
// records which were visible for the next frame. A cluster hidden behind a
// wall is never drawn; one that comes into view is drawn the frame it does,
// in pass 2, never a frame late. On the first frame, or after a cut, pass 1
// draws nothing and pass 2 everything visible — slower for a frame, never
// wrong.
//
// Shadow passes cull with the light's frustum (CULL_PLAIN), never the camera's
// occlusion: a caster the camera cannot see still casts. They draw back faces,
// so the normal cone is tested there only on placements of a CLOSED cooked
// mesh (instShadowCone, internal/nanite/closed.go), whose clusters turned
// wholly away from the light can never be the nearest depth; the cone's eye
// is read off the light's own frustum (frustumEye) — the light's position for
// a point light's face, the direction its rays run for a sun cascade.
//
// # Switches
//
// Every piece can be turned off, and the old path stays as the fallback:
//
//	FOUNDRY_NANITE_CLUSTERS=0   compiled surfaces culled on the CPU, as before
//	FOUNDRY_NANITE_OCCLUSION=0  one pass, frustum and cone only
//	FOUNDRY_NANITE_OCCLUSION_BRUSHES=1  two passes for compiled surfaces and
//	                            chunk brushes too (off: see NaniteOcclusionBrushes)
//	FOUNDRY_NANITE_PREPASS=0    no depth prepass (nanite_prepass.go)
//	FOUNDRY_NANITE_COMPACT=0    draw the fixed command list even where the
//	                            indirect-count draw is available
//	FOUNDRY_NANITE_CONE=0       no normal-cone test
//	FOUNDRY_NANITE_SHADOW_CONE=0  no normal-cone test in shadow passes
//	FOUNDRY_NANITE_SHADOWS=0    shadow passes cull compiled surfaces on the CPU
//
// and a driver that will not build the compute programs gets the CPU path
// without being asked, with a line in the log.

import (
	"fmt"
	"log"
	"math"
	"os"
	"unsafe"

	"foundry/internal/cluster"
	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// The switches; see the file comment. Read at startup, and settable by tests
// and benchmarks between frames.
var (
	NaniteClusters  = gfx.EnvFlag("FOUNDRY_NANITE_CLUSTERS", true)
	NaniteOcclusion = gfx.EnvFlag("FOUNDRY_NANITE_OCCLUSION", true)
	// NaniteOcclusionBrushes extends the two-pass occlusion to a compiled
	// level's surfaces and to chunk brushes. Off by default, on measurement:
	// a level's groups are many and its triangles few, and the second pass's
	// bind and draw per group cost more GPU time than the clusters it saved
	// (docs/nanite/PHASE_1.md has the numbers). Cooked Nanite meshes — dense,
	// few materials — take the occlusion whenever NaniteOcclusion is on.
	NaniteOcclusionBrushes = gfx.EnvFlag("FOUNDRY_NANITE_OCCLUSION_BRUSHES", false)
	NaniteCone             = gfx.EnvFlag("FOUNDRY_NANITE_CONE", true)
	// NaniteShadowCone tests the normal cone in shadow passes too, on
	// placements of closed cooked meshes only (see the file comment). The
	// shadow map is the same texel for texel; the triangles drawn into it
	// are fewer.
	NaniteShadowCone = gfx.EnvFlag("FOUNDRY_NANITE_SHADOW_CONE", true)
	NaniteShadows    = gfx.EnvFlag("FOUNDRY_NANITE_SHADOWS", true)
	// NaniteCompact packs each pass's surviving commands and draws them by
	// count (glMultiDrawElementsIndirectCount), where the driver has it; off,
	// or without it, the fixed list is drawn, culled commands and all.
	NaniteCompact = gfx.EnvFlag("FOUNDRY_NANITE_COMPACT", true)
	// NaniteStats reads each camera pass's commands back to count the
	// triangles drawn, for the F3 overlay (DebugStats.NaniteTriangles). A
	// readback is a stall, so it is off unless FOUNDRY_NANITE_STATS=1.
	NaniteStats = gfx.EnvFlag("FOUNDRY_NANITE_STATS", false)
)

// The cull modes, as nanite_cull_common.glsl numbers them.
const (
	cullPlain = 0
	cullPass1 = 1
	cullPass2 = 2
)

// What the normal-cone test applies to, as nanite_cull_common.glsl numbers
// it (useCone).
const (
	coneNone   = 0
	coneAll    = 1 // every instance: a pass that culls back faces
	coneClosed = 2 // instances flagged instShadowCone: a shadow pass
)

// naniteHiZUnit is the texture unit the cull reads the Hi-Z pyramid on, and
// the pyramid's reduction its source: the foliage cull's, which is free
// outside the foliage pass.
const naniteHiZUnit = foliageHiZUnit

// naniteTileClusters is the most clusters one tile (one workgroup) takes.
const naniteTileClusters = 256

// gpuCluster is a cluster as nanite_cluster_cull_comp.glsl reads it (std430,
// 64 bytes).
type gpuCluster struct {
	Sphere [4]float32 // local centre, radius
	Apex   [4]float32
	Cone   [4]float32 // axis, cutoff (>= 1: none)
	Draw   [4]uint32  // firstIndex, indexCount, baseVertex, unused
}

// gpuTile is a run of one instance's clusters (std430, 32 bytes): a = first
// cluster, count, instance, first command slot; b.x = first history entry.
type gpuTile struct {
	A, B [4]uint32
}

// gpuInstance is an instance (std430, 96 bytes). Its first 64 bytes are also
// the per-instance model matrix the vertex stage reads at locations 7-10, so
// one buffer serves the cull and the draw.
type gpuInstance struct {
	Model  [16]float32
	Sphere [4]float32
	Flags  [4]uint32
}

const (
	instAlwaysVisible = 1 << 0
	instNoCone        = 1 << 1
	// instShadowCone: the instance's mesh is closed, so a shadow pass may
	// cone-test its clusters (coneClosed).
	instShadowCone = 1 << 2
)

// gpuInstanceStride is gpuInstance's size, the vertex attribute stride of its
// matrix.
const gpuInstanceStride = int32(unsafe.Sizeof(gpuInstance{}))

// identityInstance is the instance a batch of world-space geometry culls
// under: always visible, its clusters tested one by one.
func identityInstance() gpuInstance {
	return gpuInstance{Model: mgl32.Ident4(), Flags: [4]uint32{instAlwaysVisible}}
}

// placedInstance is an instance of a mesh whose clusters fit in the sphere
// (centre, radius) of its own space, placed by m.
func placedInstance(m mgl32.Mat4, centre mgl32.Vec3, radius float32) gpuInstance {
	in := gpuInstance{Model: m, Sphere: [4]float32{centre[0], centre[1], centre[2], radius}}
	if !coneSafe(m) {
		in.Flags[0] |= instNoCone
	}
	return in
}

// coneSafe is whether a cluster's normal cone, carried through m, still says
// where its back faces are: m must turn and scale evenly, without a mirror.
// A mirror turns every triangle's winding round; an uneven scale bends the
// normals off the cone's axis.
func coneSafe(m mgl32.Mat4) bool {
	m3 := m.Mat3()
	if m3.Det() <= 0 {
		return false
	}
	a, b, c := m3.Col(0).Len(), m3.Col(1).Len(), m3.Col(2).Len()
	hi, lo := max(a, b, c), min(a, b, c)
	if lo <= 0 || hi/lo > 1.001 {
		return false
	}
	// And square: columns at right angles.
	x, y, z := m3.Col(0).Mul(1/a), m3.Col(1).Mul(1/b), m3.Col(2).Mul(1/c)
	return math.Abs(float64(x.Dot(y))) < 1e-3 && math.Abs(float64(y.Dot(z))) < 1e-3 && math.Abs(float64(x.Dot(z))) < 1e-3
}

// toGPUCluster is a cluster as the shader reads it, its draw range offset by
// firstIndex (indices) and baseVertex.
func toGPUCluster(c *cluster.Cluster, firstIndex uint32, baseVertex int32) gpuCluster {
	g := gpuCluster{
		Sphere: [4]float32{c.Center[0], c.Center[1], c.Center[2], c.Radius},
		Apex:   [4]float32{c.ConeApex[0], c.ConeApex[1], c.ConeApex[2], 0},
		Cone:   [4]float32{c.ConeAxis[0], c.ConeAxis[1], c.ConeAxis[2], c.ConeCutoff},
		Draw:   [4]uint32{firstIndex + 3*c.TriFirst, 3 * c.TriCount, uint32(baseVertex), 0},
	}
	return g
}

// cullGroup is one material's run of a batch's command slots.
type cullGroup struct {
	first, count int32
}

// cullBatch is what one pass culls; see the file comment.
type cullBatch struct {
	// clusters is the SSBO the tiles index — not the batch's own: a compiled
	// level's batches and the Nanite mesh pool each keep one.
	clusters uint32

	tiles      []gpuTile
	tilesDirty bool
	insts      []gpuInstance
	instDirty  bool
	groups     []cullGroup
	// slots is how many commands the tiles write.
	slots int

	tileBuf, instBuf, visBuf, histBuf, cmdBuf uint32
	histLen                                   int
	// The sizes the per-pass stores were last given (passStore).
	cmdSize, packedSize, countSize int

	// The compacted form of a pass's commands (compactable): packed holds
	// each group's survivors at the front of its run, counts how many, and
	// groupBuf the runs. compacted says this pass's draws read them.
	packedBuf, countBuf, groupBuf uint32
	groupsDirty                   bool
	compacted                     bool
	// tileCountBuf is each tile's survivor count, which the cluster cull
	// writes; tileOffBuf each tile's place in its group, which the packing's
	// scan writes.
	tileCountBuf, tileOffBuf uint32

	// live, when set, is per group whether the CPU found any of its
	// candidates in the frustum this frame (markLive): a group with none
	// is not bound or drawn at all. Its clusters are still culled on the GPU
	// — they would come out culled anyway — but the bind and the indirect
	// draw are a group's real cost, and a level of a thousand groups pays it
	// a thousand times.
	live []bool
}

// groupLive is whether group gi is drawn this pass.
func (b *cullBatch) groupLive(gi int) bool { return b.live == nil || (gi < len(b.live) && b.live[gi]) }

// markLive sets live from the clusters' spheres: a group is live when any
// cluster in its tiles, carried by model, touches the frustum. spheres is
// indexed as the tiles' clusters are.
func (b *cullBatch) markLive(spheres [][4]float32, model mgl32.Mat4, frustum *Frustum) {
	if len(b.live) != len(b.groups) {
		b.live = make([]bool, len(b.groups))
	}
	scale := float32(math.Sqrt(float64(max(model.Col(0).Vec3().LenSqr(), model.Col(1).Vec3().LenSqr(), model.Col(2).Vec3().LenSqr()))))
	ti := 0
	for gi, g := range b.groups {
		b.live[gi] = false
		end := g.first + g.count
		for ; ti < len(b.tiles) && int32(b.tiles[ti].A[3]) < end; ti++ {
			if b.live[gi] {
				continue
			}
			t := &b.tiles[ti]
			for k := uint32(0); k < t.A[1]; k++ {
				sp := spheres[t.A[0]+k]
				c := model.Mul4x1(mgl32.Vec4{sp[0], sp[1], sp[2], 1}).Vec3()
				if frustum == nil || sphereInFrustum(*frustum, c, sp[3]*scale) {
					b.live[gi] = true
					break
				}
			}
		}
	}
}

// sphereInFrustum is the cull shaders' inFrustum, on the CPU.
func sphereInFrustum(f Frustum, c mgl32.Vec3, r float32) bool {
	for _, p := range f.Planes {
		n := p.Vec3()
		if n.Dot(c)+p[3] < -r*n.Len() {
			return false
		}
	}
	return true
}

// cullView is what a pass culls against.
type cullView struct {
	planes   [6]mgl32.Vec4
	projView mgl32.Mat4
	// eye is where the cone is tested from: a position (w 1) or, for an
	// orthographic light, the direction its rays run (w 0).
	eye  mgl32.Vec4
	cone int32 // coneNone, coneAll or coneClosed
	mode int32
	// camera is the camera's view (not a shadow's), for the stats.
	camera bool
	// compact packs the pass's commands (where the driver can): the camera
	// always, a shadow pass when its caller expects most commands empty.
	compact bool
}

// cameraCullView is the camera's view for a pass in mode.
func cameraCullView(frustum Frustum, projView mgl32.Mat4, eye mgl32.Vec3, mode int32) cullView {
	v := cullView{planes: frustum.Planes, projView: projView, eye: eye.Vec4(1), mode: mode, camera: true, compact: true}
	if NaniteCone {
		v.cone = coneAll
	}
	return v
}

// shadowCullView is a shadow pass's view: the light's frustum (or nothing,
// for a pass that culls nothing) and no occlusion. The cone is tested on
// closed meshes only, from the eye the frustum itself says the light is at.
func shadowCullView(cull *Frustum) cullView {
	v := cullView{mode: cullPlain}
	if cull != nil {
		v.planes = cull.Planes
		if eye, ok := frustumEye(*cull); ok && NaniteCone && NaniteShadowCone {
			v.eye, v.cone = eye, coneClosed
		}
	}
	return v
}

// frustumEye is where a frustum looks from, as the cone test wants it: for a
// perspective frustum (a point light's cube face) the point its four side
// planes meet at, w 1; for an orthographic one (a sun cascade), whose side
// planes are parallel in pairs, the direction it looks — its near plane's
// inward normal — w 0. False for a frustum that is neither (degenerate).
//
// Read off the planes rather than passed in, so every shadow pass gets it
// from the one thing they all already hand the walker.
func frustumEye(f Frustum) (mgl32.Vec4, bool) {
	l, r, b := f.Planes[0], f.Planes[1], f.Planes[2]
	ln, rn := l.Vec3(), r.Vec3()
	scale := ln.Len() + rn.Len()
	if scale == 0 {
		return mgl32.Vec4{}, false
	}
	if ln.Add(rn).Len() < 1e-5*scale {
		// Left and right face each other exactly: orthographic.
		d := f.Planes[4].Vec3()
		if d.Len() == 0 {
			return mgl32.Vec4{}, false
		}
		return d.Normalize().Vec4(0), true
	}
	// Perspective: every side plane passes through the eye, n·x + w = 0.
	m := mgl32.Mat3FromRows(ln, rn, b.Vec3())
	if math.Abs(float64(m.Det())) < 1e-12 {
		return mgl32.Vec4{}, false
	}
	eye := m.Inv().Mul3x1(mgl32.Vec3{-l[3], -r[3], -b[3]})
	return eye.Vec4(1), true
}

// naniteTwoPass is set by DrawWorld for the stretch of the frame between the
// camera's first pass and naniteOcclusionPass, which is what promises the
// second pass will run. A caller drawing compiled surfaces on its own (a
// test, a preview) leaves it off and gets one complete pass.
var naniteTwoPass bool

// naniteOcclusionPass is the camera's second pass for everything the GPU cull
// drew a first pass of this frame: build the Hi-Z pyramid from the depth
// drawn so far, then cull against it and draw what the first pass missed.
func naniteOcclusionPass(lvl *world.Level) {
	defer func() { naniteTwoPass = false }()
	if !naniteTwoPass || !naniteCull.ok {
		return
	}
	defer func() { naniteChunkPending = naniteChunkPending[:0] }()
	sb := staticCache[lvl]
	static := sb != nil && sb.gpu != nil && sb.gpu.pass2
	scenery := naniteScenery.pending()
	if !static && !scenery && len(naniteChunkPending) == 0 {
		return
	}
	ProfileBegin("nanite: hi-z")
	naniteCull.buildHiZ()
	ProfileEnd()
	if static {
		sb.gpu.drawOcclusion(sb)
	}
	for _, p := range naniteChunkPending {
		p.g.drawOcclusion(p.cb)
	}
	if scenery {
		naniteScenery.drawOcclusion()
	}
	gl.UseProgram(gfx.Program)
}

// naniteCuller is the cull's programs and the frame's Hi-Z pyramid.
type naniteCuller struct {
	tried, ok bool
	inst      progUniforms
	clus      progUniforms
	// compact is the packing pass (nanite_compact_comp.glsl), built when the
	// driver has the indirect-count draw.
	compactScan    progUniforms
	compactScatter progUniforms
	canCompact     bool
	// merge joins runs of surviving neighbours into one command
	// (nanite_merge_comp.glsl); every pass runs it.
	merge progUniforms
	hiz   hiZPyramid
	// hizFrame is the pass number the pyramid was last built in, and hizOK
	// whether that build produced one.
	hizBuilt bool
	hizOK    bool
}

var naniteCull naniteCuller

// ready builds the programs the first time it is asked, and reports whether
// the cull can run.
func (n *naniteCuller) ready() bool {
	if n.tried {
		return n.ok
	}
	n.tried = true
	if err := n.build(); err != nil {
		log.Printf("nanite: GPU cull unavailable: %v — clusters are culled on the CPU", err)
		return false
	}
	n.ok = true
	log.Printf("nanite: GPU cluster cull on (occlusion %v, cone %v, shadows %v, prepass %v, indirect count %v)",
		NaniteOcclusion, NaniteCone, NaniteShadows, NanitePrepass, n.compactable())
	return true
}

func (n *naniteCuller) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	common := mustReadShader(biomeShaderPath("nanite_cull_common.glsl"))
	for _, s := range []struct {
		file string
		out  *progUniforms
	}{
		{"nanite_instance_cull_comp.glsl", &n.inst},
		{"nanite_cluster_cull_comp.glsl", &n.clus},
	} {
		src := "#version 430\n" + common + "\n" + mustReadShader(biomeShaderPath(s.file))
		cs, cerr := gfx.CompileShader(src, gl.COMPUTE_SHADER)
		if cerr != nil {
			return fmt.Errorf("%s: %v", s.file, cerr)
		}
		prog, lerr := linkStages(cs)
		if lerr != nil {
			return fmt.Errorf("%s: %v", s.file, lerr)
		}
		*s.out = progUniforms{prog: prog, loc: map[string]int32{}}
	}
	{
		cs, cerr := gfx.CompileShader(mustReadShader(biomeShaderPath("nanite_merge_comp.glsl")), gl.COMPUTE_SHADER)
		if cerr != nil {
			return fmt.Errorf("nanite_merge_comp.glsl: %v", cerr)
		}
		prog, lerr := linkStages(cs)
		if lerr != nil {
			return fmt.Errorf("nanite_merge_comp.glsl: %v", lerr)
		}
		n.merge = progUniforms{prog: prog, loc: map[string]int32{}}
	}
	// The packing pass, only where the indirect-count draw is: without it
	// the fixed command list is drawn as it stands.
	glext.Init()
	if glext.IndirectCountAvailable() {
		// A failure here costs the packing, not the cull: logged, and the
		// fixed list drawn.
		src := mustReadShader(biomeShaderPath("nanite_compact_comp.glsl"))
		var cerr error
		for _, v := range []struct {
			define string
			out    *progUniforms
		}{{"COMPACT_SCAN", &n.compactScan}, {"COMPACT_SCATTER", &n.compactScatter}} {
			if cerr != nil {
				break
			}
			var cs uint32
			cs, cerr = gfx.CompileShader("#version 430\n#define "+v.define+"\n"+src, gl.COMPUTE_SHADER)
			if cerr != nil {
				break
			}
			var prog uint32
			prog, cerr = linkStages(cs)
			if cerr == nil {
				*v.out = progUniforms{prog: prog, loc: map[string]int32{}}
			}
		}
		if cerr != nil {
			log.Printf("nanite: command packing unavailable: %v — the fixed command list is drawn", cerr)
		} else {
			n.canCompact = true
		}
	}
	return n.hiz.init("nanite", naniteHiZUnit)
}

// compactable reports whether this pass's commands are packed and drawn by
// count.
func (n *naniteCuller) compactable() bool {
	return n.canCompact && NaniteCompact && glext.IndirectCountAvailable()
}

// buildHiZ builds the pyramid from the depth drawn so far, once per frame's
// occlusion pass. Reports whether there is one to test against.
func (n *naniteCuller) buildHiZ() bool {
	n.hizOK = n.hiz.build()
	n.hizBuilt = true
	return n.hizOK
}

// setTiles replaces the batch's tiles and groups.
func (b *cullBatch) setTiles(tiles []gpuTile, groups []cullGroup, slots int) {
	b.tiles, b.groups, b.slots = tiles, groups, slots
	b.tilesDirty = true
	b.groupsDirty = true
	// Each tile names its group's first slot, where the packing writes from.
	gi := 0
	for ti := range tiles {
		for gi < len(groups) && int32(tiles[ti].A[3]) >= groups[gi].first+groups[gi].count {
			gi++
		}
		if gi < len(groups) {
			tiles[ti].B[1] = uint32(groups[gi].first)
		}
	}
}

// setInstances replaces the batch's instances.
func (b *cullBatch) setInstances(insts []gpuInstance) {
	b.insts = insts
	b.instDirty = true
}

// ensureHistory sizes the visibility history to n entries, clearing it if it
// had to grow: everything is "not seen last frame", which pass 2 draws.
func (b *cullBatch) ensureHistory(n int) {
	if b.histBuf == 0 {
		gl.GenBuffers(1, &b.histBuf)
	}
	if n <= b.histLen && b.histLen > 0 {
		return
	}
	b.histLen = max(n, 1)
	zero := make([]uint32, b.histLen)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.histBuf)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, b.histLen*4, gl.Ptr(zero), gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

// forgetHistory marks everything unseen — a cut, a teleport.
func (b *cullBatch) forgetHistory() {
	if b.histBuf == 0 || b.histLen == 0 {
		return
	}
	zero := make([]uint32, b.histLen)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.histBuf)
	gl.BufferSubData(gl.SHADER_STORAGE_BUFFER, 0, b.histLen*4, gl.Ptr(zero))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

// upload sends whatever changed since the last cull.
func (b *cullBatch) upload() {
	if b.tileBuf == 0 {
		gl.GenBuffers(1, &b.tileBuf)
		gl.GenBuffers(1, &b.instBuf)
		gl.GenBuffers(1, &b.visBuf)
		gl.GenBuffers(1, &b.cmdBuf)
	}
	if b.tilesDirty {
		b.tilesDirty = false
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.tileBuf)
		if len(b.tiles) > 0 {
			gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(b.tiles)*int(unsafe.Sizeof(gpuTile{})), gl.Ptr(b.tiles), gl.STATIC_DRAW)
		}
		if b.tileCountBuf == 0 {
			gl.GenBuffers(1, &b.tileCountBuf)
			gl.GenBuffers(1, &b.tileOffBuf)
		}
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.tileCountBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, max(len(b.tiles), 1)*4, nil, gl.DYNAMIC_COPY)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.tileOffBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, max(len(b.tiles), 1)*4, nil, gl.DYNAMIC_COPY)
	}
	if b.instDirty {
		b.instDirty = false
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.instBuf)
		if len(b.insts) > 0 {
			// Always a fresh store (orphaning), never a write into the old:
			// the GPU runs most of a frame behind, and the last pass to read
			// this buffer — the occlusion pass, near the end of that frame —
			// is still queued when the next frame's first pass changes a
			// matrix. A BufferSubData there waits for it, and the wait shows
			// up as a bubble on the GPU.
			size := len(b.insts) * int(gpuInstanceStride)
			gl.BufferData(gl.SHADER_STORAGE_BUFFER, size, gl.Ptr(b.insts), gl.STREAM_DRAW)
			gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.visBuf)
			gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(b.insts)*4, nil, gl.DYNAMIC_COPY)
		}
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

// cull runs the two stages for a pass, leaving this pass's commands in
// cmdBuf. False when there is nothing to draw (no tiles), or the cull cannot
// run.
func (b *cullBatch) cull(v cullView) bool {
	if len(b.tiles) == 0 || len(b.insts) == 0 || b.slots == 0 || !naniteCull.ready() {
		return false
	}
	n := &naniteCull
	ProfileBegin("nanite: cull")
	defer ProfileEnd()
	b.upload()
	b.ensureHistory(1)
	passStore(b.cmdBuf, &b.cmdSize, b.slots*20)

	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, b.instBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, b.visBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 2, b.clusters)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 3, b.tileBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 4, b.histBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 5, b.cmdBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 9, b.tileCountBuf)

	useHiz := v.mode == cullPass2 && n.hizOK
	if useHiz {
		gl.ActiveTexture(gl.TEXTURE0 + naniteHiZUnit)
		gl.BindTexture(gl.TEXTURE_2D, n.hiz.tex)
		gl.ActiveTexture(gl.TEXTURE0)
	}
	setCommon := func(p *progUniforms) {
		gl.UseProgram(p.prog)
		gl.Uniform1i(p.U("mode"), v.mode)
		gl.Uniform4fv(p.U("planes"), 6, &v.planes[0][0])
		gl.UniformMatrix4fv(p.U("projView"), 1, false, &v.projView[0])
		gl.Uniform4f(p.U("eye"), v.eye[0], v.eye[1], v.eye[2], v.eye[3])
		gl.Uniform1i(p.U("useCone"), v.cone)
		gl.Uniform1i(p.U("useHiz"), boolToInt32(useHiz))
		gl.Uniform1i(p.U("hiz"), naniteHiZUnit)
		gl.Uniform2i(p.U("hizSize"), n.hiz.size[0], n.hiz.size[1])
		gl.Uniform1i(p.U("hizLevels"), n.hiz.levels)
	}
	setCommon(&n.inst)
	gl.Uniform1ui(n.inst.U("instanceCount"), uint32(len(b.insts)))
	gl.DispatchCompute(uint32((len(b.insts)+63)/64), 1, 1)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)

	setCommon(&n.clus)
	for done := 0; done < len(b.tiles); done += 65535 {
		cnt := min(len(b.tiles)-done, 65535)
		gl.Uniform1ui(n.clus.U("tileBase"), uint32(done))
		gl.DispatchCompute(uint32(cnt), 1, 1)
	}
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	// Runs of surviving neighbours into one command each.
	gl.UseProgram(n.merge.prog)
	for done := 0; done < len(b.tiles); done += 65535 {
		gl.Uniform1ui(n.merge.U("workBase"), uint32(done))
		gl.DispatchCompute(uint32(min(len(b.tiles)-done, 65535)), 1, 1)
	}
	gl.MemoryBarrier(gl.COMMAND_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT)
	if NaniteStats && v.camera {
		b.countDrawn()
	}
	// Packed for the camera only. A shadow pass culls by the light's frustum
	// alone, so nearly every command survives and the packing is all cost:
	// measured on 256 dense rocks, two milliseconds over three cascades.
	b.compacted = false
	if n.compactable() && v.compact && len(b.groups) > 0 {
		b.compactCommands()
	}
	for i := uint32(0); i < 11; i++ {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, i, 0)
	}
	return true
}

// drawGroup issues group gi's indirect multi-draw from this pass's commands.
// The caller has bound the program, its uniforms and the VAO (whose index
// buffer the commands address).
func (b *cullBatch) drawGroup(gi int) {
	g := b.groups[gi]
	if g.count == 0 || !b.groupLive(gi) {
		return
	}
	if b.compacted {
		gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, b.packedBuf)
		gl.BindBuffer(glext.ParameterBuffer, b.countBuf)
		glext.MultiDrawElementsIndirectCount(gl.TRIANGLES, gl.UNSIGNED_INT, uintptr(g.first)*20, uintptr(gi)*4, g.count, 20)
		gl.BindBuffer(glext.ParameterBuffer, 0)
		gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, 0)
		return
	}
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, b.cmdBuf)
	gl.MultiDrawElementsIndirect(gl.TRIANGLES, gl.UNSIGNED_INT, gl.PtrOffset(int(g.first)*20), g.count, 0)
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, 0)
}

// countDrawn adds this pass's triangles to DebugStats.NaniteTriangles: a
// readback, for the debug overlay only (NaniteStats).
func (b *cullBatch) countDrawn() {
	cmds := make([]uint32, b.slots*5)
	if len(cmds) == 0 {
		return
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.cmdBuf)
	glext.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(cmds)*4, gl.Ptr(&cmds[0]))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	for i := 0; i+1 < len(cmds); i += 5 {
		DebugStats.NaniteTriangles += int(cmds[i]*cmds[i+1]) / 3
	}
	if os.Getenv("FOUNDRY_NANITE_STATS_LOG") == "1" {
		log.Printf("nanite stats: %d slots, %d triangles drawn so far this frame", b.slots, DebugStats.NaniteTriangles)
	}
}

// passStore gives buf, a store every pass of the batch rewrites in full (the
// commands, the packed commands, the counts), size bytes — only when that is
// not the size it already has.
//
// These used to be orphaned every pass (glBufferData with no data), so that a
// pass's compute could write a fresh store while the last pass's draws still
// read the old one. On Mesa's D3D12 driver (WSLg) one of those orphanings
// never returned: a level whose chunk brushes cast into a point light's six
// cube faces re-specified the same buffer a dozen times a frame and `foundry
// play` hung in glBufferData within a few frames. Reusing the store is what
// GL promises works anyway — commands run in order, so the compute pass
// writing it waits for the draws before it that read it, the driver inserting
// the barrier — and on the native NVIDIA driver it measured the same
// (docs/nanite/PHASE_1.md).
func passStore(buf uint32, have *int, size int) {
	if *have == size {
		return
	}
	*have = size
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, buf)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, size, nil, gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

// compactCommands packs the pass's surviving commands to the front of each
// group's run and counts them (nanite_compact_comp.glsl), so drawGroup can
// draw exactly the survivors: a scan per group over its tiles' counts, then a
// scatter per tile. The cull's SSBO bindings are still up; this adds 6-10.
func (b *cullBatch) compactCommands() {
	n := &naniteCull
	if b.packedBuf == 0 {
		gl.GenBuffers(1, &b.packedBuf)
		gl.GenBuffers(1, &b.countBuf)
		gl.GenBuffers(1, &b.groupBuf)
		b.groupsDirty = true
	}
	if b.groupsDirty {
		b.groupsDirty = false
		runs := make([]uint32, 0, 4*len(b.groups))
		ti := 0
		for _, g := range b.groups {
			first := ti
			for ti < len(b.tiles) && int32(b.tiles[ti].A[3]) < g.first+g.count {
				ti++
			}
			runs = append(runs, uint32(g.first), uint32(g.count), uint32(first), uint32(ti-first))
		}
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.groupBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(runs)*4, gl.Ptr(runs), gl.STATIC_DRAW)
	}
	passStore(b.packedBuf, &b.packedSize, b.slots*20)
	passStore(b.countBuf, &b.countSize, len(b.groups)*4)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 6, b.packedBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 7, b.countBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 8, b.groupBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 10, b.tileOffBuf)

	dispatch := func(p *progUniforms, work int) {
		gl.UseProgram(p.prog)
		for done := 0; done < work; done += 65535 {
			gl.Uniform1ui(p.U("workBase"), uint32(done))
			gl.DispatchCompute(uint32(min(work-done, 65535)), 1, 1)
		}
	}
	dispatch(&n.compactScan, len(b.groups))
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	dispatch(&n.compactScatter, len(b.tiles))
	gl.MemoryBarrier(gl.COMMAND_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT)
	b.compacted = true
}

func (b *cullBatch) release() {
	for _, buf := range []*uint32{&b.tileBuf, &b.instBuf, &b.visBuf, &b.histBuf, &b.cmdBuf,
		&b.packedBuf, &b.countBuf, &b.groupBuf, &b.tileCountBuf, &b.tileOffBuf} {
		if *buf != 0 {
			gl.DeleteBuffers(1, buf)
			*buf = 0
		}
	}
	b.histLen = 0
	b.cmdSize, b.packedSize, b.countSize = 0, 0, 0
	b.tilesDirty, b.instDirty = len(b.tiles) > 0, len(b.insts) > 0
}

// appendTiles cuts a run of count clusters starting at cluster first (and
// history entry hist) of instance inst into tiles, assigning them command
// slots from *slot on.
func appendTiles(tiles []gpuTile, first, count, inst, hist uint32, slot *int) []gpuTile {
	for off := uint32(0); off < count; off += naniteTileClusters {
		n := min(count-off, naniteTileClusters)
		tiles = append(tiles, gpuTile{
			A: [4]uint32{first + off, n, inst, uint32(*slot)},
			B: [4]uint32{hist + off, 0, 0, 0},
		})
		*slot += int(n)
	}
	return tiles
}

// uploadClusters fills (making if need be) an SSBO with clusters.
func uploadClusters(buf *uint32, clusters []gpuCluster) {
	if *buf == 0 {
		gl.GenBuffers(1, buf)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, *buf)
	if len(clusters) > 0 {
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(clusters)*int(unsafe.Sizeof(gpuCluster{})), gl.Ptr(clusters), gl.STATIC_DRAW)
	} else {
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, 64, nil, gl.STATIC_DRAW)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}
