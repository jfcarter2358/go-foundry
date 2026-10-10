package render

// scene_block_gl_test.go — gfx.SceneBlock against the block the driver laid
// out from scene_block.glsl.
//
//	FOUNDRY_GL_TEST=1 go test -run SceneBlock ./internal/render/
//
// The Go struct is written to std140 by hand. A member added to one side and
// not the other, or a padding rule misread, shifts everything after it, and
// the symptom would be lights in the wrong places rather than an error — so
// every member's offset, array stride and matrix stride is read back from the
// linked world program and compared.

import (
	"reflect"
	"strings"
	"testing"
	"unicode"

	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// sceneBlockGLSLName is a Go field's GLSL member name: the same name with its
// first letter lowered.
func sceneBlockGLSLName(goName string) string {
	r := []rune(goName)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// sceneResource reads one property of a uniform (a block member included) by
// name, or reports it absent.
func sceneResource(prog uint32, name string, prop uint32) (int32, bool) {
	idx := gl.GetProgramResourceIndex(prog, gl.UNIFORM, gl.Str(name+"\x00"))
	if idx == gl.INVALID_INDEX {
		return 0, false
	}
	var v int32
	gl.GetProgramResourceiv(prog, gl.UNIFORM, idx, 1, &prop, 1, nil, &v)
	return v, true
}

// sceneMember reports whether prog has name as a uniform or block member —
// the check GetUniformLocation can no longer make, since a block member has
// no location.
func sceneMember(prog uint32, name string) bool {
	return gl.GetProgramResourceIndex(prog, gl.UNIFORM, gl.Str(name+"\x00")) != gl.INVALID_INDEX
}

func TestSceneBlockMatchesTheShader(t *testing.T) {
	gltest.Context(t)
	withRepoResources(t)
	if err := gfx.LoadProgram(repoShader(t, "vertex.glsl"), repoShader(t, "fragment.glsl")); err != nil {
		t.Fatalf("world program: %v", err)
	}
	prog := gfx.Program

	blockIdx := gl.GetProgramResourceIndex(prog, gl.UNIFORM_BLOCK, gl.Str("SceneBlock\x00"))
	if blockIdx == gl.INVALID_INDEX {
		t.Fatal("the world program has no SceneBlock")
	}
	props := []uint32{gl.BUFFER_DATA_SIZE, gl.BUFFER_BINDING}
	vals := make([]int32, len(props))
	gl.GetProgramResourceiv(prog, gl.UNIFORM_BLOCK, blockIdx, int32(len(props)), &props[0], int32(len(vals)), nil, &vals[0])
	if int(vals[0]) != gfx.SceneBlockSize {
		t.Errorf("SceneBlock is %d bytes in the shader, %d in gfx.SceneBlock", vals[0], gfx.SceneBlockSize)
	}
	if vals[1] != gfx.SceneBlockBinding {
		t.Errorf("SceneBlock binds at %d, gfx.SceneBlockBinding is %d", vals[1], gfx.SceneBlockBinding)
	}

	typ := reflect.TypeOf(gfx.SceneBlock{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Name == "_" {
			continue
		}
		name := sceneBlockGLSLName(f.Name)
		// An array member is an array of arrays on the Go side: [N][4] for
		// scalars and vec3s, [N]mgl32.Mat4 and [N][3][4] for matrices. A
		// lone vec3 ([3]float32) or mat4 (mgl32.Mat4) is an array of floats.
		isArray := f.Type.Kind() == reflect.Array && f.Type.Elem().Kind() == reflect.Array
		lookup := name
		if isArray {
			lookup = name + "[0]"
		}
		off, ok := sceneResource(prog, lookup, gl.OFFSET)
		if !ok {
			t.Errorf("%s: %q is not a member of the linked block", f.Name, lookup)
			continue
		}
		if uintptr(off) != f.Offset {
			t.Errorf("%s: offset %d in the shader, %d in gfx.SceneBlock", f.Name, off, f.Offset)
		}
		if isArray {
			stride, _ := sceneResource(prog, lookup, gl.ARRAY_STRIDE)
			if want := f.Type.Elem().Size(); uintptr(stride) != want {
				t.Errorf("%s: array stride %d in the shader, %d in gfx.SceneBlock", f.Name, stride, want)
			}
		}
		if strings.HasPrefix(name, "fogVolBasis") || strings.HasPrefix(name, "cascadeMatrix") || name == "cloudShadowMatrix" {
			mstride, _ := sceneResource(prog, lookup, gl.MATRIX_STRIDE)
			if mstride != 16 {
				t.Errorf("%s: matrix stride %d, want 16", f.Name, mstride)
			}
		}
	}
}

// The Go side's array sizes have to be the shader's, and the shader's are the
// ones the engine fills to.
func TestSceneBlockSizesAreTheEngines(t *testing.T) {
	if gfx.SceneMaxDirLights != world.MaxDirLights || gfx.SceneMaxPointLights != world.MaxPointLights ||
		gfx.SceneShadowCascades != ShadowCascades || gfx.SceneMaxFogVolumes != world.MaxFogVolumes {
		t.Errorf("gfx.Scene* sizes (%d, %d, %d, %d) differ from the engine's (%d, %d, %d, %d)",
			gfx.SceneMaxDirLights, gfx.SceneMaxPointLights, gfx.SceneShadowCascades, gfx.SceneMaxFogVolumes,
			world.MaxDirLights, world.MaxPointLights, ShadowCascades, world.MaxFogVolumes)
	}
}
