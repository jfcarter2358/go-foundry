package editor

// editor_tracker_input.go — the tracker window's keyboard.
//
// Keys arrive as SDL events (HandleEvent runs after ImGui has seen them), and
// are taken by scancode: the QWERTY piano is a physical layout, so it has to
// mean the same rows of keys on any keyboard map.
//
// Two layers. The transport and file keys (F5–F8, Ctrl+S/O/N, undo) work
// wherever the mouse is, as long as no text field has the keyboard. The rest —
// the piano, hex entry, the cursor, the clipboard and the selection tools —
// only while the grid has focus (trkGridFocus: clicking the grid takes it,
// clicking any other pane gives it up), because the same letters are what
// a name field or the effect picker's filter wants.
//
// A typed note sounds on its key-down and is released on its key-up, keyed by
// scancode, so holding a key holds the note the way a keyboard instrument does.
// Auto-repeat is ignored for notes (a held key is one note, not a drum roll)
// and honoured for the cursor keys.

import (
	"foundry/internal/imguibackend"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// trkHeldKeys are the scancodes currently sounding an audition note.
var trkHeldKeys = map[sdl.Scancode]bool{}

func trackerEditorHandleEvent(event sdl.Event) {
	t, ok := event.(sdl.KeyboardEvent)
	if !ok || trkEd == nil {
		return
	}
	sc := t.Keysym.Scancode
	if t.Type == sdl.KEYUP {
		if trkHeldKeys[sc] {
			delete(trkHeldKeys, sc)
			trkAuditionOff(int(sc))
		}
		return
	}
	if t.Type != sdl.KEYDOWN || imgui.CurrentIO().WantCaptureKeyboard() {
		return
	}
	mod := sdl.GetModState()
	ctrl := mod&sdl.KMOD_CTRL != 0
	shift := mod&sdl.KMOD_SHIFT != 0
	alt := mod&sdl.KMOD_ALT != 0
	repeat := t.Repeat != 0

	if trkGlobalKey(sc, ctrl, shift) {
		return
	}
	if !trkGridFocus || alt {
		return
	}
	trkGridKey(sc, ctrl, shift, repeat)
}

// trkGlobalKey handles the keys that work anywhere in the window.
func trkGlobalKey(sc sdl.Scancode, ctrl, shift bool) bool {
	switch sc {
	case sdl.SCANCODE_F5:
		trkPlaySong()
		return true
	case sdl.SCANCODE_F6:
		trkPlayPatternLoop()
		return true
	case sdl.SCANCODE_F7:
		trkPlayFromCursor()
		return true
	case sdl.SCANCODE_F8:
		trkStop()
		return true
	}
	if !ctrl {
		return false
	}
	switch sc {
	case sdl.SCANCODE_S:
		if shift {
			trkSaveAsName = trkEd.Name
			trkShowSaveAs = true
		} else {
			trkSaveCurrent()
		}
	case sdl.SCANCODE_O:
		trkShowOpen, trkSongList = true, nil
	case sdl.SCANCODE_Z:
		if shift {
			trkRedo()
		} else {
			trkUndo()
		}
	case sdl.SCANCODE_Y:
		trkRedo()
	default:
		return false
	}
	return true
}

// trkGridKey handles the keys that belong to the grid.
func trkGridKey(sc sdl.Scancode, ctrl, shift, repeat bool) {
	e := trkEd
	trkKeepCursorInView = true
	if ctrl {
		if d, ok := trkDigitForKey(sc); ok {
			e.Step = d
			e.Status = "Edit step " + itoa(d)
			return
		}
		switch sc {
		case sdl.SCANCODE_C:
			trkClipboardCopy()
		case sdl.SCANCODE_X:
			trkClipboardCut()
		case sdl.SCANCODE_V:
			trkClipboardPaste(shift)
		case sdl.SCANCODE_A:
			e.selectAll()
		case sdl.SCANCODE_I:
			e.interpolate()
		case sdl.SCANCODE_UP:
			if shift {
				e.transpose(12)
			} else {
				e.transpose(1)
			}
		case sdl.SCANCODE_DOWN:
			if shift {
				e.transpose(-12)
			} else {
				e.transpose(-1)
			}
		case sdl.SCANCODE_SPACE:
			trkOpenEffectPicker()
		case sdl.SCANCODE_LEFTBRACKET:
			trkSelectInstrument(e.Instr - 1)
		case sdl.SCANCODE_RIGHTBRACKET:
			trkSelectInstrument(e.Instr + 1)
		}
		return
	}

	// Navigation.
	switch sc {
	case sdl.SCANCODE_UP:
		if shift {
			e.extendSelection(-1, 0)
		} else {
			e.clearSelection()
			e.moveRows(-1)
		}
		return
	case sdl.SCANCODE_DOWN:
		if shift {
			e.extendSelection(1, 0)
		} else {
			e.clearSelection()
			e.moveRows(1)
		}
		return
	case sdl.SCANCODE_LEFT:
		if shift {
			e.extendSelection(0, -1)
		} else {
			e.clearSelection()
			e.moveField(-1)
		}
		return
	case sdl.SCANCODE_RIGHT:
		if shift {
			e.extendSelection(0, 1)
		} else {
			e.clearSelection()
			e.moveField(1)
		}
		return
	case sdl.SCANCODE_TAB:
		if shift {
			e.nextChannel(-1)
		} else {
			e.nextChannel(1)
		}
		return
	case sdl.SCANCODE_PAGEUP:
		if shift {
			e.extendSelection(-e.barRows(), 0)
		} else {
			e.moveRows(-e.barRows())
		}
		return
	case sdl.SCANCODE_PAGEDOWN:
		if shift {
			e.extendSelection(e.barRows(), 0)
		} else {
			e.moveRows(e.barRows())
		}
		return
	case sdl.SCANCODE_HOME:
		e.home()
		return
	case sdl.SCANCODE_END:
		e.end()
		return
	case sdl.SCANCODE_ESCAPE:
		e.clearSelection()
		return
	case sdl.SCANCODE_SPACE:
		if !repeat {
			e.EditMode = !e.EditMode
		}
		return
	case sdl.SCANCODE_DELETE, sdl.SCANCODE_BACKSPACE:
		if shift && sc == sdl.SCANCODE_BACKSPACE {
			e.deleteRow()
		} else {
			e.clearField()
		}
		return
	case sdl.SCANCODE_INSERT:
		e.insertRow()
		return
	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		if k, _, _ := trkFieldKind(e.Cur.Field); k == trkKindFXCmd || k == trkKindFXParam {
			trkOpenEffectPicker()
		}
		return
	case sdl.SCANCODE_KP_PLUS:
		trkSelectInstrument(e.Instr + 1)
		return
	case sdl.SCANCODE_KP_MINUS:
		trkSelectInstrument(e.Instr - 1)
		return
	}

	// The note column's octave keys work anywhere in the grid.
	kind, _, _ := trkFieldKind(e.Cur.Field)
	if kind == trkKindNote || kind == trkKindInstr {
		switch sc {
		case sdl.SCANCODE_LEFTBRACKET:
			e.Octave = clampI(e.Octave-1, 0, 8)
			e.Status = "Octave " + itoa(e.Octave)
			return
		case sdl.SCANCODE_RIGHTBRACKET:
			e.Octave = clampI(e.Octave+1, 0, 8)
			e.Status = "Octave " + itoa(e.Octave)
			return
		}
	}
	if repeat && kind == trkKindNote {
		return
	}
	note := e.typeKey(sc)
	if note >= tracker.NoteMin && note <= tracker.NoteMax {
		trkSyncPlayer(false)
		trkAudition(int(sc), e.Instr, note)
		trkHeldKeys[sc] = true
	}
}

// trkOpenEffectPicker opens the effect picker when the cursor is on an effect
// column; elsewhere it moves the cursor to the first effect column first.
func trkOpenEffectPicker() {
	if k, _, _ := trkFieldKind(trkEd.Cur.Field); k != trkKindFXCmd && k != trkKindFXParam {
		trkEd.Cur.Field = trkFieldsFixed
	}
	trkOpenPicker = true
}

// trkSelectInstrument makes instrument n (1-based) current, clamped.
func trkSelectInstrument(n int) {
	if len(trkEd.Song.Instruments) == 0 {
		trkEd.Instr = 0
		return
	}
	trkEd.Instr = clampI(n, 1, len(trkEd.Song.Instruments))
	trkSampleWork = nil
}

// ---------- clipboard ----------

// trkClip is the editor's own copy of the last copied block — used when the
// system clipboard holds something that is not tracker text.
var trkClip string

func trkClipboardCopy() {
	s := trkEd.copyText()
	trkClip = s
	imguibackend.SdlClipboard{}.SetText(s)
	r0, r1, c0, c1 := trkEd.selRect()
	trkEd.Status = "Copied " + itoa(r1-r0+1) + " rows x " + itoa(c1-c0+1) + " channels"
}

func trkClipboardCut() {
	s := trkEd.cutSelection()
	trkClip = s
	imguibackend.SdlClipboard{}.SetText(s)
	trkEd.Status = "Cut"
}

// trkClipboardPaste pastes the system clipboard if it is tracker text, else
// the editor's own copy.
func trkClipboardPaste(mix bool) {
	text := imguibackend.ClipboardText()
	if _, err := parseClip(text); err != nil {
		text = trkClip
	}
	if err := trkEd.paste(text, mix); err != nil {
		trkEd.Status = "Paste: " + err.Error()
		return
	}
	if mix {
		trkEd.Status = "Mix-pasted"
	} else {
		trkEd.Status = "Pasted"
	}
}
