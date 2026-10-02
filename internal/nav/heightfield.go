package nav

// heightfield.go — where the level is solid, one column at a time.
//
// Navigation needs a question answered that collision never asks: not "is this
// body inside anything" but "where could a body stand, and where could it go
// from there". Brushes are convex solids, which makes the first half a
// rasterisation problem with an exact answer: push a vertical column through
// the level, and every brush it passes through is one closed span of solid Y.
// The top of a span is a floor; the bottom of the next span up is the ceiling
// over it.
//
// This is Recast's heightfield, and it is built the same way for the same
// reason: it is agnostic to how the geometry was authored. A floor is a floor
// whether it is a hollow room's slab, a stair's tread or the flat top of a
// wall, and nobody has to tag any of them as walkable.
//
// # A column is a square, not a line
//
// Sampling one line at the cell's centre misses anything thinner than a cell —
// a railing, a partition — and a path through a wall the grid could not see is
// a guard walking face-first into it forever. So each face is clipped to the
// cell's whole square instead, and the span is the Y range of what survives.
// The extreme Y of a convex solid inside a square prism is at a vertex of the
// intersection, and every such vertex is a vertex of some clipped face, so
// taking min and max over the clipped faces is exact rather than approximate.
// The cost is that a cell a wall only grazes counts as wall; erosion by the
// agent's radius already wants that margin, so it is the right way to be wrong.
//
// # Profile-free
//
// Nothing here knows how big the agent is. Clearance, step height and radius
// are grid.go's business, applied on top, so one heightfield serves every size
// of agent a level has.

import (
	"math"
	"sort"

	"foundry/internal/meshcollide"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// DefaultCellSize is the column width the grid is built at when a level is
// small enough to afford it. A player-sized body is 0.6 across, so this is two
// and a half columns of it — enough for a doorway to read as a doorway.
const DefaultCellSize = float32(0.25)

// maxColumns caps how many columns a heightfield may have. A level larger than
// this at DefaultCellSize is built at a coarser cell instead: navigation that is
// slightly less precise over a huge map is better than a load that allocates
// gigabytes. 512 m square at DefaultCellSize; the cell doubles past that, so
// the tile cache (tiles.go) keeps working as a level grows.
const maxColumns = 1 << 22

// edgeEps is how far, as a fraction of a cell, a column's square is shrunk
// before faces are clipped to it. See buildHeightfield.
const edgeEps = float32(0.01)

// span is one run of solid in a column.
type span struct {
	min, max float32
	// walkable says the surface at max — the one a body standing on this span
	// would stand on — is shallow enough to stand on.
	walkable bool
}

// Heightfield is the level's solid, column by column.
type Heightfield struct {
	// Origin is the world XZ of the (0, 0) column's minimum corner.
	Origin mgl32.Vec2
	Cell   float32
	W, D   int
	// colStart indexes spans: column c's spans are spans[colStart[c]:colStart[c+1]],
	// sorted upward and never overlapping.
	colStart []int32
	spans    []span
	// MaxSlopeCos is the walkable test the spans were classified with.
	MaxSlopeCos float32
}

// Columns reports how many columns the heightfield has.
func (h *Heightfield) Columns() int { return h.W * h.D }

// column returns the spans of column c.
func (h *Heightfield) column(c int) []span {
	return h.spans[h.colStart[c]:h.colStart[c+1]]
}

// ColumnAt returns the column holding the world XZ point, or -1 outside the grid.
func (h *Heightfield) ColumnAt(x, z float32) int {
	i := int(math.Floor(float64((x - h.Origin.X()) / h.Cell)))
	j := int(math.Floor(float64((z - h.Origin.Y()) / h.Cell)))
	if i < 0 || j < 0 || i >= h.W || j >= h.D {
		return -1
	}
	return i + j*h.W
}

// ColumnCenter is the world XZ at the middle of column c.
func (h *Heightfield) ColumnCenter(c int) (x, z float32) {
	i, j := c%h.W, c/h.W
	return h.Origin.X() + (float32(i)+0.5)*h.Cell, h.Origin.Y() + (float32(j)+0.5)*h.Cell
}

// solid is one brush, carried into world space.
type solid struct {
	faces   [][]mgl32.Vec3
	normals []mgl32.Vec3
	min     mgl32.Vec3
	max     mgl32.Vec3
	// hash identifies the solid's world-space shape, for the tile cache.
	hash uint64
	// closed marks a watertight mesh (a cooked Nanite mesh in the mesh mode
	// whose collision cut has no open edge): one solid for the whole mesh,
	// and its spans are found by parity rather than by min and max, because
	// a closed mesh need not be convex — see paritySpans.
	closed bool
}

// Solids gathers the level geometry navigation is built against: every solid,
// static brush of every enabled chunk that lives in world space.
//
// Four kinds are left out, each on purpose (see navigable for the chunk half). A dynamic brush moves, and a door
// baked shut would be a wall for the life of the level — collision still stops
// an agent at one, and the controller re-paths when it does. A frame-owned
// chunk whose frame is moving — a ship in flight — is somewhere else next
// frame; parked, it is baked through its frame like any other chunk. A moving
// chunk will not be where it was baked. And a disabled chunk is not there,
// which is exactly what streaming one out means.
//
// The result is a set of fresh world-space copies sharing nothing with the
// level, which is what lets SyncAsync hand it to a goroutine.
func gatherSolids(lvl *world.Level) []solid {
	var out []solid
	if lvl == nil {
		return nil
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !navigable(lvl, ch) {
			continue
		}
		m := ch.ModelMatrix()
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if !b.Solid || b.Dynamic || len(b.Verts) < 4 {
				continue
			}
			s := solid{}
			first := true
			for fi := range b.Faces {
				f := &b.Faces[fi]
				if len(f.Indices) < 3 {
					continue
				}
				pts := make([]mgl32.Vec3, 0, len(f.Indices))
				for _, idx := range f.Indices {
					if idx < 0 || idx >= len(b.Verts) {
						continue
					}
					p := b.Verts[idx]
					r := m.Mul4x1(p.Vec4(1))
					wp := mgl32.Vec3{r.X(), r.Y(), r.Z()}
					pts = append(pts, wp)
					if first {
						s.min, s.max, first = wp, wp, false
					} else {
						s.min = mgl32.Vec3{min32(s.min.X(), wp.X()), min32(s.min.Y(), wp.Y()), min32(s.min.Z(), wp.Z())}
						s.max = mgl32.Vec3{max32(s.max.X(), wp.X()), max32(s.max.Y(), wp.Y()), max32(s.max.Z(), wp.Z())}
					}
				}
				if len(pts) < 3 {
					continue
				}
				s.faces = append(s.faces, pts)
				s.normals = append(s.normals, newellNormal(pts))
			}
			if len(s.faces) > 0 {
				s.hash = hashSolid(&s)
				out = append(out, s)
			}
		}
		if ch.Terrain != nil {
			out = appendTerrainSolids(out, ch.Terrain, m)
		}
	}
	return appendScenerySolids(out, lvl)
}

// appendScenerySolids adds the solid props that collide as themselves — a
// cooked Nanite mesh in the hulls or mesh mode (docs/nanite/PHASE_2b.md) —
// in world space, so an agent is routed round a rock and up a cliff by the
// same shape collision stops it at.
//
// A hull is a convex solid, which is exactly what a brush is to rasterise:
// one solid, its faces the hull's triangles. The mesh mode is two cases. A
// watertight mesh is one solid whose spans are found by parity
// (paritySpans), because a brush's lowest-to-highest span would fill in the
// space under an overhang. An open mesh is a sheet with no inside, and
// becomes one solid per triangle — terrain's reason for one per run — each
// floor from above.
//
// Box-mode props stay out, as scenery always has: an agent meets a crate at
// collision and re-paths.
func appendScenerySolids(out []solid, lvl *world.Level) []solid {
	for si := range lvl.Scenery {
		sc := &lvl.Scenery[si]
		mode := sc.CollisionMode()
		if mode != meshcollide.ModeHulls && mode != meshcollide.ModeMesh {
			continue
		}
		d := sc.CollisionData()
		m := sc.ModelMatrix()
		toWorld := func(p mgl32.Vec3) mgl32.Vec3 {
			r := m.Mul4x1(p.Vec4(1))
			return mgl32.Vec3{r.X(), r.Y(), r.Z()}
		}
		add := func(s *solid, pts []mgl32.Vec3, up bool) {
			n := newellNormal(pts)
			if up && n.Y() < 0 {
				n = n.Mul(-1)
			}
			// s.min/s.max start at one of the solid's own points.
			for _, p := range pts {
				s.min = mgl32.Vec3{min32(s.min.X(), p.X()), min32(s.min.Y(), p.Y()), min32(s.min.Z(), p.Z())}
				s.max = mgl32.Vec3{max32(s.max.X(), p.X()), max32(s.max.Y(), p.Y()), max32(s.max.Z(), p.Z())}
			}
			s.faces = append(s.faces, pts)
			s.normals = append(s.normals, n)
		}
		// A mirroring placement turns the winding inside out.
		flip := m.Mat3().Det() < 0
		tri := func(a, b, c mgl32.Vec3) []mgl32.Vec3 {
			if flip {
				return []mgl32.Vec3{toWorld(a), toWorld(c), toWorld(b)}
			}
			return []mgl32.Vec3{toWorld(a), toWorld(b), toWorld(c)}
		}
		if mode == meshcollide.ModeHulls {
			for hi := range d.Hulls {
				h := &d.Hulls[hi]
				s := solid{min: toWorld(h.Pts[0]), max: toWorld(h.Pts[0])}
				for k := 0; k+2 < len(h.Tris); k += 3 {
					add(&s, tri(h.Pts[h.Tris[k]], h.Pts[h.Tris[k+1]], h.Pts[h.Tris[k+2]]), false)
				}
				s.hash = hashSolid(&s)
				out = append(out, s)
			}
			continue
		}
		if d.OpenEdges == 0 && d.NonManifold == 0 {
			// Watertight: one solid, its spans by parity.
			first := toWorld(d.Dequantize(d.Tris[0]))
			s := solid{min: first, max: first, closed: true}
			for t := 0; t < d.TriangleCount(); t++ {
				ix := d.Tris[3*t : 3*t+3]
				add(&s, tri(d.Dequantize(ix[0]), d.Dequantize(ix[1]), d.Dequantize(ix[2])), false)
			}
			s.hash = hashSolid(&s)
			out = append(out, s)
			continue
		}
		for t := 0; t < d.TriangleCount(); t++ {
			ix := d.Tris[3*t : 3*t+3]
			pts := tri(d.Dequantize(ix[0]), d.Dequantize(ix[1]), d.Dequantize(ix[2]))
			s := solid{min: pts[0], max: pts[0]}
			add(&s, pts, true)
			s.hash = hashSolid(&s)
			out = append(out, s)
		}
	}
	return out
}

// appendTerrainSolids adds a chunk's terrain as solids, one per vertical run
// of solid blocks in a column. rasterise reads a solid as the span from the
// lowest to the highest point of its faces over each nav column, so a run
// needs nothing but its own faces: every run has a face on top (air above it)
// and one underneath (air, or the grid's bottom, below it), and its sides.
// One solid per run rather than per patch of ground is what keeps a cave
// open: a patch holding a cave's floor and its roof would span both and fill
// the cave in.
//
// A run's faces are whichever surface the terrain draws — the blocks, or the
// smooth surface's quads that cross the same block faces — so agents walk the
// ramps a player sees, not the steps under them.
func appendTerrainSolids(out []solid, t *world.Terrain, m mgl32.Mat4) []solid {
	toWorld := func(p mgl32.Vec3) mgl32.Vec3 {
		r := m.Mul4x1(p.Vec4(1))
		return mgl32.Vec3{r.X(), r.Y(), r.Z()}
	}
	// runOf names the run a block belongs to by the block at its foot.
	runOf := func(x, y, z int) int32 {
		for y > 0 && t.Solid(x, y-1, z) {
			y--
		}
		return int32(t.Index(x, y, z))
	}
	type runSolid struct {
		s     solid
		first bool
	}
	runs := map[int32]*runSolid{}
	var order []int32
	for _, sec := range t.Sections() {
		for fi := range sec.Faces {
			f := &sec.Faces[fi]
			x, y, z, _ := t.FaceBlock(f.Key)
			id := runOf(x, y, z)
			r := runs[id]
			if r == nil {
				r = &runSolid{first: true}
				runs[id] = r
				order = append(order, id)
			}
			for k := 0; k < 2; k++ {
				tri := f.Triangle(k)
				pts := []mgl32.Vec3{toWorld(tri[0]), toWorld(tri[1]), toWorld(tri[2])}
				for _, p := range pts {
					if r.first {
						r.s.min, r.s.max, r.first = p, p, false
					} else {
						r.s.min = mgl32.Vec3{min32(r.s.min.X(), p.X()), min32(r.s.min.Y(), p.Y()), min32(r.s.min.Z(), p.Z())}
						r.s.max = mgl32.Vec3{max32(r.s.max.X(), p.X()), max32(r.s.max.Y(), p.Y()), max32(r.s.max.Z(), p.Z())}
					}
				}
				r.s.faces = append(r.s.faces, pts)
				r.s.normals = append(r.s.normals, newellNormal(pts))
			}
		}
	}
	for _, id := range order {
		r := runs[id]
		r.s.hash = hashSolid(&r.s)
		out = append(out, r.s)
	}
	return out
}

// newellNormal is a polygon's unit normal by Newell's method, which stays
// sensible for a face that is not quite planar — and a bent brush face is a
// thing this engine allows.
func newellNormal(pts []mgl32.Vec3) mgl32.Vec3 {
	var n mgl32.Vec3
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		n[0] += (a.Y() - b.Y()) * (a.Z() + b.Z())
		n[1] += (a.Z() - b.Z()) * (a.X() + b.X())
		n[2] += (a.X() - b.X()) * (a.Y() + b.Y())
	}
	if l := n.Len(); l > 1e-9 {
		return n.Mul(1 / l)
	}
	return mgl32.Vec3{}
}

// rasterise builds the heightfield of solids over the w × d columns of cell
// whose (0, 0) corner is at origin. A solid reaching outside that area is
// clipped to it: its footprints clamp to the edge columns, and faces clipped
// against those columns' real squares leave nothing that is not inside them.
func rasterise(solids []solid, origin mgl32.Vec2, cell float32, w, d int, maxSlopeCos float32) *Heightfield {
	h := &Heightfield{Origin: origin, Cell: cell, W: w, D: d, MaxSlopeCos: maxSlopeCos}

	// Spans are collected as a linked list per column while solids are
	// rasterised, then packed once at the end. A dense slice-per-column would
	// be a slice header for every column of a large map whether or not any
	// solid reaches it.
	cols := h.W * h.D
	head := make([]int32, cols)
	for i := range head {
		head[i] = -1
	}
	type rec struct {
		span
		next int32
	}
	// Every solid adds at most one span per column of its footprint, so the
	// footprints bound the record count and the list never has to grow.
	capacity := 0
	for si := range solids {
		i0, j0 := h.cellIndex(solids[si].min.X(), solids[si].min.Z())
		i1, j1 := h.cellIndex(solids[si].max.X(), solids[si].max.Z())
		capacity += (i1 - i0 + 1) * (j1 - j0 + 1)
	}
	recs := make([]rec, 0, capacity)

	// Per-solid scratch covering the solid's own footprint.
	var accMin, accMax, accNY []float32
	var closedHits [][]faceRange
	var clipA, clipB []mgl32.Vec3
	var edgeBuf []edge2D

	for si := range solids {
		s := &solids[si]
		i0, j0 := h.cellIndex(s.min.X(), s.min.Z())
		i1, j1 := h.cellIndex(s.max.X(), s.max.Z())
		fw, fd := i1-i0+1, j1-j0+1
		n := fw * fd
		if cap(accMin) < n {
			accMin, accMax, accNY = make([]float32, n), make([]float32, n), make([]float32, n)
		}
		accMin, accMax, accNY = accMin[:n], accMax[:n], accNY[:n]
		for k := 0; k < n; k++ {
			accMin[k], accMax[k], accNY[k] = float32(math.Inf(1)), float32(math.Inf(-1)), -2
		}
		if s.closed {
			if cap(closedHits) < n {
				closedHits = make([][]faceRange, n)
			}
			closedHits = closedHits[:n]
		}

		for fi, face := range s.faces {
			n := s.normals[fi]
			ny := n.Y()
			flo, fhi := face[0], face[0]
			for _, p := range face[1:] {
				flo = mgl32.Vec3{min32(flo.X(), p.X()), 0, min32(flo.Z(), p.Z())}
				fhi = mgl32.Vec3{max32(fhi.X(), p.X()), 0, max32(fhi.Z(), p.Z())}
			}
			// A face that is not upright has a height at every XZ inside it, so
			// a column wholly inside its outline needs no clipping: the plane
			// at the column's four corners is the whole answer. That is nearly
			// every column of a floor, which is nearly every column there is.
			flat := abs32(ny) > 1e-3
			planeD := n.Dot(face[0])
			edges := edges2D(face, edgeBuf[:0])
			edgeBuf = edges
			fi0, fj0 := h.cellIndex(flo.X(), flo.Z())
			fi1, fj1 := h.cellIndex(fhi.X(), fhi.Z())
			for j := fj0; j <= fj1; j++ {
				z0 := h.Origin.Y() + float32(j)*cell
				for i := fi0; i <= fi1; i++ {
					x0 := h.Origin.X() + float32(i)*cell
					// The square is shrunk a hair so a face lying exactly on a
					// cell boundary belongs to the cell it bounds and not to
					// its neighbour too. Levels are built on a grid, and so is
					// this: without it every wall aligned to a column edge
					// would claim a column of the room beside it, and a
					// one-metre doorway would close.
					ax, az := x0+edgeEps*cell, z0+edgeEps*cell
					bx, bz := x0+cell-edgeEps*cell, z0+cell-edgeEps*cell
					k := (i - i0) + (j-j0)*fw
					// A closed solid keeps each face's own range in the
					// column, for the parity walk below; anything else
					// folds it into the column's one span.
					fy := faceRange{lo: float32(math.Inf(1)), hi: float32(math.Inf(-1)), ny: ny}
					put := func(y float32) {
						if s.closed {
							fy.lo, fy.hi = min32(fy.lo, y), max32(fy.hi, y)
						} else {
							accumulate(accMin, accMax, accNY, k, y, ny)
						}
					}
					if flat && insideEdges(edges, ax, az) && insideEdges(edges, bx, az) &&
						insideEdges(edges, ax, bz) && insideEdges(edges, bx, bz) {
						for _, c := range [4][2]float32{{ax, az}, {bx, az}, {ax, bz}, {bx, bz}} {
							put((planeD - n.X()*c[0] - n.Z()*c[1]) / ny)
						}
					} else {
						clipA = clipPolyToRect(face, ax, az, bx, bz, clipA[:0], &clipB)
						for _, p := range clipA {
							put(p.Y())
						}
					}
					if s.closed && fy.lo <= fy.hi {
						closedHits[k] = append(closedHits[k], fy)
					}
				}
			}
		}

		if s.closed {
			for j := 0; j < fd; j++ {
				for i := 0; i < fw; i++ {
					k := i + j*fw
					if len(closedHits[k]) == 0 {
						continue
					}
					c := (i + i0) + (j+j0)*h.W
					for _, sp := range paritySpans(closedHits[k], maxSlopeCos) {
						recs = append(recs, rec{span: sp, next: head[c]})
						head[c] = int32(len(recs) - 1)
					}
					closedHits[k] = closedHits[k][:0]
				}
			}
			continue
		}

		for j := 0; j < fd; j++ {
			for i := 0; i < fw; i++ {
				k := i + j*fw
				if accMin[k] > accMax[k] {
					continue
				}
				c := (i + i0) + (j+j0)*h.W
				recs = append(recs, rec{
					span: span{min: accMin[k], max: accMax[k], walkable: accNY[k] >= maxSlopeCos},
					next: head[c],
				})
				head[c] = int32(len(recs) - 1)
			}
		}
	}

	// Pack, sort and merge. Touching spans merge — a wall standing on a floor
	// slab is one solid from the slab's underside to the wall's top — because
	// the sliver of gap float error leaves between them is not somewhere a body
	// could stand.
	const mergeEps = 0.01
	h.colStart = make([]int32, cols+1)
	h.spans = make([]span, 0, len(recs))
	var col []span
	for c := 0; c < cols; c++ {
		h.colStart[c] = int32(len(h.spans))
		col = col[:0]
		for r := head[c]; r >= 0; r = recs[r].next {
			col = append(col, recs[r].span)
		}
		if len(col) == 0 {
			continue
		}
		sort.Slice(col, func(a, b int) bool { return col[a].min < col[b].min })
		cur := col[0]
		for _, sp := range col[1:] {
			if sp.min <= cur.max+mergeEps {
				if sp.max > cur.max+1e-4 {
					cur.max, cur.walkable = sp.max, sp.walkable
				} else if sp.max > cur.max-1e-4 {
					cur.walkable = cur.walkable || sp.walkable
				}
				continue
			}
			h.spans = append(h.spans, cur)
			cur = sp
		}
		h.spans = append(h.spans, cur)
	}
	h.colStart[cols] = int32(len(h.spans))
	return h
}

// faceRange is one face of a closed solid over one column: the Y range it
// covers there, and which way it faces.
type faceRange struct {
	lo, hi, ny float32
}

// paritySpans is a closed solid's spans over one column, from the faces that
// cross it. A brush is convex, so its one span is from its lowest face to its
// highest; a closed mesh is not — a mesa with a cave, a rock arch — so the
// column is walked upward instead: a face looking down is where the solid
// starts, a face looking up is where it ends, and the top of each run is
// floor if its face is shallow enough to stand on. Neighbouring faces of one
// surface (several triangles of a floor in one column) are taken as one
// crossing, and a steep face — a cliff within the column — is a span of its
// own, standing in whatever it is part of.
func paritySpans(hits []faceRange, maxSlopeCos float32) []span {
	const steep = 0.2
	const join = 0.05
	var out []span
	var level []faceRange
	for _, h := range hits {
		if abs32(h.ny) < steep {
			out = append(out, span{min: h.lo, max: h.hi})
			continue
		}
		level = append(level, h)
	}
	sort.Slice(level, func(a, b int) bool { return level[a].lo+level[a].hi < level[b].lo+level[b].hi })
	// One crossing per surface: same facing, touching ranges.
	var cross []faceRange
	for _, h := range level {
		if n := len(cross); n > 0 && (cross[n-1].ny > 0) == (h.ny > 0) && h.lo <= cross[n-1].hi+join {
			c := &cross[n-1]
			c.lo, c.hi = min32(c.lo, h.lo), max32(c.hi, h.hi)
			if h.ny > 0 {
				c.ny = min32(c.ny, h.ny) // the steeper of a surface's faces decides
			}
			continue
		}
		cross = append(cross, h)
	}
	depth := 0
	var start float32
	for _, c := range cross {
		if c.ny < 0 {
			if depth == 0 {
				start = c.lo
			}
			depth++
			continue
		}
		if depth == 0 {
			// A top with no bottom under it in this column: a sheet.
			out = append(out, span{min: c.lo, max: c.hi, walkable: c.ny >= maxSlopeCos})
			continue
		}
		depth--
		if depth == 0 {
			out = append(out, span{min: start, max: c.hi, walkable: c.ny >= maxSlopeCos})
		}
	}
	if depth > 0 {
		// A bottom with no top over it: solid to the highest thing here.
		top := start
		for _, h := range hits {
			top = max32(top, h.hi)
		}
		out = append(out, span{min: start, max: top})
	}
	return out
}

// accumulate folds one point of a face clipped to column k into that column's
// running span.
func accumulate(accMin, accMax, accNY []float32, k int, y, ny float32) {
	if y < accMin[k] {
		accMin[k] = y
	}
	// A top reached by two faces at once — a slab's top meeting the rim of its
	// own wall face — belongs to the flatter of the two: the corner of a floor
	// is still floor.
	if y > accMax[k]+1e-4 || (y > accMax[k]-1e-4 && ny > accNY[k]) {
		if y > accMax[k] {
			accMax[k] = y
		}
		accNY[k] = ny
	}
}

// edge2D is one side of a face's outline seen from above, as the line
// a·x + b·z <= c that the inside of the outline satisfies.
type edge2D struct{ a, b, c float32 }

// edges2D is a convex face's outline from above, whichever way it winds. An
// upright face has no inside from above, and gets no edges — insideEdges is
// never asked about one.
func edges2D(face []mgl32.Vec3, out []edge2D) []edge2D {
	// Signed area decides which side of each edge is inside.
	area := float32(0)
	for i := range face {
		p, q := face[i], face[(i+1)%len(face)]
		area += p.X()*q.Z() - q.X()*p.Z()
	}
	sign := float32(1)
	if area < 0 {
		sign = -1
	}
	for i := range face {
		p, q := face[i], face[(i+1)%len(face)]
		// Inside is to the left of p→q for a positive area.
		a := -(q.Z() - p.Z()) * sign
		b := (q.X() - p.X()) * sign
		out = append(out, edge2D{a: -a, b: -b, c: -(a*p.X() + b*p.Z())})
	}
	return out
}

// insideEdges reports whether an XZ point is inside (or on) an outline.
func insideEdges(edges []edge2D, x, z float32) bool {
	for _, e := range edges {
		if e.a*x+e.b*z > e.c+1e-5 {
			return false
		}
	}
	return len(edges) > 0
}

// cellIndex returns the column coordinates holding a world XZ point, clamped to
// the grid.
func (h *Heightfield) cellIndex(x, z float32) (int, int) {
	i := int(math.Floor(float64((x - h.Origin.X()) / h.Cell)))
	j := int(math.Floor(float64((z - h.Origin.Y()) / h.Cell)))
	if i < 0 {
		i = 0
	}
	if j < 0 {
		j = 0
	}
	if i >= h.W {
		i = h.W - 1
	}
	if j >= h.D {
		j = h.D - 1
	}
	return i, j
}

// clipPolyToRect clips a polygon to an axis-aligned XZ rectangle, carrying Y
// along each cut edge — Sutherland–Hodgman against four half-planes. out is
// reused; scratch is the ping-pong buffer.
//
// A vertical face projects to a line and so has no area in XZ, but it still
// clips correctly: the points that survive are the stretch of the wall inside
// the square, which is what tells the column the wall is there.
func clipPolyToRect(poly []mgl32.Vec3, x0, z0, x1, z1 float32, out []mgl32.Vec3, scratch *[]mgl32.Vec3) []mgl32.Vec3 {
	// Quick accept: every vertex inside.
	inside := true
	for _, p := range poly {
		if p.X() < x0 || p.X() > x1 || p.Z() < z0 || p.Z() > z1 {
			inside = false
			break
		}
	}
	if inside {
		return append(out, poly...)
	}
	cur := append((*scratch)[:0], poly...)
	next := out
	type plane struct {
		axis  int
		bound float32
		keepG bool // keep points >= bound
	}
	planes := [4]plane{{0, x0, true}, {0, x1, false}, {2, z0, true}, {2, z1, false}}
	for _, pl := range planes {
		next = next[:0]
		n := len(cur)
		if n == 0 {
			break
		}
		in := func(p mgl32.Vec3) bool {
			if pl.keepG {
				return p[pl.axis] >= pl.bound
			}
			return p[pl.axis] <= pl.bound
		}
		for i := 0; i < n; i++ {
			a, b := cur[i], cur[(i+1)%n]
			ain, bin := in(a), in(b)
			if ain {
				next = append(next, a)
			}
			if ain != bin {
				d := b[pl.axis] - a[pl.axis]
				if d != 0 {
					t := (pl.bound - a[pl.axis]) / d
					next = append(next, a.Add(b.Sub(a).Mul(t)))
				}
			}
		}
		cur, next = next, cur
	}
	*scratch = next[:0]
	return cur
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
