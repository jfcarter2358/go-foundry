package editor

// editor_score_view.go — the Score editor's view of the music: the engraved
// score on paper as one continuous, scrolling system, with the staff headers
// in a column on the left (editor_score_header.go), the time ruler along the
// top (editor_score_ruler.go), the loop region, the selection, the
// note-input ghost and caret, the play head across every staff, and the
// status bar.
//
// The engraving is the display list internal/score/engrave makes (cached per
// edit in scoreLayout), drawn through internal/score/scoredraw with the glyph
// atlas; everything here is in staff spaces until the last moment, scaled by
// scoreZoom pixels a space. The pane is an ImGui window with its content size
// set to the layout's, so its scrollbars and the wheel scroll it; Ctrl+wheel
// zooms about the pointer.
//
// Clicks land on one InvisibleButton covering only the visible music — not
// the header column, the ruler or the scrollbars — so nothing drawn over it
// later needs to see a click (the Tracker header trap); the ruler and the
// headers have their own, over only themselves, and the keypad panel, a
// window of its own above this one, takes its own clicks.

import (
	"fmt"
	"math"

	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/engrave"
	"foundry/internal/score/glyph"
	"foundry/internal/score/scoredraw"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const (
	scoreHeaderW = float32(210)
	scoreRulerH  = float32(36)
	scorePad     = float32(18)
	scoreScrollW = float32(14)
)

// The paper look, after the mockup (docs/score/mockups): warm paper, near
// black ink, lighter staff lines, voices 2–4 in green, orange and magenta.
var (
	scoreColPaper     = scoreRGB(0xfb, 0xfa, 0xf6, 1)
	scoreColInk       = scoreRGB(0x1c, 0x1c, 0x1e, 1)
	scoreColStaff     = scoreRGB(0x55, 0x58, 0x5e, 1)
	scoreColSel       = scoreRGB(0x1f, 0x6f, 0xe0, 1)
	scoreColSelDim    = scoreRGB(0x1f, 0x6f, 0xe0, 0.5)
	scoreColSelFill   = scoreRGB(0x1f, 0x6f, 0xe0, 0.10)
	scoreColGhost     = scoreRGB(0x1f, 0x6f, 0xe0, 0.55)
	scoreColPlay      = scoreRGB(0x22, 0xa8, 0x61, 1)
	scoreColHeader    = scoreRGB(0x1e, 0x21, 0x27, 1)
	scoreColRuler     = scoreRGB(0x19, 0x1b, 0x20, 1)
	scoreColRulerText = scoreRGB(0xc8, 0xcd, 0xd6, 1)
	scoreColDim       = scoreRGB(0x8b, 0x93, 0xa1, 1)
	scoreColText      = scoreRGB(0xdf, 0xe3, 0xea, 1)
	scoreColTip       = scoreRGB(0x0d, 0x1a, 0x2e, 0.95)
	scoreColTipText   = scoreRGB(0xd6, 0xe6, 0xff, 1)
	scoreColEdge      = scoreRGB(0x0c, 0x0d, 0x10, 1)
	scoreColCaret     = scoreRGB(0x1f, 0x6f, 0xe0, 0.8)

	scoreColVoice = [score.MaxVoices]imgui.PackedColor{
		scoreRGB(0x1c, 0x1c, 0x1e, 1), scoreRGB(0x1b, 0x8a, 0x4a, 1), scoreRGB(0xc9, 0x71, 0x1d, 1), scoreRGB(0xb8, 0x35, 0x7f, 1),
	}
	// scoreVoiceUICol are the voices on the dark toolbar.
	scoreVoiceUICol = [score.MaxVoices]imgui.Vec4{
		{X: 0.50, Y: 0.70, Z: 1, W: 1}, {X: 0.37, Y: 0.83, Z: 0.58, W: 1}, {X: 0.94, Y: 0.64, Z: 0.36, W: 1}, {X: 0.94, Y: 0.50, Z: 0.75, W: 1},
	}
)

func scoreRGB(r, g, b uint8, a float32) imgui.PackedColor {
	return imgui.PackedColorFromVec4(imgui.Vec4{X: float32(r) / 255, Y: float32(g) / 255, Z: float32(b) / 255, W: a})
}

// scoreItemColour picks an item's colour: the selection, a voice's colour,
// the lighter staff lines, or ink.
func scoreItemColour(it *engrave.Item) imgui.PackedColor {
	if it.Role == engrave.RoleStaff {
		return scoreColStaff
	}
	r := it.Ref
	e := scEd
	if e.Sel.Kind == scoreSelEvent && r.Voice >= 0 && scoreSameEvent(r, e.Sel.Loc) {
		if e.Sel.Note >= 0 && r.Note >= 0 && r.Note != e.Sel.Note {
			return scoreColSelDim
		}
		return scoreColSel
	}
	if scoreVoiceColours && r.Voice > 0 && r.Voice < score.MaxVoices {
		return scoreColVoice[r.Voice]
	}
	return scoreColInk
}

// scoreViewGeom maps between the layout (staff spaces) and the screen.
type scoreViewGeom struct {
	origin imgui.Vec2 // the layout's (0,0) on screen
	zoom   float32
	// vis is the visible music area on screen.
	visMin, visMax imgui.Vec2
}

func (g scoreViewGeom) toScreen(p engrave.Point) imgui.Vec2 {
	return imgui.Vec2{X: g.origin.X + float32(p.X)*g.zoom, Y: g.origin.Y + float32(p.Y)*g.zoom}
}

func (g scoreViewGeom) toLayout(p imgui.Vec2) engrave.Point {
	return engrave.Point{X: float64((p.X - g.origin.X) / g.zoom), Y: float64((p.Y - g.origin.Y) / g.zoom)}
}

// clip is the visible part of the layout.
func (g scoreViewGeom) clip() engrave.Rect {
	return engrave.Rect{Min: g.toLayout(g.visMin), Max: g.toLayout(g.visMax)}
}

// scoreViewPane draws the score and takes the pointer.
func scoreViewPane(pos, size imgui.Vec2) {
	e := scEd
	l := scoreLayout()
	z := scoreZoom
	io := imgui.CurrentIO()
	ctrl := io.KeyCtrlPressed()

	contentW := scoreHeaderW + float32(l.Width)*z + 2*scorePad
	contentH := scoreRulerH + float32(l.Height)*z + 2*scorePad
	imgui.SetNextWindowPos(pos)
	imgui.SetNextWindowSize(size)
	imgui.SetNextWindowContentSize(imgui.Vec2{X: contentW, Y: contentH})
	flags := trkPaneFlags | imgui.WindowFlagsHorizontalScrollbar
	if ctrl {
		flags |= imgui.WindowFlagsNoScrollWithMouse
	}
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{})
	imgui.PushStyleColor(imgui.StyleColorWindowBg, imgui.Vec4{X: 0.984, Y: 0.980, Z: 0.965, W: 1})
	imgui.BeginV("##scoreview", nil, flags)
	imgui.PopStyleColor()
	imgui.PopStyleVar()

	wp := imgui.WindowPos()
	sx, sy := imgui.ScrollX(), imgui.ScrollY()
	scoreViewScroll = imgui.Vec2{X: sx, Y: sy}
	g := scoreViewGeom{
		origin: imgui.Vec2{X: wp.X + scoreHeaderW + scorePad - sx, Y: wp.Y + scoreRulerH + scorePad - sy},
		zoom:   z,
		visMin: imgui.Vec2{X: wp.X + scoreHeaderW, Y: wp.Y + scoreRulerH},
		visMax: imgui.Vec2{X: wp.X + size.X - scoreScrollW, Y: wp.Y + size.Y - scoreScrollW},
	}
	visW, visH := g.visMax.X-g.visMin.X, g.visMax.Y-g.visMin.Y
	imgui.SetCursorPos(imgui.Vec2{X: sx + scoreHeaderW, Y: sy + scoreRulerH})
	imgui.InvisibleButton("##sccanvas", imgui.Vec2{X: max(visW, 1), Y: max(visH, 1)})
	hovered := imgui.IsItemHovered()
	clicked := imgui.IsItemClicked()

	dl := imgui.WindowDrawList()
	dl.PushClipRect(g.visMin, g.visMax)
	scoreDrawLoopShade(dl, l, g)
	scoreDrawSelection(dl, l, g)
	scoreRend.Draw(dl, l, scoredraw.View{Origin: g.origin, Scale: z, Clip: g.clip(), Ink: scoreColInk, Colour: scoreItemColour, Faint: 0.3})
	scoreDrawLanes(dl, l, g)
	scoreDrawCaret(dl, l, g)
	scoreDrawPlayhead(dl, l, g)
	mouse := imgui.MousePos()
	// A lane takes the pointer over it (and a drag begun in it wherever the
	// pointer goes): no ghost there, and its click is the lane's.
	inLane := scoreLanePointer(l, g, hovered, clicked)
	var target scoreTarget
	targetOK := false
	if hovered && !inLane {
		target, targetOK = scorePointerTarget(l, e.Score, e.Voice, e.Dur, g.toLayout(mouse))
		if e.Input && targetOK {
			scoreDrawGhost(dl, l, g, target)
		}
	}
	dl.PopClipRect()
	scoreRuler(dl, l, g, wp, size)
	scoreHeaderColumn(dl, l, g, wp, size)
	if hovered && !inLane && e.Input && targetOK {
		scoreDrawGhostTip(dl, g, target)
	}
	scoreFollow(l, g)

	// Ctrl+wheel zooms about the pointer.
	if _, wheel := io.MouseWheel(); hovered && ctrl && wheel != 0 {
		at := g.toLayout(mouse)
		scoreSetZoom(scoreZoom * float32(math.Pow(1.15, float64(wheel))))
		imgui.SetScrollX(float32(at.X)*scoreZoom + scoreHeaderW + scorePad - (mouse.X - wp.X))
		imgui.SetScrollY(float32(at.Y)*scoreZoom + scoreRulerH + scorePad - (mouse.Y - wp.Y))
	}
	if clicked && !inLane {
		scoreClick(l, g.toLayout(mouse), target, targetOK, io)
	}
	if e.Words != nil && !scoreDrawWordField(l, g) {
		scoreDrawPinnedWordField(g)
	}
	if e.Reveal {
		e.Reveal = false
		scoreReveal(l, g, wp, size)
	}
	imgui.End()
}

// scoreViewScroll is the score view's scroll as last drawn (what following
// the play head moves; the tests read it).
var scoreViewScroll imgui.Vec2

// scoreClick is a left click on the music.
func scoreClick(l *engrave.List, p engrave.Point, t scoreTarget, tOK bool, io imgui.IO) {
	e := scEd
	// A click on the music lets go of a lane's override, and finishes the
	// syllable or chord symbol being typed.
	e.LaneSel = scoreLaneSel{Index: -1}
	if e.Words != nil {
		e.wordsKey(scoreWordEnter)
		e.Words = nil
	}
	switch {
	case io.KeyAltPressed():
		if tOK {
			e.pasteAt(t.Line, t.Staff, t.Bar, t.At)
		}
	case e.Input:
		e.TabKeys = tOK && t.Tab
		switch {
		case tOK && t.Tab:
			e.clickTab(t.Line, t.Staff, t.Bar, t.At, t.String)
		case tOK:
			e.clickWrite(t.Line, t.Staff, t.Bar, t.At, t.Diatonic)
		}
	default:
		if cb := l.ChordAt(p); cb != nil && cb.Index < len(e.Score.Lines[cb.Line].Chords) {
			e.editChordAt(cb.Line, e.Score.Lines[cb.Line].Chords[cb.Index].At)
			return
		}
		sb := l.StaffAt(p.Y)
		e.TabKeys = sb != nil && sb.Tab
		if e.TabKeys {
			e.TabString = sb.StringAt(p.Y)
		}
		if ref, ok := scoreHitEvent(l, p); ok && edit.Event(e.Score, scoreRefLoc(ref)) != nil {
			e.selectEvent(scoreRefLoc(ref), ref.Note)
			// A clicked note is where the play head goes (playing or not),
			// as the mockup has it: Space then plays from there.
			e.seekToSel()
			return
		}
		if line, staff, bar, ok := scoreHitBarStaff(l, p); ok {
			e.clickBarStaff(line, staff, bar, io.KeyCtrlPressed(), io.KeyShiftPressed())
			return
		}
		e.Sel = scoreSel{}
		e.Status = ""
	}
}

// scoreReveal scrolls the selection into sight.
func scoreReveal(l *engrave.List, g scoreViewGeom, wp, size imgui.Vec2) {
	var r engrave.Rect
	ok := false
	switch scEd.Sel.Kind {
	case scoreSelEvent:
		r, ok = scoreEventRect(l, scEd.Sel.Loc)
	case scoreSelBars:
		r, ok = scoreBarsRect(l, scEd.Sel.LineA, scEd.Sel.LineA, scEd.Sel.BarA, scEd.Sel.BarA)
	case scoreSelCaret:
		r, ok = scoreBarsRect(l, scEd.Sel.Loc.Line, scEd.Sel.Loc.Line, scEd.Sel.Loc.Bar, scEd.Sel.Loc.Bar)
	}
	if !ok {
		return
	}
	a, b := g.toScreen(r.Min), g.toScreen(r.Max)
	margin := float32(60)
	if a.X < g.visMin.X+margin || b.X > g.visMax.X-margin {
		x := float32(r.Min.X)*g.zoom + scoreHeaderW + scorePad - (g.visMax.X-g.visMin.X)/3 - scoreHeaderW
		imgui.SetScrollX(max(0, x))
	}
	if a.Y < g.visMin.Y || b.Y > g.visMax.Y {
		y := float32(r.Min.Y)*g.zoom + scoreRulerH + scorePad - (g.visMax.Y-g.visMin.Y)/3 - scoreRulerH
		imgui.SetScrollY(max(0, y))
	}
}

// scoreDrawSelection shades selected bars under the music.
func scoreDrawSelection(dl imgui.DrawList, l *engrave.List, g scoreViewGeom) {
	q := scEd.Sel
	if q.Kind != scoreSelBars {
		return
	}
	// One box per line, so a gap between lines reads as two selections of
	// the same bars rather than one block over another line's music.
	for li := q.LineA; li <= q.LineB; li++ {
		r, ok := scoreBarsRect(l, li, li, q.BarA, q.BarB)
		if !ok {
			continue
		}
		a, b := g.toScreen(r.Min), g.toScreen(r.Max)
		dl.AddRectFilledV(a, b, scoreColSelFill, 2, imgui.DrawFlagsRoundCornersAll)
		dl.AddRectV(a, b, scoreColSel, 2, imgui.DrawFlagsRoundCornersAll, 1.4)
	}
}

// scoreDrawCaret marks where the next typed note goes, in note input.
func scoreDrawCaret(dl imgui.DrawList, l *engrave.List, g scoreViewGeom) {
	e := scEd
	if !e.Input {
		return
	}
	loc, at, ok := e.caret()
	if !ok {
		return
	}
	bb := l.Bar(loc.Bar)
	if bb == nil {
		return
	}
	for _, sb := range l.Staves {
		if sb.Line != loc.Line || sb.Staff != loc.Staff {
			continue
		}
		x := bb.XAt(at.Float()) - 0.35
		a := g.toScreen(engrave.Point{X: x, Y: sb.Top - 1})
		b := g.toScreen(engrave.Point{X: x, Y: sb.Top + 5})
		dl.AddLineV(a, b, scoreColCaret, max(1.5, g.zoom*0.18))
	}
}

// scoreDrawPlayhead is the play head: a line across every staff, where the
// music is while it plays and where it will start when stopped.
func scoreDrawPlayhead(dl imgui.DrawList, l *engrave.List, g scoreViewGeom) {
	hx, ok := scoreHeadX(l)
	if !ok || len(l.Staves) == 0 {
		return
	}
	x := g.toScreen(engrave.Point{X: hx}).X
	top := g.toScreen(engrave.Point{Y: l.Staves[0].Top - 3}).Y
	bottom := g.toScreen(engrave.Point{Y: l.Staves[len(l.Staves)-1].Top + 7}).Y
	dl.AddLineV(imgui.Vec2{X: x, Y: max(top, g.visMin.Y)}, imgui.Vec2{X: x, Y: min(bottom, g.visMax.Y)}, scoreColPlay, 1.6)
}

// scoreDrawGhost is the notehead a click would write, spelled in the key,
// with its ledger lines and the armed accidental.
func scoreDrawGhost(dl imgui.DrawList, l *engrave.List, g scoreViewGeom, t scoreTarget) {
	var sb *engrave.StaffBox
	for i := range l.Staves {
		if l.Staves[i].Line == t.Line && l.Staves[i].Staff == t.Staff && l.Staves[i].Tab == t.Tab {
			sb = &l.Staves[i]
		}
	}
	if sb == nil {
		return
	}
	f := glyph.Bravura()
	if t.Tab {
		// The fret a click writes: the open string, on its line.
		it := engrave.Item{Kind: engrave.ItemText, Text: "0", Size: 1.35, Align: engrave.AlignCenter, Font: engrave.TextPlain,
			P: engrave.Point{X: t.X + 0.59, Y: t.Y + 0.49}, Ref: engrave.NoRef}
		scoreRend.Draw(dl, &engrave.List{Items: []engrave.Item{it}}, scoredraw.View{Origin: g.origin, Scale: g.zoom, Ink: scoreColGhost})
		return
	}
	head := scoreHeadGlyph(scEd.Dur.Base)
	drums := scEd.percussion(t.Line, t.Staff)
	if drums {
		d, ok := scEd.ghostDrum(t.Line, t.Diatonic)
		if !ok {
			return
		}
		head = engrave.DrumHeadGlyph(d.Head, scEd.Dur.Base)
	}
	hw := f.Width(head)
	items := []engrave.Item{{Kind: engrave.ItemGlyph, Glyph: head, Size: 1, P: engrave.Point{X: t.X, Y: t.Y}, Ref: engrave.NoRef}}
	// Ledger lines from the staff out to the note.
	_, bottom := sb.ClefAt(t.X)
	pos := t.Diatonic - bottom
	ext := f.Defaults.LegerLineExtension
	led := func(d int) {
		y := sb.YAt(t.X, bottom+d)
		items = append(items, engrave.Item{Kind: engrave.ItemLine, P: engrave.Point{X: t.X - ext, Y: y}, Q: engrave.Point{X: t.X + hw + ext, Y: y},
			Width: f.Defaults.LegerLineThickness, Ref: engrave.NoRef})
	}
	for d := -2; d >= pos; d -= 2 {
		led(d)
	}
	for d := 10; d <= pos; d += 2 {
		led(d)
	}
	p := scEd.ghost(t.Line, t.Staff, t.Bar, t.Diatonic)
	if scEd.Armed && !drums {
		name := scoreAccGlyph(int(p.Alter))
		items = append(items, engrave.Item{Kind: engrave.ItemGlyph, Glyph: name, Size: 1,
			P: engrave.Point{X: t.X - f.Width(name) - 0.25, Y: t.Y}, Ref: engrave.NoRef})
	}
	scoreRend.Draw(dl, &engrave.List{Items: items}, scoredraw.View{Origin: g.origin, Scale: g.zoom, Ink: scoreColGhost})
}

// scoreDrawGhostTip is the label beside the ghost: pitch, value, voice.
func scoreDrawGhostTip(dl imgui.DrawList, g scoreViewGeom, t scoreTarget) {
	p := scEd.ghost(t.Line, t.Staff, t.Bar, t.Diatonic)
	what := edit.PitchName(p)
	if t.Tab {
		what = fmt.Sprintf("string %d open", t.String)
		if st := edit.Staff(scEd.Score, t.Line, t.Staff); st != nil && st.Tab != nil {
			what += " (" + edit.PitchName(score.SpellMIDI(st.Tab.Open(t.String), 0, 1)) + "), then type the fret"
		}
	} else if scEd.percussion(t.Line, t.Staff) {
		what = "no drum here"
		if d, ok := scEd.ghostDrum(t.Line, t.Diatonic); ok {
			what = d.Name
		}
	}
	text := fmt.Sprintf("%s  %s  voice %d", what, edit.DurName(scEd.Dur), scEd.Voice+1)
	at := g.toScreen(engrave.Point{X: t.X, Y: t.Y})
	sz := imgui.CalcTextSize(text, false, 0)
	a := imgui.Vec2{X: at.X + 16, Y: at.Y - sz.Y - 14}
	dl.AddRectFilledV(a, imgui.Vec2{X: a.X + sz.X + 12, Y: a.Y + sz.Y + 6}, scoreColTip, 4, imgui.DrawFlagsRoundCornersAll)
	dl.AddText(imgui.Vec2{X: a.X + 6, Y: a.Y + 3}, scoreColTipText, text)
}

// scoreClipText cuts text to a pixel width with an ellipsis.
func scoreClipText(s string, w float32) string {
	if imgui.CalcTextSize(s, false, 0).X <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && imgui.CalcTextSize(string(r)+"...", false, 0).X > w {
		r = r[:len(r)-1]
	}
	return string(r) + "..."
}

// scoreHeadGlyph is a value's notehead.
func scoreHeadGlyph(b score.DurBase) string {
	switch {
	case b <= score.Breve:
		return "noteheadDoubleWhole"
	case b == score.WholeNote:
		return "noteheadWhole"
	case b == score.Half:
		return "noteheadHalf"
	}
	return "noteheadBlack"
}

// scoreAccGlyph is an alteration's accidental.
func scoreAccGlyph(alter int) string {
	switch alter {
	case -2:
		return "accidentalDoubleFlat"
	case -1:
		return "accidentalFlat"
	case 1:
		return "accidentalSharp"
	case 2:
		return "accidentalDoubleSharp"
	}
	return "accidentalNatural"
}

// ---------- glyphs on buttons ----------

// scoreDrawGlyph draws one SMuFL glyph centred in a box, as large as fits
// in most of it.
func scoreDrawGlyph(dl imgui.DrawList, name string, a, b imgui.Vec2, col imgui.PackedColor) {
	box, ok := glyph.Bravura().Box(name)
	if !ok || box.W() <= 0 || box.H() <= 0 || scoreRend == nil {
		return
	}
	w, h := float64(b.X-a.X), float64(b.Y-a.Y)
	s := math.Min(w*0.8/box.W(), h*0.88/box.H())
	s = math.Min(s, h/3) // a small glyph (a dot) stays at a note's scale
	cx, cy := float64(a.X+b.X)/2, float64(a.Y+b.Y)/2
	origin := imgui.Vec2{X: float32(cx - (box.Min.X+box.W()/2)*s), Y: float32(cy - (box.Min.Y+box.H()/2)*s)}
	list := &engrave.List{Items: []engrave.Item{{Kind: engrave.ItemGlyph, Glyph: name, Size: 1, Ref: engrave.NoRef}}}
	scoreRend.Draw(dl, list, scoredraw.View{Origin: origin, Scale: float32(s), Ink: col})
}

// scoreGlyphButton is a toolbar button showing a glyph, lit when on.
func scoreGlyphButton(id, glyphName string, on bool) bool {
	sz := imgui.Vec2{X: 26, Y: imgui.FrameHeight()}
	pressed := imgui.InvisibleButton(id, sz)
	a, b := imgui.ItemRectMin(), imgui.ItemRectMax()
	style := imgui.CurrentStyle()
	col := style.Color(imgui.StyleColorButton)
	switch {
	case on:
		col = scoreColActive
	case imgui.IsItemActive():
		col = style.Color(imgui.StyleColorButtonActive)
	case imgui.IsItemHovered():
		col = style.Color(imgui.StyleColorButtonHovered)
	}
	dl := imgui.WindowDrawList()
	dl.AddRectFilledV(a, b, imgui.PackedColorFromVec4(col), style.FrameRounding(), imgui.DrawFlagsRoundCornersAll)
	scoreDrawGlyph(dl, glyphName, a, b, scoreRGB(0xdf, 0xe3, 0xea, 1))
	return pressed
}

// ---------- the status bar ----------

// scoreStatusBar is the line along the bottom: mode, key, meter, duration,
// voice, selection, clipboard, keymap.
func scoreStatusBar(pos imgui.Vec2, w float32) {
	imgui.SetNextWindowPos(pos)
	imgui.SetNextWindowSize(imgui.Vec2{X: w, Y: scoreStatusH})
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{X: 10, Y: 5})
	imgui.PushStyleColor(imgui.StyleColorWindowBg, imgui.Vec4{X: 0.063, Y: 0.071, Z: 0.086, W: 1})
	imgui.BeginV("##scorestatus", nil, trkPaneFlags|imgui.WindowFlagsNoScrollbar)
	imgui.PopStyleColor()
	imgui.PopStyleVar()
	for i, part := range scoreStatusParts() {
		if i > 0 {
			imgui.SameLineV(0, 18)
		}
		if i == 0 {
			imgui.Text(part)
			continue
		}
		editorTextMuted(part)
	}
	imgui.End()
}

// scoreStatusParts are the status bar's pieces, the mode first.
func scoreStatusParts() []string {
	e := scEd
	bar := scoreSelBar()
	mode := "Select"
	if e.Input {
		mode = "Note input"
	}
	key := e.Score.KeyAt(bar)
	parts := []string{
		mode,
		scoreKeyName(key.Fifths, key.Mode),
		e.Score.MeterAt(bar).String(),
		"Duration: " + edit.DurName(e.Dur),
		fmt.Sprintf("Voice %d", e.Voice+1),
	}
	if e.Keypad != scoreKpNotes {
		parts = append(parts, "Keypad: "+scoreKpNames[e.Keypad])
	}
	if e.Armed {
		parts = append(parts, []string{"Sharp", "Flat", "Natural"}[e.ArmedAcc]+" armed")
	}
	parts = append(parts, e.describeSel())
	switch {
	case e.Clip != nil:
		parts = append(parts, fmt.Sprintf("Clipboard: %d line(s) x %d bar(s)", len(e.Clip.Lines), e.Clip.Bars))
	case e.ClipEvent != nil:
		parts = append(parts, "Clipboard: "+scoreEventText(e, 0, 0, e.ClipEvent))
	}
	if e.Score.Concert {
		parts = append(parts, "Concert pitch")
	}
	if e.Grace != score.NotGrace {
		parts = append(parts, map[score.Grace]string{score.Acciaccatura: "Grace notes: acciaccaturas", score.Appoggiatura: "Grace notes: appoggiaturas"}[e.Grace])
	}
	if e.Transport.Loop.On {
		parts = append(parts, scoreLoopText(e.Score, e.Transport.Loop))
	}
	if e.Transport.Scale != 1 {
		parts = append(parts, fmt.Sprintf("Tempo %.0f%%", e.Transport.Scale*100))
	}
	if m := scoreMIDIStatus(); m != "" {
		parts = append(parts, m)
	}
	parts = append(parts, "Keymap: "+scoreKeymapActive().Name+" (F1)")
	return parts
}
