package script

import (
	"testing"

	"foundry/internal/platform"

	"github.com/veandco/go-sdl2/sdl"
)

// A level script polling keys from :tick: and hearing :key_down:/:key_up:.
func TestScriptsReadKeysAndHearThem(t *testing.T) {
	withPlayerSettings(t)
	platform.ResetKeys()
	t.Cleanup(platform.ResetKeys)
	lvl := pointerLevel(t, "", `
def on_tick(:tick:, dt float) -> null
    if key_pressed("space")
        @crate.attr.n = @crate.attr.n + 1
    end
    if action_down("jump")
        @crate.attr.x = key_held_time("Space")
    end
    if len(keys_down()) == 1
        @crate.attr.hit = keys_down()[0]
    end
end

def on_down(:key_down:, key str) -> null
    @crate.attr.level = key
end

def on_up(:key_up:, "Space", held float) -> null
    @crate.attr.seen = "up"
end`)
	ptick(lvl, 1)

	platform.PressKey(platform.KeyInput(sdl.SCANCODE_SPACE))
	ptick(lvl, 31)
	if n := crateAttr(t, lvl, "n").I; n != 1 {
		t.Errorf("key_pressed was true on %d ticks, want 1", n)
	}
	if x := crateAttr(t, lvl, "x").F; x < 0.49 || x > 0.51 {
		t.Errorf("key_held_time after 30 more ticks = %g, want 0.5", x)
	}
	if got := crateAttr(t, lvl, "level").S; got != "Space" {
		t.Errorf(":key_down: carried %q, want Space", got)
	}
	if got := crateAttr(t, lvl, "hit").S; got != "Space" {
		t.Errorf("keys_down()[0] = %q, want Space", got)
	}

	platform.ReleaseKey(platform.KeyInput(sdl.SCANCODE_SPACE))
	ptick(lvl, 1)
	if got := crateAttr(t, lvl, "seen").S; got != "up" {
		t.Errorf(":key_up: for Space was not heard (seen = %q)", got)
	}
}
