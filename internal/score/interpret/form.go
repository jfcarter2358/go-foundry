package interpret

// form.go — the bars in the order they play.
//
// The rules, as a player reads them:
//
//   - A repeat plays its bars Times times (2 by default), jumping back
//     from the end of its last bar to its first.
//   - An ending (volta) plays only on the passes it lists, counted through
//     the repeat it belongs to; skipped, playback goes straight on past it,
//     so a first ending skipped on the last pass never jumps back.
//   - A D.C. (at the end of its bar) goes back to the top, a D.S. to the
//     segno, each once. On the way through after it, a Fine ends the piece
//     and a To Coda goes on to the coda (al Fine / al Coda say which; a
//     plain D.C. follows whichever the score has). Repeats are played once
//     on the way through, with their last ending, unless the jump says
//     Repeats (con rip.).
//   - At the end of a bar the order of precedence is Fine, To Coda, the
//     repeat's jump back, then a D.C. or D.S.
//
// Bar.Pass counts the times a written bar has played.

import (
	"sort"

	"foundry/internal/score"
)

// layout unrolls the form into the bars in playing order.
func (in *interp) layout() {
	s := in.s
	n := s.Measures()
	in.starts = s.BarStarts()
	straight := in.o.NoRepeats || s.Playback.IgnoreRepeats
	var reps []score.Repeat
	if !straight {
		for _, r := range s.Repeats {
			if r.Start >= 1 && r.End <= n && r.End >= r.Start {
				reps = append(reps, r)
			}
		}
		sort.SliceStable(reps, func(i, j int) bool { return reps[i].Start < reps[j].Start })
	}
	// voltaRep is the repeat each ending belongs to (index into reps).
	voltaRep := make([]int, len(s.Voltas))
	for i, v := range s.Voltas {
		voltaRep[i] = -1
		for k, r := range reps {
			if r.Start <= v.Start {
				voltaRep[i] = k
			}
		}
	}
	jumpTo := func(k score.JumpKind) int {
		b := s.FirstJump(k)
		if b < 1 || b > n {
			return 0
		}
		return b
	}
	count := make([]int, len(reps))  // jumps back taken this time through
	passOf := make([]int, len(reps)) // the pass a repeat is on, 0 before it starts
	times := make([]int, n+1)
	exhausted := false // after a D.C./D.S. that does not take the repeats again
	jumped := false
	var al score.JumpKind
	taken := make([]bool, len(s.Jumps))
	back := -1 // the repeat that just jumped back

	at := score.Frac{Num: 0, Den: 1}
	for b := 1; b <= n && len(in.order) < maxPlayedBars; {
		// Arriving at a repeat's first bar, other than by its own jump
		// back, starts it afresh.
		for k, r := range reps {
			if r.Start == b && k != back && !(jumped && passOf[k] > 0) {
				if exhausted {
					passOf[k], count[k] = r.Plays(), r.Plays()-1
				} else {
					passOf[k], count[k] = 1, 0
				}
			}
		}
		back = -1
		if !straight {
			if vi := voltaIndex(s, b); vi >= 0 {
				pass := 1
				if k := voltaRep[vi]; k >= 0 && passOf[k] > 0 {
					pass = passOf[k]
				}
				if !s.Voltas[vi].PlaysOn(pass) {
					b++
					continue
				}
			}
		}
		times[b]++
		l := s.BarLength(b)
		in.order = append(in.order, played{bar: b, pass: times[b], start: at, length: l})
		at = at.Add(l)
		if straight {
			b++
			continue
		}
		if jumped && s.JumpAt(b, score.JumpFine) != nil && al != score.JumpCoda {
			break
		}
		if jumped && s.JumpAt(b, score.JumpToCoda) != nil && al != score.JumpFine {
			if c := jumpTo(score.JumpCoda); c > 0 && c != b+1 {
				b = c
				continue
			}
		}
		took := false
		for k, r := range reps {
			if b == r.End && passOf[k] > 0 && count[k]+1 < r.Plays() {
				count[k]++
				passOf[k]++
				b, back, took = r.Start, k, true
				break
			}
		}
		if took {
			continue
		}
		for ji, j := range s.Jumps {
			if taken[ji] || j.Bar != b || (j.Kind != score.JumpDC && j.Kind != score.JumpDS) {
				continue
			}
			to := 1
			if j.Kind == score.JumpDS {
				if to = jumpTo(score.JumpSegno); to == 0 {
					continue
				}
			}
			taken[ji] = true
			jumped, al = true, j.Al
			exhausted = !j.Repeats
			for k, r := range reps {
				passOf[k], count[k] = 0, 0
				if r.Start <= to && to <= r.End {
					// Landing inside a repeat: on its last pass, or
					// its first when the repeats are taken again.
					passOf[k], count[k] = 1, 0
					if exhausted {
						passOf[k], count[k] = r.Plays(), r.Plays()-1
					}
				}
			}
			b, took = to, true
			break
		}
		if took {
			continue
		}
		b++
	}
}

// voltaIndex is the index of the ending a bar is in, -1 for none.
func voltaIndex(s *score.Score, bar int) int {
	for i, v := range s.Voltas {
		if bar >= v.Start && bar <= v.End {
			return i
		}
	}
	return -1
}
