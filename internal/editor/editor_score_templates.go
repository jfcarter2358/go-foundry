package editor

// editor_score_templates.go — the scores File > New starts from.
//
// Each is the shape of a piece rather than a piece: the lines a common
// ensemble has, on the library instruments the starter set ships
// (resources/sounds/instruments, `foundry tracker gen-instruments`), with
// their clefs and transpositions, eight empty bars of 4/4 in C at 100. The
// key, meter and tempo are a dialog away. Like the Tracker's templates they
// are generated here rather than shipped as files, so there is nothing to
// keep in step with the format; an instrument a project's library lacks is a
// silent line, which `foundry score inspect` reports, not an error.

import (
	"foundry/internal/score"
)

// scoreTemplate is one File > New choice.
type scoreTemplate struct {
	Name  string
	Desc  string
	Build func() *score.Score
}

var scoreTemplates = []scoreTemplate{
	{"Solo line", "One treble staff (flute): a melody, a cue, a sketch.", scoreTemplateSolo},
	{"Piano", "A grand staff: treble and bass, one piano.", scoreTemplatePiano},
	{"String quartet", "Two violins, viola (alto clef) and cello.", scoreTemplateQuartet},
	{"Chamber ensemble", "Flute, clarinet in Bb and horn in F (written transposed), violins and cello.", scoreTemplateChamber},
	{"Rhythm section", "Piano on a grand staff, electric bass, and a drum kit on a percussion staff.", scoreTemplateRhythm},
}

// scoreTemplateBase is the empty score every template starts from.
func scoreTemplateBase(title string) *score.Score {
	s := score.New("")
	s.Title = title
	s.Bars = 8
	s.Tempo = []score.TempoMark{{At: score.Pos{Bar: 1}, BPM: 100, Unit: score.Duration{Base: score.Quarter}}}
	return s
}

func scoreTemplateSolo() *score.Score {
	s := scoreTemplateBase("Untitled")
	s.Lines = []*score.Line{score.NewLine("Flute", "woodwinds/flute", "treble")}
	return s
}

func scoreTemplatePiano() *score.Score {
	s := scoreTemplateBase("Untitled")
	l := score.NewLine("Piano", "keys/grand_piano", "treble")
	l.Staves = append(l.Staves, &score.Staff{Clef: "bass", Voices: []*score.Voice{{}}})
	s.Lines = []*score.Line{l}
	return s
}

func scoreTemplateQuartet() *score.Score {
	s := scoreTemplateBase("String Quartet")
	s.Lines = []*score.Line{
		score.NewLine("Violin I", "strings/violin_section", "treble"),
		score.NewLine("Violin II", "strings/violin_section", "treble"),
		score.NewLine("Viola", "strings/viola_section", "alto"),
		score.NewLine("Cello", "strings/cello_section", "bass"),
	}
	s.Lines[0].Pan, s.Lines[1].Pan, s.Lines[2].Pan, s.Lines[3].Pan = -0.4, -0.15, 0.15, 0.4
	return s
}

func scoreTemplateChamber() *score.Score {
	s := scoreTemplateBase("Untitled")
	cl := score.NewLine("Clarinet in Bb", "woodwinds/clarinet", "treble")
	cl.Staves[0].Transpose = score.Interval{Diatonic: -1, Chromatic: -2} // sounds a major second lower
	hn := score.NewLine("Horn in F", "brass/horn", "treble")
	hn.Staves[0].Transpose = score.Interval{Diatonic: -4, Chromatic: -7} // a fifth lower
	s.Lines = []*score.Line{
		score.NewLine("Flute", "woodwinds/flute", "treble"),
		cl,
		hn,
		score.NewLine("Violins", "strings/violin_section", "treble"),
		score.NewLine("Cello", "strings/cello_section", "bass"),
	}
	return s
}

func scoreTemplateRhythm() *score.Score {
	s := scoreTemplateBase("Untitled")
	s.Lines = []*score.Line{scoreGrandStaffLine(), score.NewLine("Bass", "bass/bass_finger", "bass"), scorePercussionLine()}
	s.Lines[1].Pan = -0.1
	return s
}
