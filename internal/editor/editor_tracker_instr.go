package editor

// editor_tracker_instr.go — the right pane: the instrument list and the
// instrument editor (samples, keymap, levels, envelopes, filter, vibrato).
//
// The envelopes, the keymap and the filter are drawn rather than typed in,
// because each is a shape: a volume envelope is read as "fast attack, long
// tail" long before anyone reads its numbers. They are still edited in the
// song directly, one undo step per drag, and handed to the player like any
// other edit, so a dragged envelope is heard on the next audition.
//
// Deleting an instrument renumbers the pattern cells that name the ones
// after it (deleteInstrument), so a song never ends up with notes that
// silently changed sound.
//
// A slot can instead play an instrument from the shared library
// (sounds/instruments/*.finst, picked in the Library combo). Such a slot is
// read-only here — it plays what the file says, the same in every song and
// score that names it — until "Make local copy" embeds it, and an embedded
// instrument can be saved to the library for others to use.

import (
	"fmt"
	"math"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// trkSampleNames caches the library listing for the sample pickers.
var trkSampleNames []string

func trkLibrarySamples() []string {
	if trkSampleNames == nil {
		trkSampleNames = tracker.ScanSampleNames()
		if trkSampleNames == nil {
			trkSampleNames = []string{}
		}
	}
	return trkSampleNames
}

// trkInstrNames caches the instrument library listing for the Library combo.
var trkInstrNames []string

func trkLibraryInstruments() []string {
	if trkInstrNames == nil {
		trkInstrNames = tracker.ScanInstrumentNames()
		if trkInstrNames == nil {
			trkInstrNames = []string{}
		}
	}
	return trkInstrNames
}

// trkInstrument is the current instrument, or nil.
func trkInstrument() *tracker.Instrument {
	return trkEd.Song.Instrument(uint8(max(0, trkEd.Instr)))
}

func trkInstrumentPane() {
	w := trackerEditorWindow
	x := float32(w.W) - trkRightW
	imgui.SetNextWindowPos(imgui.Vec2{X: x, Y: trkToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: trkRightW, Y: float32(w.H) - trkToolbarH})
	if imgui.BeginV("##trkinstr", nil, trkPaneFlags) {
		trkPaneClickTakesFocus()
		trkInstrumentList()
		imgui.Separator()
		if in := trkInstrument(); in != nil {
			trkInstrumentEditor(in)
		}
		pos, size := imgui.WindowPos(), imgui.WindowSize()
		trkSplitter("##trksplitright", imgui.Vec2{X: pos.X, Y: pos.Y}, imgui.Vec2{X: pos.X + 4, Y: pos.Y + size.Y}, true, &trkRightW, -1)
	}
	imgui.End()
}

var (
	trkRenamingInstr = -1
	trkInstrRename   string
	// trkInstrNameBuf is the instrument editor's Name field while it is being
	// typed in; between edits it follows the instrument's own name.
	trkInstrNameBuf    string
	trkInstrNameActive bool
	// trkInstrNameFor is the instrument the field was typed for. Clicking
	// another instrument in the list ends the edit on the same frame the
	// selection changes, so committing to trkEd.Instr would rename the one
	// just clicked.
	trkInstrNameFor int
)

// trkRenameInstrument names instrument n (1-based). A blank name, or the name
// it already has, changes nothing. Cells refer to an instrument by number, so
// the pattern is unaffected; a script's play_note that names it by its old
// name will no longer find it.
func trkRenameInstrument(n int, name string) bool {
	in := trkEd.Song.Instrument(uint8(max(0, n)))
	name = strings.TrimSpace(name)
	if in == nil || name == "" || name == in.Name {
		return false
	}
	trkEd.pushUndo("")
	in.Name = name
	trkEd.touch()
	return true
}

func trkInstrumentList() {
	s := trkEd.Song
	imgui.Text("Instruments")
	imgui.SameLine()
	if trkSmallButton("+##trkinadd") {
		trkEd.addInstrument()
	}
	editorMenuTooltip("Add an instrument")
	imgui.SameLine()
	imguibackend.EditorPushDisabled(trkEd.Instr <= 0)
	if trkSmallButton("Dup##trkindup") {
		trkEd.duplicateInstrument(trkEd.Instr)
	}
	imgui.SameLine()
	if trkSmallButton("Del##trkindel") {
		trkEd.deleteInstrument(trkEd.Instr)
		trkAfterStructural()
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHoveredV(imgui.HoveredFlagsAllowWhenDisabled) {
		imgui.SetTooltip("Delete this instrument. Notes that play it are left\nwithout one; later instruments are renumbered.")
	}

	imgui.BeginChildV("##trkinlist", imgui.Vec2{X: 0, Y: 150}, true, 0)
	for i, in := range s.Instruments {
		n := i + 1
		imgui.PushIDInt(n)
		if trkSmallButton(">") {
			trkSelectInstrument(n)
			trkAuditionInstrument(n)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Play C-4")
		}
		imgui.SameLine()
		if trkRenamingInstr == n {
			imgui.SetNextItemWidth(-1)
			if imgui.IsWindowAppearing() || !imgui.IsAnyItemActive() {
				imgui.SetKeyboardFocusHere()
			}
			// Enter or clicking away keeps the name; Escape puts the old one
			// back (imgui reverts the text, so the rename is a no-op).
			imgui.InputTextV("##trkinrename", &trkInstrRename, imgui.InputTextFlagsAutoSelectAll, nil)
			if imgui.IsItemDeactivated() {
				if imgui.IsItemDeactivatedAfterEdit() {
					trkRenameInstrument(n, trkInstrRename)
				}
				trkRenamingInstr = -1
			}
		} else {
			missing, lib := "", ""
			if len(in.Samples) == 0 || in.Loaded(0) == nil {
				missing = "  (silent)"
			}
			if in.Ref != "" {
				lib = "  [" + in.Ref + "]"
			}
			if imgui.SelectableV(fmt.Sprintf("%02X  %s%s%s", n, in.Name, lib, missing), n == trkEd.Instr, 0, imgui.Vec2{}) {
				trkSelectInstrument(n)
			}
			if imgui.IsItemHovered() && imgui.IsMouseDoubleClicked(0) {
				trkRenamingInstr, trkInstrRename = n, in.Name
			}
			if imgui.IsItemHovered() {
				if missing != "" && in.Ref != "" {
					imgui.SetTooltip("Library instrument " + in.Ref + " did not load\nDouble-click to rename")
				} else if missing != "" {
					imgui.SetTooltip("No sample loaded: " + strings.Join(in.Samples, ", ") + "\nDouble-click to rename")
				} else if in.Ref != "" {
					imgui.SetTooltip("Plays library instrument " + in.Ref + "\nDouble-click to rename")
				} else {
					imgui.SetTooltip("Double-click to rename")
				}
			}
		}
		imgui.PopID()
	}
	imgui.EndChild()
}

// trkAuditionInstrument plays C-4 on instrument n for a moment.
func trkAuditionInstrument(n int) {
	trkSyncPlayer(false)
	if trkPlayer != nil {
		trkPlayer.NoteFor(uint8(n), tracker.NoteC4, 1, 0.6)
	}
}

// ---------- instrument operations ----------

func (t *trackerState) addInstrument() {
	if len(t.Song.Instruments) >= tracker.MaxInstruments {
		return
	}
	t.pushUndo("")
	t.Song.Instruments = append(t.Song.Instruments, tracker.NewInstrument(fmt.Sprintf("Instrument %d", len(t.Song.Instruments)+1), ""))
	t.Instr = len(t.Song.Instruments)
	t.touch()
}

func (t *trackerState) duplicateInstrument(n int) {
	in := t.Song.Instrument(uint8(max(0, n)))
	if in == nil || len(t.Song.Instruments) >= tracker.MaxInstruments {
		return
	}
	t.pushUndo("")
	c := in.Clone()
	c.Name += " copy"
	t.Song.Instruments = append(t.Song.Instruments, c)
	t.Instr = len(t.Song.Instruments)
	t.touch()
}

// deleteInstrument removes instrument n (1-based) and renumbers every cell:
// notes on it lose their instrument, notes on later ones follow them down.
func (t *trackerState) deleteInstrument(n int) {
	if n <= 0 || n > len(t.Song.Instruments) {
		return
	}
	t.pushUndo("")
	t.Song.Instruments = append(t.Song.Instruments[:n-1], t.Song.Instruments[n:]...)
	for _, p := range t.Song.Patterns {
		for r := range p.Cells {
			for c := range p.Cells[r] {
				cell := &p.Cells[r][c]
				switch {
				case int(cell.Instr) == n:
					cell.Instr = 0
				case int(cell.Instr) > n:
					cell.Instr--
				}
			}
		}
	}
	if t.Instr > len(t.Song.Instruments) {
		t.Instr = len(t.Song.Instruments)
	}
	t.touch()
}

// ---------- library instruments ----------

// trkDefaultInstrName is how addInstrument names a new slot; a slot still
// called that takes the library instrument's own name when it is set.
func trkDefaultInstrName(name string) bool {
	var n int
	_, err := fmt.Sscanf(name, "Instrument %d", &n)
	return name == "" || err == nil && fmt.Sprintf("Instrument %d", n) == name
}

// setInstrumentLibrary makes slot n (1-based) play library instrument name;
// "" makes it the song's own again, keeping what it played (embedInstrument).
func (t *trackerState) setInstrumentLibrary(n int, name string) error {
	in := t.Song.Instrument(uint8(max(0, n)))
	if in == nil {
		return fmt.Errorf("no instrument %d", n)
	}
	if name == "" {
		t.embedInstrument(n)
		return nil
	}
	if name == in.Ref {
		return nil
	}
	lib, err := tracker.LoadInstrument(name)
	if err != nil {
		return err
	}
	t.pushUndo("")
	keep := in.Name
	if trkDefaultInstrName(keep) {
		keep = lib.Name
	}
	if err := in.UseLibrary(name); err != nil {
		return err
	}
	in.Name = keep
	t.touch()
	return nil
}

// embedInstrument copies a library slot into the song, so it can be edited
// without touching the library.
func (t *trackerState) embedInstrument(n int) {
	in := t.Song.Instrument(uint8(max(0, n)))
	if in == nil || in.Ref == "" {
		return
	}
	t.pushUndo("")
	in.Embed()
	t.touch()
}

// saveInstrumentToLibrary writes slot n into the library as name
// ("category/name"), and the slot then plays the saved file.
func (t *trackerState) saveInstrumentToLibrary(n int, name string) error {
	in := t.Song.Instrument(uint8(max(0, n)))
	if in == nil {
		return fmt.Errorf("no instrument %d", n)
	}
	name = strings.Trim(strings.TrimSpace(strings.TrimSuffix(name, tracker.InstrumentExt)), "/")
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `\:`) {
		return fmt.Errorf("name the instrument as category/name")
	}
	if !strings.Contains(name, "/") {
		name = "custom/" + name
	}
	if err := tracker.SaveInstrument(name, in); err != nil {
		return err
	}
	trkInstrNames = nil
	t.pushUndo("")
	in.Ref = name
	t.touch()
	return nil
}

var (
	trkLibSaveName string
	trkLibStatus   string
)

// trkInstrLibrary is the Library row: which library instrument the slot
// plays, if any, and what a library slot is.
func trkInstrLibrary(in *tracker.Instrument) {
	preview := "(this song's own)"
	if in.Ref != "" {
		preview = in.Ref
	}
	imgui.SetNextItemWidth(-60)
	if imguibackend.BeginCombo("Library##trkinlib", preview) {
		if imguibackend.SelectableV("(this song's own)", in.Ref == "", 0, imgui.Vec2{}) && in.Ref != "" {
			trkEd.embedInstrument(trkEd.Instr)
		}
		for _, name := range trkLibraryInstruments() {
			if imguibackend.SelectableV(name, name == in.Ref, 0, imgui.Vec2{}) && name != in.Ref {
				if err := trkEd.setInstrumentLibrary(trkEd.Instr, name); err != nil {
					trkEd.Status = err.Error()
				} else {
					trkEd.Status = "Instrument plays " + name
					trkAuditionInstrument(trkEd.Instr)
				}
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("Play an instrument from the shared library (sounds/instruments/).\nIt sounds the same in every song and score that uses it.")
	if in.Ref == "" {
		imgui.SetNextItemWidth(-110)
		imgui.InputTextWithHint("##trklibsave", "category/name", &trkLibSaveName)
		imgui.SameLine()
		imguibackend.EditorPushDisabled(strings.TrimSpace(trkLibSaveName) == "")
		if trkSmallButton("Save to library##trklibsavebtn") {
			if err := trkEd.saveInstrumentToLibrary(trkEd.Instr, trkLibSaveName); err != nil {
				trkLibStatus = err.Error()
			} else {
				trkLibStatus, trkLibSaveName = "Saved as "+in.Ref, ""
			}
		}
		imguibackend.EditorPopDisabled()
		editorMenuTooltip("Write this instrument to sounds/instruments/<category>/<name>.finst\nand have this slot play the saved file.")
		if trkLibStatus != "" {
			editorTextMuted(trkLibStatus)
		}
		return
	}
	if in.Description != "" {
		editorTextMutedWrapped(in.Description)
	}
	if trkSmallButton("Make local copy##trkinembed") {
		trkEd.embedInstrument(trkEd.Instr)
	}
	editorMenuTooltip("Copy the library instrument into this song so it can be edited\nhere. The library file is not changed.")
	trkInstrInfo(in)
}

// trkInstrInfo shows what a library instrument carries beyond a Tracker
// instrument: its articulations (the @ command's numbers), velocity layers,
// round robin and notation defaults.
func trkInstrInfo(in *tracker.Instrument) {
	layers, rr := 0, 0
	for _, z := range in.Zones {
		layers = max(layers, len(z.Layers))
		rr = max(rr, len(z.RoundRobin)+1)
		for _, l := range z.Layers {
			rr = max(rr, len(l.RoundRobin)+1)
		}
	}
	editorTextMuted(fmt.Sprintf("%d samples, %d zones", len(in.Samples), len(in.Zones)))
	if layers > 1 {
		editorTextMuted(fmt.Sprintf("%d velocity layers (the volume column picks one)", layers))
	}
	if rr > 1 {
		editorTextMuted(fmt.Sprintf("round robin of %d takes", rr))
	}
	if len(in.Articulations) > 0 {
		editorTextMuted("@00 default")
		for i, a := range in.Articulations {
			line := fmt.Sprintf("@%02X %s", i+1, a.Name)
			if len(a.Triggers) > 0 {
				line += "  (" + strings.Join(a.Triggers, ", ") + ")"
			}
			editorTextMuted(line)
		}
	}
	pb := in.Playback
	var parts []string
	if pb.Low != 0 || pb.High != 0 {
		parts = append(parts, "range "+tracker.NoteName(max(pb.Low, tracker.NoteMin))+".."+tracker.NoteName(max(pb.High, pb.Low)))
	}
	if pb.Clef != "" {
		parts = append(parts, pb.Clef+" clef")
	}
	if pb.Transposition != 0 {
		parts = append(parts, fmt.Sprintf("sounds %+d st from written", pb.Transposition))
	}
	if len(parts) > 0 {
		editorTextMutedWrapped(strings.Join(parts, ", "))
	}
}

// ---------- the editor ----------

func trkInstrumentEditor(in *tracker.Instrument) {
	imgui.Text(fmt.Sprintf("%02X", trkEd.Instr))
	imgui.SameLine()
	imgui.SetNextItemWidth(-60)
	if !trkInstrNameActive {
		trkInstrNameBuf, trkInstrNameFor = in.Name, trkEd.Instr
	}
	imgui.InputTextV("Name##trkinname", &trkInstrNameBuf, 0, nil)
	trkInstrNameActive = imgui.IsItemActive()
	if imgui.IsItemDeactivatedAfterEdit() {
		trkRenameInstrument(trkInstrNameFor, trkInstrNameBuf)
	}
	editorMenuTooltip("The instrument's name. Enter or clicking away keeps it; Escape cancels.")
	trkInstrLibrary(in)
	if in.Ref != "" {
		// A library slot plays its file; edits here would be lost on the
		// next resolve, so there is nothing to edit until it is embedded.
		return
	}
	// Velocity layers, round robin and articulations, edited
	// (editor_tracker_layers.go); open when the instrument has any.
	flags := imgui.TreeNodeFlags(0)
	if len(in.Articulations) > 0 || trkHasLayers(in) {
		flags = imgui.TreeNodeFlagsDefaultOpen
	}
	if imgui.CollapsingHeaderV("Layers & articulations##trkinarts", flags) {
		trkLayersSection(in)
	}
	if imgui.CollapsingHeaderV("Samples & keymap##trkinsamp", imgui.TreeNodeFlagsDefaultOpen) {
		trkInstrSamples(in)
		trkKeymap(in)
	}
	if imgui.CollapsingHeaderV("Levels##trkinlvl", imgui.TreeNodeFlagsDefaultOpen) {
		imgui.PushItemWidth(-90)
		v := in.Volume
		if trkEdited(imgui.SliderFloatV("Volume##trkinvol", &v, 0, 1, "%.2f", 0)) {
			in.Volume = v
		}
		p := in.Pan
		if trkEdited(imgui.SliderFloatV("Pan##trkinpan", &p, -1, 1, "%+.2f", 0)) {
			in.Pan = p
		}
		tr := int32(in.Transpose)
		if trkEdited(imgui.SliderIntV("Transpose##trkintr", &tr, -48, 48, "%+d st", 0)) {
			in.Transpose = int(tr)
		}
		ft := in.FineTune
		if trkEdited(imgui.SliderFloatV("Fine tune##trkinft", &ft, -100, 100, "%+.0f cents", 0)) {
			in.FineTune = ft
		}
		fo := in.FadeOut
		if trkEdited(imgui.DragFloatV("Fade out##trkinfo", &fo, 0.01, 0, 20, "%.2f s", 0)) {
			in.FadeOut = fo
		}
		editorMenuTooltip("Release after a note-off, when the volume envelope has no sustain")
		poly := int32(max(1, in.Polyphony))
		if trkEdited(imgui.SliderIntV("Polyphony##trkinpoly", &poly, 1, 8, "%d", 0)) {
			in.Polyphony = int(poly)
		}
		editorMenuTooltip("Above 1, a new note lets the old one ring out instead of cutting it")
		interp := []string{"Song default", "Nearest", "Linear", "Cubic"}
		cur := int32(in.Interp)
		if imguibackend.Combo("Interpolation##trkininterp", &cur, interp) {
			trkEd.pushUndo("")
			in.Interp = tracker.Interp(cur)
			trkEd.touch()
		}
		imgui.PopItemWidth()
	}
	if imgui.CollapsingHeaderV("Envelopes##trkinenv", imgui.TreeNodeFlagsDefaultOpen) {
		trkEnvelopeTabs(in)
	}
	if imgui.CollapsingHeaderV("Filter##trkinflt", 0) {
		trkInstrFilter(in)
	}
	if imgui.CollapsingHeaderV("Auto-vibrato##trkinvib", 0) {
		imgui.PushItemWidth(-90)
		r := in.VibRate
		if trkEdited(imgui.SliderFloatV("Rate##trkvibrate", &r, 0, 20, "%.1f Hz", 0)) {
			in.VibRate = r
		}
		d := in.VibDepth
		if trkEdited(imgui.SliderFloatV("Depth##trkvibdepth", &d, 0, 2, "%.2f st", 0)) {
			in.VibDepth = d
		}
		dl := in.VibDelay
		if trkEdited(imgui.SliderFloatV("Delay##trkvibdelay", &dl, 0, 4, "%.2f s", 0)) {
			in.VibDelay = dl
		}
		imgui.PopItemWidth()
	}
}

// trkInstrSamples is the instrument's sample slots, each picked from the
// library.
func trkInstrSamples(in *tracker.Instrument) {
	names := trkLibrarySamples()
	for i := range in.Samples {
		imgui.PushIDInt(i)
		imgui.SetNextItemWidth(-50)
		preview := in.Samples[i]
		if preview == "" {
			preview = "(none)"
		}
		if imguibackend.BeginCombo("##trksamp", preview) {
			for _, n := range names {
				if imguibackend.SelectableV(n, n == in.Samples[i], 0, imgui.Vec2{}) && n != in.Samples[i] {
					trkEd.pushUndo("")
					in.Samples[i] = n
					in.Resolve(tracker.SampleFor)
					trkSampleWork = nil
					trkEd.touch()
				}
			}
			imguibackend.EndCombo()
		}
		if in.Loaded(i) == nil && in.Samples[i] != "" && imgui.IsItemHovered() {
			imgui.SetTooltip("Not in the library yet — this slot plays silence.")
		}
		imgui.SameLine()
		if trkSmallButton("x") {
			trkEd.pushUndo("")
			// Zones, layers and articulations past the slot move down with it.
			in.RemoveSample(i)
			in.Resolve(tracker.SampleFor)
			trkEd.touch()
			imgui.PopID()
			break
		}
		imgui.PopID()
	}
	if trkSmallButton("Add sample##trkaddsamp") {
		trkEd.pushUndo("")
		in.Samples = append(in.Samples, "")
		trkEd.touch()
	}
}

func trkHasLayers(in *tracker.Instrument) bool {
	for _, z := range in.Zones {
		if len(z.Layers) > 0 || len(z.RoundRobin) > 0 {
			return true
		}
	}
	return false
}

// ---------- keymap ----------

var trkZoneDrag = struct {
	zone, edge int // edge 0 low, 1 high; zone -1 when idle
}{zone: -1}

// trkKeymap draws the zones as bars over a piano strip; each bar's ends drag.
func trkKeymap(in *tracker.Instrument) {
	if len(in.Samples) < 2 && len(in.Zones) == 0 {
		if len(in.Samples) == 1 && trkSmallButton("Split into zones##trkzonesplit") {
			trkEd.pushUndo("")
			in.Zones = []tracker.Zone{{Sample: 0, Low: tracker.NoteMin, High: tracker.NoteMax}}
			trkEd.touch()
		}
		return
	}
	width := imgui.ContentRegionAvail().X
	const keyH, barH = float32(26), float32(12)
	p := imgui.CursorScreenPos()
	height := keyH + float32(len(in.Zones))*(barH+2) + 4
	imgui.InvisibleButton("##trkkeymap", imgui.Vec2{X: width, Y: height})
	hovered := imgui.IsItemHovered()
	dl := imgui.WindowDrawList()
	nkeys := int(tracker.NoteMax)
	kw := width / float32(nkeys)
	noteX := func(n uint8) float32 { return p.X + float32(int(n)-1)*kw }
	// Zones.
	for zi, z := range in.Zones {
		y := p.Y + float32(zi)*(barH+2)
		x0, x1 := noteX(z.Low), noteX(z.High)+kw
		dl.AddRectFilledV(imgui.Vec2{X: x0, Y: y}, imgui.Vec2{X: x1, Y: y + barH}, trkSectionColor(z.Sample, 0.75), 2, imgui.DrawFlagsRoundCornersAll)
		label := fmt.Sprintf("%d", z.Sample+1)
		dl.AddText(imgui.Vec2{X: x0 + 2, Y: y - 1}, trkColText, label)
	}
	// Keys.
	ky := p.Y + float32(len(in.Zones))*(barH+2) + 2
	for n := 1; n <= nkeys; n++ {
		pc := (n - 1) % 12
		black := pc == 1 || pc == 3 || pc == 6 || pc == 8 || pc == 10
		x := noteX(uint8(n))
		col := trkColKeyWhite
		h := keyH
		if black {
			col, h = trkColKeyBlack, keyH*0.6
		}
		dl.AddRectFilled(imgui.Vec2{X: x, Y: ky}, imgui.Vec2{X: x + kw - 0.5, Y: ky + h}, col)
		if pc == 0 && kw*12 > 30 {
			dl.AddText(imgui.Vec2{X: x, Y: ky + keyH - 13}, trkColBg, fmt.Sprintf("%d", (n-1)/12))
		}
	}
	mouse := imgui.MousePos()
	note := uint8(clampI(int((mouse.X-p.X)/kw)+1, int(tracker.NoteMin), int(tracker.NoteMax)))
	if hovered && trkZoneDrag.zone < 0 {
		zi := int((mouse.Y - p.Y) / (barH + 2))
		if zi >= 0 && zi < len(in.Zones) {
			z := in.Zones[zi]
			dLow := absF32(mouse.X - noteX(z.Low))
			dHigh := absF32(mouse.X - (noteX(z.High) + kw))
			if dLow < 6 || dHigh < 6 {
				imgui.SetMouseCursor(imgui.MouseCursorResizeEW)
				if imgui.IsMouseClicked(0) {
					edge := 0
					if dHigh < dLow {
						edge = 1
					}
					trkEd.pushUndo("")
					trkZoneDrag.zone, trkZoneDrag.edge = zi, edge
				}
			}
			imgui.SetTooltip(fmt.Sprintf("Sample %d: %s..%s", z.Sample+1, tracker.NoteName(z.Low), tracker.NoteName(z.High)))
		} else if mouse.Y >= ky {
			imgui.SetTooltip(tracker.NoteName(note))
			if imgui.IsMouseClicked(0) {
				trkAudition(-101, trkEd.Instr, note)
			}
		}
	}
	if imgui.IsMouseReleased(0) {
		trkAuditionOff(-101)
	}
	if trkZoneDrag.zone >= 0 {
		if !imgui.IsMouseDown(0) || trkZoneDrag.zone >= len(in.Zones) {
			trkZoneDrag.zone = -1
		} else {
			z := &in.Zones[trkZoneDrag.zone]
			if trkZoneDrag.edge == 0 {
				z.Low = min(note, z.High)
			} else {
				z.High = max(note, z.Low)
			}
			trkEd.touch()
		}
	}
	// Zone rows: sample and delete.
	for zi := range in.Zones {
		z := &in.Zones[zi]
		imgui.PushIDInt(1000 + zi)
		imgui.SetNextItemWidth(120)
		preview := fmt.Sprintf("Sample %d", z.Sample+1)
		if imguibackend.BeginCombo("##trkzonesamp", preview) {
			for si, name := range in.Samples {
				if imguibackend.SelectableV(fmt.Sprintf("%d  %s", si+1, name), si == z.Sample, 0, imgui.Vec2{}) {
					trkEd.pushUndo("")
					z.Sample = si
					trkEd.touch()
				}
			}
			imguibackend.EndCombo()
		}
		imgui.SameLine()
		span := tracker.NoteName(z.Low) + " .. " + tracker.NoteName(z.High)
		if len(z.Layers) > 0 {
			span += fmt.Sprintf("  %d layers", len(z.Layers))
		}
		if len(z.RoundRobin) > 0 {
			span += fmt.Sprintf("  rr %d", len(z.RoundRobin)+1)
		}
		imgui.Text(span)
		imgui.SameLine()
		if trkSmallButton("x") {
			trkEd.pushUndo("")
			in.Zones = append(in.Zones[:zi], in.Zones[zi+1:]...)
			trkEd.touch()
			imgui.PopID()
			break
		}
		imgui.PopID()
	}
	if trkSmallButton("Add zone##trkaddzone") {
		trkEd.pushUndo("")
		in.Zones = append(in.Zones, tracker.Zone{Sample: max(0, len(in.Samples)-1), Low: tracker.NoteC4, High: tracker.NoteC4 + 11})
		trkEd.touch()
	}
}

// ---------- envelopes ----------

// trkEnvKind names one of an instrument's four envelopes and its value range.
type trkEnvKind struct {
	Name     string
	Lo, Hi   float32
	Unit     string
	Default  []tracker.EnvPoint
	Envelope func(in *tracker.Instrument) *tracker.Envelope
}

var trkEnvKinds = []trkEnvKind{
	{"Volume", 0, 1, "", []tracker.EnvPoint{{T: 0, V: 0}, {T: 0.02, V: 1}, {T: 0.5, V: 0.7}, {T: 1.5, V: 0}},
		func(in *tracker.Instrument) *tracker.Envelope { return &in.VolEnv }},
	{"Pan", -1, 1, "", []tracker.EnvPoint{{T: 0, V: 0}, {T: 1, V: 0}},
		func(in *tracker.Instrument) *tracker.Envelope { return &in.PanEnv }},
	{"Pitch", -12, 12, " st", []tracker.EnvPoint{{T: 0, V: 0}, {T: 1, V: 0}},
		func(in *tracker.Instrument) *tracker.Envelope { return &in.PitchEnv }},
	{"Filter", -4, 4, " oct", []tracker.EnvPoint{{T: 0, V: 0}, {T: 1, V: 0}},
		func(in *tracker.Instrument) *tracker.Envelope { return &in.FilterEnv }},
}

// trkEnvStash keeps an envelope's points while it is switched off, so the
// toggle is not destructive within a session. View state, keyed by the
// instrument's name and the envelope.
var trkEnvStash = map[string][]tracker.EnvPoint{}

var (
	trkEnvTab  int
	trkEnvDrag = -1
)

func trkEnvelopeTabs(in *tracker.Instrument) {
	for i, k := range trkEnvKinds {
		if i > 0 {
			imgui.SameLine()
		}
		label := k.Name
		if k.Envelope(in).On() {
			label += "*"
		}
		if editorActiveToggleButtonColor(label+"##trkenvtab"+k.Name, trkEnvTab == i, trkColActiveButton) {
			trkEnvTab = i
			trkEnvDrag = -1
		}
	}
	k := trkEnvKinds[trkEnvTab]
	env := k.Envelope(in)
	stashKey := in.Name + "/" + k.Name
	on := env.On()
	if imgui.Checkbox("On##trkenvon", &on) {
		trkEd.pushUndo("")
		if on {
			pts := trkEnvStash[stashKey]
			if len(pts) == 0 {
				pts = k.Default
			}
			env.Points = append([]tracker.EnvPoint(nil), pts...)
		} else {
			trkEnvStash[stashKey] = append([]tracker.EnvPoint(nil), env.Points...)
			env.Points = nil
			env.Sustain, env.LoopStart, env.LoopEnd = -1, -1, -1
		}
		trkEd.touch()
	}
	if !env.On() {
		return
	}
	trkEnvelopeCanvas(env, k)
	// Sustain and loop, by point.
	n := int32(len(env.Points) - 1)
	imgui.PushItemWidth(80)
	sus := int32(env.Sustain)
	if trkEdited(imgui.SliderIntV("Sustain##trkenvsus", &sus, -1, n, trkPointFormat(int(sus)), 0)) {
		env.Sustain = int(sus)
	}
	imgui.SameLine()
	ls := int32(env.LoopStart)
	if trkEdited(imgui.SliderIntV("Loop##trkenvls", &ls, -1, n, trkPointFormat(int(ls)), 0)) {
		env.LoopStart = int(ls)
		if env.LoopEnd <= env.LoopStart {
			env.LoopEnd = min(int(n), env.LoopStart+1)
		}
	}
	imgui.SameLine()
	le := int32(env.LoopEnd)
	if trkEdited(imgui.SliderIntV("to##trkenvle", &le, -1, n, trkPointFormat(int(le)), 0)) {
		env.LoopEnd = int(le)
	}
	imgui.PopItemWidth()
}

func trkPointFormat(i int) string {
	if i < 0 {
		return "off"
	}
	return "pt %d"
}

// trkEnvelopeCanvas draws and edits one envelope: drag a point, double-click
// to add, right-click to delete. Sustain is a dashed line, the loop a band.
func trkEnvelopeCanvas(env *tracker.Envelope, k trkEnvKind) {
	width := imgui.ContentRegionAvail().X
	const height = float32(110)
	p := imgui.CursorScreenPos()
	imgui.InvisibleButtonV("##trkenvcanvas", imgui.Vec2{X: width, Y: height}, imgui.ButtonFlagsMouseButtonLeft|imgui.ButtonFlagsMouseButtonRight)
	hovered := imgui.IsItemHovered()
	dl := imgui.WindowDrawList()
	end := imgui.Vec2{X: p.X + width, Y: p.Y + height}
	dl.AddRectFilledV(p, end, trkColBg, 3, imgui.DrawFlagsRoundCornersAll)
	pts := env.Points
	span := float32(1)
	for _, pt := range pts {
		span = max32(span, pt.T*1.2)
	}
	toScreen := func(pt tracker.EnvPoint) imgui.Vec2 {
		return imgui.Vec2{
			X: p.X + 6 + pt.T/span*(width-12),
			Y: p.Y + 6 + (1-(pt.V-k.Lo)/(k.Hi-k.Lo))*(height-12),
		}
	}
	fromScreen := func(v imgui.Vec2) tracker.EnvPoint {
		return tracker.EnvPoint{
			T: clampF32((v.X-p.X-6)/(width-12)*span, 0, 60),
			V: clampF32(k.Lo+(1-(v.Y-p.Y-6)/(height-12))*(k.Hi-k.Lo), k.Lo, k.Hi),
		}
	}
	// Zero line for bipolar envelopes.
	if k.Lo < 0 {
		y := toScreen(tracker.EnvPoint{V: 0}).Y
		dl.AddLine(imgui.Vec2{X: p.X, Y: y}, imgui.Vec2{X: end.X, Y: y}, trkCol(1, 1, 1, 0.1))
	}
	// Loop band and sustain line.
	if env.LoopStart >= 0 && env.LoopEnd > env.LoopStart && env.LoopEnd < len(pts) {
		a, b := toScreen(pts[env.LoopStart]), toScreen(pts[env.LoopEnd])
		dl.AddRectFilled(imgui.Vec2{X: a.X, Y: p.Y}, imgui.Vec2{X: b.X, Y: end.Y}, trkCol(0.45, 0.65, 1, 0.12))
	}
	if env.Sustain >= 0 && env.Sustain < len(pts) {
		x := toScreen(pts[env.Sustain]).X
		for y := p.Y; y < end.Y; y += 6 {
			dl.AddLine(imgui.Vec2{X: x, Y: y}, imgui.Vec2{X: x, Y: min(y+3, end.Y)}, trkColSolo)
		}
	}
	// Curve and points.
	hot := -1
	mouse := imgui.MousePos()
	for i, pt := range pts {
		s := toScreen(pt)
		if i > 0 {
			dl.AddLineV(toScreen(pts[i-1]), s, trkColVol, 2)
		}
		if hovered && absF32(mouse.X-s.X) < 6 && absF32(mouse.Y-s.Y) < 6 {
			hot = i
		}
	}
	for i, pt := range pts {
		col := trkColText
		if i == hot || i == trkEnvDrag {
			col = trkColSelEdge
		}
		dl.AddCircleFilled(toScreen(pt), 4, col)
	}
	switch {
	case hovered && imgui.IsMouseDoubleClicked(0) && hot < 0:
		np := fromScreen(mouse)
		trkEd.pushUndo("")
		at := len(pts)
		for i, pt := range pts {
			if pt.T > np.T {
				at = i
				break
			}
		}
		env.Points = append(env.Points, tracker.EnvPoint{})
		copy(env.Points[at+1:], env.Points[at:])
		env.Points[at] = np
		trkEnvShiftIndices(env, at, 1)
		trkEd.touch()
	case hovered && imgui.IsMouseClicked(1) && hot >= 0 && len(pts) > 1:
		trkEd.pushUndo("")
		env.Points = append(env.Points[:hot], env.Points[hot+1:]...)
		trkEnvShiftIndices(env, hot, -1)
		trkEd.touch()
	case hovered && imgui.IsMouseClicked(0) && hot >= 0:
		trkEd.pushUndo("")
		trkEnvDrag = hot
	}
	if trkEnvDrag >= 0 {
		if !imgui.IsMouseDown(0) || trkEnvDrag >= len(env.Points) {
			trkEnvDrag = -1
		} else {
			np := fromScreen(mouse)
			// Points stay in time order: a point cannot pass its neighbours,
			// and the first stays at 0 so the envelope starts with the note.
			lo, hi := float32(0), float32(60)
			if trkEnvDrag > 0 {
				lo = env.Points[trkEnvDrag-1].T
			}
			if trkEnvDrag < len(env.Points)-1 {
				hi = env.Points[trkEnvDrag+1].T
			}
			if trkEnvDrag == 0 {
				hi = 0
			}
			np.T = clampF32(np.T, lo, hi)
			env.Points[trkEnvDrag] = np
			trkEd.touch()
		}
	}
	if hot >= 0 {
		pt := pts[hot]
		imgui.SetTooltip(fmt.Sprintf("%.2f s  %.2f%s", pt.T, pt.V, k.Unit))
	}
}

// trkEnvShiftIndices keeps sustain and loop pointing at the same points when
// one is inserted (delta 1) or removed (delta -1) at index at.
func trkEnvShiftIndices(env *tracker.Envelope, at, delta int) {
	fix := func(i *int) {
		switch {
		case *i < 0:
		case delta < 0 && *i == at:
			*i = -1
		case *i >= at:
			*i += delta
		}
	}
	fix(&env.Sustain)
	fix(&env.LoopStart)
	fix(&env.LoopEnd)
	if env.LoopStart < 0 || env.LoopEnd <= env.LoopStart {
		env.LoopStart, env.LoopEnd = -1, -1
	}
}

// ---------- filter ----------

func trkInstrFilter(in *tracker.Instrument) {
	modes := []string{"Off", "Low-pass", "High-pass", "Band-pass"}
	cur := int32(in.Filter)
	imgui.PushItemWidth(-90)
	if imguibackend.Combo("Mode##trkfltmode", &cur, modes) {
		trkEd.pushUndo("")
		in.Filter = tracker.FilterMode(cur)
		if in.Cutoff <= 0 {
			in.Cutoff = 2000
		}
		trkEd.touch()
	}
	if in.Filter != tracker.FilterOff {
		c := in.Cutoff
		if trkEdited(imgui.SliderFloatV("Cutoff##trkfltcut", &c, 20, 20000, "%.0f Hz", imgui.SliderFlagsLogarithmic)) {
			in.Cutoff = c
		}
		r := in.Resonance
		if trkEdited(imgui.SliderFloatV("Resonance##trkfltres", &r, 0, 1, "%.2f", 0)) {
			in.Resonance = r
		}
	}
	imgui.PopItemWidth()
	trkFilterCurve(in)
}

// trkFilterResponse is the filter's magnitude in dB at f Hz: the textbook
// two-pole state-variable responses, close enough to show the shape the
// player's SVF has.
func trkFilterResponse(mode tracker.FilterMode, cutoff, res float32, f float64) float64 {
	if mode == tracker.FilterOff || cutoff <= 0 {
		return 0
	}
	q := 0.5 + float64(res)*12
	w := f / float64(cutoff)
	den := math.Sqrt((1-w*w)*(1-w*w) + (w/q)*(w/q))
	var mag float64
	switch mode {
	case tracker.FilterLowPass:
		mag = 1 / den
	case tracker.FilterHighPass:
		mag = w * w / den
	case tracker.FilterBandPass:
		mag = (w / q) / den
	}
	if mag < 1e-6 {
		mag = 1e-6
	}
	return 20 * math.Log10(mag)
}

func trkFilterCurve(in *tracker.Instrument) {
	width := imgui.ContentRegionAvail().X
	const height = float32(70)
	p := imgui.CursorScreenPos()
	imgui.Dummy(imgui.Vec2{X: width, Y: height})
	dl := imgui.WindowDrawList()
	end := imgui.Vec2{X: p.X + width, Y: p.Y + height}
	dl.AddRectFilledV(p, end, trkColBg, 3, imgui.DrawFlagsRoundCornersAll)
	const dbTop, dbBot = 18.0, -36.0
	y0 := p.Y + float32((dbTop-0)/(dbTop-dbBot))*height
	dl.AddLine(imgui.Vec2{X: p.X, Y: y0}, imgui.Vec2{X: end.X, Y: y0}, trkCol(1, 1, 1, 0.1))
	var prev imgui.Vec2
	for i := 0; i <= 120; i++ {
		f := 20 * math.Pow(1000, float64(i)/120)
		db := trkFilterResponse(in.Filter, in.Cutoff, in.Resonance, f)
		db = math.Max(dbBot, math.Min(dbTop, db))
		pt := imgui.Vec2{X: p.X + float32(i)/120*width, Y: p.Y + float32((dbTop-db)/(dbTop-dbBot))*height}
		if i > 0 {
			dl.AddLineV(prev, pt, trkColFX, 2)
		}
		prev = pt
	}
}
