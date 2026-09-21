package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

// An executable carries its own shaders only when it runs apart from its
// project; run from the project, the project's are the executable's.
func TestEngineShadersFor(t *testing.T) {
	root := t.TempDir()
	install := filepath.Join(root, "install")
	proj := filepath.Join(root, "proj")
	shaders := filepath.Join(install, "resources", "shaders")
	if err := os.MkdirAll(shaders, 0755); err != nil {
		t.Fatal(err)
	}
	if got := EngineShadersFor(install, proj); got != "" {
		t.Errorf("no fragment.glsl, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(shaders, "fragment.glsl"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if got := EngineShadersFor(install, proj); got != shaders {
		t.Errorf("apart from the project: %q, want %q", got, shaders)
	}
	if got := EngineShadersFor(install, install); got != "" {
		t.Errorf("run from the project: %q, want the project's", got)
	}

	prevE, prevR := EngineShadersDir, ResourcesPath
	defer func() { EngineShadersDir, ResourcesPath = prevE, prevR }()
	ResourcesPath = filepath.Join(proj, "resources")
	EngineShadersDir = ""
	if got := ShaderPath("shaders/fog.glsl"); got != filepath.Join(proj, "resources", "shaders", "fog.glsl") {
		t.Errorf("project shader = %q", got)
	}
	EngineShadersDir = shaders
	if got := ShaderPath("fog.glsl"); got != filepath.Join(shaders, "fog.glsl") {
		t.Errorf("engine shader = %q", got)
	}
}
