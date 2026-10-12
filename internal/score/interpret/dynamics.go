package interpret

// dynamics.go — a line's dynamic level, from the marks to the curve.
//
// Every voice and staff of a line reads one level, set by the dynamics
// marks on any of them (default mf) and moved by hairpins:
//
//   - A mark (p, mf, ff, niente) steps the level where it stands; fp, sfp
//     and sfpp step it to their second level ("After").
//   - A hairpin (`cresc`/`dim` on its first event, `hairpin_end` on its
//     last, inclusive) or "cresc."/"dim." text with dashes (the same marks
//     with a text value) ramps the level in a straight line from where it
//     is to the dynamic at its end. A dynamic inside it is passed through
//     on the way. With no dynamic at its end it moves one step
//     (score.StepDynamic: mp to mf) and the level stays there. With no
//     `hairpin_end` it runs to the next dynamic on the line (the end of
//     its voice when there is none), or to the next hairpin in its voice.
//   - Momentary dynamics (sfz, sf, fz, rfz) and fp's first level act on
//     the notes they stand on only, as accents (Note.Accent): the curve
//     carries on beneath them.
//
// That is the line's Written curve. The dynamics lane's overrides
// (Line.Overrides: a segment dragged up or down) replace it between their
// two written points on every pass through them, which gives the
// Effective curve: what notes read their velocity from and what a held
// note follows. Marks stay as printed.

import (
	"sort"

	"foundry/internal/score"
)

// dynMark is a dynamic mark in performance time.
type dynMark struct {
	at   score.Frac
	name string
	dyn  score.Dynamic
}

// hairpin is a ramp of a line's level in performance time; open ones run
// to the next dynamic mark (at most to end).
type hairpin struct {
	at, end score.Frac
	dir     int
	open    bool
}

type pedalMark struct {
	at   score.Frac
	down bool
}

// lineMarks are the marks a walk collects that act on a whole line.
type lineMarks struct {
	dyns  []dynMark
	hairs []hairpin
	pedal []pedalMark
}

// hairpinState follows one voice's hairpin while the walk goes.
type hairpinState struct {
	on  bool
	at  score.Frac
	dir int
}

func (h *hairpinState) start(in *interp, li int, at score.Frac, dir int) {
	if h.on {
		// A hairpin starting closes the voice's last one there.
		in.lines[li].hairs = append(in.lines[li].hairs, hairpin{at: h.at, end: at, dir: h.dir})
	}
	*h = hairpinState{on: true, at: at, dir: dir}
}

func (h *hairpinState) end(in *interp, li int, end score.Frac) {
	if h.on {
		in.lines[li].hairs = append(in.lines[li].hairs, hairpin{at: h.at, end: end, dir: h.dir})
	}
	h.on = false
}

// close ends a voice's hairpin left open: at the next dynamic, or where
// the voice ends.
func (h *hairpinState) close(in *interp, li int, voiceEnd score.Frac) {
	if h.on {
		in.lines[li].hairs = append(in.lines[li].hairs, hairpin{at: h.at, end: voiceEnd, dir: h.dir, open: true})
	}
	h.on = false
}

// fpoint is a breakpoint of a level curve in performance time.
type fpoint struct {
	at score.Frac
	v  float32
}

// fcurve is a level curve: linear between points, a step where two share
// a place (the later wins from there on), the ends held.
type fcurve []fpoint

// right is the level at x from x on.
func (c fcurve) right(x score.Frac) float32 {
	i := sort.Search(len(c), func(i int) bool { return x.Less(c[i].at) })
	return c.between(i, x)
}

// left is the level just before x.
func (c fcurve) left(x score.Frac) float32 {
	i := sort.Search(len(c), func(i int) bool { return !c[i].at.Less(x) })
	return c.between(i, x)
}

// between interpolates between points i-1 and i.
func (c fcurve) between(i int, x score.Frac) float32 {
	switch {
	case len(c) == 0:
		return score.Dynamics[score.DefaultDynamic].Level
	case i == 0:
		return c[0].v
	case i == len(c):
		return c[len(c)-1].v
	}
	a, b := c[i-1], c[i]
	if !a.at.Less(b.at) {
		return a.v
	}
	t := x.Sub(a.at).Div(b.at.Sub(a.at)).Float()
	return a.v + (b.v-a.v)*float32(t)
}

// ticks is the curve in performance ticks.
func (c fcurve) ticks() []CurvePoint {
	out := make([]CurvePoint, len(c))
	for i, p := range c {
		out[i] = CurvePoint{Tick: p.at.TicksFloat(), Value: p.v}
	}
	return out
}

// hold replaces the curve with v over [a, b).
func (c fcurve) hold(a, b score.Frac, v float32) fcurve {
	out := make(fcurve, 0, len(c)+4)
	for _, p := range c {
		if p.at.Less(a) {
			out = append(out, p)
		}
	}
	out = append(out, fpoint{a, c.left(a)}, fpoint{a, v}, fpoint{b, v}, fpoint{b, c.right(b)})
	for _, p := range c {
		if b.Less(p.at) {
			out = append(out, p)
		}
	}
	return out
}

// lineCurves are a line's written and effective levels.
type lineCurves struct {
	written, effective fcurve
	// moments are the dynamics that act on the notes at their place only:
	// sfz and the first level of fp.
	moments []dynMark
}

// dynamics builds every line's curves from the marks the walk collected.
func (in *interp) dynamics() {
	in.dyn = make([]lineCurves, len(in.s.Lines))
	for li := range in.s.Lines {
		lm := &in.lines[li]
		sort.SliceStable(lm.dyns, func(i, j int) bool { return lm.dyns[i].at.Less(lm.dyns[j].at) })
		// The same mark at one place in two staves (a piano's) is one.
		var marks, moments []dynMark
		for _, d := range lm.dyns {
			if n := len(marks); n > 0 && marks[n-1].at.Eq(d.at) && marks[n-1].name == d.name {
				continue
			}
			if d.dyn.Momentary {
				moments = append(moments, d)
				continue
			}
			if d.dyn.After > 0 {
				moments = append(moments, d)
			}
			marks = append(marks, d)
		}
		w := in.writtenDynamics(marks, lm.hairs)
		lc := lineCurves{written: w, effective: w, moments: moments}
		for _, o := range in.s.Lines[li].Overrides {
			a, b := in.s.PosFrac(o.From, in.starts), in.s.PosFrac(o.To, in.starts)
			for _, r := range in.passesOver(a, b) {
				lc.effective = lc.effective.hold(r[0], r[1], o.Level)
			}
		}
		in.dyn[li] = lc
	}
}

// passesOver maps a written span to the performance spans it plays as,
// one per pass, joining the bars that play one after another.
func (in *interp) passesOver(a, b score.Frac) [][2]score.Frac {
	var out [][2]score.Frac
	for _, pb := range in.order {
		ws := in.starts[pb.bar-1]
		we := ws.Add(pb.length)
		lo, hi := score.MaxFrac(a, ws), score.MinFrac(b, we)
		if !lo.Less(hi) {
			continue
		}
		pa, pz := pb.start.Add(lo.Sub(ws)), pb.start.Add(hi.Sub(ws))
		if n := len(out); n > 0 && out[n-1][1].Eq(pa) {
			out[n-1][1] = pz
			continue
		}
		out = append(out, [2]score.Frac{pa, pz})
	}
	return out
}

// writtenDynamics sweeps a line's marks and hairpins in time order into
// its level curve (see the file comment).
func (in *interp) writtenDynamics(marks []dynMark, hairs []hairpin) fcurve {
	sort.SliceStable(hairs, func(i, j int) bool { return hairs[i].at.Less(hairs[j].at) })
	level := score.Dynamics[score.DefaultDynamic].Level
	c := fcurve{{score.Frac{Num: 0, Den: 1}, level}}
	push := func(at score.Frac, v float32) { c = append(c, fpoint{at, v}) }
	// The hairpins are resolved first: open ones end at the next mark.
	var hs []hairpin
	for _, h := range hairs {
		if h.open {
			for _, m := range marks {
				if h.at.Less(m.at) {
					if m.at.Less(h.end) {
						h.end = m.at
					}
					break
				}
			}
		}
		if !h.at.Less(h.end) {
			continue
		}
		// The same hairpin in two staves (a piano's) is one.
		dup := false
		for _, o := range hs {
			if o.at.Eq(h.at) && o.end.Eq(h.end) && o.dir == h.dir {
				dup = true
			}
		}
		if !dup {
			hs = append(hs, h)
		}
	}
	type ramp struct {
		start, end score.Frac
		from       float32
		dir        int
	}
	var r *ramp
	finish := func() {
		// A ramp that meets no mark moves one step and stays there.
		level = score.StepDynamic(level, r.dir)
		push(r.end, level)
		r = nil
	}
	mi, hi := 0, 0
	for mi < len(marks) || hi < len(hs) {
		// Marks before hairpins at one place, so a hairpin starts from
		// the mark under it.
		isMark := hi >= len(hs) || mi < len(marks) && !hs[hi].at.Less(marks[mi].at)
		at := score.Frac{}
		if isMark {
			at = marks[mi].at
		} else {
			at = hs[hi].at
		}
		if r != nil && r.end.Less(at) {
			finish()
		}
		if isMark {
			m := marks[mi]
			mi++
			if r != nil {
				// The ramp reaches the mark and goes on from it.
				push(m.at, m.dyn.Level)
				if !m.at.Less(r.end) {
					r = nil
				}
			} else {
				push(m.at, level)
				push(m.at, m.dyn.Level)
			}
			level = m.dyn.Level
			if m.dyn.After > 0 {
				level = m.dyn.After
				push(m.at, level)
			}
			if r != nil {
				r.start, r.from = m.at, level
			}
			continue
		}
		h := hs[hi]
		hi++
		if r != nil {
			// A hairpin starting inside another (another voice's) ends
			// the first where it starts, as far along its step as it got.
			to := score.StepDynamic(r.from, r.dir)
			k := h.at.Sub(r.start).Div(r.end.Sub(r.start)).Float()
			level = r.from + (to-r.from)*float32(k)
			r = nil
		}
		push(h.at, level)
		r = &ramp{start: h.at, end: h.end, from: level, dir: h.dir}
	}
	if r != nil {
		finish()
	}
	return c
}

// velocity is a note's velocity and accent: the effective level where it
// starts, times its articulation's accent and any momentary dynamic on
// the line there (sfz, fp's first level).
func (in *interp) velocity(r *rawNote) (float32, float32) {
	lc := &in.dyn[r.line]
	e := lc.effective.right(r.velAt)
	acc := r.accent
	if acc <= 0 {
		acc = 1
	}
	v := e * acc
	for _, m := range lc.moments {
		if m.at.Eq(r.velAt) {
			if u := lc.written.right(r.velAt); e == u || u <= 0 {
				v = m.dyn.Level * acc
			} else {
				v = e * m.dyn.Level / u * acc
			}
		}
	}
	v = min(max(v, 0), 1)
	if e > 0 {
		acc = v / e
	} else {
		acc = 1
	}
	return v, acc
}

// WrittenDynamics is every line's dynamics over the score as written —
// straight through, each bar once, ticks counted from the top of the
// written score (Frac.Ticks of a written position) — which is what the
// editor's dynamics lane draws under a staff: the curve the marks give and
// the one that plays.
func WrittenDynamics(s *score.Score) ([]LineDynamics, error) {
	p, err := Interpret(s, Options{NoRepeats: true})
	if err != nil {
		return nil, err
	}
	return p.Dynamics, nil
}
