package engrave

// beam.go — stems, flags and beams.
//
// Beam groups follow the meter (beamUnits): a simple meter's beats, two
// beats of eighths together in 4/4 (never across the middle of the bar),
// the dotted beats of a compound meter, the groups of an additive one
// (3+2+2/8), and for an odd count of eighths a grouping that reads (5/8 as
// 3+2, 7/8 as 2+2+3). A tuplet's notes beam as one group of their own.
// Rests, quarters and longer break a group; one note left alone gets a
// flag.
//
// A group's stems all go one way: the voice's in multi-voice music,
// otherwise away from the note furthest from the middle line. The beam
// slopes with the line from its first note to its last, by a quarter space
// for a second up to a space for a fifth or more, never more steeply than a
// quarter space a space, and lies flat when the notes inside reach further
// toward it than both ends; close notes slope less (maxBeamSlope). It then sits at a standard stem's length from
// the note nearest it, reaches the middle line from notes far outside the
// staff, and inside the staff is moved outward until it sits on, hangs from
// or straddles a line at both ends (no white wedges against the lines).
// Secondary beams join the notes that have them — in a compound meter the
// 16ths' beam broken at each eighth of the dotted beat — and a lone note's
// partial beam points to the note it belongs with.

import (
	"math"

	"foundry/internal/score"
)

// beamGroup is a run of notes under one beam.
type beamGroup struct {
	evs   []*ev
	up    bool
	grace bool
	size  float64
	// The primary beam's outer edge (the stem tips): y = y0 + slope·(x-x0).
	x0, y0, slope float64
	// knee is a beam joining notes on both staves of a grand staff, kneeY
	// the other staff's top line (cross.go).
	knee  bool
	kneeY float64
}

func (g *beamGroup) yAt(x float64) float64 { return g.y0 + g.slope*(x-g.x0) }

// maxBeamSlope is the steepest a beam rises, in spaces a space: about 22°.
const maxBeamSlope = 0.4

// beamCount is how many beams a value has.
func beamCount(b score.DurBase) int { return max(0, int(b-score.Quarter)) }

// flagGlyph is the flag for a value.
func flagGlyph(b score.DurBase, up bool) string {
	names := []string{"flag8th", "flag16th", "flag32nd", "flag64th", "flag128th", "flag256th"}
	i := min(max(int(b-score.Eighth), 0), len(names)-1)
	if up {
		return names[i] + "Up"
	}
	return names[i] + "Down"
}

// beamUnits are the lengths a meter's bar beams in, in order
// (score.Meter.BeamUnits).
func beamUnits(m score.Meter) []score.Frac { return m.BeamUnits() }

// beamable is a note that takes a beam.
func beamable(x *ev) bool { return len(x.notes) > 0 && x.base >= score.Eighth }

// beamVoice finds a voice's beam groups in one bar.
func (e *engraver) beamVoice(sb *staffBar, vi int, v []*ev) {
	bc := sb.bar
	units := beamUnits(bc.meter)
	// A pickup is the end of a bar: its beats are the meter's last ones.
	shift := bc.meter.Length().Sub(bc.length)
	if shift.Sign() < 0 || bc.bar != 1 {
		shift = score.Frac{}
	}
	unitOf := func(t score.Frac) int {
		t = t.Add(shift)
		at := score.Frac{}
		for i, u := range units {
			at = at.Add(u)
			if t.Less(at) {
				return i
			}
		}
		return len(units)
	}
	type key struct{ unit, tuplet int }
	var groups [][]*ev
	var cur []*ev
	var ck key
	flush := func() {
		if len(cur) > 1 {
			groups = append(groups, cur)
		}
		cur = nil
	}
	var graceRun []*ev
	flushGraces := func() {
		if len(graceRun) > 1 {
			g := &beamGroup{evs: graceRun, grace: true, size: graceSize, up: graceRun[0].up}
			for _, x := range graceRun {
				x.beam, x.up = g, g.up
			}
			sb.beams = append(sb.beams, g)
		}
		graceRun = nil
	}
	for _, x := range v {
		if x.grace {
			if beamable(x) && !x.e.Has("nobeam") {
				graceRun = append(graceRun, x)
			} else {
				flushGraces()
			}
			continue
		}
		flushGraces()
		if !beamable(x) || x.e.Has("nobeam") {
			flush()
			continue
		}
		k := key{unit: unitOf(x.onset)}
		if len(x.tuplets) > 0 {
			k = key{unit: -1, tuplet: x.tuplets[len(x.tuplets)-1]}
		}
		if len(cur) > 0 && k != ck {
			flush()
		}
		cur, ck = append(cur, x), k
	}
	flush()
	flushGraces()

	// 4/4: two beats of plain eighths beam together, either half of the bar.
	if bc.meter.Den == 4 && len(bc.meter.Groups) == 1 && bc.meter.Groups[0] == 4 && shift.IsZero() {
		groups = mergeHalves(groups, unitOf)
	}
	for _, evs := range groups {
		g := &beamGroup{evs: evs, size: 1}
		g.up = e.groupUp(evs, vi, sb.forced)
		if g.kneed() {
			// Across the staves (cross.go): each note's own direction.
			for _, x := range evs {
				x.beam = g
			}
			sb.beams = append(sb.beams, g)
			continue
		}
		for _, x := range evs {
			x.beam, x.up = g, g.up
		}
		sb.beams = append(sb.beams, g)
	}
}

// mergeHalves joins two beat groups of exactly two plain eighths each when
// they make a half of a 4/4 bar.
func mergeHalves(groups [][]*ev, unitOf func(score.Frac) int) [][]*ev {
	plain := func(g []*ev) bool {
		if len(g) != 2 {
			return false
		}
		for _, x := range g {
			if x.base != score.Eighth || x.dots != 0 || len(x.tuplets) > 0 {
				return false
			}
		}
		return !g[0].onset.Add(g[0].actual).Less(g[1].onset) && g[1].onset.Sub(g[0].onset).Eq(score.F(1, 8))
	}
	var out [][]*ev
	for i := 0; i < len(groups); i++ {
		g := groups[i]
		if i+1 < len(groups) && plain(g) && plain(groups[i+1]) {
			u, w := unitOf(g[0].onset), unitOf(groups[i+1][0].onset)
			if (u == 0 || u == 2) && w == u+1 && g[0].onset.Eq(score.F(int64(u), 4)) {
				out = append(out, append(append([]*ev(nil), g...), groups[i+1]...))
				i++
				continue
			}
		}
		out = append(out, g)
	}
	return out
}

// groupUp is a beam group's stem direction.
func (e *engraver) groupUp(evs []*ev, voice int, forced bool) bool {
	for _, x := range evs {
		if x.e.Has("up") {
			return true
		}
		if x.e.Has("down") {
			return false
		}
	}
	if forced {
		return voice%2 == 0
	}
	var ps []int
	for _, x := range evs {
		for _, n := range x.notes {
			ps = append(ps, n.pos)
		}
	}
	return autoUp(ps)
}

// ---------- geometry ----------

// stemX is the centre of an event's stem.
func (e *engraver) stemX(x *ev) float64 {
	t := e.d.StemThickness * x.size
	g := x.head
	if x.up {
		a, _ := e.f.Anchor(g, "stemUpSE")
		return x.x + a.X*x.size - t/2
	}
	a, _ := e.f.Anchor(g, "stemDownNW")
	return x.x + a.X*x.size + t/2
}

// stemBase is where an event's stem leaves its far notehead.
func (e *engraver) stemBase(x *ev) float64 {
	g := x.head
	if x.up {
		a, _ := e.f.Anchor(g, "stemUpSE")
		return x.notes[0].y + a.Y*x.size
	}
	a, _ := e.f.Anchor(g, "stemDownNW")
	return x.notes[len(x.notes)-1].y + a.Y*x.size
}

// flagExtra is how much longer a stem gets so a flag of three or more
// hooks clears the notehead as a 16th's does.
func (e *engraver) flagExtra(b score.DurBase, up bool) float64 {
	if b < score.D32 {
		return 0
	}
	if up {
		a16, _ := e.f.Anchor("flag16thUp", "stemUpNW")
		an, _ := e.f.Anchor(flagGlyph(b, true), "stemUpNW")
		return math.Max(0, a16.Y-an.Y)
	}
	a16, _ := e.f.Anchor("flag16thDown", "stemDownSW")
	an, _ := e.f.Anchor(flagGlyph(b, false), "stemDownSW")
	return math.Max(0, an.Y-a16.Y)
}

// outwardStem is the stem length of a note in multi-voice music whose
// stem points away from the staff from a note outside it — an upper
// voice's high note, a lower voice's low one: such stems are shortened
// (Gould: to two and a half spaces, a space under the standard), keeping
// the voices' extremes close to the staff.
const outwardStem = 2.5

// stemLen is the standard stem for an event's direction: shorter for an
// outward stem from a note outside the staff in multi-voice music.
func stemLen(x *ev, far float64) float64 {
	if x.forced && !x.grace && (x.up && far < -0.25 || !x.up && far > 4.25) {
		return outwardStem
	}
	return stemLength
}

// stemTip is an unbeamed stem's end: a standard length past the far note,
// longer for many-hooked flags, reaching the middle line.
func (e *engraver) stemTip(x *ev) float64 {
	far := x.notes[0].y
	if x.up {
		far = x.notes[len(x.notes)-1].y
	}
	l := stemLen(x, far-x.crossY)*x.size + e.flagExtra(x.base, x.up)*x.size
	mid := midY(x)
	if x.up {
		tip := x.notes[len(x.notes)-1].y - l
		if !x.grace && tip > mid {
			tip = mid
		}
		return tip
	}
	tip := x.notes[0].y + l
	if !x.grace && tip < mid {
		tip = mid
	}
	return tip
}

// layoutBeam fixes a group's beam line once x is known.
func (e *engraver) layoutBeam(g *beamGroup) {
	evs := g.evs
	s := g.size
	xs := make([]float64, len(evs))
	ref := make([]float64, len(evs))
	maxB := 0
	for i, x := range evs {
		xs[i] = e.stemX(x)
		if g.up {
			ref[i] = x.notes[len(x.notes)-1].y
		} else {
			ref[i] = x.notes[0].y
		}
		maxB = max(maxB, beamCount(x.base))
	}
	n := len(evs)
	g.x0 = xs[0]
	dx := xs[n-1] - xs[0]
	if g.knee {
		e.layoutKnee(g, xs)
		return
	}
	// A group written wholly on the other staff reckons by that staff.
	cy := evs[0].crossY

	// The slope: by the interval from first to last, flat when the inner
	// notes reach past both ends toward the beam.
	slope := 0.0
	dy := ref[n-1] - ref[0] // y down: positive falls
	concave := false
	for i := 1; i < n-1; i++ {
		if g.up && ref[i] < math.Min(ref[0], ref[n-1]) || !g.up && ref[i] > math.Max(ref[0], ref[n-1]) {
			concave = true
		}
	}
	if dx > 0 && dy != 0 && !concave {
		steps := math.Abs(dy) * 2 // staff positions
		mag := 0.25 * math.Min(steps, 4)
		mag = math.Min(mag, math.Floor(dx*maxBeamSlope*4)/4)
		slope = math.Copysign(mag, dy) / dx
	}
	g.slope = slope

	// The note nearest the beam gets a standard stem (an outward one in
	// multi-voice music shorter: stemLen); more beams than two lengthen it.
	std, outward := stemLength, true
	for i, x := range evs {
		outward = outward && stemLen(x, ref[i]) == outwardStem
	}
	if outward {
		std = outwardStem
	}
	l := (std + beamStep*math.Max(0, float64(maxB-2))) * s
	if g.up {
		y0 := math.Inf(1)
		for i := range evs {
			y0 = math.Min(y0, ref[i]-l-slope*(xs[i]-g.x0))
		}
		g.y0 = y0
	} else {
		y0 := math.Inf(-1)
		for i := range evs {
			y0 = math.Max(y0, ref[i]+l-slope*(xs[i]-g.x0))
		}
		g.y0 = y0
	}
	if g.grace {
		return
	}
	// Reach the middle line from far outside the staff.
	ya, yb := g.yAt(xs[0]), g.yAt(xs[n-1])
	if g.up && math.Max(ya, yb) > 2+cy {
		g.y0 -= math.Max(ya, yb) - 2 - cy
	}
	if !g.up && math.Min(ya, yb) < 2+cy {
		g.y0 += 2 + cy - math.Min(ya, yb)
	}
	// Quantise against the staff lines, moving outward.
	dir := -1.0
	if !g.up {
		dir = 1
	}
	g.y0 -= cy
	if g.up {
		g.y0 = math.Floor(g.y0*4+1e-6) / 4
	} else {
		g.y0 = math.Ceil(g.y0*4-1e-6) / 4
	}
	base := g.y0
	for k := 0; k <= 4; k++ {
		g.y0 = base + dir*0.25*float64(k)
		if beamEndOK(g.yAt(xs[0]), g.up) && beamEndOK(g.yAt(xs[n-1]), g.up) {
			g.y0 += cy
			return
		}
	}
	g.y0 = base + cy
}

// beamEndOK reports a beam end that meets the staff lines cleanly: its
// outer edge at y sits on a line, hangs from one or straddles one, or is
// outside the staff.
func beamEndOK(y float64, up bool) bool {
	if y < -0.3 || y > 4.3 {
		return true
	}
	f := y - math.Floor(y)
	near := func(v float64) bool { return math.Abs(f-v) < 0.02 || math.Abs(f-v+1) < 0.02 }
	if up {
		// Outer edge on top: hang (0), sit (.5: the bottom edge on the
		// line below), straddle (.75).
		return near(0) || near(0.5) || near(0.75)
	}
	return near(0) || near(0.25) || near(0.5)
}

// drawBeam draws a group's stems and beams.
func (e *engraver) drawBeam(sc *staffCtx, g *beamGroup) {
	e.layoutBeam(g)
	s := g.size
	t := e.d.StemThickness * s
	th := e.d.BeamThickness * s
	step := beamStep * s
	xs := make([]float64, len(g.evs))
	for i, x := range g.evs {
		xs[i] = e.stemX(x)
		x.stemX = xs[i]
		x.tipY = g.yAt(xs[i])
		e.add(sc, x, Item{Kind: ItemLine, Role: RoleStem, Ref: x.ref, P: Point{X: xs[i], Y: e.stemBase(x)}, Q: Point{X: xs[i], Y: x.tipY}, Width: t})
	}
	maxB := 0
	for _, x := range g.evs {
		maxB = max(maxB, beamCount(x.base))
	}
	quad := func(x *ev, xa, xb float64, level int) {
		off := float64(level) * step
		var a0, b0 float64 // the edge nearer the tips
		if g.up {
			a0, b0 = g.yAt(xa)+off, g.yAt(xb)+off
			e.add(sc, x, polyItem(RoleBeam, x.ref, Point{X: xa, Y: a0}, Point{X: xb, Y: b0}, Point{X: xb, Y: b0 + th}, Point{X: xa, Y: a0 + th}))
		} else {
			a0, b0 = g.yAt(xa)-off, g.yAt(xb)-off
			e.add(sc, x, polyItem(RoleBeam, x.ref, Point{X: xa, Y: a0 - th}, Point{X: xb, Y: b0 - th}, Point{X: xb, Y: b0}, Point{X: xa, Y: a0}))
		}
	}
	n := len(g.evs)
	quad(g.evs[0], xs[0]-t/2, xs[n-1]+t/2, 0)
	// In a compound meter the 16ths' beam breaks at each eighth of the
	// dotted beat, so the beat's three eighths read under one primary beam
	// (Gould: secondary beams show the beat's subdivisions).
	compound := !g.grace && g.evs[0].sb != nil && g.evs[0].sb.bar.meter.Compound()
	breakAt := func(level, j int) bool {
		if !compound || level != 1 {
			return false
		}
		x := g.evs[j]
		return x.tuplets == nil && onGrid(x.onset, score.F(1, 8))
	}
	for level := 1; level < maxB; level++ {
		for i := 0; i < n; {
			if beamCount(g.evs[i].base) <= level {
				i++
				continue
			}
			j := i
			for j+1 < n && beamCount(g.evs[j+1].base) > level && !breakAt(level, j+1) {
				j++
			}
			if j > i {
				quad(g.evs[i], xs[i]-t/2, xs[j]+t/2, level)
			} else {
				hook := 1.1 * s
				left := i == n-1
				if i > 0 && i < n-1 {
					unit := score.F(1, int64(1)<<uint(level+2)) // the value one level up
					left = !onGrid(g.evs[i].onset, unit)
				}
				if left {
					hook = math.Min(hook, 0.6*(xs[i]-xs[i-1]))
					quad(g.evs[i], xs[i]-hook, xs[i]+t/2, level)
				} else {
					hook = math.Min(hook, 0.6*(xs[i+1]-xs[i]))
					quad(g.evs[i], xs[i]-t/2, xs[i]+hook, level)
				}
			}
			i = j + 1
		}
	}
	if g.grace && g.evs[0].e.Grace == score.Acciaccatura {
		// The slash through the first stem of a beamed acciaccatura group.
		x := g.evs[0]
		yb := e.stemBase(x)
		yt := x.tipY
		ym := yt + (yb-yt)*0.45
		d := 0.9 * s
		dirY := 1.0
		if !g.up {
			dirY = -1
		}
		e.add(sc, x, Item{Kind: ItemLine, Role: RoleStem, Ref: x.ref, P: Point{X: xs[0] - d*0.6, Y: ym + d*0.5*dirY}, Q: Point{X: xs[0] + d*0.8, Y: ym - d*0.5*dirY}, Width: t})
	}
}

// onGrid reports a time on a multiple of unit.
func onGrid(t, unit score.Frac) bool { return t.Div(unit).IsInt() }

// drawStem draws an unbeamed stem and its flag (and an acciaccatura's
// slash).
func (e *engraver) drawStem(sc *staffCtx, x *ev) {
	s := x.size
	t := e.d.StemThickness * s
	sx := e.stemX(x)
	tip := e.stemTip(x)
	x.stemX, x.tipY = sx, tip
	e.add(sc, x, Item{Kind: ItemLine, Role: RoleStem, Ref: x.ref, P: Point{X: sx, Y: e.stemBase(x)}, Q: Point{X: sx, Y: tip}, Width: t})
	slashed := x.e.Grace == score.Acciaccatura
	if x.base >= score.Eighth {
		fg := flagGlyph(x.base, x.up)
		an := "stemDownSW"
		if x.up {
			an = "stemUpNW"
		}
		a, _ := e.f.Anchor(fg, an)
		ox, oy := sx-t/2-a.X*s, tip-a.Y*s
		e.add(sc, x, e.glyphItem(RoleFlag, x.ref, fg, ox, oy, s))
		if slashed {
			sw, ne := "graceNoteSlashSW", "graceNoteSlashNE"
			if !x.up {
				sw, ne = "graceNoteSlashNW", "graceNoteSlashSE"
			}
			// Bravura gives the slash's ends only on the eighth's flag. A
			// shorter grace takes the eighth's slash moved toward the
			// notehead by the extra flags' height, so it crosses the stem
			// and the last flag the way the eighth's crosses its one.
			p, ok1 := e.f.Anchor(fg, sw)
			q, ok2 := e.f.Anchor(fg, ne)
			dy := 0.0
			if !ok1 || !ok2 {
				f8 := flagGlyph(score.Eighth, x.up)
				p, ok1 = e.f.Anchor(f8, sw)
				q, ok2 = e.f.Anchor(f8, ne)
				b8, _ := e.f.Box(f8)
				bf, _ := e.f.Box(fg)
				dy = math.Max(bf.H()-b8.H(), 0) * s
				if !x.up {
					dy = -dy
				}
			}
			if ok1 && ok2 {
				e.add(sc, x, Item{Kind: ItemLine, Role: RoleFlag, Ref: x.ref,
					P: Point{X: ox + p.X*s, Y: oy + p.Y*s + dy}, Q: Point{X: ox + q.X*s, Y: oy + q.Y*s + dy}, Width: t})
				slashed = false
			}
		}
	}
	if slashed {
		ym := tip + (e.stemBase(x)-tip)*0.4
		d := 0.9 * s
		dirY := 1.0
		if !x.up {
			dirY = -1
		}
		e.add(sc, x, Item{Kind: ItemLine, Role: RoleStem, Ref: x.ref, P: Point{X: sx - d*0.6, Y: ym + d*0.5*dirY}, Q: Point{X: sx + d*0.8, Y: ym - d*0.5*dirY}, Width: t})
	}
}
