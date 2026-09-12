package editor

// editor_particle_ramp.go — the transport bar and the three ramp strips.
//
// The strips are hand-drawn on an InvisibleButton canvas with WindowDrawList,
// the technique editor_model_timeline.go uses and for the same reason its
// header gives: imgui has no widget for "a row of draggable points on a curve".
//
// Two structural ideas are lifted from that file verbatim, because both were
// learned the hard way there:
//
//   - a layout struct shared between drawing and hit-testing, so a stop is
//     grabbed exactly where it was painted rather than a few pixels off;
//   - SELECTION AND DRAGGING KEYED BY T, NOT BY INDEX. Dragging a stop past its
//     neighbour re-sorts the list, so an index taken at mouse-down names a
//     different stop by mouse-up — the drag jumps to the wrong point and the
//     curve tangles.

import (
	"fmt"
	"foundry/internal/particle"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

const (
	particleTransportRowH = float32(30)
	particleStripH        = float32(56)
	particleStripPad      = float32(6)
	particleStopRadius    = float32(4.5)
	// particleGrabPx is how close the cursor has to be to grab a stop. Larger
	// than the dot it grabs, because a 4-pixel target is not a target.
	particleGrabPx = float32(9)
)

// particleRampKind is which strip is being drawn.
type particleRampKind int

const (
	rampSize particleRampKind = iota
	rampAlpha
	rampColor
	// The two a volume layer adds (particle.VolumeLook). Offered only while
	// the active layer renders as a volume — a sprite layer has no use for them.
	rampEmission
	rampErosion
)

var particleRampNames = []string{"Size", "Alpha", "Color", "Emission", "Erosion"}

// particleRampOffered reports whether a strip applies to the layer.
func particleRampOffered(k particleRampKind, lay *particle.Layer) bool {
	if k == rampEmission || k == rampErosion {
		return lay != nil && lay.Render == particle.RenderVolume
	}
	return true
}

// particleActiveRamp is which strip is showing. One at a time rather than three
// stacked: three would leave the preview a letterbox at this window's height.
var particleActiveRamp = rampAlpha

// particleDragStop identifies the stop being dragged, by kind and by T.
type particleDragStop struct {
	Kind   particleRampKind
	T      float32
	Active bool
}

var particleDrag particleDragStop

// particleColorPick is the stop a colour picker is open on, or -1.
var particleColorPick = -1

// particleTransportHeight is how much of the window the bottom bar takes.
func particleTransportHeight() float32 {
	return particleTransportRowH + particleStripH + particleStripPad*3 + 24
}

// particleTransportWindow draws the playback row and the active ramp strip.
func particleTransportWindow() {
	w := particleEditorWindow
	if w == nil || particleDoc == nil {
		return
	}
	h := particleTransportHeight()
	imgui.SetNextWindowPos(imgui.Vec2{X: particleLayerListWidth, Y: float32(w.H) - h})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W) - particleLayerListWidth - particlePanelWidth, Y: h})
	imgui.BeginV("##ptransport", nil,
		imgui.WindowFlagsNoMove|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoTitleBar|
			imgui.WindowFlagsNoScrollbar)

	particleTransportRow()
	particleRampTabs()
	particleRampCanvas()

	imgui.End()
}

// particleTransportRow draws play/pause/restart/step, the speed, and the
// readouts.
func particleTransportRow() {
	label := "Play"
	if particlePlaying {
		label = "Pause"
	}
	if imgui.Button(label + "##pplay") {
		particlePlaying = !particlePlaying
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Space")
	}
	imgui.SameLine()
	if imgui.Button("Restart##prestart") {
		particleRestart()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("R")
	}
	imgui.SameLine()
	if imgui.Button("Step##pstep") {
		particleStepOnce = true
		particlePlaying = false
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("One frame at 60Hz, whatever this window is running at.  .")
	}

	imgui.SameLine()
	imgui.SetNextItemWidth(90)
	if imgui.BeginCombo("##pspeedcombo", fmt.Sprintf("%.2gx", particleSpeed)) {
		for _, s := range []float32{0.1, 0.25, 0.5, 1, 2, 4} {
			if imgui.SelectableV(fmt.Sprintf("%.2gx", s), s == particleSpeed, 0, imgui.Vec2{}) {
				particleSpeed = s
			}
		}
		imgui.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Preview speed. Not saved with the effect.")
	}

	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	dur := float32(1)
	if l := particleActive(); l != nil {
		dur = l.EffectiveDuration()
	}
	editorTextMuted(fmt.Sprintf("t %.2f / %.2f    live %d / %d",
		modFloat(particleTime, dur), dur, particleLiveCount(), particleMaxCount()))
}

// modFloat wraps t into [0, d), for the cycle readout.
func modFloat(t, d float32) float32 {
	if d <= 0 {
		return t
	}
	for t >= d {
		t -= d
	}
	return t
}

// particleRampTabs picks which strip is showing.
func particleRampTabs() {
	lay := particleActive()
	if !particleRampOffered(particleActiveRamp, lay) {
		particleActiveRamp = rampAlpha
	}
	for i, n := range particleRampNames {
		if !particleRampOffered(particleRampKind(i), lay) {
			continue
		}
		label := n
		if particleRampKind(i) == rampAlpha && lay != nil && lay.Render == particle.RenderVolume {
			// The same ramp, read as what it means on a volume layer.
			label = "Density"
		}
		if i > 0 {
			imgui.SameLine()
		}
		if editorActiveToggleButton(label+"##pramptab"+n, particleActiveRamp == particleRampKind(i)) {
			particleActiveRamp = particleRampKind(i)
			particleColorPick = -1
		}
	}
	imgui.SameLine()
	editorTextMuted("Click adds a stop, drag moves it, right-click deletes.")
}

// particleRampLayout is the geometry both the drawing and the hit-testing read,
// so a stop is grabbed where it was painted.
type particleRampLayout struct {
	x0, x1, y0, y1 float32
}

func (l particleRampLayout) tToX(t float32) float32 { return l.x0 + t*(l.x1-l.x0) }
func (l particleRampLayout) xToT(x float32) float32 {
	if l.x1 <= l.x0 {
		return 0
	}
	return clamp01f((x - l.x0) / (l.x1 - l.x0))
}
func (l particleRampLayout) vToY(v float32) float32 { return l.y1 - clamp01f(v)*(l.y1-l.y0) }
func (l particleRampLayout) yToV(y float32) float32 {
	if l.y1 <= l.y0 {
		return 0
	}
	return clamp01f((l.y1 - y) / (l.y1 - l.y0))
}

// particleRampCanvas draws the active strip and handles its input.
func particleRampCanvas() {
	lay := particleActive()
	if lay == nil {
		return
	}
	origin := imgui.CursorScreenPos()
	avail := imgui.ContentRegionAvail()
	size := imgui.Vec2{X: avail.X, Y: particleStripH}
	imgui.InvisibleButton("##prampcanvas", size)

	l := particleRampLayout{
		x0: origin.X + particleStripPad,
		x1: origin.X + size.X - particleStripPad,
		y0: origin.Y + particleStripPad,
		y1: origin.Y + size.Y - particleStripPad,
	}

	dl := imgui.WindowDrawList()
	bg := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.13, Y: 0.13, Z: 0.16, W: 1})
	dl.AddRectFilled(origin, imgui.Vec2{X: origin.X + size.X, Y: origin.Y + size.Y}, bg)

	if particleActiveRamp == rampColor {
		particleDrawGradient(dl, l, lay)
	} else {
		particleDrawScalarRamp(dl, l, lay)
	}

	particleRampInput(l, lay)
	particleColorPopup(lay)
}

// particleDrawScalarRamp paints the size or alpha curve.
func particleDrawScalarRamp(dl imgui.DrawList, l particleRampLayout, lay *particle.Layer) {
	r := particleRampFor(lay)
	grid := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.25, Y: 0.25, Z: 0.3, W: 1})
	line := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.5, Y: 0.8, Z: 1, W: 1})
	dot := imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 0.85, Z: 0.3, W: 1})

	// A midline, so "half" is readable without counting pixels.
	dl.AddLine(imgui.Vec2{X: l.x0, Y: l.vToY(0.5)}, imgui.Vec2{X: l.x1, Y: l.vToY(0.5)}, grid)

	// The curve is sampled rather than drawn stop-to-stop, so it shows exactly
	// what Ramp.At will return — including the clamped flat ends.
	const samples = 96
	prev := imgui.Vec2{X: l.tToX(0), Y: l.vToY(scaleForDisplay(*r, r.At(0)))}
	for i := 1; i <= samples; i++ {
		t := float32(i) / samples
		p := imgui.Vec2{X: l.tToX(t), Y: l.vToY(scaleForDisplay(*r, r.At(t)))}
		dl.AddLine(prev, p, line)
		prev = p
	}
	for _, s := range *r {
		c := imgui.Vec2{X: l.tToX(s.T), Y: l.vToY(scaleForDisplay(*r, s.V))}
		dl.AddCircleFilled(c, particleStopRadius, dot)
	}
	if len(*r) == 0 {
		dl.AddText(imgui.Vec2{X: l.x0 + 4, Y: l.y0 + 2},
			imgui.PackedColorFromVec4(imgui.Vec4{X: 0.6, Y: 0.6, Z: 0.6, W: 1}),
			"No stops: the value is 1 for the whole life. Click to add one.")
	}
}

// scaleForDisplay maps a ramp value into the strip's 0..1 height. Size ramps
// routinely exceed 1 (smoke grows), so the strip rescales to fit rather than
// clipping the curve off its own top.
func scaleForDisplay(r particle.Ramp, v float32) float32 {
	max := float32(1)
	for _, s := range r {
		if s.V > max {
			max = s.V
		}
	}
	return v / max
}

// particleDrawGradient paints the colour ramp as a bar with its stops beneath.
func particleDrawGradient(dl imgui.DrawList, l particleRampLayout, lay *particle.Layer) {
	const bands = 128
	barTop, barBot := l.y0, l.y1-16
	for i := 0; i < bands; i++ {
		t0 := float32(i) / bands
		t1 := float32(i+1) / bands
		c := lay.ColorRamp.At((t0 + t1) * 0.5)
		col := imgui.PackedColorFromVec4(imgui.Vec4{X: c.X(), Y: c.Y(), Z: c.Z(), W: 1})
		dl.AddRectFilled(
			imgui.Vec2{X: l.tToX(t0), Y: barTop},
			imgui.Vec2{X: l.tToX(t1) + 1, Y: barBot}, col)
	}
	dot := imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1})
	for _, s := range lay.ColorRamp {
		dl.AddCircleFilled(imgui.Vec2{X: l.tToX(s.T), Y: barBot + 7}, particleStopRadius, dot)
	}
	if len(lay.ColorRamp) == 0 {
		dl.AddText(imgui.Vec2{X: l.x0 + 4, Y: l.y0 + 2},
			imgui.PackedColorFromVec4(imgui.Vec4{X: 0.6, Y: 0.6, Z: 0.6, W: 1}),
			"No stops: white for the whole life. Click to add one.")
	}
}

// particleRampFor returns the scalar ramp the active strip edits.
func particleRampFor(lay *particle.Layer) *particle.Ramp {
	switch particleActiveRamp {
	case rampSize:
		return &lay.SizeRamp
	case rampEmission:
		return &lay.Volume.EmissionRamp
	case rampErosion:
		return &lay.Volume.ErosionRamp
	}
	return &lay.AlphaRamp
}

// particleRampInput handles clicks and drags on the strip.
func particleRampInput(l particleRampLayout, lay *particle.Layer) {
	if !imgui.IsItemHovered() && !particleDrag.Active {
		return
	}
	mouse := imgui.MousePos()
	t := l.xToT(mouse.X)

	// Right-click deletes the nearest stop, if one is close enough.
	if imgui.IsMouseClicked(1) && imgui.IsItemHovered() {
		if idx := particleNearestStop(l, lay, mouse); idx >= 0 {
			ParticlePushUndo()
			particleDeleteStop(lay, idx)
			particleTouch()
		}
		return
	}

	if imgui.IsMouseClicked(0) && imgui.IsItemHovered() {
		if idx := particleNearestStop(l, lay, mouse); idx >= 0 {
			if particleActiveRamp == rampColor && imgui.IsMouseDoubleClicked(0) {
				particleColorPick = idx
				return
			}
			particleDrag = particleDragStop{Kind: particleActiveRamp, T: particleStopT(lay, idx), Active: true}
			ParticlePushUndo()
			return
		}
		// Empty space: add a stop where the click landed.
		ParticlePushUndo()
		particleAddStop(lay, t, l.yToV(mouse.Y))
		particleTouch()
		particleDrag = particleDragStop{Kind: particleActiveRamp, T: t, Active: true}
		return
	}

	if particleDrag.Active {
		if !imgui.IsMouseDown(0) {
			particleDrag.Active = false
			return
		}
		if particleDrag.Kind != particleActiveRamp {
			particleDrag.Active = false
			return
		}
		// Found by T, not by index: the drag may have carried this stop past a
		// neighbour, which re-sorts the list.
		idx := particleStopIndexAt(lay, particleDrag.T)
		if idx < 0 {
			particleDrag.Active = false
			return
		}
		particleMoveStop(lay, idx, t, l.yToV(mouse.Y))
		particleDrag.T = t
		particleTouch()
	}
}

// particleColorPopup opens the picker on a double-clicked colour stop.
func particleColorPopup(lay *particle.Layer) {
	if particleActiveRamp != rampColor || particleColorPick < 0 {
		return
	}
	if particleColorPick >= len(lay.ColorRamp) {
		particleColorPick = -1
		return
	}
	imgui.OpenPopup("##pcolorstop")
	if imgui.BeginPopup("##pcolorstop") {
		c := lay.ColorRamp[particleColorPick].C
		v := [4]float32{c.X(), c.Y(), c.Z(), 1}
		if imgui.ColorPicker4V("##pcolorpick", &v,
			imgui.ColorPickerFlagsNoAlpha|imgui.ColorPickerFlagsNoLabel) {
			particleBeginEdit("pcolorstop")
			lay.ColorRamp[particleColorPick].C = mgl32.Vec3{v[0], v[1], v[2]}
			particleTouch()
		}
		if imgui.Button("Done##pcolordone") {
			particleColorPick = -1
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	} else {
		particleColorPick = -1
	}
}

// ---------- stop list operations ----------
//
// Written against whichever ramp the active strip names, so the three strips
// share one set rather than each having its own nearly-identical three.

func particleStopCount(lay *particle.Layer) int {
	if particleActiveRamp == rampColor {
		return len(lay.ColorRamp)
	}
	return len(*particleRampFor(lay))
}

func particleStopT(lay *particle.Layer, i int) float32 {
	if particleActiveRamp == rampColor {
		return lay.ColorRamp[i].T
	}
	return (*particleRampFor(lay))[i].T
}

func particleStopV(lay *particle.Layer, i int) float32 {
	if particleActiveRamp == rampColor {
		return 0
	}
	r := *particleRampFor(lay)
	return scaleForDisplay(r, r[i].V)
}

// particleStopIndexAt finds the stop at T, which is how a drag re-locates the
// point it is carrying after a re-sort.
func particleStopIndexAt(lay *particle.Layer, t float32) int {
	const eps = 1e-5
	for i := 0; i < particleStopCount(lay); i++ {
		d := particleStopT(lay, i) - t
		if d < eps && d > -eps {
			return i
		}
	}
	return -1
}

// particleNearestStop returns the stop within grab range of the cursor, or -1.
func particleNearestStop(l particleRampLayout, lay *particle.Layer, mouse imgui.Vec2) int {
	best, bestD := -1, particleGrabPx*particleGrabPx
	for i := 0; i < particleStopCount(lay); i++ {
		x := l.tToX(particleStopT(lay, i))
		var y float32
		if particleActiveRamp == rampColor {
			y = l.y1 - 16 + 7
		} else {
			y = l.vToY(particleStopV(lay, i))
		}
		dx, dy := mouse.X-x, mouse.Y-y
		if d := dx*dx + dy*dy; d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func particleAddStop(lay *particle.Layer, t, v float32) {
	if particleActiveRamp == rampColor {
		lay.ColorRamp = append(lay.ColorRamp, particle.ColorStop{T: t, C: lay.ColorRamp.At(t)})
		particleSortColorRamp(lay)
		return
	}
	r := particleRampFor(lay)
	// The value is taken from the click height, rescaled back out of the
	// strip's normalised space so a size ramp that peaks at 3 keeps its range.
	max := float32(1)
	for _, s := range *r {
		if s.V > max {
			max = s.V
		}
	}
	*r = append(*r, particle.Stop{T: t, V: v * max})
	particleSortRamp(r)
}

func particleMoveStop(lay *particle.Layer, i int, t, v float32) {
	if particleActiveRamp == rampColor {
		lay.ColorRamp[i].T = t
		particleSortColorRamp(lay)
		return
	}
	r := particleRampFor(lay)
	max := float32(1)
	for _, s := range *r {
		if s.V > max {
			max = s.V
		}
	}
	(*r)[i].T = t
	(*r)[i].V = v * max
	particleSortRamp(r)
}

func particleDeleteStop(lay *particle.Layer, i int) {
	if particleActiveRamp == rampColor {
		lay.ColorRamp = append(lay.ColorRamp[:i], lay.ColorRamp[i+1:]...)
		return
	}
	r := particleRampFor(lay)
	*r = append((*r)[:i], (*r)[i+1:]...)
}

// particleSortRamp keeps a ramp ordered by T, which Ramp.At depends on.
func particleSortRamp(r *particle.Ramp) {
	sort.SliceStable(*r, func(a, b int) bool { return (*r)[a].T < (*r)[b].T })
}

func particleSortColorRamp(lay *particle.Layer) {
	sort.SliceStable(lay.ColorRamp, func(a, b int) bool {
		return lay.ColorRamp[a].T < lay.ColorRamp[b].T
	})
}

func clamp01f(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
