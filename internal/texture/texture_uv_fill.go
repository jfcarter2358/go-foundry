package texture

// texture_uv_fill.go — "Fill UV": paint the regions of the image that the faces
// selected in a viewport sample.
//
// The loops come from whatever wears the texture (the texture editor's
// UVSelectionForTexture), in the same UV space its overlay draws in: (0,0) is
// the image's top-left corner and (1,1) its bottom-right.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// FillUVIslands writes idx into every texel of the active layer that the given
// UV loops cover, as one undo entry, and reports whether anything changed.
//
// Coverage is conservative: a texel counts when its centre is inside a loop or
// a loop's edge passes through it. Centres alone would leave the island's rim
// in the old colour, and the texture's filtering samples that rim along every
// seam of the face. The unwrap's gutter is what keeps the extra texel from
// landing on a neighbouring island.
func (e *TexEditorState) FillUVIslands(loops [][]mgl32.Vec2, idx uint8) bool {
	layer := e.activeLayer()
	if layer == nil || len(loops) == 0 {
		return false
	}
	e.BeginStroke()
	for _, loop := range loops {
		e.fillUVLoop(loop, idx)
	}
	changed := false
	for i, v := range e.strokeBefore {
		if layer.Idx[i] != v {
			changed = true
			break
		}
	}
	e.EndStroke()
	return changed
}

// fillUVLoop rasterises one loop onto the active layer.
func (e *TexEditorState) fillUVLoop(loop []mgl32.Vec2, idx uint8) {
	if len(loop) == 0 {
		return
	}
	w, h := float32(e.Doc.W), float32(e.Doc.H)
	pts := make([]mgl32.Vec2, len(loop))
	lo := mgl32.Vec2{float32(math.Inf(1)), float32(math.Inf(1))}
	hi := mgl32.Vec2{float32(math.Inf(-1)), float32(math.Inf(-1))}
	for i, p := range loop {
		pts[i] = mgl32.Vec2{p.X() * w, p.Y() * h}
		lo = mgl32.Vec2{min(lo.X(), pts[i].X()), min(lo.Y(), pts[i].Y())}
		hi = mgl32.Vec2{max(hi.X(), pts[i].X()), max(hi.Y(), pts[i].Y())}
	}

	// Interior: every texel whose centre is inside, by even-odd crossing.
	x0 := max(int(math.Floor(float64(lo.X()))), 0)
	y0 := max(int(math.Floor(float64(lo.Y()))), 0)
	x1 := min(int(math.Ceil(float64(hi.X()))), e.Doc.W-1)
	y1 := min(int(math.Ceil(float64(hi.Y()))), e.Doc.H-1)
	if len(pts) >= 3 {
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				if pointInLoop(pts, float32(x)+0.5, float32(y)+0.5) {
					e.setTexel(x, y, idx)
				}
			}
		}
	}

	// Rim: every texel an edge passes through, sampled finely enough (a quarter
	// texel) that a shallow edge cannot step over one.
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		steps := int(math.Ceil(float64(b.Sub(a).Len())*4)) + 1
		for s := 0; s <= steps; s++ {
			p := a.Add(b.Sub(a).Mul(float32(s) / float32(steps)))
			e.setTexel(int(math.Floor(float64(p.X()))), int(math.Floor(float64(p.Y()))), idx)
		}
	}
}

// pointInLoop is the even-odd rule: does a ray from (x, y) towards +X cross the
// loop an odd number of times. Winding-independent, which matters because an
// unwrap can lay a face down either way round.
func pointInLoop(pts []mgl32.Vec2, x, y float32) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		a, b := pts[i], pts[j]
		if (a.Y() > y) != (b.Y() > y) &&
			x < (b.X()-a.X())*(y-a.Y())/(b.Y()-a.Y())+a.X() {
			in = !in
		}
	}
	return in
}
