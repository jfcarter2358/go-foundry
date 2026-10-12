package stages

import (
	"os"
	"testing"

	"foundry/internal/fileutil"
)

// TestMain gives the package's tests a resources folder of their own. The
// placing stages write their rock materials under fileutil.ResourcesPath's
// materials/ (materials_out.go), and a test that runs one without setting
// it would otherwise write into the package's own directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "stages-resources-")
	if err != nil {
		panic(err)
	}
	if fileutil.ResourcesPath == "" {
		fileutil.ResourcesPath = dir
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
