package levelio

// level_blend_test.go — blend_texture/blend_mask survive the YAML round trip
// on a brush face, and stay unset (blending off) when never authored.

import (
	"foundry/internal/testenv"
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func blendRawChunk() world.RawChunk {
	b := world.NewBoxBrush("room", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{5, 2, 5}, "concrete")
	b.Faces[0].BlendTexture = "dirt"
	b.Faces[0].BlendMask = "mask"
	return world.RawChunk{ID: "chunk", Brushes: []world.RawBrush{world.BrushToRaw(&b)}}
}
func TestBlendTextureAndMaskSurviveSaveLoadRoundTrip(t *testing.T) {
	testenv.WithoutGeometryUploads(t)

	first, _, err := loadRawChunk(blendRawChunk())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	check := func(label string, b *world.Brush) {
		if b.Faces[0].BlendTexture != "dirt" || b.Faces[0].BlendMask != "mask" {
			t.Fatalf("%s: face 0 blend fields = (%q, %q), want (dirt, mask)",
				label, b.Faces[0].BlendTexture, b.Faces[0].BlendMask)
		}
		for i := 1; i < len(b.Faces); i++ {
			if b.Faces[i].BlendTexture != "" || b.Faces[i].BlendMask != "" {
				t.Fatalf("%s: face %d should have no blend fields, got (%q, %q)",
					label, i, b.Faces[i].BlendTexture, b.Faces[i].BlendMask)
			}
		}
	}
	check("load", &first.Brushes[0])

	raw, err := chunkToRaw(&first)
	if err != nil {
		t.Fatalf("chunkToRaw: %v", err)
	}
	second, _, err := loadRawChunk(raw)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	check("round trip", &second.Brushes[0])
}
