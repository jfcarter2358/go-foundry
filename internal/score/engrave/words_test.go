package engrave

import (
	"math"
	"sort"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/tracker"
)

// Lyrics stand under the staff in a line a verse, centred under their
// notes (a melisma's first syllable at its note's left), hyphens between
// a word's syllables, an extender under a melisma; no two syllables touch,
// however tightly the notes would otherwise sit.
func TestLyrics(t *testing.T) {
	s := one(t, "treble", "| sC5(ly=Ex-) sD5(ly=tra-) sE5(ly=or-) sF5(ly=di-) sG5(ly=na-) sA5(ly=ri-) sB5(ly=ly) sC6(ly=long_) sD6 sE6 sF6 sG6 qr q_ |")
	s.Lines[0].Staves[0].Voices[0].Measures[0].Events[0].SetLyric(score.Lyric{Verse: 2, Text: "two"})
	l := Engrave(s, Options{})
	syl := itemsOf(l, RoleLyric)
	if len(syl) != 9 {
		t.Fatalf("%d syllables", len(syl))
	}
	st := l.Staves[0]
	ys := map[float64]int{}
	var v1 []Item
	for _, it := range syl {
		ys[math.Round(it.P.Y*1000)/1000]++
		if it.P.Y-st.Top < 4.5 {
			t.Errorf("%q at %.2f, inside the staff", it.Text, it.P.Y-st.Top)
		}
		if it.Text != "two" {
			v1 = append(v1, it)
		}
	}
	if len(ys) != 2 {
		t.Errorf("baselines %v: a verse is one line", ys)
	}
	sort.Slice(v1, func(i, j int) bool { return v1[i].P.X < v1[j].P.X })
	for i := 1; i < len(v1); i++ {
		a, b := ItemBounds(&v1[i-1]), ItemBounds(&v1[i])
		if a.Max.X+0.5 > b.Min.X {
			t.Errorf("%q and %q touch: %.2f, %.2f", v1[i-1].Text, v1[i].Text, a.Max.X, b.Min.X)
		}
	}
	// Six hyphens (one in each gap of Ex-tra-or-di-na-ri-ly) and one extender.
	var hy, ext int
	for _, it := range itemsOf(l, RoleLine) {
		if it.P.Y-st.Top < 4.5 {
			continue
		}
		if it.Q.X-it.P.X < 1 {
			hy++
		} else {
			ext++
			// From "long" to the end of the G6 (event 11).
			heads := itemsBy(l, RoleNotehead, 1, 0, 11)
			if len(heads) == 0 || math.Abs(it.Q.X-(heads[0].P.X+1.18)) > 0.2 {
				t.Errorf("extender ends at %.2f", it.Q.X)
			}
		}
	}
	if hy != 6 || ext != 1 {
		t.Errorf("%d hyphens, %d extenders", hy, ext)
	}
	// A syllable is centred under its head.
	head := itemsBy(l, RoleNotehead, 1, 0, 0)[0]
	b := ItemBounds(&v1[0])
	if c := (b.Min.X + b.Max.X) / 2; math.Abs(c-(head.P.X+0.59)) > 0.15 {
		t.Errorf("Ex centred at %.2f, head at %.2f", c, head.P.X+0.59)
	}
}

// A hyphen or extender over a system break runs to the system's end and
// on from the next one's start.
func TestLyricsAcrossSystems(t *testing.T) {
	s := one(t, "treble", "| hC5(ly=hal-) hD5(ly=le_) | hE5 hF5(ly=lu-) | wG5(ly=jah) |")
	first := Engrave(s, Options{From: 1, To: 1})
	second := Engrave(s, Options{From: 2, To: 2})
	third := Engrave(s, Options{From: 3, To: 3})
	lines := func(l *List) int {
		n := 0
		for _, it := range itemsOf(l, RoleLine) {
			if it.P.Y > l.Staves[0].Top+4.5 {
				n++
			}
		}
		return n
	}
	if lines(first) != 2 { // hal- le, and le's extender off the end
		t.Errorf("first system: %d", lines(first))
	}
	if lines(second) != 2 { // le's extender in, lu's hyphen out
		t.Errorf("second system: %d", lines(second))
	}
	if lines(third) != 1 { // lu's hyphen in before jah
		t.Errorf("third system: %d", lines(third))
	}
}

// Chord symbols stand over the top staff at one baseline, at their
// places (a change inside a held note gets a column of its own), never
// touching; on a transposing staff they are transposed with it.
func TestChordSymbols(t *testing.T) {
	s := one(t, "treble", "| wC5 | hD5 hE5 |")
	set := func(at score.Pos, sym string) {
		c, err := score.ParseChord(sym)
		if err != nil {
			t.Fatal(err)
		}
		s.Lines[0].Chords = append(s.Lines[0].Chords, score.ChordSymbol{At: at, Chord: c})
	}
	set(score.Pos{Bar: 1}, "Cmaj7#11")
	set(score.Pos{Bar: 1, Offset: score.F(1, 4)}, "Bbm7b5/E")
	set(score.Pos{Bar: 1, Offset: score.F(1, 2)}, "A7(b9,#9)")
	set(score.Pos{Bar: 2}, "Dm9")
	l := Engrave(s, Options{})
	if len(l.Chords) != 4 {
		t.Fatalf("%d chord boxes", len(l.Chords))
	}
	for i := 1; i < len(l.Chords); i++ {
		a, b := l.Chords[i-1].Box, l.Chords[i].Box
		if a.Max.X+0.5 > b.Min.X {
			t.Errorf("chords %d and %d touch", i-1, i)
		}
		if a.Max.Y > l.Staves[0].Top-0.5 {
			t.Errorf("chord %d reaches into the staff", i-1)
		}
	}
	if bb := l.Bar(1); len(bb.Columns) != 4 {
		t.Errorf("bar 1 has %d columns (one note, two changes inside it, the end)", len(bb.Columns))
	}
	if got := l.ChordAt(l.Chords[2].Box.Min.Add(Point{X: 0.2, Y: 0.2})); got == nil || got.Index != 2 {
		t.Errorf("hit: %+v", got)
	}
	var roots []string
	for _, it := range itemsOf(l, RoleChordSymbol) {
		if it.Kind == ItemText && it.Size == chordSize && it.Text != "/" {
			roots = append(roots, it.Text)
		}
	}
	if len(roots) != 5 || roots[1] != "B" || roots[2] != "E" { // the bass E at the root's size
		t.Errorf("roots %v", roots)
	}
	// A B-flat clarinet's part shows them a tone up.
	s.Lines[0].Staves[0].Transpose = score.Interval{Diatonic: -1, Chromatic: -2}
	l = Engrave(s, Options{})
	if it := itemsOf(l, RoleChordSymbol)[0]; it.Text != "D" {
		t.Errorf("transposed root %q", it.Text)
	}
	if it := itemsOf(Engrave(s, Options{Concert: true}), RoleChordSymbol)[0]; it.Text != "C" {
		t.Errorf("concert root %q", it.Text)
	}
}

// A guitar staff with tablature is two staves on the page: the notation,
// and the strings under it with each note's fret on its string; the
// tablature's events are the notation's, for the pointer.
func TestTablature(t *testing.T) {
	s := one(t, "treble_8vb", "| qC3+E3+G3+C4+E4 qG3~ qG3 qr |")
	s.Lines[0].Staves[0].Tab = score.NewTab("guitar")
	l := Engrave(s, Options{})
	if len(l.Staves) != 2 || !l.Staves[1].Tab || l.Staves[1].Staff != 0 || l.Staves[1].Lines != 6 {
		t.Fatalf("staves %+v", l.Staves)
	}
	tb := l.Staves[1]
	frets := map[string]int{}
	for _, it := range itemsOf(l, RoleNotehead) {
		if it.Kind != ItemText {
			continue
		}
		str := tb.StringAt(it.P.Y - 0.36*tabFretSize)
		frets[it.Text+"@"+string(rune('0'+str))]++
	}
	for _, want := range []string{"3@5", "2@4", "0@3", "1@2", "0@1", "(0)@3"} {
		if frets[want] == 0 {
			t.Errorf("no fret %s in %v", want, frets)
		}
	}
	if tb.StringY(1) != tb.Top || tb.BottomY() != tb.Top+5*tabGap {
		t.Error("string geometry")
	}
	// No ledger lines, no accidentals, no key on the tablature.
	for _, it := range l.Items {
		if it.Ref.Staff == 0 && (it.Role == RoleKey || it.Role == RoleLedger) && it.P.Y > tb.Top-0.5 {
			t.Errorf("a %v on the tablature", it.Role)
		}
	}
	// Alone, the strings carry the rhythm: stems under them.
	s.Lines[0].Staves[0].Tab.Only = true
	l = Engrave(s, Options{})
	if len(l.Staves) != 1 || !l.Staves[0].Tab {
		t.Fatalf("tab alone: %+v", l.Staves)
	}
	stems := 0
	for _, it := range itemsOf(l, RoleStem) {
		if it.P.Y > l.Staves[0].BottomY() {
			stems++
		}
	}
	if stems != 3 {
		t.Errorf("%d rhythm stems", stems)
	}
	if len(itemsOf(l, RoleRest)) != 1 {
		t.Error("the rest on a tab alone")
	}
	// Alone, it carries the dynamics (below its rhythm) and the lyrics.
	ev := &s.Lines[0].Staves[0].Voices[0].Measures[0].Events[1]
	ev.Marks = append(ev.Marks, score.Mark{Name: "p"})
	ev.SetLyric(score.Lyric{Verse: 1, Text: "la"})
	l = Engrave(s, Options{})
	dyn, ly := itemsOf(l, RoleDynamic), itemsOf(l, RoleLyric)
	if len(dyn) == 0 || len(ly) != 1 {
		t.Fatalf("%d dynamic items, %d syllables", len(dyn), len(ly))
	}
	if dyn[0].P.Y < l.Staves[0].BottomY()+tabStemTop+tabStem {
		t.Errorf("the p at %.2f, over the rhythm (to %.2f)", dyn[0].P.Y, l.Staves[0].BottomY()+tabStemTop+tabStem)
	}
}

// A drum map from the instrument: its places and heads, the articulation
// a sound is written with, a one-line staff for a single drum.
func TestDrumMaps(t *testing.T) {
	s := one(t, "percussion", "| qF#2 qA#2 qD2 qC4 |")
	s.Lines[0].Instrument = "x/kit"
	kit := score.KitOf(&tracker.Instrument{Name: "Kit", Playback: tracker.Playback{Drums: []tracker.DrumNote{
		{Key: 42, Name: "Hat", Line: 9, Head: "x"}, {Key: 46, Name: "Hat open", Line: 9, Head: "x", Articulation: "open"},
		{Key: 38, Name: "Snare", Line: 5, Head: "diamond"}}}})
	l := Engrave(s, Options{Kits: func(string) *score.Kit { return kit }})
	head := func(ev int) Item { return itemsBy(l, RoleNotehead, 1, 0, ev)[0] }
	if g := head(1).Glyph; g != "noteheadXBlack" {
		t.Errorf("open hat %s", g)
	}
	if g := head(2).Glyph; g != "noteheadDiamondBlack" {
		t.Errorf("snare %s", g)
	}
	arts := itemsBy(l, RoleArticulation, 1, 0, 1)
	if len(arts) != 1 || arts[0].Glyph != "brassMuteOpen" {
		t.Errorf("open hat's o: %+v", arts)
	}
	if len(itemsBy(l, RoleArticulation, 1, 0, 0)) != 0 {
		t.Error("the closed hat has an articulation")
	}
	// One line: drawn as one, the note on it, no ledger lines.
	s.Lines[0].Staves[0].Lines = 1
	l = Engrave(s, Options{Kits: func(string) *score.Kit { return kit }})
	staffLines := 0
	for _, it := range itemsOf(l, RoleStaff) {
		if it.Q.X-it.P.X > 5 {
			staffLines++
		}
	}
	if staffLines != 1 || l.Staves[0].Lines != 1 || len(itemsOf(l, RoleLedger)) != 0 {
		t.Errorf("%d lines, box %d, %d ledgers", staffLines, l.Staves[0].Lines, len(itemsOf(l, RoleLedger)))
	}
}

// A sung staff's dynamics go over it, out of the words' way; the same line
// with no words (and not a voice) has them under it.
func TestVocalDynamicsAbove(t *testing.T) {
	sung := one(t, "treble", "| qD4(mf,ly=A-) qG4(ly=ma-) qA4(ly=zing) qB4(p,ly=grace) |")
	l := Engrave(sung, Options{})
	dyn := 0
	for _, it := range l.Items {
		if it.Role == RoleDynamic {
			dyn++
			if b := ItemBounds(&it); staffY(l, b.Max.Y) > 0 {
				t.Errorf("a sung staff's dynamic under its top line: %v", b)
			}
		}
	}
	if dyn == 0 {
		t.Fatal("no dynamics")
	}
	plain := one(t, "treble", "| qD4(mf) qG4 qA4 qB4(p) |")
	l = Engrave(plain, Options{})
	for _, it := range l.Items {
		if it.Role == RoleDynamic {
			if b := ItemBounds(&it); staffY(l, b.Min.Y) < 4 {
				t.Errorf("an instrument's dynamic over its bottom line: %v", b)
			}
		}
	}
	// A choir with no words yet is sung too.
	plain.Lines[0].Instrument = "choir/choir"
	l = Engrave(plain, Options{})
	for _, it := range l.Items {
		if it.Role == RoleDynamic {
			if b := ItemBounds(&it); staffY(l, b.Max.Y) > 0 {
				t.Errorf("a choir's dynamic under its top line: %v", b)
			}
		}
	}
}

// A tablature alone carries the marks drawn at the notes: articulations,
// slurs, ornaments and fermatas over the strings, tremolo strokes on its
// rhythm stems, an arpeggio beside the numbers, tuplets under the rhythm.
func TestTablatureAloneCarriesTheMarks(t *testing.T) {
	s := one(t, "treble_8vb", "| qC3(stacc) qE3(acc) eG3(slur) eA3 eB3(slur_end) eC4 | 3{eC4 eD4 eE4} 3{qF4 qG4 qr} | hC3+E3+G3+C4(arp) qE4(tr) qG4(ferm) | hC4(trem=3) qE4(trem2=2) qG4 |")
	s.Lines[0].Staves[0].Tab = score.NewTab("guitar")
	s.Lines[0].Staves[0].Tab.Only = true
	l := Engrave(s, Options{})
	top := l.Staves[0].Top
	for _, g := range []string{"articStaccatoAbove", "articAccentAbove", "ornamentTrill", "fermataAbove"} {
		gs := glyphsNamed(l, g)
		if len(gs) != 1 {
			t.Errorf("%s: %d", g, len(gs))
			continue
		}
		if b := ItemBounds(&gs[0]); b.Max.Y > top+0.05 {
			t.Errorf("%s not over the strings: %v (top %v)", g, b, top)
		}
	}
	if n := len(glyphsNamed(l, "tuplet3")); n != 2 {
		t.Errorf("%d tuplet numbers", n)
	}
	stemsBottom := top
	for _, it := range l.Items {
		if it.Role == RoleStem {
			stemsBottom = math.Max(stemsBottom, math.Max(it.P.Y, it.Q.Y))
		}
	}
	for _, g := range glyphsNamed(l, "tuplet3") {
		if b := ItemBounds(&g); b.Min.Y < stemsBottom-0.6 {
			t.Errorf("a tuplet number among the stems: %v (stems to %v)", b, stemsBottom)
		}
	}
	if len(glyphsNamed(l, "tremolo3")) != 1 {
		t.Error("no tremolo strokes")
	}
	slurs, paths, beams := 0, 0, 0
	for _, it := range l.Items {
		switch {
		case it.Role == RoleSlur:
			slurs++
		case it.Role == RoleArticulation && it.Kind == ItemPath:
			paths++
		case it.Role == RoleBeam && it.Ref.Bar == 4:
			beams++
		}
	}
	if slurs != 1 || paths < 1 || beams != 2 {
		t.Errorf("%d slurs, %d arpeggio paths, %d two-note tremolo strokes", slurs, paths, beams)
	}
}

// A tablature in a tuning of its own, or with a capo, is headed with them
// at the start of the music — the retuned strings circled, every string
// when most move — and a standard tuning with no capo says nothing.
func TestTablatureTuningHeading(t *testing.T) {
	heading := func(tuning string, capo, from int) (texts []string, circles []string, tabTop float64, l *List) {
		s := one(t, "treble_8vb", "| qE2 qA2 qD3 qG3 | wE4 |")
		s.Lines[0].Staves[0].Tab = score.NewTab(tuning)
		s.Lines[0].Staves[0].Tab.Capo = capo
		l = Engrave(s, Options{From: from})
		for _, it := range itemsOf(l, RoleText) {
			if it.Kind == ItemText {
				texts = append(texts, it.Text)
			} else {
				circles = append(circles, it.Glyph)
			}
		}
		return texts, circles, l.Staves[len(l.Staves)-1].Top, l
	}
	if texts, circles, _, _ := heading("guitar", 0, 1); len(texts)+len(circles) != 0 {
		t.Errorf("standard tuning headed %v %v", texts, circles)
	}
	texts, circles, top, l := heading("guitar_drop_d", 2, 1)
	if strings.Join(texts, "|") != " = D|     |Capo 2" || len(circles) != 1 || circles[0] != "guitarString6" {
		t.Errorf("drop D, capo 2: %q %v", texts, circles)
	}
	for _, it := range itemsOf(l, RoleText) {
		if b := ItemBounds(&it); b.Max.Y > top || b.Min.Y < l.Staves[0].Top+4 {
			t.Errorf("%q stands at %v, not between the notation and its tablature (top %v)", it.Text, b, top)
		}
	}
	if texts, _, _, _ := heading("guitar_open_d", 0, 1); len(texts) == 0 || texts[0] != "Tuning: " {
		t.Errorf("open D: %q", texts)
	}
	// Only where the music starts.
	if texts, circles, _, _ := heading("guitar_drop_d", 2, 2); len(texts)+len(circles) != 0 {
		t.Errorf("headed again at bar 2: %v %v", texts, circles)
	}
}
