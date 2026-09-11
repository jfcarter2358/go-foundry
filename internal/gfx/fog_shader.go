package gfx

// fog_shader.go — splicing resources/shaders/fog.glsl into a fragment stage.
//
// Same seam InjectSkinning is: one shared source file, read once, spliced in
// after the #version line of every stage that needs it. Fog needs it in more
// places than skinning does — the world shader, the two detail programs that
// reuse its fragment stage, the biome and sprite programs — and the whole point
// of the arrangement is that those are five compilations of ONE fog model, so
// there is nothing to keep in step between them.

import (
	"fmt"
	"foundry/internal/fileutil"
	"path/filepath"
	"sync"
)

var (
	fogShaderMu  sync.Mutex
	fogShaderSrc string
)

// FogCommonSource returns fog.glsl, reading it once.
func FogCommonSource() (string, error) {
	fogShaderMu.Lock()
	defer fogShaderMu.Unlock()
	if fogShaderSrc != "" {
		return fogShaderSrc, nil
	}
	path := filepath.Join(fileutil.ResourcesPath, "shaders", "fog.glsl")
	src, err := ReadShaderFile(path)
	if err != nil {
		return "", fmt.Errorf("read fog shader: %w", err)
	}
	fogShaderSrc = src
	return src, nil
}

// InjectFog splices the shared fog source into a fragment shader.
//
// The error is returned rather than swallowed, exactly as InjectSkinning's is:
// every stage this is called for CALLS applyFog, so continuing without the file
// produces a compile failure naming a function instead of the file that could
// not be read.
func InjectFog(src string) (string, error) {
	shared, err := FogCommonSource()
	if err != nil {
		return "", err
	}
	return InjectAfterVersion(src, shared), nil
}

// MustInjectFog is InjectFog for the callers that already panic on a shader
// they cannot compile (the detail and biome programs). A missing fog.glsl is a
// broken install, not a runtime condition worth limping through.
func MustInjectFog(src string) string {
	out, err := InjectFog(src)
	if err != nil {
		panic(err.Error())
	}
	return out
}
