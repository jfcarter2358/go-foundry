package editor

// editor_score_keymap.go — the Score editor's keyboard: its commands, the
// two keymaps it ships (Sibelius, the default, and MuseScore), the player's
// own rebindings on top, and how a key chord finds its command.
//
// A command is a name ("note_input", "interval_3"), a label, a group for the
// F1 overlay and the settings list, and nothing else: what it does is
// scoreRunCmd (editor_score_window.go), which calls the editing model. A
// keymap maps each command to the chords that run it; a chord is a scancode
// and the Ctrl/Shift/Alt it is pressed with. Scancodes, not key names,
// because the keypad's 1-6 and the main row's 1-6 are different commands and
// a German keyboard's Z is still the key under the left hand.
//
// The rebindings are kept per command in score.yaml (fileutil.StatePath,
// beside claude.yaml): a command listed there has exactly the chords listed,
// and every other command keeps the preset's. Binding a chord already in
// use takes it from the command that had it.
//
// Two layers decide what a keypad slot means. In Sibelius the keypad is
// "slots" whose meaning is the keypad layout (F8 flips to the second one:
// 64th, 128th, 256th and breve on 1-4), so the keypad commands are slot
// commands; the toolbar and MuseScore's digits name durations directly.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/edit"

	"github.com/veandco/go-sdl2/sdl"
)

// scoreCmd is one thing a key can do in the Score editor.
type scoreCmd int

// The commands. Runs of them are ranges (letters C..B, intervals 2nd..9th,
// keypad slots 1..6, the direct durations 256th..breve, voices, tuplets 2..9);
// the blanks hold their places, so scLetterC+step is a letter's command.
const (
	scNone scoreCmd = iota
	scNoteInput
	scEscape
	scLetterC // + step: C D E F G A B
	_
	_
	_
	_
	_
	_
	scAddC // + step: add the letter to the chord
	_
	_
	_
	_
	_
	_
	scInt2 // + n-2: an interval above, 2nd..9th
	_
	_
	_
	_
	_
	_
	_
	scBelow2 // + n-2: an interval below
	_
	_
	_
	_
	_
	_
	_
	scSharp
	scFlat
	scNatural
	scSemiUp
	scSemiDown
	scStepUp
	scStepDown
	scOctUp
	scOctDown
	scRespell
	scKeypad1 // + slot-1: keypad 1..6, by the keypad layout
	_
	_
	_
	_
	_
	scDur256 // + index into scoreDirectDurs: 256th..breve
	scDur128
	_
	_
	_
	_
	_
	_
	_
	_
	scLayout
	scDot
	scRest
	scTie
	scFlip
	scVoiceCycle
	scVoice1 // + voice
	_
	_
	_
	scTuplet2 // + n-2
	_
	_
	_
	_
	_
	_
	_
	scDelete
	scPrev
	scNext
	scCopy
	scPaste
	scRepeat
	scSelectAll
	scUndo
	scRedo
	scKeySig
	scTimeSig
	scClef
	scPlay
	scPlayFrom
	scSave
	scSaveAs
	scOpen
	scZoomIn
	scZoomOut
	scZoomReset
	scKeypadPanel
	scLoop
	scLoopToggle
	scToStart
	scFollow
	scMixer
	scExportAudio
	scAddLine
	// Expression (editor_score_marks.go).
	scSlur
	scCresc
	scDim
	scGliss
	scExpression
	scTempoText
	scStaccato
	scStaccatissimo
	scAccent
	scTenuto
	scMarcato
	scKeypad7 // keypad 7, 8, 9 and +, by the keypad layout: accidentals and
	scKeypad8 // the accent on the first, articulations on the second
	scKeypad9
	scKeypadPlus
	scGraceAcc
	scGraceApp
	scMarksPanel
	scConcert
	scLane
	scRehearsal
	scRepeatBars
	scEnding
	scClefChange
	scCmdCount
)

// scoreCmdInfo is a command's name, label and group.
type scoreCmdInfo struct {
	Name, Label, Group string
}

// scoreCmdInfos describes every command, indexed by scoreCmd.
var scoreCmdInfos = buildScoreCmdInfos()

// The keymap's groups, in the order the overlay and the settings list them.
var scoreCmdGroups = []string{"Entry", "Pitch", "Durations", "Rhythm", "Voices & tuplets", "Expression", "Form", "Selection", "File & view", "Playback"}

func buildScoreCmdInfos() []scoreCmdInfo {
	in := make([]scoreCmdInfo, scCmdCount)
	set := func(c scoreCmd, name, label, group string) { in[c] = scoreCmdInfo{name, label, group} }
	set(scNoteInput, "note_input", "Note input on / off", "Entry")
	set(scEscape, "escape", "Leave note input, then clear the selection", "Entry")
	for i := 0; i < 7; i++ {
		l := string("CDEFGAB"[i])
		set(scLetterC+scoreCmd(i), "letter_"+strings.ToLower(l), "Note "+l, "Entry")
		set(scAddC+scoreCmd(i), "add_"+strings.ToLower(l), "Add "+l+" to the chord", "Entry")
	}
	ordinal := []string{"2nd", "3rd", "4th", "5th", "6th", "7th", "octave", "9th"}
	for n := 2; n <= 9; n++ {
		set(scInt2+scoreCmd(n-2), fmt.Sprintf("interval_%d", n), "Add a "+ordinal[n-2]+" above", "Pitch")
		set(scBelow2+scoreCmd(n-2), fmt.Sprintf("interval_below_%d", n), "Add a "+ordinal[n-2]+" below", "Pitch")
	}
	set(scSharp, "sharp", "Sharp (again: double sharp)", "Pitch")
	set(scFlat, "flat", "Flat (again: double flat)", "Pitch")
	set(scNatural, "natural", "Natural", "Pitch")
	set(scSemiUp, "semitone_up", "Up a semitone", "Pitch")
	set(scSemiDown, "semitone_down", "Down a semitone", "Pitch")
	set(scStepUp, "step_up", "Up a step in the key", "Pitch")
	set(scStepDown, "step_down", "Down a step in the key", "Pitch")
	set(scOctUp, "octave_up", "Up an octave", "Pitch")
	set(scOctDown, "octave_down", "Down an octave", "Pitch")
	set(scRespell, "respell", "Respell (F# / Gb)", "Pitch")
	// A slot's label says what it is on both layouts.
	slots := []string{"32nd (second layout: 64th)", "16th (second layout: 128th)", "8th (second layout: 256th)",
		"Quarter (second layout: breve)", "Half", "Whole"}
	for i := 0; i < 6; i++ {
		set(scKeypad1+scoreCmd(i), fmt.Sprintf("keypad_%d", i+1), slots[i], "Durations")
	}
	for i, d := range scoreDirectDurs {
		set(scDur256+scoreCmd(i), "duration_"+d.name, scoreDurLabel(d.base)+" note", "Durations")
	}
	set(scLayout, "keypad_layout", "Second keypad layout (64th, 128th, 256th, breve)", "Durations")
	set(scDot, "dot", "Dot (again: double dot)", "Rhythm")
	set(scRest, "rest", "Rest", "Rhythm")
	set(scTie, "tie", "Tie", "Rhythm")
	set(scFlip, "flip", "Flip the stem", "Rhythm")
	set(scVoiceCycle, "voice_next", "Next voice", "Voices & tuplets")
	for v := 0; v < 4; v++ {
		set(scVoice1+scoreCmd(v), fmt.Sprintf("voice_%d", v+1), fmt.Sprintf("Voice %d", v+1), "Voices & tuplets")
	}
	for n := 2; n <= 9; n++ {
		set(scTuplet2+scoreCmd(n-2), fmt.Sprintf("tuplet_%d", n), fmt.Sprintf("Tuplet of %d", n), "Voices & tuplets")
	}
	set(scDelete, "delete", "Delete (a note becomes a rest)", "Rhythm")
	set(scPrev, "previous", "Select the previous note", "Selection")
	set(scNext, "next", "Select the next note", "Selection")
	set(scCopy, "copy", "Copy", "Selection")
	set(scPaste, "paste", "Paste", "Selection")
	set(scRepeat, "repeat", "Repeat the selection after itself", "Selection")
	set(scSelectAll, "select_all", "Select every bar of every line", "Selection")
	set(scUndo, "undo", "Undo", "Selection")
	set(scRedo, "redo", "Redo", "Selection")
	set(scKeySig, "key_signature", "Key signature...", "Entry")
	set(scTimeSig, "time_signature", "Time signature...", "Entry")
	set(scClef, "clef", "Clef...", "Entry")
	set(scPlay, "play", "Play / stop", "Playback")
	set(scPlayFrom, "play_from_selection", "Play from the selection", "Playback")
	set(scSave, "save", "Save", "File & view")
	set(scSaveAs, "save_as", "Save as...", "File & view")
	set(scOpen, "open", "Open...", "File & view")
	set(scZoomIn, "zoom_in", "Zoom in", "File & view")
	set(scZoomOut, "zoom_out", "Zoom out", "File & view")
	set(scZoomReset, "zoom_reset", "Actual size", "File & view")
	set(scKeypadPanel, "keypad_panel", "Show / hide the keypad", "File & view")
	set(scLoop, "loop_selection", "Loop the selected bars (no selection: loop on / off)", "Playback")
	set(scLoopToggle, "loop", "Loop on / off", "Playback")
	set(scToStart, "to_start", "Play head to the start", "Playback")
	set(scFollow, "follow", "Follow playback on / off", "Playback")
	set(scMixer, "mixer", "Score / Mixer view", "File & view")
	set(scExportAudio, "export_audio", "Export audio (WAV)...", "File & view")
	set(scAddLine, "add_line", "Add a line...", "Entry")
	set(scSlur, "slur", "Slur to the next note (again: one note further); over selected bars", "Expression")
	set(scCresc, "crescendo", "Crescendo hairpin to the next note (again: further)", "Expression")
	set(scDim, "diminuendo", "Diminuendo hairpin to the next note (again: further)", "Expression")
	set(scGliss, "glissando", "Glissando to the next note on / off", "Expression")
	set(scExpression, "expression", "Dynamic or expression text... (mf, pizz., dolce, cresc.)", "Expression")
	set(scTempoText, "tempo_text", "Tempo mark or tempo text...", "Form")
	set(scStaccato, "staccato", "Staccato", "Expression")
	set(scStaccatissimo, "staccatissimo", "Staccatissimo", "Expression")
	set(scAccent, "accent", "Accent", "Expression")
	set(scTenuto, "tenuto", "Tenuto", "Expression")
	set(scMarcato, "marcato", "Marcato", "Expression")
	set(scKeypad7, "keypad_7", "Natural (second layout: staccato)", "Expression")
	set(scKeypad8, "keypad_8", "Sharp (second layout: tenuto)", "Expression")
	set(scKeypad9, "keypad_9", "Flat (second layout: staccatissimo)", "Expression")
	set(scKeypadPlus, "keypad_plus", "Accent (second layout: marcato)", "Expression")
	set(scGraceAcc, "grace_acciaccatura", "Grace notes (acciaccatura) on / off", "Entry")
	set(scGraceApp, "grace_appoggiatura", "Grace notes (appoggiatura) on / off", "Entry")
	set(scMarksPanel, "marks_panel", "Show / hide the marks palette", "File & view")
	set(scConcert, "concert_pitch", "Concert pitch on / off", "File & view")
	set(scLane, "dynamics_lane", "Dynamics lane of the selected line on / off", "File & view")
	set(scRehearsal, "rehearsal_mark", "Rehearsal mark (section)...", "Form")
	set(scRepeatBars, "repeat_bars", "Repeat the selected bars...", "Form")
	set(scEnding, "ending", "1st / 2nd ending over the selected bars...", "Form")
	set(scClefChange, "clef_change", "Clef change from the selection...", "Form")
	return in
}

// scoreDirectDurs are the direct duration commands, scDur256 first.
var scoreDirectDurs = []struct {
	name string
	base score.DurBase
}{
	{"256th", score.D256}, {"128th", score.D128}, {"64th", score.D64}, {"32nd", score.D32}, {"16th", score.D16},
	{"8th", score.Eighth}, {"quarter", score.Quarter}, {"half", score.Half}, {"whole", score.WholeNote}, {"breve", score.Breve},
}

// scoreDurLabel is a value's name with a capital: "Quarter", "64th".
func scoreDurLabel(b score.DurBase) string {
	n := edit.DurName(score.Duration{Base: b})
	return strings.ToUpper(n[:1]) + n[1:]
}

// scoreCmdByName finds a command by its settings name.
func scoreCmdByName(name string) (scoreCmd, bool) {
	for c := scoreCmd(1); c < scCmdCount; c++ {
		if scoreCmdInfos[c].Name == name {
			return c, true
		}
	}
	return scNone, false
}

// ---------- chords ----------

// scoreChord is a key and the modifiers held with it.
type scoreChord struct {
	Key              sdl.Scancode
	Ctrl, Shift, Alt bool
}

// scoreChordOf is a chord from SDL's modifier state; Cmd counts as Ctrl.
func scoreChordOf(sc sdl.Scancode, mod sdl.Keymod) scoreChord {
	return scoreChord{Key: sc, Ctrl: mod&(sdl.KMOD_CTRL|sdl.KMOD_GUI) != 0, Shift: mod&sdl.KMOD_SHIFT != 0, Alt: mod&sdl.KMOD_ALT != 0}
}

// String writes a chord as the settings store it and the overlay shows it:
// "Ctrl+Alt+1", "Keypad 5", "Shift+H".
func (c scoreChord) String() string {
	var b strings.Builder
	if c.Ctrl {
		b.WriteString("Ctrl+")
	}
	if c.Alt {
		b.WriteString("Alt+")
	}
	if c.Shift {
		b.WriteString("Shift+")
	}
	b.WriteString(scoreScancodeName(c.Key))
	return b.String()
}

// scoreScancodeName is SDL's name for a scancode, which is stable across
// keyboard layouts.
func scoreScancodeName(sc sdl.Scancode) string {
	if n := sdl.GetScancodeName(sc); n != "" {
		return n
	}
	return fmt.Sprintf("Key %d", sc)
}

// parseScoreChord reads what String writes (modifiers in any order, any case).
func parseScoreChord(s string) (scoreChord, error) {
	var c scoreChord
	rest := strings.TrimSpace(s)
	for {
		low := strings.ToLower(rest)
		switch {
		case strings.HasPrefix(low, "ctrl+") && len(rest) > 5:
			c.Ctrl, rest = true, rest[5:]
		case strings.HasPrefix(low, "alt+") && len(rest) > 4:
			c.Alt, rest = true, rest[4:]
		case strings.HasPrefix(low, "shift+") && len(rest) > 6:
			c.Shift, rest = true, rest[6:]
		default:
			sc := sdl.GetScancodeFromName(rest)
			if sc == sdl.SCANCODE_UNKNOWN {
				return c, fmt.Errorf("no key %q", rest)
			}
			c.Key = sc
			return c, nil
		}
	}
}

// scoreModifierKey reports a key that is only a modifier: pressed alone it
// is waited past, not bound.
func scoreModifierKey(sc sdl.Scancode) bool {
	switch sc {
	case sdl.SCANCODE_LCTRL, sdl.SCANCODE_RCTRL, sdl.SCANCODE_LSHIFT, sdl.SCANCODE_RSHIFT,
		sdl.SCANCODE_LALT, sdl.SCANCODE_RALT, sdl.SCANCODE_LGUI, sdl.SCANCODE_RGUI,
		sdl.SCANCODE_NUMLOCKCLEAR, sdl.SCANCODE_CAPSLOCK:
		return true
	}
	return false
}

// scoreReservedKey is a key the editor never lets the Score keymap take:
// F1 is the reference everywhere.
func scoreReservedKey(sc sdl.Scancode) bool { return sc == sdl.SCANCODE_F1 }

// ---------- keymaps ----------

// scoreKeymap is every command's chords.
type scoreKeymap struct {
	Name  string
	binds [scCmdCount][]scoreChord
	// lookup is the chord → command index, built from binds.
	lookup map[scoreChord]scoreCmd
}

func (k *scoreKeymap) bind(c scoreCmd, chords ...scoreChord) {
	k.binds[c] = append(k.binds[c], chords...)
}

// index rebuilds the lookup. A chord bound twice goes to the first command.
func (k *scoreKeymap) index() {
	k.lookup = map[scoreChord]scoreCmd{}
	for c := scoreCmd(1); c < scCmdCount; c++ {
		for _, ch := range k.binds[c] {
			if _, taken := k.lookup[ch]; !taken {
				k.lookup[ch] = c
			}
		}
	}
}

// command is the command a chord runs, or scNone.
func (k *scoreKeymap) command(ch scoreChord) scoreCmd {
	if k.lookup == nil {
		k.index()
	}
	return k.lookup[ch]
}

// chords are a command's chords.
func (k *scoreKeymap) chords(c scoreCmd) []scoreChord { return k.binds[c] }

// label is a command's chords joined for display, or "" when unbound.
func (k *scoreKeymap) label(c scoreCmd) string {
	var parts []string
	for _, ch := range k.binds[c] {
		parts = append(parts, ch.String())
	}
	return strings.Join(parts, ", ")
}

// rebind sets a command's chords, taking each from any other command that
// had it, and returns the commands it was taken from.
func (k *scoreKeymap) rebind(c scoreCmd, chords []scoreChord) []scoreCmd {
	var from []scoreCmd
	for _, ch := range chords {
		for o := scoreCmd(1); o < scCmdCount; o++ {
			if o == c {
				continue
			}
			kept := k.binds[o][:0:0]
			for _, oc := range k.binds[o] {
				if oc == ch {
					from = append(from, o)
					continue
				}
				kept = append(kept, oc)
			}
			k.binds[o] = kept
		}
	}
	k.binds[c] = append([]scoreChord(nil), chords...)
	k.index()
	return from
}

// clone copies a keymap.
func (k *scoreKeymap) clone() *scoreKeymap {
	c := &scoreKeymap{Name: k.Name}
	for i := range k.binds {
		c.binds[i] = append([]scoreChord(nil), k.binds[i]...)
	}
	c.index()
	return c
}

// overrides are the commands whose chords differ from base, by name.
func (k *scoreKeymap) overrides(base *scoreKeymap) map[string][]string {
	out := map[string][]string{}
	for c := scoreCmd(1); c < scCmdCount; c++ {
		if chordsEqual(k.binds[c], base.binds[c]) {
			continue
		}
		names := []string{}
		for _, ch := range k.binds[c] {
			names = append(names, ch.String())
		}
		out[scoreCmdInfos[c].Name] = names
	}
	return out
}

// applyOverrides lays saved rebindings over a preset. Unknown commands and
// chords that do not parse are reported and skipped.
func (k *scoreKeymap) applyOverrides(ov map[string][]string) []string {
	var problems []string
	names := make([]string, 0, len(ov))
	for n := range ov {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c, ok := scoreCmdByName(n)
		if !ok {
			problems = append(problems, fmt.Sprintf("no command %q", n))
			continue
		}
		var chords []scoreChord
		for _, s := range ov[n] {
			ch, err := parseScoreChord(s)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", n, err))
				continue
			}
			chords = append(chords, ch)
		}
		k.rebind(c, chords)
	}
	return problems
}

// conflicts lists chords bound to more than one command.
func (k *scoreKeymap) conflicts() map[scoreChord][]scoreCmd {
	by := map[scoreChord][]scoreCmd{}
	for c := scoreCmd(1); c < scCmdCount; c++ {
		for _, ch := range k.binds[c] {
			by[ch] = append(by[ch], c)
		}
	}
	for ch, cs := range by {
		if len(cs) < 2 {
			delete(by, ch)
		}
	}
	return by
}

func chordsEqual(a, b []scoreChord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// key, ctrl, shift, alt and their mixes, for writing the presets.
func key(sc sdl.Scancode) scoreChord       { return scoreChord{Key: sc} }
func ctrlKey(sc sdl.Scancode) scoreChord   { return scoreChord{Key: sc, Ctrl: true} }
func shiftKey(sc sdl.Scancode) scoreChord  { return scoreChord{Key: sc, Shift: true} }
func altKey(sc sdl.Scancode) scoreChord    { return scoreChord{Key: sc, Alt: true} }
func ctrlAlt(sc sdl.Scancode) scoreChord   { return scoreChord{Key: sc, Ctrl: true, Alt: true} }
func ctrlShift(sc sdl.Scancode) scoreChord { return scoreChord{Key: sc, Ctrl: true, Shift: true} }
func altShift(sc sdl.Scancode) scoreChord  { return scoreChord{Key: sc, Alt: true, Shift: true} }

var (
	scoreLetterKeys = [7]sdl.Scancode{sdl.SCANCODE_C, sdl.SCANCODE_D, sdl.SCANCODE_E, sdl.SCANCODE_F, sdl.SCANCODE_G, sdl.SCANCODE_A, sdl.SCANCODE_B}
	scoreDigitKeys  = [10]sdl.Scancode{sdl.SCANCODE_0, sdl.SCANCODE_1, sdl.SCANCODE_2, sdl.SCANCODE_3, sdl.SCANCODE_4,
		sdl.SCANCODE_5, sdl.SCANCODE_6, sdl.SCANCODE_7, sdl.SCANCODE_8, sdl.SCANCODE_9}
	scorePadKeys = [10]sdl.Scancode{sdl.SCANCODE_KP_0, sdl.SCANCODE_KP_1, sdl.SCANCODE_KP_2, sdl.SCANCODE_KP_3, sdl.SCANCODE_KP_4,
		sdl.SCANCODE_KP_5, sdl.SCANCODE_KP_6, sdl.SCANCODE_KP_7, sdl.SCANCODE_KP_8, sdl.SCANCODE_KP_9}
)

// scoreCommonBinds are the keys both presets share: the editor's own
// file, clipboard and view keys.
func scoreCommonBinds(k *scoreKeymap) {
	k.bind(scSave, ctrlKey(sdl.SCANCODE_S))
	k.bind(scSaveAs, ctrlShift(sdl.SCANCODE_S))
	k.bind(scOpen, ctrlKey(sdl.SCANCODE_O))
	k.bind(scCopy, ctrlKey(sdl.SCANCODE_C))
	k.bind(scPaste, ctrlKey(sdl.SCANCODE_V))
	k.bind(scSelectAll, ctrlKey(sdl.SCANCODE_A))
	k.bind(scUndo, ctrlKey(sdl.SCANCODE_Z))
	k.bind(scRedo, ctrlKey(sdl.SCANCODE_Y), ctrlShift(sdl.SCANCODE_Z))
	k.bind(scZoomIn, ctrlKey(sdl.SCANCODE_EQUALS), ctrlKey(sdl.SCANCODE_KP_PLUS))
	k.bind(scZoomOut, ctrlKey(sdl.SCANCODE_MINUS), ctrlKey(sdl.SCANCODE_KP_MINUS))
	k.bind(scZoomReset, ctrlKey(sdl.SCANCODE_0))
	k.bind(scEscape, key(sdl.SCANCODE_ESCAPE))
	k.bind(scPrev, key(sdl.SCANCODE_LEFT))
	k.bind(scNext, key(sdl.SCANCODE_RIGHT))
	k.bind(scDelete, key(sdl.SCANCODE_DELETE), key(sdl.SCANCODE_BACKSPACE))
	k.bind(scNoteInput, key(sdl.SCANCODE_N))
	k.bind(scRepeat, key(sdl.SCANCODE_R))
	k.bind(scPlay, key(sdl.SCANCODE_SPACE))
	k.bind(scPlayFrom, key(sdl.SCANCODE_P))
	k.bind(scLoop, key(sdl.SCANCODE_L))
	k.bind(scLoopToggle, shiftKey(sdl.SCANCODE_L))
	k.bind(scToStart, key(sdl.SCANCODE_HOME))
	for i := 0; i < 7; i++ {
		k.bind(scLetterC+scoreCmd(i), key(scoreLetterKeys[i]))
	}
	for v := 0; v < 4; v++ {
		k.bind(scVoice1+scoreCmd(v), ctrlAlt(scoreDigitKeys[v+1]))
	}
	for n := 2; n <= 9; n++ {
		k.bind(scTuplet2+scoreCmd(n-2), ctrlKey(scoreDigitKeys[n]))
	}
	k.bind(scSemiUp, key(sdl.SCANCODE_UP))
	k.bind(scSemiDown, key(sdl.SCANCODE_DOWN))
	k.bind(scOctUp, ctrlKey(sdl.SCANCODE_UP))
	k.bind(scOctDown, ctrlKey(sdl.SCANCODE_DOWN))
	// Expression: S, H and Shift+H as both programs have them. G is the
	// note G (a letter in note input, a pitch change out of it), so the
	// glissando is Alt+G.
	k.bind(scSlur, key(sdl.SCANCODE_S))
	k.bind(scCresc, key(sdl.SCANCODE_H))
	k.bind(scDim, shiftKey(sdl.SCANCODE_H))
	k.bind(scGliss, altKey(sdl.SCANCODE_G))
	k.bind(scExpression, ctrlKey(sdl.SCANCODE_E))
	k.bind(scTempoText, ctrlAlt(sdl.SCANCODE_T))
	k.bind(scMarksPanel, ctrlAlt(sdl.SCANCODE_P))
	k.bind(scConcert, ctrlShift(sdl.SCANCODE_D))
	k.bind(scLane, ctrlShift(sdl.SCANCODE_L))
	k.bind(scRehearsal, ctrlKey(sdl.SCANCODE_R))
}

// scoreSibeliusKeymap is the default: Sibelius's keys, as SPEC.md's input
// table has them.
func scoreSibeliusKeymap() *scoreKeymap {
	k := &scoreKeymap{Name: "Sibelius"}
	scoreCommonBinds(k)
	for i := 0; i < 7; i++ {
		k.bind(scAddC+scoreCmd(i), shiftKey(scoreLetterKeys[i]))
	}
	for n := 2; n <= 9; n++ {
		k.bind(scInt2+scoreCmd(n-2), key(scoreDigitKeys[n]))
		k.bind(scBelow2+scoreCmd(n-2), shiftKey(scoreDigitKeys[n]))
	}
	k.bind(scSharp, key(sdl.SCANCODE_EQUALS), shiftKey(sdl.SCANCODE_EQUALS))
	k.bind(scFlat, key(sdl.SCANCODE_MINUS))
	k.bind(scNatural, key(sdl.SCANCODE_0))
	// The keypad's 7, 8, 9 and + follow its layout.
	k.bind(scKeypad7, key(sdl.SCANCODE_KP_7))
	k.bind(scKeypad8, key(sdl.SCANCODE_KP_8))
	k.bind(scKeypad9, key(sdl.SCANCODE_KP_9))
	k.bind(scKeypadPlus, key(sdl.SCANCODE_KP_PLUS))
	// Articulations on the main keys too: . ' > _ and " (marcato: Shift+6,
	// the ^, is the sixth below).
	k.bind(scStaccato, key(sdl.SCANCODE_PERIOD))
	k.bind(scStaccatissimo, key(sdl.SCANCODE_APOSTROPHE))
	k.bind(scAccent, shiftKey(sdl.SCANCODE_PERIOD))
	k.bind(scTenuto, shiftKey(sdl.SCANCODE_MINUS))
	k.bind(scMarcato, shiftKey(sdl.SCANCODE_APOSTROPHE))
	k.bind(scStepUp, altKey(sdl.SCANCODE_UP))
	k.bind(scStepDown, altKey(sdl.SCANCODE_DOWN))
	k.bind(scRespell, key(sdl.SCANCODE_RETURN))
	for i := 0; i < 6; i++ {
		k.bind(scKeypad1+scoreCmd(i), key(scorePadKeys[i+1]), altKey(scoreDigitKeys[i+1]))
	}
	k.bind(scLayout, key(sdl.SCANCODE_F8), key(sdl.SCANCODE_KP_DIVIDE))
	k.bind(scDot, key(sdl.SCANCODE_KP_PERIOD), altKey(sdl.SCANCODE_PERIOD))
	k.bind(scRest, key(sdl.SCANCODE_KP_0), altKey(sdl.SCANCODE_0))
	k.bind(scTie, key(sdl.SCANCODE_KP_ENTER), key(sdl.SCANCODE_T))
	k.bind(scFlip, key(sdl.SCANCODE_X), key(sdl.SCANCODE_KP_MINUS))
	k.bind(scVoiceCycle, key(sdl.SCANCODE_KP_MULTIPLY))
	k.bind(scKeySig, key(sdl.SCANCODE_K))
	k.bind(scTimeSig, ctrlShift(sdl.SCANCODE_T))
	k.bind(scClef, key(sdl.SCANCODE_Q))
	k.bind(scKeypadPanel, ctrlAlt(sdl.SCANCODE_K))
	k.bind(scMixer, key(sdl.SCANCODE_M))
	k.index()
	return k
}

// scoreMuseScoreKeymap is MuseScore's: digits are durations (1 a 64th ... 7 a
// whole, 8 a breve), Alt+digit adds an interval above (Alt+Shift below),
// Shift+letter adds to the chord, J respells, Alt+Shift+arrows move by a
// step, T ties, 0 rests, X flips.
func scoreMuseScoreKeymap() *scoreKeymap {
	k := &scoreKeymap{Name: "MuseScore"}
	scoreCommonBinds(k)
	for i := 0; i < 7; i++ {
		k.bind(scAddC+scoreCmd(i), shiftKey(scoreLetterKeys[i]))
	}
	for n := 2; n <= 9; n++ {
		k.bind(scInt2+scoreCmd(n-2), altKey(scoreDigitKeys[n]))
		k.bind(scBelow2+scoreCmd(n-2), altShift(scoreDigitKeys[n]))
	}
	// 1 = 64th (scDur64 is index 2) ... 7 = whole, 8 = breve.
	for d := 1; d <= 8; d++ {
		c := scDur256 + scoreCmd(1+d) // 64th at d=1
		k.bind(c, key(scoreDigitKeys[d]), key(scorePadKeys[d]))
	}
	k.bind(scSharp, shiftKey(sdl.SCANCODE_UP))
	k.bind(scFlat, shiftKey(sdl.SCANCODE_DOWN))
	k.bind(scNatural, shiftKey(sdl.SCANCODE_N))
	k.bind(scStepUp, altShift(sdl.SCANCODE_UP))
	k.bind(scStepDown, altShift(sdl.SCANCODE_DOWN))
	k.bind(scRespell, key(sdl.SCANCODE_J))
	k.bind(scDot, key(sdl.SCANCODE_PERIOD), key(sdl.SCANCODE_KP_PERIOD))
	k.bind(scRest, key(sdl.SCANCODE_0), key(sdl.SCANCODE_KP_0))
	k.bind(scTie, key(sdl.SCANCODE_T), key(sdl.SCANCODE_KP_PLUS))
	k.bind(scFlip, key(sdl.SCANCODE_X))
	k.bind(scLayout, key(sdl.SCANCODE_F8))
	k.bind(scKeySig, ctrlShift(sdl.SCANCODE_K))
	k.bind(scTimeSig, ctrlShift(sdl.SCANCODE_T))
	k.bind(scClef, ctrlShift(sdl.SCANCODE_C))
	k.bind(scKeypadPanel, ctrlAlt(sdl.SCANCODE_K))
	k.bind(scMixer, key(sdl.SCANCODE_F10))
	// MuseScore's articulation keys (its Shift+N, tenuto, is the natural
	// here, so tenuto is Shift+T).
	k.bind(scStaccato, shiftKey(sdl.SCANCODE_S))
	k.bind(scAccent, shiftKey(sdl.SCANCODE_V))
	k.bind(scMarcato, shiftKey(sdl.SCANCODE_O))
	k.bind(scTenuto, shiftKey(sdl.SCANCODE_T))
	k.bind(scStaccatissimo, shiftKey(sdl.SCANCODE_APOSTROPHE))
	k.index()
	return k
}

// scoreKeymapPresets are the shipped keymaps, by name; the first is the
// default.
var scoreKeymapPresets = []struct {
	Name  string
	Build func() *scoreKeymap
}{
	{"Sibelius", scoreSibeliusKeymap},
	{"MuseScore", scoreMuseScoreKeymap},
}

// scorePreset builds a preset by name (the default for an unknown one).
func scorePreset(name string) *scoreKeymap {
	for _, p := range scoreKeymapPresets {
		if strings.EqualFold(p.Name, name) {
			return p.Build()
		}
	}
	return scoreKeymapPresets[0].Build()
}
