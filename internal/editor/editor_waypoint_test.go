package editor

// editor_waypoint_test.go — authoring waypoint animations, headless.
//
// Placement, deletion and selection are checked the way the sound editor's
// are: through the level they leave behind, since a wrong result looks fine
// in a screenshot until a script is run against it. The preview is checked for
// the one promise that matters most — that stopping it puts the brush back,
// because a brush left displaced in the editor is a level that plays
// differently from how it looks.

import (
	"math"
	"testing"

	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// waypointEditorLevel installs brushTestLevel's two boxes — "a" centred at
// {1,1,1}, "b" at {3,1,1} — with nothing waypoint-related selected or armed.
func waypointEditorLevel(t *testing.T) *world.Level {
	t.Helper()
	t.Cleanup(saveEditorState())
	t.Cleanup(EditorClearUndoHistory)
	t.Cleanup(func() { hooks.TakeWaypointEvents() })
	brushTestLevel(t)
	EditorClearUndoHistory()
	Editor.ActiveWaypointAnimIdx, Editor.ActiveWaypointIdx = -1, -1
	Editor.WaypointPlacementMode = false
	Editor.WaypointPreview = waypointPreview{}
	Editor.GizmoKind = gizmoKindNone
	Editor.NewWaypointAnimID = ""
	return Editor.level
}

// Placement is one click: one point, selected, and the mode put away. A second
// placement is a second path.
func TestPlacingAWaypointPlacesOneAndPutsTheModeAway(t *testing.T) {
	lvl := waypointEditorLevel(t)

	Editor.WaypointPlacementMode = true
	placeWaypointAt(mgl32.Vec3{0, 1, 0})
	if len(lvl.WaypointAnimations) != 1 || len(lvl.WaypointAnimations[0].Points) != 1 {
		t.Fatalf("placing made %+v; want one animation with one point", lvl.WaypointAnimations)
	}
	if Editor.WaypointPlacementMode {
		t.Error("placement stayed armed after its click")
	}
	if activeWaypoint() == nil {
		t.Error("the placed point is not selected, so a Ctrl+click would have nothing to add after")
	}

	Editor.WaypointPlacementMode = true
	placeWaypointAt(mgl32.Vec3{4, 1, 0})
	if len(lvl.WaypointAnimations) != 2 {
		t.Errorf("a second placement made %d animations; it should start a second path", len(lvl.WaypointAnimations))
	}
}

// What Ctrl+click does with a waypoint selected: the next point goes straight
// after the selected one.
func TestCtrlClickAddsThePointAfterTheSelectedOne(t *testing.T) {
	lvl := waypointEditorLevel(t)
	if insertWaypointAt(mgl32.Vec3{9, 9, 9}) {
		t.Fatal("with no waypoint selected, Ctrl+click added a point to nothing")
	}

	placeWaypointAt(mgl32.Vec3{0, 1, 0})
	a := &lvl.WaypointAnimations[0]
	a.Points[0].Rotation = mgl32.Vec3{0, 1, 0}

	insertWaypointAt(mgl32.Vec3{4, 1, 0})
	insertWaypointAt(mgl32.Vec3{8, 1, 0})
	if len(lvl.WaypointAnimations) != 1 {
		t.Fatalf("Ctrl+clicks started %d animations; they should add to the selected one", len(lvl.WaypointAnimations))
	}
	if len(a.Points) != 3 || a.Points[2].Position != (mgl32.Vec3{8, 1, 0}) {
		t.Fatalf("points are %+v, want three in click order", a.Points)
	}
	if Editor.ActiveWaypointIdx != 2 {
		t.Errorf("the newest point is not the selection (index %d)", Editor.ActiveWaypointIdx)
	}
	// A path is mostly a route: the turn of the point before carries on.
	if a.Points[1].Rotation != (mgl32.Vec3{0, 1, 0}) {
		t.Errorf("a new point did not carry on the previous one's turn: %+v", a.Points[1])
	}
	// Each point on the end is a new leg after the last, timed at a walk: four
	// units at two a second is two seconds a leg.
	if a.Points[1].Time != 2 || a.Points[2].Time != 4 || a.EffectiveLength() != 4 {
		t.Errorf("points on the end were timed %v and %v over %v s, want 2 and 4 over 4",
			a.Points[1].Time, a.Points[2].Time, a.EffectiveLength())
	}

	// With an earlier point selected, the next click goes straight after it.
	editorSelectWaypoint(0, 0)
	insertWaypointAt(mgl32.Vec3{2, 1, 0})
	if len(a.Points) != 4 || a.Points[1].Position != (mgl32.Vec3{2, 1, 0}) {
		t.Fatalf("a Ctrl+click with point 1 selected did not insert after it: %+v", a.Points)
	}
	// Between two points it takes the moment halfway, and no other point moves.
	if a.Points[1].Time != 1 || a.Points[2].Time != 2 || a.Points[3].Time != 4 {
		t.Errorf("inserting between points timed the path %v, %v, %v; want 1, 2, 4",
			a.Points[1].Time, a.Points[2].Time, a.Points[3].Time)
	}
}

// Deleting one point joins the one before it straight to the one after, and
// leaves the one before selected so a Ctrl+click fills the gap again.
func TestDeletingAWaypointLinksItsNeighbours(t *testing.T) {
	lvl := waypointEditorLevel(t)
	placeWaypointAt(mgl32.Vec3{0, 1, 0})
	insertWaypointAt(mgl32.Vec3{4, 1, 0})
	insertWaypointAt(mgl32.Vec3{8, 1, 0})
	a := &lvl.WaypointAnimations[0]

	editorSelectWaypoint(0, 1)
	deleteActiveWaypoint()
	if len(a.Points) != 2 || a.Points[0].Position != (mgl32.Vec3{0, 1, 0}) || a.Points[1].Position != (mgl32.Vec3{8, 1, 0}) {
		t.Fatalf("deleting the middle point left %+v, want the first leading straight to the last", a.Points)
	}
	if Editor.ActiveWaypointIdx != 0 {
		t.Errorf("after the delete point %d is selected, want the one before the gap", Editor.ActiveWaypointIdx+1)
	}
	insertWaypointAt(mgl32.Vec3{5, 1, 0})
	if len(a.Points) != 3 || a.Points[1].Position != (mgl32.Vec3{5, 1, 0}) {
		t.Errorf("a Ctrl+click after the delete did not fill the gap: %+v", a.Points)
	}
}

func TestDeletingAnAnimationsLastPointDeletesTheAnimation(t *testing.T) {
	lvl := waypointEditorLevel(t)
	placeWaypointAt(mgl32.Vec3{0, 1, 0})
	insertWaypointAt(mgl32.Vec3{4, 1, 0})

	deleteActiveWaypoint()
	if len(lvl.WaypointAnimations) != 1 || len(lvl.WaypointAnimations[0].Points) != 1 {
		t.Fatalf("deleting one of two points left %+v", lvl.WaypointAnimations)
	}
	if activeWaypoint() == nil {
		t.Error("after deleting a point, the one left is not selected")
	}

	deleteActiveWaypoint()
	if len(lvl.WaypointAnimations) != 0 {
		t.Errorf("deleting the last point left an empty animation behind: %+v", lvl.WaypointAnimations)
	}
	if activeWaypointAnimation() != nil || Editor.GizmoKind != gizmoKindNone {
		t.Error("the deleted animation is still selected")
	}
}

func TestSelectingAWaypointClearsOtherSelectionsAndTakesTheGizmo(t *testing.T) {
	waypointEditorLevel(t)
	Editor.SpawnSelected = true
	Editor.GizmoKind = gizmoKindSpawn

	placeWaypointAt(mgl32.Vec3{0, 1, 0})
	if Editor.SpawnSelected {
		t.Error("selecting a waypoint left the spawn point selected as well")
	}
	if Editor.GizmoKind != gizmoKindWaypoint || activeGizmoWaypoint() == nil {
		t.Errorf("the gizmo is on %q, want the new waypoint", Editor.GizmoKind)
	}

	// The animation selected whole gives the move gizmo the path, and no
	// single point for rotate or scale to act on.
	editorSelectWaypointAnimation(0)
	if activeGizmoWaypointAnimation() == nil || activeGizmoWaypoint() != nil {
		t.Error("selecting the animation whole did not hand the gizmo the whole path")
	}
}

// slideAnimation takes brush a from its centre at {1,1,1} ten units along +Z,
// arriving five seconds after it is sent.
func slideAnimation() world.WaypointAnimation {
	p := world.NewWaypoint(mgl32.Vec3{1, 1, 11})
	p.Time = 5
	return world.WaypointAnimation{ID: "slide", Points: []world.Waypoint{p}}
}

func TestAPreviewMovesTheBrushAndStoppingPutsItBack(t *testing.T) {
	lvl := waypointEditorLevel(t)
	b := &lvl.Chunks[0].Brushes[0]
	b.Dynamic = true
	Editor.Active = true
	lvl.WaypointAnimations = []world.WaypointAnimation{
		slideAnimation(),
	}

	if !editorWaypointPreviewStart("slide", waypointTarget{Kind: hooks.WaypointBrush, ID: "a"}) {
		t.Fatal("the preview refused to start on a dynamic brush")
	}
	editorWaypointPreviewTick(1)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{1, 1, 3}, 1e-4) {
		t.Errorf("a second into the preview the brush is at %v, want {1 1 3}", got)
	}

	editorWaypointPreviewStop()
	if !b.AtRest() || b.Waypoints != nil {
		t.Errorf("stopping the preview left the brush displaced (at %v)", b.Position())
	}
	if p := Editor.WaypointPreview; p.Playing || p.Posed || p.Time != 0 {
		t.Errorf("after Stop the preview is %+v, want it stopped at the start holding nothing", p)
	}
}

func TestAPreviewRefusesABrushThatIsNotDynamic(t *testing.T) {
	lvl := waypointEditorLevel(t)
	Editor.Active = true
	lvl.WaypointAnimations = []world.WaypointAnimation{
		slideAnimation(),
	}
	if editorWaypointPreviewStart("slide", waypointTarget{Kind: hooks.WaypointBrush, ID: "a"}) {
		t.Error("the preview played on a static brush, which a script could never move")
	}
	if b := &lvl.Chunks[0].Brushes[0]; !b.AtRest() || b.Waypoints != nil {
		t.Error("the refused preview moved the brush anyway")
	}
}

func TestALoopingPreviewGoesRoundAgainAfterHoldingTheEnd(t *testing.T) {
	lvl := waypointEditorLevel(t)
	b := &lvl.Chunks[0].Brushes[0]
	b.Dynamic = true
	Editor.Active = true
	lvl.WaypointAnimations = []world.WaypointAnimation{
		slideAnimation(),
	}
	Editor.WaypointPreview.Loop = true
	editorWaypointPreviewStart("slide", waypointTarget{Kind: hooks.WaypointBrush, ID: "a"})

	editorWaypointPreviewTick(10)
	if got := b.Position(); Editor.WaypointPreview.Time != 5 || !vecNear(got, mgl32.Vec3{1, 1, 11}, 1e-4) {
		t.Fatalf("ten seconds into a five-second path the playhead is at %v s and the brush at %v, want both at the end",
			Editor.WaypointPreview.Time, got)
	}
	editorWaypointPreviewTick(waypointPreviewLoopPause / 2)
	if Editor.WaypointPreview.Time != 5 {
		t.Error("the loop restarted before holding the end for its pause")
	}
	editorWaypointPreviewTick(waypointPreviewLoopPause)
	if p := Editor.WaypointPreview; p.Time != 0 || !p.Playing {
		t.Errorf("a looping preview did not start again after holding the end: %+v", p)
	}
	if got := b.Position(); !vecNear(got, mgl32.Vec3{1, 1, 1}, 1e-4) {
		t.Errorf("the restarted loop set off from %v, want the brush's authored centre", got)
	}
	editorWaypointPreviewStop()
}

func TestPickingFindsTheNearestWaypointMarker(t *testing.T) {
	lvl := &world.Level{WaypointAnimations: []world.WaypointAnimation{{ID: "p", Points: []world.Waypoint{
		world.NewWaypoint(mgl32.Vec3{0, 0, 0}),
		world.NewWaypoint(mgl32.Vec3{0, 0, 5}),
	}}}}
	cam := mgl32.Vec3{0, 0, -10}

	// Both markers are on the ray, and both come back — the far one is what a
	// second click cycles to. Nearest first.
	hits := pickWaypointMarkers(cam, mgl32.Vec3{0, 0, 1}, cam, lvl)
	if len(hits) != 2 {
		t.Fatalf("a ray through both markers returned %d hits, want 2", len(hits))
	}
	if hits[0].anim != 0 || hits[0].point != 0 {
		t.Fatalf("the first hit is animation %d point %d, want the nearer, 0/0", hits[0].anim, hits[0].point)
	}
	if hits[1].point != 1 {
		t.Fatalf("the second hit is point %d, want the far one, 1", hits[1].point)
	}
	if !nearlyEqual(hits[0].t, 10, 1e-4) {
		t.Errorf("the near hit is at %v, want 10", hits[0].t)
	}
	if got := pickWaypointMarkers(mgl32.Vec3{3, 0, -10}, mgl32.Vec3{0, 0, 1}, cam, lvl); len(got) != 0 {
		t.Error("a ray three units wide of every marker still picked one")
	}
}

// A waypoint's angles are interpolated as the numbers they are, so the rotate
// gizmo must not wrap them the way it wraps every other rotation.
func TestTurningAWaypointKeepsCountingPastHalfATurn(t *testing.T) {
	start := mgl32.Vec3{0, mgl32.DegToRad(170), 0}
	got := eulerAddedAbout(start, mgl32.Vec3{0, 1, 0}, mgl32.DegToRad(30))
	if !nearlyEqual(got.Y(), mgl32.DegToRad(200), 1e-4) {
		t.Errorf("170° turned by 30° became %v°, want 200°", got.Y()*180/math.Pi)
	}
}

// Undoing a Ctrl+clicked point takes it back and leaves the point before it
// selected, so the next Ctrl+click carries on the same path.
func TestUndoingAnAddedPointLeavesThePointBeforeItSelected(t *testing.T) {
	lvl := waypointEditorLevel(t)
	// Undo rebuilds the level, which resolves the boxes' textures by name.
	withSpawnMarkerTexture(t)
	placeWaypointAt(mgl32.Vec3{0, 1, 0})
	insertWaypointAt(mgl32.Vec3{4, 1, 0})

	EditorUndo()
	if len(lvl.WaypointAnimations) != 1 || len(lvl.WaypointAnimations[0].Points) != 1 {
		t.Fatalf("undo left %+v, want the second point taken back", lvl.WaypointAnimations)
	}
	if activeWaypoint() == nil || Editor.ActiveWaypointIdx != 0 {
		t.Fatal("undo dropped the path's remaining point from the selection")
	}

	insertWaypointAt(mgl32.Vec3{8, 1, 0})
	if len(lvl.WaypointAnimations) != 1 || len(lvl.WaypointAnimations[0].Points) != 2 {
		t.Errorf("the Ctrl+click after an undo made %+v, want it added to the same path", lvl.WaypointAnimations)
	}
}

// Scrubbing is the author taking the playhead in hand: it pauses the preview,
// and the brush goes where that moment has it and stays there.
func TestScrubbingThePreviewPosesTheBrushAndPauses(t *testing.T) {
	lvl := waypointEditorLevel(t)
	b := &lvl.Chunks[0].Brushes[0]
	b.Dynamic = true
	Editor.Active = true
	lvl.WaypointAnimations = []world.WaypointAnimation{slideAnimation()}
	editorWaypointPreviewStart("slide", waypointTarget{Kind: hooks.WaypointBrush, ID: "a"})

	editorWaypointPreviewSeek(2.5)
	if Editor.WaypointPreview.Playing {
		t.Error("scrubbing left the preview playing")
	}
	if got := b.Position(); !vecNear(got, mgl32.Vec3{1, 1, 6}, 1e-4) {
		t.Errorf("scrubbed to 2.5 s the brush is at %v, want halfway, {1 1 6}", got)
	}
	editorWaypointPreviewTick(1)
	if got := b.Position(); !vecNear(got, mgl32.Vec3{1, 1, 6}, 1e-4) {
		t.Errorf("a paused preview moved the brush on to %v", got)
	}
	editorWaypointPreviewStop()
}

// Key At Playhead puts a point exactly where the path already passes at that
// moment, so keying it changes nothing until the point is moved.
func TestKeyingAtThePlayheadAddsAPointOnThePath(t *testing.T) {
	lvl := waypointEditorLevel(t)
	placeWaypointAt(mgl32.Vec3{0, 1, 0})
	insertWaypointAt(mgl32.Vec3{4, 1, 0}) // reached at two seconds
	a := &lvl.WaypointAnimations[0]
	Editor.WaypointPreview.Anim, Editor.WaypointPreview.Time = a.ID, 1

	insertWaypointAtPlayhead(a)
	if len(a.Points) != 3 || Editor.ActiveWaypointIdx != 1 {
		t.Fatalf("keying at 1 s left %d points with point %d selected, want 3 with the new second one", len(a.Points), Editor.ActiveWaypointIdx+1)
	}
	if got := a.Points[1]; got.Time != 1 || !vecNear(got.Position, mgl32.Vec3{2, 1, 0}, 1e-5) {
		t.Errorf("the keyed point is at %v at %v s, want {2 1 0} at 1 s", got.Position, got.Time)
	}

	insertWaypointAtPlayhead(a)
	if len(a.Points) != 3 {
		t.Error("keying a moment that already has a point stacked a second point on it")
	}
}

// Reordering points re-routes the path, but must not make time run backwards.
func TestReorderingPointsKeepsTheTimesInOrder(t *testing.T) {
	lvl := waypointEditorLevel(t)
	placeWaypointAt(mgl32.Vec3{0, 1, 0})
	insertWaypointAt(mgl32.Vec3{4, 1, 0})
	insertWaypointAt(mgl32.Vec3{8, 1, 0})
	a := &lvl.WaypointAnimations[0]

	moveActiveWaypoint(-1)
	if a.Points[1].Position != (mgl32.Vec3{8, 1, 0}) {
		t.Fatalf("Move Earlier did not swap the last two points: %+v", a.Points)
	}
	if a.Points[0].Time != 0 || a.Points[1].Time != 2 || a.Points[2].Time != 4 {
		t.Errorf("after reordering the times are %v, %v, %v; want them left in place at 0, 2, 4",
			a.Points[0].Time, a.Points[1].Time, a.Points[2].Time)
	}
}
