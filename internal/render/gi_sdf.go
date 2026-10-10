package render

// gi_sdf.go — the GI distance field: a clipmap of signed distance around the
// camera, which the probes (gi.go) trace their rays through.
//
// LEVELS. A few cubes of giSdfRes³ voxels, centred on the camera, each with
// twice the voxel size of the one inside it: fine detail close by, reach far
// out, at a fixed cost. Each level is two 3D textures — the distance (R16F)
// and the albedo of the surface it measures to (RGBA8) — sampled with
// GL_REPEAT, because the cube scrolls by WRAPPING: a world voxel always lives
// at the same texel, and when the camera moves, the slab of bricks the cube
// leaves is overwritten by the slab it enters.
//
// BRICKS. A level is cut into giBricksPerSide³ bricks of 8³ voxels, and a
// brick is the unit of work. Every brick slot remembers which world brick it
// holds; a slot is rebuilt when that is not the one the cube wants there now
// (it scrolled) or when a source over it changed (gi_sources.go's dirty
// bounds). At most a budget of bricks is rebuilt a frame, nearest the camera
// first, so a level load or a big edit settles over a few frames rather than
// stalling one.
//
// The composite (gi_sdf_comp.glsl) runs one workgroup per brick, against the
// sources the CPU has found near it with a uniform-grid index.

import (
	"math"
	"sort"
	"unsafe"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const (
	giSdfRes        = 128
	giBrickVox      = 8
	giBricksPerSide = giSdfRes / giBrickVox
	giBrickSlots    = giBricksPerSide * giBricksPerSide * giBricksPerSide
	// giSdfBand is how many voxels deep a stored distance reaches; the shader
	// clamps to it (GI_SDF_BAND).
	giSdfBand      = 4
	giSdfMaxLevels = 4
	// giSdfFar is what a voxel no brick has written yet holds: open space.
	giSdfFar = 1000
	// giSourceFloats is one source as gi_sdf_comp.glsl's std430 Source reads
	// it: eight vec4s and a mat4.
	giSourceFloats = 48
)

type giBrickSlot struct {
	brick [3]int32
	valid bool
}

type giSdfLevel struct {
	voxel    float32
	sdf      uint32
	surf     uint32
	minBrick [3]int32
	slots    [giBrickSlots]giBrickSlot
}

// lowCorner is the level's first voxel's corner in world units.
func (l *giSdfLevel) lowCorner() mgl32.Vec3 {
	s := float32(giBrickVox) * l.voxel
	return mgl32.Vec3{float32(l.minBrick[0]) * s, float32(l.minBrick[1]) * s, float32(l.minBrick[2]) * s}
}

// giField is the clipmap and the buffers its composite reads.
type giField struct {
	levels []giSdfLevel
	prog   progUniforms

	bufBricks, bufLists, bufSrc, bufPlanes, bufMesh, bufBlocks uint32
	// The mesh grids and terrain blocks last uploaded, by identity, so an
	// entity moving does not re-upload every terrain in the level.
	meshSig, blockSig []uintptr
	meshOff           map[*giMeshSDF]int
	blockOff          map[*uint8]int

	index giIndex
	// pending is how many brick slots wanted rebuilding at the end of the
	// last build, for the profiler overlay.
	pending int
}

func newGIField(levels int, voxel0 float32, prog progUniforms) *giField {
	f := &giField{prog: prog, meshOff: map[*giMeshSDF]int{}, blockOff: map[*uint8]int{}}
	f.levels = make([]giSdfLevel, levels)
	far := make([]float32, giSdfRes*giSdfRes*giSdfRes)
	for i := range far {
		far[i] = giSdfFar
	}
	for i := range f.levels {
		l := &f.levels[i]
		l.voxel = voxel0 * float32(int(1)<<i)
		l.sdf = newGITexture3D(gl.R16F, gl.RED, gl.FLOAT, unsafe.Pointer(&far[0]))
		l.surf = newGITexture3D(gl.RGBA8, gl.RGBA, gl.UNSIGNED_BYTE, nil)
		for s := range l.slots {
			l.slots[s].brick = [3]int32{math.MaxInt32, 0, 0}
		}
	}
	for _, b := range []*uint32{&f.bufBricks, &f.bufLists, &f.bufSrc, &f.bufPlanes, &f.bufMesh, &f.bufBlocks} {
		gl.GenBuffers(1, b)
	}
	// An empty storage buffer cannot be bound; every one holds at least a
	// word from the start.
	zero := [4]uint32{}
	for _, b := range []uint32{f.bufBricks, f.bufLists, f.bufSrc, f.bufPlanes, f.bufMesh, f.bufBlocks} {
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, b)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, 16, gl.Ptr(&zero[0]), gl.STATIC_DRAW)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	return f
}

func newGITexture3D(internal int32, format, typ uint32, data unsafe.Pointer) uint32 {
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_3D, tex)
	gl.TexImage3D(gl.TEXTURE_3D, 0, internal, giSdfRes, giSdfRes, giSdfRes, 0, format, typ, data)
	gl.TexParameteri(gl.TEXTURE_3D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_3D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_3D, gl.TEXTURE_WRAP_S, gl.REPEAT)
	gl.TexParameteri(gl.TEXTURE_3D, gl.TEXTURE_WRAP_T, gl.REPEAT)
	gl.TexParameteri(gl.TEXTURE_3D, gl.TEXTURE_WRAP_R, gl.REPEAT)
	gl.BindTexture(gl.TEXTURE_3D, 0)
	return tex
}

func (f *giField) release() {
	for i := range f.levels {
		gl.DeleteTextures(1, &f.levels[i].sdf)
		gl.DeleteTextures(1, &f.levels[i].surf)
	}
	for _, b := range []*uint32{&f.bufBricks, &f.bufLists, &f.bufSrc, &f.bufPlanes, &f.bufMesh, &f.bufBlocks} {
		gl.DeleteBuffers(1, b)
	}
	f.levels = nil
}

// recentre moves every level's cube to sit on the camera.
func (f *giField) recentre(cam mgl32.Vec3) {
	for i := range f.levels {
		l := &f.levels[i]
		s := float32(giBrickVox) * l.voxel
		for k := 0; k < 3; k++ {
			l.minBrick[k] = int32(math.Floor(float64(cam[k]/s))) - giBricksPerSide/2
		}
	}
}

// invalidateAll marks every brick of every level for rebuilding.
func (f *giField) invalidateAll() {
	for i := range f.levels {
		for s := range f.levels[i].slots {
			f.levels[i].slots[s].valid = false
		}
	}
}

// invalidate marks the bricks a changed source's bounds reach — widened by the
// band, since a voxel that far from the source measured its distance to it.
func (f *giField) invalidate(lo, hi mgl32.Vec3) {
	for i := range f.levels {
		l := &f.levels[i]
		band := float32(giSdfBand) * l.voxel
		s := float32(giBrickVox) * l.voxel
		var b0, b1 [3]int32
		for k := 0; k < 3; k++ {
			b0[k] = max(int32(math.Floor(float64((lo[k]-band)/s))), l.minBrick[k])
			b1[k] = min(int32(math.Floor(float64((hi[k]+band)/s))), l.minBrick[k]+giBricksPerSide-1)
			if b0[k] > b1[k] {
				b0[0] = 1
				b1[0] = 0
				break
			}
		}
		for z := b0[2]; z <= b1[2]; z++ {
			for y := b0[1]; y <= b1[1]; y++ {
				for x := b0[0]; x <= b1[0]; x++ {
					l.slots[giBrickSlot3(x, y, z)].valid = false
				}
			}
		}
	}
}

// giBrickSlot3 is the slot a world brick wraps to.
func giBrickSlot3(x, y, z int32) int {
	w := func(v int32) int32 { return ((v % giBricksPerSide) + giBricksPerSide) % giBricksPerSide }
	return int((w(z)*giBricksPerSide+w(y))*giBricksPerSide + w(x))
}

// uploadSources sends the flattened sources to the GPU and rebuilds the index,
// after a frame in which any of them changed.
func (f *giField) uploadSources(flat []giSource, nStatic int, staticChanged bool) {
	// Mesh grids and terrain blocks: only when the set of them changed.
	var meshSig, blockSig []uintptr
	for i := range flat {
		if m := flat[i].mesh; m != nil {
			meshSig = append(meshSig, uintptr(unsafe.Pointer(m)))
		}
		if b := flat[i].blocks; len(b) > 0 {
			blockSig = append(blockSig, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
		}
	}
	sortUintptrs(meshSig)
	sortUintptrs(blockSig)
	if !equalUintptrs(meshSig, f.meshSig) {
		f.meshSig = meshSig
		f.meshOff = map[*giMeshSDF]int{}
		var data []float32
		for i := range flat {
			m := flat[i].mesh
			if m == nil {
				continue
			}
			if _, ok := f.meshOff[m]; !ok {
				f.meshOff[m] = len(data)
				data = append(data, m.Data...)
			}
		}
		uploadSSBO(f.bufMesh, unsafe.Pointer(sliceData(data)), len(data)*4)
	}
	if !equalUintptrs(blockSig, f.blockSig) {
		f.blockSig = blockSig
		f.blockOff = map[*uint8]int{}
		var data []byte
		for i := range flat {
			b := flat[i].blocks
			if len(b) == 0 {
				continue
			}
			if _, ok := f.blockOff[&b[0]]; !ok {
				f.blockOff[&b[0]] = len(data)
				data = append(data, b...)
				for len(data)%4 != 0 {
					data = append(data, 0)
				}
			}
		}
		var p unsafe.Pointer
		if len(data) > 0 {
			p = unsafe.Pointer(&data[0])
		}
		uploadSSBO(f.bufBlocks, p, len(data))
	}

	srcData := make([]float32, 0, len(flat)*giSourceFloats)
	var planes []mgl32.Vec4
	for i := range flat {
		s := flat[i]
		switch s.kind {
		case giKindHull:
			s.p0[0], s.p0[1] = float32(len(planes)), float32(len(s.planes))
			planes = append(planes, s.planes...)
		case giKindMesh:
			s.p0[0] = float32(f.meshOff[s.mesh])
		case giKindTerrain:
			s.p1[1] = float32(f.blockOff[&s.blocks[0]])
		}
		srcData = append(srcData, s.lo[0], s.lo[1], s.lo[2], float32(s.kind))
		srcData = append(srcData, s.hi[0], s.hi[1], s.hi[2], 0)
		for _, v := range []mgl32.Vec4{s.color, s.p0, s.p1, s.c1, s.c2, s.c3} {
			srcData = append(srcData, v[:]...)
		}
		srcData = append(srcData, s.m[:]...)
	}
	uploadSSBO(f.bufSrc, unsafe.Pointer(sliceData(srcData)), len(srcData)*4)
	var pp unsafe.Pointer
	if len(planes) > 0 {
		pp = unsafe.Pointer(&planes[0])
	}
	uploadSSBO(f.bufPlanes, pp, len(planes)*16)
	f.index.update(flat, nStatic, staticChanged)
}

func sliceData(v []float32) *float32 {
	if len(v) == 0 {
		return nil
	}
	return &v[0]
}

// uploadSSBO replaces a storage buffer's contents. An empty one keeps a word,
// since a zero-sized buffer cannot be bound.
func uploadSSBO(buf uint32, data unsafe.Pointer, size int) {
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, buf)
	if size == 0 || data == nil {
		zero := [4]uint32{}
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, 16, gl.Ptr(&zero[0]), gl.STREAM_DRAW)
	} else {
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, size, data, gl.STREAM_DRAW)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

type giBrickJob struct {
	level int
	slot  int
	brick [3]int32
	dist  float32
}

// build rebuilds up to budget of the bricks that need it (all of them for a
// negative budget), nearest the camera first.
func (f *giField) build(cam mgl32.Vec3, budget int) int {
	var jobs []giBrickJob
	for li := range f.levels {
		l := &f.levels[li]
		s := float32(giBrickVox) * l.voxel
		for z := int32(0); z < giBricksPerSide; z++ {
			for y := int32(0); y < giBricksPerSide; y++ {
				for x := int32(0); x < giBricksPerSide; x++ {
					b := [3]int32{l.minBrick[0] + x, l.minBrick[1] + y, l.minBrick[2] + z}
					slot := giBrickSlot3(b[0], b[1], b[2])
					st := &l.slots[slot]
					if st.valid && st.brick == b {
						continue
					}
					c := mgl32.Vec3{(float32(b[0]) + 0.5) * s, (float32(b[1]) + 0.5) * s, (float32(b[2]) + 0.5) * s}
					jobs = append(jobs, giBrickJob{level: li, slot: slot, brick: b, dist: c.Sub(cam).Len()})
				}
			}
		}
	}
	f.pending = len(jobs)
	if len(jobs) == 0 {
		return 0
	}
	if budget >= 0 && len(jobs) > budget {
		sort.Slice(jobs, func(a, b int) bool { return jobs[a].dist < jobs[b].dist })
		jobs = jobs[:budget]
	}
	sort.SliceStable(jobs, func(a, b int) bool { return jobs[a].level < jobs[b].level })

	bricks := make([]int32, 0, len(jobs)*8)
	var lists []uint32
	for _, j := range jobs {
		l := &f.levels[j.level]
		s := float32(giBrickVox) * l.voxel
		band := float32(giSdfBand) * l.voxel
		lo := mgl32.Vec3{float32(j.brick[0]) * s, float32(j.brick[1]) * s, float32(j.brick[2]) * s}
		hi := lo.Add(mgl32.Vec3{s, s, s})
		start := len(lists)
		lists = f.index.query(lo.Sub(mgl32.Vec3{band, band, band}), hi.Add(mgl32.Vec3{band, band, band}), lists)
		bricks = append(bricks, j.brick[0], j.brick[1], j.brick[2], 0,
			int32(start), int32(len(lists)-start), 0, 0)
		l.slots[j.slot] = giBrickSlot{brick: j.brick, valid: true}
	}
	uploadSSBO(f.bufBricks, unsafe.Pointer(&bricks[0]), len(bricks)*4)
	var lp unsafe.Pointer
	if len(lists) > 0 {
		lp = unsafe.Pointer(&lists[0])
	}
	uploadSSBO(f.bufLists, lp, len(lists)*4)

	gl.UseProgram(f.prog.prog)
	for i, b := range []uint32{f.bufBricks, f.bufLists, f.bufSrc, f.bufPlanes, f.bufMesh, f.bufBlocks} {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, uint32(i), b)
	}
	for start := 0; start < len(jobs); {
		li := jobs[start].level
		end := start
		for end < len(jobs) && jobs[end].level == li {
			end++
		}
		l := &f.levels[li]
		gl.BindImageTexture(0, l.sdf, 0, true, 0, gl.WRITE_ONLY, gl.R16F)
		gl.BindImageTexture(1, l.surf, 0, true, 0, gl.WRITE_ONLY, gl.RGBA8)
		gl.Uniform1f(f.prog.U("voxel"), l.voxel)
		gl.Uniform1ui(f.prog.U("brickBase"), uint32(start))
		gl.DispatchCompute(uint32(end-start), 1, 1)
		start = end
	}
	gl.MemoryBarrier(gl.SHADER_IMAGE_ACCESS_BARRIER_BIT | gl.TEXTURE_FETCH_BARRIER_BIT)
	f.pending -= len(jobs)
	return len(jobs)
}

// uploadSdfUniformsTo points prog's gi_sdf_common.glsl at the field, with the
// textures on units base.. base+7.
func (f *giField) uploadSdfUniformsTo(p *progUniforms, base uint32) {
	gl.UseProgram(p.prog)
	gl.Uniform1i(p.U("giSdfLevels"), int32(len(f.levels)))
	names := [giSdfMaxLevels]string{"0", "1", "2", "3"}
	for i := 0; i < giSdfMaxLevels; i++ {
		li := min(i, len(f.levels)-1)
		l := &f.levels[li]
		c := l.lowCorner()
		gl.Uniform4f(p.U("giSdfLevel["+names[i]+"]"), c[0], c[1], c[2], l.voxel)
		gl.ActiveTexture(gl.TEXTURE0 + base + uint32(i))
		gl.BindTexture(gl.TEXTURE_3D, l.sdf)
		gl.Uniform1i(p.U("giSdf"+names[i]), int32(base)+int32(i))
		gl.ActiveTexture(gl.TEXTURE0 + base + 4 + uint32(i))
		gl.BindTexture(gl.TEXTURE_3D, l.surf)
		gl.Uniform1i(p.U("giSurf"+names[i]), int32(base)+4+int32(i))
	}
	gl.ActiveTexture(gl.TEXTURE0)
}

// giIndex finds the sources near a box: a bounding-volume tree over their
// bounds, median-split on the longest axis. Rebuilt whenever any source
// changes, so the build has to be cheap — O(n log n) over a flat array, where
// the uniform grid it replaced entered a canyon's long brushes into tens of
// thousands of map cells and cost most of a frame to rebuild.
//
// Only the static sources — flat's first nStatic — are in the tree, and it is
// rebuilt only when they change. The moving ones after them (entities,
// dynamic brushes) are few and checked one by one, so a walking entity costs
// a copy, not a rebuild.
type giIndex struct {
	srcs    []giSource
	nStatic int
	order   []int32
	nodes   []giIndexNode
}

type giIndexNode struct {
	lo, hi mgl32.Vec3
	// A leaf holds order[start : start+count]; an inner node's children are
	// at left and left+1.
	start, count, left int32
}

// giIndexLeaf is the most sources a leaf holds.
const giIndexLeaf = 4

// update points the index at flat, rebuilding the tree when its static part
// changed.
func (x *giIndex) update(flat []giSource, nStatic int, staticChanged bool) {
	if staticChanged || nStatic != x.nStatic {
		x.build(flat[:nStatic])
	}
	x.srcs, x.nStatic = flat, nStatic
}

func (x *giIndex) build(flat []giSource) {
	x.srcs = flat
	x.nStatic = len(flat)
	x.order = x.order[:0]
	for i := range flat {
		x.order = append(x.order, int32(i))
	}
	x.nodes = x.nodes[:0]
	if len(flat) == 0 {
		return
	}
	x.nodes = append(x.nodes, giIndexNode{})
	x.split(0, 0, int32(len(flat)))
}

func (x *giIndex) split(ni, start, end int32) {
	lo, hi := x.srcs[x.order[start]].lo, x.srcs[x.order[start]].hi
	for _, i := range x.order[start+1 : end] {
		lo, hi = vmin(lo, x.srcs[i].lo), vmax(hi, x.srcs[i].hi)
	}
	x.nodes[ni] = giIndexNode{lo: lo, hi: hi, start: start, count: end - start}
	if end-start <= giIndexLeaf {
		return
	}
	ext := hi.Sub(lo)
	axis := 0
	if ext[1] > ext[axis] {
		axis = 1
	}
	if ext[2] > ext[axis] {
		axis = 2
	}
	part := x.order[start:end]
	sort.Slice(part, func(a, b int) bool {
		sa, sb := &x.srcs[part[a]], &x.srcs[part[b]]
		return sa.lo[axis]+sa.hi[axis] < sb.lo[axis]+sb.hi[axis]
	})
	mid := start + (end-start)/2
	left := int32(len(x.nodes))
	x.nodes = append(x.nodes, giIndexNode{}, giIndexNode{})
	x.nodes[ni].left, x.nodes[ni].count = left, 0
	x.split(left, start, mid)
	x.split(left+1, mid, end)
}

// query appends to out the index of every source whose bounds meet [lo, hi].
func (x *giIndex) query(lo, hi mgl32.Vec3, out []uint32) []uint32 {
	for i := x.nStatic; i < len(x.srcs); i++ {
		s := &x.srcs[i]
		if s.lo[0] > hi[0] || s.hi[0] < lo[0] || s.lo[1] > hi[1] || s.hi[1] < lo[1] || s.lo[2] > hi[2] || s.hi[2] < lo[2] {
			continue
		}
		out = append(out, uint32(i))
	}
	if len(x.nodes) == 0 {
		return out
	}
	var stack [64]int32
	sp := 1
	for sp > 0 {
		sp--
		n := &x.nodes[stack[sp]]
		if n.lo[0] > hi[0] || n.hi[0] < lo[0] || n.lo[1] > hi[1] || n.hi[1] < lo[1] || n.lo[2] > hi[2] || n.hi[2] < lo[2] {
			continue
		}
		if n.count > 0 {
			for _, i := range x.order[n.start : n.start+n.count] {
				s := &x.srcs[i]
				if s.lo[0] > hi[0] || s.hi[0] < lo[0] || s.lo[1] > hi[1] || s.hi[1] < lo[1] || s.lo[2] > hi[2] || s.hi[2] < lo[2] {
					continue
				}
				out = append(out, uint32(i))
			}
			continue
		}
		stack[sp], stack[sp+1] = n.left, n.left+1
		sp += 2
	}
	return out
}

func sortUintptrs(v []uintptr) { sort.Slice(v, func(a, b int) bool { return v[a] < v[b] }) }

func equalUintptrs(a, b []uintptr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
