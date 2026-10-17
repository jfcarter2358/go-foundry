package editor

// editor_score_words_view.go — the field lyrics and chord symbols are typed
// into, drawn where the text will stand: under the note for a syllable
// (at its verse's line), over the staff for a chord symbol. What it does
// with what is typed is editor_score_words.go's; the keys it acts on
// itself (Return, Escape, Tab, Backspace in an empty field) come through
// scoreWordsEvent. Each place has its own field (its own ImGui id), so a
// field moved on to the next note starts empty of the last one's text.

import (
	"fmt"

	"foundry/internal/score"
	"foundry/internal/score/engrave"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreWordFieldAt is where the word field goes on a layout, in staff
// spaces: the left of its text and its baseline. ok is false when the
// place is not on the layout.
func scoreWordFieldAt(l *engrave.List, w *scoreWordEntry) (x, y float64, ok bool) {
	switch w.Kind {
	case scoreWordLyric:
		for _, eb := range l.Events {
			if scoreSameEvent(eb.Ref, w.Loc) {
				x, ok = eb.X, true
				break
			}
		}
		found := false
		for _, sb := range l.Staves {
			if sb.Line == w.Loc.Line && sb.Staff == w.Loc.Staff && !found {
				// The notation's box first (a linked tablature's comes after).
				y, found = sb.BottomY()+2.9+float64(w.Verse-1)*2.3, true
			}
		}
		return x - 1, y, ok && found
	case scoreWordChord:
		bb := l.Bar(w.At.Bar)
		if bb == nil {
			return 0, 0, false
		}
		for _, sb := range l.Staves {
			if sb.Line == w.Line {
				return bb.XAt(w.At.Offset.Float()) - 0.3, sb.Top - 3, true
			}
		}
	}
	return 0, 0, false
}

// scoreDrawWordField draws the field on a layout, if its place is there
// and in sight, and gives it what is typed. It reports whether it was drawn.
func scoreDrawWordField(l *engrave.List, g scoreViewGeom) bool {
	e := scEd
	w := e.Words
	if w == nil {
		return false
	}
	x, y, ok := scoreWordFieldAt(l, w)
	if !ok {
		return false
	}
	h := imgui.FrameHeight()
	pos := g.toScreen(engrave.Point{X: x, Y: y})
	pos.Y -= h * 0.75
	if pos.X < g.visMin.X || pos.X > g.visMax.X || pos.Y < g.visMin.Y || pos.Y > g.visMax.Y {
		return false
	}
	width := max(110, 9*g.zoom)
	if w.Kind == scoreWordChord {
		width = max(150, 12*g.zoom)
	}
	scoreWordInput(pos, width)
	return true
}

// scoreDrawPinnedWordField draws the field at the view's foot when its
// place is out of sight (scrolled away while typing), so the words go on
// being typed into it rather than running into the score's keys. It says
// where they go.
func scoreDrawPinnedWordField(g scoreViewGeom) {
	e := scEd
	w := e.Words
	if w == nil {
		return
	}
	h := imgui.FrameHeight()
	pos := imgui.Vec2{X: g.visMin.X + 12, Y: g.visMax.Y - h - 10}
	where := ""
	switch w.Kind {
	case scoreWordLyric:
		where = fmt.Sprintf("Lyric, verse %d, %s bar %d", w.Verse, e.lineName(w.Loc.Line)+",", w.Loc.Bar)
	case scoreWordChord:
		where = fmt.Sprintf("Chord symbol, %s %s", e.lineName(w.Line)+",", e.Score.FormatPos(w.At))
	}
	dl := imgui.WindowDrawList()
	tw := imgui.CalcTextSize(where, false, 0).X
	dl.AddRectFilledV(imgui.Vec2{X: pos.X - 6, Y: pos.Y - h - 4}, imgui.Vec2{X: pos.X + max(tw, 200) + 12, Y: pos.Y + h + 4},
		imgui.PackedColorFromVec4(imgui.Vec4{X: 0.12, Y: 0.14, Z: 0.18, W: 0.94}), 4, imgui.DrawFlagsRoundCornersAll)
	dl.AddText(imgui.Vec2{X: pos.X, Y: pos.Y - h}, imgui.PackedColorFromVec4(imgui.Vec4{X: 0.75, Y: 0.8, Z: 0.9, W: 1}), where)
	scoreWordInput(pos, 200)
}

// scoreWordInput is the field itself at a place on screen.
func scoreWordInput(pos imgui.Vec2, width float32) {
	e := scEd
	w := e.Words
	imgui.SetCursorScreenPos(pos)
	imgui.PushStyleColor(imgui.StyleColorFrameBg, imgui.Vec4{X: 0.93, Y: 0.95, Z: 1, W: 0.97})
	imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 0.06, Y: 0.1, Z: 0.2, W: 1})
	imgui.PushStyleColor(imgui.StyleColorBorder, imgui.Vec4{X: 0.12, Y: 0.44, Z: 0.88, W: 1})
	imgui.PushStyleVarFloat(imgui.StyleVarFrameBorderSize, 1.5)
	if w.Focus {
		imgui.SetKeyboardFocusHere()
		w.Focus = false
	}
	id := fmt.Sprintf("##scword%d_%d_%d_%d_%d_%d_%d_%d_%s", w.Kind, w.Loc.Line, w.Loc.Staff, w.Loc.Voice, w.Loc.Bar, w.Loc.Event, w.Verse, w.Line, e.Score.FormatPos(w.At))
	text := w.Text
	imgui.SetNextItemWidth(width)
	if imgui.InputTextV(id, &text, 0, nil) {
		e.wordsInput(text)
	}
	imgui.PopStyleVar()
	imgui.PopStyleColor()
	imgui.PopStyleColor()
	imgui.PopStyleColor()
}

// scoreChordMenu is a line's Chord Symbols menu: how they play, and on
// what.
func scoreChordMenu(li int, l *score.Line) {
	e := scEd
	if !imgui.BeginMenu("Chord Symbols") {
		return
	}
	editorTextMuted(fmt.Sprintf("%d on the line; %s types one", len(l.Chords), scoreKeymapActive().label(scChordSymbol)))
	style, inst := score.ChordsSilent, ""
	if l.ChordPlay != nil {
		style, inst = l.ChordPlay.Style, l.ChordPlay.Instrument
	}
	for k, name := range []string{"Printed Only", "Play as a Pad", "Play as Comping"} {
		if imgui.MenuItemV(name+"##sccp"+fmt.Sprint(k), "", style == score.ChordStyle(k), true) {
			e.setChordPlay(li, score.ChordStyle(k), inst)
		}
	}
	imgui.Separator()
	on := inst
	if on == "" {
		on = "the line's instrument"
	}
	editorTextMuted("Played on " + on)
	if imgui.MenuItemV("On the Line's Instrument", "", inst == "", style != score.ChordsSilent) {
		e.setChordPlay(li, style, "")
	}
	if imgui.MenuItemV("On Another Instrument...", "", false, true) {
		scoreOpenChordPicker(li, imgui.MousePos())
	}
	imgui.EndMenu()
}

// scoreTabMenu is a line's Tablature menu, per staff that can have one:
// none, under the notation or alone, the tuning, the capo.
func scoreTabMenu(li int, l *score.Line) {
	e := scEd
	if !imgui.BeginMenu("Tablature") {
		return
	}
	for si, st := range l.Staves {
		if st.Percussion() {
			continue
		}
		if len(l.Staves) > 1 {
			editorTextMuted(fmt.Sprintf("Staff %d", si+1))
		}
		tuning := e.lineTabTuning(li)
		if st.Tab != nil && st.Tab.Preset() != "" {
			tuning = st.Tab.Preset()
		}
		if imgui.MenuItemV(fmt.Sprintf("None##sctb%d", si), "", st.Tab == nil, true) {
			e.setLineTab(li, si, "", false)
		}
		if imgui.MenuItemV(fmt.Sprintf("Under the Notation##sctb%d", si), "", st.Tab != nil && !st.Tab.Only, true) {
			e.setLineTab(li, si, tuning, false)
		}
		editorMenuTooltip("The staff's notes on the strings under it: an edit on either is an edit of both.")
		if imgui.MenuItemV(fmt.Sprintf("Alone##sctb%d", si), "", st.Tab != nil && st.Tab.Only, true) {
			e.setLineTab(li, si, tuning, true)
		}
		editorMenuTooltip("Tablature only, with the rhythm under the strings.")
		if st.Tab != nil {
			if imgui.BeginMenu(fmt.Sprintf("Tuning##sctbt%d", si)) {
				for _, p := range score.TuningPresets {
					if imgui.MenuItemV(p.Label+"##sctbp"+p.Name, "", st.Tab.Preset() == p.Name, true) {
						e.setLineTab(li, si, p.Name, st.Tab.Only)
					}
				}
				imgui.EndMenu()
			}
			if imgui.BeginMenu(fmt.Sprintf("Capo (fret %d)##sctbc%d", st.Tab.Capo, si)) {
				for c := 0; c <= 12; c++ {
					label := fmt.Sprintf("Fret %d", c)
					if c == 0 {
						label = "No Capo"
					}
					if imgui.MenuItemV(label+fmt.Sprintf("##sctbc%d_%d", si, c), "", st.Tab.Capo == c, true) {
						e.setTabCapo(li, si, c)
					}
				}
				imgui.EndMenu()
			}
		}
	}
	imgui.EndMenu()
}
