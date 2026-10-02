package pack

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/game"
	"foundry/internal/model"
	"foundry/internal/nanitecook"
)

// A cooked Nanite mesh names its slots' textures inside its binary body,
// where no token scan reaches: the prune must read them out, or the rock ships
// untextured. Its glTF source, which nothing loads, stays behind.
func TestCookedMeshTexturesAreFollowed(t *testing.T) {
	root := writeTree(t, map[string]string{
		"levels/start.yaml":    "scenery:\n  - id: r\n    model: models/rock.fnm\n    position: {x: 0, y: 0, z: 0}\n",
		"textures/granite.png": "PNG",
		"textures/lichen.png":  "PNG",
		"textures/unused.png":  "PNG",
		"textures/default.png": "PNG",
	})
	rock := nanitecook.SampleRock(1, 0)
	rock.Slots[0].Texture = "granite"
	rock.Slots[1].Texture = "lichen"
	glb, err := model.EncodeGLB(rock)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "models", "rock.glb")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := nanitecook.CookFile(src, "", true, nanitecook.Options{}); err != nil {
		t.Fatal(err)
	}
	keep := keepSet(t, root, game.Manifest{Title: "T", StartLevel: "start"})
	for _, want := range []string{"models/rock.fnm", "textures/granite.png", "textures/lichen.png"} {
		if !keep[want] {
			t.Errorf("%s was pruned", want)
		}
	}
	for _, not := range []string{"textures/unused.png", "models/rock.glb"} {
		if keep[not] {
			t.Errorf("%s was kept", not)
		}
	}
}
