package interpret

// tempobuild.go — the tempo marks, tempo text and fermatas as one tempo
// map.
//
// First the marks become a tempo over the written score (writtenTempo):
// points in written whole notes, each a tempo that holds or moves in a
// straight line to the next point. score/tempo.go has what the words
// mean; here they become numbers: a "rit." moves from the tempo it finds
// to its target by its end, "a tempo" goes back to the last tempo set by a
// number, "meno mosso" steps down from what is playing. Then that is laid
// over the bars as played, so a repeat that jumps back picks the right
// tempo up, and every fermata divides the tempo under it by its length
// (score.FermataScale): a fermata held twice as long plays at half speed
// for its own written length, in every line at once.

import (
	"sort"

	"foundry/internal/score"
)

// wpoint is a tempo in written time: from at, q quarters a minute; ramp
// moves in a straight line to the next point's q.
type wpoint struct {
	at   score.Frac
	q    float64
	ramp bool
}

// writtenTempo turns the tempo marks into points over the written score.
func (in *interp) writtenTempo() []wpoint {
	marks := append([]score.TempoMark(nil), in.s.Tempo...)
	sort.SliceStable(marks, func(i, j int) bool { return score.PosLess(marks[i].At, marks[j].At) })
	at := make([]score.Frac, len(marks))
	for i, m := range marks {
		at[i] = in.s.PosFrac(m.At, in.starts)
	}
	end := in.starts[len(in.starts)-1]
	absolute := func(m score.TempoMark) bool { return m.BPM > 0 && m.Change() == score.TempoSet }
	cur := float64(DefaultQPM)
	for _, m := range marks {
		if m.BPM > 0 {
			cur = m.QuarterBPM()
			break
		}
	}
	base, primo := cur, cur
	var pts []wpoint
	push := func(a score.Frac, q float64, ramp bool) { pts = append(pts, wpoint{a, q, ramp}) }
	for i, m := range marks {
		a := at[i]
		next, hasNext := end, i+1 < len(marks)
		if hasNext {
			next = at[i+1]
		}
		switch ch := m.Change(); {
		case m.Ramp && m.BPM > 0:
			// The older spelling: from this mark's number to the next's.
			cur = m.QuarterBPM()
			base = cur
			push(a, cur, hasNext && a.Less(next))
		case ch.Gradual():
			stop := next
			if m.HasTo {
				if to := in.s.PosFrac(m.To, in.starts); to.Less(stop) && a.Less(to) {
					stop = to
				}
			}
			target := cur * m.Factor()
			switch {
			case m.BPM > 0:
				target = m.QuarterBPM()
			case hasNext && stop.Eq(next) && absolute(marks[i+1]):
				target = marks[i+1].QuarterBPM()
			}
			if a.Less(stop) {
				push(a, cur, true)
				push(stop, target, false)
			} else {
				push(a, target, false)
			}
			cur = target
		case ch == score.TempoATempo:
			if m.BPM > 0 {
				base = m.QuarterBPM()
			}
			cur = base
			push(a, cur, false)
		case ch == score.TempoPrimo:
			cur, base = primo, primo
			push(a, cur, false)
		case ch == score.TempoMeno || ch == score.TempoPiu:
			if m.BPM > 0 {
				cur = m.QuarterBPM()
			} else {
				cur *= m.Factor()
			}
			push(a, cur, false)
		default:
			if m.BPM > 0 {
				cur = m.QuarterBPM()
				base = cur
			}
			push(a, cur, false)
		}
	}
	return pts
}

// tempoRight is the written tempo at x, from x on (a step at x counts),
// and whether it is ramping there.
func tempoRight(pts []wpoint, x score.Frac) (float64, bool) {
	k := -1
	for i := range pts {
		if !x.Less(pts[i].at) {
			k = i
		}
	}
	if k < 0 {
		if len(pts) == 0 {
			return DefaultQPM, false
		}
		return pts[0].q, false
	}
	return tempoOn(pts, k, x)
}

// tempoLeft is the written tempo just before x: what a ramp reaches at x.
func tempoLeft(pts []wpoint, x score.Frac) float64 {
	k := -1
	for i := range pts {
		if pts[i].at.Less(x) {
			k = i
		}
	}
	if k < 0 {
		q, _ := tempoRight(pts, x)
		return q
	}
	q, _ := tempoOn(pts, k, x)
	return q
}

// tempoOn is segment k's tempo at x.
func tempoOn(pts []wpoint, k int, x score.Frac) (float64, bool) {
	p := pts[k]
	if p.ramp && k+1 < len(pts) && p.at.Less(pts[k+1].at) {
		t := x.Sub(p.at).Div(pts[k+1].at.Sub(p.at)).Float()
		return p.q + (pts[k+1].q-p.q)*t, true
	}
	return p.q, false
}

// fermataSpan holds the tempo divided by k over [a, b), performance time.
type fermataSpan struct {
	a, b score.Frac
	k    float64
}

// fermatas finds every fermata as played: the span of the event it is on
// (a bar rest's whole bar), at its scale. Overlapping ones merge, the
// longest scale winning.
func (in *interp) fermatas() []fermataSpan {
	var out []fermataSpan
	for _, pb := range in.order {
		for _, l := range in.s.Lines {
			for _, st := range l.Staves {
				for _, v := range st.Voices {
					m := v.Measure(pb.bar)
					if len(m.Events) == 0 {
						continue
					}
					var offs []score.Frac
					for ei, e := range m.Events {
						if e.Grace != score.NotGrace {
							continue
						}
						for _, mk := range e.Marks {
							if mk.Name != "ferm" {
								continue
							}
							if offs == nil {
								offs, _ = m.Offsets(pb.length)
							}
							a := pb.start.Add(offs[ei])
							b := a.Add(m.Actual(e, pb.length))
							if a.Less(b) {
								out = append(out, fermataSpan{a, b, score.FermataScale(mk.Value)})
							}
						}
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].a.Less(out[j].a) })
	var merged []fermataSpan
	for _, f := range out {
		if n := len(merged); n > 0 && f.a.Less(merged[n-1].b) {
			last := &merged[n-1]
			if last.b.Less(f.b) {
				last.b = f.b
			}
			last.k = max(last.k, f.k)
			continue
		}
		merged = append(merged, f)
	}
	return merged
}

// buildTempo lays the written tempo over the bars as played, fermatas
// dividing it: a point at each played bar's start with the tempo in force
// there (so a repeat jumping back picks the right one up), one at each
// written point inside a bar and one at each fermata's ends. A ramp that
// meets a step ends on a point of its own at the step's tick, so the ramp
// reaches what it should before the tempo steps.
func (in *interp) buildTempo() {
	wp := in.writtenTempo()
	ferm := in.fermatas()
	scaleAt := func(x score.Frac, left bool) float64 {
		for _, f := range ferm {
			if left && f.a.Less(x) && !f.b.Less(x) || !left && !x.Less(f.a) && x.Less(f.b) {
				return f.k
			}
		}
		return 1
	}
	var pts []TempoPoint
	lastEnd := 0.0 // what the last point ramps to
	emit := func(tick, v0, v1 float64, ramp bool) {
		if n := len(pts); n > 0 {
			last := &pts[n-1]
			if last.Tick == tick && !last.Ramp {
				last.QPM, last.Ramp = v0, ramp
				lastEnd = v1
				return
			}
			if last.Ramp && lastEnd != v0 {
				pts = append(pts, TempoPoint{Tick: tick, QPM: lastEnd})
			} else if !last.Ramp && !ramp && last.QPM == v0 {
				return
			}
		}
		pts = append(pts, TempoPoint{Tick: tick, QPM: v0, Ramp: ramp})
		lastEnd = v1
	}
	for _, pb := range in.order {
		ws := in.starts[pb.bar-1]
		we := ws.Add(pb.length)
		xs := []score.Frac{ws}
		for _, p := range wp {
			if ws.Less(p.at) && p.at.Less(we) {
				xs = append(xs, p.at)
			}
		}
		for _, f := range ferm {
			for _, x := range []score.Frac{f.a, f.b} {
				if w := ws.Add(x.Sub(pb.start)); ws.Less(w) && w.Less(we) {
					xs = append(xs, w)
				}
			}
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i].Less(xs[j]) })
		for j, x := range xs {
			if j > 0 && x.Eq(xs[j-1]) {
				continue
			}
			x1 := we
			for k := j + 1; k < len(xs); k++ {
				if !xs[k].Eq(x) {
					x1 = xs[k]
					break
				}
			}
			px, px1 := pb.start.Add(x.Sub(ws)), pb.start.Add(x1.Sub(ws))
			q0, ramp := tempoRight(wp, x)
			v0 := q0 / scaleAt(px, false)
			v1 := v0
			if ramp {
				v1 = tempoLeft(wp, x1) / scaleAt(px1, true)
			}
			emit(px.TicksFloat(), v0, v1, ramp && v0 != v1)
		}
	}
	if n := len(pts); n > 0 && pts[n-1].Ramp && len(in.order) > 0 {
		// A ramp to the very end needs a point there to ramp to.
		last := in.order[len(in.order)-1]
		pts = append(pts, TempoPoint{Tick: last.start.Add(last.length).TicksFloat(), QPM: lastEnd})
	}
	in.tempo = NewTempoMap(pts)
}
