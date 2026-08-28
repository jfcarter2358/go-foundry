package editor

// editor_ui_theme.go — the Theme tab.
//
// A tab beside Properties rather than a window of its own: the theme is one
// small file that every screen reads, and making it a second tool would mean a
// second place to remember to save. It has its own dirty flag and its own Save,
// because it is a different document from the screen being edited.

import (
	"foundry/internal/ui"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// uiThemeDraft is the copy being edited. Separate from the file on disk so an
// unsaved experiment is visible in the preview — which is the point — without
// being what a fresh load would produce.
var uiThemeDraft *ui.Theme

func uiEnsureTheme() {
	if uiThemeDraft == nil {
		uiThemeDraft = ui.ActiveTheme.Clone()
	}
}

// uiThemeTab draws the theme editor.
func uiThemeTab() {
	uiEnsureTheme()
	t := uiThemeDraft

	editorTextMutedWrapped(
		"One theme for the project. It supplies the font and size for any text that leaves them blank, and the palette a newly created widget is built from.")

	editorPropGroupOpen("Text", func() {
		if uiFontCombo("Font##uithemefont", &t.Font) {
			uiThemeChanged()
		}
		editorTextMuted("A .ttf in resources/fonts. Blank uses the built-in bitmap face.")
		if editorDragFloat("Default size", &t.TextSize, 8, 96, "%.0f") {
			uiThemeChanged()
		}
		uiThemeColor("Text##uithemetext", &t.TextColor)
		uiThemeColor("Muted text##uithememuted", &t.TextMuted)
	})

	editorPropGroupOpen("Panels", func() {
		uiThemeColor("Panel##uithemepanel", &t.PanelColor)
		if textureCombo("Panel texture##uithemepaneltex", &t.PanelTex, true) {
			uiThemeChanged()
		}
		if t.PanelTex != "" {
			for i, name := range []string{"Slice left", "Slice top", "Slice right", "Slice bottom"} {
				if editorDragFloat(name+"##uitheme", &t.PanelSlice[i], 0, 512, "%.0f") {
					uiThemeChanged()
				}
			}
		}
	})

	editorPropGroupOpen("Buttons", func() {
		uiThemeColor("Normal##uithemebtn", &t.ButtonColor)
		uiThemeColor("Hover##uithemebtnh", &t.ButtonHover)
		uiThemeColor("Press##uithemebtnp", &t.ButtonPress)
		uiThemeColor("Disabled##uithemebtnd", &t.ButtonDisabled)
	})

	editorPropGroupOpen("Bars", func() {
		uiThemeColor("Background##uithemebarb", &t.BarBack)
		uiThemeColor("Fill##uithemebarf", &t.BarFill)
		uiThemeColor("Accent##uithemeaccent", &t.Accent)
		editorTextMuted("Accent is the focus ring, the slider handle and a toggle's mark.")
	})

	imgui.Separator()
	if imgui.Button("Save theme##uithemesave") {
		if err := ui.SaveTheme(t, ui.ThemePath()); err != nil {
			uiStatus = "Theme save failed: " + err.Error()
		} else {
			uiStatus = "Saved the theme"
		}
	}
	imgui.SameLine()
	if imgui.Button("Revert##uithemerevert") {
		ui.LoadActiveTheme()
		uiThemeDraft = ui.ActiveTheme.Clone()
		uiStatus = "Reverted the theme"
	}
	imgui.SameLine()
	if imgui.Button("Defaults##uithemedefaults") {
		uiThemeDraft = ui.DefaultTheme()
		uiThemeChanged()
	}
	if t.Dirty() {
		imgui.SameLine()
		editorTextMuted("unsaved")
	}
}

// uiThemeColor is one swatch.
func uiThemeColor(label string, c *mgl32.Vec4) {
	col := [4]float32{(*c)[0], (*c)[1], (*c)[2], (*c)[3]}
	if imgui.ColorEdit4V(label, &col, imgui.ColorEditFlagsAlphaBar) {
		*c = mgl32.Vec4{col[0], col[1], col[2], col[3]}
		uiThemeChanged()
	}
}

// uiThemeChanged makes the edit visible in the preview immediately.
//
// The draft becomes the active theme as it is edited, which is the only way to
// see what a colour actually does to a real screen. Revert puts the file's
// version back.
func uiThemeChanged() {
	uiThemeDraft.Invalidate()
	ui.ActiveTheme = uiThemeDraft
}
