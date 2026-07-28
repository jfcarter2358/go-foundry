package main

// worldgen_integration_test.go — end-to-end test that drives GenerateCity with a
// real OpenGL context (so sector geometry is actually uploaded), then verifies the
// save→reload→regenerate cycle. Skips gracefully when no display/GL is available.

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// A single shared SDL/GL context is created once and reused across all GL-backed
// tests: SDL/GL cannot be re-initialised per test, and a GL context is thread-
// affine, so each test rebinds the shared context to its own OS thread.
var (
	glInitOnce sync.Once
	glWin      *sdl.Window
	glCtx      sdl.GLContext
	glInitErr  string
)

func glTestContext(t *testing.T) func() {
	t.Helper()
	runtime.LockOSThread()

	glInitOnce.Do(func() {
		if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
			glInitErr = "no SDL video: " + err.Error()
			return
		}
		win, err := sdl.CreateWindow("worldgen-test", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
			320, 240, sdl.WINDOW_OPENGL|sdl.WINDOW_HIDDEN)
		if err != nil {
			glInitErr = "no window: " + err.Error()
			return
		}
		ctx, err := win.GLCreateContext()
		if err != nil {
			glInitErr = "no GL context: " + err.Error()
			return
		}
		if err := gl.Init(); err != nil {
			glInitErr = "gl.Init failed: " + err.Error()
			return
		}
		glWin, glCtx = win, ctx
		Window, Context = win, ctx

		dist, _ := filepath.Abs("dist")
		executablePath = dist
		if err := LoadProgram(filepath.Join(dist, vertexShaderPath), filepath.Join(dist, fragmentShaderPath)); err != nil {
			glInitErr = "LoadProgram failed (need built dist/): " + err.Error()
			return
		}
		gl.UseProgram(Program)
		AlphaUniform = gl.GetUniformLocation(Program, gl.Str("alpha\x00"))
		textureNames = ScanTextures(filepath.Join(dist, "textures"))
	})

	if glInitErr != "" {
		t.Skip(glInitErr)
	}
	if glWin == nil {
		t.Skip("GL context unavailable")
	}
	// Bind the shared context to this test's (locked) OS thread.
	if err := glWin.GLMakeCurrent(glCtx); err != nil {
		t.Skipf("GLMakeCurrent: %v", err)
	}
	return func() {}
}

func paintTestCity(g *WorldGrid) {
	// A winding road (steps + a diagonal turn) and a city block beside it.
	for cy := 2; cy < g.Rows-2; cy++ {
		g.CellType[g.cIdx(6, cy)] = TerrainRoad
	}
	for cx := 6; cx < 11; cx++ {
		g.CellType[g.cIdx(cx, g.Rows-3)] = TerrainRoad
	}
	for cy := 3; cy < g.Rows-4; cy++ {
		for cx := 7; cx < 13; cx++ {
			if g.CellType[g.cIdx(cx, cy)] != TerrainRoad {
				g.CellCity[g.cIdx(cx, cy)] = 1
			}
		}
	}
	// Some height variation so the flush-on-slope path is exercised.
	for vy := 0; vy <= g.Rows; vy++ {
		for vx := 0; vx <= g.Cols; vx++ {
			g.Heights[g.vIdx(vx, vy)] = 0.5 * float32(vx) * 0.2
		}
	}
}

func countRegions(ch *Chunk) (interior, band, road, total int) {
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		total++
		switch s.Floor.Texture {
		case "stone_floor":
			// road surface OR interior floor (both use stone_floor in desert)
			if s.Ceiling.Enabled {
				interior++
			} else {
				road++
			}
		}
		// band sectors have an enabled ceiling and a wall/roof texture floor
		if s.Ceiling.Enabled && s.Floor.Texture != "stone_floor" {
			band++
		}
	}
	return
}

func TestPaletteCityAndPreviewEndToEnd(t *testing.T) {
	cleanup := glTestContext(t)
	defer cleanup()

	g := NewWorldGrid(16, 16, 6.0, mgl32.Vec2{-48, -48}, 4242, "world")
	paintPaletteCity(g)
	g.Density = 0.7
	g.FaceRoads = true
	g.Palette = testPalette()
	chunk := g.BakeChunk()

	lvl := &Level{Chunks: []Chunk{chunk}}
	ch := &lvl.Chunks[0]
	names := GenerateCity(ch, g, resolveBuildingStyle("desert"))
	ensureTexturesLoaded(names)

	interior, band, _, total := countRegions(ch)
	t.Logf("palette city: sectors=%d interior=%d band=%d", total, interior, band)
	if interior == 0 || band == 0 {
		t.Fatalf("palette city carved no building: interior=%d band=%d", interior, band)
	}

	// Building preview carve for template B.
	pv, pnames := BuildBuildingPreview(g.Palette[1])
	ensureTexturesLoaded(pnames)
	pi, pb, _, ptot := countRegions(pv)
	t.Logf("preview: sectors=%d interior=%d band=%d points=%d", ptot, pi, pb, len(pv.Points))
	if pi == 0 || pb == 0 || len(pv.Points) == 0 {
		t.Fatalf("preview carve incomplete: interior=%d band=%d points=%d", pi, pb, len(pv.Points))
	}

	// Persist palette + density + face-roads through save/reload.
	tmp := filepath.Join(t.TempDir(), "palette_world.yaml")
	lvl.Spatial = BuildSpatialHash(lvl.Chunks, 8.0)
	if err := SaveLevel(lvl, tmp); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	reload, err := LoadLevel(tmp)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	rg := reload.Chunks[0].WorldGrid
	if rg == nil {
		t.Fatal("reloaded chunk lost its WorldGrid")
	}
	if len(rg.Palette) != 2 {
		t.Fatalf("palette not persisted: got %d templates", len(rg.Palette))
	}
	if !approxf(rg.Density, 0.7, 1e-3) || !rg.FaceRoads {
		t.Fatalf("density/faceRoads not persisted: density=%v faceRoads=%v", rg.Density, rg.FaceRoads)
	}
	if rg.Palette[1].Width != 10 || rg.Palette[1].Stories != 3 || rg.Palette[1].WallTex != "brick" {
		t.Fatalf("template fields not persisted: %+v", rg.Palette[1])
	}
	_ = os.Remove(tmp)

	// The procedural (palette-free) path shares the same GL context/thread — a GL
	// context can't move threads on this driver, so it runs here, not as its own
	// top-level test.
	proceduralCityEndToEnd(t)
	diagonalRoadEndToEnd(t)
}

// diagonalRoadEndToEnd carves a free-hand diagonal road stroke and confirms it
// produces road geometry that survives save/reload.
func diagonalRoadEndToEnd(t *testing.T) {
	g := NewWorldGrid(20, 20, 6.0, mgl32.Vec2{-60, -60}, 5, "world")
	g.Roads = []RoadStroke{{
		Points: []mgl32.Vec2{{-40, -40}, {-15, -18}, {0, 0}, {18, 15}, {40, 40}},
		Width:  4,
	}}
	chunk := g.BakeChunk()
	lvl := &Level{Chunks: []Chunk{chunk}}
	ch := &lvl.Chunks[0]
	names := GenerateCity(ch, g, resolveBuildingStyle("desert"))
	ensureTexturesLoaded(names)

	_, _, road, total := countRegions(ch)
	t.Logf("diagonal road: sectors=%d road=%d", total, road)
	if road == 0 {
		t.Fatal("diagonal road stroke carved no road sectors")
	}

	tmp := filepath.Join(t.TempDir(), "road_world.yaml")
	lvl.Spatial = BuildSpatialHash(lvl.Chunks, 8.0)
	if err := SaveLevel(lvl, tmp); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	reload, err := LoadLevel(tmp)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	rg := reload.Chunks[0].WorldGrid
	if rg == nil || len(rg.Roads) != 1 || len(rg.Roads[0].Points) != 5 {
		t.Fatalf("road stroke not persisted: %+v", rg)
	}
	_ = os.Remove(tmp)
}

func proceduralCityEndToEnd(t *testing.T) {
	g := NewWorldGrid(16, 16, 6.0, mgl32.Vec2{-48, -48}, 4242, "world")
	paintTestCity(g)
	chunk := g.BakeChunk()

	lvl := &Level{Chunks: []Chunk{chunk}}
	ch := &lvl.Chunks[0]

	style := resolveBuildingStyle("desert")
	names := GenerateCity(ch, g, style)
	ensureTexturesLoaded(names)

	if len(ch.Sectors) == 0 {
		t.Fatal("GenerateCity produced no sectors")
	}
	interior, band, _, total := countRegions(ch)
	t.Logf("sectors=%d interior=%d band=%d points=%d", total, interior, band, len(ch.Points))
	if interior == 0 {
		t.Fatal("no interior (enterable room) sectors were carved")
	}
	if band == 0 {
		t.Fatal("no wall-band sectors were carved (walls have no thickness)")
	}

	// Every floor must have exactly one VertexOffset per polygon vertex and a
	// real (loaded) geometry buffer — proves GPU upload succeeded.
	uploaded := 0
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if len(s.Floor.VertexOffsets) != len(s.PointIndices) {
			t.Fatalf("sector %q: %d offsets for %d points", s.ID, len(s.Floor.VertexOffsets), len(s.PointIndices))
		}
		if s.Floor.Geometry.Vao != 0 {
			uploaded++
		}
	}
	if uploaded == 0 {
		t.Fatal("no floor geometry was uploaded to the GPU")
	}

	// Determinism: regenerating with the same seed yields the same sector count.
	n1 := len(ch.Sectors)
	GenerateCity(ch, g, style)
	if len(ch.Sectors) != n1 {
		t.Fatalf("non-deterministic regen: %d then %d sectors", n1, len(ch.Sectors))
	}

	// Persistence: save, reload, and confirm the carved geometry + grid survive.
	tmp := filepath.Join(t.TempDir(), "world_test.yaml")
	lvl.Spatial = BuildSpatialHash(lvl.Chunks, 8.0)
	if err := SaveLevel(lvl, tmp); err != nil {
		t.Fatalf("SaveLevel: %v", err)
	}
	reload, err := LoadLevel(tmp)
	if err != nil {
		t.Fatalf("LoadLevel: %v", err)
	}
	if len(reload.Chunks) != 1 {
		t.Fatalf("reload chunk count = %d", len(reload.Chunks))
	}
	rch := &reload.Chunks[0]
	if rch.WorldGrid == nil {
		t.Fatal("reloaded chunk lost its WorldGrid")
	}
	if len(rch.Sectors) != n1 {
		t.Fatalf("reload lost geometry: %d sectors, want %d", len(rch.Sectors), n1)
	}
	ri, rb, _, _ := countRegions(rch)
	if ri == 0 || rb == 0 {
		t.Fatalf("reload lost building geometry: interior=%d band=%d", ri, rb)
	}
	_ = os.Remove(tmp)
}
