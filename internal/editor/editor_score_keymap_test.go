package editor

import (
	"testing"

	"foundry/internal/score"

	"github.com/veandco/go-sdl2/sdl"
)

// Every command has a name, a label and a group the overlay lists, and the
// names are unique (they are what score.yaml stores).
func TestScoreCommandsAreDescribed(t *testing.T) {
	seen := map[string]bool{}
	groups := map[string]bool{}
	for _, g := range scoreCmdGroups {
		groups[g] = true
	}
	for c := scoreCmd(1); c < scCmdCount; c++ {
		in := scoreCmdInfos[c]
		if in.Name == "" || in.Label == "" || !groups[in.Group] {
			t.Errorf("command %d is %+v", c, in)
		}
		if seen[in.Name] {
			t.Errorf("command name %q twice", in.Name)
		}
		seen[in.Name] = true
		if back, ok := scoreCmdByName(in.Name); !ok || back != c {
			t.Errorf("%q finds %d", in.Name, back)
		}
	}
}

// The Sibelius keymap is SPEC.md's input table.
func TestScoreSibeliusKeymap(t *testing.T) {
	k := scoreSibeliusKeymap()
	cases := []struct {
		ch   scoreChord
		want scoreCmd
	}{
		{key(sdl.SCANCODE_N), scNoteInput},
		{key(sdl.SCANCODE_A), scLetterC + 5},
		{key(sdl.SCANCODE_C), scLetterC},
		{shiftKey(sdl.SCANCODE_G), scAddC + 4},
		{key(sdl.SCANCODE_3), scInt2 + 1},
		{shiftKey(sdl.SCANCODE_6), scBelow2 + 4},
		{key(sdl.SCANCODE_KP_1), scKeypad1},
		{key(sdl.SCANCODE_KP_6), scKeypad1 + 5},
		{altKey(sdl.SCANCODE_4), scKeypad1 + 3},
		{key(sdl.SCANCODE_F8), scLayout},
		{key(sdl.SCANCODE_KP_PERIOD), scDot},
		{key(sdl.SCANCODE_KP_0), scRest},
		{altKey(sdl.SCANCODE_0), scRest},
		{key(sdl.SCANCODE_KP_ENTER), scTie},
		{key(sdl.SCANCODE_T), scTie},
		{key(sdl.SCANCODE_EQUALS), scSharp},
		{shiftKey(sdl.SCANCODE_EQUALS), scSharp},
		{key(sdl.SCANCODE_MINUS), scFlat},
		{key(sdl.SCANCODE_0), scNatural},
		// Keypad 7, 8, 9 and + follow the keypad layout: the accidentals
		// and the accent, or articulations on the second.
		{key(sdl.SCANCODE_KP_8), scKeypad8},
		{key(sdl.SCANCODE_KP_9), scKeypad9},
		{key(sdl.SCANCODE_KP_7), scKeypad7},
		{key(sdl.SCANCODE_KP_PLUS), scKeypadPlus},
		{key(sdl.SCANCODE_S), scSlur},
		{key(sdl.SCANCODE_H), scCresc},
		{shiftKey(sdl.SCANCODE_H), scDim},
		{altKey(sdl.SCANCODE_G), scGliss},
		{key(sdl.SCANCODE_G), scLetterC + 4}, // G is still the note
		{ctrlKey(sdl.SCANCODE_E), scExpression},
		{key(sdl.SCANCODE_PERIOD), scStaccato},
		{key(sdl.SCANCODE_APOSTROPHE), scStaccatissimo},
		{shiftKey(sdl.SCANCODE_PERIOD), scAccent},
		{shiftKey(sdl.SCANCODE_MINUS), scTenuto},
		{shiftKey(sdl.SCANCODE_APOSTROPHE), scMarcato},
		{ctrlAlt(sdl.SCANCODE_T), scTempoText},
		{ctrlKey(sdl.SCANCODE_R), scRehearsal},
		{ctrlShift(sdl.SCANCODE_L), scLane},
		{ctrlAlt(sdl.SCANCODE_P), scMarksPanel},
		{key(sdl.SCANCODE_UP), scSemiUp},
		{altKey(sdl.SCANCODE_DOWN), scStepDown},
		{ctrlKey(sdl.SCANCODE_UP), scOctUp},
		{key(sdl.SCANCODE_RETURN), scRespell},
		{ctrlAlt(sdl.SCANCODE_1), scVoice1},
		{ctrlAlt(sdl.SCANCODE_4), scVoice1 + 3},
		{ctrlKey(sdl.SCANCODE_3), scTuplet2 + 1},
		{ctrlKey(sdl.SCANCODE_5), scTuplet2 + 3},
		{key(sdl.SCANCODE_DELETE), scDelete},
		{ctrlKey(sdl.SCANCODE_C), scCopy},
		{ctrlKey(sdl.SCANCODE_V), scPaste},
		{key(sdl.SCANCODE_R), scRepeat},
		{ctrlKey(sdl.SCANCODE_A), scSelectAll},
		{ctrlKey(sdl.SCANCODE_Z), scUndo},
		{ctrlKey(sdl.SCANCODE_Y), scRedo},
		{key(sdl.SCANCODE_SPACE), scPlay},
		{key(sdl.SCANCODE_P), scPlayFrom},
		{key(sdl.SCANCODE_K), scKeySig},
		{key(sdl.SCANCODE_Q), scClef},
		{key(sdl.SCANCODE_X), scFlip},
		{ctrlKey(sdl.SCANCODE_S), scSave},
		{key(sdl.SCANCODE_F1), scNone}, // the reference, never the keymap's
		{key(sdl.SCANCODE_Z), scNone},
	}
	for _, c := range cases {
		if got := k.command(c.ch); got != c.want {
			t.Errorf("%s = %q, want %q", c.ch, scoreCmdInfos[got].Name, scoreCmdInfos[c.want].Name)
		}
	}
	if cs := k.conflicts(); len(cs) > 0 {
		t.Errorf("Sibelius binds a key twice: %v", cs)
	}
}

func TestScoreMuseScoreKeymap(t *testing.T) {
	k := scoreMuseScoreKeymap()
	cases := []struct {
		ch   scoreChord
		want scoreCmd
	}{
		{key(sdl.SCANCODE_1), scDur256 + 2}, // 64th
		{key(sdl.SCANCODE_5), scDur256 + 6}, // quarter
		{key(sdl.SCANCODE_7), scDur256 + 8}, // whole
		{key(sdl.SCANCODE_8), scDur256 + 9}, // breve
		{key(sdl.SCANCODE_KP_4), scDur256 + 5},
		{altKey(sdl.SCANCODE_3), scInt2 + 1},
		{altShift(sdl.SCANCODE_3), scBelow2 + 1},
		{key(sdl.SCANCODE_J), scRespell},
		{key(sdl.SCANCODE_0), scRest},
		{key(sdl.SCANCODE_T), scTie},
		{key(sdl.SCANCODE_PERIOD), scDot},
		{altShift(sdl.SCANCODE_UP), scStepUp},
		{key(sdl.SCANCODE_N), scNoteInput},
		{ctrlAlt(sdl.SCANCODE_2), scVoice1 + 1},
		{ctrlKey(sdl.SCANCODE_3), scTuplet2 + 1},
		{key(sdl.SCANCODE_E), scLetterC + 2},
	}
	for _, c := range cases {
		if got := k.command(c.ch); got != c.want {
			t.Errorf("%s = %q, want %q", c.ch, scoreCmdInfos[got].Name, scoreCmdInfos[c.want].Name)
		}
	}
	if cs := k.conflicts(); len(cs) > 0 {
		t.Errorf("MuseScore binds a key twice: %v", cs)
	}
}

// Chords print and parse back, for every chord either preset binds.
func TestScoreChordText(t *testing.T) {
	for _, p := range scoreKeymapPresets {
		k := p.Build()
		for c := scoreCmd(1); c < scCmdCount; c++ {
			for _, ch := range k.chords(c) {
				back, err := parseScoreChord(ch.String())
				if err != nil || back != ch {
					t.Errorf("%s: %q reads back as %+v, %v", p.Name, ch.String(), back, err)
				}
			}
		}
	}
	if s := ctrlAlt(sdl.SCANCODE_1).String(); s != "Ctrl+Alt+1" {
		t.Errorf("Ctrl+Alt+1 prints %q", s)
	}
	if ch, err := parseScoreChord("shift+ctrl+keypad +"); err != nil || ch != ctrlShift(sdl.SCANCODE_KP_PLUS) {
		t.Errorf("lower-case, reordered: %+v %v", ch, err)
	}
	if _, err := parseScoreChord("Ctrl+Nope"); err == nil {
		t.Error("a key that does not exist parsed")
	}
}

// Rebinding takes a chord from whatever had it, survives the round trip
// through score.yaml's overrides, and a fresh preset has none.
func TestScoreRebinding(t *testing.T) {
	k := scoreSibeliusKeymap()
	if ov := k.overrides(scoreSibeliusKeymap()); len(ov) != 0 {
		t.Errorf("a fresh preset has overrides %v", ov)
	}
	// Bind U (unbound) to tie, beside its others.
	msg := scoreBindChord(k, scTie, key(sdl.SCANCODE_U), true)
	if k.command(key(sdl.SCANCODE_U)) != scTie || k.command(key(sdl.SCANCODE_T)) != scTie {
		t.Errorf("add: %s", msg)
	}
	// Bind R (repeat's) to the rest, replacing its keys: R is taken.
	msg = scoreBindChord(k, scRest, key(sdl.SCANCODE_R), false)
	if k.command(key(sdl.SCANCODE_R)) != scRest || k.command(key(sdl.SCANCODE_KP_0)) != scNone {
		t.Errorf("replace: %s", msg)
	}
	if len(k.chords(scRepeat)) != 0 {
		t.Errorf("repeat kept R: %v", k.chords(scRepeat))
	}
	ov := k.overrides(scoreSibeliusKeymap())
	if len(ov) != 3 || len(ov["repeat"]) != 0 || ov["rest"][0] != "R" {
		t.Errorf("overrides = %v", ov)
	}
	// Laid over a fresh preset, the overrides make the same keymap.
	back := scoreSibeliusKeymap()
	if probs := back.applyOverrides(ov); len(probs) > 0 {
		t.Fatal(probs)
	}
	for c := scoreCmd(1); c < scCmdCount; c++ {
		if !chordsEqual(back.chords(c), k.chords(c)) {
			t.Errorf("%s: %v, want %v", scoreCmdInfos[c].Name, back.chords(c), k.chords(c))
		}
	}
	// Bad entries are reported, not fatal.
	if probs := back.applyOverrides(map[string][]string{"no_such": {"A"}, "tie": {"Ctrl+Nope"}}); len(probs) != 2 {
		t.Errorf("problems = %v", probs)
	}
	// The capture: Escape cancels, a modifier waits, F1 is refused.
	scoreKeys = scoreSibeliusKeymap()
	t.Cleanup(func() { scoreKeys = nil; scoreCapture = scoreCaptureState{} })
	scoreCapture = scoreCaptureState{active: true, cmd: scFlip}
	if _, done := scoreCaptureKey(sdl.SCANCODE_LSHIFT, sdl.KMOD_LSHIFT); done || !scoreCapture.active {
		t.Error("a modifier ended the capture")
	}
	if _, done := scoreCaptureKey(sdl.SCANCODE_F1, 0); done {
		t.Error("F1 was bound")
	}
	if _, done := scoreCaptureKey(sdl.SCANCODE_ESCAPE, 0); !done || scoreCapture.active {
		t.Error("Escape did not cancel")
	}
}

// A chord runs its command; a held key repeats only what holding means.
func TestScoreDispatch(t *testing.T) {
	prev := scEd
	t.Cleanup(func() { scEd = prev; scoreKeys = nil })
	scEd = scoreTestState(t)
	scoreKeys = scoreSibeliusKeymap()
	if !scoreDispatch(key(sdl.SCANCODE_N), false) || !scEd.Input {
		t.Fatal("N did not start note input")
	}
	scEd.clickBar(0, 1, false, false)
	scoreDispatch(key(sdl.SCANCODE_G), false)
	scoreDispatch(key(sdl.SCANCODE_3), false)
	scoreDispatch(key(sdl.SCANCODE_3), true) // auto-repeat: ignored
	if got := barOf(scEd, 0, 0, 1); got != "qG4+B4 qr hr" {
		t.Errorf("bar = %q", got)
	}
	scoreDispatch(key(sdl.SCANCODE_UP), false)
	scoreDispatch(key(sdl.SCANCODE_UP), true) // a held arrow repeats
	if got := barOf(scEd, 0, 0, 1); got != "qG4+C#5 qr hr" {
		t.Errorf("after two semitones = %q", got)
	}
	// In note input keypad 5 is the next note's value; out of it, the
	// selected chord's.
	scoreDispatch(key(sdl.SCANCODE_KP_5), false)
	if got := barOf(scEd, 0, 0, 1); got != "qG4+C#5 qr hr" || scEd.Dur.Base != score.Half {
		t.Errorf("keypad 5 in note input = %q (%v)", got, scEd.Dur)
	}
	scoreDispatch(key(sdl.SCANCODE_ESCAPE), false)
	scoreDispatch(key(sdl.SCANCODE_KP_5), false)
	if got := barOf(scEd, 0, 0, 1); got != "hG4+C#5 hr" {
		t.Errorf("keypad 5 = %q", got)
	}
	if scoreDispatch(key(sdl.SCANCODE_F11), false) {
		t.Error("an unbound key ran something")
	}
}
