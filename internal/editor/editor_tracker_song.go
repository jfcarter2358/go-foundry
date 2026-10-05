package editor

// editor_tracker_song.go — the bottom pane: the Sample tab
// (editor_tracker_sample.go), the song's settings, and its params, bindings
// and markers.
//
// The params tab has live sliders as well as the defaults: dragging one calls
// Player.SetParam on the editor's player, so a binding (intensity → hat
// volume) is heard while the song plays, the way a script will drive it. The
// live values are view state, not the song — only Default is saved.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

func trkBottomPane() {
	w := trackerEditorWindow
	y := float32(w.H) - trkBottomH
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: y})
	imgui.SetNextWindowSize(imgui.Vec2{X: float32(w.W) - trkRightW, Y: trkBottomH})
	if imgui.BeginV("##trkbottom", nil, trkPaneFlags) {
		trkPaneClickTakesFocus()
		pos, size := imgui.WindowPos(), imgui.WindowSize()
		if imgui.BeginTabBar("##trkbottomtabs") {
			if imgui.BeginTabItem("Sample##trktabsample") {
				trkSampleTab()
				imgui.EndTabItem()
			}
			if imgui.BeginTabItem("Song##trktabsong") {
				trkSongTab()
				imgui.EndTabItem()
			}
			if imgui.BeginTabItem("Params & markers##trktabparams") {
				trkParamsTab()
				imgui.EndTabItem()
			}
			imgui.EndTabBar()
		}
		trkSplitter("##trksplitbottom", imgui.Vec2{X: pos.X, Y: pos.Y}, imgui.Vec2{X: pos.X + size.X, Y: pos.Y + 4}, false, &trkBottomH, -1)
	}
	imgui.End()
}

// ---------- song ----------

func trkSongTab() {
	s := trkEd.Song
	imgui.ColumnsV(3, "##trksongcols", false)
	imgui.PushItemWidth(-110)

	imgui.Text("Timing")
	bpm := s.BPM
	if trkEdited(imgui.DragFloatV("BPM##trksbpm", &bpm, 0.5, 16, 999, "%.1f", 0)) {
		s.BPM = bpm
	}
	lpb := int32(s.LPB)
	if trkEdited(imgui.SliderIntV("Rows per beat##trkslpb", &lpb, 1, 32, "%d", 0)) {
		s.LPB = int(lpb)
	}
	tpr := int32(s.TicksPerRow)
	if trkEdited(imgui.SliderIntV("Ticks per row##trkstpr", &tpr, 1, 32, "%d", 0)) {
		s.TicksPerRow = int(tpr)
	}
	editorMenuTooltip("Effect resolution: slides and vibrato update once a tick")
	bpb := int32(s.BeatsPerBar)
	if trkEdited(imgui.SliderIntV("Beats per bar##trksbpb", &bpb, 1, 16, "%d", 0)) {
		s.BeatsPerBar = int(bpb)
	}
	loop := s.Loop
	if trkEdited(imgui.Checkbox("Loop at the end##trksloop", &loop)) {
		s.Loop = loop
	}
	be := s.BeatEvents
	if trkEdited(imgui.Checkbox("Beat events##trksbe", &be)) {
		s.BeatEvents = be
	}
	editorMenuTooltip("Post :song_beat: and :song_bar: to scripts")

	imgui.NextColumn()
	imgui.Text("Layout & mix")
	fx := int32(s.FXColumns)
	if trkEdited(imgui.SliderIntV("Effect columns##trksfx", &fx, 1, tracker.MaxFXColumns, "%d", 0)) {
		s.FXColumns = int(fx)
		trkEd.clampCursor()
	}
	chans := int32(len(s.Channels))
	if imgui.InputIntV("Channels##trkschans", &chans, 1, 4, imgui.InputTextFlagsEnterReturnsTrue) {
		n := clampI(int(chans), 1, tracker.MaxChannels)
		if n != len(s.Channels) {
			trkEd.pushUndo("")
			s.SetChannelCount(n)
			trkEd.touch()
			trkAfterStructural()
		}
	}
	g := s.Gain
	if trkEdited(imgui.SliderFloatV("Gain##trksgain", &g, 0, 2, "%.2f", 0)) {
		s.Gain = g
	}
	interp := []string{"Nearest", "Linear", "Cubic"}
	cur := int32(s.Interp) - 1
	if imguibackend.Combo("Interpolation##trksinterp", &cur, interp) {
		trkEd.pushUndo("")
		s.Interp = tracker.Interp(cur + 1)
		trkEd.touch()
	}
	editorMenuTooltip("How instruments that leave theirs unset read between sample frames")
	name := s.Name
	if trkEdited(imgui.InputTextV("Title##trksname", &name, 0, nil)) {
		s.Name = name
	}

	imgui.NextColumn()
	imgui.Text("Reverb (send A)")
	r := s.Reverb
	changed := imgui.SliderFloatV("Size##trksrsize", &r.Size, 0, 1, "%.2f", 0)
	changed = trkEdited(changed) || changed
	c2 := imgui.SliderFloatV("Damping##trksrdamp", &r.Damping, 0, 1, "%.2f", 0)
	c2 = trkEdited(c2) || c2
	c3 := imgui.SliderFloatV("Width##trksrwidth", &r.Width, 0, 1, "%.2f", 0)
	c3 = trkEdited(c3) || c3
	c4 := imgui.SliderFloatV("Return##trksrret", &r.Return, 0, 2, "%.2f", 0)
	c4 = trkEdited(c4) || c4
	if changed || c2 || c3 || c4 {
		s.Reverb = r
	}
	imgui.Text("Delay (send B)")
	d := s.Delay
	dc := false
	for _, ch := range []bool{
		imgui.SliderFloatV("Beats##trksdbeats", &d.Beats, 0.125, 4, "%.3f", 0),
		imgui.SliderFloatV("Feedback##trksdfb", &d.Feedback, 0, 0.95, "%.2f", 0),
		imgui.SliderFloatV("Tone##trksdtone", &d.Tone, 0, 1, "%.2f", 0),
		imgui.Checkbox("Ping-pong##trksdpp", &d.PingPong),
		imgui.SliderFloatV("Return##trksdret", &d.Return, 0, 2, "%.2f", 0),
	} {
		dc = dc || ch
	}
	if trkEdited(dc) {
		s.Delay = d
	}
	imgui.Text("Compressor (master)")
	cp := s.Compressor
	cc := false
	for _, ch := range []bool{
		imgui.Checkbox("On##trkscon", &cp.Enabled),
		imgui.SliderFloatV("Threshold##trkscthr", &cp.Threshold, -48, 0, "%.1f dB", 0),
		imgui.SliderFloatV("Ratio##trkscratio", &cp.Ratio, 1, 20, "%.1f:1", 0),
		imgui.SliderFloatV("Attack##trkscatt", &cp.Attack, 0.0005, 0.2, "%.4f s", 0),
		imgui.SliderFloatV("Release##trkscrel", &cp.Release, 0.01, 2, "%.2f s", 0),
		imgui.SliderFloatV("Makeup##trkscmk", &cp.Makeup, 0, 24, "%.1f dB", 0),
	} {
		cc = cc || ch
	}
	if trkEdited(cc) {
		s.Compressor = cp
	}
	imgui.PopItemWidth()
	imgui.Columns()
}

// ---------- params, bindings, markers ----------

// trkParamLive is each param's live slider value, by name.
var trkParamLive = map[string]float32{}

var trkBindTargets = []tracker.BindTarget{tracker.BindVolume, tracker.BindPan, tracker.BindSendA, tracker.BindSendB, tracker.BindCutoff}

func trkParamsTab() {
	s := trkEd.Song
	imgui.ColumnsV(3, "##trkparamcols", true)

	imgui.Text("Params")
	imgui.SameLine()
	if trkSmallButton("+##trkparamadd") {
		trkEd.pushUndo("")
		name := "param"
		for n := 2; s.ParamIndex(name) >= 0; n++ {
			name = fmt.Sprintf("param%d", n)
		}
		s.Params = append(s.Params, tracker.Param{Name: name, Default: 0})
		trkEd.touch()
	}
	editorMenuTooltip("A 0..1 value a script ramps with set_music_param")
	for i := range s.Params {
		p := &s.Params[i]
		imgui.PushIDInt(i)
		imgui.SetNextItemWidth(90)
		name := p.Name
		if imgui.InputTextV("##trkpname", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) && strings.TrimSpace(name) != "" && s.ParamIndex(strings.TrimSpace(name)) < 0 {
			trkEd.pushUndo("")
			old := p.Name
			p.Name = strings.TrimSpace(name)
			for b := range s.Bindings {
				if s.Bindings[b].Param == old {
					s.Bindings[b].Param = p.Name
				}
			}
			trkEd.touch()
		}
		imgui.SameLine()
		imgui.SetNextItemWidth(70)
		def := p.Default
		if trkEdited(imgui.SliderFloatV("##trkpdef", &def, 0, 1, "def %.2f", 0)) {
			p.Default = def
		}
		imgui.SameLine()
		imgui.SetNextItemWidth(80)
		live, ok := trkParamLive[p.Name]
		if !ok {
			live = p.Default
		}
		if imgui.SliderFloatV("##trkplive", &live, 0, 1, "live %.2f", 0) {
			trkParamLive[p.Name] = live
			if trkPlayer != nil {
				trkPlayer.SetParam(p.Name, live, 0.05)
			}
		}
		editorMenuTooltip("Hear the param while the song plays (not saved)")
		imgui.SameLine()
		if trkSmallButton("x") {
			trkEd.pushUndo("")
			s.Params = append(s.Params[:i], s.Params[i+1:]...)
			trkEd.touch()
			imgui.PopID()
			break
		}
		imgui.PopID()
	}

	imgui.NextColumn()
	imgui.Text("Bindings")
	imgui.SameLine()
	imguibackend.EditorPushDisabled(len(s.Params) == 0)
	if trkSmallButton("+##trkbindadd") {
		trkEd.pushUndo("")
		s.Bindings = append(s.Bindings, tracker.Binding{Param: s.Params[0].Name, Channel: s.Channels[0].Name, Target: tracker.BindVolume, From: 0, To: 1})
		trkEd.touch()
	}
	imguibackend.EditorPopDisabled()
	editorMenuTooltip("Map a param onto a channel's volume, pan, sends or cutoff")
	for i := range s.Bindings {
		b := &s.Bindings[i]
		imgui.PushIDInt(i)
		imgui.SetNextItemWidth(80)
		if imguibackend.BeginCombo("##trkbparam", b.Param) {
			for _, p := range s.Params {
				if imguibackend.SelectableV(p.Name, p.Name == b.Param, 0, imgui.Vec2{}) {
					trkEd.pushUndo("")
					b.Param = p.Name
					trkEd.touch()
				}
			}
			imguibackend.EndCombo()
		}
		imgui.SameLine()
		imgui.SetNextItemWidth(80)
		if imguibackend.BeginCombo("##trkbch", b.Channel) {
			for _, c := range s.Channels {
				if imguibackend.SelectableV(c.Name, c.Name == b.Channel, 0, imgui.Vec2{}) {
					trkEd.pushUndo("")
					b.Channel = c.Name
					trkEd.touch()
				}
			}
			imguibackend.EndCombo()
		}
		imgui.SameLine()
		imgui.SetNextItemWidth(75)
		if imguibackend.BeginCombo("##trkbtgt", string(b.Target)) {
			for _, t := range trkBindTargets {
				if imguibackend.SelectableV(string(t), t == b.Target, 0, imgui.Vec2{}) {
					trkEd.pushUndo("")
					b.Target = t
					if t == tracker.BindCutoff {
						b.From, b.To = 500, 16000
					} else if t == tracker.BindPan {
						b.From, b.To = -1, 1
					} else {
						b.From, b.To = 0, 1
					}
					trkEd.touch()
				}
			}
			imguibackend.EndCombo()
		}
		imgui.SameLine()
		lo, hi := float32(0), float32(1)
		switch b.Target {
		case tracker.BindCutoff:
			lo, hi = 20, 20000
		case tracker.BindPan:
			lo = -1
		}
		ft := [2]float32{b.From, b.To}
		imgui.SetNextItemWidth(110)
		if trkEdited(imgui.DragFloat2V("##trkbft", &ft, (hi-lo)/200, lo, hi, "%.2f", 0)) {
			b.From, b.To = ft[0], ft[1]
		}
		editorMenuTooltip("Value at param 0, value at param 1")
		imgui.SameLine()
		if trkSmallButton("x") {
			trkEd.pushUndo("")
			s.Bindings = append(s.Bindings[:i], s.Bindings[i+1:]...)
			trkEd.touch()
			imgui.PopID()
			break
		}
		imgui.PopID()
	}

	imgui.NextColumn()
	imgui.Text("Markers")
	imgui.SameLine()
	if trkSmallButton("+##trkmarkadd") {
		trkEd.pushUndo("")
		if s.Markers == nil {
			s.Markers = map[uint8]string{}
		}
		k := uint8(1)
		for ; k < 255; k++ {
			if _, used := s.Markers[k]; !used {
				break
			}
		}
		s.Markers[k] = fmt.Sprintf("marker%d", k)
		trkEd.touch()
	}
	editorMenuTooltip("Names for the E (marker) effect's values, sent to scripts as :song_marker:")
	keys := make([]int, 0, len(s.Markers))
	for k := range s.Markers {
		keys = append(keys, int(k))
	}
	sort.Ints(keys)
	for _, k := range keys {
		imgui.PushIDInt(k)
		imgui.Text(fmt.Sprintf("E%02X", k))
		imgui.SameLine()
		imgui.SetNextItemWidth(120)
		name := s.Markers[uint8(k)]
		if imgui.InputTextV("##trkmname", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) {
			trkEd.pushUndo("")
			s.Markers[uint8(k)] = name
			trkEd.touch()
		}
		imgui.SameLine()
		if trkSmallButton("x") {
			trkEd.pushUndo("")
			delete(s.Markers, uint8(k))
			trkEd.touch()
		}
		imgui.PopID()
	}
	imgui.Columns()
}
