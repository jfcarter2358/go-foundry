package main

// model_shader_gl_test.go — an opt-in smoke test that the model editor's
// shaders actually compile and link on this machine's driver.
//
// go build cannot see inside a .glsl file, and the model editor compiles its
// program lazily on first open, so a typo in model_vert.glsl would otherwise
// only surface as a black window at the moment someone clicks "Model".
//
// It needs a real GL context, which means a display, so it is opt-in:
//
//	FOUNDRY_GL_TEST=1 go test -run TestModelShadersCompile .
//
// Skipped by default so plain `go test ./...` stays headless-safe.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// glTestContext stands up a hidden GL 4.1 context, or skips the test when the
// opt-in env var is unset. Nothing is torn down on the way out: a Go test does
// not necessarily run on the process's main OS thread, and tearing an SDL video
// subsystem down from another thread segfaults in the driver. The test binary
// is about to exit, so the OS reclaims everything.
func glTestContext(t *testing.T) {
	t.Helper()
	if os.Getenv("FOUNDRY_GL_TEST") == "" {
		t.Skip("set FOUNDRY_GL_TEST=1 to run (needs a display and a GL 4.1 driver)")
	}
	runtime.LockOSThread()

	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Fatalf("sdl.Init: %v", err)
	}
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MAJOR_VERSION, 4)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MINOR_VERSION, 1)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_PROFILE_MASK, sdl.GL_CONTEXT_PROFILE_CORE)

	win, err := sdl.CreateWindow("gl test", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		64, 64, sdl.WINDOW_OPENGL|sdl.WINDOW_HIDDEN)
	if err != nil {
		t.Fatalf("CreateWindow: %v", err)
	}
	if _, err := win.GLCreateContext(); err != nil {
		t.Fatalf("GLCreateContext: %v", err)
	}
	if err := gl.Init(); err != nil {
		t.Fatalf("gl.Init: %v", err)
	}
}

func TestModelShadersCompile(t *testing.T) {
	glTestContext(t)

	prog, err := compileProgramFromFiles(
		"resources/shaders/model_vert.glsl",
		"resources/shaders/model_frag.glsl",
	)
	if err != nil {
		t.Fatalf("model editor shaders failed to build: %v", err)
	}
	if prog == 0 {
		t.Fatal("compileProgramFromFiles returned program 0")
	}

	// Every uniform the renderer sets must exist, or drawModelMesh would be
	// writing to -1 and the mesh would render with a stale matrix.
	for _, name := range []string{"projection", "view", "model", "tex", "camPos", "tint", "smoothShading"} {
		if loc := gl.GetUniformLocation(prog, gl.Str(name+"\x00")); loc < 0 {
			t.Errorf("uniform %q not found in the linked program", name)
		}
	}

	// Attribute locations must match vertex.glsl's, since Geometry.Load builds
	// its VAO against the world program's indices.
	for name, want := range map[string]int32{"vert": 0, "vertTexCoord": 1, "vertNormal": 2} {
		if got := gl.GetAttribLocation(prog, gl.Str(name+"\x00")); got != want {
			t.Errorf("attribute %q at location %d, want %d (must match vertex.glsl)", name, got, want)
		}
	}
}

// TestEntityLoadBuildsSubmeshes is the end-to-end check for the multi-texture
// entity change: a model with two materials must reach the GPU as two
// Geometry, and a model with none as exactly one carrying the entity's own
// texture. Both paths run the real Geometry.Load, which is why this needs a
// context — it resolves its attribute indices against the world shader.
//
// Deliberately not split into t.Run subtests: each would run on a new
// goroutine, and the GL context is current on the one OS thread glTestContext
// locked. GL calls from anywhere else silently do nothing, which shows up as
// geometry that uploads with a zero VAO.
func TestEntityLoadBuildsSubmeshes(t *testing.T) {
	glTestContext(t)

	// executablePath first: LoadProgram resolves the shared skinning source
	// relative to it (model_skin_gpu.go), so building the world program depends
	// on it being set.
	prevExe, prevNames := executablePath, textureNames
	executablePath = "resources"
	textureNames = ScanTextures("resources/textures")
	t.Cleanup(func() { executablePath, textureNames = prevExe, prevNames })

	// Geometry.Load resolves "vert"/"vertTexCoord"/"vertNormal" against the
	// world program, so it has to exist before any upload.
	if err := LoadProgram("resources/shaders/vertex.glsl", "resources/shaders/fragment.glsl"); err != nil {
		t.Fatalf("LoadProgram: %v", err)
	}

	dir := t.TempDir()

	// --- a model with two materials becomes two submeshes ---
	twoPath := filepath.Join(dir, "two.obj")
	m := NewCubeDoc()
	m.SetFaceTexture([]int{0, 1}, "brick")
	if err := SaveOBJ(m, twoPath); err != nil {
		t.Fatal(err)
	}
	two := Entity{ID: "two", ModelPath: twoPath, TexturePath: "stone_floor", Alpha: 1}
	if err := two.Load(); err != nil {
		t.Fatalf("Entity.Load: %v", err)
	}
	if len(two.Submeshes) != 2 {
		t.Fatalf("submeshes = %d, want 2", len(two.Submeshes))
	}
	for i, g := range two.Submeshes {
		if g.Vao == 0 || g.Vbo == 0 {
			t.Errorf("submesh %d has no GPU buffers (vao=%d vbo=%d)", i, g.Vao, g.Vbo)
		}
		if g.Alpha != 1 {
			t.Errorf("submesh %d alpha = %v, want 1", i, g.Alpha)
		}
	}
	if !two.HasGeometry() {
		t.Error("HasGeometry false after a successful load")
	}

	// --- a model with no materials keeps the entity's own texture ---
	boxPath := filepath.Join("resources", "models", "box.obj")
	box := Entity{ID: "box", ModelPath: boxPath, TexturePath: "brick", Alpha: 1}
	if err := box.Load(); err != nil {
		t.Fatalf("Entity.Load: %v", err)
	}
	if len(box.Submeshes) != 1 {
		t.Fatalf("submeshes = %d, want 1 — pre-existing entities must not change", len(box.Submeshes))
	}
	if got := box.Submeshes[0].Texture; got != "brick" {
		t.Errorf("submesh texture = %q, want the entity's own %q", got, "brick")
	}
	if box.Submeshes[0].Vao == 0 {
		t.Error("box submesh has no VAO")
	}
	// 12 triangles x 3 vertices x 5 floats.
	if got := len(box.Submeshes[0].Vertices); got != 180 {
		t.Errorf("submesh has %d floats, want 180", got)
	}

	// --- reloading replaces the upload rather than leaking it ---
	firstVao := box.Submeshes[0].Vao
	if err := box.Load(); err != nil {
		t.Fatal(err)
	}
	if len(box.Submeshes) != 1 {
		t.Errorf("reload appended instead of replacing: %d submeshes", len(box.Submeshes))
	}
	if box.Submeshes[0].Vao != firstVao {
		t.Logf("reload allocated VAO %d (was %d)", box.Submeshes[0].Vao, firstVao)
	}
	// The freed name being handed straight back is the *expected* GL behaviour
	// and is itself evidence the old VAO was deleted; a leak would show up as a
	// strictly increasing name.
	if box.Submeshes[0].Vao == 0 {
		t.Error("reload produced no VAO")
	}
}

// TestSkinnedShadersCompileAndLink is the check the text-level tests cannot
// make: that skin_common.glsl is valid GLSL, that splicing it into all three
// entity passes still links, and that its uniforms and attributes come out where
// the Go side expects them.
//
// The bone-array uniform is the one worth proving at runtime: MAX_BONES is
// chosen against GL 4.1's *guaranteed* vertex uniform budget, and a driver that
// could not fit it would fail to link here rather than silently at the moment
// someone loads a rigged model.
func TestSkinnedShadersCompileAndLink(t *testing.T) {
	glTestContext(t)

	prevExe := executablePath
	executablePath = "resources"
	t.Cleanup(func() { executablePath = prevExe })

	if _, err := skinCommonSource(); err != nil {
		t.Fatalf("skin_common.glsl did not load: %v", err)
	}

	// The colour pass, through the real loader.
	if err := LoadProgram("resources/shaders/vertex.glsl", "resources/shaders/fragment.glsl"); err != nil {
		t.Fatalf("world program with skinning failed to build: %v", err)
	}
	for _, name := range []string{"skinned", "bones"} {
		if loc := gl.GetUniformLocation(Program, gl.Str(name+"\x00")); loc < 0 {
			t.Errorf("uniform %q missing from the world program; applySkinUniforms would write to -1", name)
		}
	}
	for name, want := range map[string]int32{"vert": 0, "vertTexCoord": 1, "vertNormal": 2, "boneIdx": 3, "boneWt": 4} {
		if got := gl.GetAttribLocation(Program, gl.Str(name+"\x00")); got != want {
			t.Errorf("attribute %q at location %d, want %d", name, got, want)
		}
	}

	// Both shadow passes, through the same linkProgram the engine uses.
	var dirProg, cubeProg uint32
	if err := linkProgram("resources/shaders/shadow_vert.glsl", "resources/shaders/shadow_frag.glsl", &dirProg); err != nil {
		t.Fatalf("cascade shadow program with skinning failed to build: %v", err)
	}
	if err := linkProgram("resources/shaders/shadow_cube_vert.glsl", "resources/shaders/shadow_cube_frag.glsl", &cubeProg); err != nil {
		t.Fatalf("point shadow program with skinning failed to build: %v", err)
	}
	for _, p := range []struct {
		name string
		prog uint32
	}{{"cascade", dirProg}, {"point", cubeProg}} {
		for _, name := range []string{"skinned", "bones"} {
			if loc := gl.GetUniformLocation(p.prog, gl.Str(name+"\x00")); loc < 0 {
				t.Errorf("%s shadow program has no %q uniform; an animated entity's shadow would not follow it",
					p.name, name)
			}
		}
	}
}

// TestSkinnedEntityUploadsBoneAttributes runs a rigged model all the way to the
// GPU and checks the extra buffers exist — and, just as importantly, that a
// static model still does not get them.
func TestSkinnedEntityUploadsBoneAttributes(t *testing.T) {
	glTestContext(t)

	if err := LoadProgram("resources/shaders/vertex.glsl", "resources/shaders/fragment.glsl"); err != nil {
		t.Fatalf("LoadProgram: %v", err)
	}
	prevExe, prevNames := executablePath, textureNames
	executablePath = "resources"
	textureNames = ScanTextures("resources/textures")
	t.Cleanup(func() { executablePath, textureNames = prevExe, prevNames })

	doc, idx := boneChain(t)
	doc.AssignVertsToBone(modelAllVerts(doc), idx[0], 1)
	c := doc.Clip(doc.AddClip("walk"))
	c.SetKey(idx[0], 0, identBonePose())
	path := filepath.Join(t.TempDir(), "rigged"+modelExt)
	if err := SaveModel(doc, path); err != nil {
		t.Fatal(err)
	}

	e := Entity{ID: "rigged", ModelPath: path, TexturePath: "brick", Alpha: 1}
	if err := e.Load(); err != nil {
		t.Fatalf("Entity.Load: %v", err)
	}
	if e.Anim == nil {
		t.Fatal("a rigged, animated model produced no playback state")
	}
	if len(e.Submeshes) == 0 {
		t.Fatal("no submeshes uploaded")
	}
	for i := range e.Submeshes {
		g := &e.Submeshes[i]
		if !g.IsSkinned() {
			t.Errorf("submesh %d has no bone buffers (ibo=%d wbo=%d)", i, g.Ibo, g.Wbo)
		}
		// The palette must be the entity's own array, not a copy: the draw paths
		// read it every frame and the entity rewrites it in place.
		if len(g.BonePalette) != len(e.Anim.Palette) {
			t.Errorf("submesh %d palette has %d entries, want %d", i, len(g.BonePalette), len(e.Anim.Palette))
		} else if len(g.BonePalette) > 0 {
			e.Anim.Palette[0] = mgl32.Scale3D(2, 2, 2)
			if g.BonePalette[0] != e.Anim.Palette[0] {
				t.Errorf("submesh %d holds a copy of the palette, so per-frame updates would not reach it", i)
			}
		}
	}

	// A static model must not pay for any of it.
	static := Entity{ID: "static", ModelPath: filepath.Join("resources", "models", "box.obj"), TexturePath: "brick", Alpha: 1}
	if err := static.Load(); err != nil {
		t.Fatalf("Entity.Load: %v", err)
	}
	if static.Anim != nil {
		t.Error("a static model produced playback state")
	}
	for i := range static.Submeshes {
		if static.Submeshes[i].IsSkinned() {
			t.Errorf("static submesh %d uploaded bone buffers", i)
		}
		if len(static.Submeshes[i].BonePalette) != 0 {
			t.Errorf("static submesh %d carries a bone palette", i)
		}
	}
}
