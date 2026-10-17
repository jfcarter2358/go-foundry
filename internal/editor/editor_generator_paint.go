package editor

// editor_generator_paint.go — the Generator's 2D-view paint brush
// (docs/terrain-paint/SPEC.md "Editor", docs/guide/36-terrain-paint.md).
//
// A generator's paint layers (procgen.Doc.Paint, their pixels in
// Doc.PaintData) are painted here, over whatever the selected stage's
// preview shows — a terrain's shaded relief is the natural ground to paint
// on. The "Paint" toggle in the view's bar turns the tool on; a second row
// then holds:
//
//   - the layer (the document's declared layers) and New layer…, which
//     declares one (a name, a kind, a resolution) as an undoable document
//     edit;
//   - radius (m), strength, falloff, and the mode: add, erase, smooth;
//   - undo and redo of strokes.
//
// While it is on, the canvas belongs to the brush: a left drag paints, a
// stamp every quarter radius along the mouse's path, straight into the
// layer (the overlay shows it at once, no rerun); middle or right drag
// still pans and the wheel still zooms. [ and ] shrink and grow the brush.
// Splines are not edited while painting.
//
// THE OVERLAY is the active layer's stored tiles, each uploaded as a small
// RGBA texture tinted by the layer's kind (density green, exclude red,
// material amber, water blue, height blue below / red above, direction a
// hue by heading) with alpha from the value, and drawn into the view's own
// draw list. It is editor-only drawing, nothing of the world or material
// passes. A tile is re-uploaded when a stroke wrote it; undo and switching
// layers rebuild them all.
//
// UNDO is the brush's own stack, separate from the document's: each stroke
// (mouse down to mouse up) is one step holding the touched tiles before and
// after (procgen/paint's frozen-tile snapshots, so a step costs pointers,
// not pixels, until something writes the tiles again). Ctrl+Z / Ctrl+Y act
// on it while the tool is on, on the document otherwise. A document undo
// never touches paint (genApplySnapshot keeps the pixels).
//
// COMMITTING. Mouse-up ends the stroke: the step is pushed and the
// document marked edited (genAfterEdit), so the save chip turns dirty —
// its fingerprint folds the paint in (genFingerprint) — and the preview
// reruns. Save (and Save All) writes the changed tiles beside the .fgen
// through procgen.WriteDoc. When the generator's level is open and it has
// run once, the commit (and an undo or redo) also starts a LIVE rerun of
// just the tiles the stroke reached, spliced into the open level in place
// (editor_generator_paint_live.go).
//
// THE SAME BRUSH IN 3D. With the Paint tool on and the generator's level
// open in the level editor's 3D view, the brush paints there too: it
// raycasts onto the generated terrain, draws a ring on the ground and the
// active layer projected onto the terrain, and its strokes share this
// state, this undo stack and the live rerun (editor_generator_paint3d.go).
//
// FEEL: Shift held as a stroke starts erases; [ and ] resize; Ctrl+wheel
// changes strength; the falloff's curve (smooth, linear, sharp, dome) is a
// pen pressed lightly or hard; spacing is how far apart a drag's stamps are
// laid (a fraction of the radius), so a fast drag leaves no gaps. The
// Layer menu renames or deletes the active layer (procgen's paint_edit.go;
// both are document edits, undone with the document).
//
// PRESETS (editor_generator_paint_presets.go): the Preset combo beside the
// layer picks a brush preset, and every stamp then writes each of its
// layers at the brush's strength times the layer's — one stroke, one undo
// step for all of them. The 3D brush shares it, as it shares everything
// here.
//
// FLATTEN (phase W2): on a height layer the mode combo also offers
// flatten. The stroke samples the ground under the brush as it starts —
// the ground_edit's source terrain from the last run plus the layer
// (stages.HeightLayerBase) — and every stamp moves the layer toward the
// offset that brings the ground to that height (paint.ModeFlatten).
//
// RIVERS (phase W3): with a river brush preset (River, Stream) a stroke
// paints no layer — it becomes a river spline in the ground_edit
// (editor_generator_river.go), in the 2D and 3D views alike.
//
// PATHS AND DIRECTION (phase P4, editor_generator_path.go): a path brush
// preset (Rock path, Stepping stones, Dirt trail, Gravel path, Game trail)
// makes a footpath spline the same way; on a direction layer every stamp
// paints the drag's direction.

import (
	"fmt"
	"math"
	"sort"

	"foundry/internal/imguibackend"
	"foundry/internal/procgen"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/stages"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// genPaint is the brush's state.
var genPaint = struct {
	on       bool
	layer    string
	radius   float32
	strength float32
	falloff  float32
	mode     paint.Mode
	curve    paint.Curve
	// spacing is a drag's stamp spacing, a fraction of the radius.
	spacing float32

	// preset is the brush preset's ID ("" paints the active layer alone).
	preset string

	// The stroke in progress: the touched tiles' state before it, per
	// layer, and where the last stamp landed.
	stroking   bool
	before     map[string]map[paint.TileKey]*paint.Tile
	lastX      float64
	lastZ      float64
	strokeLay  string
	strokeMode paint.Mode // the mode, or erase when Shift was held at the start
	// flatValue and flatBase are a flatten stroke's target ground height,
	// sampled under the brush as it starts, and the ground under the layer
	// (stages.HeightLayerBase; nil: none, flatten sets the layer).
	flatValue float64
	flatBase  func(x, z float64) float64
	// dragDir is the drag's unit direction since the last stamp: what a
	// direction layer's stamps paint (phase P4).
	dragDir [2]float64

	// New layer… dialog.
	newOpen bool
	newName string
	newKind int
	newRes  float32

	// Rename layer… dialog.
	renameName string
}{radius: 8, strength: 0.6, falloff: 0.5, spacing: 0.25, newRes: float32(paint.DefaultResolution)}

// genPaintStep is one undoable stroke: one layer's tiles, and the other
// layers a preset stroke wrote.
type genPaintStep struct {
	layer         string
	before, after map[paint.TileKey]*paint.Tile
	more          []genPaintStep
	// doc marks a river brush stroke (editor_generator_river.go): a
	// document edit, undone and redone through the document's stack.
	doc bool
}

// restore puts every layer of the step back to before (or after).
func (st *genPaintStep) restore(after bool) {
	if st.doc {
		if after {
			GeneratorRedo()
		} else {
			GeneratorUndo()
		}
		return
	}
	for _, s := range append([]genPaintStep{*st}, st.more...) {
		if l := genDoc.PaintLayer(s.layer); l != nil {
			if after {
				l.Restore(s.after)
			} else {
				l.Restore(s.before)
			}
		}
	}
}

var (
	genPaintUndoStack boundedStack[genPaintStep]
	genPaintRedoStack boundedStack[genPaintStep]
)

// genPaintActive reports whether the brush owns the canvas.
func genPaintActive() bool { return genPaint.on && genViewMode == genView2D }

// genPaintLayer is the active layer, or nil.
func genPaintLayer() *paint.Layer {
	if genDoc == nil || genPaint.layer == "" {
		return nil
	}
	return genDoc.PaintLayer(genPaint.layer)
}

// genPaintEnsureLayer is the active layer, created in PaintData when it is
// declared but has no pixels yet (a document read from a file that
// declared it gets a layer on load; one declared by hand in the YAML
// editor may not).
func genPaintEnsureLayer() *paint.Layer {
	if l := genPaintLayer(); l != nil {
		return l
	}
	if genDoc == nil {
		return nil
	}
	if def := genDoc.PaintDef(genPaint.layer); def != nil {
		return genDoc.EnsurePaintLayer(*def)
	}
	return nil
}

// genPaintReset forgets the brush's history: a new document.
func genPaintReset() {
	genPaintUndoStack.clear()
	genPaintRedoStack.clear()
	genPaint.stroking, genPaint.before = false, nil
	genPaintFreeOverlay()
	if genDoc != nil && genDoc.PaintDef(genPaint.layer) == nil {
		genPaint.layer = ""
		if len(genDoc.Paint) > 0 {
			genPaint.layer = genDoc.Paint[0].Name
		}
	}
}

// ---------- the bar ----------

// genPaintToggle is the bar's Paint checkbox.
func genPaintToggle() {
	if imgui.Checkbox("Paint##genpaint", &genPaint.on) && genPaint.on && genPaint.layer == "" && genDoc != nil && len(genDoc.Paint) > 0 {
		genPaint.layer = genDoc.Paint[0].Name
	}
	editorMenuTooltip("Paint the generator's paint layers over the preview (stages read them through their paint bands).")
}

// genPaintBar is the second row of the bar while the tool is on.
func genPaintBar() {
	if !genPaint.on || genDoc == nil {
		return
	}
	cur := genOr(genPaint.layer, "(no layer)")
	if def := genDoc.PaintDef(genPaint.layer); def != nil {
		cur = def.Name + "  ·  " + def.Kind
	}
	imgui.SetNextItemWidth(170)
	if imguibackend.BeginCombo("##genpaintlayer", cur) {
		for _, def := range genDoc.Paint {
			if imguibackend.SelectableV(def.Name+"  ·  "+def.Kind+"##pl"+def.Name, def.Name == genPaint.layer, 0, imgui.Vec2{}) &&
				def.Name != genPaint.layer {
				genPaint.layer = def.Name
				genPaintFreeOverlay()
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("The layer the brush paints.")
	imgui.SameLine()
	genPaintPresetCombo()
	imgui.SameLine()
	if imgui.Button("New layer…##genpaintnew") {
		genPaint.newOpen, genPaint.newName = true, ""
		imgui.OpenPopup("##genpaintnewpop")
	}
	genPaintNewLayerPopup()
	imgui.SameLine()
	imgui.SetNextItemWidth(80)
	imgui.DragFloatV("Radius##genpaintr", &genPaint.radius, 0.1, 0.25, 500, "%.1f m", 0)
	editorMenuTooltip("The brush's radius, metres. [ and ] shrink and grow it.")
	imgui.SameLine()
	imgui.SetNextItemWidth(70)
	imgui.SliderFloatV("Strength##genpaints", &genPaint.strength, 0.01, 1, "%.2f", 0)
	editorMenuTooltip("How much one stamp changes (a height layer: metres per stamp).")
	imgui.SameLine()
	imgui.SetNextItemWidth(70)
	imgui.SliderFloatV("Falloff##genpaintf", &genPaint.falloff, 0, 1, "%.2f", 0)
	editorMenuTooltip("The outer fraction of the radius the brush eases out over: 0 a hard edge, 1 soft all the way in.")
	imgui.SameLine()
	imgui.SetNextItemWidth(80)
	if imguibackend.BeginCombo("##genpaintmode", genPaint.mode.String()) {
		modes := []paint.Mode{paint.ModeAdd, paint.ModeErase, paint.ModeSmooth}
		if genPaintIsHeight() {
			modes = append(modes, paint.ModeFlatten)
		}
		for _, m := range modes {
			if imguibackend.SelectableV(m.String(), m == genPaint.mode, 0, imgui.Vec2{}) {
				genPaint.mode = m
			}
		}
		imguibackend.EndCombo()
	}
	if genPaint.mode == paint.ModeFlatten && !genPaintIsHeight() {
		genPaint.mode = paint.ModeAdd
	}
	editorMenuTooltip("add paints up, erase takes away, smooth blurs; on a height layer, flatten levels the ground to the\n" +
		"height under the brush where the stroke starts (a terrace, a path's bed). Hold Shift as a stroke starts to erase.")
	imgui.SameLine()
	imgui.SetNextItemWidth(80)
	if imguibackend.BeginCombo("##genpaintcurve", genPaint.curve.String()) {
		for i := range paint.CurveNames {
			c := paint.Curve(i)
			if imguibackend.SelectableV(c.String(), c == genPaint.curve, 0, imgui.Vec2{}) {
				genPaint.curve = c
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("The falloff's curve: smooth, linear, sharp (a pen pressed lightly: most of the stamp at the centre) or dome (pressed hard: full almost to the rim).")
	imgui.SameLine()
	imgui.SetNextItemWidth(60)
	imgui.SliderFloatV("Spacing##genpaintsp", &genPaint.spacing, 0.05, 1, "%.2f", 0)
	editorMenuTooltip("How far apart a drag lays its stamps, as a fraction of the radius: lower is smoother and slower.")
	imgui.SameLine()
	if imgui.Button("Undo##genpaintundo") {
		genPaintUndo()
	}
	imgui.SameLine()
	if imgui.Button("Redo##genpaintredo") {
		genPaintRedo()
	}
	imgui.SameLine()
	genPaintLayerMenu()
	imgui.SameLine()
	live := !genPaintLive.off
	if imgui.Checkbox("Live##genpaintlive", &live) {
		genPaintLive.off = !live
	}
	if _, why := genLiveTarget(); why != "" && live {
		editorMenuTooltip("Live updates wait: " + why + ".")
	} else if t := genPaintLive.last; t != nil {
		editorMenuTooltip("After each stroke, rerun just the tiles it reached and update the open level in place.\nLast: " + t.String())
	} else {
		editorMenuTooltip("After each stroke, rerun just the tiles it reached and update the open level in place.")
	}
	if genPaint.layer == "" {
		imgui.SameLine()
		imgui.Text("New layer… to start")
	}
}

// genPaintNewLayerPopup declares a new layer.
func genPaintNewLayerPopup() {
	if !imgui.BeginPopup("##genpaintnewpop") {
		return
	}
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("Name##genpaintnewname", &genPaint.newName, 0, nil)
	imgui.SetNextItemWidth(160)
	if imguibackend.BeginCombo("Kind##genpaintnewkind", paint.Kinds[genPaint.newKind]) {
		for i, k := range paint.Kinds {
			if imguibackend.SelectableV(k, i == genPaint.newKind, 0, imgui.Vec2{}) {
				genPaint.newKind = i
			}
			editorMenuTooltip(paint.KindDocs[k])
		}
		imguibackend.EndCombo()
	}
	imgui.SetNextItemWidth(160)
	imgui.DragFloatV("Resolution (m)##genpaintnewres", &genPaint.newRes, 0.01, float32(paint.MinResolution), float32(paint.MaxResolution), "%.3g", 0)
	def := paint.LayerDef{Name: genPaint.newName, Kind: paint.Kinds[genPaint.newKind], Resolution: float64(genPaint.newRes)}
	err := def.Validate()
	if err == nil && genDoc.PaintDef(def.Name) != nil {
		err = fmt.Errorf("there is already a layer %s", def.Name)
	}
	if err != nil && genPaint.newName != "" {
		imgui.Text(err.Error())
	}
	if imgui.Button("Create##genpaintnewok") && err == nil {
		genPaintCreateLayer(def)
		imgui.CloseCurrentPopup()
	}
	imgui.SameLine()
	if imgui.Button("Cancel##genpaintnewcancel") {
		imgui.CloseCurrentPopup()
	}
	imgui.EndPopup()
}

// genPaintCreateLayer declares def (a document edit) and makes it active.
func genPaintCreateLayer(def paint.LayerDef) {
	GeneratorPushUndo()
	genDoc.EnsurePaintLayer(def)
	genPaint.layer = def.Name
	genPaintFreeOverlay()
	genAfterEdit()
	genStatusf("Paint layer %s (%s) added", def.Name, def.Kind)
}

// ---------- strokes ----------

// genPaintStroke is a stamp of the brush's settings at (x, z), in the
// stroke's mode.
func genPaintStroke(x, z float64) paint.Stroke {
	s := paint.Stroke{X: x, Z: z, Radius: float64(genPaint.radius), Strength: float64(genPaint.strength),
		Falloff: float64(genPaint.falloff), Mode: genPaint.strokeMode, Value: 1, Curve: genPaint.curve}
	if s.Mode == paint.ModeFlatten {
		s.Value, s.Base = genPaint.flatValue, genPaint.flatBase
	}
	s.Dir = genPaint.dragDir // a direction layer's: the drag's direction (phase P4)
	return s
}

// genPaintIsHeight reports whether the active layer (no preset) is a
// height layer: the one flatten is offered for.
func genPaintIsHeight() bool {
	def := genDoc.PaintDef(genPaint.layer)
	return genPaint.preset == "" && def != nil && def.Kind == paint.KindHeight
}

// genPaintFlattenBase is the ground under a height layer (from the last
// run's source terrain); a variable so the headless tests can stand a
// ground up without a run.
var genPaintFlattenBase = func(layer string) func(x, z float64) float64 {
	base, _, err := stages.HeightLayerBase(genDoc, procgen.OutDirFor(genDoc, procgen.Options{}), layer)
	if err != nil {
		genStatusf("Flatten: %v", err)
		return nil
	}
	return base
}

// genPaintBeginFlatten samples a flatten stroke's target under (x, z): the
// ground as this layer leaves it there.
func genPaintBeginFlatten(x, z float64) {
	genPaint.flatBase = genPaintFlattenBase(genPaint.layer)
	v := 0.0
	if l := genPaintLayer(); l != nil {
		v = l.Sample(x, z)
	}
	if genPaint.flatBase != nil {
		v += genPaint.flatBase(x, z)
	}
	genPaint.flatValue = v
	genStatusf("Flatten to %.2f m", v)
}

// genPaintShiftHeld reports whether Shift is down (erase as a stroke starts);
// a variable so the headless tests can hold it.
var genPaintShiftHeld = func() bool { return sdl.GetModState()&sdl.KMOD_SHIFT != 0 }

// genPaintBegin starts a stroke at (x, z) and stamps once.
func genPaintBegin(x, z float64) {
	if genPaintSplineBrush() {
		genRiverBegin(x, z) // a river or path brush (editor_generator_river.go, _path.go)
		return
	}
	if genPaintEnsureLayer() == nil {
		return
	}
	genPaint.stroking, genPaint.strokeLay = true, genPaintStrokeKey()
	genPaint.strokeMode = genPaint.mode
	if genPaintShiftHeld() {
		genPaint.strokeMode = paint.ModeErase
	}
	if genPaint.strokeMode == paint.ModeFlatten {
		genPaintBeginFlatten(x, z)
	}
	genPaint.before = map[string]map[paint.TileKey]*paint.Tile{}
	genPaint.lastX, genPaint.lastZ = x, z
	genPaint.dragDir = [2]float64{}
	// A direction layer's stamp takes the drag's direction: a click
	// without motion has none, and paints nothing (phase P4).
	if !genPaintIsDirection() || genPaint.strokeMode != paint.ModeAdd {
		genPaintStampAll(genPaintStroke(x, z))
	}
}

// genPaintStrokeKey names what a stroke paints: the layer, or the preset.
func genPaintStrokeKey() string {
	if genPaint.preset != "" {
		return "preset:" + genPaint.preset
	}
	return genPaint.layer
}

// genPaintStampAll stamps s into the active layer, or into each of the
// preset's layers at its strength.
func genPaintStampAll(s paint.Stroke) {
	if p := genPaintCurrentPreset(); p != nil {
		for _, ps := range p.Stamps(s) {
			if l := genPaintEnsureNamed(ps.Layer); l != nil {
				genPaintStamp(l, ps.Stroke)
			}
		}
		return
	}
	if l := genPaintLayer(); l != nil {
		genPaintStamp(l, s)
	}
}

// genPaintEnsureNamed is a declared layer's pixels, made when missing.
func genPaintEnsureNamed(name string) *paint.Layer {
	if l := genDoc.PaintLayer(name); l != nil {
		return l
	}
	if def := genDoc.PaintDef(name); def != nil {
		return genDoc.EnsurePaintLayer(*def)
	}
	return nil
}

// genPaintStamp snapshots the tiles s will touch (the first time this
// stroke touches each), then stamps.
func genPaintStamp(l *paint.Layer, s paint.Stroke) {
	before := genPaint.before[l.Def.Name]
	if before == nil {
		before = map[paint.TileKey]*paint.Tile{}
		genPaint.before[l.Def.Name] = before
	}
	var fresh []paint.TileKey
	for _, k := range l.TouchedBy(s) {
		if _, seen := before[k]; !seen {
			fresh = append(fresh, k)
		}
	}
	for k, t := range l.Snapshot(fresh) {
		before[k] = t
	}
	for _, k := range l.Apply(s) {
		genPaintOverlay.dirty[k] = true
		genPaint3DOverlay.dirty[k] = true
	}
}

// genPaintDrag continues the stroke to (x, z): a stamp every spacing ×
// radius along the way, so a fast drag leaves no gaps.
func genPaintDrag(x, z float64) {
	if genRiver.stroking {
		genRiverDrag(x, z)
		return
	}
	l := genPaintLayer()
	if !genPaint.stroking || l == nil || genPaintStrokeKey() != genPaint.strokeLay {
		return
	}
	step := math.Max(float64(genPaint.radius)*float64(max(genPaint.spacing, 0.05)), l.Def.Res())
	d := math.Hypot(x-genPaint.lastX, z-genPaint.lastZ)
	if d < step {
		return
	}
	pts := paint.LineStamps(genPaint.lastX, genPaint.lastZ, x, z, step)
	genPaint.dragDir = [2]float64{(x - genPaint.lastX) / d, (z - genPaint.lastZ) / d}
	for _, p := range pts[1:] {
		genPaintStampAll(genPaintStroke(p[0], p[1]))
	}
	genPaint.lastX, genPaint.lastZ = x, z
}

// genPaintEnd commits the stroke: one undo step, the document edited.
func genPaintEnd() {
	if genRiver.stroking {
		genRiverEnd()
		return
	}
	if !genPaint.stroking {
		return
	}
	genPaint.stroking = false
	// One step for every layer the stroke wrote, the active layer first.
	names := make([]string, 0, len(genPaint.before))
	for n := range genPaint.before {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == genPaint.layer) != (names[j] == genPaint.layer) {
			return names[i] == genPaint.layer
		}
		return names[i] < names[j]
	})
	var steps []genPaintStep
	for _, n := range names {
		l, before := genDoc.PaintLayer(n), genPaint.before[n]
		if l == nil || len(before) == 0 {
			continue
		}
		keys := make([]paint.TileKey, 0, len(before))
		for k := range before {
			keys = append(keys, k)
		}
		steps = append(steps, genPaintStep{layer: n, before: before, after: l.Snapshot(keys)})
	}
	if len(steps) == 0 {
		genPaint.before = nil
		return
	}
	st := steps[0]
	st.more = steps[1:]
	genPaintUndoStack.push(st)
	genPaintRedoStack.clear()
	genPaint.before = nil
	genAfterEdit()
	genPaintLiveKick()
}

// genPaintUndo takes the last stroke back.
func genPaintUndo() {
	st, ok := genPaintUndoStack.pop()
	if !ok || genDoc == nil {
		return
	}
	st.restore(false)
	genPaintRedoStack.push(st)
	genPaintFreeOverlay()
	genAfterEdit()
	genPaintLiveKick()
	genStatus = "Undo stroke"
}

// genPaintRedo puts it back.
func genPaintRedo() {
	st, ok := genPaintRedoStack.pop()
	if !ok || genDoc == nil {
		return
	}
	st.restore(true)
	genPaintUndoStack.push(st)
	genPaintFreeOverlay()
	genAfterEdit()
	genPaintLiveKick()
	genStatus = "Redo stroke"
}

// genPaintKey handles the brush's keys; true when it took the key.
func genPaintKey(ctrl bool, sym int) bool {
	if !genPaintActive() {
		return false
	}
	switch {
	case ctrl && sym == 'z' && genPaintShiftHeld():
		genPaintRedo()
	case ctrl && sym == 'z':
		genPaintUndo()
	case ctrl && sym == 'y':
		genPaintRedo()
	case !ctrl && sym == '[':
		genPaint.radius = float32(math.Max(0.25, float64(genPaint.radius)/1.2))
	case !ctrl && sym == ']':
		genPaint.radius = float32(math.Min(500, float64(genPaint.radius)*1.2))
	default:
		return false
	}
	return true
}

// ---------- the canvas ----------

// genPaintCanvas draws the overlay and the brush, and takes the left
// button while the tool is on. Called by the 2D canvas after the images.
func genPaintCanvas(dl imgui.DrawList, hovered bool, mouse imgui.Vec2,
	toScreen func(x, z float64) imgui.Vec2, toWorld func(imgui.Vec2) (float64, float64)) {
	if !genPaintActive() {
		genPaintEnd() // the tool turned off mid-stroke
		return
	}
	genPaintDrawOverlay(dl, toScreen)
	genRiverDrawStroke(dl, toScreen)
	x, z := toWorld(mouse)
	if hovered {
		r := float32(float64(genPaint.radius) * genV2.scale)
		dl.AddCircleV(mouse, r, genColor([4]float32{1, 1, 1, 0.9}), 48, 1.5)
		if f := genPaint.falloff; f > 0 && f < 1 {
			dl.AddCircleV(mouse, r*(1-f), genColor([4]float32{1, 1, 1, 0.35}), 48, 1)
		}
	}
	switch {
	case hovered && imgui.IsMouseClicked(0) && (genPaint.layer != "" || genPaintSplineBrush()):
		genPaintBegin(x, z)
	case genPaint.stroking && imgui.IsMouseDown(0):
		genPaintDrag(x, z)
	case genPaint.stroking:
		genPaintEnd()
	}
}

// genPaintOverlay caches the active layer's tile textures.
var genPaintOverlay = struct {
	layer string
	tex   map[paint.TileKey]uint32
	dirty map[paint.TileKey]bool
}{tex: map[paint.TileKey]uint32{}, dirty: map[paint.TileKey]bool{}}

// genPaintFreeOverlay drops every tile texture (they are rebuilt as drawn),
// the 3D view's with them.
func genPaintFreeOverlay() {
	genPaint3DFreeOverlay()
	ids := make([]uint32, 0, len(genPaintOverlay.tex))
	for _, t := range genPaintOverlay.tex {
		ids = append(ids, t)
	}
	genDeleteTextures(ids)
	genPaintOverlay.tex = map[paint.TileKey]uint32{}
	genPaintOverlay.dirty = map[paint.TileKey]bool{}
	genPaintOverlay.layer = ""
}

// genPaintDrawOverlay draws the active layer's tiles.
func genPaintDrawOverlay(dl imgui.DrawList, toScreen func(x, z float64) imgui.Vec2) {
	l := genPaintLayer()
	if genPaintOverlay.layer != genPaint.layer {
		genPaintFreeOverlay()
		genPaintOverlay.layer = genPaint.layer
	}
	if l == nil {
		return
	}
	// Tiles the layer no longer stores (erased away).
	for k, t := range genPaintOverlay.tex {
		if l.Tile(k) == nil {
			genDeleteTextures([]uint32{t})
			delete(genPaintOverlay.tex, k)
		}
	}
	for _, k := range l.Keys() {
		t, ok := genPaintOverlay.tex[k]
		if !ok || genPaintOverlay.dirty[k] {
			if ok {
				genDeleteTextures([]uint32{t})
			}
			tile := l.Tile(k)
			t = genUploadTexture(tile.N, tile.N, genPaintTileRGBA(l.Def, tile))
			genPaintOverlay.tex[k] = t
			delete(genPaintOverlay.dirty, k)
		}
		if t == 0 {
			continue
		}
		r := k.Rect()
		dl.AddImageV(imgui.TextureID(t), toScreen(r[0], r[1]), toScreen(r[2], r[3]),
			imgui.Vec2{}, imgui.Vec2{X: 1, Y: 1}, genColor([4]float32{1, 1, 1, 1}))
	}
}

// genPaintTileRGBA is a tile as the overlay draws it: the kind's tint, alpha
// from the value.
func genPaintTileRGBA(def paint.LayerDef, t *paint.Tile) []uint8 {
	n := t.N
	out := make([]uint8, 4*n*n)
	tint := map[string][3]float64{
		paint.KindDensity: {0.35, 1, 0.35}, paint.KindExclude: {1, 0.3, 0.25},
		paint.KindMaterial: {1, 0.7, 0.2}, paint.KindWater: {0.3, 0.6, 1},
	}
	for k := 0; k < n*n; k++ {
		var c [3]float64
		var a float64
		switch def.Kind {
		case paint.KindHeight:
			v := float64(t.V[k])
			a = math.Min(math.Abs(v)/4, 1)
			c = [3]float64{1, 0.35, 0.3}
			if v < 0 {
				c = [3]float64{0.3, 0.5, 1}
			}
		case paint.KindDirection:
			dx, dz := float64(t.V[2*k]), float64(t.V[2*k+1])
			a = math.Min(math.Hypot(dx, dz), 1)
			h := math.Atan2(dz, dx)
			c = [3]float64{0.5 + 0.5*math.Cos(h), 0.5 + 0.5*math.Cos(h-2.094), 0.5 + 0.5*math.Cos(h+2.094)}
		default:
			a = float64(t.V[k])
			c = tint[def.Kind]
		}
		out[4*k] = uint8(c[0] * 255)
		out[4*k+1] = uint8(c[1] * 255)
		out[4*k+2] = uint8(c[2] * 255)
		out[4*k+3] = uint8(math.Min(a, 1) * 0.65 * 255)
	}
	return out
}

// genFingerprint is the save chip's fingerprint: the document as its file
// stores it, and its paint.
func genFingerprint() uint64 {
	if genDoc == nil {
		return 0
	}
	return procgen.Fingerprint(genDoc) ^ genDoc.PaintData.Fingerprint()*0x9e3779b97f4a7c15
}

// ---------- the mask param ----------

// genMaskCombo is a mask param's widget: the document's layers, (none),
// and the kind beside each name; then the param's words (phase P4's
// orient_to: downhill, paths), which name no layer.
func genMaskCombo(label string, v *procgen.Value, words ...string) bool {
	changed := false
	shown := genOr(v.Str, "(none)")
	isWord := false
	for _, w := range words {
		isWord = isWord || w == v.Str
	}
	if v.Str != "" && !isWord {
		if def := genDoc.PaintDef(v.Str); def != nil {
			shown = def.Name + "  ·  " + def.Kind
		} else {
			shown = v.Str + "  (not declared)"
		}
	}
	if imguibackend.BeginCombo(label, shown) {
		if imguibackend.SelectableV("(none)", v.Str == "", 0, imgui.Vec2{}) && v.Str != "" {
			v.Str = ""
			changed = true
		}
		for _, def := range genDoc.Paint {
			if imguibackend.SelectableV(def.Name+"  ·  "+def.Kind+"##m"+def.Name, def.Name == v.Str, 0, imgui.Vec2{}) && def.Name != v.Str {
				v.Str = def.Name
				changed = true
			}
		}
		for _, w := range words {
			if imguibackend.SelectableV(w+"##mw"+w, w == v.Str, 0, imgui.Vec2{}) && w != v.Str {
				v.Str = w
				changed = true
			}
		}
		if len(genDoc.Paint) == 0 && len(words) == 0 {
			imgui.Text("No paint layers: add one with Paint > New layer… in the 2D view")
		}
		imguibackend.EndCombo()
	}
	return changed
}
