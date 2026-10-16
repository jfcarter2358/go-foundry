package editor

// editor_score_timeline_edit.go — the Timeline tab's editing model, with no
// ImGui in it: the snap grid, the tracks' arm and lane switches, the
// automation lanes' breakpoint drags and strokes, and the piano roll's notes
// drawn, moved, stretched, deleted and given a velocity.
//
// Everything here edits the same score the Score tab shows. A note drawn in
// the piano roll is notation (internal/score/edit/roll.go writes it into the
// bars, spelled in the key, in values the meter allows, tied where it must
// be, a triplet where the grid is), so it is engraved, played and saved like
// a note typed on the staff; an edit on the staff is on the roll the next
// frame. Lane points are the file's lane points (Line.Lanes).
//
// Edits of notes are one undo step each and hand the player a new program.
// A breakpoint drag changes the score as it goes, the way a dynamics-lane
// drag does (laneLive paces the player's programs), and is one undo step
// when it ends; a note drag is drawn as a ghost and written when it ends.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/tracker/fx"
)

// scoreGrid is the timeline's and the piano roll's snap: a note value, or
// a triplet of one; Off snaps to nothing finer than a 256th.
type scoreGrid struct {
	// Div is the note value as a fraction of a whole: 4 a quarter, 16 a
	// 16th.
	Div     int
	Triplet bool
	Off     bool
}

// scoreGrids are the grids the roll offers, in its menu's order.
var scoreGrids = []scoreGrid{{Div: 4}, {Div: 8}, {Div: 16}, {Div: 32}, {Div: 4, Triplet: true}, {Div: 8, Triplet: true}, {Div: 16, Triplet: true}, {Off: true}}

// Unit is one grid step in whole notes.
func (g scoreGrid) Unit() score.Frac {
	switch {
	case g.Off:
		return score.F(1, 256)
	case g.Triplet:
		return score.F(2, int64(3*max(g.Div, 1)))
	}
	return score.F(1, int64(max(g.Div, 1)))
}

// Label is the grid in words: "1/16", "1/8 triplet".
func (g scoreGrid) Label() string {
	switch {
	case g.Off:
		return "Off"
	case g.Triplet:
		return fmt.Sprintf("1/%d triplet", g.Div)
	}
	return fmt.Sprintf("1/%d", g.Div)
}

// scoreTLTool is what a press does in the lanes and the piano roll.
type scoreTLTool uint8

const (
	// scoreToolSelect selects, moves and stretches; a double-click draws.
	scoreToolSelect scoreTLTool = iota
	// scoreToolDraw draws: a note dragged out to its length, a lane drawn
	// freehand.
	scoreToolDraw
)

// scoreAutoSel is a breakpoint selected in an automation lane (Index -1:
// none).
type scoreAutoSel struct {
	Line  int
	Lane  string
	Index int
}

// scoreAutoDrag is a press in an automation lane: a breakpoint dragged
// (Index), or a stroke drawn (Stroke).
type scoreAutoDrag struct {
	On     bool
	Line   int
	Lane   string
	Index  int
	Stroke []score.LanePoint
	// before is the score as the drag found it, the undo step when it
	// changed something.
	before scoreSnapshot
	moved  bool
}

// scoreTimeline is the Timeline tab's state, on scoreState as TL. Lines
// are kept by name where a per-line switch must survive the lines moving
// (as the dynamics lanes are).
type scoreTimeline struct {
	// Open are the lines whose automation lanes are shown; Arm the lines
	// armed to record into.
	Open, Arm map[string]bool
	// RollLine is the line the piano roll shows when nothing is selected.
	RollLine int
	Grid     scoreGrid
	Tool     scoreTLTool
	// RollSel are the notes selected in the roll.
	RollSel []edit.RollNote
	AutoSel scoreAutoSel
	drag    scoreAutoDrag

	// The roll's notes, read once an edit.
	rollCache []edit.RollNote
	rollRev   uint64
	rollFor   int
	rollScore *score.Score
}

func newScoreTimeline() scoreTimeline {
	return scoreTimeline{Grid: scoreGrid{Div: 16}, AutoSel: scoreAutoSel{Index: -1}}
}

// ---------- time and the grid ----------

// scoreTimeEnd is the end of the written score, whole notes from the top.
func scoreTimeEnd(s *score.Score) score.Frac {
	st := s.BarStarts()
	return st[len(st)-1]
}

// scorePosOfTime is a written time as a bar and offset.
func scorePosOfTime(s *score.Score, t score.Frac) score.Pos {
	return s.PosAt(score.MaxFrac(t, score.Frac{}), s.BarStarts())
}

// scoreGridSnap snaps a written time to the grid, counted from its bar's
// start (a pickup counts from where its full bar would start), to the
// nearest line or (floor) the one at or before it. The score's end and
// past it clamp to the end.
func scoreGridSnap(s *score.Score, t score.Frac, g scoreGrid, floor bool) score.Frac {
	starts := s.BarStarts()
	end := starts[len(starts)-1]
	if !t.Less(end) {
		return end
	}
	if t.Sign() <= 0 {
		return score.Frac{Num: 0, Den: 1}
	}
	p := s.PosAt(t, starts)
	origin := starts[p.Bar-1]
	if full := s.MeterAt(p.Bar).Length(); s.BarLength(p.Bar).Less(full) {
		origin = origin.Sub(full.Sub(s.BarLength(p.Bar)))
	}
	u := g.Unit()
	k := t.Sub(origin).Div(u)
	n := k.Floor()
	if !floor && score.Whole(n).Add(score.F(1, 2)).Cmp(k) <= 0 {
		n++
	}
	x := origin.Add(u.MulInt(n))
	// A grid line past the bar's end is the next bar's start.
	if barEnd := starts[p.Bar]; barEnd.Less(x) {
		x = barEnd
	}
	return score.MinFrac(score.MaxFrac(x, starts[p.Bar-1]), end)
}

// ---------- tracks ----------

// tlKey is a lane owner's key in the Timeline's switches: a line's name
// (so a switch follows the line as lines move), or the bus's or master's
// own key (which no line name can be: it starts with a NUL).
func (e *scoreState) tlKey(owner int) (string, bool) {
	if owner < 0 {
		if !e.Score.HasOwner(owner) {
			return "", false
		}
		return "\x00" + e.Score.OwnerName(owner), true
	}
	if l := e.line(owner); l != nil {
		return l.Name, true
	}
	return "", false
}

// ownerName is a lane owner as the status bar says it: a line's name,
// "Bus A", "Bus B", "Master".
func (e *scoreState) ownerName(owner int) string {
	if owner < 0 {
		return e.Score.OwnerName(owner)
	}
	return e.lineName(owner)
}

// ownerLanes are a lane owner's lanes (nil for none).
func (e *scoreState) ownerLanes(owner int) []score.Lane {
	if ls := e.Score.LanesOf(owner); ls != nil {
		return *ls
	}
	return nil
}

// tlOpen reports an owner's automation lanes shown.
func (e *scoreState) tlOpen(owner int) bool {
	k, ok := e.tlKey(owner)
	return ok && e.TL.Open[k]
}

// toggleAutoLanes shows or hides an owner's automation lanes.
func (e *scoreState) toggleAutoLanes(owner int) {
	k, ok := e.tlKey(owner)
	if !ok {
		return
	}
	if e.TL.Open == nil {
		e.TL.Open = map[string]bool{}
	}
	if e.TL.Open[k] {
		delete(e.TL.Open, k)
	} else {
		e.TL.Open[k] = true
	}
	n := len(e.ownerLanes(owner))
	e.Status = scoreOnOff(e.TL.Open[k], fmt.Sprintf("%s: %d automation lane%s shown", e.ownerName(owner), n, scorePlural(n)), e.ownerName(owner)+": automation lanes hidden")
}

// armed reports a line armed for recording.
func (e *scoreState) armed(li int) bool {
	l := e.line(li)
	return l != nil && e.TL.Arm[l.Name]
}

// toggleArm arms a line for recording or disarms it. An armed line is the
// one the piano roll shows when nothing is selected; MIDI device input
// (SPEC phase 6) records into the armed lines.
func (e *scoreState) toggleArm(li int) {
	l := e.line(li)
	if l == nil {
		return
	}
	if e.TL.Arm == nil {
		e.TL.Arm = map[string]bool{}
	}
	if e.TL.Arm[l.Name] {
		delete(e.TL.Arm, l.Name)
		e.Status = e.lineName(li) + " disarmed"
		return
	}
	e.TL.Arm[l.Name] = true
	e.TL.RollLine = li
	e.Status = e.lineName(li) + " armed for recording"
}

// renameTLLine carries a line's timeline switches over a rename.
func (e *scoreState) renameTLLine(from, to string) {
	for _, m := range []map[string]bool{e.TL.Open, e.TL.Arm} {
		if m[from] {
			delete(m, from)
			m[to] = true
		}
	}
}

// scorePlural is "s" for any count but one.
func scorePlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---------- automation lanes ----------

// autoTargets are the lanes an owner can have, in the add menu's order:
// its own (a line's volume, pan, cutoff and sends; a bus's return and
// pan; the master's volume), then every parameter of every effect of its
// chain.
func (e *scoreState) autoTargets(owner int) []score.LaneTarget {
	ch := e.Score.ChainOf(owner)
	if ch == nil {
		return nil
	}
	var out []score.LaneTarget
	for _, k := range score.ScopeLaneKinds(score.OwnerScope(owner)) {
		out = append(out, score.LaneTarget{Kind: k})
	}
	for slot := range *ch {
		k, ok := fx.Lookup((*ch)[slot].Kind)
		if !ok {
			continue
		}
		for pi := range k.Spec().Params {
			if t, ok := score.ChainTargetForSlot(*ch, slot, pi); ok {
				out = append(out, t)
			}
		}
	}
	return out
}

// autoAdd gives a line a lane for a target and shows its lanes.
func (e *scoreState) autoAdd(li int, t score.LaneTarget) bool {
	name := t.Name()
	if !e.change(func() error { return edit.AddLane(e.Score, li, name) }) {
		return false
	}
	if e.TL.Open == nil {
		e.TL.Open = map[string]bool{}
	}
	if k, ok := e.tlKey(li); ok {
		e.TL.Open[k] = true
	}
	e.TL.AutoSel = scoreAutoSel{Line: li, Lane: name, Index: 0}
	e.Status = fmt.Sprintf("%s: %s lane added; click it to add points, drag them, right-click one for its shape", e.ownerName(li), t.Label())
	return true
}

// autoRemove takes a lane off a line.
func (e *scoreState) autoRemove(li int, name string) bool {
	if !e.change(func() error { return edit.RemoveLane(e.Score, li, name) }) {
		return false
	}
	if e.TL.AutoSel.Line == li && e.TL.AutoSel.Lane == name {
		e.TL.AutoSel = scoreAutoSel{Index: -1}
	}
	e.Status = fmt.Sprintf("%s: %s lane removed", e.ownerName(li), scoreLaneLabel(li, name))
	return true
}

// scoreLaneTarget reads an owner's lane name.
func scoreLaneTarget(owner int, name string) (score.LaneTarget, error) {
	return score.ParseLaneTargetIn(score.OwnerScope(owner), name)
}

// scoreLaneLabel is an owner's lane name in words.
func scoreLaneLabel(owner int, name string) string {
	if t, err := scoreLaneTarget(owner, name); err == nil {
		return t.Label()
	}
	return name
}

// autoPress starts a drag in a lane at a place and value: on point hit, that
// point; elsewhere a new point there. With the draw tool it starts a
// stroke instead.
func (e *scoreState) autoPress(li int, name string, at score.Pos, v float32, hit int) {
	if e.Score.LaneOf(li, name) == nil {
		return
	}
	d := scoreAutoDrag{On: true, Line: li, Lane: name, Index: hit, before: scoreSnapshot{Score: e.Score.Clone(), Sel: e.Sel}}
	if e.TL.Tool == scoreToolDraw && hit < 0 {
		d.Index = -1
		d.Stroke = []score.LanePoint{{At: at, Value: v}}
		e.TL.drag = d
		return
	}
	if hit < 0 {
		i, err := edit.AddLanePoint(e.Score, li, name, at, v)
		if err != nil {
			e.Status = scoreErrText(err)
			return
		}
		d.Index, d.moved = i, true
		e.laneLive++
	}
	e.TL.AutoSel = scoreAutoSel{Line: li, Lane: name, Index: d.Index}
	e.TL.drag = d
	e.autoStatus()
}

// autoMove carries a lane drag on: the point to a new place and value (it
// stops at its neighbours), or the stroke on to the pointer.
func (e *scoreState) autoMove(at score.Pos, v float32) {
	d := &e.TL.drag
	if !d.On {
		return
	}
	if d.Stroke != nil {
		last := d.Stroke[len(d.Stroke)-1]
		if at == last.At && v == last.Value {
			return
		}
		d.Stroke = append(d.Stroke, score.LanePoint{At: at, Value: v})
		return
	}
	ln := e.Score.LaneOf(d.Line, d.Lane)
	if ln == nil || d.Index < 0 || d.Index >= len(ln.Points) {
		return
	}
	p := ln.Points[d.Index]
	if p.At == at && p.Value == v {
		return
	}
	if err := edit.MoveLanePoint(e.Score, d.Line, d.Lane, d.Index, at, v); err != nil {
		e.Status = scoreErrText(err)
		return
	}
	if ln.Points[d.Index] != p {
		d.moved = true
		e.laneLive++
	}
	e.autoStatus()
}

// autoRelease ends a lane drag: a stroke is drawn into the lane; a moved
// point is one undo step.
func (e *scoreState) autoRelease() {
	d := e.TL.drag
	e.TL.drag = scoreAutoDrag{}
	if !d.On {
		return
	}
	if d.Stroke != nil {
		pts := d.Stroke
		sort.SliceStable(pts, func(i, j int) bool { return score.PosLess(pts[i].At, pts[j].At) })
		from, to := pts[0].At, pts[len(pts)-1].At
		if len(pts) < 2 || from == to {
			// A click with the draw tool: one point.
			if e.change(func() error {
				i, err := edit.AddLanePoint(e.Score, d.Line, d.Lane, from, pts[0].Value)
				e.TL.AutoSel = scoreAutoSel{Line: d.Line, Lane: d.Lane, Index: i}
				return err
			}) {
				e.autoStatus()
			}
			return
		}
		if e.change(func() error { return edit.DrawLane(e.Score, d.Line, d.Lane, from, to, pts, 0.004) }) {
			e.TL.AutoSel = scoreAutoSel{Index: -1}
			ln := e.Score.LaneOf(d.Line, d.Lane)
			e.Status = fmt.Sprintf("%s: %s drawn from %s to %s (%d points)", e.ownerName(d.Line), scoreLaneLabel(d.Line, d.Lane), e.Score.FormatPos(from), e.Score.FormatPos(to), len(ln.Points))
		}
		return
	}
	if !d.moved {
		return
	}
	e.undo.push(d.before)
	e.redo.clear()
	e.mixKey = ""
	e.touch()
}

// autoPoint is the selected breakpoint, or nil.
func (e *scoreState) autoPoint() *score.LanePoint {
	q := e.TL.AutoSel
	if q.Index < 0 {
		return nil
	}
	ln := e.Score.LaneOf(q.Line, q.Lane)
	if ln == nil || q.Index >= len(ln.Points) {
		return nil
	}
	return &ln.Points[q.Index]
}

// autoStatus says where the selected point is and what it holds.
func (e *scoreState) autoStatus() {
	p := e.autoPoint()
	if p == nil {
		return
	}
	q := e.TL.AutoSel
	t, _ := scoreLaneTarget(q.Line, q.Lane)
	e.Status = fmt.Sprintf("%s · %s: %s at %s", e.ownerName(q.Line), t.Label(), t.Format(p.Value), e.Score.FormatPos(p.At))
}

// autoDelete removes the selected breakpoint.
func (e *scoreState) autoDelete() bool {
	q := e.TL.AutoSel
	if e.autoPoint() == nil {
		e.Status = "Click a point in an automation lane first"
		return false
	}
	if !e.change(func() error { return edit.RemoveLanePoint(e.Score, q.Line, q.Lane, q.Index) }) {
		return false
	}
	e.TL.AutoSel.Index = -1
	e.Status = fmt.Sprintf("%s · %s: point removed", e.ownerName(q.Line), scoreLaneLabel(q.Line, q.Lane))
	return true
}

// autoShape sets how the segment after a breakpoint moves.
func (e *scoreState) autoShape(li int, name string, i int, shape score.LaneShape, curve float32) bool {
	if !e.change(func() error { return edit.SetLaneShape(e.Score, li, name, i, shape, curve) }) {
		return false
	}
	words := map[score.LaneShape]string{score.LaneLinear: "a straight line", score.LaneStep: "a step", score.LaneCurve: "a curve"}
	e.Status = fmt.Sprintf("%s · %s: point %d moves on as %s", e.ownerName(li), scoreLaneLabel(li, name), i+1, words[shape])
	return true
}

// ---------- the piano roll ----------

// rollLine is the line the piano roll shows: the selection's, else the one
// last clicked or armed.
func (e *scoreState) rollLine() int {
	if e.Sel.Kind != scoreSelNone {
		return e.selLine()
	}
	return clampI(e.TL.RollLine, 0, max(len(e.Score.Lines)-1, 0))
}

// rollNotes are the roll line's notes, read once an edit.
func (e *scoreState) rollNotes() []edit.RollNote {
	li := e.rollLine()
	tl := &e.TL
	if tl.rollRev != e.Rev || tl.rollFor != li || tl.rollScore != e.Score || tl.rollCache == nil {
		tl.rollCache = edit.RollNotes(e.Score, li)
		if tl.rollCache == nil {
			tl.rollCache = []edit.RollNote{}
		}
		tl.rollRev, tl.rollFor, tl.rollScore = e.Rev, li, e.Score
	}
	return tl.rollCache
}

// rollSelNow is the roll's selection as the roll line has it now: notes
// an edit elsewhere took away (or another line's) are not in it.
func (e *scoreState) rollSelNow() []edit.RollNote {
	if len(e.TL.RollSel) == 0 {
		return nil
	}
	var out []edit.RollNote
	for _, n := range e.rollNotes() {
		for _, s := range e.TL.RollSel {
			if s.Staff == n.Staff && s.Voice == n.Voice && s.Start.Eq(n.Start) && s.MIDI() == n.MIDI() {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

// rollSelected reports a note selected in the roll.
func (e *scoreState) rollSelected(n edit.RollNote) bool {
	for _, s := range e.TL.RollSel {
		if s.Staff == n.Staff && s.Voice == n.Voice && s.Start.Eq(n.Start) && s.MIDI() == n.MIDI() {
			return true
		}
	}
	return false
}

// rollStaffFor is the staff a new note at a pitch goes on: a grand staff
// splits at middle C.
func (e *scoreState) rollStaffFor(li, midi int) int {
	l := e.line(li)
	if l == nil || len(l.Staves) < 2 {
		return 0
	}
	if midi >= 60 {
		return 0
	}
	return len(l.Staves) - 1
}

// rollSpell spells a MIDI note for a staff at a time: in the key as the
// staff is written (a B-flat clarinet in its own key), sharps as the key
// has them; a percussion staff's notes are its drums' keys.
func (e *scoreState) rollSpell(li, staff, midi int, at score.Frac) score.Pitch {
	if e.percussion(li, staff) {
		return score.SpellMIDI(midi, 0, 0)
	}
	bar := scorePosOfTime(e.Score, at).Bar
	concert := score.SpellMIDI(midi, e.fifths(bar), 0)
	w := e.toWritten(li, staff, concert)
	return e.toConcert(li, staff, score.SpellMIDI(w.MIDI(), e.writtenFifths(li, staff, bar), 0))
}

// rollDescribe is a note in words: "F#4, bar 3 beat 2, a quarter (voice 2)".
func (e *scoreState) rollDescribe(li int, n edit.RollNote) string {
	p := scorePosOfTime(e.Score, n.Start)
	name := edit.PitchName(e.toWritten(li, n.Staff, n.Pitch))
	if e.percussion(li, n.Staff) {
		name = score.DrumName(n.Pitch)
	}
	length := n.Length().String()
	if d, ok := score.DurationFor(n.Length()); ok {
		length = edit.DurName(d)
	}
	v := ""
	if n.Vel > 0 {
		v = fmt.Sprintf(", velocity %d", n.Vel)
	}
	return fmt.Sprintf("%s, bar %d %s, %s (voice %d%s)", name, p.Bar, scoreBeatText(e.Score, p.Bar, p.Offset), length, n.Voice+1, v)
}

// rollSelect selects notes in the roll; one note is also selected on the
// staff (and heard), so the two views agree.
func (e *scoreState) rollSelect(li int, ns []edit.RollNote, extend bool) {
	if extend {
		for _, n := range ns {
			if !e.rollSelected(n) {
				e.TL.RollSel = append(e.TL.RollSel, n)
			}
		}
	} else {
		e.TL.RollSel = append([]edit.RollNote(nil), ns...)
	}
	e.TL.RollLine = li
	if len(e.TL.RollSel) == 1 && len(ns) == 1 {
		n := ns[0]
		e.selectEvent(n.Parts[0].Loc, n.Parts[0].Note)
		e.Status = e.rollDescribe(li, n)
		return
	}
	if len(ns) > 0 {
		e.Status = fmt.Sprintf("%d notes selected", len(e.TL.RollSel))
	}
}

// rollDraw writes a note from start to end at a pitch: on the staff its
// pitch goes on, in the current voice unless it would cut a note there
// (then the first voice that can take it), spelled in the key.
func (e *scoreState) rollDraw(li int, start, end score.Frac, midi int) bool {
	if e.line(li) == nil {
		return false
	}
	staff := e.rollStaffFor(li, midi)
	n := edit.RollNote{Staff: staff, Voice: e.Voice, Start: start, End: end, Pitch: e.rollSpell(li, staff, midi, start)}
	if v := edit.RollFreeVoice(e.Score, li, staff, start, end, e.Voice); v >= 0 {
		n.Voice = v
	}
	var got edit.RollNote
	if !e.change(func() error {
		var err error
		got, err = edit.RollAdd(e.Score, li, n)
		return err
	}) {
		return false
	}
	e.TL.RollSel = []edit.RollNote{got}
	e.TL.RollLine = li
	e.Sel = scoreSel{Kind: scoreSelEvent, Loc: got.Parts[0].Loc, Note: got.Parts[0].Note}
	e.audition(li, got.Pitch)
	e.Status = "Wrote " + e.rollDescribe(li, got)
	return true
}

// rollMove moves the selected notes (or n, when it is not among them) by
// dt in time and dm semitones, each stretched to end at its own end plus
// dEnd (a resize moves only the ends). Each note keeps its staff; one that
// would cut another goes into a voice that can take it.
func (e *scoreState) rollMove(li int, n edit.RollNote, dt score.Frac, dm int, dEnd score.Frac) bool {
	olds := e.rollSelNow()
	if !e.rollSelected(n) {
		olds = []edit.RollNote{n}
	}
	if len(olds) == 0 || dt.IsZero() && dm == 0 && dEnd.IsZero() {
		return false
	}
	news := make([]edit.RollNote, len(olds))
	for i, o := range olds {
		m := o.MIDI() + dm
		nn := edit.RollNote{Staff: o.Staff, Voice: o.Voice, Start: o.Start.Add(dt), End: o.End.Add(dt).Add(dEnd), Vel: o.Vel}
		if nn.Start.Sign() < 0 {
			e.Status = "That would move a note before the first bar"
			return false
		}
		if !nn.Start.Less(nn.End) {
			nn.End = nn.Start.Add(e.TL.Grid.Unit())
		}
		if dm == 0 {
			nn.Pitch = o.Pitch
		} else {
			nn.Pitch = e.rollSpell(li, o.Staff, m, nn.Start)
		}
		news[i] = nn
	}
	var got []edit.RollNote
	if !e.change(func() error {
		var err error
		got, err = edit.RollMove(e.Score, li, olds, news, true)
		return err
	}) {
		return false
	}
	e.TL.RollSel = got
	switch {
	case len(got) == 1:
		e.Sel = scoreSel{Kind: scoreSelEvent, Loc: got[0].Parts[0].Loc, Note: got[0].Parts[0].Note}
		e.Status = "Moved to " + e.rollDescribe(li, got[0])
		if dm != 0 {
			e.audition(li, got[0].Pitch)
		}
	default:
		e.Sel = scoreSel{}
		e.TL.RollLine = li
		e.Status = fmt.Sprintf("Moved %d notes", len(got))
	}
	return true
}

// rollDelete takes the selected notes out (their events become rests, or
// lose the pitch in a chord).
func (e *scoreState) rollDelete(li int) bool {
	ns := e.rollSelNow()
	if len(ns) == 0 {
		e.Status = "Select notes in the piano roll first"
		return false
	}
	// Latest first, so earlier notes' events stay where they were.
	sorted := append([]edit.RollNote(nil), ns...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[j].Start.Less(sorted[i].Start) })
	if !e.change(func() error {
		for _, n := range sorted {
			if err := edit.RollRemove(e.Score, li, n); err != nil {
				return err
			}
		}
		return nil
	}) {
		return false
	}
	e.TL.RollSel = nil
	e.Sel = scoreSel{}
	e.TL.RollLine = li
	e.Status = fmt.Sprintf("Deleted %d note%s", len(ns), scorePlural(len(ns)))
	return true
}

// rollVelocity sets the velocity of the selected notes (or n, when it is
// not among them); 0 gives them back to the dynamics.
func (e *scoreState) rollVelocity(li int, n edit.RollNote, vel int) bool {
	ns := e.rollSelNow()
	if !e.rollSelected(n) {
		ns = []edit.RollNote{n}
	}
	vel = clampI(vel, 0, 127)
	if !e.change(func() error {
		for _, x := range ns {
			if err := edit.RollSetVelocity(e.Score, li, x, vel); err != nil {
				return err
			}
		}
		return nil
	}) {
		return false
	}
	// The selection's notes as they are now (their Vel changed).
	var sel []edit.RollNote
	for _, x := range edit.RollNotes(e.Score, li) {
		for _, o := range ns {
			if x.Staff == o.Staff && x.Voice == o.Voice && x.Start.Eq(o.Start) && x.MIDI() == o.MIDI() {
				sel = append(sel, x)
			}
		}
	}
	e.TL.RollSel = sel
	if vel == 0 {
		e.Status = fmt.Sprintf("%d note%s play at the dynamics' level again", len(ns), scorePlural(len(ns)))
	} else {
		e.Status = fmt.Sprintf("Velocity %d on %d note%s", vel, len(ns), scorePlural(len(ns)))
	}
	return true
}

// rollSelectInBox selects the roll notes inside a box of time and pitch.
func (e *scoreState) rollSelectInBox(li int, t0, t1 score.Frac, m0, m1 int, extend bool) {
	if t1.Less(t0) {
		t0, t1 = t1, t0
	}
	if m1 < m0 {
		m0, m1 = m1, m0
	}
	var ns []edit.RollNote
	for _, n := range e.rollNotes() {
		if n.Start.Less(t1) && t0.Less(n.End) && n.MIDI() >= m0 && n.MIDI() <= m1 {
			ns = append(ns, n)
		}
	}
	if !extend {
		e.Sel = scoreSel{}
	}
	e.rollSelect(li, ns, extend)
	if len(ns) == 0 && !extend {
		e.TL.RollSel = nil
		e.Status = "No notes there"
	}
}

// rollVoiceSummary lists the voices a line uses, for the roll's legend.
func rollVoiceSummary(ns []edit.RollNote) string {
	var used [score.MaxVoices]bool
	for _, n := range ns {
		if n.Voice >= 0 && n.Voice < score.MaxVoices {
			used[n.Voice] = true
		}
	}
	var out []string
	for v, u := range used {
		if u {
			out = append(out, fmt.Sprint(v+1))
		}
	}
	return strings.Join(out, " ")
}
