package render

// sprite_fade_test.go — a cutout sprite has to be able to fade.
//
// Cutout's alpha is a threshold, not an opacity: sprite_frag.glsl discards
// below alphaCutoff and turns everything above it into full coverage. A cutout
// sprite faded by its own Alpha therefore showed nothing until the threshold
// and then the whole sprite at once. The fix routes a cutout sprite below 1
// into the alpha pass; these pin both halves of that — the batching here, and
// the shader path the batch selects.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestFadingCutoutSpriteDrawsInTheAlphaPass(t *testing.T) {
	lvl := &world.Level{}
	opaque := world.NewSprite("opaque", mgl32.Vec3{})
	fading := world.NewSprite("fading", mgl32.Vec3{2, 0, 0})
	fading.Alpha = 0.3
	// A true alpha sprite on the same texture and modes. It must not share the
	// fading sprite's batch, or one of the two would draw with the other's
	// shader path.
	translucent := world.NewSprite("translucent", mgl32.Vec3{4, 0, 0})
	translucent.Blend = gfx.SpriteBlendAlpha
	translucent.Alpha = 0.3
	lvl.Sprites = []world.Sprite{opaque, fading, translucent}

	batches := SpriteBatches(lvl, nil, mgl32.Vec3{}, false)
	if len(batches) != 3 {
		t.Fatalf("got %d batches, want 3 (opaque cutout, fading cutout, alpha)", len(batches))
	}
	byFirst := map[float32]spriteBatch{}
	for _, b := range batches {
		if len(b.Instances) != 1 {
			t.Fatalf("batch %+v holds %d instances, want 1", b, len(b.Instances))
		}
		byFirst[b.Instances[0].Pos.X()] = b
	}

	if b := byFirst[0]; b.Blend != gfx.SpriteBlendCutout || b.FadingCutout {
		t.Errorf("a cutout sprite at alpha 1 left the opaque pass: blend %v, fading %v", b.Blend, b.FadingCutout)
	}
	if b := byFirst[2]; b.Blend != gfx.SpriteBlendAlpha || !b.FadingCutout {
		t.Errorf("a cutout sprite at alpha 0.3 is not drawn as a fading cutout: blend %v, fading %v", b.Blend, b.FadingCutout)
	}
	if b := byFirst[4]; b.Blend != gfx.SpriteBlendAlpha || b.FadingCutout {
		t.Errorf("an alpha sprite was marked as a fading cutout: blend %v, fading %v", b.Blend, b.FadingCutout)
	}
}

func TestSpriteShaderCutsFadingCutoutAgainstTheTextureAlone(t *testing.T) {
	src := readShader(t, "sprite_frag.glsl")
	if !strings.Contains(src, "blendMode == 3") {
		t.Fatal("sprite_frag.glsl has no path for spriteModeFadingCutout — a fading cutout sprite would draw as plain alpha and lose its edge")
	}
	if !strings.Contains(src, "if (texel.a < alphaCutoff) {") {
		t.Error("sprite_frag.glsl does not cut a fading cutout against the texture's own alpha — the fade would erode the silhouette instead")
	}
}
