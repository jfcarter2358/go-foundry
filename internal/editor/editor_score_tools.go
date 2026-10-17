package editor

// editor_score_tools.go — the Score editor's tools over a passage or the
// whole score: transpose (by an interval or to a key, chromatic or
// diatonic), respell (each note to its next spelling, or every note as the
// key spells it), find (a pitch, a text, a mark) and the command palette.
// The model, with no ImGui (the dialogs are editor_score_tools_view.go).
//
// What a tool works on is the selection: selected bars of selected lines,
// or the selected note's chord; with nothing selected, or "whole score"
// asked for, every line and bar. Pitches are compared and spelled as the
// page shows them (a transposing staff's written pitch, unless the view is
// in concert pitch), as every other pitch command is.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/edit"
)

// ---------- the range a tool works on ----------

// scoreRange is lines lo..hi, bars a..z.
type scoreRange struct {
	Lo, Hi, A, Z int
	// Whole is every line and bar of the score.
	Whole bool
}

// toolRange is what a tool works on: the selection, or the whole score.
func (e *scoreState) toolRange(whole bool) scoreRange {
	n := len(e.Score.Lines) - 1
	all := scoreRange{Lo: 0, Hi: n, A: 1, Z: e.Score.Measures(), Whole: true}
	if whole {
		return all
	}
	switch e.Sel.Kind {
	case scoreSelBars:
		q := e.Sel
		r := scoreRange{Lo: q.LineA, Hi: q.LineB, A: q.BarA, Z: q.BarB}
		r.Whole = r.Lo == 0 && r.Hi == n && r.A == 1 && r.Z == e.Score.Measures()
		return r
	case scoreSelEvent, scoreSelCaret:
		l := e.Sel.Loc
		return scoreRange{Lo: l.Line, Hi: l.Line, A: l.Bar, Z: l.Bar}
	}
	return all
}

// describeRange is a range in words: "the whole score", "Flute, bars 2-4".
func (e *scoreState) describeRange(r scoreRange) string {
	if r.Whole {
		return "the whole score"
	}
	who := e.lineName(r.Lo)
	if r.Hi > r.Lo {
		who = fmt.Sprintf("%s to %s", e.lineName(r.Lo), e.lineName(r.Hi))
		if r.Lo == 0 && r.Hi == len(e.Score.Lines)-1 {
			who = "all lines"
		}
	}
	if r.A == r.Z {
		return fmt.Sprintf("%s, bar %d", who, r.A)
	}
	return fmt.Sprintf("%s, bars %d-%d", who, r.A, r.Z)
}

// ---------- transpose ----------

// scoreTransposeOpts is the Transpose dialog's choices.
type scoreTransposeOpts struct {
	// ByKey transposes to Fifths (major or minor as the score's key is),
	// Dir 1 up, -1 down, 0 the closer; otherwise by Interval.
	ByKey  bool
	Fifths int
	Dir    int
	// Interval is the chromatic interval (Diatonic false), or Steps the
	// letters a diatonic transposition moves (Diatonic true).
	Interval score.Interval
	Diatonic bool
	Steps    int
	// Whole is the whole score, not the selection.
	Whole bool
	// Keys moves the key signatures too (a chromatic transposition of every
	// line).
	Keys bool
	// NoChords leaves chord symbols; Doubles keeps double sharps and flats.
	NoChords, Doubles bool
}

// transposeInterval is the chromatic interval the options mean for a
// range: theirs, or the one from the range's key to the chosen key.
func (e *scoreState) transposeInterval(o scoreTransposeOpts, r scoreRange) score.Interval {
	if !o.ByKey {
		return o.Interval
	}
	k := e.Score.KeyAt(r.A)
	return edit.KeyInterval(k.Fifths, o.Fifths, k.Mode, o.Dir)
}

// transposeSel is the dialog's OK: one undo step.
func (e *scoreState) transposeSel(o scoreTransposeOpts) bool {
	if len(e.Score.Lines) == 0 {
		e.Status = "Nothing to transpose"
		return false
	}
	r := e.toolRange(o.Whole)
	t := edit.Transposition{Diatonic: o.Diatonic && !o.ByKey, Steps: o.Steps, NoChords: o.NoChords, Doubles: o.Doubles}
	if !t.Diatonic {
		t.Interval = e.transposeInterval(o, r)
		// Key signatures belong to every line: they move when every line does.
		t.Keys = (o.Keys || o.ByKey) && r.Lo == 0 && r.Hi == len(e.Score.Lines)-1
	}
	var res edit.TransposeResult
	keep := e.Sel
	if !e.change(func() error {
		var err error
		res, err = edit.Transpose(e.Score, r.Lo, r.Hi, r.A, r.Z, t)
		return err
	}) {
		return false
	}
	e.Sel = keep
	e.dropStaleSel()
	what := fmt.Sprintf("%d step(s)", t.Steps)
	if !t.Diatonic {
		what = res.Interval.String()
		if o.ByKey {
			what = fmt.Sprintf("to %s (%s)", scoreKeyName(o.Fifths, e.Score.KeyAt(r.A).Mode), res.Interval)
		}
	}
	e.Status = fmt.Sprintf("Transposed %s %s: %d note(s)", e.describeRange(r), what, res.Moved)
	if res.Kept > 0 {
		e.Status += fmt.Sprintf("; %d would leave the range and stayed", res.Kept)
	}
	if t.Keys {
		e.Status += ", key signatures with them"
	}
	return true
}

// ---------- respell ----------

// eachChordIn visits every chord of a range in each voice's order, with
// its bar's written key.
func (e *scoreState) eachChordIn(r scoreRange, f func(l edit.Loc, ev *score.Event, fifths int)) {
	for li := r.Lo; li <= r.Hi && li < len(e.Score.Lines); li++ {
		for si, st := range e.Score.Lines[li].Staves {
			if st.Percussion() {
				continue
			}
			for vi, v := range st.Voices {
				for b := r.A; b <= r.Z && b <= len(v.Measures); b++ {
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

// respellSel gives every note of the selection its next spelling (F# to
// Gb, Gb to F#): one undo step. With one note of a chord selected, that
// note alone.
func (e *scoreState) respellSel() bool {
	if e.Sel.Kind == scoreSelEvent {
		if e.chord() == nil {
			e.Status = "Select notes to respell"
			return false
		}
		return e.transform(e.Sel.Note, func(p score.Pitch, _ int) score.Pitch { return edit.Respell(p) })
	}
	return e.respellRange(e.toolRange(e.Sel.Kind == scoreSelNone), "Respelled", func(p score.Pitch, _, _ int) score.Pitch {
		return edit.Respell(p)
	})
}

// spellInKeySel spells every note of the selection as its bar's key spells
// the sound — the key's note, else a sharp when the line rises to it and a
// flat when it falls: what an import or a careless transposition leaves
// tidied up.
func (e *scoreState) spellInKeySel(whole bool) bool {
	return e.respellRange(e.toolRange(whole || e.Sel.Kind == scoreSelNone), "Spelled in the key", func(p score.Pitch, fifths, dir int) score.Pitch {
		return edit.SpellInKey(p, fifths, dir)
	})
}

// respellRange respells a range's notes by f (written pitch, the written
// key, and which way the line moved to the note), counting the ones whose
// spelling changed.
func (e *scoreState) respellRange(r scoreRange, verb string, f func(p score.Pitch, fifths, dir int) score.Pitch) bool {
	changed := 0
	keep := e.Sel
	ok := e.change(func() error {
		type voiceKey struct{ line, staff, voice int }
		prev := map[voiceKey]int{}
		e.eachChordIn(r, func(l edit.Loc, ev *score.Event, fifths int) {
			k := voiceKey{l.Line, l.Staff, l.Voice}
			last, had := prev[k]
			for i, n := range ev.Notes {
				w := e.toWritten(l.Line, l.Staff, n.Pitch)
				dir := 0
				if had {
					switch m := w.MIDI(); {
					case m > last:
						dir = 1
					case m < last:
						dir = -1
					}
				}
				q := f(w, fifths, dir)
				if q != w && q.MIDI() == w.MIDI() {
					ev.Notes[i].Pitch = e.toConcert(l.Line, l.Staff, q)
					changed++
				}
			}
			prev[k] = e.toWritten(l.Line, l.Staff, ev.Notes[len(ev.Notes)-1].Pitch).MIDI()
		})
		if changed == 0 {
			return fmt.Errorf("every note there is spelled so already")
		}
		return nil
	})
	if !ok {
		return false
	}
	e.Sel = keep
	e.Status = fmt.Sprintf("%s %d note(s) in %s", verb, changed, e.describeRange(r))
	return true
}

// ---------- cross-staff ----------

// moveStaff is Ctrl+Shift+Up/Down: the selected chord's notes (or every
// chord of the selected bars) onto the staff above or below, or back.
func (e *scoreState) moveStaff(dir int) bool {
	if e.Sel.Kind == scoreSelEvent {
		if e.chord() == nil {
			e.Status = "Select notes to put on another staff"
			return false
		}
		l := e.Sel.Loc
		if !e.change(func() error { return edit.MoveStaff(e.Score, l, dir) }) {
			return false
		}
		switch c := e.event().CrossStaff(); {
		case c < 0:
			e.Status = "Written on the staff above (its voice is still this staff's)"
		case c > 0:
			e.Status = "Written on the staff below (its voice is still this staff's)"
		default:
			e.Status = "Back on its own staff"
		}
		return true
	}
	if e.Sel.Kind != scoreSelBars {
		e.Status = "Select notes or bars to put on another staff"
		return false
	}
	moved := 0
	ok := e.change(func() error {
		e.eachChordIn(e.toolRange(false), func(l edit.Loc, ev *score.Event, _ int) {
			if edit.MoveStaff(e.Score, l, dir) == nil {
				moved++
			}
		})
		if moved == 0 {
			return fmt.Errorf("no note there has a staff %s it", map[bool]string{true: "above", false: "below"}[dir < 0])
		}
		return nil
	})
	if ok {
		e.Status = fmt.Sprintf("Moved %d chord(s) %s", moved, map[bool]string{true: "up a staff", false: "down a staff"}[dir < 0])
	}
	return ok
}

// ---------- find ----------

// scoreFindKind is what a query looks for.
type scoreFindKind uint8

const (
	// scoreFindAny tries the query as a pitch, then a mark, then text.
	scoreFindAny scoreFindKind = iota
	scoreFindPitch
	scoreFindText
	scoreFindMark
)

var scoreFindKindNames = []string{"Anything", "Pitch", "Text", "Mark"}

// scoreFindHit is one thing found: an event (one of its notes for a
// pitch), or a place in a bar (a chord symbol, a rehearsal mark, a tempo
// mark) on a line (-1: every line).
type scoreFindHit struct {
	Loc  edit.Loc
	Note int
	// Event is false for a place; Line, Bar and At are then where it is.
	Event bool
	Line  int
	Bar   int
	At    score.Frac
	// What says what matched, for the results list.
	What string
}

// scoreFind is the Find dialog's state.
type scoreFind struct {
	Query  string
	Kind   scoreFindKind
	Exact  bool // a pitch's spelling must match, not only its sound
	InSel  bool // only in the selection
	Hits   []scoreFindHit
	Cur    int
	Err    string
	search scoreRange
}

// scoreMarkWords are the names a mark is found by: what a musician calls
// it, and what the file calls it.
var scoreMarkWords = map[string][]string{
	"staccato": {"stacc"}, "staccatissimo": {"staccmo"}, "accent": {"acc"}, "tenuto": {"ten"},
	"marcato": {"marc"}, "portato": {"portato"}, "legato": {"legato"},
	"trill": {"tr"}, "mordent": {"mord", "imord"}, "turn": {"turn", "iturn"}, "tremolo": {"trem", "trem2"},
	"fermata": {"ferm"}, "pause": {"ferm"}, "arpeggio": {"arp"},
	"slur": {"slur"}, "glissando": {"gliss"}, "gliss": {"gliss"},
	"hairpin": {"cresc", "dim"}, "crescendo": {"cresc"}, "diminuendo": {"dim"}, "decrescendo": {"dim"},
	"ottava": {"8va", "8vb", "15ma", "15mb"}, "pedal": {"ped"}, "open": {"open"}, "stopped": {"stopped"},
	"technique": {"tech"}, "expression": {"expr"}, "text": {"text", "expr", "tech"},
	"scoop": {"scoop"}, "plop": {"plop"}, "doit": {"doit"}, "fall": {"fall"}, "falloff": {"fall"},
	"bend": {"bend"}, "jazz": {"scoop", "plop", "doit", "fall", "bend"},
}

// scoreFindMarks are the mark names a word finds, or nil. "dynamic" finds
// every dynamic, "grace" "tie" and "tuplet" are found by the event.
func scoreFindMarks(word string) ([]string, bool) {
	w := strings.ToLower(strings.TrimSpace(word))
	switch w {
	case "dynamic", "dynamics", "grace", "graces", "grace note", "tie", "ties", "tuplet", "tuplets":
		return []string{w}, true
	}
	if names, ok := scoreMarkWords[w]; ok {
		return names, true
	}
	if _, ok := score.Dynamics[w]; ok {
		return []string{w}, true
	}
	if score.KindOf(w) != score.MarkUnknown {
		return []string{w}, true
	}
	return nil, false
}

// scorePitchQuery is a pitch to find: a whole pitch (Octave known), a
// pitch class (F#, any octave), or a range of sounds (C4-G4).
type scorePitchQuery struct {
	P, To  score.Pitch
	Octave bool
	Range  bool
}

// parsePitchQuery reads "F#4", "Gb", "C4-G5".
func parsePitchQuery(q string) (scorePitchQuery, bool) {
	s := strings.TrimSpace(q)
	for _, sep := range []string{"..", " to ", "–", "-"} {
		if a, b, ok := strings.Cut(s, sep); ok {
			pa, ea := score.ParsePitch(strings.TrimSpace(a))
			pb, eb := score.ParsePitch(strings.TrimSpace(b))
			if ea == nil && eb == nil {
				return scorePitchQuery{P: pa, To: pb, Octave: true, Range: true}, true
			}
		}
	}
	if p, err := score.ParsePitch(s); err == nil {
		return scorePitchQuery{P: p, Octave: true}, true
	}
	// No octave: a letter and its accidentals.
	if p, err := score.ParsePitch(s + "4"); err == nil && len(s) <= 3 {
		return scorePitchQuery{P: p}, true
	}
	return scorePitchQuery{}, false
}

// matches reports a written pitch matching the query.
func (q scorePitchQuery) matches(p score.Pitch, exact bool) bool {
	switch {
	case q.Range:
		lo, hi := q.P.MIDI(), q.To.MIDI()
		if lo > hi {
			lo, hi = hi, lo
		}
		return p.MIDI() >= lo && p.MIDI() <= hi
	case exact && q.Octave:
		return p == q.P
	case exact:
		return p.Step == q.P.Step && p.Alter == q.P.Alter
	case q.Octave:
		return p.MIDI() == q.P.MIDI()
	}
	return ((p.MIDI()-q.P.MIDI())%12+12)%12 == 0
}

// findAll is every match of the dialog's query, in reading order: by bar,
// then line, staff, voice and event.
func (e *scoreState) findAll(f *scoreFind) []scoreFindHit {
	f.Err = ""
	query := strings.TrimSpace(f.Query)
	if query == "" {
		f.Err = "Type a pitch (F#4, Bb, C4..G5), a mark (staccato, fermata, p) or a text"
		return nil
	}
	r := e.toolRange(!f.InSel || e.Sel.Kind == scoreSelNone)
	f.search = r
	kind := f.Kind
	pq, isPitch := parsePitchQuery(query)
	marks, isMark := scoreFindMarks(query)
	if kind == scoreFindAny {
		switch {
		case isPitch:
			kind = scoreFindPitch
		case isMark:
			kind = scoreFindMark
		default:
			kind = scoreFindText
		}
	}
	switch {
	case kind == scoreFindPitch && !isPitch:
		f.Err = fmt.Sprintf("%q is not a pitch: try F#4, Bb or C4..G5", query)
		return nil
	case kind == scoreFindMark && !isMark:
		f.Err = fmt.Sprintf("No mark is called %q", query)
		return nil
	}
	low := strings.ToLower(query)
	var hits []scoreFindHit
	for li := r.Lo; li <= r.Hi && li < len(e.Score.Lines); li++ {
		line := e.Score.Lines[li]
		for si, st := range line.Staves {
			for vi, v := range st.Voices {
				for b := r.A; b <= r.Z && b <= len(v.Measures); b++ {
					for i := range v.Measures[b-1].Events {
						ev := &v.Measures[b-1].Events[i]
						l := edit.Loc{Line: li, Staff: si, Voice: vi, Bar: b, Event: i}
						hits = append(hits, e.findInEvent(l, ev, kind, pq, marks, low, f.Exact)...)
					}
				}
			}
		}
		if kind == scoreFindText || kind == scoreFindPitch {
			for _, c := range line.Chords {
				if c.At.Bar < r.A || c.At.Bar > r.Z {
					continue
				}
				text := c.Chord.String()
				if kind == scoreFindText && strings.Contains(strings.ToLower(text), low) ||
					kind == scoreFindPitch && !pq.Range && !pq.Octave && !c.Chord.NoChord && scoreChordRootMatch(c.Chord.Root, pq, f.Exact) {
					hits = append(hits, scoreFindHit{Line: li, Bar: c.At.Bar, At: c.At.Offset, What: "chord symbol " + text})
				}
			}
		}
	}
	if kind == scoreFindText {
		for _, sec := range e.Score.Sections {
			if sec.Bar >= r.A && sec.Bar <= r.Z && strings.Contains(strings.ToLower(sec.Name), low) {
				hits = append(hits, scoreFindHit{Line: -1, Bar: sec.Bar, What: "rehearsal mark " + sec.Name})
			}
		}
		for _, t := range e.Score.Tempo {
			if t.At.Bar >= r.A && t.At.Bar <= r.Z && t.Text != "" && strings.Contains(strings.ToLower(t.Text), low) {
				hits = append(hits, scoreFindHit{Line: -1, Bar: t.At.Bar, At: t.At.Offset, What: "tempo " + t.Text})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return e.findBefore(hits[i], hits[j]) })
	return hits
}

// scoreChordRootMatch reports a chord symbol's root matching a pitch class.
func scoreChordRootMatch(root score.Pitch, q scorePitchQuery, exact bool) bool {
	if exact {
		return root.Step == q.P.Step && root.Alter == q.P.Alter
	}
	r := root
	r.Octave = 4
	return ((r.MIDI()-q.P.MIDI())%12+12)%12 == 0
}

// findInEvent is an event's matches.
func (e *scoreState) findInEvent(l edit.Loc, ev *score.Event, kind scoreFindKind, pq scorePitchQuery, marks []string, low string, exact bool) []scoreFindHit {
	hit := func(note int, what string) scoreFindHit {
		return scoreFindHit{Loc: l, Note: note, Event: true, Line: l.Line, Bar: l.Bar, What: what}
	}
	var out []scoreFindHit
	switch kind {
	case scoreFindPitch:
		if ev.Kind != score.EventChord || e.percussion(l.Line, l.Staff) {
			return nil
		}
		for k, n := range ev.Notes {
			w := e.toWritten(l.Line, l.Staff, n.Pitch)
			if pq.matches(w, exact) {
				out = append(out, hit(k, edit.PitchName(w)))
			}
		}
	case scoreFindMark:
		for _, name := range marks {
			switch name {
			case "dynamic", "dynamics":
				for _, m := range ev.Marks {
					if m.Kind() == score.MarkDynamic {
						return []scoreFindHit{hit(-1, m.Name)}
					}
				}
			case "grace", "graces", "grace note":
				if ev.Grace != score.NotGrace {
					return []scoreFindHit{hit(-1, "grace note")}
				}
			case "tie", "ties":
				for k, n := range ev.Notes {
					if n.Tie {
						out = append(out, hit(k, "tie"))
					}
				}
				return out
			case "tuplet", "tuplets":
				if ev.Tuplet > 0 {
					return []scoreFindHit{hit(-1, "tuplet")}
				}
			default:
				if ev.Has(name) {
					return []scoreFindHit{hit(-1, name)}
				}
			}
		}
	case scoreFindText:
		for _, m := range ev.Marks {
			k := m.Kind()
			if (k == score.MarkText || k == score.MarkTechnique) && strings.Contains(strings.ToLower(m.Value), low) {
				return []scoreFindHit{hit(-1, fmt.Sprintf("%q", m.Value))}
			}
		}
		for _, ly := range ev.Lyrics() {
			if strings.Contains(strings.ToLower(ly.Text), low) {
				return []scoreFindHit{hit(-1, fmt.Sprintf("lyric %q (verse %d)", ly.Text, ly.Verse))}
			}
		}
	}
	return out
}

// findBefore orders hits for reading: bar, offset, line, staff, voice,
// event, note.
func (e *scoreState) findBefore(a, b scoreFindHit) bool {
	if a.Bar != b.Bar {
		return a.Bar < b.Bar
	}
	ao, bo := a.At, b.At
	if a.Event {
		ao, _ = edit.Onset(e.Score, a.Loc)
	}
	if b.Event {
		bo, _ = edit.Onset(e.Score, b.Loc)
	}
	if !ao.Eq(bo) {
		return ao.Less(bo)
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	if a.Loc.Staff != b.Loc.Staff {
		return a.Loc.Staff < b.Loc.Staff
	}
	if a.Loc.Voice != b.Loc.Voice {
		return a.Loc.Voice < b.Loc.Voice
	}
	if a.Loc.Event != b.Loc.Event {
		return a.Loc.Event < b.Loc.Event
	}
	return a.Note < b.Note
}

// find runs the query and selects the first match from the selection on
// (the first of all when none is after it).
func (e *scoreState) find(f *scoreFind) bool {
	f.Hits = e.findAll(f)
	f.Cur = -1
	if f.Err != "" {
		e.Status = f.Err
		return false
	}
	if len(f.Hits) == 0 {
		e.Status = fmt.Sprintf("No %q in %s", f.Query, e.describeRange(f.search))
		return false
	}
	return e.findStep(f, 1)
}

// findStep is Find Next (dir 1) and Find Previous (-1): the match after (or
// before) the one last shown, wrapping round.
func (e *scoreState) findStep(f *scoreFind, dir int) bool {
	if len(f.Hits) == 0 {
		if f.Query == "" {
			e.Status = "Nothing to find yet: Ctrl+F"
			return false
		}
		return e.find(f)
	}
	if f.Cur < 0 {
		f.Cur = 0
		// From the selection on: the first match at or after it.
		if e.Sel.Kind != scoreSelNone {
			here := scoreFindHit{Event: true, Loc: e.Sel.Loc, Line: e.selLine(), Bar: e.selBar()}
			if e.Sel.Kind == scoreSelBars {
				here = scoreFindHit{Line: e.Sel.LineA, Bar: e.Sel.BarA}
			}
			for i, h := range f.Hits {
				if !e.findBefore(h, here) {
					f.Cur = i
					break
				}
			}
		}
		if dir < 0 {
			f.Cur = (f.Cur - 1 + len(f.Hits)) % len(f.Hits)
		}
	} else {
		f.Cur = (f.Cur + dir + len(f.Hits)) % len(f.Hits)
	}
	h := f.Hits[f.Cur]
	e.showHit(h)
	e.Status = fmt.Sprintf("Found %d of %d: %s, %s", f.Cur+1, len(f.Hits), h.What, e.hitWhere(h))
	return true
}

// hitWhere is where a match is: "Flute, bar 3, beat 2", "bar 9" (every line).
func (e *scoreState) hitWhere(h scoreFindHit) string {
	at := h.At
	if h.Event {
		at, _ = edit.Onset(e.Score, h.Loc)
	}
	where := fmt.Sprintf("bar %d, %s", h.Bar, scoreBeatText(e.Score, h.Bar, at))
	if h.Line >= 0 {
		where = e.lineName(h.Line) + ", " + where
	}
	return where
}

// showHit selects a match and scrolls to it.
func (e *scoreState) showHit(h scoreFindHit) {
	if h.Event && edit.Event(e.Score, h.Loc) != nil {
		e.Sel = scoreSel{Kind: scoreSelEvent, Loc: h.Loc, Note: h.Note}
		e.Voice = h.Loc.Voice
	} else {
		lo, hi := h.Line, h.Line
		if h.Line < 0 {
			lo, hi = 0, len(e.Score.Lines)-1
		}
		e.Sel = scoreSel{Kind: scoreSelBars, LineA: lo, LineB: hi, BarA: h.Bar, BarB: h.Bar}
	}
	e.Reveal = true
}

// ---------- the command palette ----------

// scorePaletteItem is one thing the palette runs: a command (with its
// keys) or a menu action no key is bound to.
type scorePaletteItem struct {
	Label, Group string
	Cmd          scoreCmd
	Run          func()
}

// keys is an item's shortcut in the active keymap.
func (it scorePaletteItem) keys() string {
	if it.Cmd == scNone {
		return ""
	}
	return scoreKeymapActive().label(it.Cmd)
}

// scorePaletteItems is every command, in the overlay's group order, then
// the menus' other actions.
func scorePaletteItems() []scorePaletteItem {
	var out []scorePaletteItem
	for _, g := range scoreCmdGroups {
		for _, c := range scoreGroupCmds(g) {
			out = append(out, scorePaletteItem{Label: scoreCmdInfos[c].Label, Group: g, Cmd: c})
		}
	}
	for _, t := range scoreTemplates {
		t := t
		out = append(out, scorePaletteItem{Label: "New score: " + t.Name, Group: "File & view", Run: func() { scoreNew(t) }})
	}
	for _, name := range scoreSettings().Recent {
		name := name
		out = append(out, scorePaletteItem{Label: "Open recent: " + name, Group: "File & view", Run: func() {
			if err := scoreLoad(name); err != nil {
				scEd.Status = err.Error()
			}
		}})
	}
	extra := []scorePaletteItem{
		{Label: "Save All", Group: "File & view", Run: func() { EditorSaveAll() }},
		{Label: "Keyboard shortcuts... (keymap, rebinding)", Group: "File & view", Run: func() { scoreShowKeys = true }},
		{Label: "Swing and humanise...", Group: "Playback", Run: scoreOpenFeel},
		{Label: "Tempo slider to 100%", Group: "Playback", Run: func() { scEd.setTempoScale(1) }},
		{Label: "Write the tempo slider as tempo marks", Group: "Playback", Run: func() { scEd.writeTempoScaled() }},
		{Label: "Add a grand staff (piano)", Group: "Entry", Run: func() { scEd.insertLine(len(scEd.Score.Lines), scoreGrandStaffLine()) }},
		{Label: "Add a percussion staff (drum kit)", Group: "Entry", Run: func() { scEd.insertLine(len(scEd.Score.Lines), scorePercussionLine()) }},
		{Label: "Instrument library...", Group: "Entry", Run: func() { scoreOpenLibrary(scEd.selLine(), false) }},
		{Label: "Voice colours on / off", Group: "File & view", Run: func() { scoreVoiceColours = !scoreVoiceColours }},
		{Label: "Select none", Group: "Selection", Run: func() { scEd.Sel = scoreSel{} }},
		{Label: "Find previous", Group: "Selection", Run: func() { scEd.findStep(&scEd.Find, -1) }},
		{Label: "Close the Score window", Group: "File & view", Run: CloseScoreEditorWindow},
	}
	return append(out, extra...)
}

// scorePaletteMatch is the items a query keeps, best first: every word of
// the query must be in the label, the command's name, the group or its
// keys; a word found at the start of the label or the name ranks first,
// then at a word's start, then inside one, then in the group or keys, and
// nearer the front before further in.
func scorePaletteMatch(items []scorePaletteItem, query string) []int {
	words := strings.Fields(strings.ToLower(query))
	type ranked struct{ i, score int }
	// cost is where a word is in a text: its start, a word start in it,
	// inside it, then how far in; -1 when it is not there.
	cost := func(text, w string) int {
		switch at := strings.Index(text, w); {
		case at < 0:
			return -1
		case at == 0:
			return 0
		case strings.Contains(" "+text, " "+w):
			return 1000 + strings.Index(" "+text, " "+w)
		default:
			return 2000 + at
		}
	}
	var out []ranked
	for i, it := range items {
		label := strings.ToLower(it.Label)
		name := ""
		if it.Cmd != scNone {
			name = strings.ReplaceAll(scoreCmdInfos[it.Cmd].Name, "_", " ")
		}
		rest := strings.ToLower(it.Group) + " " + strings.ToLower(it.keys())
		sc, ok := 0, true
		for _, w := range words {
			best := -1
			for _, c := range []int{cost(label, w), cost(name, w)} {
				if c >= 0 && (best < 0 || c < best) {
					best = c
				}
			}
			if best < 0 {
				if k := strings.Index(rest, w); k >= 0 {
					best = 4000 + k
				}
			}
			if best < 0 {
				ok = false
				break
			}
			sc += best
		}
		if ok {
			out = append(out, ranked{i, sc})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].score < out[b].score })
	idx := make([]int, len(out))
	for k, r := range out {
		idx[k] = r.i
	}
	return idx
}

// runPaletteItem runs what the palette picked.
func runPaletteItem(it scorePaletteItem) {
	switch {
	case it.Run != nil:
		it.Run()
	case it.Cmd != scNone:
		scoreRunCmd(it.Cmd)
	}
}
