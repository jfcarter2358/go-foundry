package world

// surface_mesh_raw.go — a surface mesh's on-disk form.
//
// The conversion lives here rather than in levelio because it is the mesh's own
// business what a valid one looks like, and because the loader has to be able
// to reject a malformed one without knowing anything about faces and rings. A
// mesh read off disk is untrusted input: point indices are just integers in a
// file, and a face pointing past the end of the pool would panic somewhere deep
// in the triangulator rather than at the edge where it can be reported.

import "github.com/go-gl/mathgl/mgl32"

// SurfaceMeshToRaw flattens a mesh for writing, returning nil for an empty one
// so a flat surface writes no `mesh:` key at all.
func SurfaceMeshToRaw(m *SurfaceMesh) *RawSurfaceMesh {
	if m.IsEmpty() {
		return nil
	}
	out := &RawSurfaceMesh{
		Points:   make([]float32, 0, len(m.Points)*3),
		Boundary: make([]int, 0, len(m.Points)),
		Faces:    make([]RawSurfaceFace, 0, len(m.Faces)),
	}
	for _, p := range m.Points {
		out.Points = append(out.Points, p.U, p.V, p.Depth)
		out.Boundary = append(out.Boundary, p.Boundary)
	}
	for i := range m.Faces {
		f := &m.Faces[i]
		rf := RawSurfaceFace{
			Verts:   append([]int(nil), f.Verts...),
			Texture: f.Texture,
			Alpha:   f.Alpha,
			Hole:    f.Hole,
			Smooth:  f.Smooth,
		}
		for _, uv := range f.UVs {
			rf.UVs = append(rf.UVs, uv.X(), uv.Y())
		}
		for _, h := range f.Holes {
			rf.Holes = append(rf.Holes, append([]int(nil), h...))
		}
		out.Faces = append(out.Faces, rf)
	}
	return out
}

// SurfaceMeshFromRaw rebuilds a mesh from its on-disk form, dropping anything
// malformed rather than failing the load.
//
// Dropping rather than erroring, because the alternative is a level that will
// not open at all over one bad face — and a mesh is recoverable in a way a
// level file is not: a missing face is visible, selectable and re-cuttable in
// the editor, while a level that refuses to load leaves an author with nothing.
// A face indexing past the point pool is the failure this actually guards, and
// it can only come from a hand-edited file or a bug on the way out.
func SurfaceMeshFromRaw(r *RawSurfaceMesh) SurfaceMesh {
	if r == nil || len(r.Points) < 9 || len(r.Faces) == 0 {
		return SurfaceMesh{}
	}
	n := len(r.Points) / 3
	m := SurfaceMesh{Points: make([]SurfacePoint, n)}
	for i := 0; i < n; i++ {
		b := SurfaceFree
		if i < len(r.Boundary) {
			b = r.Boundary[i]
		}
		m.Points[i] = SurfacePoint{
			U: r.Points[i*3], V: r.Points[i*3+1], Depth: r.Points[i*3+2],
			Boundary: b,
		}
	}
	for i := range r.Faces {
		rf := &r.Faces[i]
		if !surfaceRingInRange(rf.Verts, n) {
			continue
		}
		f := SurfaceFace{
			Verts:   append([]int(nil), rf.Verts...),
			Texture: rf.Texture,
			Alpha:   rf.Alpha,
			Hole:    rf.Hole,
			Smooth:  rf.Smooth,
		}
		// A partial UV list is worse than none: it would pair the first few
		// corners with authored coordinates and the rest with derived ones.
		if len(rf.UVs) == len(rf.Verts)*2 {
			f.UVs = make([]mgl32.Vec2, len(rf.Verts))
			for j := range f.UVs {
				f.UVs[j] = mgl32.Vec2{rf.UVs[j*2], rf.UVs[j*2+1]}
			}
		}
		for _, h := range rf.Holes {
			if surfaceRingInRange(h, n) {
				f.Holes = append(f.Holes, append([]int(nil), h...))
			}
		}
		m.Faces = append(m.Faces, f)
	}
	if len(m.Faces) == 0 {
		return SurfaceMesh{}
	}
	return m
}

func surfaceRingInRange(ring []int, n int) bool {
	if len(ring) < 3 {
		return false
	}
	for _, v := range ring {
		if v < 0 || v >= n {
			return false
		}
	}
	return true
}
