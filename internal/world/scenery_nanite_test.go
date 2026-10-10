package world

// scenery_nanite_test.go — a scenery placement of a cooked Nanite mesh (.fnm)
// with no GL: it loads the cook's bounds and one submesh per material slot
// over the cooked pool, honours slot texture overrides, and never claims the
// GPU path headlessly.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/model"
	"foundry/internal/nanitecook"
	"foundry/internal/testenv"

	"github.com/go-gl/mathgl/mgl32"
)

func TestSceneryLoadsACookedMesh(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	dir := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = dir
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	rock := nanitecook.SampleRock(3, 0.2)
	rock.Slots[0].Texture = "granite"
	glb, err := model.EncodeGLB(rock)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "models", "rock.glb")
	if err := os.WriteFile(src, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := nanitecook.CookFile(src, "", true, nanitecook.Options{})
	if err != nil {
		t.Fatal(err)
	}

	s := NewScenery("r", "models/rock.fnm", mgl32.Vec3{})
	s.Materials = map[string]string{"moss": "snow"}
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if s.Nanite == nil || s.NaniteGPU {
		t.Fatalf("cooked mesh %v, GPU path %v", s.Nanite != nil, s.NaniteGPU)
	}
	if s.LocalMin != res.Mesh.Lo || s.LocalMax != res.Mesh.Hi || !s.HasBounds() {
		t.Fatal("the placement's bounds are not the cook's")
	}
	if len(s.Submeshes) != 2 {
		t.Fatalf("%d submeshes for two slots", len(s.Submeshes))
	}
	if s.SlotTexture(0) != "granite" || s.SlotTexture(1) != "snow" {
		t.Fatalf("slot textures %q %q", s.SlotTexture(0), s.SlotTexture(1))
	}
	if s.Submeshes[0].Texture != "granite" || s.Submeshes[1].Texture != "snow" {
		t.Fatal("the submeshes do not wear the resolved textures")
	}
	tris := 0
	for i := range s.Submeshes {
		s.Submeshes[i].ForEachTriangle(func(a, b, c mgl32.Vec3) bool { tris++; return true })
	}
	if tris != res.Mesh.TriangleCount() {
		t.Fatalf("the submeshes hold %d triangles of %d", tris, res.Mesh.TriangleCount())
	}
	// And the placement round-trips with its overrides.
	back := SceneryFromRaw(SceneryToRaw(&s))
	if back.Materials["moss"] != "snow" {
		t.Fatal("the override was lost")
	}
}
