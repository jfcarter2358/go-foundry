package geom

// smooth.go — smooth shading for world surfaces.
//
// Geometry carries no normals: the fragment shader recovers a flat facet normal
// from screen-space derivatives of the world position (see fragment.glsl). That
// is exactly right for a flat quad and exactly wrong for anything meant to read
// as curved — a terrain floor, a sphere, a bevelled extrusion — where every
// triangle lights as its own facet with a hard step at each shared edge.
//
// Turning Smooth on for a surface makes Geometry.Load generate per-vertex
// normals here and upload them beside the positions; the shader interpolates
// them across the triangle, so the lighting varies continuously instead of
// stepping. Vertices are welded by position so the triangles meeting at a point
// share one normal — but only across edges whose two facets are within
// SmoothAngle of each other, so a genuine crease (a cube corner, the lip of an
// extrusion, the rim of a cylinder) stays sharp rather than smearing.
//
// Displaced procedural detail smooths a different way: its surface is generated
// on the GPU and has no CPU-side vertices to weld, so the tessellation stage
// differentiates the displacement field analytically instead. Both are driven by
// the same per-surface Smooth flag — see detailNormal() in detail_common.glsl.

import "math"

// DefaultSmoothAngle is the crease threshold used when a surface enables
// smoothing without picking one. 60° keeps a box's corners sharp while merging
// the gentle steps of a terrain mesh or a subdivided sphere.
const DefaultSmoothAngle = float32(60)

// smoothAngleOrDefault normalizes a stored smoothing angle. The upper clamp is
// just under 180 so that back-to-back facets (the two sides of a double-sided
// floor, which occupy the same positions with opposite windings) always fail the
// crease test instead of averaging each other out to nothing.
func smoothAngleOrDefault(a float32) float32 {
	if a <= 0 {
		return DefaultSmoothAngle
	}
	if a > 179 {
		return 179
	}
	return a
}

// smoothWeldKey quantizes a position so vertices that are meant to be the same
// point weld together despite float drift. Matches the 1e-4 grid
// computeBoundaryEdges uses, so both agree on which triangles share a vertex.
type smoothWeldKey [3]int64

func smoothWeld(x, y, z float32) smoothWeldKey {
	return smoothWeldKey{
		int64(math.Round(float64(x) * 1e4)),
		int64(math.Round(float64(y) * 1e4)),
		int64(math.Round(float64(z) * 1e4)),
	}
}

// ComputeSmoothNormals returns one unit normal per entry in indices — i.e. one
// per de-indexed corner, parallel to the 5-float vertex records Geometry.Load
// builds — so the result can be uploaded as its own buffer without disturbing
// the position/UV layout every other reader assumes.
//
// vertices supplies the indexed vertex pool ([X,Y,Z,U,V] each; only XYZ is read)
// and indices the triangle list. angleDeg is the crease threshold in degrees;
// 0 means "use the default".
func ComputeSmoothNormals(vertices [][]float32, indices []int, angleDeg float32) []float32 {
	triCount := len(indices) / 3
	if triCount == 0 {
		return nil
	}

	pos := func(i int) vec3 {
		v := vertices[indices[i]]
		return vec3{v[0], v[1], v[2]}
	}

	// Face normals are left unnormalized: their length is twice the triangle
	// area, which is exactly the weight a shared vertex should give each
	// neighbour — a sliver contributes proportionally less than a broad face.
	faceN := make([]vec3, triCount)
	for t := 0; t < triCount; t++ {
		a, b, c := pos(t*3), pos(t*3+1), pos(t*3+2)
		faceN[t] = cross3(sub3(b, a), sub3(c, a))
	}

	// Which triangles touch each welded position.
	adj := make(map[smoothWeldKey][]int32, triCount*3)
	for t := 0; t < triCount; t++ {
		for c := 0; c < 3; c++ {
			p := pos(t*3 + c)
			k := smoothWeld(p[0], p[1], p[2])
			adj[k] = append(adj[k], int32(t))
		}
	}

	cosLimit := float32(math.Cos(float64(smoothAngleOrDefault(angleDeg)) * math.Pi / 180))

	out := make([]float32, 0, len(indices)*3)
	for t := 0; t < triCount; t++ {
		own := normalize3(faceN[t])
		for c := 0; c < 3; c++ {
			p := pos(t*3 + c)
			var acc vec3
			for _, o := range adj[smoothWeld(p[0], p[1], p[2])] {
				n := faceN[o]
				if int(o) != t && dot3(normalize3(n), own) < cosLimit {
					continue // crease — this neighbour lights as its own facet
				}
				acc = add3(acc, n)
			}
			n := normalize3(acc)
			if n == (vec3{}) {
				// Everything cancelled (or the triangle is degenerate): fall back
				// to the facet normal, which is what no smoothing would have used.
				n = own
			}
			out = append(out, n[0], n[1], n[2])
		}
	}
	return out
}

// Small vector helpers, kept local so this file has no dependency on the GL
// math package for what is plain arithmetic over the raw vertex buffer.
type vec3 [3]float32

func sub3(a, b vec3) vec3 { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func add3(a, b vec3) vec3 { return vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }

func cross3(a, b vec3) vec3 {
	return vec3{
		a[1]*b[2] - a[2]*b[1],
		a[2]*b[0] - a[0]*b[2],
		a[0]*b[1] - a[1]*b[0],
	}
}

func dot3(a, b vec3) float32 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

func normalize3(v vec3) vec3 {
	l := float32(math.Sqrt(float64(dot3(v, v))))
	if l < 1e-12 {
		return vec3{}
	}
	return vec3{v[0] / l, v[1] / l, v[2] / l}
}
