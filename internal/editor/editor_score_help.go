package editor

// editor_score_help.go — the Score window's F1 overlay (the active keymap,
// rebindings included, over the score) and the Keyboard Shortcuts dialog
// that picks the keymap and rebinds commands.
//
// F1 is the editor-wide reference key (uiwin/keybind_help.go); the Score
// window draws its own overlay through UIWindow.HelpOverlay because its keys
// are not fixed — they are whichever keymap is chosen, with whatever the
// player has rebound — so the reference has to be read from the keymap, not
// written down. Every row comes from scoreCmdInfos and the active keymap.

import (
	"fmt"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreHelpOverlay is F1 in the Score window.
func scoreHelpOverlay(arriving bool) {
	w := scoreEditorWindow
	if w == nil {
		return
	}
	W, H := float32(w.W), float32(w.H)
	imgui.SetNextWindowPosV(imgui.Vec2{X: W / 2, Y: H / 2}, imgui.ConditionAlways, imgui.Vec2{X: 0.5, Y: 0.5})
	imgui.SetNextWindowSizeV(imgui.Vec2{X: min(W*0.92, 1180), Y: H * 0.86}, imgui.ConditionAlways)
	imgui.SetNextWindowBgAlpha(0.95)
	if arriving {
		imgui.SetNextWindowFocus()
	}
	open := true
	flags := imgui.WindowFlagsNoResize | imgui.WindowFlagsNoMove | imgui.WindowFlagsNoCollapse |
		imgui.WindowFlagsNoSavedSettings | imgui.WindowFlagsNoFocusOnAppearing
	if imgui.BeginV("Score keymap (F1)##scorehelp", &open, flags) {
		scoreHelpBody()
	}
	imgui.End()
	if !open {
		uiwin.KeybindHelpSetVisible(false)
	}
}

// scoreHelpRow is one line of the overlay: a group's heading (cmd scNone)
// or a bound command.
type scoreHelpRow struct {
	group string
	cmd   scoreCmd
}

// scoreHelpColumns lays the keymap's bound commands out in n columns of about
// the same height, in group order: a group too long for what is left of a
// column carries on at the top of the next under its heading again.
func scoreHelpColumns(k *scoreKeymap, n int) [][]scoreHelpRow {
	var all []scoreHelpRow
	for _, g := range scoreCmdGroups {
		for _, c := range scoreGroupCmds(g) {
			if k.label(c) != "" {
				all = append(all, scoreHelpRow{g, c})
			}
		}
	}
	// Each group's heading takes two rows' room (title and gap).
	groups := 0
	for i, r := range all {
		if i == 0 || all[i-1].group != r.group {
			groups++
		}
	}
	per := (len(all) + 2*groups + n - 1) / n
	cols := make([][]scoreHelpRow, 1, n)
	used := 0
	for i, r := range all {
		if used >= per && len(cols) < n {
			cols, used = append(cols, nil), 0
		}
		cur := &cols[len(cols)-1]
		if len(*cur) == 0 || (i > 0 && all[i-1].group != r.group) {
			*cur = append(*cur, scoreHelpRow{group: r.group})
			used += 2
		}
		*cur = append(*cur, r)
		used++
	}
	return cols
}

// scoreHelpBody is the overlay's content: every bound command with its keys,
// by group, in three columns side by side (groups, not ImGui columns: a
// separator inside legacy columns moves every column's line start).
func scoreHelpBody() {
	k := scoreKeymapActive()
	n := len(k.overrides(scorePreset(k.Name)))
	head := "Keymap: " + k.Name
	if n > 0 {
		head += fmt.Sprintf(" (%d command(s) rebound)", n)
	}
	imgui.Text(head)
	imgui.SameLine()
	if imgui.Button("Keyboard Shortcuts...##schelpkeys") {
		uiwin.KeybindHelpSetVisible(false)
		scoreShowKeys = true
	}
	editorTextMuted("Mouse: click a line or space in note input to write the note the key implies; click a note to select it;\n" +
		"click empty space in a bar to select it (Ctrl+click: every line, Shift+click: extend); Alt+click pastes there;\n" +
		"Ctrl+wheel zooms. Esc or F1 closes this.")
	imgui.Separator()
	imgui.BeginChildV("##schelpbody", imgui.Vec2{}, false, 0)
	const ncol = 3
	gap := float32(18)
	colW := (imgui.ContentRegionAvail().X - gap*(ncol-1)) / ncol
	for i, col := range scoreHelpColumns(k, ncol) {
		if i > 0 {
			imgui.SameLineV(0, gap)
		}
		x0 := imgui.CursorPosX()
		// The keys take what the widest needs here, up to half the column.
		keyW := float32(0)
		for _, r := range col {
			if r.cmd != scNone {
				keyW = max(keyW, imgui.CalcTextSize(k.label(r.cmd), false, 0).X)
			}
		}
		keyW = min(keyW+12, colW/2)
		imgui.BeginGroup()
		imgui.PushTextWrapPosV(x0 + colW)
		for j, r := range col {
			if r.cmd == scNone {
				if j > 0 {
					imgui.Spacing()
				}
				imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 0.45, Y: 0.80, Z: 1, W: 1})
				imgui.Text(r.group)
				imgui.PopStyleColor()
				continue
			}
			imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 1, Y: 0.85, Z: 0.45, W: 1})
			imgui.PushTextWrapPosV(x0 + keyW - 6)
			imgui.Text(k.label(r.cmd))
			imgui.PopTextWrapPos()
			imgui.PopStyleColor()
			imgui.SameLineV(keyW, 0)
			imgui.Text(scoreCmdInfos[r.cmd].Label)
		}
		imgui.PopTextWrapPos()
		imgui.Dummy(imgui.Vec2{X: colW, Y: 1})
		imgui.EndGroup()
	}
	imgui.EndChild()
}

// scoreGroupCmds are a group's commands in order.
func scoreGroupCmds(group string) []scoreCmd {
	var out []scoreCmd
	for c := scoreCmd(1); c < scCmdCount; c++ {
		if scoreCmdInfos[c].Group == group {
			out = append(out, c)
		}
	}
	return out
}

// scoreKeysFilter narrows the shortcuts list.
var scoreKeysFilter string

// scoreKeysDialog is Edit > Keyboard Shortcuts: the preset, and every
// command with its chords and buttons to add, replace or clear them.
func scoreKeysDialog() {
	imguibackend.WithModalPopup("Keyboard Shortcuts##sckeys", &scoreShowKeys, func() {
		k := scoreKeymapActive()
		imgui.SetNextItemWidth(180)
		if imguibackend.BeginCombo("Keymap##sckm", k.Name) {
			for _, p := range scoreKeymapPresets {
				if imguibackend.SelectableV(p.Name+"##sckmp", p.Name == k.Name, 0, imgui.Vec2{}) && p.Name != k.Name {
					scoreSetKeymap(p.Name)
					scEd.Status = "Keymap: " + p.Name
				}
			}
			imguibackend.EndCombo()
		}
		editorMenuTooltip("Sibelius is the default. Switching drops your rebindings.")
		imgui.SameLine()
		if imgui.Button("Reset to " + k.Name + "##sckreset") {
			scoreSetKeymap(k.Name)
			scEd.Status = "Keymap reset to " + k.Name
		}
		imgui.SetNextItemWidth(260)
		imgui.InputTextWithHint("##sckfilter", "Filter commands or keys", &scoreKeysFilter)
		if scoreCapture.active {
			imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 1, Y: 0.85, Z: 0.45, W: 1})
			imgui.Text("Press the keys for \"" + scoreCmdInfos[scoreCapture.cmd].Label + "\" (Esc cancels)")
			imgui.PopStyleColor()
		} else {
			editorTextMuted("Set replaces a command's keys, Add gives it another. A key in use moves to the new command.")
		}
		k = scoreKeymapActive()
		imgui.BeginChildV("##sckeyslist", imgui.Vec2{X: 720, Y: 440}, true, 0)
		imgui.ColumnsV(3, "##sckeycols", false)
		imgui.SetColumnWidth(0, 320)
		imgui.SetColumnWidth(1, 250)
		f := strings.ToLower(strings.TrimSpace(scoreKeysFilter))
		for _, g := range scoreCmdGroups {
			first := true
			for _, c := range scoreGroupCmds(g) {
				info := scoreCmdInfos[c]
				keys := k.label(c)
				if f != "" && !strings.Contains(strings.ToLower(info.Label), f) && !strings.Contains(strings.ToLower(keys), f) {
					continue
				}
				if first {
					imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 0.45, Y: 0.80, Z: 1, W: 1})
					imgui.Text(g)
					imgui.PopStyleColor()
					imgui.NextColumn()
					imgui.NextColumn()
					imgui.NextColumn()
					first = false
				}
				imgui.Text(info.Label)
				imgui.NextColumn()
				if keys == "" {
					editorTextMuted("(none)")
				} else {
					imgui.Text(keys)
				}
				imgui.NextColumn()
				capturing := scoreCapture.active && scoreCapture.cmd == c
				if editorActiveToggleButtonColor(fmt.Sprintf("Set##scks%d", c), capturing && !scoreCapture.add, scoreColActive) {
					scoreCapture = scoreCaptureState{active: true, cmd: c}
				}
				imgui.SameLine()
				if editorActiveToggleButtonColor(fmt.Sprintf("Add##scka%d", c), capturing && scoreCapture.add, scoreColActive) {
					scoreCapture = scoreCaptureState{active: true, cmd: c, add: true}
				}
				imgui.SameLine()
				if imgui.Button(fmt.Sprintf("Clear##sckc%d", c)) {
					k.rebind(c, nil)
					scoreSaveBindings()
				}
				imgui.NextColumn()
			}
		}
		imgui.Columns()
		imgui.EndChild()
		if cs := k.conflicts(); len(cs) > 0 {
			imgui.PushStyleColor(imgui.StyleColorText, saveChipUnsaved)
			imgui.Text(fmt.Sprintf("%d key(s) are bound twice; the first command listed wins.", len(cs)))
			imgui.PopStyleColor()
		}
		if imgui.Button("Close##sckclose") {
			scoreCapture = scoreCaptureState{}
			imgui.CloseCurrentPopup()
		}
	})
}
