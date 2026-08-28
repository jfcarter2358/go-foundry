package ui

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestThemeRoundTrips(t *testing.T) {
	uiTempDir(t)

	want := DefaultTheme()
	want.Font = "inter"
	want.TextSize = 18
	want.TextColor = mgl32.Vec4{0.1, 0.2, 0.3, 1}
	want.PanelTex = "ui_frame"
	want.PanelSlice = [4]float32{8, 8, 8, 8}
	want.Accent = mgl32.Vec4{1, 0.4, 0, 1}

	if err := SaveTheme(want, ThemePath()); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadTheme(ThemePath())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got.Path, want.Path = "", ""
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip lost something.\n got: %+v\nwant: %+v", got, want)
	}
}

// TestThemeWritesOnlyDecisions keeps a theme file a list of what the project
// chose, rather than a dump of every colour in the engine.
func TestThemeWritesOnlyDecisions(t *testing.T) {
	uiTempDir(t)

	th := DefaultTheme()
	th.Accent = mgl32.Vec4{1, 0, 0, 1}
	if err := SaveTheme(th, ThemePath()); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, _ := os.ReadFile(ThemePath())
	body := string(data)

	if !strings.Contains(body, "accent:") {
		t.Errorf("the one changed colour was not written:\n%s", body)
	}
	for _, unwanted := range []string{"button_color:", "bar_fill:", "text_size:", "panel_color:"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("an unchanged default wrote %q:\n%s", unwanted, body)
		}
	}
}

func TestPartialThemeFillsFromTheDefault(t *testing.T) {
	// A hand-written theme naming one colour must not leave the rest black,
	// which is what a plain Vec4 zero would give and which reads as a rendering
	// failure rather than as a partial file.
	uiTempDir(t)
	if err := os.WriteFile(ThemePath(), []byte("accent: {r: 1, g: 0, b: 0, a: 1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadTheme(ThemePath())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	def := DefaultTheme()
	if got.Accent != (mgl32.Vec4{1, 0, 0, 1}) {
		t.Errorf("accent is %v", got.Accent)
	}
	if got.TextSize != def.TextSize || got.ButtonColor != def.ButtonColor {
		t.Error("an unmentioned field did not fall back to the compiled-in default")
	}
}

func TestLoadActiveThemeSurvivesAMissingFile(t *testing.T) {
	// A project that has not customised anything is not an error.
	_, logs := uiTempDir(t)
	prev := ActiveTheme
	t.Cleanup(func() { ActiveTheme = prev })

	ActiveTheme = DefaultTheme()
	LoadActiveTheme()
	if ActiveTheme == nil {
		t.Fatal("a missing theme file cleared the active theme")
	}
	if len(*logs) != 0 {
		t.Errorf("a missing theme file complained: %v", *logs)
	}
}

func TestLoadActiveThemeReportsABrokenFile(t *testing.T) {
	_, logs := uiTempDir(t)
	prev := ActiveTheme
	t.Cleanup(func() { ActiveTheme = prev })

	if err := os.WriteFile(ThemePath(), []byte("accent: [not, a, colour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	LoadActiveTheme()
	if len(*logs) == 0 {
		t.Error("a malformed theme file was swallowed silently")
	}
	if ActiveTheme == nil {
		t.Error("a malformed theme cleared the active one instead of keeping the last good")
	}
}

func TestFontForFallsBackToTheTheme(t *testing.T) {
	th := DefaultTheme()
	th.Font = "inter"
	th.TextSize = 20

	name, size := th.FontFor(TextSpec{})
	if name != "inter" || size != 20 {
		t.Errorf("an unset spec gave %q/%v, want the theme's", name, size)
	}
	name, size = th.FontFor(TextSpec{Font: "mono", Size: 32})
	if name != "mono" || size != 32 {
		t.Errorf("an explicit spec was overridden: %q/%v", name, size)
	}

	// A theme that names no size must still produce a usable one, or every
	// label collapses to nothing.
	bare := &Theme{}
	if _, size := bare.FontFor(TextSpec{}); size <= 0 {
		t.Errorf("an empty theme gave size %v", size)
	}
}

func TestStyleForGivesAButtonItsStates(t *testing.T) {
	th := DefaultTheme()
	s := th.StyleFor(KindButton)
	if s.Hover == nil || s.Press == nil || s.Disabled == nil {
		t.Error("a new button was created with no state styles")
	}
	if s.Color != th.ButtonColor {
		t.Errorf("a new button is %v, want the theme's %v", s.Color, th.ButtonColor)
	}
	// A plain panel needs none of that.
	if p := th.StyleFor(KindPanel); p.Hover != nil {
		t.Error("a panel was given a hover state it can never enter")
	}
}

func TestThemeCloneIsIndependent(t *testing.T) {
	a := DefaultTheme()
	b := a.Clone()
	b.Accent = mgl32.Vec4{9, 9, 9, 9}
	if a.Accent == b.Accent {
		t.Error("clone shared state")
	}
}
