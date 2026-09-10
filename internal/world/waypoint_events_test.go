package world

// waypoint_events_test.go — the atoms a waypoint posts when a brush reaches it.
//
// The posting is the script package's; what is asserted here is the world's
// half: that each arrival carries the reached point's own events, that the end
// carries none, and that an event list survives the level file and a clone
// without being shared or misspelt on the way.

import (
	"reflect"
	"testing"

	"foundry/internal/hooks"

	"github.com/go-gl/mathgl/mgl32"
)

func TestAnArrivalCarriesTheReachedPointsEvents(t *testing.T) {
	first := NewWaypoint(mgl32.Vec3{4, 1, 0})
	first.Events = []string{"halfway"}
	last := NewWaypoint(mgl32.Vec3{4, 5, 0})
	last.Events = []string{"lift_top", "chime"}
	lvl, ch, b := waypointTestLevel(first, last)
	sendAlongPath(lvl, ch, b)

	// One tick longer than any path this could be, so the test is about which
	// point announced what rather than when.
	AdvanceBrushes(lvl, 100)

	got := hooks.TakeWaypointEvents()
	if len(got) != 3 {
		t.Fatalf("announced %+v, want two arrivals and an end", got)
	}
	if !reflect.DeepEqual(got[0].Events, []string{"halfway"}) {
		t.Errorf("the first arrival carried %v, want the first point's [halfway]", got[0].Events)
	}
	if !reflect.DeepEqual(got[1].Events, []string{"lift_top", "chime"}) {
		t.Errorf("the last arrival carried %v, want the last point's events in order", got[1].Events)
	}
	if !got[2].Ended || len(got[2].Events) != 0 {
		t.Errorf("the end was %+v; it should carry no events, the last point's went out with its arrival", got[2])
	}
}

func TestNormalizeWaypointEventAtom(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"door_open", "door_open", true},
		{":door_open:", "door_open", true},
		{"  Door_Open ", "door_open", true},
		{"", "", false},
		{"::", "", false},
		{"two words", "", false},
		{"lift2", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeWaypointEventAtom(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("NormalizeWaypointEventAtom(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestWaypointEventsSurviveRawAndAreTidiedOnTheWayIn(t *testing.T) {
	a := WaypointAnimation{ID: "lift", Points: []Waypoint{
		NewWaypoint(mgl32.Vec3{0, 1, 0}),
		NewWaypoint(mgl32.Vec3{0, 6, 0}),
	}}
	a.Points[1].Events = []string{"lift_top", "chime"}

	raw := WaypointAnimationToRaw(&a)
	if raw.Points[0].Events != nil {
		t.Errorf("a point with no events wrote %v; it should write nothing", raw.Points[0].Events)
	}
	back := WaypointAnimationFromRaw(raw)
	if back.Points[0].Events != nil || !reflect.DeepEqual(back.Points[1].Events, a.Points[1].Events) {
		t.Errorf("events changed across raw: got %v and %v", back.Points[0].Events, back.Points[1].Events)
	}

	// A hand-edited file: colons and capitals are the same event, a repeat is
	// one, and a name no handler could match is dropped rather than kept.
	raw.Points[1].Events = []string{":Door_Open:", "door_open", "bad name", "x1"}
	if got := WaypointAnimationFromRaw(raw).Points[1].Events; !reflect.DeepEqual(got, []string{"door_open"}) {
		t.Errorf("hand-written events read back as %v, want [door_open]", got)
	}
}

func TestACloneDoesNotShareEventLists(t *testing.T) {
	a := WaypointAnimation{ID: "lift", Points: []Waypoint{NewWaypoint(mgl32.Vec3{})}}
	a.Points[0].Events = []string{"lift_top"}

	cp := a.Clone()
	cp.Points[0].Events[0] = "changed"
	if a.Points[0].Events[0] != "lift_top" {
		t.Error("editing a clone's event renamed the original's")
	}
}

func TestWaypointEventAtomsListsEachOnceInOrder(t *testing.T) {
	p1, p2, p3 := NewWaypoint(mgl32.Vec3{}), NewWaypoint(mgl32.Vec3{}), NewWaypoint(mgl32.Vec3{})
	p1.Events = []string{"open", "chime"}
	p3.Events = []string{"chime"}
	lvl := &Level{WaypointAnimations: []WaypointAnimation{
		{ID: "a", Points: []Waypoint{p1, p2}},
		{ID: "b", Points: []Waypoint{p3}},
	}}
	if got := WaypointEventAtoms(lvl); !reflect.DeepEqual(got, []string{"chime", "open"}) {
		t.Errorf("WaypointEventAtoms = %v, want [chime open]", got)
	}
	if got := WaypointEventAtoms(nil); got != nil {
		t.Errorf("a nil level listed %v", got)
	}
}
