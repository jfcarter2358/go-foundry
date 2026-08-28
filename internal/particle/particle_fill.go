package particle

// particle_fill.go — turning live particles into draw instances.
//
// The renderer owns the GPU buffer and the batching; this produces the records
// that go into it. Kept here rather than in internal/render because everything
// it reads — the ramps, the sheet, the tint — is document state, and because the
// particle editor's preview fills the same records from the same code.

import (
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// Instance is one quad's worth of draw state. Deliberately the same shape as
// the renderer's own instance record; render.SpriteInstance is built from it.
type Instance struct {
	Pos   mgl32.Vec3
	Size  mgl32.Vec2
	Rot   float32
	Color mgl32.Vec4
	UV    mgl32.Vec4 // u0, v0, du, dv
}

// Fill writes one instance per live particle into dst and returns how many it
// wrote, capped by len(dst). Nothing is allocated.
//
// sortBackToFront orders the instances by distance from camPos, which the alpha
// blend mode needs and the additive and cutout modes do not — sorting is the
// caller's decision because only it knows which pass this is for.
func (s *System) Fill(dst []Instance, camPos mgl32.Vec3, sortBackToFront bool) int {
	l := &s.Layer
	n := 0
	for i := range s.Live {
		if n >= len(dst) {
			break
		}
		p := &s.Live[i]
		t := p.LifeT()

		size := p.Size * l.SizeRamp.At(t)
		if size <= 0 {
			// A ramp that pinches to nothing is an author saying "gone"; a
			// zero-area quad still costs a draw and a vertex fetch.
			continue
		}
		c := l.ColorRamp.At(t)
		u0, v0, du, dv := s.cell(p, t)

		dst[n] = Instance{
			Pos:   p.Pos,
			Size:  mgl32.Vec2{size, size},
			Rot:   p.Rot,
			Color: mgl32.Vec4{c.X() * l.Tint.X(), c.Y() * l.Tint.Y(), c.Z() * l.Tint.Z(), l.AlphaRamp.At(t)},
			UV:    mgl32.Vec4{u0, v0, du, dv},
		}
		n++
	}

	if sortBackToFront && n > 1 {
		out := dst[:n]
		sort.Slice(out, func(a, b int) bool {
			return out[a].Pos.Sub(camPos).LenSqr() > out[b].Pos.Sub(camPos).LenSqr()
		})
	}
	return n
}

// cell picks the sheet rectangle for one particle. The whole texture when no
// sheet is authored, which is the common case.
func (s *System) cell(p *Particle, t float32) (u0, v0, du, dv float32) {
	l := &s.Layer
	if l.SheetCols <= 0 || l.SheetRows <= 0 {
		return 0, 0, 1, 1
	}
	total := l.SheetCols * l.SheetRows
	var f int
	switch l.FrameMode {
	case FrameRandom:
		// Fixed at birth, from the particle's own seed — one sheet of four
		// smoke puffs is what stops a cloud looking like one puff repeated.
		f = int(p.Seed % uint32(total))
	case FrameStatic:
		f = 0
	default: // FrameOverLife
		f = int(t * float32(total))
		if f >= total {
			f = total - 1
		}
	}
	du = 1 / float32(l.SheetCols)
	dv = 1 / float32(l.SheetRows)
	return float32(f%l.SheetCols) * du, float32(f/l.SheetCols) * dv, du, dv
}

// Bounds returns a world-space box holding every live particle, for culling.
// ok is false when nothing is alive.
//
// Widened by each particle's current size, because the box has to hold the
// quads rather than their centres — a large particle whose centre is just off
// screen still covers part of it.
func (s *System) Bounds() (min, max mgl32.Vec3, ok bool) {
	l := &s.Layer
	for i := range s.Live {
		p := &s.Live[i]
		r := p.Size * l.SizeRamp.At(p.LifeT()) * 0.5
		lo := mgl32.Vec3{p.Pos.X() - r, p.Pos.Y() - r, p.Pos.Z() - r}
		hi := mgl32.Vec3{p.Pos.X() + r, p.Pos.Y() + r, p.Pos.Z() + r}
		if !ok {
			min, max, ok = lo, hi, true
			continue
		}
		min = mgl32.Vec3{minF(min.X(), lo.X()), minF(min.Y(), lo.Y()), minF(min.Z(), lo.Z())}
		max = mgl32.Vec3{maxF(max.X(), hi.X()), maxF(max.Y(), hi.Y()), maxF(max.Z(), hi.Z())}
	}
	return min, max, ok
}

func minF(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
