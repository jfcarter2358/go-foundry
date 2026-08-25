package editor

// editor_script_ui.go — the script editor's ImGui.
//
// Four regions: a folder tree on the left (editor_script_tree.go), a tab strip
// over the buffer (editor_script_tabs.go), the buffer itself with its line
// numbers, and the diagnostics underneath. The diagnostics pane is the point of
// the whole window — a script that will not parse fails silently at runtime, and
// a list of exactly where and why is the difference between that being a
// two-second fix and a hunt.
//
// The language reference used to live below that pane, squeezed into whatever
// height was left. It is now a tab of the F1 overlay, which has the room to say
// what a builtin actually does rather than only that it exists — see
// app/script_reference.go.
//
// The text area is an InputTextMultiline rather than a hand-rolled editor. A
// real one means owning the cursor, selection, clipboard and undo, which is a
// text editor rather than a feature; what is added over the top is colour (the
// widget's own glyphs drawn transparent, coloured ones painted into its own
// child window) and a gutter beside it. Both depend on the widget never
// scrolling itself — see drawScriptEditorText.

import (
	"fmt"
	"strconv"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/script"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// Dialog flags, in the style the texture editor uses: a bool per dialog, raised
// from wherever and consumed by WithModalPopup at the end of the frame.
var (
	scriptEditorShowNew       bool
	scriptEditorShowOpen      bool
	scriptEditorShowSaveAs    bool
	scriptEditorShowNewFolder bool
	scriptEditorShowMove      bool
	scriptEditorNewName       string
	scriptEditorSaveAsName    string
	scriptEditorNewFolderIn   string
	scriptEditorNewFolderName string
	scriptEditorMoveFrom      string
	scriptEditorMoveTo        string
	scriptEditorShowDelete    bool
	scriptEditorDeleteRel     string

	// scriptEditorFiles and scriptEditorDirs are the last scan of
	// resources/scripts/. Nil means "never scanned"; empty means "scanned, and
	// there is nothing there", which is what stops an empty folder being
	// rescanned every frame.
	scriptEditorFiles []string
	scriptEditorDirs  []string

	scriptEditorStatusMsg  string
	scriptEditorStatusTime uint32
)

// scriptEditorStatus sets the transient message under the menu bar.
func scriptEditorStatus(format string, args ...any) {
	scriptEditorStatusMsg = fmt.Sprintf(format, args...)
	scriptEditorStatusTime = sdl.GetTicks()
}

// scriptEditorRescan re-reads the scripts folder, files and folders both.
func scriptEditorRescan() {
	scriptEditorFiles = fileutil.ScanScriptFiles()
	if scriptEditorFiles == nil {
		scriptEditorFiles = []string{}
	}
	scriptEditorDirs = fileutil.ScanScriptDirs()
	if scriptEditorDirs == nil {
		scriptEditorDirs = []string{}
	}
}

// drawScriptEditorWindow renders one frame. Called by RenderSecondaryWindows
// with this window's GL and ImGui contexts current.
func drawScriptEditorWindow() {
	w := scriptEditorWindow
	if w == nil {
		return
	}
	scriptEditorEnsureDoc()
	for _, d := range scriptDocs {
		// Every open document is reparsed, not only the visible one: the tab
		// strip shows which of them have unsaved work, and the file tree marks
		// the ones with problems.
		d.Reparse(sdl.GetTicks(), Editor.level)
	}

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

	const sidebarW = 250
	imgui.BeginChildV("##scriptfiles", imgui.Vec2{X: sidebarW, Y: 0}, true, 0)
	drawScriptEditorFileList()
	imgui.EndChild()

	imgui.SameLine()

	imgui.BeginChildV("##scriptmain", imgui.Vec2{X: 0, Y: 0}, false, 0)
	drawScriptEditorTabs()
	drawScriptEditorBuffer()
	drawScriptEditorDiagnostics()
	imgui.EndChild()

	// Dialogs are drawn unconditionally; WithModalPopup opens whichever flag was
	// raised this frame.
	drawScriptEditorNewDialog()
	drawScriptEditorOpenDialog()
	drawScriptEditorSaveAsDialog()
	drawScriptEditorNewFolderDialog()
	drawScriptEditorMoveDialog()
	drawScriptEditorDeleteConfirm()
	drawScriptEditorCloseConfirm()

	imgui.End()
}

// drawScriptEditorMenuBar draws the file actions and the run/reload buttons.
func drawScriptEditorMenuBar() {
	if imgui.Button("New") {
		scriptEditorNewName = ""
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
		uiwin.RequestWindowOpen(OpenLogWindow)
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

// drawScriptEditorBuffer draws the gutter and the text area beside it.
func drawScriptEditorBuffer() {
	d := activeScript

	// Leave room for the diagnostics pane below — as much as it needs, and no
	// more. With the reference moved out to F1 there is nothing under the
	// buffer but problems, so a clean file gets the whole window instead of
	// holding a third of it open for a list with nothing in it.
	reserve := float32(2.4 * float64(imgui.TextLineHeightWithSpacing()))
	if d.HasErrors() || d.HasIssues() {
		reserve = 180
	}
	h := imgui.ContentRegionAvail().Y - reserve
	if h < 120 {
		h = 120
	}

	// The document names every widget under it, so two tabs cannot share a
	// cursor, a selection or an undo stack — see scriptDoc.id.
	imgui.PushID(fmt.Sprintf("scriptdoc%d", d.id))
	defer imgui.PopID()

	gutterW := scriptGutterWidth(len(d.Lines()))
	top := imgui.CursorPos()

	// The text pane is drawn first even though it sits to the right, because it
	// is what decides the vertical scroll the gutter has to match. Drawing the
	// gutter first would leave it a frame behind, which during a drag-scroll
	// reads as the numbers sliding against their own lines.
	imgui.SetCursorPos(imgui.Vec2{X: top.X + gutterW, Y: top.Y})
	imgui.BeginChildV(scriptBufScrollID, imgui.Vec2{X: -1, Y: h}, true,
		imgui.WindowFlagsHorizontalScrollbar)
	drawScriptEditorText(d)
	imgui.EndChild()

	// Captured now: the buffer's context menu tests the rectangle the text pane
	// occupied, and the gutter below would otherwise be the last item drawn.
	bufMin, bufMax := imgui.ItemRectMin(), imgui.ItemRectMax()
	below := imgui.CursorPos()

	imgui.SetCursorPos(top)
	drawScriptEditorGutter(d, gutterW, h)
	imgui.SetCursorPos(below)

	drawScriptEditorBufferMenu(bufMin, bufMax)
	scriptEditorExpirePending()
}

// scriptBufScrollID names the window the buffer scrolls in, and scriptBufID the
// text area inside it. The second is also the name of the child window ImGui
// builds for the text area itself, which is what lets the colouring be drawn
// into it — see drawScriptEditorOverlay.
const (
	scriptBufScrollID = "##scriptscroll"
	scriptBufID       = "##scriptbuffer"
	scriptGutterID    = "##scriptgutter"
)

// scriptGutterWidth is how wide the line-number column has to be for a file of
// n lines, measured in the font it will be drawn in rather than guessed from a
// character count — the atlas is not necessarily monospaced.
func scriptGutterWidth(n int) float32 {
	digits := len(strconv.Itoa(max(n, 1)))
	return imgui.CalcTextSize(strings.Repeat("0", digits), false, 0).X +
		imgui.CurrentStyle().ItemSpacing().X*2 + 8
}

// scriptBufTextOrigin is where the first line of text was drawn last frame, in
// screen coordinates, and scriptBufTextValid says whether it means anything
// yet.
//
// The gutter takes its vertical alignment from this rather than recomputing it.
// Getting a line's y from first principles means reproducing the text area's
// own inset — the child's border, ImGui's frame padding, the outer window's
// scroll — and any of the three drifting would show up as numbers that are
// almost, but not quite, beside their lines. Reading back the one number the
// text was actually drawn at cannot drift.
var (
	scriptBufTextOrigin imgui.Vec2
	scriptBufTextValid  bool
)

// drawScriptEditorGutter draws the line numbers beside the buffer.
//
// A line with a parse error is numbered in red and one with a level-aware issue
// in amber, which is the whole reason the gutter is worth more than a ruler: the
// diagnostics pane says which lines are wrong, and this says where they are
// while you scroll past them.
func drawScriptEditorGutter(d *scriptDoc, w, h float32) {
	// NoScrollWithMouse as well as NoScrollbar: the wheel over the numbers
	// should move the text, and a gutter that scrolled independently of it
	// would be worse than one that did not scroll at all.
	imgui.BeginChildV(scriptGutterID, imgui.Vec2{X: w, Y: h}, false,
		imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoScrollWithMouse)
	defer imgui.EndChild()

	if !scriptBufTextValid {
		return
	}
	lineH := imgui.TextLineHeight()
	list := imgui.WindowDrawList()
	winMin, winMax := imgui.WindowPos(), imgui.WindowPos().Plus(imgui.WindowSize())
	right := winMax.X - imgui.CurrentStyle().ItemSpacing().X

	muted := imgui.PackedColorFromVec4(imgui.Vec4{X: 0.45, Y: 0.49, Z: 0.56, W: 1})
	errCol := imgui.PackedColorFromVec4(imgui.Vec4{X: 1, Y: 0.45, Z: 0.45, W: 1})
	issueCol := imgui.PackedColorFromVec4(scriptIssueColor)
	current := imgui.PackedColorFromVec4(imgui.CurrentStyle().Color(imgui.StyleColorText))

	caretLine := scriptBufCaretLine
	lines := len(d.Lines())
	// Only the band the pane can actually show: a thousand-line file is a
	// thousand AddText calls otherwise, every frame, for twenty visible rows.
	first := int((winMin.Y - scriptBufTextOrigin.Y) / lineH)
	last := int((winMax.Y-scriptBufTextOrigin.Y)/lineH) + 1

	for i := max(first, 0); i <= min(last, lines-1); i++ {
		y := scriptBufTextOrigin.Y + float32(i)*lineH
		if y+lineH < winMin.Y || y > winMax.Y {
			continue
		}
		num := strconv.Itoa(i + 1)
		col := muted
		switch {
		case len(d.DiagnosticsOn(i+1)) > 0:
			col = errCol
		case len(d.IssuesOn(i+1)) > 0:
			col = issueCol
		case i == caretLine:
			// The line the cursor is on, at full contrast: with the buffer
			// scrolled, "where am I" is the question the gutter is asked most.
			col = current
		}
		// Right-aligned, so the digits line up as the file grows past 99.
		x := right - imgui.CalcTextSize(num, false, 0).X
		list.AddText(imgui.Vec2{X: x, Y: y}, col, num)
	}
}

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
	scriptBufActive = active

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

	// Frame padding, and not only the cursor start: a multi-line InputText
	// insets its text by exactly that much inside its child window before
	// drawing a glyph. Without it the coloured text lands half a character to
	// the left of the widget's own — which is invisible while reading, since
	// the widget's glyphs are transparent, and unmissable the moment anything
	// is selected: ImGui draws the selection over the real glyph positions, so
	// a highlight appeared to start midway through its first character.
	origin := imgui.WindowPos().Plus(imgui.CursorStartPos()).
		Plus(imgui.CurrentStyle().FramePadding())
	scriptBufTextOrigin, scriptBufTextValid = origin, true

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
	scriptBufCaretLine = line
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

	// scriptBufActive is whether the text area held the keyboard on the last
	// frame drawn. Read from the window's event filter, which runs between
	// frames and so cannot ask ImGui directly — see scriptEditorFilterEvent.
	scriptBufActive bool

	// scriptBufCursor is where the cursor was as of the last frame the text
	// area was active, scriptBufLastCursor is where it was when the buffer was
	// last scrolled to follow it, scriptBufCaretAt is when it last moved —
	// which is what restarts the caret's blink — and scriptBufCaretLine is the
	// line it landed on, which is what the gutter highlights.
	scriptBufCursor     int
	scriptBufLastCursor int
	scriptBufCaretAt    uint32
	scriptBufCaretLine  int
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
// Testing the rectangle the widget occupied is what works instead, and that
// rectangle is passed in rather than read from ImGui because the gutter is
// drawn after the text pane and would otherwise be the item measured.
func drawScriptEditorBufferMenu(bufMin, bufMax imgui.Vec2) {
	const id = "##scriptbufmenu"
	if imgui.IsMouseClicked(1) &&
		imgui.IsWindowHoveredV(imgui.HoveredFlagsChildWindows) &&
		vec2InRect(imgui.MousePos(), bufMin, bufMax) {
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
		imgui.SameLine()
		editorTextMuted("— F1 for the events, the standard library and every property.")
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
	scriptEditorAfterSave(d)
}

// scriptEditorAfterSave is what every successful write does next.
//
// Hot reload: a script saved while the game is running should take effect
// without a restart, which is most of why an in-editor editor is worth having
// at all.
func scriptEditorAfterSave(*scriptDoc) {
	script.ScriptReload()
	scriptEditorRescan()
}

// scriptEditorOpenFile brings a script up in a tab, or switches to the tab it
// is already in.
//
// name is relative to the scripts folder. Re-opening a file that is already
// open must never re-read it: the tab may be holding unsaved edits, and
// "clicked the file I am editing in the sidebar" would then mean discarding
// them.
func scriptEditorOpenFile(name string) {
	path := fileutil.ScriptsDirName + "/" +
		strings.TrimPrefix(strings.TrimSuffix(name, fileutil.ScriptExt), fileutil.ScriptsDirName+"/") +
		fileutil.ScriptExt
	if open := scriptDocForPath(path); open != nil {
		scriptEditorShowDoc(open)
		return
	}
	d, err := OpenScriptDoc(name)
	if err != nil {
		scriptEditorStatus("%v", err)
		return
	}
	scriptEditorAdoptEmptyTab(d)
	scriptEditorShowDoc(d)
	scriptEditorStatus("Opened %s", d.Path)
}

// scriptEditorReloadAll re-reads every script the level uses.
func scriptEditorReloadAll() {
	script.ScriptReload()
	scriptEditorRescan()
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
		editorTextMuted("A name with a / in it creates the folders it needs.")
		imgui.InputTextV("##newscriptname", &scriptEditorNewName, 0, nil)
		if imgui.Button("Create") && scriptEditorNewName != "" {
			d := NewScriptDoc()
			if err := d.Save(scriptEditorNewName); err != nil {
				scriptEditorStatus("%v", err)
			} else {
				scriptEditorAdoptEmptyTab(d)
				scriptEditorShowDoc(d)
				scriptEditorRescan()
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
		editorTextMuted("A name with a / in it creates the folders it needs.")
		imgui.InputTextV("##saveasscriptname", &scriptEditorSaveAsName, 0, nil)
		if imgui.Button("Save##saveasscript") && scriptEditorSaveAsName != "" {
			if err := activeScript.Save(scriptEditorSaveAsName); err != nil {
				scriptEditorStatus("%v", err)
			} else {
				scriptEditorStatus("Saved %s", activeScript.Path)
				scriptEditorAfterSave(activeScript)
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

// drawScriptEditorNewFolderDialog makes a folder in the scripts tree.
func drawScriptEditorNewFolderDialog() {
	imguibackend.WithModalPopup("New Folder", &scriptEditorShowNewFolder, func() {
		where := scriptEditorNewFolderIn
		if where == "" {
			where = "resources/scripts/"
		} else {
			where += "/"
		}
		imgui.Text("New folder in " + where)
		imgui.InputTextV("##newfoldername", &scriptEditorNewFolderName, 0, nil)
		if imgui.Button("Create##newfolder") && scriptEditorNewFolderName != "" {
			rel := strings.Trim(scriptEditorNewFolderName, "/")
			if scriptEditorNewFolderIn != "" {
				rel = scriptEditorNewFolderIn + "/" + rel
			}
			if err := fileutil.MakeScriptDir(rel); err != nil {
				scriptEditorStatus("%v", err)
			} else {
				scriptEditorRescan()
				scriptEditorStatus("Created %s/", rel)
			}
			scriptEditorNewFolderName = ""
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##newfolder") {
			imgui.CloseCurrentPopup()
		}
	})
}

// drawScriptEditorMoveDialog moves or renames a script.
//
// A move is a rename with a folder in it, so there is one dialog rather than
// two — and one place where the open tab has to be told its file went somewhere
// else, which is the part that would be forgotten in the second.
func drawScriptEditorMoveDialog() {
	imguibackend.WithModalPopup("Move or rename", &scriptEditorShowMove, func() {
		imgui.Text("Moving " + scriptEditorMoveFrom)
		editorTextMuted("Give it a new path, relative to resources/scripts/.")
		imgui.InputTextV("##movescriptto", &scriptEditorMoveTo, 0, nil)
		if imgui.Button("Move") && scriptEditorMoveTo != "" {
			scriptEditorMoveFile(scriptEditorMoveFrom, scriptEditorMoveTo)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##movescript") {
			imgui.CloseCurrentPopup()
		}
	})
}

// scriptEditorMoveFile renames a script on disk and repoints whatever was
// looking at it.
//
// The levels and entity definitions that name the old path are *not* rewritten,
// and the status line says so: they live outside the scripts folder, a script
// can be named by several of them, and a move that silently edited level files
// would be a far bigger operation than the one that was asked for. The lint
// pass reports the dangling reference the next time the level is opened.
func scriptEditorMoveFile(from, to string) {
	if !strings.HasSuffix(to, fileutil.ScriptExt) {
		to += fileutil.ScriptExt
	}
	if err := fileutil.MoveScriptFile(from, to); err != nil {
		scriptEditorStatus("%v", err)
		return
	}
	oldPath := fileutil.ScriptsDirName + "/" + from
	newPath := fileutil.ScriptsDirName + "/" + strings.TrimPrefix(to, fileutil.ScriptsDirName+"/")
	if d := scriptDocForPath(oldPath); d != nil {
		d.Path = newPath
	}
	script.ScriptParamsInvalidate(oldPath)
	script.ScriptReload()
	scriptEditorRescan()
	scriptEditorStatus("Moved to %s — anything that named the old path still does", newPath)
}

// drawScriptEditorDeleteConfirm asks before removing a script from disk.
//
// Everything else the sidebar does can be undone by doing it again — a move is
// a move back, a folder is a folder. This cannot, so it is the one file action
// with a confirmation, and the confirmation is where it says what will break:
// the level that names the script keeps naming it, and a script that imports it
// stops compiling the moment it is gone. Neither is a reason to refuse — a file
// nothing uses yet is the usual thing to delete — but both are worth reading
// before clicking.
func drawScriptEditorDeleteConfirm() {
	imguibackend.WithModalPopup("Delete script?", &scriptEditorShowDelete, func() {
		rel := scriptEditorDeleteRel
		if rel == "" {
			imgui.CloseCurrentPopup()
			return
		}
		path := fileutil.ScriptsDirName + "/" + rel
		imgui.Text("Delete " + path + "?")
		editorTextMuted("It is removed from disk. This cannot be undone.")

		if d := scriptDocForPath(path); d != nil && d.Dirty() {
			editorTextColored(scriptIssueColor,
				"The open tab has unsaved changes. They go with it.")
		}
		if named, importedBy := script.ScriptUsage(path); named || len(importedBy) > 0 {
			imgui.Separator()
			if named {
				editorTextColored(scriptIssueColor, "This level names this script.")
			}
			if len(importedBy) > 0 {
				editorTextColored(scriptIssueColor,
					"Imported by "+strings.Join(importedBy, ", ")+".")
			}
		}

		imgui.Separator()
		if imgui.Button("Delete##deletescript") {
			scriptEditorDeleteFile(rel)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##deletescript") {
			imgui.CloseCurrentPopup()
		}
	})
}

// scriptEditorDeleteFile removes a script and forgets everything that was
// holding it.
//
// The open tab is dropped rather than kept pointing at a file that is gone: a
// buffer whose Save would silently recreate the file just deleted is a worse
// answer than closing it, and the confirmation said as much. Reloading
// afterwards is what turns "the level names a script that no longer exists"
// into a diagnostic on the row rather than a surprise at play time.
func scriptEditorDeleteFile(rel string) {
	if err := fileutil.DeleteScriptFile(rel); err != nil {
		scriptEditorStatus("%v", err)
		return
	}
	path := fileutil.ScriptsDirName + "/" + rel
	if d := scriptDocForPath(path); d != nil {
		scriptEditorDropDoc(d)
	}
	script.ScriptParamsInvalidate(path)
	script.ScriptReload()
	scriptEditorRescan()
	scriptEditorStatus("Deleted %s", path)
}
