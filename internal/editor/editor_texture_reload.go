package editor

// editor_texture_reload.go — File > Reload Textures.
//
// The editor scans textures/ once, when it starts. Anything written into that
// directory afterwards by something other than the texture editor — a generator
// script, a branch switched in the Git window, Claude asked for a new wall
// texture — was invisible until the editor was restarted: the picker never
// offered the new name, and a texture that was overwritten kept drawing the
// image it had at startup.
//
// internal/texture does the work; what lives here is the way to ask for it and
// the answer, and the rule about where it is asked from. Every entry point goes
// through uiwin.RequestWindowOpen, so the reload runs on the frame loop with
// the main GL context current rather than inside whichever pop-out editor's
// window the menu was in — the same rule Save All follows, for the same reason.

import (
	"log"

	"foundry/internal/texture"
	"foundry/internal/textureui"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// EditorReloadTextures re-reads textures/ and says what it found. Exported
// because it is also the whole of the Claude panel's texture reload — see
// claudeApplyFileChange.
//
// Must run on the frame loop: call it through uiwin.RequestWindowOpen from
// anything drawing a window.
func EditorReloadTextures() {
	r := texture.ReloadTextures()
	msg := "Textures are up to date."
	if r.Changed() {
		msg = "Reloaded textures: " + r.Summary() + "."
	}
	for _, err := range r.Errs {
		log.Printf("WARNING: texture reload: %v", err)
	}
	editorSetStatus("%s", msg)
	// The reload is offered in the texture editor's menus too, and that window
	// has its own status line — the one the person who asked is looking at.
	if textureui.TextureEditorOpen() {
		textureui.TextureEditorStatus(msg)
	}
}

// editorReloadTexturesMenuItem is the File-menu entry, shared by the editors
// that offer it.
func editorReloadTexturesMenuItem() {
	if imgui.MenuItem("Reload Textures") {
		uiwin.RequestWindowOpen(EditorReloadTextures)
	}
	editorMenuTooltip("Re-read textures/ from disk: pick up textures added or changed\n" +
		"outside the editor, and update the ones already in the level.")
}
