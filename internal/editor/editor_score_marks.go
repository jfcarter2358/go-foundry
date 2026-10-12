package editor

// editor_score_marks.go — expression in the Score editor's editing model,
// with no ImGui in it: dynamics and expression text, articulations,
// ornaments, tremolos, fermatas and arpeggios, the lines drawn from note to
// note (slurs, hairpins, glissandi, pedal and ottava lines), grace notes,
// moving a note into another voice, tempo marks, rehearsal marks, repeats,
// endings, the navigation marks and clef changes, the concert-pitch view,
// a line's glissando style, and the dynamics lane.
//
// What a mark goes on follows the selection, as in Sibelius:
//
//   - one note: the mark goes on it; a line (slur, hairpin) runs from it to
//     the voice's next note, and the same key again on the note a line
//     starts at carries it one note further;
//   - bars: an articulation or an ornament goes on every note in them (and
//     again takes it off every one); a dynamic or expression text on each
//     line's first event there; a fermata on each voice's last; a slur over
//     each voice's notes there; a hairpin over each line's first voice
//     there; a pedal line on each line's last staff; an ottava line on
//     each staff.
//
// The dynamics lane is view state per line (which lanes are open) plus the
// line's overrides in the score (score.DynOverride): a drag across the lane
// makes one over the beats it covers at the level of the pointer, a drag on
// one moves its level, Delete removes the one selected. A drag changes the
// score as it goes — so the player hears it while it plays — and is one
// undo step when it ends.

import (
	"fmt"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/interpret"
)

// ---------- what the selection covers ----------

// selEvents are the events a mark goes on: the selected event, or every
// event in the selected bars (every staff and voice of the lines), grace
// notes left out; notes only (no rests) when notes.
func (e *scoreState) selEvents(notes bool) []edit.Loc {
	keep := func(ev *score.Event) bool {
		if ev.Grace != score.NotGrace && e.Sel.Kind == scoreSelBars {
			return false
		}
		if notes {
			return ev.Kind == score.EventChord
		}
		return ev.Kind == score.EventChord || ev.Kind == score.EventRest || ev.Kind == score.EventBarRest
	}
	switch e.Sel.Kind {
	case scoreSelEvent:
		if ev := e.event(); ev != nil && keep(ev) {
			return []edit.Loc{e.Sel.Loc}
		}
	case scoreSelBars:
		var out []edit.Loc
		e.eachSelVoice(func(line, staff, voice int, evs []edit.Loc) {
			for _, l := range evs {
				if keep(edit.Event(e.Score, l)) {
					out = append(out, l)
				}
			}
		})
		return out
	}
	return nil
}

// eachSelVoice calls f for every voice of every staff of the selected
// lines with its events in the selected bars, in order.
func (e *scoreState) eachSelVoice(f func(line, staff, voice int, evs []edit.Loc)) {
	q := e.Sel
	if q.Kind != scoreSelBars {
		return
	}
	for li := q.LineA; li <= q.LineB && li < len(e.Score.Lines); li++ {
		for si, st := range e.Score.Lines[li].Staves {
			for vi, v := range st.Voices {
				var evs []edit.Loc
				for b := q.BarA; b <= q.BarB && b <= len(v.Measures); b++ {
					for ei := range v.Measures[b-1].Events {
						evs = append(evs, edit.Loc{Line: li, Staff: si, Voice: vi, Bar: b, Event: ei})
					}
				}
				if len(evs) > 0 {
					f(li, si, vi, evs)
				}
			}
		}
	}
}

// marked runs a change that puts marks on the selection and says so, or
// why not.
func (e *scoreState) marked(what string, f func() (int, error)) bool {
	if e.Sel.Kind == scoreSelNone || e.Sel.Kind == scoreSelCaret {
		e.Status = "Select a note or bars first"
		return false
	}
	n := 0
	ok := e.change(func() error {
		var err error
		n, err = f()
		if err == nil && n == 0 {
			err = fmt.Errorf("nothing in the selection takes %s", what)
		}
		return err
	})
	if ok {
		e.Status = what
		if n > 1 {
			e.Status = fmt.Sprintf("%s (%d)", what, n)
		}
	}
	return ok
}

// ---------- articulations, ornaments, tremolos, arpeggios ----------

// markNames name the marks for the status line.
var markNames = map[string]string{
	"stacc": "Staccato", "staccmo": "Staccatissimo", "portato": "Portato", "ten": "Tenuto", "acc": "Accent",
	"marc": "Marcato", "tr": "Trill", "mord": "Mordent", "imord": "Inverted mordent", "turn": "Turn",
	"iturn": "Inverted turn", "trem": "Tremolo", "trem2": "Two-note tremolo", "arp": "Arpeggio",
	"gliss": "Glissando", "ferm": "Fermata", "legato": "Legato",
}

// toggleMark is an articulation, ornament, tremolo or arpeggio key or
// palette button: on every selected note, or — when every one has it with
// that value — off every one.
func (e *scoreState) toggleMark(name, value string) bool {
	locs := e.selEvents(true)
	label := markNames[name]
	if label == "" {
		label = name
	}
	if len(locs) == 0 {
		e.Status = "Select a note (or bars) for the " + strings.ToLower(label)
		return false
	}
	all := true
	for _, l := range locs {
		ev := edit.Event(e.Score, l)
		has := false
		for _, m := range ev.Marks {
			if m.Name == name && m.Value == value {
				has = true
			}
		}
		all = all && has
	}
	verb := label
	if all {
		verb = label + " off"
	}
	return e.marked(verb, func() (int, error) {
		n := 0
		for _, l := range locs {
			ev := edit.Event(e.Score, l)
			has := false
			for _, m := range ev.Marks {
				if m.Name == name && m.Value == value {
					has = true
				}
			}
			if has != all {
				continue
			}
			if _, err := edit.ToggleArticulation(e.Score, l, name, value); err != nil {
				return n, err
			}
			n++
		}
		return n, nil
	})
}

// fermata puts a fermata of a length on the selected note or rest, or on
// each voice's last event in the selected bars (again: off).
func (e *scoreState) fermata(value string) bool {
	var locs []edit.Loc
	switch e.Sel.Kind {
	case scoreSelEvent:
		locs = e.selEvents(false)
	case scoreSelBars:
		e.eachSelVoice(func(line, staff, voice int, evs []edit.Loc) {
			for i := len(evs) - 1; i >= 0; i-- {
				if ev := edit.Event(e.Score, evs[i]); ev.Grace == score.NotGrace && ev.Kind != score.EventSpace {
					locs = append(locs, evs[i])
					break
				}
			}
		})
	}
	if len(locs) == 0 {
		e.Status = "Select a note, a rest or bars for the fermata"
		return false
	}
	return e.marked("Fermata", func() (int, error) {
		for _, l := range locs {
			if _, err := edit.ToggleMark(e.Score, l, "ferm", value); err != nil {
				return 0, err
			}
		}
		return len(locs), nil
	})
}

// gliss toggles a glissando from the selected note to the next one.
func (e *scoreState) gliss(value string) bool {
	if e.chord() == nil {
		e.Status = "Select the note the glissando starts on"
		return false
	}
	l := e.Sel.Loc
	if _, ok := edit.NextNote(e.Score, l); !ok {
		e.Status = "A glissando goes to the next note: write it first"
		return false
	}
	on := false
	if e.change(func() error {
		var err error
		on, err = edit.ToggleArticulation(e.Score, l, "gliss", value)
		return err
	}) {
		e.Status = scoreOnOff(on, "Glissando", "Glissando off")
		return true
	}
	return false
}

// keypadMark is keypad 7, 8, 9 and + (slot 7..10): accidentals and the
// accent on the first layout, articulations on the second.
func (e *scoreState) keypadMark(slot int) {
	if e.Layout2 {
		e.toggleMark([]string{"stacc", "ten", "staccmo", "marc"}[slot-7], "")
		return
	}
	switch slot {
	case 7:
		e.accidental(edit.Natural)
	case 8:
		e.accidental(edit.Sharp)
	case 9:
		e.accidental(edit.Flat)
	default:
		e.toggleMark("acc", "")
	}
}

// ---------- dynamics and expression text ----------

// firstEvents are the events a dynamic or a text goes on: the selected
// one, or each line's first event in the selected bars (its first staff's
// first voice with music there).
func (e *scoreState) firstEvents() []edit.Loc {
	switch e.Sel.Kind {
	case scoreSelEvent:
		return e.selEvents(false)
	case scoreSelBars:
		var out []edit.Loc
		seen := map[int]bool{}
		e.eachSelVoice(func(line, staff, voice int, evs []edit.Loc) {
			if seen[line] {
				return
			}
			for _, l := range evs {
				if ev := edit.Event(e.Score, l); ev.Grace == score.NotGrace && ev.Kind != score.EventSpace {
					out = append(out, l)
					seen[line] = true
					return
				}
			}
		})
		return out
	}
	return nil
}

// dynamic puts a dynamic on the selection's first events ("" takes it off).
func (e *scoreState) dynamic(name string) bool {
	locs := e.firstEvents()
	if len(locs) == 0 {
		e.Status = "Select a note or bars for the dynamic"
		return false
	}
	what := name
	if name == "" {
		what = "Dynamic off"
	}
	return e.marked(what, func() (int, error) {
		for _, l := range locs {
			if err := edit.SetDynamic(e.Score, l, name); err != nil {
				return 0, err
			}
		}
		return len(locs), nil
	})
}

// expression is what the expression box (Ctrl+E) writes: a dynamic, a
// technique word (pizz., con sord.), cresc. or dim. with dashes, tempo
// text (rit., a tempo), or expression text.
func (e *scoreState) expression(text string) bool {
	k, t := edit.ClassifyExpression(text)
	if t == "" {
		e.Status = "Type a dynamic or a word"
		return false
	}
	switch k {
	case edit.ExprDynamic:
		return e.dynamic(t)
	case edit.ExprCresc:
		return e.hairpin("cresc.")
	case edit.ExprDim:
		return e.hairpin("dim.")
	case edit.ExprTempo:
		at := score.Pos{Bar: e.selBar(), Offset: e.selOnset()}
		return e.setTempo(score.TempoMark{At: at, Text: t})
	}
	kind := "expr"
	if k == edit.ExprTechnique {
		kind = "tech"
	}
	locs := e.firstEvents()
	if len(locs) == 0 {
		e.Status = "Select a note or bars for the text"
		return false
	}
	return e.marked(fmt.Sprintf("%q", t), func() (int, error) {
		for _, l := range locs {
			if err := edit.SetText(e.Score, l, kind, t); err != nil {
				return 0, err
			}
		}
		return len(locs), nil
	})
}

// removeText takes expression, technique and staff text off the selection.
func (e *scoreState) removeText() bool {
	locs := e.selEvents(false)
	return e.marked("Text removed", func() (int, error) {
		n := 0
		for _, l := range locs {
			for _, k := range []string{"expr", "tech", "text"} {
				if ok, _ := edit.RemoveMark(e.Score, l, k); ok {
					n++
				}
			}
		}
		return n, nil
	})
}

// ---------- lines from note to note ----------

// spanLabel names a spanner for the status line.
func spanLabel(k edit.SpanKind, value string) string {
	switch k {
	case edit.SpanSlur:
		return "Slur"
	case edit.SpanPedal:
		return "Pedal"
	case edit.SpanOttava:
		if value == "" {
			value = "8va"
		}
		return value + " line"
	}
	switch value {
	case "dim":
		return "Diminuendo"
	case "cresc.":
		return "cresc."
	case "dim.":
		return "dim."
	}
	return "Crescendo"
}

// selRuns are the runs a spanner of a kind goes over in the selected bars:
// a slur each voice's notes; a hairpin each line's first voice; a pedal
// line each line's last staff; an ottava each staff.
func (e *scoreState) selRuns(k edit.SpanKind) [][2]edit.Loc {
	var out [][2]edit.Loc
	took := map[[2]int]bool{}
	e.eachSelVoice(func(line, staff, voice int, evs []edit.Loc) {
		key := [2]int{line, -1}
		switch k {
		case edit.SpanSlur:
			key = [2]int{line*16 + staff*4 + voice, 0}
		case edit.SpanOttava:
			key = [2]int{line, staff}
		case edit.SpanPedal:
			if staff != len(e.Score.Lines[line].Staves)-1 {
				return
			}
		}
		if took[key] {
			return
		}
		var first, last *edit.Loc
		for i := range evs {
			ev := edit.Event(e.Score, evs[i])
			if ev.Grace != score.NotGrace || ev.Kind == score.EventSpace || (k == edit.SpanSlur && ev.Kind != score.EventChord) {
				continue
			}
			if first == nil {
				first = &evs[i]
			}
			last = &evs[i]
		}
		if first == nil || (k != edit.SpanOttava && k != edit.SpanPedal && *first == *last) {
			return
		}
		took[key] = true
		out = append(out, [2]edit.Loc{*first, *last})
	})
	return out
}

// span is S, H, Shift+H and the palette's pedal and ottava buttons: from
// the selected note to the next (on the note a line of the kind already
// starts at, one note further), or over the selected bars.
func (e *scoreState) span(k edit.SpanKind, value string) bool {
	label := spanLabel(k, value)
	switch e.Sel.Kind {
	case scoreSelBars:
		runs := e.selRuns(k)
		return e.marked(label, func() (int, error) {
			for _, r := range runs {
				if err := edit.AddSpan(e.Score, r[0], r[1], k, value); err != nil {
					return 0, err
				}
			}
			return len(runs), nil
		})
	case scoreSelEvent:
	default:
		e.Status = "Select a note or bars first"
		return false
	}
	l := e.Sel.Loc
	ev := e.event()
	if ev == nil || ev.Kind == score.EventSpace {
		e.Status = "Select a note first"
		return false
	}
	from, to := l, l
	if a, z, ok := edit.SpanAt(e.Score, l, k); ok && a == l {
		// Again on its first note: one note further.
		from, to = a, z
	}
	if k == edit.SpanSlur || k == edit.SpanHairpin || to != l {
		next, ok := edit.NextNote(e.Score, to)
		if !ok {
			e.Status = label + " runs to the next note: write it first"
			return false
		}
		to = next
	}
	if e.change(func() error { return edit.AddSpan(e.Score, from, to, k, value) }) {
		e.Status = fmt.Sprintf("%s to bar %d", label, to.Bar)
		return true
	}
	return false
}

// slur is S.
func (e *scoreState) slur() bool { return e.span(edit.SpanSlur, "") }

// hairpin is H (cresc), Shift+H (dim), or the expression box's "cresc." /
// "dim." with dashes.
func (e *scoreState) hairpin(kind string) bool { return e.span(edit.SpanHairpin, kind) }

// removeSpans takes the lines of a kind off the selection: the one over
// the selected note, or every one starting in the selected bars.
func (e *scoreState) removeSpans(k edit.SpanKind) bool {
	label := spanLabel(k, "")
	var locs []edit.Loc
	switch e.Sel.Kind {
	case scoreSelEvent:
		locs = []edit.Loc{e.Sel.Loc}
	case scoreSelBars:
		e.eachSelVoice(func(_, _, _ int, evs []edit.Loc) { locs = append(locs, evs...) })
	}
	return e.marked(label+" removed", func() (int, error) {
		n := 0
		for _, l := range locs {
			if edit.RemoveSpan(e.Score, l, k) {
				n++
			}
		}
		return n, nil
	})
}

// ---------- grace notes ----------

// toggleGrace is the grace-note key (keypad 5 / 6 on the second layout):
// in note input the next notes are grace notes of that kind (again: back to
// normal notes); out of it a selected grace note changes kind.
func (e *scoreState) toggleGrace(kind score.Grace) {
	name := map[score.Grace]string{score.Acciaccatura: "acciaccaturas", score.Appoggiatura: "appoggiaturas"}[kind]
	if !e.Input {
		if ev := e.event(); ev != nil && ev.Grace != score.NotGrace {
			l := e.Sel.Loc
			if e.change(func() error { return edit.SetGrace(e.Score, l, kind) }) {
				e.Status = e.describeSel()
			}
			return
		}
		e.Grace = kind
		e.Status = "Grace notes (" + name + "): the next notes typed in note input"
		return
	}
	if e.Grace == kind {
		e.Grace = score.NotGrace
		e.Status = "Grace notes off: the next note is the one they lean on"
		return
	}
	e.Grace = kind
	e.Status = "Grace notes (" + name + "): type them, then switch off and type their note"
}

// writeGrace writes a grace note before the note at a place: the voice's
// event starting there (a rest is written first in an empty bar).
func (e *scoreState) writeGrace(l edit.Loc, at score.Frac, concert score.Pitch) bool {
	g := score.Event{Kind: score.EventChord, Dur: e.Dur, Grace: e.Grace, Notes: []score.Note{{Pitch: concert}}}
	if g.Dur.Base < score.Eighth {
		g.Dur = score.Duration{Base: score.Eighth}
	}
	return e.change(func() error {
		k, ok := scoreEventAt(e.Score, l, at)
		if !ok {
			// Nothing starts there yet: a rest for the grace to lean on.
			loc, err := edit.Write(e.Score, l.Line, l.Staff, l.Voice, l.Bar, at, score.Event{Kind: score.EventRest, Dur: e.Dur})
			if err != nil {
				return err
			}
			k = loc.Event
		}
		target := l
		target.Event = k
		loc, err := edit.InsertGrace(e.Score, target, g)
		if err != nil {
			return err
		}
		e.Sel = scoreSel{Kind: scoreSelEvent, Loc: loc, Note: 0}
		return nil
	})
}

// ---------- voices ----------

// moveToVoice is Ctrl+Alt+1-4 on a selected note out of note input
// (Sibelius): the note moves into that voice at its beat.
func (e *scoreState) moveToVoice(v int) bool {
	l := e.Sel.Loc
	var to edit.Loc
	if e.change(func() error {
		var err error
		to, err = edit.MoveToVoice(e.Score, l, v)
		if err != nil {
			return err
		}
		e.Sel = scoreSel{Kind: scoreSelEvent, Loc: to, Note: -1}
		return nil
	}) {
		e.Voice = v
		e.Status = fmt.Sprintf("Moved to voice %d", v+1)
		return true
	}
	return false
}

// ---------- tempo, rehearsal marks, repeats, endings, jumps, clefs ----------

// setTempo puts a tempo mark (or tempo text) at its place.
func (e *scoreState) setTempo(t score.TempoMark) bool {
	if e.change(func() error { return edit.SetTempoMark(e.Score, t) }) {
		e.Status = fmt.Sprintf("Tempo at bar %d: %s", t.At.Bar, scoreTempoWords(t))
		return true
	}
	return false
}

// removeTempo takes the tempo mark at a place out.
func (e *scoreState) removeTempo(at score.Pos) bool {
	if at == (score.Pos{Bar: 1}) {
		e.Status = "The opening tempo mark is the piece's tempo: change it instead"
		return false
	}
	ok := e.change(func() error {
		if !edit.RemoveTempoMark(e.Score, at) {
			return fmt.Errorf("no tempo mark at bar %d", at.Bar)
		}
		return nil
	})
	if ok {
		e.Status = fmt.Sprintf("Tempo mark at bar %d removed", at.Bar)
	}
	return ok
}

// setRehearsal puts a rehearsal mark at a bar.
func (e *scoreState) setRehearsal(bar int, name string) bool {
	if e.change(func() error { return edit.SetSection(e.Score, bar, name) }) {
		e.Status = fmt.Sprintf("Rehearsal mark %q at bar %d", strings.TrimSpace(name), bar)
		return true
	}
	return false
}

// removeRehearsal takes a bar's rehearsal mark out.
func (e *scoreState) removeRehearsal(bar int) bool {
	ok := e.change(func() error {
		if !edit.RemoveSection(e.Score, bar) {
			return fmt.Errorf("no rehearsal mark at bar %d", bar)
		}
		return nil
	})
	if ok {
		e.Status = fmt.Sprintf("Rehearsal mark at bar %d removed", bar)
	}
	return ok
}

// selBars are the selected bars, or the selected note's bar.
func (e *scoreState) selBars() (a, z int, ok bool) {
	switch e.Sel.Kind {
	case scoreSelBars:
		return e.Sel.BarA, e.Sel.BarB, true
	case scoreSelEvent, scoreSelCaret:
		return e.Sel.Loc.Bar, e.Sel.Loc.Bar, true
	}
	return 0, 0, false
}

// setRepeat repeats bars a..z.
func (e *scoreState) setRepeat(a, z, times int) bool {
	if e.change(func() error { return edit.SetRepeat(e.Score, a, z, times) }) {
		e.Status = fmt.Sprintf("Bars %d-%d repeat", a, z)
		if times > 2 {
			e.Status += fmt.Sprintf(", %d times", times)
		}
		return true
	}
	return false
}

// removeRepeats takes the repeats over bars a..z out.
func (e *scoreState) removeRepeats(a, z int) bool {
	n := 0
	ok := e.change(func() error {
		if n = edit.RemoveRepeats(e.Score, a, z); n == 0 {
			return fmt.Errorf("no repeat over bars %d-%d", a, z)
		}
		return nil
	})
	if ok {
		e.Status = fmt.Sprintf("%d repeat(s) removed", n)
	}
	return ok
}

// setEnding makes bars a..z an ending on the passes listed.
func (e *scoreState) setEnding(a, z int, passes []int) bool {
	if e.change(func() error { return edit.SetVolta(e.Score, a, z, passes) }) {
		e.Status = fmt.Sprintf("Ending %s over bars %d-%d", score.Volta{Passes: passes}.Label(), a, z)
		return true
	}
	return false
}

// removeEndings takes the endings over bars a..z out.
func (e *scoreState) removeEndings(a, z int) bool {
	ok := e.change(func() error {
		if edit.RemoveVoltas(e.Score, a, z) == 0 {
			return fmt.Errorf("no ending over bars %d-%d", a, z)
		}
		return nil
	})
	if ok {
		e.Status = "Ending removed"
	}
	return ok
}

// setJump puts a navigation mark at the selected bar (a segno or coda at
// the first selected bar, the rest at the last).
func (e *scoreState) setJump(k score.JumpKind, al score.JumpKind) bool {
	a, z, ok := e.selBars()
	if !ok {
		e.Status = "Select the bar for the mark"
		return false
	}
	bar := z
	if k == score.JumpSegno || k == score.JumpCoda {
		bar = a
	}
	j := score.Jump{Kind: k, Bar: bar, Al: al}
	if e.change(func() error { return edit.SetJump(e.Score, j) }) {
		e.Status = fmt.Sprintf("%s at bar %d", j.Text(), bar)
		return true
	}
	return false
}

// removeJumps takes the navigation marks of the selected bars out.
func (e *scoreState) removeJumps() bool {
	a, z, ok := e.selBars()
	if !ok {
		e.Status = "Select the bars"
		return false
	}
	n := 0
	done := e.change(func() error {
		for b := a; b <= z; b++ {
			n += edit.RemoveJumps(e.Score, b, 0)
		}
		if n == 0 {
			return fmt.Errorf("no segno, coda, D.C., D.S. or Fine in the selection")
		}
		return nil
	})
	if done {
		e.Status = fmt.Sprintf("%d mark(s) removed", n)
	}
	return done
}

// selPos is where the selection starts: a note's onset, a caret, a bar's
// start.
func (e *scoreState) selPos() score.Pos {
	return score.Pos{Bar: e.selBar(), Offset: e.selOnset()}
}

// setClefChange changes a staff's clef from a point on.
func (e *scoreState) setClefChange(line, staff int, at score.Pos, clef string) bool {
	if e.change(func() error { return edit.SetClefChange(e.Score, line, staff, at, clef) }) {
		e.Status = fmt.Sprintf("Clef %s from bar %d %s", clef, at.Bar, scoreBeatText(e.Score, at.Bar, at.Offset))
		return true
	}
	return false
}

// removeClefChange takes a clef change out.
func (e *scoreState) removeClefChange(line, staff int, at score.Pos) bool {
	ok := e.change(func() error {
		if !edit.RemoveClefChange(e.Score, line, staff, at) {
			return fmt.Errorf("no clef change there")
		}
		return nil
	})
	if ok {
		e.Status = "Clef change removed"
	}
	return ok
}

// ---------- the view's switches ----------

// toggleConcert switches the score between written and concert pitch. It
// is the score's (saved with it), not an undo step.
func (e *scoreState) toggleConcert() {
	e.Score.Concert = !e.Score.Concert
	e.touch()
	e.Status = scoreOnOff(e.Score.Concert, "Concert pitch: transposing staves at sounding pitch", "Transposing staves at written pitch")
}

// setLineGliss sets how a line's glissandi play ("" the instrument's).
func (e *scoreState) setLineGliss(li int, style string) bool {
	l := e.line(li)
	if l == nil {
		return false
	}
	if e.change(func() error { l = e.Score.Lines[li]; l.Gliss = style; return nil }) {
		if style == "" {
			style = "the instrument's"
		}
		e.Status = fmt.Sprintf("%s: glissandi play %s", e.lineName(li), style)
		return true
	}
	return false
}

// ---------- the dynamics lane ----------

// scoreLaneHeight is a lane's height, in staff spaces.
const scoreLaneHeight = 5.5

// scoreLaneDrag is a drag in a dynamics lane.
type scoreLaneDrag struct {
	On   bool
	Line int
	// Create draws a new override over Anchor..At; otherwise the drag moves
	// override Index's level.
	Create bool
	Index  int
	Anchor score.Pos
	At     score.Pos
	Level  float32
	// before is the score as the drag found it: the undo step when it ends
	// having changed something.
	before scoreSnapshot
	moved  bool
}

// laneShown reports a line's dynamics lane open.
func (e *scoreState) laneShown(li int) bool {
	l := e.line(li)
	return l != nil && e.Lanes[l.Name]
}

// toggleLane opens or closes a line's dynamics lane.
func (e *scoreState) toggleLane(li int) {
	l := e.line(li)
	if l == nil {
		e.Status = "Select a line for its dynamics lane"
		return
	}
	if e.Lanes == nil {
		e.Lanes = map[string]bool{}
	}
	e.Lanes[l.Name] = !e.Lanes[l.Name]
	if !e.Lanes[l.Name] {
		delete(e.Lanes, l.Name)
		if e.LaneSel.Line == li {
			e.LaneSel = scoreLaneSel{Index: -1}
		}
	}
	e.laneRev++
	e.Status = scoreOnOff(e.Lanes[l.Name], e.lineName(li)+": dynamics lane open (drag across it to hold a level)", e.lineName(li)+": dynamics lane closed")
}

// laneBelow is the room the open lanes take under their lines.
func (e *scoreState) laneBelow() map[int]float64 {
	if len(e.Lanes) == 0 {
		return nil
	}
	out := map[int]float64{}
	for li, l := range e.Score.Lines {
		if e.Lanes[l.Name] {
			out[li] = scoreLaneHeight
		}
	}
	return out
}

// scoreLaneSel is the override selected in a lane (Index -1: none).
type scoreLaneSel struct {
	Line, Index int
}

// laneCurves are every line's dynamics as written (written ticks), cached
// per edit.
func (e *scoreState) laneCurves() []interpret.LineDynamics {
	if e.laneCurveRev == e.Rev && e.laneCurveLive == e.laneLive && e.laneCurveScore == e.Score && e.laneCurveSet {
		return e.laneCurveCache
	}
	ds, err := interpret.WrittenDynamics(e.Score)
	if err != nil {
		ds = nil
	}
	e.laneCurveCache, e.laneCurveRev, e.laneCurveLive, e.laneCurveScore, e.laneCurveSet = ds, e.Rev, e.laneLive, e.Score, true
	return ds
}

// laneCurve is one line's dynamics, or nil.
func (e *scoreState) laneCurve(li int) *interpret.LineDynamics {
	for i, d := range e.laneCurves() {
		if d.Line == li {
			return &e.laneCurves()[i]
		}
	}
	return nil
}

// writtenTick is a written position's tick from the top, straight through.
func (e *scoreState) writtenTick(p score.Pos) float64 {
	return e.Score.PosFrac(p, e.Score.BarStarts()).TicksFloat()
}

// overrideAt is the index of a line's override over a written point, -1
// for none.
func (e *scoreState) overrideAt(li int, p score.Pos) int {
	l := e.line(li)
	if l == nil {
		return -1
	}
	for i, o := range l.Overrides {
		if !score.PosLess(p, o.From) && score.PosLess(p, o.To) {
			return i
		}
	}
	return -1
}

// laneLevel is a level clamped to what an override holds.
func laneLevel(v float32) float32 { return min(max(v, 0.02), 1) }

// lanePress starts a drag in a line's lane at a written point (snapped to
// the beat) and a level: on an override's level line (hit), its level;
// elsewhere a new override from there.
func (e *scoreState) lanePress(li int, at score.Pos, level float32, hit int) {
	l := e.line(li)
	if l == nil {
		return
	}
	d := scoreLaneDrag{On: true, Line: li, Anchor: at, At: at, Level: laneLevel(level), Index: hit,
		before: scoreSnapshot{Score: e.Score.Clone(), Sel: e.Sel}}
	if hit < 0 || hit >= len(l.Overrides) {
		d.Create, d.Index = true, -1
	} else {
		d.Level = l.Overrides[hit].Level
		e.LaneSel = scoreLaneSel{Line: li, Index: hit}
	}
	e.laneDrag = d
}

// laneMove carries a lane drag on: a new override's span and level, or an
// override's new level. The score changes as it goes, so the player hears
// it.
func (e *scoreState) laneMove(at score.Pos, level float32) {
	d := &e.laneDrag
	if !d.On {
		return
	}
	level = laneLevel(level)
	l := e.Score.Lines[d.Line]
	switch {
	case d.Create:
		if at == d.At && level == d.Level && d.moved {
			return
		}
		d.At, d.Level = at, level
		a, z := d.Anchor, d.At
		if score.PosLess(z, a) {
			a, z = z, a
		}
		// Start again from the score the drag found: the span grows and
		// shrinks freely.
		l.Overrides = append([]score.DynOverride(nil), d.before.Score.Lines[d.Line].Overrides...)
		if score.PosLess(a, z) {
			if err := edit.SetDynamicOverride(e.Score, d.Line, a, z, level); err != nil {
				e.Status = scoreErrText(err)
				return
			}
			d.moved = true
			e.LaneSel = scoreLaneSel{Line: d.Line, Index: e.overrideAt(d.Line, a)}
		}
	default:
		if d.Index < 0 || d.Index >= len(l.Overrides) || l.Overrides[d.Index].Level == level {
			return
		}
		l.Overrides[d.Index].Level = level
		d.Level, d.moved = level, true
	}
	// Not an edit of the notes (nothing to engrave again): the lane redraws
	// and the player hears it by laneLive.
	e.laneLive++
	e.Status = fmt.Sprintf("%s held at %.2f (%s); the printed dynamics are unchanged", e.lineName(d.Line), d.Level, scoreDynName(d.Level))
}

// laneRelease ends a lane drag: one undo step when it changed the score; a
// press on empty lane that never moved selects nothing.
func (e *scoreState) laneRelease() {
	d := e.laneDrag
	e.laneDrag = scoreLaneDrag{}
	if !d.On {
		return
	}
	if !d.moved {
		if d.Create {
			e.LaneSel = scoreLaneSel{Index: -1}
		}
		return
	}
	e.undo.push(d.before)
	e.redo.clear()
	e.mixKey = ""
	e.touch()
	if o := e.laneSelected(); o != nil {
		e.Status = fmt.Sprintf("%s held at %.2f (%s) from %s to %s; the printed dynamics are unchanged",
			e.lineName(d.Line), o.Level, scoreDynName(o.Level), e.Score.FormatPos(o.From), e.Score.FormatPos(o.To))
	}
}

// laneSelected is the selected override, or nil.
func (e *scoreState) laneSelected() *score.DynOverride {
	q := e.LaneSel
	l := e.line(q.Line)
	if l == nil || q.Index < 0 || q.Index >= len(l.Overrides) {
		return nil
	}
	return &l.Overrides[q.Index]
}

// laneDelete removes the selected override: its span plays its marks
// again.
func (e *scoreState) laneDelete() bool {
	o := e.laneSelected()
	if o == nil {
		e.Status = "Click an override in a dynamics lane first"
		return false
	}
	li, from, to := e.LaneSel.Line, o.From, o.To
	if e.change(func() error {
		edit.ClearDynamicOverrides(e.Score, li, from, to)
		return nil
	}) {
		e.LaneSel = scoreLaneSel{Index: -1}
		e.Status = fmt.Sprintf("%s: bars %s to %s follow the marks again", e.lineName(li), e.Score.FormatPos(from), e.Score.FormatPos(to))
		return true
	}
	return false
}

// laneClearSel clears every override in the selected bars of the selected
// lines.
func (e *scoreState) laneClearSel() bool {
	q := e.Sel
	if q.Kind != scoreSelBars {
		e.Status = "Select bars to clear their overrides"
		return false
	}
	n := 0
	ok := e.change(func() error {
		for li := q.LineA; li <= q.LineB; li++ {
			n += len(e.Score.Lines[li].Overrides)
			edit.ClearDynamicOverrides(e.Score, li, score.Pos{Bar: q.BarA}, score.Pos{Bar: q.BarB + 1})
			n -= len(e.Score.Lines[li].Overrides)
		}
		if n == 0 {
			return fmt.Errorf("no overrides in the selection")
		}
		return nil
	})
	if ok {
		e.LaneSel = scoreLaneSel{Index: -1}
		e.Status = "Overrides cleared: the selection follows its marks"
	}
	return ok
}

// scoreDynName is the dynamic nearest a level: "mf", "p".
func scoreDynName(v float32) string {
	best, bd := "mf", float32(2)
	for _, n := range score.DynamicSteps {
		if d := score.Dynamics[n].Level - v; d*d < bd {
			best, bd = n, d*d
		}
	}
	return best
}
