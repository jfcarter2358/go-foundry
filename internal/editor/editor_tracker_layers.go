package editor

// editor_tracker_layers.go — the instrument editor's "Layers &
// articulations" section: velocity layers and round robin per zone, and the
// named articulations (their trigger words, gain, release and, when they
// play other samples, zones of their own), so a .finst's sampled-library
// half is authored here rather than in YAML. The rules live in
// internal/tracker/instrument_edit.go; this file draws them and records
// each change as one undo step.

import (
	"fmt"
	"strings"

	"foundry/internal/imguibackend"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// trkLayerZone is the zone whose layers and takes the section shows (per
// instrument editor, view state).
var trkLayerZone int

// trkTextBufs hold a text field's typing until it is committed (Return or
// clicking away), by field id; trkTextActive is the field being typed in.
var (
	trkTextBufs   = map[string]string{}
	trkTextActive string
)

// trkTextField is a text field that commits when it is left: the new text
// and true once, else the value as it is.
func trkTextField(id, value string, width float32) (string, bool) {
	buf := value
	if trkTextActive == id {
		buf = trkTextBufs[id]
	}
	imgui.SetNextItemWidth(width)
	imgui.InputTextV(id, &buf, 0, nil)
	if imgui.IsItemActive() {
		trkTextActive, trkTextBufs[id] = id, buf
	}
	if imgui.IsItemDeactivatedAfterEdit() {
		delete(trkTextBufs, id)
		trkTextActive = ""
		return buf, true
	}
	if trkTextActive == id && !imgui.IsItemActive() {
		delete(trkTextBufs, id)
		trkTextActive = ""
	}
	return value, false
}

// trkSampleCombo picks one of the instrument's sample slots; it reports a
// new pick.
func trkSampleCombo(id string, in *tracker.Instrument, cur *int, width float32) bool {
	imgui.SetNextItemWidth(width)
	preview := fmt.Sprintf("Sample %d", *cur+1)
	if *cur >= 0 && *cur < len(in.Samples) && in.Samples[*cur] != "" {
		preview = fmt.Sprintf("%d %s", *cur+1, trkShortSample(in.Samples[*cur]))
	}
	changed := false
	if imguibackend.BeginCombo(id, preview) {
		for si, name := range in.Samples {
			if imguibackend.SelectableV(fmt.Sprintf("%d  %s", si+1, name), si == *cur, 0, imgui.Vec2{}) && si != *cur {
				*cur, changed = si, true
			}
		}
		imguibackend.EndCombo()
	}
	return changed
}

// trkShortSample is a sample's name without its category.
func trkShortSample(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// trkZoneLabel names a zone in a picker: its range and what it plays.
func trkZoneLabel(zi int, z tracker.Zone) string {
	s := fmt.Sprintf("Zone %d: %s..%s", zi+1, tracker.NoteName(z.Low), tracker.NoteName(z.High))
	if len(z.Layers) > 0 {
		return s + fmt.Sprintf(", %d layers", len(z.Layers))
	}
	return s + fmt.Sprintf(", sample %d", z.Sample+1)
}

// trkLayersSection draws velocity layers and round robin for one zone, then
// the articulations.
func trkLayersSection(in *tracker.Instrument) {
	editorTextMuted(fmt.Sprintf("%d samples, %d zones", len(in.Samples), len(in.Zones)))
	imgui.Separator()
	imgui.Text("Velocity layers and round robin")
	if len(in.Zones) == 0 {
		editorTextMutedWrapped("Layers and takes belong to a zone of the keymap.")
		if len(in.Samples) > 0 && trkSmallButton("Make a zone over every key##trklayzone") {
			trkEd.pushUndo("")
			in.Zones = []tracker.Zone{{Sample: 0, Low: tracker.NoteMin, High: tracker.NoteMax}}
			trkLayerZone = 0
			trkEd.touch()
		}
	} else {
		trkLayerZone = max(0, min(trkLayerZone, len(in.Zones)-1))
		imgui.SetNextItemWidth(-1)
		if imguibackend.BeginCombo("##trklayzonepick", trkZoneLabel(trkLayerZone, in.Zones[trkLayerZone])) {
			for zi, z := range in.Zones {
				if imguibackend.SelectableV(trkZoneLabel(zi, z)+fmt.Sprintf("##trklz%d", zi), zi == trkLayerZone, 0, imgui.Vec2{}) {
					trkLayerZone = zi
				}
			}
			imguibackend.EndCombo()
		}
		trkZoneLayers(in, &in.Zones[trkLayerZone], "z")
	}
	imgui.Separator()
	trkArticulations(in)
}

// trkZoneLayers edits one zone's velocity layers (or, with none, its round
// robin); id keeps two zones' widgets apart.
func trkZoneLayers(in *tracker.Instrument, z *tracker.Zone, id string) {
	imgui.PushID("trklay" + id)
	defer imgui.PopID()
	if len(z.Layers) == 0 {
		trkTakes(in, z, -1)
		if trkSmallButton("Split by velocity##trksplitvel") {
			trkEd.pushUndo("")
			z.SplitByVelocity()
			trkEd.touch()
		}
		editorMenuTooltip("Two layers, quiet (0-63) and loud (64-127): point the loud one at its recording.\nOverlapping ranges crossfade.")
		return
	}
	for li := range z.Layers {
		l := &z.Layers[li]
		imgui.PushIDInt(li)
		s := l.Sample
		if trkSampleCombo("##trklaysamp", in, &s, 130) {
			trkEd.pushUndo("")
			l.Sample = s
			trkEd.touch()
		}
		imgui.SameLine()
		lo, hi := int32(l.VelLow), int32(l.VelHigh)
		imgui.SetNextItemWidth(70)
		if trkEdited(imgui.SliderIntV("##trklaylo", &lo, 0, 127, "from %d", 0)) {
			z.SetLayerVelocity(li, int(lo), int(hi))
		}
		editorMenuTooltip("The lowest MIDI velocity (Tracker volume x 2) this layer plays at")
		imgui.SameLine()
		imgui.SetNextItemWidth(70)
		if trkEdited(imgui.SliderIntV("##trklayhi", &hi, 0, 127, "to %d", 0)) {
			z.SetLayerVelocity(li, int(lo), int(hi))
		}
		imgui.SameLine()
		g := l.Gain
		imgui.SetNextItemWidth(70)
		if trkEdited(imgui.SliderFloatV("##trklaygain", &g, -24, 12, "%+.1f dB", 0)) {
			l.Gain = g
		}
		editorMenuTooltip("Trims the layer, to match its loudness to the next")
		imgui.SameLine()
		if trkSmallButton("x##trklaydel") {
			trkEd.pushUndo("")
			z.RemoveLayer(li)
			trkEd.touch()
			imgui.PopID()
			return
		}
		editorMenuTooltip("Remove the layer: its velocities go to the layer below")
		imgui.Indent()
		trkTakes(in, z, li)
		imgui.Unindent()
		imgui.PopID()
	}
	if trkSmallButton("Add layer##trkaddlayer") {
		trkEd.pushUndo("")
		z.SplitByVelocity()
		trkEd.touch()
	}
	editorMenuTooltip("Halves the widest layer")
}

// trkTakes edits a zone's (layer < 0) or a layer's round robin: the
// recordings played in turn, one note each.
func trkTakes(in *tracker.Instrument, z *tracker.Zone, layer int) {
	imgui.PushIDInt(9000 + layer)
	defer imgui.PopID()
	editorTextMuted(fmt.Sprintf("Takes: %d", z.Takes(layer)))
	rr := z.RoundRobin
	if layer >= 0 {
		rr = z.Layers[layer].RoundRobin
	}
	for i := range rr {
		imgui.SameLine()
		imgui.PushIDInt(i)
		s := rr[i]
		if trkSampleCombo("##trkrr", in, &s, 110) {
			trkEd.pushUndo("")
			z.SetRoundRobin(layer, i, s)
			trkEd.touch()
		}
		imgui.SameLine()
		if trkSmallButton("x##trkrrdel") {
			trkEd.pushUndo("")
			z.RemoveRoundRobin(layer, i)
			trkEd.touch()
			imgui.PopID()
			return
		}
		imgui.PopID()
	}
	imgui.SameLine()
	if trkSmallButton("+ take##trkrradd") {
		trkEd.pushUndo("")
		// The next slot after the sample, the usual order of a take set.
		base := z.Sample
		if layer >= 0 {
			base = z.Layers[layer].Sample
		}
		next := base + z.Takes(layer)
		if next >= len(in.Samples) {
			next = base
		}
		z.AddRoundRobin(layer, next)
		trkEd.touch()
	}
	editorMenuTooltip("Another recording of the same note, played in turn so a repeated note does not machine-gun")
}

// trkArticulations edits the named articulations: the @ command's numbers
// in the Tracker, chosen by marks and technique text in the Score.
func trkArticulations(in *tracker.Instrument) {
	imgui.Text("Articulations")
	editorMenuTooltip("Named ways of playing the instrument. The Tracker's @01, @02 ... pick them;\nin the Score a mark or technique text matching the name or a trigger word does.")
	for ai := range in.Articulations {
		a := &in.Articulations[ai]
		imgui.PushIDInt(20000 + ai)
		editorTextMuted(fmt.Sprintf("@%02X", ai+1))
		imgui.SameLine()
		if name, ok := trkTextField("##trkartname", a.Name, 120); ok {
			trkEd.pushUndo("")
			if err := in.RenameArticulation(ai, name); err != nil {
				trkEd.Status = "Articulation: " + err.Error()
			}
			trkEd.touch()
		}
		editorMenuTooltip("Its name (sustain, staccato, pizzicato ...): a Score mark or technique text of this name picks it")
		imgui.SameLine()
		if trig, ok := trkTextField("##trkarttrig", strings.Join(a.Triggers, ", "), -110); ok {
			trkEd.pushUndo("")
			in.SetTriggers(ai, trig)
			trkEd.touch()
		}
		editorMenuTooltip("More words that pick it, separated by commas: \"pizz., pizzicato\"")
		imgui.SameLine()
		if trkSmallButton("^##trkartup") {
			trkEd.pushUndo("")
			in.MoveArticulation(ai, -1)
			trkEd.touch()
		}
		imgui.SameLine()
		if trkSmallButton("v##trkartdn") {
			trkEd.pushUndo("")
			in.MoveArticulation(ai, 1)
			trkEd.touch()
		}
		imgui.SameLine()
		if trkSmallButton("x##trkartdel") {
			trkEd.pushUndo("")
			in.RemoveArticulation(ai)
			trkEd.touch()
			imgui.PopID()
			break
		}
		editorMenuTooltip("Remove it (the articulations after it move down an @ number)")
		imgui.Indent()
		g := a.Gain
		imgui.SetNextItemWidth(110)
		if trkEdited(imgui.SliderFloatV("Gain##trkartgain", &g, -24, 12, "%+.1f dB", 0)) {
			a.Gain = g
		}
		imgui.SameLine()
		fo := a.FadeOut
		imgui.SetNextItemWidth(110)
		if trkEdited(imgui.DragFloatV("Release##trkartfo", &fo, 0.01, 0, 20, "%.2f s", 0)) {
			a.FadeOut = fo
		}
		editorMenuTooltip("Its own release after the note ends (0 keeps the instrument's)")
		own := len(a.Zones) > 0
		if imgui.Checkbox("Its own samples##trkartown", &own) {
			trkEd.pushUndo("")
			in.SetOwnZones(ai, own)
			trkEd.touch()
		}
		editorMenuTooltip("Off, it plays the instrument's keymap (only its gain and release differ).\nOn, it starts from a copy of the keymap to point at its own recordings.")
		if len(a.Zones) > 0 {
			trkArticulationZones(in, a, ai)
		}
		imgui.Unindent()
		imgui.PopID()
	}
	imgui.SetNextItemWidth(160)
	if imguibackend.BeginCombo("##trkartadd", "Add articulation") {
		for _, name := range tracker.CommonArticulations {
			if imguibackend.SelectableV(name+"##trkartnew", false, 0, imgui.Vec2{}) {
				trkEd.pushUndo("")
				in.AddArticulation(name)
				trkEd.touch()
			}
		}
		imguibackend.EndCombo()
	}
}

// trkArticulationZones edits an articulation's own keymap: each zone's
// range and sample (and its layers and takes).
func trkArticulationZones(in *tracker.Instrument, a *tracker.Articulation, ai int) {
	for zi := range a.Zones {
		z := &a.Zones[zi]
		imgui.PushIDInt(30000 + zi)
		lo, hi := int32(z.Low), int32(z.High)
		imgui.SetNextItemWidth(80)
		if trkEdited(imgui.SliderIntV("##trkazlo", &lo, int32(tracker.NoteMin), int32(tracker.NoteMax), tracker.NoteName(uint8(lo)), 0)) {
			z.Low = uint8(min(lo, int32(z.High)))
		}
		imgui.SameLine()
		imgui.SetNextItemWidth(80)
		if trkEdited(imgui.SliderIntV("##trkazhi", &hi, int32(tracker.NoteMin), int32(tracker.NoteMax), tracker.NoteName(uint8(hi)), 0)) {
			z.High = uint8(max(hi, int32(z.Low)))
		}
		imgui.SameLine()
		if len(z.Layers) == 0 {
			s := z.Sample
			if trkSampleCombo("##trkazsamp", in, &s, 130) {
				trkEd.pushUndo("")
				z.Sample = s
				trkEd.touch()
			}
			imgui.SameLine()
		}
		if trkSmallButton("x##trkazdel") {
			trkEd.pushUndo("")
			a.Zones = append(a.Zones[:zi], a.Zones[zi+1:]...)
			if len(a.Zones) == 0 {
				a.Zones = nil
			}
			trkEd.touch()
			imgui.PopID()
			return
		}
		trkZoneLayers(in, z, fmt.Sprintf("a%d_%d", ai, zi))
		imgui.PopID()
	}
	if trkSmallButton("Add zone##trkazadd") {
		trkEd.pushUndo("")
		a.Zones = append(a.Zones, tracker.Zone{Sample: max(0, len(in.Samples)-1), Low: tracker.NoteC4, High: tracker.NoteC4 + 11})
		trkEd.touch()
	}
}
