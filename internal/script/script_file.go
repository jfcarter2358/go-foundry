package script

// script_file.go — reading .flang files, and resolving imports safely.
//
// The one rule worth stating: an import may not escape the scripts root. A
// script tree that can read arbitrary files is a script tree that becomes
// dangerous the moment someone hands you a level, and the check costs one
// filepath.Rel.

import (
	"fmt"
	"foundry/internal/fileutil"
	"os"
	"path/filepath"
	"strings"
)

// readScriptFile reads a .flang source file.
func ReadScriptFile(path string) ([]byte, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", fileutil.RelAssetPath(path), err)
	}
	return src, nil
}

// resolveScriptImport turns an `import("…")` path into an absolute one, relative
// to the importing file and rooted at the scripts folder.
func ResolveScriptImport(fromFile, importPath string) (string, error) {
	if importPath == "" {
		return "", fmt.Errorf("import needs a path")
	}
	if !strings.HasSuffix(importPath, fileutil.ScriptExt) {
		importPath += fileutil.ScriptExt
	}
	dir := filepath.Dir(fromFile)
	resolved := filepath.Clean(filepath.Join(dir, filepath.FromSlash(importPath)))

	root := fileutil.ScriptsDir()
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("import %q leaves the scripts folder", importPath)
	}
	return resolved, nil
}
