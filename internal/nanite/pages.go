package nanite

// pages.go — a cooked mesh's geometry in fixed-size PAGES, the unit the
// renderer streams (render/nanite_stream.go). Phase 3 of the Nanite plan
// (docs/nanite/PHASE_3.md), components 3 and 9.
//
// # What a page holds
//
// A page is a run of the mesh's clusters, in cluster order, whose decoded
// vertices and triangles fit one slot of the GPU's page pool: at most
// PageMaxVertices vertices and PageMaxTriangles triangles. A cluster's
// vertices are its own (the distinct pool vertices its triangles use, at most
// 128), packed relative to its own lowest corner on the mesh's quantisation
// grid (quant.go); its triangles are local indices at the bits its vertex
// count needs. Decoded, a page is one vertex array and one index list over
// it, every cluster's triangles where cluster order puts them — so a run of
// a page's clusters is one contiguous range of indices, which is what the
// cull draws as one command.
//
// # Which clusters go together
//
// A DAG group's refinement — drawing the clusters it consumed instead of the
// ones it made (internal/cluster's dag.go) — needs every cluster it consumed
// at once, so a group's input wants to be on one page; and the renderer draws
// a DAG as RANGE WINDOWS of a few leaves of its LOD tree each
// (cluster.RangeWindows), every window a few command slots that cost whether
// they draw or not, and a window cannot straddle two pages, which need not
// sit side by side in the pool. The DAG's layout serves both: it puts a
// group's input together (by level, then by consuming group), and leaves
// follow it. So pages are packed from the renderer's own windows, in cluster
// order, whole, a new page started whenever the next would not fit: no
// window is ever split, and a group's input is split only where a page ends
// in the middle of it — then a refinement needs both pages, which the
// residency knows (Residency.PagesNeededWith). Measured on the bench rock,
// packing by groups instead left the renderer 187 windows where 167 do
// (docs/nanite/PHASE_3.md).
//
// The clusters no group consumed — the ROOTS, the coarsest version of every
// part of the mesh — are packed into pages of their own (Page.Root): their
// windows never take another leaf. Root pages are always resident: with
// nothing else loaded, the roots alone are a complete, crack-free mesh.
//
// # What a page depends on
//
// A page's clusters are drawn only when the groups that consumed them are
// refined, and a group may only be refined when every coarser group its
// output went to is too — otherwise the coarse and the fine version of one
// surface would both be drawn. So a page is of use only while the pages
// holding what its groups MADE are resident: those are its Parents. The
// runtime's residency (residency.go) works this out exactly per group; the
// page-level Parents are what keeps an eviction from starting at the top.

import (
	"encoding/binary"
	"fmt"
	"slices"

	"foundry/internal/cluster"
)

// The page limits: one slot of the GPU's page pool. Fixed for every mesh,
// since the pool's slots are.
const (
	PageMaxVertices  = 4096
	PageMaxTriangles = 4096
)

// Page is one page of a mesh.
type Page struct {
	// Offset and Size are the page's packed bytes in the file's data
	// section.
	Offset uint64
	Size   uint32
	// ClusterFirst and ClusterCount are its clusters, in Mesh.Clusters.
	ClusterFirst, ClusterCount int32
	// Vertices and Triangles are its decoded size.
	Vertices, Triangles int32
	// Level is the finest DAG level among its clusters.
	Level int32
	// Root pages hold only clusters no group consumed: always resident.
	Root bool
	// Parents are the pages that hold what its clusters' consuming groups
	// made: the pages it is of no use without (see the file comment).
	Parents []int32
}

// PageData is a page decoded: 5 floats a vertex (x, y, z, u, v), 3 normal
// floats a vertex, and the triangles as indices into this page's vertices.
type PageData struct {
	Vertices []float32
	Normals  []float32
	Indices  []uint32
}

// pageMagic starts every page's bytes.
const pageMagic = 0x4547504e // "NPGE"

// PageOf is the page holding cluster c (binary search over Pages).
func (m *Mesh) PageOf(c int32) int32 {
	i, _ := slices.BinarySearchFunc(m.Pages, c, func(p Page, c int32) int {
		switch {
		case c < p.ClusterFirst:
			return 1
		case c >= p.ClusterFirst+p.ClusterCount:
			return -1
		}
		return 0
	})
	return int32(i)
}

// slotOf is each cluster's material slot.
func (m *Mesh) slotOf() []int32 {
	out := make([]int32, len(m.Clusters))
	for si, s := range m.Slots {
		for c := s.First; c < s.First+s.Count; c++ {
			out[c] = int32(si)
		}
	}
	return out
}

// groupKey names a DAG group across the mesh: a slot's DAG numbers its own.
type groupKey struct{ slot, group int32 }

// BuildPages packs the mesh's clusters into pages (see the file comment).
// The mesh must be whole in memory — its pool snapped to its quantisation
// (Quant.Quantize), q the pool's integers — with its clusters and tree
// built. Afterwards the mesh's Vertices, Normals and Indices are the decoded
// pages, exactly what a load decodes, and the packed bytes are kept for
// Encode.
func (m *Mesh) BuildPages(q []QVertex) error {
	if len(q) != len(m.Vertices)/5 {
		return fmt.Errorf("pages: %d quantised vertices for a %d-vertex pool", len(q), len(m.Vertices)/5)
	}
	slot := m.slotOf()
	leaves := cluster.LeafRanges(m.Nodes)
	if len(leaves) == 0 && len(m.Clusters) > 0 {
		return fmt.Errorf("pages: no LOD tree to page by")
	}
	// Each cluster's distinct vertices.
	local := make([][]uint32, len(m.Clusters))
	for ci := range m.Clusters {
		c := &m.Clusters[ci]
		seen := map[uint32]bool{}
		for _, v := range m.Indices[3*c.TriFirst : 3*(c.TriFirst+c.TriCount)] {
			if !seen[v] {
				seen[v] = true
				local[ci] = append(local[ci], v)
			}
		}
	}
	// The windows the renderer will draw (cluster.RangeWindows, per slot as
	// it cuts them), never mixing root leaves with others: the unit pages
	// are packed from, so no window straddles two pages.
	type window struct {
		first, count int32
		root         bool
		verts, tris  int
	}
	var windows []window
	li := 0
	for si := range m.Slots {
		sl := &m.Slots[si]
		var in [][2]int32
		for ; li < len(leaves) && leaves[li][0] < sl.First+sl.Count; li++ {
			if leaves[li][0] >= sl.First {
				in = append(in, leaves[li])
			}
		}
		isRoot := func(l [2]int32) bool { return m.Clusters[l[0]].IsRoot() }
		for _, w := range cluster.RangeWindows(in, func(a, b int) bool { return isRoot(in[a]) != isRoot(in[b]) }) {
			win := window{first: w[0], count: w[1], root: m.Clusters[w[0]].IsRoot()}
			for c := w[0]; c < w[0]+w[1]; c++ {
				win.verts += len(local[c])
				win.tris += int(m.Clusters[c].TriCount)
			}
			if win.verts > PageMaxVertices || win.tris > PageMaxTriangles {
				return fmt.Errorf("pages: a window of %d vertices and %d triangles does not fit a page", win.verts, win.tris)
			}
			windows = append(windows, win)
		}
	}

	// Pack.
	m.Pages = m.Pages[:0]
	var cur *Page
	for _, w := range windows {
		if cur == nil || cur.Root != w.root ||
			int(cur.Vertices)+w.verts > PageMaxVertices || int(cur.Triangles)+w.tris > PageMaxTriangles {
			m.Pages = append(m.Pages, Page{ClusterFirst: w.first, Root: w.root})
			cur = &m.Pages[len(m.Pages)-1]
		}
		cur.ClusterCount += w.count
		cur.Vertices += int32(w.verts)
		cur.Triangles += int32(w.tris)
	}
	// Every cluster is in exactly one page, in order.
	next := int32(0)
	for pi := range m.Pages {
		p := &m.Pages[pi]
		if p.ClusterFirst != next {
			return fmt.Errorf("pages: page %d starts at cluster %d, not %d", pi, p.ClusterFirst, next)
		}
		next += p.ClusterCount
		p.Level = 1 << 30
		for c := p.ClusterFirst; c < p.ClusterFirst+p.ClusterCount; c++ {
			p.Level = min(p.Level, m.Clusters[c].Level)
		}
	}
	if int(next) != len(m.Clusters) {
		return fmt.Errorf("pages cover %d of %d clusters", next, len(m.Clusters))
	}

	// Parents: the pages holding what each consuming group made.
	madeIn := map[groupKey][]int32{}
	for pi := range m.Pages {
		p := &m.Pages[pi]
		for c := p.ClusterFirst; c < p.ClusterFirst+p.ClusterCount; c++ {
			if g := m.Clusters[c].Group; g >= 0 {
				k := groupKey{slot[c], g}
				if l := madeIn[k]; len(l) == 0 || l[len(l)-1] != int32(pi) {
					madeIn[k] = append(l, int32(pi))
				}
			}
		}
	}
	for pi := range m.Pages {
		p := &m.Pages[pi]
		p.Parents = nil
		for c := p.ClusterFirst; c < p.ClusterFirst+p.ClusterCount; c++ {
			if g := m.Clusters[c].Parent; g >= 0 {
				for _, pp := range madeIn[groupKey{slot[c], g}] {
					if pp != int32(pi) && !slices.Contains(p.Parents, pp) {
						p.Parents = append(p.Parents, pp)
					}
				}
			}
		}
		slices.Sort(p.Parents)
	}

	// Encode, then decode what was encoded: the mesh becomes exactly what
	// a load will see, and anything the packing lost is caught here.
	var blob []byte
	for pi := range m.Pages {
		p := &m.Pages[pi]
		b := m.encodePage(pi, local, q)
		p.Offset, p.Size = uint64(len(blob)), uint32(len(b))
		blob = append(blob, b...)
	}
	m.data = blob
	m.dataPages = uint64(len(blob))
	var verts, normals []float32
	var idx []uint32
	for pi := range m.Pages {
		d, err := m.decodePage(pi, blob[m.Pages[pi].Offset:m.Pages[pi].Offset+uint64(m.Pages[pi].Size)])
		if err != nil {
			return fmt.Errorf("pages: page %d does not decode: %w", pi, err)
		}
		base := uint32(len(verts) / 5)
		for _, ix := range d.Indices {
			idx = append(idx, base+ix)
		}
		verts = append(verts, d.Vertices...)
		normals = append(normals, d.Normals...)
	}
	if len(idx) != len(m.Indices) {
		return fmt.Errorf("pages decode to %d indices, not %d", len(idx), len(m.Indices))
	}
	for i := range idx {
		a, b := m.Indices[i], idx[i]
		if !slices.Equal(m.Vertices[5*a:5*a+5], verts[5*b:5*b+5]) || !slices.Equal(m.Normals[3*a:3*a+3], normals[3*b:3*b+3]) {
			return fmt.Errorf("pages: corner %d decodes to %v %v, not %v %v (was the pool quantised?)", i,
				verts[5*b:5*b+5], normals[3*b:3*b+3], m.Vertices[5*a:5*a+5], m.Normals[3*a:3*a+3])
		}
	}
	m.Vertices, m.Normals, m.Indices = verts, normals, idx
	m.PoolVertices = int32(len(verts) / 5)
	return nil
}

// encodePage packs page pi: a header, then each cluster (encodeCluster).
func (m *Mesh) encodePage(pi int, local [][]uint32, q []QVertex) []byte {
	p := &m.Pages[pi]
	var w bitWriter
	w.write(pageMagic, 32)
	w.write(uint32(p.ClusterFirst), 32)
	w.write(uint32(p.ClusterCount), 32)
	for c := p.ClusterFirst; c < p.ClusterFirst+p.ClusterCount; c++ {
		m.encodeCluster(&w, &m.Clusters[c], local[c], q)
	}
	return w.buf
}

// encodeCluster packs one cluster: its vertex and triangle counts, its
// fields' bases and widths, its vertices, then its local indices.
func (m *Mesh) encodeCluster(w *bitWriter, c *Cluster, local []uint32, q []QVertex) {
	nv := len(local)
	var pmin, pmax [3]int32
	var umin, umax [2]int32
	for i, v := range local {
		for k := 0; k < 3; k++ {
			x := q[v].Pos[k]
			if i == 0 || x < pmin[k] {
				pmin[k] = x
			}
			if i == 0 || x > pmax[k] {
				pmax[k] = x
			}
		}
		for k := 0; k < 2; k++ {
			x := q[v].UV[k]
			if i == 0 || x < umin[k] {
				umin[k] = x
			}
			if i == 0 || x > umax[k] {
				umax[k] = x
			}
		}
	}
	var pb [3]uint8
	var ub [2]uint8
	for k := 0; k < 3; k++ {
		pb[k] = bitsFor(uint32(pmax[k] - pmin[k]))
	}
	for k := 0; k < 2; k++ {
		ub[k] = bitsFor(uint32(umax[k] - umin[k]))
	}
	ib := bitsFor(uint32(max(nv-1, 0)))
	w.write(uint32(nv), 16)
	w.write(c.TriCount, 16)
	for k := 0; k < 3; k++ {
		w.writeI32(pmin[k])
		w.write(uint32(pb[k]), 8)
	}
	for k := 0; k < 2; k++ {
		w.writeI32(umin[k])
		w.write(uint32(ub[k]), 8)
	}
	w.write(uint32(ib), 8)
	nb := m.Quant.NormalBits
	for _, v := range local {
		for k := 0; k < 3; k++ {
			w.write(uint32(q[v].Pos[k]-pmin[k]), pb[k])
		}
		for k := 0; k < 2; k++ {
			w.write(uint32(q[v].UV[k]-umin[k]), ub[k])
		}
		w.write(uint32(q[v].Normal[0]), nb)
		w.write(uint32(q[v].Normal[1]), nb)
	}
	slotOf := make(map[uint32]uint32, nv)
	for i, v := range local {
		slotOf[v] = uint32(i)
	}
	for _, v := range m.Indices[3*c.TriFirst : 3*(c.TriFirst+c.TriCount)] {
		w.write(slotOf[v], ib)
	}
	w.align()
}

// decodePage unpacks page pi from its bytes.
func (m *Mesh) decodePage(pi int, b []byte) (PageData, error) {
	if pi < 0 || pi >= len(m.Pages) {
		return PageData{}, fmt.Errorf("no page %d", pi)
	}
	p := &m.Pages[pi]
	r := bitReader{buf: b}
	hdr := [3]uint32{}
	for i := range hdr {
		v, err := r.read(32)
		if err != nil {
			return PageData{}, err
		}
		hdr[i] = v
	}
	if hdr[0] != pageMagic || int32(hdr[1]) != p.ClusterFirst || int32(hdr[2]) != p.ClusterCount {
		return PageData{}, fmt.Errorf("page %d's header does not match the mesh (re-cooked under it?)", pi)
	}
	d := PageData{
		Vertices: make([]float32, 0, int(p.Vertices)*5),
		Normals:  make([]float32, 0, int(p.Vertices)*3),
		Indices:  make([]uint32, 0, int(p.Triangles)*3),
	}
	q := m.Quant
	for c := p.ClusterFirst; c < p.ClusterFirst+p.ClusterCount; c++ {
		nv32, err := r.read(16)
		if err != nil {
			return PageData{}, err
		}
		nt, err := r.read(16)
		if err != nil {
			return PageData{}, err
		}
		if nt != m.Clusters[c].TriCount {
			return PageData{}, fmt.Errorf("page %d: cluster %d has %d triangles, not %d", pi, c, nt, m.Clusters[c].TriCount)
		}
		var pmin [3]int32
		var pb [3]uint8
		var umin [2]int32
		var ub [2]uint8
		for k := 0; k < 3; k++ {
			if pmin[k], err = r.readI32(); err != nil {
				return PageData{}, err
			}
			v, err := r.read(8)
			if err != nil {
				return PageData{}, err
			}
			pb[k] = uint8(min(v, 32))
		}
		for k := 0; k < 2; k++ {
			if umin[k], err = r.readI32(); err != nil {
				return PageData{}, err
			}
			v, err := r.read(8)
			if err != nil {
				return PageData{}, err
			}
			ub[k] = uint8(min(v, 32))
		}
		ibv, err := r.read(8)
		if err != nil {
			return PageData{}, err
		}
		ib := uint8(min(ibv, 32))
		base := uint32(len(d.Vertices) / 5)
		nv := int(nv32)
		if int(base)+nv > int(p.Vertices) {
			return PageData{}, fmt.Errorf("page %d holds more vertices than it says", pi)
		}
		for i := 0; i < nv; i++ {
			var pos [3]float32
			for k := 0; k < 3; k++ {
				v, err := r.read(pb[k])
				if err != nil {
					return PageData{}, err
				}
				pos[k] = q.Pos(pmin[k] + int32(v))
			}
			var uv [2]float32
			for k := 0; k < 2; k++ {
				v, err := r.read(ub[k])
				if err != nil {
					return PageData{}, err
				}
				uv[k] = q.UV(umin[k] + int32(v))
			}
			ox, err := r.read(q.NormalBits)
			if err != nil {
				return PageData{}, err
			}
			oy, err := r.read(q.NormalBits)
			if err != nil {
				return PageData{}, err
			}
			n := q.Normal([2]uint16{uint16(ox), uint16(oy)})
			d.Vertices = append(d.Vertices, pos[0], pos[1], pos[2], uv[0], uv[1])
			d.Normals = append(d.Normals, n[0], n[1], n[2])
		}
		for i := 0; i < 3*int(nt); i++ {
			v, err := r.read(ib)
			if err != nil {
				return PageData{}, err
			}
			if int(v) >= nv {
				return PageData{}, fmt.Errorf("page %d: cluster %d indexes past its vertices", pi, c)
			}
			d.Indices = append(d.Indices, base+v)
		}
		r.align()
	}
	if len(d.Vertices)/5 != int(p.Vertices) || len(d.Indices)/3 != int(p.Triangles) {
		return PageData{}, fmt.Errorf("page %d decodes to %d vertices and %d triangles, not %d and %d",
			pi, len(d.Vertices)/5, len(d.Indices)/3, p.Vertices, p.Triangles)
	}
	return d, nil
}

// ClusterOffsets is, for page pi, each of its clusters' first triangle
// within the page's decoded indices — what a draw of one of them needs —
// without decoding it.
func (m *Mesh) ClusterOffsets(pi int) []uint32 {
	p := &m.Pages[pi]
	out := make([]uint32, p.ClusterCount)
	at := uint32(0)
	for k := range out {
		out[k] = at
		at += m.Clusters[p.ClusterFirst+int32(k)].TriCount
	}
	return out
}

// RootPages is how many of the pages are root pages.
func (m *Mesh) RootPages() int {
	n := 0
	for i := range m.Pages {
		if m.Pages[i].Root {
			n++
		}
	}
	return n
}

// PageBytes is the packed size of every page together.
func (m *Mesh) PageBytes() uint64 {
	var n uint64
	for i := range m.Pages {
		n += uint64(m.Pages[i].Size)
	}
	return n
}

// le is the byte order of the file's few fixed fields.
var le = binary.LittleEndian
