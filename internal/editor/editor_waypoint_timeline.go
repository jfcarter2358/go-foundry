package editor

// editor_waypoint_timeline.go — what a waypoint animation draws over the 3D
// view in ImGui rather than GL: the timeline docked along the bottom, and each
// leg's length written above its line.
//
// The timeline is the model editor's (editor_model_timeline.go) with a single
// lane instead of one per bone: every point of the selected animation is a
// keyframe at its Time. It sits under the viewport for that file's reason — a
// time axis wants the window's width, not the panel's — and a click on it stays
// out of 3D picking the way a click on the side panel does, through
// io.WantCaptureMouse in the editor's event handler.
//
// Clicking a key selects its point, the same selection clicking the arrow
// makes, so the gizmo and the Properties panel follow it. Dragging a key retimes
// the point, held between its neighbours (WaypointAnimation.SetPointTime).
// Clicking anywhere else scrubs the playhead, which the preview object and the
// orange ghost arrow follow (editor_waypoint.go).

import (
	"fmt"
	"math"
	"strconv"

	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/platform"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	waypointTimelineRulerH  = float32(18)  // the seconds ruler, with room for its labels
	waypointTimelineLaneH   = float32(22)  // the one lane of keys
	waypointTimelineGutterW = float32(70)  // the lane's name
	waypointTimelinePad     = float32(10)  // keeps the first and last keys grabbable
	waypointRulerMinTickPx  = float32(56)  // the least room a ruler label gets
	waypointTimelineMinW    = float32(300) // narrower than this and there is no timeline to speak of
	// waypointTimelineSnap is the grid a dragged key lands on, in seconds.
	// Shift drags it free.
	waypointTimelineSnap = float32(0.05)
	// waypointLabelMinLegPx is how long a leg must be on screen to get its
	// length written on it. Any shorter and the label would sit over the arrows
	// at either end rather than over the line.
	waypointLabelMinLegPx = float32(48)
)

// waypointRulerSteps are the round numbers of seconds the ruler ticks at.
var waypointRulerSteps = [...]float32{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600}

// waypointTimelineDrag is the index of the point whose key is being dragged, or
// -1. waypointTimelineDragUndo is whether that drag has pushed its undo step —
// done once it actually moves, so a click that only selects a key is not an
// undo step of its own.
var (
	waypointTimelineDrag     = -1
	waypointTimelineDragUndo bool
)

// ---------- the docked window ----------

// waypointTimelineHeight is how tall the strip is: a title bar, the transport
// row, and the ruler and lane under it.
func waypointTimelineHeight() float32 {
	return imgui.FrameHeight() + imgui.FrameHeightWithSpacing() +
		waypointTimelineRulerH + waypointTimelineLaneH + 2*waypointTimelinePad
}

// editorWaypointTimelineWindow draws the strip while a waypoint animation is
// selected. Called from EditorDrawUI after the side panel, so it lands on top
// of the viewport.
//
// Only while one is selected, for the model timeline's reason: the strip costs
// the view its bottom edge, and selecting an animation is exactly "I am timing
// this now".
func editorWaypointTimelineWindow() {
	a := activeWaypointAnimation()
	if a == nil || Editor.Mode != Mode3D || ws().level != Editor.level {
		return
	}
	width := float32(platform.WinWidth) - Editor.PanelWidth
	if width < waypointTimelineMinW {
		return
	}
	height := waypointTimelineHeight()
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: float32(platform.WinHeight) - height})
	imgui.SetNextWindowSize(imgui.Vec2{X: width, Y: height})
	// ### keeps the window one window while its title follows the selection.
	if !imgui.BeginV("Waypoint Timeline: "+a.ID+"###wptimeline", nil,
		imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoCollapse|imgui.WindowFlagsNoScrollbar) {
		imgui.End()
		return
	}
	waypointTimelineTransportRow(a)
	// The row can delete the animation along with its last point, so the
	// canvas looks it up again rather than drawing what was there.
	if a = activeWaypointAnimation(); a != nil {
		waypointTimelineCanvas(a, levelWaypointTimelineHost(a))
	}
	imgui.End()
}

// waypointTimelineTransportRow is the transport, the length, the preview object
// and the two key actions, so timing a path never needs the side panel.
func waypointTimelineTransportRow(a *world.WaypointAnimation) {
	p := &Editor.WaypointPreview
	length := a.EffectiveLength()

	if imgui.Button("|<##wptl") {
		editorWaypointPreviewSeek(0)
	}
	waypointTimelineTip("Back to the start")
	imgui.SameLine()
	if imgui.Button("<##wptl") {
		editorWaypointPreviewSeek(waypointKeyTimeBefore(a, p.Time))
	}
	waypointTimelineTip("The point before the playhead")
	imgui.SameLine()
	// Latched, because clicking flips Playing — see editorActiveToggleButton
	// (editor_ui.go).
	playing := p.Playing
	label := "Play##wptl"
	if playing {
		label = "Pause##wptl"
	}
	if editorActiveToggleButton(label, playing) {
		if playing {
			editorWaypointPreviewPause()
		} else {
			editorWaypointPreviewPlay(a.ID)
		}
	}
	imgui.SameLine()
	if imgui.Button(">##wptl") {
		editorWaypointPreviewSeek(waypointKeyTimeAfter(a, p.Time))
	}
	waypointTimelineTip("The point after the playhead")
	imgui.SameLine()
	if imgui.Button(">|##wptl") {
		editorWaypointPreviewSeek(length)
	}
	waypointTimelineTip("To the end")
	imgui.SameLine()
	if imgui.Button("Stop##wptl") {
		editorWaypointPreviewStop()
	}
	waypointTimelineTip("Back to the start, and the preview object back where it was authored")
	imgui.SameLine()
	imgui.Checkbox("Loop##wptl", &p.Loop)

	imgui.SameLine()
	imgui.Text(fmt.Sprintf("  %.2f / %.2f s  ", p.Time, length))

	imgui.SameLine()
	imgui.SetNextItemWidth(80)
	// A floor above zero: stretching every time down to nothing and back up
	// again would lose the rhythm the stretch exists to keep.
	if l := length; editorDragFloatStep("Length##wptllen", &l, waypointTimelineSnap, 3600, "%.2f s", waypointTimelineSnap) {
		editorSetWaypointAnimationLength(a, l)
	}
	waypointTimelineTip("How long the whole animation takes. Changing it stretches every\n" +
		"point's time with it; a length past the last point holds the object there.")

	imgui.SameLine()
	imgui.SetNextItemWidth(150)
	editorWaypointPreviewTargetCombo("Object##wptlobj")

	imgui.SameLine()
	if imgui.Button("Key At Playhead##wptlkey") {
		insertWaypointAtPlayhead(a)
		return
	}
	waypointTimelineTip("A point where the playhead has the path, at the playhead's time.\n" +
		"Move, turn or scale it with the gizmo afterwards.")
	imgui.SameLine()
	imguibackend.EditorPushDisabled(activeWaypoint() == nil)
	if imgui.Button("Delete Point##wptldel") {
		deleteActiveWaypoint()
	}
	imguibackend.EditorPopDisabled()
}

func waypointTimelineTip(s string) {
	if imgui.IsItemHovered() {
		imgui.SetTooltip(s)
	}
}

// waypointKeyTimeBefore is the latest point time before t, or the start.
func waypointKeyTimeBefore(a *world.WaypointAnimation, t float32) float32 {
	best := float32(0)
	for i := range a.Points {
		if pt := a.Points[i].Time; pt < t-1e-4 && pt > best {
			best = pt
		}
	}
	return best
}

// waypointKeyTimeAfter is the earliest point time after t, or the end. The
// times run forwards along the list, so the first one past t is the earliest.
func waypointKeyTimeAfter(a *world.WaypointAnimation, t float32) float32 {
	for i := range a.Points {
		if pt := a.Points[i].Time; pt > t+1e-4 {
			return pt
		}
	}
	return a.EffectiveLength()
}

// ---------- the lane ----------

// waypointTimelineHost is what the lane needs from the window it is drawn in.
// The level editor's strip and the entity editor's each supply one, so the
// ruler, the keys and a key's drag are written once.
type waypointTimelineHost struct {
	// selected is the point whose key is drawn on top, or -1.
	selected int
	// playhead is where the playhead line is drawn, in seconds.
	playhead float32
	// selectPoint selects point i: the same selection clicking its arrow makes.
	selectPoint func(i int)
	// seek moves the playhead to t and poses the preview there.
	seek func(t float32)
	// pushUndo is called once, before a drag first retimes a key.
	pushUndo func()
	// tip is the tooltip over point i's key.
	tip func(i int) string
	// locked keeps the keys where they are — a path an entity carries, seen
	// from the level editor. Clicking a key still selects it and parks on it.
	locked bool
}

// levelWaypointTimelineHost is the level editor's host for a.
func levelWaypointTimelineHost(a *world.WaypointAnimation) waypointTimelineHost {
	return waypointTimelineHost{
		selected:    Editor.ActiveWaypointIdx,
		playhead:    Editor.WaypointPreview.Time,
		selectPoint: func(i int) { editorSelectWaypoint(Editor.ActiveWaypointAnimIdx, i) },
		seek:        editorWaypointPreviewSeek,
		pushUndo:    EditorPushUndo,
		tip: func(i int) string {
			return fmt.Sprintf("Point %d at %.2f s\n%s", i+1, a.Points[i].Time, waypointLegSummary(a, i))
		},
		locked: waypointCarried(a),
	}
}

// waypointTimelineCanvas draws the ruler, the keys and the playhead, and
// resolves clicks and drags on them.
//
// The geometry is the model timeline's modelTimelineLayout, so the two strips
// map time to pixels the same way; the lanes and frame rate it also carries go
// unused here, since a waypoint animation has neither bones nor frames.
func waypointTimelineCanvas(a *world.WaypointAnimation, host waypointTimelineHost) {
	origin := imgui.CursorScreenPos()
	avail := imgui.ContentRegionAvail()
	if avail.X < waypointTimelineGutterW+60 {
		return
	}
	height := waypointTimelineRulerH + waypointTimelineLaneH
	imgui.InvisibleButton("##wptlcanvas", imgui.Vec2{X: avail.X, Y: height})
	hovered, held := imgui.IsItemHovered(), imgui.IsItemActive()

	l := modelTimelineLayout{
		x0:  origin.X + waypointTimelineGutterW,
		x1:  origin.X + avail.X - waypointTimelinePad,
		top: origin.Y,
		dur: a.EffectiveLength(),
	}
	l.span = l.x1 - l.x0
	if l.span <= 1 {
		return
	}
	laneY := origin.Y + waypointTimelineRulerH + waypointTimelineLaneH/2

	waypointTimelineDraw(l, a, origin, avail.X, height, laneY, host)
	waypointTimelineInput(l, a, laneY, hovered, held, host)
}

func waypointTimelineDraw(l modelTimelineLayout, a *world.WaypointAnimation, origin imgui.Vec2, width, height, laneY float32, host waypointTimelineHost) {
	dl := imgui.WindowDrawList()
	bg := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.11, Y: 0.11, Z: 0.13, W: 1})
	gutter := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.15, Y: 0.15, Z: 0.18, W: 1})
	grid := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.28, Y: 0.28, Z: 0.33, W: 1})
	laneCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.20, Y: 0.20, Z: 0.24, W: 1})
	textCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.72, Y: 0.74, Z: 0.80, W: 1})
	dimText := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.50, Y: 0.52, Z: 0.58, W: 1})
	keyCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.95, Y: 0.78, Z: 0.25, W: 1})
	selCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1})
	headCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.95, Y: 0.35, Z: 0.25, W: 1})

	dl.AddRectFilled(origin, imgui.Vec2{X: origin.X + width, Y: origin.Y + height}, bg)
	dl.AddRectFilled(origin, imgui.Vec2{X: origin.X + waypointTimelineGutterW, Y: origin.Y + height}, gutter)

	// Seconds ruler, at the first round step that leaves each label room.
	step := waypointRulerStep(l)
	for i := 0; i < 10000; i++ {
		t := float32(i) * step
		if t > l.dur+1e-4 {
			break
		}
		x := l.timeToX(t)
		dl.AddLine(imgui.Vec2{X: x, Y: origin.Y + 2}, imgui.Vec2{X: x, Y: origin.Y + waypointTimelineRulerH}, grid)
		dl.AddText(imgui.Vec2{X: x + 3, Y: origin.Y + 1}, dimText, strconv.FormatFloat(float64(t), 'f', -1, 32))
	}

	dl.AddText(imgui.Vec2{X: origin.X + 8, Y: laneY - 7}, textCol, "Points")
	dl.AddLine(imgui.Vec2{X: l.x0, Y: laneY}, imgui.Vec2{X: l.x1, Y: laneY}, laneCol)
	sel := host.selected
	for i := range a.Points {
		if i != sel {
			modelDrawKeyDiamond(dl, l.timeToX(a.Points[i].Time), laneY, modelTimelineKeyR+1, keyCol)
		}
	}
	// The selected key last, so it is the one on top of a stack of keys at
	// the same moment.
	if sel >= 0 && sel < len(a.Points) {
		modelDrawKeyDiamond(dl, l.timeToX(a.Points[sel].Time), laneY, modelTimelineKeyR+1, selCol)
	}

	px := l.timeToX(host.playhead)
	dl.AddLine(imgui.Vec2{X: px, Y: origin.Y}, imgui.Vec2{X: px, Y: origin.Y + height}, headCol)
}

// waypointRulerStep is the seconds between ruler ticks.
func waypointRulerStep(l modelTimelineLayout) float32 {
	if l.dur <= 0 {
		return 1
	}
	for _, s := range waypointRulerSteps {
		if s/l.dur*l.span >= waypointRulerMinTickPx {
			return s
		}
	}
	return waypointRulerSteps[len(waypointRulerSteps)-1]
}

// waypointTimelineInput resolves the mouse on the lane: a key selects and
// drags, anything else scrubs.
//
// Keys are tested before the scrub, as the model timeline tests its diamonds,
// so a click aimed at a key is never read as "move the playhead near it".
func waypointTimelineInput(l modelTimelineLayout, a *world.WaypointAnimation, laneY float32, hovered, held bool, host waypointTimelineHost) {
	mouse := imgui.MousePos()
	if hovered && !held {
		if i, ok := waypointTimelinePickKey(l, a, laneY, mouse, host.selected); ok && host.tip != nil {
			imgui.SetTooltip(host.tip(i))
		}
	}
	if !held {
		waypointTimelineDrag = -1
		return
	}

	if imgui.IsMouseClicked(0) {
		waypointTimelineDrag, waypointTimelineDragUndo = -1, false
		if i, ok := waypointTimelinePickKey(l, a, laneY, mouse, host.selected); ok {
			host.selectPoint(i)
			// Park on the key, so what the view shows is the point just picked.
			host.seek(a.Points[i].Time)
			waypointTimelineDrag = i
			return
		}
	}

	if i := waypointTimelineDrag; i >= 0 {
		// A locked key is still held, so the press does not turn into a scrub
		// the moment the mouse moves; it just goes nowhere.
		if i >= len(a.Points) || !imgui.IsMouseDragging(0, 2) || host.locked {
			return
		}
		if !waypointTimelineDragUndo {
			host.pushUndo()
			waypointTimelineDragUndo = true
		}
		t := l.xToTime(mouse.X)
		if sdl.GetModState()&sdl.KMOD_SHIFT == 0 {
			t = float32(math.Round(float64(t/waypointTimelineSnap))) * waypointTimelineSnap
			// Rounding may carry the last key past the end, which would
			// quietly lengthen the animation from a drag that stayed inside it.
			if t > l.dur {
				t = l.dur
			}
		}
		host.seek(a.SetPointTime(i, t))
		return
	}

	host.seek(l.xToTime(mouse.X))
}

// waypointTimelinePickKey returns the point whose key is under the cursor.
//
// Keys at the same moment stack. The selected one wins, since it is the one
// drawn on top; otherwise the later one does, which is the one a drag to the
// right can pull out of the stack.
func waypointTimelinePickKey(l modelTimelineLayout, a *world.WaypointAnimation, laneY float32, mouse imgui.Vec2, selected int) (int, bool) {
	if geom.Abs32(mouse.Y-laneY) > waypointTimelineLaneH/2 {
		return -1, false
	}
	reach := modelTimelineKeyR + 3
	best, bestD := -1, reach
	for i := range a.Points {
		d := geom.Abs32(l.timeToX(a.Points[i].Time) - mouse.X)
		switch {
		case d > reach:
		case i == selected:
			return i, true
		case d <= bestD:
			best, bestD = i, d
		}
	}
	return best, best >= 0
}

// ---------- leg distances ----------

// editorWaypointDistanceLabels writes each leg's length above the line that
// joins its two points. It is the number an author timing a path needs beside
// the times on the timeline: how fast the object goes is the one over the other.
//
// On the background draw list, so the side panel and the timeline cover a label
// rather than the other way round. A leg too short on screen to hold one, or
// with an end behind the camera, goes unlabelled.
func editorWaypointDistanceLabels() {
	lvl := Editor.level
	if lvl == nil || Editor.Mode != Mode3D || ws().level != lvl || len(lvl.WaypointAnimations) == 0 {
		return
	}
	projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
	dl := imgui.BackgroundDrawList()
	bg := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.05, Y: 0.05, Z: 0.07, W: 0.72})

	for ai := range lvl.WaypointAnimations {
		a := &lvl.WaypointAnimations[ai]
		c := waypointDimColor
		if ai == Editor.ActiveWaypointAnimIdx {
			c = waypointArrowColor
		}
		col := imgui.PackedColorFromVec4(imgui.Vec4{X: c.X(), Y: c.Y(), Z: c.Z(), W: 1})

		for pi := 1; pi < len(a.Points); pi++ {
			from := waypointWorldPosition(lvl, a, a.Points[pi-1].Position)
			to := waypointWorldPosition(lvl, a, a.Points[pi].Position)
			s0, ok0 := projectToScreen(projView, from)
			s1, ok1 := projectToScreen(projView, to)
			if !ok0 || !ok1 {
				continue
			}
			leg := s1.Sub(s0)
			if leg.Len() < waypointLabelMinLegPx {
				continue
			}
			// The 3D midpoint, where the leg's chevron is drawn, rather than
			// the midpoint on screen, which perspective slides off it.
			mid, ok := projectToScreen(projView, from.Add(to).Mul(0.5))
			if !ok {
				continue
			}
			// "Above" whichever way the line runs across the screen: along its
			// perpendicular, on the side that points up.
			perp := mgl32.Vec2{-leg.Y(), leg.X()}.Normalize()
			if perp.Y() > 0 {
				perp = perp.Mul(-1)
			}
			text := fmt.Sprintf("%.2f", to.Sub(from).Len())
			size := imgui.CalcTextSize(text, false, 0)
			centre := mid.Add(perp.Mul(size.Y/2 + 8))
			x, y := centre.X()-size.X/2, centre.Y()-size.Y/2
			dl.AddRectFilledV(imgui.Vec2{X: x - 3, Y: y - 1}, imgui.Vec2{X: x + size.X + 3, Y: y + size.Y + 1},
				bg, 3, imgui.DrawFlagsRoundCornersAll)
			dl.AddText(imgui.Vec2{X: x, Y: y}, col, text)
		}
	}
}
