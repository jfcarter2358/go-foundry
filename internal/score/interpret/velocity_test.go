package interpret

import "testing"

// A vel= mark (the piano roll's velocity lane) sets its notes' velocity
// whatever the dynamics and accents say; the next note reads them again.
func TestVelocityMarks(t *testing.T) {
	s := one(t, "bars: 1", "| qC4(p) qD4(vel=120,acc) qE4+G4(vel=20) qF4 |")
	p := perform(t, s)
	want := []float32{48.0 / 127, 120.0 / 127, 20.0 / 127, 20.0 / 127, 48.0 / 127}
	if len(p.Notes) != len(want) {
		t.Fatalf("%d notes", len(p.Notes))
	}
	for i, n := range p.Notes {
		if d := n.Velocity - want[i]; d > 1e-6 || d < -1e-6 {
			t.Errorf("note %d (MIDI %d): velocity %.4f, want %.4f", i, n.MIDI, n.Velocity, want[i])
		}
	}
	if p.Notes[1].Accent != 1 {
		t.Errorf("a set velocity has no accent on top: %v", p.Notes[1].Accent)
	}
	for _, is := range s.Validate(nil) {
		t.Errorf("valid velocity marks flagged: %v", is)
	}
	bad := one(t, "bars: 1", "| qC4(vel=200) qD4 hr |")
	if len(bad.Validate(nil)) == 0 {
		t.Error("vel=200 passed validation")
	}
}
