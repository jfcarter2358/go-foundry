package editor

import (
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/tracker"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// The instrument pane draws with its stacks even for each kind of slot: a
// plain embedded instrument, one carrying layers and articulations (the
// layers section's editors: layers, takes, an articulation's own zones),
// and one playing a library instrument (the read-only view).
func TestTrackerInstrumentPaneDrawsEveryKindOfSlot(t *testing.T) {
	dir := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = dir
	tracker.ReloadSongs()
	trkInstrNames = nil
	prevEd := trkEd
	t.Cleanup(func() { fileutil.ResourcesPath = prev; tracker.ReloadSongs(); trkInstrNames = nil; trkEd = prevEd })

	lib := tracker.NewInstrument("Lib", "keys/piano")
	lib.Zones = []tracker.Zone{{Sample: 0, Low: tracker.NoteMin, High: tracker.NoteMax, RoundRobin: []int{0},
		Layers: []tracker.Layer{{Sample: 0, VelLow: 0, VelHigh: 100}, {Sample: 0, VelLow: 80, VelHigh: 127}}}}
	lib.Articulations = []tracker.Articulation{{Name: "staccato", Triggers: []string{"stacc."}, VolEnv: tracker.EnvelopeOff()}}
	lib.Playback = tracker.Playback{Low: 40, High: 80, Clef: "treble", Transposition: -2}
	if err := tracker.SaveInstrument("keys/lib", lib); err != nil {
		t.Fatal(err)
	}
	e := trkTestState()
	rich := lib.Clone()
	// An articulation of its own samples, one zone split by velocity, so
	// the editor draws every part of the layers section.
	rich.Samples = append(rich.Samples, "keys/piano_soft")
	pizz := rich.AddArticulation("pizzicato")
	rich.SetOwnZones(pizz, true)
	rich.Articulations[pizz].Zones[0].Layers = nil
	rich.Articulations[pizz].Zones[0].SplitByVelocity()
	rich.Articulations[pizz].Zones[0].AddRoundRobin(1, 1)
	ref := tracker.NewInstrument("Ref", "")
	if err := ref.UseLibrary("keys/lib"); err != nil {
		t.Fatal(err)
	}
	e.Song.Instruments = append(e.Song.Instruments, rich, ref)
	trkEd = e
	withImgui(func() {
		// No imgui.ini written into the package directory.
		imgui.CurrentIO().SetIniFilename("")
		for n := 1; n <= len(e.Song.Instruments); n++ {
			e.Instr = n
			io := imgui.CurrentIO()
			io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 800})
			io.SetDeltaTime(1.0 / 60.0)
			imgui.NewFrame()
			imgui.SetNextWindowSize(imgui.Vec2{X: 360, Y: 800})
			imgui.Begin("instr")
			trkInstrumentList()
			trkInstrumentEditor(trkInstrument())
			trkInstrInfo(trkInstrument())
			imgui.End()
			imgui.Render()
		}
	})
}
