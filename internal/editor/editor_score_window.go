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
//	│ File Edit View Notes Lines Play Window │ Level … Score  name [Saved]│
//	│ Note input │ 64 32 16 8 4 2 1 . rest │ # b n │ Voice 1 2 3 4 │ …    │
//	│ Score Mixer │ Start Play Loop Follow │ Bar 5 · Beat 2 · 0:07.3 │ Tempo│
//	├────────┬─────────────────────────────────────────────────────────┤
//	│ staff  │ time ruler: bars, times, beats, the loop, the play head │
//	│ headers│ the score, engraved on paper, one continuous system     │
//	│ (name, │                                         ┌─keypad─┐      │
//	│ M S ⧉) │                                         └────────┘      │
//	├────────┴─────────────────────────────────────────────────────────┤
//	│ mode · key · meter · duration · voice · selection · clipboard     │
//	└──────────────────────────────────────────────────────────────────┘
//
// The Mixer tab puts the mixer (editor_score_mixer.go) where the score is.
// The view is editor_score_view.go, the headers editor_score_header.go, the
// transport row and the ruler editor_score_ruler.go, the keypad
// editor_score_keypad.go, the keys editor_score_input.go, the F1 overlay and
// the shortcut settings editor_score_help.go, WAV export
// editor_score_export.go. What every command does to the music is
// editor_score_edit.go, to the lines and the mixer editor_score_lines.go, to
// the transport editor_score_transport.go.
//
// The player is told of changes two ways (scoreSyncPlayer): a change to the
// music hands it a fresh clone to prepare; a change to the mixer alone is
// sent live (SetMix, SetGain, SetReturns, MuteIndex, SetInstrument, and for
// the effects SetFX, SetBypass, SetBus or SetEffects — editor_score_fx.go),
// so a fader or an effect's knob can be moved while the music plays.

import (
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"time"

	"foundry/internal/audio"
	"foundry/internal/imguibackend"
	"foundry/internal/score"
	"foundry/internal/score/edit"
	"foundry/internal/score/engrave"
	"foundry/internal/score/io/exportset"
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
	// scorePlayerLane is the dynamics-lane drag state the player was last
	// given (scoreState.laneLive), scorePlayerLaneAt when: a drag reaches
	// the player at most every scoreLaneSendEvery, each a new program.
	scorePlayerLane   uint64
	scorePlayerLaneAt time.Time
	// What the player was last told of the mixer, so a fader drag sends
	// only what moved.
	scorePlayerMixRev uint64
	scoreSentMix      []play.LineMix
	scoreSentMute     []bool
	scoreSentGain     float32
	scoreSentReturns  [2]float32
	// scoreWasPlaying notices the transport stopping by itself (the end).
	scoreWasPlaying bool
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
	if scoreRecording() {
		at := scEd.MIDI.Take.Tick
		if scorePlayer != nil {
			at = scorePlayer.Position().Tick
		}
		scEd.finishTake(at, scoreSettings().MIDI)
	}
	scoreMIDIDisconnect()
	scoreMIDITried = false
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

// scoreEnsurePlayer starts the editor's voice if it is not running, with
// the transport as it stands: the tempo slider, the loop, the play head.
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
	scoreAdopted()
	p.TempoScale(scEd.Transport.Scale, 0)
	p.MoveTo(scEd.Transport.Head)
	scoreVoice = audio.PlayGenerator(scoreKeepAlive{p}, audio.PlayOptions{Bus: audio.BusMusic, Priority: audio.PriorityHigh})
}

func scoreStopPlayer() {
	if scoreVoice != 0 {
		audio.Stop(scoreVoice)
	}
	scorePlayer, scoreVoice, scorePlayerSet, scoreWasPlaying = nil, 0, false, false
	scoreSentMix, scoreSentMute = nil, nil
}

// scoreAdopted records that the player now holds the score as it stands
// (mixer and all) and re-sends the loop, whose ticks an edit may have moved.
func scoreAdopted() {
	e := scEd
	scorePlayerRev, scorePlayerMixRev, scorePlayerSet = e.Rev, e.MixRev, true
	scorePlayerLane, scorePlayerLaneAt = e.laneLive, time.Now()
	s := e.Score
	// The program only re-mutes a line whose own flags changed against the
	// player's last program, which a live mute never touched (an undo of
	// one leaves the flags as that program had them): every strip whose
	// mute differs from what it was last sent is told again.
	for i := range s.Lines {
		if want := play.LineSilenced(s, i); i >= len(scoreSentMute) || scoreSentMute[i] != want {
			scorePlayer.MuteIndex(i, want, 0.02)
		}
	}
	scoreSentMix = scoreSentMix[:0]
	scoreSentMute = scoreSentMute[:0]
	for i, l := range s.Lines {
		scoreSentMix = append(scoreSentMix, scoreLineMix(l))
		scoreSentMute = append(scoreSentMute, play.LineSilenced(s, i))
	}
	scoreSentGain, scoreSentReturns = s.Gain, [2]float32{s.Buses.A.Return, s.Buses.B.Return}
	scoreRecordFX(s)
	if a, b, ok := scoreLoopTicks(scorePlayer.Performance(), e.Transport.Loop); ok && e.Transport.Loop.On {
		scorePlayer.SetLoop(a, b)
	} else {
		scorePlayer.ClearLoop()
	}
}

func scoreLineMix(l *score.Line) play.LineMix {
	return play.LineMix{Volume: l.Volume, Pan: l.Pan, SendA: l.SendA, SendB: l.SendB, PreA: l.PreA, PreB: l.PreB}
}

// scoreLaneSendEvery paces a dynamics-lane drag's programs: often enough
// to hear the level move, not a full interpretation every frame.
var scoreLaneSendEvery = 40 * time.Millisecond

// scoreSyncPlayer hands the player the score as it stands: a new program
// when the music changed, the mixer alone (live, nothing prepared) when only
// that did.
func scoreSyncPlayer(force bool) {
	if scorePlayer == nil {
		return
	}
	e := scEd
	lane := scorePlayerLane != e.laneLive && time.Since(scorePlayerLaneAt) >= scoreLaneSendEvery
	if force || !scorePlayerSet || scorePlayerRev != e.Rev || lane {
		if _, err := scorePlayer.SetScore(e.Score.Clone()); err != nil {
			e.Status = "Playback: " + err.Error()
		}
		scoreAdopted()
		return
	}
	if scorePlayerMixRev == e.MixRev {
		return
	}
	s := e.Score
	scoreSyncFX(scorePlayer, s)
	for i, l := range s.Lines {
		if m := scoreLineMix(l); i >= len(scoreSentMix) || scoreSentMix[i] != m {
			scorePlayer.SetMix(i, m)
		}
		if muted := play.LineSilenced(s, i); i >= len(scoreSentMute) || scoreSentMute[i] != muted {
			scorePlayer.MuteIndex(i, muted, 0.02)
		}
	}
	if s.Gain != scoreSentGain {
		scorePlayer.SetGain(s.Gain)
	}
	if r := [2]float32{s.Buses.A.Return, s.Buses.B.Return}; r != scoreSentReturns {
		scorePlayer.SetReturns(r[0], r[1])
	}
	scoreSentMix, scoreSentMute = scoreSentMix[:0], scoreSentMute[:0]
	for i, l := range s.Lines {
		scoreSentMix = append(scoreSentMix, scoreLineMix(l))
		scoreSentMute = append(scoreSentMute, play.LineSilenced(s, i))
	}
	scoreSentGain, scoreSentReturns = s.Gain, [2]float32{s.Buses.A.Return, s.Buses.B.Return}
	scorePlayerMixRev = e.MixRev
}

// scoreApplyOps hands the player what the transport asked for this frame,
// with the score brought up to date first (the ops' ticks are the current
// score's).
func scoreApplyOps() {
	ops := scEd.takeOps()
	if len(ops) == 0 {
		if scorePlaying() {
			scoreSyncPlayer(false)
		}
		return
	}
	scoreEnsurePlayer()
	if scorePlayer == nil {
		return
	}
	scoreSyncPlayer(false)
	p := scorePlayer
	for _, o := range ops {
		switch o.Kind {
		case scoreOpSeek:
			p.MoveTo(o.Tick)
		case scoreOpPlay:
			p.PlayFrom(o.Tick)
		case scoreOpStop:
			p.StopTransport()
		case scoreOpLoop:
			p.SetLoop(o.A, o.B)
			// A loop set while the music plays elsewhere starts at once.
			if o.A < o.B && scorePlaying() {
				if t := p.Position().Tick; t < o.A || t >= o.B {
					p.PlayFrom(o.A)
				}
			}
		case scoreOpTempo:
			p.TempoScale(o.Scale, 0.15)
		case scoreOpSwap:
			if err := p.SetInstrument(o.Line, o.Name); err != nil {
				scEd.Status = "Playback: " + err.Error()
			}
		case scoreOpRecord:
			p.ClearLoop()
			p.SetClick(o.Level > 0, o.Level)
			p.CountIn(o.Tick, o.Bars, o.Level)
		case scoreOpClick:
			p.SetClick(o.Level > 0, o.Level)
		}
	}
}

// scoreWatchTransport notices the transport stopping by itself at the end:
// the play head stays where it stopped.
func scoreWatchTransport() {
	playing := scorePlaying()
	if scoreWasPlaying && !playing && scorePlayer != nil {
		scEd.Transport.Head = scorePlayer.Position().Tick
	}
	scoreWasPlaying = playing
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

// scorePlayToggle is Space: stop where it is, or play from the play head.
func scorePlayToggle() {
	at := scEd.Transport.Head
	if scorePlaying() {
		at = scorePlayer.Position().Tick
	}
	scEd.togglePlay(scorePlaying(), at)
}

// scorePlayFromSelection is P: play from the selected note or bar.
func scorePlayFromSelection() { scEd.playFromSelection() }

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

func scoreKey() any { return docKey{scEd.Score, scEd.Rev + scEd.MixRev + scEd.LayoutRev} }

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
	scEd.seekTick(0)
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
	scEd.seekTick(0)
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
		if !e.drumLetter(int(c-scLetterC), false) {
			e.letter(int(c - scLetterC))
		}
		return
	case c >= scAddC && c < scAddC+7:
		if !e.drumLetter(int(c-scAddC), true) {
			e.addLetter(int(c - scAddC))
		}
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
	case scSemiUp, scSemiDown:
		dir := 1
		if c == scSemiDown {
			dir = -1
		}
		switch {
		case e.tabFocus():
			e.tabMove(-dir) // up the page: toward string 1
		case e.drumStep(dir):
		default:
			e.semitone(dir)
		}
	case scStepUp, scStepDown:
		dir := 1
		if c == scStepDown {
			dir = -1
		}
		switch {
		case e.tabFocus():
			e.moveToString(-dir)
		case e.drumStep(dir):
		default:
			e.step(dir)
		}
	case scLyrics:
		e.startLyrics(1)
	case scLyrics2:
		e.startLyrics(2)
	case scChordSymbol:
		e.startChord()
	case scOctUp:
		e.octave(1)
	case scOctDown:
		e.octave(-1)
	case scRespell:
		// Return on selected bars breaks the system after them, as in
		// Sibelius; on a note it respells.
		if e.Sel.Kind == scoreSelBars {
			e.toggleBreak(false)
		} else {
			e.respell()
		}
	case scSystemBreak:
		e.toggleBreak(false)
	case scPageBreak:
		e.toggleBreak(true)
	case scPageView:
		scorePageView = !scorePageView
		scoreView = scoreViewScore
		e.Reveal = true
		if scorePageView {
			e.Status = "Page view: the score as it prints (Layout > Page Setup for paper and staff size)"
		} else {
			e.Status = "Continuous view"
		}
	case scPageSetup:
		scoreShowPageSetup = true
	case scExportPDF:
		scoreOpenPDF()
	case scExportImage:
		scoreOpenImage()
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
		// In the timeline: a lane's point, then the roll's notes.
		if scoreView == scoreViewTimeline {
			if e.autoPoint() != nil {
				e.autoDelete()
				break
			}
			if len(e.rollSelNow()) > 0 {
				e.rollDelete(e.rollLine())
				break
			}
		}
		// A lane's override, clicked with nothing else selected.
		if e.Sel.Kind == scoreSelNone && e.laneSelected() != nil {
			e.laneDelete()
			break
		}
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
	case scLoop:
		e.loopSelection()
	case scLoopToggle:
		e.toggleLoop()
	case scToStart:
		e.toStart()
	case scFollow:
		e.Transport.Follow = !e.Transport.Follow
		e.Status = "Follow playback " + scoreOnOff(e.Transport.Follow, "on", "off")
	case scMixer:
		if scoreView == scoreViewMixer {
			scoreView = scoreViewScore
		} else {
			scoreView = scoreViewMixer
		}
	case scTimeline:
		if scoreView == scoreViewTimeline {
			scoreView = scoreViewScore
		} else {
			scoreView = scoreViewTimeline
		}
	case scScoreView:
		scoreView = scoreViewScore
	case scRollDraw:
		if e.TL.Tool == scoreToolDraw {
			e.TL.Tool = scoreToolSelect
			e.Status = "Timeline: Select tool"
		} else {
			e.TL.Tool = scoreToolDraw
			e.Status = "Timeline: Draw tool (drag out notes, draw lanes freehand)"
		}
	case scExportAudio:
		scoreOpenExportOnly(exportset.WAV)
	case scExport:
		scoreOpenExport()
	case scImport:
		scoreOpenImport()
	case scAddLine:
		scoreOpenAddLine(len(e.Score.Lines))
	// Expression (editor_score_marks.go, editor_score_palette.go).
	case scSlur:
		e.slur()
	case scCresc:
		e.hairpin("cresc")
	case scDim:
		e.hairpin("dim")
	case scGliss:
		e.gliss("")
	case scExpression:
		scoreOpenExpression()
	case scTempoText:
		scoreOpenTempo()
	case scStaccato:
		e.toggleMark("stacc", "")
	case scStaccatissimo:
		e.toggleMark("staccmo", "")
	case scAccent:
		e.toggleMark("acc", "")
	case scTenuto:
		e.toggleMark("ten", "")
	case scMarcato:
		e.toggleMark("marc", "")
	case scKeypad7, scKeypad8, scKeypad9, scKeypadPlus:
		e.keypadMark(7 + int(c-scKeypad7))
	case scGraceAcc:
		e.toggleGrace(score.Acciaccatura)
	case scGraceApp:
		e.toggleGrace(score.Appoggiatura)
	case scMarksPanel:
		scoreShowMarks = !scoreShowMarks
	case scConcert:
		e.toggleConcert()
	case scLane:
		if e.Sel.Kind == scoreSelNone && len(e.Score.Lines) > 1 {
			e.Status = "Select a line for its dynamics lane"
			break
		}
		e.toggleLane(e.selLine())
	case scRehearsal:
		scoreOpenRehearsal()
	case scRepeatBars:
		scoreOpenRepeat()
	case scEnding:
		scoreOpenEnding()
	case scClefChange:
		scoreOpenClefChange()
	case scRecord:
		scoreRecordToggle()
	case scMIDIInput:
		scoreOpenMIDI()
	case scTranspose:
		scoreOpenTranspose()
	case scRespellSel:
		e.respellSel()
	case scSpellInKey:
		e.spellInKeySel(false)
	case scFind:
		scoreOpenFind()
	case scFindNext:
		e.findStep(&e.Find, 1)
	case scFindPrev:
		e.findStep(&e.Find, -1)
	case scPalette:
		scoreOpenPalette()
	case scCrossUp:
		e.moveStaff(-1)
	case scCrossDown:
		e.moveStaff(1)
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
	scoreDrawFrame(float32(w.W), float32(w.H))
	scoreMIDIEnsure()
	scorePollMIDI()
	// Ops first: an instrument just picked is the one its audition plays.
	scoreApplyOps()
	scorePlayAuditions()
	scoreWatchTransport()
	scorePollExport()
	scorePollImport()
}

// scoreViewTop is where the main area (the score or the mixer) starts.
const scoreViewTop = editorTopBarH + scoreTransportH

// scoreDrawFrame draws the whole window at a size: the top bar, the
// transport row, the score or the mixer, the status bar, the keypad and
// the dialogs. The tests draw through it too.
func scoreDrawFrame(W, H float32) {
	scoreToolbar(W)
	scoreTransportBar(imgui.Vec2{X: 0, Y: editorTopBarH}, W)
	area := imgui.Vec2{X: W, Y: H - scoreViewTop - scoreStatusH}
	switch scoreView {
	case scoreViewMixer:
		scoreMixerPane(imgui.Vec2{X: 0, Y: scoreViewTop}, area)
	case scoreViewTimeline:
		scoreTimelinePane(imgui.Vec2{X: 0, Y: scoreViewTop}, area)
	default:
		if scorePageView {
			scorePagePane(imgui.Vec2{X: 0, Y: scoreViewTop}, area)
		} else {
			scoreViewPane(imgui.Vec2{X: 0, Y: scoreViewTop}, area)
		}
	}
	scoreStatusBar(imgui.Vec2{X: 0, Y: H - scoreStatusH}, W)
	if scoreShowKeypad && scoreView == scoreViewScore {
		scoreKeypadPanel(W, H)
	}
	if scoreShowMarks && scoreView == scoreViewScore {
		scorePalettePanel(W, H)
	}
	scoreDialogs()
	scoreFindWindow(W)
	scorePaletteWindow(W)
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
	if editorActiveToggleButtonColor("Keypad##sckp", scoreShowKeypad, scoreColActive) {
		scoreRunCmd(scKeypadPanel)
	}
	editorMenuTooltip("The on-screen keypad" + scoreKeysSuffix(scKeypadPanel))
	imgui.SameLine()
	if editorActiveToggleButtonColor("Marks##scmk", scoreShowMarks, scoreColActive) {
		scoreRunCmd(scMarksPanel)
	}
	editorMenuTooltip("The marks palette: dynamics, lines, articulations, ornaments, form" + scoreKeysSuffix(scMarksPanel))
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
		imgui.Separator()
		if imgui.MenuItemV("Import...", km.label(scImport), false, !scoreImportBusy()) {
			scoreRunCmd(scImport)
		}
		editorMenuTooltip("A MusicXML file (what Sibelius exports), a MIDI file or a Tracker song, as a new score.")
		if imgui.MenuItemV("Export...", km.label(scExport), false, !scoreExportBusy()) {
			scoreRunCmd(scExport)
		}
		editorMenuTooltip("PDF, MusicXML (for Sibelius), MIDI, OGG, WAV, MP3, FLAC and a Tracker song, in one pass.")
		if imgui.MenuItemV("Export Audio...", km.label(scExportAudio), false, !scoreExportBusy()) {
			scoreRunCmd(scExportAudio)
		}
		editorMenuTooltip("Render the whole score, the loop region or the selection; one mix or a stem per line.")
		if imgui.MenuItemV("Export PDF...", km.label(scExportPDF), false, !scoreExportBusy()) {
			scoreRunCmd(scExportPDF)
		}
		editorMenuTooltip("The full score and its parts as PDFs, laid out as the page view shows them.")
		item("Export Selection as Image...", scExportImage, e.Sel.Kind != scoreSelNone)
		editorMenuTooltip("The selected bars as a PNG or an SVG.")
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
		item("Find...", scFind, true)
		editorMenuTooltip("A pitch (F#4, Bb, C4..G5), a mark (staccato, fermata, mf, tie) or a text\n(a lyric, an expression, a chord symbol, a rehearsal mark).")
		item("Find Next", scFindNext, e.Find.Query != "")
		item("Find Previous", scFindPrev, e.Find.Query != "")
		imgui.Separator()
		if imgui.MenuItem("Keyboard Shortcuts...") {
			scoreShowKeys = true
		}
		editorMenuTooltip("Choose the Sibelius or MuseScore keymap and rebind any command.")
		item("MIDI Input...", scMIDIInput, true)
		editorMenuTooltip("The MIDI keyboard: step entry in note input, recording, MIDI thru.")
		imgui.EndMenu()
	}
	if imgui.BeginMenu("View") {
		item("Zoom In", scZoomIn, true)
		item("Zoom Out", scZoomOut, true)
		item("Actual Size", scZoomReset, true)
		imgui.Separator()
		if imgui.MenuItemV("Concert Pitch", km.label(scConcert), e.Score.Concert, true) {
			scoreRunCmd(scConcert)
		}
		editorMenuTooltip("Show transposing instruments at sounding pitch (entry follows).")
		editorMenuToggle("Voice Colours", "", &scoreVoiceColours)
		if imgui.MenuItemV("Keypad", km.label(scKeypadPanel), scoreShowKeypad, true) {
			scoreRunCmd(scKeypadPanel)
		}
		if imgui.MenuItemV("Mixer", km.label(scMixer), scoreView == scoreViewMixer, true) {
			scoreRunCmd(scMixer)
		}
		if imgui.MenuItemV("Page View", km.label(scPageView), scorePageView, true) {
			scoreRunCmd(scPageView)
		}
		editorMenuTooltip("The score on pages, as it prints; off, one continuous system.")
		if imgui.MenuItemV("Keymap Overlay", "F1", uiwin.KeybindHelpVisible, true) {
			uiwin.KeybindHelpToggle()
		}
		item("Command Palette...", scPalette, true)
		editorMenuTooltip("Every command by name, with its keys: type to narrow, Return runs.")
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
		item("Cross to Staff Above", scCrossUp, e.chord() != nil || e.Sel.Kind == scoreSelBars)
		editorMenuTooltip("A grand staff's notes written on the staff above their own (their voice\nand beams stay theirs; a beam across the staves is kneed). Again: back.")
		item("Cross to Staff Below", scCrossDown, e.chord() != nil || e.Sel.Kind == scoreSelBars)
		imgui.Separator()
		item("Transpose...", scTranspose, len(e.Score.Lines) > 0)
		editorMenuTooltip("The selection or the whole score: by an interval (chromatic or diatonic)\nor to another key, with the key signatures and chord symbols.")
		item("Respell Selection", scRespellSel, e.Sel.Kind != scoreSelNone)
		editorMenuTooltip("Every selected note to its other spelling (F# to Gb).")
		item("Spell in Key", scSpellInKey, true)
		editorMenuTooltip("Every selected note (the whole score with nothing selected) as the key\nspells it: the key's own note, else a sharp rising and a flat falling.")
		if imgui.MenuItemV("Second Keypad Layout", km.label(scLayout), e.Layout2, true) {
			scoreRunCmd(scLayout)
		}
		imgui.EndMenu()
	}
	scoreMarksMenu()
	if imgui.BeginMenu("Lines") {
		item("Add Line...", scAddLine, true)
		editorMenuTooltip("A line for any instrument of the library, written on the staff it asks for\n(a grand staff for a piano, a percussion staff for a kit).")
		if imgui.MenuItem("Add Grand Staff (Piano)") {
			e.insertLine(len(e.Score.Lines), scoreGrandStaffLine())
		}
		if imgui.MenuItem("Add Percussion Staff (Drum Kit)") {
			e.insertLine(len(e.Score.Lines), scorePercussionLine())
		}
		editorMenuTooltip("Each drum has its place and notehead (a hi-hat's x above the staff,\nthe snare in the third space): a click writes the drum on that line or space.")
		imgui.Separator()
		li := e.selLine()
		if e.Sel.Kind == scoreSelNone {
			editorTextMuted("Select a line for its commands")
		} else if imgui.BeginMenu(e.lineName(li) + "##sclinesub") {
			scoreLineMenuItems(li)
			imgui.EndMenu()
		}
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Layout") {
		if imgui.MenuItemV("Page View", km.label(scPageView), scorePageView, true) {
			scoreRunCmd(scPageView)
		}
		if imgui.BeginMenuV("Show", scorePageView) {
			if imgui.MenuItemV("Full Score##scshowfull", "", e.pagePart() < 0, true) {
				scorePagePart = -1
			}
			for i := range e.Score.Lines {
				if imgui.MenuItemV(fmt.Sprintf("Part: %s##scshow%d", e.lineName(i), i), "", e.pagePart() == i, true) {
					scorePagePart = i
				}
			}
			imgui.EndMenu()
		}
		item("Page Setup...", scPageSetup, true)
		imgui.Separator()
		_, z, has := e.selBars()
		bk, hasBreak := score.BreakIn(e.breaksOf(e.pagePart()), z+1)
		if imgui.MenuItemV("System Break", km.label(scSystemBreak), hasBreak && !bk.Page, has) {
			scoreRunCmd(scSystemBreak)
		}
		editorMenuTooltip("A new system after the selected bar (again: none)")
		if imgui.MenuItemV("Page Break", km.label(scPageBreak), hasBreak && bk.Page, has) {
			scoreRunCmd(scPageBreak)
		}
		editorMenuTooltip("A new page after the selected bar (again: none)")
		imgui.Separator()
		if imgui.MenuItemV("Export PDF...##sclayoutpdf", km.label(scExportPDF), false, !scoreExportBusy()) {
			scoreRunCmd(scExportPDF)
		}
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Play") {
		if imgui.MenuItemV("Play / Stop", km.label(scPlay), scorePlaying(), true) {
			scoreRunCmd(scPlay)
		}
		item("Play from Selection", scPlayFrom, true)
		item("To Start", scToStart, true)
		if imgui.MenuItemV("Record", km.label(scRecord), scoreRecording(), true) {
			scoreRunCmd(scRecord)
		}
		editorMenuTooltip("Play the MIDI keyboard along with the score into the armed (or selected)\nline, from the selected bar, after a count-in; the take replaces those bars.")
		imgui.Separator()
		item("Loop Selection", scLoop, e.Sel.Kind == scoreSelBars || e.Transport.Loop.Set)
		if imgui.MenuItemV("Loop", km.label(scLoopToggle), e.Transport.Loop.On, true) {
			scoreRunCmd(scLoopToggle)
		}
		editorMenuTooltip(scoreLoopText(e.Score, e.Transport.Loop))
		if imgui.MenuItemV("Follow Playback", km.label(scFollow), e.Transport.Follow, true) {
			scoreRunCmd(scFollow)
		}
		imgui.Separator()
		if imgui.MenuItemV("Tempo 100%", "", e.Transport.Scale == 1, true) {
			e.setTempoScale(1)
		}
		if imgui.MenuItemV(fmt.Sprintf("Write Tempo Slider (%.0f%%) as Tempo Marks", e.Transport.Scale*100), "", false, e.Transport.Scale != 1) {
			e.writeTempoScaled()
		}
		imgui.Separator()
		if imgui.MenuItem("Swing and Humanise...") {
			scoreOpenFeel()
		}
		editorMenuTooltip("The score's swing and humanise, or a line's own (with one line's bars selected)")
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
	// scoreDlgRebar re-bars the music under a new time signature (on by
	// default, as Sibelius's "rewrite bars" is).
	scoreDlgRebar = true
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
		imgui.Checkbox("Re-bar the music##sctimerebar", &scoreDlgRebar)
		editorMenuTooltip("On: the notes keep their time and are written again across the new\nbarlines, tied over them, up to the next time signature (the bars after\nmove with them). Off: each bar keeps its notes; what is past the new\nbarline is cut.")
		if scoreDlgRebar {
			editorTextMuted("Any meter: 7/8, 3+2+2/8, 12/16, C, C|. The music up to the next time\nsignature is re-barred in the new meter.")
		} else {
			editorTextMuted("Any meter: 7/8, 3+2+2/8, 12/16, C, C|. The bars up to the next time\nsignature take the new length: music past a new barline is cut.")
		}
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
				if scoreDlgRebar {
					e.rebarMeter(int(scoreDlgBar), m)
				} else {
					e.setMeter(int(scoreDlgBar), m)
				}
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

	scoreMarkDialogs()
	scoreKeysDialog()
	scoreExportDialog()
	scoreImportDialog()
	scorePageSetupDialog()
	scoreImageDialog()
	scoreLinePopups()
	scoreFXPopups()
	scoreFeelPopup()
	scoreTimelinePopups()
	scoreLibraryDialog()
	scoreMIDIDialog()
	scoreTransposeDialog()
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
	scoreList       *engrave.List
	scoreListRev    uint64
	scoreListScore  *score.Score
	scoreListLanes  uint64
	scoreListLayout uint64
)

// scoreLayout is the score engraved as one continuous system. A layout
// change re-engraves it too (the family brackets are drawn in both views).
func scoreLayout() *engrave.List {
	e := scEd
	if scoreList == nil || scoreListRev != e.Rev || scoreListScore != e.Score || scoreListLanes != e.laneRev || scoreListLayout != e.LayoutRev {
		scoreList = engrave.Engrave(e.Score, engrave.Options{BarNumbers: -1, Below: e.laneBelow()})
		scoreListRev, scoreListScore, scoreListLanes, scoreListLayout = e.Rev, e.Score, e.laneRev, e.LayoutRev
	}
	return scoreList
}

// scoreFocused reports whether the window owns the keyboard.
func scoreFocused() bool {
	return scoreEditorWindow != nil && scoreEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}
