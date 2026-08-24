package main

// fileutil.go — generic file-system helpers with no level/texture-specific
// logic, shared across the editor and texture-editor code.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// writeFileAtomic writes data to path via a temporary file in the same
// directory followed by a rename, so an interrupted or failing write can never
// leave a truncated file behind. The previous contents are retained as
// path+".bak".
func writeFileAtomic(path string, data []byte) error {
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

// scanFilesByExt returns the sorted filenames in dir whose name ends in ext
// (e.g. ".yaml"), skipping subdirectories. Returns nil if dir can't be read.
func scanFilesByExt(dir, ext string) []string {
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

// resolveAssetPath makes a stored asset path absolute against the executable
// directory.
//
// Entity definitions store model paths relative to the install root so a
// definition stays portable between machines and platforms (relAssetPath strips
// the prefix on save). Handing that relative path straight to os.Open would
// instead resolve it against the working directory, which only happens to be
// the install root when the game is launched from inside it — "make run-linux"
// runs ./dist/foundry from the repo root, and every entity model would fail to
// load.
//
// An absolute path is left alone: it is either already resolved or points
// somewhere outside the install root on purpose.
func resolveAssetPath(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(executablePath, p)
}

// relAssetPath is resolveAssetPath's inverse, used when writing a path back to
// YAML. A path that does not live under the executable directory is written as
// it is — there is nothing shorter that would still find it — and the result is
// slash-separated so a definition saved on Windows still loads on Linux.
func relAssetPath(p string) string {
	if p == "" || executablePath == "" {
		return p
	}
	rel, err := filepath.Rel(executablePath, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.ToSlash(rel)
}
