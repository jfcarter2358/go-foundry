package gfx

// sim_gl_test.go — a texture asked for during the simulation step waits for
// the frame's GL half, and is there by the time the frame draws.

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestATextureAskedForDuringTheStepIsUploadedByTheDrain(t *testing.T) {
	file := filepath.Join(t.TempDir(), "deferred.png")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 4, 2))); err != nil {
		t.Fatal(err)
	}
	f.Close()

	const name = "sim_gl_test_deferred"
	t.Cleanup(func() { delete(Textures, name); delete(TextureSizes, name) })
	prev := TextureUploadsEnabled
	TextureUploadsEnabled = true
	t.Cleanup(func() { TextureUploadsEnabled = prev })
	var hits []string
	SetGLTripwire(func(what string) { hits = append(hits, what) })
	t.Cleanup(func() { SetGLTripwire(nil) })

	BeginSimStep()
	if err := LoadTexture(name, file); err != nil {
		EndSimStep()
		t.Fatalf("LoadTexture: %v", err)
	}
	EndSimStep()
	if len(hits) != 0 {
		t.Fatalf("LoadTexture during the step reached the GL: %v", hits)
	}
	if _, ok := Textures[name]; ok {
		t.Fatal("the texture was registered before its upload")
	}
	// Loaded as far as anyone asking is concerned, and its size known.
	if TextureNames == nil {
		TextureNames = map[string]string{}
	}
	TextureNames[name] = file
	t.Cleanup(func() { delete(TextureNames, name) })
	if err := EnsureTextureLoaded(name); err != nil || len(deferredTextures) != 1 {
		t.Errorf("asking again before the drain: err %v, %d queued; want nil and still one", err, len(deferredTextures))
	}
	if w, h, ok := TextureSize(name); !ok || w != 4 || h != 2 {
		t.Errorf("size %vx%v (%v), want 4x2 straight away", w, h, ok)
	}

	DrainTextureUploads()
	if len(hits) != 1 || hits[0] != "LoadTexture" {
		t.Errorf("the drain made %v, want the one upload", hits)
	}
	if _, ok := Textures[name]; !ok {
		t.Error("after the drain the texture is not registered")
	}
	if len(deferredTextures) != 0 || deferredTextureNames[name] {
		t.Error("the drain left the queue behind")
	}
}
