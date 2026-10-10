package editor

// editor_claude_input.go — soft-wrapping the panel's input box.
//
// ImGui's multiline input does not wrap. A long line scrolls sideways instead,
// so a paragraph slides out of the box a word at a time and only its tail can be
// read — which is exactly the wrong behaviour for the one widget in the editor
// that prose is typed into.
//
// So the breaks are put in by hand. They go in from a CallbackAlways callback,
// which is the only place the text may be changed while the box is being typed
// into: InputText keeps its own copy of the buffer while it is active and
// ignores the one it was handed, so a string rewritten between frames would be
// thrown away.
//
// The breaks are real newlines, which means they have to be taken out again
// before the message is sent, and the marker that says which newlines are ours
// is the space in front of them. A break is only ever inserted directly after an
// existing space, and claudeUnwrap turns every space-newline back into a space.
// What is sent is therefore what was typed, character for character, and the box
// is free to re-break the whole paragraph on every keystroke.
//
// The one thing that costs: a line break typed with Ctrl+Enter directly after a
// space cannot be told from one of ours, and is absorbed back into the
// paragraph. A break typed after a word — which is how anyone types one — stays.

import (
	"strings"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// The box's height in lines: three while it is empty, growing with the text so
// that a wrapped paragraph can be read without scrolling inside a three-line
// window. The maximum, and the fraction of the window in claudeInputHeight,
// keep a long paste from squeezing the transcript out altogether.
const (
	claudeInputMinLines = 3
	claudeInputMaxLines = 12
)

// claudeInputHeight is the height to draw the input box with this frame.
func claudeInputHeight() float32 {
	lines := strings.Count(claudeState.input, "\n") + 1
	if lines < claudeInputMinLines {
		lines = claudeInputMinLines
	}
	if lines > claudeInputMaxLines {
		lines = claudeInputMaxLines
	}
	st := imgui.CurrentStyle()
	h := float32(lines)*imgui.TextLineHeight() + st.FramePadding().Y*2
	floor := float32(claudeInputMinLines)*imgui.TextLineHeight() + st.FramePadding().Y*2
	if limit := imgui.WindowHeight() * 0.45; h > limit {
		h = limit
	}
	if h < floor {
		h = floor
	}
	return h
}

// claudeInputWrapWidth is how wide the text inside the box may be, given the
// space the box itself is about to be given.
//
// The vertical scrollbar's width comes off whether or not it is showing. It
// appears as soon as the text is taller than the box, and wrapping that got
// wider the moment it appeared would break, unbreak and break again.
func claudeInputWrapWidth(boxW float32) float32 {
	st := imgui.CurrentStyle()
	return boxW - st.FramePadding().X*2 - st.ScrollbarSize() - 4
}

// claudeDrawInputBox draws the box, and reports whether Enter was pressed in it.
//
// CallbackAlways is what lets claudeWrapInput keep the text wrapped while it is
// being typed. The width to wrap to can only be worked out here, where the
// layout is known, so it is left on the panel's state for the callback to read.
func claudeDrawInputBox() bool {
	flags := imgui.InputTextFlagsEnterReturnsTrue | imgui.InputTextFlagsCtrlEnterForNewLine |
		imgui.InputTextFlagsCallbackAlways
	claudeState.wrapPx = claudeInputWrapWidth(imgui.ContentRegionAvail().X)
	imgui.PushItemWidth(-1)
	send := imgui.InputTextMultilineV("##claudeinput", &claudeState.input,
		imgui.Vec2{X: -1, Y: claudeInputHeight()}, flags, claudeWrapInput)
	imgui.PopItemWidth()
	return send
}

// claudeWrapInput is the input box's callback: it keeps the buffer wrapped to
// claudeState.wrapPx as the text changes under it.
func claudeWrapInput(data imgui.InputTextCallbackData) int32 {
	if data.EventFlag() != imgui.InputTextFlagsCallbackAlways {
		return 0
	}
	buf := string(data.Buffer())
	// Nothing has been typed since the last pass, so nothing needs measuring.
	// Without this the wrap runs every frame the box has focus, and a selection
	// made with the mouse would be cleared by the edit below.
	if buf == claudeState.wrapped {
		return 0
	}
	want := claudeWrap(claudeUnwrap(buf), claudeState.wrapPx)
	claudeState.wrapped = want
	if want == buf {
		return 0
	}
	// Where the caret is in the text without the breaks is the thing an edit to
	// the breaks does not change.
	at := claudeRealIndex(buf, data.CursorPos())

	// The smallest edit that turns buf into want — usually one newline going in
	// or coming out. Replacing the whole buffer would also work, and would throw
	// away the widget's own undo history every keystroke.
	p := claudeCommonPrefix(buf, want)
	s := claudeCommonSuffix(buf[p:], want[p:])
	if n := len(buf) - p - s; n > 0 {
		data.DeleteBytes(p, n)
	}
	if ins := want[p : len(want)-s]; ins != "" {
		data.InsertBytes(p, []byte(ins))
	}
	data.MarkBufferModified()

	pos := claudeWrappedIndex(want, at)
	data.SetCursorPos(pos)
	data.SetSelectionStart(pos)
	data.SetSelectionEnd(pos)
	return 0
}

// claudeSoftBreak reports whether the byte at i in s is a break the box put in.
func claudeSoftBreak(s string, i int) bool {
	return i >= 0 && i < len(s) && s[i] == '\n' && i > 0 && s[i-1] == ' '
}

// claudeUnwrap is the text as it was typed: the soft breaks taken back out. The
// space each one was inserted after stays, so nothing is lost.
func claudeUnwrap(s string) string {
	if !strings.Contains(s, " \n") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if claudeSoftBreak(s, i) {
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// claudeWrap breaks s so that no line is wider than px in the font the box is
// drawn with.
//
// Breaks go after a space and nowhere else. A word wider than the whole box — a
// path, an id, a URL — is left to run off the edge rather than split, because
// half an identifier on each of two lines is harder to read than one that
// scrolls, and the break would not survive claudeUnwrap either.
func claudeWrap(s string, px float32) string {
	if px <= 0 || s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + len(s)/40 + 1)
	var lineW float32 // width of what is on the line so far
	spaceW := claudeTextWidth(" ")
	afterSpace := false
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '\n':
			b.WriteByte('\n')
			lineW, afterSpace = 0, false
			i++
		case c == ' ':
			b.WriteByte(' ')
			lineW += spaceW
			afterSpace = true
			i++
		default:
			j := i
			for j < len(s) && s[j] != ' ' && s[j] != '\n' {
				j++
			}
			word := s[i:j]
			w := claudeTextWidth(word)
			// The space this word follows stays at the end of the line it is
			// already on. That is what marks the break as the box's own.
			if afterSpace && lineW > 0 && lineW+w > px {
				b.WriteByte('\n')
				lineW = 0
			}
			b.WriteString(word)
			lineW += w
			afterSpace = false
			i = j
		}
	}
	return b.String()
}

// claudeTextWidth measures a run of text in the current font.
//
// Words are measured one at a time and the widths added up, rather than
// measuring the whole line again for every word: ImGui does not kern, so a line
// is exactly as wide as its parts, and the sum is the same answer for a fraction
// of the work.
func claudeTextWidth(s string) float32 {
	if s == "" {
		return 0
	}
	// Measured with hideTextAfterDoubleHash off: "##" is a label convention, and
	// in a message it is a heading.
	return imgui.CalcTextSize(s, false, 0).X
}

// claudeRealIndex is a byte offset into the wrapped text, as an offset into the
// same text without the soft breaks.
func claudeRealIndex(s string, i int) int {
	if i > len(s) {
		i = len(s)
	}
	if i < 0 {
		i = 0
	}
	n := 0
	for k := 0; k < i; k++ {
		if !claudeSoftBreak(s, k) {
			n++
		}
	}
	return n
}

// claudeWrappedIndex is the reverse of claudeRealIndex: where offset n of the
// unwrapped text has ended up in the wrapped one.
//
// A caret that lands on a break goes past it, onto the start of the next line,
// which is where the word being typed has just moved to.
func claudeWrappedIndex(s string, n int) int {
	i, count := 0, 0
	for i < len(s) && count < n {
		if !claudeSoftBreak(s, i) {
			count++
		}
		i++
	}
	for claudeSoftBreak(s, i) {
		i++
	}
	return i
}

// claudeCommonPrefix is how many leading bytes a and b share.
func claudeCommonPrefix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// claudeCommonSuffix is how many trailing bytes a and b share.
func claudeCommonSuffix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[len(a)-1-i] != b[len(b)-1-i] {
			return i
		}
	}
	return n
}
