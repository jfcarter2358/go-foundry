package render

// nfoliage.go — Nanite foliage. Phase 6 of the Nanite plan
// (docs/nanite/PHASE_6.md): voxels, assemblies and skinning (UE 5.7's Nanite
// Foliage) on top of the foliage system (foliage.go, foliage_units.go).
//
// # What changes, and what does not
//
// Placement does not: units are built on the CPU exactly as before, their
// instances in the one pool, ranked and thinned by distance, the survivors
// grown to cover for the thinned (foliage_units.go, foliage_params.glsl).
// What changes is what an instance is drawn AS. The classic path picks a
// TIER for it — near mesh, far mesh, clump cards or an octahedral impostor —
// and cross-fades between tiers through complementary dithers: a band across
// the field at every boundary, and an impostor that pops between baked views
// as the camera moves. Here every variant is a Nanite asset
// (nfoliage_asset.go): its ASSEMBLY of parts and core (foliage_assembly.go)
// at level 0, a cluster DAG above it whose coarse levels are VOXEL bricks
// (internal/cluster's voxel.go), and BONES posed by the wind. An instance
// draws the DAG cut whose error, seen from the camera, is under a pixel
// (FOUNDRY_NANITE_FOLIAGE_PX): its parts and triangles near, a few hundred
// bricks at a few hundred metres, one brick at the horizon. Levels change
// where the change is under a pixel, so there is no band and nothing to pop.
//
// # A pass
//
// Every pass — the camera's, and each sun cascade or virtual-map rectangle
// (shadow.go, vsm.go) — is four compute stages and a few indirect draws:
//
//  1. CULL (nfoliage_cull_comp.glsl): a thread per candidate instance, in
//     tiles of one variant on one terrain (the CPU's rank prefix, as
//     foliage_gpu.go builds them). Thinning, frustum and Hi-Z on the plant;
//     then its cut, cluster by cluster, each cluster against the frustum and
//     the Hi-Z; every survivor an ITEM, counted into its cluster's command.
//  2. SCAN (nfoliage_scan_comp.glsl): the counts into runs of one reference
//     list.
//  3. SCATTER (nfoliage_scatter_comp.glsl): each item's references into its
//     run — one per triangle cluster, part or brick.
//  4. DRAW: one multi-draw per program — triangles and parts with the full
//     lighting, ground cover's with one shadow tap, voxel bricks — each
//     command a cluster drawn once per reference. Voxel bricks are boxes
//     ray-marched voxel by voxel (nfoliage_vox_*.glsl); where the driver
//     has 64-bit atomics the bricks small on screen are rasterised in
//     compute instead and resolved (nfoliage_voxraster.go).
//
// Shadow passes do not draw the cut: they cast each variant's SHADOW PROXY
// (the classic far mesh, on the same bones — nfoliage_asset.go's
// buildProxy), the casting shapes within foliageShadowRange, as the classic
// path's do. The camera's full-detail cut, cast into every sun rectangle of
// the virtual map, cost three to four times the classic foliage's shadows,
// and its voxel levels more again at the map's finest texels.
//
// # Bones and motion
//
// Once a frame, before the first pass, nfoliage_bones_comp.glsl poses every
// variant's bones for the wind (foliage_assembly.go has the model). Last
// frame's poses are kept, so in a TAA frame each vertex — each voxel hit —
// writes its own motion into the scene target's motion attachment: a
// swaying branch reprojects as a swaying branch, not as the camera's motion
// of whatever stood there.
//
// # Switches
//
//	FOUNDRY_NANITE_FOLIAGE=0          the classic tiers (foliage.go), as before
//	FOUNDRY_NANITE_FOLIAGE_GROUND=1   the ground cover on this path too
//	FOUNDRY_NANITE_FOLIAGE_PX         the cut's error in pixels (default 1)
//	FOUNDRY_NANITE_FOLIAGE_SWRASTER   auto | 1 | 0: small bricks by the
//	                                  compute rasteriser (64-bit atomics)
//	FOUNDRY_NANITE_FOLIAGE_GI=1       tree crowns into the GI field
//	                                  (nfoliage_gi.go; off by default)
//
// A driver that cannot build the programs gets the classic path, with a
// line in the log.

import (
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// NaniteFoliage switches the Nanite foliage path; off draws the classic
// tiers.
var NaniteFoliage = gfx.EnvFlag("FOUNDRY_NANITE_FOLIAGE", true)

// NaniteFoliageGround puts the ground cover on the Nanite path too — the
// shapes whose classic far tier is clump cards or none (grass, flowers,
// ferns, moss, pebbles, litter...). Off by default: the Nanite path is for
// what grows tall enough to have had an impostor — trees, shrubs, rocks,
// deadwood — where the impostor tier popped and banded; a meadow of
// one-pixel voxels cost several times its clump cards on the GTX 1070
// (docs/nanite/PHASE_6.md), and the ground cover keeps its tiers.
var NaniteFoliageGround = gfx.EnvFlag("FOUNDRY_NANITE_FOLIAGE_GROUND", false)

// naniteFoliagePixels is the cut's error, in pixels.
var naniteFoliagePixels = func() float32 {
	if v, err := strconv.ParseFloat(os.Getenv("FOUNDRY_NANITE_FOLIAGE_PX"), 32); err == nil && v > 0 {
		return float32(min(max(v, 0.001), 16))
	}
	return 1
}()

// The bones' slots: gust levels times flutter phases
// (nfoliage_bones_comp.glsl).
const (
	nfGusts    = 8
	nfFlutters = 4
	nfSlots    = nfGusts * nfFlutters
)

// nfBucketWords is a variant's row in the bucket table: three uvec4s —
// (first bone, bones, first cluster, clusters), its bounding sphere's bits,
// and (DAG clusters, shadow-proxy clusters, 0, 0). Shaders index it as
// buckets[bucket * 3 + i].
const nfBucketWords = 12

// Texture units, clear of the lighting's, the GI's and the virtual map's.
const (
	nfItemsUnit    = 34
	nfClustersUnit = 35
	nfPartsUnit    = 36
	nfBricksUnit   = 37
	nfVoxelsUnit   = 38
	nfPoseUnit     = 39
	nfPosePrevUnit = 40
	nfTerrainsUnit = 41
	// The compute rasteriser's resolve's (nfoliage_voxraster.go).
	nfBoxUnit     = 42
	nfRefsUnit    = 43
	nfUnitsNeeded = 44
)

// Initial capacities; both grow when a frame reports it ran out.
const (
	nfItemCap0 = 1 << 17
	nfRefCap0  = 1 << 20
)

// The counters buffer, in words (nfoliage_scan_comp.glsl's Counters).
const (
	nfCtrItems    = 0
	nfCtrItemCap  = 1
	nfCtrRefCap   = 2
	nfCtrOverflow = 3
	nfCtrScatter  = 4 // x, y, z, references used
	nfCtrVoxDisp  = 8 // x, y, z
	nfCtrVoxRefs  = 12
	nfCtrWords    = 16
)

// The programs.
const (
	nfProgFull = iota
	nfProgCheap
	nfProgVox
	nfProgShadowTri
	nfDrawPrograms
)

// nfSmallBrickPixels is the largest brick, across on screen, the compute
// rasteriser takes (nfoliage_voxraster.go) — under its splats' 4x4 pixels a
// voxel; larger ones are ray-marched boxes.
const nfSmallBrickPixels = 16

// nfStats is what a camera pass did a frame or two ago (its counters, read
// back without waiting): for the F3 overlay, the benchmark, the lists'
// growth and the tests.
type nfStats struct {
	// voxRefs is every voxel brick drawn, smallBricks those the compute
	// rasteriser took.
	items, refs, voxRefs, smallBricks int
	overflow                          uint32
}

type nfRenderer struct {
	ok     bool
	failed bool
	data   nfAssetData
	// cacheName is the assets' entry in the cache (nfoliage_cache.go), set
	// by FoliageInit from the templates it built.
	cacheName string
	// cmdOf is each cluster's draw command (a voxel cluster's as boxes) and
	// smallCmdOf a voxel cluster's for the compute rasteriser's small
	// bricks; groupFirst/Count each program group's run of commands.
	cmdOf      []int
	smallCmdOf []int
	groupFirst [nfGroups]int
	groupCount [nfGroups]int
	cmdInit    []uint32

	progs [nfDrawPrograms]progUniforms
	// paramsAt is the frame each program (the cull's first) last had the
	// shape table.
	paramsAt [nfDrawPrograms + 1]uint64
	cull     progUniforms
	scan     progUniforms
	scatter  progUniforms
	bones    progUniforms

	vao, vbo, ebo          uint32
	boxVAO, boxVBO, boxEBO uint32

	// Static: the compute stages' clusters, buckets and bones, and the
	// vertex stages' texture buffers.
	clusterSSBO, bucketSSBO, boneSSBO   uint32
	clusterTB, partTB, brickTB, voxelTB texBuffer
	pose                                [2]texBuffer
	poseCur                             int
	posedFrame                          uint64
	posedOnce                           bool
	prevTime, curTime                   float32

	// Per pass.
	tileBuf, cmdBuf, cursorBuf, counterBuf uint32
	items, terrains                        texBuffer
	refBuf                                 uint32
	itemCap, refCap                        int
	growItems, growRefs                    bool
	hiz                                    hiZPyramid
	readback                               [3]readbackSlot
	rbNext                                 int

	tiles     []uint32
	terrainMx []float32
	last      nfStats
	vox       nfVoxRaster
	// canopies are the variants' crowns for the GI field, baked when first
	// asked for (nfoliage_gi.go).
	canopies map[int]*giMeshSDF
	// flush submits the GL's queued work around the path's passes: on
	// Mesa's D3D12 driver (see nfFlushD3D12).
	flush bool
}

// texBuffer is a buffer and a texture-buffer view of it.
type texBuffer struct {
	buf, tex uint32
	format   uint32
	size     int
}

func (t *texBuffer) make(format uint32, data []byte, size int) {
	t.format = format
	if t.buf == 0 {
		gl.GenBuffers(1, &t.buf)
		gl.GenTextures(1, &t.tex)
	}
	t.size = max(size, 16)
	gl.BindBuffer(gl.TEXTURE_BUFFER, t.buf)
	var p unsafe.Pointer
	if len(data) > 0 {
		p = gl.Ptr(data)
	}
	gl.BufferData(gl.TEXTURE_BUFFER, t.size, p, gl.STATIC_DRAW)
	gl.BindTexture(gl.TEXTURE_BUFFER, t.tex)
	gl.TexBuffer(gl.TEXTURE_BUFFER, format, t.buf)
	gl.BindTexture(gl.TEXTURE_BUFFER, 0)
	gl.BindBuffer(gl.TEXTURE_BUFFER, 0)
}

func (t *texBuffer) bind(unit uint32) {
	gl.ActiveTexture(gl.TEXTURE0 + unit)
	gl.BindTexture(gl.TEXTURE_BUFFER, t.tex)
}

func (t *texBuffer) release() {
	if t.buf != 0 {
		gl.DeleteBuffers(1, &t.buf)
		gl.DeleteTextures(1, &t.tex)
		t.buf, t.tex = 0, 0
	}
}

// active is whether this frame's foliage takes the Nanite path.
func (r *nfRenderer) active() bool { return r.ok && NaniteFoliage }

// takes is whether the Nanite path draws shape this frame (the classic tiers
// draw what it does not).
func (r *nfRenderer) takes(s world.FoliageShape) bool {
	return r.active() && (NaniteFoliageGround || foliageShapes[s].impostor)
}

// leavesAny is whether the Nanite path leaves any shape to the classic
// tiers this frame.
func (r *nfRenderer) leavesAny() bool { return !NaniteFoliageGround }

// init builds the assets and brings the path up, after FoliageInit has the
// classic path's pool, atlas and parameters.
func (r *nfRenderer) init(atlas nfAtlas) {
	r.ok = false
	if r.failed || !NaniteFoliage {
		return
	}
	var units int32
	gl.GetIntegerv(gl.MAX_COMBINED_TEXTURE_IMAGE_UNITS, &units)
	if units < nfUnitsNeeded {
		log.Printf("foliage: Nanite path needs %d texture units, the driver has %d — the classic tiers draw", nfUnitsNeeded, units)
		r.failed = true
		return
	}
	r.release()
	if r.data.buckets == nil {
		r.data = nfAssetsCached(r.cacheName, atlas)
	}
	if err := r.build(); err != nil {
		log.Printf("foliage: Nanite path: %v — the classic tiers draw", err)
		r.failed = true
		return
	}
	r.ok = true
	r.flush = nfFlushD3D12(gl.GoStr(gl.GetString(gl.RENDERER)))
	r.vox.init(r)
	log.Printf("foliage: Nanite path on: %d variants, %d clusters, %d parts, %d bricks, voxel raster %s",
		len(r.data.buckets), len(r.data.clusters), len(r.data.parts), len(r.data.bricks), r.vox.mode())
}

// nfSnippet is what every Nanite foliage vertex stage is preceded by.
func nfSnippet() string {
	return fmt.Sprintf("#define FOLIAGE_SHAPES %d\n#define FOLIAGE_BUCKETS %d\n#define NF_SLOTS %d\n#define NF_GUSTS %d\n#define NF_FLUTTERS %d\n",
		world.FoliageShapeCount, foliage.buckets, nfSlots, nfGusts, nfFlutters) +
		mustReadShader(biomeShaderPath("foliage_params.glsl")) + "\n" +
		mustReadShader(biomeShaderPath("nfoliage_common.glsl"))
}

// nfCompute compiles a compute stage with the defines and, when withParams,
// foliage_params.glsl ahead of it.
func nfCompute(name string, withParams bool) (progUniforms, error) {
	src := "#version 430\n" + fmt.Sprintf("#define FOLIAGE_SHAPES %d\n#define FOLIAGE_BUCKETS %d\n#define NF_SLOTS %d\n#define NF_GUSTS %d\n#define NF_FLUTTERS %d\n",
		world.FoliageShapeCount, foliage.buckets, nfSlots, nfGusts, nfFlutters)
	if withParams {
		src += mustReadShader(biomeShaderPath("foliage_params.glsl")) + "\n"
	}
	src += mustReadShader(biomeShaderPath(name))
	cs, err := gfx.CompileShader(src, gl.COMPUTE_SHADER)
	if err != nil {
		return progUniforms{}, fmt.Errorf("%s: %v", name, err)
	}
	prog, err := linkStages(cs)
	if err != nil {
		return progUniforms{}, fmt.Errorf("%s: %v", name, err)
	}
	return progUniforms{prog: prog, loc: map[string]int32{}}, nil
}

func (r *nfRenderer) build() (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	mk := func(prog uint32) progUniforms { return progUniforms{prog: prog, loc: map[string]int32{}} }
	common := nfSnippet()
	r.progs[nfProgFull] = mk(mustBiomeProgram(biomeShaderPath("nfoliage_vert.glsl"), biomeShaderPath("nfoliage_frag.glsl"),
		biomeLightSnippet(1, false, false)+"\n"+common, biomeLightSnippet(0, false, false)))
	r.progs[nfProgCheap] = mk(mustBiomeProgram(biomeShaderPath("nfoliage_vert.glsl"), biomeShaderPath("nfoliage_frag.glsl"),
		biomeLightSnippet(0, false, false)+"\n"+common, biomeLightSnippet(0, false, false)))
	r.progs[nfProgVox] = mk(mustBiomeProgram(biomeShaderPath("nfoliage_vox_vert.glsl"), biomeShaderPath("nfoliage_vox_frag.glsl"),
		common, biomeLightSnippet(0, false, false)+"\n#define NF_VOXEL_SHADE 1\n"+mustReadShader(biomeShaderPath("nfoliage_voxel.glsl"))))
	r.progs[nfProgShadowTri] = mk(mustBiomeProgram(biomeShaderPath("nfoliage_shadow_vert.glsl"), biomeShaderPath("nfoliage_shadow_frag.glsl"),
		common, ""))
	for _, c := range []struct {
		name   string
		params bool
		out    *progUniforms
	}{
		{"nfoliage_cull_comp.glsl", true, &r.cull},
		{"nfoliage_scan_comp.glsl", false, &r.scan},
		{"nfoliage_scatter_comp.glsl", false, &r.scatter},
		{"nfoliage_bones_comp.glsl", false, &r.bones},
	} {
		p, cerr := nfCompute(c.name, c.params)
		if cerr != nil {
			return cerr
		}
		*c.out = p
	}
	if err := r.hiz.init("nanite foliage", foliageHiZUnit); err != nil {
		return err
	}
	r.upload()
	return nil
}

// upload sends the assets to the GPU and lays the commands out.
func (r *nfRenderer) upload() {
	d := &r.data
	// Commands, grouped by program: a voxel cluster has two, its bricks as
	// boxes and its small bricks for the compute rasteriser (never drawn).
	r.cmdOf = make([]int, len(d.clusters))
	r.smallCmdOf = make([]int, len(d.clusters))
	r.cmdInit = r.cmdInit[:0]
	n := 0
	for g := 0; g < nfGroups; g++ {
		r.groupFirst[g] = n
		for ci := range d.clusters {
			c := &d.clusters[ci]
			switch {
			case g == nfGroupSmall && c.kind == nfKindVoxels:
				r.smallCmdOf[ci] = n
				r.cmdInit = append(r.cmdInit, 1, 0, 0, 0, 0)
			case c.group != g:
				continue
			case c.kind == nfKindVoxels:
				r.cmdOf[ci] = n
				r.cmdInit = append(r.cmdInit, 36, 0, 0, 0, 0)
			default:
				r.cmdOf[ci] = n
				r.cmdInit = append(r.cmdInit, uint32(c.indexCount), 0, uint32(c.firstIndex), uint32(c.baseVertex), 0)
			}
			n++
		}
		r.groupCount[g] = n - r.groupFirst[g]
	}

	// The compute stages' clusters (NFCluster, 96 bytes) and the vertex
	// stages' (two texels).
	cs := make([]uint32, 0, len(d.clusters)*24)
	ct := make([]uint32, 0, len(d.clusters)*8)
	f := math.Float32bits
	for ci := range d.clusters {
		c := &d.clusters[ci]
		first := c.firstPart
		count := c.parts
		if c.kind == nfKindVoxels {
			first, count = c.firstBrick, c.bricks
		}
		for _, v := range []mgl32.Vec4{c.sphere, c.lod, c.parent, {c.err, c.perr, c.voxelSize, 0}} {
			cs = append(cs, f(v[0]), f(v[1]), f(v[2]), f(v[3]))
		}
		cs = append(cs, uint32(c.kind), uint32(c.bucket), uint32(r.cmdOf[ci]), uint32(c.mult),
			uint32(first), uint32(count), uint32(r.smallCmdOf[ci]), 0)
		ct = append(ct, uint32(c.kind), uint32(c.bucket), uint32(first), uint32(d.buckets[c.bucket].firstBone),
			f(c.voxelSize), uint32(c.mult), 0, 0)
	}
	bk := make([]uint32, 0, len(d.buckets)*nfBucketWords)
	for bi := range d.buckets {
		b := &d.buckets[bi]
		sph := r.bucketSphere(bi)
		// The shadow proxy's clusters are the bucket's tail (nfAssetData.pack):
		// a pass walks only its own half.
		dag := b.clusters
		for dag > 0 && d.clusters[b.firstCluster+dag-1].kind == nfKindProxy {
			dag--
		}
		bk = append(bk, uint32(b.firstBone), uint32(b.bones), uint32(b.firstCluster), uint32(b.clusters),
			f(sph[0]), f(sph[1]), f(sph[2]), f(sph[3]),
			uint32(dag), uint32(b.clusters-dag), 0, 0)
	}
	bones := make([]float32, 0, len(d.bones)*12)
	for _, b := range d.bones {
		bones = append(bones, b.pivot[:]...)
		bones = append(bones, b.tip[:]...)
		bones = append(bones, b.params[:]...)
	}
	ssbo := func(buf *uint32, data []byte) {
		if *buf == 0 {
			gl.GenBuffers(1, buf)
		}
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, *buf)
		var p unsafe.Pointer
		if len(data) > 0 {
			p = gl.Ptr(data)
		}
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, max(len(data), 16), p, gl.STATIC_DRAW)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	}
	ssbo(&r.clusterSSBO, u32bytes(cs))
	ssbo(&r.bucketSSBO, u32bytes(bk))
	ssbo(&r.boneSSBO, f32bytes(bones))
	r.clusterTB.make(gl.RGBA32UI, u32bytes(ct), len(ct)*4)
	parts := make([]float32, 0, len(d.parts)*16)
	for _, p := range d.parts {
		parts = append(parts, p.right[:]...)
		parts = append(parts, p.up[:]...)
		parts = append(parts, p.n[:]...)
		parts = append(parts, p.base[:]...)
	}
	r.partTB.make(gl.RGBA32F, f32bytes(parts), len(parts)*4)
	bricks := make([]uint32, 0, len(d.bricks)*8)
	for _, b := range d.bricks {
		bricks = append(bricks, f(b.Corner[0]), f(b.Corner[1]), f(b.Corner[2]), f(b.Corner[3]),
			b.Mask[0], b.Mask[1], b.First, b.Bind)
	}
	r.brickTB.make(gl.RGBA32UI, u32bytes(bricks), len(bricks)*4)
	voxels := make([]uint32, 0, len(d.voxels)*4)
	for _, v := range d.voxels {
		voxels = append(voxels, v[:]...)
	}
	r.voxelTB.make(gl.RGBA32UI, u32bytes(voxels), len(voxels)*4)
	poseBytes := len(d.bones) * nfSlots * 2 * 2 * 3 * 16
	for i := range r.pose {
		r.pose[i].make(gl.RGBA32F, nil, poseBytes)
	}
	r.posedOnce = false

	// The vertices: triangles, parts' templates.
	gl.GenVertexArrays(1, &r.vao)
	gl.GenBuffers(1, &r.vbo)
	gl.GenBuffers(1, &r.ebo)
	gl.GenBuffers(1, &r.refBuf)
	r.refCap = nfRefCap0
	gl.BindBuffer(gl.ARRAY_BUFFER, r.refBuf)
	gl.BufferData(gl.ARRAY_BUFFER, r.refCap*8, nil, gl.DYNAMIC_DRAW)
	gl.BindVertexArray(r.vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(d.verts)*4, gl.Ptr(d.verts), gl.STATIC_DRAW)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, r.ebo)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(d.idx)*4, gl.Ptr(d.idx), gl.STATIC_DRAW)
	stride := int32(nfVertexFloats * 4)
	for _, a := range []struct {
		loc uint32
		n   int32
		off int
	}{{0, 3, 0}, {1, 3, 3}, {2, 2, 6}, {3, 3, 8}, {5, 2, 11}} {
		gl.EnableVertexAttribArray(a.loc)
		gl.VertexAttribPointerWithOffset(a.loc, a.n, gl.FLOAT, false, stride, uintptr(a.off*4))
	}
	r.bindRefs()
	gl.BindVertexArray(0)

	// The box a brick is marched in: its corners in 0..1, outward-wound.
	corners := []float32{0, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0, 0, 0, 1, 1, 0, 1, 1, 1, 1, 0, 1, 1}
	faces := []uint32{
		0, 3, 2, 0, 2, 1, // -z
		4, 5, 6, 4, 6, 7, // +z
		0, 4, 7, 0, 7, 3, // -x
		1, 2, 6, 1, 6, 5, // +x
		0, 1, 5, 0, 5, 4, // -y
		3, 7, 6, 3, 6, 2, // +y
	}
	gl.GenVertexArrays(1, &r.boxVAO)
	gl.GenBuffers(1, &r.boxVBO)
	gl.GenBuffers(1, &r.boxEBO)
	gl.BindVertexArray(r.boxVAO)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.boxVBO)
	gl.BufferData(gl.ARRAY_BUFFER, len(corners)*4, gl.Ptr(corners), gl.STATIC_DRAW)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, r.boxEBO)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(faces)*4, gl.Ptr(faces), gl.STATIC_DRAW)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 12, 0)
	r.bindRefs()
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)

	// The per-pass buffers.
	for _, b := range []*uint32{&r.tileBuf, &r.cmdBuf, &r.cursorBuf, &r.counterBuf} {
		gl.GenBuffers(1, b)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, r.cursorBuf)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, max(len(r.cmdInit)/5, 1)*8, nil, gl.DYNAMIC_DRAW)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	r.itemCap = nfItemCap0
	r.items.make(gl.RGBA32UI, nil, r.itemCap*16)
	r.terrains.make(gl.RGBA32F, nil, 64*16)
	for i := range r.readback {
		r.readback[i].init(nfCtrWords*4, false)
	}
}

// bindRefs points attribute 4 at the reference list; a VAO must be bound.
func (r *nfRenderer) bindRefs() {
	gl.BindBuffer(gl.ARRAY_BUFFER, r.refBuf)
	gl.EnableVertexAttribArray(4)
	gl.VertexAttribIPointerWithOffset(4, 2, gl.UNSIGNED_INT, 8, 0)
	gl.VertexAttribDivisor(4, 1)
}

// bucketSphere is a sphere round every cluster of bucket bi.
func (r *nfRenderer) bucketSphere(bi int) mgl32.Vec4 {
	b := &r.data.buckets[bi]
	cs := r.data.clusters[b.firstCluster : b.firstCluster+b.clusters]
	lo, hi := cs[0].sphere.Vec3(), cs[0].sphere.Vec3()
	for _, c := range cs {
		e := mgl32.Vec3{c.sphere[3], c.sphere[3], c.sphere[3]}
		lo = mgl32.Vec3{min(lo[0], c.sphere[0]-e[0]), min(lo[1], c.sphere[1]-e[1]), min(lo[2], c.sphere[2]-e[2])}
		hi = mgl32.Vec3{max(hi[0], c.sphere[0]+e[0]), max(hi[1], c.sphere[1]+e[1]), max(hi[2], c.sphere[2]+e[2])}
	}
	centre := lo.Add(hi).Mul(0.5)
	rad := float32(0)
	for _, c := range cs {
		rad = max(rad, c.sphere.Vec3().Sub(centre).Len()+c.sphere[3])
	}
	return centre.Vec4(rad * 1.001)
}

// poseBones poses this frame's bones, once a frame, keeping last frame's.
func (r *nfRenderer) poseBones() {
	if r.posedOnce && r.posedFrame == foliage.frame {
		return
	}
	ProfileBegin("foliage: bones")
	defer ProfileEnd()
	first := !r.posedOnce
	r.posedOnce, r.posedFrame = true, foliage.frame
	r.prevTime, r.curTime = r.curTime, BiomeWindTime
	if first {
		r.prevTime = r.curTime
	}
	r.poseCur ^= 1
	b := &r.bones
	gl.UseProgram(b.prog)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 5, r.bucketSSBO)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 9, r.boneSSBO)
	gl.Uniform1i(b.U("bucketCount"), int32(len(r.data.buckets)))
	gl.Uniform1f(b.U("windStrength"), foliageWind.strength)
	threads := uint32(len(r.data.buckets) * nfSlots * 2)
	for k := 0; k < 2; k++ {
		if k == 1 && !first {
			break
		}
		// The first frame poses last frame's too: nothing moved before it.
		dst := r.pose[r.poseCur]
		t := r.curTime
		if k == 1 {
			dst = r.pose[r.poseCur^1]
			t = r.prevTime
		}
		gl.Uniform1f(b.U("time"), t)
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 10, dst.buf)
		gl.DispatchCompute((threads+63)/64, 1, 1)
	}
	gl.MemoryBarrier(gl.TEXTURE_FETCH_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT)
	gl.UseProgram(gfx.Program)
}

// nfView is what one pass draws for.
type nfView struct {
	projView mgl32.Mat4
	frustum  *Frustum
	// shadow: a sun's shadow pass, which casts the shadow proxies.
	shadow bool
	useHiz bool
	// viewport is the pass's; pxPerUnit a camera pass's pixels per unit at
	// distance 1.
	viewport  [4]int32
	pxPerUnit float32
}

// nfCameraView is the camera pass's view.
func nfCameraView(projView mgl32.Mat4, frustum *Frustum) nfView {
	vp := boundViewport()
	return nfView{projView: projView, frustum: frustum, viewport: vp,
		pxPerUnit: float32(vp[3]) * gfx.Projection[5] / 2}
}

// nfShadowView is a sun shadow pass's view.
func nfShadowView(lsm mgl32.Mat4, cull *Frustum) nfView {
	v := nfView{projView: lsm, frustum: cull, shadow: true, viewport: boundViewport()}
	if cull == nil {
		f := ExtractFrustum(lsm)
		v.frustum = &f
	}
	return v
}

// gather builds the pass's tiles and terrain list from this frame's units.
// Returns how many instances it submits.
func (r *nfRenderer) gather(v nfView) int {
	fr := &foliage
	r.tiles = r.tiles[:0]
	r.terrainMx = r.terrainMx[:0]
	byModel := map[*mgl32.Mat4]uint32{}
	submitted := 0
	reach := float32(math.MaxFloat32)
	if v.shadow {
		reach = foliageShadowRange * FoliageDistanceScale()
	}
	for di := range fr.draws {
		dr := &fr.draws[di]
		if dr.d > reach {
			continue
		}
		if v.frustum != nil && !AABBInFrustum(*v.frustum, dr.wlo, dr.whi) {
			continue
		}
		ti, ok := byModel[dr.model]
		if !ok {
			ti = uint32(len(r.terrainMx) / 16)
			byModel[dr.model] = ti
			r.terrainMx = append(r.terrainMx, dr.model[:]...)
		}
		for bi := range dr.u.batches {
			b := &dr.u.batches[bi]
			if !r.takes(b.shape) || (v.shadow && !foliageShapes[b.shape].casts) {
				continue
			}
			p := fr.params[b.shape]
			if dr.d > p.rng {
				continue
			}
			n := min(int32(float32(b.count)*foliageKeepRank(p, dr.d)+0.999), b.count)
			bucket := nfBucketOf(b.shape, b.variant)
			for off := int32(0); off < n; off += foliageCullTile {
				cnt := min(n-off, foliageCullTile)
				r.tiles = append(r.tiles, uint32(b.first+off), uint32(cnt), uint32(bucket), ti)
			}
			submitted += int(n)
		}
	}
	return submitted
}

// cullPass runs the cull, scan and scatter for one view. False when there
// is nothing to draw.
func (r *nfRenderer) cullPass(v nfView) bool {
	if len(r.tiles) == 0 {
		return false
	}
	ProfileBegin("foliage: nanite cull")
	defer ProfileEnd()
	// Grow what last frames ran out of.
	if r.growItems {
		r.growItems = false
		r.itemCap *= 2
		r.items.make(gl.RGBA32UI, nil, r.itemCap*16)
	}
	if r.growRefs {
		r.growRefs = false
		r.refCap *= 2
		gl.BindBuffer(gl.ARRAY_BUFFER, r.refBuf)
		gl.BufferData(gl.ARRAY_BUFFER, r.refCap*8, nil, gl.DYNAMIC_DRAW)
		gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	}
	upload := func(target, buf uint32, data []byte) {
		gl.BindBuffer(target, buf)
		gl.BufferData(target, len(data), gl.Ptr(data), gl.STREAM_DRAW)
		gl.BindBuffer(target, 0)
	}
	upload(gl.SHADER_STORAGE_BUFFER, r.tileBuf, u32bytes(r.tiles))
	// A pass resets and lays out only its own commands: a shadow pass's are
	// the proxies (the last group), a camera pass's everything before them.
	// Orphaned first, so the last pass's draws never hold it up; the whole
	// table every pass cost ~0.25 ms of upload and scan per shadow
	// rectangle once trees ran to thousands of clusters.
	cmdFirst, cmdCount := 0, r.groupFirst[nfGroupProxy]
	if v.shadow {
		cmdFirst, cmdCount = r.groupFirst[nfGroupProxy], r.groupCount[nfGroupProxy]
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, r.cmdBuf)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(r.cmdInit)*4, nil, gl.STREAM_DRAW)
	if cmdCount > 0 {
		gl.BufferSubData(gl.SHADER_STORAGE_BUFFER, cmdFirst*5*4, cmdCount*5*4, gl.Ptr(r.cmdInit[cmdFirst*5:]))
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	ctr := make([]uint32, nfCtrWords)
	ctr[nfCtrItemCap], ctr[nfCtrRefCap] = uint32(r.itemCap), uint32(r.refCap)
	upload(gl.SHADER_STORAGE_BUFFER, r.counterBuf, u32bytes(ctr))
	if need := len(r.terrainMx) * 4; need > r.terrains.size {
		r.terrains.make(gl.RGBA32F, f32bytes(r.terrainMx), need*2)
	} else {
		gl.BindBuffer(gl.TEXTURE_BUFFER, r.terrains.buf)
		gl.BufferSubData(gl.TEXTURE_BUFFER, 0, need, gl.Ptr(r.terrainMx))
		gl.BindBuffer(gl.TEXTURE_BUFFER, 0)
	}

	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, r.tileBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 1, r.cmdBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 2, r.items.buf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 3, r.refBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 4, r.clusterSSBO)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 5, r.bucketSSBO)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 6, r.cursorBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 12, r.terrains.buf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 13, r.counterBuf)

	c := &r.cull
	gl.UseProgram(c.prog)
	if r.paramsAt[0] != foliage.frame || foliage.frame == 0 {
		uploadFoliageParams(c.U("shapeRange"), c.U("shapeMore"), c.U("bucketInfo"), c.U("bucketImp"), c.U("shapeBark"))
		r.paramsAt[0] = foliage.frame
	}
	lod := currentNaniteLOD()
	gl.Uniform3f(c.U("camPos"), lod.eye[0], lod.eye[1], lod.eye[2])
	var planes [6]mgl32.Vec4
	if v.frustum != nil {
		planes = v.frustum.Planes
	}
	gl.Uniform4fv(c.U("planes"), 6, &planes[0][0])
	gl.UniformMatrix4fv(c.U("projView"), 1, false, &v.projView[0])
	gl.Uniform1i(c.U("useHiz"), boolToInt32(v.useHiz))
	gl.Uniform1i(c.U("hiz"), foliageHiZUnit)
	gl.Uniform2i(c.U("hizSize"), r.hiz.size[0], r.hiz.size[1])
	gl.Uniform1i(c.U("hizLevels"), r.hiz.levels)
	gl.Uniform1i(c.U("instances"), foliageInstanceUnit)
	gl.Uniform1f(c.U("lodK"), lod.k*gfx.NaniteLODError/naniteFoliagePixels)
	gl.Uniform1i(c.U("shadowPass"), boolToInt32(v.shadow))
	gl.Uniform1f(c.U("pxPerUnit"), v.pxPerUnit)
	small := float32(0)
	if r.vox.ok && !v.shadow {
		small = nfSmallBrickPixels
	}
	gl.Uniform1f(c.U("smallMax"), small)
	gl.Uniform1f(c.U("shadowReach"), foliageShadowRange*FoliageDistanceScale())
	if v.useHiz {
		gl.ActiveTexture(gl.TEXTURE0 + foliageHiZUnit)
		gl.BindTexture(gl.TEXTURE_2D, r.hiz.tex)
	}
	gl.ActiveTexture(gl.TEXTURE0 + foliageInstanceUnit)
	gl.BindTexture(gl.TEXTURE_BUFFER, foliage.pool.tex)
	gl.ActiveTexture(gl.TEXTURE0)
	tiles := len(r.tiles) / 4
	for done := 0; done < tiles; done += 65535 {
		gl.Uniform1ui(c.U("tileBase"), uint32(done))
		gl.DispatchCompute(uint32(min(tiles-done, 65535)), 1, 1)
	}
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)

	s := &r.scan
	gl.UseProgram(s.prog)
	gl.Uniform1ui(s.U("cmdFirst"), uint32(cmdFirst))
	gl.Uniform1ui(s.U("cmdCount"), uint32(cmdCount))
	voxCount, boxCount := r.groupCount[nfGroupSmall], r.groupCount[nfGroupVoxel]
	if v.shadow {
		voxCount, boxCount = 0, 0
	}
	gl.Uniform1ui(s.U("voxFirst"), uint32(r.groupFirst[nfGroupSmall]))
	gl.Uniform1ui(s.U("voxCount"), uint32(voxCount))
	gl.Uniform1ui(s.U("boxFirst"), uint32(r.groupFirst[nfGroupVoxel]))
	gl.Uniform1ui(s.U("boxCount"), uint32(boxCount))
	gl.DispatchCompute(1, 1, 1)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT | gl.COMMAND_BARRIER_BIT)

	gl.UseProgram(r.scatter.prog)
	gl.BindBuffer(gl.DISPATCH_INDIRECT_BUFFER, r.counterBuf)
	gl.DispatchComputeIndirect(nfCtrScatter * 4)
	gl.BindBuffer(gl.DISPATCH_INDIRECT_BUFFER, 0)
	gl.MemoryBarrier(gl.COMMAND_BARRIER_BIT | gl.VERTEX_ATTRIB_ARRAY_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT)
	gl.UseProgram(gfx.Program)
	return true
}

// bindVertexData binds what every Nanite foliage vertex stage reads.
func (r *nfRenderer) bindVertexData(p *progUniforms) {
	r.items.bind(nfItemsUnit)
	r.clusterTB.bind(nfClustersUnit)
	r.partTB.bind(nfPartsUnit)
	r.brickTB.bind(nfBricksUnit)
	r.voxelTB.bind(nfVoxelsUnit)
	r.pose[r.poseCur].bind(nfPoseUnit)
	r.pose[r.poseCur^1].bind(nfPosePrevUnit)
	r.terrains.bind(nfTerrainsUnit)
	gl.ActiveTexture(gl.TEXTURE0 + foliageInstanceUnit)
	gl.BindTexture(gl.TEXTURE_BUFFER, foliage.pool.tex)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, foliage.atlas)
	for name, unit := range map[string]int32{"nfItems": nfItemsUnit, "nfClusters": nfClustersUnit, "nfParts": nfPartsUnit,
		"nfBricks": nfBricksUnit, "nfVoxels": nfVoxelsUnit, "nfPose": nfPoseUnit, "nfPosePrev": nfPosePrevUnit,
		"nfTerrains": nfTerrainsUnit, "instances": foliageInstanceUnit, "atlas": 0} {
		gl.Uniform1i(p.U(name), unit)
	}
	gl.Uniform1f(p.U("time"), r.curTime)
	gl.Uniform1f(p.U("prevTime"), r.prevTime)
	gl.Uniform2f(p.U("windDir"), foliageWind.dir[0], foliageWind.dir[1])
	gl.Uniform1f(p.U("windStrength"), foliageWind.strength)
	cam := gfx.Cam.Position
	gl.Uniform3f(p.U("camPos"), cam[0], cam[1], cam[2])
}

// useProgram readies draw program i for a pass.
func (r *nfRenderer) useProgram(i int, lvl *world.Level, v nfView, frame ShadowFrame, motion bool) *progUniforms {
	p := &r.progs[i]
	if i == nfProgShadowTri {
		gl.UseProgram(p.prog)
	} else {
		uploadBiomeLighting(p.prog, lvl, frame)
		gl.Uniform1i(p.U("alphaToCoverage"), boolToInt32(alphaToCoverage()))
		gl.Uniform1i(p.U("coverageDither"), coverageDither())
	}
	if r.paramsAt[1+i] != foliage.frame || foliage.frame == 0 {
		r.paramsAt[1+i] = foliage.frame
		uploadFoliageParams(p.U("shapeRange"), p.U("shapeMore"), p.U("bucketInfo"), p.U("bucketImp"), p.U("shapeBark"))
	}
	gl.UniformMatrix4fv(p.U("projView"), 1, false, &v.projView[0])
	inv := v.projView.Inv()
	gl.UniformMatrix4fv(p.U("invProjView"), 1, false, &inv[0])
	gl.Uniform4f(p.U("viewport"), float32(v.viewport[0]), float32(v.viewport[1]), float32(v.viewport[2]), float32(v.viewport[3]))
	r.bindVertexData(p)
	gl.Uniform1i(p.U("writeMotion"), boolToInt32(motion))
	if motion {
		vp, prev := taa.viewProj, taa.prevViewProj
		if !taa.prevValid {
			prev = vp
		}
		gl.UniformMatrix4fv(p.U("motionViewProj"), 1, false, &vp[0])
		gl.UniformMatrix4fv(p.U("motionPrevViewProj"), 1, false, &prev[0])
	}
	return p
}

// drawGroup multi-draws group g's commands.
func (r *nfRenderer) drawGroup(g int) {
	if r.groupCount[g] == 0 {
		return
	}
	if g == nfGroupVoxel {
		gl.BindVertexArray(r.boxVAO)
	} else {
		gl.BindVertexArray(r.vao)
	}
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, r.cmdBuf)
	gl.MultiDrawElementsIndirect(gl.TRIANGLES, gl.UNSIGNED_INT, gl.PtrOffset(r.groupFirst[g]*5*4), int32(r.groupCount[g]), 0)
	gl.BindBuffer(gl.DRAW_INDIRECT_BUFFER, 0)
}

// drawBoxes draws the pass's voxel bricks as ray-marched boxes.
func (r *nfRenderer) drawBoxes(lvl *world.Level, v nfView, frame ShadowFrame, motion bool) {
	if r.groupCount[nfGroupVoxel] == 0 {
		return
	}
	r.useProgram(nfProgVox, lvl, v, frame, motion)
	gl.Enable(gl.CULL_FACE)
	gl.CullFace(gl.BACK)
	r.drawGroup(nfGroupVoxel)
	gl.Disable(gl.CULL_FACE)
}

// nfFlushD3D12 is whether the path submits the queued work (glFlush) ahead
// of its camera pass and after each shadow pass, from the driver's renderer
// string; FOUNDRY_NANITE_FOLIAGE_FLUSH=1/0 forces it.
//
// On Mesa's D3D12 driver (the GL WSLg gives a Windows GPU) a frame whose sun
// shadow passes ran this path's cull, followed by the camera pass's triangle
// draws, hung for good: the next frame's glBufferData of the cull's commands
// never returned (zz_foliage's broadleaf forest floor, looking back across
// it, in every TAA headless run; never natively, never with the shadow
// culls or the camera's triangles left out). The same family as
// NaniteVSMFinish (vsm.go). A glFlush between the shadow work and the
// camera's — either side — ends it; a glFinish there hangs as well.
func nfFlushD3D12(renderer string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FOUNDRY_NANITE_FOLIAGE_FLUSH"))) {
	case "1", "on", "true", "yes":
		return true
	case "0", "off", "false", "no":
		return false
	}
	return strings.Contains(renderer, "D3D12")
}

// render draws this frame's foliage for the camera. Returns how many
// instances it submitted.
func (r *nfRenderer) render(lvl *world.Level, projView mgl32.Mat4, frustum *Frustum, frame ShadowFrame) int {
	if r.flush {
		// See nfFlushD3D12.
		gl.Flush()
	}
	r.poseBones()
	v := nfCameraView(projView, frustum)
	submitted := r.gather(v)
	if submitted == 0 {
		return 0
	}
	v.useHiz = r.hiz.build()
	if !r.cullPass(v) {
		return 0
	}
	r.collectStats()
	DebugStats.NaniteFoliage = true
	DebugStats.NaniteFoliageItems, DebugStats.NaniteFoliageRefs = r.last.items, r.last.refs
	DebugStats.NaniteFoliageBricks, DebugStats.NaniteFoliageOverflow = r.last.voxRefs, r.last.overflow
	ProfileBegin("foliage: nanite draw")
	motion := taaActive()
	if motion {
		gl.DrawBuffers(3, &[]uint32{sceneAttachColour, gl.NONE, sceneAttachVelocity}[0])
	}
	for _, g := range []int{nfGroupFull, nfGroupCheap} {
		if r.groupCount[g] == 0 {
			continue
		}
		prog := nfProgFull
		if g == nfGroupCheap {
			prog = nfProgCheap
		}
		r.useProgram(prog, lvl, v, frame, motion)
		r.drawGroup(g)
	}
	ProfileEnd()
	if r.groupCount[nfGroupVoxel] > 0 {
		ProfileBegin("foliage: nanite voxels")
		r.drawBoxes(lvl, v, frame, motion)
		r.vox.draw(r, lvl, v, frame, motion)
		ProfileEnd()
	}
	if motion {
		drawBuffersColour()
	}
	r.copyStats()
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	gl.UseProgram(gfx.Program)
	return submitted
}

// renderShadow draws the casting foliage into the bound sun shadow layer
// with light-space matrix lsm. Restores Shadows.Program.
func (r *nfRenderer) renderShadow(lvl *world.Level, lsm mgl32.Mat4, cull *Frustum) {
	r.poseBones()
	v := nfShadowView(lsm, cull)
	if r.gather(v) == 0 || !r.cullPass(v) {
		return
	}
	// A shadow pass casts the shadow proxies alone (nfBuilt.buildProxy).
	r.useProgram(nfProgShadowTri, lvl, v, ShadowFrame{}, false)
	r.drawGroup(nfGroupProxy)
	if r.flush {
		// See nfFlushD3D12.
		gl.Flush()
	}
	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	gl.UseProgram(Shadows.Program)
}

// copyStats copies the camera pass's counters into the readback ring.
func (r *nfRenderer) copyStats() {
	s := &r.readback[r.rbNext]
	if s.pending {
		return
	}
	s.copyFrom(r.counterBuf, nfCtrWords*4, uint32(foliage.frame))
	r.rbNext = (r.rbNext + 1) % len(r.readback)
}

// collectStats reads whichever counters have landed: how much the last
// camera pass drew, and whether it ran out of room.
func (r *nfRenderer) collectStats() {
	for i := range r.readback {
		s := &r.readback[i]
		if !s.pending || !s.ready() {
			continue
		}
		w := s.read()
		s.done()
		s.pending = false
		if w == nil {
			continue
		}
		r.last = nfStats{items: int(min(w[nfCtrItems], w[nfCtrItemCap])), refs: int(w[nfCtrScatter+3]),
			voxRefs: int(w[nfCtrVoxRefs+1] + w[nfCtrVoxRefs+2]), smallBricks: int(w[nfCtrVoxRefs+1]), overflow: w[nfCtrOverflow]}
		if w[nfCtrOverflow]&1 != 0 && r.itemCap < 1<<24 {
			r.growItems = true
		}
		if w[nfCtrOverflow]&2 != 0 && r.refCap < 1<<26 {
			r.growRefs = true
		}
	}
}

// NaniteFoliageVoxelMode is how the Nanite foliage draws its voxels, for
// reports.
func NaniteFoliageVoxelMode() string { return foliage.nan.vox.mode() }

// release frees the path's GL objects (FoliageInit brings it up again).
// The assets, built on the CPU, are kept.
func (r *nfRenderer) release() {
	r.ok = false
	for _, p := range append(r.progs[:], r.cull, r.scan, r.scatter, r.bones) {
		if p.prog != 0 {
			gl.DeleteProgram(p.prog)
		}
	}
	for _, vao := range []*uint32{&r.vao, &r.boxVAO} {
		if *vao != 0 {
			gl.DeleteVertexArrays(1, vao)
			*vao = 0
		}
	}
	for _, b := range []*uint32{&r.vbo, &r.ebo, &r.boxVBO, &r.boxEBO, &r.clusterSSBO, &r.bucketSSBO, &r.boneSSBO,
		&r.tileBuf, &r.cmdBuf, &r.cursorBuf, &r.counterBuf, &r.refBuf} {
		if *b != 0 {
			gl.DeleteBuffers(1, b)
			*b = 0
		}
	}
	for _, t := range []*texBuffer{&r.clusterTB, &r.partTB, &r.brickTB, &r.voxelTB, &r.pose[0], &r.pose[1], &r.items, &r.terrains} {
		t.release()
	}
	for i := range r.readback {
		r.readback[i].release()
		r.readback[i] = readbackSlot{}
	}
	r.rbNext = 0
	r.hiz.release()
	r.vox.release()
	r.posedOnce = false
}
