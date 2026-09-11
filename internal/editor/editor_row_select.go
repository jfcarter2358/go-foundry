package editor

// editor_row_select.go — selecting and copying rows in the read-only lists.
//
// The log window and the script editor's problems pane are both lists you
// eventually want to get text *out* of: an error into a search box, a run of
// log lines into a bug report. ImGui's Text is not selectable, so each row gets
// a full-width Selectable underneath its drawing and this file is the selection
// model the two lists share.
//
// Three things it has to get right:
//
//   - **The row keeps working.** A log row whose position is a clickable button
//     still has to take that click, so the selectable yields hit-testing to
//     whatever is painted over it.
//   - **Click, ctrl-click, shift-click.** A list four thousand lines long is not
//     worth selecting in one line at a time, so it does ranges — and a range
//     needs the whole frame's rows, which is why a click is recorded during the
//     row and applied at the end of the list.
//   - **What you copy is the whole line.** The timestamp and the source are
//     drawn muted because they repeat down the column, but they are exactly
//     what a pasted line needs in order to still mean something somewhere else.

import (
	"fmt"
	"strings"

	"foundry/internal/imguibackend"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// rowSelection is one list's selection.
//
// Keyed by a caller-supplied string rather than by index because both lists
// renumber underneath the selection: the log ring evicts from the front as it
// fills, and the problems pane is rebuilt from scratch on every reparse.
type rowSelection struct {
	sel    map[string]bool
	anchor string

	// keys is the rows drawn in the last completed frame, in draw order, and
	// text is what each of them copies as. A copy therefore comes out in the
	// order it was read rather than in map order, and reading them between
	// frames — which is when a keyboard shortcut arrives — sees the list as it
	// was last drawn.
	keys []string
	text map[string]string

	// pending is a click waiting for the rest of the list. A shift-click needs
	// the rows *below* the one clicked, and those have not been drawn yet at
	// the moment the click is reported.
	pending *rowClick
}

// rowClick is one click on a row, with the modifiers that decide what it means.
type rowClick struct {
	key   string
	ctrl  bool
	shift bool
}

// begin starts a frame's worth of rows. Call it inside the list, before the
// first row.
func (s *rowSelection) begin() {
	if s.sel == nil {
		s.sel = make(map[string]bool)
	}
	if s.text == nil {
		s.text = make(map[string]string)
	}
	s.keys = s.keys[:0]
	clear(s.text)
}

// row draws one row: a full-width Selectable of the given height, with draw
// painted over the top of it.
//
// height has to cover everything draw paints: the next row starts where the
// selectable ends, so anything draw puts below that is drawn over.
//
// copyText is what this row contributes to the clipboard, which is deliberately
// not the same string as the one on screen — see the file comment.
func (s *rowSelection) row(key, copyText string, height float32, draw func()) {
	s.keys = append(s.keys, key)
	s.text[key] = copyText

	before := imgui.CursorPos()
	if imgui.SelectableV("##rowsel_"+key, s.sel[key],
		imgui.SelectableFlagsAllowItemOverlap, imgui.Vec2{X: 0, Y: height}) {
		io := imgui.CurrentIO()
		s.pending = &rowClick{key: key, ctrl: io.KeyCtrlPressed(), shift: io.KeyShiftPressed()}
	}
	// Hands hit-testing on to whatever draw paints on top, so a row's own
	// button still takes its click instead of being swallowed by the row.
	imgui.SetItemAllowOverlap()
	// A right-click selects the row it lands on unless that row is already part
	// of the selection — the rule every file manager uses, and the one that
	// makes "Copy" mean what the highlight says it means.
	if imgui.IsItemHovered() && imgui.IsMouseClicked(1) && !s.sel[key] {
		s.pending = &rowClick{key: key}
	}
	after := imgui.CursorPos()

	imgui.SetCursorPos(before)
	draw()
	// Back to where the *selectable* left the cursor rather than where draw did.
	// The height was measured for the selectable, and letting the two disagree
	// is how rows start overlapping each other.
	imgui.SetCursorPos(after)
}

// end closes a frame's rows, applying whatever click the frame collected.
func (s *rowSelection) end() {
	if s.pending != nil {
		s.apply(*s.pending)
		s.pending = nil
	}
	// A row that is no longer listed cannot be copied, so it is no longer
	// selected: the ring evicts, the filters hide, and a selection outliving
	// what it pointed at would put text on the clipboard that nobody can see.
	for k := range s.sel {
		if _, ok := s.text[k]; !ok {
			delete(s.sel, k)
		}
	}
	if _, ok := s.text[s.anchor]; !ok {
		s.anchor = ""
	}
}

// apply resolves one click against the complete row list: plain replaces the
// selection, ctrl toggles one row, shift extends from the anchor.
//
// Shift leaves the anchor where it is on purpose. That is what makes a second
// shift-click re-cut the range from the same end rather than from wherever the
// last one happened to stop.
func (s *rowSelection) apply(c rowClick) {
	switch {
	case c.shift && s.anchor != "":
		lo, hi := rowIndexOf(s.keys, s.anchor), rowIndexOf(s.keys, c.key)
		if lo < 0 || hi < 0 {
			break
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		if !c.ctrl {
			clear(s.sel)
		}
		for _, k := range s.keys[lo : hi+1] {
			s.sel[k] = true
		}
	case c.ctrl:
		if s.sel[c.key] {
			delete(s.sel, c.key)
		} else {
			s.sel[c.key] = true
		}
		s.anchor = c.key
	default:
		clear(s.sel)
		s.sel[c.key] = true
		s.anchor = c.key
	}
}

// rowIndexOf is the position of key in keys, or -1.
func rowIndexOf(keys []string, key string) int {
	for i, k := range keys {
		if k == key {
			return i
		}
	}
	return -1
}

// count is how many rows are selected.
func (s *rowSelection) count() int { return len(s.sel) }

// selectAll selects every listed row; clearSel selects none.
func (s *rowSelection) selectAll() {
	if s.sel == nil {
		s.sel = make(map[string]bool)
	}
	for _, k := range s.keys {
		s.sel[k] = true
	}
}

func (s *rowSelection) clearSel() {
	clear(s.sel)
	s.anchor = ""
}

// selectedText is the selected rows in list order, one per line, and allText is
// every listed row the same way. Both end without a trailing newline: a pasted
// single line should not arrive as a line plus a blank one.
func (s *rowSelection) selectedText() string {
	return s.join(func(k string) bool { return s.sel[k] })
}

func (s *rowSelection) allText() string {
	return s.join(func(string) bool { return true })
}

func (s *rowSelection) join(want func(string) bool) string {
	var b strings.Builder
	for _, k := range s.keys {
		if !want(k) {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s.text[k])
	}
	return b.String()
}

// copySelected and copyAll put text on the system clipboard and report how many
// rows went, so the caller can say so — a clipboard write is invisible, and an
// action with no feedback reads as an action that did not happen.
func (s *rowSelection) copySelected() int {
	text := s.selectedText()
	if text == "" {
		return 0
	}
	imguibackend.SdlClipboard{}.SetText(text)
	return s.count()
}

func (s *rowSelection) copyAll() int {
	text := s.allText()
	if text == "" {
		return 0
	}
	imguibackend.SdlClipboard{}.SetText(text)
	return len(s.keys)
}

// rowSelectionMenuItems is the copy menu both lists raise on a right-click.
// done reports how many rows were copied, for the caller's status line.
func (s *rowSelection) menuItems(done func(n int)) []ctxMenuItem {
	n := s.count()
	copyLabel := "Copy"
	if n > 0 {
		copyLabel = fmt.Sprintf("Copy %s", plural(n, "line"))
	}
	return []ctxMenuItem{
		{Label: copyLabel, Shortcut: "Ctrl+C", Disabled: n == 0,
			Do: func() { done(s.copySelected()) }},
		{Label: fmt.Sprintf("Copy all %s", plural(len(s.keys), "line")),
			Disabled: len(s.keys) == 0, Do: func() { done(s.copyAll()) }},
		{Separator: true},
		{Label: "Select all", Shortcut: "Ctrl+A", Disabled: len(s.keys) == 0,
			Do: s.selectAll},
		{Label: "Clear selection", Disabled: n == 0, Do: s.clearSel},
	}
}

// rowLineHeight and rowFrameHeight are the two row heights the lists need: one
// line of text, and one line of text inside a button's padding. The selectable
// has to be told its height before the row is drawn, so it cannot be measured
// from the row itself.
func rowLineHeight() float32 { return imgui.TextLineHeight() }

func rowFrameHeight() float32 {
	return imgui.TextLineHeight() + 2*imgui.CurrentStyle().FramePadding().Y
}

// plural renders a count with its noun: "1 line", "3 lines".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
