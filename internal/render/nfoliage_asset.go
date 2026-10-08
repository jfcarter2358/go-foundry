package render

// nfoliage_asset.go — every foliage variant built as a Nanite asset: its
// assembly (foliage_assembly.go) cut into clusters, the cluster DAG over it
// with voxel nodes (internal/cluster's dag.go and voxel.go), and its bones —
// packed into the buffers the Nanite foliage path draws from
// (nfoliage.go). Phase 6 of the Nanite plan (docs/nanite/PHASE_6.md).
//
// # Level 0 is the assembly
//
// A variant's level 0 is its parts and its core. Its card parts are grouped
// by template (atlas tile and colouring), along a Morton walk of where they
// stand, up to nfPartsPerCluster to a PART CLUSTER: drawn as that many
// instances of the template's two triangles, each placed by its part's
// transform. The core is cut into ordinary triangle clusters. Above level 0
// the DAG works on the flattened plant (the parts' cards written out where
// they stand): a group of trunk clusters simplifies like any mesh; a group
// of cards has nothing to simplify and becomes voxels, which also carry the
// coverage of the cards' alpha cut-outs, their colouring and their bones.
//
// # The vertex
//
// nfVertexFloats a vertex: position, shading normal, atlas uv, sway (kept
// for reference; the bones replace it), part, tone, bone and the vertex's
// place along that bone. A template's vertices carry the template's own
// unit corner and no bone (-1): its part says which.

import (
	"image"
	"math"
	"runtime"
	"sync"

	"foundry/internal/cluster"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// nfVertexFloats is one vertex in the Nanite foliage vertex buffer.
const nfVertexFloats = 13

// nfPartsPerCluster is the most parts one part cluster places.
const nfPartsPerCluster = 64

// The kinds of cluster, as nfoliage_*.glsl number them.
const (
	nfKindTriangles = 0
	nfKindParts     = 1
	nfKindVoxels    = 2
	// nfKindProxy is a cluster of the variant's shadow proxy: what shadow
	// passes draw instead of its DAG (nfBuilt.buildProxy).
	nfKindProxy = 3
)

// The command groups, by the program that draws them.
const (
	nfGroupFull  = 0 // triangles and parts, full lighting
	nfGroupCheap = 1 // ground cover's, one-tap lighting
	nfGroupVoxel = 2 // voxel bricks, as boxes
	nfGroupSmall = 3 // small voxel bricks, for the compute rasteriser
	nfGroupProxy = 4 // shadow proxies
	nfGroups     = 5
)

// The voxel attributes, in VoxelOptions.Attrs order.
const (
	nfAttrColour = iota // share coloured by the instance's colour
	nfAttrAccent        // ... by its accent colour
	nfAttrBark          // ... by bark
	nfAttrThin          // lit from both sides, glowing through
	nfAttrBlade         // a grass blade: shaded half as the ground
	nfAttrTone          // the baked brightness
	nfAttrGrey          // the atlas's veins and edges
	nfAttrs
)

// nfCluster is one cluster of one variant's DAG.
type nfCluster struct {
	kind, bucket, group, level int
	// sphere bounds what it draws in the plant's frame at unit scale, with
	// room for the most the wind bends it.
	sphere mgl32.Vec4
	// The LOD test's numbers (cluster.LODCluster): own error and sphere,
	// parent's (perr < 0: a root).
	lod, parent mgl32.Vec4
	err, perr   float32
	// What it draws: triangles (its own, or a part cluster's template) by
	// first index, count and base vertex; parts by first and count; voxels
	// by first brick and count.
	firstIndex, indexCount, baseVertex int
	firstPart, parts                   int
	firstBrick, bricks                 int
	voxelSize                          float32
	// mult is how many references one placement of it takes: one, its
	// parts, or its bricks.
	mult int
}

// nfBucket is one variant's span of everything.
type nfBucket struct {
	firstCluster, clusters int
	firstBone, bones       int
	// radius bounds the plant (unit scale) round its origin, wind and all.
	radius float32
	// Levels and the DAG's shape, for the log and the tests.
	levels, triClusters, voxClusters, partClusters int
}

// nfPart is a part as nfoliage_*.glsl reads it: four vec4s.
type nfPart struct {
	right, up, n, base mgl32.Vec4 // right·w0, up·w1, normal·tone, base·bone
}

// nfBrick is a brick as the shaders read it (std430, 32 bytes): its corner
// and voxel edge, its occupancy mask, its first voxel and its bone (low 16
// bits) and the bone weight (high 16, unorm).
type nfBrick struct {
	Corner [4]float32
	Mask   [2]uint32
	First  uint32
	Bind   uint32
}

// nfVoxel is a voxel as the shaders read it (16 bytes): its coverage along
// the three axes and the first diagonal (cluster.VoxelDirs, as the chance a
// ray that way is stopped, nfCoverage); along the other three diagonals and
// its tone; its mean normal (snorm) and blade share; its accent, bark and
// thin shares and its atlas grey (the colour share is what the accent and
// bark leave).
type nfVoxel [4]uint32

// nfBone is a bone as the shaders read it: pivot·parent, tip·at,
// (flex, flutter, phase, 0).
type nfBone struct {
	pivot, tip, params mgl32.Vec4
}

// nfTemplate is a card template's slice of the buffers.
type nfTemplate struct {
	firstIndex, indexCount, baseVertex int
}

// nfAssetData is every variant, packed.
type nfAssetData struct {
	verts     []float32
	idx       []uint32
	templates []nfTemplate
	buckets   []nfBucket
	clusters  []nfCluster
	parts     []nfPart
	bricks    []nfBrick
	voxels    []nfVoxel
	bones     []nfBone
	// maxBones is the most bones any variant has.
	maxBones int
}

// nfAtlas reads the foliage atlas on the CPU: its full-size level for the
// cut-out a voxel's coverage comes from, and its mip chain for the shade.
type nfAtlas struct{ chain []*image.NRGBA }

// at is the atlas's luminance and alpha at uv on mip level lvl.
func (a nfAtlas) at(uv mgl32.Vec2, lvl int) (lum, alpha float32) {
	if len(a.chain) == 0 {
		return 1, 1
	}
	img := a.chain[min(max(lvl, 0), len(a.chain)-1)]
	w, h := img.Rect.Dx(), img.Rect.Dy()
	x := min(max(int(uv[0]*float32(w)), 0), w-1)
	y := min(max(int(uv[1]*float32(h)), 0), h-1)
	i := y*img.Stride + x*4
	return float32(img.Pix[i]) / 255, float32(img.Pix[i+3]) / 255
}

// width is the full-size level's width in texels.
func (a nfAtlas) width() float32 {
	if len(a.chain) == 0 {
		return 1
	}
	return float32(a.chain[0].Bounds().Dx())
}

// cardTemplateKey is what a card template is: its atlas tile and colouring.
type cardTemplateKey struct{ tile, part int }

// nfBuilt is one variant built, before packing.
type nfBuilt struct {
	shape   world.FoliageShape
	variant int
	a       foliageAssembly
	verts   []float32 // the flattened plant, nfVertexFloats a vertex
	coreTri int       // triangles of core before the cards
	// coreBase is how many Base entries (level-0 clusters) the core has;
	// the part clusters follow, chunks[i] being Base entry coreBase+i.
	coreBase int
	chunks   [][2]int // part clusters: first part, count (parts reordered)
	dag      cluster.DAG
	pad      []float32 // per DAG cluster: how far the wind can move it
	radius   float32
	// proxy is the shadow proxy's triangles (indices into verts) cut into
	// clusters, and proxyPad how far the wind moves each.
	proxy    cluster.Result
	proxyIdx []uint32
	proxyPad []float32
}

// buildNaniteFoliageAssets builds every variant. atlas may be empty (every
// card then counts as whole).
func buildNaniteFoliageAssets(atlas nfAtlas) nfAssetData {
	var out nfAssetData
	// Card templates first: one per (tile, colouring), four corners each.
	tmpl := map[cardTemplateKey]int{}
	type job struct {
		s world.FoliageShape
		v int
		b *nfBuilt
	}
	var jobs []job
	for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
		for v := 0; v < world.FoliageVariants[s]; v++ {
			// Recording is single-threaded (asmRecording); the DAGs after it
			// are not.
			jobs = append(jobs, job{s: s, v: v, b: &nfBuilt{shape: s, variant: v, a: recordFoliageAssembly(s, v)}})
		}
	}
	for _, j := range jobs {
		for _, p := range j.b.a.parts {
			k := cardTemplateKey{p.card.tile, p.card.part}
			if _, ok := tmpl[k]; !ok {
				tmpl[k] = len(out.templates)
				base := len(out.verts) / nfVertexFloats
				for c := 0; c < 4; c++ {
					x, y := cardTemplateCorners[c][0], cardTemplateCorners[c][1]
					uv := tileUV(k.tile, (x+1)/2, y)
					out.verts = append(out.verts, x, y, 0, 0, 0, 1, uv[0], uv[1], y, float32(k.part), 1, -1, y)
				}
				out.templates = append(out.templates, nfTemplate{firstIndex: len(out.idx), indexCount: 6, baseVertex: base})
				for _, c := range cardCornerOrder {
					out.idx = append(out.idx, uint32(c))
				}
			}
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(b *nfBuilt) {
			defer wg.Done()
			defer func() { <-sem }()
			b.build(atlas)
		}(j.b)
	}
	wg.Wait()
	for bi, j := range jobs {
		out.pack(bi, j.s, j.b, tmpl)
	}
	return out
}

// build flattens the assembly, cuts level 0 and builds the DAG.
func (b *nfBuilt) build(atlas nfAtlas) {
	a := &b.a
	// The core, indexed: corners shared by position and binding, so the
	// simplifier sees a connected surface.
	seen := map[[nfVertexFloats]float32]uint32{}
	var coreIdx []uint32
	put := func(v []float32) uint32 {
		var k [nfVertexFloats]float32
		copy(k[:], v)
		if i, ok := seen[k]; ok {
			return i
		}
		i := uint32(len(b.verts) / nfVertexFloats)
		seen[k] = i
		b.verts = append(b.verts, k[:]...)
		return i
	}
	nv := len(a.core) / foliageVertexFloats
	for i := 0; i < nv; i++ {
		v := append(append([]float32(nil), a.core[i*foliageVertexFloats:(i+1)*foliageVertexFloats]...), a.coreBind[i][0], a.coreBind[i][1])
		coreIdx = append(coreIdx, put(v))
	}
	b.coreTri = len(coreIdx) / 3
	var base [][]uint32
	if b.coreTri > 0 {
		res := cluster.Build(cluster.Mesh{Positions: b.verts, Stride: nfVertexFloats, Indices: coreIdx}, cluster.Options{})
		for _, c := range res.Clusters {
			base = append(base, append([]uint32(nil), res.Order[c.TriFirst:c.TriFirst+c.TriCount]...))
		}
	}
	b.coreBase = len(base)
	// Parts: by template, then along a Morton walk of their bases, in runs
	// of nfPartsPerCluster — and reordered so each run is contiguous.
	groups := map[cardTemplateKey][]int{}
	var keys []cardTemplateKey
	for i, p := range a.parts {
		k := cardTemplateKey{p.card.tile, p.card.part}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], i)
	}
	var order []int
	for _, k := range keys {
		g := groups[k]
		cent := make([]mgl32.Vec3, len(g))
		for i, pi := range g {
			c := a.parts[pi].card
			cent[i] = c.base.Add(c.up.Mul(0.5))
		}
		for start, walk := 0, mortonWalk(cent); start < len(walk); start += nfPartsPerCluster {
			end := min(start+nfPartsPerCluster, len(walk))
			b.chunks = append(b.chunks, [2]int{len(order), end - start})
			for _, w := range walk[start:end] {
				order = append(order, g[w])
			}
		}
	}
	parts := make([]foliagePart, len(order))
	for i, pi := range order {
		parts[i] = a.parts[pi]
	}
	a.parts = parts
	cardIdx := coreIdx
	for _, p := range a.parts {
		first := uint32(len(b.verts) / nfVertexFloats)
		for k := 0; k < 4; k++ {
			v := cardVertex(p.card, k)
			w := p.w0
			if cardTemplateCorners[k][1] > 0 {
				w = p.w1
			}
			b.verts = append(b.verts, v...)
			b.verts = append(b.verts, float32(p.bone), w)
		}
		for _, c := range cardCornerOrder {
			cardIdx = append(cardIdx, first+uint32(c))
		}
	}
	for _, ch := range b.chunks {
		var list []uint32
		for p := ch[0]; p < ch[0]+ch[1]; p++ {
			list = append(list, uint32(b.coreTri+2*p), uint32(b.coreTri+2*p+1))
		}
		base = append(base, list)
	}
	mesh := cluster.Mesh{Positions: b.verts, Stride: nfVertexFloats, Indices: cardIdx}
	b.dag = cluster.BuildDAG(mesh, cluster.DAGOptions{
		Base: base,
		// After x, y, z: normal, uv, sway, part, tone, bone, place. A part
		// or a bone is never smeared into its neighbour.
		AttrWeights: []float32{0.3, 0.3, 0.3, 0, 0, 0.2, 50, 0.2, 50, 0.3},
		Voxels: &cluster.VoxelOptions{Attrs: nfAttrs, RootBricks: 1,
			Sample: func(v [3]uint32, bc [3]float32, size float32, attr []float32) cluster.VoxelSample {
				return b.sample(atlas, v, bc, size, attr)
			}},
	})
	b.windPad()
	b.buildProxy()
}

// buildProxy builds the variant's SHADOW PROXY: the classic path's far mesh
// (buildFoliageTemplate at level 1 — a fifth of the triangles), bound to the
// full plant's bones (each vertex to the nearest, at its place along it) so
// its shadow bends with the tree the camera sees. Shadow passes draw it
// instead of the DAG's cut: a shadow is soft enough that nobody counts its
// leaves, the classic path cast exactly this mesh, and the camera's
// full-detail cut cast into every sun rectangle cost three to four times
// the classic foliage's shadows on the GTX 1070 (docs/nanite/PHASE_6.md) —
// and its coarser levels are voxels, which cost more still at the virtual
// map's finest texels. Phase 4 made the same choice for classic-path cooked
// meshes (gfx.Geometry.ShadowLOD).
func (b *nfBuilt) buildProxy() {
	a := &b.a
	far := buildFoliageTemplate(b.shape, b.variant, 1)
	n := len(far) / foliageVertexFloats
	if n == 0 {
		return
	}
	nearest := func(p mgl32.Vec3) (int, float32) {
		best, bd := 0, float32(math.MaxFloat32)
		for bi := 1; bi < len(a.bones); bi++ {
			if d := a.bones[bi].dist(p); d < bd {
				best, bd = bi, d
			}
		}
		return best, a.bones[best].t(p)
	}
	first := uint32(len(b.verts) / nfVertexFloats)
	for i := 0; i < n; i++ {
		v := far[i*foliageVertexFloats : (i+1)*foliageVertexFloats]
		bone, t := nearest(mgl32.Vec3{v[0], v[1], v[2]})
		b.verts = append(b.verts, v...)
		b.verts = append(b.verts, float32(bone), t)
		b.proxyIdx = append(b.proxyIdx, first+uint32(i))
	}
	mesh := cluster.Mesh{Positions: b.verts, Stride: nfVertexFloats, Indices: b.proxyIdx}
	b.proxy = cluster.Build(mesh, cluster.Options{})
	poses := [2][]mgl32.Mat4{
		poseFoliageBones(a.bones, nfWindAmount, 0, 1),
		poseFoliageBones(a.bones, nfWindAmount, 1, 1),
	}
	for _, c := range b.proxy.Clusters {
		pad := float32(0)
		for _, t := range b.proxy.Order[c.TriFirst : c.TriFirst+c.TriCount] {
			for k := 0; k < 3; k++ {
				r := b.verts[int(b.proxyIdx[3*t+uint32(k)])*nfVertexFloats:]
				p := mgl32.Vec3{r[0], r[1], r[2]}
				bone, w := int(r[11]+0.5), r[12]
				for _, ps := range poses {
					q := mulAffine(ps[2*bone], p).Mul(1 - w).Add(mulAffine(ps[2*bone+1], p).Mul(w))
					pad = max(pad, q.Sub(p).Len())
				}
			}
		}
		b.proxyPad = append(b.proxyPad, pad)
		b.radius = max(b.radius, c.Center.Len()+c.Radius+pad)
	}
}

// sample is the plant's surface at barycentric bc of triangle v, for a voxel
// of edge size: whether a card's cut-out keeps it (the full-size cut-out), its
// normal, its colouring — the atlas's shade at the mip level the hardware
// reads when the card is drawn that far off, a voxel to a pixel, so a plant
// does not change shade where its triangles hand over to its voxels — and its
// bone.
func (b *nfBuilt) sample(atlas nfAtlas, v [3]uint32, bc [3]float32, size float32, attr []float32) cluster.VoxelSample {
	// Called for every sample of every piece of every triangle voxelised,
	// millions of times for the rainforest's trees: the three rows are
	// read once, not through closures per field.
	r0 := b.verts[int(v[0])*nfVertexFloats : int(v[0])*nfVertexFloats+nfVertexFloats]
	r1 := b.verts[int(v[1])*nfVertexFloats : int(v[1])*nfVertexFloats+nfVertexFloats]
	r2 := b.verts[int(v[2])*nfVertexFloats : int(v[2])*nfVertexFloats+nfVertexFloats]
	lerp := func(f int) float32 { return r0[f]*bc[0] + r1[f]*bc[1] + r2[f]*bc[2] }
	n := mgl32.Vec3{lerp(3), lerp(4), lerp(5)}
	if n.Len() > 1e-6 {
		n = n.Normalize()
	}
	part := int(r0[9] + 0.5)
	weight, grey := float32(1), float32(1)
	if part == partLeaf || part == partAccentCard {
		uv := mgl32.Vec2{lerp(6), lerp(7)}
		if _, alpha := atlas.at(uv, 0); alpha < 0.5 {
			weight = 0
		}
		// Texels per unit across the triangle: its uv area over its area.
		e1 := mgl32.Vec3{r1[0] - r0[0], r1[1] - r0[1], r1[2] - r0[2]}
		e2 := mgl32.Vec3{r2[0] - r0[0], r2[1] - r0[1], r2[2] - r0[2]}
		area := e1.Cross(e2).Len()
		u1, v1, u2, v2 := r1[6]-r0[6], r1[7]-r0[7], r2[6]-r0[6], r2[7]-r0[7]
		uvArea := float32(math.Abs(float64(u1*v2 - v1*u2)))
		lvl := 0
		if area > 1e-12 && uvArea > 0 {
			perUnit := atlas.width() * float32(math.Sqrt(float64(uvArea/area)))
			lvl = nfRoundLog2(size * perUnit)
		}
		lum, _ := atlas.at(uv, lvl)
		// foliage_frag.glsl's c *= 0.35 + 0.85·t.r.
		grey = 0.35 + 0.85*lum
	}
	switch part {
	case partAccent, partAccentCard:
		attr[nfAttrAccent] = 1
	case partBark:
		attr[nfAttrBark] = 1
	default:
		attr[nfAttrColour] = 1
	}
	if part == partLeaf || part == partAccentCard || part == partBlade {
		attr[nfAttrThin] = 1
	}
	if part == partBlade {
		attr[nfAttrBlade] = 1
	}
	attr[nfAttrTone] = lerp(10)
	attr[nfAttrGrey] = grey
	return cluster.VoxelSample{Weight: weight, Normal: n, Bind: int32(r0[11] + 0.5), BindWeight: lerp(12)}
}

// nfRoundLog2 is round(log2(max(x, 1))), from the float's exponent: the mip
// level a footprint of x texels reads.
func nfRoundLog2(x float32) int {
	if !(x > 1) {
		return 0
	}
	// log2(x·√2) floored is log2(x) rounded.
	return int((math.Float32bits(x*math.Sqrt2)>>23)&0xff) - 127
}

// nfWindAmount is the strongest wind the bounds allow for: strength 1.5 in
// a full gust.
const nfWindAmount = 1.5

// windPad works out how far the strongest wind moves each DAG cluster's
// surface, and the plant's radius with it.
func (b *nfBuilt) windPad() {
	poses := [2][]mgl32.Mat4{
		poseFoliageBones(b.a.bones, nfWindAmount, 0, 1),
		poseFoliageBones(b.a.bones, nfWindAmount, 1, 1),
	}
	moved := func(p mgl32.Vec3, bone int, w float32) float32 {
		d := float32(0)
		for _, ps := range poses {
			q := mulAffine(ps[2*bone], p).Mul(1 - w).Add(mulAffine(ps[2*bone+1], p).Mul(w))
			d = max(d, q.Sub(p).Len())
		}
		return d
	}
	vp := func(i uint32) (mgl32.Vec3, int, float32) {
		r := b.verts[int(i)*nfVertexFloats:]
		return mgl32.Vec3{r[0], r[1], r[2]}, int(r[11] + 0.5), r[12]
	}
	d := &b.dag
	b.pad = make([]float32, len(d.Clusters))
	for ci := range d.Clusters {
		c := &d.Clusters[ci]
		pad := float32(0)
		if c.Kind == cluster.KindVoxels {
			vc := &d.Voxels[c.Voxel]
			for bi := range vc.Bricks {
				lo, hi := vc.BrickBox(bi)
				br := &vc.Bricks[bi]
				for k := 0; k < 8; k++ {
					corner := mgl32.Vec3{lo[0], lo[1], lo[2]}
					if k&1 != 0 {
						corner[0] = hi[0]
					}
					if k&2 != 0 {
						corner[1] = hi[1]
					}
					if k&4 != 0 {
						corner[2] = hi[2]
					}
					pad = max(pad, moved(corner, int(br.Bind), br.BindWeight))
				}
			}
		} else {
			for _, i := range d.Indices[3*c.TriFirst : 3*(c.TriFirst+c.TriCount)] {
				p, bone, w := vp(i)
				pad = max(pad, moved(p, bone, w))
			}
		}
		b.pad[ci] = pad
		b.radius = max(b.radius, c.Center.Len()+c.Radius+pad)
	}
}

// pack appends a built variant to the shared arrays.
func (out *nfAssetData) pack(bucket int, shape world.FoliageShape, b *nfBuilt, tmpl map[cardTemplateKey]int) {
	d := &b.dag
	group := nfGroupFull
	if foliageShapes[shape].cheap {
		group = nfGroupCheap
	}
	bk := nfBucket{firstCluster: len(out.clusters), firstBone: len(out.bones), bones: len(b.a.bones),
		radius: b.radius, levels: d.Levels}
	out.maxBones = max(out.maxBones, len(b.a.bones))
	for _, bn := range b.a.bones {
		out.bones = append(out.bones, nfBone{
			pivot:  bn.pivot.Vec4(float32(bn.parent)),
			tip:    bn.tip.Vec4(bn.at),
			params: mgl32.Vec4{bn.flex, bn.flutter, bn.phase, 0},
		})
	}
	partBase := len(out.parts)
	for _, p := range b.a.parts {
		c := p.card
		out.parts = append(out.parts, nfPart{
			right: c.right.Vec4(p.w0),
			up:    c.up.Vec4(p.w1),
			n:     c.n.Vec4(c.tone),
			base:  c.base.Vec4(float32(p.bone)),
		})
	}
	vertBase := len(out.verts) / nfVertexFloats
	out.verts = append(out.verts, b.verts...)
	for ci := range d.Clusters {
		c := &d.Clusters[ci]
		nc := nfCluster{bucket: bucket, group: group, level: int(c.Level), mult: 1,
			sphere: c.Center.Vec4(c.Radius + b.pad[ci]),
			lod:    c.LODCenter.Vec4(c.LODRadius), parent: c.ParentCenter.Vec4(c.ParentRadius),
			err: c.Error, perr: c.ParentError}
		switch {
		case c.Kind == cluster.KindVoxels:
			vc := &d.Voxels[c.Voxel]
			nc.kind, nc.group = nfKindVoxels, nfGroupVoxel
			nc.firstBrick, nc.bricks, nc.mult = len(out.bricks), len(vc.Bricks), len(vc.Bricks)
			nc.voxelSize = vc.Size
			out.packVoxels(vc)
			bk.voxClusters++
		case c.Level == 0 && int(c.Base) >= b.coreBase:
			ch := b.chunks[int(c.Base)-b.coreBase]
			first := b.a.parts[ch[0]].card
			t := out.templates[tmpl[cardTemplateKey{first.tile, first.part}]]
			nc.kind = nfKindParts
			nc.firstIndex, nc.indexCount, nc.baseVertex = t.firstIndex, t.indexCount, t.baseVertex
			nc.firstPart, nc.parts, nc.mult = partBase+ch[0], ch[1], ch[1]
			bk.partClusters++
		default:
			nc.kind = nfKindTriangles
			nc.firstIndex, nc.indexCount, nc.baseVertex = len(out.idx), int(3*c.TriCount), vertBase
			out.idx = append(out.idx, d.Indices[3*c.TriFirst:3*(c.TriFirst+c.TriCount)]...)
			bk.triClusters++
		}
		out.clusters = append(out.clusters, nc)
	}
	// The shadow proxy's clusters, after the DAG's: always "in the cut" —
	// a shadow pass takes them and nothing else, a camera pass never.
	for ci, c := range b.proxy.Clusters {
		nc := nfCluster{bucket: bucket, group: nfGroupProxy, kind: nfKindProxy, mult: 1, level: -1,
			sphere: c.Center.Vec4(c.Radius + b.proxyPad[ci]), err: 0, perr: -1,
			firstIndex: len(out.idx), indexCount: int(3 * c.TriCount), baseVertex: vertBase}
		for _, t := range b.proxy.Order[c.TriFirst : c.TriFirst+c.TriCount] {
			out.idx = append(out.idx, b.proxyIdx[3*t:3*t+3]...)
		}
		out.clusters = append(out.clusters, nc)
	}
	bk.clusters = len(out.clusters) - bk.firstCluster
	out.buckets = append(out.buckets, bk)
}

// packVoxels appends a voxel cluster's bricks and voxels.
func (out *nfAssetData) packVoxels(vc *cluster.VoxelCluster) {
	u8 := func(x float32) uint32 { return uint32(min(max(x, 0), 1)*255 + 0.5) }
	s8 := func(x float32) uint32 { return uint32(int32(min(max(x, -1), 1)*127)) & 0xff }
	for bi := range vc.Bricks {
		br := &vc.Bricks[bi]
		lo, _ := vc.BrickBox(bi)
		out.bricks = append(out.bricks, nfBrick{
			Corner: [4]float32{lo[0], lo[1], lo[2], vc.Size},
			Mask:   [2]uint32{uint32(br.Mask), uint32(br.Mask >> 32)},
			First:  uint32(len(out.voxels)),
			Bind:   uint32(br.Bind)&0xffff | uint32(min(max(br.BindWeight, 0), 1)*65535+0.5)<<16,
		})
		for k := 0; k < br.Count(); k++ {
			vi := int(br.First) + k
			v := &vc.Voxels[vi]
			at := vc.Attrs[vi*nfAttrs : (vi+1)*nfAttrs]
			n := v.MeanNormal()
			cov := func(k int) uint32 { return u8(nfCoverage(v.Coverage(k, vc.Size), at[nfAttrThin])) }
			out.voxels = append(out.voxels, nfVoxel{
				cov(0) | cov(1)<<8 | cov(2)<<16 | cov(3)<<24,
				cov(4) | cov(5)<<8 | cov(6)<<16 | u8(at[nfAttrTone]/2)<<24,
				s8(n[0]) | s8(n[1])<<8 | s8(n[2])<<16 | u8(at[nfAttrBlade])<<24,
				u8(at[nfAttrAccent]) | u8(at[nfAttrBark])<<8 | u8(at[nfAttrThin])<<16 | u8(at[nfAttrGrey])<<24,
			})
		}
	}
}

// nfCoverage is the chance a ray across a voxel is stopped, from the area
// inside it projected across the ray over the voxel's face (a, which may
// pass 1) and how much of that is thin (leaves and blades). Leaves are
// scattered: they overlap, and a ray passes a crowd of them with the chance
// Beer–Lambert gives, exp(−a) — which is also what makes a coarse voxel's
// chance the same as the two fine ones a ray crosses in its place. A solid
// surface does not overlap itself: a wall square to the ray stops it,
// min(a, 1). A voxel of both mixes the two.
func nfCoverage(a, thin float32) float32 {
	leaves := 1 - float32(math.Exp(-float64(a)))
	solid := min(a, 1)
	return solid + (leaves-solid)*min(max(thin, 0), 1)
}

// mortonWalk is the order of a Morton walk through points.
func mortonWalk(p []mgl32.Vec3) []int {
	if len(p) == 0 {
		return nil
	}
	lo, hi := p[0], p[0]
	for _, q := range p {
		for k := 0; k < 3; k++ {
			lo[k], hi[k] = min(lo[k], q[k]), max(hi[k], q[k])
		}
	}
	code := func(q mgl32.Vec3) uint64 {
		var c uint64
		var u [3]uint32
		for k := 0; k < 3; k++ {
			e := hi[k] - lo[k]
			if e > 0 {
				u[k] = uint32((q[k] - lo[k]) / e * 1023)
			}
		}
		for bit := 9; bit >= 0; bit-- {
			for k := 0; k < 3; k++ {
				c = c<<1 | uint64(u[k]>>uint(bit)&1)
			}
		}
		return c
	}
	idx := make([]int, len(p))
	codes := make([]uint64, len(p))
	for i := range p {
		idx[i] = i
		codes[i] = code(p[i])
	}
	sortStableBy(idx, func(a, b int) bool { return codes[a] < codes[b] })
	return idx
}

func sortStableBy(idx []int, less func(a, b int) bool) {
	// Insertion sort is plenty: a variant has at most a few hundred parts.
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && less(idx[j], idx[j-1]); j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
}

// nfBucketOf is (shape, variant)'s bucket: the order buckets are built in,
// which is foliage.bucket's.
func nfBucketOf(s world.FoliageShape, variant int) int {
	n := 0
	for k := world.FoliageShape(0); k < s; k++ {
		n += world.FoliageVariants[k]
	}
	return n + variant
}

// nfBucketCount is how many buckets there are.
func nfBucketCount() int { return nfBucketOf(world.FoliageShapeCount, 0) }
