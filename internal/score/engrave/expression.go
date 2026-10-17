package engrave

// expression.go — what stands at one note: articulations, fermatas,
// ornaments, tremolo strokes and arpeggio lines, and the order everything
// around the notes is placed in (drawExpression).
//
// Articulations go on the notehead's side, away from the stem — in
// multi-voice music on the stem's side, up for voices 1 and 3 and down for
// 2 and 4, at the stem's end. The staccato dot, the staccatissimo wedge,
// the tenuto line and portato sit nearest the note and, inside the staff,
// in a space (never on a line); an accent or a marcato goes beyond them and
// outside the staff. Each is centred on the notehead (on the stem, at the
// stem's end).
//
// Fermatas and ornaments (trill, mordents, turns, with the accidental their
// auxiliary note takes above or below them) go over the staff, clear of
// everything already there — the notes, the articulations, the slurs; a
// lower voice's below it. Tremolo strokes cross the stem between the head
// and the tip, or stand over a stemless note; a two-note tremolo's strokes
// run between the two notes like beams that do not touch a half note's
// stems. An arpeggio is a wavy line left of the chord and its accidentals
// (spacing leaves the room, arrangeColumn), with an arrowhead for `up` or
// `down`. Jazz slides stand beside the top note — a scoop or a plop left of
// the accidentals, a doit or a fall right of the dots — and a jazz bend
// over it.

import (
	"math"

	"foundry/internal/score"
	"foundry/internal/score/glyph"
)

// drawExpression places everything around one staff's notes, once the
// notes, beams, ties and tuplets are drawn: each thing goes clear of what
// came before it, so the order is the order of closeness — articulations
// and tremolos, glissandi, slurs, fermatas and ornaments, then text and
// dynamics, ottava lines and pedal lines furthest out.
func (e *engraver) drawExpression(sc *staffCtx) {
	endX := e.bars[len(e.bars)-1].x1 + e.barlineWidth(e.barlineBefore(len(e.bars)))
	sc.sky = e.skyOf(sc.items, -8, endX+8)
	for _, sb := range sc.bars {
		for _, v := range sb.voices {
			for _, x := range v {
				if x.space || x.col == nil || x.grace {
					continue
				}
				e.drawArticulations(sc, x)
				e.drawJazz(sc, x)
				e.drawTremolo(sc, x)
			}
		}
	}
	e.drawGlissandi(sc)
	e.drawSlurs(sc)
	for _, sb := range sc.bars {
		for _, v := range sb.voices {
			for _, x := range v {
				if x.space || x.col == nil || x.grace {
					continue
				}
				e.drawOrnaments(sc, x)
			}
		}
	}
	e.drawLyrics(sc)
	e.drawDynamics(sc)
	e.drawStaffText(sc)
	e.drawOttavas(sc)
	e.drawPedal(sc)
	if sc.first {
		e.drawChordSymbols(sc)
	}
}

// put adds an item to a staff and its sky; with an event it also grows the
// event's box (what the editor hit-tests and reveals).
func (e *engraver) put(sc *staffCtx, x *ev, it Item) Rect {
	r := itemBounds(e.f, &it)
	if x != nil {
		e.add(sc, x, it)
	} else {
		e.addStaff(sc, it)
	}
	if sc.sky != nil {
		sc.sky.add(r)
	}
	return r
}

// centred is a glyph placed so its box's centre is at (cx, cy).
func (e *engraver) centred(role Role, ref Ref, name string, cx, cy, size float64) Item {
	b, _ := e.f.Box(name)
	return e.glyphItem(role, ref, name, cx-(b.Min.X+b.Max.X)/2*size, cy-(b.Min.Y+b.Max.Y)/2*size, size)
}

// markAbove is the side a voice's marks go on in multi-voice music, and
// whether the staff is multi-voice there at all.
func markAbove(x *ev) (above, forced bool) {
	return x.voice%2 == 0, x.forced
}

// ---------- articulations ----------

// articulation is one articulation's glyphs (above, below) and whether it
// may sit inside the staff (in a space).
type articulation struct {
	name         string
	above, below string
	inside       bool
	order        int // nearest the note first
}

var articulations = []articulation{
	{"stacc", "articStaccatoAbove", "articStaccatoBelow", true, 0},
	{"staccmo", "articStaccatissimoAbove", "articStaccatissimoBelow", true, 0},
	{"portato", "articTenutoStaccatoAbove", "articTenutoStaccatoBelow", true, 0},
	{"ten", "articTenutoAbove", "articTenutoBelow", true, 1},
	{"acc", "articAccentAbove", "articAccentBelow", false, 2},
	{"marc", "articMarcatoAbove", "articMarcatoBelow", false, 3},
	// Open and stopped: a horn's "o" and "+", a hi-hat's.
	{"open", "brassMuteOpen", "brassMuteOpen", false, 4},
	{"stopped", "brassMuteClosed", "brassMuteClosed", false, 4},
}

// ---------- jazz slides ----------

// jazzGap is the room between a note (its accidentals, its dots) and a
// jazz slide beside it.
const jazzGap = 0.15

// jazzGlyphs are the slides beside an event: in (a scoop or a plop, left
// of it) and out (a doit or a fall, right of it), "" for none.
func jazzGlyphs(x *ev) (in, out string) {
	if x.e == nil || len(x.notes) == 0 {
		return "", ""
	}
	for _, m := range x.e.Marks {
		switch m.Name {
		case "scoop":
			in = "brassScoop"
		case "plop":
			if in == "" {
				in = "brassPlop"
			}
		case "doit":
			out = "brassDoitShort"
			if m.Value == "long" {
				out = "brassDoitMedium"
			}
		case "fall":
			if out == "" {
				out = "brassFallLipShort"
				if m.Value == "long" {
					out = "brassFallLipMedium"
				}
			}
		}
	}
	return in, out
}

// drawJazz draws an event's jazz marks at its top note: a scoop rising
// into the head from below-left, a plop dropping into it from above-left,
// a doit rising and a fall dropping from its right, and a bend's dip over
// it, above the stem and whatever else is there.
func (e *engraver) drawJazz(sc *staffCtx, x *ev) {
	if x.col == nil || len(x.notes) == 0 {
		return
	}
	top := x.notes[len(x.notes)-1]
	y := top.y
	if x.e.Has("bend") {
		b, _ := e.f.Box("brassBend")
		cx := x.x + top.dx + x.headW/2
		w, h := b.W()*x.size, b.H()*x.size
		cy := math.Min(y-0.75-h/2, sc.sky.topIn(cx-w/2, cx+w/2, math.Inf(1))-0.25-h/2)
		e.put(sc, x, e.centred(RoleArticulation, x.ref, "brassBend", cx, cy, x.size))
	}
	in, out := jazzGlyphs(x)
	if in == "" && out == "" {
		return
	}
	place := func(g string, right bool, edge float64) {
		b, _ := e.f.Box(g)
		ox := edge - b.Min.X*x.size // left edge at edge
		if !right {
			ox = edge - b.Max.X*x.size // right edge at edge
		}
		var oy float64
		switch g {
		case "brassScoop", "brassFallLipShort", "brassFallLipLong":
			oy = y - 0.15 - b.Min.Y*x.size // hanging from the head's middle
		default:
			oy = y + 0.15 - b.Max.Y*x.size // standing on it
		}
		e.put(sc, x, e.glyphItem(RoleArticulation, x.ref, g, ox, oy, x.size))
	}
	if in != "" {
		place(in, false, x.col.x+x.jazzInX)
	}
	if out != "" {
		place(out, true, x.col.x+x.jazzOutX)
	}
}

// eventArticulations are an event's articulations, nearest first; also
// names a drum map draws with its notes (an open hi-hat's "o").
func eventArticulations(se *score.Event, also ...string) []articulation {
	var out []articulation
	for _, a := range articulations {
		has := se.Has(a.name)
		for _, n := range also {
			has = has || n == a.name
		}
		if has {
			out = append(out, a)
		}
	}
	// Nearest the note first, keeping the written order among equals.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].order < out[j-1].order; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// artSide is where an event's articulations go: above or below, and
// whether at the stem's end.
func artSide(x *ev) (above, stemEnd bool) {
	if a, forced := markAbove(x); forced {
		return a, x.hasStem && x.up == a
	}
	return !x.up, false
}

// drawArticulations draws an event's articulations and records how far
// they reach (artTop/artBot, infinite for none on that side) for slurs.
func (e *engraver) drawArticulations(sc *staffCtx, x *ev) {
	x.artTop, x.artBot = math.Inf(1), math.Inf(-1)
	if len(x.notes) == 0 {
		return
	}
	var kit []string
	for _, n := range x.notes {
		if n.art != "" {
			kit = append(kit, n.art)
		}
	}
	arts := eventArticulations(x.e, kit...)
	if len(arts) == 0 {
		return
	}
	above, stemEnd := artSide(x)
	dir := 1.0 // down the page
	if above {
		dir = -1
	}
	cx := x.x + x.headW/2
	var edge float64
	switch {
	case stemEnd:
		cx = x.stemX
		edge = x.tipY
		if x.beam != nil {
			// Past the beam at the stem.
			edge = x.tipY + dir*(e.d.BeamThickness+float64(max(0, beamCount(x.base)-1))*beamStep)*x.size
		} else if x.base >= score.Eighth {
			edge = x.tipY + dir*0.3
		}
	case above:
		top := x.notes[len(x.notes)-1]
		cx = x.x + top.dx + x.headW/2
		edge = top.y - 0.5
	default:
		bot := x.notes[0]
		cx = x.x + bot.dx + x.headW/2
		edge = bot.y + 0.5
	}
	for i, a := range arts {
		g := a.below
		if above {
			g = a.above
		}
		b, _ := e.f.Box(g)
		h := b.H()
		gap := 0.3
		if i > 0 {
			gap = 0.2
		}
		cy := edge + dir*(gap+h/2)
		// Clear of the ink beside it too: a neighbour's low note under an
		// accent at a stem's end.
		w := b.W() * x.size
		if above {
			cy = math.Min(cy, sc.sky.topIn(cx-w/2, cx+w/2, math.Inf(1))-0.12-h/2)
		} else {
			cy = math.Max(cy, sc.sky.botIn(cx-w/2, cx+w/2, math.Inf(-1))+0.12+h/2)
		}
		inStaff := cy-h/2 < 4.2 && cy+h/2 > -0.2
		switch {
		case inStaff && a.inside:
			// In a space: the nearest one outward.
			sp := math.Floor(cy) + 0.5
			if above && sp > cy+1e-9 {
				sp--
			}
			if !above && sp < cy-1e-9 {
				sp++
			}
			cy = sp
		case inStaff:
			// Outside the staff.
			if above {
				cy = math.Min(cy, -0.35-h/2)
			} else {
				cy = math.Max(cy, 4.35+h/2)
			}
		}
		r := e.put(sc, x, e.centred(RoleArticulation, x.ref, g, cx, cy, x.size))
		if above {
			edge = r.Min.Y
			x.artTop = math.Min(x.artTop, r.Min.Y)
		} else {
			edge = r.Max.Y
			x.artBot = math.Max(x.artBot, r.Max.Y)
		}
	}
}

// ---------- tremolo ----------

// drawTremolo draws a note's tremolo strokes (trem=n) on its stem, and a
// two-note tremolo's (trem2=n) toward the next note.
func (e *engraver) drawTremolo(sc *staffCtx, x *ev) {
	if len(x.notes) == 0 {
		return
	}
	for _, mk := range x.e.Marks {
		switch mk.Name {
		case "trem":
			n, ok := score.TremoloStrokes(mk.Value)
			if !ok {
				continue
			}
			g := []string{"tremolo1", "tremolo2", "tremolo3", "tremolo4"}[n-1]
			b, _ := e.f.Box(g)
			var cx, cy float64
			if x.hasStem {
				cx = x.stemX
				base := e.stemBase(x)
				k := 0.5
				if x.base >= score.Eighth {
					k = 0.38 // clear of the flag or beams
				}
				cy = base + (x.tipY-base)*k
				// The strokes stay off the notehead.
				if x.up {
					cy = math.Min(cy, base-0.6-b.H()/2)
				} else {
					cy = math.Max(cy, base+0.6+b.H()/2)
				}
			} else {
				cx = x.x + x.headW/2
				if x.up {
					cy = x.notes[len(x.notes)-1].y - 0.9 - b.H()/2
				} else {
					cy = x.notes[0].y + 0.9 + b.H()/2
				}
			}
			e.put(sc, x, e.centred(RoleArticulation, x.ref, g, cx, cy, x.size))
		case "trem2":
			n, ok := score.TremoloStrokes(mk.Value)
			if !ok || x.nextEv == nil || x.nextEv.col == nil || len(x.nextEv.notes) == 0 {
				continue
			}
			e.drawTwoNoteTremolo(sc, x, x.nextEv, n)
		}
	}
}

// drawTwoNoteTremolo draws n strokes between two notes.
func (e *engraver) drawTwoNoteTremolo(sc *staffCtx, a, b *ev, n int) {
	th := e.d.BeamThickness * 0.9
	var xa, ya, xb, yb float64
	if a.hasStem && b.hasStem {
		mid := func(x *ev) float64 { base := e.stemBase(x); return base + (x.tipY-base)*0.55 }
		xa, ya, xb, yb = a.stemX, mid(a), b.stemX, mid(b)
		if a.base <= score.Half {
			// Strokes between a half note's stems do not touch them.
			xa, xb = xa+0.45, xb-0.45
		}
	} else {
		xa, xb = a.x+a.headW+0.4, b.x-0.4
		dy := -1.2
		if !a.up {
			dy = 1.2
		}
		ya, yb = a.notes[len(a.notes)-1].y+dy, b.notes[len(b.notes)-1].y+dy
		if !a.up {
			ya, yb = a.notes[0].y+dy, b.notes[0].y+dy
		}
	}
	if xb-xa < 0.6 {
		return
	}
	slope := (yb - ya) / (xb - xa)
	slope = math.Max(-0.3, math.Min(0.3, slope))
	ym := (ya + yb) / 2
	xm := (xa + xb) / 2
	total := float64(n-1)*beamStep + th
	for k := 0; k < n; k++ {
		off := -total/2 + float64(k)*beamStep
		y0 := ym + off + slope*(xa-xm)
		y1 := ym + off + slope*(xb-xm)
		e.put(sc, a, polyItem(RoleBeam, a.ref, Point{X: xa, Y: y0}, Point{X: xb, Y: y1}, Point{X: xb, Y: y1 + th}, Point{X: xa, Y: y0 + th}))
	}
}

// ---------- arpeggios ----------

// arpWidth is the room an arpeggio line takes left of a chord, and
// arpGap its distance from what it is beside.
const (
	arpWidth = 0.5
	arpGap   = 0.25
)

// arpeggio reports an event's arpeggio and its arrow ("", "up", "down").
func arpeggio(x *ev) (bool, string) {
	for _, m := range x.e.Marks {
		if m.Name == "arp" {
			return len(x.notes) > 1 || len(x.notes) == 1, m.Value
		}
	}
	return false, ""
}

// drawArpeggio draws a chord's arpeggio line at its reserved place.
func (e *engraver) drawArpeggio(sc *staffCtx, x *ev) {
	ok, arrow := arpeggio(x)
	if !ok || x.col == nil || len(x.notes) == 0 {
		return
	}
	cx := x.col.x + x.arpX + arpWidth/2
	top, bot := x.notes[len(x.notes)-1].y-0.6, x.notes[0].y+0.6
	if bot-top < 1.4 {
		m := (top + bot) / 2
		top, bot = m-0.7, m+0.7
	}
	y0, y1 := top, bot
	if arrow == "up" {
		y0 += 0.45
	}
	if arrow == "down" {
		y1 -= 0.45
	}
	path := wavyPath(Point{X: cx, Y: y1}, Point{X: cx, Y: y0}, 0.16, 0.75, 0.14)
	e.add(sc, x, Item{Kind: ItemPath, Role: RoleArticulation, Ref: x.ref, Path: path})
	head := func(tipY, dir float64) {
		e.add(sc, x, polyItem(RoleArticulation, x.ref, Point{X: cx, Y: tipY}, Point{X: cx + 0.3, Y: tipY - dir*0.55}, Point{X: cx - 0.3, Y: tipY - dir*0.55}))
	}
	switch arrow {
	case "up":
		head(top, -1)
	case "down":
		head(bot, 1)
	}
}

// wavyPath is a filled wavy ribbon from p0 to p1: a sine of amplitude amp
// and wavelength wl, th thick — an arpeggio, a wavy glissando.
func wavyPath(p0, p1 Point, amp, wl, th float64) []glyph.Segment {
	dx, dy := p1.X-p0.X, p1.Y-p0.Y
	l := math.Hypot(dx, dy)
	if l == 0 {
		return nil
	}
	ux, uy := dx/l, dy/l
	nx, ny := -uy, ux
	n := max(8, int(math.Ceil(l/wl*12)))
	var up, down []Point
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n) * l
		w := amp * math.Sin(2*math.Pi*t/wl)
		cx, cy := p0.X+ux*t+nx*w, p0.Y+uy*t+ny*w
		up = append(up, Point{X: cx + nx*th/2, Y: cy + ny*th/2})
		down = append(down, Point{X: cx - nx*th/2, Y: cy - ny*th/2})
	}
	var segs []glyph.Segment
	for i, p := range up {
		op := glyph.LineTo
		if i == 0 {
			op = glyph.MoveTo
		}
		segs = append(segs, glyph.Segment{Op: op, P: [3]Point{p}})
	}
	for i := len(down) - 1; i >= 0; i-- {
		segs = append(segs, glyph.Segment{Op: glyph.LineTo, P: [3]Point{down[i]}})
	}
	return segs
}

// ---------- fermatas and ornaments ----------

// ornament is an ornament mark's glyph.
var ornamentGlyphs = map[string]string{
	"tr": "ornamentTrill", "mord": "ornamentMordent", "imord": "ornamentShortTrill",
	"turn": "ornamentTurn", "iturn": "ornamentTurnInverted",
}

// fermataGlyph is a fermata's glyph by its length.
func fermataGlyph(value string, above bool) string {
	name := "fermata"
	switch value {
	case "short":
		name = "fermataShort"
	case "long":
		name = "fermataLong"
	case "verylong":
		name = "fermataVeryLong"
	}
	if above {
		return name + "Above"
	}
	return name + "Below"
}

// drawOrnaments draws an event's fermata and ornaments over (or, for a
// lower voice in multi-voice music, under) the staff.
func (e *engraver) drawOrnaments(sc *staffCtx, x *ev) {
	above := true
	if a, forced := markAbove(x); forced {
		above = a
	}
	var cx float64
	switch {
	case x.barRest:
		g, _ := barRestGlyph(x.sb.bar)
		cx = x.x + e.f.Width(g)/2
	case x.rest:
		cx = x.x + x.restX + e.f.Width(restGlyph(x.base))/2
	default:
		cx = x.x + x.headW/2
	}
	place := func(role Role, g string, size float64) Rect {
		b, _ := e.f.Box(g)
		w := b.W() * size
		var cy float64
		if above {
			top := sc.sky.topIn(cx-w/2, cx+w/2, 0)
			cy = math.Min(top-0.45, -0.6) - b.H()*size/2
		} else {
			bot := sc.sky.botIn(cx-w/2, cx+w/2, 4)
			cy = math.Max(bot+0.45, 4.6) + b.H()*size/2
		}
		return e.put(sc, x, e.centred(role, x.ref, g, cx, cy, size))
	}
	for _, mk := range x.e.Marks {
		g, ok := ornamentGlyphs[mk.Name]
		if !ok || len(x.notes) == 0 {
			continue
		}
		up, low, hasUp, hasLow, valid := score.OrnamentAccidentals(mk.Value)
		if !valid {
			hasUp, hasLow = false, false
		}
		// The lower accidental hangs between the ornament and the staff.
		if hasLow && above {
			e.placeOrnamentAcc(sc, x, cx, low, above)
		}
		if hasUp && !above {
			e.placeOrnamentAcc(sc, x, cx, up, above)
		}
		place(RoleOrnament, g, 1)
		if hasUp && above {
			e.placeOrnamentAcc(sc, x, cx, up, above)
		}
		if hasLow && !above {
			e.placeOrnamentAcc(sc, x, cx, low, above)
		}
	}
	for _, mk := range x.e.Marks {
		if mk.Name == "ferm" {
			place(RoleFermata, fermataGlyph(mk.Value, above), 1)
		}
	}
}

// placeOrnamentAcc puts an ornament's small accidental beyond what is there.
func (e *engraver) placeOrnamentAcc(sc *staffCtx, x *ev, cx float64, alter int, above bool) {
	const size = 0.6
	g := accGlyph(alter)
	b, _ := e.f.Box(g)
	w := b.W() * size
	var cy float64
	if above {
		cy = math.Min(sc.sky.topIn(cx-w/2, cx+w/2, 0)-0.25, -0.6) - b.H()*size/2
	} else {
		cy = math.Max(sc.sky.botIn(cx-w/2, cx+w/2, 4)+0.25, 4.6) + b.H()*size/2
	}
	e.put(sc, x, e.centred(RoleOrnament, x.ref, g, cx, cy, size))
}
