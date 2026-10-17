package savegame

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/savefile"
	"foundry/internal/score"
	"foundry/internal/score/play"
	"foundry/internal/testenv"
	"foundry/internal/tracker"
	"foundry/internal/world"
)

// A score playing in a level is saved where it was — its tick, its section,
// whether it was playing, and what scripts set — and a restore resumes it
// there (the next MusicUpdate starts it at ResumeTick), as a song resumes
// at its order and row.
func TestAScoreResumesWhereItWasSaved(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	prevPlayer := world.PlayerEntity
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{})
	t.Cleanup(func() { world.PlayerEntity = prevPlayer })
	prevLoad := play.LoadInstrument
	play.LoadInstrument = func(name string) (*tracker.Instrument, error) {
		d := make([]float32, 4800)
		for i := range d {
			d[i] = 0.1
		}
		in := tracker.NewInstrument(name, name)
		in.SetLoaded(0, &tracker.Sample{Name: name, Data: d, Channels: 1, Rate: 48000, BaseNote: tracker.NoteC4, Volume: 1,
			Loop: tracker.LoopForward, LoopStart: 0, LoopEnd: len(d)})
		return in, nil
	}
	t.Cleanup(func() { play.LoadInstrument = prevLoad })

	s, err := score.Parse([]byte(`bars: 3
sections: [{name: a, bar: 1}, {name: b, bar: 2, loop: true}]
params: [{name: intensity, default: 0}]
lines:
  - name: Horn
    instrument: test/horn
    staves: [{clef: treble, voices: ["| wC4 | wD4 | wE4 |"]}]
`))
	if err != nil {
		t.Fatal(err)
	}
	p, errs := play.New(s)
	if p == nil {
		t.Fatal(errs)
	}
	p.Start("a")
	buf := make([]float32, 480*2)
	for i := 0; i < 250; i++ { // 2.5 s at 120: bar 2, beat 2
		p.Render(buf, 480, 48000)
	}

	lvl := authored(t)
	lvl.Music = &world.Music{Track: "theme", Gain: 0.8, Loop: true, Fade: 2, Score: p, Voice: 1,
		Section: "a", Params: map[string]float32{"intensity": 0.6}, Mutes: map[string]bool{"Horn": true}}
	snap := Capture(lvl, savefile.KindManual, "")
	data, err := savefile.Encode(snap)
	if err != nil {
		t.Fatal(err)
	}
	back, err := savefile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	ms := back.World.Music
	if ms == nil || ms.Tick != 4800 || !ms.Playing || ms.Section != "b" {
		t.Fatalf("saved music %+v, want tick 4800, playing, in section b", ms)
	}

	fresh := load(t)
	if err := Apply(fresh, back); err != nil {
		t.Fatal(err)
	}
	m := fresh.Music
	if m == nil || m.Track != "theme" || !m.Resume || m.ResumeTick != 4800 || m.Section != "b" ||
		m.Params["intensity"] != 0.6 || !m.Mutes["Horn"] || m.Gain != 0.8 || m.Fade != 2 {
		t.Fatalf("restored music %+v", m)
	}

	// Started where the save says — what startScore hands play.Play.
	r, errs := play.New(s)
	if r == nil {
		t.Fatal(errs)
	}
	r.StartAt(m.ResumeTick)
	if pos := r.Position(); pos.Bar != 2 || pos.Beat != 2 || !pos.Playing {
		t.Errorf("resumed at %+v, want bar 2, beat 2", pos)
	}
}
