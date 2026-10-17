package editor

// editor_score_words.go — typing words into the score, and the staves that
// are not five lines of pitches: lyrics and chord symbols typed in place,
// drums written by their kit's letters, frets typed onto a tablature, and
// the line settings for all of them. The editing model only; the view
// draws the text field (scoreDrawWordField) and the key handler routes
// fret digits here (scoreEditorHandleEvent).
//
// Lyrics are typed as Sibelius has it: Ctrl+L on a note opens a field
// under it; Space goes to the next note (a word ends), "-" to the next
// note with a hyphen (the word goes on), "_" holds the syllable over the
// next note (a melisma), and Space with nothing typed holds the syllable
// before it over this note too; Backspace in an empty field goes back to
// the note before; Enter or Escape finish. Ctrl+Alt+L types the second
// verse (Ctrl+Alt+L again in the field moves to the next verse). Notes a
// syllable cannot go on — rests, grace notes, notes tied over — are
// passed over (edit.NextSung).
//
// Chord symbols: Ctrl+K at the selection opens a field over the staff;
// Space writes the symbol and goes to the next beat or note, whichever
// comes first (edit.NextChordPlace); Enter or Escape write and finish; an
// empty field takes the symbol there away. What is typed is read as a
// musician writes it (score.ParseChord); a symbol that does not read stays
// in the field with the reason in the status line. A click on a chord
// symbol edits it.
//
// Drums: on a percussion staff the letters A-G write the sound of the
// line's drum map with that shortcut (Shift adds it to the chord), a click
// writes the first sound at that line or space, and Up/Down walk a note
// through every sound of the kit (score.Kit.Step).
//
// Tablature: a click on a string in note input writes that open string's
// note there; the number keys type the fret on the string in hand (two
// digits within a second make 10-24); Up/Down move the hand to the string
// above or below while the tablature has the keyboard.

import (
	"fmt"
	"strings"
	"time"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/play"
)

// scoreWordKind is what a word field types.
type scoreWordKind uint8

const (
	scoreWordLyric scoreWordKind = iota + 1
	scoreWordChord
)

// scoreWordEntry is text being typed into the score.
type scoreWordEntry struct {
	Kind scoreWordKind
	// Loc is the note a syllable goes on, Verse its verse.
	Loc   edit.Loc
	Verse int
	// Line and At are where a chord symbol goes.
	Line int
	At   score.Pos
	// Text is the field's text.
	Text string
	// Focus asks the view to give the field the keyboard.
	Focus bool
}

// scoreWordKey is a key a word field acts on itself.
type scoreWordKey uint8

const (
	scoreWordEnter scoreWordKey = iota
	scoreWordEscape
	scoreWordBackspace // Backspace in an empty field
	scoreWordTab       // on to the next place, writing what is typed
	scoreWordNextVerse // Ctrl+Alt+L in a lyric field
)

// ---------- lyrics ----------

// startLyrics opens a lyric field in a verse at the selection: its note,
// the next note a syllable can go on after a rest, the first sung note of
// a selected bar.
func (e *scoreState) startLyrics(verse int) {
	l, ok := e.wordsLoc()
	if !ok {
		e.Status = "Select a note to type its syllable under it"
		return
	}
	at, ok := edit.FirstSung(e.Score, l)
	if !ok {
		e.Status = "No note to sing from here on in this voice"
		return
	}
	e.Input = false
	e.Words = &scoreWordEntry{Kind: scoreWordLyric, Loc: at, Verse: verse, Focus: true}
	e.loadLyric()
	e.Sel = scoreSel{Kind: scoreSelEvent, Loc: at, Note: -1}
	e.Reveal = true
	e.Status = e.wordsStatus()
}

// wordsLoc is the event the selection names for word entry: the selected
// event, the event a caret is before, a selected bar's first event in the
// voice.
func (e *scoreState) wordsLoc() (edit.Loc, bool) {
	switch e.Sel.Kind {
	case scoreSelEvent:
		return e.Sel.Loc, edit.Event(e.Score, e.Sel.Loc) != nil
	case scoreSelCaret, scoreSelBars:
		l, at, ok := e.caret()
		if !ok {
			return l, false
		}
		v := edit.Voice(e.Score, l.Line, l.Staff, l.Voice)
		if v == nil {
			return l, false
		}
		m := v.Measure(l.Bar)
		offs, _ := m.Offsets(e.Score.BarLength(l.Bar))
		for i := range m.Events {
			if !offs[i].Less(at) {
				l.Event = i
				return l, true
			}
		}
	}
	return edit.Loc{}, false
}

// loadLyric puts the syllable already on the field's note into the field.
func (e *scoreState) loadLyric() {
	w := e.Words
	w.Text = ""
	if ev := edit.Event(e.Score, w.Loc); ev != nil {
		if ly, ok := ev.Lyric(w.Verse); ok {
			w.Text = ly.Text
		}
	}
}

// commitLyric writes the field's syllable (joined on as asked) onto its
// note; an empty field takes the syllable there away. It reports whether
// the score took it.
func (e *scoreState) commitLyric(text string, hyphen, extend bool) bool {
	w := e.Words
	ly := score.Lyric{Verse: w.Verse, Text: strings.TrimSpace(text), Hyphen: hyphen, Extend: extend}
	if ly.Text == "" {
		ly.Hyphen, ly.Extend = false, false
	}
	ev := edit.Event(e.Score, w.Loc)
	if ev == nil {
		return false
	}
	if old, ok := ev.Lyric(w.Verse); ok && old == ly || !ok && ly.Text == "" {
		return true // nothing changes: no undo step
	}
	return e.change(func() error { return edit.SetLyric(e.Score, w.Loc, ly) })
}

// lyricNext moves the field to the next note a syllable can go on, or
// closes it at the voice's end.
func (e *scoreState) lyricNext() {
	w := e.Words
	at, ok := edit.NextSung(e.Score, w.Loc)
	if !ok {
		e.Words = nil
		e.Status = "Lyrics: the end of the voice"
		return
	}
	w.Loc, w.Focus = at, true
	e.loadLyric()
	e.Sel = scoreSel{Kind: scoreSelEvent, Loc: at, Note: -1}
	e.Reveal = true
	e.Status = e.wordsStatus()
}

// lyricInput takes what the field holds after a keystroke: a space, a
// hyphen or an underscore at its end write the syllable and move on.
func (e *scoreState) lyricInput(text string) {
	w := e.Words
	switch {
	case strings.HasSuffix(text, " "):
		word := strings.TrimSpace(text)
		if word == "" {
			// Space alone: the syllable before is held over this note.
			if ev := edit.Event(e.Score, w.Loc); ev != nil {
				if _, has := ev.Lyric(w.Verse); !has {
					e.change(func() error {
						if _, ok := edit.Melisma(e.Score, w.Loc, w.Verse); !ok {
							return errNoMelisma
						}
						return nil
					})
				}
			}
			e.lyricNext()
			return
		}
		if e.commitLyric(word, false, false) {
			e.lyricNext()
		}
	case strings.HasSuffix(text, "-") && len(strings.TrimSpace(text)) > 1:
		if e.commitLyric(strings.TrimSuffix(text, "-"), true, false) {
			e.lyricNext()
		}
	case strings.HasSuffix(text, "_") && len(strings.TrimSpace(text)) > 1:
		if e.commitLyric(strings.TrimSuffix(text, "_"), false, true) {
			e.lyricNext()
		}
	default:
		w.Text = text
	}
}

// ---------- chord symbols ----------

// startChord opens a chord symbol field at the selection's place on its
// line (a selected bar's start).
func (e *scoreState) startChord() {
	var line int
	var at score.Pos
	switch e.Sel.Kind {
	case scoreSelEvent:
		on, _ := edit.Onset(e.Score, e.Sel.Loc)
		line, at = e.Sel.Loc.Line, score.Pos{Bar: e.Sel.Loc.Bar, Offset: on}
	case scoreSelCaret:
		line, at = e.Sel.Loc.Line, score.Pos{Bar: e.Sel.Loc.Bar, Offset: e.Sel.At}
	case scoreSelBars:
		line, at = e.Sel.LineA, score.Pos{Bar: e.Sel.BarA}
	default:
		e.Status = "Select a note or a bar to put a chord symbol over"
		return
	}
	if !at.Offset.Less(e.Score.BarLength(at.Bar)) {
		at = score.Pos{Bar: at.Bar + 1}
	}
	e.editChordAt(line, at)
}

// editChordAt opens the chord symbol field at a place (the symbol there,
// if any, in it).
func (e *scoreState) editChordAt(line int, at score.Pos) {
	if at.Bar > e.Score.Measures() {
		e.Status = "No bar there for a chord symbol"
		return
	}
	e.Input = false
	e.Words = &scoreWordEntry{Kind: scoreWordChord, Line: line, At: at, Focus: true}
	if cs := edit.ChordSymbolAt(e.Score, line, at); cs != nil {
		e.Words.Text = cs.Chord.String()
	}
	e.Status = e.wordsStatus()
}

// commitChord writes the field's chord symbol (an empty field takes the
// one there away); a symbol that will not read is refused and stays.
func (e *scoreState) commitChord(text string) bool {
	w := e.Words
	text = strings.TrimSpace(text)
	cur := edit.ChordSymbolAt(e.Score, w.Line, w.At)
	if text == "" {
		if cur == nil {
			return true
		}
		return e.change(func() error { edit.RemoveChordSymbol(e.Score, w.Line, w.At); return nil })
	}
	c, err := score.ParseChord(text)
	if err != nil {
		e.Status = "Chord symbol: " + scoreErrText(err)
		w.Text, w.Focus = text, true
		return false
	}
	if cur != nil && cur.Chord.String() == c.String() {
		return true
	}
	return e.change(func() error { _, err := edit.SetChordSymbol(e.Score, w.Line, w.At, c); return err })
}

// chordNext moves the field to the next place on the line.
func (e *scoreState) chordNext() {
	w := e.Words
	at, ok := edit.NextChordPlace(e.Score, w.Line, w.At)
	if !ok {
		e.Words = nil
		e.Status = "Chord symbols: the end of the score"
		return
	}
	e.editChordAt(w.Line, at)
}

// chordInput takes what the field holds: a space at its end writes the
// symbol and moves on.
func (e *scoreState) chordInput(text string) {
	if strings.HasSuffix(text, " ") {
		if strings.TrimSpace(text) == "" {
			e.chordNext() // nothing typed: on to the next place
			return
		}
		if e.commitChord(text) {
			e.chordNext()
		}
		return
	}
	e.Words.Text = text
}

// errNoMelisma is a Space with no syllable before it to hold on.
var errNoMelisma = fmt.Errorf("no syllable before this note to hold over it")

// ---------- the field ----------

// wordsInput is the field's text after a keystroke.
func (e *scoreState) wordsInput(text string) {
	if e.Words == nil {
		return
	}
	switch e.Words.Kind {
	case scoreWordLyric:
		e.lyricInput(text)
	case scoreWordChord:
		e.chordInput(text)
	}
}

// wordsKey is a key the field acts on.
func (e *scoreState) wordsKey(k scoreWordKey) {
	w := e.Words
	if w == nil {
		return
	}
	switch k {
	case scoreWordEnter, scoreWordEscape:
		ok := true
		if w.Kind == scoreWordLyric {
			ok = e.commitLyric(w.Text, false, false)
		} else if k == scoreWordEnter || strings.TrimSpace(w.Text) != "" {
			ok = e.commitChord(w.Text)
		}
		if ok || k == scoreWordEscape {
			e.Words = nil
			e.Status = e.describeSel()
		}
	case scoreWordTab:
		if w.Kind == scoreWordLyric {
			if e.commitLyric(w.Text, false, false) {
				e.lyricNext()
			}
		} else if e.commitChord(w.Text) {
			e.chordNext()
		}
	case scoreWordBackspace:
		if w.Kind == scoreWordLyric {
			if p, ok := edit.PrevSung(e.Score, w.Loc); ok {
				w.Loc, w.Focus = p, true
				e.loadLyric()
				e.Sel = scoreSel{Kind: scoreSelEvent, Loc: p, Note: -1}
			}
		} else if p, ok := edit.PrevChordPlace(e.Score, w.Line, w.At); ok {
			e.editChordAt(w.Line, p)
		}
		e.Status = e.wordsStatus()
	case scoreWordNextVerse:
		if w.Kind == scoreWordLyric {
			e.commitLyric(w.Text, false, false)
			w.Verse = w.Verse%score.MaxVerses + 1
			w.Focus = true
			e.loadLyric()
			e.Status = e.wordsStatus()
		}
	}
}

// wordsStatus says what the field types.
func (e *scoreState) wordsStatus() string {
	w := e.Words
	if w == nil {
		return ""
	}
	if w.Kind == scoreWordLyric {
		return fmt.Sprintf("Lyrics, verse %d, %s bar %d: Space next note, - hyphen, _ melisma, Enter done", w.Verse, e.lineName(w.Loc.Line), w.Loc.Bar)
	}
	return fmt.Sprintf("Chord symbol, %s at %s: Space next beat, Enter done (e.g. Cmaj7, F#m7b5, Bb7(b9)/D, N.C.)", e.lineName(w.Line), e.Score.FormatPos(w.At))
}

// ---------- drums ----------

// kitOf is the drum map a line's percussion staves read.
func (e *scoreState) kitOf(line int) *score.Kit {
	if l := e.line(line); l != nil {
		return l.Kit()
	}
	return score.GMKit
}

// drumLetter is a letter on a percussion staff: the kit's sound with that
// shortcut, written (in note input), put in place of the selected note,
// or (add) added to the chord. It reports whether the staff is one.
func (e *scoreState) drumLetter(step int, add bool) bool {
	l, ok := e.drumLoc()
	if !ok {
		return false
	}
	letter := score.Steps[step]
	kit := e.kitOf(l.Line)
	d, found := kit.ByShortcut(letter)
	if !found {
		e.Status = fmt.Sprintf("%s has no drum on %c", kit.Name, letter)
		return true
	}
	p := score.DrumPitch(d)
	rev := e.Rev
	switch {
	case add:
		if e.chord() == nil {
			e.Status = "Select a drum note to add to"
			return true
		}
		e.addPitch(p)
	case e.Input || e.chord() == nil:
		loc, off, ok := e.caret()
		if !e.Input && e.Sel.Kind == scoreSelEvent && e.chord() == nil {
			// A rest: the drum goes where it is.
			off, _ = edit.Onset(e.Score, e.Sel.Loc)
			loc, ok = edit.Loc{Line: l.Line, Staff: l.Staff, Voice: e.Sel.Loc.Voice, Bar: e.Sel.Loc.Bar}, true
		}
		if !ok {
			e.Status = "Select a bar or a note first"
			return true
		}
		e.Input = true
		e.write(loc, off, &p)
	default:
		k := e.Sel.Note
		if ev := e.chord(); k < 0 || k >= len(ev.Notes) {
			k = len(ev.Notes) - 1
		}
		e.transform(k, func(score.Pitch, int) score.Pitch { return p })
	}
	if e.Rev != rev {
		e.Status = d.Name + ": " + e.describeSel()
	}
	return true
}

// drumLoc is the selection's place when it is on a percussion staff.
func (e *scoreState) drumLoc() (edit.Loc, bool) {
	var l edit.Loc
	switch e.Sel.Kind {
	case scoreSelEvent, scoreSelCaret:
		l = e.Sel.Loc
	case scoreSelBars:
		l = edit.Loc{Line: e.Sel.LineA, Staff: e.Sel.Staff}
	default:
		return l, false
	}
	return l, e.percussion(l.Line, l.Staff)
}

// drumStep walks the selected drum note (the chord's top one) to the
// next sound of the kit up or down. It reports whether the selection is a
// drum note.
func (e *scoreState) drumStep(dir int) bool {
	ev := e.chord()
	if ev == nil || !e.percussion(e.Sel.Loc.Line, e.Sel.Loc.Staff) {
		return false
	}
	kit := e.kitOf(e.Sel.Loc.Line)
	k := e.Sel.Note
	if k < 0 || k >= len(ev.Notes) {
		k = len(ev.Notes) - 1
	}
	d, ok := kit.Step(ev.Notes[k].Pitch.MIDI(), dir)
	if !ok {
		e.Status = "No drum further that way in " + kit.Name
		return true
	}
	if e.transform(k, func(score.Pitch, int) score.Pitch { return score.DrumPitch(d) }) {
		e.Status = d.Name
	}
	return true
}

// ---------- tablature ----------

// tabFocus reports that the keyboard is the tablature's: the selection is
// on a staff with tablature, picked there (a click on the strings).
func (e *scoreState) tabFocus() bool {
	if !e.TabKeys {
		return false
	}
	l, _, ok := e.caret()
	if e.Sel.Kind == scoreSelEvent {
		l, ok = e.Sel.Loc, true
	}
	if !ok {
		return false
	}
	st := edit.Staff(e.Score, l.Line, l.Staff)
	return st != nil && st.Tab != nil
}

// tabStaff is the selection's staff's tablature, if it has one.
func (e *scoreState) tabStaff() (*score.Staff, edit.Loc, bool) {
	l := e.Sel.Loc
	if e.Sel.Kind != scoreSelEvent {
		var ok bool
		if l, _, ok = e.caret(); !ok {
			return nil, l, false
		}
	}
	st := edit.Staff(e.Score, l.Line, l.Staff)
	return st, l, st != nil && st.Tab != nil
}

// clickTab is a click on a tablature's string in note input: that open
// string's note at the place, kept on the string, the hand on it.
func (e *scoreState) clickTab(line, staff, bar int, at score.Frac, str int) bool {
	st := edit.Staff(e.Score, line, staff)
	if st == nil || st.Tab == nil {
		return false
	}
	p, err := edit.FretPitch(st.Tab, str, 0, e.fifths(bar))
	if err != nil {
		e.Status = scoreErrText(err)
		return false
	}
	e.TabString, e.TabKeys = str, true
	written := e.toWritten(line, staff, p)
	if !e.write(edit.Loc{Line: line, Staff: staff, Voice: e.Voice, Bar: bar}, at, &written) {
		return false
	}
	if ev := e.chord(); ev != nil && len(ev.Notes) == 1 {
		e.changeLast(func() error { return edit.SetTabString(e.Score, e.Sel.Loc, 0, str) })
	}
	e.Status = fmt.Sprintf("String %d: type the fret", str)
	return true
}

// changeLast runs an edit into the newest undo step (a write and the
// string it is kept on are one step).
func (e *scoreState) changeLast(f func() error) {
	if err := f(); err != nil {
		e.Status = scoreErrText(err)
		return
	}
	e.touch()
}

// tabDigit is a number key on the tablature: the fret on the string in
// hand, of the selected chord (a note there changes, or one is added); a
// second digit within a second makes a two-digit fret.
func (e *scoreState) tabDigit(d int) {
	st, l, ok := e.tabStaff()
	if !ok || e.chord() == nil {
		e.Status = "Select a note on the tablature, or click a string in note input"
		return
	}
	str := e.TabString
	if str < 1 || str > st.Tab.Strings() {
		str = e.selectedString()
	}
	fret := d
	now := time.Now()
	if f := e.fretTyped; f.loc == l && f.str == str && now.Sub(f.at) < time.Second && f.fret*10+d <= score.MaxFret-st.Tab.Capo && f.fret > 0 {
		fret = f.fret*10 + d
	}
	if e.change(func() error {
		k, err := edit.SetFret(e.Score, l, str, fret, e.fifths(l.Bar))
		if err != nil {
			return err
		}
		e.Sel.Note = k
		return nil
	}) {
		e.fretTyped = scoreFretTyped{loc: l, str: str, fret: fret, at: now}
		e.auditionChord()
		e.Status = fmt.Sprintf("String %d, fret %d: %s", str, fret, e.describeSel())
	}
}

// scoreFretTyped is the last fret typed, for a second digit.
type scoreFretTyped struct {
	loc       edit.Loc
	str, fret int
	at        time.Time
}

// selectedString is the string the selected note is shown on (the
// chord's top note's when the chord is selected), 1 when none.
func (e *scoreState) selectedString() int {
	places := edit.TabPlaces(e.Score, e.Sel.Loc)
	k := e.Sel.Note
	if k < 0 || k >= len(places) {
		k = len(places) - 1
	}
	if k >= 0 && places[k].OK {
		return places[k].String
	}
	return 1
}

// tabMove moves the hand a string up (dir < 0, toward string 1) or down,
// and selects the chord's note on that string, if it has one.
func (e *scoreState) tabMove(dir int) {
	st, _, ok := e.tabStaff()
	if !ok {
		return
	}
	str := e.TabString
	if str < 1 {
		str = e.selectedString()
	}
	str = max(1, min(st.Tab.Strings(), str+dir))
	e.TabString = str
	if e.Sel.Kind == scoreSelEvent {
		e.Sel.Note = -1
		for i, p := range edit.TabPlaces(e.Score, e.Sel.Loc) {
			if p.OK && p.String == str {
				e.Sel.Note = i
			}
		}
	}
	e.Status = fmt.Sprintf("String %d (%s open)", str, edit.PitchName(score.SpellMIDI(st.Tab.Open(str), 0, 1)))
}

// moveToString puts the selected note on another string, keeping its
// pitch (Alt+Up/Down on the tablature).
func (e *scoreState) moveToString(dir int) {
	_, l, ok := e.tabStaff()
	if !ok || e.chord() == nil {
		return
	}
	k := e.Sel.Note
	if k < 0 {
		k = len(e.chord().Notes) - 1
	}
	places := edit.TabPlaces(e.Score, l)
	if k >= len(places) || !places[k].OK {
		e.Status = "That note is not on the strings"
		return
	}
	to := places[k].String + dir
	if e.change(func() error { return edit.SetTabString(e.Score, l, k, to) }) {
		e.TabString = to
		e.Status = fmt.Sprintf("On string %d: %s", to, e.describeSel())
	}
}

// ---------- line settings ----------

// setLineTab gives a line's staff a tablature in a tuning (alone: the
// tablature only), or takes it off (tuning "").
func (e *scoreState) setLineTab(li, staff int, tuning string, alone bool) bool {
	st := edit.Staff(e.Score, li, staff)
	if st == nil {
		return false
	}
	var t *score.Tab
	if tuning != "" {
		t = score.NewTab(tuning)
		if st.Tab != nil && tuning == st.Tab.Preset() {
			t.Capo = st.Tab.Capo
		}
		t.Only = alone
	}
	if e.change(func() error { return edit.SetTab(e.Score, li, staff, t) }) {
		e.laneRev++
		switch {
		case t == nil:
			e.Status = e.lineName(li) + ": no tablature"
		case alone:
			e.Status = fmt.Sprintf("%s: tablature alone (%s)", e.lineName(li), tuning)
		default:
			e.Status = fmt.Sprintf("%s: tablature under the notation (%s)", e.lineName(li), tuning)
		}
		return true
	}
	return false
}

// setTabCapo moves a tablature's capo.
func (e *scoreState) setTabCapo(li, staff, capo int) bool {
	st := edit.Staff(e.Score, li, staff)
	if st == nil || st.Tab == nil {
		return false
	}
	t := st.Tab.Clone()
	t.Capo = max(0, min(12, capo))
	if e.change(func() error { return edit.SetTab(e.Score, li, staff, t) }) {
		e.Status = fmt.Sprintf("%s: capo at %d", e.lineName(li), t.Capo)
		return true
	}
	return false
}

// lineTabTuning is the tuning a line's instrument names for tablature
// (its .finst playback tuning), guitar when it names none.
func (e *scoreState) lineTabTuning(li int) string {
	if l := e.line(li); l != nil {
		if in, err := play.LoadInstrument(l.Instrument); err == nil && in != nil {
			if _, ok := score.FindTuning(in.Playback.Tuning); ok {
				return in.Playback.Tuning
			}
		}
	}
	return "guitar"
}

// setChordPlay sets how a line's chord symbols play: a style (off, pad,
// comp) on an instrument ("" the line's own).
func (e *scoreState) setChordPlay(li int, style score.ChordStyle, instrument string) bool {
	if e.line(li) == nil {
		return false
	}
	ok := e.change(func() error {
		l := e.Score.Lines[li]
		if style == score.ChordsSilent && instrument == "" {
			l.ChordPlay = nil
			return nil
		}
		cp := score.ChordPlay{Volume: 1}
		if l.ChordPlay != nil {
			cp = *l.ChordPlay
		}
		cp.Style, cp.Instrument = style, instrument
		l.ChordPlay = &cp
		return nil
	})
	if ok {
		what := score.ChordStyles[style]
		if style != score.ChordsSilent {
			on := instrument
			if on == "" {
				on = "the line's instrument"
			}
			what = "a " + what + " on " + on
		}
		e.Status = fmt.Sprintf("%s: chord symbols play %s", e.lineName(li), what)
	}
	return ok
}

// clearLyrics takes every verse's syllables off the selected bars.
func (e *scoreState) clearLyrics() {
	q := e.Sel
	if q.Kind != scoreSelBars {
		e.Status = "Select bars to clear their lyrics"
		return
	}
	n := 0
	e.change(func() error {
		if n = edit.ClearLyrics(e.Score, q.LineA, q.LineB, q.BarA, q.BarB, 0); n == 0 {
			return fmt.Errorf("no lyrics in the selection")
		}
		return nil
	})
	if n > 0 {
		e.Status = fmt.Sprintf("%d syllable(s) cleared", n)
	}
}
