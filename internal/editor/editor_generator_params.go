package editor

// editor_generator_params.go — the stage parameter panel, built from the
// kind's schema.
//
// One widget per procgen.ParamDef by its type, with its range, default and
// tooltip; a parameter still at its default shows a dim "default" and is not
// in the file, and the x beside a changed one resets it. Nothing here knows
// what any stage's parameter means, so a new kind or a new parameter is a
// row in its schema and no UI work — material_panel's rule, carried over.
//
// The composite types:
//
//   - spline: a point table (position and the per-point channels), plus
//     "Edit in view", which hands the spline to the top-down 2D view for
//     click-to-add, drag-to-move, right-click-to-delete
//     (editor_generator_view2d.go). Splines inside list items are reached
//     the same way, by a param PATH (genParamPath).
//   - curve: a small graph to drag points on (click empty space to add,
//     right-click a point to remove), with the numbers under it.
//   - list: a tree node per item, each item its own panel of the list's
//     Fields, with add, remove and reorder.
//
// Every edit goes through genSetParam, which writes the value at its path
// into the stage (materialising a defaulted list first) and reports it.

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"time"

	"foundry/internal/imguibackend"
	"foundry/internal/procgen"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// genPathStep is one step into a stage's parameters: a param by name, and,
// for a list, which item to descend into (Item < 0 means the param itself).
type genPathStep struct {
	Name string
	Item int
}

// genParamPath addresses a parameter anywhere in a stage, list items included.
type genParamPath []genPathStep

func (p genParamPath) key() string {
	s := ""
	for _, st := range p {
		s += st.Name + "[" + strconv.Itoa(st.Item) + "]"
	}
	return s
}

// child is the path to a field of item i of the list at p.
func (p genParamPath) child(item int, field string) genParamPath {
	out := append(genParamPath(nil), p...)
	out[len(out)-1].Item = item
	return append(out, genPathStep{Name: field, Item: -1})
}

// genDefAt is the def a path names, or nil.
func genDefAt(defs []procgen.ParamDef, path genParamPath) *procgen.ParamDef {
	var d *procgen.ParamDef
	for i, st := range path {
		d = nil
		for k := range defs {
			if defs[k].Name == st.Name {
				d = &defs[k]
			}
		}
		if d == nil {
			return nil
		}
		if i+1 < len(path) {
			defs = d.Fields
		}
	}
	return d
}

// genValueAt is the value at path in p — the document's own, or the default
// when the document does not set it — and whether the document sets it.
func genValueAt(defs []procgen.ParamDef, p procgen.Params, path genParamPath) (procgen.Value, bool) {
	d := genDefAt(defs, path[:1])
	if d == nil {
		return procgen.Value{}, false
	}
	v, set := p[d.Name]
	if !set || v.Type != d.Type {
		v, set = d.Default, false
	}
	if len(path) == 1 {
		return v, set
	}
	i := path[0].Item
	if i < 0 || i >= len(v.Items) {
		return procgen.Value{}, false
	}
	// A field the item does not hold reads its default, one level down.
	return genValueAt(d.Fields, v.Items[i], path[1:])
}

// genSetValueAt writes v at path into p, materialising a defaulted list (and
// a missing field of an item) on the way down.
func genSetValueAt(defs []procgen.ParamDef, p procgen.Params, path genParamPath, v procgen.Value) {
	d := genDefAt(defs, path[:1])
	if d == nil {
		return
	}
	if len(path) == 1 {
		p[d.Name] = v
		return
	}
	cur, ok := p[d.Name]
	if !ok || cur.Type != d.Type {
		cur = d.Default.Clone()
	}
	i := path[0].Item
	if i < 0 || i >= len(cur.Items) {
		return
	}
	if cur.Items[i] == nil {
		cur.Items[i] = procgen.Params{}
	}
	genSetValueAt(d.Fields, cur.Items[i], path[1:], v)
	p[d.Name] = cur
}

// genClearValueAt resets the value at path to its default: deleted from the
// stage at the top level, set to the field's default inside an item.
func genClearValueAt(defs []procgen.ParamDef, p procgen.Params, path genParamPath) {
	if len(path) == 1 {
		delete(p, path[0].Name)
		return
	}
	if d := genDefAt(defs, path); d != nil {
		genSetValueAt(defs, p, path, d.Default.Clone())
	}
}

// genParamsPanel draws the selected stage's parameters.
func genParamsPanel(s *procgen.Stage, k *procgen.StageKind) {
	if s.Params == nil {
		s.Params = procgen.Params{}
	}
	for i := range k.Params {
		genParamRow(s, k, genParamPath{{Name: k.Params[i].Name, Item: -1}}, &k.Params[i])
	}
}

// genParamRow is one parameter: its widget, its tooltip, its reset.
func genParamRow(s *procgen.Stage, k *procgen.StageKind, path genParamPath, def *procgen.ParamDef) {
	imgui.PushID(path.key())
	defer imgui.PopID()
	cur, set := genValueAt(k.Params, s.Params, path)
	if set && cur.Equal(def.Default) {
		set = false
	}
	label := def.DisplayLabel()
	id := s.ID + "/" + path.key()

	nv := cur.Clone()
	switch def.Type {
	case procgen.ParamSpline:
		genSplineWidget(s, k, path, def, label, &nv, id)
	case procgen.ParamCurve:
		genCurveWidget(def, label, &nv, id)
	case procgen.ParamList:
		genListWidget(s, k, path, def, label, cur, id)
		genResetButton(s, k, path, set, def)
		return
	default:
		if genScalarWidget(def, label, &nv, s, k) {
			genBeginEdit(id)
			genSetValueAt(k.Params, s.Params, path, def.Clamp(nv))
			genAfterEdit()
		}
		if imgui.IsItemHovered() && def.Doc != "" {
			imgui.SetTooltip(wrapTooltip(def.Doc))
		}
		genResetButton(s, k, path, set, def)
		return
	}
	if !nv.Equal(cur) {
		genBeginEdit(id)
		genSetValueAt(k.Params, s.Params, path, def.Clamp(nv))
		genAfterEdit()
	}
	genResetButton(s, k, path, set, def)
}

// genResetButton is the x (or the dim "default") after a param's widget.
func genResetButton(s *procgen.Stage, k *procgen.StageKind, path genParamPath, set bool, def *procgen.ParamDef) {
	imgui.SameLine()
	if set {
		if imgui.Button("x##reset") {
			GeneratorPushUndo()
			genClearValueAt(k.Params, s.Params, path)
			genAfterEdit()
		}
		editorMenuTooltip("Back to the default, and out of the saved file.")
	} else {
		editorTextMuted("default")
	}
}

// genF32 is a float64 through a float32 widget and back, rounded to the
// shortest decimal that is the same float32 — so dragging a value to 0.008
// stores 0.008 and not 0.00800000037997961.
func genF32(f float32) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(float64(f), 'g', -1, 32), 64)
	return v
}

// genDragSpeed is a drag's speed: the def's Step, or a hundredth of its
// range, or a tenth for an unbounded one.
func genDragSpeed(def *procgen.ParamDef) float32 {
	if def.Step > 0 {
		return float32(def.Step)
	}
	if def.Max > def.Min {
		return float32(math.Max((def.Max-def.Min)/400, 1e-4))
	}
	return 0.1
}

func genRange(def *procgen.ParamDef) (float32, float32) {
	if def.Min == def.Max {
		return -1e9, 1e9
	}
	return float32(def.Min), float32(def.Max)
}

// genScalarWidget is every one-widget type. Reports whether it changed.
func genScalarWidget(def *procgen.ParamDef, label string, v *procgen.Value, s *procgen.Stage, k *procgen.StageKind) bool {
	imgui.SetNextItemWidth(170)
	lo, hi := genRange(def)
	switch def.Type {
	case procgen.ParamFloat:
		f := float32(v.Num)
		if imgui.DragFloatV(label, &f, genDragSpeed(def), lo, hi, "%.4g", imgui.SliderFlagsAlwaysClamp) {
			v.Num = genF32(f)
			return true
		}
	case procgen.ParamInt:
		n := int32(v.Num)
		ilo, ihi := int32(math.Max(float64(lo), -2e9)), int32(math.Min(float64(hi), 2e9))
		speed := genDragSpeed(def)
		if def.Step == 0 {
			speed = float32(math.Max(float64(speed), 0.1))
		}
		if imgui.DragIntV(label, &n, speed, ilo, ihi, "%d", imgui.SliderFlagsAlwaysClamp) {
			v.Num = float64(n)
			return true
		}
	case procgen.ParamSeed:
		n := int32(v.Num)
		imgui.SetNextItemWidth(120)
		changed := imgui.InputIntV(label, &n, 1, 100, 0)
		if changed {
			v.Num = float64(n)
		}
		imgui.SameLine()
		if imgui.Button("Dice##dice") {
			v.Num = float64(genRandomSeed())
			changed = true
		}
		return changed
	case procgen.ParamBool:
		b := v.Bool
		if imgui.Checkbox(label, &b) {
			v.Bool = b
			return true
		}
	case procgen.ParamEnum:
		if imguibackend.BeginCombo(label, v.Str) {
			changed := false
			for _, c := range def.Choices {
				if imguibackend.SelectableV(c, c == v.Str, 0, imgui.Vec2{}) && c != v.Str {
					v.Str = c
					changed = true
				}
			}
			imguibackend.EndCombo()
			return changed
		}
	case procgen.ParamVec2:
		xy := [2]float32{float32(v.Vec[0]), float32(v.Vec[1])}
		if imgui.DragFloat2V(label, &xy, genDragSpeed(def), lo, hi, "%.4g", 0) {
			v.Vec[0], v.Vec[1] = genF32(xy[0]), genF32(xy[1])
			return true
		}
	case procgen.ParamVec3:
		xyz := [3]float32{float32(v.Vec[0]), float32(v.Vec[1]), float32(v.Vec[2])}
		if imgui.DragFloat3V(label, &xyz, genDragSpeed(def), lo, hi, "%.4g", 0) {
			v.Vec[0], v.Vec[1], v.Vec[2] = genF32(xyz[0]), genF32(xyz[1]), genF32(xyz[2])
			return true
		}
	case procgen.ParamColor:
		rgb := [3]float32{float32(v.Vec[0]), float32(v.Vec[1]), float32(v.Vec[2])}
		if imgui.ColorEdit3V(label, &rgb, imgui.ColorEditFlagsNoInputs) {
			v.Vec[0], v.Vec[1], v.Vec[2] = genF32(rgb[0]), genF32(rgb[1]), genF32(rgb[2])
			return true
		}
	case procgen.ParamTexture:
		tex := v.Str
		if textureCombo(label, &tex, true) {
			v.Str = tex
			return true
		}
	case procgen.ParamString:
		str := v.Str
		if imgui.InputTextV(label, &str, 0, nil) && str != v.Str {
			v.Str = str
			return true
		}
	case procgen.ParamStage:
		return genStageRefCombo(def, label, v, s)
	}
	return false
}

// genStageRefCombo offers the stages above s of an acceptable kind.
func genStageRefCombo(def *procgen.ParamDef, label string, v *procgen.Value, s *procgen.Stage) bool {
	changed := false
	if imguibackend.BeginCombo(label, genOr(v.Str, "(none)")) {
		if imguibackend.SelectableV("(none)", v.Str == "", 0, imgui.Vec2{}) && v.Str != "" {
			v.Str = ""
			changed = true
		}
		for i := range genDoc.Stages {
			up := &genDoc.Stages[i]
			if up == s || up.ID == s.ID {
				break // only stages above this one
			}
			if len(def.StageKinds) > 0 && !genContains(def.StageKinds, up.Kind) {
				continue
			}
			if imguibackend.SelectableV(up.ID+"  ·  "+up.Kind, up.ID == v.Str, 0, imgui.Vec2{}) && up.ID != v.Str {
				v.Str = up.ID
				changed = true
			}
		}
		imguibackend.EndCombo()
	}
	return changed
}

func genContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// genRandomSeed is a fresh seed for the dice buttons.
func genRandomSeed() int64 {
	return rand.New(rand.NewSource(time.Now().UnixNano())).Int63n(1 << 30)
}

// genForceTreesOpen opens every spline and list node, for the draw test,
// which must reach the widgets inside them.
var genForceTreesOpen bool

func genTreeOpenHook() {
	if genForceTreesOpen {
		imgui.SetNextItemOpen(true, imgui.ConditionAlways)
	}
}

// ---------- spline ----------

// genSplineWidget is a spline's point table and its "edit in view" toggle.
func genSplineWidget(s *procgen.Stage, k *procgen.StageKind, path genParamPath, def *procgen.ParamDef,
	label string, v *procgen.Value, id string) {
	genTreeOpenHook()
	open := imgui.TreeNodeV(fmt.Sprintf("%s (%d points)##spl", label, len(v.Points)), 0)
	if imgui.IsItemHovered() && def.Doc != "" {
		imgui.SetTooltip(wrapTooltip(def.Doc))
	}
	imgui.SameLine()
	editing := genSpline.matches(s.ID, path)
	if editorActiveToggleButton("Edit in view##splview", editing) {
		if editing {
			genSpline = genSplineRef{}
		} else {
			genSpline = genSplineRef{stage: s.ID, path: append(genParamPath(nil), path...)}
			genViewMode = genView2D
		}
	}
	editorMenuTooltip("Edit this path in the top-down view: click to add a point, drag one to\nmove it, right-click one to delete it.")
	if !open {
		return
	}
	keys := def.PointKeys()
	remove := -1
	for i := range v.Points {
		imgui.PushID(fmt.Sprintf("pt%d", i))
		pt := v.Points[i]
		for c := range pt {
			if c > 0 {
				imgui.SameLine()
			}
			f := float32(pt[c])
			imgui.SetNextItemWidth(58)
			if imgui.DragFloatV("##c"+strconv.Itoa(c), &f, 0.25, -1e6, 1e6, "%.4g", 0) {
				pt[c] = genF32(f)
			}
			if imgui.IsItemHovered() && c < len(keys) {
				imgui.SetTooltip(keys[c])
			}
		}
		imgui.SameLine()
		if imgui.Button("x##rmpt") {
			remove = i
		}
		imgui.PopID()
	}
	if remove >= 0 {
		v.Points = append(v.Points[:remove], v.Points[remove+1:]...)
	}
	if def.MaxItems == 0 || len(v.Points) < def.MaxItems {
		if imgui.Button("Add point##addpt") {
			np := def.NewPoint()
			if n := len(v.Points); n > 0 {
				copy(np, v.Points[n-1])
				np[0] += 10
			}
			v.Points = append(v.Points, np)
		}
	}
	editorTextMuted("columns: " + fmt.Sprint(keys))
	imgui.TreePop()
}

// ---------- curve ----------

// genCurveDrag is the curve point being dragged: its widget ID and index.
var genCurveDrag struct {
	id    string
	point int
}

const (
	genCurveW = float32(240)
	genCurveH = float32(110)
)

// genCurveWidget is a little graph of a curve with draggable points.
func genCurveWidget(def *procgen.ParamDef, label string, v *procgen.Value, id string) {
	imgui.Text(label)
	if imgui.IsItemHovered() && def.Doc != "" {
		imgui.SetTooltip(wrapTooltip(def.Doc))
	}
	xlo, xhi := def.CurveDomain()
	ylo, yhi := def.Min, def.Max
	if ylo == yhi {
		ylo, yhi = 0, 1
		for _, p := range v.Points {
			ylo, yhi = math.Min(ylo, p[1]), math.Max(yhi, p[1])
		}
	}
	origin := imgui.CursorScreenPos()
	size := imgui.Vec2{X: genCurveW, Y: genCurveH}
	imgui.InvisibleButton("##curve", size)
	hovered, active := imgui.IsItemHovered(), imgui.IsItemActive()
	dl := imgui.WindowDrawList()
	end := imgui.Vec2{X: origin.X + size.X, Y: origin.Y + size.Y}
	dl.AddRectFilled(origin, end, imgui.PackedColorFromVec4(imgui.Vec4{X: 0.13, Y: 0.14, Z: 0.17, W: 1}))
	dl.AddRect(origin, end, imgui.PackedColorFromVec4(imgui.Vec4{X: 0.3, Y: 0.3, Z: 0.35, W: 1}))
	toScreen := func(x, y float64) imgui.Vec2 {
		return imgui.Vec2{
			X: origin.X + float32((x-xlo)/(xhi-xlo))*size.X,
			Y: origin.Y + size.Y - float32((y-ylo)/math.Max(yhi-ylo, 1e-9))*size.Y,
		}
	}
	fromScreen := func(p imgui.Vec2) (float64, float64) {
		x := xlo + float64((p.X-origin.X)/size.X)*(xhi-xlo)
		y := ylo + float64((origin.Y+size.Y-p.Y)/size.Y)*(yhi-ylo)
		return noiseClamp(x, xlo, xhi), noiseClamp(y, ylo, yhi)
	}
	// The curve, sampled.
	line := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.9, Y: 0.75, Z: 0.4, W: 1})
	const steps = 48
	prev := toScreen(xlo, v.CurveAt(xlo))
	for i := 1; i <= steps; i++ {
		x := xlo + (xhi-xlo)*float64(i)/steps
		p := toScreen(x, v.CurveAt(x))
		dl.AddLineV(prev, p, line, 2)
		prev = p
	}
	// The points, and the mouse.
	mouse := imgui.MousePos()
	near := -1
	for i, p := range v.Points {
		sp := toScreen(p[0], p[1])
		col := imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1}
		if dx, dy := sp.X-mouse.X, sp.Y-mouse.Y; dx*dx+dy*dy < 49 {
			near = i
			col = imgui.Vec4{X: 1, Y: 0.85, Z: 0.3, W: 1}
		}
		dl.AddCircleFilled(sp, 4, imgui.PackedColorFromVec4(col))
	}
	switch {
	case active && imgui.IsMouseClicked(0):
		if near >= 0 {
			genCurveDrag.id, genCurveDrag.point = id, near
		} else {
			x, y := fromScreen(mouse)
			v.Points = append(v.Points, []float64{x, y})
			genSortCurve(v)
			genCurveDrag.id = id
			for i, p := range v.Points {
				if p[0] == x && p[1] == y {
					genCurveDrag.point = i
				}
			}
		}
	case active && genCurveDrag.id == id && genCurveDrag.point < len(v.Points):
		x, y := fromScreen(mouse)
		v.Points[genCurveDrag.point] = []float64{x, y}
		// Keep the dragged point's identity through the sort.
		genSortCurve(v)
		for i, p := range v.Points {
			if p[0] == x && p[1] == y {
				genCurveDrag.point = i
			}
		}
	case hovered && imgui.IsMouseClicked(1) && near >= 0 && len(v.Points) > def.MinItems:
		v.Points = append(v.Points[:near], v.Points[near+1:]...)
	}
	if !active && genCurveDrag.id == id {
		genCurveDrag.id = ""
	}
	if hovered {
		x, y := fromScreen(mouse)
		imgui.SetTooltip(fmt.Sprintf("x %.3g  y %.3g\nclick: add  drag: move  right-click: remove", x, y))
	}
}

func genSortCurve(v *procgen.Value) {
	for i := 1; i < len(v.Points); i++ {
		for j := i; j > 0 && v.Points[j][0] < v.Points[j-1][0]; j-- {
			v.Points[j], v.Points[j-1] = v.Points[j-1], v.Points[j]
		}
	}
}

func noiseClamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }

// ---------- list ----------

// genListWidget is a list's items, each a sub-panel of the list's Fields.
func genListWidget(s *procgen.Stage, k *procgen.StageKind, path genParamPath, def *procgen.ParamDef,
	label string, cur procgen.Value, id string) {
	genTreeOpenHook()
	open := imgui.TreeNodeV(fmt.Sprintf("%s (%d)##list", label, len(cur.Items)), 0)
	if imgui.IsItemHovered() && def.Doc != "" {
		imgui.SetTooltip(wrapTooltip(def.Doc))
	}
	if !open {
		return
	}
	defer imgui.TreePop()
	write := func(v procgen.Value) {
		GeneratorPushUndo()
		genSetValueAt(k.Params, s.Params, path, v)
		genAfterEdit()
	}
	for i := range cur.Items {
		imgui.PushID(fmt.Sprintf("item%d", i))
		// An asset list (Poly Haven IDs) shows each item's thumbnail and ID.
		itemLabel := genAssetItemThumb(def, cur.Items[i], i)
		itemOpen := imgui.TreeNodeV(itemLabel+"##it", imgui.TreeNodeFlagsDefaultOpen)
		imgui.SameLine()
		act := ""
		if i > 0 && imgui.Button("^##up") {
			act = "up"
		}
		imgui.SameLine()
		if i+1 < len(cur.Items) && imgui.Button("v##down") {
			act = "down"
		}
		imgui.SameLine()
		if imgui.Button("x##rm") {
			act = "rm"
		}
		if act != "" {
			v := cur.Clone()
			switch act {
			case "up":
				v.Items[i-1], v.Items[i] = v.Items[i], v.Items[i-1]
			case "down":
				v.Items[i+1], v.Items[i] = v.Items[i], v.Items[i+1]
			case "rm":
				v.Items = append(v.Items[:i], v.Items[i+1:]...)
			}
			write(v)
			if itemOpen {
				imgui.TreePop()
			}
			imgui.PopID()
			return // the items moved under us; draw them next frame
		}
		if itemOpen {
			for f := range def.Fields {
				genParamRow(s, k, path.child(i, def.Fields[f].Name), &def.Fields[f])
			}
			imgui.TreePop()
		}
		imgui.PopID()
	}
	if def.MaxItems == 0 || len(cur.Items) < def.MaxItems {
		if imgui.Button("Add item##additem") {
			v := cur.Clone()
			v.Items = append(v.Items, def.NewItem())
			write(v)
		}
	}
	genAssetListBrowse(s, path, def)
}
