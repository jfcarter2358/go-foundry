package editor

// editor_yaml_ui.go — the level YAML editor's ImGui.
//
// From the top: a toolbar, the find bar when it is open, the buffer with its
// line numbers, a status line (caret position and where in the level it is),
// and the problems the loader would have with the file.
//
// The buffer is built the way the script editor's is (editor_script_ui.go
// explains it at length): an InputTextMultiline sized to its whole content so
// it never scrolls itself, its own glyphs drawn transparent, and coloured ones
// painted over them in its own child window. What differs is scale. level1 is
// 130,000 lines, so nothing here may touch every line per frame: the colouring
// is worked out only for the lines on screen (editor_yaml_hl.go), the caret and
// the gutter go through a line index instead of splitting the text, and the
// widest-line measurement is made once per edit from that index.
//
// Every change this window makes to the text goes through yamlQueueOp. After
// the widget has run each frame — and so it is known whether it holds the
// keyboard — a queued operation either goes into the widget's callback, when
// the widget owns the text, or straight into the document, when it does not.
// Deciding that in one place is what keeps a button's edit and a keystroke's
// edit from ever fighting over which copy of the text is current.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/game"
	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// yamlEd is the window's state. One document on screen at a time: a level
// file, or the project's game.yaml, toggled from the toolbar. Each keeps its
// own text, history and caret while the other is shown.
var yamlEd = struct {
	doc      *yamlDoc
	levelDoc *yamlDoc
	gameDoc  *yamlDoc
	docSeq   int
	checker  yamlChecker

	status    string
	statusAt  uint32
	statusErr bool

	// The buffer, as of the last frame — see the script editor's equivalents
	// (scriptBufActive and its neighbours) for what each is for.
	active           bool
	textOrigin       imgui.Vec2
	textValid        bool
	rectMin, rectMax imgui.Vec2
	cursor           int
	lastCursor       int
	prevCursor       int
	caretAt          uint32
	caretLine        int
	sel              scriptSel
	anchor           int
	extend           bool
	wantFocus        bool
	enterPressed     bool

	// queued waits for this frame's widget; cbOps for its callback.
	queued   []yamlOp
	cbOps    []yamlOp
	cbOpsAt  uint32
	cbRecord yamlRecordMode

	// scrollTo is a byte offset to bring into view, -1 for none.
	scrollTo int

	width    float32
	widthGen int

	lastPoll uint32

	// applyRaw is a save waiting to reach the level editor — one made during
	// play, or while a load was already running.
	applyRaw  *world.RawLevel
	applyPath string

	showOpen, showGoto bool
	gotoLine           string
	levelFiles         []string

	hover struct {
		pos   imgui.Vec2
		since uint32
	}

	crumb    string
	crumbAt  int
	crumbGen int

	find struct {
		open, replace bool
		opts          yamlFindOpts
		repl          string
		matches       []yamlFindMatch
		capped        bool
		err           error
		cur           int
		gen           int
		key           yamlFindOpts
		valid         bool
		focusFind     bool
		focusRepl     bool
		fieldActive   bool
		// pendingFrom is where to look for the next match once a replace has
		// changed the text, -1 for none.
		pendingFrom int
	}
}{scrollTo: -1}

func init() {
	yamlEd.find.cur = -1
	yamlEd.find.pendingFrom = -1
}

const (
	yamlBufScrollID = "##yamlscroll"
	yamlBufID       = "##yamlbuffer"
	yamlGutterID    = "##yamlgutter"
)

// yamlProblemsH is the height the problems pane takes when there are any.
const yamlProblemsH = 150

// yamlStatus sets the transient message on the toolbar.
func yamlStatus(format string, args ...any) {
	yamlEd.status = fmt.Sprintf(format, args...)
	yamlEd.statusAt = sdl.GetTicks()
	yamlEd.statusErr = false
}

// yamlStatusErr is yamlStatus in red, for a save that did not happen.
func yamlStatusErr(format string, args ...any) {
	yamlStatus(format, args...)
	yamlEd.statusErr = true
}

// yamlQueueOp asks for an operation on the buffer. See the file's header.
func yamlQueueOp(op yamlOp) { yamlEd.queued = append(yamlEd.queued, op) }

// ---------- documents ----------

// yamlSwitchTo makes d the document on screen, forgetting everything the
// window knew about the previous one's text.
func yamlSwitchTo(d *yamlDoc) {
	if prev := yamlEd.doc; prev != nil {
		prev.caret = yamlEd.cursor
	}
	yamlEd.doc = d
	if d.Kind == yamlKindGame {
		yamlEd.gameDoc = d
	} else {
		yamlEd.levelDoc = d
	}
	yamlEd.docSeq++
	yamlEd.cursor, yamlEd.lastCursor, yamlEd.prevCursor = 0, 0, 0
	yamlEd.sel = scriptSel{}
	yamlEd.queued, yamlEd.cbOps = nil, nil
	yamlEd.scrollTo = 0
	yamlEd.find.valid = false
	yamlEd.find.cur = -1
	yamlEd.widthGen = -1
	yamlEd.crumbGen = -1
	yamlComplClose()
	// A text the list has never seen would read as typing on the first frame.
	yamlCompl.src, yamlCompl.cursor = d.Text, 0
	if c := min(d.caret, len(d.Text)); c > 0 {
		yamlQueueOp(yamlSelectOp(c, c))
		yamlEd.scrollTo = c
	}
	// A check that finished while another document was showing was dropped;
	// ask for a fresh one.
	if !d.Checked() && d.dirtyAt == 0 {
		d.dirtyAt = 1
	}
}

// yamlShowLevel shows the level document, opening the level editor's level
// when there is none yet.
func yamlShowLevel() {
	if yamlEd.levelDoc == nil {
		yamlOpenLevelEditorLevel()
		return
	}
	if yamlEd.doc != yamlEd.levelDoc {
		yamlSwitchTo(yamlEd.levelDoc)
	}
}

// yamlGamePath is the open project's game.yaml.
func yamlGamePath() string { return game.ProjectPath(fileutil.ProjectPath) }

// yamlShowGame shows the project's game.yaml. A project without one starts
// from the manifest a new project is given, and saving creates the file.
func yamlShowGame() {
	if yamlEd.gameDoc != nil {
		if yamlEd.doc != yamlEd.gameDoc {
			yamlSwitchTo(yamlEd.gameDoc)
		}
		return
	}
	path := yamlGamePath()
	d, err := openYAMLDocKind(path, yamlKindGame)
	if err != nil {
		d = &yamlDoc{Path: path, Kind: yamlKindGame, Text: game.DefaultManifest}
		yamlSwitchTo(d)
		yamlStatus("%s is not on disk yet — saving creates it", game.FileName)
		return
	}
	yamlSwitchTo(d)
}

// yamlOpenLevelEditorLevel opens the level the level editor has open — its
// file when it has one, and otherwise the level as the editor holds it.
func yamlOpenLevelEditorLevel() {
	path := Editor.SavePath
	if path != "" {
		if d, err := openYAMLDoc(path); err == nil {
			yamlSwitchTo(d)
			return
		}
	}
	d := &yamlDoc{Path: path}
	if text, err := yamlLevelEditorText(); err == nil {
		d.Text = text
	}
	yamlSwitchTo(d)
	if path != "" {
		yamlStatus("%s is not on disk yet — this is the level as the editor holds it; saving creates the file", filepath.Base(path))
	}
}

// yamlOpenFile opens a level file in place of the current document, refusing
// while the current one has unsaved edits — there is one document, and
// replacing it would drop them.
func yamlOpenFile(path string) {
	cur := yamlEd.levelDoc
	if cur != nil && cur.Dirty() && !sameLevelPath(cur.Path, path) {
		yamlStatusErr("%s has unsaved changes — save or revert it first", cur.Name())
		return
	}
	if cur != nil && sameLevelPath(cur.Path, path) {
		yamlShowLevel()
		return
	}
	d, err := openYAMLDoc(path)
	if err != nil {
		yamlStatusErr("%v", err)
		return
	}
	yamlSwitchTo(d)
	yamlStatus("Opened %s", d.Name())
}

// yamlPullFromLevelEditor replaces the buffer with the level editor's level as
// it is in memory, unsaved edits and all.
func yamlPullFromLevelEditor() {
	d := yamlEd.doc
	if !sameLevelPath(d.Path, Editor.SavePath) {
		if d.Dirty() {
			yamlStatusErr("%s has unsaved changes — save or revert it first", d.Name())
			return
		}
		yamlOpenLevelEditorLevel()
		d = yamlEd.doc
	}
	text, err := yamlLevelEditorText()
	if err != nil {
		yamlStatusErr("%v", err)
		return
	}
	if text == d.Text {
		yamlStatus("The buffer already matches the level editor")
		return
	}
	yamlQueueOp(yamlTextOp("pull", text, min(yamlEd.cursor, len(text))))
	yamlStatus("Replaced the buffer with the level editor's level — Ctrl+Z to take it back")
}

// yamlEditorSave writes the file and, when it is the level the level editor
// has open, rebuilds that level from it.
func yamlEditorSave() {
	d := yamlEd.doc
	if d == nil {
		return
	}
	r, err := d.Save()
	if err != nil {
		yamlStatusErr("Not saved: %v", err)
		return
	}
	msg := "Saved " + d.Name()
	if n := yamlCountWarnings(r.Diags); n > 0 {
		msg += fmt.Sprintf(" with %s", plural(n, "warning"))
	}
	if d.Kind == yamlKindGame {
		msg += " — release builds read it; the editor does not"
	} else if sameLevelPath(d.Path, Editor.SavePath) && Editor.level != nil {
		raw := r.Raw
		// The check's result is now the level editor's to build, and may be
		// changed as it is; this document must not hand it out a second time.
		r.Raw, r.rawTaken = world.RawLevel{}, true
		switch {
		case inPlaySession():
			yamlEd.applyRaw, yamlEd.applyPath = &raw, d.Path
			msg += " — it reaches the level editor when play stops"
		case editorBeginReloadLevel(d.Path, raw):
			msg += " — rebuilding the level editor's level (Ctrl+Z there takes it back)"
		default:
			yamlEd.applyRaw, yamlEd.applyPath = &raw, d.Path
			msg += " — it reaches the level editor when the current load finishes"
		}
	}
	yamlStatus("%s", msg)
}

// yamlApplyPending hands a waiting save to the level editor once it can take
// one.
func yamlApplyPending() {
	if yamlEd.applyRaw == nil || inPlaySession() || EditorLevelLoading() {
		return
	}
	raw, path := yamlEd.applyRaw, yamlEd.applyPath
	yamlEd.applyRaw = nil
	// The level editor may have moved to another level meanwhile, and then
	// this save is not about it.
	if !sameLevelPath(path, Editor.SavePath) {
		return
	}
	editorBeginReloadLevel(path, *raw)
}

// yamlCountWarnings counts the diagnostics that do not stop a save.
func yamlCountWarnings(ds []yamlDiag) int {
	n := 0
	for _, d := range ds {
		if d.Warn {
			n++
		}
	}
	return n
}

// yamlPollDisk notices the file changing underneath — the level editor's own
// Save is the usual cause. A clean buffer follows it; a dirty one is told, and
// the author decides.
func yamlPollDisk(d *yamlDoc, now uint32) {
	if now-yamlEd.lastPoll < 1000 || d.Path == "" {
		return
	}
	yamlEd.lastPoll = now
	mt := d.diskModTime()
	if mt.IsZero() || mt.Equal(d.modTime) {
		return
	}
	d.modTime = mt
	if d.Dirty() {
		d.diskChanged = true
		return
	}
	data, err := os.ReadFile(d.Path)
	if err != nil {
		return
	}
	text := imguiSafeText(string(data))
	if text == d.Text {
		return
	}
	d.saved = text
	yamlQueueOp(yamlTextOp("reload", text, min(yamlEd.cursor, len(text))))
	yamlStatus("%s changed on disk — reloaded", d.Name())
}

// ---------- the frame ----------

// drawYAMLEditorWindow renders one frame, with this window's contexts current.
func drawYAMLEditorWindow() {
	w := yamlEditorWindow
	if w == nil {
		return
	}
	if yamlEd.doc == nil {
		yamlOpenLevelEditorLevel()
	}
	d := yamlEd.doc
	now := sdl.GetTicks()
	d.Update(&yamlEd.checker, now)
	yamlPollDisk(d, now)
	yamlApplyPending()

	imgui.SetNextWindowPos(imgui.Vec2{})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	if !imgui.BeginV("##yamleditor_root", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoCollapse|imgui.WindowFlagsNoScrollbar|
			imgui.WindowFlagsMenuBar) {
		imgui.End()
		return
	}

	drawYAMLToolbar(d)
	drawYAMLDiskBanner(d)
	if yamlEd.find.open {
		drawYAMLFindBar(d)
	} else {
		yamlEd.find.fieldActive = false
	}
	imgui.Separator()
	drawYAMLBuffer(d)
	drawYAMLStatusLine(d)
	drawYAMLProblems(d)

	drawYAMLOpenDialog()
	drawYAMLGotoDialog()
	imgui.End()

	// Outside every window, for the reason drawScriptComplPopup is.
	drawYAMLComplPopup()
}

// drawYAMLToolbar draws the menus (editor_menubar.go lays out the part every
// editor shares), then the line under them: whether the file is the level
// editor's, and the transient status.
func drawYAMLToolbar(d *yamlDoc) {
	dirty := d.Dirty()
	if imgui.BeginMenuBar() {
		yamlEditorMenus(d, dirty)
		editorMenuBarShared(editorKindYAML)
		editorMenuBarDoc(d.Name(), dirty)
		if imgui.IsItemHovered() && d.Path != "" {
			imgui.SetTooltip(d.Path)
		}
		imgui.EndMenuBar()
	}

	if editorActiveToggleButton("Level", d.Kind == yamlKindLevel) {
		yamlShowLevel()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Edit a level file.")
	}
	imgui.SameLine()
	if editorActiveToggleButton(game.FileName, d.Kind == yamlKindGame) {
		yamlShowGame()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Edit the project's game manifest: title, version, start level, window.\n" +
			"Each file keeps its own unsaved edits and undo while the other is shown.")
	}
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	d = yamlEd.doc

	switch {
	case d.Kind == yamlKindGame:
		editorTextMuted("Read by release builds at boot")
	case sameLevelPath(d.Path, Editor.SavePath):
		editorTextMuted("Open in the level editor")
	case d.Path != "":
		imguibackend.EditorPushDisabled(dirty || EditorLevelLoading())
		if imgui.Button("Open in Level Editor") {
			EditorBeginLoadLevel(d.Path)
		}
		imguibackend.EditorPopDisabled()
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Open this file in the level editor, in place of its level.\nSave first: the level editor reads the file on disk.")
		}
	default:
		editorTextMuted("Not saved to a file yet")
	}

	if yamlEd.status != "" {
		if sdl.GetTicks()-yamlEd.statusAt > 6000 {
			yamlEd.status = ""
		} else {
			imgui.SameLine()
			editorToolbarGroupSep()
			imgui.SameLine()
			if yamlEd.statusErr {
				editorTextColored(imgui.Vec4{X: 1, Y: 0.5, Z: 0.5, W: 1}, yamlEd.status)
			} else {
				editorTextMuted(yamlEd.status)
			}
		}
	}
}

// yamlEditorMenus draws the YAML editor's own menus.
func yamlEditorMenus(d *yamlDoc, dirty bool) {
	if imgui.BeginMenu("File") {
		if imgui.MenuItemV("Open...", "Ctrl+O", false, true) {
			yamlEd.levelFiles = levelio.ScanLevelFiles(Editor.LevelsDir)
			yamlEd.showOpen = true
		}
		editorMenuTooltip("Open another level file.")
		imgui.Separator()
		if imgui.MenuItemV("Save", "Ctrl+S", false, dirty) {
			yamlEditorSave()
		}
		editorMenuTooltip("Check and write the file.\n" +
			"A file with errors the loader would refuse is not written.\n" +
			"When it is the level the level editor has open, that level is rebuilt from it.")
		editorSaveAllMenuItem()
		if imgui.MenuItemV("Revert", "", false, dirty && d.Path != "") {
			if err := yamlRevert(d); err != nil {
				yamlStatusErr("%v", err)
			}
		}
		editorMenuTooltip("Put the buffer back to the file on disk. Ctrl+Z takes the revert back.")
		imgui.Separator()
		if imgui.MenuItemV("Pull From Level Editor", "", false, Editor.level != nil && d.Kind == yamlKindLevel) {
			yamlPullFromLevelEditor()
		}
		editorMenuTooltip("Replace the buffer with the level editor's level as it is right now,\n" +
			"including edits it has not saved. Written the way its own Save writes,\n" +
			"so comments in the file are not kept. Ctrl+Z takes it back.")
		editorCloseWindowItem(CloseLevelYAMLEditor)
		imgui.EndMenu()
	}

	if imgui.BeginMenu("Edit") {
		ctxDrawMenuItems(yamlBufferMenuItems())
		imgui.EndMenu()
	}
}

// yamlRevert puts the buffer back to the file.
func yamlRevert(d *yamlDoc) error {
	data, err := os.ReadFile(d.Path)
	if err != nil {
		return err
	}
	text := imguiSafeText(string(data))
	d.saved = text
	d.diskChanged = false
	d.modTime = d.diskModTime()
	yamlQueueOp(yamlTextOp("revert", text, min(yamlEd.cursor, len(text))))
	yamlStatus("Reverted to the file on disk")
	return nil
}

// drawYAMLDiskBanner is the question a dirty buffer is asked when its file
// changes underneath it.
func drawYAMLDiskBanner(d *yamlDoc) {
	if !d.diskChanged {
		return
	}
	editorTextColored(scriptIssueColor, d.Name()+" changed on disk, and this buffer has unsaved changes.")
	imgui.SameLine()
	if imgui.Button("Load the file") {
		if err := yamlRevert(d); err != nil {
			yamlStatusErr("%v", err)
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Replace the buffer with what is on disk. Ctrl+Z brings your edits back.")
	}
	imgui.SameLine()
	if imgui.Button("Keep mine") {
		d.diskChanged = false
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Keep editing; saving writes over what is on disk.")
	}
}

// ---------- find and replace ----------

// yamlFindOpen shows the find bar, seeded with the selection when it is a
// short piece of one line — the thing most often searched for is the word the
// caret is already on.
func yamlFindOpen(replace bool) {
	f := &yamlEd.find
	f.open, f.replace = true, replace
	if d := yamlEd.doc; d != nil {
		s := yamlEd.sel.ordered(len(d.Text))
		if !s.empty() && s.End-s.Start < 200 && !strings.Contains(d.Text[s.Start:s.End], "\n") {
			f.opts.Query = d.Text[s.Start:s.End]
			if f.opts.Regex {
				f.opts.Query = regexpQuote(f.opts.Query)
			}
		}
	}
	f.valid = false
	f.focusFind = true
	f.cur = -1
}

// yamlFindClose hides the find bar. With selectMatch, the current match is
// selected in the buffer and the buffer takes the keyboard — what Escape in
// the find box means: "that one, I want to edit it".
func yamlFindClose(selectMatch bool) {
	f := &yamlEd.find
	f.open, f.fieldActive = false, false
	if selectMatch && f.cur >= 0 && f.cur < len(f.matches) {
		m := f.matches[f.cur]
		yamlQueueOp(yamlSelectOp(m.Start, m.End))
		return
	}
	yamlEd.wantFocus = true
}

// yamlFindRefresh recomputes the matches when the text or the query changed.
func yamlFindRefresh(d *yamlDoc) {
	f := &yamlEd.find
	if f.valid && f.gen == d.gen && f.key == f.opts {
		return
	}
	f.matches, f.capped, f.err = yamlFindAll(d.Text, f.opts)
	f.gen, f.key, f.valid = d.gen, f.opts, true
	switch {
	case f.pendingFrom >= 0:
		f.cur = yamlFindNext(f.matches, f.pendingFrom, false)
		f.pendingFrom = -1
		if f.cur >= 0 {
			yamlEd.scrollTo = f.matches[f.cur].Start
		}
	case f.cur >= len(f.matches):
		f.cur = -1
	}
}

// yamlFindStep moves to the next match, or the previous one.
func yamlFindStep(back bool) {
	d, f := yamlEd.doc, &yamlEd.find
	if d == nil || f.opts.Query == "" {
		if !f.open {
			yamlFindOpen(false)
		}
		return
	}
	yamlFindRefresh(d)
	at := yamlEd.cursor
	if f.cur >= 0 && f.cur < len(f.matches) {
		if back {
			at = f.matches[f.cur].Start
		} else {
			at = f.matches[f.cur].End
		}
	}
	f.cur = yamlFindNext(f.matches, at, back)
	if f.cur >= 0 {
		yamlEd.scrollTo = f.matches[f.cur].Start
	}
}

// yamlReplaceCurrent replaces the current match and moves on to the next —
// or, with no current match, only finds one, so the first press shows what
// the second will replace.
func yamlReplaceCurrent() {
	d, f := yamlEd.doc, &yamlEd.find
	yamlFindRefresh(d)
	if f.cur < 0 || f.cur >= len(f.matches) {
		yamlFindStep(false)
		return
	}
	m, gen, opts, repl := f.matches[f.cur], d.gen, f.opts, f.repl
	yamlQueueOp(yamlOp{Name: "replace", Record: yamlRecordSealed,
		Fn: func(text string, sel scriptSel) (string, scriptSel, bool) {
			// Only against the text the match was found in.
			if yamlEd.doc.gen != gen || m.End > len(text) {
				return text, sel, false
			}
			r := yamlReplacement(text, m, opts, repl)
			at := m.Start + len(r)
			yamlEd.find.pendingFrom = at
			return text[:m.Start] + r + text[m.End:], scriptSel{at, at}, true
		}})
}

// yamlReplaceEverything replaces every match, however many — the match list
// the overlay draws stops at yamlFindMaxMatches, and this does not.
func yamlReplaceEverything() {
	opts, repl := yamlEd.find.opts, yamlEd.find.repl
	if opts.Query == "" {
		return
	}
	yamlQueueOp(yamlOp{Name: "replace all", Record: yamlRecordSealed,
		Fn: func(text string, sel scriptSel) (string, scriptSel, bool) {
			ms, _, err := yamlFindAllLimit(text, opts, -1)
			if err != nil || len(ms) == 0 {
				yamlStatus("Nothing to replace")
				return text, sel, false
			}
			out, n := yamlReplaceAll(text, ms, opts, repl)
			yamlStatus("Replaced %s — Ctrl+Z takes them all back", plural(n, "occurrence"))
			c := min(sel.End, len(out))
			return out, scriptSel{c, c}, true
		}})
}

// drawYAMLFindBar draws the find row, and the replace row under it.
func drawYAMLFindBar(d *yamlDoc) {
	f := &yamlEd.find
	yamlFindRefresh(d)
	shift := sdl.GetModState()&sdl.KMOD_SHIFT != 0

	imgui.SetNextItemWidth(300)
	if f.focusFind {
		imgui.SetKeyboardFocusHere()
		f.focusFind = false
	}
	q := f.opts.Query
	enter := imgui.InputTextWithHintV("##yamlfind", "Find", &q, imgui.InputTextFlagsEnterReturnsTrue, nil)
	active := imgui.IsItemActive()
	if q != f.opts.Query {
		f.opts.Query = q
		yamlFindRefresh(d)
		// Searching as the query is typed, from the caret, the way every
		// editor's find box does.
		f.cur = yamlFindNext(f.matches, yamlEd.cursor, false)
		if f.cur >= 0 {
			yamlEd.scrollTo = f.matches[f.cur].Start
		}
	}
	if enter {
		yamlFindStep(shift)
		// Enter takes the keyboard off a one-line field; give it back, so the
		// next Enter finds the next match.
		f.focusFind = true
	}
	imgui.SameLine()
	imgui.Checkbox("Case", &f.opts.Case)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Match case")
	}
	imgui.SameLine()
	imgui.Checkbox("Word", &f.opts.Word)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Whole words only")
	}
	imgui.SameLine()
	imgui.Checkbox("Regex", &f.opts.Regex)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Regular expression (Go syntax). The replacement can use $1, ${name}.")
	}
	imgui.SameLine()
	if imgui.Button("Prev##yamlfindprev") {
		yamlFindStep(true)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Previous match  Shift+Enter / Shift+F3")
	}
	imgui.SameLine()
	if imgui.Button("Next##yamlfindnext") {
		yamlFindStep(false)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Next match  Enter / F3")
	}
	imgui.SameLine()
	switch {
	case f.err != nil:
		editorTextColored(imgui.Vec4{X: 1, Y: 0.5, Z: 0.5, W: 1}, "bad pattern")
		if imgui.IsItemHovered() {
			imgui.SetTooltip(f.err.Error())
		}
	case f.opts.Query == "":
	case len(f.matches) == 0:
		editorTextColored(imgui.Vec4{X: 1, Y: 0.6, Z: 0.5, W: 1}, "No results")
	default:
		total := strconv.Itoa(len(f.matches))
		if f.capped {
			total += "+"
		}
		if f.cur >= 0 {
			editorTextMuted(fmt.Sprintf("%d of %s", f.cur+1, total))
		} else {
			editorTextMuted(total + " found")
		}
	}
	imgui.SameLine()
	if imgui.Button("Close##yamlfindclose") {
		yamlFindClose(false)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Close  Esc (in the find box: close and select the match)")
	}

	replActive := false
	if f.replace {
		imgui.SetNextItemWidth(300)
		if f.focusRepl {
			imgui.SetKeyboardFocusHere()
			f.focusRepl = false
		}
		if imgui.InputTextWithHintV("##yamlrepl", "Replace", &f.repl, imgui.InputTextFlagsEnterReturnsTrue, nil) {
			yamlReplaceCurrent()
			f.focusRepl = true
		}
		replActive = imgui.IsItemActive()
		imgui.SameLine()
		imguibackend.EditorPushDisabled(len(f.matches) == 0)
		if imgui.Button("Replace##yamlreplone") {
			yamlReplaceCurrent()
		}
		imgui.SameLine()
		if imgui.Button("Replace All") {
			yamlReplaceEverything()
		}
		imguibackend.EditorPopDisabled()
	}
	f.fieldActive = active || replActive
}

// regexpQuote escapes a selection being dropped into a regex find box.
func regexpQuote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(`\.+*?()|[]{}^$`, s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ---------- the buffer ----------

// drawYAMLBuffer draws the gutter and the text beside it.
func drawYAMLBuffer(d *yamlDoc) {
	reserve := imgui.TextLineHeightWithSpacing()*2.6 + imgui.CurrentStyle().ItemSpacing().Y*2
	if len(d.Diags()) > 0 {
		reserve += yamlProblemsH
	}
	h := max(imgui.ContentRegionAvail().Y-reserve, 120)

	imgui.PushID(fmt.Sprintf("yamldoc%d", yamlEd.docSeq))
	defer imgui.PopID()

	ix := d.Index()
	gutterW := scriptGutterWidth(ix.Count())
	top := imgui.CursorPos()

	imgui.SetCursorPos(imgui.Vec2{X: top.X + gutterW, Y: top.Y})
	imgui.BeginChildV(yamlBufScrollID, imgui.Vec2{X: -1, Y: h}, true,
		imgui.WindowFlagsHorizontalScrollbar)
	drawYAMLText(d)
	imgui.EndChild()

	bufMin, bufMax := imgui.ItemRectMin(), imgui.ItemRectMax()
	yamlEd.rectMin, yamlEd.rectMax = bufMin, bufMax
	below := imgui.CursorPos()
	imgui.SetCursorPos(top)
	drawYAMLGutter(d, gutterW, h)
	imgui.SetCursorPos(below)

	drawYAMLBufferMenu(bufMin, bufMax)
	if len(yamlEd.cbOps) > 0 && sdl.GetTicks()-yamlEd.cbOpsAt > scriptEditPendingMS {
		yamlEd.cbOps = nil
	}
}

// yamlLineWidth is the width of the widest line, in pixels, for the current
// text. Measured on the few longest lines by byte count — every line would be
// a hundred thousand text measurements per keystroke — with a margin for a
// narrow-glyphed long line losing to a wide-glyphed shorter one.
func yamlLineWidth(d *yamlDoc) float32 {
	if yamlEd.widthGen == d.gen {
		return yamlEd.width
	}
	ix := d.Index()
	const keep = 24
	type ln struct{ i, n int }
	var longest []ln
	for i := 0; i < ix.Count(); i++ {
		n := ix.End(i) - ix.Start(i)
		if len(longest) == keep && n <= longest[keep-1].n {
			continue
		}
		longest = append(longest, ln{i, n})
		sort.Slice(longest, func(a, b int) bool { return longest[a].n > longest[b].n })
		if len(longest) > keep {
			longest = longest[:keep]
		}
	}
	var w float32
	for _, l := range longest {
		w = max(w, imgui.CalcTextSize(ix.Line(l.i), false, 0).X)
	}
	yamlEd.width, yamlEd.widthGen = w*1.1+16, d.gen
	return yamlEd.width
}

// drawYAMLText draws the buffer and everything painted over it.
func drawYAMLText(d *yamlDoc) {
	now := sdl.GetTicks()
	ix := d.Index()
	lineH := imgui.TextLineHeight()
	style := imgui.CurrentStyle()
	framePad, winPad := style.FramePadding(), style.WindowPadding()
	view := imgui.ContentRegionAvail()
	textCol := imgui.PackedColorFromVec4(style.Color(imgui.StyleColorText))

	size := imgui.Vec2{
		X: max(yamlLineWidth(d)+framePad.X*2+winPad.X*2+8, view.X),
		Y: max(float32(ix.Count())*lineH+framePad.Y*2+winPad.Y*2+lineH, view.Y),
	}
	origin := imgui.CursorPos()

	if yamlEd.wantFocus {
		imgui.SetKeyboardFocusHere()
		yamlEd.wantFocus = false
	}
	yamlEd.extend = yamlEd.active && imgui.IsMouseClicked(0) && !imgui.IsMouseDoubleClicked(0) &&
		(imgui.IsKeyDown(int(sdl.SCANCODE_LSHIFT)) || imgui.IsKeyDown(int(sdl.SCANCODE_RSHIFT))) &&
		imgui.IsWindowHoveredV(imgui.HoveredFlagsChildWindows)

	imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{})
	text := d.Text
	changed := imgui.InputTextMultilineV(yamlBufID, &text, size,
		imgui.InputTextFlagsCallbackAlways|imgui.InputTextFlagsNoHorizontalScroll,
		yamlBufferCallback)
	imgui.PopStyleColor()
	active := imgui.IsItemActive()
	yamlEd.active = active

	if changed || text != d.Text {
		if mode := yamlEd.cbRecord; mode != yamlRecordNone {
			d.undo.Record(d.Text, text, now, mode == yamlRecordSealed)
		}
		d.setText(text, now)
	} else if yamlEd.cursor != yamlEd.prevCursor {
		// The caret went somewhere without typing: typing there is a new
		// undo step, not more of the last one.
		d.undo.Seal()
	}
	yamlEd.cbRecord = yamlRecordTyping
	yamlEd.prevCursor = yamlEd.cursor

	yamlFlushQueued(d, active, now)
	ix = d.Index()

	drawYAMLOverlay(d, ix, size, lineH, textCol, active)
	if active {
		yamlFollowCaret(ix, origin.Plus(framePad).Plus(winPad), lineH, view)
		yamlComplUpdate(d)
	} else {
		yamlComplClose()
	}
	if yamlEd.scrollTo >= 0 {
		yamlScrollInto(ix, yamlEd.scrollTo, origin.Plus(framePad).Plus(winPad), lineH, view)
		yamlEd.scrollTo = -1
	}
}

// yamlFlushQueued settles every operation asked for since the last frame, now
// that it is known whether the widget holds the keyboard. See the file's
// header.
func yamlFlushQueued(d *yamlDoc, active bool, now uint32) {
	ops := yamlEd.queued
	yamlEd.queued = nil
	for _, op := range ops {
		if active || op.NeedsSel {
			yamlEd.cbOps = append(yamlEd.cbOps, op)
			yamlEd.cbOpsAt = now
			if !active {
				yamlEd.wantFocus = true
			}
			continue
		}
		out, nsel, ok := op.Fn(d.Text, yamlEd.sel)
		if !ok {
			continue
		}
		if op.Record != yamlRecordNone {
			d.undo.Record(d.Text, out, now, op.Record == yamlRecordSealed)
		}
		d.setText(out, now)
		yamlEd.cursor, yamlEd.sel, yamlEd.prevCursor = nsel.End, nsel, nsel.End
		yamlEd.scrollTo = nsel.End
		// Not typing: the completion list must not read it as such when the
		// buffer next takes the keyboard.
		yamlCompl.src, yamlCompl.cursor = out, nsel.End
	}
}

// yamlBufferCallback runs every frame the widget holds the keyboard: the only
// time its text, caret and selection can be read or written.
func yamlBufferCallback(data imgui.InputTextCallbackData) int32 {
	sel := scriptSel{Start: data.SelectionStart(), End: data.SelectionEnd()}
	if yamlEd.extend {
		yamlEd.extend = false
		sel = yamlExtendSelection(data)
	}
	yamlEd.anchor = sel.Start
	if sel.Start == sel.End {
		yamlEd.anchor = data.CursorPos()
	}
	yamlEd.sel = sel.ordered(len(data.Buffer()))
	yamlEd.cursor = data.CursorPos()

	if yamlEd.enterPressed {
		yamlEd.enterPressed = false
		yamlAutoIndentAfterEnter(data)
	}
	if yamlCompl.accept >= 0 {
		yamlComplApply(data)
	}
	if len(yamlEd.cbOps) > 0 {
		ops := yamlEd.cbOps
		yamlEd.cbOps = nil
		for _, op := range ops {
			yamlApplyToWidget(data, op)
		}
	}
	return 0
}

// yamlExtendSelection re-anchors a shift-click — scriptExtendSelection, for
// this buffer's anchor.
func yamlExtendSelection(data imgui.InputTextCallbackData) scriptSel {
	n := len(data.Buffer())
	anchor := min(max(yamlEd.anchor, 0), n)
	cursor := data.CursorPos()
	if anchor == cursor {
		return scriptSel{Start: cursor, End: cursor}
	}
	data.SetSelectionStart(anchor)
	data.SetSelectionEnd(cursor)
	return scriptSel{Start: anchor, End: cursor}
}

// yamlApplyToWidget runs one operation against the widget's own buffer.
//
// Only the part of the text that changed is spliced, so an undo of one
// character does not rewrite two megabytes through the widget.
func yamlApplyToWidget(data imgui.InputTextCallbackData, op yamlOp) {
	text := string(data.Buffer())
	sel := scriptSel{Start: data.SelectionStart(), End: data.SelectionEnd()}
	out, nsel, ok := op.Fn(text, sel)
	if !ok {
		return
	}
	if out != text {
		at, old, nw := yamlDiff(text, out)
		if len(old) > 0 {
			data.DeleteBytes(at, len(old))
		}
		if nw != "" {
			data.InsertBytes(at, []byte(nw))
		}
		yamlEd.cbRecord = max(yamlEd.cbRecord, op.Record)
	}
	nsel = nsel.ordered(len(out))
	data.SetCursorPos(nsel.End)
	data.SetSelectionStart(nsel.Start)
	data.SetSelectionEnd(nsel.End)
	yamlEd.cursor, yamlEd.sel = nsel.End, nsel
	yamlCompl.src, yamlCompl.cursor = out, nsel.End
}

// yamlAutoIndentAfterEnter indents the line Enter just opened. ImGui has
// inserted the newline by now; this inserts the indent after it.
func yamlAutoIndentAfterEnter(data imgui.InputTextCallbackData) {
	buf := data.Buffer()
	c := data.CursorPos()
	if c <= 0 || c > len(buf) || buf[c-1] != '\n' {
		return
	}
	ls := strings.LastIndexByte(string(buf[:c-1]), '\n') + 1
	ind := yamlAutoIndent(string(buf[ls : c-1]))
	if ind == "" {
		return
	}
	data.InsertBytes(c, []byte(ind))
	c += len(ind)
	data.SetCursorPos(c)
	data.SetSelectionStart(c)
	data.SetSelectionEnd(c)
	yamlEd.cursor = c
	yamlEd.sel = scriptSel{c, c}
}

// yamlCaretXY returns where byte off is drawn, relative to the text origin.
func yamlCaretXY(ix *yamlLineIndex, off int, lineH float32) (line int, x, y float32) {
	off = min(max(off, 0), len(ix.src))
	line = ix.LineOf(off)
	x = imgui.CalcTextSize(ix.src[ix.Start(line):off], false, 0).X
	return line, x, float32(line) * lineH
}

// yamlFollowCaret scrolls the buffer to keep the caret in view, when it moved.
func yamlFollowCaret(ix *yamlLineIndex, origin imgui.Vec2, lineH float32, view imgui.Vec2) {
	if yamlEd.cursor == yamlEd.lastCursor {
		return
	}
	yamlEd.lastCursor = yamlEd.cursor
	yamlEd.caretAt = sdl.GetTicks()
	_, x, y := yamlCaretXY(ix, yamlEd.cursor, lineH)
	top, left := origin.Y+y, origin.X+x
	switch {
	case top < imgui.ScrollY():
		imgui.SetScrollY(top)
	case top+lineH > imgui.ScrollY()+view.Y:
		imgui.SetScrollY(top + lineH - view.Y)
	}
	const margin = 24
	switch {
	case left-margin < imgui.ScrollX():
		imgui.SetScrollX(max(left-margin, 0))
	case left+margin > imgui.ScrollX()+view.X:
		imgui.SetScrollX(left + margin - view.X)
	}
}

// yamlScrollInto brings byte off into view — a third of the way down, so the
// lines above a find match or a problem are on screen too — unless it already
// is.
func yamlScrollInto(ix *yamlLineIndex, off int, origin imgui.Vec2, lineH float32, view imgui.Vec2) {
	_, x, y := yamlCaretXY(ix, off, lineH)
	top, left := origin.Y+y, origin.X+x
	if top < imgui.ScrollY() || top+lineH > imgui.ScrollY()+view.Y {
		imgui.SetScrollY(max(top-view.Y/3, 0))
	}
	if left < imgui.ScrollX() || left > imgui.ScrollX()+view.X-24 {
		imgui.SetScrollX(max(left-view.X/2, 0))
	}
}

// Colours the overlay paints with.
var (
	yamlMatchCol    = imgui.PackedColorFromVec4(imgui.Vec4{X: 0.95, Y: 0.75, Z: 0.25, W: 0.22})
	yamlCurMatchCol = imgui.PackedColorFromVec4(imgui.Vec4{X: 0.98, Y: 0.62, Z: 0.15, W: 0.50})
	yamlCurLineCol  = imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 1, Z: 1, W: 0.035})
	yamlErrLineCol  = imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 0.35, Z: 0.35, W: 0.9})
	yamlWarnLineCol = imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 0.78, Z: 0.35, W: 0.9})
)

// drawYAMLOverlay paints the coloured text, the find matches, the problem
// underlines and the caret over the widget, in the widget's own child window —
// see drawScriptEditorOverlay for why appending to that window is what keeps
// the two aligned.
func drawYAMLOverlay(d *yamlDoc, ix *yamlLineIndex, size imgui.Vec2, lineH float32, textCol imgui.PackedColor, active bool) {
	first := max(int((imgui.ScrollY()-lineH)/lineH), 0)
	last := min(int((imgui.ScrollY()+imgui.WindowSize().Y+lineH)/lineH), ix.Count()-1)

	if !imgui.BeginChildV(yamlBufID, size, true, 0) {
		imgui.EndChild()
		return
	}
	defer imgui.EndChild()
	list := imgui.WindowDrawList()
	origin := imgui.WindowPos().Plus(imgui.CursorStartPos()).Plus(imgui.CurrentStyle().FramePadding())
	yamlEd.textOrigin, yamlEd.textValid = origin, true

	if active {
		if line := ix.LineOf(min(yamlEd.cursor, len(ix.src))); line >= first && line <= last {
			y := origin.Y + float32(line)*lineH
			list.AddRectFilled(imgui.Vec2{X: imgui.WindowPos().X, Y: y},
				imgui.Vec2{X: imgui.WindowPos().X + size.X, Y: y + lineH}, yamlCurLineCol)
		}
	}
	yamlDrawMatches(list, ix, origin, lineH, first, last)

	for i := first; i <= last; i++ {
		raw := ix.Line(i)
		x, y := origin.X, origin.Y+float32(i)*lineH
		for _, c := range yamlHighlightLine(raw, yamlInBlockScalar(ix, i)) {
			w := imgui.CalcTextSize(c.Text, false, 0).X
			if !isBlank(c.Text) {
				list.AddText(imgui.Vec2{X: x, Y: y}, scriptHLColor(c.Class, textCol), c.Text)
			}
			x += w
		}
		// A problem's line is underlined from its first character to its last,
		// so the eye finds it without reading the gutter.
		if dg := d.DiagOn(i + 1); dg != nil && d.Checked() {
			indent, _ := yamlIndentOf(raw)
			x0 := origin.X + imgui.CalcTextSize(raw[:indent], false, 0).X
			x1 := max(x, x0+12)
			col := yamlErrLineCol
			if dg.Warn {
				col = yamlWarnLineCol
			}
			list.AddLine(imgui.Vec2{X: x0, Y: y + lineH - 1}, imgui.Vec2{X: x1, Y: y + lineH - 1}, col)
		}
	}

	if active {
		line, x, y := yamlCaretXY(ix, yamlEd.cursor, lineH)
		yamlEd.caretLine = line
		if (sdl.GetTicks()-yamlEd.caretAt)%1200 <= 800 {
			at := imgui.Vec2{X: origin.X + x, Y: origin.Y + y}
			list.AddLine(at, imgui.Vec2{X: at.X, Y: at.Y + lineH}, textCol)
		}
		yamlComplSetAnchor(ix, origin, lineH)
	}
	yamlDrawHover(ix, origin, lineH)
}

// yamlDrawMatches highlights the find matches on the visible lines, the
// current one stronger.
func yamlDrawMatches(list imgui.DrawList, ix *yamlLineIndex, origin imgui.Vec2, lineH float32, first, last int) {
	f := &yamlEd.find
	if !f.open || !f.valid || len(f.matches) == 0 || f.gen != yamlEd.doc.gen {
		return
	}
	lo, hi := ix.Start(first), ix.End(last)
	i := sort.Search(len(f.matches), func(k int) bool { return f.matches[k].End > lo })
	for ; i < len(f.matches) && f.matches[i].Start <= hi; i++ {
		m := f.matches[i]
		col := yamlMatchCol
		if i == f.cur {
			col = yamlCurMatchCol
		}
		for l := max(ix.LineOf(m.Start), first); l <= min(ix.LineOf(m.End), last); l++ {
			ls, le := ix.Start(l), ix.End(l)
			a, b := max(m.Start, ls), min(m.End, le)
			x0 := imgui.CalcTextSize(ix.src[ls:a], false, 0).X
			x1 := x0 + max(imgui.CalcTextSize(ix.src[a:b], false, 0).X, 3)
			y := origin.Y + float32(l)*lineH
			list.AddRectFilled(imgui.Vec2{X: origin.X + x0, Y: y}, imgui.Vec2{X: origin.X + x1, Y: y + lineH}, col)
		}
	}
}

// yamlOffsetAt returns the byte offset under a screen point, or -1 off the text.
func yamlOffsetAt(ix *yamlLineIndex, origin imgui.Vec2, lineH float32, p imgui.Vec2) int {
	if p.Y < origin.Y || p.X < origin.X {
		return -1
	}
	line := int((p.Y - origin.Y) / lineH)
	if line >= ix.Count() {
		return -1
	}
	raw := ix.Line(line)
	x := p.X - origin.X
	var w float32
	for i := 0; i < len(raw); i++ {
		cw := imgui.CalcTextSize(raw[i:i+1], false, 0).X
		if x < w+cw {
			return ix.Start(line) + i
		}
		w += cw
	}
	return -1
}

// yamlHoverDelayMS is how long the mouse has to rest on a key before its
// documentation comes up — long enough that sweeping across the buffer does
// not flicker tooltips.
const yamlHoverDelayMS = 450

// yamlDrawHover shows a key's type and documentation when the mouse rests on it.
func yamlDrawHover(ix *yamlLineIndex, origin imgui.Vec2, lineH float32) {
	h := &yamlEd.hover
	mp := imgui.MousePos()
	if !imgui.IsWindowHovered() || imgui.IsMouseDown(0) || yamlCompl.Open {
		h.since = 0
		return
	}
	if mp != h.pos || h.since == 0 {
		h.pos, h.since = mp, sdl.GetTicks()
		return
	}
	if sdl.GetTicks()-h.since < yamlHoverDelayMS {
		return
	}
	off := yamlOffsetAt(ix, origin, lineH, mp)
	if off < 0 {
		return
	}
	info, ok := yamlHoverAt(ix, off)
	if !ok {
		return
	}
	imgui.BeginTooltip()
	imgui.PushTextWrapPosV(imgui.FontSize() * 34)
	head := info.Key
	if info.Type != "" {
		head += ": " + info.Type
	}
	editorTextColored(scriptHLPalette[yamlClassKey], head)
	if info.Path != "" {
		editorTextMuted(info.Path)
	}
	if info.Doc != "" {
		imgui.Separator()
		imgui.Text(info.Doc)
	}
	if len(info.Enum) > 0 {
		imgui.Separator()
		editorTextMuted("One of: " + strings.Join(info.Enum, ", "))
	}
	imgui.PopTextWrapPos()
	imgui.EndTooltip()
}

// drawYAMLGutter draws the line numbers — red on a line with an error, amber on
// one with a warning, full contrast on the caret's.
func drawYAMLGutter(d *yamlDoc, w, h float32) {
	imgui.BeginChildV(yamlGutterID, imgui.Vec2{X: w, Y: h}, false,
		imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoScrollWithMouse)
	defer imgui.EndChild()
	if !yamlEd.textValid {
		return
	}
	ix := d.Index()
	lineH := imgui.TextLineHeight()
	list := imgui.WindowDrawList()
	winMin, winMax := imgui.WindowPos(), imgui.WindowPos().Plus(imgui.WindowSize())
	right := winMax.X - imgui.CurrentStyle().ItemSpacing().X

	muted := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.45, Y: 0.49, Z: 0.56, W: 1})
	current := imgui.PackedColorFromVec4(imgui.CurrentStyle().Color(imgui.StyleColorText))
	first := int((winMin.Y - yamlEd.textOrigin.Y) / lineH)
	last := int((winMax.Y-yamlEd.textOrigin.Y)/lineH) + 1
	checked := d.Checked()
	for i := max(first, 0); i <= min(last, ix.Count()-1); i++ {
		y := yamlEd.textOrigin.Y + float32(i)*lineH
		num := strconv.Itoa(i + 1)
		col := muted
		switch dg := d.DiagOn(i + 1); {
		case dg != nil && checked && !dg.Warn:
			col = yamlErrLineCol
		case dg != nil && checked:
			col = yamlWarnLineCol
		case yamlEd.active && i == yamlEd.caretLine:
			col = current
		}
		list.AddText(imgui.Vec2{X: right - imgui.CalcTextSize(num, false, 0).X, Y: y}, col, num)
	}
}

// drawYAMLBufferMenu is the text's right-click menu. See
// drawScriptEditorBufferMenu for why it is opened by hand.
func drawYAMLBufferMenu(bufMin, bufMax imgui.Vec2) {
	const id = "##yamlbufmenu"
	if imgui.IsMouseClicked(1) && imgui.IsWindowHoveredV(imgui.HoveredFlagsChildWindows) &&
		vec2InRect(imgui.MousePos(), bufMin, bufMax) {
		imgui.OpenPopup(id)
	}
	if !imgui.BeginPopup(id) {
		return
	}
	ctxDrawMenuItems(yamlBufferMenuItems())
	imgui.EndPopup()
}

// yamlBufferMenuItems is the right-click menu's rows, which the Edit menu
// offers as well.
func yamlBufferMenuItems() []ctxMenuItem {
	hasSel := !yamlEd.sel.empty()
	hasClip := imguibackend.ClipboardText() != ""
	d := yamlEd.doc
	return []ctxMenuItem{
		{Label: "Undo", Shortcut: "Ctrl+Z", Disabled: !d.undo.CanUndo(), Do: func() { yamlQueueOp(yamlUndoOp()) }},
		{Label: "Redo", Shortcut: "Ctrl+Y", Disabled: !d.undo.CanRedo(), Do: func() { yamlQueueOp(yamlRedoOp()) }},
		{Separator: true},
		{Label: "Cut", Shortcut: "Ctrl+X", Disabled: !hasSel, Do: func() { yamlQueueOp(yamlClipboardOp(scriptEditCut)) }},
		{Label: "Copy", Shortcut: "Ctrl+C", Disabled: !hasSel, Do: func() { yamlQueueOp(yamlClipboardOp(scriptEditCopy)) }},
		{Label: "Paste", Shortcut: "Ctrl+V", Disabled: !hasClip, Do: func() { yamlQueueOp(yamlClipboardOp(scriptEditPaste)) }},
		{Label: "Delete", Shortcut: "Del", Disabled: !hasSel, Do: func() { yamlQueueOp(yamlClipboardOp(scriptEditDelete)) }},
		{Separator: true},
		{Label: "Select All", Shortcut: "Ctrl+A", Do: func() { yamlQueueOp(yamlClipboardOp(scriptEditSelectAll)) }},
		{Separator: true},
		{Label: "Find", Shortcut: "Ctrl+F", Do: func() { yamlFindOpen(false) }},
		{Label: "Replace", Shortcut: "Ctrl+H", Do: func() { yamlFindOpen(true) }},
		{Label: "Go to Line", Shortcut: "Ctrl+G", Do: func() { yamlEd.gotoLine, yamlEd.showGoto = "", true }},
		{Separator: true},
		{Label: "Toggle Comment", Shortcut: "Ctrl+/", Do: func() {
			yamlQueueOp(yamlOp{Name: "comment", Record: yamlRecordSealed, NeedsSel: true, Fn: yamlToggleComment})
		}},
		{Label: "Indent", Shortcut: "Tab", Do: func() {
			yamlQueueOp(yamlOp{Name: "indent", Record: yamlRecordSealed, NeedsSel: true, Fn: yamlIndent})
		}},
		{Label: "Outdent", Shortcut: "Shift+Tab", Do: func() {
			yamlQueueOp(yamlOp{Name: "dedent", Record: yamlRecordSealed, NeedsSel: true, Fn: yamlDedent})
		}},
		{Separator: true},
		{Label: "Suggest", Shortcut: "Ctrl+Space", Do: func() {
			yamlCompl.force = true
			yamlCompl.cursor = -1
			yamlEd.wantFocus = true
		}},
	}
}

// ---------- under the buffer ----------

// drawYAMLStatusLine is the caret's position and where in the level it is.
func drawYAMLStatusLine(d *yamlDoc) {
	ix := d.Index()
	cur := min(yamlEd.cursor, len(d.Text))
	line := ix.LineOf(cur)
	col := cur - ix.Start(line)
	if yamlEd.crumbGen != d.gen || yamlEd.crumbAt != cur {
		yamlEd.crumb = yamlBreadcrumb(ix, cur)
		yamlEd.crumbGen, yamlEd.crumbAt = d.gen, cur
	}
	editorTextMuted(fmt.Sprintf("Ln %d, Col %d", line+1, col+1))
	if yamlEd.crumb != "" {
		imgui.SameLine()
		editorTextColored(scriptHLPalette[yamlClassKey], yamlEd.crumb)
	}
	imgui.SameLine()
	editorTextMuted(fmt.Sprintf("· %s", plural(ix.Count(), "line")))
}

// drawYAMLProblems is what the loader would make of the file.
func drawYAMLProblems(d *yamlDoc) {
	diags := d.Diags()
	checking := !d.Checked()
	errCol := imgui.Vec4{X: 1, Y: 0.55, Z: 0.55, W: 1}
	switch {
	case len(diags) == 0 && checking:
		editorTextMuted("Checking...")
		return
	case len(diags) == 0:
		editorTextColored(imgui.Vec4{X: 0.5, Y: 0.9, Z: 0.5, W: 1}, "No problems — this loads.")
		imgui.SameLine()
		editorTextMuted("Hover a key for its documentation; Ctrl+Space for suggestions.")
		return
	}
	warns := yamlCountWarnings(diags)
	errs := len(diags) - warns
	head := ""
	switch {
	case errs > 0 && warns > 0:
		head = fmt.Sprintf("%s, %s", plural(errs, "error"), plural(warns, "warning"))
	case errs > 0:
		head = plural(errs, "error")
	default:
		head = plural(warns, "warning") + " — this loads, but the loader ignores these"
	}
	if errs > 0 {
		editorTextColored(errCol, head+" — this will not load, and will not be saved")
	} else {
		editorTextColored(scriptIssueColor, head)
	}
	if checking {
		imgui.SameLine()
		editorTextMuted("(checking the latest edit...)")
	}

	imgui.BeginChildV("##yamlproblems", imgui.Vec2{}, true, 0)
	ix := d.Index()
	const maxRows = 500
	for i, dg := range diags {
		if i == maxRows {
			editorTextMuted(fmt.Sprintf("...and %d more", len(diags)-i))
			break
		}
		col := errCol
		if dg.Warn {
			col = scriptIssueColor
		}
		label := dg.Msg
		if dg.Line > 0 {
			label = fmt.Sprintf("Ln %d   %s", dg.Line, dg.Msg)
		}
		imgui.PushStyleColor(imgui.StyleColorText, col)
		clicked := imgui.SelectableV(fmt.Sprintf("%s##yamldiag%d", label, i), false, 0, imgui.Vec2{})
		imgui.PopStyleColor()
		if clicked && dg.Line > 0 && dg.Line <= ix.Count() {
			l := dg.Line - 1
			yamlQueueOp(yamlSelectOp(ix.Start(l), ix.End(l)))
			yamlEd.scrollTo = ix.Start(l)
		}
		if imgui.IsItemHovered() && dg.Line > 0 && dg.Line <= ix.Count() {
			imgui.SetTooltip(strings.TrimSpace(ix.Line(dg.Line - 1)))
		}
	}
	imgui.EndChild()
}

// ---------- dialogs ----------

// drawYAMLOpenDialog lists the level files.
func drawYAMLOpenDialog() {
	imguibackend.WithModalPopup("Open Level File", &yamlEd.showOpen, func() {
		if Editor.SavePath != "" {
			if imgui.SelectableV("The level editor's level ("+filepath.Base(Editor.SavePath)+")", false, 0, imgui.Vec2{}) {
				cur := yamlEd.levelDoc
				switch {
				case cur != nil && sameLevelPath(cur.Path, Editor.SavePath):
					yamlShowLevel()
				case cur != nil && cur.Dirty():
					yamlStatusErr("%s has unsaved changes — save or revert it first", cur.Name())
				default:
					yamlOpenLevelEditorLevel()
				}
				imgui.CloseCurrentPopup()
			}
			imgui.Separator()
		}
		if len(yamlEd.levelFiles) == 0 {
			editorTextMuted("No level files in " + Editor.LevelsDir)
		}
		for _, name := range yamlEd.levelFiles {
			if imgui.SelectableV(name, false, 0, imgui.Vec2{}) {
				yamlOpenFile(filepath.Join(Editor.LevelsDir, name))
				imgui.CloseCurrentPopup()
			}
		}
		imgui.Separator()
		if imgui.Button("Cancel##yamlopen") {
			imgui.CloseCurrentPopup()
		}
	})
}

// drawYAMLGotoDialog asks for a line number.
func drawYAMLGotoDialog() {
	imguibackend.WithModalPopup("Go to Line", &yamlEd.showGoto, func() {
		ix := yamlEd.doc.Index()
		imgui.Text(fmt.Sprintf("Line (1 - %d)", ix.Count()))
		if imgui.IsWindowAppearing() {
			imgui.SetKeyboardFocusHere()
		}
		enter := imgui.InputTextV("##yamlgoto", &yamlEd.gotoLine,
			imgui.InputTextFlagsCharsDecimal|imgui.InputTextFlagsEnterReturnsTrue, nil)
		if imgui.Button("Go##yamlgoto") || enter {
			if n, err := strconv.Atoi(strings.TrimSpace(yamlEd.gotoLine)); err == nil {
				l := min(max(n, 1), ix.Count()) - 1
				yamlQueueOp(yamlSelectOp(ix.Start(l), ix.Start(l)))
				yamlEd.scrollTo = ix.Start(l)
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##yamlgoto") {
			imgui.CloseCurrentPopup()
		}
	})
}
