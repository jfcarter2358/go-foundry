package terrain

// flood.go — routing water over a heightfield: priority flood.
//
// Both the drainage erosion and the flow mask need to know, for every
// sample, where its water goes and in what order to visit samples so that a
// sample's water has arrived before it is passed on. Priority flood (Barnes,
// Lehman & Mulla 2014) answers both at once:
//
//   - seed a min-heap with every edge sample (the map edge is the outlet:
//     water leaving it is gone);
//   - pop the lowest; each neighbour not yet reached is pushed at
//     max(its height, the popped height + eps), and its RECEIVER is the
//     sample that reached it.
//
// The pop order runs from the outlets uphill, so a sample's receiver always
// pops before it: walking the order backwards accumulates drainage area
// (each sample adds its area to its receiver), walking it forwards erodes
// from base level up. A pit is flooded to its spill height in the process
// (the +eps makes flats and filled pits drain toward their outlet rather
// than nowhere), which is what lets the drainage phase fill basins into flat
// playas and sand-floored washes.
//
// The heap breaks ties by push order, so the result never depends on
// anything but the heights.

import (
	"math"
)

// floodHeap is a binary min-heap of samples by (height, push sequence).
type floodHeap struct {
	h   []float32
	idx []int32
	seq []uint32
	n   uint32
}

func (q *floodHeap) less(a, b int) bool {
	if q.h[a] != q.h[b] {
		return q.h[a] < q.h[b]
	}
	return q.seq[a] < q.seq[b]
}

func (q *floodHeap) swap(a, b int) {
	q.h[a], q.h[b] = q.h[b], q.h[a]
	q.idx[a], q.idx[b] = q.idx[b], q.idx[a]
	q.seq[a], q.seq[b] = q.seq[b], q.seq[a]
}

func (q *floodHeap) push(h float32, i int32) {
	q.h = append(q.h, h)
	q.idx = append(q.idx, i)
	q.seq = append(q.seq, q.n)
	q.n++
	c := len(q.h) - 1
	for c > 0 {
		p := (c - 1) / 2
		if !q.less(c, p) {
			break
		}
		q.swap(c, p)
		c = p
	}
}

func (q *floodHeap) pop() (float32, int32) {
	h, i := q.h[0], q.idx[0]
	last := len(q.h) - 1
	q.swap(0, last)
	q.h, q.idx, q.seq = q.h[:last], q.idx[:last], q.seq[:last]
	c := 0
	for {
		l, r := 2*c+1, 2*c+2
		m := c
		if l < last && q.less(l, m) {
			m = l
		}
		if r < last && q.less(r, m) {
			m = r
		}
		if m == c {
			break
		}
		q.swap(c, m)
		c = m
	}
	return h, i
}

// neighbour offsets, 8-connected, and their lengths in cells.
var (
	nbDI   = [8]int{1, -1, 0, 0, 1, 1, -1, -1}
	nbDJ   = [8]int{0, 0, 1, -1, 1, -1, 1, -1}
	nbDist = [8]float32{1, 1, 1, 1, math.Sqrt2, math.Sqrt2, math.Sqrt2, math.Sqrt2}
)

// flood is a priority flood of the s x s heights h: the visit order (outlets
// first), each sample's receiver (-1 for an outlet) and the filled heights.
// eps is the rise per cell forced across flats.
type flood struct {
	order    []int32
	receiver []int32
	filled   []float32
	// rdist is the distance to the receiver, in cells.
	rdist []float32

	seen []bool
	heap floodHeap
}

func newFlood(n int) *flood {
	return &flood{order: make([]int32, 0, n), receiver: make([]int32, n), filled: make([]float32, n), rdist: make([]float32, n),
		seen: make([]bool, n)}
}

// run floods h (s x s) into f, reusing its buffers.
func (f *flood) run(h []float32, s int, eps float32) {
	f.order = f.order[:0]
	seen := f.seen
	for i := range seen {
		seen[i] = false
	}
	q := &f.heap
	q.h, q.idx, q.seq, q.n = q.h[:0], q.idx[:0], q.seq[:0], 0
	edge := func(i, j int) {
		k := j*s + i
		if seen[k] {
			return
		}
		seen[k] = true
		f.receiver[k] = -1
		f.rdist[k] = 1
		f.filled[k] = h[k]
		q.push(h[k], int32(k))
	}
	for i := 0; i < s; i++ {
		edge(i, 0)
		edge(i, s-1)
	}
	for j := 1; j < s-1; j++ {
		edge(0, j)
		edge(s-1, j)
	}
	for len(q.h) > 0 {
		hc, c := q.pop()
		f.order = append(f.order, c)
		ci, cj := int(c)%s, int(c)/s
		for d := 0; d < 8; d++ {
			ni, nj := ci+nbDI[d], cj+nbDJ[d]
			if ni < 0 || nj < 0 || ni >= s || nj >= s {
				continue
			}
			k := nj*s + ni
			if seen[k] {
				continue
			}
			seen[k] = true
			hn := max(h[k], hc+eps*nbDist[d])
			f.filled[k] = hn
			f.receiver[k] = c
			f.rdist[k] = nbDist[d]
			q.push(hn, int32(k))
		}
	}
}

// accumulate is each sample's drainage area in cells (itself included),
// walking the order backwards.
func (f *flood) accumulate(area []float32) {
	for i := range area {
		area[i] = 1
	}
	for k := len(f.order) - 1; k >= 0; k-- {
		c := f.order[k]
		if r := f.receiver[c]; r >= 0 {
			area[r] += area[c]
		}
	}
}

// accumulateMFD is drainage area in cells by MULTIPLE flow directions: each
// sample, highest first, shares its area among every lower neighbour on the
// filled surface in proportion to its slope (Quinn et al. 1991). Single-receiver
// area (accumulate) draws every channel as a straight 0°/45° line; shared
// area spreads over divergent ground and gathers in hollows, so the valleys
// it erodes are rounded and wander the way the ground does.
func (f *flood) accumulateMFD(area []float32, s int) {
	for i := range area {
		area[i] = 1
	}
	var w [8]float32
	for k := len(f.order) - 1; k >= 0; k-- {
		c := int(f.order[k])
		hc := f.filled[c]
		ci, cj := c%s, c/s
		sum := float32(0)
		for d := 0; d < 8; d++ {
			w[d] = 0
			ni, nj := ci+nbDI[d], cj+nbDJ[d]
			if ni < 0 || nj < 0 || ni >= s || nj >= s {
				continue
			}
			if dh := hc - f.filled[nj*s+ni]; dh > 0 {
				w[d] = dh / nbDist[d]
				sum += w[d]
			}
		}
		if sum <= 0 {
			continue // an outlet, or the bottom of a flat: it keeps its area
		}
		a := area[c] / sum
		for d := 0; d < 8; d++ {
			if w[d] > 0 {
				area[(cj+nbDJ[d])*s+ci+nbDI[d]] += a * w[d]
			}
		}
	}
}
