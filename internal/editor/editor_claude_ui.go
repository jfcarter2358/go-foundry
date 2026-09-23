package editor

// editor_claude_ui.go — drawing the panel.
//
// Three bands: a header of controls, the transcript, and the input box. The
// transcript is the only part that is at all involved, because a transcript is
// not a list of strings — it is user turns, streamed answers, tool calls that
// fill themselves in, and permission prompts that have to be answered before
// anything else can happen.
//
// The rule throughout: draw from the snapshot taken at the top of the frame,
// never from the session. Half a transcript from before a message arrived and
// half from after would be a race that shows up as flicker.

import (
	"fmt"
	"strings"
	"time"

	"foundry/internal/claude"
	"foundry/internal/imguibackend"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

var (
	claudeUserColor   = imgui.Vec4{X: 0.55, Y: 0.78, Z: 1.00, W: 1}
	claudeToolColor   = imgui.Vec4{X: 0.70, Y: 0.70, Z: 0.78, W: 1}
	claudeThinkColor  = imgui.Vec4{X: 0.58, Y: 0.55, Z: 0.70, W: 1}
	claudeErrColor    = imgui.Vec4{X: 1.00, Y: 0.50, Z: 0.50, W: 1}
	claudeWarnColor   = imgui.Vec4{X: 1.00, Y: 0.80, Z: 0.35, W: 1}
	claudeOKColor     = imgui.Vec4{X: 0.55, Y: 0.85, Z: 0.60, W: 1}
	claudeCodeBGColor = imgui.Vec4{X: 0.13, Y: 0.13, Z: 0.16, W: 1}
)

// claudeInputH is the height of the input box. Three lines: enough to see a
// paragraph-long instruction without it turning into a text editor.
const claudeInputH = 72

func drawClaudeWindow() {
	w := claudeState.win
	if w == nil {
		return
	}
	claudeLoadSettings()
	if claudeState.expanded == nil {
		claudeState.expanded = map[string]bool{}
	}
	imgui.SetNextWindowPos(imgui.Vec2{})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	if !imgui.BeginV("##clauderoot", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoCollapse) {
		imgui.End()
		return
	}
	// Code blocks are identified by their order within the frame, so the count
	// starts again here. See claudeCodeSeq.
	claudeCodeCounter = 0
	snap := claudeSnapshot()
	claudeDrawHeader(snap)
	imgui.Separator()
	claudeDrawTranscript(snap)
	claudeDrawFooter(snap)
	claudeDrawSessionPicker()
	imgui.End()
}

// claudeDrawHeader is the controls row: what is running, on which model, under
// which permission mode.
func claudeDrawHeader(snap claude.Snapshot) {
	if claudeState.binErr != nil {
		editorTextColored(claudeErrColor, "Claude Code is not available")
		imgui.PushTextWrapPos()
		editorTextColored(claudeErrColor, claudeState.binErr.Error())
		imgui.PopTextWrapPos()
		if imgui.Button("Look again##claudefind") {
			claudeFindCLI()
		}
		return
	}

	switch snap.Status {
	case claude.StatusWorking:
		editorTextColored(claudeWarnColor, "Working…")
	case claude.StatusWaiting:
		editorTextColored(claudeWarnColor, "Waiting for you")
	case claude.StatusExited:
		editorTextMuted("Not running")
	default:
		editorTextColored(claudeOKColor, "Ready")
	}

	imgui.SameLine()
	imgui.SetNextItemWidth(120)
	model := claudeState.model
	if model == "" {
		model = "default"
	}
	if imguibackend.BeginCombo("##claudemodel", model) {
		for _, m := range claudeModels {
			if imguibackend.SelectableV(m, m == model, 0, imgui.Vec2{}) {
				claudeState.model = m
				claudeSaveSettings()
				if snap.Status != claude.StatusExited && claudeState.sess != nil {
					claudeSetStatus("The model changes when you start a new conversation.")
				}
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Which model answers. A running conversation keeps the one it started with.")
	}

	imgui.SameLine()
	imgui.SetNextItemWidth(140)
	if imguibackend.BeginCombo("##claudemode", claudeModeLabel(claudeState.mode)) {
		for _, m := range claudeModes {
			if imguibackend.SelectableV(m.Label, m.Mode == claudeState.mode, 0, imgui.Vec2{}) {
				claudeSetMode(m.Mode)
				claudeSaveSettings()
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(m.Tip)
			}
		}
		imguibackend.EndCombo()
	}

	imgui.SameLine()
	if imgui.Button("New##claudenew") {
		claudeNewConversation()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Start a fresh conversation. This one stays in Claude Code's own history.")
	}
	imgui.SameLine()
	if imgui.Button("Resume…##clauderesume") {
		claudeOpenSessionPicker()
	}

	// The running total, right-aligned, where it is visible but not in the way.
	if snap.Cost > 0 || snap.Tokens > 0 {
		right := fmt.Sprintf("%s  $%.2f", claudeTokens(snap.Tokens), snap.Cost)
		w := imgui.CalcTextSize(right, false, 0).X
		imgui.SameLine()
		imgui.SetCursorPos(imgui.Vec2{X: imgui.WindowWidth() - w - 16, Y: imgui.CursorPosY()})
		editorTextMuted(right)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Context used this turn, and what the conversation has cost so far.")
		}
	}
}

func claudeModeLabel(mode string) string {
	for _, m := range claudeModes {
		if m.Mode == mode {
			return m.Label
		}
	}
	return mode
}

// claudeTokens reads a token count the way a person would.
func claudeTokens(n int) string {
	switch {
	case n <= 0:
		return ""
	case n < 1000:
		return fmt.Sprintf("%d tokens", n)
	default:
		return fmt.Sprintf("%.0fk tokens", float64(n)/1000)
	}
}

// claudeDrawTranscript is the scrolling conversation.
func claudeDrawTranscript(snap claude.Snapshot) {
	// Everything above the input box and the status line below it.
	h := -float32(claudeInputH + 56)
	if !imgui.BeginChildV("##claudescroll", imgui.Vec2{X: 0, Y: h}, false, 0) {
		imgui.EndChild()
		return
	}
	if len(snap.Entries) == 0 {
		claudeDrawEmptyState()
	}
	// ImGui lays out everything it is handed, whether or not it is on screen, and
	// a day's conversation is thousands of rows of wrapped text and code boxes.
	// Only the tail is drawn; the rest is one button away. (A list clipper is the
	// usual answer and does not fit: these rows have no common height, and their
	// height is not known until they are drawn.)
	from := 0
	if hidden := len(snap.Entries) - claudeState.shown(); hidden > 0 {
		from = hidden
		if imgui.Button(fmt.Sprintf("Show %s earlier##claudeshowmore", plural(hidden, "message"))) {
			claudeState.showMore += hidden
		}
		imgui.Spacing()
	}
	for i := from; i < len(snap.Entries); i++ {
		claudeDrawEntry(&snap.Entries[i], i)
	}
	// Follow the tail while new text arrives, but only while the user has not
	// scrolled up to read something — taking the scroll position away from
	// someone mid-sentence is the most irritating thing a chat panel can do.
	if snap.Seq != claudeState.lastSeq {
		claudeState.lastSeq = snap.Seq
		if imgui.ScrollY() >= imgui.ScrollMaxY()-4 {
			claudeState.scrollToEnd = true
		}
	}
	if claudeState.scrollToEnd {
		imgui.SetScrollHereY(1)
		claudeState.scrollToEnd = false
	}
	imgui.EndChild()
}

func claudeDrawEmptyState() {
	editorTextMuted("Ask Claude to build something in this project.")
	imgui.Spacing()
	for _, ex := range []string{
		"Write a script that opens the hangar door when the player is near it.",
		"Add a red warning light to level1 and make it pulse.",
		"Generate a rusted metal texture in tools/ and apply it to the crates.",
	} {
		editorTextMuted("  • " + ex)
	}
	imgui.Spacing()
	editorTextMuted("It sees the project's files, and the editor's own state through the Foundry tools.")
	claudeDrawContextVolumeHint()
}

// claudeDrawContextVolumeHint lists the level's named regions.
//
// Without this a context volume is invisible: the author placed it, possibly
// days ago, and nothing on screen says the name exists or how to use it. Shown
// in the empty state, which is where someone is deciding what to ask.
func claudeDrawContextVolumeHint() {
	vols := world.ContextVolumes(Editor.level)
	if len(vols) == 0 {
		return
	}
	imgui.Spacing()
	editorTextMuted("Named places in this level — mention one to talk about it:")
	for i, v := range vols {
		if i >= 8 {
			editorTextMuted(fmt.Sprintf("  …and %d more", len(vols)-8))
			break
		}
		line := "  @" + v.ID
		if v.Context.Note != "" {
			line += " — " + v.Context.Note
		}
		editorTextMuted(line)
	}
}

func claudeDrawEntry(e *claude.Entry, i int) {
	switch e.Kind {
	case claude.EntryUser:
		imgui.Spacing()
		editorTextColored(claudeUserColor, "You")
		claudeDrawWrapped(e.Text)
		claudeCopyable(fmt.Sprintf("user%d", i), e.Text)
		imgui.Spacing()
	case claude.EntryAssistant:
		claudeDrawMarkdown(e.Text)
		claudeCopyable(fmt.Sprintf("asst%d", i), e.Text)
		if e.Live {
			imgui.SameLine()
			editorTextMuted("▌")
		}
	case claude.EntryThinking:
		claudeDrawThinking(e, i)
	case claude.EntryTool:
		claudeDrawToolCall(e.Tool, i)
	case claude.EntryPermission:
		claudeDrawPermission(e.Perm, i)
	case claude.EntryNotice:
		editorTextMuted(e.Text)
	}
}

func claudeDrawWrapped(s string) {
	imgui.PushTextWrapPos()
	imgui.Text(s)
	imgui.PopTextWrapPos()
}

// claudeCopyable makes the item just drawn offer its text on a right-click.
//
// ImGui's labels cannot be selected, so without this there is no way at all to
// get a paragraph — or a script Claude has just written — out of the panel and
// into anything else. A context menu rather than a visible button: a copy button
// on every row would be most of what the transcript looked like.
func claudeCopyable(id, text string) {
	if text == "" {
		return
	}
	if imgui.BeginPopupContextItemV("##claudecopy"+id, 1) {
		if imgui.Selectable("Copy") {
			imguibackend.SdlClipboard{}.SetText(text)
			claudeSetStatus("Copied.")
		}
		imgui.EndPopup()
	}
}

// claudeDrawThinking shows reasoning collapsed. It is worth having — it is often
// where the answer to "why did it do that" is — but it is not the answer, and
// expanded by default it would bury the answer.
func claudeDrawThinking(e *claude.Entry, i int) {
	id := fmt.Sprintf("##claudethink%d", i)
	key := fmt.Sprintf("think%d", i)
	open := claudeState.expanded[key]
	label := "Thinking"
	if open {
		label = "Thinking (hide)"
	}
	imgui.PushStyleColor(imgui.StyleColorText, claudeThinkColor)
	if imgui.Button(label + id) {
		claudeState.expanded[key] = !open
	}
	imgui.PopStyleColor()
	if open {
		imgui.Indent()
		imgui.PushStyleColor(imgui.StyleColorText, claudeThinkColor)
		claudeDrawWrapped(e.Text)
		imgui.PopStyleColor()
		imgui.Unindent()
	}
}

// claudeDrawToolCall is one row per tool, which fills in when its result lands.
func claudeDrawToolCall(c *claude.ToolCall, i int) {
	if c == nil {
		return
	}
	key := "tool" + c.ID
	open := claudeState.expanded[key]

	marker := "…"
	col := claudeToolColor
	switch {
	case c.IsError:
		marker, col = "✗", claudeErrColor
	case c.Done:
		marker = "✓"
	}
	label := fmt.Sprintf("%s %s", marker, claudeToolLabel(c.Name))
	if c.Summary != "" {
		label += "  " + c.Summary
	}
	imgui.PushStyleColor(imgui.StyleColorText, col)
	if imgui.Button(label + "##claudetool" + c.ID) {
		claudeState.expanded[key] = !open
	}
	imgui.PopStyleColor()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Click to see what was passed and what came back.")
	}
	if !open {
		return
	}
	imgui.Indent()
	claudeDrawToolDetail(c)
	imgui.Unindent()
}

// claudeToolLabel is the editor's own tools under a name that says whose they
// are. mcp__foundry__editor_state is not a name to show anybody.
func claudeToolLabel(name string) string {
	if rest, ok := strings.CutPrefix(name, "mcp__foundry__"); ok {
		return "Foundry: " + strings.ReplaceAll(rest, "_", " ")
	}
	return name
}

func claudeDrawToolDetail(c *claude.ToolCall) {
	if in := claudeToolInputText(c); in != "" {
		claudeDrawCode(in, claudeIsDiffLike(c.Name))
	}
	if !c.Done {
		editorTextMuted("running…")
		return
	}
	if c.Result == "" {
		editorTextMuted("(no output)")
		return
	}
	col := claudeCodeBGColor
	imgui.PushStyleColor(imgui.StyleColorChildBg, col)
	claudeDrawCode(claudeClip(c.Result, claudeMaxResultLines), false)
	imgui.PopStyleColor()
}

// claudeMaxResultLines bounds what one tool result draws. ImGui lays out every
// line it is handed and a Read of a generated level is tens of thousands of
// them — the same reason the Git window clips a diff.
const claudeMaxResultLines = 200

func claudeClip(s string, max int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[:max], "\n") +
		fmt.Sprintf("\n… %d more lines", len(lines)-max)
}

// claudeIsDiffLike reports whether a tool's input should be drawn with diff
// colouring: the edits are the calls worth reading closely.
func claudeIsDiffLike(name string) bool {
	return name == "Edit" || name == "Write" || name == "NotebookEdit"
}

// claudeDrawCode draws a block in the code style, optionally colouring it as a
// diff. Same colours as the Git window, so a change reads the same wherever it
// is seen.
func claudeDrawCode(s string, asDiff bool) {
	imgui.PushStyleColor(imgui.StyleColorChildBg, claudeCodeBGColor)
	lines := strings.Split(s, "\n")
	h := float32(len(lines))*imgui.TextLineHeightWithSpacing() + 8
	if max := float32(320); h > max {
		h = max
	}
	id := fmt.Sprint(claudeCodeSeq())
	if imgui.BeginChildV("##code"+id, imgui.Vec2{X: 0, Y: h}, true, 0) {
		for _, l := range lines {
			switch {
			case !asDiff:
				imgui.Text(l)
			case strings.HasPrefix(l, "+"):
				editorTextColored(gitAddColor, l)
			case strings.HasPrefix(l, "-"):
				editorTextColored(gitDelColor, l)
			case strings.HasPrefix(l, "@@"):
				editorTextColored(gitHunkColor, l)
			default:
				imgui.Text(l)
			}
		}
	}
	imgui.EndChild()
	claudeCopyable("code"+id, s)
	imgui.PopStyleColor()
}

// claudeCodeSeq gives each code block its own ImGui id, by its position in the
// frame. Two children sharing an id share scroll state, which reads as blocks
// scrolling each other; ids that changed between frames would lose that state
// every frame instead. Counting from zero each frame gives every block the same
// id on every frame, as long as the transcript above it has not changed.
var claudeCodeCounter int

func claudeCodeSeq() int {
	claudeCodeCounter++
	return claudeCodeCounter
}

// claudeToolInputText is a tool's arguments as something worth reading: the new
// text of a write, the command of a Bash call, the JSON otherwise.
func claudeToolInputText(c *claude.ToolCall) string {
	return claudeToolInputSummary(c.Name, c.InputJSON)
}

// claudeDrawMarkdown draws an assistant message.
func claudeDrawMarkdown(s string) {
	for _, b := range claudeParseMarkdown(s) {
		switch b.Kind {
		case mdCode:
			claudeDrawCode(b.Text, false)
		case mdHeading:
			editorTextColored(claudeOKColor, claudeStripEmphasis(b.Text))
		case mdBullet:
			imgui.IndentV(float32(8 + 12*b.Level))
			claudeDrawWrapped("• " + claudeStripEmphasis(b.Text))
			imgui.UnindentV(float32(8 + 12*b.Level))
		case mdNumbered:
			imgui.IndentV(float32(8 + 12*b.Level))
			claudeDrawWrapped(claudeStripEmphasis(b.Text))
			imgui.UnindentV(float32(8 + 12*b.Level))
		default:
			claudeDrawWrapped(claudeStripEmphasis(b.Text))
		}
	}
}

// claudeDrawPermission is a request Claude is blocked on.
//
// Drawn in the transcript rather than as a modal on purpose: a modal would stop
// the editor, and the whole point of the panel is that the editor keeps working
// while Claude does. An unanswered request is also why the turn has stalled, so
// it belongs where the turn is.
func claudeDrawPermission(p *claude.Permission, _ int) {
	if p == nil {
		return
	}
	imgui.Spacing()
	if p.Answered {
		switch p.Decision {
		case claude.DecisionDeny:
			editorTextMuted(fmt.Sprintf("Declined %s.", p.DisplayName))
		default:
			editorTextMuted(fmt.Sprintf("Allowed %s.", p.DisplayName))
		}
		return
	}
	imgui.PushStyleColor(imgui.StyleColorChildBg, imgui.Vec4{X: 0.18, Y: 0.15, Z: 0.08, W: 1})
	if imgui.BeginChildV("##claudeperm"+p.RequestID, imgui.Vec2{X: 0, Y: 118}, true, 0) {
		editorTextColored(claudeWarnColor, "Claude wants to use "+p.DisplayName)
		if p.Description != "" {
			claudeDrawWrapped(p.Description)
		}
		if detail := claudeToolInputSummary(p.ToolName, p.InputJSON); detail != "" {
			editorTextMuted(claudeClip(detail, 6))
		}
		if imgui.Button("Allow##claudeallow" + p.RequestID) {
			claudeAnswer(p, claude.DecisionAllow)
		}
		if p.AlwaysLabel != "" {
			imgui.SameLine()
			if imgui.Button(p.AlwaysLabel + "##claudealways" + p.RequestID) {
				claudeAnswer(p, claude.DecisionAllowAlways)
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("For the rest of this conversation.")
			}
		}
		imgui.SameLine()
		if imgui.Button("Decline##claudedeny" + p.RequestID) {
			claudeAnswer(p, claude.DecisionDeny)
		}
		imgui.SameLine()
		imgui.SetNextItemWidth(-1)
		imgui.InputTextV("##claudedenynote"+p.RequestID, &claudeState.denyNote, 0, nil)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Optional: tell Claude why, and it will try something else.")
		}
	}
	imgui.EndChild()
	imgui.PopStyleColor()
	imgui.Spacing()
}

func claudeAnswer(p *claude.Permission, d claude.Decision) {
	if claudeState.sess == nil {
		return
	}
	claudeState.sess.Answer(p.RequestID, d, strings.TrimSpace(claudeState.denyNote))
	claudeState.denyNote = ""
	claudeState.scrollToEnd = true
}

// claudeDrawFooter is the input box and what sits around it.
func claudeDrawFooter(snap claude.Snapshot) {
	claudeDrawUnsavedWarning()
	claudeDrawContextLine()

	flags := imgui.InputTextFlagsEnterReturnsTrue | imgui.InputTextFlagsCtrlEnterForNewLine
	imgui.PushItemWidth(-1)
	send := imgui.InputTextMultilineV("##claudeinput", &claudeState.input,
		imgui.Vec2{X: -1, Y: claudeInputH}, flags, nil)
	imgui.PopItemWidth()
	if send {
		claudeSend()
		// Put the caret back where it was: the next instruction usually follows
		// straight on, and clicking back into the box every turn is a tax.
		imgui.SetKeyboardFocusHereV(-1)
	}

	working := snap.Status == claude.StatusWorking || snap.Status == claude.StatusWaiting
	imguibackend.EditorPushDisabled(working || claudeState.binErr != nil)
	if imgui.Button("Send##claudesend") {
		claudeSend()
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	imguibackend.EditorPushDisabled(!working)
	if imgui.Button("Stop##claudestop") {
		claudeStop()
	}
	imguibackend.EditorPopDisabled()
	imgui.SameLine()
	editorTextMuted("Enter sends · Ctrl+Enter for a new line")

	if claudeState.status != "" && time.Since(claudeState.statusAt) < 8*time.Second {
		imgui.SameLine()
		editorTextColored(claudeWarnColor, claudeState.status)
	}
}

// claudeDrawContextLine shows what will be sent with the message besides the
// message.
//
// It is shown rather than merely done: a note the sender cannot see is a note
// they cannot correct, and "why does it keep talking about the other chunk" is
// a question with no answer if the selection is being attached invisibly. The
// toggle is for the cases where it is wrong — asking a general question while
// something irrelevant happens to be selected.
func claudeDrawContextLine() {
	note := claudeContextNote()
	if note == "" {
		return
	}
	include := !claudeState.noContext
	if imgui.Checkbox("##claudectx", &include) {
		claudeState.noContext = !include
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Send what the editor is looking at with the message.\nTurn it off to ask about something else.")
	}
	imgui.SameLine()
	if include {
		editorTextMuted(claudeClip(note, 1))
	} else {
		editorTextMuted("(editor context off)")
	}
}
