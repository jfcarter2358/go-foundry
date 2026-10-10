package gfx

// shader_include.go — `#include "file.glsl"` for the engine's shaders.
//
// GLSL has no include. The engine's shared shader code has always been spliced
// by the Go side instead (InjectFog, biomeLightSnippet, …), which suits a file
// whose functions a pass opts into. It suits a DECLARATION every one of those
// files depends on — the scene's uniform block (scene_block.glsl), which fog.glsl,
// cloud_shadow.glsl, light_model.glsl, biome_light.glsl and the world shader all
// read — much less well: each splice site would have to remember it, ahead of
// all the others. So a file that reads the block says so itself, and
// CompileShader, which every shader in the engine goes through, expands the
// directive. The included file carries its own #ifndef guard, which is what
// lets several spliced files each ask for it.

import (
	"fmt"
	"strings"

	"foundry/internal/fileutil"
)

// maxShaderIncludeDepth stops an include cycle the guard did not.
const maxShaderIncludeDepth = 8

// ExpandShaderIncludes replaces every line of the form
//
//	#include "name.glsl"
//
// with that file from the shader directory, recursively. Anything else on a
// line, a commented-out directive included, is left alone.
func ExpandShaderIncludes(src string) (string, error) {
	return expandShaderIncludes(src, 0)
}

func expandShaderIncludes(src string, depth int) (string, error) {
	if !strings.Contains(src, "#include") {
		return src, nil
	}
	if depth > maxShaderIncludeDepth {
		return "", fmt.Errorf("shader #include nested more than %d deep", maxShaderIncludeDepth)
	}
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "#include") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, "#include"))
		if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
			return "", fmt.Errorf("shader: malformed %q (want #include \"file.glsl\")", t)
		}
		name := rest[1 : len(rest)-1]
		body, err := ReadShaderFile(fileutil.ShaderPath(name))
		if err != nil {
			return "", fmt.Errorf("shader #include %q: %w", name, err)
		}
		if body, err = expandShaderIncludes(body, depth+1); err != nil {
			return "", err
		}
		lines[i] = body
	}
	return strings.Join(lines, "\n"), nil
}
