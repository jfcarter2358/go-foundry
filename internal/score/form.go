package score

// form.go — the marks that change the order bars play in.
//
// Repeats (score.go) play a run of bars more than once. Voltas are their
// endings: a run of bars played only on some passes ("1." the first time,
// "2." the second). Jumps are the navigation marks: a D.C. sends playback
// back to the top, a D.S. to the segno; on that second time through a To
// Coda sends it on to the coda and a Fine ends the piece.
//
//	repeats: [{start: 1, end: 8}]
//	voltas:  [{start: 7, end: 8, passes: [1]}, {start: 9, end: 9, passes: [2]}]
//	jumps:   [{mark: segno, bar: 3}, {mark: to_coda, bar: 10},
//	          {mark: ds, bar: 16, al: coda}, {mark: coda, bar: 17}]
//
// A segno and a coda mark the start of their bar (where playback lands); a
// To Coda, a Fine, a D.C. and a D.S. the end of theirs (where playback
// leaves). internal/score/interpret plays them; Options.NoRepeats and
// Playback.IgnoreRepeats play the score straight through instead.

import (
	"fmt"
	"strings"
)

// Volta is an ending: bars Start..End (inclusive) play only on the passes
// listed, counted through the repeat it belongs to — the repeat with the
// latest start at or before the volta's.
type Volta struct {
	Start, End int
	Passes     []int
}

// PlaysOn reports whether the ending plays on a pass.
func (v Volta) PlaysOn(pass int) bool {
	for _, p := range v.Passes {
		if p == pass {
			return true
		}
	}
	return false
}

// Label is what the ending's bracket prints: "1.", "1.-3.", "2.".
func (v Volta) Label() string {
	if len(v.Passes) > 2 {
		run := true
		for i := 1; i < len(v.Passes); i++ {
			if v.Passes[i] != v.Passes[i-1]+1 {
				run = false
			}
		}
		if run {
			return fmt.Sprintf("%d.-%d.", v.Passes[0], v.Passes[len(v.Passes)-1])
		}
	}
	var parts []string
	for _, p := range v.Passes {
		parts = append(parts, fmt.Sprintf("%d.", p))
	}
	return strings.Join(parts, " ")
}

// JumpKind is a navigation mark.
type JumpKind uint8

const (
	// JumpSegno is the sign a D.S. goes back to (start of its bar).
	JumpSegno JumpKind = iota + 1
	// JumpCoda is the coda a To Coda goes on to (start of its bar).
	JumpCoda
	// JumpToCoda leaves for the coda, on the time through after a D.C. or
	// D.S. (end of its bar).
	JumpToCoda
	// JumpFine ends the piece on the time through after a D.C. or D.S.
	// (end of its bar).
	JumpFine
	// JumpDC goes back to the top (end of its bar).
	JumpDC
	// JumpDS goes back to the segno (end of its bar).
	JumpDS
)

var jumpNames = []string{"", "segno", "coda", "to_coda", "fine", "dc", "ds"}

func (k JumpKind) String() string {
	if int(k) < len(jumpNames) {
		return jumpNames[k]
	}
	return fmt.Sprintf("jump%d", k)
}

// ParseJumpKind reads a jump's name: segno, coda, to_coda, fine, dc, ds.
func ParseJumpKind(s string) (JumpKind, bool) {
	for i, n := range jumpNames {
		if i > 0 && n == s {
			return JumpKind(i), true
		}
	}
	return 0, false
}

// Jump is a navigation mark at a bar.
type Jump struct {
	Kind JumpKind
	Bar  int
	// Al, on a D.C. or D.S., is where the time through after it ends:
	// JumpFine (al Fine) or JumpCoda (al Coda). Zero follows whichever
	// Fine or To Coda the score has.
	Al JumpKind
	// Repeats, on a D.C. or D.S., takes the repeats again on the way
	// through ("con rip."); by default they are played once, last ending.
	Repeats bool
}

// Text is what the mark prints: "D.S. al Coda", "Fine", "To Coda".
func (j Jump) Text() string {
	var t string
	switch j.Kind {
	case JumpSegno:
		return "Segno"
	case JumpCoda:
		return "Coda"
	case JumpToCoda:
		return "To Coda"
	case JumpFine:
		return "Fine"
	case JumpDC:
		t = "D.C."
	case JumpDS:
		t = "D.S."
	default:
		return j.Kind.String()
	}
	switch j.Al {
	case JumpFine:
		t += " al Fine"
	case JumpCoda:
		t += " al Coda"
	}
	return t
}

// JumpAt finds the first jump of a kind at a bar, or nil.
func (s *Score) JumpAt(bar int, k JumpKind) *Jump {
	for i := range s.Jumps {
		if s.Jumps[i].Bar == bar && s.Jumps[i].Kind == k {
			return &s.Jumps[i]
		}
	}
	return nil
}

// FirstJump is the bar of the first jump of a kind, 0 when there is none.
func (s *Score) FirstJump(k JumpKind) int {
	bar := 0
	for _, j := range s.Jumps {
		if j.Kind == k && (bar == 0 || j.Bar < bar) {
			bar = j.Bar
		}
	}
	return bar
}

// VoltaAt is the ending a bar is in, or nil.
func (s *Score) VoltaAt(bar int) *Volta {
	for i := range s.Voltas {
		if bar >= s.Voltas[i].Start && bar <= s.Voltas[i].End {
			return &s.Voltas[i]
		}
	}
	return nil
}

// VoltaRepeat is the index into Repeats of the repeat an ending belongs
// to — the latest that starts at or before it — or -1.
func (s *Score) VoltaRepeat(v Volta) int {
	k := -1
	for i, r := range s.Repeats {
		if r.Start <= v.Start && (k < 0 || r.Start > s.Repeats[k].Start) {
			k = i
		}
	}
	return k
}
