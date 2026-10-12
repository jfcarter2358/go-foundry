package stages

// cliffwall_back.go — the chunks' back profiles for cliff_wall's silhouette
// seating (place/backprofile.go, wall.go).
//
// A profile is measured off the chunk's cooked FALLBACK mesh: the DAG cut at
// a middle level the cook keeps beside the pages, a few thousand triangles,
// which is detail enough for a 24² grid across a back several metres wide
// and costs a few milliseconds a variant. A variant whose mesh cannot be read
// (a .glb output with cook off, a missing file) keeps no profile and is
// seated by its box, as before.

import (
	"sort"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/nanite"
	"foundry/internal/procgen"
	"foundry/internal/procgen/place"
)

// backProfileCells is the profile's grid a side.
const backProfileCells = 24

// profileVariants measures every backed variant's back profile in place.
func profileVariants(rc *procgen.RunContext, vs []place.Variant) {
	n := 0
	for i := range vs {
		v := &vs[i]
		if !v.Back || !strings.HasSuffix(v.Model, ".fnm") {
			continue
		}
		m, err := nanite.Load(fileutil.ResolveAssetPath(v.Model))
		if err != nil {
			rc.Logf("%s: no back profile (%v): seated by its box", v.Name, err)
			continue
		}
		verts, normals, idx := m.FallbackVertices, m.FallbackNormals, m.Fallback
		if len(idx) == 0 {
			if err := m.Flat(); err != nil {
				continue
			}
			verts, normals, idx = m.Vertices, m.Normals, m.Indices
		}
		pos := make([][3]float64, len(verts)/5)
		nrm := make([][3]float64, 0, len(pos))
		for k := range pos {
			pos[k] = [3]float64{float64(verts[5*k]), float64(verts[5*k+1]), float64(verts[5*k+2])}
			if len(normals) == 3*len(pos) {
				nrm = append(nrm, [3]float64{float64(normals[3*k]), float64(normals[3*k+1]), float64(normals[3*k+2])})
			}
		}
		tris := make([][3]int, len(idx)/3)
		for k := range tris {
			tris[k] = [3]int{int(idx[3*k]), int(idx[3*k+1]), int(idx[3*k+2])}
		}
		v.BackProfile = place.MeasureBack(v, pos, nrm, tris, backProfileCells, backProfileCells)
		if bp := v.BackProfile; bp != nil {
			open := 0
			gaps := make([]float64, 0, len(bp.Outline))
			for _, c := range bp.Outline {
				if c.Gap > 0 {
					open++
				}
				gaps = append(gaps, c.Gap)
			}
			sort.Float64s(gaps)
			q := func(f float64) float64 { return gaps[min(int(f*float64(len(gaps))), len(gaps)-1)] }
			rc.Logf("%s (%.1f × %.1f × %.1f m): back outline %d cells, %d open; gap median %.2f, 75%% %.2f, 90%% %.2f, max %.2f m",
				v.Name, v.Extent[0], v.Extent[1], v.Extent[2], len(bp.Outline), open, q(0.5), q(0.75), q(0.9), gaps[len(gaps)-1])
		}
		if v.BackProfile != nil {
			n++
		}
	}
	if n > 0 {
		rc.Logf("back profiles measured for %d chunk variants", n)
	}
}
