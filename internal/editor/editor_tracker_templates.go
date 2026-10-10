package editor

// editor_tracker_templates.go — the songs File > New starts from.
//
// A blank grid is the hardest place to start writing music, so New offers a
// handful of small, finished-sounding songs in common game shapes: a dance
// beat, a chiptune, an ambient bed, a combat loop with sections and an
// intensity param. Each is generated here rather than shipped as a file, so
// there is nothing to keep in step with the format.
//
// They name samples from the shared library by the names the library ships
// with ("drums/kick_808", "pads/pad_warm" …). A sample that is not there yet
// is a silent instrument, not an error — the tracker resolves samples lazily
// and a missing one leaves a quiet slot — so a template is always valid even
// in a project whose library is empty.

import (
	"foundry/internal/tracker"
)

// trkTemplate is one File > New choice.
type trkTemplate struct {
	Name  string
	Desc  string
	Build func() *tracker.Song
}

var trkTemplates = []trkTemplate{
	{"Empty", "Four empty channels and one instrument.", trkTemplateEmpty},
	{"4-on-the-floor", "Kick, clap, hats and a bass line at 124 BPM.", trkTemplateFloor},
	{"Chiptune", "Pulse lead with arpeggios, triangle bass and noise drums.", trkTemplateChip},
	{"Ambient", "Slow warm pads with long reverb tails.", trkTemplateAmbient},
	{"Combat loop", "An intro that hands over to a looping section, with an intensity param on the drums.", trkTemplateCombat},
}

// trkNote is a note by name, for writing templates readably. It panics on a
// typo, which a template test catches.
func trkNote(s string) uint8 {
	n, err := tracker.ParseNote(s)
	if err != nil {
		panic(err)
	}
	return n
}

// trkPut writes a note into a pattern cell.
func trkPut(p *tracker.Pattern, row, ch int, note string, instr int, vol int) {
	if row >= p.Rows() {
		return
	}
	c := &p.Cells[row][ch]
	c.Note = trkNote(note)
	c.Instr = uint8(instr)
	if vol >= 0 {
		c.Vol = uint8(vol)
	}
}

// trkNamedChannels renames the song's channels in order.
func trkNamedChannels(s *tracker.Song, names ...string) {
	s.SetChannelCount(len(names))
	for i, n := range names {
		s.Channels[i].Name = n
	}
}

func trkTemplateEmpty() *tracker.Song {
	s := tracker.NewSong("untitled")
	s.Instruments = []*tracker.Instrument{tracker.NewInstrument("Lead", "chip/chip_pulse_25")}
	return s
}

func trkTemplateFloor() *tracker.Song {
	s := tracker.NewSong("floor")
	s.BPM = 124
	trkNamedChannels(s, "Kick", "Clap", "Hats", "Bass")
	s.Instruments = []*tracker.Instrument{
		tracker.NewInstrument("Kick", "drums/kick_808"),
		tracker.NewInstrument("Clap", "drums/clap_808"),
		tracker.NewInstrument("Hat", "drums/hat_closed_808"),
		tracker.NewInstrument("Bass", "bass/bass_moog"),
	}
	s.Channels[1].SendA = 0.25
	s.Channels[2].Pan = 0.2
	p := s.Patterns[0]
	bass := []string{"C-2", "C-2", "D#2", "C-2", "G-1", "G-1", "A#1", "C-2"}
	for r := 0; r < p.Rows(); r += 4 {
		trkPut(p, r, 0, "C-4", 1, -1)
		trkPut(p, r+2, 2, "C-4", 3, 0x28)
		trkPut(p, r+2, 3, bass[(r/4)%len(bass)], 4, 0x30)
	}
	for r := 4; r < p.Rows(); r += 8 {
		trkPut(p, r, 1, "C-4", 2, -1)
	}
	return s
}

func trkTemplateChip() *tracker.Song {
	s := tracker.NewSong("chiptune")
	s.BPM = 150
	s.Interp = tracker.InterpNearest
	trkNamedChannels(s, "Lead", "Harmony", "Bass", "Noise")
	s.Instruments = []*tracker.Instrument{
		tracker.NewInstrument("Pulse 25", "chip/chip_pulse_25"),
		tracker.NewInstrument("Pulse 12", "chip/chip_pulse_12"),
		tracker.NewInstrument("Triangle", "chip/chip_triangle"),
		tracker.NewInstrument("Noise", "chip/chip_noise"),
	}
	p := s.Patterns[0]
	lead := []string{"C-5", "E-5", "G-5", "E-5", "A-4", "C-5", "E-5", "C-5", "F-4", "A-4", "C-5", "A-4", "G-4", "B-4", "D-5", "B-4"}
	for i, n := range lead {
		trkPut(p, i*4, 0, n, 1, 0x30)
	}
	// Major and minor arpeggios on the harmony channel, one chord a bar.
	chords := []struct {
		note string
		arp  uint8
	}{{"C-4", 0x47}, {"A-3", 0x37}, {"F-3", 0x47}, {"G-3", 0x47}}
	for i, c := range chords {
		trkPut(p, i*16, 1, c.note, 2, 0x20)
		p.Cells[i*16][1].FX[0] = tracker.Effect{Cmd: tracker.FxArpeggio, Param: c.arp}
	}
	bass := []string{"C-2", "A-1", "F-1", "G-1"}
	for i, n := range bass {
		for r := 0; r < 16; r += 4 {
			trkPut(p, i*16+r, 2, n, 3, -1)
		}
	}
	for r := 0; r < p.Rows(); r += 2 {
		vol := 0x18
		if r%8 == 4 {
			vol = 0x38
		}
		trkPut(p, r, 3, "C-5", 4, vol)
	}
	return s
}

func trkTemplateAmbient() *tracker.Song {
	s := tracker.NewSong("ambient")
	s.BPM = 70
	trkNamedChannels(s, "Pad A", "Pad B", "Pad C", "Air")
	pad := tracker.NewInstrument("Warm pad", "pads/pad_warm")
	pad.VolEnv = tracker.Envelope{
		Points:  []tracker.EnvPoint{{T: 0, V: 0}, {T: 1.5, V: 1}, {T: 4, V: 0.8}, {T: 7, V: 0}},
		Sustain: 2, LoopStart: -1, LoopEnd: -1,
	}
	pad.FadeOut = 2
	pad.Polyphony = 2
	s.Instruments = []*tracker.Instrument{pad}
	s.Reverb.Size, s.Reverb.Return = 0.9, 1
	for i := range s.Channels {
		s.Channels[i].SendA = 0.7
		s.Channels[i].Volume = 0.7
	}
	s.Channels[0].Pan, s.Channels[2].Pan = -0.4, 0.4
	p := s.Patterns[0]
	chords := [][3]string{{"C-4", "E-4", "G-4"}, {"A-3", "C-4", "E-4"}, {"F-3", "A-3", "C-4"}, {"G-3", "B-3", "D-4"}}
	for i, ch := range chords {
		for j, n := range ch {
			trkPut(p, i*16, j, n, 1, 0x28)
		}
		p.Cells[i*16+15][0].Note = tracker.NoteOff
	}
	trkPut(p, 8, 3, "G-5", 1, 0x10)
	trkPut(p, 40, 3, "E-5", 1, 0x10)
	return s
}

func trkTemplateCombat() *tracker.Song {
	s := tracker.NewSong("combat")
	s.BPM = 140
	trkNamedChannels(s, "Kick", "Snare", "Hats", "Bass", "Stab")
	s.Instruments = []*tracker.Instrument{
		tracker.NewInstrument("Kick", "drums/kick_808"),
		tracker.NewInstrument("Snare", "drums/snare_808"),
		tracker.NewInstrument("Hat", "drums/hat_closed_808"),
		tracker.NewInstrument("Bass", "bass/bass_moog"),
		tracker.NewInstrument("Stab", "winds/brass_section"),
	}
	intro := s.Patterns[0]
	intro.Name = "intro"
	for r := 0; r < intro.Rows(); r += 8 {
		trkPut(intro, r, 3, "D-2", 4, 0x30)
	}
	for r := 48; r < intro.Rows(); r += 2 {
		trkPut(intro, r, 1, "C-4", 2, (r-48)*2+8)
	}
	loop := tracker.NewPattern("loop", 64, len(s.Channels))
	bass := []string{"D-2", "D-2", "F-2", "D-2", "C-2", "C-2", "A#1", "C-2"}
	for r := 0; r < loop.Rows(); r += 2 {
		trkPut(loop, r, 2, "C-4", 3, 0x20)
		if r%4 == 0 {
			trkPut(loop, r, 3, bass[(r/4)%len(bass)], 4, 0x34)
		}
	}
	for r := 0; r < loop.Rows(); r += 4 {
		trkPut(loop, r, 0, "C-4", 1, -1)
	}
	for r := 4; r < loop.Rows(); r += 8 {
		trkPut(loop, r, 1, "C-4", 2, -1)
	}
	for _, r := range []int{0, 6, 32, 38} {
		trkPut(loop, r, 4, "D-4", 5, 0x30)
	}
	s.Patterns = append(s.Patterns, loop)
	s.Order = []int{0, 1, 1}
	s.Sections = []tracker.Section{
		{Name: "intro", Start: 0, End: 1, Next: "loop"},
		{Name: "loop", Start: 1, End: 3, Loop: true},
	}
	s.Params = []tracker.Param{{Name: "intensity", Default: 0.5}}
	s.Bindings = []tracker.Binding{
		{Param: "intensity", Channel: "Hats", Target: tracker.BindVolume, From: 0, To: 1},
		{Param: "intensity", Channel: "Stab", Target: tracker.BindCutoff, From: 800, To: 16000},
	}
	s.Markers = map[uint8]string{1: "drop"}
	loop.Cells[0][4].FX[0] = tracker.Effect{Cmd: tracker.FxMarker, Param: 1}
	return s
}
