package stages

// noise_preview.go — the `noise_preview` stage: a noise heightfield, tiled,
// cooked and placed. A debugging aid that touches every part of the
// generator machinery, kept because a stage that exercises the plumbing and
// nothing else is the quickest way to tell a plumbing bug from a terrain bug.
//
// What it does, in order:
//
//  1. Samples a fractal (procgen/noise) over a square centred on the origin,
//     optionally domain-warped, remapped through a `curve`, plus a `list` of
//     bumps, minus a channel carved along a `spline` whose points carry a
//     width and depth, minus cellular cracks. Rows in parallel.
//  2. Writes heightmap.png (16-bit) and slope.png (8-bit) and a meta.yaml
//     with the bounds and the height range.
//  3. Splits the field into tiles x tiles CLOSED tiles (meshout.HeightGrid:
//     top, skirts, bottom), and cooks each into a .fnm with its rectangle
//     border-locked, in parallel.
//  4. Places each tile as scenery: `collision` as chosen, `chunk: auto`.
//
// Its preview is the same field at no more than 96 samples a side: a height
// image, a shaded relief, the slope mask, the bump centres with their radii,
// the carve path, the tile grid, and one coarse mesh for the 3D view.
//
// Its Load reads heightmap.png and meta.yaml back into a Heightfield, so a
// downstream stage can read it from a run that skipped this one.

import (
	"fmt"
	"math"

	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/noise"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// NoisePreviewKind is the stage's name.
const NoisePreviewKind = "noise_preview"

// noisePreviewMaxSamples bounds the preview's grid.
const noisePreviewMaxSamples = 96

// Heightfield is a square of height samples centred on the origin: the
// noise_preview stage's product, and what its Load rebuilds.
type Heightfield struct {
	// N is the cells a side; there are N+1 samples a side.
	N int
	// Size is the side in metres; the field spans [-Size/2, Size/2] on X and Z.
	Size float64
	// H is the heights, (N+1)^2, row-major from -Z.
	H []float32
	// Lo and Hi are the height range.
	Lo, Hi float32
}

// At is sample (i, j).
func (f *Heightfield) At(i, j int) float32 { return f.H[j*(f.N+1)+i] }

// Bounds is the field's XZ rectangle.
func (f *Heightfield) Bounds() procgen.Rect {
	h := f.Size / 2
	return procgen.Rect{MinX: -h, MinZ: -h, MaxX: h, MaxZ: h}
}

// heightMeta is meta.yaml.
type heightMeta struct {
	Size  float64 `yaml:"size"`
	Cells int     `yaml:"cells"`
	Lo    float32 `yaml:"height_lo"`
	Hi    float32 `yaml:"height_hi"`
}

func init() {
	procgen.RegisterStage(procgen.StageKind{
		Name:     NoisePreviewKind,
		Label:    "Noise preview",
		Category: "Debug",
		Version:  1,
		Doc: "A noise heightfield, cut into closed tiles, cooked as Nanite meshes and placed as scenery. " +
			"A debugging aid for the generator machinery: every parameter type, a live preview, a real run.",
		Params:   noisePreviewParams(),
		Run:      runNoisePreview,
		Preview:  previewNoisePreview,
		Load:     loadNoisePreview,
		Validate: validateNoisePreview,
	})
	procgen.RegisterTemplate(procgen.Template{
		Name:  "noise_demo",
		Label: "Noise demo",
		Doc:   "One noise_preview stage with a carve path and a bump: the quickest way to see a run end to end.",
		New: func(name string) *procgen.Doc {
			return &procgen.Doc{Name: name, Seed: 1, Doc: "Noise demo", Stages: []procgen.Stage{{
				Kind: NoisePreviewKind, ID: "ground", Params: procgen.Params{
					"path": procgen.Spline(
						[]float64{-110, -60, 14, 6}, []float64{-30, -10, 10, 8},
						[]float64{40, 20, 16, 10}, []float64{110, 70, 20, 6}),
					"bumps": procgen.List(procgen.Params{
						"position": procgen.Vec2(60, -60), "radius": procgen.Float(40), "height": procgen.Float(25)}),
				},
			}}}
		},
	})
}

func noisePreviewParams() []procgen.ParamDef {
	return []procgen.ParamDef{
		{Name: "size", Type: procgen.ParamFloat, Label: "Size (m)", Default: procgen.Float(256), Min: 8, Max: 4096,
			Doc: "The side of the square, in metres, centred on the origin."},
		{Name: "resolution", Type: procgen.ParamInt, Label: "Cells", Default: procgen.Int(128), Min: 8, Max: 2048,
			Doc: "Cells a side over the whole square. Must divide by Tiles."},
		{Name: "tiles", Type: procgen.ParamInt, Label: "Tiles", Default: procgen.Int(2), Min: 1, Max: 16,
			Doc: "Tiles a side. Each is cooked on its own, border-locked so neighbours meet at every LOD."},
		{Name: "height", Type: procgen.ParamFloat, Label: "Height (m)", Default: procgen.Float(40), Min: 0, Max: 2000,
			Doc: "The noise's full height range."},
		{Name: "shape", Type: procgen.ParamEnum, Default: procgen.Enum("fbm"), Choices: noise.ShapeNames,
			Doc: "fbm: rolling. ridged: sharp crests. billow: rounded lumps."},
		{Name: "basis", Type: procgen.ParamEnum, Default: procgen.Enum("gradient"), Choices: noise.BasisNames,
			Doc: "The noise each octave is: gradient (Perlin) or value."},
		{Name: "octaves", Type: procgen.ParamInt, Default: procgen.Int(5), Min: 1, Max: 10,
			Doc: "How many octaves of detail."},
		{Name: "frequency", Type: procgen.ParamFloat, Default: procgen.Float(0.008), Min: 0.0001, Max: 1, Step: 0.0005,
			Doc: "The first octave's frequency, in cycles per metre."},
		{Name: "gain", Type: procgen.ParamFloat, Default: procgen.Float(0.5), Min: 0, Max: 1,
			Doc: "Each octave's amplitude as a fraction of the one before."},
		{Name: "warp", Type: procgen.ParamFloat, Label: "Warp (m)", Default: procgen.Float(0), Min: 0, Max: 500,
			Doc: "Domain warp distance: bends the features."},
		{Name: "seed", Type: procgen.ParamSeed, Default: procgen.Seed(0),
			Doc: "Changes the noise without changing anything else."},
		{Name: "profile", Type: procgen.ParamCurve, Label: "Height curve",
			Default: procgen.Curve([2]float64{0, 0}, [2]float64{1, 1}), Min: 0, Max: 1, MinItems: 2,
			Doc: "Remaps the noise (0 low, 1 high) before it is scaled by Height: flatten the lows, terrace, invert."},
		{Name: "path", Type: procgen.ParamSpline, Label: "Carve path",
			Default: procgen.Spline(), Channels: []string{"width", "depth"}, ChannelDefaults: []float64{12, 6},
			Doc: "A channel carved along this path, its width and depth per point. Edit it in the top-down view."},
		{Name: "bumps", Type: procgen.ParamList, Default: procgen.List(), MaxItems: 64,
			Doc: "Round hills added to the field.",
			Fields: []procgen.ParamDef{
				{Name: "position", Type: procgen.ParamVec2, Default: procgen.Vec2(0, 0), Doc: "Centre, X and Z in metres."},
				{Name: "radius", Type: procgen.ParamFloat, Default: procgen.Float(30), Min: 0.1, Max: 2000, Doc: "Radius in metres."},
				{Name: "height", Type: procgen.ParamFloat, Default: procgen.Float(15), Min: -500, Max: 500, Doc: "Height at the centre; negative digs a pit."},
			}},
		{Name: "cracks", Type: procgen.ParamFloat, Label: "Cracks (m)", Default: procgen.Float(0), Min: 0, Max: 20,
			Doc: "Depth of a cellular crack network (30 m cells) cut into the surface."},
		{Name: "collision", Type: procgen.ParamEnum, Default: procgen.Enum("mesh"),
			Choices: []string{"mesh", "hulls", "box", "none", "auto"},
			Doc:     "How the placed tiles collide."},
		{Name: "texture", Type: procgen.ParamTexture, Default: procgen.Texture(""),
			Doc: "The tiles' texture; none draws them in their normal colours."},
		{Name: "cook", Type: procgen.ParamBool, Default: procgen.Bool(true),
			Doc: "Cook and place the tiles. Off writes the images and the tiles as .glb only, and places nothing."},
		{Name: "note", Type: procgen.ParamString, Default: procgen.String(""),
			Doc: "Free text, unused: here so the string widget has a caller."},
	}
}

func validateNoisePreview(_ *procgen.Doc, _ *procgen.Stage, p procgen.Params) []string {
	if p.Int("resolution")%p.Int("tiles") != 0 {
		return []string{fmt.Sprintf("Cells (%d) must divide by Tiles (%d)", p.Int("resolution"), p.Int("tiles"))}
	}
	return nil
}

// sampleNoise builds the field at n cells a side. Rows in parallel.
func sampleNoise(env *procgen.Env, n int) (*Heightfield, error) {
	p := env.Params
	size := p.Float("size")
	f := noise.Fractal{
		Seed: env.Seed, Octaves: p.Int("octaves"), Frequency: p.Float("frequency"), Gain: p.Float("gain"),
		Shape: noise.ShapeFromName(p.Str("shape")), Basis: noise.BasisFromName(p.Str("basis")),
	}
	warpF := noise.Fractal{Seed: noise.Hash64(env.Seed, 1), Octaves: 3, Frequency: p.Float("frequency") * 0.5}
	warp := p.Float("warp")
	height := p.Float("height")
	profile := p.Curve("profile")
	path := p.Points("path")
	bumps := p.Items("bumps")
	cracks := p.Float("cracks")
	crackSeed := noise.Hash64(env.Seed, 2)

	field := &Heightfield{N: n, Size: size, H: make([]float32, (n+1)*(n+1))}
	err := env.ParallelProgress(n+1, func(j int) error {
		if err := env.Err(); err != nil {
			return err
		}
		z := -size/2 + size*float64(j)/float64(n)
		for i := 0; i <= n; i++ {
			x := -size/2 + size*float64(i)/float64(n)
			sx, sz := x, z
			if warp > 0 {
				sx, sz = noise.Warp2(warpF, x, z, warp)
			}
			t := (f.At2(sx, sz) + 1) / 2
			h := profile.CurveAt(t) * height
			for _, b := range bumps {
				c := b.Vec2("position")
				r := b.Float("radius")
				d := math.Hypot(x-c[0], z-c[1])
				if d < r {
					u := 1 - d/r
					h += b.Float("height") * u * u * (3 - 2*u)
				}
			}
			if len(path) >= 2 {
				d, w, depth := splineDistance(path, x, z)
				h -= depth * (1 - noise.SmoothStep(0, w, d))
			}
			if cracks > 0 {
				c := noise.Cellular2(crackSeed, x/30, z/30, 1, noise.MetricEuclidean)
				h -= cracks * (1 - noise.SmoothStep(0, 0.06, c.Edge()))
			}
			field.H[j*(n+1)+i] = float32(h)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	field.Lo, field.Hi = field.H[0], field.H[0]
	for _, v := range field.H {
		field.Lo, field.Hi = min(field.Lo, v), max(field.Hi, v)
	}
	return field, nil
}

// splineDistance is the distance from (x, z) to a polyline of [x, z, width,
// depth] points, and the width and depth interpolated at the nearest point.
func splineDistance(pts [][]float64, x, z float64) (dist, width, depth float64) {
	dist = math.Inf(1)
	for k := 0; k+1 < len(pts); k++ {
		a, b := pts[k], pts[k+1]
		dx, dz := b[0]-a[0], b[1]-a[1]
		l2 := dx*dx + dz*dz
		t := 0.0
		if l2 > 0 {
			t = noise.Clamp(((x-a[0])*dx+(z-a[1])*dz)/l2, 0, 1)
		}
		px, pz := a[0]+dx*t, a[1]+dz*t
		if d := math.Hypot(x-px, z-pz); d < dist {
			dist = d
			width = a[2] + (b[2]-a[2])*t
			depth = a[3] + (b[3]-a[3])*t
		}
	}
	return
}

// slopeMask is 0 (flat) to 1 (vertical) per sample.
func slopeMask(f *Heightfield) []float32 {
	n := f.N
	cell := f.Size / float64(n)
	out := make([]float32, len(f.H))
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			gx := float64(f.At(min(i+1, n), j)-f.At(max(i-1, 0), j)) / (cell * float64(min(i+1, n)-max(i-1, 0)))
			gz := float64(f.At(i, min(j+1, n))-f.At(i, max(j-1, 0))) / (cell * float64(min(j+1, n)-max(j-1, 0)))
			out[j*(n+1)+i] = float32(math.Atan(math.Hypot(gx, gz)) / (math.Pi / 2))
		}
	}
	return out
}

// relief is a shaded-relief image (sun from the north-west), as RGBA.
func relief(f *Heightfield) []uint8 {
	n := f.N
	cell := float32(f.Size / float64(n))
	sun := mgl32.Vec3{-1, 1.4, -1}.Normalize()
	out := make([]uint8, (n+1)*(n+1)*4)
	span := f.Hi - f.Lo
	if span == 0 {
		span = 1
	}
	for j := 0; j <= n; j++ {
		for i := 0; i <= n; i++ {
			dx := (f.At(min(i+1, n), j) - f.At(max(i-1, 0), j)) / (2 * cell)
			dz := (f.At(i, min(j+1, n)) - f.At(i, max(j-1, 0))) / (2 * cell)
			nrm := mgl32.Vec3{-dx, 1, -dz}.Normalize()
			lit := max(nrm.Dot(sun), 0)*0.8 + 0.2
			t := (f.At(i, j) - f.Lo) / span
			// Sand at the bottom to rust at the top.
			r := (0.55 + 0.35*t) * lit
			g := (0.45 + 0.15*t) * lit
			b := (0.32 + 0.05*t) * lit
			k := (j*(n+1) + i) * 4
			out[k], out[k+1], out[k+2], out[k+3] = uint8(min(r, 1)*255), uint8(min(g, 1)*255), uint8(min(b, 1)*255), 255
		}
	}
	return out
}

// tileGrid is tile (ti, tj) of a field cut into t x t, in tile-local
// coordinates (corner at the origin), and the world position of its corner.
func tileGrid(f *Heightfield, t, ti, tj int, closed bool) (meshout.HeightGrid, procgen.Point3) {
	per := f.N / t
	ts := float32(f.Size / float64(t))
	x0 := float32(-f.Size/2) + ts*float32(ti)
	z0 := float32(-f.Size/2) + ts*float32(tj)
	g := meshout.HeightGrid{
		SizeX: ts, SizeZ: ts, NX: per, NZ: per, Closed: closed, Base: f.Lo - 5,
		Height: func(i, j int) float32 { return f.At(ti*per+i, tj*per+j) },
	}
	return g, procgen.Point3{X: x0, Y: 0, Z: z0}
}

func runNoisePreview(rc *procgen.RunContext) error {
	p := rc.Params
	field, err := sampleNoise(&rc.Env, p.Int("resolution"))
	if err != nil {
		return err
	}
	rc.Artifact = field
	n := field.N + 1
	hm, err := encodePNG16(n, n, field.H, field.Lo, field.Hi)
	if err != nil {
		return err
	}
	if err := rc.WriteFile("heightmap.png", hm); err != nil {
		return err
	}
	sm, err := encodePNG8(n, n, slopeMask(field))
	if err != nil {
		return err
	}
	if err := rc.WriteFile("slope.png", sm); err != nil {
		return err
	}
	meta, _ := yaml.Marshal(heightMeta{Size: field.Size, Cells: field.N, Lo: field.Lo, Hi: field.Hi})
	if err := rc.WriteFile("meta.yaml", meta); err != nil {
		return err
	}
	rc.Logf("field %d x %d, heights %.1f .. %.1f m", n, n, field.Lo, field.Hi)

	t := p.Int("tiles")
	cook := p.Bool("cook")
	tex := p.Str("texture")
	return rc.ParallelProgress(t*t, func(k int) error {
		ti, tj := k%t, k/t
		g, corner := tileGrid(field, t, ti, tj, true)
		var b meshout.Builder
		b.Slot("ground", tex)
		g.Build(&b)
		m := b.Mesh()
		name := fmt.Sprintf("tile_%d_%d", ti, tj)
		if !cook {
			return meshout.WriteGLB(rc.Path(name+".glb"), m)
		}
		rect := g.Rect()
		if _, err := meshout.Cook(m, rc.Path(name+".fnm"), meshout.CookOptions{LockRect: &rect}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		rc.Place(procgen.Placement{
			ID: name, Model: rc.AssetPath(name + ".fnm"), Position: corner,
			Collision: p.Str("collision"), Chunk: procgen.ChunkAuto,
		})
		return nil
	})
}

func previewNoisePreview(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	n := min(p.Int("resolution"), noisePreviewMaxSamples)
	field, err := sampleNoise(&pc.Env, n)
	if err != nil {
		return nil, err
	}
	b := field.Bounds()
	side := n + 1
	out := &procgen.PreviewData{Bounds: b, Artifact: field}
	ramp := [][4]uint8{{20, 24, 40, 255}, {70, 110, 90, 255}, {190, 170, 110, 255}, {250, 245, 235, 255}}
	h := procgen.RampImage("Height", side, side, field.H, field.Lo, field.Hi, b, ramp)
	h.Unit = "m"
	out.Images = append(out.Images,
		procgen.ImageLayer{Name: "Relief", W: side, H: side, RGBA: relief(field), Values: field.H, Unit: "m", Bounds: b},
		h,
		procgen.GrayImage("Slope", side, side, slopeMask(field), 0, 1, b),
	)
	if path := p.Points("path"); len(path) > 0 {
		l := procgen.LineLayer{Name: "Carve path", Color: [4]float32{0.3, 0.8, 1, 1}}
		for _, q := range path {
			l.Points = append(l.Points, [2]float64{q[0], q[1]})
		}
		out.Lines = append(out.Lines, l)
	}
	if bumps := p.Items("bumps"); len(bumps) > 0 {
		pl := procgen.PointLayer{Name: "Bumps", Color: [4]float32{1, 0.6, 0.2, 1}}
		for _, bp := range bumps {
			c := bp.Vec2("position")
			pl.Points = append(pl.Points, [2]float64{c[0], c[1]})
			pl.Radii = append(pl.Radii, bp.Float("radius"))
		}
		out.Points = append(out.Points, pl)
	}
	t := p.Int("tiles")
	for k := 1; k < t; k++ {
		x := b.MinX + (b.MaxX-b.MinX)*float64(k)/float64(t)
		z := b.MinZ + (b.MaxZ-b.MinZ)*float64(k)/float64(t)
		grid := [4]float32{1, 1, 1, 0.35}
		out.Lines = append(out.Lines,
			procgen.LineLayer{Name: "Tiles", Points: [][2]float64{{x, b.MinZ}, {x, b.MaxZ}}, Color: grid},
			procgen.LineLayer{Name: "Tiles", Points: [][2]float64{{b.MinX, z}, {b.MaxX, z}}, Color: grid})
	}

	// One open mesh of the whole preview field for the 3D view.
	g := meshout.HeightGrid{X0: float32(b.MinX), Z0: float32(b.MinZ), SizeX: float32(field.Size), SizeZ: float32(field.Size),
		NX: n, NZ: n, Height: field.At}
	var mb meshout.Builder
	g.Build(&mb)
	m := mb.Mesh()
	pm := procgen.PreviewMesh{Name: "Ground", Indices: m.Indices}
	for i := range m.Positions {
		pm.Positions = append(pm.Positions, [3]float32(m.Positions[i]))
		pm.Normals = append(pm.Normals, [3]float32(m.Normals[i]))
	}
	out.Meshes = append(out.Meshes, pm)
	out.Notes = append(out.Notes,
		fmt.Sprintf("%.0f m square, heights %.1f .. %.1f m", field.Size, field.Lo, field.Hi),
		fmt.Sprintf("preview %d cells a side (run: %d), %d tiles", n, p.Int("resolution"), t*t))
	return out, nil
}

func loadNoisePreview(res *procgen.StageResult) (any, error) {
	metaData, err := readFile(res.File("meta.yaml"))
	if err != nil {
		return nil, err
	}
	var meta heightMeta
	if err := yaml.Unmarshal(metaData, &meta); err != nil {
		return nil, err
	}
	data, err := readFile(res.File("heightmap.png"))
	if err != nil {
		return nil, err
	}
	w, h, vals, err := decodePNG16(data)
	if err != nil {
		return nil, err
	}
	if w != meta.Cells+1 || h != meta.Cells+1 {
		return nil, fmt.Errorf("heightmap is %dx%d, meta says %d cells", w, h, meta.Cells)
	}
	f := &Heightfield{N: meta.Cells, Size: meta.Size, Lo: meta.Lo, Hi: meta.Hi, H: vals}
	for i, v := range vals {
		f.H[i] = meta.Lo + v*(meta.Hi-meta.Lo)
	}
	return f, nil
}
