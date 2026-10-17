package edit

// mix.go — effect chains: adding, removing and reordering effects on a
// line's inserts, a send bus or the master, and the automation lanes that
// go with them.
//
// A lane names its effect by kind and number among the line's effects of
// that kind ("eq#2.low"), so when the chain changes the lanes are renamed
// to keep following the same effect: removing the first of two EQs makes
// the second's lanes plain "eq.…"; removing an effect drops its lanes
// (RemoveInsert reports how many).

import (
	"fmt"

	"foundry/internal/score"
	"foundry/internal/tracker/fx"
)

// MaxEffects is the most effects a chain holds.
const MaxEffects = 8

// AddEffect puts an effect into a chain at index at (clamped; -1 or past
// the end appends) and returns where it went.
func AddEffect(chain *[]score.FX, at int, f score.FX) (int, error) {
	if len(*chain) >= MaxEffects {
		return -1, fmt.Errorf("a chain holds at most %d effects", MaxEffects)
	}
	if _, ok := fx.Lookup(f.Kind); !ok {
		return -1, fmt.Errorf("no effect %q", f.Kind)
	}
	if at < 0 || at > len(*chain) {
		at = len(*chain)
	}
	c := append(*chain, score.FX{})
	copy(c[at+1:], c[at:])
	c[at] = f
	*chain = c
	return at, nil
}

// RemoveEffect takes effect i out of a chain.
func RemoveEffect(chain *[]score.FX, i int) error {
	if i < 0 || i >= len(*chain) {
		return fmt.Errorf("no effect %d", i+1)
	}
	*chain = append((*chain)[:i:i], (*chain)[i+1:]...)
	return nil
}

// MoveEffect moves effect i to index to.
func MoveEffect(chain *[]score.FX, i, to int) error {
	c := *chain
	if i < 0 || i >= len(c) || to < 0 || to >= len(c) {
		return fmt.Errorf("no effect %d or place %d", i+1, to+1)
	}
	f := c[i]
	if i < to {
		copy(c[i:to], c[i+1:to+1])
	} else {
		copy(c[to+1:i+1], c[to:i])
	}
	c[to] = f
	return nil
}

// laneSlots is, for each of an owner's lanes, the chain slot its effect
// lane points at (-1 for a lane that is not an effect's, -2 for one that
// points at nothing).
func laneSlots(sc score.LaneScope, lanes []score.Lane, chain []score.FX) []int {
	out := make([]int, len(lanes))
	for i, ln := range lanes {
		t, err := score.ParseLaneTargetIn(sc, ln.Name)
		switch {
		case err != nil || t.Kind != score.LaneFX:
			out[i] = -1
		default:
			if out[i] = score.ChainSlot(chain, t); out[i] < 0 {
				out[i] = -2
			}
		}
	}
	return out
}

// relane renames an owner's effect lanes after its chain changed: each
// lane's slot goes through moved (old slot to new, -1 for removed), and
// lanes of removed effects go. It returns how many lanes went.
func relane(sc score.LaneScope, lanes *[]score.Lane, chain []score.FX, slots []int, moved func(int) int) int {
	var keep []score.Lane
	gone := 0
	for i, ln := range *lanes {
		if slots[i] < 0 {
			keep = append(keep, ln)
			continue
		}
		ns := moved(slots[i])
		t, err := score.ParseLaneTargetIn(sc, ln.Name)
		if ns < 0 || err != nil {
			gone++
			continue
		}
		if nt, ok := score.ChainTargetForSlot(chain, ns, t.Param); ok {
			ln.Name = nt.Name()
		}
		keep = append(keep, ln)
	}
	*lanes = keep
	return gone
}

// owned is an owner's chain and lanes, for the chain edits.
func owned(s *score.Score, owner int) (*[]score.FX, *[]score.Lane, score.LaneScope, error) {
	ch, ls := s.ChainOf(owner), s.LanesOf(owner)
	if ch == nil || ls == nil {
		return nil, nil, 0, fmt.Errorf("no line %d", owner+1)
	}
	return ch, ls, score.OwnerScope(owner), nil
}

// AddOwnedEffect adds an effect to an owner's chain (a line's inserts, a
// bus's effects, the master's) at index at; the owner's lanes keep
// following their effects.
func AddOwnedEffect(s *score.Score, owner, at int, f score.FX) (int, error) {
	ch, ls, sc, err := owned(s, owner)
	if err != nil {
		return -1, err
	}
	slots := laneSlots(sc, *ls, *ch)
	i, err := AddEffect(ch, at, f)
	if err != nil {
		return -1, err
	}
	relane(sc, ls, *ch, slots, func(s int) int {
		if s >= i {
			return s + 1
		}
		return s
	})
	return i, nil
}

// RemoveOwnedEffect takes effect i off an owner's chain, with its lanes;
// it returns how many lanes went with it.
func RemoveOwnedEffect(s *score.Score, owner, i int) (int, error) {
	ch, ls, sc, err := owned(s, owner)
	if err != nil {
		return 0, err
	}
	slots := laneSlots(sc, *ls, *ch)
	if err := RemoveEffect(ch, i); err != nil {
		return 0, err
	}
	return relane(sc, ls, *ch, slots, func(s int) int {
		switch {
		case s == i:
			return -1
		case s > i:
			return s - 1
		}
		return s
	}), nil
}

// MoveOwnedEffect moves effect i of an owner's chain to index to; its
// lanes go with it.
func MoveOwnedEffect(s *score.Score, owner, i, to int) error {
	ch, ls, sc, err := owned(s, owner)
	if err != nil {
		return err
	}
	slots := laneSlots(sc, *ls, *ch)
	if err := MoveEffect(ch, i, to); err != nil {
		return err
	}
	relane(sc, ls, *ch, slots, func(s int) int {
		switch {
		case s == i:
			return to
		case i < to && s > i && s <= to:
			return s - 1
		case to < i && s >= to && s < i:
			return s + 1
		}
		return s
	})
	return nil
}

// OwnerHasEffectLanes reports whether any of an owner's lanes moves an
// effect of its chain.
func OwnerHasEffectLanes(s *score.Score, owner int) bool {
	ch, ls, sc, err := owned(s, owner)
	if err != nil {
		return false
	}
	for _, k := range laneSlots(sc, *ls, *ch) {
		if k >= 0 {
			return true
		}
	}
	return false
}

// AddInsert adds an effect to a line's inserts at index at; its lanes
// keep following their effects.
func AddInsert(l *score.Line, at int, f score.FX) (int, error) {
	return AddOwnedEffect(lineScore(l), 0, at, f)
}

// RemoveInsert takes insert i off a line, with its lanes; it returns how
// many lanes went with it.
func RemoveInsert(l *score.Line, i int) (int, error) {
	return RemoveOwnedEffect(lineScore(l), 0, i)
}

// MoveInsert moves insert i to index to; its lanes go with it.
func MoveInsert(l *score.Line, i, to int) error {
	return MoveOwnedEffect(lineScore(l), 0, i, to)
}

// HasEffectLanes reports whether any of the line's lanes moves an insert.
func HasEffectLanes(l *score.Line) bool { return OwnerHasEffectLanes(lineScore(l), 0) }

// lineScore is a score holding only l (shared, not copied), for the line
// forms of the owner edits.
func lineScore(l *score.Line) *score.Score { return &score.Score{Lines: []*score.Line{l}} }
