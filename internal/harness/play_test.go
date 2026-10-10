package harness

// play_test.go — a script actually moves the player, through the real frame.
//
// The thing worth pinning is that `forward` is a walk and not a teleport: it
// goes through sim.Step, which means gravity, collision and the level's own
// shape all apply, and a script that walks into a wall stays where it is. A
// harness that got this wrong would pass every test it wrote about itself and
// lie about every level it was pointed at.

import (
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestAScriptWalksThePlayerThroughTheFrame(t *testing.T) {
	dir := scratchProject(t, "walkroom")
	s, err := Open(Options{Project: dir, Level: "walkroom"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Teleport(mgl32.Vec3{5, 1, 5}) // the middle of the room: leveltest's spans 0..10
	s.RunFor(0.5)                   // land
	from := world.PlayerEntity.Position

	if _, err := s.Play("look 0 0; forward 1", 10); err != nil {
		t.Fatalf("Play: %v", err)
	}
	moved := world.PlayerEntity.Position.Sub(from)
	if moved.Len() < 0.5 {
		t.Errorf("a second of walking moved the player %.2fm (from %v to %v)",
			moved.Len(), from, world.PlayerEntity.Position)
	}
	// Along +X, which is where yaw 0 looks — a walk in the wrong axis is the
	// classic sign of a basis copied wrongly out of the engine.
	if moved.X() < 0.5 || abs(moved.Z()) > 0.2 {
		t.Errorf("walked %v, want it along +X", moved)
	}
}

func TestAScriptReportsWhatItDidAndRefusesATypo(t *testing.T) {
	dir := scratchProject(t, "sayroom")
	var log []string
	s, err := Open(Options{Project: dir, Level: "sayroom", OnEvent: func(e Event) {
		log = append(log, strings.TrimSpace(e.Kind+" "+e.Name+" "+e.Value))
	}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Play("say testing the door; use", 10); err != nil {
		t.Fatalf("Play: %v", err)
	}
	joined := strings.Join(log, "\n")
	for _, want := range []string{"testing the door", "INTERACT"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the log does not mention %q:\n%s", want, joined)
		}
	}
	// A typo is refused before anything runs, rather than half way through.
	if _, err := s.Play("forward 1; jmup", 10); err == nil {
		t.Error("a misspelt command was accepted")
	}
}

// TestAPlaySessionSaysWhereItIs covers the other half of a headless run: the
// state report, which is what a blind session has instead of eyes.
func TestAPlaySessionSaysWhereItIs(t *testing.T) {
	dir := scratchProject(t, "stateroom")
	s, err := Open(Options{Project: dir, Level: "stateroom"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Teleport(mgl32.Vec3{5, 1, 5}) // the middle of the room: leveltest's spans 0..10
	s.RunFor(0.5)
	got := s.State()
	for _, want := range []string{"at ", "yaw", "crosshair"} {
		if !strings.Contains(got, want) {
			t.Errorf("the state report has no %q in it:\n%s", want, got)
		}
	}
	// Standing in a sealed room, the crosshair has to be on one of its walls.
	if !strings.Contains(got, "brush") {
		t.Errorf("the crosshair found nothing inside a closed room:\n%s", got)
	}
}

func abs(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}
