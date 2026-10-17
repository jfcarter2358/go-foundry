package play

import (
	"math"
	"testing"

	"foundry/internal/score"
)

// peakOf is the largest sample.
func peakOf(out []float32) float64 {
	p := 0.0
	for _, v := range out {
		p = math.Max(p, math.Abs(float64(v)))
	}
	return p
}

// A line's chord symbols play on the instrument and at the volume its
// chord_play names; its own instrument plays its notes.
func TestChordSymbolsPlayOnTheirInstrument(t *testing.T) {
	useTestInstruments(t)
	render := func(inst string, vol float32) float64 {
		s := build(t, "bars: 1", "A|dc/0.5|0|| wr |")
		s.Lines[0].Chords = nil
		c := mustChord(t, "C")
		s.Lines[0].Chords = append(s.Lines[0].Chords, c)
		s.Lines[0].ChordPlay = chordPlay(inst, vol)
		prog := prepare(t, s)
		if inst != "" && prog.noteInst(0).Name != inst {
			t.Fatalf("chord note plays on %s", prog.noteInst(0).Name)
		}
		return peakOf(Render(prog, RenderOptions{Rate: rate, Block: 480, Tail: 0}))
	}
	a, b, half := render("dc/0.2", 1), render("dc/0.4", 1), render("dc/0.4", 0.5)
	if a == 0 || math.Abs(b/a-2) > 0.05 {
		t.Errorf("dc/0.4 against dc/0.2: %.4f / %.4f", b, a)
	}
	if math.Abs(half/b-0.5) > 0.05 {
		t.Errorf("volume 0.5: %.4f against %.4f", half, b)
	}
	// The line's own instrument plays them when chord_play names none.
	if own := render("", 1); own == 0 || math.Abs(own/a-2.5) > 0.1 {
		t.Errorf("on the line's dc/0.5: %.4f against dc/0.2's %.4f", own, a)
	}
}

func mustChord(t *testing.T, sym string) score.ChordSymbol {
	t.Helper()
	c, err := score.ParseChord(sym)
	if err != nil {
		t.Fatal(err)
	}
	return score.ChordSymbol{At: score.Pos{Bar: 1}, Chord: c}
}

func chordPlay(inst string, vol float32) *score.ChordPlay {
	return &score.ChordPlay{Style: score.ChordsPad, Instrument: inst, Volume: vol}
}
