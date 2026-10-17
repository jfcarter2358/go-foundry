package editor

// editor_score_midi_view.go — the MIDI keyboard's window side: the device
// (opened from the settings when the window opens, chosen in Edit > MIDI
// Input), the messages drained once a frame into the model
// (editor_score_midi.go), MIDI thru played on the score player, a
// recording's ticks read off the player's clock, and the MIDI Input dialog.
//
// A recorded key's tick is worked out from the player's last published
// position: the score seconds it was at when its block was rendered, plus
// the time since then at the tempo slider's speed, less the latency the
// settings give. A key that arrives during the count-in's last beat is a
// player coming in early and goes on the downbeat; one before that is
// heard (thru) but not kept.

import (
	"fmt"
	"time"

	"foundry/internal/imguibackend"
	"foundry/internal/mididev"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

var (
	// scoreMIDIIn is the open input, nil for none.
	scoreMIDIIn *mididev.Input
	// scoreMIDIErr says why there is no input (a device that would not
	// open, one unplugged); "" when there is one or none was asked for.
	scoreMIDIErr string
	// scoreMIDITried is the settings' device tried this session.
	scoreMIDITried bool
	scoreMIDIBuf   []mididev.Message
	// scoreMIDIPorts are the ports the dialog lists (scoreMIDIPortsErr why
	// there are none), read when it opens and on Refresh.
	scoreMIDIPorts    []mididev.Port
	scoreMIDIPortsErr string
	scoreShowMIDI     bool
	// scoreThruOn are the keys thru is sounding, with their line, so a
	// device closing or a line going away releases them.
	scoreThruOn = map[int]int{}
	// scoreRecSeen is that the recording's transport has started (counting
	// in or playing), so a stopped player means the take is over.
	scoreRecSeen bool
)

// scoreColRec lights the Rec button while a recording runs.
var scoreColRec = imgui.Vec4{X: 0.69, Y: 0.19, Z: 0.19, W: 1}

// scoreThruKey is the player's preview key of a thru note (well clear of
// the auditions').
func scoreThruKey(key int) int { return 1000 + key }

// scoreMIDIConnect opens a port by name, closing the one open; "" closes.
func scoreMIDIConnect(name string) {
	scoreMIDIDisconnect()
	scoreMIDIErr = ""
	if name == "" {
		return
	}
	in, err := mididev.OpenByName(name)
	if err != nil {
		scoreMIDIErr = err.Error()
		return
	}
	scoreMIDIIn = in
}

// scoreMIDIDisconnect closes the input and silences what thru is playing.
func scoreMIDIDisconnect() {
	if scoreMIDIIn != nil {
		scoreMIDIIn.Close()
		scoreMIDIIn = nil
	}
	scoreThruRelease()
	if scEd != nil {
		scEd.MIDI.held, scEd.MIDI.chord = nil, false
	}
}

// scoreThruRelease releases every thru note.
func scoreThruRelease() {
	for key := range scoreThruOn {
		if scorePlayer != nil {
			scorePlayer.NoteOff(scoreThruKey(key))
		}
		delete(scoreThruOn, key)
	}
}

// scoreMIDIEnsure opens the settings' device the first time the window
// asks this session.
func scoreMIDIEnsure() {
	if scoreMIDITried {
		return
	}
	scoreMIDITried = true
	if name := scoreSettings().MIDI.Device; name != "" && scoreMIDIIn == nil {
		scoreMIDIConnect(name)
	}
}

// scoreMIDITick is the performance tick a key that arrived at a time falls
// on, when the transport is recording: ok false while it is not (or during
// the count-in before its last beat).
func scoreMIDITick(at time.Time) (int64, bool) {
	t := scEd.MIDI.Take
	if scorePlayer == nil || t == nil {
		return 0, false
	}
	pos := scorePlayer.Position()
	if pos.CountIn {
		// Early by less than a beat: the downbeat.
		return t.Tick, pos.CountBeat >= t.Count
	}
	if !pos.Playing {
		return 0, false
	}
	perf := scorePlayer.Performance()
	if perf == nil {
		return 0, false
	}
	lag := time.Duration(scoreSettings().MIDI.LatencyMS) * time.Millisecond
	sec := pos.Seconds + at.Add(-lag).Sub(pos.Published).Seconds()*scEd.Transport.Scale
	return int64(perf.Tempo.Tick(max(sec, 0)) + 0.5), true
}

// scorePollMIDI drains the keyboard into the model, sounds thru, and ends a
// recording whose transport has stopped. Once a frame.
func scorePollMIDI() {
	e := scEd
	cfg := scoreSettings().MIDI
	e.MIDI.Thru = !cfg.NoThru
	if in := scoreMIDIIn; in != nil {
		var err error
		scoreMIDIBuf, err = in.Poll(scoreMIDIBuf[:0])
		for _, m := range scoreMIDIBuf {
			if cfg.Channel > 0 && m.Channel() != cfg.Channel-1 {
				continue
			}
			tick, ok := scoreMIDITick(m.Time)
			e.midiIn(m, tick, ok)
		}
		if err != nil {
			scoreMIDIErr = fmt.Sprintf("%s: %v", in.Port.Name, err)
			scoreMIDIDisconnect()
			e.Status = "MIDI: " + scoreMIDIErr
		}
	}
	scorePlayThru()
	if e.MIDI.Take != nil && scorePlayer != nil {
		pos := scorePlayer.Position()
		switch {
		case pos.Playing || pos.CountIn:
			scoreRecSeen = true
		case scoreRecSeen:
			scoreRecSeen = false
			e.finishTake(pos.Tick, cfg)
		}
	}
}

// scorePlayThru starts and stops the keys the model asked to hear.
func scorePlayThru() {
	for _, t := range scEd.takeThru() {
		if scorePlayer == nil {
			continue
		}
		if t.On {
			scoreSyncPlayer(false)
			if _, on := scoreThruOn[t.Key]; on {
				scorePlayer.NoteOff(scoreThruKey(t.Key))
			}
			scorePlayer.Note(scoreThruKey(t.Key), t.Line, t.Key, t.Vel)
			scoreThruOn[t.Key] = t.Line
			continue
		}
		if _, on := scoreThruOn[t.Key]; on {
			scorePlayer.NoteOff(scoreThruKey(t.Key))
			delete(scoreThruOn, t.Key)
		}
	}
}

// scoreRecordToggle is the record command: start recording from a MIDI
// keyboard, or stop the one under way.
func scoreRecordToggle() {
	e := scEd
	cfg := scoreSettings().MIDI
	if e.MIDI.Take != nil {
		at := e.MIDI.Take.Tick
		if scorePlayer != nil {
			at = scorePlayer.Position().Tick
		}
		e.op(scorePlayerOp{Kind: scoreOpStop})
		scoreRecSeen = false
		e.finishTake(at, cfg)
		return
	}
	if scoreMIDIIn == nil {
		e.Status = "Recording needs a MIDI keyboard: choose one in Edit > MIDI Input"
		if scoreMIDIErr != "" {
			e.Status += " (" + scoreMIDIErr + ")"
		}
		scoreOpenMIDI()
		return
	}
	if scorePlaying() {
		e.togglePlay(true, scorePlayer.Position().Tick)
	}
	scoreRecSeen = false
	e.startTake(cfg)
}

// scoreRecording reports a recording under way.
func scoreRecording() bool { return scEd != nil && scEd.MIDI.Take != nil }

// scoreMIDIStatus is the status bar's word on the keyboard, "" with none.
func scoreMIDIStatus() string {
	switch {
	case scoreRecording():
		if scorePlayer != nil && scorePlayer.Position().CountIn {
			return fmt.Sprintf("Count-in %d", scorePlayer.Position().CountBeat)
		}
		return fmt.Sprintf("Recording: %d note(s)", scEd.MIDI.Take.Notes())
	case scoreMIDIIn != nil:
		return "MIDI: " + scoreMIDIIn.Port.Name
	case scoreMIDIErr != "":
		return "MIDI: not connected"
	}
	return ""
}

// ---------- the dialog ----------

// scoreOpenMIDI opens the MIDI Input dialog on a fresh port list.
func scoreOpenMIDI() {
	scoreMIDIRefresh()
	scoreShowMIDI = true
}

func scoreMIDIRefresh() {
	ports, err := mididev.List()
	scoreMIDIPorts, scoreMIDIPortsErr = ports, ""
	if err != nil {
		scoreMIDIPortsErr = err.Error()
	}
}

// scoreMIDIGrids are the quantise grids the dialog offers.
var scoreMIDIGrids = []struct{ Value, Label string }{
	{"8", "8ths"}, {"8,3", "8ths and triplets"}, {"16", "16ths"}, {"", "16ths and triplets"},
	{"16,3,5", "16ths, triplets and quintuplets"}, {"32", "32nds"}, {"32,3", "32nds and triplets"},
}

func scoreMIDIGridLabel(v string) string {
	for _, g := range scoreMIDIGrids {
		if g.Value == v {
			return g.Label
		}
	}
	return v
}

func scoreMIDIDialog() {
	imguibackend.WithModalPopup("MIDI Input##scmidi", &scoreShowMIDI, func() {
		cfg := scoreSettings()
		m := &cfg.MIDI
		changed := false
		imgui.Text("Keyboard")
		imgui.SetNextItemWidth(360)
		preview := m.Device
		if preview == "" {
			preview = "(none)"
		}
		if imguibackend.BeginCombo("Device##scmidiport", preview) {
			if imguibackend.SelectableV("(none)##scmidinone", m.Device == "", 0, imgui.Vec2{}) {
				m.Device, changed = "", true
				scoreMIDIConnect("")
			}
			for i, p := range scoreMIDIPorts {
				if imguibackend.SelectableV(fmt.Sprintf("%s##scmidip%d", p.Name, i), p.Name == m.Device, 0, imgui.Vec2{}) {
					m.Device, changed = p.Name, true
					scoreMIDIConnect(p.Name)
				}
			}
			imguibackend.EndCombo()
		}
		imgui.SameLine()
		if imgui.Button("Refresh##scmidirefresh") {
			scoreMIDIRefresh()
		}
		switch {
		case scoreMIDIIn != nil:
			editorTextMuted("Connected: " + scoreMIDIIn.Port.Name + " (" + scoreMIDIIn.Port.ID + ")")
		case scoreMIDIErr != "":
			imgui.PushStyleColor(imgui.StyleColorText, saveChipUnsaved)
			imgui.Text(scoreMIDIErr)
			imgui.PopStyleColor()
			if m.Device != "" && imgui.Button("Connect##scmidiretry") {
				scoreMIDIConnect(m.Device)
			}
		case scoreMIDIPortsErr != "":
			editorTextMuted(scoreMIDIPortsErr)
		case len(scoreMIDIPorts) == 0:
			editorTextMuted("No MIDI ports: plug a keyboard in and press Refresh.")
		}
		if last := scEd.MIDI.Last; last != "" {
			editorTextMuted("Last heard: " + last)
		}
		imgui.SetNextItemWidth(140)
		ch := "All channels"
		if m.Channel > 0 {
			ch = fmt.Sprintf("Channel %d", m.Channel)
		}
		if imguibackend.BeginCombo("Channel##scmidich", ch) {
			for c := 0; c <= 16; c++ {
				label := "All channels"
				if c > 0 {
					label = fmt.Sprintf("Channel %d", c)
				}
				if imguibackend.SelectableV(label+fmt.Sprintf("##scmidic%d", c), m.Channel == c, 0, imgui.Vec2{}) {
					m.Channel, changed = c, true
				}
			}
			imguibackend.EndCombo()
		}
		thru := !m.NoThru
		if imgui.Checkbox("MIDI thru##scmidithru", &thru) {
			m.NoThru, changed = !thru, true
			if !thru {
				scoreThruRelease()
			}
		}
		editorMenuTooltip("Play each key on the selected line's instrument as it is pressed.")
		imgui.Separator()
		imgui.Text("Step entry")
		editorTextMuted("In note input (N), a key writes a note of the chosen value after the\n" +
			"selection; keys played while it is held join it as a chord. Rests, dots\n" +
			"and ties stay on the computer keyboard.")
		imgui.Separator()
		imgui.Text("Recording" + scoreKeysSuffix(scRecord))
		editorTextMuted("Into the first armed line (Timeline's R), else the selected one, from\n" +
			"the selected bar. Space stops; the take replaces the bars it covered.")
		count := int32(m.countIn())
		imgui.SetNextItemWidth(160)
		if imgui.SliderIntV("Count-in bars##scmidicount", &count, 0, 4, "%d", 0) {
			c := int(count)
			m.CountIn, changed = &c, true
		}
		click := !m.NoClick
		if imgui.Checkbox("Click##scmidiclick", &click) {
			m.NoClick, changed = !click, true
		}
		editorMenuTooltip("A metronome on every beat while recording (the count-in clicks either way).")
		imgui.SameLine()
		lvl := m.clickLevel()
		if lvl == 0 {
			lvl = 0.6
		}
		imgui.SetNextItemWidth(140)
		if imgui.SliderFloatV("Level##scmidilevel", &lvl, 0.05, 1, "%.2f", 0) {
			m.ClickLevel, changed = lvl, true
		}
		imgui.SetNextItemWidth(260)
		if imguibackend.BeginCombo("Quantise to##scmidigrid", scoreMIDIGridLabel(m.Grid)) {
			for i, g := range scoreMIDIGrids {
				if imguibackend.SelectableV(fmt.Sprintf("%s##scmidig%d", g.Label, i), g.Value == m.Grid, 0, imgui.Vec2{}) {
					m.Grid, changed = g.Value, true
				}
			}
			imguibackend.EndCombo()
		}
		voices := int32(2)
		if m.Voices > 0 {
			voices = int32(m.Voices)
		}
		imgui.SetNextItemWidth(160)
		if imgui.SliderIntV("Voices##scmidivoices", &voices, 1, 4, "%d", 0) {
			m.Voices, changed = int(voices), true
		}
		editorMenuTooltip("The most voices a take is written in: 1 writes overlapping notes as chords.")
		legato, graces, dyn := !m.NoLegato, !m.NoGraces, !m.NoDynamics
		if imgui.Checkbox("Close small gaps##scmidileg", &legato) {
			m.NoLegato, changed = !legato, true
		}
		imgui.SameLine()
		if imgui.Checkbox("Grace notes##scmidigr", &graces) {
			m.NoGraces, changed = !graces, true
		}
		imgui.SameLine()
		if imgui.Checkbox("Dynamics from velocity##scmididyn", &dyn) {
			m.NoDynamics, changed = !dyn, true
		}
		lat := int32(m.LatencyMS)
		imgui.SetNextItemWidth(160)
		if imgui.SliderIntV("Latency (ms)##scmidilat", &lat, 0, 250, "%d", 0) {
			m.LatencyMS, changed = int(lat), true
		}
		editorMenuTooltip("How late your audio device plays: a take is moved this much earlier.")
		if changed {
			scoreSaveSettings()
		}
		if imgui.Button("Close##scmidiclose") {
			imgui.CloseCurrentPopup()
		}
	})
}
