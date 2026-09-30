package render

// terrain_gl_test.go — the terrain pass on a real GPU.
//
//	FOUNDRY_GL_TEST=1 go test -run Terrain ./internal/render/

import (
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gltest"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// terrainProbe draws a level's terrain through RenderTerrain, looking down at
// 45 degrees onto the chunk origin, lit by flat white ambient so the shaded
// colour is the albedo.
type terrainProbe struct {
	t   *testing.T
	lvl *world.Level
}

func newTerrainProbe(t *testing.T, tr *world.Terrain) *terrainProbe {
	t.Helper()
	gltest.Context(t)
	withRepoResources(t)
	skyTestTarget(t)
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())

	if err := InitShadows(repoShader(t, "shadow_vert.glsl"), repoShader(t, "shadow_frag.glsl"),
		repoShader(t, "shadow_cube_vert.glsl"), repoShader(t, "shadow_cube_frag.glsl")); err != nil {
		t.Fatalf("shadow programs: %v", err)
	}
	TerrainInit()
	t.Cleanup(func() {
		ReleaseTerrainMeshes()
		terrains.initialized = false
	})

	var ls world.LightSet
	ls.Global.Color = mgl32.Vec3{1, 1, 1}
	ls.Global.Brightness = 1
	ch := world.Chunk{ID: "ground", Terrain: tr}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}, Lights: ls}

	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45), 1, 0.1, 100)
	gfx.Cam.Position = mgl32.Vec3{0, 3, 3}
	gfx.Cam.Direction = mgl32.Vec3{0, -1, -1}.Normalize()
	gfx.UpdateCamMatrix()
	return &terrainProbe{t: t, lvl: lvl}
}

// frame syncs and draws the level's terrain and reads the whole target back.
func (p *terrainProbe) frame() []uint8 {
	p.t.Helper()
	SyncTerrainMeshes(p.lvl)
	gl.Disable(gl.CULL_FACE)
	gl.ClearColor(1, 0, 1, 1) // magenta: nothing drawn
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	pv := gfx.Projection.Mul4(gfx.Cam.Matrix)
	f := ExtractFrustum(pv)
	RenderTerrain(p.lvl, pv, &f, ShadowFrame{})
	if e := gl.GetError(); e != gl.NO_ERROR {
		p.t.Fatalf("GL error 0x%x drawing terrain", e)
	}
	px := make([]uint8, skyTestSize*skyTestSize*4)
	gl.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}

func centre(px []uint8) [4]uint8 {
	i := (skyTestSize/2*skyTestSize + skyTestSize/2) * 4
	return [4]uint8{px[i], px[i+1], px[i+2], px[i+3]}
}

func flatTerrain() *world.Terrain {
	return world.NewTerrain(mgl32.Vec3{-4, 0, -4}, 1, 8, 8)
}

func TestUntexturedTerrainIsPaintedWithItsNormal(t *testing.T) {
	p := newTerrainProbe(t, flatTerrain())
	c := centre(p.frame())
	nearlyByte(t, "red", c[0], 0.5)
	nearlyByte(t, "green", c[1], 1.0)
	nearlyByte(t, "blue", c[2], 0.5)
}

// A layer painted to full weight shows that layer's texture: the same pixel
// a terrain made only of that texture shows, and not the base layer's.
func TestAPaintedTerrainLayerShowsItsTexture(t *testing.T) {
	grassOnly := flatTerrain()
	grassOnly.Layers[0] = "grass"
	p := newTerrainProbe(t, grassOnly)
	want := p.frame()

	painted := flatTerrain()
	painted.Layers = [world.TerrainLayerCount]string{"concrete", "", "grass"}
	for z := 0; z <= painted.CellsZ; z++ {
		for x := 0; x <= painted.CellsX; x++ {
			painted.PaintWeight(x, z, 2, 1)
		}
	}
	painted.Touch()
	p.lvl.Chunks[0].Terrain = painted
	got := p.frame()
	if c, w := centre(got), centre(want); c != w {
		t.Fatalf("layer 2 painted to full shows %v, the grass terrain shows %v", c, w)
	}

	concrete := flatTerrain()
	concrete.Layers[0] = "concrete"
	p.lvl.Chunks[0].Terrain = concrete
	if centre(p.frame()) == centre(want) {
		t.Fatal("concrete and grass came out the same colour — the probe proves nothing")
	}
}

func TestATerrainHoleDrawsNothing(t *testing.T) {
	tr := flatTerrain()
	tr.SetHole(4, 3, true)
	tr.SetHole(3, 3, true)
	tr.SetHole(4, 4, true)
	tr.SetHole(3, 4, true)
	p := newTerrainProbe(t, tr)
	if c := centre(p.frame()); c != [4]uint8{255, 0, 255, 255} {
		t.Fatalf("the hole under the centre drew %v", c)
	}
}

// A sculpt re-uploads only the rows and patches it touched. What that draws
// has to be exactly what uploading the whole edited grid again draws.
func TestASculptedTerrainRegionDrawsLikeAFullRebuild(t *testing.T) {
	tr := world.NewTerrain(mgl32.Vec3{-40, 0, -40}, 1, 80, 80)
	tr.Layers[0] = "grass"
	p := newTerrainProbe(t, tr)
	p.frame()

	for z := 38; z <= 42; z++ {
		for x := 37; x <= 43; x++ {
			tr.Heights[tr.Index(x, z)] = 0.6
		}
	}
	tr.PaintWeight(40, 40, 1, 1)
	tr.SetHole(34, 40, true)
	tr.TouchRect(34, 38, 43, 42)
	partial := p.frame()

	ReleaseTerrainMeshes()
	tr.Touch()
	full := p.frame()
	for i := range full {
		if partial[i] != full[i] {
			t.Fatalf("pixel %d differs: partial upload %d, full rebuild %d", i/4, partial[i], full[i])
		}
	}
}
