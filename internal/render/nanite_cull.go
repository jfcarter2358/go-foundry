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
// slots fall into, one per material. The cull is three compute stages:
//
//  1. nanite_instance_cull_comp.glsl — a thread per instance: its sphere
//     against the frustum and, in the occlusion pass, the Hi-Z pyramid.
//  2. nanite_lod_traverse_comp.glsl — for instances of a mesh with a cluster
//     DAG (a cooked Nanite mesh; Phase 2), a workgroup per instance walks the
//     tree over its cluster groups (cluster.BuildLODTree) breadth first and
//     marks the leaves that may hold a cluster of this pass's cut — see
//     "Level of detail" below.
//  3. the cluster stage, the tests themselves (nanite_cluster_common.glsl):
//     the instance's verdict, the cluster's LOD test (its leaf's mark, then
//     the exact test), its sphere against the frustum, its normal cone, and
//     the Hi-Z pyramid. For a CLUSTER tile (nanite_cluster_cull_comp.glsl) a
//     workgroup per tile and a slot per cluster; for a RANGE tile — a window
//     of a DAG's leaves (tileRange) — a thread per tile and rangeSlots slots
//     for its survivors' runs (nanite_range_cull_comp.glsl). Every slot is
//     written, a culled one as a zero count, so the command list is never
//     cleared — and since the slots are fixed, the draw order is the same
//     every frame, which is what keeps a capture deterministic.
//
// Then a merge pass joins each cluster tile's runs of neighbouring survivors
// into one command (nanite_merge_comp.glsl) and, where the driver has the
// indirect-count draw and the pass expects mostly empty slots, a packing pass
// moves the survivors to the front of each group (compactCommands); then one
// indirect multi-draw per group. Nothing is read back (outside
// FOUNDRY_NANITE_STATS).
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
// # Level of detail
//
// A cooked mesh's clusters are every level of its DAG (internal/cluster's
// dag.go), and so are a compiled level's large opaque groups' (the compiler
// builds those: world/static_clusters.go), each cluster with its own error
// and LOD sphere and its parent's. A pass
// draws a cluster when its error, projected from the CAMERA, is within
// gfx.NaniteLODError pixels and its parent's is not — the per-cluster test
// that yields a complete, crack-free cut with no traversal at all. The tree
// walk only saves work: whole subtrees whose every cluster the test would
// reject are skipped, and a leaf it does not reach writes zero commands
// without reading its clusters' bounds. Leaf marks carry the pass's stamp
// (passStamp), so nothing is ever cleared.
//
// Every pass — the camera's two and every shadow view — chooses the cut from
// the camera, never from the light. A shadow drawn from a coarser cut than
// the surface the camera sees would shadow that surface by its own
// difference: acne where the coarse caster pokes through, light leaking where
// it falls short. From the camera, caster and receiver are the same
// triangles; and a distant mesh — most of a sun cascade — is coarse in both.
// The camera's parameters are taken once at the top of DrawWorld
// (beginNaniteLODFrame) so every pass of a frame agrees.
//
// FOUNDRY_NANITE_LOD=0 draws level 0 only, the full detail, as Phase 1 did.
//
// A DAG's tiles are range tiles: its clusters are twice its full detail's,
// and a command slot costs the draw whether it draws or not, so a slot per
// cluster made the rock field slower than without LOD at all
// (docs/nanite/PHASE_2.md).
//
// # Switches
//
// Every piece can be turned off, and the old path stays as the fallback:
//
//	FOUNDRY_NANITE_LOD=0        full detail always (no LOD selection)
//	FOUNDRY_NANITE_LOD_STATIC=0 the compiler gives no group a DAG (read by
//	                            the compiler: world.StaticLOD)
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
// 112 bytes).
type gpuCluster struct {
	Sphere [4]float32 // local centre, radius
	Apex   [4]float32
	Cone   [4]float32 // axis, cutoff (>= 1: none)
	Draw   [4]uint32  // firstIndex, indexCount, baseVertex, leaf (in its mesh's tree)
	// LOD is the sphere its own error is projected from, Parent its parent
	// group's; Errs: own error, parent error (< 0: a root), level.
	LOD    [4]float32
	Parent [4]float32
	Errs   [4]float32
}

// gpuNode is a node of a mesh's LOD tree (cluster.LODNode) as
// nanite_lod_traverse_comp.glsl reads it (std430, 80 bytes).
type gpuNode struct {
	Sphere [4]float32 // the cull sphere
	Parent [4]float32 // a sphere round every parent sphere under it
	Own    [4]float32 // a sphere round every own LOD sphere under it
	Errs   [4]float32 // the largest parent error, the smallest own error
	// Link: first child (a global node index), child count (0: a leaf),
	// first leaf, leaf count — a leaf's own number is its first leaf.
	Link [4]uint32
}

// gpuTile is a run of one instance's clusters (std430, 32 bytes): a = first
// cluster, count, instance, first command slot; b.x = first history entry,
// b.y the first slot of its group (setTiles), b.z its mode (tileClusters or
// tileRange).
type gpuTile struct {
	A, B [4]uint32
}

// A tile's modes. A CLUSTERS tile gives every cluster its own command slot,
// and the merge pass joins runs of survivors. A RANGE tile is a window of a
// DAG's clusters — at most rangeMaxClusters of them and rangeMaxLeaves whole
// leaves of its LOD tree (cluster.LeafRanges), each of which the LOD test
// takes or leaves whole — with rangeSlots command slots: its survivors' runs,
// joined wherever what lies between them is in the cut (dropped only by the
// frustum, the cone or the occlusion, which draw harmlessly), never across a
// cluster outside it (nanite_range_cull_comp.glsl). What it saves is slots
// and commands: a cooked mesh's DAG holds twice the clusters of its full
// detail, and a slot costs the draw whether or not it draws, a command
// whenever it does (measured: docs/nanite/PHASE_2.md).
const (
	tileClusters = 0
	tileRange    = 1
)

// rangeSlots is a range tile's command slots (the shaders' RANGE_SLOTS);
// rangeMaxLeaves the most leaves a window takes, so that the runs no join
// can bridge — separated by whole leaves outside the cut — never outnumber
// the slots: ceil(rangeMaxLeaves/2) <= rangeSlots. rangeMaxClusters is the
// most clusters, a 32-bit mask's worth.
const (
	rangeSlots       = 2
	rangeMaxLeaves   = cluster.WindowMaxLeaves
	rangeMaxClusters = cluster.WindowMaxClusters
)

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
	// instLOD: the instance's mesh has a cluster DAG; Flags[1] is its tree's
	// root node and Flags[2] the first of its leaf marks.
	instLOD = 1 << 3
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
// firstIndex (indices) and baseVertex: a cluster with no DAG over it — level
// 0, no error, a root — which every LOD test passes.
func toGPUCluster(c *cluster.Cluster, firstIndex uint32, baseVertex int32) gpuCluster {
	g := gpuCluster{
		Sphere: [4]float32{c.Center[0], c.Center[1], c.Center[2], c.Radius},
		Apex:   [4]float32{c.ConeApex[0], c.ConeApex[1], c.ConeApex[2], 0},
		Cone:   [4]float32{c.ConeAxis[0], c.ConeAxis[1], c.ConeAxis[2], c.ConeCutoff},
		Draw:   [4]uint32{firstIndex + 3*c.TriFirst, 3 * c.TriCount, uint32(baseVertex), 0},
		LOD:    [4]float32{c.Center[0], c.Center[1], c.Center[2], c.Radius},
		Errs:   [4]float32{0, -1, 0, 0},
	}
	return g
}

// toGPULODCluster is a DAG cluster as the shader reads it, in leaf leaf of
// its mesh's tree.
func toGPULODCluster(c *cluster.LODCluster, leaf int32, firstIndex uint32, baseVertex int32) gpuCluster {
	g := toGPUCluster(&c.Cluster, firstIndex, baseVertex)
	g.Draw[3] = uint32(leaf)
	g.LOD = [4]float32{c.LODCenter[0], c.LODCenter[1], c.LODCenter[2], c.LODRadius}
	g.Parent = [4]float32{c.ParentCenter[0], c.ParentCenter[1], c.ParentCenter[2], c.ParentRadius}
	g.Errs = [4]float32{c.Error, c.ParentError, float32(c.Level), 0}
	if c.ParentError < 0 {
		g.Errs[1] = -1
	}
	return g
}

// toGPUNode is a tree node as the shader reads it, its children offset by
// nodeBase (the mesh's root's place in the pool).
func toGPUNode(n *cluster.LODNode, nodeBase uint32) gpuNode {
	g := gpuNode{
		Sphere: [4]float32{n.Center[0], n.Center[1], n.Center[2], n.Radius},
		Parent: [4]float32{n.ParentCenter[0], n.ParentCenter[1], n.ParentCenter[2], n.ParentRadius},
		Own:    [4]float32{n.OwnCenter[0], n.OwnCenter[1], n.OwnCenter[2], n.OwnRadius},
		Errs:   [4]float32{n.MaxParentError, n.MinError, 0, 0},
		Link:   [4]uint32{0, uint32(n.ChildCount), uint32(n.LeafFirst), uint32(n.LeafCount)},
	}
	if n.ChildCount > 0 {
		g.Link[0] = nodeBase + uint32(n.FirstChild)
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

	// nodes is the SSBO of LOD tree nodes the instances' Flags[1] index (the
	// Nanite mesh pool's); leaves is how many leaf marks the instances need
	// in all, leafBuf where they are kept. Zero leaves: no instance has a
	// DAG, and the traversal is not run.
	nodes   uint32
	leaves  int
	leafBuf uint32
	leafCap int

	// kindBuf lists the tiles by kind — the cluster tiles' numbers, then
	// the range tiles' — which each stage is dispatched over.
	kindBuf                 uint32
	clusterTiles, rangeTile int

	// slotBuf is the debug views' per-slot table (nanite_debug.go), remade
	// when the tiles change.
	slotBuf    uint32
	slotsDirty bool

	// tileSpheres is a sphere round each tile's clusters, in the space
	// their spheres are in (markLive), remade when the tiles change.
	tileSpheres [][4]float32

	// stream, when set, is the page pool the clusters are streamed through
	// (nanite_stream.go): the camera's passes write its feedback.
	stream *naniteStreamer

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
// indexed as the tiles' clusters are. Each tile's own sphere, round its
// clusters', is tested first (tileSpheres), so a group wholly out of view
// costs a test a tile rather than a cluster — which matters since a DAG
// holds every level's clusters.
func (b *cullBatch) markLive(spheres [][4]float32, model mgl32.Mat4, frustum *Frustum) {
	if len(b.live) != len(b.groups) {
		b.live = make([]bool, len(b.groups))
	}
	if len(b.tileSpheres) != len(b.tiles) {
		b.tileSpheres = make([][4]float32, len(b.tiles))
		for ti := range b.tiles {
			t := &b.tiles[ti]
			var lo, hi mgl32.Vec3
			for k := uint32(0); k < t.A[1]; k++ {
				sp := spheres[t.A[0]+k]
				r := mgl32.Vec3{sp[3], sp[3], sp[3]}
				c := mgl32.Vec3{sp[0], sp[1], sp[2]}
				if k == 0 {
					lo, hi = c.Sub(r), c.Add(r)
					continue
				}
				lo = mgl32.Vec3{min(lo[0], c[0]-r[0]), min(lo[1], c[1]-r[1]), min(lo[2], c[2]-r[2])}
				hi = mgl32.Vec3{max(hi[0], c[0]+r[0]), max(hi[1], c[1]+r[1]), max(hi[2], c[2]+r[2])}
			}
			c := lo.Add(hi).Mul(0.5)
			b.tileSpheres[ti] = [4]float32{c[0], c[1], c[2], hi.Sub(lo).Len()/2*1.001 + 1e-6}
		}
	}
	scale := float32(math.Sqrt(float64(max(model.Col(0).Vec3().LenSqr(), model.Col(1).Vec3().LenSqr(), model.Col(2).Vec3().LenSqr()))))
	touches := func(sp [4]float32) bool {
		c := model.Mul4x1(mgl32.Vec4{sp[0], sp[1], sp[2], 1}).Vec3()
		return frustum == nil || sphereInFrustum(*frustum, c, sp[3]*scale)
	}
	ti := 0
	for gi, g := range b.groups {
		b.live[gi] = false
		end := g.first + g.count
		for ; ti < len(b.tiles) && int32(b.tiles[ti].A[3]) < end; ti++ {
			if b.live[gi] || !touches(b.tileSpheres[ti]) {
				continue
			}
			t := &b.tiles[ti]
			for k := uint32(0); k < t.A[1]; k++ {
				if touches(spheres[t.A[0]+k]) {
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
	// lod is the camera's LOD parameters, whatever view this is.
	lod naniteLODView
	// compact packs the pass's commands (where the driver can): the camera
	// always, a shadow pass when its caller expects most commands empty.
	compact bool
}

// cameraCullView is the camera's view for a pass in mode.
func cameraCullView(frustum Frustum, projView mgl32.Mat4, eye mgl32.Vec3, mode int32) cullView {
	v := cullView{planes: frustum.Planes, projView: projView, eye: eye.Vec4(1), mode: mode, camera: true, compact: true,
		lod: currentNaniteLOD()}
	if NaniteCone {
		v.cone = coneAll
	}
	return v
}

// shadowCullView is a shadow pass's view: the light's frustum (or nothing,
// for a pass that culls nothing) and no occlusion. The cone is tested on
// closed meshes only, from the eye the frustum itself says the light is at.
func shadowCullView(cull *Frustum) cullView {
	v := cullView{mode: cullPlain, lod: currentNaniteLOD()}
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
	// rng is the cluster stage for range tiles (nanite_range_cull_comp.glsl).
	rng progUniforms
	// lod walks the LOD trees (nanite_lod_traverse_comp.glsl).
	lod progUniforms
	// stamp numbers the passes, for the leaf marks.
	stamp uint32
	// compact is the packing pass (nanite_compact_comp.glsl), built when the
	// driver has the indirect-count draw.
	compactScan         progUniforms
	compactScatter      progUniforms
	compactScatterRange progUniforms
	canCompact          bool
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
	log.Printf("nanite: GPU cluster cull on (LOD %v, occlusion %v, cone %v, shadows %v, prepass %v, indirect count %v)",
		NaniteLOD, NaniteOcclusion, NaniteCone, NaniteShadows, NanitePrepass, n.compactable())
	return true
}

func (n *naniteCuller) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	common := mustReadShader(biomeShaderPath("nanite_cull_common.glsl"))
	clusterCommon := mustReadShader(biomeShaderPath("nanite_cluster_common.glsl"))
	for _, s := range []struct {
		file     string
		out      *progUniforms
		clusters bool
	}{
		{"nanite_instance_cull_comp.glsl", &n.inst, false},
		{"nanite_lod_traverse_comp.glsl", &n.lod, false},
		{"nanite_cluster_cull_comp.glsl", &n.clus, true},
		{"nanite_range_cull_comp.glsl", &n.rng, true},
	} {
		src := fmt.Sprintf("#version 430\n#define RANGE_SLOTS %du\n", rangeSlots) + common + "\n"
		if s.clusters {
			src += clusterCommon + "\n"
		}
		src += mustReadShader(biomeShaderPath(s.file))
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
		}{{"COMPACT_SCAN", &n.compactScan}, {"COMPACT_SCATTER", &n.compactScatter},
			{"COMPACT_SCATTER_RANGE", &n.compactScatterRange}} {
			if cerr != nil {
				break
			}
			var cs uint32
			cs, cerr = gfx.CompileShader(fmt.Sprintf("#version 430\n#define RANGE_SLOTS_COMPACT %du\n#define %s\n", rangeSlots, v.define)+src, gl.COMPUTE_SHADER)
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
	b.tileSpheres = b.tileSpheres[:0]
	b.tilesDirty = true
	b.groupsDirty = true
	b.slotsDirty = true
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
			gl.GenBuffers(1, &b.kindBuf)
		}
		kinds := make([]uint32, 0, len(b.tiles))
		for ti := range b.tiles {
			if b.tiles[ti].B[2] != tileRange {
				kinds = append(kinds, uint32(ti))
			}
		}
		b.clusterTiles = len(kinds)
		for ti := range b.tiles {
			if b.tiles[ti].B[2] == tileRange {
				kinds = append(kinds, uint32(ti))
			}
		}
		b.rangeTile = len(kinds) - b.clusterTiles
		kinds = append(kinds, 0)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.kindBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(kinds)*4, gl.Ptr(kinds), gl.STATIC_DRAW)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.tileCountBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, max(len(b.tiles), 1)*4, nil, gl.DYNAMIC_COPY)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.tileOffBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, max(len(b.tiles), 1)*4, nil, gl.DYNAMIC_COPY)
	}
	if b.leaves > b.leafCap || (b.leaves > 0 && b.leafBuf == 0) {
		// The marks start at zero, which no pass's stamp is.
		if b.leafBuf == 0 {
			gl.GenBuffers(1, &b.leafBuf)
		}
		b.leafCap = b.leaves
		zero := make([]uint32, b.leafCap)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.leafBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, b.leafCap*4, gl.Ptr(zero), gl.DYNAMIC_COPY)
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
	lodOn := NaniteLOD && b.leaves > 0
	walk := lodOn && naniteLODWalk
	// A debug view's colour pass: a command a cluster, its slot its base
	// instance (nanite_debug.go).
	debug := v.camera && naniteDebugActive()
	n.stamp++
	if n.stamp == 0 {
		n.stamp = 1
	}
	if b.leafBuf != 0 {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 11, b.nodes)
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 12, b.leafBuf)
	}
	// Streaming feedback, from the camera's passes only: a shadow view's
	// cut is the camera's, and what it alone would ask for is off screen.
	streamOn := b.stream != nil && v.camera && b.stream.wantsFeedback()
	var streamFrame, streamPages uint32
	if streamOn {
		b.stream.bind()
		streamFrame, streamPages = b.stream.frame, uint32(len(b.stream.pages))
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
		gl.Uniform1i(p.U("lodMode"), boolToInt32(lodOn))
		gl.Uniform1i(p.U("lodWalk"), boolToInt32(walk))
		gl.Uniform3f(p.U("lodEye"), v.lod.eye[0], v.lod.eye[1], v.lod.eye[2])
		gl.Uniform1f(p.U("lodK"), v.lod.k)
		gl.Uniform1ui(p.U("passStamp"), n.stamp)
		gl.Uniform1ui(p.U("instanceCount"), uint32(len(b.insts)))
		gl.Uniform1i(p.U("debugSlots"), boolToInt32(debug))
		gl.Uniform1i(p.U("streamOn"), boolToInt32(streamOn))
		gl.Uniform1ui(p.U("streamFrame"), streamFrame)
		gl.Uniform1ui(p.U("streamPages"), streamPages)
	}
	setCommon(&n.inst)
	gl.DispatchCompute(uint32((len(b.insts)+63)/64), 1, 1)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)

	if walk {
		// The LOD trees, a workgroup an instance.
		ProfileBegin("nanite: lod")
		setCommon(&n.lod)
		for done := 0; done < len(b.insts); done += 65535 {
			gl.Uniform1ui(n.lod.U("workBase"), uint32(done))
			gl.DispatchCompute(uint32(min(len(b.insts)-done, 65535)), 1, 1)
		}
		gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
		ProfileEnd()
	}

	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 13, b.kindBuf)
	if b.clusterTiles > 0 {
		setCommon(&n.clus)
		for done := 0; done < b.clusterTiles; done += 65535 {
			cnt := min(b.clusterTiles-done, 65535)
			gl.Uniform1ui(n.clus.U("tileBase"), uint32(done))
			gl.DispatchCompute(uint32(cnt), 1, 1)
		}
	}
	if b.rangeTile > 0 {
		setCommon(&n.rng)
		total := b.clusterTiles + b.rangeTile
		gl.Uniform1ui(n.rng.U("kindTileCount"), uint32(total))
		groups := (b.rangeTile + 63) / 64
		for done := 0; done < groups; done += 65535 {
			gl.Uniform1ui(n.rng.U("tileBase"), uint32(b.clusterTiles+done*64))
			gl.DispatchCompute(uint32(min(groups-done, 65535)), 1, 1)
		}
	}
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	// Runs of surviving neighbours into one command each — except in a
	// debug view, which needs every cluster its own.
	if !debug && b.clusterTiles > 0 {
		gl.UseProgram(n.merge.prog)
		for done := 0; done < b.clusterTiles; done += 65535 {
			gl.Uniform1ui(n.merge.U("workBase"), uint32(done))
			gl.DispatchCompute(uint32(min(b.clusterTiles-done, 65535)), 1, 1)
		}
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
		if NaniteStats && v.camera && os.Getenv("FOUNDRY_NANITE_STATS_LOG") == "1" {
			counts := make([]uint32, len(b.groups))
			gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b.countBuf)
			glext.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, len(counts)*4, gl.Ptr(&counts[0]))
			gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
			total, live := 0, 0
			for gi, c := range counts {
				total += int(c)
				if b.groupLive(gi) {
					live += int(c)
				}
			}
			log.Printf("nanite stats: packed %d commands in %d groups (%d in live groups)", total, len(counts), live)
		}
	}
	for i := uint32(0); i < 16; i++ {
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
	live := 0
	for i := 0; i+1 < len(cmds); i += 5 {
		DebugStats.NaniteTriangles += int(cmds[i]*cmds[i+1]) / 3
		if cmds[i+1] != 0 {
			live++
		}
	}
	if os.Getenv("FOUNDRY_NANITE_STATS_LOG") == "1" {
		log.Printf("nanite stats: %d slots (%d tiles: %d cluster, %d range), %d commands drawing, %d triangles drawn so far this frame",
			b.slots, len(b.tiles), b.clusterTiles, b.rangeTile, live, DebugStats.NaniteTriangles)
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
	if b.clusterTiles > 0 {
		dispatch(&n.compactScatter, b.clusterTiles)
	}
	if b.rangeTile > 0 {
		p := &n.compactScatterRange
		gl.UseProgram(p.prog)
		gl.Uniform1ui(p.U("kindTileCount"), uint32(b.clusterTiles+b.rangeTile))
		groups := (b.rangeTile + 63) / 64
		for done := 0; done < groups; done += 65535 {
			gl.Uniform1ui(p.U("workBase"), uint32(b.clusterTiles+done*64))
			gl.DispatchCompute(uint32(min(groups-done, 65535)), 1, 1)
		}
	}
	gl.MemoryBarrier(gl.COMMAND_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT)
	b.compacted = true
}

func (b *cullBatch) release() {
	for _, buf := range []*uint32{&b.tileBuf, &b.instBuf, &b.visBuf, &b.histBuf, &b.cmdBuf,
		&b.packedBuf, &b.countBuf, &b.groupBuf, &b.tileCountBuf, &b.tileOffBuf, &b.leafBuf, &b.slotBuf, &b.kindBuf} {
		if *buf != 0 {
			gl.DeleteBuffers(1, buf)
			*buf = 0
		}
	}
	b.histLen, b.leafCap = 0, 0
	b.cmdSize, b.packedSize, b.countSize = 0, 0, 0
	b.tilesDirty, b.instDirty = len(b.tiles) > 0, len(b.insts) > 0
}

// appendRangeTile is a range tile (see tileRange) over count clusters from
// first (history entry hist) of instance inst: rangeSlots command slots from
// *slot.
func appendRangeTile(tiles []gpuTile, first, count, inst, hist uint32, slot *int) []gpuTile {
	tiles = append(tiles, gpuTile{
		A: [4]uint32{first, count, inst, uint32(*slot)},
		B: [4]uint32{hist, 0, tileRange, 0},
	})
	*slot += rangeSlots
	return tiles
}

// appendLeafTiles tiles a run of a DAG's clusters [first, first+count) of
// instance inst (history from hist), whose leaves are leaves (the mesh's
// cluster.LeafRanges, offset by base into the batch's cluster list): range
// tiles of whole consecutive leaves (rangeWindows), or — in a debug view,
// which needs every cluster its own command — cluster tiles. pages, when
// set, is each leaf's page in a streamed pool: a window takes one page's
// leaves only.
func appendLeafTiles(tiles []gpuTile, leaves [][2]int32, pages []int32, base, first, count, inst, hist uint32, slot *int) []gpuTile {
	if naniteDebugActive() {
		return appendTiles(tiles, first, count, inst, hist, slot)
	}
	var in [][2]int32
	var inPages []int32
	for li, l := range leaves {
		if lf := base + uint32(l[0]); lf >= first && lf < first+count {
			in = append(in, [2]int32{int32(lf), l[1]})
			if pages != nil {
				inPages = append(inPages, pages[li])
			}
		}
	}
	for _, w := range rangeWindows(in, inPages) {
		tiles = appendRangeTile(tiles, uint32(w[0]), uint32(w[1]), inst, hist+(uint32(w[0])-first), slot)
	}
	return tiles
}

// rangeWindows is cluster.RangeWindows — whose rule the cook's pages are
// packed by, so that the windows never straddle a page — with, when pages is
// set (each leaf's page in a streamed pool), a break wherever the page
// changes: two pages need not sit side by side in the index buffer. A leaf
// is never longer than cluster.MaxLeafClusters, which is rangeMaxClusters.
func rangeWindows(leaves [][2]int32, pages []int32) [][2]int32 {
	if pages == nil {
		return cluster.RangeWindows(leaves, nil)
	}
	return cluster.RangeWindows(leaves, func(a, b int) bool { return pages[a] != pages[b] })
}

// appendTiles cuts a run of count clusters starting at cluster first (and
// history entry hist) of instance inst into cluster tiles, assigning them
// command slots from *slot on.
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

// uploadNodes fills (making if need be) an SSBO with LOD tree nodes.
func uploadNodes(buf *uint32, nodes []gpuNode) {
	if *buf == 0 {
		gl.GenBuffers(1, buf)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, *buf)
	if len(nodes) > 0 {
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(nodes)*int(unsafe.Sizeof(gpuNode{})), gl.Ptr(nodes), gl.STATIC_DRAW)
	} else {
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, 80, nil, gl.STATIC_DRAW)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
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
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, int(unsafe.Sizeof(gpuCluster{})), nil, gl.STATIC_DRAW)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}
