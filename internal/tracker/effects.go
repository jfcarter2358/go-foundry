package tracker

// effects.go — the effect column's vocabulary.
//
// Stored as a letter and a byte, the way trackers always have, because a
// pattern is read at a glance and three characters per command is what makes
// eight channels fit on a screen. The letters are chosen to be guessable
// (V vibrato, S slide, P pan) rather than to match ProTracker's digits, and
// every one carries a name and a sentence so the editor never has to show a
// bare code without a way to find out what it means.

import "fmt"

// Effect letters.
const (
	FxArpeggio    byte = 'A' // xy: cycle note, +x, +y semitones each tick
	FxPitchUp     byte = 'U' // xx: slide pitch up xx/16 semitone per tick
	FxPitchDown   byte = 'D' // xx: slide pitch down xx/16 semitone per tick
	FxGlide       byte = 'G' // xx: glide to the row's note at xx/16 semitone per tick
	FxVibrato     byte = 'V' // xy: speed x, depth y/8 semitone
	FxTremolo     byte = 'T' // xy: speed x, depth y/15
	FxVolSlide    byte = 'S' // xy: volume up x or down y per tick
	FxPan         byte = 'P' // xx: 00 left, 80 centre, FF right
	FxPanSlide    byte = 'Y' // xy: pan right x or left y per tick
	FxRetrigger   byte = 'Q' // xx: restart the note every xx ticks
	FxOffset      byte = 'O' // xx: start xx/256 of the way into the sample
	FxNoteCut     byte = 'C' // xx: silence the note after xx ticks
	FxNoteDelay   byte = 'N' // xx: hold the row's note back xx ticks
	FxCutoff      byte = 'F' // xx: channel low-pass cutoff, 00 dark .. FF open
	FxResonance   byte = 'R' // xx: channel filter resonance
	FxCutoffSlide byte = 'W' // xy: open x or close y per tick
	FxFineTune    byte = 'J' // xx: detune, 80 = none, ±1 cent per step
	FxSendA       byte = 'X' // xx: reverb send level
	FxSendB       byte = 'Z' // xx: delay send level
	FxJump        byte = 'B' // xx: jump to order position xx after this row
	FxBreak       byte = 'K' // xx: next pattern, starting at row xx
	FxLoop        byte = 'L' // 00 marks the loop start; 0x repeats back x times
	FxTempo       byte = 'M' // xx: set BPM to xx (20..FF)
	FxParam       byte = 'H' // xy: set song param x to y/15
	FxMarker      byte = 'E' // xx: fire marker event xx
	FxReverse     byte = 'I' // 01 plays the sample backwards, 00 forwards
)

// EffectInfo describes one command for the editor.
type EffectInfo struct {
	Cmd   byte
	Name  string
	Short string // at most 8 characters, for the readable grid
	Desc  string
	// Split says the byte is two nibbles (x, y) rather than one value.
	Split bool
	Group string
}

// Effects is every command, in the order the editor's picker lists them.
var Effects = []EffectInfo{
	{FxPitchUp, "Pitch slide up", "Pitch↑", "Slide the pitch up by xx/16 of a semitone every tick.", false, "Pitch"},
	{FxPitchDown, "Pitch slide down", "Pitch↓", "Slide the pitch down by xx/16 of a semitone every tick.", false, "Pitch"},
	{FxGlide, "Glide", "Glide", "Glide from the playing note to this row's note at xx/16 semitone per tick, instead of restarting.", false, "Pitch"},
	{FxVibrato, "Vibrato", "Vibrato", "Wobble the pitch: x is the speed, y the depth in eighths of a semitone.", true, "Pitch"},
	{FxArpeggio, "Arpeggio", "Arp", "Cycle between the note, +x and +y semitones, one step per tick.", true, "Pitch"},
	{FxFineTune, "Fine tune", "Tune", "Detune the note: 80 is none, each step is one cent.", false, "Pitch"},
	{FxVolSlide, "Volume slide", "VolSlide", "Raise the volume by x or lower it by y every tick.", true, "Volume"},
	{FxTremolo, "Tremolo", "Tremolo", "Wobble the volume: x is the speed, y the depth.", true, "Volume"},
	{FxNoteCut, "Note cut", "Cut", "Silence the note after xx ticks.", false, "Volume"},
	{FxNoteDelay, "Note delay", "Delay", "Start this row's note xx ticks late.", false, "Volume"},
	{FxRetrigger, "Retrigger", "Retrig", "Restart the note every xx ticks.", false, "Volume"},
	{FxPan, "Set pan", "Pan", "Place the channel: 00 is hard left, 80 centre, FF hard right.", false, "Space"},
	{FxPanSlide, "Pan slide", "PanSlide", "Move the pan right by x or left by y every tick.", true, "Space"},
	{FxSendA, "Reverb send", "Reverb", "Set how much of the channel goes to the reverb, 00..FF.", false, "Space"},
	{FxSendB, "Delay send", "Echo", "Set how much of the channel goes to the delay, 00..FF.", false, "Space"},
	{FxOffset, "Sample offset", "Offset", "Start the sample xx/256 of the way through.", false, "Sample"},
	{FxReverse, "Reverse", "Reverse", "01 plays the sample backwards, 00 forwards again.", false, "Sample"},
	{FxCutoff, "Filter cutoff", "Cutoff", "Set the channel's low-pass: 00 is dark, FF fully open.", false, "Filter"},
	{FxResonance, "Filter resonance", "Reso", "Set the filter's resonance, 00..FF.", false, "Filter"},
	{FxCutoffSlide, "Cutoff slide", "CutSlide", "Open the filter by x or close it by y every tick.", true, "Filter"},
	{FxJump, "Jump to order", "Jump", "After this row, continue from order position xx.", false, "Flow"},
	{FxBreak, "Pattern break", "Break", "After this row, go to the next pattern, starting at row xx.", false, "Flow"},
	{FxLoop, "Loop rows", "Loop", "00 marks where a loop starts; 0x goes back there x times.", false, "Flow"},
	{FxTempo, "Set tempo", "BPM", "Set the tempo to xx beats per minute (20..FF).", false, "Flow"},
	{FxParam, "Set param", "Param", "Set song param x (in list order) to y/15.", true, "Flow"},
	{FxMarker, "Marker", "Marker", "Fire marker xx to scripts as :song_marker:.", false, "Flow"},
}

var effectByCmd = func() map[byte]*EffectInfo {
	m := map[byte]*EffectInfo{}
	for i := range Effects {
		m[Effects[i].Cmd] = &Effects[i]
	}
	return m
}()

// EffectFor returns the description of a command letter, or nil.
func EffectFor(cmd byte) *EffectInfo { return effectByCmd[cmd] }

// Describe renders a command as readable words: "Vibrato 4/8".
func (e Effect) Describe() string {
	info := EffectFor(e.Cmd)
	if info == nil {
		if e.Cmd == 0 {
			return ""
		}
		return fmt.Sprintf("?%c%02X", e.Cmd, e.Param)
	}
	if info.Split {
		return fmt.Sprintf("%s %X/%X", info.Short, e.Param>>4, e.Param&15)
	}
	return fmt.Sprintf("%s %02X", info.Short, e.Param)
}
