package editor

// editor_generator_view2d.go — the Generator editor's top-down 2D view.
//
// It draws whatever the selected stage's preview holds (procgen.PreviewData),
// without knowing what any of it is: a base image layer and an optional
// overlay at an opacity, the point layers (with radii) and line layers over
// them, a metre grid, and a readout of the world position and the base
// layer's value under the cursor. Looking down -Y: +X right, +Z down the
// screen — the convention procgen/preview.go documents.
//
// And it is where splines are edited. Every spline parameter of the selected
// stage, list items included, is drawn; one at a time is ACTIVE (picked in
// the bar above the view, or by the spline's "Edit in view" button in the
// parameter panel) and gets handles:
//
//   - left-click empty space: add a point — inserted into the nearest
//     segment when the click is on one, appended otherwise;
//   - left-drag a handle: move it (one undo step per drag);
//   - right-click a handle: delete it.
//
// Middle-drag (or right-drag away from a handle) pans, the wheel zooms about
// the cursor, and Frame fits the preview.

import (
	"fmt"
	"math"

	"foundry/internal/imguibackend"
	"foundry/internal/procgen"

	"github.com/go-gl/gl/v4.3-core/gl"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// genSplineRef names the spline being edited in the view: a stage and the
// param path to it. The zero value is none.
type genSplineRef struct {
	stage string
	path  genParamPath
}

func (r genSplineRef) valid() bool { return r.stage != "" && len(r.path) > 0 }

func (r genSplineRef) matches(stage string, path genParamPath) bool {
	return r.stage == stage && r.path.key() == path.key()
}

// genSpline is the active spline.
var genSpline genSplineRef

// genV2 is the 2D view's camera and interaction state.
var genV2 = struct {
	cx, cz  float64 // world point at the canvas centre
	scale   float64 // pixels per metre
	framed  bool
	base    int // image layer index drawn first
	overlay int // second image layer, or -1
	alpha   float32
	points  bool
	lines   bool

	dragging  bool
	dragPoint int
	dragID    string
	panning   bool
	rightAt   imgui.Vec2
}{scale: 2, overlay: -1, alpha: 0.5, points: true, lines: true}

// genViewDragging reports a handle drag in progress (the undo latch holds).
func genViewDragging() bool { return genV2.dragging }

// ---------- layer textures ----------

// genLayerTex holds the GL textures of the preview being drawn: one per image
// layer, made when a new preview lands and freed when it is replaced.
var genLayerTex struct {
	data *procgen.PreviewData
	ids  []uint32
}

// genUploadTexture makes a texture of an RGBA image and returns its name. A
// variable so the draw tests, which have no GL, can stand in for it.
var genUploadTexture = func(w, h int, rgba []uint8) uint32 {
	if w <= 0 || h <= 0 || len(rgba) < w*h*4 {
		return 0
	}
	var t uint32
	gl.GenTextures(1, &t)
	gl.BindTexture(gl.TEXTURE_2D, t)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(w), int32(h), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(rgba))
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return t
}

// genDeleteTextures frees textures genUploadTexture made. A variable for the
// same reason.
var genDeleteTextures = func(ids []uint32) {
	for _, t := range ids {
		if t != 0 {
			gl.DeleteTextures(1, &t)
		}
	}
}

// genLayerTextures is the textures for p's image layers, uploading them the
// first time p is drawn.
func genLayerTextures(p *procgen.PreviewData) []uint32 {
	if genLayerTex.data == p {
		return genLayerTex.ids
	}
	genFreeLayerTextures()
	genLayerTex.data = p
	if p == nil {
		return nil
	}
	for _, im := range p.Images {
		genLayerTex.ids = append(genLayerTex.ids, genUploadTexture(im.W, im.H, im.RGBA))
	}
	if !genV2.framed {
		genView2DFrame()
	}
	return genLayerTex.ids
}

func genFreeLayerTextures() {
	genDeleteTextures(genLayerTex.ids)
	genLayerTex.data, genLayerTex.ids = nil, nil
}

// ---------- framing ----------

// genViewBounds is what Frame fits: the preview's bounds, else the active
// spline's, else a 200 m square about the origin.
func genViewBounds() procgen.Rect {
	if p := genCurrentPreview(); p != nil {
		if b := p.FrameBounds(); !b.Empty() {
			return b
		}
	}
	var r procgen.Rect
	for _, sp := range genStageSplines() {
		for _, pt := range sp.value.Points {
			x, z := genSplineXZ(sp.def, pt)
			r = r.Union(procgen.Rect{MinX: x - 10, MinZ: z - 10, MaxX: x + 10, MaxZ: z + 10})
		}
	}
	if r.Empty() {
		r = procgen.Rect{MinX: -100, MinZ: -100, MaxX: 100, MaxZ: 100}
	}
	return r
}

// genView2DFrame fits genViewBounds into the canvas.
func genView2DFrame() {
	b := genViewBounds()
	genV2.cx, genV2.cz = (b.MinX+b.MaxX)/2, (b.MinZ+b.MaxZ)/2
	w, h := genCanvasSize()
	sx := float64(w) / ((b.MaxX - b.MinX) * 1.08)
	sz := float64(h) / ((b.MaxZ - b.MinZ) * 1.08)
	genV2.scale = math.Max(math.Min(sx, sz), 1e-4)
	genV2.framed = true
}

// genCanvasSize is the viewport's size, roughly — Frame can be asked for
// before the canvas has been laid out.
func genCanvasSize() (float32, float32) {
	w := generatorEditorWindow
	if w == nil {
		return 640, 480
	}
	return float32(w.W) - generatorListWidth - generatorPanelWidth - 16,
		float32(w.H) - generatorToolbarH - generatorFooterH - 48
}

// ---------- the viewport window ----------

// generatorViewportWindow is the centre: the 2D view, or a small overlay over
// the 3D view (which is drawn under the UI, and orbits through raw input).
func generatorViewportWindow() {
	w := generatorEditorWindow
	if w == nil {
		return
	}
	x, y := generatorListWidth, generatorToolbarH
	width := float32(w.W) - generatorListWidth - generatorPanelWidth
	height := float32(w.H) - generatorToolbarH - generatorFooterH
	if width < 100 || height < 100 {
		return
	}
	if genViewMode == genView3D {
		genView3DOverlay(x, y)
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: x, Y: y})
	imgui.SetNextWindowSize(imgui.Vec2{X: width, Y: height})
	imgui.BeginV("##genview2d", nil, genPanelFlags|imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoScrollWithMouse)
	genView2DBar()
	genView2DCanvas()
	imgui.End()
}

// genView2DBar is the row above the canvas: layers, the active spline,
// toggles, Frame.
func genView2DBar() {
	p := genCurrentPreview()
	layerName := func(i int) string {
		if p == nil || i < 0 || i >= len(p.Images) {
			return "(none)"
		}
		return p.Images[i].Name
	}
	imgui.SetNextItemWidth(110)
	if imguibackend.BeginCombo("##genbase", layerName(genV2.base)) {
		if p != nil {
			for i := range p.Images {
				if imguibackend.SelectableV(p.Images[i].Name+fmt.Sprintf("##b%d", i), i == genV2.base, 0, imgui.Vec2{}) {
					genV2.base = i
				}
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("The image drawn first.")
	imgui.SameLine()
	imgui.SetNextItemWidth(110)
	if imguibackend.BeginCombo("##genover", layerName(genV2.overlay)) {
		if imguibackend.SelectableV("(none)##ovnone", genV2.overlay < 0, 0, imgui.Vec2{}) {
			genV2.overlay = -1
		}
		if p != nil {
			for i := range p.Images {
				if imguibackend.SelectableV(p.Images[i].Name+fmt.Sprintf("##o%d", i), i == genV2.overlay, 0, imgui.Vec2{}) {
					genV2.overlay = i
				}
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("An image drawn over the first, at the opacity beside it.")
	if genV2.overlay >= 0 {
		imgui.SameLine()
		imgui.SetNextItemWidth(70)
		imgui.SliderFloatV("##genovalpha", &genV2.alpha, 0, 1, "%.2f", 0)
	}
	imgui.SameLine()
	imgui.Checkbox("Points##genpts", &genV2.points)
	imgui.SameLine()
	imgui.Checkbox("Lines##genlines", &genV2.lines)
	imgui.SameLine()
	splines := genStageSplines()
	cur := "(no path)"
	for _, sp := range splines {
		if genSpline.matches(sp.stage, sp.path) {
			cur = sp.label
		}
	}
	imgui.SetNextItemWidth(150)
	if imguibackend.BeginCombo("##genspl", cur) {
		if imguibackend.SelectableV("(no path)##splnone", !genSpline.valid(), 0, imgui.Vec2{}) {
			genSpline = genSplineRef{}
		}
		for _, sp := range splines {
			if imguibackend.SelectableV(sp.label+"##spl"+sp.path.key(), genSpline.matches(sp.stage, sp.path), 0, imgui.Vec2{}) {
				genSpline = genSplineRef{stage: sp.stage, path: sp.path}
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("The path to edit: click to add a point, drag to move, right-click to delete.")
	imgui.SameLine()
	if imgui.Button("Frame##genframe") {
		genView2DFrame()
	}
}

// genStageSpline is one spline param of the selected stage, found anywhere.
type genStageSpline struct {
	stage string
	path  genParamPath
	def   *procgen.ParamDef
	value procgen.Value
	label string
}

// genStageSplines is every spline in the selected stage, list items included.
func genStageSplines() []genStageSpline {
	s := genSelectedStage()
	if s == nil {
		return nil
	}
	k := procgen.KindByName(s.Kind)
	if k == nil {
		return nil
	}
	var out []genStageSpline
	var walk func(defs []procgen.ParamDef, prefix genParamPath, label string)
	walk = func(defs []procgen.ParamDef, prefix genParamPath, label string) {
		for i := range defs {
			d := &defs[i]
			path := append(append(genParamPath(nil), prefix...), genPathStep{Name: d.Name, Item: -1})
			switch d.Type {
			case procgen.ParamSpline:
				v, _ := genValueAt(k.Params, s.Params, path)
				out = append(out, genStageSpline{stage: s.ID, path: path, def: d, value: v, label: label + d.DisplayLabel()})
			case procgen.ParamList:
				v, _ := genValueAt(k.Params, s.Params, path)
				for it := range v.Items {
					child := append(genParamPath(nil), path...)
					child[len(child)-1].Item = it
					walk(d.Fields, child, fmt.Sprintf("%s%s %d: ", label, d.DisplayLabel(), it+1))
				}
			}
		}
	}
	walk(k.Params, nil, "")
	return out
}

// genSplineXZ is a spline point's ground position.
func genSplineXZ(d *procgen.ParamDef, pt []float64) (float64, float64) {
	if d.Spline3D {
		return pt[0], pt[2]
	}
	return pt[0], pt[1]
}

func genSetSplineXZ(d *procgen.ParamDef, pt []float64, x, z float64) {
	pt[0] = x
	if d.Spline3D {
		pt[2] = z
	} else {
		pt[1] = z
	}
}

// genColor packs an RGBA float colour.
func genColor(c [4]float32) imgui.PackedColor {
	return imgui.PackedColorFromVec4(imgui.Vec4{X: c[0], Y: c[1], Z: c[2], W: c[3]})
}

// genView2DCanvas is the drawing and the mouse.
func genView2DCanvas() {
	origin := imgui.CursorScreenPos()
	avail := imgui.ContentRegionAvail()
	if avail.X < 20 || avail.Y < 20 {
		return
	}
	imgui.InvisibleButton("##gencanvas", avail)
	hovered := imgui.IsItemHovered()
	dl := imgui.WindowDrawList()
	end := imgui.Vec2{X: origin.X + avail.X, Y: origin.Y + avail.Y}
	dl.PushClipRect(origin, end)
	defer dl.PopClipRect()
	dl.AddRectFilled(origin, end, imgui.PackedColorFromVec4(imgui.Vec4{X: 0.08, Y: 0.085, Z: 0.10, W: 1}))

	p := genCurrentPreview()
	tex := genLayerTextures(p)
	if !genV2.framed {
		genView2DFrame()
	}
	centre := imgui.Vec2{X: origin.X + avail.X/2, Y: origin.Y + avail.Y/2}
	toScreen := func(x, z float64) imgui.Vec2 {
		return imgui.Vec2{X: centre.X + float32((x-genV2.cx)*genV2.scale), Y: centre.Y + float32((z-genV2.cz)*genV2.scale)}
	}
	toWorld := func(s imgui.Vec2) (float64, float64) {
		return genV2.cx + float64(s.X-centre.X)/genV2.scale, genV2.cz + float64(s.Y-centre.Y)/genV2.scale
	}

	// Images.
	drawImage := func(i int, alpha float32) {
		if p == nil || i < 0 || i >= len(p.Images) || i >= len(tex) || tex[i] == 0 {
			return
		}
		b := p.Images[i].Bounds
		if b.Empty() {
			b = p.FrameBounds()
		}
		dl.AddImageV(imgui.TextureID(tex[i]), toScreen(b.MinX, b.MinZ), toScreen(b.MaxX, b.MaxZ),
			imgui.Vec2{}, imgui.Vec2{X: 1, Y: 1}, genColor([4]float32{1, 1, 1, alpha}))
	}
	drawImage(genV2.base, 1)
	if genV2.overlay != genV2.base {
		drawImage(genV2.overlay, genV2.alpha)
	}
	genDrawGrid(dl, origin, end, toScreen, toWorld)

	// Layers.
	if p != nil && genV2.lines {
		for _, l := range p.Lines {
			col := genColor(l.Color)
			for k := 0; k+1 < len(l.Points); k++ {
				dl.AddLineV(toScreen(l.Points[k][0], l.Points[k][1]), toScreen(l.Points[k+1][0], l.Points[k+1][1]), col, 1.5)
			}
			if l.Closed && len(l.Points) > 2 {
				a, b := l.Points[len(l.Points)-1], l.Points[0]
				dl.AddLineV(toScreen(a[0], a[1]), toScreen(b[0], b[1]), col, 1.5)
			}
		}
	}
	if p != nil && genV2.points {
		genDrawPoints(dl, p, origin, end, toScreen)
	}

	// Splines, and the active one's handles.
	mouse := imgui.MousePos()
	splines := genStageSplines()
	var active *genStageSpline
	for i := range splines {
		sp := &splines[i]
		on := genSpline.matches(sp.stage, sp.path)
		if on {
			active = sp
		}
		col := genColor([4]float32{0.55, 0.85, 1, 0.55})
		if on {
			col = genColor([4]float32{0.35, 0.9, 1, 1})
		}
		for k := 0; k+1 < len(sp.value.Points); k++ {
			ax, az := genSplineXZ(sp.def, sp.value.Points[k])
			bx, bz := genSplineXZ(sp.def, sp.value.Points[k+1])
			dl.AddLineV(toScreen(ax, az), toScreen(bx, bz), col, 2)
		}
	}
	near := -1
	if active != nil {
		for k, pt := range active.value.Points {
			x, z := genSplineXZ(active.def, pt)
			sp := toScreen(x, z)
			c := [4]float32{1, 1, 1, 1}
			if dx, dy := sp.X-mouse.X, sp.Y-mouse.Y; dx*dx+dy*dy < 64 && hovered {
				near = k
				c = [4]float32{1, 0.85, 0.3, 1}
			}
			dl.AddCircleFilled(sp, 5, genColor(c))
			dl.AddCircleV(sp, 5, genColor([4]float32{0, 0, 0, 1}), 12, 1)
		}
	}

	genView2DInput(hovered, active, near, mouse, toScreen, toWorld)

	// The readout.
	if hovered {
		x, z := toWorld(mouse)
		text := fmt.Sprintf("x %.1f  z %.1f", x, z)
		if p != nil && genV2.base >= 0 && genV2.base < len(p.Images) {
			im := &p.Images[genV2.base]
			if v, ok := im.ValueAt(x, z); ok {
				text += fmt.Sprintf("   %s %.3g %s", im.Name, v, im.Unit)
			}
		}
		dl.AddText(imgui.Vec2{X: origin.X + 6, Y: end.Y - 18}, genColor([4]float32{1, 1, 1, 0.9}), text)
	}
	if p == nil {
		msg := "No preview yet."
		if st := genSelectedStage(); st == nil {
			msg = "Add a stage to see it here."
		} else if busy, _ := genPreviewBusy(); busy {
			msg = "Previewing..."
		}
		dl.AddText(imgui.Vec2{X: origin.X + 8, Y: origin.Y + 8}, genColor([4]float32{1, 1, 1, 0.6}), msg)
	}
}

// genView2DInput is the canvas's mouse: pan, zoom, and the active spline's
// handles.
func genView2DInput(hovered bool, active *genStageSpline, near int, mouse imgui.Vec2,
	toScreen func(x, z float64) imgui.Vec2, toWorld func(imgui.Vec2) (float64, float64)) {
	io := imgui.CurrentIO()
	if hovered {
		if _, wy := io.MouseWheel(); wy != 0 {
			bx, bz := toWorld(mouse)
			genV2.scale = math.Min(math.Max(genV2.scale*math.Pow(1.15, float64(wy)), 1e-4), 1e4)
			ax, az := toWorld(mouse)
			genV2.cx += bx - ax
			genV2.cz += bz - az
		}
	}
	// Pan: middle drag, or right drag that did not start on a handle.
	if hovered && (imgui.IsMouseClicked(2) || (imgui.IsMouseClicked(1) && near < 0)) {
		genV2.panning = true
		genV2.rightAt = mouse
	}
	if genV2.panning {
		if imgui.IsMouseDown(2) || imgui.IsMouseDown(1) {
			d := io.MouseDelta()
			genV2.cx -= float64(d.X) / genV2.scale
			genV2.cz -= float64(d.Y) / genV2.scale
		} else {
			genV2.panning = false
		}
	}
	if active == nil {
		genV2.dragging = false
		return
	}
	s := genSelectedStage()
	k := procgen.KindByName(s.Kind)
	id := s.ID + "/view/" + active.path.key()
	write := func(v procgen.Value) {
		genSetValueAt(k.Params, s.Params, active.path, active.def.Clamp(v))
		genAfterEdit()
	}
	switch {
	case hovered && imgui.IsMouseClicked(1) && near >= 0:
		if len(active.value.Points) > active.def.MinItems {
			GeneratorPushUndo()
			v := active.value.Clone()
			v.Points = append(v.Points[:near], v.Points[near+1:]...)
			write(v)
		}
	case hovered && imgui.IsMouseClicked(0) && near >= 0:
		genV2.dragging, genV2.dragPoint, genV2.dragID = true, near, id
		genBeginEdit(id)
	case hovered && imgui.IsMouseClicked(0):
		if active.def.MaxItems > 0 && len(active.value.Points) >= active.def.MaxItems {
			return
		}
		GeneratorPushUndo()
		x, z := toWorld(mouse)
		v := active.value.Clone()
		at := genInsertIndex(active, mouse, toScreen)
		np := active.def.NewPoint()
		// A new point between two takes their channels' average; at an end,
		// its neighbour's.
		if n := len(v.Points); n > 0 {
			a := v.Points[max(at-1, 0)]
			b := v.Points[min(at, n-1)]
			for c := range np {
				np[c] = (a[c] + b[c]) / 2
			}
		}
		genSetSplineXZ(active.def, np, x, z)
		v.Points = append(v.Points[:at], append([][]float64{np}, v.Points[at:]...)...)
		write(v)
		genV2.dragging, genV2.dragPoint, genV2.dragID = true, at, id
		genDragActive = id // this click already pushed
	case genV2.dragging && genV2.dragID == id && imgui.IsMouseDown(0):
		if genV2.dragPoint < len(active.value.Points) {
			x, z := toWorld(mouse)
			v := active.value.Clone()
			genSetSplineXZ(active.def, v.Points[genV2.dragPoint], x, z)
			if !v.Equal(active.value) {
				genBeginEdit(id)
				write(v)
			}
		}
	}
	if !imgui.IsMouseDown(0) {
		genV2.dragging = false
	}
}

// genInsertIndex is where a click at mouse adds a point: into a segment it
// is within 8 px of, else after the end nearer the click.
func genInsertIndex(sp *genStageSpline, mouse imgui.Vec2, toScreen func(x, z float64) imgui.Vec2) int {
	pts := sp.value.Points
	if len(pts) < 2 {
		return len(pts)
	}
	best, bestD := -1, float32(8)
	for k := 0; k+1 < len(pts); k++ {
		ax, az := genSplineXZ(sp.def, pts[k])
		bx, bz := genSplineXZ(sp.def, pts[k+1])
		if d := genSegDist(mouse, toScreen(ax, az), toScreen(bx, bz)); d < bestD {
			best, bestD = k+1, d
		}
	}
	if best >= 0 {
		return best
	}
	fx, fz := genSplineXZ(sp.def, pts[0])
	lx, lz := genSplineXZ(sp.def, pts[len(pts)-1])
	f, l := toScreen(fx, fz), toScreen(lx, lz)
	if genDist2(mouse, f) < genDist2(mouse, l) {
		return 0
	}
	return len(pts)
}

func genDist2(a, b imgui.Vec2) float32 { dx, dy := a.X-b.X, a.Y-b.Y; return dx*dx + dy*dy }

func genSegDist(p, a, b imgui.Vec2) float32 {
	abx, aby := b.X-a.X, b.Y-a.Y
	l2 := abx*abx + aby*aby
	t := float32(0)
	if l2 > 0 {
		t = ((p.X-a.X)*abx + (p.Y-a.Y)*aby) / l2
		t = max(0, min(1, t))
	}
	q := imgui.Vec2{X: a.X + abx*t, Y: a.Y + aby*t}
	return float32(math.Sqrt(float64(genDist2(p, q))))
}

// genDrawGrid draws metre lines at a spacing that keeps them 40+ px apart,
// with the axes brighter, and the spacing in the corner.
func genDrawGrid(dl imgui.DrawList, origin, end imgui.Vec2, toScreen func(x, z float64) imgui.Vec2,
	toWorld func(imgui.Vec2) (float64, float64)) {
	step := math.Pow(10, math.Ceil(math.Log10(40/genV2.scale)))
	if step*genV2.scale/2 >= 40 {
		step /= 2
	}
	x0, z0 := toWorld(origin)
	x1, z1 := toWorld(end)
	minor := genColor([4]float32{1, 1, 1, 0.07})
	axis := genColor([4]float32{1, 1, 1, 0.25})
	for x := math.Floor(x0/step) * step; x <= x1 && (x1-x0)/step < 400; x += step {
		col := minor
		if math.Abs(x) < step/2 {
			col = axis
		}
		dl.AddLine(toScreen(x, z0), toScreen(x, z1), col)
	}
	for z := math.Floor(z0/step) * step; z <= z1 && (z1-z0)/step < 400; z += step {
		col := minor
		if math.Abs(z) < step/2 {
			col = axis
		}
		dl.AddLine(toScreen(x0, z), toScreen(x1, z), col)
	}
	dl.AddText(imgui.Vec2{X: end.X - 70, Y: origin.Y + 4}, genColor([4]float32{1, 1, 1, 0.5}), fmt.Sprintf("grid %g m", step))
}

// genPointVertexBudget is the most vertices the point layers may add to the
// 2D view's draw list a frame. ImGui's draw lists index with 16 bits here
// (the backend has no vertex offset), so one list holds at most 65,536
// vertices: a scatter's twenty thousand dots drawn as circles would
// overflow it.
const genPointVertexBudget = 40000

// genDrawPoints draws the point layers within the vertex budget: only the
// dots on screen, each a small square (eight vertices, anti-aliased), and a footprint
// circle only where its radius is a few pixels or more, with segments to
// match. What did not fit is counted in a line at the bottom; zooming in
// brings it back.
func genDrawPoints(dl imgui.DrawList, p *procgen.PreviewData, origin, end imgui.Vec2, toScreen func(x, z float64) imgui.Vec2) {
	used, skipped := 0, 0
	for _, l := range p.Points {
		col := genColor(l.Color)
		for k, q := range l.Points {
			sp := toScreen(q[0], q[1])
			r := float32(0)
			if k < len(l.Radii) {
				r = float32(l.Radii[k] * genV2.scale)
			}
			reach := max(r, 2)
			if sp.X+reach < origin.X || sp.Y+reach < origin.Y || sp.X-reach > end.X || sp.Y-reach > end.Y {
				continue
			}
			seg := 0
			if r >= 3 {
				seg = int(min(max(r/2, 8), 32))
			}
			// Anti-aliased: a filled quad is 8 vertices, a stroked
			// circle up to 6 a segment.
			cost := 8 + seg*6
			if used+cost > genPointVertexBudget {
				skipped++
				continue
			}
			used += cost
			if seg > 0 {
				dl.AddCircleV(sp, r, col, seg, 1)
			}
			// No rounding: AddRectFilled's default rounds the corners,
			// which is an arc's worth of vertices each.
			dl.AddRectFilledV(imgui.Vec2{X: sp.X - 1.5, Y: sp.Y - 1.5}, imgui.Vec2{X: sp.X + 1.5, Y: sp.Y + 1.5}, col, 0, imgui.DrawCornerFlagsNone)
		}
	}
	if skipped > 0 {
		dl.AddText(imgui.Vec2{X: origin.X + 8, Y: end.Y - 22}, genColor([4]float32{1, 0.85, 0.5, 1}),
			fmt.Sprintf("%d dots not drawn: zoom in", skipped))
	}
}
