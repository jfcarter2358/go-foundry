package nav

// grid.go — where an agent of one size can stand, and where it can step.
//
// The heightfield says where the level is solid; this says what that means to
// a body of a given radius and height with a given step. Every gap between two
// solid spans whose floor is walkable and whose ceiling clears the body is a
// node. Two nodes in neighbouring columns are linked when the body could walk
// from one to the other: the floors within a step of each other, and the
// headroom still clearing it at the higher of the two.
//
// Then the walkable area is ERODED by the radius. A node closer to an edge than
// the body is wide is dropped, so a path never asks a body to put its centre
// somewhere its shoulder would be in a wall — and a passage narrower than the
// body is simply not in the graph, rather than a place an agent grinds against
// forever. A ledge is an edge like a wall is, so agents keep a radius back from
// drops as well.
//
// Linked components get a region number, which is what lets a path request
// between two islands be refused in constant time instead of by flooding the
// whole of one of them.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// Params describe the agent a grid is built for.
//
// There is no slope here: what counts as too steep to stand on is the
// engine's, not the agent's (MaxSlopeCos), and the heightfield has already
// classified every surface by it.
type Params struct {
	Radius     float32
	Height     float32
	StepHeight float32
}

// direction offsets, orthogonal first. Diagonals are only ever taken where both
// orthogonals beside them are, so a path never cuts a corner the body cannot.
var dirDX = [8]int{1, 0, -1, 0, 1, -1, -1, 1}
var dirDZ = [8]int{0, 1, 0, -1, 1, 1, -1, -1}

// diagonal i (4..7) is made of orthogonals diagOrtho[i-4].
var diagOrtho = [4][2]int{{0, 1}, {2, 1}, {2, 3}, {0, 3}}

// Node is one place an agent can stand.
type Node struct {
	Col   int32
	Floor float32
	Ceil  float32
	// Links index the node reached by stepping in each direction, or -1.
	Links  [8]int32
	Region int32
}

// Grid is the navigation graph for one agent profile.
type Grid struct {
	HF     *Heightfield
	Params Params
	Nodes  []Node
	// colNodes indexes Nodes by column, as Heightfield.colStart indexes spans.
	colNodes []int32

	// A* scratch, reused across queries and stamped by generation so a query
	// does not clear arrays the size of the level. Navigation runs on the frame
	// loop's thread; nothing here is safe to share.
	gen    uint32
	stamp  []uint32
	g      []float32
	parent []int32
	closed []uint32
}

// NodePos is where an agent standing on node n has its feet.
func (gr *Grid) NodePos(n int32) mgl32.Vec3 {
	nd := &gr.Nodes[n]
	x, z := gr.HF.ColumnCenter(int(nd.Col))
	return mgl32.Vec3{x, nd.Floor, z}
}

// ColumnNodes returns the nodes standing in column c, lowest first.
func (gr *Grid) ColumnNodes(c int) []int32 {
	if c < 0 || c+1 >= len(gr.colNodes) {
		return nil
	}
	lo, hi := gr.colNodes[c], gr.colNodes[c+1]
	out := make([]int32, 0, hi-lo)
	for i := lo; i < hi; i++ {
		out = append(out, i)
	}
	return out
}

// buildGrid derives the graph for one profile from a heightfield.
func buildGrid(hf *Heightfield, p Params) *Grid {
	// A node is a span with room on top, so the spans bound the node count.
	gr := &Grid{HF: hf, Params: p, Nodes: make([]Node, 0, len(hf.spans))}
	cols := hf.Columns()
	gr.colNodes = make([]int32, cols+1)
	const openSky = float32(1e9)
	for c := 0; c < cols; c++ {
		gr.colNodes[c] = int32(len(gr.Nodes))
		col := hf.column(c)
		for k := range col {
			if !col[k].walkable {
				continue
			}
			ceil := openSky
			if k+1 < len(col) {
				ceil = col[k+1].min
			}
			if ceil-col[k].max < p.Height {
				continue
			}
			nd := Node{Col: int32(c), Floor: col[k].max, Ceil: ceil, Region: -1}
			for d := range nd.Links {
				nd.Links[d] = -1
			}
			gr.Nodes = append(gr.Nodes, nd)
		}
	}
	gr.colNodes[cols] = int32(len(gr.Nodes))

	gr.link()
	if p.Radius > 0 {
		gr.erode()
	}
	gr.label()
	return gr
}

// link joins every node to its neighbours.
func (gr *Grid) link() {
	hf := gr.HF
	p := gr.Params
	for ni := range gr.Nodes {
		a := &gr.Nodes[ni]
		ci, cj := int(a.Col)%hf.W, int(a.Col)/hf.W
		for d := 0; d < 4; d++ {
			i, j := ci+dirDX[d], cj+dirDZ[d]
			if i < 0 || j < 0 || i >= hf.W || j >= hf.D {
				continue
			}
			c := i + j*hf.W
			best, bestDY := int32(-1), float32(math.MaxFloat32)
			for bi := gr.colNodes[c]; bi < gr.colNodes[c+1]; bi++ {
				b := &gr.Nodes[bi]
				dy := abs32(b.Floor - a.Floor)
				if dy > p.StepHeight {
					continue
				}
				if min32(a.Ceil, b.Ceil)-max32(a.Floor, b.Floor) < p.Height {
					continue
				}
				if dy < bestDY {
					best, bestDY = bi, dy
				}
			}
			a.Links[d] = best
		}
	}
	gr.linkDiagonals()
}

// linkDiagonals adds a diagonal wherever the two orthogonal routes round it
// both exist and arrive at the same node.
func (gr *Grid) linkDiagonals() {
	for ni := range gr.Nodes {
		a := &gr.Nodes[ni]
		for d := 4; d < 8; d++ {
			a.Links[d] = -1
			o1, o2 := a.Links[diagOrtho[d-4][0]], a.Links[diagOrtho[d-4][1]]
			if o1 < 0 || o2 < 0 {
				continue
			}
			via1 := gr.Nodes[o1].Links[diagOrtho[d-4][1]]
			via2 := gr.Nodes[o2].Links[diagOrtho[d-4][0]]
			if via1 >= 0 && via1 == via2 {
				a.Links[d] = via1
			}
		}
	}
}

// erode drops every node closer to an edge than the agent's radius.
//
// Distance is measured over the links themselves, from every node missing an
// orthogonal neighbour, so a ledge and a wall are the same kind of edge. A
// border node's centre is half a column from whatever stopped it, which is the
// half added back before comparing.
//
// It is a two-pass chamfer distance transform — Recast's — rather than a
// search: nodes are stored in column order, so one sweep forward over the
// neighbours already visited and one back over the rest reach every node twice.
// That approximates a true search's distance to within a fraction of a column
// round tight corners, which is well inside the margin a radius measured in a
// handful of columns needs, and it is a good deal cheaper on a level whose floor
// is mostly open.
func (gr *Grid) erode() {
	n := len(gr.Nodes)
	dist := make([]float32, n)
	inf := float32(math.MaxFloat32)
	for i := range gr.Nodes {
		dist[i] = inf
		for d := 0; d < 4; d++ {
			if gr.Nodes[i].Links[d] < 0 {
				dist[i] = 0
				break
			}
		}
	}
	relax := func(i int, dirs [4]int) {
		di := dist[i]
		for k, d := range dirs {
			j := gr.Nodes[i].Links[d]
			if j < 0 {
				continue
			}
			step := float32(1)
			if k >= 2 {
				step = math.Sqrt2
			}
			if v := dist[j] + step; v < di {
				di = v
			}
		}
		dist[i] = di
	}
	// Forward: the neighbours that come earlier in column order — -X, -Z and
	// the two diagonals on the -Z side.
	for i := 0; i < n; i++ {
		relax(i, [4]int{2, 3, 6, 7})
	}
	// Back: +X, +Z and the diagonals on the +Z side.
	for i := n - 1; i >= 0; i-- {
		relax(i, [4]int{0, 1, 4, 5})
	}
	need := gr.Params.Radius/gr.HF.Cell - 0.5
	removed := make([]bool, n)
	any := false
	for i := range gr.Nodes {
		if dist[i] < need-1e-4 {
			removed[i] = true
			any = true
		}
	}
	if !any {
		return
	}
	// Compact: renumber the survivors and drop links to the removed.
	remap := make([]int32, n)
	out := gr.Nodes[:0]
	cols := gr.HF.Columns()
	newColNodes := make([]int32, cols+1)
	c := 0
	for i := 0; i < n; i++ {
		for c <= int(gr.Nodes[i].Col) {
			newColNodes[c] = int32(len(out))
			c++
		}
		if removed[i] {
			remap[i] = -1
			continue
		}
		remap[i] = int32(len(out))
		out = append(out, gr.Nodes[i])
	}
	for ; c <= cols; c++ {
		newColNodes[c] = int32(len(out))
	}
	for i := range out {
		for d := range out[i].Links {
			if l := out[i].Links[d]; l >= 0 {
				out[i].Links[d] = remap[l]
			}
		}
	}
	gr.Nodes = out
	gr.colNodes = newColNodes
	// A diagonal whose orthogonals went is a corner cut; rebuild them.
	gr.linkDiagonals()
}

// label numbers the connected components.
func (gr *Grid) label() {
	var stack []int32
	region := int32(0)
	for i := range gr.Nodes {
		if gr.Nodes[i].Region >= 0 {
			continue
		}
		gr.Nodes[i].Region = region
		stack = append(stack[:0], int32(i))
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, l := range gr.Nodes[k].Links {
				if l >= 0 && gr.Nodes[l].Region < 0 {
					gr.Nodes[l].Region = region
					stack = append(stack, l)
				}
			}
		}
		region++
	}
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// ---------- a small binary heap ----------

type heapItem struct {
	node int32
	key  float32
}

type nodeHeap struct{ items []heapItem }

func (h *nodeHeap) len() int { return len(h.items) }

func (h *nodeHeap) push(n int32, key float32) {
	h.items = append(h.items, heapItem{n, key})
	i := len(h.items) - 1
	for i > 0 {
		p := (i - 1) / 2
		if h.items[p].key <= h.items[i].key {
			break
		}
		h.items[p], h.items[i] = h.items[i], h.items[p]
		i = p
	}
}

func (h *nodeHeap) pop() (int32, float32) {
	top := h.items[0]
	last := len(h.items) - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	i := 0
	for {
		l, r := 2*i+1, 2*i+2
		m := i
		if l < len(h.items) && h.items[l].key < h.items[m].key {
			m = l
		}
		if r < len(h.items) && h.items[r].key < h.items[m].key {
			m = r
		}
		if m == i {
			break
		}
		h.items[m], h.items[i] = h.items[i], h.items[m]
		i = m
	}
	return top.node, top.key
}
