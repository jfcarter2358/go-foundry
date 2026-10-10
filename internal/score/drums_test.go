package score

import "testing"

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
