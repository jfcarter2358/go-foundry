package engrave

import (
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"

	"foundry/internal/score"
)

// glyphsNamed are the list's glyph items of a name.
func glyphsNamed(l *List, name string) []Item {
	var out []Item
	for _, it := range l.Items {
		if it.Kind == ItemGlyph && it.Glyph == name {
			out = append(out, it)
		}
	}
	return out
}

// textsOf are the list's text items saying s.
func textsOf(l *List, s string) []Item {
	var out []Item
	for _, it := range l.Items {
		if it.Kind == ItemText && it.Text == s {
			out = append(out, it)
		}
	}
	return out
}

// staffY is a y as the staff counts it (its top line 0).
func staffY(l *List, y float64) float64 { return y - l.Staves[0].Top }

// boxCentre is an item's ink's middle.
func boxCentre(it Item) Point {
	b := ItemBounds(&it)
	return Point{X: (b.Min.X + b.Max.X) / 2, Y: (b.Min.Y + b.Max.Y) / 2}
}

func TestArticulationsSitInSpacesOnTheHeadsSide(t *testing.T) {
	// B4 (middle line) stems down: the dot above, in the space beyond the
	// one the head touches; G4 stems up: below, under the staff; C5 (a
	// space) a space above it.
	l := Engrave(one(t, "treble", "| qB4(stacc) qG4(stacc) qC5(stacc) qD5(ten)"), Options{})
	dots := glyphsNamed(l, "articStaccatoAbove")
	below := glyphsNamed(l, "articStaccatoBelow")
	if len(dots) != 2 || len(below) != 1 {
		t.Fatalf("%d staccatos above, %d below; want 2 and 1", len(dots), len(below))
	}
	for _, it := range append(dots, below...) {
		y := staffY(l, boxCentre(it).Y)
		if y > -0.2 && y < 4.2 {
			if f := y - math.Floor(y); math.Abs(f-0.5) > 1e-6 {
				t.Errorf("staccato inside the staff at %.3f: not in a space", y)
			}
		}
	}
	if y := staffY(l, boxCentre(dots[0]).Y); math.Abs(y-0.5) > 1e-6 {
		t.Errorf("B4's staccato at %.2f, want the top space 0.5", y)
	}
	if y := staffY(l, boxCentre(below[0]).Y); y < 4.2 {
		t.Errorf("G4's staccato at %.2f, want under the staff", y)
	}
	if y := staffY(l, boxCentre(dots[1]).Y); math.Abs(y-0.5) > 1e-6 {
		t.Errorf("C5's staccato at %.2f, want 0.5", y)
	}
	if len(glyphsNamed(l, "articTenutoAbove")) != 1 {
		t.Error("no tenuto over D5")
	}
	// Centred on the head.
	for _, eb := range l.Events {
		if eb.Ref.Event == 0 {
			if cx := boxCentre(dots[0]).X; math.Abs(cx-(eb.X+0.59)) > 0.1 {
				t.Errorf("staccato at x %.2f, head centre %.2f", cx, eb.X+0.59)
			}
		}
	}
}

func TestAccentsAndMarcatoGoOutsideTheStaff(t *testing.T) {
	l := Engrave(one(t, "treble", "| qB4(acc) qG4(acc) qC5(marc) qF4(stacc,acc)"), Options{})
	for _, name := range []string{"articAccentAbove", "articAccentBelow", "articMarcatoAbove"} {
		for _, it := range glyphsNamed(l, name) {
			b := ItemBounds(&it)
			top, bot := staffY(l, b.Min.Y), staffY(l, b.Max.Y)
			if top < 4 && bot > 0 {
				t.Errorf("%s at %.2f..%.2f is inside the staff", name, top, bot)
			}
		}
	}
	// stacc then acc: the dot nearer the note.
	dot := glyphsNamed(l, "articStaccatoBelow")
	acc := glyphsNamed(l, "articAccentBelow")
	if len(dot) != 1 || len(acc) != 2 {
		t.Fatalf("%d dots, %d accents below", len(dot), len(acc))
	}
	if boxCentre(dot[0]).Y > boxCentre(acc[1]).Y {
		t.Error("the accent is nearer the note than the staccato")
	}
}

func TestArticulationsAtTheStemInVoices(t *testing.T) {
	// Two voices: voice 1's marks over its stems, voice 2's under.
	l := Engrave(one(t, "treble", "| qE5(stacc) qE5(acc) hE5", "| qC4(stacc) qC4(acc) hC4"), Options{})
	for _, it := range l.Items {
		if it.Role != RoleArticulation {
			continue
		}
		y := staffY(l, boxCentre(it).Y)
		var tip float64
		for _, s := range itemsBy(l, RoleStem, it.Ref.Bar, it.Ref.Voice, it.Ref.Event) {
			tip = staffY(l, math.Min(s.P.Y, s.Q.Y))
			if it.Ref.Voice == 1 {
				tip = staffY(l, math.Max(s.P.Y, s.Q.Y))
			}
		}
		if it.Ref.Voice == 0 && y > tip {
			t.Errorf("voice 1's %s at %.2f is under its stem tip %.2f", it.Glyph, y, tip)
		}
		if it.Ref.Voice == 1 && y < tip {
			t.Errorf("voice 2's %s at %.2f is over its stem tip %.2f", it.Glyph, y, tip)
		}
	}
}

func TestDynamicsHangUnderTheStaffInOneRow(t *testing.T) {
	l := Engrave(one(t, "treble", "| qC5(p) qD5 qE5(cresc) qF5 | qG5(hairpin_end) qA5(f) qG5(sffz) qF5(niente)"), Options{})
	p, f := glyphsNamed(l, "dynamicPiano"), glyphsNamed(l, "dynamicForte")
	if len(p) != 1 || len(f) != 3 { // f, and sffz's two f's
		t.Fatalf("%d p, %d f glyphs", len(p), len(f))
	}
	if len(glyphsNamed(l, "dynamicNiente")) != 1 || len(glyphsNamed(l, "dynamicZ")) != 1 {
		t.Error("niente or sffz's z missing")
	}
	for _, it := range l.Items {
		if it.Role == RoleDynamic {
			if b := ItemBounds(&it); staffY(l, b.Min.Y) < 4.2 {
				t.Errorf("%s reaches into the staff (%.2f)", it.Glyph, staffY(l, b.Min.Y))
			}
		}
	}
	// The hairpin runs from the E5 to just before the f, at the f's
	// height, opening to the right.
	var hp []Item
	for _, it := range l.Items {
		if it.Role == RoleHairpin {
			hp = append(hp, it)
		}
	}
	if len(hp) != 2 {
		t.Fatalf("%d hairpin lines", len(hp))
	}
	if hp[0].P != hp[1].P || hp[0].Q.X != hp[1].Q.X || hp[0].Q.X <= hp[0].P.X {
		t.Errorf("a crescendo opens from its left point: %v %v", hp[0], hp[1])
	}
	fb := ItemBounds(&f[0])
	if hp[0].Q.X > fb.Min.X {
		t.Errorf("hairpin ends at %.2f, over the f at %.2f", hp[0].Q.X, fb.Min.X)
	}
	if fy := boxCentre(f[0]).Y; math.Abs(fy-hp[0].P.Y) > 0.6 {
		t.Errorf("hairpin at y %.2f, the f at %.2f: not one row", hp[0].P.Y, fy)
	}
	// A diminuendo opens on its left.
	l = Engrave(one(t, "treble", "| qC5(f,dim) qD5 qE5 qF5(hairpin_end) | wG5(p)"), Options{})
	hp = nil
	for _, it := range l.Items {
		if it.Role == RoleHairpin {
			hp = append(hp, it)
		}
	}
	if len(hp) != 2 || hp[0].P.X <= hp[0].Q.X || hp[0].P != hp[1].P {
		t.Errorf("a diminuendo's point is on its right: %v", hp)
	}
}

func TestNeighbouringDynamicsDoNotTouch(t *testing.T) {
	l := Engrave(one(t, "treble", "| sC5(pppp) sD5(ffff) sE5(sffz) sF5(mf) sG5(sfpp) sA5(fp) sB5(rfz) sC6(mp) h_"), Options{})
	type g struct {
		r   Rect
		ev  int
		bar int
	}
	var gs []g
	for _, it := range l.Items {
		if it.Role == RoleDynamic && it.Kind == ItemGlyph {
			gs = append(gs, g{ItemBounds(&it), it.Ref.Event, it.Ref.Bar})
		}
	}
	for i := range gs {
		for j := i + 1; j < len(gs); j++ {
			if gs[i].ev != gs[j].ev && overlap(gs[i].r, gs[j].r, 0.05) {
				t.Errorf("event %d's dynamic hits event %d's", gs[i].ev, gs[j].ev)
			}
		}
	}
}

func TestTextCrescendoAndExpressionText(t *testing.T) {
	l := Engrave(one(t, "treble", `| qC5(cresc=text) qD5 qE5 qF5(hairpin_end) | qG5(dim=text) qF5 hE5(expr=dolce,tech=pizz.)`), Options{})
	for _, s := range []string{"cresc.", "dim.", "dolce", "pizz."} {
		if len(textsOf(l, s)) != 1 {
			t.Errorf("no %q", s)
		}
	}
	dashes := 0
	for _, it := range l.Items {
		if it.Role == RoleLine && it.Kind == ItemLine && it.P.Y == it.Q.Y {
			dashes++
		}
	}
	if dashes < 4 {
		t.Errorf("%d dashes after the words", dashes)
	}
	// Technique text over the staff, expression text under it.
	if y := staffY(l, textsOf(l, "pizz.")[0].P.Y); y > 0 {
		t.Errorf("pizz. at %.2f, want over the staff", y)
	}
	if y := staffY(l, textsOf(l, "dolce")[0].P.Y); y < 4 {
		t.Errorf("dolce at %.2f, want under the staff", y)
	}
	if textsOf(l, "cresc.")[0].Font != TextItalic {
		t.Error("cresc. is not italic")
	}
}

// slurPath is the first slur's outline, flattened.
func slurPath(t *testing.T, l *List) []Point {
	t.Helper()
	for _, it := range l.Items {
		if it.Role == RoleSlur {
			polys := Flatten(it.Path, 0.02)
			if len(polys) == 0 {
				t.Fatal("an empty slur")
			}
			return polys[0]
		}
	}
	t.Fatal("no slur")
	return nil
}

// curveYAt is the slur's inner edge's y at x: the point nearest the notes.
func curveYAt(pts []Point, x float64, above bool) float64 {
	y := math.Inf(-1)
	if !above {
		y = math.Inf(1)
	}
	for _, p := range pts {
		if math.Abs(p.X-x) < 0.15 {
			if above {
				y = math.Max(y, p.Y)
			} else {
				y = math.Min(y, p.Y)
			}
		}
	}
	return y
}

func TestSlursCurveOverOrUnderTheNotes(t *testing.T) {
	// Stems down: over; stems up: under; mixed: over; clear of the heads
	// and the beam between.
	cases := []struct {
		voice string
		above bool
	}{
		{"| qC5(slur) qD5 qE5 qF5(slur_end)", true},
		{"| qE4(slur) qF4 qG4 qA4(slur_end)", false},
		{"| qC4(slur) qG5 qD4 qA5(slur_end)", true},
		{"| eC5(slur) eD5 eE5 eF5 eG5 eA5 eB5 eC6(slur_end)", true},
		{"| qG4(slur) qC6 qD6 qA4(slur_end)", true},
	}
	for _, c := range cases {
		l := Engrave(one(t, "treble", c.voice), Options{})
		pts := slurPath(t, l)
		minY, maxY := math.Inf(1), math.Inf(-1)
		for _, p := range pts {
			minY, maxY = math.Min(minY, p.Y), math.Max(maxY, p.Y)
		}
		for _, it := range l.Items {
			if it.Role != RoleNotehead && it.Role != RoleBeam && it.Role != RoleStem {
				continue
			}
			b := ItemBounds(&it)
			x := (b.Min.X + b.Max.X) / 2
			y := curveYAt(pts, x, c.above)
			if math.IsInf(y, 0) {
				continue
			}
			if c.above && y > b.Min.Y-0.1 {
				t.Errorf("%s: the slur at x %.2f (%.2f) is not over the %v (top %.2f)", c.voice, x, y, it.Role, b.Min.Y)
			}
			if !c.above && y < b.Max.Y+0.1 {
				t.Errorf("%s: the slur at x %.2f (%.2f) is not under the %v (bottom %.2f)", c.voice, x, y, it.Role, b.Max.Y)
			}
		}
	}
}

func TestSlursCrossBarsAndLayoutEdges(t *testing.T) {
	s := one(t, "treble", "| hC5(slur) hE5 | qF5 qE5 hD5(slur_end) | wC5")
	if n := len(itemsOf(Engrave(s, Options{}), RoleSlur)); n != 1 {
		t.Errorf("%d slurs over the barline", n)
	}
	// Bar 2 alone: the slur comes in from the layout's start.
	l := Engrave(s, Options{From: 2, To: 2})
	sl := itemsOf(l, RoleSlur)
	if len(sl) != 1 {
		t.Fatalf("%d slurs in bar 2 alone", len(sl))
	}
	if b := ItemBounds(&sl[0]); b.Min.X > l.Bars[0].Content+0.5 {
		t.Errorf("the slur starts at %.2f, after the bar's start %.2f", b.Min.X, l.Bars[0].Content)
	}
	// And bar 1 alone: it runs to the end.
	l = Engrave(s, Options{From: 1, To: 1})
	if sl := itemsOf(l, RoleSlur); len(sl) != 1 || ItemBounds(&sl[0]).Max.X < l.Bars[0].X1-0.6 {
		t.Errorf("the slur does not run to the layout's end %.2f: %v", l.Bars[0].X1, sl)
	}
	// A tie and a slur over the same notes are both drawn, the slur
	// further out.
	l = Engrave(one(t, "treble", "| hG4~(slur) qG4 qA4(slur_end)"), Options{})
	ties, slurs := itemsOf(l, RoleTie), itemsOf(l, RoleSlur)
	if len(ties) != 1 || len(slurs) != 1 {
		t.Fatalf("%d ties, %d slurs", len(ties), len(slurs))
	}
	if ItemBounds(&slurs[0]).Max.Y <= ItemBounds(&ties[0]).Max.Y {
		t.Error("the slur is not outside the tie")
	}
}

func TestGlissandoLines(t *testing.T) {
	l := Engrave(one(t, "treble", "| qC4(gliss) qC5 qG4(gliss=wavy) qE5 | hC5(gliss) hC4"), Options{})
	var lines, waves int
	for _, it := range l.Items {
		if it.Role == RoleLine && it.Kind == ItemLine {
			lines++
			if up := it.Q.Y < it.P.Y; up != (it.Ref.Bar == 1) {
				t.Errorf("bar %d's glissando drawn the wrong way: %v", it.Ref.Bar, it)
			}
		}
		if it.Role == RoleLine && it.Kind == ItemPath {
			waves++
		}
	}
	// Bar 1: one straight up, one wavy; bar 2: one straight down.
	if lines != 2 || waves != 1 {
		t.Errorf("%d straight lines and %d wavy, want 2 and 1", lines, waves)
	}
	if len(textsOf(l, "gliss.")) != 1 {
		t.Error("the long glissando has no gliss.")
	}
}

func TestOrnamentsFermatasTremolosArpeggios(t *testing.T) {
	l := Engrave(one(t, "treble", "| qC5(tr=#) qD5(mord) qE5(turn=b/#) qF5(ferm=long) | hC5(trem=3) hE5(trem2=2) | hC4+E4+G4(arp=down) hF5(ferm) | R(ferm)"), Options{})
	for _, g := range []string{"ornamentTrill", "ornamentMordent", "ornamentTurn", "fermataLongAbove", "tremolo3", "fermataAbove"} {
		if len(glyphsNamed(l, g)) == 0 {
			t.Errorf("no %s", g)
		}
	}
	if n := len(glyphsNamed(l, "fermataAbove")); n != 2 {
		t.Errorf("%d plain fermatas, want the half note's and the bar rest's", n)
	}
	// Over the staff, the trill's sharp over the trill, the turn's flat
	// over it and sharp between it and the staff.
	for _, it := range l.Items {
		if it.Role == RoleOrnament || it.Role == RoleFermata {
			if y := staffY(l, ItemBounds(&it).Max.Y); y > 0 {
				t.Errorf("%s reaches %.2f, into the staff", it.Glyph, y)
			}
		}
	}
	tr := glyphsNamed(l, "ornamentTrill")[0]
	turn := glyphsNamed(l, "ornamentTurn")[0]
	var trSharp, turnFlat, turnSharp bool
	for _, it := range l.Items {
		if it.Role != RoleOrnament || it.Kind != ItemGlyph {
			continue
		}
		c := boxCentre(it)
		switch {
		case it.Glyph == "accidentalSharp" && it.Ref.Event == tr.Ref.Event && c.Y < boxCentre(tr).Y:
			trSharp = true
		case it.Glyph == "accidentalFlat" && it.Ref.Event == turn.Ref.Event && c.Y < boxCentre(turn).Y:
			turnFlat = true
		case it.Glyph == "accidentalSharp" && it.Ref.Event == turn.Ref.Event && c.Y > boxCentre(turn).Y:
			turnSharp = true
		}
	}
	if !trSharp || !turnFlat || !turnSharp {
		t.Errorf("ornament accidentals: trill's sharp %v, turn's flat %v and sharp %v", trSharp, turnFlat, turnSharp)
	}
	// The tremolo crosses its stem.
	trem := glyphsNamed(l, "tremolo3")[0]
	stems := itemsBy(l, RoleStem, 2, 0, 0)
	if len(stems) == 0 {
		t.Fatal("no stem on the tremolo note")
	}
	if c := boxCentre(trem); math.Abs(c.X-stems[0].P.X) > 0.05 || c.Y < math.Min(stems[0].P.Y, stems[0].Q.Y) || c.Y > math.Max(stems[0].P.Y, stems[0].Q.Y) {
		t.Errorf("tremolo at %v, stem %v", c, stems[0])
	}
	// Two strokes between the half notes, touching neither stem.
	var strokes []Item
	for _, it := range itemsBy(l, RoleBeam, 2, 0, 1) {
		strokes = append(strokes, it)
	}
	if len(strokes) != 2 {
		t.Errorf("%d two-note tremolo strokes", len(strokes))
	}
	// The arpeggio left of the chord, its arrow at the bottom.
	var arp []Item
	for _, it := range itemsBy(l, RoleArticulation, 3, 0, 0) {
		arp = append(arp, it)
	}
	if len(arp) != 2 {
		t.Fatalf("arpeggio: %d items, want the line and its arrow", len(arp))
	}
	heads := itemsBy(l, RoleNotehead, 3, 0, 0)
	for _, it := range arp {
		if ItemBounds(&it).Max.X > ItemBounds(&heads[0]).Min.X-0.1 {
			t.Error("the arpeggio is not left of the chord")
		}
	}
}

func TestOttavaLinesMoveTheNotesTheyCover(t *testing.T) {
	plain := Engrave(one(t, "treble", "| qC6 qD6 qE6 qF6"), Options{})
	l := Engrave(one(t, "treble", "| qC6(8va) qD6 qE6 qF6(ottava_end)"), Options{})
	hp, ho := itemsBy(plain, RoleNotehead, 1, 0, 0), itemsBy(l, RoleNotehead, 1, 0, 0)
	if d := (ho[0].P.Y - l.Staves[0].Top) - (hp[0].P.Y - plain.Staves[0].Top); math.Abs(d-3.5) > 1e-9 {
		t.Errorf("under 8va C6 moved %.2f spaces, want an octave (3.5) down", d)
	}
	if len(glyphsNamed(l, "ottavaAlta")) != 1 {
		t.Fatal("no 8va")
	}
	hooks, dashes := 0, 0
	for _, it := range l.Items {
		if it.Role == RoleLine && it.Kind == ItemLine {
			if it.P.X == it.Q.X {
				hooks++
			} else {
				dashes++
			}
		}
	}
	if hooks != 1 || dashes < 3 {
		t.Errorf("%d hooks, %d dashes", hooks, dashes)
	}
	// 8vb under the staff, notes an octave up.
	l = Engrave(one(t, "treble", "| qC4(8vb) qD4 hE4"), Options{})
	if g := glyphsNamed(l, "ottavaBassaVb"); len(g) != 1 || staffY(l, ItemBounds(&g[0]).Min.Y) < 4 {
		t.Errorf("8vb: %v", g)
	}
}

func TestClefChangesInsideAStaff(t *testing.T) {
	s := one(t, "bass", "| qC3 qE3 qG4 qC5 | qE4 qG4 qC3 qE3")
	s.Lines[0].Staves[0].Clefs = []score.ClefChange{{At: score.Pos{Bar: 1, Offset: score.F(1, 2)}, Clef: "treble"}, {At: score.Pos{Bar: 2, Offset: score.F(1, 2)}, Clef: "bass"}}
	l := Engrave(s, Options{})
	if n := len(glyphsNamed(l, "gClefChange")); n != 1 {
		t.Fatalf("%d treble clef changes", n)
	}
	if n := len(glyphsNamed(l, "fClefChange")); n != 1 {
		t.Fatalf("%d bass clef changes", n)
	}
	// G4 after the treble clef sits on the second line.
	h := itemsBy(l, RoleNotehead, 1, 0, 2)
	if y := staffY(l, h[0].P.Y); math.Abs(y-3) > 1e-9 {
		t.Errorf("G4 in the treble clef at %.2f, want the second line (3)", y)
	}
	// The clef stands before the note it changes.
	g := glyphsNamed(l, "gClefChange")[0]
	if ItemBounds(&g).Max.X > ItemBounds(&h[0]).Min.X {
		t.Error("the clef change is not before its note")
	}
	sb := l.Staves[0]
	if len(sb.Changes) != 2 || sb.Changes[0].Clef != "treble" {
		t.Fatalf("staff box changes %v", sb.Changes)
	}
	// The pointer reads the clef in force where it is.
	x := ItemBounds(&h[0]).Min.X
	if d := sb.DiatonicAt(x, h[0].P.Y); d != (score.Pitch{Step: 4, Octave: 4}).Diatonic() {
		t.Errorf("DiatonicAt the G4 = %d", d)
	}
	if d := sb.DiatonicAt(sb.Changes[0].X-1, sb.Top+4); d != clefBottom("bass") {
		t.Errorf("before the change the bottom line is %d", d)
	}
	// A change at a bar's start stands before that bar's barline.
	s.Lines[0].Staves[0].Clefs = []score.ClefChange{{At: score.Pos{Bar: 2}, Clef: "treble"}}
	l = Engrave(s, Options{})
	g = glyphsNamed(l, "gClefChange")[0]
	if b := l.Bar(2); ItemBounds(&g).Max.X > b.X0 {
		t.Errorf("a change at bar 2 ends at %.2f, after the barline %.2f", ItemBounds(&g).Max.X, b.X0)
	}
	// Engraving from bar 2 starts in the clef in force there.
	l = Engrave(s, Options{From: 2})
	if l.Staves[0].Clef != "treble" || len(glyphsNamed(l, "gClef")) != 1 {
		t.Errorf("from bar 2: clef %s", l.Staves[0].Clef)
	}
}

func TestPedalLines(t *testing.T) {
	l := Engrave(one(t, "bass", "| qC3(ped) qE3 qG3(ped) qC4(ped_end) | wC3"), Options{})
	if len(glyphsNamed(l, "keyboardPedalPed")) != 1 {
		t.Fatal("no Ped.")
	}
	var lines []Item
	for _, it := range l.Items {
		if it.Role == RoleLine && it.Kind == ItemLine {
			lines = append(lines, it)
		}
	}
	// The line, the notch's two strokes and the hook (and the line on).
	if len(lines) < 5 {
		t.Fatalf("%d pedal lines", len(lines))
	}
	for _, it := range lines {
		if staffY(l, math.Min(it.P.Y, it.Q.Y)) < 4.5 {
			t.Errorf("pedal line at %.2f, inside the staff", staffY(l, it.P.Y))
		}
	}
}

func TestRepeatBarlinesEndingsAndJumps(t *testing.T) {
	s := fixture(t, "form")
	l := Engrave(s, Options{})
	// Repeat dots: two per staff per repeat barline — |:3, 6:|, |:9, 10:|
	// — four barlines on two staves.
	if n := len(glyphsNamed(l, "repeatDot")); n != 4*2*2 {
		t.Errorf("%d repeat dots, want 16", n)
	}
	if len(textsOf(l, "×3")) != 1 {
		t.Error("the three-time repeat says nothing")
	}
	for _, s := range []string{"1. 2.", "3.", "To Coda", "D.S. al Coda", "Fine", "Allegro", "rit.", "a tempo"} {
		if len(textsOf(l, s)) != 1 {
			t.Errorf("no %q", s)
		}
	}
	if len(glyphsNamed(l, "segno")) != 1 || len(glyphsNamed(l, "coda")) != 2 {
		t.Error("segno or coda signs missing")
	}
	// The first ending closes on its repeat (two hooks); the last is open.
	hooks := map[int]int{}
	for _, it := range l.Items {
		if it.Role == RoleEnding && it.Kind == ItemLine && it.P.X == it.Q.X {
			hooks[it.Ref.Bar]++
		}
	}
	if hooks[5] != 2 || hooks[7] != 1 {
		t.Errorf("ending hooks %v, want 2 at bar 5 and 1 at bar 7", hooks)
	}
	// Everything over the system is over the top staff.
	for _, it := range l.Items {
		switch it.Role {
		case RoleTempo, RoleRehearsal, RoleEnding, RoleJump:
			if b := ItemBounds(&it); b.Max.Y > l.Staves[0].Top+0.01 {
				t.Errorf("%v %q %s reaches %.2f, under the top line %.2f", it.Role, it.Text, it.Glyph, b.Max.Y, l.Staves[0].Top)
			}
		}
	}
	// Fine ends with a final barline: a thick line after bar 12 on each staff.
	thick := 0
	for _, it := range l.Items {
		if it.Role == RoleBarline && it.Width >= 0.4 && it.Ref.Bar == 12 {
			thick++
		}
	}
	if thick < 2 {
		t.Errorf("%d thick lines at the Fine", thick)
	}
}

func TestRehearsalAndTempoMarks(t *testing.T) {
	s := fixture(t, "form")
	l := Engrave(s, Options{})
	for _, letter := range []string{"A", "B", "C"} {
		if len(textsOf(l, letter)) != 1 {
			t.Errorf("no rehearsal mark %s", letter)
		}
	}
	for _, name := range []string{"intro", "verse", "coda"} {
		if len(textsOf(l, name)) != 1 {
			t.Errorf("no section name %s", name)
		}
	}
	if RehearsalLetter(0) != "A" || RehearsalLetter(25) != "Z" || RehearsalLetter(26) != "AA" || RehearsalLetter(27) != "BB" {
		t.Error("rehearsal letters")
	}
	// The box: four lines round the letter.
	box := 0
	for _, it := range l.Items {
		if it.Role == RoleRehearsal && it.Kind == ItemLine && it.Ref.Bar == 3 {
			box++
		}
	}
	if box != 4 {
		t.Errorf("rehearsal box of %d lines", box)
	}
	// Allegro in bold with its metronome mark; Più mosso in italic with a
	// dotted quarter; rit. with dashes and no number.
	if a := textsOf(l, "Allegro"); a[0].Font != TextBold {
		t.Error("Allegro is not bold")
	}
	if p := textsOf(l, "Più mosso"); len(p) != 1 || p[0].Font != TextItalic {
		t.Errorf("Più mosso: %v", p)
	}
	if len(textsOf(l, " = 96")) != 1 || len(textsOf(l, " = 72")) != 1 {
		t.Error("metronome numbers missing")
	}
	if len(glyphsNamed(l, "metNoteQuarterUp")) != 2 || len(glyphsNamed(l, "metAugmentationDot")) != 1 {
		t.Errorf("metronome notes: %d quarters, %d dots", len(glyphsNamed(l, "metNoteQuarterUp")), len(glyphsNamed(l, "metAugmentationDot")))
	}
	dashes := 0
	for _, it := range l.Items {
		if it.Role == RoleTempo && it.Kind == ItemLine {
			dashes++
		}
	}
	if dashes < 3 {
		t.Errorf("rit. has %d dashes", dashes)
	}
	// Nothing over the system overlaps anything else there.
	var sys []Item
	for _, it := range l.Items {
		switch it.Role {
		case RoleTempo, RoleRehearsal, RoleJump:
			sys = append(sys, it)
		}
	}
	for i := range sys {
		for j := i + 1; j < len(sys); j++ {
			a, b := sys[i], sys[j]
			if a.Ref.Bar == b.Ref.Bar && a.Role == b.Role {
				continue // a mark's own parts
			}
			if overlap(ItemBounds(&a), ItemBounds(&b), 0.05) {
				t.Errorf("%v %q%s overlaps %v %q%s", a.Role, a.Text, a.Glyph, b.Role, b.Text, b.Glyph)
			}
		}
	}
}

func TestFourVoicesKeepTheirRestsClear(t *testing.T) {
	l := Engrave(fixture(t, "voices4"), Options{})
	var rests, ink []Item
	for _, it := range l.Items {
		switch it.Role {
		case RoleRest:
			rests = append(rests, it)
		case RoleNotehead, RoleStem:
			ink = append(ink, it)
		}
	}
	if len(rests) < 8 {
		t.Fatalf("%d rests", len(rests))
	}
	for _, r := range rests {
		rb := ItemBounds(&r)
		for _, o := range ink {
			if o.Ref.Voice == r.Ref.Voice {
				continue
			}
			if overlap(rb, ItemBounds(&o), 0.05) {
				t.Errorf("voice %d's rest in bar %d hits voice %d's %v", r.Ref.Voice+1, r.Ref.Bar, o.Ref.Voice+1, o.Role)
			}
		}
		for _, o := range rests {
			if o.Ref.Voice != r.Ref.Voice && o.Ref.Bar == r.Ref.Bar && overlap(rb, ItemBounds(&o), 0.05) {
				t.Errorf("voice %d's rest hits voice %d's in bar %d", r.Ref.Voice+1, o.Ref.Voice+1, r.Ref.Bar)
			}
		}
	}
}

func TestTheScoresConcertPitch(t *testing.T) {
	s := fixture(t, "transposing")
	written := Engrave(s, Options{})
	concert := Engrave(s, Options{Concert: true})
	s.Concert = true
	flag := Engrave(s, Options{})
	if len(flag.Items) != len(concert.Items) {
		t.Fatal("the score's Concert does not engrave as Options.Concert")
	}
	for i := range flag.Items {
		if flag.Items[i].P != concert.Items[i].P {
			t.Fatalf("item %d differs", i)
		}
	}
	if len(written.Items) == len(concert.Items) && written.Items[len(written.Items)/2].P == concert.Items[len(concert.Items)/2].P {
		t.Error("written and concert engrave the same")
	}
}

func TestRoomForALaneUnderALine(t *testing.T) {
	s := fixture(t, "chords")
	s.Lines = append(s.Lines, s.Lines[0].Clone())
	s.Lines[1].Name = "Two"
	plain := Engrave(s, Options{})
	l := Engrave(s, Options{Below: map[int]float64{0: 5}})
	if len(l.Lanes) != 1 || l.Lanes[0].Line != 0 || l.Lanes[0].Height != 5 {
		t.Fatalf("lanes %v", l.Lanes)
	}
	ln := l.Lanes[0]
	if ln.Top < l.Staves[0].Extent.Max.Y {
		t.Errorf("the lane at %.2f starts in the staff's ink (to %.2f)", ln.Top, l.Staves[0].Extent.Max.Y)
	}
	if next := l.Staves[1].Extent.Min.Y; next < ln.Top+ln.Height {
		t.Errorf("the next line's ink at %.2f reaches into the lane (to %.2f)", next, ln.Top+ln.Height)
	}
	if d := (l.Staves[1].Top - l.Staves[0].Top) - (plain.Staves[1].Top - plain.Staves[0].Top); d < 5 {
		t.Errorf("the lane pushed the next line down only %.2f", d)
	}
	// Under the last line too.
	l = Engrave(s, Options{Below: map[int]float64{1: 4}})
	if len(l.Lanes) != 1 || l.Lanes[0].Top+l.Lanes[0].Height > l.Height {
		t.Errorf("a lane under the last line is outside the layout: %v, height %.2f", l.Lanes, l.Height)
	}
}

// TestNoCollisionsWithExpression engraves random music with random marks
// and checks that dynamics, articulations, ornaments and fermatas never
// run into noteheads, accidentals or one another.
func TestNoCollisionsWithExpression(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	n := 120
	if os.Getenv("SCORE_ENGRAVE_LONG") != "" {
		n = 3000
	}
	marks := []string{"p", "mf", "ff", "sfz", "fp", "stacc", "ten", "acc", "marc", "staccmo", "ferm", "tr", "mord", "turn", "stacc,acc", "ten,stacc"}
	for iter := 0; iter < n; iter++ {
		s := randomScore(r)
		for _, st := range s.Lines[0].Staves {
			for _, v := range st.Voices {
				for bi := range v.Measures {
					for ei := range v.Measures[bi].Events {
						e := &v.Measures[bi].Events[ei]
						if e.Grace != score.NotGrace || e.Kind != score.EventChord || r.Intn(3) != 0 {
							continue
						}
						for _, name := range strings.Split(marks[r.Intn(len(marks))], ",") {
							e.Marks = append(e.Marks, score.Mark{Name: name})
						}
					}
				}
			}
		}
		l := Engrave(s, Options{})
		type box struct {
			b  Rect
			it Item
		}
		var marksB, inkB []box
		for _, it := range l.Items {
			switch it.Role {
			case RoleDynamic, RoleArticulation, RoleOrnament, RoleFermata:
				marksB = append(marksB, box{ItemBounds(&it), it})
			case RoleNotehead, RoleAccidental:
				inkB = append(inkB, box{ItemBounds(&it), it})
			}
		}
		for _, m := range marksB {
			for _, o := range inkB {
				if overlap(m.b, o.b, 0.04) {
					t.Fatalf("iteration %d: %s of bar %d ev %d v%d hits %s of bar %d ev %d v%d\n%s", iter,
						m.it.Glyph, m.it.Ref.Bar, m.it.Ref.Event, m.it.Ref.Voice, o.it.Glyph, o.it.Ref.Bar, o.it.Ref.Event, o.it.Ref.Voice, voicesText(s))
				}
			}
		}
		for i := range marksB {
			for j := i + 1; j < len(marksB); j++ {
				a, b := marksB[i], marksB[j]
				if a.it.Ref == b.it.Ref {
					continue
				}
				if overlap(a.b, b.b, 0.04) {
					t.Fatalf("iteration %d: %s (bar %d ev %d v%d) hits %s (bar %d ev %d v%d)\n%s", iter,
						a.it.Glyph, a.it.Ref.Bar, a.it.Ref.Event, a.it.Ref.Voice, b.it.Glyph, b.it.Ref.Bar, b.it.Ref.Event, b.it.Ref.Voice, voicesText(s))
				}
			}
		}
	}
}
