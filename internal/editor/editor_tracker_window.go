package editor

// editor_tracker_window.go — the tracker editor, in its own OS window.
//
// It owns a document (*tracker.Song) the way the particle and AI editors own
// theirs: the song outlives the window, so popping the editor in and out
// returns to the same song, and the main window carries on with the level.
//
// What this editor has that the others do not is a voice on the mixer. While
// the window is up it keeps one long-lived tracker.Player playing on the music
// bus (trkEnsurePlayer), silent until asked: the transport starts and stops
// the song inside it, typed notes audition through it, and every edit is
// handed to it as a fresh clone (trkSyncPlayer) so what plays is always what
// is on screen. The player never holds the song being edited — it would be
// read on the mixer goroutine while this one writes it.
//
// Layout (each splitter can be dragged):
//
//	┌──────────────────────────────────────────────────────────────────┐
//	│ File Edit View Pattern Window │ Level … Tracker     name [Saved] │
//	│ ▶ song ▶ pattern ▶ cursor ■   BPM LPB Oct Step  Follow Edit  ~~~ │
//	├────────┬────────────────────────────────────────┬────────────────┤
//	│ order  │ overview                               │ instruments    │
//	│ list & │ pattern grid  (piano roll below when   │                │
//	│ section│               a channel is flipped)    │ instrument     │
//	├────────┴────────────────────────────────────────┤ editor         │
//	│ Sample │ Song │ Params & markers                │                │
//	└─────────────────────────────────────────────────┴────────────────┘

import (
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/audio"
	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"
	"foundry/internal/tracker"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	trackerWindowW = int32(1500)
	trackerWindowH = int32(920)

	// trkToolbarH is the shared two-row top bar (editor_menubar.go).
	trkToolbarH = editorTopBarH
)

// Pane sizes. View state: dragged by the splitters, never saved.
var (
	trkLeftW   = float32(210)
	trkRightW  = float32(370)
	trkBottomH = float32(270)
)

// trackerEditorWindow is the pop-out; nil when the editor is closed.
var trackerEditorWindow *uiwin.UIWindow

// trkEd is the song being edited and everything about how it is being
// edited. It outlives the window.
var trkEd *trackerState

// The editor's voice on the mixer. trkPlayerRev is the trkEd.Rev last handed
// to it; trkPlayPattern says the transport is looping the cursor's pattern
// rather than walking the order list, which changes what song it is handed.
var (
	trkPlayer      *tracker.Player
	trkVoice       audio.VoiceID
	trkPlayerRev   uint64
	trkPlayerSet   bool
	trkPlayPattern bool
	trkPlayOrder   int // the order entry a pattern loop is playing
)

// View toggles.
var (
	trkFollow    = true
	trkHexFX     bool
	trkShowMap   = true
	trkGridFocus = true
	// trkRollCh is the channel shown as a piano roll under the grid, or -1.
	trkRollCh = -1
)

// trkSaved tracks the song against its last save, for the chip and Save All.
var trkSaved savedState

// Dialog state.
var (
	trkShowOpen     bool
	trkShowSaveAs   bool
	trkShowNew      bool
	trkShowExport   bool
	trkSaveAsName   string
	trkExportPath   string
	trkSongList     []string
	trkExportResult chan string
)

// ---------- lifecycle ----------

// TrackerEditorOpen reports whether the window is up.
func TrackerEditorOpen() bool { return trackerEditorWindow != nil }

// OpenTrackerEditorWindow opens the editor, or raises it if it is already up.
// With nothing loaded it starts on the Empty template, the way the model
// editor starts on a cube rather than on a file dialog.
func OpenTrackerEditorWindow() {
	if trackerEditorWindow != nil {
		trackerEditorWindow.Win.Raise()
		return
	}
	trkEnsureDoc()
	w, err := uiwin.NewUIWindow("Tracker", trackerWindowW, trackerWindowH, drawTrackerEditorWindow, onTrackerEditorClose)
	if err != nil {
		log.Printf("open tracker window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.09, 0.10, 0.12, 1}
	w.HandleEvent = trackerEditorHandleEvent
	// Assigned last, on the particle editor's rule: until this is set,
	// TrackerEditorOpen has to keep saying no.
	trackerEditorWindow = w
	trkEnsurePlayer()
}

// OpenTrackerEditorOn opens the editor on a named song. Deferred through
// RequestWindowOpen because the caller is inside another window's Draw.
func OpenTrackerEditorOn(name string) {
	if name == "" {
		return
	}
	uiwin.RequestWindowOpen(func() {
		if err := trkLoad(name); err != nil {
			editorSetStatus("Could not open %s: %v", name, err)
			return
		}
		OpenTrackerEditorWindow()
	})
}

// CloseTrackerEditorWindow asks for the window to go away at the end of the
// frame. Safe to call from inside its own Draw.
func CloseTrackerEditorWindow() {
	if trackerEditorWindow != nil {
		trackerEditorWindow.CloseRequested = true
	}
}

// onTrackerEditorClose stops the editor's voice. The song stays in memory.
func onTrackerEditorClose() {
	trkStopPlayer()
	trackerEditorWindow = nil
}

// trackerEditorFocused reports whether this window owns keyboard input.
func trackerEditorFocused() bool {
	return trackerEditorWindow != nil &&
		trackerEditorWindow.Win.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
}

// trkEnsureDoc guarantees there is something to edit.
func trkEnsureDoc() {
	if trkEd == nil {
		s := trkTemplateEmpty()
		tracker.ResolveSamples(s)
		trkEd = newTrackerState(s)
		trkMarkSaved()
	}
}

// ---------- the player ----------

// trkKeepAlive keeps the editor's voice alive when a song ends. A Player
// reports "no more" once a non-looping song has rung out, which ends its
// voice; the editor's voice has to outlive that, because the next Play and
// every audition go through the same player.
type trkKeepAlive struct{ p *tracker.Player }

func (k trkKeepAlive) Render(dst []float32, frames, rate int) bool {
	k.p.Render(dst, frames, rate)
	return true
}

// trkEnsurePlayer starts the editor's voice if it is not running.
func trkEnsurePlayer() {
	if trkPlayer != nil || trkEd == nil {
		return
	}
	trkPlayer = tracker.NewPlayer(trkPlayerSong())
	trkPlayerRev, trkPlayerSet = trkEd.Rev, true
	trkVoice = audio.PlayGenerator(trkKeepAlive{trkPlayer}, audio.PlayOptions{Bus: audio.BusMusic, Priority: audio.PriorityHigh})
}

func trkStopPlayer() {
	if trkVoice != 0 {
		audio.Stop(trkVoice)
	}
	trkPlayer, trkVoice, trkPlayerSet = nil, 0, false
}

// trkPreviewSample, when set, is added to the player's song as one extra
// instrument past the song's own — how the sample editor and the Generate
// dialog audition a sample that is not in the song (or not saved) yet.
var trkPreviewSample *tracker.Sample

// trkPreviewInstr is the 1-based instrument number the preview sample plays
// as in the player's song.
func trkPreviewInstr() uint8 { return uint8(len(trkEd.Song.Instruments) + 1) }

// trkPlayerSong is what the player is handed: a clone of the song with its
// samples resolved, cut down to the cursor's pattern, looping, when the
// transport is in pattern mode, plus the preview instrument.
func trkPlayerSong() *tracker.Song {
	tracker.ResolveSamples(trkEd.Song)
	c := trkEd.Song.Clone()
	if trkPlayPattern && trkPlayOrder >= 0 && trkPlayOrder < len(c.Order) {
		c.Order = []int{c.Order[trkPlayOrder]}
		c.Sections = nil
		c.Loop = true
	}
	if trkPreviewSample != nil && len(c.Instruments) < tracker.MaxInstruments {
		in := tracker.NewInstrument("preview", trkPreviewSample.Name)
		in.SetLoaded(0, trkPreviewSample)
		c.Instruments = append(c.Instruments, in)
	}
	return c
}

// trkSyncPlayer hands the player the song as it now stands, once per frame at
// most, and only when something changed.
func trkSyncPlayer(force bool) {
	if trkPlayer == nil {
		return
	}
	if !force && trkPlayerSet && trkPlayerRev == trkEd.Rev {
		return
	}
	trkPlayer.SetSong(trkPlayerSong())
	trkPlayerRev, trkPlayerSet = trkEd.Rev, true
}

// Transport.
func trkPlaySong() {
	trkEnsurePlayer()
	trkPlayPattern = false
	trkSyncPlayer(true)
	order := 0
	trkPlayer.PlayFrom(order, 0)
}

func trkPlayPatternLoop() {
	trkEnsurePlayer()
	trkPlayPattern, trkPlayOrder = true, trkEd.Cur.Order
	trkSyncPlayer(true)
	trkPlayer.PlayFrom(0, 0)
}

func trkPlayFromCursor() {
	trkEnsurePlayer()
	trkPlayPattern = false
	trkSyncPlayer(true)
	trkPlayer.PlayFrom(trkEd.Cur.Order, trkEd.Cur.Row)
}

func trkStop() {
	if trkPlayer != nil {
		trkPlayer.StopTransport()
	}
}

// trkPlayPosition is where the transport is, in the edited song's order list:
// a pattern loop reports order 0 of its one-entry song, which is mapped back
// to the order entry it was started from.
func trkPlayPosition() (order, row int, playing bool) {
	if trkPlayer == nil {
		return 0, 0, false
	}
	pos := trkPlayer.Position()
	order = pos.Order
	if trkPlayPattern {
		order = trkPlayOrder
	}
	return order, pos.Row, pos.Playing
}

// trkAudition plays a note on the current instrument, keyed so the key-up can
// release it.
func trkAudition(key int, instr int, note uint8) {
	if trkPlayer == nil || instr <= 0 || note < tracker.NoteMin || note > tracker.NoteMax {
		return
	}
	trkSyncPlayer(false)
	trkPlayer.Note(key, uint8(instr), note, 1)
}

func trkAuditionOff(key int) {
	if trkPlayer != nil {
		trkPlayer.NoteOff(key)
	}
}

// ---------- the document ----------

// trkFingerprint is a hash of the song as its file would store it.
func trkFingerprint(s *tracker.Song) uint64 {
	b, err := tracker.Marshal(s)
	if err != nil {
		return 0
	}
	h := fnv.New64a()
	h.Write(b)
	return h.Sum64()
}

func trkKey() any { return docKey{trkEd.Song, trkEd.Rev} }

func trkMarkSaved() {
	if trkEd != nil {
		trkSaved.baseline(trkKey(), trkFingerprint(trkEd.Song))
	}
}

func trackerEditorDirty() bool {
	if trkEd == nil {
		return false
	}
	trkSaved.refresh(trkKey(), 200, func() uint64 { return trkFingerprint(trkEd.Song) })
	return trkSaved.dirty()
}

// trkDocName is the song's library name, or "(unsaved)".
func trkDocName() string {
	if trkEd == nil || trkEd.Name == "" {
		return "(unsaved)"
	}
	return trkEd.Name
}

// trkNew replaces the song with a template.
func trkNew(tpl trkTemplate) {
	s := tpl.Build()
	tracker.ResolveSamples(s)
	trkEd.setSong(s)
	trkEd.Name = ""
	trkPlayPattern = false
	trkStop()
	trkSyncPlayer(true)
	trkMarkSaved()
	trkEd.Status = "New song from " + tpl.Name
}

// trkLoad opens a song by library name.
func trkLoad(name string) error {
	path := tracker.SongPath(name)
	if path == "" {
		return fmt.Errorf("no song %q", name)
	}
	s, err := tracker.LoadFile(path)
	if err != nil {
		return err
	}
	s.Validate()
	tracker.ResolveSamples(s)
	trkEnsureDoc()
	trkEd.setSong(s)
	trkEd.Name = name
	trkPlayPattern = false
	trkStop()
	trkSyncPlayer(true)
	trkMarkSaved()
	trkEd.Status = "Opened " + name
	return nil
}

// trkSaveAs writes the song as sounds/<name>.ftrk. The song cache is dropped
// so a level playing it picks the edit up on its next play_music.
func trkSaveAs(name string) error {
	name = strings.TrimSuffix(strings.TrimSpace(name), tracker.FileExt)
	if name == "" {
		return fmt.Errorf("no name")
	}
	path := filepath.Join(audio.SoundsDir(), filepath.FromSlash(name)+tracker.FileExt)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if trkEd.Song.Name == "" || trkEd.Song.Name == "untitled" {
		trkEd.Song.Name = filepath.Base(name)
	}
	if err := tracker.SaveFile(path, trkEd.Song); err != nil {
		return err
	}
	trkEd.Name = name
	trkSongList = nil
	tracker.ReloadSongs()
	trkMarkSaved()
	trkEd.Status = "Saved " + name + tracker.FileExt
	return nil
}

// trkSaveCurrent saves over the open song, or asks for a name.
func trkSaveCurrent() {
	if trkEd == nil {
		return
	}
	if trkEd.Name == "" {
		trkSaveAsName = trkEd.Song.Name
		trkShowSaveAs = true
		return
	}
	if err := trkSaveAs(trkEd.Name); err != nil {
		trkEd.Status = "Save failed: " + err.Error()
	}
}

// trkStartExport renders the song to a WAV off the frame: a few seconds of
// rendering is too long to hold the window still for.
func trkStartExport(path string) {
	if trkExportResult != nil {
		return
	}
	song := trkEd.Song.Clone()
	tracker.ResolveSamples(song)
	ch := make(chan string, 1)
	trkExportResult = ch
	trkEd.Status = "Exporting..."
	go func() {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			ch <- "Export failed: " + err.Error()
			return
		}
		if err := tracker.RenderToWAV(song, path, tracker.RenderOptions{Tail: 2}); err != nil {
			ch <- "Export failed: " + err.Error()
			return
		}
		ch <- "Exported " + path
	}()
}

func trkPollExport() {
	if trkExportResult == nil {
		return
	}
	select {
	case msg := <-trkExportResult:
		trkEd.Status = msg
		trkExportResult = nil
	default:
	}
}

// trkDefaultExportPath is exports/<name>.wav beside resources/ — outside
// sounds/, so the export is never mistaken for the song by play_music.
func trkDefaultExportPath() string {
	name := trkEd.Name
	if name == "" {
		name = trkEd.Song.Name
	}
	if name == "" {
		name = "song"
	}
	return filepath.Join(filepath.Dir(filepath.Clean(fileutil.ResourcesPath)), "exports", filepath.FromSlash(name)+".wav")
}

// trkUndo and trkRedo are the menu's and the keys' undo.
func trkUndo() {
	if trkEd.undoStep() {
		trkAfterStructural()
	}
}

func trkRedo() {
	if trkEd.redoStep() {
		trkAfterStructural()
	}
}

// trkAfterStructural keeps the view's indices valid after the song changed
// shape (undo, channel count, instruments deleted).
func trkAfterStructural() {
	trkEd.clampCursor()
	if trkRollCh >= len(trkEd.Song.Channels) {
		trkRollCh = -1
	}
	if trkPlayPattern && trkPlayOrder >= len(trkEd.Song.Order) {
		trkPlayOrder = len(trkEd.Song.Order) - 1
	}
	trkSelSection = clampI(trkSelSection, -1, len(trkEd.Song.Sections)-1)
}

// ---------- the frame ----------

func drawTrackerEditorWindow() {
	w := trackerEditorWindow
	if w == nil {
		return
	}
	trkEnsureDoc()
	trkEnsurePlayer()
	trkPollExport()
	trkFollowPlayback()

	trackerToolbar()
	trkClampLayout(float32(w.W), float32(w.H))
	trkOrderPane()
	trkGridPane()
	trkInstrumentPane()
	trkBottomPane()
	trkDialogs()

	// A continuous edit whose widget was not drawn this frame (a tab or a
	// popup closed under it) still has to end its undo run.
	if trkWidgetEditing && !imgui.IsAnyItemActive() {
		trkWidgetEditing = false
		trkEd.breakUndoRun()
	}
	trkSyncPlayer(false)
}

// trkClampLayout keeps the panes on screen when the window shrinks.
func trkClampLayout(w, h float32) {
	trkLeftW = clampF32(trkLeftW, 140, w*0.3)
	trkRightW = clampF32(trkRightW, 260, w*0.45)
	trkBottomH = clampF32(trkBottomH, 120, (h-trkToolbarH)*0.7)
}

func clampF32(v, lo, hi float32) float32 {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// trkSmallButton is a button with no vertical frame padding — imgui's
// SmallButton, which this binding does not export.
func trkSmallButton(label string) bool {
	imgui.PushStyleVarVec2(imgui.StyleVarFramePadding, imgui.Vec2{X: 4, Y: 0})
	ok := imgui.Button(label)
	imgui.PopStyleVar()
	return ok
}

// trkPaneFlags is every fixed pane's window flags.
const trkPaneFlags = imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize | imgui.WindowFlagsNoMove |
	imgui.WindowFlagsNoSavedSettings | imgui.WindowFlagsNoBringToFrontOnFocus

// trkPaneClickTakesFocus hands keyboard focus away from the grid when a pane
// other than the grid is clicked. Called inside each such pane's window.
func trkPaneClickTakesFocus() {
	if imgui.IsWindowHoveredV(imgui.HoveredFlagsChildWindows) && (imgui.IsMouseClicked(0) || imgui.IsMouseClicked(1)) {
		trkGridFocus = false
	}
}

// trkSplitter is a draggable edge. vertical splitters drag along X. sign says
// which way a positive drag moves *v. Drawn as a thin line that lights when
// hovered.
func trkSplitter(id string, min, max imgui.Vec2, vertical bool, v *float32, sign float32) {
	imgui.SetCursorScreenPos(min)
	size := imgui.Vec2{X: max.X - min.X, Y: max.Y - min.Y}
	imgui.InvisibleButton(id, size)
	hot := imgui.IsItemHovered() || imgui.IsItemActive()
	if hot {
		if vertical {
			imgui.SetMouseCursor(imgui.MouseCursorResizeEW)
		} else {
			imgui.SetMouseCursor(imgui.MouseCursorResizeNS)
		}
	}
	if imgui.IsItemActive() {
		d := imgui.CurrentIO().MouseDelta()
		if vertical {
			*v += d.X * sign
		} else {
			*v += d.Y * sign
		}
	}
	col := trkColSplitter
	if hot {
		col = trkColSplitterHot
	}
	imgui.ForegroundDrawList().AddRectFilled(min, max, col)
}

// trackerToolbar is the shared two-row top bar: menus, then the transport.
func trackerToolbar() {
	w := trackerEditorWindow
	editorTopBarBegin("##trktoolbar", float32(w.W))
	if imgui.BeginMenuBar() {
		trackerMenus()
		editorMenuBarShared(editorKindTracker)
		editorMenuBarDoc(trkDocName(), trackerEditorDirty())
		imgui.EndMenuBar()
	}
	trkTransportRow()
	imgui.End()
}

// trkTransportRow is row two: the transport and the numbers a tracker keeps
// in view while typing.
func trkTransportRow() {
	_, _, playing := trkPlayPosition()
	if editorActiveToggleButtonColor("Play song##trkplay", playing && !trkPlayPattern, trkColActiveButton) {
		trkPlaySong()
	}
	editorMenuTooltip("Play the song from the top (F5).")
	imgui.SameLine()
	if editorActiveToggleButtonColor("Play pattern##trkplaypat", playing && trkPlayPattern, trkColActiveButton) {
		trkPlayPatternLoop()
	}
	editorMenuTooltip("Loop the pattern under the cursor (F6).")
	imgui.SameLine()
	if imgui.Button("From cursor##trkplaycur") {
		trkPlayFromCursor()
	}
	editorMenuTooltip("Play the song from the cursor's row (F7).")
	imgui.SameLine()
	if imgui.Button("Stop##trkstop") {
		trkStop()
	}
	editorMenuTooltip("Stop (F8).")
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()

	s := trkEd.Song
	imgui.SetNextItemWidth(70)
	bpm := s.BPM
	if imgui.DragFloatV("BPM##trkbpm", &bpm, 0.5, 16, 999, "%.1f", 0) {
		trkEditBegin("bpm")
		s.BPM = bpm
		trkEd.touch()
	}
	trkEditEnd()
	imgui.SameLine()
	imgui.SetNextItemWidth(60)
	lpb := int32(s.LPB)
	if imgui.InputIntV("LPB##trklpb", &lpb, 1, 1, 0) {
		trkEditBegin("")
		s.LPB = clampI(int(lpb), 1, 32)
		trkEd.touch()
	}
	editorMenuTooltip("Lines (rows) per beat.")
	imgui.SameLine()
	imgui.SetNextItemWidth(60)
	oct := int32(trkEd.Octave)
	if imgui.InputIntV("Oct##trkoct", &oct, 1, 1, 0) {
		trkEd.Octave = clampI(int(oct), 0, 8)
	}
	editorMenuTooltip("The keyboard's lower octave ([ and ]).")
	imgui.SameLine()
	imgui.SetNextItemWidth(60)
	step := int32(trkEd.Step)
	if imgui.InputIntV("Step##trkstep", &step, 1, 1, 0) {
		trkEd.Step = clampI(int(step), 0, 16)
	}
	editorMenuTooltip("Rows the cursor moves after each entry (Ctrl+0..9).")
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	imgui.Checkbox("Follow##trkfollow", &trkFollow)
	editorMenuTooltip("Keep the playing row in view.")
	imgui.SameLine()
	if editorActiveToggleButtonColor("Edit##trkedit", trkEd.EditMode, trkColRecordButton) {
		trkEd.EditMode = !trkEd.EditMode
	}
	editorMenuTooltip("Edit mode (Space): typed notes are written into the pattern.\nOff, the keyboard only plays them.")
	imgui.SameLine()
	editorToolbarGroupSep()
	imgui.SameLine()
	voices := 0
	if trkPlayer != nil {
		voices = trkPlayer.LiveVoices()
	}
	editorTextMuted(fmt.Sprintf("%3d voices", voices))
	imgui.SameLine()
	trkDrawScope(imgui.Vec2{X: 140, Y: 22})
	editorStatusText(trkEd.Status)
}

// trkScopeBuf is the oscilloscope's sample buffer, reused every frame.
var trkScopeBuf [256]float32

// trkDrawScope is the master oscilloscope: the player's last few hundred
// output samples as a line.
func trkDrawScope(size imgui.Vec2) {
	p := imgui.CursorScreenPos()
	imgui.Dummy(size)
	dl := imgui.WindowDrawList()
	max := imgui.Vec2{X: p.X + size.X, Y: p.Y + size.Y}
	dl.AddRectFilledV(p, max, trkColScopeBg, 3, imgui.DrawFlagsRoundCornersAll)
	if trkPlayer == nil {
		return
	}
	trkPlayer.Scope(trkScopeBuf[:])
	mid := p.Y + size.Y/2
	prev := imgui.Vec2{X: p.X, Y: mid}
	for i, v := range trkScopeBuf {
		v = clampF32(v, -1, 1)
		pt := imgui.Vec2{X: p.X + float32(i)/float32(len(trkScopeBuf)-1)*size.X, Y: mid - v*size.Y*0.48}
		if i > 0 {
			dl.AddLine(prev, pt, trkColScope)
		}
		prev = pt
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Master output")
	}
}

// trkEditBegin and trkEditEnd bracket a widget that edits continuously (a drag
// or slider): one undo step for the whole drag. key coalesces a run.
var trkWidgetEditing bool

func trkEditBegin(key string) {
	if !trkWidgetEditing {
		trkEd.pushUndo(key)
		trkWidgetEditing = true
	}
}

// trkEditEnd ends a continuous edit when its widget lets go. Called right
// after the widget, every frame.
func trkEditEnd() {
	if trkWidgetEditing && imgui.IsItemDeactivated() {
		trkWidgetEditing = false
		trkEd.breakUndoRun()
	}
	if trkWidgetEditing && !imgui.IsAnyItemActive() {
		trkWidgetEditing = false
		trkEd.breakUndoRun()
	}
}

// trkEdited is the one-shot form: a widget that changed the song this frame.
// Drags coalesce through trkEditBegin's flag; a text field or checkbox is one
// step per change.
func trkEdited(changed bool) bool {
	if changed {
		trkEditBegin("")
		trkEd.touch()
	}
	trkEditEnd()
	return changed
}

// trackerMenus draws the tracker's own menus.
func trackerMenus() {
	if imgui.BeginMenu("File") {
		if imgui.BeginMenu("New") {
			for _, tpl := range trkTemplates {
				if imgui.MenuItem(tpl.Name + "##trknew") {
					trkNew(tpl)
				}
				editorMenuTooltip(tpl.Desc)
			}
			imgui.EndMenu()
		}
		if imgui.MenuItemV("Open...", "Ctrl+O", false, true) {
			trkShowOpen, trkSongList = true, nil
		}
		imgui.Separator()
		if imgui.MenuItemV("Save", "Ctrl+S", false, true) {
			trkSaveCurrent()
		}
		if imgui.MenuItemV("Save As...", "Ctrl+Shift+S", false, true) {
			trkSaveAsName = trkEd.Name
			if trkSaveAsName == "" {
				trkSaveAsName = trkEd.Song.Name
			}
			trkShowSaveAs = true
		}
		editorSaveAllMenuItem()
		imgui.Separator()
		if imgui.MenuItemV("Export WAV...", "", false, trkExportResult == nil) {
			trkExportPath = trkDefaultExportPath()
			trkShowExport = true
		}
		if imgui.MenuItem("Reload Samples") {
			tracker.ReloadSamples()
			trkSampleNames = nil
			trkSampleWork = nil
			trkSyncPlayer(true)
			trkEd.Status = "Samples reloaded"
		}
		editorMenuTooltip("Read every sample from disk again — after editing one\nin another program.")
		editorCloseWindowItem(CloseTrackerEditorWindow)
		imgui.EndMenu()
	}

	if imgui.BeginMenu("Edit") {
		editorUndoRedoItems(trkEd.canUndo(), trkEd.canRedo(), trkUndo, trkRedo)
		imgui.Separator()
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
		if imgui.MenuItemV("Select All", "Ctrl+A", false, true) {
			trkEd.selectAll()
		}
		imgui.Separator()
		trkSelectionToolsMenu()
		imgui.EndMenu()
	}

	if imgui.BeginMenu("View") {
		editorMenuToggle("Follow Playback", "", &trkFollow)
		editorMenuToggle("Effects as Hex", "", &trkHexFX)
		editorMenuToggle("Pattern Overview", "", &trkShowMap)
		imgui.Separator()
		if imgui.MenuItemV("Piano Roll for This Channel", "", trkRollCh == trkEd.Cur.Ch, true) {
			if trkRollCh == trkEd.Cur.Ch {
				trkRollCh = -1
			} else {
				trkRollCh = trkEd.Cur.Ch
			}
		}
		imgui.EndMenu()
	}

	if imgui.BeginMenu("Transport") {
		if imgui.MenuItemV("Play Song", "F5", false, true) {
			trkPlaySong()
		}
		if imgui.MenuItemV("Play Pattern", "F6", false, true) {
			trkPlayPatternLoop()
		}
		if imgui.MenuItemV("Play from Cursor", "F7", false, true) {
			trkPlayFromCursor()
		}
		if imgui.MenuItemV("Stop", "F8", false, true) {
			trkStop()
		}
		imgui.EndMenu()
	}
}

// ---------- dialogs ----------

func trkDialogs() {
	imguibackend.WithModalPopup("Open Song##trkopen", &trkShowOpen, func() {
		if trkSongList == nil {
			trkSongList = tracker.ScanSongNames()
			if trkSongList == nil {
				trkSongList = []string{}
			}
		}
		if len(trkSongList) == 0 {
			editorTextMuted("No songs under sounds/ yet.")
		}
		imgui.BeginChildV("##trkopenlist", imgui.Vec2{X: 360, Y: 300}, true, 0)
		for _, name := range trkSongList {
			if !imguibackend.Selectable(name + "##trkopen") {
				continue
			}
			if err := trkLoad(name); err != nil {
				trkEd.Status = err.Error()
			}
			imgui.CloseCurrentPopup()
		}
		imgui.EndChild()
		if imgui.Button("Cancel##trkopencancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Save Song As##trksaveas", &trkShowSaveAs, func() {
		imgui.SetNextItemWidth(320)
		enter := imgui.InputTextV("Name##trksavename", &trkSaveAsName, imgui.InputTextFlagsEnterReturnsTrue, nil)
		editorTextMuted("sounds/<name>" + tracker.FileExt + "   play_music(\"<name>\")")
		if (imgui.Button("Save##trksavego") || enter) && strings.TrimSpace(trkSaveAsName) != "" {
			if err := trkSaveAs(trkSaveAsName); err != nil {
				trkEd.Status = "Save failed: " + err.Error()
			}
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##trksavecancel") {
			imgui.CloseCurrentPopup()
		}
	})

	imguibackend.WithModalPopup("Export WAV##trkexport", &trkShowExport, func() {
		imgui.SetNextItemWidth(480)
		imgui.InputTextV("Path##trkexportpath", &trkExportPath, 0, nil)
		editorTextMuted(fmt.Sprintf("One pass of the order list, %.1f s, plus a tail.", trkEd.Song.Duration()))
		if imgui.Button("Export##trkexportgo") && trkExportPath != "" {
			trkStartExport(trkExportPath)
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##trkexportcancel") {
			imgui.CloseCurrentPopup()
		}
	})

	trkEffectPickerPopup()
	trkChannelPopups()
	trkGenerateDialog()
}
