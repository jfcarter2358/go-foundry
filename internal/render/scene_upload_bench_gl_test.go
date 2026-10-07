package render

// scene_upload_bench_gl_test.go — what a frame pays to hand the scene's
// lights, shadows and fog to the programs that shade with them.
//
//	FOUNDRY_GL_TEST=1 go test -run '^$' -bench FrameSceneUpload -benchmem ./internal/render/
//
// The upload is CPU work in front of a cgo call per uniform, so it is measured
// here on a real context: five lit programs' worth a frame (the world program,
// sprites, the biome passes, Nanite's material pass, terrain), with a level's
// worth of lights — two suns, eight point lights, four fog volumes.

import (
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/glctx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// benchSceneLevel is the light and fog load the benchmark uploads.
func benchSceneLevel() (world.LightSet, *world.Fog, []world.Volume) {
	var ls world.LightSet
	ls.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.3}
	for i := 0; i < 2; i++ {
		d := world.DirectionalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1, Yaw: float32(30 * i), Pitch: -40, CastShadows: true, ShadowStrength: 1}
		d.SyncDirection()
		ls.Directional = append(ls.Directional, d)
	}
	for i := 0; i < world.MaxPointLights; i++ {
		ls.Points = append(ls.Points, world.PointLight{
			Position: mgl32.Vec3{float32(i), 2, 0}, Color: mgl32.Vec3{1, 0.8, 0.6},
			Brightness: 1, Radius: 10, CastShadows: i < 3, ShadowStrength: 1,
		})
	}
	fog := &world.Fog{Enabled: true, Mode: 1, Color: mgl32.Vec3{0.5, 0.5, 0.6}, Start: 10, End: 200, MaxOpacity: 1}
	var vols []world.Volume
	for i := 0; i < 4; i++ {
		f := world.DefaultVolumeFog()
		f.Enabled = true
		vols = append(vols, world.Volume{Position: mgl32.Vec3{float32(10 * i), 0, 0}, HalfX: 4, HalfY: 4, HalfZ: 4, Fog: &f})
	}
	return ls, fog, vols
}

func BenchmarkFrameSceneUpload(b *testing.B) {
	if os.Getenv("FOUNDRY_GL_TEST") == "" {
		b.Skip("set FOUNDRY_GL_TEST=1 to run (needs a display and a GL 4.3 driver)")
	}
	if err := glctx.Acquire(64, 64); err != nil {
		b.Fatalf("GL context: %v", err)
	}
	defer glctx.Release()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join("..", "..", "resources")
	defer func() { fileutil.ResourcesPath = prev }()
	shaders := filepath.Join("..", "..", "resources", "shaders")
	if err := gfx.LoadProgram(filepath.Join(shaders, "vertex.glsl"), filepath.Join(shaders, "fragment.glsl")); err != nil {
		b.Fatalf("world program: %v", err)
	}
	EnsureCascadeArray(ShadowCascades)
	ls, fog, vols := benchSceneLevel()
	frame := newShadowFrame(len(ls.Directional), 0.1)
	const programs = 5

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchUploadFrameScene(gfx.Program, programs, ls, fog, vols, frame)
	}
}
