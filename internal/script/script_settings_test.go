package script

import (
	"testing"

	"foundry/internal/prefs"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// A settings screen's round trip: an unset setting reads as its default, a
// written one reads back as what was written — in the default's own type — and
// the store is marked for saving.
func TestSettingReadsBackWhatSetSettingWrote(t *testing.T) {
	prefs.Reset()
	t.Cleanup(prefs.Reset)
	root := withTempScriptDir(t)
	path := writeScript(t, root, "s.flang", `def on_spawn(:spawn:) -> null
    @self.attr.before = setting("hud_corner", "right")
    set_setting("hud_corner", "left")
    set_setting("hud_size", 3)
    @self.attr.after = setting("hud_corner", "right")
    @self.attr.size = setting("hud_size", 1)
    @self.attr.wrong_kind = setting("hud_corner", false)
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "screen", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{
			"before": world.StringAttr(""), "after": world.StringAttr(""),
			"size": world.IntAttr(0), "wrong_kind": world.BoolAttr(true),
		},
	})
	runTicks(t, lvl, 1)

	e := &lvl.Entities[0]
	if a, _ := e.Attr("before"); a.S != "right" {
		t.Errorf("an unset setting read %q, want its default", a.S)
	}
	if a, _ := e.Attr("after"); a.S != "left" {
		t.Errorf("the setting read %q after being set to left", a.S)
	}
	if a, _ := e.Attr("size"); a.I != 3 {
		t.Errorf("an int setting read %v, want 3", a.I)
	}
	if a, _ := e.Attr("wrong_kind"); a.B {
		t.Error("a str setting asked for as a bool did not fall back to the bool default")
	}
	if !prefs.Changed {
		t.Error("set_setting did not mark the settings for saving")
	}
}
