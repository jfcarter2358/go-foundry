package edit

// form.go — the score-wide marks: tempo marks and tempo text, rehearsal
// marks (sections), repeats, endings, the navigation marks, and clef
// changes inside a staff.
//
// Each operation keeps the score valid: one that would leave a D.S. with no
// segno, an ending with no repeat before it or two rehearsal marks of one
// name is refused with what is missing, and changes nothing.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/score"
)

// formErrors are the score-wide errors validation finds.
func formErrors(s *score.Score) []string {
	var out []string
	for _, is := range s.Validate(nil) {
		if is.Severity == score.Error && is.Line == 0 {
			out = append(out, is.Msg)
		}
	}
	return out
}

// guarded runs a change to the score's form and undoes it when it leaves
// an error validation did not find before.
func guarded(s *score.Score, f func() error) error {
	before := map[string]int{}
	for _, m := range formErrors(s) {
		before[m]++
	}
	keep := s.Clone()
	if err := f(); err != nil {
		*s = *keep
		return err
	}
	for _, m := range formErrors(s) {
		if before[m] > 0 {
			before[m]--
			continue
		}
		*s = *keep
		return fmt.Errorf("%s", m)
	}
	return nil
}

// samePos reports two positions the same point (however their offsets'
// zero is spelled).
func samePos(a, b score.Pos) bool { return a.Bar == b.Bar && a.Offset.Eq(b.Offset) }

// barsIn checks a run of bars is inside the score.
func barsIn(s *score.Score, a, z int) error {
	if a < 1 || z < a || z > s.Measures() {
		return fmt.Errorf("bars %d to %d are not in the score (1 to %d)", a, z, s.Measures())
	}
	return nil
}

// ---------- tempo ----------

// SetTempoMark puts a tempo mark at its place, in place of any there.
func SetTempoMark(s *score.Score, t score.TempoMark) error {
	return guarded(s, func() error {
		if err := barsIn(s, t.At.Bar, t.At.Bar); err != nil {
			return err
		}
		if t.BPM < 0 || t.BPM > 1000 {
			return fmt.Errorf("%g beats a minute is not a tempo", t.BPM)
		}
		if t.BPM == 0 && strings.TrimSpace(t.Text) == "" {
			return fmt.Errorf("a tempo mark needs words or a number")
		}
		RemoveTempoMark(s, t.At)
		s.Tempo = append(s.Tempo, t)
		s.SortMaps()
		return nil
	})
}

// RemoveTempoMark takes the tempo mark at a place out. It does not guard
// the opening mark (SetTempoMark replaces through it); the editor refuses
// to remove the piece's tempo.
func RemoveTempoMark(s *score.Score, at score.Pos) bool {
	out := s.Tempo[:0:0]
	found := false
	for _, t := range s.Tempo {
		if samePos(t.At, at) {
			found = true
			continue
		}
		out = append(out, t)
	}
	s.Tempo = out
	return found
}

// TempoMarkAt is the tempo mark at a place, or nil.
func TempoMarkAt(s *score.Score, at score.Pos) *score.TempoMark {
	for i := range s.Tempo {
		if samePos(s.Tempo[i].At, at) {
			return &s.Tempo[i]
		}
	}
	return nil
}

// ---------- rehearsal marks ----------

// SetSection puts a rehearsal mark (a section a game can jump to) at a
// bar, in place of one there; names are the game's, so they are unique.
func SetSection(s *score.Score, bar int, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a rehearsal mark needs a name: the game moves between sections by it")
	}
	if err := barsIn(s, bar, bar); err != nil {
		return err
	}
	for _, sec := range s.Sections {
		if sec.Name == name && sec.Bar != bar {
			return fmt.Errorf("bar %d's rehearsal mark is already called %q", sec.Bar, name)
		}
	}
	for i := range s.Sections {
		if s.Sections[i].Bar == bar {
			s.Sections[i].Name = name
			return nil
		}
	}
	s.Sections = append(s.Sections, score.Section{Name: name, Bar: bar})
	s.SortMaps()
	return nil
}

// RemoveSection takes the rehearsal mark at a bar out, and any other
// section's "next" that named it.
func RemoveSection(s *score.Score, bar int) bool {
	out := s.Sections[:0:0]
	gone := ""
	for _, sec := range s.Sections {
		if sec.Bar == bar {
			gone = sec.Name
			continue
		}
		out = append(out, sec)
	}
	for i := range out {
		if gone != "" && out[i].Next == gone {
			out[i].Next = ""
		}
	}
	s.Sections = out
	return gone != ""
}

// ---------- repeats and endings ----------

// SetRepeat repeats bars a..z (played times times; 2 for 0), in place of
// any repeat they overlap.
func SetRepeat(s *score.Score, a, z, times int) error {
	return guarded(s, func() error {
		if err := barsIn(s, a, z); err != nil {
			return err
		}
		if times != 0 && (times < 2 || times > 99) {
			return fmt.Errorf("a repeat plays 2 to 99 times")
		}
		RemoveRepeats(s, a, z)
		s.Repeats = append(s.Repeats, score.Repeat{Start: a, End: z, Times: times})
		s.SortMaps()
		return nil
	})
}

// RemoveRepeats takes out the repeats that overlap bars a..z and says how
// many went; the endings that belonged to them go too.
func RemoveRepeats(s *score.Score, a, z int) int {
	n := 0
	out := s.Repeats[:0:0]
	for _, r := range s.Repeats {
		if r.Start <= z && r.End >= a {
			n++
			continue
		}
		out = append(out, r)
	}
	s.Repeats = out
	if n > 0 {
		// Endings left with no repeat before them.
		vs := s.Voltas[:0:0]
		for _, v := range s.Voltas {
			if s.VoltaRepeat(v) >= 0 {
				vs = append(vs, v)
			}
		}
		s.Voltas = vs
	}
	return n
}

// SetVolta makes bars a..z an ending played on the passes listed, in place
// of any ending they overlap.
func SetVolta(s *score.Score, a, z int, passes []int) error {
	return guarded(s, func() error {
		if err := barsIn(s, a, z); err != nil {
			return err
		}
		if len(passes) == 0 {
			return fmt.Errorf("an ending plays on at least one pass")
		}
		ps := append([]int(nil), passes...)
		sort.Ints(ps)
		RemoveVoltas(s, a, z)
		s.Voltas = append(s.Voltas, score.Volta{Start: a, End: z, Passes: ps})
		s.SortMaps()
		return nil
	})
}

// RemoveVoltas takes out the endings that overlap bars a..z.
func RemoveVoltas(s *score.Score, a, z int) int {
	n := 0
	out := s.Voltas[:0:0]
	for _, v := range s.Voltas {
		if v.Start <= z && v.End >= a {
			n++
			continue
		}
		out = append(out, v)
	}
	s.Voltas = out
	return n
}

// ParsePasses reads an ending's passes: "1", "1, 2", "1-3".
func ParsePasses(t string) ([]int, error) {
	var out []int
	for _, part := range strings.FieldsFunc(t, func(r rune) bool { return r == ',' || r == ' ' || r == '.' }) {
		a, b, ok := strings.Cut(part, "-")
		var x, y int
		if _, err := fmt.Sscanf(a, "%d", &x); err != nil || x < 1 {
			return nil, fmt.Errorf("bad pass %q", part)
		}
		y = x
		if ok {
			if _, err := fmt.Sscanf(b, "%d", &y); err != nil || y < x {
				return nil, fmt.Errorf("bad passes %q", part)
			}
		}
		for p := x; p <= y && p <= 99; p++ {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no passes")
	}
	return out, nil
}

// ---------- navigation marks ----------

// SetJump puts a navigation mark at a bar, in place of one of its kind
// there. A D.S. needs a segno, a To Coda a coda, "al Fine" a Fine and "al
// Coda" a To Coda: put those first.
func SetJump(s *score.Score, j score.Jump) error {
	return guarded(s, func() error {
		if err := barsIn(s, j.Bar, j.Bar); err != nil {
			return err
		}
		if j.Kind < score.JumpSegno || j.Kind > score.JumpDS {
			return fmt.Errorf("no navigation mark %d", j.Kind)
		}
		RemoveJumps(s, j.Bar, j.Kind)
		s.Jumps = append(s.Jumps, j)
		s.SortMaps()
		return nil
	})
}

// RemoveJumps takes the navigation marks of a kind (any, for 0) at a bar
// out.
func RemoveJumps(s *score.Score, bar int, k score.JumpKind) int {
	n := 0
	out := s.Jumps[:0:0]
	for _, j := range s.Jumps {
		if j.Bar == bar && (k == 0 || j.Kind == k) {
			n++
			continue
		}
		out = append(out, j)
	}
	s.Jumps = out
	return n
}

// ---------- clef changes ----------

// SetClefChange changes a staff's clef from a point on (in place of a
// change there). A change at the staff's very start is its clef.
func SetClefChange(s *score.Score, line, staff int, at score.Pos, clef string) error {
	st := Staff(s, line, staff)
	if st == nil {
		return fmt.Errorf("no staff %d of line %d", staff+1, line+1)
	}
	if st.Clef == "percussion" || clef == "percussion" {
		return fmt.Errorf("a percussion staff keeps its clef")
	}
	ok := false
	for _, c := range score.StaffClefs() {
		if c == clef {
			ok = true
		}
	}
	if !ok {
		return fmt.Errorf("no clef %q", clef)
	}
	if err := barsIn(s, at.Bar, at.Bar); err != nil {
		return err
	}
	if at.Offset.Sign() < 0 || !at.Offset.Less(s.BarLength(at.Bar)) {
		return fmt.Errorf("%s is outside bar %d", at.Offset, at.Bar)
	}
	if at.Bar == 1 && at.Offset.IsZero() {
		st.Clef = clef
		return nil
	}
	RemoveClefChange(s, line, staff, at)
	st.Clefs = append(st.Clefs, score.ClefChange{At: at, Clef: clef})
	st.SortClefs()
	return nil
}

// RemoveClefChange takes a clef change out.
func RemoveClefChange(s *score.Score, line, staff int, at score.Pos) bool {
	st := Staff(s, line, staff)
	if st == nil {
		return false
	}
	out := st.Clefs[:0:0]
	found := false
	for _, c := range st.Clefs {
		if samePos(c.At, at) {
			found = true
			continue
		}
		out = append(out, c)
	}
	st.Clefs = out
	return found
}
