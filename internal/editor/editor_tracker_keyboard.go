package editor

// editor_tracker_keyboard.go — the tracker's keyboard display: a pop-out
// window with the QWERTY piano drawn as a piano, each key labelled with the
// note it plays and the computer key that plays it, lit while it is held —
// and above it a key to the hex codes: what each field of a cell means and
// what every effect command does.
//
// It is its own OS window, so it can sit on another monitor or under the grid,
// and its key events go to the tracker's own handler: typing a note with the
// keyboard window focused is the same as typing it in the grid. Clicking a key
// auditions it, holding as long as the mouse is down — it does not write the
// note, because a click has no cursor to write it at.
//
// It draws from the same map the input reads (trkPianoKeys) and the same held
// set (trkHeldKeys), so what it shows cannot drift from what the keys do.

import (
	"log"
	"strings"

	"foundry/internal/tracker"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	trkKbWindowW = 820
	trkKbWindowH = 640
	// trkKbSemis is how far the QWERTY piano reaches: the top row runs to
	// P, 28 semitones over the lower octave's C.
	trkKbSemis = 29
	// trkKbMouseKey is the audition key a click plays on, offset past every
	// scancode so a held mouse note and a held computer key never collide.
	trkKbMouseKey = 1 << 20
)

// trackerKeyboardWindow is the pop-out; nil when it is closed.
var trackerKeyboardWindow *uiwin.UIWindow

// trkKbMouseSemi is the key the mouse is holding down, or -1.
var trkKbMouseSemi = -1

var (
	trkColKbWhite     = trkCol(0.90, 0.91, 0.93, 1)
	trkColKbBlack     = trkCol(0.14, 0.15, 0.18, 1)
	trkColKbEdge      = trkCol(0, 0, 0, 0.85)
	trkColKbHeld      = trkCol(1, 0.62, 0.16, 1)
	trkColKbOctaveC   = trkCol(0.30, 0.45, 0.70, 0.35)
	trkColKbLabelDark = trkCol(0.20, 0.22, 0.26, 1)
	trkColKbLabelKey  = trkCol(0.30, 0.45, 0.70, 1)
)

// TrackerKeyboardOpen reports whether the keyboard window is up.
func TrackerKeyboardOpen() bool { return trackerKeyboardWindow != nil }

// ToggleTrackerKeyboardWindow opens or closes the keyboard window. Deferred
// through RequestWindowOpen when opening, because the caller is the tracker
// window's own Draw.
func ToggleTrackerKeyboardWindow() {
	if trackerKeyboardWindow != nil {
		trackerKeyboardWindow.CloseRequested = true
		return
	}
	uiwin.RequestWindowOpen(openTrackerKeyboardWindow)
}

func openTrackerKeyboardWindow() {
	if trackerKeyboardWindow != nil {
		trackerKeyboardWindow.Win.Raise()
		return
	}
	w, err := uiwin.NewUIWindow("Tracker Keyboard", trkKbWindowW, trkKbWindowH, drawTrackerKeyboardWindow, onTrackerKeyboardClose)
	if err != nil {
		log.Printf("open tracker keyboard window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.09, 0.10, 0.12, 1}
	// Typing with this window focused plays and enters notes just as it would
	// in the grid.
	w.HandleEvent = trackerEditorHandleEvent
	trackerKeyboardWindow = w
}

func onTrackerKeyboardClose() {
	trkKbRelease()
	trackerKeyboardWindow = nil
}

// trkKbRelease lets go of a note the mouse was holding.
func trkKbRelease() {
	if trkKbMouseSemi >= 0 {
		trkAuditionOff(trkKbMouseKey)
		trkKbMouseSemi = -1
	}
}

// trkKbIsBlack reports whether a semitone above C is a black key.
func trkKbIsBlack(semi int) bool {
	switch semi % 12 {
	case 1, 3, 6, 8, 10:
		return true
	}
	return false
}

// trkKbKeyLabels lists, per semitone, the computer keys that play it ("Z",
// "Q ,"), in the order the piano rows read.
func trkKbKeyLabels() [trkKbSemis]string {
	var out [trkKbSemis]string
	for sc, semi := range trkPianoKeys {
		if semi < 0 || semi >= trkKbSemis {
			continue
		}
		name := sdl.GetScancodeName(sc)
		if out[semi] == "" {
			out[semi] = name
		} else if !strings.Contains(out[semi], name) {
			// Bottom row first: it is the one the octave label refers to.
			if _, bottom := trkKbBottomRow[sc]; bottom {
				out[semi] = name + " " + out[semi]
			} else {
				out[semi] += " " + name
			}
		}
	}
	return out
}

// trkKbBottomRow is the lower piano row, for ordering a shared key's labels.
var trkKbBottomRow = map[sdl.Scancode]struct{}{
	sdl.SCANCODE_Z: {}, sdl.SCANCODE_S: {}, sdl.SCANCODE_X: {}, sdl.SCANCODE_D: {}, sdl.SCANCODE_C: {},
	sdl.SCANCODE_V: {}, sdl.SCANCODE_G: {}, sdl.SCANCODE_B: {}, sdl.SCANCODE_H: {}, sdl.SCANCODE_N: {},
	sdl.SCANCODE_J: {}, sdl.SCANCODE_M: {}, sdl.SCANCODE_COMMA: {}, sdl.SCANCODE_L: {},
	sdl.SCANCODE_PERIOD: {}, sdl.SCANCODE_SEMICOLON: {}, sdl.SCANCODE_SLASH: {},
}

// trkKbHeld is which semitones the computer keys are holding right now.
func trkKbHeld() [trkKbSemis]bool {
	var held [trkKbSemis]bool
	for sc := range trkHeldKeys {
		if semi, ok := trkPianoKeys[sc]; ok && semi >= 0 && semi < trkKbSemis {
			held[semi] = true
		}
	}
	if trkKbMouseSemi >= 0 && trkKbMouseSemi < trkKbSemis {
		held[trkKbMouseSemi] = true
	}
	return held
}

func drawTrackerKeyboardWindow() {
	w := trackerKeyboardWindow
	if w == nil {
		return
	}
	// The keyboard means nothing without the tracker it plays.
	if trackerEditorWindow == nil {
		w.CloseRequested = true
		return
	}
	trkEnsureDoc()
	e := trkEd

	imgui.SetNextWindowPos(imgui.Vec2{})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{X: 10, Y: 8})
	imgui.BeginV("##trkkeyboard", nil, trkPaneFlags)
	imgui.PopStyleVar()

	// The header: octave, instrument, what the keys are doing.
	if trkSmallButton("-##trkkboct") {
		e.Octave = clampI(e.Octave-1, 0, 8)
	}
	editorMenuTooltip("Octave down ([ in the grid).")
	imgui.SameLine()
	imgui.Text("Octave " + itoa(e.Octave))
	imgui.SameLine()
	if trkSmallButton("+##trkkboct") {
		e.Octave = clampI(e.Octave+1, 0, 8)
	}
	editorMenuTooltip("Octave up (] in the grid).")
	imgui.SameLine()
	editorTextMuted("|")
	imgui.SameLine()
	instr := "no instrument"
	if e.Instr > 0 && e.Instr <= len(e.Song.Instruments) {
		instr = itoa(e.Instr) + " " + e.Song.Instruments[e.Instr-1].Name
	}
	imgui.Text("Instrument " + instr)
	imgui.SameLine()
	editorTextMuted("|")
	imgui.SameLine()
	if e.EditMode {
		imgui.Text("Edit mode: typed notes are written")
	} else {
		editorTextMuted("Edit mode off: typed notes only sound")
	}

	held := trkKbHeld()
	var names []string
	for semi, on := range held {
		if on {
			if n := e.Octave*12 + semi + 1; n >= int(tracker.NoteMin) && n <= int(tracker.NoteMax) {
				names = append(names, tracker.NoteName(uint8(n)))
			}
		}
	}
	if len(names) > 0 {
		imgui.Text("Playing  " + strings.Join(names, "  "))
	} else {
		editorTextMuted("Playing  -")
	}

	// The hex-code key takes what the piano leaves; the piano keeps a playable
	// height however small the window gets.
	kbH := clampF32(imgui.ContentRegionAvail().Y*0.4, 140, 220)
	if imgui.CollapsingHeaderV("Hex code key##trkkbkey", imgui.TreeNodeFlagsDefaultOpen) {
		keyH := imgui.ContentRegionAvail().Y - kbH - imgui.CurrentStyle().ItemSpacing().Y
		if keyH > 40 {
			imgui.BeginChildV("##trkkbkeychild", imgui.Vec2{X: 0, Y: keyH}, false, 0)
			trkKbHexKey()
			imgui.EndChild()
		}
	}

	origin := imgui.CursorScreenPos()
	avail := imgui.ContentRegionAvail()
	trkKbDraw(origin, avail, held)
	imgui.End()
}

// The key's colours match the grid's sub-columns (trkColNote and the rest),
// as Vec4s for the text style.
var (
	trkKbNoteV4  = imgui.Vec4{X: 0.92, Y: 0.95, Z: 1.00, W: 1}
	trkKbInstrV4 = imgui.Vec4{X: 0.50, Y: 0.85, Z: 0.55, W: 1}
	trkKbVolV4   = imgui.Vec4{X: 0.45, Y: 0.80, Z: 0.95, W: 1}
	trkKbFXV4    = imgui.Vec4{X: 0.98, Y: 0.75, Z: 0.35, W: 1}
	trkKbFlowV4  = imgui.Vec4{X: 0.90, Y: 0.55, Z: 0.95, W: 1}
	trkKbDimV4   = imgui.Vec4{X: 0.62, Y: 0.66, Z: 0.72, W: 1}
	trkKbRowHot  = imgui.Vec4{X: 1, Y: 0.62, Z: 0.16, W: 0.22}
)

func trkKbColored(col imgui.Vec4, s string) {
	imgui.PushStyleColor(imgui.StyleColorText, col)
	imgui.Text(s)
	imgui.PopStyleColor()
}

// trkKbCode is how an effect's code reads in the key: its letter and what its
// two hex digits are, xx (one value) or xy (two).
func trkKbCode(fx tracker.EffectInfo) string {
	if fx.Split {
		return string(fx.Cmd) + "xy"
	}
	return string(fx.Cmd) + "xx"
}

// trkKbCursorEffect is the effect command under the grid's cursor, or 0.
func trkKbCursorEffect() byte {
	kind, k, _ := trkFieldKind(trkEd.Cur.Field)
	if kind != trkKindFXCmd && kind != trkKindFXParam {
		return 0
	}
	c := trkEd.cell()
	if c == nil || k < 0 || k >= len(c.FX) {
		return 0
	}
	return c.FX[k].Cmd
}

// trkKbHexKey is the legend: what each field of a cell means, then every
// effect, grouped as the picker groups them. Read from tracker.Effects, so a
// new command shows up here with nothing else to change. The command under
// the grid's cursor is highlighted.
func trkKbHexKey() {
	// A cell, field by field, in the grid's colours.
	trkKbColored(trkKbNoteV4, "C-4")
	imgui.SameLine()
	trkKbColored(trkKbInstrV4, "01")
	imgui.SameLine()
	trkKbColored(trkKbVolV4, "40")
	imgui.SameLine()
	trkKbColored(trkKbFXV4, "V44")
	imgui.SameLine()
	editorTextMuted("  a cell: note, instrument, volume, effects")

	fields := []struct {
		col        imgui.Vec4
		code, text string
	}{
		{trkKbNoteV4, "C-4  C#4", "Note: name, sharp, octave.  === releases the note, ^^^ cuts it dead, ... is empty."},
		{trkKbInstrV4, "01..FF", "Instrument number, in hex.  .. keeps the channel's last instrument."},
		{trkKbVolV4, "00..40", "Volume, in hex: 40 is full, 20 half, 00 silent.  .. is full for a new note, unchanged otherwise."},
		{trkKbFXV4, "#xx  #xy", "Effect: a letter and two hex digits.  xx is one value 00..FF; xy is two digits, x and y, 0..F each.  A 0 on a slide, vibrato or tremolo repeats the channel's last value."},
	}
	flags := imgui.TableFlagsRowBg | imgui.TableFlagsSizingStretchProp
	if imgui.BeginTableV("##trkkbfields", 2, flags, imgui.Vec2{}, 0) {
		imgui.TableSetupColumnV("code", imgui.TableColumnFlagsWidthFixed, 80, 0)
		imgui.TableSetupColumnV("text", 0, 0, 0)
		for _, f := range fields {
			imgui.TableNextRow()
			imgui.TableNextColumn()
			trkKbColored(f.col, f.code)
			imgui.TableNextColumn()
			imgui.PushTextWrapPos()
			imgui.Text(f.text)
			imgui.PopTextWrapPos()
		}
		imgui.EndTable()
	}
	imgui.Spacing()

	hot := trkKbCursorEffect()
	if imgui.BeginTableV("##trkkbfx", 3, flags, imgui.Vec2{}, 0) {
		imgui.TableSetupColumnV("Code", imgui.TableColumnFlagsWidthFixed, 44, 0)
		imgui.TableSetupColumnV("Effect", imgui.TableColumnFlagsWidthFixed, 120, 0)
		imgui.TableSetupColumnV("What it does", 0, 0, 0)
		imgui.TableHeadersRow()
		group := ""
		for _, fx := range tracker.Effects {
			if fx.Group != group {
				group = fx.Group
				imgui.TableNextRow()
				imgui.TableNextColumn()
				imgui.TableNextColumn()
				trkKbColored(trkKbDimV4, group)
			}
			imgui.TableNextRow()
			if fx.Cmd == hot {
				imgui.TableSetBgColor(imgui.TableBgTargetRowBg1, trkKbRowHot)
			}
			col := trkKbFXV4
			if fx.Group == "Flow" {
				col = trkKbFlowV4
			}
			imgui.TableNextColumn()
			trkKbColored(col, trkKbCode(fx))
			imgui.TableNextColumn()
			imgui.Text(fx.Name)
			imgui.TableNextColumn()
			imgui.PushTextWrapPos()
			imgui.Text(fx.Desc)
			imgui.PopTextWrapPos()
		}
		imgui.EndTable()
	}
}

// trkKbDraw draws the piano in the box at origin, size, and auditions the key
// under a mouse press.
func trkKbDraw(origin, size imgui.Vec2, held [trkKbSemis]bool) {
	e := trkEd
	dl := imgui.WindowDrawList()
	labels := trkKbKeyLabels()

	whites := 0
	for s := 0; s < trkKbSemis; s++ {
		if !trkKbIsBlack(s) {
			whites++
		}
	}
	whiteW := size.X / float32(whites)
	whiteH := size.Y - 2
	blackW := whiteW * 0.62
	blackH := whiteH * 0.6

	// Each key's rectangle, white keys first so black ones draw over them.
	type keyRect struct {
		semi     int
		min, max imgui.Vec2
	}
	var whiteKeys, blackKeys []keyRect
	wi := 0
	for s := 0; s < trkKbSemis; s++ {
		if trkKbIsBlack(s) {
			x := origin.X + float32(wi)*whiteW - blackW/2
			blackKeys = append(blackKeys, keyRect{s, imgui.Vec2{X: x, Y: origin.Y}, imgui.Vec2{X: x + blackW, Y: origin.Y + blackH}})
			continue
		}
		x := origin.X + float32(wi)*whiteW
		whiteKeys = append(whiteKeys, keyRect{s, imgui.Vec2{X: x, Y: origin.Y}, imgui.Vec2{X: x + whiteW, Y: origin.Y + whiteH}})
		wi++
	}

	noteOf := func(semi int) (uint8, bool) {
		n := e.Octave*12 + semi + 1
		if n < int(tracker.NoteMin) || n > int(tracker.NoteMax) {
			return 0, false
		}
		return uint8(n), true
	}

	// The mouse: black keys sit on top, so they are tested first.
	mouse := imgui.MousePos()
	hit := -1
	in := func(r keyRect) bool {
		return mouse.X >= r.min.X && mouse.X < r.max.X && mouse.Y >= r.min.Y && mouse.Y < r.max.Y
	}
	if imgui.IsWindowHovered() {
		for _, k := range blackKeys {
			if in(k) {
				hit = k.semi
			}
		}
		if hit < 0 {
			for _, k := range whiteKeys {
				if in(k) {
					hit = k.semi
				}
			}
		}
	}
	if imgui.IsMouseDown(0) && hit >= 0 && hit != trkKbMouseSemi && (trkKbMouseSemi >= 0 || imgui.IsMouseClicked(0)) {
		// A press, or a drag across the keys: glissando, one note at a time.
		trkKbRelease()
		if n, ok := noteOf(hit); ok {
			trkAudition(trkKbMouseKey, e.Instr, n)
			trkKbMouseSemi = hit
		}
	}
	if !imgui.IsMouseDown(0) {
		trkKbRelease()
	}

	drawKey := func(k keyRect, black bool) {
		col := trkColKbWhite
		if black {
			col = trkColKbBlack
		}
		if held[k.semi] {
			col = trkColKbHeld
		}
		dl.AddRectFilledV(k.min, k.max, col, 3, imgui.DrawFlagsRoundCornersBottom)
		dl.AddRectV(k.min, k.max, trkColKbEdge, 3, imgui.DrawFlagsRoundCornersBottom, 1)
		if k.semi%12 == 0 && !held[k.semi] {
			// Each octave's C is tinted, so the octave is findable at a glance.
			dl.AddRectFilledV(imgui.Vec2{X: k.min.X + 1, Y: k.max.Y - 6}, imgui.Vec2{X: k.max.X - 1, Y: k.max.Y - 1}, trkColKbOctaveC, 2, imgui.DrawFlagsRoundCornersBottom)
		}

		keyCol, noteCol := trkColKbLabelKey, trkColKbLabelDark
		if black && !held[k.semi] {
			keyCol, noteCol = trkColText, trkColTextDim
		}
		lineH := imgui.TextLineHeight()
		y := k.max.Y - 8 - lineH*2
		if n, ok := noteOf(k.semi); ok {
			t := tracker.NoteName(n)
			tw := imgui.CalcTextSize(t, false, 0).X
			dl.AddText(imgui.Vec2{X: (k.min.X + k.max.X - tw) / 2, Y: y + lineH}, noteCol, t)
		}
		for i, part := range strings.Fields(labels[k.semi]) {
			tw := imgui.CalcTextSize(part, false, 0).X
			dl.AddText(imgui.Vec2{X: (k.min.X + k.max.X - tw) / 2, Y: y - float32(i)*lineH}, keyCol, part)
		}
	}
	for _, k := range whiteKeys {
		drawKey(k, false)
	}
	for _, k := range blackKeys {
		drawKey(k, true)
	}
}
