package editor

// editor_tracker_grid.go — the pattern grid, the overview strip above it and
// the piano roll below it.
//
// The grid is drawn straight into the window's draw list, one AddText per
// field, with a single invisible button over the whole area to take the
// mouse. A 64-row, 8-channel pattern is a few thousand fields; as widgets that
// would be a few thousand IDs, hit tests and layout passes every frame, as
// draw-list text it is nothing. The scrolling is the editor's own (a top row
// and a pixel offset) for the same reason, and because a tracker's scrolling
// is not a document's: the cursor row is kept in view, and while the song
// plays the playing row is.
//
// Field positions are measured in characters of the editor font (ProggyClean,
// which is monospace), so a column of notes lines up whatever the font size.
// trkFieldAt is the inverse — a click's x inside a channel to the field under
// it — and is a pure function so the test can check the two agree.

import (
	"fmt"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

func trkCol(r, g, b, a float32) imgui.PackedColor {
	return imgui.PackedColorFromVec4(imgui.Vec4{X: r, Y: g, Z: b, W: a})
}

// The grid's palette: the AI canvas's dark slate, with each sub-column in its
// own hue so a glance separates notes from numbers.
var (
	trkColBg          = trkCol(0.09, 0.10, 0.12, 1)
	trkColBeat        = trkCol(1, 1, 1, 0.035)
	trkColBar         = trkCol(1, 1, 1, 0.075)
	trkColCursorRow   = trkCol(0.30, 0.45, 0.70, 0.22)
	trkColPlayRow     = trkCol(1, 0.62, 0.16, 0.28)
	trkColCursor      = trkCol(0.45, 0.65, 1, 0.85)
	trkColCursorDim   = trkCol(0.45, 0.65, 1, 0.35)
	trkColSel         = trkCol(1, 0.62, 0.16, 0.20)
	trkColSelEdge     = trkCol(1, 0.62, 0.16, 0.85)
	trkColChanSep     = trkCol(0, 0, 0, 0.6)
	trkColHeader      = trkCol(0.13, 0.15, 0.18, 1)
	trkColHeaderHot   = trkCol(0.17, 0.20, 0.25, 1)
	trkColText        = trkCol(0.93, 0.94, 0.96, 1)
	trkColTextDim     = trkCol(0.62, 0.66, 0.72, 1)
	trkColDot         = trkCol(1, 1, 1, 0.16)
	trkColNote        = trkCol(0.92, 0.95, 1.00, 1)
	trkColNoteOff     = trkCol(0.85, 0.45, 0.42, 1)
	trkColInstr       = trkCol(0.50, 0.85, 0.55, 1)
	trkColVol         = trkCol(0.45, 0.80, 0.95, 1)
	trkColFX          = trkCol(0.98, 0.75, 0.35, 1)
	trkColFXFlow      = trkCol(0.90, 0.55, 0.95, 1)
	trkColRowNum      = trkCol(0.50, 0.54, 0.60, 1)
	trkColRowNumBar   = trkCol(0.85, 0.88, 0.92, 1)
	trkColMute        = trkCol(0.85, 0.30, 0.28, 1)
	trkColSolo        = trkCol(0.95, 0.78, 0.30, 1)
	trkColButtonOff   = trkCol(1, 1, 1, 0.08)
	trkColMeter       = trkCol(0.40, 0.85, 0.50, 1)
	trkColMeterHot    = trkCol(0.98, 0.45, 0.35, 1)
	trkColScrollTrack = trkCol(1, 1, 1, 0.04)
	trkColScrollThumb = trkCol(1, 1, 1, 0.18)
	trkColSplitter    = trkCol(0, 0, 0, 0.35)
	trkColSplitterHot = trkCol(1, 0.62, 0.16, 0.8)
	trkColScopeBg     = trkCol(0, 0, 0, 0.35)
	trkColScope       = trkCol(0.45, 0.85, 0.95, 1)
	trkColRollNote    = trkCol(0.45, 0.65, 1, 0.9)
	trkColRollNoteSel = trkCol(1, 0.62, 0.16, 0.95)
	trkColKeyWhite    = trkCol(0.80, 0.82, 0.86, 1)
	trkColKeyBlack    = trkCol(0.16, 0.17, 0.20, 1)

	trkColActiveButton = imgui.Vec4{X: 0.20, Y: 0.48, Z: 0.30, W: 1}
	trkColRecordButton = imgui.Vec4{X: 0.62, Y: 0.20, Z: 0.20, W: 1}
)

// trkSectionColors tint order entries and brackets by section.
var trkSectionColors = []imgui.Vec4{
	{X: 0.30, Y: 0.55, Z: 0.90, W: 1},
	{X: 0.35, Y: 0.75, Z: 0.45, W: 1},
	{X: 0.90, Y: 0.60, Z: 0.25, W: 1},
	{X: 0.75, Y: 0.40, Z: 0.85, W: 1},
	{X: 0.85, Y: 0.35, Z: 0.40, W: 1},
	{X: 0.30, Y: 0.80, Z: 0.80, W: 1},
}

func trkSectionColor(i int, a float32) imgui.PackedColor {
	c := trkSectionColors[i%len(trkSectionColors)]
	c.W = a
	return imgui.PackedColorFromVec4(c)
}

// ---------- layout ----------

// trkFxChars is an effect column's width in characters: three in hex mode,
// enough for the longest readable short name plus "x/y" otherwise.
func trkFxChars(hex bool) int {
	if hex {
		return 3
	}
	return 12
}

// trkChannelChars is a channel's content width in characters.
func trkChannelChars(fx int, hex bool) int {
	return 9 + fx*(trkFxChars(hex)+1)
}

// trkFieldSpan is where a field sits inside a channel, in characters: its
// start column and width. Effect fields are always the letter and two
// nibbles at the effect column's start — in readable mode the column under
// the cursor is drawn in hex, so what is typed is what is seen.
func trkFieldSpan(f int, hex bool) (start, width int) {
	switch {
	case f <= 0:
		return 0, 3
	case f <= 2:
		return 4 + (f - 1), 1
	case f <= 4:
		return 7 + (f - 3), 1
	}
	k := (f - trkFieldsFixed) / 3
	return 10 + k*(trkFxChars(hex)+1) + (f-trkFieldsFixed)%3, 1
}

// trkFieldAt is the field under a character column inside a channel —
// trkFieldSpan's inverse, with the gaps between fields going to the field on
// their left so every click lands somewhere.
func trkFieldAt(col float32, fx int, hex bool) int {
	n := trkFieldCount(fx)
	best := 0
	for f := 0; f < n; f++ {
		s, _ := trkFieldSpan(f, hex)
		if col >= float32(s) {
			best = f
		}
	}
	// Past the three typed characters of a readable effect column is still
	// that column's last nibble.
	return best
}

// trkGridMetrics are the measurements the grid is drawn and hit-tested with.
type trkGridMetrics struct {
	cw, rowH float32 // character width, row height
	padX     float32 // padding either side of a channel's text
	gutter   float32 // row-number column
	headerH  float32
	chanW    float32
}

func trkMetrics() trkGridMetrics {
	cw := imgui.CalcTextSize("0", false, 0).X
	th := imgui.TextLineHeight()
	m := trkGridMetrics{cw: cw, rowH: th + 3, padX: cw, gutter: cw*4 + 6}
	m.headerH = th*2 + 14
	m.chanW = float32(trkChannelChars(trkEd.Song.FXColumns, trkHexFX))*cw + 2*m.padX
	return m
}

// Grid view state.
var (
	trkScrollRow int     // top visible row
	trkScrollX   float32 // horizontal pixel scroll
	trkDragSel   bool    // a drag-select is in progress
	trkVScroll   bool    // dragging the vertical scrollbar
	trkHScroll   bool
	trkLastVisN  = 32 // rows that fit, last frame
)

// ---------- the pane ----------

// trkGridPane is the centre column: overview, grid, piano roll.
func trkGridPane() {
	w := trackerEditorWindow
	x0 := trkLeftW
	x1 := float32(w.W) - trkRightW
	y0 := trkToolbarH
	y1 := float32(w.H) - trkBottomH
	imgui.SetNextWindowPos(imgui.Vec2{X: x0, Y: y0})
	imgui.SetNextWindowSize(imgui.Vec2{X: x1 - x0, Y: y1 - y0})
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{})
	if imgui.BeginV("##trkgridpane", nil, trkPaneFlags|imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoScrollWithMouse) {
		origin := imgui.WindowPos()
		size := imgui.WindowSize()
		top := origin.Y
		if trkShowMap {
			h := trkOverviewHeight()
			trkDrawOverview(imgui.Vec2{X: origin.X, Y: top}, imgui.Vec2{X: size.X, Y: h})
			top += h + 2
		}
		gridH := origin.Y + size.Y - top
		if trkRollCh >= 0 && trkRollCh < len(trkEd.Song.Channels) {
			rollH := gridH * 0.42
			gridH -= rollH
			trkDrawGrid(imgui.Vec2{X: origin.X, Y: top}, imgui.Vec2{X: size.X, Y: gridH})
			trkDrawPianoRoll(imgui.Vec2{X: origin.X, Y: top + gridH + 2}, imgui.Vec2{X: size.X, Y: rollH - 2})
		} else {
			trkDrawGrid(imgui.Vec2{X: origin.X, Y: top}, imgui.Vec2{X: size.X, Y: gridH})
		}
		trkGridContextMenu()
	}
	imgui.End()
	imgui.PopStyleVar()
}

// trkVisibleRows is how many rows fit under the header.
func trkVisibleRows(m trkGridMetrics, h float32) int {
	n := int((h - m.headerH - 12) / m.rowH)
	if n < 1 {
		n = 1
	}
	return n
}

// trkScrollToRow keeps row in view, with a margin, or centred when centre is
// set (following playback).
func trkScrollToRow(row, visible, rows int, centre bool) {
	if centre {
		trkScrollRow = row - visible/2
	} else {
		margin := 2
		if visible < 8 {
			margin = 0
		}
		if row < trkScrollRow+margin {
			trkScrollRow = row - margin
		}
		if row > trkScrollRow+visible-1-margin {
			trkScrollRow = row - visible + 1 + margin
		}
	}
	trkScrollRow = clampI(trkScrollRow, 0, max(0, rows-visible))
}

// trkFollowPlayback moves the view to the playing pattern while following.
func trkFollowPlayback() {
	order, _, playing := trkPlayPosition()
	if !trkFollow || !playing {
		return
	}
	if order != trkEd.Cur.Order && order >= 0 && order < len(trkEd.Song.Order) {
		trkEd.Cur.Order = order
		trkEd.SelOn = false
		trkEd.clampCursor()
	}
}

// trkCellText is a field group's text and colour for drawing.
func trkNoteColor(n uint8) imgui.PackedColor {
	if n == tracker.NoteOff || n == tracker.NoteCut {
		return trkColNoteOff
	}
	return trkColNote
}

func trkFXColor(cmd byte) imgui.PackedColor {
	if info := tracker.EffectFor(cmd); info != nil && info.Group == "Flow" {
		return trkColFXFlow
	}
	return trkColFX
}

// trkDrawText draws s at p, with '.' runs dimmed — empty fields are dots.
func trkDrawField(dl imgui.DrawList, p imgui.Vec2, s string, col imgui.PackedColor) {
	if strings.Trim(s, ".") == "" {
		dl.AddText(p, trkColDot, s)
		return
	}
	dl.AddText(p, col, s)
}

func trkDrawGrid(origin, size imgui.Vec2) {
	dl := imgui.WindowDrawList()
	m := trkMetrics()
	s := trkEd.Song
	pat := trkEd.pattern()
	if pat == nil {
		return
	}
	end := imgui.Vec2{X: origin.X + size.X, Y: origin.Y + size.Y}
	dl.AddRectFilled(origin, end, trkColBg)

	const sbW = float32(10)
	rowsTop := origin.Y + m.headerH
	rowsH := size.Y - m.headerH - sbW
	visible := trkVisibleRows(m, size.Y)
	trkLastVisN = visible
	contentW := m.gutter + float32(len(s.Channels))*m.chanW
	viewW := size.X - sbW
	trkScrollX = clampF32(trkScrollX, 0, max32(0, contentW-viewW))

	// Keep the cursor's channel in view horizontally.
	if trkKeepCursorInView {
		cx0 := m.gutter + float32(trkEd.Cur.Ch)*m.chanW
		if cx0-m.gutter < trkScrollX {
			trkScrollX = cx0 - m.gutter
		}
		if cx0+m.chanW > trkScrollX+viewW {
			trkScrollX = cx0 + m.chanW - viewW
		}
		trkScrollX = clampF32(trkScrollX, 0, max32(0, contentW-viewW))
	}

	// Vertical: the playing row while following, else the cursor's.
	playOrder, playRow, playing := trkPlayPosition()
	showPlay := playing && playOrder == trkEd.Cur.Order
	if showPlay && trkFollow {
		trkScrollToRow(playRow, visible, pat.Rows(), true)
	} else if trkKeepCursorInView {
		trkScrollToRow(trkEd.Cur.Row, visible, pat.Rows(), false)
	}
	trkKeepCursorInView = false
	trkScrollRow = clampI(trkScrollRow, 0, max(0, pat.Rows()-visible))

	// The mouse.
	imgui.SetCursorScreenPos(origin)
	imgui.InvisibleButtonV("##trkgridbtn", imgui.Vec2{X: viewW, Y: size.Y - sbW},
		imgui.ButtonFlagsMouseButtonLeft|imgui.ButtonFlagsMouseButtonRight)
	hovered := imgui.IsItemHovered()
	mouse := imgui.MousePos()
	trkGridMouse(m, origin, rowsTop, viewW, hovered, mouse, pat)

	// Rows.
	dl.PushClipRect(imgui.Vec2{X: origin.X, Y: rowsTop}, imgui.Vec2{X: origin.X + viewW, Y: rowsTop + rowsH})
	beat := s.LPB
	bar := s.LPB * s.BeatsPerBar
	r0sel, r1sel, c0sel, c1sel := trkEd.selRect()
	firstCh := int(trkScrollX / m.chanW)
	for i := 0; i < visible+1; i++ {
		r := trkScrollRow + i
		if r >= pat.Rows() {
			break
		}
		y := rowsTop + float32(i)*m.rowH
		rowMin := imgui.Vec2{X: origin.X, Y: y}
		rowMax := imgui.Vec2{X: origin.X + viewW, Y: y + m.rowH}
		switch {
		case bar > 0 && r%bar == 0:
			dl.AddRectFilled(rowMin, rowMax, trkColBar)
		case beat > 0 && r%beat == 0:
			dl.AddRectFilled(rowMin, rowMax, trkColBeat)
		}
		if r == trkEd.Cur.Row {
			col := trkColCursorRow
			if !trkGridFocus {
				col = trkColBeat
			}
			dl.AddRectFilled(rowMin, rowMax, col)
		}
		if showPlay && r == playRow {
			dl.AddRectFilled(rowMin, rowMax, trkColPlayRow)
		}
		for c := firstCh; c < len(s.Channels); c++ {
			x := origin.X + m.gutter + float32(c)*m.chanW - trkScrollX
			if x > origin.X+viewW {
				break
			}
			if trkEd.SelOn && r >= r0sel && r <= r1sel && c >= c0sel && c <= c1sel {
				dl.AddRectFilled(imgui.Vec2{X: x, Y: y}, imgui.Vec2{X: x + m.chanW, Y: y + m.rowH}, trkColSel)
			}
			trkDrawCell(dl, m, x+m.padX, y+1, pat.Cells[r][c], r, c)
		}
	}
	// Selection outline.
	if trkEd.SelOn {
		x0 := origin.X + m.gutter + float32(c0sel)*m.chanW - trkScrollX
		x1 := origin.X + m.gutter + float32(c1sel+1)*m.chanW - trkScrollX
		y0 := rowsTop + float32(r0sel-trkScrollRow)*m.rowH
		y1 := rowsTop + float32(r1sel+1-trkScrollRow)*m.rowH
		dl.AddRectV(imgui.Vec2{X: x0, Y: y0}, imgui.Vec2{X: x1, Y: y1}, trkColSelEdge, 0, 0, 1)
	}
	// Channel separators.
	for c := firstCh; c <= len(s.Channels); c++ {
		x := origin.X + m.gutter + float32(c)*m.chanW - trkScrollX
		dl.AddLine(imgui.Vec2{X: x, Y: origin.Y}, imgui.Vec2{X: x, Y: rowsTop + rowsH}, trkColChanSep)
	}
	dl.PopClipRect()

	// Row numbers, drawn over the scrolled cells so they stay put.
	dl.AddRectFilled(imgui.Vec2{X: origin.X, Y: rowsTop}, imgui.Vec2{X: origin.X + m.gutter, Y: rowsTop + rowsH}, trkColHeader)
	for i := 0; i < visible+1; i++ {
		r := trkScrollRow + i
		if r >= pat.Rows() {
			break
		}
		col := trkColRowNum
		if beat > 0 && r%beat == 0 {
			col = trkColRowNumBar
		}
		y := rowsTop + float32(i)*m.rowH + 1
		dl.AddText(imgui.Vec2{X: origin.X + 4, Y: y}, col, fmt.Sprintf("%03d", r))
	}

	trkDrawHeaders(dl, m, origin, viewW)
	trkDrawScrollbars(dl, m, origin, size, sbW, visible, pat.Rows(), contentW, viewW)
	trkEffectHover(m, origin, rowsTop, viewW, hovered, mouse, pat)
}

// trkKeepCursorInView asks the next grid draw to scroll the cursor into view.
// Set by the keyboard: a click already landed on something visible, and
// scrolling under the mouse mid-drag is disorienting.
var trkKeepCursorInView = true

// trkDrawCell draws one cell's fields, and the cursor if it is in this cell.
func trkDrawCell(dl imgui.DrawList, m trkGridMetrics, x, y float32, c tracker.Cell, r, ch int) {
	s := trkEd.Song
	here := r == trkEd.Cur.Row && ch == trkEd.Cur.Ch
	at := func(col int) imgui.Vec2 { return imgui.Vec2{X: x + float32(col)*m.cw, Y: y} }
	if here {
		start, width := trkFieldSpan(trkEd.Cur.Field, trkHexFX)
		col := trkColCursor
		if !trkGridFocus {
			col = trkColCursorDim
		}
		p := at(start)
		dl.AddRectFilled(imgui.Vec2{X: p.X - 1, Y: y - 1}, imgui.Vec2{X: p.X + float32(width)*m.cw + 1, Y: y + m.rowH - 2}, col)
	}
	trkDrawField(dl, at(0), tracker.NoteName(c.Note), trkNoteColor(c.Note))
	trkDrawField(dl, at(4), trkInstrText(c), trkColInstr)
	trkDrawField(dl, at(7), trkVolText(c), trkColVol)
	fxw := trkFxChars(trkHexFX)
	curKind, curFX, _ := trkFieldKind(trkEd.Cur.Field)
	for k := 0; k < s.FXColumns; k++ {
		hex := trkHexFX
		if here && (curKind == trkKindFXCmd || curKind == trkKindFXParam) && curFX == k {
			hex = true
		}
		col := 10 + k*(fxw+1)
		trkDrawField(dl, at(col), trkEffectText(c.FX[k], hex, fxw), trkFXColor(c.FX[k].Cmd))
	}
}

// trkCellAt maps a mouse position over the rows to a row, channel and field.
func trkCellAt(m trkGridMetrics, origin imgui.Vec2, rowsTop float32, mouse imgui.Vec2) (row, ch, field int, ok bool) {
	if mouse.Y < rowsTop || mouse.X < origin.X+m.gutter {
		return 0, 0, 0, false
	}
	row = trkScrollRow + int((mouse.Y-rowsTop)/m.rowH)
	cx := mouse.X - origin.X - m.gutter + trkScrollX
	ch = int(cx / m.chanW)
	inCh := cx - float32(ch)*m.chanW - m.padX
	field = trkFieldAt(inCh/m.cw, trkEd.Song.FXColumns, trkHexFX)
	return row, ch, field, true
}

// trkGridMouse is clicks and drags on the rows: click places the cursor on a
// field, drag selects a block, right-click opens the selection tools.
func trkGridMouse(m trkGridMetrics, origin imgui.Vec2, rowsTop, viewW float32, hovered bool, mouse imgui.Vec2, pat *tracker.Pattern) {
	s := trkEd.Song
	if hovered {
		_, wheel := imgui.CurrentIO().MouseWheel()
		if wheel != 0 {
			if imgui.CurrentIO().KeyShiftPressed() {
				trkScrollX -= wheel * m.chanW * 0.5
			} else {
				trkScrollRow -= int(wheel * 4)
			}
		}
	}
	row, ch, field, ok := trkCellAt(m, origin, rowsTop, mouse)
	inRange := ok && row < pat.Rows() && ch < len(s.Channels)
	if hovered && imgui.IsMouseClicked(0) {
		trkGridFocus = true
		if inRange {
			trkEd.breakUndoRun()
			if imgui.CurrentIO().KeyShiftPressed() {
				if !trkEd.SelOn {
					trkEd.SelOn, trkEd.SelA = true, trkPoint{trkEd.Cur.Row, trkEd.Cur.Ch}
				}
				trkEd.SelB = trkPoint{row, ch}
			} else {
				trkEd.SelOn = false
				trkDragSel = true
			}
			trkEd.Cur.Row, trkEd.Cur.Ch, trkEd.Cur.Field = row, ch, field
		}
	}
	if hovered && imgui.IsMouseClicked(1) && mouse.Y >= rowsTop {
		trkGridFocus = true
		if inRange {
			r0, r1, c0, c1 := trkEd.selRect()
			inside := trkEd.SelOn && row >= r0 && row <= r1 && ch >= c0 && ch <= c1
			if !inside {
				trkEd.SelOn = false
				trkEd.Cur.Row, trkEd.Cur.Ch, trkEd.Cur.Field = row, ch, field
			}
		}
		imgui.OpenPopup("##trkgridctx")
	}
	if trkDragSel {
		if !imgui.IsMouseDown(0) {
			trkDragSel = false
		} else if ok {
			// Dragging past the top or bottom scrolls.
			if mouse.Y < rowsTop+4 {
				trkScrollRow--
			}
			if mouse.Y > rowsTop+float32(trkLastVisN)*m.rowH {
				trkScrollRow++
			}
			row = clampI(row, 0, pat.Rows()-1)
			ch = clampI(ch, 0, len(s.Channels)-1)
			if row != trkEd.Cur.Row || ch != trkEd.Cur.Ch || trkEd.SelOn {
				if !trkEd.SelOn {
					trkEd.SelOn, trkEd.SelA = true, trkPoint{trkEd.Cur.Row, trkEd.Cur.Ch}
				}
				trkEd.SelB = trkPoint{row, ch}
			}
		}
	}
}

// trkEffectHover is the effect column's tooltip: the command's name and what
// it does, so a letter is never a mystery.
func trkEffectHover(m trkGridMetrics, origin imgui.Vec2, rowsTop, viewW float32, hovered bool, mouse imgui.Vec2, pat *tracker.Pattern) {
	if !hovered || trkDragSel {
		return
	}
	row, ch, field, ok := trkCellAt(m, origin, rowsTop, mouse)
	if !ok || row >= pat.Rows() || ch >= len(trkEd.Song.Channels) {
		return
	}
	kind, fx, _ := trkFieldKind(field)
	if kind != trkKindFXCmd && kind != trkKindFXParam {
		return
	}
	e := pat.Cells[row][ch].FX[fx]
	info := tracker.EffectFor(e.Cmd)
	if info == nil {
		return
	}
	imgui.SetTooltip(fmt.Sprintf("%s  (%s)\n%s", info.Name, tracker.FormatEffect(e), info.Desc))
}

// ---------- channel headers ----------

// Channel popups' target, set when one is opened.
var (
	trkPopupCh     int
	trkRenameText  string
	trkOpenRename  bool
	trkOpenMixer   bool
	trkSoloCh      = -1
	trkHeaderHover = -1
)

// trkDrawHeaders is each channel's name, mute and solo, piano-roll and mixer
// buttons and a live level meter. Hit-tested by hand, like the cells.
func trkDrawHeaders(dl imgui.DrawList, m trkGridMetrics, origin imgui.Vec2, viewW float32) {
	s := trkEd.Song
	hdrMax := imgui.Vec2{X: origin.X + viewW, Y: origin.Y + m.headerH}
	dl.AddRectFilled(origin, imgui.Vec2{X: origin.X + viewW + 10, Y: hdrMax.Y}, trkColHeader)
	mouse := imgui.MousePos()
	inHeader := imgui.IsWindowHovered() && mouse.Y >= origin.Y && mouse.Y < hdrMax.Y && mouse.X < hdrMax.X
	clicked := inHeader && imgui.IsMouseClicked(0)
	dbl := inHeader && imgui.IsMouseDoubleClicked(0)
	rclick := inHeader && imgui.IsMouseClicked(1)
	trkHeaderHover = -1
	dl.PushClipRect(imgui.Vec2{X: origin.X + m.gutter, Y: origin.Y}, hdrMax)
	th := imgui.TextLineHeight()
	for c := range s.Channels {
		x := origin.X + m.gutter + float32(c)*m.chanW - trkScrollX
		if x+m.chanW < origin.X || x > hdrMax.X {
			continue
		}
		ch := &s.Channels[c]
		cmin := imgui.Vec2{X: x + 1, Y: origin.Y + 2}
		cmax := imgui.Vec2{X: x + m.chanW - 1, Y: hdrMax.Y - 2}
		hot := inHeader && mouse.X >= cmin.X && mouse.X < cmax.X
		bg := trkColHeader
		if hot {
			bg = trkColHeaderHot
			trkHeaderHover = c
		}
		dl.AddRectFilledV(cmin, cmax, bg, 3, imgui.DrawFlagsRoundCornersAll)
		name := ch.Name
		maxChars := int((m.chanW - 2*m.padX - 4*(th+2)) / m.cw)
		if maxChars > 0 && len(name) > maxChars {
			name = name[:maxChars]
		}
		nameCol := trkColText
		if ch.Mute {
			nameCol = trkColTextDim
		}
		dl.AddText(imgui.Vec2{X: x + m.padX, Y: cmin.Y + 2}, nameCol, name)

		// Buttons, right-aligned on the name line: M, S, roll, mixer.
		bw := th + 2
		bx := cmax.X - 4*(bw+2)
		by := cmin.Y + 1
		btn := func(i int, label string, on bool, onCol imgui.PackedColor) bool {
			bmin := imgui.Vec2{X: bx + float32(i)*(bw+2), Y: by}
			bmax := imgui.Vec2{X: bmin.X + bw, Y: by + bw}
			col := trkColButtonOff
			if on {
				col = onCol
			}
			dl.AddRectFilledV(bmin, bmax, col, 2, imgui.DrawFlagsRoundCornersAll)
			tw := imgui.CalcTextSize(label, false, 0).X
			dl.AddText(imgui.Vec2{X: bmin.X + (bw-tw)/2, Y: by + 1}, trkColText, label)
			over := mouse.X >= bmin.X && mouse.X < bmax.X && mouse.Y >= bmin.Y && mouse.Y < bmax.Y
			return over
		}
		overM := btn(0, "M", ch.Mute, trkColMute)
		overS := btn(1, "S", trkSoloCh == c, trkColSolo)
		overR := btn(2, "R", trkRollCh == c, trkColCursor)
		overX := btn(3, "=", false, 0)
		switch {
		case hot && overM:
			imgui.SetTooltip("Mute")
		case hot && overS:
			imgui.SetTooltip("Solo")
		case hot && overR:
			imgui.SetTooltip("Show this channel as a piano roll")
		case hot && overX:
			imgui.SetTooltip("Volume, pan and sends")
		}

		// Level meter under the name.
		lvl := float32(0)
		if trkPlayer != nil {
			lvl = trkPlayer.ChannelLevel(c)
		}
		my := cmax.Y - 6
		mw := (cmax.X - cmin.X - 2*m.padX)
		dl.AddRectFilled(imgui.Vec2{X: x + m.padX, Y: my}, imgui.Vec2{X: x + m.padX + mw, Y: my + 3}, trkColButtonOff)
		mcol := trkColMeter
		if lvl > 1 {
			mcol = trkColMeterHot
		}
		dl.AddRectFilled(imgui.Vec2{X: x + m.padX, Y: my}, imgui.Vec2{X: x + m.padX + mw*clampF32(lvl, 0, 1), Y: my + 3}, mcol)

		// Volume and pan as text, under the name.
		dl.AddText(imgui.Vec2{X: x + m.padX, Y: cmin.Y + th + 3}, trkColTextDim,
			fmt.Sprintf("vol %3.0f%%  pan %+.2f", ch.Volume*100, ch.Pan))

		if !hot {
			continue
		}
		switch {
		case clicked && overM:
			trkEd.pushUndo("")
			ch.Mute = !ch.Mute
			trkEd.touch()
		case clicked && overS:
			if trkSoloCh == c {
				trkSoloCh = -1
			} else {
				trkSoloCh = c
			}
			if trkPlayer != nil {
				trkPlayer.Solo(trkSoloCh)
			}
		case clicked && overR:
			if trkRollCh == c {
				trkRollCh = -1
			} else {
				trkRollCh = c
			}
		case (clicked && overX) || rclick:
			trkPopupCh, trkOpenMixer = c, true
		case dbl:
			trkPopupCh, trkRenameText, trkOpenRename = c, ch.Name, true
		case clicked:
			trkGridFocus = true
			trkEd.Cur.Ch, trkEd.Cur.Field = c, 0
		}
	}
	dl.PopClipRect()
	dl.AddRectFilled(origin, imgui.Vec2{X: origin.X + m.gutter, Y: hdrMax.Y}, trkColHeader)
	dl.AddText(imgui.Vec2{X: origin.X + 4, Y: origin.Y + 4}, trkColTextDim, fmt.Sprintf("%02X", trkEd.patternIndex()))
}

// trkChannelPopups are the rename and mixer popups the headers open.
func trkChannelPopups() {
	if trkOpenRename {
		imgui.OpenPopup("##trkrename")
		trkOpenRename = false
	}
	if trkOpenMixer {
		imgui.OpenPopup("##trkmixer")
		trkOpenMixer = false
	}
	s := trkEd.Song
	if trkPopupCh < 0 || trkPopupCh >= len(s.Channels) {
		return
	}
	ch := &s.Channels[trkPopupCh]
	if imgui.BeginPopup("##trkrename") {
		if imgui.IsWindowAppearing() {
			imgui.SetKeyboardFocusHere()
		}
		imgui.SetNextItemWidth(200)
		if imgui.InputTextV("##trkrenametext", &trkRenameText, imgui.InputTextFlagsEnterReturnsTrue|imgui.InputTextFlagsAutoSelectAll, nil) {
			if t := strings.TrimSpace(trkRenameText); t != "" && t != ch.Name {
				trkRenameChannel(trkPopupCh, t)
			}
			imgui.CloseCurrentPopup()
		}
		imgui.EndPopup()
	}
	if imgui.BeginPopup("##trkmixer") {
		imgui.Text(ch.Name)
		imgui.Separator()
		imgui.PushItemWidth(180)
		name := ch.Name
		if imgui.InputTextV("Name##trkmixname", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) && strings.TrimSpace(name) != "" {
			trkRenameChannel(trkPopupCh, strings.TrimSpace(name))
		}
		v := ch.Volume
		if trkEdited(imgui.SliderFloatV("Volume##trkmixvol", &v, 0, 1, "%.2f", 0)) {
			ch.Volume = v
		}
		p := ch.Pan
		if trkEdited(imgui.SliderFloatV("Pan##trkmixpan", &p, -1, 1, "%+.2f", 0)) {
			ch.Pan = p
		}
		a := ch.SendA
		if trkEdited(imgui.SliderFloatV("Reverb send##trkmixa", &a, 0, 1, "%.2f", 0)) {
			ch.SendA = a
		}
		b := ch.SendB
		if trkEdited(imgui.SliderFloatV("Delay send##trkmixb", &b, 0, 1, "%.2f", 0)) {
			ch.SendB = b
		}
		drive := ch.Drive
		if trkEdited(imgui.SliderFloatV("Drive##trkmixdrive", &drive, 0, 1, "%.2f", 0)) {
			ch.Drive = drive
		}
		editorMenuTooltip("Saturation on the channel, before the sends.")
		crush := int32(ch.Crush)
		if trkEdited(imgui.SliderIntV("Crush##trkmixcrush", &crush, 0, 16, trkCrushFormat(int(crush)), 0)) {
			ch.Crush = int(crush)
		}
		editorMenuTooltip("Bit-crush the channel to this many bits; 0 is off.")
		mute := ch.Mute
		if trkEdited(imgui.Checkbox("Mute##trkmixmute", &mute)) {
			ch.Mute = mute
		}
		imgui.PopItemWidth()
		imgui.EndPopup()
	}
}

// trkCrushFormat labels the crush slider: "off" at 0, the bit depth otherwise.
func trkCrushFormat(bits int) string {
	if bits <= 0 {
		return "off"
	}
	return "%d bits"
}

// trkRenameChannel renames a channel and the bindings that name it, so a
// rename never silently disconnects a param.
func trkRenameChannel(c int, name string) {
	s := trkEd.Song
	trkEd.pushUndo("")
	old := s.Channels[c].Name
	s.Channels[c].Name = name
	for i := range s.Bindings {
		if s.Bindings[i].Channel == old {
			s.Bindings[i].Channel = name
		}
	}
	trkEd.touch()
}

// ---------- scrollbars ----------

func trkDrawScrollbars(dl imgui.DrawList, m trkGridMetrics, origin, size imgui.Vec2, sbW float32, visible, rows int, contentW, viewW float32) {
	mouse := imgui.MousePos()
	// Vertical.
	tMin := imgui.Vec2{X: origin.X + size.X - sbW, Y: origin.Y + m.headerH}
	tMax := imgui.Vec2{X: origin.X + size.X, Y: origin.Y + size.Y - sbW}
	dl.AddRectFilled(tMin, tMax, trkColScrollTrack)
	trackH := tMax.Y - tMin.Y
	if rows > visible {
		thumbH := max32(20, trackH*float32(visible)/float32(rows))
		thumbY := tMin.Y + (trackH-thumbH)*float32(trkScrollRow)/float32(rows-visible)
		dl.AddRectFilledV(imgui.Vec2{X: tMin.X + 2, Y: thumbY}, imgui.Vec2{X: tMax.X - 2, Y: thumbY + thumbH}, trkColScrollThumb, 3, imgui.DrawFlagsRoundCornersAll)
		over := imgui.IsWindowHovered() && mouse.X >= tMin.X && mouse.X < tMax.X && mouse.Y >= tMin.Y && mouse.Y < tMax.Y
		if over && imgui.IsMouseClicked(0) {
			trkVScroll = true
		}
		if trkVScroll {
			if !imgui.IsMouseDown(0) {
				trkVScroll = false
			} else {
				f := (mouse.Y - tMin.Y - thumbH/2) / (trackH - thumbH)
				trkScrollRow = clampI(int(f*float32(rows-visible)+0.5), 0, rows-visible)
			}
		}
	}
	// Horizontal.
	hMin := imgui.Vec2{X: origin.X, Y: origin.Y + size.Y - sbW}
	hMax := imgui.Vec2{X: origin.X + viewW, Y: origin.Y + size.Y}
	dl.AddRectFilled(hMin, hMax, trkColScrollTrack)
	if contentW > viewW {
		trackW := hMax.X - hMin.X
		thumbW := max32(30, trackW*viewW/contentW)
		thumbX := hMin.X + (trackW-thumbW)*trkScrollX/(contentW-viewW)
		dl.AddRectFilledV(imgui.Vec2{X: thumbX, Y: hMin.Y + 2}, imgui.Vec2{X: thumbX + thumbW, Y: hMax.Y - 2}, trkColScrollThumb, 3, imgui.DrawFlagsRoundCornersAll)
		over := imgui.IsWindowHovered() && mouse.X >= hMin.X && mouse.X < hMax.X && mouse.Y >= hMin.Y && mouse.Y < hMax.Y
		if over && imgui.IsMouseClicked(0) {
			trkHScroll = true
		}
		if trkHScroll {
			if !imgui.IsMouseDown(0) {
				trkHScroll = false
			} else {
				f := (mouse.X - hMin.X - thumbW/2) / (trackW - thumbW)
				trkScrollX = clampF32(f*(contentW-viewW), 0, contentW-viewW)
			}
		}
	}
}

// ---------- selection tools ----------

// Selection-tool settings, remembered between uses.
var (
	trkHumaniseAmount = int32(6)
	trkHumaniseSeed   = int32(1)
	trkFillN          = int32(4)
)

// trkGridContextMenu is the right-click menu on the grid.
func trkGridContextMenu() {
	if imgui.BeginPopup("##trkgridctx") {
		if imgui.MenuItemV("Cut", "Ctrl+X", false, true) {
			trkClipboardCut()
		}
		if imgui.MenuItemV("Copy", "Ctrl+C", false, true) {
			trkClipboardCopy()
		}
		if imgui.MenuItemV("Paste", "Ctrl+V", false, true) {
			trkClipboardPaste(false)
		}
		if imgui.MenuItemV("Mix Paste", "Ctrl+Shift+V", false, true) {
			trkClipboardPaste(true)
		}
		imgui.Separator()
		trkSelectionToolsMenu()
		imgui.EndPopup()
	}
}

// trkSelectionToolsMenu is the selection tools, shared by the context menu and
// Edit.
func trkSelectionToolsMenu() {
	if imgui.BeginMenu("Transpose") {
		for _, t := range []struct {
			label, key string
			semis      int
		}{{"Up a semitone", "Ctrl+Up", 1}, {"Down a semitone", "Ctrl+Down", -1},
			{"Up an octave", "Ctrl+Shift+Up", 12}, {"Down an octave", "Ctrl+Shift+Down", -12}} {
			if imgui.MenuItemV(t.label, t.key, false, true) {
				trkEd.transpose(t.semis)
			}
		}
		imgui.EndMenu()
	}
	if imgui.MenuItemV("Interpolate", "Ctrl+I", false, true) {
		trkEd.interpolate()
	}
	editorMenuTooltip("Ramp volumes, and effect values of the same command,\nfrom the selection's first row to its last.")
	if imgui.BeginMenu("Humanise") {
		imgui.SetNextItemWidth(120)
		imgui.SliderIntV("Amount##trkhum", &trkHumaniseAmount, 1, 32, "%d", 0)
		imgui.SetNextItemWidth(120)
		imgui.InputIntV("Seed##trkhumseed", &trkHumaniseSeed, 1, 10, 0)
		if imgui.Button("Apply##trkhumgo") {
			trkEd.humanise(int(trkHumaniseAmount), uint32(trkHumaniseSeed))
			imgui.CloseCurrentPopup()
		}
		imgui.EndMenu()
	}
	editorMenuTooltip("Jitter note volumes by up to the amount. The same seed\ngives the same result.")
	if imgui.BeginMenu("Fill Every N Rows") {
		imgui.SetNextItemWidth(120)
		imgui.SliderIntV("N##trkfilln", &trkFillN, 1, 32, "%d", 0)
		if imgui.Button("Fill##trkfillgo") {
			trkEd.fillEvery(int(trkFillN))
			imgui.CloseCurrentPopup()
		}
		imgui.EndMenu()
	}
	editorMenuTooltip("Repeat the selection's first row every N rows down it\n(to the end of the pattern with nothing selected).")
	if imgui.BeginMenu("Chord Stamp") {
		for i, c := range trkChords {
			if imgui.MenuItem(c.Name + "##trkchord") {
				trkEd.chordStamp(i)
			}
		}
		imgui.EndMenu()
	}
	editorMenuTooltip("Build a chord on the cursor's note across this channel\nand the ones to its right.")
	if imgui.BeginMenu("Scale Lock") {
		for i, sc := range trkScales {
			if imgui.MenuItemV(sc.Name+"##trkscale", "", trkEd.Scale.Kind == i, true) {
				trkEd.Scale.Kind = i
			}
		}
		imgui.Separator()
		for i := 0; i < 12; i++ {
			if imgui.MenuItemV("Root "+strings.TrimSuffix(tracker.NoteName(uint8(i+1)), "0")+"##trkroot", "", trkEd.Scale.Root == i, true) {
				trkEd.Scale.Root = i
			}
		}
		imgui.EndMenu()
	}
	editorMenuTooltip("Snap typed notes to a key and scale.")
	imgui.Separator()
	if imgui.MenuItemV("Clear", "Del", false, true) {
		trkEd.clearSelected()
	}
}

// ---------- effect picker ----------

var (
	trkOpenPicker bool
	trkPickFilter string
)

// trkEffectPickerPopup lists every effect by group with its description; the
// filter field takes the keyboard, Enter picks the first match.
func trkEffectPickerPopup() {
	if trkOpenPicker {
		imgui.OpenPopup("##trkfxpicker")
		trkOpenPicker = false
		trkPickFilter = ""
	}
	imgui.SetNextWindowSizeV(imgui.Vec2{X: 420, Y: 420}, imgui.ConditionAppearing)
	if !imgui.BeginPopup("##trkfxpicker") {
		return
	}
	if imgui.IsWindowAppearing() {
		imgui.SetKeyboardFocusHere()
	}
	imgui.SetNextItemWidth(-1)
	enter := imgui.InputTextWithHintV("##trkfxfilter", "Type to filter...", &trkPickFilter,
		imgui.InputTextFlagsEnterReturnsTrue, nil)
	imgui.BeginChildV("##trkfxlist", imgui.Vec2{X: 0, Y: 0}, false, 0)
	group := ""
	picked := false
	for _, e := range tracker.Effects {
		label := fmt.Sprintf("%c  %s", e.Cmd, e.Name)
		if !trkPickMatches(trkPickFilter, e) {
			continue
		}
		if e.Group != group {
			group = e.Group
			editorTextMuted(group)
		}
		if (enter && !picked) || imguibackend.Selectable(label+"##trkfx"+string(e.Cmd)) {
			trkEd.setEffect(e.Cmd)
			trkGridFocus = true
			picked = true
			imgui.CloseCurrentPopup()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(e.Desc)
		}
	}
	imgui.EndChild()
	imgui.EndPopup()
}

// trkPickMatches filters the picker on the letter, name, group or
// description.
func trkPickMatches(filter string, e tracker.EffectInfo) bool {
	f := strings.TrimSpace(strings.ToLower(filter))
	if f == "" {
		return true
	}
	if len(f) == 1 && strings.ToLower(string(e.Cmd)) == f {
		return true
	}
	hay := strings.ToLower(string(e.Cmd) + " " + e.Name + " " + e.Group + " " + e.Desc)
	for _, w := range strings.Fields(f) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// ---------- overview ----------

// trkDensity caches each pattern's note density per channel, rebuilt when the
// song changes.
var (
	trkDensity    [][]float32
	trkDensityRev uint64
	trkDensitySet bool
)

func trkOverviewHeight() float32 {
	n := len(trkEd.Song.Channels)
	return clampF32(float32(n)*3+6, 18, 60)
}

func trkRefreshDensity() {
	if trkDensitySet && trkDensityRev == trkEd.Rev {
		return
	}
	trkDensity = trkPatternDensity(trkEd.Song)
	trkDensityRev, trkDensitySet = trkEd.Rev, true
}

// trkPatternDensity is notes per row for each pattern and channel, 0..1.
func trkPatternDensity(s *tracker.Song) [][]float32 {
	out := make([][]float32, len(s.Patterns))
	for i, p := range s.Patterns {
		d := make([]float32, len(s.Channels))
		for _, row := range p.Cells {
			for c := range d {
				if c < len(row) && row[c].Note >= tracker.NoteMin && row[c].Note <= tracker.NoteMax {
					d[c]++
				}
			}
		}
		for c := range d {
			d[c] = clampF32(d[c]/float32(max(1, p.Rows()))*2.5, 0, 1)
		}
		out[i] = d
	}
	return out
}

// trkDrawOverview is the song at a glance: one column per order entry, one
// band per channel, brighter where there are more notes. Click to go there.
func trkDrawOverview(origin, size imgui.Vec2) {
	trkRefreshDensity()
	dl := imgui.WindowDrawList()
	s := trkEd.Song
	end := imgui.Vec2{X: origin.X + size.X, Y: origin.Y + size.Y}
	dl.AddRectFilled(origin, end, trkColHeader)
	n := len(s.Order)
	if n == 0 {
		return
	}
	colW := clampF32((size.X-8)/float32(n), 3, 28)
	bandH := (size.Y - 6) / float32(len(s.Channels))
	playOrder, _, playing := trkPlayPosition()
	for i, pi := range s.Order {
		x := origin.X + 4 + float32(i)*colW
		if x > end.X {
			break
		}
		if sec := trkSectionAt(i); sec >= 0 {
			dl.AddRectFilled(imgui.Vec2{X: x, Y: origin.Y + 1}, imgui.Vec2{X: x + colW - 1, Y: origin.Y + 3}, trkSectionColor(sec, 0.9))
		}
		for c := range s.Channels {
			d := float32(0)
			if pi < len(trkDensity) && c < len(trkDensity[pi]) {
				d = trkDensity[pi][c]
			}
			y := origin.Y + 4 + float32(c)*bandH
			dl.AddRectFilled(imgui.Vec2{X: x, Y: y}, imgui.Vec2{X: x + colW - 1, Y: y + bandH - 1}, trkCol(0.45, 0.65, 1, 0.08+0.8*d))
		}
		if i == trkEd.Cur.Order {
			dl.AddRectV(imgui.Vec2{X: x - 1, Y: origin.Y + 1}, imgui.Vec2{X: x + colW, Y: end.Y - 1}, trkColCursor, 0, 0, 1)
		}
		if playing && i == playOrder {
			dl.AddRectFilled(imgui.Vec2{X: x, Y: end.Y - 3}, imgui.Vec2{X: x + colW - 1, Y: end.Y - 1}, trkColPlayRow)
		}
	}
	imgui.SetCursorScreenPos(origin)
	imgui.InvisibleButton("##trkoverview", size)
	if imgui.IsItemHovered() {
		i := int((imgui.MousePos().X - origin.X - 4) / colW)
		if i >= 0 && i < n {
			name := s.Patterns[s.Order[i]].Name
			imgui.SetTooltip(fmt.Sprintf("Order %02X: pattern %02X %s", i, s.Order[i], name))
			if imgui.IsMouseDown(0) {
				trkGotoOrder(i)
			}
		}
	}
}

// trkGotoOrder moves the cursor to an order entry.
func trkGotoOrder(i int) {
	if i == trkEd.Cur.Order {
		return
	}
	trkEd.breakUndoRun()
	trkEd.Cur.Order = i
	trkEd.SelOn = false
	trkEd.clampCursor()
	trkKeepCursorInView = true
}

// ---------- piano roll ----------

var (
	trkRollTop     = 72 // highest note shown
	trkRollDrag    = -1 // row of the note being dragged, or -1
	trkRollDragRow int
)

// trkDrawPianoRoll shows one channel of the cursor's pattern as bars over a
// vertical keyboard. The grid stays the source of truth: a bar is a note cell
// and its length runs to the next note, note-off or the pattern's end.
func trkDrawPianoRoll(origin, size imgui.Vec2) {
	dl := imgui.WindowDrawList()
	pat := trkEd.pattern()
	ch := trkRollCh
	if pat == nil {
		return
	}
	end := imgui.Vec2{X: origin.X + size.X, Y: origin.Y + size.Y}
	dl.AddRectFilled(origin, end, trkColBg)
	const keyW = float32(36)
	const noteH = float32(9)
	lanes := int(size.Y / noteH)
	trkRollTop = clampI(trkRollTop, lanes, int(tracker.NoteMax))
	rows := pat.Rows()
	rowW := (size.X - keyW - 4) / float32(rows)
	gx := origin.X + keyW

	imgui.SetCursorScreenPos(origin)
	imgui.InvisibleButtonV("##trkroll", size, imgui.ButtonFlagsMouseButtonLeft|imgui.ButtonFlagsMouseButtonRight)
	hovered := imgui.IsItemHovered()
	mouse := imgui.MousePos()
	if hovered {
		trkGridFocus = trkGridFocus || imgui.IsMouseClicked(0)
		if _, wheel := imgui.CurrentIO().MouseWheel(); wheel != 0 {
			trkRollTop += int(wheel * 3)
		}
	}
	noteAtY := func(y float32) int { return trkRollTop - int((y-origin.Y)/noteH) }
	rowAtX := func(x float32) int { return int((x - gx) / rowW) }

	dl.PushClipRect(origin, end)
	// Lanes and keys.
	for i := 0; i <= lanes; i++ {
		n := trkRollTop - i
		if n < int(tracker.NoteMin) {
			break
		}
		y := origin.Y + float32(i)*noteH
		pc := (n - 1) % 12
		black := pc == 1 || pc == 3 || pc == 6 || pc == 8 || pc == 10
		key := trkColKeyWhite
		lane := trkCol(1, 1, 1, 0.025)
		if black {
			key, lane = trkColKeyBlack, trkCol(0, 0, 0, 0.18)
		}
		dl.AddRectFilled(imgui.Vec2{X: gx, Y: y}, imgui.Vec2{X: end.X, Y: y + noteH}, lane)
		dl.AddRectFilled(imgui.Vec2{X: origin.X, Y: y}, imgui.Vec2{X: gx - 2, Y: y + noteH - 1}, key)
		if pc == 0 {
			dl.AddText(imgui.Vec2{X: origin.X + 2, Y: y - 3}, trkColBg, tracker.NoteName(uint8(n)))
			dl.AddLine(imgui.Vec2{X: gx, Y: y + noteH}, imgui.Vec2{X: end.X, Y: y + noteH}, trkCol(1, 1, 1, 0.08))
		}
	}
	// Beat lines.
	for r := 0; r <= rows; r++ {
		if trkEd.Song.LPB > 0 && r%trkEd.Song.LPB == 0 {
			x := gx + float32(r)*rowW
			col := trkCol(1, 1, 1, 0.06)
			if r%(trkEd.Song.LPB*max(1, trkEd.Song.BeatsPerBar)) == 0 {
				col = trkCol(1, 1, 1, 0.16)
			}
			dl.AddLine(imgui.Vec2{X: x, Y: origin.Y}, imgui.Vec2{X: x, Y: end.Y}, col)
		}
	}
	// Notes.
	hotRow := -1
	for r := 0; r < rows; r++ {
		c := pat.Cells[r][ch]
		if c.Note < tracker.NoteMin || c.Note > tracker.NoteMax {
			continue
		}
		length := 1
		for r2 := r + 1; r2 < rows; r2++ {
			if pat.Cells[r2][ch].Note != tracker.NoteNone {
				break
			}
			length++
		}
		y := origin.Y + float32(trkRollTop-int(c.Note))*noteH
		x := gx + float32(r)*rowW
		bmin := imgui.Vec2{X: x + 1, Y: y + 1}
		bmax := imgui.Vec2{X: x + float32(length)*rowW - 1, Y: y + noteH - 1}
		col := trkColRollNote
		if r == trkEd.Cur.Row || r == trkRollDrag {
			col = trkColRollNoteSel
		}
		dl.AddRectFilledV(bmin, bmax, col, 2, imgui.DrawFlagsRoundCornersAll)
		if hovered && mouse.X >= bmin.X && mouse.X < bmax.X && mouse.Y >= bmin.Y && mouse.Y < bmax.Y {
			hotRow = r
		}
	}
	// Playing row.
	if order, prow, playing := trkPlayPosition(); playing && order == trkEd.Cur.Order {
		x := gx + float32(prow)*rowW
		dl.AddLine(imgui.Vec2{X: x, Y: origin.Y}, imgui.Vec2{X: x, Y: end.Y}, trkColSelEdge)
	}
	dl.PopClipRect()

	if !hovered && trkRollDrag < 0 {
		return
	}
	note := noteAtY(mouse.Y)
	row := rowAtX(mouse.X)
	switch {
	case hovered && mouse.X < gx && imgui.IsMouseClicked(0):
		if note >= int(tracker.NoteMin) && note <= int(tracker.NoteMax) {
			trkAudition(-100, trkEd.Instr, uint8(note))
		}
	case hovered && imgui.IsMouseClicked(1) && hotRow >= 0:
		trkEd.pushUndo("")
		pat.Cells[hotRow][ch].Note, pat.Cells[hotRow][ch].Instr = tracker.NoteNone, 0
		trkEd.touch()
	case hovered && imgui.IsMouseClicked(0) && mouse.X >= gx:
		if hotRow >= 0 {
			trkRollDrag, trkRollDragRow = hotRow, hotRow
			trkEd.Cur.Row, trkEd.Cur.Ch, trkEd.Cur.Field = hotRow, ch, 0
			trkEd.pushUndo("")
		} else if row >= 0 && row < rows && note >= int(tracker.NoteMin) && note <= int(tracker.NoteMax) {
			trkEd.pushUndo("")
			c := &pat.Cells[row][ch]
			c.Note = trkEd.snapToScale(uint8(note))
			if trkEd.Instr > 0 {
				c.Instr = uint8(trkEd.Instr)
			}
			trkEd.touch()
			trkEd.Cur.Row, trkEd.Cur.Ch, trkEd.Cur.Field = row, ch, 0
			trkAudition(-100, trkEd.Instr, c.Note)
			trkRollDrag, trkRollDragRow = row, row
		}
	}
	if trkRollDrag >= 0 {
		if !imgui.IsMouseDown(0) {
			trkRollDrag = -1
			trkAuditionOff(-100)
			return
		}
		row = clampI(row, 0, rows-1)
		src := &pat.Cells[trkRollDrag][ch]
		if note >= int(tracker.NoteMin) && note <= int(tracker.NoteMax) && uint8(note) != src.Note {
			src.Note = trkEd.snapToScale(uint8(note))
			trkEd.touch()
		}
		if row != trkRollDrag && pat.Cells[row][ch].IsEmpty() {
			pat.Cells[row][ch] = *src
			*src = tracker.EmptyCell
			trkRollDrag = row
			trkEd.Cur.Row = row
			trkEd.touch()
		}
	}
	if hotRow >= 0 && trkRollDrag < 0 {
		imgui.SetTooltip(tracker.FormatCell(pat.Cells[hotRow][ch], trkEd.Song.FXColumns))
	}
}
