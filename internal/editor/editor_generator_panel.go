package editor

// editor_generator_panel.go — the Generator editor's panels: the top bar,
// the generator and stage lists on the left, the selected stage's parameters
// on the right, the footer under the viewport, and the File dialogs.
//
// The stage list is the document's spine: add (from a menu built from the
// registered kinds, by category), remove, duplicate, reorder, enable, select.
// Each row says whether the stage is stale — what a plain Run would run —
// from the manifest beside its outputs.
//
// Nothing here knows any stage kind. The parameter panel is
// editor_generator_params.go, driven by the kind's schema.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/procgen"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

var genWarnColor = imgui.Vec4{X: 0.95, Y: 0.72, Z: 0.30, W: 1}
var genStaleColor = imgui.Vec4{X: 0.95, Y: 0.60, Z: 0.35, W: 1}
var genFreshColor = imgui.Vec4{X: 0.45, Y: 0.75, Z: 0.50, W: 1}

const genPanelFlags = imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize | imgui.WindowFlagsNoMove |
	imgui.WindowFlagsNoSavedSettings

// ---------- the top bar ----------

func generatorToolbar() {
	w := generatorEditorWindow
	if w == nil {
		return
	}
	editorTopBarBegin("##gentoolbar", float32(w.W))
	if imgui.BeginMenuBar() {
		generatorMenus()
		editorMenuBarShared(editorKindGenerator)
		name := genName()
		if genPath == "" {
			name += " (unsaved)"
		}
		editorMenuBarDoc(name, generatorEditorDirty())
		imgui.EndMenuBar()
	}

	// Row two: run, view mode.
	imguibackend.EditorPushDisabled(GeneratorRunning())
	if imgui.Button("Run##genrun") {
		genBeginRun(false, nil)
	}
	editorMenuTooltip("Run every stage that is stale (F5), then splice the placements into the level.")
	imgui.SameLine()
	st := genSelectedStage()
	imguibackend.EditorPushDisabled(st == nil)
	if imgui.Button("Run Stage##genrunstage") && st != nil {
		genBeginRun(false, []string{st.ID})
	}
	imguibackend.EditorPopDisabled()
	editorMenuTooltip("Rerun just the selected stage (and any stale stage it reads from).")
	imgui.SameLine()
	if imgui.Button("Force##genforce") {
		genConfirmForce = true
	}
	editorMenuTooltip("Rerun every stage, fresh or not (Ctrl+F5).")
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	if editorActiveToggleButton("2D##genv2d", genViewMode == genView2D) {
		genViewMode = genView2D
	}
	editorMenuTooltip("Top-down: the preview's images, points and paths. Edit splines here. (Tab)")
	imgui.SameLine()
	if editorActiveToggleButton("3D##genv3d", genViewMode == genView3D) {
		genViewMode = genView3D
	}
	editorMenuTooltip("The preview's meshes, lit. Drag to orbit, middle-drag to pan, wheel to zoom. (Tab)")
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(genDoc.Level == "")
	if imgui.Button("Open Level##genopenlevel") {
		genOpenTargetLevel()
	}
	imguibackend.EditorPopDisabled()
	editorMenuTooltip("Open the generator's target level in the level editor.")
	if busy, frac := genPreviewBusy(); busy {
		imgui.SameLine()
		editorTextMuted(fmt.Sprintf("previewing %d%%", int(frac*100)))
	}
	editorStatusText(genStatus)
	imgui.End()
}

func generatorMenus() {
	if imgui.BeginMenu("File") {
		if imgui.MenuItemV("New...", "Ctrl+N", false, true) {
			genShowNew, genNewName = true, ""
		}
		if imgui.MenuItemV("Open...", "Ctrl+O", false, true) {
			genShowOpen, genFileList = true, nil
		}
		genGenerateWorldMenuItem()
		imgui.Separator()
		if imgui.MenuItemV("Save", "Ctrl+S", false, true) {
			genSaveCurrent()
		}
		if imgui.MenuItem("Save As...") {
			genSaveAsName = genName()
			genShowSaveAs = true
		}
		editorSaveAllMenuItem()
		editorCloseWindowItem(CloseGeneratorEditorWindow)
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Edit") {
		editorUndoRedoItems(GeneratorCanUndo(), GeneratorCanRedo(), GeneratorUndo, GeneratorRedo)
		imgui.EndMenu()
	}
	if imgui.BeginMenu("View") {
		if imgui.MenuItemV("Top-down 2D", "Tab", genViewMode == genView2D, true) {
			genViewMode = genView2D
		}
		if imgui.MenuItemV("3D", "Tab", genViewMode == genView3D, true) {
			genViewMode = genView3D
		}
		imgui.Separator()
		if imgui.MenuItem("Frame Preview") {
			genView2DFrame()
			genView3DFrame()
		}
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Run") {
		running := GeneratorRunning()
		if imgui.MenuItemV("Run", "F5", false, !running) {
			genBeginRun(false, nil)
		}
		if st := genSelectedStage(); imgui.MenuItemV("Run Stage", "", false, !running && st != nil) {
			genBeginRun(false, []string{st.ID})
		}
		// A level generation doc: run one environment region's stages, and
		// splice only its placements (docs/regions/PHASE_4.md).
		if rs := genRegionIDs(); len(rs) > 0 && imgui.BeginMenu("Run Region") {
			for _, id := range rs {
				if imgui.MenuItemV(id, "", false, !running) {
					genBeginRegionRun(id)
				}
			}
			imgui.EndMenu()
		}
		if imgui.MenuItemV("Force Run All", "Ctrl+F5", false, !running) {
			genConfirmForce = true
		}
		if imgui.MenuItemV("Cancel", "", false, running) {
			genCancelRun()
		}
		imgui.EndMenu()
	}
}

// genOpenTargetLevel opens the document's level in the level editor.
func genOpenTargetLevel() {
	if genDoc == nil || genDoc.Level == "" {
		return
	}
	path := levelio.LevelPath(genDoc.Level)
	uiwin.RequestWindowOpen(func() {
		raiseMainWindow()
		if genLevelIsOpen(path) {
			return
		}
		if editorLevelDirty() {
			editorSetStatus("Save or discard the open level first, then open %s", genDoc.Level)
			genStatusf("The open level has unsaved changes; save it, then Open Level")
			return
		}
		EditorBeginLoadLevel(path)
	})
}

// ---------- the left column ----------

func generatorListWindow() {
	w := generatorEditorWindow
	if w == nil {
		return
	}
	h := float32(w.H) - generatorToolbarH
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: generatorToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: generatorListWidth, Y: h})
	imgui.BeginV("Generators##genlist", nil, genPanelFlags)

	imgui.Text("Generators")
	imgui.Separator()
	if genFileList == nil {
		genFileList = procgen.ScanDocs()
	}
	imgui.BeginChildV("##genfiles", imgui.Vec2{X: 0, Y: h * 0.28}, false, 0)
	current := genName()
	for _, name := range genFileList {
		if imguibackend.SelectableV(name, name == current && genPath != "", 0, imgui.Vec2{}) && name != current {
			if err := genLoad(procgen.DocPath(name)); err != nil {
				genStatusf("%v", err)
			}
		}
	}
	if len(genFileList) == 0 {
		editorTextMutedWrapped("Nothing in " + procgen.GeneratorsDirName + "/ yet. File > New, then Save.")
	}
	imgui.EndChild()
	imgui.Separator()
	generatorStageList()
	imgui.End()
}

// generatorStageList is the document's stages.
func generatorStageList() {
	d := genDoc
	imgui.Text("Stages")
	imgui.SameLine()
	if imgui.Button("Add...##genadd") {
		imgui.OpenPopup("##genaddstage")
	}
	genAddStagePopup()

	if len(d.Stages) == 0 {
		editorTextMutedWrapped("No stages. Add one: each makes something — a terrain, rocks, a scatter — " +
			"and later stages can read earlier ones.")
		return
	}
	act, actAt := "", -1
	for i := range d.Stages {
		s := &d.Stages[i]
		imgui.PushID(fmt.Sprintf("st%d", i))
		on := !s.Disabled
		if imgui.Checkbox("##on", &on) {
			genBeginEdit(fmt.Sprintf("enable%d", i))
			s.Disabled = !on
			genAfterEdit()
		}
		editorMenuTooltip("Enabled: runs, and its placements are spliced.")
		imgui.SameLine()
		k := procgen.KindByName(s.Kind)
		label := s.ID + "  ·  " + s.Kind
		if k == nil {
			label += " (unknown)"
		}
		if imguibackend.SelectableV(label+"##sel", i == genSelected, 0, imgui.Vec2{X: generatorListWidth - 70}) {
			if genSelected != i {
				genSelected = i
				genSpline = genSplineRef{}
				genRequestPreview()
			}
		}
		if imgui.BeginPopupContextItem() {
			if imgui.Selectable("Duplicate") {
				act, actAt = "dup", i
			}
			if i > 0 && imgui.Selectable("Move up") {
				act, actAt = "up", i
			}
			if i+1 < len(d.Stages) && imgui.Selectable("Move down") {
				act, actAt = "down", i
			}
			if imgui.Selectable("Remove") {
				act, actAt = "remove", i
			}
			imgui.EndPopup()
		}
		imgui.SameLine()
		switch {
		case s.Disabled:
			editorTextMuted("off")
		case genStageStale(s.ID):
			editorTextColored(genStaleColor, "stale")
		default:
			editorTextColored(genFreshColor, "ok")
		}
		imgui.PopID()
	}
	// The row actions, as buttons too: a menu you have to know is there is
	// not a way to do something.
	if st := genSelectedStage(); st != nil {
		i := genSelected
		if imgui.Button("Dup##gendup") {
			act, actAt = "dup", i
		}
		imgui.SameLine()
		imguibackend.EditorPushDisabled(i == 0)
		if imgui.Button("Up##genup") {
			act, actAt = "up", i
		}
		imguibackend.EditorPopDisabled()
		imgui.SameLine()
		imguibackend.EditorPushDisabled(i+1 >= len(d.Stages))
		if imgui.Button("Down##gendown") {
			act, actAt = "down", i
		}
		imguibackend.EditorPopDisabled()
		imgui.SameLine()
		if imgui.Button("Remove##genrm") {
			act, actAt = "remove", i
		}
	}
	if act != "" {
		genStageAction(act, actAt)
	}
}

// genStageAction applies one stage-list action, as one undo step.
func genStageAction(act string, i int) {
	d := genDoc
	if i < 0 || i >= len(d.Stages) {
		return
	}
	GeneratorPushUndo()
	switch act {
	case "dup":
		s := d.Stages[i]
		s.Params = s.Params.Clone()
		s.ID = d.UniqueStageID(s.ID)
		d.Stages = append(d.Stages[:i+1], append([]procgen.Stage{s}, d.Stages[i+1:]...)...)
		genSelected = i + 1
	case "up":
		d.Stages[i-1], d.Stages[i] = d.Stages[i], d.Stages[i-1]
		genSelected = i - 1
	case "down":
		d.Stages[i+1], d.Stages[i] = d.Stages[i], d.Stages[i+1]
		genSelected = i + 1
	case "remove":
		d.Stages = append(d.Stages[:i], d.Stages[i+1:]...)
		if genSelected >= len(d.Stages) {
			genSelected = len(d.Stages) - 1
		}
	}
	genSpline = genSplineRef{}
	genAfterEdit()
}

// genAddStagePopup lists every registered kind by category.
func genAddStagePopup() {
	if !imgui.BeginPopup("##genaddstage") {
		return
	}
	cat := ""
	for _, k := range procgen.Kinds() {
		if k.Category != cat {
			cat = k.Category
			editorTextMuted(cat)
		}
		if imguibackend.SelectableV("  "+k.DisplayLabel()+"##add"+k.Name, false, 0, imgui.Vec2{}) {
			GeneratorPushUndo()
			s := genDoc.NewStage(k.Name)
			at := genSelected + 1
			if at <= 0 || at > len(genDoc.Stages) {
				at = len(genDoc.Stages)
			}
			genDoc.Stages = append(genDoc.Stages[:at], append([]procgen.Stage{s}, genDoc.Stages[at:]...)...)
			genSelected = at
			genSpline = genSplineRef{}
			genAfterEdit()
			imgui.CloseCurrentPopup()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(wrapTooltip(k.Doc))
		}
	}
	imgui.EndPopup()
}

// ---------- the right column ----------

func generatorPanel() {
	w := generatorEditorWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(w.W) - generatorPanelWidth, Y: generatorToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: generatorPanelWidth, Y: float32(w.H) - generatorToolbarH})
	imgui.BeginV("Stage##genpanel", nil, genPanelFlags)
	generatorDocSection()
	imgui.Separator()
	// A level generation doc's regions (editor_generator_region_list.go).
	generatorRegionsSection()
	generatorStageSection()
	imgui.End()
}

// generatorDocSection is the document's own fields.
func generatorDocSection() {
	d := genDoc
	if imgui.CollapsingHeaderV("Generator##gendochdr", imgui.TreeNodeFlagsDefaultOpen) {
		note := d.Doc
		imgui.SetNextItemWidth(-1)
		if imgui.InputTextV("##gendocnote", &note, 0, nil) && note != d.Doc {
			genBeginEdit("docnote")
			d.Doc = note
			genAfterEdit()
		}
		editorMenuTooltip("A note for whoever opens this next.")

		seed := int32(d.Seed)
		imgui.SetNextItemWidth(140)
		if imgui.InputIntV("Seed##gendocseed", &seed, 1, 100, 0) {
			genBeginEdit("docseed")
			d.Seed = int64(seed)
			genAfterEdit()
		}
		editorMenuTooltip("Seeds every stage. Each stage's own seed is this, its ID and its seed params, hashed.")
		imgui.SameLine()
		if imgui.Button("Dice##gendocdice") {
			GeneratorPushUndo()
			d.Seed = genRandomSeed()
			genAfterEdit()
		}

		out := d.Output
		imgui.SetNextItemWidth(220)
		if imgui.InputTextV("Output##gendocout", &out, 0, nil) && out != d.Output {
			genBeginEdit("docout")
			d.Output = out
			genAfterEdit()
		}
		editorMenuTooltip("The folder under resources/ the outputs go in. Empty: " + procgen.DefaultOutputRoot + "/<name>.\nNow: " + d.OutputDir())

		level := d.Level
		imgui.SetNextItemWidth(220)
		if imguibackend.BeginCombo("Level##gendoclevel", genOr(level, "(none)")) {
			if imguibackend.SelectableV("(none)", level == "", 0, imgui.Vec2{}) && level != "" {
				GeneratorPushUndo()
				d.Level = ""
				genAfterEdit()
			}
			for _, name := range genLevelNames() {
				if imguibackend.SelectableV(name, name == level, 0, imgui.Vec2{}) && name != level {
					GeneratorPushUndo()
					d.Level = name
					genAfterEdit()
				}
			}
			imguibackend.EndCombo()
		}
		editorMenuTooltip("The level whose scenery receives the placements, each ID prefixed\n" +
			procgen.IDPrefix(genName()) + "<stage>. — a run replaces exactly those.")
	}
}

// generatorStageSection is the selected stage: its ID, its kind's blurb, its
// parameters.
func generatorStageSection() {
	s := genSelectedStage()
	if s == nil {
		editorTextMutedWrapped("Select a stage to edit its parameters.")
		return
	}
	k := procgen.KindByName(s.Kind)
	id := s.ID
	imgui.SetNextItemWidth(160)
	if imgui.InputTextV("ID##genstageid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != s.ID {
		GeneratorPushUndo()
		genRenameStage(s.ID, id)
		genAfterEdit()
	}
	editorMenuTooltip("Unique in the generator: names the stage's output folder, its placements'\n" +
		"ID prefix, and what later stages' refs say. Enter to apply; refs follow the rename.")
	imgui.SameLine()
	editorTextMuted(s.Kind)
	if k == nil {
		editorTextWrappedColored(genWarnColor, "This build has no stage kind \""+s.Kind+"\". Its settings are kept and written back on save.")
		return
	}
	editorTextMutedWrapped(k.Doc)
	imgui.Separator()
	genParamsPanel(s, k)
}

// genRenameStage renames a stage and every ref to it.
func genRenameStage(from, to string) {
	for i := range genDoc.Stages {
		st := &genDoc.Stages[i]
		if st.ID == from {
			st.ID = to
		}
		if k := procgen.KindByName(st.Kind); k != nil {
			genRenameRefs(k.Params, st.Params, from, to)
		}
	}
}

func genRenameRefs(defs []procgen.ParamDef, p procgen.Params, from, to string) {
	for i := range defs {
		d := &defs[i]
		v, ok := p[d.Name]
		if !ok {
			continue
		}
		switch d.Type {
		case procgen.ParamStage:
			if v.Str == from {
				v.Str = to
				p[d.Name] = v
			}
		case procgen.ParamList:
			for _, it := range v.Items {
				genRenameRefs(d.Fields, it, from, to)
			}
		}
	}
}

// ---------- the footer ----------

func generatorFooter() {
	w := generatorEditorWindow
	if w == nil {
		return
	}
	x := generatorListWidth
	width := float32(w.W) - generatorListWidth - generatorPanelWidth
	imgui.SetNextWindowPos(imgui.Vec2{X: x, Y: float32(w.H) - generatorFooterH})
	imgui.SetNextWindowSize(imgui.Vec2{X: width, Y: generatorFooterH})
	imgui.BeginV("##genfooter", nil, genPanelFlags)
	probs := genDoc.Validate()
	for _, p := range probs {
		editorTextWrappedColored(genWarnColor, "- "+p.String())
	}
	if p := genCurrentPreview(); p != nil {
		for _, n := range p.Notes {
			editorTextMuted(n)
		}
		editorTextMuted(fmt.Sprintf("preview %s", formatCompileDuration(p.Elapsed)))
	} else if genPreview.err != nil && genSelectedStage() != nil && genPreview.dataStage == genSelectedStage().ID {
		editorTextWrappedColored(genWarnColor, "Preview: "+genPreview.err.Error())
	} else if st := genSelectedStage(); st != nil {
		if k := procgen.KindByName(st.Kind); k != nil && k.Preview == nil {
			editorTextMuted("This stage has no preview. Run it to see its outputs.")
		}
	}
	if genRunJob.last != "" {
		editorTextMuted(genRunJob.last)
	}
	if len(probs) == 0 && genStaleErr == nil && genDoc.Level != "" {
		editorTextMuted("Placements go to level " + genDoc.Level + " as " + procgen.IDPrefix(genName()) + "<stage>.*")
	}
	imgui.End()
}

// ---------- dialogs ----------

func generatorDialogs() {
	imguibackend.WithModalPopup("New Generator##gennewdlg", &genShowNew, func() {
		imgui.InputTextV("Name##gennewname", &genNewName, 0, nil)
		imgui.SetNextItemWidth(200)
		t := procgen.TemplateByName(genNewTemplate)
		if imguibackend.BeginCombo("Template##gennewtpl", genTemplateLabel(t)) {
			for _, tp := range procgen.Templates() {
				if imguibackend.SelectableV(genTemplateLabel(tp), tp.Name == genNewTemplate, 0, imgui.Vec2{}) {
					genNewTemplate = tp.Name
				}
				if imgui.IsItemHovered() {
					imgui.SetTooltip(wrapTooltip(tp.Doc))
				}
			}
			imguibackend.EndCombo()
		}
		ok := procgen.ValidDocName(genNewName)
		if genNewName != "" && !ok {
			editorTextWrappedColored(genWarnColor, "Lower-case letters, digits and underscores, starting with a letter: the name is part of every ID the generator places.")
		}
		editorTextMutedWrapped("Written to " + procgen.GeneratorsDirName + "/<name>" + procgen.Ext + " when you save.")
		imguibackend.EditorPushDisabled(!ok)
		if imgui.Button("Create##gennewgo") && ok {
			d, err := procgen.NewFromTemplate(genNewTemplate, genNewName)
			if err != nil {
				genStatusf("%v", err)
			} else {
				genSetDoc(d, "")
				genStatusf("New generator %q — not saved yet", genNewName)
			}
			imgui.CloseCurrentPopup()
		}
		imguibackend.EditorPopDisabled()
		imgui.SameLine()
		if imgui.Button("Cancel##gennewcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Open Generator##genopendlg", &genShowOpen, func() {
		if genFileList == nil {
			genFileList = procgen.ScanDocs()
		}
		if len(genFileList) == 0 {
			editorTextMutedWrapped("Nothing in " + procgen.GeneratorsDirName + "/ yet.")
		}
		// Through the backend's Selectable, which does not dismiss the modal
		// on click by itself (see imgui-selectable-closes-modals).
		for _, name := range genFileList {
			if !imguibackend.Selectable(name + "##genopen") {
				continue
			}
			if err := genLoad(procgen.DocPath(name)); err != nil {
				genStatusf("%v", err)
			}
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if imgui.Button("Cancel##genopencancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Save Generator As##gensavedlg", &genShowSaveAs, func() {
		imgui.InputTextV("Name##gensavename", &genSaveAsName, 0, nil)
		ok := procgen.ValidDocName(genSaveAsName)
		if genSaveAsName != "" && !ok {
			editorTextWrappedColored(genWarnColor, "Lower-case letters, digits and underscores, starting with a letter.")
		}
		if genPath != "" && genSaveAsName != genName() {
			editorTextMutedWrapped("A new name is a new generator: its placements get a new ID prefix, " +
				"and the level keeps the old name's until you remove them.")
		}
		editorTextMutedWrapped("Written to " + procgen.GeneratorsDirName + "/<name>" + procgen.Ext)
		imguibackend.EditorPushDisabled(!ok)
		if imgui.Button("Save##gensavego") && ok {
			if err := genSave(procgen.DocPath(genSaveAsName)); err != nil {
				genStatusf("%v", err)
			}
			imgui.CloseCurrentPopup()
		}
		imguibackend.EditorPopDisabled()
		imgui.SameLine()
		if imgui.Button("Cancel##gensavecancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Force Run##genforcedlg", &genConfirmForce, func() {
		imgui.Text("Rerun every stage, even the up-to-date ones?")
		editorTextMutedWrapped("Every output is rebuilt from scratch. A large generator can take minutes.")
		if imgui.Button("Force Run##genforcego") {
			genBeginRun(true, nil)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##genforcecancel") {
			imgui.CloseCurrentPopup()
		}
	})
}

func genTemplateLabel(t *procgen.Template) string {
	if t == nil {
		return "(none)"
	}
	if t.Label != "" {
		return t.Label
	}
	return t.Name
}

// genLevelNames is every level by name, sorted.
func genLevelNames() []string {
	names := levelio.ScanLevelFiles(levelio.LevelsDir())
	for i := range names {
		names[i] = strings.TrimSuffix(names[i], levelio.LevelExt)
	}
	sort.Strings(names)
	return names
}

func genOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
