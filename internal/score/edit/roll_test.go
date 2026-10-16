package edit

import (
	"fmt"
	"strings"
	"testing"

	"foundry/internal/score"
)

func f(n, d int64) score.Frac { return score.F(n, d) }

func pitch(t *testing.T, s string) score.Pitch {
	t.Helper()
	p, err := score.ParsePitch(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// rollText is a line's roll notes as "pitch@start-end/voice".
func rollText(s *score.Score) string {
	var out []string
	for _, n := range RollNotes(s, 0) {
		out = append(out, fmt.Sprintf("%s@%s-%s/%d", n.Pitch, n.Start, n.End, n.Voice+1))
	}
	return strings.Join(out, " ")
}

func voiceText(s *score.Score, v int) string {
	vv := Voice(s, 0, 0, v)
	if vv == nil {
		return "<none>"
	}
	var b strings.Builder
	for _, m := range vv.Measures {
		b.WriteString("| " + score.FormatMeasure(m) + " ")
	}
	return b.String() + "|"
}

func TestRollNotesMergeTiesAndChords(t *testing.T) {
	s := one(t, "4/4", 0, "| qC5 qD5 hE5~ | qE5 qG5+B5 hr |", "| hr hC4 | w_ |")
	if got, want := rollText(s), "C5@0-1/4/1 D5@1/4-1/2/1 E5@1/2-5/4/1 C4@1/2-1/2 G5@5/4-3/2/1 B5@5/4-3/2/1"; got != want {
		t.Errorf("roll:\n got %s\nwant %s", got, want)
	}
	n := RollNotes(s, 0)[2]
	if len(n.Parts) != 2 || n.Parts[0].Loc.Bar != 1 || n.Parts[1].Loc.Bar != 2 {
		t.Errorf("E5's parts %+v", n.Parts)
	}
}

// Adding a note writes notation: values the meter allows, tied where it
// crosses a beat it may not, spelled as asked, a chord with a note that
// starts and stops with it.
func TestRollAddWritesNotation(t *testing.T) {
	s := one(t, "4/4", 1, "| R |")
	if _, err := RollAdd(s, 0, RollNote{Start: f(1, 8), End: f(3, 4), Pitch: pitch(t, "F#4")}); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| er q.F#4~ qF#4 qr |" {
		t.Errorf("F# from beat 1& to beat 4: %s", got)
	}
	fullBars(t, s)
	// The same span in another pitch: a chord.
	if _, err := RollAdd(s, 0, RollNote{Start: f(1, 8), End: f(3, 4), Pitch: pitch(t, "A4")}); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| er q.F#4~+A4~ qF#4+A4 qr |" {
		t.Errorf("chord: %s", got)
	}
	// Across the barline, into a bar the score does not have yet.
	if _, err := RollAdd(s, 0, RollNote{Start: f(3, 4), End: f(3, 2), Pitch: pitch(t, "D5")}); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| er q.F#4~+A4~ qF#4+A4 qD5~ | hD5 hr |" {
		t.Errorf("over the barline: %s", got)
	}
	if s.Measures() != 2 {
		t.Errorf("%d bars", s.Measures())
	}
	fullBars(t, s)
}

// A note on a triplet grid comes out as a triplet; a 5/16 note as tied
// values.
func TestRollAddTupletsAndOddLengths(t *testing.T) {
	s := one(t, "4/4", 0, "| R |")
	for k := int64(0); k < 3; k++ {
		if _, err := RollAdd(s, 0, RollNote{Start: f(k, 12), End: f(k+1, 12), Pitch: pitch(t, "C5")}); err != nil {
			t.Fatal(err)
		}
	}
	if got := voiceText(s, 0); got != "| 3{eC5 eC5 eC5} qr hr |" {
		t.Errorf("triplets: %s", got)
	}
	fullBars(t, s)
	s = one(t, "4/4", 0, "| R |")
	if _, err := RollAdd(s, 0, RollNote{Start: f(1, 2), End: f(13, 16), Pitch: pitch(t, "G4")}); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| hr qG4~ sG4 e.r |" {
		t.Errorf("5/16: %s", got)
	}
	fullBars(t, s)
}

// Writing over a voice cuts what the note lands on; RollFreeVoice finds a
// voice that loses nothing.
func TestRollOverlapAndFreeVoice(t *testing.T) {
	s := one(t, "4/4", 0, "| wC5 |")
	if v := RollFreeVoice(s, 0, 0, f(1, 4), f(1, 2), 0); v != 1 {
		t.Errorf("free voice %d, want 2nd", v)
	}
	if v := RollFreeVoice(s, 0, 0, f(0, 1), f(1, 1), 0); v != 0 {
		t.Errorf("a chord with the whole note: voice %d", v)
	}
	if _, err := RollAdd(s, 0, RollNote{Start: f(1, 2), End: f(3, 4), Pitch: pitch(t, "E5")}); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| hC5 qE5 qr |" {
		t.Errorf("written over: %s", got)
	}
	if _, err := RollAdd(s, 0, RollNote{Voice: 1, Start: f(1, 4), End: f(1, 2), Pitch: pitch(t, "A4")}); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 1); got != "| qr qA4 hr |" {
		t.Errorf("voice 2: %s", got)
	}
	fullBars(t, s)
}

// Moving and resizing keep what was written about the note: its own marks
// travel with it, a dynamic stays where it was written (on the rest left
// behind), the spelling and the other notes' marks stay.
func TestRollReplaceKeepsMarks(t *testing.T) {
	s := one(t, "4/4", -3, "| qEb5(stacc,p) qG5(acc) hr |")
	ns := RollNotes(s, 0)
	eb := ns[0]
	moved := eb
	moved.Start, moved.End = f(1, 2), f(3, 4)
	if _, err := RollReplace(s, 0, eb, moved); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| qr(p) qG5(acc) qEb5(stacc) qr |" {
		t.Errorf("moved: %s", got)
	}
	// A semitone up, spelled as the caller asks, stays where it is.
	ns = RollNotes(s, 0)
	g := ns[0]
	up := g
	up.Pitch = pitch(t, "Ab5")
	if _, err := RollReplace(s, 0, g, up); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| qr(p) qAb5(acc) qEb5(stacc) qr |" {
		t.Errorf("transposed: %s", got)
	}
	fullBars(t, s)
}

// Removing a note leaves a rest, which keeps the dynamic the note carried;
// removing one note of a chord leaves the others.
func TestRollRemove(t *testing.T) {
	s := one(t, "4/4", 0, "| qC5+E5(mf,stacc) qD5 hr |")
	ns := RollNotes(s, 0)
	if err := RollRemove(s, 0, ns[1]); err != nil { // E5
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| qC5(mf,stacc) qD5 hr |" {
		t.Errorf("one of a chord: %s", got)
	}
	ns = RollNotes(s, 0)
	if err := RollRemove(s, 0, ns[0]); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| qr(mf) qD5 hr |" {
		t.Errorf("a note: %s", got)
	}
	if err := RollRemove(s, 0, ns[0]); err == nil {
		t.Error("removed a note twice")
	}
}

// A tied note's whole chain is rewritten when it is edited, and a note
// held through others' changes keeps its tie.
func TestRollTieChains(t *testing.T) {
	s := one(t, "4/4", 0, "| hC5 hE5~ | hE5 hr |")
	ns := RollNotes(s, 0)
	e := ns[1]
	short := e
	short.End = f(5, 4)
	if _, err := RollReplace(s, 0, e, short); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| hC5 hE5~ | qE5 qr hr |" {
		t.Errorf("shortened: %s", got)
	}
	// A G that starts under the held E goes into a voice of its own, so
	// the E holds.
	g := RollNote{Start: f(3, 4), End: f(1, 1), Pitch: pitch(t, "G5")}
	g.Voice = RollFreeVoice(s, 0, 0, g.Start, g.End, 0)
	if g.Voice != 1 {
		t.Fatalf("G into voice %d", g.Voice+1)
	}
	if _, err := RollAdd(s, 0, g); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0) + " " + voiceText(s, 1); got != "| hC5 hE5~ | qE5 qr hr | | hr qr qG5 |" {
		t.Errorf("held through: %s", got)
	}
	fullBars(t, s)
	// A note held while the notes around it change keeps its tie: take
	// the C out from under a held E.
	s = one(t, "4/4", 0, "| hC5+E5~ qE5+G5 qr |")
	if err := RollRemove(s, 0, RollNotes(s, 0)[0]); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| hE5~ qE5+G5 qr |" {
		t.Errorf("held E: %s", got)
	}
	if got := rollText(s); got != "E5@0-3/4/1 G5@1/2-3/4/1" {
		t.Errorf("roll: %s", got)
	}
	fullBars(t, s)
}

func TestRollVelocity(t *testing.T) {
	s := one(t, "4/4", 0, "| qC5+E5 qD5 hr |")
	n := RollNotes(s, 0)[0]
	if err := RollSetVelocity(s, 0, n, 100); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| qC5+E5(vel=100) qD5 hr |" {
		t.Errorf("velocity: %s", got)
	}
	if v := RollNotes(s, 0)[1].Vel; v != 100 {
		t.Errorf("chord's other note velocity %d", v)
	}
	if err := RollSetVelocity(s, 0, n, 0); err != nil || voiceText(s, 0) != "| qC5+E5 qD5 hr |" {
		t.Errorf("cleared: %v %s", err, voiceText(s, 0))
	}
	// A moved note keeps its velocity.
	RollSetVelocity(s, 0, RollNotes(s, 0)[2], 30)
	d := RollNotes(s, 0)[2]
	m := d
	m.Start, m.End = f(1, 2), f(3, 4)
	if got, err := RollReplace(s, 0, d, m); err != nil || got.Vel != 30 {
		t.Errorf("moved velocity %d %v: %s", got.Vel, err, voiceText(s, 0))
	}
}

// Moving a group keeps its shape: a note held through another stays held
// and both stay in their voice; a group the voice cannot take moves whole.
func TestRollMoveKeepsAGroup(t *testing.T) {
	s := one(t, "4/4", 0, "| hC5+E5~ qE5+G5 qr | R |")
	ns := RollNotes(s, 0) // C5, E5 (held), G5
	var olds, news []RollNote
	for _, n := range ns[1:] {
		olds = append(olds, n)
		m := n
		m.Start, m.End = n.Start.Add(f(1, 1)), n.End.Add(f(1, 1))
		news = append(news, m)
	}
	if _, err := RollMove(s, 0, olds, news, true); err != nil {
		t.Fatal(err)
	}
	if got := voiceText(s, 0); got != "| hC5 hr | hE5~ qE5+G5 qr |" {
		t.Errorf("moved a bar on: %s", got)
	}
	// Onto a note of the voice: the group goes to voice 2 whole.
	ns = RollNotes(s, 0)
	olds, news = nil, nil
	for _, n := range ns[1:] {
		olds = append(olds, n)
		m := n
		m.Start, m.End = n.Start.Sub(f(1, 1)), n.End.Sub(f(1, 1))
		news = append(news, m)
	}
	got, err := RollMove(s, 0, olds, news, true)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Voice != 1 || got[1].Voice != 1 {
		t.Errorf("voices %d %d", got[0].Voice+1, got[1].Voice+1)
	}
	if v1, v2 := voiceText(s, 0), voiceText(s, 1); v1 != "| hC5 hr | R |" || v2 != "| hE5~ qE5+G5 qr |" {
		t.Errorf("moved back over the C: %s / %s", v1, v2)
	}
	fullBars(t, s)
}
