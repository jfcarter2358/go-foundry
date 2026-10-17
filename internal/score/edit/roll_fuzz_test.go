package edit

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"foundry/internal/score"
)

// rollSet is a line's notes as a sorted multiset of "midi@start-end".
func rollSet(ns []RollNote) []string {
	var out []string
	for _, n := range ns {
		out = append(out, fmt.Sprintf("%d@%s-%s", n.MIDI(), n.Start, n.End))
	}
	sort.Strings(out)
	return out
}

func remove1(set []string, x string) ([]string, bool) {
	for i, s := range set {
		if s == x {
			return append(set[:i:i], set[i+1:]...), true
		}
	}
	return set, false
}

// TestRollRandomEditsKeepEveryNote drives the piano roll's write-back with
// random draws, deletes and moves on grids of eighths, triplets and
// sixteenths, in 4/4, 6/8 and 3+2+2/8, and checks after every step that
// exactly the notes asked for sound — nothing lost, cut or doubled when a
// note goes into a free voice — and that every bar is still full and
// valid.
func TestRollRandomEditsKeepEveryNote(t *testing.T) {
	grids := []score.Frac{f(1, 8), f(1, 12), f(1, 16), f(1, 4)}
	for _, meter := range []string{"4/4", "6/8", "3+2+2/8"} {
		for seed := int64(1); seed <= 40; seed++ {
			r := rand.New(rand.NewSource(seed))
			s := one(t, meter, int(seed%3)-1, "| R | R | R | R |")
			starts := s.BarStarts()
			total := starts[len(starts)-1]
			var want []string
			for step := 0; step < 60; step++ {
				ns := RollNotes(s, 0)
				g := grids[r.Intn(len(grids))]
				slots := total.Div(g).Floor()
				what := r.Intn(10)
				switch {
				case what < 5 || len(ns) == 0: // draw
					a := r.Int63n(slots - 1)
					b := a + 1 + r.Int63n(min(slots-a, 8))
					st, en := g.MulInt(a), g.MulInt(b)
					midi := 55 + r.Intn(24)
					v := RollFreeVoice(s, 0, 0, st, en, 0)
					if v < 0 {
						continue
					}
					n := RollNote{Voice: v, Start: st, End: en, Pitch: score.SpellMIDI(midi, 0, 1)}
					if _, err := RollAdd(s, 0, n); err != nil {
						t.Fatalf("%s seed %d step %d: add %d@%s-%s: %v", meter, seed, step, midi, st, en, err)
					}
					// A note starting and stopping with one of the same
					// pitch in its voice is that note.
					dup := false
					for _, o := range ns {
						if o.Voice == v && o.MIDI() == midi && o.Start.Eq(st) && o.End.Eq(en) {
							dup = true
						}
					}
					if !dup {
						want = append(want, fmt.Sprintf("%d@%s-%s", midi, st, en))
					}
				case what < 7: // delete
					n := ns[r.Intn(len(ns))]
					if err := RollRemove(s, 0, n); err != nil {
						t.Fatalf("%s seed %d step %d: remove: %v", meter, seed, step, err)
					}
					var ok bool
					if want, ok = remove1(want, fmt.Sprintf("%d@%s-%s", n.MIDI(), n.Start, n.End)); !ok {
						t.Fatalf("removed a note the test did not know")
					}
				default: // move one or two notes by a grid step and some semitones
					k := 1 + r.Intn(min(2, len(ns)))
					perm := r.Perm(len(ns))[:k]
					d := g.MulInt(int64(r.Intn(5) - 2))
					dm := r.Intn(7) - 3
					var dEnd score.Frac // a stretch, now and then
					if r.Intn(3) == 0 {
						d, dm, dEnd = score.Frac{}, 0, g.MulInt(int64(r.Intn(5)-1))
					}
					var olds, news []RollNote
					ok := true
					for _, i := range perm {
						o := ns[i]
						n := o
						n.Parts = nil
						n.Start, n.End = o.Start.Add(d), o.End.Add(d).Add(dEnd)
						if !n.Start.Less(n.End) {
							ok = false
						}
						n.Pitch = score.SpellMIDI(o.MIDI()+dm, 0, 1)
						if n.Start.Sign() < 0 || total.Less(n.End) {
							ok = false
						}
						olds, news = append(olds, o), append(news, n)
					}
					if !ok {
						continue
					}
					got, err := RollMove(s, 0, olds, news, true)
					if err != nil {
						// No voice free for it: the move is refused whole
						// and nothing changes.
						if g := rollSet(RollNotes(s, 0)); strings.Join(g, " ") != strings.Join(rollSet(ns), " ") {
							t.Fatalf("%s seed %d step %d: a refused move changed the notes: %v", meter, seed, step, err)
						}
						continue
					}
					for i, o := range olds {
						want, _ = remove1(want, fmt.Sprintf("%d@%s-%s", o.MIDI(), o.Start, o.End))
						n := got[i]
						want = append(want, fmt.Sprintf("%d@%s-%s", n.MIDI(), n.Start, n.End))
						if !n.Start.Eq(news[i].Start) || !n.End.Eq(news[i].End) || n.MIDI() != news[i].Pitch.MIDI() {
							t.Fatalf("%s seed %d step %d: moved to %d@%s-%s, asked %d@%s-%s", meter, seed, step,
								n.MIDI(), n.Start, n.End, news[i].Pitch.MIDI(), news[i].Start, news[i].End)
						}
					}
					// Two moved notes landing on one place are one note.
					sort.Strings(want)
					want = dedupeSameVoice(s, want)
				}
				fullBars(t, s)
				got := rollSet(RollNotes(s, 0))
				sort.Strings(want)
				if strings.Join(got, " ") != strings.Join(want, " ") {
					t.Fatalf("%s seed %d step %d (op %d):\n got %v\nwant %v\nvoices:\n%s\n%s", meter, seed, step, what, got, want, voiceText(s, 0), voiceText(s, 1))
				}
			}
		}
	}
}

// dedupeSameVoice drops expected notes the roll holds once: two equal
// notes in one voice are one (a chord does not hold a pitch twice).
func dedupeSameVoice(s *score.Score, want []string) []string {
	have := map[string]int{}
	for _, x := range rollSet(RollNotes(s, 0)) {
		have[x]++
	}
	var out []string
	seen := map[string]int{}
	for _, x := range want {
		seen[x]++
		if seen[x] <= have[x] || have[x] == 0 {
			out = append(out, x)
		}
	}
	return out
}

// A move no voice can take is refused whole: the score is as it was, the
// note it would have cut still there.
func TestRollMoveRefusedLeavesTheScore(t *testing.T) {
	s := one(t, "4/4", 0, "| wC4 | wD4 |", "| wE4 | wF4 |", "| wG4 | wA4 |", "| wB4 | wC5 |")
	before := rollText(s)
	ns := RollNotes(s, 0)
	var c RollNote
	for _, n := range ns {
		if n.MIDI() == 60 {
			c = n
		}
	}
	n := c
	n.Parts = nil
	n.Start, n.End = f(1, 2), f(3, 2)
	n.Pitch = pitch(t, "C#4")
	if _, err := RollMove(s, 0, []RollNote{c}, []RollNote{n}, true); err == nil {
		t.Fatal("a move with every voice busy was taken")
	}
	if got := rollText(s); got != before {
		t.Errorf("a refused move changed the roll:\n got %s\nwant %s", got, before)
	}
	fullBars(t, s)
}
