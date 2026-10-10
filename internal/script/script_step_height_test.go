package script

// script_step_height_test.go — `@level.prop.step_height`, the one writable
// property on the level.
//
// It matters that this is writable at all: the level file's `player:` block is
// authored once, and a crawlspace or a climbing sequence is a thing that starts
// and stops during play. What has to hold is that a write lands on the field
// MovePlayer reads (world.Level.Player), that reading reports the height in
// force rather than the raw zero a level that set nothing carries, and that
// writing zero hands the setting back instead of pinning the player against
// every floor seam in the level.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

func TestScriptWritesTheLevelStepHeight(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "crawl.flang", `def on_spawn(:spawn:) -> null
    @level.prop.step_height = 0.2
end`)

	lvl := tickTestLevel(t)
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 1)

	if got := lvl.Player.StepHeight; got != 0.2 {
		t.Errorf("step height is %v, want 0.2 — the write did not reach the level", got)
	}
	if got, want := lvl.Player.EffectiveStepHeight(), float32(0.2); got != want {
		t.Errorf("the height in force is %v, want %v", got, want)
	}
}

// Reading it on a level that never set one reports the default, not zero: zero
// is never a height the player is actually walking with.
func TestScriptReadsTheStepHeightInForce(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "read.flang", `def on_spawn(:spawn:) -> null
    @self.attr.seen = @level.prop.step_height
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "reader", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"seen": world.FloatAttr(0)},
	})
	runTicks(t, lvl, 1)

	seen, _ := lvl.Entities[0].Attr("seen")
	if got, want := float32(seen.F), world.DefaultStepHeight(); got != want {
		t.Errorf("read %v, want the default %v", got, want)
	}
}

// Zero is how a script hands the setting back, not a way to say "no stepping".
func TestScriptWritingZeroRestoresTheDefault(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "restore.flang", `def on_spawn(:spawn:) -> null
    @level.prop.step_height = 0
end`)

	lvl := tickTestLevel(t)
	lvl.Player.StepHeight = 0.2
	lvl.Scripts = []string{path}
	runTicks(t, lvl, 1)

	if got := lvl.Player.StepHeight; got != 0 {
		t.Errorf("step height is %v, want 0", got)
	}
	if got, want := lvl.Player.EffectiveStepHeight(), world.DefaultStepHeight(); got != want {
		t.Errorf("the height in force is %v, want the default %v", got, want)
	}
}
