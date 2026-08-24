package leveltest

// repo.go — finding the repository root from inside a package's tests.
//
// Several tests assert that a .glsl file and the Go that uploads to it still
// agree, which means reading both off disk. `go test` runs each package with
// its own directory as the working directory, so a path like
// "resources/shaders/fragment.glsl" resolves only for a test in the root
// package — and silently stops resolving the moment that test moves into a
// sub-package, which is exactly what happened during the package split.
//
// RepoRoot walks up from the working directory to the directory holding go.mod,
// so the same call works from any package.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/gfx"
)

// RepoRoot returns the absolute path of the directory holding go.mod.
func RepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// RepoFile reads a file named relative to the repository root.
func RepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{RepoRoot(t)}, parts...)...)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// WithoutGeometryUploads makes gfx.Geometry.Load stop at the CPU mesh, so an
// edit that rebuilds a sector can run with no GL context. Restored on cleanup.
//
// Shared rather than duplicated because it is the one seam that lets otherwise
// pure logic — a sector reload, a level load, a script writing a property —
// be tested headless, and four packages' tests need it.
func WithoutGeometryUploads(t *testing.T) {
	t.Helper()
	prev := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = false
	t.Cleanup(func() { gfx.GeometryUploadsEnabled = prev })
}
