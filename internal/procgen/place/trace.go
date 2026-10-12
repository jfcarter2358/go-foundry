package place

// trace.go — the feet of the cliffs, traced from the cliff mask.
//
// A heightfield cliff is a band of steep samples: the cliff mask over its
// threshold. Its outline (marching squares on the mask, resampled to a
// coarse grid so a 0.5 m terrain's speckle does not make a thousand
// one-metre loops) runs round each band, along its FOOT on the downhill side
// and its RIM uphill. A cliff wall is built up from the foot, so the outline
// is split where it turns from one to the other — where the outward normal
// of the band (down the mask's gradient) stops pointing downhill — and the
// foot runs long enough to carry chunks are kept, smoothed and resampled to
// about a metre.

import (
	"math"
	"sort"

	"foundry/internal/procgen/terrain"
)

// WallLine is a line along the foot of a wall: points in XZ, and at each the
// horizontal unit direction OUT of the wall (downhill).
type WallLine struct {
	Points [][2]float64
	Out    [][2]float64
}

// Length is the line's length in metres.
func (l *WallLine) Length() float64 {
	d := 0.0
	for k := 1; k < len(l.Points); k++ {
		d += math.Hypot(l.Points[k][0]-l.Points[k-1][0], l.Points[k][1]-l.Points[k-1][1])
	}
	return d
}

// TraceSettings configure TraceCliffFeet.
type TraceSettings struct {
	// Threshold is the cliff mask level the outline follows.
	Threshold float64
	// Cell is the coarse grid the mask is averaged onto first (metres).
	Cell float64
	// MinLength drops feet shorter than this (metres).
	MinLength float64
	// Smooth is how many passes of neighbour averaging the line gets.
	Smooth int
}

// TraceCliffFeet is the feet of every cliff band of g's cliff mask.
func TraceCliffFeet(g *Ground, s TraceSettings) []WallLine {
	m := g.masks[terrain.MaskCliff]
	if m == nil {
		return nil
	}
	if s.Cell <= 0 {
		s.Cell = 2
	}
	if s.Threshold <= 0 {
		s.Threshold = 0.5
	}
	// The coarse field: each coarse corner the mean of the fine samples
	// within half a coarse cell.
	gr := g.Grid
	size := gr.Size()
	n := max(int(math.Round(size/s.Cell)), 2)
	cell := size / float64(n)
	cs := n + 1
	f := make([]float64, cs*cs)
	fs := gr.N + 1
	rad := max(int(math.Round(cell/gr.Cell/2)), 0)
	for j := 0; j < cs; j++ {
		for i := 0; i < cs; i++ {
			ci := int(math.Round(float64(i) * cell / gr.Cell))
			cj := int(math.Round(float64(j) * cell / gr.Cell))
			sum, cnt := 0.0, 0
			for dj := -rad; dj <= rad; dj++ {
				for di := -rad; di <= rad; di++ {
					a, b := ci+di, cj+dj
					if a < 0 || b < 0 || a >= fs || b >= fs {
						continue
					}
					sum += float64(m[b*fs+a])
					cnt++
				}
			}
			if cnt > 0 {
				f[j*cs+i] = sum / float64(cnt)
			}
		}
	}
	at := func(i, j int) float64 {
		i = min(max(i, 0), cs-1)
		j = min(max(j, 0), cs-1)
		return f[j*cs+i]
	}
	wx := func(i float64) float64 { return gr.MinX + i*cell }
	wz := func(j float64) float64 { return gr.MinZ + j*cell }
	thr := s.Threshold
	// Marching squares: each crossing edge gets an ID (its cell edge), and a
	// segment joins two crossings; lines are walked through the shared IDs.
	// Edge IDs: horizontal edge (i,j)-(i+1,j) is 2·(j·cs+i), vertical
	// (i,j)-(i,j+1) is 2·(j·cs+i)+1.
	cross := map[int][2]float64{}
	crossing := func(id int) [2]float64 {
		if p, ok := cross[id]; ok {
			return p
		}
		k := id / 2
		i, j := k%cs, k/cs
		a := at(i, j)
		var b float64
		var p [2]float64
		if id%2 == 0 {
			b = at(i+1, j)
			t := (thr - a) / (b - a)
			p = [2]float64{wx(float64(i) + t), wz(float64(j))}
		} else {
			b = at(i, j+1)
			t := (thr - a) / (b - a)
			p = [2]float64{wx(float64(i)), wz(float64(j) + t)}
		}
		cross[id] = p
		return p
	}
	adj := map[int][]int{}
	link := func(a, b int) {
		adj[a] = append(adj[a], b)
		adj[b] = append(adj[b], a)
	}
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			c := 0
			if at(i, j) >= thr {
				c |= 1
			}
			if at(i+1, j) >= thr {
				c |= 2
			}
			if at(i+1, j+1) >= thr {
				c |= 4
			}
			if at(i, j+1) >= thr {
				c |= 8
			}
			if c == 0 || c == 15 {
				continue
			}
			top := 2 * (j*cs + i)     // (i,j)-(i+1,j)
			bot := 2 * ((j+1)*cs + i) // (i,j+1)-(i+1,j+1)
			left := 2*(j*cs+i) + 1    // (i,j)-(i,j+1)
			right := 2*(j*cs+i+1) + 1 // (i+1,j)-(i+1,j+1)
			switch c {
			case 1, 14:
				link(top, left)
			case 2, 13:
				link(top, right)
			case 3, 12:
				link(left, right)
			case 4, 11:
				link(right, bot)
			case 6, 9:
				link(top, bot)
			case 7, 8:
				link(left, bot)
			case 5:
				// Saddles: decided by the centre, the same rule for every
				// cell (the line never crosses itself).
				if (at(i, j)+at(i+1, j)+at(i+1, j+1)+at(i, j+1))/4 >= thr {
					link(top, right)
					link(left, bot)
				} else {
					link(top, left)
					link(right, bot)
				}
			case 10:
				if (at(i, j)+at(i+1, j)+at(i+1, j+1)+at(i, j+1))/4 >= thr {
					link(top, left)
					link(right, bot)
				} else {
					link(top, right)
					link(left, bot)
				}
			}
		}
	}
	// Walk the lines, in a fixed order (by edge ID) so the result is
	// deterministic.
	ids := make([]int, 0, len(adj))
	for id := range adj {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	used := map[[2]int]bool{}
	edgeKey := func(a, b int) [2]int {
		if a > b {
			a, b = b, a
		}
		return [2]int{a, b}
	}
	var lines [][][2]float64
	for _, start := range ids {
		for _, nb := range adj[start] {
			if used[edgeKey(start, nb)] {
				continue
			}
			// Extend both ways from this segment.
			seq := []int{start, nb}
			used[edgeKey(start, nb)] = true
			for dir := 0; dir < 2; dir++ {
				for {
					last := seq[len(seq)-1]
					next := -1
					for _, c := range adj[last] {
						if !used[edgeKey(last, c)] {
							next = c
							break
						}
					}
					if next < 0 {
						break
					}
					used[edgeKey(last, next)] = true
					seq = append(seq, next)
				}
				reverseInts(seq)
			}
			pts := make([][2]float64, len(seq))
			for k, id := range seq {
				pts[k] = crossing(id)
			}
			lines = append(lines, pts)
		}
	}
	// Split each outline into its feet: where the band's outward normal
	// (down the mask's gradient) points downhill.
	var out []WallLine
	for _, pts := range lines {
		var cur WallLine
		flush := func() {
			if len(cur.Points) >= 2 {
				cur = resampleLine(cur, 1, s.Smooth)
				if cur.Length() >= s.MinLength {
					out = append(out, cur)
				}
			}
			cur = WallLine{}
		}
		for _, p := range pts {
			fi := (p[0] - gr.MinX) / cell
			fj := (p[1] - gr.MinZ) / cell
			mx := (sampleCoarse(f, cs, fi+0.5, fj) - sampleCoarse(f, cs, fi-0.5, fj)) / cell
			mz := (sampleCoarse(f, cs, fi, fj+0.5) - sampleCoarse(f, cs, fi, fj-0.5)) / cell
			ml := math.Hypot(mx, mz)
			hx, hz := g.Gradient(p[0], p[1])
			hl := math.Hypot(hx, hz)
			foot := false
			var o [2]float64
			if ml > 1e-9 && hl > 1e-9 {
				o = [2]float64{-mx / ml, -mz / ml}
				// Out of the band and downhill together.
				foot = (o[0]*(-hx)+o[1]*(-hz))/hl > 0.35
			}
			if !foot {
				flush()
				continue
			}
			cur.Points = append(cur.Points, p)
			cur.Out = append(cur.Out, o)
		}
		flush()
	}
	return out
}

// sampleCoarse is a bilinear lookup in a cs×cs field at fractional corners.
func sampleCoarse(f []float64, cs int, fi, fj float64) float64 {
	fi = math.Max(0, math.Min(float64(cs-1), fi))
	fj = math.Max(0, math.Min(float64(cs-1), fj))
	i, j := min(int(fi), cs-2), min(int(fj), cs-2)
	u, v := fi-float64(i), fj-float64(j)
	k := j*cs + i
	a := f[k] + (f[k+1]-f[k])*u
	b := f[k+cs] + (f[k+cs+1]-f[k+cs])*u
	return a + (b-a)*v
}

// resampleLine smooths a line (passes of neighbour averaging, the ends
// held) and resamples it every step metres, the out directions carried
// along and renormalised.
func resampleLine(l WallLine, step float64, passes int) WallLine {
	pts := append([][2]float64(nil), l.Points...)
	outs := append([][2]float64(nil), l.Out...)
	for p := 0; p < passes; p++ {
		np := append([][2]float64(nil), pts...)
		no := append([][2]float64(nil), outs...)
		for k := 1; k+1 < len(pts); k++ {
			for c := 0; c < 2; c++ {
				np[k][c] = (pts[k-1][c] + 2*pts[k][c] + pts[k+1][c]) / 4
				no[k][c] = (outs[k-1][c] + 2*outs[k][c] + outs[k+1][c]) / 4
			}
		}
		pts, outs = np, no
	}
	var res WallLine
	if len(pts) == 0 {
		return res
	}
	res.Points = append(res.Points, pts[0])
	res.Out = append(res.Out, unit2(outs[0]))
	carry := 0.0
	for k := 1; k < len(pts); k++ {
		a, b := pts[k-1], pts[k]
		d := math.Hypot(b[0]-a[0], b[1]-a[1])
		for carry+step <= d+1e-9 && d > 0 {
			carry += step
			t := carry / d
			res.Points = append(res.Points, [2]float64{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t})
			o := [2]float64{outs[k-1][0] + (outs[k][0]-outs[k-1][0])*t, outs[k-1][1] + (outs[k][1]-outs[k-1][1])*t}
			res.Out = append(res.Out, unit2(o))
		}
		carry -= d
	}
	return res
}

func unit2(v [2]float64) [2]float64 {
	l := math.Hypot(v[0], v[1])
	if l == 0 {
		return [2]float64{0, 1}
	}
	return [2]float64{v[0] / l, v[1] / l}
}

func reverseInts(a []int) {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
}
