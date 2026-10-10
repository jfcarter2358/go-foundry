package gfx

// texture_rescan.go — noticing textures that appeared, changed or went away
// after startup.
//
// ScanTextures runs once, at startup, and every texture picker in the editor
// reads through the map it built. That was enough while the only way to get a
// new texture was the texture editor, which registers what it saves. It is not
// enough once something outside the editor writes into textures/ — a generator
// script, a checkout, Claude — because then the file is on disk and nothing in
// the running editor can see it:
//
//   - a new .png is not in TextureNames, so no picker offers it and every
//     reference to it is an "unknown texture";
//   - a rewritten .png keeps drawing the image that was there when the process
//     started, because a texture resident in Textures is never read twice.
//
// RescanTextures closes both gaps. The names map is rebuilt from the directory,
// and every resident texture whose file has moved on is re-decoded into the GL
// object it already owns — re-specifying the same handle, as the texture editor
// does for a live edit, so everything already drawing with it shows the new
// image on the next frame with no level reload and no geometry rebuild.
//
// Uploads go through the queue rather than straight to GL: a rescan can be
// asked for from a pop-out editor's window, and DrainTextureUploads is where
// this project puts world-GL mutation. A frame's delay is invisible.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"foundry/internal/fileutil"
)

// textureStamp is what was on disk the last time a texture's file was read.
// Comparing against it is what tells a file that has been rewritten from one
// that has merely been looked at again.
type textureStamp struct {
	path string
	mod  time.Time
	size int64
}

// textureStamps is keyed by the name the texture is resident under, which for a
// normal map is NormalMapName(albedo).
var textureStamps = map[string]textureStamp{}

// noteTextureStamp records what file was read for name. Called from every path
// that reads a texture off disk.
func noteTextureStamp(name, file string) {
	if st, err := os.Stat(file); err == nil {
		textureStamps[name] = textureStamp{path: file, mod: st.ModTime(), size: st.Size()}
	}
}

// textureFileChanged reports whether file differs from what name was last read
// from.
//
// A texture with no stamp is resident without ever having been read off disk —
// the texture editor's own uploads, which are ahead of the file rather than
// behind it. Its file is stamped here and not reloaded: re-reading it would
// replace what is on screen with the last thing that was saved, which is the
// one direction this must never go.
func textureFileChanged(name, file string) bool {
	st, err := os.Stat(file)
	if err != nil {
		return false
	}
	prev, ok := textureStamps[name]
	if !ok {
		textureStamps[name] = textureStamp{path: file, mod: st.ModTime(), size: st.Size()}
		return false
	}
	return prev.path != file || !prev.mod.Equal(st.ModTime()) || prev.size != st.Size()
}

// TextureRescan is what one rescan found, in the terms a status line wants:
// names, not counts, because "which" is the question anyone asking for a reload
// actually has.
type TextureRescan struct {
	// Added are names textures/ has that it did not before. They are not
	// uploaded here — EnsureTextureLoaded does that the first time one is
	// actually drawn.
	Added []string
	// Removed are names that have gone from disk. Their GL objects stay
	// resident; see RescanTextures.
	Removed []string
	// Reloaded are the resident textures re-read because their file changed,
	// normal maps included.
	Reloaded []string
	// Filled are names something asked for and went without — a level naming a
	// texture that did not exist yet — and that this rescan has now loaded.
	Filled []string
	// Errs is one error per file that could not be re-read. A bad file is
	// reported and skipped rather than stopping the rest of the rescan.
	Errs []error
}

// Summary is the one line a status message wants. Empty when nothing changed,
// so a caller can tell "nothing to do" from "here is what happened".
func (r TextureRescan) Summary() string {
	var parts []string
	if n := len(r.Added); n > 0 {
		parts = append(parts, fmt.Sprintf("%d new", n))
	}
	if n := len(r.Reloaded); n > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", n))
	}
	if n := len(r.Filled); n > 0 {
		parts = append(parts, fmt.Sprintf("%d no longer missing", n))
	}
	if n := len(r.Removed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d gone", n))
	}
	if n := len(r.Errs); n > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", n))
	}
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += ", " + p
	}
	return out
}

// Changed reports whether the rescan found anything at all.
func (r TextureRescan) Changed() bool {
	return len(r.Added)+len(r.Removed)+len(r.Reloaded)+len(r.Filled)+len(r.Errs) > 0
}

// missingTextures is every name something asked for and could not be given,
// because there was no file of that name on disk. Kept because a rescan is
// exactly the moment that can stop being true: a level that named a texture
// nobody had painted yet has been drawing black walls ever since it opened, and
// once the file appears those walls should stop being black without the level
// being reloaded to find out.
var missingTextures = map[string]bool{}

// NoteMissingTexture records a name that could not be resolved. Called from
// every lookup that can come up empty — EnsureTextureLoaded, and the level
// loader, which resolves through TextureNames itself.
func NoteMissingTexture(name string) {
	if name != "" {
		missingTextures[name] = true
	}
}

// RescanTextures re-reads the textures directory: rebuilds TextureNames, and
// re-uploads every resident texture whose file has changed since it was read.
//
// A texture that has gone from disk is dropped from TextureNames — so no picker
// offers it any more — but its GL object is left resident. Anything still
// drawing with that name keeps drawing the image it had; freeing the handle
// would bind texture 0 instead, which samples black, and a level that still
// names a deleted texture is better off looking unchanged than looking burnt.
func RescanTextures() TextureRescan {
	var r TextureRescan
	if TextureNames == nil {
		TextureNames = map[string]string{}
	}
	dir := fileutil.TexturesDir()
	fresh := ScanTextures(dir)
	for name := range fresh {
		if _, had := TextureNames[name]; !had {
			r.Added = append(r.Added, name)
		}
	}
	for name := range TextureNames {
		if _, still := fresh[name]; !still {
			r.Removed = append(r.Removed, name)
		}
	}
	TextureNames = fresh

	// The names something has already gone without. Loaded outright rather
	// than left for the next draw: nothing asks twice — a level walks its
	// textures once, when it opens — so this is the only chance they get.
	for name := range missingTextures {
		rel, ok := TextureNames[name]
		if !ok {
			continue
		}
		delete(missingTextures, name)
		if _, resident := Textures[name]; resident {
			continue
		}
		if err := LoadTexture(name, filepath.Join(fileutil.ResourcesPath, rel)); err != nil {
			r.Errs = append(r.Errs, err)
			continue
		}
		r.Filled = append(r.Filled, name)
	}

	for name, rel := range TextureNames {
		// Only what is already on the GPU. A texture nothing has drawn yet has
		// no stale copy to correct, and EnsureTextureLoaded will read the file
		// as it stands the first time something does.
		if !textureResident(name) {
			continue
		}
		file := filepath.Join(fileutil.ResourcesPath, rel)
		if textureFileChanged(name, file) {
			if err := reloadTextureFile(name, file); err != nil {
				r.Errs = append(r.Errs, err)
			} else {
				r.Reloaded = append(r.Reloaded, name)
			}
		}
		// The albedo's normal map is not in TextureNames — ScanTextures keeps
		// it out — so it is checked here, beside the texture it belongs to,
		// and picked up whether it changed or has only just appeared.
		nm := normalMapPath(name)
		if _, err := os.Stat(nm); err != nil {
			continue
		}
		// normalMapped rather than HasNormalMap: the upload of one picked up a
		// moment ago may still be in the queue, and re-decoding it every rescan
		// until the queue drains would be work for nothing.
		if normalMapped[name] && !textureFileChanged(NormalMapName(name), nm) {
			continue
		}
		if err := reloadNormalMapFile(name, nm); err != nil {
			r.Errs = append(r.Errs, err)
		} else {
			r.Reloaded = append(r.Reloaded, NormalMapName(name))
		}
	}

	sort.Strings(r.Added)
	sort.Strings(r.Removed)
	sort.Strings(r.Reloaded)
	sort.Strings(r.Filled)
	return r
}

// textureResident reports whether name has a GL object, or is about to: a
// texture queued for upload this frame counts, so two rescans in a row don't
// decode the same file twice.
func textureResident(name string) bool {
	if _, ok := Textures[name]; ok {
		return true
	}
	for _, u := range pendingTextureUploads {
		if u.Name == name {
			return true
		}
	}
	return false
}

// ReloadTexture re-reads one texture from disk into the GL object it already
// owns, and picks up a normal map beside it. Used by the editor's per-file
// reload, which knows the name but not whether anything else has changed.
//
// A name with no file is not an error: it is a texture that lives only in
// memory (a UI atlas page, a document that has never been saved), and there is
// nothing on disk to re-read.
func ReloadTexture(name string) error {
	rel, ok := TextureNames[name]
	if !ok {
		return nil
	}
	if !textureResident(name) && !missingTextures[name] {
		// Nothing has drawn it, so there is no stale image to correct and no
		// reason to spend a decode and a texture object on art that may never
		// be used — a generator run writes hundreds of these. Being in
		// TextureNames is what it needs; EnsureTextureLoaded will read it the
		// first time something actually wants it.
		return nil
	}
	delete(missingTextures, name)
	file := filepath.Join(fileutil.ResourcesPath, rel)
	if err := reloadTextureFile(name, file); err != nil {
		return err
	}
	if nm := normalMapPath(name); fileExists(nm) {
		return reloadNormalMapFile(name, nm)
	}
	return nil
}

// reloadTextureFile decodes file and queues it over whatever name is resident
// as, recording the stamp so the next rescan leaves it alone.
func reloadTextureFile(name, file string) error {
	rgba, err := decodeTextureFile(file)
	if err != nil {
		return fmt.Errorf("texture %q: %w", name, err)
	}
	noteTextureStamp(name, file)
	if !TextureUploadsEnabled {
		// Headless: there is no GPU to put it on, but a caller reading sizes
		// still wants the new one.
		noteTextureSize(name, int32(rgba.Rect.Size().X), int32(rgba.Rect.Size().Y))
		noteTextureMean(name, rgba)
		return nil
	}
	QueueTextureUpload(name, rgba)
	return nil
}

// reloadNormalMapFile does the same for an albedo's normal map, and marks the
// albedo as normal-mapped — which is what makes a bump map that has only just
// appeared on disk light a surface that was flat a moment ago.
func reloadNormalMapFile(name, file string) error {
	rgba, err := decodeTextureFile(file)
	if err != nil {
		return fmt.Errorf("normal map for %q: %w", name, err)
	}
	noteTextureStamp(NormalMapName(name), file)
	if !TextureUploadsEnabled {
		return nil
	}
	SetNormalMap(name, rgba)
	return nil
}
