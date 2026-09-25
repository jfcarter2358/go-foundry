package devui

import (
	"strings"
	"testing"

	"foundry/internal/platform"
	"foundry/internal/world"
)

// output is the scrollback as one string, for tests that care what was said
// rather than how it was coloured.
func output() string {
	var b strings.Builder
	for _, l := range console.back {
		b.WriteString(l.text)
		b.WriteByte('\n')
	}
	return b.String()
}

// lastKind is the colour of the newest row.
func lastKind() lineKind {
	if len(console.back) == 0 {
		return kindOut
	}
	return console.back[len(console.back)-1].kind
}

func TestEveryCommandIsReachableByName(t *testing.T) {
	byName = nil
	for _, name := range commandNames() {
		if lookup(name) == nil {
			t.Errorf("%q is in the table but does not resolve", name)
		}
	}
	if len(commandNames()) < 10 {
		t.Fatalf("only %d commands — the table did not build", len(commandNames()))
	}
}

func TestHelpListsEveryCommand(t *testing.T) {
	reset(t)
	run(nil, "help")
	out := output()
	for _, name := range commandNames() {
		if !strings.Contains(out, name) {
			t.Errorf("help did not mention %q", name)
		}
	}
}

func TestHelpOnOneCommandShowsItsDetail(t *testing.T) {
	reset(t)
	run(nil, "help tp")
	out := output()
	if !strings.Contains(out, "teleport the player") {
		t.Fatalf("help tp did not print the summary:\n%s", out)
	}
	if !strings.Contains(out, "ID first and as a tag second") {
		t.Fatalf("help tp did not print the detail:\n%s", out)
	}
}

func TestAnUnknownCommandSuggestsTheNearestOne(t *testing.T) {
	reset(t)
	run(nil, "spwan grenade")
	out := output()
	if !strings.Contains(out, "spawn") {
		t.Fatalf("no suggestion for a near miss:\n%s", out)
	}
	if lastKind() != kindErr {
		t.Errorf("the failure was not reported as one")
	}
}

func TestASigilLineGoesToFlang(t *testing.T) {
	reset(t)
	// No level, so it cannot run — but the point is which path it took: a
	// command dispatch would have complained about there being no command
	// called "@player.prop.position".
	run(nil, "@player.prop.position")
	if out := output(); strings.Contains(out, "no command") {
		t.Fatalf("an @ line was read as a command:\n%s", out)
	}
}

func TestTheReplModeTakesEveryLineAndExitLeaves(t *testing.T) {
	reset(t)
	run(nil, "repl")
	if !console.repl {
		t.Fatal("repl did not switch modes")
	}
	if got := prompt(); got != "flang> " {
		t.Fatalf("prompt = %q", got)
	}
	// "help" is a command, and in this mode it must not be one.
	run(nil, "help")
	if strings.Contains(output(), "list the commands") {
		t.Fatal("a command ran from inside the flang prompt")
	}
	run(nil, "exit")
	if console.repl {
		t.Fatal("exit did not leave the flang prompt")
	}
}

func TestSwitchArgFlipsWithNoArgumentAndReadsOneWithIt(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		current bool
		want    bool
		wantErr bool
	}{
		{nil, false, true, false},
		{nil, true, false, false},
		{[]string{"on"}, false, true, false},
		{[]string{"OFF"}, true, false, false},
		{[]string{"1"}, false, true, false},
		{[]string{"sideways"}, false, false, true},
	} {
		got, err := switchArg(tc.args, tc.current)
		if (err != nil) != tc.wantErr {
			t.Errorf("switchArg(%v, %v) error = %v", tc.args, tc.current, err)
			continue
		}
		if err == nil && got != tc.want {
			t.Errorf("switchArg(%v, %v) = %v, want %v", tc.args, tc.current, got, tc.want)
		}
	}
}

func TestASwitchCommandReportsWhatItDid(t *testing.T) {
	reset(t)
	was := world.EntityEntityCollisionEnabled
	t.Cleanup(func() { world.EntityEntityCollisionEnabled = was })

	world.EntityEntityCollisionEnabled = true
	run(nil, "entity_collision off")
	if world.EntityEntityCollisionEnabled {
		t.Fatal("the switch did not go off")
	}
	if !strings.Contains(output(), "entity_collision off") {
		t.Fatalf("no confirmation:\n%s", output())
	}
	run(nil, "entity_collision")
	if !world.EntityEntityCollisionEnabled {
		t.Fatal("a bare switch command did not flip it back")
	}
}

// noclip is free-fly plus no world collision. It used to be "gravity off" plus
// no world collision, which is not noclip at all: a body walking through walls
// while still being pulled down is a body falling out of the level.
func TestNoclipMovesBothSwitchesTogether(t *testing.T) {
	reset(t)
	fly, collide := platform.FlyMode, world.EntityWorldCollisionEnabled
	t.Cleanup(func() { platform.FlyMode = fly; world.EntityWorldCollisionEnabled = collide })

	platform.FlyMode = false
	world.EntityWorldCollisionEnabled = true
	run(nil, "noclip on")
	if !platform.FlyMode || world.EntityWorldCollisionEnabled {
		t.Fatal("noclip on left the player walking, or collision on")
	}
	run(nil, "noclip off")
	if platform.FlyMode || !world.EntityWorldCollisionEnabled {
		t.Fatal("noclip off did not put both back")
	}
}

// The two halves are separately addressable, and `gravity off` must not fly the
// player: weightless is not the same as noclip.
func TestGravityAndFlyAreSeparateSwitches(t *testing.T) {
	reset(t)
	grav, fly := platform.GravityEnabled, platform.FlyMode
	t.Cleanup(func() { platform.GravityEnabled, platform.FlyMode = grav, fly })

	platform.GravityEnabled, platform.FlyMode = true, false
	run(nil, "gravity off")
	if platform.GravityEnabled {
		t.Fatal("gravity off did not turn gravity off")
	}
	if platform.FlyMode {
		t.Fatal("gravity off also put the player in free-fly")
	}
	run(nil, "fly on")
	if !platform.FlyMode {
		t.Fatal("fly on did not turn free-fly on")
	}
	if platform.GravityEnabled {
		t.Fatal("fly on turned gravity back on")
	}
}

func TestVecLiteralsAreAlwaysFloats(t *testing.T) {
	// `<1, 0, 0>` is three ints in flang, and not a vec at all.
	got := vecLit([3]float32{1, 0, -2.5})
	if got != "<1.0, 0.0, -2.5>" {
		t.Fatalf("vecLit = %q", got)
	}
}

func TestStringLiteralsAreEscaped(t *testing.T) {
	if got := strLit(`a"b\c`); got != `"a\"b\\c"` {
		t.Fatalf("strLit = %s", got)
	}
}

func TestSettingValueTakesTheTypeItLooksLike(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want any
	}{
		{"true", true},
		{"false", false},
		{"7", int64(7)},
		{"0.5", 0.5},
		{"medium", "medium"},
	} {
		if got := settingValue(tc.in); got != tc.want {
			t.Errorf("settingValue(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}
