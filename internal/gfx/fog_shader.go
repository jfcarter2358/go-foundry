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
	path := fileutil.ShaderPath("fog.glsl")
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

var (
	cloudShadowShaderMu  sync.Mutex
	cloudShadowShaderSrc string
)

// CloudShadowSource returns cloud_shadow.glsl, reading it once.
func CloudShadowSource() (string, error) {
	cloudShadowShaderMu.Lock()
	defer cloudShadowShaderMu.Unlock()
	if cloudShadowShaderSrc != "" {
		return cloudShadowShaderSrc, nil
	}
	path := fileutil.ShaderPath("cloud_shadow.glsl")
	src, err := ReadShaderFile(path)
	if err != nil {
		return "", fmt.Errorf("read cloud shadow shader: %w", err)
	}
	cloudShadowShaderSrc = src
	return src, nil
}

// InjectCloudShadow splices the shared cloud-shadow lookup into a fragment
// shader, on InjectFog's terms: the world shader calls cloudShadowFactor, so a
// missing file is an error naming the file rather than a compile failure naming
// the function. The biome_light.glsl consumers get the same file through
// render's biomeLightSnippet instead.
func InjectCloudShadow(src string) (string, error) {
	shared, err := CloudShadowSource()
	if err != nil {
		return "", err
	}
	return InjectAfterVersion(src, shared), nil
}

var (
	lightModelShaderMu  sync.Mutex
	lightModelShaderSrc string
)

// LightModelSource returns light_model.glsl, reading it once.
func LightModelSource() (string, error) {
	lightModelShaderMu.Lock()
	defer lightModelShaderMu.Unlock()
	if lightModelShaderSrc != "" {
		return lightModelShaderSrc, nil
	}
	path := fileutil.ShaderPath("light_model.glsl")
	src, err := ReadShaderFile(path)
	if err != nil {
		return "", fmt.Errorf("read light model shader: %w", err)
	}
	lightModelShaderSrc = src
	return src, nil
}

// InjectLightModel splices the shared lighting model (wrapped Lambert,
// hemispheric ambient) into a fragment shader, on
// InjectFog's terms. The biome_light.glsl consumers get the same file through
// render's biomeLightSnippet.
func InjectLightModel(src string) (string, error) {
	shared, err := LightModelSource()
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
