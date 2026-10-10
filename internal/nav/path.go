package nav

// path.go — finding a way from here to there.
//
// A* over the grid's nodes, then string-pulled: the raw result steps from
// column centre to column centre, which is a staircase across any open floor,
// so every corner that can be seen past along the grid is dropped. What is left
// is the handful of points a body actually has to turn at.
//
// "Seen past" is walked over the links rather than tested against geometry. A
// straight line that stays on linked nodes is one the body can walk — erosion
// already kept every node a radius clear of the walls — and walking the links
// also refuses a shortcut across a drop or up a riser too tall to step, which a
// ray through empty air would happily take.
//
// A request that cannot be met in full still gets an answer: the path to the
// reachable node nearest the goal, marked partial. A guard told to chase
// someone standing on a crate should come and stand at the foot of it, not
// stand still and report failure.

import (
	"math"
	"math/rand"

	"github.com/go-gl/mathgl/mgl32"
)

// MaxExpansions bounds one search, so a request across a huge level costs a
// bounded hitch rather than an unbounded one. The search answers with the best
// partial path it had when it ran out.
const MaxExpansions = 200000

// Path is a found route.
type Path struct {
	// Points are the corners, feet-height, starting at the first corner past
	// the start and ending at the goal (or as near it as could be reached).
	Points []mgl32.Vec3
	// Partial is set when the goal itself could not be reached.
	Partial bool
}

// Locate finds the node a point stands on: in its own column, the highest floor
// at or below a step above it; failing that, the nearest node within radius
// world units. ok is false when there is nothing that close.
func (gr *Grid) Locate(p mgl32.Vec3, radius float32) (int32, bool) {
	if gr == nil || len(gr.Nodes) == 0 {
		return -1, false
	}
	hf := gr.HF
	up := gr.Params.StepHeight + 0.05
	if c := hf.ColumnAt(p.X(), p.Z()); c >= 0 {
		best := int32(-1)
		for n := gr.colNodes[c]; n < gr.colNodes[c+1]; n++ {
			if gr.Nodes[n].Floor <= p.Y()+up {
				best = n // ascending, so the last one wins
			}
		}
		if best >= 0 && p.Y()-gr.Nodes[best].Floor < gr.Params.Height+up {
			return best, true
		}
	}
	// Search rings outward. Rank by a distance that counts height as well, so a
	// point on a balcony prefers the balcony to the courtyard under it.
	ci, cj := hf.cellIndex(p.X(), p.Z())
	rings := int(radius/hf.Cell) + 1
	best, bestD := int32(-1), float32(math.MaxFloat32)
	for r := 0; r <= rings; r++ {
		for j := cj - r; j <= cj+r; j++ {
			for i := ci - r; i <= ci+r; i++ {
				if r > 0 && i != ci-r && i != ci+r && j != cj-r && j != cj+r {
					continue
				}
				if i < 0 || j < 0 || i >= hf.W || j >= hf.D {
					continue
				}
				c := i + j*hf.W
				for n := gr.colNodes[c]; n < gr.colNodes[c+1]; n++ {
					np := gr.NodePos(n)
					dy := np.Y() - p.Y()
					if dy > up {
						dy *= 2 // above the point is a worse guess than below it
					}
					d := sq(np.X()-p.X()) + sq(np.Z()-p.Z()) + sq(dy)
					if d < bestD {
						best, bestD = n, d
					}
				}
			}
		}
		// Anything in a later ring is at least r columns away.
		if best >= 0 && float32(math.Sqrt(float64(bestD))) <= float32(r)*hf.Cell {
			break
		}
	}
	if best < 0 || float32(math.Sqrt(float64(bestD))) > radius+hf.Cell {
		return -1, false
	}
	return best, true
}

// FindPath finds a route between two points. ok is false only when either end
// is nowhere near the grid; an unreachable goal gives a partial path instead.
func (gr *Grid) FindPath(from, to mgl32.Vec3) (Path, bool) {
	start, ok := gr.Locate(from, 2)
	if !ok {
		return Path{}, false
	}
	goal, ok := gr.Locate(to, 4)
	if !ok {
		return Path{}, false
	}
	nodes, reached := gr.search(start, goal)
	if len(nodes) == 0 {
		return Path{}, false
	}
	corners := gr.pull(nodes)
	pts := make([]mgl32.Vec3, 0, len(corners))
	for _, n := range corners[1:] {
		pts = append(pts, gr.NodePos(n))
	}
	if reached {
		// End on the point asked for rather than the middle of its column, at
		// the floor the goal node stands on.
		end := mgl32.Vec3{to.X(), gr.Nodes[goal].Floor, to.Z()}
		if gr.HF.ColumnAt(to.X(), to.Z()) != int(gr.Nodes[goal].Col) {
			end = gr.NodePos(goal)
		}
		if len(pts) == 0 {
			pts = append(pts, end)
		} else {
			pts[len(pts)-1] = end
		}
	}
	if len(pts) == 0 {
		pts = append(pts, gr.NodePos(start))
	}
	return Path{Points: pts, Partial: !reached}, true
}

// Reachable reports whether a walk from one point could arrive at the other.
func (gr *Grid) Reachable(from, to mgl32.Vec3) bool {
	a, ok := gr.Locate(from, 2)
	if !ok {
		return false
	}
	b, ok := gr.Locate(to, 4)
	return ok && gr.Nodes[a].Region == gr.Nodes[b].Region
}

// searchOpen is A*'s open list, kept between searches so a search does not
// grow a fresh heap from nothing — on a long route, thousands of pushes and a
// dozen reallocations. Searches run on the frame loop's thread only, the same
// rule the Grid's own A* scratch follows; the heap's contents are reset per
// search and pushed and popped in exactly the order a fresh one would be.
var searchOpen nodeHeap

// search runs A* and returns the node chain, and whether it ends at goal.
func (gr *Grid) search(start, goal int32) ([]int32, bool) {
	n := len(gr.Nodes)
	if len(gr.stamp) != n {
		gr.stamp = make([]uint32, n)
		gr.closed = make([]uint32, n)
		gr.g = make([]float32, n)
		gr.parent = make([]int32, n)
		gr.gen = 0
	}
	gr.gen++
	if gr.gen == 0 {
		for i := range gr.stamp {
			gr.stamp[i], gr.closed[i] = 0, 0
		}
		gr.gen = 1
	}
	gen := gr.gen
	if start == goal {
		return []int32{start}, true
	}

	sameRegion := gr.Nodes[start].Region == gr.Nodes[goal].Region
	goalPos := gr.NodePos(goal)
	hf := gr.HF
	heur := func(i int32) float32 {
		p := gr.NodePos(i)
		dx := abs32(p.X()-goalPos.X()) / hf.Cell
		dz := abs32(p.Z()-goalPos.Z()) / hf.Cell
		lo, hi := dx, dz
		if lo > hi {
			lo, hi = hi, lo
		}
		return hi + (math.Sqrt2-1)*lo + abs32(p.Y()-goalPos.Y())/hf.Cell
	}

	open := &searchOpen
	open.items = open.items[:0]
	gr.stamp[start], gr.g[start], gr.parent[start] = gen, 0, -1
	open.push(start, heur(start))
	best, bestH := start, heur(start)
	expansions := 0
	for open.len() > 0 {
		// A node reached again more cheaply is pushed again rather than
		// re-keyed; the cheaper entry pops first and closes it, and the stale
		// one is skipped here.
		cur, _ := open.pop()
		if gr.closed[cur] == gen {
			continue
		}
		gr.closed[cur] = gen
		if cur == goal {
			best = goal
			break
		}
		if h := heur(cur); h < bestH {
			best, bestH = cur, h
		}
		if expansions++; expansions > MaxExpansions {
			break
		}
		// Out of the goal's region, the search is only looking for the nearest
		// approach; it cannot arrive, so it need not expand forever either.
		if !sameRegion && expansions > MaxExpansions/4 {
			break
		}
		for d := 0; d < 8; d++ {
			nb := gr.Nodes[cur].Links[d]
			if nb < 0 || gr.closed[nb] == gen {
				continue
			}
			step := float32(1)
			if d >= 4 {
				step = math.Sqrt2
			}
			step += abs32(gr.Nodes[nb].Floor-gr.Nodes[cur].Floor) / hf.Cell
			ng := gr.g[cur] + step
			if gr.stamp[nb] == gen && ng >= gr.g[nb] {
				continue
			}
			gr.stamp[nb], gr.g[nb], gr.parent[nb] = gen, ng, cur
			open.push(nb, ng+heur(nb))
		}
	}

	var chain []int32
	for k := best; k >= 0; k = gr.parent[k] {
		chain = append(chain, k)
		if k == start {
			break
		}
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, best == goal
}

// pull drops every corner of a node chain that a straight walk can skip.
func (gr *Grid) pull(chain []int32) []int32 {
	if len(chain) <= 2 {
		return chain
	}
	out := []int32{chain[0]}
	anchor := 0
	for i := 2; i < len(chain); i++ {
		if !gr.WalkableLine(chain[anchor], chain[i]) {
			anchor = i - 1
			out = append(out, chain[anchor])
		}
	}
	return append(out, chain[len(chain)-1])
}

// WalkableLine reports whether a body could walk straight from node a to node
// b without leaving the grid: stepping column to column along the line, each
// step taken over a link.
func (gr *Grid) WalkableLine(a, b int32) bool {
	if a == b {
		return true
	}
	hf := gr.HF
	pa, pb := gr.NodePos(a), gr.NodePos(b)
	// In column units, from the centre of a's column.
	x0, z0 := (pa.X()-hf.Origin.X())/hf.Cell, (pa.Z()-hf.Origin.Y())/hf.Cell
	x1, z1 := (pb.X()-hf.Origin.X())/hf.Cell, (pb.Z()-hf.Origin.Y())/hf.Cell
	ci, cj := int(math.Floor(float64(x0))), int(math.Floor(float64(z0)))
	ti, tj := int(math.Floor(float64(x1))), int(math.Floor(float64(z1)))
	dx, dz := x1-x0, z1-z0
	stepI, stepJ := 1, 1
	if dx < 0 {
		stepI = -1
	}
	if dz < 0 {
		stepJ = -1
	}
	inf := float32(math.MaxFloat32)
	tMaxX, tMaxZ, tDeltaX, tDeltaZ := inf, inf, inf, inf
	if dx != 0 {
		tDeltaX = abs32(1 / dx)
		next := float32(ci)
		if stepI > 0 {
			next++
		}
		tMaxX = (next - x0) / dx
	}
	if dz != 0 {
		tDeltaZ = abs32(1 / dz)
		next := float32(cj)
		if stepJ > 0 {
			next++
		}
		tMaxZ = (next - z0) / dz
	}
	cur := a
	limit := absInt(ti-ci) + absInt(tj-cj) + 2
	for guard := 0; guard < limit && (ci != ti || cj != tj); guard++ {
		var dir int
		switch {
		case abs32(tMaxX-tMaxZ) < 1e-5:
			// Through a corner exactly: only the diagonal, and only where
			// both of its orthogonals are there too.
			dir = diagonalDir(stepI, stepJ)
			ci += stepI
			cj += stepJ
			tMaxX += tDeltaX
			tMaxZ += tDeltaZ
		case tMaxX < tMaxZ:
			dir = orthoDir(stepI, 0)
			ci += stepI
			tMaxX += tDeltaX
		default:
			dir = orthoDir(0, stepJ)
			cj += stepJ
			tMaxZ += tDeltaZ
		}
		next := gr.Nodes[cur].Links[dir]
		if next < 0 {
			return false
		}
		cur = next
	}
	return cur == b
}

func orthoDir(di, dj int) int {
	switch {
	case di > 0:
		return 0
	case dj > 0:
		return 1
	case di < 0:
		return 2
	}
	return 3
}

func diagonalDir(di, dj int) int {
	switch {
	case di > 0 && dj > 0:
		return 4
	case di < 0 && dj > 0:
		return 5
	case di < 0 && dj < 0:
		return 6
	}
	return 7
}

// RandomPoint picks a node at random among those whose feet point passes
// accept, inside the world XZ box [lo, hi], in the given region (or any, for
// region < 0). ok is false when none qualifies.
//
// One pass, reservoir-sampled, so a large box costs its columns and nothing
// more.
func (gr *Grid) RandomPoint(lo, hi mgl32.Vec3, region int32, accept func(mgl32.Vec3) bool, rng *rand.Rand) (mgl32.Vec3, bool) {
	if gr == nil || len(gr.Nodes) == 0 {
		return mgl32.Vec3{}, false
	}
	hf := gr.HF
	i0, j0 := hf.cellIndex(lo.X(), lo.Z())
	i1, j1 := hf.cellIndex(hi.X(), hi.Z())
	var pick mgl32.Vec3
	seen := 0
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			c := i + j*hf.W
			for n := gr.colNodes[c]; n < gr.colNodes[c+1]; n++ {
				nd := &gr.Nodes[n]
				if region >= 0 && nd.Region != region {
					continue
				}
				if nd.Floor < lo.Y()-gr.Params.StepHeight || nd.Floor > hi.Y() {
					continue
				}
				p := gr.NodePos(n)
				if accept != nil && !accept(p) {
					continue
				}
				seen++
				if rng == nil {
					if seen == 1 {
						pick = p
					}
					continue
				}
				if rng.Intn(seen) == 0 {
					pick = p
				}
			}
		}
	}
	return pick, seen > 0
}

// RegionAt is the region of the node a point stands on, or -1.
func (gr *Grid) RegionAt(p mgl32.Vec3) int32 {
	n, ok := gr.Locate(p, 2)
	if !ok {
		return -1
	}
	return gr.Nodes[n].Region
}

func sq(v float32) float32 { return v * v }

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// SamplePoints picks up to k standable points at random from those whose feet
// point passes accept, inside the world XZ box [lo, hi] and in the given region
// (or any, for region < 0). Every qualifying point is equally likely to be
// among them, and the box's columns are walked once — what a search that has
// to test each candidate expensively, as a cover search does with rays, wants
// to bound its work by.
func (gr *Grid) SamplePoints(lo, hi mgl32.Vec3, region int32, accept func(mgl32.Vec3) bool, k int, rng *rand.Rand) []mgl32.Vec3 {
	if gr == nil || len(gr.Nodes) == 0 || k <= 0 {
		return nil
	}
	hf := gr.HF
	i0, j0 := hf.cellIndex(lo.X(), lo.Z())
	i1, j1 := hf.cellIndex(hi.X(), hi.Z())
	out := make([]mgl32.Vec3, 0, k)
	seen := 0
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			c := i + j*hf.W
			for n := gr.colNodes[c]; n < gr.colNodes[c+1]; n++ {
				nd := &gr.Nodes[n]
				if region >= 0 && nd.Region != region {
					continue
				}
				if nd.Floor < lo.Y()-gr.Params.StepHeight || nd.Floor > hi.Y() {
					continue
				}
				p := gr.NodePos(n)
				if accept != nil && !accept(p) {
					continue
				}
				seen++
				switch {
				case len(out) < k:
					out = append(out, p)
				case rng != nil:
					if r := rng.Intn(seen); r < k {
						out[r] = p
					}
				}
			}
		}
	}
	return out
}
