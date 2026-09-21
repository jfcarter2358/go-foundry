package gfx

// UniformLoc remembers a location per program name, and a name is reused once
// its program is deleted, so every link of a program must forget what was
// remembered under its name — or a relinked shader is driven through the old
// shader's locations, which fails silently as uniforms landing in the wrong
// slot. The check is on the source, following world_passes_test.go's precedent
// for an invariant with no symptom until it is broken.
//
// imgui and the UI renderer link programs of their own that are never asked
// about through UniformLoc, so they are exempt.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEveryProgramLinkForgetsItsUniformLocations(t *testing.T) {
	exempt := map[string]bool{"imguibackend": true, "uidraw": true}
	root := filepath.Join("..")
	links := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		if exempt[filepath.Base(filepath.Dir(path))] {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(src), "\n")
		for i, l := range lines {
			if !strings.Contains(l, "gl.LinkProgram(") {
				continue
			}
			links++
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], "ForgetUniformLocations(") {
				t.Errorf("%s:%d links a program without ForgetUniformLocations on the next line", path, i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if links < 4 {
		t.Fatalf("found only %d program links under internal/ — the walk is not looking where the engine links", links)
	}
}
