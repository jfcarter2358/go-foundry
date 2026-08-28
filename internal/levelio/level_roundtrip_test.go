package levelio

// level_roundtrip_test.go — the shipped levels, out through the writer and back.

import (
	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

// TestShippedLevelsSurviveASaveReload loads every level in resources/, writes
// it back out and loads it again.
//
// The point is the surface-mesh migration: the writer no longer emits
// `extrusions:` or `terrain_points:` at all, so if the loader could not read
// what it wrote, a single save would quietly flatten every shaped surface in
// the game. A unit test on one synthetic wall would not catch that, because the
// failure is in the seam between the two halves.
func TestShippedLevelsSurviveASaveReload(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	if gfx.TextureNames == nil {
		gfx.TextureNames = map[string]string{}
	}
	entries, _ := os.ReadDir(filepath.Join(testenv.RepoRoot(t), "resources", "textures"))
	names := []string{"", "default"}
	for _, e := range entries {
		n := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		names = append(names, n)
	}
	for _, n := range names {
		gfx.Textures[n] = 1
		gfx.TextureNames[n] = n
	}
	for _, path := range []string{"canyon.yaml", "test.yaml"} {
		full := filepath.Join(testenv.RepoRoot(t), "resources", "levels", path)
		lvl, err := LoadLevel(full)
		if err != nil {
			t.Fatalf("%s load: %v", path, err)
		}
		raw, err := LevelToRaw(&lvl)
		if err != nil {
			t.Fatalf("%s save: %v", path, err)
		}
		out, err := yaml.Marshal(raw)
		if err != nil {
			t.Fatalf("%s marshal: %v", path, err)
		}
		tmp := filepath.Join(t.TempDir(), path)
		if err := os.WriteFile(tmp, out, 0o644); err != nil {
			t.Fatal(err)
		}
		lvl2, err := LoadLevel(tmp)
		if err != nil {
			t.Fatalf("%s reload: %v", path, err)
		}
		nMesh, nFaces := 0, 0
		for ci := range lvl2.Chunks {
			for si := range lvl2.Chunks[ci].Sectors {
				s := &lvl2.Chunks[ci].Sectors[si]
				for _, m := range []*world.SurfaceMesh{&s.Floor.Mesh, &s.Ceiling.Mesh} {
					if !m.IsEmpty() {
						nMesh++
						nFaces += len(m.Faces)
					}
				}
				for wi := range s.Walls {
					if m := &s.Walls[wi].Mesh; !m.IsEmpty() {
						nMesh++
						nFaces += len(m.Faces)
					}
				}
			}
		}
		if len(lvl2.Chunks) != len(lvl.Chunks) {
			t.Errorf("%s: %d chunks in, %d out", path, len(lvl.Chunks), len(lvl2.Chunks))
		}
		t.Logf("%s: %d chunks, %d shaped surfaces, %d faces; wrote %d bytes",
			path, len(lvl2.Chunks), nMesh, nFaces, len(out))
		if strings.Contains(string(out), "extrusions:") || strings.Contains(string(out), "terrain_points:") {
			t.Errorf("%s wrote legacy keys back out", path)
		}
	}
}
