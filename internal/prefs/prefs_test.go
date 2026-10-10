package prefs

import "testing"

// A choice made on a settings screen comes back as the same kind of value
// after a save and a load — a number stays a number, a bool a bool.
func TestSettingsSurviveASaveAndLoad(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	for name, v := range map[string]any{"hud_corner": "left", "hud_scale": 0.75, "hints": false, "count": 3} {
		if err := Set(name, v); err != nil {
			t.Fatalf("Set(%q): %v", name, err)
		}
	}
	if !Changed {
		t.Fatal("a Set did not mark the store changed")
	}
	data, err := Marshal()
	if err != nil {
		t.Fatal(err)
	}
	Reset()
	if err := Load(data); err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]any{"hud_corner": "left", "hud_scale": 0.75, "hints": false, "count": 3.0}
	for name, w := range want {
		if got, ok := Get(name); !ok || got != w {
			t.Errorf("%s = %#v (%v), want %#v", name, got, ok, w)
		}
	}
	if Changed {
		t.Error("loading marked the store changed; it would be written straight back")
	}
}

// Writing the value a setting already has is not a change: a screen that
// writes every control back on close does not cost a file write.
func TestSettingTheSameValueIsNotAChange(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	_ = Set("hud_corner", "right")
	Changed = false
	_ = Set("hud_corner", "right")
	if Changed {
		t.Error("rewriting an unchanged value marked the store changed")
	}
}

func TestASettingThatIsNotAValueIsRefused(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	if err := Set("x", []int{1}); err == nil {
		t.Error("a list was accepted as a setting")
	}
	if err := Load([]byte("ok: 1\nbad: [1, 2]\n")); err == nil {
		t.Error("a list in the file was loaded without complaint")
	}
	if v, ok := Get("ok"); !ok || v != 1.0 {
		t.Errorf("the good entry beside a bad one was lost: %#v", v)
	}
}
