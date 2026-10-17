package score

import (
	"fmt"
	"testing"
	"time"

	"foundry/internal/tracker"
)

// The kit map names each key once, never puts two drums at one place with
// one head (they would read the same), and stores each drum as its key.
func TestGMDrums(t *testing.T) {
	keys := map[int]bool{}
	places := map[[2]int]string{}
	for _, d := range GMDrums {
		if keys[d.Key] {
			t.Errorf("key %d twice", d.Key)
		}
		keys[d.Key] = true
		at := [2]int{d.Pos, int(d.Head)}
		if o, ok := places[at]; ok {
			t.Errorf("%s and %s look the same", o, d.Name)
		}
		places[at] = d.Name
		if p := DrumPitch(d); p.MIDI() != d.Key {
			t.Errorf("%s stored as %s (%d)", d.Name, p, p.MIDI())
		}
		if pos, h := DrumDisplay(DrumPitch(d)); pos != d.Pos || h != d.Head {
			t.Errorf("%s shows at %d/%d", d.Name, pos, h)
		}
	}
	for k := 35; k <= 59; k++ {
		if !keys[k] {
			t.Errorf("GM key %d has no place", k)
		}
	}
	if d, ok := DrumAt(5); !ok || d.Name != "Snare" {
		t.Errorf("third space: %+v", d)
	}
	if _, ok := DrumAt(-6); ok {
		t.Error("a drum far below the staff")
	}
	if pos, h := DrumDisplay(Pitch{Step: 0, Alter: 1, Octave: 5}); pos != 5 || h != DrumHeadNormal {
		t.Errorf("C#5 (unmapped) at %d/%d", pos, h)
	}
	if n := DrumName(Pitch{Step: 1, Octave: 2}); n != "Snare" {
		t.Errorf("D2 = %q", n)
	}
	if n := DrumName(Pitch{Step: 0, Octave: 6}); n != "key 84" {
		t.Errorf("C6 = %q", n)
	}
}

// An instrument's own drum map: places, heads, articulations that pick
// another sound, shortcuts, Up/Down through every sound; the library's maps
// read once and again when the file changes.
func TestKits(t *testing.T) {
	in := &tracker.Instrument{Name: "Kit", Playback: tracker.Playback{Drums: []tracker.DrumNote{
		{Key: 36, Name: "Kick", Line: 1, Head: "normal", Shortcut: "b"},
		{Key: 42, Name: "Hat", Line: 9, Head: "x", Shortcut: "G"},
		{Key: 46, Name: "Hat open", Line: 9, Head: "x", Articulation: "open"},
		{Key: 38, Name: "Snare", Line: 5, Shortcut: "A"},
	}}}
	k := KitOf(in)
	if k.GM || len(k.Drums) != 4 {
		t.Fatalf("kit %+v", k)
	}
	if pos, h, art := k.Display(SpellMIDI(46, 0, 1)); pos != 9 || h != DrumHeadX || art != "open" {
		t.Errorf("open hat shows %d %v %q", pos, h, art)
	}
	if pos, h, _ := k.Display(SpellMIDI(61, 0, 1)); pos != SpellMIDI(61, 0, 1).Diatonic()-PercussionBottom || h != DrumHeadNormal {
		t.Errorf("an unmapped key at %d", pos)
	}
	if d, ok := k.ByShortcut('b'); !ok || d.Key != 36 {
		t.Errorf("b writes %+v", d)
	}
	if _, ok := k.ByShortcut('C'); ok {
		t.Error("C writes nothing in this kit")
	}
	if k.Sound(42, []Mark{{Name: "open"}}) != 46 || k.Sound(42, nil) != 42 || k.Sound(46, []Mark{{Name: "open"}}) != 46 || k.Sound(38, []Mark{{Name: "open"}}) != 38 {
		t.Error("articulations pick sounds")
	}
	var walk []int
	for d, ok := k.ForKey(36); ok; d, ok = k.Step(d.Key, 1) {
		walk = append(walk, d.Key)
	}
	if fmt.Sprint(walk) != "[36 38 42 46]" {
		t.Errorf("up through the kit: %v", walk)
	}
	if d, ok := k.Step(38, -1); !ok || d.Key != 36 {
		t.Errorf("down from the snare: %+v", d)
	}
	if KitOf(nil) != GMKit || KitOf(&tracker.Instrument{}) != GMKit {
		t.Error("no map is the GM kit's")
	}

	old := LoadKitInstrument
	defer func() { LoadKitInstrument = old; ReloadKits() }()
	loads := 0
	mod := time.Unix(100, 0)
	LoadKitInstrument = func(name string) (*tracker.Instrument, time.Time, error) {
		loads++
		if name != "percussion/kit" {
			return nil, time.Time{}, fmt.Errorf("no %s", name)
		}
		return in, mod, nil
	}
	ReloadKits()
	if KitFor("percussion/kit").Name != "Kit" || KitFor("percussion/kit").Name != "Kit" || loads != 1 {
		t.Errorf("cached: %d loads", loads)
	}
	if KitFor("nothing") != GMKit {
		t.Error("a missing instrument is the GM kit")
	}
	if (&Line{Instrument: "percussion/kit"}).Kit().Name != "Kit" {
		t.Error("a line's kit")
	}
}

// A General MIDI key finds a kit's sound by its name, then by its place
// and head; and back.
func TestKitFromAndToGM(t *testing.T) {
	in := &tracker.Instrument{Name: "Kit", Playback: tracker.Playback{Drums: []tracker.DrumNote{
		{Key: 60, Name: "Bass Drum", Line: 1, Head: "normal"},
		{Key: 62, Name: "Rim", Line: 5, Head: "normal"},
		{Key: 64, Name: "Hat", Line: 9, Head: "x"},
		{Key: 38, Name: "Odd", Line: 3, Head: "diamond"},
	}}}
	k := KitOf(in)
	for _, c := range []struct {
		gm, want int
		ok       bool
	}{
		{36, 60, true}, // Bass Drum by name
		{38, 38, true}, // the map names key 38 itself
		{42, 64, true}, // Closed Hi-Hat: the x at place 9
		{49, 49, false},
		{90, 90, false},
	} {
		if got, ok := k.FromGM(c.gm); got != c.want || ok != c.ok {
			t.Errorf("FromGM(%d) = %d %v, want %d %v", c.gm, got, ok, c.want, c.ok)
		}
	}
	if got, ok := k.ToGM(60); got != 36 || !ok {
		t.Errorf("ToGM(60) = %d %v", got, ok)
	}
	if got, ok := k.ToGM(64); got != 42 || !ok {
		t.Errorf("ToGM(64) = %d %v", got, ok)
	}
	if got, ok := GMKit.FromGM(42); got != 42 || !ok {
		t.Error("the GM kit maps to itself")
	}
}
