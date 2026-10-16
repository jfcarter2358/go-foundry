package editor

// editor_score_lines.go — the Score editor's lines and mixer on the editing
// model, with no ImGui in it: adding a line for an instrument, duplicating
// one with its notes or empty, removing, reordering and renaming lines,
// swapping a line's instrument (while it plays: the notes do not change, the
// next note plays the new one), and the mixer — fader, pan, sends, mute,
// solo, the buses' returns and the master.
//
// Two kinds of change. Lines added, removed, moved, renamed or duplicated
// change what is performed, so they go through change (one undo step, Rev
// moves, the window hands the player a fresh program). The mixer and an
// instrument swap only change how the same performance is played: they go
// through mixChange, which moves MixRev instead, and the window sends them
// to the player live (SetMix, SetGain, SetReturns, MuteIndex,
// SetInstrument) so a fader can be dragged while the music plays without
// the score being interpreted every frame. Mixer settings are part of the
// file, so they are undoable too: one drag of one control is one step.

import (
	"fmt"
	"math"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/play"
	"foundry/internal/tracker"
)

// The mixer's ranges. A fader of 1 is unity (0 dB); the top is +6 dB.
const (
	scoreFaderMax  = float32(2)
	scoreMasterMin = float32(0.01) // a file's gain of 0 would read as unity
)

// line is line i, or nil.
func (e *scoreState) line(i int) *score.Line {
	if i < 0 || i >= len(e.Score.Lines) {
		return nil
	}
	return e.Score.Lines[i]
}

// mixChange runs a mixer edit as an undo step — the same step as the
// newest one when that was this control too (key "" is always its own
// step) — and moves MixRev.
func (e *scoreState) mixChange(key string, f func()) {
	if key == "" || key != e.mixKey {
		e.undo.push(scoreSnapshot{Score: e.Score.Clone(), Sel: e.Sel})
		e.redo.clear()
	}
	f()
	e.mixKey = key
	e.MixRev++
}

// endMixGesture closes a drag: the next move of the control is a new step.
func (e *scoreState) endMixGesture() { e.mixKey = "" }

// setVolume is a line's fader.
func (e *scoreState) setVolume(i int, v float32) {
	l := e.line(i)
	if l == nil {
		return
	}
	v = clampF32(v, 0, scoreFaderMax)
	if v == l.Volume {
		return
	}
	e.mixChange(fmt.Sprintf("vol%d", i), func() { l.Volume = v })
	e.Status = fmt.Sprintf("%s: %s", e.lineName(i), scoreDB(v))
}

// setPan is a line's pan, -1 left .. 1 right.
func (e *scoreState) setPan(i int, v float32) {
	l := e.line(i)
	if l == nil {
		return
	}
	v = clampF32(v, -1, 1)
	if v == l.Pan {
		return
	}
	e.mixChange(fmt.Sprintf("pan%d", i), func() { l.Pan = v })
	e.Status = fmt.Sprintf("%s: pan %s", e.lineName(i), scorePanText(v))
}

// setSend is a line's send to bus A (0, the reverb) or B (1, the delay).
func (e *scoreState) setSend(i, bus int, v float32) {
	l := e.line(i)
	if l == nil {
		return
	}
	v = clampF32(v, 0, 1)
	p := &l.SendA
	if bus == 1 {
		p = &l.SendB
	}
	if v == *p {
		return
	}
	e.mixChange(fmt.Sprintf("send%d.%d", bus, i), func() { *p = v })
	e.Status = fmt.Sprintf("%s: %s send %.0f%%", e.lineName(i), scoreBusName(bus), v*100)
}

// setReturn is a bus's return level.
func (e *scoreState) setReturn(bus int, v float32) {
	v = clampF32(v, 0, 1)
	p := &e.Score.Buses.A.Return
	if bus == 1 {
		p = &e.Score.Buses.B.Return
	}
	if v == *p {
		return
	}
	e.mixChange(fmt.Sprintf("return%d", bus), func() { *p = v })
	e.Status = fmt.Sprintf("%s return: %s", scoreBusName(bus), scoreDB(v))
}

// setGain is the master fader.
func (e *scoreState) setGain(v float32) {
	v = clampF32(v, scoreMasterMin, scoreFaderMax)
	if v == e.Score.Gain {
		return
	}
	e.mixChange("gain", func() { e.Score.Gain = v })
	e.Status = "Master: " + scoreDB(v)
}

// toggleMute and toggleSolo are a line's M and S.
func (e *scoreState) toggleMute(i int) {
	l := e.line(i)
	if l == nil {
		return
	}
	e.mixChange("", func() { l.Mute = !l.Mute })
	e.Status = e.lineName(i) + scoreOnOff(l.Mute, " muted", " unmuted")
}

func (e *scoreState) toggleSolo(i int) {
	l := e.line(i)
	if l == nil {
		return
	}
	e.mixChange("", func() { l.Solo = !l.Solo })
	e.Status = e.lineName(i) + scoreOnOff(l.Solo, " soloed", " no longer soloed")
}

// setInstrument plays line i on another library instrument. The notes do
// not change; while the score plays, the next note is the new instrument's
// (what is sounding finishes on the old one).
func (e *scoreState) setInstrument(i int, name string, playing bool) {
	l := e.line(i)
	if l == nil || name == "" {
		return
	}
	if name == l.Instrument {
		e.Status = e.lineName(i) + " already plays " + scoreInstrumentLabel(name)
		return
	}
	// The staff takes the new instrument's transposition (a clarinet's
	// line is written a tone up): the sound is the same, the page follows.
	// (The swap loads it anyway, and the loader caches.)
	in, err := play.LoadInstrument(name)
	retransposed := false
	e.mixChange("", func() {
		l.Instrument = name
		if err == nil && in != nil {
			retransposed = edit.Retranspose(l, in.Playback)
		}
	})
	if retransposed {
		e.laneRev++ // engrave again; the notes and the program are unchanged
	}
	e.op(scorePlayerOp{Kind: scoreOpSwap, Line: i, Name: name})
	e.Status = fmt.Sprintf("%s now plays %s", e.lineName(i), scoreInstrumentLabel(name))
	if playing {
		e.Status += ", from the next note"
	}
	e.Status += ". The notes did not change"
	if retransposed {
		e.Status += " (written at the new instrument's transposition)"
	}
	e.Status += "."
}

// ---------- whole lines ----------

// remapLines moves the selection's lines through f (an old index to its
// new one, -1 for a line that went), clearing a selection that lost one.
func (e *scoreState) remapLines(f func(int) int) {
	e.LaneSel = scoreLaneSel{Index: -1}
	q := &e.Sel
	switch q.Kind {
	case scoreSelEvent, scoreSelCaret:
		if n := f(q.Loc.Line); n >= 0 {
			q.Loc.Line = n
			return
		}
	case scoreSelBars:
		a, b := f(q.LineA), f(q.LineB)
		if a >= 0 && b >= a && b-a == q.LineB-q.LineA {
			q.LineA, q.LineB = a, b
			return
		}
	default:
		return
	}
	e.Sel = scoreSel{}
}

// selectLine selects every bar of a line: a click on its name.
func (e *scoreState) selectLine(i int) {
	if e.line(i) == nil {
		return
	}
	e.Sel = scoreSel{Kind: scoreSelBars, LineA: i, LineB: i, BarA: 1, BarB: e.Score.Measures()}
	e.Status = e.describeSel()
}

// addLine puts a new line for a library instrument at index at: its
// clef, grand staff or percussion staff and transposition as the
// instrument's playback defaults say, its display name (unique).
func (e *scoreState) addLine(at int, instrument string) bool {
	name, pb := scoreInstrumentLabel(instrument), tracker.Playback{}
	if in, err := play.LoadInstrument(instrument); err == nil && in != nil {
		pb = in.Playback
		if in.Name != "" {
			name = in.Name
		}
	}
	return e.insertLine(at, edit.NewLineFor(name, instrument, pb))
}

// insertLine adds a line built elsewhere (a template's grand staff, say).
func (e *scoreState) insertLine(at int, l *score.Line) bool {
	var i int
	if !e.change(func() error {
		i = edit.AddLine(e.Score, at, l)
		e.remapLines(func(o int) int {
			if o >= i {
				return o + 1
			}
			return o
		})
		return nil
	}) {
		return false
	}
	e.Sel = scoreSel{Kind: scoreSelBars, LineA: i, LineB: i, BarA: 1, BarB: 1}
	kind := "a staff"
	switch {
	case len(l.Staves) > 1:
		kind = "a grand staff"
	case len(l.Staves) == 1 && l.Staves[0].Clef == "percussion":
		kind = "a percussion staff"
	}
	e.Status = fmt.Sprintf("Added %s (%s, %s)", e.lineName(i), scoreInstrumentLabel(l.Instrument), kind)
	return true
}

// duplicateLine copies line i just below itself, with its notes or empty
// (the same instrument and mixer settings), and selects the copy.
func (e *scoreState) duplicateLine(i int, withNotes bool) bool {
	var n int
	if !e.change(func() error {
		var err error
		n, err = edit.DuplicateLine(e.Score, i, withNotes)
		return err
	}) {
		return false
	}
	e.Sel = scoreSel{Kind: scoreSelBars, LineA: n, LineB: n, BarA: 1, BarB: e.Score.Measures()}
	if withNotes {
		e.Status = fmt.Sprintf("Duplicated %s with its notes, instrument and mixer settings as %s", e.lineName(i), e.lineName(n))
	} else {
		e.Status = fmt.Sprintf("Added %s: %s's instrument and mixer settings, no notes", e.lineName(n), e.lineName(i))
	}
	return true
}

// removeLine takes line i out of the score.
func (e *scoreState) removeLine(i int) bool {
	name := e.lineName(i)
	if !e.change(func() error {
		if err := edit.RemoveLine(e.Score, i); err != nil {
			return err
		}
		e.remapLines(func(o int) int {
			switch {
			case o == i:
				return -1
			case o > i:
				return o - 1
			}
			return o
		})
		return nil
	}) {
		return false
	}
	e.Status = "Removed " + name + " (Ctrl+Z brings it back)"
	return true
}

// moveLine moves line i up (dir < 0) or down one place; the selection
// moves with it.
func (e *scoreState) moveLine(i, dir int) bool {
	to := i + dir
	if !e.change(func() error {
		if to < 0 || to >= len(e.Score.Lines) {
			return fmt.Errorf("%s is already at the %s", e.lineName(i), scoreOnOff(dir < 0, "top", "bottom"))
		}
		if err := edit.MoveLine(e.Score, i, to); err != nil {
			return err
		}
		e.remapLines(func(o int) int {
			switch o {
			case i:
				return to
			case to:
				return i
			}
			return o
		})
		return nil
	}) {
		return false
	}
	e.Status = fmt.Sprintf("Moved %s %s", e.lineName(to), scoreOnOff(dir < 0, "up", "down"))
	return true
}

// renameLine renames line i (bindings that name it follow).
func (e *scoreState) renameLine(i int, name string) bool {
	if e.line(i) == nil {
		return false
	}
	old := e.lineName(i)
	raw := e.Score.Lines[i].Name
	if !e.change(func() error { return edit.RenameLine(e.Score, i, name) }) {
		return false
	}
	// An open dynamics lane goes with the line (lanes are kept by name).
	if e.Lanes[raw] {
		delete(e.Lanes, raw)
		e.Lanes[e.Score.Lines[i].Name] = true
		e.laneRev++
	}
	e.Status = fmt.Sprintf("Renamed %s to %s", old, e.lineName(i))
	return true
}

// ---------- words ----------

// scoreInstrumentLabel is a library name as a person reads it:
// "strings/violin_section" is "Violin section".
func scoreInstrumentLabel(name string) string {
	if name == "" {
		return "(no instrument)"
	}
	base := name[strings.LastIndex(name, "/")+1:]
	base = strings.ReplaceAll(base, "_", " ")
	if base == "" {
		return name
	}
	return strings.ToUpper(base[:1]) + base[1:]
}

// scoreDB is a level in decibels: "-6.0 dB", "-inf dB".
func scoreDB(v float32) string {
	if v <= 0.0001 {
		return "-inf dB"
	}
	return fmt.Sprintf("%+.1f dB", 20*math.Log10(float64(v)))
}

// scorePanText is "C", "L40", "R100".
func scorePanText(v float32) string {
	switch {
	case v < -0.005:
		return fmt.Sprintf("L%.0f", -v*100)
	case v > 0.005:
		return fmt.Sprintf("R%.0f", v*100)
	}
	return "C"
}

// scoreBusName is bus A's or B's name as the mixer shows it.
func scoreBusName(bus int) string {
	if bus == 1 {
		return "Delay"
	}
	return "Reverb"
}

func scoreOnOff(on bool, yes, no string) string {
	if on {
		return yes
	}
	return no
}
