package editor

// The input box's soft wrap. What matters is not where the breaks land but that
// they are reversible: the message sent is the text typed, or the box has
// silently rewritten what someone said.

import (
	"strings"
	"testing"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const claudeTestWrapPx = 200

// withClaudeTextFrame runs body inside a live imgui frame. Measuring text needs
// one: the current font is picked in NewFrame, and CalcTextSize without it walks
// off a null pointer inside imgui rather than returning zero.
func withClaudeTextFrame(body func()) {
	withImgui(func() {
		io := imgui.CurrentIO()
		io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 900})
		io.SetDeltaTime(1.0 / 60.0)
		imgui.NewFrame()
		// Sized: claudeInputHeight is a fraction of the window it is drawn in, and
		// an auto-sized window is a few pixels tall.
		imgui.SetNextWindowSize(imgui.Vec2{X: 1280, Y: 900})
		imgui.Begin("wrap")
		body()
		imgui.End()
		imgui.Render()
	})
}

// A long line comes back broken into lines that fit, and unwrapping gives the
// original back character for character.
func TestClaudeWrapIsReversible(t *testing.T) {
	withClaudeTextFrame(func() {
		text := "Make the hangar door open when the player walks into the trigger " +
			"volume in front of it, and close again five seconds after they leave."
		wrapped := claudeWrap(text, claudeTestWrapPx)
		if !strings.Contains(wrapped, "\n") {
			t.Fatalf("nothing was wrapped:\n%s", wrapped)
		}
		for _, line := range strings.Split(wrapped, "\n") {
			if w := claudeTextWidth(strings.TrimRight(line, " ")); w > claudeTestWrapPx {
				t.Errorf("line is %.0fpx wide, over %d: %q", w, claudeTestWrapPx, line)
			}
		}
		if got := claudeUnwrap(wrapped); got != text {
			t.Errorf("unwrap did not give the text back:\n got %q\nwant %q", got, text)
		}
	})
}

// Wrapping text that is already wrapped changes nothing. The callback runs the
// whole pass on every keystroke, so a pass that moved a break every time would
// fight the typist.
func TestClaudeWrapIsIdempotent(t *testing.T) {
	withClaudeTextFrame(func() {
		text := "The dropship waits in the descent clouds with the wind blowing " +
			"upwards, which is what makes the fall read as a fall."
		once := claudeWrap(text, claudeTestWrapPx)
		twice := claudeWrap(claudeUnwrap(once), claudeTestWrapPx)
		if once != twice {
			t.Errorf("second pass differs:\n%q\n%q", once, twice)
		}
	})
}

// A line break typed after a word survives the pass. This is the one the tooltip
// promises: Ctrl+Enter is for a break that means something.
func TestClaudeWrapKeepsTypedBreaks(t *testing.T) {
	withClaudeTextFrame(func() {
		text := "first line\nsecond line"
		if got := claudeWrap(text, claudeTestWrapPx); got != text {
			t.Errorf("a typed break was not left alone: %q", got)
		}
		if got := claudeUnwrap(text); got != text {
			t.Errorf("a typed break was unwrapped: %q", got)
		}
	})
}

// A word wider than the box is left whole. Breaking an id or a path in half
// would not survive unwrapping either, since there is no space to hang the break
// on.
func TestClaudeWrapLeavesLongWordsAlone(t *testing.T) {
	withClaudeTextFrame(func() {
		word := "resources/levels/level1_compound_north_gate_approach.yaml"
		wrapped := claudeWrap("see "+word, claudeTestWrapPx)
		if !strings.Contains(wrapped, word) {
			t.Errorf("the word was split: %q", wrapped)
		}
	})
}

// The caret's place in the text is what is kept across a re-wrap, not its byte
// offset: the mapping has to survive breaks being inserted in front of it.
func TestClaudeCaretSurvivesRewrap(t *testing.T) {
	// "a b" with a break after the space is the shape every wrap produces.
	wrapped := "one two \nthree"
	unwrapped := claudeUnwrap(wrapped)
	if unwrapped != "one two three" {
		t.Fatalf("unwrap: %q", unwrapped)
	}
	// The caret sitting just before "three" in the wrapped text is offset 9 (past
	// the break); in the unwrapped text it is 8.
	if got := claudeRealIndex(wrapped, 9); got != 8 {
		t.Errorf("real index of the caret: got %d, want 8", got)
	}
	// And back again: it lands past the break rather than stranded at the end of
	// the line above.
	if got := claudeWrappedIndex(wrapped, 8); got != 9 {
		t.Errorf("wrapped index of the caret: got %d, want 9", got)
	}
	// The end of the text maps to the end of the text.
	if got := claudeWrappedIndex(wrapped, len(unwrapped)); got != len(wrapped) {
		t.Errorf("end of text: got %d, want %d", got, len(wrapped))
	}
}

// The box grows with the text and then stops, so a long paste cannot squeeze the
// transcript out of the window.
func TestClaudeInputHeightGrowsAndStops(t *testing.T) {
	defer func(prev string) { claudeState.input = prev }(claudeState.input)
	var empty, grown, capped, winH float32
	withClaudeTextFrame(func() {
		claudeState.input = ""
		empty = claudeInputHeight()
		claudeState.input = strings.Repeat("line\n", 6)
		grown = claudeInputHeight()
		claudeState.input = strings.Repeat("line\n", 400)
		capped = claudeInputHeight()
		winH = imgui.WindowHeight()
	})
	{
		if grown <= empty {
			t.Errorf("the box did not grow: %.0f then %.0f", empty, grown)
		}
		if capped > winH/2 {
			t.Errorf("the box grew to %.0f of a %.0f window", capped, winH)
		}
	}
}

// Typing into the real widget: characters go in through imgui's input queue, the
// callback runs as it does in the editor, and what comes out is wrapped text that
// unwraps to exactly what was typed.
//
// This is the half the unit tests above cannot reach — that the box is given the
// callback and the CallbackAlways flag at all, and that rewriting the buffer from
// inside the callback does not upset the widget.
func TestClaudeInputBoxWrapsTyping(t *testing.T) {
	defer func(prev claudePanelState) { claudeState = prev }(claudeState)
	claudeState.input = ""
	claudeState.wrapped = ""

	typed := "Put a checkpoint volume just inside the hangar door, and have the " +
		"mechanics stop working when the player walks past them."
	withImgui(func() {
		io := imgui.CurrentIO()
		io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 900})
		io.SetDeltaTime(1.0 / 60.0)
		for frame := 0; frame < 6; frame++ {
			imgui.NewFrame()
			imgui.SetNextWindowSize(imgui.Vec2{X: 420, Y: 400})
			imgui.Begin("typing")
			if frame == 0 {
				imgui.SetKeyboardFocusHere()
			}
			// Frame 0 asks for the focus, frame 1 is where the box takes it, and
			// typing lands from frame 2 on: characters queued before the widget is
			// active are dropped.
			if frame == 2 {
				io.AddInputCharacters(typed)
			}
			claudeDrawInputBox()
			imgui.End()
			imgui.Render()
		}
	})

	if claudeState.input == "" {
		t.Skip("imgui did not accept the typing without a backend")
	}
	if !strings.Contains(claudeState.input, "\n") {
		t.Errorf("the box did not wrap what was typed: %q", claudeState.input)
	}
	if got := claudeUnwrap(claudeState.input); got != typed {
		t.Errorf("what would be sent is not what was typed:\n got %q\nwant %q", got, typed)
	}
}
