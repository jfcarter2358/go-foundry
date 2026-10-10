package gfx

// vsm_shader.go — splicing resources/shaders/vsm.glsl, the sun's virtual
// shadow map lookup (render/vsm.go; Nanite Phase 5), into the world
// program's fragment stage, on the terms fog.glsl and cloud_shadow.glsl are
// spliced in on (fog_shader.go): one file, read once, and every stage that
// shades with the sun compiling the same lookup. fragment.glsl calls
// vsmShadow for the light the map shadows (vsmLightSlot, its index + 1; 0, none,
// as every program not told otherwise holds). The biome, sprite and foliage
// programs get the same file with biome_light.glsl (render's
// biomeLightSnippet).

import (
	"fmt"
	"sync"

	"foundry/internal/fileutil"
)

var (
	vsmShaderMu  sync.Mutex
	vsmShaderSrc string
)

// VSMSource returns vsm.glsl, reading it once.
func VSMSource() (string, error) {
	vsmShaderMu.Lock()
	defer vsmShaderMu.Unlock()
	if vsmShaderSrc != "" {
		return vsmShaderSrc, nil
	}
	src, err := ReadShaderFile(fileutil.ShaderPath("vsm.glsl"))
	if err != nil {
		return "", fmt.Errorf("read virtual shadow map shader: %w", err)
	}
	vsmShaderSrc = src
	return src, nil
}

// InjectVSM splices the virtual shadow map lookup into a fragment shader.
func InjectVSM(src string) (string, error) {
	shared, err := VSMSource()
	if err != nil {
		return "", err
	}
	return InjectAfterVersion(src, shared), nil
}
