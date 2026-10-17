package pack

// generated_test.go — a procedural generator's outputs reach a release when
// a level names them, and are never offered as unused.

import (
	"testing"

	"foundry/internal/game"
)

func TestGeneratedMeshesNamedByALevelAreKept(t *testing.T) {
	root := writeTree(t, map[string]string{
		"levels/canyon.yaml":                    "scenery:\n  - id: gen.canyon.ground.tile_0_0\n    model: generated/canyon/ground/tile_0_0.fnm\n",
		"generated/canyon/ground/tile_0_0.fnm":  "x",
		"generated/canyon/ground/heightmap.png": "x",
		"generated/canyon/manifest.yaml":        "version: 1\n",
		"generators/canyon.fgen":                "seed: 1\n",
	})
	keep := keepSet(t, root, game.Manifest{StartLevel: "canyon"})
	if !keep["generated/canyon/ground/tile_0_0.fnm"] {
		t.Error("a generated mesh the level places is not kept: the release would ship a level without its terrain")
	}
	if keep["generated/canyon/ground/heightmap.png"] {
		t.Error("a generator's working file nothing names is kept")
	}
	r, err := FindUnused(UnusedOptions{Src: unusedTree(t, map[string]string{
		"levels/canyon.yaml":                    "scenery:\n  - id: gen.canyon.ground.tile_0_0\n    model: generated/canyon/ground/tile_0_0.fnm\n",
		"generated/canyon/ground/tile_0_0.fnm":  "x",
		"generated/canyon/ground/heightmap.png": "x",
	}), AllLevels: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range r.Unused {
		if len(u.Path) >= 10 && u.Path[:10] == "generated/" {
			t.Errorf("%s is offered as unused: the generator owns it", u.Path)
		}
	}
}
