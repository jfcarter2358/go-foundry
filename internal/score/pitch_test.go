package score

import "testing"

func TestPitchMIDIAndSpelling(t *testing.T) {
	for s, midi := range map[string]int{
		"C4": 60, "A4": 69, "B#3": 60, "Cb4": 59, "F#5": 78, "Bb4": 70, "C##4": 62, "Ebb3": 50, "C-1": 0, "G9": 127, "Fx4": 67, "En4": 64,
	} {
		p, err := ParsePitch(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if p.MIDI() != midi {
			t.Errorf("%s is MIDI %d, want %d", s, p.MIDI(), midi)
		}
	}
	for _, s := range []string{"C4", "Bb4", "F#5", "C##4", "Ebb3", "C-1", "B#3"} {
		p, _ := ParsePitch(s)
		if p.String() != s {
			t.Errorf("%s prints as %s", s, p)
		}
	}
	for _, bad := range []string{"H4", "c4", "C", "C10", "C#b4", "C###4", "C-2", "Cn#4"} {
		if _, err := ParsePitch(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestKeyAlterAndInKey(t *testing.T) {
	// G major: F sharp. B-flat major: B and E flat. C# major: everything sharp.
	if KeyAlter(1, 3) != 1 || KeyAlter(1, 0) != 0 {
		t.Error("G major")
	}
	if KeyAlter(-2, 6) != -1 || KeyAlter(-2, 2) != -1 || KeyAlter(-2, 5) != 0 {
		t.Error("B-flat major")
	}
	for st := 0; st < 7; st++ {
		if KeyAlter(7, st) != 1 || KeyAlter(-7, st) != -1 {
			t.Error("seven sharps or flats")
		}
	}
	// Requirement 9: a click on the F line in G major writes F#.
	f5 := Pitch{Step: 3, Octave: 5}
	if p := InKey(f5.Diatonic(), 1); p.String() != "F#5" {
		t.Errorf("F line in G major writes %s", p)
	}
}

func TestSpellMIDI(t *testing.T) {
	cases := []struct {
		midi, fifths, dir int
		want              string
	}{
		{66, 1, 0, "F#4"},  // in key
		{70, -2, 0, "Bb4"}, // in key
		{61, 0, 1, "C#4"},  // up: sharp
		{61, 0, -1, "Db4"}, // down: flat
		{63, -3, 1, "Eb4"}, // the key's own spelling wins over direction
		{60, 0, 0, "C4"},
		{11, 0, 0, "B-1"},
	}
	for _, c := range cases {
		if got := SpellMIDI(c.midi, c.fifths, c.dir).String(); got != c.want {
			t.Errorf("SpellMIDI(%d, %d, %d) = %s, want %s", c.midi, c.fifths, c.dir, got, c.want)
		}
	}
	for m := 0; m < 128; m++ {
		for f := -7; f <= 7; f++ {
			if p := SpellMIDI(m, f, 1); p.MIDI() != m {
				t.Fatalf("SpellMIDI(%d, %d) = %s, MIDI %d", m, f, p, p.MIDI())
			}
		}
	}
}

func TestIntervals(t *testing.T) {
	c4, _ := ParsePitch("C4")
	for s, want := range map[string]string{"+m3": "Eb4", "+M3": "E4", "-M2": "Bb3", "+P5": "G4", "-P8": "C3", "+A4": "F#4", "+d5": "Gb4", "+M9": "D5"} {
		iv, err := ParseInterval(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		if got := c4.Transpose(iv).String(); got != want {
			t.Errorf("C4 %s = %s, want %s", s, got, want)
		}
		if iv.String() != s {
			t.Errorf("%s prints as %s", s, iv)
		}
		if back := IntervalBetween(c4, c4.Transpose(iv)); back != iv {
			t.Errorf("%s: interval back is %v", s, back)
		}
	}
	if iv, _ := ParseInterval("M2"); iv != (Interval{1, 2}) {
		t.Error("an unsigned interval goes up")
	}
	for _, bad := range []string{"P3", "M5", "x", "+m", "0"} {
		if _, err := ParseInterval(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
	// A B-flat clarinet: sounds a major second below written.
	bb := Interval{-1, -2}
	if bb.String() != "-M2" {
		t.Errorf("clarinet transposition prints %s", bb)
	}
}

func TestDurations(t *testing.T) {
	for d, want := range map[Duration]Frac{
		{Base: Quarter}:          F(1, 4),
		{Base: Quarter, Dots: 1}: F(3, 8),
		{Base: Half, Dots: 2}:    F(7, 8),
		{Base: Breve}:            Whole(2),
		{Base: D256}:             F(1, 256),
		{Base: Eighth, Dots: 3}:  F(15, 64),
	} {
		if !d.Value().Eq(want) {
			t.Errorf("%v is %v, want %v", d, d.Value(), want)
		}
		if back, ok := DurationFor(want); !ok || back != d {
			t.Errorf("DurationFor(%v) = %v", want, back)
		}
	}
}

func TestMeters(t *testing.T) {
	for s, beats := range map[string][]Frac{
		"4/4":     {F(1, 4), F(1, 4), F(1, 4), F(1, 4)},
		"6/8":     {F(3, 8), F(3, 8)},
		"3+2+2/8": {F(3, 8), F(1, 4), F(1, 4)},
		"3/8":     {F(1, 8), F(1, 8), F(1, 8)},
		"C|":      {F(1, 2), F(1, 2)},
	} {
		m, err := ParseMeter(s)
		if err != nil {
			t.Fatal(err)
		}
		if m.String() != s {
			t.Errorf("%s prints as %s", s, m)
		}
		got := m.Beats()
		if len(got) != len(beats) {
			t.Errorf("%s has beats %v, want %v", s, got, beats)
			continue
		}
		sum := Frac{}
		for i := range got {
			if got[i] != beats[i] {
				t.Errorf("%s beat %d is %v, want %v", s, i, got[i], beats[i])
			}
			sum = sum.Add(got[i])
		}
		if !sum.Eq(m.Length()) {
			t.Errorf("%s beats add to %v, bar is %v", s, sum, m.Length())
		}
	}
	for _, bad := range []string{"4", "4/3", "0/4", "x/4", "3+/8"} {
		if _, err := ParseMeter(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestPositions(t *testing.T) {
	s := New("t")
	s.Bars = 8
	m, _ := ParseMeter("3+2+2/8")
	s.Meter = append(s.Meter, MeterMark{Bar: 5, Meter: m})
	cases := map[string]Pos{
		"1":       {Bar: 1, Offset: F(0, 1)},
		"2:3":     {Bar: 2, Offset: F(1, 2)},
		"2:2+1/2": {Bar: 2, Offset: F(3, 8)},
		"5:2":     {Bar: 5, Offset: F(3, 8)},
		"5:3+1/2": {},
	}
	cases["5:3+1/2"] = Pos{Bar: 5, Offset: F(3, 8).Add(F(1, 4)).Add(F(1, 8))}
	for text, want := range cases {
		got, err := s.ParsePos(text)
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		if got.Bar != want.Bar || !got.Offset.Eq(want.Offset) {
			t.Errorf("%s = %v, want %v", text, got, want)
		}
		if back := s.FormatPos(got); back != text {
			t.Errorf("%v formats as %s, want %s", got, back, text)
		}
	}
	for _, bad := range []string{"0", "x", "2:0", "2:", "-1:2"} {
		if _, err := s.ParsePos(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

// A transposition never needs a triple sharp or flat, and intervals past
// doubly augmented or diminished are not intervals a file may hold.
func TestTransposeStaysWritable(t *testing.T) {
	gss, _ := ParsePitch("G##4")
	a2, _ := ParseInterval("+A2")
	if got := gss.Transpose(a2); got.MIDI() != gss.MIDI()+3 || got.Alter > 2 || got.Alter < -2 {
		t.Errorf("G##4 up an augmented second: %v (MIDI %d)", got, got.MIDI())
	}
	fbb, _ := ParsePitch("Fbb4")
	d2 := Interval{Diatonic: -1, Chromatic: 0}
	if got := fbb.Transpose(d2); got.MIDI() != fbb.MIDI() || got.Alter < -2 {
		t.Errorf("Fbb4 by a dim. unison-ish step: %v", got)
	}
	for _, s := range []string{"+AAA4", "ddd3", "-ddd5"} {
		if _, err := ParseInterval(s); err == nil {
			t.Errorf("%s parsed", s)
		}
	}
	for _, s := range []string{"+AA4", "dd3", "-dd5", "+A1", "-M2", "+P8"} {
		iv, err := ParseInterval(s)
		if err != nil || !iv.Valid() {
			t.Errorf("%s: %v", s, err)
		}
	}
	if (Interval{Diatonic: 2, Chromatic: 0}).Valid() {
		t.Error("a third of no semitones is valid")
	}
}
