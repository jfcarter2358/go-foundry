package stages

// terrain_preview.go — the terrain stage's fast preview and its Load.
//
// The preview is the whole pipeline on a coarse grid (terrain.Settings.
// Scaled: at most preview_cells samples a side, fewer droplet passes and
// thermal rounds to match): the same mesas, canyons and drainage, without
// the fine erosion. It shows
//
//   - image layers: the shaded relief (the default base layer; its readout
//     is the height), the height, the albedo hint, and every mask with a
//     ramp that reads at a glance — overlay any of them on the relief;
//   - lines: the canyons as carved (the meandered centrelines, tributaries
//     in a paler colour) and the tile grid; the authored paths are drawn and
//     edited by the editor itself, from the canyons' spline params;
//   - points: each canyon control point with its half width, so the width
//     channel reads in the view where the path is edited;
//   - meshes: one per tile, painted with the albedo hint, for the 3D view.
//
// Load rebuilds a terrain.Terrain from a run's folder, so a downstream stage
// (scatter, cliff walls) reads heights and masks from a run that skipped
// this stage as up to date.

import (
	"fmt"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// Mask ramps for the preview.
var (
	rampFlow    = [][4]uint8{{12, 14, 20, 255}, {30, 70, 140, 255}, {80, 170, 230, 255}, {230, 250, 255, 255}}
	rampSand    = [][4]uint8{{40, 30, 24, 255}, {150, 110, 70, 255}, {235, 205, 150, 255}}
	rampCliff   = [][4]uint8{{20, 20, 20, 255}, {200, 60, 40, 255}, {255, 230, 120, 255}}
	rampCurve   = [][4]uint8{{220, 120, 40, 255}, {128, 128, 128, 255}, {40, 110, 220, 255}}
	rampHeights = [][4]uint8{{60, 40, 30, 255}, {150, 85, 55, 255}, {205, 140, 90, 255}, {245, 225, 190, 255}}
)

func previewTerrain(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	full := terrainSettings(&pc.Env)
	s := full.Scaled(p.Int("preview_cells"))
	t0 := time.Now()
	res, err := terrain.Generate(&s, &pc.Env, func(_ terrain.Phase, done float32) { pc.Progress(done) })
	if err != nil {
		return nil, err
	}
	g := res.Grid
	sz := g.S()
	half := g.Size() / 2
	b := procgen.Rect{MinX: -half, MinZ: -half, MaxX: half, MaxZ: half}
	lo, hi := g.Range()
	out := &procgen.PreviewData{Bounds: b, Artifact: terrain.FromResult(res, terrain.NewMeta(&s, res), "")}

	relief := procgen.ImageLayer{Name: "Relief", W: sz, H: sz, RGBA: terrain.Relief(g, res.Albedo), Values: g.H, Unit: "m", Bounds: b}
	height := procgen.RampImage("Height", sz, sz, g.H, lo, hi, b, rampHeights)
	height.Unit = "m"
	albedo := procgen.ImageLayer{Name: "Albedo", W: sz, H: sz, RGBA: rgbToRGBA(res.Albedo), Bounds: b}
	out.Images = append(out.Images, relief, height, albedo,
		procgen.RampImage("Flow", sz, sz, res.Masks[terrain.MaskFlow], 0, 1, b, rampFlow),
		procgen.GrayImage("Slope", sz, sz, res.Masks[terrain.MaskSlope], 0, 1, b),
		procgen.RampImage("Deposition", sz, sz, res.Masks[terrain.MaskDeposition], 0, 1, b, rampSand),
		procgen.RampImage("Cliff", sz, sz, res.Masks[terrain.MaskCliff], 0, 1, b, rampCliff),
		strataImage(res, &s, b),
		procgen.GrayImage("Strata height", sz, sz, res.Masks[terrain.MaskStrataHeight], 0, 1, b),
		procgen.RampImage("Curvature", sz, sz, res.Masks[terrain.MaskCurvature], 0, 1, b, rampCurve),
	)
	for _, cp := range res.Paths {
		l := procgen.LineLayer{Name: "Canyon", Color: [4]float32{0.35, 0.85, 1, 0.9}}
		if cp.Tributary {
			l.Name, l.Color = "Tributary", [4]float32{0.6, 0.9, 1, 0.55}
		}
		l.Points = cp.Points
		out.Lines = append(out.Lines, l)
	}
	// The authored canyons' widths at their control points (the editor draws
	// and edits the paths themselves from the spline params).
	for _, c := range s.Canyons {
		pl := procgen.PointLayer{Name: "Canyon width", Color: [4]float32{0.35, 0.85, 1, 0.5}}
		for _, q := range c.Path {
			pl.Points = append(pl.Points, [2]float64{q.X, q.Z})
			pl.Radii = append(pl.Radii, q.Width/2)
		}
		if len(pl.Points) > 0 {
			out.Points = append(out.Points, pl)
		}
	}
	// The tile grid, and the tiles as preview meshes.
	tp := tilePlan{size: p.Float("tile_size"), step: 1}
	tp.count, _ = whole(g.Size(), tp.size)
	grid := [4]float32{1, 1, 1, 0.3}
	for k := 1; k < tp.count; k++ {
		x := b.MinX + (b.MaxX-b.MinX)*float64(k)/float64(tp.count)
		out.Lines = append(out.Lines,
			procgen.LineLayer{Name: "Tiles", Points: [][2]float64{{x, b.MinZ}, {x, b.MaxZ}}, Color: grid},
			procgen.LineLayer{Name: "Tiles", Points: [][2]float64{{b.MinX, x}, {b.MaxX, x}}, Color: grid})
	}
	if per, ok := whole(tp.size, g.Cell); ok && tp.count > 0 && tp.count*per == g.N {
		tp.per = per
		for tj := 0; tj < tp.count; tj++ {
			for ti := 0; ti < tp.count; ti++ {
				out.Meshes = append(out.Meshes, tilePreviewMesh(tileName(ti, tj), g, tp, ti, tj))
			}
		}
	} else {
		// The preview grid does not split into whole tiles: one mesh.
		tp = tilePlan{size: g.Size(), count: 1, per: g.N, step: 1}
		out.Meshes = append(out.Meshes, tilePreviewMesh("ground", g, tp, 0, 0))
	}
	fn, _ := whole(full.Size, full.Cell)
	ft, _ := whole(full.Size, p.Float("tile_size"))
	out.Notes = append(out.Notes,
		fmt.Sprintf("%.0f m square, heights %.1f .. %.1f m", g.Size(), lo, hi),
		fmt.Sprintf("preview %d² at %.2f m (run: %d² at %g m, %d tiles), %s", sz, g.Cell, fn+1, full.Cell, ft*ft,
			time.Since(t0).Round(time.Millisecond)))
	return out, nil
}

// strataImage paints each sample its layer's colour, shaded by the position
// within the layer so the bedding reads.
func strataImage(res *terrain.Result, s *terrain.Settings, b procgen.Rect) procgen.ImageLayer {
	sz := res.Grid.S()
	im := procgen.ImageLayer{Name: "Strata", W: sz, H: sz, RGBA: make([]uint8, sz*sz*4), Bounds: b,
		Values: make([]float32, sz*sz)}
	u := res.Masks[terrain.MaskStrataHeight]
	for k, idx := range res.StrataIndex {
		c := [3]float64{0.6, 0.6, 0.6}
		if int(idx) < len(s.Strata.Layers) {
			c = s.Strata.Layers[idx].Color
		}
		f := 0.75 + 0.25*float64(u[k])
		im.RGBA[4*k] = uint8(min(c[0]*f, 1) * 255)
		im.RGBA[4*k+1] = uint8(min(c[1]*f, 1) * 255)
		im.RGBA[4*k+2] = uint8(min(c[2]*f, 1) * 255)
		im.RGBA[4*k+3] = 255
		im.Values[k] = float32(idx)
	}
	return im
}

func rgbToRGBA(rgb []uint8) []uint8 {
	out := make([]uint8, len(rgb)/3*4)
	for k := 0; k < len(rgb)/3; k++ {
		out[4*k], out[4*k+1], out[4*k+2], out[4*k+3] = rgb[3*k], rgb[3*k+1], rgb[3*k+2], 255
	}
	return out
}

func loadTerrain(res *procgen.StageResult) (any, error) {
	return terrain.Load(res.Dir)
}
