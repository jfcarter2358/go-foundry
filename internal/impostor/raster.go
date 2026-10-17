package impostor

// raster.go — the bake's views: the cooked mesh's full detail rasterised on
// the CPU, orthographically, onto one card at a time.
//
// A z-buffer rasteriser, as the GPU's would be for the same draw: each
// triangle's pixels whose centres it covers (either winding — the engine
// draws models two-sided), its texel's alpha tested against the cutout
// (0.5) BEFORE the depth test, so a leaf's clear texels let the leaves behind
// show as they do on screen, then the nearest along the card's normal kept.

import (
	"image"
	"image/draw"
	"math"

	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

// cutout is the alpha a texel must reach to be surface (fragment.glsl's
// default cutout level).
const cutout = 0.5

// sampler is a slot's texture, decoded to straight (non-premultiplied) RGBA.
type sampler struct {
	w, h int
	pix  []uint8
}

func newSampler(img image.Image) *sampler {
	n, ok := img.(*image.NRGBA)
	if !ok || n.Stride != n.Rect.Dx()*4 || n.Rect.Min != (image.Point{}) {
		n = image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
		draw.Draw(n, n.Bounds(), img, img.Bounds().Min, draw.Src)
	}
	return &sampler{w: n.Rect.Dx(), h: n.Rect.Dy(), pix: n.Pix}
}

// at is the bilinear sample at uv (wrapping), channels in 0..1.
func (s *sampler) at(uv mgl32.Vec2) [4]float32 {
	if s == nil || s.w == 0 || s.h == 0 {
		return [4]float32{0.6, 0.6, 0.6, 1}
	}
	x := float64(uv[0])*float64(s.w) - 0.5
	y := float64(uv[1])*float64(s.h) - 0.5
	x0, y0 := math.Floor(x), math.Floor(y)
	fx, fy := float32(x-x0), float32(y-y0)
	wrap := func(v, n int) int {
		v %= n
		if v < 0 {
			v += n
		}
		return v
	}
	var out [4]float32
	for k := 0; k < 4; k++ {
		dx, dy := k&1, k>>1
		px, py := wrap(int(x0)+dx, s.w), wrap(int(y0)+dy, s.h)
		wx := 1 - fx
		if dx == 1 {
			wx = fx
		}
		wy := 1 - fy
		if dy == 1 {
			wy = fy
		}
		o := 4 * (py*s.w + px)
		for c := 0; c < 4; c++ {
			out[c] += float32(s.pix[o+c]) / 255 * wx * wy
		}
	}
	return out
}

// source is the mesh's full detail, ready to rasterise.
type source struct {
	pos    []mgl32.Vec3
	uv     []mgl32.Vec2
	nor    []mgl32.Vec3
	col    [][4]float32 // nil without vertex colours
	tris   [][3]uint32
	slot   []int
	smooth []bool
	tex    []*sampler
	lo, hi mgl32.Vec3
}

// sourceTriangles gathers m's level 0 and its slots' textures.
func sourceTriangles(m *nanite.Mesh, tex Textures) (*source, error) {
	s := &source{}
	nv := len(m.Vertices) / 5
	for v := 0; v < nv; v++ {
		s.pos = append(s.pos, mgl32.Vec3{m.Vertices[5*v], m.Vertices[5*v+1], m.Vertices[5*v+2]})
		s.uv = append(s.uv, mgl32.Vec2{m.Vertices[5*v+3], m.Vertices[5*v+4]})
		if len(m.Normals) >= 3*(v+1) {
			s.nor = append(s.nor, mgl32.Vec3{m.Normals[3*v], m.Normals[3*v+1], m.Normals[3*v+2]})
		} else {
			s.nor = append(s.nor, mgl32.Vec3{})
		}
	}
	if m.HasColors && len(m.Colors) == nv {
		for _, c := range m.Colors {
			s.col = append(s.col, [4]float32{float32(c&0xff) / 255, float32(c>>8&0xff) / 255, float32(c>>16&0xff) / 255, float32(c>>24) / 255})
		}
	}
	first := true
	for si := range m.Slots {
		var smp *sampler
		if name := m.Slots[si].Texture; name != "" && tex != nil {
			if img, err := tex(name); err == nil && img != nil {
				smp = newSampler(img)
			}
		}
		s.tex = append(s.tex, smp)
		s.smooth = append(s.smooth, m.Slots[si].Smooth)
		f, n := m.SlotTriangles(si)
		for t := f; t < f+n; t++ {
			if int(3*t+2) >= len(m.Indices) {
				break
			}
			tri := [3]uint32{m.Indices[3*t], m.Indices[3*t+1], m.Indices[3*t+2]}
			if int(max(tri[0], tri[1], tri[2])) >= nv {
				continue
			}
			s.tris = append(s.tris, tri)
			s.slot = append(s.slot, si)
			for _, i := range tri {
				p := s.pos[i]
				if first {
					s.lo, s.hi, first = p, p, false
				}
				s.lo = mgl32.Vec3{min(s.lo[0], p[0]), min(s.lo[1], p[1]), min(s.lo[2], p[2])}
				s.hi = mgl32.Vec3{max(s.hi[0], p[0]), max(s.hi[1], p[1]), max(s.hi[2], p[2])}
			}
		}
	}
	return s, nil
}

// view is one card's supersampled raster: per pixel, whether anything was
// drawn, its colour and its (unit, mesh-frame) normal.
type view struct {
	size  int
	hit   []bool
	col   []mgl32.Vec3
	nor   []mgl32.Vec3
	depth []float32
}

// render rasterises the mesh onto card cd at size x size.
func (s *source) render(cd card, size int) *view {
	v := &view{size: size, hit: make([]bool, size*size), col: make([]mgl32.Vec3, size*size),
		nor: make([]mgl32.Vec3, size*size), depth: make([]float32, size*size)}
	for i := range v.depth {
		v.depth[i] = float32(math.Inf(-1))
	}
	rl, dl := cd.right.LenSqr(), cd.down.LenSqr()
	fs := float32(size)
	proj := func(p mgl32.Vec3) (x, y, z float32) {
		d := p.Sub(cd.origin)
		return d.Dot(cd.right) / rl * fs, d.Dot(cd.down) / dl * fs, d.Dot(cd.n)
	}
	for ti, tri := range s.tris {
		var px, py, pz [3]float32
		for k := 0; k < 3; k++ {
			px[k], py[k], pz[k] = proj(s.pos[tri[k]])
		}
		area := (px[1]-px[0])*(py[2]-py[0]) - (px[2]-px[0])*(py[1]-py[0])
		if area == 0 || math.IsNaN(float64(area)) {
			continue
		}
		x0 := max(int(math.Floor(float64(min(px[0], px[1], px[2])))), 0)
		x1 := min(int(math.Ceil(float64(max(px[0], px[1], px[2])))), size-1)
		y0 := max(int(math.Floor(float64(min(py[0], py[1], py[2])))), 0)
		y1 := min(int(math.Ceil(float64(max(py[0], py[1], py[2])))), size-1)
		if x0 > x1 || y0 > y1 {
			continue
		}
		a, b, c := tri[0], tri[1], tri[2]
		// The face as the mesh winds it, and whether the card sees its back
		// (the shader turns a back face's normal round).
		face := s.pos[b].Sub(s.pos[a]).Cross(s.pos[c].Sub(s.pos[a]))
		back := face.Dot(cd.n) < 0
		if fl := face.Len(); fl > 0 {
			face = face.Mul(1 / fl)
		}
		smp := s.tex[s.slot[ti]]
		smooth := s.smooth[s.slot[ti]]
		inv := 1 / area
		for y := y0; y <= y1; y++ {
			cy := float32(y) + 0.5
			for x := x0; x <= x1; x++ {
				cx := float32(x) + 0.5
				w0 := ((px[1]-cx)*(py[2]-cy) - (px[2]-cx)*(py[1]-cy)) * inv
				w1 := ((px[2]-cx)*(py[0]-cy) - (px[0]-cx)*(py[2]-cy)) * inv
				w2 := 1 - w0 - w1
				if w0 < 0 || w1 < 0 || w2 < 0 {
					continue
				}
				z := w0*pz[0] + w1*pz[1] + w2*pz[2]
				i := y*size + x
				if z <= v.depth[i] {
					continue
				}
				uv := s.uv[a].Mul(w0).Add(s.uv[b].Mul(w1)).Add(s.uv[c].Mul(w2))
				t := smp.at(uv)
				if t[3] < cutout {
					continue
				}
				col := mgl32.Vec3{t[0], t[1], t[2]}
				if s.col != nil {
					vc := [4]float32{}
					for k := 0; k < 4; k++ {
						vc[k] = s.col[a][k]*w0 + s.col[b][k]*w1 + s.col[c][k]*w2
					}
					// The tint, and the cavity half-way into the albedo: the
					// shader takes it off the light, which a card's own
					// lighting cannot know.
					ao := 0.5 + 0.5*vc[3]
					col = mgl32.Vec3{col[0] * vc[0] * ao, col[1] * vc[1] * ao, col[2] * vc[2] * ao}
				}
				n := face
				if smooth {
					sn := s.nor[a].Mul(w0).Add(s.nor[b].Mul(w1)).Add(s.nor[c].Mul(w2))
					if l := sn.Len(); l > 1e-6 {
						n = sn.Mul(1 / l)
					}
				}
				if back {
					n = n.Mul(-1)
				}
				v.depth[i], v.hit[i], v.col[i], v.nor[i] = z, true, col, n
			}
		}
	}
	return v
}
