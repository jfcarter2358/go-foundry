package play

// script_test.go — the vocabulary, and what a command costs in ticks.
//
// The timing is the part worth pinning: a script says "walk forward for two
// seconds" and the runner has to spend exactly that, whatever the tick rate,
// and let go of what it was holding at the end. A run that quietly walked for
// one second, or one that left the trigger down, would look like a level bug
// rather than a harness bug — which is the expensive kind of wrong.

import (
	"strings"
	"testing"

	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// fakeTarget is a level and a log, which is all a runner needs.
type fakeTarget struct {
	lvl   *world.Level
	log   []string
	shots int
}

func (f *fakeTarget) Level() *world.Level { return f.lvl }
func (f *fakeTarget) Emit(kind, name, value string) {
	f.log = append(f.log, strings.TrimSpace(kind+" "+name+" "+value))
}
func (f *fakeTarget) Read(string) string { return "?" }
func (f *fakeTarget) Shot(name string) (string, error) {
	f.shots++
	return "/tmp/" + name, nil
}

func newTarget(t *testing.T) *fakeTarget {
	t.Helper()
	lvl := &world.Level{}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	prev := world.PlayerEntity
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{})
	t.Cleanup(func() { world.PlayerEntity = prev })
	return &fakeTarget{lvl: lvl}
}

func TestParseTakesAScriptAndRefusesATypo(t *testing.T) {
	cmds, err := Parse("look 90 -5; forward 2 // to the door\nuse")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cmds) != 3 {
		t.Fatalf("got %d commands, want 3: %+v", len(cmds), cmds)
	}
	if cmds[1].Verb != "forward" || cmds[1].Args[0] != "2" {
		t.Errorf("second command is %+v", cmds[1])
	}
	if _, err := Parse("look 90; sprnt on"); err == nil {
		t.Error("a misspelt verb parsed")
	}
	// The one that matters: # is a tag, not a comment.
	cmds, err = Parse("look_at #enemy")
	if err != nil {
		t.Fatalf("Parse a tag: %v", err)
	}
	if cmds[0].Args[0] != "#enemy" {
		t.Errorf("the tag was eaten: %+v", cmds[0])
	}
}

func TestParseChecksHowManyArgumentsAVerbTakes(t *testing.T) {
	if _, err := Parse("crouch"); err == nil {
		t.Error("crouch with no on/off parsed")
	}
	if _, err := Parse("say a whole sentence, spaces and all"); err != nil {
		t.Errorf("say takes the rest of the line: %v", err)
	}
}

func TestAWalkIsSpentOverExactlyItsSeconds(t *testing.T) {
	f := newTarget(t)
	cmds, err := Parse("forward 1")
	if err != nil {
		t.Fatal(err)
	}
	r := New(f, cmds)

	const dt = 1.0 / 60
	moving := 0
	for i := 0; i < 200 && !r.Done(); i++ {
		in := r.Tick(dt)
		if in.Intent.Len() > 0 {
			moving++
		}
	}
	if !r.Done() {
		t.Fatal("the script never finished")
	}
	// One second at 60 Hz, give or take the tick the hold runs out on.
	if moving < 59 || moving > 61 {
		t.Errorf("walked for %d ticks, want 60", moving)
	}
}

func TestFireHoldsTheTriggerAndLetsGo(t *testing.T) {
	f := newTarget(t)
	cmds, err := Parse("fire 0.2")
	if err != nil {
		t.Fatal(err)
	}
	r := New(f, cmds)
	hooks.TakeScriptPrimaryFireEdges()

	const dt = 1.0 / 60
	r.Tick(dt)
	if down, _ := hooks.TakeScriptPrimaryFireEdges(); !down {
		t.Error("fire did not press the trigger")
	}
	for i := 0; i < 60 && !r.Done(); i++ {
		r.Tick(dt)
	}
	if !r.Done() {
		t.Fatal("fire never finished")
	}
	if _, up := hooks.TakeScriptPrimaryFireEdges(); !up {
		t.Error("fire never let the trigger go — the next run would start shooting")
	}
}

func TestAFinishedRunIsHoldingNothing(t *testing.T) {
	f := newTarget(t)
	cmds, err := Parse("forward 0.1")
	if err != nil {
		t.Fatal(err)
	}
	r := New(f, cmds)
	for i := 0; i < 60 && !r.Done(); i++ {
		r.Tick(1.0 / 60)
	}
	if in := r.Tick(1.0 / 60); in.Intent.Len() != 0 {
		t.Errorf("still walking after the script ended: %v", in.Intent)
	}
}
