package editor

// editor_volume_region.go — environment region volumes in the level editor
// (docs/regions/SPEC.md, phase 1): the inspector's Environment Region group,
// the outlines in each environment's colour, and footprint point editing in
// the 3D view.
//
// A region is a volume with a `region:` block (world/volume_region.go): a
// closed polygon footprint, relative to the volume, that a level generation
// doc grows an environment preset over. The level is the source of truth —
// the generation doc's regions stage is a copy synced before every run — so
// this is where footprints are shaped.
//
// Footprint editing works on the SELECTED region, in the 3D view, on the
// plane through the volume's position (its own Y): every point has a handle;
//
//   - drag a handle to move the point (one undo step per drag, pushed when it
//     first moves);
//   - Ctrl+click near an edge to insert a point there (and keep dragging it);
//   - right-click a handle to delete the point (a footprint keeps 3).
//
// After every change the footprint is refitted (Volume.FitRegion): recentred
// on the volume's position with the box sized to it, so picking, the AABB,
// the translate gizmo and the plain cage all keep working on regions with no
// case of their own. The other way round, a change of the box's X/Z extents
// (the scale gizmo, the Shape group) SCALES the footprint to match
// (editorReconcileRegion), so the scale gizmo resizes a region as an author
// would expect.

import (
	"fmt"
	"math"
	"path"
	"strings"

	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/procgen"
	"foundry/internal/procgen/gencli"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// regionDefaultEnvironment is what a volume turned into a region grows.
const regionDefaultEnvironment = "desert_canyon"

// regionEdit is the footprint drag in progress.
var regionEdit struct {
	// dragging is the point index being dragged, or -1.
	dragging int
	// vol is the dragged volume's ID (an undo rebuilds the level, so not a
	// pointer).
	vol string
	// pushed latches the drag's single undo step.
	pushed bool
	// hover is the handle under the cursor, for the highlight; -1 for none.
	hover int
}

func init() { regionEdit.dragging, regionEdit.hover = -1, -1 }

// activeRegionVolume is the selected volume when it is a region the level
// owns (not one an entity's type carries), else nil.
func activeRegionVolume() *world.Volume {
	v := activeVolume()
	if v == nil || v.Region == nil || v.FromEntity != "" {
		return nil
	}
	return v
}

// regionColor is an environment's colour for the outlines.
func regionColor(env string) mgl32.Vec3 {
	c := procgen.EnvironmentColor(env)
	return mgl32.Vec3{float32(c[0]), float32(c[1]), float32(c[2])}
}

// ---------- the inspector ----------

// editorVolumeRegion draws the Environment Region group.
func editorVolumeRegion(v *world.Volume) {
	has := v.Region != nil
	if imgui.Checkbox("This volume is an environment region##volregon", &has) {
		EditorPushUndo()
		if has {
			v.Region = world.NewVolumeRegion(v, regionDefaultEnvironment)
			v.Rotation = mgl32.Vec3{}
			v.FitRegion()
			editorSetStatus("Volume %q is now a %s region", v.ID, regionDefaultEnvironment)
		} else {
			v.Region = nil
			editorSetStatus("Volume %q is no longer a region", v.ID)
		}
		_ = v.Load()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A region of the level's generated world: the level's world generator\n" +
			"(Generate World) grows this environment over the footprint, blended\n" +
			"into its neighbours across the transition band. Nothing at play time.")
	}
	g := v.Region
	if g == nil {
		return
	}

	if imguibackend.BeginCombo("Environment##volregenv", g.Environment) {
		for _, name := range procgen.EnvironmentNames() {
			label := name
			if e := procgen.EnvironmentByName(name); e != nil {
				label = name + "  (" + e.Label + ")"
			}
			if imguibackend.SelectableV(label+"##env"+name, name == g.Environment, 0, imgui.Vec2{}) && name != g.Environment {
				EditorPushUndo()
				g.Environment = name
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The preset grown here: resources/environments/<name>.fgen,\nor the built-in of that name.")
	}

	genLabel := g.Generator
	if genLabel == "" {
		genLabel = "(the environment's preset)"
	}
	if imguibackend.BeginCombo("Generator##volreggen", genLabel) {
		if imguibackend.SelectableV("(the environment's preset)##gennone", g.Generator == "", 0, imgui.Vec2{}) && g.Generator != "" {
			EditorPushUndo()
			g.Generator = ""
		}
		for _, ref := range regionGeneratorChoices() {
			if imguibackend.SelectableV(ref+"##gen"+ref, ref == g.Generator, 0, imgui.Vec2{}) && ref != g.Generator {
				EditorPushUndo()
				g.Generator = ref
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Optional: a .fgen to grow instead of the environment's preset.")
	}

	pri := int32(g.Priority)
	if imgui.InputInt("Priority##volregpri", &pri) && int(pri) != g.Priority {
		EditorPushUndo()
		g.Priority = int(pri)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Where regions overlap the higher priority wins;\nequal priorities share the band evenly.")
	}

	tr := g.Transition
	if imgui.DragFloatV("Transition (m)##volregtr", &tr, 1, 1, 10000, "%.0f", 0) {
		g.Transition = max(tr, 1)
	}
	if imgui.IsItemActivated() {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The blend band's width, centred on the footprint's edge.")
	}

	anchored := g.Anchor != nil
	if imgui.Checkbox("Anchor##volreganchoron", &anchored) {
		EditorPushUndo()
		if anchored {
			g.Anchor = &[2]float32{}
		} else {
			g.Anchor = nil
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Where the preset's own coordinates (its canyon paths) are measured\n" +
			"from, relative to the volume. Off: the footprint's centroid.")
	}
	if g.Anchor != nil {
		a := *g.Anchor
		if imgui.DragFloat2V("##volreganchor", &a, 1, -100000, 100000, "%.1f", 0) {
			*g.Anchor = a
		}
		if imgui.IsItemActivated() {
			EditorPushUndo()
		}
	}

	// The adjacency table's forbidden neighbours, checked off the UI thread
	// as the regions move (editor_region_adjacency.go).
	editorRegionAdjacencyWarnings(v)

	editorTextMuted(fmt.Sprintf("%d points, %.0f m² — drag a point in the view;", len(g.Footprint),
		world.RegionFootprintArea(g.Footprint)))
	editorTextMuted("Ctrl+click an edge adds one, right-click deletes.")
	if len(g.Footprint) < 3 {
		editorTextColored(scriptIssueColor, "A footprint needs at least 3 points.")
		if imgui.Button("Reset Footprint To The Box##volregreset") {
			EditorPushUndo()
			g.Footprint = world.NewVolumeRegion(v, g.Environment).Footprint
			v.FitRegion()
			_ = v.Load()
		}
	}
	if imgui.Button("Open World Generator##volregworld") {
		editorOpenWorldGenerator()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Open (or create) this level's world generator,\nresources/generators/<level>_world.fgen.")
	}
}

// regionGeneratorChoices is every .fgen a region could grow instead of its
// preset: the environment presets by name, then the generators by path.
func regionGeneratorChoices() []string {
	var out []string
	out = append(out, procgen.EnvironmentNames()...)
	for _, name := range procgen.ScanDocs() {
		out = append(out, path.Join(procgen.GeneratorsDirName, name+procgen.Ext))
	}
	return out
}

// editorOpenWorldGenerator creates the open level's world generator when it
// has none (and the built-in environment presets with no file yet) and opens
// it in the Generator.
func editorOpenWorldGenerator() {
	if Editor.level == nil || Editor.SavePath == "" {
		editorSetStatus("Save the level first: its world generator is named after it")
		return
	}
	level := levelio.LevelNameFromPath(Editor.SavePath)
	if strings.Contains(level, "/") {
		level = path.Base(level)
	}
	p, created, err := gencli.EnsureWorldDoc(level)
	if err != nil {
		editorSetStatus("World generator: %v", err)
		return
	}
	if created {
		if _, err := procgen.WriteEnvironments(); err != nil {
			editorSetStatus("Environment presets: %v", err)
		}
		editorSetStatus("Created %s", procgen.NameFromPath(p)+procgen.Ext)
	}
	OpenGeneratorEditorOn(procgen.NameFromPath(p))
}

// ---------- drawing ----------

// drawRegionVolumeOutlines traces every region's footprint at its volume's
// height in its environment's colour (depth test off, with the selection
// marks), and the selected region's point handles.
func drawRegionVolumeOutlines() {
	lvl := Editor.level
	if lvl == nil {
		return
	}
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.Region == nil || len(v.Region.Footprint) < 2 {
			continue
		}
		col := regionColor(v.Region.Environment)
		fp := v.Region.Footprint
		for k := range fp {
			gizmo.GizmoLine(v.RegionLocalToWorld(fp[k]), v.RegionLocalToWorld(fp[(k+1)%len(fp)]), col)
		}
	}
	v := activeRegionVolume()
	if v == nil {
		return
	}
	editorReconcileRegion(v)
	cam := ws().camPos()
	for k, p := range v.Region.Footprint {
		w := v.RegionLocalToWorld(p)
		r := max(w.Sub(cam).Len()*0.008, 0.05)
		c := mgl32.Vec3{1, 1, 1}
		if k == regionEdit.hover || k == regionEdit.dragging {
			c = volumeSelColor
		}
		a, b := w.Add(mgl32.Vec3{-r, 0, -r}), w.Add(mgl32.Vec3{r, 0, -r})
		cc, d := w.Add(mgl32.Vec3{r, 0, r}), w.Add(mgl32.Vec3{-r, 0, r})
		gizmo.GizmoLine(a, b, c)
		gizmo.GizmoLine(b, cc, c)
		gizmo.GizmoLine(cc, d, c)
		gizmo.GizmoLine(d, a, c)
	}
	if v.Region.Anchor != nil {
		w := v.RegionLocalToWorld(*v.Region.Anchor)
		r := max(w.Sub(cam).Len()*0.012, 0.08)
		col := regionColor(v.Region.Environment)
		gizmo.GizmoLine(w.Add(mgl32.Vec3{-r, 0, 0}), w.Add(mgl32.Vec3{r, 0, 0}), col)
		gizmo.GizmoLine(w.Add(mgl32.Vec3{0, 0, -r}), w.Add(mgl32.Vec3{0, 0, r}), col)
	}
}

// editorReconcileRegion keeps a region's footprint and box in step: a box
// whose X/Z extents no longer match the footprint's (the scale gizmo or the
// Shape group moved them) scales the footprint to fit, and a footprint off
// centre is refitted. Cheap; called every frame for the selected region.
func editorReconcileRegion(v *world.Volume) {
	g := v.Region
	if g == nil || len(g.Footprint) < 3 || regionEdit.dragging >= 0 {
		return
	}
	lo, hi := g.Footprint[0], g.Footprint[0]
	for _, p := range g.Footprint[1:] {
		lo = [2]float32{min(lo[0], p[0]), min(lo[1], p[1])}
		hi = [2]float32{max(hi[0], p[0]), max(hi[1], p[1])}
	}
	hx, hz := (hi[0]-lo[0])/2, (hi[1]-lo[1])/2
	sx, sz := float32(1), float32(1)
	if hx > 0.25 && math.Abs(float64(v.HalfX-hx)) > 1e-3*float64(max(hx, 1)) {
		sx = v.HalfX / hx
	}
	if hz > 0.25 && math.Abs(float64(v.HalfZ-hz)) > 1e-3*float64(max(hz, 1)) {
		sz = v.HalfZ / hz
	}
	changed := false
	if sx != 1 || sz != 1 {
		c := [2]float32{(lo[0] + hi[0]) / 2, (lo[1] + hi[1]) / 2}
		for i := range g.Footprint {
			g.Footprint[i][0] = c[0] + (g.Footprint[i][0]-c[0])*sx
			g.Footprint[i][1] = c[1] + (g.Footprint[i][1]-c[1])*sz
		}
		if g.Anchor != nil {
			g.Anchor[0] = c[0] + (g.Anchor[0]-c[0])*sx
			g.Anchor[1] = c[1] + (g.Anchor[1]-c[1])*sz
		}
		changed = true
	}
	if v.FitRegion() || changed {
		_ = v.Load()
	}
}

// ---------- footprint editing ----------

// regionProjView is the main view's projection × view.
func regionProjView() mgl32.Mat4 { return gfx.Projection.Mul4(gfx.Cam.Matrix) }

// regionHandleAt is the selected region's point whose handle is under the
// cursor, or -1.
func regionHandleAt(v *world.Volume, mx, my int32) int {
	pv := regionProjView()
	cursor := mgl32.Vec2{float32(mx), float32(my)}
	best, bestD := -1, gizmoPickPx
	for k, p := range v.Region.Footprint {
		sp, ok := projectToScreen(pv, v.RegionLocalToWorld(p))
		if !ok {
			continue
		}
		if d := sp.Sub(cursor).Len(); d <= bestD {
			best, bestD = k, d
		}
	}
	return best
}

// regionPlaneHit is where the cursor's ray meets the region's plane (through
// the volume's position, normal to its own up), in the footprint's local XZ.
func regionPlaneHit(v *world.Volume, mx, my int32) ([2]float32, bool) {
	o, d := ws().ray(mx, my)
	m := v.ModelMatrix()
	n := m.Mul4x1(mgl32.Vec4{0, 1, 0, 0}).Vec3().Normalize()
	den := d.Dot(n)
	if math.Abs(float64(den)) < 1e-6 {
		return [2]float32{}, false
	}
	t := v.Position.Sub(o).Dot(n) / den
	if t <= 0 {
		return [2]float32{}, false
	}
	return v.RegionWorldToLocal(o.Add(d.Mul(t))), true
}

// regionEdgeAt is the footprint edge (from point i to i+1) within pick
// distance of the cursor on screen, or -1.
func regionEdgeAt(v *world.Volume, mx, my int32) int {
	pv := regionProjView()
	fp := v.Region.Footprint
	best, bestD := -1, gizmoPickPx
	for i := range fp {
		a, okA := projectToScreen(pv, v.RegionLocalToWorld(fp[i]))
		b, okB := projectToScreen(pv, v.RegionLocalToWorld(fp[(i+1)%len(fp)]))
		if !okA || !okB {
			continue
		}
		if d := distToSegment2D(float32(mx), float32(my), a.X(), a.Y(), b.X(), b.Y()); d <= bestD {
			best, bestD = i, d
		}
	}
	return best
}

// editorRegionMouseDown answers a left press in the 3D view when a region is
// selected: grab a handle, or (Ctrl) insert a point on an edge. Reports
// whether it took the click.
func editorRegionMouseDown(mx, my int32, ctrl bool) bool {
	v := activeRegionVolume()
	if v == nil {
		return false
	}
	if k := regionHandleAt(v, mx, my); k >= 0 && !ctrl {
		regionEdit.dragging, regionEdit.vol, regionEdit.pushed = k, v.ID, false
		return true
	}
	if !ctrl {
		return false
	}
	e := regionEdgeAt(v, mx, my)
	if e < 0 {
		return false
	}
	p, ok := regionPlaneHit(v, mx, my)
	if !ok {
		return false
	}
	EditorPushUndo()
	fp := v.Region.Footprint
	at := e + 1
	v.Region.Footprint = append(fp[:at], append([][2]float32{p}, fp[at:]...)...)
	_ = v.Load()
	// Keep dragging what was just made; the insert was the undo step.
	regionEdit.dragging, regionEdit.vol, regionEdit.pushed = at, v.ID, true
	editorSetStatus("Added a point to region %q (%d points)", v.ID, len(v.Region.Footprint))
	return true
}

// editorRegionRightClick deletes the point whose handle is under the cursor.
// Reports whether it took the click (so the camera does not turn and no
// context menu opens).
func editorRegionRightClick(mx, my int32) bool {
	v := activeRegionVolume()
	if v == nil {
		return false
	}
	k := regionHandleAt(v, mx, my)
	if k < 0 {
		return false
	}
	if len(v.Region.Footprint) <= 3 {
		editorSetStatus("A region's footprint keeps at least 3 points")
		return true
	}
	EditorPushUndo()
	v.Region.Footprint = append(v.Region.Footprint[:k], v.Region.Footprint[k+1:]...)
	v.FitRegion()
	_ = v.Load()
	regionEdit.hover = -1
	editorSetStatus("Deleted a point of region %q (%d left)", v.ID, len(v.Region.Footprint))
	return true
}

// editorRegionDragging reports whether a footprint point is being dragged.
func editorRegionDragging() bool { return regionEdit.dragging >= 0 }

// editorRegionMouseMove follows the cursor: the dragged point, or the
// handle highlight.
func editorRegionMouseMove(mx, my int32) {
	v := activeRegionVolume()
	if v == nil {
		regionEdit.dragging, regionEdit.hover = -1, -1
		return
	}
	if !editorRegionDragging() {
		regionEdit.hover = regionHandleAt(v, mx, my)
		return
	}
	if v.ID != regionEdit.vol || regionEdit.dragging >= len(v.Region.Footprint) {
		regionEdit.dragging = -1
		return
	}
	p, ok := regionPlaneHit(v, mx, my)
	if !ok || p == v.Region.Footprint[regionEdit.dragging] {
		return
	}
	if !regionEdit.pushed {
		EditorPushUndo()
		regionEdit.pushed = true
		// The undo snapshot rebuilt nothing under us, but be sure.
		if v = activeRegionVolume(); v == nil {
			regionEdit.dragging = -1
			return
		}
	}
	v.Region.Footprint[regionEdit.dragging] = p
	_ = v.Load()
}

// editorRegionEndDrag lets the point go, refitting the box round the
// footprint (left alone during the drag so the point under the cursor does
// not shift as the centre moves).
func editorRegionEndDrag() {
	regionEdit.dragging = -1
	if v := activeRegionVolume(); v != nil && v.FitRegion() {
		_ = v.Load()
	}
}
