package editor

// editor_folder_browser.go — choosing a folder without typing its path.
//
// SDL2 has no native directory dialog and the one Windows offers lives behind
// COM, so the editor draws its own: a path strip, the folders inside it, and a
// button that takes the one you are standing in. It is deliberately a *folder*
// browser — nothing here lists files, because every caller so far is asking
// "which directory", not "which file".
//
// Navigation is one click deep: clicking a folder walks into it, and the button
// underneath chooses wherever you have ended up. A folder that already holds a
// project is tagged, so Open Project can be done by eye rather than by memory.

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"foundry/internal/project"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// folderBrowser is a directory chooser drawn inside another window. The zero
// value is a closed browser; open it with start.
type folderBrowser struct {
	// open is whether draw puts anything on the screen at all.
	open bool
	// dir is the folder being listed.
	dir string
	// scanned is the folder entries was read for, so draw rescans only when
	// the two have drifted apart.
	scanned string
	entries []folderEntry
	err     string
}

// folderEntry is one folder in the listing.
type folderEntry struct {
	name string
	path string
	// isProject marks a folder that already holds resources/game.yaml, which
	// is the one thing Open Project cares about.
	isProject bool
}

// start opens the browser on dir, or on the nearest ancestor of it that
// exists — a path half-typed into the field above still lands somewhere
// useful rather than on an error.
func (b *folderBrowser) start(dir string) {
	b.open = true
	b.err = ""
	b.goTo(nearestExistingDir(dir))
}

// goTo lists another folder.
func (b *folderBrowser) goTo(dir string) {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	b.dir = filepath.Clean(dir)
	b.scanned = "" // forces the rescan in draw
}

// scan reads the folders inside b.dir.
func (b *folderBrowser) scan() {
	b.scanned = b.dir
	b.entries = nil
	b.err = ""
	list, err := os.ReadDir(b.dir)
	if err != nil {
		b.err = err.Error()
		return
	}
	for _, e := range list {
		// A symlink to a directory reports itself as a link, not a dir, so
		// anything that is not plainly a file gets a stat to settle it.
		folder := e.IsDir()
		full := filepath.Join(b.dir, e.Name())
		if !folder && e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(full); err == nil {
				folder = info.IsDir()
			}
		}
		if !folder || strings.HasPrefix(e.Name(), ".") {
			continue // .git and friends are never what anyone is looking for
		}
		b.entries = append(b.entries, folderEntry{
			name:      e.Name(),
			path:      full,
			isProject: project.IsProject(full),
		})
	}
	sort.Slice(b.entries, func(i, j int) bool {
		return strings.ToLower(b.entries[i].name) < strings.ToLower(b.entries[j].name)
	})
}

// draw puts the browser on the screen, and reports the folder chosen. id keeps
// the widgets apart from any other browser drawn in the same window.
func (b *folderBrowser) draw(id string, height float32) (chosen string, ok bool) {
	if !b.open {
		return "", false
	}
	if b.scanned != b.dir {
		b.scan()
	}

	// The shortcuts: the places a folder is most likely to be under, and on
	// Windows the drives, which have no common parent to walk up to.
	for i, r := range folderRoots() {
		if i > 0 {
			imgui.SameLine()
		}
		if imgui.Button(r.label + "##" + id + "root" + r.path) {
			b.goTo(r.path)
		}
	}
	imgui.Separator()
	editorTextMuted(b.dir)

	imgui.BeginChildV("##"+id+"list", imgui.Vec2{X: 560, Y: height}, true, 0)
	if parent := filepath.Dir(b.dir); parent != b.dir {
		if imgui.SelectableV(".. (up one folder)", false,
			imgui.SelectableFlagsDontClosePopups, imgui.Vec2{}) {
			b.goTo(parent)
		}
	}
	if b.err != "" {
		editorTextColored(releaseErrorColor, b.err)
	}
	for i, e := range b.entries {
		// Latched before the widget: the pop below has to be decided by the
		// same value the push was (editor_style_stack_test.go).
		tagged := e.isProject
		label := e.name
		if tagged {
			label += "   — project"
			imgui.PushStyleColor(imgui.StyleColorText, releaseDoneColor)
		}
		// DontClosePopups: this browser is drawn inside a modal, and a plain
		// Selectable dismisses the modal it is clicked in (imgui_widgets.cpp,
		// "Automatically close popups"). Walking into a folder must not close
		// the dialog that is asking which folder.
		clicked := imgui.SelectableV(label+"##"+id+"e"+itoa(i), false,
			imgui.SelectableFlagsDontClosePopups, imgui.Vec2{})
		if tagged {
			imgui.PopStyleColor()
		}
		if clicked {
			b.goTo(e.path)
		}
	}
	imgui.EndChild()

	if imgui.Button("Use This Folder##" + id) {
		return b.dir, true
	}
	imgui.SameLine()
	if imgui.Button("Cancel##" + id + "browse") {
		b.open = false
	}
	return "", false
}

// folderRoot is one shortcut button above the listing.
type folderRoot struct {
	label string
	path  string
}

// folderRoots are the places worth one click: the user's home and documents,
// and on Windows every drive there is, since C:\ and D:\ share no parent.
func folderRoots() []folderRoot {
	var roots []folderRoot
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, folderRoot{"Home", home})
		if docs := filepath.Join(home, "Documents"); isDir(docs) {
			roots = append(roots, folderRoot{"Documents", docs})
		}
	}
	if runtime.GOOS == "windows" {
		for c := 'C'; c <= 'Z'; c++ {
			drive := string(c) + `:\`
			if isDir(drive) {
				roots = append(roots, folderRoot{string(c) + ":", drive})
			}
		}
	} else {
		roots = append(roots, folderRoot{"/", "/"})
	}
	return roots
}

// nearestExistingDir is dir, or the closest ancestor of it that exists, or the
// home directory when none of it does.
func nearestExistingDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		for d := filepath.Clean(dir); ; {
			if isDir(d) {
				return d
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return string(filepath.Separator)
}

// isDir reports whether path is a directory that can be stat'd.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
