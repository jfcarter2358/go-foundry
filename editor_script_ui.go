package main

// editor_script_ui.go — the script editor's ImGui.
//
// Three panes: a file list on the left, the buffer in the middle, and the
// diagnostics underneath it. The diagnostics pane is the point of the whole
// window — a script that will not parse fails silently at runtime, and a list of
// exactly where and why is the difference between that being a two-second fix
// and a hunt.
//
// The text area is an InputTextMultiline rather than a hand-rolled editor with a
// gutter. A real gutter means owning the cursor, selection, clipboard and undo,
// which is a text editor rather than a feature; clicking a diagnostic to see its
// line, and a line-numbered error list beside the buffer, gets most of the value
// for a fraction of the surface.
//
// The colouring keeps that bargain. The widget still owns everything it owned
// before — it is drawn with its own glyphs made transparent and coloured ones
// painted into its own child window over the top, so nothing about editing
// changes. What that costs is scrolling, which has to move out to a window this
// side owns; see drawScriptEditorText.

import (
	"fmt"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/imguibackend"
	"foundry/internal/script"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// Dialog flags, in the style the texture editor uses: a bool per dialog, raised
// from wherever and consumed by WithModalPopup at the end of the frame.
var (
	scriptEditorShowNew    bool
	scriptEditorShowOpen   bool
	scriptEditorShowSaveAs bool
	scriptEditorNewName    string
	scriptEditorSaveAsName string
	scriptEditorFiles      []string
	scriptEditorStatusMsg  string
	scriptEditorStatusTime uint32
)

// scriptEditorStatus sets the transient message under the menu bar.
func scriptEditorStatus(format string, args ...any) {
	scriptEditorStatusMsg = fmt.Sprintf(format, args...)
	scriptEditorStatusTime = sdl.GetTicks()
}

// drawScriptEditorWindow renders one frame. Called by RenderSecondaryWindows
// with this window's GL and ImGui contexts current.
func drawScriptEditorWindow() {
	w := scriptEditorWindow
	if w == nil {
		return
	}
	if activeScript == nil {
		activeScript = NewScriptDoc()
		activeScript.reparseNow(Editor.level)
	}
	activeScript.Reparse(sdl.GetTicks(), Editor.level)

	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	if !imgui.BeginV("##scripteditor_root", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoCollapse) {
		imgui.End()
		return
	}

	drawScriptEditorMenuBar()
	imgui.Separator()

	const sidebarW = 220
	imgui.BeginChildV("##scriptfiles", imgui.Vec2{X: sidebarW, Y: 0}, true, 0)
	drawScriptEditorFileList()
	imgui.EndChild()

	imgui.SameLine()

	imgui.BeginChildV("##scriptmain", imgui.Vec2{X: 0, Y: 0}, false, 0)
	drawScriptEditorBuffer()
	drawScriptEditorDiagnostics()
	imgui.EndChild()

	// Dialogs are drawn unconditionally; WithModalPopup opens whichever flag was
	// raised this frame.
	drawScriptEditorNewDialog()
	drawScriptEditorOpenDialog()
	drawScriptEditorSaveAsDialog()

	imgui.End()
}

// drawScriptEditorMenuBar draws the file actions and the run/reload buttons.
func drawScriptEditorMenuBar() {
	if imgui.Button("New") {
		scriptEditorShowNew = true
	}
	imgui.SameLine()
	if imgui.Button("Open") {
		scriptEditorShowOpen = true
	}
	imgui.SameLine()

	// The latched-bool pattern: the condition is read *before* the button, since
	// clicking it changes the answer and re-testing afterwards would pop a style
	// colour that was never pushed, unbalancing ImGui's global stack.
	unsaved := activeScript != nil && activeScript.Dirty()
	imguibackend.EditorPushDisabled(!unsaved)
	if imgui.Button("Save") {
		scriptEditorSave()
	}
	imguibackend.EditorPopDisabled()

	imgui.SameLine()
	if imgui.Button("Save As") {
		scriptEditorShowSaveAs = true
	}
	imgui.SameLine()
	if imgui.Button("Reload All") {
		scriptEditorReloadAll()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Re-read every script the level uses, from disk.")
	}

	imgui.SameLine()
	if imgui.Button("Log") {
		// Deferred: this runs inside this window's own Draw, and opening a
		// window there would hand our imgui context away mid-frame. See
		// RequestWindowOpen.
		RequestWindowOpen(OpenLogWindow)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Open the log window: script output, and where errors happened.")
	}

	imgui.SameLine()
	drawScriptEditorRunButton()

	if scriptEditorStatusMsg != "" {
		if sdl.GetTicks()-scriptEditorStatusTime > 4000 {
			scriptEditorStatusMsg = ""
		} else {
			imgui.SameLine()
			editorTextMuted(scriptEditorStatusMsg)
		}
	}
}

// drawScriptEditorRunButton draws the deliberate editor-time run.
//
// Opt-in, never automatic, and it pushes an undo entry first — so running a
// script against the level you are editing is as undoable as any other editor
// operation. Everything else about scripts is gated on play.
func drawScriptEditorRunButton() {
	canRun := Editor.Active && activeScript != nil && !activeScript.HasErrors()
	imguibackend.EditorPushDisabled(!canRun)
	if imgui.Button("Run on the editor level") {
		scriptEditorRunOnEditorLevel()
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Calls this script's `main` against the level you are editing.\n" +
			"Pushes an undo entry first, so it can be taken back.")
	}

	// Issues do not disable the button. They are checked against whichever level
	// is open, and a script shared between levels will legitimately name things
	// this one does not have — so they are shown beside the button and the
	// author decides, rather than being treated as a reason it cannot run.
	if activeScript == nil || !activeScript.HasIssues() {
		return
	}
	imgui.SameLine()
	editorTextColored(scriptIssueColor,
		fmt.Sprintf("%d issue(s)", len(activeScript.Issues)))
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Checked against the open level — these parse fine but would do nothing.\n\n" +
			script.ScriptIssuesText(activeScript.Issues))
	}
}

// scriptIssueColor is the amber the level-aware checks are drawn in: distinct
// from the red a parse error gets, because one means "cannot run" and the other
// means "will run and do nothing".
var scriptIssueColor = imgui.Vec4{X: 1, Y: 0.78, Z: 0.35, W: 1}

// drawScriptEditorFileList lists the scripts folder and the level's attachments.
func drawScriptEditorFileList() {
	if imgui.Button("Rescan##scriptfiles") || scriptEditorFiles == nil {
		scriptEditorFiles = fileutil.ScanScriptFiles()
		if scriptEditorFiles == nil {
			// Distinguish "scanned, found nothing" from "never scanned", so an
			// empty folder is not rescanned every frame.
			scriptEditorFiles = []string{}
		}
	}
	imgui.Separator()

	if len(scriptEditorFiles) == 0 {
		editorTextWrapped("No scripts yet. New creates one in resources/scripts/.")
		return
	}
	for _, name := range scriptEditorFiles {
		path := fileutil.ScriptsDirName + "/" + name
		selected := activeScript != nil && activeScript.Path == path
		label := name
		if selected && activeScript.Dirty() {
			label += " *"
		}
		if imgui.SelectableV(label, selected, 0, imgui.Vec2{}) && !selected {
			scriptEditorOpenFile(name)
		}
		// A script the level actually uses is worth telling apart from one
		// sitting in the folder unreferenced.
		if diags := script.ScriptDiagnostics(path); len(diags) > 0 {
			imgui.SameLine()
			editorTextColored(imgui.Vec4{X: 1, Y: 0.4, Z: 0.4, W: 1}, "!")
			if imgui.IsItemHovered() {
				imgui.SetTooltip(diags.Error())
			}
		} else if issues := script.ScriptIssues(path); len(issues) > 0 {
			// Only when it compiled: a script with parse errors has not been
			// linted, and two markers on one row would say the same thing twice.
			imgui.SameLine()
			editorTextColored(scriptIssueColor, "?")
			if imgui.IsItemHovered() {
				imgui.SetTooltip(script.ScriptIssuesText(issues))
			}
		}
	}
}

// drawScriptEditorBuffer draws the text area.
func drawScriptEditorBuffer() {
	d := activeScript
	header := d.Name()
	if d.Dirty() {
		header += "  (unsaved)"
	}
	imgui.Text(header)

	// Leave room for the diagnostics pane below.
	h := imgui.ContentRegionAvail().Y - 160
	if h < 120 {
		h = 120
	}
	imgui.BeginChildV(scriptBufScrollID, imgui.Vec2{X: -1, Y: h}, true,
		imgui.WindowFlagsHorizontalScrollbar)
	drawScriptEditorText(d)
	imgui.EndChild()

	drawScriptEditorBufferMenu()
	scriptEditorExpirePending()
}

// scriptBufScrollID names the window the buffer scrolls in, and scriptBufID the
// text area inside it. The second is also the name of the child window ImGui
// builds for the text area itself, which is what lets the colouring be drawn
// into it — see drawScriptEditorOverlay.
const (
	scriptBufScrollID = "##scriptscroll"
	scriptBufID       = "##scriptbuffer"
)

// drawScriptEditorText draws the buffer, in colour.
//
// The text area is sized to its whole contents and scrolls in a window of ours
// rather than in its own. That is what makes the colouring possible: ImGui
// keeps a multi-line InputText's horizontal scroll in state this side cannot
// read, so any overlay drawn over a text area that had scrolled itself would
// sit a few characters off. With NoHorizontalScroll set and the widget sized to
// fit, both of its scrolls are pinned at zero, its glyphs land at exactly one
// place, and ours can land there too.
//
// The cost is that keeping the cursor in view becomes this side's job, which is
// scriptBufFollowCaret below.
func drawScriptEditorText(d *scriptDoc) {
	lines := d.HighlightLines()
	lineH := imgui.TextLineHeight()
	style := imgui.CurrentStyle()
	framePad, winPad := style.FramePadding(), style.WindowPadding()
	view := imgui.ContentRegionAvail()
	textCol := imgui.PackedColorFromVec4(style.Color(imgui.StyleColorText))

	// Sized past what the text needs on both axes, so that ImGui never finds
	// content taller or wider than the frame and scrolls the widget itself —
	// and never smaller than the view, so that a short script's background
	// still reaches the bottom of the pane instead of stopping at its last
	// line.
	size := imgui.Vec2{
		X: max(d.LineWidth()+framePad.X*2+winPad.X*2+8, view.X),
		Y: max(float32(len(lines))*lineH+framePad.Y*2+winPad.Y*2+lineH, view.Y),
	}
	origin := imgui.CursorPos() // where the widget sits in the scrolled content

	if scriptBufWantFocus {
		// Takes effect on the frame *after* this one, which is the whole
		// reason a parked operation waits rather than running at once: it
		// needs the widget active again to reach the selection.
		imgui.SetKeyboardFocusHere()
		scriptBufWantFocus = false
	}

	// The widget draws its own glyphs in a colour with no alpha, and the
	// coloured ones go over the top. This takes the caret with it — ImGui draws
	// that in the text colour too — so drawScriptEditorOverlay draws that as
	// well.
	imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{})
	text := d.Text
	changed := imgui.InputTextMultilineV(scriptBufID, &text, size,
		imgui.InputTextFlagsAllowTabInput|imgui.InputTextFlagsCallbackAlways|
			imgui.InputTextFlagsNoHorizontalScroll,
		scriptBufferCallback)
	imgui.PopStyleColor()
	active := imgui.IsItemActive()

	if changed {
		// Before the overlay, not after: the widget has already drawn the new
		// text this frame, and painting the previous frame's colouring over it
		// would be a frame of visible nonsense on every keystroke.
		d.SetText(text, sdl.GetTicks())
		lines = d.HighlightLines()
	}

	drawScriptEditorOverlay(lines, size, lineH, textCol, active)
	if active {
		scriptBufFollowCaret(lines, origin.Plus(framePad).Plus(winPad), lineH, view)
	}
}

// drawScriptEditorOverlay paints the coloured text over the widget's own.
//
// It draws inside the text area's child window rather than beside it, by
// beginning that same window a second time. ImGui builds the child from the
// widget's label and its own ID, both of which this call reproduces, so this is
// the same window appended to rather than a new one — which is the only way to
// share its coordinate space, its clipping and its draw order. Appending adds
// nothing to its layout, so the widget's content size, and with it the scroll
// that would break the alignment, stay exactly where they were.
func drawScriptEditorOverlay(lines [][]scriptHLChunk, size imgui.Vec2, lineH float32, textCol imgui.PackedColor, active bool) {
	// The visible band, in the scrolled window this is called from, decides
	// which lines are worth drawing at all.
	first := int((imgui.ScrollY() - lineH) / lineH)
	last := int((imgui.ScrollY() + imgui.WindowSize().Y + lineH) / lineH)

	if !imgui.BeginChildV(scriptBufID, size, true, 0) {
		imgui.EndChild()
		return
	}
	defer imgui.EndChild()

	list := imgui.WindowDrawList()
	origin := imgui.WindowPos().Plus(imgui.CursorStartPos())

	for i := max(first, 0); i <= min(last, len(lines)-1); i++ {
		x, y := origin.X, origin.Y+float32(i)*lineH
		for _, c := range lines[i] {
			w := imgui.CalcTextSize(c.Text, false, 0).X
			// Indentation is most of a script's plain runs, and drawing spaces
			// costs the same as drawing anything else.
			if !isBlank(c.Text) {
				list.AddText(imgui.Vec2{X: x, Y: y}, scriptHLColor(c.Class, textCol), c.Text)
			}
			x += w
		}
	}

	if active {
		drawScriptEditorCaret(list, lines, origin, lineH, textCol)
	}
}

// drawScriptEditorCaret draws the cursor the transparent text colour hid.
//
// The blink is this side's own — ImGui's phase is in state that is not reachable
// from here — restarted whenever the cursor moves, which is the part of the
// behaviour that matters: a caret that blinks off mid-keystroke reads as a
// dropped character.
func drawScriptEditorCaret(list imgui.DrawList, lines [][]scriptHLChunk, origin imgui.Vec2, lineH float32, col imgui.PackedColor) {
	line, x := scriptCaretPos(lines, scriptBufCursor)
	if line < 0 {
		return
	}
	if (sdl.GetTicks()-scriptBufCaretAt)%1200 > 800 {
		return
	}
	at := imgui.Vec2{X: origin.X + x, Y: origin.Y + float32(line)*lineH}
	list.AddLine(at, imgui.Vec2{X: at.X, Y: at.Y + lineH}, col)
}

// scriptCaretPos returns the line the cursor is on and how far along it the
// cursor sits, in pixels. A cursor past the end of the buffer — which is what a
// stale reading from the last frame the widget was active looks like — reports
// line -1.
func scriptCaretPos(lines [][]scriptHLChunk, cursor int) (line int, x float32) {
	at := 0
	for i, chunks := range lines {
		n := 0
		for _, c := range chunks {
			n += len(c.Text)
		}
		if cursor <= at+n {
			return i, imgui.CalcTextSize(scriptLinePrefix(chunks, cursor-at), false, 0).X
		}
		at += n + 1 // past the newline
	}
	return -1, 0
}

// scriptLinePrefix returns the first n bytes of a line.
func scriptLinePrefix(chunks []scriptHLChunk, n int) string {
	var b strings.Builder
	for _, c := range chunks {
		if n <= 0 {
			break
		}
		if n < len(c.Text) {
			b.WriteString(c.Text[:n])
			break
		}
		b.WriteString(c.Text)
		n -= len(c.Text)
	}
	return b.String()
}

// scriptBufFollowCaret scrolls the buffer to keep the cursor in view.
//
// Only when the cursor has moved: scrolling on every frame the widget is active
// would take the wheel away from the reader who is looking somewhere else.
func scriptBufFollowCaret(lines [][]scriptHLChunk, origin imgui.Vec2, lineH float32, view imgui.Vec2) {
	if scriptBufCursor == scriptBufLastCursor {
		return
	}
	scriptBufLastCursor = scriptBufCursor
	scriptBufCaretAt = sdl.GetTicks()

	line, x := scriptCaretPos(lines, scriptBufCursor)
	if line < 0 {
		return
	}
	top, left := origin.Y+float32(line)*lineH, origin.X+x
	switch {
	case top < imgui.ScrollY():
		imgui.SetScrollY(top)
	case top+lineH > imgui.ScrollY()+view.Y:
		imgui.SetScrollY(top + lineH - view.Y)
	}
	// A margin either side horizontally, so that typing at the right-hand edge
	// does not leave the caret sitting on it.
	const margin = 24
	switch {
	case left-margin < imgui.ScrollX():
		imgui.SetScrollX(max(left-margin, 0))
	case left+margin > imgui.ScrollX()+view.X:
		imgui.SetScrollX(left + margin - view.X)
	}
}

// ---------- the buffer's selection, clipboard and context menu ----------

// scriptEditPendingMS is how long a parked operation waits for the text area to
// take the keyboard focus back before it is dropped. Two frames is all it should
// need; the limit is only there so that a focus that never returns cannot leave
// a paste armed to fire minutes later.
const scriptEditPendingMS = 1000

var (
	// scriptBufSel is the selection as of the last frame the text area was
	// active. It outlives that frame on purpose: opening the context menu takes
	// the focus away, and the menu still has to know whether to offer Cut.
	scriptBufSel scriptSel

	// scriptBufPending is the operation waiting for the callback, and
	// scriptBufPendingAt is when it was asked for.
	scriptBufPending   scriptEditOp
	scriptBufPendingAt uint32

	// scriptBufWantFocus asks the text area for the keyboard focus next frame.
	scriptBufWantFocus bool

	// scriptBufCursor is where the cursor was as of the last frame the text
	// area was active, scriptBufLastCursor is where it was when the buffer was
	// last scrolled to follow it, and scriptBufCaretAt is when it last moved —
	// which is what restarts the caret's blink. All three are only meaningful
	// while the widget is active, which is the only time they are read.
	scriptBufCursor     int
	scriptBufLastCursor int
	scriptBufCaretAt    uint32
)

// scriptBufferCallback runs once per frame while the text area holds the
// keyboard focus. It is the only place the widget's selection can be read or
// written, so it does double duty: remembering where the selection is, and
// applying whatever the context menu parked.
func scriptBufferCallback(data imgui.InputTextCallbackData) int32 {
	sel := scriptSel{Start: data.SelectionStart(), End: data.SelectionEnd()}
	scriptBufSel = sel.ordered(len(data.Buffer()))
	scriptBufCursor = data.CursorPos()

	if scriptBufPending == scriptEditNone {
		return 0
	}
	op := scriptBufPending
	scriptBufPending = scriptEditNone
	scriptApplyEdit(data, op)
	return 0
}

// scriptApplyEdit performs op against the widget's own buffer.
//
// The splice is done with DeleteBytes/InsertBytes rather than by handing ImGui a
// wholly new string, so the widget's undo history sees an ordinary edit and
// Ctrl+Z takes back a paste the way it takes back typing.
func scriptApplyEdit(data imgui.InputTextCallbackData, op scriptEditOp) {
	text := string(data.Buffer())
	sel := scriptSel{Start: data.SelectionStart(), End: data.SelectionEnd()}
	e, ok := scriptEditFor(op, text, sel, imguibackend.ClipboardText())
	if !ok {
		return
	}
	if e.SetClip {
		imguibackend.SdlClipboard{}.SetText(e.Clip)
	}
	if e.End > e.Start {
		data.DeleteBytes(e.Start, e.End-e.Start)
	}
	if e.Text != "" {
		data.InsertBytes(e.Start, []byte(e.Text))
	}
	// Order matters to ImGui's read-back: it takes the cursor as the anchor and
	// reads a selection bound equal to it as "collapsed here".
	data.SetCursorPos(e.Sel.End)
	data.SetSelectionStart(e.Sel.Start)
	data.SetSelectionEnd(e.Sel.End)
}

// scriptEditorRequest parks an operation for the callback and asks for the
// focus it needs to run.
//
// Nothing can be done here and now. The popup took the keyboard focus from the
// text area the moment it opened, which deactivates the widget — and an inactive
// InputText runs no callback, so there is no way to reach its selection.
//
// The popup closes itself: a menu row picked inside one always does.
func scriptEditorRequest(op scriptEditOp) {
	scriptBufPending = op
	scriptBufPendingAt = sdl.GetTicks()
	scriptBufWantFocus = true
}

// scriptEditorExpirePending drops an operation the text area never came back
// for — a window closed with the menu open, say.
func scriptEditorExpirePending() {
	if scriptBufPending != scriptEditNone && sdl.GetTicks()-scriptBufPendingAt > scriptEditPendingMS {
		scriptBufPending = scriptEditNone
	}
}

// drawScriptEditorBufferMenu draws the text area's right-click menu.
//
// It cannot hang off BeginPopupContextItem, which is what this would normally
// be. InputTextMultiline draws into a child window, so for every click *inside*
// the text the hovered window is that child rather than this one, and
// IsItemHovered is false — the item is never hovered at the moment it matters.
// Testing the rectangle the widget just occupied is what works instead.
func drawScriptEditorBufferMenu() {
	const id = "##scriptbufmenu"
	if imgui.IsMouseClicked(1) &&
		imgui.IsWindowHoveredV(imgui.HoveredFlagsChildWindows) &&
		vec2InRect(imgui.MousePos(), imgui.ItemRectMin(), imgui.ItemRectMax()) {
		imgui.OpenPopup(id)
	}
	if !imgui.BeginPopup(id) {
		return
	}
	ctxDrawMenuItems(scriptEditorBufferMenuItems())
	imgui.EndPopup()
}

// scriptEditorBufferMenuItems is the menu's rows. The shortcuts are listed
// because they are the faster way to do all of this — the menu is there for the
// hand already on the mouse, and for saying that the shortcuts exist.
func scriptEditorBufferMenuItems() []ctxMenuItem {
	hasSel := !scriptBufSel.empty()
	hasClip := imguibackend.ClipboardText() != ""
	return []ctxMenuItem{
		{Label: "Cut", Shortcut: "Ctrl+X", Disabled: !hasSel,
			Do: func() { scriptEditorRequest(scriptEditCut) }},
		{Label: "Copy", Shortcut: "Ctrl+C", Disabled: !hasSel,
			Do: func() { scriptEditorRequest(scriptEditCopy) }},
		{Label: "Paste", Shortcut: "Ctrl+V", Disabled: !hasClip,
			Do: func() { scriptEditorRequest(scriptEditPaste) }},
		{Label: "Delete", Shortcut: "Del", Disabled: !hasSel,
			Do: func() { scriptEditorRequest(scriptEditDelete) }},
		{Separator: true},
		{Label: "Select All", Shortcut: "Ctrl+A",
			Do: func() { scriptEditorRequest(scriptEditSelectAll) }},
	}
}

// vec2InRect reports whether p lies within the rectangle (min, max).
func vec2InRect(p, min, max imgui.Vec2) bool {
	return p.X >= min.X && p.X < max.X && p.Y >= min.Y && p.Y < max.Y
}

// drawScriptEditorDiagnostics draws the error list.
//
// This is the pane that earns the window: a script that will not parse fails
// silently at runtime, and the whole list at once is what makes fixing a pasted
// block a single pass instead of one error per save.
func drawScriptEditorDiagnostics() {
	d := activeScript
	imgui.Separator()

	if !d.HasErrors() && !d.HasIssues() {
		editorTextColored(imgui.Vec4{X: 0.5, Y: 0.9, Z: 0.5, W: 1}, "No problems.")
		drawScriptEditorReference()
		return
	}

	switch {
	case d.HasErrors() && d.HasIssues():
		editorTextColored(imgui.Vec4{X: 1, Y: 0.5, Z: 0.5, W: 1},
			fmt.Sprintf("%d problem(s), %d issue(s)", len(d.Diags), len(d.Issues)))
	case d.HasErrors():
		editorTextColored(imgui.Vec4{X: 1, Y: 0.5, Z: 0.5, W: 1},
			fmt.Sprintf("%d problem(s)", len(d.Diags)))
	default:
		editorTextColored(scriptIssueColor,
			fmt.Sprintf("%d issue(s) — this parses, but the open level cannot do it", len(d.Issues)))
	}

	imgui.BeginChildV("##scriptdiags", imgui.Vec2{X: 0, Y: 0}, true, 0)
	lines := d.Lines()
	for i, e := range d.Diags {
		drawScriptEditorProblem(fmt.Sprintf("diag%d", i),
			imgui.Vec4{X: 1, Y: 0.6, Z: 0.6, W: 1}, e.Pos.Line, e.Pos.Col, e.Msg, lines)
	}
	for i, s := range d.Issues {
		drawScriptEditorProblem(fmt.Sprintf("issue%d", i),
			scriptIssueColor, s.Pos.Line, s.Pos.Col, s.Msg, lines)
	}
	imgui.EndChild()
}

// drawScriptEditorProblem draws one row of the pane: the message, then the line
// it is about underneath, so the two are read together rather than by scrolling
// between them.
func drawScriptEditorProblem(id string, col imgui.Vec4, line, colno int, msg string, lines []string) {
	imgui.PushID(id)
	defer imgui.PopID()
	editorTextWrappedColored(col, fmt.Sprintf("%d:%d  %s", line, colno, msg))
	if line >= 1 && line <= len(lines) {
		editorTextMuted("    " + strings.TrimRight(lines[line-1], " \t"))
	}
}

// drawScriptEditorReference lists what a script can call, when there is nothing
// more urgent to show. Generated from flang's own table, so a builtin added to
// the language appears here the same day.
func drawScriptEditorReference() {
	if !imgui.CollapsingHeader("Reference##scriptref") {
		return
	}
	imgui.BeginChildV("##scriptrefbody", imgui.Vec2{X: 0, Y: 0}, true, 0)

	imgui.Text("Functions")
	editorTextWrapped(strings.Join(scriptCompletions(activeScript), ", "))
	imgui.Separator()

	imgui.Text("Events")
	editorTextWrapped(":spawn:  :destroy:  :tick:  :enter:  :exit:  " +
		":collide:  :interact:  :anim_start:  :anim_end:")
	imgui.Separator()

	imgui.Text("Properties")
	for _, k := range scriptRefKindOrder {
		names := script.PropertyNames(k.kind)
		if len(names) == 0 {
			continue
		}
		imgui.Text("  " + k.label)
		editorTextWrapped("    " + strings.Join(names, ", "))
	}
	imgui.EndChild()
}

// scriptRefKindOrder is the order the reference lists object kinds. Fixed rather
// than map order, so the panel does not shuffle between frames.
var scriptRefKindOrder = []struct {
	label string
	kind  flang.Kind_
}{
	{"entity", flang.RefEntity},
	{"volume", flang.RefVolume},
	{"edge", flang.RefEdge},
	{"floor", flang.RefFloor},
	{"ceiling", flang.RefCeiling},
	{"chunk", flang.RefChunk},
	{"light", flang.RefLight},
	{"biome", flang.RefBiome},
}

// ---------- actions ----------

// scriptEditorSave writes the buffer and reloads whatever is using it.
func scriptEditorSave() {
	d := activeScript
	if d == nil {
		return
	}
	if d.Path == "" {
		scriptEditorShowSaveAs = true
		return
	}
	if err := d.Save(""); err != nil {
		scriptEditorStatus("Save failed: %v", err)
		return
	}
	scriptEditorStatus("Saved %s", d.Path)
	// Hot reload: a script saved while the game is running should take effect
	// without a restart, which is most of why an in-editor editor is worth
	// having at all.
	script.ScriptReload()
	scriptEditorFiles = nil
}

// scriptEditorOpenFile loads a script into the buffer.
func scriptEditorOpenFile(name string) {
	d, err := OpenScriptDoc(name)
	if err != nil {
		scriptEditorStatus("%v", err)
		return
	}
	activeScript = d
	scriptEditorStatus("Opened %s", d.Path)
}

// scriptEditorReloadAll re-reads every script the level uses.
func scriptEditorReloadAll() {
	script.ScriptReload()
	scriptEditorFiles = nil
	scriptEditorStatus("Reloaded all scripts")
}

// scriptEditorRunOnEditorLevel calls the buffer's `main` against the level being
// edited, pushing an undo entry first.
func scriptEditorRunOnEditorLevel() {
	if activeScript == nil || Editor.level == nil {
		return
	}
	EditorPushUndo()
	if err := script.RunScriptOnce(Editor.level, activeScript.Name(), activeScript.Text, "main"); err != nil {
		scriptEditorStatus("%v", err)
		return
	}
	if n := len(activeScript.Issues); n > 0 {
		scriptEditorStatus("Ran %s, with %d unresolved issue(s)", activeScript.Name(), n)
		return
	}
	scriptEditorStatus("Ran %s", activeScript.Name())
}

// ---------- dialogs ----------

func drawScriptEditorNewDialog() {
	imguibackend.WithModalPopup("New Script", &scriptEditorShowNew, func() {
		imgui.Text("Name (relative to resources/scripts/)")
		imgui.InputTextV("##newscriptname", &scriptEditorNewName, 0, nil)
		if imgui.Button("Create") && scriptEditorNewName != "" {
			d := NewScriptDoc()
			if err := d.Save(scriptEditorNewName); err != nil {
				scriptEditorStatus("%v", err)
			} else {
				activeScript = d
				scriptEditorFiles = nil
				scriptEditorStatus("Created %s", d.Path)
			}
			scriptEditorNewName = ""
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##newscript") {
			imgui.CloseCurrentPopup()
		}
	})
}

func drawScriptEditorOpenDialog() {
	imguibackend.WithModalPopup("Open Script", &scriptEditorShowOpen, func() {
		files := fileutil.ScanScriptFiles()
		if len(files) == 0 {
			imgui.Text("No scripts in resources/scripts/.")
		}
		for _, name := range files {
			if imgui.SelectableV(name, false, 0, imgui.Vec2{}) {
				scriptEditorOpenFile(name)
				imgui.CloseCurrentPopup()
			}
		}
		imgui.Separator()
		if imgui.Button("Cancel##openscript") {
			imgui.CloseCurrentPopup()
		}
	})
}

func drawScriptEditorSaveAsDialog() {
	imguibackend.WithModalPopup("Save Script As", &scriptEditorShowSaveAs, func() {
		imgui.Text("Name (relative to resources/scripts/)")
		imgui.InputTextV("##saveasscriptname", &scriptEditorSaveAsName, 0, nil)
		if imgui.Button("Save##saveasscript") && scriptEditorSaveAsName != "" {
			if err := activeScript.Save(scriptEditorSaveAsName); err != nil {
				scriptEditorStatus("%v", err)
			} else {
				scriptEditorStatus("Saved %s", activeScript.Path)
				script.ScriptReload()
				scriptEditorFiles = nil
			}
			scriptEditorSaveAsName = ""
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##saveasscript") {
			imgui.CloseCurrentPopup()
		}
	})
}
