package editor

// editor_ai_panel_test.go — the AI group, the objective group and the
// navigation overlay, drawn for real with no window and no GL. The groups are
// imgui stack discipline like the rest of the panel (see
// editor_panel_draw_test.go), and each has an empty state and a full one that
// open different subtrees.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// drawInWindow runs one imgui frame with body inside a window.
func drawInWindow(body func()) {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 800})
	io.SetDeltaTime(1.0 / 60.0)
	imgui.NewFrame()
	imgui.SetNextWindowSize(imgui.Vec2{X: 360, Y: 800})
	imgui.Begin("panel")
	body()
	imgui.End()
	imgui.Render()
}

// aiEditorLevel is a room with an agent listing two objectives, one present.
func aiEditorLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	lvl := selectTestLevel(t)
	room := world.Chunk{ID: "room"}
	room.Brushes = world.NewHollowRoom("room", mgl32.Vec3{}, mgl32.Vec3{12, 4, 12}, 0.5, "")
	room.RecomputeBounds()
	lvl.Chunks = []world.Chunk{room}
	spec := world.DefaultAISpec()
	spec.Targets = []string{"player", "#intruder"}
	spec.Objectives = []string{"post", "#missing"}
	spec.Loop = true
	agent := world.NewBlankEntity("guard")
	agent.Position = mgl32.Vec3{2, 0, 2}
	agent.BoundHalfX, agent.BoundHalfY, agent.BoundHalfZ = 0.3, 0.9, 0.3
	agent.BoundOffset = mgl32.Vec3{0, 0.9, 0}
	agent.AI = &spec
	lvl.Entities = []world.Entity{agent, world.NewBlankEntity("crate")}
	post := world.NewVolume("post", mgl32.Vec3{9, 1, 9})
	o := world.DefaultVolumeObjective()
	post.Objective = &o
	lvl.Volumes = []world.Volume{post, world.NewVolume("plain", mgl32.Vec3{1, 1, 1})}
	_ = world.RebuildIndexes(lvl)
	return lvl
}

func TestTheAIGroupsDrawInEveryState(t *testing.T) {
	defer saveEditorSelection()()
	lvl := aiEditorLevel(t)
	withImgui(func() {
		// A placed entity with a controller, one without, and one whose type
		// cannot give it one.
		drawInWindow(func() { editorEntityAISection(&lvl.Entities[0]) })
		drawInWindow(func() { editorEntityAISection(&lvl.Entities[1]) })
		// The volume's group, as an objective and not.
		drawInWindow(func() { editorVolumeObjective(&lvl.Volumes[0]) })
		drawInWindow(func() { editorVolumeObjective(&lvl.Volumes[1]) })
		// A type's group in the entity editor, with a controller and without.
		prevDef := entityDef
		defer func() { entityDef = prevDef }()
		entityDef = &world.EntityDef{Name: "guard", AI: lvl.Entities[0].AI.Clone()}
		drawInWindow(entityPanelAI)
		entityDef = &world.EntityDef{Name: "crate"}
		drawInWindow(entityPanelAI)
		// The whole panel with each selected, which reaches the groups
		// through their real call sites.
		editorSelectEntity(0)
		drawPanelOnce(lvl, panelFocusEntity)
		editorSelectVolume(0)
		drawPanelOnce(lvl, panelFocusVolume)
	})
}

func TestTheNavigationOverlayDrawsTheGridAndTheRoute(t *testing.T) {
	defer saveEditorSelection()()
	aiEditorLevel(t)
	prevCam, prevShow := gfx.Cam.Position, Editor.ShowNavigation
	t.Cleanup(func() { gfx.Cam.Position, Editor.ShowNavigation = prevCam, prevShow })
	gfx.Cam.Position = mgl32.Vec3{6, 3, 6}

	gizmo.Buf = gizmo.Buf[:0]
	Editor.ShowNavigation = false
	drawNavigationOverlay()
	if len(gizmo.Buf) != 0 {
		t.Fatal("the overlay drew with Show navigation off")
	}

	Editor.ShowNavigation = true
	editorClearObjectSelections()
	drawNavigationOverlay()
	grid := len(gizmo.Buf)
	if grid == 0 {
		t.Fatal("the overlay drew nothing over a walkable room")
	}

	gizmo.Buf = gizmo.Buf[:0]
	editorSelectEntity(0)
	drawNavigationOverlay()
	if len(gizmo.Buf) <= grid {
		t.Errorf("selecting an agent with objectives drew no route (%d floats, the bare grid was %d)", len(gizmo.Buf), grid)
	}
	gizmo.Buf = gizmo.Buf[:0]
}

func TestThePointAIGroupDrawsAndMakesAStopVolume(t *testing.T) {
	defer saveEditorSelection()()
	lvl := aiEditorLevel(t)
	a := world.WaypointAnimation{ID: "sweep", Points: []world.Waypoint{
		world.NewWaypoint(mgl32.Vec3{3, 0, 3}), world.NewWaypoint(mgl32.Vec3{9, 0, 3}),
	}}
	lvl.WaypointAnimations = []world.WaypointAnimation{a}
	anim := &lvl.WaypointAnimations[0]
	withImgui(func() {
		// No block, then a full one, in the level editor and the entity editor.
		drawInWindow(func() { editorWaypointAIUI(lvl, anim, 0, &anim.Points[0], func(string) {}) })
		anim.Points[0].AI = &world.WaypointAI{TakeCover: true, Wait: 2, WaitForSignal: true, Engage: world.AIEngageHold}
		drawInWindow(func() { editorWaypointAIUI(lvl, anim, 0, &anim.Points[0], func(string) {}) })
		drawInWindow(func() { editorWaypointAIUI(nil, anim, 0, &anim.Points[0], func(string) {}) })
		// Through the real panel, with the point selected.
		editorSelectWaypoint(0, 0)
		drawPanelOnce(lvl, panelFocusWaypoint)
	})

	before := len(lvl.Volumes)
	id := createStopVolume(lvl, anim, 1)
	if len(lvl.Volumes) != before+1 || world.FindVolume(lvl, id) == nil {
		t.Fatalf("Create Volume Here made no volume (%q)", id)
	}
	v := world.FindVolume(lvl, id)
	if d := v.WorldPosition(lvl).Sub(mgl32.Vec3{9, 1, 3}).Len(); d > 1e-4 {
		t.Errorf("the stop volume was placed at %v, want over the point", v.WorldPosition(lvl))
	}

	gizmo.Buf = gizmo.Buf[:0]
	drawWaypointAIMarks(lvl, mgl32.Ident4(), &anim.Points[0])
	if len(gizmo.Buf) < 6*33 {
		t.Errorf("a cover point that waits drew %d floats; want its cover ring and its wait mark", len(gizmo.Buf))
	}
	gizmo.Buf = gizmo.Buf[:0]
}

func TestTheRoutePreviewWalksAnAgentsPath(t *testing.T) {
	defer saveEditorSelection()()
	lvl := aiEditorLevel(t)
	lvl.Entities[0].AI.Objectives = nil
	lvl.WaypointAnimations = []world.WaypointAnimation{{ID: "beat", Points: []world.Waypoint{
		world.NewWaypoint(mgl32.Vec3{9, 0, 3}), world.NewWaypoint(mgl32.Vec3{9, 0, 9}),
	}}}
	gizmo.Buf = gizmo.Buf[:0]
	drawAgentRoute(lvl, &lvl.Entities[0])
	if len(gizmo.Buf) != 0 {
		t.Fatal("an agent with no path and no objectives drew a route")
	}
	lvl.Entities[0].AI.Waypoints = "beat"
	drawAgentRoute(lvl, &lvl.Entities[0])
	if len(gizmo.Buf) < 2*6 {
		t.Errorf("an agent with a two-point path drew %d floats of route", len(gizmo.Buf))
	}
	gizmo.Buf = gizmo.Buf[:0]
}
