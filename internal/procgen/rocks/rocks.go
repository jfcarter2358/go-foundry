// Package rocks is the rock and cliff generator behind the `rocks` stage:
// weathered desert sandstone and granite as signed-distance shapes
// (internal/procgen/sdf), meshed, decimated to a triangle budget, box-mapped
// and baked with vertex colour — strata tint, cavity, edge wear, varnish and
// dust in RGB, ambient occlusion in A — ready for the Nanite cook.
//
// It is pure computation (procgen/sdf, procgen/noise and the cluster
// simplifier; layering-asserted, internal/layering_test.go). The stage
// (internal/procgen/stages/rocks*.go) turns a Result into a model.ImportMesh,
// cooks it and writes the manifest later stages place variants from.
//
// # One variant
//
// Generate makes one variant of a preset from its seed:
//
//  1. SHAPE: the preset's builder (presets.go) composes the rock — a block
//     broken out along fracture planes, a cliff face cut by joints and
//     weathered back bed by bed, a hoodoo's column, a pebble's ellipsoid —
//     from the settings and a random stream keyed by the seed.
//  2. RESOLUTION: a coarse meshing measures the surface area, and the cell
//     is chosen so the mesh has a few times the target triangles (about
//     three triangles per cell's worth of area), never more than the
//     mesher's 512 a side.
//  3. MESH: sdf.Polygonize — dual contouring with the preset's sharpness,
//     a couple of steps of convex weathering — then the fragments a joint
//     cut free are dropped.
//  4. DECIMATE to the target (the cluster simplifier: quadric edge collapse
//     onto existing vertices, the normals weighed in), which keeps the shape
//     a mesh made at that resolution directly would have lost.
//  5. BAKE the vertex colours from the FIELD, not the mesh (color.go): a
//     scatter rock of ten thousand triangles still carries its laminations,
//     crevices and chip scars in its colour.
//  6. BOX UVs: each triangle projected along its normal's main axis, a
//     vertex split wherever two projections meet (finish.go).
//  7. FRAME: the rock's lowest point on y = 0, centred on its footprint; a
//     cliff chunk's flat back on z = 0 facing −Z. Then its metadata
//     (meta.go): bounds, footprint, height, triangle count, the back, the
//     collision a placement should use.
//
// The same settings and seed make the same bytes on any number of workers.
package rocks

import (
	"context"
	"fmt"
	"math"
	"time"

	"foundry/internal/procgen/sdf"
)

// Settings are one rocks stage's choices (the stage's schema maps onto
// these; zero values take the preset's defaults where noted).
type Settings struct {
	Preset string
	// Lithology is "sandstone" or "granite".
	Lithology string
	// Size is the preset's main dimension in metres (0: the preset's), and
	// SizeJitter how much it varies variant to variant (0..0.5).
	Size, SizeJitter float64
	// Triangles is the target triangle count (0: the preset's).
	Triangles int
	// The shape's character, 0..1 each (see the stage's docs).
	Fracture, Weathering, Roughness, Chips float64
	// JointSpacing is the joint network's spacing, metres (0: the
	// preset's, from the size).
	JointSpacing float64
	// StrataThickness (metres, 0: the preset's), Undercut 0..1, and
	// StrataRelief 0..1 (how far soft beds weather back).
	StrataThickness, Undercut, StrataRelief float64
	// Colour: the rock's base colour, the band colour beds mix toward, how
	// much the beds vary, and the bake's strengths (0..1).
	Color, BandColor [3]float64
	ColorVariation   float64
	// Palette names the colours (Palettes; "auto" by lithology, "custom"
	// Color and BandColor).
	Palette                     string
	Cavity, AO, Varnish, Dust   float64
	AORadius                    float64 // metres (0: from the size)
	UVScale                     float64 // UV units per metre (0: 0.5)
	Sharpness                   float64 // −1: the preset's; else 0..1
	Workers                     int
	Ctx                         context.Context
	Progress                    func(done float32)
	MaxCells                    int // the mesher's cap a side (0: sdf.MaxCells)
	SkipBake, SkipSimplify, Raw bool
}

// Mesh is a finished rock in its placement frame.
type Mesh struct {
	Positions [][3]float32
	Normals   [][3]float32
	UVs       [][2]float32
	Colors    [][4]uint8
	Indices   []uint32
}

// TriangleCount is len(Indices)/3.
func (m *Mesh) TriangleCount() int { return len(m.Indices) / 3 }

// Result is one variant.
type Result struct {
	Mesh *Mesh
	Meta VariantMeta
	// Timings: the shape, the meshing, the decimation, the bake and the UVs.
	Timings Timings
}

// Timings is where a variant's time went.
type Timings struct {
	Mesh, Simplify, Bake, Finish, Total time.Duration
	// MeshTriangles is how many the mesher made before the decimation;
	// Cell the cell it meshed at.
	MeshTriangles int
	Cell          float64
	Evaluations   int64
}

// Defaults is the stage's defaults: a red sandstone boulder.
func Defaults() Settings {
	return Settings{
		Preset: "boulder", Lithology: "", Palette: "auto", SizeJitter: 0.2,
		Fracture: 0.5, Weathering: 0.5, Roughness: 0.5, Chips: 0.5,
		Undercut: 0.6, StrataRelief: 0.6,
		Color: [3]float64{0.70, 0.37, 0.23}, BandColor: [3]float64{0.84, 0.62, 0.45}, ColorVariation: 0.6,
		Cavity: 0.6, AO: 0.8, Varnish: 0.25, Dust: 0.3, Sharpness: -1,
	}
}

// Generate makes the variant of s with seed (see the package comment).
func Generate(s Settings, seed uint64) (*Result, error) {
	t0 := time.Now()
	ctx := s.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	pr, ok := presets[s.Preset]
	if !ok {
		return nil, fmt.Errorf("rocks: no preset %q (have %v)", s.Preset, PresetNames())
	}
	s = resolve(s, pr)
	progress := func(a, b float32) func(float32) {
		return func(f float32) {
			if s.Progress != nil {
				s.Progress(a + (b-a)*f)
			}
		}
	}
	// The shape is built twice: once to measure, then again knowing the
	// vertex spacing, so detail finer than the mesh can hold (laminations)
	// is left out of the geometry and the colour rather than aliased into
	// them. The same seed draws the same numbers both times.
	shape := pr.build(newBuild(s, pr, seed))
	if shape.Box.IsInfinite() || shape.Box.Empty() {
		return nil, fmt.Errorf("rocks: preset %s built a shape with no box", s.Preset)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sharp := pr.sharpness
	if s.Sharpness >= 0 {
		sharp = s.Sharpness
	}
	weather := int(math.Round(s.Weathering * 2.4))
	maxCells := s.MaxCells
	if maxCells <= 0 || maxCells > sdf.MaxCells {
		maxCells = sdf.MaxCells
	}

	// The resolution: a coarse look at the area, then the cell for the mesh
	// budget.
	ext := shape.Box.Size().MaxComp()
	coarse, _, err := sdf.Polygonize(shape, sdf.MeshOptions{Cell: ext / 40, Sharpness: sharp, Workers: s.Workers, Ctx: ctx})
	if err != nil {
		return nil, err
	}
	// The coarse mesh misses the fine relief; the fine one has about a third
	// more area (measured over the presets).
	area := coarse.Area() * 1.3
	meshTris := float64(s.Triangles) * oversample(s.Triangles)
	if s.Raw || s.SkipSimplify {
		meshTris = float64(s.Triangles)
	}
	cell := math.Sqrt(3 * area / math.Max(meshTris, 64))
	// Within the grid's cap (the box plus the mesher's margin, in blocks).
	cell = math.Max(cell, (ext+4*cell)/float64(maxCells-16))
	b := newBuild(s, pr, seed)
	b.spacing = cell * math.Sqrt(math.Max(meshTris/float64(s.Triangles), 1))
	shape = pr.build(b)
	t1 := time.Now()
	m, st, err := sdf.Polygonize(shape, sdf.MeshOptions{Cell: cell, Sharpness: sharp, Weathering: weather,
		WeatherRate: 0.9, Workers: s.Workers, Ctx: ctx, Progress: progress(0, 0.55)})
	if err != nil {
		return nil, err
	}
	// A hero piece is meshed straight at its budget (no decimation to take
	// up the slack), and the coarse look can be a third out either way on a
	// rock this rough: well under, it is meshed once more at the cell the
	// first mesh says gives the budget.
	if !s.Raw && s.Triangles >= 100000 && float64(m.TriangleCount()) < 0.8*meshTris {
		cell2 := cell * math.Sqrt(float64(m.TriangleCount())/meshTris)
		cell2 = math.Max(cell2, (ext+4*cell2)/float64(maxCells-16))
		if cell2 < cell*0.97 {
			cell = cell2
			b = newBuild(s, pr, seed)
			b.spacing = cell * math.Sqrt(math.Max(meshTris/float64(s.Triangles), 1))
			shape = pr.build(b)
			var st2 sdf.MeshStats
			if m, st2, err = sdf.Polygonize(shape, sdf.MeshOptions{Cell: cell, Sharpness: sharp, Weathering: weather,
				WeatherRate: 0.9, Workers: s.Workers, Ctx: ctx, Progress: progress(0.3, 0.55)}); err != nil {
				return nil, err
			}
			st.Evaluations += st2.Evaluations
		}
	}
	m.KeepLargest(0.02)
	m.SplitNonManifold()
	res := &Result{}
	res.Timings.Mesh = time.Since(t1)
	res.Timings.MeshTriangles = m.TriangleCount()
	res.Timings.Cell = cell
	res.Timings.Evaluations = st.Evaluations
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Bake, in the shape's own frame, on the full mesh: the decimation then
	// weighs the colours in, keeping vertices where a crack's dark line or a
	// band's edge runs and spending few where the colour is even.
	t3 := time.Now()
	var colors [][4]uint8
	if !s.SkipBake {
		colors, err = bake(b, shape, m, ctx, s.Workers)
		if err != nil {
			return nil, err
		}
	}
	res.Timings.Bake = time.Since(t3)
	report(s.Progress, 0.75)

	// Decimate.
	t2 := time.Now()
	// A hero mesh a little over budget is kept: decimating 900k triangles is
	// half a minute for a tenth of the count.
	slack := 1.1
	if s.Triangles >= 200000 {
		slack = 1.4
	}
	if !s.SkipSimplify && !s.Raw && float64(m.TriangleCount()) > float64(s.Triangles)*slack {
		colors = simplify(m, colors, s.Triangles, ext)
		// The gradient's normals describe the surface at the mesher's
		// scale; over the decimated triangles they make streaks. The mesh's
		// own, angle-weighted, describe what is drawn.
		meshNormals(m)
	}
	res.Timings.Simplify = time.Since(t2)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	report(s.Progress, 0.95)

	// Frame, UVs, metadata.
	t4 := time.Now()
	off := placementOffset(m, pr)
	for i := range m.Positions {
		m.Positions[i] = m.Positions[i].Add(off)
	}
	res.Mesh = boxUVs(m, colors, s.UVScale)
	res.Meta = measure(res.Mesh, s, pr, seed)
	res.Meta.Size = b.size
	res.Timings.Finish = time.Since(t4)
	res.Timings.Total = time.Since(t0)
	ms := func(d time.Duration) float64 { return math.Round(float64(d.Microseconds())/100) / 10 }
	res.Meta.MeshMS, res.Meta.SimplifyMS, res.Meta.BakeMS = ms(res.Timings.Mesh), ms(res.Timings.Simplify), ms(res.Timings.Bake)
	res.Meta.MeshTriangles, res.Meta.Cell = res.Timings.MeshTriangles, cell
	report(s.Progress, 1)
	return res, nil
}

func report(p func(float32), f float32) {
	if p != nil {
		p(f)
	}
}

// oversample is how many times the target the mesher makes before the
// decimation takes it down: three for a scatter rock (where the decimation's
// choice of what to keep is most of the quality), falling to one for a hero
// cliff of hundreds of thousands (where the mesh itself is the detail and a
// decimation would cost minutes).
func oversample(target int) float64 {
	t := float64(target)
	return 1 + 2*sdf.Clamp((250e3-t)/200e3, 0, 1)
}

// resolve fills the settings' preset defaults.
func resolve(s Settings, pr *preset) Settings {
	if s.Size <= 0 {
		s.Size = pr.size
	}
	if s.Triangles <= 0 {
		s.Triangles = pr.triangles
	}
	if s.Lithology == "" {
		s.Lithology = pr.lithology
	}
	if s.UVScale <= 0 {
		s.UVScale = 0.5
	}
	if s.StrataThickness <= 0 {
		s.StrataThickness = pr.strata * s.Size
	}
	if s.JointSpacing <= 0 {
		s.JointSpacing = pr.joints * s.Size
	}
	if s.AORadius <= 0 {
		s.AORadius = pr.aoRadius * s.Size
	}
	return s
}
