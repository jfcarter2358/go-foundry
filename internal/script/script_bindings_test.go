package script

import (
	"testing"

	"foundry/internal/audio"
	"foundry/internal/platform"
	"foundry/internal/ui"

	"github.com/veandco/go-sdl2/sdl"
)

func withPlayerSettings(t *testing.T) {
	t.Helper()
	prevB, prevC := platform.Bindings, platform.ControlSettingsChanged
	m, mu, e, prevA := audio.MasterGain(), audio.MusicGain(), audio.EffectsGain(), audio.SettingsChanged
	t.Cleanup(func() {
		platform.EndBindingCapture()
		platform.Bindings, platform.ControlSettingsChanged = prevB, prevC
		audio.SetMasterGain(m)
		audio.SetMusicGain(mu)
		audio.SetEffectsGain(e)
		audio.SettingsChanged = prevA
	})
	platform.Bindings = platform.DefaultBindings
}

// The settings screen's whole rebind loop: bind_key listens, the press lands
// in the table, and the screen hears :bound: with the action and slot.
func TestBindKeyPostsBoundToTheScreen(t *testing.T) {
	withPlayerSettings(t)
	root := withTempScriptDir(t)
	st := withUIStack(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    @self.attr.before = binding_name("jump", 1)
    bind_key("jump", 2)
end

def on_bound(:bound:, src ref, action str, slot int) -> null
    @self.attr.action = action
    @self.attr.slot = slot
    @self.attr.after = binding_name(action, slot)
end`)

	s := ui.NewScreen("settings")
	s.Canvas = ui.Canvas{Mode: ui.ScaleConstantPixel}
	s.Script = path
	st.Push(s)
	st.Tick(0, 800, 600)

	lvl := tickTestLevel(t)
	runTicks(t, lvl, 2)
	if !platform.BindingCaptureActive() {
		t.Fatal("bind_key did not start listening")
	}
	platform.CaptureBindingInput(platform.KeyInput(sdl.SCANCODE_J))
	ScriptTick(lvl, 1.0/60.0, true)

	scr := st.Find("settings")
	attr := func(name string) string {
		v, ok := scr.Attr(name)
		if !ok {
			t.Fatalf("attr %s was never set", name)
		}
		if s, ok := v.Str(); ok {
			return s
		}
		n, _ := v.Int()
		return string(rune('0' + n))
	}
	if attr("before") != "Space" {
		t.Errorf("binding_name(jump, 1) = %q", attr("before"))
	}
	if attr("action") != "jump" || attr("slot") != "2" || attr("after") != "J" {
		t.Errorf(":bound: gave %s %s %s", attr("action"), attr("slot"), attr("after"))
	}
}

func TestVolumePropsAndResetBindings(t *testing.T) {
	withPlayerSettings(t)
	root := withTempScriptDir(t)
	withUIStack(t)
	platform.Bindings.Bind(platform.ActReload, 0, platform.KeyInput(sdl.SCANCODE_T))
	path := writeScript(t, root, "l.flang", `def on_spawn(:spawn:) -> null
    @level.prop.master_volume = 0.25
    @level.prop.music_volume = 2
    reset_bindings()
end`)
	lvl := tickTestLevel(t)
	lvl.Chunks[0].Brushes[0].Script = path
	runTicks(t, lvl, 2)

	if audio.MasterGain() != 0.25 || audio.MusicGain() != 1 || !audio.SettingsChanged {
		t.Errorf("master %v, music %v (want clamped to 1), changed %v", audio.MasterGain(), audio.MusicGain(), audio.SettingsChanged)
	}
	if platform.Bindings != platform.DefaultBindings || !platform.ControlSettingsChanged {
		t.Error("reset_bindings did not restore the defaults")
	}
}
