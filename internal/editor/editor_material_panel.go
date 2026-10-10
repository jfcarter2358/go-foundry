package editor

// editor_material_panel.go — the material editor's panels.
//
// The parameter panel is BUILT FROM THE SCHEMA. There is no code here that
// knows what a water wave or a cloud's coverage is: materialEffectParams walks
// material.Kind.Params and draws a widget per ParamDef according to its type,
// with its own range, its own default and its own tooltip. Adding a parameter
// to an effect — or a whole new effect kind — is a row in material_kinds.go
// and no UI work at all, which is the reason the schema exists.
//
// Two consequences worth stating, because both are deliberate:
//
//   - A parameter still at its kind's default is shown with a dimmed "default"
//     marker and is not written to the file. Clearing one (the x beside it)
//     puts it back. That is what keeps a .fmat readable — a material says what
//     an author changed, not what the engine happens to think today.
//   - The editor never validates by refusing. An effect in the wrong domain,
//     or one from a build that knew more kinds than this one, is shown with
//     what is known about it and reported in the status line. A document that
//     cannot be opened is worse than one that is drawn imperfectly.

import (
	"fmt"
	"strings"

	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/material"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// materialLiveUpdate publishes every edit into the running library
// immediately, so the level window's walls change as a slider moves rather
// than on save. On by default: it is the reason to have the two windows open
// at once. Turned off, only a save publishes.
var materialLiveUpdate = true

// materialAfterEdit is called after anything changes the document. It is what
// makes Live Update live, and it is deliberately the only route — an edit that
// forgets to call it shows in the swatch (which publishes its own preview copy
// every frame) but not in the level, which is exactly the kind of half-working
// that is hard to notice.
func materialAfterEdit() {
	if materialLiveUpdate {
		materialPublish()
	}
}

// ---------- the top bar ----------

func materialEditorToolbar() {
	w := materialEditorWindow
	if w == nil {
		return
	}
	editorTopBarBegin("##materialtoolbar", float32(w.W))

	if imgui.BeginMenuBar() {
		materialEditorMenus()
		editorMenuBarShared(editorKindMaterial)
		name := materialName()
		if name == "" {
			name = "(unsaved)"
		}
		editorMenuBarDoc(name, materialEditorDirty())
		imgui.EndMenuBar()
	}

	// Second row: what the preview is showing. All of it is in the View menu
	// too; here it is one click.
	imgui.SetNextItemWidth(110)
	if imguibackend.BeginCombo("##mshape", materialShapeNames[materialShape]) {
		for i, n := range materialShapeNames {
			if imguibackend.SelectableV(n, i == materialShape, 0, imgui.Vec2{}) {
				materialShape = i
			}
		}
		imguibackend.EndCombo()
	}
	imgui.SameLine()
	imgui.Checkbox("Spin##mspin", &materialSpin)
	imgui.SameLine()
	imgui.Checkbox("Grid##mgrid", &materialShowGrid)
	imgui.SameLine()
	imgui.Checkbox("Lit##mlit", &materialPreviewLit)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Shade the swatch with the preview's light rig.\n" +
			"Off shows the albedo flat — for checking art rather than lighting.\n" +
			"Not the same as the material's own Unlit effect, which is part of\n" +
			"the material and is always shown.")
	}
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	imgui.Checkbox("Live Update##mlive", &materialLiveUpdate)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Push every edit into the running level as it is made,\n" +
			"so walls, sprites and particles wearing this material change\n" +
			"in the level window without a save.")
	}
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	imgui.SetNextItemWidth(110)
	imgui.ColorEdit3V("BG##mbg", &materialBgColor, imgui.ColorEditFlagsNoInputs)
	if w != nil {
		w.ClearColor = [4]float32{materialBgColor[0], materialBgColor[1], materialBgColor[2], 1}
	}
	editorStatusText(materialStatus)

	imgui.End()
}

func materialEditorMenus() {
	if imgui.BeginMenu("File") {
		if imgui.MenuItemV("New...", "Ctrl+N", false, true) {
			materialShowNewDialog = true
			materialNewName = ""
		}
		if imgui.MenuItemV("Open...", "Ctrl+O", false, true) {
			materialShowOpenDialog = true
			materialFileList = nil
		}
		imgui.Separator()
		if imgui.MenuItemV("Save", "Ctrl+S", false, true) {
			materialSaveCurrent()
		}
		if imgui.MenuItem("Save As...") {
			materialSaveAsName = materialName()
			materialShowSaveAsDialog = true
		}
		editorSaveAllMenuItem()
		editorCloseWindowItem(CloseMaterialEditorWindow)
		imgui.EndMenu()
	}

	if imgui.BeginMenu("Edit") {
		editorUndoRedoItems(MaterialCanUndo(), MaterialCanRedo(), MaterialUndo, MaterialRedo)
		imgui.EndMenu()
	}

	if imgui.BeginMenu("View") {
		editorMenuToggle("Grid", "", &materialShowGrid)
		editorMenuToggle("Spin", "Space", &materialSpin)
		editorMenuToggle("Lit", "", &materialPreviewLit)
		imgui.Separator()
		for i, n := range materialShapeNames {
			if imgui.MenuItemV(n, "", i == materialShape, true) {
				materialShape = i
			}
		}
		imgui.EndMenu()
	}
}

// ---------- the material list ----------

// materialListWindow is the left column: every .fmat in the project, so
// switching between two materials being balanced against each other is one
// click rather than a trip through a file dialog.
func materialListWindow() {
	w := materialEditorWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: materialToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: materialListWidth, Y: float32(w.H) - materialToolbarH})
	imgui.BeginV("Materials##mlist", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoMove|
			imgui.WindowFlagsNoSavedSettings)

	imgui.Text("Materials")
	imgui.Separator()

	if materialFileList == nil {
		materialFileList = material.ScanMaterialFiles()
	}
	current := materialName()
	for _, name := range materialFileList {
		if !imguibackend.SelectableV(name, name == current, 0, imgui.Vec2{}) {
			continue
		}
		if name == current {
			continue
		}
		if err := materialLoad(material.MaterialPath(name)); err != nil {
			materialStatusf("%v", err)
		}
	}
	if len(materialFileList) == 0 {
		editorTextMutedWrapped("Nothing in " + material.MaterialsDirName + "/ yet. File > New, then Save.")
	}

	imgui.Separator()
	if imgui.Button("New##mnewbtn") {
		materialShowNewDialog = true
		materialNewName = ""
	}
	imgui.SameLine()
	if imgui.Button("Rescan##mrescan") {
		materialFileList = nil
		gfx.LoadMaterials()
		materialStatusf("Reloaded materials")
	}
	imgui.End()
}

// ---------- the parameter panel ----------

func materialEditorPanel() {
	w := materialEditorWindow
	if w == nil || materialDoc == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(w.W) - materialPanelWidth, Y: materialToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: materialPanelWidth, Y: float32(w.H) - materialToolbarH})
	imgui.BeginV("Material##mpanel", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|imgui.WindowFlagsNoMove|
			imgui.WindowFlagsNoSavedSettings)

	materialHeaderSection()
	imgui.Separator()
	materialEffectsSection()
	imgui.Separator()
	materialProblemsSection()

	imgui.End()
}

// materialHeaderSection is what the material IS: its domain, the texture it
// supplies, and a note for whoever opens it next.
func materialHeaderSection() {
	d := materialDoc

	imgui.Text("Name")
	imgui.SameLine()
	name := materialName()
	if name == "" {
		name = "(unsaved)"
	}
	editorTextMuted(name)

	// The domain decides which effects are legal, so changing it is a real
	// edit and not a view filter. Effects already on the document are kept
	// either way — Validate reports the ones that no longer belong, and
	// switching back makes them work again, which is friendlier than deleting
	// an author's work because they clicked the wrong row.
	domain := int32(d.Domain)
	imgui.SetNextItemWidth(160)
	if imguibackend.BeginCombo("Kind##mdomain", material.DomainName(d.Domain)) {
		for i, n := range material.DomainNames {
			if imguibackend.SelectableV(n, int32(i) == domain, 0, imgui.Vec2{}) && int32(i) != domain {
				MaterialPushUndo()
				d.Domain = material.Domain(i)
				materialAfterEdit()
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("What may wear this material.\n" +
			"surface: brush faces, compiled surfaces, models, entity attachments.\n" +
			"sprite: billboards.  particle: one layer of a .fpar.  sky: the sky and its clouds.")
	}

	if d.Domain != material.DomainSky {
		tex := d.Texture
		imgui.SetNextItemWidth(200)
		if textureCombo("Texture##mtex", &tex, true) {
			MaterialPushUndo()
			d.Texture = tex
			materialAfterEdit()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("The albedo this material supplies.\n" +
				"Leave it as (none) and whatever wears the material keeps its own —\n" +
				"which is what lets one material be worn by surfaces of different art.")
		}
	}

	note := d.Doc
	imgui.SetNextItemWidth(-1)
	if imgui.InputTextV("##mdocnote", &note, 0, nil) && note != d.Doc {
		MaterialPushUndo()
		d.Doc = note
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A note for whoever opens this next. Saved with the material.")
	}
}

// materialEffectsSection is the effect stack: what the material does.
func materialEffectsSection() {
	d := materialDoc

	imgui.Text("Effects")
	imgui.SameLine()
	if imgui.Button("Add...##maddfx") {
		imgui.OpenPopup("addeffect##maddfxpopup")
	}
	materialAddEffectPopup()

	if len(d.Effects) == 0 {
		editorTextMutedWrapped("No effects yet. A material with none supplies only its texture. " +
			"Add one to give it behaviour.")
		return
	}

	remove := -1
	for i := range d.Effects {
		e := &d.Effects[i]
		imgui.PushID(fmt.Sprintf("fx%d", i))

		// Where this row starts and how wide it is, taken before anything is
		// drawn into it — the remove button below is placed against its right
		// edge, and the collapsing header has already eaten the whole width by
		// the time it could be measured from there.
		rowX := imgui.CursorPosX()
		rowW := imgui.ContentRegionAvail().X

		// The enable box outside the header, so an effect can be switched off
		// without opening it — which is how you compare with and without.
		on := !e.Disabled
		if imgui.Checkbox("##mfxon", &on) {
			MaterialPushUndo()
			e.Disabled = !on
			materialAfterEdit()
		}
		imgui.SameLine()

		kind := material.KindByName(e.Kind)
		label := e.Kind
		if kind != nil {
			label = kind.Label
		}
		if e.Disabled {
			label += " (off)"
		}
		open := imgui.CollapsingHeaderV(label+"##mfxhdr", imgui.TreeNodeFlagsDefaultOpen)
		if imgui.BeginPopupContextItem() {
			if imgui.Selectable("Remove") {
				remove = i
			}
			if i > 0 && imgui.Selectable("Move up") {
				MaterialPushUndo()
				d.Effects[i-1], d.Effects[i] = d.Effects[i], d.Effects[i-1]
				materialAfterEdit()
			}
			if i+1 < len(d.Effects) && imgui.Selectable("Move down") {
				MaterialPushUndo()
				d.Effects[i+1], d.Effects[i] = d.Effects[i], d.Effects[i+1]
				materialAfterEdit()
			}
			imgui.EndPopup()
		}

		// Remove, against the right edge of the header and on top of it. The
		// right-click menu above has the same item and is where this started;
		// a menu you have to know is there is not a way to delete something.
		// It is placed rather than SameLine'd because the header has taken the
		// full width, and drawn AFTER the header so the click reaches the
		// button rather than toggling the fold underneath it.
		imgui.SameLineV(rowX+rowW-materialRemoveButtonW(), 0)
		if imgui.Button("x##mfxdel") {
			remove = i
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Remove this effect.\nCtrl+Z puts it back.")
		}

		if open {
			materialEffectBody(e, kind)
		}
		imgui.PopID()
	}
	if remove >= 0 {
		MaterialPushUndo()
		d.Effects = append(d.Effects[:remove], d.Effects[remove+1:]...)
		materialAfterEdit()
	}
}

// materialRemoveButtonW is how wide the "x" button comes out, from the font and
// the current style rather than a guess — a hardcoded width clips the glyph at
// one UI scale and floats away from the edge at another.
func materialRemoveButtonW() float32 {
	return imgui.CalcTextSize("x", false, 0).X + imgui.CurrentStyle().FramePadding().X*2
}

// materialEffectBody draws one effect's documentation and its parameters.
func materialEffectBody(e *material.Effect, kind *material.Kind) {
	imgui.Indent()
	defer imgui.Unindent()

	if kind == nil {
		editorTextWrappedColored(materialWarnColor,
			"This build does not know the effect \""+e.Kind+"\". Its settings are kept and "+
				"written back on save, so opening the material here and saving it loses nothing.")
		return
	}
	if !kind.InDomain(materialDoc.Domain) {
		editorTextWrappedColored(materialWarnColor,
			"Not available on a "+material.DomainName(materialDoc.Domain)+
				" material. It is kept, but nothing reads it.")
	}
	editorTextMutedWrapped(kind.Doc)
	if len(kind.Params) == 0 {
		editorTextMutedWrapped("No settings — having the effect at all is the setting.")
		return
	}
	materialEffectParams(e, kind)
}

// materialEffectParams is the schema-driven part: one widget per ParamDef,
// chosen by its type. Nothing here knows what any particular parameter means.
func materialEffectParams(e *material.Effect, kind *material.Kind) {
	for i := range kind.Params {
		p := &kind.Params[i]
		imgui.PushID(p.Name)

		_, isSet := e.Params[p.Name]
		cur := e.Param(p.Name)
		label := p.Label
		if label == "" {
			label = p.Name
		}

		if materialParamWidget(p, label, &cur) {
			MaterialPushUndo()
			e.Set(p.Name, p.Clamp(cur))
			materialAfterEdit()
		}
		if imgui.IsItemHovered() && p.Doc != "" {
			imgui.SetTooltip(wrapTooltip(p.Doc))
		}

		// The reset, only where there is something to reset: a parameter at its
		// default is not in the file, and the marker is how an author can see
		// that without opening it.
		imgui.SameLine()
		if isSet {
			if imgui.Button("x##mreset") {
				MaterialPushUndo()
				e.Clear(p.Name)
				materialAfterEdit()
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Back to the default, and out of the saved file.")
			}
		} else {
			editorTextMuted("default")
		}
		imgui.PopID()
	}
}

// materialParamWidget draws the widget for one parameter and reports whether it
// changed. The whole type switch of the editor, in one place.
func materialParamWidget(p *material.ParamDef, label string, v *material.Value) bool {
	imgui.SetNextItemWidth(150)
	switch p.Type {
	case material.ParamFloat:
		f := v.Num[0]
		min, max := p.Min, p.Max
		if min == max {
			// Unbounded in the schema: give the drag a workable span rather
			// than clamping to zero, which is what a 0..0 range would do.
			min, max = -1e6, 1e6
		}
		if editorDragFloat(label, &f, min, max, "%.3f") {
			*v = material.FloatValue(f)
			return true
		}
	case material.ParamVec2:
		xy := [2]float32{v.Num[0], v.Num[1]}
		if imgui.DragFloat2V(label, &xy, 0.01, p.Min, p.Max, "%.3f", 0) {
			*v = material.Vec2Value(mgl32.Vec2{xy[0], xy[1]})
			return true
		}
	case material.ParamVec3:
		xyz := [3]float32{v.Num[0], v.Num[1], v.Num[2]}
		if imgui.DragFloat3V(label, &xyz, 0.01, p.Min, p.Max, "%.3f", 0) {
			*v = material.Vec3Value(mgl32.Vec3{xyz[0], xyz[1], xyz[2]})
			return true
		}
	case material.ParamColor:
		rgb := [3]float32{v.Num[0], v.Num[1], v.Num[2]}
		if imgui.ColorEdit3V(label, &rgb, imgui.ColorEditFlagsNoInputs) {
			*v = material.ColorValue(mgl32.Vec3{rgb[0], rgb[1], rgb[2]})
			return true
		}
	case material.ParamBool:
		b := v.Bool
		if imgui.Checkbox(label, &b) {
			*v = material.BoolValue(b)
			return true
		}
	case material.ParamTexture:
		tex := v.Str
		if textureCombo(label, &tex, true) {
			*v = material.TextureValue(tex)
			return true
		}
	case material.ParamEnum:
		if imguibackend.BeginCombo(label, v.Str) {
			changed := false
			for _, c := range p.Choices {
				if imguibackend.SelectableV(c, c == v.Str, 0, imgui.Vec2{}) && c != v.Str {
					*v = material.EnumValue(c)
					changed = true
				}
			}
			imguibackend.EndCombo()
			return changed
		}
	}
	return false
}

// materialProblemsSection is Doc.Validate's report. Advisory: nothing here
// refuses to save, because a document that cannot be written is worse than one
// with a parameter nothing reads.
func materialProblemsSection() {
	problems := materialDoc.Validate()
	if len(problems) == 0 {
		editorTextMuted("No problems.")
		return
	}
	imgui.Text("Problems")
	for _, p := range problems {
		editorTextWrappedColored(materialWarnColor, "- "+p)
	}
}

var materialWarnColor = imgui.Vec4{X: 0.95, Y: 0.72, Z: 0.30, W: 1}

// materialAddEffectPopup lists every effect kind legal in this material's
// domain, with the ones already on it greyed. Built from KindsFor, so a new
// kind appears here the moment it is registered.
func materialAddEffectPopup() {
	if !imgui.BeginPopup("addeffect##maddfxpopup") {
		return
	}
	for _, k := range material.KindsFor(materialDoc.Domain) {
		have := materialDoc.Find(k.Name) != nil
		label := k.Label
		if have {
			label += "  (already on)"
		}
		if imguibackend.SelectableV(label, false, 0, imgui.Vec2{}) && !have {
			MaterialPushUndo()
			materialDoc.AddEffect(k.Name)
			materialAfterEdit()
			imgui.CloseCurrentPopup()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(wrapTooltip(k.Doc))
		}
	}
	imgui.EndPopup()
}

// wrapTooltip breaks a schema doc string into tooltip-width lines. The schema
// keeps its text as one paragraph, because it is also read by `foundry check`
// and by the documentation generator, neither of which wants an editor's line
// breaks baked in.
func wrapTooltip(s string) string {
	const width = 72
	var b strings.Builder
	col := 0
	for _, word := range strings.Fields(s) {
		if col > 0 && col+1+len(word) > width {
			b.WriteByte('\n')
			col = 0
		} else if col > 0 {
			b.WriteByte(' ')
			col++
		}
		b.WriteString(word)
		col += len(word)
	}
	return b.String()
}

// ---------- dialogs ----------

func materialDialogs() {
	imguibackend.WithModalPopup("New Material##mnewdlg", &materialShowNewDialog, func() {
		imgui.InputTextV("Name##mnewname", &materialNewName, 0, nil)
		imgui.SetNextItemWidth(160)
		if imguibackend.BeginCombo("Kind##mnewkind", material.DomainName(material.Domain(materialNewDomain))) {
			for i, n := range material.DomainNames {
				if imguibackend.SelectableV(n, int32(i) == materialNewDomain, 0, imgui.Vec2{}) {
					materialNewDomain = int32(i)
				}
			}
			imguibackend.EndCombo()
		}
		editorTextMutedWrapped("Written to " + material.MaterialsDirName + "/<name>" + material.MaterialExt +
			" when you save.")
		if imgui.Button("Create##mnewgo") && materialNewName != "" {
			materialDoc = materialNewDoc(materialNewName, material.Domain(materialNewDomain))
			materialPath = ""
			MaterialClearUndoHistory()
			materialMarkSaved()
			materialStatusf("New %s material %q — not saved yet",
				material.DomainName(material.Domain(materialNewDomain)), materialNewName)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##mnewcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Open Material##mopendlg", &materialShowOpenDialog, func() {
		if materialFileList == nil {
			materialFileList = material.ScanMaterialFiles()
		}
		if len(materialFileList) == 0 {
			editorTextMutedWrapped("Nothing in " + material.MaterialsDirName + "/ yet. Make one first.")
		}
		for _, name := range materialFileList {
			if !imguibackend.Selectable(name) {
				continue
			}
			if err := materialLoad(material.MaterialPath(name)); err != nil {
				materialStatusf("%v", err)
			}
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel##mopencancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Save Material As##msavedlg", &materialShowSaveAsDialog, func() {
		imgui.InputTextV("Name##msavename", &materialSaveAsName, 0, nil)
		editorTextMutedWrapped("Written to " + material.MaterialsDirName + "/<name>" + material.MaterialExt)
		if imgui.Button("Save##msavego") && materialSaveAsName != "" {
			if err := materialSave(material.MaterialPath(materialSaveAsName)); err != nil {
				materialStatusf("%v", err)
			}
			// The Open dialog's cache is stale the moment a new file exists.
			materialFileList = nil
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##msavecancel") {
			imgui.CloseCurrentPopup()
		}
	})
}
