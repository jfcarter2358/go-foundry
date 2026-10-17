package engrave

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/score/glyph"
)

// one builds a score of one line on one staff from voice texts.
func one(t testing.TB, clef string, voices ...string) *score.Score {
	t.Helper()
	s := score.New("t")
	l := score.NewLine("Line", "keys/grand_piano", clef)
	l.Staves[0].Voices = nil
	for _, v := range voices {
		pv, err := score.ParseVoice(v)
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		l.Staves[0].Voices = append(l.Staves[0].Voices, pv)
	}
	s.Lines = append(s.Lines, l)
	return s
}

func fixture(t testing.TB, name string) *score.Score {
	t.Helper()
	s, err := score.LoadFile(filepath.Join("testdata", name+score.FileExt))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// items are a list's items by role and event.
func items(l *List, role Role, bar, event int) []Item {
	var out []Item
	for _, it := range l.Items {
		if it.Role == role && it.Ref.Bar == bar && (event < 0 || it.Ref.Event == event) {
			out = append(out, it)
		}
	}
	return out
}

// stemsUp reports each stemmed event's direction in a bar of voice v: up
// when its stem rises from its notes.
func stemDir(t *testing.T, l *List, bar, voice, event int) string {
	t.Helper()
	for _, it := range l.Items {
		if it.Role == RoleStem && it.Ref.Bar == bar && it.Ref.Voice == voice && it.Ref.Event == event && it.Kind == ItemLine {
			if it.Q.Y < it.P.Y {
				return "up"
			}
			return "down"
		}
	}
	return "none"
}

func TestStemDirections(t *testing.T) {
	// B4 (the middle line) and above down, below up; a chord by its note
	// furthest from the middle; balanced, the majority.
	l := Engrave(one(t, "treble", "| qB4 qA4 qC5 qG4+D5 | qD4+B4 qF4+G5 qE4+A5 wC4"), Options{})
	want := map[[2]int]string{
		{1, 0}: "down", {1, 1}: "up", {1, 2}: "down", {1, 3}: "down",
		{2, 0}: "up", {2, 1}: "down", {2, 2}: "down", {2, 3}: "none",
	}
	for k, w := range want {
		if got := stemDir(t, l, k[0], 0, k[1]); got != w {
			t.Errorf("bar %d event %d: stem %s, want %s", k[0], k[1], got, w)
		}
	}
	// In a bass staff the middle line is D3.
	l = Engrave(one(t, "bass", "| qD3 qC3 qE3 qr"), Options{})
	for ev, w := range []string{"down", "up", "down"} {
		if got := stemDir(t, l, 1, 0, ev); got != w {
			t.Errorf("bass event %d: %s, want %s", ev, got, w)
		}
	}
	// Two voices: up and down whatever the pitch; marks override.
	l = Engrave(one(t, "treble", "| qC4 qA5 qC4(down) qr", "| qA5 qC4 qr qG5(up)"), Options{})
	for ev, w := range []string{"up", "up", "down"} {
		if got := stemDir(t, l, 1, 0, ev); got != w {
			t.Errorf("voice 1 event %d: %s, want %s", ev, got, w)
		}
	}
	for ev, w := range []string{"down", "down", "none", "up"} {
		if got := stemDir(t, l, 1, 1, ev); got != w {
			t.Errorf("voice 2 event %d: %s, want %s", ev, got, w)
		}
	}
	// A voice of spaces does not make the staff multi-voice.
	l = Engrave(one(t, "treble", "| qC5 qC5 qC5 qC5", "| w_"), Options{})
	if got := stemDir(t, l, 1, 0, 0); got != "down" {
		t.Errorf("with a silent second voice: %s", got)
	}
}

func TestStemsReachTheMiddleLine(t *testing.T) {
	l := Engrave(one(t, "treble", "| qC6 qA3 qE5 qC4"), Options{})
	for ev, wantY := range map[int]float64{0: 2, 1: 2} {
		for _, it := range items(l, RoleStem, 1, ev) {
			tip := l.Staves[0].Top + wantY
			if math.Abs(it.Q.Y-tip) > 1e-9 {
				t.Errorf("event %d: tip at %g, want the middle line %g", ev, it.Q.Y, tip)
			}
		}
	}
	// Close to the staff: an octave long.
	for _, it := range items(l, RoleStem, 1, 2) {
		if d := math.Abs(it.Q.Y - it.P.Y); math.Abs(d-(stemLength-0.168)) > 1e-6 {
			t.Errorf("E5's stem is %g long", d)
		}
	}
}

func TestSecondsGoEitherSideOfTheStem(t *testing.T) {
	l := Engrave(one(t, "treble", "| qC4+D4 qC5+D5 qE4+F4+G4 wC4+D4"), Options{})
	heads := func(ev int) []float64 {
		var xs []float64
		for _, it := range items(l, RoleNotehead, 1, ev) {
			xs = append(xs, it.P.X)
		}
		return xs
	}
	// Stem up: the upper note of the second moves right; stem down: the
	// lower moves left. Notes are drawn bottom to top.
	if h := heads(0); !(h[1] > h[0]+0.9) {
		t.Errorf("stem up second: heads at %v", h)
	}
	if h := heads(1); !(h[0] < h[1]-0.9) {
		t.Errorf("stem down second: heads at %v", h)
	}
	if h := heads(2); !(h[1] > h[0]+0.9 && math.Abs(h[2]-h[0]) < 1e-9) {
		t.Errorf("E F G: heads at %v (F alone moves)", h)
	}
	if h := heads(3); !(h[1]-h[0] > 1.6) {
		t.Errorf("whole-note second: heads at %v (a whole head apart)", h)
	}
}

func TestAccidentalsByTheBar(t *testing.T) {
	l := Engrave(fixture(t, "accidentals"), Options{})
	count := func(bar, ev int) []string {
		var out []string
		for _, it := range items(l, RoleAccidental, bar, ev) {
			out = append(out, it.Glyph)
		}
		sort.Strings(out)
		return out
	}
	cases := []struct {
		bar, ev int
		want    string
	}{
		{1, 0, "accidentalNatural"}, // F in D major
		{1, 1, ""},                  // the bar remembers it
		{1, 2, ""},
		{1, 3, "accidentalSharp"}, // forced: F# is in the key
		{2, 0, "accidentalSharp"}, // G#; C# is the key's
		{2, 1, ""},                // G# again in the bar
		{2, 2, "accidentalFlat accidentalFlat accidentalFlat accidentalNatural accidentalNatural"}, // Cn Eb Gn Bb Db
		{2, 3, "accidentalDoubleFlat accidentalDoubleSharp"},
		{3, 0, "accidentalNatural"}, // B in E-flat
		{3, 1, ""},
		{3, 2, "accidentalNatural accidentalParensLeft accidentalParensRight"}, // cautionary
		{4, 0, ""},                  // tied over: not repeated
		{4, 1, "accidentalNatural"}, // A in E-flat
		{4, 2, "accidentalDoubleFlat"},
		{4, 3, "accidentalFlat"}, // back from the double flat
	}
	for _, c := range cases {
		if got := strings.Join(count(c.bar, c.ev), " "); got != c.want {
			t.Errorf("bar %d event %d: %q, want %q", c.bar, c.ev, got, c.want)
		}
	}
	// A tied-over note's accidental is not the bar's: the next one shows it.
	l = Engrave(one(t, "treble", "| h.C4 qC#4~ | qC#4 qC#4 h_"), Options{})
	if n := len(items(l, RoleAccidental, 2, 0)); n != 0 {
		t.Errorf("tied-over C#: %d accidentals", n)
	}
	if n := len(items(l, RoleAccidental, 2, 1)); n != 1 {
		t.Errorf("C# after the tied one: %d accidentals, want 1", n)
	}
	// Voices share a staff's accidentals.
	l = Engrave(one(t, "treble", "| qF#5 qF5 h_", "| q_ qF#4 qF4 q_"), Options{})
	n := 0
	for _, it := range items(l, RoleAccidental, 1, 1) {
		if it.Ref.Voice == 1 {
			n++
		}
	}
	if n != 1 {
		t.Errorf("voice 2's F#4 after voice 1's F#5: %d, want 1 (other octave)", n)
	}
	// An altered unison shows both.
	l = Engrave(one(t, "treble", "| qC4+C#4 qr hr"), Options{})
	if n := len(items(l, RoleAccidental, 1, 0)); n != 2 {
		t.Errorf("C and C# together: %d accidentals", n)
	}
}

func TestAccidentalsStackWithoutColliding(t *testing.T) {
	l := Engrave(one(t, "treble", "| qC#4+E4+G#4+B#4+D#5+F#5 qDb4+Eb4+Gb4 qC#5+D#5+E#5 qr"), Options{})
	f := glyph.Bravura()
	for ev := 0; ev < 3; ev++ {
		acc := items(l, RoleAccidental, 1, ev)
		heads := items(l, RoleNotehead, 1, ev)
		for i := range acc {
			a := itemBounds(f, &acc[i])
			for j := i + 1; j < len(acc); j++ {
				if overlap(a, itemBounds(f, &acc[j]), 0.04) {
					t.Errorf("event %d: accidentals %d and %d collide", ev, i, j)
				}
			}
			for j := range heads {
				if overlap(a, itemBounds(f, &heads[j]), 0.04) {
					t.Errorf("event %d: accidental %d hits a notehead", ev, i)
				}
			}
		}
	}
	// The six-note chord takes more than one column but not six.
	xs := map[float64]bool{}
	for _, it := range items(l, RoleAccidental, 1, 0) {
		xs[math.Round(it.P.X*100)/100] = true
	}
	if len(xs) < 2 || len(xs) > 4 {
		t.Errorf("six accidentals in %d columns", len(xs))
	}
}

func overlap(a, b Rect, eps float64) bool {
	return a.Min.X+eps < b.Max.X-eps && b.Min.X+eps < a.Max.X-eps && a.Min.Y+eps < b.Max.Y-eps && b.Min.Y+eps < a.Max.Y-eps
}

func TestKeySignaturePositions(t *testing.T) {
	pos := func(accs []keyAcc) []int {
		var out []int
		for _, a := range accs {
			out = append(out, a.pos)
		}
		return out
	}
	eq := func(a, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	cases := []struct {
		clef   string
		fifths int
		want   []int
	}{
		{"treble", 7, []int{8, 5, 9, 6, 3, 7, 4}},  // F5 C5 G5 D5 A4 E5 B4
		{"treble", -7, []int{4, 7, 3, 6, 2, 5, 1}}, // B4 E5 A4 D5 G4 C5 F4
		{"bass", 3, []int{6, 3, 7}},                // F3 C3 G3
		{"bass", -2, []int{2, 5}},                  // B2 E3
		{"alto", 2, []int{7, 4}},                   // F4 C4
		{"alto", -3, []int{3, 6, 2}},               // B3 E4 A3
		{"tenor", 4, []int{2, 6, 3, 7}},            // F3 C4 G3 D4
		{"tenor", -1, []int{5}},                    // B3
		{"treble_8vb", 1, []int{8}},
		{"percussion", 3, nil},
	}
	for _, c := range cases {
		if got := pos(keyAccs(c.clef, c.fifths)); !eq(got, c.want) {
			t.Errorf("%s %+d: %v, want %v", c.clef, c.fifths, got, c.want)
		}
	}
	// Every sharp and flat is the pitch the key alters.
	for _, clef := range []string{"treble", "bass", "alto", "tenor"} {
		for f := -7; f <= 7; f++ {
			for _, a := range keyAccs(clef, f) {
				p := score.FromDiatonic(clefBottom(clef) + a.pos)
				want := score.KeyAlter(f, int(p.Step))
				if (a.glyph == "accidentalSharp") != (want > 0) || want == 0 {
					t.Errorf("%s %+d: %s on %s, which the key alters by %d", clef, f, a.glyph, p, want)
				}
			}
		}
	}
	// A change cancels what it drops.
	ch := keyChangeAccs("treble", 2, -3)
	if n := countGlyph(ch, "accidentalNatural"); n != 2 || countGlyph(ch, "accidentalFlat") != 3 {
		t.Errorf("D to E-flat: %v", ch)
	}
	if ch := keyChangeAccs("treble", 5, 2); countGlyph(ch, "accidentalNatural") != 3 || countGlyph(ch, "accidentalSharp") != 2 {
		t.Errorf("B to D: %v", ch)
	}
	if ch := keyChangeAccs("treble", -4, -6); countGlyph(ch, "accidentalNatural") != 0 {
		t.Errorf("A-flat to G-flat cancels nothing: %v", ch)
	}
}

func countGlyph(accs []keyAcc, g string) int {
	n := 0
	for _, a := range accs {
		if a.glyph == g {
			n++
		}
	}
	return n
}

func TestTimeSignatures(t *testing.T) {
	l := Engrave(fixture(t, "meters"), Options{})
	glyphsAt := func(bar int) string {
		var out []string
		for _, it := range l.Items {
			if it.Role == RoleTime && it.Ref.Bar == bar {
				out = append(out, it.Glyph)
			}
		}
		return strings.Join(out, " ")
	}
	want := map[int]string{
		1: "timeSigCommon",
		2: "timeSig3 timeSig4",
		3: "timeSig6 timeSig8",
		4: "timeSig3 timeSigPlusSmall timeSig2 timeSigPlusSmall timeSig2 timeSig8",
		5: "timeSigCutCommon",
		7: "timeSig1 timeSig2 timeSig8",
	}
	for bar, w := range want {
		if got := glyphsAt(bar); got != w {
			t.Errorf("bar %d: %q, want %q", bar, got, w)
		}
	}
	// No meter change, no signature; a layout from mid-score shows one only
	// where the score changes meter.
	if l2 := Engrave(one(t, "treble", "| wC5 | wC5"), Options{From: 2}); hasRole(l2, RoleTime) {
		t.Error("a time signature at a bar with no change")
	}
	if l2 := Engrave(fixture(t, "meters"), Options{From: 2, To: 2}); !hasRole(l2, RoleTime) {
		t.Error("no time signature at a change")
	}
}

func hasRole(l *List, r Role) bool {
	for _, it := range l.Items {
		if it.Role == r {
			return true
		}
	}
	return false
}

// beamGroups are a bar's beam groups as event-index runs.
func beamGroups(t *testing.T, s *score.Score, bar int) [][]int {
	t.Helper()
	e := newEngraver(s, Options{From: bar, To: bar})
	e.collect()
	var out [][]int
	for _, g := range e.staves[0].bars[0].beams {
		if g.grace {
			continue
		}
		var run []int
		for _, x := range g.evs {
			run = append(run, x.ref.Event)
		}
		out = append(out, run)
	}
	return out
}

func TestBeamingFollowsTheMeter(t *testing.T) {
	s := fixture(t, "meters")
	cases := map[int]string{
		1: "[[0 1 2 3]]",              // 4/4: a half bar of eighths
		2: "[[0 1] [2 3] [4 5]]",      // 3/4: by the beat
		3: "[[0 1 2] [3 4 5]]",        // 6/8: dotted quarters
		4: "[[0 1 2] [3 4] [5 6]]",    // 3+2+2/8
		5: "[[0 1 2 3]]",              // cut time: the half
		6: "[[0 1 2] [3 4]]",          // 5/8 as 3+2
		7: "[[1 2 3] [5 6 7 8 9 10]]", // 12/8
		8: "[[0 1] [2 3] [4 5 6]]",    // 7/8 as 2+2+3
	}
	for bar, want := range cases {
		if got := fmtGroups(beamGroups(t, s, bar)); got != want {
			t.Errorf("bar %d: %s, want %s", bar, got, want)
		}
	}
	b := fixture(t, "beams")
	if got := fmtGroups(beamGroups(t, b, 4)); got != "[[0 1 2 3]]" {
		t.Errorf("rests break beams: %s", got)
	}
	// Never across the middle of 4/4, nor a 16th pair's half bar.
	s2 := one(t, "treble", "| qC5 eC5 eC5 eC5 eC5 qC5 | sC5 sC5 sC5 sC5 sC5 sC5 sC5 sC5 hC5")
	if got := fmtGroups(beamGroups(t, s2, 1)); got != "[[1 2] [3 4]]" {
		t.Errorf("off-beat eighths: %s", got)
	}
	if got := fmtGroups(beamGroups(t, s2, 2)); got != "[[0 1 2 3] [4 5 6 7]]" {
		t.Errorf("16ths by the beat: %s", got)
	}
	// A tuplet beams on its own.
	s3 := one(t, "treble", "| eC5 eC5 3{eC5 eC5 eC5} hC5")
	if got := fmtGroups(beamGroups(t, s3, 1)); got != "[[0 1] [2 3 4]]" {
		t.Errorf("triplet: %s", got)
	}
}

func fmtGroups(g [][]int) string { return fmt.Sprint(g) }

func TestBeamGeometry(t *testing.T) {
	// Slope by interval, limited; flat for a repeated note and for a
	// concave shape; the nearest note's stem a standard length.
	slope := func(text string) (float64, *beamGroup) {
		s := one(t, "treble", text)
		e := newEngraver(s, Options{})
		e.collect()
		e.measure()
		e.placeGalley()
		e.draw()
		g := e.staves[0].bars[0].beams[0]
		n := len(g.evs)
		return g.yAt(g.evs[n-1].stemX) - g.yAt(g.evs[0].stemX), g
	}
	if d, _ := slope("| eG4 eG4 hr qr"); d != 0 {
		t.Errorf("repeated notes: slope %g", d)
	}
	if d, _ := slope("| eG4 eA4 hr qr"); math.Abs(d+0.25) > 1e-9 {
		t.Errorf("a second up: beam rises %g, want 0.25", -d)
	}
	if d, _ := slope("| eG4 eG5 hr qr"); math.Abs(d+1) > 1e-9 {
		t.Errorf("an octave up: beam rises %g, want 1 (the limit)", -d)
	}
	if d, _ := slope("| eE4 eA5 eE4 eC4 hr"); d != 0 {
		t.Errorf("concave: slope %g", d)
	}
	_, g := slope("| eF4 eA4 hr qr")
	for _, x := range g.evs {
		l := x.notes[0].y - x.tipY
		if l < stemLength-1e-6 {
			t.Errorf("a beamed stem %g long", l)
		}
	}
	// Inside the staff the beam's ends sit, hang or straddle.
	for _, text := range []string{"| eF4 eA4 hr qr", "| eC5 eE5 hr qr", "| eD5 eB4 hr qr", "| sF4 sG4 sA4 sB4 hr qr"} {
		_, g := slope(text)
		for _, x := range []*ev{g.evs[0], g.evs[len(g.evs)-1]} {
			if !beamEndOK(x.tipY, g.up) {
				t.Errorf("%s: beam end at %g makes a wedge", text, x.tipY)
			}
		}
	}
}

func TestPartialBeamsPointToTheirNote(t *testing.T) {
	l := Engrave(one(t, "treble", "| e.C5 sC5 sC5 e.C5 hr"), Options{})
	// Each 16th's hook lies on its stem's side toward the dotted note.
	var stems []float64
	for _, it := range l.Items {
		if it.Role == RoleStem && it.Kind == ItemLine {
			stems = append(stems, it.P.X)
		}
	}
	if len(stems) != 4 {
		t.Fatalf("%d stems", len(stems))
	}
	var hooks []Rect
	f := glyph.Bravura()
	for i := range l.Items {
		it := &l.Items[i]
		if it.Role == RoleBeam {
			b := itemBounds(f, it)
			if b.W() < 1.5 {
				hooks = append(hooks, b)
			}
		}
	}
	if len(hooks) != 2 {
		t.Fatalf("%d partial beams, want 2", len(hooks))
	}
	sort.Slice(hooks, func(i, j int) bool { return hooks[i].Min.X < hooks[j].Min.X })
	if !(hooks[0].Max.X <= stems[1]+0.1) {
		t.Error("the first 16th's hook does not point back to its dotted eighth")
	}
	if !(hooks[1].Min.X >= stems[2]-0.1) {
		t.Error("the second 16th's hook does not point on to its dotted eighth")
	}
}

func TestDotsSitInSpaces(t *testing.T) {
	l := Engrave(one(t, "treble", "| h.E4+G4+B4 qr | q.F4 er h.A4+B4"), Options{})
	top := l.Staves[0].Top
	for _, it := range l.Items {
		if it.Role != RoleDot || it.Ref.Event < 0 {
			continue
		}
		p := (top + 4 - it.P.Y) * 2 // staff position
		if math.Abs(math.Mod(math.Abs(math.Round(p)), 2)-1) > 1e-6 || math.Abs(p-math.Round(p)) > 1e-6 {
			t.Errorf("bar %d event %d: a dot at position %g, not a space", it.Ref.Bar, it.Ref.Event, p)
		}
	}
	// A second in a dotted chord still gets a dot each, in different spaces.
	ys := map[float64]bool{}
	for _, it := range items(l, RoleDot, 2, 2) {
		ys[it.P.Y] = true
	}
	if len(ys) != 2 {
		t.Errorf("A4+B4 dotted: dots in %d spaces", len(ys))
	}
}

func TestRestsMoveForVoices(t *testing.T) {
	l := Engrave(one(t, "treble", "| qr qC5 hr", "| qr qA4 hr"), Options{})
	top := l.Staves[0].Top
	y := func(voice, ev int) float64 {
		for _, it := range l.Items {
			if it.Role == RoleRest && it.Ref.Voice == voice && it.Ref.Event == ev {
				return it.P.Y - top
			}
		}
		return math.NaN()
	}
	if !(y(0, 0) < 2 && y(1, 0) > 2) {
		t.Errorf("quarter rests at %g and %g: voice 1 should be above, voice 2 below", y(0, 0), y(1, 0))
	}
	if !(y(0, 2) < 2 && y(1, 2) > 2) || y(0, 2) != math.Round(y(0, 2)) {
		t.Errorf("half rests at %g and %g, on lines", y(0, 2), y(1, 2))
	}
	// Alone, a rest sits in the middle.
	l = Engrave(one(t, "treble", "| qr qC5 hr"), Options{})
	for _, it := range l.Items {
		if it.Role == RoleRest && it.P.Y-l.Staves[0].Top != 2 {
			t.Errorf("a lone rest at %g", it.P.Y-l.Staves[0].Top)
		}
	}
}

func TestBarRestsAreCentred(t *testing.T) {
	l := Engrave(one(t, "treble", "| R | wr | qC5 qC5 hC5 |  | R"), Options{})
	f := glyph.Bravura()
	for _, bb := range l.Bars {
		for i := range l.Items {
			it := &l.Items[i]
			if it.Role != RoleRest || it.Ref.Bar != bb.Bar {
				continue
			}
			b := itemBounds(f, it)
			mid := (b.Min.X + b.Max.X) / 2
			if math.Abs(mid-(bb.Content+bb.X1)/2) > 1e-6 {
				t.Errorf("bar %d: rest centred at %g, the bar's middle is %g", bb.Bar, mid, (bb.Content+bb.X1)/2)
			}
			if bb.Bar == 4 && !it.Faint {
				t.Error("an empty bar's implied rest is not faint")
			}
		}
	}
}

func TestStavesShareOnsets(t *testing.T) {
	s := fixture(t, "grandstaff")
	l := Engrave(s, Options{})
	// Bar 2: quarters in the violin, both piano staves; every onset's
	// heads on one x.
	xs := map[float64][]float64{}
	for _, eb := range l.Events {
		if eb.Ref.Bar == 2 {
			xs[eb.Offset] = append(xs[eb.Offset], eb.X)
		}
	}
	if len(xs) != 3 {
		t.Fatalf("bar 2 onsets %v", xs)
	}
	for off, x := range xs {
		for _, v := range x[1:] {
			if math.Abs(v-x[0]) > 1e-9 {
				t.Errorf("onset %g at %v", off, x)
			}
		}
	}
	// One brace, barlines through the piano's gap but not the violin's.
	braces := 0
	for _, it := range l.Items {
		if it.Role == RoleBrace {
			braces++
		}
	}
	if braces != 1 {
		t.Errorf("%d braces", braces)
	}
	vb := l.Staves[0].Top + 4
	pt := l.Staves[1].Top
	for _, it := range l.Items {
		if it.Role == RoleBarline && it.Ref.Bar == 2 && it.P.Y < pt-0.5 && it.Q.Y > vb+0.5 {
			t.Error("a barline joins the violin to the piano")
		}
	}
	found := false
	for _, it := range l.Items {
		if it.Role == RoleBarline && it.Ref.Bar == 2 && math.Abs(it.P.Y-(l.Staves[1].Top+4)) < 1e-9 && math.Abs(it.Q.Y-l.Staves[2].Top) < 1e-9 {
			found = true
		}
	}
	if !found {
		t.Error("no barline through the grand staff")
	}
}

func TestBarlines(t *testing.T) {
	l := Engrave(fixture(t, "grandstaff"), Options{})
	widths := func(bar int) []float64 {
		var out []float64
		for _, it := range l.Items {
			if it.Role == RoleBarline && it.Ref.Bar == bar && it.Ref.Staff == 0 && it.Ref.Line == 0 {
				out = append(out, it.Width)
			}
		}
		return out
	}
	d := glyph.Bravura().Defaults
	if w := widths(1); len(w) != 1 {
		t.Errorf("bar 1 closes with %v", w)
	}
	if w := widths(2); len(w) != 2 || w[0] != d.ThinBarlineThickness || w[1] != d.ThinBarlineThickness {
		t.Errorf("before the key change: %v, want a double barline", w)
	}
	if w := widths(4); len(w) != 2 || w[1] != d.ThickBarlineThickness {
		t.Errorf("the end: %v, want a final barline", w)
	}
	if w := widths(3); len(Engrave(fixture(t, "grandstaff"), Options{To: 3}).Items) == 0 || len(w) != 1 {
		t.Errorf("bar 3: %v", w)
	}
	if l3 := Engrave(fixture(t, "grandstaff"), Options{To: 3}); hasThick(l3, d) {
		t.Error("a final barline before the end")
	}
}

func hasThick(l *List, d glyph.Defaults) bool {
	for _, it := range l.Items {
		if it.Role == RoleBarline && it.Width == d.ThickBarlineThickness {
			return true
		}
	}
	return false
}

func TestTransposingStaves(t *testing.T) {
	s := fixture(t, "transposing")
	l := Engrave(s, Options{})
	if l.Staves[0].Fifths != 0 || l.Staves[1].Fifths != -1 || l.Staves[2].Fifths != -2 {
		t.Errorf("written keys %d %d %d, want C, F, B-flat", l.Staves[0].Fifths, l.Staves[1].Fifths, l.Staves[2].Fifths)
	}
	if !l.Staves[0].Transposed || l.Staves[2].Transposed {
		t.Error("transposed flags")
	}
	// The clarinet's Bb4 is written C5: the same line as the flute's C5.
	cl := firstHead(l, 0)
	fl := firstHead(l, 2)
	if math.Abs((cl.P.Y-l.Staves[0].Top)-(fl.P.Y-l.Staves[2].Top-0.5)) > 1e-9 {
		t.Errorf("clarinet's first note at %g, the flute's Bb4 at %g", cl.P.Y-l.Staves[0].Top, fl.P.Y-l.Staves[2].Top)
	}
	c := Engrave(s, Options{Concert: true})
	if c.Staves[0].Fifths != -2 || c.Staves[0].Transposed {
		t.Error("at concert pitch the clarinet reads in B-flat")
	}
	if b := c.Staves[1]; b.Diatonic(firstHead(c, 1).P.Y) != (score.Pitch{Step: 6, Octave: 3}).Diatonic() {
		t.Error("at concert pitch the horn's first note is not on B3's line")
	}
}

func firstHead(l *List, line int) Item {
	for _, it := range l.Items {
		if it.Role == RoleNotehead && it.Ref.Line == line {
			return it
		}
	}
	return Item{}
}

func TestTies(t *testing.T) {
	l := Engrave(fixture(t, "ties"), Options{})
	ties := map[[2]int]int{}
	for _, it := range l.Items {
		if it.Role == RoleTie {
			ties[[2]int{it.Ref.Bar, it.Ref.Event}]++
		}
	}
	for _, k := range [][2]int{{1, 0}, {1, 2}, {2, 1}, {3, 0}, {3, 1}, {5, 0}, {5, 2}, {6, 1}} {
		if ties[k] != 1 {
			t.Errorf("bar %d event %d: %d ties", k[0], k[1], ties[k])
		}
	}
	// From a stem-up note the tie curves down (its middle below its ends).
	for i := range l.Items {
		it := &l.Items[i]
		if it.Role == RoleTie && it.Ref.Bar == 1 && it.Ref.Event == 0 {
			b := itemBounds(glyph.Bravura(), it)
			if !(b.Max.Y > it.Path[0].P[0].Y+0.3) {
				t.Error("G4's tie does not curve down")
			}
		}
	}
	// Starting from bar 2 the incoming tie is drawn into the first note, and
	// one leaving the layout runs to its last barline.
	l2 := Engrave(fixture(t, "ties"), Options{From: 2, To: 3})
	in, out := false, false
	for _, it := range l2.Items {
		if it.Role == RoleTie && it.Ref.Bar == 2 && it.Ref.Event == 0 {
			in = true
		}
		if it.Role == RoleTie && it.Ref.Bar == 3 && it.Ref.Event == 1 {
			out = true
		}
	}
	if !in || !out {
		t.Errorf("tie in %v, tie out %v", in, out)
	}
}

func TestTuplets(t *testing.T) {
	l := Engrave(fixture(t, "tuplets"), Options{})
	nums := func(bar int) []string {
		var out []string
		for _, it := range l.Items {
			if it.Role == RoleTuplet && it.Ref.Bar == bar && it.Kind == ItemGlyph {
				out = append(out, it.Glyph)
			}
		}
		return out
	}
	brackets := func(bar int) int {
		n := 0
		for _, it := range l.Items {
			if it.Role == RoleTuplet && it.Ref.Bar == bar && it.Kind == ItemLine {
				n++
			}
		}
		return n / 4 // two hooks and the two halves
	}
	if got := strings.Join(nums(1), " "); got != "tuplet3 tuplet3 tuplet3" {
		t.Errorf("bar 1 numbers: %s", got)
	}
	if b := brackets(1); b != 1 {
		t.Errorf("bar 1: %d brackets, want 1 (the beamed triplets need none)", b)
	}
	if got := strings.Join(nums(3), " "); !strings.Contains(got, "tuplet5 tupletColon tuplet3") {
		t.Errorf("bar 3: %s, want the ratio 5:3", got)
	}
	if b := brackets(4); b != 2 {
		t.Errorf("bar 4: %d brackets, want 2", b)
	}
	// Nested: the outer number clears the inner bracket.
	f := glyph.Bravura()
	var boxes []Rect
	for i := range l.Items {
		it := &l.Items[i]
		if it.Role == RoleTuplet && it.Ref.Bar == 3 && it.Ref.Event >= 5 {
			boxes = append(boxes, itemBounds(f, it))
		}
	}
	for i := range boxes {
		for j := i + 1; j < len(boxes); j++ {
			if overlap(boxes[i], boxes[j], 0.05) && !(boxes[i].W() < 0.2 || boxes[j].W() < 0.2) {
				t.Errorf("nested tuplet pieces %d and %d collide", i, j)
			}
		}
	}
}

func TestGraceNotes(t *testing.T) {
	l := Engrave(fixture(t, "graces"), Options{})
	for _, it := range l.Items {
		if it.Role == RoleNotehead && it.Ref.Bar == 1 && it.Ref.Event == 0 && it.Size != graceSize {
			t.Errorf("a grace head at size %g", it.Size)
		}
	}
	// The slash: an acciaccatura has one more stroke than an appoggiatura.
	strokes := func(ev int) int {
		n := 0
		for _, it := range l.Items {
			if it.Ref.Bar == 1 && it.Ref.Event == ev && it.Kind == ItemLine && (it.Role == RoleFlag || it.Role == RoleStem) {
				n++
			}
		}
		return n
	}
	if strokes(0) != strokes(2)+1 {
		t.Errorf("acciaccatura %d strokes, appoggiatura %d", strokes(0), strokes(2))
	}
	// Graces sit left of their note, clear of its accidental.
	var grace, main float64
	for _, eb := range l.Events {
		if eb.Ref.Bar == 2 && eb.Ref.Event == 4 {
			grace = eb.Box.Max.X
		}
		if eb.Ref.Bar == 2 && eb.Ref.Event == 5 {
			main = eb.Box.Min.X
		}
	}
	if !(grace < main) {
		t.Errorf("grace ink to %g, its note's from %g", grace, main)
	}
}

// An unbeamed acciaccatura shorter than an eighth is slashed too: Bravura
// has the slash's anchors only on the eighth's flag, and the 16th's used
// to come out with none, the same as an appoggiatura.
func TestShortAcciaccaturasAreSlashed(t *testing.T) {
	l := Engrave(one(t, "treble", "| /sG5 qA5 ^sG5 qA5 /tG4 qA4 ^tG4 qA4"), Options{})
	strokes := func(ev int) (n int, box Rect) {
		box = Rect{Min: Point{X: math.Inf(1), Y: math.Inf(1)}, Max: Point{X: math.Inf(-1), Y: math.Inf(-1)}}
		for _, it := range l.Items {
			if it.Ref.Bar == 1 && it.Ref.Event == ev && it.Kind == ItemLine && (it.Role == RoleFlag || it.Role == RoleStem) {
				n++
				box.Min.X, box.Min.Y = math.Min(box.Min.X, math.Min(it.P.X, it.Q.X)), math.Min(box.Min.Y, math.Min(it.P.Y, it.Q.Y))
				box.Max.X, box.Max.Y = math.Max(box.Max.X, math.Max(it.P.X, it.Q.X)), math.Max(box.Max.Y, math.Max(it.P.Y, it.Q.Y))
			}
		}
		return
	}
	for _, c := range []struct {
		acc, app int
		what     string
	}{{0, 2, "16th"}, {4, 6, "32nd"}} {
		na, ba := strokes(c.acc)
		np, _ := strokes(c.app)
		if na != np+1 {
			t.Errorf("%s: acciaccatura %d strokes, appoggiatura %d", c.what, na, np)
		}
		// The slash crosses the stem: it reaches either side of it.
		if !(ba.W() > 0.5) {
			t.Errorf("%s: the slash spans %g of a space", c.what, ba.W())
		}
	}
}

func TestBoxesMapPointerToMusic(t *testing.T) {
	l := Engrave(one(t, "treble", "| qC5 qD5 hE5 | wF5"), Options{})
	sb := l.Staves[0]
	if p := sb.Diatonic(sb.Top + 4); p != (score.Pitch{Step: 2, Octave: 4}).Diatonic() {
		t.Errorf("the bottom line is %d", p)
	}
	if p := sb.Diatonic(sb.Y(40)); p != 40 {
		t.Error("Y and Diatonic disagree")
	}
	bb := l.Bar(1)
	if bb == nil || l.Bar(2) == nil || l.Bar(3) != nil {
		t.Fatal("bar boxes")
	}
	for _, off := range []float64{0, 0.25, 0.5, 0.75, 1} {
		if got := bb.OffsetAt(bb.XAt(off)); math.Abs(got-off) > 1e-9 {
			t.Errorf("offset %g maps back to %g", off, got)
		}
	}
	if l.BarAt((bb.X0+bb.X1)/2) != bb || l.StaffAt(sb.Top+2) == nil {
		t.Error("lookups")
	}
	for _, eb := range l.Events {
		if eb.Box.Empty() || eb.Box.Min.X > eb.X+0.01 {
			t.Errorf("event %+v box %v", eb.Ref, eb.Box)
		}
	}
	// Everything is inside the layout.
	b := l.Bounds()
	if b.Min.X < 0 || b.Min.Y < 0 || b.Max.X > l.Width || b.Max.Y > l.Height {
		t.Errorf("bounds %v outside %gx%g", b, l.Width, l.Height)
	}
}

// TestEngraveEveryScore lays out every fixture and every shipped score,
// whole and bar by bar, and checks the lists are sane.
func TestEngraveEveryScore(t *testing.T) {
	var paths []string
	fx, _ := filepath.Glob(filepath.Join("testdata", "*"+score.FileExt))
	paths = append(paths, fx...)
	shipped, _ := filepath.Glob(filepath.Join("..", "..", "..", "resources", "sounds", "*"+score.FileExt))
	paths = append(paths, shipped...)
	if len(paths) < 10 {
		t.Fatalf("only %d scores", len(paths))
	}
	for _, p := range paths {
		s, err := score.LoadFile(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		checkList(t, p, Engrave(s, Options{}))
		for b := 1; b <= s.Measures(); b++ {
			checkList(t, p, Engrave(s, Options{From: b, To: b, Concert: b%2 == 0}))
		}
	}
}

func checkList(t *testing.T, name string, l *List) {
	t.Helper()
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	for _, it := range l.Items {
		if !finite(it.P.X) || !finite(it.P.Y) || !finite(it.Q.X) || !finite(it.Q.Y) {
			t.Fatalf("%s: item %+v is not finite", name, it)
		}
		if it.Kind == ItemGlyph && it.Rune == 0 {
			t.Fatalf("%s: glyph %q has no code point", name, it.Glyph)
		}
	}
	if !(l.Width > 0 && l.Height > 0) {
		t.Fatalf("%s: size %gx%g", name, l.Width, l.Height)
	}
}

// TestNoCollisions engraves random music and checks no notehead,
// accidental or dot runs into another event's, nor an accidental into its
// own chord: whatever is written, the spacing keeps ink apart.
func TestNoCollisions(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	f := glyph.Bravura()
	n := 150
	if os.Getenv("SCORE_ENGRAVE_LONG") != "" {
		n = 5000
	}
	for iter := 0; iter < n; iter++ {
		s := randomScore(r)
		l := Engrave(s, Options{})
		type box struct {
			b    Rect
			it   *Item
			role Role
		}
		byStaff := map[[2]int][]box{}
		for i := range l.Items {
			it := &l.Items[i]
			switch it.Role {
			case RoleNotehead, RoleAccidental, RoleDot:
			default:
				continue
			}
			k := [2]int{it.Ref.Line, it.Ref.Staff}
			byStaff[k] = append(byStaff[k], box{itemBounds(f, it), it, it.Role})
		}
		for _, bs := range byStaff {
			for i := range bs {
				for j := i + 1; j < len(bs); j++ {
					a, b := bs[i], bs[j]
					same := a.it.Ref.Bar == b.it.Ref.Bar && a.it.Ref.Voice == b.it.Ref.Voice && a.it.Ref.Event == b.it.Ref.Event
					if same && (a.role == RoleNotehead && b.role == RoleNotehead || a.role == RoleDot || b.role == RoleDot) {
						continue // a chord's own heads touch on a second; its dots are its own
					}
					if a.role == RoleNotehead && b.role == RoleNotehead && a.it.P == b.it.P {
						continue // a unison shared by two voices
					}
					if a.role == RoleDot && b.role == RoleDot && a.it.P.Y == b.it.P.Y && math.Abs(a.it.P.X-b.it.P.X) < 1e-9 {
						continue // the shared unison's dots
					}
					// 0.08 a side: a head beside a shared unison overlaps it
					// by a stem's width, as a chord's second does.
					if overlap(a.b, b.b, 0.08) {
						t.Fatalf("iteration %d: %v of bar %d ev %d v%d (%v) hits %v of bar %d ev %d v%d (%v)\n%s",
							iter, a.it.Glyph, a.it.Ref.Bar, a.it.Ref.Event, a.it.Ref.Voice, a.b,
							b.it.Glyph, b.it.Ref.Bar, b.it.Ref.Event, b.it.Ref.Voice, b.b, voicesText(s))
					}
				}
			}
		}
	}
}

func voicesText(s *score.Score) string {
	var b strings.Builder
	for _, l := range s.Lines {
		for _, st := range l.Staves {
			for _, v := range st.Voices {
				b.WriteString(score.FormatVoice(v))
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

// randomScore writes a few bars of random but valid music: random meters
// and keys, chords and rests, dots, accidentals, ties, triplets, grace
// notes, one or two voices.
func randomScore(r *rand.Rand) *score.Score {
	s := score.New("random")
	meters := []string{"4/4", "3/4", "6/8", "2/4", "3+2+2/8", "5/8"}
	m, _ := score.ParseMeter(meters[r.Intn(len(meters))])
	s.Meter[0].Meter = m
	s.Key[0].Fifths = r.Intn(15) - 7
	bars := 2 + r.Intn(3)
	s.Bars = bars
	clefs := []string{"treble", "bass", "alto"}
	l := score.NewLine("L", "keys/grand_piano", clefs[r.Intn(len(clefs))])
	st := l.Staves[0]
	base := clefBottom(st.Clef)
	nv := 1 + r.Intn(2)
	st.Voices = nil
	for v := 0; v < nv; v++ {
		var text strings.Builder
		for b := 0; b < bars; b++ {
			text.WriteString("| ")
			text.WriteString(randomBar(r, m.Length(), base, v, nv))
			text.WriteString("\n")
		}
		pv, err := score.ParseVoice(text.String())
		if err != nil {
			panic(err.Error() + "\n" + text.String())
		}
		st.Voices = append(st.Voices, pv)
	}
	s.Lines = append(s.Lines, l)
	return s
}

func randomBar(r *rand.Rand, length score.Frac, base, voice, nv int) string {
	type val struct {
		text string
		len  score.Frac
	}
	vals := []val{{"h", score.F(1, 2)}, {"q", score.F(1, 4)}, {"e", score.F(1, 8)}, {"s", score.F(1, 16)},
		{"q.", score.F(3, 8)}, {"e.", score.F(3, 16)}, {"h.", score.F(3, 4)}, {"t", score.F(1, 32)}}
	var out []string
	left := length
	for left.Sign() > 0 {
		// A triplet of eighths when a quarter is left.
		if r.Intn(8) == 0 && !left.Less(score.F(1, 4)) {
			out = append(out, "3{"+randomNote(r, "e", base, voice, nv)+" "+randomNote(r, "e", base, voice, nv)+" "+randomNote(r, "e", base, voice, nv)+"}")
			left = left.Sub(score.F(1, 4))
			continue
		}
		v := vals[r.Intn(len(vals))]
		if left.Less(v.len) {
			continue
		}
		left = left.Sub(v.len)
		if r.Intn(5) == 0 {
			out = append(out, v.text+"r")
			continue
		}
		if r.Intn(7) == 0 {
			out = append(out, "/"+randomNote(r, "e", base, voice, nv))
		}
		out = append(out, randomNote(r, v.text, base, voice, nv))
	}
	return strings.Join(out, " ")
}

func randomNote(r *rand.Rand, dur string, base, voice, nv int) string {
	n := 1 + r.Intn(4)
	lo := base - 6
	if nv > 1 {
		lo = base + 2 - voice*6
	}
	var ps []string
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		d := lo + r.Intn(14)
		p := score.FromDiatonic(d)
		p.Alter = int8(r.Intn(5) - 2)
		if r.Intn(2) == 0 {
			p.Alter = 0
		}
		t := p.String()
		if seen[t] {
			continue
		}
		seen[t] = true
		if r.Intn(10) == 0 {
			t += "?"
		}
		ps = append(ps, t)
	}
	return dur + strings.Join(ps, "+")
}

func BenchmarkEngraveDemo(b *testing.B) {
	s, err := score.LoadFile(filepath.Join("..", "..", "..", "resources", "sounds", "score_demo"+score.FileExt))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Engrave(s, Options{})
	}
}

func TestMain(m *testing.M) {
	glyph.Bravura() // parse once, outside any test's timing
	os.Exit(m.Run())
}

// A percussion staff shows each drum at its place with its notehead (a
// hi-hat's x above the staff, the snare in the third space, the kick in
// the bottom one), with no key signature and no accidentals; a key the kit
// map does not name sits at its letter's place.
func TestPercussionStaff(t *testing.T) {
	s := fixture(t, "percussion")
	l := Engrave(s, Options{})
	if got := len(itemsOf(l, RoleKey)); got != 0 {
		t.Errorf("%d key signature items on a percussion staff", got)
	}
	if got := len(itemsOf(l, RoleAccidental)); got != 0 {
		t.Errorf("%d accidentals on a percussion staff", got)
	}
	heads := func(bar, voice, event int) map[string]float64 {
		out := map[string]float64{}
		for _, it := range l.Items {
			if it.Role == RoleNotehead && it.Ref.Bar == bar && it.Ref.Voice == voice && it.Ref.Event == event {
				out[it.Glyph] = it.P.Y
			}
		}
		return out
	}
	// y is staff-relative: the top line at 0, the bottom at 4.
	top := l.Staves[0].Top
	check := func(bar, voice, event int, want map[string]float64) {
		t.Helper()
		got := heads(bar, voice, event)
		if len(got) != len(want) {
			t.Errorf("bar %d voice %d event %d: heads %v, want %v", bar, voice+1, event, got, want)
			return
		}
		for g, y := range want {
			if gy, ok := got[g]; !ok || math.Abs(gy-top-y) > 1e-9 {
				t.Errorf("bar %d voice %d event %d: %s at %v, want %v (all %v)", bar, voice+1, event, g, gy-top, y, got)
			}
		}
	}
	check(1, 0, 0, map[string]float64{"noteheadXBlack": -1}) // hi-hat G5 and crash A5: both x, a second apart
	if n := len(itemsBy(l, RoleNotehead, 1, 0, 0)); n != 2 {
		t.Errorf("hi-hat and crash: %d heads", n)
	}
	check(1, 0, 2, map[string]float64{"noteheadXBlack": -0.5, "noteheadBlack": 1.5}) // hi-hat + snare
	check(1, 0, 5, map[string]float64{"noteheadCircleX": -0.5})                      // open hi-hat
	check(1, 1, 0, map[string]float64{"noteheadBlack": 3.5})                         // kick, bottom space
	check(2, 0, 0, map[string]float64{"noteheadBlack": 0})                           // high tom, top line
	check(2, 0, 10, map[string]float64{"noteheadBlack": 3})                          // low floor tom, G4
	check(2, 1, 2, map[string]float64{"noteheadHalf": 3.5})                          // a half-note kick
	// A key the kit map does not name: C5 (72) on the third space, plain.
	k := one(t, "percussion", "| qC5 qF4 hr |")
	kl := Engrave(k, Options{})
	for _, it := range kl.Items {
		if it.Role == RoleNotehead && it.Ref.Event == 0 && (it.Glyph != "noteheadBlack" || math.Abs(it.P.Y-kl.Staves[0].Top-1.5) > 1e-9) {
			t.Errorf("unmapped C5: %s at %v", it.Glyph, it.P.Y-kl.Staves[0].Top)
		}
	}
}

func itemsOf(l *List, role Role) []Item {
	var out []Item
	for _, it := range l.Items {
		if it.Role == role {
			out = append(out, it)
		}
	}
	return out
}

func itemsBy(l *List, role Role, bar, voice, event int) []Item {
	var out []Item
	for _, it := range l.Items {
		if it.Role == role && it.Ref.Bar == bar && it.Ref.Voice == voice && it.Ref.Event == event {
			out = append(out, it)
		}
	}
	return out
}

// In compound time the 16ths' beam breaks at each eighth of the dotted
// beat: six 16ths are one primary beam and three pairs; in simple time
// four 16ths keep one secondary beam.
func TestSecondaryBeamsBreakInCompoundBeats(t *testing.T) {
	count := func(meter, voice string) int {
		s := one(t, "treble", voice)
		m, _ := score.ParseMeter(meter)
		s.Meter = []score.MeterMark{{Bar: 1, Meter: m}}
		l := Engrave(s, Options{})
		n := 0
		for _, it := range l.Items {
			if it.Role == RoleBeam && it.Ref.Bar == 1 && it.Ref.Event < 6 {
				n++
			}
		}
		return n
	}
	if n := count("6/8", "| sC5 sD5 sE5 sF5 sG5 sA5 q.B5 |"); n != 4 {
		t.Errorf("6/8: %d beams over six 16ths, want 4 (the primary and three pairs)", n)
	}
	if n := count("2/4", "| sC5 sD5 sE5 sF5 qG5 |"); n != 2 {
		t.Errorf("2/4: %d beams over four 16ths, want 2", n)
	}
}

// In two voices an outward stem from a note outside the staff is shortened
// to two and a half spaces; one from inside it keeps the standard octave.
func TestOutwardStemsShorten(t *testing.T) {
	s := one(t, "treble", "| qA5 qD5 hr |", "| qC4 qG4 hr |")
	l := Engrave(s, Options{})
	length := func(voice, event int) float64 {
		for _, it := range l.Items {
			if it.Role == RoleStem && it.Ref.Voice == voice && it.Ref.Event == event && it.Ref.Bar == 1 {
				return math.Abs(it.Q.Y - it.P.Y)
			}
		}
		t.Fatalf("no stem for voice %d event %d", voice, event)
		return 0
	}
	// From the notehead's stem anchor, so a little short of the full value.
	if a, d := length(0, 0), length(0, 1); math.Abs(a-d) < 0.8 || a > 2.6 || d < 3.2 {
		t.Errorf("A5's stem %.2f, D5's %.2f: want about 2.5 and 3.5", a, d)
	}
	if c := length(1, 0); c > 2.6 {
		t.Errorf("the lower voice's C4 stem %.2f: want about 2.5", c)
	}
	// One voice alone: no shortening.
	single := Engrave(one(t, "treble", "| qC4 qr hr |"), Options{})
	for _, it := range single.Items {
		if it.Role == RoleStem && math.Abs(it.Q.Y-it.P.Y) < 3.2 {
			t.Errorf("a single voice's stem %.2f", math.Abs(it.Q.Y-it.P.Y))
		}
	}
}

// Cross-staff notes stand on the other staff of the grand staff, with its
// clef; a beam joining both staves is flat in the gap between them, each
// note's stem reaching it from its own side.
func TestCrossStaffNotes(t *testing.T) {
	s := score.New("t")
	l := score.NewLine("Piano", "keys/piano", "treble")
	l.Staves = append(l.Staves, &score.Staff{Clef: "bass"})
	up, _ := score.ParseVoice("| wE5 |")
	low, _ := score.ParseVoice("| eC3 eG3 eC4(cross=up) eE4(cross=up) hC3 |")
	l.Staves[0].Voices = []*score.Voice{up}
	l.Staves[1].Voices = []*score.Voice{low}
	s.Lines = []*score.Line{l}
	s.Bars = 1
	ls := Engrave(s, Options{})
	treble, bass := ls.Staves[0], ls.Staves[1]
	if d := bass.Top - treble.Top; d < crossMinStep-1e-9 {
		t.Errorf("staves %.2f apart, want at least %.1f for the kneed beam", d, crossMinStep)
	}
	heads := map[int]Item{}
	for _, it := range ls.Items {
		if it.Role == RoleNotehead && it.Ref.Staff == 1 {
			heads[it.Ref.Event] = it
		}
	}
	// C4 is a ledger line under the treble staff: one space under its
	// bottom line; E4 on the bottom line.
	if y := heads[2].P.Y - treble.Top; math.Abs(y-5) > 1e-6 {
		t.Errorf("C4 at %.2f under the treble top, want 5", y)
	}
	if y := heads[3].P.Y - treble.Top; math.Abs(y-4) > 1e-6 {
		t.Errorf("E4 at %.2f, want 4", y)
	}
	if y := heads[0].P.Y - bass.Top; math.Abs(y-2.5) > 1e-6 {
		t.Errorf("C3 stays on the bass staff (its second space): %.2f", y)
	}
	// The beam: flat, between the treble's bottom and the bass's top.
	var beams []Item
	for _, it := range ls.Items {
		if it.Role == RoleBeam && it.Ref.Staff == 1 {
			beams = append(beams, it)
		}
	}
	if len(beams) != 1 {
		t.Fatalf("%d beams", len(beams))
	}
	b := ItemBounds(&beams[0])
	if b.Min.Y < treble.Top+4 || b.Max.Y > bass.Top || b.H() > 0.6 {
		t.Errorf("the beam %v is not flat in the gap (%v..%v)", b, treble.Top+4, bass.Top)
	}
	// Stems: the bass notes' up to it, the crossed ones' down to it.
	for _, it := range ls.Items {
		if it.Role != RoleStem || it.Ref.Staff != 1 || it.Ref.Event > 3 {
			continue
		}
		top, bot := math.Min(it.P.Y, it.Q.Y), math.Max(it.P.Y, it.Q.Y)
		if it.Ref.Event < 2 && math.Abs(top-b.Min.Y) > 0.6 {
			t.Errorf("bass stem %d does not reach up to the beam: %v..%v", it.Ref.Event, top, bot)
		}
		if it.Ref.Event >= 2 && math.Abs(bot-b.Max.Y) > 0.6 {
			t.Errorf("crossed stem %d does not reach down to the beam: %v..%v", it.Ref.Event, top, bot)
		}
	}
	// One ledger line: C4's, at the treble's.
	ledgers := 0
	for _, it := range ls.Items {
		if it.Role == RoleLedger && it.Ref.Staff == 1 {
			ledgers++
			if math.Abs(it.P.Y-(treble.Top+5)) > 1e-6 {
				t.Errorf("a ledger at %.2f, want the treble's first one under", it.P.Y)
			}
		}
	}
	if ledgers != 1 {
		t.Errorf("%d ledger lines", ledgers)
	}
	// A line of one staff has nowhere to cross to: the note stays.
	solo := one(t, "treble", "| qC4(cross=down) qD4 hE4 |")
	sl := Engrave(solo, Options{})
	for _, it := range sl.Items {
		if it.Role == RoleNotehead && it.Ref.Event == 0 && math.Abs(it.P.Y-sl.Staves[0].Top-5) > 1e-6 {
			t.Errorf("C4 moved with no staff to go to: %.2f", it.P.Y-sl.Staves[0].Top)
		}
	}
	if is := solo.Validate(nil); !strings.Contains(fmt.Sprint(is), "no staff below") {
		t.Errorf("validation: %v", is)
	}
}

// A crossed note's accidental is reckoned on the staff it is written on:
// an F# there earlier in the bar needs none again, and a crossed F# needs
// its sharp though its own staff had one.
func TestCrossStaffAccidentals(t *testing.T) {
	s := score.New("t")
	l := score.NewLine("Piano", "keys/piano", "treble")
	l.Staves = append(l.Staves, &score.Staff{Clef: "bass"})
	up, _ := score.ParseVoice("| qF#4 qr hr |")
	low, _ := score.ParseVoice("| qF#2 qr qF#4(cross=up) qF#4(cross=up) |")
	l.Staves[0].Voices = []*score.Voice{up}
	l.Staves[1].Voices = []*score.Voice{low}
	s.Lines = []*score.Line{l}
	s.Bars = 1
	ls := Engrave(s, Options{})
	acc := map[[2]int]int{}
	for _, it := range ls.Items {
		if it.Role == RoleAccidental {
			acc[[2]int{it.Ref.Staff, it.Ref.Event}]++
		}
	}
	if acc[[2]int{0, 0}] != 1 || acc[[2]int{1, 0}] != 1 {
		t.Errorf("each staff's own F#: %v", acc)
	}
	if acc[[2]int{1, 2}] != 0 || acc[[2]int{1, 3}] != 0 {
		t.Errorf("the crossed F#4s follow the treble's F#4: %v", acc)
	}
}

// A crossed note a second from the other staff's note at the same moment
// moves beside it, as two voices would.
func TestCrossStaffNotesSitBesideTheOthers(t *testing.T) {
	s := score.New("t")
	l := score.NewLine("Piano", "keys/piano", "treble")
	l.Staves = append(l.Staves, &score.Staff{Clef: "bass"})
	up, _ := score.ParseVoice("| hE4 hr |")
	low, _ := score.ParseVoice("| qF4(cross=up) qC3 hr |")
	l.Staves[0].Voices = []*score.Voice{up}
	l.Staves[1].Voices = []*score.Voice{low}
	s.Lines = []*score.Line{l}
	s.Bars = 1
	ls := Engrave(s, Options{})
	var e4, f4 Rect
	for _, it := range ls.Items {
		if it.Role == RoleNotehead && it.Ref.Event == 0 {
			if it.Ref.Staff == 0 {
				e4 = ItemBounds(&it)
			} else {
				f4 = ItemBounds(&it)
			}
		}
	}
	if f4.Min.X < e4.Max.X-0.05 {
		t.Errorf("the crossed F4 %v overlaps the treble's E4 %v", f4, e4)
	}
}

// Voices stemmed the same way never share a stem: a third voice's notes
// stand right of the first voice's stem (on one stem they read as one
// voice's chord), and its rests stand beside the first voice's notes at
// one height, not stacked over each stem's tip.
func TestSameWayVoicesStandApart(t *testing.T) {
	l := Engrave(one(t, "treble", "| qE5 qD5 hC5 | qC5 qD5 qE5 qF5 |", "| wC4 | wE4 |", "| qF4 qG4 hA4 | qr qr qr qr |"), Options{})
	f := glyph.Bravura()
	stems := map[[2]int]float64{} // bar, voice → the first event's stem x
	heads := map[[3]int]Rect{}
	for i := range l.Items {
		it := &l.Items[i]
		if it.Role == RoleStem && it.Kind == ItemLine && it.Ref.Bar == 1 {
			stems[[2]int{it.Ref.Event, it.Ref.Voice}] = it.P.X
		}
		if it.Role == RoleNotehead {
			heads[[3]int{it.Ref.Bar, it.Ref.Voice, it.Ref.Event}] = itemBounds(f, it)
		}
	}
	for ev := 0; ev < 3; ev++ {
		v1, ok1 := stems[[2]int{ev, 0}]
		v3, ok3 := stems[[2]int{ev, 2}]
		if !ok1 || !ok3 {
			t.Fatalf("event %d: stems %v", ev, stems)
		}
		if v3 < v1+0.5 {
			t.Errorf("event %d: voice 3's stem at %.2f, voice 1's at %.2f (one stem)", ev, v3, v1)
		}
		// Its head clear of voice 1's stem.
		if h := heads[[3]int{1, 2, ev}]; h.Min.X < v1+0.1 {
			t.Errorf("event %d: voice 3's head from %.2f, voice 1's stem at %.2f", ev, h.Min.X, v1)
		}
	}
	var ys []float64
	for i := range l.Items {
		it := &l.Items[i]
		if it.Role != RoleRest || it.Ref.Bar != 2 || it.Ref.Voice != 2 {
			continue
		}
		ys = append(ys, it.P.Y)
		rb := itemBounds(f, it)
		for j := range l.Items {
			o := &l.Items[j]
			if o.Ref.Voice != 0 || o.Ref.Bar != 2 || (o.Role != RoleNotehead && o.Role != RoleStem) {
				continue
			}
			if overlap(rb, itemBounds(f, o), 0.05) {
				t.Errorf("voice 3's rest %d touches voice 1's %v %d", it.Ref.Event, o.Role, o.Ref.Event)
			}
		}
	}
	if len(ys) != 4 {
		t.Fatalf("%d rests", len(ys))
	}
	for _, y := range ys[1:] {
		if y != ys[0] {
			t.Errorf("voice 3's rests at %v: not one height", ys)
			break
		}
	}
}

// A tempo mark rises over an ornament that ends just short of its words:
// in the crowded fixture's bar 3 (a 16th acciaccatura and a trill on A5
// after a time signature, three voices below) "accel." sat on the trill's
// line, its last letter in the trill's swash.
func TestTempoTextClearsAnOrnament(t *testing.T) {
	l := Engrave(fixture(t, "crowded"), Options{})
	f := glyph.Bravura()
	var tempo, orn []Rect
	for i := range l.Items {
		it := &l.Items[i]
		switch {
		case it.Role == RoleTempo && it.Ref.Bar == 3:
			tempo = append(tempo, itemBounds(f, it))
		case it.Role == RoleOrnament && it.Ref.Bar == 3:
			orn = append(orn, itemBounds(f, it))
		}
	}
	if len(tempo) == 0 || len(orn) == 0 {
		t.Fatalf("tempo %d, ornaments %d", len(tempo), len(orn))
	}
	for _, a := range tempo {
		for _, b := range orn {
			// Not over it, and not beside it on its line.
			grown := Rect{Min: Point{X: b.Min.X - 0.4, Y: b.Min.Y}, Max: Point{X: b.Max.X + 0.4, Y: b.Max.Y}}
			if overlap(a, grown, 0) {
				t.Errorf("the tempo mark %v meets the trill %v", a, b)
			}
		}
	}
}

// Jazz marks stand beside their note: a scoop or a plop left of it and
// its accidentals, a doit or a fall right of it and its dots, a bend over
// the stem; none touches the note's own ink, and spacing leaves the room.
func TestJazzMarks(t *testing.T) {
	l := Engrave(fixture(t, "jazz"), Options{})
	f := glyph.Bravura()
	type evKey struct{ bar, ev int }
	jazz := map[evKey][]*Item{}
	own := map[evKey][]Rect{} // heads, accidentals, dots
	all := []Rect{}
	stemTop := map[evKey]float64{}
	for i := range l.Items {
		it := &l.Items[i]
		k := evKey{it.Ref.Bar, it.Ref.Event}
		if _, ok := stemTop[k]; !ok {
			stemTop[k] = math.Inf(1)
		}
		switch {
		case it.Role == RoleArticulation && strings.HasPrefix(it.Glyph, "brass"):
			jazz[k] = append(jazz[k], it)
		case it.Role == RoleNotehead || it.Role == RoleAccidental || it.Role == RoleDot:
			own[k] = append(own[k], itemBounds(f, it))
			all = append(all, itemBounds(f, it))
		case it.Role == RoleStem || it.Role == RoleFlag || it.Role == RoleBeam:
			all = append(all, itemBounds(f, it))
			if it.Role == RoleStem {
				stemTop[k] = math.Min(stemTop[k], itemBounds(f, it).Min.Y)
			}
		}
	}
	want := map[evKey][]string{
		{1, 0}: {"brassScoop"}, {1, 1}: {"brassPlop"}, {1, 2}: {"brassDoitShort"}, {1, 3}: {"brassFallLipShort"},
		{2, 0}: {"brassBend"}, {2, 1}: {"brassFallLipMedium"}, {2, 2}: {"brassDoitMedium"},
		{3, 0}: {"brassScoop", "brassFallLipShort"}, {3, 2}: {"brassPlop"}, {3, 3}: {"brassScoop", "brassDoitShort"},
	}
	for k, names := range want {
		var got []string
		for _, it := range jazz[k] {
			got = append(got, it.Glyph)
		}
		sort.Strings(got)
		sort.Strings(names)
		if strings.Join(got, ",") != strings.Join(names, ",") {
			t.Errorf("bar %d event %d: %v, want %v", k.bar, k.ev+1, got, names)
		}
		for _, it := range jazz[k] {
			jb := itemBounds(f, it)
			for _, r := range all {
				if overlap(jb, r, 0.05) {
					t.Errorf("bar %d event %d: %s touches the music at %v", k.bar, k.ev+1, it.Glyph, r)
				}
			}
			var ink Rect
			for i, r := range own[k] {
				if i == 0 {
					ink = r
				} else {
					ink = ink.Union(r)
				}
			}
			switch it.Glyph {
			case "brassScoop", "brassPlop":
				if jb.Max.X > ink.Min.X {
					t.Errorf("bar %d event %d: the %s ends at %.2f, right of the note's ink from %.2f", k.bar, k.ev+1, it.Glyph, jb.Max.X, ink.Min.X)
				}
			case "brassBend":
				if top := math.Min(ink.Min.Y, stemTop[k]); jb.Max.Y > top {
					t.Errorf("bar %d event %d: the bend reaches %.2f, under the note's top at %.2f", k.bar, k.ev+1, jb.Max.Y, top)
				}
			default:
				if jb.Min.X < ink.Max.X {
					t.Errorf("bar %d event %d: the %s starts at %.2f, inside the note's ink to %.2f", k.bar, k.ev+1, it.Glyph, jb.Min.X, ink.Max.X)
				}
			}
		}
	}
}
