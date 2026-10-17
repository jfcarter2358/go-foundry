package musicxml

// stems.go — every note's stem, as the page has it.
//
// A notation program reading a file without <stem> elements chooses its
// own directions, and they need not be ours: MuseScore draws a drum set's
// voices all stems up, say, where the score had the kit's two voices
// split. So export writes the direction the engraver draws for every
// stemmed note (internal/score/engrave, the score at written pitch), not
// only where a mark forces one. Reading such a file back — one of ours —
// keeps a stem as a forced mark only where it differs from what the
// engraver would choose with no marks at all, as the tablature's string
// choices are kept (looseStrings), so a round trip does not turn every
// stem into a mark.

import (
	"foundry/internal/score"
	"foundry/internal/score/engrave"
)

// stemKey is an event of the score.
type stemKey struct{ line, staff, voice, bar, event int }

// stemDirs is the direction (up true) of every stem the engraver draws for
// a score, by event.
func stemDirs(s *score.Score) map[stemKey]bool {
	l := engrave.Engrave(s, engrave.Options{BarNumbers: -1})
	key := func(r engrave.Ref) stemKey { return stemKey{r.Line, r.Staff, r.Voice, r.Bar, r.Event} }
	head := map[stemKey]float64{}
	for i := range l.Items {
		it := &l.Items[i]
		if it.Role == engrave.RoleNotehead && it.Kind == engrave.ItemGlyph {
			k := key(it.Ref)
			if _, ok := head[k]; !ok {
				head[k] = it.P.Y
			}
		}
	}
	out := map[stemKey]bool{}
	for i := range l.Items {
		it := &l.Items[i]
		if it.Role != engrave.RoleStem || it.Kind != engrave.ItemLine {
			continue
		}
		k := key(it.Ref)
		y, ok := head[k]
		if !ok {
			continue
		}
		out[k] = (it.P.Y+it.Q.Y)/2 < y
	}
	return out
}

// looseStems drops the stem marks an imported score of ours carries where
// the engraver would draw the stem that way anyway.
func looseStems(s *score.Score) {
	bare := s.Clone()
	type at struct {
		k  stemKey
		up bool
	}
	var marked []at
	for li, l := range bare.Lines {
		for si, st := range l.Staves {
			for vi, v := range st.Voices {
				for bi := range v.Measures {
					for ei := range v.Measures[bi].Events {
						e := &v.Measures[bi].Events[ei]
						var keep []score.Mark
						for _, m := range e.Marks {
							if m.Name == "up" || m.Name == "down" {
								marked = append(marked, at{stemKey{li, si, vi, bi + 1, ei}, m.Name == "up"})
								continue
							}
							keep = append(keep, m)
						}
						e.Marks = keep
					}
				}
			}
		}
	}
	if len(marked) == 0 {
		return
	}
	dirs := stemDirs(bare)
	for _, m := range marked {
		up, ok := dirs[m.k]
		if !ok || up != m.up {
			continue
		}
		e := &s.Lines[m.k.line].Staves[m.k.staff].Voices[m.k.voice].Measures[m.k.bar-1].Events[m.k.event]
		var keep []score.Mark
		for _, mk := range e.Marks {
			if mk.Name != "up" && mk.Name != "down" {
				keep = append(keep, mk)
			}
		}
		e.Marks = keep
	}
}
