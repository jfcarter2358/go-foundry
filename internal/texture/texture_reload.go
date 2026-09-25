package texture

// texture_reload.go — picking up textures written from outside the editor.
//
// The editor scans textures/ once, at startup. Everything that came along
// afterwards used to arrive through the texture editor, which registers and
// uploads what it saves, so nothing else needed a way in. Now things do: a
// generator script run from a terminal, a branch switched in the Git window,
// and — the one that made this necessary — Claude writing a PNG into textures/
// on request. None of those go anywhere near the texture editor, so the new
// file was on disk and invisible to the running editor until it was restarted.
//
// This is the one door for all of them, in two sizes: ReloadTextures for "go
// and look", and ReloadTexture for "this file, which I have just written".
// Both are the same work underneath — the names map, the resident images, the
// animation stacks — and both are safe to call repeatedly, because each half
// compares what is on disk against what was last read and does nothing when
// they agree.
//
// It lives here rather than in gfx because the .ftex half needs this package's
// reader, and gfx cannot import it: the arrow between them already points the
// other way.

import (
	"fmt"
	"os"

	"foundry/internal/gfx"
)

// ReloadTextures re-reads textures/ in full: new files join the pickers,
// changed files are re-uploaded over the GL objects already drawing them,
// deleted files stop being offered, and the animated ones re-register their
// frame stacks.
//
// Materials get a pass too, because a material naming a texture that has only
// just appeared has been drawing black until now, and nothing else would ever
// go back and load it.
func ReloadTextures() gfx.TextureRescan {
	r := gfx.RescanTextures()
	RescanAnimatedTextures()
	gfx.EnsureMaterialTextures()
	return r
}

// ReloadTexture re-reads one texture by name — what a caller who has just
// written textures/<name>.png (or .ftex) wants, without a walk of the whole
// directory behind it.
//
// A name the editor has never heard of is registered on the spot: that is the
// case this exists for, a texture that did not exist when the editor started.
func ReloadTexture(name string) error {
	if name == "" {
		return nil
	}
	pngPath := texPNGPath(name)
	if _, err := os.Stat(pngPath); err == nil {
		if _, known := gfx.TextureNames[name]; !known {
			gfx.RegisterTexture(name)
		}
		if err := gfx.ReloadTexture(name); err != nil {
			return err
		}
	} else if _, known := gfx.TextureNames[name]; known {
		// The PNG has gone. The name stops being offered; the GL object stays,
		// for the same reason RescanTextures leaves it — see the note there.
		delete(gfx.TextureNames, name)
	}

	// The .ftex beside it, when there is one, is what says whether the texture
	// animates. Its flattened image is already on disk as the PNG handled
	// above, so this is only about the frame stack.
	ftex := FtexPath(name)
	if _, err := os.Stat(ftex); err != nil {
		gfx.UnregisterAnimatedTexture(name)
		gfx.UnregisterAnimatedTexture(gfx.NormalMapName(name))
		delete(ftexStamps, name)
		return nil
	}
	d, err := ReadTexDoc(ftex)
	if err != nil {
		return fmt.Errorf("%s.ftex: %w", name, err)
	}
	if st, err := os.Stat(ftex); err == nil {
		ftexStamps[name] = ftexStamp{mod: st.ModTime(), size: st.Size()}
	}
	SyncAnimatedFrames(d, name)
	return nil
}
