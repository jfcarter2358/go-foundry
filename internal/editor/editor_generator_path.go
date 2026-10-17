package editor

// editor_generator_path.go — footpaths in the Generator (docs/terrain-paint/
// PHASE_P4.md; the stage side is procgen/stages/ground_edit_path*.go and
// paint_preset_path.go).
//
// THE PATH BRUSH. With a path preset picked (Rock path, Stepping stones,
// Dirt trail, Gravel path, Game trail: a preset with a `path:` block), a
// drag in the 2D view or the 3D viewport paints no layer, exactly as a
// river brush's does (editor_generator_river.go — the stroke recording,
// its drawing and the brush-stack marker are shared): on mouse-up the
// brush centre's path becomes a `paths` item in the document's ground_edit
// in the preset's style, simplified, every point as wide as the brush's
// diameter times the preset's width scale (stages.AddPathStroke, which
// applies the preset's recipe first when the document lacks its ground_edit,
// stones or splat entries). It is one document undo step, the new path
// becomes the active spline (its points dragged, inserted and deleted with
// the ordinary spline editing, its banks drawn from the width channel and
// Shift+wheel over a point widening it), and it kicks the live rerun.
//
// THE PATHS PANEL, above a ground_edit's parameters (genStageExtraPanels):
// one row per `paths` item — name, a style combo, a Flatten checkbox, the
// last run's length, crossings and stones (paths.yaml) — with Edit (its
// spline becomes the active one), Width −/+ (every point ×0.8 / ×1.25)
// and Delete. Every change is one document undo step and kicks the live
// rerun. The run's note (stones laid out but no Path rocks chosen) shows
// under the table.
//
// DIRECTION BY HAND. On a direction layer the brush's drag direction is
// the stamp's direction (genPaintDrag sets each stamp's Dir from the
// mouse's motion), so a drag down a slope orients fallen branches down it;
// a click without motion paints nothing.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"foundry/internal/imguibackend"
	"foundry/internal/procgen"
	"foundry/internal/procgen/stages"
	"foundry/internal/procgen/terrain"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// genPaintPathActive reports whether the brush is a path brush.
func genPaintPathActive() bool {
	p := genPaintCurrentPreset()
	return p != nil && p.Path != nil
}

// genPaintSplineBrush reports whether a stroke becomes a spline (a river
// or a path brush) rather than paint.
func genPaintSplineBrush() bool { return genPaintRiverActive() || genPaintPathActive() }

// genPathEnd turns a path brush stroke into a path (file comment).
func genPathEnd(p *stages.Preset, pts [][2]float64) {
	GeneratorPushUndo()
	ge, idx, notes, err := stages.AddPathStroke(genDoc, p, pts, float64(genPaint.radius))
	if err != nil {
		generatorUndoStack.pop()
		genStatusf("Path: %v", err)
		return
	}
	genPaintUndoStack.push(genPaintStep{doc: true})
	genPaintRedoStack.clear()
	genSpline = genSplineRef{stage: ge, path: genParamPath{{Name: "paths", Item: idx}, {Name: "path", Item: -1}}}
	genAfterEdit()
	genPaintLiveKick()
	it := genDoc.StageByID(ge).Params["paths"].Items[idx]
	msg := fmt.Sprintf("Path %s (%s): %d points in %s", it.Str("name"), it.Str("style"), len(it.Points("path")), ge)
	if len(notes) > 0 {
		msg += fmt.Sprintf(" (recipe: %d changes)", len(notes))
	}
	genStatusf("%s", msg)
}

// genPaintIsDirection reports whether the brush paints a direction layer
// alone (no preset): its stamps take the drag's direction.
func genPaintIsDirection() bool {
	def := genDoc.PaintDef(genPaint.layer)
	return genPaint.preset == "" && def != nil && def.Kind == "direction"
}

// ---------- the paths panel ----------

// genPathUI caches the last paths.yaml read.
var genPathUI struct {
	path  string
	mod   time.Time
	paths *stages.GroundPaths
	err   error
}

// genPathsFile is the stage's paths table from its last run (nil before
// any run with paths).
func genPathsFile(s *procgen.Stage) (*stages.GroundPaths, error) {
	path := filepath.Join(procgen.OutDirFor(genDoc, procgen.Options{}), s.ID, stages.PathsFile)
	fi, err := os.Stat(path)
	if err != nil {
		genPathUI.path, genPathUI.paths, genPathUI.err = path, nil, nil
		return nil, nil
	}
	if path != genPathUI.path || !fi.ModTime().Equal(genPathUI.mod) {
		genPathUI.path, genPathUI.mod = path, fi.ModTime()
		genPathUI.paths, genPathUI.err = stages.ReadGroundPaths(path)
	}
	return genPathUI.paths, genPathUI.err
}

// genPathEdit applies fn to the stage's paths list as one document edit.
func genPathEdit(s *procgen.Stage, fn func(items []procgen.Params) []procgen.Params) {
	GeneratorPushUndo()
	cur := s.Params["paths"]
	if cur.Type != procgen.ParamList {
		cur = procgen.List()
	}
	cur.Items = fn(cur.Items)
	s.Params["paths"] = cur
	genAfterEdit()
	genPaintLiveKick()
}

// genPathsPanel draws the panel (file comment) above a ground_edit's
// parameters.
func genPathsPanel(s *procgen.Stage) {
	items := s.Params["paths"].Items
	if len(items) == 0 {
		return
	}
	if !imgui.CollapsingHeaderV("Paths##genpaths", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	tab, err := genPathsFile(s)
	if err != nil {
		editorTextWrappedColored(genWarnColor, err.Error())
	}
	laid := map[int]stages.GroundPath{}
	if tab != nil {
		for _, p := range tab.Paths {
			laid[p.Entry] = p
		}
	}
	flags := imgui.TableFlagsBordersInnerH | imgui.TableFlagsSizingFixedFit
	if imgui.BeginTableV("##genpathstable", 5, flags, imgui.Vec2{}, 0) {
		imgui.TableSetupColumn("Path")
		imgui.TableSetupColumn("Style")
		imgui.TableSetupColumn("Flatten")
		imgui.TableSetupColumn("Run")
		imgui.TableSetupColumn("")
		imgui.TableHeadersRow()
		for i, it := range items {
			imgui.PushID(fmt.Sprintf("path%d", i))
			p, ok := laid[i]
			genPathRow(s, i, it, p, ok)
			imgui.PopID()
		}
		imgui.EndTable()
	}
	switch {
	case tab == nil:
		editorTextMutedWrapped("Run the generator to lay the paths (flatten, wear, stones).")
	case tab.Note != "":
		editorTextWrappedColored(genWarnColor, tab.Note)
	}
}

// genPathRun is a laid path's length, crossings and stones for the table.
func genPathRun(p stages.GroundPath) string {
	s := fmt.Sprintf("%.0f m", p.Length)
	if p.Crossings > 0 {
		s += fmt.Sprintf(", %d ford", p.Crossings)
		if p.Crossings > 1 {
			s += "s"
		}
	}
	n := 0
	var kinds []string
	for _, k := range []string{terrain.StonesFlag, terrain.StonesStep, terrain.StonesFord} {
		if c := p.Stones[k]; c > 0 {
			n += c
			kinds = append(kinds, fmt.Sprintf("%d %s", c, k))
		}
	}
	if n > 0 {
		s += ", " + strings.Join(kinds, " / ") + " stones"
	}
	return s
}

// genPathRow is one path's row.
func genPathRow(s *procgen.Stage, i int, it procgen.Params, p stages.GroundPath, laid bool) {
	imgui.TableNextRow()
	imgui.TableNextColumn()
	name := it.Str("name")
	if name == "" {
		name = fmt.Sprintf("path_%d", i+1)
	}
	imgui.Text(name)
	editorMenuTooltip(fmt.Sprintf("%d points. Shift+wheel over a point in the 2D view to change its width.", len(it.Points("path"))))
	imgui.TableNextColumn()
	style := it.Str("style")
	if style == "" {
		style = terrain.PathDirt
	}
	imgui.SetNextItemWidth(130)
	if imguibackend.BeginCombo("##pathstyle", style) {
		for _, st := range terrain.PathStyleNames {
			if imguibackend.SelectableV(st, st == style, 0, imgui.Vec2{}) && st != style {
				ss := st
				genPathEdit(s, func(items []procgen.Params) []procgen.Params { items[i]["style"] = procgen.Enum(ss); return items })
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("The path's style: its stones, surface, wear and whether it fords water.")
	imgui.TableNextColumn()
	flat := true
	if v, ok := it["flatten"]; ok {
		flat = v.Bool
	}
	if imgui.Checkbox("##pathflat", &flat) {
		f := flat
		genPathEdit(s, func(items []procgen.Params) []procgen.Params { items[i]["flatten"] = procgen.Bool(f); return items })
	}
	editorMenuTooltip("Level the ground across the path to its smoothed centreline (never in water).")
	imgui.TableNextColumn()
	if laid {
		imgui.Text(genPathRun(p))
	} else {
		editorTextMuted("not laid yet")
	}
	imgui.TableNextColumn()
	if imgui.Button("Edit") {
		genSpline = genSplineRef{stage: s.ID, path: genParamPath{{Name: "paths", Item: i}, {Name: "path", Item: -1}}}
	}
	editorMenuTooltip("Edit its path in the 2D view: drag points, click to add, right-click to delete.")
	imgui.SameLine()
	if imgui.Button("−") {
		genPathScaleWidth(s, i, 0.8)
	}
	editorMenuTooltip("Narrower: every point's width ×0.8.")
	imgui.SameLine()
	if imgui.Button("+") {
		genPathScaleWidth(s, i, 1.25)
	}
	editorMenuTooltip("Wider: every point's width ×1.25.")
	imgui.SameLine()
	if imgui.Button("Delete") {
		genPathEdit(s, func(items []procgen.Params) []procgen.Params { return append(items[:i:i], items[i+1:]...) })
	}
}

// genPathScaleWidth scales every point's width of path i.
func genPathScaleWidth(s *procgen.Stage, i int, f float64) {
	genPathEdit(s, func(items []procgen.Params) []procgen.Params {
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
