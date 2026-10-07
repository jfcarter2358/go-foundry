package editor

import (
	"testing"

	"foundry/internal/tracker"

	"github.com/veandco/go-sdl2/sdl"
)

func TestTrackerKeyboardLabelsEveryKeyItDraws(t *testing.T) {
	labels := trkKbKeyLabels()
	for semi, l := range labels {
		if l == "" {
			t.Errorf("semitone %d has no computer key", semi)
		}
	}
	if labels[0] != "Z" {
		t.Errorf("C of the lower octave is %q, want Z", labels[0])
	}
	// The octave's C is on both rows; the bottom row's key reads first.
	if labels[12] != ", Q" {
		t.Errorf("the upper C is %q, want \", Q\"", labels[12])
	}
	if labels[28] != "P" {
		t.Errorf("the top key is %q, want P", labels[28])
	}
}

func TestTrackerKeyboardLightsHeldKeys(t *testing.T) {
	defer func(h map[sdl.Scancode]bool, m int) { trkHeldKeys, trkKbMouseSemi = h, m }(trkHeldKeys, trkKbMouseSemi)
	trkHeldKeys = map[sdl.Scancode]bool{sdl.SCANCODE_X: true, sdl.SCANCODE_2: true}
	trkKbMouseSemi = 7
	held := trkKbHeld()
	for semi, on := range held {
		want := semi == 2 || semi == 13 || semi == 7
		if on != want {
			t.Errorf("semitone %d lit=%v, want %v", semi, on, want)
		}
	}
}

func TestTrackerKeyboardBlackKeys(t *testing.T) {
	var got []int
	for s := 0; s < 12; s++ {
		if trkKbIsBlack(s) {
			got = append(got, s)
		}
	}
	want := []int{1, 3, 6, 8, 10}
	if len(got) != len(want) {
		t.Fatalf("black keys %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("black keys %v, want %v", got, want)
		}
	}
}

func TestTrackerHexKeyListsEveryEffectUnderOneHeading(t *testing.T) {
	// The key prints a group's heading when the group changes, so a group
	// split across the list would print its heading twice.
	seen := map[string]bool{}
	last := ""
	for _, fx := range tracker.Effects {
		if fx.Group != last {
			if seen[fx.Group] {
				t.Errorf("group %q is not contiguous in tracker.Effects", fx.Group)
			}
			seen[fx.Group] = true
			last = fx.Group
		}
		code := trkKbCode(fx)
		if len(code) != 3 || code[0] != fx.Cmd {
			t.Errorf("%s reads %q in the key", fx.Name, code)
		}
	}
}
