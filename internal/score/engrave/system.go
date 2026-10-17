package engrave

// system.go — what stands over the whole system rather than one staff:
// endings, the navigation marks, rehearsal marks and tempo marks, over the
// top staff, each clear of everything under it.
//
// An ending (volta) is a bracket over its bars with its label ("1.",
// "2."), hooked down at its start and — when it closes on a repeat barline
// — at its end; the last ending stays open. A segno or coda sign stands at
// the start of its bar; "To Coda" (with the sign), "Fine", "D.C." and
// "D.S. al Coda" at the end of theirs, right-aligned to the barline. A
// rehearsal mark is its section's letter in a box at the bar's barline (A
// for the first section, B for the next…), with the section's name beside
// it. A tempo mark is its text in bold — or, for a change of speed (rit.,
// accel., meno mosso), italic — and its metronome mark, a note glyph and
// "= 92"; a gradual change with a `to:` runs dashes to where it ends. A
// repeat played more than twice says how many times over its end barline.
//
// The repeat barlines themselves are barline kinds (engrave.go) drawn with
// the other barlines.

import (
	"fmt"
	"math"
	"strconv"

	"foundry/internal/score"
)

// Sizes of the system's text, in staff spaces.
const (
	tempoTextSize   = 2.0
	tempoChangeSize = 1.7
	metronomeSize   = 1.7
	rehearsalSize   = 2.0
	sectionNameSize = 1.5
	jumpTextSize    = 1.7
	endingLabelSize = 1.4
	endingHook      = 1.6
	systemClear     = 0.6 // what is placed here to what is under it
	// systemClearX is the room kept either side of a tempo mark: ink that
	// ends just short of the words (an ornament's swash beside "accel.")
	// still lifts them over it.
	systemClearX      = 0.5
	systemLowest      = -1.8
	metronomeNoteSize = 0.72
)

// drawSystemMarks draws the system's marks over the top staff.
func (e *engraver) drawSystemMarks() {
	top := e.staves[0]
	endX := e.bars[len(e.bars)-1].x1 + e.barlineWidth(e.barlineBefore(len(e.bars)))
	k := newSky(-8, endX+8)
	for i := range top.items {
		it := offsetItem(top.items[i], Point{X: 0, Y: top.top})
		switch it.Role {
		case RoleStaff, RoleBarline, RoleBrace:
			continue
		}
		k.add(itemBounds(e.f, &it))
	}
	for i := range e.items {
		if e.items[i].Role == RoleBarNumber {
			k.add(itemBounds(e.f, &e.items[i]))
		}
	}
	lowest := top.top + systemLowest
	put := func(it Item) Rect {
		r := itemBounds(e.f, &it)
		e.items = append(e.items, it)
		k.add(r)
		return r
	}
	// baseline is where text of a size over x0..x1 sits: clear of the ink
	// under it.
	baseline := func(x0, x1, size float64) float64 {
		return math.Min(k.topIn(x0, x1, lowest)-systemClear, lowest) - 0.25*size
	}
	sysRef := func(bar int) Ref { return Ref{Line: -1, Staff: -1, Voice: -1, Bar: bar, Event: -1, Note: -1} }

	e.drawEndings(k, put, lowest, sysRef)

	// Repeats played more than twice.
	for _, r := range e.s.Repeats {
		if r.End < e.from || r.End > e.to || r.Plays() <= 2 {
			continue
		}
		bc := e.bars[r.End-e.from]
		text := fmt.Sprintf("×%d", r.Plays())
		w := TextWidthFont(text, TextBoldItalic, jumpTextSize)
		x1 := bc.x1 + e.barlineWidth(e.barlineBefore(r.End-e.from+1))
		put(Item{Kind: ItemText, Role: RoleJump, Ref: sysRef(r.End), P: Point{X: x1, Y: baseline(x1-w, x1, jumpTextSize)},
			Text: text, Font: TextBoldItalic, Size: jumpTextSize, Align: AlignRight})
	}

	// Navigation marks.
	for _, j := range e.s.Jumps {
		if j.Bar < e.from || j.Bar > e.to {
			continue
		}
		bi := j.Bar - e.from
		bc := e.bars[bi]
		ref := sysRef(j.Bar)
		switch j.Kind {
		case score.JumpSegno, score.JumpCoda:
			g := "segno"
			if j.Kind == score.JumpCoda {
				g = "coda"
			}
			b, _ := e.f.Box(g)
			x0 := e.barStartX(bi)
			bottom := math.Min(k.topIn(x0, x0+b.W(), lowest)-systemClear, lowest)
			put(e.glyphItem(RoleJump, ref, g, x0-b.Min.X, bottom-b.Max.Y, 1))
		default:
			text := j.Text()
			x1 := bc.x1 - 0.2
			w := TextWidthFont(text, TextBoldItalic, jumpTextSize)
			if j.Kind == score.JumpToCoda {
				// The coda sign after the words.
				b, _ := e.f.Box("coda")
				size := 0.7
				gw := b.W() * size
				y := baseline(x1-w-gw-0.3, x1, jumpTextSize)
				put(e.glyphItem(RoleJump, ref, "coda", x1-gw-b.Min.X*size, y+0.1-b.Max.Y*size, size))
				x1 -= gw + 0.3
				put(Item{Kind: ItemText, Role: RoleJump, Ref: ref, P: Point{X: x1, Y: y}, Text: text, Font: TextBoldItalic, Size: jumpTextSize, Align: AlignRight})
				continue
			}
			put(Item{Kind: ItemText, Role: RoleJump, Ref: ref, P: Point{X: x1, Y: baseline(x1-w, x1, jumpTextSize)},
				Text: text, Font: TextBoldItalic, Size: jumpTextSize, Align: AlignRight})
		}
	}

	// Rehearsal marks: the section's letter in a box, its name beside it.
	rehearsalEnd := map[int]float64{}
	for i, sec := range e.s.Sections {
		if sec.Bar < e.from || sec.Bar > e.to {
			continue
		}
		bi := sec.Bar - e.from
		ref := sysRef(sec.Bar)
		letter := RehearsalLetter(i)
		w := TextWidthFont(letter, TextBold, rehearsalSize)
		const pad = 0.35
		x0 := e.bars[bi].x0 - 0.3
		if bi == 0 {
			x0 = 0
		}
		bw := w + 2*pad
		nameW := 0.0
		if sec.Name != "" {
			nameW = 0.5 + TextWidthFont(sec.Name, TextItalic, sectionNameSize)
		}
		base := baseline(x0, x0+bw+nameW, rehearsalSize) - pad
		boxTop, boxBot := base-0.75*rehearsalSize-pad, base+0.25*rehearsalSize+pad
		th := e.d.TextEnclosureThickness
		for _, l := range [][2]Point{
			{{X: x0, Y: boxTop}, {X: x0 + bw, Y: boxTop}}, {{X: x0 + bw, Y: boxTop}, {X: x0 + bw, Y: boxBot}},
			{{X: x0 + bw, Y: boxBot}, {X: x0, Y: boxBot}}, {{X: x0, Y: boxBot}, {X: x0, Y: boxTop}},
		} {
			put(Item{Kind: ItemLine, Role: RoleRehearsal, Ref: ref, P: l[0], Q: l[1], Width: th})
		}
		put(Item{Kind: ItemText, Role: RoleRehearsal, Ref: ref, P: Point{X: x0 + bw/2, Y: base}, Text: letter, Font: TextBold, Size: rehearsalSize, Align: AlignCenter})
		end := x0 + bw
		if sec.Name != "" {
			put(Item{Kind: ItemText, Role: RoleRehearsal, Ref: ref, P: Point{X: x0 + bw + 0.5, Y: base}, Text: sec.Name, Font: TextItalic, Size: sectionNameSize})
			end += nameW
		}
		rehearsalEnd[sec.Bar] = end
	}

	// Tempo marks.
	for i, t := range e.s.Tempo {
		if t.At.Bar < e.from || t.At.Bar > e.to {
			continue
		}
		e.drawTempoMark(k, put, lowest, i, t, rehearsalEnd)
	}
}

// RehearsalLetter is the letter of the i-th rehearsal mark (0-based): A…Z,
// then AA, BB… as engravers double them.
func RehearsalLetter(i int) string {
	l := string(rune('A' + i%26))
	out := l
	for n := i / 26; n > 0; n-- {
		out += l
	}
	return out
}

// barStartX is where a bar's music starts after its opening barline (the
// layout's first bar: after the signatures).
func (e *engraver) barStartX(bi int) float64 {
	bc := e.bars[bi]
	if bi == 0 {
		return bc.content - 0.6
	}
	return bc.x0 + e.barlineWidth(e.barlineBefore(bi)) + 0.3
}

// drawEndings draws the endings' brackets.
func (e *engraver) drawEndings(k *sky, put func(Item) Rect, lowest float64, sysRef func(int) Ref) {
	th := e.d.RepeatEndingLineThickness
	for _, v := range e.s.Voltas {
		if v.End < e.from || v.Start > e.to || v.End < v.Start {
			continue
		}
		a, z := max(v.Start, e.from), min(v.End, e.to)
		x0 := e.barStartX(a-e.from) - 0.2
		if a == e.from && a > 1 && v.Start < e.from {
			x0 = e.layoutStart()
		}
		x1 := e.bars[z-e.from].x1 - 0.15
		closed := e.repeatEnds(v.End) && v.End <= e.to
		label := v.Label()
		lw := TextWidth(label, endingLabelSize)
		y := math.Min(k.topIn(x0, x1, lowest)-systemClear, lowest)
		// The label hangs inside the bracket: room for it too.
		y = math.Min(y, k.topIn(x0, x0+lw+0.6, lowest)-systemClear-1.7)
		ref := sysRef(v.Start)
		put(Item{Kind: ItemLine, Role: RoleEnding, Ref: ref, P: Point{X: x0, Y: y}, Q: Point{X: x1, Y: y}, Width: th})
		if v.Start >= e.from {
			put(Item{Kind: ItemLine, Role: RoleEnding, Ref: ref, P: Point{X: x0 + th/2, Y: y - th/2}, Q: Point{X: x0 + th/2, Y: y + endingHook}, Width: th})
			put(Item{Kind: ItemText, Role: RoleEnding, Ref: ref, P: Point{X: x0 + 0.4, Y: y + 1.3}, Text: label, Font: TextPlain, Size: endingLabelSize})
		}
		if closed {
			put(Item{Kind: ItemLine, Role: RoleEnding, Ref: ref, P: Point{X: x1 - th/2, Y: y - th/2}, Q: Point{X: x1 - th/2, Y: y + endingHook}, Width: th})
		}
	}
}

// metNoteGlyph is a metronome mark's note for a unit.
func metNoteGlyph(b score.DurBase) string {
	switch b {
	case score.Breve:
		return "metNoteDoubleWhole"
	case score.WholeNote:
		return "metNoteWhole"
	case score.Half:
		return "metNoteHalfUp"
	case score.Eighth:
		return "metNote8thUp"
	case score.D16:
		return "metNote16thUp"
	case score.D32:
		return "metNote32ndUp"
	}
	return "metNoteQuarterUp"
}

// drawTempoMark draws one tempo mark: its words, its metronome mark, and a
// gradual change's dashes.
func (e *engraver) drawTempoMark(k *sky, put func(Item) Rect, lowest float64, i int, t score.TempoMark, after map[int]float64) {
	bi := t.At.Bar - e.from
	ref := Ref{Line: -1, Staff: -1, Voice: -1, Bar: t.At.Bar, Event: -1, Note: -1}
	x0 := e.xAtPos(t.At) - 0.3
	if t.At.Offset.IsZero() {
		x0 = e.barStartX(bi)
	}
	if r, ok := after[t.At.Bar]; ok && t.At.Offset.IsZero() {
		x0 = math.Max(x0, r+0.8)
	}
	change := t.Change()
	font, size := TextBold, tempoTextSize
	if change == score.TempoRit || change == score.TempoAccel || change == score.TempoMeno || change == score.TempoPiu {
		font, size = TextItalic, tempoChangeSize
	}
	// Measure the whole mark first: it sits on one baseline.
	w := 0.0
	if t.Text != "" {
		w = TextWidthFont(t.Text, font, size)
	}
	unit := t.Unit
	if unit == (score.Duration{}) {
		unit = score.Duration{Base: score.Quarter}
	}
	g := metNoteGlyph(unit.Base)
	gb, _ := e.f.Box(g)
	db, _ := e.f.Box("metAugmentationDot")
	num := ""
	// A gradual change says only its words: the number it starts from is
	// already playing.
	if t.BPM > 0 && !t.Ramp && !change.Gradual() {
		num = " = " + strconv.FormatFloat(math.Round(t.BPM*10)/10, 'f', -1, 64)
	}
	metW := 0.0
	if num != "" {
		metW = gb.W()*metronomeNoteSize + float64(unit.Dots)*(db.W()*metronomeNoteSize+0.15) + TextWidth(num, metronomeSize)
		if w > 0 {
			metW += 0.6
		}
	}
	if w == 0 && metW == 0 {
		return
	}
	// A gradual change's dashes to where it ends: its to:, or (the older
	// ramp) the next mark.
	var to score.Pos
	has := false
	switch {
	case t.HasTo && change.Gradual():
		to, has = t.To, true
	case t.Ramp && i+1 < len(e.s.Tempo):
		to, has = e.s.Tempo[i+1].At, true
	}
	dashEnd := x0 + w + metW
	if has && score.PosLess(t.At, to) {
		dashEnd = e.endAtPos(to, 0.6)
	} else {
		has = false
	}
	base := math.Min(k.topIn(x0-systemClearX, math.Max(x0+w+metW, dashEnd)+systemClearX, lowest)-systemClear, lowest) - 0.25*size
	// The metronome note's stem must clear the ink too.
	if metW > 0 {
		base = math.Min(base, k.topIn(x0+w, x0+w+metW, lowest)-systemClear-gb.Max.Y*metronomeNoteSize)
	}
	x := x0
	if t.Text != "" {
		put(Item{Kind: ItemText, Role: RoleTempo, Ref: ref, P: Point{X: x, Y: base}, Text: t.Text, Font: font, Size: size})
		x += w
		if metW > 0 {
			x += 0.6
		}
	}
	if num != "" {
		put(e.glyphItem(RoleTempo, ref, g, x-gb.Min.X*metronomeNoteSize, base, metronomeNoteSize))
		x += gb.W() * metronomeNoteSize
		for d := 0; d < int(unit.Dots); d++ {
			x += 0.15
			put(e.glyphItem(RoleTempo, ref, "metAugmentationDot", x-db.Min.X*metronomeNoteSize, base, metronomeNoteSize))
			x += db.W() * metronomeNoteSize
		}
		put(Item{Kind: ItemText, Role: RoleTempo, Ref: ref, P: Point{X: x, Y: base}, Text: num, Font: TextPlain, Size: metronomeSize})
		x += TextWidth(num, metronomeSize)
	}
	if has {
		x1 := dashEnd
		if x1 > x+1.2 {
			y := base - 0.3*size
			for d := x + 0.5; d < x1; d += 1.2 {
				put(Item{Kind: ItemLine, Role: RoleTempo, Ref: ref, P: Point{X: d, Y: y}, Q: Point{X: math.Min(d+0.6, x1), Y: y}, Width: 0.12})
			}
		}
	}
}
