package editor

// editor_score_palette.go — entering expression with the mouse and in
// words: the marks palette (a floating panel like the keypad), the Marks
// menu, and the dialogs for what needs typing — the expression box
// (Ctrl+E: "mf", "pizz.", "dolce", "cresc.", "rit."), tempo marks and tempo
// text, rehearsal marks, repeats, endings and clef changes.
//
// Every button and item calls the same scoreState method its key does
// (editor_score_marks.go), so the palette, the menu and the keyboard
// cannot drift apart; the palette shows each one's keys in its tooltip.

import (
	"fmt"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/engrave"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// scoreShowMarks is the marks palette shown.
var scoreShowMarks bool

// Dialog state for the marks.
var (
	scoreShowExpr     bool
	scoreExprText     string
	scoreExprFocus    bool
	scoreShowTempo    bool
	scoreTempoDlg     scoreTempoFields
	scoreShowRehearse bool
	scoreRehearseName string
	scoreRehearseBar  int32
	scoreShowRepeat   bool
	scoreShowEnding   bool
	scoreFormA        int32
	scoreFormZ        int32
	scoreRepeatTimes  int32
	scoreEndingText   string
	scoreShowClefAt   bool
	scoreClefAtPos    score.Pos
	scoreFormErr      string
)

// scoreTempoFields are the tempo dialog's fields.
type scoreTempoFields struct {
	At   score.Pos
	Text string
	BPM  int32
	Unit int
	// Has says a mark is at At already (the dialog edits it).
	Has bool
}

// scoreTempoUnits are the beat a tempo mark counts, as the dialog offers.
var scoreTempoUnits = []struct {
	Label string
	Dur   score.Duration
}{
	{"quarter", score.Duration{Base: score.Quarter}},
	{"dotted quarter", score.Duration{Base: score.Quarter, Dots: 1}},
	{"half", score.Duration{Base: score.Half}},
	{"dotted half", score.Duration{Base: score.Half, Dots: 1}},
	{"eighth", score.Duration{Base: score.Eighth}},
	{"dotted eighth", score.Duration{Base: score.Eighth, Dots: 1}},
}

// The words the expression box offers as buttons, by what they are.
var (
	scoreDynButtons     = []string{"ppp", "pp", "p", "mp", "mf", "f", "ff", "fff", "sfz", "sf", "fz", "rfz", "fp", "sfp", "niente"}
	scoreTechButtons    = []string{"pizz.", "arco", "con sord.", "senza sord.", "trem.", "sul pont.", "sul tasto", "col legno", "ord."}
	scoreExprButtons    = []string{"dolce", "espress.", "cantabile", "legato", "marcato", "cresc.", "dim."}
	scoreTempoButtons   = []string{"rit.", "rall.", "accel.", "a tempo", "Tempo primo", "meno mosso", "più mosso", "poco rit.", "molto rit."}
	scoreTempoNames     = []string{"Largo", "Adagio", "Andante", "Moderato", "Allegretto", "Allegro", "Vivace", "Presto"}
	scoreTempoNameBPM   = []int32{50, 66, 84, 104, 112, 132, 160, 184}
	scoreExpressionKind = map[edit.Expression]string{
		edit.ExprText: "expression text", edit.ExprDynamic: "a dynamic", edit.ExprTechnique: "technique (playback switches articulation)",
		edit.ExprCresc: "cresc. with dashes", edit.ExprDim: "dim. with dashes", edit.ExprTempo: "tempo text (playback follows it)",
	}
)

// scoreTitle is a word with a capital.
func scoreTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// scoreSelDynamic is the selected event's dynamic, or "".
func scoreSelDynamic() string {
	if ev := scEd.event(); ev != nil {
		return edit.Dynamic(ev)
	}
	return ""
}

// ---------- opening the dialogs ----------

// scoreOpenExpression is Ctrl+E: the expression box, for the selection.
func scoreOpenExpression() {
	e := scEd
	if e.Sel.Kind == scoreSelNone || e.Sel.Kind == scoreSelCaret {
		e.Status = "Select a note or bars, then Ctrl+E writes a dynamic or a word there"
		return
	}
	scoreShowExpr, scoreExprText, scoreExprFocus = true, "", true
}

// scoreOpenTempo is the tempo dialog at the selection (or the play head's
// bar), filled from the mark already there.
func scoreOpenTempo() {
	e := scEd
	at := score.Pos{Bar: e.headBar()}
	if e.Sel.Kind != scoreSelNone {
		at = e.selPos()
	}
	f := scoreTempoFields{At: at, BPM: 0}
	if t := edit.TempoMarkAt(e.Score, at); t != nil {
		f.Has, f.Text, f.BPM = true, t.Text, int32(t.BPM+0.5)
		for i, u := range scoreTempoUnits {
			if u.Dur == t.Unit {
				f.Unit = i
			}
		}
	}
	scoreTempoDlg, scoreFormErr, scoreShowTempo = f, "", true
}

// scoreOpenRehearsal is the rehearsal-mark dialog at the selected bar.
func scoreOpenRehearsal() {
	e := scEd
	bar := e.selBar()
	scoreRehearseBar, scoreRehearseName = int32(bar), ""
	for _, sec := range e.Score.Sections {
		if sec.Bar == bar {
			scoreRehearseName = sec.Name
		}
	}
	scoreFormErr, scoreShowRehearse = "", true
}

// scoreOpenFormBars fills the bar range of the repeat and ending dialogs
// from the selection.
func scoreOpenFormBars() bool {
	a, z, ok := scEd.selBars()
	if !ok {
		scEd.Status = "Select the bars first"
		return false
	}
	scoreFormA, scoreFormZ, scoreFormErr = int32(a), int32(z), ""
	return true
}

func scoreOpenRepeat() {
	if scoreOpenFormBars() {
		scoreRepeatTimes, scoreShowRepeat = 2, true
	}
}

func scoreOpenEnding() {
	if scoreOpenFormBars() {
		scoreEndingText, scoreShowEnding = "1", true
		for _, v := range scEd.Score.Voltas {
			if v.End+1 == int(scoreFormA) {
				// After an ending, the next one plays on the next pass.
				scoreEndingText = fmt.Sprint(v.Passes[len(v.Passes)-1] + 1)
			}
		}
	}
}

// scoreOpenClefChange is the clef-change dialog for the selected staff
// from the selection's start.
func scoreOpenClefChange() {
	e := scEd
	switch e.Sel.Kind {
	case scoreSelEvent, scoreSelCaret:
		scoreDlgLine, scoreDlgStaff = e.Sel.Loc.Line, e.Sel.Loc.Staff
	case scoreSelBars:
		scoreDlgLine, scoreDlgStaff = e.Sel.LineA, e.Sel.Staff
	default:
		e.Status = "Select where the clef changes"
		return
	}
	st := edit.Staff(e.Score, scoreDlgLine, scoreDlgStaff)
	if st == nil {
		return
	}
	scoreClefAtPos = e.selPos()
	scoreDlgClef = st.ClefAt(scoreClefAtPos)
	scoreFormErr, scoreShowClefAt = "", true
}

// ---------- the dialogs ----------

// scoreMarkDialogs draws the marks' dialogs.
func scoreMarkDialogs() {
	e := scEd
	imguibackend.WithModalPopup("Expression##scexpr", &scoreShowExpr, func() {
		editorTextMuted("A dynamic, a technique word, cresc. / dim. or tempo text, or any words.")
		imgui.SetNextItemWidth(360)
		if scoreExprFocus {
			imgui.SetKeyboardFocusHere()
			scoreExprFocus = false
		}
		enter := imgui.InputTextV("##scexprtext", &scoreExprText, imgui.InputTextFlagsEnterReturnsTrue, nil)
		k, t := edit.ClassifyExpression(scoreExprText)
		if t != "" {
			editorTextMuted(fmt.Sprintf("%q: %s", t, scoreExpressionKind[k]))
		} else {
			editorTextMuted(" ")
		}
		pick := ""
		row := func(label string, words []string, glyphs bool) {
			editorTextMuted(label)
			for i, w := range words {
				if i > 0 && i%9 != 0 {
					imgui.SameLine()
				}
				g := engrave.DynamicGlyphs(w)
				if glyphs && len(g) == 1 {
					if scoreGlyphButton("##scexd"+w, g[0], false) {
						pick = w
					}
					editorMenuTooltip(w)
					continue
				}
				if imgui.Button(w + "##scexw") {
					pick = w
				}
			}
		}
		row("Dynamics", scoreDynButtons, true)
		row("Technique", scoreTechButtons, false)
		row("Expression", scoreExprButtons, false)
		row("Tempo", scoreTempoButtons, false)
		if pick != "" {
			e.expression(pick)
			imgui.CloseCurrentPopup()
		}
		imgui.Separator()
		if (imgui.Button("OK##scexprok") || enter) && strings.TrimSpace(scoreExprText) != "" {
			e.expression(scoreExprText)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Remove Text##scexprrm") {
			e.removeText()
			imgui.CloseCurrentPopup()
		}
		editorMenuTooltip("Take the expression and technique text off the selection")
		imgui.SameLine()
		if imgui.Button("Remove Dynamic##scexprrmd") {
			e.dynamic("")
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##scexprcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Tempo##sctempodlg", &scoreShowTempo, func() {
		f := &scoreTempoDlg
		editorTextMuted(fmt.Sprintf("At bar %d %s", f.At.Bar, scoreBeatText(e.Score, f.At.Bar, f.At.Offset)))
		imgui.SetNextItemWidth(260)
		imgui.InputText("Text##sctempotext", &f.Text)
		imgui.SetNextItemWidth(120)
		imgui.InputInt("Beats a minute##sctempobpm", &f.BPM)
		f.BPM = int32(clampI(int(f.BPM), 0, 400))
		imgui.SetNextItemWidth(160)
		if imguibackend.BeginCombo("Beat##sctempounit", scoreTempoUnits[f.Unit].Label) {
			for i, u := range scoreTempoUnits {
				if imguibackend.SelectableV(u.Label+"##sctu", i == f.Unit, 0, imgui.Vec2{}) {
					f.Unit = i
				}
			}
			imguibackend.EndCombo()
		}
		editorTextMuted("0 beats a minute is text only: rit., accel. and a tempo play\nfrom the marks around them.")
		for i, n := range scoreTempoNames {
			if i > 0 {
				imgui.SameLine()
			}
			if imgui.Button(n + "##sctn") {
				f.Text, f.BPM = n, scoreTempoNameBPM[i]
			}
		}
		for i, n := range scoreTempoButtons {
			if i > 0 {
				imgui.SameLine()
			}
			if imgui.Button(n + "##sctt") {
				f.Text, f.BPM = n, 0
			}
		}
		scoreDlgError()
		if imgui.Button("OK##sctempook") {
			t := score.TempoMark{At: f.At, Text: strings.TrimSpace(f.Text), BPM: float64(f.BPM)}
			if f.BPM > 0 && f.Unit > 0 {
				t.Unit = scoreTempoUnits[f.Unit].Dur
			}
			if t.BPM == 0 && t.Text == "" {
				scoreFormErr = "Give the mark a tempo or words"
			} else if e.setTempo(t) {
				imgui.CloseCurrentPopup()
			} else {
				scoreFormErr = e.Status
			}
		}
		imgui.SameLine()
		if f.Has && imgui.Button("Remove##sctemporm") {
			if e.removeTempo(f.At) {
				imgui.CloseCurrentPopup()
			} else {
				scoreFormErr = e.Status
			}
		}
		imgui.SameLine()
		if imgui.Button("Cancel##sctempocancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Rehearsal Mark##screhdlg", &scoreShowRehearse, func() {
		imgui.SetNextItemWidth(100)
		imgui.InputInt("At bar##screhbar", &scoreRehearseBar)
		scoreRehearseBar = int32(clampI(int(scoreRehearseBar), 1, e.Score.Measures()))
		imgui.SetNextItemWidth(240)
		imgui.InputText("Section name##screhname", &scoreRehearseName)
		editorTextMuted("Printed as a boxed letter with the name; scripts and play_song\nfind sections by it.")
		scoreDlgError()
		if imgui.Button("OK##screhok") {
			if e.setRehearsal(int(scoreRehearseBar), scoreRehearseName) {
				imgui.CloseCurrentPopup()
			} else {
				scoreFormErr = e.Status
			}
		}
		imgui.SameLine()
		if imgui.Button("Remove##screhrm") {
			if e.removeRehearsal(int(scoreRehearseBar)) {
				imgui.CloseCurrentPopup()
			} else {
				scoreFormErr = e.Status
			}
		}
		imgui.SameLine()
		if imgui.Button("Cancel##screhcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Repeat##screpdlg", &scoreShowRepeat, func() {
		scoreFormBarFields("screp")
		imgui.SetNextItemWidth(100)
		imgui.InputInt("Plays##screptimes", &scoreRepeatTimes)
		scoreRepeatTimes = int32(clampI(int(scoreRepeatTimes), 2, 16))
		editorTextMuted("Repeat barlines round the bars; more than twice prints \"x3\".")
		scoreDlgError()
		if imgui.Button("OK##screpok") {
			if e.setRepeat(int(scoreFormA), int(scoreFormZ), int(scoreRepeatTimes)) {
				imgui.CloseCurrentPopup()
			} else {
				scoreFormErr = e.Status
			}
		}
		imgui.SameLine()
		if imgui.Button("Remove##screprm") {
			if e.removeRepeats(int(scoreFormA), int(scoreFormZ)) {
				imgui.CloseCurrentPopup()
			} else {
				scoreFormErr = e.Status
			}
		}
		imgui.SameLine()
		if imgui.Button("Cancel##screpcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Ending##scenddlg", &scoreShowEnding, func() {
		scoreFormBarFields("scend")
		imgui.SetNextItemWidth(160)
		imgui.InputText("Passes##scendpasses", &scoreEndingText)
		editorTextMuted("The times through the repeat this ending plays on: 1, or 1, 2, or 3.")
		scoreDlgError()
		if imgui.Button("OK##scendok") {
			passes, err := edit.ParsePasses(scoreEndingText)
			switch {
			case err != nil:
				scoreFormErr = err.Error()
			case e.setEnding(int(scoreFormA), int(scoreFormZ), passes):
				imgui.CloseCurrentPopup()
			default:
				scoreFormErr = e.Status
			}
		}
		imgui.SameLine()
		if imgui.Button("Remove##scendrm") {
			if e.removeEndings(int(scoreFormA), int(scoreFormZ)) {
				imgui.CloseCurrentPopup()
			} else {
				scoreFormErr = e.Status
			}
		}
		imgui.SameLine()
		if imgui.Button("Cancel##scendcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Clef Change##scclefat", &scoreShowClefAt, func() {
		p := scoreClefAtPos
		editorTextMuted(fmt.Sprintf("%s from bar %d %s", scoreStaffName(scoreDlgLine, scoreDlgStaff), p.Bar, scoreBeatText(e.Score, p.Bar, p.Offset)))
		imgui.SetNextItemWidth(240)
		if imguibackend.BeginCombo("Clef##scclefatcombo", scoreDlgClef) {
			for _, c := range score.StaffClefs() {
				if c == "percussion" {
					continue
				}
				if imguibackend.SelectableV(c+"##sccla", c == scoreDlgClef, 0, imgui.Vec2{}) {
					scoreDlgClef = c
				}
			}
			imguibackend.EndCombo()
		}
		editorTextMuted("The notes keep their pitches; the staff reads them in the new clef\nfrom here to the next change.")
		scoreDlgError()
		if imgui.Button("OK##scclefatok") {
			if e.setClefChange(scoreDlgLine, scoreDlgStaff, p, scoreDlgClef) {
				imgui.CloseCurrentPopup()
			} else {
				scoreFormErr = e.Status
			}
		}
		imgui.SameLine()
		if imgui.Button("Remove##scclefatrm") {
			if e.removeClefChange(scoreDlgLine, scoreDlgStaff, p) {
				imgui.CloseCurrentPopup()
			} else {
				scoreFormErr = e.Status
			}
		}
		imgui.SameLine()
		if imgui.Button("Cancel##scclefatcancel") {
			imgui.CloseCurrentPopup()
		}
	})
}

// scoreFormBarFields are the from / to bar fields of the repeat and ending
// dialogs.
func scoreFormBarFields(id string) {
	n := scEd.Score.Measures()
	imgui.SetNextItemWidth(100)
	imgui.InputInt("From bar##"+id+"a", &scoreFormA)
	imgui.SetNextItemWidth(100)
	imgui.InputInt("To bar##"+id+"z", &scoreFormZ)
	scoreFormA = int32(clampI(int(scoreFormA), 1, n))
	scoreFormZ = int32(clampI(int(scoreFormZ), int(scoreFormA), n))
}

// scoreDlgError shows the last refusal in a marks dialog.
func scoreDlgError() {
	if scoreFormErr == "" {
		return
	}
	imgui.PushStyleColor(imgui.StyleColorText, saveChipUnsaved)
	imgui.Text(scoreFormErr)
	imgui.PopStyleColor()
}

// ---------- jumps ----------

// scoreJumps are the navigation marks the palette and the menu offer.
var scoreJumps = []struct {
	Label, Glyph string
	Kind, Al     score.JumpKind
}{
	{"Segno", "segno", score.JumpSegno, 0},
	{"Coda", "coda", score.JumpCoda, 0},
	{"To Coda", "", score.JumpToCoda, 0},
	{"Fine", "", score.JumpFine, 0},
	{"D.C.", "", score.JumpDC, 0},
	{"D.C. al Fine", "", score.JumpDC, score.JumpFine},
	{"D.C. al Coda", "", score.JumpDC, score.JumpCoda},
	{"D.S.", "", score.JumpDS, 0},
	{"D.S. al Fine", "", score.JumpDS, score.JumpFine},
	{"D.S. al Coda", "", score.JumpDS, score.JumpCoda},
}

// ---------- the palette ----------

// scorePaletteMark is one palette button: a mark name and value, a glyph,
// a label.
type scorePaletteMark struct {
	Name, Value, Glyph, Label string
	Cmd                       scoreCmd
}

var (
	scorePaletteArticulations = []scorePaletteMark{
		{"stacc", "", "articStaccatoAbove", "Staccato", scStaccato},
		{"staccmo", "", "articStaccatissimoAbove", "Staccatissimo", scStaccatissimo},
		{"portato", "", "articTenutoStaccatoAbove", "Portato", scNone},
		{"ten", "", "articTenutoAbove", "Tenuto", scTenuto},
		{"acc", "", "articAccentAbove", "Accent", scAccent},
		{"marc", "", "articMarcatoAbove", "Marcato", scMarcato},
	}
	// The jazz articulations, as the keypad's Jazz tab has them.
	scorePaletteJazz = []scorePaletteMark{
		{"scoop", "", "brassScoop", "Scoop (slide up into the note)", scNone},
		{"plop", "", "brassPlop", "Plop (drop into the note)", scNone},
		{"bend", "", "brassBend", "Bend (dip and back)", scNone},
		{"doit", "", "brassDoitShort", "Doit (slide up off the note)", scNone},
		{"doit", "long", "brassDoitMedium", "Long doit", scNone},
		{"fall", "", "brassFallLipShort", "Fall (drop off the note)", scNone},
		{"fall", "long", "brassFallLipMedium", "Long fall", scNone},
	}
	scorePaletteOrnaments = []scorePaletteMark{
		{"tr", "", "ornamentTrill", "Trill", scNone},
		{"mord", "", "ornamentMordent", "Mordent", scNone},
		{"imord", "", "ornamentShortTrill", "Inverted mordent", scNone},
		{"turn", "", "ornamentTurn", "Turn", scNone},
		{"iturn", "", "ornamentTurnInverted", "Inverted turn", scNone},
		{"arp", "", "arpeggiatoUp", "Arpeggio (up)", scNone},
		{"arp", "down", "arpeggiatoDown", "Arpeggio (down)", scNone},
	}
	scorePaletteTremolos = []scorePaletteMark{
		{"trem", "1", "tremolo1", "Tremolo, one stroke (eighths)", scNone},
		{"trem", "2", "tremolo2", "Tremolo, two strokes (16ths)", scNone},
		{"trem", "3", "tremolo3", "Tremolo, three strokes (unmeasured)", scNone},
		{"trem2", "3", "tremoloFingered3", "Two-note tremolo with the next note", scNone},
	}
	scorePaletteFermatas = []scorePaletteMark{
		{"ferm", "short", "fermataShortAbove", "Short fermata (x1.5)", scNone},
		{"ferm", "", "fermataAbove", "Fermata (x2)", scNone},
		{"ferm", "long", "fermataLongAbove", "Long fermata (x3)", scNone},
		{"ferm", "verylong", "fermataVeryLongAbove", "Very long fermata (x4)", scNone},
	}
)

// scorePaletteGlyphRow draws a row of mark buttons, lit when the selected
// note has the mark.
func scorePaletteGlyphRow(id string, marks []scorePaletteMark, run func(m scorePaletteMark)) {
	ev := scEd.event()
	for i, m := range marks {
		if i > 0 {
			imgui.SameLine()
		}
		on := false
		if ev != nil {
			for _, k := range ev.Marks {
				on = on || k.Name == m.Name && k.Value == m.Value
			}
		}
		if scoreGlyphButton(fmt.Sprintf("##%s%d", id, i), m.Glyph, on) {
			run(m)
		}
		tip := m.Label
		if m.Cmd != scNone {
			tip += scoreKeysSuffix(m.Cmd)
		}
		editorMenuTooltip(tip)
	}
}

// scorePalettePanel is the marks palette, floating over the score.
func scorePalettePanel(winW, winH float32) {
	e := scEd
	imgui.SetNextWindowPosV(imgui.Vec2{X: winW - 640, Y: scoreViewTop + 60}, imgui.ConditionFirstUseEver, imgui.Vec2{})
	imgui.SetNextWindowBgAlpha(1)
	open := true
	flags := imgui.WindowFlagsAlwaysAutoResize | imgui.WindowFlagsNoSavedSettings | imgui.WindowFlagsNoCollapse
	if !imgui.BeginV("Marks##scoremarks", &open, flags) {
		imgui.End()
		if !open {
			scoreShowMarks = false
		}
		return
	}
	section := func(name string) {
		imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 0.55, Y: 0.58, Z: 0.63, W: 1})
		imgui.Text(name)
		imgui.PopStyleColor()
	}
	section("Dynamics" + scoreKeysSuffix(scExpression))
	for i, d := range scoreDynButtons {
		if i > 0 && i != 8 {
			imgui.SameLine()
		}
		g := engrave.DynamicGlyphs(d)
		if len(g) != 1 {
			continue
		}
		if scoreGlyphButton("##scpd"+d, g[0], scoreSelDynamic() == d) {
			e.dynamic(d)
		}
		editorMenuTooltip(d)
	}
	imgui.SameLine()
	if imgui.Button("Text...##scpexpr") {
		scoreOpenExpression()
	}
	editorMenuTooltip("Any dynamic, technique (pizz.), expression or tempo text" + scoreKeysSuffix(scExpression))

	section("Lines")
	first := true
	lineBtn := func(label, tip string, c scoreCmd, f func()) {
		if !first {
			imgui.SameLine()
		}
		first = false
		if imgui.Button(label) {
			f()
		}
		if c != scNone {
			tip += scoreKeysSuffix(c)
		}
		editorMenuTooltip(tip)
	}
	lineBtn("Slur##scps", "Slur to the next note (again: one note further), or over the selected bars", scSlur, func() { e.slur() })
	lineBtn("Cresc <##scpc", "Crescendo hairpin", scCresc, func() { e.hairpin("cresc") })
	lineBtn("Dim >##scpd", "Diminuendo hairpin", scDim, func() { e.hairpin("dim") })
	lineBtn("cresc.##scpct", "cresc. with dashes", scNone, func() { e.hairpin("cresc.") })
	lineBtn("dim.##scpdt", "dim. with dashes", scNone, func() { e.hairpin("dim.") })
	lineBtn("Gliss##scpg", "Straight glissando to the next note", scGliss, func() { e.gliss("") })
	lineBtn("Gliss ~##scpgw", "Wavy glissando to the next note", scNone, func() { e.gliss("wavy") })
	first = true
	lineBtn("Ped.##scpp", "Sustain pedal over the selection (the line's last staff)", scNone, func() { e.span(edit.SpanPedal, "") })
	for _, o := range []string{"8va", "8vb", "15ma", "15mb"} {
		o := o
		lineBtn(o+"##scpo"+o, "Ottava line: the notes under it are written an octave (two) away", scNone, func() { e.span(edit.SpanOttava, o) })
	}

	section("Articulations")
	scorePaletteGlyphRow("scpa", scorePaletteArticulations, func(m scorePaletteMark) { e.toggleMark(m.Name, m.Value) })
	section("Jazz")
	scorePaletteGlyphRow("scpj", scorePaletteJazz, func(m scorePaletteMark) { e.toggleMark(m.Name, m.Value) })
	section("Ornaments, arpeggios")
	scorePaletteGlyphRow("scpor", scorePaletteOrnaments, func(m scorePaletteMark) { e.toggleMark(m.Name, m.Value) })
	section("Tremolo, fermata")
	scorePaletteGlyphRow("scptr", scorePaletteTremolos, func(m scorePaletteMark) { e.toggleMark(m.Name, m.Value) })
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	scorePaletteGlyphRow("scpf", scorePaletteFermatas, func(m scorePaletteMark) { e.fermata(m.Value) })

	section("Grace notes")
	for i, g := range []struct {
		kind  score.Grace
		glyph string
		label string
		cmd   scoreCmd
	}{
		{score.Acciaccatura, "graceNoteAcciaccaturaStemUp", "Acciaccaturas: the next notes typed are grace notes", scGraceAcc},
		{score.Appoggiatura, "graceNoteAppoggiaturaStemUp", "Appoggiaturas: the next notes typed are grace notes", scGraceApp},
	} {
		if i > 0 {
			imgui.SameLine()
		}
		if scoreGlyphButton(fmt.Sprintf("##scpgr%d", i), g.glyph, e.Grace == g.kind) {
			e.toggleGrace(g.kind)
		}
		editorMenuTooltip(g.label + scoreKeysSuffix(g.cmd))
	}

	section("Form")
	if imgui.Button("Repeat...##scprep") {
		scoreOpenRepeat()
	}
	imgui.SameLine()
	if imgui.Button("Ending...##scpend") {
		scoreOpenEnding()
	}
	imgui.SameLine()
	if imgui.Button("Rehearsal...##scpreh") {
		scoreOpenRehearsal()
	}
	editorMenuTooltip("A rehearsal mark: a section of the score" + scoreKeysSuffix(scRehearsal))
	imgui.SameLine()
	if imgui.Button("Tempo...##scptempo") {
		scoreOpenTempo()
	}
	editorMenuTooltip("A tempo mark or tempo text (rit., a tempo)" + scoreKeysSuffix(scTempoText))
	imgui.SameLine()
	if imgui.Button("Clef...##scpclef") {
		scoreOpenClefChange()
	}
	editorMenuTooltip("A clef change from the selection")
	for i, j := range scoreJumps {
		if i != 0 && i != 4 {
			imgui.SameLine()
		}
		if j.Glyph != "" {
			if scoreGlyphButton("##scpj"+j.Label, j.Glyph, false) {
				e.setJump(j.Kind, j.Al)
			}
			editorMenuTooltip(j.Label + " at the selected bar")
			continue
		}
		if imgui.Button(j.Label + "##scpj") {
			e.setJump(j.Kind, j.Al)
		}
		editorMenuTooltip(j.Label + " at the end of the selected bar")
	}
	imgui.End()
	if !open {
		scoreShowMarks = false
	}
}

// ---------- the Marks menu ----------

// scoreMarksMenu is the menu bar's Marks menu: everything the palette has.
func scoreMarksMenu() {
	e := scEd
	km := scoreKeymapActive()
	item := func(label string, c scoreCmd) {
		if imgui.MenuItemV(label, km.label(c), false, true) {
			scoreRunCmd(c)
		}
	}
	if !imgui.BeginMenu("Marks") {
		return
	}
	item("Expression or Dynamic...", scExpression)
	if imgui.BeginMenu("Dynamic") {
		for _, d := range scoreDynButtons {
			if imgui.MenuItem(d + "##scmd") {
				e.dynamic(d)
			}
		}
		imgui.EndMenu()
	}
	imgui.Separator()
	item("Slur", scSlur)
	item("Crescendo", scCresc)
	item("Diminuendo", scDim)
	if imgui.MenuItem("cresc. (Text)") {
		e.hairpin("cresc.")
	}
	if imgui.MenuItem("dim. (Text)") {
		e.hairpin("dim.")
	}
	item("Glissando", scGliss)
	if imgui.MenuItem("Wavy Glissando") {
		e.gliss("wavy")
	}
	if imgui.MenuItem("Pedal") {
		e.span(edit.SpanPedal, "")
	}
	if imgui.BeginMenu("Ottava") {
		for _, o := range []string{"8va", "8vb", "15ma", "15mb"} {
			if imgui.MenuItem(o + "##scmo") {
				e.span(edit.SpanOttava, o)
			}
		}
		imgui.EndMenu()
	}
	imgui.Separator()
	if imgui.BeginMenu("Articulation") {
		for _, m := range scorePaletteArticulations {
			if imgui.MenuItemV(m.Label, km.label(m.Cmd), false, true) {
				e.toggleMark(m.Name, m.Value)
			}
		}
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Jazz") {
		for _, m := range scorePaletteJazz {
			if imgui.MenuItem(m.Label + "##scmjz") {
				e.toggleMark(m.Name, m.Value)
			}
		}
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Ornament") {
		for _, m := range scorePaletteOrnaments {
			if imgui.MenuItem(m.Label) {
				e.toggleMark(m.Name, m.Value)
			}
		}
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Tremolo") {
		for _, m := range scorePaletteTremolos {
			if imgui.MenuItem(m.Label) {
				e.toggleMark(m.Name, m.Value)
			}
		}
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Fermata") {
		for _, m := range scorePaletteFermatas {
			if imgui.MenuItem(m.Label) {
				e.fermata(m.Value)
			}
		}
		imgui.EndMenu()
	}
	if imgui.MenuItemV("Grace Notes (Acciaccatura)", km.label(scGraceAcc), e.Grace == score.Acciaccatura, true) {
		scoreRunCmd(scGraceAcc)
	}
	if imgui.MenuItemV("Grace Notes (Appoggiatura)", km.label(scGraceApp), e.Grace == score.Appoggiatura, true) {
		scoreRunCmd(scGraceApp)
	}
	imgui.Separator()
	item("Lyrics", scLyrics)
	item("Lyrics, Verse 2", scLyrics2)
	item("Chord Symbol", scChordSymbol)
	if imgui.MenuItemV("Clear Lyrics in the Selection", "", false, e.Sel.Kind == scoreSelBars) {
		e.clearLyrics()
	}
	if imgui.BeginMenu("Open / Stopped") {
		for _, m := range []struct{ name, label string }{{"open", "Open (o)"}, {"stopped", "Stopped / Closed (+)"}} {
			if imgui.MenuItem(m.label + "##scmos") {
				e.toggleMark(m.name, "")
			}
		}
		imgui.EndMenu()
	}
	editorMenuTooltip("A horn's open and stopped notes; on a drum kit an \"o\" plays the open hi-hat")
	imgui.Separator()
	item("Tempo...", scTempoText)
	item("Rehearsal Mark...", scRehearsal)
	item("Repeat...", scRepeatBars)
	item("Ending...", scEnding)
	if imgui.BeginMenu("Segno, Coda, D.C., D.S., Fine") {
		for _, j := range scoreJumps {
			if imgui.MenuItem(j.Label + "##scmj") {
				e.setJump(j.Kind, j.Al)
			}
		}
		imgui.EndMenu()
	}
	item("Clef Change...", scClefChange)
	imgui.Separator()
	if imgui.BeginMenu("Remove") {
		if imgui.MenuItem("Dynamics") {
			e.dynamic("")
		}
		if imgui.MenuItem("Text") {
			e.removeText()
		}
		for _, k := range []struct {
			label string
			kind  edit.SpanKind
		}{{"Slurs", edit.SpanSlur}, {"Hairpins", edit.SpanHairpin}, {"Pedal Lines", edit.SpanPedal}, {"Ottava Lines", edit.SpanOttava}} {
			if imgui.MenuItem(k.label) {
				e.removeSpans(k.kind)
			}
		}
		if imgui.MenuItem("Segno, Coda, D.C., D.S., Fine") {
			e.removeJumps()
		}
		if imgui.MenuItem("Dynamics Overrides") {
			e.laneClearSel()
		}
		imgui.EndMenu()
	}
	imgui.Separator()
	if imgui.MenuItemV("Marks Palette", km.label(scMarksPanel), scoreShowMarks, true) {
		scoreRunCmd(scMarksPanel)
	}
	if imgui.MenuItemV("Dynamics Lane", km.label(scLane), e.laneShown(e.selLine()), e.Sel.Kind != scoreSelNone) {
		scoreRunCmd(scLane)
	}
	imgui.EndMenu()
}
