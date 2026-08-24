package main

// editor_model_timeline.go — the keyframe timeline, docked along the bottom of
// the viewport.
//
// It sits under the 3D view rather than inside the side panel because a
// timeline is a *time* axis: it wants the window's full width, and the panel is
// 280px of it. Overlaying the viewport is the same arrangement the side panel
// already uses — the GL view is drawn full-size underneath and ImGui paints on
// top, and modelEditorHandleEvent's io.WantCaptureMouse() check keeps a click on
// the strip from also falling through to 3D picking.
//
// Keyframes are selectable here, and selection is what Delete acts on. Without
// it the only way to remove a key was to park the playhead on it exactly, which
// is fiddly on a clip whose keys are not on round frames.

import (
	"fmt"
	"foundry/internal/geom"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	modelTimelineLaneH   = float32(16)  // one bone's row
	modelTimelineRulerH  = float32(14)  // the frame ruler above the lanes
	modelTimelineGutterW = float32(110) // bone-name column
	modelTimelinePad     = float32(8)   // keeps frame 0 and the last frame grabbable
	modelTimelineHeaderH = float32(30)  // the transport row
	modelTimelineMaxH    = float32(230) // past this the lanes scroll instead of growing
	modelTimelineKeyR    = float32(4.5) // diamond half-extent, and the click radius
)

// modelKeyRef identifies one keyframe: a bone's track plus the instant.
//
// A time rather than an index into Keys, because inserting or deleting a key
// renumbers the slice and would silently reselect a different one.
type modelKeyRef struct {
	Bone int
	Time float32
}

// modelSelectedKeys is what Delete acts on.
var modelSelectedKeys []modelKeyRef

func modelKeySelected(bone int, t float32) bool {
	for _, k := range modelSelectedKeys {
		if k.Bone == bone && geom.Abs32(k.Time-t) <= modelKeyEpsilon {
			return true
		}
	}
	return false
}

func modelClearKeySelection() { modelSelectedKeys = nil }

// modelSelectKey adds or replaces the selection. Additive is ctrl-click, the
// same modifier the mesh and bone selections use.
func modelSelectKey(refs []modelKeyRef, additive bool) {
	if !additive {
		modelSelectedKeys = append([]modelKeyRef(nil), refs...)
		return
	}
	for _, r := range refs {
		if modelKeySelected(r.Bone, r.Time) {
			kept := modelSelectedKeys[:0]
			for _, k := range modelSelectedKeys {
				if k.Bone == r.Bone && geom.Abs32(k.Time-r.Time) <= modelKeyEpsilon {
					continue
				}
				kept = append(kept, k)
			}
			modelSelectedKeys = kept
			continue
		}
		modelSelectedKeys = append(modelSelectedKeys, r)
	}
}

// modelPruneKeySelection drops references to keys that no longer exist — after
// a delete, an undo, or a clip switch.
func modelPruneKeySelection() {
	c := modelActiveClip()
	if c == nil {
		modelSelectedKeys = nil
		return
	}
	kept := modelSelectedKeys[:0]
	for _, k := range modelSelectedKeys {
		if c.HasKeyAt(k.Bone, k.Time) {
			kept = append(kept, k)
		}
	}
	modelSelectedKeys = kept
}

// modelDeleteSelectedKeys removes the selected keyframes and returns how many
// went. Falls back to the playhead when nothing is selected, so Delete still
// does the obvious thing before anyone has clicked a diamond.
func modelDeleteSelectedKeys() int {
	c := modelActiveClip()
	if c == nil {
		return 0
	}
	if len(modelSelectedKeys) == 0 {
		modelDeleteKeyAtPlayhead()
		return 0
	}
	ModelPushUndo()
	removed := 0
	for _, k := range modelSelectedKeys {
		if c.DeleteKey(k.Bone, k.Time) {
			removed++
		}
	}
	modelClearKeySelection()
	if removed == 0 {
		modelUndoStack.pop()
		return 0
	}
	modelPoseSampleClip()
	modelSetStatus("Deleted %d keyframe(s)", removed)
	return removed
}

// ---------- layout ----------

// modelTimelineLayout is the geometry the drawing and the hit test share, so a
// diamond is always clicked exactly where it was painted.
type modelTimelineLayout struct {
	x0, x1, span float32
	top          float32
	lanes        []int
	dur, fps     float32
}

func (l modelTimelineLayout) timeToX(t float32) float32 {
	if l.dur <= 0 {
		return l.x0
	}
	return l.x0 + l.span*geom.ClampValue(t/l.dur, 0, 1)
}

func (l modelTimelineLayout) xToTime(x float32) float32 {
	if l.span <= 0 {
		return 0
	}
	return geom.ClampValue((x-l.x0)/l.span, 0, 1) * l.dur
}

// summaryY is the "every bone" lane, directly under the ruler.
func (l modelTimelineLayout) summaryY() float32 {
	return l.top + modelTimelineRulerH + modelTimelineLaneH/2
}

func (l modelTimelineLayout) laneY(i int) float32 {
	return l.top + modelTimelineRulerH + modelTimelineLaneH*float32(i+1) + modelTimelineLaneH/2
}

// modelTimelineHeight is how tall the docked strip needs to be, capped.
func modelTimelineHeight() float32 {
	lanes := len(modelKeyedBoneIndices()) + 1 // + the summary lane
	h := modelTimelineHeaderH + modelTimelineRulerH + float32(lanes)*modelTimelineLaneH + modelTimelinePad*2
	if h > modelTimelineMaxH {
		h = modelTimelineMaxH
	}
	return h
}

// ---------- the docked window ----------

// modelAnimTimelineWindow draws the strip. Called from the editor window's Draw
// after the side panel, so it lands on top of the viewport.
func modelAnimTimelineWindow() {
	w := modelEditorWindow
	// Only while posing. The strip is ~200px of viewport, and a rigged model
	// keeps its clips forever — leaving it up during plain mesh editing would
	// permanently crop the view for a panel that has nothing to say there. Pose
	// mode is exactly "I am animating now", and P toggles it.
	if w == nil || modelDoc == nil || !modelPoseActive() || modelActiveClip() == nil {
		return
	}
	height := modelTimelineHeight()
	width := float32(w.W) - modelPanelWidth
	if width < 200 {
		return // no room beside the panel; the panel keeps the transport
	}

	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: float32(w.H) - height})
	imgui.SetNextWindowSize(imgui.Vec2{X: width, Y: height})
	if !imgui.BeginV("Timeline##modeltimeline", nil,
		imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoCollapse) {
		imgui.End()
		return
	}
	modelTimelineTransportRow()
	modelTimelineCanvas()
	imgui.End()
}

// modelTimelineTransportRow is the compact transport, so the strip is usable
// without reaching back to the side panel.
func modelTimelineTransportRow() {
	c := modelActiveClip()
	if c == nil {
		return
	}
	if imgui.Button("|<##tl") {
		modelPosePlaying = false
		modelPoseSetFrame(0)
	}
	imgui.SameLine()
	if imgui.Button("<##tl") {
		modelPosePlaying = false
		modelPoseStepFrame(-1)
	}
	imgui.SameLine()
	// Latched, because clicking flips modelPosePlaying — see
	// editorActiveToggleButton (editor_ui.go).
	playing := modelPosePlaying
	label := "Play##tl"
	if playing {
		label = "Pause##tl"
	}
	if editorActiveToggleButton(label, playing) {
		modelTogglePlayback()
	}
	imgui.SameLine()
	if imgui.Button(">##tl") {
		modelPosePlaying = false
		modelPoseStepFrame(1)
	}
	imgui.SameLine()
	if imgui.Button(">|##tl") {
		modelPosePlaying = false
		modelPoseSetFrame(c.frameCount())
	}

	imgui.SameLine()
	imgui.Text(fmt.Sprintf("  %s   frame %d / %d", c.Name, modelPoseFrame(), c.frameCount()))

	imgui.SameLine()
	editorPushDisabled(len(modelSelectedKeys) == 0)
	if imgui.Button("Delete key##tldel") {
		modelDeleteSelectedKeys()
	}
	editorPopDisabled()
	if n := len(modelSelectedKeys); n > 0 {
		imgui.SameLine()
		imgui.Text(fmt.Sprintf("(%d selected)", n))
	}
}

// modelTimelineCanvas draws the ruler, the lanes and the playhead, and resolves
// clicks on them.
//
// The lanes live in a scrolling child region rather than being capped: a
// keyframe you cannot scroll to is a keyframe you cannot select, and selection
// is the whole point of the click handling below.
func modelTimelineCanvas() {
	c := modelActiveClip()
	if c == nil {
		return
	}
	if !imgui.BeginChildV("##modeltimelinelanes", imgui.Vec2{}, false, 0) {
		imgui.EndChild()
		return
	}

	lanes := modelKeyedBoneIndices()
	origin := imgui.CursorScreenPos()
	avail := imgui.ContentRegionAvail()
	canvasH := modelTimelineRulerH + float32(len(lanes)+1)*modelTimelineLaneH + modelTimelinePad
	if avail.X < modelTimelineGutterW+60 {
		imgui.EndChild()
		return
	}
	imgui.InvisibleButton("##modeltimelinecanvas", imgui.Vec2{X: avail.X, Y: canvasH})
	hovered := imgui.IsItemHovered()
	held := imgui.IsItemActive()

	l := modelTimelineLayout{
		x0:    origin.X + modelTimelineGutterW,
		x1:    origin.X + avail.X - modelTimelinePad,
		top:   origin.Y,
		dur:   c.duration(),
		fps:   c.fps(),
		lanes: lanes,
	}
	l.span = l.x1 - l.x0
	if l.span <= 1 {
		imgui.EndChild()
		return
	}

	modelTimelineDraw(l, c, origin, avail.X, canvasH)
	if hovered || held {
		modelTimelineHandleInput(l, c, held)
	}
	imgui.EndChild()
}

func modelTimelineDraw(l modelTimelineLayout, c *ModelClip, origin imgui.Vec2, width, height float32) {
	dl := imgui.WindowDrawList()
	bg := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.11, Y: 0.11, Z: 0.13, W: 1})
	gutter := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.15, Y: 0.15, Z: 0.18, W: 1})
	grid := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.28, Y: 0.28, Z: 0.33, W: 1})
	laneCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.20, Y: 0.20, Z: 0.24, W: 1})
	textCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.72, Y: 0.74, Z: 0.80, W: 1})
	keyCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.95, Y: 0.78, Z: 0.25, W: 1})
	selCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1})
	headCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.95, Y: 0.35, Z: 0.25, W: 1})

	dl.AddRectFilled(origin, imgui.Vec2{X: origin.X + width, Y: origin.Y + height}, bg)
	dl.AddRectFilled(origin, imgui.Vec2{X: origin.X + modelTimelineGutterW, Y: origin.Y + height}, gutter)

	// Frame ruler, thinned so a long clip does not draw a solid bar.
	frames := c.frameCount()
	step := 1
	for frames/step > 40 {
		step *= 2
	}
	for f := 0; f <= frames; f += step {
		x := l.timeToX(float32(f) / l.fps)
		dl.AddLine(imgui.Vec2{X: x, Y: origin.Y + 2}, imgui.Vec2{X: x, Y: origin.Y + modelTimelineRulerH - 2}, grid)
	}

	// Summary lane: every instant any bone is keyed at.
	sy := l.summaryY()
	dl.AddText(imgui.Vec2{X: origin.X + 4, Y: sy - 7}, textCol, "All bones")
	dl.AddLine(imgui.Vec2{X: l.x0, Y: sy}, imgui.Vec2{X: l.x1, Y: sy}, laneCol)
	for _, t := range c.KeyTimes() {
		col := keyCol
		// The summary marker reads as selected only when every bone keyed at
		// that instant is, which is what clicking it selects.
		if modelSummaryKeySelected(c, t) {
			col = selCol
		}
		modelDrawKeyDiamond(dl, l.timeToX(t), sy, modelTimelineKeyR, col)
	}

	for i, bi := range l.lanes {
		y := l.laneY(i)
		name := fmt.Sprintf("Bone %d", bi)
		if modelDoc.validBone(bi) {
			name = modelDoc.Bones[bi].Name
		}
		nameCol := textCol
		if modelBoneSelected(bi) {
			nameCol = selCol
		}
		dl.AddText(imgui.Vec2{X: origin.X + 12, Y: y - 7}, nameCol, name)
		dl.AddLine(imgui.Vec2{X: l.x0, Y: y}, imgui.Vec2{X: l.x1, Y: y}, laneCol)

		if tr := c.trackFor(bi); tr != nil {
			for _, k := range tr.Keys {
				col := keyCol
				if modelKeySelected(bi, k.Time) {
					col = selCol
				}
				modelDrawKeyDiamond(dl, l.timeToX(k.Time), y, modelTimelineKeyR, col)
			}
		}
	}

	// Playhead last, over the keys.
	px := l.timeToX(modelPoseTime)
	dl.AddLine(imgui.Vec2{X: px, Y: origin.Y}, imgui.Vec2{X: px, Y: origin.Y + height}, headCol)
}

// modelSummaryKeySelected reports whether every bone keyed at t is selected.
func modelSummaryKeySelected(c *ModelClip, t float32) bool {
	any := false
	for i := range c.Tracks {
		for _, k := range c.Tracks[i].Keys {
			if geom.Abs32(k.Time-t) > modelKeyEpsilon {
				continue
			}
			any = true
			if !modelKeySelected(c.Tracks[i].Bone, k.Time) {
				return false
			}
		}
	}
	return any
}

// modelTimelineHandleInput resolves a click: a diamond selects, anything else
// scrubs.
//
// Diamonds are tested before the scrub so a click aimed at a key is never read
// as "move the playhead near it", which is the difference between being able to
// delete a key and not.
func modelTimelineHandleInput(l modelTimelineLayout, c *ModelClip, held bool) {
	mouse := imgui.MousePos()
	if imgui.IsMouseClicked(0) {
		// SDL's modifier state rather than imgui's: this window reads its mouse
		// from imgui but the rest of the editor takes ctrl-additive from
		// GetModState (modelEditorClick's call site), and the two must agree.
		additive := sdl.GetModState()&sdl.KMOD_CTRL != 0
		if refs, ok := modelTimelinePickKey(l, c, mouse); ok {
			modelPosePlaying = false
			modelSelectKey(refs, additive)
			// Park on the key that was clicked, so Delete and re-keying both
			// act on what the user is looking at.
			modelPoseSetTime(refs[0].Time)
			return
		}
		if !additive {
			modelClearKeySelection()
		}
	}
	if held || imgui.IsMouseClicked(0) {
		modelPosePlaying = false
		if !modelPoseMode {
			modelSetPoseMode(true)
		}
		// Snapped to the frame grid, so a scrub parks somewhere a key can be
		// dropped.
		modelPoseSetFrame(int(l.xToTime(mouse.X)*l.fps + 0.5))
	}
}

// modelTimelinePickKey returns the keyframes under the cursor: one for a bone
// lane, or every bone keyed at that instant for the summary lane.
func modelTimelinePickKey(l modelTimelineLayout, c *ModelClip, mouse imgui.Vec2) ([]modelKeyRef, bool) {
	const rowSlop = modelTimelineLaneH / 2

	// Summary lane.
	if geom.Abs32(mouse.Y-l.summaryY()) <= rowSlop {
		best, ok := modelNearestKeyTime(l, c.KeyTimes(), mouse.X)
		if !ok {
			return nil, false
		}
		var refs []modelKeyRef
		for i := range c.Tracks {
			for _, k := range c.Tracks[i].Keys {
				if geom.Abs32(k.Time-best) <= modelKeyEpsilon {
					refs = append(refs, modelKeyRef{Bone: c.Tracks[i].Bone, Time: k.Time})
				}
			}
		}
		return refs, len(refs) > 0
	}

	for i, bi := range l.lanes {
		if geom.Abs32(mouse.Y-l.laneY(i)) > rowSlop {
			continue
		}
		tr := c.trackFor(bi)
		if tr == nil {
			return nil, false
		}
		times := make([]float32, len(tr.Keys))
		for j, k := range tr.Keys {
			times[j] = k.Time
		}
		best, ok := modelNearestKeyTime(l, times, mouse.X)
		if !ok {
			return nil, false
		}
		return []modelKeyRef{{Bone: bi, Time: best}}, true
	}
	return nil, false
}

// modelNearestKeyTime returns the key time whose diamond is nearest x, within
// the diamond's own radius.
func modelNearestKeyTime(l modelTimelineLayout, times []float32, x float32) (float32, bool) {
	best := float32(0)
	bestD := modelTimelineKeyR + 2
	found := false
	for _, t := range times {
		if d := geom.Abs32(l.timeToX(t) - x); d <= bestD {
			best, bestD, found = t, d, true
		}
	}
	return best, found
}

// modelDrawKeyDiamond draws one keyframe marker as two triangles — this imgui
// binding exposes AddTriangleFilled but no filled quad.
//
// A diamond rather than a dot because it is the shape every animation timeline
// uses for a key, and because its points make the exact frame easier to read off
// the ruler than a circle's edge does.
func modelDrawKeyDiamond(dl imgui.DrawList, x, y, r float32, col imgui.PackedColor) {
	top := imgui.Vec2{X: x, Y: y - r}
	right := imgui.Vec2{X: x + r, Y: y}
	bottom := imgui.Vec2{X: x, Y: y + r}
	left := imgui.Vec2{X: x - r, Y: y}
	dl.AddTriangleFilled(top, right, bottom, col)
	dl.AddTriangleFilled(top, bottom, left, col)
}
