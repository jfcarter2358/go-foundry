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
	FoliageInit()
	if !terrains.initialized || !foliage.initialized {
		t.Fatal("the terrain or foliage pass did not come up")
	}
	t.Cleanup(func() {
		ReleaseTerrainMeshes()
		ReleaseFoliage()
		terrains.initialized = false
		foliage.initialized = false
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
	RenderFoliage(p.lvl, pv, &f, ShadowFrame{})
	if e := gl.GetError(); e != gl.NO_ERROR {
		p.t.Fatalf("GL error 0x%x drawing foliage", e)
	}
	px := make([]uint8, skyTestSize*skyTestSize*4)
	gl.ReadPixels(0, 0, skyTestSize, skyTestSize, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&px[0]))
	return px
}

func centre(px []uint8) [4]uint8 {
	i := (skyTestSize/2*skyTestSize + skyTestSize/2) * 4
	return [4]uint8{px[i], px[i+1], px[i+2], px[i+3]}
}

// flatTerrain is 8x2x8 blocks whose top is at Y 0, one block deep.
func flatTerrain() *world.Terrain {
	t := world.NewTerrain(mgl32.Vec3{-4, -1, -4}, 1, 8, 2, 8)
	t.Generate(world.TerrainFill{Height: 1, Top: 1, Under: 1, Deep: 1})
	return t
}

func TestUntexturedTerrainIsPaintedWithItsNormal(t *testing.T) {
	p := newTerrainProbe(t, flatTerrain())
	c := centre(p.frame())
	nearlyByte(t, "red", c[0], 0.5)
	nearlyByte(t, "green", c[1], 1.0)
	nearlyByte(t, "blue", c[2], 0.5)
}

// A block made of a material shows that material's texture: the same pixel
// a terrain made only of that texture shows, and not the first material's.
func TestABlocksMaterialShowsItsTexture(t *testing.T) {
	grassOnly := flatTerrain()
	grassOnly.Layers[0] = "grass"
	p := newTerrainProbe(t, grassOnly)
	want := p.frame()

	retextured := flatTerrain()
	retextured.Layers = [world.TerrainLayerCount]string{"concrete", "", "grass"}
	for i, b := range retextured.Blocks {
		if b != 0 {
			retextured.Blocks[i] = 3
		}
	}
	retextured.Touch()
	p.lvl.Chunks[0].Terrain = retextured
	got := p.frame()
	if c, w := centre(got), centre(want); c != w {
		t.Fatalf("blocks of material 3 show %v, the grass terrain shows %v", c, w)
	}

	concrete := flatTerrain()
	concrete.Layers[0] = "concrete"
	p.lvl.Chunks[0].Terrain = concrete
	if centre(p.frame()) == centre(want) {
		t.Fatal("concrete and grass came out the same colour — the probe proves nothing")
	}
}

func TestAMinedShaftDrawsNothing(t *testing.T) {
	tr := flatTerrain()
	for _, xz := range [][2]int{{3, 3}, {4, 3}, {3, 4}, {4, 4}} {
		tr.SetBlock(xz[0], 0, xz[1], 0)
	}
	p := newTerrainProbe(t, tr)
	// Looking down at 45 degrees into a 2x2 shaft, the centre pixel sees its
	// far wall rather than through it; look straight down instead.
	gfx.Cam.Position = mgl32.Vec3{0, 3, 0.001}
	gfx.Cam.Direction = mgl32.Vec3{0, -1, -0.0001}.Normalize()
	gfx.UpdateCamMatrix()
	if c := centre(p.frame()); c != [4]uint8{255, 0, 255, 255} {
		t.Fatalf("the shaft under the centre drew %v", c)
	}
}

// An edit re-uploads only the sections it touched. What that draws has to be
// exactly what uploading the whole edited terrain again draws — in both views.
func TestAnEditedTerrainDrawsLikeAFullRebuild(t *testing.T) {
	for _, smooth := range []bool{false, true} {
		tr := world.NewTerrain(mgl32.Vec3{-40, -4, -40}, 1, 80, 8, 80)
		tr.Generate(world.TerrainFill{Height: 4, Hills: 2, HillSize: 10, Top: 1, Under: 2, Deep: 2})
		tr.Layers = [world.TerrainLayerCount]string{"grass", "dirt"}
		tr.Smooth = smooth
		tr.Touch()
		p := newTerrainProbe(t, tr)
		p.frame()

		for x := 37; x <= 43; x++ {
			tr.SetBlock(x, 3, 40, 0)
			tr.SetBlock(x, 4, 38, 2)
		}
		tr.SetBlock(34, 2, 40, 0)
		partial := p.frame()

		ReleaseTerrainMeshes()
		tr.Touch()
		full := p.frame()
		for i := range full {
			if partial[i] != full[i] {
				t.Fatalf("smooth=%v: pixel %d differs: partial upload %d, full rebuild %d", smooth, i/4, partial[i], full[i])
			}
		}
	}
}

// Painting a biome grows something: the same view with the ground painted
// draws different pixels, the shadow pass draws its casters without a GL
// error, and the foliage goes when the paint does.
func TestAPaintedBiomeGrowsFoliage(t *testing.T) {
	tr := world.NewTerrain(mgl32.Vec3{-16, -2, -16}, 1, 32, 12, 32)
	tr.Layers[0] = "dirt"
	tr.Generate(world.TerrainFill{Height: 2, Top: 1, Under: 1, Deep: 1})
	p := newTerrainProbe(t, tr)
	bare := p.frame()

	tr.Biomes = []world.TerrainBiome{{Preset: "forest"}, {Preset: "meadow"}}
	for z := 0; z < 32; z++ {
		for x := 0; x < 32; x++ {
			slot := uint8(2)
			if x < 16 {
				slot = 1
			}
			tr.SetPaint(tr.FaceKey(x, 1, z, world.FaceYPos), slot)
		}
	}
	grown := p.frame()
	g := foliage.cache[tr]
	if g == nil {
		t.Fatal("the painted terrain has no foliage")
	}
	count := 0
	for _, s := range g.sections {
		count += s.count
	}
	if count < 1000 {
		t.Fatalf("a 32x32 painted field grew only %d plants", count)
	}
	differ := 0
	for i := range bare {
		if bare[i] != grown[i] {
			differ++
		}
	}
	if differ < len(bare)/20 {
		t.Fatalf("the painted field draws almost like the bare one (%d bytes differ)", differ)
	}

	RenderFoliageDirShadow(p.lvl, mgl32.Ortho(-20, 20, -20, 20, -50, 50).Mul4(mgl32.LookAtV(mgl32.Vec3{5, 10, 5}, mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})), 0, nil)
	if e := gl.GetError(); e != gl.NO_ERROR {
		t.Fatalf("GL error 0x%x drawing foliage shadows", e)
	}

	tr.Paint = nil
	tr.Touch()
	p.frame()
	for _, s := range foliage.cache[tr].sections {
		if s.count != 0 {
			t.Fatal("foliage stayed after the paint was cleared")
		}
	}
}

// A biome that names a ground texture lays it over the ground it is painted
// on — even a terrain with no textures of its own, which is otherwise drawn
// in its normal colour — and taking the paint away takes the texture with it.
func TestAPaintedBiomeShowsItsGroundTexture(t *testing.T) {
	world.PutFoliagePreset(world.FoliagePreset{Name: "test_sand", Label: "Sand", GroundTexture: "ground_desert",
		GroundTint: mgl32.Vec3{0.8, 0.65, 0.45}})
	t.Cleanup(func() { world.LoadBiomes(t.TempDir()) })
	tr := flatTerrain()
	tr.Biomes = []world.TerrainBiome{{Preset: "test_sand"}}
	for z := 0; z < 8; z++ {
		for x := 0; x < 8; x++ {
			tr.SetPaint(tr.FaceKey(x, 0, z, world.FaceYPos), 1)
		}
	}
	p := newTerrainProbe(t, tr)
	c := centre(p.frame())
	if len(ground.names) != 1 || ground.names[0] != "ground_desert" || ground.missing["ground_desert"] {
		t.Fatalf("the ground array holds %v (missing %v)", ground.names, ground.missing)
	}
	// Sand is warm: red well over blue, and nothing like the normal colour's
	// (0.5, 1, 0.5).
	if !(int(c[0]) > int(c[2])+40 && c[1] < 230) {
		t.Fatalf("the painted ground is %v, not sand", c)
	}

	tr.Paint = nil
	tr.Touch()
	c = centre(p.frame())
	nearlyByte(t, "green", c[1], 1.0)
}
