package editor

// editor_tracker_order.go — the left pane: the order list, the sections laid
// over it, and the pattern under the cursor.
//
// The order list is the song's running order — which pattern plays at each
// position — so it is drawn as a vertical strip with each entry tinted by the
// section that contains it and the sections as brackets beside the strip.
// That is how a game song is read: "intro, then the loop, which repeats
// until combat ends".
//
// The order operations are trackerState methods (below the pane) so the test
// can check the one thing that is easy to get wrong: sections index the order
// list, and inserting or deleting an entry has to move the sections after it.

import (
	"fmt"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// trkSelSection is the section open in the section editor, or -1.
var trkSelSection = -1

// trkSectionAt is the section containing order entry i, or -1.
func trkSectionAt(i int) int {
	for si, sec := range trkEd.Song.Sections {
		if i >= sec.Start && i < sec.End {
			return si
		}
	}
	return -1
}

func trkOrderPane() {
	w := trackerEditorWindow
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: trkToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: trkLeftW, Y: float32(w.H) - trkToolbarH - trkBottomH})
	if imgui.BeginV("##trkorder", nil, trkPaneFlags) {
		trkPaneClickTakesFocus()
		trkOrderList()
		imgui.Separator()
		trkPatternProps()
		imgui.Separator()
		trkSectionEditor()
		pos, size := imgui.WindowPos(), imgui.WindowSize()
		trkSplitter("##trksplitleft", imgui.Vec2{X: pos.X + size.X - 4, Y: pos.Y}, imgui.Vec2{X: pos.X + size.X, Y: pos.Y + size.Y}, true, &trkLeftW, 1)
	}
	imgui.End()
}

// trkOrderList is the strip itself, with its buttons.
func trkOrderList() {
	s := trkEd.Song
	imgui.Text("Order")
	imgui.SameLine()
	editorTextMuted(fmt.Sprintf("%d entries, %.0fs", len(s.Order), s.Duration()))

	btn := func(label, tip string, enabled bool, fn func()) {
		imguibackend.EditorPushDisabled(!enabled)
		if trkSmallButton(label) {
			fn()
		}
		imguibackend.EditorPopDisabled()
		if imgui.IsItemHoveredV(imgui.HoveredFlagsAllowWhenDisabled) {
			imgui.SetTooltip(tip)
		}
		imgui.SameLine()
	}
	o := trkEd.Cur.Order
	btn("+##trkordnew", "Insert a new empty pattern after this entry", len(s.Patterns) < tracker.MaxPatterns, trkEd.insertNewPattern)
	btn("Rep##trkordrep", "Play this pattern again after this entry", true, trkEd.repeatOrder)
	btn("Dup##trkorddup", "Insert a copy of this pattern after this entry", len(s.Patterns) < tracker.MaxPatterns, trkEd.duplicateOrder)
	btn("Uniq##trkorduniq", "Give this entry its own copy of the pattern, so editing it\nleaves the other entries that play it alone", trkEd.patternShared(o), trkEd.makeUnique)
	imgui.Dummy(imgui.Vec2{})
	btn("Up##trkordup", "Move this entry up", o > 0, func() { trkEd.moveOrder(-1) })
	btn("Down##trkorddown", "Move this entry down", o < len(s.Order)-1, func() { trkEd.moveOrder(1) })
	btn("Del##trkorddel", "Remove this entry (the pattern stays in the song)", len(s.Order) > 1, trkEd.deleteOrder)
	imgui.Dummy(imgui.Vec2{})

	listH := imgui.ContentRegionAvail().Y * 0.45
	imgui.BeginChildV("##trkorderlist", imgui.Vec2{X: 0, Y: max32(listH, 100)}, true, 0)
	dl := imgui.WindowDrawList()
	playOrder, _, playing := trkPlayPosition()
	lineH := imgui.TextLineHeightWithSpacing()
	for i, pi := range s.Order {
		sec := trkSectionAt(i)
		start := imgui.CursorScreenPos()
		width := imgui.ContentRegionAvail().X
		if sec >= 0 {
			// The section bracket at the left edge, and a tint behind the row.
			dl.AddRectFilled(start, imgui.Vec2{X: start.X + 3, Y: start.Y + lineH}, trkSectionColor(sec, 0.95))
			dl.AddRectFilled(imgui.Vec2{X: start.X + 3, Y: start.Y}, imgui.Vec2{X: start.X + width, Y: start.Y + lineH - 1}, trkSectionColor(sec, 0.12))
			if s.Sections[sec].Start == i {
				dl.AddLine(imgui.Vec2{X: start.X, Y: start.Y}, imgui.Vec2{X: start.X + 10, Y: start.Y}, trkSectionColor(sec, 0.95))
			}
			if s.Sections[sec].End == i+1 {
				dl.AddLine(imgui.Vec2{X: start.X, Y: start.Y + lineH - 1}, imgui.Vec2{X: start.X + 10, Y: start.Y + lineH - 1}, trkSectionColor(sec, 0.95))
			}
		}
		name := ""
		if pi >= 0 && pi < len(s.Patterns) {
			name = s.Patterns[pi].Name
		}
		label := fmt.Sprintf("  %02X  %02X %s", i, pi, name)
		if playing && i == playOrder {
			label = "> " + label[2:]
		}
		imgui.SetCursorScreenPos(imgui.Vec2{X: start.X + 6, Y: start.Y})
		if imgui.SelectableV(label+"##trkord"+itoa(i), i == trkEd.Cur.Order, 0, imgui.Vec2{}) {
			trkGotoOrder(i)
		}
		if imgui.IsItemHovered() {
			tip := fmt.Sprintf("Pattern %02X, %d rows", pi, s.Patterns[pi].Rows())
			if sec >= 0 {
				tip += "\nSection " + s.Sections[sec].Name
			}
			imgui.SetTooltip(tip)
			if imgui.IsMouseDoubleClicked(0) {
				trkPlayFromOrder(i)
			}
		}
		if imgui.BeginPopupContextItemV("##trkordctx"+itoa(i), imgui.PopupFlagsMouseButtonRight) {
			trkGotoOrder(i)
			trkOrderContextMenu(i)
			imgui.EndPopup()
		}
	}
	imgui.EndChild()
}

// trkPlayFromOrder plays the song from an order entry's first row.
func trkPlayFromOrder(i int) {
	trkEnsurePlayer()
	trkPlayPattern = false
	trkSyncPlayer(true)
	trkPlayer.PlayFrom(i, 0)
}

func trkOrderContextMenu(i int) {
	s := trkEd.Song
	if imgui.MenuItem("Play from here") {
		trkPlayFromOrder(i)
	}
	imgui.Separator()
	if imgui.BeginMenu("Pattern") {
		for pi, p := range s.Patterns {
			if imgui.MenuItemV(fmt.Sprintf("%02X %s##trksetpat", pi, p.Name), "", s.Order[i] == pi, true) {
				trkEd.pushUndo("")
				s.Order[i] = pi
				trkEd.touch()
				trkEd.clampCursor()
			}
		}
		imgui.EndMenu()
	}
	if imgui.MenuItem("New section here") {
		trkEd.addSection(i)
		trkSelSection = len(s.Sections) - 1
	}
	if sec := trkSectionAt(i); sec >= 0 && imgui.MenuItem("Edit section "+s.Sections[sec].Name) {
		trkSelSection = sec
	}
}

// trkPatternProps edits the pattern under the cursor: name and length.
func trkPatternProps() {
	p := trkEd.pattern()
	if p == nil {
		return
	}
	imgui.Text(fmt.Sprintf("Pattern %02X", trkEd.patternIndex()))
	imgui.PushItemWidth(-1)
	name := p.Name
	if trkEdited(imgui.InputTextWithHintV("##trkpatname", "name", &name, 0, nil)) {
		p.Name = name
	}
	rows := int32(p.Rows())
	if imgui.InputIntV("##trkpatrows", &rows, 16, 16, imgui.InputTextFlagsEnterReturnsTrue) {
		n := clampI(int(rows), 1, tracker.MaxRows)
		if n != p.Rows() {
			trkEd.pushUndo("")
			p.Resize(n, len(trkEd.Song.Channels))
			trkEd.touch()
			trkEd.clampCursor()
		}
	}
	editorMenuTooltip("Rows in this pattern (Enter to apply)")
	imgui.PopItemWidth()
}

// trkSectionEditor lists sections and edits the selected one.
func trkSectionEditor() {
	s := trkEd.Song
	imgui.Text("Sections")
	imgui.SameLine()
	if trkSmallButton("+##trksecadd") {
		trkEd.addSection(trkEd.Cur.Order)
		trkSelSection = len(s.Sections) - 1
	}
	editorMenuTooltip("A new section starting at the cursor's order entry")
	for i, sec := range s.Sections {
		imgui.PushStyleColor(imgui.StyleColorText, trkSectionColors[i%len(trkSectionColors)])
		imgui.Bullet()
		imgui.PopStyleColor()
		imgui.SameLine()
		label := fmt.Sprintf("%s  %02X-%02X", sec.Name, sec.Start, sec.End-1)
		if sec.Loop {
			label += "  loop"
		} else if sec.Next != "" {
			label += "  > " + sec.Next
		}
		if imgui.SelectableV(label+"##trksec"+itoa(i), i == trkSelSection, 0, imgui.Vec2{}) {
			trkSelSection = i
		}
		if imgui.IsItemHovered() && imgui.IsMouseDoubleClicked(0) {
			trkGotoOrder(sec.Start)
		}
	}
	if trkSelSection < 0 || trkSelSection >= len(s.Sections) {
		return
	}
	sec := &s.Sections[trkSelSection]
	imgui.Spacing()
	imgui.PushItemWidth(-60)
	name := sec.Name
	if imgui.InputTextV("Name##trksecname", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) && strings.TrimSpace(name) != "" {
		trkEd.renameSection(trkSelSection, strings.TrimSpace(name))
	}
	start, end := int32(sec.Start), int32(sec.End-1)
	if trkEdited(imgui.SliderIntV("Start##trksecstart", &start, 0, int32(len(s.Order)-1), "%02X", 0)) {
		sec.Start = int(start)
		if sec.End <= sec.Start {
			sec.End = sec.Start + 1
		}
	}
	if trkEdited(imgui.SliderIntV("End##trksecend", &end, 0, int32(len(s.Order)-1), "%02X", 0)) {
		sec.End = int(end) + 1
		if sec.Start >= sec.End {
			sec.Start = sec.End - 1
		}
	}
	loop := sec.Loop
	if trkEdited(imgui.Checkbox("Loop##trksecloop", &loop)) {
		sec.Loop = loop
	}
	editorMenuTooltip("Repeat until a script asks for another section")
	if !sec.Loop {
		preview := sec.Next
		if preview == "" {
			preview = "(carry on)"
		}
		if imguibackend.BeginCombo("Next##trksecnext", preview) {
			if imguibackend.SelectableV("(carry on)##trksecnone", sec.Next == "", 0, imgui.Vec2{}) {
				trkEd.pushUndo("")
				sec.Next = ""
				trkEd.touch()
			}
			for _, other := range s.Sections {
				if imguibackend.SelectableV(other.Name+"##trksecnext", sec.Next == other.Name, 0, imgui.Vec2{}) {
					trkEd.pushUndo("")
					sec.Next = other.Name
					trkEd.touch()
				}
			}
			imguibackend.EndCombo()
		}
	}
	imgui.PopItemWidth()
	if trkSmallButton("Play##trksecplay") {
		trkPlayFromOrder(sec.Start)
	}
	imgui.SameLine()
	if trkSmallButton("Delete##trksecdel") {
		trkEd.deleteSection(trkSelSection)
		trkSelSection = -1
	}
}

// ---------- order operations ----------

// shiftSections moves section bounds for an order entry inserted (delta 1)
// or removed (delta -1) at position at.
func (t *trackerState) shiftSections(at, delta int) {
	for i := range t.Song.Sections {
		sec := &t.Song.Sections[i]
		switch {
		case delta > 0 && sec.Start >= at:
			sec.Start++
			sec.End++
		case delta > 0 && sec.End > at:
			sec.End++ // inserted inside the section: it grows
		case delta < 0 && at < sec.Start:
			sec.Start--
			sec.End--
		case delta < 0 && at < sec.End:
			sec.End--
		}
	}
	n := len(t.Song.Order)
	for i := range t.Song.Sections {
		sec := &t.Song.Sections[i]
		sec.Start = clampI(sec.Start, 0, n-1)
		sec.End = clampI(sec.End, sec.Start+1, n)
	}
}

// insertOrderAt puts pattern pi into the order list after the cursor and
// moves the cursor onto it.
func (t *trackerState) insertOrderAt(pi int) {
	at := t.Cur.Order + 1
	o := t.Song.Order
	o = append(o, 0)
	copy(o[at+1:], o[at:])
	o[at] = pi
	t.Song.Order = o
	t.shiftSections(at, 1)
	t.Cur.Order = at
	t.Cur.Row = 0
	t.touch()
}

// insertNewPattern appends a new empty pattern the length of the current one
// and plays it after the cursor's entry.
func (t *trackerState) insertNewPattern() {
	rows := 64
	if p := t.pattern(); p != nil {
		rows = p.Rows()
	}
	t.pushUndo("")
	t.Song.Patterns = append(t.Song.Patterns, tracker.NewPattern("", rows, len(t.Song.Channels)))
	t.insertOrderAt(len(t.Song.Patterns) - 1)
}

// repeatOrder plays the cursor's pattern again after it.
func (t *trackerState) repeatOrder() {
	pi := t.patternIndex()
	if pi < 0 {
		return
	}
	t.pushUndo("")
	t.insertOrderAt(pi)
}

// duplicateOrder inserts a copy of the cursor's pattern after it.
func (t *trackerState) duplicateOrder() {
	p := t.pattern()
	if p == nil {
		return
	}
	t.pushUndo("")
	c := p.Clone()
	t.Song.Patterns = append(t.Song.Patterns, c)
	t.insertOrderAt(len(t.Song.Patterns) - 1)
}

// patternShared reports whether order entry i's pattern also plays elsewhere.
func (t *trackerState) patternShared(i int) bool {
	if i < 0 || i >= len(t.Song.Order) {
		return false
	}
	n := 0
	for _, pi := range t.Song.Order {
		if pi == t.Song.Order[i] {
			n++
		}
	}
	return n > 1
}

// makeUnique gives the cursor's entry its own copy of its pattern.
func (t *trackerState) makeUnique() {
	p := t.pattern()
	if p == nil || len(t.Song.Patterns) >= tracker.MaxPatterns {
		return
	}
	t.pushUndo("")
	t.Song.Patterns = append(t.Song.Patterns, p.Clone())
	t.Song.Order[t.Cur.Order] = len(t.Song.Patterns) - 1
	t.touch()
}

// deleteOrder removes the cursor's entry. The pattern stays: another entry
// may play it, and an unplayed pattern is a scratch pad, not garbage.
func (t *trackerState) deleteOrder() {
	if len(t.Song.Order) <= 1 {
		return
	}
	t.pushUndo("")
	at := t.Cur.Order
	t.Song.Order = append(t.Song.Order[:at], t.Song.Order[at+1:]...)
	t.shiftSections(at, -1)
	t.clampCursor()
	t.touch()
}

// moveOrder swaps the cursor's entry with its neighbour d away.
func (t *trackerState) moveOrder(d int) {
	a := t.Cur.Order
	b := a + d
	if b < 0 || b >= len(t.Song.Order) {
		return
	}
	t.pushUndo("")
	t.Song.Order[a], t.Song.Order[b] = t.Song.Order[b], t.Song.Order[a]
	t.Cur.Order = b
	t.touch()
}

// addSection adds a one-entry section at order entry at, with a free name.
func (t *trackerState) addSection(at int) {
	t.pushUndo("")
	name := "section"
	for n := 1; t.Song.SectionIndex(name) >= 0; n++ {
		name = fmt.Sprintf("section%d", n+1)
	}
	at = clampI(at, 0, len(t.Song.Order)-1)
	t.Song.Sections = append(t.Song.Sections, tracker.Section{Name: name, Start: at, End: at + 1})
	t.touch()
}

// renameSection renames a section and every Next that named it.
func (t *trackerState) renameSection(i int, name string) {
	if t.Song.SectionIndex(name) >= 0 {
		t.Status = "A section is already called " + name
		return
	}
	t.pushUndo("")
	old := t.Song.Sections[i].Name
	t.Song.Sections[i].Name = name
	for j := range t.Song.Sections {
		if t.Song.Sections[j].Next == old {
			t.Song.Sections[j].Next = name
		}
	}
	t.touch()
}

// deleteSection removes a section and the Nexts that pointed at it.
func (t *trackerState) deleteSection(i int) {
	if i < 0 || i >= len(t.Song.Sections) {
		return
	}
	t.pushUndo("")
	old := t.Song.Sections[i].Name
	t.Song.Sections = append(t.Song.Sections[:i], t.Song.Sections[i+1:]...)
	for j := range t.Song.Sections {
		if t.Song.Sections[j].Next == old {
			t.Song.Sections[j].Next = ""
		}
	}
	t.touch()
}
