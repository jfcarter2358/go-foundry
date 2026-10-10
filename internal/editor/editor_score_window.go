package editor

// editor_score_window.go — the Score editor, in its own OS window.
//
// The notation editor beside the Tracker (docs/score/SPEC.md). Like the
// Tracker it owns a document that outlives its window (scEd, a *scoreState
// over a *score.Score) and keeps one long-lived player on the music bus while
// the window is up: here a score scheduler (internal/score/play), handed a
// fresh clone of the score whenever it has changed and something wants to
// hear it, so a click on a note, a note typed or Space plays what is on the
// page. The player never holds the score being edited.
//
// Layout:
//
//	┌──────────────────────────────────────────────────────────────────┐
//	│ File Edit View Notes Play Window │ Level … Score       name [Saved]│
//	│ Note input │ 64 32 16 8 4 2 1 . rest │ # b n │ Voice 1 2 3 4 │ …    │
//	├────────┬─────────────────────────────────────────────────────────┤
//	│ lines  │ bar ruler                                               │
//	│        │ the score, engraved on paper, one continuous system     │
//	│        │                                         ┌─keypad─┐      │
//	│        │                                         └────────┘      │
//	├────────┴─────────────────────────────────────────────────────────┤
//	│ mode · key · meter · duration · voice · selection · clipboard     │
//	└──────────────────────────────────────────────────────────────────┘
//
// The view is editor_score_view.go, the keypad editor_score_keypad.go, the
// keys editor_score_input.go, the F1 overlay and the shortcut settings
// editor_score_help.go. What every command does to the music is
// editor_score_edit.go.

import (
	"fmt"
	"hash/fnv"
	"log"
	"strings"

	"foundry/internal/audio"
	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/engrave"
	"foundry/internal/score/play"
	"foundry/internal/score/scoredraw"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	scoreWindowW = int32(1400)
	scoreWindowH = int32(900)
	// scoreStatusH is the status bar along the bottom.
	scoreStatusH = float32(26)
)

// scoreEditorWindow is the pop-out; nil when the editor is closed.
var scoreEditorWindow *uiwin.UIWindow

// scEd is the score being edited. It outlives the window.
var scEd *scoreState

// scoreSaved tracks the score against its last save.
var scoreSaved savedState

// The editor's voice on the mixer.
var (
	scorePlayer    *play.Player
	scoreVoice     audio.VoiceID
	scorePlayerRev uint64
	scorePlayerSet bool
)

// scoreRend draws notation (the glyph atlas's textures live in it).
var scoreRend *scoredraw.Renderer

// View state, never saved with the score.
var (
	// scoreZoom is pixels per staff space.
	scoreZoom         = float32(10)
	scoreVoiceColours = true
	scoreShowKeypad   bool
	scoreKeypadSet    bool
)

// Dialog state.
var (
	scoreShowOpen   bool
	scoreShowSaveAs bool
	scoreShowKey    bool
	scoreShowTime   bool
	scoreShowClef   bool
	scoreShowKeys   bool
	scoreSaveAsName string
	scoreNameList   []string
)

// ---------- lifecycle ----------

// ScoreEditorOpen reports whether the window is up.
func ScoreEditorOpen() bool { return scoreEditorWindow != nil }

// OpenScoreEditorWindow opens the editor, or raises it. With nothing loaded
// it starts on the Solo line template.
func OpenScoreEditorWindow() {
	if scoreEditorWindow != nil {
		scoreEditorWindow.Win.Raise()
		return
	}
	scoreEnsureDoc()
	w, err := uiwin.NewUIWindow("Score", scoreWindowW, scoreWindowH, drawScoreEditorWindow, onScoreEditorClose)
	if err != nil {
		log.Printf("open score window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.09, 0.10, 0.12, 1}
	w.HandleEvent = scoreEditorHandleEvent
	w.HelpOverlay = scoreHelpOverlay
	if !scoreKeypadSet {
		scoreShowKeypad, scoreKeypadSet = scoreKeypadWanted(), true
	}
	// Assigned last: until this is set, ScoreEditorOpen has to say no.
	scoreEditorWindow = w
	scoreEnsurePlayer()
}

// OpenScoreEditorOn opens the editor on a named score.
func OpenScoreEditorOn(name string) {
	if name == "" {
		return
	}
	uiwin.RequestWindowOpen(func() {
		if err := scoreLoad(name); err != nil {
			editorSetStatus("Could not open %s: %v", name, err)
			return
		}
		OpenScoreEditorWindow()
	})
}

// CloseScoreEditorWindow asks for the window to go at the end of the frame.
func CloseScoreEditorWindow() {
	if scoreEditorWindow != nil {
		scoreEditorWindow.CloseRequested = true
	}
}

func onScoreEditorClose() {
	scoreStopPlayer()
	scoreEditorWindow = nil
	scoreCapture = scoreCaptureState{}
}

// scoreEnsureDoc guarantees there is something to edit.
func scoreEnsureDoc() {
	if scEd == nil {
		scEd = newScoreState(scoreTemplates[0].Build())
		scoreMarkSaved()
	}
}

// ---------- the player ----------

// scoreKeepAlive keeps the editor's voice alive past the score's end:
// auditions and the next Play go through the same player.
type scoreKeepAlive struct{ p *play.Player }

func (k scoreKeepAlive) Render(dst []float32, frames, rate int) bool {
	k.p.Render(dst, frames, rate)
	return true
}

// scoreEnsurePlayer starts the editor's voice if it is not running.
func scoreEnsurePlayer() {
	if scorePlayer != nil || scEd == nil {
		return
	}
	p, errs := play.New(scEd.Score.Clone())
	if p == nil {
		if len(errs) > 0 {
			scEd.Status = "Playback: " + errs[len(errs)-1].Error()
		}
		return
	}
	scorePlayer = p
	scorePlayerRev, scorePlayerSet = scEd.Rev, true
	scoreVoice = audio.PlayGenerator(scoreKeepAlive{p}, audio.PlayOptions{Bus: audio.BusMusic, Priority: audio.PriorityHigh})
}

func scoreStopPlayer() {
	if scoreVoice != 0 {
		audio.Stop(scoreVoice)
	}
	scorePlayer, scoreVoice, scorePlayerSet = nil, 0, false
}

// scoreSyncPlayer hands the player the score as it stands, when it changed.
func scoreSyncPlayer(force bool) {
	if scorePlayer == nil {
		return
	}
	if !force && scorePlayerSet && scorePlayerRev == scEd.Rev {
		return
	}
	if _, err := scorePlayer.SetScore(scEd.Score.Clone()); err != nil {
		scEd.Status = "Playback: " + err.Error()
	}
	scorePlayerRev, scorePlayerSet = scEd.Rev, true
}

// scorePlayAuditions plays what the last commands asked to be heard.
func scorePlayAuditions() {
	as := scEd.takeAuditions()
	if len(as) == 0 || scorePlayer == nil {
		return
	}
	scoreSyncPlayer(false)
	for _, a := range as {
		for _, m := range a.MIDI {
			scorePlayer.NoteFor(a.Line, m, 0.7, 0.6)
		}
	}
}

// scorePlaying reports the transport running.
func scorePlaying() bool { return scorePlayer != nil && scorePlayer.Position().Playing }

// scorePlayToggle is Space: play from the selection (or the top), or stop.
func scorePlayToggle() {
	if scorePlaying() {
		scorePlayer.StopTransport()
		return
	}
	scorePlayFromSelection()
}

// scorePlayFromSelection is P: play from the selected note or bar.
func scorePlayFromSelection() {
	scoreEnsurePlayer()
	if scorePlayer == nil {
		return
	}
	scoreSyncPlayer(false)
	bar, at := scEd.selBar(), scEd.selOnset()
	perf := scorePlayer.Performance()
	tick := int64(0)
	if perf != nil {
		if t := perf.BarTick(bar); t >= 0 {
			tick = t + at.Ticks()
		}
	}
	scorePlayer.PlayFrom(tick)
}

// scorePlayhead is where the transport is: a written bar and the fraction of
// it played, while playing.
func scorePlayhead() (bar int, frac float64, ok bool) {
	if !scorePlaying() {
		return 0, 0, false
	}
	pos := scorePlayer.Position()
	perf := scorePlayer.Performance()
	if perf == nil || len(perf.Bars) == 0 {
		return 0, 0, false
	}
	b := perf.Bars[perf.BarAt(float64(pos.Tick))]
	if b.Ticks > 0 {
		frac = float64(pos.Tick-b.Tick) / float64(b.Ticks)
	}
	return b.Bar, max(0, min(frac, 1)), true
}

// ---------- the document ----------

// scoreFingerprint is a hash of the score as its file would store it.
func scoreFingerprint(s *score.Score) uint64 {
	b, err := score.Marshal(s)
	if err != nil {
		return 0
	}
	h := fnv.New64a()
	h.Write(b)
	return h.Sum64()
}

func scoreKey() any { return docKey{scEd.Score, scEd.Rev} }

func scoreMarkSaved() {
	if scEd != nil {
		scoreSaved.baseline(scoreKey(), scoreFingerprint(scEd.Score))
	}
}

func scoreEditorDirty() bool {
	if scEd == nil {
		return false
	}
	scoreSaved.refresh(scoreKey(), 200, func() uint64 { return scoreFingerprint(scEd.Score) })
	return scoreSaved.dirty()
}

// scoreDocName is the score's library name, or "(unsaved)".
func scoreDocName() string {
	if scEd == nil || scEd.Name == "" {
		return "(unsaved)"
	}
	return scEd.Name
}

// scoreNew replaces the score with a template.
func scoreNew(t scoreTemplate) {
	scEd.setScore(t.Build())
	scEd.Name = ""
	if scorePlayer != nil {
		scorePlayer.StopTransport()
	}
	scoreSyncPlayer(true)
	scoreMarkSaved()
	scEd.Status = "New score from " + t.Name
}

// scoreLoad opens a score by library name.
func scoreLoad(name string) error {
	name = strings.TrimSuffix(name, score.FileExt)
	path := score.ScorePath(name)
	if path == "" {
		return fmt.Errorf("no score %q", name)
	}
	s, err := score.LoadFile(path)
	if err != nil {
		return err
	}
	scoreEnsureDoc()
	scEd.setScore(s)
	scEd.Name = name
	if scorePlayer != nil {
		scorePlayer.StopTransport()
	}
	scoreSyncPlayer(true)
	scoreMarkSaved()
	scoreNoteRecent(name)
	scEd.Status = "Opened " + name
	return nil
}

// scoreSaveAs writes the score as sounds/<name>.fscore and drops it from
// the library's cache, so a level playing it picks the edit up.
func scoreSaveAs(name string) error {
	name = strings.TrimSuffix(strings.TrimSpace(name), score.FileExt)
	if name == "" {
		return fmt.Errorf("no name")
	}
	s := scEd.Score
	if s.Title == "" || s.Title == "Untitled" {
		s.Title = name[strings.LastIndex(name, "/")+1:]
	}
	s.Name = name
	if err := score.Save(name, s); err != nil {
		return err
	}
	scEd.Name = name
	scoreNameList = nil
	scoreMarkSaved()
	scoreNoteRecent(name)
	scEd.Status = "Saved " + name + score.FileExt
	return nil
}

// scoreSaveCurrent saves over the open score, or asks for a name.
func scoreSaveCurrent() {
	if scEd == nil {
		return
	}
	if scEd.Name == "" {
		scoreSaveAsName = ""
		scoreShowSaveAs = true
		return
	}
	if err := scoreSaveAs(scEd.Name); err != nil {
		scEd.Status = "Save failed: " + err.Error()
	}
}

func scoreUndo() {
	if scEd.undoStep() {
		scoreAfterStructural()
	}
}

func scoreRedo() {
	if scEd.redoStep() {
		scoreAfterStructural()
	}
}

// scoreAfterStructural drops a selection the score no longer has.
func scoreAfterStructural() { scEd.dropStaleSel() }

// ---------- commands ----------

// scoreRunCmd does a command: from a key, a menu, the toolbar or the keypad.
func scoreRunCmd(c scoreCmd) {
	e := scEd
	switch {
	case c >= scLetterC && c < scLetterC+7:
		e.letter(int(c - scLetterC))
		return
	case c >= scAddC && c < scAddC+7:
		e.addLetter(int(c - scAddC))
		return
	case c >= scInt2 && c < scInt2+8:
		e.interval(int(c-scInt2)+2, false)
		return
	case c >= scBelow2 && c < scBelow2+8:
		e.interval(int(c-scBelow2)+2, true)
		return
	case c >= scKeypad1 && c < scKeypad1+6:
		e.keypad(int(c-scKeypad1) + 1)
		return
	case c >= scDur256 && c < scDur256+scoreCmd(len(scoreDirectDurs)):
		e.setDur(scoreDirectDurs[c-scDur256].base)
		return
	case c >= scVoice1 && c < scVoice1+4:
		e.setVoice(int(c - scVoice1))
		return
	case c >= scTuplet2 && c < scTuplet2+8:
		e.tuplet(int(c-scTuplet2) + 2)
		return
	}
	switch c {
	case scNoteInput:
		e.toggleInput()
	case scEscape:
		e.escape()
	case scSharp:
		e.accidental(edit.Sharp)
	case scFlat:
		e.accidental(edit.Flat)
	case scNatural:
		e.accidental(edit.Natural)
	case scSemiUp:
		e.semitone(1)
	case scSemiDown:
		e.semitone(-1)
	case scStepUp:
		e.step(1)
	case scStepDown:
		e.step(-1)
	case scOctUp:
		e.octave(1)
	case scOctDown:
		e.octave(-1)
	case scRespell:
		e.respell()
	case scLayout:
		e.Layout2 = !e.Layout2
		if e.Layout2 {
			e.Status = "Keypad: second layout (1 64th, 2 128th, 3 256th, 4 breve)"
		} else {
			e.Status = "Keypad: first layout"
		}
	case scDot:
		e.toggleDot()
	case scRest:
		e.rest()
	case scTie:
		e.tie()
	case scFlip:
		if e.chord() == nil {
			e.Status = "Select a note to flip"
			break
		}
		up, ok := scoreStemUp(scoreLayout(), e.Sel.Loc)
		if !ok {
			e.Status = "That note has no stem"
			break
		}
		e.flip(up)
	case scVoiceCycle:
		e.setVoice((e.Voice + 1) % score.MaxVoices)
	case scDelete:
		e.deleteSel()
	case scPrev:
		e.moveSel(-1)
	case scNext:
		e.moveSel(1)
	case scCopy:
		e.copySel()
	case scPaste:
		e.paste()
	case scRepeat:
		e.repeat()
	case scSelectAll:
		e.selectAll()
	case scUndo:
		scoreUndo()
	case scRedo:
		scoreRedo()
	case scKeySig:
		scoreOpenKeyDialog()
	case scTimeSig:
		scoreOpenTimeDialog()
	case scClef:
		scoreOpenClefDialog()
	case scPlay:
		scorePlayToggle()
	case scPlayFrom:
		scorePlayFromSelection()
	case scSave:
		scoreSaveCurrent()
	case scSaveAs:
		scoreSaveAsName = scEd.Name
		scoreShowSaveAs = true
	case scOpen:
		scoreShowOpen, scoreNameList = true, nil
	case scZoomIn:
		scoreSetZoom(scoreZoom * 1.25)
	case scZoomOut:
		scoreSetZoom(scoreZoom / 1.25)
	case scZoomReset:
		scoreSetZoom(10)
	case scKeypadPanel:
		scoreShowKeypad = !scoreShowKeypad
		scoreSetKeypadWanted(scoreShowKeypad)
	}
}

// scoreSetZoom sets pixels per staff space, within reason.
func scoreSetZoom(z float32) { scoreZoom = clampF32(z, 4, 40) }

// ---------- the frame ----------

func drawScoreEditorWindow() {
	w := scoreEditorWindow
	if w == nil {
		return
	}
	scoreEnsureDoc()
	scoreEnsurePlayer()
	if scoreRend == nil {
		scoreRend = scoredraw.New()
	}
	W, H := float32(w.W), float32(w.H)
	scoreToolbar(W)
	scoreViewPane(imgui.Vec2{X: 0, Y: editorTopBarH}, imgui.Vec2{X: W, Y: H - editorTopBarH - scoreStatusH})
	scoreStatusBar(imgui.Vec2{X: 0, Y: H - scoreStatusH}, W)
	if scoreShowKeypad {
		scoreKeypadPanel(W, H)
	}
	scoreDialogs()
	scorePlayAuditions()
	if scorePlaying() {
		scoreSyncPlayer(false)
	}
}

// ---------- the top bar ----------

var (
	scoreColActive = imgui.Vec4{X: 0.20, Y: 0.40, Z: 0.66, W: 1}
	scoreColInput  = imgui.Vec4{X: 0.20, Y: 0.48, Z: 0.30, W: 1}
)

// scoreToolbar is the shared two-row top bar: menus, then the note tools.
func scoreToolbar(w float32) {
	editorTopBarBegin("##scoretoolbar", w)
	if imgui.BeginMenuBar() {
		scoreMenus()
		editorMenuBarShared(editorKindScore)
		editorMenuBarDoc(scoreDocName(), scoreEditorDirty())
		imgui.EndMenuBar()
	}
	scoreToolRow()
	imgui.End()
}

// scoreToolRow is row two: note input, durations, accidentals, voices, the
// signatures, and the status message.
func scoreToolRow() {
	e := scEd
	if editorActiveToggleButtonColor("Note input##scin", e.Input, scoreColInput) {
		scoreRunCmd(scNoteInput)
	}
	editorMenuTooltip("Note input (" + scoreKeymapActive().label(scNoteInput) + "): a click on a line or space writes\nthe note the key implies there; A-G write after the selection.")
	editorToolbarGroupSep()
	cur, _, _ := e.shownDur()
	for i := len(scoreDirectDurs) - 1; i >= 0; i-- {
		d := scoreDirectDurs[i]
		if d.base == score.D128 || d.base == score.D256 {
			if !e.Layout2 {
				continue
			}
		}
		imgui.SameLine()
		if scoreGlyphButton(fmt.Sprintf("##scdur%d", i), scoreDurIcon(d.base), cur.Base == d.base) {
			scoreRunCmd(scDur256 + scoreCmd(i))
		}
		editorMenuTooltip(scoreDurLabel(d.base) + scoreKeysSuffix(scDur256+scoreCmd(i)))
	}
	imgui.SameLine()
	if scoreGlyphButton("##scdot", "augmentationDot", cur.Dots > 0) {
		scoreRunCmd(scDot)
	}
	editorMenuTooltip("Dot, again for a double dot" + scoreKeysSuffix(scDot))
	imgui.SameLine()
	if scoreGlyphButton("##screst", "restQuarter", false) {
		scoreRunCmd(scRest)
	}
	editorMenuTooltip("Rest: in note input, a rest after the selection; otherwise the\nselected note becomes a rest" + scoreKeysSuffix(scRest))
	editorToolbarGroupSep()
	acc := scoreCurrentAcc()
	for i, g := range []string{"accidentalSharp", "accidentalFlat", "accidentalNatural"} {
		imgui.SameLine()
		if scoreGlyphButton(fmt.Sprintf("##scacc%d", i), g, acc == i) {
			scoreRunCmd(scSharp + scoreCmd(i))
		}
		editorMenuTooltip([]string{"Sharp (again: double sharp)", "Flat (again: double flat)", "Natural"}[i] + scoreKeysSuffix(scSharp+scoreCmd(i)))
	}
	editorToolbarGroupSep()
	imgui.SameLine()
	editorTextMuted("Voice")
	for v := 0; v < score.MaxVoices; v++ {
		imgui.SameLine()
		imgui.PushStyleColor(imgui.StyleColorText, scoreVoiceUICol[v])
		if editorActiveToggleButtonColor(fmt.Sprintf("%d##scv%d", v+1, v), e.Voice == v, scoreColActive) {
			scoreRunCmd(scVoice1 + scoreCmd(v))
		}
		imgui.PopStyleColor()
		editorMenuTooltip(fmt.Sprintf("Voice %d%s", v+1, scoreKeysSuffix(scVoice1+scoreCmd(v))))
	}
	editorToolbarGroupSep()
	imgui.SameLine()
	if imgui.Button("3##sctrip") {
		scoreRunCmd(scTuplet2 + 1)
	}
	editorMenuTooltip("Triplet on the selected note" + scoreKeysSuffix(scTuplet2+1) + "; Ctrl+2-9 for others")
	imgui.SameLine()
	if imgui.Button("Tie##sctie") {
		scoreRunCmd(scTie)
	}
	editorMenuTooltip("Tie the selected note into the next" + scoreKeysSuffix(scTie))
	editorToolbarGroupSep()
	imgui.SameLine()
	if imgui.Button("Key##sckey") {
		scoreRunCmd(scKeySig)
	}
	editorMenuTooltip("Key signature at the selection" + scoreKeysSuffix(scKeySig))
	imgui.SameLine()
	if imgui.Button("Time##sctime") {
		scoreRunCmd(scTimeSig)
	}
	editorMenuTooltip("Time signature at the selection" + scoreKeysSuffix(scTimeSig))
	imgui.SameLine()
	if imgui.Button("Clef##scclef") {
		scoreRunCmd(scClef)
	}
	editorMenuTooltip("A staff's clef" + scoreKeysSuffix(scClef))
	editorToolbarGroupSep()
	imgui.SameLine()
	if editorActiveToggleButtonColor("Play##scplay", scorePlaying(), scoreColInput) {
		scoreRunCmd(scPlay)
	}
	editorMenuTooltip("Play from the selection, or stop" + scoreKeysSuffix(scPlay))
	imgui.SameLine()
	if editorActiveToggleButtonColor("Keypad##sckp", scoreShowKeypad, scoreColActive) {
		scoreRunCmd(scKeypadPanel)
	}
	editorMenuTooltip("The on-screen keypad" + scoreKeysSuffix(scKeypadPanel))
	editorStatusText(e.Status)
}

// scoreKeysSuffix is " (keys)" for a tooltip, or "" for an unbound command.
func scoreKeysSuffix(c scoreCmd) string {
	if l := scoreKeymapActive().label(c); l != "" {
		return " (" + l + ")"
	}
	return ""
}

// scoreCurrentAcc is the accidental to light: the armed one, or the
// selected note's (0 sharp, 1 flat, 2 natural), -1 for none.
func scoreCurrentAcc() int {
	e := scEd
	if e.Armed {
		return int(e.ArmedAcc)
	}
	ev := e.chord()
	if ev == nil {
		return -1
	}
	k := e.Sel.Note
	if k < 0 || k >= len(ev.Notes) {
		k = len(ev.Notes) - 1
	}
	l := e.Sel.Loc
	p := e.toWritten(l.Line, l.Staff, ev.Notes[k].Pitch)
	switch {
	case p.Alter > 0:
		return 0
	case p.Alter < 0:
		return 1
	case score.KeyAlter(e.writtenFifths(l.Line, l.Staff, l.Bar), int(p.Step)) != 0:
		return 2
	}
	return -1
}

// scoreDurIcon is a value's toolbar glyph.
func scoreDurIcon(b score.DurBase) string {
	switch b {
	case score.Breve:
		return "metNoteDoubleWhole"
	case score.WholeNote:
		return "metNoteWhole"
	case score.Half:
		return "metNoteHalfUp"
	case score.Quarter:
		return "metNoteQuarterUp"
	case score.Eighth:
		return "metNote8thUp"
	case score.D16:
		return "metNote16thUp"
	case score.D32:
		return "metNote32ndUp"
	case score.D64:
		return "metNote64thUp"
	case score.D128:
		return "metNote128thUp"
	}
	return "metNote256thUp"
}

// ---------- menus ----------

func scoreMenus() {
	e := scEd
	km := scoreKeymapActive()
	item := func(label string, c scoreCmd, enabled bool) {
		if imgui.MenuItemV(label, km.label(c), false, enabled) {
			scoreRunCmd(c)
		}
	}
	if imgui.BeginMenu("File") {
		if imgui.BeginMenu("New") {
			for _, t := range scoreTemplates {
				if imgui.MenuItem(t.Name + "##scnew") {
					scoreNew(t)
				}
				editorMenuTooltip(t.Desc)
			}
			imgui.EndMenu()
		}
		item("Open...", scOpen, true)
		recent := scoreSettings().Recent
		if imgui.BeginMenuV("Open Recent", len(recent) > 0) {
			for i, name := range recent {
				if imgui.MenuItem(fmt.Sprintf("%s##screcent%d", name, i)) {
					if err := scoreLoad(name); err != nil {
						e.Status = err.Error()
					}
				}
			}
			imgui.EndMenu()
		}
		imgui.Separator()
		item("Save", scSave, true)
		item("Save As...", scSaveAs, true)
		editorSaveAllMenuItem()
		editorCloseWindowItem(CloseScoreEditorWindow)
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Edit") {
		if imgui.MenuItemV("Undo", km.label(scUndo), false, e.canUndo()) {
			scoreUndo()
		}
		if imgui.MenuItemV("Redo", km.label(scRedo), false, e.canRedo()) {
			scoreRedo()
		}
		imgui.Separator()
		item("Copy", scCopy, e.Sel.Kind != scoreSelNone)
		item("Paste", scPaste, e.Clip != nil || e.ClipEvent != nil)
		item("Repeat", scRepeat, e.Sel.Kind != scoreSelNone)
		item("Delete", scDelete, e.Sel.Kind != scoreSelNone)
		imgui.Separator()
		item("Select All", scSelectAll, true)
		if imgui.MenuItemV("Select None", km.label(scEscape), false, e.Sel.Kind != scoreSelNone) {
			e.Sel = scoreSel{}
		}
		imgui.Separator()
		if imgui.MenuItem("Keyboard Shortcuts...") {
			scoreShowKeys = true
		}
		editorMenuTooltip("Choose the Sibelius or MuseScore keymap and rebind any command.")
		imgui.EndMenu()
	}
	if imgui.BeginMenu("View") {
		item("Zoom In", scZoomIn, true)
		item("Zoom Out", scZoomOut, true)
		item("Actual Size", scZoomReset, true)
		imgui.Separator()
		if imgui.MenuItemV("Concert Pitch", "", e.Concert, true) {
			e.Concert = !e.Concert
			e.touch()
		}
		editorMenuTooltip("Show transposing instruments at sounding pitch (entry follows).")
		editorMenuToggle("Voice Colours", "", &scoreVoiceColours)
		if imgui.MenuItemV("Keypad", km.label(scKeypadPanel), scoreShowKeypad, true) {
			scoreRunCmd(scKeypadPanel)
		}
		if imgui.MenuItemV("Keymap Overlay", "F1", uiwin.KeybindHelpVisible, true) {
			uiwin.KeybindHelpToggle()
		}
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Notes") {
		if imgui.MenuItemV("Note Input", km.label(scNoteInput), e.Input, true) {
			scoreRunCmd(scNoteInput)
		}
		imgui.Separator()
		item("Key Signature...", scKeySig, true)
		item("Time Signature...", scTimeSig, true)
		item("Clef...", scClef, len(e.Score.Lines) > 0)
		imgui.Separator()
		if imgui.BeginMenu("Tuplet") {
			for n := 2; n <= 9; n++ {
				item(fmt.Sprintf("%d##sctup", n), scTuplet2+scoreCmd(n-2), e.event() != nil)
			}
			imgui.EndMenu()
		}
		if imgui.BeginMenu("Voice") {
			for v := 0; v < score.MaxVoices; v++ {
				if imgui.MenuItemV(fmt.Sprintf("Voice %d", v+1), km.label(scVoice1+scoreCmd(v)), e.Voice == v, true) {
					scoreRunCmd(scVoice1 + scoreCmd(v))
				}
			}
			imgui.EndMenu()
		}
		imgui.Separator()
		item("Tie", scTie, e.chord() != nil)
		item("Make Rest", scRest, e.chord() != nil || e.Input)
		item("Respell", scRespell, e.chord() != nil)
		item("Flip Stem", scFlip, e.chord() != nil)
		if imgui.MenuItemV("Second Keypad Layout", km.label(scLayout), e.Layout2, true) {
			scoreRunCmd(scLayout)
		}
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Play") {
		if imgui.MenuItemV("Play / Stop", km.label(scPlay), scorePlaying(), true) {
			scoreRunCmd(scPlay)
		}
		item("Play from Selection", scPlayFrom, true)
		imgui.EndMenu()
	}
}

// ---------- dialogs ----------

// Key, time and clef dialog fields.
var (
	scoreDlgBar    int32
	scoreDlgFifths int
	scoreDlgMinor  bool
	scoreDlgMeter  string
	scoreDlgLine   int
	scoreDlgStaff  int
	scoreDlgClef   string
	scoreDlgErr    string
)

// scoreSelBar is the bar a signature change goes at: the selection's first.
func scoreSelBar() int { return scEd.selBar() }

func scoreOpenKeyDialog() {
	b := scoreSelBar()
	k := scEd.Score.KeyAt(b)
	scoreDlgBar, scoreDlgFifths, scoreDlgMinor = int32(b), k.Fifths, k.Mode == "minor"
	scoreShowKey = true
}

func scoreOpenTimeDialog() {
	b := scoreSelBar()
	scoreDlgBar, scoreDlgMeter, scoreDlgErr = int32(b), scEd.Score.MeterAt(b).String(), ""
	scoreShowTime = true
}

func scoreOpenClefDialog() {
	if len(scEd.Score.Lines) == 0 {
		return
	}
	scoreDlgLine, scoreDlgStaff = 0, 0
	switch scEd.Sel.Kind {
	case scoreSelEvent, scoreSelCaret:
		scoreDlgLine, scoreDlgStaff = scEd.Sel.Loc.Line, scEd.Sel.Loc.Staff
	case scoreSelBars:
		scoreDlgLine = scEd.Sel.LineA
	}
	if st := edit.Staff(scEd.Score, scoreDlgLine, scoreDlgStaff); st != nil {
		scoreDlgClef = st.Clef
	}
	scoreShowClef = true
}

// scoreMeterPresets are the time dialog's one-click meters.
var scoreMeterPresets = []string{"4/4", "3/4", "2/4", "2/2", "6/8", "9/8", "12/8", "5/4", "5/8", "7/8", "3+2+2/8", "C", "C|"}

func scoreDialogs() {
	e := scEd
	imguibackend.WithModalPopup("Open Score##scopen", &scoreShowOpen, func() {
		if scoreNameList == nil {
			scoreNameList = score.ScanScoreNames()
			if scoreNameList == nil {
				scoreNameList = []string{}
			}
		}
		if len(scoreNameList) == 0 {
			editorTextMuted("No scores under sounds/ yet.")
		}
		imgui.BeginChildV("##scopenlist", imgui.Vec2{X: 360, Y: 300}, true, 0)
		for _, name := range scoreNameList {
			if !imguibackend.Selectable(name + "##scopen") {
				continue
			}
			if err := scoreLoad(name); err != nil {
				e.Status = err.Error()
			}
			imgui.CloseCurrentPopup()
		}
		imgui.EndChild()
		if imgui.Button("Cancel##scopencancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Save Score As##scsaveas", &scoreShowSaveAs, func() {
		imgui.SetNextItemWidth(320)
		enter := imgui.InputTextV("Name##scsavename", &scoreSaveAsName, imgui.InputTextFlagsEnterReturnsTrue, nil)
		editorTextMuted("sounds/<name>" + score.FileExt)
		if (imgui.Button("Save##scsavego") || enter) && strings.TrimSpace(scoreSaveAsName) != "" {
			if err := scoreSaveAs(scoreSaveAsName); err != nil {
				e.Status = "Save failed: " + err.Error()
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##scsavecancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Key Signature##sckeydlg", &scoreShowKey, func() {
		imgui.SetNextItemWidth(100)
		imgui.InputInt("At bar##sckeybar", &scoreDlgBar)
		scoreDlgBar = int32(clampI(int(scoreDlgBar), 1, e.Score.Measures()))
		mode := "major"
		if scoreDlgMinor {
			mode = "minor"
		}
		imgui.SetNextItemWidth(200)
		if imguibackend.BeginCombo("Key##sckeycombo", scoreKeyName(scoreDlgFifths, mode)) {
			for f := -7; f <= 7; f++ {
				label := fmt.Sprintf("%s  (%s)##sckf%d", scoreKeyName(f, mode), scoreFifthsText(f), f)
				if imguibackend.SelectableV(label, f == scoreDlgFifths, 0, imgui.Vec2{}) {
					scoreDlgFifths = f
				}
			}
			imguibackend.EndCombo()
		}
		if imgui.RadioButton("Major##sckmaj", !scoreDlgMinor) {
			scoreDlgMinor = false
		}
		imgui.SameLine()
		if imgui.RadioButton("Minor##sckmin", scoreDlgMinor) {
			scoreDlgMinor = true
		}
		editorTextMuted("Notes keep their pitches; the page prints what the new key needs.")
		if imgui.Button("OK##sckeyok") {
			e.setKey(int(scoreDlgBar), scoreDlgFifths, mode)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##sckeycancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Time Signature##sctimedlg", &scoreShowTime, func() {
		imgui.SetNextItemWidth(100)
		imgui.InputInt("At bar##sctimebar", &scoreDlgBar)
		scoreDlgBar = int32(clampI(int(scoreDlgBar), 1, e.Score.Measures()))
		for i, p := range scoreMeterPresets {
			if i > 0 && i%7 != 0 {
				imgui.SameLine()
			}
			if editorActiveToggleButtonColor(p+"##scmp", scoreDlgMeter == p, scoreColActive) {
				scoreDlgMeter = p
			}
		}
		imgui.SetNextItemWidth(160)
		imgui.InputText("Meter##sctimetext", &scoreDlgMeter)
		editorTextMuted("Any meter: 7/8, 3+2+2/8, 12/16, C, C|. The bars up to the next time\nsignature take the new length: music past a new barline is cut.")
		if scoreDlgErr != "" {
			imgui.PushStyleColor(imgui.StyleColorText, saveChipUnsaved)
			imgui.Text(scoreDlgErr)
			imgui.PopStyleColor()
		}
		if imgui.Button("OK##sctimeok") {
			m, err := score.ParseMeter(scoreDlgMeter)
			if err != nil {
				scoreDlgErr = err.Error()
			} else {
				e.setMeter(int(scoreDlgBar), m)
				imgui.CloseCurrentPopup()
			}
		}
		imgui.SameLine()
		if imgui.Button("Cancel##sctimecancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Clef##scclefdlg", &scoreShowClef, func() {
		imgui.SetNextItemWidth(240)
		if imguibackend.BeginCombo("Staff##scclefstaff", scoreStaffName(scoreDlgLine, scoreDlgStaff)) {
			for li, l := range e.Score.Lines {
				for si := range l.Staves {
					if imguibackend.SelectableV(scoreStaffName(li, si)+fmt.Sprintf("##scst%d_%d", li, si), li == scoreDlgLine && si == scoreDlgStaff, 0, imgui.Vec2{}) {
						scoreDlgLine, scoreDlgStaff = li, si
						scoreDlgClef = l.Staves[si].Clef
					}
				}
			}
			imguibackend.EndCombo()
		}
		imgui.SetNextItemWidth(240)
		if imguibackend.BeginCombo("Clef##scclefcombo", scoreDlgClef) {
			for _, c := range score.StaffClefs() {
				if imguibackend.SelectableV(c+"##sccl", c == scoreDlgClef, 0, imgui.Vec2{}) {
					scoreDlgClef = c
				}
			}
			imguibackend.EndCombo()
		}
		editorTextMuted("The whole staff takes the clef; its notes keep their pitches.")
		if imgui.Button("OK##scclefok") {
			e.setClef(scoreDlgLine, scoreDlgStaff, scoreDlgClef)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##scclefcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	scoreKeysDialog()
}

// scoreStaffName is "Piano (staff 2)", or the line's name for one staff.
func scoreStaffName(line, staff int) string {
	name := scEd.lineName(line)
	if line >= 0 && line < len(scEd.Score.Lines) && len(scEd.Score.Lines[line].Staves) > 1 {
		return fmt.Sprintf("%s (staff %d)", name, staff+1)
	}
	return name
}

// scoreFifthsText is "2 sharps", "no sharps or flats".
func scoreFifthsText(f int) string {
	switch {
	case f == 0:
		return "no sharps or flats"
	case f == 1:
		return "1 sharp"
	case f == -1:
		return "1 flat"
	case f > 0:
		return fmt.Sprintf("%d sharps", f)
	}
	return fmt.Sprintf("%d flats", -f)
}

// ---------- engraving ----------

// The view's engraving, rebuilt when the score or concert pitch changes.
var (
	scoreList      *engrave.List
	scoreListRev   uint64
	scoreListScore *score.Score
	scoreListConc  bool
)

// scoreLayout is the score engraved as one continuous system.
func scoreLayout() *engrave.List {
	e := scEd
	if scoreList == nil || scoreListRev != e.Rev || scoreListScore != e.Score || scoreListConc != e.Concert {
		scoreList = engrave.Engrave(e.Score, engrave.Options{Concert: e.Concert, BarNumbers: -1})
		scoreListRev, scoreListScore, scoreListConc = e.Rev, e.Score, e.Concert
	}
	return scoreList
}

// scoreFocused reports whether the window owns the keyboard.
func scoreFocused() bool {
	return scoreEditorWindow != nil && scoreEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}
