package editor

// editor_tree.go — placing, selecting and editing trees (world.Tree).
//
// A tree is placed the way an effect or a sound is — arm it from the
// Explorer's Add menu, click the ground — and edited the way scenery is: the
// move gizmo carries it, the rotate gizmo turns it about its trunk (the yaw
// ring only: a tree grows up), and the scale gizmo sizes it, uniformly. It
// draws itself (render/foliage_placed.go), so the viewport needs no marker;
// the selected one is outlined as its crown's cylinder and its trunk's box,
// which is also what a click on it is tested against.
//
// The undergrowth around it is biome paint; the trees are this.

import (
	"fmt"
	"math"
	"math/rand"
	"strings"

	"foundry/internal/gizmo"
	"foundry/internal/imguibackend"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

var (
	treeSelColor   = mgl32.Vec3{1, 1, 0}
	treeTrunkColor = mgl32.Vec3{1, 0.6, 0.2}
)

// treeSpecies is the order the Add menu and the species combo offer shapes
// in: the trees first (the rainforest's, then the other biomes'), then
// everything else that grows.
var treeSpecies = func() []world.FoliageShape {
	first := []world.FoliageShape{
		world.FoliageSitkaSpruce, world.FoliageHemlock, world.FoliageRedcedar, world.FoliageDouglasFir,
		world.FoliageBigleafMaple, world.FoliageRedAlder, world.FoliageVineMaple, world.FoliageCottonwood,
		world.FoliageBroadleaf, world.FoliageConifer, world.FoliagePalm, world.FoliageAcacia,
	}
	out := append([]world.FoliageShape(nil), first...)
	for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
		seen := false
		for _, f := range first {
			seen = seen || f == s
		}
		if !seen {
			out = append(out, s)
		}
	}
	return out
}()

// treeSpeciesIsTree is whether s is in the Add menu's first block.
func treeSpeciesIsTree(s world.FoliageShape) bool {
	for _, f := range treeSpecies[:12] {
		if f == s {
			return true
		}
	}
	return false
}

// treeSpeciesLabel is a shape as the editor names it: "Sitka spruce".
func treeSpeciesLabel(s world.FoliageShape) string {
	n := s.String()
	if n == "" || n == "?" {
		return "(unknown)"
	}
	return strings.ToUpper(n[:1]) + n[1:]
}

// ---------- selection ----------

// activeTree returns the selected tree, or nil.
func activeTree() *world.Tree {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveTreeIdx < 0 || Editor.ActiveTreeIdx >= len(lvl.Trees) {
		return nil
	}
	return &lvl.Trees[Editor.ActiveTreeIdx]
}

// editorSelectTree makes the tree at idx the selection.
func editorSelectTree(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Trees) {
		return
	}
	editorClearObjectSelections()
	Editor.ActiveTreeIdx = idx
	Editor.GizmoKind = gizmoKindTree
	Editor.PanelFocus = panelFocusTree
}

// editorClearTreeSelection drops the tree from the selection, taking the
// gizmo with it.
func editorClearTreeSelection() {
	Editor.ActiveTreeIdx = -1
	if Editor.GizmoKind == gizmoKindTree {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoTree returns the tree the gizmo is attached to, or nil.
func activeGizmoTree() *world.Tree {
	if Editor.GizmoKind != gizmoKindTree {
		return nil
	}
	return activeTree()
}

// ---------- placement ----------

// editorArmTreePlacement makes the next left-click plant a species tree.
func editorArmTreePlacement(species world.FoliageShape) {
	editorDisarmBrush()
	editorDisarmBrushPlacement()
	Editor.NewTreeSpecies = species
	Editor.TreePlacementMode = true
	Editor.EffectPlacementMode = false
	Editor.SoundPlacementMode = false
	Editor.EmitterPlacementMode = false
	Editor.SpritePlacementMode = false
	Editor.DecalPlacementMode = false
	Editor.VolumePlacementMode = false
	Editor.EntityPlacementMode = false
	Editor.PrefabPlacementMode = false
	Editor.SpawnPlacementMode = false
	Editor.WaypointPlacementMode = false
	editorSetStatus("Click to plant a %s — Escape to stop", strings.ToLower(treeSpeciesLabel(species)))
}

// placeTreeAt plants the armed species with its foot at pos, turned at
// random (a row of trees all facing one way looks planted), and selects it.
// Placement stays armed, so a stand of trees is a click per tree.
func placeTreeAt(pos mgl32.Vec3) bool {
	lvl := Editor.level
	if lvl == nil {
		return false
	}
	EditorPushUndo()
	s := Editor.NewTreeSpecies
	t := world.NewTree(world.UniqueTreeID(lvl, strings.ReplaceAll(s.String(), " ", "_")), s, pos)
	t.Variant = Editor.NewTreeVariant
	if t.Variant < 0 {
		t.Variant = rand.Intn(world.FoliageVariants[s])
	}
	t.Yaw = rand.Float32() * 2 * math.Pi
	lvl.Trees = append(lvl.Trees, t)
	editorSelectTree(len(lvl.Trees) - 1)
	editorSetStatus("Planted %s %q — click for another, Escape to stop", strings.ToLower(treeSpeciesLabel(s)), t.ID)
	return true
}

// ---------- duplicate / delete ----------

// duplicateActiveTree plants a copy of the selected tree beside it, turned
// differently.
func duplicateActiveTree() {
	lvl := Editor.level
	src := activeTree()
	if lvl == nil || src == nil {
		return
	}
	EditorPushUndo()
	cp := *src
	cp.ID = world.UniqueTreeID(lvl, src.ID)
	ext := world.FoliageExtent[src.Species%world.FoliageShapeCount]
	cp.Position = src.Position.Add(mgl32.Vec3{ext[1] * src.EffectiveScale() * 1.6, 0, 0})
	cp.Yaw = src.Yaw + 2.1
	lvl.Trees = append(lvl.Trees, cp)
	editorSelectTree(len(lvl.Trees) - 1)
	editorSetStatus("Duplicated tree %q", cp.ID)
}

// deleteActiveTree removes the selected tree.
func deleteActiveTree() {
	lvl := Editor.level
	idx := Editor.ActiveTreeIdx
	if lvl == nil || idx < 0 || idx >= len(lvl.Trees) {
		return
	}
	EditorPushUndo()
	id := lvl.Trees[idx].ID
	lvl.Trees = append(lvl.Trees[:idx], lvl.Trees[idx+1:]...)
	editorClearTreeSelection()
	editorSetStatus("Deleted tree %q", id)
}

// editor3DMoveTreeTo puts the selected tree's foot at p.
func editor3DMoveTreeTo(p mgl32.Vec3) {
	if t := activeTree(); t != nil {
		EditorPushUndo()
		t.Position = p
	}
}

// ---------- picking ----------

// treePickBox is the box a click on t is tested against: its crown's
// footprint (the inner half of the species' reach, so the gaps between
// crowns in a stand still reach what is behind) from its foot to its top.
func treePickBox(t *world.Tree) (mgl32.Vec3, mgl32.Vec3) {
	ext := world.FoliageExtent[t.Species%world.FoliageShapeCount]
	s := t.EffectiveScale()
	r, h := ext[1]*0.5*s, ext[0]*s
	if rt, _ := world.TreeTrunk(t.Species); rt*s > r {
		r = rt * s
	}
	return t.Position.Sub(mgl32.Vec3{r, 0, r}), t.Position.Add(mgl32.Vec3{r, h, r})
}

// pickTreeRayT returns where a picking ray enters t's pick box, or +Inf.
func pickTreeRayT(origin, dir mgl32.Vec3, t *world.Tree) float32 {
	lo, hi := treePickBox(t)
	tmin, tmax := float32(0), float32(math.Inf(1))
	for k := 0; k < 3; k++ {
		if absF32(dir[k]) < 1e-9 {
			if origin[k] < lo[k] || origin[k] > hi[k] {
				return float32(math.Inf(1))
			}
			continue
		}
		a, b := (lo[k]-origin[k])/dir[k], (hi[k]-origin[k])/dir[k]
		if a > b {
			a, b = b, a
		}
		tmin, tmax = max(tmin, a), min(tmax, b)
		if tmin > tmax {
			return float32(math.Inf(1))
		}
	}
	return tmin
}

// ---------- drawing ----------

// drawSelectedTreeOutline outlines the selected tree's crown cylinder and,
// when it is solid, the trunk box it collides as.
func drawSelectedTreeOutline() {
	t := activeTree()
	if t == nil {
		return
	}
	lo, hi := treePickBox(t)
	c := lo.Add(hi).Mul(0.5)
	r := (hi.X() - lo.X()) * 0.5
	const seg = 32
	ring := func(y float32) {
		prev := mgl32.Vec3{c.X() + r, y, c.Z()}
		for i := 1; i <= seg; i++ {
			a := float64(i) / seg * 2 * math.Pi
			p := mgl32.Vec3{c.X() + r*float32(math.Cos(a)), y, c.Z() + r*float32(math.Sin(a))}
			gizmo.GizmoLine(prev, p, treeSelColor)
			prev = p
		}
	}
	ring(lo.Y())
	ring(hi.Y())
	for i := 0; i < 4; i++ {
		a := float64(i) / 4 * 2 * math.Pi
		x, z := c.X()+r*float32(math.Cos(a)), c.Z()+r*float32(math.Sin(a))
		gizmo.GizmoLine(mgl32.Vec3{x, lo.Y(), z}, mgl32.Vec3{x, hi.Y(), z}, treeSelColor)
	}
	if blo, bhi, ok := t.TrunkBox(); ok {
		drawBoxLines(blo, bhi, treeTrunkColor)
	}
}

// drawBoxLines draws an axis-aligned box's twelve edges.
func drawBoxLines(lo, hi, col mgl32.Vec3) {
	p := func(i int) mgl32.Vec3 {
		v := lo
		if i&1 != 0 {
			v[0] = hi[0]
		}
		if i&2 != 0 {
			v[1] = hi[1]
		}
		if i&4 != 0 {
			v[2] = hi[2]
		}
		return v
	}
	for i := 0; i < 8; i++ {
		for _, b := range []int{1, 2, 4} {
			if i&b == 0 {
				gizmo.GizmoLine(p(i), p(i|b), col)
			}
		}
	}
}

// ---------- the Explorer ----------

// editorTreeAddMenu draws the tree half of the Explorer's Add menu.
func editorTreeAddMenu() {
	variants := []string{"Random variant", "Variant 1", "Variant 2", "Variant 3"}
	cur := Editor.NewTreeVariant + 1
	if cur < 0 || cur >= len(variants) {
		cur = 0
	}
	imgui.SetNextItemWidth(160)
	if imguibackend.BeginCombo("##newtreevar", variants[cur]) {
		for i, v := range variants {
			if imguibackend.SelectableV(v, i == cur, 0, imgui.Vec2{}) {
				Editor.NewTreeVariant = i - 1
			}
		}
		imguibackend.EndCombo()
	}
	imgui.Separator()
	for _, s := range treeSpecies {
		if !treeSpeciesIsTree(s) {
			continue
		}
		if imgui.MenuItem(treeSpeciesLabel(s) + "##placetree" + s.String()) {
			editorArmTreePlacement(s)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(world.FoliageShapeDocs[s] + "\nClick the ground to plant one; it stays armed.")
		}
	}
	if imgui.BeginMenu("Other plants") {
		for _, s := range treeSpecies {
			if treeSpeciesIsTree(s) {
				continue
			}
			if imgui.MenuItem(treeSpeciesLabel(s) + "##placeplant" + s.String()) {
				editorArmTreePlacement(s)
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(world.FoliageShapeDocs[s])
			}
		}
		imgui.EndMenu()
	}
}

// editorExplorerTreeNodes draws the level's placed trees.
func editorExplorerTreeNodes(lvl *world.Level) {
	if !editorExplorerCategory(fmt.Sprintf("Trees (%d)##trees", len(lvl.Trees)), panelFocusTree) {
		return
	}
	defer imgui.TreePop()
	if len(lvl.Trees) == 0 {
		editorTextMuted("(none)")
		return
	}
	const maxRows = 500
	del, dup := -1, -1
	for i := range lvl.Trees {
		if i == maxRows {
			editorTextMuted(fmt.Sprintf("... and %d more", len(lvl.Trees)-maxRows))
			break
		}
		t := &lvl.Trees[i]
		label := t.ID
		if label == "" {
			label = "(unnamed)"
		}
		label += fmt.Sprintf("  [%s %d]", treeSpeciesLabel(t.Species), t.EffectiveVariant()+1)
		if !t.Solid {
			label += "  (not solid)"
		}
		if editorExplorerLeaf(fmt.Sprintf("%s##tree%d", label, i), i == Editor.ActiveTreeIdx, panelFocusTree) {
			editorSelectTree(i)
		}
		editorExplorerMenu(fmt.Sprintf("treectx%d", i), func() {
			solid := t.Solid
			if imgui.MenuItemV("Solid", "", solid, true) {
				EditorPushUndo()
				t.Solid = !solid
			}
			if imgui.MenuItem("Duplicate") {
				dup = i
			}
			imgui.Separator()
			if imgui.MenuItem("Delete") {
				del = i
			}
		})
	}
	if dup >= 0 {
		editorSelectTree(dup)
		duplicateActiveTree()
	}
	if del >= 0 {
		editorSelectTree(del)
		deleteActiveTree()
	}
}

// ---------- the properties panel ----------

// editorTreePropertiesSection draws the selected tree's fields.
func editorTreePropertiesSection() {
	t := activeTree()
	if t == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusTree)
	if !imgui.CollapsingHeaderV("Tree: "+t.ID+"##treehdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusTree)
	editorPropGroupOpen("Identity##treeidgrp", func() { editorTreeIdentity(t) })
	editorPropGroupOpen("Transform##treetfgrp", func() { editorTreeTransform(t) })
	editorPropGroupOpen("Colour##treecolgrp", func() { editorTreeColours(t) })
	editorPropGroupOpen("Actions##treeactions", func() {
		if imgui.Button("Duplicate##treedup") {
			duplicateActiveTree()
		}
		imgui.SameLine()
		if imgui.Button("Delete##treedel") {
			deleteActiveTree()
		}
	})
}

// undoOnActivate pushes undo as the widget just drawn starts an edit: once
// per drag or text entry, rather than once per frame of it.
func undoOnActivate() {
	if imgui.IsItemActivated() {
		EditorPushUndo()
	}
}

func editorTreeIdentity(t *world.Tree) {
	id := t.ID
	if imgui.InputTextV("ID##treeid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != t.ID {
		lvl := Editor.level
		if id == "" {
			editorSetStatus("A tree needs an ID")
		} else if world.LevelIDTaken(lvl, id) {
			editorSetStatus("%q is already used by something else in this level", id)
		} else {
			EditorPushUndo()
			t.ID = id
		}
	}
	if imguibackend.BeginCombo("Species##treesp", treeSpeciesLabel(t.Species)) {
		for _, s := range treeSpecies {
			if imguibackend.SelectableV(treeSpeciesLabel(s)+"##sp"+s.String(), s == t.Species, 0, imgui.Vec2{}) && s != t.Species {
				EditorPushUndo()
				t.Species = s
				if t.Variant >= world.FoliageVariants[s] {
					t.Variant = 0
				}
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(world.FoliageShapeDocs[s])
			}
		}
		imguibackend.EndCombo()
	}
	if t.Species < world.FoliageShapeCount {
		n := world.FoliageVariants[t.Species]
		v := t.EffectiveVariant()
		if imguibackend.BeginCombo("Variant##treevar", fmt.Sprintf("%d of %d", v+1, n)) {
			for i := 0; i < n; i++ {
				if imguibackend.SelectableV(fmt.Sprintf("%d##treevar%d", i+1, i), i == v, 0, imgui.Vec2{}) && i != v {
					EditorPushUndo()
					t.Variant = i
				}
			}
			imguibackend.EndCombo()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(world.FoliageShapeDocs[t.Species])
		}
	}
	if imgui.Checkbox("Solid##treesolid", &t.Solid) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Its trunk stops the player, entities and shots.\nThe crown never does.")
	}
}

func editorTreeTransform(t *world.Tree) {
	pos := [3]float32{t.Position.X(), t.Position.Y(), t.Position.Z()}
	if imgui.DragFloat3V("Position##treepos", &pos, 0.05, 0, 0, "%.2f", 0) {
		t.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	undoOnActivate()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The foot of its trunk.")
	}
	yaw := t.Yaw * 180 / math.Pi
	yaw = float32(math.Mod(float64(yaw)+360, 360))
	if imgui.SliderFloatV("Turn (deg)##treeyaw", &yaw, 0, 360, "%.0f", 0) {
		t.Yaw = yaw * math.Pi / 180
	}
	undoOnActivate()
	sc := t.EffectiveScale()
	if imgui.SliderFloatV("Size##treescale", &sc, 0.2, 3, "%.2f", 0) {
		t.Scale = max(sc, 0.05)
	}
	undoOnActivate()
	if imgui.IsItemHovered() {
		_, h := world.TreeTrunk(t.Species)
		if h > 0 {
			imgui.SetTooltip(fmt.Sprintf("Against the species' own size; at 1 its trunk is about %.0f units tall.", h))
		}
	}
}

func editorTreeColours(t *world.Tree) {
	c, a := t.Colors()
	col := [3]float32{c[0], c[1], c[2]}
	if imgui.ColorEdit3V("Foliage##treecol", &col, 0) {
		t.Color = mgl32.Vec3{col[0], col[1], col[2]}
	}
	undoOnActivate()
	acc := [3]float32{a[0], a[1], a[2]}
	if imgui.ColorEdit3V("Moss / accent##treeacc", &acc, 0) {
		t.Accent = mgl32.Vec3{acc[0], acc[1], acc[2]}
	}
	undoOnActivate()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The moss on its limbs and the curtains hanging from them;\nberries or flowers on the plants that have them.")
	}
	if t.Color != (mgl32.Vec3{}) || t.Accent != (mgl32.Vec3{}) {
		if imgui.Button("Species colours##treecolreset") {
			EditorPushUndo()
			t.Color, t.Accent = mgl32.Vec3{}, mgl32.Vec3{}
		}
	} else {
		editorTextMuted("The species' own colours.")
	}
}

// editor3DTreeMenuItems is the viewport's right-click menu for a tree.
func editor3DTreeMenuItems() []ctxMenuItem {
	t := activeTree()
	if t == nil {
		return nil
	}
	return []ctxMenuItem{
		{Label: "Properties", Do: func() { editor3DFocusPanel(panelFocusTree) },
			Tooltip: "Open the tree's section: species, size, turn and colour."},
		{Label: "Move here", Do: func() { editor3DMoveTreeTo(editor3DMenuPoint) },
			Tooltip: "Put its foot where the ray under the cursor met the world."},
		{Label: "Plant more like this", Do: func() {
			Editor.NewTreeVariant = -1
			editorArmTreePlacement(t.Species)
		}, Tooltip: "Arm placement with this species, a random variant each."},
		{Label: "Solid", Checked: t.Solid, Do: func() { EditorPushUndo(); t.Solid = !t.Solid },
			Tooltip: "Its trunk stops the player, entities and shots."},
		{Label: "Duplicate", Do: duplicateActiveTree},
		{Separator: true},
		{Label: "Snap to grid", Disabled: !editor3DCanSnap(), Do: editor3DSnapSelectionToGrid},
		{Label: "Delete", Shortcut: "Del", Do: EditorDeleteSelected},
	}
}
