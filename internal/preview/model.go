package preview

// Package preview draws a .fmdl to an image with no GL and no window.
//
// "Does the helmet read as armour", "did the repaint land where the geometry
// is", "is that clip's pose actually what I measured" — every one of those was
// answered by a Python rasteriser living outside the engine, which meant it
// carried its own copy of the pose maths and drifted from them. This is the same
// picture drawn from the engine's own model code: ModelDoc for the mesh,
// ModelClip.SampleInto and PoseDeltas for the pose, SkinVerts for the skin. The
// shading is not the engine's — it is a flat painter's-algorithm fill with a
// headlight, which is all a shape check needs.
//
// Two things it does on purpose, both of which the Python version got wrong
// first and both of which are silent when wrong:
//
//   - N-gons are FANNED. A .fmdl holds quads and larger, and drawing only the
//     first three indices of each face loses most of a model built out of them:
//     a flat display plate came out as a triangular wedge.
//   - The texture is sampled PER FRAGMENT. A readout is two triangles, so one
//     sample per face paints the whole display in one flat colour and the digits
//     are simply not there.

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/model"
	"foundry/internal/texture"

	"github.com/go-gl/mathgl/mgl32"
)

// Options is how to draw a model.
type Options struct {
	// W and H are the image size; 512x512 by default.
	W, H int
	// Pose is `clip:seconds` — "walk:0.25" — or empty for the rest pose.
	Pose string
	// View is where the camera stands: front (the default), back, left, right,
	// top or iso.
	View string
	// Frame is cx,cy,cz,span: the box to fill the image with, in model space. A
	// helmet is four per cent of a body and cannot be judged from a whole
	// figure, which is what this is for. Without it the whole model is fitted.
	Frame *[4]float32
	// Image is a decoded texture to paint with, for a caller that has one in
	// hand (a generator checking its own output, a test). It wins over Texture.
	Image image.Image
	// Texture is an image to paint the model with, overriding the one the model
	// names. Either way it is resolved under resources/textures when it is a
	// bare name, and .ftex is flattened.
	Texture string
	// Untextured draws the mesh shaded rather than painted, which is what a
	// shape check wants when the paint is distracting.
	Untextured bool
	// Background is the colour behind the model.
	Background color.NRGBA
}

// Model draws m and answers the image.
func Model(m *model.ModelDoc, opts Options) (*image.NRGBA, error) {
	if opts.W <= 0 {
		opts.W = 512
	}
	if opts.H <= 0 {
		opts.H = 512
	}
	if opts.Background == (color.NRGBA{}) {
		opts.Background = color.NRGBA{R: 0x20, G: 0x22, B: 0x28, A: 0xff}
	}

	verts, err := posedVerts(m, opts.Pose)
	if err != nil {
		return nil, err
	}

	var tex image.Image
	if !opts.Untextured {
		tex = opts.Image
		name := opts.Texture
		if name == "" {
			name = m.Texture
		}
		if tex == nil && name != "" {
			// A missing texture is a note, not a failure: the shape is still
			// worth seeing, and the caller asked for a picture.
			tex, _ = loadTexture(name)
		}
	}

	view, up := viewAxes(opts.View)
	basis := cameraBasis(view, up)
	centre, span := framing(verts, opts, basis)

	img := image.NewNRGBA(image.Rect(0, 0, opts.W, opts.H))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+0] = opts.Background.R
		img.Pix[i+1] = opts.Background.G
		img.Pix[i+2] = opts.Background.B
		img.Pix[i+3] = 0xff
	}

	// Model space -> screen. Orthographic, because a shape check wants
	// measurable proportions rather than perspective.
	scale := float32(min(opts.W, opts.H)) / span
	project := func(p mgl32.Vec3) (x, y, depth float32) {
		d := p.Sub(centre)
		return float32(opts.W)/2 + basis.right.Dot(d)*scale,
			float32(opts.H)/2 - basis.up.Dot(d)*scale,
			basis.forward.Dot(d)
	}

	tris := fanTriangles(m, verts)
	// Painter's algorithm: farthest first. Per triangle rather than per face,
	// so a fanned n-gon sorts as the pieces it is drawn as.
	sortByDepth(tris, verts, project)

	for _, t := range tris {
		drawTriangle(img, t, verts, project, tex)
	}
	return img, nil
}

// ModelFile loads a .fmdl and draws it.
func ModelFile(path string, opts Options) (*image.NRGBA, error) {
	m, err := model.LoadModel(path)
	if err != nil {
		return nil, err
	}
	return Model(m, opts)
}

// ---------------------------------------------------------------- posing

// posedVerts answers the vertices to draw: the model's own in the rest pose, or
// the skinned result of a clip at a time.
//
// The maths is the engine's, which is the whole reason this is in the engine: a
// measurement taken here is the pose the engine draws.
func posedVerts(m *model.ModelDoc, pose string) ([]mgl32.Vec3, error) {
	if pose == "" {
		return m.Verts, nil
	}
	p, err := samplePose(m, pose)
	if err != nil {
		return nil, err
	}
	deltas := m.PoseDeltas(p)
	out := make([]mgl32.Vec3, len(m.Verts))
	if !model.SkinVerts(deltas, m.Weights, m.Verts, out) {
		// Nothing bound: an unrigged model poses to itself.
		return m.Verts, nil
	}
	return out, nil
}

// samplePose reads a `clip:seconds` and samples that clip at that moment.
func samplePose(m *model.ModelDoc, pose string) (model.ModelPose, error) {
	var p model.ModelPose
	name, atS, ok := strings.Cut(pose, ":")
	if !ok {
		return p, fmt.Errorf("pose %q: want clip:seconds", pose)
	}
	at, err := strconv.ParseFloat(atS, 32)
	if err != nil {
		return p, fmt.Errorf("pose %q: %w", pose, err)
	}
	i := m.ClipIndexByName(name)
	if i < 0 {
		return p, fmt.Errorf("no clip named %q (this model has %s)", name, strings.Join(m.ClipNames(), ", "))
	}
	m.Clip(i).SampleInto(&p, len(m.Bones), float32(at))
	return p, nil
}

// ---------------------------------------------------------------- framing

type basis struct{ right, up, forward mgl32.Vec3 }

// viewAxes turns a view name into a direction to look ALONG and an up vector.
func viewAxes(name string) (dir, up mgl32.Vec3) {
	up = mgl32.Vec3{0, 1, 0}
	switch strings.ToLower(name) {
	case "", "front":
		return mgl32.Vec3{0, 0, -1}, up
	case "back":
		return mgl32.Vec3{0, 0, 1}, up
	case "left":
		return mgl32.Vec3{1, 0, 0}, up
	case "right":
		return mgl32.Vec3{-1, 0, 0}, up
	case "top":
		// Looking straight down, so the up vector has to leave the Y axis or
		// the basis is degenerate.
		return mgl32.Vec3{0, -1, 0}, mgl32.Vec3{0, 0, 1}
	default: // iso
		return mgl32.Vec3{-1, -0.6, -1}.Normalize(), up
	}
}

// cameraBasis builds the camera's axes from the direction it looks ALONG and a
// rough up vector.
//
// right = forward x up, in that order: the other order mirrors the picture,
// which on a symmetrical model (every character) looks completely fine and puts
// the left arm on the right.
func cameraBasis(dir, up mgl32.Vec3) basis {
	f := dir.Normalize()
	r := f.Cross(up)
	if r.Len() < 1e-4 {
		r = mgl32.Vec3{1, 0, 0}
	}
	r = r.Normalize()
	return basis{right: r, up: r.Cross(f).Normalize(), forward: f}
}

// framing answers what the image is centred on and how wide it is, in model
// units: the caller's box, or the model's own bounds with a small margin.
func framing(verts []mgl32.Vec3, opts Options, b basis) (centre mgl32.Vec3, span float32) {
	if opts.Frame != nil {
		f := *opts.Frame
		return mgl32.Vec3{f[0], f[1], f[2]}, maxf(f[3], 1e-3)
	}
	if len(verts) == 0 {
		return mgl32.Vec3{}, 1
	}
	lo, hi := verts[0], verts[0]
	for _, v := range verts[1:] {
		for i := 0; i < 3; i++ {
			lo[i], hi[i] = minf(lo[i], v[i]), maxf(hi[i], v[i])
		}
	}
	centre = lo.Add(hi).Mul(0.5)

	// The span is what the model measures ON SCREEN, which is the spread of the
	// vertices along the camera's own two axes — not the bounding box's size
	// dotted with them. The box's diagonal projects to something far shorter
	// than the shape's real width on any view that is not square-on, so a model
	// framed that way fills a fraction of the image or bursts out of it, which
	// is what an isometric view of anything showed.
	loR, hiR := b.right.Dot(verts[0]), b.right.Dot(verts[0])
	loU, hiU := b.up.Dot(verts[0]), b.up.Dot(verts[0])
	for _, v := range verts[1:] {
		r, u := b.right.Dot(v), b.up.Dot(v)
		loR, hiR = minf(loR, r), maxf(hiR, r)
		loU, hiU = minf(loU, u), maxf(hiU, u)
	}
	// Centred on what is seen, too: the bounding box's middle is not on the
	// screen's middle once the box is turned.
	centre = b.right.Mul((loR + hiR) / 2).Add(b.up.Mul((loU + hiU) / 2)).
		Add(b.forward.Mul(b.forward.Dot(centre)))
	return centre, maxf(maxf(hiR-loR, hiU-loU)*1.1, 1e-3)
}

// ---------------------------------------------------------------- rasterising

// tri is one triangle to draw: three vertex indices and the UV at each corner.
type tri struct {
	v     [3]int
	uv    [3]mgl32.Vec2
	hasUV bool
}

// fanTriangles turns every face into triangles, fanning anything larger than
// one. The UVs come along per corner, which is what makes a per-fragment sample
// possible.
func fanTriangles(m *model.ModelDoc, verts []mgl32.Vec3) []tri {
	var out []tri
	for _, f := range m.Faces {
		if len(f.Verts) < 3 {
			continue
		}
		hasUV := len(f.UVs) == len(f.Verts)
		for i := 1; i+1 < len(f.Verts); i++ {
			t := tri{v: [3]int{f.Verts[0], f.Verts[i], f.Verts[i+1]}}
			if hasUV {
				t.uv = [3]mgl32.Vec2{f.UVs[0], f.UVs[i], f.UVs[i+1]}
				t.hasUV = true
			}
			if t.v[0] < len(verts) && t.v[1] < len(verts) && t.v[2] < len(verts) {
				out = append(out, t)
			}
		}
	}
	return out
}

func sortByDepth(tris []tri, verts []mgl32.Vec3, project func(mgl32.Vec3) (float32, float32, float32)) {
	depth := make([]float32, len(tris))
	for i, t := range tris {
		var sum float32
		for _, vi := range t.v {
			_, _, d := project(verts[vi])
			sum += d
		}
		depth[i] = sum / 3
	}
	// Insertion sort by depth, farthest (largest depth along the view
	// direction) first. The counts here are thousands, and a stable order keeps
	// coplanar faces in file order rather than shuffling them per run.
	for i := 1; i < len(tris); i++ {
		t, d := tris[i], depth[i]
		j := i - 1
		for j >= 0 && depth[j] < d {
			tris[j+1], depth[j+1] = tris[j], depth[j]
			j--
		}
		tris[j+1], depth[j+1] = t, d
	}
}

func drawTriangle(img *image.NRGBA, t tri, verts []mgl32.Vec3, project func(mgl32.Vec3) (float32, float32, float32), tex image.Image) {
	x0, y0, _ := project(verts[t.v[0]])
	x1, y1, _ := project(verts[t.v[1]])
	x2, y2, _ := project(verts[t.v[2]])

	// A headlight, so the far side of a model is shaded rather than a
	// silhouette. The facet normal is the triangle's own, in model space.
	e1 := verts[t.v[1]].Sub(verts[t.v[0]])
	e2 := verts[t.v[2]].Sub(verts[t.v[0]])
	n := e1.Cross(e2)
	if n.Len() > 1e-9 {
		n = n.Normalize()
	}
	// The light comes from over the camera's shoulder. abs, because a facet's
	// winding says which way it faces and a preview should not black out a
	// wall the author has not wound the way this code guesses.
	shade := 0.35 + 0.65*absf(n.Z()*0.5+n.Y()*0.35+n.X()*0.15)

	minX := maxi(0, int(minf(x0, minf(x1, x2))))
	maxX := mini(img.Bounds().Dx()-1, int(maxf(x0, maxf(x1, x2)))+1)
	minY := maxi(0, int(minf(y0, minf(y1, y2))))
	maxY := mini(img.Bounds().Dy()-1, int(maxf(y0, maxf(y1, y2)))+1)

	area := (x1-x0)*(y2-y0) - (x2-x0)*(y1-y0)
	if absf(area) < 1e-6 {
		return
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px, py := float32(x)+0.5, float32(y)+0.5
			w0 := ((x1-px)*(y2-py) - (x2-px)*(y1-py)) / area
			w1 := ((x2-px)*(y0-py) - (x0-px)*(y2-py)) / area
			w2 := 1 - w0 - w1
			if w0 < 0 || w1 < 0 || w2 < 0 {
				continue
			}
			c := color.NRGBA{R: 0xcc, G: 0xcc, B: 0xcc, A: 0xff}
			if tex != nil && t.hasUV {
				// Per fragment: the UV is interpolated across the triangle, not
				// read once for the whole of it.
				u := t.uv[0].X()*w0 + t.uv[1].X()*w1 + t.uv[2].X()*w2
				v := t.uv[0].Y()*w0 + t.uv[1].Y()*w1 + t.uv[2].Y()*w2
				c = sample(tex, u, v)
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: scaleChannel(c.R, shade),
				G: scaleChannel(c.G, shade),
				B: scaleChannel(c.B, shade),
				A: 0xff,
			})
		}
	}
}

// sample reads the texture at a UV, wrapping, with V measured downwards as the
// engine's own sampling does.
func sample(tex image.Image, u, v float32) color.NRGBA {
	b := tex.Bounds()
	wrap := func(f float32, n int) int {
		if n <= 0 {
			return 0
		}
		i := int(f * float32(n))
		i %= n
		if i < 0 {
			i += n
		}
		return i
	}
	x := b.Min.X + wrap(u, b.Dx())
	y := b.Min.Y + wrap(v, b.Dy())
	r, g, bl, a := tex.At(x, y).RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8), A: uint8(a >> 8)}
}

// loadTexture resolves a texture the way the engine names them: a bare logical
// name under resources/textures, either .ftex (flattened through its own
// palette) or .png.
func loadTexture(name string) (image.Image, error) {
	path := name
	if filepath.Ext(path) == "" {
		// A .ftex first, as the engine's own texture lookup does: a name with
		// both is authored in the texture editor and the .png beside it is what
		// that editor exported.
		if p := texture.FtexPath(name); statExists(p) {
			path = p
		} else {
			path = filepath.Join(fileutil.TexturesDir(), name+".png")
		}
	}
	if filepath.Ext(path) == ".ftex" {
		d, err := texture.ReadTexDoc(path)
		if err != nil {
			return nil, err
		}
		return d.Flatten(), nil
	}
	return texture.LoadImageFile(path)
}

func statExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ---------------------------------------------------------------- small maths

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func absf(f float32) float32 { return float32(math.Abs(float64(f))) }

func mini(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func scaleChannel(c uint8, by float32) uint8 {
	v := float32(c) * by
	if v > 255 {
		return 255
	}
	if v < 0 {
		return 0
	}
	return uint8(v)
}
