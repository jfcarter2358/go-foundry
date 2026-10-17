package editor

// editor_score_tools_view.go — the tools' windows: the Transpose dialog
// (modal), the Find window (not modal: the score stays live under it, and
// Find Next works with it closed) and the command palette (Ctrl+Shift+P: a
// box at the top of the window that lists every command with its keys as
// you type). The model is editor_score_tools.go.

import (
	"fmt"

	"foundry/internal/imguibackend"
	"foundry/internal/score"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// ---------- transpose ----------

// scoreIntervalChoice is one interval the dialog offers, within an octave.
type scoreIntervalChoice struct {
	Name string
	IV   score.Interval
}

var scoreIntervalChoices = []scoreIntervalChoice{
	{"Augmented unison (A1)", score.Interval{Diatonic: 0, Chromatic: 1}},
	{"Minor second (m2)", score.Interval{Diatonic: 1, Chromatic: 1}},
	{"Major second (M2)", score.Interval{Diatonic: 1, Chromatic: 2}},
	{"Minor third (m3)", score.Interval{Diatonic: 2, Chromatic: 3}},
	{"Major third (M3)", score.Interval{Diatonic: 2, Chromatic: 4}},
	{"Perfect fourth (P4)", score.Interval{Diatonic: 3, Chromatic: 5}},
	{"Augmented fourth (A4)", score.Interval{Diatonic: 3, Chromatic: 6}},
	{"Diminished fifth (d5)", score.Interval{Diatonic: 4, Chromatic: 6}},
	{"Perfect fifth (P5)", score.Interval{Diatonic: 4, Chromatic: 7}},
	{"Minor sixth (m6)", score.Interval{Diatonic: 5, Chromatic: 8}},
	{"Major sixth (M6)", score.Interval{Diatonic: 5, Chromatic: 9}},
	{"Minor seventh (m7)", score.Interval{Diatonic: 6, Chromatic: 10}},
	{"Major seventh (M7)", score.Interval{Diatonic: 6, Chromatic: 11}},
	{"Perfect octave (P8)", score.Interval{Diatonic: 7, Chromatic: 12}},
}

// The Transpose dialog's fields.
var (
	scoreShowTranspose bool
	// scoreTrErr is why the last Transpose was refused.
	scoreTrErr string
	scoreTrDlg = scoreTrFields{Interval: 2, Up: true, Dir: 0, Chords: true, Keys: true}
)

type scoreTrFields struct {
	ByKey    bool
	Fifths   int
	Dir      int
	Interval int // index into scoreIntervalChoices
	Octaves  int32
	Up       bool
	Diatonic bool
	Steps    int32
	Whole    bool
	Keys     bool
	Chords   bool
	Doubles  bool
}

// opts is what the fields ask for.
func (f scoreTrFields) opts() scoreTransposeOpts {
	o := scoreTransposeOpts{ByKey: f.ByKey, Fifths: f.Fifths, Dir: f.Dir, Whole: f.Whole, Keys: f.Keys, NoChords: !f.Chords, Doubles: f.Doubles}
	sign := 1
	if !f.Up {
		sign = -1
	}
	if f.Diatonic {
		o.Diatonic, o.Steps = true, sign*int(max(f.Steps, 1))
		return o
	}
	iv := scoreIntervalChoices[max(0, min(f.Interval, len(scoreIntervalChoices)-1))].IV
	iv.Diatonic += 7 * int(f.Octaves)
	iv.Chromatic += 12 * int(f.Octaves)
	o.Interval = score.Interval{Diatonic: sign * iv.Diatonic, Chromatic: sign * iv.Chromatic}
	return o
}

func scoreOpenTranspose() {
	e := scEd
	scoreTrDlg.Whole = e.Sel.Kind == scoreSelNone
	scoreTrDlg.Fifths = e.Score.KeyAt(e.toolRange(scoreTrDlg.Whole).A).Fifths
	scoreTrErr = ""
	scoreShowTranspose = true
}

func scoreTransposeDialog() {
	imguibackend.WithModalPopup("Transpose##sctranspose", &scoreShowTranspose, func() {
		e := scEd
		f := &scoreTrDlg
		if imgui.RadioButton("By interval##sctrbyiv", !f.ByKey) {
			f.ByKey = false
		}
		imgui.SameLine()
		if imgui.RadioButton("To a key##sctrbykey", f.ByKey) {
			f.ByKey = true
		}
		imgui.Separator()
		r := e.toolRange(f.Whole)
		if f.ByKey {
			k := e.Score.KeyAt(r.A)
			editorTextMuted("From " + scoreKeyName(k.Fifths, k.Mode) + " (concert)")
			imgui.SetNextItemWidth(220)
			if imguibackend.BeginCombo("Key##sctrkey", scoreKeyName(f.Fifths, k.Mode)) {
				for x := -7; x <= 7; x++ {
					if imguibackend.SelectableV(fmt.Sprintf("%s  (%s)##sctrk%d", scoreKeyName(x, k.Mode), scoreFifthsText(x), x), x == f.Fifths, 0, imgui.Vec2{}) {
						f.Fifths = x
					}
				}
				imguibackend.EndCombo()
			}
			for i, label := range []string{"Closest", "Up", "Down"} {
				d := []int{0, 1, -1}[i]
				if i > 0 {
					imgui.SameLine()
				}
				if imgui.RadioButton(label+"##sctrdir", f.Dir == d) {
					f.Dir = d
				}
			}
			editorTextMuted("That is " + e.transposeInterval(f.opts(), r).String() + "; the key signatures move with it.")
		} else {
			if imgui.RadioButton("Up##sctrup", f.Up) {
				f.Up = true
			}
			imgui.SameLine()
			if imgui.RadioButton("Down##sctrdown", !f.Up) {
				f.Up = false
			}
			imgui.SameLine()
			imgui.Checkbox("Diatonic##sctrdia", &f.Diatonic)
			editorMenuTooltip("By letters within the key (C up a third is E in C major, E-flat in C minor),\nthe key signatures unchanged; off, by an exact interval.")
			if f.Diatonic {
				imgui.SetNextItemWidth(160)
				imgui.SliderIntV("Steps##sctrsteps", &f.Steps, 1, 14, "%d", 0)
				if f.Steps < 1 {
					f.Steps = 1
				}
				editorTextMuted("1 a second, 2 a third ... 7 an octave")
			} else {
				imgui.SetNextItemWidth(240)
				if imguibackend.BeginCombo("Interval##sctriv", scoreIntervalChoices[f.Interval].Name) {
					for i, c := range scoreIntervalChoices {
						if imguibackend.SelectableV(c.Name+fmt.Sprintf("##sctrivc%d", i), i == f.Interval, 0, imgui.Vec2{}) {
							f.Interval = i
						}
					}
					imguibackend.EndCombo()
				}
				imgui.SetNextItemWidth(120)
				imgui.SliderIntV("Octaves more##sctroct", &f.Octaves, 0, 3, "%d", 0)
			}
		}
		imgui.Separator()
		has := e.Sel.Kind != scoreSelNone
		if imgui.RadioButton("The selection##sctrsel", !f.Whole && has) && has {
			f.Whole = false
		}
		if has {
			imgui.SameLine()
			editorTextMuted("(" + e.describeRange(e.toolRange(false)) + ")")
		}
		if imgui.RadioButton("The whole score##sctrall", f.Whole || !has) {
			f.Whole = true
		}
		allLines := r.Lo == 0 && r.Hi == len(e.Score.Lines)-1
		if !f.ByKey && !f.Diatonic {
			if allLines {
				imgui.Checkbox("Transpose key signatures##sctrkeys", &f.Keys)
				editorMenuTooltip("When every line moves, the key signatures move too (an enharmonic key is\nchosen past six sharps or flats, and the notes are spelled for it).")
			} else {
				editorTextMuted("Key signatures stay: not every line is moving.")
			}
		}
		imgui.Checkbox("Transpose chord symbols##sctrchords", &f.Chords)
		if !f.Diatonic {
			imgui.Checkbox("Use double sharps and flats##sctrdbl", &f.Doubles)
		}
		if imgui.Button("Transpose##sctrok") {
			scoreTrErr = ""
			if e.transposeSel(f.opts()) {
				imgui.CloseCurrentPopup()
			} else {
				scoreTrErr = e.Status
			}
		}
		imgui.SameLine()
		if imgui.Button("Cancel##sctrcancel") {
			imgui.CloseCurrentPopup()
		}
		if scoreTrErr != "" {
			imgui.PushStyleColor(imgui.StyleColorText, saveChipUnsaved)
			imgui.Text(scoreTrErr)
			imgui.PopStyleColor()
		}
	})
}

// ---------- find ----------

var (
	scoreFindOpen  bool
	scoreFindFocus bool
)

func scoreOpenFind() {
	scoreFindOpen, scoreFindFocus = true, true
}

// scoreFindWindow is the Find window, top right over the score.
func scoreFindWindow(W float32) {
	if !scoreFindOpen {
		return
	}
	e := scEd
	f := &e.Find
	imgui.SetNextWindowPosV(imgui.Vec2{X: W - 16, Y: scoreViewTop + 8}, imgui.ConditionAppearing, imgui.Vec2{X: 1, Y: 0})
	imgui.SetNextWindowSizeV(imgui.Vec2{X: 420, Y: 0}, imgui.ConditionAppearing)
	open := true
	if imgui.BeginV("Find##scfind", &open, imgui.WindowFlagsNoCollapse|imgui.WindowFlagsAlwaysAutoResize|imgui.WindowFlagsNoSavedSettings) {
		if scoreFindFocus {
			imgui.SetKeyboardFocusHere()
			scoreFindFocus = false
		}
		imgui.SetNextItemWidth(260)
		if imgui.InputTextV("##scfindq", &f.Query, imgui.InputTextFlagsEnterReturnsTrue, nil) {
			e.find(f)
			scoreFindFocus = true
		}
		imgui.SameLine()
		if imgui.Button("Find##scfindgo") {
			e.find(f)
		}
		for i, n := range scoreFindKindNames {
			if i > 0 {
				imgui.SameLine()
			}
			if imgui.RadioButton(n+"##scfindk", f.Kind == scoreFindKind(i)) {
				f.Kind, f.Hits = scoreFindKind(i), nil
			}
		}
		if imgui.Checkbox("Exact spelling##scfindexact", &f.Exact) {
			f.Hits = nil
		}
		editorMenuTooltip("F#4 finds F#4 only, not Gb4; off, any spelling of the sound.")
		imgui.SameLine()
		if imgui.Checkbox("In the selection##scfindsel", &f.InSel) {
			f.Hits = nil
		}
		if f.Err != "" {
			editorTextMuted(f.Err)
		}
		if len(f.Hits) > 0 {
			if imgui.Button("Previous##scfindprev") {
				e.findStep(f, -1)
			}
			editorMenuTooltip(scoreKeymapActive().label(scFindPrev))
			imgui.SameLine()
			if imgui.Button("Next##scfindnext") {
				e.findStep(f, 1)
			}
			editorMenuTooltip(scoreKeymapActive().label(scFindNext))
			imgui.SameLine()
			editorTextMuted(fmt.Sprintf("%d found", len(f.Hits)))
			imgui.BeginChildV("##scfindlist", imgui.Vec2{X: 400, Y: min(float32(len(f.Hits))*imgui.TextLineHeightWithSpacing()+8, 220)}, true, 0)
			for i, h := range f.Hits {
				if imgui.SelectableV(fmt.Sprintf("%s: %s##scfh%d", e.hitWhere(h), h.What, i), i == f.Cur, 0, imgui.Vec2{}) {
					f.Cur = i
					e.showHit(h)
				}
			}
			imgui.EndChild()
		}
		if imgui.Button("Close##scfindclose") {
			open = false
		}
	}
	imgui.End()
	scoreFindOpen = open
}

// ---------- the command palette ----------

// scorePalette is the palette's state.
var scorePalette struct {
	Open  bool
	Query string
	Cur   int
	items []scorePaletteItem
	shown []int
	focus bool
}

func scoreOpenPalette() {
	scorePalette.Open, scorePalette.Query, scorePalette.Cur, scorePalette.focus = true, "", 0, true
	scorePalette.items = scorePaletteItems()
	scorePalette.shown = scorePaletteMatch(scorePalette.items, "")
}

// scorePaletteClose closes the palette.
func scorePaletteClose() { scorePalette.Open = false }

// scorePaletteKey takes the palette's own keys before ImGui's text field:
// Up and Down move, Return runs, Escape closes. It reports a key taken.
func scorePaletteKey(sc sdl.Scancode) bool {
	p := &scorePalette
	switch sc {
	case sdl.SCANCODE_ESCAPE:
		scorePaletteClose()
	case sdl.SCANCODE_UP:
		p.Cur = max(p.Cur-1, 0)
	case sdl.SCANCODE_DOWN:
		p.Cur = min(p.Cur+1, max(len(p.shown)-1, 0))
	case sdl.SCANCODE_PAGEUP:
		p.Cur = max(p.Cur-10, 0)
	case sdl.SCANCODE_PAGEDOWN:
		p.Cur = min(p.Cur+10, max(len(p.shown)-1, 0))
	case sdl.SCANCODE_RETURN, sdl.SCANCODE_KP_ENTER:
		scorePaletteRun(p.Cur)
	default:
		return false
	}
	return true
}

// scorePaletteRun runs the k-th shown item and closes the palette.
func scorePaletteRun(k int) {
	p := &scorePalette
	if k < 0 || k >= len(p.shown) {
		return
	}
	it := p.items[p.shown[k]]
	scorePaletteClose()
	runPaletteItem(it)
}

// scorePaletteWindow draws the palette at the top of the window.
func scorePaletteWindow(W float32) {
	p := &scorePalette
	if !p.Open {
		return
	}
	w := min(W-40, 640)
	imgui.SetNextWindowPosV(imgui.Vec2{X: W / 2, Y: editorTopBarH + 6}, imgui.ConditionAlways, imgui.Vec2{X: 0.5, Y: 0})
	imgui.SetNextWindowSizeV(imgui.Vec2{X: w, Y: 0}, imgui.ConditionAlways)
	imgui.SetNextWindowBgAlpha(0.97)
	flags := imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize | imgui.WindowFlagsNoMove | imgui.WindowFlagsNoSavedSettings |
		imgui.WindowFlagsAlwaysAutoResize | imgui.WindowFlagsNoCollapse
	opening := p.focus
	if opening {
		imgui.SetNextWindowFocus()
	}
	if imgui.BeginV("##scpalette", nil, flags) {
		if p.focus {
			imgui.SetKeyboardFocusHere()
			p.focus = false
		}
		imgui.SetNextItemWidth(w - 16)
		if imgui.InputTextWithHint("##scpalq", "Type a command: transpose, slur, export, voice 2 ...", &p.Query) {
			p.shown = scorePaletteMatch(p.items, p.Query)
			p.Cur = 0
		}
		rows := min(len(p.shown), 14)
		if rows == 0 {
			editorTextMuted("No command matches")
		}
		// Keep the current row in the window of rows shown.
		first := max(0, min(p.Cur-rows/2, len(p.shown)-rows))
		dl := imgui.WindowDrawList()
		for k := first; k < first+rows; k++ {
			it := p.items[p.shown[k]]
			at := imgui.CursorScreenPos()
			if imgui.SelectableV(fmt.Sprintf("%s##scpal%d", it.Label, k), k == p.Cur, 0, imgui.Vec2{X: w - 16}) {
				scorePaletteRun(k)
				break
			}
			if keys := it.keys(); keys != "" {
				tw := imgui.CalcTextSize(keys, false, 0).X
				dl.AddText(imgui.Vec2{X: at.X + w - 24 - tw, Y: at.Y}, scorePaletteKeyCol, keys)
			}
			// The group goes between the label and the keys when there is
			// room for it.
			lw := imgui.CalcTextSize(it.Label, false, 0).X
			gw := imgui.CalcTextSize(it.Group, false, 0).X
			kw := imgui.CalcTextSize(it.keys(), false, 0).X
			if lw+gw+kw+64 < w {
				dl.AddText(imgui.Vec2{X: at.X + w - 40 - kw - gw, Y: at.Y}, scorePaletteGroupCol, it.Group)
			}
		}
		if len(p.shown) > rows {
			editorTextMuted(fmt.Sprintf("%d of %d: Up and Down for more", rows, len(p.shown)))
		}
		// A click anywhere else closes it.
		if !opening && !imgui.IsWindowFocusedV(imgui.FocusedFlagsRootAndChildWindows) {
			scorePaletteClose()
		}
	}
	imgui.End()
}

var (
	scorePaletteKeyCol   = imgui.PackedColorFromVec4(imgui.Vec4{X: 0.75, Y: 0.82, Z: 0.95, W: 1})
	scorePaletteGroupCol = imgui.PackedColorFromVec4(imgui.Vec4{X: 0.55, Y: 0.57, Z: 0.62, W: 1})
)
