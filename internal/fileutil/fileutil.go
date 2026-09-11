package fileutil

// go — generic file-system helpers with no level/texture-specific
// logic, shared across the editor and texture-editor code.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ExecutablePath is the directory containing the game executable. Only what
// belongs to the binary rather than to the game is found here — runtimes/, and a
// development build's log, window layout and release settings. Content is under
// ResourcesPath.
var ExecutablePath string

// ResourcesDirName is the folder, beside the executable, that every asset the
// engine reads lives under: levels/, textures/, scripts/, game.yaml and the rest.
const ResourcesDirName = "resources"

// ResourcesPath is the content root: the resources/ folder beside the
// executable, set at startup. Asset paths are resolved against it rather than
// against the working directory, which is whatever the launcher happened to set.
//
// A variable of its own rather than derived from ExecutablePath on every call,
// so that a tool reading a project it is not running from — the level compiler,
// the release builder — and a test reading the repository's resources/ can
// point content somewhere without also moving where the binary's own files go.
var ResourcesPath string

// ResourcesDir is the content root of the project at root: the resources/
// folder in the directory a project's executable runs from.
func ResourcesDir(root string) string { return filepath.Join(root, ResourcesDirName) }

// WriteFileAtomic writes data to path via a temporary file in the same
// directory followed by a rename, so an interrupted or failing write can never
// leave a truncated file behind. The previous contents are retained as
// path+".bak".
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}

	// Keep one generation of backup so a bad edit is still recoverable.
	if prev, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", prev, 0644); err != nil {
			return err
		}
	}

	return os.Rename(tmpName, path)
}

// ScanFilesByExt returns the sorted filenames in dir whose name ends in ext
// (e.g. ".yaml"), skipping subdirectories. Returns nil if dir can't be read.
func ScanFilesByExt(dir, ext string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	return files
}

// ResolveAssetPath makes a stored asset path absolute against the resources
// directory.
//
// Entity definitions store model paths relative to resources/ so a definition
// stays portable between machines and platforms (RelAssetPath strips the prefix
// on save). Handing that relative path straight to os.Open would instead
// resolve it against the working directory, which is not resources/ however the
// game is launched — "make run-linux" runs ./dist/foundry from the repo root,
// and every entity model would fail to load.
//
// An absolute path is left alone: it is either already resolved or points
// somewhere outside resources/ on purpose.
func ResolveAssetPath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(ResourcesPath, p)
}

// RelAssetPath is ResolveAssetPath's inverse, used when writing a path back to
// YAML. A path that does not live under the resources directory is written as
// it is — there is nothing shorter that would still find it — and the result is
// slash-separated so a definition saved on Windows still loads on Linux.
func RelAssetPath(p string) string {
	if p == "" || ResourcesPath == "" {
		return p
	}
	rel, err := filepath.Rel(ResourcesPath, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.ToSlash(rel)
}

// ScriptsDirName is the folder .flang scripts live in, alongside textures/,
// models/ and entities/.
const ScriptsDirName = "scripts"

// ScriptExt is the extension a flang source file carries.
const ScriptExt = ".flang"

// ScriptsDir returns the absolute path to the scripts folder.
func ScriptsDir() string {
	return filepath.Join(ResourcesPath, ScriptsDirName)
}

// ScriptPath resolves a script name — as written in a level or an entity
// definition, with or without its extension — to an absolute path.
func ScriptPath(name string) string {
	if name == "" {
		return ""
	}
	if !strings.HasSuffix(name, ScriptExt) {
		name += ScriptExt
	}
	// A name may already be rooted at the scripts folder ("scripts/door.flang"),
	// which is how the docs and every example write it.
	name = strings.TrimPrefix(filepath.ToSlash(name), ScriptsDirName+"/")
	return filepath.Join(ScriptsDir(), filepath.FromSlash(name))
}

// ScanScriptFiles lists every .flang file under the scripts folder, including
// ones in subdirectories, as slash-separated paths relative to that folder.
//
// Recursive where the other Scan* helpers are flat, because scripts are the one
// asset kind the language explicitly expects to be organised into folders — see
// SCRIPTING.md on import().
func ScanScriptFiles() []string {
	return ScanScriptFilesIn(ScriptsDir())
}

// ScanScriptFilesIn is ScanScriptFiles rooted at an explicit directory, for
// tests and tooling that read the source tree rather than an installed build.
func ScanScriptFilesIn(root string) []string {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(path), ScriptExt) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil
	}
	sort.Strings(out)
	return out
}

// ScanScriptDirs lists every folder under the scripts folder, as
// slash-separated paths relative to it, deepest last.
//
// Separate from ScanScriptFiles because the script editor's tree has to be able
// to show a folder that is still empty — a folder made a moment ago, to put the
// next script in, is exactly the one that has nothing in it yet, and a tree
// built only from file paths would make it disappear the instant it was
// created.
func ScanScriptDirs() []string { return ScanScriptDirsIn(ScriptsDir()) }

// ScanScriptDirsIn is ScanScriptDirs rooted at an explicit directory.
func ScanScriptDirsIn(root string) []string {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || rel == "." {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil
	}
	sort.Strings(out)
	return out
}

// MakeScriptDir creates a folder under the scripts folder. rel is
// slash-separated and relative to it; intermediate folders are created too.
func MakeScriptDir(rel string) error {
	full, ok := scriptSubPath(rel)
	if !ok {
		return fmt.Errorf("%q is not a name inside the scripts folder", rel)
	}
	return os.MkdirAll(full, 0755)
}

// MoveScriptFile renames or moves a script within the scripts folder. Both
// paths are relative to it, with or without the extension and the scripts/
// prefix. It refuses to overwrite an existing file — organising scripts should
// never be able to lose one.
func MoveScriptFile(from, to string) error {
	src, dst := ScriptPath(from), ScriptPath(to)
	if src == "" || dst == "" {
		return fmt.Errorf("a script move needs both a source and a destination")
	}
	if src == dst {
		return nil
	}
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%s already exists", to)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// DeleteScriptFile removes a script from the scripts folder. rel is relative to
// it, with or without the extension and the scripts/ prefix.
//
// Only ever a file, and only ever a `.flang` one. The rel that reaches here has
// come from a row in the sidebar, so it is a script by construction — but this
// is the one operation in the file that cannot be taken back, and "what the
// caller passed was a folder" is not a thing worth finding out by recursing
// into it.
func DeleteScriptFile(rel string) error {
	full := ScriptPath(rel)
	if full == "" {
		return fmt.Errorf("%q is not a name inside the scripts folder", rel)
	}
	if _, ok := scriptSubPath(rel); !ok {
		return fmt.Errorf("%q is not a name inside the scripts folder", rel)
	}
	info, err := os.Stat(full)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a folder, not a script", rel)
	}
	return os.Remove(full)
}

// DeleteScriptDir removes a folder under the scripts folder and everything in
// it. rel is slash-separated and relative to it, with or without the scripts/
// prefix.
//
// Kept apart from DeleteScriptFile rather than folded into it, so that a
// recursive delete is only ever something a caller asked for by name. It
// refuses the scripts folder itself — scriptSubPath has no name for it — and
// anything that is not a folder: a rel that turned out to be a script is a
// caller that meant DeleteScriptFile, and finding that out is worth an error.
func DeleteScriptDir(rel string) error {
	full, ok := scriptSubPath(rel)
	if !ok {
		return fmt.Errorf("%q is not a folder inside the scripts folder", rel)
	}
	info, err := os.Stat(full)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is a script, not a folder", rel)
	}
	return os.RemoveAll(full)
}

// scriptSubPath resolves a slash-separated name to an absolute path under the
// scripts folder, reporting false for anything that would escape it. `..` in a
// folder name is a typo rather than an attack here, but a typo that creates a
// directory beside scripts/ is still worth not doing.
func scriptSubPath(rel string) (string, bool) {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), ScriptsDirName+"/")
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return "", false
	}
	root := ScriptsDir()
	full := filepath.Join(root, filepath.FromSlash(rel))
	inside, err := filepath.Rel(root, full)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", false
	}
	return full, true
}

// ---------- texture paths ----------
//
// Where textures live on disk. Here rather than in texture_file.go because the
// GPU uploader needs the directory too, and it has no business depending on
// the .ftex reader.

const TexturesDirName = "textures"

func TexturesDir() string { return filepath.Join(ResourcesPath, TexturesDirName) }

// NormalMapSuffix is appended to a texture's logical name to get its normal
// map's: brick -> brick_normal, sampled from textures/brick_normal.png.
//
// The convention lives here, with the rest of the texture path rules, rather
// than in gfx with the code that uses it: the release packer has to know it
// too (a normal map is never named by any content file, so it would otherwise
// be pruned as unreachable), and a packaging tool has no business importing an
// OpenGL binding to learn a string.
const NormalMapSuffix = "_normal"

// NormalMapName is the logical texture name a texture's normal map lives
// under.
func NormalMapName(name string) string { return name + NormalMapSuffix }

// IsNormalMapName reports whether name is a normal map's rather than an
// albedo's.
func IsNormalMapName(name string) bool { return strings.HasSuffix(name, NormalMapSuffix) }
