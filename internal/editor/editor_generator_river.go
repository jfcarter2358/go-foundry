package editor

// editor_generator_river.go — rivers in the Generator (docs/terrain-paint/
// PHASE_W3.md; the stage side is procgen/stages/ground_edit_river*.go and
// paint_preset_river.go).
//
// THE RIVER BRUSH. With a river preset picked (River, Stream: a preset with
// a `river:` block), a drag in the 2D view or the 3D viewport paints no
// layer: the brush centre's path is recorded (drawn as a line over the
// view), and on mouse-up it becomes a river spline in the document's
// ground_edit — simplified, every point as wide as the brush's diameter
// (stages.AddRiverStroke, which applies the preset's recipe first on a
// document that lacks its ground_edit or dressing). The stroke is a
// DOCUMENT edit, one undo step; it is also pushed on the brush's own stack
// as a marker, so Ctrl+Z with the brush on takes the river back like any
// stroke. The new river's path becomes the active spline, so its points
// can be dragged, inserted and deleted at once, and it kicks the live
// rerun (editor_generator_paint_live.go) like a stroke does.
//
// WIDTH IN THE VIEW. A spline with a `width` channel (a river's path) is
// drawn with its banks — a circle of the width at each point and the two
// bank lines — and, with the spline active, Shift+wheel over a point
// widens or narrows it (one undo step per gesture, 10% a notch).
//
// THE RIVERS PANEL, above a ground_edit's parameters (genStageExtraPanels):
// one row per river the last run carved (its rivers.yaml) — name and kind
// (painted, adopted, drainage), length and drop, where it ends, and for a
// hand river its material and Edit (makes its path the active spline),
// Width ×0.8 / ×1.25 (every point) and Delete. A drainage river has Adopt:
// its traced points, widths and depths are copied into a new hand river
// (`from: adopted`), which the trace then gives way to — the river is now
// the author's to edit. Every change is one document undo step and kicks
// the live rerun.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"foundry/internal/imguibackend"
	"foundry/internal/procgen"
	"foundry/internal/procgen/stages"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// genRiver is a river brush stroke in progress.
var genRiver struct {
	stroking bool
	pts      [][2]float64
}

// genPaintRiverActive reports whether the brush is a river brush.
func genPaintRiverActive() bool {
	p := genPaintCurrentPreset()
	return p != nil && p.River != nil
}

// genRiverBegin starts a river stroke at (x, z).
func genRiverBegin(x, z float64) {
	genRiver.stroking = true
	genRiver.pts = [][2]float64{{x, z}}
	genPaint.stroking = true
}

// genRiverDrag records the brush centre, a point every quarter radius.
func genRiverDrag(x, z float64) {
	if !genRiver.stroking {
		return
	}
	last := genRiver.pts[len(genRiver.pts)-1]
	if math.Hypot(x-last[0], z-last[1]) >= math.Max(float64(genPaint.radius)*0.25, 0.25) {
		genRiver.pts = append(genRiver.pts, [2]float64{x, z})
	}
}

// genRiverEnd turns the stroke into a river (file comment).
func genRiverEnd() {
	genRiver.stroking, genPaint.stroking = false, false
	pts := genRiver.pts
	genRiver.pts = nil
	p := genPaintCurrentPreset()
	if p != nil && p.Path != nil && genDoc != nil {
		genPathEnd(p, pts) // a path brush (editor_generator_path.go)
		return
	}
	if p == nil || p.River == nil || genDoc == nil {
		return
	}
	GeneratorPushUndo()
	ge, idx, notes, err := stages.AddRiverStroke(genDoc, p, pts, float64(genPaint.radius))
	if err != nil {
		generatorUndoStack.pop()
		genStatusf("River: %v", err)
		return
	}
	genPaintUndoStack.push(genPaintStep{doc: true})
	genPaintRedoStack.clear()
	genSpline = genSplineRef{stage: ge, path: genParamPath{{Name: "rivers", Item: idx}, {Name: "path", Item: -1}}}
	genAfterEdit()
	genPaintLiveKick()
	it := genDoc.StageByID(ge).Params["rivers"].Items[idx]
	msg := fmt.Sprintf("River %s: %d points in %s", it.Str("name"), len(it.Points("path")), ge)
	if len(notes) > 0 {
		msg += fmt.Sprintf(" (recipe: %d changes)", len(notes))
	}
	genStatusf("%s", msg)
}

// genRiverDrawStroke draws the stroke in progress over the 2D view.
func genRiverDrawStroke(dl imgui.DrawList, toScreen func(x, z float64) imgui.Vec2) {
	if !genRiver.stroking || len(genRiver.pts) < 2 {
		return
	}
	col := genColor([4]float32{0.35, 0.7, 1, 0.9})
	w := float32(math.Max(float64(genPaint.radius)*2*genV2.scale, 2))
	for i := 1; i < len(genRiver.pts); i++ {
		a, b := genRiver.pts[i-1], genRiver.pts[i]
		dl.AddLineV(toScreen(a[0], a[1]), toScreen(b[0], b[1]), genColor([4]float32{0.3, 0.6, 1, 0.25}), w)
		dl.AddLineV(toScreen(a[0], a[1]), toScreen(b[0], b[1]), col, 2)
	}
}

// ---------- width in the 2D view ----------

// genSplineWidthChannel is the index of a spline's width channel in a
// point (after the position), or -1.
func genSplineWidthChannel(d *procgen.ParamDef) int {
	for i, c := range d.Channels {
		if c == "width" {
			off := 2
			if d.Spline3D {
				off = 3
			}
			return off + i
		}
	}
	return -1
}

// genDrawSplineBanks draws a width-channel spline's banks: a circle of
// the width at each point and the bank lines between.
func genDrawSplineBanks(dl imgui.DrawList, sp *genStageSpline, active bool, toScreen func(x, z float64) imgui.Vec2) {
	wc := genSplineWidthChannel(sp.def)
	pts := sp.value.Points
	if wc < 0 || len(pts) == 0 {
		return
	}
	alpha := float32(0.25)
	if active {
		alpha = 0.6
	}
	col := genColor([4]float32{0.4, 0.75, 1, alpha})
	side := func(i int, s float64) imgui.Vec2 {
		x, z := genSplineXZ(sp.def, pts[i])
		a, b := pts[max(i-1, 0)], pts[min(i+1, len(pts)-1)]
		ax, az := genSplineXZ(sp.def, a)
		bx, bz := genSplineXZ(sp.def, b)
		dx, dz := bx-ax, bz-az
		l := math.Hypot(dx, dz)
		if l < 1e-9 {
			return toScreen(x, z)
		}
		w := pts[i][wc] / 2
		return toScreen(x-dz/l*w*s, z+dx/l*w*s)
	}
	for i := range pts {
		if wc >= len(pts[i]) {
			return
		}
		x, z := genSplineXZ(sp.def, pts[i])
		if active {
			dl.AddCircleV(toScreen(x, z), float32(pts[i][wc]/2*genV2.scale), col, 24, 1)
		}
		if i > 0 {
			for _, s := range []float64{-1, 1} {
				dl.AddLineV(side(i-1, s), side(i, s), col, 1)
			}
		}
	}
}

// genSplineWidthWheel is Shift+wheel over an active spline's point: its
// width ×1.1 a notch. True when it took the wheel.
func genSplineWidthWheel(active *genStageSpline, near int, wy float32) bool {
	if active == nil || near < 0 || wy == 0 || !genPaintShiftHeld() {
		return false
	}
	wc := genSplineWidthChannel(active.def)
	if wc < 0 || near >= len(active.value.Points) || wc >= len(active.value.Points[near]) {
		return false
	}
	s := genSelectedStage()
	k := procgen.KindByName(s.Kind)
	v := active.value.Clone()
	v.Points[near][wc] = math.Max(v.Points[near][wc]*math.Pow(1.1, float64(wy)), 0.1)
	genBeginEdit(s.ID + "/width/" + active.path.key())
	genSetValueAt(k.Params, s.Params, active.path, active.def.Clamp(v))
	genAfterEdit()
	genRiverWidthAt = time.Now()
	return true
}

// genRiverWidthAt is when the last Shift+wheel changed a width: the live
// rerun waits until the wheel has been still a moment (genRiverWidthKick).
var genRiverWidthAt time.Time

// genRiverWidthKick kicks the live rerun half a second after the last
// width change. Called each frame by the 2D view.
func genRiverWidthKick() {
	if !genRiverWidthAt.IsZero() && time.Since(genRiverWidthAt) > 500*time.Millisecond {
		genRiverWidthAt = time.Time{}
		genEndEdits()
		genPaintLiveKick()
	}
}

// ---------- the rivers panel ----------

// genRiverUI caches the last rivers.yaml read.
var genRiverUI struct {
	path   string
	mod    time.Time
	rivers *stages.GroundRivers
	err    error
}

// genRiversFile is the stage's rivers table from its last run (nil before
// any run with rivers).
func genRiversFile(s *procgen.Stage) (*stages.GroundRivers, error) {
	path := filepath.Join(procgen.OutDirFor(genDoc, procgen.Options{}), s.ID, stages.RiversFile)
	fi, err := os.Stat(path)
	if err != nil {
		genRiverUI.path, genRiverUI.rivers, genRiverUI.err = path, nil, nil
		return nil, nil
	}
	if path != genRiverUI.path || !fi.ModTime().Equal(genRiverUI.mod) {
		genRiverUI.path, genRiverUI.mod = path, fi.ModTime()
		genRiverUI.rivers, genRiverUI.err = stages.ReadGroundRivers(path)
	}
	return genRiverUI.rivers, genRiverUI.err
}

// genRiverEdit applies fn to the stage's rivers list as one document edit.
func genRiverEdit(s *procgen.Stage, fn func(items []procgen.Params) []procgen.Params) {
	GeneratorPushUndo()
	cur := s.Params["rivers"]
	if cur.Type != procgen.ParamList {
		cur = procgen.List()
	}
	cur.Items = fn(cur.Items)
	s.Params["rivers"] = cur
	genAfterEdit()
	genPaintLiveKick()
}

// genRiversPanel draws the panel (file comment) above a ground_edit's
// parameters.
func genRiversPanel(s *procgen.Stage) {
	items := s.Params["rivers"].Items
	if len(items) == 0 && !s.Params["drainage"].Bool {
		return
	}
	if !imgui.CollapsingHeaderV("Rivers##genrivers", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	rv, err := genRiversFile(s)
	if err != nil {
		editorTextWrappedColored(genWarnColor, err.Error())
	}
	mats := genWaterMaterials()
	flags := imgui.TableFlagsBordersInnerH | imgui.TableFlagsSizingFixedFit
	if imgui.BeginTableV("##genriverstable", 4, flags, imgui.Vec2{}, 0) {
		imgui.TableSetupColumn("River")
		imgui.TableSetupColumn("Run")
		imgui.TableSetupColumn("Material")
		imgui.TableSetupColumn("")
		imgui.TableHeadersRow()
		// Hand rivers, from the document (a fresh stroke shows before the
		// run), with what the last run made of each.
		carved := map[int]stages.GroundRiver{}
		if rv != nil {
			for _, r := range rv.Rivers {
				if r.Entry >= 0 {
					if _, seen := carved[r.Entry]; !seen {
						carved[r.Entry] = r
					}
				}
			}
		}
		for i, it := range items {
			imgui.PushID(fmt.Sprintf("hand%d", i))
			r, ok := carved[i]
			genRiverHandRow(s, i, it, r, ok, mats)
			imgui.PopID()
		}
		if rv != nil {
			for i, r := range rv.Rivers {
				if r.Entry >= 0 {
					continue
				}
				imgui.PushID(fmt.Sprintf("drain%d", i))
				genRiverDrainageRow(s, r, rv)
				imgui.PopID()
			}
		}
		imgui.EndTable()
	}
	if rv == nil {
		editorTextMutedWrapped("Run the generator to carve the rivers (and trace the drainage ones).")
	} else if len(rv.Falls) > 0 || len(rv.Confluences) > 0 || rv.Boulders > 0 {
		editorTextMutedWrapped(fmt.Sprintf("%d waterfalls, %d confluences, %d boulders.", len(rv.Falls), len(rv.Confluences), rv.Boulders))
	}
}

// genRiverRun is a river's length, drop and mouth for the table.
func genRiverRun(r stages.GroundRiver) string {
	s := fmt.Sprintf("%.0f m, %.1f m drop", r.Length, r.Drop())
	if r.Into != "" {
		s += " → " + r.Into
	}
	return s
}

// genRiverHandRow is one hand river's row.
func genRiverHandRow(s *procgen.Stage, i int, it procgen.Params, r stages.GroundRiver, carved bool, mats []string) {
	imgui.TableNextRow()
	imgui.TableNextColumn()
	name := it.Str("name")
	if name == "" {
		name = fmt.Sprintf("river_%d", i+1)
	}
	imgui.Text(fmt.Sprintf("%s (%s)", name, it.Str("from")))
	editorMenuTooltip(fmt.Sprintf("%d points. Shift+wheel over a point in the 2D view to change its width.", len(it.Points("path"))))
	imgui.TableNextColumn()
	if carved {
		imgui.Text(genRiverRun(r))
		if r.Reversed {
			editorMenuTooltip("Drawn uphill: carved the other way round.")
		}
	} else {
		editorTextMuted("not carved yet")
	}
	imgui.TableNextColumn()
	cur := it.Str("material")
	label := cur
	if label == "" {
		label = "(default) water_river"
	}
	imgui.SetNextItemWidth(150)
	if imguibackend.BeginCombo("##rivermat", label) {
		if imguibackend.SelectableV("(default)", cur == "", 0, imgui.Vec2{}) && cur != "" {
			genRiverEdit(s, func(items []procgen.Params) []procgen.Params { items[i]["material"] = procgen.String(""); return items })
		}
		for _, m := range mats {
			if imguibackend.SelectableV(m, m == cur, 0, imgui.Vec2{}) && m != cur {
				mm := m
				genRiverEdit(s, func(items []procgen.Params) []procgen.Params { items[i]["material"] = procgen.String(mm); return items })
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("The river's water material (water_river has the flow map).")
	imgui.TableNextColumn()
	if imgui.Button("Edit") {
		genSpline = genSplineRef{stage: s.ID, path: genParamPath{{Name: "rivers", Item: i}, {Name: "path", Item: -1}}}
	}
	editorMenuTooltip("Edit its path in the 2D view: drag points, click to add, right-click to delete.")
	imgui.SameLine()
	if imgui.Button("−") {
		genRiverScaleWidth(s, i, 0.8)
	}
	editorMenuTooltip("Narrower: every point's width ×0.8.")
	imgui.SameLine()
	if imgui.Button("+") {
		genRiverScaleWidth(s, i, 1.25)
	}
	editorMenuTooltip("Wider: every point's width ×1.25.")
	imgui.SameLine()
	if imgui.Button("Delete") {
		genRiverEdit(s, func(items []procgen.Params) []procgen.Params { return append(items[:i:i], items[i+1:]...) })
	}
}

// genRiverScaleWidth scales every point's width of hand river i.
func genRiverScaleWidth(s *procgen.Stage, i int, f float64) {
	genRiverEdit(s, func(items []procgen.Params) []procgen.Params {
		v := items[i]["path"].Clone()
		for _, q := range v.Points {
			if len(q) > 2 {
				q[2] = math.Round(math.Max(q[2]*f, 0.1)*100) / 100
			}
		}
		items[i]["path"] = v
		return items
	})
}

// genRiverDrainageRow is one traced river's row, with Adopt.
func genRiverDrainageRow(s *procgen.Stage, r stages.GroundRiver, rv *stages.GroundRivers) {
	imgui.TableNextRow()
	imgui.TableNextColumn()
	imgui.Text(r.Name + " (drainage)")
	imgui.TableNextColumn()
	imgui.Text(genRiverRun(r))
	imgui.TableNextColumn()
	imgui.Text(r.Material)
	imgui.TableNextColumn()
	var tr *stages.GroundTraced
	for i := range rv.Traced {
		if rv.Traced[i].Name == r.Name {
			tr = &rv.Traced[i]
		}
	}
	if tr == nil {
		return
	}
	if imgui.Button("Adopt") {
		genRiverAdopt(s, tr)
	}
	editorMenuTooltip("Copy this traced river into a hand river you can edit; the trace then gives way to it.")
}

// genRiverAdopt copies a traced river into a new hand river.
func genRiverAdopt(s *procgen.Stage, tr *stages.GroundTraced) {
	idx := len(s.Params["rivers"].Items)
	genRiverEdit(s, func(items []procgen.Params) []procgen.Params {
		return append(items, stages.AdoptedRiver(tr, s.Params.Str("drainage_material")))
	})
	genSpline = genSplineRef{stage: s.ID, path: genParamPath{{Name: "rivers", Item: idx}, {Name: "path", Item: -1}}}
	genStatusf("Adopted %s as a hand river", tr.Name)
}
