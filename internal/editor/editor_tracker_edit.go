package editor

// editor_tracker_edit.go — the tracker editor's editing model, with no ImGui in it.
//
// Everything a keystroke or a menu item does to the song lives here as a method
// on trackerState: where the cursor is and how it moves, what a key types into
// which sub-column, the selection and what the selection tools do to it, the
// clipboard text, and the snapshot undo stack. The window, the grid and the key
// handler (editor_tracker_window.go, _grid.go, _input.go) only decide *which*
// of these to call, which is what lets editor_tracker_test.go exercise the
// whole workflow with no GL context and no ImGui frame.
//
// The cursor addresses a sub-column ("field") inside a cell rather than a
// cell, because that is what a tracker's keyboard means: the same key is a
// piano key on the note column, a hex digit on the volume column and an effect
// letter on an effect column. Fields per cell:
//
//	0        note
//	1, 2     instrument, high and low nibble
//	3, 4     volume, high and low nibble
//	5+3k     effect k's letter
//	6+3k     effect k's parameter, high nibble
//	7+3k     effect k's parameter, low nibble
//
// The selection is whole cells — a block of rows by channels — which is what
// copy, transpose, interpolate and the rest want; selecting half a cell is a
// precision nobody asks a tracker for.

import (
	"fmt"
	"strings"

	"foundry/internal/tracker"

	"github.com/veandco/go-sdl2/sdl"
)

// Field kinds, from trkFieldKind.
const (
	trkKindNote = iota
	trkKindInstr
	trkKindVol
	trkKindFXCmd
	trkKindFXParam
)

// trkFieldsFixed is the note, instrument and volume fields ahead of the
// effect columns.
const trkFieldsFixed = 5

// trkFieldCount is how many fields a cell has with fx effect columns.
func trkFieldCount(fx int) int { return trkFieldsFixed + 3*fx }

// trkFieldKind says what a field is: its kind, which effect column it belongs
// to (effects only) and which nibble it is (0 high, 1 low; hex fields only).
func trkFieldKind(f int) (kind, fx, nibble int) {
	switch {
	case f <= 0:
		return trkKindNote, 0, 0
	case f <= 2:
		return trkKindInstr, 0, f - 1
	case f <= 4:
		return trkKindVol, 0, f - 3
	}
	k := (f - trkFieldsFixed) / 3
	switch (f - trkFieldsFixed) % 3 {
	case 0:
		return trkKindFXCmd, k, 0
	case 1:
		return trkKindFXParam, k, 0
	}
	return trkKindFXParam, k, 1
}

// trkCursor is where typing lands: an order-list position (which picks the
// pattern), a row, a channel and a field.
type trkCursor struct {
	Order, Row, Ch, Field int
}

// trkPoint is one corner of the selection.
type trkPoint struct{ Row, Ch int }

// trkScale is the scale-lock setting: when Kind is not 0, entered notes snap
// to the nearest note of Root's Kind scale.
type trkScale struct {
	Root int // 0 = C .. 11 = B
	Kind int // index into trkScales; 0 is off
}

// trkScales are the scale-lock choices: a name and the semitones in it.
var trkScales = []struct {
	Name  string
	Steps []int
}{
	{"Off", nil},
	{"Major", []int{0, 2, 4, 5, 7, 9, 11}},
	{"Minor", []int{0, 2, 3, 5, 7, 8, 10}},
	{"Harmonic minor", []int{0, 2, 3, 5, 7, 8, 11}},
	{"Dorian", []int{0, 2, 3, 5, 7, 9, 10}},
	{"Mixolydian", []int{0, 2, 4, 5, 7, 9, 10}},
	{"Pentatonic major", []int{0, 2, 4, 7, 9}},
	{"Pentatonic minor", []int{0, 3, 5, 7, 10}},
	{"Blues", []int{0, 3, 5, 6, 7, 10}},
}

// trkChords are the chord stamps: a name and the intervals stacked from the
// cursor's note, one per channel.
var trkChords = []struct {
	Name      string
	Intervals []int
}{
	{"Major", []int{0, 4, 7}},
	{"Minor", []int{0, 3, 7}},
	{"Dominant 7th", []int{0, 4, 7, 10}},
	{"Major 7th", []int{0, 4, 7, 11}},
	{"Minor 7th", []int{0, 3, 7, 10}},
	{"Sus4", []int{0, 5, 7}},
	{"Power", []int{0, 7, 12}},
}

// trkSnapshot is one undo step: the whole song (a few kilobytes) and where the
// cursor was, so an undo puts the hand back where the edit was made.
type trkSnapshot struct {
	Song *tracker.Song
	Cur  trkCursor
}

// trackerState is everything the tracker editor knows about the song being
// edited. Package-level as trkEd, the way every editor's state is; a type so
// the tests can build a fresh one.
type trackerState struct {
	Song *tracker.Song
	// Name is the song's library name (path under sounds/, no extension); ""
	// until it is saved.
	Name string

	Cur trkCursor
	// SelOn says a block is selected, from SelA (the anchor) to SelB.
	SelOn      bool
	SelA, SelB trkPoint

	Octave int // the QWERTY piano's lower octave, 0..8
	Step   int // rows the cursor advances after an entry, 0..16
	Instr  int // the current instrument, 1-based; 0 when the song has none
	// EditMode on writes typed notes into the pattern; off they only sound.
	EditMode bool
	Scale    trkScale

	// Rev counts edits — the save chip's change signal and the player's.
	Rev uint64

	undo, redo boundedStack[trkSnapshot]
	// undoKey is what the last pushed undo step was for. Consecutive pushes
	// with the same non-empty key coalesce into the first, so a run of typed
	// notes is one step; any cursor move that is not an entry's own advance
	// clears it.
	undoKey string

	// Status is the line under the toolbar.
	Status string
}

// newTrackerState starts an editor on song.
func newTrackerState(song *tracker.Song) *trackerState {
	t := &trackerState{Octave: 4, Step: 1, EditMode: true}
	t.setSong(song)
	return t
}

// setSong replaces the song being edited and puts the cursor somewhere valid.
// Undo history is dropped: a snapshot of a different song would restore the
// wrong file.
func (t *trackerState) setSong(song *tracker.Song) {
	t.Song = song
	t.Cur = trkCursor{}
	t.SelOn = false
	t.undo.clear()
	t.redo.clear()
	t.undoKey = ""
	t.Instr = 0
	if len(song.Instruments) > 0 {
		t.Instr = 1
	}
	t.Rev++
}

// touch records that the song changed.
func (t *trackerState) touch() { t.Rev++ }

// ---------- undo ----------

// pushUndo records the song before an edit. Call it immediately BEFORE the
// mutation. key coalesces: see undoKey.
func (t *trackerState) pushUndo(key string) {
	if key != "" && key == t.undoKey && t.undo.len() > 0 {
		return
	}
	t.undo.push(trkSnapshot{Song: t.Song.Clone(), Cur: t.Cur})
	t.redo.clear()
	t.undoKey = key
}

// breakUndoRun ends a coalescing run: the next push starts a new step.
func (t *trackerState) breakUndoRun() { t.undoKey = "" }

func (t *trackerState) canUndo() bool { return t.undo.len() > 0 }
func (t *trackerState) canRedo() bool { return t.redo.len() > 0 }

func (t *trackerState) undoStep() bool {
	snap, ok := t.undo.pop()
	if !ok {
		return false
	}
	t.redo.push(trkSnapshot{Song: t.Song.Clone(), Cur: t.Cur})
	t.applySnapshot(snap)
	t.Status = "Undo"
	return true
}

func (t *trackerState) redoStep() bool {
	snap, ok := t.redo.pop()
	if !ok {
		return false
	}
	t.undo.push(trkSnapshot{Song: t.Song.Clone(), Cur: t.Cur})
	t.applySnapshot(snap)
	t.Status = "Redo"
	return true
}

func (t *trackerState) applySnapshot(s trkSnapshot) {
	t.Song = s.Song
	t.Cur = s.Cur
	t.undoKey = ""
	t.clampCursor()
	if t.Instr > len(t.Song.Instruments) {
		t.Instr = len(t.Song.Instruments)
	}
	t.Rev++
}

// ---------- cursor ----------

// patternIndex is the pattern under the cursor's order position, or -1.
func (t *trackerState) patternIndex() int {
	s := t.Song
	if s == nil || t.Cur.Order < 0 || t.Cur.Order >= len(s.Order) {
		return -1
	}
	p := s.Order[t.Cur.Order]
	if p < 0 || p >= len(s.Patterns) {
		return -1
	}
	return p
}

// pattern is the pattern under the cursor, or nil.
func (t *trackerState) pattern() *tracker.Pattern {
	if i := t.patternIndex(); i >= 0 {
		return t.Song.Patterns[i]
	}
	return nil
}

// cell is the cell under the cursor, or nil.
func (t *trackerState) cell() *tracker.Cell {
	p := t.pattern()
	if p == nil || t.Cur.Row >= p.Rows() || t.Cur.Ch >= len(p.Cells[t.Cur.Row]) {
		return nil
	}
	return &p.Cells[t.Cur.Row][t.Cur.Ch]
}

// clampCursor puts every coordinate back in range after the song changed
// shape under it.
func (t *trackerState) clampCursor() {
	s := t.Song
	t.Cur.Order = clampI(t.Cur.Order, 0, len(s.Order)-1)
	rows := 1
	if p := t.pattern(); p != nil {
		rows = p.Rows()
	}
	t.Cur.Row = clampI(t.Cur.Row, 0, rows-1)
	t.Cur.Ch = clampI(t.Cur.Ch, 0, len(s.Channels)-1)
	t.Cur.Field = clampI(t.Cur.Field, 0, trkFieldCount(s.FXColumns)-1)
	if t.SelOn {
		t.SelA.Row = clampI(t.SelA.Row, 0, rows-1)
		t.SelB.Row = clampI(t.SelB.Row, 0, rows-1)
		t.SelA.Ch = clampI(t.SelA.Ch, 0, len(s.Channels)-1)
		t.SelB.Ch = clampI(t.SelB.Ch, 0, len(s.Channels)-1)
	}
}

func clampI(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// moveRows moves the cursor d rows. Past either end of the pattern it carries
// on into the neighbouring order entry, so walking through a song with the
// arrow keys never stops at a pattern seam; at the ends of the song it stops.
func (t *trackerState) moveRows(d int) {
	t.breakUndoRun()
	t.stepRows(d)
}

// stepRows is moveRows without ending the undo run — an entry's own advance.
func (t *trackerState) stepRows(d int) {
	row := t.Cur.Row + d
	for {
		p := t.pattern()
		if p == nil {
			return
		}
		switch {
		case row < 0 && t.Cur.Order > 0:
			t.Cur.Order--
			row += t.pattern().Rows()
			continue
		case row >= p.Rows() && t.Cur.Order < len(t.Song.Order)-1:
			row -= p.Rows()
			t.Cur.Order++
			continue
		}
		t.Cur.Row = clampI(row, 0, p.Rows()-1)
		return
	}
}

// advance moves down by the edit step after an entry. It stays inside the
// pattern: running off the bottom of the one being typed into would be a
// surprise, where arrowing off it is a request.
func (t *trackerState) advance() {
	if p := t.pattern(); p != nil && t.Step > 0 {
		t.Cur.Row = clampI(t.Cur.Row+t.Step, 0, p.Rows()-1)
	}
}

// moveField moves the cursor d fields, crossing into the neighbouring channel
// at a cell's edge.
func (t *trackerState) moveField(d int) {
	t.breakUndoRun()
	n := trkFieldCount(t.Song.FXColumns)
	f := t.Cur.Field + d
	for f < 0 {
		if t.Cur.Ch == 0 {
			f = 0
			break
		}
		t.Cur.Ch--
		f += n
	}
	for f >= n {
		if t.Cur.Ch == len(t.Song.Channels)-1 {
			f = n - 1
			break
		}
		t.Cur.Ch++
		f -= n
	}
	t.Cur.Field = f
}

// nextChannel moves d channels, onto the note field — Tab and Shift+Tab.
func (t *trackerState) nextChannel(d int) {
	t.breakUndoRun()
	t.Cur.Ch = clampI(t.Cur.Ch+d, 0, len(t.Song.Channels)-1)
	t.Cur.Field = 0
}

// barRows is the rows in a bar, PgUp/PgDn's stride.
func (t *trackerState) barRows() int {
	n := t.Song.LPB * t.Song.BeatsPerBar
	if n <= 0 {
		n = 16
	}
	return n
}

// home and end jump to the top and bottom of the pattern; pressed again at the
// top, Home goes to the first channel, as in every tracker since FastTracker.
func (t *trackerState) home() {
	t.breakUndoRun()
	if t.Cur.Row == 0 {
		t.Cur.Ch, t.Cur.Field = 0, 0
	}
	t.Cur.Row = 0
}

func (t *trackerState) end() {
	t.breakUndoRun()
	if p := t.pattern(); p != nil {
		t.Cur.Row = p.Rows() - 1
	}
}

// ---------- selection ----------

// selRect is the selected block, normalised; with nothing selected it is the
// cursor's cell, so every selection tool also works on one cell.
func (t *trackerState) selRect() (r0, r1, c0, c1 int) {
	if !t.SelOn {
		return t.Cur.Row, t.Cur.Row, t.Cur.Ch, t.Cur.Ch
	}
	r0, r1 = t.SelA.Row, t.SelB.Row
	if r0 > r1 {
		r0, r1 = r1, r0
	}
	c0, c1 = t.SelA.Ch, t.SelB.Ch
	if c0 > c1 {
		c0, c1 = c1, c0
	}
	return
}

// extendSelection moves the cursor by (dRow, dCh) with the selection
// following it — Shift+arrows.
func (t *trackerState) extendSelection(dRow, dCh int) {
	if !t.SelOn {
		t.SelOn = true
		t.SelA = trkPoint{t.Cur.Row, t.Cur.Ch}
	}
	p := t.pattern()
	if p == nil {
		return
	}
	t.breakUndoRun()
	t.Cur.Row = clampI(t.Cur.Row+dRow, 0, p.Rows()-1)
	if dCh != 0 {
		t.Cur.Ch = clampI(t.Cur.Ch+dCh, 0, len(t.Song.Channels)-1)
	}
	t.SelB = trkPoint{t.Cur.Row, t.Cur.Ch}
}

// selectAll selects the whole pattern; pressed again, the cursor's channel.
func (t *trackerState) selectAll() {
	p := t.pattern()
	if p == nil {
		return
	}
	whole := trkPoint{p.Rows() - 1, len(t.Song.Channels) - 1}
	if t.SelOn && t.SelA == (trkPoint{}) && t.SelB == whole {
		t.SelA, t.SelB = trkPoint{0, t.Cur.Ch}, trkPoint{p.Rows() - 1, t.Cur.Ch}
		return
	}
	t.SelOn, t.SelA, t.SelB = true, trkPoint{}, whole
}

func (t *trackerState) clearSelection() { t.SelOn = false }

// eachSelected calls fn for every cell in the selection (or the cursor's).
func (t *trackerState) eachSelected(fn func(r, c int, cell *tracker.Cell)) {
	p := t.pattern()
	if p == nil {
		return
	}
	r0, r1, c0, c1 := t.selRect()
	for r := r0; r <= r1 && r < p.Rows(); r++ {
		for c := c0; c <= c1 && c < len(p.Cells[r]); c++ {
			fn(r, c, &p.Cells[r][c])
		}
	}
}

// ---------- entry ----------

// snapToScale moves a note onto the scale-lock scale, preferring the note
// below when two are equally near — what a player's hand does.
func (t *trackerState) snapToScale(n uint8) uint8 {
	if t.Scale.Kind <= 0 || t.Scale.Kind >= len(trkScales) || n < tracker.NoteMin || n > tracker.NoteMax {
		return n
	}
	in := func(v int) bool {
		pc := ((v-1)%12 - t.Scale.Root + 24) % 12
		for _, s := range trkScales[t.Scale.Kind].Steps {
			if s == pc {
				return true
			}
		}
		return false
	}
	for d := 0; d < 12; d++ {
		if v := int(n) - d; v >= int(tracker.NoteMin) && in(v) {
			return uint8(v)
		}
		if v := int(n) + d; v <= int(tracker.NoteMax) && in(v) {
			return uint8(v)
		}
	}
	return n
}

// enterNote types a note at the cursor: the note, the current instrument
// beside it, and the cursor advances by the edit step. It returns the note
// written (after scale lock), for the caller to audition. With edit mode off
// nothing is written and the note is still returned, so keys audition.
func (t *trackerState) enterNote(n uint8) uint8 {
	if n >= tracker.NoteMin && n <= tracker.NoteMax {
		n = t.snapToScale(n)
	}
	if !t.EditMode {
		return n
	}
	c := t.cell()
	if c == nil {
		return n
	}
	t.pushUndo("type")
	c.Note = n
	if n >= tracker.NoteMin && n <= tracker.NoteMax && t.Instr > 0 {
		c.Instr = uint8(t.Instr)
	}
	t.touch()
	t.advance()
	return n
}

// enterHex types one hex digit into the instrument, volume or effect
// parameter field under the cursor. An empty field reads as 00 first. Volume
// is capped at 40, the most it means. An effect parameter with no effect
// letter yet is refused: the letter is what says what the number means.
func (t *trackerState) enterHex(d int) bool {
	if !t.EditMode || d < 0 || d > 15 {
		return false
	}
	c := t.cell()
	if c == nil {
		return false
	}
	kind, fx, nib := trkFieldKind(t.Cur.Field)
	set := func(v uint8) uint8 {
		if nib == 0 {
			return v&0x0F | uint8(d)<<4
		}
		return v&0xF0 | uint8(d)
	}
	switch kind {
	case trkKindInstr:
		t.pushUndo("type")
		c.Instr = set(c.Instr)
	case trkKindVol:
		t.pushUndo("type")
		v := c.Vol
		if v == tracker.VolNone {
			v = 0
		}
		v = set(v)
		if v > tracker.VolMax {
			v = tracker.VolMax
		}
		c.Vol = v
	case trkKindFXParam:
		if c.FX[fx].Cmd == 0 {
			t.Status = "Type an effect letter first"
			return false
		}
		t.pushUndo("type")
		c.FX[fx].Param = set(c.FX[fx].Param)
	default:
		return false
	}
	t.touch()
	t.advance()
	return true
}

// enterEffect types an effect letter into the effect-letter field under the
// cursor. Letters that are not effects are refused.
func (t *trackerState) enterEffect(cmd byte) bool {
	if !t.EditMode {
		return false
	}
	kind, fx, _ := trkFieldKind(t.Cur.Field)
	if kind != trkKindFXCmd || tracker.EffectFor(cmd) == nil {
		return false
	}
	c := t.cell()
	if c == nil {
		return false
	}
	t.pushUndo("type")
	c.FX[fx].Cmd = cmd
	t.touch()
	t.advance()
	return true
}

// setEffect writes a whole effect at the cursor's effect column — the
// effect picker's pick. It does not advance: the parameter is typed next.
func (t *trackerState) setEffect(cmd byte) {
	kind, fx, _ := trkFieldKind(t.Cur.Field)
	if kind != trkKindFXCmd && kind != trkKindFXParam {
		return
	}
	c := t.cell()
	if c == nil {
		return
	}
	t.pushUndo("")
	c.FX[fx].Cmd = cmd
	t.touch()
	t.Cur.Field = trkFieldsFixed + 3*fx + 1
}

// clearField empties what the cursor is on — the note column clears the note
// and its instrument together, as a note without them is meaningless — and
// advances. With a block selected it clears the block instead.
func (t *trackerState) clearField() {
	if t.SelOn {
		t.clearSelected()
		return
	}
	c := t.cell()
	if c == nil {
		return
	}
	t.pushUndo("type")
	kind, fx, _ := trkFieldKind(t.Cur.Field)
	switch kind {
	case trkKindNote:
		c.Note, c.Instr = tracker.NoteNone, 0
	case trkKindInstr:
		c.Instr = 0
	case trkKindVol:
		c.Vol = tracker.VolNone
	default:
		c.FX[fx] = tracker.Effect{}
	}
	t.touch()
	t.advance()
}

// insertRow pushes the cursor's channel down a row from the cursor, dropping
// the last; deleteRow pulls it up, leaving an empty last row.
func (t *trackerState) insertRow() {
	p := t.pattern()
	if p == nil {
		return
	}
	t.pushUndo("")
	ch := t.Cur.Ch
	for r := p.Rows() - 1; r > t.Cur.Row; r-- {
		p.Cells[r][ch] = p.Cells[r-1][ch]
	}
	p.Cells[t.Cur.Row][ch] = tracker.EmptyCell
	t.touch()
}

func (t *trackerState) deleteRow() {
	p := t.pattern()
	if p == nil {
		return
	}
	t.pushUndo("")
	ch := t.Cur.Ch
	for r := t.Cur.Row; r < p.Rows()-1; r++ {
		p.Cells[r][ch] = p.Cells[r+1][ch]
	}
	p.Cells[p.Rows()-1][ch] = tracker.EmptyCell
	t.touch()
}

// ---------- clipboard ----------

// copyText is the selection as tracker text, one FormatRow line per row —
// the notation the song file and Claude both read.
func (t *trackerState) copyText() string {
	p := t.pattern()
	if p == nil {
		return ""
	}
	r0, r1, c0, c1 := t.selRect()
	var b strings.Builder
	for r := r0; r <= r1 && r < p.Rows(); r++ {
		b.WriteString(tracker.FormatRow(p.Cells[r][c0:c1+1], t.Song.FXColumns))
		b.WriteByte('\n')
	}
	return b.String()
}

// cutSelection copies and clears.
func (t *trackerState) cutSelection() string {
	s := t.copyText()
	t.clearSelected()
	return s
}

// parseClip reads tracker text back into rows of cells. Every line must
// parse, or nothing does: a half-pasted block is worse than a refused one.
func parseClip(text string) ([][]tracker.Cell, error) {
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("nothing to paste")
	}
	var rows [][]tracker.Cell
	for i, line := range strings.Split(text, "\n") {
		var row []tracker.Cell
		for j, part := range strings.Split(line, "|") {
			c, err := tracker.ParseCell(part)
			if err != nil {
				return nil, fmt.Errorf("line %d cell %d: %w", i+1, j+1, err)
			}
			row = append(row, c)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// paste writes clipboard text at the cursor, clipped at the pattern's edges.
// mix fills only the fields that are empty where it lands, so a drum part can
// be laid over a bass line in the same channels.
func (t *trackerState) paste(text string, mix bool) error {
	rows, err := parseClip(text)
	if err != nil {
		return err
	}
	p := t.pattern()
	if p == nil {
		return fmt.Errorf("no pattern")
	}
	t.pushUndo("")
	for i, row := range rows {
		r := t.Cur.Row + i
		if r >= p.Rows() {
			break
		}
		for j, src := range row {
			c := t.Cur.Ch + j
			if c >= len(p.Cells[r]) {
				break
			}
			dst := &p.Cells[r][c]
			if !mix {
				*dst = src
				continue
			}
			if dst.Note == tracker.NoteNone {
				dst.Note = src.Note
			}
			if dst.Instr == 0 {
				dst.Instr = src.Instr
			}
			if dst.Vol == tracker.VolNone {
				dst.Vol = src.Vol
			}
			for k := range dst.FX {
				if dst.FX[k].Cmd == 0 {
					dst.FX[k] = src.FX[k]
				}
			}
		}
	}
	t.touch()
	t.SelOn = true
	t.SelA = trkPoint{t.Cur.Row, t.Cur.Ch}
	t.SelB = trkPoint{clampI(t.Cur.Row+len(rows)-1, 0, p.Rows()-1), clampI(t.Cur.Ch+len(rows[0])-1, 0, len(t.Song.Channels)-1)}
	return nil
}

// ---------- selection tools ----------

func (t *trackerState) clearSelected() {
	t.pushUndo("")
	t.eachSelected(func(_, _ int, c *tracker.Cell) { *c = tracker.EmptyCell })
	t.touch()
}

// transpose moves every note in the selection by semis. A note the move
// would push off the keyboard stays where it is rather than piling up at the
// edge, so transposing back undoes it exactly.
func (t *trackerState) transpose(semis int) {
	t.pushUndo("")
	t.eachSelected(func(_, _ int, c *tracker.Cell) {
		if c.Note < tracker.NoteMin || c.Note > tracker.NoteMax {
			return
		}
		if v := int(c.Note) + semis; v >= int(tracker.NoteMin) && v <= int(tracker.NoteMax) {
			c.Note = uint8(v)
		}
	})
	t.touch()
}

// interpolate fills each selected channel's volume column, and each effect
// column whose first and last rows hold the same effect, with a straight line
// between the first and last rows. A split effect (vibrato's speed/depth)
// interpolates each nibble on its own, which is what the two numbers mean.
func (t *trackerState) interpolate() bool {
	p := t.pattern()
	r0, r1, c0, c1 := t.selRect()
	if p == nil || r1-r0 < 2 {
		t.Status = "Select at least three rows to interpolate"
		return false
	}
	t.pushUndo("")
	lerp := func(a, b uint8, i int) uint8 {
		f := float64(i-r0) / float64(r1-r0)
		return uint8(float64(a) + (float64(b)-float64(a))*f + 0.5)
	}
	for c := c0; c <= c1; c++ {
		top, bot := p.Cells[r0][c], p.Cells[r1][c]
		if top.Vol != tracker.VolNone && bot.Vol != tracker.VolNone {
			for r := r0 + 1; r < r1; r++ {
				p.Cells[r][c].Vol = lerp(top.Vol, bot.Vol, r)
			}
		}
		for k := 0; k < t.Song.FXColumns; k++ {
			a, b := top.FX[k], bot.FX[k]
			if a.Cmd == 0 || a.Cmd != b.Cmd {
				continue
			}
			split := false
			if info := tracker.EffectFor(a.Cmd); info != nil {
				split = info.Split
			}
			for r := r0 + 1; r < r1; r++ {
				v := lerp(a.Param, b.Param, r)
				if split {
					v = lerp(a.Param>>4, b.Param>>4, r)<<4 | lerp(a.Param&15, b.Param&15, r)
				}
				p.Cells[r][c].FX[k] = tracker.Effect{Cmd: a.Cmd, Param: v}
			}
		}
	}
	t.touch()
	return true
}

// humanise jitters the volume of every note in the selection by up to
// ±amount (in volume steps). Deterministic: the same seed and the same block
// give the same result, so a humanised part survives undo/redo and diffs.
func (t *trackerState) humanise(amount int, seed uint32) {
	if amount <= 0 {
		return
	}
	t.pushUndo("")
	t.eachSelected(func(r, c int, cell *tracker.Cell) {
		if cell.Note < tracker.NoteMin || cell.Note > tracker.NoteMax {
			return
		}
		h := seed ^ uint32(r)*0x9E3779B1 ^ uint32(c)*0x85EBCA77
		h ^= h >> 15
		h *= 0x2C1B3C6D
		h ^= h >> 12
		j := int(h%uint32(2*amount+1)) - amount
		base := int(tracker.VolMax)
		if cell.Vol != tracker.VolNone {
			base = int(cell.Vol)
		}
		cell.Vol = uint8(clampI(base+j, 0, int(tracker.VolMax)))
	})
	t.touch()
}

// fillEvery copies the top row of the selection to every n-th row below it
// in the selection (to the end of the pattern with no block selected) — a
// hi-hat in one keystroke.
func (t *trackerState) fillEvery(n int) {
	p := t.pattern()
	if p == nil || n <= 0 {
		return
	}
	r0, r1, c0, c1 := t.selRect()
	if !t.SelOn {
		r1 = p.Rows() - 1
	}
	t.pushUndo("")
	for c := c0; c <= c1; c++ {
		src := p.Cells[r0][c]
		for r := r0 + n; r <= r1; r += n {
			p.Cells[r][c] = src
		}
	}
	t.touch()
}

// chordStamp writes a chord built on the cursor's note across the cursor's
// channel and the ones to its right, same row, same instrument and volume.
func (t *trackerState) chordStamp(chord int) bool {
	c := t.cell()
	if c == nil || chord < 0 || chord >= len(trkChords) || c.Note < tracker.NoteMin || c.Note > tracker.NoteMax {
		t.Status = "Put the cursor on a note to build the chord on"
		return false
	}
	p := t.pattern()
	t.pushUndo("")
	root := *c
	for i, iv := range trkChords[chord].Intervals {
		ch := t.Cur.Ch + i
		if ch >= len(t.Song.Channels) {
			break
		}
		v := int(root.Note) + iv
		if v > int(tracker.NoteMax) {
			break
		}
		dst := &p.Cells[t.Cur.Row][ch]
		dst.Note, dst.Instr, dst.Vol = uint8(v), root.Instr, root.Vol
	}
	t.touch()
	return true
}

// ---------- the keyboard's meaning ----------

// trkPianoKeys maps a scancode to semitones above the piano's lower octave:
// the bottom letter row is one octave (with the row above as its black keys)
// and the top letter row the next, continuing up past the octave on both rows
// the way every tracker lays it out.
var trkPianoKeys = map[sdl.Scancode]int{
	sdl.SCANCODE_Z: 0, sdl.SCANCODE_S: 1, sdl.SCANCODE_X: 2, sdl.SCANCODE_D: 3,
	sdl.SCANCODE_C: 4, sdl.SCANCODE_V: 5, sdl.SCANCODE_G: 6, sdl.SCANCODE_B: 7,
	sdl.SCANCODE_H: 8, sdl.SCANCODE_N: 9, sdl.SCANCODE_J: 10, sdl.SCANCODE_M: 11,
	sdl.SCANCODE_COMMA: 12, sdl.SCANCODE_L: 13, sdl.SCANCODE_PERIOD: 14,
	sdl.SCANCODE_SEMICOLON: 15, sdl.SCANCODE_SLASH: 16,

	sdl.SCANCODE_Q: 12, sdl.SCANCODE_2: 13, sdl.SCANCODE_W: 14, sdl.SCANCODE_3: 15,
	sdl.SCANCODE_E: 16, sdl.SCANCODE_R: 17, sdl.SCANCODE_5: 18, sdl.SCANCODE_T: 19,
	sdl.SCANCODE_6: 20, sdl.SCANCODE_Y: 21, sdl.SCANCODE_7: 22, sdl.SCANCODE_U: 23,
	sdl.SCANCODE_I: 24, sdl.SCANCODE_9: 25, sdl.SCANCODE_O: 26, sdl.SCANCODE_0: 27,
	sdl.SCANCODE_P: 28,
}

// trkNoteForKey is the note a piano key plays at octave, or false. Scancodes,
// not key symbols: the piano is a physical layout, and on an AZERTY keyboard
// it should still be the bottom row.
func trkNoteForKey(sc sdl.Scancode, octave int) (uint8, bool) {
	semi, ok := trkPianoKeys[sc]
	if !ok {
		return 0, false
	}
	v := octave*12 + semi + 1
	if v < int(tracker.NoteMin) || v > int(tracker.NoteMax) {
		return 0, false
	}
	return uint8(v), true
}

// trkHexForKey is the hex digit a key types, or false.
func trkHexForKey(sc sdl.Scancode) (int, bool) {
	switch {
	case sc >= sdl.SCANCODE_1 && sc <= sdl.SCANCODE_9:
		return int(sc-sdl.SCANCODE_1) + 1, true
	case sc == sdl.SCANCODE_0 || sc == sdl.SCANCODE_KP_0:
		return 0, true
	case sc >= sdl.SCANCODE_KP_1 && sc <= sdl.SCANCODE_KP_9:
		return int(sc-sdl.SCANCODE_KP_1) + 1, true
	case sc >= sdl.SCANCODE_A && sc <= sdl.SCANCODE_F:
		return int(sc-sdl.SCANCODE_A) + 10, true
	}
	return 0, false
}

// trkLetterForKey is the upper-case letter a key types, or 0.
func trkLetterForKey(sc sdl.Scancode) byte {
	if sc >= sdl.SCANCODE_A && sc <= sdl.SCANCODE_Z {
		return byte('A' + int(sc-sdl.SCANCODE_A))
	}
	return 0
}

// trkDigitForKey is the decimal digit of a top-row key, for Ctrl+0..9.
func trkDigitForKey(sc sdl.Scancode) (int, bool) {
	switch {
	case sc >= sdl.SCANCODE_1 && sc <= sdl.SCANCODE_9:
		return int(sc-sdl.SCANCODE_1) + 1, true
	case sc == sdl.SCANCODE_0:
		return 0, true
	}
	return 0, false
}

// typeKey is what a plain key (no Ctrl) does at the cursor in the grid. It
// returns the note to audition, or 0. Kept apart from SDL events so the test
// can type into a pattern.
func (t *trackerState) typeKey(sc sdl.Scancode) (audition uint8) {
	kind, _, _ := trkFieldKind(t.Cur.Field)
	switch kind {
	case trkKindNote:
		switch sc {
		case sdl.SCANCODE_1:
			t.enterNote(tracker.NoteOff)
			return 0
		case sdl.SCANCODE_GRAVE:
			t.enterNote(tracker.NoteCut)
			return 0
		}
		if n, ok := trkNoteForKey(sc, t.Octave); ok {
			return t.enterNote(n)
		}
	case trkKindInstr, trkKindVol, trkKindFXParam:
		if d, ok := trkHexForKey(sc); ok {
			t.enterHex(d)
		}
	case trkKindFXCmd:
		if l := trkLetterForKey(sc); l != 0 {
			t.enterEffect(l)
		}
	}
	return 0
}

// ---------- describing ----------

// trkCellText is a field's display text in hex: what the grid prints.
func trkInstrText(c tracker.Cell) string {
	if c.Instr == 0 {
		return ".."
	}
	return fmt.Sprintf("%02X", c.Instr)
}

func trkVolText(c tracker.Cell) string {
	if c.Vol == tracker.VolNone {
		return ".."
	}
	return fmt.Sprintf("%02X", c.Vol)
}

// trkEffectText is an effect column's text: readable words by default, the
// three hex characters when hex is set. Readable text is cut to width
// characters so the column never spills into the next channel.
func trkEffectText(e tracker.Effect, hex bool, width int) string {
	if e.Cmd == 0 {
		if hex {
			return "..."
		}
		return strings.Repeat(".", 3)
	}
	s := tracker.FormatEffect(e)
	if !hex {
		s = e.Describe()
	}
	if len([]rune(s)) > width {
		s = string([]rune(s)[:width])
	}
	return s
}
