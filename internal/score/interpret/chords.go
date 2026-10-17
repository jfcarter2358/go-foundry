package interpret

// chords.go — a line's chord symbols played: a pad or a comping part
// under the line (score.Line.ChordPlay).
//
// The symbols are read in playing order: each sounds from its place until
// the next symbol, the end of the performance, or a "N.C."; where the
// playing order jumps (a repeat going back, a D.S.) the symbol in force at
// the bar jumped to sounds again from there. A chord is voiced in close
// position: the bass (its slash bass, or its root) in the octave below
// the bass clef's middle, the chord's notes (score.Chord.Tones) stacked
// up from its root around the E below middle C, all moved by
// ChordPlay.Octave. A pad holds the chord, legato, to the next; a comping
// part strikes it on every conductor's beat (the meter's beats), short,
// the bass on the chord's first stroke and on every downbeat.
//
// The notes are the line's (its dynamics, its pedal, its strip in the
// mixer) marked as the chords' (Note.FromChord), which the scheduler plays
// on ChordPlay.Instrument when it names one, at ChordPlay.Volume.

import (
	"foundry/internal/score"
)

// chordAt is one chord symbol as played.
type chordAt struct {
	at        score.Frac
	c         *score.Chord
	bar, pass int
	k         int // its played bar, into interp.order
}

// compLength is how much of a beat a comping stroke sounds.
var compLength = score.F(3, 5)

// chordNotes adds every line's chord playing to the notes.
func (in *interp) chordNotes() {
	if len(in.order) == 0 {
		return
	}
	last := in.order[len(in.order)-1]
	total := last.start.Add(last.length)
	for li, l := range in.s.Lines {
		cp := l.ChordPlay
		if cp == nil || cp.Style == score.ChordsSilent || len(l.Chords) == 0 {
			continue
		}
		var evs []chordAt
		for k, pb := range in.order {
			jumped := k == 0 || in.order[k-1].bar+1 != pb.bar
			onStart := false
			for i := range l.Chords {
				ch := &l.Chords[i]
				if ch.At.Bar == pb.bar {
					onStart = onStart || ch.At.Offset.IsZero()
				}
			}
			if jumped && !onStart {
				if cs := l.ChordAt(score.Pos{Bar: pb.bar}); cs != nil {
					evs = append(evs, chordAt{pb.start, &cs.Chord, pb.bar, pb.pass, k})
				}
			}
			for i := range l.Chords {
				ch := &l.Chords[i]
				if ch.At.Bar == pb.bar && ch.At.Offset.Less(pb.length) {
					evs = append(evs, chordAt{pb.start.Add(ch.At.Offset), &ch.Chord, pb.bar, pb.pass, k})
				}
			}
		}
		for i, ce := range evs {
			end := total
			if i+1 < len(evs) {
				end = evs[i+1].at
			}
			if ce.c.NoChord || !ce.at.Less(end) {
				continue
			}
			bass, upper := Voicing(*ce.c, cp.Octave)
			switch cp.Style {
			case score.ChordsComp:
				in.comp(li, ce, end, bass, upper)
			default:
				in.chordNote(li, ce, ce.at, end.Sub(ce.at), bass)
				for _, m := range upper {
					in.chordNote(li, ce, ce.at, end.Sub(ce.at), m)
				}
			}
		}
	}
}

// comp strikes a chord on the beats from its place to end: the bass on
// its first stroke and on downbeats.
func (in *interp) comp(li int, ce chordAt, end score.Frac, bass int, upper []int) {
	first := true
	for _, pb := range in.order[ce.k:] {
		be := pb.start.Add(pb.length)
		if !pb.start.Less(end) {
			break
		}
		at := pb.start
		for bi, b := range in.s.MeterAt(pb.bar).Beats() {
			beatEnd := at.Add(b)
			start := score.MaxFrac(at, ce.at)
			if start.Less(beatEnd) && start.Less(end) && !start.Less(ce.at) {
				d := score.MinFrac(b.Mul(compLength), score.MinFrac(beatEnd, end).Sub(start))
				if d.Sign() > 0 {
					if first || bi == 0 && start.Eq(pb.start) {
						in.chordNote(li, chordAt{start, ce.c, pb.bar, pb.pass, ce.k}, start, d, bass)
					}
					for _, m := range upper {
						in.chordNote(li, chordAt{start, ce.c, pb.bar, pb.pass, ce.k}, start, d, m)
					}
					first = false
				}
			}
			at = beatEnd
			if !at.Less(be) {
				break
			}
		}
	}
}

// chordNote adds one note of a line's chord playing.
func (in *interp) chordNote(li int, ce chordAt, at, dur score.Frac, midi int) {
	if midi < 0 || midi > 127 || dur.Sign() <= 0 {
		return
	}
	in.notes = append(in.notes, rawNote{at: at, dur: dur, velAt: at, pitch: score.SpellMIDI(midi, 0, 1), midi: midi,
		line: li, staff: 0, voice: -1, bar: ce.bar, pass: ce.pass, segAt: at, segDur: dur, chord: true, shaped: true})
}

// Voicing is how a chord plays: its bass note and its chord notes (MIDI),
// moved by octaves.
func Voicing(c score.Chord, octaves int) (bass int, upper []int) {
	if c.NoChord {
		return -1, nil
	}
	pc := func(p score.Pitch) int { return ((p.MIDI() % 12) + 12) % 12 }
	bass = 36 + pc(c.BassPitch()) + 12*octaves
	// The root at or above E3, the tones up from it.
	root := 52 + ((pc(c.Root)-52%12)+12)%12 + 12*octaves
	for _, t := range c.Tones() {
		upper = append(upper, root+t)
	}
	return bass, upper
}
