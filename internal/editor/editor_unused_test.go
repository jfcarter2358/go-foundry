package editor

// The Unused Assets window, drawn for real with no window and no GL, before
// and after a scan of a real (scratch) project.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
)

func TestUnusedWindowScansAndDraws(t *testing.T) {
	root := t.TempDir()
	res := fileutil.ResourcesDir(root)
	for name, body := range map[string]string{
		"game.yaml":          "title: T\nstart_level: start\n",
		"levels/start.yaml":  "chunks:\n  - floor: {texture: grass}\n",
		"textures/grass.png": "",
		"textures/stale.png": "",
		"models/old.fmdl":    "texture: stale\n",
	} {
		p := filepath.Join(res, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	prev := fileutil.ResourcesPath
	t.Cleanup(func() {
		fileutil.ResourcesPath = prev
		unusedState = unusedShared{}
		unusedSelected = map[string]bool{}
	})
	fileutil.ResourcesPath = res

	withImgui(func() { drawReleaseFrame(func() { drawUnusedBody(unusedSnapshot()) }) })

	unusedScan()
	unusedPending.Wait()
	v := unusedSnapshot()
	if v.err != nil || len(v.report.Unused) != 2 {
		t.Fatalf("scan: %v %+v", v.err, v.report.Unused)
	}
	unusedSelected["models/old.fmdl"] = true
	unusedSelected["textures/gone.png"] = true // no longer listed: dropped
	withImgui(func() { drawReleaseFrame(func() { drawUnusedBody(v) }) })
	if unusedSelected["textures/gone.png"] || !unusedSelected["models/old.fmdl"] {
		t.Errorf("selection after draw = %v", unusedSelected)
	}
}
