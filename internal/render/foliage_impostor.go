package render

// foliage_impostor.go — the far tier of trees, shrubs and rocks.
//
// Past its far distance a tree is drawn as one quad: an impostor, showing the
// tree as it looks from the nearest of impGrid x impGrid directions over the
// upper hemisphere. The directions are laid out hemi-octahedrally — the
// hemisphere folded flat onto a square — so the three baked views around any
// view direction are neighbours in the grid, and the vertex stage blends
// those three by how near each is (foliage_imp_vert.glsl). Each view is
// projected onto the quad through its own basis, so turning around a tree
// slides from one view into the next rather than switching.
//
// The views are baked here, once, from each variant's near mesh, when the
// renderer starts: what a tree looks like is whatever foliage_mesh.go builds,
// so a new shape needs nothing drawn by hand. They hold what the mesh's
// fragments would have, not the lit colour: how bright the texel is (tone,
// times the leaf texture), how much of it takes the instance's accent colour
// and how much is bark, its normal in the plant's own frame and whether it is
// thin (a leaf, lit from both sides). The impostor is lit per pixel from
// those with the scene's own lights, so it agrees with the meshes around it
// under any sun, and each instance still wears its own colours.
//
// Baked at impSuper times the resolution kept, and averaged down weighted by
// coverage, so a leaf's edge is antialiased and transparent texels do not
// pull its colour toward black. The mips are built per view — a 2x2 never
// straddles two views — with their alpha rescaled so a crown keeps its
// density with distance (the atlas's reason: foliage_atlas.go).

import (
	"log"
	"math"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

const (
	// impGrid is how many views there are along each side of the grid.
	impGrid = 8
	// impCell is how many texels each view keeps, on a side.
	impCell = 48
	// impSuper is how much finer the bake is than what is kept.
	impSuper = 2
	// impMips is how many mip levels are kept: down to a 3-texel view.
	impMips = 5
)

// Texture units, clear of the lighting's (0-15).
const (
	impAlbedoUnit = 16
	impNormalUnit = 17
)

// foliageImpostors is the baked views.
type foliageImpostors struct {
	// tex are two texture arrays, one layer per impostor: brightness,
	// accent, bark and coverage; and normal (plant frame) and thinness.
	tex    [2]uint32
	layers int
	// layer is, per bucket, its layer (-1: none); centre its bounding
	// sphere in the plant frame at unit scale, radius in w.
	layer  []int32
	centre []mgl32.Vec4
}

func (im *foliageImpostors) bind(p *progUniforms) {
	gl.Uniform1f(p.U("impGrid"), impGrid)
	gl.Uniform1i(p.U("impAlbedo"), impAlbedoUnit)
	gl.Uniform1i(p.U("impNormal"), impNormalUnit)
	gl.ActiveTexture(gl.TEXTURE0 + impAlbedoUnit)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, im.tex[0])
	gl.ActiveTexture(gl.TEXTURE0 + impNormalUnit)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, im.tex[1])
	gl.ActiveTexture(gl.TEXTURE0)
}

// impostorQuad is the impostors' card: corners at (±1, ±1) in x, y, which
// the vertex stage turns to face the camera.
func impostorQuad() []float32 {
	m := &fmesh{}
	c := func(x, y float32) fvert {
		return fvert{p: mgl32.Vec3{x, y, 0}, n: mgl32.Vec3{0, 0, 1}, uv: mgl32.Vec2{(x + 1) / 2, (y + 1) / 2}, tone: 1}
	}
	m.quad(c(-1, -1), c(1, -1), c(1, 1), c(-1, 1))
	return m.v
}

// hemiOctDecode is the view direction at grid point uv in [0,1]^2 — the same
// function foliage_imp_vert.glsl decodes with.
func hemiOctDecode(uv mgl32.Vec2) mgl32.Vec3 {
	x := uv.Mul(2).Sub(mgl32.Vec2{1, 1})
	tx, ty := (x[0]+x[1])*0.5, (x[0]-x[1])*0.5
	d := mgl32.Vec3{tx, 1 - float32(math.Abs(float64(tx))) - float32(math.Abs(float64(ty))), ty}
	return d.Normalize()
}

// impFrameBasis is the image basis of the view looking back along dir.
func impFrameBasis(dir mgl32.Vec3) (right, up mgl32.Vec3) {
	ref := mgl32.Vec3{0, 1, 0}
	if math.Abs(float64(dir.Y())) > 0.999 {
		ref = mgl32.Vec3{0, 0, -1}
	}
	right = ref.Cross(dir).Normalize()
	return right, dir.Cross(right)
}

// impostorBounds is the sphere a template fits in: its box's centre and the
// furthest vertex from it.
func impostorBounds(tris []float32) mgl32.Vec4 {
	inf := float32(math.Inf(1))
	lo, hi := mgl32.Vec3{inf, inf, inf}, mgl32.Vec3{-inf, -inf, -inf}
	for i := 0; i+2 < len(tris); i += foliageVertexFloats {
		for k := 0; k < 3; k++ {
			lo[k] = min(lo[k], tris[i+k])
			hi[k] = max(hi[k], tris[i+k])
		}
	}
	c := lo.Add(hi).Mul(0.5)
	var r float32
	for i := 0; i+2 < len(tris); i += foliageVertexFloats {
		r = max(r, mgl32.Vec3{tris[i], tris[i+1], tris[i+2]}.Sub(c).Len())
	}
	return mgl32.Vec4{c[0], c[1], c[2], max(r*1.02, 0.01)}
}

// bakeFoliageImpostors renders every impostor shape's variants and uploads
// the views. The template buffers and the atlas must already exist.
func bakeFoliageImpostors() foliageImpostors {
	im := foliageImpostors{layer: make([]int32, foliage.buckets), centre: make([]mgl32.Vec4, foliage.buckets)}
	for i := range im.layer {
		im.layer[i] = -1
	}
	type job struct {
		s      world.FoliageShape
		v      int
		bucket int
	}
	var jobs []job
	for s := world.FoliageShape(0); s < world.FoliageShapeCount; s++ {
		if !foliageShapes[s].impostor {
			continue
		}
		for v := 0; v < world.FoliageVariants[s]; v++ {
			b := foliage.bucket(s, v)
			im.layer[b] = int32(len(jobs))
			im.centre[b] = impostorBounds(buildFoliageTemplate(s, v, 0))
			jobs = append(jobs, job{s, v, b})
		}
	}
	im.layers = len(jobs)
	if im.layers == 0 {
		return im
	}

	prog, err := gfx.CompileProgramFromFiles(biomeShaderPath("foliage_imp_bake_vert.glsl"), biomeShaderPath("foliage_imp_bake_frag.glsl"))
	if err != nil {
		log.Printf("foliage: impostor bake program: %v — trees stop at their far mesh", err)
		return im
	}
	defer gl.DeleteProgram(prog)
	pu := progUniforms{prog: prog, loc: map[string]int32{}}

	big := int32(impGrid * impCell * impSuper)
	var fbo, rb uint32
	var att [2]uint32
	gl.GenFramebuffers(1, &fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.GenTextures(2, &att[0])
	for i, t := range att {
		gl.BindTexture(gl.TEXTURE_2D, t)
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, big, big, 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0+uint32(i), gl.TEXTURE_2D, t, 0)
	}
	gl.GenRenderbuffers(1, &rb)
	gl.BindRenderbuffer(gl.RENDERBUFFER, rb)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.DEPTH_COMPONENT24, big, big)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.RENDERBUFFER, rb)
	bufs := []uint32{gl.COLOR_ATTACHMENT0, gl.COLOR_ATTACHMENT1}
	gl.DrawBuffers(2, &bufs[0])
	defer func() {
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		gl.DeleteFramebuffers(1, &fbo)
		gl.DeleteRenderbuffers(1, &rb)
		gl.DeleteTextures(2, &att[0])
	}()
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("foliage: impostor bake framebuffer incomplete (0x%x) — trees stop at their far mesh", st)
		return im
	}

	// A VAO of the template attributes alone: the drawing VAO has the
	// instance attributes enabled, with nothing behind them yet.
	var vao uint32
	gl.GenVertexArrays(1, &vao)
	gl.BindVertexArray(vao)
	gl.BindBuffer(gl.ARRAY_BUFFER, foliage.vbo)
	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, foliage.ebo)
	bindTemplateAttributes()
	defer func() {
		gl.BindVertexArray(0)
		gl.DeleteVertexArrays(1, &vao)
	}()

	var prevViewport [4]int32
	gl.GetIntegerv(gl.VIEWPORT, &prevViewport[0])
	blend := gl.IsEnabled(gl.BLEND)
	cull := gl.IsEnabled(gl.CULL_FACE)
	gl.Disable(gl.BLEND)
	gl.Disable(gl.CULL_FACE)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthMask(true)
	gl.UseProgram(prog)
	gl.Uniform1i(pu.U("atlas"), 0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, foliage.atlas)

	side := impGrid * impCell
	levels := make([][2][]uint8, impMips)
	for l := range levels {
		n := (side >> l) * (side >> l) * 4 * im.layers
		levels[l] = [2][]uint8{make([]uint8, n), make([]uint8, n)}
	}
	pix := [2][]uint8{make([]uint8, int(big)*int(big)*4), make([]uint8, int(big)*int(big)*4)}
	for li, j := range jobs {
		gl.ClearColor(0, 0, 0, 0)
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
		sph := im.centre[j.bucket]
		c := mgl32.Vec3{sph[0], sph[1], sph[2]}
		r := sph[3]
		t := foliage.templates[j.s][j.v][0]
		for gy := 0; gy < impGrid; gy++ {
			for gx := 0; gx < impGrid; gx++ {
				dir := hemiOctDecode(mgl32.Vec2{float32(gx) / (impGrid - 1), float32(gy) / (impGrid - 1)})
				right, up := impFrameBasis(dir)
				m := mgl32.Mat4FromRows(
					mgl32.Vec4{right[0] / r, right[1] / r, right[2] / r, -right.Dot(c) / r},
					mgl32.Vec4{up[0] / r, up[1] / r, up[2] / r, -up.Dot(c) / r},
					mgl32.Vec4{-dir[0] / r, -dir[1] / r, -dir[2] / r, dir.Dot(c) / r},
					mgl32.Vec4{0, 0, 0, 1},
				)
				gl.UniformMatrix4fv(pu.U("viewProj"), 1, false, &m[0])
				cs := int32(impCell * impSuper)
				gl.Viewport(int32(gx)*cs, int32(gy)*cs, cs, cs)
				gl.DrawElementsBaseVertex(gl.TRIANGLES, t.count, gl.UNSIGNED_INT, gl.PtrOffset(int(t.first)*4), t.base)
			}
		}
		for a := 0; a < 2; a++ {
			gl.ReadBuffer(gl.COLOR_ATTACHMENT0 + uint32(a))
			gl.ReadPixels(0, 0, big, big, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&pix[a][0]))
		}
		views := impostorResolve(pix, int(big))
		for l, lv := range views {
			n := (side >> l) * (side >> l) * 4
			copy(levels[l][0][li*n:], lv[0])
			copy(levels[l][1][li*n:], lv[1])
		}
	}
	gl.ReadBuffer(gl.COLOR_ATTACHMENT0)
	gl.Viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3])
	if blend {
		gl.Enable(gl.BLEND)
	}
	if cull {
		gl.Enable(gl.CULL_FACE)
	}

	gl.GenTextures(2, &im.tex[0])
	for a := 0; a < 2; a++ {
		gl.BindTexture(gl.TEXTURE_2D_ARRAY, im.tex[a])
		for l := 0; l < impMips; l++ {
			s := int32(side >> l)
			gl.TexImage3D(gl.TEXTURE_2D_ARRAY, int32(l), gl.RGBA8, s, s, int32(im.layers), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&levels[l][a][0]))
		}
		gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MAX_LEVEL, impMips-1)
		gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
		gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
		gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
		gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	}
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)
	return im
}

// impostorResolve turns one supersampled bake (both attachments, big x big)
// into the kept mip chain: level l's pair of RGBA8 images.
func impostorResolve(pix [2][]uint8, big int) [][2][]uint8 {
	side := big / impSuper
	// Level 0: coverage-weighted average of each impSuper x impSuper block.
	ch := [2][]float32{make([]float32, side*side*4), make([]float32, side*side*4)}
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			var acc [2][4]float32
			var cov float32
			for dy := 0; dy < impSuper; dy++ {
				for dx := 0; dx < impSuper; dx++ {
					i := ((y*impSuper+dy)*big + x*impSuper + dx) * 4
					a := float32(pix[0][i+3]) / 255
					cov += a
					for k := 0; k < 4; k++ {
						acc[0][k] += float32(pix[0][i+k]) / 255 * a
						acc[1][k] += float32(pix[1][i+k]) / 255 * a
					}
				}
			}
			o := (y*side + x) * 4
			n := float32(impSuper * impSuper)
			for a := 0; a < 2; a++ {
				for k := 0; k < 4; k++ {
					if cov > 0 {
						ch[a][o+k] = acc[a][k] / cov
					}
				}
			}
			ch[0][o+3] = cov / n
		}
	}
	impostorBleed(ch, side)
	coverage := impostorCoverage(ch[0], 1)

	var out [][2][]uint8
	w := side
	for l := 0; l < impMips; l++ {
		out = append(out, [2][]uint8{toBytes(ch[0]), toBytes(ch[1])})
		if l == impMips-1 {
			break
		}
		nw := w / 2
		nc := [2][]float32{make([]float32, nw*nw*4), make([]float32, nw*nw*4)}
		for y := 0; y < nw; y++ {
			for x := 0; x < nw; x++ {
				var acc [2][4]float32
				var cov, wsum float32
				for dy := 0; dy < 2; dy++ {
					for dx := 0; dx < 2; dx++ {
						i := ((y*2+dy)*w + x*2 + dx) * 4
						a := ch[0][i+3]
						cov += a
						// Colour weighted by coverage, a little floor so a
						// fully clear block keeps its bled colour.
						wt := a + 1e-3
						wsum += wt
						for k := 0; k < 4; k++ {
							acc[0][k] += ch[0][i+k] * wt
							acc[1][k] += ch[1][i+k] * wt
						}
					}
				}
				o := (y*nw + x) * 4
				for a := 0; a < 2; a++ {
					for k := 0; k < 4; k++ {
						nc[a][o+k] = acc[a][k] / wsum
					}
				}
				nc[0][o+3] = cov / 4
			}
		}
		// Rescale alpha so as many texels pass one half as at full size.
		lo, hi := float32(0.5), float32(8)
		for it := 0; it < 16; it++ {
			mid := (lo + hi) / 2
			if impostorCoverage(nc[0], mid) < coverage {
				lo = mid
			} else {
				hi = mid
			}
		}
		s := (lo + hi) / 2
		for i := 3; i < len(nc[0]); i += 4 {
			nc[0][i] = min(nc[0][i]*s, 1)
		}
		ch, w = nc, nw
	}
	return out
}

func impostorCoverage(rgba []float32, scale float32) float32 {
	n := 0
	for i := 3; i < len(rgba); i += 4 {
		if rgba[i]*scale >= 0.5 {
			n++
		}
	}
	return float32(n) / float32(len(rgba)/4)
}

// impostorBleed spreads covered texels' channels into the clear texels
// around them, within each view, so filtering at a silhouette does not pull
// in black or a flat normal.
func impostorBleed(ch [2][]float32, side int) {
	known := make([]bool, side*side)
	for i := range known {
		known[i] = ch[0][i*4+3] > 0.01
	}
	for pass := 0; pass < 4; pass++ {
		next := append([]bool(nil), known...)
		for y := 0; y < side; y++ {
			for x := 0; x < side; x++ {
				i := y*side + x
				if known[i] {
					continue
				}
				var acc [2][3]float32
				c := 0
				for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, ny := x+d[0], y+d[1]
					if nx < 0 || ny < 0 || nx >= side || ny >= side || nx/impCell != x/impCell || ny/impCell != y/impCell || !known[ny*side+nx] {
						continue
					}
					j := (ny*side + nx) * 4
					for k := 0; k < 3; k++ {
						acc[0][k] += ch[0][j+k]
						acc[1][k] += ch[1][j+k]
					}
					c++
				}
				if c > 0 {
					for k := 0; k < 3; k++ {
						ch[0][i*4+k] = acc[0][k] / float32(c)
						ch[1][i*4+k] = acc[1][k] / float32(c)
					}
					next[i] = true
				}
			}
		}
		known = next
	}
}

func toBytes(f []float32) []uint8 {
	b := make([]uint8, len(f))
	for i, v := range f {
		b[i] = uint8(min(max(v, 0), 1)*255 + 0.5)
	}
	return b
}
