package fileutil

// fileutil.go — generic file-system helpers with no level/texture-specific
// logic, shared across the editor and texture-editor code.

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ExecutablePath is the directory containing the game executable. Asset paths
// are resolved against it rather than against the working directory, which is
// whatever the launcher happened to set.
var ExecutablePath string

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

// ResolveAssetPath makes a stored asset path absolute against the executable
// directory.
//
// Entity definitions store model paths relative to the install root so a
// definition stays portable between machines and platforms (RelAssetPath strips
// the prefix on save). Handing that relative path straight to os.Open would
// instead resolve it against the working directory, which only happens to be
// the install root when the game is launched from inside it — "make run-linux"
// runs ./dist/foundry from the repo root, and every entity model would fail to
// load.
//
// An absolute path is left alone: it is either already resolved or points
// somewhere outside the install root on purpose.
func ResolveAssetPath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(ExecutablePath, p)
}

// RelAssetPath is ResolveAssetPath's inverse, used when writing a path back to
// YAML. A path that does not live under the executable directory is written as
// it is — there is nothing shorter that would still find it — and the result is
// slash-separated so a definition saved on Windows still loads on Linux.
func RelAssetPath(p string) string {
	if p == "" || ExecutablePath == "" {
		return p
	}
	rel, err := filepath.Rel(ExecutablePath, p)
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
	return filepath.Join(ExecutablePath, ScriptsDirName)
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

// ---------- texture paths ----------
//
// Where textures live on disk. Here rather than in texture_file.go because the
// GPU uploader needs the directory too, and it has no business depending on
// the .ftex reader.

const TexturesDirName = "textures"

func TexturesDir() string { return filepath.Join(ExecutablePath, TexturesDirName) }
