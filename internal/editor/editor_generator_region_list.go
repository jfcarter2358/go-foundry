package editor

// editor_generator_region_list.go — the Generator's region tools for a level
// generation doc (docs/regions/SPEC.md, phase 5).
//
// A level generation doc's regions stage holds a synced COPY of its level's
// region volumes; the level is the source of truth. So the Generator shows
// the regions, and edits them, through the level:
//
//   - THE REGION LIST (right column, above the stage's parameters): each
//     region's environment colour, id, environment, priority and transition,
//     and a mark when the adjacency table forbids one of its neighbours.
//     Clicking a region selects it — highlighted in the 2D view, the regions
//     stage selected so its biome overlay is what the view shows — and
//     "Select in level" selects (and frames) its volume in the level editor.
//   - THE WEIGHT OVERLAY toggle: the 2D view's overlay becomes the selected
//     region's own weight layer (weight_<key>, from the regions stage's
//     preview), or the biome overlay with none selected.
//   - ADJACENCY WARNINGS: every forbidden pair that touches, each with
//     "Insert <env> region" (editor_region_adjacency.go's
//     editorInsertIntermediate, on the main window: one level undo step).
//   - FOOTPRINT EDITING in the 2D view, for the selected region: drag a
//     point, Ctrl+click an edge to insert one, right-click a point to delete
//     it. Each edit is WRITTEN BACK to the open level's region volume through
//     the level editor's mutation path — queued onto the main window's
//     context (uiwin.RequestWindowOpen: an undo snapshot and a cage reload
//     touch GL), one EditorPushUndo per drag — and the doc's copy follows on
//     the next sync (the half-second tick, and at once when a drag ends).
//
// With the level NOT open the list is read-only (the synced copy, as of the
// last sync from the file), the warnings come from the document's own
// regions stage (stages.RegionAdjacency, checked off the UI thread like the
// level editor's), and "Open level" opens it.

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/procgen"
	"foundry/internal/procgen/genlevel"
	"foundry/internal/procgen/regions"
	"foundry/internal/procgen/stages"
	"foundry/internal/uiwin"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// genRegionsUI is the region list's state.
var genRegionsUI struct {
	// sel is the selected region's volume ID, or "".
	sel string
	// weights shows the selected region's weight layer as the overlay.
	weights bool
}

// genRegionDrag is a footprint point drag in the 2D view.
var genRegionDrag struct {
	active bool
	vol    string
	point  int
	pushed bool
}

// genAdj is the Generator's own adjacency check, for a doc whose level is
// not open.
var genAdj regionAdjChecker

// genRegionLevelOpen reports whether the doc's level is the open one.
func genRegionLevelOpen() bool {
	return genDoc != nil && genDoc.Level != "" && genLevelIsOpen(levelio.LevelPath(genDoc.Level))
}

// genRegionVolume is the open level's region volume with the given ID, and
// its index, or nil.
func genRegionVolume(id string) (*world.Volume, int) {
	if Editor.level == nil || id == "" {
		return nil, -1
	}
	for i := range Editor.level.Volumes {
		v := &Editor.level.Volumes[i]
		if v.ID == id && v.Region != nil && v.FromEntity == "" && v.FromPrefab == "" {
			return v, i
		}
	}
	return nil, -1
}

// genRegionForbidden is the forbidden pairs that touch: the level's check
// when the level is open, else the doc's.
func genRegionForbidden() ([]regions.Adjacent, bool) {
	if genRegionLevelOpen() {
		regionAdjacencyTick()
		_, ok, _ := levelAdj.latest()
		return levelAdj.forbidden(""), ok
	}
	s := genlevel.RegionsStage(genDoc)
	if s == nil {
		return nil, false
	}
	d := genDoc.Clone()
	st := genlevel.RegionsStage(d)
	sig := fmt.Sprintf("%v|%d|%s", st.Params, d.Seed, adjacencyFileSig())
	genAdj.tick(sig, func() regionAdjacencyResult {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, pairs, err := stages.RegionAdjacency(ctx, d, st)
		return regionAdjacencyResult{pairs: pairs, err: err}
	})
	_, ok, _ := genAdj.latest()
	return genAdj.forbidden(""), ok
}

// generatorRegionsSection is the region list, the overlay toggle and the
// warnings (file comment). Nothing for a doc without a regions stage.
func generatorRegionsSection() {
	d := genDoc
	s := genlevel.RegionsStage(d)
	if s == nil {
		return
	}
	if !imgui.CollapsingHeaderV("Regions##genregionshdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	open := genRegionLevelOpen()
	if !open {
		editorTextMutedWrapped("Read-only: the level is the source of truth. Open it to edit footprints here.")
		if d.Level != "" && imgui.Button("Open level##genregopen") {
			genOpenTargetLevel()
		}
	}
	items := s.Params.Items(regions.ParamRegions)
	if len(items) == 0 {
		editorTextMutedWrapped("No regions: add region volumes to the level (a volume's Environment Region block).")
		return
	}
	forb, checked := genRegionForbidden()
	bad := map[string]bool{}
	for _, p := range forb {
		bad[p.IDA], bad[p.IDB] = true, true
	}
	for i, it := range items {
		id := it.Str(regions.FieldID)
		env := it.Str(regions.FieldEnvironment)
		c := procgen.EnvironmentColor(env)
		imgui.ColorButton(fmt.Sprintf("##genregcol%d", i), imgui.Vec4{X: float32(c[0]), Y: float32(c[1]), Z: float32(c[2]), W: 1},
			imgui.ColorEditFlagsNoTooltip, imgui.Vec2{X: 14, Y: 14})
		imgui.SameLine()
		label := fmt.Sprintf("%s  %s  p%d  %.0f m", id, env, it.Int(regions.FieldPriority), it.Float(regions.FieldTransition))
		if bad[id] {
			label = "! " + label
		}
		if imguibackend.SelectableV(label+"##genreg"+id, genRegionsUI.sel == id, 0, imgui.Vec2{}) {
			genSelectRegion(id)
		}
		if bad[id] {
			editorMenuTooltip("The adjacency table forbids one of this region's neighbours: see the warnings below.")
		}
	}
	if genRegionsUI.sel != "" {
		if open {
			if imgui.Button("Select in level##genregsel") {
				genSelectRegionInLevel(genRegionsUI.sel)
			}
			editorMenuTooltip("Select this region's volume in the level editor and frame it.")
			imgui.SameLine()
		}
		if imgui.Button("Deselect##genregdesel") {
			genRegionsUI.sel = ""
		}
		if open {
			editorTextMutedWrapped("In the 2D view: drag a point, Ctrl+click an edge to add one, right-click a point to delete it. " +
				"Edits go to the level (Ctrl+Z there undoes them).")
		}
	}
	imgui.Checkbox("Weight overlay##genregweights", &genRegionsUI.weights)
	editorMenuTooltip("Show the selected region's weight (its share of the ground, 0..1) over the view;\n" +
		"with none selected, the biome colours. Comes from the regions stage's preview.")

	// Warnings.
	switch {
	case !checked:
		editorTextMuted("Adjacency: checking...")
	case len(forb) == 0:
		editorTextMuted("Adjacency: no forbidden neighbours.")
	}
	for i, p := range forb {
		editorTextWrappedColored(genWarnColor, p.Warning())
		if c := p.Suggestion(); c != "" {
			imguibackend.EditorPushDisabled(!open)
			if imgui.Button(fmt.Sprintf("Insert %s region##genregins%d", c, i)) {
				a, b := p.IDA, p.IDB
				uiwin.RequestWindowOpen(func() {
					if !genRegionCanEdit() {
						return
					}
					if ids := editorInsertIntermediate(a, b); len(ids) > 0 {
						genRegionsUI.sel = ids[0]
						genSyncRegions(false)
						genStatusf("Inserted %s between %s and %s", strings.Join(ids, ", "), a, b)
					}
				})
			}
			imguibackend.EditorPopDisabled()
			if !open {
				editorMenuTooltip("Open the level first: the new region is a level volume.")
			}
		}
	}
	imgui.Separator()
}

// genSelectRegion selects a region in the list (and the regions stage, so
// the 2D view shows its overlay).
func genSelectRegion(id string) {
	genRegionsUI.sel = id
	for i := range genDoc.Stages {
		if genDoc.Stages[i].Kind == regions.StageKind && genSelected != i {
			genSelected = i
			genSpline = genSplineRef{}
			genRequestPreview()
			break
		}
	}
}

// genSelectRegionInLevel selects (and frames) the region's volume in the
// level editor.
func genSelectRegionInLevel(id string) {
	uiwin.RequestWindowOpen(func() {
		_, idx := genRegionVolume(id)
		if idx < 0 {
			genStatusf("The open level has no region volume %q", id)
			return
		}
		raiseMainWindow()
		editorSelectVolume(idx)
		editorFrameSelection()
	})
}

// genRegionOverlay points the 2D view's overlay at the selected region's
// weight layer (or the biome overlay) while the toggle is on.
func genRegionOverlay(p *procgen.PreviewData) {
	if !genRegionsUI.weights || p == nil || genlevel.RegionsStage(genDoc) == nil {
		return
	}
	want := stages.RegionsOverlayName
	if genRegionsUI.sel != "" {
		want = "weight_" + regions.Key(genRegionsUI.sel)
	}
	for i := range p.Images {
		if p.Images[i].Name == want {
			genV2.overlay = i
			return
		}
	}
}

// ---------- footprint editing in the 2D view ----------

// genRegionCanEdit reports whether the level may be edited now (not
// compiling, loading or playing), saying why not.
func genRegionCanEdit() bool {
	if why := genSpliceBlocked(); why != "" {
		genStatusf("Not now: %s", why)
		return false
	}
	return Editor.level != nil
}

// genRegionMovePoint moves point k of region id to world (x, z), on the
// main window: the first move of a drag pushes the level's undo step.
func genRegionMovePoint(id string, k int, x, z float64) {
	uiwin.RequestWindowOpen(func() {
		v, _ := genRegionVolume(id)
		if v == nil || k < 0 || k >= len(v.Region.Footprint) || !genRegionCanEdit() {
			return
		}
		if !genRegionDrag.pushed {
			EditorPushUndo()
			genRegionDrag.pushed = true
			if v, _ = genRegionVolume(id); v == nil {
				return
			}
		}
		v.Region.Footprint[k] = v.RegionWorldToLocal(mgl32.Vec3{float32(x), v.Position[1], float32(z)})
		_ = v.Load()
	})
}

// genRegionEndDrag refits the dragged region's box and resyncs the doc.
func genRegionEndDrag(id string) {
	uiwin.RequestWindowOpen(func() {
		if v, _ := genRegionVolume(id); v != nil && v.FitRegion() {
			_ = v.Load()
		}
		genSyncRegions(false)
	})
}

// genRegionInsertPoint inserts a point at world (x, z) after point k: one
// undo step.
func genRegionInsertPoint(id string, k int, x, z float64) {
	uiwin.RequestWindowOpen(func() {
		v, _ := genRegionVolume(id)
		if v == nil || k < 0 || k >= len(v.Region.Footprint) || !genRegionCanEdit() {
			return
		}
		EditorPushUndo()
		if v, _ = genRegionVolume(id); v == nil {
			return
		}
		p := v.RegionWorldToLocal(mgl32.Vec3{float32(x), v.Position[1], float32(z)})
		fp := v.Region.Footprint
		v.Region.Footprint = append(fp[:k+1], append([][2]float32{p}, fp[k+1:]...)...)
		_ = v.Load()
	})
}

// genRegionDeletePoint deletes point k (a footprint keeps 3): one undo step.
func genRegionDeletePoint(id string, k int) {
	uiwin.RequestWindowOpen(func() {
		v, _ := genRegionVolume(id)
		if v == nil || k < 0 || k >= len(v.Region.Footprint) || !genRegionCanEdit() {
			return
		}
		if len(v.Region.Footprint) <= 3 {
			genStatusf("A region's footprint keeps at least 3 points")
			return
		}
		EditorPushUndo()
		if v, _ = genRegionVolume(id); v == nil {
			return
		}
		v.Region.Footprint = append(v.Region.Footprint[:k], v.Region.Footprint[k+1:]...)
		v.FitRegion()
		_ = v.Load()
		genSyncRegions(false)
	})
}

// genRegionView2D draws the selected region's footprint and handles over
// the 2D view and takes the mouse for them. editing reports that the region
// owns the view's clicks (no spline is active meanwhile); near is the handle
// under the cursor (-1), so a right-click there does not pan.
func genRegionView2D(dl imgui.DrawList, hovered bool, mouse imgui.Vec2,
	toScreen func(x, z float64) imgui.Vec2, toWorld func(imgui.Vec2) (float64, float64)) (editing bool, near int) {
	near = -1
	if genRegionsUI.sel == "" || genlevel.RegionsStage(genDoc) == nil || !genRegionLevelOpen() {
		return false, -1
	}
	v, _ := genRegionVolume(genRegionsUI.sel)
	if v == nil {
		return false, -1
	}
	fp := v.RegionWorldFootprint()
	if len(fp) < 3 {
		return false, -1
	}
	c := procgen.EnvironmentColor(v.Region.Environment)
	col := genColor([4]float32{float32(c[0]), float32(c[1]), float32(c[2]), 1})
	for k := range fp {
		a, b := fp[k], fp[(k+1)%len(fp)]
		dl.AddLineV(toScreen(a[0], a[1]), toScreen(b[0], b[1]), genColor([4]float32{0, 0, 0, 0.8}), 4)
		dl.AddLineV(toScreen(a[0], a[1]), toScreen(b[0], b[1]), col, 2.5)
	}
	for k, p := range fp {
		sp := toScreen(p[0], p[1])
		hc := [4]float32{1, 1, 1, 1}
		if dx, dy := sp.X-mouse.X, sp.Y-mouse.Y; hovered && dx*dx+dy*dy < 64 {
			near = k
			hc = [4]float32{1, 0.85, 0.3, 1}
		}
		if genRegionDrag.active && genRegionDrag.point == k {
			hc = [4]float32{1, 0.85, 0.3, 1}
		}
		dl.AddCircleFilled(sp, 5, genColor(hc))
		dl.AddCircleV(sp, 5, genColor([4]float32{0, 0, 0, 1}), 12, 1)
	}
	id := v.ID
	ctrl := imgui.CurrentIO().KeyCtrlPressed()
	switch {
	case hovered && imgui.IsMouseClicked(1) && near >= 0:
		genRegionDeletePoint(id, near)
	case hovered && imgui.IsMouseClicked(0) && near >= 0 && !ctrl:
		genRegionDrag.active, genRegionDrag.vol, genRegionDrag.point, genRegionDrag.pushed = true, id, near, false
	case hovered && imgui.IsMouseClicked(0) && ctrl:
		if e := genRegionEdgeAt(fp, mouse, toScreen); e >= 0 {
			x, z := toWorld(mouse)
			genRegionInsertPoint(id, e, x, z)
			// Keep dragging the new point; the insert was the undo step.
			genRegionDrag.active, genRegionDrag.vol, genRegionDrag.point, genRegionDrag.pushed = true, id, e+1, true
		}
	case genRegionDrag.active && imgui.IsMouseDown(0):
		x, z := toWorld(mouse)
		if k := genRegionDrag.point; k < len(fp) && (math.Abs(fp[k][0]-x) > 1e-3 || math.Abs(fp[k][1]-z) > 1e-3) {
			genRegionMovePoint(genRegionDrag.vol, k, x, z)
		}
	}
	if genRegionDrag.active && !imgui.IsMouseDown(0) {
		genRegionDrag.active = false
		genRegionEndDrag(genRegionDrag.vol)
	}
	return true, near
}

// genRegionEdgeAt is the footprint edge (point k to k+1) within 8 px of the
// cursor, or -1.
func genRegionEdgeAt(fp [][2]float64, mouse imgui.Vec2, toScreen func(x, z float64) imgui.Vec2) int {
	best, bestD := -1, float32(8)
	for k := range fp {
		a, b := fp[k], fp[(k+1)%len(fp)]
		if d := genSegDist(mouse, toScreen(a[0], a[1]), toScreen(b[0], b[1])); d < bestD {
			best, bestD = k, d
		}
	}
	return best
}
