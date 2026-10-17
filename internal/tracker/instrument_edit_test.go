package tracker

import (
	"reflect"
	"testing"
)

// Velocity layers split, resize, remove and fold back the way an editor
// needs: every velocity still has a layer.
func TestZoneVelocityLayers(t *testing.T) {
	z := Zone{Sample: 1, Low: NoteMin, High: NoteMax, RoundRobin: []int{2}}
	if i := z.SplitByVelocity(); i != 1 {
		t.Errorf("first split returned %d", i)
	}
	want := []Layer{{Sample: 1, RoundRobin: []int{2}, VelLow: 0, VelHigh: 63}, {Sample: 1, VelLow: 64, VelHigh: 127}}
	if !reflect.DeepEqual(z.Layers, want) || z.RoundRobin != nil {
		t.Fatalf("split: %+v rr %v", z.Layers, z.RoundRobin)
	}
	// The widest is halved: 0..63 and 64..127 tie, the first goes.
	z.Layers[1].Sample = 3
	if i := z.SplitByVelocity(); i != 1 || z.Layers[0].VelHigh != 31 || z.Layers[1].VelLow != 32 || z.Layers[1].VelHigh != 63 || z.Layers[2].Sample != 3 {
		t.Fatalf("second split at %d: %+v", i, z.Layers)
	}
	z.SetLayerVelocity(1, 70, 20)
	if z.Layers[1].VelLow != 20 || z.Layers[1].VelHigh != 70 {
		t.Errorf("velocity: %+v", z.Layers[1])
	}
	z.SetLayerVelocity(1, -5, 300)
	if z.Layers[1].VelLow != 0 || z.Layers[1].VelHigh != 127 {
		t.Errorf("clamped: %+v", z.Layers[1])
	}
	z.SetLayerVelocity(1, 32, 63)
	// Removing the middle gives its range to the layer below.
	z.RemoveLayer(1)
	if len(z.Layers) != 2 || z.Layers[0].VelHigh != 63 || z.Layers[1].VelLow != 64 {
		t.Fatalf("remove middle: %+v", z.Layers)
	}
	// The lowest gives its range to the one above.
	z.SplitByVelocity()
	z.RemoveLayer(0)
	if z.Layers[0].VelLow != 0 {
		t.Errorf("remove lowest: %+v", z.Layers)
	}
	// The last but one folds the zone back, the last layer's sample and
	// takes the zone's.
	z.Layers[0].Sample, z.Layers[0].RoundRobin = 1, []int{2}
	for len(z.Layers) > 1 {
		z.RemoveLayer(len(z.Layers) - 1)
	}
	for len(z.Layers) > 0 {
		z.RemoveLayer(len(z.Layers) - 1)
	}
	if z.Layers != nil || z.Sample != 1 || !reflect.DeepEqual(z.RoundRobin, []int{2}) {
		t.Errorf("folded back: %+v", z)
	}
}

func TestZoneRoundRobin(t *testing.T) {
	z := Zone{Sample: 0}
	z.AddRoundRobin(-1, 1)
	z.AddRoundRobin(-1, 2)
	z.SetRoundRobin(-1, 0, 3)
	if !reflect.DeepEqual(z.RoundRobin, []int{3, 2}) || z.Takes(-1) != 3 {
		t.Errorf("zone takes %v", z.RoundRobin)
	}
	z.RemoveRoundRobin(-1, 0)
	z.RemoveRoundRobin(-1, 0)
	if z.RoundRobin != nil || z.Takes(-1) != 1 {
		t.Errorf("emptied %v", z.RoundRobin)
	}
	z.SplitByVelocity()
	z.AddRoundRobin(1, 4)
	if !reflect.DeepEqual(z.Layers[1].RoundRobin, []int{4}) || z.Takes(1) != 2 || z.Takes(5) != 0 {
		t.Errorf("layer takes %+v", z.Layers)
	}
	z.AddRoundRobin(7, 1) // no such layer
	z.RemoveRoundRobin(1, 3)
	if len(z.Layers[1].RoundRobin) != 1 {
		t.Errorf("out of range removed: %+v", z.Layers[1])
	}
}

// Articulations: unique names, renames refused when taken, triggers
// tidied, zones of their own copied from the instrument's, and the whole
// thing through the .finst file.
func TestArticulationEdits(t *testing.T) {
	in := NewInstrument("Strings", "a")
	in.Samples = []string{"a", "b", "c"}
	in.Zones = []Zone{{Sample: 0, Low: NoteMin, High: NoteC4 - 1}, {Sample: 1, Low: NoteC4, High: NoteMax}}
	a := in.AddArticulation("pizzicato")
	b := in.AddArticulation("pizzicato")
	if a != 0 || b != 1 || in.Articulations[1].Name != "pizzicato 2" {
		t.Fatalf("names %+v", in.Articulations)
	}
	if err := in.RenameArticulation(1, "PIZZICATO"); err == nil {
		t.Error("a taken name (in another case) was allowed")
	}
	if err := in.RenameArticulation(1, "  "); err == nil {
		t.Error("an empty name was allowed")
	}
	if err := in.RenameArticulation(1, "tremolo"); err != nil || in.Articulations[1].Name != "tremolo" {
		t.Errorf("rename: %v %+v", err, in.Articulations[1])
	}
	in.SetTriggers(0, " pizz., , pizzicato, PIZZ. ")
	if !reflect.DeepEqual(in.Articulations[0].Triggers, []string{"pizz.", "pizzicato"}) {
		t.Errorf("triggers %q", in.Articulations[0].Triggers)
	}
	in.SetOwnZones(0, true)
	in.Articulations[0].Zones[1].Sample = 2
	if in.Zones[1].Sample != 1 || len(in.Articulations[0].Zones) != 2 {
		t.Errorf("own zones share the instrument's: %+v", in.Articulations[0].Zones)
	}
	if got := in.ArticulationIndex("pizz."); got != 1 {
		t.Errorf("found by its trigger at %d", got)
	}
	in.MoveArticulation(0, 1)
	if in.Articulations[0].Name != "tremolo" || in.Articulations[1].Name != "pizzicato" {
		t.Errorf("moved: %+v", in.Articulations)
	}
	path := t.TempDir() + "/strings.finst"
	if err := SaveInstrumentFile(path, in); err != nil {
		t.Fatal(err)
	}
	back, err := LoadInstrumentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Articulations[1].Zones, in.Articulations[1].Zones) || !reflect.DeepEqual(back.Articulations[1].Triggers, in.Articulations[1].Triggers) {
		t.Errorf("through the file: %+v", back.Articulations)
	}
	in.SetOwnZones(1, false)
	in.RemoveArticulation(0)
	if len(in.Articulations) != 1 || in.Articulations[0].Zones != nil {
		t.Errorf("after: %+v", in.Articulations)
	}
	// An instrument with no keymap starts its articulation on the first
	// sample everywhere.
	solo := NewInstrument("Solo", "a")
	solo.SetOwnZones(solo.AddArticulation(""), true)
	if z := solo.Articulations[0].Zones; len(z) != 1 || z[0].Low != NoteMin || z[0].High != NoteMax || solo.Articulations[0].Name != "articulation" {
		t.Errorf("no keymap: %+v", solo.Articulations)
	}
}
