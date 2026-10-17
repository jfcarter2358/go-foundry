package edit

// lanes.go — automation lanes as the timeline edits them: adding and
// removing a line's (or a bus's, or the master's) lane, and its
// breakpoints added, moved, removed, reshaped and drawn freehand.
//
// A lane's points are kept in time order, so the timeline's index for a
// point is the file's. Values are clamped to what the lane's target takes
// (score.LaneTarget.Range) and a stepped target's (a switch, a count)
// rounded; positions are written positions (bar and offset), the same the
// file and the score view use.

import (
	"fmt"
	"math"
	"sort"

	"foundry/internal/score"
)

// lane finds an owner's lane by name, with its target. The owner (li
// in every function here) is a line's index, or score.OwnerBusA,
// OwnerBusB or OwnerMaster.
func lane(s *score.Score, li int, name string) (*[]score.Lane, *score.Lane, score.LaneTarget, error) {
	ls := s.LanesOf(li)
	if ls == nil {
		return nil, nil, score.LaneTarget{}, fmt.Errorf("no line %d", li+1)
	}
	t, err := score.ParseLaneTargetIn(score.OwnerScope(li), name)
	if err != nil {
		return nil, nil, t, err
	}
	ln := s.LaneOf(li, name)
	if ln == nil {
		return ls, nil, t, fmt.Errorf("%s has no %s lane", s.OwnerName(li), name)
	}
	return ls, ln, t, nil
}

// AddLane gives an owner a lane for a target, with one point at the top
// of the score holding what it plays without it (Score.LaneDefaultOf), so
// adding a lane changes nothing until it is drawn. A target that only
// takes whole values starts stepped.
func AddLane(s *score.Score, li int, name string) error {
	ls := s.LanesOf(li)
	if ls == nil {
		return fmt.Errorf("no line %d", li+1)
	}
	t, err := score.ParseLaneTargetIn(score.OwnerScope(li), name)
	if err != nil {
		return err
	}
	name = t.Name()
	if s.LaneOf(li, name) != nil {
		return fmt.Errorf("%s already has a %s lane", s.OwnerName(li), t.Label())
	}
	if t.Kind == score.LaneFX && score.ChainSlot(*s.ChainOf(li), t) < 0 {
		what := "insert"
		if li < 0 {
			what = "effect"
		}
		return fmt.Errorf("%s has no %s %s", s.OwnerName(li), t.FX.Spec().Label, what)
	}
	p := score.LanePoint{At: score.Pos{Bar: 1}, Value: t.Clamp(s.LaneDefaultOf(li, t))}
	if t.Stepped() {
		p.Shape = score.LaneStep
	}
	*ls = append(*ls, score.Lane{Name: name, Points: []score.LanePoint{p}})
	return nil
}

// RemoveLane takes a lane off its owner.
func RemoveLane(s *score.Score, li int, name string) error {
	ls, ln, _, err := lane(s, li, name)
	if err != nil {
		return err
	}
	for i := range *ls {
		if &(*ls)[i] == ln {
			*ls = append((*ls)[:i:i], (*ls)[i+1:]...)
			break
		}
	}
	if len(*ls) == 0 {
		*ls = nil
	}
	return nil
}

// posFrac is a written position in whole notes from the top.
func posFrac(s *score.Score, p score.Pos, starts []score.Frac) score.Frac {
	return s.PosFrac(p, starts)
}

// sortPoints puts a lane's points in time order (stable: two at one place
// keep theirs) and returns where the point that was at index i went.
func sortPoints(s *score.Score, ln *score.Lane, i int) int {
	starts := s.BarStarts()
	type ix struct {
		p score.LanePoint
		k int
	}
	ps := make([]ix, len(ln.Points))
	for k, p := range ln.Points {
		ps[k] = ix{p, k}
	}
	sort.SliceStable(ps, func(a, b int) bool {
		return posFrac(s, ps[a].p.At, starts).Less(posFrac(s, ps[b].p.At, starts))
	})
	out := i
	for k, p := range ps {
		ln.Points[k] = p.p
		if p.k == i {
			out = k
		}
	}
	return out
}

// samePos compares two positions as places in the score.
func samePosIn(s *score.Score, a, b score.Pos, starts []score.Frac) bool {
	return posFrac(s, a, starts).Eq(posFrac(s, b, starts))
}

// AddLanePoint puts a breakpoint into a lane and returns its index. A
// point already at that place takes the new value instead. The new point
// takes the shape of the segment it splits, so a stepped lane stays
// stepped.
func AddLanePoint(s *score.Score, li int, name string, at score.Pos, v float32) (int, error) {
	_, ln, t, err := lane(s, li, name)
	if err != nil {
		return -1, err
	}
	if at.Bar < 1 {
		return -1, fmt.Errorf("a point needs a bar")
	}
	v = t.Clamp(v)
	starts := s.BarStarts()
	at = s.PosAt(posFrac(s, at, starts), starts)
	for i := range ln.Points {
		if samePosIn(s, ln.Points[i].At, at, starts) {
			ln.Points[i].Value = v
			return i, nil
		}
	}
	p := score.LanePoint{At: at, Value: v}
	x := posFrac(s, at, starts)
	for i := len(ln.Points) - 1; i >= 0; i-- {
		if posFrac(s, ln.Points[i].At, starts).Less(x) {
			p.Shape, p.Curve = ln.Points[i].Shape, ln.Points[i].Curve
			break
		}
	}
	if t.Stepped() {
		p.Shape = score.LaneStep
	}
	ln.Points = append(ln.Points, p)
	return sortPoints(s, ln, len(ln.Points)-1), nil
}

// MoveLanePoint moves point i to a place and a value. It cannot pass its
// neighbours: a drag stops at the point beside it (as a DAW's does), so
// the index stays the same. A point that holds (HasTo) keeps its hold's
// length.
func MoveLanePoint(s *score.Score, li int, name string, i int, at score.Pos, v float32) error {
	_, ln, t, err := lane(s, li, name)
	if err != nil {
		return err
	}
	if i < 0 || i >= len(ln.Points) {
		return fmt.Errorf("no point %d", i+1)
	}
	starts := s.BarStarts()
	end := starts[len(starts)-1]
	x := score.MaxFrac(posFrac(s, at, starts), score.Frac{})
	if i > 0 {
		prev := ln.Points[i-1]
		lo := posFrac(s, prev.At, starts)
		if prev.HasTo {
			lo = posFrac(s, prev.To, starts)
		}
		x = score.MaxFrac(x, lo)
	}
	p := &ln.Points[i]
	hold := score.Frac{}
	if p.HasTo {
		hold = posFrac(s, p.To, starts).Sub(posFrac(s, p.At, starts))
	}
	if i+1 < len(ln.Points) {
		x = score.MinFrac(x, posFrac(s, ln.Points[i+1].At, starts).Sub(hold))
	}
	x = score.MinFrac(x, end)
	p.At = s.PosAt(x, starts)
	if p.HasTo {
		p.To = s.PosAt(x.Add(hold), starts)
	}
	p.Value = t.Clamp(v)
	return nil
}

// RemoveLanePoint takes point i out of a lane. A lane keeps its last
// point: RemoveLane takes the lane.
func RemoveLanePoint(s *score.Score, li int, name string, i int) error {
	_, ln, _, err := lane(s, li, name)
	if err != nil {
		return err
	}
	if i < 0 || i >= len(ln.Points) {
		return fmt.Errorf("no point %d", i+1)
	}
	if len(ln.Points) == 1 {
		return fmt.Errorf("a lane keeps one point; remove the lane instead")
	}
	ln.Points = append(ln.Points[:i:i], ln.Points[i+1:]...)
	return nil
}

// SetLaneShape sets how the segment after point i moves: straight, a step,
// or a curve bent by curve (-1..1; 0 is straight).
func SetLaneShape(s *score.Score, li int, name string, i int, shape score.LaneShape, curve float32) error {
	_, ln, t, err := lane(s, li, name)
	if err != nil {
		return err
	}
	if i < 0 || i >= len(ln.Points) {
		return fmt.Errorf("no point %d", i+1)
	}
	if t.Stepped() && shape != score.LaneStep {
		return fmt.Errorf("%s takes whole values: its lane steps", t.Label())
	}
	p := &ln.Points[i]
	p.Shape, p.Curve = shape, 0
	if shape == score.LaneCurve {
		p.Curve = min(max(curve, -1), 1)
	}
	return nil
}

// DrawLane replaces a lane's points between from and to (inclusive) with
// points drawn freehand, in time order — the draw tool's stroke, thinned
// to tol (a fraction of the lane's range, on its log scale for a
// frequency) so a slow stroke is not a point a pixel. Past the stroke the
// lane runs from its last point on to the next point that was there, as a
// DAW's pencil does.
func DrawLane(s *score.Score, li int, name string, from, to score.Pos, drawn []score.LanePoint, tol float32) error {
	_, ln, t, err := lane(s, li, name)
	if err != nil {
		return err
	}
	starts := s.BarStarts()
	a, b := posFrac(s, from, starts), posFrac(s, to, starts)
	if b.Less(a) {
		a, b = b, a
	}
	var kept []score.LanePoint
	for _, p := range ln.Points {
		x := posFrac(s, p.At, starts)
		if x.Less(a) || b.Less(x) {
			kept = append(kept, p)
		}
	}
	stroke := thin(s, drawn, t, tol, starts)
	shape := score.LaneLinear
	if t.Stepped() {
		shape = score.LaneStep
	}
	for _, p := range stroke {
		x := posFrac(s, p.At, starts)
		if x.Less(a) || b.Less(x) {
			continue
		}
		kept = append(kept, score.LanePoint{At: s.PosAt(x, starts), Value: t.Clamp(p.Value), Shape: shape})
	}
	if len(kept) == 0 {
		return fmt.Errorf("nothing drawn")
	}
	ln.Points = kept
	sortPoints(s, ln, 0)
	// Two points at one place: the later (the stroke's) wins.
	out := ln.Points[:0]
	for i, p := range ln.Points {
		if i+1 < len(ln.Points) && samePosIn(s, p.At, ln.Points[i+1].At, starts) {
			continue
		}
		out = append(out, p)
	}
	ln.Points = out
	return nil
}

// thin drops drawn points that lie within tol of the straight line
// between the points kept either side of them (Ramer–Douglas–Peucker over
// time and value).
func thin(s *score.Score, pts []score.LanePoint, t score.LaneTarget, tol float32, starts []score.Frac) []score.LanePoint {
	if len(pts) <= 2 || tol <= 0 {
		return pts
	}
	keep := make([]bool, len(pts))
	keep[0], keep[len(pts)-1] = true, true
	xs := make([]float64, len(pts))
	ys := make([]float64, len(pts))
	lo, hi := t.Range()
	for i, p := range pts {
		xs[i] = posFrac(s, p.At, starts).Float()
		ys[i] = LaneNorm(t, p.Value, lo, hi)
	}
	var walk func(a, b int)
	walk = func(a, b int) {
		if b-a < 2 {
			return
		}
		worst, far := -1, float32(0)
		for i := a + 1; i < b; i++ {
			v := ys[a]
			if span := xs[b] - xs[a]; span > 0 {
				v += (ys[b] - ys[a]) * (xs[i] - xs[a]) / span
			}
			if d := float32(math.Abs(ys[i] - v)); d > far {
				worst, far = i, d
			}
		}
		if worst >= 0 && far > tol {
			keep[worst] = true
			walk(a, worst)
			walk(worst, b)
		}
	}
	walk(0, len(pts)-1)
	var out []score.LanePoint
	for i, p := range pts {
		if keep[i] {
			out = append(out, p)
		}
	}
	return out
}

// LaneNorm is a lane value as a fraction of its target's range, 0 at the
// bottom and 1 at the top — on a log scale for a frequency or a time —
// the way the timeline draws it.
func LaneNorm(t score.LaneTarget, v, lo, hi float32) float64 {
	if hi <= lo {
		return 0
	}
	if t.Log() && lo > 0 && v > 0 {
		return math.Log(float64(v/lo)) / math.Log(float64(hi/lo))
	}
	return float64((v - lo) / (hi - lo))
}

// LaneDenorm is LaneNorm's inverse.
func LaneDenorm(t score.LaneTarget, n float64, lo, hi float32) float32 {
	n = math.Min(math.Max(n, 0), 1)
	if t.Log() && lo > 0 {
		return lo * float32(math.Pow(float64(hi/lo), n))
	}
	return lo + (hi-lo)*float32(n)
}
