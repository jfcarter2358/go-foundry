package main

// script_file.go — reading .flang files, and resolving imports safely.
//
// The one rule worth stating: an import may not escape the scripts root. A
// script tree that can read arbitrary files is a script tree that becomes
// dangerous the moment someone hands you a level, and the check costs one
// filepath.Rel.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// readScriptFile reads a .flang source file.
func readScriptFile(path string) ([]byte, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", relAssetPath(path), err)
	}
	return src, nil
}

// resolveScriptImport turns an `import("…")` path into an absolute one, relative
// to the importing file and rooted at the scripts folder.
func resolveScriptImport(fromFile, importPath string) (string, error) {
	if importPath == "" {
		return "", fmt.Errorf("import needs a path")
	}
	if !strings.HasSuffix(importPath, scriptExt) {
		importPath += scriptExt
	}
	dir := filepath.Dir(fromFile)
	resolved := filepath.Clean(filepath.Join(dir, filepath.FromSlash(importPath)))

	root := scriptsDir()
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("import %q leaves the scripts folder", importPath)
	}
	return resolved, nil
}
