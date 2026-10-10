package world

// waypoint_test.go — a dynamic brush travelling a waypoint animation.
//
// What is asserted is what an author would see and a script would hear: where
// the brush is after so many seconds, that every channel a point sets arrives
// at the same moment, and which arrivals were announced, in what order. The
// timing is checked at exact point times on purpose — a step that lands
// precisely on a point is where an off-by-one in the announcing would show.

import (
	"math"
	"reflect"
	"testing"

	"foundry/internal/hooks"

	"github.com/go-gl/mathgl/mgl32"
)

// waypointTestLevel is one unmoved chunk holding testBoxBrush — centred at
// {0,1,0}, marked dynamic — and one animation called "path" through points.
func waypointTestLevel(points ...Waypoint) (*Level, *Chunk, *Brush) {
	b := testBoxBrush()
	b.Dynamic = true
	lvl := &Level{
		Chunks:             []Chunk{{ID: "c", Scale: 1, Brushes: []Brush{b}}},
		WaypointAnimations: []WaypointAnimation{{ID: "path", Points: points}},
	}
	return lvl, &lvl.Chunks[0], &lvl.Chunks[0].Brushes[0]
}

// wp is a point at pos reached t seconds after the brush is sent, with no turn
// and no scale.
func wp(pos mgl32.Vec3, t float32) Waypoint {
	w := NewWaypoint(pos)
	w.Time = t
	return w
}

// sendAlongPath starts the level's "path" animation on b, with the event
// buffer emptied first so a test sees only its own arrivals.
func sendAlongPath(lvl *Level, ch *Chunk, b *Brush) {
	hooks.TakeWaypointEvents()
	b.PlayWaypoints(ch, &lvl.WaypointAnimations[0])
}

func nearF(a, b, tol float32) bool { return float32(math.Abs(float64(a-b))) <= tol }

func TestPoseAtIsTheFractionOfTheLegsTimeGone(t *testing.T) {
	a := WaypointAnimation{Points: []Waypoint{
		wp(mgl32.Vec3{4, 0, 0}, 2), wp(mgl32.Vec3{4, 4, 0}, 3), wp(mgl32.Vec3{0, 4, 0}, 3),
	}}
	start := WaypointPose{Scale: mgl32.Vec3{1, 1, 1}}
	cases := []struct {
		name string
		t    float32
		want mgl32.Vec3
	}{
		{"the start", 0, mgl32.Vec3{0, 0, 0}},
		{"halfway along the first leg, which starts at the brush", 1, mgl32.Vec3{2, 0, 0}},
		{"on the first point", 2, mgl32.Vec3{4, 0, 0}},
		{"halfway along the second leg", 2.5, mgl32.Vec3{4, 2, 0}},
		// Two points at the same moment: the leg between them takes no time,
		// so that moment is already the far end of it.
		{"a leg of no time is passed straight through", 3, mgl32.Vec3{0, 4, 0}},
		{"past the end holds the last point", 10, mgl32.Vec3{0, 4, 0}},
		{"before the start is the start", -1, mgl32.Vec3{0, 0, 0}},
	}
	for _, c := range cases {
		if got := a.PoseAt(start, c.t).Position; !vecNear(got, c.want, 1e-5) {
			t.Errorf("%s: at %v s the pose is at %v, want %v", c.name, c.t, got, c.want)
		}
	}
}

func TestABrushArrivesAtEachPointAtItsTime(t *testing.T) {
	// Four units in two seconds, then four more in the one after.
	lvl, ch, b := waypointTestLevel(wp(mgl32.Vec3{4, 1, 0}, 2), wp(mgl32.Vec3{4, 5, 0}, 3))
	sendAlongPath(lvl, ch, b)

	steps := []struct {
		dt   float32
		want mgl32.Vec3
	}{
		{1, mgl32.Vec3{2, 1, 0}},   // halfway along the first leg
		{1, mgl32.Vec3{4, 1, 0}},   // exactly on the first point
		{0.5, mgl32.Vec3{4, 3, 0}}, // halfway along the faster second leg
		{0.5, mgl32.Vec3{4, 5, 0}}, // on the last point
	}
	for i, s := range steps {
		AdvanceBrushes(lvl, s.dt)
		if got := ch.LocalToWorld(b.Position()); !vecNear(got, s.want, 1e-4) {
			t.Fatalf("after step %d the brush is at %v, want %v", i+1, got, s.want)
		}
	}
	if b.Waypoints != nil {
		t.Error("the brush is still following the animation after reaching its last point")
	}

	want := []hooks.WaypointEvent{
		{Object: "door", Animation: "path", Index: 0},
		{Object: "door", Animation: "path", Index: 1},
		{Object: "door", Animation: "path", Index: 1, Ended: true},
	}
	if got := hooks.TakeWaypointEvents(); !reflect.DeepEqual(got, want) {
		t.Errorf("announced %+v, want %+v", got, want)
	}
}

// The user-visible promise of linear interpolation: a brush that turns and
// grows on the way to a point is half turned and half grown halfway there,
// rather than snapping to the point's pose when it arrives.
func TestRotationAndScaleEaseAcrossALegWithThePosition(t *testing.T) {
	to := wp(mgl32.Vec3{4, 1, 0}, 2)
	to.Rotation = mgl32.Vec3{0, math.Pi, 0}
	to.Scale = mgl32.Vec3{3, 1, 1}
	lvl, ch, b := waypointTestLevel(to)
	sendAlongPath(lvl, ch, b)

	AdvanceBrushes(lvl, 1)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{2, 1, 0}, 1e-4) {
		t.Errorf("halfway, the brush is at %v, want {2 1 0}", got)
	}
	if !nearF(b.Rotation.Y(), math.Pi/2, 1e-4) {
		t.Errorf("halfway, the brush has turned %v, want half of pi", b.Rotation.Y())
	}
	if !vecNear(b.EffectiveScale(), mgl32.Vec3{2, 1, 1}, 1e-4) {
		t.Errorf("halfway, the brush is scaled %v, want {2 1 1}", b.EffectiveScale())
	}

	AdvanceBrushes(lvl, 1)
	if !nearF(b.Rotation.Y(), math.Pi, 1e-4) || !vecNear(b.EffectiveScale(), mgl32.Vec3{3, 1, 1}, 1e-4) {
		t.Errorf("on arrival the brush is turned %v and scaled %v, want the point's own pose",
			b.Rotation.Y(), b.EffectiveScale())
	}
}

// The numbers are interpolated as written, not along the shortest arc — which
// is what lets a point ask for two whole turns.
func TestRotationIsInterpolatedAsWrittenRatherThanTheShortWay(t *testing.T) {
	to := wp(mgl32.Vec3{4, 1, 0}, 2)
	to.Rotation = mgl32.Vec3{0, 4 * math.Pi, 0}
	lvl, ch, b := waypointTestLevel(to)
	sendAlongPath(lvl, ch, b)

	AdvanceBrushes(lvl, 1)
	if !nearF(b.Rotation.Y(), 2*math.Pi, 1e-3) {
		t.Errorf("halfway through a 720° leg the brush has turned %v rad, want a full turn — "+
			"the angle was wrapped or shortened", b.Rotation.Y())
	}
}

// A tick long enough to pass several points announces every arrival, and lands
// the brush exactly on the last point.
func TestALongTickCarriesTheBrushAcrossEveryLeg(t *testing.T) {
	lvl, ch, b := waypointTestLevel(
		wp(mgl32.Vec3{4, 1, 0}, 2), wp(mgl32.Vec3{4, 5, 0}, 3), wp(mgl32.Vec3{0, 5, 0}, 7))
	sendAlongPath(lvl, ch, b)

	AdvanceBrushes(lvl, 100)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{0, 5, 0}, 1e-5) {
		t.Errorf("after a long tick the brush is at %v, want the last point", got)
	}
	if b.Waypoints != nil {
		t.Error("the brush is still following after a tick longer than the whole animation")
	}
	if got := len(hooks.TakeWaypointEvents()); got != 4 {
		t.Errorf("%d events for three arrivals and an end, want 4", got)
	}
}

// A length past the last point holds the brush on it, and the end is announced
// when the length runs out rather than on arrival.
func TestALengthPastTheLastPointHoldsTheBrushThere(t *testing.T) {
	lvl, ch, b := waypointTestLevel(wp(mgl32.Vec3{4, 1, 0}, 1))
	lvl.WaypointAnimations[0].Length = 3
	sendAlongPath(lvl, ch, b)

	AdvanceBrushes(lvl, 2)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{4, 1, 0}, 1e-5) {
		t.Fatalf("a second after arriving the brush is at %v, want it held on the point", got)
	}
	if b.Waypoints == nil {
		t.Fatal("the brush stopped following on arrival, before the length ran out")
	}
	if got := hooks.TakeWaypointEvents(); len(got) != 1 || got[0].Ended {
		t.Errorf("before the length ran out the brush announced %+v, want the arrival alone", got)
	}

	AdvanceBrushes(lvl, 1)
	if b.Waypoints != nil {
		t.Error("the brush is still following after the length ran out")
	}
	if got := hooks.TakeWaypointEvents(); len(got) != 1 || !got[0].Ended {
		t.Errorf("when the length ran out the brush announced %+v, want the end", got)
	}
}

func TestAFirstPointAtTimeZeroStartsThePathOnIt(t *testing.T) {
	lvl, ch, b := waypointTestLevel(wp(mgl32.Vec3{50, 1, 0}, 0))
	lvl.WaypointAnimations[0].Length = 1
	sendAlongPath(lvl, ch, b)

	AdvanceBrushes(lvl, 0.001)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{50, 1, 0}, 1e-5) {
		t.Errorf("a first point at 0 s left the brush at %v, want it on {50 1 0}", got)
	}
}

// A brush following waypoints is driven by them alone, and its velocity picks
// up again from wherever the path leaves it.
func TestAWaypointAnimationHoldsABrushsRatesUntilItEnds(t *testing.T) {
	lvl, ch, b := waypointTestLevel(wp(mgl32.Vec3{4, 1, 0}, 2))
	b.Velocity = mgl32.Vec3{0, 0, 100}
	sendAlongPath(lvl, ch, b)

	AdvanceBrushes(lvl, 1)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{2, 1, 0}, 1e-4) {
		t.Fatalf("while following, the brush is at %v — its velocity was integrated as well", got)
	}
	AdvanceBrushes(lvl, 1)
	AdvanceBrushes(lvl, 0.5)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{4, 1, 50}, 1e-3) {
		t.Errorf("after the path ended the brush is at %v, want its velocity to carry it from the last point", got)
	}
}

func TestNothingIsAnnouncedWhileTheEditorIsUp(t *testing.T) {
	lvl, ch, b := waypointTestLevel(wp(mgl32.Vec3{4, 1, 0}, 2))
	prev := hooks.EditorActive
	hooks.EditorActive = func() bool { return true }
	t.Cleanup(func() { hooks.EditorActive = prev })

	sendAlongPath(lvl, ch, b)
	AdvanceBrushes(lvl, 10)
	if b.Waypoints != nil {
		t.Fatal("the path did not run to the end")
	}
	if got := hooks.TakeWaypointEvents(); len(got) != 0 {
		t.Errorf("a path travelled with the editor up queued %d script events for the next play session", len(got))
	}
}

// The editor's timeline scrubs a preview with SeekWaypoints: back and forth,
// posing the brush where the tick would have it, and announcing nothing.
func TestSeekingPutsTheBrushWhereThatTimeHasIt(t *testing.T) {
	lvl, ch, b := waypointTestLevel(wp(mgl32.Vec3{4, 1, 0}, 2), wp(mgl32.Vec3{4, 5, 0}, 3))
	sendAlongPath(lvl, ch, b)
	anim := &lvl.WaypointAnimations[0]

	b.SeekWaypoints(ch, anim, 2.5)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{4, 3, 0}, 1e-4) {
		t.Errorf("seeking to 2.5 s put the brush at %v, want {4 3 0}", got)
	}
	if b.Waypoints.Next != 1 {
		t.Errorf("seeking past the first point left %d points counted as reached, want 1", b.Waypoints.Next)
	}

	b.SeekWaypoints(ch, anim, 1)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{2, 1, 0}, 1e-4) {
		t.Errorf("seeking back to 1 s put the brush at %v, want {2 1 0}", got)
	}
	if got := hooks.TakeWaypointEvents(); len(got) != 0 {
		t.Errorf("seeking announced %+v; a seek is not an arrival", got)
	}

	// The tick carries on from the moment sought.
	AdvanceBrushes(lvl, 1)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{4, 1, 0}, 1e-4) {
		t.Errorf("a second on from 1 s the brush is at %v, want the first point", got)
	}
	hooks.TakeWaypointEvents()
}

func TestADeletedAnimationLeavesTheBrushWhereItGotTo(t *testing.T) {
	lvl, ch, b := waypointTestLevel(wp(mgl32.Vec3{4, 1, 0}, 2))
	sendAlongPath(lvl, ch, b)
	AdvanceBrushes(lvl, 1)

	lvl.WaypointAnimations = nil
	AdvanceBrushes(lvl, 1)
	if b.Waypoints != nil {
		t.Error("the brush still believes it is following an animation that no longer exists")
	}
	if got := b.Position(); !vecNear(got, mgl32.Vec3{2, 1, 0}, 1e-4) {
		t.Errorf("the brush moved to %v after its animation went away; it should stay put", got)
	}
}

// Positions are world space: a brush in a moved chunk still meets the arrow
// where the arrow is.
func TestWaypointPositionsAreInWorldSpace(t *testing.T) {
	lvl, ch, b := waypointTestLevel(wp(mgl32.Vec3{14, 1, 0}, 1))
	ch.Origin = mgl32.Vec3{10, 0, 0}
	sendAlongPath(lvl, ch, b)

	AdvanceBrushes(lvl, 1)
	if got := ch.LocalToWorld(b.Position()); !vecNear(got, mgl32.Vec3{14, 1, 0}, 1e-4) {
		t.Errorf("the brush's centre is at %v in the world, want the point at {14 1 0}", got)
	}
	if got := b.Position(); !vecNear(got, mgl32.Vec3{4, 1, 0}, 1e-4) {
		t.Errorf("the brush's local centre is %v, want {4 1 0} — the chunk origin was not divided out", got)
	}
}

// Sending a brush along a new animation mid-journey sets off from where it
// has got to, not from where it was authored.
func TestSendingABrushAgainStartsFromWhereItIs(t *testing.T) {
	lvl, ch, b := waypointTestLevel(wp(mgl32.Vec3{4, 1, 0}, 2))
	lvl.WaypointAnimations = append(lvl.WaypointAnimations,
		WaypointAnimation{ID: "back", Points: []Waypoint{wp(mgl32.Vec3{0, 1, 0}, 1)}})
	sendAlongPath(lvl, ch, b)
	AdvanceBrushes(lvl, 1)

	b.PlayWaypoints(ch, FindWaypointAnimation(lvl, "back"))
	AdvanceBrushes(lvl, 0.5)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{1, 1, 0}, 1e-4) {
		t.Errorf("the brush is at %v, want a unit back from where it was turned round", got)
	}
}

// ---------- timing edits ----------

func TestChangingTheLengthStretchesEveryPointsTime(t *testing.T) {
	a := WaypointAnimation{Length: 4, Points: []Waypoint{wp(mgl32.Vec3{}, 1), wp(mgl32.Vec3{}, 2)}}
	a.SetLength(2)
	if a.Length != 2 || !nearF(a.Points[0].Time, 0.5, 1e-6) || !nearF(a.Points[1].Time, 1, 1e-6) {
		t.Errorf("halving the length left the points at %v and %v over %v s, want 0.5 and 1 over 2",
			a.Points[0].Time, a.Points[1].Time, a.Length)
	}

	// An animation that took no time has no rhythm to keep, so its points are
	// spread across the new length instead.
	still := WaypointAnimation{Points: []Waypoint{wp(mgl32.Vec3{}, 0), wp(mgl32.Vec3{}, 0), wp(mgl32.Vec3{}, 0)}}
	still.SetLength(3)
	for i, want := range []float32{0, 1.5, 3} {
		if !nearF(still.Points[i].Time, want, 1e-6) {
			t.Errorf("stretching a timeless animation put point %d at %v s, want %v", i, still.Points[i].Time, want)
		}
	}
}

func TestAPointsTimeIsHeldBetweenItsNeighbours(t *testing.T) {
	a := WaypointAnimation{Length: 3, Points: []Waypoint{
		wp(mgl32.Vec3{}, 1), wp(mgl32.Vec3{}, 2), wp(mgl32.Vec3{}, 3),
	}}
	if got := a.SetPointTime(1, 5); got != 3 {
		t.Errorf("moving the middle point past the next one gave it %v s, want 3", got)
	}
	if got := a.SetPointTime(1, -1); got != 1 {
		t.Errorf("moving the middle point before the first gave it %v s, want 1", got)
	}
	// The last point has nothing after it: moving it past the end makes the
	// animation longer.
	if got := a.SetPointTime(2, 10); got != 10 || a.EffectiveLength() != 10 {
		t.Errorf("moving the last point to 10 s gave it %v over %v s, want 10 over 10", got, a.EffectiveLength())
	}
}

// ---------- serialization ----------

func TestAWaypointAnimationRoundTripsAndOmitsItsDefaults(t *testing.T) {
	want := WaypointAnimation{ID: "lift", Length: 5, Points: []Waypoint{
		NewWaypoint(mgl32.Vec3{4, 5, 6}),
		{Position: mgl32.Vec3{1, 2, 3}, Rotation: mgl32.Vec3{0, 1.5, 0}, Scale: mgl32.Vec3{2, 1, 0.5}, Time: 2.5},
	}}
	raw := WaypointAnimationToRaw(&want)

	if raw.Points[0].Scale != nil || raw.Points[0].Time != 0 {
		t.Errorf("a point at the defaults wrote scale %v and time %v", raw.Points[0].Scale, raw.Points[0].Time)
	}
	// A length past the last point is a hold the author set, not a derived
	// number, so it is written.
	if raw.Length != 5 || raw.Points[1].Time != 2.5 {
		t.Errorf("wrote length %v and time %v, want 5 and 2.5", raw.Length, raw.Points[1].Time)
	}

	if got := WaypointAnimationFromRaw(raw); !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the animation:\n got %+v\nwant %+v", got, want)
	}
}

// A level file is text anyone can edit: times written out of order are put in
// order on load, rather than sending a brush backwards in time.
func TestLoadingPutsWaypointTimesInOrder(t *testing.T) {
	a := WaypointAnimationFromRaw(RawWaypointAnimation{ID: "p", Points: []RawWaypoint{
		{Time: 2}, {Time: 1}, {Time: -3},
	}})
	for i := range a.Points {
		if a.Points[i].Time != 2 {
			t.Errorf("point %d loaded at %v s, want 2 — no earlier than the point before it", i, a.Points[i].Time)
		}
	}
	if a.Length != 2 {
		t.Errorf("the length loaded as %v, want it to reach the last point at 2", a.Length)
	}
}
