package render

// env_gl_test.go — the environment the specular reflects (envmap.go), on a
// GPU: the sky's cube and its prefiltered mips, and a reflection volume's
// capture through GI's distance field.
//
//	FOUNDRY_GL_TEST=1 go test -run Env ./internal/render/

import (
	"math"
	"testing"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// envMeans is the mean colour of every cube of the environment array at mip
// m — one readback for all of them: Mesa's D3D12 driver can hang on a second
// readback of the same texture (glext/readback.go).
func envMeans(t *testing.T, m int) [envCubes]mgl32.Vec3 {
	t.Helper()
	size := envSize >> m
	data := make([]float32, size*size*6*envCubes*4)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP_ARRAY, envMap.array)
	glext.GetTexImage(gl.TEXTURE_CUBE_MAP_ARRAY, int32(m), gl.RGBA, gl.FLOAT, gl.Ptr(&data[0]))
	gl.BindTexture(gl.TEXTURE_CUBE_MAP_ARRAY, 0)
	var out [envCubes]mgl32.Vec3
	for c := 0; c < envCubes; c++ {
		var sum mgl32.Vec3
		base := c * 6 * size * size * 4
		for i := 0; i < 6*size*size; i++ {
			px := data[base+i*4 : base+i*4+3]
			for k := 0; k < 3; k++ {
				if math.IsNaN(float64(px[k])) || math.IsInf(float64(px[k]), 0) {
					t.Fatalf("cube %d mip %d texel %d is %v", c, m, i, px)
				}
				sum[k] += px[k]
			}
		}
		out[c] = sum.Mul(1 / float32(6*size*size))
	}
	return out
}

func envTestSetup(t *testing.T) {
	t.Helper()
	prev, prevChosen := gfx.Reflections, gfx.ReflectionsChosen
	gfx.Reflections, gfx.ReflectionsChosen = gfx.ReflectionsLow, true
	envMap.failed = false
	t.Cleanup(func() {
		gfx.Reflections, gfx.ReflectionsChosen = prev, prevChosen
		envMap.lvl = nil
	})
}

func TestEnvSkyCubeKeepsAUniformSkyAtEveryRoughness(t *testing.T) {
	giTestSetup(t, gfx.GIOff, nil)
	envTestSetup(t)
	// No sky: the environment is the clear colour all round.
	lvl := &world.Level{Name: "env_uniform"}
	UpdateEnvMaps(lvl, WorldDraw{})
	gl.Finish()
	if !envMap.active {
		t.Fatal("the environment did not build")
	}
	want := WorldClearColor(lvl)
	for m := 0; m < envMips; m++ {
		got := envMeans(t, m)[0]
		if got.Sub(want).Len() > 0.01 {
			t.Errorf("sky cube mip %d (roughness %.2f) is %v; a uniform sky must stay %v at every roughness",
				m, float32(m)/float32(envMips-1), got, want)
		}
	}
	if gfx.Scene.Block.EnvOn != 1 {
		t.Errorf("envOn is %d, want 1", gfx.Scene.Block.EnvOn)
	}
}

func TestEnvReflectionVolumeCapturesTheRoomNotTheSky(t *testing.T) {
	giTestSetup(t, gfx.GIMedium, nil)
	envTestSetup(t)
	lamp := world.LightSet{Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.2}}
	lamp.Points = []world.PointLight{{Position: mgl32.Vec3{5, 2.5, 5}, Color: mgl32.Vec3{1, 0.6, 0.2}, Brightness: 2, Radius: 12}}
	lvl := giRoom(lamp, nil)
	lvl.Name = "env_room"
	lvl.Volumes = []world.Volume{{ID: "room_refl", Enabled: true, Position: mgl32.Vec3{5, 2, 5},
		HalfX: 4.5, HalfY: 1.5, HalfZ: 4.5, Reflection: &world.VolumeReflection{Enabled: true, Intensity: 1, Fade: 1}}}
	cam := mgl32.Vec3{5, 2, 5}
	giRunFrames(t, lvl, cam, 4)
	t0 := time.Now()
	UpdateEnvMaps(lvl, WorldDraw{})
	gl.Finish()
	t.Logf("capture + prefilter: %v", time.Since(t0))
	if gfx.Scene.Block.NumReflVolumes != 1 {
		t.Fatalf("%d reflection volumes in the block, want 1", gfx.Scene.Block.NumReflVolumes)
	}
	means := envMeans(t, envMips-1)
	room, sky := means[1], means[0]
	// The lamp is orange and the walls grey: the capture must be warm, where
	// the clear-colour sky is cool.
	if !(room.X() > room.Z()) {
		t.Errorf("the room's capture %v is not lit by its orange lamp (the sky is %v)", room, sky)
	}
	if room.Sub(sky).Len() < 0.02 {
		t.Errorf("the room's capture %v is the sky %v: its rays did not hit the walls", room, sky)
	}
}
