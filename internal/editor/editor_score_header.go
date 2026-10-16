package editor

// editor_score_header.go — the staff headers down the left of the Score
// view (mockup screen 1): each line's name, its instrument, mute, solo,
// duplicate, a volume slider and a live meter, beside its staves; and the
// popups they open — the instrument picker with its search, the duplicate
// menu, the line's context menu.
//
// The column scrolls with the music vertically and not across, so its
// widgets are placed in screen space from the layout's staves each frame. It
// covers only the column; the music's InvisibleButton covers only the music,
// so neither eats the other's clicks (the Tracker header trap). A widget is
// only made when all of it is in sight below the ruler: one half under the
// ruler would take the ruler's clicks.
//
// The popups are opened by flags and drawn from scoreDialogs at the top
// level, so their IDs do not depend on the line being drawn when they were
// asked for.

import (
	"fmt"
	"math"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/engrave"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// Header colours, after the mockup.
var (
	scoreColFrame   = scoreRGB(0x2a, 0x2e, 0x36, 1)
	scoreColFrameHi = scoreRGB(0x35, 0x3a, 0x45, 1)
	scoreColMute    = scoreRGB(0xa3, 0x50, 0x3a, 1)
	scoreColSolo    = scoreRGB(0x9a, 0x84, 0x20, 1)
	scoreColLaneOn  = scoreRGB(0x2c, 0x5f, 0xae, 1)
	scoreColChip    = scoreRGB(0xcf, 0xd5, 0xde, 1)
	scoreColTrack   = scoreRGB(0x0f, 0x11, 0x14, 1)
	scoreColAccent  = scoreRGB(0x42, 0x96, 0xfa, 1)
	scoreColKnob    = scoreRGB(0xdf, 0xe3, 0xea, 1)
	scoreColMeterLo = scoreRGB(0x22, 0xa8, 0x61, 1)
	scoreColMeterMd = scoreRGB(0xd6, 0xb6, 0x2a, 1)
	scoreColMeterHi = scoreRGB(0xe0, 0x48, 0x3c, 1)
)

// Header and popup state.
var (
	// scoreRenameLine is the line whose name is being typed over, -1 none.
	scoreRenameLine  = -1
	scoreRenameText  string
	scoreRenameFocus bool

	// The instrument picker: for line scorePickLine, or (scorePickAdd) a
	// new line at scorePickAt.
	scorePickOpen bool
	scorePickLine int
	scorePickAdd  bool
	scorePickAt   int
	scorePickText string
	scorePickPos  imgui.Vec2

	// The duplicate menu and the line's context menu, for scoreMenuLine.
	scoreDupOpen  bool
	scoreLineOpen bool
	scoreMenuLine int

	// scoreInstNames is the library, scanned when the picker opens.
	scoreInstNames []string

	// scoreMeterVals are the meters as shown: each line's peak, falling.
	scoreMeterVals []float32
)

// scoreLineBand is the vertical extent of a line's staves on the layout:
// its first staff's top line to its last staff's bottom line.
func scoreLineBand(l *engrave.List, line int) (top, bottom float64, ok bool) {
	top, bottom = math.Inf(1), math.Inf(-1)
	for _, sb := range l.Staves {
		if sb.Line == line {
			top, bottom = math.Min(top, sb.Top), math.Max(bottom, sb.Top+4)
		}
	}
	return top, bottom, !math.IsInf(top, 1)
}

// scoreMeter is line i's meter as shown this frame: the player's recent
// peak, held and falling a tenth a frame.
func scoreMeter(i int) float32 {
	for len(scoreMeterVals) <= i {
		scoreMeterVals = append(scoreMeterVals, 0)
	}
	lvl := float32(0)
	if scorePlayer != nil {
		lvl = scorePlayer.LineLevel(i)
	}
	v := max(lvl, scoreMeterVals[i]*0.9)
	scoreMeterVals[i] = v
	return v
}

// scoreHeaderColumn draws the headers, scrolled with the music vertically.
func scoreHeaderColumn(dl imgui.DrawList, l *engrave.List, g scoreViewGeom, wp, size imgui.Vec2) {
	e := scEd
	top := wp.Y + scoreRulerH
	dl.AddRectFilled(imgui.Vec2{X: wp.X, Y: top}, imgui.Vec2{X: wp.X + scoreHeaderW, Y: wp.Y + size.Y}, scoreColHeader)
	dl.AddLine(imgui.Vec2{X: wp.X + scoreHeaderW, Y: top}, imgui.Vec2{X: wp.X + scoreHeaderW, Y: wp.Y + size.Y}, scoreColEdge)
	clipMin, clipMax := imgui.Vec2{X: wp.X, Y: top}, imgui.Vec2{X: wp.X + scoreHeaderW, Y: g.visMax.Y}
	dl.PushClipRect(clipMin, clipMax)
	defer dl.PopClipRect()
	if scoreRenameLine >= len(e.Score.Lines) {
		scoreRenameLine = -1
	}
	mouse := imgui.MousePos()
	inColumn := imgui.IsWindowHovered() && mouse.X >= wp.X && mouse.X < wp.X+scoreHeaderW && mouse.Y >= top && mouse.Y < g.visMax.Y
	for li, line := range e.Score.Lines {
		lo, hi, ok := scoreLineBand(l, li)
		if !ok {
			continue
		}
		a, b := g.toScreen(engrave.Point{Y: lo}), g.toScreen(engrave.Point{Y: hi})
		mid := (a.Y + b.Y) / 2
		// The band a line's header may use: half way to its neighbours.
		bandTop, bandBot := a.Y-2*g.zoom, b.Y+2*g.zoom
		if bandBot < top || bandTop > g.visMax.Y {
			continue
		}
		selected := scoreLineSelected(li)
		if selected {
			dl.AddRectFilled(imgui.Vec2{X: wp.X + scoreHeaderW - 4, Y: a.Y}, imgui.Vec2{X: wp.X + scoreHeaderW - 1, Y: b.Y}, scoreColSel)
		}
		// The meter, the height of the staves, on the right edge.
		mx := wp.X + scoreHeaderW - 12
		dl.AddRectFilledV(imgui.Vec2{X: mx, Y: a.Y}, imgui.Vec2{X: mx + 4, Y: b.Y}, scoreColTrack, 1, imgui.DrawFlagsRoundCornersAll)
		scoreDrawMeterBar(dl, imgui.Vec2{X: mx, Y: a.Y}, imgui.Vec2{X: mx + 4, Y: b.Y}, scoreMeter(li))

		compact := bandBot-bandTop < 70
		x := wp.X + 10
		w := scoreHeaderW - 30
		inSight := func(y0, y1 float32) bool { return y0 >= top && y1 <= g.visMax.Y }
		imgui.PushID(fmt.Sprintf("schdr%d", li))
		// Row 1: the name; a click selects the line, a double-click renames.
		nameY := mid - 30
		if compact {
			nameY = mid - 17
		}
		nameCol := scoreColText
		if line.Mute || (scoreAnySolo() && !line.Solo) {
			nameCol = scoreColDim
		}
		if scoreRenameLine == li && inSight(nameY, nameY+20) {
			imgui.SetCursorScreenPos(imgui.Vec2{X: x - 4, Y: nameY - 2})
			imgui.SetNextItemWidth(w + 4)
			if scoreRenameFocus {
				imgui.SetKeyboardFocusHere()
				scoreRenameFocus = false
			}
			done := imgui.InputTextV("##screname", &scoreRenameText, imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil)
			if done || imgui.IsItemDeactivated() {
				if t := strings.TrimSpace(scoreRenameText); t != "" && t != line.Name {
					e.renameLine(li, t)
				}
				scoreRenameLine = -1
			}
		} else {
			dl.AddText(imgui.Vec2{X: x, Y: nameY}, nameCol, scoreClipText(e.lineName(li), w))
			if inSight(nameY, nameY+16) {
				imgui.SetCursorScreenPos(imgui.Vec2{X: x, Y: nameY})
				imgui.InvisibleButton("##scname", imgui.Vec2{X: w, Y: 16})
				if imgui.IsItemHovered() && imgui.IsMouseDoubleClicked(0) {
					scoreRenameLine, scoreRenameText, scoreRenameFocus = li, line.Name, true
				} else if imgui.IsItemClicked() {
					e.selectLine(li)
				}
				editorMenuTooltip(e.lineName(li) + "\nClick: select the line. Double-click: rename. Right-click: line commands.")
			}
		}
		// Row 2: the instrument chip.
		if !compact {
			cy := mid - 10
			if inSight(cy, cy+19) {
				imgui.SetCursorScreenPos(imgui.Vec2{X: x, Y: cy})
				if imgui.InvisibleButton("##scchip", imgui.Vec2{X: w, Y: 19}) {
					scoreOpenPicker(li, imgui.Vec2{X: x, Y: cy + 21})
				}
				col := scoreColFrame
				if imgui.IsItemHovered() {
					col = scoreColFrameHi
				}
				dl.AddRectFilledV(imgui.Vec2{X: x, Y: cy}, imgui.Vec2{X: x + w, Y: cy + 19}, col, 4, imgui.DrawFlagsRoundCornersAll)
				dl.AddText(imgui.Vec2{X: x + 6, Y: cy + 2}, scoreColChip, scoreClipText(scoreInstrumentLabel(line.Instrument), w-22))
				scoreDrawChevron(dl, imgui.Vec2{X: x + w - 11, Y: cy + 9.5}, scoreColChip)
				editorMenuTooltip(line.Instrument + scoreInstrumentNote(li) + "\nClick to play the line on another instrument (also while playing).")
			}
		}
		// Row 3: M, S, duplicate, volume.
		by := mid + 13
		if compact {
			by = mid + 1
		}
		if inSight(by, by+16) {
			bx := x
			if scoreSmallButton("M##scm", imgui.Vec2{X: bx, Y: by}, line.Mute, scoreColMute) {
				e.toggleMute(li)
			}
			editorMenuTooltip("Mute " + e.lineName(li))
			if scoreSmallButton("S##scs", imgui.Vec2{X: bx + 23, Y: by}, line.Solo, scoreColSolo) {
				e.toggleSolo(li)
			}
			editorMenuTooltip("Solo " + e.lineName(li))
			if scoreDupButton("##scdup", imgui.Vec2{X: bx + 46, Y: by}) {
				scoreDupOpen, scoreMenuLine = true, li
			}
			editorMenuTooltip("Duplicate the line: with its notes, or empty with the same instrument and mixer")
			if scoreSmallButton("D##sclane", imgui.Vec2{X: bx + 72, Y: by}, e.laneShown(li), scoreColLaneOn) {
				e.toggleLane(li)
			}
			editorMenuTooltip("The dynamics lane under " + e.lineName(li) + ": the level its marks give, and\nsegments dragged to another level without changing a printed mark")
			sx := bx + 97
			if v, changed, released := scoreHSlider("##scvol", imgui.Vec2{X: sx, Y: by}, imgui.Vec2{X: x + w - sx, Y: 16},
				line.Volume, 0, scoreFaderMax, 1); changed {
				e.setVolume(li, v)
			} else if released {
				e.endMixGesture()
			}
			editorMenuTooltip(fmt.Sprintf("Volume %s (double-click: 0 dB)", scoreDB(line.Volume)))
		}
		imgui.PopID()
		// A right-click anywhere on the line's header: its commands.
		if inColumn && mouse.Y >= bandTop && mouse.Y < bandBot && imgui.IsMouseClicked(1) {
			scoreLineOpen, scoreMenuLine = true, li
		}
	}
	scoreLaneHeaderLabels(dl, l, g, wp)
}

// scoreLineSelected reports a line the selection is on.
func scoreLineSelected(li int) bool {
	q := scEd.Sel
	return (q.Kind == scoreSelBars && li >= q.LineA && li <= q.LineB) || ((q.Kind == scoreSelEvent || q.Kind == scoreSelCaret) && q.Loc.Line == li)
}

// scoreAnySolo reports a soloed line.
func scoreAnySolo() bool {
	for _, l := range scEd.Score.Lines {
		if l.Solo {
			return true
		}
	}
	return false
}

// scoreInstrumentNote says when a line's instrument is not in the library.
func scoreInstrumentNote(li int) string {
	l := scEd.line(li)
	if l == nil || l.Instrument == "" || tracker.InstrumentPath(l.Instrument) != "" {
		return ""
	}
	return " (not in the library: the line is silent)"
}

// ---------- small widgets ----------

// scoreSmallButton is a 20×16 toggle, lit in onCol when on.
func scoreSmallButton(id string, at imgui.Vec2, on bool, onCol imgui.PackedColor) bool {
	imgui.SetCursorScreenPos(at)
	pressed := imgui.InvisibleButton(id, imgui.Vec2{X: 20, Y: 16})
	col := scoreColFrame
	switch {
	case on:
		col = onCol
	case imgui.IsItemHovered():
		col = scoreColFrameHi
	}
	dl := imgui.WindowDrawList()
	dl.AddRectFilledV(at, imgui.Vec2{X: at.X + 20, Y: at.Y + 16}, col, 3, imgui.DrawFlagsRoundCornersAll)
	label := id
	if k := strings.Index(id, "##"); k >= 0 {
		label = id[:k]
	}
	tw := imgui.CalcTextSize(label, false, 0)
	dl.AddText(imgui.Vec2{X: at.X + (20-tw.X)/2, Y: at.Y + (16-tw.Y)/2}, scoreColChip, label)
	return pressed
}

// scoreDupButton is the duplicate button: two overlapping squares.
func scoreDupButton(id string, at imgui.Vec2) bool {
	imgui.SetCursorScreenPos(at)
	pressed := imgui.InvisibleButton(id, imgui.Vec2{X: 22, Y: 16})
	col := scoreColFrame
	if imgui.IsItemHovered() {
		col = scoreColFrameHi
	}
	dl := imgui.WindowDrawList()
	dl.AddRectFilledV(at, imgui.Vec2{X: at.X + 22, Y: at.Y + 16}, col, 3, imgui.DrawFlagsRoundCornersAll)
	dl.AddRectV(imgui.Vec2{X: at.X + 5, Y: at.Y + 3}, imgui.Vec2{X: at.X + 13, Y: at.Y + 10}, scoreColChip, 1, imgui.DrawFlagsRoundCornersAll, 1.2)
	dl.AddRectFilledV(imgui.Vec2{X: at.X + 9, Y: at.Y + 6}, imgui.Vec2{X: at.X + 17, Y: at.Y + 13}, col, 1, imgui.DrawFlagsRoundCornersAll)
	dl.AddRectV(imgui.Vec2{X: at.X + 9, Y: at.Y + 6}, imgui.Vec2{X: at.X + 17, Y: at.Y + 13}, scoreColChip, 1, imgui.DrawFlagsRoundCornersAll, 1.2)
	return pressed
}

// scoreDrawChevron is a small down arrow centred at c.
func scoreDrawChevron(dl imgui.DrawList, c imgui.Vec2, col imgui.PackedColor) {
	dl.AddTriangleFilled(imgui.Vec2{X: c.X - 4, Y: c.Y - 2}, imgui.Vec2{X: c.X + 4, Y: c.Y - 2}, imgui.Vec2{X: c.X, Y: c.Y + 3}, col)
}

// scoreHSlider is a thin horizontal slider (a track and a knob, as the
// mockup draws the header's volume): drag or click to set, double-click
// for def. It reports the new value when it moved, and released when a
// drag of it ended.
func scoreHSlider(id string, at, size imgui.Vec2, v, lo, hi, def float32) (nv float32, changed, released bool) {
	imgui.SetCursorScreenPos(at)
	imgui.InvisibleButton(id, size)
	active, hov := imgui.IsItemActive(), imgui.IsItemHovered()
	released = imgui.IsItemDeactivated()
	pad := float32(5)
	x0, x1 := at.X+pad, at.X+size.X-pad
	cy := at.Y + size.Y/2
	nv = v
	switch {
	case hov && imgui.IsMouseDoubleClicked(0):
		nv, changed = def, v != def
	case active:
		f := clampF32((imgui.MousePos().X-x0)/max(x1-x0, 1), 0, 1)
		nv = lo + f*(hi-lo)
		changed = nv != v
	}
	dl := imgui.WindowDrawList()
	f := clampF32((nv-lo)/(hi-lo), 0, 1)
	dl.AddRectFilledV(imgui.Vec2{X: x0, Y: cy - 3}, imgui.Vec2{X: x1, Y: cy + 3}, scoreColTrack, 3, imgui.DrawFlagsRoundCornersAll)
	dl.AddRectFilledV(imgui.Vec2{X: x0, Y: cy - 3}, imgui.Vec2{X: x0 + (x1-x0)*f, Y: cy + 3}, scoreColAccent, 3, imgui.DrawFlagsRoundCornersAll)
	// Unity, when it is inside the range.
	if def > lo && def < hi {
		ux := x0 + (x1-x0)*(def-lo)/(hi-lo)
		dl.AddLine(imgui.Vec2{X: ux, Y: cy - 5}, imgui.Vec2{X: ux, Y: cy + 5}, scoreColDim)
	}
	r := float32(5)
	if hov || active {
		r = 6
	}
	dl.AddCircleFilled(imgui.Vec2{X: x0 + (x1-x0)*f, Y: cy}, r, scoreColKnob)
	return nv, changed, released
}

// scoreMeterFloor is the quietest level a meter shows, in dB: a meter is
// scaled in decibels, as a mixer's are, so a line playing piano still moves
// it (on a linear scale -30 dB is 3% of the bar, and most music is there).
const scoreMeterFloor = 48.0

// scoreMeterFrac is how much of a meter a level (linear, 1 = 0 dB) fills:
// scoreMeterFloor below 0 dB is empty, 0 dB is full.
func scoreMeterFrac(v float32) float32 {
	if v <= 0 {
		return 0
	}
	return clampF32(float32((20*math.Log10(float64(v))+scoreMeterFloor)/scoreMeterFloor), 0, 1)
}

// scoreDrawMeterBar fills a vertical meter from the bottom, in decibels
// (scoreMeterFrac): green, then yellow past 70% (about -14 dB), red past
// 88% (about -6 dB), and a red cap past 0 dB.
func scoreDrawMeterBar(dl imgui.DrawList, a, b imgui.Vec2, v float32) {
	h := b.Y - a.Y
	f := scoreMeterFrac(v)
	if f <= 0 {
		return
	}
	bands := []struct {
		to  float32
		col imgui.PackedColor
	}{{0.7, scoreColMeterLo}, {0.88, scoreColMeterMd}, {1, scoreColMeterHi}}
	from := float32(0)
	for _, bd := range bands {
		to := min(f, bd.to)
		if to > from {
			dl.AddRectFilled(imgui.Vec2{X: a.X, Y: b.Y - h*to}, imgui.Vec2{X: b.X, Y: b.Y - h*from}, bd.col)
		}
		from = bd.to
		if f <= bd.to {
			break
		}
	}
	if v > 1 {
		dl.AddRectFilled(imgui.Vec2{X: a.X, Y: a.Y}, imgui.Vec2{X: b.X, Y: a.Y + 3}, scoreColMeterHi)
	}
}

// ---------- popups ----------

// scoreOpenPicker asks for the instrument picker for line li, under at.
func scoreOpenPicker(li int, at imgui.Vec2) {
	scorePickOpen, scorePickLine, scorePickAdd, scorePickPos = true, li, false, at
}

// scoreOpenAddLine asks for the picker to add a line at index at.
func scoreOpenAddLine(at int) {
	scorePickOpen, scorePickAdd, scorePickAt = true, true, at
	scorePickPos = imgui.Vec2{X: 120, Y: editorTopBarH + scoreTransportH + 30}
}

// scoreLinePopups draws the picker, the duplicate menu and the line menu.
func scoreLinePopups() {
	e := scEd
	if scorePickOpen {
		scorePickOpen = false
		scorePickText = ""
		scoreInstNames = tracker.ScanInstrumentNames()
		imgui.SetNextWindowPos(scorePickPos)
		imgui.OpenPopup("##scpick")
	}
	if scoreDupOpen {
		scoreDupOpen = false
		imgui.OpenPopup("##scdupmenu")
	}
	if scoreLineOpen {
		scoreLineOpen = false
		imgui.OpenPopup("##sclinemenu")
	}
	if imgui.BeginPopup("##scpick") {
		title := "Instrument for " + e.lineName(scorePickLine)
		current := ""
		if scorePickAdd {
			title = "Add a line"
		} else if l := e.line(scorePickLine); l != nil {
			current = l.Instrument
		}
		imgui.Text(title)
		imgui.SameLineV(240, 0)
		if imgui.Button("Library...##scpicklib") {
			scoreOpenLibrary(scorePickLine, scorePickAdd)
			if scorePickAdd {
				scoreLibrary.addAt = scorePickAt
			}
			imgui.CloseCurrentPopup()
		}
		editorMenuTooltip("The instrument library: details, preview, SFZ / SF2 import")
		if name, ok := scorePickerBody(current); ok {
			if scorePickAdd {
				e.addLine(scorePickAt, name)
			} else {
				e.setInstrument(scorePickLine, name, scorePlaying())
				if !scorePlaying() {
					// Hear the new sound (the mockup plays a fifth).
					e.auditions = append(e.auditions, scoreAudition{Line: scorePickLine, MIDI: scorePickAudition(scorePickLine)})
				}
			}
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}
	if imgui.BeginPopup("##scdupmenu") {
		if imgui.MenuItem("Duplicate with notes") {
			e.duplicateLine(scoreMenuLine, true)
		}
		editorMenuTooltip("A copy of the line below it: notes, instrument, mixer.")
		if imgui.MenuItem("Duplicate empty") {
			e.duplicateLine(scoreMenuLine, false)
		}
		editorMenuTooltip("A new line below with the same instrument, clefs and mixer settings, and no notes.")
		imgui.EndPopup()
	}
	if imgui.BeginPopup("##sclinemenu") {
		scoreLineMenuItems(scoreMenuLine)
		imgui.EndPopup()
	}
}

// scorePickAudition is a fifth in the middle of a line's staff, to hear an
// instrument by.
func scorePickAudition(li int) []int {
	mid := scEd.staffMiddle(li, 0, score.Pos{Bar: 1})
	if l := scEd.line(li); l != nil && len(l.Staves) > 0 && l.Staves[0].Clef == "percussion" {
		return []int{38, 42} // snare and hi-hat
	}
	m := scEd.toConcert(li, 0, mid).MIDI()
	return []int{m, m + 7}
}

// scorePickerBody is the picker's search and list. It returns the name
// picked (a click, or Enter for the first match).
func scorePickerBody(current string) (string, bool) {
	if imgui.IsWindowAppearing() {
		imgui.SetKeyboardFocusHere()
	}
	imgui.SetNextItemWidth(300)
	enter := imgui.InputTextWithHintV("##scpickq", "Search instruments", &scorePickText, imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil)
	imgui.BeginChildV("##scpicklist", imgui.Vec2{X: 300, Y: 300}, false, 0)
	defer imgui.EndChild()
	if len(scoreInstNames) == 0 {
		editorTextMuted("No instruments under sounds/instruments/.\n`foundry tracker gen-instruments` writes the starter library.")
		return "", false
	}
	cat, shown := "", 0
	for _, n := range scoreInstNames {
		label := scoreInstrumentLabel(n)
		if !imguibackend.ComboFilterMatches(scorePickText, label+" "+n) {
			continue
		}
		shown++
		if enter && shown == 1 {
			return n, true
		}
		if c := scoreInstCategory(n); c != cat {
			cat = c
			editorTextMuted(strings.ToUpper(c))
		}
		if imguibackend.SelectableV(label+"##scpk"+n, n == current, 0, imgui.Vec2{}) {
			return n, true
		}
		file := n[strings.LastIndex(n, "/")+1:] + tracker.InstrumentExt
		imgui.SameLineV(300-imgui.CalcTextSize(file, false, 0).X-14, 0)
		editorTextMuted(file)
	}
	if shown == 0 {
		editorTextMuted("No instrument matches")
	}
	return "", false
}

// scoreInstCategory is a library name's category: "strings".
func scoreInstCategory(name string) string {
	if k := strings.Index(name, "/"); k >= 0 {
		return name[:k]
	}
	return "other"
}

// scoreLineMenuItems are line li's commands: the context menu's and the
// Lines menu's.
func scoreLineMenuItems(li int) {
	e := scEd
	l := e.line(li)
	if l == nil {
		editorTextMuted("No line")
		return
	}
	editorTextMuted(e.lineName(li))
	if imgui.MenuItem("Rename...") {
		scoreRenameLine, scoreRenameText, scoreRenameFocus = li, l.Name, true
	}
	if imgui.MenuItem("Instrument...") {
		scoreOpenPicker(li, imgui.MousePos())
	}
	if imgui.MenuItem("Instrument Library...") {
		scoreOpenLibrary(li, false)
	}
	editorMenuTooltip("Every instrument with its range, layers and articulations; import SFZ and SF2 libraries.")
	if imgui.MenuItem("Select the Line") {
		e.selectLine(li)
	}
	imgui.Separator()
	if imgui.MenuItem("Duplicate with Notes") {
		e.duplicateLine(li, true)
	}
	if imgui.MenuItem("Duplicate Empty") {
		e.duplicateLine(li, false)
	}
	if imgui.MenuItem("Add Line Above...") {
		scoreOpenAddLine(li)
	}
	if imgui.MenuItem("Add Line Below...") {
		scoreOpenAddLine(li + 1)
	}
	imgui.Separator()
	if imgui.MenuItemV("Move Up", "", false, li > 0) {
		e.moveLine(li, -1)
	}
	if imgui.MenuItemV("Move Down", "", false, li < len(e.Score.Lines)-1) {
		e.moveLine(li, 1)
	}
	imgui.Separator()
	if imgui.MenuItemV("Mute", "", l.Mute, true) {
		e.toggleMute(li)
	}
	if imgui.MenuItemV("Solo", "", l.Solo, true) {
		e.toggleSolo(li)
	}
	imgui.Separator()
	if imgui.MenuItemV("Dynamics Lane", scoreKeymapActive().label(scLane), e.laneShown(li), true) {
		e.toggleLane(li)
	}
	editorMenuTooltip("The level the line's marks give (dashed) and what plays (solid);\ndrag across the lane to hold a level, drag a held segment up or down.")
	if imgui.MenuItemV("Clear Dynamics Overrides", "", false, len(l.Overrides) > 0) {
		e.laneClearLine(li)
	}
	if imgui.BeginMenu("Glissandi Play") {
		for _, st := range append([]string{""}, score.GlissStyles...) {
			label := st
			if st == "" {
				label = "As the Instrument Says"
			}
			if imgui.MenuItemV(scoreTitle(label)+"##scgl"+st, "", l.Gliss == st, true) {
				e.setLineGliss(li, st)
			}
		}
		imgui.EndMenu()
	}
	editorMenuTooltip("A slide bends from note to note; a chromatic or diatonic run plays the notes between.")
	imgui.Separator()
	if imgui.MenuItemV("Remove Line", "", false, len(e.Score.Lines) > 1) {
		e.removeLine(li)
	}
}

// scoreGrandStaffLine is a new piano on a grand staff, for the Lines menu.
func scoreGrandStaffLine() *score.Line {
	l := score.NewLine("Piano", "keys/grand_piano", "treble")
	l.Staves = append(l.Staves, &score.Staff{Clef: "bass", Voices: []*score.Voice{{}}})
	return l
}

// scorePercussionLine is a new drum kit on a percussion staff.
func scorePercussionLine() *score.Line {
	return score.NewLine("Drums", "percussion/drum_kit", "percussion")
}
