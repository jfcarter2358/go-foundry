package cluster

// voxel.go — voxel bricks: the cluster DAG's second kind of node. Phase 6 of
// the Nanite plan (docs/nanite/PHASE_6.md), "Nanite Voxels".
//
// A triangle cluster is the right way to draw a surface until its triangles
// are smaller than the pixels they land on. Foliage gets there long before
// anything else: a tree's crown is a few hundred leaf cards with nothing
// joining them, and simplification (simplify.go) has nothing to collapse —
// every card is all border. What a crown is from a distance is a VOLUME that
// lets some light through, and that is what a voxel cluster stores.
//
// # What a voxel holds
//
// A voxel cluster is up to MaxClusterBricks BRICKS of BrickSide³ (4x4x4)
// voxels, each voxel an edge of Size, on a lattice shared by the whole DAG
// (VoxelGrid: one origin, sizes Unit·2^k, so a coarse voxel is exactly eight
// fine ones). Only the occupied voxels are kept; a brick is its position, a
// 64-bit occupancy mask and where its voxels start. A voxel holds:
//
//   - its PROJECTED AREA along seven directions — the three axes and the four
//     body diagonals (VoxelDirs): the surface it contains, flattened along
//     each, weighted by how much of it is really there (a leaf card's alpha).
//     Projected area over the voxel's own cross-section along a direction is
//     how much a ray that way meets inside it — its coverage, which the
//     renderer turns into a stochastic hit (render/nfoliage.go). A flat wall
//     crossing a voxel square to an axis covers it wholly from that side; a
//     leaf spray a tenth. The diagonals are there because the axes alone
//     cannot say what a slanting ray sees: leaves facing every way meet a
//     diagonal ray half as much as the axes' areas summed would claim.
//     Areas add, so a coarse voxel is exactly its eight children summed —
//     the coverage of a coarser level is the finer one's, seen through a
//     wider pixel.
//   - a NORMAL DISTRIBUTION: the area-weighted sum of its surface's shading
//     normals. Its direction is the mean normal; its length over the weight,
//     how much they agree (1: a flat patch; near 0: leaves facing every
//     way). The renderer draws a per-pixel normal from it.
//   - the caller's ATTRIBUTES (VoxelOptions.Attrs floats: foliage puts its
//     colour weights, tone and part there), area-weighted averages.
//   - a BINDING (a bone, for the renderer's skinning) and its weight: the
//     binding that contributed most area, and the mean weight of what it
//     contributed. A brick takes the binding of most of its voxels' area, so
//     a brick moves rigidly with one bone.
//
// # Error
//
// A voxel stands for whatever surface lies inside it, so it may be off by up
// to its half diagonal: VoxelError(size) = size·√3/2. The DAG builder
// (dag.go) compares that with what simplifying the same triangles would cost
// and takes the smaller — which, for a crown of cards, is the voxels from the
// first level up, and for a trunk, the triangles until it is a few pixels
// wide.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// BrickSide is the voxels along a brick's edge; BrickVoxels the voxels in one.
const (
	BrickSide   = 4
	BrickVoxels = BrickSide * BrickSide * BrickSide
)

// MaxClusterBricks is the most bricks one voxel cluster holds.
const MaxClusterBricks = 128

// VoxelDirs are the directions a voxel's projected area is kept along: the
// axes, then the four body diagonals (unit).
var VoxelDirs = [7]mgl32.Vec3{
	{1, 0, 0}, {0, 1, 0}, {0, 0, 1},
	{0.57735027, 0.57735027, 0.57735027}, {0.57735027, 0.57735027, -0.57735027},
	{0.57735027, -0.57735027, 0.57735027}, {-0.57735027, 0.57735027, 0.57735027},
}

// VoxelDirSection is a unit voxel's cross-section seen along VoxelDirs[k]:
// 1 along an axis, √3 along a diagonal (a cube's shadow is the sum of its
// faces', |dx| + |dy| + |dz|).
var VoxelDirSection = [7]float32{1, 1, 1, 1.7320508, 1.7320508, 1.7320508, 1.7320508}

// VoxelError is the most a voxel of edge size can be off by: its half
// diagonal.
func VoxelError(size float32) float32 { return size * 0.8660254 }

// VoxelSample is what a surface is at one point.
type VoxelSample struct {
	// Weight is how much of the surface is really there, 0..1 (a cutout
	// card's alpha); 0 adds nothing.
	Weight float32
	// Normal is its shading normal (unit).
	Normal mgl32.Vec3
	// Bind is what it moves with (a bone), and BindWeight how much.
	Bind       int32
	BindWeight float32
}

// VoxelOptions makes a DAG build voxel-capable (DAGOptions.Voxels).
type VoxelOptions struct {
	// Attrs is how many attribute floats a voxel averages.
	Attrs int
	// Sample describes the surface at barycentric b of the triangle over
	// vertices v, for a voxel of edge size (a texture is best read at the
	// detail that size can show), writing its attributes into out (Attrs
	// long).
	Sample func(v [3]uint32, b [3]float32, size float32, out []float32) VoxelSample
	// Unit is the finest voxel edge; 0 takes the mesh's largest extent/256.
	Unit float32
	// RootBricks: the build keeps coarsening until its root is a voxel
	// cluster of at most this many bricks (0: 1), even past one cluster.
	RootBricks int
}

// Voxel is one occupied voxel.
type Voxel struct {
	// Local is its place in its brick: x + 4y + 16z.
	Local uint8
	// Area is the weighted surface inside it projected along each of
	// VoxelDirs.
	Area [7]float32
	// Weight is the weighted surface area inside it.
	Weight float32
	// NormalSum is the weighted sum of its surface's shading normals.
	NormalSum mgl32.Vec3
	// Bind and BindWeight: the binding that gave it the most area, and that
	// binding's mean weight.
	Bind       int32
	BindWeight float32
}

// Coverage is how much a ray along VoxelDirs[k] meets inside a voxel of edge
// size, as a fraction of the voxel's cross-section that way: the projected
// area over it. It passes 1 where surfaces overlap; what chance that gives
// is the renderer's to say (leaves and walls overlap differently).
func (v *Voxel) Coverage(k int, size float32) float32 {
	return v.Area[k] / (size * size * VoxelDirSection[k])
}

// MeanNormal is the normal distribution's mean direction scaled by how much
// the normals agree: unit for a flat patch, short for a spread.
func (v *Voxel) MeanNormal() mgl32.Vec3 {
	if v.Weight <= 0 {
		return mgl32.Vec3{}
	}
	n := v.NormalSum.Mul(1 / v.Weight)
	if l := n.Len(); l > 1 {
		n = n.Mul(1 / l)
	}
	return n
}

// Brick is BrickSide³ voxels of a cluster.
type Brick struct {
	// Pos is its corner on the lattice, in bricks: its voxels are
	// Pos·4 + (x, y, z).
	Pos [3]int32
	// Mask has bit x + 4y + 16z set for each occupied voxel.
	Mask uint64
	// First is its first voxel in VoxelCluster.Voxels; they follow in bit
	// order, one per set bit.
	First uint32
	// Bind and BindWeight: what the brick moves with (its voxels' leading
	// binding, by area).
	Bind       int32
	BindWeight float32
}

// Count is how many voxels it holds.
func (b *Brick) Count() int {
	n := 0
	for m := b.Mask; m != 0; m &= m - 1 {
		n++
	}
	return n
}

// VoxelCluster is a voxel node of the DAG.
type VoxelCluster struct {
	// Size is the voxel edge; Origin the lattice's corner (shared by the
	// whole DAG), so a brick's corner is Origin + Pos·4·Size.
	Size   float32
	Origin mgl32.Vec3
	Bricks []Brick
	Voxels []Voxel
	// Attrs is VoxelOptions.Attrs floats a voxel, in Voxels order.
	Attrs []float32
	// Lo, Hi bound its bricks; Center, Radius are a sphere round them.
	Lo, Hi mgl32.Vec3
	Center mgl32.Vec3
	Radius float32
}

// BrickBox is brick i's corner and far corner.
func (c *VoxelCluster) BrickBox(i int) (mgl32.Vec3, mgl32.Vec3) {
	b := &c.Bricks[i]
	e := c.Size * BrickSide
	lo := c.Origin.Add(mgl32.Vec3{float32(b.Pos[0]) * e, float32(b.Pos[1]) * e, float32(b.Pos[2]) * e})
	return lo, lo.Add(mgl32.Vec3{e, e, e})
}

// VoxelCount is how many voxels it holds.
func (c *VoxelCluster) VoxelCount() int { return len(c.Voxels) }

// ---------- building ----------

// voxelAcc is a voxel being gathered.
type voxelAcc struct {
	area   [7]float64
	weight float64
	nsum   [3]float64
	attrs  []float64
	// binds is area per binding, bindW the weighted sum of its weights.
	binds map[int32][2]float64
}

// voxelGrid gathers surface into voxels of one size on the DAG's lattice.
type voxelGrid struct {
	opt    *VoxelOptions
	origin mgl32.Vec3
	size   float32
	cells  map[[3]int32]*voxelAcc
	scr    []float32
}

func newVoxelGrid(opt *VoxelOptions, origin mgl32.Vec3, size float32) *voxelGrid {
	return &voxelGrid{opt: opt, origin: origin, size: size, cells: map[[3]int32]*voxelAcc{},
		scr: make([]float32, opt.Attrs)}
}

func (g *voxelGrid) cell(p mgl32.Vec3) *voxelAcc {
	q := p.Sub(g.origin).Mul(1 / g.size)
	k := [3]int32{int32(math.Floor(float64(q[0]))), int32(math.Floor(float64(q[1]))), int32(math.Floor(float64(q[2])))}
	a := g.cells[k]
	if a == nil {
		a = &voxelAcc{attrs: make([]float64, g.opt.Attrs), binds: map[int32][2]float64{}}
		g.cells[k] = a
	}
	return a
}

// addTriangles adds triangles (index triples into m) to the grid. Each is
// CLIPPED into the voxels it crosses (slab by slab along x, y, then z), so a
// voxel's share of its area is exact; each piece is then sampled at a few
// points — sub-triangles of its fan about a third of a voxel across — for
// what is there (a cutout's alpha varies across a card) and its attributes.
func (g *voxelGrid) addTriangles(m *Mesh, idx []uint32) {
	var pieces []voxelPiece
	for t := 0; t+2 < len(idx); t += 3 {
		v := [3]uint32{idx[t], idx[t+1], idx[t+2]}
		p := [3]mgl32.Vec3{m.Position(v[0]), m.Position(v[1]), m.Position(v[2])}
		e1, e2 := p[1].Sub(p[0]), p[2].Sub(p[0])
		cr := e1.Cross(e2)
		area := cr.Len() * 0.5
		if area <= 0 {
			continue
		}
		ng := cr.Mul(1 / (2 * area))
		// For barycentrics of a point in the triangle's plane.
		d00, d01, d11 := e1.Dot(e1), e1.Dot(e2), e2.Dot(e2)
		den := d00*d11 - d01*d01
		bary := func(q mgl32.Vec3) [3]float32 {
			d := q.Sub(p[0])
			d20, d21 := d.Dot(e1), d.Dot(e2)
			u := (d11*d20 - d01*d21) / den
			w := (d00*d21 - d01*d20) / den
			u, w = min(max(u, 0), 1), min(max(w, 0), 1)
			if u+w > 1 {
				s := 1 / (u + w)
				u, w = u*s, w*s
			}
			return [3]float32{1 - u - w, u, w}
		}
		pieces = g.clip(pieces[:0], []mgl32.Vec3{p[0], p[1], p[2]})
		for _, pc := range pieces {
			g.addPiece(m, v, ng, pc, bary)
		}
	}
}

// voxelPiece is the part of a triangle inside one voxel.
type voxelPiece struct {
	cell [3]int32
	poly []mgl32.Vec3
}

// clip cuts polygon poly into its pieces in each voxel, appending to out.
func (g *voxelGrid) clip(out []voxelPiece, poly []mgl32.Vec3) []voxelPiece {
	var rec func(poly []mgl32.Vec3, axis int, cell [3]int32)
	rec = func(poly []mgl32.Vec3, axis int, cell [3]int32) {
		if axis == 3 {
			out = append(out, voxelPiece{cell: cell, poly: poly})
			return
		}
		lo, hi := poly[0][axis], poly[0][axis]
		for _, q := range poly[1:] {
			lo, hi = min(lo, q[axis]), max(hi, q[axis])
		}
		o := g.origin[axis]
		i0 := int32(math.Floor(float64((lo - o) / g.size)))
		i1 := int32(math.Floor(float64((hi - o) / g.size)))
		if i0 == i1 {
			cell[axis] = i0
			rec(poly, axis+1, cell)
			return
		}
		for i := i0; i <= i1; i++ {
			a := o + float32(i)*g.size
			b := a + g.size
			piece := clipPlane(poly, axis, a, true)
			piece = clipPlane(piece, axis, b, false)
			if len(piece) < 3 {
				continue
			}
			cell[axis] = i
			rec(piece, axis+1, cell)
		}
	}
	rec(poly, 0, [3]int32{})
	return out
}

// clipPlane keeps the part of poly with coordinate axis >= at (keepAbove) or
// <= at.
func clipPlane(poly []mgl32.Vec3, axis int, at float32, keepAbove bool) []mgl32.Vec3 {
	in := func(q mgl32.Vec3) bool {
		if keepAbove {
			return q[axis] >= at
		}
		return q[axis] <= at
	}
	out := make([]mgl32.Vec3, 0, len(poly)+2)
	for i := range poly {
		a, b := poly[i], poly[(i+1)%len(poly)]
		ia, ib := in(a), in(b)
		if ia {
			out = append(out, a)
		}
		if ia != ib {
			t := (at - a[axis]) / (b[axis] - a[axis])
			q := a.Add(b.Sub(a).Mul(t))
			q[axis] = at
			out = append(out, q)
		}
	}
	return out
}

// addPiece adds one triangle's piece in one voxel: its exact area, sampled
// over a fan of sub-triangles for weight, normal, attributes and binding.
func (g *voxelGrid) addPiece(m *Mesh, v [3]uint32, ng mgl32.Vec3, pc voxelPiece, bary func(mgl32.Vec3) [3]float32) {
	a := g.cells[pc.cell]
	for f := 1; f+1 < len(pc.poly); f++ {
		p0, p1, p2 := pc.poly[0], pc.poly[f], pc.poly[f+1]
		e1, e2 := p1.Sub(p0), p2.Sub(p0)
		area := float64(e1.Cross(e2).Len()) * 0.5
		if area <= 0 {
			continue
		}
		edge := max(e1.Len(), e2.Len(), p2.Sub(p1).Len())
		k := min(max(int(math.Ceil(float64(edge/(g.size/3)))), 1), 4)
		share := area / float64(k*k)
		inv := 1 / float32(k)
		at := func(su, sv float32) {
			q := p0.Add(e1.Mul(su)).Add(e2.Mul(sv))
			for i := range g.scr {
				g.scr[i] = 0
			}
			s := g.opt.Sample(v, bary(q), g.size, g.scr)
			if s.Weight <= 0 {
				return
			}
			if a == nil {
				a = &voxelAcc{attrs: make([]float64, g.opt.Attrs), binds: map[int32][2]float64{}}
				g.cells[pc.cell] = a
			}
			w := share * float64(s.Weight)
			for k, d := range VoxelDirs {
				a.area[k] += w * math.Abs(float64(ng.Dot(d)))
			}
			for ax := 0; ax < 3; ax++ {
				a.nsum[ax] += w * float64(s.Normal[ax])
			}
			a.weight += w
			for i, x := range g.scr {
				a.attrs[i] += w * float64(x)
			}
			bw := a.binds[s.Bind]
			a.binds[s.Bind] = [2]float64{bw[0] + w, bw[1] + w*float64(s.BindWeight)}
		}
		// Sub-triangles of a k-grid over the piece's fan triangle: "up" ones
		// at (i, j) and "down" ones between them, a sample at each centroid.
		for i := 0; i < k; i++ {
			for j := 0; j < k-i; j++ {
				at((float32(i)+1.0/3)*inv, (float32(j)+1.0/3)*inv)
				if j < k-i-1 {
					at((float32(i)+2.0/3)*inv, (float32(j)+2.0/3)*inv)
				}
			}
		}
	}
}

// addVoxels folds a finer cluster's voxels into the grid; its lattice must
// be the grid's and its size a power-of-two fraction of the grid's.
func (g *voxelGrid) addVoxels(c *VoxelCluster) {
	for bi := range c.Bricks {
		b := &c.Bricks[bi]
		vi := int(b.First)
		for l := 0; l < BrickVoxels; l++ {
			if b.Mask&(1<<uint(l)) == 0 {
				continue
			}
			v := &c.Voxels[vi]
			lx, ly, lz := int32(l&3), int32((l>>2)&3), int32(l>>4)
			centre := c.Origin.Add(mgl32.Vec3{
				(float32(b.Pos[0]*BrickSide+lx) + 0.5) * c.Size,
				(float32(b.Pos[1]*BrickSide+ly) + 0.5) * c.Size,
				(float32(b.Pos[2]*BrickSide+lz) + 0.5) * c.Size})
			a := g.cell(centre)
			for k := range v.Area {
				a.area[k] += float64(v.Area[k])
			}
			for ax := 0; ax < 3; ax++ {
				a.nsum[ax] += float64(v.NormalSum[ax])
			}
			a.weight += float64(v.Weight)
			for i := 0; i < g.opt.Attrs; i++ {
				a.attrs[i] += float64(c.Attrs[vi*g.opt.Attrs+i]) * float64(v.Weight)
			}
			bw := a.binds[v.Bind]
			a.binds[v.Bind] = [2]float64{bw[0] + float64(v.Weight), bw[1] + float64(v.Weight)*float64(v.BindWeight)}
			vi++
		}
	}
}

// brickCount is how many bricks the grid's voxels fall in.
func (g *voxelGrid) brickCount() int {
	seen := map[[3]int32]bool{}
	for k := range g.cells {
		seen[brickOf(k)] = true
	}
	return len(seen)
}

func floorDiv(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func brickOf(k [3]int32) [3]int32 {
	return [3]int32{floorDiv(k[0], BrickSide), floorDiv(k[1], BrickSide), floorDiv(k[2], BrickSide)}
}

// clusters cuts the grid's voxels into clusters of at most MaxClusterBricks
// bricks, the bricks taken along a Morton walk so each cluster is compact.
func (g *voxelGrid) clusters() []VoxelCluster {
	type brickAcc struct {
		pos   [3]int32
		cells [BrickVoxels]*voxelAcc
		mask  uint64
	}
	bricks := map[[3]int32]*brickAcc{}
	for k, a := range g.cells {
		if a.weight <= 0 {
			continue
		}
		bp := brickOf(k)
		b := bricks[bp]
		if b == nil {
			b = &brickAcc{pos: bp}
			bricks[bp] = b
		}
		l := (k[0] - bp[0]*BrickSide) + BrickSide*(k[1]-bp[1]*BrickSide) + BrickSide*BrickSide*(k[2]-bp[2]*BrickSide)
		b.cells[l] = a
		b.mask |= 1 << uint(l)
	}
	if len(bricks) == 0 {
		return nil
	}
	list := make([]*brickAcc, 0, len(bricks))
	for _, b := range bricks {
		list = append(list, b)
	}
	// A fixed order first (map iteration is random), then the walk.
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i].pos, list[j].pos
		if a[2] != b[2] {
			return a[2] < b[2]
		}
		if a[1] != b[1] {
			return a[1] < b[1]
		}
		return a[0] < b[0]
	})
	cent := make([]mgl32.Vec3, len(list))
	for i, b := range list {
		cent[i] = mgl32.Vec3{float32(b.pos[0]), float32(b.pos[1]), float32(b.pos[2])}
	}
	order := mortonOrder(cent)
	nc := (len(list) + MaxClusterBricks - 1) / MaxClusterBricks
	per := (len(list) + nc - 1) / nc
	var out []VoxelCluster
	for start := 0; start < len(order); start += per {
		end := min(start+per, len(order))
		c := VoxelCluster{Size: g.size, Origin: g.origin}
		for _, oi := range order[start:end] {
			b := list[oi]
			br := Brick{Pos: b.pos, Mask: b.mask, First: uint32(len(c.Voxels))}
			binds := map[int32][2]float64{}
			for l := 0; l < BrickVoxels; l++ {
				a := b.cells[l]
				if a == nil {
					continue
				}
				v := Voxel{Local: uint8(l), Weight: float32(a.weight)}
				for k := range v.Area {
					v.Area[k] = float32(a.area[k])
				}
				for ax := 0; ax < 3; ax++ {
					v.NormalSum[ax] = float32(a.nsum[ax])
				}
				v.Bind, v.BindWeight = leadingBind(a.binds)
				for k, bw := range a.binds {
					o := binds[k]
					binds[k] = [2]float64{o[0] + bw[0], o[1] + bw[1]}
				}
				for _, x := range a.attrs {
					c.Attrs = append(c.Attrs, float32(x/a.weight))
				}
				c.Voxels = append(c.Voxels, v)
			}
			br.Bind, br.BindWeight = leadingBind(binds)
			c.Bricks = append(c.Bricks, br)
		}
		c.bound()
		out = append(out, c)
	}
	return out
}

// leadingBind is the binding with the most area, and its mean weight; ties
// to the lower binding, so the choice is deterministic.
func leadingBind(binds map[int32][2]float64) (int32, float32) {
	best, bestA := int32(0), -1.0
	for k, bw := range binds {
		if bw[0] > bestA || (bw[0] == bestA && k < best) {
			best, bestA = k, bw[0]
		}
	}
	if bestA <= 0 {
		return best, 0
	}
	return best, float32(binds[best][1] / bestA)
}

// bound sets the cluster's box and sphere from its bricks' occupied voxels.
func (c *VoxelCluster) bound() {
	first := true
	for bi := range c.Bricks {
		b := &c.Bricks[bi]
		for l := 0; l < BrickVoxels; l++ {
			if b.Mask&(1<<uint(l)) == 0 {
				continue
			}
			lo := c.Origin.Add(mgl32.Vec3{
				float32(b.Pos[0]*BrickSide+int32(l&3)) * c.Size,
				float32(b.Pos[1]*BrickSide+int32((l>>2)&3)) * c.Size,
				float32(b.Pos[2]*BrickSide+int32(l>>4)) * c.Size})
			hi := lo.Add(mgl32.Vec3{c.Size, c.Size, c.Size})
			if first {
				c.Lo, c.Hi, first = lo, hi, false
			} else {
				c.Lo, c.Hi = vmin(c.Lo, lo), vmax(c.Hi, hi)
			}
		}
	}
	c.Center = c.Lo.Add(c.Hi).Mul(0.5)
	c.Radius = c.Hi.Sub(c.Center).Len()
}

// Voxelize turns triangles (index triples into m) into voxel clusters of
// edge size on the lattice at origin — the building block the DAG uses,
// exported for tests and tools.
func Voxelize(m Mesh, idx []uint32, opt VoxelOptions, origin mgl32.Vec3, size float32) []VoxelCluster {
	g := newVoxelGrid(&opt, origin, size)
	g.addTriangles(&m, idx)
	return g.clusters()
}

// Coarsen folds voxel clusters into clusters of edge size on the same
// lattice (size a power-of-two multiple of theirs).
func Coarsen(cs []VoxelCluster, opt VoxelOptions, size float32) []VoxelCluster {
	if len(cs) == 0 {
		return nil
	}
	g := newVoxelGrid(&opt, cs[0].Origin, size)
	for i := range cs {
		g.addVoxels(&cs[i])
	}
	return g.clusters()
}
