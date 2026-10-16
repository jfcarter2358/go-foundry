package pdf

// rasterize.go — drawing a page of a parsed PDF into an image: the
// operators the writer uses (the graphics state q Q cm w J j, grey
// colours g G, paths m l c h re, fill f and stroke S, form XObjects Do),
// through golang.org/x/image/vector as the PNG backend draws, each fill
// and each stroke into its own coverage mask. An operator outside that set
// is an error, so a test drawing a page also proves the writer wrote
// nothing else. This is how `foundry score export-pdf -png` shows what is
// in a PDF, and how the tests compare a PDF page with the same page drawn
// straight from the display list.

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/vector"
)

// matrix is a PDF transformation [a b c d e f].
type matrix [6]float64

var identity = matrix{1, 0, 0, 1, 0, 0}

// mul is m then n (a point goes through m first).
func (m matrix) mul(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

func (m matrix) apply(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

// scale is how much the matrix scales a length (uniformly or on average).
func (m matrix) scale() float64 { return math.Sqrt(math.Abs(m[0]*m[3] - m[1]*m[2])) }

type gstate struct {
	ctm        matrix
	fill, line float64 // grey
	width      float64
}

type rasterizer struct {
	f    *File
	img  *image.RGBA
	z    vector.Rasterizer
	gs   gstate
	save []gstate
	// path is the current path, in device pixels: subpaths of points with
	// their kinds.
	path   []pathOp
	cx, cy float64 // current point, user space
	depth  int
}

type pathOp struct {
	op  byte // 'm', 'l', 'c', 'h'
	pts [3][2]float64
}

// Rasterize draws a page (0-based) at a scale in pixels per point.
func Rasterize(f *File, page int, pxPerPt float64) (*image.RGBA, error) {
	if page < 0 || page >= len(f.Pages) {
		return nil, fmt.Errorf("pdf: no page %d", page+1)
	}
	pd := f.Pages[page]
	w, h, err := f.MediaBox(pd)
	if err != nil {
		return nil, err
	}
	content, err := f.Contents(pd)
	if err != nil {
		return nil, err
	}
	iw, ih := int(math.Ceil(w*pxPerPt)), int(math.Ceil(h*pxPerPt))
	if iw <= 0 || ih <= 0 || iw*ih > 1<<28 {
		return nil, fmt.Errorf("pdf: page %gx%g at %g px a point is too large", w, h, pxPerPt)
	}
	r := &rasterizer{f: f, img: image.NewRGBA(image.Rect(0, 0, iw, ih))}
	draw.Draw(r.img, r.img.Bounds(), image.White, image.Point{}, draw.Src)
	// User space (points, y up) → pixels (y down).
	r.gs = gstate{ctm: matrix{pxPerPt, 0, 0, -pxPerPt, 0, h * pxPerPt}, width: 1}
	res := f.Dict(pd["Resources"])
	if err := r.run(content, res); err != nil {
		return nil, err
	}
	return r.img, nil
}

// run interprets a content stream.
func (r *rasterizer) run(content []byte, res Dict) error {
	r.depth++
	defer func() { r.depth-- }()
	if r.depth > 16 {
		return fmt.Errorf("pdf: forms nested too deep")
	}
	lx := &lexer{b: content}
	var args []Object
	nums := func(n int) ([]float64, error) {
		if len(args) < n {
			return nil, fmt.Errorf("pdf: operator needs %d operands, has %d", n, len(args))
		}
		out := make([]float64, n)
		for i, a := range args[len(args)-n:] {
			v, ok := a.(float64)
			if !ok {
				return nil, fmt.Errorf("pdf: operand %v is not a number", a)
			}
			out[i] = v
		}
		return out, nil
	}
	for {
		lx.space()
		if lx.i >= len(lx.b) {
			return nil
		}
		c := lx.b[lx.i]
		if c == '/' || c == '(' || c == '<' || c == '[' || c == '+' || c == '-' || c == '.' || c >= '0' && c <= '9' {
			o, err := lx.object()
			if err != nil {
				return err
			}
			args = append(args, o)
			continue
		}
		op := lx.token()
		if op == "" {
			return fmt.Errorf("pdf: stray %q in a content stream", c)
		}
		var err error
		switch op {
		case "q":
			r.save = append(r.save, r.gs)
		case "Q":
			if len(r.save) == 0 {
				return fmt.Errorf("pdf: Q without q")
			}
			r.gs, r.save = r.save[len(r.save)-1], r.save[:len(r.save)-1]
		case "cm":
			var v []float64
			if v, err = nums(6); err == nil {
				r.gs.ctm = matrix{v[0], v[1], v[2], v[3], v[4], v[5]}.mul(r.gs.ctm)
			}
		case "w":
			var v []float64
			if v, err = nums(1); err == nil {
				r.gs.width = v[0]
			}
		case "J", "j":
			_, err = nums(1)
		case "g":
			var v []float64
			if v, err = nums(1); err == nil {
				r.gs.fill = v[0]
			}
		case "G":
			var v []float64
			if v, err = nums(1); err == nil {
				r.gs.line = v[0]
			}
		case "m", "l":
			var v []float64
			if v, err = nums(2); err == nil {
				x, y := r.gs.ctm.apply(v[0], v[1])
				r.path = append(r.path, pathOp{op: op[0], pts: [3][2]float64{{x, y}}})
				r.cx, r.cy = v[0], v[1]
			}
		case "c":
			var v []float64
			if v, err = nums(6); err == nil {
				var p pathOp
				p.op = 'c'
				for k := 0; k < 3; k++ {
					p.pts[k][0], p.pts[k][1] = r.gs.ctm.apply(v[2*k], v[2*k+1])
				}
				r.path = append(r.path, p)
			}
		case "re":
			var v []float64
			if v, err = nums(4); err == nil {
				for k, q := range [][2]float64{{v[0], v[1]}, {v[0] + v[2], v[1]}, {v[0] + v[2], v[1] + v[3]}, {v[0], v[1] + v[3]}} {
					x, y := r.gs.ctm.apply(q[0], q[1])
					o := byte('l')
					if k == 0 {
						o = 'm'
					}
					r.path = append(r.path, pathOp{op: o, pts: [3][2]float64{{x, y}}})
				}
				r.path = append(r.path, pathOp{op: 'h'})
			}
		case "h":
			r.path = append(r.path, pathOp{op: 'h'})
		case "f", "F", "f*":
			r.fillPath(r.gs.fill)
			r.path = r.path[:0]
		case "S":
			r.strokePath()
			r.path = r.path[:0]
		case "n":
			r.path = r.path[:0]
		case "Do":
			if len(args) == 0 {
				return fmt.Errorf("pdf: Do with no name")
			}
			name, ok := args[len(args)-1].(Name)
			if !ok {
				return fmt.Errorf("pdf: Do of %v", args[len(args)-1])
			}
			err = r.form(name, res)
		default:
			return fmt.Errorf("pdf: operator %q is not one this package writes", op)
		}
		if err != nil {
			return err
		}
		args = args[:0]
	}
}

// form draws a form XObject.
func (r *rasterizer) form(name Name, res Dict) error {
	xo := r.f.Dict(res["XObject"])
	st, ok := r.f.Resolve(xo[name]).(*Stream)
	if !ok {
		return fmt.Errorf("pdf: no XObject %s", name)
	}
	if st.Dict["Subtype"] != Name("Form") {
		return fmt.Errorf("pdf: XObject %s is not a form", name)
	}
	saved := r.gs
	r.save = append(r.save, saved)
	if m, ok := r.f.Resolve(st.Dict["Matrix"]).(Array); ok && len(m) == 6 {
		var v matrix
		for i, o := range m {
			v[i], _ = r.f.Resolve(o).(float64)
		}
		r.gs.ctm = v.mul(r.gs.ctm)
	}
	inner := res
	if d := r.f.Dict(st.Dict["Resources"]); d != nil {
		inner = d
	}
	path := r.path
	r.path = nil
	err := r.run(st.Data, inner)
	r.path = path
	r.gs, r.save = r.save[len(r.save)-1], r.save[:len(r.save)-1]
	return err
}

func grey(g float64) color.RGBA {
	v := uint8(math.Round(math.Max(0, math.Min(1, g)) * 255))
	return color.RGBA{v, v, v, 255}
}

// bounds is the current path's box in pixels.
func (r *rasterizer) bounds(pad float64) (x0, y0, x1, y1 int, ok bool) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range r.path {
		n := 1
		if p.op == 'c' {
			n = 3
		}
		if p.op == 'h' {
			n = 0
		}
		for k := 0; k < n; k++ {
			minX, minY = math.Min(minX, p.pts[k][0]), math.Min(minY, p.pts[k][1])
			maxX, maxY = math.Max(maxX, p.pts[k][0]), math.Max(maxY, p.pts[k][1])
		}
	}
	if math.IsInf(minX, 1) {
		return 0, 0, 0, 0, false
	}
	b := r.img.Bounds()
	x0, y0 = max(int(math.Floor(minX-pad))-1, b.Min.X), max(int(math.Floor(minY-pad))-1, b.Min.Y)
	x1, y1 = min(int(math.Ceil(maxX+pad))+1, b.Max.X), min(int(math.Ceil(maxY+pad))+1, b.Max.Y)
	return x0, y0, x1, y1, x1 > x0 && y1 > y0
}

// fillPath fills the current path by the nonzero rule.
func (r *rasterizer) fillPath(g float64) {
	x0, y0, x1, y1, ok := r.bounds(0)
	if !ok {
		return
	}
	r.z.Reset(x1-x0, y1-y0)
	r.z.DrawOp = draw.Over
	dx, dy := float32(-x0), float32(-y0)
	open := false
	for _, p := range r.path {
		switch p.op {
		case 'm':
			if open {
				r.z.ClosePath()
			}
			r.z.MoveTo(float32(p.pts[0][0])+dx, float32(p.pts[0][1])+dy)
			open = true
		case 'l':
			r.z.LineTo(float32(p.pts[0][0])+dx, float32(p.pts[0][1])+dy)
		case 'c':
			r.z.CubeTo(float32(p.pts[0][0])+dx, float32(p.pts[0][1])+dy, float32(p.pts[1][0])+dx, float32(p.pts[1][1])+dy,
				float32(p.pts[2][0])+dx, float32(p.pts[2][1])+dy)
		case 'h':
			if open {
				r.z.ClosePath()
				open = false
			}
		}
	}
	if open {
		r.z.ClosePath()
	}
	r.z.Draw(r.img, image.Rect(x0, y0, x1, y1), image.NewUniform(grey(g)), image.Point{})
}

// strokePath strokes the current path's straight segments with butt caps,
// each segment a quad (curves by their chords, finely enough for a test).
func (r *rasterizer) strokePath() {
	hw := r.gs.width * r.gs.ctm.scale() / 2
	if hw <= 0 {
		return
	}
	var segs [][4]float64
	var cur, start [2]float64
	for _, p := range r.path {
		switch p.op {
		case 'm':
			cur, start = p.pts[0], p.pts[0]
		case 'l':
			segs = append(segs, [4]float64{cur[0], cur[1], p.pts[0][0], p.pts[0][1]})
			cur = p.pts[0]
		case 'c':
			const steps = 16
			a := cur
			for k := 1; k <= steps; k++ {
				t := float64(k) / steps
				u := 1 - t
				x := u*u*u*cur[0] + 3*u*u*t*p.pts[0][0] + 3*u*t*t*p.pts[1][0] + t*t*t*p.pts[2][0]
				y := u*u*u*cur[1] + 3*u*u*t*p.pts[0][1] + 3*u*t*t*p.pts[1][1] + t*t*t*p.pts[2][1]
				segs = append(segs, [4]float64{a[0], a[1], x, y})
				a = [2]float64{x, y}
			}
			cur = p.pts[2]
		case 'h':
			segs = append(segs, [4]float64{cur[0], cur[1], start[0], start[1]})
			cur = start
		}
	}
	saved := r.path
	for _, s := range segs {
		l := math.Hypot(s[2]-s[0], s[3]-s[1])
		if l == 0 {
			continue
		}
		nx, ny := -(s[3]-s[1])/l*hw, (s[2]-s[0])/l*hw
		r.path = []pathOp{
			{op: 'm', pts: [3][2]float64{{s[0] + nx, s[1] + ny}}}, {op: 'l', pts: [3][2]float64{{s[2] + nx, s[3] + ny}}},
			{op: 'l', pts: [3][2]float64{{s[2] - nx, s[3] - ny}}}, {op: 'l', pts: [3][2]float64{{s[0] - nx, s[1] - ny}}}, {op: 'h'},
		}
		r.fillPath(r.gs.line)
	}
	r.path = saved
}
