package notate

// transcribe.go — the whole way from played notes to a line's staves: what
// a MIDI file's import does with each part, and what the Score editor does
// with a take recorded from a MIDI keyboard. One function, so a recording
// is written exactly as the same notes in a file would be.

import (
	"math"

	"foundry/internal/score"
)

// TranscribeOptions shape Transcribe. The zero value is the MIDI import
// dialog's defaults for one treble staff.
type TranscribeOptions struct {
	// Grid is what notes snap to (zero: DefaultGrid).
	Grid Grid
	// Voices is the most voices a staff gets (zero: 4); 1 writes
	// overlapping notes as chords in one voice.
	Voices int
	// Clefs are the staves; two make a grand staff split at Split (notes
	// from Split up on the first). None is one treble staff.
	Clefs []string
	// Split is the lowest note of a grand staff's upper staff (zero: 60).
	Split int
	// Percussion writes a drum part: at most two voices, hits that land
	// together one chord, each no longer than its beat (DrumHits).
	Percussion bool
	// NoGraces keeps notes played as grace notes (MarkGraces) as ordinary
	// notes; NoLegato keeps every gap a player left (Legato closes the small
	// ones); NoDynamics writes no dynamic marks from velocity.
	NoGraces, NoLegato, NoDynamics bool
	// Pedal are the damper pedal's presses (a MIDI file's or a keyboard's
	// CC 64), written as pedal marks on the lowest staff.
	Pedal []PedalSpan
}

// PedalSpan is the damper pedal held from Down to Up, whole notes from the
// top of the score.
type PedalSpan struct{ Down, Up score.Frac }

// Staff is one staff of a transcription: its clef and its voices (the
// first always there, bar rests where it is silent).
type Staff struct {
	Clef   string
	Voices []*score.Voice
}

// Transcribe writes played notes as notation over a layout: graces found,
// quantised to the grid, split between the staves, dealt into voices, small
// gaps closed, grace notes hung on their main notes, velocity read as
// dynamics, and each voice written as bars spelled in the key.
func Transcribe(notes []Note, l Layout, o TranscribeOptions) ([]Staff, error) {
	if o.Grid.Unit.Sign() <= 0 {
		o.Grid = DefaultGrid()
	}
	if o.Voices <= 0 {
		o.Voices = 4
	}
	if o.Split == 0 {
		o.Split = 60
	}
	clefs := o.Clefs
	if len(clefs) == 0 {
		clefs = []string{"treble"}
	}
	if !o.NoGraces {
		notes = MarkGraces(notes, o.Grid)
	}
	notes, err := Quantize(notes, l, o.Grid)
	if err != nil {
		return nil, err
	}
	if o.Percussion {
		notes = DrumHits(notes, l)
	}
	groups := make([][]Note, len(clefs))
	for _, n := range notes {
		g := 0
		if len(clefs) > 1 && n.MIDI < o.Split {
			g = 1
		}
		groups[g] = append(groups[g], n)
	}
	voices := o.Voices
	if o.Percussion {
		voices = min(voices, 2)
	}
	staffChords := make([][][]Chord, len(groups))
	for gi, g := range groups {
		staffChords[gi] = Separate(g, voices, o.Grid.Unit)
		// A drum hit has no legato to close: its length is DrumHits'.
		if !o.NoLegato && !o.Percussion {
			for _, v := range staffChords[gi] {
				Legato(v, l, score.F(1, 8))
			}
		}
	}
	// Grace notes go on the note they lead to: in their own staff, else
	// (the main note split to the other one) there.
	for gi, g := range groups {
		var graces []Note
		for _, n := range g {
			if n.Grace {
				graces = append(graces, n)
			}
		}
		left := AttachGraces(staffChords[gi], graces)
		for gj := range groups {
			if gj != gi && len(left) > 0 {
				left = AttachGraces(staffChords[gj], left)
			}
		}
	}
	if !o.NoDynamics {
		MarkDynamics(staffChords, l)
	}
	if len(o.Pedal) > 0 && len(staffChords) > 0 {
		markPedal(staffChords[len(staffChords)-1], o.Pedal, o.Grid.Unit)
	}
	out := make([]Staff, len(groups))
	for gi, vs := range staffChords {
		out[gi].Clef = clefs[gi]
		if len(vs) == 0 {
			vs = [][]Chord{nil}
		}
		for vi, cs := range vs {
			v, err := Write(cs, l, WriteOptions{BarRests: vi == 0})
			if err != nil {
				return nil, err
			}
			out[gi].Voices = append(out[gi].Voices, v)
		}
	}
	return out, nil
}

// DynamicLadder are the dynamics velocity turns into, softest first.
var DynamicLadder = []string{"ppp", "pp", "p", "mp", "mf", "f", "ff", "fff"}

// LadderVelocity is the MIDI velocity a ladder step plays at.
func LadderVelocity(i int) float64 { return float64(score.Dynamics[DynamicLadder[i]].Level) * 127 }

// NearestDynamic is the ladder step nearest a velocity.
func NearestDynamic(vel float64) int {
	best, bestD := 0, math.Inf(1)
	for i := range DynamicLadder {
		if d := math.Abs(vel - LadderVelocity(i)); d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// MarkDynamics turns velocity into dynamic marks: each bar's average
// velocity is read as the nearest of ppp..fff, and a mark is written on
// the bar's first chord (in any staff or voice) whenever the level moves —
// by more than a level's width and a bit, so a played line that wavers
// around a boundary does not flicker between two marks.
func MarkDynamics(staves [][][]Chord, l Layout) {
	type ref struct{ s, v, c int }
	bars := make([][]ref, l.Bars())
	for si, vs := range staves {
		for vi, cs := range vs {
			for ci, c := range cs {
				b := l.BarAt(c.Start)
				bars[b] = append(bars[b], ref{si, vi, ci})
			}
		}
	}
	cur := -1
	for _, refs := range bars {
		sum, n := 0.0, 0
		var first *Chord
		for _, r := range refs {
			c := &staves[r.s][r.v][r.c]
			if c.Velocity > 0 {
				sum += float64(c.Velocity)
				n++
			}
			if first == nil || c.Start.Less(first.Start) {
				first = c
			}
		}
		if n == 0 || first == nil {
			continue
		}
		mean := sum / float64(n)
		lvl := NearestDynamic(mean)
		if cur >= 0 && (lvl == cur || math.Abs(mean-LadderVelocity(cur)) < 11) {
			continue
		}
		cur = lvl
		first.Marks = append(first.Marks, score.Mark{Name: DynamicLadder[lvl]})
	}
}

// markPedal writes pedal presses on a staff's first voice: "ped" on the
// first chord the pedal goes down by (within half a grid step before it),
// "ped_end" on the last chord that starts before it comes up. A press
// with no chord under it is left out.
func markPedal(voices [][]Chord, spans []PedalSpan, unit score.Frac) {
	if len(voices) == 0 {
		return
	}
	cs := voices[0]
	half := unit.Div(score.F(2, 1))
	for _, sp := range spans {
		from := sp.Down.Sub(half)
		a, z := -1, -1
		for i, c := range cs {
			if !c.Start.Less(from) && c.Start.Less(sp.Up) {
				if a < 0 {
					a = i
				}
				z = i
			}
		}
		if a < 0 {
			continue
		}
		cs[a].Marks = addMarks(cs[a].Marks, []score.Mark{{Name: "ped"}})
		cs[z].Marks = addMarks(cs[z].Marks, []score.Mark{{Name: "ped_end"}})
	}
}
