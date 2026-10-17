package render

// gi_thick_gl_test.go — live GI on the faces of big, thick things.
//
// Phase 2 of the canyon work (docs/canyon/PHASE_2.md, "Known gaps") found
// black patches on large cooked rocks — hoodoos, cliff chunks, the Poly Haven
// cliffs — with GI on, gone with it off, on an uncoloured textured cook too.
// docs/canyon/PHASE_6.md has the hunt and the cause.
//
// These tests shade points over every face of a thick block through the
// shading's own giIrradiance (light_model.glsl, compiled into a compute
// probe), as a brush and as a cooked mesh's distance grid, and ask that no
// point the probes answer for (cover) comes back dark: a face in shadow sees
// the sky and the lit ground, so its GI is never near black.
//
//	FOUNDRY_GL_TEST=1 go test -run GIThick ./internal/render/

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/meshsdf"
	"foundry/internal/nanite"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// giShadeProbeSrc evaluates giIrradiance at a list of points.
const giShadeProbeSrc = `
layout(local_size_x = 64) in;
layout(std430, binding = 0) readonly buffer Pts { vec4 pts[]; };
layout(std430, binding = 1) readonly buffer Nrm { vec4 nrm[]; };
layout(std430, binding = 2) writeonly buffer Res { vec4 res[]; };
uniform int count;
void main() {
    int i = int(gl_GlobalInvocationID.x);
    if (i >= count) {
        return;
    }
    float cover;
    vec3 irr = giIrradiance(pts[i].xyz, nrm[i].xyz, cover);
    res[i] = vec4(irr, cover);
}
`

// giShadeSample is one point to shade: where, facing, and the eye it is seen
// from (the shading offsets its lookup toward the viewer).
type giShadeSample struct {
	p, n, eye mgl32.Vec3
}

// giShade runs the probe over samples against the GI state UpdateGI left,
// returning irradiance and cover for each.
func giShade(t *testing.T, samples []giShadeSample) ([]mgl32.Vec3, []float32) {
	t.Helper()
	src := "#version 430\n" + mustReadShader(fileutil.ShaderPath("light_model.glsl")) + "\n" + giShadeProbeSrc
	pu, err := giComputeProgram(src)
	if err != nil {
		t.Fatalf("shade probe: %v", err)
	}
	defer gl.DeleteProgram(pu.prog)

	// Grouped by eye: giCamPos is one uniform.
	irr := make([]mgl32.Vec3, len(samples))
	cover := make([]float32, len(samples))
	byEye := map[mgl32.Vec3][]int{}
	var eyes []mgl32.Vec3
	for i, s := range samples {
		if _, ok := byEye[s.eye]; !ok {
			eyes = append(eyes, s.eye)
		}
		byEye[s.eye] = append(byEye[s.eye], i)
	}
	var bufs [3]uint32
	gl.GenBuffers(3, &bufs[0])
	defer gl.DeleteBuffers(3, &bufs[0])
	for _, eye := range eyes {
		idx := byEye[eye]
		pts := make([]float32, 4*len(idx))
		nrm := make([]float32, 4*len(idx))
		for k, i := range idx {
			copy(pts[4*k:], samples[i].p[:])
			copy(nrm[4*k:], samples[i].n[:])
		}
		out := make([]float32, 4*len(idx))
		for b, data := range [][]float32{pts, nrm, out} {
			gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, bufs[b])
			gl.BufferData(gl.SHADER_STORAGE_BUFFER, 4*len(data), gl.Ptr(&data[0]), gl.DYNAMIC_COPY)
			gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, uint32(b), bufs[b])
		}
		gi.uploadProbeUniformsTo(&pu)
		gl.Uniform3f(pu.U("giCamPos"), eye[0], eye[1], eye[2])
		gl.Uniform1i(pu.U("count"), int32(len(idx)))
		gl.DispatchCompute(uint32((len(idx)+63)/64), 1, 1)
		gl.MemoryBarrier(gl.BUFFER_UPDATE_BARRIER_BIT | gl.SHADER_STORAGE_BARRIER_BIT)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, bufs[2])
		gl.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, 4*len(out), gl.Ptr(&out[0]))
		for k, i := range idx {
			irr[i] = mgl32.Vec3{out[4*k], out[4*k+1], out[4*k+2]}
			cover[i] = out[4*k+3]
		}
	}
	for b := 0; b < 3; b++ {
		gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, uint32(b), 0)
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	return irr, cover
}

// giBlockFaces samples the four sides and the top of the axis box lo..hi at
// step, each a hair off its face and seen from dist out along its normal.
func giBlockFaces(lo, hi mgl32.Vec3, step, dist float32) []giShadeSample {
	var out []giShadeSample
	type face struct {
		n    mgl32.Vec3
		u, v int // the two axes the face spans
	}
	faces := []face{
		{mgl32.Vec3{1, 0, 0}, 2, 1}, {mgl32.Vec3{-1, 0, 0}, 2, 1},
		{mgl32.Vec3{0, 0, 1}, 0, 1}, {mgl32.Vec3{0, 0, -1}, 0, 1},
		{mgl32.Vec3{0, 1, 0}, 0, 2},
	}
	centre := lo.Add(hi).Mul(0.5)
	for _, f := range faces {
		for a := lo[f.u] + step/2; a < hi[f.u]; a += step {
			for b := lo[f.v] + step/2; b < hi[f.v]; b += step {
				var p mgl32.Vec3
				for k := 0; k < 3; k++ {
					switch {
					case k == f.u:
						p[k] = a
					case k == f.v:
						p[k] = b
					case f.n[k] > 0:
						p[k] = hi[k] + 0.01
					default:
						p[k] = lo[k] - 0.01
					}
				}
				eye := centre.Add(f.n.Mul(hi.Sub(lo).Len()/2 + dist))
				eye[1] = max(eye[1], 1.7)
				out = append(out, giShadeSample{p: p, n: f.n, eye: eye})
			}
		}
	}
	return out
}

// giDarkReport counts the samples the probes answered for (cover over a half)
// whose irradiance is under floor, and draws them for a failure message.
func giDarkReport(samples []giShadeSample, irr []mgl32.Vec3, cover []float32, floor float32) (int, int, float32, string) {
	dark, answered := 0, 0
	minLum := float32(math.MaxFloat32)
	var b strings.Builder
	for i, s := range samples {
		if cover[i] < 0.5 {
			continue
		}
		answered++
		lum := 0.2126*irr[i][0] + 0.7152*irr[i][1] + 0.0722*irr[i][2]
		minLum = min(minLum, lum)
		if lum < floor {
			dark++
			if dark <= 12 {
				fmt.Fprintf(&b, "\n  at %v facing %v: irradiance %v, cover %.2f", s.p, s.n, irr[i], cover[i])
			}
		}
	}
	return dark, answered, minLum, b.String()
}

// giThickLevel is open desert: a ground slab, a sun, sky ambient — and in it
// whatever the test adds.
func giThickLevel() *world.Level {
	ground := world.NewBoxBrush("ground", mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{60, 0.5, 60}, "grey")
	lvl := &world.Level{Chunks: []world.Chunk{{ID: "ground", Brushes: []world.Brush{ground}}}}
	lvl.Lights = world.LightSet{
		Global: world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 0.45, Hemisphere: 0.5, Wrap: 0.5},
		Directional: []world.DirectionalLight{{
			ID: "sun", Direction: mgl32.Vec3{0.55, -0.5, 0.67}.Normalize(),
			Color: mgl32.Vec3{1, 0.95, 0.85}, Brightness: 1, CastShadows: true,
		}},
	}
	lvl.Lights.Global.GI = world.DefaultGISettings()
	return lvl
}

// giBoxTris is the closed box lo..hi as a triangle list, outward wound.
func giBoxTris(lo, hi mgl32.Vec3) []mgl32.Vec3 {
	c := func(i int) mgl32.Vec3 {
		p := lo
		if i&1 != 0 {
			p[0] = hi[0]
		}
		if i&2 != 0 {
			p[1] = hi[1]
		}
		if i&4 != 0 {
			p[2] = hi[2]
		}
		return p
	}
	quads := [][4]int{{0, 2, 3, 1}, {4, 5, 7, 6}, {0, 1, 5, 4}, {2, 6, 7, 3}, {0, 4, 6, 2}, {1, 3, 7, 5}}
	var tris []mgl32.Vec3
	for _, q := range quads {
		tris = append(tris, c(q[0]), c(q[1]), c(q[2]), c(q[0]), c(q[2]), c(q[3]))
	}
	return tris
}

// giThickCases are the blocks every test below is run on: a hull brush, and
// the same block as a cooked mesh's distance grid at the resolution the cook
// would give it.
func giThickCases(lo, hi mgl32.Vec3) []struct {
	name string
	add  func(*world.Level)
} {
	return []struct {
		name string
		add  func(*world.Level)
	}{
		{"brush", func(l *world.Level) {
			b := world.NewBoxBrush("block", lo.Add(hi).Mul(0.5), hi.Sub(lo).Mul(0.5), "grey")
			l.Chunks = append(l.Chunks, world.Chunk{ID: "block", Brushes: []world.Brush{b}})
		}},
		{"mesh", func(l *world.Level) {
			ext := hi.Sub(lo)
			res := min(max(int(math.Ceil(float64(max(ext[0], ext[1], ext[2])/0.15)))+5, 24), 64)
			m := &nanite.Mesh{SDF: meshsdf.Bake(giBoxTris(lo, hi), res)}
			l.Scenery = append(l.Scenery, world.Scenery{ID: "block", Model: "block.fnm", Nanite: m})
		}},
	}
}

func TestGIThickBlockFacesAreNeverBlack(t *testing.T) {
	for _, size := range []struct {
		name   string
		lo, hi mgl32.Vec3
	}{
		{"hoodoo 3x7x3", mgl32.Vec3{-1.5, 0, -1.5}, mgl32.Vec3{1.5, 7, 1.5}},
		{"cliff 20x7x7", mgl32.Vec3{-10, 0, -3.5}, mgl32.Vec3{10, 7, 3.5}},
	} {
		for _, c := range giThickCases(size.lo, size.hi) {
			t.Run(size.name+"/"+c.name, func(t *testing.T) {
				giTestSetup(t, gfx.GIMedium, map[string]mgl32.Vec3{"grey": {0.55, 0.4, 0.3}})
				lvl := giThickLevel()
				c.add(lvl)
				// From off to the side and up, as a player stands looking at it.
				giRunFrames(t, lvl, mgl32.Vec3{14, 3, 14}, 40)
				samples := giBlockFaces(size.lo, size.hi, 0.25, 6)
				irr, cover := giShade(t, samples)
				dark, answered, minLum, where := giDarkReport(samples, irr, cover, 0.03)
				t.Logf("%d of %d answered points dark; least irradiance %.3f", dark, answered, minLum)
				if answered < len(samples)/2 {
					t.Fatalf("the probes answered for %d of %d points", answered, len(samples))
				}
				if dark > 0 {
					t.Errorf("%d of %d points on the block's faces are black under GI:%s", dark, answered, where)
				}
			})
		}
	}
}

// TestGIThickCookedMeshes shades the faces of real cooked meshes, named by
// FOUNDRY_GI_FNM (comma-separated .fnm paths), placed on the desert floor: a
// diagnostic for big scans and generated hoodoos, which the repo does not ship.
func TestGIThickCookedMeshes(t *testing.T) {
	paths := os.Getenv("FOUNDRY_GI_FNM")
	if paths == "" {
		t.Skip("FOUNDRY_GI_FNM names the cooked meshes to look at")
	}
	for _, path := range strings.Split(paths, ",") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			giTestSetup(t, gfx.GIMedium, map[string]mgl32.Vec3{})
			m, err := nanite.Read(path)
			if err != nil {
				t.Fatal(err)
			}
			if m.SDF == nil {
				t.Fatal("no SDF")
			}
			lvl := giThickLevel()
			lvl.Scenery = append(lvl.Scenery, world.Scenery{ID: "mesh", Model: path, Nanite: m})
			ext := m.Hi.Sub(m.Lo)
			cam := m.Center.Add(mgl32.Vec3{ext.Len(), 3, ext.Len()})
			giRunFrames(t, lvl, cam, 40)
			var samples []giShadeSample
			fv, fn := m.FallbackVertices, m.FallbackNormals
			for i := 0; i+2 < len(m.Fallback); i += 3 {
				a, b, c := m.Fallback[i], m.Fallback[i+1], m.Fallback[i+2]
				pa := mgl32.Vec3{fv[5*a], fv[5*a+1], fv[5*a+2]}
				pb := mgl32.Vec3{fv[5*b], fv[5*b+1], fv[5*b+2]}
				pc := mgl32.Vec3{fv[5*c], fv[5*c+1], fv[5*c+2]}
				n := pb.Sub(pa).Cross(pc.Sub(pa))
				if n.Len() < 1e-9 {
					continue
				}
				n = n.Normalize()
				_ = fn
				p := pa.Add(pb).Add(pc).Mul(1.0 / 3).Add(n.Mul(0.01))
				if p[1] < 0.3 {
					continue // under the floor or at its foot
				}
				eye := p.Add(n.Mul(6))
				eye = mgl32.Vec3{float32(math.Round(float64(eye[0]))), max(float32(math.Round(float64(eye[1]))), 1.7), float32(math.Round(float64(eye[2])))}
				samples = append(samples, giShadeSample{p: p, n: n, eye: eye})
			}
			irr, cover := giShade(t, samples)
			dark, answered, minLum, where := giDarkReport(samples, irr, cover, 0.03)
			t.Logf("%s: SDF %d³ cell %.3f; %d of %d answered points dark; least irradiance %.3f",
				path, m.SDF.N, m.SDF.Cell, dark, answered, minLum)
			if dark > 0 {
				t.Errorf("%d points black:%s", dark, where)
			}
		})
	}
}

// giRuggedTris is a closed slab of rough rock lo..hi: its top a heightfield of
// knobs and ledges between hi.Y-relief and hi.Y, sides and a flat bottom.
// What a scanned cliff's top is like, and a box is not: every probe of a
// coarse cascade above it is within half a spacing of some knob.
func giRuggedTris(lo, hi mgl32.Vec3, relief float32, cells int) []mgl32.Vec3 {
	h := func(i, j int) float32 {
		if i <= 0 || j <= 0 || i >= cells || j >= cells {
			return hi[1] - relief
		}
		// Knobs on a 3-cell lattice, with a ragged field between.
		u := float64(i) / 3 * math.Pi
		v := float64(j) / 3 * math.Pi
		k := 0.5 + 0.5*math.Sin(u)*math.Sin(v)
		k = k * k * k
		r := 0.5 + 0.5*math.Sin(float64(i*7+j*13))
		return hi[1] - relief + relief*float32(0.85*k+0.15*r)
	}
	at := func(i, j int, y float32) mgl32.Vec3 {
		return mgl32.Vec3{lo[0] + (hi[0]-lo[0])*float32(i)/float32(cells), y, lo[2] + (hi[2]-lo[2])*float32(j)/float32(cells)}
	}
	var tris []mgl32.Vec3
	quad := func(a, b, c, d mgl32.Vec3) { tris = append(tris, a, b, c, a, c, d) }
	for i := 0; i < cells; i++ {
		for j := 0; j < cells; j++ {
			quad(at(i, j, h(i, j)), at(i, j+1, h(i, j+1)), at(i+1, j+1, h(i+1, j+1)), at(i+1, j, h(i+1, j)))
			quad(at(i, j, lo[1]), at(i+1, j, lo[1]), at(i+1, j+1, lo[1]), at(i, j+1, lo[1]))
		}
	}
	for k := 0; k < cells; k++ {
		quad(at(k, 0, lo[1]), at(k, 0, h(k, 0)), at(k+1, 0, h(k+1, 0)), at(k+1, 0, lo[1]))
		quad(at(k+1, cells, lo[1]), at(k+1, cells, h(k+1, cells)), at(k, cells, h(k, cells)), at(k, cells, lo[1]))
		quad(at(0, k+1, lo[1]), at(0, k+1, h(0, k+1)), at(0, k, h(0, k)), at(0, k, lo[1]))
		quad(at(cells, k, lo[1]), at(cells, k, h(cells, k)), at(cells, k+1, h(cells, k+1)), at(cells, k+1, lo[1]))
	}
	return tris
}

// The regression for the black patches: the top of a rugged slab, as a cooked
// mesh's distance grid, from far enough off that the coarser cascades answer
// for it. Before the fix every probe of some cells was refused by its
// nearest-surface plane, and the cascade still claimed full cover with
// nothing: irradiance exactly zero.
func TestGIThickRuggedTopIsNeverBlack(t *testing.T) {
	giTestSetup(t, gfx.GIMedium, map[string]mgl32.Vec3{"grey": {0.55, 0.4, 0.3}})
	lo, hi := mgl32.Vec3{-12, 0, -12}, mgl32.Vec3{12, 7, 12}
	tris := giRuggedTris(lo, hi, 3, 48)
	lvl := giThickLevel()
	lvl.Scenery = append(lvl.Scenery, world.Scenery{ID: "slab", Model: "slab.fnm",
		Nanite: &nanite.Mesh{SDF: meshsdf.Bake(tris, 64)}})
	giRunFrames(t, lvl, mgl32.Vec3{34, 6, 34}, 40)

	var samples []giShadeSample
	for i := 0; i+2 < len(tris); i += 3 {
		a, b, c := tris[i], tris[i+1], tris[i+2]
		n := b.Sub(a).Cross(c.Sub(a))
		if n.Len() < 1e-9 || n.Normalize()[1] < 0.3 {
			continue // the top only
		}
		n = n.Normalize()
		p := a.Add(b).Add(c).Mul(1.0 / 3).Add(n.Mul(0.01))
		samples = append(samples, giShadeSample{p: p, n: n, eye: mgl32.Vec3{p[0] + 4, hi[1] + 4, p[2] + 4}})
	}
	irr, cover := giShade(t, samples)
	dark, answered, minLum, where := giDarkReport(samples, irr, cover, 0.03)
	t.Logf("%d of %d answered points dark; least irradiance %.3f", dark, answered, minLum)
	if answered < len(samples)/2 {
		t.Fatalf("the probes answered for %d of %d points", answered, len(samples))
	}
	if dark > 0 {
		t.Errorf("%d of %d points on the rugged top are black under GI:%s", dark, answered, where)
	}
}
