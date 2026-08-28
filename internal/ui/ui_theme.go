package ui

// ui_theme.go — the palette and default font every screen falls back to.
//
// One theme file for the whole project rather than one per screen. A screen
// does not pick a theme in v1: there is one, everything uses it, and adding a
// picker later is purely additive. Two menus that disagree about what a button
// looks like is a bug that a per-screen theme would let an author create by
// accident.
//
// What the theme actually decides is narrower than "every unset colour", and
// deliberately so:
//
//   - At draw time it supplies the font name and size for any TextSpec that
//     left them blank. Those two are genuinely absent-able, because "" and 0
//     are not usable values.
//   - In the editor it is the palette a newly created widget is built from, via
//     StyleFor. A colour is a plain Vec4 on a Node, so once a widget exists
//     there is no "unset" left to fall back from — white is a real tint, not a
//     missing one. Making every colour a pointer purely to keep that
//     distinction alive would cost more in the format than it is worth.

import (
	"fmt"
	"os"
	"path/filepath"

	"foundry/internal/fileutil"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// ThemeExt is the theme document's extension.
const ThemeExt = ".fth"

// ThemeFileName is the one theme every screen reads, under resources/ui.
const ThemeFileName = "theme"

// Theme is the project palette.
type Theme struct {
	Font      string
	TextSize  float32
	TextColor mgl32.Vec4
	TextMuted mgl32.Vec4

	PanelColor mgl32.Vec4
	PanelTex   string
	PanelSlice [4]float32

	ButtonColor    mgl32.Vec4
	ButtonHover    mgl32.Vec4
	ButtonPress    mgl32.Vec4
	ButtonDisabled mgl32.Vec4

	BarBack mgl32.Vec4
	BarFill mgl32.Vec4

	Accent mgl32.Vec4

	Path  string
	dirty bool
	rev   uint64
}

// DefaultTheme is compiled in so that a fresh checkout with no theme file still
// renders something legible rather than a screen of white rectangles.
func DefaultTheme() *Theme {
	return &Theme{
		TextSize:  22,
		TextColor: mgl32.Vec4{0.92, 0.93, 0.95, 1},
		TextMuted: mgl32.Vec4{0.58, 0.61, 0.66, 1},

		PanelColor: mgl32.Vec4{0.10, 0.11, 0.13, 0.92},

		ButtonColor:    mgl32.Vec4{0.18, 0.20, 0.24, 1},
		ButtonHover:    mgl32.Vec4{0.25, 0.29, 0.35, 1},
		ButtonPress:    mgl32.Vec4{0.13, 0.15, 0.18, 1},
		ButtonDisabled: mgl32.Vec4{0.15, 0.16, 0.18, 0.6},

		BarBack: mgl32.Vec4{0.08, 0.09, 0.11, 0.9},
		BarFill: mgl32.Vec4{0.30, 0.62, 0.85, 1},

		Accent: mgl32.Vec4{0.30, 0.62, 0.85, 1},
	}
}

// ActiveTheme is what the running game and the editor's preview both read.
// Replaced wholesale rather than mutated in place, so a half-loaded theme is
// never briefly visible.
var ActiveTheme = DefaultTheme()

// Clone is a deep copy, for the editor's undo.
func (t *Theme) Clone() *Theme { out := *t; return &out }

// Dirty reports whether the theme has unsaved changes.
func (t *Theme) Dirty() bool { return t.dirty }

// Invalidate marks it changed.
func (t *Theme) Invalidate() { t.dirty = true; t.rev++ }

// Clean clears the dirty flag.
func (t *Theme) Clean() { t.dirty = false }

// Rev is an edit counter, for caches keyed on "has this changed".
func (t *Theme) Rev() uint64 { return t.rev }

// FontFor resolves a text spec's font and size against the theme, which is the
// only place a blank font name or a zero size is given a meaning.
func (t *Theme) FontFor(spec TextSpec) (name string, size float32) {
	name, size = spec.Font, spec.Size
	if name == "" {
		name = t.Font
	}
	if size <= 0 {
		size = t.TextSize
	}
	if size <= 0 {
		size = 16
	}
	return name, size
}

// StyleFor is the look a newly created widget of a kind starts with. The editor
// calls it when adding a node; nothing at runtime does, because by then the
// widget carries its own colours.
func (t *Theme) StyleFor(k Kind) Style {
	s := DefaultStyleFor(k)
	switch k {
	case KindPanel:
		s.Color = t.PanelColor
		s.Texture = t.PanelTex
		s.Slice = t.PanelSlice
	case KindButton:
		s.Color = t.ButtonColor
		s.Texture = t.PanelTex
		s.Slice = t.PanelSlice
		hover, press, disabled := t.ButtonHover, t.ButtonPress, t.ButtonDisabled
		s.Hover = &StateStyle{Color: &hover}
		s.Press = &StateStyle{Color: &press}
		s.Disabled = &StateStyle{Color: &disabled}
	case KindBar, KindSlider:
		s.Color = t.BarBack
	case KindToggle:
		s.Color = t.ButtonColor
	case KindInput:
		s.Color = t.BarBack
	}
	return s
}

// FillColorFor is the colour a bar or slider's filled portion is drawn in.
func (t *Theme) FillColorFor(k Kind) mgl32.Vec4 {
	if k == KindToggle {
		return t.Accent
	}
	return t.BarFill
}

// ---------- the on-disk shape ----------

// RawTheme is the theme's YAML form. Every field is a pointer so that an
// unwritten one takes the compiled-in default rather than black — which is what
// a plain Vec4 zero would give, and which would make a partial theme file look
// like a rendering failure.
type RawTheme struct {
	Font      string     `yaml:"font,omitempty"`
	TextSize  *float32   `yaml:"text_size,omitempty"`
	TextColor *RawColor4 `yaml:"text_color,omitempty"`
	TextMuted *RawColor4 `yaml:"text_muted,omitempty"`

	PanelColor *RawColor4 `yaml:"panel_color,omitempty"`
	PanelTex   string     `yaml:"panel_texture,omitempty"`
	PanelSlice []float32  `yaml:"panel_slice,omitempty"`

	ButtonColor    *RawColor4 `yaml:"button_color,omitempty"`
	ButtonHover    *RawColor4 `yaml:"button_hover,omitempty"`
	ButtonPress    *RawColor4 `yaml:"button_press,omitempty"`
	ButtonDisabled *RawColor4 `yaml:"button_disabled,omitempty"`

	BarBack *RawColor4 `yaml:"bar_back,omitempty"`
	BarFill *RawColor4 `yaml:"bar_fill,omitempty"`

	Accent *RawColor4 `yaml:"accent,omitempty"`
}

// ThemePath is where the one theme file lives.
func ThemePath() string { return filepath.Join(UIDir(), ThemeFileName+ThemeExt) }

// LoadTheme reads a theme, filling anything it does not mention from the
// compiled-in default.
func LoadTheme(path string) (*Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw RawTheme
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if n := len(raw.PanelSlice); n != 0 && n != 4 {
		return nil, fmt.Errorf("%s: panel_slice has %d insets, want 4", filepath.Base(path), n)
	}

	t := DefaultTheme()
	if raw.Font != "" {
		t.Font = raw.Font
	}
	t.TextSize = f32Default(raw.TextSize, t.TextSize)
	t.TextColor = color4Default(raw.TextColor, t.TextColor)
	t.TextMuted = color4Default(raw.TextMuted, t.TextMuted)
	t.PanelColor = color4Default(raw.PanelColor, t.PanelColor)
	t.PanelTex = raw.PanelTex
	if len(raw.PanelSlice) == 4 {
		t.PanelSlice = quadDefault(raw.PanelSlice)
	}
	t.ButtonColor = color4Default(raw.ButtonColor, t.ButtonColor)
	t.ButtonHover = color4Default(raw.ButtonHover, t.ButtonHover)
	t.ButtonPress = color4Default(raw.ButtonPress, t.ButtonPress)
	t.ButtonDisabled = color4Default(raw.ButtonDisabled, t.ButtonDisabled)
	t.BarBack = color4Default(raw.BarBack, t.BarBack)
	t.BarFill = color4Default(raw.BarFill, t.BarFill)
	t.Accent = color4Default(raw.Accent, t.Accent)
	t.Path = path
	return t, nil
}

// SaveTheme writes only what differs from the compiled-in default, so a theme
// file is a list of decisions rather than a dump of every colour in the engine.
func SaveTheme(t *Theme, path string) error {
	def := DefaultTheme()
	raw := RawTheme{
		Font:           t.Font,
		TextSize:       f32PtrIfNot(t.TextSize, def.TextSize),
		TextColor:      color4PtrIfNot(t.TextColor, def.TextColor),
		TextMuted:      color4PtrIfNot(t.TextMuted, def.TextMuted),
		PanelColor:     color4PtrIfNot(t.PanelColor, def.PanelColor),
		PanelTex:       t.PanelTex,
		PanelSlice:     quadIfAny(t.PanelSlice),
		ButtonColor:    color4PtrIfNot(t.ButtonColor, def.ButtonColor),
		ButtonHover:    color4PtrIfNot(t.ButtonHover, def.ButtonHover),
		ButtonPress:    color4PtrIfNot(t.ButtonPress, def.ButtonPress),
		ButtonDisabled: color4PtrIfNot(t.ButtonDisabled, def.ButtonDisabled),
		BarBack:        color4PtrIfNot(t.BarBack, def.BarBack),
		BarFill:        color4PtrIfNot(t.BarFill, def.BarFill),
		Accent:         color4PtrIfNot(t.Accent, def.Accent),
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(path, data); err != nil {
		return err
	}
	t.Path = path
	t.Clean()
	return nil
}

// LoadActiveTheme reads the project theme if there is one, leaving the
// compiled-in default in place if there is not. A missing theme file is not an
// error: it is a project that has not customised anything yet.
func LoadActiveTheme() {
	t, err := LoadTheme(ThemePath())
	if err != nil {
		if !os.IsNotExist(err) {
			logf("ui: theme: %v", err)
		}
		return
	}
	ActiveTheme = t
}
