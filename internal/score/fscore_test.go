package score

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/tracker"
	"foundry/internal/tracker/fx"
)

const sampleFile = `version: 1
title: Hangar Theme
composer: J. Carter
bars: 4
pickup: 1/4
tempo: [{bar: 1, bpm: 92, text: Andante}, {bar: 3, beat: "2+1/2", bpm: 60, unit: q., ramp: true}]
meter: [{bar: 1, sig: 4/4}, {bar: 3, sig: 3+2+2/8}]
key: [{bar: 1, fifths: -2, mode: major}]
sections: [{name: intro, bar: 1}, {name: theme, bar: 3, loop: true}]
markers: [{name: hit, bar: 2, beat: "3"}]
repeats: [{start: 2, end: 3}]
params: [{name: intensity, default: 0.5}]
bindings:
  - {param: intensity, line: Violin I, target: volume, from: 0.2, to: 1}
lines:
  - name: Violin I
    instrument: strings/violin_section
    volume: 0.8
    pan: -0.3
    sends: {a: 0.3}
    inserts: [{fx: eq, low: -2, high: 1.5}]
    staves:
      - clef: treble
        voices:
          - |
            | qBb4(p)
            | q.Bb4 eC5 hD5~
            | 3{eD5 eEb5 eD5} qC5 q.Bb4
            | q.F5 qG5 qA5
    lanes:
      volume: [{at: "2", value: 1}, {at: "2:3", to: "3", value: 0.6}]
  - name: Piano
    instrument: keys/grand_piano
    staves:
      - clef: treble
        voices:
          - "| qF4+Bb4+D5 |"
          - "| q_ |"
      - clef: bass
        transpose: -P8
        voices: ["| qBb2 |"]
buses:
  a: {fx: [{fx: reverb, size: 0.7}], return: 0.25}
  b: {fx: [{fx: delay, beats: 0.75, ping_pong: true}], return: 0.2}
master: [{fx: compressor, threshold: -12}]
`

func TestParseSampleFile(t *testing.T) {
	s, err := Parse([]byte(sampleFile))
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "Hangar Theme" || s.Measures() != 4 || !s.Pickup.Eq(F(1, 4)) {
		t.Errorf("header: %+v", s)
	}
	if len(s.Tempo) != 2 || s.Tempo[1].At.Bar != 3 || !s.Tempo[1].At.Offset.Eq(F(3, 8).Add(F(1, 8))) || s.Tempo[1].Unit != (Duration{Base: Quarter, Dots: 1}) || !s.Tempo[1].Ramp {
		t.Errorf("tempo: %+v", s.Tempo)
	}
	if got := s.Tempo[1].QuarterBPM(); got != 90 {
		t.Errorf("60 dotted quarters a minute is %v quarters", got)
	}
	if s.MeterAt(3).String() != "3+2+2/8" || s.KeyAt(4).Fifths != -2 {
		t.Error("meter or key map")
	}
	if !s.BarLength(1).Eq(F(1, 4)) || !s.BarLength(3).Eq(F(7, 8)) {
		t.Errorf("bar lengths %v %v", s.BarLength(1), s.BarLength(3))
	}
	vn := s.Lines[0]
	if vn.Volume != 0.8 || vn.SendA != 0.3 || vn.SendB != 0 || vn.Inserts[0].Kind != "eq" || vn.Inserts[0].Param("high", 0) != 1.5 {
		t.Errorf("line: %+v", vn)
	}
	if vol := vn.Lane("volume"); vol == nil || len(vol.Points) != 2 || !vol.Points[1].HasTo || vol.Points[1].To.Bar != 3 {
		t.Errorf("lane: %+v", vn.Lanes)
	}
	pno := s.Lines[1]
	if len(pno.Staves) != 2 || len(pno.Staves[0].Voices) != 2 || pno.Staves[1].Transpose != (Interval{-7, -12}) || pno.Volume != 1 {
		t.Errorf("piano: %+v", pno)
	}
	if s.Buses.B.FX[0].Param("ping_pong", 0) != 1 || s.Master[0].Param("threshold", 0) != -12 {
		t.Error("buses")
	}
	if s.Bindings[0].Target != tracker.BindVolume {
		t.Error("binding")
	}
	if issues := s.Validate(nil); HasErrors(issues) {
		t.Errorf("sample has errors: %v", issues)
	}
}

func TestFileRoundTrip(t *testing.T) {
	s, err := Parse([]byte(sampleFile))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if !reflect.DeepEqual(normScore(s), normScore(back)) {
		t.Fatalf("round trip changed the score:\n%s", b)
	}
	b2, _ := Marshal(back)
	if string(b2) != string(b) {
		t.Errorf("marshal is not a fixpoint:\n%s\n----\n%s", b, b2)
	}
	// The voices are block text, one bar to a line.
	if !strings.Contains(string(b), "| q.Bb4 eC5 hD5~\n") {
		t.Errorf("voice layout:\n%s", b)
	}
}

// Random whole scores survive a save and a load.
func TestFileRoundTripProperty(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		s := randomScore(r)
		b, err := Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		back, err := Parse(b)
		if err != nil {
			t.Fatalf("case %d: %v\n%s", i, err, b)
		}
		if !reflect.DeepEqual(normScore(s), normScore(back)) {
			b2, _ := Marshal(back)
			t.Fatalf("case %d: round trip changed the score:\n%s\n----\n%s", i, b, b2)
		}
	}
}

func randomScore(r *rand.Rand) *Score {
	s := New("")
	s.Title = "Random"
	meters := []string{"4/4", "3/4", "6/8", "3+2+2/8", "5/4", "C", "C|", "12/16"}
	s.Meter = nil
	bar := 1
	for k := 0; k < 1+r.Intn(3); k++ {
		m, _ := ParseMeter(meters[r.Intn(len(meters))])
		s.Meter = append(s.Meter, MeterMark{Bar: bar, Meter: m})
		bar += 1 + r.Intn(3)
	}
	s.Key = []KeyMark{{Bar: 1, Fifths: r.Intn(15) - 7, Mode: "minor"}}
	s.Tempo = []TempoMark{{At: Pos{Bar: 1}, BPM: float64(40 + r.Intn(160)), Unit: Duration{Base: Quarter}, Text: "Moderato"}}
	if r.Intn(2) == 0 {
		s.Tempo = append(s.Tempo, TempoMark{At: Pos{Bar: 2, Offset: F(1, 8)}, BPM: 72.5, Unit: Duration{Base: Quarter, Dots: 1}, Ramp: true})
	}
	if r.Intn(2) == 0 {
		s.Tempo = append(s.Tempo, TempoMark{At: Pos{Bar: 2, Offset: F(1, 4)}, Unit: Duration{Base: Quarter}, Text: "poco rit.", To: Pos{Bar: 3}, HasTo: true},
			TempoMark{At: Pos{Bar: 3}, Unit: Duration{Base: Quarter}, Text: "a tempo"})
	}
	if r.Intn(2) == 0 {
		s.Repeats = []Repeat{{Start: 1, End: 2, Times: 3}}
		s.Voltas = []Volta{{Start: 2, End: 2, Passes: []int{1, 2}}, {Start: 3, End: 3, Passes: []int{3}}}
		s.Jumps = []Jump{{Kind: JumpSegno, Bar: 1}, {Kind: JumpFine, Bar: 2}, {Kind: JumpDS, Bar: 3, Al: JumpFine, Repeats: r.Intn(2) == 0}}
	}
	s.Concert = r.Intn(2) == 0
	s.Playback = PlaybackOptions{IgnoreRepeats: r.Intn(2) == 0, Graces: GraceTiming(r.Intn(3))}
	if r.Intn(2) == 0 {
		s.Playback.Swing = Swing{Amount: 0.625, Unit: []int{0, 8, 16}[r.Intn(3)]}
		s.Playback.Humanize = Humanize{Timing: 7.5, Velocity: 0.125, Seed: int64(r.Intn(50))}
	}
	if r.Intn(2) == 0 {
		s.Buses.A.Pan, s.Buses.B.Mute = -0.5, true
		s.Buses.B.FX = append(s.Buses.B.FX, FX{Kind: "filter", Params: []FXParam{{"cutoff", 800}}, Bypass: true})
		s.Master = append(s.Master, NewFX(fx.KindLimiter, fx.Insert))
		s.Buses.A.Lanes = []Lane{{Name: "return", Points: []LanePoint{{At: Pos{Bar: 1}, Value: 0.5}, {At: Pos{Bar: 2}, Value: 0.25, Shape: LaneStep}}}}
		s.Buses.B.Lanes = []Lane{{Name: "filter.cutoff", Points: []LanePoint{{At: Pos{Bar: 1}, Value: 800, Shape: LaneCurve, Curve: 0.5}, {At: Pos{Bar: 2}, Value: 2000}}},
			{Name: "pan", Points: []LanePoint{{At: Pos{Bar: 1}, Value: -1}}}}
		s.MasterLanes = []Lane{{Name: "limiter.ceiling", Points: []LanePoint{{At: Pos{Bar: 1}, Value: -3}}},
			{Name: "volume", Points: []LanePoint{{At: Pos{Bar: 1}, Value: 1}, {At: Pos{Bar: 2, Offset: F(1, 4)}, Value: 0}}}}
	}
	s.Sections = []Section{{Name: "a", Bar: 1}, {Name: "b", Bar: 2, Loop: true, Next: "a"}}
	s.Markers = []Marker{{Name: "m", At: Pos{Bar: 2, Offset: F(1, 4)}}}
	s.Gain = []float32{1, 0.5, 0}[r.Intn(3)]
	s.Loop = r.Intn(2) == 0
	if r.Intn(2) == 0 {
		s.Subtitle = "for the hangar"
		s.Layout = Layout{Paper: Paper{Name: PaperNames[r.Intn(len(PaperNames)-1)], Landscape: r.Intn(2) == 0},
			Staff: 6.5, SystemGap: 11, Spacing: []float64{0, 0.75, 1.4}[r.Intn(3)], Breaks: []Break{{Bar: 2}, {Bar: 3, Page: true}}, TitlePage: r.Intn(2) == 0,
			NoPageNumbers: r.Intn(2) == 0, Names: NameMode(r.Intn(len(NameModes))), NoCautionary: r.Intn(2) == 0, Courtesy: r.Intn(2) == 0, NoBrackets: r.Intn(3) == 0, HideEmpty: r.Intn(3) == 0}
		if r.Intn(2) == 0 {
			s.Layout.Paper = Paper{Name: "custom", Width: 230, Height: 305, Margins: Margins{10, 12, 14, 16}}
		}
		s.Layout.Parts = []PartLayout{{Line: "Line", Staff: 7.5, Breaks: []Break{{Bar: 3}}, NoMultiRests: r.Intn(2) == 0, Paper: Paper{Name: "a4"}}}
	}
	for l := 0; l < 1+r.Intn(3); l++ {
		ln := NewLine("Line", "keys/grand_piano", "treble")
		if r.Intn(2) == 0 {
			ln.Short = "Ln."
		}
		ln.Volume = []float32{1, 0.7, 0}[r.Intn(3)]
		ln.SendB = 0.25
		ln.Inserts = []FX{{Kind: "eq", Params: []FXParam{{"low", -2}}}}
		if r.Intn(2) == 0 {
			ln.Inserts = append(ln.Inserts, FX{Kind: "delay", Params: []FXParam{{"beats", 0.5}, {"mix", 0.25}}, Bypass: r.Intn(2) == 0})
			ln.PreA, ln.PreB = r.Intn(2) == 0, true
			ln.Swing = &Swing{Amount: 0.5}
			ln.Humanize = &Humanize{Timing: 3}
		}
		ln.Staves = nil
		for st := 0; st < 1+r.Intn(2); st++ {
			staff := &Staff{Clef: StaffClefs()[r.Intn(len(StaffClefs()))]}
			if r.Intn(3) == 0 {
				staff.Transpose = Interval{-1, -2}
			}
			if r.Intn(2) == 0 {
				staff.Clefs = []ClefChange{{At: Pos{Bar: 1, Offset: F(1, 8)}, Clef: "bass"}, {At: Pos{Bar: 2}, Clef: "alto"}}
			}
			switch {
			case staff.Clef == "percussion" && r.Intn(2) == 0:
				staff.Lines = 1 + r.Intn(5)
			case staff.Clef != "percussion" && r.Intn(3) == 0:
				staff.Tab = NewTab(TuningPresets[r.Intn(len(TuningPresets))].Name)
				staff.Tab.Capo, staff.Tab.Only = r.Intn(4), r.Intn(2) == 0
				if r.Intn(3) == 0 {
					staff.Tab.Tuning = pitches("C2", "G2", "D3", "A3")
				}
			}
			for v := 0; v < 1+r.Intn(MaxVoices); v++ {
				staff.Voices = append(staff.Voices, randomVoice(r))
			}
			ln.Staves = append(ln.Staves, staff)
		}
		if r.Intn(2) == 0 {
			ln.Gliss = GlissStyles[r.Intn(len(GlissStyles))]
			ln.Overrides = []DynOverride{{From: Pos{Bar: 1, Offset: F(1, 4)}, To: Pos{Bar: 2}, Level: 0.25}}
		}
		if r.Intn(2) == 0 {
			for k, sym := range []string{"Cmaj7", "F#m7b5/C", "Bb13b9#11", "N.C.", "Dsus4add9no5"}[:1+r.Intn(5)] {
				c, err := ParseChord(sym)
				if err != nil {
					panic(err)
				}
				at := Pos{Bar: 1 + k/2}
				if k%2 == 1 {
					at.Offset = F(1, 4)
				}
				ln.Chords = append(ln.Chords, ChordSymbol{At: at, Chord: c})
			}
			ln.ChordPlay = &ChordPlay{Style: ChordStyle(r.Intn(3)), Volume: []float32{1, 0.5}[r.Intn(2)], Octave: r.Intn(3) - 1}
			if r.Intn(2) == 0 {
				ln.ChordPlay.Instrument = "synth/warm_pad"
			}
		}
		if r.Intn(2) == 0 {
			ln.Lanes = []Lane{{Name: "eq.low", Points: []LanePoint{{At: Pos{Bar: 1}, Value: -3}}},
				{Name: "volume", Points: []LanePoint{{At: Pos{Bar: 1}, Value: 0.5, Shape: LaneStep}, {At: Pos{Bar: 2, Offset: F(1, 4)}, To: Pos{Bar: 3}, HasTo: true, Value: 1, Shape: LaneCurve, Curve: -0.5}}}}
		}
		s.Lines = append(s.Lines, ln)
	}
	s.Bars = s.Measures() + r.Intn(3)
	return s
}

func normScore(s *Score) *Score {
	c := s.Clone()
	c.Name = ""
	c.Bars = s.Measures()
	for _, l := range c.Lines {
		if len(l.Lanes) == 0 {
			l.Lanes = nil
		}
		for _, st := range l.Staves {
			for i, v := range st.Voices {
				st.Voices[i] = normVoice(v)
			}
		}
	}
	return c
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"version: 9\nbars: 1\nlines: []":                                                                                                        "format version",
		"bars: 1\nlines: []\nbogus: 1":                                                                                                          "bogus",
		"bars: 1\nmeter: [{bar: 1, sig: 4/3}]\nlines: []":                                                                                       "power of two",
		"bars: 1\ntempo: [{bar: 1, bpm: 0}]\nlines: []":                                                                                         "bpm",
		"bars: 1\nlines: [{name: a, instrument: x, staves: []}]":                                                                                "no staves",
		"bars: 1\nlines: [{name: a, instrument: x, staves: [{clef: grand, voices: []}]}]":                                                       "clef",
		"bars: 1\nlines: [{name: a, instrument: x, staves: [{clef: bass, voices: [\"| qH4 |\"]}]}]":                                             "line a: staff 1, voice 1: bar 1",
		"bars: 1\nlines: [{name: a, instrument: x, staves: [{clef: bass, voices: [a, b, c, d, e]}]}]":                                           "at most 4",
		"bars: 1\nmaster: [{low: 1}]\nlines: []":                                                                                                "fx:",
		"bars: 1\nmaster: [{fx: eq, low: high}]\nlines: []":                                                                                     "not a number",
		"bars: 1\nkey: [{bar: 1, fifths: 9}]\nlines: []":                                                                                        "fifths",
		"bars: 1\npickup: x\nlines: []":                                                                                                         "pickup",
		"bars: 1\nmarkers: [{name: m, bar: 1, beat: \"0\"}]\nlines: []":                                                                         "beats count from 1",
		"bars: 1\ntempo: [{bar: 1, bpm: 0, text: rit., ramp: true}]\nlines: []":                                                                 "bpm",
		"bars: 1\nvoltas: [{start: 1, end: 1, passes: []}]\nlines: []":                                                                          "no passes",
		"bars: 1\njumps: [{mark: dal_segno, bar: 1}]\nlines: []":                                                                                "want segno",
		"bars: 1\njumps: [{mark: dc, bar: 1, al: segno}]\nlines: []":                                                                            "want fine or coda",
		"bars: 1\njumps: [{mark: fine, bar: 1, al: coda}]\nlines: []":                                                                           "only a dc or ds",
		"bars: 1\nplayback: {graces: sometimes}\nlines: []":                                                                                     "graces",
		"bars: 1\nlines: [{name: a, instrument: x, gliss: smear, staves: [{clef: bass, voices: []}]}]":                                          "gliss",
		"bars: 1\nlines: [{name: a, instrument: x, staves: [{clef: bass, clefs: [{at: \"1\", clef: grand}], voices: []}]}]":                     "clef change",
		"bars: 1\nlines: [{name: a, instrument: x, dynamic_overrides: [{at: \"1\", to: \"2\", level: 3}], staves: [{clef: bass, voices: []}]}]": "level",
	}
	for in, want := range cases {
		_, err := Parse([]byte(in))
		if err == nil {
			t.Errorf("%q should not parse", in)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v (want %q)", in, err, want)
		}
	}
}

func TestValidate(t *testing.T) {
	s, err := Parse([]byte(`bars: 3
sections: [{name: a, bar: 1, next: nowhere}, {name: a, bar: 2}]
repeats: [{start: 1, end: 2}, {start: 2, end: 3}]
bindings: [{param: nope, line: Nobody, target: wobble, from: 0, to: 1}]
lines:
  - name: Flute
    instrument: woodwinds/missing
    staves:
      - clef: treble
        voices:
          - |
            | wC4 qD4
            | hC4~
            | qE4 qr R
          - "| hC4(zork) |"
`))
	if err != nil {
		t.Fatal(err)
	}
	issues := s.Validate(func(n string) bool { return n != "woodwinds/missing" })
	var text []string
	for _, i := range issues {
		text = append(text, i.String())
	}
	all := strings.Join(text, "\n")
	for _, want := range []string{
		"two sections named \"a\"", "goes next to \"nowhere\"", "repeats overlap", "param \"nope\"", "line \"Nobody\"", "target \"wobble\"",
		"no instrument \"woodwinds/missing\"", "error: line 1, staff 1, voice 1, bar 1: bar holds 5/4", "warning: line 1, staff 1, voice 1, bar 2: bar holds 1/2",
		"tie from C4 does not reach", "bar rest must be alone", "unknown mark \"zork\"",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no issue %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "voice 2, bar 1: bar holds") {
		t.Errorf("a short second voice is not worth a warning:\n%s", all)
	}
	if !HasErrors(issues) {
		t.Error("HasErrors")
	}
}

// The buses' and the master's lanes: read, written, and checked for the
// names their scope takes, the effects their chains have, their values.
func TestMixLanes(t *testing.T) {
	src := `bars: 2
lines: []
buses:
  a:
    fx: [{fx: reverb, size: 0.6}]
    return: 0.5
    lanes:
      return: [{at: "1", value: 0.5}, {at: "2", value: 0.1}]
      reverb.size: [{at: "1", value: 0.3, curve: 0.5}, {at: "2", value: 0.9}]
      volume: [{at: "1", value: 1}]
      eq.low: [{at: "1", value: 0}]
  b:
    return: 0.2
    lanes:
      pan: [{at: "1", value: -3}]
master: [{fx: limiter, ceiling: -1}]
master_lanes:
  volume: [{at: "1", value: 1}, {at: "2:3", value: 0, step: true}]
  limiter.ceiling: [{at: "1", value: -1}]
  return: [{at: "1", value: 1}]
  send_a: [{at: "9", value: 0}]
`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if ln := s.LaneOf(OwnerBusA, "reverb.size"); ln == nil || len(ln.Points) != 2 || ln.Points[0].Shape != LaneCurve {
		t.Fatalf("bus a's reverb lane: %+v", ln)
	}
	if ln := s.LaneOf(OwnerMaster, "volume"); ln == nil || ln.Points[1].Shape != LaneStep {
		t.Fatalf("master volume lane: %+v", ln)
	}
	b, err := Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if !reflect.DeepEqual(normScore(back).Buses, normScore(s).Buses) || !reflect.DeepEqual(back.MasterLanes, s.MasterLanes) {
		t.Errorf("round trip:\n%s", b)
	}
	var text []string
	for _, i := range s.Validate(nil) {
		text = append(text, i.String())
	}
	all := strings.Join(text, "\n")
	for _, want := range []string{
		`error: bus a: lane "volume": want return, pan or <effect>.<parameter>`,
		"warning: bus a: lane eq.low: the bus has no such effect",
		"warning: bar 1: bus b: lane pan: value -3 outside -1..1",
		`error: master: lane "return": want volume or <effect>.<parameter>`,
		`error: master: lane "send_a"`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no issue %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "reverb.size") || strings.Contains(all, "limiter.ceiling") {
		t.Errorf("a good lane reported:\n%s", all)
	}
	// Every owner by one path.
	if s.OwnerName(OwnerBusB) != "Bus B" || OwnerScope(OwnerMaster) != ScopeMaster || s.LanesOf(5) != nil || s.HasOwner(-4) {
		t.Error("owners")
	}
	if v := s.LaneDefaultOf(OwnerBusA, LaneTarget{Kind: LaneReturn}); v != 0.5 {
		t.Errorf("bus a's return lane starts at %g", v)
	}
	if v := s.LaneDefaultOf(OwnerMaster, mustTarget(t, ScopeMaster, "limiter.ceiling")); v != -1 {
		t.Errorf("master limiter ceiling lane starts at %g", v)
	}
}

func mustTarget(t *testing.T, sc LaneScope, name string) LaneTarget {
	t.Helper()
	tg, err := ParseLaneTargetIn(sc, name)
	if err != nil {
		t.Fatal(err)
	}
	return tg
}
