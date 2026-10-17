package interpret

import (
	"fmt"
	"sort"
	"testing"

	"foundry/internal/score"
	"foundry/internal/tracker"
)

func chordLine(t *testing.T, style string, repeats string) *score.Score {
	t.Helper()
	src := fmt.Sprintf(`version: 1
bars: 3
%s
lines:
  - name: Lead
    instrument: keys/grand_piano
    chords: [{at: "1", sym: C}, {at: "1:3", sym: G7/B}, {at: "2", sym: N.C.}, {at: "3", sym: Am}]
    chord_play: {style: %s, volume: 0.5}
    staves:
      - clef: treble
        voices: ["| wE5 | wD5 | wC5 |"]
`, repeats, style)
	s, err := score.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func chordNotesOf(p *Performance) []Note {
	var out []Note
	for _, n := range p.Notes {
		if n.FromChord {
			out = append(out, n)
		}
	}
	return out
}

// A pad holds each chord to the next, voiced bass low and the chord close
// above; N.C. is silence.
func TestChordSymbolsPlayAsAPad(t *testing.T) {
	p := perform(t, chordLine(t, "pad", ""))
	cs := chordNotesOf(p)
	byTick := map[int64][]int{}
	for _, n := range cs {
		if n.Voice != -1 || n.Line != 0 {
			t.Fatalf("a chord note on voice %d line %d", n.Voice, n.Line)
		}
		byTick[n.Tick] = append(byTick[n.Tick], n.MIDI)
		switch n.Tick {
		case 0:
			if n.Ticks != 1920 {
				t.Errorf("C held %d ticks", n.Ticks)
			}
		case 1920:
			if n.Ticks != 1920 {
				t.Errorf("G7/B held %d ticks", n.Ticks)
			}
		case 2 * 3840:
			if n.Ticks != 3840 {
				t.Errorf("Am held %d ticks", n.Ticks)
			}
		default:
			t.Errorf("a chord note at tick %d (bar 2 is N.C.)", n.Tick)
		}
	}
	for tick, want := range map[int64][]int{0: {36, 60, 64, 67}, 1920: {47, 55, 59, 62, 65}, 7680: {45, 57, 60, 64}} {
		got := byTick[tick]
		sort.Ints(got)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("tick %d: %v, want %v", tick, got, want)
		}
	}
	// The melody is untouched.
	if n := len(p.Notes) - len(cs); n != 3 {
		t.Errorf("%d melody notes", n)
	}
}

// A comping part strikes every beat, short, the bass on the chord's first
// stroke and on downbeats.
func TestChordSymbolsComp(t *testing.T) {
	p := perform(t, chordLine(t, "comp", ""))
	strokes := map[int64]int{}
	bass := map[int64]bool{}
	for _, n := range chordNotesOf(p) {
		strokes[n.Tick]++
		if n.MIDI < 48 {
			bass[n.Tick] = true
		}
		if n.Ticks != 576 { // three fifths of a quarter
			t.Errorf("a stroke %d ticks long", n.Ticks)
		}
	}
	var ts []int64
	for k := range strokes {
		ts = append(ts, k)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	want := []int64{0, 960, 1920, 2880, 7680, 8640, 9600, 10560}
	if fmt.Sprint(ts) != fmt.Sprint(want) {
		t.Fatalf("strokes at %v, want %v", ts, want)
	}
	for _, k := range []int64{0, 1920, 7680} {
		if !bass[k] {
			t.Errorf("no bass at %d", k)
		}
	}
	if bass[960] || bass[8640] {
		t.Error("a bass on an inner beat")
	}
}

// Where a repeat jumps back, the chord in force there sounds again.
func TestChordSymbolsThroughARepeat(t *testing.T) {
	s := chordLine(t, "pad", "repeats: [{start: 3, end: 3}]")
	s.Lines[0].Chords = s.Lines[0].Chords[:3] // C, G7/B, N.C. — then bar 3 has no symbol of its own
	s.Lines[0].Chords[2].Chord, _ = score.ParseChord("F")
	p := perform(t, s)
	starts := map[int64]bool{}
	for _, n := range chordNotesOf(p) {
		starts[n.Tick] = true
	}
	// F from bar 2 holds into bar 3; the repeat of bar 3 starts it again.
	if !starts[3840] || !starts[3*3840] {
		t.Errorf("F starts at %v", starts)
	}
}

// Silent chord symbols, or no chord_play, play nothing.
func TestChordSymbolsSilent(t *testing.T) {
	for _, s := range []*score.Score{chordLine(t, "off", "")} {
		if n := len(chordNotesOf(perform(t, s))); n != 0 {
			t.Errorf("%d chord notes", n)
		}
	}
	s := chordLine(t, "pad", "")
	s.Lines[0].ChordPlay = nil
	if n := len(chordNotesOf(perform(t, s))); n != 0 {
		t.Errorf("%d chord notes with no chord_play", n)
	}
}

func TestVoicing(t *testing.T) {
	c, _ := score.ParseChord("Bbmaj7/D")
	bass, up := Voicing(c, 0)
	if bass != 38 || fmt.Sprint(up) != "[58 62 65 69]" {
		t.Errorf("Bbmaj7/D: bass %d, %v", bass, up)
	}
	bass, up = Voicing(c, -1)
	if bass != 26 || up[0] != 46 {
		t.Errorf("an octave down: %d %v", bass, up)
	}
	if b, u := Voicing(score.Chord{NoChord: true}, 0); b != -1 || u != nil {
		t.Error("N.C.")
	}
}

// An articulation on a drum plays the sound the drum map pairs it with.
func TestDrumArticulationsPickSounds(t *testing.T) {
	src := `version: 1
bars: 1
lines:
  - name: Drums
    instrument: percussion/drum_kit
    staves:
      - clef: percussion
        voices: ["| qF#2 qF#2(open) qF#2(acc) qD2(open) |"]
`
	s, err := score.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	kit := &tracker.Instrument{Playback: tracker.Playback{Drums: []tracker.DrumNote{
		{Key: 42, Name: "Hat", Line: 9, Head: "x"}, {Key: 46, Name: "Hat open", Line: 9, Head: "x", Articulation: "open"}, {Key: 38, Name: "Snare", Line: 5}}}}
	p, err := Interpret(s, Options{Instruments: []*tracker.Instrument{kit}})
	if err != nil {
		t.Fatal(err)
	}
	var keys []int
	for _, n := range p.Notes {
		keys = append(keys, n.MIDI)
	}
	if fmt.Sprint(keys) != "[42 46 42 38]" {
		t.Errorf("keys %v", keys)
	}
	if !p.InstrumentDependent[0] {
		t.Error("a kit's marks depend on the kit")
	}
	// Without the kit (the General MIDI map, no articulations) the marks play nothing else.
	keys = keys[:0]
	for _, n := range perform(t, s).Notes {
		keys = append(keys, n.MIDI)
	}
	if fmt.Sprint(keys) != "[42 42 42 38]" {
		t.Errorf("GM keys %v", keys)
	}
}
