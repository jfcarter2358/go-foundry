package stages

// rocks_dump_gl_test.go — pictures of rock variants through the engine's
// world program (the material editor's lookdev rig, the vertex colours and
// cavity, smooth normals), for judging the presets by eye. Off unless asked:
//
//	FOUNDRY_GL_TEST=1 FOUNDRY_ROCK_DUMP=/tmp/rocks [FOUNDRY_ROCK_PRESETS=boulder,pillar] \
//	  [FOUNDRY_ROCK_VARIANTS=3] [FOUNDRY_ROCK_TRIS=0] [FOUNDRY_ROCK_LITHOLOGY=granite] \
//	  [FOUNDRY_ROCK_PALETTE=navajo] go test -run TestRockDumpGL -timeout 30m ./internal/procgen/stages/
//
// Or a generator's own stage, the variants as its run makes them:
//
//	FOUNDRY_ROCK_FGEN=path/to/x.fgen FOUNDRY_ROCK_STAGE=pillars ...
//
// Each variant is drawn from three sides and from above into one PNG per
// variant, <dir>/<preset>_<variant>.png.

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/glctx"
	"foundry/internal/gltest"
	"foundry/internal/procgen"
	"foundry/internal/procgen/rocks"
	"foundry/internal/render"
	"foundry/internal/testenv"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

func TestRockDumpGL(t *testing.T) {
	dir := os.Getenv("FOUNDRY_ROCK_DUMP")
	if dir == "" {
		t.Skip("FOUNDRY_ROCK_DUMP not set")
	}
	gltest.Context(t)
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = filepath.Join(testenv.RepoRoot(t), "resources")
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	sh := fileutil.ShaderPath
	if err := render.InitShadows(sh("shadow_vert.glsl"), sh("shadow_frag.glsl"), sh("shadow_cube_vert.glsl"), sh("shadow_cube_frag.glsl")); err != nil {
		t.Fatal(err)
	}
	prog, err := gfx.CompileWorldProgram(sh("vertex.glsl"), sh("fragment.glsl"))
	if err != nil {
		t.Fatal(err)
	}
	gfx.ForgetUniformLocations(prog)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := rocks.PresetNames()
	if v := os.Getenv("FOUNDRY_ROCK_PRESETS"); v != "" {
		names = strings.Split(v, ",")
	}
	variants := 2
	if v, err := strconv.Atoi(os.Getenv("FOUNDRY_ROCK_VARIANTS")); err == nil && v > 0 {
		variants = v
	}
	tris, _ := strconv.Atoi(os.Getenv("FOUNDRY_ROCK_TRIS"))
	tex := os.Getenv("FOUNDRY_ROCK_TEXTURE")
	if tex != "" {
		gfx.TextureNames = gfx.ScanTextures(filepath.Join(testenv.RepoRoot(t), "resources", "textures"))
		if err := gfx.EnsureTextureLoaded(tex); err != nil {
			t.Fatal(err)
		}
	}
	// A generator's stage instead: its settings and seeds.
	var fromDoc func(vi int) (rocks.Settings, uint64)
	if path := os.Getenv("FOUNDRY_ROCK_FGEN"); path != "" {
		d, err := procgen.ReadDoc(path)
		if err != nil {
			t.Fatal(err)
		}
		st := d.StageByID(os.Getenv("FOUNDRY_ROCK_STAGE"))
		if st == nil {
			t.Fatalf("no stage %q", os.Getenv("FOUNDRY_ROCK_STAGE"))
		}
		k := procgen.KindByName(st.Kind)
		p := procgen.Resolve(k.Params, st.Params)
		env := procgen.Env{Params: p, Seed: procgen.StageSeed(d.Seed, st.ID, k.Params, p)}
		names = []string{p.Str("preset")}
		fromDoc = func(vi int) (rocks.Settings, uint64) { return rocksSettings(&env), variantSeed(env.Seed, vi) }
	}
	for _, name := range names {
		for vi := 0; vi < variants; vi++ {
			s := rocks.Defaults()
			s.Preset = name
			s.Triangles = tris
			if l := os.Getenv("FOUNDRY_ROCK_LITHOLOGY"); l != "" {
				s.Lithology = l
			}
			if p := os.Getenv("FOUNDRY_ROCK_PALETTE"); p != "" {
				s.Palette = p
			}
			seed := variantSeed(1234, vi)
			if fromDoc != nil {
				s, seed = fromDoc(vi)
			}
			r, err := rocks.Generate(s, seed)
			if err != nil {
				t.Fatalf("%s %d: %v", name, vi, err)
			}
			m := r.Mesh
			verts := make([][]float32, len(m.Positions))
			normals := make([][]float32, len(m.Positions))
			colors := make([][]float32, len(m.Positions))
			for i, p := range m.Positions {
				verts[i] = []float32{p[0], p[1], p[2], m.UVs[i][0], m.UVs[i][1]}
				normals[i] = []float32{m.Normals[i][0], m.Normals[i][1], m.Normals[i][2]}
				c := m.Colors[i]
				colors[i] = []float32{float32(c[0]) / 255, float32(c[1]) / 255, float32(c[2]) / 255, float32(c[3]) / 255}
			}
			idx := make([]int, len(m.Indices))
			for i, x := range m.Indices {
				idx[i] = int(x)
			}
			g := gfx.Geometry{Alpha: 1, Smooth: true, VertexNormals: normals, VertexColors: colors}
			if err := g.Load(verts, idx, tex, mgl32.Vec3{}); err != nil {
				t.Fatal(err)
			}
			for gl.GetError() != gl.NO_ERROR {
			}
			lo, hi := mgl32.Vec3(r.Meta.Min), mgl32.Vec3(r.Meta.Max)
			centre := lo.Add(hi).Mul(0.5)
			dist := hi.Sub(lo).Len() * 0.95
			const size = 480
			views := []struct{ yaw, pitch float32 }{{35, -15}, {155, -10}, {275, -25}, {35, -80}}
			sheet := image.NewNRGBA(image.Rect(0, 0, size*len(views), size))
			for k, v := range views {
				dir := gfx.DirectionFromAngles(mgl32.DegToRad(v.yaw), mgl32.DegToRad(v.pitch))
				eye := centre.Sub(dir.Mul(dist))
				view := mgl32.LookAtV(eye, centre, mgl32.Vec3{0, 1, 0})
				proj := mgl32.Perspective(mgl32.DegToRad(50), 1, dist*0.02, dist*10)
				target, err := glctx.NewTarget(size, size)
				if err != nil {
					t.Fatal(err)
				}
				target.Bind()
				gl.Viewport(0, 0, size, size)
				gl.ClearColor(0.42, 0.47, 0.53, 1)
				gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
				gl.Enable(gl.DEPTH_TEST)
				render.PrepareWorldProgramForPreview(prog, view, proj, eye, 0)
				g.DrawThroughProgram(prog, mgl32.Ident4())
				gl.Finish()
				img := target.Image()
				target.Unbind()
				target.Delete()
				draw.Draw(sheet, image.Rect(k*size, 0, (k+1)*size, size), img, image.Point{}, draw.Src)
			}
			g.Unload()
			out := filepath.Join(dir, fmt.Sprintf("%s_%d.png", name, vi))
			f, err := os.Create(out)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, sheet); err != nil {
				t.Fatal(err)
			}
			f.Close()
			t.Logf("%s: %d tris (meshed %d at %.4f m), mesh %v simplify %v bake %v total %v, extent %.3v",
				out, m.TriangleCount(), r.Timings.MeshTriangles, r.Timings.Cell, r.Timings.Mesh, r.Timings.Simplify,
				r.Timings.Bake, r.Timings.Total, r.Meta.Extent)
			_ = math.Pi
		}
	}
}
