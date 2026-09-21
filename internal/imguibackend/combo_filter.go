package imguibackend

// combo_filter.go — type-to-filter dropdowns.
//
// Every dropdown in the program opens with a focused filter field at the top,
// so typing narrows the list straight away and Enter takes the first match.
// ImGui's own combo has no such thing, and the items inside one are plain
// Selectables the caller draws itself, so the filter cannot live in the combo
// alone: BeginCombo draws the field and holds the text, and the Selectable
// wrappers below skip whatever does not match it.
//
// Only one combo can be open at a time, so the state is a single package-level
// value, live between BeginCombo and EndCombo. Outside that window the
// Selectable wrappers are exactly imgui's, which is what makes it safe to use
// them for every Selectable in a file that has a combo in it.

import (
	"strings"

	"github.com/inkyblackness/imgui-go/v4"
)

type comboFilterState struct {
	active bool
	text   string
	// enter is set on the frame Enter was pressed in the field; the first
	// matching item that frame is taken as the pick.
	enter bool
	// matched counts the items drawn this frame, for the "no matches" line.
	matched int
}

var comboFilter comboFilterState

// BeginCombo is imgui.BeginCombo with a filter field at the top of the list.
// Pair it with EndCombo, and draw the items with Selectable/SelectableV.
func BeginCombo(label, preview string) bool {
	return BeginComboV(label, preview, 0)
}

// BeginComboV is BeginCombo with imgui's combo flags.
func BeginComboV(label, preview string, flags imgui.ComboFlags) bool {
	if !imgui.BeginComboV(label, preview, flags) {
		return false
	}
	if imgui.IsWindowAppearing() {
		comboFilter.text = ""
		imgui.SetKeyboardFocusHere()
	}
	imgui.SetNextItemWidth(-1)
	enter := imgui.InputTextWithHintV("##combofilter", "Type to filter...", &comboFilter.text,
		imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil)
	comboFilter.active = true
	comboFilter.enter = enter
	comboFilter.matched = 0
	return true
}

// EndCombo closes a combo opened with BeginCombo.
func EndCombo() {
	if comboFilter.active && comboFilter.matched == 0 && strings.TrimSpace(comboFilter.text) != "" {
		imgui.PushStyleColor(imgui.StyleColorText, imgui.CurrentStyle().Color(imgui.StyleColorTextDisabled))
		imgui.Text("No matches")
		imgui.PopStyleColor()
	}
	comboFilter.active = false
	comboFilter.enter = false
	imgui.EndCombo()
}

// Selectable is imgui.Selectable, hidden when an open combo's filter rules it
// out.
func Selectable(label string) bool {
	return SelectableV(label, false, 0, imgui.Vec2{})
}

// SelectableV is imgui.SelectableV, hidden when an open combo's filter rules
// it out. Pressing Enter in the filter picks the first item still shown.
func SelectableV(label string, selected bool, flags imgui.SelectableFlags, size imgui.Vec2) bool {
	if !comboFilter.active {
		return imgui.SelectableV(label, selected, flags, size)
	}
	if !ComboFilterMatches(comboFilter.text, label) {
		return false
	}
	comboFilter.matched++
	if comboFilter.enter && comboFilter.matched == 1 {
		imgui.SelectableV(label, selected, flags, size)
		imgui.CloseCurrentPopup()
		return true
	}
	return imgui.SelectableV(label, selected, flags, size)
}

// Combo is imgui.Combo drawn through the filtering combo.
func Combo(label string, value *int32, list []string) bool {
	preview := ""
	if *value >= 0 && int(*value) < len(list) {
		preview = list[*value]
	}
	changed := false
	if BeginCombo(label, preview) {
		for i, item := range list {
			if SelectableV(item, int32(i) == *value, 0, imgui.Vec2{}) && int32(i) != *value {
				*value = int32(i)
				changed = true
			}
		}
		EndCombo()
	}
	return changed
}

// ComboFilterMatches reports whether an item label passes a filter: every
// whitespace-separated word of the filter must appear in the label's visible
// text, case-insensitively. The "##id" tail of a label is not visible, so it
// does not count.
func ComboFilterMatches(filter, label string) bool {
	if i := strings.Index(label, "##"); i >= 0 {
		label = label[:i]
	}
	label = strings.ToLower(label)
	for _, word := range strings.Fields(strings.ToLower(filter)) {
		if !strings.Contains(label, word) {
			return false
		}
	}
	return true
}
