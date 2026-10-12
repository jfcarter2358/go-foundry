package interpret

import (
	"fmt"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/tracker"
)

// heard is the notes as "pitch@tick+ticks" words in onset order.
func heard(p *Performance) string {
	var w []string
	for _, n := range p.Notes {
		w = append(w, fmt.Sprintf("%s@%d+%d", score.SpellMIDI(n.MIDI, 0, 1), n.Tick, n.Ticks))
	}
	return strings.Join(w, " ")
}

// withVoice replaces the first voice with text parsed as written, for
// marks the YAML helper cannot quote ("con sord.").
func withVoice(t *testing.T, s *score.Score, text string) {
	t.Helper()
	v, err := score.ParseVoice(text)
	if err != nil {
		t.Fatal(err)
	}
	s.Lines[0].Staves[0].Voices[0] = v
}

func performWith(t *testing.T, s *score.Score, insts ...*tracker.Instrument) *Performance {
	t.Helper()
	p, err := Interpret(s, Options{Instruments: insts})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestArticulationLengths(t *testing.T) {
	s := one(t, "bars: 3", "| qC4(stacc) qD4(staccmo) qE4(portato) qF4(stacc,ten) | qG4(marc) qA4(ten) qB4(acc) qC5 | hC4~ hC4(stacc) |")
	p := perform(t, s)
	want := "C4@0+480 D4@960+240 E4@1920+720 F4@2880+720 G4@3840+768 A4@4800+960 B4@5760+960 C5@6720+960 C4@7680+2880"
	if got := heard(p); got != want {
		t.Errorf("lengths:\n got %s\nwant %s", got, want)
	}
	// The instrument's own staccato length.
	in := tracker.NewInstrument("v", "v")
	in.Playback.Staccato = 0.4
	if got := performWith(t, s, in).Notes[0].Ticks; got != 384 {
		t.Errorf("staccato at 0.4 of a quarter is %d ticks", got)
	}
	if !p.InstrumentDependent[0] {
		t.Error("a line with staccatos depends on its instrument")
	}
	plain := one(t, "bars: 1", "| qC4 qD4(>) hE4 |")
	if perform(t, plain).InstrumentDependent[0] {
		t.Error("a line with nothing an instrument shapes does not")
	}
}

func TestArticulationAndTechniqueWords(t *testing.T) {
	s := one(t, "bars: 2", "| qC4 |")
	withVoice(t, s, `| qC4(tech="con sord.") qC4(tech=pizz.) qC4(tech=arco) qC4(stacc) | qC4(tech="senza sord.") qC4(sfz) qC4(tech=pizz.) qC4(tech=ord.) |`)
	p := perform(t, s)
	var got []string
	for _, n := range p.Notes {
		got = append(got, n.Articulation+"/"+n.Technique)
	}
	// "con sord." survives pizz./arco; senza sord. ends it; ord. ends all.
	want := "/con sord. /pizz. /con sord. staccato/con sord. / sfz/ /pizz. /"
	if strings.Join(got, " ") != want {
		t.Errorf("words:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
}

func TestSlursPlayLegato(t *testing.T) {
	s := one(t, "bars: 2", "| qC4(slur) qD4 qE4(stacc) qF4(slur_end) | qG4 qr hA4 |")
	p := perform(t, s)
	// Each slurred note runs a 64th (60 ticks) into the next; the slur's
	// last does not; a staccato under it is a portato.
	want := "C4@0+1020 D4@960+1020 E4@1920+720 F4@2880+960 G4@3840+960 A4@5760+1920"
	if got := heard(p); got != want {
		t.Errorf("slur:\n got %s\nwant %s", got, want)
	}
	for i, w := range []string{"legato", "legato", "staccato", "", ""} {
		if p.Notes[i].Articulation != w {
			t.Errorf("note %d articulation %q, want %q", i, p.Notes[i].Articulation, w)
		}
	}
}

func TestGlissandoStyles(t *testing.T) {
	voice := "| hC4(gliss) hG4 | hG4(gliss=wavy) hC4 |"
	for _, c := range []struct {
		style, key string
		want       string
	}{
		{"chromatic", "0", "C4@0+480 C#4@480+240 D4@720+240 D#4@960+240 E4@1200+240 F4@1440+240 F#4@1680+240 G4@1920+1920 G4@3840+480 " +
			"F#4@4320+240 F4@4560+240 E4@4800+240 D#4@5040+240 D4@5280+240 C#4@5520+240 C4@5760+1920"},
		{"diatonic", "0", "C4@0+480 D4@480+480 E4@960+480 F4@1440+480 G4@1920+1920 G4@3840+480 F4@4320+480 E4@4800+480 D4@5280+480 C4@5760+1920"},
		// In G major the run takes the key's F#.
		{"diatonic", "1", "C4@0+480 D4@480+480 E4@960+480 F#4@1440+480 G4@1920+1920 G4@3840+480 F#4@4320+480 E4@4800+480 D4@5280+480 C4@5760+1920"},
	} {
		s := one(t, "bars: 2\nkey: [{bar: 1, fifths: "+c.key+"}]", voice)
		s.Lines[0].Gliss = c.style
		p := perform(t, s)
		if got := heard(p); got != c.want {
			t.Errorf("%s in %s:\n got %s\nwant %s", c.style, c.key, got, c.want)
		}
		if p.InstrumentDependent[0] {
			t.Errorf("%s: a line that names its style does not depend on the instrument for it", c.style)
		}
	}
	// A slide: one note bending a fifth up over the last three quarters of
	// its half, carrying on as G4 (tied over), then down again.
	s := one(t, "bars: 2", "| hC4(gliss) hG4~ | hG4(gliss=wavy) hC4 |")
	s.Lines[0].Gliss = "slide"
	p := perform(t, s)
	if len(p.Notes) != 1 {
		t.Fatalf("a slide is one note: %s", heard(p))
	}
	n := p.Notes[0]
	if n.MIDI != 60 || n.Tick != 0 || n.Ticks != 7680 {
		t.Errorf("slide note %+v", n)
	}
	for tick, want := range map[float64]float32{0: 0, 480: 0, 1200: 3.5, 1920: 7, 4000: 7, 4320: 7, 5040: 3.5, 5760: 0, 7000: 0} {
		if got := n.BendAt(tick); !near32(got, want) {
			t.Errorf("bend at %v: %v, want %v", tick, got, want)
		}
	}
	// With no style of its own the line plays its instrument's.
	s.Lines[0].Gliss = ""
	harp := tracker.NewInstrument("harp", "harp")
	harp.Playback.Gliss = tracker.GlissDiatonic
	q := performWith(t, s, harp)
	if !strings.HasPrefix(heard(q), "C4@0+480 D4@480+480") || !q.InstrumentDependent[0] {
		t.Errorf("the harp's own gliss: %s", heard(q))
	}
	if r := perform(t, s); len(r.Notes) != 1 {
		t.Errorf("no instrument known: a slide (%s)", heard(r))
	}
	// For an export that cannot bend, a slide is the chromatic run.
	if r, _ := Interpret(s, Options{NoSlides: true}); len(r.Notes) != 15 || r.Notes[1].MIDI != 61 {
		t.Errorf("NoSlides: %s", heard(r))
	}
	// A chord glides note to note from the top.
	c := one(t, "bars: 1", "| hC4+E4(gliss) hG4+B4 |")
	c.Lines[0].Gliss = "slide"
	cp := perform(t, c)
	if len(cp.Notes) != 2 || cp.Notes[0].BendAt(1920) != 7 || cp.Notes[1].BendAt(1920) != 7 {
		t.Errorf("chord slide: %s %+v", heard(cp), cp.Notes)
	}
}

func TestGraceTiming(t *testing.T) {
	voice := "| qC4 /eD4 qE4 ^eF4 qG4 qA4 |"
	for _, c := range []struct {
		header, want string
	}{
		{"", "C4@0+840 D4@840+120 E4@960+960 F4@1920+480 G4@2400+480 A4@2880+960"},
		{"\nplayback: {graces: on}", "C4@0+960 D4@960+480 E4@1440+480 F4@1920+480 G4@2400+480 A4@2880+960"},
		{"\nplayback: {graces: before}", "C4@0+840 D4@840+120 E4@960+840 F4@1800+120 G4@1920+960 A4@2880+960"},
	} {
		s := one(t, "bars: 1"+c.header, voice)
		if got := heard(perform(t, s)); got != c.want {
			t.Errorf("graces%q:\n got %s\nwant %s", c.header, got, c.want)
		}
	}
	// A mark on the group wins over the score's setting.
	s := one(t, "bars: 1\nplayback: {graces: on}", "| qC4 /eD4(before_beat) qE4 ^eF4 qG4 qA4 |")
	if got := heard(perform(t, s)); !strings.HasPrefix(got, "C4@0+840 D4@840+120 E4@960+960") {
		t.Errorf("before_beat: %s", got)
	}
}

func TestOrnaments(t *testing.T) {
	for _, c := range []struct {
		name, header, voice, want string
	}{
		{"mordent", "", "| qD4(mord) qD4(imord) qD4(turn) qD4(iturn) |",
			"D4@0+120 C4@120+120 D4@240+720 D4@960+120 E4@1080+120 D4@1200+720 " +
				"E4@1920+240 D4@2160+240 C4@2400+240 D4@2640+240 C4@2880+240 D4@3120+240 E4@3360+240 D4@3600+240"},
		{"the key's neighbours", "\nkey: [{bar: 1, fifths: 2}]", "| qB4(mord) qE4(imord) hr |",
			"B4@0+120 A4@120+120 B4@240+720 E4@960+120 F#4@1080+120 E4@1200+720"},
		{"an accidental earlier in the bar", "", "| qF#4 qE4(imord) qG4(mord) qG4(mord=n) |",
			"F#4@0+960 E4@960+120 F#4@1080+120 E4@1200+720 G4@1920+120 F#4@2040+120 G4@2160+720 G4@2880+120 F4@3000+120 G4@3120+720"},
		{"a turn's own accidentals", "", "| qD4(turn=b/#) h.r |", "D#4@0+240 D4@240+240 C#4@480+240 D4@720+240"},
	} {
		s := one(t, "bars: 1"+c.header, c.voice)
		got := heard(perform(t, s))
		// Spelling aside: compare MIDI numbers through the spelled names.
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
	// A trill: about 12 notes a second, an odd count, starting and ending
	// on the note, alternating with the note above (F#4 when marked so).
	s := one(t, "bars: 1\ntempo: [{bar: 1, bpm: 60}]", "| hE4(tr=#) hE4(tr) |")
	p := perform(t, s)
	var first, second []int
	for _, n := range p.Notes {
		if n.Tick < 1920 {
			first = append(first, n.MIDI)
		} else {
			second = append(second, n.MIDI)
		}
	}
	if len(first) != 25 || len(second) != 25 {
		t.Fatalf("two seconds of trill: %d and %d notes", len(first), len(second))
	}
	for i := range first {
		w1, w2 := 64, 64
		if i%2 == 1 {
			w1, w2 = 66, 65
		}
		if first[i] != w1 || second[i] != w2 {
			t.Fatalf("trill %d: %d / %d, want %d / %d", i, first[i], second[i], w1, w2)
		}
	}
	if p.Notes[0].Velocity != p.Notes[24].Velocity || p.Notes[24].Tick+p.Notes[24].Ticks != 1920 {
		t.Errorf("the trill keeps its level and length: %+v", p.Notes[24])
	}
}

func TestTremolos(t *testing.T) {
	s := one(t, "bars: 2", "| qC4(trem=2) qD4(trem) hr | hC4(trem2=1) hE4 |")
	p := perform(t, s)
	var b strings.Builder
	b.WriteString("C4@0+240 C4@240+240 C4@480+240 C4@720+240")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, " D4@%d+120", 960+120*i)
	}
	for i := 0; i < 8; i++ {
		n := "C4"
		if i%2 == 1 {
			n = "E4"
		}
		fmt.Fprintf(&b, " %s@%d+480", n, 3840+480*i)
	}
	if got := heard(p); got != b.String() {
		t.Errorf("tremolos:\n got %s\nwant %s", got, b.String())
	}
	// An unmeasured tremolo on strings with a tremolo articulation is that.
	vln := tracker.NewInstrument("vln", "vln")
	vln.Articulations = []tracker.Articulation{{Name: "tremolo"}}
	q := performWith(t, s, vln)
	if got := heard(q); !strings.Contains(got, "D4@960+960") || q.Notes[4].Articulation != "tremolo" {
		t.Errorf("tremolo articulation: %s (%q)", got, q.Notes[4].Articulation)
	}
}

func TestArpeggios(t *testing.T) {
	s := one(t, "bars: 1", "| qC4+E4+G4(arp) qC4+E4+G4(arp=down) eC4+G4(arp) e_ qr |")
	got := heard(perform(t, s))
	want := "C4@0+960 E4@60+900 G4@120+840 G4@960+960 E4@1020+900 C4@1080+840 C4@1920+480 G4@1980+420"
	if got != want {
		t.Errorf("arpeggios:\n got %s\nwant %s", got, want)
	}
}

func TestPedalSustains(t *testing.T) {
	s := one(t, "bars: 3", "| hC4(ped) qD4 qE4(ped) | wF4(ped_end) | qG4 qA4 hr |")
	got := heard(perform(t, s))
	// C4 and D4 are held to the re-pedal at beat 4; E4 to the pedal's end
	// with bar 2; G4 and A4 are after it.
	want := "C4@0+2880 D4@1920+960 E4@2880+4800 F4@3840+3840 G4@7680+960 A4@8640+960"
	if got != want {
		t.Errorf("pedal:\n got %s\nwant %s", got, want)
	}
}

// Ottava lines, clefs and transposition change what is written, never
// what sounds: the stored pitches play.
func TestWritingChangesNoPitch(t *testing.T) {
	s := one(t, "bars: 1", "| qC6(8va) qD6 qE6(ottava_end) qF6 |")
	s.Lines[0].Staves[0].Transpose = score.Interval{Diatonic: -1, Chromatic: -2}
	s.Lines[0].Staves[0].Clefs = []score.ClefChange{{At: score.Pos{Bar: 1, Offset: score.F(1, 2)}, Clef: "bass"}}
	s.Concert = true
	p := perform(t, s)
	if got := heard(p); got != "C6@0+960 D6@960+960 E6@1920+960 F6@2880+960" {
		t.Errorf("played %s", got)
	}
}
