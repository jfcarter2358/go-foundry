package edit

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/tracker/fx"
)

func pointsText(s *score.Score, ln *score.Lane) string {
	var out []string
	for _, p := range ln.Points {
		t := fmt.Sprintf("%s=%g", s.FormatPos(p.At), p.Value)
		switch p.Shape {
		case score.LaneStep:
			t += "/step"
		case score.LaneCurve:
			t += fmt.Sprintf("/curve%g", p.Curve)
		}
		out = append(out, t)
	}
	return strings.Join(out, " ")
}

func TestLaneEditing(t *testing.T) {
	s := one(t, "4/4", 0, "| wC5 | wD5 | wE5 | wF5 |")
	l := s.Lines[0]
	l.Pan = -0.25
	if err := AddLane(s, 0, "pan"); err != nil {
		t.Fatal(err)
	}
	if err := AddLane(s, 0, "pan"); err == nil {
		t.Error("a second pan lane")
	}
	if err := AddLane(s, 0, "eq.low"); err == nil {
		t.Error("an EQ lane with no EQ")
	}
	ln := l.Lane("pan")
	if got := pointsText(s, ln); got != "1=-0.25" {
		t.Errorf("new lane starts at the line's pan: %s", got)
	}
	// Points go in in time order; one at the same place takes the value.
	at := func(p string) score.Pos {
		pos, err := s.ParsePos(p)
		if err != nil {
			t.Fatal(err)
		}
		return pos
	}
	for _, c := range []struct {
		at string
		v  float32
	}{{"3", 0.5}, {"2:3", -1}, {"3", 0.75}, {"4", 9}} {
		if _, err := AddLanePoint(s, 0, "pan", at(c.at), c.v); err != nil {
			t.Fatal(err)
		}
	}
	if got := pointsText(s, ln); got != "1=-0.25 2:3=-1 3=0.75 4=1" {
		t.Errorf("points (clamped to the pan's range): %s", got)
	}
	// A move stops at the neighbours.
	if err := MoveLanePoint(s, 0, "pan", 1, at("3:2"), 0); err != nil {
		t.Fatal(err)
	}
	if got := pointsText(s, ln); got != "1=-0.25 3=0 3=0.75 4=1" {
		t.Errorf("moved past its neighbour: %s", got)
	}
	if err := MoveLanePoint(s, 0, "pan", 1, at("2:2"), 0.1); err != nil {
		t.Fatal(err)
	}
	if err := SetLaneShape(s, 0, "pan", 1, score.LaneCurve, 0.5); err != nil {
		t.Fatal(err)
	}
	if err := SetLaneShape(s, 0, "pan", 2, score.LaneStep, 0); err != nil {
		t.Fatal(err)
	}
	if got := pointsText(s, ln); got != "1=-0.25 2:2=0.1/curve0.5 3=0.75/step 4=1" {
		t.Errorf("shapes: %s", got)
	}
	// The value the lane plays: held before the first point, a step holds.
	starts := s.BarStarts()
	if v := s.LaneValue(ln, score.F(5, 2), starts); v != 0.75 {
		t.Errorf("in the step: %v", v)
	}
	if v := s.LaneValue(ln, score.Frac{}, starts); v != -0.25 {
		t.Errorf("at the top: %v", v)
	}
	if v := s.LaneValue(ln, score.F(3, 2), starts); v >= 0.1+0.65/2 || v <= 0.1 {
		t.Errorf("a bent segment starts slowly: %v at its middle", v)
	}
	if err := RemoveLanePoint(s, 0, "pan", 3); err != nil {
		t.Fatal(err)
	}
	if len(ln.Points) != 3 {
		t.Errorf("%d points", len(ln.Points))
	}
	for i := 0; i < 2; i++ {
		RemoveLanePoint(s, 0, "pan", 0)
	}
	if err := RemoveLanePoint(s, 0, "pan", 0); err == nil {
		t.Error("removed a lane's last point")
	}
	if err := RemoveLane(s, 0, "pan"); err != nil || l.Lanes != nil {
		t.Errorf("remove lane: %v %+v", err, l.Lanes)
	}
	for _, is := range s.Validate(nil) {
		if is.Severity == score.Error {
			t.Error(is)
		}
	}
}

// A drawn stroke replaces the points it covers, thinned, and the curve
// after it carries on as it was.
func TestDrawLane(t *testing.T) {
	s := one(t, "4/4", 0, "| wC5 | wD5 | wE5 | wF5 |")
	AddLane(s, 0, "volume")
	AddLanePoint(s, 0, "volume", score.Pos{Bar: 4}, 0.5)
	ln := s.Lines[0].Lane("volume")
	// A straight ramp from bar 2 to bar 3, drawn as 9 points, then flat.
	var stroke []score.LanePoint
	for k := 0; k <= 8; k++ {
		stroke = append(stroke, score.LanePoint{At: score.Pos{Bar: 2, Offset: score.F(int64(k), 8)}, Value: 1 + float32(k)/8})
	}
	if err := DrawLane(s, 0, "volume", score.Pos{Bar: 2}, score.Pos{Bar: 3}, stroke, 0.01); err != nil {
		t.Fatal(err)
	}
	if got := pointsText(s, ln); got != "1=1 2=1 3=2 4=0.5" {
		t.Errorf("drawn: %s", got)
	}
	// A stroke over the middle of a segment keeps what plays after it.
	before := s.LaneValue(ln, score.F(13, 4), s.BarStarts())
	stroke = []score.LanePoint{{At: score.Pos{Bar: 3, Offset: score.F(1, 8)}, Value: 0.2}, {At: score.Pos{Bar: 3, Offset: score.F(1, 4)}, Value: 0.2}}
	if err := DrawLane(s, 0, "volume", stroke[0].At, stroke[1].At, stroke, 0.01); err != nil {
		t.Fatal(err)
	}
	if after := s.LaneValue(ln, score.F(13, 4), s.BarStarts()); after != before {
		t.Errorf("after the stroke: %v, was %v (%s)", after, before, pointsText(s, ln))
	}
}

// An effect's switch only steps; a lane on the line's second EQ is named
// for it.
func TestFXLanes(t *testing.T) {
	s := one(t, "4/4", 0, "| wC5 |")
	l := s.Lines[0]
	l.Inserts = []score.FX{score.NewFX(fx.KindEQ, fx.Insert), score.NewFX(fx.KindFilter, fx.Insert), score.NewFX(fx.KindEQ, fx.Insert)}
	tgt, ok := l.LaneTargetForSlot(2, fx.KindEQ.Spec().Param("low"))
	if !ok {
		t.Fatal("no target")
	}
	if err := AddLane(s, 0, tgt.Name()); err != nil {
		t.Fatal(err)
	}
	if l.Lanes[0].Name != "eq#2.low" || tgt.Label() != "EQ 2 · Low shelf" {
		t.Errorf("lane %q label %q", l.Lanes[0].Name, tgt.Label())
	}
	mode, _ := l.LaneTargetForSlot(1, fx.KindFilter.Spec().Param("mode"))
	if err := AddLane(s, 0, mode.Name()); err != nil {
		t.Fatal(err)
	}
	if p := l.Lane("filter.mode").Points[0]; p.Shape != score.LaneStep {
		t.Errorf("a switch's lane starts %v", p.Shape)
	}
	if err := SetLaneShape(s, 0, "filter.mode", 0, score.LaneLinear, 0); err == nil {
		t.Error("a switch's lane made straight")
	}
	if i, _ := AddLanePoint(s, 0, "filter.mode", score.Pos{Bar: 1, Offset: score.F(1, 2)}, 1.4); l.Lane("filter.mode").Points[i].Value != 1 {
		t.Errorf("a switch's value %v", l.Lane("filter.mode").Points[i].Value)
	}
}

// The buses and the master take lanes by the same calls, owner first, and
// their lanes follow their effects through chain edits.
func TestMixOwnerLanes(t *testing.T) {
	s := one(t, "4/4", 0, "| wC5 | wD5 |")
	s.Buses.A = score.Bus{Return: 0.4, Pan: -0.5, FX: []score.FX{score.NewFX(fx.KindReverb, fx.Bus)}}
	s.Master = []score.FX{score.NewFX(fx.KindEQ, fx.Insert)}
	for _, c := range []struct {
		owner int
		name  string
		want  float32
	}{{score.OwnerBusA, "return", 0.4}, {score.OwnerBusA, "pan", -0.5}, {score.OwnerBusA, "reverb.mix", 1}, {score.OwnerMaster, "volume", 1}, {score.OwnerMaster, "eq.low", 0}} {
		if err := AddLane(s, c.owner, c.name); err != nil {
			t.Fatalf("%s %s: %v", s.OwnerName(c.owner), c.name, err)
		}
		if ln := s.LaneOf(c.owner, c.name); ln == nil || ln.Points[0].Value != c.want {
			t.Errorf("%s %s starts %+v, want %g", s.OwnerName(c.owner), c.name, ln, c.want)
		}
	}
	for _, bad := range []struct {
		owner int
		name  string
	}{{score.OwnerBusA, "volume"}, {score.OwnerBusB, "reverb.size"}, {score.OwnerMaster, "pan"}, {score.OwnerMaster, "volume"}, {-7, "volume"}} {
		if err := AddLane(s, bad.owner, bad.name); err == nil {
			t.Errorf("%d %s was added", bad.owner, bad.name)
		}
	}
	if _, err := AddLanePoint(s, score.OwnerMaster, "volume", score.Pos{Bar: 2}, 0); err != nil {
		t.Fatal(err)
	}
	if err := RemoveLane(s, score.OwnerBusA, "pan"); err != nil || s.LaneOf(score.OwnerBusA, "pan") != nil {
		t.Errorf("remove a bus lane: %v", err)
	}
	// A second reverb in front of bus A's: its lane becomes reverb#2's.
	if _, err := AddOwnedEffect(s, score.OwnerBusA, 0, score.NewFX(fx.KindReverb, fx.Bus)); err != nil {
		t.Fatal(err)
	}
	if s.LaneOf(score.OwnerBusA, "reverb#2.mix") == nil || s.LaneOf(score.OwnerBusA, "reverb.mix") != nil {
		t.Errorf("bus a's lanes after a reverb in front: %+v", s.Buses.A.Lanes)
	}
	if err := MoveOwnedEffect(s, score.OwnerBusA, 1, 0); err != nil || s.LaneOf(score.OwnerBusA, "reverb.mix") == nil {
		t.Errorf("after moving it back: %v %+v", err, s.Buses.A.Lanes)
	}
	if gone, err := RemoveOwnedEffect(s, score.OwnerMaster, 0); err != nil || gone != 1 || s.LaneOf(score.OwnerMaster, "volume") == nil {
		t.Errorf("removing the master EQ: %d lanes went (%v), left %+v", gone, err, s.MasterLanes)
	}
	if !OwnerHasEffectLanes(s, score.OwnerBusA) || OwnerHasEffectLanes(s, score.OwnerMaster) {
		t.Error("OwnerHasEffectLanes")
	}
	for _, is := range s.Validate(nil) {
		if is.Severity == score.Error {
			t.Errorf("invalid: %v", is)
		}
	}
}

// Random point edits and freehand strokes on every kind of lane keep a
// lane valid: its points in time order inside the score, each value in
// its target's range (whole for a switch), never empty.
func TestRandomLaneEdits(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	s := one(t, "3+2+2/8", 0, "| q.C5 qD5 qE5 | q.C5 qD5 qE5 | q.C5 qD5 qE5 |")
	s.Lines[0].Inserts = []score.FX{score.NewFX(fx.KindFilter, fx.Insert), score.NewFX(fx.KindDelay, fx.Insert)}
	s.Master = []score.FX{score.NewFX(fx.KindLimiter, fx.Insert)}
	lanes := []struct {
		owner int
		name  string
	}{{0, "volume"}, {0, "cutoff"}, {0, "filter.mode"}, {0, "delay.beats"}, {0, "pan"}, {score.OwnerMaster, "volume"}, {score.OwnerMaster, "limiter.ceiling"}, {score.OwnerBusB, "return"}}
	for _, l := range lanes {
		if err := AddLane(s, l.owner, l.name); err != nil {
			t.Fatal(err)
		}
	}
	starts := s.BarStarts()
	end := starts[len(starts)-1]
	pos := func() score.Pos {
		return s.PosAt(score.F(int64(r.Intn(int(end.Mul(score.F(64, 1)).Floor())+1)), 64), starts)
	}
	for step := 0; step < 3000; step++ {
		l := lanes[r.Intn(len(lanes))]
		ln := s.LaneOf(l.owner, l.name)
		tg, _ := score.ParseLaneTargetIn(score.OwnerScope(l.owner), l.name)
		lo, hi := tg.Range()
		val := func() float32 { return lo - 1 + float32(r.Float64())*(hi-lo+2) }
		switch r.Intn(5) {
		case 0:
			AddLanePoint(s, l.owner, l.name, pos(), val())
		case 1:
			MoveLanePoint(s, l.owner, l.name, r.Intn(len(ln.Points)), pos(), val())
		case 2:
			RemoveLanePoint(s, l.owner, l.name, r.Intn(len(ln.Points)))
		case 3:
			SetLaneShape(s, l.owner, l.name, r.Intn(len(ln.Points)), score.LaneShape(r.Intn(3)), float32(r.Float64()*2-1))
		default:
			var stroke []score.LanePoint
			for k := 0; k < 2+r.Intn(30); k++ {
				stroke = append(stroke, score.LanePoint{At: pos(), Value: val()})
			}
			DrawLane(s, l.owner, l.name, stroke[0].At, stroke[len(stroke)-1].At, stroke, 0.004)
		}
		ln = s.LaneOf(l.owner, l.name)
		if len(ln.Points) == 0 {
			t.Fatalf("step %d: %s lost every point", step, l.name)
		}
		for i, p := range ln.Points {
			if i > 0 && score.PosLess(p.At, ln.Points[i-1].At) {
				t.Fatalf("step %d: %s out of order at %d: %+v", step, l.name, i, ln.Points)
			}
			if p.Value < lo || p.Value > hi || tg.Stepped() && (p.Value != float32(math.Round(float64(p.Value))) || p.Shape != score.LaneStep) {
				t.Fatalf("step %d: %s point %+v (range %g..%g)", step, l.name, p, lo, hi)
			}
			if p.At.Bar < 1 || p.At.Bar > s.Measures()+1 {
				t.Fatalf("step %d: %s point outside the score: %+v", step, l.name, p)
			}
		}
	}
	for _, is := range s.Validate(nil) {
		if is.Severity == score.Error {
			t.Errorf("invalid after random edits: %v", is)
		}
	}
}
