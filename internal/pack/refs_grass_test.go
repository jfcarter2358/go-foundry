package pack

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/game"
	"foundry/internal/grassfield"
)

// A grass volume names its field in the level's YAML; the field names its
// kit and the kit its textures inside their binary bodies. The prune follows
// both hops, and leaves a texture nothing names behind.
func TestGrassFieldKitAndTexturesAreFollowed(t *testing.T) {
	root := writeTree(t, map[string]string{
		"levels/start.yaml": "volumes:\n  - id: g\n    position: {x: 0, y: 0, z: 0}\n    half: {x: 1, y: 1, z: 1}\n" +
			"    grass:\n      file: generated/w/grass/grass_0_0.fgrass\n",
		"textures/ph_grass_cov.png": "PNG",
		"textures/unused.png":       "PNG",
		"textures/default.png":      "PNG",
	})
	kit := &grassfield.Kit{Kinds: []grassfield.Kind{{Name: "g", Texture: "ph_grass_cov", Height: 1, Radius: 1,
		LODs: []grassfield.Mesh{{Vertices: make([]float32, 3*grassfield.VertexFloats), Indices: []uint32{0, 1, 2}}}}}}
	kdata, err := grassfield.EncodeKit(kit)
	if err != nil {
		t.Fatal(err)
	}
	f, err := grassfield.BuildField(grassfield.FieldSpec{Kit: "generated/w/grass/grass.fgk", Min: [2]float32{0, 0},
		Max: [2]float32{4, 4}, Cell: 2, Kinds: 1}, []grassfield.Instance{{Pos: [3]float32{1, 0, 1}, Scale: 1}})
	if err != nil {
		t.Fatal(err)
	}
	fdata, err := grassfield.EncodeField(f)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "generated", "w", "grass")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"grass.fgk": kdata, "grass_0_0.fgrass": fdata, "grass_9_9.fgrass": fdata} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keep := keepSet(t, root, game.Manifest{Title: "T", StartLevel: "start"})
	for _, want := range []string{"generated/w/grass/grass_0_0.fgrass", "generated/w/grass/grass.fgk", "textures/ph_grass_cov.png"} {
		if !keep[want] {
			t.Errorf("%s was pruned", want)
		}
	}
	for _, not := range []string{"textures/unused.png", "generated/w/grass/grass_9_9.fgrass"} {
		if keep[not] {
			t.Errorf("%s was kept", not)
		}
	}
}
