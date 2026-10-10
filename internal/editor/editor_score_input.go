package editor

// editor_score_input.go — the Score window's keyboard.
//
// Keys arrive as SDL events after ImGui has seen them and are taken by
// scancode (editor_score_keymap.go says why). A chord finds its command in
// the active keymap and scoreRunCmd does it. Nothing reaches the keymap while
// ImGui wants the keyboard — a text field, or any modal dialog — except a
// rebinding in the Keyboard Shortcuts dialog, which takes the next chord
// before anything else can (scoreCapture).
//
// Auto-repeat is honoured for the keys it makes sense to hold (moving the
// selection, moving a pitch, zoom) and ignored for the rest: a held 3 is one
// third, not a stack of them.

import (
	"fmt"
	"strings"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// scoreRepeatable are the commands a held key repeats.
var scoreRepeatable = map[scoreCmd]bool{
	scPrev: true, scNext: true, scSemiUp: true, scSemiDown: true, scStepUp: true, scStepDown: true,
	scOctUp: true, scOctDown: true, scZoomIn: true, scZoomOut: true,
}

// scoreCaptureState is a rebinding waiting for its chord.
type scoreCaptureState struct {
	active bool
	cmd    scoreCmd
	// add keeps the command's other chords; otherwise the new one replaces them.
	add bool
}

var scoreCapture scoreCaptureState

func scoreEditorHandleEvent(event sdl.Event) {
	t, ok := event.(sdl.KeyboardEvent)
	if !ok || scEd == nil || t.Type != sdl.KEYDOWN {
		return
	}
	sc := t.Keysym.Scancode
	if scoreCapture.active {
		if msg, done := scoreCaptureKey(sc, sdl.GetModState()); done {
			scEd.Status = msg
		}
		return
	}
	if imgui.CurrentIO().WantCaptureKeyboard() || scoreModifierKey(sc) {
		return
	}
	scoreDispatch(scoreChordOf(sc, sdl.GetModState()), t.Repeat != 0)
}

// scoreDispatch runs the command a chord is bound to, reporting whether
// there was one.
func scoreDispatch(ch scoreChord, repeat bool) bool {
	c := scoreKeymapActive().command(ch)
	if c == scNone {
		return false
	}
	if repeat && !scoreRepeatable[c] {
		return true
	}
	scoreRunCmd(c)
	return true
}

// scoreCaptureKey takes a key for the rebinding being captured. Escape
// cancels; a modifier alone is waited past; F1 is refused. done reports the
// capture finished, with what happened.
func scoreCaptureKey(sc sdl.Scancode, mod sdl.Keymod) (msg string, done bool) {
	if sc == sdl.SCANCODE_ESCAPE {
		scoreCapture = scoreCaptureState{}
		return "Rebinding cancelled", true
	}
	if scoreModifierKey(sc) {
		return "", false
	}
	if scoreReservedKey(sc) {
		return scoreScancodeName(sc) + " is the reference everywhere; pick another key", false
	}
	c, add := scoreCapture.cmd, scoreCapture.add
	scoreCapture = scoreCaptureState{}
	msg = scoreBindChord(scoreKeymapActive(), c, scoreChordOf(sc, mod), add)
	scoreSaveBindings()
	return msg, true
}

// scoreBindChord binds a chord to a command (beside its others when add,
// else instead of them), taking it from whatever had it, and says what
// happened.
func scoreBindChord(k *scoreKeymap, c scoreCmd, ch scoreChord, add bool) string {
	var chords []scoreChord
	if add {
		for _, o := range k.chords(c) {
			if o != ch {
				chords = append(chords, o)
			}
		}
	}
	chords = append(chords, ch)
	from := k.rebind(c, chords)
	msg := fmt.Sprintf("%s: %s", ch, scoreCmdInfos[c].Label)
	if len(from) > 0 {
		var names []string
		for _, f := range from {
			names = append(names, scoreCmdInfos[f].Label)
		}
		msg += " (taken from " + strings.Join(names, ", ") + ")"
	}
	return msg
}
