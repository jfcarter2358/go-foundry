package editor

// editor_sound_panel.go — the Sounds section of the editor's side panel.
//
// Longer than the emitter's, and for a reason worth stating: an emitter's
// character lives in its .fpar and is edited elsewhere, so its panel is a
// transform and three knobs. A sound has no second editor — the clip is a
// recording, not a document — so everything about *how it is heard* is
// authored right here, and the attenuation group is the substance of it.
//
// The Audition button is what the Edit button is for an emitter: the one way
// to find out what a clip sounds like without walking to it.

import (
	"strconv"

	"foundry/internal/audio"
	"foundry/internal/imguibackend"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorSoundPropertiesSection draws the selected sound's fields.
func editorSoundPropertiesSection() {
	s := activeSound()
	if s == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusSound)
	if !imgui.CollapsingHeaderV("Sound: "+s.ID+"##sndhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusSound)
	editorSoundProperties(s)
}

// editorSoundAddMenu draws the sound half of the Explorer's Add menu.
func editorSoundAddMenu() {
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("ID##newsnd", &Editor.NewSoundID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave empty to name it \"sound\".\nMade unique on placement.")
	}
	imgui.SetNextItemWidth(160)
	soundFileCombo("Clip##newsndclip", &Editor.NewSoundClip)
	imgui.Separator()
	if imgui.MenuItem("Place By Clicking") {
		editorArmSoundPlacement()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A positional sound, placed where you click.\nIt gets quieter with distance and pans as you turn.")
	}
}

// editorSoundProperties draws the selected sound's editable state.
func editorSoundProperties(s *world.Sound) {
	editorPropGroupOpen("Identity##sndidgrp", func() { editorSoundIdentity(s) })
	editorPropGroupOpen("Clip##sndclipgrp", func() { editorSoundClip(s) })
	editorPropGroupOpen("Transform##sndtfgrp", func() { editorSoundTransform(s) })
	editorPropGroupOpen("Attenuation##sndattengrp", func() { editorSoundAttenuation(s) })
	editorPropGroup("Cone##sndconegrp", 0, func() { editorSoundCone(s) })
	editorPropGroup("Attach##sndattachgrp", 0, func() { editorSoundAttach(s) })
	editorObjectWaypointsUI(s)
	editorScriptDataUI(&s.ScriptData, "snd", strconv.Quote(s.ID))
	editorPropGroup("Actions##sndactions", 0, func() { editorSoundActions(s) })
}

// editorSoundIdentity draws the ID and the two switches.
//
// Enabled and Playing are separate for the reason an emitter's Enabled and
// Emitting are: they mean different things and an author needs both. Enabled
// off gives up the voice entirely; Playing off stops the sound while keeping
// the placement, which is what a script toggling a machine wants.
func editorSoundIdentity(s *world.Sound) {
	id := s.ID
	if imgui.InputTextV("ID##sndid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != s.ID {
		lvl := Editor.level
		if id == "" {
			editorSetStatus("A sound needs an ID for a script to address it")
		} else if world.LevelIDTaken(lvl, id) {
			editorSetStatus("%q is already used by something else in this level", id)
		} else {
			EditorPushUndo()
			s.ID = id
		}
	}

	if imgui.Checkbox("Enabled##sndenabled", &s.Enabled) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off gives up the voice entirely.")
	}
	if imgui.Checkbox("Playing##sndplaying", &s.Playing) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off stops the sound but keeps the placement.\nThis is what the stop verb does.")
	}
	if imgui.Checkbox("Loop##sndloop", &s.Loop) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("On for ambience. Off plays once and stops.")
	}
}

// editorSoundClip draws the asset picker, the level knobs, and what the clip
// resolved to.
func editorSoundClip(s *world.Sound) {
	if soundFileCombo("Clip##sndclip", &s.Clip) {
		EditorPushUndo()
	}

	// What the file actually is. A clip that will not load is the single most
	// common thing wrong with a placed sound, and saying so here is the
	// difference between a silent bug and an obvious one.
	if s.Clip == "" {
		editorTextMutedWrapped("No clip: this sound makes none.")
	} else if c, err := audio.ClipFor(s.Clip); err != nil {
		editorTextMutedWrapped("Cannot load: " + err.Error())
	} else {
		ch := "mono"
		if c.Channels == 2 {
			// Worth calling out: a stereo clip carries its own image, so it is
			// downmixed rather than panned. An author wondering why their
			// stereo ambience does not move with the camera needs this.
			ch = "stereo (downmixed when positional)"
		}
		editorTextMuted(formatClipInfo(c.Duration().Seconds(), c.Rate, ch))
	}

	if editorDragFloat("Gain##sndgain", &s.Gain, 0, 4, "%.2f") {
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Loudness multiplier. 1 is the clip as recorded.")
	}
	if editorDragFloat("Pitch##sndpitch", &s.Pitch, 0.5, 2, "%.2f") {
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Playback rate. Clamped to the range the mixer\ncan resample cleanly.")
	}

	editorSoundBusCombo(s)
}

// editorSoundBusCombo picks which slider the sound answers to.
func editorSoundBusCombo(s *world.Sound) {
	cur := string(s.AudioBus())
	if imgui.BeginCombo("Bus##sndbus", cur) {
		for _, b := range []string{string(audio.BusEffects), string(audio.BusMusic)} {
			if imgui.SelectableV(b, b == cur, 0, imgui.Vec2{}) && b != cur {
				EditorPushUndo()
				s.Bus = b
			}
		}
		imgui.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Which of the player's volume sliders this answers to.")
	}
}

func editorSoundTransform(s *world.Sound) {
	pos := [3]float32{s.Position.X(), s.Position.Y(), s.Position.Z()}
	if editorDragFloat3("Position##sndpos", &pos, 0, 0, "%.3f") {
		s.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	if !s.Attach.IsZero() && imgui.IsItemHovered() {
		imgui.SetTooltip("Local to " + s.Attach.Describe() + ", not the world.")
	}

	rot := vec3ToDegrees(s.Rotation)
	if editorDragFloat3Step("Rotation (deg)##sndrot", &rot, -360, 360, "%.1f", 1) {
		s.Rotation = vec3FromDegrees(rot)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Aims the cone. Meaningless without one.")
	}
}

// editorSoundAttenuation draws how far the sound carries.
//
// This is the substance of the panel. The two distances are the whole of how a
// positional sound is authored, and neither is visible in the viewport without
// the ranges toggle below — a torch audible across the level looks exactly
// like one audible across the room.
func editorSoundAttenuation(s *world.Sound) {
	if editorDragFloatStep("Ref distance##sndref", &s.RefDistance, 0.01, 10000, "%.2f", 0.25) {
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Full gain within this radius.")
	}
	if editorDragFloatStep("Max distance##sndmax", &s.MaxDistance, 0.01, 100000, "%.2f", 1) {
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Silent past this radius, and skipped by the mixer.")
	}
	// Max below Ref is a plateau with no falloff at all, which reads as "the
	// sound does not get quieter" and is almost never meant.
	if s.MaxDistance < s.RefDistance {
		editorTextMutedWrapped("Max is inside Ref: this sound has no falloff at all.")
	}

	if editorDragFloat("Rolloff##sndroll", &s.Rolloff, 0, 100, "%.2f") {
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How sharply it falls between the two distances.\n1 is the curve's own shape.")
	}

	editorSoundAttenCombo(s)

	if imgui.Checkbox("Show ranges##sndranges", &Editor.ShowSoundRanges) {
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Draw the two distances as rings.\nThe only way to place a sound by eye.")
	}
}

func editorSoundAttenCombo(s *world.Sound) {
	cur := s.Atten
	if cur == "" {
		cur = string(audio.AttenInverse)
	}
	if imgui.BeginCombo("Falloff##sndatten", cur) {
		for _, m := range []string{
			string(audio.AttenInverse), string(audio.AttenLinear), string(audio.AttenExponential),
		} {
			if imgui.SelectableV(m, m == cur, 0, imgui.Vec2{}) && m != cur {
				EditorPushUndo()
				s.Atten = m
			}
		}
		imgui.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Inverse is how sound behaves in the world.\nLinear and exponential are for effect.")
	}
}

// editorSoundCone draws the directional controls, which most sounds never use.
func editorSoundCone(s *world.Sound) {
	inner := s.ConeInner * 180 / 3.14159265
	outer := s.ConeOuter * 180 / 3.14159265
	if editorDragFloatStep("Inner (deg)##sndconein", &inner, 0, 360, "%.0f", 1) {
		s.ConeInner = inner * 3.14159265 / 180
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Full gain within this cone. Total width, not half.")
	}
	if editorDragFloatStep("Outer (deg)##sndconeout", &outer, 0, 360, "%.0f", 1) {
		s.ConeOuter = outer * 3.14159265 / 180
	}
	if editorDragFloat("Outside gain##sndconegain", &s.ConeOuterGain, 0, 1, "%.2f") {
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Gain outside the outer cone.")
	}
	if s.ConeOuter == 0 {
		editorTextMutedWrapped("Omnidirectional: heard equally in every direction.")
	}
}

func editorSoundAttach(s *world.Sound) {
	if editorAttachUI(Editor.level, &s.Attach, "snd") {
		EditorPushUndo()
	}
	if s.Attach.IsZero() {
		editorTextMutedWrapped("Unattached: Position is world-space.")
	} else {
		editorTextMutedWrapped("Rides " + s.Attach.Describe() + "; Position is local to it.")
	}
}

func editorSoundActions(s *world.Sound) {
	imguibackend.EditorPushDisabled(s.Clip == "" || !audio.Available())
	if imgui.Button("Audition##sndaudition") {
		auditionActiveSound()
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Play it once at the camera, ignoring distance.")
	}

	imgui.SameLine()
	if imgui.Button("Duplicate##snddup") {
		duplicateActiveSound()
	}
	imgui.SameLine()
	if imgui.Button("Delete##snddel") {
		deleteActiveSound()
	}

	if !audio.Available() {
		// A section with nothing to act on says so in one line rather than
		// being silently dead, which is the rule the rendering settings follow
		// for a window that was not granted multisampling.
		editorTextMutedWrapped("No audio device: nothing will be heard, but the placement still saves.")
	}
}

// ---------- the asset picker ----------

// soundFileCombo lists what is under sounds/.
//
// The rescan-on-close rule is particleFileCombo's: a clip dropped in while the
// editor is open has to appear without a restart, and rescanning every frame
// would be a directory read per frame.
func soundFileCombo(label string, clip *string) bool {
	preview := *clip
	if preview == "" {
		preview = "(none)"
	}
	changed := false
	if imgui.BeginCombo(label, preview) {
		if Editor.SoundFileNames == nil {
			Editor.SoundFileNames = audio.ScanSoundNames()
		}
		if imgui.SelectableV("(none)", *clip == "", 0, imgui.Vec2{}) && *clip != "" {
			*clip = ""
			changed = true
		}
		for _, n := range Editor.SoundFileNames {
			if imgui.SelectableV(n, n == *clip, 0, imgui.Vec2{}) && n != *clip {
				*clip = n
				changed = true
			}
		}
		imgui.EndCombo()
	} else {
		Editor.SoundFileNames = nil
	}
	return changed
}

// formatClipInfo renders the one line describing what a clip actually is.
func formatClipInfo(seconds float64, rate int, channels string) string {
	return strconv.FormatFloat(seconds, 'f', 2, 64) + "s, " +
		strconv.Itoa(rate) + " Hz, " + channels
}
