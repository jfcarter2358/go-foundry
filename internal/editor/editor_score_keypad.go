package editor

// editor_score_keypad.go — the on-screen keypad: a floating panel laid out
// like a numeric keypad in Sibelius's arrangement (mockup screen 7), so a
// laptop without one can click it, and so anyone can see at a glance what the
// selected note's value and accidental are — those cells are lit.
//
// Each cell runs the same command its key does (scoreRunCmd), so the panel
// cannot drift from the keyboard: the duration cells are keypad slots, whose
// meaning is the keypad layout. The tabs are the mockup's four: Notes (the
// common values and accidentals), More (F8's second layout: 64th, 128th,
// 256th and breve on 1–4, the grace notes on 5 and 6, the common
// articulations on 7, 8, 9 and +), Artic. (every articulation, the
// fermata, open/stopped and the arpeggio) and Jazz (scoop, plop, bend,
// doit and fall, short and long); "/" steps through them.

import (
	"fmt"

	"foundry/internal/score"

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
	ev := e.event()
	has := func(name, value string) bool {
		if ev == nil {
			return false
		}
		for _, m := range ev.Marks {
			if m.Name == name && m.Value == value {
				return true
			}
		}
		return false
	}
	// A layout's mark on a slot (the articulation and jazz layouts' 1-9
	// and +, the second layout's 7, 8, 9 and +), lit when the selected
	// note has it.
	markOf := func(c scoreKpCell, slot int) (scoreKpCell, bool) {
		m, ok := scoreKeypadMark(e.Keypad, slot)
		if !ok {
			return c, false
		}
		c.glyph, c.lit = m.Glyph, has(m.Name, m.Value)
		return c, true
	}
	durCell := func(slot, col, row int) scoreKpCell {
		c := scoreKpCell{col: col, row: row, w: 1, h: 1, key: fmt.Sprint(slot), cmd: scKeypad1 + scoreCmd(slot-1)}
		if mc, ok := markOf(c, slot); ok {
			return mc
		}
		// The second layout's 5 and 6 are the grace notes.
		if e.Keypad == scoreKpMore && (slot == 5 || slot == 6) {
			k := []score.Grace{score.Acciaccatura, score.Appoggiatura}[slot-5]
			c.glyph = []string{"graceNoteAcciaccaturaStemUp", "graceNoteAppoggiaturaStemUp"}[slot-5]
			c.lit = e.Grace == k
			return c
		}
		if b, ok := e.keypadDur(slot); ok {
			c.glyph = scoreDurIcon(b)
			c.lit = cur.Base == b
		}
		return c
	}
	// markCell is one of keypad 7, 8, 9 and +: an accidental or the accent
	// on the first layout, the layout's mark on the others.
	markCell := func(slot, col, row, h int) scoreKpCell {
		c := scoreKpCell{col: col, row: row, w: 1, h: h, key: fmt.Sprint(slot), cmd: scKeypad7 + scoreCmd(slot-7)}
		if slot == 10 {
			c.key = "+"
		}
		if mc, ok := markOf(c, slot); ok {
			return mc
		}
		switch slot {
		case 7:
			c.glyph, c.lit = "accidentalNatural", acc == 2
		case 8:
			c.glyph, c.lit = "accidentalSharp", acc == 0
		case 9:
			c.glyph, c.lit = "accidentalFlat", acc == 1
		default:
			c.glyph, c.lit = "articAccentAbove", has("acc", "")
		}
		return c
	}
	return []scoreKpCell{
		{col: 0, row: 0, w: 1, h: 1, key: "Num", text: "-"},
		{col: 1, row: 0, w: 1, h: 1, key: "/", text: scoreKpNames[e.Keypad], cmd: scLayoutNext, lit: e.Keypad != scoreKpNotes},
		{col: 2, row: 0, w: 1, h: 1, key: "*", text: fmt.Sprintf("Voice %d", e.Voice+1), cmd: scVoiceCycle},
		{col: 3, row: 0, w: 1, h: 1, key: "-", text: "Flip", cmd: scFlip},
		markCell(7, 0, 1, 1), markCell(8, 1, 1, 1), markCell(9, 2, 1, 1), markCell(10, 3, 1, 2),
		durCell(4, 0, 2), durCell(5, 1, 2), durCell(6, 2, 2),
		durCell(1, 0, 3), durCell(2, 1, 3), durCell(3, 2, 3),
		{col: 3, row: 3, w: 1, h: 2, key: "Enter", text: "Tie", cmd: scTie, lit: tied},
		{col: 0, row: 4, w: 2, h: 1, key: "0", glyph: "restQuarter", cmd: scRest, lit: isRest},
		{col: 2, row: 4, w: 1, h: 1, key: ".", glyph: "augmentationDot", cmd: scDot, lit: cur.Dots > 0},
	}
}

// scoreKeypadTip is what a cell's tooltip says: a layout mark's name, else
// its command's, with the keys.
func scoreKeypadTip(c scoreKpCell) string {
	slot := 0
	switch {
	case c.cmd >= scKeypad1 && c.cmd < scKeypad1+6:
		slot = int(c.cmd-scKeypad1) + 1
	case c.cmd >= scKeypad7 && c.cmd < scKeypad7+4:
		slot = int(c.cmd-scKeypad7) + 7
	}
	if m, ok := scoreKeypadMark(scEd.Keypad, slot); ok {
		return m.Label + scoreKeysSuffix(c.cmd)
	}
	return scoreCmdInfos[c.cmd].Label + scoreKeysSuffix(c.cmd)
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
	// The layouts as tabs ("/" steps through them).
	tips := [scoreKpLayouts]string{
		"The common notes (F7): 32nd to whole on 1-6, accidentals on 7-9, the accent on +.",
		"The second layout (F8): 64th, 128th, 256th and breve on 1-4, grace notes on 5 and 6,\nstaccato, tenuto, staccatissimo and marcato on 7, 8, 9 and +.",
		"Articulations (F10): staccato, tenuto, staccatissimo, accent on 7, 8, 9, +;\nmarcato, portato, fermata on 4-6; open, stopped, arpeggio on 1-3.",
		"Jazz marks (F12): scoop, plop, bend on 7-9, fall on +;\ndoit, long fall, long doit on 4-6.",
	}
	for i, name := range scoreKpNames {
		if i > 0 {
			imgui.SameLine()
		}
		if editorActiveToggleButtonColor(name+"##sckptab", e.Keypad == i, scoreColActive) && e.Keypad != i {
			e.setKeypad(i)
		}
		editorMenuTooltip(tips[i])
	}
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
			// A tall cell's glyph is a one-cell glyph, centred.
			top, bot := inner.Y, b.Y-2
			if c.h > 1 {
				mid := (a.Y + b.Y) / 2
				top, bot = mid-scoreKpCellH/2+12, mid+scoreKpCellH/2-2
			}
			scoreDrawGlyph(dl, c.glyph, imgui.Vec2{X: inner.X, Y: top}, imgui.Vec2{X: b.X - 4, Y: bot}, scoreColText)
		case c.text != "":
			ts := imgui.CalcTextSize(c.text, false, 0)
			dl.AddText(imgui.Vec2{X: (a.X + b.X - ts.X) / 2, Y: (a.Y+b.Y-ts.Y)/2 + 5}, scoreColText, c.text)
		}
		if pressed && c.cmd != scNone {
			scoreRunCmd(c.cmd)
		}
		if c.cmd != scNone && imgui.IsItemHovered() {
			imgui.SetTooltip(scoreKeypadTip(c))
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
