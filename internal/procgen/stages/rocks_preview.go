package stages

// rocks_preview.go — the rocks stage's preview: one variant (preview_variant)
// made as the run makes it, at no more than preview_triangles, shown on the
// 3D view's turntable with its baked colours, its triangle count and where
// its time went in the notes. The 2D view shows its footprint: the bounding
// rectangle, the footprint and bounding circles.

import (
	"fmt"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/rocks"
)

// RockPreviewMesh is a rock as a preview mesh (turntable, coloured).
func RockPreviewMesh(name string, m *rocks.Mesh) procgen.PreviewMesh {
	pm := procgen.PreviewMesh{Name: name, Turntable: true, Indices: m.Indices, Colors: m.Colors,
		Positions: m.Positions, Normals: m.Normals, UVs: m.UVs}
	return pm
}

func previewRocks(pc *procgen.PreviewContext) (*procgen.PreviewData, error) {
	p := pc.Params
	s := rocksSettings(&pc.Env)
	_, _, _, ptris := rocks.PresetDoc(s.Preset)
	full := s.Triangles
	if full <= 0 {
		full = ptris
	}
	s.Triangles = min(full, p.Int("preview_triangles"))
	s.Workers = pc.Workers
	s.Progress = pc.Progress
	vi := min(p.Int("preview_variant"), p.Int("variants")-1)
	t0 := time.Now()
	r, err := rocks.Generate(s, variantSeed(pc.Seed, vi))
	if err != nil {
		return nil, err
	}
	name := variantName(s.Preset, vi)
	out := &procgen.PreviewData{Meshes: []procgen.PreviewMesh{RockPreviewMesh(name, r.Mesh)}, Artifact: r}
	m := r.Meta
	b := procgen.Rect{MinX: float64(m.Min[0]), MinZ: float64(m.Min[2]), MaxX: float64(m.Max[0]), MaxZ: float64(m.Max[2])}
	out.Bounds = procgen.Rect{MinX: b.MinX - 0.2*float64(m.Radius), MinZ: b.MinZ - 0.2*float64(m.Radius),
		MaxX: b.MaxX + 0.2*float64(m.Radius), MaxZ: b.MaxZ + 0.2*float64(m.Radius)}
	out.Lines = append(out.Lines, procgen.LineLayer{Name: "Bounds", Closed: true, Color: [4]float32{1, 1, 1, 0.5},
		Points: [][2]float64{{b.MinX, b.MinZ}, {b.MaxX, b.MinZ}, {b.MaxX, b.MaxZ}, {b.MinX, b.MaxZ}}})
	out.Points = append(out.Points,
		procgen.PointLayer{Name: "Radius", Points: [][2]float64{{0, 0}}, Radii: []float64{float64(m.Radius)}, Color: [4]float32{0.6, 0.8, 1, 0.5}},
		procgen.PointLayer{Name: "Footprint", Points: [][2]float64{{0, 0}}, Radii: []float64{float64(m.FootprintRadius)}, Color: [4]float32{1, 0.7, 0.3, 0.9}})
	tm := r.Timings
	out.Notes = append(out.Notes,
		fmt.Sprintf("%s: %d triangles (%d meshed at %.1f cm; the run makes %d)", name, r.Mesh.TriangleCount(), tm.MeshTriangles, tm.Cell*100, full),
		fmt.Sprintf("%.2f × %.2f × %.2f m, footprint radius %.2f m, top %.2f m", m.Extent[0], m.Extent[1], m.Extent[2], m.FootprintRadius, m.Top),
		fmt.Sprintf("mesh %s, decimate %s, bake %s, total %s", tm.Mesh.Round(time.Millisecond), tm.Simplify.Round(time.Millisecond),
			tm.Bake.Round(time.Millisecond), time.Since(t0).Round(time.Millisecond)))
	return out, nil
}
