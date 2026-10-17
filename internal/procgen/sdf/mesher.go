package sdf

// mesher.go — turning a Shape into triangles: dual contouring on a sparse
// grid, multi-threaded, with gradient normals and an optional curvature
// weathering pass.
//
// # The grid
//
// The shape's box is covered by a grid of N³ cells of side Cell (N rounded up
// to whole BLOCKS of 16 cells, capped at MaxCells a side — 512³ effective).
// Almost all of it is far from the surface, so nothing is evaluated densely
// until it has to be:
//
//  1. A COARSE lattice, one sample every 4 cells over the whole grid (and an
//     apron of 4 cells around it), evaluated in parallel. A 4-cell SUBCUBE of
//     it is ACTIVE when its corners change sign, or one of them is within
//     reach of the surface: |F| < LocalLip·(subcube diagonal + margin). The
//     margin is what the weathering and the gradients read beyond the surface
//     (Weathering+2 cells). LocalLip is the shape's Lipschitz bound capped at
//     2: the global bound of a displaced, layered shape is far steeper than
//     its gradient almost anywhere, and trusting it would make the active
//     band ten times as thick. A feature thinner than a subcube in a place
//     the field is steeper than that can be missed — a small chip of
//     geometry, never a hole: the sign field stays consistent and the
//     surface it bounds closed.
//  2. A BLOCK (16³ cells, with an apron of 4 cells: 25³ samples) is processed
//     when any of its 6³ subcubes is active, on its own goroutine. Active
//     subcubes are evaluated exactly, sample by sample; the rest are filled
//     by trilinear interpolation of their coarse corners — the same sign, and
//     a smooth stand-in for the weathering's stencil to read.
//  3. WEATHERING (optional): a few explicit steps of curvature flow on the
//     block's samples, restricted to convex places — d += rate/6 · max(∇²d,
//     0). On a distance field the Laplacian is the sum of the principal
//     curvatures, positive where the surface is convex: corners and edges
//     move in, faces and crevices stay. That is spheroidal weathering in one
//     line: a block is attacked from more sides at its corners than its
//     faces. Each step is capped at half a cell, so a sample further than
//     that from the surface can never change sign — which is what lets two
//     blocks computing the same shared sample from the same neighbourhood
//     agree on it to the bit.
//  4. DUAL CONTOURING on the block's core, MANIFOLD: a cell whose corners
//     change sign gets one vertex for each separate piece of surface that
//     crosses it — its crossing edges grouped as marching cubes would join
//     them into polygons, every face deciding its own ambiguous case the same
//     way for both cells that share it (the inside corners kept apart) — so
//     two sheets passing through one cell (the faces of a narrow crack, two
//     bumps kissing) stay two sheets, and every edge of the mesh has exactly
//     two triangles. Each vertex is placed by the quadric error function of
//     its group's crossings (points by linear interpolation, normals the
//     field's gradient by central differences) — sharp edges and corners
//     stay sharp — pulled toward the crossings' mean by Sharpness (0 is
//     surface nets) and kept inside its cell. Each crossing edge owned by the
//     block (its start in the core) becomes a quad over its vertices in the
//     four cells around it, wound outward, split along the diagonal its
//     vertices' normals agree with best.
//
// Blocks are numbered in a fixed order and their vertices and quads
// concatenated in that order, so the mesh is identical on any number of
// workers.

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	blockCells = 16 // cells a block side
	coarseStep = 4  // cells a subcube side
	apron      = 4  // cells of apron around a block
	blockSpan  = blockCells + 2*apron + 1
	// MaxCells is the most cells a grid side may have.
	MaxCells = 512
	// MaxWeathering is the most flow steps (the apron's limit: the gradient
	// stencil needs one more sample than the flow has consumed).
	MaxWeathering = apron - 1
)

// MeshOptions configure Polygonize.
type MeshOptions struct {
	// Cell is the grid spacing in metres.
	Cell float64
	// Weathering is how many curvature-flow steps round the convex edges
	// (0..MaxWeathering), WeatherRate each step's size as a fraction of the
	// stable one (0..1; 0 is 0.8).
	Weathering  int
	WeatherRate float64
	// Sharpness is 0..1: 0 places each vertex at its crossings' mean (surface
	// nets, rounded), 1 at its quadric's minimum (dual contouring, sharp).
	Sharpness float64
	// Workers is the goroutines to use (0: GOMAXPROCS).
	Workers int
	// Ctx cancels the meshing.
	Ctx context.Context
	// Progress, if set, is told the fraction done (from any goroutine).
	Progress func(done float32)
}

// Mesh is a triangle mesh: positions and unit normals per vertex, a triangle
// list wound counter-clockwise seen from outside.
type Mesh struct {
	Positions []Vec3
	Normals   []Vec3
	Indices   []uint32
}

// TriangleCount is len(Indices)/3.
func (m *Mesh) TriangleCount() int { return len(m.Indices) / 3 }

// MeshStats says what a Polygonize did.
type MeshStats struct {
	Cells         [3]int // the grid's cells a side
	Blocks        int    // blocks processed
	ActiveSubcube int    // subcubes evaluated exactly
	Evaluations   int64  // calls of the shape's function
}

// grid is the sampling lattice: global sample g along axis k is at
// Origin[k] + g·Cell, g from −apron to N[k]+apron.
type grid struct {
	origin Vec3
	cell   float64
	n      [3]int // cells
	nb     [3]int // blocks
	nc     [3]int // coarse samples (n/4 + 3, from g = −4)
}

func (g *grid) pos(gx, gy, gz int) Vec3 {
	return Vec3{g.origin[0] + float64(gx)*g.cell, g.origin[1] + float64(gy)*g.cell, g.origin[2] + float64(gz)*g.cell}
}

// coarseIndex is coarse sample (kx, ky, kz)'s index.
func (g *grid) coarseIndex(kx, ky, kz int) int { return (kz*g.nc[1]+ky)*g.nc[0] + kx }

// subcube k spans coarse samples k..k+1 (global samples 4k−4 .. 4k).
func (g *grid) subIndex(kx, ky, kz int) int {
	return (kz*(g.nc[1]-1)+ky)*(g.nc[0]-1) + kx
}

// Polygonize meshes s at opt.Cell (see the file comment).
func Polygonize(s Shape, opt MeshOptions) (*Mesh, MeshStats, error) {
	var st MeshStats
	if opt.Cell <= 0 {
		return nil, st, fmt.Errorf("sdf: cell size %g", opt.Cell)
	}
	if s.Box.IsInfinite() || s.Box.Empty() {
		return nil, st, fmt.Errorf("sdf: the shape has no finite box")
	}
	ctx := opt.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	workers := opt.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	weather := max(min(opt.Weathering, MaxWeathering), 0)
	rate := opt.WeatherRate
	if rate <= 0 {
		rate = 0.8
	}
	rate = math.Min(rate, 1)
	sharp := Clamp(opt.Sharpness, 0, 1)

	// The grid: the box and two cells of air, in whole blocks.
	box := s.Box.Grow(2 * opt.Cell)
	size := box.Size()
	g := &grid{origin: box.Lo, cell: opt.Cell}
	for k := 0; k < 3; k++ {
		n := int(math.Ceil(size[k] / opt.Cell))
		n = (n + blockCells - 1) / blockCells * blockCells
		if n > MaxCells {
			return nil, st, fmt.Errorf("sdf: %d cells a side at %g m (the most is %d) — use a coarser cell", n, opt.Cell, MaxCells)
		}
		g.n[k] = max(n, blockCells)
		g.nb[k] = g.n[k] / blockCells
		g.nc[k] = g.n[k]/coarseStep + 3
	}
	st.Cells = g.n
	var evals atomic.Int64

	// 1. The coarse lattice.
	coarse := make([]float64, g.nc[0]*g.nc[1]*g.nc[2])
	if err := Parallel(ctx, workers, g.nc[2], func(kz int) {
		for ky := 0; ky < g.nc[1]; ky++ {
			for kx := 0; kx < g.nc[0]; kx++ {
				coarse[g.coarseIndex(kx, ky, kz)] = s.F(g.pos(4*kx-4, 4*ky-4, 4*kz-4))
			}
		}
		evals.Add(int64(g.nc[0] * g.nc[1]))
	}); err != nil {
		return nil, st, err
	}
	report(opt.Progress, 0.1)

	// Which subcubes are active.
	localLip := math.Min(math.Max(s.Lip, 1), 2)
	reach := localLip * (float64(coarseStep)*math.Sqrt(3) + float64(weather+2)) * opt.Cell
	ns := [3]int{g.nc[0] - 1, g.nc[1] - 1, g.nc[2] - 1}
	active := make([]bool, ns[0]*ns[1]*ns[2])
	nActive := 0
	for kz := 0; kz < ns[2]; kz++ {
		for ky := 0; ky < ns[1]; ky++ {
			for kx := 0; kx < ns[0]; kx++ {
				neg, pos, near := false, false, false
				for c := 0; c < 8; c++ {
					v := coarse[g.coarseIndex(kx+c&1, ky+c>>1&1, kz+c>>2&1)]
					neg = neg || v < 0
					pos = pos || v >= 0
					near = near || math.Abs(v) < reach
				}
				if (neg && pos) || near {
					active[g.subIndex(kx, ky, kz)] = true
					nActive++
				}
			}
		}
	}
	st.ActiveSubcube = nActive

	// 2. The blocks to process: any of their 6³ subcubes active.
	var blocks []int
	for bz := 0; bz < g.nb[2]; bz++ {
		for by := 0; by < g.nb[1]; by++ {
			for bx := 0; bx < g.nb[0]; bx++ {
				if blockActive(g, active, bx, by, bz) {
					blocks = append(blocks, (bz*g.nb[1]+by)*g.nb[0]+bx)
				}
			}
		}
	}
	st.Blocks = len(blocks)
	results := make([]*blockResult, len(blocks))
	slot := make(map[int]int, len(blocks)) // block id → index in blocks
	for i, b := range blocks {
		slot[b] = i
	}
	var done atomic.Int64
	pool := sync.Pool{New: func() any { return newBlockWork() }}
	bp := blockParams{g: g, s: s, coarse: coarse, active: active, weather: weather, rate: rate, sharp: sharp}
	if err := Parallel(ctx, workers, len(blocks), func(i int) {
		w := pool.Get().(*blockWork)
		b := blocks[i]
		bx, by, bz := b%g.nb[0], b/g.nb[0]%g.nb[1], b/(g.nb[0]*g.nb[1])
		results[i] = processBlock(&bp, w, bx, by, bz, &evals)
		pool.Put(w)
		report(opt.Progress, 0.1+0.8*float32(done.Add(1))/float32(max(len(blocks), 1)))
	}); err != nil {
		return nil, st, err
	}

	// 3. Number the vertices, then join the quads across blocks.
	base := make([]uint32, len(blocks))
	total := 0
	for i, r := range results {
		base[i] = uint32(total)
		total += len(r.pos)
	}
	m := &Mesh{Positions: make([]Vec3, 0, total), Normals: make([]Vec3, 0, total)}
	for _, r := range results {
		m.Positions = append(m.Positions, r.pos...)
		m.Normals = append(m.Normals, r.nrm...)
	}
	// cellVertex is the global vertex of global cell (cx, cy, cz) on its
	// edge e (cubeEdges), or −1.
	cellVertex := func(cx, cy, cz, e int) int64 {
		if cx < 0 || cy < 0 || cz < 0 || cx >= g.n[0] || cy >= g.n[1] || cz >= g.n[2] {
			return -1
		}
		bx, by, bz := cx/blockCells, cy/blockCells, cz/blockCells
		i, ok := slot[(bz*g.nb[1]+by)*g.nb[0]+bx]
		if !ok {
			return -1
		}
		lx, ly, lz := cx-bx*blockCells, cy-by*blockCells, cz-bz*blockCells
		c := (lz*blockCells+ly)*blockCells + lx
		v := results[i].cell[c]
		gr := results[i].group[12*c+e]
		if v < 0 || gr < 0 {
			return -1
		}
		return int64(base[i]) + int64(v) + int64(gr)
	}
	tris := make([][]uint32, len(blocks))
	if err := Parallel(ctx, workers, len(blocks), func(i int) {
		b := blocks[i]
		bx, by, bz := b%g.nb[0], b/g.nb[0]%g.nb[1], b/(g.nb[0]*g.nb[1])
		tris[i] = joinQuads(results[i], m, bx*blockCells, by*blockCells, bz*blockCells, cellVertex)
	}); err != nil {
		return nil, st, err
	}
	n := 0
	for _, t := range tris {
		n += len(t)
	}
	m.Indices = make([]uint32, 0, n)
	for _, t := range tris {
		m.Indices = append(m.Indices, t...)
	}
	st.Evaluations = evals.Load()
	report(opt.Progress, 1)
	return m, st, nil
}

func report(p func(float32), f float32) {
	if p != nil {
		p(f)
	}
}

// blockActive reports whether any of block (bx, by, bz)'s 6³ subcubes is.
func blockActive(g *grid, active []bool, bx, by, bz int) bool {
	for kz := 4 * bz; kz < 4*bz+6; kz++ {
		for ky := 4 * by; ky < 4*by+6; ky++ {
			for kx := 4 * bx; kx < 4*bx+6; kx++ {
				if active[g.subIndex(kx, ky, kz)] {
					return true
				}
			}
		}
	}
	return false
}

// blockParams is what every block reads.
type blockParams struct {
	g       *grid
	s       Shape
	coarse  []float64
	active  []bool
	weather int
	rate    float64
	sharp   float64
}

// blockWork is a worker's scratch: the block's samples, a copy for the flow,
// which were evaluated, and the gradients.
type blockWork struct {
	d, tmp []float64
	exact  []bool
	grad   []Vec3
}

func newBlockWork() *blockWork {
	n := blockSpan * blockSpan * blockSpan
	return &blockWork{d: make([]float64, n), tmp: make([]float64, n), exact: make([]bool, n), grad: make([]Vec3, n)}
}

// li is local sample (x, y, z)'s index, 0..blockSpan−1 each (the core is
// apron..apron+blockCells).
func li(x, y, z int) int { return (z*blockSpan+y)*blockSpan + x }

// blockResult is one block's vertices and crossing edges.
type blockResult struct {
	pos, nrm []Vec3
	// cell is each core cell's first vertex (local to this block), −1 for
	// none; group, twelve a cell, which of its vertices each edge's crossing
	// belongs to (−1: the edge does not cross).
	cell  []int32
	group []int8
	// edges are the crossing edges the block owns: core sample (x, y, z),
	// axis, and whether the start is inside.
	edges []crossEdge
}

type crossEdge struct {
	x, y, z int16
	axis    int8
	inside  bool
}

// processBlock evaluates, weathers and contours one block.
func processBlock(bp *blockParams, w *blockWork, bx, by, bz int, evals *atomic.Int64) *blockResult {
	g := bp.g
	// The block's first sample, globally.
	gx0, gy0, gz0 := bx*blockCells-apron, by*blockCells-apron, bz*blockCells-apron
	for i := range w.exact {
		w.exact[i] = false
	}
	// Exact samples in the active subcubes.
	var n int64
	for sz := 0; sz < 6; sz++ {
		for sy := 0; sy < 6; sy++ {
			for sx := 0; sx < 6; sx++ {
				if !bp.active[g.subIndex(4*bx+sx, 4*by+sy, 4*bz+sz)] {
					continue
				}
				for z := 4 * sz; z <= 4*sz+4; z++ {
					for y := 4 * sy; y <= 4*sy+4; y++ {
						for x := 4 * sx; x <= 4*sx+4; x++ {
							i := li(x, y, z)
							if w.exact[i] {
								continue
							}
							w.d[i] = bp.s.F(g.pos(gx0+x, gy0+y, gz0+z))
							w.exact[i] = true
							n++
						}
					}
				}
			}
		}
	}
	evals.Add(n)
	// The rest from the coarse corners of their subcube.
	for z := 0; z < blockSpan; z++ {
		sz := min(z/4, 5)
		fz := float64(z-4*sz) / 4
		for y := 0; y < blockSpan; y++ {
			sy := min(y/4, 5)
			fy := float64(y-4*sy) / 4
			for x := 0; x < blockSpan; x++ {
				i := li(x, y, z)
				if w.exact[i] {
					continue
				}
				sx := min(x/4, 5)
				fx := float64(x-4*sx) / 4
				kx, ky, kz := 4*bx+sx, 4*by+sy, 4*bz+sz
				c := func(dx, dy, dz int) float64 { return bp.coarse[g.coarseIndex(kx+dx, ky+dy, kz+dz)] }
				a := Mix(Mix(c(0, 0, 0), c(1, 0, 0), fx), Mix(c(0, 1, 0), c(1, 1, 0), fx), fy)
				b := Mix(Mix(c(0, 0, 1), c(1, 0, 1), fx), Mix(c(0, 1, 1), c(1, 1, 1), fx), fy)
				w.d[i] = Mix(a, b, fz)
			}
		}
	}

	// Weathering: convex curvature flow, each step valid one sample further
	// in from the block's edge.
	h := g.cell
	for it := 0; it < bp.weather; it++ {
		copy(w.tmp, w.d)
		lo, hi := 1+it, blockSpan-2-it
		for z := lo; z <= hi; z++ {
			for y := lo; y <= hi; y++ {
				for x := lo; x <= hi; x++ {
					i := li(x, y, z)
					c := w.tmp[i]
					lap := w.tmp[i-1] + w.tmp[i+1] + w.tmp[i-blockSpan] + w.tmp[i+blockSpan] +
						w.tmp[i-blockSpan*blockSpan] + w.tmp[i+blockSpan*blockSpan] - 6*c
					if lap > 0 {
						w.d[i] = c + math.Min(bp.rate/6*lap, 0.5*h)
					}
				}
			}
		}
	}

	// Gradients over the core's samples (the cells' corners), from samples
	// the flow has fully reached (it has, one beyond the core, for
	// Weathering ≤ MaxWeathering).
	for z := apron; z <= apron+blockCells; z++ {
		for y := apron; y <= apron+blockCells; y++ {
			for x := apron; x <= apron+blockCells; x++ {
				i := li(x, y, z)
				w.grad[i] = Vec3{
					w.d[i+1] - w.d[i-1],
					w.d[i+blockSpan] - w.d[i-blockSpan],
					w.d[i+blockSpan*blockSpan] - w.d[i-blockSpan*blockSpan],
				}
			}
		}
	}

	r := &blockResult{cell: make([]int32, blockCells*blockCells*blockCells), group: make([]int8, 12*blockCells*blockCells*blockCells)}
	for i := range r.cell {
		r.cell[i] = -1
	}
	// Vertices: every core cell whose corners change sign.
	for cz := 0; cz < blockCells; cz++ {
		for cy := 0; cy < blockCells; cy++ {
			for cx := 0; cx < blockCells; cx++ {
				x, y, z := cx+apron, cy+apron, cz+apron
				var mask int
				for c := 0; c < 8; c++ {
					if w.d[li(x+c&1, y+c>>1&1, z+c>>2&1)] < 0 {
						mask |= 1 << c
					}
				}
				if mask == 0 || mask == 255 {
					continue
				}
				ci := (cz*blockCells+cy)*blockCells + cx
				r.cell[ci] = int32(len(r.pos))
				corner := g.pos(gx0+x, gy0+y, gz0+z)
				cellVertices(w, x, y, z, mask, bp.sharp, r.group[12*ci:12*ci+12], func(p, n Vec3) {
					r.pos = append(r.pos, corner.Add(p.Mul(h)))
					r.nrm = append(r.nrm, n)
				})
			}
		}
	}
	// Crossing edges the block owns: start in the core, the other two
	// coordinates in the core's first blockCells.
	for z := 0; z < blockCells; z++ {
		for y := 0; y < blockCells; y++ {
			for x := 0; x < blockCells; x++ {
				i := li(x+apron, y+apron, z+apron)
				d0 := w.d[i]
				for a, step := range [3]int{1, blockSpan, blockSpan * blockSpan} {
					d1 := w.d[i+step]
					if (d0 < 0) != (d1 < 0) {
						r.edges = append(r.edges, crossEdge{int16(x), int16(y), int16(z), int8(a), d0 < 0})
					}
				}
			}
		}
	}
	return r
}

// cubeEdges are a cell's twelve edges as corner pairs (corner c is at
// (c&1, c>>1&1, c>>2&1)).
var cubeEdges = [12][2]int{
	{0, 1}, {2, 3}, {4, 5}, {6, 7}, // x
	{0, 2}, {1, 3}, {4, 6}, {5, 7}, // y
	{0, 4}, {1, 5}, {2, 6}, {3, 7}, // z
}

// cubeFaces are a cell's six faces: their four corners, in order round the
// face, and their four edges (cubeEdges indices), edge k joining corners k
// and k+1.
var cubeFaces = func() [6]struct{ corners, edges [4]int } {
	var f [6]struct{ corners, edges [4]int }
	edgeOf := func(a, b int) int {
		for i, e := range cubeEdges {
			if (e[0] == a && e[1] == b) || (e[0] == b && e[1] == a) {
				return i
			}
		}
		panic("no cube edge")
	}
	for axis := 0; axis < 3; axis++ {
		u, v := (axis+1)%3, (axis+2)%3
		for side := 0; side < 2; side++ {
			fi := 2*axis + side
			at := func(du, dv int) int { return side<<axis | du<<u | dv<<v }
			f[fi].corners = [4]int{at(0, 0), at(1, 0), at(1, 1), at(0, 1)}
			for k := 0; k < 4; k++ {
				f[fi].edges[k] = edgeOf(f[fi].corners[k], f[fi].corners[(k+1)%4])
			}
		}
	}
	return f
}()

// cellVertices places the vertices of the cell at local sample (x, y, z)
// whose inside corners are mask: one per group of its crossing edges (see the
// file comment), each emitted (position in cell units from the cell's first
// corner, and normal) in group order, with each edge's group written to
// group.
func cellVertices(w *blockWork, x, y, z, mask int, sharp float64, group []int8, emit func(p, n Vec3)) {
	var cd [8]float64
	var cg [8]Vec3
	for c := 0; c < 8; c++ {
		i := li(x+c&1, y+c>>1&1, z+c>>2&1)
		cd[c], cg[c] = w.d[i], w.grad[i]
	}
	inside := func(c int) bool { return mask>>c&1 == 1 }
	crosses := func(e int) bool { return inside(cubeEdges[e][0]) != inside(cubeEdges[e][1]) }
	// Group the crossing edges: face by face, the crossings that bound one
	// piece of the face's inside.
	var parent [12]int8
	for i := range parent {
		parent[i] = int8(i)
	}
	find := func(e int8) int8 {
		for parent[e] != e {
			e = parent[e]
		}
		return e
	}
	join := func(a, b int) {
		ra, rb := find(int8(a)), find(int8(b))
		if ra != rb {
			parent[max(ra, rb)] = min(ra, rb)
		}
	}
	for _, f := range cubeFaces {
		var xs []int
		for _, e := range f.edges {
			if crosses(e) {
				xs = append(xs, e)
			}
		}
		switch len(xs) {
		case 2:
			join(xs[0], xs[1])
		case 4:
			// The diagonal case: each inside corner is cut off by its own
			// two edges.
			for k, c := range f.corners {
				if inside(c) {
					join(f.edges[(k+3)%4], f.edges[k])
				}
			}
		}
	}
	for i := range group {
		group[i] = -1
	}
	var roots [12]int8
	nGroups := int8(0)
	for e := 0; e < 12; e++ {
		if !crosses(e) {
			continue
		}
		r := find(int8(e))
		if r == int8(e) {
			roots[e] = nGroups
			nGroups++
		}
	}
	for e := 0; e < 12; e++ {
		if crosses(e) {
			group[e] = roots[find(int8(e))]
		}
	}
	corner := func(c int) Vec3 { return Vec3{float64(c & 1), float64(c >> 1 & 1), float64(c >> 2 & 1)} }
	for gi := int8(0); gi < nGroups; gi++ {
		// The quadric of the group's crossings: Σ n nᵀ, Σ n (n·p).
		var a00, a01, a02, a11, a12, a22, b0, b1, b2 float64
		var mass Vec3
		k := 0
		for e := 0; e < 12; e++ {
			if group[e] != gi {
				continue
			}
			c0, c1 := cubeEdges[e][0], cubeEdges[e][1]
			d0, d1 := cd[c0], cd[c1]
			t := d0 / (d0 - d1)
			p := corner(c0).Add(corner(c1).Sub(corner(c0)).Mul(t))
			n := cg[c0].Add(cg[c1].Sub(cg[c0]).Mul(t)).Normalize()
			mass = mass.Add(p)
			k++
			dp := n.Dot(p)
			a00 += n[0] * n[0]
			a01 += n[0] * n[1]
			a02 += n[0] * n[2]
			a11 += n[1] * n[1]
			a12 += n[1] * n[2]
			a22 += n[2] * n[2]
			b0 += n[0] * dp
			b1 += n[1] * dp
			b2 += n[2] * dp
		}
		mass = mass.Mul(1 / float64(max(k, 1)))
		p := mass
		if sharp > 0 {
			// Regularised toward the mass point: λ large is surface nets.
			lam := Mix(4, 0.02, sharp)
			a00 += lam
			a11 += lam
			a22 += lam
			b0 += lam * mass[0]
			b1 += lam * mass[1]
			b2 += lam * mass[2]
			if q, ok := solveSym3(a00, a01, a02, a11, a12, a22, Vec3{b0, b1, b2}); ok {
				// Inside the cell, a hair off its faces: two neighbours'
				// vertices clamped to one face would meet, and a cook's
				// quantisation would turn their triangles into slivers.
				const in = 1e-3
				p = Vec3{Clamp(q[0], in, 1-in), Clamp(q[1], in, 1-in), Clamp(q[2], in, 1-in)}
			}
			if nGroups > 1 {
				// Two sheets in one cell: keep each nearer its own
				// crossings than the other's.
				p = mass.Add(p.Sub(mass).Mul(0.5))
			}
		}
		// The normal: the gradient, trilinear at the vertex.
		fx, fy, fz := p[0], p[1], p[2]
		lerp := func(a, b Vec3, t float64) Vec3 { return a.Add(b.Sub(a).Mul(t)) }
		n := lerp(lerp(lerp(cg[0], cg[1], fx), lerp(cg[2], cg[3], fx), fy), lerp(lerp(cg[4], cg[5], fx), lerp(cg[6], cg[7], fx), fy), fz)
		emit(p, n.Normalize())
	}
}

// edgeInCell is the cubeEdges index, within the cell offset (db, dc) back
// along the edge's other two axes, of a grid edge along axis a.
func edgeInCell(a, db, dc int) int {
	switch a {
	case 0: // b = y, c = z: start corner (0, db, dc)
		return db + 2*dc
	case 1: // b = z, c = x: start corner (dc, 0, db)
		return 4 + dc + 2*db
	}
	return 8 + db + 2*dc // b = x, c = y: start corner (db, dc, 0)
}

// solveSym3 solves the symmetric 3x3 system A x = b.
func solveSym3(a00, a01, a02, a11, a12, a22 float64, b Vec3) (Vec3, bool) {
	c00 := a11*a22 - a12*a12
	c01 := a02*a12 - a01*a22
	c02 := a01*a12 - a02*a11
	det := a00*c00 + a01*c01 + a02*c02
	if math.Abs(det) < 1e-12 {
		return Vec3{}, false
	}
	c11 := a00*a22 - a02*a02
	c12 := a01*a02 - a00*a12
	c22 := a00*a11 - a01*a01
	inv := 1 / det
	return Vec3{
		(c00*b[0] + c01*b[1] + c02*b[2]) * inv,
		(c01*b[0] + c11*b[1] + c12*b[2]) * inv,
		(c02*b[0] + c12*b[1] + c22*b[2]) * inv,
	}, true
}

// joinQuads turns block r's crossing edges into triangles, its core starting
// at global cell (cx0, cy0, cz0).
func joinQuads(r *blockResult, m *Mesh, cx0, cy0, cz0 int, cellVertex func(cx, cy, cz, e int) int64) []uint32 {
	out := make([]uint32, 0, len(r.edges)*6)
	for _, e := range r.edges {
		s := [3]int{cx0 + int(e.x), cy0 + int(e.y), cz0 + int(e.z)}
		a := int(e.axis)
		b, c := (a+1)%3, (a+2)%3
		at := func(db, dc int) int64 {
			q := s
			q[b] -= db
			q[c] -= dc
			return cellVertex(q[0], q[1], q[2], edgeInCell(a, db, dc))
		}
		q0, q1, q2, q3 := at(0, 0), at(1, 0), at(1, 1), at(0, 1)
		if q0 < 0 || q1 < 0 || q2 < 0 || q3 < 0 {
			continue
		}
		v := [4]uint32{uint32(q0), uint32(q1), uint32(q2), uint32(q3)}
		if !e.inside {
			v[1], v[3] = v[3], v[1]
		}
		// The diagonal whose two triangles agree best with the normals.
		if splitScore(m, v[0], v[1], v[2], v[3]) >= splitScore(m, v[1], v[2], v[3], v[0]) {
			out = append(out, v[0], v[1], v[2], v[0], v[2], v[3])
		} else {
			out = append(out, v[1], v[2], v[3], v[1], v[3], v[0])
		}
	}
	return out
}

// splitScore is how well the triangles (a, b, c) and (a, c, d) face along
// their vertices' normals: the smaller of the two agreements.
func splitScore(m *Mesh, a, b, c, d uint32) float64 {
	tri := func(i, j, k uint32) float64 {
		n := m.Positions[j].Sub(m.Positions[i]).Cross(m.Positions[k].Sub(m.Positions[i]))
		l := n.Len()
		if l == 0 {
			return -1
		}
		avg := m.Normals[i].Add(m.Normals[j]).Add(m.Normals[k])
		return n.Mul(1 / l).Dot(avg.Normalize())
	}
	return math.Min(tri(a, b, c), tri(a, c, d))
}

// Parallel runs fn(0..n−1) on workers goroutines (0: GOMAXPROCS), stopping early (with the
// context's error) when ctx is cancelled. A panic in fn is re-raised.
func Parallel(ctx context.Context, workers, n int, fn func(i int)) error {
	if n == 0 {
		return ctx.Err()
	}
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = max(min(workers, n), 1)
	var next atomic.Int64
	var wg sync.WaitGroup
	var perr atomic.Value
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					perr.Store(fmt.Errorf("sdf: %v", r))
				}
			}()
			for {
				i := int(next.Add(1) - 1)
				if i >= n || ctx.Err() != nil {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
	if e, ok := perr.Load().(error); ok {
		return e
	}
	return ctx.Err()
}
