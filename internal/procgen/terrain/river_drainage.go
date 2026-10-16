package terrain

// river_drainage.go — rivers traced down a heightfield's own drainage
// network (docs/terrain-paint/PHASE_W3.md, "from_drainage"; river.go has
// what happens to them next).
//
// THE NETWORK. A priority flood of the heights (flood.go: the map edge is
// the outlet, pits fill to their spill) gives every sample a receiver, and
// walking its order backwards gives each sample the area draining through
// it. That area is put on the flow mask's own scale (masks.go: log of the
// area, 0 at 4 m², 1 at a quarter of a square kilometre), so Threshold
// reads like a `flow` band: a sample at or above it is CHANNEL.
//
// THE RIVERS. From each outlet of the channel network (a channel sample
// whose receiver is the map edge or ground below the threshold), largest
// first, the trace walks UPSTREAM, always into the donor draining the most
// (the main stem); every other channel donor along the way starts a
// tributary of its own, traced the same way and ending ON the sample where
// it joins — so its mouth lies on its parent's centreline and the carve
// sees a confluence. A river shorter than MinLength is dropped (and with
// it whatever joins it, all of which is shorter still).
//
// THE SHAPE. The D8 staircase is simplified (Douglas–Peucker at Simplify
// metres, default four cells, the mouth kept exact), its corners cut once
// (Chaikin, the ends kept) so a traced river bends rather than zigzags, and
// each point takes a width and a depth from its flow value: WidthMin..WidthMax and DepthMin..DepthMax as
// the value runs from the threshold to 1 (width_from_flow: a river widens
// downstream as its catchment grows).
//
// HAND RIVERS WIN. TrimAgainst cuts each traced river where it enters the
// corridor of a painted (or adopted) river — its banks plus its floodplain
// and reach, so a trace running down the same valley floor beside it is
// caught too — keeping the part upstream and running it on to the nearest
// point of the hand river's centreline, so it ends ON the hand river as a
// tributary. A drainage river that has been adopted, or drawn over, is not
// carved twice.

import (
	"fmt"
	"math"
	"sort"
)

// DrainageSettings is how rivers are traced from drainage.
type DrainageSettings struct {
	// Threshold is the flow value (0..1, the flow mask's scale) from which
	// a sample is channel.
	Threshold float64
	// MinLength drops rivers shorter than this (m).
	MinLength float64
	// WidthMin/Max and DepthMin/Max are the width and depth at the
	// threshold and at a flow value of 1.
	WidthMin, WidthMax, DepthMin, DepthMax float64
	// Simplify is the Douglas–Peucker tolerance (m); 0: two cells.
	Simplify float64
	// MaxRivers caps how many (largest first); 0: no cap.
	MaxRivers int
}

// DefaultDrainage is a stream network's.
func DefaultDrainage() DrainageSettings {
	return DrainageSettings{Threshold: 0.55, MinLength: 40, WidthMin: 1.5, WidthMax: 8, DepthMin: 0.35, DepthMax: 1.4}
}

// TracedRiver is one river traced from drainage, before any shape is
// given to it: what TraceDrainage returns, and what rivers.yaml lists for
// the editor's Adopt.
type TracedRiver struct {
	Name   string
	Points [][2]float64
	Width  []float64
	Depth  []float64
	// Flow is the flow value at each point.
	Flow []float64
	// Parent is the river it joins (-1: it reaches an outlet).
	Parent int
}

// FlowValue is a drainage area (m²) on the flow mask's scale.
func FlowValue(area float64) float64 {
	lmin, lmax := math.Log(flowAreaMin), math.Log(flowAreaMax)
	return math.Min(math.Max((math.Log(math.Max(area, 1e-9))-lmin)/(lmax-lmin), 0), 1)
}

// TraceDrainage traces g's drainage network into rivers (file comment).
func TraceDrainage(g *Grid, set DrainageSettings) []TracedRiver {
	s := g.S()
	n := s * s
	fl := newFlood(n)
	fl.run(g.H, s, float32(1e-4*g.Cell))
	area := make([]float32, n)
	fl.accumulate(area)
	cell2 := g.Cell * g.Cell
	fv := make([]float64, n)
	ch := make([]bool, n)
	for k, a := range area {
		fv[k] = FlowValue(float64(a) * cell2)
		ch[k] = fv[k] >= set.Threshold
	}
	// Channel donors of every channel sample.
	donors := map[int32][]int32{}
	for k := 0; k < n; k++ {
		if !ch[k] {
			continue
		}
		if r := fl.receiver[k]; r >= 0 && ch[r] {
			donors[r] = append(donors[r], int32(k))
		}
	}
	for _, d := range donors {
		sort.Slice(d, func(a, b int) bool {
			if area[d[a]] != area[d[b]] {
				return area[d[a]] > area[d[b]]
			}
			return d[a] < d[b]
		})
	}
	var outlets []int32
	for k := 0; k < n; k++ {
		if ch[k] {
			if r := fl.receiver[k]; r < 0 || !ch[r] {
				outlets = append(outlets, int32(k))
			}
		}
	}
	sort.Slice(outlets, func(a, b int) bool {
		if area[outlets[a]] != area[outlets[b]] {
			return area[outlets[a]] > area[outlets[b]]
		}
		return outlets[a] < outlets[b]
	})
	tol := set.Simplify
	if tol <= 0 {
		tol = 4 * g.Cell
	}
	type stem struct {
		mouth  int32 // the sample it starts tracing up from (its mouth)
		parent int
		join   int32 // the parent's sample it flows into (-1: an outlet)
	}
	var out []TracedRiver
	queue := make([]stem, 0, len(outlets))
	for _, o := range outlets {
		queue = append(queue, stem{o, -1, -1})
	}
	for qi := 0; qi < len(queue); qi++ {
		st := queue[qi]
		// Walk upstream along the largest donor, collecting side donors.
		path := []int32{st.mouth}
		var sides []int32
		k := st.mouth
		for {
			d := donors[k]
			if len(d) == 0 {
				break
			}
			sides = append(sides, d[1:]...)
			k = d[0]
			path = append(path, k)
		}
		length := float64(len(path)-1) * g.Cell
		if length < set.MinLength || len(path) < 3 {
			continue
		}
		// Source to mouth, and on to the junction on its parent.
		for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
			path[i], path[j] = path[j], path[i]
		}
		if st.join >= 0 {
			path = append(path, st.join)
		}
		pts := make([][2]float64, len(path))
		for i, k := range path {
			pts[i] = [2]float64{g.X(int(k) % s), g.Z(int(k) / s)}
		}
		keep := DouglasPeucker(pts, tol)
		tr := TracedRiver{Name: fmt.Sprintf("drainage_%d", len(out)+1), Parent: st.parent}
		span := math.Max(1-set.Threshold, 1e-6)
		for _, i := range keep {
			f := fv[path[i]]
			t := math.Min(math.Max((f-set.Threshold)/span, 0), 1)
			tr.Points = append(tr.Points, [2]float64{round2(pts[i][0]), round2(pts[i][1])})
			tr.Width = append(tr.Width, round2(set.WidthMin+(set.WidthMax-set.WidthMin)*t))
			tr.Depth = append(tr.Depth, round2(set.DepthMin+(set.DepthMax-set.DepthMin)*t))
			tr.Flow = append(tr.Flow, round3(f))
		}
		chaikin(&tr)
		me := len(out)
		out = append(out, tr)
		if set.MaxRivers > 0 && len(out) >= set.MaxRivers {
			break
		}
		// Side donors join this river where they meet it: their mouth is
		// the junction sample (on this river), their trace starts there.
		for _, sd := range sides {
			if r := fl.receiver[sd]; r >= 0 {
				queue = append(queue, stem{mouth: sd, parent: me, join: r})
			}
		}
	}
	return out
}

// DouglasPeucker is the indices of pts kept at tolerance tol (the ends
// always).
func DouglasPeucker(pts [][2]float64, tol float64) []int {
	n := len(pts)
	if n <= 2 {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	keep := make([]bool, n)
	keep[0], keep[n-1] = true, true
	type span struct{ a, b int }
	stack := []span{{0, n - 1}}
	for len(stack) > 0 {
		sp := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		a, b := pts[sp.a], pts[sp.b]
		dx, dz := b[0]-a[0], b[1]-a[1]
		l := math.Hypot(dx, dz)
		best, bi := -1.0, -1
		for i := sp.a + 1; i < sp.b; i++ {
			p := pts[i]
			var d float64
			if l < 1e-9 {
				d = math.Hypot(p[0]-a[0], p[1]-a[1])
			} else {
				d = math.Abs((p[0]-a[0])*dz-(p[1]-a[1])*dx) / l
			}
			if d > best {
				best, bi = d, i
			}
		}
		if bi >= 0 && best > tol {
			keep[bi] = true
			stack = append(stack, span{sp.a, bi}, span{bi, sp.b})
		}
	}
	var idx []int
	for i, k := range keep {
		if k {
			idx = append(idx, i)
		}
	}
	return idx
}

// TrimAgainst cuts each traced river where it first enters the corridor of
// any hand river (file comment): its banks plus the hand river's floodplain
// and reach. The part upstream is kept and given one more point, the
// nearest on that hand river's centreline, so it joins it; a river that
// starts inside a corridor is dropped. minLength drops what is left too
// short.
func TrimAgainst(rivers []TracedRiver, hands []RiverSpec, minLength float64) []TracedRiver {
	if len(hands) == 0 {
		return rivers
	}
	// inside is the hand river whose corridor holds (x, z) and the nearest
	// point on its centreline, or -1.
	inside := func(x, z float64) (int, [2]float64) {
		for hi, h := range hands {
			reach := h.Shape.Floodplain + h.Shape.Reach
			best, bp := math.Inf(1), [2]float64{}
			for i := 0; i+1 < len(h.Points); i++ {
				a, b := h.Points[i], h.Points[i+1]
				w := math.Max(pick(h.Width, i, 4), pick(h.Width, i+1, 4))
				if d, p := segNearest(x, z, a, b); d <= w/2+reach+1 && d < best {
					best, bp = d, p
				}
			}
			if !math.IsInf(best, 1) {
				return hi, bp
			}
		}
		return -1, [2]float64{}
	}
	var out []TracedRiver
	for _, r := range rivers {
		cut, mouth := len(r.Points), [2]float64{}
		for i, p := range r.Points {
			if h, q := inside(p[0], p[1]); h >= 0 {
				cut, mouth = i, q
				break
			}
		}
		if cut < 2 && cut < len(r.Points) {
			continue
		}
		t := r
		t.Points = append([][2]float64(nil), r.Points[:cut]...)
		t.Width = append([]float64(nil), r.Width[:cut]...)
		t.Depth = append([]float64(nil), r.Depth[:cut]...)
		t.Flow = append([]float64(nil), r.Flow[:cut]...)
		if cut < len(r.Points) {
			t.Points = append(t.Points, [2]float64{round2(mouth[0]), round2(mouth[1])})
			t.Width = append(t.Width, r.Width[cut])
			t.Depth = append(t.Depth, r.Depth[cut])
			t.Flow = append(t.Flow, r.Flow[cut])
		}
		if len(t.Points) < 2 || polyLength(t.Points) < minLength {
			continue
		}
		out = append(out, t)
	}
	return out
}

// segNearest is the distance from (x, z) to segment ab and the nearest
// point on it.
func segNearest(x, z float64, a, b [2]float64) (float64, [2]float64) {
	dx, dz := b[0]-a[0], b[1]-a[1]
	l2 := dx*dx + dz*dz
	t := 0.0
	if l2 > 0 {
		t = math.Min(math.Max(((x-a[0])*dx+(z-a[1])*dz)/l2, 0), 1)
	}
	p := [2]float64{a[0] + dx*t, a[1] + dz*t}
	return math.Hypot(x-p[0], z-p[1]), p
}

// segDist is the distance from (x, z) to segment ab.
func segDist(x, z float64, a, b [2]float64) float64 {
	dx, dz := b[0]-a[0], b[1]-a[1]
	l2 := dx*dx + dz*dz
	t := 0.0
	if l2 > 0 {
		t = math.Min(math.Max(((x-a[0])*dx+(z-a[1])*dz)/l2, 0), 1)
	}
	return math.Hypot(x-(a[0]+dx*t), z-(a[1]+dz*t))
}

// polyLength is a polyline's length.
func polyLength(p [][2]float64) float64 {
	l := 0.0
	for i := 1; i < len(p); i++ {
		l += math.Hypot(p[i][0]-p[i-1][0], p[i][1]-p[i-1][1])
	}
	return l
}

// chaikin cuts a traced river's corners once (each segment's quarter
// points), keeping its source and its mouth; widths, depths and flows are
// interpolated alike and rounded as the trace rounds them.
func chaikin(tr *TracedRiver) {
	n := len(tr.Points)
	if n < 3 {
		return
	}
	var pts [][2]float64
	var ws, ds, fs []float64
	add := func(i, j int, t float64) {
		a, b := tr.Points[i], tr.Points[j]
		pts = append(pts, [2]float64{round2(a[0] + (b[0]-a[0])*t), round2(a[1] + (b[1]-a[1])*t)})
		ws = append(ws, round2(tr.Width[i]+(tr.Width[j]-tr.Width[i])*t))
		ds = append(ds, round2(tr.Depth[i]+(tr.Depth[j]-tr.Depth[i])*t))
		fs = append(fs, round3(tr.Flow[i]+(tr.Flow[j]-tr.Flow[i])*t))
	}
	add(0, 0, 0)
	for i := 0; i+1 < n; i++ {
		if i > 0 {
			add(i, i+1, 0.25)
		}
		if i+1 < n-1 {
			add(i, i+1, 0.75)
		}
	}
	add(n-1, n-1, 0)
	tr.Points, tr.Width, tr.Depth, tr.Flow = pts, ws, ds, fs
}
