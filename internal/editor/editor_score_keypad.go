package editor

// editor_score_keypad.go — the on-screen keypad: a floating panel laid out
// like a numeric keypad in Sibelius's arrangement (mockup screen 7), so a
// laptop without one can click it, and so anyone can see at a glance what the
// selected note's value and accidental are — those cells are lit.
//
// Each cell runs the same command its key does (scoreRunCmd), so the panel
// cannot drift from the keyboard: the duration cells are keypad slots, whose
// meaning is the keypad layout (the "More" tab is F8's second layout: 64th,
// 128th, 256th and breve on 1–4).

import (
	"fmt"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

const (
	scoreKpCellW = float32(62)
	scoreKpCellH = float32(42)
	scoreKpGap   = float32(4)
)

// scoreKpCell is one key of the panel: its place on the 4×5 grid, its span,
// the key printed in its corner, and what it shows and does.
type scoreKpCell struct {
	col, row, w, h int
	key            string
	glyph, text    string
	cmd            scoreCmd
	lit            bool
}

// scoreKeypadCells are the panel's keys for the current layout and selection.
func scoreKeypadCells() []scoreKpCell {
	e := scEd
	cur, isRest, tied := e.shownDur()
	acc := scoreCurrentAcc()
	durCell := func(slot, col, row int) scoreKpCell {
		c := scoreKpCell{col: col, row: row, w: 1, h: 1, key: fmt.Sprint(slot), cmd: scKeypad1 + scoreCmd(slot-1)}
		if b, ok := e.keypadDur(slot); ok {
			c.glyph = scoreDurIcon(b)
			c.lit = cur.Base == b
		}
		return c
	}
	layout := "Layout 1"
	if e.Layout2 {
		layout = "Layout 2"
	}
	return []scoreKpCell{
		{col: 0, row: 0, w: 1, h: 1, key: "Num", text: "-"},
		{col: 1, row: 0, w: 1, h: 1, key: "/", text: layout, cmd: scLayout, lit: e.Layout2},
		{col: 2, row: 0, w: 1, h: 1, key: "*", text: fmt.Sprintf("Voice %d", e.Voice+1), cmd: scVoiceCycle},
		{col: 3, row: 0, w: 1, h: 1, key: "-", text: "Flip", cmd: scFlip},
		{col: 0, row: 1, w: 1, h: 1, key: "7", glyph: "accidentalNatural", cmd: scNatural, lit: acc == 2},
		{col: 1, row: 1, w: 1, h: 1, key: "8", glyph: "accidentalSharp", cmd: scSharp, lit: acc == 0},
		{col: 2, row: 1, w: 1, h: 1, key: "9", glyph: "accidentalFlat", cmd: scFlat, lit: acc == 1},
		{col: 3, row: 1, w: 1, h: 2, key: "+", text: "-"},
		durCell(4, 0, 2), durCell(5, 1, 2), durCell(6, 2, 2),
		durCell(1, 0, 3), durCell(2, 1, 3), durCell(3, 2, 3),
		{col: 3, row: 3, w: 1, h: 2, key: "Enter", text: "Tie", cmd: scTie, lit: tied},
		{col: 0, row: 4, w: 2, h: 1, key: "0", glyph: "restQuarter", cmd: scRest, lit: isRest},
		{col: 2, row: 4, w: 1, h: 1, key: ".", glyph: "augmentationDot", cmd: scDot, lit: cur.Dots > 0},
	}
}

// scoreKeypadPanel draws the panel as a floating window over the score.
func scoreKeypadPanel(winW, winH float32) {
	e := scEd
	gridW := 4*scoreKpCellW + 3*scoreKpGap
	gridH := 5*scoreKpCellH + 4*scoreKpGap
	imgui.SetNextWindowPosV(imgui.Vec2{X: winW - gridW - 40, Y: winH - gridH - scoreStatusH - 80}, imgui.ConditionFirstUseEver, imgui.Vec2{})
	imgui.SetNextWindowBgAlpha(1)
	open := true
	flags := imgui.WindowFlagsAlwaysAutoResize | imgui.WindowFlagsNoSavedSettings | imgui.WindowFlagsNoCollapse | imgui.WindowFlagsNoScrollbar
	if !imgui.BeginV("Keypad##scorekp", &open, flags) {
		imgui.End()
		return
	}
	// The two layouts as tabs.
	for i, name := range []string{"Notes", "More"} {
		if i > 0 {
			imgui.SameLine()
		}
		if editorActiveToggleButtonColor(name+"##sckptab", e.Layout2 == (i == 1), scoreColActive) && e.Layout2 != (i == 1) {
			scoreRunCmd(scLayout)
		}
	}
	editorMenuTooltip("More is the second layout (F8): 64th, 128th, 256th and breve on 1-4.")
	origin := imgui.CursorPos()
	dl := imgui.WindowDrawList()
	style := imgui.CurrentStyle()
	for i, c := range scoreKeypadCells() {
		local := imgui.Vec2{X: origin.X + float32(c.col)*(scoreKpCellW+scoreKpGap), Y: origin.Y + float32(c.row)*(scoreKpCellH+scoreKpGap)}
		size := imgui.Vec2{X: float32(c.w)*scoreKpCellW + float32(c.w-1)*scoreKpGap, Y: float32(c.h)*scoreKpCellH + float32(c.h-1)*scoreKpGap}
		imgui.SetCursorPos(local)
		pressed := imgui.InvisibleButton(fmt.Sprintf("##sckp%d", i), size)
		a, b := imgui.ItemRectMin(), imgui.ItemRectMax()
		col := style.Color(imgui.StyleColorFrameBg)
		switch {
		case c.lit:
			col = imgui.Vec4{X: 0.26, Y: 0.59, Z: 0.98, W: 0.40}
		case c.cmd != scNone && imgui.IsItemActive():
			col = style.Color(imgui.StyleColorButtonActive)
		case c.cmd != scNone && imgui.IsItemHovered():
			col = style.Color(imgui.StyleColorFrameBgHovered)
		}
		dl.AddRectFilledV(a, b, imgui.PackedColorFromVec4(col), 4, imgui.DrawFlagsRoundCornersAll)
		if c.lit {
			dl.AddRectV(a, b, scoreRGB(0x42, 0x96, 0xfa, 1), 4, imgui.DrawFlagsRoundCornersAll, 1)
		}
		dl.AddText(imgui.Vec2{X: a.X + 4, Y: a.Y + 2}, scoreColDim, c.key)
		inner := imgui.Vec2{X: a.X + 4, Y: a.Y + 12}
		switch {
		case c.glyph != "":
			scoreDrawGlyph(dl, c.glyph, inner, imgui.Vec2{X: b.X - 4, Y: b.Y - 2}, scoreColText)
		case c.text != "":
			ts := imgui.CalcTextSize(c.text, false, 0)
			dl.AddText(imgui.Vec2{X: (a.X + b.X - ts.X) / 2, Y: (a.Y+b.Y-ts.Y)/2 + 5}, scoreColText, c.text)
		}
		if pressed && c.cmd != scNone {
			scoreRunCmd(c.cmd)
		}
		if c.cmd != scNone && imgui.IsItemHovered() {
			imgui.SetTooltip(scoreCmdInfos[c.cmd].Label + scoreKeysSuffix(c.cmd))
		}
	}
	imgui.SetCursorPos(imgui.Vec2{X: origin.X, Y: origin.Y + gridH + scoreKpGap})
	imgui.Dummy(imgui.Vec2{X: gridW, Y: 1})
	imgui.End()
	if !open {
		scoreShowKeypad = false
		scoreSetKeypadWanted(false)
	}
}
