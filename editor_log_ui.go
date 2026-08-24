package main

// editor_log_ui.go — the log window's ImGui.
//
// A toolbar, a counters row, and a scrolling list. What makes it a debugging
// tool rather than a text dump is three things:
//
//   - **It follows the tail, but stops when you scroll.** Autoscroll that cannot
//     be escaped is useless the moment you find the line you were looking for,
//     and autoscroll you have to re-enable by hand is annoying every other time.
//     Scrolling up releases it; scrolling back to the bottom takes it again.
//   - **Errors carry their position, and clicking one opens the script there.**
//     An error message that makes you go and find the line is half a message.
//   - **Pause freezes the view without stopping the log.** The interesting moment
//     is usually the one that just scrolled past, and the ring keeps filling
//     underneath so nothing is lost by looking.

import (
	"fmt"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/script"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// Window state. Package-level in the style of the texture and script editors:
// these outlive the window so closing and reopening keeps your filters.
var (
	logWindowPaused      bool
	logWindowAutoscroll  = true
	logWindowFilter      string
	logWindowFocusFilter bool
	logWindowShowInfo    = true
	logWindowShowWarn    = true
	logWindowShowError   = true
	logWindowShowEngine  = true

	// frozen is the snapshot the window is displaying while paused, so the view
	// genuinely stops rather than merely stopping scrolling.
	logWindowFrozen []script.LogEntry
	// lastSeq is the newest entry drawn, so autoscroll only fires when something
	// has actually arrived rather than on every frame.
	logWindowLastSeq uint64
)

// levelColors are the row colours, indexed by level. Info is left near the
// default text colour: most lines are info, and colouring them all would mean
// colouring nothing.
var levelColors = map[script.LogLevel]imgui.Vec4{
	script.LogInfo:  {X: 0.82, Y: 0.82, Z: 0.85, W: 1},
	script.LogWarn:  {X: 1.00, Y: 0.80, Z: 0.35, W: 1},
	script.LogError: {X: 1.00, Y: 0.45, Z: 0.45, W: 1},
}

// drawLogWindow renders one frame. Called by RenderSecondaryWindows with this
// window's GL and ImGui contexts current.
func drawLogWindow() {
	w := logWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	if !imgui.BeginV("##logroot", nil,
		imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoResize|
			imgui.WindowFlagsNoMove|imgui.WindowFlagsNoCollapse) {
		imgui.End()
		return
	}

	drawLogToolbar()
	drawLogCounters()
	imgui.Separator()
	drawLogList()

	imgui.End()
}

// drawLogToolbar draws the controls.
func drawLogToolbar() {
	if imgui.Button("Clear") {
		script.ScriptLog.Clear()
		logWindowFrozen = nil
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Empty the buffer.  Ctrl+L")
	}

	imgui.SameLine()
	// Latched before the button: clicking it flips the state, and re-testing
	// afterwards would pop a style colour that was never pushed.
	paused := logWindowPaused
	if paused {
		imgui.PushStyleColor(imgui.StyleColorButton, imgui.Vec4{X: 0.75, Y: 0.5, Z: 0.15, W: 1})
	}
	label := "Pause"
	if paused {
		label = "Resume"
	}
	if imgui.Button(label) {
		logWindowPaused = !logWindowPaused
		if !logWindowPaused {
			logWindowFrozen = nil
		}
	}
	if paused {
		imgui.PopStyleColor()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Freeze the view. The log keeps filling underneath.  Space")
	}

	imgui.SameLine()
	imgui.Checkbox("Follow", &logWindowAutoscroll)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Stay at the newest line. Scrolling up releases it;\nscrolling back to the bottom takes it again.")
	}

	imgui.SameLine()
	imgui.Text("  |  ")
	imgui.SameLine()
	imgui.Checkbox("info", &logWindowShowInfo)
	imgui.SameLine()
	imgui.Checkbox("warn", &logWindowShowWarn)
	imgui.SameLine()
	imgui.Checkbox("error", &logWindowShowError)
	imgui.SameLine()
	imgui.Checkbox("engine", &logWindowShowEngine)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Include the engine's own log, not just script output.")
	}

	// Filter box on its own line: it wants the width, and the toolbar above is
	// already full.
	if logWindowFocusFilter {
		imgui.SetKeyboardFocusHere()
		logWindowFocusFilter = false
	}
	imgui.SetNextItemWidth(-140)
	imgui.InputTextV("##logfilter", &logWindowFilter, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Substring match on the message and its source.  Ctrl+F")
	}
	imgui.SameLine()
	if imgui.Button("Clear filter") {
		logWindowFilter = ""
	}
}

// drawLogCounters draws the runtime's per-tick stats beside the buffer's own.
//
// They belong here rather than in the F3 overlay because they answer the
// question the log raises: a screen with no output is either a script that is
// not running or a script that has nothing to say, and "0 delivered" tells the
// two apart at a glance.
func drawLogCounters() {
	s := script.ScriptStatsSnapshot()
	parts := []string{
		fmt.Sprintf("%d held", script.ScriptLog.Len()),
		fmt.Sprintf("delivered %d", s.Delivered),
	}
	if s.Deferred > 0 {
		parts = append(parts, fmt.Sprintf("deferred %d", s.Deferred))
	}
	if s.Dropped > 0 {
		parts = append(parts, fmt.Sprintf("dropped %d", s.Dropped))
	}
	if s.Errors > 0 {
		parts = append(parts, fmt.Sprintf("errors %d", s.Errors))
	}
	if d := script.ScriptLog.Dropped(); d > 0 {
		parts = append(parts, fmt.Sprintf("%d evicted", d))
	}
	editorTextMuted(strings.Join(parts, "   ·   "))

	if !Editor.Active {
		return
	}
	// The commonest reason for an empty log, and the one least likely to be
	// guessed: scripts do not run while the editor is up.
	imgui.SameLine()
	editorTextColored(imgui.Vec4{X: 1, Y: 0.8, Z: 0.35, W: 1},
		"   scripts are paused while the editor is open (` to play)")
}

// drawLogList draws the entries.
func drawLogList() {
	entries := logWindowEntries()

	imgui.BeginChildV("##loglist", imgui.Vec2{X: 0, Y: 0}, true,
		imgui.WindowFlagsHorizontalScrollbar)

	shown := 0
	for _, e := range entries {
		if !logEntryVisible(e) {
			continue
		}
		shown++
		drawLogEntry(e)
	}
	if shown == 0 {
		editorTextMuted(logEmptyMessage(len(entries)))
	}

	// Autoscroll, and the rule that makes it usable: scrolling up releases it,
	// scrolling back to the bottom takes it again. Only re-pinned when something
	// new has arrived, so dragging the scrollbar to the bottom and stopping does
	// not fight the user for the rest of the session.
	newest := script.ScriptLog.Seq()
	if newest != logWindowLastSeq {
		logWindowLastSeq = newest
		if logWindowAutoscroll && !logWindowPaused {
			imgui.SetScrollHereY(1.0)
		}
	}
	if imgui.ScrollY() >= imgui.ScrollMaxY() {
		logWindowAutoscroll = true
	} else if imgui.ScrollMaxY() > 0 {
		logWindowAutoscroll = false
	}

	imgui.EndChild()
}

// drawLogEntry draws one row.
func drawLogEntry(e script.LogEntry) {
	col, ok := levelColors[e.Level]
	if !ok {
		col = levelColors[script.LogInfo]
	}

	// Timestamp in seconds since start: an absolute clock is no use for reading
	// what happened in the last two seconds, which is what this window is for.
	editorTextMuted(fmt.Sprintf("%7.2f", float64(e.Time)/1000.0))
	imgui.SameLine()

	if e.Source != "" {
		editorTextMuted(logShortSource(e.Source))
		imgui.SameLine()
	}

	if e.HasPos() {
		// A positioned error is clickable: the message and the line it refers to
		// belong together, and making someone go and find it is half a message.
		imgui.PushID(fmt.Sprintf("logentry%d", e.Seq))
		imgui.PushStyleColor(imgui.StyleColorText, col)
		if imgui.Button(fmt.Sprintf("%s:%d", logShortSource(e.Pos.File), e.Pos.Line)) {
			logWindowOpenAt(e.Pos)
		}
		imgui.PopStyleColor()
		imgui.PopID()
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Open this script in the script editor")
		}
		imgui.SameLine()
	}

	editorTextColored(col, e.Text)
}

// logShortSource trims a script path down to what is worth reading in a column
// that repeats it on every line.
func logShortSource(path string) string {
	s := strings.TrimPrefix(path, fileutil.ScriptsDirName+"/")
	if i := strings.LastIndex(s, "/"); i >= 0 && len(s) > 24 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(s, fileutil.ScriptExt)
}

// logWindowEntries returns what to draw, honouring pause.
//
// Pausing freezes the *snapshot*, not just the scrolling: a view that kept
// growing while claiming to be paused would be worse than no pause at all.
func logWindowEntries() []script.LogEntry {
	if logWindowPaused {
		if logWindowFrozen == nil {
			logWindowFrozen = script.ScriptLog.Snapshot()
		}
		return logWindowFrozen
	}
	logWindowFrozen = nil
	return script.ScriptLog.Snapshot()
}

// logEntryVisible applies the level, source and text filters.
func logEntryVisible(e script.LogEntry) bool {
	switch e.Level {
	case script.LogInfo:
		if !logWindowShowInfo {
			return false
		}
	case script.LogWarn:
		if !logWindowShowWarn {
			return false
		}
	case script.LogError:
		if !logWindowShowError {
			return false
		}
	}
	if e.Source == "" && !logWindowShowEngine {
		return false
	}
	if logWindowFilter == "" {
		return true
	}
	// Matched against the source as well as the text, so filtering by a script
	// name shows everything that script said rather than only the lines that
	// happen to repeat its name.
	needle := strings.ToLower(logWindowFilter)
	return strings.Contains(strings.ToLower(e.Text), needle) ||
		strings.Contains(strings.ToLower(e.Source), needle)
}

// logEmptyMessage explains an empty list, distinguishing "nothing logged" from
// "everything filtered out" — which look identical and have opposite fixes.
func logEmptyMessage(total int) string {
	if total == 0 {
		return "Nothing logged yet. Press ` to play; a script's log() lands here."
	}
	return fmt.Sprintf("All %d entries are hidden by the filters above.", total)
}

// logWindowOpenAt opens a script in the script editor.
//
// The line is reported rather than scrolled to: the buffer is a plain
// InputTextMultiline, which has no cursor placement of its own, and pretending
// otherwise would be a worse lie than saying where to look.
func logWindowOpenAt(pos flang.Pos) {
	name := strings.TrimPrefix(pos.File, fileutil.ScriptsDir()+"/")
	name = strings.TrimPrefix(name, fileutil.ScriptsDirName+"/")
	line := pos.Line
	// Deferred for the same reason the script editor's Log button is: this runs
	// inside this window's Draw, and opening one from there would hand our
	// context away mid-frame.
	uiwin.RequestWindowOpen(func() {
		OpenScriptEditorWindow()
		scriptEditorOpenFile(name)
		scriptEditorStatus("From the log: line %d", line)
	})
}
