package editor

// editor_score_edit.go — the Score editor's editing model, with no ImGui in it.
//
// What a key or a click means in the Score editor is a method on scoreState:
// the selection (an event, or a block of bars over a run of lines), note input
// and where the next note goes, the current duration, voice and keypad
// layout, the clipboard, and the snapshot undo stack. The music itself is
// changed by internal/score/edit; this file decides which of its operations a
// command is and keeps the selection, the status line and the auditions in
// step. The window, the view and the key handler (editor_score_window.go,
// _view.go, _input.go) only decide which method to call, which is what lets
// editor_score_test.go drive every command with no GL context or ImGui frame.
//
// Pitches are stored at concert pitch. A transposing staff is shown — and so
// typed, clicked and sharpened — at written pitch unless the view is in
// concert pitch, so every pitch command goes through toWritten/toConcert: a
// natural pressed on a written C of a Bb clarinet leaves it C, where on the
// sounding Bb it would have made a B.

import (
	"fmt"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/engrave"
	"foundry/internal/score/interpret"
)

// scoreSelKind is what is selected.
type scoreSelKind uint8

const (
	scoreSelNone scoreSelKind = iota
	// scoreSelEvent is one note, chord or rest (Loc), or one note of a chord
	// (Note >= 0).
	scoreSelEvent
	// scoreSelBars is bars BarA..BarB of lines LineA..LineB.
	scoreSelBars
	// scoreSelCaret is a place in a voice with no event selected: Loc's
	// line, staff, voice and bar (Event -1) and At into the bar. It is where
	// note input goes on after a voice change, between two voices' notes.
	scoreSelCaret
)

// scoreSel is the selection.
type scoreSel struct {
	Kind         scoreSelKind
	Loc          edit.Loc
	Note         int
	LineA, LineB int
	BarA, BarB   int
	// At is a caret's offset into its bar.
	At score.Frac
	// Staff is the staff of LineA a bar click landed on: where note input
	// into selected bars starts (a piano's bass staff, say).
	Staff int
}

// scoreSnapshot is one undo step: the whole score and the selection.
type scoreSnapshot struct {
	Score *score.Score
	Sel   scoreSel
}

// scoreAudition is notes to play as a click or an entry lands: a line's
// instrument, MIDI notes.
type scoreAudition struct {
	Line int
	MIDI []int
}

// scoreState is everything the Score editor knows about the score being
// edited. Package-level as scEd; a type so tests can build a fresh one.
type scoreState struct {
	Score *score.Score
	// Name is the library name (a path under sounds/, no extension); ""
	// until saved.
	Name string

	Sel scoreSel

	// Input is note input: clicks and letters write notes.
	Input bool
	// Dur is the value notes are written at.
	Dur score.Duration
	// Voice is the voice notes go into, 0..3.
	Voice int
	// Layout2 is the keypad's second layout (64th, 128th, 256th, breve).
	Layout2 bool
	// Armed is an accidental chosen with nothing selected: the next note
	// written takes it.
	Armed    bool
	ArmedAcc edit.AccKind
	// Grace is the kind of grace note note input writes; NotGrace for
	// normal notes (editor_score_marks.go).
	Grace score.Grace

	// Lanes are the lines (by name) whose dynamics lane is open, LaneSel
	// the override selected in one, laneDrag a drag in one.
	Lanes    map[string]bool
	LaneSel  scoreLaneSel
	laneDrag scoreLaneDrag
	// laneRev counts what the layout must see that is not a note edit:
	// lanes opening and closing (it leaves room for them), a swap's new
	// transposition; laneLive counts a drag's live changes (the window paces the
	// player's updates by it).
	laneRev, laneLive uint64
	// laneCurveCache is every line's dynamics as written, for the lanes.
	laneCurveCache []interpret.LineDynamics
	laneCurveRev   uint64
	laneCurveLive  uint64
	laneCurveScore *score.Score
	laneCurveSet   bool

	// The clipboard: bars, or one event.
	Clip      *edit.Clip
	ClipEvent *score.Event

	// Rev counts edits — the save chip's and the player's change signal.
	Rev uint64
	// MixRev counts mixer changes (faders, pan, sends, returns, the master,
	// mute, solo, an instrument swap): the window hands those to the player
	// live (play.Player.SetMix …) instead of preparing the score again.
	MixRev uint64
	// mixKey is the mixer control the newest undo step is a drag of: one
	// drag of one fader is one step (editor_score_lines.go).
	mixKey string

	// Transport is the play head, the loop, the tempo slider and follow.
	Transport scoreTransport
	// ops are requests for the player the window has not taken yet.
	ops []scorePlayerOp
	// perfCache is the score interpreted, for the transport's arithmetic.
	perfCache    *interpret.Performance
	perfScore    *score.Score
	perfRev      uint64
	perfComputed bool

	undo, redo boundedStack[scoreSnapshot]

	// Status is the message on the toolbar row.
	Status string
	// auditions are notes the last command wants heard; the window plays
	// and clears them.
	auditions []scoreAudition
	// Reveal asks the view to scroll the selection into sight.
	Reveal bool

	// TL is the Timeline tab's state: the grid, the tools, the roll's and
	// the lanes' selections (editor_score_timeline_edit.go).
	TL scoreTimeline
}

// newScoreState starts an editor on a score.
func newScoreState(s *score.Score) *scoreState {
	e := &scoreState{Dur: score.Duration{Base: score.Quarter}, Transport: newScoreTransport(), LaneSel: scoreLaneSel{Index: -1}, TL: newScoreTimeline()}
	e.setScore(s)
	return e
}

// setScore replaces the score; history goes with the old one.
func (e *scoreState) setScore(s *score.Score) {
	e.Score = s
	e.Sel = scoreSel{}
	e.undo.clear()
	e.redo.clear()
	e.Input = false
	e.Armed = false
	e.Grace = score.NotGrace
	e.LaneSel, e.laneDrag = scoreLaneSel{Index: -1}, scoreLaneDrag{}
	e.TL.RollSel, e.TL.AutoSel, e.TL.drag, e.TL.RollLine = nil, scoreAutoSel{Index: -1}, scoreAutoDrag{}, 0
	e.mixKey = ""
	e.Transport.Loop, e.Transport.Head = scoreLoop{}, 0
	e.Rev++
}

func (e *scoreState) touch() { e.Rev++ }

// ---------- undo ----------

// change runs an edit as one undo step. The score is snapshotted first; an
// edit that fails is rolled back to the snapshot, so a refused operation
// leaves nothing behind (not even the empty voice it made on the way), and
// its error is the status.
func (e *scoreState) change(f func() error) bool {
	snap := scoreSnapshot{Score: e.Score.Clone(), Sel: e.Sel}
	if err := f(); err != nil {
		e.Score, e.Sel = snap.Score, snap.Sel
		e.Status = scoreErrText(err)
		return false
	}
	e.undo.push(snap)
	e.redo.clear()
	e.touch()
	e.mixKey = ""
	e.Reveal = true
	e.Transport.Loop = scoreFitLoop(e.Score, e.Transport.Loop)
	return true
}

func scoreErrText(err error) string {
	s := err.Error()
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// restore puts a snapshot back. Concert pitch is a view switch, not an
// edit (toggleConcert pushes no step), so it stays as it is.
func (e *scoreState) restore(snap scoreSnapshot) {
	concert := e.Score.Concert
	e.Score, e.Sel = snap.Score, snap.Sel
	e.Score.Concert = concert
}

func (e *scoreState) canUndo() bool { return e.undo.len() > 0 }
func (e *scoreState) canRedo() bool { return e.redo.len() > 0 }

func (e *scoreState) undoStep() bool {
	snap, ok := e.undo.pop()
	if !ok {
		e.Status = "Nothing to undo"
		return false
	}
	e.redo.push(scoreSnapshot{Score: e.Score.Clone(), Sel: e.Sel})
	e.restore(snap)
	e.touch()
	e.mixKey = ""
	e.Transport.Loop = scoreFitLoop(e.Score, e.Transport.Loop)
	e.Status = "Undo"
	return true
}

func (e *scoreState) redoStep() bool {
	snap, ok := e.redo.pop()
	if !ok {
		e.Status = "Nothing to redo"
		return false
	}
	e.undo.push(scoreSnapshot{Score: e.Score.Clone(), Sel: e.Sel})
	e.restore(snap)
	e.touch()
	e.mixKey = ""
	e.Transport.Loop = scoreFitLoop(e.Score, e.Transport.Loop)
	e.Status = "Redo"
	return true
}

// dropStaleSel clears a selection the score no longer has: an event gone,
// bars or a line past the end, a caret past its bar's new end.
func (e *scoreState) dropStaleSel() {
	q := e.Sel
	switch q.Kind {
	case scoreSelEvent:
		if edit.Event(e.Score, q.Loc) == nil {
			e.Sel = scoreSel{}
		}
	case scoreSelBars:
		if q.LineB >= len(e.Score.Lines) || q.BarB > e.Score.Measures() {
			e.Sel = scoreSel{}
		}
	case scoreSelCaret:
		if edit.Staff(e.Score, q.Loc.Line, q.Loc.Staff) == nil || q.Loc.Bar < 1 || !q.At.Less(e.Score.BarLength(q.Loc.Bar)) {
			e.Sel = scoreSel{}
		}
	}
	e.TL.RollSel = e.rollSelNow()
	if e.autoPoint() == nil {
		e.TL.AutoSel.Index = -1
	}
}

// ---------- where things are ----------

// event is the selected event, or nil.
func (e *scoreState) event() *score.Event {
	if e.Sel.Kind != scoreSelEvent {
		return nil
	}
	return edit.Event(e.Score, e.Sel.Loc)
}

// chord is the selected event when it is a chord.
func (e *scoreState) chord() *score.Event {
	if ev := e.event(); ev != nil && ev.Kind == score.EventChord {
		return ev
	}
	return nil
}

// fifths is the concert key at a bar.
func (e *scoreState) fifths(bar int) int { return e.Score.KeyAt(bar).Fifths }

// writtenIV is a staff's sounding-to-written interval as the view shows it:
// zero in concert view or for a staff that does not transpose.
func (e *scoreState) writtenIV(line, staff int) score.Interval {
	st := edit.Staff(e.Score, line, staff)
	if st == nil || e.Score.Concert {
		return score.Interval{}
	}
	return score.Interval{Diatonic: -st.Transpose.Diatonic, Chromatic: -st.Transpose.Chromatic}
}

// writtenFifths is the key a staff shows at a bar.
func (e *scoreState) writtenFifths(line, staff, bar int) int {
	return engrave.WrittenFifths(e.fifths(bar), e.writtenIV(line, staff))
}

// toWritten and toConcert move a pitch between the staff's page and its sound.
func (e *scoreState) toWritten(line, staff int, p score.Pitch) score.Pitch {
	iv := e.writtenIV(line, staff)
	if iv == (score.Interval{}) {
		return p
	}
	return p.Transpose(iv)
}

func (e *scoreState) toConcert(line, staff int, p score.Pitch) score.Pitch {
	iv := e.writtenIV(line, staff)
	if iv == (score.Interval{}) {
		return p
	}
	return p.Transpose(score.Interval{Diatonic: -iv.Diatonic, Chromatic: -iv.Chromatic})
}

// staffMiddle is the written pitch of a staff's middle line at a point (in
// the clef in force there): where a letter with no note before it lands.
func (e *scoreState) staffMiddle(line, staff int, at score.Pos) score.Pitch {
	clef := "treble"
	if st := edit.Staff(e.Score, line, staff); st != nil {
		clef = st.ClefAt(at)
	}
	return score.FromDiatonic(scoreClefBottom(clef) + 4)
}

// scoreClefBottom is the diatonic number a clef puts on the bottom line.
func scoreClefBottom(clef string) int {
	switch clef {
	case "bass":
		return score.Pitch{Step: 4, Octave: 2}.Diatonic() // G2
	case "bass_8vb":
		return score.Pitch{Step: 4, Octave: 1}.Diatonic()
	case "alto":
		return score.Pitch{Step: 3, Octave: 3}.Diatonic() // F3
	case "tenor":
		return score.Pitch{Step: 1, Octave: 3}.Diatonic() // D3
	case "treble_8vb":
		return score.Pitch{Step: 2, Octave: 3}.Diatonic()
	case "treble_8va":
		return score.Pitch{Step: 2, Octave: 5}.Diatonic()
	}
	return score.Pitch{Step: 2, Octave: 4}.Diatonic() // E4
}

// caret is where the next entry goes: after the selected event, or at the
// start of the first selected bar of the first selected line.
func (e *scoreState) caret() (l edit.Loc, at score.Frac, ok bool) {
	switch e.Sel.Kind {
	case scoreSelEvent:
		if edit.Event(e.Score, e.Sel.Loc) == nil {
			return l, at, false
		}
		l = e.Sel.Loc
		_, end := edit.Onset(e.Score, l)
		if !end.Less(e.Score.BarLength(l.Bar)) {
			l.Bar++
			end = score.Frac{}
		}
		l.Event = -1
		return l, end, l.Bar <= score.MaxBars
	case scoreSelBars:
		staff := 0
		if edit.Staff(e.Score, e.Sel.LineA, e.Sel.Staff) != nil {
			staff = e.Sel.Staff
		}
		return edit.Loc{Line: e.Sel.LineA, Staff: staff, Voice: e.Voice, Bar: e.Sel.BarA, Event: -1}, score.Frac{}, true
	case scoreSelCaret:
		l = e.Sel.Loc
		l.Event = -1
		return l, e.Sel.At, l.Bar >= 1 && l.Bar <= score.MaxBars
	}
	return l, at, false
}

// selLine and selBar are the line and the first bar the selection is in
// (0 and 1 with nothing selected).
func (e *scoreState) selLine() int {
	switch e.Sel.Kind {
	case scoreSelEvent, scoreSelCaret:
		return e.Sel.Loc.Line
	case scoreSelBars:
		return e.Sel.LineA
	}
	return 0
}

func (e *scoreState) selBar() int {
	switch e.Sel.Kind {
	case scoreSelEvent, scoreSelCaret:
		return e.Sel.Loc.Bar
	case scoreSelBars:
		return e.Sel.BarA
	}
	return 1
}

// selOnset is where in its bar the selection starts: an event's onset, a
// caret's place, a bar's start.
func (e *scoreState) selOnset() score.Frac {
	switch e.Sel.Kind {
	case scoreSelEvent:
		on, _ := edit.Onset(e.Score, e.Sel.Loc)
		return on
	case scoreSelCaret:
		return e.Sel.At
	}
	return score.Frac{}
}

// shownDur is the value the toolbar and the keypad light: in note input the
// value the next note takes, otherwise the selected event's.
func (e *scoreState) shownDur() (d score.Duration, rest, tied bool) {
	d = e.Dur
	if e.Input {
		return d, false, false
	}
	if ev := e.event(); ev != nil && ev.Kind != score.EventBarRest {
		d, rest = ev.Dur, ev.Kind == score.EventRest
		for _, n := range ev.Notes {
			tied = tied || n.Tie
		}
	}
	return d, rest, tied
}

// prevPitch is the written pitch an entry at a place is nearest to: the
// selected note, else the last note before it in the voice, else the
// staff's middle line.
func (e *scoreState) prevPitch(l edit.Loc, at score.Frac) score.Pitch {
	if ev := e.chord(); ev != nil && e.Sel.Loc.Line == l.Line && e.Sel.Loc.Staff == l.Staff {
		k := e.Sel.Note
		if k < 0 || k >= len(ev.Notes) {
			k = len(ev.Notes) - 1
		}
		on, _ := edit.Onset(e.Score, e.Sel.Loc)
		return e.onPage(l.Line, l.Staff, score.Pos{Bar: e.Sel.Loc.Bar, Offset: on}, ev.Notes[k].Pitch)
	}
	if v := edit.Voice(e.Score, l.Line, l.Staff, l.Voice); v != nil {
		for b := min(l.Bar, len(v.Measures)); b >= 1; b-- {
			m := v.Measures[b-1]
			offs, _ := m.Offsets(e.Score.BarLength(b))
			for i := len(m.Events) - 1; i >= 0; i-- {
				ev := m.Events[i]
				if ev.Kind != score.EventChord || (b == l.Bar && !offs[i].Less(at)) {
					continue
				}
				return e.onPage(l.Line, l.Staff, score.Pos{Bar: b, Offset: offs[i]}, ev.Notes[len(ev.Notes)-1].Pitch)
			}
		}
	}
	return e.staffMiddle(l.Line, l.Staff, score.Pos{Bar: l.Bar, Offset: at})
}

// onPage is a stored pitch where the page shows it at a point: written for
// the staff's transposition and moved by any ottava line over it — what a
// letter typed after it is nearest to, as a click there would be.
func (e *scoreState) onPage(line, staff int, at score.Pos, p score.Pitch) score.Pitch {
	p = e.toWritten(line, staff, p)
	if st := edit.Staff(e.Score, line, staff); st != nil && !e.percussion(line, staff) {
		p.Octave -= int8(e.Score.OttavaAt(st, at))
	}
	return p
}

// ---------- entry ----------

// toggleInput is N.
func (e *scoreState) toggleInput() {
	e.Input = !e.Input
	if e.Input {
		e.Status = "Note input: click a line or space, or type A-G"
	} else {
		e.Status = "Note input off"
		e.Armed = false
	}
}

// escape leaves note input, then clears the selection.
func (e *scoreState) escape() {
	e.LaneSel = scoreLaneSel{Index: -1}
	switch {
	case e.Input:
		e.Input, e.Armed = false, false
		e.Status = "Note input off"
	case e.Sel.Kind != scoreSelNone:
		e.Sel = scoreSel{}
		e.Status = ""
	}
}

// write puts a note (written pitch, nil for a rest) of the current value
// into a place, selects it and auditions it. A note longer than the room
// left in the bar is tied over the barline.
func (e *scoreState) write(l edit.Loc, at score.Frac, written *score.Pitch) bool {
	ev := score.Event{Kind: score.EventRest, Dur: e.Dur}
	var concert score.Pitch
	if written != nil {
		p := *written
		if e.Armed && !e.percussion(l.Line, l.Staff) {
			p = edit.Accidental(p, e.ArmedAcc)
		}
		// Under an ottava line the page shows the note where it is written,
		// an octave from where it sounds.
		if st := edit.Staff(e.Score, l.Line, l.Staff); st != nil && !e.percussion(l.Line, l.Staff) {
			p.Octave += int8(e.Score.OttavaAt(st, score.Pos{Bar: l.Bar, Offset: at}))
		}
		concert = e.toConcert(l.Line, l.Staff, p)
		if !edit.Playable(concert) {
			e.Status = fmt.Sprintf("%s is out of range", edit.PitchName(p))
			return false
		}
		ev = score.Event{Kind: score.EventChord, Dur: e.Dur, Notes: []score.Note{{Pitch: concert}}}
	}
	if e.Grace != score.NotGrace {
		if written == nil {
			e.Status = "Grace notes are notes: switch grace notes off for a rest"
			return false
		}
		if !e.writeGrace(l, at, concert) {
			return false
		}
		e.Armed = false
		e.audition(l.Line, concert)
		e.Status = e.describeSel()
		return true
	}
	ok := e.change(func() error {
		// A value longer than the bar has left carries on over the barline,
		// tied; the last part is selected, so the next note follows it.
		_, loc, err := edit.WriteOver(e.Score, l.Line, l.Staff, l.Voice, l.Bar, at, ev)
		if err != nil {
			return err
		}
		e.Sel = scoreSel{Kind: scoreSelEvent, Loc: loc, Note: -1}
		if written != nil {
			e.Sel.Note = 0
		}
		return nil
	})
	if !ok {
		return false
	}
	e.Armed = false
	if written != nil {
		e.audition(l.Line, concert)
	}
	e.Status = e.describeSel()
	return true
}

// clickWrite is a click in note input: the written staff position at a
// place, spelled as the key there implies — or, on a percussion staff, the
// drum that sits there.
func (e *scoreState) clickWrite(line, staff, bar int, at score.Frac, diatonic int) bool {
	if e.percussion(line, staff) {
		d, ok := score.DrumAt(diatonic - score.PercussionBottom)
		if !ok {
			e.Status = "No drum sits on that line or space of a percussion staff"
			return false
		}
		p := score.DrumPitch(d)
		return e.write(edit.Loc{Line: line, Staff: staff, Voice: e.Voice, Bar: bar}, at, &p)
	}
	p := score.InKey(diatonic, e.writtenFifths(line, staff, bar))
	return e.write(edit.Loc{Line: line, Staff: staff, Voice: e.Voice, Bar: bar}, at, &p)
}

// percussion reports a percussion staff: its notes are drums by place
// (score.GMDrums), not pitches.
func (e *scoreState) percussion(line, staff int) bool {
	st := edit.Staff(e.Score, line, staff)
	return st != nil && st.Clef == "percussion"
}

// ghostDrum is the drum a click at a percussion staff position writes.
func (e *scoreState) ghostDrum(diatonic int) (score.Drum, bool) {
	return score.DrumAt(diatonic - score.PercussionBottom)
}

// ghost is what a click at a staff position would write, as written: the
// key's spelling, and the armed accidental if there is one (a percussion
// staff's drum there).
func (e *scoreState) ghost(line, staff, bar, diatonic int) score.Pitch {
	if e.percussion(line, staff) {
		if d, ok := e.ghostDrum(diatonic); ok {
			return score.DrumPitch(d)
		}
	}
	p := score.InKey(diatonic, e.writtenFifths(line, staff, bar))
	if e.Armed {
		p = edit.Accidental(p, e.ArmedAcc)
	}
	return p
}

// letter is A-G. In note input it writes the letter after the selection, in
// the octave nearest the note before, spelled in the key. Out of note input
// it changes the selected note to that letter; on a selected rest or bars it
// starts note input there.
func (e *scoreState) letter(step int) {
	if e.Sel.Kind == scoreSelNone {
		e.Status = "Select a bar or a note first"
		return
	}
	if !e.Input {
		if ev := e.chord(); ev != nil {
			// One note changes: the clicked one, or the chord's top note.
			k := e.Sel.Note
			if k < 0 || k >= len(ev.Notes) {
				k = len(ev.Notes) - 1
			}
			e.transform(k, func(p score.Pitch, fifths int) score.Pitch {
				return edit.NearestLetter(step, &p, fifths)
			})
			return
		}
		e.Input = true
		if ev := e.event(); ev != nil {
			// A rest: the note goes where it is.
			l := e.Sel.Loc
			on, _ := edit.Onset(e.Score, l)
			p := edit.NearestLetter(step, ptrPitch(e.prevPitch(l, on)), e.writtenFifths(l.Line, l.Staff, l.Bar))
			e.write(edit.Loc{Line: l.Line, Staff: l.Staff, Voice: l.Voice, Bar: l.Bar}, on, &p)
			return
		}
	}
	l, at, ok := e.caret()
	if !ok {
		e.Status = "Select a bar or a note first"
		return
	}
	if e.percussion(l.Line, l.Staff) {
		e.Status = "A percussion staff takes drums, not letters: click the drum's line or space"
		return
	}
	p := edit.NearestLetter(step, ptrPitch(e.prevPitch(l, at)), e.writtenFifths(l.Line, l.Staff, l.Bar))
	e.write(l, at, &p)
}

func ptrPitch(p score.Pitch) *score.Pitch { return &p }

// addLetter is Shift+A-G: the letter above the chord's top note joins it.
func (e *scoreState) addLetter(step int) {
	ev := e.chord()
	if ev == nil {
		e.Status = "Select a note to add to"
		return
	}
	l := e.Sel.Loc
	top := e.toWritten(l.Line, l.Staff, ev.Notes[len(ev.Notes)-1].Pitch)
	d := top.Diatonic() + 1
	for (d%7+7)%7 != step {
		d++
	}
	p := score.InKey(d, e.writtenFifths(l.Line, l.Staff, l.Bar))
	e.addPitch(p)
}

// addPitch adds a written pitch to the selected chord and selects it.
func (e *scoreState) addPitch(written score.Pitch) {
	l := e.Sel.Loc
	concert := e.toConcert(l.Line, l.Staff, written)
	if e.change(func() error {
		k, err := edit.AddPitch(e.Score, l, concert)
		if err != nil {
			return err
		}
		e.Sel.Note = k
		return nil
	}) {
		e.auditionChord()
		e.Status = e.describeSel()
	}
}

// interval is 2-9 (below with Shift): a diatonic interval above the
// selected note — the chord's top note when the whole chord is selected —
// spelled in the key.
func (e *scoreState) interval(n int, below bool) {
	if e.Sel.Kind == scoreSelBars {
		e.intervalBars(n, below)
		return
	}
	ev := e.chord()
	if ev == nil {
		e.Status = "Select a note, then a number adds that interval"
		return
	}
	l := e.Sel.Loc
	k := e.Sel.Note
	if k < 0 || k >= len(ev.Notes) {
		k = len(ev.Notes) - 1
		if below {
			k = 0
		}
	}
	base := e.toWritten(l.Line, l.Staff, ev.Notes[k].Pitch)
	p := edit.IntervalFrom(base, n, below, e.writtenFifths(l.Line, l.Staff, l.Bar))
	for _, q := range ev.Notes {
		if e.toWritten(l.Line, l.Staff, q.Pitch).Diatonic() == p.Diatonic() {
			e.Status = edit.PitchName(p) + " is already in the chord"
			return
		}
	}
	e.addPitch(p)
}

// transform changes note k of the selected chord (every note, k < 0) by f,
// at written pitch in the written key, and selects where note k went.
func (e *scoreState) transform(k int, f func(written score.Pitch, fifths int) score.Pitch) bool {
	ev := e.chord()
	if ev == nil {
		e.Status = "Select a note first"
		return false
	}
	l := e.Sel.Loc
	fifths := e.writtenFifths(l.Line, l.Staff, l.Bar)
	ok := e.change(func() error {
		nk, err := edit.Transform(e.Score, l, k, func(p score.Pitch) score.Pitch {
			return e.toConcert(l.Line, l.Staff, f(e.toWritten(l.Line, l.Staff, p), fifths))
		})
		if err != nil {
			return err
		}
		e.Sel.Note = nk
		return nil
	})
	if ok {
		if e.Sel.Note >= 0 {
			if c := e.chord(); c != nil {
				e.audition(l.Line, c.Notes[e.Sel.Note].Pitch)
			}
		} else {
			e.auditionChord()
		}
		e.Status = e.describeSel()
	}
	return ok
}

// accidental is + - 0: on the selected note, or armed for the next one.
func (e *scoreState) accidental(k edit.AccKind) {
	if e.chord() == nil {
		if e.Armed && e.ArmedAcc == k {
			e.Armed = false
			e.Status = "Accidental cleared"
			return
		}
		e.Armed, e.ArmedAcc = true, k
		e.Status = []string{"Sharp", "Flat", "Natural"}[k] + " on the next note"
		return
	}
	e.transform(e.Sel.Note, func(p score.Pitch, _ int) score.Pitch { return edit.Accidental(p, k) })
}

// semitone, step and octave move the selection's pitch: the selected note,
// or every note of the selected bars.
func (e *scoreState) semitone(dir int) {
	e.move(func(p score.Pitch, f int) score.Pitch { return edit.Semitone(p, dir, f) })
}

func (e *scoreState) step(dir int) {
	e.move(func(p score.Pitch, f int) score.Pitch { return edit.Step(p, dir, f) })
}

func (e *scoreState) octave(dir int) {
	e.move(func(p score.Pitch, _ int) score.Pitch { return edit.Octave(p, dir) })
}

func (e *scoreState) move(f func(written score.Pitch, fifths int) score.Pitch) {
	if e.Sel.Kind == scoreSelBars {
		e.transformBars(f)
		return
	}
	e.transform(e.Sel.Note, f)
}

// eachSelectedChord visits every chord (grace notes too) of every voice of
// every staff of the selected bars, with its written key.
func (e *scoreState) eachSelectedChord(f func(l edit.Loc, ev *score.Event, fifths int)) {
	q := e.Sel
	for li := q.LineA; li <= q.LineB && li < len(e.Score.Lines); li++ {
		for si, st := range e.Score.Lines[li].Staves {
			for vi, v := range st.Voices {
				for b := q.BarA; b <= q.BarB && b <= len(v.Measures); b++ {
					fifths := e.writtenFifths(li, si, b)
					for i := range v.Measures[b-1].Events {
						if ev := &v.Measures[b-1].Events[i]; ev.Kind == score.EventChord {
							f(edit.Loc{Line: li, Staff: si, Voice: vi, Bar: b, Event: i}, ev, fifths)
						}
					}
				}
			}
		}
	}
}

// intervalBars is 2-9 over selected bars: the interval above every chord's
// top note (below its bottom one), spelled in the key, as Sibelius adds it
// to a passage. A chord that already has the letter, or whose new note
// would be out of range, is left as it is.
func (e *scoreState) intervalBars(n int, below bool) {
	added, kept := 0, 0
	e.change(func() error {
		e.eachSelectedChord(func(l edit.Loc, ev *score.Event, fifths int) {
			k := len(ev.Notes) - 1
			if below {
				k = 0
			}
			p := edit.IntervalFrom(e.toWritten(l.Line, l.Staff, ev.Notes[k].Pitch), n, below, fifths)
			for _, q := range ev.Notes {
				if e.toWritten(l.Line, l.Staff, q.Pitch).Diatonic() == p.Diatonic() {
					kept++
					return
				}
			}
			if _, err := edit.AddPitch(e.Score, l, e.toConcert(l.Line, l.Staff, p)); err != nil {
				kept++
				return
			}
			added++
		})
		if added == 0 {
			return fmt.Errorf("no note in the selection could take the interval")
		}
		return nil
	})
	if added > 0 {
		e.Status = fmt.Sprintf("Added a note to %d chord(s)", added)
		if kept > 0 {
			e.Status += fmt.Sprintf("; %d already had it or would be out of range", kept)
		}
	}
}

// transformBars moves every note of the selected bars by f, at written
// pitch in each bar's written key. A chord f would take out of range stays.
func (e *scoreState) transformBars(f func(written score.Pitch, fifths int) score.Pitch) {
	moved, kept := 0, 0
	e.change(func() error {
		e.eachSelectedChord(func(l edit.Loc, ev *score.Event, fifths int) {
			_, err := edit.Transform(e.Score, l, -1, func(p score.Pitch) score.Pitch {
				return e.toConcert(l.Line, l.Staff, f(e.toWritten(l.Line, l.Staff, p), fifths))
			})
			if err != nil {
				kept++
				return
			}
			moved++
		})
		if moved == 0 {
			return fmt.Errorf("no note in the selection could move")
		}
		return nil
	})
	if moved > 0 {
		e.Status = fmt.Sprintf("Moved %d chord(s)", moved)
		if kept > 0 {
			e.Status += fmt.Sprintf("; %d would be out of range and stayed", kept)
		}
	}
}

// respell is Return: the same sound, the next spelling.
func (e *scoreState) respell() {
	e.transform(e.Sel.Note, func(p score.Pitch, _ int) score.Pitch { return edit.Respell(p) })
}

// ---------- rhythm ----------

// keypadDur is what a keypad slot (1-6) means in the current layout.
func (e *scoreState) keypadDur(slot int) (score.DurBase, bool) {
	if e.Layout2 {
		switch slot {
		case 1:
			return score.D64, true
		case 2:
			return score.D128, true
		case 3:
			return score.D256, true
		case 4:
			return score.Breve, true
		}
		return 0, false
	}
	if slot < 1 || slot > 6 {
		return 0, false
	}
	return []score.DurBase{score.D32, score.D16, score.Eighth, score.Quarter, score.Half, score.WholeNote}[slot-1], true
}

// keypad is a keypad slot pressed.
func (e *scoreState) keypad(slot int) {
	// The second layout's 5 and 6 are the grace notes.
	if e.Layout2 && (slot == 5 || slot == 6) {
		e.toggleGrace([]score.Grace{score.Acciaccatura, score.Appoggiatura}[slot-5])
		return
	}
	b, ok := e.keypadDur(slot)
	if !ok {
		e.Status = fmt.Sprintf("Keypad %d has nothing on the second layout (F8 goes back)", slot)
		return
	}
	e.setDur(b)
}

// setDur picks the value notes are written at. Out of note input it also
// changes the selected event to it (reflowing the bar); in note input it is
// the next note's value only, as in Sibelius and MuseScore, where the value
// is chosen before the letter: 4 C 3 D E is a quarter and two eighths.
func (e *scoreState) setDur(b score.DurBase) {
	e.Dur = score.Duration{Base: b}
	if ev := e.event(); ev != nil && ev.Kind != score.EventBarRest && !e.Input {
		l := e.Sel.Loc
		if e.change(func() error {
			nl, err := edit.SetDuration(e.Score, l, e.Dur)
			if err != nil {
				return err
			}
			e.Sel.Loc = nl
			return nil
		}) {
			e.Status = e.describeSel()
		}
		return
	}
	e.Status = "Duration: " + edit.DurName(e.Dur)
}

// toggleDot is keypad "." : one dot, two, none — on the selected event out
// of note input, or (in note input, or with nothing selected) for the notes
// to come.
func (e *scoreState) toggleDot() {
	if ev := e.event(); ev != nil && ev.Kind != score.EventBarRest && !e.Input {
		l := e.Sel.Loc
		if e.change(func() error {
			nl, err := edit.ToggleDots(e.Score, l)
			if err != nil {
				return err
			}
			e.Sel.Loc = nl
			if ne := edit.Event(e.Score, nl); ne != nil {
				e.Dur = ne.Dur
			}
			return nil
		}) {
			e.Status = e.describeSel()
		}
		return
	}
	e.Dur.Dots = (e.Dur.Dots + 1) % 3
	e.Status = "Duration: " + edit.DurName(e.Dur)
}

// rest is keypad 0: in note input a rest is written after the selection;
// otherwise the selected note becomes a rest.
func (e *scoreState) rest() {
	if e.Input {
		l, at, ok := e.caret()
		if !ok {
			e.Status = "Select a bar or a note first"
			return
		}
		e.write(l, at, nil)
		return
	}
	if e.chord() == nil {
		e.Status = "Select a note to make it a rest"
		return
	}
	l := e.Sel.Loc
	if e.change(func() error { return edit.MakeRest(e.Score, l) }) {
		e.Sel.Note = -1
		e.Status = e.describeSel()
	}
}

// tie is keypad Enter: tie the selected note (or chord) into the next.
func (e *scoreState) tie() {
	if e.chord() == nil {
		e.Status = "Select a note to tie"
		return
	}
	l, k := e.Sel.Loc, e.Sel.Note
	if e.change(func() error { return edit.ToggleTie(e.Score, l, k) }) {
		e.Status = e.describeSel()
	}
}

// flip forces the selected stem the other way; up is where it is drawn now.
func (e *scoreState) flip(up bool) {
	if e.chord() == nil {
		e.Status = "Select a note to flip"
		return
	}
	l := e.Sel.Loc
	if e.change(func() error { return edit.FlipStem(e.Score, l, up) }) {
		e.Status = "Stem flipped"
	}
}

// setVoice picks the voice notes go into (0..3). The selection keeps its
// place in time in the new voice: in note input the caret stays where the
// next note was going (so a second voice starts under the first one's last
// note's end, or wherever the caret was), out of it the new voice's event
// sounding at the selected one's onset is selected — or, where the new
// voice has nothing there yet, a caret at that onset.
func (e *scoreState) setVoice(v int) {
	v = max(0, min(v, score.MaxVoices-1))
	// Out of note input a selected note moves into the voice (Sibelius).
	if !e.Input && e.chord() != nil && e.chord().Grace == score.NotGrace && e.Sel.Loc.Voice != v {
		if e.moveToVoice(v) {
			return
		}
		status := e.Status
		defer func() { e.Status = status }()
	}
	// So do the notes of selected bars.
	if !e.Input && e.Sel.Kind == scoreSelBars {
		if !e.moveBarsToVoice(v) {
			e.Voice = v
		}
		return
	}
	e.Voice = v
	switch e.Sel.Kind {
	case scoreSelEvent, scoreSelCaret:
		if e.Sel.Loc.Voice == e.Voice {
			break
		}
		var l edit.Loc
		var at score.Frac
		if e.Input {
			cl, ca, ok := e.caret()
			if !ok {
				break
			}
			l, at = cl, ca
		} else {
			l, at = e.Sel.Loc, e.selOnset()
		}
		l.Voice, l.Event = e.Voice, -1
		if !e.Input {
			if k, ok := scoreEventAt(e.Score, l, at); ok {
				l.Event = k
				e.Sel = scoreSel{Kind: scoreSelEvent, Loc: l, Note: -1}
				break
			}
		}
		e.Sel = scoreSel{Kind: scoreSelCaret, Loc: l, Note: -1, At: at}
	}
	e.Status = fmt.Sprintf("Voice %d", e.Voice+1)
}

// scoreEventAt is the index of the event of a voice's bar that starts at an
// offset (grace notes aside).
func scoreEventAt(s *score.Score, l edit.Loc, at score.Frac) (int, bool) {
	v := edit.Voice(s, l.Line, l.Staff, l.Voice)
	if v == nil || l.Bar < 1 || l.Bar > len(v.Measures) {
		return 0, false
	}
	m := v.Measures[l.Bar-1]
	offs, _ := m.Offsets(s.BarLength(l.Bar))
	for i, ev := range m.Events {
		if ev.Grace == score.NotGrace && offs[i].Eq(at) {
			return i, true
		}
	}
	return 0, false
}

// tuplet is Ctrl+2-9: an n-tuplet starting on the selected event.
func (e *scoreState) tuplet(n int) {
	ev := e.event()
	if ev == nil {
		e.Status = "Select a note to start the tuplet on"
		return
	}
	l := e.Sel.Loc
	if e.change(func() error {
		nl, err := edit.MakeTuplet(e.Score, l, n)
		if err != nil {
			return err
		}
		e.Sel.Loc = nl
		return nil
	}) {
		e.Status = fmt.Sprintf("Tuplet of %d", n)
	}
}

// deleteSel is Delete: a note becomes a rest (one note of a chord goes);
// selected bars are cleared.
func (e *scoreState) deleteSel() {
	switch e.Sel.Kind {
	case scoreSelEvent:
		l, k := e.Sel.Loc, e.Sel.Note
		if e.change(func() error { return edit.Delete(e.Score, l, k) }) {
			e.Sel.Note = -1
			if edit.Event(e.Score, e.Sel.Loc) == nil {
				e.Sel = scoreSel{}
			}
			e.Status = e.describeSel()
		}
	case scoreSelBars:
		q := e.Sel
		if e.change(func() error {
			edit.ClearBars(e.Score, q.LineA, q.LineB, q.BarA, q.BarB)
			return nil
		}) {
			e.Status = "Cleared " + e.describeSel()
		}
	default:
		e.Status = "Nothing selected"
	}
}

// ---------- selection ----------

// selectEvent selects an event (one of its notes, note >= 0) and auditions it.
func (e *scoreState) selectEvent(l edit.Loc, note int) {
	ev := edit.Event(e.Score, l)
	if ev == nil {
		return
	}
	e.Sel = scoreSel{Kind: scoreSelEvent, Loc: l, Note: note}
	e.Voice = l.Voice
	if ev.Kind == score.EventChord {
		if note >= 0 && note < len(ev.Notes) {
			e.audition(l.Line, ev.Notes[note].Pitch)
		} else {
			e.auditionChord()
		}
	}
	e.Status = e.describeSel()
}

// clickBar is a click in empty space in a bar: that bar of that line;
// every line (all); Shift extends the bars and lines already selected.
func (e *scoreState) clickBar(line, bar int, all, extend bool) {
	e.clickBarStaff(line, 0, bar, all, extend)
}

// clickBarStaff is clickBar on a given staff of the line, which is where
// note input into the selected bars then starts.
func (e *scoreState) clickBarStaff(line, staff, bar int, all, extend bool) {
	// A plain click on one line remembers its staff; a block of lines starts
	// note input on its first line's top staff.
	defer func() {
		if e.Sel.Kind == scoreSelBars && !extend && !all {
			e.Sel.Staff = staff
		}
	}()
	lo, hi := line, line
	if all {
		lo, hi = 0, len(e.Score.Lines)-1
	}
	if extend && e.Sel.Kind == scoreSelBars {
		q := e.Sel
		a, z := min(q.BarA, bar), max(q.BarB, bar)
		if !all {
			lo, hi = min(q.LineA, line), max(q.LineB, line)
		}
		e.Sel = scoreSel{Kind: scoreSelBars, LineA: lo, LineB: hi, BarA: a, BarB: z}
	} else if extend && (e.Sel.Kind == scoreSelEvent || e.Sel.Kind == scoreSelCaret) {
		q := e.Sel.Loc
		e.Sel = scoreSel{Kind: scoreSelBars, LineA: min(q.Line, lo), LineB: max(q.Line, hi), BarA: min(q.Bar, bar), BarB: max(q.Bar, bar)}
	} else {
		e.Sel = scoreSel{Kind: scoreSelBars, LineA: lo, LineB: hi, BarA: bar, BarB: bar}
	}
	e.Status = e.describeSel()
}

// selectAll is Ctrl+A: every bar of every line.
func (e *scoreState) selectAll() {
	if len(e.Score.Lines) == 0 {
		return
	}
	e.Sel = scoreSel{Kind: scoreSelBars, LineA: 0, LineB: len(e.Score.Lines) - 1, BarA: 1, BarB: e.Score.Measures()}
	e.Status = e.describeSel()
}

// moveSel moves the selection to the previous or next event of its voice,
// across barlines. From selected bars it goes into the first bar's first event.
func (e *scoreState) moveSel(dir int) {
	switch e.Sel.Kind {
	case scoreSelBars:
		l := edit.Loc{Line: e.Sel.LineA, Voice: e.Voice, Bar: e.Sel.BarA}
		if dir < 0 {
			l.Bar = e.Sel.BarB
		}
		if v := edit.Voice(e.Score, l.Line, 0, e.Voice); v != nil && len(v.Measure(l.Bar).Events) > 0 {
			if dir < 0 {
				l.Event = len(v.Measure(l.Bar).Events) - 1
			}
			e.selectEvent(l, -1)
		}
		return
	case scoreSelCaret:
		// The event after the caret (Right) or before it (Left).
		l := e.Sel.Loc
		v := edit.Voice(e.Score, l.Line, l.Staff, l.Voice)
		if v == nil || l.Bar > len(v.Measures) {
			return
		}
		m := v.Measures[l.Bar-1]
		offs, _ := m.Offsets(e.Score.BarLength(l.Bar))
		pick := -1
		for i := range m.Events {
			if dir > 0 && pick < 0 && !offs[i].Less(e.Sel.At) {
				pick = i
			}
			if dir < 0 && offs[i].Less(e.Sel.At) {
				pick = i
			}
		}
		if pick >= 0 {
			l.Event = pick
			e.selectEvent(l, -1)
			e.Reveal = true
		}
		return
	case scoreSelNone:
		return
	}
	l := e.Sel.Loc
	v := edit.Voice(e.Score, l.Line, l.Staff, l.Voice)
	if v == nil {
		return
	}
	l.Event += dir
	for l.Bar >= 1 && l.Bar <= len(v.Measures) {
		n := len(v.Measures[l.Bar-1].Events)
		if l.Event >= 0 && l.Event < n {
			e.selectEvent(l, -1)
			e.Reveal = true
			return
		}
		if dir > 0 {
			l.Bar++
			l.Event = 0
		} else {
			l.Bar--
			if l.Bar >= 1 && l.Bar <= len(v.Measures) {
				l.Event = len(v.Measures[l.Bar-1].Events) - 1
			}
		}
	}
}

// ---------- clipboard ----------

// copySel is Ctrl+C: the selected bars, or the selected event.
func (e *scoreState) copySel() {
	switch e.Sel.Kind {
	case scoreSelBars:
		q := e.Sel
		c, err := edit.CopyBars(e.Score, q.LineA, q.LineB, q.BarA, q.BarB)
		if err != nil {
			e.Status = scoreErrText(err)
			return
		}
		e.Clip, e.ClipEvent = c, nil
		e.Status = "Copied " + e.describeSel()
	case scoreSelEvent:
		ev := e.event()
		if ev == nil {
			return
		}
		c := ev.Clone()
		e.ClipEvent, e.Clip = &c, nil
		e.Status = "Copied " + scoreEventText(e, e.Sel.Loc.Line, e.Sel.Loc.Staff, &c)
	default:
		e.Status = "Select bars or a note to copy"
	}
}

// paste is Ctrl+V: over the selection.
func (e *scoreState) paste() {
	switch {
	case e.Clip == nil && e.ClipEvent == nil:
		e.Status = "Nothing copied yet"
	case e.Sel.Kind == scoreSelBars:
		if e.Clip != nil {
			e.pasteBars(e.Sel.LineA, e.Sel.BarA)
		} else {
			e.pasteEvent(edit.Loc{Line: e.Sel.LineA, Voice: e.Voice, Bar: e.Sel.BarA}, score.Frac{})
		}
	case e.Sel.Kind == scoreSelEvent || e.Sel.Kind == scoreSelCaret:
		l := e.Sel.Loc
		if e.Clip != nil {
			e.pasteBars(l.Line, l.Bar)
			return
		}
		e.pasteEvent(l, e.selOnset())
	default:
		e.Status = "Click a bar to paste into"
	}
}

// pasteAt is Alt+click: the clipboard at a point — bars from the clicked
// bar of the clicked line, an event at the clicked beat.
func (e *scoreState) pasteAt(line, staff, bar int, at score.Frac) {
	switch {
	case e.Clip != nil:
		e.pasteBars(line, bar)
	case e.ClipEvent != nil:
		e.pasteEvent(edit.Loc{Line: line, Staff: staff, Voice: e.Voice, Bar: bar}, at)
	default:
		e.Status = "Nothing copied yet"
	}
}

func (e *scoreState) pasteBars(line, bar int) {
	c := e.Clip
	var lines int
	var cut bool
	if e.change(func() error {
		var err error
		lines, cut, err = edit.PasteBars(e.Score, c, line, bar)
		if err != nil {
			return err
		}
		e.Sel = scoreSel{Kind: scoreSelBars, LineA: line, LineB: line + lines - 1, BarA: bar, BarB: bar + c.Bars - 1}
		return nil
	}) {
		e.Status = "Pasted into " + e.describeSel()
		if cut {
			e.Status += " (bars of another length were fitted)"
		}
	}
}

func (e *scoreState) pasteEvent(l edit.Loc, at score.Frac) {
	ev := e.ClipEvent.Clone()
	if e.change(func() error {
		loc, err := edit.Write(e.Score, l.Line, l.Staff, l.Voice, l.Bar, at, ev)
		if err != nil {
			return err
		}
		e.Sel = scoreSel{Kind: scoreSelEvent, Loc: loc, Note: -1}
		return nil
	}) {
		e.auditionChord()
		e.Status = "Pasted " + e.describeSel()
	}
}

// repeat is R: the selected bars again right after them (the score grows
// when it has to), or the selected note again after itself. The copy is
// selected, so R again carries on.
func (e *scoreState) repeat() {
	switch e.Sel.Kind {
	case scoreSelBars:
		q := e.Sel
		c, err := edit.CopyBars(e.Score, q.LineA, q.LineB, q.BarA, q.BarB)
		if err != nil {
			e.Status = scoreErrText(err)
			return
		}
		save := e.Clip
		e.Clip = c
		e.pasteBars(q.LineA, q.BarB+1)
		e.Clip = save
	case scoreSelEvent:
		ev := e.event()
		if ev == nil || ev.Grace != score.NotGrace {
			e.Status = "Select a note or bars to repeat"
			return
		}
		cp := ev.Clone()
		l, at, ok := e.caret()
		if !ok {
			return
		}
		l.Voice = e.Sel.Loc.Voice
		if e.change(func() error {
			loc, err := edit.Write(e.Score, l.Line, l.Staff, l.Voice, l.Bar, at, cp)
			if err != nil {
				return err
			}
			e.Sel = scoreSel{Kind: scoreSelEvent, Loc: loc, Note: -1}
			return nil
		}) {
			e.auditionChord()
			e.Status = "Repeated: " + e.describeSel()
		}
	default:
		e.Status = "Select bars or a note to repeat"
	}
}

// ---------- signatures and clefs ----------

// setKey puts a key signature at a bar.
func (e *scoreState) setKey(bar, fifths int, mode string) {
	if e.change(func() error { return edit.SetKey(e.Score, bar, fifths, mode) }) {
		e.Status = fmt.Sprintf("%s from bar %d", scoreKeyName(fifths, mode), bar)
	}
}

// setMeter puts a time signature at a bar.
func (e *scoreState) setMeter(bar int, m score.Meter) {
	var from, to int
	var cut bool
	if e.change(func() error {
		var err error
		from, to, cut, err = edit.SetMeter(e.Score, bar, m)
		return err
	}) {
		e.Status = fmt.Sprintf("%s from bar %d", m, bar)
		if cut {
			e.Status += fmt.Sprintf("; music past the new barlines in bars %d-%d was cut (Ctrl+Z brings it back)", from, to)
		}
		// The selection may point past a bar's new end.
		e.dropStaleSel()
	}
}

// setClef changes a staff's clef.
func (e *scoreState) setClef(line, staff int, clef string) {
	if e.change(func() error { return edit.SetClef(e.Score, line, staff, clef) }) {
		e.Status = "Clef: " + clef
	}
}

// ---------- auditions ----------

func (e *scoreState) audition(line int, p score.Pitch) {
	e.auditions = append(e.auditions, scoreAudition{Line: line, MIDI: []int{p.MIDI()}})
}

// auditionChord plays every note of the selected event.
func (e *scoreState) auditionChord() {
	ev := e.chord()
	if ev == nil {
		return
	}
	a := scoreAudition{Line: e.Sel.Loc.Line}
	for _, n := range ev.Notes {
		a.MIDI = append(a.MIDI, n.Pitch.MIDI())
	}
	e.auditions = append(e.auditions, a)
}

// takeAuditions hands the window what to play and forgets it.
func (e *scoreState) takeAuditions() []scoreAudition {
	a := e.auditions
	e.auditions = nil
	return a
}

// ---------- describing ----------

// describeSel is the selection for the status line: "Flute, bar 3, voice 1:
// F#5 + A5 (quarter)", "All lines, bars 2-4".
func (e *scoreState) describeSel() string {
	q := e.Sel
	switch q.Kind {
	case scoreSelEvent:
		ev := e.event()
		if ev == nil {
			return "Nothing selected"
		}
		return fmt.Sprintf("%s, bar %d, voice %d: %s", e.lineName(q.Loc.Line), q.Loc.Bar, q.Loc.Voice+1, scoreEventText(e, q.Loc.Line, q.Loc.Staff, ev))
	case scoreSelCaret:
		return fmt.Sprintf("%s, bar %d at %s, voice %d", e.lineName(q.Loc.Line), q.Loc.Bar, scoreBeatText(e.Score, q.Loc.Bar, q.At), q.Loc.Voice+1)
	case scoreSelBars:
		who := ""
		switch {
		case q.LineA == 0 && q.LineB == len(e.Score.Lines)-1 && q.LineB > 0:
			who = "All lines"
		case q.LineA == q.LineB:
			who = e.lineName(q.LineA)
		default:
			names := []string{}
			for li := q.LineA; li <= q.LineB; li++ {
				names = append(names, e.lineName(li))
			}
			who = strings.Join(names, ", ")
		}
		if q.BarA == q.BarB {
			return fmt.Sprintf("%s, bar %d", who, q.BarA)
		}
		return fmt.Sprintf("%s, bars %d-%d", who, q.BarA, q.BarB)
	}
	return "Nothing selected"
}

// scoreBeatText is an offset into a bar as a beat: "beat 1", "beat 2.5"
// (in the meter's beat unit, the denominator's note).
func scoreBeatText(s *score.Score, bar int, at score.Frac) string {
	m := s.MeterAt(bar)
	beat := at.MulInt(int64(max(m.Den, 1))).Float() + 1
	return "beat " + strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.3f", beat), "0"), ".")
}

func (e *scoreState) lineName(i int) string {
	if i < 0 || i >= len(e.Score.Lines) {
		return fmt.Sprintf("Line %d", i+1)
	}
	if n := e.Score.Lines[i].Name; n != "" {
		return n
	}
	return fmt.Sprintf("Line %d", i+1)
}

// scoreEventText is an event in words, at written pitch: "F#5 + A5
// (quarter, tied)", "dotted 8th rest".
func scoreEventText(e *scoreState, line, staff int, ev *score.Event) string {
	switch ev.Kind {
	case score.EventRest:
		return edit.DurName(ev.Dur) + " rest"
	case score.EventSpace:
		return edit.DurName(ev.Dur) + " of silence"
	case score.EventBarRest:
		return "bar rest"
	}
	var names []string
	tied := false
	drums := e.percussion(line, staff)
	for _, n := range ev.Notes {
		if drums {
			names = append(names, score.DrumName(n.Pitch))
		} else {
			names = append(names, edit.PitchName(e.toWritten(line, staff, n.Pitch)))
		}
		tied = tied || n.Tie
	}
	extra := edit.DurName(ev.Dur)
	if ev.Grace == score.Acciaccatura {
		extra = "acciaccatura, " + extra
	} else if ev.Grace == score.Appoggiatura {
		extra = "appoggiatura, " + extra
	}
	if tied {
		extra += ", tied"
	}
	if ev.Tuplet > 0 {
		extra += ", in a tuplet"
	}
	return strings.Join(names, " + ") + " (" + extra + ")"
}

// scoreKeyName names a key signature: "G major", "Bb minor".
func scoreKeyName(fifths int, mode string) string {
	if fifths < -7 || fifths > 7 {
		return fmt.Sprintf("%d fifths", fifths)
	}
	major := []string{"Cb", "Gb", "Db", "Ab", "Eb", "Bb", "F", "C", "G", "D", "A", "E", "B", "F#", "C#"}
	minor := []string{"Ab", "Eb", "Bb", "F", "C", "G", "D", "A", "E", "B", "F#", "C#", "G#", "D#", "A#"}
	if mode == "minor" {
		return minor[fifths+7] + " minor"
	}
	if mode == "" {
		mode = "major"
	}
	return major[fifths+7] + " " + mode
}
